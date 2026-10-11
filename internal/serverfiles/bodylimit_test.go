package serverfiles_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// unlimitedCases lists the requests exempt from the global 1 MiB body limit.
var unlimitedCases = map[string]bool{
	"POST /api/servers/7/files/upload":        true,
	"POST /api/servers/7/files/upload?path=x": true,
	"PUT /api/servers/7/files/upload":         false,
	"POST /api/servers/x/files/upload":        false,
	"POST /api/servers/7/files/upload/":       false,
	"POST /api/servers/7/files/mkdir":         false,
	"POST /api/servers/7/console":             false,
	// chi routes escaped paths by RawPath; such a request never reaches the upload route.
	"POST /api/servers/7/files%2Fupload": false,
	"POST /api/servers/7/FILES/upload":   false,
	"POST /api/servers/07x/files/upload": false,
}

func TestUnlimitedBody(t *testing.T) {
	for target, want := range unlimitedCases {
		method, path, _ := strings.Cut(target, " ")
		r := httptest.NewRequest(method, path, nil)
		if got := serverfiles.UnlimitedBody(r); got != want {
			t.Errorf("UnlimitedBody(%s) = %v, want %v", target, got, want)
		}
	}
}

// TestUnlimitedBodyMatchesTheRoutes: the exemption is only meaningful while the exempt routes
// are registered under exactly the patterns UnlimitedBody recognises.
func TestUnlimitedBodyMatchesTheRoutes(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		serverfiles.NewHandler(serverfiles.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	for _, tc := range []struct{ method, path, pattern string }{
		{http.MethodPost, "/api/servers/7/files/upload", "/api/servers/{id}/files/upload"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rctx := chi.NewRouteContext()
		if !mux.Match(rctx, req.Method, req.URL.Path) || rctx.RoutePattern() != tc.pattern {
			t.Errorf("%s %s routes to %q, want %q", tc.method, tc.path, rctx.RoutePattern(), tc.pattern)
		}
		if !serverfiles.UnlimitedBody(req) {
			t.Errorf("%s %s is not exempt from the body limit", tc.method, tc.path)
		}
	}
}
