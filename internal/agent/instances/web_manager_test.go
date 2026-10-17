package instances_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

const webPinned = "caddy@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f"

// fakeWeb records the rules the manager hands to the bridge filter. On every SetWebRules it
// checks the fail-closed invariant against the fake daemon: mcpanel-web is on no server
// network whose subnet the new rules do not cover.
type fakeWeb struct {
	d *dockertest.Daemon
	// foreign are networks called mcpanel-net-* that mc-panel does not manage.
	foreign []string

	mu         sync.Mutex
	sets       [][]agentapi.WebRule
	checks     int
	removed    bool
	err        error
	violations []string
}

func (f *fakeWeb) SetWebRules(_ context.Context, rules []agentapi.WebRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if v := uncovered(f.d, rules, f.foreign); v != "" {
		f.violations = append(f.violations, "SetWebRules: "+v)
	}
	f.sets = append(f.sets, slices.Clone(rules))
	f.removed = false
	return nil
}

func (f *fakeWeb) WebRulesInPlace(context.Context, []agentapi.WebRule) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return !f.removed, f.err
}

func (f *fakeWeb) last() []agentapi.WebRule {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sets) == 0 {
		return nil
	}
	return f.sets[len(f.sets)-1]
}

func (f *fakeWeb) setCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets)
}

func (f *fakeWeb) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// uncovered names a server network mcpanel-web is on without a rule for its subnet and its
// address ("" = none); foreign networks are skipped.
func uncovered(d *dockertest.Daemon, rules []agentapi.WebRule, foreign []string) string {
	c, ok := d.Container(instances.WebName)
	if !ok {
		return ""
	}
	for _, n := range c.Networks {
		if !strings.HasPrefix(n, instances.InstanceNetworkPrefix) || slices.Contains(foreign, n) {
			continue
		}
		subnets := d.NetworkSubnets(n)
		if !slices.ContainsFunc(rules, func(r agentapi.WebRule) bool {
			return slices.Contains(subnets, r.Dest.String()) && c.IPs[n] == r.Source.String()
		}) {
			return fmt.Sprintf("%s on %s (%v, address %q) without a rule", instances.WebName, n, subnets, c.IPs[n])
		}
	}
	return ""
}

// fakeAsk answers like Caddy's ask server: 200 for a name in the written config.
type fakeAsk struct {
	dir string

	mu    sync.Mutex
	down  bool
	asked []string
}

func (a *fakeAsk) check(_ context.Context, fqdn string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, fqdn)
	if a.down {
		return false, errors.New("connection refused")
	}
	b, err := os.ReadFile(filepath.Join(instances.WebDir(a.dir), "caddy.json"))
	if err != nil {
		return false, err
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct {
						Match []struct {
							Query map[string][]string `json:"query"`
						} `json:"match"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return false, err
	}
	for _, r := range cfg.Apps.HTTP.Servers["ask"].Routes {
		for _, m := range r.Match {
			if slices.Contains(m.Query["domain"], fqdn) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (a *fakeAsk) setDown(down bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.down = down
}

func (a *fakeAsk) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.asked)
}

type webEnv struct {
	*env
	fw  *fakeWeb
	ask *fakeAsk
}

func newWebEnv(t *testing.T, opts ...func(*instances.Config)) *webEnv {
	t.Helper()
	w := &webEnv{}
	e := newEnv(t, append([]func(*instances.Config){func(c *instances.Config) {
		w.fw = &fakeWeb{}
		w.ask = &fakeAsk{dir: c.InstancesDir}
		c.Web = w.fw
		c.WebAsk = w.ask.check
	}}, opts...)...)
	w.env = e
	w.fw.d = e.d
	t.Cleanup(func() {
		w.fw.mu.Lock()
		defer w.fw.mu.Unlock()
		for _, v := range w.fw.violations {
			t.Errorf("fail-closed violation: %s", v)
		}
	})
	return w
}

func webRoute(id int64, name string, port int) agentapi.WebRoute {
	return agentapi.WebRoute{ID: id, FQDN: name + ".mc.example.com", Port: port}
}

func (e *webEnv) put(t *testing.T, routes ...agentapi.WebRoute) agentapi.WebRoutesResponse {
	t.Helper()
	resp, err := e.m.SetWebRoutes(context.Background(), agentapi.WebRoutesRequest{Routes: append([]agentapi.WebRoute{}, routes...)})
	if err != nil {
		t.Fatalf("SetWebRoutes: %v", err)
	}
	if len(resp.Routes) != len(routes) {
		t.Fatalf("response = %+v for %d routes", resp, len(routes))
	}
	return resp
}

// putWithin is put that fails the test when SetWebRoutes does not answer within d.
func (e *webEnv) putWithin(t *testing.T, d time.Duration, routes ...agentapi.WebRoute) agentapi.WebRoutesResponse {
	t.Helper()
	type result struct {
		resp agentapi.WebRoutesResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := e.m.SetWebRoutes(context.Background(), agentapi.WebRoutesRequest{Routes: append([]agentapi.WebRoute{}, routes...)})
		ch <- result{resp, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil || len(r.resp.Routes) != len(routes) {
			t.Fatalf("SetWebRoutes = %+v, %v", r.resp, r.err)
		}
		return r.resp
	case <-time.After(d):
		t.Fatalf("SetWebRoutes did not answer within %v", d)
		return agentapi.WebRoutesResponse{}
	}
}

// statuses answers the status of every route as the panel sees it (a repeated PUT).
func (e *webEnv) statuses(t *testing.T, routes ...agentapi.WebRoute) map[int64]agentapi.WebRouteStatus {
	t.Helper()
	out := map[int64]agentapi.WebRouteStatus{}
	for _, s := range e.put(t, routes...).Routes {
		out[s.ID] = s
	}
	return out
}

func (e *webEnv) tick(t *testing.T) {
	t.Helper()
	e.m.Tick(context.Background())
	e.fw.mu.Lock()
	foreign := slices.Clone(e.fw.foreign)
	e.fw.mu.Unlock()
	if v := uncovered(e.d, e.fw.last(), foreign); v != "" {
		t.Fatalf("after tick: %s", v)
	}
}

func (e *webEnv) webConfig(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, "web", "caddy.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func rule(src, dest string, port uint16) agentapi.WebRule {
	return agentapi.WebRule{Source: netip.MustParseAddr(src), Dest: netip.MustParsePrefix(dest), Port: port}
}

// The first PUT creates the network, the config, the container and the connection with the
// fixed address; the egress filter covers the web network before the container exists.
func TestWebFirstPutCreatesRouter(t *testing.T) {
	fe := &fakeEgress{}
	e := newWebEnv(t, func(c *instances.Config) { c.Egress = fe })
	e.ensure(t, ensureReq(1, "survival", 1))
	r := webRoute(1, "survival", 8100)
	if got := e.put(t, r).Routes[0]; got != (agentapi.WebRouteStatus{ID: 1, Status: agentapi.WebPending}) {
		t.Fatalf("first answer = %+v", got)
	}
	if e.d.HasNetwork(instances.WebNetwork) || e.fw.setCount() != 0 {
		t.Fatal("SetWebRoutes must not touch Docker or the firewall itself")
	}
	e.tick(t)

	webSubnet := e.d.NetworkSubnets(instances.WebNetwork)
	if !slices.Equal(webSubnet, []string{"10.213.0.16/28"}) {
		t.Fatalf("web network subnet = %v", webSubnet)
	}
	if got := fe.snapshot(); !slices.Contains(got[len(got)-1], netip.MustParsePrefix("10.213.0.16/28")) {
		t.Fatalf("egress subnets = %v, want the web network covered", got)
	}
	c := e.container(t, instances.WebName)
	if c.Status != "running" || c.Config.Labels[instances.LabelWeb] != "1" || c.Config.Labels[instances.LabelWebTemplate] == "" || c.Config.Image != webPinned {
		t.Fatalf("web container = %+v", c)
	}
	if !slices.Equal(c.Networks, []string{"mcpanel-net-1", "mcpanel-web"}) || c.IPs["mcpanel-net-1"] != "10.213.0.14" {
		t.Fatalf("web networks = %v, addresses %v", c.Networks, c.IPs)
	}
	if got := e.fw.last(); !slices.Equal(got, []agentapi.WebRule{rule("10.213.0.14", "10.213.0.0/28", 8100)}) {
		t.Fatalf("rules = %v", got)
	}
	want, err := instances.RenderWebConfig([]agentapi.WebRoute{r}, netip.MustParsePrefix("10.213.0.16/28"))
	if err != nil {
		t.Fatal(err)
	}
	if got := e.webConfig(t); got != string(want) {
		t.Fatalf("config =\n%s\nwant\n%s", got, want)
	}
	if got := e.statuses(t, r)[1]; got.Status != agentapi.WebActive {
		t.Fatalf("status after apply = %+v", got)
	}
	// A changed port is pending again until it is applied.
	r.Port = 8123
	if got := e.statuses(t, r)[1]; got.Status != agentapi.WebPending {
		t.Fatalf("status of a changed route = %+v", got)
	}
	e.tick(t)
	if got := e.fw.last(); !slices.Equal(got, []agentapi.WebRule{rule("10.213.0.14", "10.213.0.0/28", 8123)}) {
		t.Fatalf("rules after the port change = %v", got)
	}
	if got := e.statuses(t, r)[1]; got.Status != agentapi.WebActive {
		t.Fatalf("status after the port change = %+v", got)
	}
}

// The rule for a server network exists before mcpanel-web is connected to it.
func TestWebRulesBeforeConnect(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.put(t, webRoute(1, "survival", 8100))
	arrived, release := e.d.Hold(dockertest.OpNetworkConnect)
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.m.Tick(context.Background())
	}()
	select {
	case target := <-arrived:
		if target != "mcpanel-net-1" {
			t.Errorf("first connect to %s", target)
		}
		if got := e.fw.last(); !slices.Equal(got, []agentapi.WebRule{rule("10.213.0.14", "10.213.0.0/28", 8100)}) {
			t.Errorf("rules at the connect = %v", got)
		}
	case <-done:
		t.Fatal("no connect")
	}
	release()
	<-done
	if c := e.container(t, instances.WebName); !slices.Contains(c.Networks, "mcpanel-net-1") {
		t.Fatalf("web networks = %v", c.Networks)
	}
}

// PUT never waits for Docker: it answers at once while the first apply pulls the image
// (Review focus 3), and the route becomes active once the pull is done.
func TestWebPutDoesNotWaitForPull(t *testing.T) {
	e := newWebEnv(t)
	e.d.AddImage(routerPinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		e.m.Run(ctx)
	}()
	defer func() {
		cancel()
		<-runDone
	}()
	arrived, release := e.d.Hold(dockertest.OpPull)
	defer release()
	r := webRoute(1, "survival", 8100)
	start := time.Now()
	if got := e.putWithin(t, time.Second, r).Routes[0]; got.Status != agentapi.WebPending {
		t.Fatalf("answer = %+v", got)
	}
	select {
	case ref := <-arrived:
		if !strings.Contains(ref, "caddy") {
			t.Fatalf("pull of %s", ref)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the apply loop did not start the pull")
	}
	// The pull hangs; another PUT still answers at once.
	if got := e.putWithin(t, time.Second, r).Routes[0]; got.Status != agentapi.WebPending {
		t.Fatalf("answer during the pull = %+v", got)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("PUT took %v while the pull blocked", d)
	}
	release()
	deadline := time.Now().Add(10 * time.Second)
	for e.statuses(t, r)[1].Status != agentapi.WebActive {
		if time.Now().After(deadline) {
			t.Fatalf("route not active after the pull: %+v", e.statuses(t, r)[1])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A route missing from the next list is disconnected (before its rule goes) and leaves the
// config; networks mc-panel does not manage are never touched.
func TestWebSecondPutDisconnectsRemovedRoute(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.ensure(t, ensureReq(2, "creative", 1))
	r1, r2 := webRoute(1, "survival", 8100), webRoute(2, "creative", 8200)
	e.put(t, r1, r2)
	e.tick(t)
	if c := e.container(t, instances.WebName); !slices.Equal(c.Networks, []string{"mcpanel-net-1", "mcpanel-net-2", "mcpanel-web"}) {
		t.Fatalf("web networks = %v", c.Networks)
	}
	e.d.AddNetwork("mcpanel-net-7", "bridge", nil, "10.213.1.0/28") // unlabelled: foreign
	e.fw.mu.Lock()
	e.fw.foreign = []string{"mcpanel-net-7"}
	e.fw.mu.Unlock()
	e.d.Connect(instances.WebName, "mcpanel-net-7")
	e.d.AddNetwork("someone", "bridge", nil, "10.213.2.0/28")
	e.d.Connect(instances.WebName, "someone")

	e.put(t, r2)
	e.tick(t)
	c := e.container(t, instances.WebName)
	if !slices.Equal(c.Networks, []string{"mcpanel-net-2", "mcpanel-net-7", "mcpanel-web", "someone"}) {
		t.Fatalf("web networks = %v", c.Networks)
	}
	if got := e.fw.last(); !slices.Equal(got, []agentapi.WebRule{rule("10.213.0.30", "10.213.0.16/28", 8200)}) {
		t.Fatalf("rules = %v", got)
	}
	if cfg := e.webConfig(t); strings.Contains(cfg, "survival") || !strings.Contains(cfg, "creative.mc.example.com") {
		t.Fatalf("config =\n%s", cfg)
	}
	if got := e.statuses(t, r2)[2]; got.Status != agentapi.WebActive {
		t.Fatalf("status = %+v", got)
	}
}

// An empty list removes container, network and rules; a fresh manager (agent restart) that
// has not received a list yet touches nothing (Review focus 1).
func TestWebEmptyListRemovesAndNilListKeeps(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.put(t, webRoute(1, "survival", 8100))
	e.tick(t)
	before := e.container(t, instances.WebName)
	config := e.webConfig(t)

	fw2 := &fakeWeb{d: e.d}
	m2 := instances.New(e.config(func(c *instances.Config) { c.Web = fw2; c.WebAsk = e.ask.check }))
	t.Cleanup(m2.Close)
	m2.Tick(context.Background())
	m2.Tick(context.Background())
	after := e.container(t, instances.WebName)
	if after.ID != before.ID || !slices.Equal(after.Networks, before.Networks) || after.Status != "running" {
		t.Fatalf("restarted agent changed the web router: %+v → %+v", before, after)
	}
	if fw2.setCount() != 0 || e.webConfig(t) != config || !e.d.HasNetwork(instances.WebNetwork) {
		t.Fatal("restarted agent changed rules, config or network before its first list")
	}

	if _, err := m2.SetWebRoutes(context.Background(), agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{}}); err != nil {
		t.Fatal(err)
	}
	m2.Tick(context.Background())
	if _, ok := e.d.Container(instances.WebName); ok || e.d.HasNetwork(instances.WebNetwork) {
		t.Fatalf("empty list left container or network: %v", e.d.Networks())
	}
	if got := fw2.last(); got == nil || len(got) != 0 {
		t.Fatalf("rules after the empty list = %v, want an empty set", got)
	}
	if !e.d.HasNetwork("mcpanel-net-1") {
		t.Fatal("the server network must stay")
	}
}

// A container or network that only shares the name is never touched.
func TestWebForeignContainerIsNeverTouched(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	foreign := e.d.AddContainer(instances.WebName, docker.ContainerConfig{Image: "x"}, true)
	r := webRoute(1, "survival", 8100)
	e.put(t, r)
	e.tick(t)
	if got := e.statuses(t, r)[1]; got.Status != agentapi.WebError || !strings.Contains(got.Error, "not managed by mc-panel") {
		t.Fatalf("status = %+v", got)
	}
	c := e.container(t, instances.WebName)
	if c.ID != foreign.ID || slices.Contains(c.Networks, "mcpanel-net-1") {
		t.Fatalf("foreign container changed: %+v", c)
	}
	e.put(t)
	e.tick(t)
	if c := e.container(t, instances.WebName); c.ID != foreign.ID {
		t.Fatal("foreign container removed")
	}

	// A foreign network called mcpanel-web is not adopted either.
	e2 := newWebEnv(t)
	e2.ensure(t, ensureReq(1, "survival", 1))
	e2.d.AddNetwork(instances.WebNetwork, "bridge", map[string]string{"com.example": "x"}, "10.213.3.0/28")
	e2.put(t, r)
	e2.tick(t)
	if got := e2.statuses(t, r)[1]; got.Status != agentapi.WebError || !strings.Contains(got.Error, "not managed") {
		t.Fatalf("status with a foreign network = %+v", got)
	}
	if _, ok := e2.d.Container(instances.WebName); ok {
		t.Fatal("container created on a foreign network")
	}
	e2.put(t)
	e2.tick(t)
	if !e2.d.HasNetwork(instances.WebNetwork) {
		t.Fatal("foreign network removed")
	}
}

// A route whose server has no container fails alone.
func TestWebRouteWithoutContainer(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	r1, r5 := webRoute(1, "survival", 8100), webRoute(5, "ghost", 8100)
	e.put(t, r1, r5)
	e.tick(t)
	st := e.statuses(t, r1, r5)
	if st[1].Status != agentapi.WebActive || st[5].Status != agentapi.WebError || st[5].Error != "server container missing" {
		t.Fatalf("statuses = %+v", st)
	}
	if e.d.HasNetwork("mcpanel-net-5") || strings.Contains(e.webConfig(t), "ghost") {
		t.Fatal("route without container was applied")
	}
}

// Without the port restriction nothing is connected, and existing connections are cut.
func TestWebRulesFailureConnectsNothing(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.ensure(t, ensureReq(2, "creative", 1))
	r1, r2 := webRoute(1, "survival", 8100), webRoute(2, "creative", 8100)
	e.fw.fail(errors.New("nft: netlink error"))
	e.put(t, r1)
	e.tick(t)
	if got := e.statuses(t, r1)[1]; got.Status != agentapi.WebError || !strings.Contains(got.Error, "port restriction not applied") {
		t.Fatalf("status = %+v", got)
	}
	if c, ok := e.d.Container(instances.WebName); ok && slices.Contains(c.Networks, "mcpanel-net-1") {
		t.Fatal("connected without rules")
	}

	e.fw.fail(nil)
	e.tick(t)
	if c := e.container(t, instances.WebName); !slices.Contains(c.Networks, "mcpanel-net-1") {
		t.Fatalf("not connected after the filter works again: %v", c.Networks)
	}
	e.fw.fail(errors.New("nft: netlink error"))
	e.put(t, r1, r2)
	e.tick(t)
	if c := e.container(t, instances.WebName); !slices.Equal(c.Networks, []string{"mcpanel-web"}) {
		t.Fatalf("web networks after a failed rule update = %v", c.Networks)
	}
}

// Rules removed from outside are reinstalled.
func TestWebRulesAreRepaired(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.put(t, webRoute(1, "survival", 8100))
	e.tick(t)
	e.tick(t)
	if n := e.fw.setCount(); n != 1 {
		t.Fatalf("SetWebRules calls = %d, want 1 (unchanged rules are only checked)", n)
	}
	e.fw.mu.Lock()
	e.fw.removed = true
	e.fw.mu.Unlock()
	e.tick(t)
	if n := e.fw.setCount(); n != 2 {
		t.Fatalf("SetWebRules calls = %d, want 2 after an external flush", n)
	}
}

// Deleting a server whose network has mcpanel-web attached removes the network; so does the
// orphan cleanup with only the router and mcpanel-web on it (Review focus 2).
func TestWebAttachedNetworkIsRemoved(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.ensure(t, ensureReq(2, "creative", 1))
	e.put(t, webRoute(1, "survival", 8100), webRoute(2, "creative", 8100))
	e.tick(t)

	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if e.d.HasNetwork("mcpanel-net-1") {
		t.Fatalf("network of the deleted server stays: %v", e.d.Networks())
	}

	// A leftover network whose container vanished outside the agent.
	if err := docker.New(e.d.Socket).Remove(context.Background(), instances.ContainerName(2), true); err != nil {
		t.Fatal(err)
	}
	e.tick(t)
	if e.d.HasNetwork("mcpanel-net-2") {
		t.Fatalf("orphan network with router and web router stays: %v", e.d.Networks())
	}
	if c := e.container(t, instances.WebName); !slices.Equal(c.Networks, []string{"mcpanel-web"}) {
		t.Fatalf("web networks = %v", c.Networks)
	}
}

// The ask endpoint decides: active on 200, pending meanwhile, an error after three rounds.
func TestWebAskCheck(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	r := webRoute(1, "survival", 8100)
	e.put(t, r)
	e.ask.setDown(true)
	for i, want := range []agentapi.WebRouteState{agentapi.WebPending, agentapi.WebPending, agentapi.WebError} {
		e.tick(t)
		if got := e.statuses(t, r)[1]; got.Status != want || (want == agentapi.WebError && got.Error != "web router did not load the config") {
			t.Fatalf("round %d: status = %+v, want %s", i+1, got, want)
		}
	}
	e.ask.setDown(false)
	e.tick(t)
	if got := e.statuses(t, r)[1]; got.Status != agentapi.WebActive {
		t.Fatalf("status = %+v", got)
	}
}

// A down ask endpoint is asked once per round, not once per route.
func TestWebAskStopsAfterFirstFailure(t *testing.T) {
	e := newWebEnv(t)
	var routes []agentapi.WebRoute
	for id := int64(1); id <= 3; id++ {
		name := fmt.Sprintf("srv-%d", id)
		e.ensure(t, ensureReq(id, name, 1))
		routes = append(routes, webRoute(id, name, 8100))
	}
	e.put(t, routes...)
	e.ask.setDown(true)
	before := e.ask.count()
	e.tick(t)
	if n := e.ask.count() - before; n != 1 {
		t.Fatalf("ask requests = %d, want 1", n)
	}
}

// A new template (agent upgrade) replaces the container, which is connected again.
func TestWebTemplateChangeReplacesContainer(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	r := webRoute(1, "survival", 8100)
	e.put(t, r)
	e.tick(t)
	old := e.container(t, instances.WebName)

	fw2 := &fakeWeb{d: e.d}
	m2 := instances.New(e.config(func(c *instances.Config) { c.Web = fw2; c.WebAsk = e.ask.check; c.WebProxyPort = 18200 }))
	t.Cleanup(m2.Close)
	if _, err := m2.SetWebRoutes(context.Background(), agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{r}}); err != nil {
		t.Fatal(err)
	}
	m2.Tick(context.Background())
	c := e.container(t, instances.WebName)
	if c.ID == old.ID || c.Config.HostConfig.PortBindings["8080/tcp"][0].HostPort != "18200" {
		t.Fatalf("container not replaced: %+v", c.Config.HostConfig.PortBindings)
	}
	if !slices.Contains(c.Networks, "mcpanel-net-1") || c.IPs["mcpanel-net-1"] != "10.213.0.14" {
		t.Fatalf("new container networks = %v %v", c.Networks, c.IPs)
	}
	m2.Tick(context.Background())
	if again := e.container(t, instances.WebName); again.ID != c.ID {
		t.Fatal("an unchanged template must keep the container")
	}
}

// An endpoint with another address than the rule's is replaced.
func TestWebWrongAddressIsReplaced(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.put(t, webRoute(1, "survival", 8100))
	e.tick(t)
	if err := docker.New(e.d.Socket).NetworkDisconnect(context.Background(), "mcpanel-net-1", instances.WebName); err != nil {
		t.Fatal(err)
	}
	e.d.Connect(instances.WebName, "mcpanel-net-1") // dynamic address
	e.tick(t)
	if c := e.container(t, instances.WebName); c.IPs["mcpanel-net-1"] != "10.213.0.14" {
		t.Fatalf("addresses = %v", c.IPs)
	}
}

// Invalid lists and an unavailable Docker are refused before anything is stored.
func TestWebSetRoutesRefuses(t *testing.T) {
	e := newWebEnv(t)
	ctx := context.Background()
	if _, err := e.m.SetWebRoutes(ctx, agentapi.WebRoutesRequest{}); err == nil {
		t.Fatal("nil routes accepted")
	}
	if _, err := e.m.SetWebRoutes(ctx, agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{{ID: 1, FQDN: "a.mc.example.com", Port: 25565}}}); err == nil {
		t.Fatal("port 25565 accepted")
	}
	e.d.Fail(dockertest.OpPing, 500, "down")
	if _, err := e.m.SetWebRoutes(ctx, agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{}}); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Docker down: %v", err)
	}
	e.d.Clear()
	e.m.Tick(ctx)
	if e.fw.setCount() != 0 {
		t.Fatal("a refused list was applied")
	}
}

// A busy server (operation in progress) keeps mcpanel-web on its network until the next round,
// and with it its old rule: the restriction never has a gap.
func TestWebBusyServerKeepsItsRule(t *testing.T) {
	e := newWebEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.ensure(t, ensureReq(2, "creative", 1))
	r1, r2 := webRoute(1, "survival", 8100), webRoute(2, "creative", 8200)
	e.put(t, r1, r2)
	e.tick(t)

	arrived, release := e.d.Hold(dockertest.OpStop)
	defer release()
	if err := e.m.Power(context.Background(), 1, agentapi.PowerStop); err != nil {
		t.Fatal(err)
	}
	<-arrived
	e.put(t, r2)
	e.tick(t)
	if c := e.container(t, instances.WebName); !slices.Contains(c.Networks, "mcpanel-net-1") {
		t.Fatalf("web router left the network of a busy server: %v", c.Networks)
	}
	want := []agentapi.WebRule{rule("10.213.0.30", "10.213.0.16/28", 8200), rule("10.213.0.14", "10.213.0.0/28", 8100)}
	if got := e.fw.last(); !slices.Equal(got, want) {
		t.Fatalf("rules while busy = %v, want %v", got, want)
	}
	release()
	e.m.Wait()
	e.tick(t)
	if c := e.container(t, instances.WebName); slices.Contains(c.Networks, "mcpanel-net-1") {
		t.Fatalf("web networks after the operation = %v", c.Networks)
	}
	if got := e.fw.last(); !slices.Equal(got, want[:1]) {
		t.Fatalf("rules after the operation = %v", got)
	}
}
