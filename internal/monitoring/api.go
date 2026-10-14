package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// sampler is what the read routes need from the collector (*collector implements it).
type sampler interface {
	Latest() Snapshot
	Watch(serverID int64)
	Live(serverID int64) (ServerSample, bool)
}

// firingSource is what the read routes need from the alerter (*alerter implements it).
type firingSource interface {
	Firing(ctx context.Context) []FiringAlert
}

var (
	_ sampler      = (*collector)(nil)
	_ firingSource = (*alerter)(nil)
)

// apiHandlers serve the four read routes; they see the collector and the alerter only through
// interfaces, so the routes are tested without a real collector.
type apiHandlers struct {
	q      *store.Queries
	clock  clock.Clock
	src    sampler
	alerts firingSource
}

// routes registers the monitoring routes (V3, paths relative to /api).
func (h *Handler) routes(api *rbac.Router) {
	(&apiHandlers{q: h.q, clock: h.d.Clock, src: h.coll, alerts: h.alerts}).register(api)
	// fixed since phase A (settings.go): settings and the per-server switch
	api.Get("/monitoring/settings", rbac.Admin(), h.handleGetSettings)
	api.Put("/monitoring/settings", rbac.Admin().WithSudo(), h.handlePutSettings)
	api.Put("/servers/{id}/alerts", rbac.Admin(), h.handlePutServerAlerts)
}

func (a *apiHandlers) register(api *rbac.Router) {
	view := rbac.Server(rbac.PermServerView, "id")
	api.Get("/servers/{id}/metrics", view, a.handleMetrics)
	api.Get("/servers/{id}/metrics/live", view, a.handleLive)
	api.Get("/monitoring/overview", rbac.Authenticated(), a.handleOverview)
	api.Get("/monitoring/host", rbac.Admin(), a.handleHost)
}

// metricsJSON is the OpenAPI schema MetricsSeries.
type metricsJSON struct {
	Range  string             `json:"range"`
	StepMS int64              `json:"step_ms"`
	Series map[string][]point `json:"series"`
}

// liveJSON is the OpenAPI schema LiveMetrics.
type liveJSON struct {
	At               *int64   `json:"at"`
	Up               *bool    `json:"up"`
	Players          *int     `json:"players"`
	PlayersMax       *int     `json:"players_max"`
	PlayerNames      []string `json:"player_names"`
	TPS              *float64 `json:"tps"`
	MSPT             *float64 `json:"mspt"`
	CPUPercent       *float64 `json:"cpu_percent"`
	MemoryBytes      *int64   `json:"memory_bytes"`
	MemoryLimitBytes *int64   `json:"memory_limit_bytes"`
	TPSSource        string   `json:"tps_source"`
}

// loadServerID returns the {id} of an existing server; it writes 404 otherwise (admins pass
// the rbac rule for unknown ids).
func (a *apiHandlers) loadServerID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id > 0 {
		_, err = a.q.RbacGetServer(r.Context(), id)
	} else {
		err = sql.ErrNoRows
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
		return 0, false
	case err != nil:
		httpx.Internal(w, r, err)
		return 0, false
	}
	return id, true
}

func (a *apiHandlers) writeSeries(w http.ResponseWriter, r *http.Request, series int64, allowed []string) {
	rg, ok := parseRange(r.URL.Query().Get("range"))
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "range must be 1h, 24h, 7d or 30d")
		return
	}
	s, err := readSeries(r.Context(), a.q, series, rg, a.clock.Now(), allowed)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, metricsJSON{Range: rg.name, StepMS: rg.step.Milliseconds(), Series: s})
}

func (a *apiHandlers) handleMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := a.loadServerID(w, r)
	if !ok {
		return
	}
	a.writeSeries(w, r, id, serverMetrics)
}

func (a *apiHandlers) handleHost(w http.ResponseWriter, r *http.Request) {
	a.writeSeries(w, r, HostSeries, hostMetrics)
}

// handleLive marks the server as watched (5-s TPS for 30 s, spec §6.2) and returns the freshest
// values; 503 agent_unavailable when the agent did not answer the last pass. A server that is
// not running has no TPS source (V6: "unknown").
func (a *apiHandlers) handleLive(w http.ResponseWriter, r *http.Request) {
	id, ok := a.loadServerID(w, r)
	if !ok {
		return
	}
	a.src.Watch(id)
	snap := a.src.Latest()
	if !snap.At.IsZero() && !snap.Agent {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the agent did not answer the last monitoring pass")
		return
	}
	out := liveJSON{PlayerNames: []string{}, TPSSource: TPSSourceUnknown}
	if !snap.At.IsZero() {
		at := snap.At.UnixMilli()
		out.At = &at
	}
	if s, ok := a.src.Live(id); ok {
		out.Up, out.Players, out.PlayersMax = s.Up, s.Players, s.PlayersMax
		out.TPS, out.MSPT, out.CPUPercent = s.TPS, s.MSPT, s.CPUPercent
		out.MemoryBytes, out.MemoryLimitBytes = s.MemoryBytes, s.MemoryLimit
		if s.PlayerNames != nil {
			out.PlayerNames = s.PlayerNames
		}
		if s.TPSSource != "" && s.State == agentapi.StateRunning {
			out.TPSSource = s.TPSSource
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
