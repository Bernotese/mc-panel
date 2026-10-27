package devinst_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/monitoring"
)

// passes feeds a pass every 30 s for d, starting 30 s from now, with the sample of mk.
func (e *env) passes(d time.Duration, mk func() monitoring.ServerSample) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += 30 * time.Second {
		e.clk.Advance(30 * time.Second)
		e.pass(mk())
	}
}

func (e *env) sleepsOf(devID int64) int {
	n := 0
	for _, c := range e.servers.sleepCalls() {
		if c.ID == devID && c.Sleeping {
			n++
		}
	}
	return n
}

func TestSleepAfterIdle(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(stopped(devID))
	start := e.clk.Now()
	sample := func() monitoring.ServerSample { return running(devID, start, ptr(0)) }
	e.passes(14*time.Minute+30*time.Second, sample)
	if n := e.sleepsOf(devID); n != 0 {
		t.Fatalf("slept after %v", e.clk.Now().Sub(start))
	}
	e.passes(30*time.Second, sample) // 15 min
	calls := e.servers.sleepCalls()
	if len(calls) != 1 || calls[0].ID != devID || !calls[0].Sleeping || !calls[0].At.Equal(start.Add(15*time.Minute)) {
		t.Fatalf("sleep calls %+v", calls)
	}
	if r := e.row(devID); r.Sleeping != 1 {
		t.Fatal("not sleeping")
	}
	// The container is still running for a moment: no second call.
	e.passes(time.Minute, sample)
	if n := e.sleepsOf(devID); n != 1 {
		t.Fatalf("%d sleep calls", n)
	}
}

func TestSleepUnknownPlayersNeverSleeps(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(stopped(devID))
	start := e.clk.Now()
	e.passes(30*time.Minute, func() monitoring.ServerSample { return running(devID, start, nil) })
	if n := e.sleepsOf(devID); n != 0 {
		t.Fatal("slept on unknown player counts")
	}
	unknownEnd := e.clk.Now()
	zero := func() monitoring.ServerSample { return running(devID, start, ptr(0)) }
	e.passes(14*time.Minute+30*time.Second, zero)
	if n := e.sleepsOf(devID); n != 0 {
		t.Fatalf("slept %v after the unknown gap", e.clk.Now().Sub(unknownEnd))
	}
	e.passes(30*time.Second, zero)
	if n := e.sleepsOf(devID); n != 1 {
		t.Fatalf("not asleep 15 min after the unknown gap")
	}
}

func TestSleepAfterLastPlayerLeft(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(stopped(devID))
	start := e.clk.Now()
	e.passes(40*time.Minute, func() monitoring.ServerSample { return running(devID, start, ptr(2)) })
	left := e.clk.Now()
	zero := func() monitoring.ServerSample { return running(devID, start, ptr(0)) }
	e.passes(14*time.Minute+30*time.Second, zero)
	if e.sleepsOf(devID) != 0 {
		t.Fatal("slept too early")
	}
	e.passes(30*time.Second, zero)
	if c := e.servers.sleepCalls(); len(c) != 1 || !c[0].At.Equal(left.Add(15*time.Minute)) {
		t.Fatalf("sleep calls %+v", c)
	}
}

func TestManualStartIdleFromStart(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	mustExec(t, e.db, `UPDATE dev_instances SET idle_minutes = 5 WHERE server_id = ?`, devID)
	e.pass(stopped(devID))
	// Started 20 s before the next pass: the idle time counts from the start, not the pass.
	e.clk.Advance(10 * time.Second)
	start := e.clk.Now()
	sample := func() monitoring.ServerSample { return running(devID, start, ptr(0)) }
	e.clk.Advance(20 * time.Second)
	e.pass(sample())
	e.clk.Advance(4*time.Minute + 30*time.Second) // 4:50 after the start
	e.pass(sample())
	if e.sleepsOf(devID) != 0 {
		t.Fatal("slept before 5 min")
	}
	e.clk.Advance(10 * time.Second) // 5:00
	e.pass(sample())
	if e.sleepsOf(devID) != 1 {
		t.Fatal("not asleep 5 min after the start")
	}

	// A restart (new StartedAt) starts the idle time again.
	restart := e.clk.Now().Add(time.Minute)
	e.clk.Set(restart)
	e.pass(running(devID, restart, ptr(0)))
	e.clk.Advance(4 * time.Minute)
	e.pass(running(devID, restart, ptr(0)))
	if e.sleepsOf(devID) != 1 {
		t.Fatal("slept again before 5 min after the restart")
	}
	e.clk.Advance(time.Minute)
	e.pass(running(devID, restart, ptr(0)))
	if e.sleepsOf(devID) != 2 {
		t.Fatal("not asleep 5 min after the restart")
	}
}

func TestSleepAfterPanelRestartCountsFromFirstPass(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	// The handler has never seen this dev: it may have had players a minute ago.
	start := e.clk.Now().Add(-3 * time.Hour)
	e.pass(running(devID, start, ptr(0)))
	if e.sleepsOf(devID) != 0 {
		t.Fatal("slept on the first pass after a panel restart")
	}
	e.passes(15*time.Minute, func() monitoring.ServerSample { return running(devID, start, ptr(0)) })
	if e.sleepsOf(devID) != 1 {
		t.Fatal("not asleep 15 min after the first pass")
	}
}

func TestSleepNotWhileGated(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(stopped(devID))
	start := e.clk.Now()
	e.passes(30*time.Minute, func() monitoring.ServerSample {
		s := running(devID, start, ptr(0))
		s.Gated = true
		return s
	})
	if e.sleepsOf(devID) != 0 {
		t.Fatal("slept while gated")
	}
}

func TestSleepIgnoresProd(t *testing.T) {
	e := newEnv(t)
	prodID, _ := e.dev("survival")
	e.pass(stopped(prodID))
	start := e.clk.Now()
	e.passes(30*time.Minute, func() monitoring.ServerSample { return running(prodID, start, ptr(0)) })
	if c := e.servers.sleepCalls(); len(c) != 0 {
		t.Fatalf("sleep calls %+v", c)
	}
}

func TestSleepRetriesAfterError(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(stopped(devID))
	start := e.clk.Now()
	sample := func() monitoring.ServerSample { return running(devID, start, ptr(0)) }
	e.servers.errs["sleep"] = errors.New("agent down")
	e.passes(15*time.Minute, sample)
	delete(e.servers.errs, "sleep")
	e.passes(30*time.Second, sample)
	if e.sleepsOf(devID) != 1 {
		t.Fatal("no retry after a failed sleep")
	}
}
