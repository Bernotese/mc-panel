package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// upload sends a raw upload with the browser's headers (Origin, CSRF token).
func (c *client) upload(id int64, p string, data []byte) (int, map[string]any) {
	c.e.t.Helper()
	u := fmt.Sprintf("%s/api/servers/%d/files/upload?path=%s", c.e.srv.URL, id, url.QueryEscape(p))
	req, _ := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Origin", c.e.cfg.Origin())
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func (c *client) download(id int64, p string) (*http.Response, []byte) {
	c.e.t.Helper()
	resp, err := c.hc.Get(fmt.Sprintf("%s/api/servers/%d/files/download?path=%s", c.e.srv.URL, id, url.QueryEscape(p)))
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// The file routes are wired with the real middleware chain (body limit, CSRF), the rbac rules,
// the agent state check and the gate in the servers handler.
func TestFilesEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))

	// the editor may save more than the general 1 MiB body limit; other routes keep it
	big := strings.Repeat("a", 2<<20)
	admin.must(200, "PUT", fmt.Sprintf("/servers/%d/files/content", id), map[string]any{"path": "notes.txt", "content": big, "expected_mtime": nil})
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/files/mkdir", id), map[string]any{"path": big}); code != 413 {
		t.Fatalf("mkdir with a 2 MiB body = %d %v", code, out)
	}

	// upload above 1 MiB through CSRF and the body-limit exemption; without the token it is refused
	payload := bytes.Repeat([]byte{'P'}, 3<<20)
	if code, out := admin.upload(id, "plugins/big.jar", payload); code != 201 || out["size"] != float64(len(payload)) {
		t.Fatalf("upload = %d %v", code, out)
	}
	csrf := admin.csrf
	admin.csrf = ""
	if code, out := admin.upload(id, "plugins/x.jar", []byte("x")); code != 403 {
		t.Fatalf("upload without CSRF token = %d %v", code, out)
	}
	admin.csrf = csrf
	list := admin.must(200, "GET", fmt.Sprintf("/servers/%d/files?path=plugins", id), nil)
	if entries, _ := list["entries"].([]any); len(entries) != 1 {
		t.Fatalf("list = %v", list)
	}
	resp, body := admin.download(id, "plugins/big.jar")
	if resp.StatusCode != 200 || !bytes.Equal(body, payload) ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), `attachment; filename="big.jar"`) {
		t.Fatalf("download = %d %v (%d bytes)", resp.StatusCode, resp.Header, len(body))
	}

	// extracting needs a stopped server, and the server cannot start while the job runs
	waitServer(t, admin, id, "running", stateIs("running"))
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/files/extract", id), map[string]any{"path": "w.zip", "dest": "w"}); code != 409 || out["error"] != "instance_running" {
		t.Fatalf("extract while running = %d %v", code, out)
	}
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "stop"})
	waitServer(t, admin, id, "stopped", stateIs("stopped"))
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/files/extract", id), map[string]any{"path": "w.zip", "dest": "w"})
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}); code != 409 || out["error"] != "conflict" {
		t.Fatalf("start while extracting = %d %v", code, out)
	}
	job := admin.must(200, "GET", fmt.Sprintf("/servers/%d/files/jobs", id), nil)
	if j, _ := job["job"].(map[string]any); j["state"] != "running" {
		t.Fatalf("job = %v", job)
	}
	e.files.finishJob()
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"})
	waitServer(t, admin, id, "running", stateIs("running"))

	// a user with server.view only gets 403 on the files routes
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": id, "permissions": []string{"server.view"}}},
	})
	friend := signInNewUser(e, "friend", "another long secret 42")
	if code, out := friend.do("GET", fmt.Sprintf("/servers/%d/files", id), nil); code != 403 {
		t.Fatalf("friend files = %d %v", code, out)
	}
}
