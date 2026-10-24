package servers

import (
	"strings"
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
		return agentapi.InstanceStatus{ID: 1, Name: "survival", Revision: 2, State: state, Operation: op}
	}
	exited := func(code int, lastErr string) agentapi.InstanceStatus {
		return agentapi.InstanceStatus{ID: 1, Name: "survival", Revision: 2, State: agentapi.StateExited, ExitCode: code, LastError: lastErr}
	}
	missingErr := agentapi.InstanceStatus{ID: 1, Name: "survival", State: agentapi.StateMissing, LastError: "pull failed"}
	panelErr := ok()
	panelErr.PanelErrors = map[int64]string{1: "no base domain configured"}
	foreign := c(agentapi.StateRunning, agentapi.OpNone)
	foreign.Name = "oldworld"
	foreignMissing := c(agentapi.StateMissing, agentapi.OpPulling)
	foreignMissing.Name = "oldworld"
	cases := []struct {
		name string
		in   *store.Instance
		snap Snapshot
		want string
	}{
		{"no instance row", nil, ok(), stateNotConfigured},
		{"agent unreachable", in, Snapshot{}, stateUnknown},
		{"docker unavailable", in, Snapshot{Reachable: true, Docker: agentapi.DockerUnavailable}, stateUnknown},
		{"container of another server", in, ok(foreign), stateError},
		{"missing entry with another name is no container", in, ok(foreignMissing), stateCreating},
		{"pulling", in, ok(c(agentapi.StateMissing, agentapi.OpPulling)), stateCreating},
		{"creating", in, ok(c(agentapi.StateMissing, agentapi.OpCreating)), stateCreating},
		{"container not listed", in, ok(), stateCreating},
		{"missing", in, ok(c(agentapi.StateMissing, agentapi.OpNone)), stateCreating},
		{"missing with last_error (ensure failed for good)", in, ok(missingErr), stateError},
		{"missing with last_error, wants stopped", stopped, ok(missingErr), stateError},
		{"panel-side reconcile error", in, panelErr, stateError},
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
		got := deriveStatus("survival", tc.in, tc.snap)
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
		state    agentapi.ContainerState
		rev      int64
		outdated bool // template_outdated (F10)
		want     bool
	}{
		{agentapi.StateRunning, 2, false, true},
		{agentapi.StateRestarting, 2, false, true},
		{agentapi.StateRunning, 3, false, false},
		{agentapi.StateExited, 2, false, false},
		{agentapi.StateCreated, 1, false, false},
		{agentapi.StateRunning, 3, true, true},
		{agentapi.StateRestarting, 3, true, true},
		{agentapi.StateExited, 3, true, false},
		{agentapi.StateCreated, 3, true, false},
	} {
		snap := Snapshot{Reachable: true, Docker: agentapi.DockerOK, Instances: map[int64]agentapi.InstanceStatus{
			1: {ID: 1, Name: "survival", Revision: tc.rev, State: tc.state, TemplateOutdated: tc.outdated},
		}}
		if got := deriveStatus("survival", in, snap).RestartRequired; got != tc.want {
			t.Errorf("%s rev %d outdated %t: restart_required %t, want %t", tc.state, tc.rev, tc.outdated, got, tc.want)
		}
	}
}

// last_error: the agent's own error wins; a panel-side reconcile error (one that never reached
// the agent) fills it otherwise. A foreign container gets an explanation instead of its data.
func TestDeriveStatusLastError(t *testing.T) {
	in := &store.Instance{ServerID: 1, DesiredState: desiredRunning, Revision: 1}
	snap := func(st agentapi.InstanceStatus, panelErr string) Snapshot {
		s := Snapshot{Reachable: true, Docker: agentapi.DockerOK, Instances: map[int64]agentapi.InstanceStatus{1: st}}
		if panelErr != "" {
			s.PanelErrors = map[int64]string{1: panelErr}
		}
		return s
	}
	missing := agentapi.InstanceStatus{ID: 1, Name: "survival", State: agentapi.StateMissing}
	if got := deriveStatus("survival", in, snap(missing, "no base domain configured")); got.LastError != "no base domain configured" {
		t.Errorf("panel error not merged: %+v", got)
	}
	withAgentErr := missing
	withAgentErr.LastError = "pull failed"
	if got := deriveStatus("survival", in, snap(withAgentErr, "no base domain configured")); got.LastError != "pull failed" {
		t.Errorf("agent error must win: %+v", got)
	}
	foreign := agentapi.InstanceStatus{ID: 1, Name: "oldworld", Revision: 7, State: agentapi.StateRunning, MemoryBytes: 1 << 30, StartedAt: 5}
	got := deriveStatus("survival", in, snap(foreign, ""))
	if got.State != stateError || !strings.Contains(got.LastError, "another server") || got.RestartRequired ||
		got.MemoryBytes != 0 || got.StartedAt != 0 {
		t.Errorf("foreign container = %+v", got)
	}
}
