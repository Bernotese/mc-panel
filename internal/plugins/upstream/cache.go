package upstream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// Cache keeps values for a TTL and lets concurrent Gets of the same key share one fetch. At
// most max entries are kept; the oldest is evicted first. A TTL of 0 stores nothing and only
// shares concurrent calls. Failures are never stored.
type Cache[V any] struct {
	clock clock.Clock
	ttl   time.Duration
	max   int

	mu       sync.Mutex
	entries  map[string]cacheEntry[V]
	inflight map[string]*cacheCall[V]
}

type cacheEntry[V any] struct {
	at time.Time
	v  V
}

type cacheCall[V any] struct {
	done chan struct{}
	v    V
	err  error
}

// NewCache returns an empty cache; maxEntries <= 0 means MaxCacheEntries.
func NewCache[V any](clk clock.Clock, ttl time.Duration, maxEntries int) *Cache[V] {
	if maxEntries <= 0 {
		maxEntries = MaxCacheEntries
	}
	return &Cache[V]{clock: clk, ttl: ttl, max: maxEntries, entries: map[string]cacheEntry[V]{}, inflight: map[string]*cacheCall[V]{}}
}

// Get returns the value of key from the cache or from fetch. The fetch runs detached from the
// caller (context.WithoutCancel, values kept) so that other callers waiting for it are not hurt
// when the first one goes away; a caller whose ctx ends gets ErrUnavailable at once.
func (c *Cache[V]) Get(ctx context.Context, key string, fetch func(context.Context) (V, error)) (V, error) {
	c.mu.Lock()
	if v, ok := c.peekLocked(key); ok {
		c.mu.Unlock()
		return v, nil
	}
	cl, ok := c.inflight[key]
	if !ok {
		cl = &cacheCall[V]{done: make(chan struct{})}
		c.inflight[key] = cl
		go c.run(context.WithoutCancel(ctx), key, fetch, cl)
	}
	c.mu.Unlock()
	select {
	case <-cl.done:
		return cl.v, cl.err
	case <-ctx.Done():
		var zero V
		return zero, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
}

func (c *Cache[V]) run(ctx context.Context, key string, fetch func(context.Context) (V, error), cl *cacheCall[V]) {
	v, err := fetch(ctx)
	c.mu.Lock()
	if err == nil && c.ttl > 0 {
		c.putLocked(key, v)
	}
	delete(c.inflight, key)
	c.mu.Unlock()
	cl.v, cl.err = v, err
	close(cl.done)
}

// Peek returns a fresh cached value without fetching.
func (c *Cache[V]) Peek(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peekLocked(key)
}

// Put stores v under key (a TTL of 0 still stores nothing).
func (c *Cache[V]) Put(key string, v V) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, v)
}

func (c *Cache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *Cache[V]) peekLocked(key string) (V, bool) {
	e, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if c.clock.Now().Sub(e.at) >= c.ttl {
		delete(c.entries, key)
		var zero V
		return zero, false
	}
	return e.v, true
}

func (c *Cache[V]) putLocked(key string, v V) {
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		oldest, first := "", true
		var at time.Time
		for k, e := range c.entries {
			if first || e.at.Before(at) {
				oldest, at, first = k, e.at, false
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = cacheEntry[V]{at: c.clock.Now(), v: v}
}
