package auth

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"mcpanel/internal/audit"
)

// After 10 consecutive failed second-factor attempts within 15 minutes the user's second
// factor is locked for 15 minutes (429 rate_limited), across sessions and IPs; the lockout is
// audited as a denied login with reason 2fa_lockout.
func TestSecondFactorLockoutPerUser(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	bad := wrongCode(e.code(secret))
	for i := 0; i < 10; i++ {
		if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": bad}); res.code != 401 || res.errCode() != "invalid_code" {
			t.Fatalf("attempt %d = %d %s", i, res.code, res.body)
		}
		e.clock.Advance(20 * time.Second) // stays within the preauth session lifetime
	}
	// The correct code from a fresh session and another IP is refused now.
	c2 := e.newClient("198.51.100.20")
	if res := c2.login("alice", testPassword); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)})
	if res.code != http.StatusTooManyRequests || res.errCode() != "rate_limited" {
		t.Fatalf("locked: got %d %s", res.code, res.body)
	}
	if ra, _ := strconv.Atoi(res.header.Get("Retry-After")); ra < 1 || ra > 15*60 {
		t.Fatalf("Retry-After = %q", res.header.Get("Retry-After"))
	}
	var locked int
	for _, en := range e.audit.Entries {
		if en.Action == audit.ActionLogin && en.Result == audit.ResultDenied && en.Details["reason"] == "2fa_lockout" {
			locked++
		}
	}
	if locked != 1 {
		t.Fatalf("lockout audit entries = %d, want 1 (%v)", locked, e.audit.Actions())
	}
	e.clock.Advance(15 * time.Minute)
	c3 := e.newClient("198.51.100.20")
	if res := c3.login("alice", testPassword); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	if res := c3.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		t.Fatalf("after lockout expiry = %d %s", res.code, res.body)
	}
}

func TestSecondFactorFailuresOutsideWindowDoNotLock(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.fullClient("alice", "user", "203.0.113.10")
	// 12 failures, 2 minutes apart: never 10 within one 15-minute window
	for i := 0; i < 12; i++ {
		e.clock.Advance(2 * time.Minute)
		if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": "000000"}); res.code != 401 {
			t.Fatalf("attempt %d = %d %s", i, res.code, res.body)
		}
	}
	e.clock.Advance(30 * time.Second)
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		t.Fatalf("correct code = %d %s", res.code, res.body)
	}
}

func TestSecondFactorSuccessResetsCounter(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.fullClient("alice", "user", "203.0.113.10")
	sudoTOTP := func(code string) int {
		return c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": code}).code
	}
	for round := 0; round < 2; round++ {
		for i := 0; i < 9; i++ {
			if code := sudoTOTP("000000"); code != 401 {
				t.Fatalf("round %d attempt %d = %d", round, i, code)
			}
		}
		e.clock.Advance(30 * time.Second)
		if code := sudoTOTP(e.code(secret)); code != 200 {
			t.Fatalf("round %d: correct sudo code = %d", round, code)
		}
	}
	// 10 consecutive sudo failures lock the user's second factor
	for i := 0; i < 10; i++ {
		_ = sudoTOTP("000000")
	}
	e.clock.Advance(30 * time.Second)
	if code := sudoTOTP(e.code(secret)); code != http.StatusTooManyRequests {
		t.Fatalf("after 10 sudo failures = %d, want 429", code)
	}
}
