package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/store"
)

func (e *testEnv) preauthClient(username, role, ip string) (*client, int64, string) {
	e.t.Helper()
	uid := e.createUser(username, role)
	secret := e.enableTOTP(uid)
	c := e.newClient(ip)
	if res := c.login(username, testPassword); res.code != 200 || res.me(e.t).Stage != "preauth" {
		e.t.Fatalf("login = %d %s", res.code, res.body)
	}
	return c, uid, secret
}

// fullClient completes a TOTP login. The current TOTP step is consumed afterwards;
// advance the clock by 30 s before using the next code.
func (e *testEnv) fullClient(username, role, ip string) (*client, int64, string) {
	e.t.Helper()
	c, uid, secret := e.preauthClient(username, role, ip)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		e.t.Fatalf("2fa = %d %s", res.code, res.body)
	}
	return c, uid, secret
}

func hasAction(e *testEnv, action string) bool {
	for _, a := range e.audit.Actions() {
		if a == action {
			return true
		}
	}
	return false
}

// wrongCode changes the last digit of a valid code.
func wrongCode(code string) string {
	last := (code[5]-'0'+1)%10 + '0'
	return code[:5] + string(rune(last))
}

func TestTOTPLoginFlow(t *testing.T) {
	e := newTestEnv(t)
	c, uid, secret := e.preauthClient("alice", "admin", "203.0.113.10")
	preCookie, preCSRF := c.cookie, c.csrf
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 || res.me(t).Stage != "preauth" {
		t.Fatalf("me in preauth = %d %s", res.code, res.body)
	}
	if len(e.notes.Types()) != 0 {
		t.Fatalf("preauth must not notify: %v", e.notes.Types())
	}

	bad := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": wrongCode(e.code(secret))})
	if bad.code != 401 || bad.errCode() != "invalid_code" {
		t.Fatalf("wrong code = %d %s", bad.code, bad.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageTOTP || a.Success || a.Reason != "bad_code" {
		t.Fatalf("attempt = %+v", a)
	}

	res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res.code != 200 {
		t.Fatalf("2fa = %d %s", res.code, res.body)
	}
	if m := res.me(t); m.Stage != "full" || !m.TOTPEnabled || m.ID != uid {
		t.Fatalf("Me = %+v", m)
	}
	if c.cookie == preCookie || c.csrf == preCSRF {
		t.Fatal("token and CSRF token must rotate on preauth → full")
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = preCookie
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("pre-auth token must be invalid after rotation, got %d", res.code)
	}
	if a := e.guard.last(); a.Stage != defense.StageTOTP || !a.Success || a.Reason != "complete" || a.UserID == nil || *a.UserID != uid {
		t.Fatalf("attempt = %+v", a)
	}
	if !hasAction(e, audit.ActionLogin) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if got := e.notes.Types(); len(got) != 0 {
		t.Fatalf("stream A must not notify on login (new-device events come from stream B): %v", got)
	}
	e.clock.Advance(30 * time.Second)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 401 {
		t.Fatalf("2fa endpoint must reject full sessions, got %d", res.code)
	}
}

func TestTOTPReplayRejectedOnLogin(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	code := e.code(secret)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": code}); res.code != 200 {
		t.Fatalf("first use = %d", res.code)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": code}); res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("replay = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Reason != "replayed_code" {
		t.Fatalf("attempt = %+v", a)
	}
	e.clock.Advance(30 * time.Second)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		t.Fatalf("next step = %d %s", res.code, res.body)
	}
	older, err := totpCodeAt(secret, totpStep(e.clock.Now())-1) // still inside the ±1 window, but already superseded
	if err != nil {
		t.Fatal(err)
	}
	c3 := e.newClient("203.0.113.10")
	c3.login("alice", testPassword)
	if res := c3.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": older}); res.code != 401 || e.guard.last().Reason != "replayed_code" {
		t.Fatalf("older step must be rejected: %d %+v", res.code, e.guard.last())
	}
}

func TestPreAuthSessionExpires(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	e.clock.Advance(5 * time.Minute)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 401 || c.cookie != "" {
		t.Fatalf("expired preauth = %d cookie=%q", res.code, c.cookie)
	}
}

func TestSecondFactorGuard(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	e.guard.SecondFactorErr = &defense.RateLimitError{RetryAfter: 30 * time.Second}
	res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res.code != 429 || res.errCode() != "rate_limited" || res.header.Get("Retry-After") != "30" {
		t.Fatalf("rate limited = %d %s %q", res.code, res.body, res.header.Get("Retry-After"))
	}
	if e.guard.last().Stage != defense.StagePassword {
		t.Fatal("a blocked 2FA request must not be verified or recorded")
	}
	e.guard.SecondFactorErr = defense.ErrJailed
	if res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 403 || res.errCode() != "jailed" {
		t.Fatalf("jailed = %d %s", res.code, res.body)
	}
}

func TestRecoveryCodeLogin(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	c, uid, _ := e.preauthClient("alice", "user", "203.0.113.10")
	for _, code := range []string{"abcde-fghjk", "mnpqr-stuvw"} {
		if err := e.q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{UserID: uid, CodeHash: e.h.hashRecoveryCode(code), CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); strings.Join(m.SecondFactors, ",") != "totp,recovery" || m.RecoveryCodesRemaining != 2 {
		t.Fatalf("Me = %+v", m)
	}
	if res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "zzzzz-zzzzz"}); res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("wrong recovery code = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageRecovery || a.Reason != "bad_code" || a.Success {
		t.Fatalf("attempt = %+v", a)
	}
	res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "ABCDE FGHJK"})
	if res.code != 200 {
		t.Fatalf("recovery login = %d %s", res.code, res.body)
	}
	if m := res.me(t); m.Stage != "full" || m.RecoveryCodesRemaining != 1 {
		t.Fatalf("Me = %+v", m)
	}
	if !hasAction(e, audit.ActionRecoveryUsed) || !hasAction(e, audit.ActionLogin) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("alice", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 401 {
		t.Fatalf("recovery code must be one-time, got %d", res.code)
	}
}
