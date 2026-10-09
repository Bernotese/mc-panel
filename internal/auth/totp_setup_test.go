package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"mcpanel/internal/audit"
	"mcpanel/internal/notify"
	"mcpanel/internal/store"
)

// insertPasskeyRow adds a passkey row directly (counts as a 2FA method; not usable for login).
func (e *testEnv) insertPasskeyRow(userID int64, name string) int64 {
	e.t.Helper()
	credID, err := newToken()
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.db.Exec(`INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
		VALUES (?, ?, ?, '{}', 0)`, userID, []byte(credID), name)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestAdminSetupFlowWithTOTP(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	if m := c.login("root", testPassword).me(t); m.Stage != "setup" {
		t.Fatalf("stage = %s", m.Stage)
	}
	setupCookie, setupCSRF := c.cookie, c.csrf

	res := c.do(http.MethodPost, "/api/me/totp/setup", nil)
	if res.code != 200 {
		t.Fatalf("setup = %d %s", res.code, res.body)
	}
	var s totpSetupResponse
	if err := json.Unmarshal(res.body, &s); err != nil || len(s.Secret) != 32 ||
		!strings.HasPrefix(s.OtpauthURL, "otpauth://totp/") || !strings.Contains(s.OtpauthURL, "issuer=mc-panel") {
		t.Fatalf("setup response = %s (%v)", res.body, err)
	}
	var enc []byte
	var enabled int
	if err := e.db.QueryRow(`SELECT totp_secret_enc, totp_enabled FROM users WHERE id = ?`, uid).Scan(&enc, &enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || len(enc) == 0 || bytes.Contains(enc, []byte(s.Secret)) {
		t.Fatal("pending secret must be stored encrypted with totp_enabled = 0")
	}

	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": wrongCode(e.code(s.Secret))}); res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("wrong code = %d %s", res.code, res.body)
	}
	res = c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(s.Secret)})
	if res.code != 200 {
		t.Fatalf("enable = %d %s", res.code, res.body)
	}
	var rc recoveryCodesResponse
	if err := json.Unmarshal(res.body, &rc); err != nil || len(rc.Codes) != 10 {
		t.Fatalf("recovery codes = %s", res.body)
	}
	for _, code := range rc.Codes {
		if !recoveryFormat.MatchString(code) {
			t.Errorf("bad recovery code %q", code)
		}
	}
	if c.cookie == setupCookie || c.csrf != setupCSRF {
		t.Fatal("setup → full must rotate the session token and keep the CSRF token")
	}
	m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
	if m.Stage != "full" || !m.TOTPEnabled || m.RecoveryCodesRemaining != 10 || strings.Join(m.SecondFactors, ",") != "totp,recovery" {
		t.Fatalf("Me = %+v", m)
	}
	if !hasAction(e, audit.ActionTOTPEnable) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if got := e.notes.Types(); got[len(got)-1] != notify.Event2FAChanged {
		t.Fatalf("notifications = %v", got)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("root", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(s.Secret)}); res.code != 401 || e.guard.last().Reason != "replayed_code" {
		t.Fatalf("activation code must be consumed: %d", res.code)
	}
}

func TestEnableWithoutSetup(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": "123456"}); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("enable without setup = %d %s", res.code, res.body)
	}
}

func TestSetupRequiresSudoWhenUserHas2FA(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	e.insertPasskeyRow(uid, "key")
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("setup without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 200 {
		t.Fatalf("setup with sudo = %d %s", res.code, res.body)
	}
}

func TestSetupConflictWhenTOTPEnabled(t *testing.T) {
	e := newTestEnv(t)
	c, _, _ := e.fullClient("alice", "user", "203.0.113.10")
	e.grantSudo(c)
	if res := c.do(http.MethodPost, "/api/me/totp/setup", nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("setup while enabled = %d %s", res.code, res.body)
	}
}

func TestDisableTOTPLastFactorAdmin(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.fullClient("root", "admin", "203.0.113.10")
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("last factor of admin = %d %s", res.code, res.body)
	}
	var enabled int
	if err := e.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, uid).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("TOTP must stay enabled, enabled=%d err=%v", enabled, err)
	}
	e.insertPasskeyRow(uid, "backup key")
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 204 {
		t.Fatalf("with passkey left = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.TOTPEnabled || m.PasskeyCount != 1 {
		t.Fatalf("Me = %+v", m)
	}
	if !hasAction(e, audit.ActionTOTPDisable) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 {
		t.Fatalf("disable when not enabled = %d", res.code)
	}
}

func TestDisableTOTPEnforcedUser(t *testing.T) {
	for _, enforce := range []bool{false, true} {
		t.Run(fmt.Sprintf("enforce=%v", enforce), func(t *testing.T) {
			e := newTestEnv(t)
			c, uid, _ := e.fullClient("alice", "user", "203.0.113.10")
			if err := e.q.AuthInsertRecoveryCode(context.Background(), store.AuthInsertRecoveryCodeParams{
				UserID: uid, CodeHash: hashRecoveryCode("abcde-fghjk"), CreatedAt: 1,
			}); err != nil {
				t.Fatal(err)
			}
			if enforce {
				e.enforce2FAForUsers()
			}
			e.grantSudo(c)
			res := c.do(http.MethodDelete, "/api/me/totp", nil)
			if enforce {
				if res.code != 409 || res.errCode() != "conflict" {
					t.Fatalf("enforced user = %d %s", res.code, res.body)
				}
				return
			}
			if res.code != 204 {
				t.Fatalf("not enforced = %d %s", res.code, res.body)
			}
			m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
			if m.TOTPEnabled || len(m.SecondFactors) != 0 || m.RecoveryCodesRemaining != 0 {
				t.Fatalf("Me = %+v (recovery codes must be removed with the last method)", m)
			}
			var enc []byte
			if err := e.db.QueryRow(`SELECT totp_secret_enc FROM users WHERE id = ?`, uid).Scan(&enc); err != nil || enc != nil {
				t.Fatalf("secret must be removed, got %v %v", enc, err)
			}
		})
	}
}
