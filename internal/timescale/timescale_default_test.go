//go:build !acceptance

package timescale_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/timescale"
)

const childEnv = "MCPANEL_TIMESCALE_TEST_CHILD"

// A production build ignores the acceptance variable: the factor is a constant. The check runs
// in a child process that has the variable from its start, so a read during package
// initialisation would be caught too.
func TestDefaultBuildIgnoresEnv(t *testing.T) {
	if os.Getenv(childEnv) == "1" {
		if f := timescale.Factor(); f != 1 {
			t.Fatalf("Factor = %d, want 1", f)
		}
		if d := timescale.D(5 * time.Minute); d != 5*time.Minute {
			t.Fatalf("D(5m) = %v, want 5m", d)
		}
		if err := timescale.Err(); err != nil {
			t.Fatalf("Err = %v, want nil", err)
		}
		return
	}
	//nolint:gosec // G204: re-runs this test binary (os.Args[0]) with fixed arguments
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDefaultBuildIgnoresEnv$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "MCPANEL_ACCEPTANCE_TIMESCALE=10", childEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child with MCPANEL_ACCEPTANCE_TIMESCALE=10: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--- PASS: TestDefaultBuildIgnoresEnv") {
		t.Fatalf("child did not run the test:\n%s", out)
	}
}
