package backups_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// t0 is a Monday, 12:00 in Europe/Berlin (CEST).
var t0 = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

// env is a real database plus the backups handler on a real rbac.Router with fakes for the
// files service, the agent's instance and console APIs and the notifier.
type env struct {
	t       *testing.T
	db      *sql.DB
	clk     *clock.Fake
	rec     *ctxRecorder
	files   *fakeFiles
	agent   *fakeAgent
	console *fakeConsole
	notes   *notify.Recording
	kicks   *atomic.Int32
	h       *backups.Handler
	mux     *chi.Mux
	rt      *rbac.Router // registered routes (routes_test.go: route contract)
	rootID  int64
	stop    func()
}

// newEnv builds the env and starts Run.
func newEnv(t *testing.T) *env {
	e := newEnvNoRun(t, nil)
	e.run()
	return e
}

// newEnvNoRun builds the env without Run (recovery tests seed the DB first); opt may change
// the dependencies (e.g. Files = nil) before the handler is built.
func newEnvNoRun(t *testing.T, opt func(*backups.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &ctxRecorder{}, agent: newFakeAgent(),
		console: newFakeConsole(), notes: &notify.Recording{}, kicks: &atomic.Int32{},
	}
	e.files = newFakeFiles(e.clk)
	d := backups.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Notifier: e.notes, Files: e.files, Instances: e.agent,
		Console: e.console, Reconcile: func() { e.kicks.Add(1) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = backups.NewHandler(d)
	backups.SetTimings(e.h, backups.Timings{
		Poll: 5 * time.Millisecond, RCON: 2 * time.Second, Stop: 300 * time.Millisecond,
		Shutdown: 5 * time.Second, IO: 2 * time.Second,
	})
	backups.SetTickEvery(e.h, time.Hour) // tests tick by hand (backups.Tick)
	mux := chi.NewRouter()
	e.rt = rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk)
	e.h.Routes(e.rt)
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	e.stop = func() {}
	return e
}

// run starts Run in the background; stop (also at cleanup) cancels it and waits.
func (e *env) run() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	var once sync.Once
	e.stop = func() { once.Do(func() { cancel(); <-done }) }
	e.t.Cleanup(e.stop)
}

func (e *env) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

// sudoAdmin is admin with a fresh sudo window (restore and delete need it).
func (e *env) sudoAdmin() principal.Principal {
	p := e.admin()
	p.SudoUntil = t0.Add(72 * time.Hour)
	return p
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func bPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d/backups%s", id, suffix) }

// do sends a JSON request with client IP 203.0.113.7 as p (nil = anonymous). body may be a
// string (sent as is) or any JSON value.
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
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

// must is do as p that must answer want.
func (e *env) must(want int, method, path string, body any, p principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := e.do(method, path, body, &p)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
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
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// instance inserts a paper server with an instance.
func (e *env) instance(name string) int64 {
	e.t.Helper()
	id := storetest.CreateServer(e.t, e.db, name)
	if _, err := store.New(e.db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: string(instancespec.TypePaper), Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// schedule returns the server's row (it must exist).
func (e *env) schedule(id int64) store.BackupSchedule {
	e.t.Helper()
	r, err := store.New(e.db).BackupsGetSchedule(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// eventually polls cond for up to 5 s (background jobs use wall time).
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ctxRecorder records audit entries together with the actor from the context.
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

func storeUser(t *testing.T, e *env, name string) int64 {
	t.Helper()
	return storetest.CreateUser(t, e.db, name, "user")
}
