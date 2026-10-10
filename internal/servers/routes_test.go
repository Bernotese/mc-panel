package servers_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// wantRoutes are exactly the server operations of api/openapi.yaml with their rules.
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers":             rbac.Authenticated(),
	"POST /api/servers":            rbac.Admin().WithSudo(),
	"GET /api/servers/settings":    rbac.Admin(),
	"PUT /api/servers/settings":    rbac.Admin().WithSudo(),
	"GET /api/servers/{id}":        rbac.Server(rbac.PermServerView, "id"),
	"PATCH /api/servers/{id}":      rbac.Admin().WithSudo(),
	"DELETE /api/servers/{id}":     rbac.Admin().WithSudo(),
	"POST /api/servers/{id}/power": rbac.Server(rbac.PermServerPower, "id"),
}

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		servers.NewHandler(servers.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())

	got := rt.Routes()
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
