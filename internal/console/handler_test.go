package console_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// wantRoutes are exactly the console operations of api/openapi.yaml with their rules (V3/V4).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/console/stream":   rbac.Server(rbac.PermServerConsole, "id"),
	"POST /api/servers/{id}/console":         rbac.Server(rbac.PermServerConsole, "id"),
	"GET /api/servers/{id}/players":          rbac.Server(rbac.PermServerView, "id"),
	"GET /api/servers/{id}/players/lists":    rbac.Server(rbac.PermServerConsole, "id"),
	"POST /api/servers/{id}/players/actions": rbac.Server(rbac.PermServerConsole, "id"),
}

func newRouter(t *testing.T) (*chi.Mux, []rbac.RouteInfo) {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		console.NewHandler(console.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())
	return mux, rt.Routes()
}

func TestRouteContract(t *testing.T) {
	mux, got := newRouter(t)
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
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Until phase B2 every console route answers 503 agent_unavailable (V4).
func TestStubsAnswerAgentUnavailable(t *testing.T) {
	mux, _ := newRouter(t)
	admin := principal.Principal{UserID: 1, Username: "admin", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/servers/1/console/stream"},
		{http.MethodPost, "/api/servers/1/console"},
		{http.MethodGet, "/api/servers/1/players"},
		{http.MethodGet, "/api/servers/1/players/lists"},
		{http.MethodPost, "/api/servers/1/players/actions"},
	} {
		req := httptest.NewRequestWithContext(principal.With(context.Background(), admin), c.method, c.path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var body httpx.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusServiceUnavailable || body.Error != httpx.CodeAgentUnavailable {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestShutdownWithoutStreams(t *testing.T) {
	console.NewHandler(console.Deps{Clock: clock.NewFake(t0), Audit: &audit.MemoryRecorder{}}).Shutdown()
}
