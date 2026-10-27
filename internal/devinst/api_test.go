package devinst_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/devinst"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

func TestCreateDevQueuesInitialSync(t *testing.T) {
	e := newEnv(t)
	prodID := e.prod("survival")
	rec := e.must(201, "POST", path(prodID, ""), map[string]any{"memory_mib": 2048}, e.sudoAdmin())
	view := decode[map[string]any](t, rec)
	devID := int64(view["id"].(float64))
	if len(e.servers.created) != 1 || e.servers.created[0] != prodID || *e.servers.memory[0] != 2048 {
		t.Fatalf("CreateDev calls %v %v", e.servers.created, e.servers.memory)
	}
	if a := e.entries(audit.ActionDevCreate); len(a) != 1 || a[0].Result != audit.ResultSuccess {
		t.Fatalf("dev.create entries %+v", a)
	}
	s := e.syncer.last(t)
	if s.DevID != devID || s.SourceID != prodID || s.DevName != "dev-survival" || s.SourceName != "survival" || s.Trigger != "create" {
		t.Fatalf("queued %+v", s)
	}
	d := e.get(devID)
	if !d.SyncPending || d.LastSyncStatus == nil || *d.LastSyncStatus != "running" || d.SourceName != "survival" {
		t.Fatalf("after create %+v", d)
	}

	// Apply is the spec copy of servers; Done records the result.
	if err := s.Apply(e.ctx()); err != nil || e.servers.appliedCount() != 1 {
		t.Fatalf("Apply = %v, applied %d", err, e.servers.appliedCount())
	}
	// The sync stops dev only through the agent: Apply keeps it asleep (desired stopped).
	if c := e.servers.sleepCalls(); len(c) != 1 || c[0].ID != devID || !c[0].Sleeping {
		t.Fatalf("sleep calls after Apply %+v", c)
	}
	e.clk.Advance(3 * time.Minute)
	s.Done("snap1", nil)
	d = e.get(devID)
	if d.SyncPending || *d.LastSyncStatus != "ok" || d.LastSyncAt == nil || *d.LastSyncAt != e.clk.Now().UnixMilli() {
		t.Fatalf("after done %+v", d)
	}
	if r := e.row(devID); r.LastSyncSnapshot.String != "snap1" {
		t.Fatalf("snapshot %v", r.LastSyncSnapshot)
	}
	if a := e.entries(audit.ActionDevSync); len(a) != 1 || a[0].Result != audit.ResultSuccess ||
		a[0].Details["trigger"] != "create" || a[0].Details["snapshot"] != "snap1" {
		t.Fatalf("dev.sync entries %+v", a)
	}
}

func TestCreateDevDefaultMemoryIsNil(t *testing.T) {
	e := newEnv(t)
	prodID := e.prod("survival")
	e.must(201, "POST", path(prodID, ""), map[string]any{}, e.sudoAdmin())
	if e.servers.memory[0] != nil {
		t.Fatalf("memory %v, want nil (servers picks the default)", *e.servers.memory[0])
	}
}

func TestCreateDevErrors(t *testing.T) {
	for _, c := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"exists", devinst.ErrDevExists, 409, httpx.CodeDevExists},
		{"too long", devinst.ErrDevNameTooLong, 400, httpx.CodeValidation},
		{"invalid", devinst.ErrInvalid, 400, httpx.CodeValidation},
		{"no instance", devinst.ErrNoInstance, 409, httpx.CodeConflict},
		{"name taken", devinst.ErrNameTaken, 409, httpx.CodeConflict},
		{"base domain", devinst.ErrBaseDomainMissing, 409, httpx.CodeBaseDomainMissing},
		{"prod gone", devinst.ErrServerNotFound, 404, httpx.CodeNotFound},
		{"internal", errors.New("boom"), 500, httpx.CodeInternal},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			prodID := e.prod("survival")
			e.servers.errs["create"] = c.err
			rec := e.must(c.status, "POST", path(prodID, ""), map[string]any{}, e.sudoAdmin())
			if got := errCode(t, rec); got != c.code {
				t.Fatalf("code %q, want %q", got, c.code)
			}
			if len(e.syncer.queued()) != 0 {
				t.Fatal("a sync was queued")
			}
		})
	}
}

func TestCreateDevValidation(t *testing.T) {
	e := newEnv(t)
	prodID := e.prod("survival")
	for _, body := range []any{
		map[string]any{"memory_mib": 1000},
		map[string]any{"memory_mib": 2000},
		map[string]any{"memory_mib": 33024},
		`{"memory_mib":2048,"x":1}`,
	} {
		rec := e.do("POST", path(prodID, ""), body, ptr(e.sudoAdmin()))
		if rec.Code != 400 {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	if len(e.servers.created) != 0 {
		t.Fatal("CreateDev called for an invalid body")
	}
	if rec := e.do("POST", path(999, ""), map[string]any{}, ptr(e.sudoAdmin())); rec.Code != 404 {
		t.Fatalf("unknown server: %d", rec.Code)
	}
	if rec := e.do("POST", path(prodID, ""), map[string]any{}, ptr(e.admin())); rec.Code != 403 || errCode(t, rec) != httpx.CodeSudoRequired {
		t.Fatalf("without sudo: %d %s", rec.Code, rec.Body)
	}
}

func TestNotDev409(t *testing.T) {
	e := newEnv(t)
	prodID, _ := e.dev("survival")
	for _, c := range []struct{ method, suffix string }{
		{"GET", ""}, {"PATCH", ""}, {"POST", "/sync"}, {"GET", "/ops"}, {"POST", "/ops"}, {"DELETE", "/ops/Steve"},
	} {
		var body any
		switch {
		case c.method == "PATCH":
			body = map[string]any{}
		case c.suffix == "/ops" && c.method == "POST":
			body = map[string]any{"name": "Steve"}
		}
		rec := e.do(c.method, path(prodID, c.suffix), body, ptr(e.admin()))
		if rec.Code != 409 || errCode(t, rec) != httpx.CodeNotDev {
			t.Errorf("%s %s on prod: %d %s", c.method, c.suffix, rec.Code, rec.Body)
		}
		if rec := e.do(c.method, path(999, c.suffix), body, ptr(e.admin())); rec.Code != 404 {
			t.Errorf("%s %s on unknown server: %d", c.method, c.suffix, rec.Code)
		}
	}
}

func TestGetDev(t *testing.T) {
	e := newEnv(t)
	prodID, devID := e.dev("survival")
	d := e.get(devID)
	want := devJSON{
		ServerID: devID, SourceID: prodID, SourceName: "survival", SyncTime: 300, IdleMinutes: 15,
		ViewDistance: 4, SimulationDistance: 3, NextSyncAt: ptr(fiveAM.UnixMilli()), Sleeping: true,
	}
	if d.PausedAt != nil || d.LastSyncAt != nil || d.LastSyncStatus != nil || d.LastSyncError != nil ||
		d.NextSyncAt == nil || *d.NextSyncAt != *want.NextSyncAt {
		t.Fatalf("got %+v", d)
	}
	d.NextSyncAt, want.NextSyncAt = nil, nil
	if d != want {
		t.Fatalf("got %+v, want %+v", d, want)
	}
}

func TestPatchPauseAndOverrides(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	d := decode[devJSON](t, e.must(200, "PATCH", path(devID, ""), map[string]any{"sync_paused": true, "view_distance": 6}, e.admin()))
	if !d.SyncPaused || d.PausedAt == nil || *d.PausedAt != t0.UnixMilli() || d.NextSyncAt != nil || d.ViewDistance != 6 || d.SimulationDistance != 3 {
		t.Fatalf("after pause %+v", d)
	}
	if r := e.row(devID); r.PropertyOverrides != `{"view_distance":6,"simulation_distance":3}` {
		t.Fatalf("overrides %s", r.PropertyOverrides)
	}
	if e.servers.appliedCount() != 1 {
		t.Fatalf("ApplyDevSpec called %d times, want 1", e.servers.appliedCount())
	}
	a := e.entries(audit.ActionDevSettingsUpdate)
	if len(a) != 1 || a[0].Details["sync_paused"] != any(true) || a[0].Details["view_distance"] != 6 {
		t.Fatalf("dev.settings_update %+v", a)
	}

	// Resume: the next slot is computed again; no override changed → no spec copy.
	e.clk.Advance(time.Hour)
	d = decode[devJSON](t, e.must(200, "PATCH", path(devID, ""), map[string]any{"sync_paused": false, "idle_minutes": 30}, e.admin()))
	if d.SyncPaused || d.PausedAt != nil || d.NextSyncAt == nil || *d.NextSyncAt != fiveAM.UnixMilli() || d.IdleMinutes != 30 {
		t.Fatalf("after resume %+v", d)
	}
	if e.servers.appliedCount() != 1 {
		t.Fatalf("ApplyDevSpec called again for a change without overrides")
	}

	// A new sync time moves the next slot (06:30 Berlin = 04:30 UTC).
	d = decode[devJSON](t, e.must(200, "PATCH", path(devID, ""), map[string]any{"sync_time": 390}, e.admin()))
	if want := fiveAM.Add(90 * time.Minute).UnixMilli(); d.NextSyncAt == nil || *d.NextSyncAt != want {
		t.Fatalf("next sync %v, want %d", d.NextSyncAt, want)
	}

	// Pausing twice keeps the first paused_at.
	e.must(200, "PATCH", path(devID, ""), map[string]any{"sync_paused": true}, e.admin())
	first := e.row(devID).PausedAt
	e.clk.Advance(time.Hour)
	e.must(200, "PATCH", path(devID, ""), map[string]any{"sync_paused": true}, e.admin())
	if e.row(devID).PausedAt != first {
		t.Fatal("paused_at changed on a second pause")
	}

	// An empty patch changes nothing and is not audited.
	n := len(e.entries(audit.ActionDevSettingsUpdate))
	e.must(200, "PATCH", path(devID, ""), map[string]any{}, e.admin())
	if len(e.entries(audit.ActionDevSettingsUpdate)) != n {
		t.Fatal("an empty patch was audited")
	}
}

func TestPatchValidation(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	for _, body := range []any{
		map[string]any{"sync_time": -1}, map[string]any{"sync_time": 1440},
		map[string]any{"idle_minutes": 4}, map[string]any{"idle_minutes": 121},
		map[string]any{"view_distance": 2}, map[string]any{"simulation_distance": 33},
		map[string]any{"memory_mib": 4096},
	} {
		if rec := e.do("PATCH", path(devID, ""), body, ptr(e.admin())); rec.Code != 400 {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	if e.servers.appliedCount() != 0 || len(e.rec.Entries) != 0 {
		t.Fatal("an invalid patch had an effect")
	}
}

func TestPatchApplyFails(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.servers.errs["apply"] = errors.New("boom")
	e.must(500, "PATCH", path(devID, ""), map[string]any{"simulation_distance": 5}, e.admin())
	if a := e.entries(audit.ActionDevSettingsUpdate); len(a) != 1 || a[0].Result != audit.ResultFailure {
		t.Fatalf("entries %+v", a)
	}
}

func TestSyncNowWarnsAndWaits60s(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(running(devID, t0.Add(-time.Hour), ptr(2)))
	d := decode[devJSON](t, e.must(202, "POST", path(devID, "/sync"), nil, e.admin()))
	if !d.SyncPending {
		t.Fatalf("sync_pending false: %+v", d)
	}
	want := []string{fmt.Sprintf("%d say [mc-panel] Dev wird in 60 Sekunden mit survival synchronisiert, Änderungen gehen verloren.", devID)}
	if got := e.console.sent(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("rcon %q, want %q", got, want)
	}
	if rec := e.do("POST", path(devID, "/sync"), nil, ptr(e.admin())); rec.Code != 409 || errCode(t, rec) != httpx.CodeBackupRunning {
		t.Fatalf("second sync now: %d %s", rec.Code, rec.Body)
	}
	e.clk.Advance(59 * time.Second)
	devinst.FastTick(e.ctx(), e.h)
	if len(e.syncer.queued()) != 0 {
		t.Fatal("queued before 60 s")
	}
	e.clk.Advance(time.Second)
	devinst.FastTick(e.ctx(), e.h)
	s := e.syncer.last(t)
	if s.Trigger != "manual" || s.DevID != devID {
		t.Fatalf("queued %+v", s)
	}
	devinst.FastTick(e.ctx(), e.h)
	if len(e.syncer.queued()) != 1 {
		t.Fatal("queued twice")
	}
	s.Done("snap", nil)
	a := e.entries(audit.ActionDevSync)
	if len(a) != 1 || a[0].Details["trigger"] != "manual" {
		t.Fatalf("dev.sync %+v", a)
	}
}

func TestSyncNowWithoutPlayersQueuesAtOnce(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(running(devID, t0.Add(-time.Hour), ptr(0)))
	e.must(202, "POST", path(devID, "/sync"), nil, e.admin())
	if s := e.syncer.last(t); s.Trigger != "manual" {
		t.Fatalf("queued %+v", s)
	}
	if len(e.console.sent()) != 0 {
		t.Fatalf("rcon %q", e.console.sent())
	}
}

func TestSyncNowQueueBusy(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	mustExec(t, e.db, `UPDATE dev_instances SET last_sync_status = 'failed', last_sync_error = 'old' WHERE server_id = ?`, devID)
	e.syncer.err = devinst.ErrSyncBusy
	rec := e.must(409, "POST", path(devID, "/sync"), nil, e.admin())
	if errCode(t, rec) != httpx.CodeBackupRunning {
		t.Fatalf("code %s", rec.Body)
	}
	d := e.get(devID)
	if d.SyncPending || d.LastSyncStatus == nil || *d.LastSyncStatus != "failed" || *d.LastSyncError != "old" {
		t.Fatalf("status not restored: %+v", d)
	}
}

func TestDevDeletedDuringSync(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.must(202, "POST", path(devID, "/sync"), nil, e.admin())
	s := e.syncer.last(t)
	mustExec(t, e.db, `DELETE FROM servers WHERE id = ?`, devID)
	s.Done("", errors.New("server deleted"))
	if len(e.notes.Events) != 0 {
		t.Fatalf("notified for a deleted dev: %+v", e.notes.Events)
	}
	if _, err := e.q.DevGet(e.ctx(), devID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("row back: %v", err)
	}
}

func TestApplyFailsWhenSleepFails(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.must(202, "POST", path(devID, "/sync"), nil, e.admin())
	e.servers.errs["sleep"] = errors.New("db locked")
	if err := e.syncer.last(t).Apply(e.ctx()); err == nil {
		t.Fatal("Apply ignored a failed SetDevSleeping")
	}
}

func TestSyncCanceledIsNoFailure(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	mustExec(t, e.db, `UPDATE dev_instances SET last_sync_status = 'ok', last_sync_at = 5, last_sync_snapshot = 'old' WHERE server_id = ?`, devID)
	e.clk.Set(fiveAM)
	devinst.Tick(e.ctx(), e.h)
	e.syncer.last(t).Done("", fmt.Errorf("job dropped: %w", context.Canceled))
	r := e.row(devID)
	if r.LastSyncStatus.String != "ok" || r.LastSyncError.Valid || r.LastSyncSnapshot.String != "old" || r.LastSyncAt.Int64 != 5 {
		t.Fatalf("status after a canceled sync %+v", r)
	}
	if r.NextSyncAt.Int64 != fiveAM.UnixMilli() {
		t.Fatalf("next sync moved to %v: a canceled scheduled sync must stay due", r.NextSyncAt)
	}
	if len(e.notes.Events) != 0 {
		t.Fatalf("notified %+v", e.notes.Events)
	}
	if d := e.get(devID); d.SyncPending {
		t.Fatal("still pending")
	}
}
