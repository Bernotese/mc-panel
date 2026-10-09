package defense

import (
	"testing"
	"time"
)

var limT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestKeyedLimiterBurstThenRefill(t *testing.T) {
	k := newKeyedLimiter(Limit{Every: 6 * time.Second, Burst: 5})
	for i := 0; i < 5; i++ {
		if d := k.take("a", limT0); d != 0 {
			t.Fatalf("take %d denied (wait %v)", i, d)
		}
	}
	d := k.take("a", limT0)
	if d <= 5900*time.Millisecond || d > 6*time.Second {
		t.Fatalf("6th take wait = %v, want ~6s", d)
	}
	// a denied take must not consume a token
	if d2 := k.take("a", limT0); d2 <= 5900*time.Millisecond {
		t.Fatalf("second denied take wait = %v, want ~6s", d2)
	}
	if d := k.take("a", limT0.Add(6*time.Second+time.Millisecond)); d != 0 {
		t.Fatalf("after refill denied (wait %v)", d)
	}
	if d := k.take("b", limT0); d != 0 {
		t.Fatalf("other key must have its own bucket, wait %v", d)
	}
}

func TestKeyedLimiterCleanup(t *testing.T) {
	k := newKeyedLimiter(Limit{Every: time.Second, Burst: 1})
	k.take("old", limT0)
	k.take("new", limT0.Add(9*time.Minute))
	if n := k.cleanup(limT0.Add(10*time.Minute), 10*time.Minute); n != 1 {
		t.Fatalf("cleanup removed %d, want 1", n)
	}
	if k.size() != 1 {
		t.Fatalf("size = %d, want 1", k.size())
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	cases := map[time.Duration]int{
		0:                       1,
		200 * time.Millisecond:  1,
		time.Second:             1,
		1001 * time.Millisecond: 2,
		5999 * time.Millisecond: 6,
	}
	for d, want := range cases {
		if got := RetryAfterSeconds(d); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}
