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
func (h *Handler) serverGone(ctx context.Context, id int64) bool {
	_, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil && ctx.Err() == nil {
		h.log.WarnContext(ctx, "backups: look up the server of a job", "server_id", id, "err", err)
	}
	return false
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

// instanceState asks the agent for the container state; no agent counts as stopped.
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
		return false, failf(codeAgent, "the agent is not reachable, the server state is unknown")
	}
	return isRunning(st), nil
}

func (h *Handler) rcon(ctx context.Context, id int64, cmd string) error {
	ctx, cancel := context.WithTimeout(ctx, h.rconTimeout)
	defer cancel()
	if _, err := h.console.Rcon(ctx, id, cmd); err != nil {
		return failf(codeRcon, "RCON command %q failed: %v", cmd, err)
	}
	return nil
}

// markSaveOff records that the panel is about to send save-off (spec §5.4, crash recovery).
func (h *Handler) markSaveOff(ctx context.Context, id int64) error {
	if _, err := h.loadSchedule(ctx, id); err != nil {
		return err
	}
	return h.q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: nullTime(h.clock.Now()), ServerID: id})
}

// resume sends save-on. It clears saveoff_since once the server saves again or is no longer
// running; otherwise the mark stays and the ticker retries (Review Focus 1). It never uses a
// job's context: it must also run after a cancel.
func (h *Handler) resume(id int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), h.rconTimeout)
	defer cancel()
	_, err := h.console.Rcon(ctx, id, "save-on")
	if err != nil && !errors.Is(err, agentapi.ErrNotRunning) && !errors.Is(err, agentapi.ErrNotFound) {
		h.log.WarnContext(ctx, "backups: save-on failed, the next tick retries", "server_id", id, "err", err)
		return false
	}
	if err := h.q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{ServerID: id}); err != nil {
		h.log.ErrorContext(ctx, "backups: clear saveoff_since", "server_id", id, "err", err)
		return false
	}
	return true
}

// cancelService asks the service to cancel the server's running job (best effort).
func (h *Handler) cancelService(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := h.files.CancelJob(ctx, id); err != nil {
		h.log.WarnContext(ctx, "backups: cancel the service job", "server_id", id, "err", err)
	}
}

// waitService polls the service job jobID of the server until it ended; progress goes to j.
// A cancelled ctx cancels the service job.
func (h *Handler) waitService(ctx context.Context, j *job, id int64, jobID string) (filesapi.Job, error) {
	misses := 0
	for {
		select {
		case <-ctx.Done():
			h.cancelService(id)
			return filesapi.Job{}, ctx.Err()
		case <-time.After(h.pollEvery):
		}
		sj, err := h.files.Job(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				continue // the select above cancels
			}
			if misses++; misses >= maxPollMisses {
				h.cancelService(id)
				return filesapi.Job{}, fmt.Errorf("poll the service job: %w", err)
			}
			continue
		}
		misses = 0
		if sj == nil || sj.ID != jobID {
			return filesapi.Job{}, failf(codeFiles, "the file service lost the job (was it restarted?)")
		}
		j.setBytes(sj.Bytes)
		switch sj.State {
		case filesapi.JobDone:
			return *sj, nil
		case filesapi.JobFailed:
			return *sj, failf(serviceJobCode(sj.Code), "%s", sj.Error)
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
	id := j.spec.serverID
	running, err := h.containerRunning(ctx, id)
	if err != nil {
		return "", err
	}
	if running {
		if err := h.step(ctx, j, stepSaving); err != nil {
			return "", err
		}
		if err := h.markSaveOff(ctx, id); err != nil {
			return "", err
		}
		defer func() {
			j.setStep(stepResuming)
			h.resume(id)
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
	done, err := h.waitService(ctx, j, id, sj.ID)
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
			if errors.Is(err, errServerDeleted) {
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
