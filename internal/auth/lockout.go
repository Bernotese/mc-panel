package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Per-user second-factor lockout: the per-session/IP limiter (defense.SecondFactorLimit) can be
// sidestepped with fresh sessions from many addresses; this bounds guesses per account.
const (
	secondFactorMaxFailures = 10
	secondFactorWindow      = 15 * time.Minute
	secondFactorLockout     = 15 * time.Minute
)

type twoFAState struct {
	fails       int
	windowStart time.Time
	lockedUntil time.Time
}

// twoFALocks tracks consecutive failed second-factor attempts (login and sudo) per user.
// In memory: a restart forgets the counters, like every other rate limit.
type twoFALocks struct {
	mu    sync.Mutex
	users map[int64]*twoFAState
}

func newTwoFALocks() *twoFALocks { return &twoFALocks{users: make(map[int64]*twoFAState)} }

// lockedFor returns the remaining lockout of userID (0 if not locked).
func (l *twoFALocks) lockedFor(userID int64, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, ok := l.users[userID]; ok && now.Before(st.lockedUntil) {
		return st.lockedUntil.Sub(now)
	}
	return 0
}

// fail counts a failed attempt and reports whether it started a lockout.
func (l *twoFALocks) fail(userID int64, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.users[userID]
	if !ok || now.Sub(st.windowStart) > secondFactorWindow || (!st.lockedUntil.IsZero() && !now.Before(st.lockedUntil)) {
		st = &twoFAState{windowStart: now}
		l.users[userID] = st
	}
	st.fails++
	if st.fails >= secondFactorMaxFailures && st.lockedUntil.IsZero() {
		st.lockedUntil = now.Add(secondFactorLockout)
		return true
	}
	return false
}

func (l *twoFALocks) reset(userID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.users, userID)
}

// ResetSecondFactorLockout lifts the second-factor lockout of userID. A password change does
// that (spec §7.2); cmd/panel also calls it when an admin sets the user's password.
func (h *Handler) ResetSecondFactorLockout(userID int64) { h.twoFA.reset(userID) }

func isSecondFactorStage(s defense.AttemptStage) bool {
	switch s {
	case defense.StageTOTP, defense.StageWebAuthn, defense.StageRecovery, defense.StageSudo:
		return true
	}
	return false
}

// checkSecondFactor runs the defense guard (per session+IP) and the per-user lockout before a
// second factor is verified; it writes the error response itself.
func (h *Handler) checkSecondFactor(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User) bool {
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return false
	}
	if d := h.twoFA.lockedFor(u.ID, h.clock.Now()); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(defense.RetryAfterSeconds(d)))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many failed attempts, try again later")
		return false
	}
	return true
}

// noteSecondFactor updates the per-user counter from a recorded attempt.
func (h *Handler) noteSecondFactor(ctx context.Context, username string, userID int64, success bool) {
	if success {
		h.twoFA.reset(userID)
		return
	}
	if h.twoFA.fail(userID, h.clock.Now()) {
		slog.WarnContext(ctx, "second factor locked after repeated failures",
			"user_id", userID, "username", username, "lockout", secondFactorLockout.String())
		h.auditLog(ctx, userID, username, audit.ActionLogin, username, audit.ResultDenied,
			map[string]any{"reason": "2fa_lockout"})
	}
}
