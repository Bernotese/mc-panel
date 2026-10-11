package serverfiles_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
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
	// the editor's save: the handler limits it to filesapi.MaxJSONBytes
	"PUT /api/servers/7/files/content":   true,
	"POST /api/servers/7/files/content":  false,
	"GET /api/servers/7/files/content":   false,
	"PUT /api/servers/x/files/content":   false,
	"PUT /api/servers/7/files%2Fcontent": false,
	"PUT /api/servers/7/files/content/":  false,
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
		{http.MethodPut, "/api/servers/7/files/content", "/api/servers/{id}/files/content"},
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

// Review Focus #2: through the real middleware only the upload stream and the editor's save
// escape the 1 MiB limit of every other route.
func TestGlobalBodyLimit(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		fmt.Fprintf(w, "%d", n)
	})
	h := httpx.BodyLimitExcept(1<<20, serverfiles.UnlimitedBody)(read)
	for _, tc := range []struct {
		method, path string
		size         int
		want         int
	}{
		{http.MethodPost, "/api/servers/7/files/upload", 8 << 20, http.StatusOK},
		{http.MethodPut, "/api/servers/7/files/content", 5 << 20, http.StatusOK},
		{http.MethodPost, "/api/servers/7/files/mkdir", 2 << 20, http.StatusRequestEntityTooLarge},
		{http.MethodPost, "/api/servers", 2 << 20, http.StatusRequestEntityTooLarge},
		{http.MethodPost, "/api/servers/7/files/content", 2 << 20, http.StatusRequestEntityTooLarge},
		{http.MethodPost, "/api/servers/7/files/mkdir", 1 << 20, http.StatusOK},
	} {
		req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(make([]byte, tc.size)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s (%d bytes) = %d, want %d", tc.method, tc.path, tc.size, rec.Code, tc.want)
		}
	}
}

// The editor's save keeps its own ceiling (filesapi.MaxJSONBytes) and a 2 MiB config passes.
func TestWriteBodyLimit(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	ok := e.do(http.MethodPut, srvPath(id, "/files/content"), map[string]any{"path": "big.yml", "content": strings.Repeat("a", 2<<20), "expected_mtime": nil}, &admin)
	if ok.Code != http.StatusOK {
		t.Fatalf("2 MiB save: %d %s", ok.Code, ok.Body)
	}
	tooBig := `{"path":"x","expected_mtime":null,"content":"` + strings.Repeat("a", filesapi.MaxJSONBytes) + `"}`
	rec := e.doRaw(http.MethodPut, srvPath(id, "/files/content"), strings.NewReader(tooBig), &admin)
	if rec.Code != http.StatusRequestEntityTooLarge || errCode(t, rec) != httpx.CodePayloadTooLarge {
		t.Fatalf("over MaxJSONBytes: %d", rec.Code)
	}
	if calls := e.files.takeCalls(); len(calls) != 1 {
		t.Fatalf("calls = %v (only the 2 MiB save may reach the service)", calls)
	}
}
