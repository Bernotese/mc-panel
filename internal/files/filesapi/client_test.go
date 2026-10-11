package filesapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// serve starts h on a short unix socket path (macOS limits socket paths to 104 bytes).
func serve(t *testing.T, h http.Handler) *filesapi.UnixClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "f.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return filesapi.NewClient(sock)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestClientList(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req filesapi.PathRequest
		if r.Method != http.MethodPost || r.URL.Path != filesapi.PathList || r.Header.Get("Content-Type") != "application/json" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.ID != 7 || req.Path != "plugins" {
			http.Error(w, "bad request", 400)
			return
		}
		writeJSON(w, 200, filesapi.ListResponse{Entries: []filesapi.Entry{{Name: "x.jar", Type: filesapi.TypeFile, Size: 3, MTime: 5}}})
	}))
	got, err := c.List(context.Background(), 7, "plugins/")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Name != "x.jar" {
		t.Fatalf("List = %+v, %v", got, err)
	}
}

func TestClientRejectsBadPathWithoutRequest(t *testing.T) {
	called := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	if _, err := c.Read(context.Background(), 1, "../etc/shadow"); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if err := c.Rename(context.Background(), filesapi.RenameRequest{ID: 1, From: "a", To: "/b"}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("rename err = %v, want ErrInvalid", err)
	}
	if _, err := c.List(context.Background(), 0, ""); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("an invalid request reached the service")
	}
}

func TestClientErrorMapping(t *testing.T) {
	for code, want := range map[string]error{
		"exists": filesapi.ErrExists, "modified": filesapi.ErrModified, "not_text": filesapi.ErrNotText,
		"insufficient_storage": filesapi.ErrInsufficientStorage, "busy": filesapi.ErrBusy, "not_found": filesapi.ErrNotFound,
	} {
		c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			status, _ := map[string]int{"exists": 409, "modified": 409, "not_text": 415, "insufficient_storage": 507, "busy": 429, "not_found": 404}[code]
			writeJSON(w, status, filesapi.ErrorResponse{Error: "x", Code: code})
		}))
		mt := int64(1)
		_, err := c.Write(context.Background(), filesapi.WriteRequest{ID: 1, Path: "a.yml", Content: "x", ExpectedMTime: &mt})
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", code, err, want)
		}
	}
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }))
	if err := c.Mkdir(context.Background(), 1, "x"); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("500: err = %v, want ErrUnavailable", err)
	}
	gone := filesapi.NewClient(filepath.Join(t.TempDir(), "missing.sock"))
	if err := gone.Health(context.Background()); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("no socket: err = %v, want ErrUnavailable", err)
	}
}

func TestClientWriteSendsNullForNewFiles(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Contains(raw, []byte(`"expected_mtime":null`)) || !bytes.Contains(raw, []byte(`"content":"a<b"`)) {
			http.Error(w, string(raw), 400)
			return
		}
		writeJSON(w, 200, filesapi.WriteResponse{MTime: 9, Size: 3})
	}))
	got, err := c.Write(context.Background(), filesapi.WriteRequest{ID: 1, Path: "new.yml", Content: "a<b"})
	if err != nil || got.MTime != 9 {
		t.Fatalf("Write = %+v, %v", got, err)
	}
}

func TestClientUploadStreams(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodPut || r.URL.Path != filesapi.PathUpload || q.Get("id") != "3" ||
			q.Get("path") != "plugins/x.jar" || q.Get("overwrite") != "1" || r.ContentLength != 5 {
			http.Error(w, "bad request "+r.URL.String(), 400)
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		writeJSON(w, 201, filesapi.UploadResponse{Size: n})
	}))
	got, err := c.Upload(context.Background(), 3, "plugins/x.jar", true, 5, strings.NewReader("hello"))
	if err != nil || got.Size != 5 {
		t.Fatalf("Upload = %+v, %v", got, err)
	}
	if _, err := c.Upload(context.Background(), 3, "x", false, -1, strings.NewReader("")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("negative size: err = %v, want ErrInvalid", err)
	}
}

func TestClientDownload(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("path") {
		case "world":
			w.Header().Set(filesapi.HeaderKind, "dir")
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK"))
		default:
			w.Header().Set(filesapi.HeaderKind, "file")
			w.Header().Set("Content-Length", "4")
			_, _ = w.Write([]byte("data"))
		}
	}))
	d, err := c.Download(context.Background(), 1, "logs/latest.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if string(body) != "data" || d.Size != 4 || d.IsDir || d.Name != "latest.log" {
		t.Fatalf("file download = %+v %q", d, body)
	}
	d, err = c.Download(context.Background(), 1, "world")
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Body.Close()
	if !d.IsDir || d.Size != -1 || d.Name != "world" {
		t.Fatalf("dir download = %+v", d)
	}
	d, err = c.Download(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Body.Close()
	if d.Name != "" {
		t.Fatalf("root name = %q, want empty", d.Name)
	}
}

func TestClientJob(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == filesapi.PathJobs {
			if r.URL.Query().Get("id") == "2" {
				writeJSON(w, 200, filesapi.JobsResponse{})
				return
			}
			writeJSON(w, 200, filesapi.JobsResponse{Job: &filesapi.Job{ID: "j1", State: filesapi.JobRunning}})
			return
		}
		writeJSON(w, 202, filesapi.Job{ID: "j1", State: filesapi.JobRunning, Path: "w.zip", Dest: "w"})
	}))
	j, err := c.Extract(context.Background(), filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"})
	if err != nil || j.ID != "j1" {
		t.Fatalf("Extract = %+v, %v", j, err)
	}
	if got, err := c.Job(context.Background(), 1); err != nil || got == nil || got.State != filesapi.JobRunning {
		t.Fatalf("Job(1) = %+v, %v", got, err)
	}
	if got, err := c.Job(context.Background(), 2); err != nil || got != nil {
		t.Fatalf("Job(2) = %+v, %v; want nil", got, err)
	}
}
