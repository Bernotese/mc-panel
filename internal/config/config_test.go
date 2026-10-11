package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/config"
)

func TestDefaultPanelIsValidAfterSettingPublicURL(t *testing.T) {
	p := config.DefaultPanel()
	p.PublicURL = "https://panel.example.com"
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.Session.Idle.Duration != 30*time.Minute || p.Session.Absolute.Duration != 12*time.Hour ||
		p.Session.PreAuth.Duration != 5*time.Minute || p.Session.Sudo.Duration != 10*time.Minute {
		t.Fatalf("unexpected session defaults: %+v", p.Session)
	}
	if p.RPID() != "panel.example.com" || p.Origin() != "https://panel.example.com" {
		t.Fatalf("RPID/Origin = %q/%q", p.RPID(), p.Origin())
	}
	if p.DBPath() != "/var/lib/mc-panel/panel.db" {
		t.Fatalf("DBPath = %q", p.DBPath())
	}
}

func TestProdRequiresHTTPS(t *testing.T) {
	p := config.DefaultPanel()
	p.PublicURL = "http://panel.example.com"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https error, got %v", err)
	}
}

func TestDevPanel(t *testing.T) {
	p := config.DevPanel("/repo")
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !p.Dev || p.PublicURL != "http://localhost:5173" || p.RPID() != "localhost" ||
		p.Origin() != "http://localhost:5173" || p.DataDir != "/repo/.dev/data" ||
		p.AgentSocket != "/repo/.dev/agent.sock" || p.FilesSocket != "/repo/.dev/files.sock" {
		t.Fatalf("unexpected dev config: %+v", p)
	}
}

func TestLoadPanelOverridesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	content := `
public_url = "https://mc.example.org"
trusted_proxies = ["127.0.0.1/32", "10.0.0.0/8"]
[session]
idle_timeout = "15m"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadPanel(path, false, "")
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if p.Session.Idle.Duration != 15*time.Minute || p.Session.Absolute.Duration != 12*time.Hour {
		t.Fatalf("session = %+v", p.Session)
	}
	if got := len(p.TrustedPrefixes()); got != 2 {
		t.Fatalf("TrustedPrefixes len = %d", got)
	}
}

func TestLoadPanelRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	if err := os.WriteFile(path, []byte("public_url = \"https://x.org\"\nlisten_adress = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadPanel(path, false, ""); err == nil || !strings.Contains(err.Error(), "listen_adress") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestInvalidTrustedProxy(t *testing.T) {
	p := config.DevPanel("/repo")
	p.TrustedProxies = []string{"not-a-cidr"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestAgentConfig(t *testing.T) {
	a := config.DevAgent("/repo")
	if err := a.Validate(); err != nil {
		t.Fatalf("dev agent invalid: %v", err)
	}
	if a.Firewall != "noop" || a.PanelUID != os.Getuid() {
		t.Fatalf("dev agent = %+v", a)
	}
	d := config.DefaultAgent()
	if err := d.Validate(); err == nil {
		t.Fatal("default agent without panel_uid must be invalid")
	}
	d.PanelUID = 998
	if err := d.Validate(); err != nil || d.Firewall != "nftables" || d.Socket != "/run/mc-panel/agent.sock" {
		t.Fatalf("default agent = %+v err=%v", d, err)
	}
	d.Firewall = "iptables"
	if err := d.Validate(); err == nil {
		t.Fatal("unknown firewall must be invalid")
	}
}

func TestLoadPanelFileCannotEnableDevMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	content := "dev = true\npublic_url = \"http://panel.example.com\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadPanel(path, false, "")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https error when file sets dev=true and dev flag is false, got %v", err)
	}
}

func TestLoadPanelCLIDevWinsOverFileDev(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	content := "dev = false\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadPanel(path, true, "/repo")
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if !p.Dev || p.PublicURL != "http://localhost:5173" {
		t.Fatalf("dev flag must win over file: %+v", p)
	}
}

func TestLoadAgentRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "panel_uid = 998\nbogus_key = \"x\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadAgent(path, false, ""); err == nil || !strings.Contains(err.Error(), "bogus_key") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestLoadAgentFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "panel_uid = 998\nfirewall = \"nftables\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := config.LoadAgent(path, false, "")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.PanelUID != 998 || a.Firewall != "nftables" || a.Socket != "/run/mc-panel/agent.sock" || a.Dev {
		t.Fatalf("agent = %+v", a)
	}
}

func TestLoadAgentFileCannotEnableDevMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "dev = true\npanel_uid = 998\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := config.LoadAgent(path, false, "")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.Dev {
		t.Fatalf("file must not enable dev mode: %+v", a)
	}
}

func TestListenDefaults(t *testing.T) {
	p := config.DefaultPanel()
	if p.Listen != "unix:/run/mc-panel/panel.sock" {
		t.Fatalf("prod listen = %q", p.Listen)
	}
	if path, ok := p.ListenUnix(); !ok || path != "/run/mc-panel/panel.sock" {
		t.Fatalf("ListenUnix = %q, %v", path, ok)
	}
	d := config.DevPanel("/repo")
	if d.Listen != "127.0.0.1:8080" {
		t.Fatalf("dev listen = %q", d.Listen)
	}
	if _, ok := d.ListenUnix(); ok {
		t.Fatal("dev listen must be TCP")
	}
}

func TestListenValidation(t *testing.T) {
	cases := []struct {
		listen string
		ok     bool
	}{
		{"unix:/run/mc-panel/panel.sock", true},
		{"unix:/tmp/x.sock", true},
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"192.0.2.1:8080", true},
		{"unix:relative.sock", false},
		{"unix:", false},
		{"unix:/run/../etc/x.sock", false},
		{"unix:/run/x\x00.sock", false},
		{"8080", false},
		{"", false},
	}
	for _, tc := range cases {
		p := config.DefaultPanel()
		p.PublicURL = "https://panel.example.com"
		p.Listen = tc.listen
		err := p.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("listen %q: err = %v, want ok=%v", tc.listen, err, tc.ok)
		}
	}
}

func TestLoadPanelUnixListenFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	if err := os.WriteFile(path, []byte("public_url = \"https://x.org\"\nlisten = \"unix:/srv/panel.sock\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadPanel(path, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if sock, ok := p.ListenUnix(); !ok || sock != "/srv/panel.sock" {
		t.Fatalf("ListenUnix = %q, %v", sock, ok)
	}
}
