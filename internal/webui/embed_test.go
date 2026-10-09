package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"mcpanel/internal/webui"
)

func get(h http.Handler, path string) (int, string, http.Header) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b), rec.Header()
}

func TestSPAFallbackAndAssets(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})
	if code, body, _ := get(h, "/"); code != 200 || !strings.Contains(body, "app") {
		t.Fatalf("/ → %d %q", code, body)
	}
	if code, body, _ := get(h, "/security/jails"); code != 200 || !strings.Contains(body, "app") {
		t.Fatalf("SPA route → %d %q", code, body)
	}
	code, _, hdr := get(h, "/assets/app.js")
	if code != 200 || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset → %d cache=%q", code, hdr.Get("Cache-Control"))
	}
	if code, _, _ := get(h, "/api/unknown"); code != 404 {
		t.Fatalf("/api/unknown → %d, want 404", code)
	}
	if code, _, _ := get(h, "/../../etc/passwd"); code != 200 {
		// path is cleaned to /etc/passwd → not found → SPA fallback (never a file outside the FS)
		t.Fatalf("traversal → %d", code)
	}
}

func TestNotBuilt(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{".gitkeep": {}})
	if code, _, _ := get(h, "/"); code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", code)
	}
}
