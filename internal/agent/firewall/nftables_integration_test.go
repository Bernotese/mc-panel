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
	"fmt"
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

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
	"github.com/Bernotese/mc-panel/internal/config"
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

// newBackend starts the backend with the default container egress deny list.
func newBackend(t *testing.T, fd int) *firewall.NFTables {
	t.Helper()
	return newEgressBackend(t, fd, config.DefaultAgent().EgressDenyPrefixes())
}

func newEgressBackend(t *testing.T, fd int, deny []netip.Prefix) *firewall.NFTables {
	t.Helper()
	fw, err := firewall.NewNFTables(slog.New(slog.NewTextHandler(io.Discard, nil)), deny, nftables.WithNetNSFd(fd))
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
		var ctState, lookup, drop bool
		for _, e := range rules[i].Exprs {
			switch e := e.(type) {
			case *expr.Ct:
				ctState = e.Key == expr.CtKeySTATE
			case *expr.Lookup:
				lookup = e.SetName == want
			case *expr.Verdict:
				drop = e.Kind == expr.VerdictDrop
			}
		}
		if !ctState || !lookup || !drop {
			t.Errorf("rule %d does not drop new connections from @%s: %#v", i, want, rules[i].Exprs)
		}
	}

	s4, err := c.GetSetByName(table, firewall.Set4Name)
	if err != nil {
		t.Fatal(err)
	}
	if !s4.HasTimeout || s4.Interval || s4.KeyType.Name != nftables.TypeIPAddr.Name || s4.Size != firewall.SetSize {
		t.Errorf("set4 = %+v", s4)
	}
	s6, err := c.GetSetByName(table, firewall.Set6Name)
	if err != nil {
		t.Fatal(err)
	}
	if !s6.HasTimeout || !s6.Interval || s6.KeyType.Name != nftables.TypeIP6Addr.Name || s6.Size != firewall.SetSize {
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

// Sets created by an older agent without a size limit are recreated on startup.
func TestNFTablesMigratesUnsizedSets(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	table := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: firewall.TableName})
	old4 := &nftables.Set{Table: table, Name: firewall.Set4Name, KeyType: nftables.TypeIPAddr, HasTimeout: true}
	old6 := &nftables.Set{Table: table, Name: firewall.Set6Name, KeyType: nftables.TypeIP6Addr, HasTimeout: true, Interval: true}
	if err := c.AddSet(old4, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.AddSet(old6, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(); err != nil {
		t.Fatalf("create old sets: %v", err)
	}
	newBackend(t, fd)
	for _, name := range []string{firewall.Set4Name, firewall.Set6Name} {
		s, err := c.GetSetByName(table, name)
		if err != nil {
			t.Fatal(err)
		}
		if s.Size != firewall.SetSize {
			t.Fatalf("%s size = %d, want %d", name, s.Size, firewall.SetSize)
		}
	}
	newBackend(t, fd) // and a restart with current sets is a no-op
}

var mcTable = &nftables.Table{Family: nftables.TableFamilyINet, Name: firewall.TableName}

// egressRules returns the rules of the forward chain; nil and an error if it is missing.
func egressRules(t *testing.T, c *nftables.Conn) ([]*nftables.Rule, error) {
	t.Helper()
	if _, err := c.ListChain(mcTable, firewall.EgressChainName); err != nil {
		return nil, err
	}
	return c.GetRules(mcTable, &nftables.Chain{Name: firewall.EgressChainName, Table: mcTable})
}

// setKeys returns the start keys of an interval set as prefixes' addresses.
func setKeys(t *testing.T, c *nftables.Conn, name string) []string {
	t.Helper()
	s, err := c.GetSetByName(mcTable, name)
	if err != nil {
		t.Fatalf("set %s: %v", name, err)
	}
	els, err := c.GetSetElements(s)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range els {
		if !e.IntervalEnd {
			a, _ := netip.AddrFromSlice(e.Key)
			keys = append(keys, a.String())
		}
	}
	slices.Sort(keys)
	return keys
}

func TestNFTablesEgressFilter(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	fw := newBackend(t, fd)

	chain, err := c.ListChain(mcTable, firewall.EgressChainName)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Type != nftables.ChainTypeFilter ||
		chain.Hooknum == nil || *chain.Hooknum != *nftables.ChainHookForward ||
		chain.Priority == nil || *chain.Priority != nftables.ChainPriority(firewall.ChainPriority) ||
		chain.Policy == nil || *chain.Policy != nftables.ChainPolicyAccept {
		t.Fatalf("forward chain = %+v", chain)
	}
	if got, want := setKeys(t, c, firewall.EgressSet4Name), []string{"10.0.0.0", "100.64.0.0", "168.63.129.16", "169.254.0.0", "172.16.0.0", "192.168.0.0"}; !slices.Equal(got, want) {
		t.Fatalf("deny4 = %v, want %v", got, want)
	}
	if got, want := setKeys(t, c, firewall.EgressSet6Name), []string{"fc00::", "fe80::"}; !slices.Equal(got, want) {
		t.Fatalf("deny6 = %v, want %v", got, want)
	}
	if rules, err := egressRules(t, c); err != nil || len(rules) != 0 {
		t.Fatalf("before the first subnet sync: %d rules, %v", len(rules), err)
	}

	subnets := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16"), netip.MustParsePrefix("fd00:1::/64")}
	for range 2 { // idempotent: replaced, never duplicated
		if err := fw.SetEgressSubnets(context.Background(), subnets); err != nil {
			t.Fatalf("SetEgressSubnets: %v", err)
		}
	}
	rules, err := egressRules(t, c)
	if err != nil || len(rules) != 2 {
		t.Fatalf("got %d rules (%v), want 2", len(rules), err)
	}
	for i, want := range []string{firewall.EgressSet4Name, firewall.EgressSet6Name} {
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
			t.Errorf("rule %d does not drop to @%s: %#v", i, want, rules[i].Exprs)
		}
	}

	// Agent restart: the rules for the running containers stay until the next sync,
	// and the jail is untouched.
	newBackend(t, fd)
	if rules, err := egressRules(t, c); err != nil || len(rules) != 2 {
		t.Fatalf("after restart: %d rules, %v", len(rules), err)
	}
	if jail, err := c.GetRules(mcTable, &nftables.Chain{Name: firewall.ChainName, Table: mcTable}); err != nil || len(jail) != 2 {
		t.Fatalf("jail chain: %d rules, %v", len(jail), err)
	}

	// A changed deny list replaces the set elements on the next start.
	newEgressBackend(t, fd, []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	if got := setKeys(t, c, firewall.EgressSet4Name); !slices.Equal(got, []string{"10.0.0.0"}) {
		t.Fatalf("deny4 after config change = %v", got)
	}
	if got := setKeys(t, c, firewall.EgressSet6Name); len(got) != 0 {
		t.Fatalf("deny6 after config change = %v", got)
	}

	// Subnets shrink with the network: only the remaining one keeps a rule.
	if err := fw.SetEgressSubnets(context.Background(), subnets[:1]); err != nil {
		t.Fatal(err)
	}
	if rules, err := egressRules(t, c); err != nil || len(rules) != 1 {
		t.Fatalf("after shrink: %d rules, %v", len(rules), err)
	}

	// container_egress_deny = [] removes chain and sets; the jail stays.
	off := newEgressBackend(t, fd, nil)
	if _, err := egressRules(t, c); err == nil {
		t.Fatal("forward chain still exists with an empty deny list")
	}
	for _, name := range []string{firewall.EgressSet4Name, firewall.EgressSet6Name} {
		if _, err := c.GetSetByName(mcTable, name); err == nil {
			t.Fatalf("set %s still exists with an empty deny list", name)
		}
	}
	if err := off.SetEgressSubnets(context.Background(), subnets); err != nil {
		t.Fatalf("disabled filter: %v", err)
	}
	if _, err := egressRules(t, c); err == nil {
		t.Fatal("a disabled filter must not create the chain")
	}
	newEgressBackend(t, fd, nil) // removing twice is fine
	if jail, err := c.GetRules(mcTable, &nftables.Chain{Name: firewall.ChainName, Table: mcTable}); err != nil || len(jail) != 2 {
		t.Fatalf("jail chain: %d rules, %v", len(jail), err)
	}
}

// TestNFTablesEgressSelfRepair: after an external "nft flush ruleset" (or a deleted table)
// EgressInPlace reports it, and SetEgressSubnets brings table, jail, chain, sets, elements
// and rules back in one batch.
func TestNFTablesEgressSelfRepair(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	fw := newBackend(t, fd)
	ctx := context.Background()
	subnets := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	if ok, err := fw.EgressInPlace(ctx, subnets); err != nil || ok {
		t.Fatalf("before SetEgressSubnets: in place = %v, %v; want false (no rule yet)", ok, err)
	}
	if err := fw.SetEgressSubnets(ctx, subnets); err != nil {
		t.Fatal(err)
	}
	if ok, err := fw.EgressInPlace(ctx, subnets); err != nil || !ok {
		t.Fatalf("after SetEgressSubnets: in place = %v, %v", ok, err)
	}
	if ok, _ := fw.EgressInPlace(ctx, append(subnets, netip.MustParsePrefix("fd00:1::/64"))); ok {
		t.Fatal("a missing rule for a second subnet must count as not in place")
	}

	for name, damage := range map[string]func(){
		"table deleted": func() { c.DelTable(mcTable) },
		"set flushed":   func() { c.FlushSet(&nftables.Set{Table: mcTable, Name: firewall.EgressSet4Name}) },
		"chain flushed": func() { c.FlushChain(&nftables.Chain{Table: mcTable, Name: firewall.EgressChainName}) },
	} {
		damage()
		if err := c.Flush(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if ok, err := fw.EgressInPlace(ctx, subnets); err != nil || ok {
			t.Fatalf("%s: in place = %v, %v; want false", name, ok, err)
		}
		if err := fw.SetEgressSubnets(ctx, subnets); err != nil {
			t.Fatalf("%s: repair: %v", name, err)
		}
		if ok, err := fw.EgressInPlace(ctx, subnets); err != nil || !ok {
			t.Fatalf("%s: after repair: in place = %v, %v", name, ok, err)
		}
		if rules, err := egressRules(t, c); err != nil || len(rules) != 1 {
			t.Fatalf("%s: %d egress rules, %v", name, len(rules), err)
		}
		if got := setKeys(t, c, firewall.EgressSet4Name); len(got) != 6 {
			t.Fatalf("%s: deny4 = %v", name, got)
		}
		if jail, err := c.GetRules(mcTable, &nftables.Chain{Name: firewall.ChainName, Table: mcTable}); err != nil || len(jail) != 2 {
			t.Fatalf("%s: jail chain: %d rules, %v", name, len(jail), err)
		}
	}

	// Fail closed: an unusable subnet changes nothing and is an error.
	if err := fw.SetEgressSubnets(ctx, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}); err == nil {
		t.Fatal("SetEgressSubnets(0.0.0.0/0) = nil, want an error")
	}
	if err := fw.SetEgressSubnets(ctx, nil); err == nil {
		t.Fatal("SetEgressSubnets(none) = nil, want an error")
	}
	if rules, err := egressRules(t, c); err != nil || len(rules) != 1 {
		t.Fatalf("after refused calls: %d egress rules, %v", len(rules), err)
	}
}

// TestNFTablesEgressManyInstanceSubnets (SEC-03): one rule per network, for the router's
// network and many small per-server networks; adding and removing a server's network
// rewrites the rules, and a missing one counts as not in place.
func TestNFTablesEgressManyInstanceSubnets(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	fw := newBackend(t, fd)
	ctx := context.Background()
	subnets := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	for i := range 64 {
		subnets = append(subnets, netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 213, byte(i / 16), byte(i % 16 * 16)}), 28))
	}
	if err := fw.SetEgressSubnets(ctx, subnets); err != nil {
		t.Fatal(err)
	}
	rules, err := egressRules(t, c)
	if err != nil || len(rules) != len(subnets) {
		t.Fatalf("got %d rules (%v), want %d", len(rules), err, len(subnets))
	}
	// Each rule matches its own network as source (the first compare holds the address).
	var sources []netip.Addr
	for _, r := range rules {
		for _, e := range r.Exprs {
			if cmp, ok := e.(*expr.Cmp); ok && cmp.Op == expr.CmpOpEq && len(cmp.Data) == 4 {
				a, _ := netip.AddrFromSlice(cmp.Data)
				sources = append(sources, a)
				break
			}
		}
	}
	for _, p := range subnets {
		if !slices.Contains(sources, p.Addr()) {
			t.Fatalf("no rule for %s (sources %v)", p, sources)
		}
	}
	if ok, err := fw.EgressInPlace(ctx, subnets); err != nil || !ok {
		t.Fatalf("in place = %v, %v", ok, err)
	}
	more := append(slices.Clone(subnets), netip.MustParsePrefix("10.213.4.0/28"))
	if ok, _ := fw.EgressInPlace(ctx, more); ok {
		t.Fatal("a new server's network without a rule must count as not in place")
	}
	if err := fw.SetEgressSubnets(ctx, more); err != nil {
		t.Fatal(err)
	}
	if rules, err := egressRules(t, c); err != nil || len(rules) != len(more) {
		t.Fatalf("after adding: %d rules, %v", len(rules), err)
	}
	if err := fw.SetEgressSubnets(ctx, subnets[:3]); err != nil {
		t.Fatal(err)
	}
	if rules, err := egressRules(t, c); err != nil || len(rules) != 3 {
		t.Fatalf("after removing: %d rules, %v", len(rules), err)
	}
}

var webTable = &nftables.Table{Family: nftables.TableFamilyBridge, Name: firewall.WebTableName}

// webVerdicts lists the chain's rules as "<saddr>/<accept|drop>" with the port for accepts.
func webVerdicts(t *testing.T, c *nftables.Conn) []string {
	t.Helper()
	rules, err := c.GetRules(webTable, &nftables.Chain{Name: firewall.WebChainName, Table: webTable})
	if err != nil {
		t.Fatalf("list %s: %v", firewall.WebTableName, err)
	}
	var out []string
	for _, r := range rules {
		var src netip.Addr
		var port uint16
		verdict := "?"
		for i, e := range r.Exprs {
			switch e := e.(type) {
			case *expr.Cmp:
				if p, ok := r.Exprs[i-1].(*expr.Payload); ok {
					switch {
					case p.Base == expr.PayloadBaseNetworkHeader && p.Offset == 12:
						src, _ = netip.AddrFromSlice(e.Data)
					case p.Base == expr.PayloadBaseTransportHeader && p.Offset == 2:
						port = uint16(e.Data[0])<<8 | uint16(e.Data[1])
					}
				}
			case *expr.Verdict:
				verdict = map[expr.VerdictKind]string{expr.VerdictAccept: "accept", expr.VerdictDrop: "drop"}[e.Kind]
			}
		}
		s := src.String() + "/" + verdict
		if port != 0 {
			s += fmt.Sprintf(":%d", port)
		}
		out = append(out, s)
	}
	return out
}

func webTableExists(t *testing.T, c *nftables.Conn) bool {
	t.Helper()
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyBridge)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(tables, func(tb *nftables.Table) bool { return tb.Name == firewall.WebTableName })
}

// TestNFTablesWebRules: SetWebRules replaces table "bridge mcpanel_web" as a whole,
// WebRulesInPlace notices outside changes, an empty list removes the table, and neither
// "inet mcpanel" nor a foreign table is touched.
func TestNFTablesWebRules(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	foreign := c.AddTable(&nftables.Table{Family: nftables.TableFamilyBridge, Name: "othertable"})
	c.AddChain(&nftables.Chain{Name: "keepme", Table: foreign})
	if err := c.Flush(); err != nil {
		t.Fatalf("create foreign table: %v", err)
	}
	fw := newBackend(t, fd)
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), time.Hour); err != nil {
		t.Fatal(err)
	}

	// Removing a table that does not exist is a no-op; an absent table is "in place" for no rules.
	if err := fw.SetWebRules(ctx, nil); err != nil {
		t.Fatalf("SetWebRules(nil) without table: %v", err)
	}
	if ok, err := fw.WebRulesInPlace(ctx, nil); err != nil || !ok {
		t.Fatalf("WebRulesInPlace(nil) without table = %v, %v", ok, err)
	}

	// Two allowed ports on one network (given unsorted); a third port of it stays denied.
	a := agentapi.WebRule{Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{8200, 8100}}
	b := agentapi.WebRule{Source: netip.MustParseAddr("172.30.2.2"), Dest: netip.MustParsePrefix("172.30.2.0/24"), Ports: []uint16{8123}}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a}); err != nil || ok {
		t.Fatalf("before SetWebRules: in place = %v, %v", ok, err)
	}
	for range 2 { // idempotent: replaced, never duplicated
		if err := fw.SetWebRules(ctx, []agentapi.WebRule{b, a}); err != nil {
			t.Fatalf("SetWebRules: %v", err)
		}
	}
	chain, err := c.ListChain(webTable, firewall.WebChainName)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Type != nftables.ChainTypeFilter ||
		chain.Hooknum == nil || *chain.Hooknum != *nftables.ChainHookForward ||
		chain.Priority == nil || *chain.Priority != nftables.ChainPriority(firewall.WebChainPriority) ||
		chain.Policy == nil || *chain.Policy != nftables.ChainPolicyAccept {
		t.Fatalf("web chain = %+v", chain)
	}
	want := []string{"172.30.1.2/accept:8100", "172.30.1.2/accept:8200", "172.30.1.2/drop", "172.30.2.2/accept:8123", "172.30.2.2/drop"}
	got := webVerdicts(t, c)
	if !slices.Equal(got, want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	if slices.Contains(got, "172.30.1.2/accept:8300") {
		t.Fatal("the third port 8300 must fall through to the drop")
	}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a, b}); err != nil || !ok {
		t.Fatalf("after SetWebRules: in place = %v, %v", ok, err)
	}
	other := b
	other.Ports = []uint16{8124}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a, other}); err != nil || ok {
		t.Fatalf("other port: in place = %v, %v; want false", ok, err)
	}
	if ok, err := fw.WebRulesInPlace(ctx, nil); err != nil || ok {
		t.Fatalf("table present, no rules wanted: in place = %v, %v; want false", ok, err)
	}
	for _, ports := range [][]uint16{{8100}, {8100, 8200, 8300}} {
		changed := a
		changed.Ports = ports
		if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{changed, b}); err != nil || ok {
			t.Fatalf("ports %v: in place = %v, %v; want false", ports, ok, err)
		}
	}

	// One rule left: the other is gone.
	if err := fw.SetWebRules(ctx, []agentapi.WebRule{a}); err != nil {
		t.Fatal(err)
	}
	if got := webVerdicts(t, c); !slices.Equal(got, want[:3]) {
		t.Fatalf("rules = %v, want %v", got, want[:3])
	}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a}); err != nil || !ok {
		t.Fatalf("one rule: in place = %v, %v", ok, err)
	}

	// Invalid input changes nothing.
	bad := agentapi.WebRule{Source: netip.MustParseAddr("172.30.9.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{8100}}
	if err := fw.SetWebRules(ctx, []agentapi.WebRule{bad}); !errors.Is(err, firewall.ErrInvalidWebRule) {
		t.Fatalf("SetWebRules(invalid) = %v", err)
	}
	if got := webVerdicts(t, c); !slices.Equal(got, want[:3]) {
		t.Fatalf("after invalid input: rules = %v", got)
	}

	// Flushed from outside: reported, and SetWebRules repairs it.
	c.FlushChain(&nftables.Chain{Name: firewall.WebChainName, Table: webTable})
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a}); err != nil || ok {
		t.Fatalf("after outside flush: in place = %v, %v; want false", ok, err)
	}
	c.DelTable(webTable)
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a}); err != nil || ok {
		t.Fatalf("after outside delete: in place = %v, %v; want false", ok, err)
	}
	if err := fw.SetWebRules(ctx, []agentapi.WebRule{a}); err != nil {
		t.Fatal(err)
	}
	if ok, err := fw.WebRulesInPlace(ctx, []agentapi.WebRule{a}); err != nil || !ok {
		t.Fatalf("after repair: in place = %v, %v", ok, err)
	}

	// An empty list removes the table.
	if err := fw.SetWebRules(ctx, []agentapi.WebRule{}); err != nil {
		t.Fatal(err)
	}
	if webTableExists(t, c) {
		t.Fatal("table still exists after SetWebRules(empty)")
	}
	if ok, err := fw.WebRulesInPlace(ctx, nil); err != nil || !ok {
		t.Fatalf("after removal: in place = %v, %v", ok, err)
	}

	// "inet mcpanel" and the foreign table are untouched.
	if _, err := c.ListChain(foreign, "keepme"); err != nil {
		t.Fatalf("foreign table changed: %v", err)
	}
	if jail, err := c.GetRules(mcTable, &nftables.Chain{Name: firewall.ChainName, Table: mcTable}); err != nil || len(jail) != 2 {
		t.Fatalf("jail chain: %d rules, %v", len(jail), err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"203.0.113.7/32"})
}
