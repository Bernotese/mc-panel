package instancespec_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// allowedKeys is every environment variable Env may ever produce. Anything else
// (JVM_OPTS, GENERIC_PACK, MODS, PLUGINS, ...) would let the panel smuggle in code.
var allowedKeys = []string{
	"EULA", "TYPE", "VERSION", "MEMORY", "ENABLE_AUTOPAUSE", "ENABLE_RCON", "OVERRIDE_SERVER_PROPERTIES",
	"FABRIC_LOADER_VERSION", "NEOFORGE_VERSION", "MODRINTH_MODPACK", "CF_SLUG", "CF_PAGE_URL", "CF_API_KEY",
	"MOTD", "DIFFICULTY", "MODE", "MAX_PLAYERS", "PVP", "HARDCORE", "ALLOW_FLIGHT", "ENABLE_COMMAND_BLOCK",
	"ONLINE_MODE", "ENABLE_WHITELIST", "WHITELIST", "OPS", "VIEW_DISTANCE", "SIMULATION_DISTANCE",
	"SPAWN_PROTECTION", "SEED", "LEVEL_TYPE",
}

func TestEnvPaper(t *testing.T) {
	s := validSpec()
	s.Properties = instancespec.Properties{MOTD: ptr("Hallo"), Whitelist: []string{"Notch", "jeb_"}, WhitelistEnabled: ptr(true)}
	env, err := instancespec.Env("survival", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true",
		"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
		"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=true",
		"WHITELIST=Notch,jeb_", "VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
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
				Properties: instancespec.Properties{Seed: ptr("42"), LevelType: ptr("amplified"), Ops: []string{"Notch"}}},
			has: []string{"TYPE=VANILLA", "VERSION=LATEST", "SEED=42", "LEVEL_TYPE=minecraft:amplified", "OPS=Notch"},
			not: []string{"WHITELIST"},
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
