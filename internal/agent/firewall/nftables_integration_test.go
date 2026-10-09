//go:build linux && nftintegration

// Run in the Linux VM or in CI as root. Build as the normal user, run as root:
//
//	go test -c -tags nftintegration -o /tmp/firewall.test ./internal/agent/firewall/
//	sudo /tmp/firewall.test -test.v -test.count=1 -test.run NFTables
//
// Every test works in its own fresh network namespace and never touches the
// host ruleset.
package firewall_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/banrules"
	"mcpanel/internal/agent/firewall"
)

// newNetNS creates a fresh network namespace and returns an fd referring to
// it. Namespace switches are per OS thread: the thread is locked, unshared,
// and switched back. If switching back fails, t.Fatalf ends the goroutine
// with the thread still locked and Go discards that thread.
func newNetNS(t *testing.T) int {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("nftintegration tests need root (CAP_SYS_ADMIN for unshare, CAP_NET_ADMIN for nftables)")
	}
	runtime.LockOSThread()
	orig, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("open current netns: %v", err)
	}
	defer orig.Close()
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("unshare(CLONE_NEWNET): %v", err)
	}
	ns, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatalf("open new netns: %v", err)
	}
	if err := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); err != nil { //nolint:gosec // G115: fd fits into int
		t.Fatalf("restore netns: %v", err)
	}
	runtime.UnlockOSThread()
	t.Cleanup(func() { _ = ns.Close() })
	return int(ns.Fd()) //nolint:gosec // G115: fd fits into int
}

func newBackend(t *testing.T, fd int) *firewall.NFTables {
	t.Helper()
	fw, err := firewall.NewNFTables(slog.New(slog.NewTextHandler(io.Discard, nil)), nftables.WithNetNSFd(fd))
	if err != nil {
		t.Fatalf("NewNFTables: %v", err)
	}
	return fw
}

func rawConn(t *testing.T, fd int) *nftables.Conn {
	t.Helper()
	c, err := nftables.New(nftables.WithNetNSFd(fd))
	if err != nil {
		t.Fatalf("nftables.New: %v", err)
	}
	return c
}

func listBans(t *testing.T, fw *firewall.NFTables) []agentapi.Ban {
	t.Helper()
	bans, err := fw.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return bans
}

func assertPrefixes(t *testing.T, got []agentapi.Ban, want []string) {
	t.Helper()
	var prefixes []string
	for _, b := range got {
		prefixes = append(prefixes, b.Prefix)
	}
	if !slices.Equal(prefixes, want) {
		t.Fatalf("bans = %v, want %v", prefixes, want)
	}
}

func TestNFTablesSetupIsIdempotentAndScoped(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	other := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: "othertable"})
	c.AddChain(&nftables.Chain{Name: "keepme", Table: other})
	if err := c.Flush(); err != nil {
		t.Fatalf("create foreign table: %v", err)
	}

	newBackend(t, fd)
	newBackend(t, fd) // a second start must not duplicate anything

	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name)
	}
	if !slices.Contains(names, firewall.TableName) || !slices.Contains(names, "othertable") {
		t.Fatalf("tables = %v", names)
	}
	if _, err := c.ListChain(other, "keepme"); err != nil {
		t.Fatalf("foreign chain was touched: %v", err)
	}

	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: firewall.TableName}
	chain, err := c.ListChain(table, firewall.ChainName)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Type != nftables.ChainTypeFilter ||
		chain.Hooknum == nil || *chain.Hooknum != *nftables.ChainHookInput ||
		chain.Priority == nil || *chain.Priority != nftables.ChainPriority(firewall.ChainPriority) ||
		chain.Policy == nil || *chain.Policy != nftables.ChainPolicyAccept {
		t.Fatalf("chain = %+v", chain)
	}

	rules, err := c.GetRules(table, &nftables.Chain{Name: firewall.ChainName, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want exactly 2", len(rules))
	}
	for i, want := range []string{firewall.Set4Name, firewall.Set6Name} {
		var lookup, drop bool
		for _, e := range rules[i].Exprs {
			switch e := e.(type) {
			case *expr.Lookup:
				lookup = e.SetName == want
			case *expr.Verdict:
				drop = e.Kind == expr.VerdictDrop
			}
		}
		if !lookup || !drop {
			t.Errorf("rule %d does not drop @%s: %#v", i, want, rules[i].Exprs)
		}
	}

	s4, err := c.GetSetByName(table, firewall.Set4Name)
	if err != nil {
		t.Fatal(err)
	}
	if !s4.HasTimeout || s4.Interval || s4.KeyType.Name != nftables.TypeIPAddr.Name {
		t.Errorf("set4 = %+v", s4)
	}
	s6, err := c.GetSetByName(table, firewall.Set6Name)
	if err != nil {
		t.Fatal(err)
	}
	if !s6.HasTimeout || !s6.Interval || s6.KeyType.Name != nftables.TypeIP6Addr.Name {
		t.Errorf("set6 = %+v", s6)
	}
}

func TestNFTablesBanListUnban(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	durations := map[string]time.Duration{
		"203.0.113.7/32":    15 * time.Minute,
		"2001:db8:1:2::/64": time.Hour,
		"2001:db8:1:3::/64": 30 * time.Minute, // adjacent to the previous /64
		"2001:db8:9::1/128": 10 * time.Minute,
	}
	for s, d := range durations {
		if err := fw.Ban(ctx, netip.MustParsePrefix(s), d); err != nil {
			t.Fatalf("Ban %s: %v", s, err)
		}
	}
	got := listBans(t, fw)
	assertPrefixes(t, got, []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8:1:3::/64", "2001:db8:9::1/128"})
	for _, b := range got {
		want := banrules.RemainingSeconds(durations[b.Prefix])
		if b.ExpiresInSeconds > want || b.ExpiresInSeconds < want-10 {
			t.Errorf("%s expires in %ds, want about %ds", b.Prefix, b.ExpiresInSeconds, want)
		}
	}

	if err := fw.Unban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64")); err != nil {
		t.Fatalf("Unban: %v", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"203.0.113.7/32", "2001:db8:1:3::/64", "2001:db8:9::1/128"})
	if err := fw.Unban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64")); err != nil {
		t.Fatalf("second Unban must be a no-op: %v", err)
	}
	if err := fw.Unban(ctx, netip.MustParsePrefix("203.0.113.7/32")); err != nil {
		t.Fatalf("Unban v4: %v", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"2001:db8:1:3::/64", "2001:db8:9::1/128"})
}

func TestNFTablesRebanRefreshesTimeout(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	for _, s := range []string{"203.0.113.7/32", "2001:db8:1:2::/64"} {
		p := netip.MustParsePrefix(s)
		if err := fw.Ban(ctx, p, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := fw.Ban(ctx, p, 2*time.Hour); err != nil {
			t.Fatalf("re-ban %s: %v", s, err)
		}
	}
	got := listBans(t, fw)
	if len(got) != 2 {
		t.Fatalf("got %d bans, want 2: %+v", len(got), got)
	}
	for _, b := range got {
		if b.ExpiresInSeconds < 7190 {
			t.Errorf("%s: timeout not refreshed, expires in %ds", b.Prefix, b.ExpiresInSeconds)
		}
	}
	if err := fw.Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if b := listBans(t, fw)[0]; b.Prefix != "203.0.113.7/32" || b.ExpiresInSeconds > 30 {
		t.Errorf("shorter re-ban must replace the timeout: %+v", b)
	}
}

func TestNFTablesRejectsOverlap(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64"), time.Hour); err != nil {
		t.Fatal(err)
	}
	err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::1/128"), time.Hour)
	if !errors.Is(err, banrules.ErrConflict) {
		t.Fatalf("overlapping ban = %v, want ErrConflict", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"2001:db8:1:2::/64"})
}

func TestNFTablesExpiry(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	p4 := netip.MustParsePrefix("203.0.113.7/32")
	for _, p := range []netip.Prefix{p4, netip.MustParsePrefix("2001:db8:1:2::/64")} {
		if err := fw.Ban(ctx, p, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2500 * time.Millisecond)
	assertPrefixes(t, listBans(t, fw), nil)
	if err := fw.Ban(ctx, p4, time.Hour); err != nil {
		t.Fatalf("ban after expiry: %v", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"203.0.113.7/32"})
}

func TestNFTablesConcurrent(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range byte(20) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p4 := netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, i}), 32)
			p6 := netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, i}), 64)
			for _, p := range []netip.Prefix{p4, p6} {
				if err := fw.Ban(ctx, p, time.Hour); err != nil {
					t.Errorf("Ban %s: %v", p, err)
					return
				}
			}
			if i%2 == 0 {
				for _, p := range []netip.Prefix{p4, p6} {
					if err := fw.Unban(ctx, p); err != nil {
						t.Errorf("Unban %s: %v", p, err)
					}
				}
			}
		}()
	}
	wg.Wait()
	if got := listBans(t, fw); len(got) != 20 {
		t.Fatalf("got %d bans, want 20 (10 IPv4 + 10 IPv6)", len(got))
	}
}

func TestNFTablesBansSurviveAgentRestart(t *testing.T) {
	fd := newNetNS(t)
	fw := newBackend(t, fd)
	if err := fw.Ban(context.Background(), netip.MustParsePrefix("203.0.113.7/32"), time.Hour); err != nil {
		t.Fatal(err)
	}
	restarted := newBackend(t, fd) // setup flushes only the chain, never the sets
	assertPrefixes(t, listBans(t, restarted), []string{"203.0.113.7/32"})
}
