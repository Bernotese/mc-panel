package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func mustState(t *testing.T, s *Service, id int64) filesapi.StateResponse {
	t.Helper()
	st, err := s.State(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// plantJournal writes a raw journal entry (as an earlier run, or a broken disk, left it).
func plantJournal(t *testing.T, s *Service, name, body string) {
	t.Helper()
	dir := filepath.Join(s.backupsDir(), journalDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// unreadableDir makes dir unreadable for the test (and readable again at its end).
func unreadableDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
}

func TestStateSafeBusyRecovery(t *testing.T) {
	s, data := newService(t)
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe || st.Job != nil || st.Recovery != nil {
		t.Fatalf("fresh = %+v", st)
	}
	// A server without a data directory has nothing to protect either.
	if st := mustState(t, s, 7); st.State != filesapi.StateSafe {
		t.Fatalf("no data dir = %+v", st)
	}
	if _, err := s.State(context.Background(), 0); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v", err)
	}

	// restore and extract hold the server (also during their pre-check); backup, forget and
	// the pin/purge lock do not rewrite it.
	for _, k := range []filesapi.JobKind{filesapi.JobRestore, filesapi.JobExtract} {
		if err := s.jobs.acquire(1, k); err != nil {
			t.Fatal(err)
		}
		if st := mustState(t, s, 1); st.State != filesapi.StateBusy {
			t.Fatalf("%s: %+v", k, st)
		}
		s.jobs.release(1)
	}
	for _, k := range []filesapi.JobKind{filesapi.JobBackup, filesapi.JobForget, jobLock} {
		if err := s.jobs.acquire(1, k); err != nil {
			t.Fatal(err)
		}
		if st := mustState(t, s, 1); st.State != filesapi.StateSafe {
			t.Fatalf("%s: %+v", k, st)
		}
		s.jobs.release(1)
	}

	// The last job is reported with the state.
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "x.txt", body: "x"}))
	if _, err := s.Extract(context.Background(), filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, 1)
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe || st.Job == nil || st.Job.Kind != filesapi.JobExtract {
		t.Fatalf("after extract = %+v", st)
	}

	// Busy reports the job that holds the slot, not the last one (the extract above); a resolve
	// holds it without a job the panel shows.
	if err := s.jobs.acquire(1, filesapi.JobRestore); err != nil {
		t.Fatal(err)
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateBusy || st.Job == nil || st.Job.Kind != filesapi.JobRestore {
		t.Fatalf("restore pre-check after an extract = %+v", st)
	}
	s.jobs.release(1)
	if err := s.jobs.acquire(1, jobResolve); err != nil {
		t.Fatal(err)
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateBusy || st.Job != nil {
		t.Fatalf("resolve = %+v", st)
	}
	s.jobs.release(1)

	crashedRestore(t, s, data, phaseStuck, nil, map[string]string{"a.txt": "previous a"}, nil)
	st := mustState(t, s, 1)
	if st.State != filesapi.StateRecoveryRequired || st.Recovery == nil || st.Recovery.Phase != phaseStuck || st.Recovery.Since <= 0 {
		t.Fatalf("stuck = %+v %+v", st, st.Recovery)
	}
	// busy wins: the journal entry of a running swap is not a recovery
	if err := s.jobs.acquire(1, filesapi.JobRestore); err != nil {
		t.Fatal(err)
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateBusy || st.Recovery != nil {
		t.Fatalf("busy with journal = %+v", st)
	}
	s.jobs.release(1)
	// Another server is not affected.
	if st := mustState(t, s, 2); st.State != filesapi.StateSafe {
		t.Fatalf("server 2 = %+v", st)
	}
}

// TestStateUnreadableJournalEntry: an entry that cannot be read or parsed is never "safe": the
// server needs recovery, the entry survives the start, and CleanupTemp keeps the top-level temp
// folders of that server (it cannot know which one the entry meant).
func TestStateUnreadableJournalEntry(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      "not json",
		"invalid tmp":   `{"tmp":"../../etc","phase":"old_moving"}`,
		"unknown phase": `{"tmp":".mcpanel-tmp-x","phase":"later"}`,
		"too large":     strings.Repeat(" ", maxJournalBytes+1) + "{}",
	} {
		t.Run(name, func(t *testing.T) {
			s, data := newService(t)
			data2 := filepath.Join(filepath.Dir(data), "2")
			put(t, data, deadTmp+"/old/a.txt", "previous a")
			put(t, data, "w/"+deadTmp+"/part", "an upload that broke off")
			put(t, data2, deadTmp+"/old/a.txt", "leftover of server 2")
			plantJournal(t, s, "1", body)
			plantJournal(t, s, "5.new", "{}") // an unfinished write: removed at start

			st := mustState(t, s, 1)
			if st.State != filesapi.StateRecoveryRequired || st.Recovery == nil || st.Recovery.Phase != phaseUnreadable {
				t.Fatalf("state = %+v %+v", st, st.Recovery)
			}
			if n := s.RecoverRestores(); n != 0 {
				t.Fatalf("recovered %d", n)
			}
			s.CleanupTemp(context.Background())
			if _, err := os.Stat(filepath.Join(s.backupsDir(), journalDir, "1")); err != nil {
				t.Fatalf("the entry was removed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(s.backupsDir(), journalDir, "5.new")); !os.IsNotExist(err) {
				t.Fatalf("the unfinished write stays: %v", err)
			}
			if got, _ := get(t, data, deadTmp+"/old/a.txt"); got != "previous a" {
				t.Fatalf("the temp folder was removed: %q", got)
			}
			if _, ok := get(t, data, "w/"+deadTmp+"/part"); ok {
				t.Fatal("a nested leftover was kept")
			}
			noTemp(t, data2)
			if st := mustState(t, s, 1); st.State != filesapi.StateRecoveryRequired {
				t.Fatalf("after start = %+v", st)
			}
		})
	}
	t.Run("a folder instead of a file", func(t *testing.T) {
		s, _ := newService(t)
		if err := os.MkdirAll(filepath.Join(s.backupsDir(), journalDir, "1"), 0o700); err != nil {
			t.Fatal(err)
		}
		if st := mustState(t, s, 1); st.State != filesapi.StateRecoveryRequired {
			t.Fatalf("state = %+v", st)
		}
	})
	t.Run("an entry without read permission", func(t *testing.T) {
		s, _ := newService(t)
		crashedRestore(t, s, filepath.Join(s.cfg.InstancesDir, "instances", "1"), phaseOldMoving, nil, nil, nil)
		p := filepath.Join(s.backupsDir(), journalDir, "1")
		if os.Geteuid() == 0 {
			t.Skip("root reads every file")
		}
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatal(err)
		}
		s.RecoverRestores()
		if st := mustState(t, s, 1); st.State != filesapi.StateRecoveryRequired || st.Recovery.Phase != phaseUnreadable {
			t.Fatalf("state = %+v", st)
		}
	})
}

// TestStateUnreadableJournalDir: a journal that cannot be listed makes every answer an error
// (503 on the wire), and CleanupTemp keeps every top-level temp folder.
func TestStateUnreadableJournalDir(t *testing.T) {
	s, data := newService(t)
	put(t, data, deadTmp+"/old/a.txt", "previous a")
	put(t, data, "w/"+deadTmp+"/part", "partial")
	dir := filepath.Join(s.backupsDir(), journalDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	unreadableDir(t, dir)

	for _, id := range []int64{1, 2} {
		if _, err := s.State(context.Background(), id); !errors.Is(err, errJournalUnreadable) {
			t.Fatalf("State(%d): %v", id, err)
		}
	}
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	resp, err := http.Get(ts.URL + filesapi.PathState + "?id=1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || errCode(t, b) != "" {
		t.Fatalf("GET state = %d %s", resp.StatusCode, b)
	}
	// Fail closed for plain mutations and data jobs too.
	if _, err := s.Write(context.Background(), filesapi.WriteRequest{ID: 1, Path: "new.txt", Content: "x"}); !errors.Is(err, errJournalUnreadable) {
		t.Fatalf("Write: %v", err)
	}

	s.RecoverRestores()
	s.CleanupTemp(context.Background())
	if got, _ := get(t, data, deadTmp+"/old/a.txt"); got != "previous a" {
		t.Fatalf("the temp folder was removed: %q", got)
	}
	if _, ok := get(t, data, "w/"+deadTmp+"/part"); ok {
		t.Fatal("a nested leftover was kept")
	}

	t.Run("a file instead of the folder", func(t *testing.T) {
		s, _ := newService(t)
		if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(s.backupsDir(), journalDir), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.State(context.Background(), 1); !errors.Is(err, errJournalUnreadable) {
			t.Fatalf("State: %v", err)
		}
	})
}

// TestStateJournalListingIsCached: the listing of the journal is reused for at most a second;
// the service's own journal writes are seen at once.
func TestStateJournalListingIsCached(t *testing.T) {
	s, data := newService(t)
	fc := s.clock.(interface{ Advance(d time.Duration) })
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe {
		t.Fatalf("state = %+v", st)
	}
	plantJournal(t, s, "1", `{"tmp":"`+deadTmp+`","phase":"stuck"}`) // behind the service's back
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe {
		t.Fatalf("within a second = %+v", st)
	}
	fc.Advance(1100 * time.Millisecond)
	if st := mustState(t, s, 1); st.State != filesapi.StateRecoveryRequired {
		t.Fatalf("after a second = %+v", st)
	}
	if err := s.removeJournal(1); err != nil {
		t.Fatal(err)
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe {
		t.Fatalf("after removeJournal = %+v", st)
	}
	crashedRestore(t, s, data, phaseStuck, nil, nil, nil) // writeJournal
	if st := mustState(t, s, 1); st.State != filesapi.StateRecoveryRequired {
		t.Fatalf("after writeJournal = %+v", st)
	}
}

// TestStateSurvivesRestart: a stuck restore is still recovery_required for a new process, and
// blocks restores, extracts and plain mutations there.
func TestStateSurvivesRestart(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a"), reg("c.txt", "old c")))
	s.swapHook = func(step int) error {
		switch step {
		case 1:
			return errors.New("simulated rename failure")
		case -1:
			return errors.New("simulated undo failure")
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); !strings.HasPrefix(job.Error, errRestoreStuck.Error()) {
		t.Fatalf("job = %+v", job)
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateRecoveryRequired || st.Recovery.Phase != phaseStuck {
		t.Fatalf("state = %+v", st)
	}
	s.Close()

	s2 := New(s.cfg)
	t.Cleanup(s2.Close)
	s2.RecoverRestores()
	s2.CleanupTemp(context.Background())
	if st := mustState(t, s2, 1); st.State != filesapi.StateRecoveryRequired || st.Recovery.Phase != phaseStuck {
		t.Fatalf("after restart = %+v", st)
	}
	if _, err := s2.Write(context.Background(), filesapi.WriteRequest{ID: 1, Path: "new.txt", Content: "x"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("Write: %v", err)
	}
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "x.txt", body: "x"}))
	if _, err := s2.Extract(context.Background(), filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("Extract: %v", err)
	}
}

func TestResolveMovesFolders(t *testing.T) {
	s, data := newService(t)
	crashedRestore(t, s, data, phaseStuck,
		map[string]string{"a.txt": "half restored"},
		map[string]string{"a.txt": "previous a", "world/level.dat": "previous lvl"},
		map[string]string{"c.txt": "snapshot c"})
	put(t, data, tempPrefix+"other-0123/keep.txt", "not the journaled folder")
	makeReadOnly(t, data, deadTmp, deadTmp+"/old") // as the container could
	t.Cleanup(func() {                             // the read-only folder moved to backups/recovery
		_ = filepath.WalkDir(s.cfg.InstancesDir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755) //nolint:gosec // G703: test cleanup below t.TempDir()
			}
			return nil
		})
	})
	rec, err := s.ResolveRecovery(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("1-%d", testStart.Unix())
	if rec.Phase != phaseStuck || rec.Folder != want || rec.Since <= 0 {
		t.Fatalf("recovery = %+v", rec)
	}
	moved := filepath.Join(s.backupsDir(), "recovery", want)
	assertFiles(t, moved, map[string]string{
		strings.TrimPrefix(deadTmp, ".") + "/old/a.txt":           "previous a",
		strings.TrimPrefix(deadTmp, ".") + "/old/world/level.dat": "previous lvl",
		strings.TrimPrefix(deadTmp, ".") + "/new/c.txt":           "snapshot c",
	})
	for _, p := range []string{filepath.Join(s.backupsDir(), "recovery"), moved} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", p, fi.Mode(), err)
		}
	}
	// The visible data directory is untouched; only the journaled folder moved.
	assertFiles(t, data, map[string]string{"a.txt": "half restored", tempPrefix + "other-0123/keep.txt": "not the journaled folder"}, deadTmp)
	if journalExists(t, s) {
		t.Fatal("journal entry left")
	}
	if st := mustState(t, s, 1); st.State != filesapi.StateSafe {
		t.Fatalf("state after resolve = %+v", st)
	}
	// A second resolve of the same server in the same second gets its own folder.
	crashedRestore(t, s, data, phaseStuck, nil, map[string]string{"b.txt": "again"}, nil)
	rec, err = s.ResolveRecovery(context.Background(), 1)
	if err != nil || rec.Folder == want || !strings.HasPrefix(rec.Folder, want) {
		t.Fatalf("second resolve = %+v %v", rec, err)
	}

	t.Run("unreadable entry: every top-level temp folder moves", func(t *testing.T) {
		s, data := newService(t)
		put(t, data, deadTmp+"/old/a.txt", "previous a")
		put(t, data, tempPrefix+"more-0123/new/b.txt", "snapshot b")
		put(t, data, tempPrefix+"file-0123", "a temp file stays")
		put(t, data, "w/"+deadTmp+"/x.txt", "nested stays")
		plantJournal(t, s, "1", "not json")
		rec, err := s.ResolveRecovery(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Phase != phaseUnreadable {
			t.Fatalf("recovery = %+v", rec)
		}
		moved := filepath.Join(s.backupsDir(), "recovery", rec.Folder)
		assertFiles(t, moved, map[string]string{
			strings.TrimPrefix(deadTmp, ".") + "/old/a.txt":             "previous a",
			strings.TrimPrefix(tempPrefix, ".") + "more-0123/new/b.txt": "snapshot b",
		})
		assertFiles(t, data, map[string]string{tempPrefix + "file-0123": "a temp file stays", "w/" + deadTmp + "/x.txt": "nested stays"})
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
	})
	t.Run("the temp folder is gone: only the entry goes", func(t *testing.T) {
		s, _ := newService(t)
		plantJournal(t, s, "1", `{"tmp":"`+deadTmp+`","phase":"stuck"}`)
		rec, err := s.ResolveRecovery(context.Background(), 1)
		if err != nil || rec.Folder != "" || rec.Phase != phaseStuck {
			t.Fatalf("recovery = %+v %v", rec, err)
		}
		if journalExists(t, s) {
			t.Fatal("journal entry left")
		}
	})
	t.Run("over HTTP", func(t *testing.T) {
		s, data := newService(t)
		crashedRestore(t, s, data, phaseStuck, nil, map[string]string{"a.txt": "previous a"}, nil)
		ts := startHTTP(t, NewServer(s, "test", discardLog()))
		resp, err := http.Get(ts.URL + filesapi.PathState + "?id=1")
		if err != nil {
			t.Fatal(err)
		}
		var st filesapi.StateResponse
		err = json.NewDecoder(resp.Body).Decode(&st)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || st.State != filesapi.StateRecoveryRequired {
			t.Fatalf("GET state = %d %+v %v", resp.StatusCode, st, err)
		}
		code, b := postJSON(t, ts, filesapi.PathRecoveryResolve, filesapi.IDRequest{ID: 1})
		var rec filesapi.Recovery
		if code != http.StatusOK || json.Unmarshal(b, &rec) != nil || rec.Folder == "" {
			t.Fatalf("resolve = %d %s", code, b)
		}
		code, b = postJSON(t, ts, filesapi.PathRecoveryResolve, filesapi.IDRequest{ID: 1})
		if code != http.StatusNotFound || errCode(t, b) != filesapi.CodeNotFound {
			t.Fatalf("second resolve = %d %s", code, b)
		}
	})
}

func TestResolveRefusesWhenNotRecovery(t *testing.T) {
	s, data := newService(t)
	if _, err := s.ResolveRecovery(context.Background(), 1); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("safe: %v", err)
	}
	if _, err := s.ResolveRecovery(context.Background(), 0); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v", err)
	}
	crashedRestore(t, s, data, phaseStuck, nil, map[string]string{"a.txt": "previous a"}, nil)
	if err := s.jobs.acquire(1, filesapi.JobRestore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveRecovery(context.Background(), 1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	s.jobs.release(1)
	if !journalExists(t, s) {
		t.Fatal("a refused resolve removed the entry")
	}
	unreadableDir(t, filepath.Join(s.backupsDir(), journalDir))
	if _, err := s.ResolveRecovery(context.Background(), 1); !errors.Is(err, errJournalUnreadable) {
		t.Fatalf("unreadable journal: %v", err)
	}
}
