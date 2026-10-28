package plugins_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type projectJSON struct {
	ID      string  `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	PageURL string  `json:"page_url"`
	IconURL *string `json:"icon_url"`
}

type versionRefJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Channel string `json:"channel"`
}

type jarJSON struct {
	File             string            `json:"file"`
	Enabled          bool              `json:"enabled"`
	Size             int64             `json:"size"`
	MTime            int64             `json:"mtime"`
	Source           string            `json:"source"`
	Project          *projectJSON      `json:"project"`
	Version          *versionRefJSON   `json:"version"`
	Meta             *filesapi.JarMeta `json:"meta"`
	InstalledByPanel bool              `json:"installed_by_panel"`
}

type invJSON struct {
	Kind          string    `json:"kind"`
	Dir           string    `json:"dir"`
	GameVersion   *string   `json:"game_version"`
	Sources       []string  `json:"sources"`
	Jars          []jarJSON `json:"jars"`
	Truncated     bool      `json:"truncated"`
	RestartNeeded *bool     `json:"restart_needed"`
	SourceErrors  []string  `json:"source_errors"`
}

func (e *env) inventory(id int64) invJSON {
	e.t.Helper()
	return decode[invJSON](e.t, e.get(srvPath(id, "")))
}

func jarNamed(t *testing.T, inv invJSON, name string) jarJSON {
	t.Helper()
	for _, j := range inv.Jars {
		if j.File == name {
			return j
		}
	}
	t.Fatalf("no jar %s in %+v", name, inv.Jars)
	return jarJSON{}
}

func proxied(id int64, raw string) string {
	return fmt.Sprintf("/api/servers/%d/plugins/icon?url=%s", id, url.QueryEscape(raw))
}

func TestInventoryMapping(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	oldJar := e.up.jar("LuckPerms-Bukkit-5.5.53.jar", "luckperms 5.5.53")
	e.up.modrinth("Vebnzrzj", "luckperms", "LuckPerms", "required",
		mv{ID: "b0mk8uS6", Number: "v5.5.71-bukkit", File: e.up.jar("LuckPerms-Bukkit-5.5.71.jar", "luckperms 5.5.71")},
		mv{ID: "MBSY8toc", Number: "v5.5.53-bukkit", File: oldJar})
	e.up.modrinth("OFFproj", "offmod", "Off Plugin", "required")
	e.up.hangar(31, "ViaVersion", "ViaVersion")
	// hash-mapped (uploaded by hand), panel-installed from Hangar, manual, panel-installed and disabled
	e.files.put(id, "plugins/LuckPerms-old.jar", oldJar.content)
	e.files.put(id, "plugins/ViaVersion-5.12.1.jar", "viaversion 5.12.1")
	e.seedRow(id, "ViaVersion-5.12.1.jar", "viaversion 5.12.1", "hangar", "31", "ViaVersion", "ViaVersion", "5.12.1", "5.12.1", "release")
	e.files.put(id, "plugins/OldTool.jar", "old tool")
	e.files.meta[fkey(id, "plugins/OldTool.jar")] = &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: "OldTool", Version: "1.0"}
	e.files.put(id, "plugins/Off.jar.disabled", "off plugin")
	e.seedRow(id, "Off.jar", "off plugin", "modrinth", "OFFproj", "offmod", "Off Plugin", "v1", "1.0", "beta")
	e.files.put(id, "plugins/config/LuckPerms.yml", "not a jar")
	e.files.put(id, "mods/Other.jar", "other dir")

	inv := e.inventory(id)
	if inv.Kind != "plugin" || inv.Dir != "plugins" || inv.GameVersion == nil || *inv.GameVersion != "1.21.11" ||
		!slices.Equal(inv.Sources, []string{"modrinth", "hangar"}) || inv.Truncated || !isFalse(inv.RestartNeeded) ||
		inv.SourceErrors == nil || len(inv.SourceErrors) != 0 {
		t.Fatalf("inventory = %+v", inv)
	}
	var names []string
	for _, j := range inv.Jars {
		names = append(names, j.File)
	}
	if !slices.Equal(names, []string{"LuckPerms-old.jar", "Off.jar.disabled", "OldTool.jar", "ViaVersion-5.12.1.jar"}) {
		t.Fatalf("jars = %v", names)
	}
	lp := jarNamed(t, inv, "LuckPerms-old.jar")
	icon := proxied(id, e.up.iconURL("Vebnzrzj"))
	if lp.Source != "modrinth" || lp.InstalledByPanel || lp.Project == nil || lp.Project.ID != "Vebnzrzj" ||
		lp.Project.Slug != "luckperms" || lp.Project.Title != "LuckPerms" || lp.Project.PageURL != "https://modrinth.com/plugin/luckperms" ||
		lp.Project.IconURL == nil || *lp.Project.IconURL != icon ||
		lp.Version == nil || *lp.Version != (versionRefJSON{ID: "MBSY8toc", Name: "v5.5.53-bukkit", Channel: "release"}) {
		t.Fatalf("hash-mapped jar = %+v / %+v / %+v", lp, lp.Project, lp.Version)
	}
	via := jarNamed(t, inv, "ViaVersion-5.12.1.jar")
	if via.Source != "hangar" || !via.InstalledByPanel || via.Project.PageURL != "https://hangar.papermc.io/ViaVersion/ViaVersion" ||
		via.Version.ID != "5.12.1" {
		t.Fatalf("hangar jar = %+v / %+v", via, via.Project)
	}
	tool := jarNamed(t, inv, "OldTool.jar")
	if tool.Source != "manual" || tool.Project != nil || tool.Version != nil || tool.Meta == nil || tool.Meta.Name != "OldTool" {
		t.Fatalf("manual jar = %+v", tool)
	}
	off := jarNamed(t, inv, "Off.jar.disabled")
	if off.Enabled || !off.InstalledByPanel || off.Source != "modrinth" || off.Version.Channel != "beta" {
		t.Fatalf("disabled jar = %+v", off)
	}
	raw := e.get(srvPath(id, "")).Body.String()
	if !strings.Contains(raw, `"project":null`) || !strings.Contains(raw, `"version":null`) {
		t.Fatalf("manual jar must carry explicit nulls: %s", raw)
	}
	if calls := e.files.takeCalls(); !slices.Contains(calls, fmt.Sprintf("jars %d plugins", id)) {
		t.Fatalf("calls = %v", calls)
	}
}

// Review Focus #3: rows whose jar is gone or was replaced disappear on the next inventory.
func TestInventoryDropsOrphans(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("K", "kept", "Kept", "required")
	e.files.put(id, "plugins/Kept.jar", "kept")
	e.seedRow(id, "Kept.jar", "kept", "modrinth", "K", "kept", "Kept", "v1", "1", "release")
	e.seedRow(id, "Gone.jar", "gone", "modrinth", "G", "gone", "Gone", "v1", "1", "release")
	e.files.put(id, "plugins/Replaced.jar", "new content from the files tab")
	e.seedRow(id, "Replaced.jar", "old content", "modrinth", "R", "replaced", "Replaced", "v1", "1", "release")

	inv := e.inventory(id)
	if j := jarNamed(t, inv, "Replaced.jar"); j.Source != "manual" || j.InstalledByPanel {
		t.Fatalf("replaced jar = %+v", j)
	}
	rows := e.rows(id)
	if len(rows) != 1 || rows[0].Filename != "Kept.jar" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #3: a truncated listing does not show every jar, so no row counts as orphaned.
func TestInventoryKeepsRowsWhenTruncated(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.seedRow(id, "Beyond500.jar", "x", "modrinth", "B", "b", "B", "v1", "1", "release")
	e.files.setTruncated(true)
	if inv := e.inventory(id); !inv.Truncated {
		t.Fatal("truncated not passed on")
	}
	if rows := e.rows(id); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #4: without Modrinth or Hangar the inventory still answers, jars stay manual.
func TestInventorySourceErrors(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	known := e.up.jar("Known.jar", "known")
	e.up.modrinth("KN", "known", "Known", "required", mv{ID: "v1", File: known})
	e.up.hangar(31, "ViaVersion", "ViaVersion")
	e.files.put(id, "plugins/Known.jar", "known")
	e.files.put(id, "plugins/ViaVersion.jar", "via")
	e.seedRow(id, "ViaVersion.jar", "via", "hangar", "31", "ViaVersion", "ViaVersion", "5.12.1", "5.12.1", "release")
	e.up.setDown(true)

	inv := e.inventory(id)
	if !slices.Equal(inv.SourceErrors, []string{"modrinth", "hangar"}) {
		t.Fatalf("source_errors = %v", inv.SourceErrors)
	}
	if j := jarNamed(t, inv, "Known.jar"); j.Source != "manual" {
		t.Fatalf("known jar = %+v", j)
	}
	via := jarNamed(t, inv, "ViaVersion.jar")
	if via.Source != "hangar" || via.Project.PageURL != "https://hangar.papermc.io/" || via.Project.IconURL != nil {
		t.Fatalf("hangar jar without upstream = %+v", via.Project)
	}
	// the hash lookup failure is not cached: once Modrinth is back, the jar is mapped
	e.up.setDown(false)
	if j := jarNamed(t, e.inventory(id), "Known.jar"); j.Source != "modrinth" {
		t.Fatalf("after recovery = %+v", j)
	}
}

func TestInventoryGameVersion(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "latest", instancespec.TypePaper, "LATEST")
	e.cat.cat.Versions[0].ID = "26.3"
	if inv := e.inventory(id); inv.GameVersion == nil || *inv.GameVersion != "26.3" {
		t.Fatalf("game_version = %v", inv.GameVersion)
	}
	e.cat.setErr(errors.New("catalog down"))
	if inv := e.inventory(id); inv.GameVersion != nil {
		t.Fatalf("game_version without catalog = %v", *inv.GameVersion)
	}
}

func TestInventoryKinds(t *testing.T) {
	e := newEnv(t)
	vanilla := createInstance(t, e.db, "plain", instancespec.TypeVanilla, "1.21.11")
	pack := createInstance(t, e.db, "pack", instancespec.TypeModrinth, "")
	stub := storetest.CreateServer(t, e.db, "lobby")
	fab := e.fabric("modded")
	a := e.admin()
	for _, id := range []int64{vanilla, pack} {
		rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a)
		if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodePluginsUnsupported {
			t.Errorf("server %d: %d %s", id, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{srvPath(stub, ""), "/servers/x/plugins", "/servers/999/plugins"} {
		rec := e.do(http.MethodGet, path, nil, &a)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	e.files.put(fab, "mods/fabric-api.jar", "fabric api")
	inv := e.inventory(fab)
	if inv.Kind != "mod" || inv.Dir != "mods" || !slices.Equal(inv.Sources, []string{"modrinth"}) || len(inv.Jars) != 1 {
		t.Fatalf("fabric inventory = %+v", inv)
	}
}

func TestPluginsPermissions(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	other := e.paper("creative")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view", "server.files", "server.console")
	plug := storetest.CreateUser(t, e.db, "plug", "user")
	grant(t, e.db, plug, id, "server.view", "server.plugins")
	pv, pp := userP(viewer, "viewer"), userP(plug, "plug")
	for _, tc := range []struct {
		name string
		path string
		p    *principal.Principal
		code int
	}{
		{"view, files and console only", srvPath(id, ""), &pv, http.StatusForbidden},
		{"plugins on own server", srvPath(id, ""), &pp, http.StatusOK},
		{"plugins on other server", srvPath(other, ""), &pp, http.StatusForbidden},
		{"anonymous", srvPath(id, ""), nil, http.StatusUnauthorized},
	} {
		if rec := e.do(http.MethodGet, tc.path, nil, tc.p); rec.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.code)
		}
	}
}

func TestInventoryFileServiceErrors(t *testing.T) {
	e := newEnvWith(t, func(d *plugins.Deps) { d.Files = nil })
	id := e.paper("survival")
	a := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != httpx.CodeFilesUnavailable {
		t.Fatalf("no service: %d %s", rec.Code, rec.Body)
	}
	e2 := newEnv(t)
	id2 := e2.paper("survival")
	e2.files.setErr("jars", fmt.Errorf("%w: down", filesapi.ErrUnavailable))
	a2 := e2.admin()
	if rec := e2.do(http.MethodGet, srvPath(id2, ""), nil, &a2); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("service down: %d %s", rec.Code, rec.Body)
	}
}

func isTrue(b *bool) bool  { return b != nil && *b }
func isFalse(b *bool) bool { return b != nil && !*b }

func fmtTri(b *bool) string {
	if b == nil {
		return "null"
	}
	return strconv.FormatBool(*b)
}

func TestRestartNeeded(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.agent.setErr(agentapi.ErrUnavailable)
	if got := e.inventory(id).RestartNeeded; !isFalse(got) {
		t.Fatalf("without any change restart_needed = %s, want false even without the agent", fmtTri(got))
	}
	e.agent.setErr(nil)
	plugins.MarkChanged(e.h, id, t0)
	for _, tc := range []struct {
		st   agentapi.InstanceStatus
		want string
	}{
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Minute).UnixMilli()}, "true"},
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.UnixMilli()}, "true"},
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: 0}, "null"}, // start time unknown (inspect failed)
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(time.Minute).UnixMilli()}, "false"},
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateExited}, "false"},
	} {
		e.agent.set(tc.st)
		if got := fmtTri(e.inventory(id).RestartNeeded); got != tc.want {
			t.Errorf("%+v: restart_needed = %s, want %s", tc.st, got, tc.want)
		}
	}
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Minute).UnixMilli()})
	e.agent.setErr(agentapi.ErrUnavailable)
	if got := e.inventory(id).RestartNeeded; got != nil {
		t.Fatalf("agent down: restart_needed = %s, want null (unknown, never false)", fmtTri(got))
	}
	e.agent.setErr(nil)
	e.agent.setDocker(agentapi.DockerUnavailable)
	if got := e.inventory(id).RestartNeeded; got != nil {
		t.Fatalf("docker down: restart_needed = %s, want null", fmtTri(got))
	}
}

// TestRestartNeededSurvivesPanelRestart: the change time is in the DB, so a new handler on the
// same DB (a panel restart) still sees it, and only a container start after it clears it.
func TestRestartNeededSurvivesPanelRestart(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.put(id, "plugins/Tool.jar", "tool")
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Hour).UnixMilli()})
	e.clk.Advance(time.Minute)
	if code, ec := e.toggle(id, "Tool.jar", false); code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", code, ec)
	}
	e.restartPanel()
	if got := e.inventory(id).RestartNeeded; !isTrue(got) {
		t.Fatalf("after a panel restart restart_needed = %s, want true", fmtTri(got))
	}
	// the Minecraft container restarts after the change: cleared, without forgetting the change
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: e.clk.Now().Add(time.Second).UnixMilli()})
	if got := e.inventory(id).RestartNeeded; !isFalse(got) {
		t.Fatalf("after a container restart restart_needed = %s, want false", fmtTri(got))
	}
	// a clock step back does not move the recorded change back
	plugins.MarkChanged(e.h, id, t0.Add(-24*time.Hour))
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.UnixMilli()})
	if got := e.inventory(id).RestartNeeded; !isTrue(got) {
		t.Fatalf("an earlier mark moved the change back: restart_needed = %s", fmtTri(got))
	}
}

// TestRestartNeededAfterLostResponse: a files call whose answer is lost may still have changed
// the disk, so the change is recorded before the call (erring towards "restart needed").
func TestRestartNeededAfterLostResponse(t *testing.T) {
	lost := fmt.Errorf("%w: connection reset", filesapi.ErrUnavailable)
	running := func(id int64) agentapi.InstanceStatus {
		return agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Hour).UnixMilli()}
	}
	t.Run("install", func(t *testing.T) {
		e := newEnv(t)
		id := e.fabric("modded")
		fabricWorld(e)
		e.agent.set(running(id))
		e.files.setErr("upload", lost)
		e.install(id, "modrinth", "chunky")
		if j := e.waitJob(id); j.State != "failed" {
			t.Fatalf("job = %+v", j)
		}
		if got := e.inventory(id).RestartNeeded; !isTrue(got) {
			t.Fatalf("restart_needed = %s after a failed upload, want true", fmtTri(got))
		}
	})
	t.Run("toggle", func(t *testing.T) {
		e := newEnv(t)
		id := e.paper("survival")
		e.files.put(id, "plugins/Tool.jar", "tool")
		e.agent.set(running(id))
		e.files.setErr("rename", lost)
		if code, _ := e.toggle(id, "Tool.jar", false); code == http.StatusNoContent {
			t.Fatal("toggle succeeded")
		}
		if got := e.inventory(id).RestartNeeded; !isTrue(got) {
			t.Fatalf("restart_needed = %s after a failed rename, want true", fmtTri(got))
		}
	})
	t.Run("delete", func(t *testing.T) {
		e := newEnv(t)
		id := e.paper("survival")
		e.files.put(id, "plugins/Tool.jar", "tool")
		e.agent.set(running(id))
		e.files.setErr("delete", lost)
		if rec := e.post(id, "/delete", map[string]any{"file": "Tool.jar"}); rec.Code == http.StatusNoContent {
			t.Fatal("delete succeeded")
		}
		if got := e.inventory(id).RestartNeeded; !isTrue(got) {
			t.Fatalf("restart_needed = %s after a failed delete, want true", fmtTri(got))
		}
	})
}

// TestRestartNeededFutureChange: a change recorded before the clock stepped back lies in the
// future. The first read pins it to now (still "needed"), so the next real start clears it
// instead of the notice lasting until the clock catches up.
func TestRestartNeededFutureChange(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	plugins.MarkChanged(e.h, id, t0.Add(24*time.Hour))
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Minute).UnixMilli()})
	if got := e.inventory(id).RestartNeeded; !isTrue(got) {
		t.Fatalf("before a restart restart_needed = %s, want true", fmtTri(got))
	}
	e.clk.Advance(2 * time.Minute)
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(time.Minute).UnixMilli()})
	if got := e.inventory(id).RestartNeeded; !isFalse(got) {
		t.Fatalf("after a restart restart_needed = %s, want false", fmtTri(got))
	}
}
