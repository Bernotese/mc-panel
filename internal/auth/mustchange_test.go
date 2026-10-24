package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
)

const forcedNewPassword = "a brand new passphrase"

// requirePasswordChange sets the flag an admin sets with a password (POST /users, PATCH /users/{id}).
func (e *testEnv) requirePasswordChange(userID int64) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE users SET must_change_password = 1 WHERE id = ?`, userID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) mustChangePassword(userID int64) int64 {
	e.t.Helper()
	var v int64
	if err := e.db.QueryRow(`SELECT must_change_password FROM users WHERE id = ?`, userID).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

// lastEntry returns the most recent audit entry with action.
func (e *testEnv) lastEntry(action string) audit.Entry {
	e.t.Helper()
	for i := len(e.audit.Entries) - 1; i >= 0; i-- {
		if e.audit.Entries[i].Action == action {
			return e.audit.Entries[i]
		}
	}
	e.t.Fatalf("no audit entry %s in %v", action, e.audit.Actions())
	return audit.Entry{}
}

// assertPasswordStage checks that the session is limited to changing the password (SEC-45).
func assertPasswordStage(t *testing.T, c *client) {
	t.Helper()
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 || res.me(t).Stage != "password" {
		t.Fatalf("Me = %d %s, want stage password", res.code, res.body)
	}
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/me/sessions"},
		{http.MethodGet, "/api/me/passkeys"},
		{http.MethodPatch, "/api/me"},
		{http.MethodPost, "/api/me/totp/setup"},
		{http.MethodPost, "/api/me/passkeys/begin"},
		{http.MethodPost, "/api/auth/sudo/totp"},
		{http.MethodGet, "/api/admin/sessions"},
	} {
		var body any
		if rt.method != http.MethodGet {
			body = map[string]string{"locale": "en", "code": "123456"}
		}
		if res := c.do(rt.method, rt.path, body); res.code != 403 || res.errCode() != "password_change_required" {
			t.Errorf("%s %s = %d %s, want 403 password_change_required", rt.method, rt.path, res.code, res.body)
		}
	}
}

func TestMustChangePasswordLimitsSession(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	e.requirePasswordChange(uid)
	other := e.newClient("198.51.100.20")
	if res := other.login("alice", testPassword); res.code != 200 {
		t.Fatalf("other login = %d", res.code)
	}
	c := e.newClient("203.0.113.10")
	res := c.login("alice", testPassword)
	if res.code != 200 || res.me(t).Stage != "password" {
		t.Fatalf("login = %d %s, want stage password", res.code, res.body)
	}
	assertPasswordStage(t, c)
	if _, err := e.h.sessions.Recheck(context.Background(), c.sessionID(), uid); err == nil {
		t.Fatal("Recheck must refuse a session in stage password (console streams)")
	}

	change := func(cur, next string) response {
		return c.do(http.MethodPost, "/api/me/password", map[string]string{"current_password": cur, "new_password": next})
	}
	if res := change(testPassword, testPassword); res.code != 400 || res.errCode() != "validation_failed" {
		t.Fatalf("same password = %d %s, want 400 validation_failed", res.code, res.body)
	}
	if e.mustChangePassword(uid) != 1 {
		t.Fatal("a refused change must keep the flag")
	}
	oldCookie, oldCSRF := c.cookie, c.csrf
	if res := change(testPassword, forcedNewPassword); res.code != 204 {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if c.cookie == oldCookie || c.cookie == "" {
		t.Fatal("the forced change must rotate the session token")
	}
	if c.csrf != oldCSRF {
		t.Fatal("the CSRF token stays (the response is not a Me object)")
	}
	if e.mustChangePassword(uid) != 0 {
		t.Fatal("flag must be cleared")
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "full" {
		t.Fatalf("stage after change = %s, want full", m.Stage)
	}
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 200 {
		t.Fatalf("full route after change = %d %s", res.code, res.body)
	}
	if res := other.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("other sessions must be revoked, got %d", res.code)
	}
	en := e.lastEntry(audit.ActionPasswordChange)
	if en.Details["forced"] != true {
		t.Fatalf("audit details = %v, want forced: true", en.Details)
	}
}

func TestMustChangePasswordLogout(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	e.requirePasswordChange(uid)
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodPost, "/api/auth/logout", nil); res.code != 204 {
		t.Fatalf("logout = %d %s", res.code, res.body)
	}
	if !hasAction(e, audit.ActionLogout) {
		t.Fatalf("logout after the password step is audited: %v", e.audit.Actions())
	}
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("after logout = %d", res.code)
	}
}

// Review Focus 4: mandatory 2FA without a method; the password comes first, then the setup.
func TestForcedChangeThenSetup(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	e.requirePasswordChange(uid)
	c := e.newClient("203.0.113.10")
	if res := c.login("root", testPassword); res.code != 200 || res.me(t).Stage != "password" {
		t.Fatalf("login = %d %s, want stage password", res.code, res.body)
	}
	assertPasswordStage(t, c)
	if res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": forcedNewPassword,
	}); res.code != 204 {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "setup" {
		t.Fatalf("stage after change = %s, want setup", m.Stage)
	}
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 403 || res.errCode() != "setup_2fa_required" {
		t.Fatalf("full route = %d %s, want setup_2fa_required", res.code, res.body)
	}
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 200 {
		t.Fatalf("totp setup = %d %s", res.code, res.body)
	}
}

// Review Focus 4: a user with 2FA goes preauth -> (code) -> password.
func TestForcedChangeAfterSecondFactor(t *testing.T) {
	e := newTestEnv(t)
	c, uid, secret := e.preauthClient("alice", "admin", "203.0.113.10")
	e.requirePasswordChange(uid)
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "preauth" {
		t.Fatalf("stage before the code = %s, want preauth", m.Stage)
	}
	if res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": forcedNewPassword,
	}); res.code != 401 {
		t.Fatalf("change in preauth = %d %s, want 401", res.code, res.body)
	}
	res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res.code != 200 || res.me(t).Stage != "password" {
		t.Fatalf("2fa = %d %s, want stage password", res.code, res.body)
	}
	assertPasswordStage(t, c)
	if res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": forcedNewPassword,
	}); res.code != 204 {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "full" {
		t.Fatalf("stage after change = %s, want full", m.Stage)
	}
}

func TestChangePasswordRejectsSamePassword(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	res := c.do(http.MethodPost, "/api/me/password", map[string]string{"current_password": testPassword, "new_password": testPassword})
	if res.code != 400 || res.errCode() != "validation_failed" {
		t.Fatalf("same password = %d %s, want 400 validation_failed", res.code, res.body)
	}
	if hasAction(e, audit.ActionPasswordChange) {
		t.Fatal("a refused change must not be audited as a change")
	}
}

func TestVoluntaryChangeIsNotForced(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	cookie := c.cookie
	if res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": forcedNewPassword,
	}); res.code != 204 {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if c.cookie != cookie {
		t.Fatal("a voluntary change keeps the session token")
	}
	if en := e.lastEntry(audit.ActionPasswordChange); en.Details["forced"] != nil {
		t.Fatalf("audit details = %v, want no forced", en.Details)
	}
}
