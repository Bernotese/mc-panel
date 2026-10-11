package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestDefaultFiles(t *testing.T) {
	f := config.DefaultFiles()
	if f.Socket != "/run/mc-panel-files/files.sock" || f.SocketMode != "0660" || f.SocketFileMode() != 0o660 ||
		f.InstancesDir != "/srv/mc-panel" || f.PanelUID != -1 || f.Dev {
		t.Fatalf("defaults = %+v", f)
	}
	l := f.Limits
	if l.MaxUploadBytes != 4<<30 || l.MaxExtractBytes != 20<<30 || l.MaxExtractEntries != 100000 ||
		l.ReserveBytes != 2<<30 || l.MaxTextBytes != 5<<20 || l.MaxListEntries != 10000 {
		t.Fatalf("limits = %+v", l)
	}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "panel_uid") {
		t.Fatalf("Validate without panel_uid = %v", err)
	}
	f.PanelUID = 998
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDevFiles(t *testing.T) {
	root := t.TempDir()
	f := config.DevFiles(root)
	if !f.Dev || f.Socket != filepath.Join(root, ".dev", "files.sock") || f.InstancesDir != filepath.Join(root, ".dev", "mc") || f.PanelUID != os.Getuid() {
		t.Fatalf("dev = %+v", f)
	}
	// Same instance directories as the dev agent (MCPANEL_DEV_DOCKER=1 shares them).
	if a := config.DevAgent(root); a.InstancesDir != f.InstancesDir {
		t.Fatalf("agent %s != files %s", a.InstancesDir, f.InstancesDir)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.toml")
	src := "panel_uid = 998\ninstances_dir = \"/data/mc\"\nsocket_mode = \"0600\"\n[limits]\nmax_upload_bytes = 1048576\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := config.LoadFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.PanelUID != 998 || f.InstancesDir != "/data/mc" || f.SocketFileMode() != 0o600 || f.Limits.MaxUploadBytes != 1<<20 {
		t.Fatalf("loaded = %+v", f)
	}
	// Limits not named in the file keep their defaults.
	want := config.DefaultFiles().Limits
	want.MaxUploadBytes = 1 << 20
	if f.Limits != want {
		t.Fatalf("limits = %+v, want %+v", f.Limits, want)
	}
}

func TestLoadFilesRejects(t *testing.T) {
	for name, src := range map[string]string{
		"unknown key":      "panel_uid = 998\nmystery = 1\n",
		"unknown limit":    "panel_uid = 998\n[limits]\nmax_foo = 1\n",
		"relative dir":     "panel_uid = 998\ninstances_dir = \"srv\"\n",
		"root dir":         "panel_uid = 998\ninstances_dir = \"/\"\n",
		"zero limit":       "panel_uid = 998\n[limits]\nmax_text_bytes = 0\n",
		"negative reserve": "panel_uid = 998\n[limits]\nreserve_bytes = -1\n",
		"no panel uid":     "instances_dir = \"/srv/mc-panel\"\n",
		"relative socket":  "panel_uid = 998\nsocket = \"files.sock\"\n",
		"text over json":   "panel_uid = 998\n[limits]\nmax_text_bytes = 10485760\n", // > MaxJSONBytes/6
		"mode not octal":   "panel_uid = 998\nsocket_mode = \"660x\"\n",
		"mode too wide":    "panel_uid = 998\nsocket_mode = \"1777\"\n",
	} {
		path := filepath.Join(t.TempDir(), "files.toml")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFiles(path); err == nil {
			t.Errorf("%s: LoadFiles accepted %q", name, src)
		}
	}
}

func TestPanelFilesSocket(t *testing.T) {
	if got := config.DefaultPanel().FilesSocket; got != "/run/mc-panel-files/files.sock" {
		t.Fatalf("default files_socket = %q", got)
	}
	root := t.TempDir()
	if got := config.DevPanel(root).FilesSocket; got != filepath.Join(root, ".dev", "files.sock") {
		t.Fatalf("dev files_socket = %q", got)
	}
	p := config.DefaultPanel()
	p.PublicURL = "https://panel.example.com"
	p.FilesSocket = ""
	if err := p.Validate(); err == nil {
		t.Fatal("empty files_socket accepted")
	}
}

func TestFilesPanelUIDRange(t *testing.T) {
	f := config.DefaultFiles()
	f.Dev = true
	f.PanelUID = -2
	if err := f.Validate(); err == nil {
		t.Fatal("panel_uid -2 accepted")
	}
	f.PanelUID = -1
	if err := f.Validate(); err != nil {
		t.Fatalf("dev with unset panel_uid: %v", err)
	}
}

func TestFilesTextLimitFitsJSON(t *testing.T) {
	f := config.DefaultFiles()
	f.PanelUID = 998
	f.Limits.MaxTextBytes = (filesapi.MaxJSONBytes - filesapi.MaxSmallJSONBytes) / 6
	if err := f.Validate(); err != nil {
		t.Fatalf("largest text limit: %v", err)
	}
	f.Limits.MaxTextBytes++
	if err := f.Validate(); err == nil {
		t.Fatal("text limit above (MaxJSONBytes-MaxSmallJSONBytes)/6 accepted")
	}
}
