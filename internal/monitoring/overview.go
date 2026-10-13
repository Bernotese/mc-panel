package monitoring

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// overviewServerJSON is OverviewServer, hostJSON HostSnapshot, overviewJSON MonitoringOverview.
type overviewServerJSON struct {
	ID               int64        `json:"id"`
	Up               *bool        `json:"up"`
	Players          *int         `json:"players"`
	PlayersMax       *int         `json:"players_max"`
	TPS              *float64     `json:"tps"`
	CPUPercent       *float64     `json:"cpu_percent"`
	MemoryBytes      *int64       `json:"memory_bytes"`
	MemoryLimitBytes *int64       `json:"memory_limit_bytes"`
	Sparkline        [][2]float64 `json:"sparkline"`
	Alerts           []string     `json:"alerts"`
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

// handleOverview returns compact values for every server the user may view (server.view; all
// for admins) and the host block for admins only (spec §6.1).
func (a *apiHandlers) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	var list []store.Server
	var err error
	if p.IsAdmin() {
		list, err = a.q.RbacListServers(ctx)
	} else {
		list, err = a.q.RbacListViewableServers(ctx, p.UserID)
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	snap := a.src.Latest()
	samples := make(map[int64]ServerSample, len(snap.Servers))
	for _, s := range snap.Servers {
		samples[s.ID] = s
	}
	serverAlerts := map[int64][]string{}
	hostAlerts := []string{}
	for _, f := range a.alerts.Firing(ctx) {
		if f.ServerID == 0 {
			hostAlerts = append(hostAlerts, f.Kind)
		} else {
			serverAlerts[f.ServerID] = append(serverAlerts[f.ServerID], f.Kind)
		}
	}
	now := a.clock.Now()
	out := overviewJSON{Servers: make([]overviewServerJSON, 0, len(list))}
	for _, s := range list {
		spark, err := readSparkline(ctx, a.q, s.ID, now)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		js := overviewServerJSON{ID: s.ID, Sparkline: spark, Alerts: serverAlerts[s.ID]}
		if js.Alerts == nil {
			js.Alerts = []string{}
		}
		if smp, ok := samples[s.ID]; ok {
			js.Up, js.Players, js.PlayersMax, js.TPS = smp.Up, smp.Players, smp.PlayersMax, smp.TPS
			js.CPUPercent, js.MemoryBytes, js.MemoryLimitBytes = smp.CPUPercent, smp.MemoryBytes, smp.MemoryLimit
		}
		out.Servers = append(out.Servers, js)
	}
	if p.IsAdmin() {
		h := snap.Host
		hj := &hostJSON{CPUPercent: h.CPUPercent, Load1: h.Load1, MemoryUsed: h.MemoryUsed, MemoryTotal: h.MemoryTotal,
			DiskFree: h.DiskFree, DiskTotal: h.DiskTotal, Alerts: hostAlerts}
		if !snap.At.IsZero() {
			at := snap.At.UnixMilli()
			hj.At = &at
		}
		out.Host = hj
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
