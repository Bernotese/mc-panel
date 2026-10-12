package plugins_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// S2: dependencies past the depth or plan limit are refused before any upstream call, a
// version's required dependencies are capped, and one resolution makes a bounded number of
// upstream calls.
func TestResolveBoundsUpstreamWork(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for i := 7; i >= 0; i-- {
		var deps []map[string]any
		if i < 7 {
			deps = []map[string]any{mdep(fmt.Sprintf("P%d", i+1), "")}
		}
		e.up.modrinth(fmt.Sprintf("P%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("P%d", i), "required",
			mv{ID: fmt.Sprintf("v%d", i), File: e.up.jar(fmt.Sprintf("P%d.jar", i), fmt.Sprint(i)), Deps: deps})
	}
	e.resolve(id, "modrinth", "P0", "")
	if n := e.up.count("GET /v2/project/P6") + e.up.count("GET /v2/project/P6/version"); n != 0 {
		t.Fatalf("P6 (too deep) was fetched %d times", n)
	}

	// 60 direct dependencies: 19 are planned, the rest is too_many, and only the first 50
	// are considered at all
	var deps []map[string]any
	for i := range 60 {
		pid := fmt.Sprintf("D%02d", i)
		e.up.modrinth(pid, "d"+pid, pid, "required", mv{ID: "dv" + pid, File: e.up.jar(pid+".jar", pid)})
		deps = append(deps, mdep(pid, ""))
	}
	e.up.modrinth("MANY", "many", "Many", "required", mv{ID: "many1", File: e.up.jar("Many.jar", "many"), Deps: deps})
	code, res, _ := e.resolve(id, "modrinth", "MANY", "")
	if code != http.StatusOK || len(res.Install) != 20 {
		t.Fatalf("many = %d, %d to install", code, len(res.Install))
	}
	tooMany := 0
	for _, x := range res.Incompatible {
		if x.Reason == "too_many" {
			tooMany++
		}
	}
	if tooMany != 32 { // 31 beyond the plan limit, one entry for the 10 beyond the cap
		t.Fatalf("too_many entries = %d: %+v", tooMany, res.Incompatible)
	}
	for _, pid := range []string{"D19", "D49", "D55"} {
		if n := e.up.count("GET /v2/project/" + pid); n != 0 {
			t.Fatalf("%s was fetched %d times", pid, n)
		}
	}

	// fixed versions that do not exist cost a call each but plan nothing: the call budget
	// stops them
	var bogus []map[string]any
	for i := range 10 {
		pid := fmt.Sprintf("B%02d", i)
		var sub []map[string]any
		for j := range 50 {
			sub = append(sub, mdep("", fmt.Sprintf("nope%02d%02d", i, j)))
		}
		e.up.modrinth(pid, "b"+pid, pid, "required", mv{ID: "bv" + pid, File: e.up.jar(pid+".jar", pid), Deps: sub})
		bogus = append(bogus, mdep(pid, ""))
	}
	e.up.modrinth("BOGUS", "bogus", "Bogus", "required", mv{ID: "bogus1", File: e.up.jar("Bogus.jar", "bogus"), Deps: bogus})
	before := e.up.apiTotal()
	if code, _, _ := e.resolve(id, "modrinth", "BOGUS", ""); code != http.StatusOK {
		t.Fatalf("bogus = %d", code)
	}
	if n := e.up.apiTotal() - before; n > 80 {
		t.Fatalf("one resolution made %d upstream calls", n)
	}
}

// K6: an installed project asked for without a version is "already", even if no version of it
// fits the server any more.
func TestResolveInstalledWithoutCompatibleVersion(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	old := e.up.jar("Old-1.0.jar", "old 1.0")
	e.up.modrinth("OLD", "old", "Old", "required", mv{ID: "o1", File: old, GameVersions: []string{"1.20.4"}})
	e.files.put(id, "plugins/Old-1.0.jar", old.content)
	e.seedRow(id, "Old-1.0.jar", old.content, "modrinth", "OLD", "old", "Old", "o1", "o1", "release")
	code, res, errc := e.resolve(id, "modrinth", "OLD", "")
	if code != http.StatusOK || len(res.Already) != 1 || res.Already[0].File != "Old-1.0.jar" || len(res.Install) != 0 {
		t.Fatalf("resolve = %d %s %+v", code, errc, res)
	}
}

// S7: project and version references made only of dots are refused.
func TestDotRefsRefused(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	a := e.admin()
	for _, q := range []string{
		"?source=modrinth&project=.", "?source=modrinth&project=..", "?source=modrinth&project=...",
		"?source=modrinth&project=x&version=..", "?source=hangar&project=x&version=.",
	} {
		rec := e.do(http.MethodGet, srvPath(id, "/resolve"+q), nil, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%q: %d %s", q, rec.Code, rec.Body)
		}
	}
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": ".."}); rec.Code != http.StatusBadRequest {
		t.Errorf("install ..: %d %s", rec.Code, rec.Body)
	}
	if n := e.up.apiTotal(); n != 0 {
		t.Fatalf("dot refs reached the upstream %d times", n)
	}
}

// S5: a check that is not served from the cache counts against the lookup limit, so
// toggling (which drops the cache) cannot be used to query the upstream without limit.
func TestUpdatesCacheMissIsLimited(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LP-old.jar")
	a := e.admin()
	for range 20 {
		e.updates(id, "?refresh=1")
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("cached check: %d", rec.Code)
	}
	if code, errc := e.toggle(id, "LP-old.jar", false); code != http.StatusNoContent {
		t.Fatalf("toggle: %d %s", code, errc)
	}
	rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited {
		t.Fatalf("uncached check beyond the limit: %d %s", rec.Code, rec.Body)
	}
	if rec := e.post(id, "/update", map[string]any{"files": []string{}}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("update with an uncached check beyond the limit: %d %s", rec.Code, rec.Body)
	}
}

// S6: with a truncated listing a name that is not listed is not passed to the service (its
// delete removes directories recursively).
func TestManageTruncatedUnlisted(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.put(id, "plugins/data.jar/config.yml", "a directory named like a jar")
	e.files.setTruncated(true)
	e.files.takeCalls()
	rec := e.post(id, "/delete", map[string]any{"file": "data.jar"})
	if rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if code, errc := e.toggle(id, "data.jar", false); code != http.StatusNotFound {
		t.Fatalf("toggle: %d %s", code, errc)
	}
	for _, c := range e.files.takeCalls() {
		if !strings.HasPrefix(c, "jars ") {
			t.Fatalf("service call %q for an unlisted name", c)
		}
	}
}

// K8: with Modrinth down the inventory gives up on it after the first failures instead of
// waiting for every project lookup.
func TestInventoryStopsAskingDownSource(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for i := range 20 {
		name := fmt.Sprintf("J%02d.jar", i)
		e.files.put(id, "plugins/"+name, name)
		e.seedRow(id, name, name, "modrinth", fmt.Sprintf("PRJ%02d", i), "s", "T", "v", "v", "release")
	}
	e.up.setDown(true)
	before := e.up.apiTotal()
	inv := e.inventory(id)
	if len(inv.SourceErrors) != 1 || inv.SourceErrors[0] != "modrinth" {
		t.Fatalf("source_errors = %v", inv.SourceErrors)
	}
	if n := e.up.apiTotal() - before; n > 8 {
		t.Fatalf("%d project lookups against a source that is down", n)
	}
}
