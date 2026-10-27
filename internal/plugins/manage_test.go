package plugins_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

func (e *env) toggle(id int64, file string, enabled bool) (int, string) {
	e.t.Helper()
	rec := e.post(id, "/toggle", map[string]any{"file": file, "enabled": enabled})
	if rec.Code == http.StatusNoContent {
		return rec.Code, ""
	}
	return rec.Code, errCode(e.t, rec)
}

func TestToggle(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("TOOL", "tool", "Tool", "required")
	e.files.put(id, "plugins/Tool.jar", "tool")
	e.seedRow(id, "Tool.jar", "tool", "modrinth", "TOOL", "tool", "Tool", "t1", "1.0", "release")
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Hour).UnixMilli()})
	if code, ec := e.toggle(id, "Tool.jar", false); code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", code, ec)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"Tool.jar.disabled"}) {
		t.Fatalf("files = %v", names)
	}
	inv := e.inventory(id)
	if j := jarNamed(t, inv, "Tool.jar.disabled"); j.Enabled || !j.InstalledByPanel || !isTrue(inv.RestartNeeded) {
		t.Fatalf("inventory = %+v", inv)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != "Tool.jar" {
		t.Fatalf("the row must survive switching: %+v", rows)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginToggle || entries[0].Result != audit.ResultSuccess ||
		entries[0].Details["file"] != "Tool.jar" || !isBool(entries[0].Details["enabled"], false) || actors[0] != "root" {
		t.Fatalf("audit = %+v", entries)
	}
	if code, _ := e.toggle(id, "Tool.jar.disabled", true); code != http.StatusNoContent {
		t.Fatalf("enable: %d", code)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"Tool.jar"}) {
		t.Fatalf("files = %v", names)
	}
	// already in the wanted state: no rename, no audit entry
	e.files.takeCalls()
	if code, _ := e.toggle(id, "Tool.jar", true); code != http.StatusNoContent || len(e.files.takeCalls()) != 0 || len(e.rec.actions()) != 2 {
		t.Fatalf("no-op toggle: %d, audit %v", code, e.rec.actions())
	}
}

// Review Focus #2: switching onto a name that exists (in any case) is refused, nothing moves.
func TestToggleTargetExists(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for p, c := range map[string]string{
		"plugins/X.jar": "x", "plugins/X.jar.disabled": "old x",
		"plugins/Y.jar": "y", "plugins/y.JAR.disabled": "old y",
		"plugins/Z.jar.disabled": "z", "plugins/z.jar": "other z",
	} {
		e.files.put(id, p, c)
	}
	before := e.files.names(id, "plugins")
	for _, tc := range []struct {
		file    string
		enabled bool
	}{{"X.jar", false}, {"Y.jar", false}, {"Z.jar.disabled", true}} {
		if code, ec := e.toggle(id, tc.file, tc.enabled); code != http.StatusConflict || ec != httpx.CodeFileExists {
			t.Errorf("%s: %d %s", tc.file, code, ec)
		}
	}
	if after := e.files.names(id, "plugins"); !slices.Equal(after, before) {
		t.Fatalf("files changed: %v -> %v", before, after)
	}
	for _, c := range e.files.takeCalls() {
		if strings.HasPrefix(c, "rename") {
			t.Fatalf("rename reached the service: %s", c)
		}
	}
	if len(e.rec.actions()) != 0 {
		t.Fatalf("refused toggles were audited: %v", e.rec.actions())
	}
}

func TestToggleErrors(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.put(id, "plugins/A.jar", "a")
	for _, body := range []map[string]any{
		{"file": "../A.jar", "enabled": false}, {"file": "x/A.jar", "enabled": false}, {"file": "A.zip", "enabled": false},
		{"file": ".mcpanel-A.jar", "enabled": false}, {"file": "", "enabled": false}, {"file": "A.jar"},
	} {
		if rec := e.post(id, "/toggle", body); rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	if code, ec := e.toggle(id, "Missing.jar", false); code != http.StatusNotFound || ec != httpx.CodeNotFound {
		t.Errorf("missing: %d %s", code, ec)
	}
	e.files.setErr("rename", fmt.Errorf("%w: down", filesapi.ErrUnavailable))
	if code, ec := e.toggle(id, "A.jar", false); code != http.StatusServiceUnavailable || ec != httpx.CodeFilesUnavailable {
		t.Errorf("service down: %d %s", code, ec)
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.toggle:failure"}) {
		t.Errorf("audit = %v", got)
	}
}

func TestManageDuringJob(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	e.files.put(id, "mods/Other.jar", "other")
	block := make(chan struct{})
	e.up.setBlock(block)
	e.install(id, "modrinth", "chunky")
	if code, ec := e.toggle(id, "Other.jar", false); code != http.StatusConflict || ec != httpx.CodeConflict {
		t.Errorf("toggle during a job: %d %s", code, ec)
	}
	if rec := e.post(id, "/delete", map[string]any{"file": "Other.jar"}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Errorf("delete during a job: %d %s", rec.Code, rec.Body)
	}
	close(block)
	e.waitJob(id)
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.put(id, "plugins/Tool.jar.disabled", "tool")
	e.seedRow(id, "Tool.jar", "tool", "modrinth", "TOOL", "tool", "Tool", "t1", "1.0", "release")
	if rec := e.post(id, "/delete", map[string]any{"file": "Tool.jar.disabled"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if names := e.files.names(id, "plugins"); len(names) != 0 || len(e.rows(id)) != 0 {
		t.Fatalf("files %v, rows %+v", names, e.rows(id))
	}
	entries, _ := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginRemove || entries[0].Details["file"] != "Tool.jar.disabled" {
		t.Fatalf("audit = %+v", entries)
	}
	if rec := e.post(id, "/delete", map[string]any{"file": "Tool.jar.disabled"}); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
	if rec := e.post(id, "/delete", map[string]any{"file": "../x.jar"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: %d", rec.Code)
	}
	if n := len(e.rec.actions()); n != 1 {
		t.Fatalf("refused deletes were audited: %v", e.rec.actions())
	}
}

// Review Focus #2: deleting x.jar keeps the row that belongs to x.jar.disabled.
func TestDeleteKeepsOtherRow(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("X", "x", "X", "required")
	e.files.put(id, "plugins/X.jar", "manual copy")
	e.files.put(id, "plugins/X.jar.disabled", "panel copy")
	e.seedRow(id, "X.jar", "panel copy", "modrinth", "X", "x", "X", "x1", "1", "release")
	if rec := e.post(id, "/delete", map[string]any{"file": "X.jar"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rows := e.rows(id); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if j := jarNamed(t, e.inventory(id), "X.jar.disabled"); !j.InstalledByPanel {
		t.Fatalf("the remaining jar lost its origin: %+v", j)
	}
}

func TestManageDropsUpdatesCache(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LP-old.jar")
	e.files.put(id, "plugins/Other.jar", "other")
	key := "POST /v2/version_files/update"
	e.updates(id, "")
	if code, _ := e.toggle(id, "Other.jar", false); code != http.StatusNoContent {
		t.Fatalf("toggle: %d", code)
	}
	e.updates(id, "")
	if rec := e.post(id, "/delete", map[string]any{"file": "Other.jar.disabled"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	e.updates(id, "")
	if n := e.up.count(key); n != 3 {
		t.Fatalf("checks = %d, want 3 (toggle and delete drop the cache)", n)
	}
}

// isBool reports whether a decoded JSON value v is the boolean want.
func isBool(v any, want bool) bool { b, ok := v.(bool); return ok && b == want }
