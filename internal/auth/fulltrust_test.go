package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// SEC-10: a full-trust permission on any server makes 2FA mandatory for a plain user.

func (e *testEnv) grant(userID int64, perm rbac.Permission) {
	e.t.Helper()
	sid := storetest.CreateServer(e.t, e.db, "srv-"+string(perm[len("server."):]))
	if _, err := e.db.Exec(`INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, sid, string(perm)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) revokeAll(userID int64) {
	e.t.Helper()
	if _, err := e.db.Exec(`DELETE FROM permissions WHERE user_id = ?`, userID); err != nil {
		e.t.Fatal(err)
	}
}

func TestFullTrustPermissionMakes2FAMandatoryAtLogin(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	e.grant(uid, rbac.PermServerFiles)
	c := e.newClient("203.0.113.10")
	m := c.login("alice", testPassword).me(t)
	if m.Stage != "setup" || m.TwoFactorReason != reasonPermissions {
		t.Fatalf("Me = stage %s reason %s, want setup/permissions", m.Stage, m.TwoFactorReason)
	}
}

func TestFullTrustPermissionGrantedWhileLoggedIn(t *testing.T) {
	for _, perm := range rbac.FullTrustPermissions {
		t.Run(string(perm), func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", "user")
			c := e.newClient("203.0.113.10")
			if m := c.login("alice", testPassword).me(t); m.Stage != "full" || m.TwoFactorReason != reasonNone {
				t.Fatalf("Me = stage %s reason %s, want full/none", m.Stage, m.TwoFactorReason)
			}
			e.grant(uid, perm)
			if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 403 || res.errCode() != "setup_2fa_required" {
				t.Fatalf("next request = %d %s, want 403 setup_2fa_required", res.code, res.body)
			}
			if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "setup" || m.TwoFactorReason != reasonPermissions {
				t.Fatalf("Me = stage %s reason %s, want setup/permissions", m.Stage, m.TwoFactorReason)
			}
			// No lock-out: the downgraded session can enrol a factor and is full again.
			if _, res := e.registerPasskey(c, "key"); res.code != 201 {
				t.Fatalf("setup via passkey = %d %s", res.code, res.body)
			}
			if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 200 {
				t.Fatalf("full route after setup = %d %s", res.code, res.body)
			}
		})
	}
}

func TestDowngradedFullTrustUserCanLogOut(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	e.grant(uid, rbac.PermServerConsole)
	if res := c.do(http.MethodPost, "/api/auth/logout", nil); res.code != 204 {
		t.Fatalf("logout = %d %s", res.code, res.body)
	}
}

func TestOtherPermissionsKeep2FAOptional(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	e.grant(uid, rbac.PermServerView)
	e.grant(uid, rbac.PermServerPower)
	c := e.newClient("203.0.113.10")
	if m := c.login("alice", testPassword).me(t); m.Stage != "full" || m.TwoFactorReason != reasonNone {
		t.Fatalf("Me = stage %s reason %s, want full/none", m.Stage, m.TwoFactorReason)
	}
}

func TestRevokedFullTrustPermissionMakes2FAOptionalAgain(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.fullClient("alice", "user", "203.0.113.10")
	e.grant(uid, rbac.PermServerPlugins)
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 {
		t.Fatalf("disable last factor while mandatory = %d %s, want 409", res.code, res.body)
	}
	e.revokeAll(uid)
	m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
	if m.Stage != "full" || m.TwoFactorReason != reasonNone || !m.TOTPEnabled {
		t.Fatalf("Me = %+v, want full, reason none, TOTP kept", m)
	}
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 204 {
		t.Fatalf("disable after revoke = %d %s, want 204", res.code, res.body)
	}
}

func TestTwoFactorReasons(t *testing.T) {
	t.Run("admin", func(t *testing.T) {
		e := newTestEnv(t)
		uid := e.createUser("root", "admin")
		e.grant(uid, rbac.PermServerFiles)
		if m := e.newClient("203.0.113.10").login("root", testPassword).me(t); m.Stage != "setup" || m.TwoFactorReason != reasonAdmin {
			t.Fatalf("Me = stage %s reason %s, want setup/admin", m.Stage, m.TwoFactorReason)
		}
	})
	t.Run("policy", func(t *testing.T) {
		e := newTestEnv(t)
		e.createUser("alice", "user")
		e.enforce2FAForUsers()
		if m := e.newClient("203.0.113.10").login("alice", testPassword).me(t); m.Stage != "setup" || m.TwoFactorReason != reasonPolicy {
			t.Fatalf("Me = stage %s reason %s, want setup/policy", m.Stage, m.TwoFactorReason)
		}
	})
}

// A session that waits in setup because of a full-trust permission continues as full once the
// permission is revoked (a password login would give a full session then, too). Enrolling the
// first method later rotates it to a stored full session instead of falling back to setup.
func TestRevokedFullTrustPermissionReleasesSetupSession(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	e.grant(uid, rbac.PermServerFiles)
	c := e.newClient("203.0.113.10")
	if m := c.login("alice", testPassword).me(t); m.Stage != "setup" {
		t.Fatalf("stage = %s, want setup", m.Stage)
	}
	e.revokeAll(uid)
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 200 {
		t.Fatalf("full route after revoke = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "full" || m.TwoFactorReason != reasonNone {
		t.Fatalf("Me = stage %s reason %s, want full/none", m.Stage, m.TwoFactorReason)
	}
	before := c.cookie
	if _, res := e.registerPasskey(c, "key"); res.code != 201 {
		t.Fatalf("register = %d %s", res.code, res.body)
	}
	if c.cookie == before {
		t.Fatal("the first method must rotate the stored setup session")
	}
	var stored string
	if err := e.db.QueryRow(`SELECT stage FROM sessions WHERE id = ?`, c.sessionID()).Scan(&stored); err != nil || stored != "full" {
		t.Fatalf("stored stage = %q %v, want full", stored, err)
	}
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 200 {
		t.Fatalf("full route after enrolment = %d %s", res.code, res.body)
	}
}

// A stored setup session keeps its stage while 2FA stays mandatory, and a user who already has a
// method is not released either (the login would ask for the second factor).
func TestSetupSessionStaysWhile2FAIsMandatory(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	e.grant(uid, rbac.PermServerConsole)
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 403 || res.errCode() != "setup_2fa_required" {
		t.Fatalf("full route = %d %s, want 403 setup_2fa_required", res.code, res.body)
	}
	e.insertPasskeyRow(uid, "elsewhere")
	e.revokeAll(uid)
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 403 || res.errCode() != "setup_2fa_required" {
		t.Fatalf("setup session of a user with a method = %d %s, want 403 setup_2fa_required", res.code, res.body)
	}
}

func TestDeleteLastPasskeyRefusedForFullTrustUser(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	_, res := e.registerPasskey(c, "key")
	var created passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created) != nil {
		t.Fatalf("register = %d %s", res.code, res.body)
	}
	e.grant(uid, rbac.PermServerBackups)
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "full" || m.TwoFactorReason != reasonPermissions {
		t.Fatalf("Me = stage %s reason %s, want full/permissions", m.Stage, m.TwoFactorReason)
	}
	e.grantSudo(c)
	path := fmt.Sprintf("/api/me/passkeys/%d", created.Passkey.ID)
	if res := c.do(http.MethodDelete, path, nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("last passkey of a full-trust user = %d %s, want 409 conflict", res.code, res.body)
	}
	e.revokeAll(uid)
	if res := c.do(http.MethodDelete, path, nil); res.code != 204 {
		t.Fatalf("after revoke = %d %s, want 204", res.code, res.body)
	}
}
