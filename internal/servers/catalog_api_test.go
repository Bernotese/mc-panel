package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestGetCatalog(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	e.catalog.cat = catalog.Catalog{
		Versions: []catalog.Version{{ID: "26.3", Java: instancespec.Java25}, {ID: "1.21.11", Java: instancespec.Java21}},
		Loaders:  []string{"0.19.5"},
	}
	rec := e.do(http.MethodGet, "/servers/catalog/fabric", nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("fabric: %d %s", rec.Code, rec.Body)
	}
	got := decode[catalog.Catalog](t, rec)
	if len(got.Versions) != 2 || got.Versions[0].ID != "26.3" || got.Versions[0].Java != "java25" || !slices.Equal(got.Loaders, []string{"0.19.5"}) {
		t.Fatalf("catalog = %+v", got)
	}
	for _, typ := range []string{"modrinth", "curseforge", "bukkit", "..%2F..%2Fetc"} {
		rec := e.do(http.MethodGet, "/servers/catalog/"+typ, nil, &admin)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
			t.Errorf("%s: %d %s", typ, rec.Code, rec.Body)
		}
	}
	if !slices.Equal(e.catalog.asked, []instancespec.Type{instancespec.TypeFabric}) {
		t.Fatalf("catalog asked for %v", e.catalog.asked)
	}
	e.catalog.err = fmt.Errorf("%w: vanilla: GET …: HTTP 503", catalog.ErrUnavailable)
	rec = e.do(http.MethodGet, "/servers/catalog/vanilla", nil, &admin)
	if rec.Code != http.StatusBadGateway || errCode(t, rec) != "catalog_unavailable" {
		t.Fatalf("upstream down: %d %s", rec.Code, rec.Body)
	}
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	if rec := e.do(http.MethodGet, "/servers/catalog/paper", nil, &bob); rec.Code != http.StatusForbidden {
		t.Fatalf("user: %d", rec.Code)
	}
}
