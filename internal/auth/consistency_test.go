package auth

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// Credential consistency: authentication, password changes, credential maintenance and session
// revocation agree on the credential state that is current when they commit. The tests change
// that state at the test seams (afterPasswordCheck: after a password was verified; afterLoad:
// after the session loader authenticated the request) and check the outcome.

const otherPassword = "another fine passphrase"

// once runs f at most once (seams fire again for requests that f itself sends).
func once(f func()) func() {
	done := false
	return func() {
		if !done {
			done = true
			f()
		}
	}
}

// hashVerifies reports whether the stored password hash of uid matches pw.
func (e *testEnv) hashVerifies(uid int64, pw string) bool {
	e.t.Helper()
	var h string
	if err := e.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&h); err != nil {
		e.t.Fatal(err)
	}
	ok, _, err := password.Verify(pw, h)
	return ok && err == nil
}

// liveSessions counts the sessions of uid that are not revoked.
func (e *testEnv) liveSessions(uid int64) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, uid).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// adminSetsPassword writes a new password the way PATCH /users/{id} does: new hash, forced
// change, every session of the user revoked.
func (e *testEnv) adminSetsPassword(uid int64, pw string) {
	e.t.Helper()
	h, err := password.Hash(pw)
	if err != nil {
		e.t.Fatal(err)
	}
	now := e.clock.Now().UnixMilli()
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ?, must_change_password = 1, password_changed_at = ? WHERE id = ?`, h, now, uid); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, uid); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) changePassword(c *client, current, next string) response {
	return c.do(http.MethodPost, "/api/me/password", map[string]string{"current_password": current, "new_password": next})
}

func TestLoginWithSupersededPasswordCreatesNoSession(t *testing.T) {
	cases := map[string]func(e *testEnv, uid int64){
		"set by an admin": func(e *testEnv, uid int64) { e.adminSetsPassword(uid, otherPassword) },
		"changed by the user": func(e *testEnv, uid int64) {
			c := e.newClient("198.51.100.1")
			if res := c.login("alice", testPassword); res.code != 200 {
				t.Fatalf("login = %d", res.code)
			}
			if res := e.changePassword(c, testPassword, otherPassword); res.code != 204 {
				t.Fatalf("change = %d %s", res.code, res.body)
			}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", "user")
			e.h.afterPasswordCheck = once(func() { change(e, uid) })
			c := e.newClient("203.0.113.10")
			res := c.login("alice", testPassword)
			e.h.afterPasswordCheck = nil
			if res.code != 401 || res.errCode() != "invalid_credentials" || c.cookie != "" {
				t.Fatalf("login with superseded password = %d %s", res.code, res.body)
			}
			var n int
			if err := e.db.QueryRow(`SELECT count(*) FROM sessions WHERE ip = '203.0.113.10'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("a superseded password created %d session(s), err %v", n, err)
			}
			if !e.hashVerifies(uid, otherPassword) {
				t.Fatal("the new password must stay the current one")
			}
			if res := e.newClient("203.0.113.11").login("alice", otherPassword); res.code != 200 {
				t.Fatalf("login with new password = %d %s", res.code, res.body)
			}
		})
	}
}

func TestLoginRehashNeverReplacesNewerPassword(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, password.HashWithParams(testPassword, 19456, 2, 1), uid); err != nil {
		t.Fatal(err)
	}
	e.h.afterPasswordCheck = once(func() { e.adminSetsPassword(uid, otherPassword) })
	res := e.newClient("203.0.113.10").login("alice", testPassword)
	e.h.afterPasswordCheck = nil
	if res.code != 401 {
		t.Fatalf("login = %d %s", res.code, res.body)
	}
	if e.hashVerifies(uid, testPassword) || !e.hashVerifies(uid, otherPassword) {
		t.Fatal("the stored password must be the one set last")
	}
}

func TestConcurrentLoginsDuringRehashBothSucceed(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, password.HashWithParams(testPassword, 19456, 2, 1), uid); err != nil {
		t.Fatal(err)
	}
	e.h.afterPasswordCheck = once(func() { // another login of the same password upgrades the hash first
		if res := e.newClient("198.51.100.1").login("alice", testPassword); res.code != 200 {
			t.Errorf("inner login = %d %s", res.code, res.body)
		}
	})
	res := e.newClient("203.0.113.10").login("alice", testPassword)
	e.h.afterPasswordCheck = nil
	if res.code != 200 {
		t.Fatalf("login after a rehash of the same password = %d %s", res.code, res.body)
	}
	if !e.hashVerifies(uid, testPassword) {
		t.Fatal("password lost")
	}
}

func TestPasswordChangeNeedsCurrentCredentialState(t *testing.T) {
	cases := map[string]struct {
		change   func(e *testEnv, uid int64, self *client)
		wantCode int
		wantPW   string
	}{
		"changed from another session": {func(e *testEnv, uid int64, _ *client) {
			c := e.newClient("198.51.100.1")
			c.login("alice", testPassword)
			if res := e.changePassword(c, testPassword, otherPassword); res.code != 204 {
				t.Fatalf("change = %d %s", res.code, res.body)
			}
		}, 401, otherPassword},
		"set by an admin": {func(e *testEnv, uid int64, _ *client) { e.adminSetsPassword(uid, otherPassword) }, 401, otherPassword},
		"changed from the same session": {func(e *testEnv, uid int64, self *client) {
			if res := e.changePassword(self, testPassword, otherPassword); res.code != 204 {
				t.Fatalf("change = %d %s", res.code, res.body)
			}
		}, 409, otherPassword},
		"second factors reset by an admin": {func(e *testEnv, uid int64, _ *client) {
			if _, err := e.db.Exec(`UPDATE sessions SET revoked_at = 1 WHERE user_id = ?`, uid); err != nil {
				t.Fatal(err)
			}
		}, 401, testPassword},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", "user")
			c := e.newClient("203.0.113.10")
			c.login("alice", testPassword)
			e.h.afterPasswordCheck = once(func() { tc.change(e, uid, c) })
			res := e.changePassword(c, testPassword, forcedNewPassword)
			e.h.afterPasswordCheck = nil
			if res.code != tc.wantCode {
				t.Fatalf("change = %d %s, want %d", res.code, res.body, tc.wantCode)
			}
			if e.hashVerifies(uid, forcedNewPassword) || !e.hashVerifies(uid, tc.wantPW) {
				t.Fatal("a change based on a superseded credential state must not be written")
			}
		})
	}
}

func TestFirstFactorNeedsPasswordStillCurrent(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	secret := totpSetupSecret(t, c)
	e.h.afterPasswordCheck = once(func() {
		if res := e.changePassword(c, testPassword, otherPassword); res.code != 204 {
			t.Fatalf("change = %d %s", res.code, res.body)
		}
	})
	res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword})
	e.h.afterPasswordCheck = nil
	if res.code != 409 {
		t.Fatalf("enable with superseded password = %d %s", res.code, res.body)
	}
	var enabled int
	if err := e.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, uid).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("no factor may be written, enabled=%d err=%v", enabled, err)
	}
}

func TestCredentialWritesNeedActiveSession(t *testing.T) {
	type tc struct {
		setup  func(e *testEnv, uid int64, c *client) // optional
		method string
		path   func(e *testEnv, uid int64) string
		body   any
		check  func(e *testEnv, uid int64) // the credential state must be unchanged
	}
	totpEnabled := func(e *testEnv, uid int64) {
		var n int
		if err := e.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, uid).Scan(&n); err != nil || n != 1 {
			e.t.Fatalf("TOTP must stay enabled: %d %v", n, err)
		}
	}
	withTOTPAndSudo := func(e *testEnv, uid int64, c *client) {
		e.enableTOTP(uid)
		e.grantSudo(c)
	}
	cases := map[string]tc{
		"password change": {
			method: http.MethodPost, path: func(*testEnv, int64) string { return "/api/me/password" },
			body: map[string]string{"current_password": testPassword, "new_password": otherPassword},
			check: func(e *testEnv, uid int64) {
				if !e.hashVerifies(uid, testPassword) {
					e.t.Fatal("password changed")
				}
			},
		},
		"recovery codes": {
			setup: func(e *testEnv, uid int64, c *client) {
				withTOTPAndSudo(e, uid, c)
				if _, err := e.db.Exec(`INSERT INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, x'00', 0)`, uid); err != nil {
					e.t.Fatal(err)
				}
			},
			method: http.MethodPost, path: func(*testEnv, int64) string { return "/api/me/recovery-codes" },
			check: func(e *testEnv, uid int64) {
				var n int
				if err := e.db.QueryRow(`SELECT count(*) FROM recovery_codes WHERE user_id = ? AND code_hash = x'00'`, uid).Scan(&n); err != nil || n != 1 {
					e.t.Fatalf("recovery codes replaced: %d %v", n, err)
				}
			},
		},
		"totp disable": {
			setup:  withTOTPAndSudo,
			method: http.MethodDelete, path: func(*testEnv, int64) string { return "/api/me/totp" },
			check: totpEnabled,
		},
		"passkey delete": {
			setup: func(e *testEnv, uid int64, c *client) {
				withTOTPAndSudo(e, uid, c)
				if _, err := e.db.Exec(`INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at) VALUES (?, x'01', 'key', '{}', 0)`, uid); err != nil {
					e.t.Fatal(err)
				}
			},
			method: http.MethodDelete,
			path: func(e *testEnv, uid int64) string {
				var id int64
				if err := e.db.QueryRow(`SELECT id FROM webauthn_credentials WHERE user_id = ?`, uid).Scan(&id); err != nil {
					e.t.Fatal(err)
				}
				return "/api/me/passkeys/" + strconv.FormatInt(id, 10)
			},
			check: func(e *testEnv, uid int64) {
				var n int
				if err := e.db.QueryRow(`SELECT count(*) FROM webauthn_credentials WHERE user_id = ?`, uid).Scan(&n); err != nil || n != 1 {
					e.t.Fatalf("passkey removed: %d %v", n, err)
				}
			},
		},
		"revoke other sessions": {
			setup:  func(e *testEnv, uid int64, c *client) { e.newClient("198.51.100.1").login("alice", testPassword) },
			method: http.MethodPost, path: func(*testEnv, int64) string { return "/api/me/sessions/revoke-others" },
			check: func(e *testEnv, uid int64) {
				if n := e.liveSessions(uid); n != 1 {
					e.t.Fatalf("other session revoked by a revoked session: live = %d", n)
				}
			},
		},
		"revoke another session": {
			setup:  func(e *testEnv, uid int64, c *client) { e.newClient("198.51.100.1").login("alice", testPassword) },
			method: http.MethodDelete,
			path: func(e *testEnv, uid int64) string {
				var id int64
				if err := e.db.QueryRow(`SELECT max(id) FROM sessions WHERE user_id = ?`, uid).Scan(&id); err != nil {
					e.t.Fatal(err)
				}
				return "/api/me/sessions/" + strconv.FormatInt(id, 10)
			},
			check: func(e *testEnv, uid int64) {
				if n := e.liveSessions(uid); n != 1 {
					e.t.Fatalf("other session revoked by a revoked session: live = %d", n)
				}
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", "user")
			c := e.newClient("203.0.113.10")
			c.login("alice", testPassword)
			if tc.setup != nil {
				tc.setup(e, uid, c)
			}
			path := tc.path(e, uid)
			self := c.sessionID()
			e.afterLoad = func(r *http.Request) { // revoked after the loader authenticated the request
				if p, ok := principal.From(r.Context()); ok && p.SessionID == self {
					if _, err := e.db.Exec(`UPDATE sessions SET revoked_at = 1 WHERE id = ?`, self); err != nil {
						t.Error(err)
					}
				}
			}
			res := c.do(tc.method, path, tc.body)
			e.afterLoad = nil
			if res.code != 401 || res.errCode() != "unauthorized" {
				t.Fatalf("%s %s on a revoked session = %d %s", tc.method, path, res.code, res.body)
			}
			tc.check(e, uid)
		})
	}
}
