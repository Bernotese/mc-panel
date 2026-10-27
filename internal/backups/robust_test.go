package backups_test

import (
	"context"
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
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const msgStalled = "the backup made no progress for 10 minutes"

// errSocket is an agent client error as it really looks: it names the socket path.
var errSocket = fmt.Errorf("%w: Post \"http://agent/v1/instances/rcon\": dial unix /run/mcpanel/agent.sock: connect: no such file or directory", agentapi.ErrUnavailable)

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

// polled waits until the panel polled the server's running service job twice more than now
// (at least twice in all): its waitService has then read the logic clock after every Advance
// before this call (its start, stall and limit times), however slow the runner is. A fixed
// sleep is not enough under load: an Advance before waitService took its start time moves the
// time limit (seen as "timed out waiting for job end" in CI), and two Advances without a poll
// in between look like a stall.
func (e *env) polled(id int64) {
	e.t.Helper()
	want := e.files.polls(id) + 2
	eventually(e.t, "two polls of the service job", func() bool { return e.files.polls(id) >= want })
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
	e.polled(id)
	e.clk.Advance(9 * time.Minute)
	e.polled(id) // not yet stalled
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
	e.polled(id)
	for range 3 {
		e.clk.Advance(8 * time.Minute)
		e.polled(id) // polls see new bytes
	}
	if j := e.job(id); j.State != "running" {
		t.Fatalf("a job with progress stalled: %+v", j)
	}
	release()
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
}

// A forget reports no bytes: no stall watchdog, but an overall limit of 2 hours.
func TestForgetTimeLimit(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("forget")
	defer release()
	setStall(e, true)
	e.must(http.StatusAccepted, http.MethodDelete, bPath(id, "/"+snap), nil, e.sudoAdmin())
	e.waitStep(id, "deleting")
	e.polled(id)
	e.clk.Advance(time.Hour)
	e.polled(id) // no stall watchdog for a forget
	if j := e.job(id); j.State != "running" {
		t.Fatalf("forget ended after an hour without bytes: %+v", j)
	}
	e.clk.Advance(time.Hour)
	if j := e.waitJob(id); j.State != "failed" || code(j) != "internal" || j.Error != "deleting snapshots did not finish within 2 hours" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
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
	e.polled(id) // the restore's waitService runs
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
		e.console.failNext("save-off", errSocket)
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
		e.agent.powerErr = errSocket
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

// Docker not reachable is never "stopped" (dockerd may hang while the containers run): a
// backup fails without save-off and without a snapshot, a restore without a stop.
func TestDockerUnavailableFailsJobs(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(a *fakeAgent)
	}{
		{"status", func(a *fakeAgent) { a.docker = agentapi.DockerUnavailable }},
		{"error", func(a *fakeAgent) {
			a.err = fmt.Errorf("%w: GET /containers/json: context deadline exceeded", agentapi.ErrDockerUnavailable)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			id := e.instance("survival")
			e.agent.set(id, agentapi.StateRunning)
			snap := e.files.add(id, filesapi.SnapshotManual, false, "")
			c.set(e.agent)
			e.create(id, map[string]any{})
			if j := e.waitJob(id); code(j) != "agent_unavailable" || j.Error != "Docker is not reachable" {
				t.Fatalf("backup = %+v", j)
			}
			e.restore(id, snap)
			if j := e.waitJob(id); code(j) != "agent_unavailable" || j.Error != "Docker is not reachable" {
				t.Fatalf("restore = %+v", j)
			}
			if len(e.console.sent()) != 0 || len(e.agent.powerLog()) != 0 || e.files.count("create") != 0 || e.files.count("restore") != 0 {
				t.Fatalf("rcon %v, powers %v, calls %v", e.console.sent(), e.agent.powerLog(), e.files.callLog())
			}
		})
	}
}

// Docker briefly not reachable while the restore waits for the stop: those answers are not
// "stopped"; the server that keeps running ends in stop_timeout, nothing is restored.
func TestDockerBlipDuringStopWait(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.agent.stuck = true
	e.agent.blipAfterStop = 3
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.restore(id, snap)
	if j := e.waitJob(id); code(j) != "stop_timeout" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count("create") + e.files.count("restore"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
}

// After the blip a successful answer that shows the stop lets the restore go on.
func TestDockerBlipThenStopped(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.agent.blipAfterStop = 3
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.restore(id, snap)
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
}

// A job that only waits does not hold the save lock: a marked server gets its save-on retry.
func TestSaveOnRetryNotBlockedByWaitingJob(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	e.agent.set(b, agentapi.StateRunning)
	release := e.files.holdPlan("manual")
	defer release()
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, saveoff_since, updated_at) VALUES (?, ?, 0)`, b, t0.UnixMilli())
	if j := e.create(b, map[string]any{}); j.State != "queued" {
		t.Fatalf("beta = %+v", j)
	}
	backups.Tick(t.Context(), e.h)
	if !slices.Contains(e.console.sent(), fmt.Sprintf("%d save-on", b)) {
		t.Fatalf("rcon = %v, want beta's save-on", e.console.sent())
	}
	if e.schedule(b).SaveoffSince.Valid {
		t.Fatal("mark still set")
	}
}

// A server deleted and purged while its backup waits for RCON: the job ends as canceled
// without a backup.create entry (the cancel of the purge does not hide the deletion).
func TestPurgeOfDeletedServerDuringBackup(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.setHang("save-all flush")
	purged := make(chan error, 1)
	e.console.onCmd = func(cmd string) {
		if cmd == "save-all flush" {
			e.deleteServer(id)
			go func() { purged <- e.h.PurgeBackups(context.Background(), id) }()
		}
	}
	e.create(id, map[string]any{})
	if err := <-purged; err != nil {
		t.Fatal(err)
	}
	if !backups.WaitIdle(e.h) {
		t.Fatal("job did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, id); state != "failed" || c != "canceled" {
		t.Fatalf("job = %s %s", state, c)
	}
	if got := e.rec.actions(); len(got) != 0 {
		t.Fatalf("audit = %v", got)
	}
	if e.files.count("create") != 0 || e.files.count(fmt.Sprintf("purge %d", id)) != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

// The agent itself gone stays agent_unavailable (backup and restore).
func TestAgentGoneFailsRestore(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.agent.err = errSocket
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

// SEC-25: a backup that keeps writing but does not finish within backups.max_duration (4 h by
// default) is cancelled; save-on follows and a manual one notifies backup.failed.
func TestBackupMaxDuration(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	// The time limit runs from the start of waitService.
	e.polled(id)
	for range 29 { // 3 h 52 min with progress: no stall, below the limit
		e.clk.Advance(8 * time.Minute)
		e.polled(id)
	}
	if j := e.job(id); j.State != "running" {
		t.Fatalf("ended before the limit: %+v", j)
	}
	e.clk.Advance(8 * time.Minute)
	j := e.waitJob(id)
	if j.State != "failed" || code(j) != "internal" || j.Error != "the backup did not finish within 4 hours" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v, want one cancel", e.files.callLog())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
	if types := e.notes.Types(); !slices.Equal(types, []notify.EventType{notify.EventBackupFailed}) {
		t.Fatalf("notifications = %v", types)
	}
	// The cooldown holds: the backup ran (and kept save-off on) for 4 hours.
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("restart after the time limit = %d %s", rec.Code, rec.Body)
	}
}
