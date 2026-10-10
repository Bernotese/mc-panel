package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"mcpanel/internal/audit"
	"mcpanel/internal/defense"
	"mcpanel/internal/store"
)

func TestUpdateLocale(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodPatch, "/api/me", map[string]string{"locale": "en"}); res.code != 200 || res.me(t).Locale != "en" {
		t.Fatalf("PATCH /me = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Locale != "en" {
		t.Fatalf("locale not persisted: %+v", m)
	}
	if res := c.do(http.MethodPatch, "/api/me", map[string]string{"locale": "fr"}); res.code != 400 || res.errCode() != "validation_failed" {
		t.Fatalf("invalid locale = %d %s", res.code, res.body)
	}
}

func TestChangePassword(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	other := e.newClient("198.51.100.20")
	other.login("alice", testPassword)
	change := func(cur, next string) response {
		return c.do(http.MethodPost, "/api/me/password", map[string]string{"current_password": cur, "new_password": next})
	}
	if res := change("not the password!", "a brand new passphrase"); res.code != 401 || res.errCode() != "invalid_credentials" {
		t.Fatalf("wrong current = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Reason != "bad_current_password" || a.Stage != defense.StagePassword || a.Success {
		t.Fatalf("attempt = %+v", a)
	}
	for _, weak := range []string{"short", "passwordpassword", "alice-is-the-best-123"} {
		if res := change(testPassword, weak); res.code != 400 || res.errCode() != "validation_failed" {
			t.Fatalf("weak %q = %d %s", weak, res.code, res.body)
		}
	}
	if res := change(testPassword, "a brand new passphrase"); res.code != 204 {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if res := other.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("other sessions must be revoked, got %d", res.code)
	}
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 {
		t.Fatalf("current session must survive, got %d", res.code)
	}
	if res := e.newClient("203.0.113.10").login("alice", "a brand new passphrase"); res.code != 200 {
		t.Fatalf("login with new password = %d", res.code)
	}
	if res := e.newClient("203.0.113.10").login("alice", testPassword); res.code != 401 {
		t.Fatalf("login with old password = %d", res.code)
	}
	if !hasAction(e, audit.ActionPasswordChange) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	e.guard.LoginErr = defense.ErrJailed
	if res := change("a brand new passphrase", "yet another passphrase"); res.code != 403 || res.errCode() != "jailed" {
		t.Fatalf("jailed = %d %s", res.code, res.body)
	}
}

func TestRegenerateRecoveryCodes(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	c, uid, _ := e.fullClient("alice", "user", "203.0.113.10")
	old := e.h.hashRecoveryCode("abcde-fghjk")
	if err := e.q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{UserID: uid, CodeHash: old, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if res := c.do(http.MethodPost, "/api/me/recovery-codes", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d", res.code)
	}
	e.grantSudo(c)
	res := c.do(http.MethodPost, "/api/me/recovery-codes", nil)
	var rc recoveryCodesResponse
	if res.code != 200 || json.Unmarshal(res.body, &rc) != nil || len(rc.Codes) != 10 {
		t.Fatalf("regenerate = %d %s", res.code, res.body)
	}
	if n, _ := e.q.AuthCountUnusedRecoveryCodes(ctx, uid); n != 10 {
		t.Fatalf("unused = %d", n)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 401 {
		t.Fatalf("old code must be invalid, got %d", res.code)
	}
	if res := c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": rc.Codes[0]}); res.code != 200 {
		t.Fatalf("new code = %d %s", res.code, res.body)
	}
	if !hasAction(e, audit.ActionRecoveryRegenerate) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

// Password update and revocation of the other sessions commit together: if revoking fails,
// the old password stays valid.
func TestChangePasswordIsAtomic(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	if res := c.login("alice", testPassword); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	if res := e.newClient("203.0.113.12").login("alice", testPassword); res.code != 200 { // another session to revoke
		t.Fatalf("second login = %d", res.code)
	}
	if _, err := e.db.Exec(`CREATE TRIGGER fail_revoke BEFORE UPDATE OF revoked_at ON sessions
		BEGIN SELECT RAISE(ABORT, 'revoke failed'); END`); err != nil {
		t.Fatal(err)
	}
	res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": "a brand new passphrase",
	})
	if res.code != http.StatusInternalServerError {
		t.Fatalf("change = %d %s, want 500", res.code, res.body)
	}
	if _, err := e.db.Exec(`DROP TRIGGER fail_revoke`); err != nil {
		t.Fatal(err)
	}
	if res := e.newClient("203.0.113.11").login("alice", testPassword); res.code != 200 {
		t.Fatalf("old password must still work after the failed change: %d", res.code)
	}
}
