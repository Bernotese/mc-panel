// Package webui serves the embedded, pre-built React frontend.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"mcpanel/internal/httpx"
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
			serveIndex(w, r, files)
			return
		}
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			serveIndex(w, r, files)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, files http.Handler) {
	w.Header().Set("Cache-Control", "no-cache")
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	files.ServeHTTP(w, r2)
}
