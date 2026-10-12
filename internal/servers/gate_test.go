package servers_test

import (
	"context"
	"testing"

	"github.com/Bernotese/mc-panel/internal/servers"
)

func TestAnyGate(t *testing.T) {
	ctx := context.Background()
	a := &fakeGate{running: map[int64]bool{}}
	b := &fakeGate{running: map[int64]bool{}}
	g := servers.AnyGate(a, nil, b)
	if g.DataJobRunning(ctx, 1) {
		t.Fatal("no gate holds, AnyGate does")
	}
	b.set(2, true)
	if g.DataJobRunning(ctx, 1) || !g.DataJobRunning(ctx, 2) {
		t.Fatal("AnyGate must hold exactly the servers one of its gates holds")
	}
	a.set(2, true)
	asked := b.askedCount()
	if !g.DataJobRunning(ctx, 2) || b.askedCount() != asked {
		t.Fatal("the first holding gate decides; later gates are not asked")
	}
	if servers.AnyGate().DataJobRunning(ctx, 2) || servers.AnyGate(nil).DataJobRunning(ctx, 2) {
		t.Fatal("an empty AnyGate never holds")
	}
}
