// Package config loads and validates panel and agent configuration (TOML).
package config

import (
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

type Session struct {
	Idle     Duration `toml:"idle_timeout"`
	Absolute Duration `toml:"absolute_timeout"`
	PreAuth  Duration `toml:"preauth_timeout"`
	Sudo     Duration `toml:"sudo_ttl"`
}

type Panel struct {
	Dev         bool   `toml:"dev"`
	Listen      string `toml:"listen"`
	PublicURL   string `toml:"public_url"`
	DataDir     string `toml:"data_dir"`
	AgentSocket string `toml:"agent_socket"`
	FilesSocket string `toml:"files_socket"`
	GeoIPPath   string `toml:"geoip_path"`
	// MonitoringPingAddr is where the panel sends Server List Pings (mc-router); "" = no ping
	// (TP5 §3.2).
	MonitoringPingAddr string `toml:"monitoring_ping_addr"`
	// InstancesDir is statfs'ed for the host disk values (TP5 §3.4); "" = no disk values.
	InstancesDir   string   `toml:"instances_dir"`
	TrustedProxies []string `toml:"trusted_proxies"`
	Session        Session  `toml:"session"`
	Backups        Backups  `toml:"backups"`

	trusted []netip.Prefix
}

// Backups configures the backup jobs (sub-project 4).
type Backups struct {
	// MaxDuration bounds one backup: a running server stays in save-off meanwhile, and only
	// one backup job runs in the panel at a time (SEC-25).
	MaxDuration Duration `toml:"max_duration"`
}

// Bounds of backups.max_duration: below the stall watchdog (10 min) a slow disk would fail
// every backup.
const (
	minBackupDuration = 10 * time.Minute
	maxBackupDuration = 7 * 24 * time.Hour
)

func defaultSession() Session {
	return Session{
		Idle:     Duration{30 * time.Minute},
		Absolute: Duration{12 * time.Hour},
		PreAuth:  Duration{5 * time.Minute},
		Sudo:     Duration{10 * time.Minute},
	}
}

// UnixListenPrefix marks a listen value as a unix socket path ("unix:/abs/path").
const UnixListenPrefix = "unix:"

// DefaultPanel is the production profile. The panel listens on a unix socket that only the
// reverse proxy (Caddy) can reach; requests arriving over it are treated as proxied.
func DefaultPanel() Panel {
	return Panel{
		Listen:             UnixListenPrefix + "/run/mc-panel/panel.sock",
		DataDir:            "/var/lib/mc-panel",
		AgentSocket:        "/run/mc-panel/agent.sock",
		FilesSocket:        "/run/mc-panel-files/files.sock",
		GeoIPPath:          "/var/lib/mc-panel/dbip-country-lite.mmdb",
		TrustedProxies:     []string{"127.0.0.1/32", "::1/128"},
		Session:            defaultSession(),
		Backups:            Backups{MaxDuration: Duration{4 * time.Hour}},
		MonitoringPingAddr: "127.0.0.1:25565",
		InstancesDir:       DefaultInstancesDir,
	}
}

func DevPanel(root string) Panel {
	p := DefaultPanel()
	p.Dev = true
	p.Listen = "127.0.0.1:8080"
	p.PublicURL = "http://localhost:5173"
	p.DataDir = filepath.Join(root, ".dev", "data")
	p.AgentSocket = filepath.Join(root, ".dev", "agent.sock")
	p.FilesSocket = filepath.Join(root, ".dev", "files.sock")
	p.GeoIPPath = filepath.Join(root, ".dev", "dbip-country-lite.mmdb")
	p.MonitoringPingAddr = "" // no mc-router on the Mac
	p.InstancesDir = filepath.Join(root, ".dev", "mc")
	return p
}

// LoadPanel starts from the default (or dev) profile and overlays the TOML file at path (if non-empty).
func LoadPanel(path string, dev bool, root string) (Panel, error) {
	p := DefaultPanel()
	if dev {
		p = DevPanel(root)
	}
	if path != "" {
		md, err := toml.DecodeFile(path, &p)
		if err != nil {
			return Panel{}, fmt.Errorf("config: %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			keys := make([]string, len(und))
			for i, k := range und {
				keys[i] = k.String()
			}
			return Panel{}, fmt.Errorf("config: unknown keys: %s", strings.Join(keys, ", "))
		}
	}
	// The CLI flag is authoritative: the file must not be able to enable or disable dev mode.
	p.Dev = dev
	if err := p.Validate(); err != nil {
		return Panel{}, err
	}
	return p, nil
}

func (p *Panel) Validate() error {
	var errs []error
	if err := validateListen(p.Listen); err != nil {
		errs = append(errs, err)
	}
	u, err := url.Parse(p.PublicURL)
	switch {
	case p.PublicURL == "" || err != nil || u.Host == "":
		errs = append(errs, errors.New("public_url must be an absolute URL"))
	case u.Scheme != "https" && !(p.Dev && u.Scheme == "http"):
		errs = append(errs, errors.New("public_url must use https (http only allowed in dev mode)"))
	}
	if p.DataDir == "" || p.AgentSocket == "" || p.FilesSocket == "" {
		errs = append(errs, errors.New("data_dir, agent_socket and files_socket must be set"))
	}
	if err := validatePingAddr(p.MonitoringPingAddr); err != nil {
		errs = append(errs, err)
	}
	if p.InstancesDir != "" && (!filepath.IsAbs(p.InstancesDir) || filepath.Clean(p.InstancesDir) != p.InstancesDir) {
		errs = append(errs, fmt.Errorf("instances_dir: %q must be a clean absolute path", p.InstancesDir))
	}
	p.trusted = make([]netip.Prefix, 0, len(p.TrustedProxies))
	for _, s := range p.TrustedProxies {
		pf, err := netip.ParsePrefix(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("trusted_proxies: %q: %w", s, err))
			continue
		}
		p.trusted = append(p.trusted, pf.Masked())
	}
	for name, d := range map[string]Duration{
		"idle_timeout": p.Session.Idle, "absolute_timeout": p.Session.Absolute,
		"preauth_timeout": p.Session.PreAuth, "sudo_ttl": p.Session.Sudo,
	} {
		if d.Duration <= 0 {
			errs = append(errs, fmt.Errorf("session.%s must be > 0", name))
		}
	}
	if d := p.Backups.MaxDuration.Duration; d < minBackupDuration || d > maxBackupDuration {
		errs = append(errs, fmt.Errorf("backups.max_duration must be between %s and %s", minBackupDuration, maxBackupDuration))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

func validateListen(l string) error {
	if path, ok := strings.CutPrefix(l, UnixListenPrefix); ok {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
			return fmt.Errorf("listen: %q must be unix: followed by a clean absolute path", l)
		}
		return nil
	}
	if _, _, err := net.SplitHostPort(l); err != nil || l == "" {
		return fmt.Errorf("listen: %q must be host:port or unix:/absolute/path", l)
	}
	return nil
}

// validatePingAddr accepts "" (no ping) or host:port with a numeric port 1–65535.
func validatePingAddr(a string) error {
	if a == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(a)
	n, perr := strconv.Atoi(port)
	if err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("monitoring_ping_addr: %q must be host:port (port 1–65535) or empty", a)
	}
	return nil
}

// ListenUnix returns the socket path if the panel listens on a unix socket.
func (p Panel) ListenUnix() (string, bool) {
	return strings.CutPrefix(p.Listen, UnixListenPrefix)
}

func (p Panel) DBPath() string        { return filepath.Join(p.DataDir, "panel.db") }
func (p Panel) SecretKeyPath() string { return filepath.Join(p.DataDir, "secret.key") }

func (p Panel) Origin() string {
	u, err := url.Parse(p.PublicURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (p Panel) RPID() string {
	u, err := url.Parse(p.PublicURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (p Panel) TrustedPrefixes() []netip.Prefix { return append([]netip.Prefix(nil), p.trusted...) }

// Images are the digest-pinned container images the agent may run. The panel only
// ever chooses a key (java8 … java25), never an image reference.
type Images struct {
	Java8  string `toml:"java8"`
	Java17 string `toml:"java17"`
	Java21 string `toml:"java21"`
	Java25 string `toml:"java25"`
	Router string `toml:"router"`
}

// DefaultImages are the pinned defaults (verified 2026-10-10).
func DefaultImages() Images {
	return Images{
		Java8:  "itzg/minecraft-server:2026.9.2-java8@sha256:a23a59bc9bd520d2c1bae97225b8672771cf82c90073ea36dc18f18c199f325b",
		Java17: "itzg/minecraft-server:2026.9.2-java17@sha256:0198cb9f0c7e7b1747645040e1245a6915c122662f3106f3b0979473a953ab61",
		Java21: "itzg/minecraft-server:2026.9.2-java21@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa",
		Java25: "itzg/minecraft-server:2026.9.2-java25@sha256:de5d1b1a83eba576f6c8a688fac2a3523ce457724cdebc8ea48d7818b74cdf6e",
		Router: "itzg/mc-router:1.47.2@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e",
	}
}

// ByJava returns the image for a java key ("java8", "java17", "java21", "java25").
func (i Images) ByJava(java string) (string, bool) {
	switch java {
	case "java8":
		return i.Java8, true
	case "java17":
		return i.Java17, true
	case "java21":
		return i.Java21, true
	case "java25":
		return i.Java25, true
	default:
		return "", false
	}
}

// imageRe requires a digest: a tag can be moved upstream, a digest cannot.
var imageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)

func (i Images) validate() []error {
	var errs []error
	for _, e := range []struct{ key, ref string }{
		{"java8", i.Java8}, {"java17", i.Java17}, {"java21", i.Java21}, {"java25", i.Java25}, {"router", i.Router},
	} {
		if !imageRe.MatchString(e.ref) {
			errs = append(errs, fmt.Errorf("images.%s: %q must be a reference pinned with @sha256:<64 hex>", e.key, e.ref))
		}
	}
	return errs
}

const (
	DefaultDockerSocket = "/var/run/docker.sock"
	DefaultInstancesDir = "/srv/mc-panel"
	DefaultMCUser       = "mcpanel-mc"
	// DefaultRouterConnectionRateLimit is mc-router's limit of new connections per
	// second over all clients (its own default, 1/s, lets a single client starve
	// every join and Server List Ping).
	DefaultRouterConnectionRateLimit = 50
	// DefaultInstanceNetworkPool is the IPv4 range the agent carves the per-server
	// networks from (one /28 each, 4096 in a /16). Docker's own default pools are
	// 172.17.0.0/16–172.31.0.0/16 and 192.168.0.0/16.
	DefaultInstanceNetworkPool = "10.213.0.0/16"
	// InstanceNetworkBits is the prefix length of one server's network: 14 addresses for
	// the gateway, the server and mc-router.
	InstanceNetworkBits = 28

	maxEgressDeny           = 64
	maxRouterConnectionRate = 100000
	minCPUPerInstance       = 0.01 // Docker's smallest NanoCpus value
	// maxCPUPerInstance only catches typos; the agent clamps the value to the host's CPUs,
	// so a smaller (downsized) host never keeps it from starting.
	maxCPUPerInstance = 1024
)

// DefaultContainerEgressDeny is the destination list the Minecraft containers may
// not open new connections to: link-local (cloud metadata), RFC 1918, CGNAT, Azure's
// WireServer (a public address that hands out VM extension and provisioning data), IPv6
// ULA and link-local. A fresh slice on every call.
func DefaultContainerEgressDeny() []string {
	return []string{"169.254.0.0/16", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "168.63.129.16/32", "fc00::/7", "fe80::/10"}
}

type Agent struct {
	Dev          bool   `toml:"dev"`
	Socket       string `toml:"socket"`
	PanelUID     int    `toml:"panel_uid"`
	Firewall     string `toml:"firewall"` // "nftables" | "noop"
	DockerSocket string `toml:"docker_socket"`
	InstancesDir string `toml:"instances_dir"`
	// MCUser is the system user the Minecraft containers run as. Empty is only
	// allowed in dev mode and means the uid:gid of the agent process.
	MCUser string `toml:"mc_user"`
	// ContainerEgressDeny lists the destination prefixes the Minecraft containers may
	// not open new connections to (nftables forward filter of the agent). A missing key
	// means DefaultContainerEgressDeny; an empty list disables the filter.
	ContainerEgressDeny []string `toml:"container_egress_deny"`
	// RouterConnectionRateLimit is mc-router's -connection-rate-limit (new
	// connections per second, all clients together).
	RouterConnectionRateLimit int `toml:"router_connection_rate_limit"`
	// CPUPerInstance caps every Minecraft container at this many CPUs; 0 = no cap
	// (only the CPU weight applies).
	CPUPerInstance float64 `toml:"cpu_per_instance"`
	// InstanceNetworkPool is the IPv4 prefix (/8 to /24) the per-server Docker networks
	// get their /28 subnets from.
	InstanceNetworkPool string `toml:"instance_network_pool"`
	Images              Images `toml:"images"`
}

func DefaultAgent() Agent {
	return Agent{
		Socket:       "/run/mc-panel/agent.sock",
		PanelUID:     -1,
		Firewall:     "nftables",
		DockerSocket: DefaultDockerSocket,
		InstancesDir: DefaultInstancesDir,
		MCUser:       DefaultMCUser,
		Images:       DefaultImages(),

		ContainerEgressDeny:       DefaultContainerEgressDeny(),
		RouterConnectionRateLimit: DefaultRouterConnectionRateLimit,
		InstanceNetworkPool:       DefaultInstanceNetworkPool,
	}
}

func DevAgent(root string) Agent {
	a := DefaultAgent()
	a.Dev = true
	a.Socket = filepath.Join(root, ".dev", "agent.sock")
	a.PanelUID = os.Getuid()
	a.Firewall = "noop"
	a.DockerSocket = "" // Docker off: no manager, no containers on the developer machine
	if os.Getenv("MCPANEL_DEV_DOCKER") == "1" {
		a.DockerSocket = devDockerSocket()
	}
	a.InstancesDir = filepath.Join(root, ".dev", "mc")
	a.MCUser = ""
	return a
}

// devDockerSocket returns the first existing socket of OrbStack, Docker Desktop or
// the system daemon. Without any, it returns the default path and the agent reports
// Docker as unavailable.
func devDockerSocket() string {
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".orbstack", "run", "docker.sock"),
			filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	for _, c := range append(candidates, DefaultDockerSocket) {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return DefaultDockerSocket
}

func LoadAgent(path string, dev bool, root string) (Agent, error) {
	a := DefaultAgent()
	if dev {
		a = DevAgent(root)
	}
	if path != "" {
		md, err := toml.DecodeFile(path, &a)
		if err != nil {
			return Agent{}, fmt.Errorf("config: %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			return Agent{}, fmt.Errorf("config: unknown keys: %v", und)
		}
	}
	// The CLI flag is authoritative: the file must not be able to enable or disable dev mode.
	a.Dev = dev
	if err := a.Validate(); err != nil {
		return Agent{}, err
	}
	return a, nil
}

func (a Agent) Validate() error {
	var errs []error
	if a.Socket == "" {
		errs = append(errs, errors.New("socket must be set"))
	}
	if a.PanelUID < 0 {
		errs = append(errs, errors.New("panel_uid must be set to the uid of the panel user"))
	}
	if a.Firewall != "nftables" && a.Firewall != "noop" {
		errs = append(errs, fmt.Errorf("firewall must be \"nftables\" or \"noop\", got %q", a.Firewall))
	}
	// An empty docker_socket disables the instance manager; only allowed in dev mode.
	if a.DockerSocket != "" || !a.Dev {
		if err := cleanAbsPath("docker_socket", a.DockerSocket); err != nil {
			errs = append(errs, err)
		}
	}
	if err := cleanAbsPath("instances_dir", a.InstancesDir); err != nil {
		errs = append(errs, err)
	} else if a.InstancesDir == "/" || strings.ContainsAny(a.InstancesDir, ":,") {
		// ':' and ',' would break Docker's "src:dst[:opts]" bind syntax.
		errs = append(errs, fmt.Errorf("instances_dir: %q must not be / and must not contain ':' or ','", a.InstancesDir))
	}
	if a.MCUser == "" && !a.Dev {
		errs = append(errs, errors.New("mc_user must be set (system user for the Minecraft containers)"))
	}
	if _, err := parseEgressDeny(a.ContainerEgressDeny); err != nil {
		errs = append(errs, err)
	}
	if a.RouterConnectionRateLimit < 1 || a.RouterConnectionRateLimit > maxRouterConnectionRate {
		errs = append(errs, fmt.Errorf("router_connection_rate_limit must be between 1 and %d, got %d", maxRouterConnectionRate, a.RouterConnectionRateLimit))
	}
	if c := a.CPUPerInstance; math.IsNaN(c) || c < 0 || (c > 0 && c < minCPUPerInstance) || c > maxCPUPerInstance {
		errs = append(errs, fmt.Errorf("cpu_per_instance must be 0 (no cap) or between %g and %d, got %g", minCPUPerInstance, maxCPUPerInstance, c))
	}
	if _, err := parseNetworkPool(a.InstanceNetworkPool); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, a.Images.validate()...)
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

// NetworkPool returns InstanceNetworkPool parsed; call it only on a validated config.
func (a Agent) NetworkPool() netip.Prefix {
	p, _ := parseNetworkPool(a.InstanceNetworkPool)
	return p
}

func parseNetworkPool(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	switch {
	case err != nil:
		return netip.Prefix{}, fmt.Errorf("instance_network_pool: %q is not a prefix", s)
	case !p.Addr().Is4():
		return netip.Prefix{}, fmt.Errorf("instance_network_pool: %s must be an IPv4 prefix", s)
	case p != p.Masked():
		return netip.Prefix{}, fmt.Errorf("instance_network_pool: %s has host bits set (want %s)", s, p.Masked())
	case p.Bits() < 8 || p.Bits() > 24:
		return netip.Prefix{}, fmt.Errorf("instance_network_pool: %s must be between /8 and /24", s)
	}
	return p, nil
}

// EgressDenyPrefixes returns ContainerEgressDeny parsed; call it only on a validated config.
func (a Agent) EgressDenyPrefixes() []netip.Prefix {
	p, _ := parseEgressDeny(a.ContainerEgressDeny)
	return p
}

// parseEgressDeny accepts at most maxEgressDeny canonical prefixes (no host bits, no
// zone, no IPv4-mapped IPv6) that do not overlap: they become elements of an nftables
// interval set, which refuses overlapping ranges.
func parseEgressDeny(list []string) ([]netip.Prefix, error) {
	if len(list) > maxEgressDeny {
		return nil, fmt.Errorf("container_egress_deny: at most %d entries, got %d", maxEgressDeny, len(list))
	}
	out := make([]netip.Prefix, 0, len(list))
	for _, s := range list {
		p, err := netip.ParsePrefix(s)
		if err != nil || p != p.Masked() || p.Addr().Is4In6() {
			return nil, fmt.Errorf("container_egress_deny: %q must be a network prefix such as 10.0.0.0/8 (no host bits)", s)
		}
		for _, q := range out {
			if p.Overlaps(q) {
				return nil, fmt.Errorf("container_egress_deny: %s and %s overlap", q, p)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func cleanAbsPath(key, p string) error {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsRune(p, 0) {
		return fmt.Errorf("%s: %q must be a clean absolute path", key, p)
	}
	return nil
}
