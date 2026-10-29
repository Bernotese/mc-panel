package servers_test

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
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// env is a real database plus the servers handler mounted on a real rbac.Router
// (with the DB-backed checker), so every request also exercises the route rule.
type env struct {
	t       *testing.T
	db      *sql.DB
	clk     *clock.Fake
	rec     *audit.MemoryRecorder
	box     *secretbox.Box
	agent   *fakeAgent
	catalog *fakeCatalog
	data    *fakeStates
	files   *fakeStateClient // the files service's resolve
	notes   *notify.Recording
	purger  *fakePurger
	logs    *bytes.Buffer // reconcile log output
	hostMem uint64        // installed host memory in bytes; 0 = unknown
	h       *servers.Handler
	mux     http.Handler
	rootID  int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := storetest.Open(t)
	box, err := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, box: box,
		agent: newFakeAgent(), catalog: &fakeCatalog{}, logs: &bytes.Buffer{},
		data: newFakeStates(), files: &fakeStateClient{}, purger: &fakePurger{}, notes: &notify.Recording{},
	}
	e.h = servers.NewHandler(servers.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Box: box, Instances: e.agent, Web: e.agent,
		Log: slog.New(slog.NewTextHandler(e.logs, nil)), Catalog: e.catalog, DataStates: e.data, Files: e.files, Notifier: e.notes,
		Backups: e.purger,
		HostMemory: func() (uint64, error) {
			if e.hostMem == 0 {
				return 0, errors.New("host memory unknown")
			}
			return e.hostMem, nil
		},
	})
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root" with a valid sudo window.
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(5 * time.Minute),
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// do sends a request with client IP 203.0.113.7 and the given principal (nil = anonymous).
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

// grant gives a user permissions on a server directly in the database.
func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// setBaseDomain stores a base domain directly (the API path is tested in settings_test.go).
func setBaseDomain(t *testing.T, db *sql.DB, d string) {
	t.Helper()
	mustExec(t, db, `UPDATE instance_settings SET base_domain = ? WHERE id = 1`, d)
}

// createInstance inserts a server with a paper instance row (revision 1, desired running).
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

func instanceRow(t *testing.T, db *sql.DB, id int64) store.Instance {
	t.Helper()
	in, err := store.New(db).ServersGetInstance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// fakeAgent is an in-memory agentapi.InstancesClient. Container states only change when a
// test sets them; calls are recorded in order.
type fakeAgent struct {
	mu        sync.Mutex
	err       error // returned by every call while set
	actionErr error // returned by Ensure, Power and Remove only
	// blockEnsure, while set, holds every Ensure call until it is closed; entered gets one
	// value per held call (both are set before the test starts the reconcile pass).
	blockEnsure chan struct{}
	entered     chan struct{}
	docker      string
	inst        map[int64]agentapi.InstanceStatus
	calls       []string // accepted calls
	failed      []string // calls answered with an error
	ensures     []agentapi.EnsureRequest
	// webErr is returned by WebRoutes only; webStatus answers each route (nil = active).
	webErr    error
	webStatus func(agentapi.WebRoute) agentapi.WebRouteStatus
	webReqs   []agentapi.WebRoutesRequest
	removed   []int64 // InstancesResponse.Removed (confirmed removals)
	disabled  bool    // an agent without containers (make dev without Docker)
	statusN   int     // Instances calls
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{docker: agentapi.DockerOK, inst: map[int64]agentapi.InstanceStatus{}}
}

func (f *fakeAgent) setErr(err error)       { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) setActionErr(err error) { f.mu.Lock(); f.actionErr = err; f.mu.Unlock() }
func (f *fakeAgent) setDocker(s string)     { f.mu.Lock(); f.docker = s; f.mu.Unlock() }

func (f *fakeAgent) set(st agentapi.InstanceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inst[st.ID] = st
}

// takeCalls returns the recorded calls ("ensure 1 r2 recreate=false", "power 1 start",
// "remove 1") and forgets them.
func (f *fakeAgent) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statusN++
	if f.err != nil {
		return agentapi.InstancesResponse{}, f.err
	}
	if f.disabled {
		return agentapi.InstancesResponse{Docker: agentapi.DockerUnavailable, Instances: []agentapi.InstanceStatus{}, Disabled: true}, nil
	}
	list := make([]agentapi.InstanceStatus, 0, len(f.inst))
	for _, st := range f.inst {
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return agentapi.InstancesResponse{Docker: f.docker, Instances: list, Removed: slices.Clone(f.removed)}, nil
}

func (f *fakeAgent) statusCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.statusN }

// confirmRemoved makes the agent report id as removed: its container is gone.
func (f *fakeAgent) confirmRemoved(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inst, id)
	f.removed = append(f.removed, id)
}

func (f *fakeAgent) record(call string) error {
	err := f.err
	if err == nil {
		err = f.actionErr
	}
	if err != nil {
		f.failed = append(f.failed, call)
		return err
	}
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeAgent) failedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.failed)
}

func (f *fakeAgent) lastEnsure() agentapi.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ensures) == 0 {
		return agentapi.EnsureRequest{}
	}
	return f.ensures[len(f.ensures)-1]
}

func (f *fakeAgent) Ensure(_ context.Context, req agentapi.EnsureRequest) error {
	if f.blockEnsure != nil {
		f.entered <- struct{}{}
		<-f.blockEnsure
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := req.Validate(); err != nil {
		return err
	}
	if err := f.record(fmt.Sprintf("ensure %d r%d recreate=%t", req.ID, req.Revision, req.Recreate)); err != nil {
		return err
	}
	f.ensures = append(f.ensures, req)
	return nil
}

func (f *fakeAgent) Power(_ context.Context, id int64, action agentapi.PowerAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record(fmt.Sprintf("power %d %s", id, action))
}

func (f *fakeAgent) Remove(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record(fmt.Sprintf("remove %d", id))
}

func (f *fakeAgent) setWebErr(err error) { f.mu.Lock(); f.webErr = err; f.mu.Unlock() }

func (f *fakeAgent) setWebStatus(fn func(agentapi.WebRoute) agentapi.WebRouteStatus) {
	f.mu.Lock()
	f.webStatus = fn
	f.mu.Unlock()
}

// takeWeb returns the route lists sent so far and forgets them.
func (f *fakeAgent) takeWeb() []agentapi.WebRoutesRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.webReqs
	f.webReqs = nil
	return out
}

func (f *fakeAgent) WebRoutes(_ context.Context, req agentapi.WebRoutesRequest) (agentapi.WebRoutesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := req.Validate(); err != nil {
		return agentapi.WebRoutesResponse{}, err
	}
	f.webReqs = append(f.webReqs, req)
	if err := cmp.Or(f.err, f.webErr); err != nil {
		return agentapi.WebRoutesResponse{}, err
	}
	out := agentapi.WebRoutesResponse{Routes: make([]agentapi.WebRouteStatus, 0, len(req.Routes))}
	for _, r := range req.Routes {
		st := agentapi.WebRouteStatus{ID: r.ID, Status: agentapi.WebActive}
		if f.webStatus != nil {
			st = f.webStatus(r)
		}
		out.Routes = append(out.Routes, st)
	}
	return out, nil
}

// fakeCatalog is a servers.CatalogSource with a fixed answer.
type fakeCatalog struct {
	mu    sync.Mutex
	cat   catalog.Catalog
	err   error
	asked []instancespec.Type
}

func (f *fakeCatalog) Get(_ context.Context, t instancespec.Type) (catalog.Catalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, t)
	return f.cat, f.err
}

// fakeStates is a servers.DataStates with a state per server (missing = safe); err, while
// set, makes every server unknown.
type fakeStates struct {
	mu     sync.Mutex
	states map[int64]servers.State
	jobs   map[int64]string
	err    error
	asked  int
}

func newFakeStates() *fakeStates {
	return &fakeStates{states: map[int64]servers.State{}, jobs: map[int64]string{}}
}

func (f *fakeStates) set(id int64, s servers.State) { f.mu.Lock(); f.states[id] = s; f.mu.Unlock() }

// setBusy makes id busy with a job of kind.
func (f *fakeStates) setBusy(id int64, kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id], f.jobs[id] = servers.StateBusy, kind
}

func (f *fakeStates) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeStates) askedCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.asked }

func (f *fakeStates) State(ctx context.Context, id int64) (servers.State, error) {
	info, err := f.Lookup(ctx, id, nil)
	return info.State, err
}

func (f *fakeStates) Lookup(_ context.Context, id int64, _ servers.Hold) (servers.DataInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	if f.err != nil {
		return servers.DataInfo{State: servers.StateUnknown}, f.err
	}
	s, ok := f.states[id]
	if !ok {
		s = servers.StateSafe
	}
	return servers.DataInfo{State: s, Job: f.jobs[id]}, nil
}

// fakePurger is a servers.BackupPurger that records its calls.
type fakePurger struct {
	mu    sync.Mutex
	calls []int64
	err   error
	hook  func(ctx context.Context) // runs during the call (outside the lock); nil = none
}

func (p *fakePurger) PurgeBackups(ctx context.Context, id int64) error {
	p.mu.Lock()
	p.calls = append(p.calls, id)
	hook, err := p.hook, p.err
	p.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	return err
}

func (p *fakePurger) setHook(h func(ctx context.Context)) { p.mu.Lock(); p.hook = h; p.mu.Unlock() }

func (p *fakePurger) setErr(err error) { p.mu.Lock(); p.err = err; p.mu.Unlock() }

func (p *fakePurger) takeCalls() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.calls
	p.calls = nil
	return c
}
