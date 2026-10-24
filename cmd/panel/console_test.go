package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// openConsole opens the SSE stream with c's cookies and returns the response.
func openConsole(t *testing.T, c *client, id int64) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/servers/%d/console/stream", c.e.srv.URL, id), nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	return resp, cancel
}

// The console routes are wired with the real rbac rules, the agent client and the recheck.
func TestConsoleEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))

	out := admin.must(200, "POST", fmt.Sprintf("/servers/%d/console", id), map[string]string{"command": "/say hi"})
	if out["output"] != "ok: say hi" {
		t.Fatalf("command output = %v", out)
	}
	players := admin.must(200, "GET", fmt.Sprintf("/servers/%d/players", id), nil)
	if available, _ := players["available"].(bool); !available {
		t.Fatalf("players = %v", players)
	}
	lists := admin.must(200, "GET", fmt.Sprintf("/servers/%d/players/lists", id), nil)
	if ops, _ := lists["ops"].([]any); len(ops) != 1 {
		t.Fatalf("lists = %v", lists)
	}
	admin.must(200, "POST", fmt.Sprintf("/servers/%d/players/actions", id), map[string]string{"action": "op", "name": "Steve"})
	if got := e.console.sent(); !slices.Contains(got, fmt.Sprintf("%d say hi", id)) || !slices.Contains(got, fmt.Sprintf("%d op Steve", id)) {
		t.Fatalf("agent commands = %v", got)
	}

	resp, _ := openConsole(t, admin, id)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %v", resp.StatusCode, resp.Header)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	if !waitLine(lines, "id: 1760184000000000001") {
		t.Fatal("first log line not streamed")
	}
	e.app.Shutdown() // what http.Server.Shutdown triggers via RegisterOnShutdown
	if !waitClosed(lines) {
		t.Fatal("stream still open after Shutdown")
	}

	// a friend with server.view + server.power only
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": id, "permissions": []string{"server.power"}}},
	})
	friend := signInNewUser(e, "friend", "another long secret 42")
	friend.must(200, "GET", fmt.Sprintf("/servers/%d/players", id), nil)
	for _, tc := range []struct{ method, path string }{
		{"POST", fmt.Sprintf("/servers/%d/console", id)},
		{"GET", fmt.Sprintf("/servers/%d/players/lists", id)},
		{"POST", fmt.Sprintf("/servers/%d/players/actions", id)},
		{"GET", fmt.Sprintf("/servers/%d/console/stream", id)},
	} {
		if c, body := friend.do(tc.method, tc.path, map[string]string{"command": "list", "action": "op", "name": "Steve"}); c != 403 {
			t.Errorf("%s %s as friend = %d %v, want 403", tc.method, tc.path, c, body)
		}
	}
}

func waitLine(lines <-chan string, want string) bool {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return false
			}
			if strings.TrimSpace(l) == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func waitClosed(lines <-chan string) bool {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
