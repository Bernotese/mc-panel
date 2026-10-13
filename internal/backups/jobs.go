package backups

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	kindBackup  = "backup"
	kindRestore = "restore"
	kindDelete  = "delete"

	triggerManual    = "manual"
	triggerScheduled = "scheduled"

	stateQueued  = "queued"
	stateRunning = "running"
	stateDone    = "done"
	stateFailed  = "failed"

	stepQueued     = "queued"
	stepSaving     = "saving"
	stepBackingUp  = "backing_up"
	stepResuming   = "resuming"
	stepPruning    = "pruning"
	stepStopping   = "stopping"
	stepPreRestore = "pre_restore"
	stepRestoring  = "restoring"
	stepDeleting   = "deleting"
	stepDone       = "done"

	codeRcon        = "rcon_unavailable"
	codeAgent       = "agent_unavailable"
	codeFiles       = "files_unavailable"
	codeFilesBusy   = "files_busy"
	codeBackups     = "backups_unavailable"
	codeStorage     = "insufficient_storage"
	codeTooLarge    = "too_large"
	codeStopTimeout = "stop_timeout"
	codeCanceled    = "canceled"
	codeInternal    = "internal"

	msgCanceled      = "the job was canceled (the panel shut down or the server was deleted)"
	msgStalled       = "the backup made no progress for 10 minutes"
	msgForgetTimeout = "deleting snapshots did not finish within 2 hours"
	msgInternal      = "internal error (details are in the panel log)"
)

// errServerDeleted ends a job whose server was deleted while the job waited or between two
// steps: no further step, no audit entry, no notification. DELETE /servers/{id} only refuses
// while a restore holds the gate, so a waiting backup or delete job can lose its server.
var errServerDeleted error = &jobError{code: codeCanceled, msg: "the job was canceled: the server was deleted"}

// jobView is the OpenAPI schema BackupJob.
type jobView struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Trigger       string  `json:"trigger"`
	State         string  `json:"state"`
	Step          string  `json:"step"`
	QueuePosition int     `json:"queue_position"`
	Bytes         int64   `json:"bytes"`
	Snapshot      *string `json:"snapshot"`
	ErrorCode     *string `json:"error_code"`
	Error         string  `json:"error"`
	QueuedAt      int64   `json:"queued_at"`
	StartedAt     int64   `json:"started_at"`
	FinishedAt    int64   `json:"finished_at"`
}

type jobResponse struct {
	Job *jobView `json:"job"`
}

// jobError is the failure of a job step with its BackupJobError code.
type jobError struct{ code, msg string }

func (e *jobError) Error() string { return e.msg }

func failf(code, format string, args ...any) error {
	return &jobError{code: code, msg: fmt.Sprintf(format, args...)}
}

// jobErrorOf maps a job's error to its code and message (V5); messages never carry host paths
// (the service already scrubs its own).
func jobErrorOf(err error) (code, msg string) {
	var je *jobError
	switch {
	case errors.As(err, &je):
		return je.code, je.msg
	case errors.Is(err, context.Canceled):
		return codeCanceled, msgCanceled
	case errors.Is(err, filesapi.ErrBackupsUnavailable):
		return codeBackups, "restic is not available on this host"
	case errors.Is(err, filesapi.ErrInsufficientStorage):
		return codeStorage, "not enough free disk space"
	case errors.Is(err, filesapi.ErrTooLarge):
		return codeTooLarge, "the data directory exceeds the backup limits"
	case errors.Is(err, filesapi.ErrBusy):
		return codeFilesBusy, "the file service is busy with another job for this server (for example an extract); try again later"
	case errors.Is(err, filesapi.ErrNotFound):
		return codeInternal, "the server has no data directory yet or the snapshot is gone"
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return codeFiles, "the file service is not reachable"
	case errors.Is(err, agentapi.ErrUnavailable), errors.Is(err, agentapi.ErrDockerUnavailable):
		return codeAgent, "the agent is not reachable"
	}
	return codeInternal, msgInternal // the error itself may name host paths; the queue logs it
}

// canceledOr returns ctx's error when ctx ended and err is set: a step that failed because the
// panel shut down (or the job was dropped) is a cancel, not an RCON, agent or service error.
// errServerDeleted stays (it is a cancel too, but one without audit entry).
func canceledOr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && !errors.Is(err, errServerDeleted) {
		return ctx.Err()
	}
	return err
}
