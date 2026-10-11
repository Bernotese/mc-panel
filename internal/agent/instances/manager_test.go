package instances_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

const (
	java21Pinned = "itzg/minecraft-server@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa"
	routerPinned = "itzg/mc-router@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e"
)

// syncBuffer is a bytes.Buffer safe for concurrent writers and readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type env struct {
	m    *instances.Manager
	d    *dockertest.Daemon
	dir  string
	clk  *clock.Fake
	logs *syncBuffer
}

func newEnv(t *testing.T, opts ...func(*instances.Config)) *env {
	t.Helper()
	d := dockertest.New(t)
	e := &env{d: d, dir: t.TempDir(), clk: clock.NewFake(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)), logs: &syncBuffer{}}
	e.m = instances.New(e.config(opts...))
	t.Cleanup(e.m.Close)
	return e
}

// config is the manager configuration of this env (for a second manager on the same daemon).
func (e *env) config(opts ...func(*instances.Config)) instances.Config {
	cfg := instances.Config{
		Docker: docker.New(e.d.Socket), InstancesDir: e.dir, UID: os.Getuid(), GID: os.Getgid(),
		Images: config.DefaultImages(), Clock: e.clk,
		Log: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

func ensureReq(id int64, name string, revision int64) agentapi.EnsureRequest {
	return agentapi.EnsureRequest{
		ID: id, Name: name, FQDN: name + ".mc.example.com", Revision: revision,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	}
}

// do runs one operation to completion.
func (e *env) do(t *testing.T, op func(context.Context) error) {
	t.Helper()
	if err := op(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.m.Wait()
}

func (e *env) ensure(t *testing.T, req agentapi.EnsureRequest) {
	t.Helper()
	e.do(t, func(ctx context.Context) error { return e.m.Ensure(ctx, req) })
}

func (e *env) power(t *testing.T, id int64, a agentapi.PowerAction) {
	t.Helper()
	e.do(t, func(ctx context.Context) error { return e.m.Power(ctx, id, a) })
}

func (e *env) status(t *testing.T, id int64) agentapi.InstanceStatus {
	t.Helper()
	for _, s := range e.m.Status(context.Background()).Instances {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("instance %d not in status", id)
	return agentapi.InstanceStatus{}
}

func (e *env) container(t *testing.T, name string) dockertest.Container {
	t.Helper()
	c, ok := e.d.Container(name)
	if !ok {
		t.Fatalf("container %s missing", name)
	}
	return c
}

func (e *env) routes(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, "router", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// callsAfter returns the Docker calls since mark (len of Calls() earlier).
func (e *env) callsAfter(mark int) []string { return e.d.Calls()[mark:] }

func TestEnsureCreatesInstance(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))

	c := e.container(t, "mcpanel-mc-1")
	if c.Status != "created" || c.Config.Labels["mcpanel.revision"] != "1" || c.Config.Image != java21Pinned {
		t.Fatalf("container = %+v", c)
	}
	if !e.d.HasNetwork("mcpanel-mc") {
		t.Fatal("network mcpanel-mc not created")
	}
	if got := e.d.Pulls(); !slices.Equal(got, []string{java21Pinned, routerPinned}) {
		t.Fatalf("pulls = %v", got)
	}
	data := filepath.Join(e.dir, "instances", "1")
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o750 {
		t.Fatalf("data dir = %v, %v; want 0750 directory", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(e.dir, "instances")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("instances dir = %v, %v; want 0700", fi, err)
	}
	if !strings.Contains(e.routes(t), `"survival.mc.example.com": "mcpanel-mc-1:25565"`) {
		t.Fatalf("routes = %s", e.routes(t))
	}
	r := e.container(t, "mcpanel-router")
	if r.Status != "running" || r.Config.Labels["mcpanel.router"] != "1" {
		t.Fatalf("router = %+v", r)
	}
	want := agentapi.InstanceStatus{ID: 1, Name: "survival", Revision: 1, State: agentapi.StateCreated}
	if got := e.status(t, 1); got != want {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	resp := e.m.Status(context.Background())
	if resp.Docker != agentapi.DockerOK || resp.Router != (agentapi.RouterStatus{State: agentapi.StateRunning}) {
		t.Fatalf("status = %+v", resp)
	}
}

func TestEnsureWithoutRecreateKeepsOldRevision(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	before := e.container(t, "mcpanel-mc-1")

	e.ensure(t, ensureReq(1, "survival", 2)) // recreate false
	after := e.container(t, "mcpanel-mc-1")
	if after.ID != before.ID || after.Status != "running" || after.Config.Labels["mcpanel.revision"] != "1" {
		t.Fatalf("container changed without recreate: %+v", after)
	}
	if s := e.status(t, 1); s.Revision != 1 || s.State != agentapi.StateRunning || s.StartedAt == 0 {
		t.Fatalf("status = %+v", s)
	}
}

func TestEnsureRecreate(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	old := e.container(t, "mcpanel-mc-1")

	mark := len(e.d.Calls())
	req := ensureReq(1, "survival", 2)
	req.Recreate = true
	req.Spec.MemoryMiB = 6144
	e.ensure(t, req)
	c := e.container(t, "mcpanel-mc-1")
	if c.ID == old.ID || c.Status != "running" || c.Config.Labels["mcpanel.revision"] != "2" || c.Config.HostConfig.Memory != 7680<<20 {
		t.Fatalf("recreated running container = %+v", c)
	}
	var lifecycle []string
	for _, call := range e.callsAfter(mark) {
		if strings.HasSuffix(call, " mcpanel-mc-1") && !strings.HasPrefix(call, "inspect") {
			lifecycle = append(lifecycle, call)
		}
	}
	if want := []string{"stop mcpanel-mc-1", "remove mcpanel-mc-1", "create mcpanel-mc-1", "start mcpanel-mc-1"}; !slices.Equal(lifecycle, want) {
		t.Fatalf("lifecycle = %v, want %v", lifecycle, want)
	}

	e.power(t, 1, agentapi.PowerStop)
	req.Revision = 3
	e.ensure(t, req)
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "created" || c.Config.Labels["mcpanel.revision"] != "3" {
		t.Fatalf("a stopped container must stay stopped after recreate: %+v", c)
	}
	same := e.container(t, "mcpanel-mc-1")
	e.ensure(t, req) // same revision + recreate: nothing to do
	if e.container(t, "mcpanel-mc-1").ID != same.ID {
		t.Fatal("ensure with the current revision must not recreate")
	}
}

func TestPowerActions(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "running" {
		t.Fatalf("after start: %+v", c)
	}
	e.power(t, 1, agentapi.PowerStop)
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "exited" || !slices.Equal(c.StopTimes, []int{60}) {
		t.Fatalf("after stop: %+v", c)
	}
	e.power(t, 1, agentapi.PowerStart)
	mark := len(e.d.Calls())
	e.power(t, 1, agentapi.PowerRestart)
	if got := e.callsAfter(mark); !slices.Equal(got, []string{"inspect mcpanel-mc-1", "stop mcpanel-mc-1", "start mcpanel-mc-1"}) {
		t.Fatalf("restart calls = %v", got)
	}
	if s := e.status(t, 1); s.State != agentapi.StateRunning || s.Operation != agentapi.OpNone || s.LastError != "" {
		t.Fatalf("status = %+v", s)
	}
}

func TestPowerOnMissingContainerSetsLastError(t *testing.T) {
	e := newEnv(t)
	e.power(t, 9, agentapi.PowerStart) // accepted (202), fails asynchronously
	s := e.status(t, 9)
	if s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "mcpanel-mc-9 does not exist") {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := e.d.Container("mcpanel-mc-9"); ok {
		t.Fatal("power must never create a container")
	}
}

// TestForeignContainersAreNeverTouched is Review Focus #2: a container that only
// shares the name (no mcpanel.instance label, or a label for another id) is
// neither stopped nor removed nor replaced, and its data stays in place.
func TestForeignContainersAreNeverTouched(t *testing.T) {
	e := newEnv(t)
	foreign := map[int64]docker.ContainerConfig{
		7: {Image: "nginx@sha256:" + strings.Repeat("ab", 32), Labels: map[string]string{"com.example.app": "blog"}},
		8: {Image: "nginx@sha256:" + strings.Repeat("ab", 32), Labels: map[string]string{
			"mcpanel.instance": "9", "mcpanel.name": "evil", "mcpanel.fqdn": "evil.mc.example.com",
		}},
	}
	before := map[int64]dockertest.Container{}
	for id, cfg := range foreign {
		before[id] = e.d.AddContainer(instances.ContainerName(id), cfg, true)
		dataDir := instances.DataDir(e.dir, id)
		if err := os.MkdirAll(filepath.Join(dataDir, "world"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "world", "level.dat"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for id := range foreign {
		req := ensureReq(id, "srv"+string(rune('0'+id)), 1)
		req.Recreate = true
		e.ensure(t, req)
		e.power(t, id, agentapi.PowerStop)
		e.power(t, id, agentapi.PowerRestart)
		e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, id) })
	}
	for id := range foreign {
		name := instances.ContainerName(id)
		c := e.container(t, name)
		if c.ID != before[id].ID || c.Status != "running" || len(c.Kills) != 0 || len(c.StopTimes) != 0 || c.Config.Image != before[id].Config.Image {
			t.Errorf("%s was touched: %+v", name, c)
		}
		for _, call := range e.d.Calls() {
			if strings.HasSuffix(call, " "+name) && !strings.HasPrefix(call, "inspect ") {
				t.Errorf("forbidden call on foreign container: %q", call)
			}
		}
		if _, err := os.Stat(filepath.Join(instances.DataDir(e.dir, id), "world", "level.dat")); err != nil {
			t.Errorf("data of %s moved or deleted: %v", name, err)
		}
		if s := e.status(t, id); !strings.Contains(s.LastError, "not managed by mc-panel") {
			t.Errorf("%s: last_error = %q", name, s.LastError)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(e.dir, "deleted")); len(entries) != 0 {
		t.Fatalf("deleted/ = %v", entries)
	}
	e.m.Tick(context.Background())
	if strings.Contains(e.routes(t), "evil") {
		t.Fatalf("foreign container got a route: %s", e.routes(t))
	}
}

func TestRemoveArchivesDataAndRoute(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	if err := os.WriteFile(filepath.Join(e.dir, "instances", "1", "level.dat"), []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })

	if _, ok := e.d.Container("mcpanel-mc-1"); ok {
		t.Fatal("container still exists")
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "deleted", "survival-20261010T120000Z", "level.dat"))
	if err != nil || string(got) != "world" {
		t.Fatalf("archived data = %q, %v", got, err)
	}
	if strings.Contains(e.routes(t), "survival") {
		t.Fatalf("route not removed: %s", e.routes(t))
	}
	// The router was created after the first routes file was written, so only the
	// removal needs a reload.
	if r := e.container(t, "mcpanel-router"); !slices.Equal(r.Kills, []string{"SIGHUP"}) {
		t.Fatalf("router kills = %v; want exactly one SIGHUP", r.Kills)
	}
	if st := e.m.Status(context.Background()); len(st.Instances) != 0 {
		t.Fatalf("removed instance still listed: %+v", st.Instances)
	}

	// Same name removed again within the same second: numeric suffix, nothing overwritten.
	e.ensure(t, ensureReq(1, "survival", 1))
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if _, err := os.Stat(filepath.Join(e.dir, "deleted", "survival-20261010T120000Z-2")); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	// Removing an unknown instance is idempotent.
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if st := e.m.Status(context.Background()); len(st.Instances) != 0 {
		t.Fatalf("status = %+v", st.Instances)
	}
}

func TestQueueIsSerialAndBounded(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	ctx := context.Background()

	arrived, release := e.d.Hold(dockertest.OpStart)
	defer release()
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatal(err)
	}
	<-arrived // the first operation now blocks inside Docker
	if s := e.status(t, 1); s.Operation != agentapi.OpStarting {
		t.Fatalf("operation while starting = %q", s.Operation)
	}
	for _, a := range []agentapi.PowerAction{agentapi.PowerStop, agentapi.PowerStart, agentapi.PowerStop} {
		if err := e.m.Power(ctx, 1, a); err != nil {
			t.Fatalf("queue %s: %v", a, err)
		}
	}
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("fifth open operation = %v, want ErrBusy", err)
	}
	if err := e.m.Power(ctx, 2, agentapi.PowerStop); err != nil {
		t.Fatalf("other instances have their own queue: %v", err)
	}
	release()
	e.m.Wait()

	var order []string
	for _, call := range e.d.Calls() {
		if call == "start mcpanel-mc-1" || call == "stop mcpanel-mc-1" {
			order = append(order, call)
		}
	}
	want := []string{"start mcpanel-mc-1", "stop mcpanel-mc-1", "start mcpanel-mc-1", "stop mcpanel-mc-1"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatalf("queue must accept work again: %v", err)
	}
	e.m.Wait()
}

func TestLastErrorIsRedactedAndBounded(t *testing.T) {
	e := newEnv(t)
	key := "$2a$10$verysecretcurseforgekey"
	e.d.Fail(dockertest.OpCreate, http.StatusInternalServerError,
		"create failed: CF_API_KEY="+key+"\ninjected line "+strings.Repeat("x", 2000))
	req := ensureReq(3, "packs", 1)
	req.Spec = instancespec.Spec{
		Type: instancespec.TypeCurseForge, Modpack: ptr("atm10"), CurseForgeAPIKey: &key,
		Java: instancespec.Java21, MemoryMiB: 8192,
	}
	e.ensure(t, req)
	s := e.status(t, 3)
	if s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "create failed") ||
		!strings.Contains(s.LastError, "[redacted]") || strings.Contains(s.LastError, key) ||
		strings.ContainsAny(s.LastError, "\n\r") || utf8.RuneCountInString(s.LastError) > 500 {
		t.Fatalf("last_error = %q", s.LastError)
	}
	if strings.Contains(e.logs.String(), key) {
		t.Fatal("the CurseForge key reached the log")
	}

	e.d.Clear()
	e.ensure(t, req)
	if s := e.status(t, 3); s.LastError != "" || s.State != agentapi.StateCreated {
		t.Fatalf("a successful retry must clear last_error: %+v", s)
	}
}

func TestPullFailureIsReported(t *testing.T) {
	e := newEnv(t)
	e.d.FailPull("manifest unknown")
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "manifest unknown") {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := e.d.Container("mcpanel-mc-1"); ok {
		t.Fatal("no container may be created without its image")
	}
}

func TestDockerUnavailable(t *testing.T) {
	logs := &syncBuffer{}
	m := instances.New(instances.Config{
		Docker: docker.New(filepath.Join(t.TempDir(), "missing.sock")), InstancesDir: t.TempDir(),
		UID: os.Getuid(), GID: os.Getgid(), Images: config.DefaultImages(),
		Clock: clock.NewFake(time.Unix(0, 0)), Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	defer m.Close()
	ctx := context.Background()
	if m.CheckDocker(ctx) || m.DockerStatus() != agentapi.DockerUnavailable {
		t.Fatal("docker must be unavailable")
	}
	for name, err := range map[string]error{
		"ensure": m.Ensure(ctx, ensureReq(1, "survival", 1)),
		"power":  m.Power(ctx, 1, agentapi.PowerStart),
		"remove": m.Remove(ctx, 1),
	} {
		if !errors.Is(err, agentapi.ErrDockerUnavailable) {
			t.Errorf("%s = %v, want ErrDockerUnavailable", name, err)
		}
	}
	st := m.Status(ctx)
	if st.Docker != agentapi.DockerUnavailable || st.Instances == nil || len(st.Instances) != 0 || st.Router.State != agentapi.StateMissing {
		t.Fatalf("status = %+v", st)
	}
	m.Tick(ctx) // must not panic or block without Docker
}

func TestDockerRecoversImmediately(t *testing.T) {
	e := newEnv(t)
	e.d.Fail(dockertest.OpPing, http.StatusInternalServerError, "daemon restarting")
	if e.m.CheckDocker(context.Background()) {
		t.Fatal("ping failed, docker must be unavailable")
	}
	if err := e.m.Ensure(context.Background(), ensureReq(1, "survival", 1)); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Ensure = %v", err)
	}
	e.d.Clear()
	e.ensure(t, ensureReq(1, "survival", 1)) // re-checks right away, no need to wait for Tick
	if e.m.DockerStatus() != agentapi.DockerOK {
		t.Fatal("docker must be ok again")
	}
}

func TestInvalidRequestsAreRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	bad := ensureReq(1, "survival", 1)
	bad.Spec.Properties.MOTD = ptr("a\nJVM_OPTS=-Xmx1g")
	for name, err := range map[string]error{
		"ensure": e.m.Ensure(ctx, bad),
		"power":  e.m.Power(ctx, 1, "kill"),
		"id":     e.m.Power(ctx, 0, agentapi.PowerStart),
		"remove": e.m.Remove(ctx, -1),
	} {
		if !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
	if calls := e.d.Calls(); len(calls) != 0 {
		t.Fatalf("invalid requests reached Docker: %v", calls)
	}
}

func TestStatsSampling(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	stats := func(total, system uint64, mem docker.MemoryStats) docker.Stats {
		return docker.Stats{
			CPUStats:    docker.CPUStats{CPUUsage: docker.CPUUsage{TotalUsage: total}, SystemCPUUsage: system, OnlineCPUs: 4},
			MemoryStats: mem,
		}
	}
	v2 := docker.MemoryStats{Usage: 3 << 30, Limit: 5 << 30, Stats: map[string]uint64{"inactive_file": 1 << 30}}
	e.d.SetStats("mcpanel-mc-1", stats(1000, 100000, v2))
	e.m.SampleStats(ctx)
	if s := e.status(t, 1); s.CPUPercent != 0 || s.MemoryBytes != 2<<30 || s.MemoryLimitBytes != 5<<30 {
		t.Fatalf("first sample = %+v (no previous sample: cpu 0)", s)
	}
	v1 := docker.MemoryStats{Usage: 3 << 30, Limit: 5 << 30, Stats: map[string]uint64{"cache": 512 << 20}}
	e.d.SetStats("mcpanel-mc-1", stats(1500, 110000, v1))
	e.m.SampleStats(ctx)
	if s := e.status(t, 1); s.CPUPercent != 20 || s.MemoryBytes != (3<<30)-(512<<20) {
		t.Fatalf("second sample = %+v; want cpu 500/10000*4*100 = 20", s)
	}
	e.power(t, 1, agentapi.PowerStop)
	e.m.SampleStats(ctx)
	if s := e.status(t, 1); s.CPUPercent != 0 || s.MemoryBytes != 0 || s.MemoryLimitBytes != 0 {
		t.Fatalf("stopped container keeps stats: %+v", s)
	}
}

func TestTickManagesRouter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.m.Tick(ctx)
	if e.routes(t) != "{\n  \"mappings\": {}\n}\n" {
		t.Fatalf("routes = %q", e.routes(t))
	}
	r := e.container(t, "mcpanel-router")
	if r.Status != "running" || r.Config.Image != routerPinned {
		t.Fatalf("router = %+v", r)
	}
	mark := len(e.d.Calls())
	e.m.Tick(ctx)
	for _, call := range e.callsAfter(mark) {
		if strings.HasPrefix(call, "create") || strings.HasPrefix(call, "kill") || strings.HasPrefix(call, "start") {
			t.Fatalf("unchanged routes must not touch the router: %v", e.callsAfter(mark))
		}
	}
	e.d.SetState("mcpanel-router", "exited", 2)
	e.m.Tick(ctx)
	if again := e.container(t, "mcpanel-router"); again.Status != "running" || again.ID != r.ID {
		t.Fatalf("stopped router must be started again (same container): %+v", again)
	}

	// Agent upgrade with a new pinned router image: the router container is replaced.
	images := config.DefaultImages()
	images.Router = "itzg/mc-router:1.48.0@sha256:" + strings.Repeat("cd", 32)
	m2 := instances.New(instances.Config{
		Docker: docker.New(e.d.Socket), InstancesDir: e.dir, UID: os.Getuid(), GID: os.Getgid(),
		Images: images, Clock: e.clk, Log: slog.New(slog.NewTextHandler(e.logs, nil)),
	})
	defer m2.Close()
	m2.Tick(ctx)
	if r2 := e.container(t, "mcpanel-router"); r2.ID == r.ID || r2.Config.Image != "itzg/mc-router@sha256:"+strings.Repeat("cd", 32) || r2.Status != "running" {
		t.Fatalf("router not replaced: %+v", r2)
	}
}

func TestForeignRouterIsNeverTouched(t *testing.T) {
	e := newEnv(t)
	before := e.d.AddContainer("mcpanel-router", docker.ContainerConfig{Image: "nginx@sha256:" + strings.Repeat("ab", 32)}, true)
	e.m.Tick(context.Background())
	after := e.container(t, "mcpanel-router")
	if after.ID != before.ID || len(after.Kills) != 0 || after.Status != "running" {
		t.Fatalf("foreign router touched: %+v", after)
	}
	st := e.m.Status(context.Background())
	if st.Router.State != agentapi.StateMissing || !strings.Contains(st.Router.LastError, "not managed by mc-panel") {
		t.Fatalf("router status = %+v", st.Router)
	}
}

func TestCloseRejectsNewWork(t *testing.T) {
	e := newEnv(t)
	e.m.Close()
	if err := e.m.Power(context.Background(), 1, agentapi.PowerStart); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Power after Close = %v", err)
	}
}

// TestEnsureLeavesListFiles: since sub-project 3a the server owns ops.json and whitelist.json;
// creating and recreating a container must leave them exactly as they are.
func TestEnsureLeavesListFiles(t *testing.T) {
	e := newEnv(t)
	data := instances.DataDir(e.dir, 1)
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	ops := []byte(`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch","level":4}]` + "\n")
	for _, name := range []string{"ops.json", "whitelist.json"} {
		if err := os.WriteFile(filepath.Join(data, name), ops, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	req := ensureReq(1, "survival", 2)
	req.Recreate = true
	e.ensure(t, req)
	if s := e.status(t, 1); s.LastError != "" || s.Revision != 2 {
		t.Fatalf("status = %+v", s)
	}
	for _, name := range []string{"ops.json", "whitelist.json"} {
		b, err := os.ReadFile(filepath.Join(data, name))
		if err != nil || string(b) != string(ops) {
			t.Fatalf("%s = %q, %v; want it unchanged", name, b, err)
		}
	}
}
