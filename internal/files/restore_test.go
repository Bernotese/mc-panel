package files

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// snapshotOf puts a tar as a snapshot of server 1 into the fake repository (with key).
func snapshotOf(t *testing.T, s *Service, f *fakeRestic, b []byte) string {
	t.Helper()
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	return f.addSnap(t, s.repoDir(1), time.Now(), b, "mcpanel", "kind=manual")
}

func TestRestoreReplacesTheTopLevel(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "new world")
	put(t, data, "only-now.txt", "gone after restore")
	put(t, data, ".rcon-cli.env", "password=keep\n")
	sid := snapshotOf(t, s, f, makeTar(t, dir("world/"), reg("world/level.dat", "old world"), reg("server.properties", "motd=old\n")))
	job, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid})
	if err != nil || job.Kind != filesapi.JobRestore || job.Snapshot != sid {
		t.Fatalf("restore = %+v %v", job, err)
	}
	if done := waitJob(t, s, 1); done.State != jobDone {
		t.Fatalf("job = %+v", done)
	}
	if got, _ := get(t, data, "world/level.dat"); got != "old world" {
		t.Fatalf("level.dat = %q", got)
	}
	if _, ok := get(t, data, "only-now.txt"); ok {
		t.Fatal("an entry that is not in the snapshot survived")
	}
	if got, _ := get(t, data, ".rcon-cli.env"); got != "password=keep\n" {
		t.Fatalf("hidden file changed: %q", got)
	}
	noTemp(t, data)
	if c := f.callsOf("dump"); len(c) != 1 || !slices.Equal(c[0].Args, []string{"--no-cache", "dump", sid, "/data.tar"}) {
		t.Fatalf("dump calls = %+v", c)
	}
}

// TestRestoreHostileSnapshotLeavesDataAlone (review focus #3).
func TestRestoreHostileSnapshotLeavesDataAlone(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "current")
	sid := snapshotOf(t, s, f, makeTar(t, reg("ok.txt", "x"), tarEntry{name: "evil", typ: '2', linkname: "/etc/shadow"}))
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "neither a file nor a folder") {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "world/level.dat"); got != "current" {
		t.Fatalf("data changed: %q", got)
	}
	if _, ok := get(t, data, "ok.txt"); ok {
		t.Fatal("part of a failed restore is visible")
	}
	noTemp(t, data)
}

func TestRestorePreChecks(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: "nothex"}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: strings.Repeat("a", 64)}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
	sid := snapshotOf(t, s, f, makeTar(t, reg("a", "x")))
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 9, Snapshot: sid}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("no data directory: %v", err)
	}
}

func TestRestoreNeedsFreeSpace(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, _, f := newBackupService(t, func(c *Config) { c.Disk = disk })
	sid := snapshotOf(t, s, f, makeTar(t, reg("a", strings.Repeat("x", 4096))))
	disk.set(4096) // snapshot (≈5 KiB tar) + 1 KiB reserve do not fit
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("full disk: %v", err)
	}
	if len(f.callsOf("dump")) != 0 {
		t.Fatal("dump ran on a full disk")
	}
}

func TestSwapRollsBackOnFailure(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	put(t, data, "b.txt", "current b")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a"), reg("c.txt", "old c")))
	s.swapHook = func(step int) error {
		if step == 3 { // after both old entries moved away and one new one moved up
			return errors.New("simulated rename failure")
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed {
		t.Fatalf("job = %+v", job)
	}
	if a, _ := get(t, data, "a.txt"); a != "current a" {
		t.Fatalf("a.txt = %q (rollback)", a)
	}
	if b, _ := get(t, data, "b.txt"); b != "current b" {
		t.Fatalf("b.txt = %q (rollback)", b)
	}
	if _, ok := get(t, data, "c.txt"); ok {
		t.Fatal("c.txt of the snapshot is visible after the rollback")
	}
	noTemp(t, data)
}

func TestCancelJob(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	if err := s.CancelJob(1); err != nil {
		t.Fatalf("no job: %v", err)
	}
	f.gate = make(chan struct{})
	put(t, data, "a", "a")
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Error != "the job was canceled" || len(f.snaps(s.repoDir(1))) != 0 {
		t.Fatalf("canceled backup: %+v", job)
	}
	// Extract jobs cannot be canceled.
	f.gate = nil
	putBytes(t, data, "w.zip", makeZip(t, zipEntry{name: "x.txt", body: "x"}))
	release := make(chan struct{})
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelJob(1); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("cancel extract: %v", err)
	}
	close(release)
	waitJob(t, s, 1)
}

func TestCancelRestoreBeforeAndAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old")))

	// Before the swap: the data directory stays.
	f.gate = make(chan struct{})
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed || job.Error != "the job was canceled" {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "current" {
		t.Fatalf("a.txt = %q", got)
	}
	noTemp(t, data)

	// During the swap: cancel has no effect, the restore completes.
	f.gate = nil
	entered := make(chan struct{})
	proceed := make(chan struct{})
	s.swapHook = func(step int) error {
		if step == 0 {
			close(entered)
			<-proceed
		}
		return nil
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job after a late cancel = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "old" {
		t.Fatalf("a.txt = %q", got)
	}
}
