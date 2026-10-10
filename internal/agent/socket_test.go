package agent_test

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
)

func TestListenCreatesSocketWithMode(t *testing.T) {
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want socket with 0600", fi.Mode())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket not removed on close: %v", err)
	}
}

func TestListenSetsGroup(t *testing.T) {
	path := socketPath(t)
	gid := os.Getgid()
	l, err := agent.Listen(path, 0o660, gid)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	if int(st.Gid) != gid || fi.Mode().Perm() != 0o660 {
		t.Fatalf("gid=%d mode=%v, want gid=%d mode=0660", st.Gid, fi.Mode(), gid)
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close() // simulates a crashed agent: file stays, nobody listens
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer l.Close()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial new socket: %v", err)
	}
	_ = c.Close()
}

func TestListenRefusesWhenAgentRunning(t *testing.T) {
	path := socketPath(t)
	first, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := agent.Listen(path, 0o600, -1); !errors.Is(err, agent.ErrAlreadyRunning) {
		t.Fatalf("second Listen = %v, want ErrAlreadyRunning", err)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("first agent's socket was broken: %v", err)
	}
	_ = c.Close()
}

func TestListenRefusesNonSocket(t *testing.T) {
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Listen(path, 0o600, -1); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("Listen over regular file = %v, want 'not a socket' error", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me" {
		t.Fatalf("regular file was modified: %q, %v", b, err)
	}
}

func TestListenRefusesSymlink(t *testing.T) {
	path := socketPath(t)
	target := path + ".target"
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Listen(path, 0o600, -1); err == nil {
		t.Fatal("Listen over a symlink must fail")
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
}

func TestListenRefusesRelativeAndAbstractPaths(t *testing.T) {
	for _, path := range []string{"agent.sock", "./agent.sock", "", "@mcpanel-agent", "\x00mcpanel"} {
		l, err := agent.Listen(path, 0o600, -1)
		if err == nil {
			_ = l.Close()
			t.Errorf("Listen(%q) succeeded, want error", path)
		}
	}
}
