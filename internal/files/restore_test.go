package files

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
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

// treeSums lists every entry below dir with its type, mode, size, mtime and (for files) sha256:
// two equal listings mean nothing in dir was written, renamed or touched.
func treeSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%v %d %d", fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p) //nolint:gosec // G122: test fixture below t.TempDir()
			if err != nil {
				return err
			}
			line += fmt.Sprintf(" %x", sha256.Sum256(b))
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = line
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRestoreFromRefusesSameID(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old")))
	for _, src := range []int64{1, -1} {
		if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid, SourceID: src}); !errors.Is(err, filesapi.ErrInvalid) {
			t.Fatalf("source %d: %v", src, err)
		}
	}
	if s.Job(1) != nil || len(f.callsOf("dump")) != 0 {
		t.Fatal("a refused restore-from started something")
	}
	if got, _ := get(t, data, "a.txt"); got != "current" {
		t.Fatalf("a.txt = %q", got)
	}
}

// TestRestoreFromOtherRepo (review focus #1): a snapshot of server 1 restored into server 2
// leaves server 1's data directory byte-identical (it is not even readable meanwhile) and
// its repository unwritten.
func TestRestoreFromOtherRepo(t *testing.T) {
	ctx := context.Background()
	s, src, f := newBackupService(t)
	put(t, src, "world/level.dat", "prod world, newer than the snapshot")
	put(t, src, "server.properties", "motd=prod\nrcon.password=prodsecret\n")
	put(t, src, ".rcon-cli.env", "password=prodsecret\n")
	sid := snapshotOf(t, s, f, makeTar(t, dir("world/"), reg("world/level.dat", "prod world"),
		reg("server.properties", "motd=prod\nrcon.password=********\n"), reg("mods/a.jar", "jar")))
	dev := filepath.Join(filepath.Dir(src), "2")
	put(t, dev, "world/level.dat", "dev world")
	put(t, dev, "dev-only.txt", "gone after the sync")
	put(t, dev, ".rcon-cli.env", "password=devsecret\n")
	before := treeSums(t, src)

	// The source's data directory cannot be opened at all while the restore runs.
	if err := os.Chmod(src, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(src, 0o750) })
	job, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 2, Snapshot: sid, SourceID: 1})
	if err != nil || job.Kind != filesapi.JobRestore || job.Snapshot != sid {
		t.Fatalf("restore-from = %+v %v", job, err)
	}
	if done := waitJob(t, s, 2); done.State != jobDone {
		t.Fatalf("job = %+v", done)
	}
	if err := os.Chmod(src, 0o750); err != nil {
		t.Fatal(err)
	}
	if after := treeSums(t, src); !maps.Equal(before, after) {
		t.Fatalf("the source's data directory changed:\nbefore %v\nafter  %v", before, after)
	}
	if s.Job(1) != nil {
		t.Fatalf("a job ran on the source: %+v", s.Job(1))
	}

	if got, _ := get(t, dev, "world/level.dat"); got != "prod world" {
		t.Fatalf("dev level.dat = %q", got)
	}
	if got, _ := get(t, dev, "mods/a.jar"); got != "jar" {
		t.Fatalf("dev mods/a.jar = %q", got)
	}
	if _, ok := get(t, dev, "dev-only.txt"); ok {
		t.Fatal("an entry of the dev that is not in the snapshot survived")
	}
	if got, _ := get(t, dev, ".rcon-cli.env"); got != "password=devsecret\n" {
		t.Fatalf("the dev's rcon file changed: %q", got)
	}
	if got, _ := get(t, dev, "server.properties"); strings.Contains(got, "prodsecret") {
		t.Fatalf("the source's rcon password reached the dev: %q", got)
	}
	noTemp(t, dev)

	// restic: the source's repository was dumped and nothing else; the dev got no repository.
	dumps := f.callsOf("dump")
	if len(dumps) != 1 || envOf(dumps[0].Env, "RESTIC_REPOSITORY") != s.repoDir(1) || envOf(dumps[0].Env, "RESTIC_PASSWORD_FILE") != s.keyFile(1) {
		t.Fatalf("dump calls = %+v", dumps)
	}
	for _, c := range f.calls {
		if cmd, _ := command(c.Args); cmd != "dump" && cmd != "snapshots" && cmd != "version" {
			t.Fatalf("restic %s ran during a restore-from: %+v", cmd, c)
		}
	}
	if _, err := os.Lstat(s.repoDir(2)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the dev got a repository: %v", err)
	}
	if len(f.snaps(s.repoDir(1))) != 1 {
		t.Fatal("the source's snapshots changed")
	}
}

// TestRestoreFromSourceBusy: a job on the source refuses the restore-from; while it reads the
// source's snapshot, no forget, pin or purge of the source can start.
func TestRestoreFromSourceBusy(t *testing.T) {
	ctx := context.Background()
	// Two restic jobs at a time, so only the source's reader refuses the forget below.
	s, src, f := newBackupService(t, func(c *Config) { c.Limits.MaxBackupJobs = 2 })
	put(t, src, "a.txt", "prod")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "snap")))
	dev := filepath.Join(filepath.Dir(src), "2")
	put(t, dev, "a.txt", "dev")

	f.gate = make(chan struct{})
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 2, Snapshot: sid, SourceID: 1}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("restore-from while the source backs up: %v", err)
	}
	if s.Job(2) != nil {
		t.Fatalf("a refused restore-from left a job: %+v", s.Job(2))
	}
	close(f.gate)
	waitJob(t, s, 1)

	f.gate = make(chan struct{})
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 2, Snapshot: sid, SourceID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid}}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("forget of the source during a restore-from: %v", err)
	}
	if _, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sid, Pinned: true}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("pin of the source during a restore-from: %v", err)
	}
	if err := s.PurgeBackups(ctx, 1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("purge of the source during a restore-from: %v", err)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 2, Snapshot: sid, SourceID: 1}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("second restore-from into the same target: %v", err)
	}
	close(f.gate)
	if job := waitJob(t, s, 2); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	// The reader is gone with the job.
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid}}); err != nil {
		t.Fatalf("forget of the source after the restore-from: %v", err)
	}
	waitJob(t, s, 1)
	if got, _ := get(t, src, "a.txt"); got != "prod" {
		t.Fatalf("source a.txt = %q", got)
	}
}

// TestRestoreFromMissingSourceRepo: a source without a repository (or without the snapshot)
// is a clean not-found; the target stays as it is and its slot is free again.
func TestRestoreFromMissingSourceRepo(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "target")
	before := treeSums(t, data)
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: strings.Repeat("a", 64), SourceID: 3}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("source without a repository: %v", err)
	}
	// A snapshot that exists only in the target's own repository is not found in the source's.
	own := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "own")))
	if err := s.ensureKey(3); err != nil {
		t.Fatal(err)
	}
	f.addSnap(t, s.repoDir(3), time.Now(), makeTar(t, reg("a.txt", "other")), "mcpanel", "kind=scheduled")
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: own, SourceID: 3}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("snapshot of another repository: %v", err)
	}
	if after := treeSums(t, data); !maps.Equal(before, after) {
		t.Fatal("the target changed")
	}
	if len(f.callsOf("dump")) != 0 || s.Job(1) != nil {
		t.Fatal("a refused restore-from started something")
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: own}); err != nil {
		t.Fatalf("the slot of the target was not given back: %v", err)
	}
	waitJob(t, s, 1)
}
