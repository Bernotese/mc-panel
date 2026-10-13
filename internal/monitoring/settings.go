package monitoring

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Limits of the alert settings (Global Constraints).
const (
	minDiskPercent, maxDiskPercent = 1, 50
	minDiskBytes, maxDiskBytes     = 1 << 30, 10 << 40
	minDownAfter, maxDownAfter     = 1, 60
	minCrashRestarts, maxCrashRest = 2, 20
	minCrashWindow, maxCrashWindow = 1, 120
)

// settingsJSON is MonitoringSettings; updateSettingsRequest UpdateMonitoringSettingsRequest.
type settingsJSON struct {
	DiskMinPercent int64             `json:"disk_min_percent"`
	DiskMinBytes   int64             `json:"disk_min_bytes"`
	DownAfterMin   int64             `json:"down_after_min"`
	CrashRestarts  int64             `json:"crash_restarts"`
	CrashWindowMin int64             `json:"crash_window_min"`
	Alerts         []firingAlertJSON `json:"alerts"`
}

type updateSettingsRequest struct {
	DiskMinPercent int64 `json:"disk_min_percent"`
	DiskMinBytes   int64 `json:"disk_min_bytes"`
	DownAfterMin   int64 `json:"down_after_min"`
	CrashRestarts  int64 `json:"crash_restarts"`
	CrashWindowMin int64 `json:"crash_window_min"`
}

// firingAlertJSON is FiringAlert of the API.
type firingAlertJSON struct {
	Kind       string  `json:"kind"`
	ServerID   *int64  `json:"server_id"`
	ServerName *string `json:"server_name"`
	Since      int64   `json:"since"`
	Detail     string  `json:"detail"`
}

func firingJSON(fs []FiringAlert) []firingAlertJSON {
	out := make([]firingAlertJSON, 0, len(fs))
	for _, f := range fs {
		j := firingAlertJSON{Kind: f.Kind, Since: f.Since.UnixMilli(), Detail: f.Detail}
		if f.ServerID != 0 {
			id, name := f.ServerID, f.ServerName
			j.ServerID, j.ServerName = &id, &name
		}
		out = append(out, j)
	}
	return out
}

func (req updateSettingsRequest) validate() error {
	for _, c := range []struct {
		name      string
		v, lo, hi int64
	}{
		{"disk_min_percent", req.DiskMinPercent, minDiskPercent, maxDiskPercent},
		{"disk_min_bytes", req.DiskMinBytes, minDiskBytes, maxDiskBytes},
		{"down_after_min", req.DownAfterMin, minDownAfter, maxDownAfter},
		{"crash_restarts", req.CrashRestarts, minCrashRestarts, maxCrashRest},
		{"crash_window_min", req.CrashWindowMin, minCrashWindow, maxCrashWindow},
	} {
		if c.v < c.lo || c.v > c.hi {
			return fmt.Errorf("%s must be between %d and %d", c.name, c.lo, c.hi)
		}
	}
	return nil
}

func settingsView(s store.MonitoringSetting, firing []FiringAlert) settingsJSON {
	return settingsJSON{
		DiskMinPercent: s.DiskMinPercent, DiskMinBytes: s.DiskMinBytes, DownAfterMin: s.DownAfterMin,
		CrashRestarts: s.CrashRestarts, CrashWindowMin: s.CrashWindowMin, Alerts: firingJSON(firing),
	}
}

// settingsValues are the five thresholds of a row (the audit details from/to, without alerts).
func settingsValues(s store.MonitoringSetting) updateSettingsRequest {
	return updateSettingsRequest{
		DiskMinPercent: s.DiskMinPercent, DiskMinBytes: s.DiskMinBytes, DownAfterMin: s.DownAfterMin,
		CrashRestarts: s.CrashRestarts, CrashWindowMin: s.CrashWindowMin,
	}
}

func (h *Handler) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.q.MonitoringGetSettings(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsView(s, h.alerts.Firing(r.Context())))
}

func (h *Handler) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateSettingsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, err.Error())
		return
	}
	old, err := h.q.MonitoringGetSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.q.MonitoringUpdateSettings(ctx, store.MonitoringUpdateSettingsParams{
		DiskMinPercent: req.DiskMinPercent, DiskMinBytes: req.DiskMinBytes, DownAfterMin: req.DownAfterMin,
		CrashRestarts: req.CrashRestarts, CrashWindowMin: req.CrashWindowMin, UpdatedAt: h.d.Clock.Now().UnixMilli(),
	}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	s, err := h.q.MonitoringGetSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionMonitoringSettingsUpdate, auditTarget, audit.ResultSuccess,
		map[string]any{"from": settingsValues(old), "to": settingsValues(s)})
	httpx.WriteJSON(w, http.StatusOK, settingsView(s, h.alerts.Firing(ctx)))
}

func (h *Handler) handlePutServerAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id > 0 {
		_, err = h.q.RbacGetServer(ctx, id)
	}
	if errors.Is(err, sql.ErrNoRows) || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var req struct {
		DownAlerts *bool `json:"down_alerts"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.DownAlerts == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "down_alerts is required")
		return
	}
	v := int64(0)
	if *req.DownAlerts {
		v = 1
	}
	if err := h.q.MonitoringSetServerAlerts(ctx, store.MonitoringSetServerAlertsParams{ServerID: id, DownAlerts: v}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerAlertsUpdate, serverTarget(id), audit.ResultSuccess, map[string]any{"down_alerts": *req.DownAlerts})
	w.WriteHeader(http.StatusNoContent)
}
