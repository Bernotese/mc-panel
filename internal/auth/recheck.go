package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Recheck reports whether session sessionID still fully authenticates userID: it exists,
// belongs to userID, is neither revoked nor expired nor idle, and its effective stage is full
// (including the downgrade of effectiveStage). It returns the current user row, so callers see
// role changes. Long-lived requests (console streams) call it periodically; it never touches
// the session. Any failure of the session itself is ErrNoSession.
func (s *Sessions) Recheck(ctx context.Context, sessionID, userID int64) (store.User, error) {
	sess, err := s.q.AuthGetSessionByID(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, err
	}
	if sess.UserID != userID || !s.valid(sess) {
		return store.User{}, ErrNoSession
	}
	user, err := s.q.AuthGetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, err
	}
	stage, err := s.effectiveStage(ctx, user, sess)
	if err != nil {
		return store.User{}, err
	}
	if stage != principal.StageFull {
		return store.User{}, ErrNoSession
	}
	return user, nil
}

// RecheckTx is Recheck on q, typically a write transaction of another package: admin writes
// re-check their caller with it before they change anything.
func (s *Sessions) RecheckTx(ctx context.Context, q *store.Queries, sessionID, userID int64) (store.User, error) {
	return s.withQueries(q).Recheck(ctx, sessionID, userID)
}
