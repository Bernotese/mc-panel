package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

// stubWaker records the requests and answers with resp or err.
type stubWaker struct {
	mu   sync.Mutex
	err  error
	resp agentapi.WakerResponse
	got  []agentapi.WakerRequest
}

func (s *stubWaker) SetWaker(_ context.Context, req agentapi.WakerRequest) (agentapi.WakerResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, req)
	return s.resp, s.err
}

func (s *stubWaker) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

const wakerBody = `{"sleeping":[{"id":2,"fqdn":"dev-survival.mc.example.com"},{"id":4,"fqdn":"dev-creative.mc.example.com"}]}`

func newWakerServer(st *stubWaker) *agent.Server {
	fw, _ := newNoop()
	return agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithWaker(st))
}

func TestWakerHandler(t *testing.T) {
	st := &stubWaker{resp: agentapi.WakerResponse{Ready: true, Wakes: []int64{2}}}
	rec := serve(newWakerServer(st), http.MethodPut, agentapi.PathWaker, jsonCT, wakerBody)
	if want := `{"ready":true,"wakes":[2]}` + "\n"; rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("PUT waker: %d %s", rec.Code, rec.Body)
	}
	want := []agentapi.WakerTarget{{ID: 2, FQDN: "dev-survival.mc.example.com"}, {ID: 4, FQDN: "dev-creative.mc.example.com"}}
	if len(st.got) != 1 || fmt.Sprint(st.got[0].Sleeping) != fmt.Sprint(want) {
		t.Fatalf("got = %+v", st.got)
	}
	rec = serve(newWakerServer(&stubWaker{resp: agentapi.WakerResponse{Wakes: []int64{}}}), http.MethodPut, agentapi.PathWaker, jsonCT, `{"sleeping":[]}`)
	if want := `{"ready":false,"wakes":[]}` + "\n"; rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body)
	}
}

func TestWakerHandlerRejects(t *testing.T) {
	for _, tc := range []struct {
		name, method, ct, body string
		want                   int
	}{
		{"unknown field", http.MethodPut, jsonCT, `{"sleeping":[],"extra":1}`, http.StatusBadRequest},
		{"unknown target field", http.MethodPut, jsonCT, `{"sleeping":[{"id":2,"fqdn":"dev-a.b.c","addr":"x"}]}`, http.StatusBadRequest},
		{"null list", http.MethodPut, jsonCT, `{"sleeping":null}`, http.StatusBadRequest},
		{"missing list", http.MethodPut, jsonCT, `{}`, http.StatusBadRequest},
		{"not a dev", http.MethodPut, jsonCT, `{"sleeping":[{"id":2,"fqdn":"survival.mc.example.com"}]}`, http.StatusBadRequest},
		{"upper case", http.MethodPut, jsonCT, `{"sleeping":[{"id":2,"fqdn":"DEV-a.mc.example.com"}]}`, http.StatusBadRequest},
		{"id 0", http.MethodPut, jsonCT, `{"sleeping":[{"id":0,"fqdn":"dev-a.mc.example.com"}]}`, http.StatusBadRequest},
		{"duplicate id", http.MethodPut, jsonCT, `{"sleeping":[{"id":2,"fqdn":"dev-a.mc.example.com"},{"id":2,"fqdn":"dev-b.mc.example.com"}]}`, http.StatusBadRequest},
		{"trailing data", http.MethodPut, jsonCT, `{"sleeping":[]}{}`, http.StatusBadRequest},
		{"too large", http.MethodPut, jsonCT, `{"sleeping":[],"x":"` + strings.Repeat("a", agentapi.MaxWakerRequestBytes) + `"}`, http.StatusRequestEntityTooLarge},
		{"wrong content type", http.MethodPut, "text/plain", `{"sleeping":[]}`, http.StatusUnsupportedMediaType},
		{"wrong method", http.MethodPost, jsonCT, `{"sleeping":[]}`, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubWaker{}
			rec := serve(newWakerServer(st), tc.method, agentapi.PathWaker, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if st.calls() != 0 {
				t.Fatalf("backend called %d times", st.calls())
			}
		})
	}
}

// TestWakerHandlerMaxList: the largest valid list fits into the body limit.
func TestWakerHandlerMaxList(t *testing.T) {
	targets := make([]agentapi.WakerTarget, agentapi.MaxWakerTargets)
	for i := range targets {
		label := fmt.Sprintf("dev-%028d", i) // 32 characters, the longest name
		targets[i] = agentapi.WakerTarget{ID: int64(i + 1), FQDN: label + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 63) + ".com"}
	}
	body, err := json.Marshal(agentapi.WakerRequest{Sleeping: targets})
	if err != nil {
		t.Fatal(err)
	}
	st := &stubWaker{resp: agentapi.WakerResponse{Wakes: []int64{}}}
	if rec := serve(newWakerServer(st), http.MethodPut, agentapi.PathWaker, jsonCT, string(body)); rec.Code != http.StatusOK {
		t.Fatalf("%d targets (%d bytes): %d %s", len(targets), len(body), rec.Code, rec.Body)
	}
}

func TestWakerHandlerErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: shutting down", agentapi.ErrDockerUnavailable), http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable},
		{errors.New("disk on fire at /srv/mc-panel"), http.StatusInternalServerError, ""},
	} {
		rec := serve(newWakerServer(&stubWaker{err: tc.err}), http.MethodPut, agentapi.PathWaker, jsonCT, wakerBody)
		var er agentapi.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != tc.status || er.Code != tc.code {
			t.Fatalf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "/srv") {
			t.Fatalf("internal error leaked: %s", rec.Body)
		}
	}
}

func TestServerWithoutWaker(t *testing.T) {
	fw, _ := newNoop()
	rec := serve(agent.NewServer(fw, "test", testLogger(io.Discard)), http.MethodPut, agentapi.PathWaker, jsonCT, wakerBody)
	var er agentapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != http.StatusServiceUnavailable || er.Code != agentapi.ErrorCodeDockerUnavailable {
		t.Fatalf("without waker: %d %s", rec.Code, rec.Body)
	}
}

// TestWakerPeerUID: like every endpoint, PUT /v1/waker is reachable only through the peer
// credential check of the socket; a client whose uid is not allowed never reaches the backend.
func TestWakerPeerUID(t *testing.T) {
	fw, _ := newNoop()
	st := &stubWaker{resp: agentapi.WakerResponse{Wakes: []int64{}}}
	srv := agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithWaker(st))
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(selfUID(t)+1), testLogger(io.Discard))) }()
	t.Cleanup(func() {
		_ = srv.Shutdown(context.Background())
		<-done
	})
	_, err = agent.NewClient(path).Waker(context.Background(), agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{}})
	if err == nil {
		t.Fatal("foreign uid reached the waker endpoint")
	}
	if st.calls() != 0 {
		t.Fatalf("backend called %d times", st.calls())
	}

	// The allowed uid gets through.
	path2 := socketPath(t)
	l2, err := agent.Listen(path2, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv2 := agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithWaker(st))
	done2 := make(chan error, 1)
	go func() { done2 <- srv2.Serve(peercred.Wrap(l2, peercred.AllowUIDs(selfUID(t)), testLogger(io.Discard))) }()
	t.Cleanup(func() {
		_ = srv2.Shutdown(context.Background())
		<-done2
	})
	if _, err := agent.NewClient(path2).Waker(context.Background(), agentapi.WakerRequest{Sleeping: []agentapi.WakerTarget{}}); err != nil {
		t.Fatalf("allowed uid: %v", err)
	}
	if st.calls() != 1 {
		t.Fatalf("backend called %d times, want 1", st.calls())
	}
}
