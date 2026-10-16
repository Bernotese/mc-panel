package instances_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/config"
)

func ptr[T any](v T) *T { return &v }

func testHost() instances.Host {
	return instances.Host{InstancesDir: "/srv/mc-panel", UID: 996, GID: 995, Images: config.DefaultImages()}
}

func paperRequest() agentapi.EnsureRequest {
	return agentapi.EnsureRequest{
		ID: 7, Name: "survival", FQDN: "survival.mc.example.com", Revision: 3,
		Spec: instancespec.Spec{
			Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096,
			Properties: instancespec.Properties{MOTD: ptr("Hallo")},
		},
	}
}

func TestInstanceContainerGolden(t *testing.T) {
	got, err := instances.InstanceContainer(testHost(), paperRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := docker.ContainerConfig{
		Image: "itzg/minecraft-server@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa",
		User:  "996:995",
		Env: []string{
			"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
			"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true",
			"PATCH_DEFINITIONS=/mcpanel-patches/bukkit-connection-throttle.json",
			"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
			"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=false",
			"VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
			"LEVEL_TYPE=minecraft:normal",
		},
		Hostname: "survival",
		Labels: map[string]string{
			"mcpanel.instance": "7",
			"mcpanel.revision": "3",
			"mcpanel.name":     "survival",
			"mcpanel.fqdn":     "survival.mc.example.com",
		},
		StopTimeout: 60,
		HostConfig: docker.HostConfig{
			Binds: []string{
				"/srv/mc-panel/instances/7:/data", "/srv/mc-panel/tmp/7:/tmp",
				"/srv/mc-panel/patches:/mcpanel-patches:ro",
			},
			NetworkMode:    "mcpanel-net-7", // its own network: no other server reaches it (SEC-03)
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,       // the image only writes /data and /tmp
			Memory:         5120 << 20, // 4096 MiB × 1.25
			MemorySwap:     5120 << 20,
			PidsLimit:      1024,
			CPUShares:      256, // cgroup v2 weight ≈ 10: a busy server never starves panel, agent or router
			Ulimits:        []docker.Ulimit{{Name: "nofile", Soft: 65536, Hard: 65536}},
			LogConfig:      docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-net-7": {}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template =\n%+v\nwant\n%+v", got, want)
	}
}

func TestRouterContainerGolden(t *testing.T) {
	got, err := instances.RouterContainer(testHost())
	if err != nil {
		t.Fatal(err)
	}
	want := docker.ContainerConfig{
		Image:        "itzg/mc-router@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e",
		Cmd:          []string{"-routes-config", "/config/routes.json", "-connection-rate-limit", "50"},
		Labels:       map[string]string{"mcpanel.router": "1"},
		ExposedPorts: map[string]struct{}{"25565/tcp": {}},
		HostConfig: docker.HostConfig{
			Binds:          []string{"/srv/mc-panel/router:/config:ro"},
			NetworkMode:    "mcpanel-mc",
			PortBindings:   map[string][]docker.PortBinding{"25565/tcp": {{HostPort: "25565"}}},
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,
			Memory:         256 << 20,
			MemorySwap:     256 << 20,
			PidsLimit:      128,
			LogConfig:      docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-mc": {}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("router template =\n%+v\nwant\n%+v", got, want)
	}
	if slices.Contains(got.Cmd, "-routes-config-watch") {
		t.Fatal("the watch flag never removes routes; reload happens via SIGHUP")
	}
}

// allowedEnvKeys mirrors the allowlist of instancespec.Env: the template must never add anything.
var allowedEnvKeys = []string{
	"EULA", "TYPE", "VERSION", "MEMORY", "ENABLE_AUTOPAUSE", "ENABLE_RCON", "OVERRIDE_SERVER_PROPERTIES", "PATCH_DEFINITIONS",
	"FABRIC_LOADER_VERSION", "NEOFORGE_VERSION", "MODRINTH_MODPACK", "CF_SLUG", "CF_PAGE_URL", "CF_API_KEY",
	"MOTD", "DIFFICULTY", "MODE", "MAX_PLAYERS", "PVP", "HARDCORE", "ALLOW_FLIGHT", "ENABLE_COMMAND_BLOCK",
	"ONLINE_MODE", "ENABLE_WHITELIST", "VIEW_DISTANCE", "SIMULATION_DISTANCE",
	"SPAWN_PROTECTION", "SEED", "LEVEL_TYPE",
}

func variedRequests() []agentapi.EnsureRequest {
	full := instancespec.Properties{
		MOTD: ptr("§aMOTD with = and : and ,"), Difficulty: ptr("hard"), Gamemode: ptr("creative"), MaxPlayers: ptr(500),
		PVP: ptr(false), Hardcore: ptr(true), AllowFlight: ptr(true), EnableCommandBlock: ptr(true), OnlineMode: ptr(false),
		WhitelistEnabled: ptr(true), ViewDistance: ptr(32), SimulationDistance: ptr(3),
		SpawnProtection: ptr(0), Seed: ptr("-42=JVM_OPTS"), LevelType: ptr("amplified"),
	}
	specs := []instancespec.Spec{
		{Type: instancespec.TypeVanilla, Version: "LATEST", Java: instancespec.Java25, MemoryMiB: 1024},
		{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 32768, Properties: full},
		{Type: instancespec.TypePurpur, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 2048},
		{Type: instancespec.TypeFabric, Version: "26.3", LoaderVersion: ptr("0.19.5"), Java: instancespec.Java25, MemoryMiB: 4096},
		{Type: instancespec.TypeNeoForge, Version: "1.21.4", LoaderVersion: ptr("21.4.140"), Java: instancespec.Java21, MemoryMiB: 6144},
		{Type: instancespec.TypeModrinth, Modpack: ptr("cobblemon-fabric"), Java: instancespec.Java21, MemoryMiB: 8192},
		{Type: instancespec.TypeCurseForge, Modpack: ptr("atm10"), CurseForgeAPIKey: ptr("$2a$10$key"), Java: instancespec.Java17, MemoryMiB: 8192},
		{Type: instancespec.TypeCurseForge, Modpack: ptr("https://www.curseforge.com/minecraft/modpacks/atm10"), CurseForgeAPIKey: ptr("k"), Java: instancespec.Java21, MemoryMiB: 8192, Properties: full},
		{Type: instancespec.TypeVanilla, Version: "1.16.5", Java: instancespec.Java8, MemoryMiB: 1280},
	}
	reqs := make([]agentapi.EnsureRequest, len(specs))
	for i, s := range specs {
		reqs[i] = agentapi.EnsureRequest{ID: int64(i + 1), Name: "srv-" + string(rune('a'+i)), Revision: int64(i + 1), Spec: s}
		reqs[i].FQDN = reqs[i].Name + ".mc.example.com"
	}
	return reqs
}

// TestInstanceContainerInvariants: no spec can add mounts, ports, capabilities,
// privileges or environment variables beyond the allowlist.
func TestInstanceContainerInvariants(t *testing.T) {
	h := testHost()
	for _, req := range variedRequests() {
		c, err := instances.InstanceContainer(h, req)
		if err != nil {
			t.Fatalf("%s (%s): %v", req.Name, req.Spec.Type, err)
		}
		hc := c.HostConfig
		if !slices.Equal(hc.Binds, []string{instances.DataDir("/srv/mc-panel", req.ID) + ":/data", instances.TmpDir("/srv/mc-panel", req.ID) + ":/tmp",
			instances.PatchesDir("/srv/mc-panel") + ":/mcpanel-patches:ro"}) {
			t.Errorf("%s: binds = %v", req.Name, hc.Binds)
		}
		if hc.PortBindings != nil || c.ExposedPorts != nil || hc.CapAdd != nil || hc.Privileged {
			t.Errorf("%s: ports/caps/privileges must stay unset: %+v", req.Name, hc)
		}
		if !hc.ReadonlyRootfs || hc.NanoCPUs != 0 ||
			!slices.Equal(hc.Ulimits, []docker.Ulimit{{Name: "nofile", Soft: 65536, Hard: 65536}}) {
			t.Errorf("%s: rootfs/tmpfs/limits changed: %+v", req.Name, hc)
		}
		if !slices.Equal(hc.CapDrop, []string{"ALL"}) || !slices.Equal(hc.SecurityOpt, []string{"no-new-privileges:true"}) ||
			hc.NetworkMode != instances.InstanceNetwork(req.ID) || hc.PidsLimit != 1024 || hc.RestartPolicy.Name != "unless-stopped" {
			t.Errorf("%s: hardening changed: %+v", req.Name, hc)
		}
		if want := int64(req.Spec.MemoryMiB) * 5 << 20 / 4; hc.Memory != want || hc.MemorySwap != want {
			t.Errorf("%s: memory = %d/%d, want %d", req.Name, hc.Memory, hc.MemorySwap, want)
		}
		if c.User != "996:995" || c.Cmd != nil || c.StopTimeout != 60 || c.Hostname != req.Name {
			t.Errorf("%s: user/cmd/stop/hostname = %q %v %d %q", req.Name, c.User, c.Cmd, c.StopTimeout, c.Hostname)
		}
		if len(c.Labels) != 4 || c.Labels["mcpanel.instance"] == "" || c.Labels["mcpanel.fqdn"] != req.FQDN {
			t.Errorf("%s: labels = %v", req.Name, c.Labels)
		}
		ref, _ := h.Images.ByJava(string(req.Spec.Java))
		if !strings.HasSuffix(ref, strings.TrimPrefix(c.Image, "itzg/minecraft-server")) {
			t.Errorf("%s: image %q does not match %s", req.Name, c.Image, req.Spec.Java)
		}
		seen := map[string]bool{}
		for _, kv := range c.Env {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || !slices.Contains(allowedEnvKeys, k) || seen[k] || strings.ContainsAny(v, "\x00\n\r") {
				t.Errorf("%s: env entry %q not allowed", req.Name, kv)
			}
			seen[k] = true
		}
	}
}

func TestInstanceContainerRejectsInvalidRequests(t *testing.T) {
	h := testHost()
	cases := map[string]func(*agentapi.EnsureRequest){
		"motd newline": func(r *agentapi.EnsureRequest) { r.Spec.Properties.MOTD = ptr("x\nJVM_OPTS=-Xmx64g") },
		"seed nul":     func(r *agentapi.EnsureRequest) { r.Spec.Properties.Seed = ptr("1\x002") },
		"name":         func(r *agentapi.EnsureRequest) { r.Name = "../etc" },
		"fqdn":         func(r *agentapi.EnsureRequest) { r.FQDN = "evil.example.com" },
		"id":           func(r *agentapi.EnsureRequest) { r.ID = -1 },
		"java":         func(r *agentapi.EnsureRequest) { r.Spec.Java = "java11" },
	}
	for name, mod := range cases {
		r := paperRequest()
		mod(&r)
		if c, err := instances.InstanceContainer(h, r); !errors.Is(err, instancespec.ErrInvalid) || c.Image != "" {
			t.Errorf("%s: err = %v, config = %+v", name, err, c)
		}
	}
	bad := testHost()
	bad.Images.Java21 = "itzg/minecraft-server:java21"
	if _, err := instances.InstanceContainer(bad, paperRequest()); err == nil {
		t.Fatal("an unpinned image must never reach Docker")
	}
	bad.Images.Router = "itzg/mc-router:latest"
	if _, err := instances.RouterContainer(bad); err == nil {
		t.Fatal("an unpinned router image must never reach Docker")
	}
}

// TestTemplateHostLimits: cpu_per_instance and router_connection_rate_limit come from
// the agent config (Host), never from the panel.
func TestTemplateHostLimits(t *testing.T) {
	h := testHost()
	h.NanoCPUs = 1_500_000_000
	h.RouterConnectionRateLimit = 200
	c, err := instances.InstanceContainer(h, paperRequest())
	if err != nil {
		t.Fatal(err)
	}
	if c.HostConfig.NanoCPUs != 1_500_000_000 {
		t.Fatalf("NanoCpus = %d", c.HostConfig.NanoCPUs)
	}
	h.NanoCPUs = 1 << 50 // cpu_per_instance above this host's CPUs (e.g. a downsized VPS)
	if c, err := instances.InstanceContainer(h, paperRequest()); err != nil || c.HostConfig.NanoCPUs != instances.MaxNanoCPUs() {
		t.Fatalf("NanoCpus = %d, %v; want the clamp to %d", c.HostConfig.NanoCPUs, err, instances.MaxNanoCPUs())
	}
	r, err := instances.RouterContainer(h)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Cmd, []string{"-routes-config", "/config/routes.json", "-connection-rate-limit", "200"}) || r.HostConfig.NanoCPUs != 0 {
		t.Fatalf("router = %v, NanoCpus %d", r.Cmd, r.HostConfig.NanoCPUs)
	}
}

func TestPaths(t *testing.T) {
	if instances.ContainerName(12) != "mcpanel-mc-12" || instances.DataDir("/srv/mc-panel", 12) != "/srv/mc-panel/instances/12" ||
		instances.RouterDir("/srv/mc-panel") != "/srv/mc-panel/router" || instances.PatchesDir("/srv/mc-panel") != "/srv/mc-panel/patches" || instances.DeletedDir("/srv/mc-panel") != "/srv/mc-panel/deleted" ||
		instances.TmpDir("/srv/mc-panel", 12) != "/srv/mc-panel/tmp/12" {
		t.Fatal("paths changed")
	}
}
