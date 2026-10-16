//go:build !acceptance

package timescale_test

import (
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/timescale"
)

// A production build ignores the acceptance variable: the factor is a constant.
func TestDefaultBuildIgnoresEnv(t *testing.T) {
	t.Setenv("MCPANEL_ACCEPTANCE_TIMESCALE", "10")
	if f := timescale.Factor(); f != 1 {
		t.Fatalf("Factor = %d, want 1", f)
	}
	if d := timescale.D(5 * time.Minute); d != 5*time.Minute {
		t.Fatalf("D(5m) = %v, want 5m", d)
	}
	if err := timescale.Err(); err != nil {
		t.Fatalf("Err = %v, want nil", err)
	}
}
