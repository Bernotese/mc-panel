package backups

import (
	"context"
	"database/sql"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

func nullTime(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }

func timeOf(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64)
}

// nullString maps "" to NULL.
func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func ptrInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func ptrString(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// loadSchedule returns the server's row, created with the defaults on first use.
func (h *Handler) loadSchedule(ctx context.Context, serverID int64) (store.BackupSchedule, error) {
	if err := h.q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: serverID, UpdatedAt: h.clock.Now().UnixMilli()}); err != nil {
		return store.BackupSchedule{}, err
	}
	return h.q.BackupsGetSchedule(ctx, serverID)
}

func scheduleOf(r store.BackupSchedule) Schedule {
	return Schedule{
		Mode: Mode(r.Mode), IntervalHours: int(r.IntervalHours), TimeOfDay: int(r.TimeOfDay), Weekday: time.Weekday(r.Weekday),
		KeepLast: int(r.KeepLast), KeepDaily: int(r.KeepDaily), KeepWeekly: int(r.KeepWeekly), KeepMonthly: int(r.KeepMonthly),
	}
}

// location returns the panel time zone of the schedules and its configured name. An invalid
// stored name (the settings route validates, so only a hand-edited DB) falls back to UTC
// instead of stopping all backups.
func (h *Handler) location(ctx context.Context) (*time.Location, string, error) {
	name, err := h.q.BackupsGetTimezone(ctx)
	if err != nil {
		return nil, "", err
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		h.log.WarnContext(ctx, "backups: invalid backup_timezone, using UTC", "timezone", name, "err", err)
		return time.UTC, name, nil
	}
	return loc, name, nil
}

// tzName is the zone sent with every backup and every retention forget: restic stores the
// snapshot time in the backup's zone and forget buckets days/weeks/months by it, so both must
// use the same source. An invalid stored name falls back to UTC (location), an unreadable
// setting too – never "" (the service's own zone), which forget would not accept.
func (h *Handler) tzName(ctx context.Context) string {
	loc, _, err := h.location(ctx)
	if err != nil {
		return time.UTC.String()
	}
	return loc.String()
}

// scheduleView is the OpenAPI schema BackupSchedule.
type scheduleView struct {
	Mode           string  `json:"mode"`
	IntervalHours  int64   `json:"interval_hours"`
	TimeOfDay      int64   `json:"time_of_day"`
	Weekday        int64   `json:"weekday"`
	KeepLast       int64   `json:"keep_last"`
	KeepDaily      int64   `json:"keep_daily"`
	KeepWeekly     int64   `json:"keep_weekly"`
	KeepMonthly    int64   `json:"keep_monthly"`
	Timezone       string  `json:"timezone"`
	NextRunAt      *int64  `json:"next_run_at"`
	LastRunAt      *int64  `json:"last_run_at"`
	LastStatus     *string `json:"last_status"`
	LastError      *string `json:"last_error"`
	LastSnapshotID *string `json:"last_snapshot_id"`
}

func viewSchedule(r store.BackupSchedule, tz string) scheduleView {
	return scheduleView{
		Mode: r.Mode, IntervalHours: r.IntervalHours, TimeOfDay: r.TimeOfDay, Weekday: r.Weekday,
		KeepLast: r.KeepLast, KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly,
		Timezone: tz, NextRunAt: ptrInt(r.NextRunAt), LastRunAt: ptrInt(r.LastRunAt),
		LastStatus: ptrString(r.LastStatus), LastError: ptrString(r.LastError), LastSnapshotID: ptrString(r.LastSnapshotID),
	}
}

type scheduleResponse struct {
	Schedule scheduleView `json:"schedule"`
}
