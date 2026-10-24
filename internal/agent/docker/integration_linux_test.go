//go:build linux && dockerintegration

// Integration tests against the real Docker daemon. Build as the normal user, run as root
// (Docker socket, chown of the data directory):
//
//	go test -tags dockerintegration -exec sudo -count=1 -v -run Integration ./internal/agent/docker/
//
// TestIntegrationClientLifecycle uses its own names and runs anywhere. The manager test
// uses the real names (mcpanel-mc-<id>, mcpanel-router, networks mcpanel-mc and mcpanel-net-<id>); on a host
// where they exist (e.g. a host with a running agent) it is skipped, with
// MCPANEL_IT_STRICT=1 (CI) it fails instead:
//
//	go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" -count=1 ./internal/agent/...
package docker_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// testImage is a small multi-arch image (verified on Docker Hub 2026-10-10: tag 1.37.0 = 1.37).
const testImage = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

// testCmd keeps the container running and exits on SIGTERM. As PID 1 without a
// handler, SIGHUP is ignored, like mc-router reloading its routes.
var testCmd = []string{"sh", "-c", "trap 'exit 0' TERM; while true; do sleep 1; done"}

const nobody = 65534

func dockerSocket() string {
	if s := os.Getenv("MCPANEL_DOCKER_SOCKET"); s != "" {
		return s
	}
	return "/var/run/docker.sock"
}

func requireDocker(t *testing.T) *docker.Client {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("dockerintegration tests need root (Docker socket, chown)")
	}
	c := docker.New(dockerSocket())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Docker not reachable at %s: %v", dockerSocket(), err)
	}
	pinned := pinnedRef(t, testImage)
	ok, err := c.ImageExists(context.Background(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if err := c.PullImage(context.Background(), pinned); err != nil {
			t.Fatalf("pull %s: %v", testImage, err)
		}
	}
	return c
}

func pinnedRef(t *testing.T, ref string) string {
	t.Helper()
	r, err := docker.ParseImageRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	return r.Pinned()
}

// rawRequest talks to the daemon directly for calls the client deliberately lacks.
func rawRequest(t *testing.T, method, path string) int {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", dockerSocket())
		},
	}}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://docker"+docker.APIVersion+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func forceRemove(t *testing.T, c *docker.Client, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := c.Remove(context.Background(), n, true); err != nil && !docker.IsNotFound(err) {
			t.Errorf("cleanup %s: %v", n, err)
		}
	}
}

func TestIntegrationClientLifecycle(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	name := fmt.Sprintf("mcpanel-it-%d", os.Getpid())
	label := "mcpanel.integration"
	network := name + "-net"
	t.Cleanup(func() {
		forceRemove(t, c, name)
		if code := rawRequest(t, http.MethodDelete, "/networks/"+network); code != http.StatusNoContent && code != http.StatusNotFound {
			t.Errorf("cleanup network: HTTP %d", code)
		}
	})

	for range 2 {
		if err := c.EnsureNetwork(ctx, network, map[string]string{label: "1"}); err != nil {
			t.Fatalf("EnsureNetwork: %v", err)
		}
	}
	cfg := docker.ContainerConfig{
		Image: pinnedRef(t, testImage), Cmd: testCmd, User: fmt.Sprintf("%d:%d", nobody, nobody),
		Labels: map[string]string{label: name}, Hostname: "it", StopTimeout: 5,
		HostConfig: docker.HostConfig{
			NetworkMode: network, RestartPolicy: docker.RestartPolicy{Name: "no"},
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
			Memory: 64 << 20, MemorySwap: 64 << 20, PidsLimit: 64,
			LogConfig: docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m", "max-file": "1"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{network: {}}},
	}
	id, err := c.CreateContainer(ctx, name, cfg)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if _, err := c.CreateContainer(ctx, name, cfg); !docker.IsConflict(err) {
		t.Fatalf("duplicate create = %v, want conflict", err)
	}
	info, err := c.Inspect(ctx, name)
	if err != nil || info.ID != id || info.State.Status != "created" || info.Config.Labels[label] != name {
		t.Fatalf("Inspect after create = %+v, %v", info, err)
	}

	if err := c.Start(ctx, name); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Start(ctx, name); err != nil {
		t.Fatalf("second Start (304) must succeed: %v", err)
	}
	info, err = c.Inspect(ctx, name)
	if err != nil || !info.State.Running {
		t.Fatalf("Inspect after start = %+v, %v", info, err)
	}
	if started, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err != nil || time.Since(started) > time.Minute {
		t.Fatalf("StartedAt = %q, %v", info.State.StartedAt, err)
	}

	list, err := c.List(ctx, label)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, s := range list {
		if _, ok := s.Labels[label]; !ok {
			t.Errorf("label filter returned %v", s.Names)
		}
		if s.HasName(name) {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("List(%s) = %+v", label, list)
	}

	if err := c.Kill(ctx, name, "SIGHUP"); err != nil {
		t.Fatalf("Kill SIGHUP: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if info, err := c.Inspect(ctx, name); err != nil || !info.State.Running {
		t.Fatalf("SIGHUP must not stop the container: %+v, %v", info.State, err)
	}

	stats, err := c.Stats(ctx, name)
	if err != nil || stats.MemoryStats.Limit != 64<<20 || stats.MemoryStats.Usage == 0 || stats.CPUStats.SystemCPUUsage == 0 {
		t.Fatalf("Stats = %+v, %v", stats, err)
	}

	if err := c.Restart(ctx, name, 5); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	for range 2 {
		if err := c.Stop(ctx, name, 5); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	if info, err := c.Inspect(ctx, name); err != nil || info.State.Running || info.State.Status != "exited" {
		t.Fatalf("after stop: %+v, %v", info.State, err)
	}
	if err := c.Remove(ctx, name, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := c.Inspect(ctx, name); !docker.IsNotFound(err) {
		t.Fatalf("Inspect after remove = %v", err)
	}
}

func removeNetworks(t *testing.T, c *docker.Client, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := c.RemoveNetwork(context.Background(), n); err != nil {
			t.Errorf("cleanup network %s: %v", n, err)
		}
	}
}

// requireCleanHost skips (or, with MCPANEL_IT_STRICT=1, fails) when mc-panel resources exist.
func requireCleanHost(t *testing.T, c *docker.Client) {
	t.Helper()
	ctx := context.Background()
	var dirty []string
	if list, err := c.List(ctx, instances.LabelInstance); err != nil || len(list) > 0 {
		dirty = append(dirty, fmt.Sprintf("containers with %s (%d, %v)", instances.LabelInstance, len(list), err))
	}
	if _, err := c.Inspect(ctx, instances.RouterName); !docker.IsNotFound(err) {
		dirty = append(dirty, "container "+instances.RouterName)
	}
	if nets, err := c.ListNetworks(ctx, instances.LabelNetwork); err != nil || len(nets) > 0 {
		dirty = append(dirty, fmt.Sprintf("networks with %s (%d, %v)", instances.LabelNetwork, len(nets), err))
	}
	if len(dirty) == 0 {
		return
	}
	msg := fmt.Sprintf("host is not clean (%s); stop the agent and remove them, or run in CI", strings.Join(dirty, ", "))
	if os.Getenv("MCPANEL_IT_STRICT") == "1" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func TestIntegrationManagerCycle(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	requireCleanHost(t, c)
	t.Cleanup(func() {
		forceRemove(t, c, instances.ContainerName(1), instances.RouterName)
		removeNetworks(t, c, instances.RouterNetwork, instances.InstanceNetwork(1))
	})

	dir := t.TempDir()
	images := config.Images{Java8: testImage, Java17: testImage, Java21: testImage, Java25: testImage, Router: testImage}
	m := instances.New(instances.Config{
		Docker: c, InstancesDir: dir, UID: nobody, GID: nobody, Images: images, Clock: clock.Real{},
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Customize: func(name string, cfg *docker.ContainerConfig) {
			cfg.Cmd = testCmd
			if name == instances.RouterName { // keep the host port free on developer machines
				cfg.HostConfig.PortBindings = nil
				cfg.ExposedPorts = nil
			}
		},
	})
	t.Cleanup(m.Close)

	status := func() agentapi.InstanceStatus {
		t.Helper()
		for _, s := range m.Status(ctx).Instances {
			if s.ID == 1 {
				return s
			}
		}
		return agentapi.InstanceStatus{State: agentapi.StateMissing}
	}
	run := func(op func() error) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatal(err)
		}
		m.Wait()
		if s := status(); s.LastError != "" {
			t.Fatalf("last_error = %q", s.LastError)
		}
	}
	req := agentapi.EnsureRequest{
		ID: 1, Name: "it", FQDN: "it.mc.localhost", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 1024},
	}
	run(func() error { return m.Ensure(ctx, req) })
	if s := status(); s.State != agentapi.StateCreated || s.Revision != 1 {
		t.Fatalf("after ensure: %+v", s)
	}
	fi, err := os.Stat(instances.DataDir(dir, 1))
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != nobody || st.Gid != nobody || fi.Mode().Perm() != 0o750 {
		t.Fatalf("data dir owner %d:%d mode %v; want %d:%d 0750", st.Uid, st.Gid, fi.Mode().Perm(), nobody, nobody)
	}
	routes, err := os.ReadFile(filepath.Join(dir, "router", "routes.json"))
	if err != nil || !strings.Contains(string(routes), `"it.mc.localhost": "mcpanel-mc-1:25565"`) {
		t.Fatalf("routes = %s, %v", routes, err)
	}
	// The router must survive the background loop: Docker's own spelling of the image
	// reference or the template label must never cause a replacement loop.
	router, err := c.Inspect(ctx, instances.RouterName)
	if err != nil || !router.State.Running {
		t.Fatalf("router: %+v, %v", router.State, err)
	}
	for range 3 {
		m.Tick(ctx)
	}
	if again, err := c.Inspect(ctx, instances.RouterName); err != nil || again.ID != router.ID || !again.State.Running {
		t.Fatalf("router replaced or stopped by Tick: %s -> %+v, %v", router.ID, again, err)
	}

	run(func() error { return m.Power(ctx, 1, agentapi.PowerStart) })
	if s := status(); s.State != agentapi.StateRunning || s.StartedAt == 0 {
		t.Fatalf("after start: %+v", s)
	}
	m.SampleStats(ctx)
	m.SampleStats(ctx)
	if s := status(); s.MemoryBytes <= 0 || s.MemoryLimitBytes != 1280<<20 {
		t.Fatalf("stats: %+v (limit must be 1024 MiB × 1.25)", s)
	}
	run(func() error { return m.Power(ctx, 1, agentapi.PowerRestart) })
	before, err := c.Inspect(ctx, instances.ContainerName(1))
	if err != nil {
		t.Fatal(err)
	}

	req.Revision, req.Recreate = 2, true
	run(func() error { return m.Ensure(ctx, req) })
	after, err := c.Inspect(ctx, instances.ContainerName(1))
	if err != nil || after.ID == before.ID || !after.State.Running || after.Config.Labels[instances.LabelRevision] != "2" {
		t.Fatalf("recreate: %+v, %v", after, err)
	}
	if r, err := c.Inspect(ctx, instances.RouterName); err != nil || !r.State.Running {
		t.Fatalf("router: %+v, %v", r.State, err)
	}

	run(func() error { return m.Power(ctx, 1, agentapi.PowerStop) })
	if s := status(); s.State != agentapi.StateExited {
		t.Fatalf("after stop: %+v", s)
	}
	run(func() error { return m.Remove(ctx, 1) })
	if _, err := c.Inspect(ctx, instances.ContainerName(1)); !docker.IsNotFound(err) {
		t.Fatalf("container after remove: %v", err)
	}
	entries, err := os.ReadDir(instances.DeletedDir(dir))
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "it-") {
		t.Fatalf("deleted/ = %v, %v", entries, err)
	}
	routes, _ = os.ReadFile(filepath.Join(dir, "router", "routes.json"))
	if strings.Contains(string(routes), "it.mc.localhost") {
		t.Fatalf("route not removed: %s", routes)
	}
	if r, err := c.Inspect(ctx, instances.RouterName); err != nil || !r.State.Running {
		t.Fatalf("router must survive the SIGHUP: %+v, %v", r.State, err)
	}
	if slices.ContainsFunc(m.Status(ctx).Instances, func(s agentapi.InstanceStatus) bool { return s.ID == 1 }) {
		t.Fatal("removed instance still listed")
	}
}

// consoleCmd prints to stdout and stderr like a server, then ticks once a second and
// exits on SIGTERM. The pause after "hello" keeps the log file in timestamp order around it:
// Docker stamps stdout and stderr in separate goroutines, so two lines written in the same
// instant can land in the file in the other order, and its since filter stops at the first
// entry at or after since in file order (everything after it comes unfiltered; the panel
// drops the older lines itself).
var consoleCmd = []string{"sh", "-c", "echo hello; sleep 1; echo oops >&2; trap 'exit 0' TERM; i=0; while true; do i=$((i+1)); echo tick $i; sleep 1; done"}

func TestIntegrationConsole(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	requireCleanHost(t, c)
	t.Cleanup(func() {
		forceRemove(t, c, instances.ContainerName(1), instances.RouterName)
		removeNetworks(t, c, instances.RouterNetwork, instances.InstanceNetwork(1))
	})

	dir := t.TempDir()
	images := config.Images{Java8: testImage, Java17: testImage, Java21: testImage, Java25: testImage, Router: testImage}
	newManager := func(rcon string) *instances.Manager {
		m := instances.New(instances.Config{
			Docker: c, InstancesDir: dir, UID: nobody, GID: nobody, Images: images, Clock: clock.Real{},
			Log:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
			RconCommand: rcon,
			Customize: func(name string, cfg *docker.ContainerConfig) {
				if name == instances.RouterName {
					cfg.Cmd = testCmd
					cfg.HostConfig.PortBindings = nil // keep the host port free on developer machines
					cfg.ExposedPorts = nil
					return
				}
				cfg.Cmd = consoleCmd
			},
		})
		t.Cleanup(m.Close)
		return m
	}
	m := newManager("echo")
	run := func(op func() error) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatal(err)
		}
		m.Wait()
	}
	req := agentapi.EnsureRequest{
		ID: 1, Name: "it", FQDN: "it.mc.localhost", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 1024},
	}
	run(func() error { return m.Ensure(ctx, req) })
	run(func() error { return m.Power(ctx, 1, agentapi.PowerStart) })

	// Live log: history plus new lines, stdout and stderr apart.
	lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	lines, done := make(chan agentapi.LogLine, 256), make(chan error, 1)
	go func() {
		done <- m.Logs(lctx, agentapi.LogsRequest{ID: 1, Tail: 100}, func(running bool) {
			if !running {
				t.Error("start(false) for the running container")
			}
		}, func(l agentapi.LogLine) error {
			select {
			case lines <- l:
			default: // never block the stream
			}
			return nil
		})
	}()
	seen := map[string]agentapi.LogLine{}
	timeout := time.After(30 * time.Second)
	for seen["hello"].TS == 0 || seen["oops"].TS == 0 || seen["tick 3"].TS == 0 {
		select {
		case l := <-lines:
			seen[l.Line] = l
		case <-timeout:
			t.Fatalf("log lines missing after 30 s: %v", seen)
		}
	}
	if seen["hello"].Stream != agentapi.StreamOut || seen["oops"].Stream != agentapi.StreamErr {
		t.Fatalf("streams: hello=%+v oops=%+v", seen["hello"], seen["oops"])
	}

	// Console command: exec as the container user in /data, output back.
	// RconCommand is echo here: it prints the "--" that ends rcon-cli's options as well.
	if out, err := m.Rcon(ctx, 1, "say hallo"); err != nil || out != "-- say hallo" {
		t.Fatalf("Rcon = %q, %v", out, err)
	}
	// A failing rcon-cli (exit code != 0) is rcon_unavailable.
	if _, err := newManager("false").Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) {
		t.Fatalf("failing rcon command = %v, want ErrRconUnavailable", err)
	}

	// Player lists from the real data directory.
	if err := os.WriteFile(filepath.Join(instances.DataDir(dir, 1), "ops.json"), []byte(`[{"uuid":"u1","name":"Notch","level":4}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	players, err := m.Players(ctx, 1)
	if err != nil || len(players.Ops) != 1 || players.Ops[0].Name != "Notch" || len(players.Errors) != 0 {
		t.Fatalf("Players = %+v, %v", players, err)
	}

	// Stopping the container ends the stream; commands then report not running.
	run(func() error { return m.Power(ctx, 1, agentapi.PowerStop) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Logs after stop = %v, want nil", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("log stream did not end after the container stopped")
	}
	if _, err := m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrNotRunning) {
		t.Fatalf("Rcon on a stopped container = %v, want ErrNotRunning", err)
	}

	// Resume after "hello": since wins over tail, no line at or before it, and the stream of a
	// stopped container ends after the history.
	var resumed []string
	var resumedRunning bool
	since := seen["hello"].TS + 1
	err = m.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 1, Since: since}, func(running bool) { resumedRunning = running }, func(l agentapi.LogLine) error {
		if l.TS < since {
			t.Errorf("resumed line %q at %d is older than since %d", l.Line, l.TS, since)
		}
		resumed = append(resumed, l.Line)
		return nil
	})
	if err != nil || resumedRunning || slices.Contains(resumed, "hello") || !slices.Contains(resumed, "oops") || !slices.Contains(resumed, "tick 1") {
		t.Fatalf("resumed lines = %q (running %v), %v", resumed, resumedRunning, err)
	}
}
