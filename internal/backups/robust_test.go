package backups_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const msgStalled = "the backup made no progress for 10 minutes"

// socketErr is an agent client error as it really looks: it names the socket path.
var socketErr = fmt.Errorf("%w: Post \"http://agent/v1/instances/rcon\": dial unix /run/mcpanel/agent.sock: connect: no such file or directory", agentapi.ErrUnavailable)

func setStall(e *env, on bool) {
	e.files.mu.Lock()
	defer e.files.mu.Unlock()
	e.files.stall = on
}

func setFilesErr(e *env, op string, err error) {
	e.files.mu.Lock()
	defer e.files.mu.Unlock()
	if err == nil {
		delete(e.files.errs, op)
		return
	}
	e.files.errs[op] = err
}

// A service job whose byte count does not change for 10 minutes (logic clock) is cancelled
// and fails; save-on follows as in every failure path.
func TestStalledBackupFails(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	release := e.files.holdPlan("manual")
	defer release()
	setStall(e, true)
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	e.clk.Advance(9 * time.Minute)
	time.Sleep(50 * time.Millisecond) // some polls: not yet stalled
	if j := e.job(id); j.State != "running" {
		t.Fatalf("failed before 10 minutes without progress: %+v", j)
	}
	e.clk.Advance(time.Minute)
	j := e.waitJob(id)
	if j.State != "failed" || code(j) != "internal" || j.Error != msgStalled {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v, want one cancel", e.files.callLog())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

// Progress resets the watchdog: a long job that keeps writing never stalls.
func TestProgressResetsStallWatchdog(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("manual")
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	for range 3 {
		e.clk.Advance(8 * time.Minute)
		time.Sleep(30 * time.Millisecond) // polls see new bytes
	}
	if j := e.job(id); j.State != "running" {
		t.Fatalf("a job with progress stalled: %+v", j)
	}
	release()
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
}

// The retention forget has the watchdog as well.
func TestStalledForgetFails(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("forget")
	defer release()
	setStall(e, true)
	e.must(http.StatusAccepted, http.MethodDelete, bPath(id, "/"+snap), nil, e.sudoAdmin())
	e.waitStep(id, "deleting")
	e.clk.Advance(10 * time.Minute)
	if j := e.waitJob(id); j.State != "failed" || j.Error != msgStalled {
		t.Fatalf("job = %+v", j)
	}
}

// Restore before the swap: a stalled job is cancelled; the gate stays closed until a poll shows
// that the service job ended.
func TestStalledRestoreKeepsGateUntilEnd(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("restore")
	defer release()
	e.restore(id, snap)
	e.waitStep(id, "restoring")
	setStall(e, true)
	setFilesErr(e, "cancel", errors.New("the swap has begun")) // the cancel does not stop it
	e.clk.Advance(10 * time.Minute)
	eventually(t, "cancel", func() bool { return e.files.count(fmt.Sprintf("cancel %d", id)) >= 1 })
	time.Sleep(50 * time.Millisecond)
	if !e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate open while the service job still runs")
	}
	if j := e.job(id); j.State != "running" {
		t.Fatalf("job = %+v", j)
	}
	release() // the service finishes the swap
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	if e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate still closed")
	}
}

// While restoring, poll errors never end the wait (unlike other steps after 5 misses).
func TestRestoringSurvivesPollErrors(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("restore")
	defer release()
	e.restore(id, snap)
	e.waitStep(id, "restoring")
	setFilesErr(e, "job", filesapi.ErrUnavailable)
	time.Sleep(100 * time.Millisecond) // about 20 polls of 5 ms
	if !e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate opened after poll errors")
	}
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	setFilesErr(e, "job", nil)
	release()
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
}

// Backups send their RCON commands with the long agent-side timeout (60 s; 2 s in the tests).
func TestRconUsesLongTimeout(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.create(id, map[string]any{})
	e.waitJob(id)
	got := e.console.rconTimeouts()
	if len(got) != 3 || slices.ContainsFunc(got, func(d time.Duration) bool { return d != 2*time.Second }) {
		t.Fatalf("timeouts = %v, want three times the RCON timeout", got)
	}
}

func noPath(t *testing.T, what, s string) {
	t.Helper()
	if strings.Contains(s, "/run/") || strings.Contains(s, "/srv/") || strings.Contains(s, ".sock") {
		t.Fatalf("%s names a host path: %q", what, s)
	}
}

// Job errors, last_error and notifications carry fixed texts, never the agent's or the
// service client's error (which names socket and data paths).
func TestJobErrorsHideHostPaths(t *testing.T) {
	t.Run("agent gone at save-off", func(t *testing.T) {
		e := newEnv(t)
		id := e.instance("survival")
		e.agent.set(id, agentapi.StateRunning)
		e.console.failNext("save-off", socketErr)
		e.create(id, map[string]any{})
		j := e.waitJob(id)
		if code(j) != "agent_unavailable" || j.Error != "the agent is not reachable" {
			t.Fatalf("job = %+v", j)
		}
	})
	t.Run("rcon fails in a scheduled run", func(t *testing.T) {
		e := newEnv(t)
		id := e.instance("survival")
		e.agent.set(id, agentapi.StateRunning)
		putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
		e.console.failNext("save-all flush", fmt.Errorf("%w: cannot write /srv/mc/instances/1/world", agentapi.ErrRconUnavailable))
		e.clk.Advance(time.Hour)
		e.tick()
		row := e.schedule(id)
		if row.LastError.String != "RCON command save-all flush failed" {
			t.Fatalf("last_error = %q", row.LastError.String)
		}
		if len(e.notes.Events) != 1 {
			t.Fatalf("events = %v", e.notes.Types())
		}
		noPath(t, "notification", e.notes.Events[0].Message)
	})
	t.Run("stop fails in a restore", func(t *testing.T) {
		e := newEnv(t)
		id := e.instance("survival")
		e.agent.set(id, agentapi.StateRunning)
		e.agent.powerErr = socketErr
		snap := e.files.add(id, filesapi.SnapshotManual, false, "")
		e.restore(id, snap)
		j := e.waitJob(id)
		if code(j) != "agent_unavailable" || j.Error != "the agent is not reachable" {
			t.Fatalf("job = %+v", j)
		}
		if len(e.notes.Events) != 1 {
			t.Fatalf("events = %v", e.notes.Types())
		}
		noPath(t, "notification", e.notes.Events[0].Message)
	})
	t.Run("other stop error", func(t *testing.T) {
		e := newEnv(t)
		id := e.instance("survival")
		e.agent.set(id, agentapi.StateRunning)
		e.agent.powerErr = errors.New("docker: kill /var/lib/docker/containers/abc: permission denied")
		snap := e.files.add(id, filesapi.SnapshotManual, false, "")
		e.restore(id, snap)
		if j := e.waitJob(id); j.Error != "stopping the server failed" {
			t.Fatalf("job = %+v", j)
		}
	})
	t.Run("unknown error", func(t *testing.T) {
		e := newEnv(t)
		id := e.instance("survival")
		setFilesErr(e, "create", errors.New("open /srv/mc/backups/keys/1: permission denied"))
		e.create(id, map[string]any{})
		j := e.waitJob(id)
		if code(j) != "internal" {
			t.Fatalf("job = %+v", j)
		}
		noPath(t, "job error", j.Error)
	})
}

// Run returns within the shutdown wait although the agent hangs in recovery's save-on; the
// mark stays for the next start.
func TestShutdownWithAgentHangingInRecovery(t *testing.T) {
	e := newEnvNoRun(t, nil)
	backups.SetTimings(e.h, backups.Timings{Poll: 5 * time.Millisecond, RCON: 30 * time.Second, Stop: time.Second, Shutdown: 5 * time.Second, IO: time.Second})
	id := e.instance("survival")
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, saveoff_since, updated_at) VALUES (?, ?, 0)`, id, t0.UnixMilli())
	e.console.setHang("save-on")
	e.runNoWait()
	eventually(t, "save-on", func() bool { return slices.Contains(e.console.sent(), fmt.Sprintf("%d save-on", id)) })
	begin := time.Now()
	e.stop()
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("shutdown took %v", d)
	}
	if !e.schedule(id).SaveoffSince.Valid {
		t.Fatal("the mark was cleared although save-on never got through")
	}
}

// A tick that hangs in the save-on retry does not hold up the shutdown, and new jobs are
// refused at once.
func TestShutdownWithAgentHangingInTick(t *testing.T) {
	e := newEnvNoRun(t, nil)
	backups.SetTimings(e.h, backups.Timings{Poll: 5 * time.Millisecond, RCON: 30 * time.Second, Stop: time.Second, Shutdown: 5 * time.Second, IO: time.Second})
	backups.SetTickEvery(e.h, 10*time.Millisecond)
	id := e.instance("survival")
	other := e.instance("other")
	e.console.setHang("save-on")
	e.run()
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, saveoff_since, updated_at) VALUES (?, ?, 0)`, id, t0.UnixMilli())
	eventually(t, "save-on retry", func() bool { return slices.Contains(e.console.sent(), fmt.Sprintf("%d save-on", id)) })
	begin := time.Now()
	e.stop()
	if d := time.Since(begin); d > 3*time.Second {
		t.Fatalf("shutdown took %v", d)
	}
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(other, ""), map[string]any{}, &a); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "backups_unavailable" {
		t.Fatalf("create after shutdown = %d %s", rec.Code, rec.Body)
	}
	if !e.schedule(id).SaveoffSince.Valid {
		t.Fatal("mark cleared")
	}
}

// The save-on retry of a tick never answers the save-off of a running job.
func TestTickSkipsSaveOnDuringBackup(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	release := e.console.hold("save-all flush")
	e.create(id, map[string]any{})
	eventually(t, "flush", func() bool { return slices.Contains(e.console.sent(), fmt.Sprintf("%d save-all flush", id)) })
	backups.Tick(t.Context(), e.h)
	if slices.Contains(e.console.sent(), fmt.Sprintf("%d save-on", id)) {
		t.Fatalf("the tick sent save-on during the backup: %v", e.console.sent())
	}
	release()
	e.waitJob(id)
	n := 0
	for _, c := range e.console.sent() {
		if c == fmt.Sprintf("%d save-on", id) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("rcon = %v, want exactly one save-on", e.console.sent())
	}
}

// A shutdown while a scheduled run waits for RCON is a cancel, not an agent failure: no
// backup.failed.
func TestShutdownDuringSaveIsCanceled(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.console.setHang("save-all flush")
	e.clk.Advance(time.Hour)
	backups.Tick(t.Context(), e.h)
	eventually(t, "flush", func() bool { return slices.Contains(e.console.sent(), fmt.Sprintf("%d save-all flush", id)) })
	e.stop()
	if state, c, _ := backups.JobOf(e.h, id); state != "failed" || c != "canceled" {
		t.Fatalf("job = %s %s, want failed canceled", state, c)
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("events = %v", e.notes.Types())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

// Without a file service nothing panics: the schedule route works, the ticker queues nothing.
func TestNoFilesSkipsSchedules(t *testing.T) {
	e := newEnvNoRun(t, func(d *backups.Deps) { d.Files = nil })
	e.run()
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.clk.Advance(time.Hour)
	e.tick()
	if _, _, ok := backups.JobOf(e.h, id); ok {
		t.Fatal("a job was queued without a file service")
	}
	if err := e.h.PurgeBackups(t.Context(), id); err != nil {
		t.Fatal(err)
	}
}

// The purge waits until the service no longer runs a job of the server.
func TestPurgeWaitsForServiceJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("scheduled")
	e.files.runningJob(id, filesapi.JobBackup, "scheduled") // e.g. left by a crashed panel
	done := make(chan error, 1)
	go func() { done <- e.h.PurgeBackups(t.Context(), id) }()
	time.Sleep(50 * time.Millisecond)
	if n := e.files.count(fmt.Sprintf("purge %d", id)); n != 0 {
		t.Fatalf("purged while the service job runs: %v", e.files.callLog())
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := e.files.count(fmt.Sprintf("purge %d", id)); n != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
}

// Without Docker no container runs: a backup needs no save-off, a restore no stop.
func TestDockerUnavailableCountsAsStopped(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.agent.docker = agentapi.DockerUnavailable
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("backup = %+v", j)
	}
	snap := e.files.list(id)[0].ID
	e.restore(id, snap)
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("restore = %+v", j)
	}
	if len(e.console.sent()) != 0 || len(e.agent.powerLog()) != 0 {
		t.Fatalf("rcon %v, powers %v", e.console.sent(), e.agent.powerLog())
	}
	e.agent.docker = ""
	e.agent.err = agentapi.ErrDockerUnavailable // the same as an error of the instances call
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("backup = %+v", j)
	}
}

// The agent itself gone stays agent_unavailable (backup and restore).
func TestAgentGoneFailsRestore(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.agent.err = socketErr
	e.restore(id, snap)
	j := e.waitJob(id)
	if code(j) != "agent_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	noPath(t, "job error", j.Error)
}

// The download outlives the server's WriteTimeout (deadline extended at the start).
func TestDownloadOutlivesWriteTimeout(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.delay["list"] = 200 * time.Millisecond
	a := e.admin()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := httpx.WithClientIP(r.Context(), netip.MustParseAddr("203.0.113.7"))
		e.mux.ServeHTTP(w, r.WithContext(principal.With(ctx, a)))
	}))
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Config.ErrorLog = slog.NewLogLogger(slog.NewTextHandler(io.Discard, nil), slog.LevelError)
	srv.Start()
	defer srv.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+bPath(id, "/"+snap+"/download"), nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("download through a short WriteTimeout: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || len(body) != len(e.files.zip) {
		t.Fatalf("status %d, %d bytes, %v", resp.StatusCode, len(body), err)
	}
}
