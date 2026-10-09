package users

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store"
)

var serverNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)

var powerActions = map[string]bool{"start": true, "stop": true, "restart": true}

// serverJSON is the OpenAPI schema Server; permissions are the caller's effective permissions.
type serverJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Permissions []rbac.Permission `json:"permissions"`
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func allPerms() []rbac.Permission { return slices.Clone(rbac.AllPermissions) }

func (h *Handler) serverRoutes(api *rbac.Router) {
	api.Get("/servers", rbac.Authenticated(), h.handleListServers)
	api.Post("/servers", rbac.Admin().WithSudo(), h.handleCreateServer)
	api.Get("/servers/{id}", rbac.Server(rbac.PermServerView, "id"), h.handleGetServer)
	api.Delete("/servers/{id}", rbac.Admin().WithSudo(), h.handleDeleteServer)
	api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), h.handleServerPower)
}

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx)
	out := []serverJSON{}
	if p.IsAdmin() {
		list, err := h.q.RbacListServers(ctx)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		for _, s := range list {
			out = append(out, serverJSON{ID: s.ID, Name: s.Name, Permissions: allPerms()})
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	list, err := h.q.RbacListViewableServers(ctx, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	grants, err := loadGrants(ctx, h.q, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	perms := make(map[int64][]rbac.Permission, len(grants))
	for _, g := range grants {
		perms[g.ServerID] = g.Permissions
	}
	for _, s := range list {
		out = append(out, serverJSON{ID: s.ID, Name: s.Name, Permissions: perms[s.ID]})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// loadServer resolves {id}; it writes 404 itself and returns false if the server is missing.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "server")
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r, "server")
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

func (h *Handler) handleGetServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	perms := allPerms()
	if p, _ := principal.From(ctx); !p.IsAdmin() {
		rows, err := h.q.RbacListUserServerPermissions(ctx, store.RbacListUserServerPermissionsParams{UserID: p.UserID, ServerID: s.ID})
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		set := make(map[rbac.Permission]bool, len(rows))
		for _, row := range rows {
			set[rbac.Permission(row)] = true
		}
		perms = orderedPerms(set)
	}
	httpx.WriteJSON(w, http.StatusOK, serverJSON{ID: s.ID, Name: s.Name, Permissions: perms})
}

type createServerRequest struct {
	Name string `json:"name"`
}

func (h *Handler) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !serverNameRe.MatchString(req.Name) {
		writeValidation(w, r, "name must match ^[a-z0-9][a-z0-9-]{1,31}$")
		return
	}
	if _, err := h.q.RbacGetServerByName(ctx, req.Name); err == nil {
		writeConflict(w, r, "server name already taken")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	s, err := h.q.RbacCreateServer(ctx, store.RbacCreateServerParams{Name: req.Name, CreatedAt: h.clock.Now().UnixMilli()})
	if isUniqueViolation(err) {
		writeConflict(w, r, "server name already taken")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerCreate, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	httpx.WriteJSON(w, http.StatusCreated, serverJSON{ID: s.ID, Name: s.Name, Permissions: allPerms()})
}

func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	n, err := h.q.RbacDeleteServer(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 { // deleted concurrently between lookup and delete
		writeNotFound(w, r, "server")
		return
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	w.WriteHeader(http.StatusNoContent)
}

type powerRequest struct {
	Action string `json:"action"`
}

// handleServerPower is a stub in sub-project 1: it validates, audits and accepts.
func (h *Handler) handleServerPower(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req powerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !powerActions[req.Action] {
		writeValidation(w, r, "action must be start, stop or restart")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
		secs := int(math.Ceil(d.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many power actions, try again later")
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": req.Action, "name": s.Name})
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
