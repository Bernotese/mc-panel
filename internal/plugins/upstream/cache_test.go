package upstream_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestCacheTTL(t *testing.T) {
	clk := clock.NewFake(t0)
	c := upstream.NewCache[int](clk, time.Minute, 10)
	var calls atomic.Int64
	fetch := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	for range 2 {
		if v, err := c.Get(bg, "a", fetch); err != nil || v != 1 {
			t.Fatalf("Get = %d, %v", v, err)
		}
	}
	clk.Advance(time.Minute)
	if v, _ := c.Get(bg, "a", fetch); v != 2 {
		t.Fatalf("after the TTL Get = %d, want a new fetch", v)
	}
	if v, ok := c.Peek("a"); !ok || v != 2 {
		t.Fatalf("Peek = %d, %v", v, ok)
	}
	c.Delete("a")
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Delete kept the entry")
	}
}

func TestCacheSharedCall(t *testing.T) {
	c := upstream.NewCache[string](clock.NewFake(t0), time.Minute, 10)
	release := make(chan struct{})
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		<-release
		return "v", nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if v, err := c.Get(bg, "k", fetch); err != nil || v != "v" {
				t.Errorf("Get = %q, %v", v, err)
			}
		})
	}
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetch ran %d times, want 1", n)
	}
}

// A caller that gives up gets ErrUnavailable at once; the shared fetch still fills the cache.
func TestCacheCallerCancel(t *testing.T) {
	c := upstream.NewCache[string](clock.NewFake(t0), time.Minute, 10)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := c.Get(ctx, "k", func(context.Context) (string, error) { <-release; return "v", nil }); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("cancelled Get: %v", err)
	}
	close(release)
	if v, err := c.Get(bg, "k", func(context.Context) (string, error) { return "v", nil }); err != nil || v != "v" {
		t.Fatalf("Get = %q, %v", v, err)
	}
}

func TestCacheErrorsNotCached(t *testing.T) {
	c := upstream.NewCache[int](clock.NewFake(t0), time.Minute, 10)
	boom := errors.New("boom")
	if _, err := c.Get(bg, "a", func(context.Context) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if v, err := c.Get(bg, "a", func(context.Context) (int, error) { return 7, nil }); err != nil || v != 7 {
		t.Fatalf("after an error Get = %d, %v", v, err)
	}
}

// TTL 0 never stores: every Get after the previous one ended fetches again.
func TestCacheZeroTTL(t *testing.T) {
	c := upstream.NewCache[int](clock.NewFake(t0), 0, 10)
	var calls atomic.Int64
	fetch := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	_, _ = c.Get(bg, "a", fetch)
	_, _ = c.Get(bg, "a", fetch)
	if calls.Load() != 2 || c.Len() != 0 {
		t.Fatalf("calls = %d, len = %d", calls.Load(), c.Len())
	}
}

func TestCacheEvictsOldest(t *testing.T) {
	clk := clock.NewFake(t0)
	c := upstream.NewCache[int](clk, time.Hour, 3)
	for i, k := range []string{"a", "b", "c", "d"} {
		c.Put(k, i)
		clk.Advance(time.Second)
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d", c.Len())
	}
	if _, ok := c.Peek("a"); ok {
		t.Fatal("the oldest entry was kept")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := c.Peek(k); !ok {
			t.Errorf("%s evicted", k)
		}
	}
	if d := upstream.NewCache[int](clk, time.Hour, 0); d == nil {
		t.Fatal("max 0 must fall back to MaxCacheEntries")
	}
}
