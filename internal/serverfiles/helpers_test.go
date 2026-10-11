package serverfiles_test

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// env is a real database plus the files handler mounted on a real rbac.Router (with the
// DB-backed checker), so every request also exercises the route rule.
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *audit.MemoryRecorder
	files  *fakeFiles
	agent  *fakeAgent
	h      *serverfiles.Handler
	mux    http.Handler
	rootID int64
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith lets a test change the dependencies (e.g. Files = nil) before the handler is built.
func newEnvWith(t *testing.T, opt func(*serverfiles.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{},
		files: newFakeFiles(), agent: newFakeAgent(),
	}
	d := serverfiles.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Files: e.files, Instances: e.agent,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = serverfiles.NewHandler(d)
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root".
func (e *env) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func srvPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d%s", id, suffix) }

// do sends a JSON request with client IP 203.0.113.7 and the given principal (nil = anonymous).
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
	return e.doRaw(method, path, rd, p)
}

// doRaw sends body unchanged (a *bytes.Reader sets Content-Length).
func (e *env) doRaw(method, path string, body io.Reader, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, path, body)
	rctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		rctx = principal.With(rctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(rctx))
	return rec
}

// server starts a real HTTP server for the handler (stream and deadline tests); every request
// runs as p from client IP 203.0.113.7. tune may change the http.Server before it starts.
func (e *env) server(p principal.Principal, tune func(*http.Server)) *httptest.Server {
	e.t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := httpx.WithClientIP(r.Context(), netip.MustParseAddr("203.0.113.7"))
		e.mux.ServeHTTP(w, r.WithContext(principal.With(ctx, p)))
	})
	srv := httptest.NewUnstartedServer(h)
	if tune != nil {
		tune(srv.Config)
	}
	srv.Start()
	e.t.Cleanup(srv.Close)
	return srv
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
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// grant gives a user permissions on a server directly in the database.
func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// createInstance inserts a server with a paper instance row.
func createInstance(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	id := storetest.CreateServer(t, db, name)
	if _, err := store.New(db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: "paper", Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// lastEntry returns the newest audit entry (fails if there is none).
func (e *env) lastEntry() audit.Entry {
	e.t.Helper()
	if len(e.rec.Entries) == 0 {
		e.t.Fatal("no audit entry")
	}
	return e.rec.Entries[len(e.rec.Entries)-1]
}

// fakeFiles is an in-memory filesapi.Client. Calls are recorded in order ("list 1 plugins").
type fakeFiles struct {
	mu       sync.Mutex
	err      error  // returned by every call while set
	onCall   func() // runs at the start of every call (outside the lock), e.g. to cancel a request
	calls    []string
	list     filesapi.ListResponse
	read     filesapi.ReadResponse
	writeRes filesapi.WriteResponse
	writes   []filesapi.WriteRequest
	uploads  map[string][]byte
	// uploadHook, if set, consumes the upload body instead of io.ReadAll (stream tests).
	uploadHook func(body io.Reader) (int64, error)
	download   filesapi.Download
	job        *filesapi.Job
	extracts   []filesapi.ExtractRequest
}

func newFakeFiles() *fakeFiles { return &fakeFiles{uploads: map[string][]byte{}} }

func (f *fakeFiles) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeFiles) setJob(j *filesapi.Job) { f.mu.Lock(); f.job = j; f.mu.Unlock() }

func (f *fakeFiles) setUploadHook(h func(io.Reader) (int64, error)) {
	f.mu.Lock()
	f.uploadHook = h
	f.mu.Unlock()
}

// takeCalls returns the recorded calls and forgets them.
func (f *fakeFiles) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

// begin records call; like the real client it fails with ErrUnavailable once ctx is done.
func (f *fakeFiles) begin(ctx context.Context, call string) error {
	f.mu.Lock()
	hook := f.onCall
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeFiles) Health(ctx context.Context) error { return f.begin(ctx, "health") }

func (f *fakeFiles) List(ctx context.Context, id int64, path string) (filesapi.ListResponse, error) {
	if err := f.begin(ctx, fmt.Sprintf("list %d %s", id, path)); err != nil {
		return filesapi.ListResponse{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list, nil
}

func (f *fakeFiles) Read(ctx context.Context, id int64, path string) (filesapi.ReadResponse, error) {
	if err := f.begin(ctx, fmt.Sprintf("read %d %s", id, path)); err != nil {
		return filesapi.ReadResponse{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read, nil
}

func (f *fakeFiles) Write(ctx context.Context, req filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	if err := f.begin(ctx, fmt.Sprintf("write %d %s", req.ID, req.Path)); err != nil {
		return filesapi.WriteResponse{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	return f.writeRes, nil
}

func (f *fakeFiles) Upload(ctx context.Context, id int64, path string, overwrite bool, size int64, body io.Reader) (filesapi.UploadResponse, error) {
	if err := f.begin(ctx, fmt.Sprintf("upload %d %s overwrite=%t size=%d", id, path, overwrite, size)); err != nil {
		return filesapi.UploadResponse{}, err
	}
	f.mu.Lock()
	hook := f.uploadHook
	f.mu.Unlock()
	if hook != nil {
		n, err := hook(body)
		return filesapi.UploadResponse{Size: n}, err
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: got %d bytes, announced %d", filesapi.ErrInvalid, len(b), size)
	}
	f.mu.Lock()
	f.uploads[path] = b
	f.mu.Unlock()
	return filesapi.UploadResponse{Size: size}, nil
}

func (f *fakeFiles) Download(ctx context.Context, id int64, path string) (filesapi.Download, error) {
	if err := f.begin(ctx, fmt.Sprintf("download %d %s", id, path)); err != nil {
		return filesapi.Download{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.download, nil
}

func (f *fakeFiles) Mkdir(ctx context.Context, id int64, path string) error {
	return f.begin(ctx, fmt.Sprintf("mkdir %d %s", id, path))
}

func (f *fakeFiles) Rename(ctx context.Context, req filesapi.RenameRequest) error {
	return f.begin(ctx, fmt.Sprintf("rename %d %s %s", req.ID, req.From, req.To))
}

func (f *fakeFiles) Delete(ctx context.Context, id int64, path string) error {
	return f.begin(ctx, fmt.Sprintf("delete %d %s", id, path))
}

func (f *fakeFiles) Extract(ctx context.Context, req filesapi.ExtractRequest) (filesapi.Job, error) {
	if err := f.begin(ctx, fmt.Sprintf("extract %d %s %s", req.ID, req.Path, req.Dest)); err != nil {
		return filesapi.Job{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.extracts = append(f.extracts, req)
	j := filesapi.Job{ID: "j1", State: "running", Path: req.Path, Dest: req.Dest, StartedAt: t0.UnixMilli()}
	f.job = &j
	return j, nil
}

func (f *fakeFiles) Job(ctx context.Context, id int64) (*filesapi.Job, error) {
	if err := f.begin(ctx, fmt.Sprintf("job %d", id)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.job == nil {
		return nil, nil
	}
	j := *f.job
	return &j, nil
}

// fakeAgent is an in-memory agentapi.InstancesClient for the extract state check.
type fakeAgent struct {
	mu     sync.Mutex
	onCall func() // runs at the start of Instances (outside the lock)
	err    error
	docker string
	inst   map[int64]agentapi.InstanceStatus
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{docker: agentapi.DockerOK, inst: map[int64]agentapi.InstanceStatus{}}
}

func (f *fakeAgent) set(st agentapi.InstanceStatus) { f.mu.Lock(); f.inst[st.ID] = st; f.mu.Unlock() }

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	hook := f.onCall
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
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
	return agentapi.InstancesResponse{Docker: f.docker, Instances: list}, nil
}

var errNotUsed = errors.New("fake agent: not used by serverfiles")

func (f *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error     { return errNotUsed }
func (f *fakeAgent) Power(context.Context, int64, agentapi.PowerAction) error { return errNotUsed }
func (f *fakeAgent) Remove(context.Context, int64) error                      { return errNotUsed }

// httptestRequestWithContext builds a request for p (client IP 203.0.113.7) on ctx.
func httptestRequestWithContext(ctx context.Context, method, path string, p principal.Principal) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	rctx := principal.With(httpx.WithClientIP(ctx, netip.MustParseAddr("203.0.113.7")), p)
	return req.WithContext(rctx)
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }
