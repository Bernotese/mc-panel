package auth

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/defense"
	"mcpanel/internal/password"
)

func TestLoginUserWithout2FAGetsFullSession(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	res := c.login("alice", testPassword)
	if res.code != http.StatusOK {
		t.Fatalf("code = %d body = %s", res.code, res.body)
	}
	sc := res.header.Get("Set-Cookie")
	for _, part := range []string{"__Host-sid=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(sc, part) {
			t.Errorf("Set-Cookie %q missing %q", sc, part)
		}
	}
	m := res.me(t)
	if m.ID != uid || m.Username != "alice" || m.Role != "user" || m.Stage != "full" || m.Locale != "de" ||
		m.CSRFToken == "" || m.SecondFactors == nil || len(m.SecondFactors) != 0 || m.TOTPEnabled || m.SudoUntil != 0 {
		t.Fatalf("Me = %+v", m)
	}
	if res := c.do(http.MethodGet, "/api/auth/me", nil); res.code != 200 || res.me(t).Stage != "full" {
		t.Fatalf("GET /auth/me = %d %s", res.code, res.body)
	}
	a := e.guard.last()
	if a.Stage != defense.StagePassword || !a.Success || a.Reason != "complete" || a.UserID == nil || *a.UserID != uid ||
		a.IP.String() != "203.0.113.10" || a.UserAgent != "test-agent" || a.Username != "alice" {
		t.Fatalf("attempt = %+v", a)
	}
	if got := e.audit.Actions(); len(got) != 1 || got[0] != audit.ActionLogin {
		t.Fatalf("audit = %v", got)
	}
	var country string
	if err := e.db.QueryRow(`SELECT country FROM sessions WHERE user_id = ?`, uid).Scan(&country); err != nil || country != "DE" {
		t.Fatalf("session country = %q, %v", country, err)
	}
}

func TestLoginStages(t *testing.T) {
	cases := []struct {
		name, role    string
		totp, enforce bool
		stage         string
		factors       string
		reason        string
	}{
		{"admin without 2fa must set up", "admin", false, false, "setup", "", "complete"},
		{"user with enforcement must set up", "user", false, true, "setup", "", "complete"},
		{"user with totp", "user", true, false, "preauth", "totp", "second_factor_pending"},
		{"admin with totp", "admin", true, false, "preauth", "totp", "second_factor_pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("bob", tc.role)
			if tc.totp {
				e.enableTOTP(uid)
			}
			if tc.enforce {
				e.enforce2FAForUsers()
			}
			res := e.newClient("203.0.113.10").login("bob", testPassword)
			if res.code != 200 {
				t.Fatalf("code = %d %s", res.code, res.body)
			}
			m := res.me(t)
			if m.Stage != tc.stage || strings.Join(m.SecondFactors, ",") != tc.factors {
				t.Fatalf("stage=%s factors=%v", m.Stage, m.SecondFactors)
			}
			if a := e.guard.last(); a.Reason != tc.reason || !a.Success {
				t.Fatalf("attempt = %+v", a)
			}
			if tc.stage == "preauth" && len(e.audit.Entries) != 0 {
				t.Fatalf("preauth must not be audited as login: %v", e.audit.Actions())
			}
		})
	}
}

func TestLoginInvalidCredentialsIdentical(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	wrong := c.login("alice", "wrong password 123")
	unknown := c.login("mallory", testPassword)
	if wrong.code != 401 || unknown.code != 401 || wrong.errCode() != "invalid_credentials" || !bytes.Equal(wrong.body, unknown.body) {
		t.Fatalf("wrong=%d %s unknown=%d %s", wrong.code, wrong.body, unknown.code, unknown.body)
	}
	if c.cookie != "" {
		t.Fatal("failed login must not set a session cookie")
	}
	att := e.guard.Recorded()
	if len(att) != 2 || att[0].Reason != "bad_password" || att[0].Success || att[0].UserID == nil ||
		att[1].Reason != "unknown_user" || att[1].UserID != nil || att[1].Username != "mallory" {
		t.Fatalf("attempts = %+v", att)
	}
	if len(e.audit.Entries) != 0 {
		t.Fatalf("failed logins must not be audited: %v", e.audit.Actions())
	}
}

func TestLoginUnusablePasswordHash(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	if _, err := e.db.Exec(`UPDATE users SET password_hash = '!' WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	if res := e.newClient("203.0.113.10").login("alice", testPassword); res.code != 401 || res.errCode() != "invalid_credentials" {
		t.Fatalf("got %d %s", res.code, res.body)
	}
}

func TestLoginBadRequest(t *testing.T) {
	e := newTestEnv(t)
	c := e.newClient("203.0.113.10")
	if res := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "alice"}); res.code != 400 {
		t.Fatalf("missing password: %d", res.code)
	}
	if res := c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": "a", "password": "b", "x": "y"}); res.code != 400 {
		t.Fatalf("unknown field: %d", res.code)
	}
	if res := c.login(strings.Repeat("a", 65), testPassword); res.code != 400 {
		t.Fatalf("long username: %d", res.code)
	}
}

func TestLoginGuardErrors(t *testing.T) {
	cases := []struct {
		name, role      string
		loginErr, admin error
		code            int
		errCode, retry  string
	}{
		{"jailed", "user", defense.ErrJailed, nil, 403, "jailed", ""},
		{"ip blocked", "user", defense.ErrIPBlocked, nil, 403, "ip_blocked", ""},
		{"rate limited", "user", &defense.RateLimitError{RetryAfter: 1500 * time.Millisecond}, nil, 429, "rate_limited", "2"},
		{"admin outside allowlist", "admin", nil, defense.ErrAdminNetwork, 403, "forbidden", ""},
		{"admin network only applies to admins", "user", nil, defense.ErrAdminNetwork, 200, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.createUser("alice", tc.role)
			e.guard.LoginErr = tc.loginErr
			e.guard.AdminNetErr = tc.admin
			c := e.newClient("203.0.113.10")
			res := c.login("alice", testPassword)
			if res.code != tc.code || res.errCode() != tc.errCode || res.header.Get("Retry-After") != tc.retry {
				t.Fatalf("got %d %q retry=%q", res.code, res.body, res.header.Get("Retry-After"))
			}
			if tc.code != 200 && c.cookie != "" {
				t.Fatal("blocked login must not create a session")
			}
			if tc.errCode == "forbidden" && e.guard.last().Reason != "admin_network" {
				t.Fatalf("attempt = %+v", e.guard.last())
			}
		})
	}
}

func TestLoginRehashesOldHash(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, password.HashWithParams(testPassword, 19456, 2, 1), uid); err != nil {
		t.Fatal(err)
	}
	if res := e.newClient("203.0.113.10").login("alice", testPassword); res.code != 200 {
		t.Fatalf("login = %d", res.code)
	}
	var h string
	if err := e.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&h); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash not upgraded: %q", h)
	}
}

func TestLoginRevokesPresentedSession(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	old := c.cookie
	if res := c.login("alice", testPassword); res.code != 200 || c.cookie == old || c.cookie == "" {
		t.Fatalf("second login = %d", res.code)
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = old
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("session presented at login must be revoked, got %d", res.code)
	}
}

func TestLogout(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	old := c.cookie
	if res := c.do(http.MethodPost, "/api/auth/logout", nil); res.code != 204 || c.cookie != "" {
		t.Fatalf("logout = %d cookie=%q", res.code, c.cookie)
	}
	stale := e.newClient("203.0.113.10")
	stale.cookie = old
	if res := stale.do(http.MethodGet, "/api/auth/me", nil); res.code != 401 {
		t.Fatalf("after logout = %d", res.code)
	}
	if got := e.audit.Actions(); got[len(got)-1] != audit.ActionLogout {
		t.Fatalf("audit = %v", got)
	}
}

// New-device detection and EventNewDeviceLogin belong to stream B (inside
// LoginGuard.RecordAttempt). Stream A must report exactly one successful
// "complete" attempt per completed login and must not notify by itself.
func TestCompleteAttemptRecordedWithoutNotification(t *testing.T) {
	for _, role := range []string{"user", "admin"} { // user → full, admin without 2FA → setup
		t.Run(role, func(t *testing.T) {
			e := newTestEnv(t)
			uid := e.createUser("alice", role)
			c := e.newClient("203.0.113.10")
			c.login("alice", "wrong password 123")
			if res := c.login("alice", testPassword); res.code != 200 {
				t.Fatalf("login = %d %s", res.code, res.body)
			}
			var complete []defense.Attempt
			for _, a := range e.guard.Recorded() {
				if a.Success && a.Reason == "complete" {
					complete = append(complete, a)
				}
			}
			if len(complete) != 1 || complete[0].Stage != defense.StagePassword || complete[0].UserID == nil ||
				*complete[0].UserID != uid || complete[0].IP.String() != "203.0.113.10" || complete[0].Username != "alice" {
				t.Fatalf("complete attempts = %+v", complete)
			}
			if len(e.notes.Events) != 0 {
				t.Fatalf("stream A must not send notifications on login: %v", e.notes.Types())
			}
		})
	}
}
