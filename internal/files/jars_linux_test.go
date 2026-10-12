//go:build linux

package files

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Review focus 5: symlinked jars (out of the instance and inside it), a FIFO and a folder
// with a .jar name are left out; nothing outside the data directory is read, nothing blocks.
func TestJarsSkipsLinksAndSpecialFiles(t *testing.T) {
	s, data := newService(t)
	outside := layout(t, data) // plugins/EssentialsX.jar is a regular file
	plugins := filepath.Join(data, "plugins")
	if err := os.Symlink(outside, filepath.Join(plugins, "secret.jar")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("EssentialsX.jar", filepath.Join(plugins, "alias.jar")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(plugins, "pipe.jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(plugins, "dir.jar.disabled"), 0o755); err != nil {
		t.Fatal(err)
	}
	type result struct {
		res filesapi.JarsResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := s.Jars(ctx, 1, filesapi.DirPlugins)
		done <- result{res, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("jars blocks on a FIFO")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	if want := []string{"EssentialsX.jar"}; !reflect.DeepEqual(jarNames(r.res), want) {
		t.Fatalf("jars %v, want %v", jarNames(r.res), want)
	}
	if h256, _ := sums("PK\x00\x01binary"); r.res.Jars[0].SHA256 != h256 {
		t.Fatalf("checksum of the regular jar: %+v", r.res.Jars[0])
	}
}

// A plugins or mods folder that is a symlink is never read, wherever it points.
func TestJarsIgnoresSymlinkedFolder(t *testing.T) {
	s, data := newService(t)
	put(t, data, "real/a.jar", "a")
	if err := os.Symlink("real", filepath.Join(data, "plugins")); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(s.cfg.InstancesDir, "elsewhere")
	put(t, elsewhere, "b.jar", "b")
	if err := os.Symlink(elsewhere, filepath.Join(data, "mods")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filesapi.DirPlugins, filesapi.DirMods} {
		if res := listJars(t, s, dir); len(res.Jars) != 0 {
			t.Errorf("%s through a symlink: %v", dir, jarNames(res))
		}
	}
}

// A jar swapped for a FIFO after findJars saw it: describeJar skips it without blocking.
func TestDescribeJarSkipsSwappedFIFO(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/real.jar", "x")
	if err := syscall.Mkfifo(filepath.Join(data, "plugins", "pipe.jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fi, err := root.Lstat("plugins/real.jar")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, ok, err := s.describeJar(ctx, root, &jarScan{id: 1, dir: filesapi.DirPlugins, budget: s.jarHashBudget}, jarFile{name: "pipe.jar", fi: fi})
		done <- result{ok, err}
	}()
	select {
	case r := <-done:
		if r.ok || r.err != nil {
			t.Fatalf("swapped FIFO: ok=%v err=%v, want skipped", r.ok, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("describeJar blocks on a FIFO")
	}
}
