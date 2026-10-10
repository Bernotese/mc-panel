package servers

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Power actions per user: 10 per minute (burst 10). Every accepted action writes an audit
// entry, so this also bounds how fast one account can grow the audit chain.
const (
	powerEvery = 6 * time.Second
	powerBurst = 10
)

// powerLimiter keeps one token bucket per user id (bounded by the number of users).
type powerLimiter struct {
	mu    sync.Mutex
	users map[int64]*rate.Limiter
}

func newPowerLimiter() *powerLimiter { return &powerLimiter{users: make(map[int64]*rate.Limiter)} }

// take consumes a token for userID at now; it returns 0 if allowed, otherwise the wait time
// (no token is consumed then).
func (p *powerLimiter) take(userID int64, now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.users[userID]
	if !ok {
		l = rate.NewLimiter(rate.Every(powerEvery), powerBurst)
		p.users[userID] = l
	}
	r := l.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}
