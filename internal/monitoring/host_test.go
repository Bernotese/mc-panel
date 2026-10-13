package monitoring

import (
	"errors"
	"testing"
)

func TestHostReaderDiskFromStatfs(t *testing.T) {
	h := &hostReader{disk: "/srv/mc-panel", statfs: func(dir string) (uint64, uint64, error) {
		if dir != "/srv/mc-panel" {
			t.Fatalf("statfs(%q)", dir)
		}
		return 40 << 30, 100 << 30, nil
	}}
	s := h.read()
	if s.DiskFree == nil || s.DiskTotal == nil || *s.DiskFree != 40<<30 || *s.DiskTotal != 100<<30 {
		t.Fatalf("disk = %v/%v", s.DiskFree, s.DiskTotal)
	}
}

func TestHostReaderWithoutDisk(t *testing.T) {
	h := &hostReader{statfs: func(string) (uint64, uint64, error) { return 0, 0, errors.New("x") }}
	if s := h.read(); s.DiskFree != nil || s.DiskTotal != nil {
		t.Fatalf("no instances_dir must leave disk unset: %+v", s)
	}
	h.disk = "/nope"
	if s := h.read(); s.DiskFree != nil {
		t.Fatal("statfs error must leave disk unset")
	}
	h.statfs = func(string) (uint64, uint64, error) { return 0, 0, nil }
	if s := h.read(); s.DiskFree != nil {
		t.Fatal("a zero-sized file system must leave disk unset")
	}
}

func TestStatfsBytesOnTempDir(t *testing.T) {
	free, total, err := statfsBytes(t.TempDir())
	if err != nil || total == 0 || free > total {
		t.Fatalf("statfs = %d/%d (%v)", free, total, err)
	}
}
