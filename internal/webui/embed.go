// Package webui serves the embedded, pre-built React frontend.
package webui

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

//go:embed all:dist
var dist embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return NewHandler(sub)
}

// NoncePlaceholder is the value of Vite's html.cspNonce in the production build
// (web/vite.config.ts); serveIndex replaces it with a fresh nonce per response.
const NoncePlaceholder = "MCPANEL_CSP_NONCE"

// NewHandler serves files from fsys with SPA fallback to index.html.
func NewHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no such endpoint")
			return
		}
		if _, err := fs.Stat(fsys, "index.html"); err != nil {
			http.Error(w, "frontend not built - run `make web-build`", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, fsys)
			return
		}
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			if name == "assets" || strings.HasPrefix(name, "assets/") {
				// a missing hashed asset (old chunk after a deploy) must not get index.html
				http.NotFound(w, r)
				return
			}
			serveIndex(w, r, fsys)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// serveIndex sends index.html with a per-request style nonce in the body and the CSP. It is
// never cached (no-store, no validators): a 304 would pair a cached body with a new header nonce.
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	page, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.Error(w, "index.html unreadable", http.StatusInternalServerError)
		return
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails since Go 1.24
	nonce := base64.StdEncoding.EncodeToString(b)
	page = bytes.ReplaceAll(page, []byte(NoncePlaceholder), []byte(nonce))
	h := w.Header()
	h.Set("Content-Security-Policy", httpx.CSP(nonce))
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(page)
}
