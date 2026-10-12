package store_test

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func i64(v int64) sql.NullInt64   { return sql.NullInt64{Int64: v, Valid: true} }
func str(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }

func dueIDs(t *testing.T, q *store.Queries, now int64) []int64 {
	t.Helper()
	rows, err := q.BackupsListDue(t.Context(), i64(now))
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, r := range rows {
		ids = append(ids, r.ServerID)
	}
	return ids
}

func TestBackupScheduleDefaultsAndRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	a := storetest.CreateServer(t, db, "survival")
	b := storetest.CreateServer(t, db, "creative")

	if err := q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: a, UpdatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	// a second ensure keeps the row as it is
	if err := q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: a, UpdatedAt: 9}); err != nil {
		t.Fatal(err)
	}
	got, err := q.BackupsGetSchedule(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	want := store.BackupSchedule{
		ServerID: a, Mode: "off", IntervalHours: 24, TimeOfDay: 240, Weekday: 0,
		KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6, UpdatedAt: 5,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}

	upd := store.BackupsUpdateScheduleParams{
		Mode: "daily", IntervalHours: 6, TimeOfDay: 180, Weekday: 3, KeepLast: 5, KeepDaily: 0,
		KeepWeekly: 2, KeepMonthly: 1, NextRunAt: i64(1000), UpdatedAt: 20, ServerID: a,
	}
	if err := q.BackupsUpdateSchedule(ctx, upd); err != nil {
		t.Fatal(err)
	}
	if err := q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: b, UpdatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	// b is off: never due, even with a next run in the past
	if err := q.BackupsSetNextRun(ctx, store.BackupsSetNextRunParams{NextRunAt: i64(1), ServerID: b}); err != nil {
		t.Fatal(err)
	}
	if ids := dueIDs(t, q, 999); len(ids) != 0 {
		t.Fatalf("due before next_run_at: %v", ids)
	}
	if ids := dueIDs(t, q, 1000); !slices.Equal(ids, []int64{a}) {
		t.Fatalf("due at next_run_at: %v, want [%d]", ids, a)
	}
	enabled, err := q.BackupsListEnabled(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].ServerID != a {
		t.Fatalf("enabled = %+v, %v", enabled, err)
	}

	// a scheduled run: start, finish
	if err := q.BackupsStartRun(ctx, store.BackupsStartRunParams{LastRunAt: i64(1000), ServerID: a}); err != nil {
		t.Fatal(err)
	}
	got, _ = q.BackupsGetSchedule(ctx, a)
	if got.LastStatus != str("running") || got.LastRunAt != i64(1000) || got.LastError.Valid {
		t.Fatalf("after start: %+v", got)
	}
	if err := q.BackupsFinishRun(ctx, store.BackupsFinishRunParams{
		LastStatus: str("ok"), LastError: sql.NullString{}, LastSnapshotID: str("abc"), NextRunAt: i64(2000), ServerID: a,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = q.BackupsGetSchedule(ctx, a)
	if got.LastStatus != str("ok") || got.LastSnapshotID != str("abc") || got.NextRunAt != i64(2000) {
		t.Fatalf("after finish: %+v", got)
	}

	// a panel crash: running rows become failed
	if err := q.BackupsStartRun(ctx, store.BackupsStartRunParams{LastRunAt: i64(2000), ServerID: a}); err != nil {
		t.Fatal(err)
	}
	if err := q.BackupsFailRunning(ctx, str("panel restarted")); err != nil {
		t.Fatal(err)
	}
	got, _ = q.BackupsGetSchedule(ctx, a)
	if got.LastStatus != str("failed") || got.LastError != str("panel restarted") {
		t.Fatalf("after FailRunning: %+v", got)
	}

	// save-off markers
	if err := q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: i64(3000), ServerID: b}); err != nil {
		t.Fatal(err)
	}
	marks, err := q.BackupsListSaveOff(ctx)
	if err != nil || len(marks) != 1 || marks[0].ServerID != b || marks[0].SaveoffSince != i64(3000) {
		t.Fatalf("save-off marks = %+v, %v", marks, err)
	}
	if err := q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: sql.NullInt64{}, ServerID: b}); err != nil {
		t.Fatal(err)
	}
	if marks, _ := q.BackupsListSaveOff(ctx); len(marks) != 0 {
		t.Fatalf("cleared marks = %+v", marks)
	}
}

func TestBackupTimezoneSetting(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	if tz, err := q.BackupsGetTimezone(ctx); err != nil || tz != "Europe/Berlin" {
		t.Fatalf("default time zone = %q, %v", tz, err)
	}
	if err := q.ServersSetBackupTimezone(ctx, store.ServersSetBackupTimezoneParams{BackupTimezone: "UTC", UpdatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	s, err := q.ServersGetSettings(ctx)
	if err != nil || s.BackupTimezone != "UTC" || s.UpdatedAt != 7 {
		t.Fatalf("settings = %+v, %v", s, err)
	}
}

func TestBackupScheduleChecksAndCascade(t *testing.T) {
	db := storetest.Open(t)
	ctx := t.Context()
	id := storetest.CreateServer(t, db, "survival")
	for _, col := range []string{"mode = 'hourly'", "interval_hours = 0", "interval_hours = 169", "time_of_day = 1440",
		"weekday = 7", "last_status = 'done'"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO backup_schedules (server_id, updated_at) VALUES (?, 0)
			ON CONFLICT (server_id) DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE backup_schedules SET `+col+` WHERE server_id = ?`, id); err == nil {
			t.Errorf("%s accepted", col)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM backup_schedules`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after server delete = %d, %v", n, err)
	}
}
