package backups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// ready checks what a job needs before it is queued: an instance (if needInstance), a file
// service and restic on the host.
func (h *Handler) ready(ctx context.Context, srv server, needInstance bool) error {
	if needInstance && !srv.HasInstance {
		return errNoInstance
	}
	if h.files == nil {
		return errNoFiles
	}
	st, err := h.files.BackupsStatus(ctx)
	if err != nil {
		return err
	}
	if !st.Available {
		return fmt.Errorf("%w: %s", errNotAvailable, st.Reason)
	}
	return nil
}

// serverGone reports whether the server row no longer exists (deleted while its job waited or
// ran). A failing lookup counts as present: the next step fails on its own if the DB is gone.
func (h *Handler) serverGone(parent context.Context, id int64) bool {
	// also after a cancel (a purge drops the job of a server it deletes)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), cleanupTimeout)
	defer cancel()
	_, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil {
		h.log.WarnContext(ctx, "backups: look up the server of a job", "server_id", id, "err", err)
	}
	return false
}

// deletedOr turns a failure of a job whose server is gone meanwhile into errServerDeleted
// (no audit entry, no notification); other results stay.
func (h *Handler) deletedOr(ctx context.Context, id int64, err error) error {
	if err == nil || errors.Is(err, errServerDeleted) {
		return err
	}
	if h.serverGone(ctx, id) {
		return errServerDeleted
	}
	return err
}

// step moves j to step unless its server was deleted meanwhile (errServerDeleted). A service
// job that already runs is not checked: it may finish for a deleted server.
func (h *Handler) step(ctx context.Context, j *job, step string) error {
	if h.serverGone(ctx, j.spec.serverID) {
		return errServerDeleted
	}
	j.setStep(step)
	return nil
}

// instanceState asks the agent for the container state; no agent client counts as stopped.
// Docker not ok is always an error, never "stopped": the agent also reports it when dockerd
// hangs while the containers keep running.
func (h *Handler) instanceState(ctx context.Context, id int64) (agentapi.ContainerState, error) {
	if h.inst == nil {
		return agentapi.StateMissing, nil
	}
	resp, err := h.inst.Instances(ctx)
	if err != nil {
		return "", err
	}
	if resp.Docker != agentapi.DockerOK {
		return "", agentapi.ErrDockerUnavailable
	}
	for _, s := range resp.Instances {
		if s.ID == id {
			return s.State, nil
		}
	}
	return agentapi.StateMissing, nil
}

func isRunning(s agentapi.ContainerState) bool {
	return s == agentapi.StateRunning || s == agentapi.StateRestarting
}

// containerRunning reports whether the server runs; an unknown state fails the job (a
// snapshot of a running server without save-off could be inconsistent).
func (h *Handler) containerRunning(ctx context.Context, id int64) (bool, error) {
	st, err := h.instanceState(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		h.log.WarnContext(ctx, "backups: read the server state", "server_id", id, "err", err)
		return false, stateError(err)
	}
	return isRunning(st), nil
}

// stateError is the job error of an unknown server state (fixed texts: the agent's errors may
// name its socket path).
func stateError(err error) error {
	if errors.Is(err, agentapi.ErrDockerUnavailable) {
		return failf(codeAgent, "Docker is not reachable")
	}
	return failf(codeAgent, "the agent is not reachable, the server state is unknown")
}

// sendRcon runs one console command with the agent-side timeout rconTimeout (60 s: save-off
// and save-all flush of a large world take longer than the agent's default of 10 s).
func (h *Handler) sendRcon(ctx context.Context, id int64, cmd string) error {
	ctx, cancel := context.WithTimeout(ctx, h.rconTimeout+rconMargin)
	defer cancel()
	var err error
	if h.rconT != nil {
		_, err = h.rconT.RconTimeout(ctx, id, cmd, h.rconTimeout)
	} else {
		_, err = h.console.Rcon(ctx, id, cmd)
	}
	return err
}

// rcon sends cmd for a job step. The job error has a fixed text (the agent's error may name
// its socket path); the original error goes to the log.
func (h *Handler) rcon(ctx context.Context, id int64, cmd string) error {
	err := h.sendRcon(ctx, id, cmd)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	h.log.WarnContext(ctx, "backups: RCON command failed", "server_id", id, "command", cmd, "err", err)
	if errors.Is(err, agentapi.ErrUnavailable) {
		return failf(codeAgent, "the agent is not reachable")
	}
	return failf(codeRcon, "RCON command %s failed", cmd)
}

// markSaveOff records that the panel is about to send save-off (spec §5.4, crash recovery).
func (h *Handler) markSaveOff(ctx context.Context, id int64) error {
	if _, err := h.loadSchedule(ctx, id); err != nil {
		return err
	}
	return h.q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: nullTime(h.clock.Now()), ServerID: id})
}

// resume sends save-on. It clears saveoff_since once the server saves again or is no longer
// running; otherwise the mark stays and the ticker retries (Review Focus 1). A job passes a
// context without its cancel (save-on must follow a cancelled job too); the ticker and the
// recovery pass Run's context, so a hanging agent does not delay the shutdown (the mark stays).
func (h *Handler) resume(parent context.Context, id int64) bool {
	err := h.sendRcon(parent, id, "save-on")
	if err != nil && !errors.Is(err, agentapi.ErrNotRunning) && !errors.Is(err, agentapi.ErrNotFound) {
		if parent.Err() != nil {
			h.log.InfoContext(parent, "backups: save-on interrupted by the shutdown, the next start retries", "server_id", id)
			return false
		}
		h.log.WarnContext(parent, "backups: save-on failed, the next tick retries", "server_id", id, "err", err)
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), cleanupTimeout)
	defer cancel()
	if err := h.q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{ServerID: id}); err != nil {
		h.log.ErrorContext(ctx, "backups: clear saveoff_since", "server_id", id, "err", err)
		return false
	}
	return true
}

// cancelService asks the service to cancel the server's running job (best effort, bounded by
// cleanupTimeout and parent).
func (h *Handler) cancelService(parent context.Context, id int64) {
	if h.files == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, cleanupTimeout)
	defer cancel()
	if err := h.files.CancelJob(ctx, id); err != nil {
		h.log.WarnContext(ctx, "backups: cancel the service job", "server_id", id, "err", err)
	}
}

// waitMode says how waitService watches a service job.
type waitMode struct {
	// stall: the job reports byte progress (backup, pre-restore, restore); no change for
	// stallTimeout cancels and fails it.
	stall bool
	// limit > 0 bounds the whole job instead (forget reports no bytes).
	limit time.Duration
	// untilEnd: the restoring step, which holds the start gate. Poll errors never end the wait
	// and a cancelled job is waited for until a successful poll shows its end, so the gate does
	// not open while the service may still swap the data directory.
	untilEnd bool
}

var (
	waitBackup  = waitMode{stall: true}
	waitRestore = waitMode{stall: true, untilEnd: true}
	waitForget  = waitMode{limit: forgetTimeout}
)

// waitService polls the service job jobID of the server until it ended; progress goes to j.
// A cancelled ctx cancels the service job. Stall watchdog and overall limit (h.clock) follow
// m: on expiry the job is cancelled and fails (with m.untilEnd only once its end is seen).
func (h *Handler) waitService(ctx context.Context, j *job, id int64, jobID string, m waitMode) (filesapi.Job, error) {
	if h.files == nil {
		return filesapi.Job{}, errNoFiles
	}
	untilEnd := m.untilEnd
	started := h.clock.Now()
	misses := 0
	lastBytes, lastChange := int64(0), h.clock.Now() // a new job starts at 0 bytes
	stalled, overMsg := false, ""                    // stalled: cancelled for the watchdog or the limit
	for {
		select {
		case <-ctx.Done():
			h.cancelService(context.WithoutCancel(ctx), id)
			return filesapi.Job{}, ctx.Err()
		case <-time.After(h.pollEvery):
		}
		sj, err := h.files.Job(ctx, id)
		switch {
		case err != nil && ctx.Err() != nil:
			continue // the select above cancels
		case err != nil && !untilEnd:
			if misses++; misses >= maxPollMisses {
				h.cancelService(ctx, id)
				return filesapi.Job{}, fmt.Errorf("poll the service job: %w", err)
			}
		case err != nil:
			// untilEnd: keep polling, the gate stays closed until the end is seen
		case sj == nil || sj.ID != jobID:
			return filesapi.Job{}, failf(codeFiles, "the file service lost the job (was it restarted?)")
		default:
			misses = 0
			if sj.Bytes != lastBytes {
				lastBytes, lastChange = sj.Bytes, h.clock.Now()
			}
			j.setBytes(sj.Bytes)
			switch sj.State {
			case filesapi.JobDone:
				return *sj, nil
			case filesapi.JobFailed:
				if stalled {
					return *sj, failf(codeInternal, "%s", overMsg)
				}
				return *sj, failf(serviceJobCode(sj.Code), "%s", sj.Error)
			}
		}
		now := h.clock.Now()
		over := ""
		switch {
		case stalled:
		case m.stall && now.Sub(lastChange) >= stallTimeout:
			over = msgStalled
		case m.limit > 0 && now.Sub(started) >= m.limit:
			over = msgForgetTimeout
		}
		if over != "" {
			h.log.WarnContext(ctx, "backups: cancelling the service job", "server_id", id, "job", jobID, "reason", over)
			h.cancelService(ctx, id)
			if !untilEnd {
				return filesapi.Job{}, failf(codeInternal, "%s", over)
			}
			stalled, overMsg = true, over
		}
	}
}

// serviceJobCode maps the wire code of a failed service job (filesapi.Job.Code) to a
// BackupJobError (V5); unknown or empty codes are internal.
func serviceJobCode(code string) string {
	switch code {
	case filesapi.CodeTooLarge:
		return codeTooLarge
	case filesapi.CodeInsufficientStorage:
		return codeStorage
	case filesapi.CodeBusy:
		return codeFilesBusy
	case filesapi.CodeUnavailable:
		return codeBackups
	}
	return codeInternal
}

// runBackup takes one snapshot of kind; a running server gets save-off/save-all flush before
// and save-on after it, in every path (spec §5.4).
func (h *Handler) runBackup(ctx context.Context, j *job, kind filesapi.SnapshotKind, note string) (snapshot string, err error) {
	defer func() { err = canceledOr(ctx, err) }() // runs last: after save-on
	if h.files == nil {
		return "", errNoFiles
	}
	id := j.spec.serverID
	running, err := h.containerRunning(ctx, id)
	if err != nil {
		return "", err
	}
	if running {
		if err := h.step(ctx, j, stepSaving); err != nil {
			return "", err
		}
		// Held from the mark to save-on: a tick that retries save-on skips the server meanwhile.
		lock := h.saveLocks.get(id)
		lock.Lock()
		defer lock.Unlock()
		if err := h.markSaveOff(ctx, id); err != nil {
			return "", err
		}
		defer func() {
			j.setStep(stepResuming)
			h.resume(context.WithoutCancel(ctx), id)
		}()
		if err := h.rcon(ctx, id, "save-off"); err != nil {
			return "", err
		}
		if err := h.rcon(ctx, id, "save-all flush"); err != nil {
			return "", err
		}
	}
	if err := h.step(ctx, j, stepBackingUp); err != nil {
		return "", err
	}
	sj, err := h.files.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: id, Kind: kind, Note: note, Timezone: h.tzName(ctx)})
	if err != nil {
		return "", err
	}
	done, err := h.waitService(ctx, j, id, sj.ID, waitBackup)
	if err != nil {
		return "", err
	}
	if done.Snapshot == "" {
		return "", failf(codeInternal, "the file service reported no snapshot")
	}
	return done.Snapshot, nil
}

// createRequest is the OpenAPI schema CreateBackupRequest.
type createRequest struct {
	Note *string `json:"note"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	note := ""
	if req.Note != nil {
		note = *req.Note
	}
	if !filesapi.ValidNote(note) {
		writeValidation(w, r, "note: at most 64 characters, no control characters, not only spaces")
		return
	}
	if err := h.ready(ctx, srv, true); err != nil {
		h.writeError(w, r, err)
		return
	}
	v, err := h.queue.add(ctx, jobSpec{
		serverID: srv.ID, name: srv.Name, kind: kindBackup, trigger: triggerManual,
		run: func(ctx context.Context, j *job) (string, error) {
			snap, err := h.runBackup(ctx, j, filesapi.SnapshotManual, note)
			if err = h.deletedOr(ctx, srv.ID, err); errors.Is(err, errServerDeleted) {
				return "", err
			}
			details := map[string]any{"note": nil, "snapshot": nil}
			if note != "" {
				details["note"] = note
			}
			if snap != "" {
				details["snapshot"] = snap
			}
			h.record(ctx, audit.ActionBackupCreate, srv.ID, result(err), details)
			return snap, err
		},
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, jobResponse{Job: &v})
}

func (h *Handler) handleJob(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, jobResponse{Job: h.queue.get(srv.ID)})
}
