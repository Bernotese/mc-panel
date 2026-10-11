package agent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

type stubConsoleConfig struct {
	running    bool // passed to start
	lines      []agentapi.LogLine
	logsErr    error         // returned instead of opening the stream
	pause      time.Duration // before the second line
	block      bool          // after the lines: wait until ctx ends
	hold       time.Duration // after the lines: wait this long, then end cleanly
	afterErr   error         // after the lines: fail with this error
	skipStart  bool          // never call start (a broken backend)
	waitStart  bool          // before start: wait until ctx ends
	rconOut    string
	rconErr    error
	rconDelay  time.Duration
	players    agentapi.PlayersResponse
	playersErr error
}

// stubConsole is a configurable Console; set changes it between requests.
type stubConsole struct {
	mu    sync.Mutex
	cfg   stubConsoleConfig
	calls []string
	reqs  []agentapi.LogsRequest
}

var _ agent.Console = (*stubConsole)(nil)

func newStubConsole(cfg stubConsoleConfig) *stubConsole { return &stubConsole{cfg: cfg} }

func (s *stubConsole) set(f func(*stubConsoleConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.cfg)
}

func (s *stubConsole) enter(call string) stubConsoleConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	return s.cfg
}

func (s *stubConsole) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubConsole) logsRequests() []agentapi.LogsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentapi.LogsRequest(nil), s.reqs...)
}

func waitCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *stubConsole) Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error {
	cfg := s.enter("logs")
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	if cfg.logsErr != nil {
		return cfg.logsErr
	}
	if cfg.waitStart {
		<-ctx.Done()
		return ctx.Err()
	}
	if !cfg.skipStart {
		start(cfg.running)
	}
	for i, l := range cfg.lines {
		if i == 1 && cfg.pause > 0 {
			if err := waitCtx(ctx, cfg.pause); err != nil {
				return err
			}
		}
		if err := emit(l); err != nil {
			return err
		}
	}
	if cfg.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if cfg.hold > 0 {
		if err := waitCtx(ctx, cfg.hold); err != nil {
			return err
		}
	}
	return cfg.afterErr
}

func (s *stubConsole) Rcon(ctx context.Context, _ int64, command string) (string, error) {
	cfg := s.enter("rcon " + command)
	if cfg.rconDelay > 0 {
		if err := waitCtx(ctx, cfg.rconDelay); err != nil {
			return "", err
		}
	}
	return cfg.rconOut, cfg.rconErr
}

func (s *stubConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	cfg := s.enter("players")
	return cfg.players, cfg.playersErr
}

func newConsoleServer(c agent.Console) *agent.Server {
	fw, _ := newNoop()
	return agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithConsole(c))
}

// startConsoleServer serves on a real Unix socket (deadlines, background reads, shutdown).
func startConsoleServer(t *testing.T, c agent.Console, opts ...agent.Option) (string, *agent.Server) {
	t.Helper()
	return startConsoleServerLog(t, c, io.Discard, opts...)
}

// startConsoleServerLog is startConsoleServer with the server log written to log.
func startConsoleServerLog(t *testing.T, c agent.Console, log io.Writer, opts ...agent.Option) (string, *agent.Server) {
	t.Helper()
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(log), append([]agent.Option{agent.WithConsole(c)}, opts...)...)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return path, srv
}

func unixHTTPClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
}

func postLogs(ctx context.Context, hc *http.Client, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+agentapi.PathInstanceLogs, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", jsonCT)
	return hc.Do(req)
}

func ndjson(t *testing.T, lines ...agentapi.LogLine) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestServerConsoleEndpoints(t *testing.T) {
	lines := []agentapi.LogLine{
		{TS: 1760097600000000000, Stream: agentapi.StreamOut, Line: "[12:00:00 INFO]: Done (3.2s)!"},
		{TS: 1760097601000000000, Stream: agentapi.StreamErr, Line: "\x1b[33mWARN\x1b[0m slow"},
	}
	players := agentapi.PlayersResponse{
		Ops:       []agentapi.PlayerOp{{UUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5", Name: "Notch", Level: 4}},
		Whitelist: []agentapi.PlayerEntry{},
		Bans: []agentapi.PlayerBan{{UUID: "u2", Name: "Griefer", Reason: "x", Source: "Server",
			Created: "2026-10-11 09:32:51 +0000", Expires: "forever"}},
		Errors: []string{},
	}
	st := newStubConsole(stubConsoleConfig{running: true, lines: lines, rconOut: "There are 0 of a max of 20 players online: ", players: players})
	srv := newConsoleServer(st)

	rec := serve(srv, http.MethodPost, agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":2,"since":0}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" ||
		rec.Header().Get(agentapi.HeaderContainerRunning) != "true" || rec.Body.String() != ndjson(t, lines...) {
		t.Fatalf("logs: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if reqs := st.logsRequests(); len(reqs) != 1 || reqs[0] != (agentapi.LogsRequest{ID: 1, Tail: 2}) {
		t.Fatalf("logs requests = %+v", reqs)
	}
	rec = serve(srv, http.MethodPost, agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"list"}`)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"output":"There are 0 of a max of 20 players online: "}`+"\n" {
		t.Fatalf("rcon: %d %s", rec.Code, rec.Body)
	}
	rec = serve(srv, http.MethodPost, agentapi.PathInstancePlayers, jsonCT, `{"id":1}`)
	want := `{"ops":[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch","level":4}],"whitelist":[],` +
		`"bans":[{"uuid":"u2","name":"Griefer","reason":"x","source":"Server","created":"2026-10-11 09:32:51 +0000","expires":"forever"}],"errors":[]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("players: %d %s", rec.Code, rec.Body)
	}
}

func TestServerLogsOfStoppedContainer(t *testing.T) {
	// A stopped container: start(false), no lines, the stream ends by itself.
	rec := serve(newConsoleServer(newStubConsole(stubConsoleConfig{})), http.MethodPost, agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":0,"since":0}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" ||
		rec.Header().Get(agentapi.HeaderContainerRunning) != "false" || rec.Body.Len() != 0 {
		t.Fatalf("empty log: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
}

func TestServerRejectsInvalidConsoleRequests(t *testing.T) {
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"logs tail -1", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":-1,"since":0}`, 400},
		{"logs tail 1001", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":1001,"since":0}`, 400},
		{"logs since -1", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":-1}`, 400},
		{"logs id 0", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":0,"tail":10,"since":0}`, 400},
		{"logs unknown field", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":0,"follow":false}`, 400},
		{"logs trailing data", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":0}{}`, 400},
		{"logs wrong content type", "POST", agentapi.PathInstanceLogs, "text/plain", `{"id":1,"tail":10,"since":0}`, 415},
		{"logs GET", "GET", agentapi.PathInstanceLogs, "", "", 405},
		{"rcon empty", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":""}`, 400},
		{"rcon spaces", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"   "}`, 400},
		{"rcon newline", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say a\nop evil"}`, 400},
		{"rcon carriage return", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say a\rop evil"}`, 400},
		{"rcon tab", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say\ta"}`, 400},
		{"rcon nul", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say \u0000"}`, 400},
		{"rcon del", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say \u007f"}`, 400},
		{"rcon 1001 bytes", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"` + strings.Repeat("a", 1001) + `"}`, 400},
		{"rcon id 0", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":0,"command":"list"}`, 400},
		{"rcon unknown field", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"list","user":"root"}`, 400},
		{"rcon too large", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"` + strings.Repeat("a", 17<<10) + `"}`, 413},
		{"players id 0", "POST", agentapi.PathInstancePlayers, jsonCT, `{"id":0}`, 400},
		{"players negative", "POST", agentapi.PathInstancePlayers, jsonCT, `{"id":-3}`, 400},
		{"players unknown field", "POST", agentapi.PathInstancePlayers, jsonCT, `{"id":1,"file":"/etc/shadow"}`, 400},
	}
	st := newStubConsole(stubConsoleConfig{})
	srv := newConsoleServer(st)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(srv, tc.method, tc.path, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			var er agentapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || er.Error == "" {
				t.Fatalf("error body = %s (%v)", rec.Body, err)
			}
		})
	}
	if n := st.callCount(); n != 0 {
		t.Fatalf("invalid requests reached the console backend (%d calls)", n)
	}
}

func TestServerConsoleErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: container mcpanel-mc-1 does not exist", agentapi.ErrNotFound), http.StatusNotFound, agentapi.ErrorCodeNotFound},
		{agentapi.ErrNotRunning, http.StatusConflict, agentapi.ErrorCodeNotRunning},
		{agentapi.ErrBusy, http.StatusTooManyRequests, agentapi.ErrorCodeBusy},
		{fmt.Errorf("%w: Failed to connect to RCON", agentapi.ErrRconUnavailable), http.StatusServiceUnavailable, agentapi.ErrorCodeRconUnavailable},
		{agentapi.ErrDockerUnavailable, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable},
		{fmt.Errorf("%w: id", instancespec.ErrInvalid), http.StatusBadRequest, ""},
		{errors.New("disk on fire at /srv/mc-panel"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		st := newStubConsole(stubConsoleConfig{logsErr: tc.err, rconErr: tc.err, playersErr: tc.err})
		srv := newConsoleServer(st)
		for _, ep := range []struct{ path, body string }{
			{agentapi.PathInstanceLogs, `{"id":1,"tail":10,"since":0}`},
			{agentapi.PathInstanceRcon, `{"id":1,"command":"list"}`},
			{agentapi.PathInstancePlayers, `{"id":1}`},
		} {
			rec := serve(srv, http.MethodPost, ep.path, jsonCT, ep.body)
			var er agentapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != tc.status || er.Code != tc.code {
				t.Errorf("%s with %v: %d %s", ep.path, tc.err, rec.Code, rec.Body)
			}
			if tc.status == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "/srv") {
				t.Errorf("%s: internal error leaked: %s", ep.path, rec.Body)
			}
			if tc.code == agentapi.ErrorCodeRconUnavailable && !strings.Contains(er.Error, "Failed to connect") {
				t.Errorf("%s: rcon output missing in %q", ep.path, er.Error)
			}
		}
	}
}

func TestServerWithoutConsoleBackend(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	for _, ep := range []struct{ path, body string }{
		{agentapi.PathInstanceLogs, `{"id":1,"tail":10,"since":0}`},
		{agentapi.PathInstanceRcon, `{"id":1,"command":"list"}`},
		{agentapi.PathInstancePlayers, `{"id":1}`},
	} {
		rec := serve(srv, http.MethodPost, ep.path, jsonCT, ep.body)
		var er agentapi.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != http.StatusServiceUnavailable || er.Code != agentapi.ErrorCodeDockerUnavailable {
			t.Errorf("%s without backend: %d %s", ep.path, rec.Code, rec.Body)
		}
	}
}

func TestServerLogStreamLimit(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{running: true, lines: []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "hi"}}, block: true})
	path, _ := startConsoleServer(t, st)
	hc := unixHTTPClient(path)
	const body = `{"id":1,"tail":10,"since":0}`
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, c := range cancels {
			c()
		}
	})
	for i := range agent.MaxLogStreams {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		resp, err := postLogs(ctx, hc, body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: %v %v", i, resp, err)
		}
		if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
			t.Fatalf("stream %d: first line: %v", i, err)
		}
	}
	resp, err := postLogs(context.Background(), hc, body)
	if err != nil {
		t.Fatal(err)
	}
	var er agentapi.ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&er)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || er.Code != agentapi.ErrorCodeBusy {
		t.Fatalf("stream %d: %d %+v, want 429 busy", agent.MaxLogStreams+1, resp.StatusCode, er)
	}

	cancels[0]() // the panel went away: its slot must be freed
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithCancel(context.Background())
		resp, err := postLogs(ctx, hc, body)
		ok := err == nil && resp.StatusCode == http.StatusOK
		if resp != nil {
			_ = resp.Body.Close()
		}
		cancel()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slot of a cancelled stream was not released")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestServerLongRequestsOutliveReadTimeout(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{
		running: true,
		lines:   []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "one"}, {TS: 2, Stream: agentapi.StreamOut, Line: "two"}},
		pause:   600 * time.Millisecond, rconOut: "pong", rconDelay: 600 * time.Millisecond,
	})
	path, _ := startConsoleServer(t, st, agent.WithReadTimeout(200*time.Millisecond))
	c := agent.NewClient(path)
	ctx := context.Background()
	stream, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Running() {
		t.Fatal("X-Container-Running did not reach the client")
	}
	for _, want := range []string{"one", "two"} {
		l, err := stream.Next()
		if err != nil || l.Line != want {
			t.Fatalf("line = %+v, %v; want %q (the stream must outlive ReadTimeout)", l, err, want)
		}
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream = %v, want io.EOF", err)
	}
	if out, err := c.Rcon(ctx, 1, "ping"); err != nil || out != "pong" {
		t.Fatalf("slow rcon = %q, %v (must outlive ReadTimeout)", out, err)
	}
}

func TestServerShutdownEndsLogStreams(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{lines: []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "hi"}}, block: true})
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := newConsoleServer(st)
	go func() { _ = srv.Serve(l) }()
	stream, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1, Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with an open log stream = %v", err)
	}
	// A stream ended by shutdown is broken, not a clean end: io.EOF means "container
	// stopped" to the panel.
	if _, err := stream.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("log stream after shutdown: Next = %v, want a broken stream (not io.EOF)", err)
	}
}

// TestServerLogStreamEndsCleanlyAfterSilence: the closing chunk is written after a long
// silence (longer than the per-write deadline); the stream must still end with io.EOF.
func TestServerLogStreamEndsCleanlyAfterSilence(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{
		running: true, lines: []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "one"}}, hold: 600 * time.Millisecond,
	})
	path, _ := startConsoleServer(t, st, agent.WithStreamWriteTimeout(200*time.Millisecond))
	stream, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1, Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if l, err := stream.Next(); err != nil || l.Line != "one" {
		t.Fatalf("first line = %+v, %v", l, err)
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end after silence = %v, want io.EOF", err)
	}
}

// TestServerLogStreamBackendErrorBreaksStream: a backend error after the first line must
// not look like a clean end (io.EOF) to the client.
func TestServerLogStreamBackendErrorBreaksStream(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{
		running: true, lines: []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "one"}}, afterErr: errors.New("docker connection reset"),
	})
	path, _ := startConsoleServer(t, st)
	stream, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1, Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if l, err := stream.Next(); err != nil || l.Line != "one" {
		t.Fatalf("first line = %+v, %v", l, err)
	}
	if _, err := stream.Next(); err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("after a backend error: Next = %v, want a broken stream (not io.EOF)", err)
	}
}

// TestServerEmitBeforeStart: a backend that emits without calling start still produces a
// valid stream (headers first, container reported as not running).
func TestServerEmitBeforeStart(t *testing.T) {
	line := agentapi.LogLine{TS: 1, Stream: agentapi.StreamOut, Line: "early"}
	st := newStubConsole(stubConsoleConfig{running: true, skipStart: true, lines: []agentapi.LogLine{line}})
	rec := serve(newConsoleServer(st), http.MethodPost, agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":0}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" ||
		rec.Header().Get(agentapi.HeaderContainerRunning) != "false" || rec.Body.String() != ndjson(t, line) {
		t.Fatalf("emit before start: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
}

// TestServerCallerAbortIsNoError: a panel that goes away (before the log stream opened,
// or during a console command) is no server error.
func TestServerCallerAbortIsNoError(t *testing.T) {
	var logs syncBuffer
	st := newStubConsole(stubConsoleConfig{waitStart: true, rconDelay: 5 * time.Second})
	path, _ := startConsoleServerLog(t, st, &logs)
	c := agent.NewClient(path)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}); err == nil {
		t.Fatal("Logs succeeded although the caller gave up")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if _, err := c.Rcon(ctx2, 1, "list"); err == nil {
		t.Fatal("Rcon succeeded although the caller gave up")
	}
	deadline := time.Now().Add(5 * time.Second)
	for strings.Count(logs.String(), "console request ended by the caller") < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("aborts not logged as caller aborts:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(logs.String(), "level=ERROR") || strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("caller abort logged as a failure:\n%s", logs.String())
	}
}

func TestClientConsoleEndToEnd(t *testing.T) {
	players := agentapi.PlayersResponse{
		Ops:       []agentapi.PlayerOp{{UUID: "u1", Name: "Notch", Level: 4}},
		Whitelist: []agentapi.PlayerEntry{}, Bans: []agentapi.PlayerBan{}, Errors: []string{"whitelist.json"},
	}
	st := newStubConsole(stubConsoleConfig{players: players})
	path, _ := startConsoleServer(t, st)
	c := agent.NewClient(path)
	ctx := context.Background()
	if got, err := c.Players(ctx, 1); err != nil || !reflect.DeepEqual(got, players) {
		t.Fatalf("Players = %+v, %v", got, err)
	}
	for _, tc := range []struct{ err, want error }{
		{agentapi.ErrNotFound, agentapi.ErrNotFound},
		{agentapi.ErrNotRunning, agentapi.ErrNotRunning},
		{fmt.Errorf("%w: connection refused", agentapi.ErrRconUnavailable), agentapi.ErrRconUnavailable},
		{agentapi.ErrBusy, agentapi.ErrBusy},
		{agentapi.ErrDockerUnavailable, agentapi.ErrDockerUnavailable},
	} {
		st.set(func(c *stubConsoleConfig) { c.rconErr = tc.err })
		if _, err := c.Rcon(ctx, 1, "list"); !errors.Is(err, tc.want) {
			t.Errorf("Rcon with %v = %v, want %v through the real client", tc.err, err, tc.want)
		}
	}
	st.set(func(c *stubConsoleConfig) { c.logsErr = agentapi.ErrNotFound })
	if _, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("Logs of a missing container = %v, want ErrNotFound before any line", err)
	}
}
