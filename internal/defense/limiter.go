package defense

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limit describes a token bucket: one token every Every, at most Burst tokens.
type Limit struct {
	Every time.Duration
	Burst int
}

type limiterEntry struct {
	lim  *rate.Limiter
	last time.Time
}

// keyedLimiter keeps one token bucket per key. All methods take the current
// time explicitly so tests can use clock.Fake.
type keyedLimiter struct {
	limit   Limit
	mu      sync.Mutex
	entries map[string]*limiterEntry
}

func newKeyedLimiter(l Limit) *keyedLimiter {
	return &keyedLimiter{limit: l, entries: make(map[string]*limiterEntry)}
}

// take consumes one token for key. It returns 0 if the request is allowed,
// otherwise the duration until a token becomes available; in that case no
// token is consumed.
func (k *keyedLimiter) take(key string, now time.Time) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.entries[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(rate.Every(k.limit.Every), k.limit.Burst)}
		k.entries[key] = e
	}
	e.last = now
	r := e.lim.ReserveN(now, 1)
	if !r.OK() {
		return k.limit.Every
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}

// cleanup removes buckets that were not used for at least idle.
func (k *keyedLimiter) cleanup(now time.Time, idle time.Duration) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for key, e := range k.entries {
		if now.Sub(e.last) >= idle {
			delete(k.entries, key)
			n++
		}
	}
	return n
}

func (k *keyedLimiter) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.entries)
}

// RetryAfterSeconds converts a wait time into a Retry-After header value
// (whole seconds, rounded up, at least 1).
func RetryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}
