// Package config loads and validates panel and agent configuration (TOML).
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
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
	Dev            bool     `toml:"dev"`
	Listen         string   `toml:"listen"`
	PublicURL      string   `toml:"public_url"`
	DataDir        string   `toml:"data_dir"`
	AgentSocket    string   `toml:"agent_socket"`
	GeoIPPath      string   `toml:"geoip_path"`
	TrustedProxies []string `toml:"trusted_proxies"`
	Session        Session  `toml:"session"`

	trusted []netip.Prefix
}

func defaultSession() Session {
	return Session{
		Idle:     Duration{30 * time.Minute},
		Absolute: Duration{12 * time.Hour},
		PreAuth:  Duration{5 * time.Minute},
		Sudo:     Duration{10 * time.Minute},
	}
}

func DefaultPanel() Panel {
	return Panel{
		Listen:         "127.0.0.1:8080",
		DataDir:        "/var/lib/mc-panel",
		AgentSocket:    "/run/mc-panel/agent.sock",
		GeoIPPath:      "/var/lib/mc-panel/dbip-country-lite.mmdb",
		TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
		Session:        defaultSession(),
	}
}

func DevPanel(root string) Panel {
	p := DefaultPanel()
	p.Dev = true
	p.PublicURL = "http://localhost:5173"
	p.DataDir = filepath.Join(root, ".dev", "data")
	p.AgentSocket = filepath.Join(root, ".dev", "agent.sock")
	p.GeoIPPath = filepath.Join(root, ".dev", "dbip-country-lite.mmdb")
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
	if p.Listen == "" {
		errs = append(errs, errors.New("listen must be set"))
	}
	u, err := url.Parse(p.PublicURL)
	switch {
	case p.PublicURL == "" || err != nil || u.Host == "":
		errs = append(errs, errors.New("public_url must be an absolute URL"))
	case u.Scheme != "https" && !(p.Dev && u.Scheme == "http"):
		errs = append(errs, errors.New("public_url must use https (http only allowed in dev mode)"))
	}
	if p.DataDir == "" || p.AgentSocket == "" {
		errs = append(errs, errors.New("data_dir and agent_socket must be set"))
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
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
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

type Agent struct {
	Dev      bool   `toml:"dev"`
	Socket   string `toml:"socket"`
	PanelUID int    `toml:"panel_uid"`
	Firewall string `toml:"firewall"` // "nftables" | "noop"
}

func DefaultAgent() Agent {
	return Agent{Socket: "/run/mc-panel/agent.sock", PanelUID: -1, Firewall: "nftables"}
}

func DevAgent(root string) Agent {
	return Agent{Dev: true, Socket: filepath.Join(root, ".dev", "agent.sock"), PanelUID: os.Getuid(), Firewall: "noop"}
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
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}
