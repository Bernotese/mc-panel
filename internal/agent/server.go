// Package agent implements the privileged mc-panel agent: the HTTP server on
// the Unix socket (Server, Listen) and the panel-side client (Client).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

const maxRequestBytes = 4 << 10

type route struct {
	method  string
	handler http.HandlerFunc
}

// Instances is the instance backend of the server (*instances.Manager).
type Instances interface {
	DockerStatus() string
	Status(ctx context.Context) agentapi.InstancesResponse
	Ensure(ctx context.Context, req agentapi.EnsureRequest) error
	Power(ctx context.Context, id int64, action agentapi.PowerAction) error
	Remove(ctx context.Context, id int64) error
}

// Option configures a Server.
type Option func(*Server)

// WithInstances enables the /v1/instances endpoints. Without it they answer as
// if Docker were unavailable.
func WithInstances(in Instances) Option { return func(s *Server) { s.inst = in } }

// Server serves the agentapi endpoints. It implements http.Handler; Serve
// adds the peer UID to each request context and applies conservative timeouts.
type Server struct {
	fw      agentapi.Firewall
	inst    Instances
	console Console
	version string
	log     *slog.Logger
	routes  map[string]route
	srv     *http.Server

	streams     chan struct{}   // one token per open log stream (MaxLogStreams)
	streamCtx   context.Context // cancelled by Shutdown: ends all log streams
	stopStreams context.CancelFunc

	readTimeout, writeTimeout time.Duration
}

func NewServer(fw agentapi.Firewall, version string, log *slog.Logger, opts ...Option) *Server {
	s := &Server{
		fw: fw, inst: noInstances{}, console: noConsole{}, version: version, log: log,
		streams: make(chan struct{}, MaxLogStreams), readTimeout: 5 * time.Second, writeTimeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	// Shutdown waits for running requests, and a log stream only ends with its context.
	s.streamCtx, s.stopStreams = context.WithCancel(context.Background()) //nolint:gosec // G118: stopStreams runs on Shutdown (RegisterOnShutdown below)
	s.routes = map[string]route{
		agentapi.PathHealth:          {method: http.MethodGet, handler: s.health},
		agentapi.PathBan:             {method: http.MethodPost, handler: s.ban},
		agentapi.PathUnban:           {method: http.MethodPost, handler: s.unban},
		agentapi.PathBans:            {method: http.MethodGet, handler: s.bans},
		agentapi.PathInstances:       {method: http.MethodGet, handler: s.instances},
		agentapi.PathInstanceEnsure:  {method: http.MethodPost, handler: s.ensure},
		agentapi.PathInstancePower:   {method: http.MethodPost, handler: s.power},
		agentapi.PathInstanceRemove:  {method: http.MethodPost, handler: s.remove},
		agentapi.PathInstanceLogs:    {method: http.MethodPost, handler: s.logs},
		agentapi.PathInstanceRcon:    {method: http.MethodPost, handler: s.rcon},
		agentapi.PathInstancePlayers: {method: http.MethodPost, handler: s.players},
	}
	s.srv = &http.Server{
		Handler:           s,
		ConnContext:       peercred.ConnContext,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       s.readTimeout,
		WriteTimeout:      s.writeTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	s.srv.RegisterOnShutdown(s.stopStreams)
	return s
}

// Serve accepts connections on l until Shutdown is called; it then returns nil.
func (s *Server) Serve(l net.Listener) error {
	if err := s.srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting, closes the listener (removing the socket file)
// and waits for running requests.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.routes[r.URL.Path]
	if !ok {
		s.reject(w, r, http.StatusNotFound, errors.New("not found"))
		return
	}
	if r.Method != rt.method {
		w.Header().Set("Allow", rt.method)
		s.reject(w, r, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
		return
	}
	if r.Method == http.MethodPost {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			s.reject(w, r, http.StatusUnsupportedMediaType, errors.New("content type must be application/json"))
			return
		}
	}
	rt.handler(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, agentapi.HealthResponse{
		Status: "ok", Version: s.version, Firewall: s.fw.Name(), Docker: s.inst.DockerStatus(),
	})
}

func (s *Server) ban(w http.ResponseWriter, r *http.Request) {
	var req agentapi.BanRequest
	if !s.decode(w, r, &req, maxRequestBytes) {
		return
	}
	p, err := banrules.ParsePrefix(req.Prefix)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	d, err := banrules.TimeoutFromSeconds(req.TimeoutSeconds)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.fw.Ban(r.Context(), p, d); err != nil {
		s.firewallError(w, r, "ban", p.String(), err)
		return
	}
	s.log.Info("firewall ban", "prefix", p.String(), "timeout_s", req.TimeoutSeconds, "backend", s.fw.Name(), "peer_uid", peerUID(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unban(w http.ResponseWriter, r *http.Request) {
	var req agentapi.UnbanRequest
	if !s.decode(w, r, &req, maxRequestBytes) {
		return
	}
	p, err := banrules.ParsePrefix(req.Prefix)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.fw.Unban(r.Context(), p); err != nil {
		s.firewallError(w, r, "unban", p.String(), err)
		return
	}
	s.log.Info("firewall unban", "prefix", p.String(), "backend", s.fw.Name(), "peer_uid", peerUID(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bans(w http.ResponseWriter, r *http.Request) {
	list, err := s.fw.List(r.Context())
	if err != nil {
		s.firewallError(w, r, "list", "", err)
		return
	}
	if list == nil {
		list = []agentapi.Ban{}
	}
	s.log.Debug("firewall list", "count", len(list), "peer_uid", peerUID(r))
	writeJSON(w, http.StatusOK, agentapi.BansResponse{Bans: list})
}

func (s *Server) instances(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inst.Status(r.Context()))
}

func (s *Server) ensure(w http.ResponseWriter, r *http.Request) {
	var req agentapi.EnsureRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	s.queued(w, r, "ensure", req.ID, s.inst.Ensure(r.Context(), req))
}

func (s *Server) power(w http.ResponseWriter, r *http.Request) {
	var req agentapi.PowerRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if req.ID <= 0 || !agentapi.ValidPowerAction(req.Action) {
		s.reject(w, r, http.StatusBadRequest, errors.New("id must be > 0 and action start, stop or restart"))
		return
	}
	s.queued(w, r, "power "+string(req.Action), req.ID, s.inst.Power(r.Context(), req.ID, req.Action))
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	var req agentapi.RemoveRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if req.ID <= 0 {
		s.reject(w, r, http.StatusBadRequest, errors.New("id must be > 0"))
		return
	}
	s.queued(w, r, "remove", req.ID, s.inst.Remove(r.Context(), req.ID))
}

// queued answers a write to the instance queue: 202, or the mapped error.
func (s *Server) queued(w http.ResponseWriter, r *http.Request, op string, id int64, err error) {
	switch {
	case err == nil:
		s.log.Info("instance operation accepted", "op", op, "id", id, "peer_uid", peerUID(r))
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, instancespec.ErrInvalid), errors.Is(err, banrules.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	case errors.Is(err, agentapi.ErrBusy):
		s.rejectCode(w, r, http.StatusTooManyRequests, agentapi.ErrorCodeBusy, err)
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable, err)
	default:
		s.log.Error("instance operation failed", "op", op, "id", id, "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "instance operation failed"})
	}
}

// decode strictly decodes exactly one JSON object of at most limit bytes.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		s.decodeError(w, r, err, limit)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after JSON body")
		}
		s.decodeError(w, r, err, limit)
		return false
	}
	return true
}

func (s *Server) decodeError(w http.ResponseWriter, r *http.Request, err error, limit int64) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		s.reject(w, r, http.StatusRequestEntityTooLarge, fmt.Errorf("request body exceeds %d bytes", limit))
		return
	}
	s.reject(w, r, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
}

func (s *Server) firewallError(w http.ResponseWriter, r *http.Request, op, prefix string, err error) {
	switch {
	case errors.Is(err, banrules.ErrConflict):
		s.reject(w, r, http.StatusConflict, err)
	case errors.Is(err, banrules.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	default:
		s.log.Error("firewall operation failed", "op", op, "prefix", prefix, "backend", s.fw.Name(), "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "firewall operation failed"})
	}
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.rejectCode(w, r, status, "", err)
}

func (s *Server) rejectCode(w http.ResponseWriter, r *http.Request, status int, code string, err error) {
	s.log.Warn("agent request rejected", "method", r.Method, "path", r.URL.Path, "status", status, "code", code,
		"err", err.Error(), "peer_uid", peerUID(r))
	writeJSON(w, status, agentapi.ErrorResponse{Error: err.Error(), Code: code})
}

// noInstances is the backend of a server without WithInstances.
type noInstances struct{}

func (noInstances) DockerStatus() string { return agentapi.DockerUnavailable }

func (noInstances) Status(context.Context) agentapi.InstancesResponse {
	return agentapi.InstancesResponse{
		Docker: agentapi.DockerUnavailable, Instances: []agentapi.InstanceStatus{},
		Router: agentapi.RouterStatus{State: agentapi.StateMissing},
	}
}

func (noInstances) Ensure(context.Context, agentapi.EnsureRequest) error {
	return agentapi.ErrDockerUnavailable
}

func (noInstances) Power(context.Context, int64, agentapi.PowerAction) error {
	return agentapi.ErrDockerUnavailable
}

func (noInstances) Remove(context.Context, int64) error { return agentapi.ErrDockerUnavailable }

// peerUID returns the UID stored by peercred.ConnContext, or -1 if unknown.
func peerUID(r *http.Request) int64 {
	if uid, ok := peercred.FromContext(r.Context()); ok {
		return int64(uid)
	}
	return -1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
