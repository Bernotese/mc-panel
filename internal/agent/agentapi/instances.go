package agentapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	PathInstances      = "/v1/instances"
	PathInstanceEnsure = "/v1/instances/ensure"
	PathInstancePower  = "/v1/instances/power"
	PathInstanceRemove = "/v1/instances/remove"

	// MaxInstanceRequestBytes is the body limit of the /v1/instances/* endpoints (a full
	// spec needs more than the 4 KiB of the firewall endpoints).
	MaxInstanceRequestBytes = 16 << 10

	ErrorCodeBusy              = "busy"
	ErrorCodeDockerUnavailable = "docker_unavailable"

	DockerOK          = "ok"
	DockerUnavailable = "unavailable"
)

var (
	// ErrBusy: the instance queue is full; retry later.
	ErrBusy = errors.New("agent busy")
	// ErrDockerUnavailable: the agent runs but cannot reach Docker.
	ErrDockerUnavailable = errors.New("docker unavailable")
)

type PowerAction string

const (
	PowerStart   PowerAction = "start"
	PowerStop    PowerAction = "stop"
	PowerRestart PowerAction = "restart"
)

func ValidPowerAction(a PowerAction) bool {
	return a == PowerStart || a == PowerStop || a == PowerRestart
}

type ContainerState string

const (
	StateCreated    ContainerState = "created"
	StateRunning    ContainerState = "running"
	StateRestarting ContainerState = "restarting"
	StateExited     ContainerState = "exited"
	StateMissing    ContainerState = "missing"
)

type Operation string

const (
	OpNone     Operation = ""
	OpPulling  Operation = "pulling"
	OpCreating Operation = "creating"
	OpStarting Operation = "starting"
	OpStopping Operation = "stopping"
	OpRemoving Operation = "removing"
)

type EnsureRequest struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	FQDN     string            `json:"fqdn"`
	Revision int64             `json:"revision"`
	Spec     instancespec.Spec `json:"spec"`
	Recreate bool              `json:"recreate"`
}

func (r EnsureRequest) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	}
	if !instancespec.ValidName(r.Name) {
		return fmt.Errorf("%w: name: invalid", instancespec.ErrInvalid)
	}
	if err := instancespec.CheckFQDN(r.Name, r.FQDN); err != nil {
		return err
	}
	if r.Revision <= 0 {
		return fmt.Errorf("%w: revision: must be > 0", instancespec.ErrInvalid)
	}
	return r.Spec.Validate()
}

type PowerRequest struct {
	ID     int64       `json:"id"`
	Action PowerAction `json:"action"`
}

type RemoveRequest struct {
	ID int64 `json:"id"`
}

type InstanceStatus struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Revision         int64          `json:"revision"`
	State            ContainerState `json:"state"`
	ExitCode         int            `json:"exit_code"`
	StartedAt        int64          `json:"started_at"` // Unix ms, 0 = not running
	MemoryBytes      int64          `json:"memory_bytes"`
	MemoryLimitBytes int64          `json:"memory_limit_bytes"`
	CPUPercent       float64        `json:"cpu_percent"`
	Operation        Operation      `json:"operation"`
	LastError        string         `json:"last_error"`
}

type RouterStatus struct {
	State     ContainerState `json:"state"`
	LastError string         `json:"last_error"`
}

type InstancesResponse struct {
	Docker    string           `json:"docker"`
	Instances []InstanceStatus `json:"instances"`
	Router    RouterStatus     `json:"router"`
}

// InstancesClient is the panel-side view of the instance endpoints. It is separate from
// Client so that firewall-only fakes keep compiling.
type InstancesClient interface {
	Instances(ctx context.Context) (InstancesResponse, error)
	Ensure(ctx context.Context, req EnsureRequest) error
	Power(ctx context.Context, id int64, action PowerAction) error
	Remove(ctx context.Context, id int64) error
}
