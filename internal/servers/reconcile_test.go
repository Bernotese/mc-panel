package servers_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func (e *env) pass() []string {
	e.h.ReconcileOnce(context.Background())
	return e.agent.takeCalls()
}

// container returns the agent status of instance id (container label mcpanel.name = name) in
// state s with revision rev.
func container(name string, id, rev int64, s agentapi.ContainerState, op agentapi.Operation) agentapi.InstanceStatus {
	return agentapi.InstanceStatus{ID: id, Name: name, Revision: rev, State: s, Operation: op}
}

// Every row of the table in spec §5.3 (plus the plan's refinement: a container that is not
// up is recreated before it is started, never started with an old revision).
func TestReconcileTable(t *testing.T) {
	const missing = agentapi.ContainerState("") // not listed by the agent at all
	cases := []struct {
		name     string
		desired  string
		revision int64
		state    agentapi.ContainerState
		crev     int64
		op       agentapi.Operation
		want     string // "" = no call
	}{
		{"running, not listed", "running", 1, missing, 0, "", "ensure 1 r1 recreate=false"},
		{"stopped, not listed", "stopped", 1, missing, 0, "", "ensure 1 r1 recreate=false"},
		{"running, missing", "running", 1, agentapi.StateMissing, 0, "", "ensure 1 r1 recreate=false"},
		{"stopped, outdated exited", "stopped", 2, agentapi.StateExited, 1, "", "ensure 1 r2 recreate=true"},
		{"stopped, outdated created", "stopped", 2, agentapi.StateCreated, 1, "", "ensure 1 r2 recreate=true"},
		{"running, outdated running", "running", 2, agentapi.StateRunning, 1, "", ""},
		{"running, outdated restarting", "running", 2, agentapi.StateRestarting, 1, "", ""},
		{"running, outdated exited", "running", 2, agentapi.StateExited, 1, "", "ensure 1 r2 recreate=true"},
		{"running, exited", "running", 1, agentapi.StateExited, 1, "", "power 1 start"},
		{"running, created", "running", 1, agentapi.StateCreated, 1, "", "power 1 start"},
		{"stopped, running", "stopped", 1, agentapi.StateRunning, 1, "", "power 1 stop"},
		{"stopped, restarting", "stopped", 1, agentapi.StateRestarting, 1, "", "power 1 stop"},
		{"operation pulling", "running", 1, agentapi.StateMissing, 0, agentapi.OpPulling, ""},
		{"operation starting", "running", 1, agentapi.StateCreated, 1, agentapi.OpStarting, ""},
		{"operation stopping", "running", 1, agentapi.StateRunning, 1, agentapi.OpStopping, ""},
		{"running, running", "running", 1, agentapi.StateRunning, 1, "", ""},
		{"stopped, exited", "stopped", 1, agentapi.StateExited, 1, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			setBaseDomain(t, e.db, "mc.example.com")
			id := createInstance(t, e.db, "survival")
			if id != 1 {
				t.Fatalf("id = %d", id)
			}
			mustExec(t, e.db, `UPDATE instances SET desired_state = ?, revision = ? WHERE server_id = ?`, tc.desired, tc.revision, id)
			if tc.state != missing {
				e.agent.set(container("survival", id, tc.crev, tc.state, tc.op))
			}
			var want []string
			if tc.want != "" {
				want = []string{tc.want}
			}
			if got := e.pass(); !slices.Equal(got, want) {
				t.Fatalf("calls = %v, want %v", got, want)
			}
		})
	}
}

func TestReconcileEnsureRequest(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	mustExec(t, e.db, `UPDATE instances SET properties = '{"motd":"Hallo"}' WHERE server_id = ?`, id)
	e.pass()
	req := e.agent.lastEnsure()
	if req.ID != id || req.Name != "survival" || req.FQDN != "survival.mc.example.com" || req.Revision != 1 || req.Recreate ||
		req.Spec.Type != "paper" || req.Spec.Version != "1.21.11" || req.Spec.MemoryMiB != 4096 ||
		req.Spec.Properties.MOTD == nil || *req.Spec.Properties.MOTD != "Hallo" || req.Spec.CurseForgeAPIKey != nil {
		t.Fatalf("ensure = %+v", req)
	}
}

// Review focus 4: while the agent is down nothing is sent, the state is unknown and power
// wishes are stored; once the agent answers again they are applied.
func TestReconcileAgentOutageAndRecovery(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	e.agent.set(container("survival", id, 1, agentapi.StateRunning, ""))
	e.agent.setErr(agentapi.ErrUnavailable)
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls while down = %v", calls)
	}
	if st := e.get(t, id).Status.State; st != "unknown" {
		t.Fatalf("state while down = %s", st)
	}
	if code, _ := e.power(id, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop while down: %d", code)
	}
	e.pass()
	if !strings.Contains(e.logs.String(), "reconcile paused") || strings.Count(e.logs.String(), "reconcile paused") != 1 {
		t.Fatalf("outage must be logged once:\n%s", e.logs)
	}
	e.agent.setErr(nil)
	if calls := e.pass(); !slices.Equal(calls, []string{"power 1 stop"}) {
		t.Fatalf("calls after recovery = %v", calls)
	}
	if !strings.Contains(e.logs.String(), "reconcile resumed") {
		t.Fatalf("recovery not logged:\n%s", e.logs)
	}
	e.agent.set(container("survival", id, 1, agentapi.StateExited, ""))
	e.h.ReconcileOnce(context.Background())
	if st := e.get(t, id).Status.State; st != "stopped" {
		t.Fatalf("state after recovery = %s", st)
	}

	// docker unavailable behaves like an outage
	e.agent.setDocker(agentapi.DockerUnavailable)
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'running' WHERE server_id = ?`, id)
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls without docker = %v", calls)
	}
}

// Review focus 4: one agent call per instance and pass, and a permanent error leads to an
// exponential backoff instead of a tight loop.
func TestReconcileBacksOff(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r1 recreate=false", a), fmt.Sprintf("ensure %d r1 recreate=false", b)}) {
		t.Fatalf("first pass = %v", calls)
	}
	// immediate second pass (e.g. triggered): the same action is not repeated yet
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("second pass = %v", calls)
	}
	// the agent keeps failing to create alpha: 10 s, 20 s, 40 s … between attempts, 5 min max
	e.agent.set(agentapi.InstanceStatus{ID: a, Name: "alpha", State: agentapi.StateMissing, LastError: "pull failed"})
	e.agent.set(container("beta", b, 1, agentapi.StateRunning, ""))
	var at []time.Duration
	for elapsed := 10 * time.Second; elapsed <= 10*time.Minute; elapsed += 10 * time.Second {
		e.clk.Advance(10 * time.Second)
		if calls := e.pass(); len(calls) > 0 {
			if len(calls) != 1 || calls[0] != fmt.Sprintf("ensure %d r1 recreate=false", a) {
				t.Fatalf("calls at %s = %v", elapsed, calls)
			}
			at = append(at, elapsed)
		}
	}
	want := []time.Duration{10 * time.Second, 30 * time.Second, 70 * time.Second, 150 * time.Second, 310 * time.Second}
	if !slices.Equal(at, want) {
		t.Fatalf("ensure attempts at %v, want %v", at, want)
	}
	// a new wish starts over: the stop is applied at once
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, a)
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r1 recreate=false", a)}) {
		t.Fatalf("after new desired state = %v", calls)
	}
}

func TestReconcileAbortsWhenAgentFailsMidPass(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	createInstance(t, e.db, "alpha")
	createInstance(t, e.db, "beta")
	e.agent.setActionErr(agentapi.ErrUnavailable)
	e.pass()
	if failed := e.agent.failedCalls(); len(failed) != 1 {
		t.Fatalf("unavailable agent: attempts = %v, want only the first", failed)
	}
	// a busy instance queue only affects that instance
	e.clk.Advance(time.Hour)
	e.agent.setActionErr(agentapi.ErrBusy)
	e.pass()
	if failed := e.agent.failedCalls(); len(failed) != 3 {
		t.Fatalf("busy agent: attempts = %v, want both instances", failed)
	}
}

// A base domain change bumps every revision: stopped servers are recreated with the new
// FQDN, running ones show restart_required and keep running.
func TestReconcileAfterBaseDomainChange(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	up := createInstance(t, e.db, "alpha")
	down := createInstance(t, e.db, "beta")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, down)
	e.agent.set(container("alpha", up, 1, agentapi.StateRunning, ""))
	e.agent.set(container("beta", down, 1, agentapi.StateExited, ""))
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("steady state = %v", calls)
	}
	e.putSettings(t, `{"base_domain":"play.example.org"}`)
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r2 recreate=true", down)}) {
		t.Fatalf("after base domain change = %v", calls)
	}
	if fqdn := e.agent.lastEnsure().FQDN; fqdn != "beta.play.example.org" {
		t.Fatalf("fqdn = %s", fqdn)
	}
	if st := e.get(t, up).Status; !st.RestartRequired || st.State != "running" {
		t.Fatalf("running server = %+v", st)
	}
}

func TestReconcileLeavesUnknownContainersAlone(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	e.agent.set(agentapi.InstanceStatus{ID: 99, Name: "old", Revision: 1, State: agentapi.StateRunning})
	e.pass()
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
	if n := strings.Count(e.logs.String(), "container without database entry"); n != 1 {
		t.Fatalf("unknown container logged %d times:\n%s", n, e.logs)
	}
}

func TestReconcileWithoutBaseDomainDoesNotEnsure(t *testing.T) {
	e := newEnv(t)
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	e.agent.set(container("beta", b, 1, agentapi.StateExited, ""))
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("power %d start", b)}) {
		t.Fatalf("calls = %v (alpha %d cannot be created without base domain)", calls, a)
	}
	if !strings.Contains(e.logs.String(), "no base domain") {
		t.Fatalf("missing base domain not logged:\n%s", e.logs)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	e.h.Trigger()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}
