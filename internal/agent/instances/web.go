package instances

import (
	"cmp"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/config"
)

// The web router mcpanel-web (web ports spec §2) is a Caddy container that serves the
// published HTTP ports of a server under its own name, one at the root and the others under
// paths (web port paths spec §4). Host Caddy terminates TLS and forwards to its proxy port;
// its ask port tells host Caddy which names may get a certificate.

const (
	WebName    = "mcpanel-web" // container
	WebNetwork = "mcpanel-web" // its home network (the published ports)
	LabelWeb   = "mcpanel.web"
	// LabelWebTemplate carries the SHA-256 of the web router's container template; a
	// different value (agent upgrade) replaces the container.
	LabelWebTemplate = "mcpanel.web-template"
	// WebMemoryBytes caps mcpanel-web (Caddy needs a few dozen MiB).
	WebMemoryBytes = 128 << 20
	WebPidsLimit   = 128

	webProxyPort  = "8080/tcp"
	webAskPort    = "8081/tcp"
	webConfigDir  = "/etc/mcpanel-web" // read-only bind of WebDir
	webConfigFile = "caddy.json"
	// webUser is the unprivileged uid:gid Caddy runs as (the image's default is root); the
	// tmpfs mounts belong to it, because Caddy creates /data/caddy at start.
	webUser  = "65532:65532"
	webTmpfs = "rw,noexec,nosuid,nodev,uid=65532,gid=65532,mode=0700,size="

	webNotFoundBody    = "Not found\n"
	webUnreachableBody = "The server's web interface is not reachable right now.\n"
)

// WebDir is <instances_dir>/web, root-owned 0755, mounted read-only at /etc/mcpanel-web.
// It holds only the generated Caddy config.
func WebDir(instancesDir string) string { return filepath.Join(instancesDir, "web") }

// WebContainer builds the hardened Caddy container: non-root, read-only rootfs with small
// tmpfs mounts for Caddy's data and config directories, no capabilities except the one its
// binary needs to start (see CapAdd), and both ports
// published on 127.0.0.1 only. It gets no Docker socket and no admin API; it reloads its
// config file through Caddy's file watch.
func WebContainer(h Host) (docker.ContainerConfig, error) {
	img, err := pinnedImage(h.Images.Web)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	proxy := strconv.Itoa(cmp.Or(h.WebProxyPort, config.DefaultWebProxyPort))
	ask := strconv.Itoa(cmp.Or(h.WebAskPort, config.DefaultWebAskPort))
	return docker.ContainerConfig{
		Image:        img,
		Cmd:          []string{"caddy", "run", "--config", webConfigDir + "/" + webConfigFile, "--watch"},
		User:         webUser,
		Env:          []string{"XDG_DATA_HOME=/data", "XDG_CONFIG_HOME=/config"},
		Labels:       map[string]string{LabelWeb: "1"},
		ExposedPorts: map[string]struct{}{webProxyPort: {}, webAskPort: {}},
		HostConfig: docker.HostConfig{
			// Directory, not file: an atomic rename of caddy.json stays visible.
			Binds:       []string{WebDir(h.InstancesDir) + ":" + webConfigDir + ":ro"},
			NetworkMode: WebNetwork,
			PortBindings: map[string][]docker.PortBinding{
				webProxyPort: {{HostIP: "127.0.0.1", HostPort: proxy}},
				webAskPort:   {{HostIP: "127.0.0.1", HostPort: ask}},
			},
			Tmpfs: map[string]string{
				"/data":   webTmpfs + "16m",
				"/config": webTmpfs + "1m",
			},
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"},
			// The image's /usr/bin/caddy carries the file capability cap_net_bind_service, and
			// the kernel refuses to exec such a binary when the bounding set lacks it. It only
			// allows binding ports below 1024 inside the container's own network namespace.
			CapAdd:         []string{"NET_BIND_SERVICE"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			ReadonlyRootfs: true,
			Memory:         WebMemoryBytes,
			MemorySwap:     WebMemoryBytes,
			PidsLimit:      WebPidsLimit,
			LogConfig:      logConfig(),
		},
		NetworkingConfig: network(WebNetwork),
	}, nil
}

// WebAddress is mcpanel-web's fixed address on a server network: the last usable host of
// the subnet (10.213.0.16/28 → 10.213.0.30). Docker hands out addresses from the bottom, so
// it never collides with the server or the router. The bridge rules are written for it
// before the connect, which keeps the port restriction fail-closed.
func WebAddress(subnet netip.Prefix) (netip.Addr, bool) {
	if !subnet.IsValid() || !subnet.Addr().Is4() || subnet.Bits() > 30 {
		return netip.Addr{}, false
	}
	base := binary.BigEndian.Uint32(subnet.Masked().Addr().AsSlice())
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], base+uint32(1)<<(32-subnet.Bits())-2)
	return netip.AddrFrom4(a), true
}

// Caddy JSON config, only the fields mc-panel sets. The config is always built from these
// structs with encoding/json, never from string templates.
type (
	caddyConfig struct {
		Admin caddyAdmin `json:"admin"`
		Apps  caddyApps  `json:"apps"`
	}
	caddyAdmin struct {
		Disabled bool `json:"disabled"`
		Config   struct {
			Persist bool `json:"persist"`
		} `json:"config"`
	}
	caddyApps struct {
		HTTP struct {
			Servers map[string]caddyServer `json:"servers"`
		} `json:"http"`
	}
	caddyServer struct {
		Listen         []string `json:"listen"`
		AutomaticHTTPS struct {
			Disable bool `json:"disable"`
		} `json:"automatic_https"`
		TrustedProxies *caddyIPSource `json:"trusted_proxies,omitempty"`
		Routes         []caddyRoute   `json:"routes"`
		Errors         *caddyErrors   `json:"errors,omitempty"`
	}
	caddyIPSource struct {
		Source string   `json:"source"`
		Ranges []string `json:"ranges"`
	}
	caddyErrors struct {
		Routes []caddyRoute `json:"routes"`
	}
	caddyRoute struct {
		Match    []caddyMatch   `json:"match,omitempty"`
		Handle   []caddyHandler `json:"handle"`
		Terminal bool           `json:"terminal,omitempty"`
	}
	caddyMatch struct {
		Host  []string            `json:"host,omitempty"`
		Path  []string            `json:"path,omitempty"`
		Query map[string][]string `json:"query,omitempty"`
	}
	caddyHandler struct {
		Handler         string              `json:"handler"`
		Routes          []caddyRoute        `json:"routes,omitempty"` // subroute
		StripPathPrefix string              `json:"strip_path_prefix,omitempty"`
		Request         *caddyHeaderOps     `json:"request,omitempty"` // headers
		Upstreams       []caddyUpstream     `json:"upstreams,omitempty"`
		StatusCode      int                 `json:"status_code,omitempty"`
		Headers         map[string][]string `json:"headers,omitempty"` // static_response
		Body            string              `json:"body,omitempty"`
	}
	caddyHeaderOps struct {
		Set map[string][]string `json:"set"`
	}
	caddyUpstream struct {
		Dial string `json:"dial"`
	}
)

func staticResponse(status int, body string) caddyHandler {
	return caddyHandler{Handler: "static_response", StatusCode: status, Body: body}
}

func reverseProxy(id int64, port int) caddyHandler {
	return caddyHandler{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: ContainerName(id) + ":" + strconv.Itoa(port)}}}
}

// webSubroutes is the subroute of one server (web port paths spec §4.2): for every path p,
// "/p" redirects to "/p/" and "/p/*" goes to its port with the prefix stripped and named in
// X-Forwarded-Prefix; then the root target takes every other path, or the static 404 answers
// them. The targets are validated, so p is one segment of [a-z0-9-].
func webSubroutes(r agentapi.WebRoute) []caddyRoute {
	var routes []caddyRoute
	var root *agentapi.WebTarget
	for _, t := range r.Targets {
		if t.Path == "" {
			root = &t
			continue
		}
		prefix := "/" + t.Path
		redirect := staticResponse(308, "")
		redirect.Headers = map[string][]string{"Location": {prefix + "/"}}
		routes = append(routes,
			caddyRoute{Match: []caddyMatch{{Path: []string{prefix}}}, Handle: []caddyHandler{redirect}, Terminal: true},
			caddyRoute{
				Match: []caddyMatch{{Path: []string{prefix + "/*"}}},
				Handle: []caddyHandler{
					{Handler: "rewrite", StripPathPrefix: prefix},
					{Handler: "headers", Request: &caddyHeaderOps{Set: map[string][]string{"X-Forwarded-Prefix": {prefix}}}},
					reverseProxy(r.ID, t.Port),
				},
				Terminal: true,
			})
	}
	if root != nil {
		return append(routes, caddyRoute{Handle: []caddyHandler{reverseProxy(r.ID, root.Port)}})
	}
	return append(routes, caddyRoute{Handle: []caddyHandler{staticResponse(404, webNotFoundBody)}})
}

func newCaddyServer(listen string) caddyServer {
	s := caddyServer{Listen: []string{listen}}
	s.AutomaticHTTPS.Disable = true
	return s
}

// RenderWebConfig returns the Caddy JSON config for routes (sorted by id). It validates the
// routes again (the agent never trusts the panel), so only checked ids, lowercase DNS names
// and ports reach the config. trusted is the subnet of the mcpanel-web network: host
// Caddy's requests arrive from its gateway, so their X-Forwarded-For is kept.
//
// The proxy server (:8080) routes each name through its subroute (webSubroutes) to
// mcpanel-mc-<id>:<port>, answers unknown names with 404 and an unreachable upstream with a
// static 502 page. The ask server (:8081) answers 200 only when ?domain= is exactly one of
// the names.
func RenderWebConfig(routes []agentapi.WebRoute, trusted netip.Prefix) ([]byte, error) {
	sorted := slices.Clone(routes)
	if sorted == nil {
		sorted = []agentapi.WebRoute{}
	}
	if err := (agentapi.WebRoutesRequest{Routes: sorted}).Validate(); err != nil {
		return nil, err
	}
	if !trusted.IsValid() || !trusted.Addr().Is4() {
		return nil, fmt.Errorf("web router: trusted network %v is not an IPv4 prefix", trusted)
	}
	slices.SortFunc(sorted, func(a, b agentapi.WebRoute) int { return cmp.Compare(a.ID, b.ID) })

	proxy := newCaddyServer(":" + portNumber(webProxyPort))
	proxy.TrustedProxies = &caddyIPSource{Source: "static", Ranges: []string{trusted.Masked().String()}}
	ask := newCaddyServer(":" + portNumber(webAskPort))
	for _, r := range sorted {
		proxy.Routes = append(proxy.Routes, caddyRoute{
			Match:    []caddyMatch{{Host: []string{r.FQDN}}},
			Handle:   []caddyHandler{{Handler: "subroute", Routes: webSubroutes(r)}},
			Terminal: true,
		})
		ask.Routes = append(ask.Routes, caddyRoute{
			Match:    []caddyMatch{{Query: map[string][]string{"domain": {r.FQDN}}}},
			Handle:   []caddyHandler{staticResponse(200, "")},
			Terminal: true,
		})
	}
	proxy.Routes = append(proxy.Routes, caddyRoute{Handle: []caddyHandler{staticResponse(404, webNotFoundBody)}})
	proxy.Errors = &caddyErrors{Routes: []caddyRoute{{Handle: []caddyHandler{staticResponse(502, webUnreachableBody)}}}}
	ask.Routes = append(ask.Routes, caddyRoute{Handle: []caddyHandler{staticResponse(404, "")}})

	var cfg caddyConfig
	cfg.Admin.Disabled = true // no listener; --watch reloads the file without one
	cfg.Apps.HTTP.Servers = map[string]caddyServer{"proxy": proxy, "ask": ask}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// portNumber is the number of a Docker port key ("8080/tcp" → "8080").
func portNumber(key string) string {
	n, _, _ := strings.Cut(key, "/")
	return n
}
