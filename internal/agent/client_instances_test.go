package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// handlerServer serves h on a fresh Unix socket and returns the socket path.
func handlerServer(t *testing.T, h http.Handler) string {
	t.Helper()
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return path
}

func TestClientInstanceCalls(t *testing.T) {
	var got []string
	var lastBody []byte
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		lastBody, _ = io.ReadAll(r.Body)
		if r.URL.Path == agentapi.PathInstances {
			_ = json.NewEncoder(w).Encode(agentapi.InstancesResponse{
				Docker:    agentapi.DockerOK,
				Instances: []agentapi.InstanceStatus{{ID: 1, Name: "survival", Revision: 2, State: agentapi.StateRunning}},
				Router:    agentapi.RouterStatus{State: agentapi.StateRunning},
			})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	c := agent.NewClient(path)
	ctx := context.Background()

	list, err := c.Instances(ctx)
	if err != nil || list.Docker != "ok" || len(list.Instances) != 1 || list.Instances[0].Revision != 2 {
		t.Fatalf("Instances = %+v, %v", list, err)
	}
	req := agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 2, Recreate: true,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	}
	if err := c.Ensure(ctx, req); err != nil {
		t.Fatal(err)
	}
	var sent agentapi.EnsureRequest
	if err := json.Unmarshal(lastBody, &sent); err != nil || sent.Revision != 2 || !sent.Recreate || sent.Spec.MemoryMiB != 4096 {
		t.Fatalf("sent %s (%v)", lastBody, err)
	}
	if err := c.Power(ctx, 1, agentapi.PowerStop); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, 1); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/instances", "POST /v1/instances/ensure", "POST /v1/instances/power", "POST /v1/instances/remove"}
	if len(got) != len(want) {
		t.Fatalf("calls = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

func TestClientInstanceCallsValidateBeforeSending(t *testing.T) {
	calls := 0
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusAccepted) }))
	c := agent.NewClient(path)
	ctx := context.Background()
	if err := c.Ensure(ctx, agentapi.EnsureRequest{ID: 1, Name: "survival"}); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Ensure invalid: %v", err)
	}
	if err := c.Power(ctx, 1, "kill"); !errors.Is(err, agent.ErrInvalid) || !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Power invalid: %v", err)
	}
	if err := c.Remove(ctx, 0); !errors.Is(err, agent.ErrInvalid) || !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Remove invalid: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached the agent (%d calls)", calls)
	}
}

func TestClientInstanceErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusServiceUnavailable, `{"error":"docker unavailable","code":"docker_unavailable"}`, agentapi.ErrDockerUnavailable},
		{http.StatusTooManyRequests, `{"error":"queue full","code":"busy"}`, agentapi.ErrBusy},
		{http.StatusBadRequest, `{"error":"bad"}`, agent.ErrInvalid},
		// One sentinel for the panel: an agent-side 400 is a validation error too.
		{http.StatusBadRequest, `{"error":"bad"}`, instancespec.ErrInvalid},
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		err := agent.NewClient(path).Power(context.Background(), 1, agentapi.PowerStart)
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: got %v, want %v", tc.status, err, tc.want)
		}
	}
}
