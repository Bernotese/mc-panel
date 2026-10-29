package instances_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

// TestStatusReportsConfirmedRemovals: Status lists an instance as removed only after a remove
// job of it succeeded, and not once another job of it ran (issue #148: the panel completes a
// deletion only on this confirmation).
func TestStatusReportsConfirmedRemovals(t *testing.T) {
	e := newEnv(t)
	e.runningInstance(t, 1)
	e.runningInstance(t, 2)
	removed := func() []int64 { return e.m.Status(context.Background()).Removed }
	if got := removed(); len(got) != 0 {
		t.Fatalf("removed before any remove = %v", got)
	}

	e.d.Fail(dockertest.OpRemove, 500, "daemon trouble")
	if err := e.m.Remove(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	e.m.Wait()
	if got := removed(); len(got) != 0 {
		t.Fatalf("a failed remove counts as removed: %v", got)
	}
	e.d.Clear()
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 2) })
	if got := removed(); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("removed = %v", got)
	}
	// a remove of an instance that is already gone is confirmed again (the panel asks once more
	// after an agent restart)
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 2) })
	if got := removed(); !slices.Equal(got, []int64{1, 2}) {
		t.Fatalf("removed after a repeated remove = %v", got)
	}
	e.ensure(t, ensureReq(2, "survival", 1))
	if got := removed(); !slices.Equal(got, []int64{1}) {
		t.Fatalf("removed after an ensure = %v", got)
	}
}
