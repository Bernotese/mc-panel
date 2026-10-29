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
	"github.com/Bernotese/mc-panel/internal/servers"
)

// The agent works asynchronously: right after an ensure it reports an operation. That must not
// reset the backoff, or a permanently failing pull is retried on every pass.
func TestReconcileBackoffSurvivesOperation(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "alpha")
	ensure := fmt.Sprintf("ensure %d r1 recreate=false", id)
	if calls := e.pass(); !slices.Equal(calls, []string{ensure}) {
		t.Fatalf("first pass = %v", calls)
	}
	e.clk.Advance(10 * time.Second)
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "alpha", State: agentapi.StateMissing, Operation: agentapi.OpPulling})
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("pass while pulling = %v", calls)
	}
	// the pull failed: second attempt after 10 s, the third only 20 s later
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "alpha", State: agentapi.StateMissing, LastError: "pull failed"})
	e.clk.Advance(10 * time.Second)
	if calls := e.pass(); !slices.Equal(calls, []string{ensure}) {
		t.Fatalf("second attempt = %v", calls)
	}
	e.clk.Advance(10 * time.Second)
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("backoff ignored, attempt after 10 s: %v", calls)
	}
	e.clk.Advance(10 * time.Second)
	if calls := e.pass(); !slices.Equal(calls, []string{ensure}) {
		t.Fatalf("third attempt = %v", calls)
	}
}

// An action that never reached the agent does not count for the backoff: the stored wish is
// applied on the first pass after the agent answers again.
func TestReconcileUnreachedActionIsRetriedAtOnce(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "alpha")
	e.agent.setActionErr(agentapi.ErrUnavailable)
	e.pass()
	e.agent.setActionErr(nil)
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r1 recreate=false", id)}) {
		t.Fatalf("after recovery = %v", calls)
	}
}

// A DELETE while a reconcile pass is running must not let the pass recreate the container it
// listed before the delete (orphan container for a deleted server).
func TestDeleteWaitsForRunningReconcilePass(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	e.agent.blockEnsure = make(chan struct{})
	e.agent.entered = make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.h.ReconcileOnce(context.Background()) }()
	<-e.agent.entered // the pass holds the rows of alpha and beta and waits in ensure alpha
	admin := e.admin()
	deleted := make(chan int, 1)
	go func() {
		defer wg.Done()
		deleted <- e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", b), nil, &admin).Code
	}()
	time.Sleep(100 * time.Millisecond)
	close(e.agent.blockEnsure)
	wg.Wait()
	if code := <-deleted; code != http.StatusAccepted {
		t.Fatalf("delete: %d", code)
	}
	var beta []string
	for _, c := range e.agent.takeCalls() {
		if strings.HasPrefix(c, fmt.Sprintf("ensure %d ", b)) || c == fmt.Sprintf("remove %d", b) {
			beta = append(beta, c)
		}
	}
	if len(beta) == 0 || beta[len(beta)-1] != fmt.Sprintf("remove %d", b) {
		t.Fatalf("calls for beta = %v: the container was recreated after the delete (alpha %d)", beta, a)
	}
}

// A refresh whose request was cancelled (client gone, timeout) must not replace the cached
// state with "agent unreachable".
func TestCancelledRefreshKeepsStatus(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "alpha")
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "alpha", Revision: 1, State: agentapi.StateRunning})
	e.refresh()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.agent.setErr(context.Canceled)
	servers.RefreshStatus(ctx, e.h)
	if st := e.get(t, id).Status.State; st != "running" {
		t.Fatalf("state after cancelled refresh = %s", st)
	}
}
