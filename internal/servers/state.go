package servers

import (
	"errors"
	"fmt"

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

// errForeignContainer: the container with the server's ID belongs to another server.
var errForeignContainer = errors.New("the container belongs to another server")

// foreignContainer reports a container with the instance's ID whose mcpanel.name label names
// another server: a leftover of a deleted server whose ID came back (databases before
// migration 00003 reused IDs). The panel never sends actions for it. A missing entry is no
// container (queued work or a remembered error), so its name does not matter.
func foreignContainer(st agentapi.InstanceStatus, name string) bool {
	return st.State != agentapi.StateMissing && st.Name != name
}

func foreignContainerError(st agentapi.InstanceStatus) string {
	return fmt.Sprintf("container mcpanel-mc-%d belongs to another server (%q); the panel leaves it alone, "+
		"remove it on the host or delete this server", st.ID, st.Name)
}

// deriveStatus implements the state table of the plan (contract V4). in == nil means the
// server has no instance yet (TP1 stub); name is the server's name.
func deriveStatus(name string, in *store.Instance, snap Snapshot) statusJSON {
	if in == nil {
		return statusJSON{State: stateNotConfigured}
	}
	desired := in.DesiredState
	out := statusJSON{State: stateUnknown, DesiredState: &desired}
	if !snap.Reachable || snap.Docker != agentapi.DockerOK {
		return out
	}
	st := snap.actualStatus(in.ServerID)
	if foreignContainer(st, name) {
		out.State = stateError
		out.LastError = foreignContainerError(st)
		return out
	}
	if st.LastError == "" {
		st.LastError = snap.PanelErrors[in.ServerID] // the reconcile loop failed before reaching the agent
	}
	out.State = containerState(st, desired)
	out.RestartRequired = isUp(st.State) && needsRecreate(in.Revision, st)
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
	case st.State == agentapi.StateMissing && st.Operation == agentapi.OpNone && st.LastError != "":
		return stateError // the ensure failed for good (or never reached the agent)
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
