package plugins_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type hitJSON struct {
	Source      string  `json:"source"`
	ProjectID   string  `json:"project_id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Author      string  `json:"author"`
	Downloads   int64   `json:"downloads"`
	IconURL     *string `json:"icon_url"`
	PageURL     string  `json:"page_url"`
	Installed   bool    `json:"installed"`
}

type searchJSON struct {
	Hits   []hitJSON `json:"hits"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
}

func TestSearchModrinth(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("Vebnzrzj", "luckperms", "LuckPerms", "required")
	chunky := e.up.jar("Chunky.jar", "chunky")
	e.up.modrinth("fALzjamp", "chunky", "Chunky", "optional", mv{ID: "c1", File: chunky})
	e.files.put(id, "plugins/Chunky.jar", chunky.content)
	res := decode[searchJSON](t, e.get(srvPath(id, "/search?source=modrinth&q=perm&offset=20")))
	q := e.up.query()
	if q.Get("query") != "perm" || q.Get("offset") != "20" ||
		q.Get("facets") != `[["project_type:plugin"],["categories:paper","categories:spigot","categories:bukkit"],["versions:1.21.11"]]` {
		t.Fatalf("upstream query = %v", q)
	}
	if res.Total != 2 || res.Offset != 20 || len(res.Hits) != 2 {
		t.Fatalf("result = %+v", res)
	}
	lp, ch := res.Hits[0], res.Hits[1]
	if lp.Source != "modrinth" || lp.ProjectID != "Vebnzrzj" || lp.Installed || lp.Author != "author-luckperms" ||
		lp.PageURL != "https://modrinth.com/plugin/luckperms" || lp.IconURL == nil || *lp.IconURL != proxied(id, e.up.iconURL("Vebnzrzj")) {
		t.Fatalf("hit = %+v", lp)
	}
	if !ch.Installed {
		t.Fatalf("installed project not marked: %+v", ch)
	}
}

func TestSearchMods(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	e.up.modrinth("fALzjamp", "chunky", "Chunky", "optional")
	res := decode[searchJSON](t, e.get(srvPath(id, "/search?source=modrinth")))
	q := e.up.query()
	if q.Get("facets") != `[["project_type:mod"],["categories:fabric"],["versions:1.21.11"],["server_side:required","server_side:optional"]]` ||
		q.Get("index") != "downloads" {
		t.Fatalf("upstream query = %v", q)
	}
	if res.Hits[0].PageURL != "https://modrinth.com/mod/chunky" {
		t.Fatalf("hit = %+v", res.Hits[0])
	}
}

func TestSearchHangar(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.hangar(12, "ViaVersion", "ViaBackwards")
	res := decode[searchJSON](t, e.get(srvPath(id, "/search?source=hangar&q=via")))
	if q := e.up.query(); q.Get("q") != "via" || q.Get("platform") != "PAPER" || q.Get("version") != "1.21.11" {
		t.Fatalf("upstream query = %v", q)
	}
	if h := res.Hits[0]; h.Source != "hangar" || h.ProjectID != "12" || h.Author != "ViaVersion" ||
		h.PageURL != "https://hangar.papermc.io/ViaVersion/ViaBackwards" || h.IconURL == nil {
		t.Fatalf("hit = %+v", h)
	}
}

func TestSearchValidation(t *testing.T) {
	e := newEnv(t)
	paper := e.paper("survival")
	fab := e.fabric("modded")
	a := e.admin()
	long := url.QueryEscape(strings.Repeat("ä", 101))
	for _, tc := range []struct {
		id int64
		q  string
	}{
		{paper, ""}, {paper, "?source=curseforge"}, {fab, "?source=hangar"},
		{paper, "?source=modrinth&q=" + long}, {paper, "?source=modrinth&offset=-1"},
		{paper, "?source=modrinth&offset=1001"}, {paper, "?source=modrinth&offset=x"},
	} {
		rec := e.do(http.MethodGet, srvPath(tc.id, "/search"+tc.q), nil, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%d %q: %d %s", tc.id, tc.q, rec.Code, rec.Body)
		}
	}
	ok := url.QueryEscape(strings.Repeat("ä", 100))
	if rec := e.do(http.MethodGet, srvPath(paper, "/search?source=modrinth&offset=1000&q="+ok), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("limits themselves: %d %s", rec.Code, rec.Body)
	}
}

// Review Focus #4: upstream down → 502 plugin_source_unavailable; LATEST without catalog → 502
// catalog_unavailable; the file service is not needed for searching.
func TestSearchSourceDown(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	latest := createInstance(t, e.db, "latest", instancespec.TypePaper, "LATEST")
	a := e.admin()
	e.up.modrinth("X", "x", "X", "required")
	e.files.setErr("jars", filesapi.ErrUnavailable)
	if rec := e.do(http.MethodGet, srvPath(id, "/search?source=modrinth"), nil, &a); rec.Code != http.StatusOK ||
		decode[searchJSON](t, rec).Hits[0].Installed {
		t.Fatalf("without file service: %d %s", rec.Code, rec.Body)
	}
	e.up.setDown(true)
	rec := e.do(http.MethodGet, srvPath(id, "/search?source=modrinth&q=new"), nil, &a)
	if rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodePluginSourceUnavailable {
		t.Fatalf("upstream down: %d %s", rec.Code, rec.Body)
	}
	e.cat.setErr(errors.New("catalog down"))
	rec = e.do(http.MethodGet, srvPath(latest, "/search?source=modrinth"), nil, &a)
	if rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodeCatalogUnavailable {
		t.Fatalf("catalog down: %d %s", rec.Code, rec.Body)
	}
}

type versionsJSON struct {
	Versions []struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		Channel      string  `json:"channel"`
		PublishedAt  int64   `json:"published_at"`
		Downloadable bool    `json:"downloadable"`
		ExternalURL  *string `json:"external_url"`
	} `json:"versions"`
}

func TestVersionsList(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("MIX", "mix", "Mix", "required",
		mv{ID: "beta2", Number: "2.0-beta", Type: "beta", File: e.up.jar("Mix-2.jar", "2")},
		mv{ID: "rel1", Number: "1.0", File: e.up.jar("Mix-1.jar", "1")},
		mv{ID: "fut", GameVersions: []string{"26.3"}, File: e.up.jar("Mix-26.jar", "26")})
	e.up.hangar(23, "EssentialsX", "Essentials",
		hv{Name: "2.22.0", External: "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0"},
		hv{Name: "2.21.0", External: "javascript:alert(1)"})
	res := decode[versionsJSON](t, e.get(srvPath(id, "/versions?source=modrinth&project=MIX")))
	if len(res.Versions) != 2 || res.Versions[0].ID != "beta2" || res.Versions[0].Name != "2.0-beta" || res.Versions[0].Channel != "beta" ||
		!res.Versions[0].Downloadable || res.Versions[0].ExternalURL != nil || res.Versions[0].PublishedAt != t0.UnixMilli() ||
		res.Versions[1].ID != "rel1" {
		t.Fatalf("modrinth versions = %+v", res.Versions)
	}
	res = decode[versionsJSON](t, e.get(srvPath(id, "/versions?source=hangar&project=Essentials")))
	if len(res.Versions) != 2 || res.Versions[0].Downloadable || res.Versions[0].ExternalURL == nil ||
		*res.Versions[0].ExternalURL != "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0" || res.Versions[1].ExternalURL != nil {
		t.Fatalf("hangar versions = %+v", res.Versions)
	}
	a := e.admin()
	if rec := e.do(http.MethodGet, srvPath(id, "/versions?source=modrinth"), nil, &a); rec.Code != http.StatusBadRequest {
		t.Fatalf("without project: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/versions?source=modrinth&project=NOPE"), nil, &a); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d %s", rec.Code, rec.Body)
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("X", "x", "X", "required", mv{ID: "x1", File: e.up.jar("X.jar", "x")})
	a := e.admin()
	paths := []string{"/search?source=modrinth", "/versions?source=modrinth&project=X", "/resolve?source=modrinth&project=X"}
	for i := range 20 {
		if rec := e.do(http.MethodGet, srvPath(id, paths[i%3]), nil, &a); rec.Code != http.StatusOK {
			t.Fatalf("call %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := e.do(http.MethodGet, srvPath(id, paths[0]), nil, &a)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("21st call: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	// another account has its own bucket; the inventory is not limited
	other := storetest.CreateUser(t, e.db, "other", "user")
	grant(t, e.db, other, id, "server.view", "server.plugins")
	po := userP(other, "other")
	if rec := e.do(http.MethodGet, srvPath(id, paths[0]), nil, &po); rec.Code != http.StatusOK {
		t.Fatalf("other user: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("inventory limited: %d", rec.Code)
	}
	e.clk.Advance(time.Second)
	if rec := e.do(http.MethodGet, srvPath(id, paths[0]), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("after a second: %d", rec.Code)
	}
}
