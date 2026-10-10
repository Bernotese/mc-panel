package servers

import "context"

// RefreshStatus loads the agent's actual state into the status cache, which the reconcile
// loop does in production.
func RefreshStatus(ctx context.Context, h *Handler) { _, _, _ = h.refreshStatus(ctx) }

// Attempts returns the number of instances with a remembered reconcile attempt (backoff).
func Attempts(h *Handler) int {
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	return len(h.reconcile.attempts)
}
