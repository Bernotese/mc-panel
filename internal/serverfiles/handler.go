// Package serverfiles implements the file browser of a Minecraft server (sub-project 3b). Every
// file operation runs in the unprivileged mcpanel-files service; the panel checks the server,
// the permission and the path, streams uploads and downloads through and writes the audit log.
package serverfiles

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// ioTimeout aborts an upload or download after this long without progress (spec §5.1).
const ioTimeout = 60 * time.Second

const (
	// maxEditorCalls bounds the editor reads and saves running at the same time panel-wide:
	// each holds up to max_text_bytes of text (a save up to filesapi.MaxJSONBytes of JSON) in
	// memory.
	maxEditorCalls = 8
	// maxEditorCallsPerUser keeps one account from taking every editor slot.
	maxEditorCallsPerUser = 2
	// saveTimeout bounds reading the body of an editor save as a whole (on top of ioTimeout per
	// read): a save that trickles in must not hold an editor slot for long.
	saveTimeout = 2 * time.Minute
)

// Deps are the dependencies of the file handlers.
type Deps struct {
	DB    *sql.DB // from store.Open
	Clock clock.Clock
	Audit audit.Recorder
	// Files is the mcpanel-files service; nil means "no service" (every route answers 503
	// files_unavailable).
	Files filesapi.Client
	// Instances is the agent's instance API, asked for the actual state before extracting;
	// nil means "no agent" (extracting answers 503 agent_unavailable).
	Instances agentapi.InstancesClient
	Log       *slog.Logger // nil = slog.Default()
}

type Handler struct {
	q         *store.Queries
	clock     clock.Clock
	ioClock   clock.Clock // wall clock for I/O deadlines (always clock.Real)
	ioTimeout time.Duration
	// saveTimeout bounds reading an editor save (tests shorten it).
	saveTimeout time.Duration
	rec         audit.Recorder
	files       filesapi.Client
	inst        agentapi.InstancesClient
	log         *slog.Logger
	uploads     *userSlots
	downloads   *userSlots
	editor      chan struct{} // at most maxEditorCalls reads and saves
	editors     *userSlots    // at most maxEditorCallsPerUser of them per user
	zips        *userSlots    // at most maxZipsPerUser folder zips per user
	pending     *pendingExtracts
}

func NewHandler(d Deps) *Handler {
	fc := d.Files
	if fc == nil {
		fc = noFiles{}
	}
	inst := d.Instances
	if inst == nil {
		inst = noAgent{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, ioClock: clock.Real{}, ioTimeout: ioTimeout,
		rec: d.Audit, files: fc, inst: inst, log: log,
		uploads: newUserSlots(maxUploadsPerUser), downloads: newUserSlots(maxDownloadsPerUser),
		editor: make(chan struct{}, maxEditorCalls), editors: newUserSlots(maxEditorCallsPerUser),
		zips: newUserSlots(maxZipsPerUser), saveTimeout: saveTimeout, pending: newPendingExtracts(),
	}
}

// Routes registers the file routes (paths relative to /api, contract V3/V4).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerFiles, "id")
	api.Get("/servers/{id}/files", rule, h.handleList)
	api.Get("/servers/{id}/files/content", rule, h.handleRead)
	api.Put("/servers/{id}/files/content", rule, h.handleWrite)
	api.Post("/servers/{id}/files/upload", rule, h.handleUpload)
	api.Get("/servers/{id}/files/download", rule, h.handleDownload)
	api.Post("/servers/{id}/files/mkdir", rule, h.handleMkdir)
	api.Post("/servers/{id}/files/rename", rule, h.handleRename)
	api.Post("/servers/{id}/files/delete", rule, h.handleDelete)
	api.Post("/servers/{id}/files/extract", rule, h.handleExtract)
	api.Get("/servers/{id}/files/jobs", rule, h.handleJobs)
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func result(err error) audit.Result {
	if err != nil {
		return audit.ResultFailure
	}
	return audit.ResultSuccess
}

// record writes an audit entry; actor and IP come from the request context. The operation
// already happened, so the entry is written even if the client went away (WithoutCancel), and
// a failing audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: serverTarget(serverID), Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "server_id", serverID, "err", err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeServerNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeFileNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "file not found")
}

// loadServer returns the server of the {id} path parameter if it has an instance; it writes
// 404 itself for malformed ids, unknown servers and servers without instance (spec §5.1).
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeServerNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	_, err = h.q.ServersGetInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

// cleanPath validates a path (spec §3.4); "" is the data directory. A hidden path is answered
// like a missing file. It writes the error itself.
func cleanPath(w http.ResponseWriter, r *http.Request, p string) (string, bool) {
	c, err := filesapi.CleanPath(p)
	if err != nil {
		writeValidation(w, r, "invalid path: "+err.Error())
		return "", false
	}
	if filesapi.IsHidden(c) {
		writeFileNotFound(w, r)
		return "", false
	}
	return c, true
}

// cleanItemPath is cleanPath for operations on one file or directory: the data directory
// itself is not allowed (decision 4).
func cleanItemPath(w http.ResponseWriter, r *http.Request, p string) (string, bool) {
	c, ok := cleanPath(w, r, p)
	if ok && c == "" {
		writeValidation(w, r, "the path must name a file or directory inside the data directory")
		return "", false
	}
	return c, ok
}

// apiError is the answer to a failed service call.
type apiError struct {
	status int
	code   string
	msg    string
}

// mapFilesError maps a failed service call to the API error (spec §5.1); ok is false for an
// internal error (500).
func mapFilesError(err error) (e apiError, ok bool) {
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		return apiError{http.StatusNotFound, httpx.CodeNotFound, "file not found"}, true
	case errors.Is(err, filesapi.ErrExists):
		return apiError{http.StatusConflict, httpx.CodeFileExists, "the target already exists"}, true
	case errors.Is(err, filesapi.ErrModified):
		return apiError{http.StatusConflict, httpx.CodeFileModified, "the file was changed since it was opened"}, true
	case errors.Is(err, filesapi.ErrNotText):
		return apiError{http.StatusUnsupportedMediaType, httpx.CodeNotText, "the editor cannot open this file (too large or not text)"}, true
	case errors.Is(err, filesapi.ErrNotRegular):
		return apiError{http.StatusBadRequest, httpx.CodeNotRegular, "not a regular file"}, true
	case errors.Is(err, filesapi.ErrTooLarge):
		return apiError{http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, serviceMessage(err, "the file is too large")}, true
	case errors.Is(err, filesapi.ErrInsufficientStorage):
		return apiError{http.StatusInsufficientStorage, httpx.CodeInsufficientStorage, "not enough free disk space"}, true
	case errors.Is(err, filesapi.ErrBusy):
		return apiError{http.StatusTooManyRequests, httpx.CodeRateLimited, "the file service is busy, try again shortly"}, true
	case errors.Is(err, filesapi.ErrInvalid):
		return apiError{http.StatusBadRequest, httpx.CodeValidation, serviceMessage(err, err.Error())}, true
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return apiError{http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the file service is not reachable"}, true
	}
	return apiError{http.StatusInternalServerError, httpx.CodeInternal, "internal error"}, false
}

// ErrorStatus returns the HTTP status and error code a failed service call is answered with
// (the contract test checks them against api/openapi.yaml).
func ErrorStatus(err error) (status int, code string) {
	e, _ := mapFilesError(err)
	return e.status, e.code
}

// serviceMessage returns the message the service wrote (without the client's transport
// prefix; the service keeps host paths out of it), or fallback for other errors.
func serviceMessage(err error, fallback string) string {
	var se *filesapi.StatusError
	if errors.As(err, &se) && se.Message != "" {
		return se.Message
	}
	return fallback
}

// writeFilesError answers a failed service call (mapFilesError).
func (h *Handler) writeFilesError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		// The client went away: nobody reads an answer, and it is not a server error.
		h.log.DebugContext(r.Context(), "file operation ended with the request", "err", err)
		return
	}
	e, ok := mapFilesError(err)
	if !ok {
		httpx.Internal(w, r, err)
		return
	}
	if e.status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "5")
	}
	httpx.WriteError(w, r, e.status, e.code, e.msg)
}

// rejected reports a service answer that refused the request rather than failing it: a busy
// service is a rate limit, and rate limits are not audited (spec §5.3).
func rejected(err error) bool { return errors.Is(err, filesapi.ErrBusy) }

// recordResult audits the outcome of a service call unless the service rejected it.
func (h *Handler) recordResult(ctx context.Context, action string, serverID int64, err error, details map[string]any) {
	if err != nil && rejected(err) {
		return
	}
	h.record(ctx, action, serverID, result(err), details)
}

// extendWrite gives the answer a fresh write deadline after a call that may have outlasted the
// server's WriteTimeout (a large delete, the extract pre-check, a slow upload or save).
func (h *Handler) extendWrite(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	if err := ignoreUnsupported(rc.SetWriteDeadline(h.ioClock.Now().Add(h.ioTimeout))); err != nil {
		h.log.DebugContext(r.Context(), "set write deadline", "err", err)
	}
}

// editorSlot takes one of the user's editor slots and one of the panel-wide ones; on false it
// has answered 429.
func (h *Handler) editorSlot(w http.ResponseWriter, r *http.Request) (release func(), ok bool) {
	pr, _ := principal.From(r.Context()) // guaranteed by the rbac rule
	releaseUser, ok := h.editors.acquire(pr.UserID)
	if ok {
		select {
		case h.editor <- struct{}{}:
			return func() { <-h.editor; releaseUser() }, true
		default:
			releaseUser()
		}
	}
	w.Header().Set("Retry-After", "5")
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many files are being opened or saved, try again shortly")
	return nil, false
}

var errNoFiles = fmt.Errorf("%w: no file service configured", filesapi.ErrUnavailable)

// noFiles stands in for a missing service client: every call fails as if the service were down.
type noFiles struct{}

func (noFiles) Health(context.Context) error { return errNoFiles }
func (noFiles) List(context.Context, int64, string) (filesapi.ListResponse, error) {
	return filesapi.ListResponse{}, errNoFiles
}
func (noFiles) Read(context.Context, int64, string) (filesapi.ReadResponse, error) {
	return filesapi.ReadResponse{}, errNoFiles
}
func (noFiles) Write(context.Context, filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	return filesapi.WriteResponse{}, errNoFiles
}
func (noFiles) Upload(context.Context, int64, string, bool, int64, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNoFiles
}
func (noFiles) Download(context.Context, int64, string) (filesapi.Download, error) {
	return filesapi.Download{}, errNoFiles
}
func (noFiles) Mkdir(context.Context, int64, string) error           { return errNoFiles }
func (noFiles) Rename(context.Context, filesapi.RenameRequest) error { return errNoFiles }
func (noFiles) Delete(context.Context, int64, string) error          { return errNoFiles }
func (noFiles) Extract(context.Context, filesapi.ExtractRequest) (filesapi.Job, error) {
	return filesapi.Job{}, errNoFiles
}
func (noFiles) Job(context.Context, int64) (*filesapi.Job, error) { return nil, errNoFiles }
func (noFiles) UploadChecked(context.Context, int64, string, bool, int64, filesapi.Checksum, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNoFiles
}
func (noFiles) Jars(context.Context, int64, string) (filesapi.JarsResponse, error) {
	return filesapi.JarsResponse{}, errNoFiles
}

var errNoAgent = fmt.Errorf("%w: no agent configured", agentapi.ErrUnavailable)

// noAgent stands in for a missing agent client.
type noAgent struct{}

func (noAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	return agentapi.InstancesResponse{}, errNoAgent
}
func (noAgent) Ensure(context.Context, agentapi.EnsureRequest) error     { return errNoAgent }
func (noAgent) Power(context.Context, int64, agentapi.PowerAction) error { return errNoAgent }
func (noAgent) Remove(context.Context, int64) error                      { return errNoAgent }
