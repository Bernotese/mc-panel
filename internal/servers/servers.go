package servers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
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
		v, err := d.server(ctx, s, ps)
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
	out, err := d.server(ctx, s, perms)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, redactFor(p, out))
}

// redactFor hides the raw last_error from non-admins: it can name host paths, images and
// Docker internals. The state (error) stays visible. The alert switch is admin-only too. The
// web routes and their error are admin-only as well (viewers get web_url and web_status). The
// world seed is left out unless the caller is admin or has server.files on this server (it
// reads server.properties anyway; SEC-28). v.Permissions are the caller's permissions.
func redactFor(p principal.Principal, v serverJSON) serverJSON {
	if p.IsAdmin() {
		return v
	}
	v.Status.LastError = ""
	v.DownAlerts = nil
	v.WebRoutes, v.WebError = nil, ""
	if v.Instance != nil && !slices.Contains(v.Permissions, rbac.PermServerFiles) {
		in := *v.Instance
		in.Properties.Seed = nil
		v.Instance = &in
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
	if reservedName(req.Name) {
		writeValidation(w, r, "names starting with dev- are reserved for dev instances")
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
	if err := checkMemoryLimit(c.MemoryMiB, h.maxMemoryMiB(ctx)); err != nil {
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
		if err := nameFree(ctx, q, req.Name); err != nil {
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
	if errors.Is(err, errNameDeleting) {
		writeConflict(w, r, errNameDeleting.Error())
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerCreate, serverTarget(s.ID), audit.ResultSuccess, c.auditDetails(s.Name))
	h.Trigger()
	out, err := view(s, allPerms(), &in, nil, set.BaseDomain, h.snapshot()) // a new server has no web routes
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	on := true // a new server has no server_alerts row
	out.DownAlerts = &on
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
	// WebRoutes is not part of the spec (no revision bump): the complete list, [] = off; null
	// is refused.
	WebRoutes optional[[]agentapi.WebTarget] `json:"web_routes"`
	// ExpectedRevision makes the request conditional: it is refused (409 server_modified) unless
	// the stored revision still equals it. Absent or null = no check.
	ExpectedRevision *int64 `json:"expected_revision"`
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

// patchError is an answer decided inside the PATCH transaction; returning it rolls back.
type patchError struct {
	status    int
	code, msg string
}

func (e *patchError) Error() string { return e.msg }

// patchResult is what one PATCH transaction committed.
type patchResult struct {
	s   store.Server
	in  store.Instance // the row after the write
	set instanceSettings
	// changes are the spec fields this write replaced (diff of the row it read), nil if none.
	changes map[string]any
	// routes are the stored web routes after the write; oldRoutes the ones it replaced when
	// routesChanged.
	routes, oldRoutes []agentapi.WebTarget
	routesChanged     bool
}

// handleUpdateServer applies a partial update (UpdateServerRequest) in one write transaction:
// it reads the row, checks expected_revision, merges and validates the request, then writes
// the spec and the web routes, so concurrent requests never revert each other's fields and a
// failure leaves everything unchanged (issue #150). Audit entries follow the commit and name
// the values this write replaced.
func (h *Handler) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r)
		return
	}
	maxMiB := h.maxMemoryMiB(ctx) // reads /proc: outside the transaction
	var res patchResult
	err := h.inTx(ctx, func(q *store.Queries) error {
		var err error
		res, err = h.patchServer(ctx, q, id, req, maxMiB)
		return err
	})
	if pe := (*patchError)(nil); errors.As(err, &pe) {
		httpx.WriteError(w, r, pe.status, pe.code, pe.msg)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if len(res.changes) > 0 {
		h.record(ctx, audit.ActionServerUpdate, serverTarget(res.s.ID), audit.ResultSuccess,
			map[string]any{"name": res.s.Name, "revision": res.in.Revision, "changes": res.changes})
	}
	if res.routesChanged {
		h.record(ctx, audit.ActionServerWebRoutesUpdate, serverTarget(res.s.ID), audit.ResultSuccess,
			map[string]any{"name": res.s.Name, "old": webRoutesDetails(res.oldRoutes), "new": webRoutesDetails(res.routes)})
	}
	if len(res.changes) > 0 || res.routesChanged {
		h.Trigger() // a new revision or route list for the reconcile loop
	}
	out, err := view(res.s, allPerms(), &res.in, res.routes, res.set.BaseDomain, h.snapshot())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	on, err := h.downAlerts(ctx, res.s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.DownAlerts = &on
	dd, err := h.loadDevData(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	dd.apply(&out)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// patchServer is the transaction of handleUpdateServer. maxMiB is the host's per-server
// memory limit.
func (h *Handler) patchServer(ctx context.Context, q *store.Queries, id int64, req updateServerRequest, maxMiB int) (patchResult, error) {
	var res patchResult
	var err error
	if res.s, err = q.RbacGetServer(ctx, id); errors.Is(err, sql.ErrNoRows) {
		return res, &patchError{http.StatusNotFound, httpx.CodeNotFound, "server not found"}
	} else if err != nil {
		return res, err
	}
	in, err := q.ServersGetInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return res, &patchError{http.StatusConflict, httpx.CodeConflict, "server is not configured (no instance)"}
	}
	if err != nil {
		return res, err
	}
	if req.ExpectedRevision != nil && *req.ExpectedRevision != in.Revision {
		return res, &patchError{http.StatusConflict, httpx.CodeServerModified,
			fmt.Sprintf("the server was changed meanwhile (revision %d, expected %d): reload it and apply the changes again", in.Revision, *req.ExpectedRevision)}
	}
	dev, err := isDev(ctx, q, id)
	if err != nil {
		return res, err
	}
	if dev && req.setsMoreThanMemory() {
		return res, &patchError{http.StatusConflict, httpx.CodeDevManaged,
			"a dev instance follows its source server: only memory_mib can be changed here, dev settings go through /servers/{id}/dev"}
	}
	invalid := func(err error) error { return &patchError{http.StatusBadRequest, httpx.CodeValidation, err.Error()} }
	cur, err := configFromInstance(in)
	if err != nil {
		return res, err
	}
	next, err := req.apply(cur)
	if err != nil {
		return res, invalid(err)
	}
	if res.set, err = h.loadSettingsQ(ctx, q); err != nil {
		return res, err
	}
	if err := next.validate(res.set.CurseForgeKey); err != nil {
		return res, invalid(err)
	}
	if err := checkMemoryLimit(next.MemoryMiB, maxMiB); err != nil {
		return res, invalid(err)
	}
	webNext, err := req.webRoutes()
	if err != nil {
		return res, invalid(err)
	}
	if len(webNext) > 0 && res.set.BaseDomain == nil {
		return res, &patchError{http.StatusConflict, httpx.CodeConflict, errBaseDomainRequired}
	}
	if res.changes, err = diff(cur, next); err != nil {
		return res, err
	}
	res.in = in
	if len(res.changes) > 0 {
		now := h.clock.Now().UnixMilli()
		if dev {
			// Only the memory, like a sync writes everything but the memory.
			res.in, err = q.ServersUpdateDevMemory(ctx, store.ServersUpdateDevMemoryParams{
				MemoryMib: int64(next.MemoryMiB), UpdatedAt: now, ServerID: id,
			})
		} else {
			propsJSON, perr := next.propertiesJSON()
			if perr != nil {
				return res, perr
			}
			res.in, err = q.ServersUpdateInstance(ctx, store.ServersUpdateInstanceParams{
				Version: next.Version, LoaderVersion: toNullString(next.LoaderVersion), Modpack: toNullString(next.Modpack),
				Java: string(next.Java), MemoryMib: int64(next.MemoryMiB), Properties: propsJSON,
				UpdatedAt: now, ServerID: id,
			})
		}
		if err != nil {
			return res, err
		}
	} else {
		res.changes = nil
	}
	rows, err := q.ServersListServerWebRoutes(ctx, id)
	if err != nil {
		return res, err
	}
	res.routes = webTargets(rows)
	if req.WebRoutes.Set && !slices.Equal(res.routes, webNext) {
		if err := replaceWebRoutes(ctx, q, id, webNext); err != nil {
			return res, err
		}
		res.oldRoutes, res.routes, res.routesChanged = res.routes, webNext, true
	}
	return res, nil
}

// setsMoreThanMemory reports whether the request sets a field a dev instance takes from its
// source (dev instances spec §2: only memory_mib is the dev's own).
func (req updateServerRequest) setsMoreThanMemory() bool {
	return req.Version.Set || req.LoaderVersion.Set || req.Modpack.Set || req.Java.Set ||
		req.Properties.Set || req.WebRoutes.Set
}

// webRoutes returns the requested web routes sorted by path (root first), validated with the
// agent's rules (web port paths spec §2); nil when the request does not set them. An empty
// list (off) is valid.
func (req updateServerRequest) webRoutes() ([]agentapi.WebTarget, error) {
	if !req.WebRoutes.Set {
		return nil, nil
	}
	if req.WebRoutes.Value == nil {
		return nil, fmt.Errorf("web_routes: %w", errNotNullable)
	}
	ts := slices.Clone(*req.WebRoutes.Value)
	if len(ts) == 0 {
		return ts, nil
	}
	slices.SortStableFunc(ts, func(a, b agentapi.WebTarget) int { return strings.Compare(a.Path, b.Path) })
	if err := agentapi.CheckWebTargets(ts); err != nil {
		return nil, fmt.Errorf("web_routes: %w", err)
	}
	return ts, nil
}

// replaceWebRoutes replaces the stored web routes of a server with the validated, sorted list
// next (inside the caller's transaction).
func replaceWebRoutes(ctx context.Context, q *store.Queries, id int64, next []agentapi.WebTarget) error {
	if err := q.ServersDeleteWebRoutes(ctx, id); err != nil {
		return err
	}
	for _, t := range next {
		if err := q.ServersInsertWebRoute(ctx, store.ServersInsertWebRouteParams{ServerID: id, Path: t.Path, Port: int64(t.Port)}); err != nil {
			return err
		}
	}
	return nil
}

// downAlerts is the TP5 per-server alert switch; a missing row means on.
func (h *Handler) downAlerts(ctx context.Context, id int64) (bool, error) {
	v, err := h.q.MonitoringGetServerAlerts(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return v != 0, err
}

const (
	// purgeTimeout bounds purging the backups of a deleted server: removing a large restic
	// repository takes as long as a large delete in the file browser (filesapi.DeleteTimeout),
	// plus a minute for ending the server's running backup job first.
	purgeTimeout = filesapi.DeleteTimeout + time.Minute
	// purgeAnswerSlack is the time left for the answer after a purge that used all of purgeTimeout.
	purgeAnswerSlack = 10 * time.Second
)

// extendWrite gives the answer a write deadline d from now, past the server's WriteTimeout
// (like the long calls of serverfiles); a ResponseWriter without deadlines is fine.
func (h *Handler) extendWrite(w http.ResponseWriter, r *http.Request, d time.Duration) {
	err := http.NewResponseController(w).SetWriteDeadline(h.ioClock.Now().Add(d))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.log.DebugContext(r.Context(), "servers: set write deadline", "err", err)
	}
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

// handleDeleteServer deletes a server durably (issue #148): under the server's admission lock
// and the reconcile lock, its rows go and a tombstone (server_deletions) is written in one
// transaction; then the first cleanup attempt runs right away (the agent is asked to remove the
// container, data goes to deleted/ on the host; with ?purge_backups=1 the backups are purged,
// backups spec §5.6) and the answer is 202 with the deletion. Whatever does not succeed now is
// retried by the deletion loop until the agent confirms the removal; GET /servers/deletions
// shows the progress. A container of another server is never removed. The delete is admitted
// like a start (data safety spec §3), but a server whose restore could not be completed
// (recovery_required) may be deleted too: its half-swapped folders are first moved to
// backups/recovery/ by the files service (never deleted with the server, D4), audited on their
// own.
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
	// Answered before anything is locked; deleteServerRows checks again in its transaction.
	if _, err := h.q.DevGetBySource(ctx, s.ID); err == nil {
		writeDevExists(w, r)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	dev, err := h.q.DevGet(ctx, s.ID)
	wasDev := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	// A refusal that needs no lock comes at once (no 5 s wait behind a restore).
	if !h.deleteAllowed(w, r, s.ID) {
		return
	}
	unlock, err := h.admission.Lock(ctx, s.ID)
	if err != nil {
		if !WriteDataStateError(w, r, err) {
			h.log.DebugContext(ctx, "servers: delete admission ended with the request", "err", err)
		}
		return
	}
	defer unlock()
	configured, recovery, ok := h.deleteServerRows(w, r, s, purge)
	if !ok {
		return
	}
	// The rows are gone: neither the first cleanup attempt nor the audit entry may be cut short
	// by a client that goes away. A purge can take minutes, longer than the server's
	// WriteTimeout: the answer gets a write deadline past it.
	ctx = context.WithoutCancel(ctx)
	if purge {
		h.extendWrite(w, r, purgeTimeout+purgeAnswerSlack)
	}
	d, completed, err := h.attemptDeletion(ctx, s.ID, nil)
	if err != nil {
		h.log.Error("servers: delete: first cleanup attempt", "server_id", s.ID, "err", err)
	}
	details := map[string]any{"name": s.Name, "configured": configured}
	if recovery != nil {
		// the half-swapped folders of the restore stay on the host, below backups/recovery/
		details["recovery_required"], details["recovery_folder"] = true, recovery.Folder
	}
	if purge {
		details["backups_purged"] = d.Backups == delPurged
	}
	// a cleanup that finishes later gets a server.delete_completed entry of its own
	details["completed"] = completed
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, details)
	if wasDev {
		h.record(ctx, audit.ActionDevDelete, serverTarget(s.ID), audit.ResultSuccess,
			map[string]any{"name": s.Name, "source_id": dev.SourceID})
	}
	h.Trigger() // the next pass drops the server's web route
	h.KickDeletions()
	if err != nil { // the tombstone could not be read back: describe it from the request
		d = store.ServerDeletion{ServerID: s.ID, Name: s.Name, Container: delPending, Backups: delNone}
		if !configured {
			d.Container = delNone
		}
		if purge {
			d.Backups = delPending
		}
	}
	httpx.WriteJSON(w, http.StatusAccepted, toDeletionJSON(d))
}

// deleteServerRows deletes the rows of s under the reconcile lock (the caller holds the
// server's admission lock) and writes its tombstone in the same transaction: a reconcile pass
// or restart that read this server before can no longer recreate its container, and the
// deletion loop removes it. A server in recovery_required has its restore resolved first;
// recovery is the files service's answer then (nil otherwise). On failure it has written the
// error answer and returns ok = false.
func (h *Handler) deleteServerRows(w http.ResponseWriter, r *http.Request, s store.Server, purge bool) (configured bool, recovery *filesapi.Recovery, ok bool) {
	ctx := r.Context()
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	info, ok := h.deleteState(w, r, s.ID)
	if !ok {
		return false, nil, false
	}
	_, err := h.q.ServersGetInstance(ctx, s.ID)
	configured = err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	if info.State == StateRecoveryRequired {
		// Last before the rows go (data safety spec D4). Should the transaction still fail, the
		// server is safe again but must not start with a half-swapped world: its wish to run is
		// dropped first.
		if configured {
			if _, err := h.q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{
				DesiredState: desiredStopped, UpdatedAt: h.clock.Now().UnixMilli(), ServerID: s.ID,
			}); err != nil {
				httpx.Internal(w, r, err)
				return false, nil, false
			}
		}
		if recovery, ok = h.resolveForDelete(w, r, s); !ok {
			return false, nil, false
		}
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	defer func() { _ = tx.Rollback() }()
	q := h.q.WithTx(tx)
	// A dev created since the check in handleDeleteServer: its source stays (dev_instances has no
	// cascade on source_id).
	if _, err := q.DevGetBySource(ctx, s.ID); err == nil {
		writeDevExists(w, r)
		return false, nil, false
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	n, err := q.RbacDeleteServer(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	if n == 0 { // deleted concurrently between lookup and delete
		writeNotFound(w, r)
		return false, nil, false
	}
	if err := deleteMonitoring(ctx, q, s.ID); err != nil {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	container, backups := delNone, delNone
	if configured {
		container = delPending
	}
	if purge {
		backups = delPending
	}
	now := h.clock.Now().UnixMilli()
	if err := q.ServersCreateDeletion(ctx, store.ServersCreateDeletionParams{
		ServerID: s.ID, Name: s.Name, Container: container, Backups: backups, RequestedAt: now, UpdatedAt: now,
	}); err != nil {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	if err := tx.Commit(); err != nil {
		httpx.Internal(w, r, err)
		return false, nil, false
	}
	delete(h.reconcile.attempts, s.ID)
	delete(h.reconcile.foreign, s.ID)
	h.setPanelError(s.ID, "")
	return configured, recovery, true
}

// deleteAllowed answers a delete that the data state refuses (busy, unknown) without waiting for
// the admission lock; deleteState checks again under it.
func (h *Handler) deleteAllowed(w http.ResponseWriter, r *http.Request, id int64) bool {
	_, ok := h.deleteState(w, r, id)
	return ok
}

// deleteState returns the data state of server id if a delete is allowed in it (safe or
// recovery_required); otherwise it has written the refusal.
func (h *Handler) deleteState(w http.ResponseWriter, r *http.Request, id int64) (DataInfo, bool) {
	ctx := r.Context()
	info, err := h.data.Lookup(ctx, id, nil)
	if err != nil && ctx.Err() == nil {
		h.log.WarnContext(ctx, "servers: delete refused, data state unknown", "server_id", id, "err", err)
	}
	if err := StateErr(info.State); err != nil && info.State != StateRecoveryRequired {
		WriteDataStateError(w, r, err)
		return info, false
	}
	return info, true
}

// resolveForDelete moves the half-swapped folders of the unresolved restore of s out of its data
// directory before the delete takes the directory away (data safety spec D4) and audits the
// move on its own, so it is recorded even if the delete fails afterwards. A service that finds
// no unresolved restore (any more) leaves nothing to keep: rec is nil then. On failure it has
// written the error answer.
func (h *Handler) resolveForDelete(w http.ResponseWriter, r *http.Request, s store.Server) (rec *filesapi.Recovery, ok bool) {
	got, err := h.resolve(w, r, s)
	switch {
	case err == nil:
		return &got, true
	case errors.Is(err, filesapi.ErrNotFound):
		return nil, true
	case errors.Is(err, filesapi.ErrBusy):
		writeConflict(w, r, "a job holds this server's data; try again when it is done")
	default:
		writeResolveError(w, r, err)
	}
	return nil, false
}

// deleteMonitoring removes the TP5 series and alert states of a deleted server (server_alerts
// goes by ON DELETE CASCADE). The keys follow internal/monitoring's alertKey; servers cannot
// import monitoring (monitoring imports servers for DataStates).
func deleteMonitoring(ctx context.Context, q *store.Queries, id int64) error {
	for _, del := range []func(context.Context, int64) error{
		q.MonitoringDeleteSeriesRaw, q.MonitoringDeleteSeries5m, q.MonitoringDeleteSeries1h,
	} {
		if err := del(ctx, id); err != nil {
			return err
		}
	}
	for _, kind := range []string{"server.down", "server.crash_loop", "server.recovery_required"} {
		if err := q.MonitoringDeleteAlertState(ctx, fmt.Sprintf("%s:%d", kind, id)); err != nil {
			return err
		}
	}
	return nil
}

func writeDevExists(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeDevExists, "the server has a dev instance: delete the dev instance first")
}
