package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// startUnix serves a Service on a real Unix socket with peer-UID checking (allow) and returns
// a filesapi client for it. The socket lives in a short temp dir (macOS limits socket paths
// to 104 bytes; t.TempDir is too long there).
func startUnix(t *testing.T, allow uint32) (*filesapi.UnixClient, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	svc, data := newService(t)
	srv := NewServer(svc, "e2e", discardLog())
	sock := filepath.Join(dir, "f.sock")
	l, err := unixsock.Listen(sock, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(allow), discardLog())) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return filesapi.NewClient(sock), data
}

func TestEndToEndWithClient(t *testing.T) {
	c, data := startUnix(t, uint32(os.Getuid()))
	bg := context.Background()
	if err := c.Health(bg); err != nil {
		t.Fatalf("health: %v", err)
	}
	put(t, data, "server.properties", "rcon.password=s3cret\nmotd=x\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")

	if err := c.Mkdir(bg, 1, "plugins"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := c.Mkdir(bg, 1, "plugins"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("mkdir twice: %v, want ErrExists", err)
	}
	w, err := c.Write(bg, filesapi.WriteRequest{ID: 1, Path: "plugins/config.yml", Content: "a: 1\n"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	stale := w.MTime - 1
	if _, err := c.Write(bg, filesapi.WriteRequest{ID: 1, Path: "plugins/config.yml", Content: "a: 2\n", ExpectedMTime: &stale}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("stale write: %v, want ErrModified", err)
	}
	r, err := c.Read(bg, 1, "server.properties")
	if err != nil || strings.Contains(r.Content, "s3cret") {
		t.Fatalf("read: %q %v", r.Content, err)
	}
	if _, err := c.Read(bg, 1, ".rcon-cli.env"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("hidden: %v, want ErrNotFound", err)
	}
	if _, err := c.Read(bg, 1, "plugins"); !errors.Is(err, filesapi.ErrNotRegular) {
		t.Fatalf("read folder: %v, want ErrNotRegular", err)
	}

	payload := bytes.Repeat([]byte("jar"), 10000)
	up, err := c.Upload(bg, 1, "plugins/Big.jar", false, int64(len(payload)), bytes.NewReader(payload))
	if err != nil || up.Size != int64(len(payload)) {
		t.Fatalf("upload: %+v %v", up, err)
	}
	if _, err := c.Upload(bg, 1, "plugins/Big.jar", false, 3, strings.NewReader("abc")); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("upload exists: %v, want ErrExists", err)
	}
	d, err := c.Download(bg, 1, "plugins/Big.jar")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if !bytes.Equal(got, payload) || d.IsDir || d.Size != int64(len(payload)) {
		t.Fatalf("download content/size: %d bytes, %+v", len(got), d)
	}
	d, err = c.Download(bg, 1, "plugins")
	if err != nil {
		t.Fatalf("download folder: %v", err)
	}
	zipped, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if !d.IsDir || zipNames(t, zipped)["plugins/config.yml"] != "a: 1\n" {
		t.Fatalf("folder zip: %+v", d)
	}

	list, err := c.List(bg, 1, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range list.Entries {
		if strings.HasPrefix(e.Name, ".rcon-cli") {
			t.Fatalf("list shows %s", e.Name)
		}
	}
	if err := c.Rename(bg, filesapi.RenameRequest{ID: 1, From: "plugins/Big.jar", To: "Big.jar"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := c.Delete(bg, 1, "plugins"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	putBytes(t, data, "w.zip", makeZip(t, zipEntry{name: "world/level.dat", body: "lvl"}))
	job, err := c.Extract(bg, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "imported"})
	if err != nil || job.ID == "" {
		t.Fatalf("extract: %+v %v", job, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := c.Job(bg, 1)
		if err != nil {
			t.Fatalf("job: %v", err)
		}
		if j != nil && j.State == jobDone {
			break
		}
		if time.Now().After(deadline) || (j != nil && j.State == jobFailed) {
			t.Fatalf("job did not finish: %+v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v, _ := get(t, data, "imported/world/level.dat"); v != "lvl" {
		t.Fatal("extract over the client produced nothing")
	}
	if j, err := c.Job(bg, 2); err != nil || j != nil {
		t.Fatalf("job of a server without jobs: %+v %v", j, err)
	}
	if _, err := c.List(bg, 2, ""); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("server without data dir: %v, want ErrNotFound", err)
	}
}

func TestPeerUIDIsChecked(t *testing.T) {
	c, _ := startUnix(t, uint32(os.Getuid())+1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Health(ctx); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("disallowed peer: %v, want ErrUnavailable", err)
	}
}
