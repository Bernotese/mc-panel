package agent

import (
	"context"
	"errors"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// Waker is the waker part of the instance manager (instances.Manager implements it).
type Waker interface {
	SetWaker(ctx context.Context, req agentapi.WakerRequest) (agentapi.WakerResponse, error)
}

// WithWaker enables PUT /v1/waker. Without it the endpoint answers as if Docker were
// unavailable.
func WithWaker(w Waker) Option { return func(s *Server) { s.waker = w } }

// setWaker serves PUT /v1/waker: the complete list of sleeping dev instances, answered with
// the ids a player tried to join since the previous call.
func (s *Server) setWaker(w http.ResponseWriter, r *http.Request) {
	var req agentapi.WakerRequest
	if !s.decode(w, r, &req, agentapi.MaxWakerRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	resp, err := s.waker.SetWaker(r.Context(), req)
	switch {
	case err == nil:
		if len(resp.Wakes) > 0 {
			s.log.Info("dev instance wake requested", "ids", resp.Wakes, "peer_uid", peerUID(r))
		}
		writeJSON(w, http.StatusOK, resp)
	case errors.Is(err, instancespec.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable, err)
	default:
		s.log.Error("waker failed", "sleeping", len(req.Sleeping), "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "waker failed"})
	}
}

// noWaker is the waker backend of a server without WithWaker.
type noWaker struct{}

func (noWaker) SetWaker(context.Context, agentapi.WakerRequest) (agentapi.WakerResponse, error) {
	return agentapi.WakerResponse{}, agentapi.ErrDockerUnavailable
}
