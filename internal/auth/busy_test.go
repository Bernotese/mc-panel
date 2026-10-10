package auth

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"mcpanel/internal/password"
)

// A saturated argon2 limiter must answer 429 rate_limited (Retry-After: 1) for known and
// unknown usernames alike, without recording a failed attempt (no password was checked).
func TestLoginArgonBusyReturns429(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	password.Warmup()
	c := e.newClient("203.0.113.10")
	unblock := password.BlockForTesting(10 * time.Millisecond)
	known := c.login("alice", testPassword)
	unknown := c.login("mallory", testPassword)
	unblock()
	for name, res := range map[string]response{"known": known, "unknown": unknown} {
		if res.code != http.StatusTooManyRequests || res.errCode() != "rate_limited" || res.header.Get("Retry-After") != "1" {
			t.Fatalf("%s: got %d %s Retry-After=%q", name, res.code, res.body, res.header.Get("Retry-After"))
		}
	}
	if !bytes.Equal(known.body, unknown.body) {
		t.Fatalf("bodies differ: %s vs %s", known.body, unknown.body)
	}
	if n := len(e.guard.Recorded()); n != 0 {
		t.Fatalf("busy logins must not be recorded as attempts, got %d", n)
	}
	if res := c.login("alice", testPassword); res.code != http.StatusOK {
		t.Fatalf("login after unblock = %d %s", res.code, res.body)
	}
}

func TestChangePasswordArgonBusyReturns429(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	if res := c.login("alice", testPassword); res.code != http.StatusOK {
		t.Fatalf("login = %d", res.code)
	}
	unblock := password.BlockForTesting(10 * time.Millisecond)
	res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": "a brand new passphrase",
	})
	unblock()
	if res.code != http.StatusTooManyRequests || res.errCode() != "rate_limited" || res.header.Get("Retry-After") != "1" {
		t.Fatalf("got %d %s", res.code, res.body)
	}
	for _, a := range e.guard.Recorded() {
		if !a.Success {
			t.Fatalf("busy password change must not record a failure: %+v", a)
		}
	}
}
