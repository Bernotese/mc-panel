package files

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// barrier makes the commit hook wait until n callers arrived, then lets all of them go.
func barrier(n int) func(string) {
	var mu sync.Mutex
	arrived := 0
	all := make(chan struct{})
	return func(string) {
		mu.Lock()
		if arrived++; arrived == n {
			close(all)
		}
		mu.Unlock()
		<-all
	}
}

// #149: two saves with the same base both pass the early check and stage their temp file;
// exactly one of them commits, the other gets ErrModified and leaves nothing behind.
func TestConcurrentSavesSameBaseOneWins(t *testing.T) {
	for _, tc := range []struct {
		name string
		base func(mtime int64, version string) filesapi.WriteRequest
	}{
		{"mtime", func(m int64, _ string) filesapi.WriteRequest { return filesapi.WriteRequest{ExpectedMTime: &m} }},
		{"version", func(_ int64, v string) filesapi.WriteRequest { return filesapi.WriteRequest{ExpectedVersion: &v} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for round := range 20 {
				s, data := newService(t)
				put(t, data, "notes.txt", "base")
				rd, err := s.Read(ctx, 1, "notes.txt")
				if err != nil {
					t.Fatal(err)
				}
				s.commitHook = barrier(2)
				errs := make([]error, 2)
				var wg sync.WaitGroup
				for i := range 2 {
					wg.Go(func() {
						req := tc.base(rd.MTime, rd.Version)
						req.ID, req.Path, req.Content = 1, "notes.txt", "save "+string(rune('A'+i))
						_, errs[i] = s.Write(ctx, req)
					})
				}
				wg.Wait()
				won := -1
				for i, err := range errs {
					switch {
					case err == nil && won < 0:
						won = i
					case err == nil:
						t.Fatalf("round %d: both saves succeeded", round)
					case !errors.Is(err, filesapi.ErrModified):
						t.Fatalf("round %d: save %d: %v, want ErrModified", round, i, err)
					}
				}
				if won < 0 {
					t.Fatalf("round %d: no save succeeded: %v", round, errs)
				}
				if v, _ := get(t, data, "notes.txt"); v != "save "+string(rune('A'+won)) {
					t.Fatalf("round %d: content %q, winner %d", round, v, won)
				}
				noTemp(t, data)
			}
		})
	}
}

// #149: the base is checked again at commit time: a change between the early check and the
// rename (here by another save while this one waits in the hook) is a conflict.
func TestSaveRevalidatesAtCommit(t *testing.T) {
	s, data := newService(t)
	put(t, data, "notes.txt", "base")
	rd, err := s.Read(ctx, 1, "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	s.commitHook = func(string) {
		s.commitHook = nil // the inner save runs without the hook
		m := rd.MTime
		if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "other", ExpectedMTime: &m}); err != nil {
			t.Errorf("inner save: %v", err)
		}
	}
	m := rd.MTime
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "mine", ExpectedMTime: &m}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("outer save: %v, want ErrModified", err)
	}
	if v, _ := get(t, data, "notes.txt"); v != "other" {
		t.Fatalf("content %q", v)
	}
	noTemp(t, data)
}

// #149: a file created while a "new file" save stages is never replaced, and a file deleted
// while an update stages is not brought back.
func TestSaveCommitSeesCreateAndDelete(t *testing.T) {
	s, data := newService(t)
	s.commitHook = func(string) { put(t, data, "new.txt", "theirs") }
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "new.txt", Content: "mine"}); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("create: %v, want ErrExists", err)
	}
	if v, _ := get(t, data, "new.txt"); v != "theirs" {
		t.Fatalf("content %q", v)
	}
	rd, _ := s.Read(ctx, 1, "new.txt")
	s.commitHook = func(string) { _ = os.Remove(filepath.Join(data, "new.txt")) }
	m := rd.MTime
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "new.txt", Content: "mine", ExpectedMTime: &m}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("update of a deleted file: %v, want ErrModified", err)
	}
	if _, ok := get(t, data, "new.txt"); ok {
		t.Fatal("a deleted file came back")
	}
	noTemp(t, data)
}

// #149: a commit never leaves the file with the millisecond mtime it replaced, so a save based
// on the old mtime cannot pass even when both writes fall into the same millisecond (here: the
// old file has an mtime in the future).
func TestSaveMovesMTimeForward(t *testing.T) {
	s, data := newService(t)
	put(t, data, "notes.txt", "base")
	future := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	if err := os.Chtimes(filepath.Join(data, "notes.txt"), future, future); err != nil {
		t.Fatal(err)
	}
	m := future.UnixMilli()
	res, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "one", ExpectedMTime: &m})
	if err != nil {
		t.Fatal(err)
	}
	if res.MTime != m+1 {
		t.Fatalf("mtime %d, want %d", res.MTime, m+1)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "two", ExpectedMTime: &m}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("stale base: %v, want ErrModified", err)
	}
}

// #149: the version token changes with every commit, also within the same millisecond and
// with the same size, and Read, Write and List agree on it.
func TestVersionToken(t *testing.T) {
	s, data := newService(t)
	put(t, data, "notes.txt", "base")
	rd, err := s.Read(ctx, 1, "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if rd.Version == "" {
		t.Fatal("read has no version")
	}
	v := rd.Version
	res, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "BASE", ExpectedVersion: &v})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version == "" || res.Version == v {
		t.Fatalf("version after save %q (before %q)", res.Version, v)
	}
	if rd2, _ := s.Read(ctx, 1, "notes.txt"); rd2.Version != res.Version {
		t.Fatalf("read %q, write answered %q", rd2.Version, res.Version)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "x", ExpectedVersion: &v}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("stale version: %v, want ErrModified", err)
	}
	// Rewritten in place by someone else with the same size and mtime: the token still differs.
	fi, _ := os.Stat(filepath.Join(data, "notes.txt"))
	cur := res.Version
	time.Sleep(10 * time.Millisecond) // ctime granularity
	put(t, data, "notes.txt", "bAsE")
	if err := os.Chtimes(filepath.Join(data, "notes.txt"), fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if rd3, _ := s.Read(ctx, 1, "notes.txt"); rd3.Version == cur {
		t.Fatal("same version after an in-place rewrite")
	}
	gone := "nope"
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "missing.txt", Content: "x", ExpectedVersion: &gone}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("version of a missing file: %v, want ErrModified", err)
	}
}

// #149: a file created at the target after Rename's own check is never replaced (Linux:
// renameat2 RENAME_NOREPLACE, macOS: RENAME_EXCL; the fallback re-checks under the commit lock).
func TestRenameNeverReplacesConcurrentTarget(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "fallback"}[fallback], func(t *testing.T) {
			if fallback {
				old := noReplaceDisabled
				noReplaceDisabled = true
				t.Cleanup(func() { noReplaceDisabled = old })
			}
			for _, kind := range []string{"file", "dir", "symlink"} {
				s, data := newService(t)
				put(t, data, "src/a.txt", "source")
				s.renameHook = func() {
					switch kind {
					case "file":
						put(t, data, "dst", "theirs")
					case "dir":
						put(t, data, "dst/x", "theirs")
					case "symlink":
						if err := os.Symlink("nowhere", filepath.Join(data, "dst")); err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, from := range []string{"src/a.txt", "src"} {
					err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: from, To: "dst"})
					if !errors.Is(err, filesapi.ErrExists) {
						t.Fatalf("%s onto a new %s: %v, want ErrExists", from, kind, err)
					}
					if kind == "dir" {
						_ = os.RemoveAll(filepath.Join(data, "dst"))
					} else {
						_ = os.Remove(filepath.Join(data, "dst"))
					}
				}
				if v, _ := get(t, data, "src/a.txt"); v != "source" {
					t.Fatalf("source changed: %q", v)
				}
			}
		})
	}
}

// renameNoReplace itself refuses every kind of existing target and moves into free names.
func TestRenameNoReplacePrimitive(t *testing.T) {
	s, data := newService(t)
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	put(t, data, "a", "a")
	put(t, data, "b", "b")
	put(t, data, "d/x", "x")
	if err := s.renameNoReplace(root, "a", "b"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("over a file: %v", err)
	}
	if err := s.renameNoReplace(root, "a", "d"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("over a folder: %v", err)
	}
	if err := s.renameNoReplace(root, "a", "d/y"); err != nil {
		t.Fatalf("into a folder: %v", err)
	}
	if v, _ := get(t, data, "d/y"); v != "a" {
		t.Fatalf("moved %q", v)
	}
	if v, _ := get(t, data, "b"); v != "b" {
		t.Fatalf("target changed %q", v)
	}
}
