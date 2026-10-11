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

// The real fd-3 path of socket activation is exercised on the test VM and in
// scripts/ci-instances.sh (Phase C), not here: a unit test cannot own fd 3 safely.

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

func TestActivatedListener(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var unset []string
	unsetenv := func(k string) error { unset = append(unset, k); return nil }

	l, err := activatedListener(env(nil), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("no activation: %v %v", l, err)
	}
	if len(unset) != 3 {
		t.Fatalf("activation variables not removed: %v", unset)
	}
	l, err = activatedListener(env(map[string]string{"LISTEN_PID": "7", "LISTEN_FDS": "1"}), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("activation for another pid must be ignored: %v %v", l, err)
	}
	if _, err := activatedListener(env(map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2"}), unsetenv, 42); err == nil {
		t.Fatal("two sockets accepted")
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
