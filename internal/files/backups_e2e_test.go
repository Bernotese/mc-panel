package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// TestBackupsEndToEndWithClient drives the panel's client against the service over a real
// Unix socket with the fake restic (no MCPANEL_TEST_RESTIC needed).
func TestBackupsEndToEndWithClient(t *testing.T) {
	dir, err := os.MkdirTemp("", "mcb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s, data, _ := newBackupService(t)
	srv := NewServer(s, "e2e", discardLog())
	sock := filepath.Join(dir, "f.sock")
	l, err := unixsock.Listen(sock, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(uint32(os.Getuid())), discardLog())) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	c := filesapi.NewClient(sock)
	ctx := context.Background()

	st, err := c.BackupsStatus(ctx)
	if err != nil || !st.Available {
		t.Fatalf("status = %+v %v", st, err)
	}
	put(t, data, "a.txt", "one")
	if _, err := c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled}); err != nil {
		t.Fatal(err)
	}
	var job *filesapi.Job
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if job, err = c.Job(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if job != nil && job.State != filesapi.JobRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not finish")
		}
	}
	if job.Kind != filesapi.JobBackup || job.State != filesapi.JobDone {
		t.Fatalf("job = %+v", job)
	}
	snaps, err := c.Snapshots(ctx, 1)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %+v %v", snaps, err)
	}
	pinned, err := c.PinBackup(ctx, 1, snaps[0].ID, true)
	if err != nil || !pinned.Pinned {
		t.Fatalf("pin = %+v %v", pinned, err)
	}
	d, err := c.DownloadBackup(ctx, 1, pinned.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if !bytes.HasPrefix(b, []byte("PK")) {
		t.Fatalf("download is no zip: %q", b[:min(len(b), 16)])
	}
	if _, err := c.RestoreBackup(ctx, 1, pinned.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelJob(ctx, 1); err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, 1)
	if _, err := c.RestoreBackup(ctx, 1, "0000000000000000000000000000000000000000000000000000000000000000"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
	if err := c.PurgeBackups(ctx, 1); err != nil {
		t.Fatal(err)
	}
}
