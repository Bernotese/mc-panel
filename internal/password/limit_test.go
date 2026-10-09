package password

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// withLimiter swaps the process-wide limiter for the duration of the test.
func withLimiter(t *testing.T, slots int, wait time.Duration) *limiter {
	t.Helper()
	old := lim
	l := newLimiter(slots, wait)
	lim = l
	t.Cleanup(func() { lim = old })
	return l
}

func TestDefaultSlots(t *testing.T) {
	want := max(2, runtime.NumCPU()/2)
	if got := cap(lim.slots); got != want {
		t.Fatalf("default slots = %d, want %d", got, want)
	}
	if lim.wait != 2*time.Second {
		t.Fatalf("default wait budget = %v, want 2s", lim.wait)
	}
}

func TestConcurrencyNeverExceedsSlots(t *testing.T) {
	l := withLimiter(t, 2, 10*time.Second)
	enc := HashWithParams("correct horse battery", 4*1024, 2, 1)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _, err := VerifyContext(context.Background(), "correct horse battery", enc); !ok || err != nil {
				t.Errorf("VerifyContext = %v, %v", ok, err)
			}
			if _, err := HashContext(context.Background(), "correct horse battery"); err != nil {
				t.Errorf("HashContext: %v", err)
			}
			if err := DummyVerifyContext(context.Background(), "x"); err != nil {
				t.Errorf("DummyVerifyContext: %v", err)
			}
		}()
	}
	wg.Wait()
	if p := l.peak.Load(); p < 1 || p > 2 {
		t.Fatalf("peak concurrency = %d, want 1..2", p)
	}
	if a := l.active.Load(); a != 0 {
		t.Fatalf("active after completion = %d, want 0", a)
	}
}

func TestBudgetExhaustionReturnsErrBusy(t *testing.T) {
	Warmup() // the dummy hash itself is computed outside the limiter
	l := withLimiter(t, 1, 30*time.Millisecond)
	l.slots <- struct{}{} // occupy the only slot
	defer func() { <-l.slots }()

	enc := HashWithParams("pw", 8, 1, 1)
	start := time.Now()
	if _, _, err := VerifyContext(context.Background(), "pw", enc); !errors.Is(err, ErrBusy) {
		t.Fatalf("VerifyContext err = %v, want ErrBusy", err)
	}
	if d := time.Since(start); d < 25*time.Millisecond || d > 2*time.Second {
		t.Fatalf("VerifyContext waited %v, want ~30ms", d)
	}
	if _, err := HashContext(context.Background(), "pw"); !errors.Is(err, ErrBusy) {
		t.Fatalf("HashContext err = %v, want ErrBusy", err)
	}
	if err := DummyVerifyContext(context.Background(), "pw"); !errors.Is(err, ErrBusy) {
		t.Fatalf("DummyVerifyContext err = %v, want ErrBusy", err)
	}
	// The legacy wrappers share the limiter.
	if _, err := Hash("pw"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Hash err = %v, want ErrBusy", err)
	}
}

func TestCanceledContextReturnsErrBusy(t *testing.T) {
	l := withLimiter(t, 1, 10*time.Second)
	l.slots <- struct{}{}
	defer func() { <-l.slots }()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := HashContext(ctx, "pw"); !errors.Is(err, ErrBusy) {
		t.Fatalf("HashContext err = %v, want ErrBusy", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("cancellation not honoured, waited %v", d)
	}
}

func TestMalformedHashNeedsNoSlot(t *testing.T) {
	l := withLimiter(t, 1, 10*time.Millisecond)
	l.slots <- struct{}{}
	defer func() { <-l.slots }()
	if _, _, err := VerifyContext(context.Background(), "x", "!"); !errors.Is(err, ErrMalformedHash) {
		t.Fatalf("VerifyContext err = %v, want ErrMalformedHash", err)
	}
}
