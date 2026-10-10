package auth

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
)

func TestSudoTOTPGrantsSudo(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.fullClient("root", "admin", "203.0.113.10")
	e.clock.Advance(30 * time.Second)
	before, csrf := c.cookie, c.csrf
	res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)})
	if res.code != 200 {
		t.Fatalf("sudo = %d %s", res.code, res.body)
	}
	var s sudoResponse
	if err := json.Unmarshal(res.body, &s); err != nil || s.SudoUntil != e.clock.Now().Add(10*time.Minute).UnixMilli() {
		t.Fatalf("sudo response = %s", res.body)
	}
	if c.cookie == before || c.csrf != csrf {
		t.Fatal("sudo must rotate the session token and keep the CSRF token")
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = before
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("pre-sudo token must be invalid, got %d", res.code)
	}
	if m := c.do(http.MethodGet, "/api/auth/me", nil).me(t); m.SudoUntil != s.SudoUntil || m.Stage != "full" {
		t.Fatalf("Me = %+v", m)
	}
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 409 {
		t.Fatalf("sudo route must pass rbac (409 = last factor), got %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageSudo || !a.Success || a.Reason != "sudo" {
		t.Fatalf("attempt = %+v", a)
	}
	if !hasAction(e, audit.ActionSudo) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestTOTPCodeNotReusableForSudo(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("root", "admin", "203.0.113.10")
	code := e.code(secret)
	if res := c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": code}); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": code})
	if res.code != 401 || res.errCode() != "invalid_code" {
		t.Fatalf("login code reused for sudo = %d %s", res.code, res.body)
	}
	if a := e.guard.last(); a.Stage != defense.StageSudo || a.Success || a.Reason != "replayed_code" {
		t.Fatalf("attempt = %+v", a)
	}
	last := e.audit.Entries[len(e.audit.Entries)-1]
	if last.Action != audit.ActionSudo || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
	e.clock.Advance(30 * time.Second)
	sudoCode := e.code(secret)
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": sudoCode}); res.code != 200 {
		t.Fatalf("fresh code = %d %s", res.code, res.body)
	}
	c2 := e.newClient("203.0.113.10")
	c2.login("root", testPassword)
	if res := c2.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": sudoCode}); res.code != 401 || e.guard.last().Reason != "replayed_code" {
		t.Fatalf("sudo code reused for login = %d", res.code)
	}
}

func TestSudoExpires(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.fullClient("root", "admin", "203.0.113.10")
	e.clock.Advance(30 * time.Second)
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)}); res.code != 200 {
		t.Fatalf("sudo = %d", res.code)
	}
	e.clock.Advance(10 * time.Minute)
	if res := c.do(http.MethodDelete, "/api/me/totp", nil); res.code != 403 || res.errCode() != "sudo_required" {
		t.Fatalf("expired sudo = %d %s", res.code, res.body)
	}
}

func TestSudoRequiresFullSession(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("root", "admin", "203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": e.code(secret)}); res.code != 401 {
		t.Fatalf("preauth sudo = %d", res.code)
	}
}
