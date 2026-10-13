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
		s.CleanupTemp(context.Background()) // the temp folder is an ordinary leftover now
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
		s.CleanupTemp(context.Background())
		noTemp(t, data)
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
	})
	t.Run("stuck stays", func(t *testing.T) {
		s, data := newService(t)
		crashedRestore(t, s, data, phaseStuck, map[string]string{"a.txt": "x"}, map[string]string{"a.txt": "previous a"}, nil)
		if n := s.RecoverRestores(); n != 0 {
			t.Fatalf("recovered %d", n)
		}
		s.CleanupTemp(context.Background())
		if !journalExists(t, s) {
			t.Fatal("a stuck entry was removed")
		}
		if got, _ := get(t, data, deadTmp+"/old/a.txt"); got != "previous a" {
			t.Fatalf("old state = %q", got)
		}
	})
	t.Run("the temp folder is gone already", func(t *testing.T) {
		s, data := newService(t)
		if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.writeJournal(1, restoreJournal{Tmp: deadTmp, Phase: phaseNewMoving}); err != nil {
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
	if job := waitJob(t, s, 1); !strings.HasPrefix(job.Error, errRestoreStuck.Error()) || !strings.Contains(job.Error, "stuck") {
		t.Fatalf("job = %+v", job)
	}
	j, ok := s.readJournals(false)[1]
	if !ok || j.Phase != phaseStuck {
		t.Fatalf("journal = %+v %v", j, ok)
	}
	s.swapHook = nil
	s.RecoverRestores()
	s.CleanupTemp(context.Background())
	if got, _ := get(t, data, j.Tmp+"/old/b.txt"); got != "current b" {
		t.Fatalf("the previous state was removed: %q", got)
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, filesapi.ErrBusy) ||
		!strings.Contains(err.Error(), "a previous restore of this server could not be completed") {
		t.Fatalf("restore while stuck: %v", err)
	}
	// Purging the server's backups removes the stuck entry as well.
	if err := s.PurgeBackups(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if journalExists(t, s) {
		t.Fatal("PurgeBackups left the journal entry")
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
		phases = append(phases, fmt.Sprintf("%d:%s", step, s.readJournals(false)[1].Phase))
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
	if job := waitJob(t, s, 1); job.State != jobFailed || strings.HasPrefix(job.Error, errRestoreStuck.Error()) {
		t.Fatalf("job = %+v", job)
	}
	if journalExists(t, s) {
		t.Fatal("journal entry left after an undone swap")
	}
	noTemp(t, data)
}

// makeReadOnly chmods the given folders below data to 0555 (as a container could) and makes the
// whole tree writable again when the test ends, so t.TempDir can remove it.
func makeReadOnly(t *testing.T, data string, dirs ...string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(filepath.Dir(data), func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755) //nolint:gosec // G703: test cleanup below t.TempDir()
			}
			return nil
		})
	})
	// deepest first, so the parents are still writable while their children are changed
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(filepath.Join(data, dirs[i]), 0o555); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRestoreWithReadOnlyFolders: folders the container made read-only (0555, at the top and
// deep in the tree) do not stop the swap or the removal of the old state.
func TestRestoreWithReadOnlyFolders(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "world/region/deep/r.mca", "current")
	put(t, data, "a.txt", "current a")
	sid := snapshotOf(t, s, f, makeTar(t, dir("world/"), reg("world/level.dat", "old lvl"), reg("a.txt", "old a")))
	makeReadOnly(t, data, ".", "world", "world/region", "world/region/deep")
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	assertFiles(t, data, map[string]string{"a.txt": "old a", "world/level.dat": "old lvl"}, "world/region")
	noTemp(t, data)
}

func TestCleanupRemovesReadOnlyLeftovers(t *testing.T) {
	s, data := newService(t)
	put(t, data, "w/"+deadTmp+"/x/y/z.txt", "partial")
	put(t, data, deadTmp+"/old/world/level.dat", "planted")
	makeReadOnly(t, data, "w", "w/"+deadTmp, "w/"+deadTmp+"/x", "w/"+deadTmp+"/x/y", deadTmp+"/old", deadTmp+"/old/world")
	s.CleanupTemp(context.Background())
	noTemp(t, data)
}

func TestRecoverWithReadOnlyFolders(t *testing.T) {
	s, data := newService(t)
	crashedRestore(t, s, data, phaseOldMoving, nil, map[string]string{"world/region/r.mca": "previous"}, nil)
	makeReadOnly(t, data, ".", deadTmp, deadTmp+"/old", deadTmp+"/old/world", deadTmp+"/old/world/region")
	if n := s.RecoverRestores(); n != 1 {
		t.Fatalf("recovered %d", n)
	}
	assertFiles(t, data, map[string]string{"world/region/r.mca": "previous"})
	s.CleanupTemp(context.Background())
	noTemp(t, data)
}

// swapSetup prepares a data directory with current entries a.txt, b.txt and a temp folder whose
// new/ holds a.txt and c.txt.
func swapSetup(t *testing.T) (*os.Root, string, string) {
	t.Helper()
	s, data := newService(t)
	put(t, data, "a.txt", "current a")
	put(t, data, "b.txt", "current b")
	put(t, data, deadTmp+"/new/a.txt", "old a")
	put(t, data, deadTmp+"/new/c.txt", "old c")
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, data, deadTmp
}

// TestSwapJournalConsistency: whatever the journal says after a failure is what the next start
// does, so it must match the data directory.
func TestSwapJournalConsistency(t *testing.T) {
	failAt := func(step int) func(int) error {
		return func(i int) error {
			if i == step {
				return errors.New("simulated rename failure")
			}
			return nil
		}
	}
	t.Run("new_moving cannot be recorded: undone", func(t *testing.T) {
		root, data, tmp := swapSetup(t)
		var phases []string
		err := swapTop(root, tmp, nil, func(p string) error {
			phases = append(phases, p)
			if p == phaseNewMoving {
				return errors.New("disk full")
			}
			return nil
		})
		if err == nil || errors.Is(err, errRestoreStuck) {
			t.Fatalf("err = %v", err)
		}
		// new_moving may have reached the disk: stuck before the undo, old_moving after it
		if strings.Join(phases, ",") != "new_moving,stuck,old_moving" {
			t.Fatalf("phases = %v", phases)
		}
		assertFiles(t, data, map[string]string{"a.txt": "current a", "b.txt": "current b"}, "c.txt")
	})
	t.Run("stuck cannot be recorded: no undo, the journal says new_moving", func(t *testing.T) {
		root, data, tmp := swapSetup(t)
		journal := phaseOldMoving
		err := swapTop(root, tmp, failAt(3), func(p string) error {
			if p == phaseStuck {
				return errors.New("disk full")
			}
			journal = p
			return nil
		})
		var se *restoreStuckError
		if !errors.As(err, &se) || se.phase != phaseNewMoving || journal != phaseNewMoving {
			t.Fatalf("err = %v, journal %s", err, journal)
		}
		// Nothing was undone: the next start moves the rest of new/ up and completes the restore.
		assertFiles(t, data, map[string]string{"a.txt": "old a", tmp + "/old/a.txt": "current a", tmp + "/old/b.txt": "current b", tmp + "/new/c.txt": "old c"})
	})
}
