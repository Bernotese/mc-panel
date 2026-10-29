package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
	"github.com/Bernotese/mc-panel/internal/users"
)

const adminPW = "mc-panel integration pw 1"

type env struct {
	t         *testing.T
	srv       *httptest.Server
	app       *app
	clk       *clock.Fake
	agent     *fakeAgent
	instances *fakeInstances
	console   *fakeConsole
	files     *fakeFiles
	backups   *fakeBackups
	up        *fakeUpstream
	cfg       config.Panel
	db        *sql.DB
	deps      appDeps
	handler   atomic.Pointer[http.Handler] // the current app's handler (restartApp swaps it)
	stopApp   func()
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith is newEnv; opt may replace dependencies (e.g. a real files service) before the app
// is built.
func newEnvWith(t *testing.T, opt func(*appDeps)) *env {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	cfg := config.DevPanel(t.TempDir())
	cfg.Dev = false
	// WebAuthn requires a domain RP ID (not an IP), so the public origin is https://localhost:<port>
	// while requests are still sent to the test server's 127.0.0.1 address.
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	cfg.PublicURL = "https://localhost:" + port
	cfg.DataDir = t.TempDir()
	cfg.GeoIPPath = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	db := storetest.Open(t)
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	fa := newFakeAgent()
	fi := newFakeInstances()
	fc := newFakeConsole()
	fc.logLines = []agentapi.LogLine{{TS: 1_760_184_000_000_000_001, Stream: agentapi.StreamOut, Line: "Done (3.0s)!"}}
	ff := newFakeFiles()
	fb := newFakeBackups()
	up := newFakeUpstream(t)
	deps := appDeps{Clock: clk, Agent: fa, Instances: fi, Console: fc, Files: ff, Backups: fb, Upstream: up.client(clk), Version: "test",
		HostMemory: func() (uint64, error) { return 16 << 30, nil }}
	if opt != nil {
		opt(&deps)
	}
	e := &env{t: t, srv: srv, clk: clk, agent: fa, instances: fi, console: fc, files: ff, backups: fb, up: up, cfg: cfg, db: db, deps: deps}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*e.handler.Load()).ServeHTTP(w, r) })
	e.startApp()
	t.Cleanup(func() { e.stopApp() })

	if err := users.RunAdminCreate(context.Background(), db, clk, audit.NewSQLRecorder(db, clk),
		[]string{"-username", "admin"}, strings.NewReader(adminPW+"\n"), io.Discard); err != nil {
		t.Fatalf("admin create: %v", err)
	}
	return e
}

// startApp builds the app on the env's database and dependencies and runs it.
func (e *env) startApp() {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := buildApp(ctx, e.cfg, e.db, e.deps)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	e.app = a
	h := a.Handler
	e.handler.Store(&h)
	e.stopApp = func() { cancel(); <-done; a.Close() }
}

// restartApp simulates a panel restart: the running app stops and a new one starts on the same
// database (sessions survive, in-memory state does not).
func (e *env) restartApp() {
	e.t.Helper()
	e.stopApp()
	e.startApp()
}

type client struct {
	e    *env
	hc   *http.Client
	csrf string
	xff  string
}

func (e *env) client(xff string) *client {
	jar, _ := cookiejar.New(nil)
	// httptest.Server.Client() returns one shared *http.Client; every test client needs its own jar.
	hc := &http.Client{Transport: e.srv.Client().Transport, Jar: jar}
	return &client{e: e, hc: hc, xff: xff}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+"/api"+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.e.cfg.Origin())
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.xff != "" {
		req.Header.Set("X-Forwarded-For", c.xff)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if tok, ok := out["csrf_token"].(string); ok && tok != "" {
		c.csrf = tok
	}
	return resp.StatusCode, out
}

// refresh reloads Me (CSRF token may rotate on stage changes / sudo).
func (c *client) refresh() map[string]any {
	c.e.t.Helper()
	code, me := c.do("GET", "/auth/me", nil)
	if code != 200 {
		c.e.t.Fatalf("GET /auth/me = %d %v", code, me)
	}
	return me
}

func (c *client) must(want int, method, path string, body any) map[string]any {
	c.e.t.Helper()
	code, out := c.do(method, path, body)
	if code != want {
		c.e.t.Fatalf("%s %s = %d (%v), want %d", method, path, code, out, want)
	}
	return out
}

func totpCode(e *env, secret string) string {
	e.t.Helper()
	c, err := totp.GenerateCode(secret, e.clk.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// loginAdminWithTOTP performs the forced 2FA setup and returns the client and TOTP secret.
func loginAdminWithTOTP(e *env) (*client, string) {
	c := e.client("")
	me := c.must(200, "POST", "/auth/login", map[string]string{"username": "admin", "password": adminPW})
	if me["stage"] != "setup" {
		e.t.Fatalf("admin without 2FA must land in setup stage, got %v", me["stage"])
	}
	c.must(403, "GET", "/servers", nil) // setup_2fa_required
	setup := c.must(200, "POST", "/me/totp/setup", nil)
	secret, _ := setup["secret"].(string)
	rc := c.must(200, "POST", "/me/totp/enable", map[string]string{"code": totpCode(e, secret), "current_password": adminPW})
	if codes, _ := rc["codes"].([]any); len(codes) != 10 {
		e.t.Fatalf("expected 10 recovery codes, got %v", rc["codes"])
	}
	if me := c.refresh(); me["stage"] != "full" || !isTrue(me["totp_enabled"]) {
		e.t.Fatalf("after enable: %v", me)
	}
	return c, secret
}

// signInNewUser signs in a user whose password an admin set and makes the forced password
// change (SEC-45): until then the session can only reach /auth/me, the logout and /me/password.
func signInNewUser(e *env, username, pw string) *client {
	e.t.Helper()
	c := e.client("")
	if me := c.must(200, "POST", "/auth/login", map[string]string{"username": username, "password": pw}); me["stage"] != "password" {
		e.t.Fatalf("%s must land in stage password, got %v", username, me["stage"])
	}
	if code, out := c.do("GET", "/servers", nil); code != 403 || out["error"] != "password_change_required" {
		e.t.Fatalf("GET /servers in stage password = %d %v", code, out)
	}
	c.must(204, "POST", "/me/password", map[string]string{"current_password": pw, "new_password": pw + " renewed"})
	if me := c.refresh(); me["stage"] == "password" {
		e.t.Fatalf("%s still in stage password after the change", username)
	}
	return c
}

func sudo(e *env, c *client, secret string) {
	e.clk.Advance(30 * time.Second) // next TOTP step (replay protection)
	c.must(200, "POST", "/auth/sudo/totp", map[string]string{"code": totpCode(e, secret)})
	c.refresh()
}

func TestRouteCoverage(t *testing.T) {
	e := newEnv(t)
	if un := rbac.Uncovered(e.app.Mux, e.app.Routes, func(_, p string) bool { return p == "/*" }); len(un) != 0 {
		t.Fatalf("routes registered without rbac rule: %v", un)
	}
	if n := len(e.app.Routes); n != 117 {
		t.Fatalf("registered %d API routes, OpenAPI defines 117 operations (incl. GET /health)", n)
	}
}

func TestAboutNeedsSignInAndReportsVersion(t *testing.T) {
	e := newEnv(t)
	e.client("").must(401, "GET", "/about", nil)
	admin, _ := loginAdminWithTOTP(e)
	if got := admin.must(200, "GET", "/about", nil); got["version"] != "test" {
		t.Fatalf("about = %v, want version test", got)
	}
}

func TestFullFlowPermissionsAndAudit(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)

	// sudo required for admin mutations
	if c, out := admin.do("POST", "/servers", paperServer("survival")); c != 403 || out["error"] != "sudo_required" {
		t.Fatalf("POST /servers without sudo = %d %v", c, out)
	}
	sudo(e, admin, secret)
	if c, out := admin.do("POST", "/servers", paperServer("survival")); c != 409 || out["error"] != "base_domain_missing" {
		t.Fatalf("POST /servers without base domain = %d %v", c, out)
	}
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	a := admin.must(201, "POST", "/servers", paperServer("survival"))
	b := admin.must(201, "POST", "/servers", paperServer("creative"))
	srvA, srvB := int64(a["id"].(float64)), int64(b["id"].(float64))

	u := admin.must(201, "POST", "/users", map[string]string{"username": "maxUser", "password": "another long secret 42", "role": "user"})
	uid := int64(u["id"].(float64))
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", uid), map[string]any{
		"grants": []map[string]any{{"server_id": srvA, "permissions": []string{"server.power"}}},
	})

	// acceptance criterion 4: user sees only server A, 403 on B
	maxUser := signInNewUser(e, "maxUser", "another long secret 42")
	if me := maxUser.refresh(); me["stage"] != "full" {
		t.Fatalf("user without enforced 2FA should be full after the password change, got %v", me["stage"])
	}
	code, list := maxUser.doList("GET", "/servers")
	if code != 200 || len(list) != 1 || int64(list[0]["id"].(float64)) != srvA {
		t.Fatalf("GET /servers as maxUser = %d %v", code, list)
	}
	maxUser.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "stop"})
	if c, out := maxUser.do("POST", fmt.Sprintf("/servers/%d/power", srvB), map[string]string{"action": "stop"}); c != 403 || out["error"] != "forbidden" {
		t.Fatalf("power on B = %d %v", c, out)
	}
	if c, _ := maxUser.do("GET", "/users", nil); c != 403 {
		t.Fatalf("user on admin route = %d", c)
	}

	// acceptance criterion 6: chain valid and actions recorded
	v := admin.must(200, "GET", "/audit/verify", nil)
	if !isTrue(v["ok"]) {
		t.Fatalf("audit verify: %v", v)
	}
	page := admin.must(200, "GET", "/audit?limit=200", nil)
	seen := map[string]bool{}
	for _, it := range page["items"].([]any) {
		seen[it.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{audit.ActionTOTPEnable, audit.ActionSudo, audit.ActionServerSettingsUpdate,
		audit.ActionServerCreate, audit.ActionUserCreate, audit.ActionPermissionsSet, audit.ActionServerPower} {
		if !seen[want] {
			t.Errorf("audit log misses %s (have %v)", want, seen)
		}
	}
}

// TestAdminSetPasswordForcesChange: a password an admin sets for another user must be changed at
// the next sign-in (SEC-45); the admin's own password set the same way is not forced.
func TestAdminSetPasswordForcesChange(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	u := admin.must(201, "POST", "/users", map[string]string{"username": "maxUser", "password": "another long secret 42", "role": "user"})
	uid := int64(u["id"].(float64))
	if !isTrue(u["must_change_password"]) {
		t.Fatalf("created user: %v", u)
	}
	signInNewUser(e, "maxUser", "another long secret 42")
	if got := admin.must(200, "GET", fmt.Sprintf("/users/%d", uid), nil); isTrue(got["must_change_password"]) {
		t.Fatalf("flag after the change: %v", got)
	}

	// The admin resets it: the open session is revoked and the next sign-in is limited again.
	admin.must(200, "PATCH", fmt.Sprintf("/users/%d", uid), map[string]string{"password": "a password from the admin 7"})
	c := e.client("")
	if me := c.must(200, "POST", "/auth/login", map[string]string{"username": "maxUser", "password": "a password from the admin 7"}); me["stage"] != "password" {
		t.Fatalf("stage = %v, want password", me["stage"])
	}
	for _, rt := range []struct{ method, path string }{{"GET", "/servers"}, {"GET", "/about"}, {"GET", "/me/sessions"}} {
		if code, out := c.do(rt.method, rt.path, nil); code != 403 || out["error"] != "password_change_required" {
			t.Fatalf("%s %s = %d %v", rt.method, rt.path, code, out)
		}
	}
	c.must(204, "POST", "/auth/logout", nil)
	c.must(401, "GET", "/auth/me", nil)

	// Own password through the user management: no forced change.
	me := admin.refresh()
	admin.must(200, "PATCH", fmt.Sprintf("/users/%d", int64(me["id"].(float64))), map[string]string{"password": "my very own fresh pw 9"})
	again := e.client("")
	if me := again.must(200, "POST", "/auth/login", map[string]string{"username": "admin", "password": "my very own fresh pw 9"}); me["stage"] != "preauth" {
		t.Fatalf("admin with TOTP = %v, want preauth", me["stage"])
	}
	e.clk.Advance(30 * time.Second)
	if me := again.must(200, "POST", "/auth/2fa/totp", map[string]string{"code": totpCode(e, secret)}); me["stage"] != "full" {
		t.Fatalf("admin after the code = %v, want full", me["stage"])
	}
}

// paperServer is a valid CreateServerRequest.
func paperServer(name string) map[string]any {
	return map[string]any{"name": name, "type": "paper", "version": "1.21.11", "memory_mib": 4096, "eula_accepted": true}
}

func (c *client) doList(method, path string) (int, []map[string]any) {
	c.e.t.Helper()
	req, _ := http.NewRequest(method, c.e.srv.URL+"/api"+path, nil)
	req.Header.Set("Origin", c.e.cfg.Origin())
	resp, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestCSRFRejected(t *testing.T) {
	e := newEnv(t)
	admin, _ := loginAdminWithTOTP(e)
	token := admin.csrf
	admin.csrf = ""
	if c, out := admin.do("POST", "/me/sessions/revoke-others", nil); c != 403 || out["error"] != "csrf_invalid" {
		t.Fatalf("POST without CSRF token = %d %v", c, out)
	}
	admin.csrf = token
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/me/sessions/revoke-others", nil)
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := admin.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 403 || body["error"] != "csrf_invalid" {
		t.Fatalf("foreign Origin = %d %v, want 403 csrf_invalid", resp.StatusCode, body)
	}
	admin.must(204, "POST", "/me/sessions/revoke-others", nil)
}

func TestJailBlocksEverything(t *testing.T) {
	e := newEnv(t)
	attacker := e.client("203.0.113.50")
	for i := 0; i < 5; i++ {
		if c, out := attacker.do("POST", "/auth/login", map[string]string{"username": "admin", "password": "wrong password!!"}); c != 401 {
			t.Fatalf("attempt %d = %d %v", i+1, c, out)
		}
	}
	if c, out := attacker.do("POST", "/auth/login", map[string]string{"username": "admin", "password": adminPW}); c != 403 || out["error"] != "jailed" {
		t.Fatalf("6th attempt (even correct password) = %d %v", c, out)
	}
	if c, _ := attacker.do("GET", "/health", nil); c != 403 {
		t.Fatalf("jailed IP reached /api/health: %d", c)
	}
	other := e.client("198.51.100.9")
	other.must(200, "GET", "/health", nil)

	deadline := time.Now().Add(3 * time.Second)
	for !e.agent.banned("203.0.113.50/32") {
		if time.Now().After(deadline) {
			t.Fatal("jail was not synced to the agent firewall")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The per-username login limiter (spec 7.1: 5/min) was drained by the attacker's attempts
	// against "admin"; accounts are never locked, so the limiter refills with time. Stay well
	// inside the 15 min jail so the lift below is what unbans the attacker.
	e.clk.Advance(time.Minute)
	// lift via API (admin, sudo) → unbanned
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	_, jails := admin.doList("GET", "/security/jails")
	if len(jails) != 1 {
		t.Fatalf("jails = %v", jails)
	}
	admin.must(204, "DELETE", fmt.Sprintf("/security/jails/%d", int64(jails[0]["id"].(float64))), nil)
	attacker.must(200, "GET", "/health", nil)
	deadline = time.Now().Add(3 * time.Second)
	for e.agent.banned("203.0.113.50/32") {
		if time.Now().After(deadline) {
			t.Fatal("lifted jail was not removed from the agent firewall")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestAgentDownStillWorks(t *testing.T) {
	cfg := config.DevPanel(t.TempDir())
	cfg.DataDir = t.TempDir()
	cfg.GeoIPPath = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	db := storetest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ac := agentClientForMissingSocket(t)
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac})
	if err != nil {
		t.Fatalf("buildApp must succeed without agent: %v", err)
	}
	defer a.Close()
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 200 {
		t.Fatalf("/api/health without agent = %d", rec.Code)
	}
	// The application-level jail works without the agent (the firewall sync just fails).
	login := func() (int, string) {
		req := httptest.NewRequest("POST", "/api/auth/login",
			strings.NewReader(`{"username":"nobody","password":"wrong password!!"}`))
		req.RemoteAddr = "203.0.113.60:4242"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", cfg.Origin())
		rec := httptest.NewRecorder()
		a.Handler.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		code, _ := body["error"].(string)
		return rec.Code, code
	}
	for i := 0; i < 5; i++ {
		if c, code := login(); c != 401 || code != "invalid_credentials" {
			t.Fatalf("attempt %d = %d %s", i+1, c, code)
		}
	}
	if c, code := login(); c != 403 || code != "jailed" {
		t.Fatalf("6th attempt without agent = %d %s, want 403 jailed", c, code)
	}
}

func agentClientForMissingSocket(t *testing.T) *agent.Client {
	return agent.NewClient(t.TempDir() + "/missing.sock")
}

func isTrue(v any) bool { b, ok := v.(bool); return ok && b }
