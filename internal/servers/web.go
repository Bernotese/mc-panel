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

// errBaseDomainRequired answers web routes without base domain (409 conflict).
const errBaseDomainRequired = "base domain required"

// webEntry is the agent's last answer for one route, together with the route it answered:
// a status for another fqdn or target list (base domain or routes changed since) is no status.
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

// webTargetsByServer groups the web_routes rows (ordered by server_id, path) per server,
// keeping the order: root first, then by path (web port paths spec §2).
func webTargetsByServer(rows []store.WebRoute) map[int64][]agentapi.WebTarget {
	out := make(map[int64][]agentapi.WebTarget)
	for _, r := range rows {
		out[r.ServerID] = append(out[r.ServerID], agentapi.WebTarget{Path: r.Path, Port: int(r.Port)})
	}
	return out
}

// webTargets converts the rows of one server (ordered by path).
func webTargets(rows []store.WebRoute) []agentapi.WebTarget {
	out := make([]agentapi.WebTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, agentapi.WebTarget{Path: r.Path, Port: int(r.Port)})
	}
	return out
}

// webRoutes builds the complete desired list (spec §3.3) from the instance rows, their web
// routes and the base domain: one route per instance with at least one web route, fqdn =
// name + "." + baseDomain; empty (never nil) when baseDomain is "". Sorted by id.
func webRoutes(rows []store.ServersListInstancesRow, routes map[int64][]agentapi.WebTarget, baseDomain string) []agentapi.WebRoute {
	out := []agentapi.WebRoute{}
	if baseDomain == "" {
		return out
	}
	for _, r := range rows {
		if ts := routes[r.ServerID]; len(ts) > 0 {
			out = append(out, agentapi.WebRoute{ID: r.ServerID, FQDN: r.Name + "." + baseDomain, Targets: ts})
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
	targets, err := h.q.ServersListWebRoutes(ctx)
	if err != nil {
		h.log.Error("servers: web routes: list routes", "err", err)
		return
	}
	set, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		h.log.Error("servers: web routes: load settings", "err", err)
		return
	}
	routes := webRoutes(rows, webTargetsByServer(targets), set.BaseDomain.String)
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

// webRouteURL is the public URL of one target: https://<fqdn>/ or https://<fqdn>/<path>/.
func webRouteURL(fqdn, path string) string {
	if path == "" {
		return "https://" + fqdn + "/"
	}
	return "https://" + fqdn + "/" + path + "/"
}

// applyWebView fills the web fields of out (redactFor removes web_routes and web_error for
// non-admins). routes are the server's stored web routes, root first. The status is off
// without routes or base domain, otherwise the agent's answer for exactly this route list,
// pending until there is one. web_url is the root's link and absent without a root route.
func applyWebView(out *serverJSON, name string, in *store.Instance, routes []agentapi.WebTarget, baseDomain *string, web map[int64]webEntry) {
	out.WebStatus = webOff
	var fqdn string
	if baseDomain != nil {
		fqdn = name + "." + *baseDomain
	}
	// The stored routes, also while they are off for want of a base domain (then without url).
	list := make([]webRouteJSON, 0, len(routes))
	for _, t := range routes {
		j := webRouteJSON{Path: t.Path, Port: t.Port}
		if baseDomain != nil {
			u := webRouteURL(fqdn, t.Path)
			j.URL = &u
		}
		list = append(list, j)
	}
	out.WebRoutes = &list
	if in == nil || len(routes) == 0 || baseDomain == nil {
		return
	}
	if routes[0].Path == "" {
		url := "https://" + fqdn
		out.WebURL = &url
	}
	route := agentapi.WebRoute{ID: in.ServerID, FQDN: fqdn, Targets: routes}
	out.WebStatus = webPending
	if e, ok := web[in.ServerID]; ok && e.route.Equal(route) {
		out.WebStatus = string(e.status.Status)
		if e.status.Status == agentapi.WebError {
			out.WebError = e.status.Error
		}
	}
}

// webRoutesDetails is the audit value of a route list: [{path, port}], never null.
func webRoutesDetails(ts []agentapi.WebTarget) []agentapi.WebTarget {
	if ts == nil {
		return []agentapi.WebTarget{}
	}
	return ts
}
