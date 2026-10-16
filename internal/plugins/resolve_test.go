package plugins_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

type planJSON struct {
	Source      string  `json:"source"`
	ProjectID   string  `json:"project_id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	VersionID   string  `json:"version_id"`
	VersionName string  `json:"version_name"`
	Channel     string  `json:"channel"`
	Filename    string  `json:"filename"`
	Size        int64   `json:"size"`
	RequiredBy  *string `json:"required_by"`
}

type resJSON struct {
	Install []planJSON `json:"install"`
	Already []struct {
		Source    string `json:"source"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		File      string `json:"file"`
	} `json:"already"`
	External []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"external"`
	Incompatible []struct {
		Title  string `json:"title"`
		Reason string `json:"reason"`
	} `json:"incompatible"`
}

// resolve asks the resolve route as admin; it returns the status and, for 200, the plan.
func (e *env) resolve(id int64, src, project, version string) (int, resJSON, string) {
	e.t.Helper()
	q := url.Values{"source": {src}, "project": {project}}
	if version != "" {
		q.Set("version", version)
	}
	a := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, "/resolve?"+q.Encode()), nil, &a)
	if rec.Code != http.StatusOK {
		return rec.Code, resJSON{}, errCode(e.t, rec)
	}
	return rec.Code, decode[resJSON](e.t, rec), ""
}

func (r resJSON) installTitles() []string {
	var out []string
	for _, p := range r.Install {
		out = append(out, p.Title)
	}
	return out
}

// fabricWorld registers Chunky (requires Fabric API) and Fabric API on the fake.
func fabricWorld(e *env) (chunky, fapi *upFile) {
	fapi = e.up.jar("fabric-api-0.141.6+1.21.11.jar", "fabric api 0.141.6")
	e.up.modrinth("P7dR8mSH", "fabric-api", "Fabric API", "optional",
		mv{ID: "6qAuTtLR", Number: "0.141.6+1.21.11", File: fapi, Loaders: []string{"fabric"}})
	chunky = e.up.jar("Chunky-Fabric-1.4.55.jar", "chunky 1.4.55")
	e.up.modrinth("fALzjamp", "chunky", "Chunky", "optional",
		mv{ID: "1CpEkmcD", Number: "1.4.55", File: chunky, Loaders: []string{"fabric"}, Deps: []map[string]any{mdep("P7dR8mSH", "")}})
	return chunky, fapi
}

func TestResolveWithDependency(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	code, res, ec := e.resolve(id, "modrinth", "chunky", "")
	if code != http.StatusOK {
		t.Fatalf("resolve: %d %s", code, ec)
	}
	if len(res.Install) != 2 {
		t.Fatalf("install = %+v", res.Install)
	}
	dep, root := res.Install[0], res.Install[1]
	if dep.ProjectID != "P7dR8mSH" || dep.Filename != fapi.Name || dep.Size != fapi.Size || dep.VersionID != "6qAuTtLR" ||
		dep.VersionName != "0.141.6+1.21.11" || dep.Channel != "release" || dep.RequiredBy == nil || *dep.RequiredBy != "Chunky" {
		t.Fatalf("dependency = %+v", dep)
	}
	if root.ProjectID != "fALzjamp" || root.Slug != "chunky" || root.Filename != chunky.Name || root.RequiredBy != nil {
		t.Fatalf("root = %+v", root)
	}
	if len(res.Already)+len(res.External)+len(res.Incompatible) != 0 {
		t.Fatalf("resolution = %+v", res)
	}
}

func TestResolveAlready(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	// Fabric API uploaded by hand and switched off: still installed (hash mapping)
	e.files.put(id, "mods/fapi.jar.disabled", fapi.content)
	_, res, _ := e.resolve(id, "modrinth", "chunky", "")
	if !slices.Equal(res.installTitles(), []string{"Chunky"}) || len(res.Already) != 1 ||
		res.Already[0].ProjectID != "P7dR8mSH" || res.Already[0].File != "fapi.jar.disabled" {
		t.Fatalf("resolution = %+v", res)
	}
	// the requested project itself is installed: nothing to do
	e.files.put(id, "mods/chunky.jar", chunky.content)
	_, res, _ = e.resolve(id, "modrinth", "chunky", "")
	if len(res.Install) != 0 || len(res.Already) != 1 || res.Already[0].File != "chunky.jar" {
		t.Fatalf("installed root = %+v", res)
	}
}

// Decision 4: another version of an installed project is a replacement, the same one is "already".
func TestResolveVersionChange(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	newJar, oldJar := e.up.jar("Tool-2.jar", "tool 2"), e.up.jar("Tool-1.jar", "tool 1")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: newJar}, mv{ID: "t1", File: oldJar})
	e.files.put(id, "plugins/Tool-1.jar", oldJar.content)
	e.seedRow(id, "Tool-1.jar", oldJar.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	_, res, _ := e.resolve(id, "modrinth", "TOOL", "t2")
	if len(res.Install) != 1 || res.Install[0].VersionID != "t2" || res.Install[0].Filename != "Tool-2.jar" || len(res.Already) != 0 {
		t.Fatalf("version change = %+v", res)
	}
	_, res, _ = e.resolve(id, "modrinth", "TOOL", "t1")
	if len(res.Install) != 0 || len(res.Already) != 1 {
		t.Fatalf("same version = %+v", res)
	}
}

func TestResolveHangarDependencyAndExternal(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.hangar(31, "ViaVersion", "ViaVersion",
		hv{Name: "5.12.2-SNAPSHOT+1", Channel: "Snapshot", File: e.up.jar("ViaVersion-5.12.2-SNAPSHOT.jar", "vv snap")},
		hv{Name: "5.12.1", File: e.up.jar("ViaVersion-5.12.1.jar", "vv 5.12.1")})
	e.up.hangar(12, "ViaVersion", "ViaBackwards",
		hv{Name: "5.12.1", File: e.up.jar("ViaBackwards-5.12.1.jar", "vb 5.12.1"), Deps: []map[string]any{
			hdep("ViaVersion", 31, ""),
			hdep("Vault", 0, "https://www.spigotmc.org/resources/vault.34315/"),
			hdep("Evil", 0, "javascript:alert(1)"),
			hdep("Plain", 0, "http://evil.example/plugin.jar"),
			hdep("Data", 0, "data:text/html,hi"),
		}})
	code, res, ec := e.resolve(id, "hangar", "12", "")
	if code != http.StatusOK {
		t.Fatalf("resolve: %d %s", code, ec)
	}
	if !slices.Equal(res.installTitles(), []string{"ViaVersion", "ViaBackwards"}) {
		t.Fatalf("install = %+v", res.Install)
	}
	if vv := res.Install[0]; vv.VersionID != "5.12.1" || vv.Channel != "release" || vv.Source != "hangar" || vv.ProjectID != "31" {
		t.Fatalf("ViaVersion must be the newest Release, got %+v", vv)
	}
	if len(res.External) != 4 || res.External[0].Name != "Vault" || res.External[0].URL != "https://www.spigotmc.org/resources/vault.34315/" {
		t.Fatalf("external = %+v", res.External)
	}
	for _, x := range res.External[1:] {
		if x.URL != "" {
			t.Errorf("external %s: url %q, want empty (only https links pass)", x.Name, x.URL)
		}
	}
}

func TestResolveIncompatibleDependency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("NEWLIB", "newlib", "New Lib", "required", mv{ID: "n1", GameVersions: []string{"26.3"}, File: e.up.jar("NewLib.jar", "n")})
	e.up.modrinth("APP", "app", "App", "required",
		mv{ID: "a1", File: e.up.jar("App.jar", "a"), Deps: []map[string]any{mdep("NEWLIB", ""), mdep("MISSING", "")}})
	code, res, _ := e.resolve(id, "modrinth", "APP", "")
	if code != http.StatusOK || !slices.Equal(res.installTitles(), []string{"App"}) || len(res.Incompatible) != 2 {
		t.Fatalf("resolution = %d %+v", code, res)
	}
	if res.Incompatible[0].Title != "New Lib" || res.Incompatible[0].Reason != "no_compatible_version" ||
		res.Incompatible[1].Title != "MISSING" || res.Incompatible[1].Reason != "no_compatible_version" {
		t.Fatalf("incompatible = %+v", res.Incompatible)
	}
}

func TestResolveRequestedErrors(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("FUTURE", "future", "Future", "required", mv{ID: "f1", GameVersions: []string{"26.3"}, File: e.up.jar("F.jar", "f")})
	e.up.modrinth("OTHER", "other", "Other", "required", mv{ID: "o1", File: e.up.jar("O.jar", "o")})
	e.up.modrinth("FAB", "fab", "Fabric only", "required", mv{ID: "fab1", Loaders: []string{"fabric"}, File: e.up.jar("Fab.jar", "x")})
	e.up.hangar(23, "EssentialsX", "Essentials", hv{Name: "2.22.0", External: "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0"})
	for _, tc := range []struct {
		src, project, version string
		code                  int
		ec                    string
	}{
		{"modrinth", "FUTURE", "", http.StatusConflict, httpx.CodePluginIncompatible},
		{"modrinth", "FAB", "", http.StatusConflict, httpx.CodePluginIncompatible},
		{"modrinth", "FUTURE", "f1", http.StatusConflict, httpx.CodePluginIncompatible},
		{"modrinth", "FUTURE", "o1", http.StatusConflict, httpx.CodePluginIncompatible}, // version of another project
		{"hangar", "23", "", http.StatusConflict, httpx.CodePluginNotDownloadable},
		{"hangar", "Essentials", "2.22.0", http.StatusConflict, httpx.CodePluginNotDownloadable},
		{"modrinth", "NOPE", "", http.StatusNotFound, httpx.CodeNotFound},
		{"modrinth", "OTHER", "nope", http.StatusNotFound, httpx.CodeNotFound},
	} {
		if code, _, ec := e.resolve(id, tc.src, tc.project, tc.version); code != tc.code || ec != tc.ec {
			t.Errorf("%s %s %s: %d %s, want %d %s", tc.src, tc.project, tc.version, code, ec, tc.code, tc.ec)
		}
	}
}

func TestResolveFixedVersionID(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("LIB", "lib", "Lib", "required",
		mv{ID: "lib-new", File: e.up.jar("Lib-2.jar", "lib 2")},
		mv{ID: "lib-old", File: e.up.jar("Lib-1.jar", "lib 1")},
		mv{ID: "lib-26", GameVersions: []string{"26.3"}, File: e.up.jar("Lib-26.jar", "lib 26")})
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a1", File: e.up.jar("A.jar", "a"), Deps: []map[string]any{mdep("LIB", "lib-old")}})
	e.up.modrinth("B", "b", "B", "required", mv{ID: "b1", File: e.up.jar("B.jar", "b"), Deps: []map[string]any{mdep("", "lib-old")}})
	e.up.modrinth("C", "c", "C", "required", mv{ID: "c1", File: e.up.jar("C.jar", "c"), Deps: []map[string]any{mdep("LIB", "lib-26")}})
	for _, p := range []string{"A", "B"} {
		_, res, _ := e.resolve(id, "modrinth", p, "")
		if len(res.Install) != 2 || res.Install[0].VersionID != "lib-old" || res.Install[0].ProjectID != "LIB" {
			t.Errorf("%s: %+v", p, res.Install)
		}
	}
	_, res, _ := e.resolve(id, "modrinth", "C", "")
	if len(res.Incompatible) != 1 || res.Incompatible[0].Title != "Lib" || res.Incompatible[0].Reason != "no_compatible_version" {
		t.Fatalf("fixed incompatible version = %+v", res)
	}
}

func TestResolveChannel(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("MIX", "mix", "Mix", "required",
		mv{ID: "beta2", Type: "beta", File: e.up.jar("Mix-2b.jar", "2b")},
		mv{ID: "rel1", File: e.up.jar("Mix-1.jar", "1")})
	e.up.modrinth("BETA", "beta", "Beta only", "required", mv{ID: "b1", Type: "beta", File: e.up.jar("B.jar", "b")})
	if _, res, _ := e.resolve(id, "modrinth", "MIX", ""); res.Install[0].VersionID != "rel1" {
		t.Errorf("default must be the newest release: %+v", res.Install)
	}
	if _, res, _ := e.resolve(id, "modrinth", "BETA", ""); res.Install[0].VersionID != "b1" || res.Install[0].Channel != "beta" {
		t.Errorf("without release the newest version: %+v", res.Install)
	}
	if _, res, _ := e.resolve(id, "modrinth", "MIX", "beta2"); res.Install[0].VersionID != "beta2" {
		t.Errorf("explicit beta: %+v", res.Install)
	}
}

func TestResolveClientOnly(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	e.up.modrinth("PtjYWJkn", "sodium-extra", "Sodium Extra", "unsupported",
		mv{ID: "s1", Loaders: []string{"fabric"}, File: e.up.jar("sodium-extra.jar", "s")})
	e.up.modrinth("NEEDS", "needs", "Needs Sodium", "optional",
		mv{ID: "n1", Loaders: []string{"fabric"}, File: e.up.jar("Needs.jar", "n"), Deps: []map[string]any{mdep("PtjYWJkn", "")}})
	if code, _, ec := e.resolve(id, "modrinth", "sodium-extra", ""); code != http.StatusConflict || ec != httpx.CodePluginIncompatible {
		t.Fatalf("client-only root: %d %s", code, ec)
	}
	_, res, _ := e.resolve(id, "modrinth", "NEEDS", "")
	if len(res.Incompatible) != 1 || res.Incompatible[0].Reason != "client_only" || res.Incompatible[0].Title != "Sodium Extra" {
		t.Fatalf("client-only dependency = %+v", res)
	}
}

func TestResolveDepthAndLimit(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	// chain P0 -> P1 -> ... -> P7: depth 5 is the last one planned
	for i := 7; i >= 0; i-- {
		var deps []map[string]any
		if i < 7 {
			deps = []map[string]any{mdep(fmt.Sprintf("P%d", i+1), "")}
		}
		e.up.modrinth(fmt.Sprintf("P%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("P%d", i), "required",
			mv{ID: fmt.Sprintf("v%d", i), File: e.up.jar(fmt.Sprintf("P%d.jar", i), fmt.Sprint(i)), Deps: deps})
	}
	_, res, _ := e.resolve(id, "modrinth", "P0", "")
	if !slices.Equal(res.installTitles(), []string{"P5", "P4", "P3", "P2", "P1", "P0"}) ||
		len(res.Incompatible) != 1 || res.Incompatible[0].Title != "P6" || res.Incompatible[0].Reason != "too_deep" {
		t.Fatalf("chain = %v / %+v", res.installTitles(), res.Incompatible)
	}
	// 25 direct dependencies: 19 of them plus the root fill the 20 packages
	var deps []map[string]any
	for i := range 25 {
		pid := fmt.Sprintf("W%02d", i)
		e.up.modrinth(pid, "w"+pid, pid, "required", mv{ID: "wv" + pid, File: e.up.jar(pid+".jar", pid)})
		deps = append(deps, mdep(pid, ""))
	}
	e.up.modrinth("WIDE", "wide", "Wide", "required", mv{ID: "wide1", File: e.up.jar("Wide.jar", "wide"), Deps: deps})
	_, res, _ = e.resolve(id, "modrinth", "WIDE", "")
	if len(res.Install) != 20 || len(res.Incompatible) != 6 || res.Incompatible[0].Reason != "too_many" {
		t.Fatalf("wide = %d install, %+v", len(res.Install), res.Incompatible)
	}
}

func TestResolveCycle(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("CA", "ca", "Cycle A", "required", mv{ID: "a1", File: e.up.jar("CA.jar", "a"), Deps: []map[string]any{mdep("CB", "")}})
	e.up.modrinth("CB", "cb", "Cycle B", "required", mv{ID: "b1", File: e.up.jar("CB.jar", "b"), Deps: []map[string]any{mdep("CA", ""), mdep("CB", "")}})
	code, res, _ := e.resolve(id, "modrinth", "CA", "")
	if code != http.StatusOK || !slices.Equal(res.installTitles(), []string{"Cycle B", "Cycle A"}) || len(res.Incompatible) != 0 {
		t.Fatalf("cycle = %d %+v", code, res)
	}
}

func TestResolveValidation(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	a := e.admin()
	for _, q := range []string{
		"", "?source=modrinth", "?source=hangar&project=x", "?source=curseforge&project=x",
		"?source=modrinth&project=..%2Fx", "?source=modrinth&project=x&version=a%20b",
		"?source=modrinth&project=x%00",
	} {
		rec := e.do(http.MethodGet, srvPath(id, "/resolve"+q), nil, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%q: %d %s", q, rec.Code, rec.Body)
		}
	}
}
