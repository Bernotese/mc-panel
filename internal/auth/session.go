package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

const (
	CookieName    = "__Host-sid"
	touchInterval = time.Minute
)

var ErrNoSession = errors.New("auth: no valid session")

// Sessions manages server-side sessions. The cookie carries a random token;
// the database stores only its SHA-256.
type Sessions struct {
	q     *store.Queries
	clock clock.Clock
	cfg   config.Session
}

func NewSessions(q *store.Queries, c clock.Clock, cfg config.Session) *Sessions {
	return &Sessions{q: q, clock: c, cfg: cfg}
}

type NewSession struct {
	UserID    int64
	Stage     principal.Stage
	IP        netip.Addr
	UserAgent string
	Country   string
}

type Rotation struct {
	Stage     principal.Stage
	NewCSRF   bool
	SudoUntil int64
}

func (s *Sessions) expiresAt(stage principal.Stage, createdAt int64) int64 {
	if stage == principal.StagePreAuth {
		return createdAt + s.cfg.PreAuth.Milliseconds()
	}
	return createdAt + s.cfg.Absolute.Milliseconds()
}

func (s *Sessions) Create(ctx context.Context, in NewSession) (string, store.Session, error) {
	tok, err := newToken()
	if err != nil {
		return "", store.Session{}, err
	}
	csrf, err := newToken()
	if err != nil {
		return "", store.Session{}, err
	}
	now := s.clock.Now().UnixMilli()
	ip := ""
	if in.IP.IsValid() {
		ip = in.IP.String()
	}
	sess, err := s.q.AuthCreateSession(ctx, store.AuthCreateSessionParams{
		TokenHash:  hashToken(tok),
		UserID:     in.UserID,
		Stage:      string(in.Stage),
		CsrfToken:  csrf,
		Ip:         ip,
		UserAgent:  in.UserAgent,
		Country:    in.Country,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  s.expiresAt(in.Stage, now),
	})
	if err != nil {
		return "", store.Session{}, err
	}
	return tok, sess, nil
}

func (s *Sessions) Lookup(ctx context.Context, token string) (store.Session, error) {
	if !validTokenFormat(token) {
		return store.Session{}, ErrNoSession
	}
	sess, err := s.q.AuthGetSessionByTokenHash(ctx, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return store.Session{}, ErrNoSession
	}
	if err != nil {
		return store.Session{}, err
	}
	if !s.valid(sess) {
		return store.Session{}, ErrNoSession
	}
	return sess, nil
}

func (s *Sessions) valid(sess store.Session) bool {
	now := s.clock.Now().UnixMilli()
	if sess.RevokedAt.Valid || now >= sess.ExpiresAt {
		return false
	}
	if principal.Stage(sess.Stage) != principal.StagePreAuth && now-sess.LastSeenAt >= s.cfg.Idle.Milliseconds() {
		return false
	}
	return true
}

func (s *Sessions) Touch(ctx context.Context, sess store.Session) error {
	now := s.clock.Now().UnixMilli()
	if now-sess.LastSeenAt < touchInterval.Milliseconds() {
		return nil
	}
	return s.q.AuthTouchSession(ctx, store.AuthTouchSessionParams{LastSeenAt: now, ID: sess.ID})
}

// Rotate issues a new token for the same session (fixation protection) and
// applies the new stage / sudo deadline. Pending WebAuthn state is discarded.
func (s *Sessions) Rotate(ctx context.Context, sess store.Session, rot Rotation) (string, store.Session, error) {
	tok, err := newToken()
	if err != nil {
		return "", store.Session{}, err
	}
	csrf := sess.CsrfToken
	if rot.NewCSRF {
		if csrf, err = newToken(); err != nil {
			return "", store.Session{}, err
		}
	}
	now := s.clock.Now().UnixMilli()
	p := store.AuthRotateSessionParams{
		TokenHash:  hashToken(tok),
		Stage:      string(rot.Stage),
		CsrfToken:  csrf,
		SudoUntil:  rot.SudoUntil,
		ExpiresAt:  s.expiresAt(rot.Stage, sess.CreatedAt),
		LastSeenAt: now,
		ID:         sess.ID,
		// Compare-and-swap: only the holder of the presented token/stage may rotate it.
		OldTokenHash: sess.TokenHash,
		OldStage:     sess.Stage,
	}
	n, err := s.q.AuthRotateSession(ctx, p)
	if err != nil {
		return "", store.Session{}, err
	}
	if n != 1 {
		return "", store.Session{}, ErrNoSession
	}
	sess.TokenHash, sess.Stage, sess.CsrfToken = p.TokenHash, p.Stage, p.CsrfToken
	sess.SudoUntil, sess.ExpiresAt, sess.LastSeenAt = p.SudoUntil, p.ExpiresAt, p.LastSeenAt
	sess.WebauthnState = sql.NullString{}
	return tok, sess, nil
}

func (s *Sessions) Revoke(ctx context.Context, id int64) error {
	_, err := s.q.AuthRevokeSession(ctx, store.AuthRevokeSessionParams{
		RevokedAt: sql.NullInt64{Int64: s.clock.Now().UnixMilli(), Valid: true}, ID: id,
	})
	return err
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}
