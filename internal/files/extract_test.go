package files

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type zipEntry struct {
	name string
	body string
	mode fs.FileMode // 0 = derive from name
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: time.Unix(1_700_000_000, 0)}
		if e.mode != 0 {
			fh.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawZip writes one entry with a lying header (declared size, method, flags).
func rawZip(t *testing.T, name string, payload []byte, declared uint64, method uint16, flags uint16) []byte {
	t.Helper()
	var comp bytes.Buffer
	data := payload
	if method == zip.Deflate {
		fw, _ := flate.NewWriter(&comp, flate.BestSpeed)
		_, _ = fw.Write(payload)
		_ = fw.Close()
		data = comp.Bytes()
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: name, Method: method, Flags: flags, CRC32: crc32.ChecksumIEEE(payload),
		CompressedSize64: uint64(len(data)), UncompressedSize64: declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func putBytes(t *testing.T, dir, rel string, b []byte) {
	t.Helper()
	put(t, dir, rel, string(b))
}

func waitJob(t *testing.T, s *Service, id int64) filesapi.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j := s.Job(id)
		if j != nil && j.State != jobRunning {
			return *j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still running: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func noTemp(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), tempPrefix) {
			t.Fatalf("temp entry left behind: %s", p)
		}
		return nil
	})
}

func TestExtractSuccess(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "uploads/world.zip", makeZip(t,
		zipEntry{name: "world/"},
		zipEntry{name: "world/level.dat", body: "lvl"},
		zipEntry{name: "world/region/r.0.0.mca", body: strings.Repeat("r", 5000)},
		zipEntry{name: "readme.txt", body: "hi"},
	))
	job, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "uploads/world.zip", Dest: "imported"})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != jobRunning || job.TotalEntries != 4 || job.TotalBytes != 5005 || job.ID == "" {
		t.Fatalf("started job: %+v", job)
	}
	done := waitJob(t, s, 1)
	if done.State != jobDone || done.Bytes != 5005 || done.Entries != 4 || done.FinishedAt == 0 || done.Error != "" {
		t.Fatalf("finished job: %+v", done)
	}
	if v, _ := get(t, data, "imported/world/level.dat"); v != "lvl" {
		t.Fatalf("extracted content %q", v)
	}
	fi, err := os.Stat(filepath.Join(data, "imported", "readme.txt"))
	if err != nil || !fi.ModTime().Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("mtime from the zip: %v %v", fi, err)
	}
	noTemp(t, data)
}

func TestExtractDestination(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	if err := os.Mkdir(filepath.Join(data, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "empty"}); err != nil {
		t.Fatalf("empty destination: %v", err)
	}
	if j := waitJob(t, s, 1); j.State != jobDone {
		t.Fatalf("job: %+v", j)
	}
	if v, _ := get(t, data, "empty/f.txt"); v != "x" {
		t.Fatal("not extracted into the empty folder")
	}
	for req, want := range map[filesapi.ExtractRequest]error{
		{ID: 1, Path: "a.zip", Dest: "empty"}:          filesapi.ErrExists, // no longer empty
		{ID: 1, Path: "a.zip", Dest: "a.zip"}:          filesapi.ErrInvalid,
		{ID: 1, Path: "a.zip", Dest: ""}:               filesapi.ErrInvalid,
		{ID: 1, Path: "a.zip", Dest: "nope/x"}:         filesapi.ErrNotFound,
		{ID: 1, Path: "empty/f.txt", Dest: "y"}:        filesapi.ErrInvalid, // not a .zip
		{ID: 1, Path: "missing.zip", Dest: "y"}:        filesapi.ErrNotFound,
		{ID: 1, Path: "a.zip", Dest: ".mcpanel-tmp-x"}: filesapi.ErrNotFound,
	} {
		if _, err := s.Extract(ctx, req); !errors.Is(err, want) {
			t.Errorf("extract %+v: %v, want %v", req, err, want)
		}
	}
	put(t, data, "garbage.zip", "this is not a zip")
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "garbage.zip", Dest: "g"}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("broken zip: %v", err)
	}
}

func TestExtractRejectsArchives(t *testing.T) {
	s, data := newService(t)
	many := make([]zipEntry, 101)
	for i := range many {
		many[i] = zipEntry{name: "f" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "/" + time.Duration(i).String(), body: "x"}
	}
	cases := map[string]struct {
		zip  []byte
		want error
	}{
		"dotdot":         {makeZip(t, zipEntry{name: "../evil.txt", body: "x"}), filesapi.ErrInvalid},
		"nested dotdot":  {makeZip(t, zipEntry{name: "a/../../evil.txt", body: "x"}), filesapi.ErrInvalid},
		"absolute":       {makeZip(t, zipEntry{name: "/etc/evil", body: "x"}), filesapi.ErrInvalid},
		"backslash":      {makeZip(t, zipEntry{name: `a\..\evil`, body: "x"}), filesapi.ErrInvalid},
		"symlink entry":  {makeZip(t, zipEntry{name: "link", body: "/etc/shadow", mode: fs.ModeSymlink | 0o777}), filesapi.ErrInvalid},
		"hidden":         {makeZip(t, zipEntry{name: ".rcon-cli.env", body: "x"}), filesapi.ErrInvalid},
		"reserved":       {makeZip(t, zipEntry{name: "w/.mcpanel-tmp-1", body: "x"}), filesapi.ErrInvalid},
		"duplicate":      {makeZip(t, zipEntry{name: "a.txt", body: "1"}, zipEntry{name: "a.txt", body: "2"}), filesapi.ErrInvalid},
		"file as parent": {makeZip(t, zipEntry{name: "a", body: "1"}, zipEntry{name: "a/b", body: "2"}), filesapi.ErrInvalid},
		"too many":       {makeZip(t, many...), filesapi.ErrTooLarge},
		"declared large": {makeZip(t, zipEntry{name: "big.bin", body: strings.Repeat("z", 1<<20+1)}), filesapi.ErrTooLarge},
		"encrypted":      {rawZip(t, "e.txt", []byte("secret"), 6, zip.Store, 0x1), filesapi.ErrInvalid},
		"unknown method": {rawZip(t, "m.txt", []byte("data"), 4, 99, 0), filesapi.ErrInvalid},
	}
	for name, c := range cases {
		putBytes(t, data, "bad.zip", c.zip)
		if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "bad.zip", Dest: "out"}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if _, err := os.Stat(filepath.Join(data, "out")); !os.IsNotExist(err) {
			t.Fatalf("%s: rejected archive created output", name)
		}
	}
	if s.Job(1) != nil {
		t.Fatal("a rejected archive started a job")
	}
	noTemp(t, data)
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(data)), "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("zip slip wrote outside")
	}
}

func TestExtractDetectsLyingSizes(t *testing.T) {
	s, data := newService(t)
	// Declares 10 bytes, inflates to 200 KiB: a zip bomb in miniature.
	putBytes(t, data, "bomb.zip", rawZip(t, "bomb.bin", bytes.Repeat([]byte{0}, 200<<10), 10, zip.Deflate, 0))
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "bomb.zip", Dest: "out"}); err != nil {
		t.Fatalf("pre-check passes on declared sizes: %v", err)
	}
	j := waitJob(t, s, 1)
	if j.State != jobFailed || j.Error == "" {
		t.Fatalf("bomb job: %+v", j)
	}
	if _, err := os.Stat(filepath.Join(data, "out")); !os.IsNotExist(err) {
		t.Fatal("failed job left its destination")
	}
	noTemp(t, data)
}

func TestExtractInsufficientStorage(t *testing.T) {
	disk := &fakeDisk{avail: 2 << 10}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.bin", body: strings.Repeat("x", 4<<10)}))
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("reserve: %v", err)
	}
}

func TestExtractJobLimits(t *testing.T) {
	s, data := newService(t)
	release := make(chan struct{})
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	z := makeZip(t, zipEntry{name: "f.txt", body: "x"})
	for _, id := range []int64{1, 2, 3} {
		d := s.instanceDir(id)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		putBytes(t, d, "a.zip", z)
	}
	_ = data
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "o1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "o2"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("second job on the same server: %v, want ErrBusy", err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 2, Path: "a.zip", Dest: "o1"}); err != nil {
		t.Fatalf("job on another server: %v", err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 3, Path: "a.zip", Dest: "o1"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("third job overall: %v, want ErrBusy", err)
	}
	close(release)
	if j := waitJob(t, s, 1); j.State != jobDone {
		t.Fatalf("job 1: %+v", j)
	}
	if j := waitJob(t, s, 2); j.State != jobDone {
		t.Fatalf("job 2: %+v", j)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 3, Path: "a.zip", Dest: "o1"}); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
	waitJob(t, s, 3)
}

func TestCloseCancelsRunningJobs(t *testing.T) {
	s, data := newService(t)
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) { <-ctx.Done() }
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	j := s.Job(1)
	if j == nil || j.State != jobFailed {
		t.Fatalf("job after Close: %+v", j)
	}
	if _, err := os.Stat(filepath.Join(data, "out")); !os.IsNotExist(err) {
		t.Fatal("cancelled job left output")
	}
	noTemp(t, data)
}

func TestServerExtract(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	status, body := postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"})
	var job filesapi.Job
	if status != http.StatusAccepted || json.Unmarshal(body, &job) != nil || job.ID == "" {
		t.Fatalf("extract: %d %s", status, body)
	}
	waitJob(t, s, 1)
	resp, err := http.Get(ts.URL + filesapi.PathJobs + "?id=1")
	if err != nil {
		t.Fatal(err)
	}
	var jr filesapi.JobsResponse
	_ = json.NewDecoder(resp.Body).Decode(&jr)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || jr.Job == nil || jr.Job.State != jobDone {
		t.Fatalf("jobs: %d %+v", resp.StatusCode, jr.Job)
	}
	resp, err = http.Get(ts.URL + filesapi.PathJobs + "?id=2")
	if err != nil {
		t.Fatal(err)
	}
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(b.String()) != `{"job":null}` {
		t.Fatalf("jobs of a server without job: %s", b)
	}
	status, body = postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "../x.zip", Dest: "o"})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("invalid path: %d %s", status, body)
	}
}
