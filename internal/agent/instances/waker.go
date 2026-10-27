package instances

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

// The waker mcpanel-waker (dev instances spec §6.2) answers in place of a sleeping dev
// instance: mc-router sends the dev's hostname to it, it refuses logins with a fixed text
// and logs "wake <fqdn>", which the agent reads from its container log. It is the agent
// binary in its unprivileged "waker" mode, bind-mounted read-only into the pinned mc-router
// image (a static binary needs nothing else), and it gets no Docker socket, no capability,
// no writable path and no published port.

const (
	WakerName  = "mcpanel-waker" // container
	LabelWaker = "mcpanel.waker"
	// LabelWakerTemplate carries the SHA-256 of the waker's container template and the agent
	// version; a different value (agent update: the bind mount still holds the old binary)
	// replaces the container.
	LabelWakerTemplate = "mcpanel.waker-template"
	// WakerMemoryBytes caps mcpanel-waker (a few MiB of Go heap).
	WakerMemoryBytes = 32 << 20
	WakerPidsLimit   = 32

	// wakerBinary is where the agent binary appears inside the container.
	wakerBinary = "/mcpanel-waker"
	wakerUser   = "65534:65534" // nobody
)

// WakerAddress is the routes file backend of a sleeping dev instance (MinecraftPort).
const WakerAddress = WakerName + ":25565"

// WakerContainer builds the hardened waker container. agentBinary is the absolute host path
// of the running agent binary; it never comes from the panel.
func WakerContainer(h Host, agentBinary string) (docker.ContainerConfig, error) {
	if !filepath.IsAbs(agentBinary) || filepath.Clean(agentBinary) != agentBinary || strings.ContainsAny(agentBinary, ":,\x00\n") {
		return docker.ContainerConfig{}, fmt.Errorf("waker: agent binary path %q is not usable as a bind source", agentBinary)
	}
	img, err := pinnedImage(h.Images.Router)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	return docker.ContainerConfig{
		Image:      img,
		Entrypoint: []string{wakerBinary},
		Cmd:        []string{"waker", "-listen", ":" + strconv.Itoa(MinecraftPort)},
		User:       wakerUser,
		// The Go runtime stays within the pids and memory limits on hosts with many CPUs.
		Env:    []string{"GOMAXPROCS=2", "GOMEMLIMIT=16MiB"},
		Labels: map[string]string{LabelWaker: "1"},
		HostConfig: docker.HostConfig{
			Binds:          []string{agentBinary + ":" + wakerBinary + ":ro"},
			NetworkMode:    RouterNetwork,
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,
			Memory:         WakerMemoryBytes,
			MemorySwap:     WakerMemoryBytes,
			PidsLimit:      WakerPidsLimit,
			LogConfig:      logConfig(),
		},
		NetworkingConfig: network(RouterNetwork),
	}, nil
}

// wakerTemplateHash covers the template and the agent version.
func wakerTemplateHash(cfg docker.ContainerConfig, version string) (string, error) {
	return templateHash(struct {
		Version   string
		Container docker.ContainerConfig
	}{version, cfg})
}
