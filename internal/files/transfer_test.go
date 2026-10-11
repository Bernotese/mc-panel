package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func upload(s *Service, path string, overwrite bool, content string) (filesapi.UploadResponse, error) {
	return s.Upload(ctx, 1, path, overwrite, int64(len(content)), strings.NewReader(content))
}

func TestUploadWritesAtomically(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/old.jar", "old")
	res, err := upload(s, "plugins/new.jar", false, "jar bytes")
	if err != nil || res.Size != 9 {
		t.Fatalf("upload: %+v %v", res, err)
	}
	if v, _ := get(t, data, "plugins/new.jar"); v != "jar bytes" {
		t.Fatalf("content %q", v)
	}
	if _, err := upload(s, "plugins/old.jar", false, "new"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("exists without overwrite: %v", err)
	}
	if v, _ := get(t, data, "plugins/old.jar"); v != "old" {
		t.Fatal("refused upload changed the file")
	}
	if _, err := upload(s, "plugins/old.jar", true, "new"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if v, _ := get(t, data, "plugins/old.jar"); v != "new" {
		t.Fatalf("overwrite content %q", v)
	}
	// Body shorter than Content-Length (client aborted): no file, no temp file.
	if _, err := s.Upload(ctx, 1, "short.bin", false, 10, strings.NewReader("abc")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("short body: %v", err)
	}
	if _, ok := get(t, data, "short.bin"); ok {
		t.Fatal("partial upload became visible")
	}
	for _, n := range append(names(t, data), names(t, filepath.Join(data, "plugins"))...) {
		if strings.HasPrefix(n, tempPrefix) {
			t.Fatalf("temp file left behind: %s", n)
		}
	}
}

func TestUploadLimits(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	outside := layout(t, data)
	for name, c := range map[string]struct {
		path string
		size int64
		want error
	}{
		"no length":     {"a.bin", -1, filesapi.ErrInvalid},
		"too large":     {"a.bin", 1<<20 + 1, filesapi.ErrTooLarge},
		"root":          {"", 1, filesapi.ErrInvalid},
		"hidden":        {".rcon-cli.env", 1, filesapi.ErrNotFound},
		"missing dir":   {"nope/a.bin", 1, filesapi.ErrNotFound},
		"symlink dir":   {"current/a.bin", 1, filesapi.ErrNotRegular},
		"symlink file":  {"abs-link", 1, filesapi.ErrNotRegular},
		"fifo":          {"pipe", 1, filesapi.ErrNotRegular},
		"onto a folder": {"world", 1, filesapi.ErrNotRegular},
	} {
		if _, err := s.Upload(ctx, 1, c.path, true, c.size, strings.NewReader("x")); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "host secret" {
		t.Fatal("upload changed a file outside the data dir")
	}
	disk.set(2 << 10)
	if _, err := upload(s, "big.bin", false, strings.Repeat("x", 1500)); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("reserve: %v", err)
	}
}

// blockingReader blocks until released, then reports EOF.
type blockingReader struct{ release chan struct{} }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.release
	return 0, io.EOF
}

func TestUploadBusy(t *testing.T) {
	s, _ := newService(t)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := range maxUploads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Upload(ctx, 1, "slow"+string(rune('a'+i)), false, 5, blockingReader{release})
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.uploads) < maxUploads {
		if time.Now().After(deadline) {
			t.Fatal("uploads did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := upload(s, "fifth", false, "x"); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("fifth upload: %v, want ErrBusy", err)
	}
	close(release)
	wg.Wait()
	if _, err := upload(s, "after", false, "x"); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
}

// zeroReader yields n zero bytes.
type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	clear(p)
	z.n -= int64(len(p))
	return len(p), nil
}

func TestUploadStreamsWithoutBuffering(t *testing.T) {
	const size = 64 << 20
	s, data := newService(t, func(c *Config) { c.Limits.MaxUploadBytes = size })
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := s.Upload(ctx, 1, "world.zip", false, size, &zeroReader{n: size}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
		t.Fatalf("a %d MiB upload allocated %d MiB", size>>20, grew>>20)
	}
	if fi, err := os.Stat(filepath.Join(data, "world.zip")); err != nil || fi.Size() != size {
		t.Fatalf("stored size: %v", err)
	}
}

type download struct {
	started int
	isDir   bool
	size    int64
	body    bytes.Buffer
}

func (d *download) start(isDir bool, size int64) {
	d.started++
	d.isDir, d.size = isDir, size
}

func TestDownloadFile(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	var d download
	if err := s.Download(ctx, 1, "plugins/Essentials/config.yml", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	if d.started != 1 || d.isDir || d.size != 12 || d.body.String() != "spawn: true\n" {
		t.Fatalf("download: %+v %q", d, d.body.String())
	}
	for p, want := range map[string]error{
		"abs-link": filesapi.ErrNotRegular, "pipe": filesapi.ErrNotRegular, "current": filesapi.ErrNotRegular,
		".rcon-cli.env": filesapi.ErrNotFound, "missing": filesapi.ErrNotFound,
	} {
		var d download
		if err := s.Download(ctx, 1, p, d.start, &d.body); !errors.Is(err, want) || d.started != 0 {
			t.Errorf("download %q: %v started=%d, want %v before start", p, err, d.started, want)
		}
	}
}

func TestDownloadRedacts(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	var d download
	if err := s.Download(ctx, 1, "server.properties", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.body.String(), "s3cret") || d.size != int64(d.body.Len()) {
		t.Fatalf("download of server.properties: size %d body %q", d.size, d.body.String())
	}
}

func zipNames(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(c)
	}
	return out
}

func TestDownloadDirZip(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	put(t, data, "world/region/r.0.0.mca", "region")
	put(t, data, "world/.mcpanel-tmp-x/partial", "temp")
	if err := os.Symlink("../../secret.txt", filepath.Join(data, "world", "escape")); err != nil {
		t.Fatal(err)
	}

	var d download
	if err := s.Download(ctx, 1, "world", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	if !d.isDir || d.size != -1 {
		t.Fatalf("folder download header: %+v", d)
	}
	got := zipNames(t, d.body.Bytes())
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, " ") != "world/ world/level.dat world/region/ world/region/r.0.0.mca" {
		t.Fatalf("zip entries: %v", keys)
	}

	d = download{}
	if err := s.Download(ctx, 1, "", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	got = zipNames(t, d.body.Bytes())
	for name, content := range got {
		if strings.Contains(content, "s3cret") || strings.Contains(content, "host secret") {
			t.Fatalf("zip leaks a secret in %s", name)
		}
		if strings.HasPrefix(name, ".rcon-cli") || strings.Contains(name, tempPrefix) || name == "abs-link" || name == "pipe" || name == "current" {
			t.Fatalf("zip contains %s", name)
		}
	}
	if !strings.Contains(got["server.properties"], "rcon.password=********") || got["plugins/.rcon-cli.env"] != "nested, visible" {
		t.Fatalf("root zip: %v", got)
	}
}

func TestUploadsReserveSpaceWhileRunning(t *testing.T) {
	disk := &fakeDisk{avail: 10 << 10}
	s, _ := newService(t, func(c *Config) { c.Disk = disk })
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.Upload(ctx, 1, "slow.bin", false, 5<<10, blockingReader{release})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		p := s.pending
		s.mu.Unlock()
		if p == 5<<10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first upload did not reserve its size")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := upload(s, "second.bin", false, strings.Repeat("x", 5<<10)); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("second upload ignores the running one: %v", err)
	}
	close(release)
	<-done // the first one fails (short body) and frees its reservation
	if _, err := upload(s, "second.bin", false, strings.Repeat("x", 5<<10)); err != nil {
		t.Fatalf("reservation not released: %v", err)
	}
}

func TestDirZipSkipsUnsafeNames(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	for _, bad := range []string{`..\..\evil.bat`, "ctl\x01name", "bad\xffutf8", `sub\dir`} {
		if err := os.WriteFile(filepath.Join(data, "world", bad), []byte("evil"), 0o644); err != nil {
			t.Logf("file system refuses %q: %v", bad, err)
		}
	}
	if err := os.Mkdir(filepath.Join(data, "world", `dir\..`), 0o755); err == nil {
		put(t, data, "world/dir\\../inner.txt", "evil")
	}
	var d download
	if err := s.Download(ctx, 1, "world", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	got := zipNames(t, d.body.Bytes())
	for name, content := range got {
		if clean, err := filesapi.CleanPath(strings.TrimSuffix(name, "/")); err != nil || clean != strings.TrimSuffix(name, "/") {
			t.Errorf("zip entry %q is not a clean path", name)
		}
		if content == "evil" {
			t.Errorf("zip entry %q has content of an unsafe name", name)
		}
	}
	if got["world/level.dat"] != "lvl" {
		t.Fatalf("safe entry missing: %v", got)
	}
}

func TestDirZipLimits(t *testing.T) {
	s, data := newService(t, func(c *Config) {
		c.Limits.MaxExtractEntries = 5
		c.Limits.MaxTextBytes = 16
	})
	put(t, data, "server.properties", "rcon.password=s3cret\nmotd=far too long for the limit\n")
	put(t, data, "a.txt", "a")
	// An oversized server.properties is left out, the rest is zipped.
	var d download
	if err := s.Download(ctx, 1, "", d.start, &d.body); err != nil {
		t.Fatalf("oversized server.properties aborts the zip: %v", err)
	}
	got := zipNames(t, d.body.Bytes())
	if _, ok := got["server.properties"]; ok || got["a.txt"] != "a" {
		t.Fatalf("zip: %v", got)
	}
	// More entries than max_extract_entries: the zip is refused (its directory is held in memory).
	for i := range 6 {
		put(t, data, "many/f"+strconv.Itoa(i), "x")
	}
	d = download{}
	if err := s.Download(ctx, 1, "many", d.start, &d.body); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("too many entries: %v, want ErrTooLarge", err)
	}
}
