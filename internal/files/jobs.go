package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// jobs tracks extract jobs: the last one per instance, at most one running per instance and
// maxJobsTotal overall. Jobs live in memory; a restart forgets them (and CleanupTemp removes
// their temp folders).
type jobs struct {
	mu      sync.Mutex
	last    map[int64]*filesapi.Job
	running int
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	hook    func(ctx context.Context, id int64, entry string) // tests: runs before each entry
}

func newJobs() *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobs{last: map[int64]*filesapi.Job{}, ctx: ctx, cancel: cancel}
}

func (j *jobs) start(id int64, src, dest string, plan archivePlan, now time.Time) (filesapi.Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if cur := j.last[id]; cur != nil && cur.State == jobRunning {
		return filesapi.Job{}, fmt.Errorf("%w: an archive is already being extracted for this server", filesapi.ErrBusy)
	}
	if j.running >= maxJobsTotal {
		return filesapi.Job{}, fmt.Errorf("%w: too many archives are being extracted", filesapi.ErrBusy)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return filesapi.Job{}, err
	}
	job := &filesapi.Job{
		ID: hex.EncodeToString(b[:]), State: jobRunning, Path: src, Dest: dest,
		TotalBytes: plan.bytes, TotalEntries: plan.entries, StartedAt: now.UnixMilli(),
	}
	j.last[id] = job
	j.running++
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
	job := j.last[id]
	if job == nil {
		return
	}
	job.FinishedAt = now.UnixMilli()
	if err != nil {
		job.State = jobFailed
		job.Error = err.Error()
	} else {
		job.State = jobDone
	}
	j.running--
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
