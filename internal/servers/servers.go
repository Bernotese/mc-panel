package servers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx)
	d, err := h.loadViewData(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var list []store.Server
	var perms map[int64][]rbac.Permission
	if p.IsAdmin() {
		list, err = h.q.RbacListServers(ctx)
	} else {
		list, err = h.q.RbacListViewableServers(ctx, p.UserID)
		if err == nil {
			perms, err = userPerms(ctx, h.q, p.UserID)
		}
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]serverJSON, 0, len(list))
	for _, s := range list {
		ps := allPerms()
		if !p.IsAdmin() {
			ps = perms[s.ID]
		}
		v, err := d.server(s, ps)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out = append(out, redactFor(p, v))
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

// loadInstance returns the instance of s; it writes 409 itself if the server has none.
func (h *Handler) loadInstance(w http.ResponseWriter, r *http.Request, s store.Server) (store.Instance, bool) {
	in, err := h.q.ServersGetInstance(r.Context(), s.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeConflict(w, r, "server is not configured (no instance)")
		return store.Instance{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Instance{}, false
	}
	return in, true
}

// writeAgentError maps a failed synchronous agent call to the API error.
func writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errForeignContainer):
		writeConflict(w, r, err.Error())
	case errors.Is(err, errBaseDomainMissing):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeBaseDomainMissing, "set a base domain in the server settings first")
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeDockerUnavailable, "docker is not available on the host")
	case errors.Is(err, agentapi.ErrBusy):
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "the agent is busy with this server, try again shortly")
	case errors.Is(err, instancespec.ErrInvalid):
		writeValidation(w, r, err.Error())
	case errors.Is(err, agentapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the agent is not reachable")
	default:
		httpx.Internal(w, r, err)
	}
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
	d, err := h.loadViewData(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := d.server(s, perms)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, redactFor(p, out))
}

// redactFor hides the raw last_error from non-admins: it can name host paths, images and
// Docker internals. The state (error) stays visible.
func redactFor(p principal.Principal, v serverJSON) serverJSON {
	if !p.IsAdmin() {
		v.Status.LastError = ""
	}
	return v
}

// createServerRequest is the OpenAPI schema CreateServerRequest. Properties are decoded
// separately so that unknown keys become validation errors.
type createServerRequest struct {
	Name          string             `json:"name"`
	Type          instancespec.Type  `json:"type"`
	Version       string             `json:"version"`
	LoaderVersion *string            `json:"loader_version"`
	Modpack       *string            `json:"modpack"`
	Java          *instancespec.Java `json:"java"`
	MemoryMiB     int                `json:"memory_mib"`
	Properties    json.RawMessage    `json:"properties"`
	EulaAccepted  bool               `json:"eula_accepted"`
}

func (h *Handler) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if set.BaseDomain == nil {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeBaseDomainMissing, "set a base domain in the server settings first")
		return
	}
	if !instancespec.ValidName(req.Name) {
		writeValidation(w, r, "name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$")
		return
	}
	if !req.EulaAccepted {
		writeValidation(w, r, "eula_accepted must be true (https://aka.ms/MinecraftEULA)")
		return
	}
	props, err := decodeProperties(req.Properties)
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	c := config{
		Type: req.Type, Version: req.Version, LoaderVersion: req.LoaderVersion, Modpack: req.Modpack,
		Java: instancespec.JavaFor(req.Type, req.Version), MemoryMiB: req.MemoryMiB, Properties: props,
	}
	if req.Java != nil {
		c.Java = *req.Java
	}
	if err := c.validate(set.CurseForgeKey); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	propsJSON, err := c.propertiesJSON()
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	var (
		s  store.Server
		in store.Instance
	)
	// Server and instance row are written together: a taken name leaves no half instance.
	err = h.inTx(ctx, func(q *store.Queries) error {
		// An explicit, never reused ID: a new server must not adopt the container or world of a
		// deleted one (migration 00003).
		id, err := q.ServersNextServerID(ctx)
		if err != nil {
			return err
		}
		if s, err = q.ServersCreateServer(ctx, store.ServersCreateServerParams{ID: id, Name: req.Name, CreatedAt: now}); err != nil {
			return err
		}
		in, err = q.ServersCreateInstance(ctx, store.ServersCreateInstanceParams{
			ServerID: s.ID, Type: string(c.Type), Version: c.Version, LoaderVersion: toNullString(c.LoaderVersion),
			Modpack: toNullString(c.Modpack), Java: string(c.Java), MemoryMib: int64(c.MemoryMiB),
			Properties: propsJSON, CreatedAt: now, UpdatedAt: now,
		})
		return err
	})
	if store.IsUniqueViolation(err) {
		writeConflict(w, r, "server name already taken")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerCreate, serverTarget(s.ID), audit.ResultSuccess, c.auditDetails(s.Name))
	h.Trigger()
	out, err := view(s, allPerms(), &in, set.BaseDomain, h.snapshot())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// updateServerRequest is the OpenAPI schema UpdateServerRequest: absent fields stay,
// properties replaces the whole object. Only loader_version and modpack may be null.
type updateServerRequest struct {
	Version       optional[string]            `json:"version"`
	LoaderVersion optional[string]            `json:"loader_version"`
	Modpack       optional[string]            `json:"modpack"`
	Java          optional[instancespec.Java] `json:"java"`
	MemoryMiB     optional[int]               `json:"memory_mib"`
	Properties    optional[json.RawMessage]   `json:"properties"`
}

var errNotNullable = errors.New("must not be null")

// apply merges the request into c.
func (req updateServerRequest) apply(c config) (config, error) {
	if req.Version.Set {
		if req.Version.Value == nil {
			return c, fmt.Errorf("version: %w", errNotNullable)
		}
		c.Version = *req.Version.Value
	}
	if req.LoaderVersion.Set {
		c.LoaderVersion = req.LoaderVersion.Value
	}
	if req.Modpack.Set {
		c.Modpack = req.Modpack.Value
	}
	if req.Java.Set {
		if req.Java.Value == nil {
			return c, fmt.Errorf("java: %w", errNotNullable)
		}
		c.Java = *req.Java.Value
	}
	if req.MemoryMiB.Set {
		if req.MemoryMiB.Value == nil {
			return c, fmt.Errorf("memory_mib: %w", errNotNullable)
		}
		c.MemoryMiB = *req.MemoryMiB.Value
	}
	if req.Properties.Set {
		if req.Properties.Value == nil {
			return c, fmt.Errorf("properties: %w", errNotNullable)
		}
		p, err := decodeProperties(*req.Properties.Value)
		if err != nil {
			return c, err
		}
		c.Properties = p
	}
	return c, nil
}

func (h *Handler) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	in, ok := h.loadInstance(w, r, s)
	if !ok {
		return
	}
	cur, err := configFromInstance(in)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	next, err := req.apply(cur)
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := next.validate(set.CurseForgeKey); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	changes, err := diff(cur, next)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if len(changes) > 0 {
		propsJSON, err := next.propertiesJSON()
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		in, err = h.q.ServersUpdateInstance(ctx, store.ServersUpdateInstanceParams{
			Version: next.Version, LoaderVersion: toNullString(next.LoaderVersion), Modpack: toNullString(next.Modpack),
			Java: string(next.Java), MemoryMib: int64(next.MemoryMiB), Properties: propsJSON,
			UpdatedAt: h.clock.Now().UnixMilli(), ServerID: s.ID,
		})
		if errors.Is(err, sql.ErrNoRows) { // deleted concurrently
			writeNotFound(w, r)
			return
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		h.record(ctx, audit.ActionServerUpdate, serverTarget(s.ID), audit.ResultSuccess,
			map[string]any{"name": s.Name, "revision": in.Revision, "changes": changes})
		h.Trigger()
	}
	out, err := view(s, allPerms(), &in, set.BaseDomain, h.snapshot())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// purgeBackupsParam parses ?purge_backups: absent, "0" and "false" = no; "1" and "true" = yes.
func purgeBackupsParam(r *http.Request) (purge, ok bool) {
	switch r.URL.Query().Get("purge_backups") {
	case "", "0", "false":
		return false, true
	case "1", "true":
		return true, true
	}
	return false, false
}

// handleDeleteServer deletes the rows in a transaction and asks the agent to remove the
// container (data goes to deleted/ on the host) while it is open: the rows are committed away
// only once the agent accepted the remove, and a remove is only sent for rows that are gone.
// Without the agent a configured server cannot be deleted, so no container is orphaned; a
// server without instance needs no agent. A container of another server is never removed.
// With ?purge_backups=1 the server's backups are deleted after the commit (backups spec §5.6).
func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	purge, ok := purgeBackupsParam(r)
	if !ok {
		writeValidation(w, r, "purge_backups must be true, false, 1 or 0")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	configured, ok := h.deleteServerRows(w, r, s)
	if !ok {
		return
	}
	details := map[string]any{"name": s.Name, "configured": configured}
	if purge {
		// After the commit and outside the reconcile lock: deleting a large repository can take
		// minutes. A failure leaves an orphaned repository (as without the option); the server
		// stays deleted. A client that goes away must not cut the purge short.
		if err := h.backups.PurgeBackups(context.WithoutCancel(ctx), s.ID); err != nil {
			h.log.Error("servers: delete: purging the backups failed", "server_id", s.ID, "err", err)
			details["backups_purged"] = false
		} else {
			details["backups_purged"] = true
		}
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

// deleteServerRows deletes the rows of s and (if configured and not foreign) its container under
// the reconcile lock. On failure it has written the error answer and returns ok = false.
func (h *Handler) deleteServerRows(w http.ResponseWriter, r *http.Request, s store.Server) (configured, ok bool) {
	ctx := r.Context()
	// Exclude a concurrent reconcile pass or restart: one that read this server before the
	// delete would otherwise recreate the container after the remove (orphan container).
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	_, err := h.q.ServersGetInstance(ctx, s.ID)
	configured = err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return false, false
	}
	remove := configured
	if configured {
		foreign, err := h.isForeign(ctx, s)
		if err != nil {
			writeAgentError(w, r, err)
			return false, false
		}
		if foreign {
			h.log.Warn("servers: delete leaves the container of another server alone", "server_id", s.ID, "name", s.Name)
			remove = false
		}
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.Internal(w, r, err)
		return false, false
	}
	defer func() { _ = tx.Rollback() }()
	n, err := h.q.WithTx(tx).RbacDeleteServer(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return false, false
	}
	if n == 0 { // deleted concurrently between lookup and delete
		writeNotFound(w, r)
		return false, false
	}
	if remove {
		if err := h.inst.Remove(ctx, s.ID); err != nil {
			writeAgentError(w, r, err) // the rollback keeps the rows
			return false, false
		}
	}
	if err := tx.Commit(); err != nil {
		h.log.Error("servers: delete: commit failed after the agent accepted the remove", "server_id", s.ID, "err", err)
		httpx.Internal(w, r, err)
		return false, false
	}
	delete(h.reconcile.attempts, s.ID)
	delete(h.reconcile.foreign, s.ID)
	h.setPanelError(s.ID, "")
	return configured, true
}

// isForeign asks the agent whether the container with the server's ID belongs to another
// server (see foreignContainer).
func (h *Handler) isForeign(ctx context.Context, s store.Server) (bool, error) {
	resp, snap, err := h.refreshStatus(ctx)
	if err != nil {
		return false, err
	}
	if resp.Docker != agentapi.DockerOK {
		return false, agentapi.ErrDockerUnavailable
	}
	return foreignContainer(snap.actualStatus(s.ID), s.Name), nil
}
