package instancespec

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Properties are the allowlisted server.properties (spec §7). nil/empty = default.
// Ops and the whitelist are not here: the server keeps them in ops.json/whitelist.json and
// the panel changes them over RCON (spec 3a §3.1).
type Properties struct {
	MOTD               *string `json:"motd,omitempty"`
	Difficulty         *string `json:"difficulty,omitempty"`
	Gamemode           *string `json:"gamemode,omitempty"`
	MaxPlayers         *int    `json:"max_players,omitempty"`
	PVP                *bool   `json:"pvp,omitempty"`
	Hardcore           *bool   `json:"hardcore,omitempty"`
	AllowFlight        *bool   `json:"allow_flight,omitempty"`
	EnableCommandBlock *bool   `json:"enable_command_block,omitempty"`
	OnlineMode         *bool   `json:"online_mode,omitempty"`
	WhitelistEnabled   *bool   `json:"whitelist_enabled,omitempty"`
	ViewDistance       *int    `json:"view_distance,omitempty"`
	SimulationDistance *int    `json:"simulation_distance,omitempty"`
	SpawnProtection    *int    `json:"spawn_protection,omitempty"`
	Seed               *string `json:"seed,omitempty"`
	LevelType          *string `json:"level_type,omitempty"`
}

var (
	difficulties = []string{"peaceful", "easy", "normal", "hard"}
	gamemodes    = []string{"survival", "creative", "adventure", "spectator"}
	levelTypes   = []string{"normal", "flat", "large_biomes", "amplified"}
)

const (
	maxMOTDRunes = 150
	maxSeedRunes = 64
)

func (p Properties) Validate() error {
	if p.MOTD != nil {
		if err := printable("motd", *p.MOTD, maxMOTDRunes); err != nil {
			return err
		}
	}
	if err := oneOf("difficulty", p.Difficulty, difficulties); err != nil {
		return err
	}
	if err := oneOf("gamemode", p.Gamemode, gamemodes); err != nil {
		return err
	}
	if err := oneOf("level_type", p.LevelType, levelTypes); err != nil {
		return err
	}
	for _, r := range []struct {
		field    string
		v        *int
		min, max int
	}{
		{"max_players", p.MaxPlayers, 1, 500},
		{"view_distance", p.ViewDistance, 3, 32},
		{"simulation_distance", p.SimulationDistance, 3, 32},
		{"spawn_protection", p.SpawnProtection, 0, 256},
	} {
		if r.v != nil && (*r.v < r.min || *r.v > r.max) {
			return invalid(r.field, "must be %d..%d", r.min, r.max)
		}
	}
	if p.Seed != nil {
		if err := printable("seed", *p.Seed, maxSeedRunes); err != nil {
			return err
		}
	}
	return nil
}

// printable allows any printable Unicode (including § color codes) but no control
// characters, so a value can never break out of its environment variable, and no
// backslash: server.properties is read with java.util.Properties, where a trailing
// backslash continues the line and "\u" without four hex digits aborts the load.
func printable(field, s string, maxRunes int) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return invalid(field, "must be valid UTF-8 with at most %d characters", maxRunes)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return invalid(field, "must not contain control characters")
		}
		if r == '\\' {
			return invalid(field, "must not contain a backslash")
		}
	}
	return nil
}

func oneOf(field string, v *string, allowed []string) error {
	if v != nil && !slices.Contains(allowed, *v) {
		return invalid(field, "must be one of %s", strings.Join(allowed, ", "))
	}
	return nil
}

// Resolve returns p with every unset field replaced by its default (spec §7).
func Resolve(name string, p Properties) Properties {
	str := func(v *string, d string) *string {
		if v != nil {
			return v
		}
		return &d
	}
	num := func(v *int, d int) *int {
		if v != nil {
			return v
		}
		return &d
	}
	flag := func(v *bool, d bool) *bool {
		if v != nil {
			return v
		}
		return &d
	}
	return Properties{
		MOTD:               str(p.MOTD, name),
		Difficulty:         str(p.Difficulty, "normal"),
		Gamemode:           str(p.Gamemode, "survival"),
		MaxPlayers:         num(p.MaxPlayers, 20),
		PVP:                flag(p.PVP, true),
		Hardcore:           flag(p.Hardcore, false),
		AllowFlight:        flag(p.AllowFlight, false),
		EnableCommandBlock: flag(p.EnableCommandBlock, false),
		OnlineMode:         flag(p.OnlineMode, true),
		WhitelistEnabled:   flag(p.WhitelistEnabled, false),
		ViewDistance:       num(p.ViewDistance, 10),
		SimulationDistance: num(p.SimulationDistance, 10),
		SpawnProtection:    num(p.SpawnProtection, 16),
		Seed:               str(p.Seed, ""),
		LevelType:          str(p.LevelType, "normal"),
	}
}
