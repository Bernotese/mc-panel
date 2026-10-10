//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"mcpanel/internal/config"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// openDB tightens an existing, too-open data directory and database files to 0700/0600.
func TestOpenDBTightensPermissions(t *testing.T) {
	old := syscall.Umask(0o022) // simulate a process that did not restrict its umask
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DevPanel(t.TempDir())
	cfg.DataDir = dir
	db, err := openDB(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS perm_probe (x)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for _, f := range []string{"panel.db", "panel.db-wal", "panel.db-shm"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err == nil {
			_ = os.Chmod(p, 0o644) // a previous run without the fix left it world-readable
		}
	}
	_ = os.Chmod(dir, 0o755)

	db, err = openDB(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if m := mode(t, dir); m != 0o700 {
		t.Fatalf("data dir mode = %v, want 0700", m)
	}
	for _, f := range []string{"panel.db", "panel.db-wal", "panel.db-shm"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if m := mode(t, p); m != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", f, m)
		}
	}
}

func TestCheckPrivateRejectsGroupOtherBits(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivate(p); err != nil {
		t.Fatalf("0600: %v", err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivate(p); err == nil || !strings.Contains(err.Error(), "group/others") {
		t.Fatalf("0640: err = %v", err)
	}
}

func TestRestrictUmask(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	restrictUmask()
	if got := syscall.Umask(0o022); got != 0o077 {
		t.Fatalf("umask = %#o, want 0077", got)
	}
}
