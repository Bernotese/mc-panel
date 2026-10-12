package upstream_test

import (
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestNewDefaults(t *testing.T) {
	if upstream.DefaultBases != (upstream.Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}) {
		t.Fatalf("DefaultBases = %+v", upstream.DefaultBases)
	}
	if !slices.Equal(upstream.AllowedHosts, []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}) {
		t.Fatalf("AllowedHosts = %v", upstream.AllowedHosts)
	}
	c := upstream.New(upstream.Options{Clock: clock.NewFake(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))})
	if c.Bases() != upstream.DefaultBases {
		t.Fatalf("Bases() = %+v, want the defaults", c.Bases())
	}
	if c.UserAgent() != "mc-panel/dev (https://github.com/Bernotese/mc-panel)" {
		t.Fatalf("UserAgent() = %q", c.UserAgent())
	}
	test := upstream.Bases{Modrinth: "https://127.0.0.1:1", Hangar: "https://127.0.0.1:2"}
	c = upstream.New(upstream.Options{Version: "1.2.3", Bases: test})
	if c.Bases() != test || c.UserAgent() != "mc-panel/1.2.3 (https://github.com/Bernotese/mc-panel)" {
		t.Fatalf("Bases() = %+v, UserAgent() = %q", c.Bases(), c.UserAgent())
	}
}

func TestDefaultHostAllowed(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://cdn.modrinth.com/data/Vebnzrzj/versions/abc/LuckPerms-Bukkit-5.5.72.jar":                      true,
		"https://hangarcdn.papermc.io/plugins/ViaVersion/ViaVersion/versions/5.5.1/PAPER/ViaVersion-5.5.1.jar": true,
		"https://CDN.Modrinth.com/data/x/icon.png":                                                             true,
		"https://cdn.modrinth.com:443/x.jar":                                                                   true,
		"http://cdn.modrinth.com/x.jar":                                                                        false,
		"https://cdn.modrinth.com:8443/x.jar":                                                                  false,
		"https://user:pw@cdn.modrinth.com/x.jar":                                                               false,
		"https://cdn.modrinth.com.evil.example/x.jar":                                                          false,
		"https://evil.example/cdn.modrinth.com/x.jar":                                                          false,
		"https://modrinth.com/plugin/luckperms":                                                                false,
		"https://api.modrinth.com/v2/project/luckperms":                                                        false, // API host, not a download host
		"https://hangar.papermc.io/api/v1/projects/ViaVersion":                                                 false,
		"ftp://cdn.modrinth.com/x.jar":                                                                         false,
		"//cdn.modrinth.com/x.jar":                                                                             false,
		"cdn.modrinth.com/x.jar":                                                                               false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := upstream.DefaultHostAllowed(u); got != want {
			t.Errorf("DefaultHostAllowed(%s) = %v, want %v", raw, got, want)
		}
	}
	if upstream.DefaultHostAllowed(nil) {
		t.Fatal("nil URL allowed")
	}
}

// Tests inject AllowHost for their httptest TLS servers; it replaces the default check.
func TestHostAllowedOverride(t *testing.T) {
	c := upstream.New(upstream.Options{AllowHost: func(u *url.URL) bool { return u.Host == "127.0.0.1:8443" }})
	local, _ := url.Parse("https://127.0.0.1:8443/x.jar")
	cdn, _ := url.Parse("https://cdn.modrinth.com/x.jar")
	if !c.HostAllowed(local) || c.HostAllowed(cdn) || c.HostAllowed(nil) {
		t.Fatal("AllowHost does not replace the default check")
	}
	if d := upstream.New(upstream.Options{}); !d.HostAllowed(cdn) || d.HostAllowed(local) {
		t.Fatal("default client must use DefaultHostAllowed")
	}
}
