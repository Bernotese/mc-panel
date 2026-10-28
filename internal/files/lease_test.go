package files

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func (j *jobs) leaseCount(id int64) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.leases[id]
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mutations runs every plain file mutation on server 1 and returns their errors by name.
func mutations(s *Service) map[string]error {
	ctx := context.Background()
	out := map[string]error{}
	_, out["write"] = s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "new.txt", Content: "x"})
	out["mkdir"] = s.Mkdir(ctx, 1, "newdir")
	out["rename"] = s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "a.txt", To: "moved.txt"})
	out["delete"] = s.Delete(ctx, 1, "a.txt")
	_, out["upload"] = s.Upload(ctx, 1, "up.txt", false, 1, strings.NewReader("x"))
	_, out["upload checked"] = s.UploadChecked(ctx, 1, "up.jar", false, 1,
		filesapi.Checksum{Algo: "sha256", Hex: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"}, strings.NewReader("x"))
	return out
}

func TestLeaseRefusedDuringRestore(t *testing.T) {
	for _, k := range []filesapi.JobKind{filesapi.JobRestore, filesapi.JobExtract} {
		t.Run(string(k), func(t *testing.T) {
			s, data := newService(t)
			put(t, data, "a.txt", "a")
			if err := s.jobs.acquire(1, k); err != nil {
				t.Fatal(err)
			}
			for op, err := range mutations(s) {
				if !errors.Is(err, filesapi.ErrBusy) {
					t.Errorf("%s: %v", op, err)
				}
			}
			assertFiles(t, data, map[string]string{"a.txt": "a"}, "new.txt", "newdir", "moved.txt", "up.txt", "up.jar")
			s.jobs.release(1)
			for op, err := range mutations(s) {
				if err != nil {
					t.Errorf("%s after release: %v", op, err)
				}
			}
		})
	}
	// A backup, forget or lock does not rewrite the server: mutations go on.
	s, data := newService(t)
	put(t, data, "a.txt", "a")
	if err := s.jobs.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	for op, err := range mutations(s) {
		if err != nil {
			t.Errorf("%s during a backup: %v", op, err)
		}
	}
}

// TestLeaseRefusedInsideSwap: a save that arrives while a real restore swaps the top level is
// refused and never lands in the restored tree.
func TestLeaseRefusedInsideSwap(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a")))
	errs := map[string]error{}
	s.swapHook = func(step int) error {
		if step == 1 { // a.txt is in <tmp>/old, the restored one not up yet
			errs = mutations(s)
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	for op, err := range errs {
		if !errors.Is(err, filesapi.ErrBusy) {
			t.Errorf("%s inside the swap: %v", op, err)
		}
	}
	assertFiles(t, data, map[string]string{"a.txt": "old a"}, "new.txt", "newdir", "moved.txt", "up.txt", "up.jar")
}

func TestLeaseVsRestoreWaitsThenBusy(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a")))
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "x.txt", body: "x"}))

	rel, err := s.lease(1)
	if err != nil {
		t.Fatal(err)
	}
	// A lease that is held longer than the wait: busy, and the slot is free again.
	s.jobs.drainWait = 50 * time.Millisecond
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, filesapi.ErrBusy) ||
		!strings.Contains(err.Error(), "file operations") {
		t.Fatalf("restore: %v", err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("extract: %v", err)
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe {
		t.Fatalf("state after the refused restore = %+v", st)
	}
	// A canceled caller stops waiting.
	cctx, cancel := context.WithCancel(ctx)
	s.jobs.drainWait = time.Minute
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := s.RestoreBackup(cctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled restore: %v", err)
	}

	// A lease that ends within the wait: the restore is admitted; meanwhile the server is
	// busy and new leases are refused at once.
	done := make(chan error, 1)
	go func() {
		_, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid})
		done <- err
	}()
	waitFor(t, "the restore to wait", func() bool { return s.jobs.rewriting(1) })
	if st := mustState(t, s, 1); st.State != filesapi.StateBusy {
		t.Fatalf("state while draining = %+v", st)
	}
	if _, err := s.lease(1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("lease while draining: %v", err)
	}
	rel()
	rel() // a second release is a no-op
	if err := <-done; err != nil {
		t.Fatalf("restore after the lease ended: %v", err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	assertFiles(t, data, map[string]string{"a.txt": "old a"})
	if n := s.jobs.leaseCount(1); n != 0 {
		t.Fatalf("leases = %d", n)
	}
}

// TestLeaseVsExtract: a rename or delete that holds the server makes an extract wait.
func TestLeaseVsExtract(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "x.txt", body: "x"}))
	rel, err := s.lease(1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Extract(context.Background(), filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"})
		done <- err
	}()
	waitFor(t, "the extract to wait", func() bool { return s.jobs.rewriting(1) })
	if err := s.Delete(context.Background(), 1, "a.zip"); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("delete while the extract waits: %v", err)
	}
	rel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	assertFiles(t, data, map[string]string{"out/x.txt": "x"})
}

func TestLeaseUploadHeldWhileStreaming(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a")))
	s.jobs.drainWait = 50 * time.Millisecond
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := s.Upload(ctx, 1, "big.bin", false, 10, pr)
		done <- err
	}()
	if _, err := pw.Write([]byte("01234")); err != nil {
		t.Fatal(err)
	}
	if n := s.jobs.leaseCount(1); n != 1 {
		t.Fatalf("leases while streaming = %d", n)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("restore during the upload: %v", err)
	}
	if _, err := pw.Write([]byte("56789")); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := s.jobs.leaseCount(1); n != 0 {
		t.Fatalf("leases after the upload = %d", n)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	assertFiles(t, data, map[string]string{"a.txt": "old a"}, "big.bin")
}

func TestBackupNotBlockedByLease(t *testing.T) {
	s, data, _ := newBackupService(t)
	put(t, data, "a.txt", "a")
	rel, err := s.lease(1)
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: "manual"}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	// and a lease is taken while a backup holds the server
	if err := s.jobs.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	defer s.jobs.release(1)
	rel2, err := s.lease(1)
	if err != nil {
		t.Fatal(err)
	}
	rel2()
}

// TestDrainWakesOnClose: a restore waiting for leases ends when the service shuts down.
func TestDrainWakesOnClose(t *testing.T) {
	s, _ := newService(t)
	if _, err := s.lease(1); err != nil {
		t.Fatal(err)
	}
	if err := s.jobs.acquire(1, filesapi.JobRestore); err != nil {
		t.Fatal(err)
	}
	s.jobs.drainWait = time.Minute
	done := make(chan error, 1)
	go func() { done <- s.jobs.drain(context.Background(), 1) }()
	time.Sleep(20 * time.Millisecond)
	go s.Close()
	select {
	case err := <-done:
		if !errors.Is(err, errShuttingDown) {
			t.Fatalf("drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not end on Close")
	}
}
