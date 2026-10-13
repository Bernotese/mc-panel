package docker

import (
	"fmt"
	"regexp"
	"strings"
)

// ContainerConfig is the body of POST /containers/create: the container-level
// fields plus HostConfig and NetworkingConfig. Only fields mc-panel sets exist here,
// so nothing else can ever reach Docker.
type ContainerConfig struct {
	Image            string              `json:"Image"`
	Cmd              []string            `json:"Cmd,omitempty"`
	User             string              `json:"User,omitempty"`
	Env              []string            `json:"Env,omitempty"`
	Labels           map[string]string   `json:"Labels,omitempty"`
	Hostname         string              `json:"Hostname,omitempty"`
	StopTimeout      int                 `json:"StopTimeout,omitempty"`
	ExposedPorts     map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig       HostConfig          `json:"HostConfig"`
	NetworkingConfig NetworkingConfig    `json:"NetworkingConfig"`
}

type HostConfig struct {
	Binds          []string                 `json:"Binds,omitempty"`
	NetworkMode    string                   `json:"NetworkMode,omitempty"`
	PortBindings   map[string][]PortBinding `json:"PortBindings,omitempty"`
	RestartPolicy  RestartPolicy            `json:"RestartPolicy"`
	CapAdd         []string                 `json:"CapAdd,omitempty"`
	CapDrop        []string                 `json:"CapDrop,omitempty"`
	SecurityOpt    []string                 `json:"SecurityOpt,omitempty"`
	Privileged     bool                     `json:"Privileged"`
	ReadonlyRootfs bool                     `json:"ReadonlyRootfs"`
	Memory         int64                    `json:"Memory,omitempty"`
	MemorySwap     int64                    `json:"MemorySwap,omitempty"`
	PidsLimit      int64                    `json:"PidsLimit,omitempty"`
	CPUShares      int64                    `json:"CpuShares,omitempty"` // relative CPU weight; 0 = Docker default 1024
	LogConfig      LogConfig                `json:"LogConfig"`
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type RestartPolicy struct {
	Name string `json:"Name"`
}

type LogConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config,omitempty"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointSettings `json:"EndpointsConfig,omitempty"`
}

type EndpointSettings struct {
	Aliases []string `json:"Aliases,omitempty"`
}

// ContainerSummary is one entry of GET /containers/json.
type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"` // with leading "/"
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
	State  string            `json:"State"` // created, running, paused, restarting, removing, exited, dead
}

// HasName reports whether the container is called name (Docker prefixes names with "/").
func (c ContainerSummary) HasName(name string) bool {
	for _, n := range c.Names {
		if strings.TrimPrefix(n, "/") == name {
			return true
		}
	}
	return false
}

// ContainerInfo is the subset of GET /containers/{id}/json that mc-panel reads.
type ContainerInfo struct {
	ID    string         `json:"Id"`
	Name  string         `json:"Name"` // with leading "/"
	State ContainerState `json:"State"`
	// RestartCount counts restarts by the restart policy since the container was created
	// (Docker inspect "RestartCount"; TP5 crash-loop detection).
	RestartCount int        `json:"RestartCount"`
	Config       InfoConfig `json:"Config"`
}

type ContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Restarting bool   `json:"Restarting"`
	ExitCode   int    `json:"ExitCode"`
	StartedAt  string `json:"StartedAt"` // RFC 3339; "0001-01-01T00:00:00Z" if never started
}

type InfoConfig struct {
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

// Stats is the subset of GET /containers/{id}/stats?stream=false&one-shot=true.
// One-shot stats carry no precpu_stats; callers keep the previous sample themselves.
type Stats struct {
	CPUStats    CPUStats    `json:"cpu_stats"`
	MemoryStats MemoryStats `json:"memory_stats"`
}

type CPUStats struct {
	CPUUsage       CPUUsage `json:"cpu_usage"`
	SystemCPUUsage uint64   `json:"system_cpu_usage"`
	OnlineCPUs     uint32   `json:"online_cpus"`
}

type CPUUsage struct {
	TotalUsage uint64 `json:"total_usage"`
}

type MemoryStats struct {
	Usage uint64            `json:"usage"`
	Limit uint64            `json:"limit"`
	Stats map[string]uint64 `json:"stats"` // cgroup v2: inactive_file; v1: cache
}

// ImageRef is a parsed "repo[:tag]@sha256:<hex>" reference.
type ImageRef struct {
	Repo   string // e.g. "itzg/minecraft-server" or "registry:5000/x/y"
	Tag    string // informational only; may be empty
	Digest string // "sha256:<64 hex>"
}

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	repoRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*$`)
	tagRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// ParseImageRef accepts only digest-pinned references.
func ParseImageRef(ref string) (ImageRef, error) {
	name, digest, ok := strings.Cut(ref, "@")
	if !ok || !digestRe.MatchString(digest) {
		return ImageRef{}, fmt.Errorf("docker: image %q is not pinned with @sha256:<64 hex>", ref)
	}
	repo, tag := name, ""
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		repo, tag = name[:i], name[i+1:]
		if !tagRe.MatchString(tag) {
			return ImageRef{}, fmt.Errorf("docker: image %q has an invalid tag", ref)
		}
	}
	if !repoRe.MatchString(repo) || strings.HasSuffix(repo, "/") || strings.Contains(repo, "//") {
		return ImageRef{}, fmt.Errorf("docker: image %q has an invalid repository", ref)
	}
	return ImageRef{Repo: repo, Tag: tag, Digest: digest}, nil
}

// Pinned is "repo@sha256:…". Docker ignores a tag next to a digest anyway, and the
// containerd image store resolves this form reliably for inspect, pull and create.
func (r ImageRef) Pinned() string { return r.Repo + "@" + r.Digest }
