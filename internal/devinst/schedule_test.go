package devinst_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/devinst"
	"github.com/Bernotese/mc-panel/internal/notify"
)

const deferText = "say [mc-panel] Dev-Sync verschoben: es sind noch Spieler online (nächster Versuch in 15 Minuten)."

func TestScheduledSyncQueuesAtTime(t *testing.T) {
	e := newEnv(t)
	prodID, devID := e.dev("survival")
	e.clk.Set(fiveAM.Add(-time.Minute))
	devinst.Tick(e.ctx(), e.h)
	if len(e.syncer.queued()) != 0 {
		t.Fatal("queued before the sync time")
	}
	e.clk.Set(fiveAM)
	devinst.Tick(e.ctx(), e.h)
	s := e.syncer.last(t)
	if s.Trigger != "scheduled" || s.DevID != devID || s.SourceID != prodID || s.DevName != "dev-survival" || s.SourceName != "survival" {
		t.Fatalf("queued %+v", s)
	}
	devinst.Tick(e.ctx(), e.h)
	if len(e.syncer.queued()) != 1 {
		t.Fatal("queued twice while pending")
	}
	e.clk.Advance(5 * time.Minute)
	s.Done("snap", nil)
	r := e.row(devID)
	if want := fiveAM.Add(24 * time.Hour).UnixMilli(); r.NextSyncAt.Int64 != want || r.DeferredSince.Valid {
		t.Fatalf("next sync %v, deferred %v; want %d", r.NextSyncAt, r.DeferredSince, want)
	}
	if a := e.entries(audit.ActionDevSync); len(a) != 1 || a[0].Details["trigger"] != "scheduled" {
		t.Fatalf("dev.sync %+v", a)
	}
}

func TestScheduledSyncDefersWhilePlayers(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	start := fiveAM.Add(-3 * time.Hour)
	e.clk.Set(fiveAM)
	e.pass(running(devID, start, ptr(1)))
	deferrals := 0
	for range 8 {
		devinst.Tick(e.ctx(), e.h)
		if len(e.syncer.queued()) != 0 {
			t.Fatalf("queued at %v while players are online", e.clk.Now())
		}
		deferrals++
		r := e.row(devID)
		if want := e.clk.Now().Add(15 * time.Minute).UnixMilli(); r.NextSyncAt.Int64 != want || r.DeferredSince.Int64 != fiveAM.UnixMilli() {
			t.Fatalf("deferral %d: next %v deferred %v", deferrals, r.NextSyncAt, r.DeferredSince)
		}
		// Not due in between.
		e.clk.Advance(14 * time.Minute)
		devinst.Tick(e.ctx(), e.h)
		e.clk.Advance(time.Minute)
		e.pass(running(devID, start, ptr(1)))
	}
	got := e.console.sent()
	if len(got) != 8 {
		t.Fatalf("rcon %q, want 8 deferral messages", got)
	}
	for _, c := range got {
		if c != fmt.Sprintf("%d %s", devID, deferText) {
			t.Fatalf("rcon %q", c)
		}
	}
	// 07:00: two hours after the scheduled time it runs anyway, after a final warning.
	if !e.clk.Now().Equal(fiveAM.Add(2 * time.Hour)) {
		t.Fatalf("clock %v", e.clk.Now())
	}
	e.console.reset()
	devinst.Tick(e.ctx(), e.h)
	if got := e.console.sent(); len(got) != 1 || !strings.Contains(got[0], "Dev wird in 60 Sekunden mit survival synchronisiert") {
		t.Fatalf("final warning %q", got)
	}
	devinst.FastTick(e.ctx(), e.h)
	if len(e.syncer.queued()) != 0 {
		t.Fatal("queued without the 60 s wait")
	}
	e.clk.Advance(time.Minute)
	devinst.FastTick(e.ctx(), e.h)
	s := e.syncer.last(t)
	if s.Trigger != "scheduled" {
		t.Fatalf("queued %+v", s)
	}
	s.Done("snap", nil)
	if r := e.row(devID); r.DeferredSince.Valid || r.NextSyncAt.Int64 != fiveAM.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("after the sync: next %v deferred %v", r.NextSyncAt, r.DeferredSince)
	}
}

func TestScheduledSyncPlayersLeave(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	start := fiveAM.Add(-time.Hour)
	e.clk.Set(fiveAM)
	e.pass(running(devID, start, ptr(3)))
	devinst.Tick(e.ctx(), e.h)
	e.clk.Advance(15 * time.Minute)
	e.pass(running(devID, start, ptr(0)))
	devinst.Tick(e.ctx(), e.h)
	if s := e.syncer.last(t); s.Trigger != "scheduled" {
		t.Fatalf("queued %+v", s)
	}
	if got := e.console.sent(); len(got) != 1 {
		t.Fatalf("rcon %q, want only the one deferral", got)
	}
}

func TestPausedSkips(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.must(200, "PATCH", path(devID, ""), map[string]any{"sync_paused": true}, e.admin())
	for _, at := range []time.Time{fiveAM, fiveAM.Add(24 * time.Hour), fiveAM.Add(48 * time.Hour)} {
		e.clk.Set(at)
		devinst.Tick(e.ctx(), e.h)
	}
	if len(e.syncer.queued()) != 0 {
		t.Fatal("a paused dev was synced")
	}
	if r := e.row(devID); r.NextSyncAt.Valid {
		t.Fatalf("next_sync_at %v while paused", r.NextSyncAt)
	}
	// Sync now still works while paused; its result keeps next_sync_at empty.
	e.must(202, "POST", path(devID, "/sync"), nil, e.admin())
	e.syncer.last(t).Done("snap", nil)
	if r := e.row(devID); r.NextSyncAt.Valid {
		t.Fatalf("next_sync_at %v after a sync while paused", r.NextSyncAt)
	}
}

func TestSyncFailedNotifiesOncePer6h(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	fail := func() {
		t.Helper()
		e.must(202, "POST", path(devID, "/sync"), nil, e.admin())
		e.syncer.last(t).Done("", errors.New("restic exploded"))
	}
	fail()
	d := e.get(devID)
	if *d.LastSyncStatus != "failed" || *d.LastSyncError != "restic exploded" || d.LastSyncAt != nil || d.SyncPending {
		t.Fatalf("after failure %+v", d)
	}
	if got := e.notes.Types(); len(got) != 1 || got[0] != notify.EventDevSyncFailed {
		t.Fatalf("events %v", got)
	}
	if a := e.entries(audit.ActionDevSync); len(a) != 1 || a[0].Result != audit.ResultFailure {
		t.Fatalf("dev.sync %+v", a)
	}
	e.clk.Advance(5 * time.Hour)
	fail()
	if n := len(e.notes.Types()); n != 1 {
		t.Fatalf("%d notifications within 6 h", n)
	}
	e.clk.Advance(time.Hour)
	fail()
	if n := len(e.notes.Types()); n != 2 {
		t.Fatalf("%d notifications after 6 h, want 2", n)
	}
	// A success resets the throttle.
	e.must(202, "POST", path(devID, "/sync"), nil, e.admin())
	e.syncer.last(t).Done("snap", nil)
	fail()
	if n := len(e.notes.Types()); n != 3 {
		t.Fatalf("%d notifications after a success and a failure, want 3", n)
	}
	// A failure keeps the last successful sync.
	r := e.row(devID)
	if r.LastSyncSnapshot.String != "snap" || !r.LastSyncAt.Valid {
		t.Fatalf("last success lost: %+v", r)
	}
}

func TestRecoverMarksInterruptedSync(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	mustExec(t, e.db, `UPDATE dev_instances SET last_sync_status = 'running' WHERE server_id = ?`, devID)
	devinst.Recover(e.ctx(), e.h)
	d := e.get(devID)
	if d.LastSyncStatus == nil || *d.LastSyncStatus != "failed" || d.LastSyncError == nil {
		t.Fatalf("after recover %+v", d)
	}
}

func TestTimezoneChangeMovesNextSync(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	devinst.Tick(e.ctx(), e.h) // remembers Europe/Berlin
	mustExec(t, e.db, `UPDATE instance_settings SET backup_timezone = 'UTC' WHERE id = 1`)
	devinst.Tick(e.ctx(), e.h)
	if want := time.Date(2026, 10, 13, 5, 0, 0, 0, time.UTC).UnixMilli(); e.row(devID).NextSyncAt.Int64 != want {
		t.Fatalf("next sync %v, want 05:00 UTC", e.row(devID).NextSyncAt)
	}
}
