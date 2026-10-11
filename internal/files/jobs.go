package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Short local names for the job states of filesapi (V1/Phase A).
const (
	jobRunning = filesapi.JobRunning
	jobDone    = filesapi.JobDone
	jobFailed  = filesapi.JobFailed
)

var errShuttingDown = errors.New("the files service is shutting down")

// jobs tracks extract jobs: the last one per instance, at most one pre-check or running job
// per instance and maxJobsTotal overall. A slot is taken before the archive is even parsed, so
// the pre-checks are throttled like the jobs. Jobs live in memory; a restart forgets them (and
// CleanupTemp removes their temp folders).
type jobs struct {
	mu     sync.Mutex
	last   map[int64]*filesapi.Job
	busy   map[int64]bool // instances with a pre-check or a running job
	slots  int            // pre-checks plus running jobs
	closed bool           // Close was called: no new jobs (wg.Add only happens under mu)
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	hook   func(ctx context.Context, id int64, entry string) // tests: runs before each entry
}

func newJobs() *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobs{last: map[int64]*filesapi.Job{}, busy: map[int64]bool{}, ctx: ctx, cancel: cancel}
}

// acquire takes the slot of instance id for a pre-check; the caller must either start a job
// with it or give it back with release.
func (j *jobs) acquire(id int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.closed:
		return errShuttingDown
	case j.busy[id]:
		return fmt.Errorf("%w: an archive is already being extracted for this server", filesapi.ErrBusy)
	case j.slots >= maxJobsTotal:
		return fmt.Errorf("%w: too many archives are being extracted", filesapi.ErrBusy)
	}
	j.busy[id] = true
	j.slots++
	return nil
}

// release gives back a slot that did not become a job.
func (j *jobs) release(id int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.freeLocked(id)
}

func (j *jobs) freeLocked(id int64) {
	if j.busy[id] {
		delete(j.busy, id)
		j.slots--
	}
}

// start turns the slot of instance id into a running job: the job is registered as running
// before start returns (so a GET /v1/jobs after the 202 always sees it), then run executes in
// the background with the jobs' context. On error the slot is given back.
func (j *jobs) start(id int64, src, dest string, plan archivePlan, now func() time.Time, run func(context.Context) error) (filesapi.Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		j.freeLocked(id)
		return filesapi.Job{}, errShuttingDown
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		j.freeLocked(id)
		return filesapi.Job{}, err
	}
	job := &filesapi.Job{
		ID: hex.EncodeToString(b[:]), State: jobRunning, Path: src, Dest: dest,
		TotalBytes: plan.bytes, TotalEntries: plan.entries, StartedAt: now().UnixMilli(),
	}
	j.last[id] = job
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		err := run(j.ctx)
		j.finish(id, err, now())
	}()
	return *job, nil
}

func (j *jobs) progress(id, bytes int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		job.Bytes += bytes
		job.Entries++
	}
}

func (j *jobs) finish(id int64, err error, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		job.FinishedAt = now.UnixMilli()
		if err != nil {
			job.State = jobFailed
			job.Error = err.Error()
		} else {
			job.State = jobDone
		}
	}
	j.freeLocked(id)
}

// close stops new jobs, cancels the running ones and waits for them. It may be called more
// than once.
func (j *jobs) close() {
	j.mu.Lock()
	j.closed = true
	j.mu.Unlock()
	j.cancel()
	j.wg.Wait()
}

func (j *jobs) get(id int64) *filesapi.Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.last[id]
	if job == nil {
		return nil
	}
	c := *job
	return &c
}

// Job returns a copy of the last extract job of server id, or nil.
func (s *Service) Job(id int64) *filesapi.Job { return s.jobs.get(id) }
