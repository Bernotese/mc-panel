package servers

import "context"

// RefreshStatus loads the agent's actual state into the status cache, which the reconcile
// loop does in production.
func RefreshStatus(ctx context.Context, h *Handler) { _, _ = h.refreshStatus(ctx) }
