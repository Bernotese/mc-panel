package plugins_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var pluginsRule = rbac.Server(rbac.PermServerPlugins, "id")

// wantRoutes are exactly the plugins operations of api/openapi.yaml (V3).
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

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		plugins.NewHandler(plugins.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())
	got := rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

func TestRunReturnsWhenCancelled(t *testing.T) {
	h := plugins.NewHandler(plugins.Deps{DB: storetest.Open(t), Clock: clock.NewFake(t0), Audit: &audit.MemoryRecorder{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
