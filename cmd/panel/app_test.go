package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/rbac"
)

func newTestApp(t *testing.T) (*app, config.Panel) {
	t.Helper()
	cfg, err := config.LoadPanel("", true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.GeoIPPath = filepath.Join(t.TempDir(), "missing.mmdb")
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
	return a, cfg
}

func do(a *app, method, target, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:12345"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want JSON (body %q)", ct, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	return body.Error
}

func TestAPIUnknownPathIsJSON404(t *testing.T) {
	a, _ := newTestApp(t)
	rec := do(a, http.MethodGet, "/api/does-not-exist", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := errCode(t, rec); got != "not_found" {
		t.Fatalf("error = %q, want not_found", got)
	}
}

func TestAPIWrongMethodIsJSON405(t *testing.T) {
	a, cfg := newTestApp(t)
	rec := do(a, http.MethodPost, "/api/health", cfg.Origin())
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := errCode(t, rec); got != "bad_request" {
		t.Fatalf("error = %q, want bad_request", got)
	}
}

func TestAPIHealthAndSecurityHeaders(t *testing.T) {
	a, _ := newTestApp(t)
	rec := do(a, http.MethodGet, "/api/health", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") == "" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("missing request id / security headers: %v", rec.Header())
	}
}

func TestNonAPIPathsGoToWebUI(t *testing.T) {
	a, _ := newTestApp(t)
	rec := do(a, http.MethodGet, "/some/spa/route", "")
	if rec.Code == http.StatusNotFound || strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("non-API path must be served by the SPA handler, got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestEveryRouteDeclaresARule(t *testing.T) {
	a, _ := newTestApp(t)
	if len(a.Routes) == 0 {
		t.Fatal("no routes registered")
	}
	unc := rbac.Uncovered(a.Mux, a.Routes, func(_, pattern string) bool {
		return pattern == "/*" || pattern == "/api" || pattern == "/api/" || pattern == "/api/*"
	})
	if len(unc) > 0 {
		t.Fatalf("routes without an rbac rule: %v", unc)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	a, _ := newTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background loops did not stop after cancel")
	}
}
