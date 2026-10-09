package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"mcpanel/internal/audit"
	"mcpanel/internal/notify"
)

func TestPasskeyRegistrationAndList(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)

	begin := c.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin.code != 200 {
		t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	var opts struct {
		PublicKey struct {
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(begin.body, &opts); err != nil || opts.PublicKey.RP.ID != "localhost" || opts.PublicKey.User.Name != "alice" {
		t.Fatalf("creation options = %s", begin.body)
	}
	if res := c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": " ", "credential": json.RawMessage(`{}`)}); res.code != 400 || res.errCode() != "validation_failed" {
		t.Fatalf("empty name = %d %s", res.code, res.body)
	}
	a := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	res := c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "  YubiKey  ", "credential": a.register(begin.body)})
	if res.code != 201 {
		t.Fatalf("finish = %d %s", res.code, res.body)
	}
	var created passkeyCreatedResponse
	if err := json.Unmarshal(res.body, &created); err != nil || created.Passkey.Name != "YubiKey" ||
		created.Passkey.LastUsedAt != nil || len(created.RecoveryCodes) != 10 {
		t.Fatalf("created = %s", res.body)
	}
	var credID []byte
	var credJSON string
	if err := e.db.QueryRow(`SELECT credential_id, credential_json FROM webauthn_credentials WHERE user_id = ?`, uid).Scan(&credID, &credJSON); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(credID, a.credID) || !strings.Contains(credJSON, "publicKey") {
		t.Fatalf("stored credential id=%x json=%s", credID, credJSON)
	}
	if res := c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "again", "credential": a.register(begin.body)}); res.code != 400 {
		t.Fatalf("ceremony state must be one-shot, got %d", res.code)
	}
	var keys []passkeyJSON
	if res := c.do(http.MethodGet, "/api/me/passkeys", nil); res.code != 200 || json.Unmarshal(res.body, &keys) != nil ||
		len(keys) != 1 || keys[0].ID != created.Passkey.ID {
		t.Fatalf("list = %d %s", res.code, res.body)
	}
	if !hasAction(e, audit.ActionPasskeyAdd) || !slices.Contains(e.notes.Types(), notify.Event2FAChanged) {
		t.Fatalf("audit=%v notes=%v", e.audit.Actions(), e.notes.Types())
	}

	if res := c.do(http.MethodPost, "/api/me/passkeys/begin", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("second passkey without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	begin2 := c.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin2.code != 200 || !strings.Contains(string(begin2.body), b64url.EncodeToString(a.credID)) {
		t.Fatalf("existing credential must be in excludeCredentials: %s", begin2.body)
	}
	b := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	res = c.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "Phone", "credential": b.register(begin2.body)})
	var created2 passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created2) != nil || created2.RecoveryCodes == nil || len(created2.RecoveryCodes) != 0 {
		t.Fatalf("second passkey = %d %s (recovery_codes must be [])", res.code, res.body)
	}
}

func TestAdminSetupWithPasskeyPromotesSession(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	if _, res := e.registerPasskey(c, "key"); res.code != 201 {
		t.Fatalf("register = %d %s", res.code, res.body)
	}
	m := c.do(http.MethodGet, "/api/auth/me", nil).me(t)
	if m.Stage != "full" || m.PasskeyCount != 1 || strings.Join(m.SecondFactors, ",") != "webauthn,recovery" {
		t.Fatalf("Me = %+v", m)
	}
}

func TestDeleteLastPasskeyOfAdmin(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	_, res := e.registerPasskey(c, "key")
	var created passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created) != nil {
		t.Fatalf("register = %d", res.code)
	}
	path := fmt.Sprintf("/api/me/passkeys/%d", created.Passkey.ID)
	if res := c.do(http.MethodDelete, path, nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("without sudo = %d", res.code)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, path, nil); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("last passkey of admin = %d %s", res.code, res.body)
	}
	bob := e.createUser("bob", "user")
	bobKey := e.insertPasskeyRow(bob, "bob's key")
	for _, p := range []string{"/api/me/passkeys/999", "/api/me/passkeys/abc", fmt.Sprintf("/api/me/passkeys/%d", bobKey)} {
		if res := c.do(http.MethodDelete, p, nil); res.code != 404 {
			t.Fatalf("DELETE %s = %d", p, res.code)
		}
	}
	e.insertPasskeyRow(uid, "backup")
	if res := c.do(http.MethodDelete, path, nil); res.code != 204 {
		t.Fatalf("with backup key = %d %s", res.code, res.body)
	}
	var keys []passkeyJSON
	if res := c.do(http.MethodGet, "/api/me/passkeys", nil); json.Unmarshal(res.body, &keys) != nil || len(keys) != 1 || keys[0].Name != "backup" {
		t.Fatalf("list = %s", res.body)
	}
	if !hasAction(e, audit.ActionPasskeyRemove) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestDeletePasskeyOfUserRemovesRecoveryCodes(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	_, res := e.registerPasskey(c, "key")
	var created passkeyCreatedResponse
	if res.code != 201 || json.Unmarshal(res.body, &created) != nil {
		t.Fatalf("register = %d", res.code)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodDelete, fmt.Sprintf("/api/me/passkeys/%d", created.Passkey.ID), nil); res.code != 204 {
		t.Fatalf("delete = %d %s", res.code, res.body)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.PasskeyCount != 0 || m.RecoveryCodesRemaining != 0 || len(m.SecondFactors) != 0 {
		t.Fatalf("Me = %+v", m)
	}
}

func TestConcurrentFactorRemovalKeepsOne(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.fullClient("root", "admin", "203.0.113.10")
	keyID := e.insertPasskeyRow(uid, "key")
	e.grantSudo(c)
	c1, c2 := *c, *c
	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); codes[0] = c1.do(http.MethodDelete, "/api/me/totp", nil).code }()
	go func() {
		defer wg.Done()
		codes[1] = c2.do(http.MethodDelete, fmt.Sprintf("/api/me/passkeys/%d", keyID), nil).code
	}()
	wg.Wait()
	slices.Sort(codes)
	if codes[0] != 204 || codes[1] != 409 {
		t.Fatalf("codes = %v, want exactly one 204 and one 409", codes)
	}
	var totp, keys int
	if err := e.db.QueryRow(`SELECT totp_enabled, (SELECT count(*) FROM webauthn_credentials WHERE user_id = ?) FROM users WHERE id = ?`, uid, uid).Scan(&totp, &keys); err != nil {
		t.Fatal(err)
	}
	if totp+keys != 1 {
		t.Fatalf("admin must keep exactly one factor, totp=%d keys=%d", totp, keys)
	}
}
