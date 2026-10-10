//go:build unix

package main

import (
	"context"
	"database/sql"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/clock"
	"mcpanel/internal/config"
)

// serveUnix starts the full app on a unix socket in a short temp dir (macOS: 104-byte limit).
func serveUnix(t *testing.T) (*http.Client, config.Panel, *sql.DB, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "panel.sock")

	cfg := config.DefaultPanel()
	cfg.PublicURL = "https://panel.example.com"
	cfg.Listen = "unix:" + sock
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.GeoIPPath = ""
	cfg.AgentSocket = filepath.Join(dir, "agent.sock")
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := openDB(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)

	l, err := listen(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := newHTTPServer(a.Handler)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	return hc, cfg, db, sock
}

func TestServeOverUnixSocket(t *testing.T) {
	hc, cfg, db, sock := serveUnix(t)

	fi, err := os.Lstat(sock)
	if err != nil || fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o660 {
		t.Fatalf("socket = %v, %v; want socket with mode 0660", fi, err)
	}

	get := func(xff string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://panel/api/health", nil)
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if resp := get("203.0.113.9"); resp.StatusCode != http.StatusOK {
		t.Fatalf("health via proxy = %d", resp.StatusCode)
	}
	// Without X-Forwarded-For the client IP is unknown: fail closed.
	if resp := get(""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("health without XFF = %d, want 403", resp.StatusCode)
	}

	// The client IP recorded for a login attempt is the X-Forwarded-For entry.
	req, _ := http.NewRequest(http.MethodPost, "http://panel/api/auth/login",
		strings.NewReader(`{"username":"nobody","password":"wrong password 123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", cfg.Origin())
	req.Header.Set("X-Forwarded-For", "198.51.100.23")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login = %d, want 401", resp.StatusCode)
	}
	var ip string
	if err := db.QueryRow(`SELECT ip FROM login_attempts ORDER BY id DESC LIMIT 1`).Scan(&ip); err != nil {
		t.Fatal(err)
	}
	if ip != "198.51.100.23" {
		t.Fatalf("recorded ip = %q, want the X-Forwarded-For client", ip)
	}
}

func TestListenTCP(t *testing.T) {
	cfg := config.DevPanel(t.TempDir())
	cfg.Listen = "127.0.0.1:0"
	l, err := listen(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, ok := l.Addr().(*net.TCPAddr); !ok {
		t.Fatalf("listener addr = %T, want TCP", l.Addr())
	}
}
