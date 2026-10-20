package agent_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestClientWebRoutes(t *testing.T) {
	st := &stubWeb{resp: agentapi.WebRoutesResponse{Routes: []agentapi.WebRouteStatus{{ID: 1, Status: agentapi.WebPending}}}}
	c := agent.NewClient(handlerServer(t, newWebServer(st)))
	req := agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{{ID: 1, FQDN: "survival.mc.example.com", Targets: []agentapi.WebTarget{{Port: 8100}}}}}

	resp, err := c.WebRoutes(context.Background(), req)
	if err != nil || len(resp.Routes) != 1 || resp.Routes[0] != (agentapi.WebRouteStatus{ID: 1, Status: agentapi.WebPending}) {
		t.Fatalf("WebRoutes = %+v, %v", resp, err)
	}
	if len(st.got) != 1 || len(st.got[0].Routes) != 1 || !st.got[0].Routes[0].Equal(req.Routes[0]) {
		t.Fatalf("agent got %+v", st.got)
	}

	// An empty list is sent as [] (never null), so the agent removes every route.
	if _, err := c.WebRoutes(context.Background(), agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{}}); err != nil {
		t.Fatal(err)
	}
	if len(st.got) != 2 || st.got[1].Routes == nil || len(st.got[1].Routes) != 0 {
		t.Fatalf("empty list sent as %+v", st.got)
	}
}

func TestClientWebRoutesValidatesFirst(t *testing.T) {
	st := &stubWeb{}
	c := agent.NewClient(handlerServer(t, newWebServer(st)))
	for _, req := range []agentapi.WebRoutesRequest{
		{},
		{Routes: []agentapi.WebRoute{{ID: 1, FQDN: "survival.mc.example.com", Targets: []agentapi.WebTarget{{Port: 25575}}}}},
	} {
		if _, err := c.WebRoutes(context.Background(), req); !errors.Is(err, instancespec.ErrInvalid) {
			t.Fatalf("WebRoutes(%+v) = %v", req, err)
		}
	}
	if st.calls() != 0 {
		t.Fatalf("agent called %d times", st.calls())
	}
}

func TestClientWebRoutesDockerUnavailable(t *testing.T) {
	fw, _ := newNoop()
	c := agent.NewClient(handlerServer(t, agent.NewServer(fw, "test", testLogger(io.Discard))))
	_, err := c.WebRoutes(context.Background(), agentapi.WebRoutesRequest{Routes: []agentapi.WebRoute{}})
	if !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("WebRoutes = %v", err)
	}
}
