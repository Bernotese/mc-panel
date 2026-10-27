package devinst_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/monitoring"
)

type opsJSON struct {
	Names []string `json:"names"`
}

func TestOpsAddRemoveWhileRunning(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(running(devID, t0.Add(-time.Minute), ptr(1)))
	e.console.reset() // the re-apply of the (empty) list sends nothing anyway

	got := decode[opsJSON](t, e.must(200, "POST", path(devID, "/ops"), map[string]any{"name": "Steve_1"}, e.admin()))
	if !slices.Equal(got.Names, []string{"Steve_1"}) {
		t.Fatalf("names %v", got.Names)
	}
	e.must(200, "POST", path(devID, "/ops"), map[string]any{"name": "alex"}, e.admin())
	got = decode[opsJSON](t, e.must(200, "GET", path(devID, "/ops"), nil, e.admin()))
	if !slices.Equal(got.Names, []string{"alex", "Steve_1"}) {
		t.Fatalf("names %v", got.Names)
	}
	got = decode[opsJSON](t, e.must(200, "DELETE", path(devID, "/ops/Steve_1"), nil, e.admin()))
	if !slices.Equal(got.Names, []string{"alex"}) {
		t.Fatalf("names after delete %v", got.Names)
	}
	want := []string{
		fmt.Sprintf("%d op Steve_1", devID), fmt.Sprintf("%d op alex", devID), fmt.Sprintf("%d deop Steve_1", devID),
	}
	if c := e.console.sent(); !slices.Equal(c, want) {
		t.Fatalf("rcon %q, want %q", c, want)
	}
	add, rm := e.entries(audit.ActionDevOpAdd), e.entries(audit.ActionDevOpRemove)
	if len(add) != 2 || add[0].Details["name"] != "Steve_1" || len(rm) != 1 || rm[0].Details["name"] != "Steve_1" {
		t.Fatalf("audit add %+v remove %+v", add, rm)
	}
}

func TestOpsWhileStoppedOnlyStored(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(stopped(devID))
	e.must(200, "POST", path(devID, "/ops"), map[string]any{"name": "Steve"}, e.admin())
	e.must(200, "DELETE", path(devID, "/ops/Steve"), nil, e.admin())
	if c := e.console.sent(); len(c) != 0 {
		t.Fatalf("rcon %q for a stopped dev", c)
	}
}

func TestOpsRconFailureStillStored(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	e.pass(running(devID, t0, ptr(0)))
	e.console.fail = true
	got := decode[opsJSON](t, e.must(200, "POST", path(devID, "/ops"), map[string]any{"name": "Steve"}, e.admin()))
	if !slices.Equal(got.Names, []string{"Steve"}) {
		t.Fatalf("names %v", got.Names)
	}
}

func TestOpsValidation(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	for _, name := range []string{"ab", "a_very_long_name_17", "Steve say hi", "Stéve", "", "-op"} {
		rec := e.do("POST", path(devID, "/ops"), map[string]any{"name": name}, ptr(e.admin()))
		if rec.Code != 400 || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("POST %q: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := e.do("DELETE", path(devID, "/ops/a%20b"), nil, ptr(e.admin())); rec.Code != 400 {
		t.Errorf("DELETE invalid name: %d", rec.Code)
	}
	if rec := e.do("DELETE", path(devID, "/ops/Nobody"), nil, ptr(e.admin())); rec.Code != 404 {
		t.Errorf("DELETE unknown name: %d", rec.Code)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("audited %+v", e.rec.Entries)
	}
}

func TestOpsReappliedOncePerStart(t *testing.T) {
	e := newEnv(t)
	_, devID := e.dev("survival")
	for _, n := range []string{"Steve", "alex"} {
		e.must(200, "POST", path(devID, "/ops"), map[string]any{"name": n}, e.admin())
	}
	want := []string{fmt.Sprintf("%d op alex", devID), fmt.Sprintf("%d op Steve", devID)}

	s1 := t0.Add(time.Minute)
	e.clk.Set(s1)
	starting := running(devID, s1, nil) // container runs, server not answering yet
	e.pass(starting)
	if c := e.console.sent(); len(c) != 0 {
		t.Fatalf("rcon %q before the server answers", c)
	}
	up := func(started time.Time) monitoring.ServerSample { return running(devID, started, ptr(0)) }
	e.clk.Advance(30 * time.Second)
	e.pass(up(s1))
	if c := e.console.sent(); !slices.Equal(c, want) {
		t.Fatalf("rcon %q, want %q", c, want)
	}
	e.clk.Advance(30 * time.Second)
	e.pass(up(s1))
	if c := e.console.sent(); len(c) != 2 {
		t.Fatalf("re-applied twice for one start: %q", c)
	}

	// A new start: again, and an RCON failure is retried on the next pass.
	e.console.reset()
	e.console.fail = true
	s2 := e.clk.Now()
	e.clk.Advance(30 * time.Second)
	e.pass(up(s2))
	e.console.fail = false
	e.clk.Advance(30 * time.Second)
	e.pass(up(s2))
	if c := e.console.sent(); !slices.Equal(c, want) {
		t.Fatalf("after the second start rcon %q, want %q", c, want)
	}
}
