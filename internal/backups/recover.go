package backups

import (
	"context"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const msgRestarted = "the panel restarted during the backup"

// recover runs once before the worker starts (spec §5.4, crash of the panel): scheduled runs
// that were still running failed, and every server the panel had sent save-off gets save-on,
// after a backup job the crashed panel left running in the service is cancelled.
func (h *Handler) recover(ctx context.Context) {
	if ctx.Err() != nil { // shut down before it started: the next start recovers
		return
	}
	if err := h.q.BackupsFailRunning(ctx, nullString(msgRestarted)); err != nil {
		h.log.ErrorContext(ctx, "backups: mark interrupted runs failed", "err", err)
	}
	rows, err := h.q.BackupsListSaveOff(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list save-off marks", "err", err)
		return
	}
	for _, r := range rows {
		if h.files != nil {
			if sj, err := h.files.Job(ctx, r.ServerID); err == nil && sj != nil && sj.State == filesapi.JobRunning && sj.Kind == filesapi.JobBackup {
				h.cancelService(ctx, r.ServerID)
			}
		}
		h.resume(ctx, r.ServerID) // Run's context: a hanging agent does not delay a shutdown
		if ctx.Err() != nil {
			return // the marks stay, the next start retries
		}
	}
}
