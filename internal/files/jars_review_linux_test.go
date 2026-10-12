//go:build linux

package files

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// rewriteKeepingMTime replaces the content of p (same size) and sets its mtime back, as a
// container can with utimensat; only the ctime shows the change.
func rewriteKeepingMTime(t *testing.T, p, content string) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	// Past the file system's timestamp granularity, so the ctime moves.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // G703: test fixture below t.TempDir()
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	now, err := os.Stat(p)
	if err != nil || now.Size() != fi.Size() || !now.ModTime().Equal(fi.ModTime()) {
		t.Fatalf("rewrite changed size or mtime: %v", err)
	}
}

// A jar rewritten in place with the same size and its old mtime is hashed again: the ctime is
// part of the cache key (the container cannot set it).
func TestJarsCacheNoticesRestoredMTime(t *testing.T) {
	s, data := newService(t)
	p := filepath.Join(data, "plugins", "a.jar")
	put(t, data, "plugins/a.jar", "aaaa")
	listJars(t, s, filesapi.DirPlugins)
	rewriteKeepingMTime(t, p, "AAAA")
	if h256, _ := sums("AAAA"); jarByName(t, listJars(t, s, filesapi.DirPlugins), "a.jar").SHA256 != h256 {
		t.Fatal("a jar rewritten with its old mtime kept its stale checksum")
	}
}

// A jar rewritten while it is hashed, even with its mtime set back, gets nothing and is not
// cached.
func TestJarsNoticesRewriteDuringHash(t *testing.T) {
	s, data := newService(t)
	p := filepath.Join(data, "plugins", "a.jar")
	put(t, data, "plugins/a.jar", "aaaa")
	var h countingHook
	s.jarHook = func(name string) {
		h.hook(name)
		if h.count(name) == 1 {
			rewriteKeepingMTime(t, p, "AAAA")
		}
	}
	if j := jarByName(t, listJars(t, s, filesapi.DirPlugins), "a.jar"); j.SHA256 != "" || j.SHA512 != "" {
		t.Fatalf("jar rewritten during the hash: %+v, want no checksums", j)
	}
	if h256, _ := sums("AAAA"); jarByName(t, listJars(t, s, filesapi.DirPlugins), "a.jar").SHA256 != h256 || h.count("a.jar") != 2 {
		t.Fatalf("next listing: computed %d times, want the new content hashed", h.count("a.jar"))
	}
}

// A sparse jar (fewer allocated blocks than its size) is never hashed: reading it costs no
// disk for the container but CPU for the service.
func TestJarsSkipsSparseJars(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/sparse.jar", "PK")
	p := filepath.Join(data, "plugins", "sparse.jar")
	if err := os.Truncate(p, 64<<20); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Blocks*512 >= fi.Size() {
		t.Skip("the file system does not create sparse files")
	}
	put(t, data, "plugins/dense.jar", "dense")
	res := listJars(t, s, filesapi.DirPlugins)
	if j := jarByName(t, res, "sparse.jar"); j.SHA256 != "" || j.SHA512 != "" || j.Size != 64<<20 {
		t.Fatalf("sparse jar: %+v, want no checksums", j)
	}
	if jarByName(t, res, "dense.jar").SHA256 == "" {
		t.Fatal("a regular jar lost its checksum")
	}
}
