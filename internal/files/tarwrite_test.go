package files

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type tarItem struct {
	name string
	typ  byte
	body string
	mode int64
}

// readTar reads a backup stream; it checks that the stream ends with the end entry and that
// its counts match, and returns the entries before it.
func readTar(t *testing.T, b []byte) []tarItem {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	var out []tarItem
	var counted backupEnd
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			t.Fatalf("the stream has no end entry: %v", tarNames(out))
		}
		if err == nil && h.Name == backupEndName {
			if err := checkBackupEnd(tr, h, counted); err != nil {
				t.Fatalf("end entry: %v", err)
			}
			return out
		}
		if err == nil {
			counted.Entries++
			counted.Bytes += h.Size
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
			t.Fatalf("%s carries an owner: %+v", h.Name, h)
		}
		out = append(out, tarItem{name: h.Name, typ: h.Typeflag, body: string(body), mode: h.Mode})
	}
}

func tarNames(items []tarItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.name
	}
	return out
}

// backupTar runs writeBackupTar on the data directory of server 1.
func backupTar(t *testing.T, s *Service, hook func(string)) ([]byte, int, error) {
	t.Helper()
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var buf bytes.Buffer
	skipped := 0
	err = writeBackupTar(context.Background(), root, &buf, s.cfg.Limits, hook, func(int64) {}, func(string, error) { skipped++ })
	return buf.Bytes(), skipped, err
}

func TestBackupTarContentOrderAndRedaction(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/region/r.0.0.mca", "region")
	put(t, data, "b.txt", "b")
	put(t, data, "a.txt", "a")
	put(t, data, "server.properties", "motd=hi\nrcon.password=s3cret\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")
	put(t, data, "world/.mcpanel-tmp-x/leftover", "x")
	if err := os.Chmod(filepath.Join(data, "a.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := readTar(t, b)
	want := []string{"a.txt", "b.txt", "server.properties", "world/", "world/region/", "world/region/r.0.0.mca"}
	if got := tarNames(items); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v (sorted, hidden left out)", got, want)
	}
	if skipped != 2 { // .rcon-cli.env and .mcpanel-tmp-x
		t.Fatalf("skipped = %d, want 2", skipped)
	}
	for _, it := range items {
		switch it.name {
		case "server.properties":
			if strings.Contains(it.body, "s3cret") || !strings.Contains(it.body, "rcon.password=********") {
				t.Fatalf("server.properties not redacted: %q", it.body)
			}
		case "a.txt":
			if it.mode&0o111 == 0 || it.typ != tar.TypeReg {
				t.Fatalf("a.txt = %+v", it)
			}
		case "world/":
			if it.typ != tar.TypeDir {
				t.Fatalf("world/ = %+v", it)
			}
		}
	}
}

// TestBackupTarSwappedFolderStaysInside (review focus #2): a running server swaps a folder for a
// symlink to the neighbour instance while the walk is under way; nothing of the neighbour may end
// up in the stream.
func TestBackupTarSwappedFolderStaysInside(t *testing.T) {
	s, data := newService(t)
	other := filepath.Join(s.cfg.InstancesDir, "instances", "2")
	put(t, other, "secret.txt", "other server")
	put(t, data, "world/level.dat", "mine")
	put(t, data, "zz/after.txt", "z")
	swapped := false
	hook := func(p string) {
		if p == "world/level.dat" && !swapped { // world/ was listed, now swap it
			swapped = true
			if err := os.Rename(filepath.Join(data, "world"), filepath.Join(data, "world-old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(data, "world")); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, _, err := backupTar(t, s, hook)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range readTar(t, b) {
		if strings.Contains(it.body, "other server") || strings.Contains(it.name, "secret") {
			t.Fatalf("the neighbour's data is in the backup: %+v", it)
		}
	}
	if !swapped {
		t.Fatal("hook did not run")
	}
}

func TestBackupTarSwappedFileIsSkipped(t *testing.T) {
	s, data := newService(t)
	outside := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(outside, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	put(t, data, "a.txt", "a")
	put(t, data, "b.txt", "b")
	hook := func(p string) {
		if p == "b.txt" {
			_ = os.Remove(filepath.Join(data, "b.txt"))
			_ = os.Symlink(outside, filepath.Join(data, "b.txt"))
		}
	}
	b, skipped, err := backupTar(t, s, hook)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range readTar(t, b) {
		if it.body == "host" {
			t.Fatalf("followed the swapped-in symlink: %+v", it)
		}
	}
	if skipped == 0 {
		t.Fatal("the swapped file was not counted as skipped")
	}
}

func TestBackupTarSkipsSymlinks(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "a")
	if err := os.Symlink("a.txt", filepath.Join(data, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(data, "dirlink")); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(readTar(t, b)); len(got) != 1 || got[0] != "a.txt" || skipped != 2 {
		t.Fatalf("entries %v, skipped %d", got, skipped)
	}
}

// TestBackupTarShrinkingFile: a file that changes size between Stat and read keeps the size of
// its Stat (padded with zeros or cut), so the tar stays well-formed.
func TestBackupTarShrinkingFile(t *testing.T) {
	s, data := newService(t)
	put(t, data, "log.txt", "0123456789")
	// The hook runs before the entry's Lstat and open: the header carries the new size.
	b, _, err := backupTar(t, s, func(p string) {
		if p == "log.txt" {
			_ = os.Truncate(filepath.Join(data, "log.txt"), 4)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	items := readTar(t, b)
	if len(items) != 1 || items[0].body != "0123" {
		t.Fatalf("items = %+v", items)
	}
	// Padding path: copyPadded fills a short reader with zeros up to the announced size.
	var buf bytes.Buffer
	if err := copyPadded(&buf, strings.NewReader("ab"), 5); err != nil || buf.String() != "ab\x00\x00\x00" {
		t.Fatalf("copyPadded = %q %v", buf.String(), err)
	}
	buf.Reset()
	if err := copyPadded(&buf, strings.NewReader("abcdefgh"), 3); err != nil || buf.String() != "abc" {
		t.Fatalf("copyPadded (grown) = %q %v", buf.String(), err)
	}
}

func TestBackupTarLimits(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxBackupEntries = 3; c.Limits.MaxBackupBytes = 10 })
	put(t, data, "a", "12345")
	put(t, data, "b", "12345")
	if _, _, err := backupTar(t, s, nil); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	put(t, data, "c", "1")
	if _, _, err := backupTar(t, s, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("over the byte limit: %v", err)
	}
	s2, data2 := newService(t, func(c *Config) { c.Limits.MaxBackupEntries = 2 })
	put(t, data2, "x/y/z", "")
	if _, _, err := backupTar(t, s2, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("over the entry limit: %v", err)
	}
}

// TestBackupTarSwappedFolderInsideRootIsNotFollowed: a parent folder swapped for a symlink
// that stays inside the data directory is not followed either: the walk keeps reading the folder
// it opened (now world-old), never the symlink's target.
func TestBackupTarSwappedFolderInsideRootIsNotFollowed(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/a/inner.txt", "inner")
	put(t, data, "zz/a/other.txt", "other")
	hook := func(p string) {
		if p == "world/a" {
			if err := os.Rename(filepath.Join(data, "world"), filepath.Join(data, "world-old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("zz", filepath.Join(data, "world")); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, _, err := backupTar(t, s, hook)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range readTar(t, b) {
		if strings.HasPrefix(it.name, "world/") && (strings.Contains(it.name, "other") || it.body == "other") {
			t.Fatalf("followed the swapped-in symlink: %+v", it)
		}
	}
}

// TestBackupDeepChain: every entry costs O(1) system calls (folder handles), folders deeper than
// maxBackupDepth are left out.
func TestBackupDeepChain(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxBackupEntries = 10_000 })
	parts := make([]string, maxBackupDepth+40)
	for i := range parts {
		parts[i] = "d"
	}
	if err := os.MkdirAll(filepath.Join(data, filepath.Join(parts...)), 0o755); err != nil {
		t.Fatal(err)
	}
	put(t, data, filepath.ToSlash(filepath.Join(parts[:maxBackupDepth]...))+"/f.txt", "deep")
	start := time.Now()
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := readTar(t, b)
	dirs, files := 0, 0
	for _, it := range items {
		if strings.HasSuffix(it.name, "/") {
			dirs++
		} else {
			files++
		}
	}
	if dirs != maxBackupDepth || files != 1 || skipped != 1 {
		t.Fatalf("%d folders, %d files, %d skipped; want %d, 1, 1", dirs, files, skipped, maxBackupDepth)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %v", d)
	}
}

// TestBackupTarSwappedMiddleFolder: a folder in the middle of the path is swapped for a symlink
// to the neighbour instance while the walk is below it; the walk stays on the folder it opened.
func TestBackupTarSwappedMiddleFolder(t *testing.T) {
	s, data := newService(t)
	other := filepath.Join(s.cfg.InstancesDir, "instances", "2")
	put(t, other, "c/secret.txt", "other server")
	put(t, data, "a/b/c/x.txt", "mine")
	put(t, data, "a/b/c/y.txt", "mine too")
	hook := func(p string) {
		if p == "a/b/c/y.txt" {
			if err := os.Rename(filepath.Join(data, "a/b"), filepath.Join(data, "a/b-old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(data, "a/b")); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, _, err := backupTar(t, s, hook)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range readTar(t, b) {
		if strings.Contains(it.body, "other server") || strings.Contains(it.name, "secret") {
			t.Fatalf("the neighbour's data is in the backup: %+v", it)
		}
		got[it.name] = it.body
	}
	if got["a/b/c/y.txt"] != "mine too" {
		t.Fatalf("entries = %v", got)
	}
}
