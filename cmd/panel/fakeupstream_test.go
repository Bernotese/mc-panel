package main

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// fakeUpstream is a small Modrinth (API and CDN, both TLS) for the end-to-end test; only the
// CDN host is an allowed download host.
type fakeUpstream struct {
	api, cdn *httptest.Server
	mu       sync.Mutex
	projects map[string]map[string]any   // by id and slug
	versions map[string][]map[string]any // by project id, newest first
	files    map[string][]byte           // CDN path -> content
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	f := &fakeUpstream{projects: map[string]map[string]any{}, versions: map[string][]map[string]any{}, files: map[string][]byte{}}
	f.api = httptest.NewTLSServer(http.HandlerFunc(f.serveAPI))
	f.cdn = httptest.NewTLSServer(http.HandlerFunc(f.serveCDN))
	t.Cleanup(f.api.Close)
	t.Cleanup(f.cdn.Close)
	return f
}

func (f *fakeUpstream) client(clk clock.Clock) *upstream.Client {
	host := f.cdn.Listener.Addr().String()
	return upstream.New(upstream.Options{
		Clock: clk, Version: "test", HTTPClient: f.api.Client(),
		Bases:     upstream.Bases{Modrinth: f.api.URL, Hangar: f.api.URL},
		AllowHost: func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host && u.User == nil },
	})
}

// jar puts content on the CDN and returns the Modrinth file object.
func (f *fakeUpstream) jar(name, content string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := "/data/" + name
	f.files[p] = []byte(content)
	sum := sha512.Sum512([]byte(content))
	return map[string]any{
		"url": f.cdn.URL + p, "filename": name, "primary": true, "size": len(content),
		"hashes": map[string]string{"sha512": hex.EncodeToString(sum[:])},
	}
}

// mVersion is a Paper-compatible Modrinth release for 1.21.11; deps are required project ids.
func mVersion(id, number string, file map[string]any, published time.Time, deps ...string) map[string]any {
	ds := []map[string]any{}
	for _, d := range deps {
		ds = append(ds, map[string]any{"project_id": d, "version_id": nil, "file_name": nil, "dependency_type": "required"})
	}
	return map[string]any{
		"id": id, "version_number": number, "version_type": "release", "date_published": published.Format(time.RFC3339),
		"game_versions": []string{"1.21.11"}, "loaders": []string{"paper"}, "files": []any{file}, "dependencies": ds,
	}
}

func (f *fakeUpstream) project(id, slug, title string, versions ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := map[string]any{
		"id": id, "slug": slug, "title": title, "description": title + " description", "server_side": "required",
		"icon_url": f.cdn.URL + "/icons/" + id + ".png",
	}
	f.projects[id], f.projects[slug] = p, p
	for _, v := range versions {
		v["project_id"] = id
	}
	f.versions[id] = versions
}

func fileHash(v map[string]any) string {
	file := v["files"].([]any)[0].(map[string]any)
	return file["hashes"].(map[string]string)["sha512"]
}

func (f *fakeUpstream) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	p := r.URL.Path
	switch {
	case p == "/v2/search":
		hits := []any{}
		for id, pr := range f.projects {
			if id == pr["id"] {
				hits = append(hits, map[string]any{
					"project_id": id, "slug": pr["slug"], "title": pr["title"], "description": pr["description"],
					"author": "someone", "downloads": 1, "icon_url": pr["icon_url"],
				})
			}
		}
		write(map[string]any{"hits": hits, "total_hits": len(hits)})
	case strings.HasPrefix(p, "/v2/project/") && strings.HasSuffix(p, "/version"):
		pr, ok := f.projects[strings.TrimSuffix(strings.TrimPrefix(p, "/v2/project/"), "/version")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(f.versions[pr["id"].(string)])
	case strings.HasPrefix(p, "/v2/project/"):
		pr, ok := f.projects[strings.TrimPrefix(p, "/v2/project/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(pr)
	case strings.HasPrefix(p, "/v2/version/"):
		for _, vs := range f.versions {
			for _, v := range vs {
				if v["id"] == strings.TrimPrefix(p, "/v2/version/") {
					write(v)
					return
				}
			}
		}
		http.NotFound(w, r)
	case p == "/v2/version_files" || p == "/v2/version_files/update":
		var in struct {
			Hashes []string `json:"hashes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		for _, h := range in.Hashes {
			for _, vs := range f.versions {
				for _, v := range vs {
					if fileHash(v) != h {
						continue
					}
					out[h] = v
					if p == "/v2/version_files/update" {
						out[h] = vs[0] // newest of the project
					}
				}
			}
		}
		write(out)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeUpstream) serveCDN(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/icons/") {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...))
		return
	}
	f.mu.Lock()
	b, ok := f.files[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
