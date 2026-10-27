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
// after the change clears the notice without resetting anything. Called as soon as the files
// service confirmed a change on disk, also when a later step of the operation fails. A failure
// is logged: the change itself has already happened.
func (h *Handler) markChanged(ctx context.Context, serverID int64) {
	ctx = context.WithoutCancel(ctx)
	err := h.q.PluginsMarkChanged(ctx, store.PluginsMarkChangedParams{ServerID: serverID, ChangedAt: h.clock.Now().UnixMilli()})
	if err != nil {
		h.log.WarnContext(ctx, "plugins: record plugin change", "server_id", serverID, "err", err)
	}
}

// restartNeeded reports whether the server's running container started before the last plugin
// change of the panel (a start time of 0 counts as before): true, false, or nil = unknown (agent
// or Docker unavailable, DB error), which never claims that the new plugins are loaded. Without
// a recorded change, or with a server that does not run (it loads the current plugins when it
// starts), the answer is false.
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
		if st.StartedAt == 0 || changedAt >= st.StartedAt {
			return &yes
		}
		return &no
	}
	return &no // no container
}
