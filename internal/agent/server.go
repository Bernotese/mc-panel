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

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/banrules"
	"mcpanel/internal/agent/peercred"
)

const maxRequestBytes = 4 << 10

type route struct {
	method  string
	handler http.HandlerFunc
}

// Server serves the agentapi endpoints. It implements http.Handler; Serve
// adds the peer UID to each request context and applies conservative timeouts.
type Server struct {
	fw      agentapi.Firewall
	version string
	log     *slog.Logger
	routes  map[string]route
	srv     *http.Server
}

func NewServer(fw agentapi.Firewall, version string, log *slog.Logger) *Server {
	s := &Server{fw: fw, version: version, log: log}
	s.routes = map[string]route{
		agentapi.PathHealth: {method: http.MethodGet, handler: s.health},
		agentapi.PathBan:    {method: http.MethodPost, handler: s.ban},
		agentapi.PathUnban:  {method: http.MethodPost, handler: s.unban},
		agentapi.PathBans:   {method: http.MethodGet, handler: s.bans},
	}
	s.srv = &http.Server{
		Handler:           s,
		ConnContext:       peercred.ConnContext,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
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
	writeJSON(w, http.StatusOK, agentapi.HealthResponse{Status: "ok", Version: s.version, Firewall: s.fw.Name()})
}

func (s *Server) ban(w http.ResponseWriter, r *http.Request) {
	var req agentapi.BanRequest
	if !s.decode(w, r, &req) {
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
	if !s.decode(w, r, &req) {
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

// decode strictly decodes exactly one JSON object of at most maxRequestBytes.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		s.decodeError(w, r, err)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after JSON body")
		}
		s.decodeError(w, r, err)
		return false
	}
	return true
}

func (s *Server) decodeError(w http.ResponseWriter, r *http.Request, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		s.reject(w, r, http.StatusRequestEntityTooLarge, fmt.Errorf("request body exceeds %d bytes", maxRequestBytes))
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
	s.log.Warn("agent request rejected", "method", r.Method, "path", r.URL.Path, "status", status, "err", err.Error(), "peer_uid", peerUID(r))
	writeJSON(w, status, agentapi.ErrorResponse{Error: err.Error()})
}

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
