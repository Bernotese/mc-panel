package plugins

import "time"

// MarkChanged records a plugin change of serverID at t (restart_needed tests).
func MarkChanged(h *Handler, serverID int64, t time.Time) { h.restart.mark(serverID, t) }

// WaitJobs waits until every started job has finished (only between sequential test steps).
func WaitJobs(h *Handler) { h.jobs.wg.Wait() }

// SetIconCacheBytes shrinks the icon cache (eviction test).
func SetIconCacheBytes(h *Handler, n int64) { h.icons.maxBytes = n }
