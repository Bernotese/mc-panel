package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"

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
		name, _, isDir := strings.Cut(rest, "/")
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

// Mkdir succeeds: directories exist implicitly in the flat map.
func (f *fakeFiles) Mkdir(context.Context, int64, string) error { return nil }

func (f *fakeFiles) Rename(_ context.Context, req filesapi.RenameRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to := key(req.ID, req.From), key(req.ID, req.To)
	b, ok := f.files[from]
	if !ok {
		return filesapi.ErrNotFound
	}
	if _, exists := f.files[to]; exists {
		return filesapi.ErrExists
	}
	delete(f.files, from)
	f.files[to] = b
	return nil
}

func (f *fakeFiles) Delete(_ context.Context, id int64, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[key(id, p)]; !ok {
		return filesapi.ErrNotFound
	}
	delete(f.files, key(id, p))
	return nil
}

func (f *fakeFiles) Extract(_ context.Context, req filesapi.ExtractRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := filesapi.Job{ID: "j1", Kind: filesapi.JobExtract, State: "running", Path: req.Path, Dest: req.Dest}
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

// State is busy while the extract job runs (data safety spec §2.1), else safe.
func (f *fakeFiles) State(context.Context, int64) (filesapi.StateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.job != nil && f.job.State == filesapi.JobRunning {
		j := *f.job
		return filesapi.StateResponse{State: filesapi.StateBusy, Job: &j}, nil
	}
	return filesapi.StateResponse{State: filesapi.StateSafe}, nil
}

func (f *fakeFiles) ResolveRecovery(context.Context, int64) (filesapi.Recovery, error) {
	return filesapi.Recovery{}, &filesapi.StatusError{Status: 409, Code: "conflict", Message: "no recovery required"}
}

// UploadChecked stores the upload only if the digest really matches; without overwrite an
// existing file is ErrExists (like the service).
func (f *fakeFiles) UploadChecked(_ context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	if err := sum.Validate(); err != nil {
		return filesapi.UploadResponse{}, err
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, filesapi.ErrInvalid
	}
	h256, h512 := digests(b)
	if !sum.Matches(h256, h512) {
		return filesapi.UploadResponse{}, filesapi.ErrHashMismatch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[key(id, p)]; ok && !overwrite {
		return filesapi.UploadResponse{}, filesapi.ErrExists
	}
	f.files[key(id, p)] = b
	return filesapi.UploadResponse{Size: size, SHA256: h256, SHA512: h512}, nil
}

// Jars lists the files directly in <dir> whose names are jars, with real digests and without
// metadata, sorted by name.
func (f *fakeFiles) Jars(_ context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if !filesapi.ValidJarDir(dir) {
		return filesapi.JarsResponse{}, filesapi.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []filesapi.Jar{}
	for k, b := range f.files {
		name, ok := strings.CutPrefix(k, key(id, dir+"/"))
		if !ok || strings.Contains(name, "/") {
			continue
		}
		enabled, isJar := filesapi.IsJarName(name)
		if !isJar {
			continue
		}
		h256, h512 := digests(b)
		out = append(out, filesapi.Jar{Name: name, Enabled: enabled, Size: int64(len(b)), MTime: f.mtime, SHA256: h256, SHA512: h512})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return filesapi.JarsResponse{Jars: out}, nil
}

// digests returns the lowercase hex sha256 and sha512 of b.
func digests(b []byte) (sha256Hex, sha512Hex string) {
	s256, s512 := sha256.Sum256(b), sha512.Sum512(b)
	return hex.EncodeToString(s256[:]), hex.EncodeToString(s512[:])
}

// The fake behaves like the service for what phase B2 relies on: real digests, a refused
// mismatch leaves nothing behind, only top-level jars are listed.
func TestFakeFilesJarsAndChecksums(t *testing.T) {
	f := newFakeFiles()
	ctx := t.Context()
	data := []byte("jar bytes")
	h256, h512 := digests(data)
	wrong := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: strings.Repeat("0", 128)}
	if _, err := f.UploadChecked(ctx, 1, "plugins/a.jar", false, int64(len(data)), wrong, bytes.NewReader(data)); !errors.Is(err, filesapi.ErrHashMismatch) {
		t.Fatalf("wrong digest: %v", err)
	}
	if _, ok := f.files[key(1, "plugins/a.jar")]; ok {
		t.Fatal("a mismatching upload was stored")
	}
	got, err := f.UploadChecked(ctx, 1, "plugins/a.jar", false, int64(len(data)), filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: h256}, bytes.NewReader(data))
	if err != nil || got.SHA512 != h512 || got.SHA256 != h256 || got.Size != int64(len(data)) {
		t.Fatalf("UploadChecked = %+v, %v", got, err)
	}
	if _, err := f.UploadChecked(ctx, 1, "plugins/a.jar", false, int64(len(data)), filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: h512}, bytes.NewReader(data)); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("existing without overwrite: %v", err)
	}
	f.files[key(1, "plugins/B.jar.disabled")] = []byte("b")
	f.files[key(1, "plugins/readme.txt")] = []byte("x")
	f.files[key(1, "plugins/LuckPerms/config.yml")] = []byte("x")
	f.files[key(2, "plugins/other.jar")] = []byte("x")
	list, err := f.Jars(ctx, 1, filesapi.DirPlugins)
	if err != nil || len(list.Jars) != 2 || list.Jars[0].Name != "B.jar.disabled" || list.Jars[0].Enabled ||
		list.Jars[1].Name != "a.jar" || !list.Jars[1].Enabled || list.Jars[1].SHA512 != h512 {
		t.Fatalf("Jars = %+v, %v", list, err)
	}
	if _, err := f.Jars(ctx, 1, "world"); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("bad dir: %v", err)
	}
}
