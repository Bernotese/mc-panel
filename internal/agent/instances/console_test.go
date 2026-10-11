package instances_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// startInstance creates and starts mcpanel-mc-<id> the way the panel does.
func (e *env) startInstance(t *testing.T, id int64, name string) {
	t.Helper()
	e.ensure(t, ensureReq(id, name, 1))
	e.power(t, id, agentapi.PowerStart)
}

// collectLogs runs Logs in the background: lines arrive on lines, the result on done,
// started receives the running flag of every start call (the tests check it was exactly one).
func collectLogs(ctx context.Context, m *instances.Manager, req agentapi.LogsRequest) (lines chan agentapi.LogLine, started chan bool, done chan error) {
	lines, started, done = make(chan agentapi.LogLine, 64), make(chan bool, 2), make(chan error, 1)
	go func() {
		done <- m.Logs(ctx, req, func(running bool) { started <- running }, func(l agentapi.LogLine) error {
			lines <- l
			return nil
		})
	}()
	return lines, started, done
}

func nextLine(t *testing.T, lines <-chan agentapi.LogLine) agentapi.LogLine {
	t.Helper()
	select {
	case l := <-lines:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("no log line within 5 s")
		return agentapi.LogLine{}
	}
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Logs did not return within 5 s")
		return nil
	}
}

// drain returns the lines already delivered (call after Logs returned).
func drain(lines chan agentapi.LogLine) []agentapi.LogLine {
	var out []agentapi.LogLine
	for {
		select {
		case l := <-lines:
			out = append(out, l)
		default:
			return out
		}
	}
}

func TestLogsStreamsLines(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	name := instances.ContainerName(1)
	e.d.AppendLog(name, docker.Stdout, "[12:00:00 INFO]: Starting minecraft server")
	t2 := e.d.AppendLog(name, docker.Stderr, "\x1b[33mWARN\x1b[0m slow tick")
	t3 := e.d.AppendLog(name, docker.Stdout, "[12:00:03 INFO]: Done (3.2s)!")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines, started, done := collectLogs(ctx, e.m, agentapi.LogsRequest{ID: 1, Tail: 2})
	for _, want := range []agentapi.LogLine{
		{TS: t2.UnixNano(), Stream: agentapi.StreamErr, Line: "\x1b[33mWARN\x1b[0m slow tick"},
		{TS: t3.UnixNano(), Stream: agentapi.StreamOut, Line: "[12:00:03 INFO]: Done (3.2s)!"},
	} {
		if got := nextLine(t, lines); got != want {
			t.Fatalf("line = %+v, want %+v", got, want)
		}
	}
	select {
	case running := <-started:
		if !running {
			t.Fatal("start(false) for a running container")
		}
	default:
		t.Fatal("start was not called before the first line")
	}
	t4 := e.d.AppendLog(name, docker.Stdout, "live")
	if got := nextLine(t, lines); got != (agentapi.LogLine{TS: t4.UnixNano(), Stream: agentapi.StreamOut, Line: "live"}) {
		t.Fatalf("live line = %+v", got)
	}
	e.d.SetState(name, "exited", 0)
	if err := waitDone(t, done); err != nil {
		t.Fatalf("Logs after the container stopped = %v, want nil", err)
	}
	if len(started) != 0 {
		t.Fatal("start called more than once")
	}
	if q := e.d.LogQueries(); len(q) != 1 || !strings.Contains(q[0], "follow=1") || !strings.Contains(q[0], "tail=2") {
		t.Fatalf("log queries = %q", q)
	}
}

func TestLogsSinceWinsOverTail(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1)) // created, not running: the stream ends after the history
	name := instances.ContainerName(1)
	e.d.AppendLog(name, docker.Stdout, "a")
	t2 := e.d.AppendLog(name, docker.Stdout, "b")
	t3 := e.d.AppendLog(name, docker.Stdout, "c")
	since := t2.UnixNano() + 1
	lines, started, done := collectLogs(context.Background(), e.m, agentapi.LogsRequest{ID: 1, Tail: 1, Since: since})
	if err := waitDone(t, done); err != nil {
		t.Fatal(err) // a stopped container: history, then the stream ends by itself
	}
	if running := <-started; running {
		t.Fatal("start(true) for a container that is not running")
	}
	if got := drain(lines); !reflect.DeepEqual(got, []agentapi.LogLine{{TS: t3.UnixNano(), Stream: agentapi.StreamOut, Line: "c"}}) {
		t.Fatalf("lines after since = %+v", got)
	}
	q := e.d.LogQueries()
	wantSince := fmt.Sprintf("since=%d.%09d", since/int64(time.Second), since%int64(time.Second))
	if len(q) != 1 || !strings.Contains(q[0], "tail=all") || !strings.Contains(q[0], wantSince) {
		t.Fatalf("log query = %q, want tail=all and %s", q, wantSince)
	}
}

func TestLogsReassemblesAndSanitizesLines(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1)) // not running: the stream ends after the history
	name := instances.ContainerName(1)
	const (
		ts1 = "2026-10-10T12:00:01.000000001Z"
		ts2 = "2026-10-10T12:00:02.000000002Z"
		ts3 = "2026-10-10T12:00:03.000000003Z"
		ts4 = "2026-10-10T12:00:04.000000004Z"
		ts5 = "2026-10-10T12:00:05.000000005Z"
		ts6 = "2026-10-10T12:00:06.000000006Z"
	)
	raw := func(st docker.StreamType, s string) { e.d.AppendRawLog(name, st, []byte(s)) }
	raw(docker.Stdout, ts1+" split ")                                          // line continues in a later frame
	raw(docker.Stderr, ts2+" err line\n")                                      // other stream in between
	raw(docker.Stdout, "line\r\n")                                             // end of the split line, CRLF
	raw(docker.Stdout, ts3+" a"+strings.Repeat("ä", 5000)+"\n")                // 10001 bytes: cut at a rune boundary
	raw(docker.Stdout, ts4+" "+strings.Repeat("x", 9000))                      // overlong, no newline yet
	raw(docker.Stdout, strings.Repeat("y", 100)+"\n"+ts5+" bad\xff\xfe end\n") // rest dropped; invalid UTF-8
	raw(docker.Stdout, "no timestamp\n")                                       // inherits ts5
	raw(docker.Stdout, ts6+" last line without newline")                       // flushed at the end

	lines, _, done := collectLogs(context.Background(), e.m, agentapi.LogsRequest{ID: 1, Tail: 1000})
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
	ts := func(s string) int64 {
		tt, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return tt.UnixNano()
	}
	want := []agentapi.LogLine{
		{TS: ts(ts2), Stream: agentapi.StreamErr, Line: "err line"},
		{TS: ts(ts1), Stream: agentapi.StreamOut, Line: "split line"},
		{TS: ts(ts3), Stream: agentapi.StreamOut, Line: "a" + strings.Repeat("ä", 4095)},
		{TS: ts(ts4), Stream: agentapi.StreamOut, Line: strings.Repeat("x", agentapi.MaxLogLineBytes)},
		{TS: ts(ts5), Stream: agentapi.StreamOut, Line: "bad� end"},
		{TS: ts(ts5), Stream: agentapi.StreamOut, Line: "no timestamp"},
		{TS: ts(ts6), Stream: agentapi.StreamOut, Line: "last line without newline"},
	}
	if got := drain(lines); !reflect.DeepEqual(got, want) {
		for i := range max(len(got), len(want)) {
			var g, w agentapi.LogLine
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if g != w {
				t.Errorf("line %d: got %d %q (%d bytes), want %d %q (%d bytes)", i, g.TS, firstN(g.Line, 40), len(g.Line), w.TS, firstN(w.Line, 40), len(w.Line))
			}
		}
		t.FailNow()
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestLogsErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	startCalled := false
	start := func(bool) { startCalled = true }
	noop := func(agentapi.LogLine) error { return nil }

	if err := e.m.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}, start, noop); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("missing container = %v, want ErrNotFound", err)
	}
	e.d.AddContainer(instances.ContainerName(7), docker.ContainerConfig{Image: java21Pinned}, true) // foreign: no labels
	if err := e.m.Logs(ctx, agentapi.LogsRequest{ID: 7, Tail: 10}, start, noop); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("foreign container = %v, want ErrNotFound", err)
	}
	for _, bad := range []agentapi.LogsRequest{{ID: 0}, {ID: 1, Tail: -1}, {ID: 1, Tail: agentapi.MaxLogTail + 1}, {ID: 1, Since: -1}} {
		if err := e.m.Logs(ctx, bad, start, noop); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("Logs(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
	if slices.ContainsFunc(e.d.Calls(), func(c string) bool { return strings.HasPrefix(c, dockertest.OpLogs+" ") }) {
		t.Fatalf("logs requested for a refused call: %v", e.d.Calls())
	}

	e.startInstance(t, 1, "survival")
	e.d.Fail(dockertest.OpLogs, http.StatusInternalServerError, "log driver broken")
	if err := e.m.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}, start, noop); err == nil || errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("failing log driver = %v", err)
	}
	e.d.Clear()

	cfg := e.config()
	cfg.Docker = docker.New(filepath.Join(t.TempDir(), "missing.sock"))
	down := instances.New(cfg)
	defer down.Close()
	if err := down.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}, start, noop); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Docker down = %v, want ErrDockerUnavailable", err)
	}
	if startCalled {
		t.Fatal("start called for a stream that never opened")
	}
}

func TestLogsEndsOnCancelAndEmitError(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	e.d.AppendLog(instances.ContainerName(1), docker.Stdout, "one")
	req := agentapi.LogsRequest{ID: 1, Tail: 10}

	ctx, cancel := context.WithCancel(context.Background())
	lines, _, done := collectLogs(ctx, e.m, req)
	nextLine(t, lines)
	cancel() // the panel went away
	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Logs after cancel = %v, want context.Canceled", err)
	}

	errStop := errors.New("panel gone")
	err := e.m.Logs(context.Background(), req, func(bool) {}, func(agentapi.LogLine) error { return errStop })
	if !errors.Is(err, errStop) {
		t.Fatalf("Logs with failing emit = %v, want %v", err, errStop)
	}
}

func TestRconRunsCommandInContainer(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	out, err := e.m.Rcon(context.Background(), 1, "say Hallo Welt")
	if err != nil || out != "say Hallo Welt" { // the fake echoes the arguments after the program
		t.Fatalf("Rcon = %q, %v", out, err)
	}
	want := dockertest.ExecRecord{Container: instances.ContainerName(1), Config: docker.ExecConfig{
		Cmd: []string{"rcon-cli", "say Hallo Welt"}, User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		WorkingDir: "/data", Env: []string{"HOME=/data"}, AttachStdout: true, AttachStderr: true,
	}}
	if execs := e.d.Execs(); len(execs) != 1 || !reflect.DeepEqual(execs[0], want) {
		t.Fatalf("execs = %+v, want %+v", execs, want)
	}
}

func TestRconCommandIsConfigurable(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.RconCommand = "echo" })
	e.startInstance(t, 1, "survival")
	if _, err := e.m.Rcon(context.Background(), 1, "list"); err != nil {
		t.Fatal(err)
	}
	if execs := e.d.Execs(); len(execs) != 1 || execs[0].Config.Cmd[0] != "echo" {
		t.Fatalf("execs = %+v", execs)
	}
}

func TestRconErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("missing container = %v, want ErrNotFound", err)
	}
	e.ensure(t, ensureReq(1, "survival", 1)) // created, not running
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrNotRunning) {
		t.Fatalf("stopped container = %v, want ErrNotRunning", err)
	}
	e.d.AddContainer(instances.ContainerName(7), docker.ContainerConfig{Image: java21Pinned}, true) // foreign
	if _, err := e.m.Rcon(ctx, 7, "list"); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("foreign container = %v, want ErrNotFound", err)
	}
	for _, bad := range []string{"", "   ", "say a\nop b", "say\ta", "say \x00", "say \x7f", strings.Repeat("a", agentapi.MaxCommandBytes+1)} {
		if _, err := e.m.Rcon(ctx, 1, bad); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("Rcon(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := e.m.Rcon(ctx, 0, "list"); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("id 0 = %v, want ErrInvalid", err)
	}
	if n := len(e.d.Execs()); n != 0 {
		t.Fatalf("%d execs for refused commands; foreign or stopped containers must never get one", n)
	}

	e.power(t, 1, agentapi.PowerStart)
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stderr: "Failed to connect to RCON serverdial tcp [::1]:25575: connect: connection refused\n", ExitCode: 1}
	})
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("rcon-cli exit 1 = %v, want ErrRconUnavailable with its output", err)
	}
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: strings.Repeat("x", 2000), ExitCode: 1}
	})
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) || strings.Count(err.Error(), "x") != 500 {
		t.Fatalf("long failure output must be cut to 500 characters: %d", strings.Count(fmt.Sprint(err), "x"))
	}
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult { return dockertest.ExecResult{ExitCode: 2} })
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) || !strings.Contains(err.Error(), "rcon-cli exited with code 2") {
		t.Fatalf("silent failure = %v", err)
	}
}

func TestRconOutputIsBoundedAndValid(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	ctx := context.Background()
	const mark = "\n[… gekürzt]"
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: strings.Repeat("a", agentapi.MaxRconOutputBytes) + "OVERFLOW"}
	})
	out, err := e.m.Rcon(ctx, 1, "list")
	if err != nil || !strings.HasSuffix(out, mark) || strings.Contains(out, "OVERFLOW") || len(out) > agentapi.MaxRconOutputBytes+len(mark) {
		t.Fatalf("overlong output: %d bytes, suffix %q, %v", len(out), out[max(len(out)-20, 0):], err)
	}
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: "ok \xff\n", Stderr: "warn\n"}
	})
	if out, err := e.m.Rcon(ctx, 1, "list"); err != nil || out != "ok �\nwarn" {
		t.Fatalf("output = %q, %v", out, err)
	}
}

func TestRconParallelLimit(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	ctx := context.Background()
	arrived, release := e.d.Hold(dockertest.OpExecStart)
	defer release()
	errs := make(chan error, instances.MaxParallelRcon)
	for range instances.MaxParallelRcon {
		go func() {
			_, err := e.m.Rcon(ctx, 1, "list")
			errs <- err
		}()
	}
	for range instances.MaxParallelRcon {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("exec start not reached")
		}
	}
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("5th parallel command = %v, want ErrBusy", err)
	}
	release()
	for range instances.MaxParallelRcon {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if out, err := e.m.Rcon(ctx, 1, "list"); err != nil || out != "list" {
		t.Fatalf("after the burst = %q, %v (slots must be free again)", out, err)
	}
}

func TestRconTimeout(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.RconTimeout = 200 * time.Millisecond })
	e.startInstance(t, 1, "survival")
	ctx := context.Background()
	_, release := e.d.Hold(dockertest.OpExecStart)
	begin := time.Now()
	_, err := e.m.Rcon(ctx, 1, "list")
	if !errors.Is(err, agentapi.ErrRconUnavailable) || time.Since(begin) > 5*time.Second {
		t.Fatalf("hanging rcon-cli = %v after %v, want ErrRconUnavailable after the timeout", err, time.Since(begin))
	}
	release()
	if _, err := e.m.Rcon(ctx, 1, "list"); err != nil {
		t.Fatalf("slot not released after a timeout: %v", err)
	}
}
