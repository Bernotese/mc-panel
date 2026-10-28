package plugins

import (
	"context"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// MarkChanged records a plugin change of serverID at t (restart_needed tests).
func MarkChanged(h *Handler, serverID int64, t time.Time) {
	if err := h.q.PluginsMarkChanged(context.Background(), store.PluginsMarkChangedParams{ServerID: serverID, ChangedAt: t.UnixMilli()}); err != nil {
		panic(err)
	}
}

// WaitJobs waits until every started job has finished (only between sequential test steps).
func WaitJobs(h *Handler) { h.jobs.wg.Wait() }

// SetIconCacheBytes shrinks the icon cache (eviction test).
func SetIconCacheBytes(h *Handler, n int64) { h.icons.maxBytes = n }

// SetBusyBackoff shortens the wait before a job step retries an upload the service refused as
// busy.
func SetBusyBackoff(h *Handler, d time.Duration) { h.busyBackoff = d }
