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
	"github.com/Bernotese/mc-panel/internal/servers/admit"
)

// Holding reports a restore or dev sync job (keyed by the dev) of the server that waits or
// runs, from the 202 to the end of the service's restore (servers.Hold; spec §5.5): the data
// state is busy before the service knows the job. The service's own restore job is part of
// servers.DataStates, also across a panel restart.
func (h *Handler) Holding(serverID int64) (kind string, ok bool) {
	return h.queue.restoreHeld(serverID)
}

// admit runs fn under the admission of server id (Deps.Admit; the queue's own hold left out)
// and turns a refused admission into a job error.
func (h *Handler) admit(ctx context.Context, id int64, fn func(context.Context) error) error {
	if h.admitFn == nil {
		return fn(ctx)
	}
	err := h.admitFn(ctx, id, fn)
	switch {
	case errors.Is(err, admit.ErrAdmissionBusy):
		return failf(codeFilesBusy, "another operation on this server is being admitted, try again")
	case errors.Is(err, admit.ErrDataBusy):
		return failf(codeFilesBusy, "a file job is rewriting the server's data")
	case errors.Is(err, admit.ErrRecoveryRequired):
		return failf(codeFilesBusy, "a restore of this server could not be completed; resolve it first")
	case errors.Is(err, admit.ErrDeleted):
		return errServerDeleted
	case errors.Is(err, admit.ErrDataStateUnknown):
		return failf(codeFiles, "the files service did not report whether the server's data is safe")
	}
	return err
}

// stopSettled stops the server through the agent (desired_state stays; the data state keeps
// the reconcile loop from starting it) and waits until it does not run and the agent has
// nothing running or queued for it (data safety spec §2.2): a start the agent queued before the
// admission runs before the data job, never after it. A stop is sent whenever the container
// runs while the agent is idle for it.
func (h *Handler) stopSettled(ctx context.Context, id int64) error {
	if h.inst == nil {
		return failf(codeAgent, "no agent: the server cannot be stopped")
	}
	sctx, cancel := context.WithTimeout(ctx, h.stopTimeout)
	defer cancel()
	sent := false
	for first := true; ; first = false {
		st, err := h.instanceStatus(sctx, id)
		switch {
		case err != nil && first:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			h.log.WarnContext(ctx, "backups: read the server state", "server_id", id, "err", err)
			return stateError(err)
		case err != nil:
			// only an answer with Docker ok counts; errors (also Docker briefly not reachable)
			// keep polling until the stop timeout
		case !isRunning(st.State) && st.Settled():
			return nil
		case !isRunning(st.State):
			sent = false // a queued start may run next: stop it again once it is up
		case st.Settled() && !sent:
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
			sent = true
		}
		select {
		case <-sctx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return failf(codeStopTimeout, "the server did not stop within %s", h.stopTimeout)
		case <-time.After(h.pollEvery):
		}
	}
}

// runRestore is the restore flow of spec §5.5; the queue holds the server (busy) while it runs.
func (h *Handler) runRestore(ctx context.Context, j *job, snapshot string) (err error) {
	defer func() { err = canceledOr(ctx, err) }()
	if h.files == nil {
		return errNoFiles
	}
	id := j.spec.serverID
	if err := h.step(ctx, j, stepStopping); err != nil {
		return err
	}
	// Admitted twice (data safety spec §3): the server is stopped and settled before its
	// pre-restore snapshot, and again right before the service takes the restore job.
	if err := h.admit(ctx, id, func(ctx context.Context) error { return h.stopSettled(ctx, id) }); err != nil {
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
	var rj filesapi.Job
	err = h.admit(ctx, id, func(ctx context.Context) error {
		if err := h.stopSettled(ctx, id); err != nil {
			return err
		}
		rj, err = h.files.RestoreBackup(ctx, id, snapshot)
		return err
	})
	if err != nil {
		return err
	}
	// the queue's hold stays until a poll shows that the service's restore ended
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
	// The job is admitted again when it runs; this answers a server that cannot be restored
	// now (another job rewrites it, a restore is unresolved, the state is unknown) at once.
	if h.admitFn != nil {
		err := h.admitFn(ctx, srv.ID, func(context.Context) error { return nil })
		if admit.WriteError(w, r, err) {
			return
		}
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
