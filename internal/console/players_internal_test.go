package console

import (
	"context"
	"sync"
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

// blockingAgent answers "list" only after release is closed and counts the calls.
type blockingAgent struct {
	noAgent
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (a *blockingAgent) Rcon(ctx context.Context, _ int64, _ string) (string, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	select {
	case <-a.release:
		return "There are 0 of a max of 20 players online: ", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Concurrent viewers of one server wait for the request in flight instead of asking the agent
// themselves (one RCON call, one pair of RCON lines in the server log).
func TestOnlineConcurrentCallersShareOneCall(t *testing.T) {
	ag := &blockingAgent{release: make(chan struct{})}
	h := NewHandler(Deps{Clock: clock.NewFake(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)), Agent: ag})
	const n = 8
	var started, done sync.WaitGroup
	results := make([]onlinePlayers, n)
	for i := range n {
		started.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			started.Done()
			results[i] = h.onlinePlayers(context.Background(), 1)
		}()
	}
	started.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ag.mu.Lock()
		calls := ag.calls
		ag.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no caller reached the agent")
		}
		time.Sleep(time.Millisecond)
	}
	close(ag.release)
	done.Wait()
	if ag.calls != 1 {
		t.Fatalf("%d rcon calls for %d concurrent callers, want 1", ag.calls, n)
	}
	for i, r := range results {
		if !r.Available {
			t.Fatalf("caller %d got %+v", i, r)
		}
	}
}
