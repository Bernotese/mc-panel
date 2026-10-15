package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// --- Fix round 1, item 1: second factors need sudo once another factor exists ---

func totpSetupSecret(t *testing.T, c *client) string {
	t.Helper()
	res := c.do(http.MethodPost, "/api/me/totp/setup", nil)
	var s totpSetupResponse
	if res.code != 200 || json.Unmarshal(res.body, &s) != nil || s.Secret == "" {
		t.Fatalf("totp setup = %d %s", res.code, res.body)
	}
	return s.Secret
}

func TestEnableTOTPAfterOtherFactorNeedsSudo(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	s1 := e.newClient("203.0.113.10")
	if m := s1.login("root", testPassword).me(t); m.Stage != "setup" {
		t.Fatalf("stage = %s", m.Stage)
	}
	secret := totpSetupSecret(t, s1)
	e.insertPasskeyRow(uid, "enrolled elsewhere") // the real admin enrolled a factor meanwhile
	res := s1.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword})
	if res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("enable with foreign factor present = %d %s", res.code, res.body)
	}
	var enabled int
	if err := e.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, uid).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("TOTP must not be enabled, enabled=%d err=%v", enabled, err)
	}
}

func TestPasskeyFinishAfterOtherFactorNeedsSudo(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	s1 := e.newClient("203.0.113.10")
	s1.login("root", testPassword)
	begin := s1.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin.code != 200 {
		t.Fatalf("begin = %d %s", begin.code, begin.body)
	}
	e.enableTOTP(uid) // the real admin enabled TOTP meanwhile
	a := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	res := s1.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "attacker", "credential": a.register(begin.body), "current_password": testPassword})
	if res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("finish with foreign factor present = %d %s", res.code, res.body)
	}
	if n, _ := e.q.AuthCountCredentials(context.Background(), uid); n != 0 {
		t.Fatalf("no credential may be stored, got %d", n)
	}
}

func TestFirstFactorRevokesOtherPendingSessions(t *testing.T) {
	for _, method := range []string{"passkey", "totp"} {
		t.Run(method, func(t *testing.T) {
			e := newTestEnv(t)
			e.createUser("root", "admin")
			s1, s2, s3 := e.newClient("198.51.100.1"), e.newClient("203.0.113.10"), e.newClient("198.51.100.3")
			for _, c := range []*client{s1, s2, s3} {
				if m := c.login("root", testPassword).me(t); m.Stage != "setup" {
					t.Fatalf("stage = %s", m.Stage)
				}
			}
			secret := totpSetupSecret(t, s1) // attacker prepares a pending secret in S1
			if method == "passkey" {
				if _, res := e.registerPasskey(s2, "key"); res.code != 201 {
					t.Fatalf("register = %d %s", res.code, res.body)
				}
			} else {
				s2secret := totpSetupSecret(t, s2)
				if res := s2.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(s2secret), "current_password": testPassword}); res.code != 200 {
					t.Fatalf("enable = %d %s", res.code, res.body)
				}
			}
			for name, c := range map[string]*client{"S1": s1, "S3": s3} {
				if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
					t.Fatalf("%s must be revoked once the first factor exists, got %d", name, res.code)
				}
			}
			if m := s2.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "full" {
				t.Fatalf("S2 = %+v", m)
			}
			_ = secret
		})
	}
}

func TestFirstFactorRevokesAllOtherSessions(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user") // no enforcement: full session without 2FA
	f1, f2 := e.newClient("198.51.100.1"), e.newClient("203.0.113.10")
	f1.login("alice", testPassword)
	f2.login("alice", testPassword)
	if _, res := e.registerPasskey(f2, "key"); res.code != 201 {
		t.Fatalf("register = %d", res.code)
	}
	// Every other session was authenticated by password only; none may outlive the first factor.
	if res := f1.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("other password-only session must be revoked, got %d", res.code)
	}
	if res := f2.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 {
		t.Fatalf("enrolling session must survive, got %d", res.code)
	}
}

func TestDowngradedFullSessionIsRevokedByFirstFactor(t *testing.T) {
	for _, tc := range []string{"promoted to admin", "enforcement switched on"} {
		for _, method := range []string{"passkey", "totp"} {
			t.Run(tc+"/"+method, func(t *testing.T) {
				e := newTestEnv(t)
				uid := e.createUser("alice", "user")
				old, fresh := e.newClient("198.51.100.1"), e.newClient("203.0.113.10")
				old.login("alice", testPassword) // stored stage: full
				if tc == "promoted to admin" {
					if _, err := e.db.Exec(`UPDATE users SET role = 'admin' WHERE id = ?`, uid); err != nil {
						t.Fatal(err)
					}
				} else {
					e.enforce2FAForUsers()
				}
				if m := fresh.login("alice", testPassword).me(t); m.Stage != "setup" {
					t.Fatalf("fresh stage = %s", m.Stage)
				}
				if method == "passkey" {
					if _, res := e.registerPasskey(fresh, "key"); res.code != 201 {
						t.Fatalf("register = %d %s", res.code, res.body)
					}
				} else {
					secret := totpSetupSecret(t, fresh)
					if res := fresh.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword}); res.code != 200 {
						t.Fatalf("enable = %d %s", res.code, res.body)
					}
				}
				if res := old.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
					t.Fatalf("stored-full session must not regain access without a second factor, got %d %s", res.code, res.body)
				}
			})
		}
	}
}

func TestPendingTOTPSecretExpires(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	secret := totpSetupSecret(t, c)
	e.clock.Advance(pendingTOTPTTL + time.Minute)
	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword}); res.code != 409 || res.errCode() != "conflict" {
		t.Fatalf("enable after expiry = %d %s", res.code, res.body)
	}
	secret = totpSetupSecret(t, c)
	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword}); res.code != 200 {
		t.Fatalf("enable after fresh setup = %d %s", res.code, res.body)
	}
}

func TestSecondMethodNeedsSudoInsideOneSession(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	secret := totpSetupSecret(t, c) // no factor yet: no sudo needed
	if _, res := e.registerPasskey(c, "key"); res.code != 201 {
		t.Fatalf("first factor = %d %s", res.code, res.body)
	}
	res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword})
	if res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("second method without sudo = %d %s", res.code, res.body)
	}
	e.grantSudo(c)
	if res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword}); res.code != 200 {
		t.Fatalf("second method with sudo = %d %s", res.code, res.body)
	}
}

// --- item 2: ErrIPBlocked is mapped to 403 ip_blocked by every guard check ---

func TestIPBlockedMappedEverywhere(t *testing.T) {
	want := func(t *testing.T, what string, res response) {
		t.Helper()
		if res.code != 403 || res.errCode() != "ip_blocked" {
			t.Fatalf("%s = %d %s, want 403 ip_blocked", what, res.code, res.body)
		}
	}
	t.Run("login", func(t *testing.T) {
		e := newTestEnv(t)
		e.createUser("alice", "user")
		e.guard.LoginErr = defense.ErrIPBlocked
		want(t, "login", e.newClient("203.0.113.10").login("alice", testPassword))
	})
	t.Run("admin network check", func(t *testing.T) {
		e := newTestEnv(t)
		e.createUser("root", "admin")
		e.guard.AdminNetErr = defense.ErrIPBlocked
		want(t, "admin login", e.newClient("203.0.113.10").login("root", testPassword))
	})
	t.Run("second factor endpoints", func(t *testing.T) {
		e := newTestEnv(t)
		c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
		e.guard.SecondFactorErr = defense.ErrIPBlocked
		want(t, "2fa totp", c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}))
		want(t, "2fa recovery", c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}))
		want(t, "2fa webauthn", c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": json.RawMessage(`{}`)}))
	})
	t.Run("sudo, enable and password change", func(t *testing.T) {
		e := newTestEnv(t)
		c, _, secret := e.fullClient("alice", "user", "203.0.113.10")
		e.clock.Advance(30 * time.Second)
		e.guard.SecondFactorErr = defense.ErrIPBlocked
		want(t, "sudo totp", c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)}))
		want(t, "sudo webauthn", c.do(http.MethodPost, "/api/auth/sudo/webauthn/finish", map[string]any{"credential": json.RawMessage(`{}`)}))
		e.guard.SecondFactorErr = nil
		e.guard.LoginErr = defense.ErrIPBlocked
		want(t, "password change", c.do(http.MethodPost, "/api/me/password",
			map[string]string{"current_password": testPassword, "new_password": "a brand new passphrase"}))
	})
	t.Run("enable totp", func(t *testing.T) {
		e := newTestEnv(t)
		e.createUser("root", "admin")
		c := e.newClient("203.0.113.10")
		c.login("root", testPassword)
		secret := totpSetupSecret(t, c)
		e.guard.SecondFactorErr = defense.ErrIPBlocked
		want(t, "enable", c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword}))
	})
}

// --- item 3: session rotation is a compare-and-swap ---

func TestRotateIsCompareAndSwap(t *testing.T) {
	s, _, _, uid := newTestSessions(t)
	ctx := context.Background()
	_, sess := mustCreate(t, s, uid, principal.StagePreAuth)
	if _, _, err := s.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, NewCSRF: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, NewCSRF: true}); err == nil {
		t.Fatal("rotating with a stale token hash must fail")
	}
}

func TestSecondFactorOnRevokedSessionYieldsNoSession(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.preauthClient("alice", "user", "203.0.113.10")
	if err := e.q.AuthInsertRecoveryCode(context.Background(), store.AuthInsertRecoveryCodeParams{
		UserID: uid, CodeHash: e.h.hashRecoveryCode("abcde-fghjk"), CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	e.guard.hook = func(key string) { // the session is revoked after the request passed authentication
		if _, err := e.db.Exec(`UPDATE sessions SET revoked_at = 1 WHERE id = ?`, key); err != nil {
			t.Error(err)
		}
	}
	res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"})
	if res.code != 401 || res.errCode() != "unauthorized" {
		t.Fatalf("recovery on revoked session = %d %s", res.code, res.body)
	}
	for _, a := range e.guard.Recorded() {
		if a.Reason == "complete" {
			t.Fatalf("no complete attempt without an issued session: %+v", a)
		}
	}
	if hasAction(e, audit.ActionLogin) {
		t.Fatal("no login audit entry without an issued session")
	}
}

func TestConcurrentSecondFactorCompletionYieldsOneSession(t *testing.T) {
	for i := 0; i < 4; i++ {
		e := newTestEnv(t)
		c, uid, _ := e.preauthClient("alice", "user", "203.0.113.10")
		codes := []string{"abcde-fghjk", "mnpqr-stuvw"}
		for _, code := range codes {
			if err := e.q.AuthInsertRecoveryCode(context.Background(), store.AuthInsertRecoveryCodeParams{
				UserID: uid, CodeHash: e.h.hashRecoveryCode(code), CreatedAt: 1,
			}); err != nil {
				t.Fatal(err)
			}
		}
		c1, c2 := *c, *c
		results := make([]int, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			results[0] = c1.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": codes[0]}).code
		}()
		go func() {
			defer wg.Done()
			results[1] = c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": codes[1]}).code
		}()
		wg.Wait()
		ok := 0
		for _, r := range results {
			if r == 200 {
				ok++
			}
		}
		complete := 0
		for _, a := range e.guard.Recorded() {
			if a.Reason == "complete" {
				complete++
			}
		}
		if ok != 1 || complete != 1 {
			t.Fatalf("round %d: results=%v complete attempts=%d, want exactly one of each", i, results, complete)
		}
	}
}

// --- item 5: recovery codes are never lost ---

func TestEnableTOTPReturnsCodesEvenIfPromotionFails(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	secret := totpSetupSecret(t, c)
	e.guard.hook = func(key string) { e.rotateSessionBehindTheLoader(key) } // promotion will lose the CAS
	res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword})
	var rc recoveryCodesResponse
	if res.code != 200 || json.Unmarshal(res.body, &rc) != nil || len(rc.Codes) != 10 {
		t.Fatalf("enable = %d %s", res.code, res.body)
	}
	if n, _ := e.q.AuthCountUnusedRecoveryCodes(context.Background(), uid); n != 10 {
		t.Fatalf("stored recovery codes = %d", n)
	}
}

// --- item 6: sign counter update is conditional ---

func TestCredentialUseUpdateIsConditional(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	id := e.insertPasskeyRow(uid, "key") // credential_json = '{}'
	ctx := context.Background()
	upd := func(old, next string) int64 {
		n, err := e.q.AuthUpdateCredentialUse(ctx, store.AuthUpdateCredentialUseParams{
			CredentialJson: next, OldCredentialJson: old, LastUsedAt: sql.NullInt64{Int64: 1, Valid: true}, ID: id,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := upd("{}", `{"a":1}`); n != 1 {
		t.Fatalf("first update rows = %d", n)
	}
	if n := upd("{}", `{"a":2}`); n != 0 {
		t.Fatalf("stale update rows = %d, want 0", n)
	}
}

func TestWebAuthnConcurrentAssertionsWithSameCounter(t *testing.T) {
	e := newTestEnv(t)
	a := e.passkeyUser("alice", "user")
	c1, c2 := e.newClient("203.0.113.10"), e.newClient("198.51.100.20")
	c1.login("alice", testPassword)
	c2.login("alice", testPassword)
	b1 := c1.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	b2 := c2.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	a1, a2 := *a, *a // identical counters
	cred1, cred2 := a1.assert(b1.body), a2.assert(b2.body)
	results := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = c1.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": cred1}).code
	}()
	go func() {
		defer wg.Done()
		results[1] = c2.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": cred2}).code
	}()
	wg.Wait()
	ok := 0
	for _, r := range results {
		if r == 200 {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("results = %v, want exactly one success", results)
	}
}

func TestWebAuthnAssertionWithForeignCredentialRejected(t *testing.T) {
	e := newTestEnv(t)
	e.passkeyUser("alice", "user")
	bobKey := e.passkeyUser("bob", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	begin := c.do(http.MethodPost, "/api/auth/2fa/webauthn/begin", nil)
	res := c.do(http.MethodPost, "/api/auth/2fa/webauthn/finish", map[string]any{"credential": bobKey.assert(begin.body)})
	if res.code != 401 || e.guard.last().Reason != "bad_assertion" {
		t.Fatalf("foreign credential = %d %+v", res.code, e.guard.last())
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "preauth" {
		t.Fatalf("session must stay preauth, got %s", m.Stage)
	}
}

// --- item 7: mandatory 2FA is enforced on existing full sessions ---

func TestFullSessionWithoutMandatory2FAIsDowngradedToSetup(t *testing.T) {
	for _, tc := range []string{"promoted to admin", "enforcement switched on"} {
		t.Run(tc, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", "user")
			c := e.newClient("203.0.113.10")
			if m := c.login("alice", testPassword).me(t); m.Stage != "full" {
				t.Fatalf("stage = %s", m.Stage)
			}
			if tc == "promoted to admin" {
				if _, err := e.db.Exec(`UPDATE users SET role = 'admin' WHERE id = ?`, uid); err != nil {
					t.Fatal(err)
				}
			} else {
				e.enforce2FAForUsers()
			}
			if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "setup" {
				t.Fatalf("Me.stage = %s, want setup", m.Stage)
			}
			if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 403 || res.errCode() != "setup_2fa_required" {
				t.Fatalf("full route = %d %s", res.code, res.body)
			}
			if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": "123456"}); res.code != 403 || res.errCode() != "setup_2fa_required" {
				t.Fatalf("sudo = %d %s", res.code, res.body)
			}
			if _, res := e.registerPasskey(c, "key"); res.code != 201 {
				t.Fatalf("setup via passkey = %d %s", res.code, res.body)
			}
			if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.Stage != "full" {
				t.Fatalf("Me.stage after setup = %s", m.Stage)
			}
			if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code != 200 {
				t.Fatalf("full route after setup = %d %s", res.code, res.body)
			}
		})
	}
}

func TestSetupSessionIsBlockedFromFullRoutes(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("root", "admin")
	c := e.newClient("203.0.113.10")
	c.login("root", testPassword)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/api/me/sessions"},
		{http.MethodGet, "/api/me/passkeys"},
		{http.MethodPost, "/api/auth/sudo/totp"},
		{http.MethodPost, "/api/auth/sudo/webauthn/begin"},
		{http.MethodPost, "/api/me/password"},
	} {
		var body any
		if rt.method == http.MethodPost {
			body = map[string]string{"code": "123456"}
		}
		if res := c.do(rt.method, rt.path, body); res.code != 403 || res.errCode() != "setup_2fa_required" {
			t.Errorf("%s %s = %d %s", rt.method, rt.path, res.code, res.body)
		}
	}
}

// --- item 8: preauth logout is not audited ---

func TestPreAuthLogoutIsNotAudited(t *testing.T) {
	e := newTestEnv(t)
	c, _, _ := e.preauthClient("alice", "user", "203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/logout", nil); res.code != 204 {
		t.Fatalf("logout = %d", res.code)
	}
	if len(e.audit.Entries) != 0 {
		t.Fatalf("preauth logout must not flood the audit chain: %v", e.audit.Actions())
	}
}

// --- item 9: duplicate credential id ---

func TestDuplicateCredentialIDIsBadRequest(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	e.createUser("bob", "user")
	ca, cb := e.newClient("203.0.113.10"), e.newClient("198.51.100.20")
	ca.login("alice", testPassword)
	cb.login("bob", testPassword)
	a, res := e.registerPasskey(ca, "key")
	if res.code != 201 {
		t.Fatalf("alice = %d %s", res.code, res.body)
	}
	b := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	b.credID = a.credID // same credential id, different key pair
	begin := cb.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	res = cb.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "dup", "credential": b.register(begin.body), "current_password": testPassword})
	if res.code != 400 || res.errCode() != "bad_request" {
		t.Fatalf("duplicate credential id = %d %s", res.code, res.body)
	}
}

// --- item 10: user-agent truncation ---

func TestUserAgentTruncatesAtRuneBoundary(t *testing.T) {
	ua := strings.Repeat("a", maxUserAgent-1) + "é" + "tail" // é straddles the byte limit
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("User-Agent", ua)
	got := userAgent(r)
	if !utf8.ValidString(got) || len(got) > maxUserAgent || !strings.HasPrefix(ua, got) {
		t.Fatalf("userAgent produced %d bytes, valid utf-8 = %v", len(got), utf8.ValidString(got))
	}
	r.Header.Set("User-Agent", "short")
	if userAgent(r) != "short" {
		t.Fatal("short user agents must stay unchanged")
	}
}

// --- fix round 2: rotation is bound to the token the client presented ---

func TestRotationBoundToPresentedToken(t *testing.T) {
	t.Run("second factor", func(t *testing.T) {
		e := newTestEnv(t)
		c, uid, _ := e.preauthClient("alice", "user", "203.0.113.10")
		if err := e.q.AuthInsertRecoveryCode(context.Background(), store.AuthInsertRecoveryCodeParams{
			UserID: uid, CodeHash: e.h.hashRecoveryCode("abcde-fghjk"), CreatedAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
		e.guard.hook = func(key string) { e.rotateSessionBehindTheLoader(key) }
		res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"})
		if res.code != 401 || res.errCode() != "unauthorized" {
			t.Fatalf("second factor with a superseded token = %d %s", res.code, res.body)
		}
		for _, a := range e.guard.Recorded() {
			if a.Reason == "complete" {
				t.Fatalf("no complete attempt expected: %+v", a)
			}
		}
	})
	t.Run("sudo", func(t *testing.T) {
		e := newTestEnv(t)
		c, _, secret := e.fullClient("root", "admin", "203.0.113.10")
		e.clock.Advance(30 * time.Second)
		e.guard.hook = func(key string) { e.rotateSessionBehindTheLoader(key) }
		res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)})
		if res.code != 401 || res.errCode() != "unauthorized" {
			t.Fatalf("sudo with a superseded token = %d %s", res.code, res.body)
		}
	})
	t.Run("setup promotion", func(t *testing.T) {
		e := newTestEnv(t)
		e.createUser("root", "admin")
		c := e.newClient("203.0.113.10")
		c.login("root", testPassword)
		secret := totpSetupSecret(t, c)
		e.guard.hook = func(key string) { e.rotateSessionBehindTheLoader(key) }
		res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword})
		if res.code != 200 {
			t.Fatalf("enable = %d %s (codes must still be returned)", res.code, res.body)
		}
		if strings.Contains(res.header.Get("Set-Cookie"), CookieName+"=") && c.cookie != "" {
			t.Fatal("a superseded token must not receive a promoted session cookie")
		}
	})
}

// rotateSessionBehindTheLoader simulates a concurrent request that rotated the session token
// after this request's loader had already authenticated it.
func (e *testEnv) rotateSessionBehindTheLoader(key string) {
	e.t.Helper()
	id, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		e.t.Error(err)
		return
	}
	sess, err := e.q.AuthGetSessionByID(context.Background(), id)
	if err != nil {
		e.t.Error(err)
		return
	}
	if _, _, err := e.h.Sessions().Rotate(context.Background(), sess, Rotation{Stage: principal.Stage(sess.Stage), SudoUntil: sess.SudoUntil}); err != nil {
		e.t.Error(err)
	}
}

// --- fix round 3 ---

func TestLoginStageDecisionAndInsertAreAtomicWithEnrolment(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user") // not enforced: a password-only login yields a full session
	enrolling := e.newClient("198.51.100.1")
	enrolling.login("alice", testPassword)
	a := newSoftAuthenticator(t, e.cfg.RPID(), e.cfg.Origin())
	begin := enrolling.do(http.MethodPost, "/api/me/passkeys/begin", nil)
	if begin.code != 200 {
		t.Fatalf("begin = %d", begin.code)
	}
	cred := a.register(begin.body)
	var wg sync.WaitGroup
	e.h.afterStageDecision = func() { // the first factor is enrolled while the login is deciding
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res := enrolling.do(http.MethodPost, "/api/me/passkeys/finish", map[string]any{"name": "key", "credential": cred, "current_password": testPassword}); res.code != 201 {
				t.Errorf("enrolment = %d %s", res.code, res.body)
			}
		}()
		time.Sleep(200 * time.Millisecond) // give the enrolment every chance to run first
	}
	c := e.newClient("203.0.113.10")
	res := c.login("alice", testPassword)
	e.h.afterStageDecision = nil
	wg.Wait()
	if res.code != 200 {
		t.Fatalf("login = %d %s", res.code, res.body)
	}
	if n, _ := e.q.AuthCountCredentials(context.Background(), uid); n != 1 {
		t.Fatalf("enrolment did not happen: %d", n)
	}
	// A password-only session must never survive the enrolment as a usable full session.
	if res := c.do(http.MethodGet, "/api/me/sessions", nil); res.code == 200 {
		t.Fatalf("password-only session usable after enrolment: %s", res.body)
	}
}

func TestEnrolmentRequiresActiveSession(t *testing.T) {
	t.Run("totp enable on revoked session", func(t *testing.T) {
		e := newTestEnv(t)
		uid := e.createUser("root", "admin")
		c := e.newClient("203.0.113.10")
		c.login("root", testPassword)
		secret := totpSetupSecret(t, c)
		e.guard.hook = func(key string) { // revoked after the loader authenticated the request
			if _, err := e.db.Exec(`UPDATE sessions SET revoked_at = 1 WHERE id = ?`, key); err != nil {
				t.Error(err)
			}
		}
		res := c.do(http.MethodPost, "/api/me/totp/enable", map[string]string{"code": e.code(secret), "current_password": testPassword})
		if res.code != 401 || res.errCode() != "unauthorized" {
			t.Fatalf("enable on revoked session = %d %s", res.code, res.body)
		}
		var enabled int
		if err := e.db.QueryRow(`SELECT totp_enabled FROM users WHERE id = ?`, uid).Scan(&enabled); err != nil || enabled != 0 {
			t.Fatalf("no factor may be written, enabled=%d err=%v", enabled, err)
		}
	})
	t.Run("guardAddFactor", func(t *testing.T) {
		e := newTestEnv(t)
		uid := e.createUser("alice", "user")
		c := e.newClient("203.0.113.10")
		c.login("alice", testPassword)
		p := principal.Principal{UserID: uid, SessionID: c.sessionID(), Stage: principal.StageFull}
		check := func() error {
			return e.h.withTx(context.Background(), func(q *store.Queries) error {
				return e.h.guardAddFactor(context.Background(), q, p, uid, true)
			})
		}
		if err := check(); err != nil {
			t.Fatalf("active session: %v", err)
		}
		// The first factor needs the verified password, also when a method vanished meanwhile.
		if err := e.h.withTx(context.Background(), func(q *store.Queries) error {
			return e.h.guardAddFactor(context.Background(), q, p, uid, false)
		}); !errors.Is(err, errPasswordRequired) {
			t.Fatalf("first factor without password: err = %v, want errPasswordRequired", err)
		}
		e.clock.Advance(13 * time.Hour) // absolute lifetime over
		if err := check(); !errors.Is(err, ErrNoSession) {
			t.Fatalf("expired session: err = %v, want ErrNoSession", err)
		}
		e.clock.Advance(-13 * time.Hour)
		if _, err := e.db.Exec(`UPDATE sessions SET revoked_at = 1 WHERE id = ?`, p.SessionID); err != nil {
			t.Fatal(err)
		}
		if err := check(); !errors.Is(err, ErrNoSession) {
			t.Fatalf("revoked session: err = %v, want ErrNoSession", err)
		}
	})
}
