package httpx_test

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	httpx.WriteError(rec, req, http.StatusForbidden, httpx.CodeForbidden, "nope")
	if rec.Code != 403 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body httpx.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != "forbidden" || body.Message != "nope" {
		t.Fatalf("body=%s err=%v", rec.Body, err)
	}
}

func TestInternalHidesDetails(t *testing.T) {
	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.Internal(w, r, errors.New("db password is hunter2"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body httpx.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.RequestID == "" || body.RequestID != rec.Header().Get("X-Request-ID") {
		t.Fatalf("request id missing/mismatch: %+v / %q", body, rec.Header().Get("X-Request-ID"))
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	cases := []struct {
		name, body string
		ok         bool
		status     int
	}{
		{"valid", `{"name":"a"}`, true, 0},
		{"unknown field", `{"name":"a","x":1}`, false, 400},
		{"trailing data", `{"name":"a"}{"name":"b"}`, false, 400},
		{"malformed", `{`, false, 400},
		{"too large", `{"name":"` + strings.Repeat("a", 2000) + `"}`, false, 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got in
			var ok bool
			h := httpx.BodyLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ok = httpx.DecodeJSON(w, r, &got)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (body %s)", ok, tc.ok, rec.Body)
			}
			if !tc.ok && rec.Code != tc.status {
				t.Fatalf("status=%d want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestIPKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":          "203.0.113.7/32",
		"::ffff:203.0.113.7":   "203.0.113.7/32",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1": "2001:db8:1:2::/64",
		"fe80::1%en0":          "fe80::/64",
	}
	for in, want := range cases {
		if got := httpx.IPKey(netip.MustParseAddr(in)).String(); got != want {
			t.Errorf("IPKey(%s) = %s, want %s", in, got, want)
		}
	}
	if httpx.IPKey(netip.Addr{}).IsValid() {
		t.Error("IPKey(invalid) must be invalid")
	}
}

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name, remote, xff, want string
	}{
		{"direct, no proxy", "198.51.100.1:5000", "", "198.51.100.1"},
		{"untrusted peer ignores XFF", "198.51.100.1:5000", "1.2.3.4", "198.51.100.1"},
		{"trusted proxy uses XFF", "127.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"rightmost untrusted wins (spoofed left entry)", "127.0.0.1:5000", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"mapped v4 unmapped", "[::ffff:198.51.100.1]:5000", "", "198.51.100.1"},
		{"garbage XFF fails closed", "127.0.0.1:5000", "not-an-ip", "invalid IP"},
		{"garbage right of a trusted hop fails closed", "127.0.0.1:5000", "203.0.113.9, junk, 127.0.0.1", "invalid IP"},
		{"empty XFF entry fails closed", "127.0.0.1:5000", "203.0.113.9, ", "invalid IP"},
		{"garbage left of the rightmost untrusted entry is irrelevant", "127.0.0.1:5000", "junk, 203.0.113.9", "203.0.113.9"},
		{"v6 trusted proxy", "[::1]:5000", "2001:db8::5", "2001:db8::5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got netip.Addr
			h := httpx.RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = httpx.ClientIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.String() != tc.want {
				t.Fatalf("ClientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestRealIPXFFVariants(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"zoned XFF entry from trusted peer loses zone", "127.0.0.1:5000", []string{"fe80::1%en0"}, "fe80::1"},
		{"zoned peer loses zone", "[fe80::1%en0]:5000", nil, "fe80::1"},
		{"multiple XFF lines, rightmost untrusted wins", "127.0.0.1:5000", []string{"6.6.6.6", "203.0.113.9"}, "203.0.113.9"},
		{"multiple XFF lines, trusted hop on last line skipped", "127.0.0.1:5000", []string{"203.0.113.9", "127.0.0.1"}, "203.0.113.9"},
		{"all XFF entries trusted falls back to peer", "127.0.0.1:5000", []string{"127.0.0.1", "::1"}, "127.0.0.1"},
		{"all XFF entries trusted falls back to v6 peer", "[::1]:5000", []string{"::1"}, "::1"},
		{"IPv4-mapped XFF entry unmapped", "127.0.0.1:5000", []string{"::ffff:198.51.100.1"}, "198.51.100.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got netip.Addr
			h := httpx.RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = httpx.ClientIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for _, line := range tc.xff {
				req.Header.Add("X-Forwarded-For", line)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.Zone() != "" {
				t.Fatalf("ClientIP %s carries zone %q", got, got.Zone())
			}
			if got.String() != tc.want {
				t.Fatalf("ClientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestHSTSOnlyOutsideDev(t *testing.T) {
	noop := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	rec := httptest.NewRecorder()
	httpx.SecurityHeaders(false)(noop).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("prod HSTS = %q, want max-age=31536000", got)
	}
	rec = httptest.NewRecorder()
	httpx.SecurityHeaders(true)(noop).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("dev must not send HSTS (http://localhost), got %q", got)
	}
}

func TestDecodeJSONTrailingTooLarge(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	var got in
	var ok bool
	h := httpx.BodyLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok = httpx.DecodeJSON(w, r, &got)
	}))
	rec := httptest.NewRecorder()
	body := `{"name":"a"}` + strings.Repeat(" ", 2000)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	if ok {
		t.Fatal("ok=true, want false")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413 (body %s)", rec.Code, rec.Body)
	}
	var eb httpx.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil || eb.Error != httpx.CodePayloadTooLarge {
		t.Fatalf("error code=%q err=%v body=%s", eb.Error, err, rec.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := httpx.SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	for k, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"Referrer-Policy":            "same-origin",
		"Cache-Control":              "no-store",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, part := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP %q missing %q", csp, part)
		}
	}
	if strings.Contains(csp, "'unsafe-inline'") || strings.Contains(csp, "'unsafe-eval'") || strings.Contains(csp, "data:") {
		t.Errorf("CSP must not allow inline, eval or data: URLs: %q", csp)
	}
	if ro := rec.Header().Get("Content-Security-Policy-Report-Only"); ro != "require-trusted-types-for 'script'; trusted-types 'none'" {
		t.Errorf("report-only CSP = %q", ro)
	}
	idx := strings.Index(csp, "script-src")
	if idx < 0 {
		t.Fatalf("CSP %q has no script-src directive", csp)
	}
	scriptSrc := csp[idx+len("script-src"):]
	if end := strings.Index(scriptSrc, ";"); end >= 0 {
		scriptSrc = scriptSrc[:end]
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") || strings.Contains(scriptSrc, "'unsafe-eval'") {
		t.Errorf("script-src must not allow inline/eval scripts: %q (csp %q)", scriptSrc, csp)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Header().Get("Cache-Control") == "no-store" {
		t.Error("static assets must not get no-store")
	}
}

func TestCSPStyleNonce(t *testing.T) {
	if got := httpx.CSP(""); !strings.Contains(got, "style-src 'self';") {
		t.Errorf("CSP without nonce = %q", got)
	}
	got := httpx.CSP("bm9uY2U=")
	if !strings.Contains(got, "style-src 'self' 'nonce-bm9uY2U=';") || !strings.Contains(got, "script-src 'self';") {
		t.Errorf("CSP with nonce = %q (the nonce applies to styles only)", got)
	}
}

// Requests that arrived over the panel's unix socket come from the reverse proxy: their
// RemoteAddr is "@" or "", so trust is carried by a context flag set in ConnContext.
func TestRealIPUnixSocketPeer(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"single XFF entry", "@", []string{"203.0.113.9"}, "203.0.113.9"},
		{"empty remote addr", "", []string{"203.0.113.9"}, "203.0.113.9"},
		{"rightmost untrusted wins", "@", []string{"6.6.6.6, 203.0.113.9"}, "203.0.113.9"},
		{"trusted hops skipped", "@", []string{"203.0.113.9, 127.0.0.1"}, "203.0.113.9"},
		{"all entries trusted: leftmost (the original client)", "@", []string{"127.0.0.1"}, "127.0.0.1"},
		{"no XFF fails closed", "@", nil, "invalid IP"},
		{"garbage XFF fails closed", "@", []string{"junk"}, "invalid IP"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got netip.Addr
			h := httpx.RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = httpx.ClientIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for _, line := range tc.xff {
				req.Header.Add("X-Forwarded-For", line)
			}
			req = req.WithContext(httpx.UnixConnContext(req.Context(), &net.UnixConn{}))
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.String() != tc.want {
				t.Fatalf("ClientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestUnixConnContextIgnoresTCP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	var got netip.Addr
	h := httpx.RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = httpx.ClientIP(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.1:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	req = req.WithContext(httpx.UnixConnContext(req.Context(), &net.TCPConn{}))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.String() != "198.51.100.1" {
		t.Fatalf("ClientIP = %s, want the untrusted TCP peer", got)
	}
}

func TestBodyLimitExcept(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := httpx.BodyLimitExcept(16, func(r *http.Request) bool { return r.URL.Path == "/upload" })(read)
	big := strings.Repeat("x", 64)
	for path, want := range map[string]int{"/upload": http.StatusNoContent, "/other": http.StatusRequestEntityTooLarge} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(big)))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
	}
}
