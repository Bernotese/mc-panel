// Package instances runs the Minecraft containers on the agent side: a fixed,
// hardened container template, the mc-router routes file and a manager with one
// serial work queue per instance. Containers without the mcpanel.instance label
// are never touched.
package instances

import (
	"cmp"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/config"
)

const (
	// RouterNetwork is mc-router's home network (its published port); since SEC-03 no
	// instance container stays on it.
	RouterNetwork   = "mcpanel-mc"
	RouterName      = "mcpanel-router"
	ContainerPrefix = "mcpanel-mc-"

	LabelInstance = "mcpanel.instance"
	LabelRevision = "mcpanel.revision"
	LabelName     = "mcpanel.name"
	LabelFQDN     = "mcpanel.fqdn"
	LabelRouter   = "mcpanel.router"
	// LabelTemplate carries InstanceTemplateHash of the container; a missing or different
	// value (agent upgrade) makes the instance "restart required", and a recreate fixes it.
	// LabelType and LabelJava are its inputs besides the host settings.
	LabelTemplate = "mcpanel.template"
	LabelType     = "mcpanel.type"
	LabelJava     = "mcpanel.java"
	// LabelRouterTemplate carries the SHA-256 of the router's container template; a
	// different value (agent upgrade) replaces the router container.
	LabelRouterTemplate = "mcpanel.router-template"
	LabelNetwork        = "mcpanel.network"

	MinecraftPort      = 25565
	StopTimeoutSeconds = 60
	PidsLimit          = 1024
	// CPUShares is the CPU weight of instance containers: 256 of Docker's default
	// 1024 (cgroup v2 weight ≈ 10 instead of 100), so a busy server can use idle CPU
	// but never starves panel, agent or router under contention.
	CPUShares = 256
	// RouterMemoryBytes caps mc-router (it needs a few MiB).
	RouterMemoryBytes = 256 << 20
	// RouterPidsLimit caps mc-router's threads (about 13 under a flood of 200 connections).
	RouterPidsLimit = 128
	// NofileLimit is the open-files limit of instance containers (soft = hard).
	NofileLimit = 65536

	routerPort        = "25565/tcp"
	routerConfigDir   = "/config"
	routesFileName    = "routes.json"
	mib               = 1 << 20
	memoryNumerator   = 5 // Memory = memory_mib × 1.25 MiB: heap plus JVM/native overhead
	memoryDenominator = 4
)

// TemplateEpoch is part of every instance template hash. InstanceTemplateHash only sees the
// template for canonical spec values; bump it for a template change that shows only for
// other values (e.g. a new variable for one property value), so existing containers are
// marked outdated as well.
const TemplateEpoch = 1

// Host holds the agent-side settings the templates need; none of them come from the panel.
type Host struct {
	InstancesDir string
	UID, GID     int
	Images       config.Images
	// NanoCPUs caps every instance container (cpu_per_instance); 0 = no cap. Above the
	// host's CPUs it is clamped (MaxNanoCPUs).
	NanoCPUs int64
	// RouterConnectionRateLimit is mc-router's -connection-rate-limit; 0 means
	// config.DefaultRouterConnectionRateLimit.
	RouterConnectionRateLimit int
	// WebProxyPort and WebAskPort are the 127.0.0.1 host ports of mcpanel-web; 0 means
	// config.DefaultWebProxyPort / config.DefaultWebAskPort.
	WebProxyPort, WebAskPort int
}

// MaxNanoCPUs is the CPU cap of this host: all its CPUs.
func MaxNanoCPUs() int64 { return int64(runtime.NumCPU()) * 1e9 }

func ContainerName(id int64) string { return ContainerPrefix + strconv.FormatInt(id, 10) }

// DataDir is the host directory mounted at /data: <instances_dir>/instances/<id>.
func DataDir(instancesDir string, id int64) string {
	return filepath.Join(instancesDir, "instances", strconv.FormatInt(id, 10))
}

// RouterDir is mounted read-only at /config in the router container.
func RouterDir(instancesDir string) string { return filepath.Join(instancesDir, "router") }

// TmpDir is mounted at /tmp: <instances_dir>/tmp/<id>. With a read-only rootfs it is the only
// path the itzg image writes outside /data (Java native libraries; modpacks stage the whole
// .mrpack there), so it is disk-backed next to the data instead of a tmpfs charged to the
// container's memory, and outside instances/<id> so the files service and backups never see
// it. The agent empties it whenever the container is (re)created and removes it with the
// instance. Executable, like /data, because Java loads its native libraries from there.
func TmpDir(instancesDir string, id int64) string {
	return filepath.Join(instancesDir, "tmp", strconv.FormatInt(id, 10))
}

// PatchesDir holds the itzg patch definitions (instancespec.Patches), root-owned and mounted
// read-only at instancespec.PatchesDir in every instance container. The agent writes it,
// never a data directory.
func PatchesDir(instancesDir string) string { return filepath.Join(instancesDir, "patches") }

// DeletedDir receives the data of removed instances.
func DeletedDir(instancesDir string) string { return filepath.Join(instancesDir, "deleted") }

func logConfig() docker.LogConfig {
	return docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}}
}

func network(name string) docker.NetworkingConfig {
	return docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{name: {}}}
}

func pinnedImage(ref string) (string, error) {
	r, err := docker.ParseImageRef(ref)
	if err != nil {
		return "", err
	}
	return r.Pinned(), nil
}

// InstanceContainer builds the complete container configuration for one instance.
// It validates req again (the agent never trusts the panel); everything not derived
// from req is fixed here and cannot be influenced by the panel.
func InstanceContainer(h Host, req agentapi.EnsureRequest) (docker.ContainerConfig, error) {
	if err := req.Validate(); err != nil {
		return docker.ContainerConfig{}, err
	}
	env, err := instancespec.Env(req.Name, req.Spec)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	ref, ok := h.Images.ByJava(string(req.Spec.Java))
	if !ok {
		return docker.ContainerConfig{}, fmt.Errorf("%w: java: no image for %q", instancespec.ErrInvalid, req.Spec.Java)
	}
	img, err := pinnedImage(ref)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	memory := int64(req.Spec.MemoryMiB) * mib * memoryNumerator / memoryDenominator
	return docker.ContainerConfig{
		Image:    img,
		User:     fmt.Sprintf("%d:%d", h.UID, h.GID),
		Env:      env,
		Hostname: req.Name,
		Labels: map[string]string{
			LabelInstance: strconv.FormatInt(req.ID, 10),
			LabelRevision: strconv.FormatInt(req.Revision, 10),
			LabelName:     req.Name,
			LabelFQDN:     req.FQDN,
		},
		StopTimeout: StopTimeoutSeconds,
		HostConfig: docker.HostConfig{
			Binds: []string{
				DataDir(h.InstancesDir, req.ID) + ":/data",
				TmpDir(h.InstancesDir, req.ID) + ":/tmp",
				PatchesDir(h.InstancesDir) + ":" + instancespec.PatchesDir + ":ro",
			},
			NetworkMode:    InstanceNetwork(req.ID),
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			Privileged:     false,
			ReadonlyRootfs: true,
			Memory:         memory,
			MemorySwap:     memory,
			PidsLimit:      PidsLimit,
			CPUShares:      CPUShares,
			NanoCPUs:       min(h.NanoCPUs, MaxNanoCPUs()),
			Ulimits:        []docker.Ulimit{{Name: "nofile", Soft: NofileLimit, Hard: NofileLimit}},
			LogConfig:      logConfig(),
		},
		NetworkingConfig: network(InstanceNetwork(req.ID)),
	}, nil
}

// canonicalRequest is a valid request for the template of type t with java j: fixed name,
// version, memory and default properties, so the hash covers the template, not the spec
// values (the revision covers those).
func canonicalRequest(id int64, t instancespec.Type, j instancespec.Java) agentapi.EnsureRequest {
	s := instancespec.Spec{Type: t, Version: "LATEST", Java: j, MemoryMiB: instancespec.MinMemoryMiB}
	if t.IsModpack() {
		pack := "template"
		s.Version, s.Modpack = "", &pack
	}
	if t == instancespec.TypeCurseForge {
		key := "template"
		s.CurseForgeAPIKey = &key
	}
	return agentapi.EnsureRequest{ID: id, Name: "template", FQDN: "template.mcpanel.invalid", Revision: 1, Spec: s}
}

// InstanceTemplateHash is the template hash of instance id with type t and java j on host h
// (LabelTemplate). It covers everything InstanceContainer fixes (image, user, mounts, limits,
// hardening, the shape of the environment) except the labels; an invalid type or java (no
// container can have it) is an error.
func InstanceTemplateHash(h Host, id int64, t instancespec.Type, j instancespec.Java) (string, error) {
	cfg, err := InstanceContainer(h, canonicalRequest(id, t, j))
	if err != nil {
		return "", err
	}
	cfg.Labels = nil
	return templateHash(struct {
		Epoch     int
		Container docker.ContainerConfig
	}{TemplateEpoch, cfg})
}

// RouterContainer builds the mc-router container: the only container with a
// published port. The image already runs as uid 65532, and the router gets no
// Docker socket; it only reads the routes file and reloads it on SIGHUP.
func RouterContainer(h Host) (docker.ContainerConfig, error) {
	img, err := pinnedImage(h.Images.Router)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	return docker.ContainerConfig{
		Image: img,
		Cmd: []string{
			"-routes-config", routerConfigDir + "/" + routesFileName,
			"-connection-rate-limit", strconv.Itoa(cmp.Or(h.RouterConnectionRateLimit, config.DefaultRouterConnectionRateLimit)),
		},
		Labels:       map[string]string{LabelRouter: "1"},
		ExposedPorts: map[string]struct{}{routerPort: {}},
		HostConfig: docker.HostConfig{
			// Directory, not file: an atomic rename of routes.json stays visible.
			Binds:          []string{RouterDir(h.InstancesDir) + ":" + routerConfigDir + ":ro"},
			NetworkMode:    RouterNetwork,
			PortBindings:   map[string][]docker.PortBinding{routerPort: {{HostPort: strconv.Itoa(MinecraftPort)}}},
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,
			Memory:         RouterMemoryBytes,
			MemorySwap:     RouterMemoryBytes,
			PidsLimit:      RouterPidsLimit,
			LogConfig:      logConfig(),
		},
		NetworkingConfig: network(RouterNetwork),
	}, nil
}
