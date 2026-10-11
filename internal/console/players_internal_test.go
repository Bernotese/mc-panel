package console

import (
	"context"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// countingAgent answers "list" and fails for a cancelled context, like the real client.
type countingAgent struct {
	noAgent
	calls int
}

func (a *countingAgent) Rcon(ctx context.Context, _ int64, _ string) (string, error) {
	a.calls++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "There are 0 of a max of 20 players online: ", nil
}

// An answer computed for a caller that went away is not cached.
func TestOnlineNotCachedForCancelledCaller(t *testing.T) {
	ag := &countingAgent{}
	h := NewHandler(Deps{Clock: clock.NewFake(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)), Agent: ag})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := h.onlinePlayers(ctx, 1); got.Available {
		t.Fatalf("cancelled caller got %+v", got)
	}
	if got := h.onlinePlayers(context.Background(), 1); !got.Available || ag.calls != 2 {
		t.Fatalf("second caller: %+v after %d calls, want a fresh answer (2 calls)", got, ag.calls)
	}
	_ = h.onlinePlayers(context.Background(), 1)
	if ag.calls != 2 {
		t.Fatalf("third caller: %d calls, want the cached answer", ag.calls)
	}
}
