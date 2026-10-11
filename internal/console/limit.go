package console

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Console commands and player actions per user: 30 per minute, burst 10 (spec §6.4). Every
// accepted request writes an audit entry, so this also bounds how fast one account grows the
// audit chain.
const (
	commandEvery = 2 * time.Second
	commandBurst = 10
)

// userLimiter keeps one token bucket per user id (bounded by the number of users).
type userLimiter struct {
	mu    sync.Mutex
	users map[int64]*rate.Limiter
}

func newUserLimiter() *userLimiter { return &userLimiter{users: make(map[int64]*rate.Limiter)} }

// take consumes a token for userID at now; it returns 0 if allowed, otherwise the wait time
// (no token is consumed then).
func (l *userLimiter) take(userID int64, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.users[userID]
	if !ok {
		lim = rate.NewLimiter(rate.Every(commandEvery), commandBurst)
		l.users[userID] = lim
	}
	r := lim.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}
