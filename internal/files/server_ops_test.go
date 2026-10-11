package files

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerOps(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "hello")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	status, body := postJSON(t, ts, filesapi.PathList, filesapi.PathRequest{ID: 1, Path: ""})
	var list filesapi.ListResponse
	if status != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list.Entries) != 1 {
		t.Fatalf("list: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathRead, filesapi.PathRequest{ID: 1, Path: "a.txt"})
	var read filesapi.ReadResponse
	if status != http.StatusOK || json.Unmarshal(body, &read) != nil || read.Content != "hello" {
		t.Fatalf("read: %d %s", status, body)
	}
	stale := read.MTime - 5
	status, body = postJSON(t, ts, filesapi.PathWrite, filesapi.WriteRequest{ID: 1, Path: "a.txt", Content: "x", ExpectedMTime: &stale})
	if status != http.StatusConflict || errCode(t, body) != filesapi.CodeModified {
		t.Fatalf("stale write: %d %s", status, body)
	}
	if status, _ := postJSON(t, ts, filesapi.PathMkdir, filesapi.PathRequest{ID: 1, Path: "dir"}); status != http.StatusCreated {
		t.Fatalf("mkdir: %d", status)
	}
	if status, _ := postJSON(t, ts, filesapi.PathRename, filesapi.RenameRequest{ID: 1, From: "a.txt", To: "dir/a.txt"}); status != http.StatusNoContent {
		t.Fatalf("rename: %d", status)
	}
	if status, _ := postJSON(t, ts, filesapi.PathDelete, filesapi.PathRequest{ID: 1, Path: "dir"}); status != http.StatusNoContent {
		t.Fatalf("delete: %d", status)
	}
	status, body = postJSON(t, ts, filesapi.PathRead, filesapi.PathRequest{ID: 1, Path: "a.txt"})
	if status != http.StatusNotFound || errCode(t, body) != filesapi.CodeNotFound {
		t.Fatalf("read deleted: %d %s", status, body)
	}

	// Strict decoding and limits.
	status, body = postJSON(t, ts, filesapi.PathList, map[string]any{"id": 1, "path": "", "extra": true})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("unknown field: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathList, filesapi.PathRequest{ID: 0})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("id 0: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathList, filesapi.PathRequest{ID: 1, Path: strings.Repeat("a", 70<<10)})
	if status != http.StatusRequestEntityTooLarge || errCode(t, body) != filesapi.CodeTooLarge {
		t.Fatalf("oversized body: %d %s", status, body)
	}
	resp, err := http.Post(ts.URL+filesapi.PathList, "text/plain", strings.NewReader(`{"id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", resp.StatusCode)
	}
	// A large editor save fits (write allows MaxJSONBytes).
	status, body = postJSON(t, ts, filesapi.PathWrite, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: strings.Repeat("b", 60<<10)})
	if status != http.StatusOK {
		t.Fatalf("large write: %d %s", status, body)
	}
}

func TestServerRejectsTrailingData(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "x")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	for _, body := range []string{`{"id":1,"path":""}{"id":1}`, `{"id":1,"path":""} x`, `{"id":1,"path":""}]`} {
		resp, err := http.Post(ts.URL+filesapi.PathList, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || errCode(t, b) != filesapi.CodeInvalid {
			t.Errorf("body %q: %d %s", body, resp.StatusCode, b)
		}
	}
	// Trailing white space is fine.
	resp, err := http.Post(ts.URL+filesapi.PathList, "application/json", strings.NewReader("{\"id\":1,\"path\":\"\"}\n \n"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trailing white space: %d", resp.StatusCode)
	}
}
