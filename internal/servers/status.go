package servers

import (
	"context"
	"errors"
	"maps"
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
	// PanelErrors are reconcile errors that never reached the agent, by server id.
	PanelErrors map[int64]string
	// Web is the agent's last answer per web route; kept also when the snapshot is too old
	// (the reconcile pass marks the routes error itself when the agent does not answer).
	Web map[int64]webEntry
}

type statusCache struct {
	mu       sync.Mutex
	snap     Snapshot
	panelErr map[int64]string // reconcile errors that never reached the agent, by server id
}

// setPanelError remembers (msg != "") or clears the panel-side reconcile error of a server.
func (h *Handler) setPanelError(id int64, msg string) {
	h.status.mu.Lock()
	defer h.status.mu.Unlock()
	if msg == "" {
		delete(h.status.panelErr, id)
		return
	}
	if h.status.panelErr == nil {
		h.status.panelErr = map[int64]string{}
	}
	h.status.panelErr[id] = msg
}

// keepPanelErrors forgets the panel-side errors of servers that are gone.
func (h *Handler) keepPanelErrors(keep map[int64]bool) {
	h.status.mu.Lock()
	defer h.status.mu.Unlock()
	maps.DeleteFunc(h.status.panelErr, func(id int64, _ string) bool { return !keep[id] })
}

// snapshot returns the cached state, or the zero Snapshot (unreachable) if it is too old.
func (h *Handler) snapshot() Snapshot {
	h.status.mu.Lock()
	s := h.status.snap
	s.PanelErrors = maps.Clone(h.status.panelErr)
	h.status.mu.Unlock()
	if s.At.IsZero() || h.clock.Now().Sub(s.At) > statusMaxAge {
		s = Snapshot{}
	}
	s.Web = h.webs.snapshot()
	return s
}

// refreshStatus asks the agent for the actual state, caches the answer (or the failure) and
// returns it together with the snapshot built from it; callers use that snapshot, not the
// cache, which a concurrent refresh may already have replaced. A failure caused by the
// caller's own context (client gone, timeout) is not cached: it says nothing about the agent.
func (h *Handler) refreshStatus(ctx context.Context) (agentapi.InstancesResponse, Snapshot, error) {
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
	if err != nil && ctx.Err() != nil {
		return resp, snap, err
	}
	h.status.mu.Lock()
	h.status.snap = snap
	h.status.mu.Unlock()
	return resp, snap, err
}
