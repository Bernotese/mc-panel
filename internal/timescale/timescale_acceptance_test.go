//go:build acceptance

package timescale

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 1, true}, {"1", 1, true}, {"10", 10, true}, {"60", 60, true},
		{"0", 1, false}, {"61", 1, false}, {"-2", 1, false}, {"1.5", 1, false}, {"x", 1, false},
	} {
		got, err := parse(c.in)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("parse(%q) = %d, %v; want %d, ok %v", c.in, got, err, c.want, c.ok)
		}
	}
}

func TestDScales(t *testing.T) {
	old := factor
	t.Cleanup(func() { factor = old })
	factor = 10
	if d := D(5 * time.Minute); d != 30*time.Second {
		t.Fatalf("D(5m) at factor 10 = %v, want 30s", d)
	}
}
