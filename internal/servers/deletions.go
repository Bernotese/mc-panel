package servers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
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
	deletionPending = "pending" // cleanup in progress
	deletionFailed  = "failed"  // the container could not be removed yet; retried
	deletionPartial = "partial" // the server is gone, but its backups could not be purged; retried
	deletionDone    = "done"
)

const (
	// deletionKeep is how long completed deletions stay listed.
	deletionKeep = 7 * 24 * time.Hour
	// purgeRetry is the first delay before a failed purge is tried again (doubling, at most
	// purgeRetryMax); the container is asked again after ReconcileInterval (doubling, at most
	// maxBackoff).
	purgeRetry    = time.Minute
	purgeRetryMax = time.Hour
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

// asked reports whether a remove of id was asked for since the panel started.
func (d *deletionRetries) asked(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.get(id).removes > 0
}

// unbook takes back the remove attempt due just booked: nothing was asked for.
func (d *deletionRetries) unbook(id int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := d.m[id]; r != nil && r.removes > 0 {
		r.removes--
		r.removeAt = time.Time{}
	}
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
// and forgets completed deletions older than deletionKeep.
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
	for _, d := range rows {
		if ctx.Err() != nil {
			return
		}
		unlock, ok := h.admission.TryLock(d.ServerID)
		if !ok {
			continue // the request's own attempt (or a retry) is running
		}
		if _, err := h.attemptDeletion(ctx, d.ServerID); err != nil && ctx.Err() == nil {
			h.log.Error("servers: deletion attempt", "server_id", d.ServerID, "err", err)
		}
		unlock()
	}
}

// attemptDeletion makes one cleanup attempt for deletion id (the caller holds its admission
// lock) and returns the stored result. An error is a database error or ctx's (shut down);
// cleanup failures are stored in the row.
func (h *Handler) attemptDeletion(ctx context.Context, id int64) (store.ServerDeletion, error) {
	d, err := h.q.ServersGetDeletion(ctx, id)
	if err != nil || d.CompletedAt.Valid {
		return d, err
	}
	before := d
	now := h.clock.Now()
	if d.Container == delPending && h.dels.due(id, false, now) {
		d.Container, d.ContainerError = h.removeContainer(ctx, d)
	} else if d.Container == delPending {
		d.Container, d.ContainerError = h.checkRemoved(ctx, d)
	}
	if (d.Backups == delPending || d.Backups == delFailed) && h.dels.due(id, true, now) {
		pctx, cancel := context.WithTimeout(ctx, purgeTimeout)
		err := h.backups.PurgeBackups(pctx, id)
		cancel()
		switch {
		case err == nil:
			d.Backups, d.BackupsError = delPurged, ""
		case ctx.Err() != nil:
			return d, ctx.Err() // shut down: nothing learned, nothing stored
		default:
			h.log.Error("servers: deletion: purging the backups failed", "server_id", id, "err", err)
			d.Backups, d.BackupsError = delFailed, "the backups could not be purged; retried later"
		}
	}
	if deletionState(d) == deletionDone {
		d.CompletedAt = sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true}
		h.dels.forget(id)
		h.log.Info("servers: deletion complete", "server_id", id, "name", d.Name, "container", d.Container, "backups", d.Backups)
	}
	if d == before {
		return d, nil
	}
	d.UpdatedAt = h.clock.Now().UnixMilli()
	err = h.q.ServersUpdateDeletion(context.WithoutCancel(ctx), store.ServersUpdateDeletionParams{
		Container: d.Container, ContainerError: d.ContainerError, Backups: d.Backups, BackupsError: d.BackupsError,
		UpdatedAt: d.UpdatedAt, CompletedAt: d.CompletedAt, ServerID: id,
	})
	return d, err
}

// checkRemoved looks for the agent's confirmation of a remove asked for earlier, without asking
// again (the retry delay has not passed).
func (h *Handler) checkRemoved(ctx context.Context, d store.ServerDeletion) (container, msg string) {
	resp, snap, err := h.refreshStatus(ctx)
	if err != nil || resp.Docker != agentapi.DockerOK {
		return d.Container, d.ContainerError
	}
	c, msg, _ := h.removalSeen(d, resp, snap)
	return c, msg
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
	case listed && st.LastError != "" && h.dels.asked(d.ServerID):
		return d.Container, st.LastError, false
	}
	return d.Container, d.ContainerError, false
}

// removeContainer asks the agent to remove the container of d unless it confirmed the removal
// already or is still working on the instance. The remove is idempotent: a missing container
// only has its data archived (if any is left).
func (h *Handler) removeContainer(ctx context.Context, d store.ServerDeletion) (container, msg string) {
	resp, snap, err := h.refreshStatus(ctx)
	switch {
	case err == nil && resp.Docker != agentapi.DockerOK, errors.Is(err, agentapi.ErrDockerUnavailable):
		return d.Container, "Docker is not available on the host; retried later"
	case err != nil:
		return d.Container, "the agent is not reachable; retried later"
	}
	c, msg, final := h.removalSeen(d, resp, snap)
	if final {
		return c, msg
	}
	if st, listed := snap.Instances[d.ServerID]; listed && !st.Settled() {
		h.dels.unbook(d.ServerID) // the agent is working on it: look again on the next pass
		return d.Container, msg
	}
	if err := h.inst.Remove(ctx, d.ServerID); err != nil {
		if errors.Is(err, agentapi.ErrBusy) {
			return d.Container, "the agent is busy; retried later"
		}
		h.log.Warn("servers: deletion: remove the container", "server_id", d.ServerID, "err", err)
		return d.Container, "the agent did not accept the remove; retried later"
	}
	return d.Container, msg
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
// deletion runs right away (admins, sudo: it may purge backups).
func (h *Handler) handleRetryDeletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "deletion not found")
		return
	}
	d, err := h.q.ServersGetDeletion(ctx, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "deletion not found")
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	case d.CompletedAt.Valid:
		writeConflict(w, r, "the deletion is complete")
		return
	}
	unlock, err := h.admission.Lock(ctx, id)
	if err != nil {
		if !WriteDataStateError(w, r, err) {
			h.log.DebugContext(ctx, "servers: deletion retry ended with the request", "err", err)
		}
		return
	}
	defer unlock()
	h.dels.reset(id)
	ctx = context.WithoutCancel(ctx)
	if d.Backups == delPending || d.Backups == delFailed {
		h.extendWrite(w, r, purgeTimeout+purgeAnswerSlack)
	}
	if d, err = h.attemptDeletion(ctx, id); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.KickDeletions()
	httpx.WriteJSON(w, http.StatusOK, toDeletionJSON(d))
}
