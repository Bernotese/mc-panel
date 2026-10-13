package backups

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// runFunc runs a job; the snapshot is the job's result (backup) or "".
type runFunc func(ctx context.Context, j *job) (snapshot string, err error)

// jobSpec describes a job to queue.
type jobSpec struct {
	serverID int64
	name     string // server name, for notifications
	kind     string
	trigger  string
	snapshot string // restore/delete: the source snapshot
	run      runFunc
	after    func() // runs after the job has ended (the restore gate is open by then); may be nil
}

type job struct {
	q      *queue
	spec   jobSpec
	view   jobView // guarded by q.mu
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (j *job) setStep(step string) {
	j.q.mu.Lock()
	defer j.q.mu.Unlock()
	j.view.Step, j.view.Bytes = step, 0
}

func (j *job) setBytes(n int64) {
	j.q.mu.Lock()
	defer j.q.mu.Unlock()
	j.view.Bytes = n
}

// queue keeps the backup jobs in memory (spec §5.3): the newest job per server (at most one
// queued or running), a FIFO of waiting jobs and at most one running job in the panel. The
// last result stays until the next job of the server or a panel restart.
type queue struct {
	clock   clock.Clock
	mu      sync.Mutex
	last    map[int64]*job
	waiting []*job
	running *job
	wake    chan struct{}
	closed  bool
	// gone reports whether the server was deleted; a taken job of such a server ends as
	// errServerDeleted without running (nil = never).
	gone func(ctx context.Context, serverID int64) bool
	log  *slog.Logger // nil = slog.Default()
}

func newQueue(clk clock.Clock) *queue {
	return &queue{clock: clk, last: map[int64]*job{}, wake: make(chan struct{}, 1)}
}

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func active(j *job) bool {
	return j != nil && (j.view.State == stateQueued || j.view.State == stateRunning)
}

// add queues a job. base only passes its values (actor, IP) to the job.
func (q *queue) add(base context.Context, s jobSpec) (jobView, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.closed:
		return jobView{}, errShuttingDown
	case active(q.last[s.serverID]):
		return jobView{}, errBackupRunning
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(base))
	j := &job{q: q, spec: s, ctx: ctx, cancel: cancel, done: make(chan struct{}), view: jobView{
		ID: newJobID(), Kind: s.kind, Trigger: s.trigger, State: stateQueued, Step: stepQueued,
		QueuedAt: q.clock.Now().UnixMilli(),
	}}
	if s.snapshot != "" {
		snap := s.snapshot
		j.view.Snapshot = &snap
	}
	q.last[s.serverID] = j
	q.waiting = append(q.waiting, j)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return q.viewLocked(j), nil
}

// viewLocked copies the job's view with its current queue position; call with q.mu held.
func (q *queue) viewLocked(j *job) jobView {
	v := j.view
	if v.Snapshot != nil {
		s := *v.Snapshot
		v.Snapshot = &s
	}
	if v.ErrorCode != nil {
		c := *v.ErrorCode
		v.ErrorCode = &c
	}
	if v.State == stateQueued {
		v.QueuePosition = slices.Index(q.waiting, j) + 1
	}
	return v
}

func (q *queue) get(serverID int64) *jobView {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.last[serverID]
	if j == nil {
		return nil
	}
	v := q.viewLocked(j)
	return &v
}

// busy reports whether the server has a queued or running job.
func (q *queue) busy(serverID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return active(q.last[serverID])
}

// restoreHeld reports whether the server has a restore job that waits or runs: the gate closes
// with the 202, not only when the worker picks the job up (another server's job may still run).
func (q *queue) restoreHeld(serverID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.last[serverID]
	return active(j) && j.spec.kind == kindRestore
}

// idle reports whether no job waits or runs.
func (q *queue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running == nil && len(q.waiting) == 0
}

// work runs queued jobs one at a time until ctx is done or the queue is closed.
func (q *queue) work(ctx context.Context) {
	for {
		j := q.take(ctx)
		if j == nil {
			return
		}
		if q.gone != nil && q.gone(j.ctx, j.spec.serverID) {
			q.finish(j, "", errServerDeleted)
		} else {
			snap, err := j.spec.run(j.ctx, j)
			if err != nil && !errors.Is(err, errServerDeleted) && q.gone != nil && q.gone(j.ctx, j.spec.serverID) {
				err = errServerDeleted // failed because its server went away: a cancel without log
			}
			q.finish(j, snap, err)
		}
		if j.spec.after != nil {
			j.spec.after()
		}
	}
}

func (q *queue) take(ctx context.Context) *job {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil
		}
		if len(q.waiting) > 0 {
			j := q.waiting[0]
			q.waiting = q.waiting[1:]
			q.running = j
			j.view.State, j.view.StartedAt = stateRunning, q.clock.Now().UnixMilli()
			q.mu.Unlock()
			return j
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-q.wake:
		}
	}
}

func (q *queue) finish(j *job, snapshot string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.view.FinishedAt = q.clock.Now().UnixMilli()
	err = canceledOr(j.ctx, err) // dropped or shut down: never an RCON or agent error
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errServerDeleted) {
		log := q.log
		if log == nil {
			log = slog.Default()
		}
		// the full error (it may name host paths) goes only to the log, the job shows a summary
		log.Warn("backups: job failed", "server_id", j.spec.serverID, "kind", j.spec.kind, "err", err)
	}
	if snapshot != "" {
		s := snapshot
		j.view.Snapshot = &s
	}
	if err != nil {
		code, msg := jobErrorOf(err)
		j.view.State, j.view.ErrorCode, j.view.Error = stateFailed, &code, msg
	} else {
		j.view.State, j.view.Step = stateDone, stepDone
	}
	if q.running == j {
		q.running = nil
	}
	j.cancel()
	close(j.done)
}

// cancelLocked ends a waiting job without running it; call with q.mu held.
func (q *queue) cancelLocked(j *job) {
	code := codeCanceled
	j.view.State, j.view.ErrorCode, j.view.Error = stateFailed, &code, msgCanceled
	j.view.FinishedAt = q.clock.Now().UnixMilli()
	j.cancel()
	close(j.done)
}

// drop ends the server's job: a waiting one is removed and fails as canceled, a running one is
// cancelled and awaited for at most wait.
func (q *queue) drop(serverID int64, wait time.Duration) {
	q.mu.Lock()
	j := q.last[serverID]
	if !active(j) {
		q.mu.Unlock()
		return
	}
	if j.view.State == stateQueued {
		if i := slices.Index(q.waiting, j); i >= 0 {
			q.waiting = slices.Delete(q.waiting, i, i+1)
		}
		q.cancelLocked(j)
		q.mu.Unlock()
		return
	}
	j.cancel()
	done := j.done
	q.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// close refuses new jobs, ends the waiting ones as canceled and cancels the running one.
func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	for _, j := range q.waiting {
		q.cancelLocked(j)
	}
	q.waiting = nil
	if q.running != nil {
		q.running.cancel()
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
