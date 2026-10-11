package console_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const unavailableJSON = `{"available":false,"online":[],"max":null}`

func TestOnlinePlayers(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	e.agent.setOutput("list uuids", "There are 1 of a max of 20 players online: Alex (8667ba71-b85a-4004-af54-457a9734eed7)\n")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view")
	vp := userP(viewer, "viewer")

	rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &vp)
	want := `{"available":true,"online":[{"name":"Alex","uuid":"8667ba71-b85a-4004-af54-457a9734eed7"}],"max":20}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("players: %d %s", rec.Code, rec.Body)
	}
	if got := e.agent.takeRcons(); len(got) != 1 || got[0] != fmt.Sprintf("%d list uuids", id) {
		t.Fatalf("rcon calls = %v", got)
	}
	if got := e.rec.Actions(); len(got) != 0 {
		t.Fatalf("reading online players must not be audited: %v", got)
	}

	other := storetest.CreateUser(t, e.db, "other", "user")
	op := userP(other, "other")
	if rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &op); rec.Code != http.StatusForbidden {
		t.Fatalf("without view: %d", rec.Code)
	}
	stub := storetest.CreateServer(t, e.db, "stub")
	admin := e.admin()
	if rec := e.do(http.MethodGet, srvPath(stub, "/players"), nil, &admin); rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
		t.Fatalf("not configured: %d %s", rec.Code, rec.Body)
	}
}

func TestOnlinePlayersCache(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	e.agent.setOutput("list uuids", "There are 0 of a max of 20 players online: ")
	admin := e.admin()
	for range 3 {
		e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	}
	if got := e.agent.takeRcons(); len(got) != 1 {
		t.Fatalf("within 10 s: %d rcon calls, want 1", len(got))
	}
	e.clk.Advance(9 * time.Second)
	e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	if got := e.agent.takeRcons(); len(got) != 0 {
		t.Fatalf("after 9 s: %v", got)
	}
	e.clk.Advance(time.Second)
	e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	if got := e.agent.takeRcons(); len(got) != 1 {
		t.Fatalf("after 10 s: %d rcon calls, want 1", len(got))
	}
}

// Review Focus #5: nothing about the answer may produce a 500.
func TestOnlinePlayersUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
	}{
		{"not running", "", &agent.StatusError{StatusCode: 409, Code: agentapi.ErrorCodeNotRunning}},
		{"rcon not ready", "", &agent.StatusError{StatusCode: 503, Code: agentapi.ErrorCodeRconUnavailable}},
		{"docker down", "", &agent.StatusError{StatusCode: 503, Code: agentapi.ErrorCodeDockerUnavailable}},
		{"agent down", "", fmt.Errorf("%w: dial", agentapi.ErrUnavailable)},
		{"unknown format", "Online (2): Alex, Steve", nil},
		{"plugin error", "An internal error occurred while attempting to perform this command", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			id := createInstance(t, e.db, "survival")
			e.agent.setOutput("list uuids", tc.out)
			e.agent.setRconErr("list uuids", tc.err)
			admin := e.admin()
			rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != unavailableJSON {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
	e := newEnvWith(t, func(d *console.Deps) { d.Agent = nil })
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	if rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin); strings.TrimSpace(rec.Body.String()) != unavailableJSON {
		t.Fatalf("no agent: %d %s", rec.Code, rec.Body)
	}
}
