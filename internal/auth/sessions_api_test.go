package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
)

func listSessions(t *testing.T, c *client, path string) []sessionJSON {
	t.Helper()
	res := c.do(http.MethodGet, path, nil)
	var out []sessionJSON
	if res.code != 200 || json.Unmarshal(res.body, &out) != nil {
		t.Fatalf("GET %s = %d %s", path, res.code, res.body)
	}
	return out
}

func TestMySessions(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	a := e.newClient("203.0.113.10")
	a.login("alice", testPassword)
	b := e.newClient("198.51.100.20")
	b.login("alice", testPassword)

	list := listSessions(t, a, "/api/me/sessions")
	if len(list) != 2 {
		t.Fatalf("sessions = %+v", list)
	}
	var bID int64
	current := 0
	for _, s := range list {
		if s.UserID != uid || s.Username != "alice" || s.Country != "DE" || s.UserAgent != "test-agent" || s.ExpiresAt <= s.CreatedAt {
			t.Fatalf("session = %+v", s)
		}
		if s.Current {
			current++
			if s.IP != "203.0.113.10" {
				t.Fatalf("current session ip = %s", s.IP)
			}
		} else {
			bID = s.ID
		}
	}
	if current != 1 || bID == 0 {
		t.Fatalf("current=%d bID=%d", current, bID)
	}

	e.createUser("mallory", "user")
	m := e.newClient("192.0.2.30")
	m.login("mallory", testPassword)
	for _, p := range []string{fmt.Sprintf("/api/me/sessions/%d", m.sessionID()), "/api/me/sessions/99999", "/api/me/sessions/abc"} {
		if res := a.do(http.MethodDelete, p, nil); res.code != 404 {
			t.Fatalf("DELETE %s = %d", p, res.code)
		}
	}
	if res := a.do(http.MethodDelete, fmt.Sprintf("/api/me/sessions/%d", bID), nil); res.code != 204 {
		t.Fatalf("revoke b = %d %s", res.code, res.body)
	}
	if res := b.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("revoked session still valid: %d", res.code)
	}
	if !hasAction(e, audit.ActionSessionRevoke) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	own := a.sessionID()
	if res := a.do(http.MethodDelete, fmt.Sprintf("/api/me/sessions/%d", own), nil); res.code != 204 || a.cookie != "" {
		t.Fatalf("revoking the current session must clear the cookie: %d cookie=%q", res.code, a.cookie)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	a, b, c := e.newClient("203.0.113.10"), e.newClient("198.51.100.20"), e.newClient("192.0.2.30")
	for _, cl := range []*client{a, b, c} {
		cl.login("alice", testPassword)
	}
	if res := a.do(http.MethodPost, "/api/me/sessions/revoke-others", nil); res.code != 204 {
		t.Fatalf("revoke-others = %d %s", res.code, res.body)
	}
	for _, cl := range []*client{b, c} {
		if res := cl.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
			t.Fatalf("other session still valid: %d", res.code)
		}
	}
	if res := a.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 {
		t.Fatalf("current session = %d", res.code)
	}
}

func TestSessionListExcludesIdle(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	a := e.newClient("203.0.113.10")
	a.login("alice", testPassword)
	e.newClient("198.51.100.20").login("alice", testPassword)
	e.clock.Advance(20 * time.Minute)
	a.do(http.MethodGet, "/api/auth/me", nil) // keeps a alive
	e.clock.Advance(11 * time.Minute)         // b idle for 31 min
	if list := listSessions(t, a, "/api/me/sessions"); len(list) != 1 || !list[0].Current {
		t.Fatalf("sessions = %+v", list)
	}
}

func TestAdminSessions(t *testing.T) {
	e := newTestEnv(t)
	admin, _, _ := e.fullClient("root", "admin", "203.0.113.10")
	e.createUser("alice", "user")
	u := e.newClient("198.51.100.20")
	u.login("alice", testPassword)
	if res := u.do(http.MethodGet, "/api/admin/sessions", nil); res.code != 403 || res.errCode() != "forbidden" {
		t.Fatalf("user on admin route = %d %s", res.code, res.body)
	}
	if list := listSessions(t, admin, "/api/admin/sessions"); len(list) != 2 {
		t.Fatalf("all sessions = %+v", list)
	}
	path := fmt.Sprintf("/api/admin/sessions/%d", u.sessionID())
	if res := admin.do(http.MethodDelete, path, nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d", res.code)
	}
	e.grantSudo(admin)
	if res := admin.do(http.MethodDelete, path, nil); res.code != 204 {
		t.Fatalf("revoke = %d %s", res.code, res.body)
	}
	if res := u.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("revoked user session = %d", res.code)
	}
	if res := admin.do(http.MethodDelete, "/api/admin/sessions/999999", nil); res.code != 404 {
		t.Fatalf("unknown session = %d", res.code)
	}
}
