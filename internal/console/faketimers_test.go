package console_test

import (
	"sync"
	"testing"
	"time"
)

// fakeTimers hands out one unbuffered channel per duration (3 s reconnect, 15 s heartbeat,
// 30 s recheck); fire delivers exactly one tick to whichever stream waits on it.
type fakeTimers struct {
	mu sync.Mutex
	ch map[time.Duration]chan time.Time
}

func newFakeTimers() *fakeTimers { return &fakeTimers{ch: map[time.Duration]chan time.Time{}} }

func (f *fakeTimers) get(d time.Duration) chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.ch[d]
	if !ok {
		c = make(chan time.Time)
		f.ch[d] = c
	}
	return c
}

func (f *fakeTimers) Every(d time.Duration) (<-chan time.Time, func()) { return f.get(d), func() {} }
func (f *fakeTimers) After(d time.Duration) (<-chan time.Time, func()) { return f.get(d), func() {} }

// fire blocks until a stream takes the tick for d (at most 5 s).
func (f *fakeTimers) fire(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f.get(d) <- t0:
	case <-time.After(5 * time.Second):
		t.Fatalf("no stream waits for the %v timer", d)
	}
}
