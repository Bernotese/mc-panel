package instances_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

// TestInstancesDirBelongsToMCGroup: instances/ carries the gid of the container user (the files
// service runs as that user and must traverse it); deleted/ keeps the agent's own group.
func TestInstancesDirBelongsToMCGroup(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	fi, err := os.Stat(filepath.Join(e.dir, "instances"))
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no stat_t on this platform")
	}
	if int(st.Gid) != os.Getgid() || int(st.Uid) != os.Geteuid() {
		t.Fatalf("instances/ owner = %d:%d, want %d:%d", st.Uid, st.Gid, os.Geteuid(), os.Getgid())
	}
	if fi.Mode().Perm() != 0o750 {
		t.Fatalf("instances/ mode = %v, want 0750", fi.Mode().Perm())
	}
}

// TestCheckInstancesDirAcceptsGroupReadable: a group-readable (but not group-writable) tree is
// fine for the strict start check; only write bits for group/others are refused.
func TestCheckInstancesDirAcceptsGroupReadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "srv")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := instances.CheckInstancesDir(dir, false, 0); err != nil {
		t.Fatalf("non-strict: %v", err)
	}
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	// Strict mode also walks the parents (t.TempDir lives below a world-writable sticky /tmp or
	// a user-owned folder), so only the last component's group-write bit is asserted here via the
	// error text.
	err := instances.CheckInstancesDir(dir, true, uint32(os.Getuid())) //nolint:gosec // G115: test uid is non-negative
	if err == nil {
		t.Fatal("strict check accepted a group-writable instances_dir")
	}
}
