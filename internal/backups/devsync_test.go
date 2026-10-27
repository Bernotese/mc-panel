package backups_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// orderLog records the calls of the files service and the agent (and Apply) in one order.
type orderLog struct {
	mu    sync.Mutex
	lines []string
}

func (o *orderLog) add(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, fmt.Sprintf(format, args...))
}

func (o *orderLog) all() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.lines)
}

// orderedFiles and orderedAgent add every writing call to the order log.
type orderedFiles struct {
	*fakeFiles
	log *orderLog
}

func (f orderedFiles) CreateBackup(ctx context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	f.log.add("backup %d %s", req.ID, req.Kind)
	return f.fakeFiles.CreateBackup(ctx, req)
}

func (f orderedFiles) RestoreFrom(ctx context.Context, target, source int64, snap string) (filesapi.Job, error) {
	f.log.add("restore-from %d %d %s", target, source, short(snap))
	return f.fakeFiles.RestoreFrom(ctx, target, source, snap)
}

func (f orderedFiles) RestoreBackup(ctx context.Context, id int64, snap string) (filesapi.Job, error) {
	f.log.add("restore %d %s", id, short(snap))
	return f.fakeFiles.RestoreBackup(ctx, id, snap)
}

func (f orderedFiles) ForgetBackups(ctx context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	ids := make([]string, len(req.Snapshots))
	for i, s := range req.Snapshots {
		ids[i] = short(s)
	}
	f.log.add("forget %d %s", req.ID, strings.Join(ids, ","))
	return f.fakeFiles.ForgetBackups(ctx, req)
}

type orderedAgent struct {
	*fakeAgent
	log *orderLog
}

func (a orderedAgent) Power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	a.log.add("%s %d", action, id)
	return a.fakeAgent.Power(ctx, id, action)
}

// devEnv is an env with a prod ("survival") and its dev ("dev-survival") and an order log.
type devEnv struct {
	*env
	log       *orderLog
	prod, dev int64
}

func newDevEnv(t *testing.T) *devEnv {
	t.Helper()
	log := &orderLog{}
	e := newEnvNoRun(t, func(d *backups.Deps) {
		d.Files = orderedFiles{d.Files.(*fakeFiles), log}
		d.Instances = orderedAgent{d.Instances.(*fakeAgent), log}
	})
	e.run()
	de := &devEnv{env: e, log: log, prod: e.instance("survival"), dev: e.instance("dev-survival")}
	if _, err := store.New(e.db).DevCreate(context.Background(), store.DevCreateParams{
		ServerID: de.dev, SourceID: de.prod, SyncTime: 300, IdleMinutes: 15, PropertyOverrides: `{}`,
		CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	return de
}

type syncResult struct {
	snap string
	err  error
}

// queue queues a scheduled sync of the dev; apply (nil = success) runs as Apply. The channel
// gets Done's arguments; a second Done call fails the test.
func (e *devEnv) queue(apply func() error) <-chan syncResult {
	e.t.Helper()
	return e.queueAs("scheduled", apply)
}

// queueAs is queue with the given trigger.
func (e *devEnv) queueAs(trigger string, apply func() error) <-chan syncResult {
	e.t.Helper()
	done := make(chan syncResult, 1)
	var calls sync.Mutex
	n := 0
	err := e.h.QueueDevSync(context.Background(), backups.DevSync{
		DevID: e.dev, SourceID: e.prod, DevName: "dev-survival", SourceName: "survival", Trigger: trigger,
		Apply: func(context.Context) error {
			e.log.add("apply %d", e.dev)
			if apply != nil {
				return apply()
			}
			return nil
		},
		Done: func(snap string, err error) {
			calls.Lock()
			defer calls.Unlock()
			if n++; n > 1 {
				e.t.Errorf("Done called %d times", n)
				return
			}
			done <- syncResult{snap, err}
		},
	})
	if err != nil {
		e.t.Fatalf("QueueDevSync: %v", err)
	}
	return done
}

func (e *devEnv) wait(ch <-chan syncResult) syncResult {
	e.t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		e.t.Fatal("Done was not called")
	}
	return syncResult{}
}

// snapAt seeds a snapshot of the prod taken d before t0.
func (e *devEnv) snapAt(d time.Duration, kind filesapi.SnapshotKind) string {
	e.clk.Set(t0.Add(-d))
	defer e.clk.Set(t0)
	return e.files.add(e.prod, kind, false, "")
}

// noProdWrite fails unless the log has no stop, restore or restore-from into the prod.
func (e *devEnv) noProdWrite() {
	e.t.Helper()
	for _, l := range e.log.all() {
		if l == fmt.Sprintf("stop %d", e.prod) || strings.HasPrefix(l, fmt.Sprintf("restore %d ", e.prod)) ||
			strings.HasPrefix(l, fmt.Sprintf("restore-from %d ", e.prod)) || strings.HasPrefix(l, "restore ") {
			e.t.Fatalf("the prod was written: %v", e.log.all())
		}
	}
}

func TestDevSyncReusesFreshScheduled(t *testing.T) {
	e := newDevEnv(t)
	snap := e.snapAt(2*time.Hour, filesapi.SnapshotScheduled)
	r := e.wait(e.queue(nil))
	if r.err != nil || r.snap != snap {
		t.Fatalf("done = %+v, want %s", r, short(snap))
	}
	want := []string{fmt.Sprintf("restore-from %d %d %s", e.dev, e.prod, short(snap)), fmt.Sprintf("apply %d", e.dev)}
	if got := e.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(e.console.sent()) != 0 {
		t.Fatalf("RCON sent although a snapshot was reused: %v", e.console.sent())
	}
}

func TestDevSyncIgnoresManual(t *testing.T) {
	e := newDevEnv(t)
	manual := e.snapAt(time.Hour, filesapi.SnapshotManual)
	e.snapAt(time.Hour, filesapi.SnapshotPreRestore)
	r := e.wait(e.queue(nil))
	if r.err != nil || r.snap == "" || r.snap == manual {
		t.Fatalf("done = %+v", r)
	}
	got := e.log.all()
	if len(got) < 2 || got[0] != fmt.Sprintf("backup %d dev-sync", e.prod) || got[1] != fmt.Sprintf("restore-from %d %d %s", e.dev, e.prod, short(r.snap)) {
		t.Fatalf("calls = %v", got)
	}
}

func TestDevSyncTooOld(t *testing.T) {
	e := newDevEnv(t)
	e.snapAt(7*time.Hour, filesapi.SnapshotScheduled)
	r := e.wait(e.queue(nil))
	if r.err != nil || e.log.all()[0] != fmt.Sprintf("backup %d dev-sync", e.prod) {
		t.Fatalf("done = %+v, calls = %v", r, e.log.all())
	}

	// A fresh snapshot that is not newer than the last sync is not used either.
	e2 := newDevEnv(t)
	e2.snapAt(2*time.Hour, filesapi.SnapshotScheduled)
	mustExec(t, e2.db, `UPDATE dev_instances SET last_sync_at = ? WHERE server_id = ?`, t0.Add(-time.Hour).UnixMilli(), e2.dev)
	r = e2.wait(e2.queue(nil))
	if r.err != nil || e2.log.all()[0] != fmt.Sprintf("backup %d dev-sync", e2.prod) {
		t.Fatalf("after a sync: done = %+v, calls = %v", r, e2.log.all())
	}
}

// TestDevSyncOrder (review focus #1): backup of the prod, stop of the dev, restore from the
// prod's repository into the dev, Apply, forget of the prod's older dev-sync snapshots only.
// The prod is never stopped or restored, and RCON goes only to the prod's save-off/save-on.
func TestDevSyncOrder(t *testing.T) {
	e := newDevEnv(t)
	e.agent.set(e.prod, agentapi.StateRunning)
	e.agent.set(e.dev, agentapi.StateRunning)
	oldSync := e.snapAt(8*time.Hour, filesapi.SnapshotDevSync)
	e.snapAt(8*time.Hour, filesapi.SnapshotScheduled)
	e.snapAt(8*time.Hour, filesapi.SnapshotManual)
	r := e.wait(e.queue(nil))
	if r.err != nil {
		t.Fatal(r.err)
	}
	want := []string{
		fmt.Sprintf("backup %d dev-sync", e.prod),
		fmt.Sprintf("stop %d", e.dev),
		fmt.Sprintf("restore-from %d %d %s", e.dev, e.prod, short(r.snap)),
		fmt.Sprintf("apply %d", e.dev),
		fmt.Sprintf("forget %d %s", e.prod, short(oldSync)),
	}
	if got := e.log.all(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v\nwant %v", got, want)
	}
	e.noProdWrite()
	wantRcon := []string{fmt.Sprintf("%d save-off", e.prod), fmt.Sprintf("%d save-all flush", e.prod), fmt.Sprintf("%d save-on", e.prod)}
	if got := e.console.sent(); !slices.Equal(got, wantRcon) {
		t.Fatalf("rcon = %v", got)
	}
	kinds := map[filesapi.SnapshotKind]int{}
	for _, s := range e.files.list(e.prod) {
		kinds[s.Kind]++
	}
	if kinds[filesapi.SnapshotDevSync] != 1 || kinds[filesapi.SnapshotScheduled] != 1 || kinds[filesapi.SnapshotManual] != 1 {
		t.Fatalf("prod snapshots after the sync: %v", kinds)
	}
	if len(e.notes.Types()) != 0 {
		t.Fatalf("notifications: %v", e.notes.Types())
	}
}

// blockWorker keeps the queue's worker busy with a held backup of another server; the
// returned func releases it.
func (e *devEnv) blockWorker() func() {
	e.t.Helper()
	other := e.instance("blocker")
	release := e.files.holdPlan("manual")
	e.create(other, map[string]any{})
	e.waitStep(other, "backing_up")
	return release
}

func TestDevSyncHoldsGate(t *testing.T) {
	e := newDevEnv(t)
	ctx := t.Context()
	release := e.blockWorker()
	holdRestore := e.files.holdPlan("restore")
	ch := e.queue(nil)
	if !e.h.DataJobRunning(ctx, e.dev) || e.h.DataJobRunning(ctx, e.prod) {
		t.Fatal("queued: the gate must hold the dev and only the dev")
	}
	release()
	eventually(t, "restoring", func() bool {
		st, _, _ := backups.JobOf(e.h, e.dev)
		return st == "running" && e.files.count("restore-from") == 1
	})
	if !e.h.DataJobRunning(ctx, e.dev) || e.h.DataJobRunning(ctx, e.prod) {
		t.Fatal("running: the gate must hold the dev and only the dev")
	}
	holdRestore()
	if r := e.wait(ch); r.err != nil {
		t.Fatal(r.err)
	}
	eventually(t, "gate open", func() bool { return !e.h.DataJobRunning(ctx, e.dev) })
	if err := e.h.QueueDevSync(ctx, backups.DevSync{DevID: e.dev, SourceID: e.prod, Apply: func(context.Context) error { return nil }, Done: func(string, error) {}}); err != nil {
		t.Fatalf("a second sync after the first: %v", err)
	}
	if err := e.h.QueueDevSync(ctx, backups.DevSync{DevID: e.dev, SourceID: e.prod, Apply: func(context.Context) error { return nil }, Done: func(string, error) {}}); !errors.Is(err, backups.ErrBackupRunning) {
		t.Fatalf("a sync while one is queued: %v", err)
	}
}

// TestQueueDevSyncRefusesWrongDirection: swapped ids, a target that is no dev and a source
// that is not the dev's are refused before anything is queued; a source changed after the
// queueing fails the job before any restore.
func TestQueueDevSyncRefusesWrongDirection(t *testing.T) {
	e := newDevEnv(t)
	ctx := t.Context()
	other := e.instance("other")
	ok := func(context.Context) error { return nil }
	for _, s := range []backups.DevSync{
		{DevID: e.prod, SourceID: e.dev},
		{DevID: other, SourceID: e.prod},
		{DevID: e.dev, SourceID: other},
		{DevID: e.dev, SourceID: e.dev},
	} {
		s.Apply, s.Done = ok, func(string, error) {}
		if err := e.h.QueueDevSync(ctx, s); err == nil {
			t.Fatalf("queued %d <- %d", s.DevID, s.SourceID)
		}
	}
	release := e.blockWorker()
	ch := e.queue(nil)
	mustExec(t, e.db, `UPDATE dev_instances SET source_id = ? WHERE server_id = ?`, other, e.dev)
	release()
	if r := e.wait(ch); r.err == nil || errors.Is(r.err, context.Canceled) {
		t.Fatalf("done = %+v", r)
	}
	if n := e.files.count("restore") + e.files.count("restore-from"); n != 0 {
		t.Fatalf("restored: %v", e.files.callLog())
	}
	e.noProdWrite()
}

// TestDevSyncDevDeleted (review focus #2): a dropped job (queued or restoring) and a job whose
// dev was deleted while it waited end with Done(canceled) and restore nothing more.
func TestDevSyncDevDeleted(t *testing.T) {
	e := newDevEnv(t)
	e.snapAt(time.Hour, filesapi.SnapshotScheduled)

	release := e.blockWorker()
	ch := e.queue(nil)
	backups.Drop(e.h, e.dev)
	if r := e.wait(ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("dropped while queued: %+v", r)
	}

	ch = e.queue(nil)
	mustExec(t, e.db, `DELETE FROM servers WHERE id = ?`, e.dev)
	release()
	if r := e.wait(ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("deleted while queued: %+v", r)
	}
	if e.files.count("restore-from") != 0 || e.files.count(fmt.Sprintf("create %d", e.prod)) != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}

	e2 := newDevEnv(t)
	e2.snapAt(time.Hour, filesapi.SnapshotScheduled)
	e2.files.holdPlan("restore")
	ch = e2.queue(nil)
	eventually(t, "restoring", func() bool { return e2.files.count("restore-from") == 1 })
	backups.Drop(e2.h, e2.dev)
	if r := e2.wait(ch); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("dropped while restoring: %+v", r)
	}
	if e2.files.count(fmt.Sprintf("cancel %d", e2.dev)) == 0 || e2.files.count(fmt.Sprintf("cancel %d", e2.prod)) != 0 {
		t.Fatalf("cancel calls = %v", e2.files.callLog())
	}
	for _, l := range e2.log.all() {
		if strings.HasPrefix(l, "apply") {
			t.Fatal("Apply ran for a dropped sync")
		}
	}
}

func TestDevSyncNoRestic(t *testing.T) {
	e := newDevEnv(t)
	e.files.mu.Lock()
	e.files.status = filesapi.BackupsStatus{Reason: "restic is not configured (restic_binary)"}
	e.files.mu.Unlock()
	r := e.wait(e.queue(nil))
	if r.err == nil || errors.Is(r.err, context.Canceled) || !strings.Contains(r.err.Error(), "restic is not available") {
		t.Fatalf("done = %+v", r)
	}
	if len(e.log.all()) != 0 {
		t.Fatalf("calls = %v", e.log.all())
	}
}

func TestDevSyncApplyFails(t *testing.T) {
	e := newDevEnv(t)
	e.snapAt(time.Hour, filesapi.SnapshotScheduled)
	r := e.wait(e.queue(func() error { return errors.New("db is gone: /var/lib/secret/path") }))
	if r.err == nil || errors.Is(r.err, context.Canceled) || strings.Contains(r.err.Error(), "/var/lib") {
		t.Fatalf("done = %+v", r)
	}
	eventually(t, "gate open", func() bool { return !e.h.DataJobRunning(t.Context(), e.dev) })
	if e.files.count("forget") != 0 {
		t.Fatal("forgot snapshots after a failed sync")
	}
	if len(e.notes.Types()) != 0 {
		t.Fatalf("notifications: %v", e.notes.Types())
	}
}

// TestBackupEndpointsRefuseDev: every backup route answers 409 dev_managed on a dev, also for
// an admin, and the schedule ticker never queues a backup of a dev.
func TestBackupEndpointsRefuseDev(t *testing.T) {
	e := newDevEnv(t)
	snap := strings.Repeat("a", 64)
	for _, c := range []struct{ method, suffix string }{
		{http.MethodGet, ""}, {http.MethodPost, ""}, {http.MethodGet, "/job"}, {http.MethodPut, "/schedule"},
		{http.MethodPost, "/" + snap + "/pin"}, {http.MethodPost, "/" + snap + "/restore"},
		{http.MethodDelete, "/" + snap}, {http.MethodGet, "/" + snap + "/download"},
	} {
		rec := e.must(http.StatusConflict, c.method, bPath(e.dev, c.suffix), map[string]any{}, e.sudoAdmin())
		if errCode(t, rec) != httpx.CodeDevManaged {
			t.Fatalf("%s %s: %s", c.method, c.suffix, rec.Body)
		}
	}
	e.must(http.StatusOK, http.MethodGet, bPath(e.prod, ""), nil, e.admin())

	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, mode, interval_hours, next_run_at, updated_at) VALUES (?, 'interval', 6, ?, 0)`,
		e.dev, t0.Add(-time.Minute).UnixMilli())
	backups.Tick(t.Context(), e.h)
	if !backups.WaitIdle(e.h) {
		t.Fatal("queue not idle")
	}
	if _, _, ok := backups.JobOf(e.h, e.dev); ok || e.files.count("create") != 0 {
		t.Fatalf("the ticker queued a backup of the dev: %v", e.files.callLog())
	}
}

// TestDevSyncManualAlwaysFresh: "Sync now" always takes a new dev-sync snapshot of the source;
// the first sync of a new dev reuses a fresh scheduled one like a scheduled sync.
func TestDevSyncManualAlwaysFresh(t *testing.T) {
	e := newDevEnv(t)
	fresh := e.snapAt(10*time.Minute, filesapi.SnapshotScheduled)
	e.snapAt(5*time.Minute, filesapi.SnapshotDevSync)
	r := e.wait(e.queueAs("manual", nil))
	if r.err != nil || r.snap == fresh {
		t.Fatalf("done = %+v", r)
	}
	if got := e.log.all(); len(got) < 2 || got[0] != fmt.Sprintf("backup %d dev-sync", e.prod) ||
		got[1] != fmt.Sprintf("restore-from %d %d %s", e.dev, e.prod, short(r.snap)) {
		t.Fatalf("calls = %v", got)
	}

	e2 := newDevEnv(t)
	fresh = e2.snapAt(10*time.Minute, filesapi.SnapshotScheduled)
	r = e2.wait(e2.queueAs("create", nil))
	if r.err != nil || r.snap != fresh || e2.files.count("create") != 0 {
		t.Fatalf("create: done = %+v, calls = %v", r, e2.files.callLog())
	}
}

// TestDevSyncKeepsPinned: older dev-sync snapshots of the source are forgotten after a sync,
// a pinned one is kept.
func TestDevSyncKeepsPinned(t *testing.T) {
	e := newDevEnv(t)
	e.clk.Set(t0.Add(-8 * time.Hour))
	pinned := e.files.add(e.prod, filesapi.SnapshotDevSync, true, "")
	old := e.files.add(e.prod, filesapi.SnapshotDevSync, false, "")
	e.clk.Set(t0)
	r := e.wait(e.queue(nil))
	if r.err != nil {
		t.Fatal(r.err)
	}
	if got := e.log.all(); got[len(got)-1] != fmt.Sprintf("forget %d %s", e.prod, short(old)) {
		t.Fatalf("calls = %v", got)
	}
	var ids []string
	for _, s := range e.files.list(e.prod) {
		ids = append(ids, s.ID)
	}
	if !slices.Contains(ids, pinned) || slices.Contains(ids, old) || !slices.Contains(ids, r.snap) {
		t.Fatalf("prod snapshots after the sync: %v (pinned %s, old %s, new %s)", ids, short(pinned), short(old), short(r.snap))
	}
}
