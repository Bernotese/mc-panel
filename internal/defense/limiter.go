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
	max     int // maximum number of distinct buckets before the shared overflow bucket is used
	mu      sync.Mutex
	entries map[string]*limiterEntry
}

// maxLimiterEntries bounds memory against key rotation (e.g. many IPv6 /64s).
const maxLimiterEntries = 100_000

// overflowKey is the shared bucket used once the cap is reached. It cannot
// collide with real keys (they never contain a NUL byte).
const overflowKey = "\x00overflow"

func newKeyedLimiter(l Limit) *keyedLimiter {
	return &keyedLimiter{limit: l, max: maxLimiterEntries, entries: make(map[string]*limiterEntry)}
}

// take consumes one token for key. It returns 0 if the request is allowed,
// otherwise the duration until a token becomes available; in that case no
// token is consumed.
func (k *keyedLimiter) take(key string, now time.Time) time.Duration {
	return k.takeOpt(key, now, false)
}

// takeExempt is like take, but a trusted (allowlisted) caller is never put into
// the shared overflow bucket: if the map is full and the key is new, it passes.
func (k *keyedLimiter) takeExempt(key string, now time.Time) time.Duration {
	return k.takeOpt(key, now, true)
}

func (k *keyedLimiter) takeOpt(key string, now time.Time, exempt bool) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	e := k.entry(key, now, exempt)
	if e == nil {
		return 0
	}
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

// entry returns the bucket of key and marks it used; must be called with k.mu held. Once the
// map is full, new keys share the overflow bucket (fail closed for this dimension), except for
// exempt callers, which get nil (not limited).
func (k *keyedLimiter) entry(key string, now time.Time, exempt bool) *limiterEntry {
	e, ok := k.entries[key]
	if !ok && len(k.entries) >= k.max {
		if exempt {
			return nil
		}
		key = overflowKey
		e, ok = k.entries[key]
	}
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(rate.Every(k.limit.Every), k.limit.Burst)}
		k.entries[key] = e
	}
	e.last = now
	return e
}

// wait returns how long until key has a token (0 = now) without consuming one. Used where only
// failures cost a token (consume).
func (k *keyedLimiter) wait(key string, now time.Time) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.entries[key]
	if !ok {
		if len(k.entries) < k.max {
			return 0 // a new key starts with a full bucket
		}
		if e, ok = k.entries[overflowKey]; !ok {
			return 0
		}
	}
	r := e.lim.ReserveN(now, 1)
	if !r.OK() {
		return k.limit.Every
	}
	d := r.DelayFrom(now)
	r.CancelAt(now)
	return d
}

// consume takes one token for key if one is available (an empty bucket stays empty: the
// failure that would have cost it was refused by wait already, or raced with another one).
func (k *keyedLimiter) consume(key string, now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.entry(key, now, false).lim.AllowN(now, 1)
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
