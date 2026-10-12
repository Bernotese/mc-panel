package plugins_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/plugins"
)

// Review Focus #2/#3: a jar the user replaces in the files tab while an update job runs is
// neither overwritten (same name) nor deleted (new name).
func TestUpdateJarSwappedDuringJob(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	a1, a2 := e.up.jar("A-1.jar", "a 1"), e.up.jar("A-2.jar", "a 2")
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a2", File: a2}, mv{ID: "a1", File: a1})
	b1, b2 := e.up.jar("B-1.jar", "b 1"), e.up.jar("B-2.jar", "b 2")
	e.up.modrinth("B", "b", "B", "required", mv{ID: "b2", File: b2}, mv{ID: "b1", File: b1})
	t1, t2 := e.up.jar("Tool.jar", "tool 1"), e.up.jar("Tool.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2}, mv{ID: "t1", File: t1})
	for _, f := range []*upFile{a1, b1, t1} {
		e.files.put(id, "plugins/"+f.Name, f.content)
	}
	if got := e.updates(id, "").summary(); len(got) != 3 {
		t.Fatalf("updates = %v", got)
	}
	e.files.setOnUpload(func(sid int64, p string) {
		if strings.HasSuffix(p, "/A-2.jar") {
			e.files.put(sid, "plugins/B-1.jar", "my own b build")
			e.files.put(sid, "plugins/Tool.jar", "my own tool build")
		}
	})
	if rec := e.post(id, "/update", map[string]any{"files": []string{}}); rec.Code != http.StatusAccepted {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	j := e.waitJob(id)
	want := []string{"A-2.jar:done", "B-2.jar:failed:file_exists", "Tool.jar:failed:file_exists"}
	if j.State != "failed" || !slices.Equal(steps(j), want) {
		t.Fatalf("job = %v", steps(j))
	}
	if c, _ := e.files.get(id, "plugins/B-1.jar"); c != "my own b build" {
		t.Fatalf("B-1.jar = %q", c)
	}
	if c, _ := e.files.get(id, "plugins/Tool.jar"); c != "my own tool build" {
		t.Fatalf("Tool.jar = %q", c)
	}
	if _, ok := e.files.get(id, "plugins/B-2.jar"); ok {
		t.Fatal("B-2.jar was uploaded")
	}
}

// A project removed upstream (Modrinth 404, Hangar 404) is no source failure: the inventory
// shows no source error and the updates check still works for the other jars.
func TestProjectGoneUpstream(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LP-old.jar")
	e.files.put(id, "plugins/Gone.jar", "gone")
	e.seedRow(id, "Gone.jar", "gone", "modrinth", "GONE", "gone", "Gone", "g1", "g1", "release")
	e.files.put(id, "plugins/HGone.jar", "hangar gone")
	e.seedRow(id, "HGone.jar", "hangar gone", "hangar", "99", "HGone", "HGone", "1.0", "1.0", "release")
	inv := e.inventory(id)
	if len(inv.SourceErrors) != 0 {
		t.Fatalf("source_errors = %v", inv.SourceErrors)
	}
	if g := jarNamed(t, inv, "Gone.jar"); g.Source != "modrinth" || g.Project == nil || g.Project.Title != "Gone" {
		t.Fatalf("Gone.jar = %+v", g)
	}
	if got := e.updates(id, "").summary(); len(got) != 1 || !strings.HasPrefix(got[0], "LP-old.jar:") {
		t.Fatalf("updates = %v", got)
	}
}

// The cached updates check belongs to one game version: after a version change of the
// server the next check asks again.
func TestUpdatesFollowGameVersion(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LP-old.jar")
	if got := e.updates(id, "").summary(); len(got) != 1 {
		t.Fatalf("updates = %v", got)
	}
	mustExec(t, e.db, `UPDATE instances SET version = '26.3' WHERE server_id = ?`, id)
	if got := e.updates(id, "").summary(); len(got) != 0 {
		t.Fatalf("updates for 26.3 = %v (stale cache)", got)
	}
}

// hookJars runs hook (once) at the start of a jars listing.
type hookJars struct {
	filesapi.Client
	mu   *sync.Mutex
	hook *func()
}

func (h hookJars) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	h.mu.Lock()
	f := *h.hook
	*h.hook = nil
	h.mu.Unlock()
	if f != nil {
		f()
	}
	return h.Client.Jars(ctx, id, dir)
}

// Review Focus #3: a job that starts and ends between an inventory's row read and its jar
// listing (same-name update) keeps its fresh row.
func TestInventoryKeepsRowOfJobBetweenReads(t *testing.T) {
	var (
		mu   sync.Mutex
		hook func()
	)
	e := newEnvWith(t, func(d *plugins.Deps) { d.Files = hookJars{Client: d.Files, mu: &mu, hook: &hook} })
	id := e.paper("survival")
	t1, t2 := e.up.jar("Tool.jar", "tool 1"), e.up.jar("Tool.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool.jar", t1.content)
	e.seedRow(id, "Tool.jar", t1.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	e.updates(id, "")
	mu.Lock()
	hook = func() {
		if rec := e.post(id, "/update", map[string]any{"files": []string{"Tool.jar"}}); rec.Code != http.StatusAccepted {
			t.Errorf("update: %d %s", rec.Code, rec.Body)
			return
		}
		e.waitJob(id)
	}
	mu.Unlock()
	e.inventory(id)
	if rows := e.rows(id); len(rows) != 1 || rows[0].VersionID != "t2" || rows[0].Sha512 != t2.SHA512 {
		t.Fatalf("rows = %+v", rows)
	}
}
