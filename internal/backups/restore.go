package backups

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// DataJobRunning is true while the panel holds a restore job for the server, from the 202 (also
// while it waits behind another server's job) to the end of the service's restore
// (servers.DataGate; spec §5.5). The service's own restore job is covered by
// serverfiles.DataJobRunning, also across a panel restart.
func (h *Handler) DataJobRunning(_ context.Context, serverID int64) bool {
	return h.queue.restoreHeld(serverID)
}

// stop stops a running server through the agent (desired_state stays; the gate keeps the
// reconcile loop from starting it) and waits until it no longer runs.
func (h *Handler) stop(ctx context.Context, id int64) error {
	if h.inst == nil {
		return failf(codeAgent, "no agent: the server cannot be stopped")
	}
	st, err := h.instanceState(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h.log.WarnContext(ctx, "backups: read the server state", "server_id", id, "err", err)
		return stateError(err)
	}
	if !isRunning(st) {
		return nil
	}
	if err := h.inst.Power(ctx, id, agentapi.PowerStop); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// the agent's error may name its socket path: log it, show a fixed text
		h.log.WarnContext(ctx, "backups: stop the server for a restore", "server_id", id, "err", err)
		if errors.Is(err, agentapi.ErrUnavailable) {
			return failf(codeAgent, "the agent is not reachable")
		}
		return failf(codeAgent, "stopping the server failed")
	}
	sctx, cancel := context.WithTimeout(ctx, h.stopTimeout)
	defer cancel()
	for {
		select {
		case <-sctx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return failf(codeStopTimeout, "the server did not stop within %s", h.stopTimeout)
		case <-time.After(h.pollEvery):
		}
		// only an answer with Docker ok counts as stopped; errors (also Docker briefly not
		// reachable) keep polling until the stop timeout
		if st, err := h.instanceState(sctx, id); err == nil && !isRunning(st) {
			return nil
		}
	}
}

// runRestore is the restore flow of spec §5.5; the queue holds the gate while it runs.
func (h *Handler) runRestore(ctx context.Context, j *job, snapshot string) (err error) {
	defer func() { err = canceledOr(ctx, err) }()
	if h.files == nil {
		return errNoFiles
	}
	id := j.spec.serverID
	if err := h.step(ctx, j, stepStopping); err != nil {
		return err
	}
	if err := h.stop(ctx, id); err != nil {
		return err
	}
	if err := h.step(ctx, j, stepPreRestore); err != nil {
		return err
	}
	pre, err := h.files.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: id, Kind: filesapi.SnapshotPreRestore, Timezone: h.tzName(ctx)})
	if err != nil {
		return err
	}
	if _, err := h.waitService(ctx, j, id, pre.ID, h.waitBackup()); err != nil {
		return err
	}
	if err := h.step(ctx, j, stepRestoring); err != nil {
		return err
	}
	rj, err := h.files.RestoreBackup(ctx, id, snapshot)
	if err != nil {
		return err
	}
	// untilEnd: the gate stays closed until a poll shows that the service's restore ended
	_, err = h.waitService(ctx, j, id, rj.ID, waitRestore)
	return err
}

func (h *Handler) handleRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	if err := h.ready(ctx, srv, true); err != nil {
		h.writeError(w, r, err)
		return
	}
	if h.queue.busy(srv.ID) {
		h.writeError(w, r, ErrBackupRunning)
		return
	}
	s, err := h.findSnapshot(ctx, srv.ID, snap)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	v, err := h.queue.add(ctx, jobSpec{
		serverID: srv.ID, name: srv.Name, kind: kindRestore, trigger: triggerManual, snapshot: snap,
		after: h.reconcile, // the gate is open again: start the server if it should run
		run: func(ctx context.Context, j *job) (string, error) {
			err := h.deletedOr(ctx, srv.ID, h.runRestore(ctx, j, snap))
			if errors.Is(err, errServerDeleted) {
				return snap, err
			}
			h.record(ctx, audit.ActionBackupRestore, srv.ID, result(err), map[string]any{"snapshot": snap, "time": s.Time, "kind": string(s.Kind)})
			switch {
			case err == nil:
				h.alertOK(srv.ID, alertRestore)
			case ctx.Err() == nil:
				_, msg := jobErrorOf(err)
				h.alert(ctx, srv.ID, srv.Name, alertRestore, msg)
			}
			return snap, err
		},
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, jobResponse{Job: &v})
}
