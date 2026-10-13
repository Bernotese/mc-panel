package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const deadTmp = tempPrefix + "dead0000-0123456789abcdef" // a temp folder of an earlier process

// crashedRestore lays out what a restore that died inside its swap leaves behind: the given
// entries at the top level, in <tmp>/old and in <tmp>/new, and the journal entry (phase "" =
// none).
func crashedRestore(t *testing.T, s *Service, data, phase string, top, old, nw map[string]string) {
	t.Helper()
	if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	for dir, files := range map[string]map[string]string{"": top, deadTmp + "/old": old, deadTmp + "/new": nw} {
		if err := os.MkdirAll(filepath.Join(data, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		for n, c := range files {
			put(t, filepath.Join(data, dir), n, c)
		}
	}
	if phase != "" {
		if err := s.writeJournal(1, restoreJournal{Tmp: deadTmp, Phase: phase}); err != nil {
			t.Fatal(err)
		}
	}
}

func journalExists(t *testing.T, s *Service) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(s.backupsDir(), journalDir, "1"))
	return err == nil
}

func assertFiles(t *testing.T, data string, want map[string]string, absent ...string) {
	t.Helper()
	for n, c := range want {
		if got, ok := get(t, data, n); !ok || got != c {
			t.Errorf("%s = %q (exists %v), want %q", n, got, ok, c)
		}
	}
	for _, n := range absent {
		if _, ok := get(t, data, n); ok {
			t.Errorf("%s exists", n)
		}
	}
}

// TestRecoverRestorePhases simulates a crash in every phase of the swap.
func TestRecoverRestorePhases(t *testing.T) {
	t.Run("old_moving: the previous state goes back", func(t *testing.T) {
		s, data := newService(t)
		crashedRestore(t, s, data, phaseOldMoving,
			map[string]string{"b.txt": "previous b"},                                    // not moved yet
			map[string]string{"a.txt": "previous a", "world/level.dat": "previous lvl"}, // moved
			map[string]string{"a.txt": "snapshot a", "c.txt": "snapshot c"})
		if n := s.RecoverRestores(); n != 1 {
			t.Fatalf("recovered %d", n)
		}
		assertFiles(t, data, map[string]string{"a.txt": "previous a", "b.txt": "previous b", "world/level.dat": "previous lvl"}, "c.txt")
		noTemp(t, data)
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
	})
	t.Run("new_moving: the restore is completed", func(t *testing.T) {
		s, data := newService(t)
		crashedRestore(t, s, data, phaseNewMoving,
			map[string]string{"a.txt": "snapshot a"}, // already moved up
			map[string]string{"a.txt": "previous a", "b.txt": "previous b"},
			map[string]string{"c.txt": "snapshot c", "world/level.dat": "snapshot lvl"})
		s.RecoverRestores()
		assertFiles(t, data, map[string]string{"a.txt": "snapshot a", "c.txt": "snapshot c", "world/level.dat": "snapshot lvl"}, "b.txt")
		noTemp(t, data)
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
	})
	t.Run("cleanup: only the temp folder goes", func(t *testing.T) {
		s, data := newService(t)
		crashedRestore(t, s, data, phaseCleanup, map[string]string{"a.txt": "snapshot a"}, map[string]string{"a.txt": "previous a"}, nil)
		s.RecoverRestores()
		assertFiles(t, data, map[string]string{"a.txt": "snapshot a"})
		noTemp(t, data)
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
	})
	t.Run("the temp folder is gone already", func(t *testing.T) {
		s, data := newService(t)
		if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.writeJournal(1, restoreJournal{Tmp: deadTmp, Phase: phaseCleanup}); err != nil {
			t.Fatal(err)
		}
		s.RecoverRestores()
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
		noTemp(t, data)
	})
	t.Run("broken entries are dropped", func(t *testing.T) {
		s, _ := newService(t)
		dir := filepath.Join(s.backupsDir(), journalDir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{
			"1":     `{"tmp":"../../etc","phase":"old_moving"}`,
			"2":     `not json`,
			"3":     `{"tmp":".mcpanel-tmp-x","phase":"later"}`,
			"4.new": `{}`,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if n := s.RecoverRestores(); n != 0 {
			t.Fatalf("recovered %d", n)
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Fatalf("left in the journal: %v", left)
		}
	})
}

// TestFakeRestoreLeftoverIsRemoved: a container cannot fake a restore leftover: a temp folder
// without a journal entry is removed by CleanupTemp, its old/ is never moved up.
func TestFakeRestoreLeftoverIsRemoved(t *testing.T) {
	s, data := newService(t)
	crashedRestore(t, s, data, "", nil, map[string]string{"planted.txt": "from the container"}, nil)
	s.RecoverRestores()
	s.CleanupTemp(context.Background())
	noTemp(t, data)
	assertFiles(t, data, nil, "planted.txt")
}

// TestStuckRestoreIsKept: a swap whose undo failed keeps its temp folder through recovery and
// CleanupTemp, and a new restore of the server is refused until a person removes the entry.
func TestStuckRestoreIsKept(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	put(t, data, "b.txt", "current b")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a"), reg("c.txt", "old c")))
	s.swapHook = func(step int) error {
		switch step {
		case 3:
			return errors.New("simulated rename failure")
		case -1 - 1:
			return errors.New("simulated undo failure")
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.Error != errRestoreStuck.Error() {
		t.Fatalf("job = %+v", job)
	}
	j, ok := s.readJournals()[1]
	if !ok || j.Phase != phaseStuck {
		t.Fatalf("journal = %+v %v", j, ok)
	}
	s.swapHook = nil
	s.RecoverRestores()
	s.CleanupTemp(context.Background())
	if got, _ := get(t, data, j.Tmp+"/old/b.txt"); got != "current b" {
		t.Fatalf("the previous state was removed: %q", got)
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("restore while stuck: %v", err)
	}
}

// TestRestoreJournalFollowsTheSwap: the journal says old_moving while the current entries move
// away, new_moving while the restored ones move up, and is gone after the restore.
func TestRestoreJournalFollowsTheSwap(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	put(t, data, "b.txt", "current b")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a"), reg("c.txt", "old c")))
	var phases []string
	s.swapHook = func(step int) error {
		phases = append(phases, fmt.Sprintf("%d:%s", step, s.readJournals()[1].Phase))
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	want := "0:old_moving,1:old_moving,2:new_moving,3:new_moving"
	if got := strings.Join(phases, ","); got != want {
		t.Fatalf("phases = %s, want %s", got, want)
	}
	if journalExists(t, s) {
		t.Fatal("journal entry left after the restore")
	}
	// A failed swap that is undone leaves no journal entry either.
	s.swapHook = func(step int) error {
		if step == 3 {
			return errors.New("simulated rename failure")
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed || job.Error == errRestoreStuck.Error() {
		t.Fatalf("job = %+v", job)
	}
	if journalExists(t, s) {
		t.Fatal("journal entry left after an undone swap")
	}
	noTemp(t, data)
}
