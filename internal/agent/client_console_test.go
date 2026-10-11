package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

var _ agentapi.ConsoleClient = (*agent.Client)(nil)

func writeLines(w http.ResponseWriter, lines ...string) {
	for _, l := range lines {
		_, _ = io.WriteString(w, l+"\n")
	}
	w.(http.Flusher).Flush()
}

func TestClientLogsStreamsLines(t *testing.T) {
	var got agentapi.LogsRequest
	var ct string
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != agentapi.PathInstanceLogs {
			http.Error(w, "wrong route", http.StatusNotFound)
			return
		}
		ct = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set(agentapi.HeaderContainerRunning, "true")
		writeLines(w,
			`{"ts":1760000000000000001,"stream":"out","line":"[12:00:00 INFO]: Done (8.9s)!"}`,
			``,
			`{"ts":1760000000000000002,"stream":"err","line":"oops"}`)
	}))
	s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 7, Tail: 500})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.Running() {
		t.Fatal("Running() = false, want true from the header")
	}
	l1, err := s.Next()
	if err != nil || l1.TS != 1760000000000000001 || l1.Stream != agentapi.StreamOut || !strings.Contains(l1.Line, "Done") {
		t.Fatalf("line 1 = %+v, %v", l1, err)
	}
	l2, err := s.Next()
	if err != nil || l2.Stream != agentapi.StreamErr || l2.Line != "oops" {
		t.Fatalf("line 2 = %+v, %v", l2, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end = %v, want io.EOF", err)
	}
	if got != (agentapi.LogsRequest{ID: 7, Tail: 500}) || ct != "application/json" {
		t.Fatalf("request = %+v, content type %q", got, ct)
	}
	if err := s.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
}

// The client hands out each line as it arrives and Close ends the request on the agent side.
func TestClientLogsIsLiveAndCloseCancels(t *testing.T) {
	done := make(chan struct{})
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeLines(w, `{"ts":1,"stream":"out","line":"first"}`)
		<-r.Context().Done()
		close(done)
	}))
	s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1})
	if err != nil {
		t.Fatal(err)
	}
	if l, err := s.Next(); err != nil || l.Line != "first" {
		t.Fatalf("first = %+v, %v", l, err)
	}
	_ = s.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the agent request")
	}
}

// Running comes from X-Container-Running; anything but "true" (including a missing header) is false.
func TestClientLogsRunningHeader(t *testing.T) {
	for _, tc := range []struct {
		header string // "" = not set
		want   bool
	}{
		{"true", true},
		{"false", false},
		{"", false},
		{"yes", false},
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if tc.header != "" {
				w.Header().Set(agentapi.HeaderContainerRunning, tc.header)
			}
			w.WriteHeader(http.StatusOK)
		}))
		s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1})
		if err != nil {
			t.Fatalf("header %q: %v", tc.header, err)
		}
		if got := s.Running(); got != tc.want {
			t.Errorf("header %q: Running() = %v, want %v", tc.header, got, tc.want)
		}
		if _, err := s.Next(); !errors.Is(err, io.EOF) {
			t.Errorf("header %q: empty stream must end with io.EOF, got %v", tc.header, err)
		}
		_ = s.Close()
	}
}

func TestClientLogsHeaderTimeout(t *testing.T) {
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never answers
	}))
	c := agent.NewClient(path)
	agent.SetLogsHeaderTimeout(c, 50*time.Millisecond)
	start := time.Now()
	_, err := c.Logs(context.Background(), agentapi.LogsRequest{ID: 1})
	if !errors.Is(err, agentapi.ErrUnavailable) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

func TestClientLogsBrokenStream(t *testing.T) {
	for name, body := range map[string]string{
		"oversized line": strings.Repeat("x", 70<<10) + "\n",
		"not json":       "hello\n",
		"truncated line": `{"ts":1,"stream":"out","line":"cut`,
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		s, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := s.Next(); !errors.Is(err, agentapi.ErrUnavailable) {
			t.Errorf("%s: Next = %v, want ErrUnavailable", name, err)
		}
		_ = s.Close()
	}
}

func TestClientRconAndPlayers(t *testing.T) {
	var rcon agentapi.RconRequest
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case agentapi.PathInstanceRcon:
			_ = json.NewDecoder(r.Body).Decode(&rcon)
			_ = json.NewEncoder(w).Encode(agentapi.RconResponse{Output: "There are 0 of a max of 20 players online: "})
		case agentapi.PathInstancePlayers:
			_, _ = io.WriteString(w, `{"ops":[{"uuid":"u1","name":"Notch","level":4}],"whitelist":null,"bans":null,"errors":["banned-players.json"]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	c := agent.NewClient(path)
	ctx := context.Background()
	out, err := c.Rcon(ctx, 3, "list uuids")
	if err != nil || !strings.HasPrefix(out, "There are 0") || rcon != (agentapi.RconRequest{ID: 3, Command: "list uuids"}) {
		t.Fatalf("Rcon = %q, %v (sent %+v)", out, err, rcon)
	}
	p, err := c.Players(ctx, 3)
	if err != nil || len(p.Ops) != 1 || p.Ops[0].Level != 4 || p.Whitelist == nil || p.Bans == nil ||
		len(p.Errors) != 1 || p.Errors[0] != "banned-players.json" {
		t.Fatalf("Players = %+v, %v", p, err)
	}
}

func TestClientConsoleValidatesBeforeSending(t *testing.T) {
	calls := 0
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++ }))
	c := agent.NewClient(path)
	ctx := context.Background()
	if _, err := c.Rcon(ctx, 1, "say a\nstop"); !errors.Is(err, agent.ErrInvalid) || !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Rcon newline: %v", err)
	}
	if _, err := c.Rcon(ctx, 0, "list"); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Rcon id 0: %v", err)
	}
	if _, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 1001}); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Logs tail: %v", err)
	}
	if _, err := c.Players(ctx, 0); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Players id 0: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached the agent (%d calls)", calls)
	}
}

func TestClientConsoleErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
		not    error
	}{
		{http.StatusNotFound, `{"error":"no container","code":"not_found"}`, agentapi.ErrNotFound, agentapi.ErrNotRunning},
		{http.StatusConflict, `{"error":"stopped","code":"not_running"}`, agentapi.ErrNotRunning, agentapi.ErrNotFound},
		{http.StatusServiceUnavailable, `{"error":"rcon: connection refused","code":"rcon_unavailable"}`, agentapi.ErrRconUnavailable, agentapi.ErrDockerUnavailable},
		{http.StatusServiceUnavailable, `{"error":"docker down","code":"docker_unavailable"}`, agentapi.ErrDockerUnavailable, agentapi.ErrRconUnavailable},
		{http.StatusConflict, `{"error":"other conflict"}`, agent.ErrConflict, agentapi.ErrNotRunning},
		{http.StatusTooManyRequests, `{"error":"busy","code":"busy"}`, agentapi.ErrBusy, agentapi.ErrNotRunning},
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		c := agent.NewClient(path)
		_, rconErr := c.Rcon(context.Background(), 1, "list")
		_, logsErr := c.Logs(context.Background(), agentapi.LogsRequest{ID: 1})
		for _, err := range []error{rconErr, logsErr} {
			if !errors.Is(err, tc.want) || errors.Is(err, tc.not) {
				t.Errorf("%d %s: got %v, want %v and not %v", tc.status, tc.body, err, tc.want, tc.not)
			}
		}
	}
}
