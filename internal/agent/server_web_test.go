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
)

// stubWeb records the route lists and answers with resp or err.
type stubWeb struct {
	mu   sync.Mutex
	err  error
	resp agentapi.WebRoutesResponse
	got  []agentapi.WebRoutesRequest
}

func (s *stubWeb) SetWebRoutes(_ context.Context, req agentapi.WebRoutesRequest) (agentapi.WebRoutesResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, req)
	return s.resp, s.err
}

func (s *stubWeb) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

const webBody = `{"routes":[{"id":1,"fqdn":"survival.mc.example.com","port":8100},{"id":2,"fqdn":"creative.mc.example.com","port":8123}]}`

func newWebServer(st *stubWeb) *agent.Server {
	fw, _ := newNoop()
	return agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithWeb(st))
}

func TestServerWebRoutes(t *testing.T) {
	st := &stubWeb{resp: agentapi.WebRoutesResponse{Routes: []agentapi.WebRouteStatus{
		{ID: 1, Status: agentapi.WebActive},
		{ID: 2, Status: agentapi.WebError, Error: "server network missing"},
	}}}
	rec := serve(newWebServer(st), http.MethodPut, agentapi.PathWebRoutes, jsonCT, webBody)
	want := `{"routes":[{"id":1,"status":"active"},{"id":2,"status":"error","error":"server network missing"}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("PUT web routes: %d %s", rec.Code, rec.Body)
	}
	if len(st.got) != 1 || len(st.got[0].Routes) != 2 || st.got[0].Routes[1] != (agentapi.WebRoute{ID: 2, FQDN: "creative.mc.example.com", Port: 8123}) {
		t.Fatalf("got = %+v", st.got)
	}
}

func TestServerWebRoutesRejects(t *testing.T) {
	for _, tc := range []struct {
		name, method, ct, body string
		want                   int
	}{
		{"unknown field", http.MethodPut, jsonCT, `{"routes":[],"extra":1}`, http.StatusBadRequest},
		{"unknown route field", http.MethodPut, jsonCT, `{"routes":[{"id":1,"fqdn":"a.b.c","port":8100,"host":"x"}]}`, http.StatusBadRequest},
		{"null routes", http.MethodPut, jsonCT, `{"routes":null}`, http.StatusBadRequest},
		{"missing routes", http.MethodPut, jsonCT, `{}`, http.StatusBadRequest},
		{"invalid port", http.MethodPut, jsonCT, `{"routes":[{"id":1,"fqdn":"a.b.c","port":25565}]}`, http.StatusBadRequest},
		{"upper case fqdn", http.MethodPut, jsonCT, `{"routes":[{"id":1,"fqdn":"A.b.c","port":8100}]}`, http.StatusBadRequest},
		{"trailing data", http.MethodPut, jsonCT, `{"routes":[]}{}`, http.StatusBadRequest},
		{"too large", http.MethodPut, jsonCT, `{"routes":[],"x":"` + strings.Repeat("a", agentapi.MaxWebRequestBytes) + `"}`, http.StatusRequestEntityTooLarge},
		{"wrong content type", http.MethodPut, "text/plain", `{"routes":[]}`, http.StatusUnsupportedMediaType},
		{"wrong method", http.MethodPost, jsonCT, `{"routes":[]}`, http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &stubWeb{}
			rec := serve(newWebServer(st), tc.method, agentapi.PathWebRoutes, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if st.calls() != 0 {
				t.Fatalf("backend called %d times", st.calls())
			}
		})
	}
}

func TestServerWebRoutesErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: shutting down", agentapi.ErrDockerUnavailable), http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable},
		{errors.New("disk on fire at /srv/mc-panel"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		rec := serve(newWebServer(&stubWeb{err: tc.err}), http.MethodPut, agentapi.PathWebRoutes, jsonCT, webBody)
		var er agentapi.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != tc.status || er.Code != tc.code {
			t.Fatalf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "/srv") {
			t.Fatalf("internal error leaked: %s", rec.Body)
		}
	}
}

func TestServerWithoutWeb(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	rec := serve(srv, http.MethodPut, agentapi.PathWebRoutes, jsonCT, webBody)
	var er agentapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != http.StatusServiceUnavailable || er.Code != agentapi.ErrorCodeDockerUnavailable {
		t.Fatalf("without web: %d %s", rec.Code, rec.Body)
	}
}
