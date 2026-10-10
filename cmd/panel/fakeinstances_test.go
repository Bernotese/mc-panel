package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// fakeInstances simulates the agent's instance API: every accepted call takes effect at once
// (no queue, no Docker).
type fakeInstances struct {
	mu      sync.Mutex
	inst    map[int64]agentapi.InstanceStatus
	ensures []agentapi.EnsureRequest
	powers  []string
}

func newFakeInstances() *fakeInstances {
	return &fakeInstances{inst: map[int64]agentapi.InstanceStatus{}}
}

func (f *fakeInstances) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := make([]agentapi.InstanceStatus, 0, len(f.inst))
	for _, st := range f.inst {
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: list,
		Router: agentapi.RouterStatus{State: agentapi.StateRunning}}, nil
}

func (f *fakeInstances) Ensure(_ context.Context, req agentapi.EnsureRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensures = append(f.ensures, req)
	cur, ok := f.inst[req.ID]
	switch {
	case !ok:
		f.inst[req.ID] = agentapi.InstanceStatus{ID: req.ID, Name: req.Name, Revision: req.Revision, State: agentapi.StateCreated}
	case req.Recreate && req.Revision > cur.Revision:
		// recreated; it runs again if it ran before. Like the agent, never downgrade: an
		// older revision (stale or replayed request) is a no-op, even with recreate.
		cur.Revision = req.Revision
		f.inst[req.ID] = cur
	}
	return nil
}

func (f *fakeInstances) Power(_ context.Context, id int64, action agentapi.PowerAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.inst[id]
	if !ok {
		return fmt.Errorf("%w: no container", agentapi.ErrUnavailable)
	}
	f.powers = append(f.powers, fmt.Sprintf("%d %s", id, action))
	st.State = agentapi.StateRunning
	if action == agentapi.PowerStop {
		st.State = agentapi.StateExited
	}
	f.inst[id] = st
	return nil
}

func (f *fakeInstances) Remove(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inst, id)
	return nil
}

func (f *fakeInstances) firstEnsure() agentapi.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ensures) == 0 {
		return agentapi.EnsureRequest{}
	}
	return f.ensures[0]
}

func (f *fakeInstances) lastEnsure() agentapi.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ensures) == 0 {
		return agentapi.EnsureRequest{}
	}
	return f.ensures[len(f.ensures)-1]
}
