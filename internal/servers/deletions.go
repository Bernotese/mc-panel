package servers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Durable server deletion (issue #148). DELETE /servers/{id} removes the server's rows and
// leaves a tombstone (server_deletions) in the same transaction; the cleanup is driven from it
// until it is confirmed: the container through the agent (done only when the agent reports the
// removal in InstancesResponse.Removed, so a remove lost in an agent restart is asked for
// again), the backups through the files service when the request asked for a purge. Every
// attempt holds the server's admission lock (IDs are never reused), so the request's own first
// attempt and the deletion loop never run at the same time.

// Container and backup states of a deletion (migration 00015).
const (
	delNone    = "none"
	delPending = "pending"
	delRemoved = "removed"
	delForeign = "foreign"
	delPurged  = "purged"
	delFailed  = "failed"
)

// Overall states of a deletion (OpenAPI ServerDeletionState).
const (
	deletionPending   = "pending" // cleanup in progress
	deletionFailed    = "failed"  // the container could not be removed yet; retried
	deletionPartial   = "partial" // the server is gone, but its backups could not be purged; retried
	deletionDone      = "done"
	deletionAbandoned = "abandoned" // an admin closed it unfinished; leftovers are the operator's
)

const (
	// deletionKeep is how long completed deletions stay listed.
	deletionKeep = 7 * 24 * time.Hour
	// purgeRetry is the first delay before a failed purge is tried again (doubling, at most
	// purgeRetryMax); the container is asked again after ReconcileInterval (doubling, at most
	// maxBackoff).
	purgeRetry    = time.Minute
	purgeRetryMax = time.Hour
	// deletionAlertAfter: a deletion that is still failed or partial this long after the request
	// is reported once (server.delete_failed); transient failures (an agent restart) stay quiet.
	deletionAlertAfter = 10 * time.Minute
)

// deletionJSON is the OpenAPI schema ServerDeletion.
type deletionJSON struct {
	ServerID       int64   `json:"server_id"`
	Name           string  `json:"name"`
	State          string  `json:"state"`
	Container      string  `json:"container"`
	ContainerError *string `json:"container_error"`
	Backups        string  `json:"backups"`
	BackupsError   *string `json:"backups_error"`
	RequestedAt    int64   `json:"requested_at"`
	UpdatedAt      int64   `json:"updated_at"`
	CompletedAt    *int64  `json:"completed_at"`
}

func containerDone(c string) bool { return c == delNone || c == delRemoved || c == delForeign }

func backupsDone(b string) bool { return b == delNone || b == delPurged }

func deletionState(d store.ServerDeletion) string {
	switch {
	case d.Abandoned != 0:
		return deletionAbandoned
	case containerDone(d.Container) && backupsDone(d.Backups):
		return deletionDone
	case containerDone(d.Container) && d.Backups == delFailed:
		return deletionPartial
	case d.ContainerError != "":
		return deletionFailed
	}
	return deletionPending
}

func toDeletionJSON(d store.ServerDeletion) deletionJSON {
	return deletionJSON{
		ServerID: d.ServerID, Name: d.Name, State: deletionState(d), Container: d.Container,
		ContainerError: emptyNil(d.ContainerError), Backups: d.Backups, BackupsError: emptyNil(d.BackupsError),
		RequestedAt: d.RequestedAt, UpdatedAt: d.UpdatedAt, CompletedAt: nullInt(d.CompletedAt),
	}
}

func emptyNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// deletionRetries are the in-memory retry delays per deletion (a panel restart retries at once).
type deletionRetries struct {
	mu sync.Mutex
	m  map[int64]*deletionRetry
}

type deletionRetry struct {
	removeAt, purgeAt time.Time // not before
	removes, purges   int       // attempts so far
	// issued: the agent accepted a remove since the panel started, so its last_error (once it
	// is settled again) is the remove's, not an older error of the instance.
	issued bool
}

func (d *deletionRetries) get(id int64) *deletionRetry {
	if d.m == nil {
		d.m = map[int64]*deletionRetry{}
	}
	r := d.m[id]
	if r == nil {
		r = &deletionRetry{}
		d.m[id] = r
	}
	return r
}

// due reports whether the remove (purge = false) or purge of deletion id may be attempted at
// now, and if so, books the attempt: the next one waits base doubled per attempt, at most limit.
func (d *deletionRetries) due(id int64, purge bool, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.get(id)
	at, n, base, limit := &r.removeAt, &r.removes, ReconcileInterval, maxBackoff
	if purge {
		at, n, base, limit = &r.purgeAt, &r.purges, purgeRetry, purgeRetryMax
	}
	if now.Before(*at) {
		return false
	}
	*at = now.Add(min(base<<min(*n, 10), limit))
	*n++
	return true
}

// issued reports whether the agent accepted a remove of id since the panel started.
func (d *deletionRetries) issued(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.get(id).issued
}

func (d *deletionRetries) markIssued(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.get(id).issued = true
}

func (d *deletionRetries) reset(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := d.m[id]; r != nil {
		r.removeAt, r.purgeAt = time.Time{}, time.Time{}
	}
}

func (d *deletionRetries) forget(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.m, id)
}

// KickDeletions asks the deletion loop for an immediate pass. It never blocks.
func (h *Handler) KickDeletions() {
	select {
	case h.delKick <- struct{}{}:
	default:
	}
}

// runDeletions drives the open deletions until ctx is cancelled: one pass right away, then
// every ReconcileInterval and after every KickDeletions.
func (h *Handler) runDeletions(ctx context.Context) {
	t := time.NewTicker(ReconcileInterval)
	defer t.Stop()
	for {
		h.DeletionPass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.delKick:
		}
	}
}

// DeletionPass makes one cleanup attempt for every open deletion whose admission lock is free
// (with one agent status for all of them) and forgets completed deletions older than
// deletionKeep.
func (h *Handler) DeletionPass(ctx context.Context) {
	now := h.clock.Now()
	if err := h.q.ServersPruneDeletions(ctx, sql.NullInt64{Int64: now.Add(-deletionKeep).UnixMilli(), Valid: true}); err != nil {
		h.log.Error("servers: prune deletions", "err", err)
	}
	rows, err := h.q.ServersListOpenDeletions(ctx)
	if err != nil {
		h.log.Error("servers: list open deletions", "err", err)
		return
	}
	var view *agentView
	for _, d := range rows {
		if ctx.Err() != nil {
			return
		}
		unlock, ok := h.admission.TryLock(d.ServerID)
		if !ok {
			continue // the request's own attempt (or a retry) is running
		}
		if view == nil && d.Container == delPending {
			view = h.loadAgentView(ctx)
		}
		d, completed, err := h.attemptDeletion(ctx, d.ServerID, view)
		unlock()
		switch {
		case err != nil && ctx.Err() == nil:
			h.log.Error("servers: deletion attempt", "server_id", d.ServerID, "err", err)
		case completed:
			h.recordDeletionCompleted(ctx, d)
		}
	}
}

// agentView is one answer of the agent for the deletion attempts of a pass.
type agentView struct {
	resp agentapi.InstancesResponse
	snap Snapshot
	err  error
}

func (h *Handler) loadAgentView(ctx context.Context) *agentView {
	resp, snap, err := h.refreshStatus(ctx)
	return &agentView{resp: resp, snap: snap, err: err}
}

// recordDeletionCompleted audits a deletion the panel completed after its request (system).
func (h *Handler) recordDeletionCompleted(ctx context.Context, d store.ServerDeletion) {
	err := h.rec.Record(ctx, audit.Entry{
		Action: audit.ActionServerDeleteCompleted, Target: serverTarget(d.ServerID), Result: audit.ResultSuccess,
		ActorType: audit.ActorSystem, ActorName: string(audit.ActorSystem),
		Details: map[string]any{"name": d.Name, "container": d.Container, "backups": d.Backups, "backups_purged": d.Backups == delPurged},
	})
	if err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", audit.ActionServerDeleteCompleted, "err", err)
	}
}

// attemptDeletion makes one cleanup attempt for deletion id (the caller holds its admission
// lock) with the agent's answer view (nil = asked when needed) and returns the stored result;
// completed reports that this attempt finished the deletion. An error is a database error or
// ctx's (shut down); cleanup failures are stored in the row.
func (h *Handler) attemptDeletion(ctx context.Context, id int64, view *agentView) (d store.ServerDeletion, completed bool, err error) {
	d, err = h.q.ServersGetDeletion(ctx, id)
	if err != nil || d.CompletedAt.Valid {
		return d, false, err
	}
	before := d
	now := h.clock.Now()
	if d.Container == delPending {
		if view == nil {
			view = h.loadAgentView(ctx)
		}
		d.Container, d.ContainerError = h.containerStep(ctx, d, view, now)
	}
	if (d.Backups == delPending || d.Backups == delFailed) && h.dels.due(id, true, now) {
		pctx, cancel := context.WithTimeout(ctx, purgeTimeout)
		err := h.backups.PurgeBackups(pctx, id)
		cancel()
		switch {
		case err == nil:
			d.Backups, d.BackupsError = delPurged, ""
		case ctx.Err() != nil:
			return d, false, ctx.Err() // shut down: nothing learned, nothing stored
		default:
			h.log.Error("servers: deletion: purging the backups failed", "server_id", id, "err", err)
			d.Backups, d.BackupsError = delFailed, "the backups could not be purged; retried later"
		}
	}
	switch st := deletionState(d); {
	case st == deletionDone:
		d.CompletedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
		completed = true
		h.dels.forget(id)
		h.log.Info("servers: deletion complete", "server_id", id, "name", d.Name, "container", d.Container, "backups", d.Backups)
	case (st == deletionFailed || st == deletionPartial) && d.Notified == 0 && now.Sub(time.UnixMilli(d.RequestedAt)) >= deletionAlertAfter:
		d.Notified = 1
		h.notifyDeletionStuck(ctx, d, st)
	}
	if d == before {
		return d, false, nil
	}
	d.UpdatedAt = now.UnixMilli()
	err = h.q.ServersUpdateDeletion(context.WithoutCancel(ctx), store.ServersUpdateDeletionParams{
		Container: d.Container, ContainerError: d.ContainerError, Backups: d.Backups, BackupsError: d.BackupsError,
		Notified: d.Notified, UpdatedAt: d.UpdatedAt, CompletedAt: d.CompletedAt, ServerID: id,
	})
	return d, completed && err == nil, err
}

// notifyDeletionStuck sends server.delete_failed once per deletion (operational queue).
func (h *Handler) notifyDeletionStuck(ctx context.Context, d store.ServerDeletion, state string) {
	msg := fmt.Sprintf("The deletion of %s (server %d) is not complete after %d minutes", d.Name, d.ServerID, int(deletionAlertAfter/time.Minute))
	switch {
	case state == deletionPartial:
		msg += ": its backups could not be purged."
	case d.ContainerError != "":
		msg += ": " + d.ContainerError
	default:
		msg += "."
	}
	msg += " It is retried; see Servers → Deletions, retry or abandon it there."
	h.notifier.Notify(context.WithoutCancel(ctx), notify.Event{
		Type: notify.EventServerDeleteFailed, Title: "Server deletion stuck: " + d.Name, Message: msg, Time: h.clock.Now(),
	})
}

// containerStep advances the container part of d with the agent's answer view: confirmed,
// left alone (foreign), nothing to remove (an agent without containers), or the remove is asked
// for when its retry delay has passed and the agent is not working on the instance.
func (h *Handler) containerStep(ctx context.Context, d store.ServerDeletion, view *agentView, now time.Time) (container, msg string) {
	switch {
	case view.err == nil && view.resp.Disabled:
		return delNone, "" // make dev without Docker: no container can exist
	case errors.Is(view.err, agentapi.ErrDockerUnavailable), view.err == nil && view.resp.Docker != agentapi.DockerOK:
		return d.Container, "Docker is not available on the host; retried later"
	case view.err != nil:
		return d.Container, "the agent is not reachable; retried later"
	}
	c, msg, final := h.removalSeen(d, view.resp, view.snap)
	if final {
		return c, msg
	}
	if st, listed := view.snap.Instances[d.ServerID]; listed && !st.Settled() {
		return d.Container, msg // the agent is working on it: look again on the next pass
	}
	if !h.dels.due(d.ServerID, false, now) {
		return d.Container, msg
	}
	if err := h.inst.Remove(ctx, d.ServerID); err != nil {
		if errors.Is(err, agentapi.ErrBusy) {
			return d.Container, "the agent is busy; retried later"
		}
		h.log.Warn("servers: deletion: remove the container", "server_id", d.ServerID, "err", err)
		return d.Container, "the agent did not accept the remove; retried later"
	}
	h.dels.markIssued(d.ServerID)
	return d.Container, msg
}

// removalSeen decides from the agent's answer whether the container part of d is finished:
// confirmed removed, or a container of another server that is left alone. Otherwise msg is the
// agent's error of the last remove (if one was asked for since the panel started).
func (h *Handler) removalSeen(d store.ServerDeletion, resp agentapi.InstancesResponse, snap Snapshot) (container, msg string, final bool) {
	st, listed := snap.Instances[d.ServerID]
	switch {
	case listed && foreignContainer(st, d.Name):
		h.log.Warn("servers: deletion leaves the container of another server alone", "server_id", d.ServerID, "container_name", st.Name)
		return delForeign, "", true
	case slices.Contains(resp.Removed, d.ServerID) && (!listed || st.State == agentapi.StateMissing):
		return delRemoved, "", true
	case listed && st.Settled() && st.LastError != "" && h.dels.issued(d.ServerID):
		// the last job of the instance is the remove this panel asked for
		return d.Container, st.LastError, false
	}
	return d.Container, d.ContainerError, false
}

// errNameDeleting: the name belongs to a server whose deletion is not complete; its container
// may still be routed under the same address.
var errNameDeleting = errors.New("a server with this name is still being deleted; try again when its deletion is complete")

// nameFree returns errNameDeleting while a deletion of a server called name is open.
func nameFree(ctx context.Context, q *store.Queries, name string) error {
	n, err := q.ServersOpenDeletionByName(ctx, name)
	if err != nil {
		return err
	}
	if n > 0 {
		return errNameDeleting
	}
	return nil
}

// handleListDeletions answers GET /servers/deletions: the open deletions and those completed
// within deletionKeep, newest first (admins).
func (h *Handler) handleListDeletions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ServersListDeletions(r.Context(), sql.NullInt64{Int64: h.clock.Now().Add(-deletionKeep).UnixMilli(), Valid: true})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]deletionJSON, 0, len(rows))
	for _, d := range rows {
		out = append(out, toDeletionJSON(d))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// handleRetryDeletion answers POST /servers/deletions/{id}/retry: the next attempt of an open
// deletion runs right away (admins, sudo: it may purge backups; audited).
func (h *Handler) handleRetryDeletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, unlock, ok := h.openDeletion(w, r)
	if !ok {
		return
	}
	defer unlock()
	h.dels.reset(d.ServerID)
	ctx = context.WithoutCancel(ctx)
	if d.Backups == delPending || d.Backups == delFailed {
		h.extendWrite(w, r, purgeTimeout+purgeAnswerSlack)
	}
	d, completed, err := h.attemptDeletion(ctx, d.ServerID, nil)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerDeleteRetry, serverTarget(d.ServerID), audit.ResultSuccess, map[string]any{
		"name": d.Name, "state": deletionState(d), "container": d.Container, "backups": d.Backups,
	})
	if completed {
		h.recordDeletionCompleted(ctx, d)
	}
	h.KickDeletions()
	httpx.WriteJSON(w, http.StatusOK, toDeletionJSON(d))
}

// handleAbandonDeletion answers POST /servers/deletions/{id}/abandon (admins, sudo; audited):
// an open deletion that cannot finish (an unmanaged container under the server's name, data
// that cannot be archived) is closed as abandoned and its name is free again. Nothing is
// removed: the leftovers are the operator's (docs/install.md, "A server deletion does not
// finish").
func (h *Handler) handleAbandonDeletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, unlock, ok := h.openDeletion(w, r)
	if !ok {
		return
	}
	defer unlock()
	now := h.clock.Now().UnixMilli()
	n, err := h.q.ServersAbandonDeletion(ctx, store.ServersAbandonDeletionParams{
		UpdatedAt: now, CompletedAt: sql.NullInt64{Int64: now, Valid: true}, ServerID: d.ServerID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		writeConflict(w, r, "the deletion is complete")
		return
	}
	h.dels.forget(d.ServerID)
	h.log.Warn("servers: deletion abandoned, leftovers stay on the host", "server_id", d.ServerID, "name", d.Name,
		"container", d.Container, "backups", d.Backups)
	h.record(ctx, audit.ActionServerDeleteAbandon, serverTarget(d.ServerID), audit.ResultSuccess, map[string]any{
		"name": d.Name, "container": d.Container, "container_error": d.ContainerError, "backups": d.Backups,
	})
	if d, err = h.q.ServersGetDeletion(ctx, d.ServerID); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDeletionJSON(d))
}

// openDeletion loads the open deletion of the {id} path parameter and takes its admission lock;
// on failure it has written the answer (404 unknown, 409 complete or another attempt runs).
func (h *Handler) openDeletion(w http.ResponseWriter, r *http.Request) (store.ServerDeletion, func(), bool) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "deletion not found")
		return store.ServerDeletion{}, nil, false
	}
	unlock, err := h.admission.Lock(ctx, id)
	if err != nil {
		if !WriteDataStateError(w, r, err) {
			h.log.DebugContext(ctx, "servers: deletion request ended with the request", "err", err)
		}
		return store.ServerDeletion{}, nil, false
	}
	d, err := h.q.ServersGetDeletion(ctx, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "deletion not found")
	case err != nil:
		httpx.Internal(w, r, err)
	case d.CompletedAt.Valid:
		writeConflict(w, r, "the deletion is complete")
	default:
		return d, unlock, true
	}
	unlock()
	return store.ServerDeletion{}, nil, false
}
