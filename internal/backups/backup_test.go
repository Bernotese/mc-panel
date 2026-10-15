package backups_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type jobJSON struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Trigger       string  `json:"trigger"`
	State         string  `json:"state"`
	Step          string  `json:"step"`
	QueuePosition int     `json:"queue_position"`
	Bytes         int64   `json:"bytes"`
	Snapshot      *string `json:"snapshot"`
	ErrorCode     *string `json:"error_code"`
	Error         string  `json:"error"`
	QueuedAt      int64   `json:"queued_at"`
	StartedAt     int64   `json:"started_at"`
	FinishedAt    int64   `json:"finished_at"`
}

type jobResp struct {
	Job *jobJSON `json:"job"`
}

func (e *env) job(id int64) *jobJSON {
	e.t.Helper()
	return decode[jobResp](e.t, e.must(http.StatusOK, http.MethodGet, bPath(id, "/job"), nil, e.admin())).Job
}

// waitJob waits until the server's job has ended and returns it.
func (e *env) waitJob(id int64) jobJSON {
	e.t.Helper()
	var j *jobJSON
	eventually(e.t, "job end", func() bool {
		j = e.job(id)
		return j != nil && (j.State == "done" || j.State == "failed")
	})
	return *j
}

// waitStep waits until the server's job is running in step.
func (e *env) waitStep(id int64, step string) {
	e.t.Helper()
	eventually(e.t, "step "+step, func() bool {
		j := e.job(id)
		return j != nil && j.State == "running" && j.Step == step
	})
}

func (e *env) create(id int64, body any) jobJSON {
	e.t.Helper()
	rec := e.must(http.StatusAccepted, http.MethodPost, bPath(id, ""), body, e.admin())
	return *decode[jobResp](e.t, rec).Job
}

func code(j jobJSON) string {
	if j.ErrorCode == nil {
		return ""
	}
	return *j.ErrorCode
}

func TestJobRouteBeforeAnyJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	if j := e.job(id); j != nil {
		t.Fatalf("job = %+v, want null", j)
	}
}

func TestCreateBackupStoppedServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	j := e.create(id, map[string]any{"note": "vor Update"})
	if j.Kind != "backup" || j.Trigger != "manual" || j.QueuedAt != t0.UnixMilli() {
		t.Fatalf("queued job = %+v", j)
	}
	done := e.waitJob(id)
	if done.State != "done" || done.Step != "done" || done.Snapshot == nil || done.ErrorCode != nil {
		t.Fatalf("job = %+v", done)
	}
	if got := e.files.callLog(); !slices.Equal(got, []string{fmt.Sprintf("create %d manual", id)}) {
		t.Fatalf("calls = %v", got)
	}
	if s := e.files.list(id); len(s) != 1 || s[0].Note != "vor Update" || s[0].ID != *done.Snapshot {
		t.Fatalf("snapshots = %+v", s)
	}
	if cmds := e.console.sent(); len(cmds) != 0 {
		t.Fatalf("a stopped server got RCON: %v", cmds)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionBackupCreate || entries[0].Result != audit.ResultSuccess ||
		actors[0] != "root" || entries[0].Details["snapshot"] != *done.Snapshot || entries[0].Details["note"] != "vor Update" {
		t.Fatalf("audit = %+v %v", entries, actors)
	}
}

func TestCreateBackupRunningServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	want := []string{fmt.Sprintf("%d save-off", id), fmt.Sprintf("%d save-all flush", id), fmt.Sprintf("%d save-on", id)}
	if got := e.console.sent(); !slices.Equal(got, want) {
		t.Fatalf("rcon = %v, want %v", got, want)
	}
	if row := e.schedule(id); row.SaveoffSince.Valid {
		t.Fatalf("saveoff_since still set: %+v", row)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	for _, note := range []string{strings.Repeat("ä", 65), "a\nb", "   "} {
		a := e.admin()
		if rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{"note": note}, &a); rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("note %q = %d %s", note, rec.Code, rec.Body)
		}
	}
	lobby := storeServer(t, e, "lobby") // no instance
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(lobby, ""), map[string]any{}, &a); rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("no instance = %d %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("rejections audited: %v", e.rec.actions())
	}
}

func TestCreateUnavailable(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.status = filesapi.BackupsStatus{Available: false, Reason: "restic not found"}
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "backups_unavailable" {
		t.Fatalf("no restic = %d %s", rec.Code, rec.Body)
	}
	e2 := newEnvNoRun(t, func(d *backups.Deps) { d.Files = nil })
	e2.run()
	id2 := e2.instance("survival")
	a2 := e2.admin()
	if rec := e2.do(http.MethodPost, bPath(id2, ""), map[string]any{}, &a2); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "files_unavailable" {
		t.Fatalf("no service = %d %s", rec.Code, rec.Body)
	}
}

func TestCreateTwiceConcurrently(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("manual")
	defer release()
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := e.admin()
			rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a)
			if rec.Code == http.StatusConflict && errCode(t, rec) != "backup_running" {
				t.Errorf("409 with %s", rec.Body)
			}
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	var got []int
	for c := range codes {
		got = append(got, c)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusAccepted, http.StatusConflict}) {
		t.Fatalf("codes = %v", got)
	}
}

func TestQueueRunsOneJobAtATime(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	qb := e.create(b, map[string]any{})
	if qb.State != "queued" || qb.QueuePosition != 1 {
		t.Fatalf("second job = %+v, want queued at 1", qb)
	}
	if n := e.files.count(fmt.Sprintf("create %d", b)); n != 0 {
		t.Fatalf("beta started while alpha runs")
	}
	release()
	if j := e.waitJob(b); j.State != "done" {
		t.Fatalf("beta = %+v", j)
	}
	if got := e.files.callLog(); !slices.Equal(got, []string{fmt.Sprintf("create %d manual", a), fmt.Sprintf("create %d manual", b)}) {
		t.Fatalf("calls = %v", got)
	}
}

// Review Focus 1: every failure after save-off ends with save-on.
func TestSaveOffFailsStillSendsSaveOn(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-off", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "failed" || code(j) != "rcon_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); !slices.Equal(got, []string{fmt.Sprintf("%d save-off", id), fmt.Sprintf("%d save-on", id)}) {
		t.Fatalf("rcon = %v", got)
	}
	if n := e.files.count("create"); n != 0 {
		t.Fatal("the service was called although save-off failed")
	}
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since still set")
	}
}

func TestFlushFailsStillSendsSaveOn(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-all flush", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "rcon_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

func TestSaveOnAfterServiceJobFails(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.files.fail["manual"] = "restic: disk full"
	e.create(id, map[string]any{})
	j := e.waitJob(id)
	if j.State != "failed" || code(j) != "internal" || j.Error != "restic: disk full" {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
	if es, _ := e.rec.all(); len(es) != 1 || es[0].Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", es)
	}
}

// A failed service job carries its wire code (filesapi.Job.Code); the panel job maps it (V5).
func TestServiceJobFailureCodes(t *testing.T) {
	for _, c := range []struct{ wire, want string }{
		{filesapi.CodeTooLarge, "too_large"},
		{filesapi.CodeInsufficientStorage, "insufficient_storage"},
		{filesapi.CodeBusy, "files_busy"},
		{filesapi.CodeUnavailable, "backups_unavailable"},
		{"", "internal"},
		{"something_new", "internal"},
	} {
		t.Run(c.want+"/"+c.wire, func(t *testing.T) {
			e := newEnv(t)
			id := e.instance("survival")
			e.files.fail["manual"] = "the job failed"
			e.files.failCode["manual"] = c.wire
			e.create(id, map[string]any{})
			if j := e.waitJob(id); j.State != "failed" || code(j) != c.want || j.Error != "the job failed" {
				t.Fatalf("job = %+v, want code %s", j, c.want)
			}
		})
	}
}

func TestSaveOnAfterCreateRefused(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.files.errs["create"] = filesapi.ErrBusy // e.g. an extract runs in the files tab
	e.create(id, map[string]any{})
	j := e.waitJob(id)
	if code(j) != "files_busy" || !strings.Contains(j.Error, "busy") {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

func TestSaveOnRetriedByTicker(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-on", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("a failed save-on fails the backup: %+v", j)
	}
	if !e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since cleared although save-on failed")
	}
	backups.Tick(t.Context(), e.h)
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) || len(got) != 4 {
		t.Fatalf("rcon = %v, want a second save-on", got)
	}
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since still set after the retry")
	}
}

func TestSaveOnNotRunningClears(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-on", agentapi.ErrNotRunning) // the server stopped meanwhile
	e.create(id, map[string]any{})
	e.waitJob(id)
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("a stopped server keeps saveoff_since")
	}
}

func TestShutdownCancelsServiceJobAndResumes(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	e.stop()
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v, want one cancel", e.files.callLog())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since still set after shutdown")
	}
}

func TestRecoverAfterCrash(t *testing.T) {
	e := newEnvNoRun(t, nil)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	// next_run_at lies ahead: the first tick must not start a new run that would set the row
	// to running again (a missed run is caught up, TestMissedRunRunsOnce)
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, mode, next_run_at, last_status, saveoff_since, updated_at)
		VALUES (?, 'daily', ?, 'running', ?, 0)`, id, t0.Add(time.Hour).UnixMilli(), t0.Add(-10*time.Minute).UnixMilli())
	release := e.files.holdPlan("scheduled")
	defer release()
	e.files.runningJob(id, filesapi.JobBackup, "scheduled") // the backup the crashed panel started
	e.run()
	eventually(t, "save-on", func() bool { return slices.Contains(e.console.sent(), fmt.Sprintf("%d save-on", id)) })
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	row := e.schedule(id)
	if row.SaveoffSince.Valid || row.LastStatus.String != "failed" || row.LastError.String != "the panel restarted during the backup" {
		t.Fatalf("row = %+v", row)
	}
}

func TestAgentDownFailsBackup(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.err = agentapi.ErrUnavailable
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "agent_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if len(e.console.sent()) != 0 || e.files.count("create") != 0 {
		t.Fatalf("rcon %v, calls %v", e.console.sent(), e.files.callLog())
	}
}

func TestServiceLosesJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.loseJobs = true
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "files_unavailable" {
		t.Fatalf("job = %+v", j)
	}
}

func TestServiceUnreachableWhilePolling(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.errs["job"] = filesapi.ErrUnavailable
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "files_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v, want a cancel after the misses", e.files.callLog())
	}
}

// deleteServer removes the server row like DELETE /servers/{id} (which only refuses while a
// restore holds the gate); instance and schedule rows go with it (ON DELETE CASCADE).
func (e *env) deleteServer(id int64) {
	e.t.Helper()
	mustExec(e.t, e.db, `DELETE FROM servers WHERE id = ?`, id)
}

// A backup that waits while its server is deleted ends as canceled without a service call,
// an audit entry or a notification.
func TestQueuedJobOfDeletedServer(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	e.create(b, map[string]any{})
	e.deleteServer(b)
	release()
	if !backups.WaitIdle(e.h) {
		t.Fatal("jobs did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, b); state != "failed" || c != "canceled" {
		t.Fatalf("beta job = %s %s, want failed canceled", state, c)
	}
	if n := e.files.count(fmt.Sprintf("create %d", b)); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{audit.ActionBackupCreate + ":success"}) {
		t.Fatalf("audit = %v, want only alpha's backup", got)
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("events = %v", e.notes.Types())
	}
}

// A server deleted while its job runs, before the next step: no further step, the job ends
// as canceled; save-on still follows a save-off.
func TestDeletedBeforeNextStep(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	deleted := false
	e.console.onCmd = func(cmd string) {
		if cmd == "save-all flush" && !deleted {
			deleted = true
			e.deleteServer(id)
		}
	}
	e.create(id, map[string]any{})
	if !backups.WaitIdle(e.h) {
		t.Fatal("job did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, id); state != "failed" || c != "canceled" {
		t.Fatalf("job = %s %s, want failed canceled", state, c)
	}
	if n := e.files.count("create"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("audit = %v", e.rec.actions())
	}
}

// A service backup job that already runs may finish for a deleted server.
func TestRunningBackupOfDeletedServerFinishes(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("manual")
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	e.deleteServer(id)
	release()
	if !backups.WaitIdle(e.h) {
		t.Fatal("job did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, id); state != "done" || c != "" {
		t.Fatalf("job = %s %s, want done", state, c)
	}
	if n := e.files.count("cancel"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
}

// SEC-04/SEC-25: a manual backup at most 10 minutes after the last one ended; one refused
// before the file service started does not count, one that failed in the service does, and
// other servers are not affected.
func TestManualBackupCooldown(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	other := e.instance("creative")
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	a := e.admin()
	rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "600" {
		t.Fatalf("second manual backup = %d %s (Retry-After %q)", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	e.create(other, map[string]any{}) // another server
	e.waitJob(other)
	e.clk.Advance(10 * time.Minute)
	e.create(id, map[string]any{})
	e.waitJob(id)

	// A manual backup refused before the file service started its stream does not count:
	// the service is busy, or save-off fails on the running server.
	e.clk.Advance(10 * time.Minute)
	setFilesErr(e, "create", filesapi.ErrBusy)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "failed" || code(j) != "files_busy" {
		t.Fatalf("refused by the service = %+v", j)
	}
	setFilesErr(e, "create", nil)
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-off", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "failed" || code(j) != "rcon_unavailable" {
		t.Fatalf("save-off failed = %+v", j)
	}

	// One that ran and failed in the service counts: no immediate restart (save-off again).
	e.files.mu.Lock()
	e.files.fail["manual"] = "restic: disk full"
	e.files.mu.Unlock()
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "failed" || code(j) != "internal" {
		t.Fatalf("service job failed = %+v", j)
	}
	if rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("restart after a failed service job = %d %s", rec.Code, rec.Body)
	}
}
