package instances_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

// TestTmpDirLayout: tmp/ belongs to the agent (0700, nobody else can swap an entry) and
// tmp/<id> to the container user (0700); both sit outside instances/, where the files
// service and backups look.
func TestTmpDirLayout(t *testing.T) {
	e := newEnv(t)
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	if uid, gid, mode := owner(t, filepath.Join(e.dir, "tmp")); uid != os.Geteuid() || gid != os.Getegid() || mode != 0o700 {
		t.Fatalf("tmp/ = %d:%d %v, want %d:%d 0700", uid, gid, mode, os.Geteuid(), os.Getegid())
	}
	e.d.AddImage(java21Pinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	if uid, gid, mode := owner(t, instances.TmpDir(e.dir, 1)); uid != os.Getuid() || gid != os.Getgid() || mode != 0o700 {
		t.Fatalf("tmp/1 = %d:%d %v, want the container user and 0700", uid, gid, mode)
	}
	if binds := e.container(t, instances.ContainerName(1)).Config.HostConfig.Binds; len(binds) != 2 || binds[1] != instances.TmpDir(e.dir, 1)+":/tmp" {
		t.Fatalf("binds = %v", binds)
	}
}

// TestTmpDirIsEmptiedOnRecreateAndRemovedWithTheInstance: nothing written to /tmp survives a
// new container, and a removed instance leaves no tmp directory behind.
func TestTmpDirIsEmptiedOnRecreateAndRemovedWithTheInstance(t *testing.T) {
	e := newEnv(t)
	e.d.AddImage(java21Pinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	tmp := instances.TmpDir(e.dir, 1)
	if err := os.MkdirAll(filepath.Join(tmp, "x", "y"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "pack.mrpack"), []byte("pack"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(tmp, "link")); err != nil {
		t.Fatal(err)
	}

	// Same revision without recreate: the running container keeps its /tmp.
	e.ensure(t, ensureReq(1, "survival", 1))
	if _, err := os.Stat(filepath.Join(tmp, "pack.mrpack")); err != nil {
		t.Fatalf("tmp emptied without a recreate: %v", err)
	}

	req := ensureReq(1, "survival", 2)
	req.Recreate = true
	e.ensure(t, req)
	entries, err := os.ReadDir(tmp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("tmp after recreate = %v, %v; want empty", entries, err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Fatalf("symlink target touched: %q, %v", b, err)
	}

	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if _, err := os.Lstat(tmp); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("tmp/1 after remove: %v", err)
	}
}

// TestPowerStartRestoresTmpDir: a start finds tmp/<id> with its owner and mode (Docker
// would create a missing bind source as root 0755) and does not empty it.
func TestPowerStartRestoresTmpDir(t *testing.T) {
	e := newEnv(t)
	e.d.AddImage(java21Pinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	tmp := instances.TmpDir(e.dir, 1)
	if err := os.WriteFile(filepath.Join(tmp, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	e.power(t, 1, agentapi.PowerStart)
	if got := perm(t, tmp); got != 0o700 {
		t.Fatalf("tmp/1 = %v after start, want 0700", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, "keep")); err != nil {
		t.Fatalf("start emptied tmp/1: %v", err)
	}

	if err := os.RemoveAll(tmp); err != nil {
		t.Fatal(err)
	}
	e.power(t, 1, agentapi.PowerRestart)
	if uid, gid, mode := owner(t, tmp); uid != os.Getuid() || gid != os.Getgid() || mode != 0o700 {
		t.Fatalf("tmp/1 after restart = %d:%d %v, want the container user and 0700", uid, gid, mode)
	}
	if s := e.status(t, 1); s.LastError != "" {
		t.Fatalf("last_error = %q", s.LastError)
	}
}

// TestSymlinkedTmpDirIsReplaced: a symlink in place of tmp/<id> is removed as a link (never
// followed) and replaced by a real directory.
func TestSymlinkedTmpDirIsReplaced(t *testing.T) {
	e := newEnv(t)
	e.d.AddImage(java21Pinned)
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, instances.TmpDir(e.dir, 1)); err != nil {
		t.Fatal(err)
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.LastError != "" || s.State == agentapi.StateMissing {
		t.Fatalf("status = %+v", s)
	}
	if fi, err := os.Lstat(instances.TmpDir(e.dir, 1)); err != nil || !fi.IsDir() {
		t.Fatalf("tmp/1 = %v, %v; want a real directory", fi, err)
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Fatalf("symlink target touched: %v", err)
	}
}
