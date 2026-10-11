package console_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// streamServer serves e.mux over real HTTP (streams need flushing); the principal comes from
// the X-Test-User header and e.principals.
func (e *env) streamServer(configure func(*http.Server)) *httptest.Server {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, _ := strconv.ParseInt(r.Header.Get("X-Test-User"), 10, 64)
		if p, ok := e.principals.Load(id); ok {
			ctx = principal.With(ctx, p.(principal.Principal))
		}
		e.mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	if configure != nil {
		configure(srv.Config)
	}
	srv.Start()
	e.t.Cleanup(srv.Close)
	return srv
}

type sseEvent struct{ Event, ID, Data, Comment string }

type sseConn struct {
	t      *testing.T
	resp   *http.Response
	body   []byte // non-200 answers
	cancel context.CancelFunc
	events chan sseEvent
}

func (e *env) openStream(srv *httptest.Server, p principal.Principal, id int64, query, lastEventID string) *sseConn {
	e.t.Helper()
	e.principals.Store(p.UserID, p)
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+srvPath(id, "/console/stream")+query, nil)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", strconv.FormatInt(p.UserID, 10))
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	c := &sseConn{t: e.t, resp: resp, cancel: cancel, events: make(chan sseEvent, 256)}
	e.t.Cleanup(c.close)
	if resp.StatusCode == http.StatusOK {
		go c.read()
	} else {
		c.body, _ = io.ReadAll(resp.Body)
		close(c.events)
	}
	return c
}

func (c *sseConn) read() {
	defer close(c.events)
	sc := bufio.NewScanner(c.resp.Body)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev != (sseEvent{}) {
				c.events <- ev
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, ":"):
			ev.Comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "event: "):
			ev.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (c *sseConn) close() {
	c.cancel()
	_ = c.resp.Body.Close()
}

func (c *sseConn) next() sseEvent {
	c.t.Helper()
	select {
	case ev, ok := <-c.events:
		if !ok {
			c.t.Fatal("stream ended unexpectedly")
		}
		return ev
	case <-time.After(5 * time.Second):
		c.t.Fatal("no event within 5 s")
	}
	return sseEvent{}
}

// expectClosed waits until the panel ends the stream (events before that are ignored).
func (c *sseConn) expectClosed() {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.events:
			if !ok {
				return
			}
		case <-deadline:
			c.t.Fatal("stream still open after 5 s")
		}
	}
}

func (c *sseConn) errCode() string {
	var b httpx.ErrorBody
	_ = json.Unmarshal(c.body, &b)
	return b.Error
}

func expectState(t *testing.T, ev sseEvent, running bool) {
	t.Helper()
	if want := fmt.Sprintf(`{"running":%t}`, running); ev.Event != "state" || ev.Data != want {
		t.Fatalf("event = %+v, want state %s", ev, want)
	}
}

func TestStreamHeadersAndLines(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	if c.resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", c.resp.StatusCode, c.body)
	}
	h := c.resp.Header
	if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-store" || h.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers = %v", h)
	}
	expectState(t, c.next(), true) // right after opening, from Running()
	s.send(t, agentapi.LogLine{TS: 1_760_184_000_123_456_789, Stream: agentapi.StreamOut, Line: `[12:00:00 INFO]: Done (3.1s)! "quoted"`})
	s.send(t, agentapi.LogLine{TS: 1_760_184_000_223_456_789, Stream: agentapi.StreamErr, Line: "warn"})
	ev := c.next()
	if ev.Event != "line" || ev.ID != "1760184000123456789" ||
		ev.Data != `{"ts":1760184000123,"stream":"out","line":"[12:00:00 INFO]: Done (3.1s)! \"quoted\""}` {
		t.Fatalf("line event = %+v", ev)
	}
	if ev := c.next(); ev.ID != "1760184000223456789" || !strings.Contains(ev.Data, `"stream":"err"`) {
		t.Fatalf("second line = %+v", ev)
	}
	if reqs := e.agent.requests(); len(reqs) != 1 || reqs[0] != (agentapi.LogsRequest{ID: id, Tail: 500}) {
		t.Fatalf("agent requests = %+v", reqs)
	}
}

func TestStreamLastEventID(t *testing.T) {
	for _, tc := range []struct {
		query, lastID string
		want          agentapi.LogsRequest
	}{
		{"", "42", agentapi.LogsRequest{Tail: 500, Since: 43}},
		{"?tail=20", "", agentapi.LogsRequest{Tail: 20}},
		{"?tail=0", "", agentapi.LogsRequest{Tail: 0}},
		{"", "abc", agentapi.LogsRequest{Tail: 500}},
		{"", "-5", agentapi.LogsRequest{Tail: 500}},
		{"", "0", agentapi.LogsRequest{Tail: 500}},
	} {
		e := newEnv(t)
		id := createInstance(t, e.db, "survival")
		srv := e.streamServer(nil)
		e.agent.prepare()
		c := e.openStream(srv, e.admin(), id, tc.query, tc.lastID)
		waitFor(t, "agent request", func() bool { return len(e.agent.requests()) == 1 })
		tc.want.ID = id
		if got := e.agent.requests()[0]; got != tc.want {
			t.Errorf("query %q Last-Event-ID %q: request %+v, want %+v", tc.query, tc.lastID, got, tc.want)
		}
		c.close()
	}
}

func TestStreamRejectsBadRequests(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	stub := storetest.CreateServer(t, e.db, "stub")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view")
	admin, vp := e.admin(), userP(viewer, "viewer")
	for _, tc := range []struct {
		path   string
		p      *principal.Principal
		status int
		code   string
	}{
		{srvPath(id, "/console/stream?tail=1001"), &admin, 400, httpx.CodeValidation},
		{srvPath(id, "/console/stream?tail=-1"), &admin, 400, httpx.CodeValidation},
		{srvPath(id, "/console/stream?tail=x"), &admin, 400, httpx.CodeValidation},
		{srvPath(stub, "/console/stream"), &admin, 404, httpx.CodeNotFound},
		{srvPath(id, "/console/stream"), &vp, 403, httpx.CodeForbidden},
		{srvPath(id, "/console/stream"), nil, 401, httpx.CodeUnauthorized},
	} {
		rec := e.do(http.MethodGet, tc.path, nil, tc.p)
		if rec.Code != tc.status || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
	if reqs := e.agent.requests(); len(reqs) != 0 {
		t.Fatalf("agent asked: %+v", reqs)
	}
}

// Review Focus #4: state events, reconnect with since, no duplicate lines.
func TestStreamRunningStateAndReconnect(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s1 := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")

	expectState(t, c.next(), true)
	s1.send(t, agentapi.LogLine{TS: 100, Stream: agentapi.StreamOut, Line: "a"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "100" {
		t.Fatalf("first line = %+v", ev)
	}
	s1.finish() // container stopped
	expectState(t, c.next(), false)
	s1.waitClosed(t)

	s2 := e.agent.prepare()
	e.timers.fire(t, 3*time.Second)
	expectState(t, c.next(), true)                                               // running again
	s2.send(t, agentapi.LogLine{TS: 100, Stream: agentapi.StreamOut, Line: "a"}) // repeated by the agent: dropped
	s2.send(t, agentapi.LogLine{TS: 101, Stream: agentapi.StreamOut, Line: "b"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "101" {
		t.Fatalf("after reconnect = %+v", ev)
	}
	if reqs := e.agent.requests(); len(reqs) != 2 || reqs[1] != (agentapi.LogsRequest{ID: id, Tail: 500, Since: 101}) {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A stopped container: the agent delivers the history and ends at once. No "running" flapping.
func TestStreamStoppedServerDoesNotFlap(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s1 := e.agent.prepareStopped()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), false)
	s1.send(t, agentapi.LogLine{TS: 5, Stream: agentapi.StreamOut, Line: "old"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "5" {
		t.Fatalf("history line = %+v", ev)
	}
	s1.finish() // still false: no event
	for range 3 {
		s := e.agent.prepareStopped()
		e.timers.fire(t, 3*time.Second)
		s.finish()
		s.waitClosed(t)
	}
	e.timers.fire(t, 15*time.Second)
	if ev := c.next(); ev.Comment != "ping" {
		t.Fatalf("expected only a ping after the reconnects, got %+v", ev)
	}
}

func TestStreamAgentDownRetries(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	e.agent.setLogsErr(fmt.Errorf("%w: dial", agentapi.ErrUnavailable))
	c := e.openStream(srv, e.admin(), id, "", "")
	if c.resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", c.resp.StatusCode)
	}
	expectState(t, c.next(), false)
	e.timers.fire(t, 3*time.Second)
	waitFor(t, "second attempt", func() bool { return len(e.agent.requests()) == 2 })
	e.agent.setLogsErr(nil)
	s := e.agent.prepare()
	e.timers.fire(t, 3*time.Second)
	expectState(t, c.next(), true) // no second state:false before it
	s.send(t, agentapi.LogLine{TS: 7, Stream: agentapi.StreamOut, Line: "back"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "7" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestStreamHeartbeat(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	e.timers.fire(t, 15*time.Second)
	if ev := c.next(); ev.Comment != "ping" {
		t.Fatalf("event = %+v, want ping", ev)
	}
}

// Review Focus #3: permission revoked, session ended.
func TestStreamRecheckClosesStream(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	e.timers.fire(t, 30*time.Second) // allowed: stays open
	e.timers.fire(t, 15*time.Second)
	if ev := c.next(); ev.Comment != "ping" {
		t.Fatalf("event = %+v", ev)
	}
	e.access.set(console.ErrAccessRevoked)
	e.timers.fire(t, 30*time.Second)
	c.expectClosed()
	s.waitClosed(t)
	e.access.mu.Lock()
	defer e.access.mu.Unlock()
	if len(e.access.perms) != 2 || e.access.perms[0] != "server.console" {
		t.Fatalf("recheck perms = %v", e.access.perms)
	}
}

func TestStreamClosesOnDeletedServer(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	e.agent.setLogsErr(fmt.Errorf("%w: dial", agentapi.ErrUnavailable))
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), false)
	mustExec(t, e.db, `DELETE FROM servers WHERE id = ?`, id)
	e.timers.fire(t, 30*time.Second) // while waiting to reconnect
	c.expectClosed()
}

func TestStreamClientGoneClosesAgentStream(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	s.send(t, agentapi.LogLine{TS: 1, Stream: agentapi.StreamOut, Line: "x"})
	if ev := c.next(); ev.Event != "line" {
		t.Fatalf("event = %+v", ev)
	}
	c.close()
	s.waitClosed(t)
}

func TestStreamLimitPerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	op := storetest.CreateUser(t, e.db, "op", "user")
	grant(t, e.db, op, id, "server.view", "server.console")
	opP := userP(op, "op")
	var open []*sseConn
	for i := range 4 {
		c := e.openStream(srv, opP, id, "", "")
		if c.resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: %d", i+1, c.resp.StatusCode)
		}
		open = append(open, c)
	}
	c5 := e.openStream(srv, opP, id, "", "")
	if c5.resp.StatusCode != http.StatusTooManyRequests || c5.errCode() != httpx.CodeRateLimited {
		t.Fatalf("5th stream: %d %s", c5.resp.StatusCode, c5.body)
	}
	if c := e.openStream(srv, e.admin(), id, "", ""); c.resp.StatusCode != http.StatusOK {
		t.Fatalf("other user: %d", c.resp.StatusCode)
	}
	open[0].close()
	waitFor(t, "a free slot after closing a stream", func() bool {
		c := e.openStream(srv, opP, id, "", "")
		defer c.close()
		return c.resp.StatusCode == http.StatusOK
	})
}

func TestShutdownClosesStreams(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c1 := e.openStream(srv, e.admin(), id, "", "")
	c2 := e.openStream(srv, e.admin(), id, "", "")
	waitFor(t, "both streams asked the agent", func() bool { return len(e.agent.requests()) == 2 })
	e.h.Shutdown()
	c1.expectClosed()
	c2.expectClosed()
	s.waitClosed(t)
	c3 := e.openStream(srv, e.admin(), id, "", "")
	if c3.resp.StatusCode != http.StatusServiceUnavailable || c3.errCode() != httpx.CodeAgentUnavailable {
		t.Fatalf("after shutdown: %d %s", c3.resp.StatusCode, c3.body)
	}
}

// The panel's http.Server has ReadTimeout 30 s and WriteTimeout 60 s; a stream must outlive both.
func TestStreamSurvivesServerTimeouts(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(func(s *http.Server) {
		s.ReadTimeout = 100 * time.Millisecond
		s.WriteTimeout = 100 * time.Millisecond
	})
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	time.Sleep(300 * time.Millisecond)
	s.send(t, agentapi.LogLine{TS: 9, Stream: agentapi.StreamOut, Line: "late"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "9" {
		t.Fatalf("event = %+v", ev)
	}
}
