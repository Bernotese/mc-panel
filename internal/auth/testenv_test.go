package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const testPassword = "correct horse battery staple"

var testNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

var testPasswordHash = sync.OnceValue(func() string {
	h, err := password.Hash(testPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// testGuard is defense.FakeGuard plus a helper for the most recent attempt.
type testGuard struct {
	defense.FakeGuard
	// hook, if set, runs at the start of CheckSecondFactor (key = decimal session id).
	// Tests use it to interleave another action deterministically with a request.
	hook func(key string)
}

func (g *testGuard) CheckSecondFactor(ctx context.Context, ip netip.Addr, key string) error {
	if g.hook != nil {
		g.hook(key)
	}
	return g.FakeGuard.CheckSecondFactor(ctx, ip, key)
}

func (g *testGuard) last() defense.Attempt {
	all := g.Recorded()
	if len(all) == 0 {
		return defense.Attempt{}
	}
	return all[len(all)-1]
}

type fixedGeo string

func (g fixedGeo) Country(netip.Addr) string { return string(g) }

type denyAll struct{}

func (denyAll) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

type testEnv struct {
	t      *testing.T
	db     *sql.DB
	q      *store.Queries
	cfg    config.Panel
	clock  *clock.Fake
	guard  *testGuard
	audit  *audit.MemoryRecorder
	notes  *notify.Recording
	h      *Handler
	router *rbac.Router
	srv    http.Handler
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	db := storetest.Open(t)
	cfg := config.DevPanel(t.TempDir())
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New(bytes.Repeat([]byte{1}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(testNow)
	e := &testEnv{
		t: t, db: db, q: store.New(db), cfg: cfg, clock: clk,
		guard: &testGuard{}, audit: &audit.MemoryRecorder{}, notes: &notify.Recording{},
	}
	h, err := New(Deps{
		DB: db, Config: cfg, Clock: clk, Box: box, Guard: e.guard,
		Geo: fixedGeo("DE"), Audit: e.audit, Notifier: e.notes,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	mux := chi.NewRouter()
	mux.Use(httpx.RealIP(nil), h.Sessions().Middleware, CSRF(cfg.Origin()))
	mux.Route("/api", func(sub chi.Router) {
		e.router = rbac.NewRouter(sub, denyAll{}, clk)
		h.Routes(e.router)
	})
	e.srv = mux
	return e
}

func (e *testEnv) createUser(username, role string) int64 {
	e.t.Helper()
	id := storetest.CreateUser(e.t, e.db, username, role)
	if _, err := e.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, testPasswordHash(), id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// enableTOTP activates TOTP directly in the database and returns the secret.
func (e *testEnv) enableTOTP(userID int64) string {
	e.t.Helper()
	secret, _, err := newTOTPKey("test")
	if err != nil {
		e.t.Fatal(err)
	}
	enc, err := sealTOTPSecret(e.h.box, userID, secret)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE users SET totp_secret_enc = ?, totp_enabled = 1 WHERE id = ?`, enc, userID); err != nil {
		e.t.Fatal(err)
	}
	return secret
}

// code returns the TOTP code for the current fake-clock step.
func (e *testEnv) code(secret string) string {
	e.t.Helper()
	c, err := totpCodeAt(secret, totpStep(e.clock.Now()))
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *testEnv) grantSudo(c *client) {
	e.t.Helper()
	if _, err := e.db.Exec(`UPDATE sessions SET sudo_until = ? WHERE token_hash = ?`,
		e.clock.Now().Add(10*time.Minute).UnixMilli(), hashToken(c.cookie)); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) enforce2FAForUsers() {
	e.t.Helper()
	s := store.DefaultSecuritySettings()
	s.Enforce2FAForUsers = true
	if err := store.SaveSecuritySettings(context.Background(), e.q, s, e.clock.Now()); err != nil {
		e.t.Fatal(err)
	}
}

// client simulates one browser: it keeps the session cookie and the CSRF token.
type client struct {
	env    *testEnv
	ip     string
	cookie string
	csrf   string
}

func (e *testEnv) newClient(ip string) *client { return &client{env: e, ip: ip} }

type response struct {
	code   int
	body   []byte
	header http.Header
}

func (r response) errCode() string {
	var b struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(r.body, &b)
	return b.Error
}

func (r response) me(t *testing.T) meResponse {
	t.Helper()
	var m meResponse
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("decode Me: %v (%s)", err, r.body)
	}
	return m
}

func (c *client) do(method, path string, body any) response { return c.doWith(method, path, body, nil) }

func (c *client) doWith(method, path string, body any, mutate func(*http.Request)) response {
	c.env.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.env.t.Error(err) // not Fatal: do() is also used from goroutines
			return response{code: -1}
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = c.ip + ":40000"
	req.Header.Set("User-Agent", "test-agent")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Origin", c.env.cfg.Origin())
		if c.csrf != "" {
			req.Header.Set("X-CSRF-Token", c.csrf)
		}
	}
	if c.cookie != "" {
		req.AddCookie(&http.Cookie{Name: CookieName, Value: c.cookie}) //nolint:gosec // G124: request cookie, attributes are response-only
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	c.env.srv.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == CookieName {
			if ck.MaxAge < 0 || ck.Value == "" {
				c.cookie = ""
			} else {
				c.cookie = ck.Value
			}
		}
	}
	var m struct {
		CSRFToken string `json:"csrf_token"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &m) == nil && m.CSRFToken != "" {
		c.csrf = m.CSRFToken
	}
	return response{code: rec.Code, body: rec.Body.Bytes(), header: rec.Header()}
}

func (c *client) login(username, pw string) response {
	return c.do(http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": pw})
}

func (c *client) sessionID() int64 {
	c.env.t.Helper()
	s, err := c.env.q.AuthGetSessionByTokenHash(context.Background(), hashToken(c.cookie))
	if err != nil {
		c.env.t.Fatal(err)
	}
	return s.ID
}
