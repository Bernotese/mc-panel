package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	type want struct {
		ok      bool
		max     int
		players string // "Name=uuid|Name=" (empty uuid = null)
	}
	cases := map[string]want{
		"vanilla_uuids.txt":   {true, 20, "Alex=8667ba71-b85a-4004-af54-457a9734eed7|Steve=069a79f4-44e9-4726-a5be-fca90e38aaf5"},
		"paper_empty.txt":     {true, 20, ""},
		"no_uuids.txt":        {true, 20, "Alex=|Steve="},
		"formatted.txt":       {true, 20, "Alex=8667ba71-b85a-4004-af54-457a9734eed7"},
		"bukkit_slash.txt":    {true, 20, "Alex=|Steve="},
		"bukkit_outof.txt":    {true, 50, "Alex="},
		"foreign.txt":         {false, 0, ""},
		"unknown_command.txt": {false, 0, ""},
	}
	files, err := filepath.Glob(filepath.Join("testdata", "list", "*.txt"))
	if err != nil || len(files) != len(cases) {
		t.Fatalf("testdata files = %v (%v), want %d", files, err, len(cases))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		w, ok := cases[filepath.Base(f)]
		if !ok {
			t.Fatalf("no expectation for %s", f)
		}
		players, maxP, gotOK := parseList(string(raw))
		if gotOK != w.ok || maxP != w.max || render(players) != w.players {
			t.Errorf("%s: ok=%v max=%d players=%q, want ok=%v max=%d players=%q",
				filepath.Base(f), gotOK, maxP, render(players), w.ok, w.max, w.players)
		}
	}
}

func TestParseListEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", false},
		{"There are 99999999999999999999 of a max of 20 players online: ", false}, // overflow
		{"There are 1 of a max of 20 players online: Alex (not-a-uuid)", true},    // name kept as is
		{"  There are 0 of a max of 5 players online:  \r\n", true},
	} {
		if _, _, ok := parseList(tc.in); ok != tc.ok {
			t.Errorf("parseList(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
	}
	// at most 500 entries, names cut to 64 runes
	long := "There are 600 of a max of 1000 players online: " + strings.Repeat(strings.Repeat("x", 80)+", ", 600)
	players, _, ok := parseList(long)
	if !ok || len(players) != maxListedPlayers || len([]rune(players[0].Name)) != maxPlayerName {
		t.Fatalf("long list: ok=%v len=%d name=%d", ok, len(players), len([]rune(players[0].Name)))
	}
	if p, _, _ := parseList("There are 1 of a max of 20 players online: Alex (not-a-uuid)"); p[0].Name != "Alex (not-a-uuid)" || p[0].UUID != nil {
		t.Fatalf("invalid uuid: %+v", p[0])
	}
}

func render(ps []onlinePlayer) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		u := ""
		if p.UUID != nil {
			u = *p.UUID
		}
		parts[i] = p.Name + "=" + u
	}
	return strings.Join(parts, "|")
}
