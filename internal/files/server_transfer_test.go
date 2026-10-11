package files

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

func TestServerUploadDownload(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	q := url.Values{"id": {"1"}, "path": {"plugins.jar"}, "overwrite": {"0"}}
	req, _ := http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), strings.NewReader("jar"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"size":3`) {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	// Same again without overwrite: 409 exists.
	req, _ = http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), strings.NewReader("jar"))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || errCode(t, body) != filesapi.CodeExists {
		t.Fatalf("second upload: %d %s", resp.StatusCode, body)
	}
	// Chunked body (no Content-Length): 400 invalid.
	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte("x")); _ = pw.Close() }()
	q.Set("path", "chunked.bin")
	req, _ = http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), pr)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("chunked upload: %d %s", resp.StatusCode, body)
	}

	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=plugins.jar")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "jar" || resp.Header.Get(filesapi.HeaderKind) != "file" || resp.ContentLength != 3 {
		t.Fatalf("download file: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=world")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(filesapi.HeaderKind) != "dir" || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download dir: %d %v", resp.StatusCode, resp.Header)
	}
	if got := zipNames(t, body); got["world/level.dat"] != "lvl" {
		t.Fatalf("dir zip: %v", got)
	}
	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=missing")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || errCode(t, body) != filesapi.CodeNotFound {
		t.Fatalf("download missing: %d %s", resp.StatusCode, body)
	}
	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=x&path=a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: %d", resp.StatusCode)
	}
}

// stallingReader yields n zero bytes and then blocks until release is closed: a client that is
// still in the middle of sending a large body.
type stallingReader struct {
	n       int64
	release chan struct{}
}

func (r *stallingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		<-r.release
		return 0, io.EOF
	}
	if int64(len(p)) > r.n {
		p = p[:r.n]
	}
	clear(p)
	r.n -= int64(len(p))
	return len(p), nil
}

// startUnixPlain serves srv on a Unix socket without peer-UID checks and returns a filesapi
// client for it (short socket path: macOS limits them to 104 bytes).
func startUnixPlain(t *testing.T, srv *Server) *filesapi.UnixClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "f.sock")
	l, err := unixsock.Listen(sock, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return filesapi.NewClient(sock)
}

// An upload refused early (409, 413, 507) must reach the client as that answer while the
// client is still sending the body: the service answers at once instead of waiting for the
// whole body, and it does not reset the connection before the client has read the answer.
func TestUploadEarlyErrorReachesClient(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, data := newService(t, func(c *Config) {
		c.Disk = disk
		c.Limits.MaxUploadBytes = 1 << 40
	})
	put(t, data, "exists.bin", "old")
	c := startUnixPlain(t, NewServer(s, "test", discardLog()))

	const announced = 1 << 30 // far more than is ever sent in the stalled cases
	cases := []struct {
		name string
		path string
		size int64
		disk uint64
		want error
	}{
		{"exists", "exists.bin", announced, 1 << 40, filesapi.ErrExists},
		{"too large", "big.bin", 1<<40 + 1, 1 << 40, filesapi.ErrTooLarge},
		{"disk full", "full.bin", announced, 1 << 20, filesapi.ErrInsufficientStorage},
	}
	for _, tc := range cases {
		disk.set(tc.disk)
		// A client that stalls after 1 MiB: the answer must not wait for the rest of the body.
		// The whole answer (with Content-Length) must arrive while the service still drains the
		// body, not only when it gives up after uploadDrainTime.
		release := make(chan struct{})
		began := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := c.Upload(ctx, 1, tc.path, false, tc.size, &stallingReader{n: 1 << 20, release: release})
		cancel()
		close(release)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s, stalled client: %v, want %v", tc.name, err, tc.want)
		}
		if took := time.Since(began); took >= uploadDrainTime/2 {
			t.Errorf("%s, stalled client: the answer took %s", tc.name, took)
		}
		// A client that keeps writing at full speed: the answer must win against the end of the
		// connection, every time.
		for i := range 10 {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := c.Upload(ctx, 1, tc.path, false, tc.size, &zeroReader{n: tc.size})
			cancel()
			if !errors.Is(err, tc.want) {
				t.Errorf("%s, fast client, try %d: %v, want %v", tc.name, i, err, tc.want)
				break
			}
		}
	}
	if v, _ := get(t, data, "exists.bin"); v != "old" {
		t.Fatalf("refused upload changed the file: %q", v)
	}
	for _, n := range names(t, data) {
		if strings.HasPrefix(n, tempPrefix) || n == "big.bin" || n == "full.bin" {
			t.Fatalf("refused upload left %s", n)
		}
	}
}
