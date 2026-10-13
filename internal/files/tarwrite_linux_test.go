//go:build linux

package files

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBackupTarSkipsSpecialFiles(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "a")
	if err := syscall.Mkfifo(filepath.Join(data, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "mcs") // socket paths are short
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Rename(filepath.Join(dir, "s"), filepath.Join(data, "sock")); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(readTar(t, b)); len(got) != 1 || got[0] != "a.txt" || skipped != 2 {
		t.Fatalf("entries %v, skipped %d", got, skipped)
	}
}

// TestBackupTarBadNames: names that filesapi.CleanPath refuses (control characters, invalid
// UTF-8, a backslash) are left out with their content - Linux allows any byte but '/' and NUL.
func TestBackupTarBadNames(t *testing.T) {
	s, data := newService(t)
	put(t, data, "ok.txt", "ok")
	for _, n := range []string{"tab\tname", "bad\xffutf8", `back\slash`} {
		put(t, data, n+"/inner.txt", "x")
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(readTar(t, b)); len(got) != 1 || got[0] != "ok.txt" || skipped != 3 {
		t.Fatalf("entries %v, skipped %d", got, skipped)
	}
}
