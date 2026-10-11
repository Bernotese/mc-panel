package files

// Tests for the whole-feature review findings of sub-project 3b (S1-S3, B1, B3, B5, B7, B8).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// S1: folder zips and file downloads are limited; a download over the limit is refused before
// any header goes out.
func TestDownloadsAreLimited(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	put(t, data, "a.txt", "a")

	for range maxZipDownloads {
		s.zipDownloads <- struct{}{}
	}
	var d download
	if err := s.Download(ctx, 1, "world", d.start, &d.body); !errors.Is(err, filesapi.ErrBusy) || d.started != 0 {
		t.Fatalf("zip over the limit: %v (started %d), want ErrBusy before the headers", err, d.started)
	}
	d = download{}
	if err := s.Download(ctx, 1, "a.txt", d.start, &d.body); err != nil || d.body.String() != "a" {
		t.Fatalf("file downloads must not wait for zip slots: %v %q", err, d.body.String())
	}
	for range maxZipDownloads {
		<-s.zipDownloads
	}

	for range maxFileDownloads {
		s.fileDownloads <- struct{}{}
	}
	d = download{}
	if err := s.Download(ctx, 1, "a.txt", d.start, &d.body); !errors.Is(err, filesapi.ErrBusy) || d.started != 0 {
		t.Fatalf("file over the limit: %v (started %d), want ErrBusy", err, d.started)
	}
	for range maxFileDownloads {
		<-s.fileDownloads
	}
	d = download{}
	if err := s.Download(ctx, 1, "world", d.start, &d.body); err != nil {
		t.Fatalf("slots not given back: %v", err)
	}
	if len(s.zipDownloads) != 0 || len(s.fileDownloads) != 0 {
		t.Fatalf("a finished download keeps its slot: zips %d files %d", len(s.zipDownloads), len(s.fileDownloads))
	}
}

func TestServerDownloadBusyIs429(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	for range maxZipDownloads {
		s.zipDownloads <- struct{}{}
	}
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	resp, err := http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=world")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || errCode(t, body) != filesapi.CodeBusy {
		t.Fatalf("busy zip download: %d %s", resp.StatusCode, body)
	}
}

// S2: messages that leave the service never contain host paths of the data directories.
func TestPublicMessagesHideHostPaths(t *testing.T) {
	s, data := newService(t)
	base := s.cfg.InstancesDir
	cases := []string{
		"write " + data + "/world/region/r.0.0.mca: no space left on device",
		"open " + data + ": permission denied",
		"stat " + filepath.Join(base, "instances", "17", "x") + ": gone",
		"config at " + base + "/files.toml",
	}
	for _, msg := range cases {
		got := s.publicMessage(msg)
		if strings.Contains(got, base) {
			t.Errorf("publicMessage(%q) = %q still names the host path", msg, got)
		}
	}
	if got := s.publicMessage("write " + data + "/world/level.dat: disk full"); got != "write world/level.dat: disk full" {
		t.Errorf("path inside the instance: %q", got)
	}
	if got := s.publicMessage("entry \"a/b\" is encrypted"); got != "entry \"a/b\" is encrypted" {
		t.Errorf("message without a path changed: %q", got)
	}
}

func TestFailAnswersWithoutHostPaths(t *testing.T) {
	s, data := newService(t)
	srv := NewServer(s, "test", discardLog())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, filesapi.PathWrite, nil)
	srv.fail(rec, req, "write", fmt.Errorf("%w: write %s/world/x.yml: no space left on device", filesapi.ErrInsufficientStorage, data))
	if rec.Code != http.StatusInsufficientStorage || strings.Contains(rec.Body.String(), s.cfg.InstancesDir) {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
}

func TestJobErrorHasNoHostPath(t *testing.T) {
	s, data := newService(t)
	if err := s.jobs.acquire(1); err != nil {
		t.Fatal(err)
	}
	_, err := s.jobs.start(1, "w.zip", "w", archivePlan{}, s.clock.Now, func(context.Context) error {
		return fmt.Errorf("%w: write %s/.mcpanel-tmp-x/region/r.mca: no space left on device", filesapi.ErrInsufficientStorage, data)
	})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, s, 1)
	if j.State != jobFailed || strings.Contains(j.Error, s.cfg.InstancesDir) || !strings.Contains(j.Error, "no space left") {
		t.Fatalf("job error %q", j.Error)
	}
}

// S3: folder zips leave out names Windows cannot store safely.
func TestDirZipSkipsWindowsUnsafeNames(t *testing.T) {
	s, data := newService(t)
	keep := []string{"level.dat", "console.txt", "COM10", "lpt", "aux1.txt", ".hidden-ok"}
	skip := []string{"CON", "con.txt", "Nul", "aux.tar.gz", "COM1", "lpt9.log", "prn .txt", "a:b", "trail.", "trail "}
	for _, n := range append(append([]string{}, keep...), skip...) {
		put(t, data, "world/"+n, "x")
	}
	put(t, data, "world/AUX/inner.txt", "evil")
	var d download
	if err := s.Download(ctx, 1, "world", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	got := zipNames(t, d.body.Bytes())
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range keep {
		if _, ok := got["world/"+n]; !ok {
			t.Errorf("safe name %q missing: %v", n, names)
		}
	}
	for _, n := range append(skip, "AUX/", "AUX/inner.txt") {
		if _, ok := got["world/"+n]; ok {
			t.Errorf("unsafe name %q in the zip", n)
		}
	}
	// A folder whose own name cannot be stored is refused before the headers.
	put(t, data, "plugins/CON/a.txt", "a")
	d = download{}
	if err := s.Download(ctx, 1, "plugins/CON", d.start, &d.body); !errors.Is(err, filesapi.ErrInvalid) || d.started != 0 {
		t.Fatalf("folder named CON: %v (started %d), want ErrInvalid up front", err, d.started)
	}
}

func TestZipSafeName(t *testing.T) {
	for name, want := range map[string]bool{
		"world": true, "CON": false, "con": false, "con.txt": false, "CONSOLE": true, "COM0": true, "COM9": false,
		"LPT1.x.y": false, "a:b": false, "x.": false, "x ": false, ".x": true, "x y": true, "AUX .log": false,
	} {
		if got := zipSafeName(name); got != want {
			t.Errorf("zipSafeName(%q) = %t, want %t", name, got, want)
		}
	}
}

// B1: an extract whose caller went away during the pre-check starts no job.
type hookDisk struct {
	fakeDisk
	onAvailable func()
}

func (d *hookDisk) Available(p string) (uint64, error) {
	if d.onAvailable != nil {
		d.onAvailable()
	}
	return d.fakeDisk.Available(p)
}

func TestExtractChecksTheCallerBeforeStarting(t *testing.T) {
	cctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	disk := &hookDisk{fakeDisk: fakeDisk{avail: 1 << 40}, onAvailable: cancel}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	putBytes(t, data, "w.zip", makeZip(t, zipEntry{name: "a.txt", body: "a"}))
	if _, err := s.Extract(cctx, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("extract after the caller left: %v, want context.Canceled", err)
	}
	if j := s.Job(1); j != nil {
		t.Fatalf("a job started for a caller that left: %+v", j)
	}
	s.mu.Lock()
	pending := s.pending
	s.mu.Unlock()
	if pending != 0 {
		t.Fatalf("reservation kept: %d", pending)
	}
	disk.onAvailable = nil
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"}); err != nil {
		t.Fatalf("slot not given back: %v", err)
	}
	waitJob(t, s, 1)
}

// B1: delete and extract may take longer than the server's WriteTimeout; the answer still
// reaches the client.
func TestLongOperationsOutliveWriteTimeout(t *testing.T) {
	s, data := newService(t)
	put(t, data, "big/a.txt", "a")
	putBytes(t, data, "w.zip", makeZip(t, zipEntry{name: "a.txt", body: "a"}))
	srv := NewServer(s, "test", discardLog())
	srv.testHook = func(string) { time.Sleep(300 * time.Millisecond) }
	ts := httptest.NewUnstartedServer(srv)
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)

	if code, body := postJSON(t, ts, filesapi.PathDelete, filesapi.PathRequest{ID: 1, Path: "big"}); code != http.StatusNoContent {
		t.Fatalf("slow delete: %d %s", code, body)
	}
	if code, body := postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"}); code != http.StatusAccepted {
		t.Fatalf("slow extract: %d %s", code, body)
	}
	waitJob(t, s, 1)
}

// B3: the headers of a download are flushed before the first byte of the body is written.
type flushOrderWriter struct {
	*httptest.ResponseRecorder
	flushedBeforeBody bool
	wrote             bool
}

func (f *flushOrderWriter) Write(p []byte) (int, error) {
	if !f.wrote {
		f.wrote = true
		f.flushedBeforeBody = f.Flushed
	}
	return f.ResponseRecorder.Write(p)
}

func TestDownloadFlushesHeaders(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	put(t, data, "a.txt", "a")
	srv := NewServer(s, "test", discardLog())
	for _, p := range []string{"world", "a.txt"} {
		w := &flushOrderWriter{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(http.MethodGet, filesapi.PathDownload+"?"+url.Values{"id": {"1"}, "path": {p}}.Encode(), nil)
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK || !w.wrote || !w.flushedBeforeBody {
			t.Fatalf("%s: status %d, headers flushed before the body: %t", p, w.Code, w.flushedBeforeBody)
		}
	}
}

// B5: a folder with too many entries is refused before the headers, not cut off mid-stream.
func TestDirZipLimitIsCheckedUpFront(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxExtractEntries = 5 })
	for i := range 6 {
		put(t, data, fmt.Sprintf("many/f%d", i), "x")
	}
	var d download
	if err := s.Download(ctx, 1, "many", d.start, &d.body); !errors.Is(err, filesapi.ErrTooLarge) || d.started != 0 {
		t.Fatalf("too many entries: %v (started %d), want ErrTooLarge before the headers", err, d.started)
	}
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	resp, err := http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=many")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge || errCode(t, body) != filesapi.CodeTooLarge {
		t.Fatalf("too many entries over HTTP: %d %s", resp.StatusCode, body)
	}
}

// B7: an upload body that stalls or breaks off is the caller's problem (400 invalid), not an
// internal error of the service.
type failingReader struct {
	data []byte
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}

func TestUploadBodyErrorsAreInvalid(t *testing.T) {
	s, data := newService(t)
	for name, rerr := range map[string]error{
		"stalled":     fmt.Errorf("read tcp: %w", os.ErrDeadlineExceeded),
		"interrupted": io.ErrUnexpectedEOF,
	} {
		_, err := s.Upload(ctx, 1, name+".bin", false, 10, &failingReader{data: []byte("abc"), err: rerr})
		if !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s body: %v, want ErrInvalid", name, err)
		}
		if _, ok := get(t, data, name+".bin"); ok {
			t.Errorf("%s upload became visible", name)
		}
	}
	_, err := s.Upload(ctx, 1, "stall.bin", false, 10, &failingReader{data: []byte("abc"), err: os.ErrDeadlineExceeded})
	if err == nil || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("a stalled upload says so: %v", err)
	}
	noTemp(t, data)
}

// B8: the temp cleanup may run while the service already serves: it never removes the temp
// entries of running operations of this process, and it stops when its context ends.
func TestCleanupTempKeepsOwnTempEntries(t *testing.T) {
	s, data := newService(t)
	own, err := s.tempName("world")
	if err != nil {
		t.Fatal(err)
	}
	put(t, data, own, "running upload")
	put(t, data, "world/.mcpanel-tmp-dead", "leftover of an earlier run")
	if n := s.CleanupTemp(ctx); n != 1 {
		t.Fatalf("removed %d entries, want 1", n)
	}
	if _, ok := get(t, data, own); !ok {
		t.Fatal("cleanup removed the temp file of a running operation")
	}
	if _, ok := get(t, data, "world/.mcpanel-tmp-dead"); ok {
		t.Fatal("leftover survived")
	}

	other := New(Config{InstancesDir: s.cfg.InstancesDir, Limits: testLimits(), Log: discardLog(), Disk: &fakeDisk{avail: 1 << 40}})
	defer other.Close()
	if n := other.CleanupTemp(ctx); n != 1 {
		t.Fatalf("a new process keeps the temp entries of the old one: removed %d", n)
	}
}

func TestCleanupTempStopsWithContext(t *testing.T) {
	s, data := newService(t)
	put(t, data, ".mcpanel-tmp-dead", "leftover")
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n := s.CleanupTemp(cctx); n != 0 {
		t.Fatalf("a cancelled cleanup removed %d entries", n)
	}
}

// Review round 2: the folder zip slot is taken only after the pre-check (the walk holds no zip
// in memory), so a folder over the limit answers 413 even while every zip slot is taken.
func TestZipSlotIsTakenAfterThePreCheck(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxExtractEntries = 5 })
	for i := range 6 {
		put(t, data, fmt.Sprintf("many/f%d", i), "x")
	}
	put(t, data, "world/level.dat", "lvl")
	for range maxZipDownloads {
		s.zipDownloads <- struct{}{}
	}
	var d download
	if err := s.Download(ctx, 1, "many", d.start, &d.body); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("folder over the limit with all zip slots taken: %v, want ErrTooLarge", err)
	}
	d = download{}
	if err := s.Download(ctx, 1, "world", d.start, &d.body); !errors.Is(err, filesapi.ErrBusy) || d.started != 0 {
		t.Fatalf("zip with all slots taken: %v (started %d), want ErrBusy before the headers", err, d.started)
	}
}

// Review round 2: only a path prefix is a host path; a user's name that merely contains the
// instances_dir string stays as it is.
func TestPublicMessageOnlyReplacesPathPrefixes(t *testing.T) {
	s, _ := newService(t)
	base := s.cfg.InstancesDir
	for _, msg := range []string{
		`entry "backup` + base + `/x" is encrypted`,
		"entry world" + base + "/instances/1/a is invalid",
	} {
		if got := s.publicMessage(msg); got != msg {
			t.Errorf("publicMessage(%q) = %q, want it unchanged", msg, got)
		}
	}
	if got := s.publicMessage(`open "` + base + `/instances/1/a": denied`); got != `open "a": denied` {
		t.Errorf("quoted host path: %q", got)
	}
}
