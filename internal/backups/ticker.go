package backups

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// retrySaveOn repeats save-on for servers whose last attempt failed (Review Focus 1). A server
// whose save lock is held is skipped: a job holds it from before its mark to after its
// save-on, so the retry never answers a fresh save-off. A job that only waits does not hold
// it and does not delay the save-on.
func (h *Handler) retrySaveOn(ctx context.Context) {
	rows, err := h.q.BackupsListSaveOff(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.log.ErrorContext(ctx, "backups: list save-off marks", "err", err)
		}
		return
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return // the marks stay, the next tick or start retries
		}
		lock := h.saveLocks.get(r.ServerID)
		if !lock.TryLock() {
			continue
		}
		h.resume(ctx, r.ServerID)
		lock.Unlock()
	}
}

// tick runs once per tickEvery (and once when Run starts): pending save-on, a changed time
// zone, then the due schedules.
func (h *Handler) tick(ctx context.Context) {
	if ctx.Err() != nil { // shutting down: every query would fail
		return
	}
	h.tickMu.Lock()
	defer h.tickMu.Unlock()
	now := h.clock.Now()
	h.retrySaveOn(ctx)
	h.checkTimezone(ctx, now)
	h.enqueueDue(ctx, now)
}

// checkTimezone recomputes next_run_at of every active schedule when the panel time zone
// changed since the last tick (deviation 5: the first tick only remembers it).
func (h *Handler) checkTimezone(ctx context.Context, now time.Time) {
	loc, name, err := h.location(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: read the time zone", "err", err)
		return
	}
	if h.lastTZ == "" || h.lastTZ == name {
		h.lastTZ = name
		return
	}
	h.lastTZ = name
	rows, err := h.q.BackupsListEnabled(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list schedules", "err", err)
		return
	}
	for _, r := range rows {
		var next sql.NullInt64
		if t, ok := NextRun(scheduleOf(r), loc, now, timeOf(r.LastRunAt)); ok {
			next = nullTime(t)
		}
		if err := h.q.BackupsSetNextRun(ctx, store.BackupsSetNextRunParams{NextRunAt: next, ServerID: r.ServerID}); err != nil {
			h.log.ErrorContext(ctx, "backups: set next run", "server_id", r.ServerID, "err", err)
		}
	}
}

// enqueueDue queues a scheduled run for every due schedule; a server with a queued or running
// job is skipped and checked again on the next tick, a dev instance always.
func (h *Handler) enqueueDue(ctx context.Context, now time.Time) {
	if h.files == nil {
		return // no file service: nothing can run (the schedules stay due)
	}
	rows, err := h.q.BackupsListDue(ctx, nullTime(now))
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list due schedules", "err", err)
		return
	}
	for _, r := range rows {
		s, err := h.q.RbacGetServer(ctx, r.ServerID)
		if err != nil {
			continue // deleted meanwhile (the row goes with it)
		}
		if dev, err := h.isDev(ctx, r.ServerID); dev || err != nil {
			continue // a dev instance has no backups (its sync is queued by devinst)
		}
		_, err = h.queue.add(context.Background(), jobSpec{
			serverID: r.ServerID, name: s.Name, kind: kindBackup, trigger: triggerScheduled, run: h.runScheduled,
		})
		if errors.Is(err, errShuttingDown) {
			return
		}
	}
}
