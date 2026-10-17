package servers

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// web_status values of the Server schema (web ports spec §3.2).
const (
	webOff     = "off"
	webPending = string(agentapi.WebPending)
)

// errBaseDomainRequired answers a web port without base domain (409 conflict).
const errBaseDomainRequired = "base domain required"

// webEntry is the agent's last answer for one route, together with the route it answered:
// a status for another fqdn or port (base domain or port changed since) is no status.
type webEntry struct {
	route  agentapi.WebRoute
	status agentapi.WebRouteStatus
}

// webState is kept in memory (like the panel errors): the agent's last answer per server id,
// or "agent unavailable" for every route when the PUT failed.
type webState struct {
	mu   sync.Mutex
	byID map[int64]webEntry
}

func (s *webState) set(byID map[int64]webEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID = byID
}

func (s *webState) snapshot() map[int64]webEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.byID)
}

// webRoutes builds the complete desired list (spec §3.3) from the instance rows and the
// base domain: every row with web_port set, fqdn = name + "." + baseDomain; empty (never
// nil) when baseDomain is "". Sorted by id.
func webRoutes(rows []store.ServersListInstancesRow, baseDomain string) []agentapi.WebRoute {
	out := []agentapi.WebRoute{}
	if baseDomain == "" {
		return out
	}
	for _, r := range rows {
		if r.WebPort.Valid {
			out = append(out, agentapi.WebRoute{ID: r.ServerID, FQDN: r.Name + "." + baseDomain, Port: int(r.WebPort.Int64)})
		}
	}
	slices.SortFunc(out, func(a, b agentapi.WebRoute) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// webErrorText is the web_error of every route when the route list did not reach the agent.
func webErrorText(err error) string {
	if errors.Is(err, agentapi.ErrDockerUnavailable) {
		return "docker unavailable"
	}
	return "agent unavailable"
}

// syncWeb sends the complete route list to the agent and remembers its answer. It runs on
// every reconcile pass, also when the agent or Docker is down (the answer is then an error
// for every route), so deletes and base domain changes need no extra hook.
func (h *Handler) syncWeb(ctx context.Context) {
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		h.log.Error("servers: web routes: list instances", "err", err)
		return
	}
	set, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		h.log.Error("servers: web routes: load settings", "err", err)
		return
	}
	routes := webRoutes(rows, set.BaseDomain.String)
	resp, err := h.web.WebRoutes(ctx, agentapi.WebRoutesRequest{Routes: routes})
	if err != nil && ctx.Err() != nil {
		return // our own context ended: that says nothing about the agent
	}
	byID := make(map[int64]webEntry, len(routes))
	if err != nil {
		if !h.reconcile.webDown {
			h.log.Warn("servers: web routes not applied", "routes", len(routes), "err", err)
		}
		h.reconcile.webDown = true
		for _, r := range routes {
			byID[r.ID] = webEntry{route: r, status: agentapi.WebRouteStatus{ID: r.ID, Status: agentapi.WebError, Error: webErrorText(err)}}
		}
		h.webs.set(byID)
		return
	}
	if h.reconcile.webDown {
		h.log.Info("servers: web routes applied again", "routes", len(routes))
		h.reconcile.webDown = false
	}
	answered := make(map[int64]agentapi.WebRouteStatus, len(resp.Routes))
	for _, st := range resp.Routes {
		answered[st.ID] = st
	}
	for _, r := range routes {
		st, ok := answered[r.ID]
		if !ok {
			st = agentapi.WebRouteStatus{ID: r.ID, Status: agentapi.WebPending}
		}
		byID[r.ID] = webEntry{route: r, status: st}
	}
	h.webs.set(byID)
}

// applyWebView fills the web fields of out (redactFor removes web_port and web_error for
// non-admins). The status is off without web port or base domain, otherwise the agent's
// answer for exactly this route, pending until there is one.
func applyWebView(out *serverJSON, name string, in *store.Instance, baseDomain *string, web map[int64]webEntry) {
	out.WebStatus = webOff
	if in == nil || !in.WebPort.Valid {
		return
	}
	port := int(in.WebPort.Int64)
	out.WebPort = &port // the stored setting, also while it is off for want of a base domain
	if baseDomain == nil {
		return
	}
	route := agentapi.WebRoute{ID: in.ServerID, FQDN: name + "." + *baseDomain, Port: port}
	url := "https://" + route.FQDN
	out.WebURL = &url
	out.WebStatus = webPending
	if e, ok := web[in.ServerID]; ok && e.route == route {
		out.WebStatus = string(e.status.Status)
		if e.status.Status == agentapi.WebError {
			out.WebError = e.status.Error
		}
	}
}

// webPortDetails is the audit value of a web port (number or null).
func webPortDetails(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
