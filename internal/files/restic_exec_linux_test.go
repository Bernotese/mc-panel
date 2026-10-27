//go:build linux

package files

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// prioOf runs the fake binary's "prio" command and returns its nice value and I/O class line
// ("" without ionice).
func prioOf(t *testing.T, bin string, low bool) (nice, class string) {
	t.Helper()
	var out bytes.Buffer
	p, err := execRunner{bin: bin, log: discardLog()}.Start(context.Background(), resticCmd{Args: []string{"prio"}, Stdout: &out, LowPriority: low})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	nice, class, _ = strings.Cut(strings.TrimSpace(out.String()), "\n")
	return nice, class
}

// TestExecRunnerLowPriority (dev instances spec §4.3): a restore-from runs restic at nice 10 in
// the idle I/O class; other runs, also later ones, keep the service's priority.
func TestExecRunnerLowPriority(t *testing.T) {
	bin, _ := fakeBinary(t)
	baseNice, baseClass := prioOf(t, bin, false)
	if baseNice == "10" {
		t.Skip("the test itself runs at nice 10")
	}
	nice, class := prioOf(t, bin, true)
	if nice != "10" {
		t.Fatalf("nice of a low-priority restic = %q, want 10", nice)
	}
	if class != "" && class != "idle" {
		t.Fatalf("I/O class of a low-priority restic = %q, want idle", class)
	}
	// The thread that forked it ended with it: later runs are not lowered.
	for range 20 {
		if n, c := prioOf(t, bin, false); n != baseNice || c != baseClass {
			t.Fatalf("a normal run after a low-priority one: nice %q class %q, want %q %q", n, c, baseNice, baseClass)
		}
	}
}
