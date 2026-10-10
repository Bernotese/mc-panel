package defense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// fakeAgent is an in-memory agentapi.Client.
type fakeAgent struct {
	mu         sync.Mutex
	err        error // returned by every call while set
	banErr     error // returned by Ban only
	health     agentapi.HealthResponse
	bans       map[netip.Prefix]time.Duration
	banCalls   []netip.Prefix
	banDurs    []time.Duration
	unbanCalls []netip.Prefix
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		health: agentapi.HealthResponse{Status: "ok", Version: "test", Firewall: "noop"},
		bans:   make(map[netip.Prefix]time.Duration),
	}
}

var errAgentDown = fmt.Errorf("dial unix /run/mc-panel/agent.sock: %w", agentapi.ErrUnavailable)

func withAgent(a agentapi.Client) func(*Deps) { return func(d *Deps) { d.Agent = a } }

func (f *fakeAgent) setErr(err error)    { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) setBanErr(err error) { f.mu.Lock(); f.banErr = err; f.mu.Unlock() }
func (f *fakeAgent) clearBans() {
	f.mu.Lock()
	f.bans = make(map[netip.Prefix]time.Duration)
	f.mu.Unlock()
}

func (f *fakeAgent) calls() (bans, unbans []netip.Prefix, durs []time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]netip.Prefix(nil), f.banCalls...), append([]netip.Prefix(nil), f.unbanCalls...),
		append([]time.Duration(nil), f.banDurs...)
}

func (f *fakeAgent) Health(context.Context) (agentapi.HealthResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentapi.HealthResponse{}, f.err
	}
	return f.health, nil
}

func (f *fakeAgent) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.banErr != nil {
		return f.banErr
	}
	f.banCalls = append(f.banCalls, p)
	f.banDurs = append(f.banDurs, d)
	f.bans[p] = d
	return nil
}

func (f *fakeAgent) Unban(_ context.Context, p netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.unbanCalls = append(f.unbanCalls, p)
	delete(f.bans, p)
	return nil
}

func (f *fakeAgent) Bans(context.Context) ([]agentapi.Ban, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]agentapi.Ban, 0, len(f.bans))
	for p, d := range f.bans {
		out = append(out, agentapi.Ban{Prefix: p.String(), ExpiresInSeconds: int64(d.Seconds())})
	}
	return out, nil
}

func (e *env) pending(t *testing.T) int64 {
	t.Helper()
	n, err := e.svc.pendingSync(context.Background(), e.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSyncCatchesUpAfterAgentOutage(t *testing.T) {
	fa := newFakeAgent()
	fa.setErr(errAgentDown)
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()

	// login handling and the in-app jail work while the agent is down
	e.fail(t, "203.0.113.7", 5)
	if err := e.svc.CheckLogin(ctx, addr("203.0.113.7"), "admin"); !errors.Is(err, ErrJailed) {
		t.Fatalf("CheckLogin = %v, want ErrJailed", err)
	}
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("filter code = %d", rec.Code)
	}
	if err := e.svc.SyncOnce(ctx); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("SyncOnce = %v, want ErrUnavailable", err)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}

	fa.setErr(nil)
	e.clk.Advance(time.Minute)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce after recovery: %v", err)
	}
	bans, _, durs := fa.calls()
	if len(bans) != 1 || bans[0] != netip.MustParsePrefix("203.0.113.7/32") || durs[0] != 14*time.Minute {
		t.Fatalf("ban calls = %v %v", bans, durs)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 {
		t.Fatalf("already banned jails must not be banned again: %v", bans)
	}
}

func TestSyncUnbansLiftedJails(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	id := e.insertJail(t, "2001:db8:1:2::/64", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec("UPDATE jails SET lifted_at = ?, lifted_by = 'admin' WHERE id = ?", t0.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, unbans, _ := fa.calls()
	if len(unbans) != 1 || unbans[0] != netip.MustParsePrefix("2001:db8:1:2::/64") {
		t.Fatalf("unban calls = %v", unbans)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, unbans, _ := fa.calls(); len(unbans) != 1 {
		t.Fatalf("unban must happen once: %v", unbans)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestSyncReconcilesMissingBans(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	fa.clearBans() // agent restart / host reboot: nftables set is empty
	e.clk.Advance(10 * time.Minute)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	bans, _, durs := fa.calls()
	if len(bans) != 2 || bans[1] != netip.MustParsePrefix("203.0.113.7/32") || durs[1] != 50*time.Minute {
		t.Fatalf("expected re-ban with remaining time, got %v %v", bans, durs)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 2 {
		t.Fatalf("present bans must not be re-banned: %v", bans)
	}
}

func TestSyncPermanentErrorIsNotRetried(t *testing.T) {
	fa := newFakeAgent()
	fa.setBanErr(errors.New("agent: 409 conflict: overlapping ban"))
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.8/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatalf("permanent errors must not abort the cycle: %v", err)
	}
	fa.setBanErr(nil)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 0 {
		t.Fatalf("permanently failed jails must not be retried: %v", bans)
	}
	e.insertJail(t, "203.0.113.9/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 || bans[0] != netip.MustParsePrefix("203.0.113.9/32") {
		t.Fatalf("new jails must still be synced: %v", bans)
	}
}

func TestSyncWithoutAgentIsNoop(t *testing.T) {
	e := newEnv(t)
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRunKicksSyncAfterJail(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // let the initial sync pass
	e.fail(t, "203.0.113.7", 5)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if bans, _, _ := fa.calls(); len(bans) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("jail was not synced after kick (SyncInterval is 10s, kick must trigger earlier)")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestPruneOnce(t *testing.T) {
	e := newEnv(t)
	e.clk.Set(t0.Add(-91 * 24 * time.Hour))
	e.fail(t, "203.0.113.7", 1)
	e.clk.Set(t0.Add(-89 * 24 * time.Hour))
	e.fail(t, "203.0.113.7", 1)
	e.clk.Set(t0)
	n, err := e.svc.PruneOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("PruneOnce = %d, %v; want 1", n, err)
	}
}

func TestCleanupLimiters(t *testing.T) {
	e := newEnv(t)
	_ = e.svc.CheckLogin(context.Background(), addr("203.0.113.7"), "admin")
	e.clk.Advance(LimiterIdle)
	e.svc.cleanupLimiters(e.clk.Now())
	if e.svc.loginIP.size() != 0 || e.svc.loginUser.size() != 0 {
		t.Fatalf("idle buckets not removed: %d/%d", e.svc.loginIP.size(), e.svc.loginUser.size())
	}
}

// An allowlisted /128 inside a jailed /64 must never be firewalled.
func TestSyncSkipsJailsOverlappingAllowRule(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.addRule(t, "2001:db8:1:2::5/128", "allow", nil)
	// another address in the same /64 gets the whole key jailed
	e.fail(t, "2001:db8:1:2::99", 5)
	if js := e.activeJails(t); len(js) != 1 || js[0].IpKey != "2001:db8:1:2::/64" {
		t.Fatalf("precondition: jails = %+v", js)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 0 {
		t.Fatalf("a /64 containing an allowlisted address must not be banned: %v", bans)
	}
	// the app-level jail stays in place for everybody else
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:1:2::99"), "x"); !errors.Is(err, ErrJailed) {
		t.Fatalf("app-level jail must remain: %v", err)
	}
}

func TestSyncUnbansWhenAllowRuleIsAddedLater(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.insertJail(t, "2001:db8:1:2::/64", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 {
		t.Fatalf("precondition: bans = %v", bans)
	}
	e.addRule(t, "2001:db8:1:2::5/128", "allow", nil)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, unbans, _ := fa.calls()
	if len(unbans) != 1 || unbans[0] != netip.MustParsePrefix("2001:db8:1:2::/64") {
		t.Fatalf("unban calls = %v", unbans)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, unbans, _ := fa.calls(); len(bans) != 1 || len(unbans) != 1 {
		t.Fatalf("no ban/unban churn expected: bans=%v unbans=%v", bans, unbans)
	}
}

func TestSyncCancelledCallsAreTransient(t *testing.T) {
	fa := newFakeAgent()
	fa.setBanErr(context.Canceled) // e.g. shutdown while a call is in flight
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncOnce = %v, want context.Canceled", err)
	}
	fa.setBanErr(nil)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 {
		t.Fatalf("jail must not be marked unbannable after a cancelled call: %v", bans)
	}
}

// bareAgent reports bans as bare addresses without a prefix length.
type bareAgent struct{ *fakeAgent }

func (b bareAgent) Bans(context.Context) ([]agentapi.Ban, error) {
	return []agentapi.Ban{{Prefix: "203.0.113.7", ExpiresInSeconds: 600}, {Prefix: "2001:db8:1:2::", ExpiresInSeconds: 600}}, nil
}

func TestReconcileAcceptsBareAddressesFromAgent(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(bareAgent{fa}))
	ctx := context.Background()
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 {
		t.Fatalf("a ban listed as bare address is present and must not be re-banned: %v", bans)
	}
}

func TestPendingSyncIgnoresUnbannableJails(t *testing.T) {
	fa := newFakeAgent()
	fa.setBanErr(errors.New("agent: 409 conflict: overlapping ban"))
	e := newEnv(t, withAgent(fa))
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
	if err := e.svc.SyncOnce(context.Background()); err != nil { // marks the jail unbannable
		t.Fatal(err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("permanently failed jails are not retried and must not count as pending, got %d", n)
	}
}
