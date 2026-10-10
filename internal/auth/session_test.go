package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var sessionStart = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func newTestSessions(t *testing.T) (*Sessions, *store.Queries, *clock.Fake, int64) {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(sessionStart)
	q := store.New(db)
	uid := storetest.CreateUser(t, db, "alice", "user")
	return NewSessions(q, clk, config.DefaultPanel().Session), q, clk, uid
}

func mustCreate(t *testing.T, s *Sessions, uid int64, stage principal.Stage) (string, store.Session) {
	t.Helper()
	tok, sess, err := s.Create(context.Background(), NewSession{
		UserID: uid, Stage: stage, IP: netip.MustParseAddr("203.0.113.7"), UserAgent: "ua", Country: "DE",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok, sess
}

func TestSessionCreateAndLookup(t *testing.T) {
	s, _, _, uid := newTestSessions(t)
	ctx := context.Background()
	tok, sess := mustCreate(t, s, uid, principal.StageFull)
	if !bytes.Equal(sess.TokenHash, hashToken(tok)) || bytes.Contains(sess.TokenHash, []byte(tok)) {
		t.Fatal("DB must store only SHA-256 of the token")
	}
	if sess.CsrfToken == "" || sess.Ip != "203.0.113.7" || sess.Country != "DE" || sess.UserAgent != "ua" ||
		sess.Stage != "full" || sess.ExpiresAt != sessionStart.Add(12*time.Hour).UnixMilli() {
		t.Fatalf("unexpected session %+v", sess)
	}
	got, err := s.Lookup(ctx, tok)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("Lookup = %+v, %v", got, err)
	}
	other, _ := newToken()
	for _, bad := range []string{"", "garbage", other} {
		if _, err := s.Lookup(ctx, bad); !errors.Is(err, ErrNoSession) {
			t.Errorf("Lookup(%q) err = %v, want ErrNoSession", bad, err)
		}
	}
}

func TestPreAuthSessionExpiresAfterFiveMinutes(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	tok, sess := mustCreate(t, s, uid, principal.StagePreAuth)
	if sess.ExpiresAt != sessionStart.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("ExpiresAt = %d", sess.ExpiresAt)
	}
	clk.Advance(5*time.Minute - time.Second)
	if _, err := s.Lookup(context.Background(), tok); err != nil {
		t.Fatalf("still valid: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := s.Lookup(context.Background(), tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired preauth: err = %v", err)
	}
}

func TestIdleTimeoutAndTouchThrottling(t *testing.T) {
	s, q, clk, uid := newTestSessions(t)
	ctx := context.Background()
	tok, sess := mustCreate(t, s, uid, principal.StageFull)

	clk.Advance(30 * time.Second)
	if err := s.Touch(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.AuthGetSessionByID(ctx, sess.ID); got.LastSeenAt != sessionStart.UnixMilli() {
		t.Fatalf("touch within a minute must not write, last_seen=%d", got.LastSeenAt)
	}
	clk.Advance(31 * time.Second)
	if err := s.Touch(ctx, sess); err != nil {
		t.Fatal(err)
	}
	touched := clk.Now()
	if got, _ := q.AuthGetSessionByID(ctx, sess.ID); got.LastSeenAt != touched.UnixMilli() {
		t.Fatalf("touch after a minute must write, last_seen=%d", got.LastSeenAt)
	}
	clk.Advance(30*time.Minute - time.Second)
	if _, err := s.Lookup(ctx, tok); err != nil {
		t.Fatalf("29:59 idle must be valid: %v", err)
	}
	clk.Advance(time.Second)
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("30 min idle must expire, err = %v", err)
	}
}

func TestAbsoluteTimeout(t *testing.T) {
	s, _, clk, uid := newTestSessions(t)
	ctx := context.Background()
	tok, _ := mustCreate(t, s, uid, principal.StageFull)
	for i := 0; i < 24; i++ { // 24 × 29 min = 11h36m, always active
		clk.Advance(29 * time.Minute)
		sess, err := s.Lookup(ctx, tok)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if err := s.Touch(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(29 * time.Minute) // 12h05m
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("absolute timeout not enforced, err = %v", err)
	}
}

func TestRotateAndRevoke(t *testing.T) {
	s, _, _, uid := newTestSessions(t)
	ctx := context.Background()
	tok, sess := mustCreate(t, s, uid, principal.StagePreAuth)

	tok2, full, err := s.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, NewCSRF: true})
	if err != nil {
		t.Fatal(err)
	}
	if tok2 == tok || full.Stage != "full" || full.CsrfToken == sess.CsrfToken ||
		full.ExpiresAt != sessionStart.Add(12*time.Hour).UnixMilli() || full.ID != sess.ID {
		t.Fatalf("rotated = %+v", full)
	}
	if _, err := s.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatal("old token must be invalid after rotation")
	}
	if got, err := s.Lookup(ctx, tok2); err != nil || got.Stage != "full" {
		t.Fatalf("new token: %+v, %v", got, err)
	}

	tok3, sudo, err := s.Rotate(ctx, full, Rotation{Stage: principal.StageFull, SudoUntil: 123})
	if err != nil || sudo.CsrfToken != full.CsrfToken || sudo.SudoUntil != 123 {
		t.Fatalf("sudo rotation = %+v, %v", sudo, err)
	}
	if err := s.Revoke(ctx, sudo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, tok3); !errors.Is(err, ErrNoSession) {
		t.Fatal("revoked session must be invalid")
	}
	if _, _, err := s.Rotate(ctx, sudo, Rotation{Stage: principal.StageFull}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("rotating a revoked session must fail with ErrNoSession, got %v", err)
	}
}

func TestSessionCookies(t *testing.T) {
	rec := httptest.NewRecorder()
	setSessionCookie(rec, "tok")
	h := rec.Header().Get("Set-Cookie")
	for _, part := range []string{"__Host-sid=tok", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(h, part) {
			t.Errorf("Set-Cookie %q missing %q", h, part)
		}
	}
	if strings.Contains(h, "Domain") || strings.Contains(h, "Max-Age") {
		t.Errorf("Set-Cookie %q must not contain Domain or Max-Age", h)
	}
	rec = httptest.NewRecorder()
	clearSessionCookie(rec)
	if h := rec.Header().Get("Set-Cookie"); !strings.Contains(h, "__Host-sid=;") || !strings.Contains(h, "Max-Age=0") {
		t.Errorf("clear cookie = %q", h)
	}
}
