package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/webui"
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

// A missing hashed asset (e.g. an old chunk after a deploy) must be a 404, not index.html
// served as JavaScript.
func TestMissingAssetIs404(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})
	for _, p := range []string{"/assets/old-chunk.js", "/assets/", "/assets", "/assets/sub/x.css"} {
		code, body, hdr := get(h, p)
		if code != http.StatusNotFound || strings.Contains(body, "<html>") {
			t.Errorf("%s → %d %q", p, code, body)
		}
		if strings.Contains(hdr.Get("Cache-Control"), "immutable") {
			t.Errorf("%s: a 404 must not be cached as immutable", p)
		}
	}
}

// SEC-31: index.html carries a fresh style nonce per response, in the body and in the CSP, and
// is never cached (a revalidated body would keep an old nonce).
func TestIndexStyleNonce(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{
		"index.html": {Data: []byte(`<meta property="csp-nonce" nonce="` + webui.NoncePlaceholder + `"><link rel="stylesheet" nonce="` + webui.NoncePlaceholder + `">`)},
	})
	nonceRE := regexp.MustCompile(`'nonce-([A-Za-z0-9+/]{22}==)'`)
	seen := map[string]bool{}
	for _, p := range []string{"/", "/index.html", "/servers/1/files"} {
		code, body, hdr := get(h, p)
		m := nonceRE.FindStringSubmatch(hdr.Get("Content-Security-Policy"))
		if code != http.StatusOK || m == nil {
			t.Fatalf("%s → %d csp=%q", p, code, hdr.Get("Content-Security-Policy"))
		}
		if strings.Contains(body, webui.NoncePlaceholder) || strings.Count(body, `nonce="`+m[1]+`"`) != 2 {
			t.Errorf("%s: body %q does not carry nonce %s", p, body, m[1])
		}
		if seen[m[1]] {
			t.Errorf("%s: nonce %s reused", p, m[1])
		}
		seen[m[1]] = true
		if strings.Contains(hdr.Get("Content-Security-Policy"), "unsafe-inline") {
			t.Errorf("%s: csp %q", p, hdr.Get("Content-Security-Policy"))
		}
		if hdr.Get("Cache-Control") != "no-store" || hdr.Get("ETag") != "" || hdr.Get("Last-Modified") != "" {
			t.Errorf("%s: cache headers %v", p, hdr)
		}
		if hdr.Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s: content type %q", p, hdr.Get("Content-Type"))
		}
	}
}

// The Vite dev server (make dev, the e2e mock) sends the same policy as the panel, so the
// e2e violation check covers the production CSP.
func TestViteConfigMatchesCSP(t *testing.T) {
	b, err := os.ReadFile("../../web/vite.config.ts")
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	for _, want := range []string{httpx.CSP("${nonce}"), httpx.CSPReportOnly, "'" + webui.NoncePlaceholder + "'"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("web/vite.config.ts does not contain %q", want)
		}
	}
}
