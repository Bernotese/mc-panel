package plugins_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

// env is a real database plus the plugins handler on a real rbac.Router, a fake file service,
// a fake agent, a fake catalog and a fake Modrinth/Hangar/CDN (fakeup_test.go). Run runs in
// the background until the test ends (or stop is called).
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *ctxRecorder
	files  *fakeFiles
	agent  *fakeAgent
	cat    *fakeCatalog
	up     *fakeUp
	h      *plugins.Handler
	mux    http.Handler
	rootID int64
	stop   func()
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith lets a test change the dependencies (e.g. Files = nil) before the handler is built.
func newEnvWith(t *testing.T, opt func(*plugins.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &ctxRecorder{}, files: newFakeFiles(), agent: newFakeAgent(),
		cat: &fakeCatalog{cat: catalog.Catalog{Versions: []catalog.Version{{ID: "1.21.11", Java: instancespec.Java21}}}},
	}
	e.up = newFakeUp(t)
	d := plugins.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Files: e.files, Instances: e.agent, Catalog: e.cat,
		Upstream: e.up.client(e.clk), Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = plugins.NewHandler(d)
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	var once sync.Once
	e.stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(e.stop)
	return e
}

func (e *env) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func srvPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d/plugins%s", id, suffix) }

// do sends a JSON request with client IP 203.0.113.7 as p (nil = anonymous).
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// get is do(GET) as admin that must answer 200.
func (e *env) get(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	a := e.admin()
	rec := e.do(http.MethodGet, path, nil, &a)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// createInstance inserts a server with an instance of type typ and version.
func createInstance(t *testing.T, db *sql.DB, name string, typ instancespec.Type, version string) int64 {
	t.Helper()
	id := storetest.CreateServer(t, db, name)
	if _, err := store.New(db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: string(typ), Version: version, Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) paper(name string) int64 {
	return createInstance(e.t, e.db, name, instancespec.TypePaper, "1.21.11")
}

func (e *env) fabric(name string) int64 {
	return createInstance(e.t, e.db, name, instancespec.TypeFabric, "1.21.11")
}

// seedRow writes a plugin_installs row for a jar the panel installed.
func (e *env) seedRow(serverID int64, filename, content string, source, projectID, slug, title, versionID, versionName, channel string) {
	e.t.Helper()
	if err := store.New(e.db).PluginsUpsertInstall(context.Background(), store.PluginsUpsertInstallParams{
		ServerID: serverID, Filename: filename, Sha512: sha512Hex(content), Source: source, ProjectID: projectID,
		ProjectSlug: slug, Title: title, VersionID: versionID, VersionName: versionName, Channel: channel,
		InstalledAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) rows(serverID int64) []store.PluginInstall {
	e.t.Helper()
	rows, err := store.New(e.db).PluginsListInstalls(context.Background(), serverID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func sha512Hex(s string) string { b := sha512.Sum512([]byte(s)); return hex.EncodeToString(b[:]) }

func sha256Hex(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }

// ctxRecorder records audit entries together with the actor from the context (the SQL recorder
// takes the actor from there as well).
type ctxRecorder struct {
	mu      sync.Mutex
	entries []audit.Entry
	actors  []string
}

func (r *ctxRecorder) Record(ctx context.Context, e audit.Entry) error {
	p, _ := principal.From(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	r.actors = append(r.actors, p.Username)
	return nil
}

func (r *ctxRecorder) all() ([]audit.Entry, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.entries), slices.Clone(r.actors)
}

func (r *ctxRecorder) actions() []string {
	es, _ := r.all()
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Action + ":" + string(e.Result)
	}
	return out
}

// fakeFiles is an in-memory mcpanel-files: a flat map of file contents per server. Jars lists
// "<dir>/<name>" like the real service (hashes computed, Meta from meta), UploadChecked checks
// the checksum, Rename and Delete really move and remove.
type fakeFiles struct {
	mu        sync.Mutex
	files     map[string][]byte // "<id>/<path>"
	meta      map[string]*filesapi.JarMeta
	errs      map[string]error // by operation: jars, upload, rename, delete, mkdir
	calls     []string
	truncated bool
	onUpload  func(id int64, path string) // runs before an upload is stored (outside the lock)
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{files: map[string][]byte{}, meta: map[string]*filesapi.JarMeta{}, errs: map[string]error{}}
}

func fkey(id int64, p string) string { return fmt.Sprintf("%d/%s", id, p) }

func (f *fakeFiles) put(id int64, p, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fkey(id, p)] = []byte(content)
}

func (f *fakeFiles) get(id int64, p string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[fkey(id, p)]
	return string(b), ok
}

// names returns the sorted file names directly in dir.
func (f *fakeFiles) names(id int64, dir string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.files {
		if n, ok := strings.CutPrefix(k, fkey(id, dir+"/")); ok && !strings.Contains(n, "/") {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func (f *fakeFiles) setErr(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[op] = err
}

func (f *fakeFiles) setTruncated(v bool) { f.mu.Lock(); f.truncated = v; f.mu.Unlock() }

func (f *fakeFiles) setOnUpload(h func(id int64, path string)) {
	f.mu.Lock()
	f.onUpload = h
	f.mu.Unlock()
}

func (f *fakeFiles) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

func (f *fakeFiles) begin(ctx context.Context, op, call string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.errs[op]
}

func (f *fakeFiles) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if err := f.begin(ctx, "jars", fmt.Sprintf("jars %d %s", id, dir)); err != nil {
		return filesapi.JarsResponse{}, err
	}
	if !filesapi.ValidJarDir(dir) {
		return filesapi.JarsResponse{}, filesapi.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := filesapi.JarsResponse{Jars: []filesapi.Jar{}, Truncated: f.truncated}
	for k, b := range f.files {
		name, ok := strings.CutPrefix(k, fkey(id, dir+"/"))
		if !ok || strings.Contains(name, "/") {
			continue
		}
		enabled, ok := filesapi.IsJarName(name)
		if !ok {
			continue
		}
		out.Jars = append(out.Jars, filesapi.Jar{
			Name: name, Enabled: enabled, Size: int64(len(b)), MTime: t0.UnixMilli(),
			SHA256: sha256Hex(string(b)), SHA512: sha512Hex(string(b)), Meta: f.meta[k],
		})
	}
	sort.Slice(out.Jars, func(i, j int) bool { return out.Jars[i].Name < out.Jars[j].Name })
	return out, nil
}

func (f *fakeFiles) UploadChecked(ctx context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	if err := f.begin(ctx, "upload", fmt.Sprintf("upload %d %s overwrite=%t", id, p, overwrite)); err != nil {
		return filesapi.UploadResponse{}, err
	}
	if err := sum.Validate(); err != nil {
		return filesapi.UploadResponse{}, err
	}
	b, err := io.ReadAll(body)
	if err != nil { // like the real client: a broken body ends the request
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: got %d bytes, announced %d", filesapi.ErrInvalid, len(b), size)
	}
	s256, s512 := sha256Hex(string(b)), sha512Hex(string(b))
	if !sum.Matches(s256, s512) {
		return filesapi.UploadResponse{}, filesapi.ErrHashMismatch
	}
	f.mu.Lock()
	hook := f.onUpload
	f.mu.Unlock()
	if hook != nil {
		hook(id, p)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.files[fkey(id, p)]; exists && !overwrite {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s", filesapi.ErrExists, p)
	}
	f.files[fkey(id, p)] = b
	return filesapi.UploadResponse{Size: size, SHA256: s256, SHA512: s512}, nil
}

func (f *fakeFiles) Rename(ctx context.Context, req filesapi.RenameRequest) error {
	if err := f.begin(ctx, "rename", fmt.Sprintf("rename %d %s %s", req.ID, req.From, req.To)); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to := fkey(req.ID, req.From), fkey(req.ID, req.To)
	b, ok := f.files[from]
	if !ok {
		return filesapi.ErrNotFound
	}
	if _, exists := f.files[to]; exists {
		return filesapi.ErrExists
	}
	delete(f.files, from)
	f.files[to] = b
	if m, ok := f.meta[from]; ok {
		delete(f.meta, from)
		f.meta[to] = m
	}
	return nil
}

func (f *fakeFiles) Delete(ctx context.Context, id int64, p string) error {
	if err := f.begin(ctx, "delete", fmt.Sprintf("delete %d %s", id, p)); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[fkey(id, p)]; !ok {
		return filesapi.ErrNotFound
	}
	delete(f.files, fkey(id, p))
	return nil
}

func (f *fakeFiles) Mkdir(ctx context.Context, id int64, p string) error {
	return f.begin(ctx, "mkdir", fmt.Sprintf("mkdir %d %s", id, p))
}

var errNotUsed = errors.New("fake files: not used by plugins")

func (f *fakeFiles) Health(context.Context) error { return errNotUsed }
func (f *fakeFiles) List(context.Context, int64, string) (filesapi.ListResponse, error) {
	return filesapi.ListResponse{}, errNotUsed
}
func (f *fakeFiles) Read(context.Context, int64, string) (filesapi.ReadResponse, error) {
	return filesapi.ReadResponse{}, errNotUsed
}
func (f *fakeFiles) Write(context.Context, filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	return filesapi.WriteResponse{}, errNotUsed
}
func (f *fakeFiles) Upload(context.Context, int64, string, bool, int64, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNotUsed
}
func (f *fakeFiles) Download(context.Context, int64, string) (filesapi.Download, error) {
	return filesapi.Download{}, errNotUsed
}
func (f *fakeFiles) Extract(context.Context, filesapi.ExtractRequest) (filesapi.Job, error) {
	return filesapi.Job{}, errNotUsed
}
func (f *fakeFiles) Job(context.Context, int64) (*filesapi.Job, error) { return nil, errNotUsed }

// fakeAgent is an in-memory agentapi.InstancesClient for restart_needed.
type fakeAgent struct {
	mu   sync.Mutex
	err  error
	inst map[int64]agentapi.InstanceStatus
}

func newFakeAgent() *fakeAgent { return &fakeAgent{inst: map[int64]agentapi.InstanceStatus{}} }

func (f *fakeAgent) set(st agentapi.InstanceStatus) { f.mu.Lock(); f.inst[st.ID] = st; f.mu.Unlock() }

func (f *fakeAgent) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentapi.InstancesResponse{}, f.err
	}
	list := make([]agentapi.InstanceStatus, 0, len(f.inst))
	for _, st := range f.inst {
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: list}, nil
}

func (f *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error     { return errNotUsed }
func (f *fakeAgent) Power(context.Context, int64, agentapi.PowerAction) error { return errNotUsed }
func (f *fakeAgent) Remove(context.Context, int64) error                      { return errNotUsed }

// fakeCatalog is a plugins.Catalog with a fixed answer.
type fakeCatalog struct {
	mu    sync.Mutex
	cat   catalog.Catalog
	err   error
	asked int
}

func (f *fakeCatalog) Get(context.Context, instancespec.Type) (catalog.Catalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.cat, f.err
}

func (f *fakeCatalog) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }
