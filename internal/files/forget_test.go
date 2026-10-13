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

func TestForgetArgs(t *testing.T) {
	ids := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	if got := forgetArgs(filesapi.ForgetRequest{ID: 1, Snapshots: ids}); !slices.Equal(got,
		[]string{"forget", "--prune", "--max-unused", "10%", ids[0], ids[1]}) {
		t.Fatalf("ids: %q", got)
	}
	got := forgetArgs(filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 24, KeepDaily: 0, KeepWeekly: 4, KeepMonthly: 6}, Timezone: "Europe/Berlin"})
	want := []string{"forget", "--prune", "--max-unused", "10%", "--host", "mcpanel", "--tag", "kind=scheduled",
		"--keep-tag", "pinned", "--group-by", "host", "--keep-last", "24", "--keep-weekly", "4", "--keep-monthly", "6"}
	if !slices.Equal(got, want) {
		t.Fatalf("policy: %q\nwant %q", got, want)
	}
}

func TestForgetPolicyJob(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	repo := s.repoDir(1)
	t0 := time.Now()
	f.addSnap(t, repo, t0, nil, "mcpanel", "kind=scheduled")
	keep := f.addSnap(t, repo, t0.Add(time.Hour), nil, "mcpanel", "kind=scheduled")
	pinned := f.addSnap(t, repo, t0.Add(-time.Hour), nil, "mcpanel", "kind=scheduled", "pinned")
	manual := f.addSnap(t, repo, t0.Add(-2*time.Hour), nil, "mcpanel", "kind=manual")
	job, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "Europe/Berlin"})
	if err != nil || job.Kind != filesapi.JobForget {
		t.Fatalf("forget = %+v %v", job, err)
	}
	if done := waitJob(t, s, 1); done.State != jobDone {
		t.Fatalf("job = %+v", done)
	}
	var left []string
	for _, sn := range f.snaps(repo) {
		left = append(left, sn.ID)
	}
	slices.Sort(left)
	want := []string{keep, pinned, manual}
	slices.Sort(want)
	if !slices.Equal(left, want) {
		t.Fatalf("left = %v, want %v", left, want)
	}
	c := f.callsOf("forget")
	if len(c) != 1 || envOf(c[0].Env, "TZ") != "Europe/Berlin" || len(f.callsOf("unlock")) != 1 {
		t.Fatalf("forget calls = %+v", c)
	}
}

func TestForgetIDsAndErrors(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	// Policy without repository: a job that is done at once.
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone || len(f.callsOf("forget")) != 0 {
		t.Fatalf("policy without repo: %+v", job)
	}
	// IDs without repository / unknown IDs: 404 before any job.
	unknown := strings.Repeat("c", 64)
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{unknown}}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("ids without repo: %v", err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	sid := f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=pre-restore")
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid, unknown}}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("one unknown id: %v", err)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid}}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone || len(f.snaps(s.repoDir(1))) != 0 {
		t.Fatalf("forget id: %+v, %d left", job, len(f.snaps(s.repoDir(1))))
	}
	// Invalid requests (filesapi.ForgetRequest.Validate).
	for _, req := range []filesapi.ForgetRequest{
		{ID: 1},
		{ID: 1, Snapshots: []string{sid}, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "UTC"},
		{ID: 1, Policy: &filesapi.ForgetPolicy{}, Timezone: "UTC"},
		{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "Mars/Olympus"},
	} {
		if _, err := s.ForgetBackups(ctx, req); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%+v: %v", req, err)
		}
	}
}

func TestPinBackup(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	sched := f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=scheduled")
	manual := f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=manual")
	sn, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sched, Pinned: true})
	if err != nil || !sn.Pinned || sn.ID == sched || sn.Kind != filesapi.SnapshotScheduled {
		t.Fatalf("pin = %+v %v (new id expected)", sn, err)
	}
	if c := f.callsOf("tag"); !slices.Equal(c[0].Args, []string{"--no-cache", "--json", "tag", "--add", "pinned", sched}) {
		t.Fatalf("tag args = %q", c[0].Args)
	}
	same, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sn.ID, Pinned: true})
	if err != nil || same.ID != sn.ID {
		t.Fatalf("pin again = %+v %v (no change, same id)", same, err)
	}
	un, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sn.ID, Pinned: false})
	if err != nil || un.Pinned || un.ID == sn.ID {
		t.Fatalf("unpin = %+v %v", un, err)
	}
	if _, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: manual, Pinned: true}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("pin manual: %v", err)
	}
	// While a job runs for the instance: busy.
	if err := s.jobs.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: un.ID, Pinned: true}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("pin during a job: %v", err)
	}
	s.jobs.release(1)
}

func TestPurgeBackups(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatalf("purge without repo: %v", err)
	}
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=manual")
	if err := s.jobs.acquire(1, filesapi.JobForget); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeBackups(ctx, 1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("purge during a job: %v", err)
	}
	s.jobs.release(1)
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.repoDir(1), s.keyFile(1)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", p, err)
		}
	}
}

// Purge needs no restic (V5): a server can be deleted with its backups on a host without it,
// and a symlinked repos/ entry is removed as a link, never followed.
func TestPurgeBackupsWithoutRestic(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, func(c *Config) { c.ResticBinary = "/nonexistent/restic" }) // restic is unavailable
	s.InitBackups(ctx)
	if st := s.BackupsStatus(); st.Available {
		t.Fatalf("status = %+v", st)
	}
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatalf("purge without backups/: %v", err)
	}
	for _, d := range []string{"repos/1", "keys"} {
		if err := os.MkdirAll(filepath.Join(s.backupsDir(), d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.backupsDir(), "repos", "1", "config"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.backupsDir(), "keys", "1"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.backupsDir(), "repos", "2")); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatalf("purge without restic: %v", err)
	}
	if err := s.PurgeBackups(ctx, 2); err != nil {
		t.Fatalf("purge of a symlinked repo: %v", err)
	}
	for _, p := range []string{"repos/1", "keys/1", "repos/2"} {
		if _, err := os.Lstat(filepath.Join(s.backupsDir(), p)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatalf("purge followed the symlink: %v", err)
	}
}

// TestDownloadAndForgetExclude (review focus #4).
func TestDownloadAndForgetExclude(t *testing.T) {
	j := newJobs(1)
	release, err := j.addReader(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.acquire(1, filesapi.JobForget); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("forget during a download: %v", err)
	}
	if _, err := j.lock(1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("pin/purge during a download: %v", err)
	}
	if err := j.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatalf("backup during a download: %v", err) // both only read-lock the repository
	}
	j.release(1)
	release()
	if err := j.acquire(1, filesapi.JobForget); err != nil {
		t.Fatal(err)
	}
	if _, err := j.addReader(1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("download during forget: %v", err)
	}
}
