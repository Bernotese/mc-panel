package devinst

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
)

// overrides are the dev overrides of property_overrides (dev instances spec §3.2).
type overrides struct {
	ViewDistance       int `json:"view_distance"`
	SimulationDistance int `json:"simulation_distance"`
}

var defaultOverrides = overrides{ViewDistance: 4, SimulationDistance: 3}

// parseOverrides reads property_overrides; missing or broken values take the defaults.
func parseOverrides(s string) overrides {
	ov := defaultOverrides
	_ = json.Unmarshal([]byte(s), &ov) // a broken value keeps the defaults
	if ov.ViewDistance < minDistance || ov.ViewDistance > maxDistance {
		ov.ViewDistance = defaultOverrides.ViewDistance
	}
	if ov.SimulationDistance < minDistance || ov.SimulationDistance > maxDistance {
		ov.SimulationDistance = defaultOverrides.SimulationDistance
	}
	return ov
}

func (o overrides) encode() string {
	b, _ := json.Marshal(o) // two ints never fail
	return string(b)
}

// Ranges (OpenAPI UpdateDevRequest / CreateDevRequest).
const (
	minDistance, maxDistance = 3, 32
	minIdle, maxIdle         = 5, 120
	maxSyncTime              = 1439
	minMemory, maxMemory     = 1024, 32768
	memoryStep               = 256
)

// devView is the OpenAPI schema DevInstance.
type devView struct {
	ServerID           int64   `json:"server_id"`
	SourceID           int64   `json:"source_id"`
	SourceName         string  `json:"source_name"`
	SyncPaused         bool    `json:"sync_paused"`
	PausedAt           *int64  `json:"paused_at"`
	SyncTime           int64   `json:"sync_time"`
	IdleMinutes        int64   `json:"idle_minutes"`
	ViewDistance       int     `json:"view_distance"`
	SimulationDistance int     `json:"simulation_distance"`
	NextSyncAt         *int64  `json:"next_sync_at"`
	LastSyncAt         *int64  `json:"last_sync_at"`
	LastSyncStatus     *string `json:"last_sync_status"`
	LastSyncError      *string `json:"last_sync_error"`
	SyncPending        bool    `json:"sync_pending"`
	Sleeping           bool    `json:"sleeping"`
}

// viewOf renders d. sync_pending follows the rule of the Server view (servers): a deferred or
// running sync, busy data (the sync is queued or restoring), or a sync this handler is about to
// queue.
func (h *Handler) viewOf(ctx context.Context, d dev) devView {
	r := d.row
	ov := parseOverrides(r.PropertyOverrides)
	h.mu.Lock()
	pending := h.devs[r.ServerID] != nil && h.devs[r.ServerID].pending
	h.mu.Unlock()
	if !pending && !r.DeferredSince.Valid && r.LastSyncStatus.String != statusRunning {
		st, _ := h.data.State(ctx, r.ServerID) // an error comes with StateUnknown
		pending = st == servers.StateBusy
	}
	pending = pending || r.DeferredSince.Valid || r.LastSyncStatus.String == statusRunning
	return devView{
		ServerID: r.ServerID, SourceID: r.SourceID, SourceName: d.sourceName, SyncPaused: r.SyncPaused != 0,
		PausedAt: msPtr(r.PausedAt), SyncTime: r.SyncTime, IdleMinutes: r.IdleMinutes,
		ViewDistance: ov.ViewDistance, SimulationDistance: ov.SimulationDistance,
		NextSyncAt: msPtr(r.NextSyncAt), LastSyncAt: msPtr(r.LastSyncAt), LastSyncStatus: strPtr(r.LastSyncStatus),
		LastSyncError: strPtr(r.LastSyncError), SyncPending: pending, Sleeping: r.Sleeping != 0,
	}
}

// writeView answers the current view of devID with status.
func (h *Handler) writeView(w http.ResponseWriter, r *http.Request, status int, devID int64) {
	d, err := h.getDev(r.Context(), devID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, status, h.viewOf(r.Context(), d))
}

// handleGet answers GET /servers/{id}/dev.
func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDev(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.viewOf(r.Context(), d))
}

type createRequest struct {
	MemoryMiB *int `json:"memory_mib"`
}

// handleCreate answers POST /servers/{id}/dev: create the dev of prod {id} and queue its
// first sync (no player wait: the dev has never run).
func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	prod, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	if m := req.MemoryMiB; m != nil && (*m < minMemory || *m > maxMemory || *m%memoryStep != 0) {
		writeValidation(w, r, "memory_mib must be 1024-32768 and a multiple of 256")
		return
	}
	ctx := r.Context()
	s, err := h.servers.CreateDev(ctx, prod.ID, req.MemoryMiB)
	if err != nil {
		if status, code, msg, ok := classifyCreate(err); ok {
			httpx.WriteError(w, r, status, code, msg)
			return
		}
		h.record(ctx, audit.ActionDevCreate, prod.ID, audit.ResultFailure, map[string]any{"source_id": prod.ID, "source_name": prod.Name})
		httpx.Internal(w, r, err)
		return
	}
	details := map[string]any{"source_id": prod.ID, "source_name": prod.Name, "name": s.Name}
	if req.MemoryMiB != nil {
		details["memory_mib"] = *req.MemoryMiB
	}
	h.record(ctx, audit.ActionDevCreate, s.ID, audit.ResultSuccess, details)
	if err := h.queueSync(context.WithoutCancel(ctx), s.ID, triggerCreate); err != nil {
		// The dev exists; the nightly schedule or "Sync now" fills it later.
		h.log.WarnContext(ctx, "devinst: queue the first sync", "server_id", s.ID, "err", err)
	}
	view, err := h.view(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, view)
}

// classifyCreate maps an error of Servers.CreateDev to its answer; ok is false for an
// internal error. Validation errors keep their text (it names the field), the others get a
// fixed message.
func classifyCreate(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, errServerNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, "server not found", true
	case errors.Is(err, errDevExists):
		return http.StatusConflict, httpx.CodeDevExists, "the server already has a dev instance or is one", true
	case errors.Is(err, instancespec.ErrInvalid):
		return http.StatusBadRequest, httpx.CodeValidation, err.Error(), true
	case errors.Is(err, errNoInstance):
		return http.StatusConflict, httpx.CodeConflict, "the server has no instance yet", true
	case errors.Is(err, errNameTaken):
		return http.StatusConflict, httpx.CodeConflict, "the name of the dev instance is taken by another server", true
	case errors.Is(err, errBaseDomainMissing):
		return http.StatusConflict, httpx.CodeBaseDomainMissing, "no base domain configured: set one in the server settings", true
	}
	return 0, "", "", false
}

// updateRequest is UpdateDevRequest; omitted fields stay unchanged.
type updateRequest struct {
	SyncPaused         *bool `json:"sync_paused"`
	SyncTime           *int  `json:"sync_time"`
	IdleMinutes        *int  `json:"idle_minutes"`
	ViewDistance       *int  `json:"view_distance"`
	SimulationDistance *int  `json:"simulation_distance"`
}

func (u updateRequest) validate() string {
	in := func(v *int, lo, hi int) bool { return v == nil || (*v >= lo && *v <= hi) }
	switch {
	case !in(u.SyncTime, 0, maxSyncTime):
		return "sync_time must be 0-1439"
	case !in(u.IdleMinutes, minIdle, maxIdle):
		return "idle_minutes must be 5-120"
	case !in(u.ViewDistance, minDistance, maxDistance):
		return "view_distance must be 3-32"
	case !in(u.SimulationDistance, minDistance, maxDistance):
		return "simulation_distance must be 3-32"
	}
	return ""
}

// handleUpdate answers PATCH /servers/{id}/dev. Pausing clears next_sync_at (paused devs have
// no next sync), resuming or a new sync time computes it again; a changed override is applied
// to the dev's spec at once.
func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req updateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if problem := req.validate(); problem != "" {
		writeValidation(w, r, problem)
		return
	}
	d, ok := h.loadDev(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	h.writeMu.Lock()
	row, err := h.q.DevGet(ctx, d.row.ServerID) // fresh under the lock
	if err != nil {
		h.writeMu.Unlock()
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now()
	p := store.DevUpdateSettingsParams{
		SyncPaused: row.SyncPaused, PausedAt: row.PausedAt, SyncTime: row.SyncTime, IdleMinutes: row.IdleMinutes,
		PropertyOverrides: row.PropertyOverrides, NextSyncAt: row.NextSyncAt, UpdatedAt: now.UnixMilli(), ServerID: row.ServerID,
	}
	changed := map[string]any{}
	if v := req.SyncPaused; v != nil && *v != (row.SyncPaused != 0) {
		changed["sync_paused"] = *v
		if *v {
			p.SyncPaused, p.PausedAt = 1, nullTime(now)
		} else {
			p.SyncPaused, p.PausedAt = 0, sql.NullInt64{}
		}
	}
	if v := req.SyncTime; v != nil && int64(*v) != row.SyncTime {
		changed["sync_time"] = *v
		p.SyncTime = int64(*v)
	}
	if v := req.IdleMinutes; v != nil && int64(*v) != row.IdleMinutes {
		changed["idle_minutes"] = *v
		p.IdleMinutes = int64(*v)
	}
	ov := parseOverrides(row.PropertyOverrides)
	if v := req.ViewDistance; v != nil && *v != ov.ViewDistance {
		changed["view_distance"] = *v
		ov.ViewDistance = *v
	}
	if v := req.SimulationDistance; v != nil && *v != ov.SimulationDistance {
		changed["simulation_distance"] = *v
		ov.SimulationDistance = *v
	}
	_, ovView := changed["view_distance"]
	_, ovSim := changed["simulation_distance"]
	overridesChanged := ovView || ovSim
	p.PropertyOverrides = ov.encode()
	_, pausedChanged := changed["sync_paused"]
	_, timeChanged := changed["sync_time"]
	if pausedChanged || timeChanged {
		p.NextSyncAt = sql.NullInt64{}
		if p.SyncPaused == 0 {
			p.NextSyncAt = nullTime(h.nextSync(ctx, p.SyncTime, now))
		}
	}
	if len(changed) > 0 {
		err = h.q.DevUpdateSettings(ctx, p)
	}
	if err == nil && p.SyncPaused != 0 && row.SyncPaused == 0 {
		err = h.clearDeferred(ctx, row.ServerID) // a waiting sync ends with the pause
	}
	h.writeMu.Unlock()
	if len(changed) == 0 {
		h.writeView(w, r, http.StatusOK, row.ServerID)
		return
	}
	if err == nil && overridesChanged {
		// Only the overrides: prod's current type, version or plugins must not be put onto
		// the data of the last sync.
		err = h.servers.UpdateDevOverrides(ctx, row.ServerID)
	}
	h.record(ctx, audit.ActionDevSettingsUpdate, row.ServerID, result(err), changed)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.writeView(w, r, http.StatusOK, row.ServerID)
}

// handleSync answers POST /servers/{id}/dev/sync. With players (or an unknown count) on the
// dev it warns them in chat and queues the sync warnDelay later; otherwise it queues at once.
// Each manual sync forces a fresh backup of prod, so they are at least manualCooldown apart
// (429 with Retry-After).
func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDev(w, r)
	if !ok {
		return
	}
	ctx := context.WithoutCancel(r.Context()) // the job outlives the request; actor and IP stay
	id := d.row.ServerID
	ok, wait, prev := h.claimManual(id, h.clock.Now())
	if !ok && wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "the last manual sync of this dev was less than 10 minutes ago")
		return
	}
	if !ok {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeBackupRunning, "a sync of this dev is already queued or running")
		return
	}
	if h.occupied(id) {
		h.say(ctx, id, warnText(d.sourceName))
		if err := h.delay(ctx, d, triggerManual); err != nil {
			h.unclaimManual(id, prev)
			httpx.Internal(w, r, err)
			return
		}
		h.writeView(w, r, http.StatusAccepted, id)
		return
	}
	if err := h.queueClaimed(ctx, d, triggerManual); err != nil {
		h.unclaimManual(id, prev) // a refused sync does not start the cooldown
		if errors.Is(err, errSyncBusy) {
			httpx.WriteError(w, r, http.StatusConflict, httpx.CodeBackupRunning, "a sync of this dev is already queued or running")
			return
		}
		httpx.Internal(w, r, err)
		return
	}
	h.writeView(w, r, http.StatusAccepted, id)
}
