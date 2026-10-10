package unixsock_test

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/unixsock"
)

func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcu") // short: macOS limits socket paths to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "p.sock")
}

func TestListenModeStaleAndInUse(t *testing.T) {
	path := sockPath(t)
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close() // stale file, nobody listens

	l, err := unixsock.Listen(path, 0o660, -1)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer l.Close()
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode = %v, %v", fi, err)
	}
	if _, err := unixsock.Listen(path, 0o660, -1); !errors.Is(err, unixsock.ErrInUse) {
		t.Fatalf("second Listen = %v, want ErrInUse", err)
	}
}
