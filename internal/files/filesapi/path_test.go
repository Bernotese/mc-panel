package filesapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"":                       "",
		".":                      "",
		"server.properties":      "server.properties",
		"plugins/Essentials":     "plugins/Essentials",
		"plugins/Essentials/":    "plugins/Essentials", // one trailing slash (UI folder paths) is fine
		"world/region/r.0.0.mca": "world/region/r.0.0.mca",
		".hidden":                ".hidden",
		"Ünïcödé/日本語.yml":        "Ünïcödé/日本語.yml",
		"a..b":                   "a..b",
		strings.Repeat("a", 255): strings.Repeat("a", 255),
	}
	for in, want := range ok {
		got, err := filesapi.CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"/etc/passwd", "../x", "a/../b", "a/..", "..", "a//b", "a/./b", "./a", "a\\b", "a/b//",
		"a\x00b", "a\nb", "a\tb", "a\x7fb", "\xff\xfe", strings.Repeat("a", 256),
		strings.Repeat("a/", 2049) + "a", // > 4096 bytes
	}
	for _, in := range bad {
		if got, err := filesapi.CleanPath(in); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("CleanPath(%q) = %q, %v; want ErrInvalid", in, got, err)
		}
	}
}

func TestIsHidden(t *testing.T) {
	for in, want := range map[string]bool{
		".rcon-cli.env":           true,
		".rcon-cli.yaml":          true,
		"plugins/.rcon-cli.env":   false, // only the top-level files hold the RCON password
		".mcpanel-tmp-abc":        true,
		"world/.mcpanel-tmp-1/x":  true,
		"plugins/.mcpanel-upload": true,
		".rcon-cli.envx":          false,
		"server.properties":       false,
		"":                        false,
		".mcpanelx":               false,
	} {
		if got := filesapi.IsHidden(in); got != want {
			t.Errorf("IsHidden(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	if got := filesapi.Join("", "a"); got != "a" {
		t.Fatalf("Join(\"\", a) = %q", got)
	}
	if got := filesapi.Join("plugins", "x.jar"); got != "plugins/x.jar" {
		t.Fatalf("Join = %q", got)
	}
}

// TestCleanPathPinnedNames: look-alike slashes and C1 control characters are ordinary name
// characters (only ASCII "/" separates components); pinned so a change is deliberate.
func TestCleanPathPinnedNames(t *testing.T) {
	for _, in := range []string{"a／b", "a∕b", "a\u0085b", "\u009fx"} {
		if got, err := filesapi.CleanPath(in); err != nil || got != in {
			t.Errorf("CleanPath(%q) = %q, %v; want it unchanged", in, got, err)
		}
	}
}

// TestIsHiddenIgnoresCase: macOS dev volumes are case-insensitive, so ".RCON-CLI.ENV" opens the
// same file as ".rcon-cli.env".
func TestIsHiddenIgnoresCase(t *testing.T) {
	for in, want := range map[string]bool{
		".RCON-CLI.ENV":         true,
		".Rcon-Cli.Yaml":        true,
		".rcon-cli.env/x":       true,
		".MCPANEL-tmp":          true,
		"world/.McPanel-upload": true,
		"plugins/.RCON-CLI.ENV": false,
	} {
		if got := filesapi.IsHidden(in); got != want {
			t.Errorf("IsHidden(%q) = %v, want %v", in, got, want)
		}
	}
}
