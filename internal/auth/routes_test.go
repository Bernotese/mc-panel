package auth

import (
	"net/http"
	"testing"

	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
)

func TestRoutesMatchContract(t *testing.T) {
	e := newTestEnv(t)
	anyStage := rbac.Authenticated().AllowStages(principal.StagePreAuth, principal.StageSetup, principal.StageFull)
	preAuth := rbac.Authenticated().AllowStages(principal.StagePreAuth)
	setupOrFull := rbac.Authenticated().AllowStages(principal.StageSetup, principal.StageFull)
	want := map[string]rbac.Rule{
		"POST /auth/login":                rbac.Public(),
		"GET /auth/me":                    anyStage,
		"POST /auth/logout":               anyStage,
		"POST /auth/2fa/totp":             preAuth,
		"POST /auth/2fa/recovery":         preAuth,
		"POST /auth/2fa/webauthn/begin":   preAuth,
		"POST /auth/2fa/webauthn/finish":  preAuth,
		"POST /auth/sudo/totp":            rbac.Authenticated(),
		"POST /auth/sudo/webauthn/begin":  rbac.Authenticated(),
		"POST /auth/sudo/webauthn/finish": rbac.Authenticated(),
		"PATCH /me":                       rbac.Authenticated(),
		"POST /me/password":               rbac.Authenticated(),
		"POST /me/totp/setup":             setupOrFull,
		"POST /me/totp/enable":            setupOrFull,
		"DELETE /me/totp":                 rbac.Authenticated().WithSudo(),
		"POST /me/passkeys/begin":         setupOrFull,
		"POST /me/passkeys/finish":        setupOrFull,
		"GET /me/passkeys":                rbac.Authenticated(),
		"DELETE /me/passkeys/{id}":        rbac.Authenticated().WithSudo(),
		"POST /me/recovery-codes":         rbac.Authenticated().WithSudo(),
		"GET /me/sessions":                rbac.Authenticated(),
		"DELETE /me/sessions/{id}":        rbac.Authenticated(),
		"POST /me/sessions/revoke-others": rbac.Authenticated(),
		"GET /admin/sessions":             rbac.Admin(),
		"DELETE /admin/sessions/{id}":     rbac.Admin().WithSudo(),
	}
	if len(want) != 25 {
		t.Fatalf("contract table has %d entries, want 25", len(want))
	}
	got := map[string]string{}
	for _, ri := range e.router.Routes() {
		key := ri.Method + " " + ri.Pattern
		if _, dup := got[key]; dup {
			t.Fatalf("route %s registered twice", key)
		}
		got[key] = ri.Rule.String()
	}
	for key, rule := range want {
		if got[key] != rule.String() {
			t.Errorf("%s: rule %q, want %q", key, got[key], rule.String())
		}
	}
	if len(got) != len(want) {
		t.Errorf("registered %d routes, want %d: %v", len(got), len(want), got)
	}
}

func TestCSRFEnforcedOnAuthRoutes(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	body := map[string]string{"locale": "en"}
	if res := c.doWith(http.MethodPatch, "/api/me", body, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }); res.code != 403 || res.errCode() != "csrf_invalid" {
		t.Fatalf("missing token = %d %s", res.code, res.body)
	}
	if res := c.doWith(http.MethodPatch, "/api/me", body, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }); res.code != 403 || res.errCode() != "csrf_invalid" {
		t.Fatalf("cross-site = %d %s", res.code, res.body)
	}
	if res := c.do(http.MethodPatch, "/api/me", body); res.code != 200 {
		t.Fatalf("valid request = %d %s", res.code, res.body)
	}
	evil := e.newClient("203.0.113.10")
	creds := map[string]string{"username": "alice", "password": testPassword}
	if res := evil.doWith(http.MethodPost, "/api/auth/login", creds, func(r *http.Request) {
		r.Header.Del("Sec-Fetch-Site")
		r.Header.Set("Origin", "https://evil.example")
	}); res.code != 403 || res.errCode() != "csrf_invalid" || evil.cookie != "" {
		t.Fatalf("cross-origin login = %d %s", res.code, res.body)
	}
}
