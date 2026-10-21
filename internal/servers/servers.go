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
	if err := checkMemoryLimit(next.MemoryMiB, h.maxMemoryMiB(ctx)); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	webNext, err := req.webRoutes()
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	if len(webNext) > 0 && set.BaseDomain == nil {
		writeConflict(w, r, errBaseDomainRequired)
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
	var routes []agentapi.WebTarget
	if req.WebRoutes.Set {
		if routes, ok = h.updateWebRoutes(w, r, s, webNext); !ok {
			return
		}
	} else {
		rows, err := h.q.ServersListServerWebRoutes(ctx, s.ID)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		routes = webTargets(rows)
	}
	out, err := view(s, allPerms(), &in, routes, set.BaseDomain, h.snapshot())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	on, err := h.downAlerts(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.DownAlerts = &on
	httpx.WriteJSON(w, http.StatusOK, out)
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

// updateWebRoutes replaces the web routes of s with the validated, sorted list next, audits
// the change and triggers the reconcile loop that sends the route list; an unchanged list
// writes nothing. It returns the stored list. On failure it has written the error answer and
// returns ok = false.
func (h *Handler) updateWebRoutes(w http.ResponseWriter, r *http.Request, s store.Server, next []agentapi.WebTarget) ([]agentapi.WebTarget, bool) {
	ctx := r.Context()
	var old []agentapi.WebTarget
	changed := false
	// Read and replace in one write transaction, so old is the list this write replaced.
	err := h.inTx(ctx, func(q *store.Queries) error {
		if _, err := q.ServersGetInstance(ctx, s.ID); err != nil {
			return err // sql.ErrNoRows: deleted concurrently
		}
		rows, err := q.ServersListServerWebRoutes(ctx, s.ID)
		if err != nil {
			return err
		}
		old = webTargets(rows)
		if slices.Equal(old, next) {
			return nil
		}
		changed = true
		if err := q.ServersDeleteWebRoutes(ctx, s.ID); err != nil {
			return err
		}
		for _, t := range next {
			if err := q.ServersInsertWebRoute(ctx, store.ServersInsertWebRouteParams{ServerID: s.ID, Path: t.Path, Port: int64(t.Port)}); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
		return nil, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return nil, false
	}
	if !changed {
		return old, true
	}
	h.record(ctx, audit.ActionServerWebRoutesUpdate, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"name": s.Name, "old": webRoutesDetails(old), "new": webRoutesDetails(next)})
	h.Trigger()
	return next, true
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

// handleDeleteServer deletes the rows in a transaction and asks the agent to remove the
// container (data goes to deleted/ on the host) while it is open: the rows are committed away
// only once the agent accepted the remove, and a remove is only sent for rows that are gone.
// Without the agent a configured server cannot be deleted, so no container is orphaned; a
// server without instance needs no agent. A container of another server is never removed.
// With ?purge_backups=1 the server's backups are deleted after the commit (backups spec §5.6).
// While a job rewrites the data directory (extract or restore) the delete is refused like a start.
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
	if h.gate.DataJobRunning(ctx, s.ID) {
		writeConflict(w, r, "a file or backup job is running for this server; try again when it is done")
		return
	}
	configured, ok := h.deleteServerRows(w, r, s)
	if !ok {
		return
	}
	// The rows are gone: neither the purge nor the audit entry may be cut short by a client that
	// goes away.
	ctx = context.WithoutCancel(ctx)
	details := map[string]any{"name": s.Name, "configured": configured}
	if purge {
		// After the commit and outside the reconcile lock: deleting a large repository can take
		// minutes, longer than the server's WriteTimeout. The purge gets its own deadline and the
		// answer a write deadline past it. A failure leaves an orphaned repository (as without the
		// option); the server stays deleted.
		h.extendWrite(w, r, purgeTimeout+purgeAnswerSlack)
		pctx, cancel := context.WithTimeout(ctx, purgeTimeout)
		err := h.backups.PurgeBackups(pctx, s.ID)
		cancel()
		if err != nil {
			h.log.Error("servers: delete: purging the backups failed", "server_id", s.ID, "err", err)
			details["backups_purged"] = false
		} else {
			details["backups_purged"] = true
		}
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, details)
	h.Trigger() // the next pass drops the server's web route
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
	if err := deleteMonitoring(ctx, h.q.WithTx(tx), s.ID); err != nil {
		httpx.Internal(w, r, err)
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

// deleteMonitoring removes the TP5 series and alert states of a deleted server (server_alerts
// goes by ON DELETE CASCADE). The keys follow internal/monitoring's alertKey; servers cannot
// import monitoring (monitoring imports servers for DataGate).
func deleteMonitoring(ctx context.Context, q *store.Queries, id int64) error {
	for _, del := range []func(context.Context, int64) error{
		q.MonitoringDeleteSeriesRaw, q.MonitoringDeleteSeries5m, q.MonitoringDeleteSeries1h,
	} {
		if err := del(ctx, id); err != nil {
			return err
		}
	}
	for _, kind := range []string{"server.down", "server.crash_loop"} {
		if err := q.MonitoringDeleteAlertState(ctx, fmt.Sprintf("%s:%d", kind, id)); err != nil {
			return err
		}
	}
	return nil
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
