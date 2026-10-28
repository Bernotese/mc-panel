package instances_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

const otherServerError = "container mcpanel-mc-5 belongs to another server (old); remove it first"

// lifecycleCalls returns the Docker calls since mark that act on the container
// (everything except inspect).
func (e *env) lifecycleCalls(mark int, name string) []string {
	var out []string
	for _, call := range e.callsAfter(mark) {
		if strings.HasSuffix(call, " "+name) && !strings.HasPrefix(call, "inspect ") {
			out = append(out, call)
		}
	}
	return out
}

// TestEnsureRefusesContainerOfAnotherServer: server IDs can be reused after a
// delete. A leftover mcpanel-mc-<id> whose mcpanel.name differs from the request
// belongs to another (deleted) server: ensure must neither adopt it (also not as a
// "never downgrade" no-op) nor recreate or start it, and must say so in last_error.
func TestEnsureRefusesContainerOfAnotherServer(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(5, "old", 7))
	e.power(t, 5, agentapi.PowerStart)
	before := e.container(t, "mcpanel-mc-5")
	routes := e.routes(t)

	for _, tc := range []struct {
		revision int64
		recreate bool
	}{{1, false}, {1, true}, {7, false}, {7, true}, {8, false}, {8, true}} {
		mark := len(e.d.Calls())
		req := ensureReq(5, "new", tc.revision)
		req.Recreate = tc.recreate
		e.ensure(t, req)
		if s := e.status(t, 5); s.LastError != otherServerError {
			t.Errorf("revision %d recreate %v: last_error = %q, want %q", tc.revision, tc.recreate, s.LastError, otherServerError)
		}
		if calls := e.lifecycleCalls(mark, "mcpanel-mc-5"); len(calls) != 0 {
			t.Errorf("revision %d recreate %v: container of another server touched: %v", tc.revision, tc.recreate, calls)
		}
	}
	after := e.container(t, "mcpanel-mc-5")
	if after.ID != before.ID || after.Status != "running" || len(after.StopTimes) != 0 ||
		after.Config.Labels["mcpanel.revision"] != "7" || after.Config.Labels["mcpanel.name"] != "old" {
		t.Fatalf("container of another server changed: %+v", after)
	}
	if got := e.routes(t); got != routes || strings.Contains(got, "new.mc.example.com") {
		t.Fatalf("routes changed: %s", got)
	}
}

// TestPowerRefusesContainerOfAnotherServer: once an ensure has named the server
// that owns the id, power never acts on a leftover container of another server.
func TestPowerRefusesContainerOfAnotherServer(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(5, "old", 7))
	e.power(t, 5, agentapi.PowerStart)
	e.ensure(t, ensureReq(5, "new", 1)) // refused, remembers the name "new"

	for _, a := range []agentapi.PowerAction{agentapi.PowerStop, agentapi.PowerRestart, agentapi.PowerStart} {
		mark := len(e.d.Calls())
		e.power(t, 5, a)
		if s := e.status(t, 5); s.LastError != otherServerError {
			t.Errorf("%s: last_error = %q, want %q", a, s.LastError, otherServerError)
		}
		if calls := e.lifecycleCalls(mark, "mcpanel-mc-5"); len(calls) != 0 {
			t.Errorf("%s: container of another server touched: %v", a, calls)
		}
	}
	if c := e.container(t, "mcpanel-mc-5"); c.Status != "running" || len(c.StopTimes) != 0 {
		t.Fatalf("container of another server changed: %+v", c)
	}
}

// TestRemoveRemovesContainerOfAnotherServer: remove still removes a leftover
// container (it carries mc-panel's label for this id), and archives the data under
// the name on its label, i.e. of the server that wrote it.
func TestRemoveRemovesContainerOfAnotherServer(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(5, "old", 7))
	if err := os.WriteFile(filepath.Join(instances.DataDir(e.dir, 5), "level.dat"), []byte("old world"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := ensureReq(5, "new", 8)
	req.Recreate = true
	e.ensure(t, req) // refused
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 5) })

	if _, ok := e.d.Container("mcpanel-mc-5"); ok {
		t.Fatal("container still exists after remove")
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "deleted", "old-20261010T120000Z", "level.dat"))
	if err != nil || string(got) != "old world" {
		t.Fatalf("archived data = %q, %v; want it under the name of the container's server", got, err)
	}
	if st := e.m.Status(context.Background()); len(st.Instances) != 0 {
		t.Fatalf("status after remove = %+v", st.Instances)
	}
}

// TestEnsureAcceptsChangedBaseDomain: a new base domain changes the FQDN of every
// server but not its name (names are immutable). That is a normal configuration
// change (new revision + recreate), not a container of another server.
func TestEnsureAcceptsChangedBaseDomain(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	req := ensureReq(1, "survival", 2)
	req.FQDN = "survival.play.example.org"
	req.Recreate = true
	e.ensure(t, req)
	c := e.container(t, "mcpanel-mc-1")
	if c.Config.Labels["mcpanel.fqdn"] != "survival.play.example.org" || c.Config.Labels["mcpanel.revision"] != "2" || c.Status != "running" {
		t.Fatalf("container = %+v", c)
	}
	if s := e.status(t, 1); s.LastError != "" {
		t.Fatalf("last_error = %q", s.LastError)
	}
	if r := e.routes(t); !strings.Contains(r, `"survival.play.example.org": "mcpanel-mc-1:25565"`) || strings.Contains(r, "mc.example.com") {
		t.Fatalf("routes = %s", r)
	}
}

// TestDefaultBoundsStayBelowPanelTimeout: the panel's agent client gives up after
// 3 s (agent.DefaultTimeout). Every synchronous answer that waits for Docker must
// come earlier, so the panel sees "unavailable" / 503 docker_unavailable instead
// of a timeout.
func TestDefaultBoundsStayBelowPanelTimeout(t *testing.T) {
	const bound = 2500 * time.Millisecond
	e := newEnv(t)
	ctx := context.Background()

	_, releaseList := e.d.Hold(dockertest.OpList)
	defer releaseList()
	start := time.Now()
	st := e.m.Status(ctx)
	if d := time.Since(start); d > bound {
		t.Errorf("Status took %v with a hanging Docker, want ≤ %v", d, bound)
	}
	if st.Docker != agentapi.DockerUnavailable {
		t.Errorf("Status docker = %q, want unavailable", st.Docker)
	}

	_, releasePing := e.d.Hold(dockertest.OpPing)
	defer releasePing()
	start = time.Now()
	if e.m.CheckDocker(ctx) {
		t.Error("CheckDocker = true with a hanging ping")
	}
	if d := time.Since(start); d > bound {
		t.Errorf("CheckDocker took %v, want ≤ %v", d, bound)
	}
	start = time.Now()
	if err := e.m.Ensure(ctx, ensureReq(1, "survival", 1)); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Errorf("Ensure = %v, want ErrDockerUnavailable", err)
	}
	if d := time.Since(start); d > bound {
		t.Errorf("Ensure took %v with a hanging ping, want ≤ %v", d, bound)
	}
}

// gateWriter blocks the first log line containing match written after arm, until open.
type gateWriter struct {
	match   []byte
	armed   atomic.Bool
	arrived chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGateWriter(match string) *gateWriter {
	return &gateWriter{match: []byte(match), arrived: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, g.match) && g.armed.CompareAndSwap(true, false) {
		close(g.arrived)
		<-g.release
	}
	return len(p), nil
}

func (g *gateWriter) open() { g.once.Do(func() { close(g.release) }) }

// TestStatusReportsQueuedOperation: between two queued operations (one finished,
// the next not yet picked up) the instance has work but no running job. Status
// must report the next operation, not "", or the panel's reconciler would act on
// an instance the agent is still working on.
func TestStatusReportsQueuedOperation(t *testing.T) {
	gate := newGateWriter("instance operation done")
	e := newEnv(t, func(c *instances.Config) { c.Log = slog.New(slog.NewTextHandler(gate, nil)) })
	t.Cleanup(gate.open) // before e.m.Close (cleanups run last-in first-out)
	e.ensure(t, ensureReq(1, "survival", 1))
	ctx := context.Background()

	arrived, release := e.d.Hold(dockertest.OpStart)
	defer release()
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatal(err)
	}
	<-arrived
	if err := e.m.Power(ctx, 1, agentapi.PowerStop); err != nil {
		t.Fatal(err)
	}
	gate.armed.Store(true)
	release() // start finishes; the worker stops at its "done" log line, stop still queued
	select {
	case <-gate.arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish the start operation")
	}
	if s := e.status(t, 1); s.Operation != agentapi.OpStopping || len(s.Queued) != 0 {
		// The next job is reported as the operation, so it must not be in queued as well.
		t.Errorf("operation with a queued stop = %q, queued = %v; want %q, []", s.Operation, s.Queued, agentapi.OpStopping)
	}
	gate.open()
	e.m.Wait()
	if s := e.status(t, 1); s.Operation != agentapi.OpNone || s.State != agentapi.StateExited {
		t.Fatalf("status after the queue drained = %+v", s)
	}
}
