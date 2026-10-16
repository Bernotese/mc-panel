package instances_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

// fakeEgress records the subnets the manager hands to the firewall; removed simulates an
// external "nft flush ruleset".
type fakeEgress struct {
	mu      sync.Mutex
	calls   [][]netip.Prefix
	checks  int
	removed bool
	err     error
}

func (f *fakeEgress) SetEgressSubnets(_ context.Context, subnets []netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, slices.Clone(subnets))
	f.removed = false
	return nil
}

func (f *fakeEgress) EgressInPlace(context.Context, []netip.Prefix) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return !f.removed, f.err
}

func (f *fakeEgress) remove() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = true
}

func (f *fakeEgress) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

func (f *fakeEgress) snapshot() [][]netip.Prefix {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeEgress) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func newEgressEnv(t *testing.T) (*env, *fakeEgress) {
	t.Helper()
	fe := &fakeEgress{}
	return newEnv(t, func(c *instances.Config) { c.Egress = fe }), fe
}

func prefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// The filter covers a server's own network before its container is created there, and
// the router's network before the router is created.
func TestEnsureAppliesEgressFilterBeforeCreate(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddImage(java21Pinned)
	e.d.AddImage(routerPinned)
	hold, release := e.d.Hold(dockertest.OpCreate)
	if err := e.m.Ensure(context.Background(), ensureReq(1, "survival", 1)); err != nil {
		t.Fatal(err)
	}
	if got := <-hold; got != instances.ContainerName(1) {
		t.Fatalf("first create = %q", got)
	}
	if calls := fe.snapshot(); len(calls) != 1 || !slices.Equal(calls[0], prefixes("10.213.0.0/28")) {
		t.Fatalf("egress calls before the instance is created = %v", calls)
	}
	release()
	e.m.Wait()
	if e.status(t, 1).LastError != "" {
		t.Fatalf("status = %+v", e.status(t, 1))
	}
	// The router's network came next, before the router was created on it.
	if calls := fe.snapshot(); len(calls) != 2 || !slices.Equal(calls[1], prefixes("10.213.0.0/28", "172.19.0.0/16")) {
		t.Fatalf("egress calls = %v", calls)
	}

	// A second server: its own subnet is added.
	e.ensure(t, ensureReq(2, "creative", 1))
	calls := fe.snapshot()
	if len(calls) != 3 || !slices.Equal(calls[2], prefixes("10.213.0.0/28", "10.213.0.16/28", "172.19.0.0/16")) {
		t.Fatalf("egress calls = %v", calls)
	}

	// Unchanged subnets and the filter still in place: no rule rewrite, neither from
	// ensure nor from the loop; both only check that it is still installed.
	checks := fe.checkCount()
	e.ensure(t, ensureReq(2, "creative", 1))
	e.m.Tick(context.Background())
	if n := len(fe.snapshot()); n != 3 {
		t.Fatalf("egress applied %d times for unchanged networks", n)
	}
	if fe.checkCount() <= checks {
		t.Fatal("ensure and the loop must check that the filter is still installed")
	}
}

// After an external "nft flush ruleset" the cache is not trusted: the next round (or
// the next ensure, before it creates a container) installs the filter again.
func TestEgressFilterIsReinstalledAfterExternalFlush(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.20.0.0/16")
	if err := e.m.SyncEgress(context.Background()); err != nil {
		t.Fatal(err)
	}
	fe.remove()
	e.m.Tick(context.Background())
	if calls := fe.snapshot(); len(calls) != 2 || !slices.Equal(calls[1], prefixes("172.20.0.0/16")) {
		t.Fatalf("egress calls = %v, want a reinstall", calls)
	}

	fe.remove()
	e.d.AddImage(java21Pinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	if n := len(fe.snapshot()); n != 3 {
		t.Fatalf("ensure after a flush: egress applied %d times, want 3", n)
	}
	if !strings.Contains(e.logs.String(), "removed from outside") {
		t.Fatalf("no warning logged:\n%s", e.logs.String())
	}
}

// After an agent restart (new manager, network already there) the background loop
// applies the filter without recreating any container.
func TestTickAppliesEgressFilterToExistingNetwork(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.20.0.0/16", "fd00:1::/64")
	// cmd/agent runs SyncEgress once before serving, before the first loop round.
	if err := e.m.SyncEgress(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.m.Tick(context.Background())
	if calls := fe.snapshot(); len(calls) != 1 || !slices.Equal(calls[0], prefixes("172.20.0.0/16", "fd00:1::/64")) {
		t.Fatalf("egress calls = %v", calls)
	}
	// The network was recreated with another subnet (e.g. by an operator): re-applied.
	e.d.SetNetworkSubnets(instances.RouterNetwork, "172.21.0.0/16")
	e.m.Tick(context.Background())
	if calls := fe.snapshot(); len(calls) != 2 || !slices.Equal(calls[1], prefixes("172.21.0.0/16")) {
		t.Fatalf("egress calls = %v", calls)
	}
}

func TestTickWithoutNetworkSkipsEgress(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.FailPull("no router in this test") // the router (and with it the network) is never created
	e.m.Tick(context.Background())
	if calls := fe.snapshot(); len(calls) != 0 {
		t.Fatalf("egress calls without a network = %v", calls)
	}
}

// A foreign network that only shares the name is never filtered (nor adopted).
func TestEgressIgnoresForeignNetwork(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{"com.example": "x"}, "10.99.0.0/16")
	e.m.Tick(context.Background())
	if calls := fe.snapshot(); len(calls) != 0 {
		t.Fatalf("egress calls = %v", calls)
	}
}

// Fail closed: without the filter no container is created.
func TestEnsureFailsWhenEgressFilterFails(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddImage(java21Pinned)
	fe.fail(errors.New("netlink: operation not permitted"))
	e.ensure(t, ensureReq(1, "survival", 1))
	for _, c := range e.d.Calls() {
		if c == "create "+instances.ContainerName(1) {
			t.Fatalf("container created without egress filter: %v", e.d.Calls())
		}
	}
	if st := e.status(t, 1); !strings.Contains(st.LastError, "egress") {
		t.Fatalf("last_error = %q", st.LastError)
	}

	// Once the firewall works again, the next ensure applies the filter and creates it.
	fe.fail(nil)
	e.ensure(t, ensureReq(1, "survival", 1))
	if st := e.status(t, 1); st.LastError != "" || st.State == agentapi.StateMissing {
		t.Fatalf("status after recovery = %+v", st)
	}
	if calls := fe.snapshot(); len(calls) == 0 || !slices.Equal(calls[0], prefixes("10.213.0.0/28")) {
		t.Fatalf("egress calls = %v", calls)
	}
}
