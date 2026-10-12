package backups

import "context"

// tick runs once per tickEvery (and once when Run starts). Task 11 adds the time zone check
// and the due schedules.
func (h *Handler) tick(ctx context.Context) {
	h.tickMu.Lock()
	defer h.tickMu.Unlock()
	h.retrySaveOn(ctx)
}

// retrySaveOn repeats save-on for servers whose last attempt failed (Review Focus 1); a server
// with a running job is skipped, its job sends save-on itself.
func (h *Handler) retrySaveOn(ctx context.Context) {
	rows, err := h.q.BackupsListSaveOff(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list save-off marks", "err", err)
		return
	}
	for _, r := range rows {
		if !h.queue.runningFor(r.ServerID) {
			h.resume(r.ServerID)
		}
	}
}
