package plugins_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// upFile is a jar the fake CDN serves.
type upFile struct {
	URL, Name      string
	Size           int64
	SHA512, SHA256 string
	content        string
}

// mv describes a Modrinth version of the fake. Zero values: Type release, published t0 minus
// the version's position in days (the first version is the newest), game versions {1.21.11},
// loaders {paper, purpur, fabric}.
type mv struct {
	ID, Number, Type      string
	Published             time.Time
	GameVersions, Loaders []string
	File                  *upFile
	Deps                  []map[string]any
}

// hv describes a Hangar version of the fake. Zero values: channel Release, published as for mv,
// game versions {1.21.11}.
type hv struct {
	Name, Channel string
	Published     time.Time
	GameVersions  []string
	File          *upFile
	External      string
	Deps          []map[string]any
}

// mdep is a required Modrinth dependency ("" = null).
func mdep(projectID, versionID string) map[string]any {
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	return map[string]any{"project_id": null(projectID), "version_id": null(versionID), "file_name": nil, "dependency_type": "required"}
}

// hdep is a required Hangar dependency: projectID 0 = null (then external is the link).
func hdep(name string, projectID int64, external string) map[string]any {
	d := map[string]any{"name": name, "required": true, "platform": "PAPER", "projectId": nil, "externalUrl": nil}
	if projectID > 0 {
		d["projectId"] = projectID
	}
	if external != "" {
		d["externalUrl"] = external
	}
	return d
}

type mRec struct {
	v    mv
	body map[string]any
}

type mProj struct {
	ID, Slug, Title, ServerSide string
	recs                        []mRec
}

type hRec struct {
	v    hv
	body map[string]any
}

type hProj struct {
	ID          int64
	Owner, Slug string
	recs        []hRec
}

// fakeUp is a fake Modrinth + Hangar API (one TLS server, both bases) and a fake CDN (a second
// TLS server, the only allowed download host).
type fakeUp struct {
	api, cdn *httptest.Server

	mu          sync.Mutex
	mOrder      []string
	mProjects   map[string]*mProj // by id and slug
	hOrder      []int64
	hProjects   map[string]*hProj // by id (text) and slug
	cdnFiles    map[string][]byte // CDN path -> body
	n           int
	apiDown     bool
	cdnStatus   int
	block       chan struct{}
	hits        map[string]int
	lastQuery   url.Values
	updateTypes [][]string
	cdnActive   int
	cdnPeak     int
}

func newFakeUp(t *testing.T) *fakeUp {
	f := &fakeUp{mProjects: map[string]*mProj{}, hProjects: map[string]*hProj{}, cdnFiles: map[string][]byte{}, hits: map[string]int{}}
	f.api = httptest.NewTLSServer(http.HandlerFunc(f.serveAPI))
	f.cdn = httptest.NewTLSServer(http.HandlerFunc(f.serveCDN))
	t.Cleanup(f.api.Close)
	t.Cleanup(f.cdn.Close)
	return f
}

func (f *fakeUp) cdnHost() string { return f.cdn.Listener.Addr().String() }

// client returns an upstream client against the fake; only the fake CDN is an allowed host.
func (f *fakeUp) client(clk clock.Clock) *upstream.Client {
	host := f.cdnHost()
	return upstream.New(upstream.Options{
		Clock: clk, Version: "test", HTTPClient: f.api.Client(),
		Bases:     upstream.Bases{Modrinth: f.api.URL, Hangar: f.api.URL},
		AllowHost: func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host && u.User == nil },
	})
}

// jar registers a jar on the fake CDN.
func (f *fakeUp) jar(name, content string) *upFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	p := fmt.Sprintf("/f/%d/%s", f.n, name)
	f.cdnFiles[p] = []byte(content)
	return &upFile{URL: f.cdn.URL + p, Name: name, Size: int64(len(content)), SHA512: sha512Hex(content), SHA256: sha256Hex(content), content: content}
}

// serveInstead makes the CDN answer body for file (corrupted or wrong-sized downloads).
func (f *fakeUp) serveInstead(file *upFile, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cdnFiles[strings.TrimPrefix(file.URL, f.cdn.URL)] = []byte(body)
}

func (f *fakeUp) iconURL(id string) string { return f.cdn.URL + "/icons/" + id + ".png" }

func published(p time.Time, i int) time.Time {
	if p.IsZero() {
		return t0.Add(-time.Duration(i) * 24 * time.Hour)
	}
	return p
}

// modrinth registers a Modrinth project with its versions (newest first).
func (f *fakeUp) modrinth(id, slug, title, serverSide string, vs ...mv) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &mProj{ID: id, Slug: slug, Title: title, ServerSide: serverSide}
	for i, v := range vs {
		if v.GameVersions == nil {
			v.GameVersions = []string{"1.21.11"}
		}
		if v.Loaders == nil {
			v.Loaders = []string{"paper", "purpur", "fabric"}
		}
		v.Type = cmp.Or(v.Type, "release")
		v.Published = published(v.Published, i)
		files := []map[string]any{}
		if v.File != nil {
			files = append(files, map[string]any{
				"url": v.File.URL, "filename": v.File.Name, "primary": true, "size": v.File.Size,
				"hashes": map[string]string{"sha512": v.File.SHA512, "sha1": "unused"},
			})
		}
		deps := v.Deps
		if deps == nil {
			deps = []map[string]any{}
		}
		p.recs = append(p.recs, mRec{v: v, body: map[string]any{
			"id": v.ID, "project_id": id, "version_number": cmp.Or(v.Number, v.ID), "name": "Version " + v.ID,
			"version_type": v.Type, "date_published": v.Published.Format(time.RFC3339), "game_versions": v.GameVersions,
			"loaders": v.Loaders, "files": files, "dependencies": deps,
		}})
	}
	f.mOrder = append(f.mOrder, id)
	f.mProjects[id], f.mProjects[slug] = p, p
}

// hangar registers a Hangar project with its versions (newest first).
func (f *fakeUp) hangar(id int64, owner, slug string, vs ...hv) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &hProj{ID: id, Owner: owner, Slug: slug}
	for i, v := range vs {
		if v.GameVersions == nil {
			v.GameVersions = []string{"1.21.11"}
		}
		v.Channel = cmp.Or(v.Channel, "Release")
		v.Published = published(v.Published, i)
		dl := map[string]any{"fileInfo": nil, "externalUrl": nil, "downloadUrl": nil}
		if v.File != nil {
			dl = map[string]any{
				"fileInfo":    map[string]any{"name": v.File.Name, "sizeBytes": v.File.Size, "sha256Hash": v.File.SHA256},
				"externalUrl": nil, "downloadUrl": v.File.URL,
			}
		} else if v.External != "" {
			dl["externalUrl"] = v.External
		}
		deps := v.Deps
		if deps == nil {
			deps = []map[string]any{}
		}
		p.recs = append(p.recs, hRec{v: v, body: map[string]any{
			"projectId": id, "name": v.Name, "createdAt": v.Published.Format(time.RFC3339),
			"channel": map[string]any{"name": v.Channel}, "downloads": map[string]any{"PAPER": dl},
			"pluginDependencies": map[string]any{"PAPER": deps}, "platformDependencies": map[string]any{"PAPER": v.GameVersions},
		}})
	}
	f.hOrder = append(f.hOrder, id)
	f.hProjects[strconv.FormatInt(id, 10)], f.hProjects[slug] = p, p
}

func (f *fakeUp) setDown(v bool)            { f.mu.Lock(); f.apiDown = v; f.mu.Unlock() }
func (f *fakeUp) setCDNStatus(code int)     { f.mu.Lock(); f.cdnStatus = code; f.mu.Unlock() }
func (f *fakeUp) setBlock(ch chan struct{}) { f.mu.Lock(); f.block = ch; f.mu.Unlock() }

func (f *fakeUp) count(key string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.hits[key] }

// apiTotal counts all API requests so far (CDN requests excluded).
func (f *fakeUp) apiTotal() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for k, v := range f.hits {
		if !strings.HasPrefix(k, "CDN ") {
			n += v
		}
	}
	return n
}

func (f *fakeUp) query() url.Values { f.mu.Lock(); defer f.mu.Unlock(); return f.lastQuery }

func (f *fakeUp) types() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.updateTypes)
}

func (f *fakeUp) peak() int { f.mu.Lock(); defer f.mu.Unlock(); return f.cdnPeak }

func overlaps(a, b []string) bool {
	return slices.ContainsFunc(a, func(s string) bool { return slices.Contains(b, s) })
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// byHash finds the Modrinth version whose jar has sha512 h (f.mu held).
func (f *fakeUp) byHash(h string) (*mProj, mRec, bool) {
	for _, id := range f.mOrder {
		p := f.mProjects[id]
		for _, r := range p.recs {
			if r.v.File != nil && r.v.File.SHA512 == h {
				return p, r, true
			}
		}
	}
	return nil, mRec{}, false
}

func (f *fakeUp) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[r.Method+" "+r.URL.Path]++
	if f.apiDown {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	p, q := r.URL.Path, r.URL.Query()
	switch {
	case r.Method == http.MethodGet && p == "/v2/search":
		f.lastQuery = q
		hits := []map[string]any{}
		for _, id := range f.mOrder {
			pr := f.mProjects[id]
			hits = append(hits, map[string]any{
				"project_id": pr.ID, "slug": pr.Slug, "title": pr.Title, "description": pr.Title + " description",
				"author": "author-" + pr.Slug, "downloads": 100, "icon_url": f.iconURL(pr.ID),
			})
		}
		writeJSON(w, map[string]any{"hits": hits, "total_hits": len(hits), "offset": 0, "limit": 20})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/project/") && strings.HasSuffix(p, "/version"):
		pr, ok := f.mProjects[strings.TrimSuffix(strings.TrimPrefix(p, "/v2/project/"), "/version")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var loaders, gvs []string
		_ = json.Unmarshal([]byte(q.Get("loaders")), &loaders)
		_ = json.Unmarshal([]byte(q.Get("game_versions")), &gvs)
		out := []map[string]any{}
		for _, rec := range pr.recs {
			if (len(loaders) == 0 || overlaps(rec.v.Loaders, loaders)) && (len(gvs) == 0 || overlaps(rec.v.GameVersions, gvs)) {
				out = append(out, rec.body)
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodGet && p == "/v2/projects":
		var ids []string
		_ = json.Unmarshal([]byte(q.Get("ids")), &ids)
		out := []map[string]any{}
		for _, id := range ids {
			if pr, ok := f.mProjects[id]; ok && pr.ID == id {
				out = append(out, f.mProjectJSON(pr))
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/project/"):
		pr, ok := f.mProjects[strings.TrimPrefix(p, "/v2/project/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, f.mProjectJSON(pr))
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/version/"):
		id := strings.TrimPrefix(p, "/v2/version/")
		for _, pid := range f.mOrder {
			for _, rec := range f.mProjects[pid].recs {
				if rec.v.ID == id {
					writeJSON(w, rec.body)
					return
				}
			}
		}
		http.NotFound(w, r)
	case r.Method == http.MethodPost && p == "/v2/version_files":
		var in struct {
			Hashes []string `json:"hashes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		for _, h := range in.Hashes {
			if _, rec, ok := f.byHash(h); ok {
				out[h] = rec.body
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodPost && p == "/v2/version_files/update":
		var in struct {
			Hashes       []string `json:"hashes"`
			Loaders      []string `json:"loaders"`
			GameVersions []string `json:"game_versions"`
			VersionTypes []string `json:"version_types"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.updateTypes = append(f.updateTypes, in.VersionTypes)
		out := map[string]any{}
		for _, h := range in.Hashes {
			pr, _, ok := f.byHash(h)
			if !ok {
				continue
			}
			for _, c := range pr.recs {
				if c.v.File != nil && overlaps(c.v.Loaders, in.Loaders) && overlaps(c.v.GameVersions, in.GameVersions) && slices.Contains(in.VersionTypes, c.v.Type) {
					out[h] = c.body
					break
				}
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodGet && p == "/api/v1/projects":
		f.lastQuery = q
		res := []map[string]any{}
		for _, id := range f.hOrder {
			res = append(res, f.hProjectJSON(f.hProjects[strconv.FormatInt(id, 10)]))
		}
		writeJSON(w, map[string]any{"pagination": map[string]any{"count": len(res), "limit": 20, "offset": 0}, "result": res})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/api/v1/projects/"):
		parts := strings.Split(strings.TrimPrefix(p, "/api/v1/projects/"), "/")
		pr, ok := f.hProjects[parts[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1:
			writeJSON(w, f.hProjectJSON(pr))
		case len(parts) == 2 && parts[1] == "versions":
			out := []map[string]any{}
			for _, rec := range pr.recs {
				gv := q.Get("platformVersion")
				ch := q.Get("channel")
				if (gv == "" || slices.Contains(rec.v.GameVersions, gv)) && (ch == "" || strings.EqualFold(ch, rec.v.Channel)) && len(out) < 25 {
					out = append(out, rec.body)
				}
			}
			writeJSON(w, map[string]any{"pagination": map[string]any{"count": len(out)}, "result": out})
		case len(parts) == 3 && parts[1] == "versions":
			for _, rec := range pr.recs {
				if rec.v.Name == parts[2] {
					writeJSON(w, rec.body)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeUp) mProjectJSON(pr *mProj) map[string]any {
	return map[string]any{
		"id": pr.ID, "slug": pr.Slug, "title": pr.Title, "description": pr.Title + " description",
		"icon_url": f.iconURL(pr.ID), "server_side": pr.ServerSide, "client_side": "optional", "project_type": "mod",
	}
}

func (f *fakeUp) hProjectJSON(p *hProj) map[string]any {
	id := strconv.FormatInt(p.ID, 10)
	return map[string]any{
		"id": p.ID, "name": p.Slug, "namespace": map[string]any{"owner": p.Owner, "slug": p.Slug},
		"stats": map[string]any{"downloads": 50}, "description": p.Slug + " description", "avatarUrl": f.iconURL("h" + id),
	}
}

// iconPNG is a PNG signature plus padding: enough for the magic-byte check.
var iconPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 24)...)

func (f *fakeUp) serveCDN(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits["CDN "+r.URL.Path]++
	f.cdnActive++
	f.cdnPeak = max(f.cdnPeak, f.cdnActive)
	status, block := f.cdnStatus, f.block
	body, ok := f.cdnFiles[r.URL.Path]
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.cdnActive--; f.mu.Unlock() }()
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/icons/") {
		switch {
		case strings.HasSuffix(r.URL.Path, ".svg"):
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
		case strings.Contains(r.URL.Path, "big"):
			_, _ = w.Write(append(bytes.Clone(iconPNG), make([]byte, upstream.MaxIconBytes)...))
		default:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(iconPNG)
		}
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
