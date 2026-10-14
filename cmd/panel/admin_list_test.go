package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRunAdminList(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "bob", "admin")
	storetest.CreateUser(t, db, "alice", "admin")
	storetest.CreateUser(t, db, "carol", "user")
	var out bytes.Buffer
	if err := runAdminList(context.Background(), db, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "alice\nbob\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunAdminListEmpty(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "carol", "user")
	var out bytes.Buffer
	if err := runAdminList(context.Background(), db, &out); err != nil || out.Len() != 0 {
		t.Fatalf("no admins: %q %v", out.String(), err)
	}
}

func TestAdminListCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out, errOut bytes.Buffer
	if code := run([]string{"admin", "list", "-dev"}, f, &out, &errOut); code != 0 || out.Len() != 0 {
		t.Fatalf("admin list without a DB: code %d, out %q, err %q", code, out.String(), errOut.String())
	}
	// Read-only: no data directory and no database appear.
	if _, err := os.Lstat(".dev"); !os.IsNotExist(err) {
		t.Fatalf("admin list created .dev: %v", err)
	}
	if code := run([]string{"admin", "list", "-dev", "extra"}, f, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "unexpected arguments") {
		t.Fatalf("extra argument: code %d, err %q", code, errOut.String())
	}
}

func TestUsageMentionsAdminList(t *testing.T) {
	if !strings.Contains(usageText, "panel admin list") {
		t.Fatal("usage does not mention admin list")
	}
}

// TestAdminListCommandReadOnly reads a database the panel wrote and closed (no -wal/-shm left)
// without changing anything in the data directory.
func TestAdminListCommandReadOnly(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultPanel()
	cfg.DataDir = t.TempDir()
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	storetest.CreateUser(t, db, "bob", "admin")
	storetest.CreateUser(t, db, "alice", "admin")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before := dirNames(t, cfg.DataDir)
	sum := fileBytes(t, cfg.DBPath())

	var out bytes.Buffer
	if err := adminListCommand(ctx, cfg, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "alice\nbob\n" {
		t.Fatalf("output = %q", got)
	}
	if after := dirNames(t, cfg.DataDir); !slices.Equal(before, after) {
		t.Fatalf("data directory changed: %v -> %v", before, after)
	}
	if !bytes.Equal(sum, fileBytes(t, cfg.DBPath())) {
		t.Fatal("database file changed")
	}
}

// TestAdminListCommandWhilePanelRuns reads a database that is still open (the running panel):
// admins committed to the WAL are seen, and no file is added.
func TestAdminListCommandWhilePanelRuns(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultPanel()
	cfg.DataDir = t.TempDir()
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	storetest.CreateUser(t, db, "carol", "admin")
	before := dirNames(t, cfg.DataDir)
	var out bytes.Buffer
	if err := adminListCommand(ctx, cfg, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "carol\n" {
		t.Fatalf("output = %q", got)
	}
	if after := dirNames(t, cfg.DataDir); !slices.Equal(before, after) {
		t.Fatalf("data directory changed: %v -> %v", before, after)
	}
}

func TestAdminListCommandRefusesSymlink(t *testing.T) {
	cfg := config.DefaultPanel()
	cfg.DataDir = t.TempDir()
	if err := os.Symlink(filepath.Join(cfg.DataDir, "elsewhere.db"), cfg.DBPath()); err != nil {
		t.Fatal(err)
	}
	if err := adminListCommand(context.Background(), cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("symlinked database accepted")
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: test file in t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	return b
}
