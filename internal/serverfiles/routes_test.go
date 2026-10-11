package serverfiles_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var filesRule = rbac.Server(rbac.PermServerFiles, "id")

// wantRoutes are exactly the files operations of api/openapi.yaml (V3), all with server.files.
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/files":          filesRule,
	"GET /api/servers/{id}/files/content":  filesRule,
	"PUT /api/servers/{id}/files/content":  filesRule,
	"POST /api/servers/{id}/files/upload":  filesRule,
	"GET /api/servers/{id}/files/download": filesRule,
	"POST /api/servers/{id}/files/mkdir":   filesRule,
	"POST /api/servers/{id}/files/rename":  filesRule,
	"POST /api/servers/{id}/files/delete":  filesRule,
	"POST /api/servers/{id}/files/extract": filesRule,
	"GET /api/servers/{id}/files/jobs":     filesRule,
}

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		serverfiles.NewHandler(serverfiles.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
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
