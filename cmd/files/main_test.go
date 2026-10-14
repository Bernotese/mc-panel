package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
)

// Socket activation (unixsock.Activated) is tested in internal/unixsock, including a real
// fd-3 handover to a child process.

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, &out, &bytes.Buffer{}, func() int { return 0 }); err != nil {
		t.Fatalf("-version: %v", err)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("version output %q", out.String())
	}
}

func TestRunRefusesRoot(t *testing.T) {
	err := run(context.Background(), []string{"-dev"}, &bytes.Buffer{}, &bytes.Buffer{}, func() int { return 0 })
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root start: %v, want refusal", err)
	}
}

func TestRunRejectsArguments(t *testing.T) {
	if err := run(context.Background(), []string{"extra"}, &bytes.Buffer{}, &bytes.Buffer{}, func() int { return 1000 }); err == nil {
		t.Fatal("unexpected positional argument accepted")
	}
}

func TestRunRejectsBrokenConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "files.toml")
	if err := os.WriteFile(p, []byte("unknown_key = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-config", p}, &bytes.Buffer{}, &bytes.Buffer{}, func() int { return 1000 }); err == nil {
		t.Fatal("unknown config key accepted")
	}
}

func TestPanelUID(t *testing.T) {
	if _, err := panelUID(config.Files{PanelUID: -1}); err == nil {
		t.Fatal("unset panel_uid accepted outside dev mode")
	}
	if uid, err := panelUID(config.Files{PanelUID: -1, Dev: true}); err != nil || int(uid) != os.Getuid() {
		t.Fatalf("dev default: %d %v", uid, err)
	}
	if uid, err := panelUID(config.Files{PanelUID: 998}); err != nil || uid != 998 {
		t.Fatalf("configured uid: %d %v", uid, err)
	}
	if _, err := panelUID(config.Files{PanelUID: 1 << 40}); err == nil {
		t.Fatal("out-of-range uid accepted")
	}
}
