package upstream_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

var paperLoaders = []string{"paper", "spigot", "bukkit"}

const (
	luckperms553 = "a0e087adfc1c7b9fab8fdb5a430a3331a2ca30bfc72818bb7e65ce9baff051a1490834be8cbb9cbeda292f2e92c44cf03fb829ebeb233ade9d666ed908e49ad5"
	unknownHash  = "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
)

func TestModrinthSearchPlugin(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_search_plugin.json"))
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	q := upstream.SearchQuery{Query: "luckperms", Offset: 20, ProjectType: "plugin", Loaders: paperLoaders, GameVersion: "1.21.11"}
	res, err := c.Search(bg, upstream.Modrinth, q)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	if req.URL.Path != "/v2/search" {
		t.Fatalf("path = %s", req.URL.Path)
	}
	got := req.URL.Query()
	wantFacets := `[["project_type:plugin"],["categories:paper","categories:spigot","categories:bukkit"],["versions:1.21.11"]]`
	if got.Get("facets") != wantFacets || got.Get("index") != "relevance" || got.Get("offset") != "20" ||
		got.Get("limit") != "20" || got.Get("query") != "luckperms" {
		t.Fatalf("query = %v", got)
	}
	if res.Total != 99 || len(res.Hits) != 2 {
		t.Fatalf("result = %+v", res)
	}
	want := upstream.SearchHit{
		ProjectID: "Vebnzrzj", Slug: "luckperms", Title: "LuckPerms", Description: "A permissions plugin/mod for Minecraft servers.",
		Author: "lucko", Downloads: 2898442,
		IconURL: "https://cdn.modrinth.com/data/Vebnzrzj/90943902cc650e95b167265b8f2d7c893f82c8f4_96.webp",
	}
	if res.Hits[0] != want {
		t.Fatalf("hit = %+v", res.Hits[0])
	}
	// cached for 5 minutes
	_, _ = c.Search(bg, upstream.Modrinth, q)
	clk.Advance(4 * time.Minute)
	_, _ = c.Search(bg, upstream.Modrinth, q)
	if n := api.count("GET /v2/search"); n != 1 {
		t.Fatalf("searched %d times within 5 min", n)
	}
	clk.Advance(time.Minute)
	_, _ = c.Search(bg, upstream.Modrinth, q)
	if n := api.count("GET /v2/search"); n != 2 {
		t.Fatalf("searched %d times after 5 min", n)
	}
}

func TestModrinthSearchMod(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_search_mod.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	res, err := c.Search(bg, upstream.Modrinth, upstream.SearchQuery{ProjectType: "mod", Loaders: []string{"fabric"}, GameVersion: "1.21.11", ServerSideOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	want := `[["project_type:mod"],["categories:fabric"],["versions:1.21.11"],["server_side:required","server_side:optional"]]`
	if q.Get("facets") != want || q.Get("index") != "downloads" || q.Get("offset") != "0" {
		t.Fatalf("query = %v", q)
	}
	if res.Total != 5 || res.Hits[0].Slug != "chunky" || res.Hits[1].Author != "tollg92" {
		t.Fatalf("result = %+v", res)
	}
}

func TestModrinthProject(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/project/luckperms":
			_, _ = w.Write(testdata(t, "modrinth_project_luckperms.json"))
		case "/v2/project/sodium-extra":
			_, _ = w.Write(testdata(t, "modrinth_project_sodium_extra.json"))
		default:
			http.NotFound(w, r)
		}
	})
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	p, err := c.Project(bg, upstream.Modrinth, "luckperms")
	if err != nil {
		t.Fatal(err)
	}
	want := upstream.Project{
		Source: upstream.Modrinth, ID: "Vebnzrzj", Slug: "luckperms", Title: "LuckPerms",
		Description: "A permissions plugin/mod for Minecraft servers.", ServerSide: "required",
		IconURL: "https://cdn.modrinth.com/data/Vebnzrzj/90943902cc650e95b167265b8f2d7c893f82c8f4_96.webp",
	}
	if p != want || p.ClientOnly() {
		t.Fatalf("project = %+v", p)
	}
	s, err := c.Project(bg, upstream.Modrinth, "sodium-extra")
	if err != nil || !s.ClientOnly() {
		t.Fatalf("sodium-extra = %+v, %v", s, err)
	}
	// 15 minutes cache
	clk.Advance(14 * time.Minute)
	_, _ = c.Project(bg, upstream.Modrinth, "luckperms")
	if n := api.count("GET /v2/project/luckperms"); n != 1 {
		t.Fatalf("fetched %d times", n)
	}
}

func TestModrinthVersions(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_versions_luckperms.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Modrinth, "Vebnzrzj", upstream.VersionFilter{Loaders: paperLoaders, GameVersion: "1.21.11"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	if req.URL.Path != "/v2/project/Vebnzrzj/version" || q.Get("loaders") != `["paper","spigot","bukkit"]` || q.Get("game_versions") != `["1.21.11"]` {
		t.Fatalf("request = %s %v", req.URL.Path, q)
	}
	if len(vs) != 3 {
		t.Fatalf("versions = %d", len(vs))
	}
	v := vs[0]
	wantFile := upstream.File{
		URL:  "https://cdn.modrinth.com/data/Vebnzrzj/versions/b0mk8uS6/LuckPerms-Bukkit-5.5.71.jar",
		Name: "LuckPerms-Bukkit-5.5.71.jar", Size: 1501521,
		SHA512: "188a91f0a543d23bfda32385fca6db63d61e49c8a422bd452a260bd9cbc6a7d7fe45071199e9fca8f3ce43c2b41ee84fd315bd15464577028ff3951a7d4fab27",
	}
	if v.Source != upstream.Modrinth || v.ID != "b0mk8uS6" || v.ProjectID != "Vebnzrzj" || v.Name != "v5.5.71-bukkit" ||
		v.Channel != upstream.Release || !v.Downloadable() || *v.File != wantFile || len(v.Dependencies) != 0 ||
		!v.PublishedAt.Equal(time.Date(2026, 8, 6, 19, 40, 10, 637955000, time.UTC)) ||
		!slices.Contains(v.GameVersions, "1.21.11") || !slices.Contains(v.Loaders, "paper") {
		t.Fatalf("version = %+v", v)
	}
	for i := 1; i < len(vs); i++ {
		if vs[i].PublishedAt.After(vs[i-1].PublishedAt) {
			t.Fatal("not newest first")
		}
	}
	// ReleaseOnly filters the cached list locally (no second request)
	if rs, _ := c.Versions(bg, upstream.Modrinth, "Vebnzrzj", upstream.VersionFilter{Loaders: paperLoaders, GameVersion: "1.21.11", ReleaseOnly: true}); len(rs) != 3 {
		t.Fatalf("release only = %d", len(rs))
	}
	if n := api.total(); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

// mVersionJSON is a minimal Modrinth version answer for the channel and dependency tests.
func mVersionJSON(id, typ, published string, files, deps string) string {
	return `{"id":"` + id + `","project_id":"P","version_number":"` + id + `","version_type":"` + typ +
		`","date_published":"` + published + `","game_versions":["1.21.11"],"loaders":["fabric"],"files":` + files +
		`,"dependencies":` + deps + `}`
}

const oneJar = `[{"url":"https://cdn.modrinth.com/data/P/a.jar","filename":"a.jar","primary":true,"size":3,"hashes":{"sha512":"ABC"}}]`

func TestModrinthChannelsAndOrder(t *testing.T) {
	body := "[" + strings.Join([]string{
		mVersionJSON("old", "release", "2026-01-01T00:00:00Z", oneJar, "[]"),
		mVersionJSON("newbeta", "beta", "2026-03-01T00:00:00Z", oneJar, "[]"),
		mVersionJSON("alpha", "alpha", "2026-02-01T00:00:00Z", oneJar, "[]"),
		mVersionJSON("weird", "nightly", "2026-02-15T00:00:00Z", oneJar, "[]"),
	}, ",") + "]"
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Modrinth, "P", upstream.VersionFilter{Loaders: []string{"fabric"}, GameVersion: "1.21.11"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.ID+":"+string(v.Channel))
	}
	if want := []string{"newbeta:beta", "weird:alpha", "alpha:alpha", "old:release"}; !slices.Equal(got, want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	if vs[0].File.SHA512 != "abc" {
		t.Fatalf("hash not lower-cased: %q", vs[0].File.SHA512)
	}
	rs, _ := c.Versions(bg, upstream.Modrinth, "P", upstream.VersionFilter{Loaders: []string{"fabric"}, GameVersion: "1.21.11", ReleaseOnly: true})
	if len(rs) != 1 || rs[0].ID != "old" {
		t.Fatalf("release only = %+v", rs)
	}
}

func TestModrinthDependencies(t *testing.T) {
	deps := `[{"project_id":"REQ","version_id":null,"file_name":null,"dependency_type":"required"},
	{"project_id":null,"version_id":"VONLY","file_name":null,"dependency_type":"required"},
	{"project_id":null,"version_id":null,"file_name":"lib.jar","dependency_type":"required"},
	{"project_id":"OPT","version_id":null,"file_name":null,"dependency_type":"optional"},
	{"project_id":"BAD","version_id":null,"file_name":null,"dependency_type":"incompatible"},
	{"project_id":"EMB","version_id":null,"file_name":null,"dependency_type":"embedded"}]`
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mVersionJSON("v1", "release", "2026-01-01T00:00:00Z", oneJar, deps)))
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	v, err := c.Version(bg, upstream.Modrinth, "", "v1")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	if req.URL.Path != "/v2/version/v1" {
		t.Fatalf("path = %s", req.URL.Path)
	}
	want := []upstream.Dependency{{ProjectID: "REQ"}, {VersionID: "VONLY"}, {Name: "lib.jar"}}
	if !slices.Equal(v.Dependencies, want) {
		t.Fatalf("dependencies = %+v", v.Dependencies)
	}
}

func TestModrinthRealDependency(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_versions_chunky_fabric.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Modrinth, "fALzjamp", upstream.VersionFilter{Loaders: []string{"fabric"}, GameVersion: "1.21.11"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	if !slices.Equal(vs[0].Dependencies, []upstream.Dependency{{ProjectID: "P7dR8mSH"}}) || vs[0].File.Name != "Chunky-Fabric-1.4.55.jar" {
		t.Fatalf("version = %+v", vs[0])
	}
}

// Review Focus #1: null fields and files without a jar never break the decoder.
func TestModrinthNullFields(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/version/nulls":
			_, _ = w.Write([]byte(`{"id":"nulls","project_id":"P","version_number":null,"version_type":null,"date_published":null,
				"game_versions":null,"loaders":null,"files":null,"dependencies":null}`))
		case "/v2/version/zip":
			_, _ = w.Write([]byte(mVersionJSON("zip", "release", "2026-01-01T00:00:00Z",
				`[{"url":"https://cdn.modrinth.com/a.zip","filename":"a.zip","primary":true,"size":1,"hashes":{}},
				  {"url":"https://cdn.modrinth.com/b.JAR","filename":"b.JAR","primary":false,"size":2,"hashes":{"sha512":"x"}}]`, "[]")))
		case "/v2/version/nojar":
			_, _ = w.Write([]byte(mVersionJSON("nojar", "release", "2026-01-01T00:00:00Z",
				`[{"url":"https://cdn.modrinth.com/a.zip","filename":"a.zip","primary":true,"size":1,"hashes":{}}]`, "[]")))
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	v, err := c.Version(bg, upstream.Modrinth, "", "nulls")
	if err != nil || v.Downloadable() || v.Name != "nulls" || v.Channel != upstream.Alpha || len(v.Dependencies) != 0 {
		t.Fatalf("nulls = %+v, %v", v, err)
	}
	if v, _ := c.Version(bg, upstream.Modrinth, "", "zip"); v.File == nil || v.File.Name != "b.JAR" {
		t.Fatalf("zip = %+v", v.File)
	}
	if v, _ := c.Version(bg, upstream.Modrinth, "", "nojar"); v.Downloadable() {
		t.Fatalf("nojar downloadable: %+v", v.File)
	}
}

func TestModrinthInvalidRefs(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	if _, err := c.Project(bg, upstream.Modrinth, "../x"); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("project: %v", err)
	}
	if _, err := c.Version(bg, upstream.Modrinth, "", "a/b"); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("version: %v", err)
	}
	if _, err := c.Versions(bg, upstream.Modrinth, "", upstream.VersionFilter{}); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("versions: %v", err)
	}
	if _, err := c.Project(bg, upstream.Source("curseforge"), "x"); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("unknown source: %v", err)
	}
	if n := api.total(); n != 0 {
		t.Fatalf("invalid refs reached the upstream %d times", n)
	}
}

type hashBody struct {
	Hashes       []string `json:"hashes"`
	Algorithm    string   `json:"algorithm"`
	Loaders      []string `json:"loaders"`
	GameVersions []string `json:"game_versions"`
	VersionTypes []string `json:"version_types"`
}

func TestModrinthVersionsByHash(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var in hashBody
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Algorithm != "sha512" {
			t.Errorf("body: %+v, %v", in, err)
		}
		if slices.Contains(in.Hashes, luckperms553) {
			_, _ = w.Write(testdata(t, "modrinth_version_files.json"))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	got, err := c.VersionsByHash(bg, []string{strings.ToUpper(luckperms553), unknownHash, "not-a-hash", luckperms553})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[luckperms553].ID != "MBSY8toc" || got[luckperms553].ProjectID != "Vebnzrzj" {
		t.Fatalf("result = %+v", got)
	}
	_, body := api.lastRequest()
	var in hashBody
	_ = json.Unmarshal(body, &in)
	if !slices.Equal(in.Hashes, []string{unknownHash, luckperms553}) {
		t.Fatalf("hashes sent = %v (want sorted, unique, lower case, valid only)", in.Hashes)
	}
	// known and unknown hashes are cached for an hour
	clk.Advance(59 * time.Minute)
	if again, _ := c.VersionsByHash(bg, []string{luckperms553, unknownHash}); len(again) != 1 {
		t.Fatalf("cached result = %+v", again)
	}
	if n := api.count("POST /v2/version_files"); n != 1 {
		t.Fatalf("posted %d times", n)
	}
	clk.Advance(time.Minute)
	_, _ = c.VersionsByHash(bg, []string{unknownHash})
	if n := api.count("POST /v2/version_files"); n != 2 {
		t.Fatalf("posted %d times after an hour", n)
	}
}

func TestModrinthHashBatches(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	hashes := make([]string, 0, 600)
	for i := range 600 {
		hashes = append(hashes, hexHash(i))
	}
	if _, err := c.VersionsByHash(bg, hashes); err != nil {
		t.Fatal(err)
	}
	if n := api.count("POST /v2/version_files"); n != 2 {
		t.Fatalf("posted %d times for 600 hashes, want 2 (500 per batch)", n)
	}
	if _, err := c.VersionsByHash(bg, []string{"nope"}); err != nil || api.count("POST /v2/version_files") != 2 {
		t.Fatalf("invalid hashes only must not call the upstream: %v", err)
	}
}

// hexHash returns a valid, distinct sha512 hex string for i.
func hexHash(i int) string {
	const digits = "0123456789abcdef"
	b := []byte(strings.Repeat("0", 128))
	for p := 127; i > 0; p-- {
		b[p] = digits[i%16]
		i /= 16
	}
	return string(b)
}

func TestModrinthLatestByHash(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_version_files_update.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	got, err := c.LatestByHash(bg, []string{luckperms553}, paperLoaders, []string{"1.21.11"}, []upstream.Channel{upstream.Release, upstream.Beta})
	if err != nil {
		t.Fatal(err)
	}
	if v := got[luckperms553]; v.ID != "b0mk8uS6" || v.Name != "v5.5.71-bukkit" || !v.Downloadable() {
		t.Fatalf("latest = %+v", v)
	}
	req, body := api.lastRequest()
	var in hashBody
	_ = json.Unmarshal(body, &in)
	if req.URL.Path != "/v2/version_files/update" || in.Algorithm != "sha512" || !slices.Equal(in.Loaders, paperLoaders) ||
		!slices.Equal(in.GameVersions, []string{"1.21.11"}) || !slices.Equal(in.VersionTypes, []string{"release", "beta"}) {
		t.Fatalf("request = %s %+v", req.URL.Path, in)
	}
	_, _ = c.LatestByHash(bg, []string{luckperms553}, paperLoaders, []string{"1.21.11"}, []upstream.Channel{upstream.Release})
	if n := api.count("POST /v2/version_files/update"); n != 2 {
		t.Fatalf("update lookups are not cached here (the plugins package caches per server): %d", n)
	}
}
