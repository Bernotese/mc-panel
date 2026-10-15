package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestCreateBackupRunsResticWithTheStream(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "level")
	put(t, data, "server.properties", "rcon.password=s3cret\n")
	job, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual, Note: "vor Update", Timezone: "Europe/Berlin"})
	if err != nil || job.Kind != filesapi.JobBackup || job.State != jobRunning {
		t.Fatalf("create = %+v %v", job, err)
	}
	done := waitJob(t, s, 1)
	if done.State != jobDone || !filesapi.ValidSnapshotID(done.Snapshot) || done.Entries != 3 {
		t.Fatalf("job = %+v", done)
	}
	backups := f.callsOf("backup")
	if len(backups) != 1 {
		t.Fatalf("backup calls = %d", len(backups))
	}
	want := []string{"--no-cache", "--json", "backup", "--stdin", "--stdin-filename", "data.tar", "--host", "mcpanel",
		"--tag", "mcpanel", "--tag", "kind=manual", "--tag", noteTag("vor Update")}
	if !slices.Equal(backups[0].Args, want) {
		t.Fatalf("args = %q\nwant   %q", backups[0].Args, want)
	}
	if tz := envOf(backups[0].Env, "TZ"); tz != "Europe/Berlin" { // restic stores the snapshot time in this zone
		t.Fatalf("backup TZ = %q", tz)
	}
	if len(f.callsOf("init")) != 1 || len(f.callsOf("unlock")) != 1 {
		t.Fatalf("init %d, unlock %d", len(f.callsOf("init")), len(f.callsOf("unlock")))
	}
	snaps := f.snaps(s.repoDir(1))
	if len(snaps) != 1 || strings.Contains(string(snaps[0].Data), "s3cret") || !strings.Contains(string(snaps[0].Data), "level") {
		t.Fatalf("snapshot data = %q", snaps[0].Data)
	}
	if !f.sawEOF[0] {
		t.Fatal("restic did not see the end of the stream")
	}

	list, err := s.Snapshots(ctx, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("snapshots = %+v %v", list, err)
	}
	sn := list[0]
	if sn.ID != done.Snapshot || sn.ShortID != sn.ID[:8] || sn.Kind != filesapi.SnapshotManual || sn.Note != "vor Update" ||
		sn.Pinned || sn.Bytes != int64(len(snaps[0].Data)) || sn.Time != snaps[0].Time.UnixMilli() {
		t.Fatalf("snapshot = %+v", sn)
	}
	if c := f.callsOf("snapshots"); !slices.Equal(c[0].Args, []string{"--no-cache", "--json", "--no-lock", "snapshots", "--host", "mcpanel", "--tag", "mcpanel"}) {
		t.Fatalf("snapshots args = %q", c[0].Args)
	}
}

func TestCreateBackupValidation(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newBackupService(t)
	for _, req := range []filesapi.CreateBackupRequest{
		{ID: 1, Kind: "weekly"},
		{ID: 1, Kind: filesapi.SnapshotScheduled, Note: "only manual backups carry a note"},
		{ID: 1, Kind: filesapi.SnapshotManual, Note: "bad\nnote"},
		{ID: 1, Kind: filesapi.SnapshotManual, Note: strings.Repeat("ä", 65)},
		{ID: 0, Kind: filesapi.SnapshotManual},
		{ID: 1, Kind: filesapi.SnapshotScheduled, Timezone: "Mars/Olympus"},
		{ID: 1, Kind: filesapi.SnapshotScheduled, Timezone: "Europe/Berlin\nTZ=UTC"},
	} {
		if _, err := s.CreateBackup(ctx, req); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%+v: %v, want invalid", req, err)
		}
	}
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 7, Kind: filesapi.SnapshotManual}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("no data directory: %v", err)
	}
}

func TestBackupNeedsFreeSpace(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 9} // below the 1 KiB test reserve
	s, _, f := newBackupService(t, func(c *Config) { c.Disk = disk })
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("full disk: %v", err)
	}
	if len(f.callsOf("backup")) != 0 {
		t.Fatal("restic ran on a full disk")
	}
	if err := s.jobs.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatalf("slot leaked: %v", err)
	}
}

// TestBackupTooLargeLeavesNoSnapshot (review focus #2): a stream over the limit kills restic
// before its stdin ends, so no snapshot of a cut stream exists.
func TestBackupTooLargeLeavesNoSnapshot(t *testing.T) {
	s, data, f := newBackupService(t, func(c *Config) { c.Limits.MaxBackupBytes = 4 })
	put(t, data, "a", "12345")
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "more than 4 bytes") || job.Code != filesapi.CodeTooLarge {
		t.Fatalf("job = %+v (want code %s)", job, filesapi.CodeTooLarge)
	}
	if len(f.snaps(s.repoDir(1))) != 0 || len(f.sawEOF) != 1 || f.sawEOF[0] {
		t.Fatalf("snapshots %d, sawEOF %v: restic must be killed before the stream ends", len(f.snaps(s.repoDir(1))), f.sawEOF)
	}
}

func TestBackupResticFailureIsReported(t *testing.T) {
	s, _, f := newBackupService(t)
	f.fail["backup"] = &resticExit{Code: 1, Stderr: "Fatal: unable to save snapshot: disk full in " + s.repoDir(1) + "\n"}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "restic backup failed") || strings.Contains(job.Error, s.cfg.InstancesDir) || job.Code != "" {
		t.Fatalf("job = %+v (an error without sentinel has no code)", job)
	}
}

func TestBackupMissingKeyFails(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=manual") // repository exists, key does not
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed || !strings.Contains(job.Error, "key") {
		t.Fatalf("job = %+v", job)
	}
}

func TestSnapshotsWithoutRepoAndFiltering(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if list, err := s.Snapshots(ctx, 1); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("no repo: %v %v", list, err)
	}
	if len(f.callsOf("snapshots")) != 0 {
		t.Fatal("restic was asked although no repository exists")
	}
	repo := s.repoDir(1)
	t0 := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)
	old := f.addSnap(t, repo, t0, []byte("x"), "mcpanel", "kind=scheduled", "pinned")
	newer := f.addSnap(t, repo, t0.Add(time.Hour), []byte("xy"), "mcpanel", "kind=pre-restore")
	f.addSnap(t, repo, t0.Add(2*time.Hour), nil, "mcpanel")                           // no kind: ignored
	f.addSnap(t, repo, t0.Add(3*time.Hour), nil, "mcpanel", "kind=hourly")            // unknown kind: ignored
	f.addSnap(t, repo, t0.Add(4*time.Hour), nil, "mcpanel", "kind=manual", "note=!!") // broken note: no note
	list, err := s.Snapshots(ctx, 1)
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if list[1].ID != newer || list[2].ID != old || !list[2].Pinned || list[2].Kind != filesapi.SnapshotScheduled || list[0].Note != "" {
		t.Fatalf("order/fields = %+v", list)
	}
	if _, err := s.findSnapshot(ctx, 1, strings.Repeat("a", 64)); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
}

func TestNoteTagRoundTrip(t *testing.T) {
	for _, n := range []string{"vor Update auf 1.21.11", "ä/ö=ü,+", strings.Repeat("x", 64)} {
		tag := noteTag(n)
		if strings.ContainsAny(tag[len("note="):], ",=/+") {
			t.Fatalf("tag %q carries characters restic would split or that are not base64url", tag)
		}
		if got := parseNote(tag[len("note="):]); got != n {
			t.Fatalf("round trip %q -> %q", n, got)
		}
	}
}

func TestBackupsCancelWhileRunning(t *testing.T) {
	s, data, f := newBackupService(t)
	f.gate = make(chan struct{}) // the backup waits before it reads its stdin
	put(t, data, "a", "a")
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("second backup: %v", err)
	}
	s.Close() // shutdown: the job ends as failed, no snapshot
	job := s.Job(1)
	if job.State != jobFailed || len(f.snaps(s.repoDir(1))) != 0 {
		t.Fatalf("after close: %+v, %d snapshots", job, len(f.snaps(s.repoDir(1))))
	}
}

// SEC-25: a large sparse file is left out of a backup (counted as skipped, logged); a large
// file with real data is not.
func TestBackupSkipsSparseFiles(t *testing.T) {
	old := sparseMinSize
	sparseMinSize = 1 << 20
	t.Cleanup(func() { sparseMinSize = old })
	s, data, f := newBackupService(t, func(c *Config) { c.Limits.MaxBackupBytes = 1 << 30 })
	put(t, data, "world/level.dat", "level")
	put(t, data, "world/big.db", strings.Repeat("d", 2<<20))
	put(t, data, "huge.bin", "")
	if err := os.Truncate(filepath.Join(data, "huge.bin"), 1<<30); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(data, "huge.bin")); err != nil {
		t.Fatal(err)
	} else if alloc, ok := allocatedBytes(fi); !ok || alloc >= fi.Size()/8 {
		t.Skipf("the file system did not create a sparse file (allocated %d)", alloc)
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobDone || job.Skipped != 1 {
		t.Fatalf("job = %+v", job)
	}
	snaps := f.snaps(s.repoDir(1))
	if len(snaps) != 1 || strings.Contains(string(snaps[0].Data), "huge.bin") || !strings.Contains(string(snaps[0].Data), "level.dat") ||
		!strings.Contains(string(snaps[0].Data), "world/big.db") {
		t.Fatalf("snapshot holds %d bytes", len(snaps[0].Data))
	}
}
