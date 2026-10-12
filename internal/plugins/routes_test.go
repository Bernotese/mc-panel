package plugins_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var pluginsRule = rbac.Server(rbac.PermServerPlugins, "id")

// wantRoutes are exactly the plugins operations of api/openapi.yaml (V3), all with server.plugins.
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/plugins":          pluginsRule,
	"GET /api/servers/{id}/plugins/search":   pluginsRule,
	"GET /api/servers/{id}/plugins/versions": pluginsRule,
	"GET /api/servers/{id}/plugins/resolve":  pluginsRule,
	"POST /api/servers/{id}/plugins/install": pluginsRule,
	"GET /api/servers/{id}/plugins/updates":  pluginsRule,
	"POST /api/servers/{id}/plugins/update":  pluginsRule,
	"POST /api/servers/{id}/plugins/toggle":  pluginsRule,
	"POST /api/servers/{id}/plugins/delete":  pluginsRule,
	"GET /api/servers/{id}/plugins/jobs":     pluginsRule,
	"GET /api/servers/{id}/plugins/icon":     pluginsRule,
}

type env struct {
	db  *sql.DB
	mux *chi.Mux
	rt  *rbac.Router
	h   *plugins.Handler
}

// newEnv mounts the handler under /api on a real rbac.Router with the DB-backed checker. No
// Catalog, Upstream or Files: the defaults must build without touching the network.
func newEnv(t *testing.T) env {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	h := plugins.NewHandler(plugins.Deps{
		DB: db, Clock: clk, Audit: &audit.MemoryRecorder{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) { h.Routes(api) })
	mux.Handle("/*", http.NotFoundHandler())
	return env{db: db, mux: mux, rt: rt, h: h}
}

// do sends a request as p (nil = anonymous).
func (e env) do(method, path string, p *principal.Principal) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{}`)
	}
	req := httptest.NewRequest(method, path, body)
	if p != nil {
		req = req.WithContext(principal.With(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	got := e.rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range wantRoutes {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(e.mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Phase A: every route answers 502 plugin_source_unavailable until phase B2 implements it.
func TestStubsAnswerNotImplemented(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "root", "admin")
	admin := principal.Principal{UserID: uid, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		path := strings.Replace(pattern, "{id}", "1", 1)
		rec := e.do(method, path, &admin)
		var body httpx.ErrorBody
		if rec.Code != http.StatusBadGateway || json.Unmarshal(rec.Body.Bytes(), &body) != nil ||
			body.Error != httpx.CodePluginSourceUnavailable || body.Message != "plugins not implemented yet" {
			t.Errorf("%s: %d %s", key, rec.Code, rec.Body)
		}
		if rec := e.do(method, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d, want 401", key, rec.Code)
		}
	}
}

// The rule asks for server.plugins: server.view alone is 403.
func TestRoutesNeedServerPlugins(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	sid := storetest.CreateServer(t, e.db, "survival")
	alice := principal.Principal{UserID: uid, Username: "alice", Role: principal.RoleUser, Stage: principal.StageFull, SessionID: 2}
	grant := func(perm rbac.Permission) {
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, uid, sid, string(perm)); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/servers/%d/plugins", sid)
	grant(rbac.PermServerView)
	if rec := e.do(http.MethodGet, path, &alice); rec.Code != http.StatusForbidden {
		t.Fatalf("server.view only: %d, want 403", rec.Code)
	}
	grant(rbac.PermServerPlugins)
	if rec := e.do(http.MethodGet, path, &alice); rec.Code != http.StatusBadGateway {
		t.Fatalf("server.plugins: %d, want the stub's 502", rec.Code)
	}
}

func TestRunReturnsWhenCancelled(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	select {
	case <-done:
		t.Fatal("Run returned before ctx was cancelled")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
