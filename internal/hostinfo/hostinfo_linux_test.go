//go:build linux

package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemTotalFrom(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal: 2048 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := memTotalFrom(dir); err != nil || got != 2048*1024 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := memTotalFrom(t.TempDir()); err == nil {
		t.Fatal("a missing meminfo must fail")
	}
}

func TestMemTotalRealProc(t *testing.T) {
	if got, err := MemTotal(); err != nil || got == 0 {
		t.Fatalf("real /proc: %d, %v", got, err)
	}
}
