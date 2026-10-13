package monitoring

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// cRconOut reads a recorded RCON answer; tick-query-running, tick-query-unknown and
// paper-mspt are verbatim rcon-cli output of Paper 26.2 on the test VM (ANSI codes included).
func cRconOut(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "rcon", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cNear(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParseTickQuery(t *testing.T) {
	cases := []struct {
		file      string
		tps, mspt float64
	}{
		{"tick-query-running.txt", 20, 0.1},
		{"tick-query-running-lines.txt", 20, 3.4},
		{"tick-query-lagging.txt", 12.5, 80},
		{"tick-query-frozen.txt", 0, 0.2},
		{"tick-query-sprinting.txt", 20, 4.0},
	}
	for _, c := range cases {
		got, ok := parseTickQuery(cRconOut(t, c.file))
		if !ok || !cNear(got.TPS, c.tps) || !cNear(got.MSPT, c.mspt) {
			t.Errorf("%s: %+v ok=%v, want tps %v mspt %v", c.file, got, ok, c.tps, c.mspt)
		}
	}
	for _, f := range []string{"tick-query-unknown.txt", "garbage.txt", "paper-mspt.txt"} {
		if _, ok := parseTickQuery(cRconOut(t, f)); ok {
			t.Errorf("%s must not parse as tick query", f)
		}
	}
}

func TestParseMSPT(t *testing.T) {
	cases := []struct {
		file      string
		tps, mspt float64
	}{
		{"paper-mspt.txt", 20, 0.2},
		{"paper-mspt-section.txt", 20, 1.2},
		{"paper-mspt-comma.txt", 16, 62.5},
	}
	for _, c := range cases {
		got, ok := parseMSPT(cRconOut(t, c.file))
		if !ok || !cNear(got.TPS, c.tps) || !cNear(got.MSPT, c.mspt) {
			t.Errorf("%s: %+v ok=%v, want tps %v mspt %v", c.file, got, ok, c.tps, c.mspt)
		}
	}
	for _, f := range []string{"tick-query-unknown.txt", "garbage.txt", "tick-query-running.txt"} {
		if _, ok := parseMSPT(cRconOut(t, f)); ok {
			t.Errorf("%s must not parse as mspt", f)
		}
	}
}

func TestParseRejectsOutOfRange(t *testing.T) {
	if _, ok := parseTickQuery("Target tick rate: 20.0 per second.\nAverage time per tick: 70000.0ms (Target: 50.0ms)"); ok {
		t.Fatal("mspt above 60000 must be rejected")
	}
	if _, ok := parseTickQuery("Target tick rate: 5000.0 per second.\nAverage time per tick: 0.1ms (Target: 0.2ms)"); ok {
		t.Fatal("tps above 1000 must be rejected")
	}
	if _, ok := parseTickQuery("Target tick rate: 0.0 per second.\nAverage time per tick: 1.0ms (Target: 0.0ms)"); ok {
		t.Fatal("a target rate of 0 must be rejected")
	}
	if _, ok := parseMSPT("Server tick times (avg/min/max) from last 5s, 10s, 1m:\n◴ 99999.0/1.0/1.0, 1/1/1, 1/1/1"); ok {
		t.Fatal("mspt above 60000 must be rejected")
	}
}

func TestDetectorTickQueryAfterThreeValid(t *testing.T) {
	var d tpsDetector
	for i := range 3 {
		cmd, ok := d.command("vanilla", 1)
		if !ok || cmd != cmdTickQuery {
			t.Fatalf("round %d: cmd %q ok %v", i, cmd, ok)
		}
		if _, ok := d.observe(cmd, cRconOut(t, "tick-query-running.txt")); !ok {
			t.Fatal("valid answer not accepted")
		}
		if i < 2 && d.source() != TPSSourceUnknown {
			t.Fatalf("source fixed too early: %s", d.source())
		}
	}
	if d.source() != TPSSourceTickQuery {
		t.Fatalf("source = %s", d.source())
	}
	// once fixed, a bad answer is only a missing value
	if _, ok := d.observe(cmdTickQuery, "garbage"); ok || d.source() != TPSSourceTickQuery {
		t.Fatalf("fixed source changed: %s", d.source())
	}
}

func TestDetectorPaperFallback(t *testing.T) {
	var d tpsDetector
	for range 3 {
		cmd, _ := d.command("paper", 1)
		d.observe(cmd, cRconOut(t, "tick-query-unknown.txt"))
	}
	cmd, ok := d.command("paper", 1)
	if !ok || cmd != cmdMSPT {
		t.Fatalf("after three bad tick queries: cmd %q ok %v", cmd, ok)
	}
	for range 3 {
		cmd, _ := d.command("paper", 1)
		d.observe(cmd, cRconOut(t, "paper-mspt.txt"))
	}
	if d.source() != TPSSourcePaper {
		t.Fatalf("source = %s", d.source())
	}
	if cmd, ok := d.command("paper", 1); !ok || cmd != cmdMSPT {
		t.Fatalf("fixed paper source: cmd %q ok %v", cmd, ok)
	}
}

func TestDetectorPurpurWithoutAnySource(t *testing.T) {
	var d tpsDetector
	for range 6 {
		cmd, ok := d.command("purpur", 1)
		if !ok {
			t.Fatal("purpur tries tick query and mspt three times each")
		}
		d.observe(cmd, "Unknown command")
	}
	if _, ok := d.command("purpur", 1); ok || d.source() != TPSSourceNone {
		t.Fatalf("source = %s, want none", d.source())
	}
}

func TestDetectorNoneForVanillaWithoutTick(t *testing.T) {
	var d tpsDetector
	for range 3 {
		cmd, _ := d.command("vanilla", 1)
		d.observe(cmd, cRconOut(t, "tick-query-unknown.txt"))
	}
	if _, ok := d.command("vanilla", 1); ok || d.source() != TPSSourceNone {
		t.Fatalf("source = %s, want none and no command", d.source())
	}
}

func TestDetectorResetsOnNewRevision(t *testing.T) {
	var d tpsDetector
	for range 3 {
		cmd, _ := d.command("vanilla", 1)
		d.observe(cmd, cRconOut(t, "tick-query-unknown.txt"))
	}
	if cmd, ok := d.command("vanilla", 2); !ok || cmd != cmdTickQuery || d.source() != TPSSourceUnknown {
		t.Fatalf("new revision must start over: %q %v %s", cmd, ok, d.source())
	}
}
