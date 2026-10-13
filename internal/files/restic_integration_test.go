package files

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// realRestic returns the restic binary for integration tests (MCPANEL_TEST_RESTIC, set by
// scripts/vm-test.sh and CI); without it the test is skipped (always on the Mac).
func realRestic(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MCPANEL_TEST_RESTIC")
	if bin == "" {
		t.Skip("MCPANEL_TEST_RESTIC is not set")
	}
	return bin
}

func newRealService(t *testing.T) (*Service, string) {
	t.Helper()
	bin := realRestic(t)
	s, data := newService(t, func(c *Config) {
		c.ResticBinary = bin
		c.Limits.MaxBackupBytes = 64 << 20
		c.Limits.MaxBackupEntries = 10_000
	})
	if err := os.Mkdir(s.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if st := s.InitBackups(context.Background()); !st.Available {
		t.Fatalf("restic unavailable: %+v", st)
	}
	return s, data
}

func waitDone(t *testing.T, s *Service, id int64) filesapi.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		j := s.Job(id)
		if j != nil && j.State != jobRunning {
			if j.State != jobDone {
				t.Fatalf("job failed: %+v", *j)
			}
			return *j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still running: %+v", j)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func backupNow(t *testing.T, s *Service, kind filesapi.SnapshotKind, note string) string {
	t.Helper()
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: kind, Note: note}); err != nil {
		t.Fatal(err)
	}
	return waitDone(t, s, 1).Snapshot
}

func TestResticBackupListRestoreDownload(t *testing.T) {
	ctx := context.Background()
	s, data := newRealService(t)
	put(t, data, "world/level.dat", "version one")
	put(t, data, "server.properties", "motd=hi\nrcon.password=s3cret\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")
	sid := backupNow(t, s, filesapi.SnapshotManual, "vor Update")

	list, err := s.Snapshots(ctx, 1)
	if err != nil || len(list) != 1 || list[0].ID != sid || list[0].Kind != filesapi.SnapshotManual || list[0].Note != "vor Update" || list[0].Bytes <= 0 {
		t.Fatalf("list = %+v %v", list, err)
	}

	put(t, data, "world/level.dat", "version two")
	put(t, data, "new.txt", "appears later")
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	if got, _ := get(t, data, "world/level.dat"); got != "version one" {
		t.Fatalf("level.dat = %q", got)
	}
	if _, ok := get(t, data, "new.txt"); ok {
		t.Fatal("new.txt survived the restore")
	}
	if got, _ := get(t, data, ".rcon-cli.env"); got != "password=s3cret\n" {
		t.Fatalf("hidden file changed: %q", got)
	}
	if got, _ := get(t, data, "server.properties"); strings.Contains(got, "s3cret") {
		t.Fatalf("the password went into the snapshot: %q", got)
	}
	noTemp(t, data)

	var zipBuf bytes.Buffer
	if err := s.DownloadBackup(ctx, 1, sid, func() {}, &zipBuf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(b), "s3cret") {
			t.Fatalf("%s carries the password", f.Name)
		}
		found = found || f.Name == "world/level.dat"
	}
	if !found {
		t.Fatal("world/level.dat missing in the zip")
	}
}

func TestResticPinAndForgetPolicy(t *testing.T) {
	ctx := context.Background()
	s, data := newRealService(t)
	var sched []string
	for i := range 3 {
		put(t, data, "world/level.dat", strings.Repeat("x", i+1))
		sched = append(sched, backupNow(t, s, filesapi.SnapshotScheduled, ""))
	}
	manual := backupNow(t, s, filesapi.SnapshotManual, "")
	pinned, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sched[0], Pinned: true})
	if err != nil || !pinned.Pinned || pinned.ID == sched[0] {
		t.Fatalf("pin = %+v %v", pinned, err)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	list, err := s.Snapshots(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sn := range list {
		ids = append(ids, sn.ID)
	}
	slices.Sort(ids)
	want := []string{sched[2], manual, pinned.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("after forget: %v, want newest scheduled + manual + pinned %v", ids, want)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{pinned.ID}}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	if list, _ := s.Snapshots(ctx, 1); len(list) != 2 {
		t.Fatalf("after forget by id: %+v", list)
	}
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.repoDir(1)); !os.IsNotExist(err) {
		t.Fatalf("repo after purge: %v", err)
	}
}

// TestResticSnapshotTimeCarriesZone pins the reason for CreateBackupRequest.Timezone: restic
// stores the snapshot time in the zone of the backup process, and forget buckets by it.
func TestResticSnapshotTimeCarriesZone(t *testing.T) {
	bin := realRestic(t)
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled, Timezone: "Pacific/Kiritimati"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	cmd := exec.CommandContext(t.Context(), bin, "--no-cache", "--json", "--no-lock", "snapshots") //nolint:gosec // G204: test binary from MCPANEL_TEST_RESTIC
	cmd.Env = s.resticEnv(1)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var snaps []struct {
		Time string `json:"time"`
	}
	if err := json.Unmarshal(out, &snaps); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %s %v", out, err)
	}
	if !strings.HasSuffix(snaps[0].Time, "+14:00") {
		t.Fatalf("snapshot time %q is not in Pacific/Kiritimati (+14:00)", snaps[0].Time)
	}
}

// TestResticStaleLockIsRemoved (review focus #5): a restic killed with SIGKILL leaves its lock;
// the next job's `restic unlock` removes it.
func TestResticStaleLockIsRemoved(t *testing.T) {
	bin := realRestic(t)
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	backupNow(t, s, filesapi.SnapshotManual, "")
	cmd := exec.CommandContext(t.Context(), bin, "--no-cache", "backup", "--stdin", "--stdin-filename", "x") //nolint:gosec // G204: test binary from MCPANEL_TEST_RESTIC
	cmd.Env = s.resticEnv(1)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		locks, _ := os.ReadDir(filepath.Join(s.repoDir(1), "locks"))
		if len(locks) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restic took no lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	_ = stdin.Close()
	if _, err := s.ForgetBackups(context.Background(), filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 5}, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1) // forget needs an exclusive lock: it only works after the stale one is gone
}

func TestResticMissingKey(t *testing.T) {
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	backupNow(t, s, filesapi.SnapshotManual, "")
	if err := os.Remove(s.keyFile(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "key") {
		t.Fatalf("job = %+v", job)
	}
}

// TestResticErrorsHideHostPaths: a broken repository yields restic messages with host paths;
// what leaves the service names none.
func TestResticErrorsHideHostPaths(t *testing.T) {
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	sid := backupNow(t, s, filesapi.SnapshotManual, "")
	if err := os.RemoveAll(filepath.Join(s.repoDir(1), "data")); err != nil { // all packs gone
		t.Fatal(err)
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || strings.Contains(job.Error, s.cfg.InstancesDir) {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "a" {
		t.Fatalf("data changed by a failed restore: %q", got)
	}
	noTemp(t, data)
}

// TestResticCanceledBackupLeavesNoSnapshot (review): a backup cancelled while restic reads its
// stdin (SIGINT) and one killed before the end of the stream (too large) create no snapshot.
func TestResticCanceledBackupLeavesNoSnapshot(t *testing.T) {
	ctx := context.Background()
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	put(t, data, "b.txt", "b")
	entered, proceed := make(chan struct{}), make(chan struct{})
	s.backupHook = func(p string) {
		if p == "b.txt" {
			close(entered)
			<-proceed
		}
	}
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // restic gets its SIGINT while the stream is still open
	close(proceed)
	if job := waitJob(t, s, 1); job.State != jobFailed || job.Error != "the job was canceled" {
		t.Fatalf("canceled job = %+v", job)
	}
	if list, err := s.Snapshots(ctx, 1); err != nil || len(list) != 0 {
		t.Fatalf("snapshots after a cancel = %+v %v", list, err)
	}

	s.backupHook = nil
	s.cfg.Limits.MaxBackupBytes = 1 << 10
	put(t, data, "big.bin", strings.Repeat("x", 4<<10))
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed || job.Code != filesapi.CodeTooLarge {
		t.Fatalf("too large job = %+v", job)
	}
	if list, err := s.Snapshots(ctx, 1); err != nil || len(list) != 0 {
		t.Fatalf("snapshots after a killed backup = %+v %v", list, err)
	}
}

// TestResticIncompleteSnapshotIsRefused (review): a snapshot of a stream without the end entry
// (restic committed a cut stdin) is neither restored nor downloaded as a valid zip.
func TestResticIncompleteSnapshotIsRefused(t *testing.T) {
	bin := realRestic(t)
	ctx := context.Background()
	s, data := newRealService(t)
	put(t, data, "a.txt", "current")
	backupNow(t, s, filesapi.SnapshotManual, "") // creates the repository and its key
	cut := makeTarNoEnd(t, reg("a.txt", "old"))
	cmd := exec.CommandContext(t.Context(), bin, "--no-cache", "--json", "backup", "--stdin", "--stdin-filename", "data.tar", //nolint:gosec // G204: test binary from MCPANEL_TEST_RESTIC
		"--host", "mcpanel", "--tag", "mcpanel", "--tag", "kind=manual")
	cmd.Env = s.resticEnv(1)
	cmd.Stdin = bytes.NewReader(cut)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("restic backup: %v", err)
	}
	w := &summaryWriter{}
	_, _ = w.Write(out)
	sid, err := backupSnapshotID(w.Summary())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Code != filesapi.CodeInvalid || !strings.Contains(job.Error, "the snapshot is incomplete") {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "current" {
		t.Fatalf("a.txt = %q", got)
	}
	noTemp(t, data)
	var zipBuf bytes.Buffer
	if err := s.DownloadBackup(ctx, 1, sid, func() {}, &zipBuf); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("download = %v", err)
	}
	if _, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len())); err == nil {
		t.Fatal("the incomplete snapshot downloaded as a valid zip")
	}
}
