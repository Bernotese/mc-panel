package clock_test

import (
	"testing"
	"time"

	"mcpanel/internal/clock"
)

func TestFakeAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	var c clock.Clock = f
	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
	f.Advance(90 * time.Second)
	if got, want := c.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance Now = %v, want %v", got, want)
	}
	f.Set(start)
	if !c.Now().Equal(start) {
		t.Fatalf("after Set Now = %v, want %v", c.Now(), start)
	}
}

func TestRealIsCurrent(t *testing.T) {
	if d := time.Since(clock.Real{}.Now()); d < 0 || d > time.Second {
		t.Fatalf("Real.Now off by %v", d)
	}
}
