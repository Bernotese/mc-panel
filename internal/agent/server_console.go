package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	// MaxLogStreams bounds the log streams open at once (spec §5.2).
	MaxLogStreams = 16
	// streamWriteTimeout is the deadline of every write of a log stream (spec §5.2).
	streamWriteTimeout = 30 * time.Second
	// rconWriteTimeout covers one console command (instances.RconTimeout = 10 s) plus margin.
	rconWriteTimeout = 20 * time.Second
)

// Console is the console backend of the server (*instances.Manager).
type Console interface {
	// Logs validates req, inspects the container, opens its log, calls start exactly once
	// before the first line with the container state seen when the stream opened, and then
	// emit for every line until the log ends (nil), ctx ends or emit fails. A stopped
	// container delivers its history and the stream ends.
	Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error
	Rcon(ctx context.Context, id int64, command string) (string, error)
	Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error)
}

// WithConsole enables the console endpoints. Without it they answer as if Docker were
// unavailable.
func WithConsole(c Console) Option { return func(s *Server) { s.console = c } }

// longRequest lifts the server-wide deadlines for a request that legitimately runs longer
// than ReadTimeout/WriteTimeout. The read deadline must go: once the body is consumed,
// net/http keeps reading in the background to notice a closed connection and cancels the
// request context when that read times out. write > 0 sets a new write deadline.
// Deadlines are I/O deadlines on the real socket (time.Now, not the business clock);
// httptest recorders do not support them, which is ignored.
func longRequest(w http.ResponseWriter, write time.Duration) *http.ResponseController {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	if write > 0 {
		_ = rc.SetWriteDeadline(time.Now().Add(write))
	}
	return rc
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	var req agentapi.LogsRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		s.rejectCode(w, r, http.StatusTooManyRequests, agentapi.ErrorCodeBusy,
			fmt.Errorf("%w: %d log streams are open", agentapi.ErrBusy, MaxLogStreams))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.streamCtx, cancel) // Shutdown ends the stream
	defer stop()

	// The first deadline also covers the inspect and Docker opening the log before start.
	rc := longRequest(w, streamWriteTimeout)
	enc := json.NewEncoder(w)
	started := false
	start := func(running bool) {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set(agentapi.HeaderContainerRunning, strconv.FormatBool(running))
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()
	}
	emit := func(line agentapi.LogLine) error {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		if err := enc.Encode(line); err != nil {
			return err
		}
		return rc.Flush()
	}
	err := s.console.Logs(ctx, req, start, emit)
	switch {
	case started:
		if err != nil && ctx.Err() == nil {
			s.log.Warn("log stream ended with an error", "id", req.ID, "err", err, "peer_uid", peerUID(r))
		}
	case err == nil:
		start(false) // a backend that returned without opening a stream: an empty log
	default:
		s.consoleError(w, r, "logs", req.ID, err)
	}
}

func (s *Server) rcon(w http.ResponseWriter, r *http.Request) {
	var req agentapi.RconRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	longRequest(w, rconWriteTimeout)
	out, err := s.console.Rcon(r.Context(), req.ID, req.Command)
	if err != nil {
		s.consoleError(w, r, "rcon", req.ID, err)
		return
	}
	// The panel audits the command text; the agent logs only that one ran.
	s.log.Info("console command", "id", req.ID, "bytes", len(req.Command), "peer_uid", peerUID(r))
	writeJSON(w, http.StatusOK, agentapi.RconResponse{Output: out})
}

func (s *Server) players(w http.ResponseWriter, r *http.Request) {
	var req agentapi.PlayersRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	resp, err := s.console.Players(r.Context(), req.ID)
	if err != nil {
		s.consoleError(w, r, "players", req.ID, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// consoleError maps console errors to status and code; anything unknown becomes a generic
// 500 with the details only in the log.
func (s *Server) consoleError(w http.ResponseWriter, r *http.Request, op string, id int64, err error) {
	switch {
	case errors.Is(err, instancespec.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	case errors.Is(err, agentapi.ErrNotFound):
		s.rejectCode(w, r, http.StatusNotFound, agentapi.ErrorCodeNotFound, err)
	case errors.Is(err, agentapi.ErrNotRunning):
		s.rejectCode(w, r, http.StatusConflict, agentapi.ErrorCodeNotRunning, err)
	case errors.Is(err, agentapi.ErrBusy):
		s.rejectCode(w, r, http.StatusTooManyRequests, agentapi.ErrorCodeBusy, err)
	case errors.Is(err, agentapi.ErrRconUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeRconUnavailable, err)
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable, err)
	default:
		s.log.Error("console operation failed", "op", op, "id", id, "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "console operation failed"})
	}
}

// noConsole is the backend of a server without WithConsole (dev without Docker).
type noConsole struct{}

func (noConsole) Logs(context.Context, agentapi.LogsRequest, func(bool), func(agentapi.LogLine) error) error {
	return agentapi.ErrDockerUnavailable
}

func (noConsole) Rcon(context.Context, int64, string) (string, error) {
	return "", agentapi.ErrDockerUnavailable
}

func (noConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrDockerUnavailable
}
