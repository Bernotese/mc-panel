package instances_test

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

func TestWakerContainerHardening(t *testing.T) {
	got, err := instances.WakerContainer(testHost(), "/usr/local/bin/agent")
	if err != nil {
		t.Fatal(err)
	}
	want := docker.ContainerConfig{
		Image:      "itzg/mc-router@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e",
		Entrypoint: []string{"/mcpanel-waker"},
		Cmd:        []string{"waker", "-listen", ":25565"},
		User:       "65534:65534",
		Env:        []string{"GOMAXPROCS=2", "GOMEMLIMIT=16MiB"},
		Labels:     map[string]string{"mcpanel.waker": "1"},
		HostConfig: docker.HostConfig{
			Binds:          []string{"/usr/local/bin/agent:/mcpanel-waker:ro"},
			NetworkMode:    "mcpanel-mc",
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,
			Memory:         32 << 20,
			MemorySwap:     32 << 20,
			PidsLimit:      32,
			LogConfig:      docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-mc": {}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WakerContainer =\n%+v\nwant\n%+v", got, want)
	}
	// Nothing that reaches Docker may give it more: no ports, no privileges, no other mount.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"PortBindings", "ExposedPorts", "CapAdd", `"Privileged":true`, "Tmpfs", "docker.sock"} {
		if bytes.Contains(b, []byte(bad)) {
			t.Errorf("waker template contains %s: %s", bad, b)
		}
	}
}

func TestWakerContainerRejectsBadBinary(t *testing.T) {
	for _, p := range []string{"", "agent", "/usr/local/bin/../bin/agent", "/opt/a:b", "/opt/a,b", "/opt/a\x00"} {
		if _, err := instances.WakerContainer(testHost(), p); err == nil {
			t.Errorf("WakerContainer(%q) = nil error", p)
		}
	}
}

// TestEntrypointOnlyForWaker: the new Entrypoint field is left out of every other template,
// so their JSON and template hashes stay the same as before the field existed.
func TestEntrypointOnlyForWaker(t *testing.T) {
	router, err := instances.RouterContainer(testHost())
	if err != nil {
		t.Fatal(err)
	}
	web, err := instances.WebContainer(testHost())
	if err != nil {
		t.Fatal(err)
	}
	inst, err := instances.InstanceContainer(testHost(), paperRequest())
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]docker.ContainerConfig{"router": router, "web": web, "instance": inst} {
		b, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("Entrypoint")) {
			t.Errorf("%s template has an Entrypoint: %s", name, b)
		}
	}
}

func TestRoutesWithSleeping(t *testing.T) {
	stopped := func(name string, labels map[string]string) docker.ContainerSummary {
		s := summary(name, labels)
		s.State = "exited"
		return s
	}
	list := []docker.ContainerSummary{
		stopped("mcpanel-mc-1", mcLabels("1", "survival", "survival.mc.example.com")),
		stopped("mcpanel-mc-2", mcLabels("2", "dev-survival", "dev-survival.mc.example.com")),
		summary("mcpanel-mc-3", mcLabels("3", "dev-creative", "dev-creative.mc.example.com")), // running
		{ID: "id-4", Names: []string{"/mcpanel-mc-4"}, Labels: mcLabels("4", "dev-lobby", "dev-lobby.mc.example.com"), State: "created"},
		stopped("mcpanel-mc-5", mcLabels("5", "dev-old", "dev-old.mc.example.com")),
		stopped("mcpanel-mc-6", mcLabels("6", "dev-old", "dev-old.mc.example.com")), // the route goes to 6
	}
	if got, want := instances.RoutesWith(list, nil), instances.Routes(list); !maps.Equal(got, want) {
		t.Fatalf("RoutesWith(nil) = %v, want Routes %v", got, want)
	}
	got := instances.RoutesWith(list, map[string]int64{
		"dev-survival.mc.example.com": 2,
		"dev-creative.mc.example.com": 3, // running: stays on its container
		"dev-lobby.mc.example.com":    4,
		"dev-old.mc.example.com":      5, // the route belongs to 6
		"dev-gone.mc.example.com":     9, // no container
		"survival.mc.example.com":     2, // id of another container
	})
	want := map[string]string{
		"survival.mc.example.com":     "mcpanel-mc-1:25565",
		"dev-survival.mc.example.com": "mcpanel-waker:25565",
		"dev-creative.mc.example.com": "mcpanel-mc-3:25565",
		"dev-lobby.mc.example.com":    "mcpanel-waker:25565",
		"dev-old.mc.example.com":      "mcpanel-mc-6:25565",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("RoutesWith =\n%v\nwant\n%v", got, want)
	}
}

// wakerEnv is an env whose manager has an agent binary (returned) for the waker.
func wakerEnv(t *testing.T, version string) (*env, string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil { //nolint:gosec // G306: test stand-in for the agent binary
		t.Fatal(err)
	}
	return newEnv(t, func(c *instances.Config) { c.AgentBinary, c.AgentVersion = bin, version }), bin
}

func (e *env) setWaker(t *testing.T, targets ...agentapi.WakerTarget) agentapi.WakerResponse {
	t.Helper()
	if targets == nil {
		targets = []agentapi.WakerTarget{}
	}
	resp, err := e.m.SetWaker(context.Background(), agentapi.WakerRequest{Sleeping: targets})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func devTarget(id int64, name string) agentapi.WakerTarget {
	return agentapi.WakerTarget{ID: id, FQDN: name + ".mc.example.com"}
}

func TestSetWakerNoRequestNoChange(t *testing.T) {
	e, _ := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	want := `"dev-a.mc.example.com": "mcpanel-mc-1:25565"`
	if !strings.Contains(e.routes(t), want) {
		t.Fatalf("routes = %s", e.routes(t))
	}
	e.m.Tick(context.Background())
	if !strings.Contains(e.routes(t), want) {
		t.Fatalf("routes after a tick = %s", e.routes(t))
	}
	// An empty list changes no route and creates no waker.
	if resp := e.setWaker(t); resp.Ready || len(resp.Wakes) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	if !strings.Contains(e.routes(t), want) {
		t.Fatalf("routes = %s", e.routes(t))
	}
	if _, ok := e.d.Container(instances.WakerName); ok {
		t.Fatal("waker created for an empty list")
	}
}

func TestSetWakerRejectsForeign(t *testing.T) {
	e, _ := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	e.ensure(t, ensureReq(2, "dev-b", 1))
	// id 1 with the FQDN of 2, an id without container: nothing is routed to the waker.
	resp := e.setWaker(t, agentapi.WakerTarget{ID: 1, FQDN: "dev-b.mc.example.com"}, devTarget(3, "dev-c"))
	if resp.Ready {
		t.Fatalf("resp = %+v", resp)
	}
	if r := e.routes(t); strings.Contains(r, "mcpanel-waker") {
		t.Fatalf("routes = %s", r)
	}
	if _, ok := e.d.Container(instances.WakerName); ok {
		t.Fatal("waker created without a matching target")
	}
}

func TestSetWakerRoutesAndContainer(t *testing.T) {
	e, bin := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	e.ensure(t, ensureReq(2, "survival", 1))

	if resp := e.setWaker(t, devTarget(1, "dev-a")); !resp.Ready || len(resp.Wakes) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	r := e.routes(t)
	if !strings.Contains(r, `"dev-a.mc.example.com": "mcpanel-waker:25565"`) || !strings.Contains(r, `"survival.mc.example.com": "mcpanel-mc-2:25565"`) {
		t.Fatalf("routes = %s", r)
	}
	if k := e.container(t, "mcpanel-router").Kills; len(k) == 0 {
		t.Fatal("router not reloaded")
	}
	w := e.container(t, instances.WakerName)
	want, err := instances.WakerContainer(e.host(), bin)
	if err != nil {
		t.Fatal(err)
	}
	if w.Status != "running" || w.Config.Labels["mcpanel.waker"] != "1" || w.Config.Labels["mcpanel.waker-template"] == "" ||
		!reflect.DeepEqual(w.Config.HostConfig, want.HostConfig) || !slices.Equal(w.Config.Entrypoint, want.Entrypoint) || w.Config.User != "65534:65534" {
		t.Fatalf("waker = %+v", w)
	}

	// The tick keeps the waker route; a second call with the same list changes nothing.
	e.m.Tick(context.Background())
	if !strings.Contains(e.routes(t), `"dev-a.mc.example.com": "mcpanel-waker:25565"`) {
		t.Fatalf("routes after a tick = %s", e.routes(t))
	}
	mark := len(e.d.Calls())
	e.setWaker(t, devTarget(1, "dev-a"))
	for _, c := range e.callsAfter(mark) {
		if strings.HasPrefix(c, "create ") || strings.HasPrefix(c, "kill ") || strings.HasPrefix(c, "remove ") || strings.HasPrefix(c, "start ") {
			t.Fatalf("unchanged list: call %s", c)
		}
	}

	// Started (woken): the route goes back to the container even while it is still listed.
	e.power(t, 1, agentapi.PowerStart)
	e.setWaker(t, devTarget(1, "dev-a"))
	if r := e.routes(t); !strings.Contains(r, `"dev-a.mc.example.com": "mcpanel-mc-1:25565"`) {
		t.Fatalf("routes of a running dev = %s", r)
	}
	// Stopped again but no longer listed: its own container.
	e.power(t, 1, agentapi.PowerStop)
	e.setWaker(t)
	if r := e.routes(t); !strings.Contains(r, `"dev-a.mc.example.com": "mcpanel-mc-1:25565"`) {
		t.Fatalf("routes of an unlisted dev = %s", r)
	}
	// The waker stays up once it was needed.
	if resp := e.setWaker(t); !resp.Ready {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestSetWakerRestartsAndReplaces(t *testing.T) {
	e, bin := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	e.setWaker(t, devTarget(1, "dev-a"))
	first := e.container(t, instances.WakerName)

	e.d.SetState(instances.WakerName, "exited", 2)
	if resp := e.setWaker(t, devTarget(1, "dev-a")); !resp.Ready {
		t.Fatalf("resp = %+v", resp)
	}
	if c := e.container(t, instances.WakerName); c.ID != first.ID || c.Status != "running" {
		t.Fatalf("stopped waker not started again: %+v", c)
	}

	// An agent update (other version, same template) replaces the container.
	cfg := e.config(func(c *instances.Config) { c.AgentBinary, c.AgentVersion = bin, "1.1.0" })
	m2 := instances.New(cfg)
	t.Cleanup(m2.Close)
	if _, err := m2.SetWaker(context.Background(), agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{devTarget(1, "dev-a")}}); err != nil {
		t.Fatal(err)
	}
	c := e.container(t, instances.WakerName)
	if c.ID == first.ID || c.Config.Labels["mcpanel.waker-template"] == first.Config.Labels["mcpanel.waker-template"] {
		t.Fatalf("waker not replaced after an update: %+v", c)
	}
}

func TestSetWakerRefusesForeignContainer(t *testing.T) {
	e, _ := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	e.d.AddContainer(instances.WakerName, docker.ContainerConfig{Image: "busybox", Labels: map[string]string{"x": "1"}}, true)
	if resp := e.setWaker(t, devTarget(1, "dev-a")); resp.Ready {
		t.Fatalf("foreign container reported ready: %+v", resp)
	}
	if c := e.container(t, instances.WakerName); c.Config.Image != "busybox" {
		t.Fatalf("foreign container changed: %+v", c)
	}
}

func TestSetWakerParsesWakes(t *testing.T) {
	e, _ := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	e.ensure(t, ensureReq(2, "dev-b", 1))
	e.ensure(t, ensureReq(3, "survival", 1))
	targets := []agentapi.WakerTarget{devTarget(1, "dev-a"), devTarget(2, "dev-b")}
	// A line written before the first call (e.g. before an agent restart) is not a wake.
	e.clk.Set(e.d.AppendLog(instances.WakerName, docker.Stdout, "wake dev-b.mc.example.com"))
	e.setWaker(t, targets...)
	if resp := e.setWaker(t, targets...); len(resp.Wakes) != 0 {
		t.Fatalf("old line reported: %+v", resp)
	}

	for _, l := range []struct {
		stream docker.StreamType
		line   string
	}{
		{docker.Stdout, "wake dev-a.mc.example.com"},
		{docker.Stdout, "wake survival.mc.example.com"}, // not sleeping
		{docker.Stdout, "garbage"},
		{docker.Stdout, "wake " + strings.Repeat("a", 300) + ".mc.example.com"},
		{docker.Stdout, "wake DEV-B.mc.example.com"},
		{docker.Stdout, " wake dev-b.mc.example.com"},
		{docker.Stdout, "wake dev-b.mc.example.com extra"},
		{docker.Stderr, "wake dev-b.mc.example.com"},
		{docker.Stdout, "wake dev-a.mc.example.com"}, // twice: one id
		{docker.Stdout, "wake dev-c.mc.example.com"}, // not listed
	} {
		e.d.AppendLog(instances.WakerName, l.stream, l.line)
	}
	if resp := e.setWaker(t, targets...); !resp.Ready || !slices.Equal(resp.Wakes, []int64{1}) {
		t.Fatalf("resp = %+v, want wakes [1]", resp)
	}
	// Lines are read once.
	if resp := e.setWaker(t, targets...); len(resp.Wakes) != 0 {
		t.Fatalf("lines read twice: %+v", resp)
	}
	e.d.AppendLog(instances.WakerName, docker.Stdout, "wake dev-b.mc.example.com")
	e.d.AppendLog(instances.WakerName, docker.Stdout, "wake dev-a.mc.example.com")
	if resp := e.setWaker(t, targets...); !slices.Equal(resp.Wakes, []int64{1, 2}) {
		t.Fatalf("resp = %+v, want wakes [1 2]", resp)
	}
	// A wake of a dev that is no longer listed is dropped.
	e.d.AppendLog(instances.WakerName, docker.Stdout, "wake dev-b.mc.example.com")
	if resp := e.setWaker(t, devTarget(1, "dev-a")); len(resp.Wakes) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestSetWakerLogLimit(t *testing.T) {
	e, _ := wakerEnv(t, "1.0.0")
	e.ensure(t, ensureReq(1, "dev-a", 1))
	targets := []agentapi.WakerTarget{devTarget(1, "dev-a")}
	e.setWaker(t, targets...)
	filler := strings.Repeat("x", 1000)
	for range 100 { // about 100 KiB of noise before the wake line
		e.d.AppendLog(instances.WakerName, docker.Stdout, filler)
	}
	e.d.AppendLog(instances.WakerName, docker.Stdout, "wake dev-a.mc.example.com")
	var wakes []int64
	for range 3 {
		wakes = append(wakes, e.setWaker(t, targets...).Wakes...)
	}
	if !slices.Equal(wakes, []int64{1}) {
		t.Fatalf("wakes over three calls = %v, want [1]", wakes)
	}
}
