package agent_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/firewall"
	"mcpanel/internal/clock"
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

// syncBuffer is a bytes.Buffer safe for concurrent writers and readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func testLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newNoop() (*firewall.Noop, *clock.Fake) {
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	return firewall.NewNoop(clk, testLogger(io.Discard)), clk
}

// stubFirewall fails every operation with err.
type stubFirewall struct{ err error }

func (s stubFirewall) Name() string                                           { return "stub" }
func (s stubFirewall) Ban(context.Context, netip.Prefix, time.Duration) error { return s.err }
func (s stubFirewall) Unban(context.Context, netip.Prefix) error              { return s.err }
func (s stubFirewall) List(context.Context) ([]agentapi.Ban, error)           { return nil, s.err }
