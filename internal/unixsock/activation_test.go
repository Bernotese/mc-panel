//go:build unix

package unixsock

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestActivatedEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var unset []string
	unsetenv := func(k string) error { unset = append(unset, k); return nil }

	l, err := Activated(env(nil), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("no activation: %v %v", l, err)
	}
	if len(unset) != 3 {
		t.Fatalf("activation variables not removed: %v", unset)
	}
	l, err = Activated(env(map[string]string{"LISTEN_PID": "7", "LISTEN_FDS": "1"}), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("activation for another pid must be ignored: %v %v", l, err)
	}
	if _, err := Activated(env(map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2"}), unsetenv, 42); err == nil {
		t.Fatal("two sockets accepted")
	}
	if _, err := Activated(env(map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "x"}), unsetenv, 42); err == nil {
		t.Fatal("malformed LISTEN_FDS accepted")
	}
}

// TestActivatedHandover passes a real unix socket as fd 3 to a child process (this test
// binary, re-executed), which must accept on it – the path systemd takes.
func TestActivatedHandover(t *testing.T) {
	if os.Getenv("UNIXSOCK_CHILD") == "1" {
		// The parent cannot know the child's pid before Start: "child" stands for it.
		getenv := func(k string) string {
			if k == "LISTEN_PID" && os.Getenv(k) == "child" {
				return strconv.Itoa(os.Getpid())
			}
			return os.Getenv(k)
		}
		l, err := Activated(getenv, os.Unsetenv, os.Getpid())
		if err != nil || l == nil {
			os.Exit(3)
		}
		c, err := l.Accept()
		if err != nil {
			os.Exit(4)
		}
		_, _ = c.Write([]byte("ok"))
		_ = c.Close()
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "act") // short path (macOS: 104-byte socket path limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := ul.File()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestActivatedHandover$") //nolint:gosec // G204: re-executes this test binary
	cmd.ExtraFiles = []*os.File{f}                                       // becomes fd 3
	cmd.Env = append(os.Environ(), "UNIXSOCK_CHILD=1", "LISTEN_FDS=1", "LISTEN_PID=child")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := c.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("child did not answer on the handed-over socket: %q %v", buf, err)
	}
	_ = c.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	_ = f.Close()
	_ = ul.Close()
}
