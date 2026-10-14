package upstream_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestModrinthProjectsBatch(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_projects.json"))
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	got, err := c.ModrinthProjects(bg, []string{"P7dR8mSH", "PtjYWJkn", "P7dR8mSH", "gone0000", "../x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["P7dR8mSH"].Slug != "fabric-api" || got["PtjYWJkn"].ServerSide != "unsupported" || !got["PtjYWJkn"].ClientOnly() {
		t.Fatalf("projects = %+v", got)
	}
	req, _ := api.lastRequest()
	var ids []string
	if err := json.Unmarshal([]byte(req.URL.Query().Get("ids")), &ids); err != nil || !slices.Equal(ids, []string{"P7dR8mSH", "PtjYWJkn", "gone0000"}) {
		t.Fatalf("ids sent = %v (%v)", ids, err)
	}
	// Both are cached now, also for the single lookup.
	if again, err := c.ModrinthProjects(bg, []string{"P7dR8mSH", "PtjYWJkn"}); err != nil || len(again) != 2 {
		t.Fatalf("cached = %+v, %v", again, err)
	}
	if p, err := c.Project(bg, upstream.Modrinth, "P7dR8mSH"); err != nil || p.Title != "Fabric API" {
		t.Fatalf("project = %+v, %v", p, err)
	}
	if n := api.count("GET /v2/projects"); n != 1 || api.total() != 1 {
		t.Fatalf("requests: projects %d, total %d", n, api.total())
	}
	clk.Advance(time.Hour)
	if _, err := c.ModrinthProjects(bg, []string{"P7dR8mSH"}); err != nil || api.count("GET /v2/projects") != 2 {
		t.Fatalf("expired cache not refetched: %v", err)
	}
}

func TestModrinthProjectsChunks(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`[]`)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	ids := make([]string, 0, 250)
	for i := range 250 {
		ids = append(ids, hexHash(i)[120:])
	}
	if _, err := c.ModrinthProjects(bg, ids); err != nil {
		t.Fatal(err)
	}
	if n := api.count("GET /v2/projects"); n != 3 {
		t.Fatalf("requests = %d, want 3 (100 per batch)", n)
	}
	if _, err := c.ModrinthProjects(bg, nil); err != nil || api.count("GET /v2/projects") != 3 {
		t.Fatalf("no ids must not call the upstream: %v", err)
	}
}

func TestModrinthProjectsDown(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	if _, err := c.ModrinthProjects(bg, []string{"P7dR8mSH"}); err == nil {
		t.Fatal("no error")
	}
}

func TestVersionFileBySHA512(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"` + luckperms553 + `":{"id":"v1","project_id":"p1","version_number":"1","version_type":"release",
			"files":[{"url":"https://cdn.example/a.jar","filename":"a.jar","primary":true,"size":1,"hashes":{"sha512":"` + hexHash(1) + `"}},
			{"url":"https://cdn.example/b.jar","filename":"b-dev.jar","primary":false,"size":2,"hashes":{"sha512":"` + luckperms553 + `"}},
			{"url":"https://cdn.example/c.zip","filename":"c.zip","primary":false,"size":3,"hashes":{"sha512":"` + hexHash(3) + `"}}]}}`))
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	got, err := c.VersionsByHash(bg, []string{luckperms553})
	if err != nil {
		t.Fatal(err)
	}
	v := got[luckperms553]
	if v.File == nil || v.File.Name != "a.jar" || len(v.Files) != 2 {
		t.Fatalf("version = %+v", v)
	}
	f, ok := v.FileBySHA512(luckperms553)
	if !ok || f.Name != "b-dev.jar" || f.Size != 2 {
		t.Fatalf("file = %+v, %v", f, ok)
	}
	if _, ok := v.FileBySHA512(hexHash(3)); ok {
		t.Fatal("a zip counted as jar")
	}
}
