package files

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// fakeDisk reports a fixed amount of free space and a fixed size (0: the percentage reserve is
// 0 and reserve_bytes alone counts).
type fakeDisk struct {
	mu    sync.Mutex
	avail uint64
	total uint64
	err   error
}

func (d *fakeDisk) Available(string) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.avail, d.err
}

func (d *fakeDisk) Total(string) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.total, d.err
}

func (d *fakeDisk) set(avail uint64) {
	d.mu.Lock()
	d.avail = avail
	d.mu.Unlock()
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testLimits() config.FilesLimits {
	return config.FilesLimits{
		MaxUploadBytes:    1 << 20,
		MaxExtractBytes:   1 << 20,
		MaxExtractEntries: 100,
		ReserveBytes:      1 << 10,
		MaxTextBytes:      64 << 10,
		MaxListEntries:    50,
		MaxBackupBytes:    1 << 20,
		MaxBackupEntries:  100,
		MaxBackupJobs:     1,
	}
}

var testStart = time.UnixMilli(1_760_000_000_000)

// newService returns a Service on a fresh instances_dir that already contains the data
// directory of server 1, and the path of that data directory.
func newService(t *testing.T, mod ...func(*Config)) (*Service, string) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "instances", "1")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		InstancesDir: base,
		Limits:       testLimits(),
		Clock:        clock.NewFake(testStart),
		Log:          discardLog(),
		Disk:         &fakeDisk{avail: 1 << 40},
	}
	for _, m := range mod {
		m(&cfg)
	}
	s := New(cfg)
	t.Cleanup(s.Close)
	return s, data
}

// put writes content to rel (slash-separated) below dir, creating folders.
func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // G703: test fixture below t.TempDir()
		t.Fatal(err)
	}
}

// get reads rel below dir; ok is false if it does not exist.
func get(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// names lists the entry names of a directory on disk (including hidden ones).
func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// startHTTP serves srv over a TCP httptest server (no peer credentials).
func startHTTP(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// postJSON posts body as JSON to ts+path and returns status and body.
func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// errCode decodes the code of an error answer.
func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var er struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("not an error body: %s", body)
	}
	return er.Code
}
