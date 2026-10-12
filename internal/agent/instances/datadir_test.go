package instances_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

// otherGroup returns a gid different from the agent's own group that the test process may chown
// to: any gid as root, otherwise a supplementary group. Without one the test is skipped.
func otherGroup(t *testing.T) int {
	t.Helper()
	egid := os.Getegid()
	if os.Geteuid() == 0 {
		return egid + 4242
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Skipf("getgroups: %v", err)
	}
	for _, g := range groups {
		if g != egid {
			return g
		}
	}
	t.Skip("no supplementary group to tell the mc group apart from the agent's group")
	return 0
}

// owner returns uid, gid and permission bits of path (not following a symlink).
func owner(t *testing.T, path string) (uid, gid int, mode os.FileMode) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no stat_t on this platform")
	}
	return int(st.Uid), int(st.Gid), fi.Mode().Perm()
}

// TestInstancesDirBelongsToMCGroup: instances/ carries the gid of the container user (the files
// service runs as that user and must traverse it); deleted/ keeps the agent's own group. The mc
// group differs from the agent's group, so a regression to Getegid for instances/ fails.
func TestInstancesDirBelongsToMCGroup(t *testing.T) {
	mcGID := otherGroup(t)
	e := newEnv(t, func(c *instances.Config) { c.GID = mcGID })
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	if uid, gid, mode := owner(t, filepath.Join(e.dir, "instances")); uid != os.Geteuid() || gid != mcGID || mode != 0o750 {
		t.Fatalf("instances/ = %d:%d %v, want %d:%d 0750", uid, gid, mode, os.Geteuid(), mcGID)
	}
	if uid, gid, mode := owner(t, instances.DeletedDir(e.dir)); uid != os.Geteuid() || gid != os.Getegid() || mode != 0o700 {
		t.Fatalf("deleted/ = %d:%d %v, want %d:%d 0700", uid, gid, mode, os.Geteuid(), os.Getegid())
	}
}

// TestInitRepairsExistingLayout: an upgraded host keeps its instances/ from before (0700, or
// loosened, or with a foreign group). Init at agent start fixes it without any Ensure call, so the
// files service can enter it although no container is recreated.
func TestInitRepairsExistingLayout(t *testing.T) {
	mcGID := otherGroup(t)
	for _, mode := range []os.FileMode{0o700, 0o777} {
		e := newEnv(t, func(c *instances.Config) { c.GID = mcGID })
		dir := filepath.Join(e.dir, "instances")
		if err := os.Mkdir(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, -1, os.Getegid()); err != nil { // foreign group (not the mc group)
			t.Fatal(err)
		}
		if err := e.m.Init(); err != nil {
			t.Fatal(err)
		}
		if uid, gid, got := owner(t, dir); uid != os.Geteuid() || gid != mcGID || got != 0o750 {
			t.Fatalf("instances/ (was %v) = %d:%d %v, want %d:%d 0750", mode, uid, gid, got, os.Geteuid(), mcGID)
		}
		if got := perm(t, instances.DeletedDir(e.dir)); got != 0o700 {
			t.Fatalf("deleted/ = %v, want 0700", got)
		}
	}
}

// TestInitRefusesSymlinkedInstancesDir: a symlink in place of instances/ is refused, never followed.
func TestInitRefusesSymlinkedInstancesDir(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(e.dir, "instances")); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Init(); err == nil {
		t.Fatal("Init followed a symlinked instances/")
	}
	if got := perm(t, target); got != 0o755 {
		t.Fatalf("symlink target mode changed to %v", got)
	}
}

// TestCheckInstancesDirAcceptsGroupReadable: a group-readable (but not group-writable) tree is
// fine for the strict start check; only write bits for group/others are refused.
func TestCheckInstancesDirAcceptsGroupReadable(t *testing.T) {
	// Real path (macOS: /var is a symlink) with parents the strict walk accepts regardless of the umask.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "mc")
	dir := filepath.Join(parent, "srv")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for p, mode := range map[string]os.FileMode{parent: 0o755, dir: 0o750} {
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	trusted := uint32(os.Getuid()) //nolint:gosec // G115: test uid is non-negative
	// The strict walk also checks base's parents; skip if the temp directory itself lives in an
	// unusual (group-writable, foreign-owned) place.
	if err := instances.CheckInstancesDir(base, true, trusted); err != nil {
		t.Skipf("temp dir not usable for the strict check: %v", err)
	}
	if err := instances.CheckInstancesDir(dir, false, 0); err != nil {
		t.Fatalf("non-strict: %v", err)
	}
	if err := instances.CheckInstancesDir(dir, true, trusted); err != nil {
		t.Fatalf("strict check refused a 0750 instances_dir: %v", err)
	}
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	err = instances.CheckInstancesDir(dir, true, trusted)
	if err == nil || !strings.Contains(err.Error(), dir+" is writable by group") {
		t.Fatalf("strict check of a group-writable instances_dir = %v, want %q named as writable by group", err, dir)
	}
}

// TestInitCreatesBackupsDir: backups/ belongs to the container user (the files service runs as
// that user and owns the restic repositories) with 0700; a loosened mode is repaired, and a
// symlink in its place is refused, never followed (spec 4 §3.1).
func TestInitCreatesBackupsDir(t *testing.T) {
	e := newEnv(t)
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	dir := instances.BackupsDir(e.dir)
	if dir != filepath.Join(e.dir, "backups") {
		t.Fatalf("BackupsDir = %s", dir)
	}
	if uid, gid, mode := owner(t, dir); uid != os.Getuid() || gid != os.Getgid() || mode != 0o700 {
		t.Fatalf("backups/ = %d:%d %v, want %d:%d 0700", uid, gid, mode, os.Getuid(), os.Getgid())
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dir); got != 0o700 {
		t.Fatalf("backups/ after repair = %v, want 0700", got)
	}
}

func TestInitRefusesSymlinkedBackupsDir(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, instances.BackupsDir(e.dir)); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Init(); err == nil {
		t.Fatal("Init followed a symlinked backups/")
	}
	if got := perm(t, target); got != 0o755 {
		t.Fatalf("symlink target mode changed to %v", got)
	}
}
