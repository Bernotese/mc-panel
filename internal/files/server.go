package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	readHeaderTimeout = 5 * time.Second
	requestTimeout    = 30 * time.Second
)

type route struct {
	method  string
	handler http.HandlerFunc
}

// Server serves the filesapi endpoints on the Unix socket.
type Server struct {
	svc     *Service
	version string
	log     *slog.Logger
	routes  map[string]route
	srv     *http.Server
}

func NewServer(svc *Service, version string, log *slog.Logger) *Server {
	s := &Server{svc: svc, version: version, log: log}
	s.routes = map[string]route{
		filesapi.PathHealth: {method: http.MethodGet, handler: s.health},
	}
	s.srv = &http.Server{
		Handler:           s,
		ConnContext:       peercred.ConnContext,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       requestTimeout,
		WriteTimeout:      requestTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// Serve accepts connections on l until Shutdown; it then returns nil.
func (s *Server) Serve(l net.Listener) error {
	if err := s.srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting and waits for running requests; when ctx ends first, open
// uploads and downloads are cut off.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		_ = s.srv.Close()
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.routes[r.URL.Path]
	if !ok {
		s.fail(w, r, "route", fmt.Errorf("%w: no such endpoint %s", filesapi.ErrNotFound, r.URL.Path))
		return
	}
	if r.Method != rt.method {
		w.Header().Set("Allow", rt.method)
		writeJSON(w, http.StatusMethodNotAllowed, filesapi.ErrorResponse{Error: "method " + r.Method + " not allowed"})
		return
	}
	if r.Method == http.MethodPost {
		// No code: every wire code is answered only with its own status (filesapi codes table),
		// and 415 belongs to not_text.
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, filesapi.ErrorResponse{Error: "content type must be application/json"})
			return
		}
	}
	rt.handler(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, filesapi.HealthResponse{Status: "ok", Version: s.version})
}

// fail answers err: sentinel errors with their code, everything else as a generic 500 whose
// details only go to the log. A caller that has already gone gets no answer.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	if r.Context().Err() != nil {
		s.log.Debug("files request ended by the caller", "op", op, "path", r.URL.Path)
		return
	}
	status, code := statusFor(err)
	if status == http.StatusInternalServerError {
		s.log.Error("file operation failed", "op", op, "err", err, "peer_uid", peerUID(r))
		writeJSON(w, status, filesapi.ErrorResponse{Error: "file operation failed"})
		return
	}
	s.log.Info("file request rejected", "op", op, "status", status, "code", code, "err", err.Error(), "peer_uid", peerUID(r))
	writeJSON(w, status, filesapi.ErrorResponse{Error: err.Error(), Code: code})
}

// statusFor maps an error to the HTTP status and wire code (filesapi.CodeOf; other errors
// are internal: 500 without a code).
func statusFor(err error) (int, string) {
	code, status := filesapi.CodeOf(err)
	return status, code
}

// parseID parses the id query parameter of the streaming and job endpoints.
func parseID(v string) (int64, error) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: id must be an integer > 0", filesapi.ErrInvalid)
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// peerUID returns the UID stored by peercred.ConnContext, or -1 if unknown.
func peerUID(r *http.Request) int64 {
	if uid, ok := peercred.FromContext(r.Context()); ok {
		return int64(uid)
	}
	return -1
}
