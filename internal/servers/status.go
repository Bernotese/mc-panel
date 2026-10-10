package servers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// statusMaxAge: an older snapshot means the reconcile loop is not getting through, so the
// actual state is reported as unknown.
const statusMaxAge = time.Minute

// Snapshot is the last actual state reported by the agent.
type Snapshot struct {
	Reachable bool   // the agent answered
	Docker    string // agentapi.DockerOK or agentapi.DockerUnavailable (only if Reachable)
	At        time.Time
	Instances map[int64]agentapi.InstanceStatus // by server id
}

type statusCache struct {
	mu   sync.Mutex
	snap Snapshot
}

// snapshot returns the cached state, or the zero Snapshot (unreachable) if it is too old.
func (h *Handler) snapshot() Snapshot {
	h.status.mu.Lock()
	s := h.status.snap
	h.status.mu.Unlock()
	if s.At.IsZero() || h.clock.Now().Sub(s.At) > statusMaxAge {
		return Snapshot{}
	}
	return s
}

// refreshStatus asks the agent for the actual state and caches the answer (or the failure).
func (h *Handler) refreshStatus(ctx context.Context) (agentapi.InstancesResponse, error) {
	resp, err := h.inst.Instances(ctx)
	snap := Snapshot{At: h.clock.Now()}
	switch {
	case err == nil:
		snap.Reachable, snap.Docker = true, resp.Docker
		snap.Instances = make(map[int64]agentapi.InstanceStatus, len(resp.Instances))
		for _, st := range resp.Instances {
			snap.Instances[st.ID] = st
		}
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		snap.Reachable, snap.Docker = true, agentapi.DockerUnavailable
	}
	h.status.mu.Lock()
	h.status.snap = snap
	h.status.mu.Unlock()
	return resp, err
}
