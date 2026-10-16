package backups_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
)

type scheduleJSON struct {
	Mode           string  `json:"mode"`
	IntervalHours  int     `json:"interval_hours"`
	TimeOfDay      int     `json:"time_of_day"`
	Weekday        int     `json:"weekday"`
	KeepLast       int     `json:"keep_last"`
	KeepDaily      int     `json:"keep_daily"`
	KeepWeekly     int     `json:"keep_weekly"`
	KeepMonthly    int     `json:"keep_monthly"`
	Timezone       string  `json:"timezone"`
	NextRunAt      *int64  `json:"next_run_at"`
	LastRunAt      *int64  `json:"last_run_at"`
	LastStatus     *string `json:"last_status"`
	LastError      *string `json:"last_error"`
	LastSnapshotID *string `json:"last_snapshot_id"`
}

func scheduleBody(mode string, interval, timeOfDay, weekday int) map[string]any {
	return map[string]any{
		"mode": mode, "interval_hours": interval, "time_of_day": timeOfDay, "weekday": weekday,
		"keep_last": 24, "keep_daily": 7, "keep_weekly": 4, "keep_monthly": 6,
	}
}

func putSchedule(e *env, id int64, body any) scheduleJSON {
	e.t.Helper()
	rec := e.must(http.StatusOK, http.MethodPut, bPath(id, "/schedule"), body, e.sudoAdmin())
	return decode[struct {
		Schedule scheduleJSON `json:"schedule"`
	}](e.t, rec).Schedule
}

func TestUpdateScheduleDaily(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	s := putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	want := time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC).UnixMilli() // 04:00 CEST tomorrow
	if s.Mode != "daily" || s.TimeOfDay != 240 || s.Timezone != "Europe/Berlin" || s.NextRunAt == nil || *s.NextRunAt != want {
		t.Fatalf("schedule = %+v, next want %d", s, want)
	}
	if s.LastRunAt != nil || s.LastStatus != nil || s.LastError != nil || s.LastSnapshotID != nil {
		t.Fatalf("last_* must be null before any run: %+v", s)
	}
	if row := e.schedule(id); !row.NextRunAt.Valid || row.NextRunAt.Int64 != want || row.Mode != "daily" {
		t.Fatalf("row = %+v", row)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionBackupScheduleUpdate || entries[0].Result != audit.ResultSuccess || actors[0] != "root" {
		t.Fatalf("audit = %+v %v", entries, actors)
	}
	to, _ := entries[0].Details["to"].(map[string]any)
	if to["mode"] != "daily" || to["time_of_day"] != 240 {
		t.Fatalf("audit details = %+v", entries[0].Details)
	}
}

func TestUpdateScheduleOffClearsNextRun(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	if s := putSchedule(e, id, scheduleBody("off", 24, 240, 0)); s.NextRunAt != nil {
		t.Fatalf("off keeps next_run_at %v", *s.NextRunAt)
	}
}

func TestUpdateScheduleIntervalAnchorsOnLastRun(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, last_run_at, updated_at) VALUES (?, ?, 0)`, id, t0.Add(-2*time.Hour).UnixMilli())
	s := putSchedule(e, id, scheduleBody("interval", 6, 240, 0))
	if want := t0.Add(4 * time.Hour).UnixMilli(); s.NextRunAt == nil || *s.NextRunAt != want {
		t.Fatalf("next = %v, want %d", s.NextRunAt, want)
	}
}

func TestUpdateScheduleValidation(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	missing := scheduleBody("daily", 24, 240, 0)
	delete(missing, "keep_monthly")
	noKeep := scheduleBody("daily", 24, 240, 0)
	noKeep["keep_last"], noKeep["keep_daily"], noKeep["keep_weekly"], noKeep["keep_monthly"] = 0, 0, 0, 0
	unknown := scheduleBody("daily", 24, 240, 0)
	unknown["cron"] = "* * * * *"
	cases := []struct {
		name string
		body any
		code string
	}{
		{"missing field", missing, "validation_failed"},
		{"bad mode", scheduleBody("hourly", 24, 240, 0), "validation_failed"},
		{"interval 0", scheduleBody("interval", 0, 240, 0), "validation_failed"},
		{"time 1440", scheduleBody("daily", 24, 1440, 0), "validation_failed"},
		{"weekday 7", scheduleBody("weekly", 24, 240, 7), "validation_failed"},
		{"no retention", noKeep, "validation_failed"},
		{"unknown field", unknown, "bad_request"},
		{"not json", "{", "bad_request"},
	}
	for _, c := range cases {
		a := e.sudoAdmin()
		rec := e.do(http.MethodPut, bPath(id, "/schedule"), c.body, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != c.code {
			t.Errorf("%s: %d %s, want 400 %s", c.name, rec.Code, rec.Body, c.code)
		}
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("rejected requests were audited: %v", e.rec.actions())
	}
}

func TestUpdateSchedulePermissions(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	viewer := userP(storeUser(t, e, "viewer"), "viewer")
	grant(t, e.db, viewer.UserID, id, "server.view")
	if rec := e.do(http.MethodPut, bPath(id, "/schedule"), scheduleBody("daily", 24, 240, 0), &viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("server.view only = %d", rec.Code)
	}
	op := userP(storeUser(t, e, "op"), "op")
	grant(t, e.db, op.UserID, id, "server.view", "server.backups")
	if rec := e.do(http.MethodPut, bPath(id, "/schedule"), scheduleBody("daily", 24, 240, 0), &op); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("server.backups without sudo = %d %s", rec.Code, rec.Body)
	}
	op.SudoUntil = t0.Add(time.Hour)
	e.must(http.StatusOK, http.MethodPut, bPath(id, "/schedule"), scheduleBody("daily", 24, 240, 0), op)
	a := e.sudoAdmin()
	if rec := e.do(http.MethodPut, bPath(9999, "/schedule"), scheduleBody("daily", 24, 240, 0), &a); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server = %d", rec.Code)
	}
}
