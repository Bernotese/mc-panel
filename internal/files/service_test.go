package files

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestOpenInstance(t *testing.T) {
	s, data := newService(t)
	root, err := s.open(1)
	if err != nil {
		t.Fatalf("open existing: %v", err)
	}
	_ = root.Close()

	if _, err := s.open(2); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("missing data dir: %v, want ErrNotFound", err)
	} else if strings.Contains(err.Error(), s.cfg.InstancesDir) {
		t.Fatalf("not-found message leaks the host path: %v", err)
	}
	if _, err := s.open(0); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v, want ErrInvalid", err)
	}

	// A data directory that is a symlink is never opened, not even to a sibling instance.
	if err := os.Symlink(data, filepath.Join(s.cfg.InstancesDir, "instances", "3")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.open(3); err == nil || errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("symlinked data dir: %v, want a hard error", err)
	}
}

func TestOpenInstanceDevCreatesDataDir(t *testing.T) {
	s, _ := newService(t, func(c *Config) { c.Dev = true })
	root, err := s.open(7)
	if err != nil {
		t.Fatalf("dev open: %v", err)
	}
	_ = root.Close()
	if fi, err := os.Stat(s.instanceDir(7)); err != nil || !fi.IsDir() {
		t.Fatalf("dev mode did not create the data dir: %v", err)
	}
}

func TestLstatNeverFollowsComponents(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "x")
	if err := os.Symlink("world", filepath.Join(data, "current")); err != nil {
		t.Fatal(err)
	}
	put(t, data, "file", "x")
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if fi, err := lstat(root, "world/level.dat"); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("plain path: %v %v", fi, err)
	}
	if fi, err := lstat(root, "current"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink itself must be reported as symlink: %v %v", fi, err)
	}
	if _, err := lstat(root, "current/level.dat"); !errors.Is(err, filesapi.ErrNotRegular) {
		t.Fatalf("path through a symlinked folder: %v, want ErrNotRegular", err)
	}
	if _, err := lstat(root, "file/x"); !errors.Is(err, filesapi.ErrNotRegular) && !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("path through a file: %v", err)
	}
	if _, err := lstat(root, "missing/x"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("missing parent: %v, want ErrNotFound", err)
	}
	if fi, err := lstat(root, ""); err != nil || !fi.IsDir() {
		t.Fatalf("root: %v %v", fi, err)
	}
}

func TestCleanVisible(t *testing.T) {
	for _, p := range []string{".rcon-cli.env", ".rcon-cli.yaml", ".mcpanel-tmp-ab", "plugins/.mcpanel-x/y"} {
		if _, err := cleanVisible(p); !errors.Is(err, filesapi.ErrNotFound) {
			t.Errorf("%q: %v, want ErrNotFound", p, err)
		}
	}
	for _, p := range []string{"../x", "/abs", `a\b`, "a//b"} {
		if _, err := cleanVisible(p); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%q: %v, want ErrInvalid", p, err)
		}
	}
	if c, err := cleanVisible("plugins/.rcon-cli.env"); err != nil || c != "plugins/.rcon-cli.env" {
		t.Errorf("nested .rcon-cli.env is a normal file: %q %v", c, err)
	}
}

func TestCleanupTempRemovesLeftovers(t *testing.T) {
	s, data := newService(t)
	put(t, data, ".mcpanel-tmp-1", "partial upload")
	put(t, data, "world/.mcpanel-tmp-2/region/r.mca", "partial extract")
	put(t, data, "world/level.dat", "keep")
	put(t, data, ".mcpanel-other", "not a temp name")
	if n := s.CleanupTemp(); n != 2 {
		t.Fatalf("removed %d entries, want 2", n)
	}
	if _, ok := get(t, data, ".mcpanel-tmp-1"); ok {
		t.Fatal("temp file survived")
	}
	if _, err := os.Stat(filepath.Join(data, "world", ".mcpanel-tmp-2")); !os.IsNotExist(err) {
		t.Fatalf("temp folder survived: %v", err)
	}
	if v, ok := get(t, data, "world/level.dat"); !ok || v != "keep" {
		t.Fatal("cleanup touched a normal file")
	}
	if _, ok := get(t, data, ".mcpanel-other"); !ok {
		t.Fatal("cleanup removed a file that is not a temp name")
	}
}

func TestReserve(t *testing.T) {
	disk := &fakeDisk{avail: 10 << 10}
	s, _ := newService(t, func(c *Config) { c.Disk = disk })
	r, err := s.reserve(1, 9<<10) // 9 KiB + 1 KiB reserve = 10 KiB
	if err != nil {
		t.Fatalf("exactly enough: %v", err)
	}
	r.release()
	if _, err := s.reserve(1, 9<<10+1); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("one byte too much: %v", err)
	}
	disk.err = errors.New("statfs broke")
	if _, err := s.reserve(1, 1); err == nil || errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("statfs error must surface as an internal error: %v", err)
	}
}

// Space announced by running uploads, writes and extract jobs counts until it has landed on
// disk (then statfs shows it) or the operation ended.
func TestReserveCountsInFlightOperations(t *testing.T) {
	disk := &fakeDisk{avail: 10 << 10}
	s, _ := newService(t, func(c *Config) { c.Disk = disk })
	r1, err := s.reserve(1, 5<<10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserve(2, 5<<10); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("second reservation ignores the first: %v", err)
	}
	r1.landed(2 << 10) // 2 KiB written: statfs now reports them as used
	disk.set(8 << 10)
	r2, err := s.reserve(2, 4<<10) // 3 KiB still announced + 4 KiB + 1 KiB reserve = 8 KiB
	if err != nil {
		t.Fatalf("landed bytes still counted: %v", err)
	}
	r1.landed(10 << 10) // more than announced: never below zero
	r1.release()
	r2.release()
	r2.release() // idempotent
	if s.pending != 0 {
		t.Fatalf("pending after release: %d", s.pending)
	}
}

func TestStatusFor(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{filesapi.ErrNotFound, http.StatusNotFound, filesapi.CodeNotFound},
		{filesapi.ErrExists, http.StatusConflict, filesapi.CodeExists},
		{filesapi.ErrModified, http.StatusConflict, filesapi.CodeModified},
		{filesapi.ErrNotRegular, http.StatusBadRequest, filesapi.CodeNotRegular},
		{filesapi.ErrNotText, http.StatusUnsupportedMediaType, filesapi.CodeNotText},
		{filesapi.ErrTooLarge, http.StatusRequestEntityTooLarge, filesapi.CodeTooLarge},
		{filesapi.ErrInsufficientStorage, http.StatusInsufficientStorage, filesapi.CodeInsufficientStorage},
		{filesapi.ErrBusy, http.StatusTooManyRequests, filesapi.CodeBusy},
		{filesapi.ErrInvalid, http.StatusBadRequest, filesapi.CodeInvalid},
		{errors.New("boom"), http.StatusInternalServerError, ""},
	} {
		status, code := statusFor(fsErr(c.err))
		if status != c.status || code != c.code {
			t.Errorf("%v: %d %q, want %d %q", c.err, status, code, c.status, c.code)
		}
	}
}

func TestOpenRegularAndRequireDir(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	if err := os.Symlink("world/level.dat", filepath.Join(data, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("world", filepath.Join(data, "current")); err != nil {
		t.Fatal(err)
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	f, fi, err := openRegular(root, "world/level.dat", os.O_RDONLY)
	if err != nil || fi.Size() != 3 {
		t.Fatalf("regular file: %v %v", fi, err)
	}
	_ = f.Close()
	for p, want := range map[string]error{
		"link":              filesapi.ErrNotRegular,
		"current/level.dat": filesapi.ErrNotRegular,
		"world":             filesapi.ErrNotRegular,
		"missing":           filesapi.ErrNotFound,
	} {
		if _, _, err := openRegular(root, p, os.O_RDONLY); !errors.Is(err, want) {
			t.Errorf("openRegular %q: %v, want %v", p, err, want)
		}
	}

	if err := requireDir(root, ""); err != nil {
		t.Fatalf("root is a folder: %v", err)
	}
	if err := requireDir(root, "world"); err != nil {
		t.Fatalf("world: %v", err)
	}
	for p, want := range map[string]error{
		"current":         filesapi.ErrNotRegular,
		"world/level.dat": filesapi.ErrNotRegular,
		"missing":         filesapi.ErrNotFound,
	} {
		if err := requireDir(root, p); !errors.Is(err, want) {
			t.Errorf("requireDir %q: %v, want %v", p, err, want)
		}
	}
}

func TestCommit(t *testing.T) {
	s, data := newService(t)
	put(t, data, "dir/existing", "old")
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	stage := func(content string) string {
		t.Helper()
		tmp, err := tempName("dir")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(tmp, "dir/"+tempPrefix) || !filesapi.IsHidden(tmp) {
			t.Fatalf("temp name %q", tmp)
		}
		put(t, data, tmp, content)
		return tmp
	}

	tmp := stage("new")
	if err := commit(root, tmp, "dir/existing", false); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("commit without overwrite onto an existing file: %v, want ErrExists", err)
	}
	if v, _ := get(t, data, "dir/existing"); v != "old" {
		t.Fatalf("refused commit changed the target: %q", v)
	}
	if err := commit(root, tmp, "dir/fresh", false); err != nil {
		t.Fatalf("commit new: %v", err)
	}
	if v, _ := get(t, data, "dir/fresh"); v != "new" {
		t.Fatalf("committed %q", v)
	}
	if _, ok := get(t, data, tmp); ok {
		t.Fatal("temp file survived a commit")
	}
	tmp = stage("replaced")
	if err := commit(root, tmp, "dir/existing", true); err != nil {
		t.Fatalf("commit with overwrite: %v", err)
	}
	if v, _ := get(t, data, "dir/existing"); v != "replaced" {
		t.Fatalf("overwritten %q", v)
	}
	if n := names(t, filepath.Join(data, "dir")); len(n) != 2 {
		t.Fatalf("left over: %v", n)
	}
}

func TestParentOfAndParseID(t *testing.T) {
	for in, want := range map[string]string{"": "", "a": "", "a/b": "a", "a/b/c": "a/b"} {
		if got := parentOf(in); got != want {
			t.Errorf("parentOf(%q) = %q, want %q", in, got, want)
		}
	}
	if id, err := parseID("42"); err != nil || id != 42 {
		t.Fatalf("parseID 42: %d %v", id, err)
	}
	for _, v := range []string{"", "0", "-1", "x", "1.5", "99999999999999999999"} {
		if _, err := parseID(v); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("parseID(%q): %v, want ErrInvalid", v, err)
		}
	}
}

func TestOpenRegularOnlyReads(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "x")
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, flag := range []int{os.O_RDWR, os.O_WRONLY, os.O_RDONLY | os.O_TRUNC, os.O_RDONLY | os.O_CREATE} {
		if f, _, err := openRegular(root, "a.txt", flag); err == nil {
			_ = f.Close()
			t.Errorf("flag %#x accepted", flag)
		}
	}
	if v, _ := get(t, data, "a.txt"); v != "x" {
		t.Fatalf("file changed: %q", v)
	}
}

func TestCleanupTempNeverFollowsSymlinks(t *testing.T) {
	s, data := newService(t)
	outside := filepath.Join(t.TempDir(), "outside")
	put(t, outside, ".mcpanel-tmp-foreign", "not ours")
	put(t, data, "real/.mcpanel-tmp-target/keep", "target of a temp-named link")
	if err := os.Symlink(outside, filepath.Join(data, "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(data, "in")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real/.mcpanel-tmp-target", filepath.Join(data, ".mcpanel-tmp-link")); err != nil {
		t.Fatal(err)
	}
	// A data directory with a non-canonical name is not an instance.
	odd := filepath.Join(s.cfg.InstancesDir, "instances", "01")
	put(t, odd, ".mcpanel-tmp-odd", "x")

	if n := s.CleanupTemp(); n != 2 { // .mcpanel-tmp-link (the link) and real/.mcpanel-tmp-target
		t.Fatalf("removed %d entries, want 2", n)
	}
	if _, err := os.Lstat(filepath.Join(data, ".mcpanel-tmp-link")); !os.IsNotExist(err) {
		t.Fatalf("temp-named symlink survived: %v", err)
	}
	if _, ok := get(t, outside, ".mcpanel-tmp-foreign"); !ok {
		t.Fatal("cleanup followed a symlink out of the data directory")
	}
	if _, ok := get(t, odd, ".mcpanel-tmp-odd"); !ok {
		t.Fatal("cleanup touched a directory with a non-canonical id")
	}
	if fi, err := os.Lstat(filepath.Join(data, "in")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink to a folder was touched: %v", err)
	}
}
