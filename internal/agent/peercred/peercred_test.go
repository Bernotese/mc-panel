package peercred_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

// socketPath returns a short socket path: Unix socket paths are limited to
// 104 bytes on macOS, and t.TempDir() can exceed that.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

func selfUID(t *testing.T) uint32 {
	t.Helper()
	uid := os.Getuid()
	if uid < 0 {
		t.Skip("no uid on this platform")
	}
	return uint32(uid) //nolint:gosec // G115: non-negative uid fits into uint32
}

func TestPeerUIDIsCurrentUser(t *testing.T) {
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	uid, err := peercred.PeerUID(server)
	if err != nil {
		t.Fatalf("PeerUID: %v", err)
	}
	if want := selfUID(t); uid != want {
		t.Fatalf("PeerUID = %d, want %d", uid, want)
	}
}

func TestPeerUIDRejectsNonUnixConn(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := peercred.PeerUID(a); err == nil {
		t.Fatal("expected error for a non-unix connection")
	}
}

func TestWrapRejectsDisallowedUID(t *testing.T) {
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	me := selfUID(t)
	var calls atomic.Int32
	// Reject the first connection, accept the second. This simulates a
	// foreign UID without needing a second user account.
	allow := func(uid uint32) bool { return calls.Add(1) > 1 && uid == me }
	var logs bytes.Buffer
	wl := peercred.Wrap(l, allow, slog.New(slog.NewTextHandler(&logs, nil)))

	rejected, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Close()
	accepted, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()

	c, err := wl.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer c.Close()
	pc, ok := c.(*peercred.Conn)
	if !ok || pc.UID != me {
		t.Fatalf("Accept returned %T %+v, want *peercred.Conn with UID %d", c, c, me)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("allow called %d times, want 2", n)
	}

	_ = rejected.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := rejected.Read(make([]byte, 1))
	var ne net.Error
	if n != 0 || err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("rejected connection: n=%d err=%v, want it closed by the server", n, err)
	}
	if !strings.Contains(logs.String(), "disallowed uid") {
		t.Fatalf("missing rejection log, got: %s", logs.String())
	}
}

func TestAllowUIDs(t *testing.T) {
	allow := peercred.AllowUIDs(1000, 0)
	if !allow(1000) || !allow(0) || allow(1001) {
		t.Fatal("AllowUIDs(1000, 0) decided wrongly")
	}
	if peercred.AllowUIDs()(0) {
		t.Fatal("AllowUIDs() must reject everyone")
	}
}

func TestConnContext(t *testing.T) {
	ctx := peercred.ConnContext(context.Background(), &peercred.Conn{UID: 42})
	if uid, ok := peercred.FromContext(ctx); !ok || uid != 42 {
		t.Fatalf("FromContext = %d, %v; want 42, true", uid, ok)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, ok := peercred.FromContext(peercred.ConnContext(context.Background(), a)); ok {
		t.Fatal("plain connections must not carry a uid")
	}
}
