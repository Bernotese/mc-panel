package plugins

import (
	"context"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// stateTimeout bounds the agent query of restart_needed.
const stateTimeout = 2 * time.Second

// restartTracker remembers the last plugin change per server (in memory: forgotten when the
// panel restarts, spec §6.2).
type restartTracker struct {
	mu      sync.Mutex
	changed map[int64]time.Time
}

func newRestartTracker() *restartTracker { return &restartTracker{changed: map[int64]time.Time{}} }

func (t *restartTracker) mark(id int64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.changed[id] = at
}

func (t *restartTracker) since(id int64) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.changed[id]
	return at, ok
}

// markChanged records a plugin change of the server now.
func (h *Handler) markChanged(serverID int64) { h.restart.mark(serverID, h.clock.Now()) }

// restartNeeded reports a running server whose container started before the last plugin change
// of the panel (a start time of 0 counts as before). Without agent answer: false.
func (h *Handler) restartNeeded(ctx context.Context, serverID int64) bool {
	at, ok := h.restart.since(serverID)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, stateTimeout)
	defer cancel()
	resp, err := h.inst.Instances(ctx)
	if err != nil {
		return false
	}
	for _, st := range resp.Instances {
		if st.ID == serverID {
			return st.State == agentapi.StateRunning && (st.StartedAt == 0 || at.UnixMilli() >= st.StartedAt)
		}
	}
	return false
}
