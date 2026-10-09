package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"mcpanel/internal/agent"
	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/peercred"
)

var _ agentapi.Client = (*agent.Client)(nil)

// startAgent runs a real server on a temporary Unix socket behind the peer
// credential check and stops it when the test ends.
func startAgent(t *testing.T, fw agentapi.Firewall, allow func(uint32) bool) (string, *syncBuffer) {
	t.Helper()
	path := socketPath(t)
	logs := &syncBuffer{}
	log := testLogger(logs)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := agent.NewServer(fw, "test", log)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(peercred.Wrap(l, allow, log)) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path, logs
}

func TestClientHealth(t *testing.T) {
	fw, _ := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	h, err := agent.NewClient(path).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h != (agentapi.HealthResponse{Status: "ok", Version: "test", Firewall: "noop"}) {
		t.Fatalf("health = %+v", h)
	}
}

func TestClientBanListUnban(t *testing.T) {
	fw, clk := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	p4 := netip.MustParsePrefix("203.0.113.7/32")
	p6 := netip.MustParsePrefix("2001:db8:1:2::/64")
	if err := c.Ban(ctx, p4, 15*time.Minute); err != nil {
		t.Fatalf("Ban v4: %v", err)
	}
	if err := c.Ban(ctx, p6, 90*time.Minute+500*time.Millisecond); err != nil { // rounded up to 5401 s
		t.Fatalf("Ban v6: %v", err)
	}
	clk.Advance(time.Minute)
	bans, err := c.Bans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentapi.Ban{{Prefix: "203.0.113.7/32", ExpiresInSeconds: 840}, {Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 5341}}
	if !slices.Equal(bans, want) {
		t.Fatalf("Bans = %+v, want %+v", bans, want)
	}
	if err := c.Unban(ctx, p4); err != nil {
		t.Fatalf("Unban: %v", err)
	}
	if err := c.Unban(ctx, p4); err != nil {
		t.Fatalf("second Unban: %v", err)
	}
	bans, err = c.Bans(ctx)
	if err != nil || !slices.Equal(bans, want[1:]) {
		t.Fatalf("Bans after unban = %+v, %v", bans, err)
	}
}

func TestClientRebanRefreshes(t *testing.T) {
	fw, _ := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")
	if err := c.Ban(ctx, p, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Ban(ctx, p, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	bans, err := c.Bans(ctx)
	if err != nil || !slices.Equal(bans, []agentapi.Ban{{Prefix: "203.0.113.7/32", ExpiresInSeconds: 7200}}) {
		t.Fatalf("Bans = %+v, %v", bans, err)
	}
}

func TestClientValidatesBeforeSending(t *testing.T) {
	fw, _ := newNoop()
	path, logs := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	cases := []struct {
		name string
		p    netip.Prefix
		d    time.Duration
	}{
		{"ipv4 /24", netip.MustParsePrefix("203.0.113.0/24"), time.Minute},
		{"mapped", netip.MustParsePrefix("::ffff:203.0.113.7/128"), time.Minute},
		{"host bits", netip.MustParsePrefix("2001:db8:1:2::5/64"), time.Minute},
		{"zero prefix", netip.Prefix{}, time.Minute},
		{"too short", netip.MustParsePrefix("203.0.113.7/32"), 999 * time.Millisecond},
		{"too long", netip.MustParsePrefix("203.0.113.7/32"), agentapi.MaxBanDuration + time.Second},
	}
	for _, tc := range cases {
		err := c.Ban(ctx, tc.p, tc.d)
		if !errors.Is(err, agent.ErrInvalid) || errors.Is(err, agentapi.ErrUnavailable) {
			t.Errorf("%s: Ban = %v, want ErrInvalid only", tc.name, err)
		}
	}
	if err := c.Unban(ctx, netip.MustParsePrefix("0.0.0.0/0")); !errors.Is(err, agent.ErrInvalid) {
		t.Errorf("Unban(0.0.0.0/0) = %v, want ErrInvalid", err)
	}
	if bans, err := c.Bans(ctx); err != nil || len(bans) != 0 {
		t.Fatalf("Bans = %+v, %v", bans, err)
	}
	if strings.Contains(logs.String(), "agent request rejected") {
		t.Fatalf("invalid requests reached the server:\n%s", logs.String())
	}
}

func TestClientMapsServerErrors(t *testing.T) {
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")

	path, _ := startAgent(t, stubFirewall{err: errors.New("netlink: boom")}, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	err := c.Ban(ctx, p, time.Minute)
	var se *agent.StatusError
	if !errors.As(err, &se) || se.StatusCode != 500 || se.Message != "firewall operation failed" {
		t.Fatalf("Ban error = %#v", err)
	}
	if !errors.Is(err, agentapi.ErrUnavailable) || errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("5xx must be ErrUnavailable only: %v", err)
	}
	if _, err := c.Bans(ctx); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("Bans error = %v, want ErrUnavailable", err)
	}

	conflict := stubFirewall{err: fmt.Errorf("%w: test", agent.ErrConflict)}
	path2, _ := startAgent(t, conflict, peercred.AllowUIDs(selfUID(t)))
	err = agent.NewClient(path2).Ban(ctx, p, time.Minute)
	if !errors.Is(err, agent.ErrConflict) || errors.Is(err, agentapi.ErrUnavailable) || !errors.As(err, &se) || se.StatusCode != 409 {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestClientUnavailableWithoutAgent(t *testing.T) {
	ctx := context.Background()
	missing := socketPath(t)
	if _, err := agent.NewClient(missing).Health(ctx); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("missing socket: %v, want ErrUnavailable", err)
	}

	stale := socketPath(t)
	l, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	if err := agent.NewClient(stale).Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), time.Minute); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("stale socket: %v, want ErrUnavailable", err)
	}
}

func TestClientTimeout(t *testing.T) {
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	held := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			held <- c // accept, never answer
		}
	}()
	t.Cleanup(func() {
		for {
			select {
			case c := <-held:
				_ = c.Close()
			default:
				return
			}
		}
	})

	start := time.Now()
	_, err = agent.NewClientWithTimeout(path, 200*time.Millisecond).Health(context.Background())
	if !errors.Is(err, agentapi.ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Health = %v, want ErrUnavailable wrapping DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestClientRejectedPeer(t *testing.T) {
	fw, _ := newNoop()
	path, logs := startAgent(t, fw, func(uint32) bool { return false })
	_, err := agent.NewClient(path).Health(context.Background())
	if !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("Health from disallowed uid = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(logs.String(), "disallowed uid") {
		t.Fatalf("rejection not logged:\n%s", logs.String())
	}
}

func TestClientLogsPeerUID(t *testing.T) {
	fw, _ := newNoop()
	me := selfUID(t)
	path, logs := startAgent(t, fw, peercred.AllowUIDs(me))
	if err := agent.NewClient(path).Ban(context.Background(), netip.MustParsePrefix("203.0.113.7/32"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("peer_uid=%d", me); !strings.Contains(logs.String(), want) {
		t.Fatalf("log missing %q:\n%s", want, logs.String())
	}
}

func TestClientConcurrent(t *testing.T) {
	fw, _ := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range byte(50) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, i}), 32)
			if err := c.Ban(ctx, p, time.Hour); err != nil {
				t.Errorf("Ban %s: %v", p, err)
				return
			}
			if i%2 == 0 {
				if err := c.Unban(ctx, p); err != nil {
					t.Errorf("Unban %s: %v", p, err)
				}
			}
		}()
	}
	wg.Wait()
	bans, err := c.Bans(ctx)
	if err != nil || len(bans) != 25 {
		t.Fatalf("Bans = %d, %v; want 25", len(bans), err)
	}
}

func TestServerShutdownRemovesSocket(t *testing.T) {
	fw, _ := newNoop()
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	log := testLogger(io.Discard)
	srv := agent.NewServer(fw, "test", log)
	allow := peercred.AllowUIDs(selfUID(t))
	done := make(chan error, 1)
	go func() { done <- srv.Serve(peercred.Wrap(l, allow, log)) }()
	if _, err := agent.NewClient(path).Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve = %v, want nil after Shutdown", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket still present after Shutdown: %v", err)
	}
}
