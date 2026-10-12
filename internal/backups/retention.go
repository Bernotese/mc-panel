package backups

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// runScheduled is a scheduled run (spec §5.4): backup, retention on success, last_* and
// next_run_at, backup.failed on failure.
func (h *Handler) runScheduled(ctx context.Context, j *job) (string, error) {
	id := j.spec.serverID
	started := h.clock.Now()
	if err := h.q.BackupsStartRun(ctx, store.BackupsStartRunParams{LastRunAt: nullTime(started), ServerID: id}); err != nil {
		return "", err
	}
	snap, err := h.runBackup(ctx, j, filesapi.SnapshotScheduled, "")
	hint := ""
	if err == nil {
		perr := h.prune(ctx, j)
		switch {
		case errors.Is(perr, errServerDeleted):
			err = perr
		case perr != nil:
			_, msg := jobErrorOf(perr)
			hint = "retention: " + msg
			h.log.WarnContext(ctx, "backups: retention failed", "server_id", id, "err", perr)
		}
	}
	if errors.Is(err, errServerDeleted) {
		return snap, err // the row went with the server: nothing to record or report
	}
	h.finishScheduled(ctx, j, started, snap, err, hint)
	return snap, err
}

// prune applies the server's retention to its scheduled snapshots (forget --keep-*, pinned
// ones stay; manual and pre-restore snapshots are never touched).
func (h *Handler) prune(ctx context.Context, j *job) error {
	id := j.spec.serverID
	if h.serverGone(ctx, id) { // a step of its own: the server may be deleted meanwhile
		return errServerDeleted
	}
	row, err := h.q.BackupsGetSchedule(ctx, id)
	if err != nil {
		return err
	}
	p := scheduleOf(row).Policy()
	if p.Validate() != nil { // switched off meanwhile without retention: nothing to apply
		return nil
	}
	j.setStep(stepPruning)
	// the same zone as the backups (tzName), so the buckets match the stored snapshot times
	sj, err := h.files.ForgetBackups(ctx, filesapi.ForgetRequest{ID: id, Policy: &p, Timezone: h.tzName(ctx)})
	if err != nil {
		return err
	}
	_, err = h.waitService(ctx, j, id, sj.ID)
	return err
}

// finishScheduled writes last_* and the next run, and notifies a failure (not for a cancel or
// a server without data directory). A full success leaves last_error NULL; a failed forget
// alone keeps last_status ok with last_error "retention: <message>".
func (h *Handler) finishScheduled(ctx context.Context, j *job, started time.Time, snap string, runErr error, hint string) {
	id := j.spec.serverID
	ctx = context.WithoutCancel(ctx) // also after a cancel the row must be written
	row, err := h.q.BackupsGetSchedule(ctx, id)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: finish run", "server_id", id, "err", err)
		return
	}
	loc, _, err := h.location(ctx)
	if err != nil {
		loc = time.UTC
	}
	var next sql.NullInt64
	if t, ok := NextRun(scheduleOf(row), loc, h.clock.Now(), started); ok {
		next = nullTime(t)
	}
	status, msg := "ok", hint
	if runErr != nil {
		status = "failed"
		_, msg = jobErrorOf(runErr)
	}
	if err := h.q.BackupsFinishRun(ctx, store.BackupsFinishRunParams{
		LastStatus: nullString(status), LastError: nullString(msg), LastSnapshotID: nullString(snap), NextRunAt: next, ServerID: id,
	}); err != nil {
		h.log.ErrorContext(ctx, "backups: finish run", "server_id", id, "err", err)
	}
	switch {
	case runErr == nil:
		h.alertOK(id, alertBackup)
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, filesapi.ErrNotFound):
		// shutdown, a deleted server or a server that never ran: nothing to report
	default:
		h.alert(ctx, id, j.spec.name, alertBackup, msg)
	}
}
