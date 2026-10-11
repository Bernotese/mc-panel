package console_test

import (
	"bytes"
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
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// env is a real database plus the console handler mounted on a real rbac.Router (with the
// DB-backed checker), so every request also exercises the route rule.
type env struct {
	t          *testing.T
	db         *sql.DB
	clk        *clock.Fake
	rec        *audit.MemoryRecorder
	agent      *fakeAgent
	access     *fakeAccess
	h          *console.Handler
	mux        http.Handler
	rootID     int64
	principals sync.Map // user id -> principal.Principal (stream tests, see stream_test.go)
	timers     *fakeTimers
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith lets a test change the dependencies (e.g. Agent = nil) before the handler is built.
func newEnvWith(t *testing.T, opt func(*console.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{},
		agent: newFakeAgent(), access: &fakeAccess{}, timers: newFakeTimers(),
	}
	d := console.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Agent: e.agent, Access: e.access,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = console.NewHandler(d)
	console.SetTimers(e.h, e.timers)
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root".
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1,
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func srvPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d%s", id, suffix) }

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
	rctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		rctx = principal.With(rctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(rctx))
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

// waitFor polls cond every 10 ms for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeAccess is a console.Access with a settable answer.
type fakeAccess struct {
	mu    sync.Mutex
	err   error
	calls int
	perms []rbac.Permission
}

func (f *fakeAccess) set(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeAccess) Recheck(_ context.Context, _ principal.Principal, _ int64, perm rbac.Permission) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.perms = append(f.perms, perm)
	return f.err
}

// fakeAgent is an in-memory agentapi.ConsoleClient.
type fakeAgent struct {
	mu         sync.Mutex
	outputs    map[string]string // Rcon output per command ("" if missing)
	rconErrs   map[string]error  // Rcon error per command
	rcons      []string          // "<id> <command>" of every Rcon call
	players    agentapi.PlayersResponse
	playersErr error
	logsErr    error         // returned by Logs while set
	streams    []*fakeStream // handed out by Logs in order; none left = ErrUnavailable
	logReqs    []agentapi.LogsRequest
}

var _ agentapi.ConsoleClient = (*fakeAgent)(nil)

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		outputs: map[string]string{}, rconErrs: map[string]error{},
		players: agentapi.PlayersResponse{
			Ops: []agentapi.PlayerOp{}, Whitelist: []agentapi.PlayerEntry{},
			Bans: []agentapi.PlayerBan{}, Errors: []string{},
		},
	}
}

func (f *fakeAgent) setLogsErr(err error) { f.mu.Lock(); f.logsErr = err; f.mu.Unlock() }

func (f *fakeAgent) setOutput(cmd, out string) { f.mu.Lock(); f.outputs[cmd] = out; f.mu.Unlock() }

func (f *fakeAgent) setRconErr(cmd string, err error) {
	f.mu.Lock()
	f.rconErrs[cmd] = err
	f.mu.Unlock()
}

func (f *fakeAgent) setPlayers(p agentapi.PlayersResponse, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.players, f.playersErr = p, err
}

// takeRcons returns the recorded Rcon calls and forgets them.
func (f *fakeAgent) takeRcons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.rcons
	f.rcons = nil
	return out
}

func (f *fakeAgent) Rcon(ctx context.Context, id int64, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rcons = append(f.rcons, fmt.Sprintf("%d %s", id, command))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := f.rconErrs[command]; err != nil {
		return "", err
	}
	return f.outputs[command], nil
}

func (f *fakeAgent) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.players, f.playersErr
}

func (f *fakeAgent) Logs(_ context.Context, req agentapi.LogsRequest) (agentapi.LogStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logReqs = append(f.logReqs, req)
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	if len(f.streams) == 0 {
		return nil, fmt.Errorf("%w: no stream prepared", agentapi.ErrUnavailable)
	}
	s := f.streams[0]
	f.streams = f.streams[1:]
	return s, nil
}

// prepare queues a stream of a running container for the next Logs call.
func (f *fakeAgent) prepare() *fakeStream { return f.prepareStream(true) }

// prepareStopped queues a stream of a stopped container (Running() == false).
func (f *fakeAgent) prepareStopped() *fakeStream { return f.prepareStream(false) }

func (f *fakeAgent) prepareStream(running bool) *fakeStream {
	s := newFakeStream(running)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, s)
	return s
}

func (f *fakeAgent) requests() []agentapi.LogsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.logReqs)
}

var errStreamClosed = errors.New("fake stream closed")

// fakeStream is an agentapi.LogStream fed by the test.
type fakeStream struct {
	running bool
	lines   chan agentapi.LogLine
	end     chan error
	closed  chan struct{}
	once    sync.Once
}

func newFakeStream(running bool) *fakeStream {
	return &fakeStream{running: running, lines: make(chan agentapi.LogLine), end: make(chan error, 1), closed: make(chan struct{})}
}

func (s *fakeStream) Running() bool { return s.running }

func (s *fakeStream) Next() (agentapi.LogLine, error) {
	select {
	case l := <-s.lines:
		return l, nil
	case err := <-s.end:
		return agentapi.LogLine{}, err
	case <-s.closed:
		return agentapi.LogLine{}, errStreamClosed
	}
}

func (s *fakeStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// send hands one line to the panel's reader (blocks until it is taken).
func (s *fakeStream) send(t *testing.T, l agentapi.LogLine) {
	t.Helper()
	select {
	case s.lines <- l:
	case <-time.After(5 * time.Second):
		t.Fatalf("nobody reads line %+v", l)
	}
}

// finish ends the stream like the agent does when the container stops.
func (s *fakeStream) finish() { s.end <- io.EOF }

func (s *fakeStream) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("agent stream was not closed")
	}
}
