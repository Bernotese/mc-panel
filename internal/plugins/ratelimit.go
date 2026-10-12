package plugins

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// Upstream lookups per account: 60 per minute, burst 20 (spec §6.6, decision 7) for search,
// versions, resolve and updates?refresh=1. Every lookup may reach Modrinth or Hangar, whose
// rate limit is per IP of the panel.
const (
	lookupEvery = time.Second
	lookupBurst = 20
)

// userLimiter keeps one token bucket per user id (bounded by the number of users).
type userLimiter struct {
	mu    sync.Mutex
	users map[int64]*rate.Limiter
}

func newUserLimiter() *userLimiter { return &userLimiter{users: map[int64]*rate.Limiter{}} }

// take consumes a token of userID at now; 0 = allowed, else the wait (no token consumed).
func (l *userLimiter) take(userID int64, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.users[userID]
	if !ok {
		lim = rate.NewLimiter(rate.Every(lookupEvery), lookupBurst)
		l.users[userID] = lim
	}
	r := lim.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}

// allow takes a lookup token of the caller; without one it writes 429 with Retry-After.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request) bool {
	pr, _ := principal.From(r.Context()) // guaranteed by the rbac rule
	d := h.limit.take(pr.UserID, h.clock.Now())
	if d <= 0 {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(d.Seconds())), 1)))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many plugin lookups, try again later")
	return false
}
