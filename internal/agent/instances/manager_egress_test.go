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
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

// fakeEgress records the subnets the manager hands to the firewall.
type fakeEgress struct {
	mu    sync.Mutex
	calls [][]netip.Prefix
	err   error
}

func (f *fakeEgress) SetEgressSubnets(_ context.Context, subnets []netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, slices.Clone(subnets))
	return nil
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

// The filter is in place before the first container is created on a fresh network.
func TestEnsureAppliesEgressFilterBeforeCreate(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddImage(java21Pinned)
	e.d.AddImage(routerPinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	calls := fe.snapshot()
	if len(calls) != 1 || !slices.Equal(calls[0], prefixes("172.18.0.0/16")) {
		t.Fatalf("egress calls = %v", calls)
	}
	created := false
	for _, c := range e.d.Calls() {
		if c == "create "+instances.ContainerName(1) {
			created = true
		}
	}
	if !created || e.status(t, 1).LastError != "" {
		t.Fatalf("instance not created: %v, %+v", e.d.Calls(), e.status(t, 1))
	}

	// Unchanged subnets: no second rule rewrite, neither from ensure nor from the loop.
	e.ensure(t, ensureReq(2, "creative", 1))
	e.m.Tick(context.Background())
	if n := len(fe.snapshot()); n != 1 {
		t.Fatalf("egress applied %d times for an unchanged network", n)
	}
}

// After an agent restart (new manager, network already there) the background loop
// applies the filter without recreating any container.
func TestTickAppliesEgressFilterToExistingNetwork(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddNetwork(instances.NetworkName, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.20.0.0/16", "fd00:1::/64")
	e.m.Tick(context.Background())
	if calls := fe.snapshot(); len(calls) != 1 || !slices.Equal(calls[0], prefixes("172.20.0.0/16", "fd00:1::/64")) {
		t.Fatalf("egress calls = %v", calls)
	}
	// The network was recreated with another subnet (e.g. by an operator): re-applied.
	e.d.SetNetworkSubnets(instances.NetworkName, "172.21.0.0/16")
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
	e.d.AddNetwork(instances.NetworkName, "bridge", map[string]string{"com.example": "x"}, "10.99.0.0/16")
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
	if n := len(fe.snapshot()); n != 1 {
		t.Fatalf("egress applied %d times", n)
	}
}
