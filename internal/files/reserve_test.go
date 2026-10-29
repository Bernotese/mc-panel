package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
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

// Review focus 3, #153: a server already below the reserve can still read, delete and rename
// files; every editor save stages a full temp copy, so even one that shrinks or keeps the size
// is refused (the old file and no temp file remain).
func TestBelowReserveSavesAreRefused(t *testing.T) {
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
	for _, content := range []string{strings.Repeat("y", 100), "short", "longer than before"} {
		if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: content, ExpectedMTime: &mtime}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
			t.Fatalf("save of %d bytes: %v", len(content), err)
		}
	}
	if v, _ := get(t, data, "big.txt"); v != strings.Repeat("x", 100) {
		t.Fatalf("old file changed: %q", v)
	}
	noTemp(t, data)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: "", ExpectedMTime: &mtime}); err != nil {
		t.Fatalf("emptying a file stages no bytes: %v", err)
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

// #153: an editor save reserves the whole temp copy (the peak while old and new file both
// exist), not only its growth.
func TestSaveReservesTheTempCopy(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	put(t, data, "f.txt", strings.Repeat("x", 500))
	keep := uint64(s.cfg.Limits.ReserveBytes)
	rd, _ := s.Read(ctx, 1, "f.txt")
	m := rd.MTime
	disk.set(keep + 299)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "f.txt", Content: strings.Repeat("y", 300), ExpectedMTime: &m}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("shrinking save one byte short of its copy: %v", err)
	}
	disk.set(keep + 300)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "f.txt", Content: strings.Repeat("y", 300), ExpectedMTime: &m}); err != nil {
		t.Fatalf("exactly enough for the copy: %v", err)
	}
	if v, _ := get(t, data, "f.txt"); v != strings.Repeat("y", 300) {
		t.Fatalf("content %q", v)
	}
	if s.pending != 0 {
		t.Fatalf("pending %d after the save", s.pending)
	}
}

// lockCheckDisk fails the test when free space is sampled outside the accounting lock.
type lockCheckDisk struct {
	fakeDisk
	t *testing.T
	s *Service
}

func (d *lockCheckDisk) Available(p string) (uint64, error) {
	if d.s.mu.TryLock() {
		d.s.mu.Unlock()
		d.t.Error("free space sampled outside the accounting lock")
	}
	return d.fakeDisk.Available(p)
}

// #153: the free-space sample and the pending bytes it is compared with are taken under the
// same lock, so a reservation that lands meanwhile is either in both or in neither.
func TestReserveSamplesUnderTheLock(t *testing.T) {
	disk := &lockCheckDisk{fakeDisk: fakeDisk{avail: 1 << 40}, t: t}
	s, _ := newService(t, func(c *Config) { c.Disk = disk })
	disk.s = s
	r, err := s.reserve(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	r.release()
}

// landingDisk is a file system whose free space shrinks by exactly the bytes that land.
type landingDisk struct {
	mu    sync.Mutex
	avail uint64
}

func (d *landingDisk) Available(string) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.avail, nil
}

func (d *landingDisk) Total(string) (uint64, error) { return 0, nil }

// #153: reservations and landings running concurrently never admit more than is free above
// the reserve: the bytes on disk plus the bytes still announced never exceed the space.
func TestReserveNeverOverAdmits(t *testing.T) {
	const space, chunk = 1 << 20, 4096
	disk := &landingDisk{avail: space}
	s, _ := newService(t, func(c *Config) { c.Disk = disk; c.Limits.ReserveBytes = 0 })
	var used, admitted int64 // guarded by disk.mu
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 200 {
				r, err := s.reserve(1, chunk)
				if err != nil {
					continue
				}
				disk.mu.Lock()
				admitted += chunk
				if admitted > space {
					t.Errorf("admitted %d bytes of %d", admitted, space)
				}
				disk.avail -= chunk // the bytes land on disk ...
				used += chunk
				disk.mu.Unlock()
				r.landed(chunk) // ... and leave the reservation
				r.release()
				disk.mu.Lock()
				disk.avail += chunk // the file is deleted again
				used -= chunk
				admitted -= chunk
				disk.mu.Unlock()
			}
		})
	}
	wg.Wait()
	if s.pending != 0 || used != 0 {
		t.Fatalf("pending %d, used %d", s.pending, used)
	}
}
