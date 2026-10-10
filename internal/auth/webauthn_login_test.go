package auth

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/defense"
)

// passkeyUser creates a user with exactly one registered passkey (no TOTP).
func (e *testEnv) passkeyUser(username, role string) *softAuthenticator {
	e.t.Helper()
	e.createUser(username, role)
	c := e.newClient("203.0.113.10")
	c.login(username, testPassword)
	a, res := e.registerPasskey(c, "key")
	if res.code != 201 {
		e.t.Fatalf("register = %d %s", res.code, res.body)
	}
	return a
}

func (c *client) webauthnLogin(a *softAuthenticator) response {
	c.env.t.Helper()
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	if begin.code != 200 {
		c.env.t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	return c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": a.assert(begin.body)})
}

func TestWebAuthnLogin(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("198.51.100.20")
	if m := c.login("alice", testPassword).me(t); m.Stage != "preauth" || strings.Join(m.SecondFactors, ",") != "webauthn,recovery" {
		t.Fatalf("Me = %+v", m)
	}
	pre := c.cookie
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	if begin.code != 200 || !strings.Contains(string(begin.body), b64url.EncodeToString(a.credID)) {
		t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": a.assert(begin.body)})
	if res.code != 200 || res.me(t).Stage != "full" || c.cookie == pre {
		t.Fatalf("finish = %d %s", res.code, res.body)
	}
	if att := e.guard.last(); att.Stage != defense.StageWebAuthn || !att.Success || att.Reason != "complete" {
		t.Fatalf("attempt = %+v", att)
	}
	var lastUsed sql.NullInt64
	var credJSON string
	if err := e.db.QueryRow(`SELECT last_used_at, credential_json FROM webauthn_credentials`).Scan(&lastUsed, &credJSON); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Authenticator struct {
			SignCount uint32 `json:"signCount"`
		} `json:"authenticator"`
	}
	if err := json.Unmarshal([]byte(credJSON), &stored); err != nil || stored.Authenticator.SignCount != 1 ||
		lastUsed.Int64 != e.clock.Now().UnixMilli() {
		t.Fatalf("sign count = %d last_used = %v (%v)", stored.Authenticator.SignCount, lastUsed, err)
	}
}

func TestWebAuthnCloneDetected(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.webauthnLogin(a); res.code != 200 {
		t.Fatalf("first login = %d %s", res.code, res.body)
	}
	a.counter = 0 // cloned authenticator replays an old counter value
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.webauthnLogin(a); res.code != 401 || res.errCode() != "invalid_code" || e.guard.last().Reason != "clone_warning" {
		t.Fatalf("clone = %d %s %+v", res.code, res.body, e.guard.last())
	}
}

func TestWebAuthnCeremonyIsOneShotAndKindBound(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	assertion := a.assert(begin.body)
	if res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": json.RawMessage(`{"id":"x"}`)}); res.code != 401 || e.guard.last().Reason != "bad_assertion" {
		t.Fatalf("garbage = %d %+v", res.code, e.guard.last())
	}
	if res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": assertion}); res.code != 401 || e.guard.last().Reason != "no_ceremony" {
		t.Fatalf("ceremony must be consumed by the failed attempt: %d %+v", res.code, e.guard.last())
	}
	if res := c.webauthnLogin(a); res.code != 200 {
		t.Fatalf("fresh ceremony = %d %s", res.code, res.body)
	}
	sudoBegin := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil)
	if sudoBegin.code != 200 {
		t.Fatalf("sudo begin = %d", sudoBegin.code)
	}
	if _, err := e.db.Exec(`UPDATE sessions SET webauthn_state = replace(webauthn_state, '"kind":"sudo"', '"kind":"login"') WHERE id = ?`, c.sessionID()); err != nil {
		t.Fatal(err)
	}
	if res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": a.assert(sudoBegin.body)}); res.code != 401 || e.guard.last().Reason != "no_ceremony" {
		t.Fatalf("login ceremony must not grant sudo: %d %+v", res.code, e.guard.last())
	}
}

func TestSudoWebAuthn(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	if res := c.webauthnLogin(a); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	begin := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil)
	if begin.code != 200 {
		t.Fatalf("sudo begin = %d %s", begin.code, begin.body)
	}
	res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": a.assert(begin.body)})
	var s sudoResponse
	if res.code != 200 || json.Unmarshal(res.body, &s) != nil || s.SudoUntil != e.clock.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatalf("sudo finish = %d %s", res.code, res.body)
	}
	if att := e.guard.last(); att.Stage != defense.StageSudo || !att.Success {
		t.Fatalf("attempt = %+v", att)
	}
	var keys []passkeyJSON
	if err := json.Unmarshal(c.do(http.MethodGet, "/api/me/passkeys", nil).body, &keys); err != nil || len(keys) != 1 {
		t.Fatalf("keys = %v %v", keys, err)
	}
	if res := c.do(http.MethodDelete, fmt.Sprintf("/api/me/passkeys/%d", keys[0].ID), nil); res.code != 409 {
		t.Fatalf("sudo route after webauthn sudo = %d (want 409 last factor)", res.code)
	}
}

func TestSudoWebAuthnCeremonyExpires(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	c.webauthnLogin(a)
	begin := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil)
	e.clock.Advance(5*time.Minute + time.Second)
	if res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": a.assert(begin.body)}); res.code != 401 || e.guard.last().Reason != "no_ceremony" {
		t.Fatalf("expired ceremony = %d %+v", res.code, e.guard.last())
	}
	last := e.audit.Entries[len(e.audit.Entries)-1]
	if last.Action != audit.ActionSudo || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
}

func TestWebAuthnBeginWithoutPasskeys(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("login begin without passkeys = %d", res.code)
	}
	c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res := c.do(http.MethodPost, "/api/auth/sudo/webauthn/begin", nil); res.code != 409 {
		t.Fatalf("sudo begin without passkeys = %d", res.code)
	}
}
