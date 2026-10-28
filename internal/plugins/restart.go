package plugins

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// stateTimeout bounds the agent query of restart_needed.
const stateTimeout = 2 * time.Second

// markChanged records a plugin change of the server now (table plugin_changes, spec §6.2): it
// survives a panel restart and is compared with the container's start time, so the first start
// after the change clears the notice without resetting anything. Called before every files call
// that changes a jar (a lost answer may hide a change that happened) and again once the service
// confirmed it (a start between the two must not clear the notice). A failure is logged.
func (h *Handler) markChanged(ctx context.Context, serverID int64) {
	ctx = context.WithoutCancel(ctx)
	err := h.q.PluginsMarkChanged(ctx, store.PluginsMarkChangedParams{ServerID: serverID, ChangedAt: h.clock.Now().UnixMilli()})
	if err != nil {
		h.log.WarnContext(ctx, "plugins: record plugin change", "server_id", serverID, "err", err)
	}
}

// restartNeeded reports whether the server's running container started before the last plugin
// change of the panel: true, false, or nil = unknown (agent or Docker unavailable, start time
// unknown, DB error), which never claims that the new plugins are loaded. Without a recorded
// change, or with a server that does not run (it loads the current plugins when it starts), the
// answer is false. A change time in the future (the clock stepped back) is pinned to now.
func (h *Handler) restartNeeded(ctx context.Context, serverID int64) *bool {
	yes, no := true, false
	changedAt, err := h.q.PluginsGetChanged(ctx, serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return &no
	}
	if err != nil {
		h.log.WarnContext(ctx, "plugins: read plugin change", "server_id", serverID, "err", err)
		return nil
	}
	if now := h.clock.Now().UnixMilli(); changedAt > now {
		changedAt = now
		if err := h.q.PluginsClampChanged(ctx, store.PluginsClampChangedParams{Now: now, ServerID: serverID}); err != nil {
			h.log.WarnContext(ctx, "plugins: pin future plugin change", "server_id", serverID, "err", err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, stateTimeout)
	defer cancel()
	resp, err := h.inst.Instances(ctx)
	if err != nil || resp.Docker != agentapi.DockerOK {
		return nil
	}
	for _, st := range resp.Instances {
		if st.ID != serverID {
			continue
		}
		if st.State != agentapi.StateRunning {
			return &no
		}
		switch {
		case st.StartedAt == 0:
			return nil
		case changedAt >= st.StartedAt:
			return &yes
		default:
			return &no
		}
	}
	return &no // no container
}
