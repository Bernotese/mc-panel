package instancespec

import (
	"strconv"
	"strings"
)

var itzgType = map[Type]string{
	TypeVanilla: "VANILLA", TypePaper: "PAPER", TypePurpur: "PURPUR", TypeFabric: "FABRIC",
	TypeNeoForge: "NEOFORGE", TypeModrinth: "MODRINTH", TypeCurseForge: "AUTO_CURSEFORGE",
}

// Env validates the spec and returns the complete itzg environment in a fixed order.
// Nothing outside this function decides which variables a container gets.
func Env(name string, s Spec) ([]string, error) {
	if !ValidName(name) {
		return nil, invalid("name", "must match %s", nameRe)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	env := []string{"EULA=TRUE", "TYPE=" + itzgType[s.Type]}
	if !s.Type.IsModpack() {
		env = append(env, "VERSION="+s.Version)
	}
	env = append(env,
		"MEMORY="+strconv.Itoa(s.MemoryMiB)+"M",
		"ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true",
		"OVERRIDE_SERVER_PROPERTIES=true",
	)
	switch s.Type {
	case TypeFabric:
		if s.LoaderVersion != nil {
			env = append(env, "FABRIC_LOADER_VERSION="+*s.LoaderVersion)
		}
	case TypeNeoForge:
		if s.LoaderVersion != nil {
			env = append(env, "NEOFORGE_VERSION="+*s.LoaderVersion)
		}
	case TypeModrinth:
		env = append(env, "MODRINTH_MODPACK="+*s.Modpack)
	case TypeCurseForge:
		if strings.HasPrefix(*s.Modpack, "https://") {
			env = append(env, "CF_PAGE_URL="+*s.Modpack)
		} else {
			env = append(env, "CF_SLUG="+*s.Modpack)
		}
		env = append(env, "CF_API_KEY="+*s.CurseForgeAPIKey)
	}
	p := Resolve(name, s.Properties)
	b := strconv.FormatBool
	env = append(env,
		"MOTD="+*p.MOTD,
		"DIFFICULTY="+*p.Difficulty,
		"MODE="+*p.Gamemode,
		"MAX_PLAYERS="+strconv.Itoa(*p.MaxPlayers),
		"PVP="+b(*p.PVP),
		"HARDCORE="+b(*p.Hardcore),
		"ALLOW_FLIGHT="+b(*p.AllowFlight),
		"ENABLE_COMMAND_BLOCK="+b(*p.EnableCommandBlock),
		"ONLINE_MODE="+b(*p.OnlineMode),
		"ENABLE_WHITELIST="+b(*p.WhitelistEnabled),
	)
	// No OPS/WHITELIST: without them itzg leaves ops.json and whitelist.json alone, so the
	// lists the server keeps (and the panel changes over RCON) survive every recreate.
	env = append(env,
		"VIEW_DISTANCE="+strconv.Itoa(*p.ViewDistance),
		"SIMULATION_DISTANCE="+strconv.Itoa(*p.SimulationDistance),
		"SPAWN_PROTECTION="+strconv.Itoa(*p.SpawnProtection),
	)
	if *p.Seed != "" {
		env = append(env, "SEED="+*p.Seed)
	}
	env = append(env, "LEVEL_TYPE=minecraft:"+*p.LevelType)
	// Defence in depth (spec §5.2): no value may carry a control character, whatever
	// future field validation might miss.
	for _, kv := range env {
		if strings.ContainsFunc(kv, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, invalid("env", "control character in %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
	return env, nil
}
