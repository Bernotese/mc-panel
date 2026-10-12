package upstream_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestHangarSearch(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_search.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	res, err := c.Search(bg, upstream.Hangar, upstream.SearchQuery{Query: "via", GameVersion: "1.21.11", Offset: 40})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	if req.URL.Path != "/api/v1/projects" || q.Get("q") != "via" || q.Get("platform") != "PAPER" || q.Get("version") != "1.21.11" ||
		q.Get("sort") != "-downloads" || q.Get("offset") != "40" || q.Get("limit") != "20" {
		t.Fatalf("request = %s %v", req.URL.Path, q)
	}
	want := upstream.SearchHit{
		ProjectID: "12", Slug: "ViaBackwards", Owner: "ViaVersion", Title: "ViaBackwards", Author: "ViaVersion",
		Description: "Allow Java Edition clients with older versions to connect to your Minecraft server",
		IconURL:     "https://hangarcdn.papermc.io/avatars/project/12.webp?v=1", Downloads: 754151,
	}
	if res.Total != 377 || len(res.Hits) != 2 || res.Hits[0] != want || res.Hits[1].ProjectID != "31" {
		t.Fatalf("result = %+v", res)
	}
}

func TestHangarProject(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/ViaBackwards":
			_, _ = w.Write(testdata(t, "hangar_project_viabackwards.json"))
		case "/api/v1/projects/31":
			_, _ = w.Write(testdata(t, "hangar_project_viaversion.json"))
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	p, err := c.Project(bg, upstream.Hangar, "ViaBackwards")
	if err != nil {
		t.Fatal(err)
	}
	want := upstream.Project{
		Source: upstream.Hangar, ID: "12", Slug: "ViaBackwards", Owner: "ViaVersion", Title: "ViaBackwards",
		Description: "Allow Java Edition clients with older versions to connect to your Minecraft server",
		IconURL:     "https://hangarcdn.papermc.io/avatars/project/12.webp?v=1",
	}
	if p != want || p.ClientOnly() {
		t.Fatalf("project = %+v", p)
	}
	if v, err := c.Project(bg, upstream.Hangar, "31"); err != nil || v.Slug != "ViaVersion" || v.ID != "31" {
		t.Fatalf("by id = %+v, %v", v, err)
	}
}

func TestHangarVersions(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_versions_viabackwards.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "ViaBackwards", upstream.VersionFilter{Loaders: []string{"ignored"}, GameVersion: "1.21.11"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	if req.URL.Path != "/api/v1/projects/ViaBackwards/versions" || q.Get("platform") != "PAPER" || q.Get("platformVersion") != "1.21.11" ||
		q.Get("limit") != "25" || q.Has("channel") || q.Has("loaders") {
		t.Fatalf("request = %s %v", req.URL.Path, q)
	}
	if len(vs) != 2 {
		t.Fatalf("versions = %+v", vs)
	}
	snap, rel := vs[0], vs[1]
	wantFile := upstream.File{
		URL:  "https://hangarcdn.papermc.io/plugins/ViaVersion/ViaBackwards/versions/5.12.2-SNAPSHOT%2B657/PAPER/ViaBackwards-5.12.2-SNAPSHOT.jar",
		Name: "ViaBackwards-5.12.2-SNAPSHOT.jar", Size: 1437280,
		SHA256: "4f2f2b37e16cbd8936257e9dcbd2179ac82002cfe1998c408056c563f8d02c8d",
	}
	if snap.Source != upstream.Hangar || snap.ID != "5.12.2-SNAPSHOT+657" || snap.Name != snap.ID || snap.ProjectID != "12" ||
		snap.Channel != upstream.Beta || snap.File == nil || *snap.File != wantFile || !slices.Contains(snap.GameVersions, "1.21.11") {
		t.Fatalf("snapshot = %+v", snap)
	}
	// only the required dependency (ViaVersion), not the optional ViaRewind
	if !slices.Equal(snap.Dependencies, []upstream.Dependency{{ProjectID: "31", Name: "ViaVersion"}}) {
		t.Fatalf("dependencies = %+v", snap.Dependencies)
	}
	if rel.ID != "5.12.1" || rel.Channel != upstream.Release || !rel.PublishedAt.Before(snap.PublishedAt) {
		t.Fatalf("release = %+v", rel)
	}
}

func TestHangarVersionsReleaseOnly(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_versions_viaversion_release.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "31", upstream.VersionFilter{GameVersion: "1.21.11", ReleaseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	if req.URL.Query().Get("channel") != "Release" {
		t.Fatalf("query = %v", req.URL.Query())
	}
	if len(vs) != 1 || vs[0].ID != "5.12.1" || vs[0].File.Name != "ViaVersion-5.12.1.jar" || len(vs[0].Dependencies) != 0 {
		t.Fatalf("versions = %+v", vs)
	}
}

func TestHangarVersion(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/ViaBackwards/versions/5.12.1":
			_, _ = w.Write(testdata(t, "hangar_version_viabackwards_5.12.1.json"))
		case "/api/v1/projects/ViaBackwards/versions/5.12.2-SNAPSHOT+657":
			_, _ = w.Write(testdata(t, "hangar_version_viabackwards_5.12.1.json"))
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	v, err := c.Version(bg, upstream.Hangar, "ViaBackwards", "5.12.1")
	if err != nil || v.ID != "5.12.1" || v.Channel != upstream.Release || v.File.SHA256 != "4b9dd5cb7662f532b44627f8491255bf96f586cbd00280d1480442590ad4450d" {
		t.Fatalf("version = %+v, %v", v, err)
	}
	if _, err := c.Version(bg, upstream.Hangar, "ViaBackwards", "5.12.2-SNAPSHOT+657"); err != nil {
		t.Fatalf("a name with + must reach the upstream unchanged: %v", err)
	}
}

// Review Focus #1: a version that is only linked externally has no File; its external URL is
// kept, and optional external dependencies are dropped.
func TestHangarExternalOnly(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_versions_essentials.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "Essentials", upstream.VersionFilter{GameVersion: "1.21.11"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	v := vs[0]
	if v.Downloadable() || v.ExternalURL != "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0" || len(v.Dependencies) != 0 {
		t.Fatalf("version = %+v", v)
	}
}

func TestHangarExternalDependency(t *testing.T) {
	body := `{"result":[{"projectId":5,"name":"1.0","createdAt":"2026-01-01T00:00:00Z","channel":{"name":"release"},
		"downloads":{"PAPER":{"fileInfo":{"name":"a.jar","sizeBytes":3,"sha256Hash":"ABC"},"externalUrl":null,"downloadUrl":"https://hangarcdn.papermc.io/a.jar"}},
		"pluginDependencies":{"PAPER":[{"name":"Vault","projectId":null,"required":true,"externalUrl":"https://www.spigotmc.org/resources/vault.34315/"},
		{"name":"Other","projectId":7,"required":true,"externalUrl":null}],"VELOCITY":[{"name":"X","projectId":9,"required":true}]},
		"platformDependencies":{"PAPER":["1.21.11"]}}]}`
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "5", upstream.VersionFilter{GameVersion: "1.21.11"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	want := []upstream.Dependency{
		{Name: "Vault", ExternalURL: "https://www.spigotmc.org/resources/vault.34315/"},
		{ProjectID: "7", Name: "Other"},
	}
	if !slices.Equal(vs[0].Dependencies, want) || vs[0].Channel != upstream.Release || vs[0].File.SHA256 != "abc" {
		t.Fatalf("version = %+v", vs[0])
	}
}
