package serverfiles_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// wantRoutes are exactly the files operations of api/openapi.yaml with their rule (V3/V4).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/files":          rbac.Server(rbac.PermServerFiles, "id"),
	"GET /api/servers/{id}/files/content":  rbac.Server(rbac.PermServerFiles, "id"),
	"PUT /api/servers/{id}/files/content":  rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/upload":  rbac.Server(rbac.PermServerFiles, "id"),
	"GET /api/servers/{id}/files/download": rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/mkdir":   rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/rename":  rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/delete":  rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/extract": rbac.Server(rbac.PermServerFiles, "id"),
	"GET /api/servers/{id}/files/jobs":     rbac.Server(rbac.PermServerFiles, "id"),
}

func newRouter(t *testing.T) (*chi.Mux, *rbac.Router) {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		serverfiles.NewHandler(serverfiles.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())
	return mux, rt
}

func TestRouteContract(t *testing.T) {
	mux, rt := newRouter(t)
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

func TestUnlimitedBody(t *testing.T) {
	for target, want := range map[string]bool{
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
	} {
		var method, path string
		for i := range target {
			if target[i] == ' ' {
				method, path = target[:i], target[i+1:]
				break
			}
		}
		r := httptest.NewRequest(method, path, nil)
		if got := serverfiles.UnlimitedBody(r); got != want {
			t.Errorf("UnlimitedBody(%s) = %v, want %v", target, got, want)
		}
	}
}

// The stub (Phase A) answers 503 files_unavailable on every route until B2.
func TestStubAnswersFilesUnavailable(t *testing.T) {
	h := serverfiles.NewHandler(serverfiles.Deps{DB: storetest.Open(t), Clock: clock.NewFake(t0), Audit: &audit.MemoryRecorder{}})
	rec := httptest.NewRecorder()
	h.ServeStub(rec, httptest.NewRequest(http.MethodGet, "/api/servers/1/files", nil))
	var body struct{ Error string }
	if rec.Code != http.StatusServiceUnavailable || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error != "files_unavailable" {
		t.Fatalf("stub = %d %s", rec.Code, rec.Body)
	}
	if h.ExtractRunning(t.Context(), 1) {
		t.Fatal("stub ExtractRunning must be false")
	}
}

// TestUnlimitedBodyMatchesTheUploadRoute: the exemption is only meaningful while the upload route
// is registered under exactly the pattern UnlimitedBody recognises.
func TestUnlimitedBodyMatchesTheUploadRoute(t *testing.T) {
	mux, rt := newRouter(t)
	found := false
	for _, r := range rt.Routes() {
		if r.Method == http.MethodPost && r.Pattern == "/api/servers/{id}/files/upload" {
			found = true
		}
	}
	if !found {
		t.Fatal("POST /api/servers/{id}/files/upload is not registered")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/servers/7/files/upload", nil)
	rctx := chi.NewRouteContext()
	if !mux.Match(rctx, req.Method, req.URL.Path) || rctx.RoutePattern() != "/api/servers/{id}/files/upload" {
		t.Fatalf("upload request routes to %q", rctx.RoutePattern())
	}
	if !serverfiles.UnlimitedBody(req) {
		t.Fatal("the upload request is not exempt from the body limit")
	}
}
