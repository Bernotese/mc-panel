package instances_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

// runningInstance creates instance id and starts it, so a stop has something to stop.
func (e *env) runningInstance(t *testing.T, id int64) {
	t.Helper()
	e.ensure(t, ensureReq(id, "survival", 1))
	e.do(t, func(ctx context.Context) error { return e.m.Power(ctx, id, agentapi.PowerStart) })
	if s := e.status(t, id); s.State != agentapi.StateRunning || !s.Settled() {
		t.Fatalf("status after start = %+v", s)
	}
}

// TestStatusShowsQueuedStartBehindStop: a start queued behind a running stop is
// reported in queued, so the panel never takes the stopped container for settled
// (data safety spec §2.2).
func TestStatusShowsQueuedStartBehindStop(t *testing.T) {
	e := newEnv(t)
	e.runningInstance(t, 1)
	ctx := context.Background()

	arrived, release := e.d.Hold(dockertest.OpStop)
	defer release()
	if err := e.m.Power(ctx, 1, agentapi.PowerStop); err != nil {
		t.Fatal(err)
	}
	<-arrived
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatal(err)
	}
	s := e.status(t, 1)
	if s.Operation != agentapi.OpStopping || !slices.Equal(s.Queued, []agentapi.PowerAction{agentapi.PowerStart}) {
		t.Fatalf("operation = %q, queued = %v; want stopping, [start]", s.Operation, s.Queued)
	}
	if s.Settled() {
		t.Fatal("settled with a stop running and a start queued")
	}
	release()
	e.m.Wait()
	if s := e.status(t, 1); s.State != agentapi.StateRunning || !s.Settled() {
		t.Fatalf("status after the queue drained = %+v", s)
	}
}

// TestStatusSettledAfterQueueDrains: queued keeps the order of the power actions
// (ensure jobs are no power action and show up only as the operation once they run),
// and an instance is settled, with "queued": [] on the wire, once the queue drained.
func TestStatusSettledAfterQueueDrains(t *testing.T) {
	e := newEnv(t)
	e.runningInstance(t, 1)
	ctx := context.Background()

	arrived, release := e.d.Hold(dockertest.OpStop)
	defer release()
	if err := e.m.Power(ctx, 1, agentapi.PowerStop); err != nil {
		t.Fatal(err)
	}
	<-arrived
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatal(err)
	}
	recreate := ensureReq(1, "survival", 2)
	recreate.Recreate = true
	if err := e.m.Ensure(ctx, recreate); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Power(ctx, 1, agentapi.PowerRestart); err != nil {
		t.Fatal(err)
	}
	want := []agentapi.PowerAction{agentapi.PowerStart, agentapi.PowerRestart}
	if s := e.status(t, 1); s.Operation != agentapi.OpStopping || !slices.Equal(s.Queued, want) {
		t.Fatalf("operation = %q, queued = %v; want stopping, %v", s.Operation, s.Queued, want)
	}
	release()
	e.m.Wait()

	resp := e.m.Status(ctx)
	if len(resp.Instances) != 1 || !resp.Instances[0].Settled() || resp.Instances[0].Revision != 2 {
		t.Fatalf("status after the queue drained = %+v", resp.Instances)
	}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"queued":[]`)) || bytes.Contains(body, []byte(`"queued":null`)) {
		t.Fatalf("queued on the wire: %s", body)
	}
}

// TestStatusQueuedForMissingContainer: work queued for an instance without a container
// (state "missing") reports its queue too.
func TestStatusQueuedForMissingContainer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	arrived, release := e.d.Hold(dockertest.OpCreate)
	defer release()
	if err := e.m.Ensure(ctx, ensureReq(1, "survival", 1)); err != nil {
		t.Fatal(err)
	}
	<-arrived
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatal(err)
	}
	s := e.status(t, 1)
	if s.State != agentapi.StateMissing || s.Operation != agentapi.OpCreating ||
		!slices.Equal(s.Queued, []agentapi.PowerAction{agentapi.PowerStart}) {
		t.Fatalf("status while creating = %+v", s)
	}
	release()
	e.m.Wait()
	if s := e.status(t, 1); s.State != agentapi.StateRunning || !s.Settled() {
		t.Fatalf("status after the queue drained = %+v", s)
	}
}

// TestStatusUncertainWhenInspectFails: a container the agent cannot inspect keeps the listing's
// state, which may be out of date, and is never reported as settled (data safety spec §2.2).
func TestStatusUncertainWhenInspectFails(t *testing.T) {
	e := newEnv(t)
	e.runningInstance(t, 1)
	e.d.Fail(dockertest.OpInspect, 500, "daemon trouble")
	s := e.status(t, 1)
	if !s.Uncertain || s.Settled() {
		t.Fatalf("status with a failing inspect = %+v", s)
	}
	e.d.Clear()
	if s := e.status(t, 1); s.Uncertain || !s.Settled() {
		t.Fatalf("status once inspect works = %+v", s)
	}
}
