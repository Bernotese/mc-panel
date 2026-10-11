package instancespec_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestPropertiesValidation(t *testing.T) {
	valid := []instancespec.Properties{
		{},
		{MOTD: ptr("§aWillkommen auf unserem Server äöü"), Difficulty: ptr("hard"), Gamemode: ptr("creative")},
		{MaxPlayers: ptr(1), ViewDistance: ptr(3), SimulationDistance: ptr(32), SpawnProtection: ptr(0)},
		{WhitelistEnabled: ptr(true)},
		{Seed: ptr("-123456789"), LevelType: ptr("large_biomes")},
	}
	for i, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("valid[%d]: %v", i, err)
		}
	}
	invalid := map[string]instancespec.Properties{
		"motd newline":     {MOTD: ptr("a\nENABLE_RCON=false")},
		"motd nul":         {MOTD: ptr("a\x00b")},
		"motd tab":         {MOTD: ptr("a\tb")},
		"motd too long":    {MOTD: ptr(strings.Repeat("ä", 151))},
		"difficulty":       {Difficulty: ptr("insane")},
		"gamemode":         {Gamemode: ptr("1")},
		"max players 0":    {MaxPlayers: ptr(0)},
		"max players 501":  {MaxPlayers: ptr(501)},
		"view distance":    {ViewDistance: ptr(2)},
		"simulation":       {SimulationDistance: ptr(33)},
		"spawn protection": {SpawnProtection: ptr(257)},
		"seed too long":    {Seed: ptr(strings.Repeat("1", 65))},
		"seed newline":     {Seed: ptr("1\n2")},
		"motd backslash u": {MOTD: ptr(`C:\users`)},
		"motd trailing bs": {MOTD: ptr(`Willkommen \`)},
		"seed backslash":   {Seed: ptr(`1\2`)},
		"level type":       {LevelType: ptr("minecraft:flat")},
	}
	for name, p := range invalid {
		if err := p.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestResolveDefaults(t *testing.T) {
	r := instancespec.Resolve("survival", instancespec.Properties{MaxPlayers: ptr(8)})
	if *r.MOTD != "survival" || *r.Difficulty != "normal" || *r.Gamemode != "survival" || *r.MaxPlayers != 8 ||
		!*r.PVP || *r.Hardcore || *r.AllowFlight || *r.EnableCommandBlock || !*r.OnlineMode || *r.WhitelistEnabled ||
		*r.ViewDistance != 10 || *r.SimulationDistance != 10 ||
		*r.SpawnProtection != 16 || *r.Seed != "" || *r.LevelType != "normal" {
		t.Fatalf("resolved = %+v", r)
	}
}

// TestListKeysAreUnknown: since sub-project 3a the server owns ops and the whitelist; a client
// that still sends them gets a validation error from the strict decoders of panel and agent.
func TestListKeysAreUnknown(t *testing.T) {
	for _, raw := range []string{`{"ops":["Notch"]}`, `{"whitelist":["Notch"]}`} {
		dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
		dec.DisallowUnknownFields()
		var p instancespec.Properties
		if err := dec.Decode(&p); err == nil {
			t.Errorf("%s: decoded without error", raw)
		}
	}
}
