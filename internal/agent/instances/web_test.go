package instances_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestWebAddress(t *testing.T) {
	for _, c := range []struct {
		subnet string
		want   string
		ok     bool
	}{
		{"10.213.0.16/28", "10.213.0.30", true},
		{"10.213.0.0/24", "10.213.0.254", true},
		{"10.213.0.21/28", "10.213.0.30", true}, // host bits are ignored
		{"10.213.0.0/30", "10.213.0.2", true},
		{"10.213.0.0/31", "", false},
		{"10.213.0.1/32", "", false},
		{"fd00::/64", "", false},
	} {
		got, ok := instances.WebAddress(netip.MustParsePrefix(c.subnet))
		if ok != c.ok || (ok && got.String() != c.want) {
			t.Errorf("WebAddress(%s) = %v, %v; want %s, %v", c.subnet, got, ok, c.want, c.ok)
		}
	}
	if _, ok := instances.WebAddress(netip.Prefix{}); ok {
		t.Error("WebAddress(invalid) must fail")
	}
}

func TestWebContainerGolden(t *testing.T) {
	h := testHost()
	h.WebProxyPort, h.WebAskPort = 18200, 18201
	got, err := instances.WebContainer(h)
	if err != nil {
		t.Fatal(err)
	}
	want := docker.ContainerConfig{
		Image: "caddy@sha256:d8542f48d34a9cf4e4c11a478865229840e87e4c96ea3f439101f31a5d35f75f",
		Cmd:   []string{"caddy", "run", "--config", "/etc/mcpanel-web/caddy.json", "--watch"},
		User:  "65532:65532",
		Env:   []string{"XDG_DATA_HOME=/data", "XDG_CONFIG_HOME=/config"},
		Labels: map[string]string{
			"mcpanel.web": "1",
		},
		ExposedPorts: map[string]struct{}{"8080/tcp": {}, "8081/tcp": {}},
		HostConfig: docker.HostConfig{
			Binds:       []string{"/srv/mc-panel/web:/etc/mcpanel-web:ro"},
			NetworkMode: "mcpanel-web",
			PortBindings: map[string][]docker.PortBinding{
				"8080/tcp": {{HostIP: "127.0.0.1", HostPort: "18200"}},
				"8081/tcp": {{HostIP: "127.0.0.1", HostPort: "18201"}},
			},
			Tmpfs: map[string]string{
				"/data":   "rw,noexec,nosuid,nodev,uid=65532,gid=65532,mode=0700,size=16m",
				"/config": "rw,noexec,nosuid,nodev,uid=65532,gid=65532,mode=0700,size=1m",
			},
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"},
			// exec of /usr/bin/caddy (file capability) fails without it in the bounding set
			CapAdd:         []string{"NET_BIND_SERVICE"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,
			Memory:         128 << 20,
			MemorySwap:     128 << 20,
			PidsLimit:      128,
			LogConfig:      docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-web": {}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("web template =\n%+v\nwant\n%+v", got, want)
	}
}

func TestWebContainerDefaultsAndPinning(t *testing.T) {
	got, err := instances.WebContainer(testHost()) // no ports set: the agent.toml defaults
	if err != nil {
		t.Fatal(err)
	}
	for port, host := range map[string]string{"8080/tcp": "18100", "8081/tcp": "18101"} {
		b := got.HostConfig.PortBindings[port]
		if len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != host {
			t.Errorf("binding %s = %+v, want 127.0.0.1:%s only", port, b, host)
		}
	}
	if len(got.HostConfig.PortBindings) != 2 || got.HostConfig.Privileged || !slices.Equal(got.HostConfig.CapAdd, []string{"NET_BIND_SERVICE"}) {
		t.Fatalf("hardening = %+v", got.HostConfig)
	}
	for _, b := range got.HostConfig.Binds {
		if !strings.HasSuffix(b, ":ro") || strings.Contains(b, "docker.sock") {
			t.Errorf("bind %q must be read-only and never the Docker socket", b)
		}
	}

	h := testHost()
	h.Images.Web = "caddy:2.11.7-alpine"
	if _, err := instances.WebContainer(h); err == nil {
		t.Fatal("an unpinned image must be refused")
	}
}

func TestWebDir(t *testing.T) {
	if got := instances.WebDir("/srv/mc-panel"); got != "/srv/mc-panel/web" {
		t.Fatalf("WebDir = %q", got)
	}
}

func TestRenderWebConfigGolden(t *testing.T) {
	// Reverse id order in the input; the output is sorted by id. Route 3 has a root and a
	// path, route 12 only a root, route 20 only a path (its root answers 404). The second
	// name looks odd but is a valid DNS name and must appear verbatim (and only) in its two
	// places.
	routes := []agentapi.WebRoute{
		{ID: 20, FQDN: "paths.example.com", Targets: []agentapi.WebTarget{{Path: "map", Port: 8300}}},
		{ID: 12, FQDN: "xn--mnchen-3ya.0-9.example.com", Targets: []agentapi.WebTarget{{Port: 65535}}},
		{ID: 3, FQDN: "survival.example.com", Targets: []agentapi.WebTarget{{Port: 8100}, {Path: "afk-smoke", Port: 8200}}},
	}
	got, err := instances.RenderWebConfig(routes, netip.MustParsePrefix("10.213.255.240/28"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "web-config.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("RenderWebConfig =\n%s\nwant\n%s", got, want)
	}
	if routes[0].ID != 20 {
		t.Fatal("RenderWebConfig must not reorder the caller's slice")
	}
	again, err := instances.RenderWebConfig([]agentapi.WebRoute{routes[2], routes[0], routes[1]}, netip.MustParsePrefix("10.213.255.240/28"))
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("equal routes must give equal bytes: %v", err)
	}
}

// The parts of the rendered config that TestRenderWebConfigPathMatchers checks.
type (
	renderedRoute struct {
		Match []struct {
			Host []string `json:"host"`
			Path []string `json:"path"`
		} `json:"match"`
		Handle   []renderedHandler `json:"handle"`
		Terminal bool              `json:"terminal"`
	}
	renderedHandler struct {
		Handler         string          `json:"handler"`
		Routes          []renderedRoute `json:"routes"`
		StripPathPrefix string          `json:"strip_path_prefix"`
		Request         struct {
			Set map[string][]string `json:"set"`
		} `json:"request"`
		Upstreams []struct {
			Dial string `json:"dial"`
		} `json:"upstreams"`
		StatusCode int                 `json:"status_code"`
		Headers    map[string][]string `json:"headers"`
		Body       string              `json:"body"`
	}
)

// Path targets come before the root, "/p" redirects, "/p/*" strips the prefix; the only path
// matchers are exactly "/p" and "/p/*", so "/afk-smokeX/" and "/afk-smoke-old/" never reach
// the afk-smoke route (Caddy's path matcher matches "/p/*" by the prefix "/p/").
func TestRenderWebConfigPathMatchers(t *testing.T) {
	b, err := instances.RenderWebConfig([]agentapi.WebRoute{
		{ID: 3, FQDN: "survival.example.com", Targets: []agentapi.WebTarget{{Port: 8100}, {Path: "afk-smoke", Port: 8200}}},
		{ID: 20, FQDN: "paths.example.com", Targets: []agentapi.WebTarget{{Path: "map", Port: 8300}}},
	}, netip.MustParsePrefix("10.213.255.240/28"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []renderedRoute `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	proxy := cfg.Apps.HTTP.Servers["proxy"].Routes
	if len(proxy) != 3 {
		t.Fatalf("%d proxy routes, want 2 servers and the 404", len(proxy))
	}
	sub := func(r renderedRoute, fqdn string) []renderedRoute {
		t.Helper()
		if len(r.Match) != 1 || !slices.Equal(r.Match[0].Host, []string{fqdn}) || !r.Terminal ||
			len(r.Handle) != 1 || r.Handle[0].Handler != "subroute" {
			t.Fatalf("route for %s = %+v", fqdn, r)
		}
		return r.Handle[0].Routes
	}
	path := func(r renderedRoute) []string {
		if len(r.Match) == 0 {
			return nil
		}
		return r.Match[0].Path
	}

	s3 := sub(proxy[0], "survival.example.com")
	if len(s3) != 3 {
		t.Fatalf("subroute of 3 = %+v", s3)
	}
	if h := s3[0].Handle; !slices.Equal(path(s3[0]), []string{"/afk-smoke"}) || !s3[0].Terminal || len(h) != 1 ||
		h[0].Handler != "static_response" || h[0].StatusCode != 308 || !slices.Equal(h[0].Headers["Location"], []string{"/afk-smoke/"}) {
		t.Errorf("redirect = %+v", s3[0])
	}
	if h := s3[1].Handle; !slices.Equal(path(s3[1]), []string{"/afk-smoke/*"}) || !s3[1].Terminal || len(h) != 3 ||
		h[0].Handler != "rewrite" || h[0].StripPathPrefix != "/afk-smoke" ||
		h[1].Handler != "headers" || len(h[1].Request.Set) != 1 || !slices.Equal(h[1].Request.Set["X-Forwarded-Prefix"], []string{"/afk-smoke"}) ||
		h[2].Handler != "reverse_proxy" || len(h[2].Upstreams) != 1 || h[2].Upstreams[0].Dial != "mcpanel-mc-3:8200" {
		t.Errorf("prefix route = %+v", s3[1])
	}
	if h := s3[2].Handle; len(s3[2].Match) != 0 || len(h) != 1 ||
		h[0].Handler != "reverse_proxy" || len(h[0].Upstreams) != 1 || h[0].Upstreams[0].Dial != "mcpanel-mc-3:8100" {
		t.Errorf("root route = %+v", s3[2])
	}

	s20 := sub(proxy[1], "paths.example.com")
	if len(s20) != 3 || !slices.Equal(path(s20[0]), []string{"/map"}) || !slices.Equal(path(s20[1]), []string{"/map/*"}) {
		t.Fatalf("subroute of 20 = %+v", s20)
	}
	if h := s20[2].Handle; len(s20[2].Match) != 0 || len(h) != 1 ||
		h[0].Handler != "static_response" || h[0].StatusCode != 404 || h[0].Body != "Not found\n" {
		t.Errorf("route 20 without root ends in %+v, want the static 404", s20[2])
	}

	// Every path matcher in the config.
	var matchers []string
	for _, r := range append(s3, s20...) {
		matchers = append(matchers, path(r)...)
	}
	if want := []string{"/afk-smoke", "/afk-smoke/*", "/map", "/map/*"}; !slices.Equal(matchers, want) {
		t.Fatalf("path matchers = %q, want %q", matchers, want)
	}
	if n := strings.Count(string(b), `"path"`); n != 4 {
		t.Fatalf("%d path matchers in the config, want 4", n)
	}
}

func TestRenderWebConfigRefusesInvalidInput(t *testing.T) {
	trusted := netip.MustParsePrefix("10.213.255.240/28")
	for name, routes := range map[string][]agentapi.WebRoute{
		"json breakout":   {{ID: 1, FQDN: `a.example.com"}],"handle":[{"handler":"x`, Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"placeholder":     {{ID: 1, FQDN: "{http.request.host}.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"upper case":      {{ID: 1, FQDN: "Survival.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"trailing dot":    {{ID: 1, FQDN: "survival.example.com.", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"two labels":      {{ID: 1, FQDN: "example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"rcon port":       {{ID: 1, FQDN: "survival.example.com", Targets: []agentapi.WebTarget{{Port: 25575}}}},
		"id 0":            {{ID: 0, FQDN: "survival.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"duplicate fqdn":  {{ID: 1, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}, {ID: 2, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"newline in name": {{ID: 1, FQDN: "a.example.com\n", Targets: []agentapi.WebTarget{{Port: 8100}}}},
		"path breakout":   {{ID: 1, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Path: `a"]}`, Port: 8100}}}},
		"path with slash": {{ID: 1, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Path: "a/../b", Port: 8100}}}},
		"path wildcard":   {{ID: 1, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Path: "a*", Port: 8100}}}},
		"unsorted":        {{ID: 1, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Path: "b", Port: 8200}, {Path: "a", Port: 8100}}}},
		"no targets":      {{ID: 1, FQDN: "a.example.com"}},
	} {
		if _, err := instances.RenderWebConfig(routes, trusted); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: RenderWebConfig = %v, want ErrInvalid", name, err)
		}
	}
	ok := []agentapi.WebRoute{{ID: 1, FQDN: "a.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}}
	for _, p := range []netip.Prefix{{}, netip.MustParsePrefix("fd00::/64")} {
		if _, err := instances.RenderWebConfig(ok, p); err == nil {
			t.Errorf("trusted %v must be refused", p)
		}
	}
}
