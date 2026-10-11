package serverfiles

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// gateTimeout bounds the job query of ExtractRunning: power actions and the reconcile loop
// must not hang on an unreachable service.
const gateTimeout = 2 * time.Second

type extractRequest struct {
	Path string `json:"path"`
	Dest string `json:"dest"`
}

// jobsJSON is the OpenAPI schema FileJobResponse.
type jobsJSON struct {
	Job *filesapi.Job `json:"job"`
}

var errRunning = errors.New("the server is running")

func (h *Handler) handleExtract(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req extractRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, ok := cleanItemPath(w, r, req.Path)
	if !ok {
		return
	}
	dest, ok := cleanItemPath(w, r, req.Dest)
	if !ok {
		return
	}
	// From the state check until the job exists, the server counts as extracting: a start
	// asking the gate meanwhile is held (Review Focus #5).
	defer h.pending.add(s.ID)()
	if err := h.checkStopped(ctx, s.ID); err != nil {
		h.writeStateError(w, r, err)
		return
	}
	job, err := h.files.Extract(context.WithoutCancel(ctx), filesapi.ExtractRequest{ID: s.ID, Path: p, Dest: dest})
	h.record(ctx, audit.ActionFileExtract, s.ID, result(err), map[string]any{"path": p, "dest": dest})
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	job, err := h.files.Job(r.Context(), s.ID)
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, jobsJSON{Job: job})
}

// checkStopped asks the agent for the actual state (spec §5.1): only a server whose container
// is exited, created or missing – or that the agent does not list at all – and that the agent
// is not working on may be extracted into.
func (h *Handler) checkStopped(ctx context.Context, id int64) error {
	resp, err := h.inst.Instances(ctx)
	if err != nil {
		return err
	}
	if resp.Docker != agentapi.DockerOK {
		return agentapi.ErrDockerUnavailable
	}
	for _, st := range resp.Instances {
		if st.ID != id {
			continue
		}
		if st.Operation != agentapi.OpNone {
			return errRunning
		}
		switch st.State {
		case agentapi.StateExited, agentapi.StateCreated, agentapi.StateMissing:
			return nil
		}
		return errRunning
	}
	return nil
}

func (h *Handler) writeStateError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errRunning):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeInstanceRunning, "stop the server before extracting an archive")
	case r.Context().Err() != nil:
		h.log.DebugContext(r.Context(), "state check ended with the request", "err", err)
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeDockerUnavailable, "docker is not available on the host")
	default:
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the agent is not reachable")
	}
}

// pendingExtracts counts the extract requests per server between the state check and the
// answer of the service.
type pendingExtracts struct {
	mu sync.Mutex
	n  map[int64]int
}

func newPendingExtracts() *pendingExtracts { return &pendingExtracts{n: map[int64]int{}} }

// add marks one request for id; the returned function removes the mark.
func (p *pendingExtracts) add(id int64) (done func()) {
	p.mu.Lock()
	p.n[id]++
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.n[id]--; p.n[id] <= 0 {
			delete(p.n, id)
		}
	}
}

func (p *pendingExtracts) has(id int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[id] > 0
}

// ExtractRunning reports whether an extract job runs for the server, or an extract request is
// between its state check and the service's answer. An unreachable service counts as "no job"
// (spec §5.1). It implements servers.ExtractGate.
func (h *Handler) ExtractRunning(ctx context.Context, serverID int64) bool {
	if h.pending.has(serverID) {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()
	job, err := h.files.Job(ctx, serverID)
	return err == nil && job != nil && job.State == filesapi.JobRunning
}
