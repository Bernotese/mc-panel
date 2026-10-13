package backups

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

const (
	// purgeWait bounds the wait for the server's panel job after its cancel.
	purgeWait = 15 * time.Second
	// purgeJobWait bounds the wait until the service no longer runs a job of the server.
	purgeJobWait = 30 * time.Second
)

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	if err := h.ready(ctx, srv, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	s, err := h.findSnapshot(ctx, srv.ID, snap)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	v, err := h.queue.add(ctx, jobSpec{
		serverID: srv.ID, name: srv.Name, kind: kindDelete, trigger: triggerManual, snapshot: snap,
		run: func(ctx context.Context, j *job) (string, error) {
			if err := h.step(ctx, j, stepDeleting); err != nil {
				return snap, err
			}
			sj, err := h.files.ForgetBackups(ctx, filesapi.ForgetRequest{ID: srv.ID, Snapshots: []string{snap}})
			if err == nil {
				_, err = h.waitService(ctx, j, srv.ID, sj.ID, false)
			}
			err = canceledOr(ctx, err)
			h.record(ctx, audit.ActionBackupDelete, srv.ID, result(err), map[string]any{"snapshot": snap, "kind": string(s.Kind)})
			return snap, err
		},
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, jobResponse{Job: &v})
}

// pinRequest is the OpenAPI schema PinBackupRequest.
type pinRequest struct {
	Pinned *bool `json:"pinned"`
}

func (h *Handler) handlePin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	var req pinRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Pinned == nil {
		writeValidation(w, r, "pinned is required")
		return
	}
	if err := h.ready(ctx, srv, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	if h.queue.busy(srv.ID) {
		h.writeError(w, r, errBackupRunning)
		return
	}
	s, err := h.files.PinBackup(ctx, srv.ID, snap, *req.Pinned)
	action := audit.ActionBackupPin
	if !*req.Pinned {
		action = audit.ActionBackupUnpin
	}
	if err != nil {
		if !rejected(err) {
			h.record(ctx, action, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
		}
		h.writeError(w, r, err)
		return
	}
	h.record(ctx, action, srv.ID, audit.ResultSuccess, map[string]any{"snapshot": snap, "new_snapshot": s.ID})
	httpx.WriteJSON(w, http.StatusOK, snapshotResponse{Snapshot: viewSnapshot(s)})
}

// PurgeBackups ends the server's jobs (a waiting one is dropped, a running one cancelled and
// awaited), waits until the service runs no job of the server any more and deletes its
// repository (servers.BackupPurger, spec §5.6). Should the service still report busy, the
// purge is retried a few times. The waiting before the service call is bounded (purgeWait +
// purgeJobWait + 5 × pollEvery ≈ 55 s): servers' DELETE gives the whole purge
// filesapi.DeleteTimeout + 1 min (context and write deadline, V2).
func (h *Handler) PurgeBackups(ctx context.Context, serverID int64) error {
	h.queue.drop(serverID, purgeWait)
	if h.files == nil {
		return nil
	}
	h.waitServiceIdle(ctx, serverID, purgeJobWait)
	var err error
	for range 5 {
		if err = h.files.PurgeBackups(ctx, serverID); !errors.Is(err, filesapi.ErrBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.pollEvery):
		}
	}
	return err
}

// waitServiceIdle polls the service until it reports no running job of the server, at most
// wait (a cancelled job may take a moment to end; poll errors count as still running).
func (h *Handler) waitServiceIdle(ctx context.Context, serverID int64, wait time.Duration) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		if sj, err := h.files.Job(ctx, serverID); err == nil && (sj == nil || sj.State != filesapi.JobRunning) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			h.log.WarnContext(ctx, "backups: the service still runs a job of the server, purging anyway", "server_id", serverID)
			return
		case <-time.After(h.pollEvery):
		}
	}
}
