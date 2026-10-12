package plugins_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// K1: the old jar of an update onto a new name cannot be deleted: the new jar is taken back,
// so the server never loads both versions.
func TestUpdateOldJarNotDeletable(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	oldJar, newJar := lpWorld(e, id, "LP-old.jar")
	e.updates(id, "")
	e.files.setCallErr(fmt.Sprintf("delete %d plugins/LP-old.jar", id), fmt.Errorf("%w: busy disk", filesapi.ErrUnavailable))
	e.post(id, "/update", map[string]any{"files": []string{"LP-old.jar"}})
	j := e.waitJob(id)
	if j.State != "failed" || j.Steps[0].State != "failed" || !strings.Contains(j.Steps[0].Error, "LP-old.jar") {
		t.Fatalf("job = %+v", j)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"LP-old.jar"}) {
		t.Fatalf("files = %v, want only the old jar", names)
	}
	if c, _ := e.files.get(id, "plugins/LP-old.jar"); c != oldJar.content {
		t.Fatalf("old jar = %q", c)
	}
	if rows := e.rows(id); len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
	// neither can be deleted: the step names both
	e.files.setCallErr(fmt.Sprintf("delete %d plugins/%s", id, newJar.Name), fmt.Errorf("%w: busy disk", filesapi.ErrUnavailable))
	e.updates(id, "?refresh=1")
	e.post(id, "/update", map[string]any{"files": []string{"LP-old.jar"}})
	j = e.waitJob(id)
	if j.Steps[0].State != "failed" || !strings.Contains(j.Steps[0].Error, "LP-old.jar") || !strings.Contains(j.Steps[0].Error, newJar.Name) {
		t.Fatalf("job = %+v", j)
	}
}

// K2: a jar that changed since the update check fails its step with file_exists.
func TestUpdateJarChangedSinceCheck(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LP-old.jar")
	e.updates(id, "")
	e.files.put(id, "plugins/LP-old.jar", "replaced by hand")
	e.post(id, "/update", map[string]any{"files": []string{"LP-old.jar"}})
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{"LP-old.jar:failed:file_exists"}) && !slices.Equal(steps(j), []string{"LuckPerms-Bukkit-5.5.71.jar:failed:file_exists"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if c, _ := e.files.get(id, "plugins/LP-old.jar"); c != "replaced by hand" {
		t.Fatalf("LP-old.jar = %q", c)
	}
}

// sharedDepWorld registers Lib and two tools whose new versions both require it.
func sharedDepWorld(e *env, id int64) (lib *upFile) {
	lib = e.up.jar("Lib.jar", "lib")
	e.up.modrinth("LIB", "lib", "Lib", "required", mv{ID: "l1", File: lib})
	for _, n := range []string{"A", "B"} {
		v1, v2 := e.up.jar(n+"-1.jar", n+" 1"), e.up.jar(n+"-2.jar", n+" 2")
		e.up.modrinth(n, strings.ToLower(n), n, "required", mv{ID: n + "2", File: v2, Deps: []map[string]any{mdep("LIB", "")}}, mv{ID: n + "1", File: v1})
		e.files.put(id, "plugins/"+n+"-1.jar", v1.content)
	}
	return lib
}

// K3: a dependency shared by two updates belongs to both groups: if it fails, neither update
// runs without it; if it succeeds, the second group does not download it again.
func TestUpdateSharedDependency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lib := sharedDepWorld(e, id)
	e.up.serveInstead(lib, "xxx") // hash mismatch
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	want := []string{"Lib.jar:failed:hash_mismatch", "A-2.jar:skipped", "Lib.jar:failed:hash_mismatch", "B-2.jar:skipped"}
	if !slices.Equal(steps(j), want) {
		t.Fatalf("failing lib: job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"A-1.jar", "B-1.jar"}) {
		t.Fatalf("files = %v", names)
	}
	e.up.serveInstead(lib, lib.content)
	before := e.up.count("CDN " + strings.TrimPrefix(lib.URL, e.up.cdn.URL))
	e.updates(id, "?refresh=1")
	e.post(id, "/update", map[string]any{"files": []string{}})
	j = e.waitJob(id)
	want = []string{"Lib.jar:done", "A-2.jar:done", "Lib.jar:done", "B-2.jar:done"}
	if !slices.Equal(steps(j), want) {
		t.Fatalf("working lib: job = %v", steps(j))
	}
	if n := e.up.count("CDN "+strings.TrimPrefix(lib.URL, e.up.cdn.URL)) - before; n != 1 {
		t.Fatalf("Lib downloaded %d times", n)
	}
	n := 0
	for _, a := range e.rec.actions() {
		if a == "plugin.install:success" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("Lib audited %d times: %v", n, e.rec.actions())
	}
}

// K5: a file named twice in an update request is updated once.
func TestUpdateDedupesFiles(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	_, newJar := lpWorld(e, id, "LP-old.jar")
	e.post(id, "/update", map[string]any{"files": []string{"LP-old.jar", "LP-old.jar"}})
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{newJar.Name + ":done"}) {
		t.Fatalf("job = %v", steps(j))
	}
}

// K7: an upload the service refuses as busy (all upload slots taken) is retried.
func TestInstallRetriesBusyService(t *testing.T) {
	e := newEnv(t)
	plugins.SetBusyBackoff(e.h, time.Millisecond)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	busy := fmt.Errorf("%w: all upload slots taken", filesapi.ErrBusy)
	e.files.queueErrs("upload", busy, busy)
	e.install(id, "modrinth", "chunky")
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.install:success"}) {
		t.Fatalf("audit = %v", got)
	}
	e2 := newEnv(t)
	plugins.SetBusyBackoff(e2.h, time.Millisecond)
	id2 := e2.fabric("modded")
	_, fapi2 := fabricWorld(e2)
	e2.files.queueErrs("upload", busy, busy, busy, busy)
	e2.install(id2, "modrinth", "chunky")
	if j := e2.waitJob(id2); j.Steps[0].State != "failed" || *j.Steps[0].ErrorCode != "files_unavailable" || j.Steps[0].File != fapi2.Name {
		t.Fatalf("still busy: job = %+v", j)
	}
}

// K9: a version change through install records the old version like an update.
func TestInstallVersionChangeAuditsFrom(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	v1, v2 := e.up.jar("Tool-1.jar", "tool 1"), e.up.jar("Tool-2.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", Number: "2.0", File: v2}, mv{ID: "t1", Number: "1.0", File: v1})
	e.files.put(id, "plugins/Tool-1.jar", v1.content)
	e.seedRow(id, "Tool-1.jar", v1.content, "modrinth", "TOOL", "tool", "Tool", "t1", "1.0", "release")
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "TOOL", "version": "t2"}); rec.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(id)
	entries, _ := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginInstall || entries[0].Details["from"] != "1.0" || entries[0].Details["version"] != "t2" {
		t.Fatalf("audit = %+v", entries)
	}
}

// K11: a user with server.plugins (no admin) starts a job; every entry names that user.
func TestJobAuditActorIsUser(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	grant(t, e.db, uid, id, string(rbac.PermServerPlugins))
	p := userP(uid, "alice")
	rec := e.do(http.MethodPost, srvPath(id, "/install"), map[string]any{"source": "modrinth", "project": "chunky"}, &p)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("install as alice: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(id)
	entries, actors := e.rec.all()
	if len(entries) != 2 || !slices.Equal(actors, []string{"alice", "alice"}) {
		t.Fatalf("audit = %+v by %v", entries, actors)
	}
}
