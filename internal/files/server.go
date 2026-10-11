package files

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
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	ioTimeout         = 60 * time.Second // stream deadline per read or write (spec §4.1)
	uploadDrainTime   = 5 * time.Second  // see failEarly
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
	io      clock.Clock // stream deadlines: always real time
	routes  map[string]route
	srv     *http.Server
}

func NewServer(svc *Service, version string, log *slog.Logger) *Server {
	s := &Server{svc: svc, version: version, log: log, io: clock.Real{}}
	s.routes = map[string]route{
		filesapi.PathHealth:   {method: http.MethodGet, handler: s.health},
		filesapi.PathList:     {method: http.MethodPost, handler: s.list},
		filesapi.PathRead:     {method: http.MethodPost, handler: s.read},
		filesapi.PathWrite:    {method: http.MethodPost, handler: s.write},
		filesapi.PathMkdir:    {method: http.MethodPost, handler: s.mkdir},
		filesapi.PathRename:   {method: http.MethodPost, handler: s.rename},
		filesapi.PathDelete:   {method: http.MethodPost, handler: s.delete},
		filesapi.PathUpload:   {method: http.MethodPut, handler: s.upload},
		filesapi.PathDownload: {method: http.MethodGet, handler: s.download},
		filesapi.PathExtract:  {method: http.MethodPost, handler: s.extract},
		filesapi.PathJobs:     {method: http.MethodGet, handler: s.jobs},
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

// decode strictly decodes exactly one JSON object of at most limit bytes; on failure it has
// answered already.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if _, terr := dec.Token(); !errors.Is(terr, io.EOF) {
			err = errors.New("unexpected data after the JSON body")
		}
	}
	if err == nil {
		return true
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		s.fail(w, r, "decode", fmt.Errorf("%w: request body exceeds %d bytes", filesapi.ErrTooLarge, limit))
	} else {
		s.fail(w, r, "decode", fmt.Errorf("%w: invalid JSON body: %w", filesapi.ErrInvalid, err))
	}
	return false
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

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	res, err := s.svc.List(r.Context(), req.ID, req.Path)
	if err != nil {
		s.fail(w, r, "list", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	res, err := s.svc.Read(r.Context(), req.ID, req.Path)
	if err != nil {
		s.fail(w, r, "read", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) write(w http.ResponseWriter, r *http.Request) {
	var req filesapi.WriteRequest
	if !s.decode(w, r, &req, filesapi.MaxJSONBytes) {
		return
	}
	res, err := s.svc.Write(r.Context(), req)
	if err != nil {
		s.fail(w, r, "write", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.Mkdir(r.Context(), req.ID, req.Path); err != nil {
		s.fail(w, r, "mkdir", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request) {
	var req filesapi.RenameRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.Rename(r.Context(), req); err != nil {
		s.fail(w, r, "rename", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.Delete(r.Context(), req.ID, req.Path); err != nil {
		s.fail(w, r, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upload: PUT ?id=&path=&overwrite=0|1, body = file. The server-wide deadlines do not apply;
// every read gets ioTimeout.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	// Full duplex: an error found before the body has been read completely (409, 413, 429, 507,
	// a full disk) is answered while the client is still sending (failEarly).
	_ = rc.EnableFullDuplex()
	body := &deadlineReader{r: r.Body, rc: rc, now: s.io.Now}
	res, err := s.receive(r, rc, body)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, res)
	case body.eof || r.ContentLength == 0:
		s.fail(w, r, "upload", err)
	default:
		s.failEarly(w, r, rc, err)
	}
}

func (s *Server) receive(r *http.Request, rc *http.ResponseController, body io.Reader) (filesapi.UploadResponse, error) {
	q := r.URL.Query()
	id, err := parseID(q.Get("id"))
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	ow := q.Get("overwrite")
	if ow != "" && ow != "0" && ow != "1" {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: overwrite must be 0 or 1", filesapi.ErrInvalid)
	}
	_ = rc.SetWriteDeadline(time.Time{})
	return s.svc.Upload(r.Context(), id, q.Get("path"), ow == "1", r.ContentLength, body)
}

// failEarly answers an upload error while the client may still be sending the body. The
// answer goes out at once with "Connection: close"; then the rest of the body is read and
// discarded until the client closes the connection - a Go client (the panel) does so as soon
// as it has read the answer - or uploadDrainTime has passed. Closing the connection right away
// instead could reset it while the client is still writing, and the client would see a broken
// connection instead of the answer.
func (s *Server) failEarly(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, err error) {
	w.Header().Set("Connection", "close")
	s.fail(w, r, "upload", err)
	if r.Context().Err() != nil {
		return
	}
	_ = rc.Flush()
	_ = rc.SetReadDeadline(s.io.Now().Add(uploadDrainTime))
	_, _ = io.Copy(io.Discard, r.Body)
}

// download: GET ?id=&path=. Errors after the first byte abort the response (the client must
// not take a cut file for a complete one).
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, err := parseID(q.Get("id"))
	if err != nil {
		s.fail(w, r, "download", err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	started := false
	start := func(isDir bool, size int64) {
		started = true
		h := w.Header()
		if isDir {
			h.Set(filesapi.HeaderKind, "dir")
			h.Set("Content-Type", "application/zip")
		} else {
			h.Set(filesapi.HeaderKind, "file")
			h.Set("Content-Type", "application/octet-stream")
			h.Set("Content-Length", strconv.FormatInt(size, 10))
		}
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		w.WriteHeader(http.StatusOK)
	}
	err = s.svc.Download(r.Context(), id, q.Get("path"), start, &deadlineWriter{w: w, rc: rc, now: s.io.Now})
	if err == nil {
		return
	}
	if !started {
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		s.fail(w, r, "download", err)
		return
	}
	if r.Context().Err() == nil {
		s.log.Warn("files: download aborted", "path", q.Get("path"), "id", id, "err", err)
	}
	panic(http.ErrAbortHandler)
}

// deadlineReader renews the connection's read deadline before every read, so an upload may
// take hours but never stalls for longer than ioTimeout. eof records that the body was read
// to its end.
type deadlineReader struct {
	r   io.Reader
	rc  *http.ResponseController
	now func() time.Time
	eof bool
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	_ = d.rc.SetReadDeadline(d.now().Add(ioTimeout))
	n, err := d.r.Read(p)
	if errors.Is(err, io.EOF) {
		d.eof = true
	}
	return n, err
}

// deadlineWriter renews the write deadline before every write (downloads).
type deadlineWriter struct {
	w   io.Writer
	rc  *http.ResponseController
	now func() time.Time
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(d.now().Add(ioTimeout))
	return d.w.Write(p)
}

func (s *Server) extract(w http.ResponseWriter, r *http.Request) {
	var req filesapi.ExtractRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	job, err := s.svc.Extract(r.Context(), req)
	if err != nil {
		s.fail(w, r, "extract", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.URL.Query().Get("id"))
	if err != nil {
		s.fail(w, r, "jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, filesapi.JobsResponse{Job: s.svc.Job(id)})
}
