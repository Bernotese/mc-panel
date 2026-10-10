package servers_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// Whole-feature review of sub-project 2: tests for the panel-side findings.

// Finding 1: SQLite reuses the highest deleted rowid. A new server must never get the ID of a
// deleted one, or it adopts the leftover container and world of that server.
func TestServerIDsAreNeverReused(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	e.create(t, paper("alpha"))
	b := e.create(t, paper("beta"))
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", b.ID), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	c := e.create(t, paper("gamma"))
	if c.ID <= b.ID {
		t.Fatalf("new server got id %d, the deleted (highest) one had %d", c.ID, b.ID)
	}
	d := e.create(t, paper("delta"))
	if d.ID <= c.ID {
		t.Fatalf("ids not increasing: %d after %d", d.ID, c.ID)
	}
}

// Finding 1: a container whose mcpanel.name label differs from the server's name belongs to
// another (deleted) server. It is shown as an error and never touched: no ensure, start,
// restart or remove.
func TestForeignContainerIsNeverTouched(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "oldworld", Revision: 1, State: agentapi.StateExited})
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("reconcile touched a foreign container: %v", calls)
	}
	st := e.get(t, id).Status
	if st.State != "error" || !strings.Contains(st.LastError, "another server") {
		t.Fatalf("status = %+v, want error naming the foreign container", st)
	}
	if code, ec := e.power(id, "restart", &admin); code != http.StatusConflict || ec != "conflict" {
		t.Fatalf("restart of a foreign container: %d %s, want 409 conflict", code, ec)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if calls, failed := e.agent.takeCalls(), e.agent.failedCalls(); len(calls) != 0 || len(failed) != 0 {
		t.Fatalf("agent calls for a foreign container: %v (failed %v)", calls, failed)
	}
	if n := e.count(t, "servers"); n != 0 {
		t.Fatalf("servers = %d after delete", n)
	}
}

// Finding 1: a delete forgets the reconcile backoff of the server.
func TestDeleteForgetsReconcileBackoff(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "alpha")
	if calls := e.pass(); len(calls) != 1 {
		t.Fatalf("first pass = %v", calls)
	}
	if n := servers.Attempts(e.h); n != 1 {
		t.Fatalf("attempts = %d, want 1", n)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if n := servers.Attempts(e.h); n != 0 {
		t.Fatalf("attempts after delete = %d, want 0", n)
	}
}

// Finding 2: an error that the panel hits before it can call the agent (no base domain)
// surfaces as last_error with state error, and disappears once the action succeeds.
func TestPanelSideReconcileErrorIsShown(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "alpha")
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls without base domain = %v", calls)
	}
	st := e.get(t, id).Status
	if st.State != "error" || !strings.Contains(st.LastError, "base domain") {
		t.Fatalf("status without base domain = %+v, want error with last_error", st)
	}
	setBaseDomain(t, e.db, "mc.example.com")
	e.clk.Advance(10 * time.Second)
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r1 recreate=false", id)}) {
		t.Fatalf("calls with base domain = %v", calls)
	}
	if st := e.get(t, id).Status; st.State != "creating" || st.LastError != "" {
		t.Fatalf("status after the ensure = %+v, want creating without last_error", st)
	}
}

// Finding 2: a permanently failing ensure (missing container, no operation, last_error) is an
// error, not "creating" forever.
func TestFailedEnsureIsError(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "alpha")
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "alpha", State: agentapi.StateMissing, LastError: "pull failed"})
	e.refresh()
	if st := e.get(t, id).Status; st.State != "error" || st.LastError != "pull failed" {
		t.Fatalf("status = %+v", st)
	}
}

// Finding 3: only admins see the raw last_error; other users see the state only.
func TestLastErrorOnlyForAdmins(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	bob := storetest.CreateUser(t, e.db, "bob", "user")
	grant(t, e.db, bob, id, "server.view")
	bp := userP(bob, "bob")
	const msg = "create container: open /srv/mc-panel/instances/1: permission denied"
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "survival", Revision: 1, State: agentapi.StateExited, ExitCode: 1, LastError: msg})
	e.refresh()
	if st := e.get(t, id).Status; st.State != "error" || st.LastError != msg {
		t.Fatalf("admin status = %+v", st)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &bp)
	if st := decode[serverResp](t, rec).Status; rec.Code != http.StatusOK || st.State != "error" || st.LastError != "" {
		t.Fatalf("user GET: %d %+v", rec.Code, st)
	}
	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &bp))
	if len(list) != 1 || list[0].Status.State != "error" || list[0].Status.LastError != "" {
		t.Fatalf("user list = %+v", list)
	}
	if strings.Contains(e.do(http.MethodGet, "/servers", nil, &bp).Body.String(), "permission denied") {
		t.Fatal("list leaks last_error to a user")
	}
}

// Finding 4: a new or removed CurseForge key changes the spec of every CurseForge instance:
// their revision goes up (running ones need a restart), other types stay.
func TestCurseForgeKeyChangeBumpsCurseForgeRevisions(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	e.putSettings(t, `{"curseforge_api_key":"key-one"}`)
	cf := e.create(t, map[string]any{
		"name": "atm", "type": "curseforge", "version": "", "modpack": "all-the-mods-10", "memory_mib": 8192, "eula_accepted": true,
	}).ID
	p := createInstance(t, e.db, "survival")
	e.agent.set(agentapi.InstanceStatus{ID: cf, Name: "atm", Revision: 1, State: agentapi.StateRunning})
	e.agent.set(agentapi.InstanceStatus{ID: p, Name: "survival", Revision: 1, State: agentapi.StateRunning})
	revs := func() (int64, int64) { return instanceRow(t, e.db, cf).Revision, instanceRow(t, e.db, p).Revision }

	e.putSettings(t, `{"curseforge_api_key":"key-two"}`)
	if a, b := revs(); a != 2 || b != 1 {
		t.Fatalf("after key change: curseforge r%d, paper r%d, want r2 and r1", a, b)
	}
	if st := e.get(t, cf).Status; !st.RestartRequired || st.State != "running" {
		t.Fatalf("running curseforge server = %+v, want restart_required", st)
	}
	if st := e.get(t, p).Status; st.RestartRequired {
		t.Fatalf("paper server = %+v, want no restart", st)
	}
	e.putSettings(t, `{"curseforge_api_key":"key-two"}`) // same key: no change
	if a, _ := revs(); a != 2 {
		t.Fatalf("same key again: curseforge r%d, want r2", a)
	}
	e.putSettings(t, `{"curseforge_api_key":null}`)
	if a, b := revs(); a != 3 || b != 1 {
		t.Fatalf("after key removal: curseforge r%d, paper r%d, want r3 and r1", a, b)
	}
}

// Finding 5: the rows go in a transaction around the agent call. If the database delete
// fails, the agent is never asked to remove the container.
func TestDeleteServerDatabaseFailureSendsNoRemove(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "alpha")
	mustExec(t, e.db, `CREATE TRIGGER servers_fail_delete BEFORE DELETE ON servers BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete with failing database: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("agent calls = %v, want none (the rows are still there)", calls)
	}
	if got := e.rec.Actions(); slices.Contains(got, audit.ActionServerDelete) {
		t.Fatalf("failed delete was audited: %v", got)
	}
}

// Finding 5: a configured server deleted concurrently (0 rows) answers 404 without a remove.
func TestDeleteConfiguredServerZeroRowsSendsNoRemove(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "alpha")
	mustExec(t, e.db, `CREATE TRIGGER servers_ignore_delete BEFORE DELETE ON servers BEGIN SELECT RAISE(IGNORE); END`)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("agent calls = %v, want none", calls)
	}
}

// Finding 6: a restart waits for the reconcile lock and re-reads the server; one deleted in
// the meantime answers 404 and the agent is not called for it.
func TestRestartOfServerDeletedMeanwhileIs404(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	e.agent.set(agentapi.InstanceStatus{ID: b, Name: "beta", Revision: 1, State: agentapi.StateRunning})
	e.agent.blockEnsure = make(chan struct{})
	e.agent.entered = make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.h.ReconcileOnce(context.Background()) }()
	<-e.agent.entered // the pass holds the reconcile lock and waits in ensure alpha
	restarted := make(chan int, 1)
	go func() {
		defer wg.Done()
		restarted <- e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", b), map[string]string{"action": "restart"}, &admin).Code
	}()
	time.Sleep(100 * time.Millisecond) // the restart has loaded beta and waits for the lock
	mustExec(t, e.db, `DELETE FROM servers WHERE id = ?`, b)
	close(e.agent.blockEnsure)
	wg.Wait()
	if code := <-restarted; code != http.StatusNotFound {
		t.Fatalf("restart of a server deleted meanwhile: %d, want 404", code)
	}
	for _, c := range e.agent.takeCalls() {
		if strings.HasPrefix(c, fmt.Sprintf("power %d ", b)) || strings.HasPrefix(c, fmt.Sprintf("ensure %d ", b)) {
			t.Fatalf("agent called for the deleted server: %s (alpha %d)", c, a)
		}
	}
}

// Finding 7: agent entries in state "missing" are queued work or a remembered error, not
// containers; they are neither listed as unknown containers nor logged as such.
func TestUnknownContainersIgnoreMissingEntries(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	e.agent.set(agentapi.InstanceStatus{ID: 50, Name: "gone", State: agentapi.StateMissing, LastError: "remove failed"})
	e.agent.set(agentapi.InstanceStatus{ID: 51, Name: "older", Revision: 1, State: agentapi.StateExited})
	want := []unknownResp{{ID: 51, Name: "older"}}
	if got := e.settings(t).UnknownContainers; !slices.Equal(got, want) {
		t.Fatalf("unknown containers = %+v, want %+v", got, want)
	}
	e.pass()
	if strings.Contains(e.logs.String(), "server_id=50") {
		t.Fatalf("missing entry logged as unknown container:\n%s", e.logs)
	}
}
