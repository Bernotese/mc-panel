package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func (e *env) power(id int64, action string, p *principal.Principal) (int, string) {
	e.t.Helper()
	rec := e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": action}, p)
	if rec.Code >= 400 {
		return rec.Code, errCode(e.t, rec)
	}
	return rec.Code, ""
}

// Start and stop only store the wish; the reconcile loop talks to the agent. They therefore
// work while the agent is down.
func TestPowerStartStopSetDesiredState(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	e.agent.setErr(agentapi.ErrUnavailable)
	if code, _ := e.power(id, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop: %d", code)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "stopped" {
		t.Fatalf("desired = %s", got)
	}
	if code, _ := e.power(id, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("start: %d", code)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "running" {
		t.Fatalf("desired = %s", got)
	}
	if calls := e.agent.failedCalls(); len(calls) != 0 {
		t.Fatalf("start/stop called the agent: %v", calls)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerPower, audit.ActionServerPower}) {
		t.Fatalf("audit = %v", got)
	}
	stub := storetest.CreateServer(t, e.db, "lobby")
	if code, ec := e.power(stub, "start", &admin); code != http.StatusConflict || ec != "conflict" {
		t.Fatalf("stub: %d %s", code, ec)
	}
}

func TestPowerRestart(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")

	// current revision: plain restart
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "survival", Revision: 1, State: agentapi.StateRunning})
	if code, ec := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", code, ec)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("power %d restart", id)}) {
		t.Fatalf("calls = %v", calls)
	}

	// outdated revision: recreate with the new config
	mustExec(t, e.db, `UPDATE instances SET revision = 2, memory_mib = 6144, desired_state = 'stopped' WHERE server_id = ?`, id)
	if code, ec := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart outdated: %d %s", code, ec)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r2 recreate=true", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	req := e.agent.lastEnsure()
	if req.FQDN != "survival.mc.example.com" || req.Spec.MemoryMiB != 6144 || !req.Recreate {
		t.Fatalf("ensure = %+v", req)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "running" {
		t.Fatalf("restart must set desired running, got %s", got)
	}

	// missing container: the reconcile loop creates and starts it
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateMissing})
	if code, _ := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart missing: %d", code)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestPowerRestartNeedsAgent(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, id)
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "survival", Revision: 1, State: agentapi.StateExited})
	for _, tc := range []struct {
		setup  func()
		status int
		code   string
	}{
		{func() { e.agent.setErr(agentapi.ErrUnavailable) }, http.StatusServiceUnavailable, "agent_unavailable"},
		{func() { e.agent.setErr(nil); e.agent.setDocker(agentapi.DockerUnavailable) }, http.StatusServiceUnavailable, "docker_unavailable"},
		{func() { e.agent.setDocker(agentapi.DockerOK); e.agent.setActionErr(agentapi.ErrBusy) }, http.StatusTooManyRequests, "rate_limited"},
	} {
		tc.setup()
		if code, ec := e.power(id, "restart", &admin); code != tc.status || ec != tc.code {
			t.Errorf("got %d %s, want %d %s", code, ec, tc.status, tc.code)
		}
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "stopped" {
		t.Fatalf("a failed restart changed the desired state to %s", got)
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("failed restarts were audited: %v", e.rec.Actions())
	}
}

// Power actions are limited to 10 per minute per user, so a user cannot flood the audit chain.
func TestServerPowerRateLimitedPerUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	a := createInstance(t, e.db, "alpha")
	for i := 0; i < 10; i++ {
		if code, ec := e.power(a, "start", &admin); code != http.StatusAccepted {
			t.Fatalf("request %d: %d %s", i, code, ec)
		}
	}
	rec := e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &admin)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th request: %d %s retry=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	if n := len(e.rec.Entries); n != 10 {
		t.Fatalf("audit entries = %d, want 10 (a limited request is not audited)", n)
	}
	// another user has its own budget
	other := principal.Principal{UserID: storetest.CreateUser(t, e.db, "root2", "admin"), Username: "root2",
		Role: principal.RoleAdmin, Stage: principal.StageFull}
	if code, _ := e.power(a, "stop", &other); code != http.StatusAccepted {
		t.Fatalf("other user: %d", code)
	}
	e.clk.Advance(6 * time.Second)
	if code, _ := e.power(a, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("after refill: %d", code)
	}
}
