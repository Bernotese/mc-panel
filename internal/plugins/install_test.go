package plugins_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/store"
)

type stepJSON struct {
	Title     string  `json:"title"`
	File      string  `json:"file"`
	State     string  `json:"state"`
	ErrorCode *string `json:"error_code"`
	Error     string  `json:"error"`
}

type jobJSON struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	Steps      []stepJSON `json:"steps"`
	StartedAt  int64      `json:"started_at"`
	FinishedAt int64      `json:"finished_at"`
}

func (e *env) post(id int64, suffix string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	a := e.admin()
	return e.do(http.MethodPost, srvPath(id, suffix), body, &a)
}

// install starts an install job and fails unless it was accepted.
func (e *env) install(id int64, src, project string) jobJSON {
	e.t.Helper()
	rec := e.post(id, "/install", map[string]any{"source": src, "project": project})
	if rec.Code != http.StatusAccepted {
		e.t.Fatalf("install %s: %d %s", project, rec.Code, rec.Body)
	}
	return decode[jobJSON](e.t, rec)
}

func (e *env) job(id int64) *jobJSON {
	e.t.Helper()
	return decode[struct {
		Job *jobJSON `json:"job"`
	}](e.t, e.get(srvPath(id, "/jobs"))).Job
}

// waitJob waits for all jobs and returns the server's last one.
func (e *env) waitJob(id int64) jobJSON {
	e.t.Helper()
	plugins.WaitJobs(e.h)
	j := e.job(id)
	if j == nil {
		e.t.Fatal("no job")
	}
	return *j
}

// steps renders the steps as "file:state[:code]".
func steps(j jobJSON) []string {
	out := make([]string, 0, len(j.Steps))
	for _, s := range j.Steps {
		v := s.File + ":" + s.State
		if s.ErrorCode != nil {
			v += ":" + *s.ErrorCode
		}
		out = append(out, v)
	}
	return out
}

func TestInstallWithDependency(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Hour).UnixMilli()})
	started := e.install(id, "modrinth", "chunky")
	if started.Kind != "install" || started.State != "running" || started.StartedAt != t0.UnixMilli() || len(started.Steps) != 2 ||
		started.Steps[0].State != "pending" || started.Steps[0].Title != "Fabric API" {
		t.Fatalf("started = %+v", started)
	}
	j := e.waitJob(id)
	if j.ID != started.ID || j.State != "done" || j.FinishedAt == 0 ||
		!slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":done"}) {
		t.Fatalf("job = %+v", j)
	}
	if c, _ := e.files.get(id, "mods/"+fapi.Name); c != fapi.content {
		t.Fatalf("fabric api content = %q", c)
	}
	if c, _ := e.files.get(id, "mods/"+chunky.Name); c != chunky.content {
		t.Fatalf("chunky content = %q", c)
	}
	calls := e.files.takeCalls()
	if !slices.Contains(calls, fmt.Sprintf("mkdir %d mods", id)) ||
		!slices.Contains(calls, fmt.Sprintf("upload %d mods/%s overwrite=false", id, chunky.Name)) {
		t.Fatalf("calls = %v", calls)
	}
	rows := e.rows(id)
	if len(rows) != 2 || rows[0].Filename != chunky.Name || rows[0].Sha512 != chunky.SHA512 || rows[0].Source != "modrinth" ||
		rows[0].ProjectID != "fALzjamp" || rows[0].ProjectSlug != "chunky" || rows[0].VersionID != "1CpEkmcD" ||
		rows[0].VersionName != "1.4.55" || rows[0].Channel != "release" || rows[0].InstalledAt != t0.UnixMilli() {
		t.Fatalf("rows = %+v", rows)
	}
	entries, actors := e.rec.all()
	if !slices.Equal(e.rec.actions(), []string{"plugin.install:success", "plugin.install:success"}) || !slices.Equal(actors, []string{"root", "root"}) {
		t.Fatalf("audit = %v by %v", e.rec.actions(), actors)
	}
	d := entries[0].Details
	if entries[0].Target != fmt.Sprintf("server:%d", id) || d["source"] != "modrinth" || d["project"] != "P7dR8mSH" ||
		d["version"] != "6qAuTtLR" || d["file"] != fapi.Name {
		t.Fatalf("audit entry = %+v", entries[0])
	}
	inv := e.inventory(id)
	if !isTrue(inv.RestartNeeded) || !jarNamed(t, inv, chunky.Name).InstalledByPanel {
		t.Fatalf("inventory after install = %+v", inv)
	}
}

func TestInstallRejects(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	vanilla := createInstance(t, e.db, "plain", instancespec.TypeVanilla, "1.21.11")
	e.up.modrinth("BROKEN", "broken", "Broken", "optional",
		mv{ID: "b1", Loaders: []string{"fabric"}, File: e.up.jar("Broken.jar", "b"), Deps: []map[string]any{mdep("NOWHERE", "")}})
	for _, tc := range []struct {
		srv  int64
		body any
		code int
		ec   string
	}{
		{id, map[string]any{"source": "modrinth", "project": "chunky", "x": 1}, http.StatusBadRequest, httpx.CodeBadRequest},
		{id, map[string]any{"source": "hangar", "project": "chunky"}, http.StatusBadRequest, httpx.CodeValidation},
		{id, map[string]any{"source": "modrinth", "project": "../x"}, http.StatusBadRequest, httpx.CodeValidation},
		{id, map[string]any{"source": "modrinth", "project": "chunky", "version": "a b"}, http.StatusBadRequest, httpx.CodeValidation},
		{id, map[string]any{"source": "modrinth", "project": "BROKEN"}, http.StatusConflict, httpx.CodePluginIncompatible},
		{vanilla, map[string]any{"source": "modrinth", "project": "chunky"}, http.StatusConflict, httpx.CodePluginsUnsupported},
	} {
		if rec := e.post(tc.srv, "/install", tc.body); rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%v: %d %s, want %d %s", tc.body, rec.Code, rec.Body, tc.code, tc.ec)
		}
	}
	// the requested project is installed already
	e.install(id, "modrinth", "chunky")
	e.waitJob(id)
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky", "version": nil}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("already installed: %d %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.actions()); n != 2 {
		t.Fatalf("rejected installs were audited: %v", e.rec.actions())
	}
}

// Review Focus #2: a planned name that a manual jar (in any case, enabled or not) already uses
// is refused before anything is downloaded.
func TestInstallFileExists(t *testing.T) {
	for _, existing := range []string{"Chunky-Fabric-1.4.55.jar", "chunky-fabric-1.4.55.JAR", "Chunky-Fabric-1.4.55.jar.disabled"} {
		e := newEnv(t)
		id := e.fabric("modded")
		chunky, fapi := fabricWorld(e)
		e.files.put(id, "mods/"+fapi.Name, fapi.content) // dependency present
		e.files.put(id, "mods/"+existing, "someone else's jar")
		rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"})
		if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeFileExists {
			t.Errorf("%s: %d %s", existing, rec.Code, rec.Body)
		}
		if c, _ := e.files.get(id, "mods/"+existing); c != "someone else's jar" || e.up.count("CDN "+strings.TrimPrefix(chunky.URL, e.up.cdn.URL)) != 0 {
			t.Errorf("%s: the foreign jar was touched or the download started", existing)
		}
	}
}

// Review Focus #2/#3: a jar that appears under the planned name during the job ends the job
// with file_exists; the steps before stay installed.
func TestInstallFileAppearsDuringJob(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.files.setOnUpload(func(sid int64, p string) {
		if strings.HasSuffix(p, fapi.Name) {
			e.files.put(sid, "mods/"+chunky.Name, "uploaded in the files tab meanwhile")
		}
	})
	e.install(id, "modrinth", "chunky")
	j := e.waitJob(id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":failed:file_exists"}) {
		t.Fatalf("job = %+v", steps(j))
	}
	if c, _ := e.files.get(id, "mods/"+chunky.Name); c != "uploaded in the files tab meanwhile" {
		t.Fatalf("the foreign jar was overwritten: %q", c)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != fapi.Name {
		t.Fatalf("rows = %+v", rows)
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.install:failure"}) {
		t.Fatalf("audit = %v", got)
	}
}

// Review Focus #1: a corrupted or wrongly sized download leaves no file and no row.
func TestInstallDownloadFailed(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.up.serveInstead(fapi, strings.Repeat("x", len(fapi.content))) // same size, wrong bytes
	e.install(id, "modrinth", "chunky")
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{fapi.Name + ":failed:hash_mismatch", chunky.Name + ":skipped"}) {
		t.Fatalf("hash mismatch job = %v", steps(j))
	}
	if names := e.files.names(id, "mods"); len(names) != 0 || len(e.rows(id)) != 0 {
		t.Fatalf("files %v, rows %+v", names, e.rows(id))
	}
	e.up.serveInstead(fapi, fapi.content)
	e.up.serveInstead(chunky, "short")
	e.install(id, "modrinth", "chunky")
	j = e.waitJob(id)
	if !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":failed:download_failed"}) {
		t.Fatalf("size mismatch job = %v", steps(j))
	}
	if names := e.files.names(id, "mods"); !slices.Equal(names, []string{fapi.Name}) {
		t.Fatalf("files = %v", names)
	}
}

// Review Focus #1: a download URL on a foreign host is never fetched, and a hostile file name
// becomes the fallback name inside mods/.
func TestInstallForeignHost(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	e.up.modrinth("EVIL", "evilmod", "Evil", "optional",
		mv{ID: "e1", Loaders: []string{"fabric"}, File: &upFile{URL: "https://evil.example/x.jar", Name: "x.jar", Size: 3, SHA512: sha512Hex("abc")}})
	good := e.up.jar("evil.jar", "payload")
	good.Name = "../../../../etc/cron.d/evil.jar" // what the API claims; the CDN path stays harmless
	e.up.modrinth("PATH", "pathmod", "Path", "optional", mv{ID: "p1", Loaders: []string{"fabric"}, File: good})
	e.install(id, "modrinth", "EVIL")
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{"x.jar:failed:download_failed"}) {
		t.Fatalf("foreign host = %v", steps(j))
	}
	e.install(id, "modrinth", "PATH")
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{"pathmod-p1.jar:done"}) {
		t.Fatalf("hostile name = %v", steps(j))
	}
	if names := e.files.names(id, "mods"); !slices.Equal(names, []string{"pathmod-p1.jar"}) {
		t.Fatalf("files = %v", names)
	}
	for _, c := range e.files.takeCalls() {
		if strings.HasPrefix(c, "upload") && !strings.HasPrefix(c, fmt.Sprintf("upload %d mods/pathmod-p1.jar", id)) {
			t.Fatalf("unexpected upload %q", c)
		}
	}
}

// Review Focus #4: the CDN fails in the middle of a job: the step says source_unavailable, the
// installed dependency stays.
func TestInstallSourceUnavailableMidway(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.files.setOnUpload(func(int64, string) { e.up.setCDNStatus(http.StatusServiceUnavailable) })
	e.install(id, "modrinth", "chunky")
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":failed:source_unavailable"}) || j.State != "failed" {
		t.Fatalf("job = %v", steps(j))
	}
	if j.Steps[1].Error == "" || strings.Contains(j.Steps[1].Error, "/") {
		t.Fatalf("step error = %q (text without paths)", j.Steps[1].Error)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != fapi.Name {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #3: two tabs start installs at the same time: exactly one job.
func TestOneJobPerServer(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	block := make(chan struct{})
	e.up.setBlock(block)
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() {
			codes[i] = e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"}).Code
		})
	}
	wg.Wait()
	slices.Sort(codes)
	if !slices.Equal(codes, []int{http.StatusAccepted, http.StatusConflict}) {
		t.Fatalf("codes = %v", codes)
	}
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("while running: %d %s", rec.Code, rec.Body)
	}
	if j := e.job(id); j == nil || j.State != "running" {
		t.Fatalf("running job = %+v", j)
	}
	close(block)
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
}

func TestJobLimit(t *testing.T) {
	e := newEnv(t)
	fabricWorld(e)
	block := make(chan struct{})
	e.up.setBlock(block)
	for i := range 4 {
		e.install(e.fabric(fmt.Sprintf("s%d", i)), "modrinth", "chunky")
	}
	rec := e.post(e.fabric("s4"), "/install", map[string]any{"source": "modrinth", "project": "chunky"})
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("fifth job: %d %s", rec.Code, rec.Body)
	}
	close(block)
	plugins.WaitJobs(e.h)
}

// Review Focus #3: stopping the panel cancels running jobs, Run waits for them, no file stays.
func TestRunCancelsJobs(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	e.up.setBlock(make(chan struct{})) // never released
	e.install(id, "modrinth", "chunky")
	e.stop()
	j := e.job(id)
	if j.State != "failed" || j.Steps[0].ErrorCode == nil || *j.Steps[0].ErrorCode != "files_unavailable" || j.Steps[1].State != "skipped" {
		t.Fatalf("cancelled job = %+v", steps(*j))
	}
	if names := e.files.names(id, "mods"); len(names) != 0 {
		t.Fatalf("files = %v", names)
	}
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("install after shutdown: %d %s", rec.Code, rec.Body)
	}
}

// Review Focus #3: while a job runs the inventory deletes no rows.
func TestInventoryKeepsRowsWhileJobRuns(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	block := make(chan struct{})
	e.up.setBlock(block)
	e.install(id, "modrinth", "chunky")
	// Seeded after the start: the install request's own inventory ran before the job existed.
	e.seedRow(id, "Gone.jar", "gone", "modrinth", "G", "gone", "Gone", "v1", "1", "release")
	e.inventory(id)
	if rows := e.rows(id); !slices.ContainsFunc(rows, func(r store.PluginInstall) bool { return r.Filename == "Gone.jar" }) {
		t.Fatalf("row deleted during a job: %+v", rows)
	}
	close(block)
	e.waitJob(id)
	e.inventory(id)
	if rows := e.rows(id); slices.ContainsFunc(rows, func(r store.PluginInstall) bool { return r.Filename == "Gone.jar" }) {
		t.Fatalf("orphan kept after the job: %+v", rows)
	}
}

// Decision 4: a version change replaces the jar; a disabled jar stays disabled, the same name
// is overwritten.
func TestInstallVersionChange(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	v1 := e.up.jar("Tool-1.jar", "tool 1")
	v2 := e.up.jar("Tool-2.jar", "tool 2")
	v3 := e.up.jar("Tool-2.jar", "tool 2 rebuilt")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t3", File: v3}, mv{ID: "t2", File: v2}, mv{ID: "t1", File: v1})
	e.files.put(id, "plugins/Tool-1.jar.disabled", v1.content)
	e.seedRow(id, "Tool-1.jar", v1.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "TOOL", "version": "t2"}); rec.Code != http.StatusAccepted {
		t.Fatalf("install t2: %d %s", rec.Code, rec.Body)
	}
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{"Tool-2.jar.disabled:done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"Tool-2.jar.disabled"}) {
		t.Fatalf("files = %v", names)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != "Tool-2.jar" || rows[0].VersionID != "t2" {
		t.Fatalf("rows = %+v", rows)
	}
	e.files.takeCalls()
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "TOOL", "version": "t3"}); rec.Code != http.StatusAccepted {
		t.Fatalf("install t3: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(id)
	if c, _ := e.files.get(id, "plugins/Tool-2.jar.disabled"); c != "tool 2 rebuilt" {
		t.Fatalf("content = %q", c)
	}
	if !slices.Contains(e.files.takeCalls(), fmt.Sprintf("upload %d plugins/Tool-2.jar.disabled overwrite=true", id)) {
		t.Fatal("same name must be uploaded with overwrite")
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.install:success"}) {
		t.Fatalf("audit = %v", got)
	}
}
