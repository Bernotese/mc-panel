package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// renderDeploy substitutes the installer placeholders (installer plan V3) with sample values.
func renderDeploy(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config", name))
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{
		"VERSION": "1.2.3", "PUBLIC_URL": "https://panel.example.com", "PANEL_UID": "998",
		"INSTANCES_DIR": "/srv/mc-panel", "RESTIC_BINARY": "/usr/bin/restic", "PING_ADDR": "127.0.0.1:25565",
	}
	s := regexp.MustCompile(`\{\{([A-Z0-9_]+)\}\}`).ReplaceAllStringFunc(string(b), func(m string) string {
		v, ok := vals[strings.Trim(m, "{}")]
		if !ok {
			t.Fatalf("%s: placeholder %s has no sample value", name, m)
		}
		return v
	})
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil { //nolint:gosec // G703: p is in t.TempDir, name is a fixed template name
		t.Fatal(err)
	}
	return p
}

func TestDeployPanelTemplate(t *testing.T) {
	p, err := LoadPanel(renderDeploy(t, "panel.toml"), false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if path, ok := p.ListenUnix(); !ok || path != "/run/mc-panel-web/panel.sock" {
		t.Fatalf("listen = %q", p.Listen)
	}
}

func TestDeployAgentTemplate(t *testing.T) {
	a, err := LoadAgent(renderDeploy(t, "agent.toml"), false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a.PanelUID != 998 || a.InstancesDir != "/srv/mc-panel" {
		t.Fatalf("agent = %+v", a)
	}
}

func TestDeployFilesTemplate(t *testing.T) {
	f, err := LoadFiles(renderDeploy(t, "files.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if f.ResticBinary != "/usr/bin/restic" {
		t.Fatalf("restic_binary = %q", f.ResticBinary)
	}
}
