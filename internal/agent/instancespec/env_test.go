package instancespec_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// allowedKeys is every environment variable Env may ever produce. Anything else
// (JVM_OPTS, GENERIC_PACK, MODS, PLUGINS, OPS, WHITELIST, ...) would let the panel smuggle in
// code or override what the server itself manages (ops and whitelist, spec 3a §3.1).
var allowedKeys = []string{
	"EULA", "TYPE", "VERSION", "MEMORY", "ENABLE_AUTOPAUSE", "ENABLE_RCON", "OVERRIDE_SERVER_PROPERTIES", "BROADCAST_RCON_TO_OPS",
	"FABRIC_LOADER_VERSION", "NEOFORGE_VERSION", "MODRINTH_MODPACK", "CF_SLUG", "CF_PAGE_URL", "CF_API_KEY",
	"MOTD", "DIFFICULTY", "MODE", "MAX_PLAYERS", "PVP", "HARDCORE", "ALLOW_FLIGHT", "ENABLE_COMMAND_BLOCK",
	"ONLINE_MODE", "ENABLE_WHITELIST", "VIEW_DISTANCE", "SIMULATION_DISTANCE",
	"SPAWN_PROTECTION", "SEED", "LEVEL_TYPE",
}

func TestEnvPaper(t *testing.T) {
	s := validSpec()
	s.Properties = instancespec.Properties{MOTD: ptr("Hallo"), WhitelistEnabled: ptr(true)}
	env, err := instancespec.Env("survival", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true", "BROADCAST_RCON_TO_OPS=false",
		"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
		"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=true",
		"VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
		"LEVEL_TYPE=minecraft:normal",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("env =\n%q\nwant\n%q", env, want)
	}
}

func TestEnvPerType(t *testing.T) {
	cases := []struct {
		spec instancespec.Spec
		has  []string
		not  []string
	}{
		{
			spec: instancespec.Spec{Type: instancespec.TypeFabric, Version: "26.3", LoaderVersion: ptr("0.19.5"), Java: instancespec.Java25, MemoryMiB: 2048},
			has:  []string{"TYPE=FABRIC", "VERSION=26.3", "FABRIC_LOADER_VERSION=0.19.5", "MEMORY=2048M"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeNeoForge, Version: "1.21.4", LoaderVersion: ptr("21.4.140"), Java: instancespec.Java21, MemoryMiB: 6144},
			has:  []string{"TYPE=NEOFORGE", "NEOFORGE_VERSION=21.4.140"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeModrinth, Modpack: ptr("cobblemon-fabric"), Java: instancespec.Java21, MemoryMiB: 6144},
			has:  []string{"TYPE=MODRINTH", "MODRINTH_MODPACK=cobblemon-fabric"},
			not:  []string{"VERSION"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeCurseForge, Modpack: ptr("atm10"), CurseForgeAPIKey: ptr("$2a$10$k"), Java: instancespec.Java21, MemoryMiB: 8192},
			has:  []string{"TYPE=AUTO_CURSEFORGE", "CF_SLUG=atm10", "CF_API_KEY=$2a$10$k"},
			not:  []string{"VERSION", "CF_PAGE_URL"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeCurseForge, Modpack: ptr("https://www.curseforge.com/minecraft/modpacks/atm10"), CurseForgeAPIKey: ptr("k"), Java: instancespec.Java21, MemoryMiB: 8192},
			has:  []string{"CF_PAGE_URL=https://www.curseforge.com/minecraft/modpacks/atm10"},
			not:  []string{"CF_SLUG"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeVanilla, Version: "LATEST", Java: instancespec.Java25, MemoryMiB: 1024,
				Properties: instancespec.Properties{Seed: ptr("42"), LevelType: ptr("amplified")}},
			has: []string{"TYPE=VANILLA", "VERSION=LATEST", "SEED=42", "LEVEL_TYPE=minecraft:amplified"},
			not: []string{"WHITELIST", "OPS", "EXISTING_OPS_FILE", "EXISTING_WHITELIST_FILE"},
		},
	}
	for _, c := range cases {
		env, err := instancespec.Env("srv", c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec.Type, err)
		}
		keys := map[string]bool{}
		for _, kv := range env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok || !slices.Contains(allowedKeys, k) || keys[k] {
				t.Errorf("%s: unexpected or duplicate env entry %q", c.spec.Type, kv)
			}
			keys[k] = true
		}
		for _, h := range c.has {
			if !slices.Contains(env, h) {
				t.Errorf("%s: missing %q in %q", c.spec.Type, h, env)
			}
		}
		for _, n := range c.not {
			if keys[n] {
				t.Errorf("%s: must not set %s", c.spec.Type, n)
			}
		}
	}
}

// The server owns ops.json and whitelist.json (spec 3a §3.1): Env never sends lists or sync
// modes, whatever the whitelist switch says.
func TestEnvNeverSendsPlayerLists(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := validSpec()
		s.Properties = instancespec.Properties{WhitelistEnabled: ptr(enabled)}
		env, err := instancespec.Env("survival", s)
		if err != nil {
			t.Fatal(err)
		}
		for _, kv := range env {
			k, _, _ := strings.Cut(kv, "=")
			if slices.Contains([]string{"WHITELIST", "OPS", "EXISTING_OPS_FILE", "EXISTING_WHITELIST_FILE"}, k) {
				t.Fatalf("whitelist_enabled=%v: env contains %q", enabled, kv)
			}
		}
		if want := "ENABLE_WHITELIST=" + strconv.FormatBool(enabled); !slices.Contains(env, want) {
			t.Fatalf("missing %s in %q", want, env)
		}
	}
}

// The panel's RCON commands (console, player actions, TPS polls) must not be echoed to
// in-game operators: itzg leaves broadcast-rcon-to-ops at the vanilla default true.
// No spec field can change it.
func TestEnvNeverBroadcastsRconToOps(t *testing.T) {
	for _, s := range []instancespec.Spec{
		validSpec(),
		{Type: instancespec.TypeFabric, Version: "26.3", Java: instancespec.Java25, MemoryMiB: 2048},
		{Type: instancespec.TypeModrinth, Modpack: ptr("cobblemon-fabric"), Java: instancespec.Java21, MemoryMiB: 6144},
	} {
		env, err := instancespec.Env("survival", s)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, kv := range env {
			if strings.HasPrefix(kv, "BROADCAST_RCON_TO_OPS=") {
				n++
				if kv != "BROADCAST_RCON_TO_OPS=false" {
					t.Fatalf("%s: %q", s.Type, kv)
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s: BROADCAST_RCON_TO_OPS appears %d times in %q", s.Type, n, env)
		}
	}
}

func TestEnvRejectsInvalidSpec(t *testing.T) {
	s := validSpec()
	s.Properties.MOTD = ptr("x\nJVM_OPTS=-Dfoo")
	if _, err := instancespec.Env("survival", s); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if _, err := instancespec.Env("Bad Name", validSpec()); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("invalid name: want ErrInvalid, got %v", err)
	}
}
