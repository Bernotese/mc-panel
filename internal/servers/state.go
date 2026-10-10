package servers

import (
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Values of the ServerState schema.
const (
	stateNotConfigured = "not_configured"
	stateUnknown       = "unknown"
	stateCreating      = "creating"
	stateStarting      = "starting"
	stateRunning       = "running"
	stateStopping      = "stopping"
	stateStopped       = "stopped"
	stateRestarting    = "restarting"
	stateError         = "error"
)

// statusJSON is the OpenAPI schema ServerStatus.
type statusJSON struct {
	State            string  `json:"state"`
	DesiredState     *string `json:"desired_state"`
	RestartRequired  bool    `json:"restart_required"`
	Operation        string  `json:"operation"`
	LastError        string  `json:"last_error"`
	StartedAt        int64   `json:"started_at"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUPercent       float64 `json:"cpu_percent"`
}

// isUp reports a container that runs (or that Docker is restarting).
func isUp(s agentapi.ContainerState) bool {
	return s == agentapi.StateRunning || s == agentapi.StateRestarting
}

// actualStatus returns the agent's status of one instance; a container the agent does not
// list is missing.
func (s Snapshot) actualStatus(id int64) agentapi.InstanceStatus {
	if st, ok := s.Instances[id]; ok {
		return st
	}
	return agentapi.InstanceStatus{ID: id, State: agentapi.StateMissing}
}

// deriveStatus implements the state table of the plan (contract V4). in == nil means the
// server has no instance yet (TP1 stub).
func deriveStatus(in *store.Instance, snap Snapshot) statusJSON {
	if in == nil {
		return statusJSON{State: stateNotConfigured}
	}
	desired := in.DesiredState
	out := statusJSON{State: stateUnknown, DesiredState: &desired}
	if !snap.Reachable || snap.Docker != agentapi.DockerOK {
		return out
	}
	st := snap.actualStatus(in.ServerID)
	out.State = containerState(st, desired)
	out.RestartRequired = isUp(st.State) && st.Revision < in.Revision
	out.Operation = string(st.Operation)
	out.LastError = st.LastError
	out.StartedAt = st.StartedAt
	out.MemoryBytes = st.MemoryBytes
	out.MemoryLimitBytes = st.MemoryLimitBytes
	out.CPUPercent = st.CPUPercent
	return out
}

func containerState(st agentapi.InstanceStatus, desired string) string {
	switch {
	case st.Operation == agentapi.OpPulling || st.Operation == agentapi.OpCreating || st.State == agentapi.StateMissing:
		return stateCreating
	case st.Operation == agentapi.OpStarting:
		return stateStarting
	case st.Operation == agentapi.OpStopping || st.Operation == agentapi.OpRemoving:
		return stateStopping
	case st.State == agentapi.StateRestarting:
		return stateRestarting
	case st.State == agentapi.StateRunning:
		return stateRunning
	case st.State == agentapi.StateExited || st.State == agentapi.StateCreated:
		if desired == desiredRunning && (st.ExitCode != 0 || st.LastError != "") {
			return stateError
		}
		return stateStopped
	default:
		return stateUnknown
	}
}
