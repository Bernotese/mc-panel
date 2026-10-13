package backups_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/notify"
)

func (e *env) restore(id int64, snap string) jobJSON {
	e.t.Helper()
	rec := e.must(http.StatusAccepted, http.MethodPost, bPath(id, "/"+snap+"/restore"), nil, e.sudoAdmin())
	return *decode[jobResp](e.t, rec).Job
}

func TestRestoreRunningServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	j := e.restore(id, snap)
	if j.Kind != "restore" || j.Snapshot == nil || *j.Snapshot != snap {
		t.Fatalf("queued = %+v", j)
	}
	if done := e.waitJob(id); done.State != "done" {
		t.Fatalf("job = %+v", done)
	}
	if got := e.agent.powerLog(); !slices.Equal(got, []string{fmt.Sprintf("stop %d", id)}) {
		t.Fatalf("powers = %v", got)
	}
	want := []string{fmt.Sprintf("create %d pre-restore", id), fmt.Sprintf("restore %d %s", id, snap[:8])}
	if got := e.files.callLog(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(e.console.sent()) != 0 {
		t.Fatalf("restore sent RCON: %v", e.console.sent())
	}
	eventually(t, "reconcile kick", func() bool { return e.kicks.Load() == 1 })
	if e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate still closed after the restore")
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionBackupRestore || entries[0].Result != audit.ResultSuccess ||
		actors[0] != "root" || entries[0].Details["snapshot"] != snap {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestGateHoldsDuringRestore(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("restore")
	e.restore(id, snap)
	e.waitStep(id, "restoring")
	if !e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate open while restoring")
	}
	other := e.instance("other")
	if e.h.DataJobRunning(t.Context(), other) {
		t.Fatal("gate closed for another server")
	}
	release()
	e.waitJob(id)
	if e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate still closed")
	}
}

func TestGateHoldsWhileStopping(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.agent.stuck = true
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.restore(id, snap)
	e.waitStep(id, "stopping")
	if !e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate open while stopping")
	}
	j := e.waitJob(id)
	if code(j) != "stop_timeout" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count("create") + e.files.count("restore"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if got := e.notes.Types(); len(got) != 1 || got[0] != notify.EventBackupFailed {
		t.Fatalf("events = %v", got)
	}
	if ev := e.notes.Events[0]; ev.Username != "root" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestRestorePreRestoreFails(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.fail["pre-restore"] = "restic: no space left"
	e.restore(id, snap)
	if j := e.waitJob(id); j.State != "failed" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count("restore"); n != 0 {
		t.Fatalf("restored without a safety snapshot: %v", e.files.callLog())
	}
}

func TestRestoreInsufficientStorage(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.errs["restore"] = filesapi.ErrInsufficientStorage
	e.restore(id, snap)
	if j := e.waitJob(id); code(j) != "insufficient_storage" {
		t.Fatalf("job = %+v", j)
	}
}

func TestRestoreRequests(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/restore"), nil, &a); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("without sudo = %d %s", rec.Code, rec.Body)
	}
	s := e.sudoAdmin()
	if rec := e.do(http.MethodPost, bPath(id, "/XYZ/restore"), nil, &s); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", rec.Code)
	}
	unknown := fmt.Sprintf("%064x", 999)
	if rec := e.do(http.MethodPost, bPath(id, "/"+unknown+"/restore"), nil, &s); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown snapshot = %d", rec.Code)
	}
	if len(e.rec.actions()) != 0 || e.files.count("create") != 0 {
		t.Fatalf("rejections had effects: %v %v", e.rec.actions(), e.files.callLog())
	}
}

// Review Focus 4: the gate closes with the 202 although the restore still waits behind another
// server's backup, so a power start right after the restore request is refused.
func TestGateHoldsWhileRestoreQueued(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	snap := e.files.add(b, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	j := e.restore(b, snap)
	if j.State != "queued" || j.QueuePosition != 1 {
		t.Fatalf("restore = %+v", j)
	}
	if !e.h.DataJobRunning(t.Context(), b) {
		t.Fatal("gate open while the restore waits")
	}
	if e.h.DataJobRunning(t.Context(), a) {
		t.Fatal("gate closed for the server that only backs up")
	}
	release()
	e.waitJob(b)
	if e.h.DataJobRunning(t.Context(), b) {
		t.Fatal("gate still closed after the restore")
	}
}

// Review Focus 4: a restore while a backup of the same server waits or runs.
func TestRestoreWhileBackupQueued(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	snap := e.files.add(b, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	e.create(b, map[string]any{}) // queued behind alpha
	s := e.sudoAdmin()
	if rec := e.do(http.MethodPost, bPath(b, "/"+snap+"/restore"), nil, &s); rec.Code != http.StatusConflict || errCode(t, rec) != "backup_running" {
		t.Fatalf("restore while queued = %d %s", rec.Code, rec.Body)
	}
}

func TestRestoreWithoutAgent(t *testing.T) {
	e := newEnvNoRun(t, func(d *backups.Deps) { d.Instances = nil })
	e.run()
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.restore(id, snap)
	if j := e.waitJob(id); code(j) != "agent_unavailable" {
		t.Fatalf("job = %+v", j)
	}
}
