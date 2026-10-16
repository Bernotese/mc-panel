package console_test

import (
	"sync"
	"testing"
	"time"
)

// fakeTimers hands out one unbuffered channel per kind and duration (Every: 15 s heartbeat,
// 5 s recheck; After: the reconnect delay); fire delivers exactly one tick to whichever
// stream waits on it.
type fakeTimers struct {
	mu    sync.Mutex
	every map[time.Duration]chan time.Time
	after map[time.Duration]chan time.Time
}

func newFakeTimers() *fakeTimers {
	return &fakeTimers{every: map[time.Duration]chan time.Time{}, after: map[time.Duration]chan time.Time{}}
}

func (f *fakeTimers) get(m map[time.Duration]chan time.Time, d time.Duration) chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := m[d]
	if !ok {
		c = make(chan time.Time)
		m[d] = c
	}
	return c
}

func (f *fakeTimers) Every(d time.Duration) (<-chan time.Time, func()) {
	return f.get(f.every, d), func() {}
}

func (f *fakeTimers) After(d time.Duration) (<-chan time.Time, func()) {
	return f.get(f.after, d), func() {}
}

// fire blocks until a stream takes a tick of an Every or After timer of d (at most 5 s).
// Where both could be waited on at once (a reconnect delay equal to a ticker interval),
// fireAfter picks the After timer.
func (f *fakeTimers) fire(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f.get(f.every, d) <- t0:
	case f.get(f.after, d) <- t0:
	case <-time.After(5 * time.Second):
		t.Fatalf("no stream waits for the %v timer", d)
	}
}

// fireAfter blocks until a stream takes the tick of an After timer of d (at most 5 s).
func (f *fakeTimers) fireAfter(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f.get(f.after, d) <- t0:
	case <-time.After(5 * time.Second):
		t.Fatalf("no stream waits for the %v reconnect delay", d)
	}
}
