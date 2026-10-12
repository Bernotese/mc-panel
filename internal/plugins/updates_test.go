package plugins_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

type updatesJSON struct {
	Updates []struct {
		File string         `json:"file"`
		From versionRefJSON `json:"from"`
		To   versionRefJSON `json:"to"`
	} `json:"updates"`
	CheckedAt int64 `json:"checked_at"`
}

func (e *env) updates(id int64, query string) updatesJSON {
	e.t.Helper()
	return decode[updatesJSON](e.t, e.get(srvPath(id, "/updates"+query)))
}

func (u updatesJSON) summary() []string {
	var out []string
	for _, x := range u.Updates {
		out = append(out, x.File+":"+x.From.ID+">"+x.To.ID+":"+x.To.Channel)
	}
	return out
}

// lpWorld registers LuckPerms with two releases and puts the old jar (uploaded by hand) on disk.
func lpWorld(e *env, id int64, file string) (oldJar, newJar *upFile) {
	newJar = e.up.jar("LuckPerms-Bukkit-5.5.71.jar", "luckperms 5.5.71")
	oldJar = e.up.jar("LuckPerms-Bukkit-5.5.53.jar", "luckperms 5.5.53")
	e.up.modrinth("Vebnzrzj", "luckperms", "LuckPerms", "required",
		mv{ID: "b0mk8uS6", Number: "v5.5.71-bukkit", File: newJar}, mv{ID: "MBSY8toc", Number: "v5.5.53-bukkit", File: oldJar})
	e.files.put(id, "plugins/"+file, oldJar.content)
	return oldJar, newJar
}

func TestUpdatesModrinth(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LuckPerms-old.jar")
	b1, b2 := e.up.jar("Beta-1.jar", "beta 1"), e.up.jar("Beta-2.jar", "beta 2")
	e.up.modrinth("BETA", "beta", "Beta", "required", mv{ID: "b2", Type: "beta", File: b2}, mv{ID: "b1", Type: "beta", File: b1})
	e.files.put(id, "plugins/Beta-1.jar", b1.content)
	e.seedRow(id, "Beta-1.jar", b1.content, "modrinth", "BETA", "beta", "Beta", "b1", "b1", "beta")
	a1, a2 := e.up.jar("Alpha-1.jar", "alpha 1"), e.up.jar("Alpha-2.jar", "alpha 2")
	e.up.modrinth("ALPHA", "alpha", "Alpha", "required", mv{ID: "a2", Type: "alpha", File: a2}, mv{ID: "a1", Type: "alpha", File: a1})
	e.files.put(id, "plugins/Alpha-1.jar", a1.content)
	e.files.put(id, "plugins/Manual.jar", "manual")
	got := e.updates(id, "")
	want := []string{"Alpha-1.jar:a1>a2:alpha", "Beta-1.jar:b1>b2:beta", "LuckPerms-old.jar:MBSY8toc>b0mk8uS6:release"}
	if !slices.Equal(got.summary(), want) || got.CheckedAt != t0.UnixMilli() {
		t.Fatalf("updates = %v (%d)", got.summary(), got.CheckedAt)
	}
	types := e.up.types()
	if !slices.ContainsFunc(types, func(ts []string) bool { return slices.Equal(ts, []string{"release"}) }) ||
		!slices.ContainsFunc(types, func(ts []string) bool { return slices.Equal(ts, []string{"release", "beta"}) }) ||
		!slices.ContainsFunc(types, func(ts []string) bool { return slices.Equal(ts, []string{"release", "beta", "alpha"}) }) {
		t.Fatalf("version_types = %v", types)
	}
}

func TestUpdatesHangar(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.hangar(31, "ViaVersion", "ViaVersion",
		hv{Name: "5.12.2-SNAPSHOT+657", Channel: "Snapshot", File: e.up.jar("VV-snap.jar", "vv snap")},
		hv{Name: "5.12.1", File: e.up.jar("ViaVersion-5.12.1.jar", "vv 5.12.1")},
		hv{Name: "5.12.0", File: e.up.jar("ViaVersion-5.12.0.jar", "vv 5.12.0")})
	e.files.put(id, "plugins/ViaVersion-5.12.0.jar", "vv 5.12.0")
	e.seedRow(id, "ViaVersion-5.12.0.jar", "vv 5.12.0", "hangar", "31", "ViaVersion", "ViaVersion", "5.12.0", "5.12.0", "release")
	e.up.hangar(40, "Dev", "Nightly",
		hv{Name: "2.1-SNAPSHOT", Channel: "Snapshot", File: e.up.jar("N-2.1.jar", "n 2.1")},
		hv{Name: "2.0-SNAPSHOT", Channel: "Snapshot", File: e.up.jar("N-2.0.jar", "n 2.0")})
	e.files.put(id, "plugins/N-2.0.jar", "n 2.0")
	e.seedRow(id, "N-2.0.jar", "n 2.0", "hangar", "40", "Nightly", "Nightly", "2.0-SNAPSHOT", "2.0-SNAPSHOT", "beta")
	// the installed version is newer than the newest release: no "update" to an older one
	e.up.hangar(50, "Dev", "Ahead",
		hv{Name: "3.1", Channel: "Snapshot", Published: t0},
		hv{Name: "3.0", Published: t0.Add(-48 * time.Hour), File: e.up.jar("Ahead-3.0.jar", "a 3.0")})
	e.files.put(id, "plugins/Ahead.jar", "ahead 3.1")
	e.seedRow(id, "Ahead.jar", "ahead 3.1", "hangar", "50", "Ahead", "Ahead", "3.1", "3.1", "release")
	got := e.updates(id, "")
	want := []string{"N-2.0.jar:2.0-SNAPSHOT>2.1-SNAPSHOT:beta", "ViaVersion-5.12.0.jar:5.12.0>5.12.1:release"}
	if !slices.Equal(got.summary(), want) {
		t.Fatalf("updates = %v", got.summary())
	}
}

func TestUpdatesCacheAndRefresh(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LuckPerms-old.jar")
	key := "POST /v2/version_files/update"
	e.updates(id, "")
	e.updates(id, "")
	e.updates(id, "?refresh=yes")
	if n := e.up.count(key); n != 1 {
		t.Fatalf("checks = %d, want 1 (cached)", n)
	}
	e.updates(id, "?refresh=true")
	e.updates(id, "?refresh=1")
	if n := e.up.count(key); n != 3 {
		t.Fatalf("checks = %d after two refreshes", n)
	}
	e.clk.Advance(time.Hour)
	e.updates(id, "")
	if n := e.up.count(key); n != 4 {
		t.Fatalf("checks = %d after an hour", n)
	}
	// refresh counts against the lookup limit (the hour refilled the 20 tokens), the plain check does not
	a := e.admin()
	for range 20 {
		e.updates(id, "?refresh=1")
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/updates?refresh=1"), nil, &a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("refresh beyond the limit: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("plain check limited: %d", rec.Code)
	}
	e.up.setDown(true)
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("a cached check needs no upstream: %d", rec.Code)
	}
	e.clk.Advance(time.Hour)
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodePluginSourceUnavailable {
		t.Fatalf("upstream down: %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateJob(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	_, newJar := lpWorld(e, id, "LuckPerms-old.jar")
	if got := e.updates(id, "").summary(); len(got) != 1 {
		t.Fatalf("updates = %v", got)
	}
	rec := e.post(id, "/update", map[string]any{"files": []string{}})
	if rec.Code != http.StatusAccepted || decode[jobJSON](t, rec).Kind != "update" {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	j := e.waitJob(id)
	if j.State != "done" || !slices.Equal(steps(j), []string{newJar.Name + ":done"}) || j.Steps[0].Title != "LuckPerms" {
		t.Fatalf("job = %+v", j)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{newJar.Name}) {
		t.Fatalf("files = %v", names)
	}
	rows := e.rows(id)
	if len(rows) != 1 || rows[0].Filename != newJar.Name || rows[0].VersionID != "b0mk8uS6" || rows[0].Sha512 != newJar.SHA512 {
		t.Fatalf("rows = %+v", rows)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginUpdate || entries[0].Result != audit.ResultSuccess || actors[0] != "root" ||
		entries[0].Details["file"] != newJar.Name || entries[0].Details["from"] != "v5.5.53-bukkit" || entries[0].Details["to"] != "v5.5.71-bukkit" {
		t.Fatalf("audit = %+v by %v", entries, actors)
	}
	// the job dropped the cached check
	if got := e.updates(id, "").summary(); len(got) != 0 {
		t.Fatalf("updates after the job = %v", got)
	}
}

func TestUpdateDisabledStaysDisabled(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	_, newJar := lpWorld(e, id, "LP.jar.disabled")
	e.updates(id, "")
	if rec := e.post(id, "/update", map[string]any{"files": []string{"LP.jar.disabled"}}); rec.Code != http.StatusAccepted {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(id)
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{newJar.Name + ".disabled"}) {
		t.Fatalf("files = %v", names)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != newJar.Name {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUpdateSameNameOverwrite(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	t1, t2 := e.up.jar("Tool.jar", "tool 1"), e.up.jar("Tool.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool.jar", t1.content)
	e.seedRow(id, "Tool.jar", t1.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	e.post(id, "/update", map[string]any{"files": []string{"Tool.jar"}})
	e.waitJob(id)
	if c, _ := e.files.get(id, "plugins/Tool.jar"); c != "tool 2" {
		t.Fatalf("content = %q", c)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].VersionID != "t2" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #2: an update onto a name another jar already uses fails only that step.
func TestUpdateTargetNameTaken(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	a1, a2 := e.up.jar("A-1.jar", "a 1"), e.up.jar("Taken.jar", "a 2")
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a2", File: a2}, mv{ID: "a1", File: a1})
	e.files.put(id, "plugins/A-1.jar", a1.content)
	e.files.put(id, "plugins/taken.JAR", "manual jar")
	_, lpNew := lpWorld(e, id, "LP-old.jar")
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{"Taken.jar:failed:file_exists", lpNew.Name + ":done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"A-1.jar", lpNew.Name, "taken.JAR"}) {
		t.Fatalf("files = %v", names)
	}
}

func TestUpdateNewDependency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lib := e.up.jar("Lib.jar", "lib")
	e.up.modrinth("LIB", "lib", "Lib", "required", mv{ID: "l1", File: lib})
	t1, t2 := e.up.jar("Tool-1.jar", "tool 1"), e.up.jar("Tool-2.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2, Deps: []map[string]any{mdep("LIB", "")}}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool-1.jar", t1.content)
	e.post(id, "/update", map[string]any{"files": []string{"Tool-1.jar"}})
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{"Lib.jar:done", "Tool-2.jar:done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.update:success"}) {
		t.Fatalf("audit = %v", got)
	}
}

func TestUpdateDependencyIncompatible(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	t1, t2 := e.up.jar("Tool-1.jar", "tool 1"), e.up.jar("Tool-2.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2, Deps: []map[string]any{mdep("NOWHERE", "")}}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool-1.jar", t1.content)
	_, lpNew := lpWorld(e, id, "LP-old.jar")
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{lpNew.Name + ":done", "Tool-2.jar:failed:download_failed"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if c, ok := e.files.get(id, "plugins/Tool-1.jar"); !ok || c != t1.content {
		t.Fatal("the old jar must stay when its update fails")
	}
}

// Review Focus #4: the CDN fails after the first update; the next update fails, the first stays.
func TestUpdateSourceUnavailable(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	a1, a2 := e.up.jar("A-1.jar", "a 1"), e.up.jar("A-2.jar", "a 2")
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a2", File: a2}, mv{ID: "a1", File: a1})
	e.files.put(id, "plugins/A-1.jar", a1.content)
	_, lpNew := lpWorld(e, id, "LP-old.jar")
	e.files.setOnUpload(func(int64, string) { e.up.setCDNStatus(http.StatusServiceUnavailable) })
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{"A-2.jar:done", lpNew.Name + ":failed:source_unavailable"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"A-2.jar", "LP-old.jar"}) {
		t.Fatalf("files = %v", names)
	}
}

func TestUpdateValidation(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for _, files := range [][]string{{"../x.jar"}, {"a/b.jar"}, {"x.zip"}, {".mcpanel-x.jar"}, {"unknown.jar"}, {}} {
		if rec := e.post(id, "/update", map[string]any{"files": files}); rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%v: %d %s", files, rec.Code, rec.Body)
		}
	}
	// a fresh server (the empty check above is cached for an hour)
	busy := e.paper("busy")
	lpWorld(e, busy, "LP-old.jar")
	block := make(chan struct{})
	e.up.setBlock(block)
	if rec := e.post(busy, "/update", map[string]any{"files": []string{}}); rec.Code != http.StatusAccepted {
		t.Fatalf("first update: %d %s", rec.Code, rec.Body)
	}
	if rec := e.post(busy, "/update", map[string]any{"files": []string{}}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("second update: %d %s", rec.Code, rec.Body)
	}
	close(block)
	e.waitJob(busy)
}
