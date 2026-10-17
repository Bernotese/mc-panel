package instances_test

import (
	"bytes"
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
	// Reverse id order in the input; the output is sorted by id. The second name looks odd
	// but is a valid DNS name and must appear verbatim (and only) in its two places.
	routes := []agentapi.WebRoute{
		{ID: 12, FQDN: "xn--mnchen-3ya.0-9.example.com", Port: 65535},
		{ID: 3, FQDN: "survival.example.com", Port: 8100},
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
	if routes[0].ID != 12 {
		t.Fatal("RenderWebConfig must not reorder the caller's slice")
	}
	again, err := instances.RenderWebConfig([]agentapi.WebRoute{routes[1], routes[0]}, netip.MustParsePrefix("10.213.255.240/28"))
	if err != nil || !bytes.Equal(again, got) {
		t.Fatalf("equal routes must give equal bytes: %v", err)
	}
}

func TestRenderWebConfigRefusesInvalidInput(t *testing.T) {
	trusted := netip.MustParsePrefix("10.213.255.240/28")
	for name, routes := range map[string][]agentapi.WebRoute{
		"json breakout":   {{ID: 1, FQDN: `a.example.com"}],"handle":[{"handler":"x`, Port: 8100}},
		"placeholder":     {{ID: 1, FQDN: "{http.request.host}.example.com", Port: 8100}},
		"upper case":      {{ID: 1, FQDN: "Survival.example.com", Port: 8100}},
		"trailing dot":    {{ID: 1, FQDN: "survival.example.com.", Port: 8100}},
		"two labels":      {{ID: 1, FQDN: "example.com", Port: 8100}},
		"rcon port":       {{ID: 1, FQDN: "survival.example.com", Port: 25575}},
		"id 0":            {{ID: 0, FQDN: "survival.example.com", Port: 8100}},
		"duplicate fqdn":  {{ID: 1, FQDN: "a.example.com", Port: 8100}, {ID: 2, FQDN: "a.example.com", Port: 8100}},
		"newline in name": {{ID: 1, FQDN: "a.example.com\n", Port: 8100}},
	} {
		if _, err := instances.RenderWebConfig(routes, trusted); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: RenderWebConfig = %v, want ErrInvalid", name, err)
		}
	}
	ok := []agentapi.WebRoute{{ID: 1, FQDN: "a.example.com", Port: 8100}}
	for _, p := range []netip.Prefix{{}, netip.MustParsePrefix("fd00::/64")} {
		if _, err := instances.RenderWebConfig(ok, p); err == nil {
			t.Errorf("trusted %v must be refused", p)
		}
	}
}
