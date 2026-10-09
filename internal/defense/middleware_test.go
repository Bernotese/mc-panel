package defense

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func errCode(rec *httptest.ResponseRecorder) string {
	var b httpx.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b.Error
}

func (e *env) filter(ip, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ip != "" {
		req = req.WithContext(httpx.WithClientIP(req.Context(), addr(ip)))
	}
	rec := httptest.NewRecorder()
	e.svc.IPFilter(okHandler()).ServeHTTP(rec, req)
	return rec
}

func TestIPFilterPassesByDefault(t *testing.T) {
	e := newEnv(t)
	if rec := e.filter("203.0.113.7", "/api/health"); rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
}

// Phase-1 context item 3: an unparsable client IP fails closed.
func TestIPFilterInvalidIPFailsClosed(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/health", "/"} {
		rec := e.filter("", path)
		if rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeIPBlocked {
			t.Fatalf("%s: code=%d err=%q", path, rec.Code, errCode(rec))
		}
	}
}

func TestIPFilterJailed(t *testing.T) {
	e := newEnv(t)
	e.svc.addJail(netip.MustParsePrefix("2001:db8:1:2::/64"), t0.Add(15*time.Minute))
	for _, path := range []string{"/api/auth/login", "/assets/app.js", "/"} {
		rec := e.filter("2001:db8:1:2::abcd", path)
		if rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeJailed || rec.Header().Get("Retry-After") != "900" {
			t.Fatalf("%s: code=%d err=%q retry=%q", path, rec.Code, errCode(rec), rec.Header().Get("Retry-After"))
		}
	}
	e.clk.Advance(15 * time.Minute)
	if rec := e.filter("2001:db8:1:2::abcd", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("after expiry: code = %d", rec.Code)
	}
}

func TestJailBlocksImmediatelyWithoutReload(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 5)
	rec := e.filter("203.0.113.7", "/api/auth/login")
	if rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeJailed {
		t.Fatalf("code=%d err=%q", rec.Code, errCode(rec))
	}
}

func TestIPFilterDenyAndAllow(t *testing.T) {
	e := newEnv(t)
	e.addRule(t, "198.51.100.0/24", "deny", nil)
	if rec := e.filter("198.51.100.8", "/"); rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeIPBlocked {
		t.Fatalf("deny: code=%d err=%q", rec.Code, errCode(rec))
	}
	e.addRule(t, "198.51.100.7/32", "allow", nil)
	if rec := e.filter("198.51.100.7", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("allow must win over deny: code=%d", rec.Code)
	}
	if rec := e.filter("198.51.100.8", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("other address still denied: code=%d", rec.Code)
	}
}

func TestIPFilterCountry(t *testing.T) {
	e := newEnv(t)
	e.geo["203.0.113.7"] = "US"
	e.geo["203.0.113.8"] = "DE"
	e.geo["10.0.0.1"] = "US"
	e.saveSettings(t, store.SecuritySettings{AllowedCountries: []string{"DE", "AT"}})
	cases := map[string]int{
		"203.0.113.7": http.StatusForbidden,
		"203.0.113.8": http.StatusNoContent,
		"203.0.113.9": http.StatusNoContent, // unknown country passes
		"10.0.0.1":    http.StatusNoContent, // private addresses are never country-filtered
	}
	for ip, want := range cases {
		rec := e.filter(ip, "/")
		if rec.Code != want {
			t.Errorf("%s: code = %d, want %d", ip, rec.Code, want)
		}
		if want == http.StatusForbidden && errCode(rec) != httpx.CodeIPBlocked {
			t.Errorf("%s: err = %q", ip, errCode(rec))
		}
	}
	e.addRule(t, "203.0.113.7/32", "allow", nil)
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("allowlisted IP must bypass the country filter: code=%d", rec.Code)
	}
}

func (e *env) apiReq(path string, sessionID int64, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := httpx.WithClientIP(req.Context(), addr(ip))
	if sessionID != 0 {
		ctx = principal.With(ctx, principal.Principal{UserID: 1, SessionID: sessionID, Role: principal.RoleUser, Stage: principal.StageFull})
	}
	rec := httptest.NewRecorder()
	e.svc.APIRateLimit(okHandler()).ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestAPIRateLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 60; i++ {
		if rec := e.apiReq("/api/servers", 7, "203.0.113.7"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: code = %d", i, rec.Code)
		}
	}
	rec := e.apiReq("/api/servers", 7, "203.0.113.7")
	if rec.Code != http.StatusTooManyRequests || errCode(rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("code=%d err=%q retry=%q", rec.Code, errCode(rec), rec.Header().Get("Retry-After"))
	}
	if rec := e.apiReq("/api/servers", 8, "203.0.113.7"); rec.Code != http.StatusNoContent {
		t.Fatalf("other session must have its own bucket: code=%d", rec.Code)
	}
	for i := 0; i < 100; i++ {
		if rec := e.apiReq("/assets/app.js", 7, "203.0.113.7"); rec.Code != http.StatusNoContent {
			t.Fatalf("non-API paths must not be limited: code=%d", rec.Code)
		}
	}
	e.clk.Advance(200*time.Millisecond + time.Millisecond)
	if rec := e.apiReq("/api/servers", 7, "203.0.113.7"); rec.Code != http.StatusNoContent {
		t.Fatalf("after refill: code=%d", rec.Code)
	}
}

func TestAPIRateLimitAnonymousByIPKey(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 60; i++ {
		if rec := e.apiReq("/api/health", 0, "2001:db8:1:2::1"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: code = %d", i, rec.Code)
		}
	}
	if rec := e.apiReq("/api/health", 0, "2001:db8:1:2::2"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64 must share the anonymous bucket: code=%d", rec.Code)
	}
}

func TestAPIRateLimitInvalidIPUsesSharedBucket(t *testing.T) {
	e := newEnv(t)
	req := func() int {
		r := httptest.NewRequest(http.MethodGet, "/api/health", nil) // no client IP in context
		rec := httptest.NewRecorder()
		e.svc.APIRateLimit(okHandler()).ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < 60; i++ {
		if c := req(); c != http.StatusNoContent {
			t.Fatalf("request %d: code = %d", i, c)
		}
	}
	if c := req(); c != http.StatusTooManyRequests {
		t.Fatalf("invalid IPs must be limited in one dedicated bucket: code=%d", c)
	}
}
