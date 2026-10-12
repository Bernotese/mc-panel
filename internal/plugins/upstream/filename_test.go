package upstream_test

import (
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// Review Focus #1: an upstream file name never leaves plugins/ or mods/ and never hides a file.
func TestFileName(t *testing.T) {
	for _, tc := range []struct{ name, slug, version, want string }{
		{"LuckPerms-Bukkit-5.5.71.jar", "luckperms", "v5.5.71-bukkit", "LuckPerms-Bukkit-5.5.71.jar"},
		{"fabric-api-0.141.6+1.21.11.jar", "fabric-api", "0.141.6+1.21.11", "fabric-api-0.141.6+1.21.11.jar"},
		{"../evil.jar", "via", "1.0", "via-1.0.jar"},
		{"a/b.jar", "via", "1.0", "via-1.0.jar"},
		{`a\b.jar`, "via", "1.0", "via-1.0.jar"},
		{"x\x00.jar", "via", "1.0", "via-1.0.jar"},
		{".hidden.jar", "via", "1.0", "via-1.0.jar"},
		{".mcpanel-tmp.jar", "via", "1.0", "via-1.0.jar"},
		{"évil.jar", "via", "1.0", "via-1.0.jar"},
		{"x.JAR", "via", "1.0", "via-1.0.jar"},
		{"x.zip", "via", "1.0", "via-1.0.jar"},
		{"", "my plugin", "1.0 beta/2", "my-plugin-1.0-beta-2.jar"},
		{"", "ViaBackwards", "5.12.2-SNAPSHOT+657", "ViaBackwards-5.12.2-SNAPSHOT+657.jar"},
		{"", "", "", "plugin.jar"},
		{"", "-..-", "", "plugin.jar"},
		{"", "../..", "x", "x.jar"},
	} {
		if got := upstream.FileName(tc.name, tc.slug, tc.version); got != tc.want {
			t.Errorf("FileName(%q, %q, %q) = %q, want %q", tc.name, tc.slug, tc.version, got, tc.want)
		}
	}
	long := upstream.FileName(strings.Repeat("a", 200)+".jar", strings.Repeat("b", 200), "1")
	if len(long) != upstream.MaxFileNameLen || !strings.HasSuffix(long, ".jar") || !upstream.ValidJarName(long) {
		t.Fatalf("long name = %q (%d)", long, len(long))
	}
}

func TestValidJarName(t *testing.T) {
	for name, want := range map[string]bool{
		"a.jar":                                 true,
		"A" + strings.Repeat("b", 127) + ".jar": true,
		"A" + strings.Repeat("b", 128) + ".jar": false,
		"-a.jar":                                false,
		"a.jar.disabled":                        false,
		"a b.jar":                               false,
	} {
		if got := upstream.ValidJarName(name); got != want {
			t.Errorf("ValidJarName(%.20q) = %v, want %v", name, got, want)
		}
	}
}
