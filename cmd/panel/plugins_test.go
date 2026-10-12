package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
)

// raw sends a GET with the client's cookies and returns the response and body.
func (c *client) raw(path string) (*http.Response, []byte) {
	c.e.t.Helper()
	resp, err := c.hc.Get(c.e.srv.URL + "/api" + path)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// waitPluginJob polls the job of the server until it is no longer running.
func waitPluginJob(t *testing.T, c *client, id int64) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := c.must(200, "GET", fmt.Sprintf("/servers/%d/plugins/jobs", id), nil)
		if j, _ := out["job"].(map[string]any); j != nil && j["state"] != "running" {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin job never ended: %v", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func jarFiles(inv map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, j := range inv["jars"].([]any) {
		m := j.(map[string]any)
		out[m["file"].(string)] = m
	}
	return out
}

// The plugin routes run through the real middleware chain (CSRF, rbac, audit) against the fake
// file service and a fake Modrinth.
func TestPluginsEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))
	base := fmt.Sprintf("/servers/%d/plugins", id)
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	e.up.project("LIB", "lib", "Lib", mVersion("lib1", "1.0", e.up.jar("Lib-1.0.jar", "lib 1.0"), day))
	e.up.project("TOOL", "tool", "Tool",
		mVersion("t2", "2.0", e.up.jar("Tool-2.0.jar", "tool 2.0"), day, "LIB"),
		mVersion("t1", "1.0", e.up.jar("Tool-1.0.jar", "tool 1.0"), day.Add(-24*time.Hour), "LIB"))

	if inv := admin.must(200, "GET", base, nil); inv["kind"] != "plugin" || inv["dir"] != "plugins" || inv["game_version"] != "1.21.11" {
		t.Fatalf("inventory = %v", inv)
	}
	if hits, _ := admin.must(200, "GET", base+"/search?source=modrinth&q=tool", nil)["hits"].([]any); len(hits) != 2 {
		t.Fatalf("hits = %v", hits)
	}
	if install, _ := admin.must(200, "GET", base+"/resolve?source=modrinth&project=tool&version=t1", nil)["install"].([]any); len(install) != 2 {
		t.Fatalf("plan = %v", install)
	}

	// installing needs the CSRF token like every POST
	csrf := admin.csrf
	admin.csrf = ""
	if code, out := admin.do("POST", base+"/install", map[string]any{"source": "modrinth", "project": "tool", "version": "t1"}); code != 403 {
		t.Fatalf("install without CSRF token = %d %v", code, out)
	}
	admin.csrf = csrf
	admin.must(202, "POST", base+"/install", map[string]any{"source": "modrinth", "project": "tool", "version": "t1"})
	if j := waitPluginJob(t, admin, id); j["state"] != "done" {
		t.Fatalf("install job = %v", j)
	}
	jars := jarFiles(admin.must(200, "GET", base, nil))
	if len(jars) != 2 || !isBool(jars["Tool-1.0.jar"]["installed_by_panel"], true) || !isBool(jars["Lib-1.0.jar"]["installed_by_panel"], true) {
		t.Fatalf("jars after install = %v", jars)
	}

	if ups, _ := admin.must(200, "GET", base+"/updates", nil)["updates"].([]any); len(ups) != 1 {
		t.Fatalf("updates = %v", ups)
	}
	admin.must(202, "POST", base+"/update", map[string]any{"files": []string{}})
	if j := waitPluginJob(t, admin, id); j["state"] != "done" {
		t.Fatalf("update job = %v", j)
	}
	admin.must(204, "POST", base+"/toggle", map[string]any{"file": "Tool-2.0.jar", "enabled": false})
	admin.must(204, "POST", base+"/delete", map[string]any{"file": "Lib-1.0.jar"})
	waitServer(t, admin, id, "running", stateIs("running"))
	inv := admin.must(200, "GET", base, nil)
	jars = jarFiles(inv)
	if len(jars) != 1 || !isBool(jars["Tool-2.0.jar.disabled"]["enabled"], false) || !isBool(inv["restart_needed"], true) {
		t.Fatalf("inventory at the end = %v", inv)
	}
	e.files.mu.Lock()
	_, onDisk := e.files.files[fmt.Sprintf("%d/plugins/Tool-2.0.jar.disabled", id)]
	e.files.mu.Unlock()
	if !onDisk {
		t.Fatal("the disabled jar is not in the file service")
	}

	// icon proxy with its own headers; foreign hosts are refused
	resp, body := admin.raw(base + "/icon?url=" + url.QueryEscape(e.up.cdn.URL+"/icons/TOOL.png"))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Content-Security-Policy") != "default-src 'none'" ||
		resp.Header.Get("Cache-Control") != "private, max-age=86400" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || len(body) == 0 {
		t.Fatalf("icon = %d %v", resp.StatusCode, resp.Header)
	}
	if code, out := admin.do("GET", base+"/icon?url="+url.QueryEscape("https://evil.example/x.png"), nil); code != 400 {
		t.Fatalf("foreign icon = %d %v", code, out)
	}

	// a user with server.view only gets 403 (acceptance criterion 6)
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": id, "permissions": []string{"server.view"}}},
	})
	friend := e.client("")
	friend.must(200, "POST", "/auth/login", map[string]string{"username": "friend", "password": "another long secret 42"})
	if code, _ := friend.do("GET", base, nil); code != 403 {
		t.Fatalf("friend plugins = %d", code)
	}

	// acceptance criterion 7: every change is in the audit log and the chain verifies
	page := admin.must(200, "GET", "/audit?limit=200", nil)
	seen := map[string]bool{}
	for _, it := range page["items"].([]any) {
		seen[it.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{audit.ActionPluginInstall, audit.ActionPluginUpdate, audit.ActionPluginToggle, audit.ActionPluginRemove} {
		if !seen[want] {
			t.Errorf("audit log misses %s", want)
		}
	}
	if v := admin.must(200, "GET", "/audit/verify", nil); !isTrue(v["ok"]) {
		t.Fatalf("audit verify: %v", v)
	}
}

// isBool reports whether a decoded JSON value v is the boolean want.
func isBool(v any, want bool) bool { b, ok := v.(bool); return ok && b == want }
