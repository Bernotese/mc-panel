package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// t0 is a fixed start for the fake clock (a Tuesday, 12:00 in Europe/Berlin).
var t0 = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

// --- agent -------------------------------------------------------------------------------

type fakeAgent struct {
	mu     sync.Mutex
	resp   agentapi.InstancesResponse
	err    error
	ncalls int
}

var _ agentapi.InstancesClient = (*fakeAgent)(nil)

func newFakeAgent() *fakeAgent {
	return &fakeAgent{resp: agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: []agentapi.InstanceStatus{}}}
}

func (f *fakeAgent) set(st ...agentapi.InstanceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resp.Docker = agentapi.DockerOK
	f.resp.Instances = append([]agentapi.InstanceStatus{}, st...)
}

func (f *fakeAgent) setDocker(state string) { f.mu.Lock(); f.resp.Docker = state; f.mu.Unlock() }
func (f *fakeAgent) setErr(err error)       { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) calls() int             { f.mu.Lock(); defer f.mu.Unlock(); return f.ncalls }

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ncalls++
	if f.err != nil {
		return agentapi.InstancesResponse{}, f.err
	}
	resp := f.resp
	resp.Instances = append([]agentapi.InstanceStatus{}, f.resp.Instances...)
	return resp, nil
}

func (f *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error     { return nil }
func (f *fakeAgent) Power(context.Context, int64, agentapi.PowerAction) error { return nil }
func (f *fakeAgent) Remove(context.Context, int64) error                      { return nil }

// --- RCON --------------------------------------------------------------------------------

type rconReply struct {
	out string
	err error
}

type fakeConsole struct {
	mu      sync.Mutex
	replies map[string]rconReply
	calls   []string
	delay   time.Duration
	active  map[int64]int
	maxAct  map[int64]int
}

var _ agentapi.ConsoleClient = (*fakeConsole)(nil)

func newFakeConsole() *fakeConsole {
	return &fakeConsole{replies: map[string]rconReply{}, active: map[int64]int{}, maxAct: map[int64]int{}}
}

func (f *fakeConsole) peak(id int64) int { f.mu.Lock(); defer f.mu.Unlock(); return f.maxAct[id] }

func (f *fakeConsole) reply(id int64, cmd, out string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[fmt.Sprintf("%d %s", id, cmd)] = rconReply{out: out, err: err}
}

func (f *fakeConsole) setDelay(d time.Duration) { f.mu.Lock(); f.delay = d; f.mu.Unlock() }

func (f *fakeConsole) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *fakeConsole) Rcon(ctx context.Context, id int64, command string) (string, error) {
	key := fmt.Sprintf("%d %s", id, command)
	f.mu.Lock()
	f.calls = append(f.calls, key)
	r, ok := f.replies[key]
	d := f.delay
	f.active[id]++
	f.maxAct[id] = max(f.maxAct[id], f.active[id])
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active[id]--; f.mu.Unlock() }()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if !ok {
		return "", agentapi.ErrRconUnavailable
	}
	return r.out, r.err
}

func (f *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, errors.New("fakeConsole: no logs")
}

func (f *fakeConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{Ops: []agentapi.PlayerOp{}, Whitelist: []agentapi.PlayerEntry{}, Bans: []agentapi.PlayerBan{}, Errors: []string{}}, nil
}

// --- Server List Ping --------------------------------------------------------------------

type pingAnswer struct {
	json string
	raw  []byte
	hang bool
	drop bool
}

type pingServer struct {
	ln      net.Listener
	mu      sync.Mutex
	answers map[string]pingAnswer
	nhits   map[string]int
}

func newPingServer(t *testing.T) *pingServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &pingServer{ln: ln, answers: map[string]pingAnswer{}, nhits: map[string]int{}}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); p.serve(c, done) }()
		}
	}()
	t.Cleanup(func() { close(done); _ = ln.Close(); wg.Wait() })
	return p
}

func (p *pingServer) addr() string { return p.ln.Addr().String() }

func (p *pingServer) set(fqdn string, a pingAnswer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[fqdn] = a
}

func (p *pingServer) hits(fqdn string) int { p.mu.Lock(); defer p.mu.Unlock(); return p.nhits[fqdn] }

// serve reads handshake (id 0, protocol, address, port, next state 1) and status request
// (id 0), then answers as configured for the handshake address.
func (p *pingServer) serve(c net.Conn, done <-chan struct{}) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	hs, err := readTestPacket(r)
	if err != nil {
		return
	}
	hr := bytes.NewReader(hs)
	if id, _ := binary.ReadUvarint(hr); id != 0 {
		return
	}
	_, _ = binary.ReadUvarint(hr) // protocol version
	n, err := binary.ReadUvarint(hr)
	if err != nil || n > 255 {
		return
	}
	host := make([]byte, n)
	if _, err := io.ReadFull(hr, host); err != nil {
		return
	}
	fqdn := string(host)
	p.mu.Lock()
	a, ok := p.answers[fqdn]
	p.nhits[fqdn]++
	p.mu.Unlock()
	if !ok || a.drop {
		return
	}
	if _, err := readTestPacket(r); err != nil { // status request
		return
	}
	switch {
	case a.hang:
		<-done
	case a.raw != nil:
		_, _ = c.Write(a.raw)
	default:
		var body bytes.Buffer
		body.Write(binary.AppendUvarint(nil, 0))
		body.Write(binary.AppendUvarint(nil, uint64(len(a.json))))
		body.WriteString(a.json)
		_, _ = c.Write(append(binary.AppendUvarint(nil, uint64(body.Len())), body.Bytes()...))
	}
}

func readTestPacket(r *bufio.Reader) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil || n > 1<<16 {
		return nil, fmt.Errorf("bad packet length: %w", err)
	}
	b := make([]byte, n)
	_, err = io.ReadFull(r, b)
	return b, err
}

// statusJSON is a minimal valid status answer.
func statusJSON(online, maxPlayers int, names ...string) string {
	sample := make([]map[string]string, 0, len(names))
	for i, n := range names {
		sample = append(sample, map[string]string{"name": n, "id": fmt.Sprintf("00000000-0000-0000-0000-%012d", i)})
	}
	b, _ := json.Marshal(map[string]any{
		"version":     map[string]any{"name": "1.21.11", "protocol": 774},
		"players":     map[string]any{"online": online, "max": maxPlayers, "sample": sample},
		"description": map[string]any{"text": "A Minecraft Server"},
	})
	return string(b)
}

// --- HTTP env ----------------------------------------------------------------------------

type env struct {
	t       *testing.T
	db      *sql.DB
	clk     *clock.Fake
	rec     *audit.MemoryRecorder
	agent   *fakeAgent
	console *fakeConsole
	notes   *notify.Recording
	h       *Handler
	mux     *chi.Mux
	rt      *rbac.Router
	rootID  int64
}

// newEnv builds the handler on a real DB and rbac router without starting Run.
func newEnv(t *testing.T, opts ...func(*Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, agent: newFakeAgent(),
		console: newFakeConsole(), notes: &notify.Recording{},
	}
	d := Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Notifier: e.notes, Instances: e.agent, Console: e.console,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(&d)
	}
	e.h = NewHandler(d)
	e.mux = chi.NewRouter()
	e.rt = rbac.NewRouter(e.mux, rbac.NewDBChecker(db), e.clk)
	e.h.Routes(e.rt)
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

func (e *env) admin() principal.Principal {
	p := e.adminNoSudo()
	p.SudoUntil = t0.Add(5 * time.Minute)
	return p
}

func (e *env) adminNoSudo() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull}
}

// user creates a user with the given per-server permissions.
func (e *env) user(name string, perms map[int64][]string) principal.Principal {
	e.t.Helper()
	id := storetest.CreateUser(e.t, e.db, name, "user")
	for sid, ps := range perms {
		for _, p := range ps {
			if _, err := e.db.Exec("INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)", id, sid, p); err != nil {
				e.t.Fatal(err)
			}
		}
	}
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// server creates a server with an instance (paper 1.21.11, desired_state running).
func (e *env) server(name string) int64 {
	e.t.Helper()
	id := storetest.CreateServer(e.t, e.db, name)
	if _, err := store.New(e.db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: "paper", Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

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

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakes compile-check (also keeps unused-helper lint quiet until B1/B2 use them).
// Every helper B1/B2 rely on is listed, so the unused linter stays quiet in Phase A.
var _ = []any{newPingServer, statusJSON, eventually,
	(*fakeAgent).set, (*fakeAgent).setDocker, (*fakeAgent).setErr, (*fakeAgent).calls,
	(*fakeConsole).reply, (*fakeConsole).setDelay, (*fakeConsole).commands, (*fakeConsole).peak,
	(*pingServer).addr, (*pingServer).set, (*pingServer).hits}
