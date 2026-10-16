package instances_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

func TestNextSubnet(t *testing.T) {
	pool := netip.MustParsePrefix("10.213.0.0/16")
	cases := []struct {
		pool netip.Prefix
		used []string
		want string // "" = none free
	}{
		{pool, nil, "10.213.0.0/28"},
		{pool, []string{"10.213.0.0/28", "10.213.0.16/28"}, "10.213.0.32/28"},
		{pool, []string{"10.213.0.16/28"}, "10.213.0.0/28"},
		{pool, []string{"10.213.0.0/24", "172.17.0.0/16"}, "10.213.1.0/28"},
		{pool, []string{"10.213.0.5/32"}, "10.213.0.16/28"},
		{pool, []string{"10.0.0.0/8"}, ""},
		{netip.MustParsePrefix("10.213.0.0/27"), []string{"10.213.0.0/28", "10.213.0.16/28"}, ""},
		{netip.MustParsePrefix("10.213.255.0/24"), []string{"10.213.255.0/25"}, "10.213.255.128/28"},
	}
	for _, c := range cases {
		got, ok := instances.NextSubnet(c.pool, 28, prefixes(c.used...))
		if (c.want == "") == ok || (ok && got.String() != c.want) {
			t.Errorf("NextSubnet(%s, %v) = %v, %v; want %q", c.pool, c.used, got, ok, c.want)
		}
	}
	if _, ok := instances.NextSubnet(netip.MustParsePrefix("fd00::/48"), 28, nil); ok {
		t.Error("an IPv6 pool must give nothing")
	}
}

// Every server gets its own network from the pool; the router is on all of them and on
// its own mcpanel-mc, and no server is on another server's network.
func TestEnsureCreatesOwnNetworkPerInstance(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.ensure(t, ensureReq(2, "creative", 1))
	for id, subnet := range map[int64]string{1: "10.213.0.0/28", 2: "10.213.0.16/28"} {
		name := instances.InstanceNetwork(id)
		if got := e.d.NetworkSubnets(name); !slices.Equal(got, []string{subnet}) {
			t.Errorf("%s subnets = %v, want %s", name, got, subnet)
		}
		if got := e.container(t, instances.ContainerName(id)).Networks; !slices.Equal(got, []string{name}) {
			t.Errorf("%s networks = %v", instances.ContainerName(id), got)
		}
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"mcpanel-mc", "mcpanel-net-1", "mcpanel-net-2"}) {
		t.Fatalf("router networks = %v", got)
	}

	// A recreate keeps the network and its subnet.
	req := ensureReq(1, "survival", 2)
	req.Recreate = true
	e.ensure(t, req)
	if got := e.d.NetworkSubnets("mcpanel-net-1"); !slices.Equal(got, []string{"10.213.0.0/28"}) {
		t.Fatalf("subnet after recreate = %v", got)
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Contains(got, "mcpanel-net-1") {
		t.Fatalf("router networks after recreate = %v", got)
	}
}

// The subnet overlaps neither another Docker network nor a network of the host.
func TestInstanceSubnetAvoidsHostAndDockerNetworks(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) {
		c.HostPrefixes = func() ([]netip.Prefix, error) { return prefixes("10.213.0.0/27"), nil }
	})
	e.d.AddNetwork("someone-else", "bridge", nil, "10.213.0.32/28")
	e.ensure(t, ensureReq(1, "survival", 1))
	if got := e.d.NetworkSubnets("mcpanel-net-1"); !slices.Equal(got, []string{"10.213.0.48/28"}) {
		t.Fatalf("subnet = %v, want 10.213.0.48/28", got)
	}
}

// A full pool fails the ensure before anything is created.
func TestPoolExhausted(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.NetworkPool = netip.MustParsePrefix("10.213.0.0/27") })
	e.ensure(t, ensureReq(1, "alpha", 1))
	e.ensure(t, ensureReq(2, "bravo", 1))
	e.ensure(t, ensureReq(3, "charlie", 1))
	if s := e.status(t, 3); s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "pool exhausted") {
		t.Fatalf("status = %+v", s)
	}
	if e.d.HasNetwork("mcpanel-net-3") {
		t.Fatal("network created from an exhausted pool")
	}
}

// Removing a server removes its network; the others stay.
func TestRemoveRemovesInstanceNetwork(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.ensure(t, ensureReq(2, "creative", 1))
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if e.d.HasNetwork("mcpanel-net-1") || !e.d.HasNetwork("mcpanel-net-2") {
		t.Fatalf("networks = %v", e.d.Networks())
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"mcpanel-mc", "mcpanel-net-2"}) {
		t.Fatalf("router networks = %v", got)
	}
	// The freed subnet is handed out again.
	e.ensure(t, ensureReq(3, "lobby", 1))
	if got := e.d.NetworkSubnets("mcpanel-net-3"); !slices.Equal(got, []string{"10.213.0.0/28"}) {
		t.Fatalf("subnet = %v", got)
	}
}

// legacy seeds a container of an older agent: on the shared mcpanel-mc only.
func (e *env) legacy(t *testing.T, id int64, name string, running bool) {
	t.Helper()
	cfg, err := instances.InstanceContainer(testHost(), ensureReq(id, name, 1))
	if err != nil {
		t.Fatal(err)
	}
	cfg.HostConfig.NetworkMode = instances.RouterNetwork
	cfg.NetworkingConfig = docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{instances.RouterNetwork: {}}}
	e.d.AddContainer(instances.ContainerName(id), cfg, running)
}

// Containers of an older agent are moved onto their own network: at once onto the new
// one, off the shared one only while stopped (online players are never cut off).
func TestLoopIsolatesLegacyContainers(t *testing.T) {
	e := newEnv(t)
	e.d.AddImage(routerPinned)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.18.0.0/16")
	e.legacy(t, 1, "survival", true)
	e.legacy(t, 2, "creative", false)
	e.m.Tick(context.Background())

	if got := e.container(t, "mcpanel-mc-1").Networks; !slices.Equal(got, []string{"mcpanel-mc", "mcpanel-net-1"}) {
		t.Fatalf("running legacy container networks = %v", got)
	}
	if got := e.container(t, "mcpanel-mc-2").Networks; !slices.Equal(got, []string{"mcpanel-net-2"}) {
		t.Fatalf("stopped legacy container networks = %v", got)
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"mcpanel-mc", "mcpanel-net-1", "mcpanel-net-2"}) {
		t.Fatalf("router networks = %v", got)
	}
	if !strings.Contains(e.logs.String(), "still share the network mcpanel-mc") || !strings.Contains(e.logs.String(), "ids=[1]") {
		t.Fatalf("no warning about server 1:\n%s", e.logs.String())
	}

	// Its next stop takes it off the shared network; the start keeps it there.
	e.power(t, 1, agentapi.PowerRestart)
	if c := e.container(t, "mcpanel-mc-1"); !slices.Equal(c.Networks, []string{"mcpanel-net-1"}) || c.Status != "running" {
		t.Fatalf("after restart: networks %v, status %s", c.Networks, c.Status)
	}
	e.m.Tick(context.Background())
	if !strings.Contains(e.logs.String(), "every server is on its own network") {
		t.Fatalf("no all-clear logged:\n%s", e.logs.String())
	}
}

// A start of a stopped container of an older agent isolates it first; so does a stop.
func TestPowerIsolatesLegacyContainer(t *testing.T) {
	e := newEnv(t)
	e.d.AddImage(routerPinned)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.18.0.0/16")
	e.legacy(t, 1, "survival", false)
	e.power(t, 1, agentapi.PowerStart)
	if c := e.container(t, "mcpanel-mc-1"); !slices.Equal(c.Networks, []string{"mcpanel-net-1"}) || c.Status != "running" {
		t.Fatalf("after start: networks %v, status %s", c.Networks, c.Status)
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Contains(got, "mcpanel-net-1") {
		t.Fatalf("router networks = %v", got)
	}

	e.legacy(t, 2, "creative", true)
	e.power(t, 2, agentapi.PowerStop)
	if got := e.container(t, "mcpanel-mc-2").Networks; !slices.Equal(got, []string{"mcpanel-net-2"}) {
		t.Fatalf("after stop: networks %v", got)
	}
}

// The loop removes the network of a server that no longer exists, but never a foreign
// network or one another container still uses.
func TestLoopRemovesOrphanNetworks(t *testing.T) {
	e := newEnv(t)
	e.d.AddImage(routerPinned)
	labels := func(id int64) map[string]string {
		return map[string]string{instances.LabelNetwork: "1", instances.LabelInstance: strconv.FormatInt(id, 10)}
	}
	e.d.AddNetwork("mcpanel-net-9", "bridge", labels(9), "10.213.0.144/28")
	e.d.AddNetwork("mcpanel-net-8", "bridge", map[string]string{"com.example": "x"}, "10.213.0.128/28")
	e.d.AddNetwork("mcpanel-net-7", "bridge", labels(7), "10.213.0.112/28")
	e.d.AddContainer("someone", docker.ContainerConfig{Image: "x", HostConfig: docker.HostConfig{NetworkMode: "mcpanel-net-7"}}, true)
	e.m.Tick(context.Background())
	if e.d.HasNetwork("mcpanel-net-9") || !e.d.HasNetwork("mcpanel-net-8") || !e.d.HasNetwork("mcpanel-net-7") {
		t.Fatalf("networks = %v", e.d.Networks())
	}
}

// A new router container (e.g. after an upgrade with a new template) is connected to
// every instance network again.
func TestNewRouterIsConnectedToInstanceNetworks(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	if err := docker.New(e.d.Socket).Remove(context.Background(), instances.RouterName, true); err != nil {
		t.Fatal(err)
	}
	e.m.Tick(context.Background())
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"mcpanel-mc", "mcpanel-net-1"}) {
		t.Fatalf("router networks = %v", got)
	}
}

// A network called like an instance network but not carrying mc-panel's labels is never
// used, removed or filtered.
func TestForeignInstanceNetworkIsRefused(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddNetwork("mcpanel-net-1", "bridge", nil, "10.99.0.0/28")
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); !strings.Contains(s.LastError, "not managed") {
		t.Fatalf("status = %+v", s)
	}
	for _, calls := range fe.snapshot() {
		if slices.Contains(calls, netip.MustParsePrefix("10.99.0.0/28")) {
			t.Fatalf("foreign subnet filtered: %v", calls)
		}
	}
}

// The pool check warns when the pool overlaps a network of the host.
func TestCheckNetworkPoolWarns(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) {
		c.HostPrefixes = func() ([]netip.Prefix, error) { return prefixes("192.168.1.0/24", "10.213.4.0/24"), nil }
	})
	if err := e.m.CheckNetworkPool(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.logs.String(), "instance_network_pool overlaps") || !strings.Contains(e.logs.String(), "10.213.4.0/24") {
		t.Fatalf("no warning:\n%s", e.logs.String())
	}
}

// Review SEC-03 #1: a server that was created but never started has no endpoint in
// Docker's network inspect; the loop must not take it for an orphan and remove its
// network, or the start would fail for good.
func TestLoopKeepsNetworkOfCreatedContainer(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "created" {
		t.Fatalf("status = %s", c.Status)
	}
	e.m.Tick(context.Background())
	if !e.d.HasNetwork("mcpanel-net-1") {
		t.Fatal("the loop removed the network of a created container")
	}
	e.power(t, 1, agentapi.PowerStart)
	if s := e.status(t, 1); s.State != agentapi.StateRunning || s.LastError != "" {
		t.Fatalf("status = %+v", s)
	}
}

// A start repairs a network deleted from outside (docker network prune while the server
// and the router were off): the network is created again and the stale endpoint of the
// container is reconnected, so Docker accepts the start.
func TestPowerStartRepairsDeletedNetwork(t *testing.T) {
	e := newEnv(t)
	dc := docker.New(e.d.Socket)
	ctx := context.Background()
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	e.power(t, 1, agentapi.PowerStop)
	if err := dc.NetworkDisconnect(ctx, "mcpanel-net-1", instances.RouterName); err != nil {
		t.Fatal(err)
	}
	if err := dc.RemoveNetwork(ctx, "mcpanel-net-1"); err != nil {
		t.Fatal(err)
	}
	e.power(t, 1, agentapi.PowerStart)
	if s := e.status(t, 1); s.State != agentapi.StateRunning || s.LastError != "" {
		t.Fatalf("status after the repair = %+v", s)
	}
	if !e.d.HasNetwork("mcpanel-net-1") || !slices.Contains(e.container(t, instances.RouterName).Networks, "mcpanel-net-1") {
		t.Fatalf("networks %v, router %v", e.d.Networks(), e.container(t, instances.RouterName).Networks)
	}
	if !strings.Contains(e.logs.String(), "deleted and created again") {
		t.Fatalf("no repair logged:\n%s", e.logs.String())
	}

	// A created container (never started) on a deleted network starts as well.
	e.ensure(t, ensureReq(2, "creative", 1))
	if err := dc.NetworkDisconnect(ctx, "mcpanel-net-2", instances.RouterName); err != nil {
		t.Fatal(err)
	}
	if err := dc.RemoveNetwork(ctx, "mcpanel-net-2"); err != nil {
		t.Fatal(err)
	}
	e.power(t, 2, agentapi.PowerStart)
	if s := e.status(t, 2); s.State != agentapi.StateRunning || s.LastError != "" {
		t.Fatalf("status of the created container = %+v", s)
	}
}

// Review SEC-03 #2: a network called mcpanel-net-<id> without mc-panel's labels is never
// disconnected or removed, also not when its server is removed.
func TestRemoveLeavesForeignNetworkAlone(t *testing.T) {
	e := newEnv(t)
	e.d.AddNetwork("mcpanel-net-5", "bridge", map[string]string{"com.example": "x"}, "10.99.0.0/28")
	e.d.AddNetwork("mcpanel-net-6", "bridge", map[string]string{instances.LabelNetwork: "1", instances.LabelInstance: "7"}, "10.99.0.16/28")
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 5) })
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 6) })
	e.m.Tick(context.Background())
	if !e.d.HasNetwork("mcpanel-net-5") || !e.d.HasNetwork("mcpanel-net-6") {
		t.Fatalf("foreign network removed: %v", e.d.Networks())
	}
	if !strings.Contains(e.logs.String(), "network not managed by mc-panel; leaving it untouched") {
		t.Fatalf("not logged:\n%s", e.logs.String())
	}
}

// Review SEC-03 #2: a container called mcpanel-router without mc-panel's label is never
// connected to a server's network.
func TestForeignRouterIsNotConnected(t *testing.T) {
	e := newEnv(t)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.18.0.0/16")
	e.d.AddContainer(instances.RouterName, docker.ContainerConfig{Image: "x", HostConfig: docker.HostConfig{NetworkMode: "bridge"}}, true)
	e.ensure(t, ensureReq(1, "survival", 1))
	if _, ok := e.d.Container("mcpanel-mc-1"); !ok {
		t.Fatal("instance not created")
	}
	if got := e.container(t, instances.RouterName).Networks; slices.Contains(got, "mcpanel-net-1") {
		t.Fatalf("foreign router connected: %v", got)
	}
	e.m.Tick(context.Background())
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"bridge"}) {
		t.Fatalf("foreign router networks = %v", got)
	}
}

// Review SEC-03 #3: a managed network with IPv6 is not used.
func TestIPv6InstanceNetworkIsRefused(t *testing.T) {
	e := newEnv(t)
	e.d.AddNetwork("mcpanel-net-1", "bridge", map[string]string{instances.LabelNetwork: "1", instances.LabelInstance: "1"}, "10.213.0.0/28")
	e.d.SetNetworkIPv6("mcpanel-net-1", true)
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "IPv6") {
		t.Fatalf("status = %+v", s)
	}
	e.d.AddNetwork("mcpanel-net-2", "bridge", map[string]string{instances.LabelNetwork: "1", instances.LabelInstance: "2"}, "10.213.0.16/28", "fd00:2::/64")
	e.ensure(t, ensureReq(2, "creative", 1))
	if s := e.status(t, 2); s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "IPv6") {
		t.Fatalf("status = %+v", s)
	}
}

// Without a working egress filter the loop neither moves a container of an older agent
// onto a new network nor connects the router to a network the filter does not cover.
func TestNoMoveAndNoRouterConnectWithoutFilter(t *testing.T) {
	e, fe := newEgressEnv(t)
	e.d.AddImage(routerPinned)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.18.0.0/16")
	e.m.Tick(context.Background()) // filter for mcpanel-mc, router created
	fe.fail(errors.New("netlink: operation not permitted"))
	e.legacy(t, 1, "survival", false)
	e.m.Tick(context.Background())
	if got := e.container(t, "mcpanel-mc-1").Networks; !slices.Equal(got, []string{"mcpanel-mc"}) {
		t.Fatalf("legacy container moved without filter: %v", got)
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"mcpanel-mc"}) {
		t.Fatalf("router networks = %v", got)
	}

	// A server network whose (changed) subnet the filter does not cover gets no router.
	fe.fail(nil)
	e.ensure(t, ensureReq(2, "creative", 1))
	if !slices.Contains(e.container(t, instances.RouterName).Networks, "mcpanel-net-2") {
		t.Fatalf("router not on mcpanel-net-2: %v", e.container(t, instances.RouterName).Networks)
	}
	fe.fail(errors.New("netlink: operation not permitted"))
	e.d.SetNetworkSubnets("mcpanel-net-2", "10.213.7.0/28")
	if err := docker.New(e.d.Socket).NetworkDisconnect(context.Background(), "mcpanel-net-2", instances.RouterName); err != nil {
		t.Fatal(err)
	}
	e.m.Tick(context.Background())
	if slices.Contains(e.container(t, instances.RouterName).Networks, "mcpanel-net-2") {
		t.Fatal("router connected to a network the filter does not cover")
	}
}

// fakeDisabledEgress behaves like the firewall with container_egress_deny = []: every
// call succeeds and the filter always counts as in place.
type fakeDisabledEgress struct{}

func (fakeDisabledEgress) SetEgressSubnets(context.Context, []netip.Prefix) error { return nil }
func (fakeDisabledEgress) EgressInPlace(context.Context, []netip.Prefix) (bool, error) {
	return true, nil
}

func TestNetworksWithEgressFilterOff(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.Egress = fakeDisabledEgress{} })
	e.d.AddImage(routerPinned)
	e.d.AddNetwork(instances.RouterNetwork, "bridge", map[string]string{instances.LabelNetwork: "1"}, "172.18.0.0/16")
	e.legacy(t, 1, "survival", false)
	e.ensure(t, ensureReq(2, "creative", 1))
	e.m.Tick(context.Background())
	if got := e.container(t, "mcpanel-mc-1").Networks; !slices.Equal(got, []string{"mcpanel-net-1"}) {
		t.Fatalf("legacy container networks = %v", got)
	}
	if got := e.container(t, instances.RouterName).Networks; !slices.Equal(got, []string{"mcpanel-mc", "mcpanel-net-1", "mcpanel-net-2"}) {
		t.Fatalf("router networks = %v", got)
	}
	e.power(t, 2, agentapi.PowerStart)
	if s := e.status(t, 2); s.State != agentapi.StateRunning || s.LastError != "" {
		t.Fatalf("status = %+v", s)
	}
}

// An exhausted pool is logged once, and again when subnets are free.
func TestPoolExhaustionLoggedOnce(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.NetworkPool = netip.MustParsePrefix("10.213.0.0/28") })
	e.ensure(t, ensureReq(1, "alpha", 1))
	e.ensure(t, ensureReq(2, "bravo", 1))
	e.ensure(t, ensureReq(3, "charlie", 1))
	if n := strings.Count(e.logs.String(), "instance_network_pool is exhausted"); n != 1 {
		t.Fatalf("exhaustion logged %d times:\n%s", n, e.logs.String())
	}
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	e.ensure(t, ensureReq(2, "bravo", 1))
	if !strings.Contains(e.logs.String(), "has free subnets again") {
		t.Fatalf("recovery not logged:\n%s", e.logs.String())
	}
}

func TestParseRoutes(t *testing.T) {
	const table = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	100	00000000	0	0	0
eth0	0001A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
wg0	0000080A	00000000	0001	0	0	0	0000FFFF	0	0	0
tun0	00D5000A	00000000	0000	0	0	0	0000FFFF	0	0	0
br-0123456789ab	00D5000A	00000000	0001	0	0	0	F0FFFFFF	0	0	0
lo	0000007F	00000000	0001	0	0	0	000000FF	0	0	0
`
	got, err := instances.ParseRoutes(strings.NewReader(table), map[string]bool{"br-0123456789ab": true})
	if err != nil || !slices.Equal(got, prefixes("192.168.1.0/24", "10.8.0.0/16")) {
		t.Fatalf("routes = %v, %v", got, err)
	}
	if _, err := instances.ParseRoutes(strings.NewReader("h\neth0 XYZ 0 0001 0 0 0 00FFFFFF\n"), nil); err == nil {
		t.Fatal("a malformed line must fail")
	}
}

func TestNetworkHostInterface(t *testing.T) {
	id := strings.Repeat("ab", 32)
	for _, c := range []struct {
		n    docker.Network
		want string
	}{
		{docker.Network{ID: id, Driver: "bridge"}, "br-abababababab"},
		{docker.Network{ID: id, Driver: "bridge", BridgeName: "docker0"}, "docker0"},
		{docker.Network{ID: id, Driver: "host"}, ""},
		{docker.Network{ID: "x", Driver: "bridge"}, ""},
	} {
		if got := c.n.HostInterface(); got != c.want {
			t.Errorf("HostInterface(%+v) = %q, want %q", c.n, got, c.want)
		}
	}
}
