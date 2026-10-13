package monitoring

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

func (h *Handler) routes(api *rbac.Router) {
	view := rbac.Server(rbac.PermServerView, "id")
	api.Get("/servers/{id}/metrics", view, h.handleMetrics)
	api.Get("/servers/{id}/metrics/live", view, h.handleLive)
	api.Put("/servers/{id}/alerts", rbac.Admin(), h.handlePutServerAlerts)
	api.Get("/monitoring/overview", rbac.Authenticated(), h.handleOverview)
	api.Get("/monitoring/host", rbac.Admin(), h.handleHost)
	api.Get("/monitoring/settings", rbac.Admin(), h.handleGetSettings)
	api.Put("/monitoring/settings", rbac.Admin().WithSudo(), h.handlePutSettings)
}

// seriesJSON is the OpenAPI schema MetricsSeries.
type seriesJSON struct {
	Range  string                  `json:"range"`
	StepMS int64                   `json:"step_ms"`
	Series map[string][][4]float64 `json:"series"`
}

// liveJSON is the OpenAPI schema LiveMetrics.
type liveJSON struct {
	At          *int64   `json:"at"`
	Up          *bool    `json:"up"`
	Players     *int     `json:"players"`
	PlayersMax  *int     `json:"players_max"`
	PlayerNames []string `json:"player_names"`
	TPS         *float64 `json:"tps"`
	MSPT        *float64 `json:"mspt"`
	CPUPercent  *float64 `json:"cpu_percent"`
	MemoryBytes *int64   `json:"memory_bytes"`
	MemoryLimit *int64   `json:"memory_limit_bytes"`
	TPSSource   string   `json:"tps_source"`
}

// overviewServerJSON is OverviewServer, hostJSON HostSnapshot, overviewJSON MonitoringOverview.
type overviewServerJSON struct {
	ID          int64        `json:"id"`
	Up          *bool        `json:"up"`
	Players     *int         `json:"players"`
	PlayersMax  *int         `json:"players_max"`
	TPS         *float64     `json:"tps"`
	CPUPercent  *float64     `json:"cpu_percent"`
	MemoryBytes *int64       `json:"memory_bytes"`
	MemoryLimit *int64       `json:"memory_limit_bytes"`
	Sparkline   [][2]float64 `json:"sparkline"`
	Alerts      []string     `json:"alerts"`
}

type hostJSON struct {
	At          *int64   `json:"at"`
	CPUPercent  *float64 `json:"cpu_percent"`
	Load1       *float64 `json:"load1"`
	MemoryUsed  *int64   `json:"memory_used"`
	MemoryTotal *int64   `json:"memory_total"`
	DiskFree    *int64   `json:"disk_free"`
	DiskTotal   *int64   `json:"disk_total"`
	Alerts      []string `json:"alerts"`
}

type overviewJSON struct {
	Servers []overviewServerJSON `json:"servers"`
	Host    *hostJSON            `json:"host"`
}

// stepFor is the resolution of a range (V3); 0 for an unknown range.
func stepFor(r string) time.Duration {
	switch r {
	case "1h":
		return collectEvery
	case "24h":
		return 5 * time.Minute
	case "7d", "30d":
		return time.Hour
	}
	return 0
}

// parseRange reads ?range= and answers 400 for unknown values.
func parseRange(w http.ResponseWriter, r *http.Request) (string, bool) {
	rg := r.URL.Query().Get("range")
	if stepFor(rg) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "range must be 1h, 24h, 7d or 30d")
		return "", false
	}
	return rg, true
}

// serverExists answers 404 for an unknown {id}.
func (h *Handler) serverExists(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id > 0 {
		_, err = h.q.RbacGetServer(r.Context(), id)
	}
	switch {
	case err == nil:
		return id, true
	case errors.Is(err, sql.ErrNoRows) || id <= 0:
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
	default:
		httpx.Internal(w, r, err)
	}
	return 0, false
}

// --- Phase A stubs (B2 replaces them) ----------------------------------------------------

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.serverExists(w, r); !ok {
		return
	}
	rg, ok := parseRange(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, seriesJSON{Range: rg, StepMS: stepFor(rg).Milliseconds(), Series: map[string][][4]float64{}})
}

func (h *Handler) handleLive(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.serverExists(w, r); !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, liveJSON{PlayerNames: []string{}, TPSSource: TPSSourceUnknown})
}

func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	out := overviewJSON{Servers: []overviewServerJSON{}}
	if p, _ := principal.From(r.Context()); p.IsAdmin() {
		out.Host = &hostJSON{Alerts: []string{}}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleHost(w http.ResponseWriter, r *http.Request) {
	rg, ok := parseRange(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, seriesJSON{Range: rg, StepMS: stepFor(rg).Milliseconds(), Series: map[string][][4]float64{}})
}
