//go:build linux

package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// TestBackupSkipsUnreadableEntries: a file or folder the service cannot read (chmod 000 from
// inside the container) is left out and counted; the backup still succeeds.
func TestBackupSkipsUnreadableEntries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	s, data := newService(t)
	put(t, data, "a.txt", "a")
	put(t, data, "locked.txt", "secret")
	put(t, data, "closed/inner.txt", "x")
	for _, p := range []string{"locked.txt", "closed"} {
		if err := os.Chmod(filepath.Join(data, p), 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(data, "closed"), 0o755) })
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tarNames(readTar(t, b)), ","); got != "a.txt" || skipped != 2 {
		t.Fatalf("entries %s, skipped %d", got, skipped)
	}

	// Through the job: done, with skipped counted.
	s2, data2, _ := newBackupService(t)
	put(t, data2, "a.txt", "a")
	put(t, data2, "locked.txt", "secret")
	if err := os.Chmod(filepath.Join(data2, "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s2, 1); job.State != jobDone || job.Skipped != 1 {
		t.Fatalf("job = %+v", job)
	}
}

// TestExecRunnerSetsParentDeathSignal: restic dies with the service (spec 4 §4.5).
func TestExecRunnerSetsParentDeathSignal(t *testing.T) {
	bin, _ := fakeBinary(t)
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{Args: []string{"version"}})
	if err != nil {
		t.Fatal(err)
	}
	ep := p.(*execProc)
	if ep.cmd.SysProcAttr == nil || ep.cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("SysProcAttr = %+v", ep.cmd.SysProcAttr)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ep.done:
	default:
		t.Fatal("the forking thread is still held after Wait")
	}
}
