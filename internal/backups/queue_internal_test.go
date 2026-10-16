package backups

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// SEC-25: the queue is fair between servers. A server has at most one queued or running job,
// so one that queues its next job as soon as the last ended goes behind every server that
// waits already: it cannot starve the others.
func TestQueueIsRoundRobin(t *testing.T) {
	q := newQueue(clock.NewFake(time.Unix(0, 0)))
	var mu sync.Mutex
	var order []string
	done := make(chan struct{})
	var spec func(id int64, n int) jobSpec
	spec = func(id int64, n int) jobSpec {
		return jobSpec{serverID: id, kind: kindBackup, trigger: triggerScheduled,
			run: func(context.Context, *job) (string, error) {
				mu.Lock()
				order = append(order, fmt.Sprintf("%d/%d", id, n))
				if len(order) == 5 {
					close(done)
				}
				mu.Unlock()
				return "", nil
			},
			after: func() {
				if id == 1 && n < 3 { // server 1 queues its next backup at once
					if _, err := q.add(context.Background(), spec(id, n+1)); err != nil {
						t.Errorf("re-queue: %v", err)
					}
				}
			},
		}
	}
	for _, id := range []int64{1, 2, 3} {
		if _, err := q.add(context.Background(), spec(id, 1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.add(context.Background(), spec(1, 9)); err == nil {
		t.Fatal("a second job of the same server was queued")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go q.work(ctx)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("jobs did not run")
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"1/1", "2/1", "3/1", "1/2", "1/3"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}
