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

// stubInstances records calls and answers every write with err.
type stubInstances struct {
	mu      sync.Mutex
	err     error
	docker  string
	status  agentapi.InstancesResponse
	ensured []agentapi.EnsureRequest
	powered []agentapi.PowerRequest
	removed []int64
}

func (s *stubInstances) DockerStatus() string { return s.docker }

func (s *stubInstances) Status(context.Context) agentapi.InstancesResponse { return s.status }

func (s *stubInstances) Ensure(_ context.Context, req agentapi.EnsureRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensured = append(s.ensured, req)
	return s.err
}

func (s *stubInstances) Power(_ context.Context, id int64, a agentapi.PowerAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.powered = append(s.powered, agentapi.PowerRequest{ID: id, Action: a})
	return s.err
}

func (s *stubInstances) Remove(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, id)
	return s.err
}

func (s *stubInstances) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ensured) + len(s.powered) + len(s.removed)
}

const ensureBody = `{"id":1,"name":"survival","fqdn":"survival.mc.example.com","revision":2,"recreate":true,` +
	`"spec":{"type":"paper","version":"1.21.4","loader_version":null,"modpack":null,"java":"java21",` +
	`"memory_mib":4096,"properties":{"motd":"Hallo","max_players":10},"curseforge_api_key":null}}`

func newInstanceServer(st *stubInstances) *agent.Server {
	fw, _ := newNoop()
	return agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithInstances(st))
}

func TestServerHealthReportsDocker(t *testing.T) {
	rec := serve(newInstanceServer(&stubInstances{docker: agentapi.DockerOK}), http.MethodGet, agentapi.PathHealth, "", "")
	var h agentapi.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil || h.Docker != "ok" {
		t.Fatalf("health = %s (%v)", rec.Body, err)
	}
}

func TestServerInstanceEndpoints(t *testing.T) {
	st := &stubInstances{docker: agentapi.DockerOK, status: agentapi.InstancesResponse{
		Docker:    agentapi.DockerOK,
		Instances: []agentapi.InstanceStatus{{ID: 1, Name: "survival", Revision: 2, State: agentapi.StateRunning, CPUPercent: 12.5, TemplateOutdated: true}},
		Router:    agentapi.RouterStatus{State: agentapi.StateRunning},
	}}
	srv := newInstanceServer(st)

	rec := serve(srv, http.MethodGet, agentapi.PathInstances, "", "")
	want := `{"docker":"ok","instances":[{"id":1,"name":"survival","revision":2,"state":"running","exit_code":0,` +
		`"restart_count":0,"started_at":0,"memory_bytes":0,"memory_limit_bytes":0,"cpu_percent":12.5,"operation":"","queued":[],"last_error":"",` +
		`"template_outdated":true}],` +
		`"router":{"state":"running","last_error":""}}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("GET instances: %d %s", rec.Code, rec.Body)
	}

	for _, tc := range []struct{ path, body string }{
		{agentapi.PathInstanceEnsure, ensureBody},
		{agentapi.PathInstancePower, `{"id":1,"action":"restart"}`},
		{agentapi.PathInstanceRemove, `{"id":1}`},
	} {
		if rec := serve(srv, http.MethodPost, tc.path, jsonCT, tc.body); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
	if len(st.ensured) != 1 || st.ensured[0].Revision != 2 || !st.ensured[0].Recreate || *st.ensured[0].Spec.Properties.MaxPlayers != 10 {
		t.Fatalf("ensured = %+v", st.ensured)
	}
	if len(st.powered) != 1 || st.powered[0] != (agentapi.PowerRequest{ID: 1, Action: agentapi.PowerRestart}) || len(st.removed) != 1 || st.removed[0] != 1 {
		t.Fatalf("powered = %+v removed = %v", st.powered, st.removed)
	}
}

func TestServerInstanceErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{agentapi.ErrBusy, http.StatusTooManyRequests, agentapi.ErrorCodeBusy},
		{fmt.Errorf("%w: shutting down", agentapi.ErrDockerUnavailable), http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable},
		{errors.New("disk on fire at /srv/mc-panel"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		srv := newInstanceServer(&stubInstances{err: tc.err})
		rec := serve(srv, http.MethodPost, agentapi.PathInstancePower, jsonCT, `{"id":1,"action":"start"}`)
		var er agentapi.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != tc.status || er.Code != tc.code {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
		if tc.status == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "/srv") {
			t.Errorf("internal error leaked: %s", rec.Body)
		}
	}
}

func TestServerRejectsInvalidInstanceRequests(t *testing.T) {
	withSpec := func(old, repl string) string { return strings.Replace(ensureBody, old, repl, 1) }
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"unknown top-level field", "POST", agentapi.PathInstanceEnsure, jsonCT, strings.Replace(ensureBody, `"id":1`, `"id":1,"image":"evil"`, 1), 400},
		{"unknown spec field", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"java":"java21"`, `"java":"java21","jvm_opts":"-X"`), 400},
		{"unknown property", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"Hallo","plugins":"x"`), 400},
		{"ops property (sub-project 3a)", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"Hallo","ops":["Notch"]`), 400},
		{"whitelist property (sub-project 3a)", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"Hallo","whitelist":["Notch"]`), 400},
		{"motd newline", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"a\nENABLE_RCON=false"`), 400},
		{"motd nul", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"a\u0000b"`), 400},
		{"memory", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"memory_mib":4096`, `"memory_mib":4000`), 400},
		{"fqdn of other server", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"fqdn":"survival.mc.example.com"`, `"fqdn":"creative.mc.example.com"`), 400},
		{"revision 0", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"revision":2`, `"revision":0`), 400},
		{"recreate string", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"recreate":true`, `"recreate":"yes"`), 400},
		{"trailing data", "POST", agentapi.PathInstanceEnsure, jsonCT, ensureBody + `{}`, 400},
		{"too large", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"`+strings.Repeat("a", 17<<10)+`"`), 413},
		{"wrong content type", "POST", agentapi.PathInstanceEnsure, "text/plain", ensureBody, 415},
		{"power kill", "POST", agentapi.PathInstancePower, jsonCT, `{"id":1,"action":"kill"}`, 400},
		{"power id 0", "POST", agentapi.PathInstancePower, jsonCT, `{"id":0,"action":"start"}`, 400},
		{"power unknown field", "POST", agentapi.PathInstancePower, jsonCT, `{"id":1,"action":"start","signal":9}`, 400},
		{"remove id 0", "POST", agentapi.PathInstanceRemove, jsonCT, `{"id":0}`, 400},
		{"remove negative", "POST", agentapi.PathInstanceRemove, jsonCT, `{"id":-5}`, 400},
		{"remove unknown field", "POST", agentapi.PathInstanceRemove, jsonCT, `{"id":1,"keep_data":false}`, 400},
		{"GET on ensure", "GET", agentapi.PathInstanceEnsure, "", "", 405},
		{"POST on instances", "POST", agentapi.PathInstances, jsonCT, `{}`, 405},
	}
	st := &stubInstances{docker: agentapi.DockerOK}
	srv := newInstanceServer(st)
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
	if n := st.calls(); n != 0 {
		t.Fatalf("invalid requests reached the manager (%d calls)", n)
	}
}

func TestServerInstanceBodyLimitIs16KiB(t *testing.T) {
	// JSON whitespace pads a valid ensure request to more than the 4 KiB firewall limit.
	body := strings.Replace(ensureBody, `"max_players":10`, `"max_players":10`+strings.Repeat(" ", 6000), 1)
	if len(body) <= 4<<10 || len(body) > 16<<10 {
		t.Fatalf("body has %d bytes; the test needs 4 KiB < body <= 16 KiB", len(body))
	}
	st := &stubInstances{docker: agentapi.DockerOK}
	if rec := serve(newInstanceServer(st), http.MethodPost, agentapi.PathInstanceEnsure, jsonCT, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body)
	}
	if len(st.ensured) != 1 || *st.ensured[0].Spec.Properties.MaxPlayers != 10 {
		t.Fatalf("ensured = %+v", st.ensured)
	}
}

func TestServerWithoutInstanceBackend(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	rec := serve(srv, http.MethodGet, agentapi.PathInstances, "", "")
	want := `{"docker":"unavailable","instances":[],"router":{"state":"missing","last_error":""}}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("GET instances: %d %s", rec.Code, rec.Body)
	}
	rec = serve(srv, http.MethodPost, agentapi.PathInstanceEnsure, jsonCT, ensureBody)
	var er agentapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != http.StatusServiceUnavailable || er.Code != agentapi.ErrorCodeDockerUnavailable {
		t.Fatalf("ensure without backend: %d %s", rec.Code, rec.Body)
	}
}

func TestClientServerInstancesEndToEnd(t *testing.T) {
	st := &stubInstances{docker: agentapi.DockerOK, err: agentapi.ErrBusy}
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := newInstanceServer(st)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	err = agent.NewClient(path).Power(context.Background(), 1, agentapi.PowerStart)
	if !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("Power = %v, want ErrBusy through the real client", err)
	}
}
