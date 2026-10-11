package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// fakeFiles simulates the mcpanel-files service: one flat map of file contents per server, a
// directory exists if a file lies below it; Extract starts a job that only finishJob ends.
type fakeFiles struct {
	mu    sync.Mutex
	files map[string][]byte // "<id>/<path>" -> content
	mtime int64
	job   *filesapi.Job
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{files: map[string][]byte{}, mtime: 1_760_184_000_000}
}

func key(id int64, p string) string { return fmt.Sprintf("%d/%s", id, p) }

func (f *fakeFiles) finishJob() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.job != nil {
		f.job.State = "done"
	}
}

func (f *fakeFiles) Health(context.Context) error { return nil }

func (f *fakeFiles) List(_ context.Context, id int64, dir string) (filesapi.ListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := key(id, "")
	if dir != "" {
		prefix = key(id, dir+"/")
	}
	seen := map[string]filesapi.Entry{}
	for k, b := range f.files {
		rest, ok := strings.CutPrefix(k, prefix)
		if !ok {
			continue
		}
		name, sub, isDir := strings.Cut(rest, "/")
		_ = sub
		if isDir {
			seen[name] = filesapi.Entry{Name: name, Type: filesapi.TypeDir, MTime: f.mtime}
		} else {
			seen[name] = filesapi.Entry{Name: name, Type: filesapi.TypeFile, Size: int64(len(b)), MTime: f.mtime}
		}
	}
	out := make([]filesapi.Entry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return filesapi.ListResponse{Entries: out}, nil
}

func (f *fakeFiles) Read(_ context.Context, id int64, p string) (filesapi.ReadResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[key(id, p)]
	if !ok {
		return filesapi.ReadResponse{}, filesapi.ErrNotFound
	}
	return filesapi.ReadResponse{Content: string(b), MTime: f.mtime, Size: int64(len(b))}, nil
}

func (f *fakeFiles) Write(_ context.Context, req filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[key(req.ID, req.Path)] = []byte(req.Content)
	f.mtime++
	return filesapi.WriteResponse{MTime: f.mtime, Size: int64(len(req.Content))}, nil
}

func (f *fakeFiles) Upload(_ context.Context, id int64, p string, _ bool, size int64, body io.Reader) (filesapi.UploadResponse, error) {
	b, err := io.ReadAll(body)
	if err != nil {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, filesapi.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[key(id, p)] = b
	return filesapi.UploadResponse{Size: size}, nil
}

func (f *fakeFiles) Download(_ context.Context, id int64, p string) (filesapi.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[key(id, p)]
	if !ok {
		return filesapi.Download{}, filesapi.ErrNotFound
	}
	return filesapi.Download{Body: io.NopCloser(bytes.NewReader(b)), Size: int64(len(b)), Name: path.Base(p)}, nil
}

func (f *fakeFiles) Mkdir(context.Context, int64, string) error           { return nil }
func (f *fakeFiles) Rename(context.Context, filesapi.RenameRequest) error { return nil }
func (f *fakeFiles) Delete(context.Context, int64, string) error          { return nil }

func (f *fakeFiles) Extract(_ context.Context, req filesapi.ExtractRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := filesapi.Job{ID: "j1", State: "running", Path: req.Path, Dest: req.Dest}
	f.job = &j
	return j, nil
}

func (f *fakeFiles) Job(context.Context, int64) (*filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.job == nil {
		return nil, nil
	}
	j := *f.job
	return &j, nil
}
