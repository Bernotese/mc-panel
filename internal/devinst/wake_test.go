package devinst_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/devinst"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
)

func (e *env) wakesOf(devID int64) int {
	n := 0
	for _, c := range e.servers.sleepCalls() {
		if c.ID == devID && !c.Sleeping {
			n++
		}
	}
	return n
}

// asleep marks devID asleep again (as if it fell asleep after a wake).
func (e *env) asleep(devID int64) {
	mustExec(e.t, e.db, `UPDATE dev_instances SET sleeping = 1 WHERE server_id = ?`, devID)
}

func TestWakeLoopResendsAfterRestart(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	_, dev1 := e.dev("survival")
	_, dev2 := e.dev("creative")
	mustExec(t, e.db, `UPDATE dev_instances SET sleeping = 0 WHERE server_id = ?`, dev2)

	// A fresh handler (panel restart) sends the sleeping devs from the database on its first tick.
	h := e.newHandler()
	devinst.FastTick(e.ctx(), h)
	reqs := e.waker.requests()
	want := []agentapi.WakerTarget{{ID: dev1, FQDN: "dev-survival.example.com"}}
	if len(reqs) != 1 || !slices.Equal(reqs[0].Sleeping, want) {
		t.Fatalf("requests %+v, want %+v", reqs, want)
	}
	if err := reqs[0].Validate(); err != nil {
		t.Fatalf("request invalid: %v", err)
	}
	// Every tick sends the complete list again.
	devinst.FastTick(e.ctx(), h)
	if n := len(e.waker.requests()); n != 2 {
		t.Fatalf("%d requests after two ticks", n)
	}
}

func TestWakeLoopWithoutBaseDomainSendsEmptyList(t *testing.T) {
	e := newEnv(t)
	e.dev("survival")
	devinst.FastTick(e.ctx(), e.h)
	reqs := e.waker.requests()
	if len(reqs) != 1 || reqs[0].Sleeping == nil || len(reqs[0].Sleeping) != 0 {
		t.Fatalf("requests %+v", reqs)
	}
}

func TestWakeWakesDev(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	prodID, devID := e.dev("survival")
	e.waker.wake(devID, prodID, 999) // only listed sleeping devs count
	devinst.FastTick(e.ctx(), e.h)
	calls := e.servers.sleepCalls()
	if len(calls) != 1 || calls[0].ID != devID || calls[0].Sleeping || !calls[0].At.Equal(t0) {
		t.Fatalf("sleep calls %+v", calls)
	}
	a := e.entries(audit.ActionDevWake)
	if len(a) != 1 || a[0].ActorType != audit.ActorSystem || a[0].Target != fmt.Sprintf("server:%d", devID) {
		t.Fatalf("dev.wake %+v", a)
	}
	// Awake now: the next request no longer lists it.
	devinst.FastTick(e.ctx(), e.h)
	if r := e.waker.requests(); len(r[len(r)-1].Sleeping) != 0 {
		t.Fatalf("still listed: %+v", r[len(r)-1])
	}
}

func TestWakeRateLimits(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	_, devID := e.dev("survival")
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	e.asleep(devID)
	e.clk.Advance(119 * time.Second)
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 1 {
		t.Fatalf("%d wakes within 2 min", n)
	}
	e.clk.Advance(time.Second)
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 2 {
		t.Fatalf("%d wakes after 2 min, want 2", n)
	}

	// Panel-wide: at most 10 per minute.
	e.clk.Advance(time.Hour)
	var ids []int64
	for i := range 12 {
		_, id := e.dev(fmt.Sprintf("s%d", i))
		ids = append(ids, id)
	}
	e.waker.wake(ids...)
	devinst.FastTick(e.ctx(), e.h)
	woken := 0
	for _, id := range ids {
		woken += e.wakesOf(id)
	}
	if woken != 10 {
		t.Fatalf("%d wakes in one minute, want 10", woken)
	}
	e.clk.Advance(time.Minute)
	e.waker.wake(ids...)
	devinst.FastTick(e.ctx(), e.h)
	woken = 0
	for _, id := range ids {
		woken += e.wakesOf(id)
	}
	if woken != 12 {
		t.Fatalf("%d wakes after another minute, want 12", woken)
	}
}

// A wake is refused unless the dev's data is safe (data safety spec §3): busy (a sync or
// restore), recovery_required and unknown (files service down) all ignore it.
func TestWakeRefusedUnlessSafe(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	_, devID := e.dev("survival")
	for _, st := range []servers.State{servers.StateBusy, servers.StateRecoveryRequired, servers.StateUnknown} {
		if st == servers.StateUnknown {
			e.data.setErr(errors.New("files down"))
		} else {
			e.data.set(devID, st)
		}
		e.waker.wake(devID)
		devinst.FastTick(e.ctx(), e.h)
		if n := e.wakesOf(devID); n != 0 {
			t.Fatalf("woken while %s", st)
		}
	}
	// The ignored attempts do not use up the dev's wake slot.
	e.data.setErr(nil)
	e.data.set(devID, servers.StateSafe)
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 1 {
		t.Fatal("not woken once the data was safe")
	}
}

// The wake stores the wish to run under the dev's admission (data safety spec §3); a refused
// admission ignores the wake without using up its slot.
func TestWakeAdmitted(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	_, devID := e.dev("survival")
	var mu sync.Mutex
	var refuse error
	var ids []int64
	e.admit = func(ctx context.Context, id int64, fn func(context.Context) error) error {
		mu.Lock()
		ids = append(ids, id)
		err := refuse
		mu.Unlock()
		if err != nil {
			return err
		}
		return fn(ctx)
	}
	e.h = e.newHandler()
	refuse = admit.ErrRecoveryRequired
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 0 {
		t.Fatal("woken although the admission was refused")
	}
	mu.Lock()
	refuse = nil
	mu.Unlock()
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 1 {
		t.Fatal("not woken once admitted")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(ids, []int64{devID, devID}) {
		t.Fatalf("admitted ids = %v", ids)
	}
}

func TestWakeAgentErrorsRetried(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	_, devID := e.dev("survival")
	e.waker.err = errors.New("agent down")
	devinst.FastTick(e.ctx(), e.h)
	e.waker.err = nil
	e.waker.notReady = true
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 1 {
		t.Fatalf("a reported wake while the waker is not ready: %d", n)
	}
	if n := len(e.waker.requests()); n != 2 {
		t.Fatalf("%d requests", n)
	}
}

func TestWakeFailureNotCounted(t *testing.T) {
	e := newEnv(t)
	e.setBaseDomain("example.com")
	_, devID := e.dev("survival")
	e.servers.errs["sleep"] = errors.New("db locked")
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	delete(e.servers.errs, "sleep")
	e.waker.wake(devID)
	devinst.FastTick(e.ctx(), e.h)
	if n := e.wakesOf(devID); n != 1 {
		t.Fatalf("a failed wake used up the rate limit: %d", n)
	}
	if a := e.entries(audit.ActionDevWake); len(a) != 1 {
		t.Fatalf("dev.wake %+v", a)
	}
}
