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

// Short local names for the job states of filesapi.
const (
	jobRunning = filesapi.JobRunning
	jobDone    = filesapi.JobDone
	jobFailed  = filesapi.JobFailed
)

// jobLock takes an instance for an operation that is not a job (pin, purge): it excludes jobs
// of every kind but counts against no slot.
const jobLock filesapi.JobKind = "lock"

var (
	errShuttingDown = errors.New("the files service is shutting down")
	errCanceled     = errors.New("the job was canceled")
)

// jobSpec describes a job when it starts.
type jobSpec struct {
	kind                     filesapi.JobKind
	path, dest, snapshot     string
	totalBytes, totalEntries int64
}

// jobs tracks the jobs of all kinds (spec 3b §4.2, spec 4 §4.1): the last one per instance, at
// most one pre-check, running job or lock per instance, at most maxJobsTotal extracts and
// maxRestic restic jobs (backup, restore, forget) overall. A slot is taken before the
// pre-check, so pre-checks are throttled like jobs. Jobs live in memory; a restart forgets them
// (and CleanupTemp removes their temp folders).
type jobs struct {
	mu        sync.Mutex
	last      map[int64]*filesapi.Job
	busy      map[int64]filesapi.JobKind // instances with a pre-check, a running job or a lock
	slots     int                        // extract pre-checks and jobs
	restic    int                        // restic pre-checks and jobs
	maxRestic int
	cancels   map[int64]context.CancelFunc
	committed map[int64]bool // restores in their swap: cancel has no effect any more
	readers   map[int64]int  // running backup downloads per instance
	closed    bool           // Close was called: no new jobs (wg.Add only happens under mu)
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	hook      func(ctx context.Context, id int64, entry string) // tests: runs before each extracted entry
	scrub     func(string) string                               // job errors without host paths (Service.publicMessage)
}

func newJobs(maxRestic int) *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobs{
		last: map[int64]*filesapi.Job{}, busy: map[int64]filesapi.JobKind{}, maxRestic: max(maxRestic, 1),
		cancels: map[int64]context.CancelFunc{}, committed: map[int64]bool{}, readers: map[int64]int{}, ctx: ctx, cancel: cancel,
	}
}

func isResticKind(k filesapi.JobKind) bool {
	return k == filesapi.JobBackup || k == filesapi.JobRestore || k == filesapi.JobForget
}

// busyText describes what holds an instance.
func busyText(k filesapi.JobKind) string {
	switch k {
	case filesapi.JobExtract:
		return "an archive is already being extracted for this server"
	case filesapi.JobBackup:
		return "a backup is running for this server"
	case filesapi.JobRestore:
		return "a restore is running for this server"
	case filesapi.JobForget:
		return "backups of this server are being deleted"
	}
	return "another backup operation is running for this server"
}

// acquire takes the slot of instance id for a pre-check of the given kind; the caller must
// either start a job with it or give it back with release.
func (j *jobs) acquire(id int64, kind filesapi.JobKind) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.acquireLocked(id, kind)
}

func (j *jobs) acquireLocked(id int64, kind filesapi.JobKind) error {
	switch {
	case j.closed:
		return errShuttingDown
	case j.busy[id] != "":
		return fmt.Errorf("%w: %s", filesapi.ErrBusy, busyText(j.busy[id]))
	case (kind == filesapi.JobForget || kind == jobLock) && j.readers[id] > 0:
		return fmt.Errorf("%w: a backup download is running for this server", filesapi.ErrBusy)
	case kind == filesapi.JobExtract && j.slots >= maxJobsTotal:
		return fmt.Errorf("%w: too many archives are being extracted", filesapi.ErrBusy)
	case isResticKind(kind) && j.restic >= j.maxRestic:
		return fmt.Errorf("%w: too many backup jobs are running", filesapi.ErrBusy)
	}
	j.busy[id] = kind
	switch {
	case kind == filesapi.JobExtract:
		j.slots++
	case isResticKind(kind):
		j.restic++
	}
	return nil
}

// release gives back a slot that did not become a job (or a lock).
func (j *jobs) release(id int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.freeLocked(id)
}

func (j *jobs) freeLocked(id int64) {
	kind, ok := j.busy[id]
	if !ok {
		return
	}
	delete(j.busy, id)
	switch {
	case kind == filesapi.JobExtract:
		j.slots--
	case isResticKind(kind):
		j.restic--
	}
}

// start turns the slot of instance id into a running job: the job is registered as running
// before start returns (so a GET /v1/jobs after the 202 always sees it), then run executes in
// the background with its own context (child of the jobs' context, cancelled by cancelJob or
// close). On error the slot is given back.
func (j *jobs) start(id int64, spec jobSpec, now func() time.Time, run func(ctx context.Context) error) (filesapi.Job, error) {
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
		ID: hex.EncodeToString(b[:]), Kind: spec.kind, State: jobRunning, Path: spec.path, Dest: spec.dest,
		Snapshot: spec.snapshot, TotalBytes: spec.totalBytes, TotalEntries: spec.totalEntries, StartedAt: now().UnixMilli(),
	}
	jctx, cancel := context.WithCancel(j.ctx)
	j.last[id] = job
	j.cancels[id] = cancel
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		err := run(jctx)
		cancel()
		j.finish(id, err, now())
	}()
	return *job, nil
}

func (j *jobs) update(id int64, f func(*filesapi.Job)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		f(job)
	}
}

// progress reports one written entry with bytes bytes.
func (j *jobs) progress(id, bytes int64) {
	j.update(id, func(job *filesapi.Job) { job.Bytes += bytes; job.Entries++ })
}

// skip reports one left-out entry (spec 4 §4.2).
func (j *jobs) skip(id int64) { j.update(id, func(job *filesapi.Job) { job.Skipped++ }) }

func (j *jobs) setSnapshot(id int64, sid string) {
	j.update(id, func(job *filesapi.Job) { job.Snapshot = sid })
}

// stopErr is the error of a job whose context ended: the service shuts down, or cancelJob.
func (j *jobs) stopErr() error {
	if j.ctx.Err() != nil {
		return errShuttingDown
	}
	return errCanceled
}

func (j *jobs) finish(id int64, err error, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		job.FinishedAt = now.UnixMilli()
		if err != nil {
			job.State = jobFailed
			job.Error = err.Error()
			if j.scrub != nil {
				job.Error = j.scrub(job.Error)
			}
			// the wire code of the error (too_large, insufficient_storage, busy, unavailable, …)
			// so the panel can map it (V1/V5); "" for errors without a sentinel
			job.Code, _ = filesapi.CodeOf(err)
		} else {
			job.State = jobDone
		}
	}
	delete(j.cancels, id)
	delete(j.committed, id)
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

// Job returns a copy of the last job (any kind) of server id, or nil.
func (s *Service) Job(id int64) *filesapi.Job { return s.jobs.get(id) }

// cancelJob cancels the running job of instance id (spec 4 §4.5): nothing running is fine; an
// extract cannot be cancelled; a restore that started its swap is not interrupted any more.
func (j *jobs) cancelJob(id int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.last[id]
	switch {
	case job == nil || job.State != jobRunning:
		return nil
	case job.Kind == filesapi.JobExtract:
		return fmt.Errorf("%w: an extract job cannot be canceled", filesapi.ErrInvalid)
	case j.committed[id]:
		return nil
	}
	if c := j.cancels[id]; c != nil {
		c()
	}
	return nil
}

// beginCommit marks the restore of instance id as swapping: from now on cancelJob has no
// effect. false = it was cancelled (or the service shuts down) before.
func (j *jobs) beginCommit(ctx context.Context, id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	j.committed[id] = true
	return true
}

// lock takes instance id for pin or purge (no job; excludes every job and download).
func (j *jobs) lock(id int64) (func(), error) {
	if err := j.acquire(id, jobLock); err != nil {
		return nil, err
	}
	return func() { j.release(id) }, nil
}

// addReader registers a backup download of instance id (spec 4 §4.4): refused while a forget
// runs (prune holds an exclusive repository lock); a forget, pin or purge is refused while one
// runs.
func (j *jobs) addReader(id int64) (func(), error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.closed:
		return nil, errShuttingDown
	case j.busy[id] == filesapi.JobForget || j.busy[id] == jobLock:
		return nil, fmt.Errorf("%w: %s", filesapi.ErrBusy, busyText(j.busy[id]))
	}
	j.readers[id]++
	return func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.readers[id]--; j.readers[id] <= 0 {
			delete(j.readers, id)
		}
	}, nil
}
