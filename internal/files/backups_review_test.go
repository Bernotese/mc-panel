package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Security review of PR #55: an incomplete stream never counts as a snapshot, bounded memory
// for names, unreadable entries, a swap that cannot be undone, restic start errors.

// lowerNameBudget sets maxBackupNameBytes for one test.
func lowerNameBudget(t *testing.T, n int64) {
	t.Helper()
	old := maxBackupNameBytes
	maxBackupNameBytes = n
	t.Cleanup(func() { maxBackupNameBytes = old })
}

func TestBackupTarEndEntryCounts(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "aaa")
	put(t, data, "w/b.txt", "bb")
	b, _, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(b))
	var last *tar.Header
	var body []byte
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		last = h
		body, _ = io.ReadAll(tr)
	}
	if last == nil || last.Name != backupEndName || string(body) != `{"entries":3,"bytes":5}` {
		t.Fatalf("last entry = %+v %q", last, body)
	}
}

// TestExtractBackupTarNeedsEnd: without a matching end entry the restore stream is refused as
// incomplete, also when the counts lie or something follows the end.
func TestExtractBackupTarNeedsEnd(t *testing.T) {
	lying := func(t *testing.T) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		_ = tw.WriteHeader(&tar.Header{Name: "a", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644, Format: tar.FormatPAX})
		_, _ = tw.Write([]byte("x"))
		if err := writeBackupEnd(tw, backupEnd{Entries: 2, Bytes: 1}); err != nil {
			t.Fatal(err)
		}
		_ = tw.Close()
		return buf.Bytes()
	}
	trailing := func(t *testing.T) []byte {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := writeBackupEnd(tw, backupEnd{}); err != nil {
			t.Fatal(err)
		}
		_ = tw.WriteHeader(&tar.Header{Name: "late", Typeflag: tar.TypeReg, Mode: 0o644, Format: tar.FormatPAX})
		_ = tw.Close()
		return buf.Bytes()
	}
	for _, c := range []struct {
		name string
		tar  func(t *testing.T) []byte
		msg  string
	}{
		{"no end", func(t *testing.T) []byte { return makeTarNoEnd(t, reg("a", "x"), dir("w/")) }, "incomplete"},
		{"empty stream", func(*testing.T) []byte { return nil }, "incomplete"},
		{"cut inside the end", func(t *testing.T) []byte { b := makeTar(t, reg("a", "x")); return b[:len(b)-1024-512+10] }, "incomplete"},
		{"lying counts", lying, "incomplete"},
		{"entries after the end", trailing, "after its end"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, data := newService(t)
			_, err := extractInto(t, s, c.tar(t))
			if !errors.Is(err, filesapi.ErrInvalid) || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err = %v, want invalid (%s)", err, c.msg)
			}
			for _, n := range names(t, data) {
				if n != "out" {
					t.Fatalf("written outside the target: %s", n)
				}
			}
		})
	}
	// With its end entry an empty snapshot is fine.
	s, _ := newService(t)
	if _, err := extractInto(t, s, makeTar(t)); err != nil {
		t.Fatalf("empty snapshot: %v", err)
	}
}

func TestRestoreIncompleteSnapshotLeavesDataAlone(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "current")
	sid := snapshotOf(t, s, f, makeTarNoEnd(t, dir("world/"), reg("world/level.dat", "old")))
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Code != filesapi.CodeInvalid || !strings.Contains(job.Error, "the snapshot is incomplete") {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "world/level.dat"); got != "current" {
		t.Fatalf("data changed: %q", got)
	}
	noTemp(t, data)
	for _, n := range names(t, data) {
		if strings.Contains(n, "mcpanel-end") {
			t.Fatalf("end entry in the data directory: %s", n)
		}
	}
}

func TestRestoreNeverWritesTheEndEntry(t *testing.T) {
	s, data, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "a")))
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job = %+v", job)
	}
	if _, ok := get(t, data, backupEndName); ok {
		t.Fatal("the end entry was written into the data directory")
	}
}

// TestTarToZipNeedsEnd: a cut stream fails before the zip is finished, so what was sent is no
// valid zip; the end entry never appears in a zip.
func TestTarToZipNeedsEnd(t *testing.T) {
	var out bytes.Buffer
	err := tarToZip(context.Background(), bytes.NewReader(makeTarNoEnd(t, reg("a.txt", "a"))), &out, testLimits(), func() {})
	if !errors.Is(err, filesapi.ErrInvalid) || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err = %v", err)
	}
	if _, zerr := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len())); zerr == nil {
		t.Fatal("a cut snapshot became a valid zip")
	}
	out.Reset()
	if err := tarToZip(context.Background(), bytes.NewReader(makeTar(t, reg("a.txt", "a"))), &out, testLimits(), func() {}); err != nil {
		t.Fatal(err)
	}
	for name := range zipNames(t, out.Bytes()) {
		if strings.Contains(name, "mcpanel-end") {
			t.Fatalf("end entry in the zip: %s", name)
		}
	}
}

func TestDownloadBackupIncompleteFails(t *testing.T) {
	s, _, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTarNoEnd(t, reg("a.txt", "hello")))
	var out bytes.Buffer
	err := s.DownloadBackup(context.Background(), 1, sid, func() {}, &out)
	if !errors.Is(err, filesapi.ErrInvalid) || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("err = %v", err)
	}
	if _, zerr := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len())); zerr == nil {
		t.Fatal("a cut snapshot downloaded as a valid zip")
	}
}

// TestTarToZipManySkippedFolders: thousands of folders that cannot go into a zip, each with
// content, are left out in linear time.
func TestTarToZipManySkippedFolders(t *testing.T) {
	lim := testLimits()
	lim.MaxBackupEntries = 100_000
	var es []tarEntry
	for i := range 5000 {
		d := fmt.Sprintf("bad:%d", i)
		es = append(es, dir(d+"/"), reg(d+"/x", "x"))
	}
	es = append(es, reg("ok.txt", "ok"))
	var out bytes.Buffer
	start := time.Now()
	if err := tarToZip(context.Background(), bytes.NewReader(makeTar(t, es...)), &out, lim, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := zipNames(t, out.Bytes()); len(got) != 1 || got["ok.txt"] != "ok" {
		t.Fatalf("zip = %d entries", len(got))
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestTarToZipBudgets(t *testing.T) {
	lim := testLimits()
	lim.MaxBackupEntries = 3
	var es []tarEntry
	for i := range 4 {
		es = append(es, reg(fmt.Sprintf("bad:%d", i), "")) // left out, still counted
	}
	if err := tarToZip(context.Background(), bytes.NewReader(makeTar(t, es...)), io.Discard, lim, func() {}); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("entries: %v", err)
	}
	lowerNameBudget(t, 10)
	if err := tarToZip(context.Background(), bytes.NewReader(makeTar(t, reg("abcdefghijk", ""))), io.Discard, testLimits(), func() {}); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("names: %v", err)
	}
}

func TestNameBudgetBackupAndRestore(t *testing.T) {
	lowerNameBudget(t, 20)
	s, data := newService(t)
	put(t, data, "0123456789/0123456789", "x") // 10 + 21 name bytes
	if _, _, err := backupTar(t, s, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("backup: %v", err)
	}
	s2, _ := newService(t)
	if _, err := extractInto(t, s2, makeTar(t, reg("0123456789", ""), reg("abcdefghijk", ""))); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("restore: %v", err)
	}
}

// TestBackupSkippedNamesCount: names that are left out (symlinks) count against
// max_backup_entries, so a folder of millions of symlinks ends the walk early.
func TestBackupSkippedNamesCount(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxBackupEntries = 10 })
	for i := range 11 {
		if err := os.Symlink("nowhere", filepath.Join(data, fmt.Sprintf("l%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := backupTar(t, s, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("11 symlinks, limit 10: %v", err)
	}
}

// TestDuplicatesWithoutSeenMap: a file twice or a file in place of a folder is refused by the
// file system (O_EXCL); a folder twice is harmless.
func TestDuplicatesWithoutSeenMap(t *testing.T) {
	s, data := newService(t)
	if _, err := extractInto(t, s, makeTar(t, dir("w/"), dir("w/"), reg("w/a", "a"))); err != nil {
		t.Fatalf("folder twice: %v", err)
	}
	if got, _ := get(t, data, "out/w/a"); got != "a" {
		t.Fatalf("w/a = %q", got)
	}
	for _, es := range [][]tarEntry{
		{reg("a", "1"), reg("a", "2")},
		{reg("a", "1"), dir("a/")},
		{dir("a/"), reg("a", "1")},
	} {
		s, _ := newService(t)
		if _, err := extractInto(t, s, makeTar(t, es...)); !errors.Is(err, filesapi.ErrInvalid) {
			t.Fatalf("%v: %v", es, err)
		}
	}
}

func TestSummaryWriterKeepsOnlyTheSummary(t *testing.T) {
	w := &summaryWriter{}
	status := []byte(`{"message_type":"status","percent_done":0.5,"total_files":1,"bytes_done":123456789}` + "\n")
	for range 200_000 { // more than two days of status lines at RESTIC_PROGRESS_FPS=1
		_, _ = w.Write(status[:20])
		_, _ = w.Write(status[20:])
	}
	_, _ = fmt.Fprintf(w, `{"message_type":"summary","snapshot_id":%q}`, strings.Repeat("a", 64)) // no final newline
	sid, err := backupSnapshotID(w.Summary())
	if err != nil || sid != strings.Repeat("a", 64) {
		t.Fatalf("snapshot = %q %v", sid, err)
	}
	if cap(w.line) > 4<<10 {
		t.Fatalf("line buffer grew to %d bytes", cap(w.line))
	}
	long := &summaryWriter{}
	_, _ = long.Write(bytes.Repeat([]byte("x"), maxResticLine+10))
	_, _ = long.Write([]byte("\n"))
	if long.Summary() != nil || cap(long.line) > maxResticLine+10 {
		t.Fatal("an overlong line was kept")
	}
	if _, err := backupSnapshotID(nil); err == nil {
		t.Fatal("no summary accepted")
	}
}

// TestBackupCanceledAtTheEndLeavesNoSnapshot: a cancel after the last entry still kills restic
// before its stdin ends.
func TestBackupCanceledAtTheEndLeavesNoSnapshot(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "a")
	put(t, data, "z.txt", "z")
	s.backupHook = func(p string) {
		if p == "z.txt" {
			_ = s.CancelJob(1)
		}
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Error != "the job was canceled" {
		t.Fatalf("job = %+v", job)
	}
	if n := len(f.snaps(s.repoDir(1))); n != 0 {
		t.Fatalf("%d snapshots after a cancel", n)
	}
	for _, eof := range f.sawEOF {
		if eof {
			t.Fatal("restic saw the end of the stream")
		}
	}
}

// failingStartRunner answers "restic version" through the fake, then cannot start restic any
// more (the binary was removed after the start check).
type failingStartRunner struct{ f *fakeRestic }

func (r failingStartRunner) Start(ctx context.Context, c resticCmd) (resticProc, error) {
	if w, _ := command(c.Args); w == "version" {
		return r.f.Start(ctx, c)
	}
	return nil, errors.New(`fork/exec /usr/local/lib/restic-0.19/restic: permission denied`)
}

func TestResticStartErrorHidesThePath(t *testing.T) {
	ctx := context.Background()
	s, data := newService(t, func(c *Config) { c.runner = failingStartRunner{f: newFakeRestic()} })
	if err := os.Mkdir(s.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if st := s.InitBackups(ctx); !st.Available {
		t.Fatalf("status = %+v", st)
	}
	put(t, data, "a", "a")
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Code != filesapi.CodeUnavailable || job.Error != "unavailable: restic cannot be started" || strings.Contains(job.Error, "/usr") {
		t.Fatalf("job = %+v", job)
	}
	// Synchronous calls answer 503 unavailable.
	if err := os.MkdirAll(s.repoDir(1), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.repoDir(1), "config"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Snapshots(ctx, 1); !errors.Is(err, filesapi.ErrBackupsUnavailable) {
		t.Fatalf("snapshots: %v", err)
	}
}

func TestPublicMessageHidesTheResticBinary(t *testing.T) {
	s, _ := newService(t, func(c *Config) { c.ResticBinary = "/opt/tools/restic" })
	if got := s.publicMessage(`exec: "/opt/tools/restic": permission denied`); strings.Contains(got, "/opt") {
		t.Fatalf("message = %q", got)
	}
}

// TestSwapUndoFailureKeepsTheTempFolder: when the undo of a failed swap fails as well, the
// previous state stays in the hidden temp folder and the job says so.
func TestSwapUndoFailureKeepsTheTempFolder(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	put(t, data, "b.txt", "current b")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a"), reg("c.txt", "old c")))
	s.swapHook = func(step int) error {
		switch step {
		case 3: // both current entries moved away, one new one moved up
			return errors.New("simulated rename failure")
		case -1 - 1: // undoing step 1 (b.txt back to the top) fails too
			return errors.New("simulated undo failure")
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.HasPrefix(job.Error, errRestoreStuck.Error()) {
		t.Fatalf("job = %+v", job)
	}
	var kept string
	for _, n := range names(t, data) {
		if strings.HasPrefix(n, tempPrefix) {
			kept = n
		}
	}
	if kept == "" {
		t.Fatal("the temp folder with the previous state was removed")
	}
	if got, _ := get(t, data, kept+"/old/b.txt"); got != "current b" {
		t.Fatalf("old/b.txt = %q", got)
	}
}

// TestNameBudgetBoundary: a snapshot that just fits the name budget when written also fits it
// when restored and downloaded (both sides count the same bytes).
func TestNameBudgetBoundary(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/region/r.0.0.mca", "r")
	put(t, data, "world/level.dat", "l")
	put(t, data, "a.txt", "a")
	// "a.txt" + "world" + "world/level.dat" + "world/region" + "world/region/r.0.0.mca"
	exact := int64(len("a.txt") + len("world") + len("world/level.dat") + len("world/region") + len("world/region/r.0.0.mca"))
	lowerNameBudget(t, exact-1)
	if _, _, err := backupTar(t, s, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("one byte below: %v", err)
	}
	maxBackupNameBytes = exact
	b, _, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatalf("at the budget: %v", err)
	}
	s2, _ := newService(t)
	if _, err := extractInto(t, s2, b); err != nil {
		t.Fatalf("restore at the budget: %v", err)
	}
	if err := tarToZip(context.Background(), bytes.NewReader(b), io.Discard, testLimits(), func() {}); err != nil {
		t.Fatalf("download at the budget: %v", err)
	}
}
