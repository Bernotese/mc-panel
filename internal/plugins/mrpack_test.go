package plugins_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type mrpackEntryJSON struct {
	File     string          `json:"file"`
	SHA512   string          `json:"sha512"`
	Action   string          `json:"action"`
	Reason   *string         `json:"reason"`
	Optional bool            `json:"optional"`
	Project  *projectRefJSON `json:"project"`
	Version  *versionRefJSON `json:"version"`
	Replaces *string         `json:"replaces"`
}

type projectRefJSON struct {
	ID      string  `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	PageURL string  `json:"page_url"`
	IconURL *string `json:"icon_url"`
}

type mrpackPlanJSON struct {
	Pack struct {
		Name      string `json:"name"`
		VersionID string `json:"version_id"`
	} `json:"pack"`
	GameVersion struct {
		Pack   string `json:"pack"`
		Server string `json:"server"`
	} `json:"game_version"`
	Loader struct {
		Name   string  `json:"name"`
		Pack   string  `json:"pack"`
		Server *string `json:"server"`
	} `json:"loader"`
	VersionMismatch bool              `json:"version_mismatch"`
	Entries         []mrpackEntryJSON `json:"entries"`
	Kept            []string          `json:"kept"`
	IgnoredFiles    int               `json:"ignored_files"`
}

// summary renders the entries as "file:action[:reason][:optional][>replaces]".
func (p mrpackPlanJSON) summary() []string {
	out := make([]string, 0, len(p.Entries))
	for _, e := range p.Entries {
		s := e.File + ":" + e.Action
		if e.Reason != nil {
			s += ":" + *e.Reason
		}
		if e.Optional {
			s += ":optional"
		}
		if e.Replaces != nil {
			s += ">" + *e.Replaces
		}
		out = append(out, s)
	}
	return out
}

type packFile struct {
	path, sha512, server string // server "" = no env
}

func packIndex(minecraft, loaderKey, loaderVersion string, files ...packFile) map[string]any {
	fs := make([]any, 0, len(files))
	for _, f := range files {
		m := map[string]any{"path": f.path, "hashes": map[string]any{"sha512": f.sha512, "sha1": strings.Repeat("0", 40)},
			"downloads": []string{"https://github.com/evil/x.jar"}, "fileSize": 1}
		if f.server != "" {
			m["env"] = map[string]any{"client": "required", "server": f.server}
		}
		fs = append(fs, m)
	}
	deps := map[string]any{"minecraft": minecraft}
	if loaderKey != "" {
		deps[loaderKey] = loaderVersion
	}
	return map[string]any{"formatVersion": 1, "game": "minecraft", "versionId": "2.0", "name": "Test Pack", "files": fs, "dependencies": deps}
}

// mrpackWorld is a fabric server with installed mods and a pack that covers every case of the
// classification (spec §3).
type mrpackWorld struct {
	id                    int64
	fapi2, lith, dis2     *upFile
	files                 []packFile
	unknownHash, lithHash string
}

func newMrpackWorld(e *env) mrpackWorld {
	w := mrpackWorld{id: e.fabric("modded")}
	fapi1 := e.up.jar("fabric-api-1.jar", "fabric api 1")
	w.fapi2 = e.up.jar("fabric-api-2.jar", "fabric api 2")
	e.up.modrinth("FAPI", "fabric-api", "Fabric API", "required", mv{ID: "fa2", File: w.fapi2}, mv{ID: "fa1", File: fapi1})
	w.lith = e.up.jar("lithium.jar", "lithium")
	e.up.modrinth("LITH", "lithium", "Lithium", "optional", mv{ID: "li1", File: w.lith})
	sod := e.up.jar("sodium.jar", "sodium")
	e.up.modrinth("SOD", "sodium", "Sodium", "unsupported", mv{ID: "so1", File: sod})
	menu := e.up.jar("modmenu.jar", "modmenu")
	e.up.modrinth("MENU", "modmenu", "Mod Menu", "required", mv{ID: "mm1", File: menu})
	neo := e.up.jar("neo-only.jar", "neo only")
	e.up.modrinth("NEO", "neo-only", "Neo Only", "required", mv{ID: "ne1", File: neo, Loaders: []string{"neoforge"}})
	fer := e.up.jar("ferrite.jar", "ferrite")
	e.up.modrinth("FER", "ferrite", "Ferrite", "required", mv{ID: "fe1", File: fer})
	clash := e.up.jar("Manual.jar", "clash")
	e.up.modrinth("CLASH", "clash", "Clash", "required", mv{ID: "cl1", File: clash})
	dis1 := e.up.jar("dis-1.jar", "dis 1")
	w.dis2 = e.up.jar("dis-2.jar", "dis 2")
	e.up.modrinth("DIS", "dis", "Dis", "required", mv{ID: "di2", File: w.dis2}, mv{ID: "di1", File: dis1})

	e.files.put(w.id, "mods/fabric-api-1.jar", fapi1.content)   // uploaded by hand, found by hash
	e.files.put(w.id, "mods/ferrite.jar.disabled", fer.content) // same hash, disabled
	e.files.put(w.id, "mods/Manual.jar", "a manual jar")        // name taken
	e.files.put(w.id, "mods/dis-1.jar.disabled", dis1.content)  // older version, disabled
	e.files.put(w.id, "mods/Other.jar", "another manual jar")   // not in the pack
	e.files.put(w.id, "config/ignored.txt", "not a jar")        // outside mods/
	w.unknownHash = sha512Hex("not on modrinth")
	w.lithHash = w.lith.SHA512
	w.files = []packFile{
		{"mods/fabric-api-2.jar", strings.ToUpper(w.fapi2.SHA512), "required"},
		{"mods/lithium.jar", w.lith.SHA512, "optional"},
		{"mods/sodium.jar", sod.SHA512, ""},
		{"mods/modmenu.jar", menu.SHA512, "unsupported"},
		{"mods/private.jar", w.unknownHash, "required"},
		{"mods/neo-only.jar", neo.SHA512, "required"},
		{"mods/ferrite.jar", fer.SHA512, "required"},
		{"mods/Manual.jar", clash.SHA512, "required"},
		{"mods/dis-2.jar", w.dis2.SHA512, ""},
		{"config/sodium-options.json", sha512Hex("options"), ""},
		{"resourcepacks/pack.zip", sha512Hex("rp"), "unsupported"},
	}
	return w
}

func (e *env) preview(id int64, index any) (int, mrpackPlanJSON, string) {
	e.t.Helper()
	rec := e.post(id, "/mrpack/preview", index)
	if rec.Code != http.StatusOK {
		return rec.Code, mrpackPlanJSON{}, errCode(e.t, rec)
	}
	return rec.Code, decode[mrpackPlanJSON](e.t, rec), ""
}

func TestMrpackPreview(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	code, plan, errc := e.preview(w.id, packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...))
	if code != http.StatusOK {
		t.Fatalf("preview: %d %s", code, errc)
	}
	want := []string{
		"fabric-api-2.jar:update>fabric-api-1.jar",
		"lithium.jar:install:optional",
		"sodium.jar:skip:client_only",
		"modmenu.jar:skip:client_only",
		"private.jar:skip:not_on_modrinth",
		"neo-only.jar:skip:incompatible",
		"ferrite.jar.disabled:unchanged",
		"Manual.jar:skip:file_exists",
		"dis-2.jar.disabled:update>dis-1.jar.disabled",
	}
	if !slices.Equal(plan.summary(), want) {
		t.Fatalf("entries =\n%v\nwant\n%v", plan.summary(), want)
	}
	if !slices.Equal(plan.Kept, []string{"Manual.jar", "Other.jar"}) || plan.IgnoredFiles != 2 {
		t.Fatalf("kept = %v, ignored = %d", plan.Kept, plan.IgnoredFiles)
	}
	if plan.Pack.Name != "Test Pack" || plan.Pack.VersionID != "2.0" || plan.GameVersion.Pack != "1.21.11" || plan.GameVersion.Server != "1.21.11" ||
		plan.Loader.Name != "fabric" || plan.Loader.Pack != "0.17.2" || plan.Loader.Server != nil || plan.VersionMismatch {
		t.Fatalf("plan = %+v", plan)
	}
	fa := plan.Entries[0]
	if fa.SHA512 != w.fapi2.SHA512 || fa.Project == nil || fa.Project.ID != "FAPI" || fa.Project.Title != "Fabric API" ||
		fa.Project.PageURL != "https://modrinth.com/mod/fabric-api" || fa.Project.IconURL == nil ||
		!strings.HasPrefix(*fa.Project.IconURL, "/api/servers/") || fa.Version == nil || fa.Version.ID != "fa2" {
		t.Fatalf("fabric api entry = %+v / %+v / %+v", fa, fa.Project, fa.Version)
	}
	if p := plan.Entries[4]; p.Project != nil || p.Version != nil || p.File != "private.jar" {
		t.Fatalf("unknown entry = %+v", p)
	}
	// Nothing was written, no job, no audit.
	if e.job(w.id) != nil || len(e.rec.actions()) != 0 {
		t.Fatalf("preview changed something: %v", e.rec.actions())
	}
	if n := e.up.count("GET /v2/projects"); n != 1 {
		t.Fatalf("project batch requests = %d", n)
	}
}

func TestMrpackImport(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	rec := e.post(w.id, "/mrpack/import", map[string]any{"index": packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...), "exclude": []string{}, "accept_mismatch": false})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if j := decode[jobJSON](t, rec); j.Kind != "mrpack" || j.State != "running" {
		t.Fatalf("job = %+v", j)
	}
	j := e.waitJob(w.id)
	if j.State != "done" || !slices.Equal(steps(j), []string{"fabric-api-2.jar:done", "lithium.jar:done", "dis-2.jar.disabled:done"}) {
		t.Fatalf("job = %s %v", j.State, steps(j))
	}
	want := []string{"Manual.jar", "Other.jar", "dis-2.jar.disabled", "fabric-api-2.jar", "ferrite.jar.disabled", "lithium.jar"}
	if got := e.files.names(w.id, "mods"); !slices.Equal(got, want) {
		t.Fatalf("mods = %v", got)
	}
	var rows []string
	for _, r := range e.rows(w.id) {
		rows = append(rows, r.Filename+":"+r.ProjectID+":"+r.VersionID)
	}
	if !slices.Equal(rows, []string{"dis-2.jar:DIS:di2", "fabric-api-2.jar:FAPI:fa2", "lithium.jar:LITH:li1"}) {
		t.Fatalf("rows = %v", rows)
	}
	es, actors := e.rec.all()
	if len(es) != 4 {
		t.Fatalf("audit = %v", e.rec.actions())
	}
	for i, x := range es[:3] {
		if x.Details["via"] != "mrpack" || x.Result != audit.ResultSuccess || actors[i] != "root" {
			t.Fatalf("entry %d = %+v (%s)", i, x, actors[i])
		}
	}
	if es[0].Action != audit.ActionPluginUpdate || es[0].Details["from"] != "fa1" || es[0].Details["to"] != "fa2" ||
		es[1].Action != audit.ActionPluginInstall || es[1].Details["file"] != "lithium.jar" ||
		es[2].Action != audit.ActionPluginUpdate || es[2].Details["file"] != "dis-2.jar.disabled" {
		t.Fatalf("jar entries = %+v", es[:3])
	}
	last := es[3]
	if last.Action != audit.ActionPluginMrpackImport || last.Result != audit.ResultSuccess || last.Target != "server:"+strconv.FormatInt(w.id, 10) ||
		last.Details["pack"] != "Test Pack" || last.Details["version"] != "2.0" || last.Details["installed"] != 1 ||
		last.Details["updated"] != 2 || last.Details["unchanged"] != 1 || last.Details["skipped"] != 5 || last.Details["failed"] != 0 {
		t.Fatalf("import entry = %+v", last)
	}
	// The import is done: a second preview shows everything unchanged.
	_, plan, _ := e.preview(w.id, packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...))
	if plan.Entries[0].Action != "unchanged" || plan.Entries[1].Action != "unchanged" || plan.Entries[8].Action != "unchanged" {
		t.Fatalf("after import: %v", plan.summary())
	}
	rec = e.post(w.id, "/mrpack/import", map[string]any{"index": packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...), "exclude": []string{}, "accept_mismatch": false})
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("nothing to do: %d %s", rec.Code, rec.Body)
	}
}

func TestMrpackImportExclude(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	index := packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...)
	// Only optional entries may be excluded.
	for _, x := range []string{w.fapi2.SHA512, w.unknownHash, sha512Hex("not in the pack"), "abc"} {
		rec := e.post(w.id, "/mrpack/import", map[string]any{"index": index, "exclude": []string{x}, "accept_mismatch": false})
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Fatalf("exclude %s: %d %s", x[:3], rec.Code, rec.Body)
		}
	}
	rec := e.post(w.id, "/mrpack/import", map[string]any{"index": index, "exclude": []string{strings.ToUpper(w.lithHash)}, "accept_mismatch": false})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	j := e.waitJob(w.id)
	if !slices.Equal(steps(j), []string{"fabric-api-2.jar:done", "dis-2.jar.disabled:done"}) {
		t.Fatalf("steps = %v", steps(j))
	}
	if _, ok := e.files.get(w.id, "mods/lithium.jar"); ok {
		t.Fatal("excluded mod installed")
	}
	es, _ := e.rec.all()
	if last := es[len(es)-1]; last.Details["skipped"] != 6 {
		t.Fatalf("import entry = %+v", last)
	}
}

func TestMrpackFailedStep(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	e.up.serveInstead(w.lith, "LITHIUM") // same size, other hash
	rec := e.post(w.id, "/mrpack/import", map[string]any{"index": packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...), "exclude": []string{}, "accept_mismatch": false})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	j := e.waitJob(w.id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{"fabric-api-2.jar:done", "lithium.jar:failed:hash_mismatch", "dis-2.jar.disabled:done"}) {
		t.Fatalf("job = %s %v", j.State, steps(j))
	}
	es, _ := e.rec.all()
	last := es[len(es)-1]
	if last.Action != audit.ActionPluginMrpackImport || last.Result != audit.ResultFailure || last.Details["failed"] != 1 ||
		last.Details["installed"] != 0 || last.Details["updated"] != 2 {
		t.Fatalf("import entry = %+v", last)
	}
	if es[1].Action != audit.ActionPluginInstall || es[1].Result != audit.ResultFailure || es[1].Details["via"] != "mrpack" {
		t.Fatalf("failed jar entry = %+v", es[1])
	}
}

func TestMrpackVersionMismatch(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	index := packIndex("1.21.10", "fabric-loader", "0.17.2", w.files...)
	_, plan, _ := e.preview(w.id, index)
	if !plan.VersionMismatch || plan.GameVersion.Pack != "1.21.10" || plan.GameVersion.Server != "1.21.11" {
		t.Fatalf("plan = %+v", plan)
	}
	rec := e.post(w.id, "/mrpack/import", map[string]any{"index": index, "exclude": []string{}, "accept_mismatch": false})
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeMrpackVersionMismatch {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	if e.job(w.id) != nil {
		t.Fatal("job started")
	}
	rec = e.post(w.id, "/mrpack/import", map[string]any{"index": index, "exclude": []string{}, "accept_mismatch": true})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("accepted import: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(w.id)
}

func TestMrpackLoaderVersion(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	mustExec(t, e.db, `UPDATE instances SET loader_version = '0.16.0' WHERE server_id = ?`, w.id)
	_, plan, _ := e.preview(w.id, packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...))
	if !plan.VersionMismatch || plan.Loader.Server == nil || *plan.Loader.Server != "0.16.0" || plan.Loader.Pack != "0.17.2" {
		t.Fatalf("plan = %+v", plan.Loader)
	}
	_, plan, _ = e.preview(w.id, packIndex("1.21.11", "fabric-loader", "0.16.0", w.files...))
	if plan.VersionMismatch {
		t.Fatal("same loader version is a mismatch")
	}
	// LATEST resolves over the catalog.
	mustExec(t, e.db, `UPDATE instances SET version = 'LATEST', loader_version = NULL WHERE server_id = ?`, w.id)
	_, plan, _ = e.preview(w.id, packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...))
	if plan.VersionMismatch || plan.GameVersion.Server != "1.21.11" {
		t.Fatalf("LATEST: %+v", plan.GameVersion)
	}
}

func TestMrpackRejects(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	paper := e.paper("survival")
	neo := createInstance(t, e.db, "neo", instancespec.TypeNeoForge, "1.21.11")
	good := packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...)
	cases := []struct {
		name   string
		id     int64
		index  any
		status int
		code   string
	}{
		{"paper", paper, good, http.StatusConflict, httpx.CodePluginsUnsupported},
		{"neoforge pack on fabric", w.id, packIndex("1.21.11", "neoforge", "21.1.1", w.files...), http.StatusConflict, httpx.CodeMrpackIncompatible},
		{"forge pack", w.id, packIndex("1.21.11", "forge", "52.0.1", w.files...), http.StatusConflict, httpx.CodeMrpackIncompatible},
		{"quilt pack", w.id, packIndex("1.21.11", "quilt-loader", "0.29.0", w.files...), http.StatusConflict, httpx.CodeMrpackIncompatible},
		{"no loader", w.id, packIndex("1.21.11", "", "", w.files...), http.StatusConflict, httpx.CodeMrpackIncompatible},
		{"fabric pack on neoforge", neo, good, http.StatusConflict, httpx.CodeMrpackIncompatible},
		{"path attack", w.id, packIndex("1.21.11", "fabric-loader", "0.17.2", packFile{"mods/../../etc/x.jar", w.lithHash, ""}), http.StatusBadRequest, httpx.CodeValidation},
		{"not an index", w.id, map[string]any{"hello": "world"}, http.StatusBadRequest, httpx.CodeValidation},
		{"unknown server", 999, good, http.StatusNotFound, httpx.CodeNotFound},
	}
	for _, c := range cases {
		for _, route := range []string{"preview", "import"} {
			body := c.index
			if route == "import" {
				body = map[string]any{"index": c.index, "exclude": []string{}, "accept_mismatch": true}
			}
			rec := e.post(c.id, "/mrpack/"+route, body)
			if rec.Code != c.status || errCode(t, rec) != c.code {
				t.Errorf("%s %s: %d %s", c.name, route, rec.Code, rec.Body)
			}
		}
	}
	for name, body := range map[string]any{
		"no index":      map[string]any{"exclude": []string{}, "accept_mismatch": false},
		"null index":    map[string]any{"index": nil},
		"unknown field": map[string]any{"index": good, "extra": 1},
	} {
		rec := e.post(w.id, "/mrpack/import", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	big := packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...)
	big["padding"] = strings.Repeat("x", 1<<20)
	rec := e.post(w.id, "/mrpack/preview", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("large body: %d %s", rec.Code, rec.Body)
	}
	if len(e.rec.actions()) != 0 || e.job(w.id) != nil {
		t.Fatalf("a rejected request changed something: %v", e.rec.actions())
	}
}

func TestMrpackPermissionAndUpstream(t *testing.T) {
	e := newEnv(t)
	w := newMrpackWorld(e)
	index := packIndex("1.21.11", "fabric-loader", "0.17.2", w.files...)
	uid := storetest.CreateUser(t, e.db, "helper", "user")
	grant(t, e.db, uid, w.id, "server.view", "server.files")
	p := userP(uid, "helper")
	if rec := e.do(http.MethodPost, srvPath(w.id, "/mrpack/preview"), index, &p); rec.Code != http.StatusForbidden {
		t.Fatalf("without server.plugins: %d", rec.Code)
	}
	e.up.setDown(true)
	if code, _, errc := e.preview(w.id, index); code != http.StatusBadGateway || errc != httpx.CodePluginSourceUnavailable {
		t.Fatalf("upstream down: %d %s", code, errc)
	}
	e.up.setDown(false)
	block := make(chan struct{})
	e.up.setBlock(block)
	rec := e.post(w.id, "/mrpack/import", map[string]any{"index": index, "exclude": []string{}, "accept_mismatch": false})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	rec = e.post(w.id, "/mrpack/import", map[string]any{"index": index, "exclude": []string{}, "accept_mismatch": false})
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("second import while running: %d %s", rec.Code, rec.Body)
	}
	close(block)
	e.waitJob(w.id)
}
