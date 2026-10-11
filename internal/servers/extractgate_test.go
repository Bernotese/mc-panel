package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/servers"
)

// Review Focus #5: no start or restart while an archive is extracted into the server.
func TestPowerBlockedWhileExtracting(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, id)
	e.gate.set(id, true)
	for _, action := range []string{"start", "restart"} {
		if code, ec := e.power(id, action, &admin); code != http.StatusConflict || ec != "conflict" {
			t.Fatalf("%s while extracting: %d %s", action, code, ec)
		}
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "stopped" {
		t.Fatalf("desired = %s", got)
	}
	asked := e.gate.askedCount()
	if code, _ := e.power(id, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop while extracting: %d", code)
	}
	if e.gate.askedCount() != asked {
		t.Fatal("a stop must not wait for the file service")
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerPower}) {
		t.Fatalf("audit = %v", got)
	}
	e.gate.set(id, false)
	if code, _ := e.power(id, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("start after the job: %d", code)
	}
}

func TestReconcileDoesNotStartWhileExtracting(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival") // desired running
	e.agent.set(container("survival", id, 1, agentapi.StateExited, ""))
	e.gate.set(id, true)
	if got := e.pass(); len(got) != 0 {
		t.Fatalf("calls while extracting = %v", got)
	}
	if n := servers.Attempts(e.h); n != 0 {
		t.Fatalf("a held start must not count for the backoff: %d attempts", n)
	}
	e.gate.set(id, false)
	if got := e.pass(); !slices.Equal(got, []string{fmt.Sprintf("power %d start", id)}) {
		t.Fatalf("calls after the job = %v", got)
	}
}

// The gate only holds starts: stopping and creating containers go on.
func TestReconcileStopsAndEnsuresWhileExtracting(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	running := createInstance(t, e.db, "survival")
	missing := createInstance(t, e.db, "creative")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, running)
	e.agent.set(container("survival", running, 1, agentapi.StateRunning, ""))
	e.gate.set(running, true)
	e.gate.set(missing, true)
	// ServersListInstances sorts by name: "creative" before "survival".
	want := []string{fmt.Sprintf("ensure %d r1 recreate=false", missing), fmt.Sprintf("power %d stop", running)}
	if got := e.pass(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
