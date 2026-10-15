package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// SEC-04: the reserve is reserve_bytes or reserve_percent of the file system, whichever is
// larger.
func TestReservePercent(t *testing.T) {
	disk := &fakeDisk{avail: 60_000, total: 1_000_000}
	s, _ := newService(t, func(c *Config) {
		c.Disk = disk
		c.Limits.ReservePercent = 5 // 50 000 bytes, more than the 1 KiB reserve_bytes
	})
	r, err := s.reserve(1, 10_000)
	if err != nil {
		t.Fatalf("exactly enough: %v", err)
	}
	r.release()
	if _, err := s.reserve(1, 10_001); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("one byte into the percentage reserve: %v", err)
	}
	s.cfg.Limits.ReservePercent = 0
	if r, err := s.reserve(1, 10_001); err != nil {
		t.Fatalf("without the percentage only reserve_bytes counts: %v", err)
	} else {
		r.release()
	}
}

// Review focus 3: a server already below the reserve can still read, delete, rename and shrink
// files; only writes that grow data are refused.
func TestBelowReserveOnlyGrowthIsRefused(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	put(t, data, "big.txt", strings.Repeat("x", 100))
	put(t, data, "old.log", "old")
	put(t, data, "a.txt", "a")
	disk.set(0) // the disk is full
	rd, err := s.Read(ctx, 1, "big.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	mtime := rd.MTime
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: strings.Repeat("y", 100), ExpectedMTime: &mtime}); err != nil {
		t.Fatalf("same size: %v", err)
	}
	rd, _ = s.Read(ctx, 1, "big.txt")
	mtime = rd.MTime
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: "short", ExpectedMTime: &mtime}); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	rd, _ = s.Read(ctx, 1, "big.txt")
	mtime = rd.MTime
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: "longer than before", ExpectedMTime: &mtime}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("grow: %v", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "new.txt", Content: "x"}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("new file: %v", err)
	}
	if err := s.Delete(ctx, 1, "old.log"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "a.txt", To: "b.txt"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := upload(s, "up.txt", false, "x"); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("upload: %v", err)
	}
	sum := sha256.Sum256([]byte("x"))
	if _, err := uploadChecked(s, "plugin.jar", false, "x", filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: hex.EncodeToString(sum[:])}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("plugin upload: %v", err)
	}
}

// dropDisk reports plenty of free space for the first calls, then little.
type dropDisk struct {
	calls, after int64
	fakeDisk
}

func (d *dropDisk) Available(p string) (uint64, error) {
	if atomic.AddInt64(&d.calls, 1) > d.after {
		return 1<<9 - 1, nil // below half the 1 KiB test reserve
	}
	return d.fakeDisk.Available(p)
}

// SEC-04: a backup whose disk fills up while it runs ends with insufficient_storage, and restic
// is killed before its stdin ends (no snapshot).
func TestBackupAbortsWhenSpaceRunsOut(t *testing.T) {
	old := backupSpaceEvery
	backupSpaceEvery = 1
	t.Cleanup(func() { backupSpaceEvery = old })
	disk := &dropDisk{after: 2, fakeDisk: fakeDisk{avail: 1 << 40}} // the check before the start, then one in the stream
	s, data, f := newBackupService(t, func(c *Config) { c.Disk = disk })
	for _, p := range []string{"a", "b", "c", "d"} {
		put(t, data, "world/"+p, strings.Repeat(p, 4096))
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Code != filesapi.CodeInsufficientStorage {
		t.Fatalf("job = %+v", job)
	}
	if len(f.snaps(s.repoDir(1))) != 0 || len(f.sawEOF) != 1 || f.sawEOF[0] {
		t.Fatalf("snapshots %d, sawEOF %v: restic must be killed before the stream ends", len(f.snaps(s.repoDir(1))), f.sawEOF)
	}
}
