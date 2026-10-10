package servers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// serverJSON is the OpenAPI schema Server; permissions are the caller's effective permissions.
type serverJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Permissions []rbac.Permission `json:"permissions"`
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
	perms, err := userPerms(ctx, h.q, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
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
		writeNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
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
	p, _ := principal.From(ctx)
	perms, err := serverPerms(ctx, h.q, p, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
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
	if !instancespec.ValidName(req.Name) {
		writeValidation(w, r, "name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$")
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
	if store.IsUniqueViolation(err) {
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
		writeNotFound(w, r)
		return
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	w.WriteHeader(http.StatusNoContent)
}
