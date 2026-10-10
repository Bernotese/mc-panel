package servers

import (
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// One case per row of the state table (contract V4), in table order.
func TestDeriveStatusTable(t *testing.T) {
	in := &store.Instance{ServerID: 1, DesiredState: desiredRunning, Revision: 2}
	stopped := &store.Instance{ServerID: 1, DesiredState: desiredStopped, Revision: 2}
	ok := func(st ...agentapi.InstanceStatus) Snapshot {
		s := Snapshot{Reachable: true, Docker: agentapi.DockerOK, Instances: map[int64]agentapi.InstanceStatus{}}
		for _, x := range st {
			s.Instances[x.ID] = x
		}
		return s
	}
	c := func(state agentapi.ContainerState, op agentapi.Operation) agentapi.InstanceStatus {
		return agentapi.InstanceStatus{ID: 1, Revision: 2, State: state, Operation: op}
	}
	exited := func(code int, lastErr string) agentapi.InstanceStatus {
		return agentapi.InstanceStatus{ID: 1, Revision: 2, State: agentapi.StateExited, ExitCode: code, LastError: lastErr}
	}
	cases := []struct {
		name string
		in   *store.Instance
		snap Snapshot
		want string
	}{
		{"no instance row", nil, ok(), stateNotConfigured},
		{"agent unreachable", in, Snapshot{}, stateUnknown},
		{"docker unavailable", in, Snapshot{Reachable: true, Docker: agentapi.DockerUnavailable}, stateUnknown},
		{"pulling", in, ok(c(agentapi.StateMissing, agentapi.OpPulling)), stateCreating},
		{"creating", in, ok(c(agentapi.StateMissing, agentapi.OpCreating)), stateCreating},
		{"container not listed", in, ok(), stateCreating},
		{"missing", in, ok(c(agentapi.StateMissing, agentapi.OpNone)), stateCreating},
		{"starting", in, ok(c(agentapi.StateCreated, agentapi.OpStarting)), stateStarting},
		{"stopping", in, ok(c(agentapi.StateRunning, agentapi.OpStopping)), stateStopping},
		{"removing", in, ok(c(agentapi.StateRunning, agentapi.OpRemoving)), stateStopping},
		{"restarting", in, ok(c(agentapi.StateRestarting, agentapi.OpNone)), stateRestarting},
		{"running", in, ok(c(agentapi.StateRunning, agentapi.OpNone)), stateRunning},
		{"exited with code, wants running", in, ok(exited(1, "")), stateError},
		{"exited with last_error, wants running", in, ok(exited(0, "pull failed")), stateError},
		{"exited cleanly, wants running", in, ok(exited(0, "")), stateStopped},
		{"exited with code, wants stopped", stopped, ok(exited(137, "")), stateStopped},
		{"created", in, ok(c(agentapi.StateCreated, agentapi.OpNone)), stateStopped},
		{"unknown container state", in, ok(c("paused", agentapi.OpNone)), stateUnknown},
	}
	for _, tc := range cases {
		got := deriveStatus(tc.in, tc.snap)
		if got.State != tc.want {
			t.Errorf("%s: state %s, want %s", tc.name, got.State, tc.want)
		}
		if (tc.in == nil) != (got.DesiredState == nil) {
			t.Errorf("%s: desired_state = %v", tc.name, got.DesiredState)
		}
	}
}

func TestRestartRequired(t *testing.T) {
	in := &store.Instance{ServerID: 1, DesiredState: desiredRunning, Revision: 3}
	for _, tc := range []struct {
		state agentapi.ContainerState
		rev   int64
		want  bool
	}{
		{agentapi.StateRunning, 2, true},
		{agentapi.StateRestarting, 2, true},
		{agentapi.StateRunning, 3, false},
		{agentapi.StateExited, 2, false},
		{agentapi.StateCreated, 1, false},
	} {
		snap := Snapshot{Reachable: true, Docker: agentapi.DockerOK, Instances: map[int64]agentapi.InstanceStatus{
			1: {ID: 1, Revision: tc.rev, State: tc.state},
		}}
		if got := deriveStatus(in, snap).RestartRequired; got != tc.want {
			t.Errorf("%s rev %d: restart_required %t, want %t", tc.state, tc.rev, got, tc.want)
		}
	}
}
