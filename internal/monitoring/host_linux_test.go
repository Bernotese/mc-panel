//go:build linux

package monitoring

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostReaderProc(t *testing.T) {
	dir := t.TempDir()
	cp := func(src, dst string) {
		b, err := os.ReadFile(filepath.Join("testdata", "proc", src))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dst), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cp("a/stat", "stat")
	cp("a/meminfo", "meminfo")
	cp("a/loadavg", "loadavg")
	h := &hostReader{proc: dir, statfs: func(string) (uint64, uint64, error) { return 0, 0, os.ErrNotExist }}

	first := h.read()
	if first.CPUPercent != nil {
		t.Fatal("the first reading has no CPU delta yet")
	}
	if first.MemoryTotal == nil || *first.MemoryTotal != 8000000*1024 || *first.MemoryUsed != 2000000*1024 {
		t.Fatalf("mem = %v/%v", first.MemoryUsed, first.MemoryTotal)
	}
	if first.Load1 == nil || !cNear(*first.Load1, 0.52) {
		t.Fatalf("load = %v", first.Load1)
	}

	cp("b/stat", "stat")
	second := h.read()
	if second.CPUPercent == nil || !cNear(*second.CPUPercent, 60) {
		t.Fatalf("cpu = %v, want 60", second.CPUPercent)
	}
	// counters that go backwards (e.g. a CPU taken offline) give no value instead of nonsense
	cp("a/stat", "stat")
	if third := h.read(); third.CPUPercent != nil {
		t.Fatalf("cpu after a counter reset = %v", *third.CPUPercent)
	}
}

func TestHostReaderBrokenProc(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"stat": "cpu x y z\n", "meminfo": "MemTotal: lots\n", "loadavg": "\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := &hostReader{proc: dir, statfs: func(string) (uint64, uint64, error) { return 0, 0, os.ErrNotExist }}
	h.read()
	s := h.read()
	if s.CPUPercent != nil || s.MemoryTotal != nil || s.Load1 != nil {
		t.Fatalf("broken files must give no values: %+v", s)
	}
}

func TestHostReaderRealProc(t *testing.T) {
	h := newHostReader(t.TempDir())
	h.read()
	s := h.read()
	if s.MemoryTotal == nil || s.Load1 == nil || s.DiskTotal == nil {
		t.Fatalf("real /proc: %+v", s)
	}
}
