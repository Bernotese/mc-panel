package upstream_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// K4: Delete also invalidates a fetch that is still running: its (stale) result is not stored,
// and a Get after the Delete fetches again instead of joining the stale call.
func TestCacheDeleteDuringFetch(t *testing.T) {
	c := upstream.NewCache[int](clock.NewFake(t0), time.Hour, 10)
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	slow := func(context.Context) (int, error) {
		n := int(calls.Add(1))
		if n == 1 {
			close(started)
			<-release
		}
		return n, nil
	}
	first := make(chan int)
	go func() {
		v, _ := c.Get(bg, "k", slow)
		first <- v
	}()
	<-started
	c.Delete("k")
	ctx, cancel := context.WithTimeout(bg, 5*time.Second) // joining the stale call would block
	defer cancel()
	if v, err := c.Get(ctx, "k", slow); err != nil || v != 2 {
		t.Fatalf("Get after Delete = %d, %v, want a new fetch", v, err)
	}
	close(release)
	if v := <-first; v != 1 {
		t.Fatalf("first Get = %d", v)
	}
	if v, ok := c.Peek("k"); !ok || v != 2 {
		t.Fatalf("Peek = %d, %v: the stale result replaced the fresh one", v, ok)
	}
	c.Delete("k")
	started2, release2 := make(chan struct{}), make(chan struct{})
	slow2 := func(context.Context) (int, error) { close(started2); <-release2; return 99, nil }
	done := make(chan struct{})
	go func() { _, _ = c.Get(bg, "k", slow2); close(done) }()
	<-started2
	c.Delete("k")
	close(release2)
	<-done
	if v, ok := c.Peek("k"); ok {
		t.Fatalf("the result of a fetch deleted while running was stored: %d", v)
	}
}
