package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/principal"
)

func TestSessionMiddleware(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	var got principal.Principal
	var ok bool
	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok = principal.From(r.Context())
	}))
	call := func(cookie string) *httptest.ResponseRecorder {
		got, ok = principal.Principal{}, false
		req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: CookieName, Value: cookie}) //nolint:gosec // G124: request cookie, attributes are response-only
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(""); ok || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("request without cookie must stay anonymous without Set-Cookie")
	}

	_, sess := mustCreate(t, s, uid, principal.StageFull)
	sudoUntil := sessionStart.Add(10 * time.Minute)
	tok, sess, err := s.Rotate(context.Background(), sess, Rotation{Stage: principal.StageFull, SudoUntil: sudoUntil.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	call(tok)
	if !ok || got.UserID != uid || got.Username != "alice" || got.Role != principal.RoleUser ||
		got.Stage != principal.StageFull || got.SessionID != sess.ID || got.CSRFToken != sess.CsrfToken ||
		!got.SudoUntil.Equal(sudoUntil) {
		t.Fatalf("principal = %+v ok=%v", got, ok)
	}

	if rec := call("not-a-valid-token"); ok || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("invalid cookie must be cleared, Set-Cookie=%q", rec.Header().Get("Set-Cookie"))
	}

	clk.Advance(31 * time.Minute)
	if rec := call(tok); ok || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatal("idle-expired session must be anonymous and cleared")
	}
}

// SEC-19: requests marked as background (polling) are served but do not extend the idle
// timeout; a normal request does.
func TestSessionMiddlewareBackgroundDoesNotTouch(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	var ok bool
	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok = principal.From(r.Context())
	}))
	callReq := func(req *http.Request, tok string, background bool) bool {
		ok = false
		req.AddCookie(&http.Cookie{Name: CookieName, Value: tok}) //nolint:gosec // G124: request cookie, attributes are response-only
		if background {
			req.Header.Set(BackgroundHeader, "1")
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		return ok
	}
	call := func(tok string, background bool) bool {
		return callReq(httptest.NewRequest(http.MethodGet, "/api/servers", nil), tok, background)
	}

	polled, _ := mustCreate(t, s, uid, principal.StageFull)
	active, _ := mustCreate(t, s, uid, principal.StageFull)
	for range 5 { // 25 minutes of polling only
		clk.Advance(5 * time.Minute)
		if !call(polled, true) {
			t.Fatal("background request within the idle time must be served with the session")
		}
	}
	if !call(active, false) {
		t.Fatal("normal request within the idle time must be authenticated")
	}
	clk.Advance(6 * time.Minute) // 31 minutes after the last real activity of polled
	if call(polled, true) {
		t.Fatal("a session kept alive only by background requests must expire after the idle time")
	}
	if !call(active, false) {
		t.Fatal("a normal request must extend the idle time")
	}

	// the header counts only on GET and HEAD: a POST is always activity
	posted, _ := mustCreate(t, s, uid, principal.StageFull)
	clk.Advance(25 * time.Minute)
	if !callReq(httptest.NewRequest(http.MethodPost, "/api/servers/1/power", nil), posted, true) {
		t.Fatal("POST within the idle time must be authenticated")
	}
	clk.Advance(25 * time.Minute)
	if !call(posted, true) {
		t.Fatal("a POST marked as background must still extend the idle time")
	}
}

func TestCSRF(t *testing.T) {
	const origin = "http://localhost:5173"
	const token = "tok-123"
	called := false
	h := CSRF(origin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	type hdr = map[string]string
	cases := []struct {
		name, method, path string
		session            bool
		headers            hdr
		want               int
	}{
		{"GET needs nothing", http.MethodGet, "/api/auth/me", true, nil, 200},
		{"no fetch metadata and no origin", http.MethodPost, "/api/me/password", true, hdr{"X-CSRF-Token": token}, 403},
		{"cross-site", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "cross-site", "X-CSRF-Token": token}, 403},
		{"same-site is not same-origin", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-site", "X-CSRF-Token": token}, 403},
		{"foreign origin", http.MethodPost, "/api/me/password", true, hdr{"Origin": "https://evil.example", "X-CSRF-Token": token}, 403},
		{"same-origin but foreign Origin header", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin", "Origin": "https://evil.example", "X-CSRF-Token": token}, 403},
		{"wrong token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin", "X-CSRF-Token": "nope"}, 403},
		{"missing token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin"}, 403},
		{"DELETE needs token", http.MethodDelete, "/api/me/totp", true, hdr{"Sec-Fetch-Site": "same-origin"}, 403},
		{"same-origin with token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "same-origin", "Origin": origin, "X-CSRF-Token": token}, 200},
		{"origin only with token", http.MethodPatch, "/api/me", true, hdr{"Origin": origin, "X-CSRF-Token": token}, 200},
		{"sec-fetch-site none with token", http.MethodPost, "/api/me/password", true, hdr{"Sec-Fetch-Site": "none", "X-CSRF-Token": token}, 200},
		{"login exempt from token", http.MethodPost, "/api/auth/login", true, hdr{"Origin": origin}, 200},
		{"login not exempt from origin", http.MethodPost, "/api/auth/login", false, hdr{"Origin": "https://evil.example"}, 403},
		{"anonymous same-origin POST", http.MethodPost, "/api/auth/logout", false, hdr{"Sec-Fetch-Site": "same-origin"}, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(tc.method, tc.path, nil)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			if tc.session {
				req = req.WithContext(principal.With(req.Context(), principal.Principal{UserID: 1, Stage: principal.StageFull, CSRFToken: token}))
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("code = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == 403 {
				var body struct{ Error string }
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				if body.Error != "csrf_invalid" || called {
					t.Fatalf("error=%q called=%v", body.Error, called)
				}
			} else if !called {
				t.Fatal("next handler not called")
			}
		})
	}
}
