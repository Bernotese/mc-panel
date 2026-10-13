package files

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type tarEntry struct {
	name     string
	typ      byte
	body     string
	size     int64 // -1 = len(body)
	mode     int64
	linkname string
}

// makeTar builds a tar exactly as given (lying sizes are written with tar.Writer's raw header
// and a short body: the stream then ends early).
func makeTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		size := e.size
		if size < 0 {
			size = int64(len(e.body))
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Size: size, Mode: mode, Linkname: e.linkname,
			ModTime: time.Unix(1_700_000_000, 0), Format: tar.FormatPAX}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil && size >= int64(len(e.body)) {
				t.Fatal(err)
			}
		}
		if size > int64(len(e.body)) {
			return buf.Bytes() // cut: the declared rest never comes
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func reg(name, body string) tarEntry {
	return tarEntry{name: name, typ: tar.TypeReg, body: body, size: -1}
}
func dir(name string) tarEntry { return tarEntry{name: name, typ: tar.TypeDir} }

func extractInto(t *testing.T, s *Service, b []byte) (string, error) {
	t.Helper()
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("out", dirMode); err != nil {
		t.Fatal(err)
	}
	res, err := s.reserve(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer res.release()
	return "out", extractBackupTar(context.Background(), root, "out", bytes.NewReader(b), s.cfg.Limits, res, func(int64) {})
}

func TestExtractBackupTarWritesFilesModesAndTimes(t *testing.T) {
	s, data := newService(t)
	b := makeTar(t, dir("world/"), reg("world/level.dat", "lvl"), tarEntry{name: "run.sh", typ: tar.TypeReg, body: "#!", size: -1, mode: 0o750},
		reg("deep/a/b.txt", "b")) // parents without own entries are created
	if _, err := extractInto(t, s, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := get(t, data, "out/world/level.dat"); got != "lvl" {
		t.Fatalf("level.dat = %q", got)
	}
	if got, _ := get(t, data, "out/deep/a/b.txt"); got != "b" {
		t.Fatalf("b.txt = %q", got)
	}
	fi, _ := os.Stat(filepath.Join(data, "out/run.sh"))
	if fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("run.sh lost its execute bit: %v", fi.Mode())
	}
	fi, _ = os.Stat(filepath.Join(data, "out/world/level.dat"))
	if !fi.ModTime().Equal(time.Unix(1_700_000_000, 0)) || fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("level.dat = %v %v", fi.ModTime(), fi.Mode())
	}
}

// TestExtractBackupTarRefuses (review focus #3): a hostile or broken stream fails before or
// while writing, and never writes outside the target folder.
func TestExtractBackupTarRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		tar  func(t *testing.T) []byte
		want error
	}{
		{"symlink", func(t *testing.T) []byte {
			return makeTar(t, tarEntry{name: "l", typ: tar.TypeSymlink, linkname: "/etc"})
		}, filesapi.ErrInvalid},
		{"hardlink", func(t *testing.T) []byte {
			return makeTar(t, reg("a", "x"), tarEntry{name: "b", typ: tar.TypeLink, linkname: "a"})
		}, filesapi.ErrInvalid},
		{"char device", func(t *testing.T) []byte { return makeTar(t, tarEntry{name: "c", typ: tar.TypeChar}) }, filesapi.ErrInvalid},
		{"fifo", func(t *testing.T) []byte { return makeTar(t, tarEntry{name: "f", typ: tar.TypeFifo}) }, filesapi.ErrInvalid},
		{"dotdot", func(t *testing.T) []byte { return makeTar(t, reg("../escape", "x")) }, filesapi.ErrInvalid},
		{"absolute", func(t *testing.T) []byte { return makeTar(t, reg("/etc/passwd", "x")) }, filesapi.ErrInvalid},
		{"double slash", func(t *testing.T) []byte { return makeTar(t, reg("a//b", "x")) }, filesapi.ErrInvalid},
		{"hidden temp", func(t *testing.T) []byte { return makeTar(t, reg(".mcpanel-x", "x")) }, filesapi.ErrInvalid},
		{"hidden rcon", func(t *testing.T) []byte { return makeTar(t, reg(".rcon-cli.env", "x")) }, filesapi.ErrInvalid},
		{"duplicate", func(t *testing.T) []byte { return makeTar(t, reg("a", "1"), reg("a", "2")) }, filesapi.ErrInvalid},
		{"file is a parent", func(t *testing.T) []byte { return makeTar(t, reg("a", "1"), reg("a/b", "2")) }, filesapi.ErrInvalid},
		{"cut stream", func(t *testing.T) []byte {
			return makeTar(t, tarEntry{name: "big", typ: tar.TypeReg, body: "abc", size: 100})
		}, filesapi.ErrInvalid},
		{"garbage", func(*testing.T) []byte { return bytes.Repeat([]byte{0xff}, 1024) }, filesapi.ErrInvalid},
		{"too many entries", func(t *testing.T) []byte {
			var es []tarEntry
			for i := range 101 {
				es = append(es, reg("f"+string(rune('a'+i%26))+string(rune('a'+i/26)), ""))
			}
			return makeTar(t, es...)
		}, filesapi.ErrTooLarge},
		{"too many bytes", func(t *testing.T) []byte { return makeTar(t, reg("a", string(make([]byte, 1<<20+1)))) }, filesapi.ErrTooLarge},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, data := newService(t)
			_, err := extractInto(t, s, c.tar(t))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			// Nothing outside out/ (the parent of the data directory stays untouched as well).
			for _, n := range names(t, data) {
				if n != "out" {
					t.Fatalf("written outside the target: %s", n)
				}
			}
			if _, err := os.Lstat(filepath.Join(s.cfg.InstancesDir, "instances", "escape")); err == nil {
				t.Fatal("escaped the data directory")
			}
		})
	}
}
