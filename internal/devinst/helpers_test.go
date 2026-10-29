package devinst_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/devinst"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/monitoring"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// t0 is a Monday, 12:00 in Europe/Berlin (CEST), the default backup_timezone.
var t0 = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

// fiveAM is 05:00 Europe/Berlin (the default sync time) on the day after t0.
var fiveAM = time.Date(2026, 10, 13, 3, 0, 0, 0, time.UTC)

// env is a real database plus the devinst handler on a real rbac.Router with fakes for the
// servers handler, the backup queue, the agent (waker, RCON) and the data states.
type env struct {
	t       *testing.T
	db      *sql.DB
	q       *store.Queries
	clk     *clock.Fake
	rec     *audit.MemoryRecorder
	notes   *notify.Recording
	servers *fakeServers
	syncer  *fakeSyncer
	waker   *fakeWaker
	console *fakeConsole
	data    *fakeStates
	admit   admit.Func // nil = no admission
	h       *devinst.Handler
	mux     *chi.Mux
	rootID  int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, q: store.New(db), clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, notes: &notify.Recording{},
		syncer: &fakeSyncer{}, waker: &fakeWaker{}, console: &fakeConsole{}, data: &fakeStates{states: map[int64]servers.State{}},
	}
	e.servers = &fakeServers{t: t, db: db, clk: e.clk, errs: map[string]error{}}
	e.h = e.newHandler()
	e.mux = chi.NewRouter()
	e.h.Routes(rbac.NewRouter(e.mux, rbac.NewDBChecker(db), e.clk))
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// newHandler builds a handler on the env's database and fakes (a second one simulates a
// panel restart).
func (e *env) newHandler() *devinst.Handler {
	return devinst.NewHandler(devinst.Deps{
		DB: e.db, Clock: e.clk, Audit: e.rec, Notifier: e.notes, Servers: e.servers, Syncer: e.syncer,
		ServerView: func(_ context.Context, id int64) (any, error) { return map[string]any{"id": id}, nil },
		Waker:      e.waker, Console: e.console, DataStates: e.data, Admit: e.admit,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func (e *env) ctx() context.Context { return context.Background() }

func (e *env) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

func (e *env) sudoAdmin() principal.Principal {
	p := e.admin()
	p.SudoUntil = t0.Add(72 * time.Hour)
	return p
}

// prod inserts a server without dev.
func (e *env) prod(name string) int64 { return storetest.CreateServer(e.t, e.db, name) }

// dev inserts prod <name> and its dev dev-<name> (asleep, next sync at fiveAM) and returns both ids.
func (e *env) dev(name string) (prodID, devID int64) {
	e.t.Helper()
	prodID = e.prod(name)
	devID = storetest.CreateServer(e.t, e.db, "dev-"+name)
	if _, err := e.q.DevCreate(e.ctx(), store.DevCreateParams{
		ServerID: devID, SourceID: prodID, SyncTime: 300, IdleMinutes: 15,
		PropertyOverrides: `{"view_distance":4,"simulation_distance":3}`,
		NextSyncAt:        sql.NullInt64{Int64: fiveAM.UnixMilli(), Valid: true},
		CreatedAt:         t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
	return prodID, devID
}

func (e *env) row(devID int64) store.DevInstance {
	e.t.Helper()
	d, err := e.q.DevGet(e.ctx(), devID)
	if err != nil {
		e.t.Fatal(err)
	}
	return d
}

func (e *env) setBaseDomain(d string) {
	e.t.Helper()
	mustExec(e.t, e.db, `UPDATE instance_settings SET base_domain = ? WHERE id = 1`, d)
}

// pass feeds one monitoring pass with the given samples (At = now).
func (e *env) pass(samples ...monitoring.ServerSample) {
	e.h.OnPass(e.ctx(), monitoring.Snapshot{At: e.clk.Now(), Agent: true, Docker: true, Servers: samples})
}

// running is a sample of a running server; players nil = unknown.
func running(id int64, startedAt time.Time, players *int) monitoring.ServerSample {
	up := players != nil
	return monitoring.ServerSample{
		ID: id, DesiredRunning: true, State: agentapi.StateRunning, StartedAt: startedAt.UnixMilli(), Up: &up, Players: players,
	}
}

func stopped(id int64) monitoring.ServerSample {
	return monitoring.ServerSample{ID: id, State: agentapi.StateExited}
}

func ptr[T any](v T) *T { return &v }

func path(id int64, suffix string) string { return fmt.Sprintf("/servers/%d/dev%s", id, suffix) }

// do sends a JSON request with client IP 203.0.113.7 as p (nil = anonymous).
func (e *env) do(method, p string, body any, as *principal.Principal) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(method, p, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if as != nil {
		ctx = principal.With(ctx, *as)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func (e *env) must(want int, method, p string, body any, as principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := e.do(method, p, body, &as)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d %s, want %d", method, p, rec.Code, rec.Body, want)
	}
	return rec
}

// devJSON is the DevInstance schema.
type devJSON struct {
	ServerID           int64   `json:"server_id"`
	SourceID           int64   `json:"source_id"`
	SourceName         string  `json:"source_name"`
	SyncPaused         bool    `json:"sync_paused"`
	PausedAt           *int64  `json:"paused_at"`
	SyncTime           int     `json:"sync_time"`
	IdleMinutes        int     `json:"idle_minutes"`
	ViewDistance       int     `json:"view_distance"`
	SimulationDistance int     `json:"simulation_distance"`
	NextSyncAt         *int64  `json:"next_sync_at"`
	LastSyncAt         *int64  `json:"last_sync_at"`
	LastSyncStatus     *string `json:"last_sync_status"`
	LastSyncError      *string `json:"last_sync_error"`
	SyncPending        bool    `json:"sync_pending"`
	Sleeping           bool    `json:"sleeping"`
}

func (e *env) get(devID int64) devJSON {
	e.t.Helper()
	return decode[devJSON](e.t, e.must(200, "GET", path(devID, ""), nil, e.admin()))
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

// entries returns the audit entries of action.
func (e *env) entries(action string) []audit.Entry {
	var out []audit.Entry
	for _, en := range e.rec.Entries {
		if en.Action == action {
			out = append(out, en)
		}
	}
	return out
}

// fakeServers emulates servers.Handler on the real database.
type fakeServers struct {
	t   *testing.T
	db  *sql.DB
	clk *clock.Fake

	mu       sync.Mutex
	errs     map[string]error // by method: create, apply, overrides, sleep
	created  []int64          // prod ids
	memory   []*int
	applied  []int64
	updated  []int64 // UpdateDevOverrides
	sleeping []sleepCall
}

type sleepCall struct {
	ID       int64
	Sleeping bool
	At       time.Time
}

func (f *fakeServers) CreateDev(ctx context.Context, prodID int64, memoryMiB *int) (store.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, prodID)
	f.memory = append(f.memory, memoryMiB)
	if err := f.errs["create"]; err != nil {
		return store.Server{}, err
	}
	q := store.New(f.db)
	prod, err := q.RbacGetServer(ctx, prodID)
	if err != nil {
		return store.Server{}, err
	}
	devID := storetest.CreateServer(f.t, f.db, "dev-"+prod.Name)
	now := f.clk.Now().UnixMilli()
	if _, err := q.DevCreate(ctx, store.DevCreateParams{
		ServerID: devID, SourceID: prodID, SyncTime: 300, IdleMinutes: 15,
		PropertyOverrides: `{"view_distance":4,"simulation_distance":3}`,
		NextSyncAt:        sql.NullInt64{Int64: fiveAM.UnixMilli(), Valid: true}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return store.Server{}, err
	}
	return q.RbacGetServer(ctx, devID)
}

func (f *fakeServers) ApplyDevSpec(_ context.Context, devID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, devID)
	return f.errs["apply"]
}

func (f *fakeServers) UpdateDevOverrides(_ context.Context, devID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updated = append(f.updated, devID)
	return f.errs["overrides"]
}

func (f *fakeServers) overridesCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updated)
}

func (f *fakeServers) SetDevSleeping(ctx context.Context, devID int64, sleeping bool, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs["sleep"]; err != nil {
		return err
	}
	f.sleeping = append(f.sleeping, sleepCall{devID, sleeping, now})
	var s int64
	var wake sql.NullInt64
	if sleeping {
		s = 1
	} else {
		wake = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
	}
	return store.New(f.db).DevSetSleeping(ctx, store.DevSetSleepingParams{Sleeping: s, LastWakeAt: wake, UpdatedAt: now.UnixMilli(), ServerID: devID})
}

func (f *fakeServers) sleepCalls() []sleepCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sleeping)
}

func (f *fakeServers) appliedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied)
}

// fakeSyncer records the queued syncs; tests end them with done.
type fakeSyncer struct {
	mu    sync.Mutex
	err   error
	syncs []backups.DevSync
}

func (f *fakeSyncer) QueueDevSync(_ context.Context, s backups.DevSync) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.syncs = append(f.syncs, s)
	return nil
}

func (f *fakeSyncer) queued() []backups.DevSync {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.syncs)
}

// last returns the newest queued sync (one must exist).
func (f *fakeSyncer) last(t *testing.T) backups.DevSync {
	t.Helper()
	q := f.queued()
	if len(q) == 0 {
		t.Fatal("no sync queued")
	}
	return q[len(q)-1]
}

// fakeWaker answers PUT /v1/waker: the next wakes once, then none.
type fakeWaker struct {
	mu       sync.Mutex
	err      error
	notReady bool
	wakes    []int64
	reqs     []agentapi.WakerRequest
}

func (f *fakeWaker) Waker(_ context.Context, req agentapi.WakerRequest) (agentapi.WakerResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return agentapi.WakerResponse{}, f.err
	}
	w := f.wakes
	f.wakes = nil
	return agentapi.WakerResponse{Ready: !f.notReady, Wakes: w}, nil
}

func (f *fakeWaker) wake(ids ...int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wakes = append(f.wakes, ids...)
}

func (f *fakeWaker) requests() []agentapi.WakerRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// fakeConsole records RCON commands per server; fail makes every RCON call fail. An "op <name>"
// adds name to the server's ops.json (Players) unless the server cannot resolve it (unknown).
type fakeConsole struct {
	mu   sync.Mutex
	fail bool
	cmds []string // "<id> <command>"

	ops     map[int64][]string
	unknown map[string]bool
}

func (f *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}

func (f *fakeConsole) Rcon(_ context.Context, id int64, cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errors.New("rcon down")
	}
	f.cmds = append(f.cmds, fmt.Sprintf("%d %s", id, cmd))
	if name, ok := strings.CutPrefix(cmd, "op "); ok {
		if f.unknown[name] {
			return "That player does not exist", nil
		}
		if !slices.Contains(f.ops[id], name) {
			if f.ops == nil {
				f.ops = map[int64][]string{}
			}
			f.ops[id] = append(f.ops[id], name)
		}
	}
	return "", nil
}

func (f *fakeConsole) Players(_ context.Context, id int64) (agentapi.PlayersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out agentapi.PlayersResponse
	for _, n := range f.ops[id] {
		out.Ops = append(out.Ops, agentapi.PlayerOp{Name: n, Level: 4})
	}
	return out, nil
}

// setOps replaces the server's ops.json (a sync copies prod's).
func (f *fakeConsole) setOps(id int64, names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ops == nil {
		f.ops = map[int64][]string{}
	}
	f.ops[id] = names
}

func (f *fakeConsole) setUnknown(names ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unknown = map[string]bool{}
	for _, n := range names {
		f.unknown[n] = true
	}
}

func (f *fakeConsole) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cmds)
}

func (f *fakeConsole) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = nil
}

// fakeStates is a servers.DataStates with a state per dev (missing = safe); err, while set,
// makes every dev unknown.
type fakeStates struct {
	mu     sync.Mutex
	states map[int64]servers.State
	err    error
}

func (f *fakeStates) State(ctx context.Context, id int64) (servers.State, error) {
	info, err := f.Lookup(ctx, id, nil)
	return info.State, err
}

func (f *fakeStates) Lookup(_ context.Context, id int64, _ servers.Hold) (servers.DataInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return servers.DataInfo{State: servers.StateUnknown}, f.err
	}
	if s, ok := f.states[id]; ok {
		return servers.DataInfo{State: s}, nil
	}
	return servers.DataInfo{State: servers.StateSafe}, nil
}

func (f *fakeStates) set(id int64, s servers.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id] = s
}

func (f *fakeStates) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}
