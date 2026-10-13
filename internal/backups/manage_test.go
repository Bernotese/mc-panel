package backups_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

type snapshotJSON struct {
	ID      string  `json:"id"`
	ShortID string  `json:"short_id"`
	Time    int64   `json:"time"`
	Kind    string  `json:"kind"`
	Pinned  bool    `json:"pinned"`
	Note    *string `json:"note"`
	Bytes   int64   `json:"bytes"`
}

type overviewJSON struct {
	Available bool           `json:"available"`
	Snapshots []snapshotJSON `json:"snapshots"`
	Job       *jobJSON       `json:"job"`
	Schedule  scheduleJSON   `json:"schedule"`
}

func (e *env) overview(id int64) overviewJSON {
	e.t.Helper()
	return decode[overviewJSON](e.t, e.must(http.StatusOK, http.MethodGet, bPath(id, ""), nil, e.admin()))
}

func TestOverview(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	old := e.files.add(id, filesapi.SnapshotScheduled, true, "")
	e.clk.Advance(time.Minute)
	manual := e.files.add(id, filesapi.SnapshotManual, false, "vor Update")
	o := e.overview(id)
	if !o.Available || o.Job != nil || len(o.Snapshots) != 2 {
		t.Fatalf("overview = %+v", o)
	}
	if s := o.Snapshots[0]; s.ID != manual || s.Kind != "manual" || s.Note == nil || *s.Note != "vor Update" || s.ShortID != manual[:8] || s.Bytes != 4096 {
		t.Fatalf("first = %+v", s)
	}
	if s := o.Snapshots[1]; s.ID != old || !s.Pinned || s.Note != nil {
		t.Fatalf("second = %+v", s)
	}
	if o.Schedule.Mode != "off" || o.Schedule.KeepLast != 24 || o.Schedule.Timezone != "Europe/Berlin" || o.Schedule.NextRunAt != nil {
		t.Fatalf("schedule = %+v", o.Schedule)
	}
}

func TestOverviewUnavailable(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.status = filesapi.BackupsStatus{Available: false, Reason: "restic not found"}
	if o := e.overview(id); o.Available || len(o.Snapshots) != 0 {
		t.Fatalf("no restic = %+v", o)
	}
	e.files.status = filesapi.BackupsStatus{Available: true}
	e.files.errs["list"] = filesapi.ErrUnavailable
	if o := e.overview(id); o.Available || o.Snapshots == nil {
		t.Fatalf("list error = %+v (snapshots must be [] not null)", o)
	}
	e2 := newEnvNoRun(t, func(d *backups.Deps) { d.Files = nil })
	e2.run()
	id2 := e2.instance("survival")
	if o := e2.overview(id2); o.Available {
		t.Fatalf("no service = %+v", o)
	}
	lobby := storeServer(t, e, "lobby")
	delete(e.files.errs, "list")
	if o := e.overview(lobby); !o.Available || len(o.Snapshots) != 0 {
		t.Fatalf("server without instance = %+v", o)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotPreRestore, false, "")
	a := e.admin()
	if rec := e.do(http.MethodDelete, bPath(id, "/"+snap), nil, &a); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("without sudo = %d", rec.Code)
	}
	rec := e.must(http.StatusAccepted, http.MethodDelete, bPath(id, "/"+snap), nil, e.sudoAdmin())
	if j := decode[jobResp](t, rec).Job; j.Kind != "delete" {
		t.Fatalf("job = %+v", j)
	}
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	if len(e.files.list(id)) != 0 || e.files.count(fmt.Sprintf("forget %d ids %s", id, snap[:8])) != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if es, _ := e.rec.all(); len(es) != 1 || es[0].Action != audit.ActionBackupDelete || es[0].Details["snapshot"] != snap {
		t.Fatalf("audit = %+v", es)
	}
	if rec := e.do(http.MethodDelete, bPath(id, "/"+snap), nil, ptr(e.sudoAdmin())); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rec.Code)
	}
}

func TestPin(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	rec := e.must(http.StatusOK, http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, e.admin())
	pinned := decode[struct {
		Snapshot snapshotJSON `json:"snapshot"`
	}](t, rec).Snapshot
	if !pinned.Pinned || pinned.ID == snap {
		t.Fatalf("pinned = %+v, want a new id", pinned)
	}
	e.must(http.StatusOK, http.MethodPost, bPath(id, "/"+pinned.ID+"/pin"), map[string]any{"pinned": false}, e.admin())
	if got := e.rec.actions(); len(got) != 2 || got[0] != audit.ActionBackupPin+":success" || got[1] != audit.ActionBackupUnpin+":success" {
		t.Fatalf("audit = %v", got)
	}
	manual := e.files.add(id, filesapi.SnapshotManual, false, "")
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+manual+"/pin"), map[string]any{"pinned": true}, &a); rec.Code != http.StatusBadRequest {
		t.Fatalf("pin manual = %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{}, &a); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing pinned = %d", rec.Code)
	}
}

func TestPinTwiceOldIDGone(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	e.must(http.StatusOK, http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, e.admin())
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, &a); rec.Code != http.StatusNotFound {
		t.Fatalf("second tab with the old id = %d %s", rec.Code, rec.Body)
	}
}

func TestPinWhileJobRunning(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, &a); rec.Code != http.StatusConflict || errCode(t, rec) != "backup_running" {
		t.Fatalf("pin during a job = %d %s", rec.Code, rec.Body)
	}
}

// The service refuses a pin with 429 busy while a download or extract runs: still 409
// backup_running, but the message names the file service, not a backup job.
func TestPinWhileServiceBusy(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	e.files.errs["pin"] = filesapi.ErrBusy
	a := e.admin()
	rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, &a)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "backup_running" ||
		!strings.Contains(rec.Body.String(), "file service is busy") {
		t.Fatalf("pin while the service is busy = %d %s", rec.Code, rec.Body)
	}
}

func TestPurgeDropsQueuedJob(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	e.create(b, map[string]any{})
	if err := e.h.PurgeBackups(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if j := e.job(b); j.State != "failed" || code(*j) != "canceled" {
		t.Fatalf("beta job = %+v", j)
	}
	release()
	backups.WaitIdle(e.h)
	if e.files.count(fmt.Sprintf("create %d", b)) != 0 || e.files.count(fmt.Sprintf("purge %d", b)) != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
}

func TestPurgeCancelsRunningJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	if err := e.h.PurgeBackups(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	calls := e.files.callLog()
	if e.files.count(fmt.Sprintf("cancel %d", id)) != 1 || calls[len(calls)-1] != fmt.Sprintf("purge %d", id) {
		t.Fatalf("calls = %v", calls)
	}
}

func ptr[T any](v T) *T { return &v }

// The synchronous routes outlive the server's WriteTimeout (here shortened to 50 ms).
func TestSlowRoutesOutliveWriteTimeout(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.add(id, filesapi.SnapshotScheduled, false, "")
	e.files.delay["list"] = 200 * time.Millisecond
	a := e.admin()
	// the same principal and client IP as e.do, but through a real server with a WriteTimeout
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := httpx.WithClientIP(r.Context(), netip.MustParseAddr("203.0.113.7"))
		e.mux.ServeHTTP(w, r.WithContext(principal.With(ctx, a)))
	}))
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+bPath(id, ""), nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("overview through a short WriteTimeout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// A delete job that waits while its server is deleted ends as canceled: no forget, no audit.
func TestQueuedDeleteOfDeletedServer(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	snap := e.files.add(b, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	e.must(http.StatusAccepted, http.MethodDelete, bPath(b, "/"+snap), nil, e.sudoAdmin())
	e.deleteServer(b)
	release()
	if !backups.WaitIdle(e.h) {
		t.Fatal("jobs did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, b); state != "failed" || c != "canceled" {
		t.Fatalf("delete job = %s %s, want failed canceled", state, c)
	}
	if n := e.files.count("forget"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if got := e.rec.actions(); len(got) != 1 || got[0] != audit.ActionBackupCreate+":success" {
		t.Fatalf("audit = %v, want only alpha's backup", got)
	}
}
