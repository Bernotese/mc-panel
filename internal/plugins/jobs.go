package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// maxJobs bounds the plugin jobs running in the panel (spec §6.4).
const maxJobs = 4

type stepState string

const (
	stepPending     stepState = "pending"
	stepDownloading stepState = "downloading"
	stepDone        stepState = "done"
	stepFailed      stepState = "failed"
	stepSkipped     stepState = "skipped"
)

const (
	jobInstall = "install"
	jobUpdate  = "update"

	jobStateRunning = "running"
	jobStateDone    = "done"
	jobStateFailed  = "failed"

	codeDownloadFailed      = "download_failed"
	codeHashMismatch        = "hash_mismatch"
	codeFileExists          = "file_exists"
	codeInsufficientStorage = "insufficient_storage"
	codeFilesUnavailable    = "files_unavailable"
	codeSourceUnavailable   = "source_unavailable"
)

// jobStep is the OpenAPI schema PluginJobStep.
type jobStep struct {
	Title     string    `json:"title"`
	File      string    `json:"file"`
	State     stepState `json:"state"`
	ErrorCode *string   `json:"error_code"`
	Error     string    `json:"error"`
}

// jobView is the OpenAPI schema PluginJob.
type jobView struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	Steps      []jobStep `json:"steps"`
	StartedAt  int64     `json:"started_at"`
	FinishedAt int64     `json:"finished_at"` // 0 while running
}

type job struct {
	reg    *jobRegistry
	view   jobView // guarded by reg.mu
	cancel context.CancelFunc
}

// jobRegistry keeps the newest job per server in memory: at most one running per server and
// maxJobs in the panel; the last result stays until the next job or a panel restart.
type jobRegistry struct {
	clock  clock.Clock
	max    int
	mu     sync.Mutex
	jobs   map[int64]*job
	active int
	closed bool
	wg     sync.WaitGroup
}

func newJobRegistry(clk clock.Clock, maxJobs int) *jobRegistry {
	return &jobRegistry{clock: clk, max: maxJobs, jobs: map[int64]*job{}}
}

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (v jobView) clone() jobView {
	v.Steps = slices.Clone(v.Steps)
	return v
}

// start runs run in the background as the server's job. base only passes its values (actor,
// IP) to the job; the job ends with run or when the registry shuts down.
func (r *jobRegistry) start(base context.Context, serverID int64, kind string, steps []jobStep, run func(ctx context.Context, j *job)) (jobView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		return jobView{}, errShuttingDown
	case r.jobs[serverID] != nil && r.jobs[serverID].view.State == jobStateRunning:
		return jobView{}, errJobRunning
	case r.active >= r.max:
		return jobView{}, errTooManyJobs
	}
	if steps == nil {
		steps = []jobStep{}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(base))
	j := &job{reg: r, cancel: cancel, view: jobView{
		ID: newJobID(), Kind: kind, State: jobStateRunning, Steps: steps, StartedAt: r.clock.Now().UnixMilli(),
	}}
	r.jobs[serverID] = j
	r.active++
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.finish(j)
		defer cancel()
		run(ctx, j)
	}()
	return j.view.clone(), nil
}

// finish ends a job: failed if a step failed, steps it never reached count as skipped.
func (r *jobRegistry) finish(j *job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j.view.State = jobStateDone
	for i := range j.view.Steps {
		switch j.view.Steps[i].State {
		case stepFailed:
			j.view.State = jobStateFailed
		case stepPending, stepDownloading:
			j.view.Steps[i].State = stepSkipped
		}
	}
	j.view.FinishedAt = r.clock.Now().UnixMilli()
	r.active--
}

func (r *jobRegistry) get(serverID int64) *jobView {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[serverID]
	if !ok {
		return nil
	}
	v := j.view.clone()
	return &v
}

func (r *jobRegistry) running(serverID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[serverID]
	return ok && j.view.State == jobStateRunning
}

// shutdown refuses new jobs, cancels the running ones and waits for them.
func (r *jobRegistry) shutdown() {
	r.mu.Lock()
	r.closed = true
	for _, j := range r.jobs {
		if j.view.State == jobStateRunning {
			j.cancel()
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
}

func (j *job) set(i int, s stepState) {
	j.reg.mu.Lock()
	defer j.reg.mu.Unlock()
	j.view.Steps[i].State = s
}

func (j *job) fail(i int, code, msg string) {
	j.reg.mu.Lock()
	defer j.reg.mu.Unlock()
	st := &j.view.Steps[i]
	st.State, st.ErrorCode, st.Error = stepFailed, &code, msg
}

func (h *Handler) jobRunning(serverID int64) bool { return h.jobs.running(serverID) }

type jobResponse struct {
	Job *jobView `json:"job"`
}

func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, jobResponse{Job: h.jobs.get(srv.ID)})
}
