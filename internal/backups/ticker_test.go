package backups_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/notify"
)

func (e *env) tick() {
	e.t.Helper()
	backups.Tick(e.t.Context(), e.h)
	if !backups.WaitIdle(e.h) {
		e.t.Fatal("jobs did not finish")
	}
}

func TestTickRunsDueSchedule(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	e.tick()
	if n := e.files.count("create"); n != 0 {
		t.Fatalf("ran before it was due: %v", e.files.callLog())
	}
	due := time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC)
	e.clk.Set(due)
	e.tick()
	calls := e.files.callLog()
	if !slices.Equal(calls, []string{fmt.Sprintf("create %d scheduled", id), fmt.Sprintf("forget %d policy 24/7/4/6 Europe/Berlin", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	row := e.schedule(id)
	snaps := e.files.list(id)
	if row.LastStatus.String != "ok" || row.LastError.Valid || row.LastRunAt.Int64 != due.UnixMilli() ||
		len(snaps) != 1 || row.LastSnapshotID.String != snaps[0].ID || row.NextRunAt.Int64 != due.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("row = %+v, snaps %+v", row, snaps)
	}
	if j := e.job(id); j.Trigger != "scheduled" || j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	if es, _ := e.rec.all(); len(es) != 1 { // only the schedule update; scheduled runs are no user action
		t.Fatalf("audit = %v", e.rec.actions())
	}
	if tz := e.files.createTZ[id]; tz != "Europe/Berlin" {
		t.Fatalf("backup timezone = %q", tz)
	}
}

// Backup and retention use the same zone, also when the stored one is invalid (both UTC):
// forget buckets by the zone the snapshot times were stored in.
func TestBackupAndRetentionShareZone(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	if _, err := e.db.ExecContext(t.Context(), `UPDATE instance_settings SET backup_timezone = 'Mars/Olympus' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	// The zone change recomputes next_run_at in UTC (04:00Z) unless the first tick of Run only
	// saw the new zone; either way the run is due at 04:00Z.
	e.tick()
	e.clk.Set(time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC))
	e.tick()
	calls := e.files.callLog()
	if tz := e.files.createTZ[id]; tz != "UTC" || len(calls) != 2 || !strings.HasSuffix(calls[1], " UTC") {
		t.Fatalf("backup timezone %q, calls %v", tz, calls)
	}
}

func TestRetentionOnlyAfterScheduledSuccess(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.create(id, map[string]any{})
	e.waitJob(id)
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.fail["scheduled"] = "boom"
	e.clk.Advance(time.Hour)
	e.tick()
	if n := e.files.count("forget"); n != 0 {
		t.Fatalf("forget after a manual backup or a failed run: %v", e.files.callLog())
	}
}

func TestRetentionFailureKeepsRunOK(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.fail["forget"] = "repository is locked"
	e.clk.Advance(time.Hour)
	e.tick()
	row := e.schedule(id)
	if row.LastStatus.String != "ok" || row.LastError.String != "retention: repository is locked" {
		t.Fatalf("row = %+v", row)
	}
	if len(e.notes.Types()) != 0 {
		t.Fatalf("a retention failure alone notified: %v", e.notes.Types())
	}
}

func TestTickSkipsBusyServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	// the run is due before the manual job starts: a clock jump during its backup would trip
	// the stall watchdog and end it early
	e.clk.Advance(time.Hour)
	release := e.files.holdPlan("manual")
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	backups.Tick(t.Context(), e.h)
	if j := e.job(id); j.Trigger != "manual" {
		t.Fatalf("the scheduled run replaced the manual job: %+v", j)
	}
	release()
	e.waitJob(id)
	e.tick()
	if n := e.files.count(fmt.Sprintf("create %d scheduled", id)); n != 1 {
		t.Fatalf("calls = %v, want the scheduled run on the next tick", e.files.callLog())
	}
}

func TestMissedRunRunsOnce(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	mustExec(t, e.db, `UPDATE backup_schedules SET next_run_at = ? WHERE server_id = ?`, t0.Add(-72*time.Hour).UnixMilli(), id)
	e.tick()
	e.tick()
	if n := e.files.count("create"); n != 1 {
		t.Fatalf("calls = %v, want exactly one catch-up run", e.files.callLog())
	}
	if want := time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC).UnixMilli(); e.schedule(id).NextRunAt.Int64 != want {
		t.Fatalf("next = %d, want %d", e.schedule(id).NextRunAt.Int64, want)
	}
}

func TestTimezoneChangeRecomputes(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	e.tick() // remembers Europe/Berlin
	mustExec(t, e.db, `UPDATE instance_settings SET backup_timezone = 'America/New_York' WHERE id = 1`)
	e.tick()
	// t0 is 06:00 EDT: 04:00 EDT has passed, next is tomorrow 04:00 EDT = 08:00Z.
	if want := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC).UnixMilli(); e.schedule(id).NextRunAt.Int64 != want {
		t.Fatalf("next = %d, want %d", e.schedule(id).NextRunAt.Int64, want)
	}
	if s := putSchedule(e, id, scheduleBody("daily", 24, 240, 0)); s.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q", s.Timezone)
	}
}

func TestScheduledFailureNotifiesThrottled(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.fail["scheduled"] = "restic: repository damaged"
	run := func() {
		e.clk.Advance(time.Hour)
		e.tick()
	}
	run() // T+1h: notifies
	run() // T+2h: within 6 h, silent
	if got := e.notes.Types(); len(got) != 1 || got[0] != notify.EventBackupFailed {
		t.Fatalf("events = %v", got)
	}
	ev := e.notes.Events[0]
	if !strings.Contains(ev.Title, "survival") || ev.Message != "restic: repository damaged" {
		t.Fatalf("event = %+v", ev)
	}
	e.clk.Advance(4 * time.Hour) // T+6h since the first alert
	run()
	if n := len(e.notes.Types()); n != 2 {
		t.Fatalf("events after 6 h = %d", n)
	}
	delete(e.files.fail, "scheduled")
	run() // success resets the throttle
	e.files.fail["scheduled"] = "boom"
	run()
	if n := len(e.notes.Types()); n != 3 {
		t.Fatalf("a failure after a success must notify at once, events = %d", n)
	}
	if row := e.schedule(id); row.LastStatus.String != "failed" || row.LastError.String != "boom" {
		t.Fatalf("row = %+v", row)
	}
}

func TestNoNotificationWithoutDataDir(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.errs["create"] = filesapi.ErrNotFound // never started: no data directory
	e.clk.Advance(time.Hour)
	e.tick()
	if row := e.schedule(id); row.LastStatus.String != "failed" {
		t.Fatalf("row = %+v", row)
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("events = %v", e.notes.Types())
	}
}

// A scheduled run that waits while its server is deleted ends as canceled: no service call,
// no notification.
func TestQueuedScheduledRunOfDeletedServer(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	putSchedule(e, b, scheduleBody("interval", 1, 240, 0))
	e.clk.Advance(time.Hour) // before alpha starts: see TestTickSkipsBusyServer
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	backups.Tick(t.Context(), e.h) // queues beta's run behind alpha
	e.deleteServer(b)
	release()
	if !backups.WaitIdle(e.h) {
		t.Fatal("jobs did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, b); state != "failed" || c != "canceled" {
		t.Fatalf("beta job = %s %s, want failed canceled", state, c)
	}
	if n := e.files.count(fmt.Sprintf("create %d", b)) + e.files.count("cancel"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("events = %v", e.notes.Types())
	}
}

// The backup of a scheduled run may finish for a deleted server, the retention after it is
// a new step and does not run.
func TestScheduledRunDeletedBeforeRetention(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	release := e.files.holdPlan("scheduled")
	e.clk.Advance(time.Hour)
	backups.Tick(t.Context(), e.h)
	e.waitStep(id, "backing_up")
	e.deleteServer(id)
	release()
	if !backups.WaitIdle(e.h) {
		t.Fatal("job did not finish")
	}
	if state, c, _ := backups.JobOf(e.h, id); state != "failed" || c != "canceled" {
		t.Fatalf("job = %s %s, want failed canceled", state, c)
	}
	if n := e.files.count("forget"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("events = %v", e.notes.Types())
	}
}
