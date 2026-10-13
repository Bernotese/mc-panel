package backups

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// updateScheduleRequest is the OpenAPI schema UpdateBackupScheduleRequest; all fields are
// required (pointers detect a missing one).
type updateScheduleRequest struct {
	Mode          *string `json:"mode"`
	IntervalHours *int    `json:"interval_hours"`
	TimeOfDay     *int    `json:"time_of_day"`
	Weekday       *int    `json:"weekday"`
	KeepLast      *int    `json:"keep_last"`
	KeepDaily     *int    `json:"keep_daily"`
	KeepWeekly    *int    `json:"keep_weekly"`
	KeepMonthly   *int    `json:"keep_monthly"`
}

func (req updateScheduleRequest) schedule() (Schedule, error) {
	ints := []struct {
		name string
		v    *int
	}{
		{"interval_hours", req.IntervalHours}, {"time_of_day", req.TimeOfDay}, {"weekday", req.Weekday},
		{"keep_last", req.KeepLast}, {"keep_daily", req.KeepDaily}, {"keep_weekly", req.KeepWeekly}, {"keep_monthly", req.KeepMonthly},
	}
	if req.Mode == nil {
		return Schedule{}, fmt.Errorf("%w: mode is required", errInvalidSchedule)
	}
	for _, f := range ints {
		if f.v == nil {
			return Schedule{}, fmt.Errorf("%w: %s is required", errInvalidSchedule, f.name)
		}
	}
	return Schedule{
		Mode: Mode(*req.Mode), IntervalHours: *req.IntervalHours, TimeOfDay: *req.TimeOfDay, Weekday: time.Weekday(*req.Weekday),
		KeepLast: *req.KeepLast, KeepDaily: *req.KeepDaily, KeepWeekly: *req.KeepWeekly, KeepMonthly: *req.KeepMonthly,
	}, nil
}

func scheduleDetails(s Schedule) map[string]any {
	return map[string]any{
		"mode": string(s.Mode), "interval_hours": s.IntervalHours, "time_of_day": s.TimeOfDay, "weekday": int(s.Weekday),
		"keep_last": s.KeepLast, "keep_daily": s.KeepDaily, "keep_weekly": s.KeepWeekly, "keep_monthly": s.KeepMonthly,
	}
}

func (h *Handler) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req updateScheduleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, err := req.schedule()
	if err == nil {
		err = s.Validate()
	}
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	before, err := h.loadSchedule(ctx, srv.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	loc, tz, err := h.location(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now()
	var next sql.NullInt64
	if t, ok := NextRun(s, loc, now, timeOf(before.LastRunAt)); ok {
		next = nullTime(t)
	}
	if err := h.q.BackupsUpdateSchedule(ctx, store.BackupsUpdateScheduleParams{
		Mode: string(s.Mode), IntervalHours: int64(s.IntervalHours), TimeOfDay: int64(s.TimeOfDay), Weekday: int64(s.Weekday),
		KeepLast: int64(s.KeepLast), KeepDaily: int64(s.KeepDaily), KeepWeekly: int64(s.KeepWeekly), KeepMonthly: int64(s.KeepMonthly),
		NextRunAt: next, UpdatedAt: now.UnixMilli(), ServerID: srv.ID,
	}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionBackupScheduleUpdate, srv.ID, audit.ResultSuccess,
		map[string]any{"from": scheduleDetails(scheduleOf(before)), "to": scheduleDetails(s)})
	after, err := h.q.BackupsGetSchedule(ctx, srv.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scheduleResponse{Schedule: viewSchedule(after, tz)})
}
