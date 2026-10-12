// Package backups implements backups of Minecraft servers (sub-project 4): manual and
// scheduled snapshots with retention, pinning, restore, deletion and zip downloads. Data is
// only read and written by the unprivileged mcpanel-files service (restic runs there); the
// panel orders the steps, keeps the schedules and sends RCON commands through the agent.
package backups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Notifier  notify.Notifier          // nil = notify.Nop{}
	Files     filesapi.BackupClient    // nil = no service: overview available=false, actions 503 files_unavailable
	Instances agentapi.InstancesClient // actual state + power stop; nil = treat as stopped, restore fails agent_unavailable
	Console   agentapi.ConsoleClient   // RCON; nil = running servers fail rcon_unavailable
	Reconcile func()                   // asks servers for an immediate reconcile pass; nil = no-op
	Log       *slog.Logger             // nil = slog.Default()
}

// Timings (spec §5, Global Constraints). The waits use wall time; clock.Clock only drives
// logic (schedules, timestamps, the notification throttle). Tests shorten them.
const (
	tickEvery      = time.Minute
	pollEvery      = 2 * time.Second
	rconTimeout    = 60 * time.Second
	stopTimeout    = 120 * time.Second
	shutdownWait   = 20 * time.Second
	cleanupTimeout = 10 * time.Second
	ioTimeout      = 60 * time.Second
	maxPollMisses  = 5
)

type Handler struct {
	q         *store.Queries
	clock     clock.Clock
	rec       audit.Recorder
	notifier  notify.Notifier
	files     filesapi.BackupClient    // nil = no service
	inst      agentapi.InstancesClient // nil = no agent
	console   agentapi.ConsoleClient
	reconcile func()
	log       *slog.Logger

	tickEvery, pollEvery, rconTimeout, stopTimeout, shutdownWait, ioTimeout time.Duration
	ioClock                                                                 clock.Clock // wall clock for I/O deadlines (always clock.Real)
}

func NewHandler(d Deps) *Handler {
	n := d.Notifier
	if n == nil {
		n = notify.Nop{}
	}
	con := d.Console
	if con == nil {
		con = noConsole{}
	}
	rf := d.Reconcile
	if rf == nil {
		rf = func() {}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, notifier: n, files: d.Files, inst: d.Instances,
		console: con, reconcile: rf, log: log,
		tickEvery: tickEvery, pollEvery: pollEvery, rconTimeout: rconTimeout, stopTimeout: stopTimeout,
		shutdownWait: shutdownWait, ioTimeout: ioTimeout, ioClock: clock.Real{},
	}
}

// Routes registers the backup routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerBackups, "id")
	sudo := rule.WithSudo()
	api.Get("/servers/{id}/backups", rule, h.notImplemented)
	api.Post("/servers/{id}/backups", rule, h.notImplemented)
	api.Get("/servers/{id}/backups/job", rule, h.notImplemented)
	api.Put("/servers/{id}/backups/schedule", rule, h.handleUpdateSchedule)
	api.Post("/servers/{id}/backups/{snapshot}/pin", rule, h.notImplemented)
	api.Post("/servers/{id}/backups/{snapshot}/restore", sudo, h.notImplemented)
	api.Delete("/servers/{id}/backups/{snapshot}", sudo, h.notImplemented)
	api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.notImplemented)
}

// Run blocks until ctx is done (task 10 adds recovery, the worker and the ticker).
func (h *Handler) Run(ctx context.Context) { <-ctx.Done() }

// DataJobRunning is true while the panel holds a restore job for the server (task 12).
func (h *Handler) DataJobRunning(context.Context, int64) bool { return false }

// PurgeBackups deletes the server's repository (task 12 drops its jobs first).
func (h *Handler) PurgeBackups(ctx context.Context, serverID int64) error {
	if h.files == nil {
		return nil
	}
	return h.files.PurgeBackups(ctx, serverID)
}

// server is a loaded server; HasInstance says whether it has a Minecraft instance.
type server struct {
	ID          int64
	Name        string
	HasInstance bool
}

// loadServer returns the server of the {id} path parameter; it writes 404 for malformed ids
// and unknown servers.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (server, bool) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeServerNotFound(w, r)
		return server{}, false
	}
	s, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return server{}, false
	}
	_, err = h.q.ServersGetInstance(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return server{}, false
	}
	return server{ID: s.ID, Name: s.Name, HasInstance: err == nil}, true
}

// snapshotParam returns the {snapshot} path parameter; it writes 400 unless it is 64 hex chars.
func snapshotParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := chi.URLParam(r, "snapshot")
	if !filesapi.ValidSnapshotID(s) {
		writeValidation(w, r, "snapshot must be 64 lowercase hex characters")
		return "", false
	}
	return s, true
}

func writeServerNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func result(err error) audit.Result {
	if err != nil {
		return audit.ResultFailure
	}
	return audit.ResultSuccess
}

// record writes an audit entry; actor and IP come from ctx (a job keeps those of the request
// that started it). A failing audit write is logged.
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: serverTarget(serverID), Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "server_id", serverID, "err", err)
	}
}

var (
	errBackupRunning    = errors.New("a backup job is already queued or running for this server")
	errShuttingDown     = errors.New("the panel is shutting down")
	errNoInstance       = errors.New("the server has no instance yet")
	errSnapshotNotFound = errors.New("snapshot not found")
	errNotAvailable     = errors.New("backups are not available on this host (restic is missing)")
	errTooManyDownloads = errors.New("at most 2 backup downloads at a time, try again when one is done")
	errNoFiles          = fmt.Errorf("%w: no file service configured", filesapi.ErrUnavailable)
	// errFilesBusy: the service refused a synchronous call (pin) with 429 busy while no panel
	// job runs for the server (a download or an extract in the files tab).
	errFilesBusy = errors.New("the file service is busy with this server (download or extract); try again later")
)

// classify maps an error to its API answer; ok is false for an internal error.
func classify(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, errBackupRunning):
		return http.StatusConflict, httpx.CodeBackupRunning, errBackupRunning.Error(), true
	case errors.Is(err, filesapi.ErrBusy):
		// same code as a panel job (V3 knows no other 409 for pin), but a message that says why
		return http.StatusConflict, httpx.CodeBackupRunning, errFilesBusy.Error(), true
	case errors.Is(err, errNoInstance):
		return http.StatusConflict, httpx.CodeConflict, errNoInstance.Error(), true
	case errors.Is(err, errSnapshotNotFound), errors.Is(err, filesapi.ErrNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, errSnapshotNotFound.Error(), true
	case errors.Is(err, errNotAvailable), errors.Is(err, filesapi.ErrBackupsUnavailable):
		return http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, errNotAvailable.Error(), true
	case errors.Is(err, errShuttingDown):
		return http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, errShuttingDown.Error(), true
	case errors.Is(err, filesapi.ErrInvalid):
		return http.StatusBadRequest, httpx.CodeValidation, "the file service refused the request: " + err.Error(), true
	case errors.Is(err, errTooManyDownloads):
		return http.StatusTooManyRequests, httpx.CodeRateLimited, errTooManyDownloads.Error(), true
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the file service is not reachable", true
	}
	return http.StatusInternalServerError, httpx.CodeInternal, "internal server error", false
}

// rejected reports whether err refused a request (not audited) rather than a failed action.
func rejected(err error) bool {
	return errors.Is(err, errBackupRunning) || errors.Is(err, errNoInstance) || errors.Is(err, errSnapshotNotFound) ||
		errors.Is(err, errTooManyDownloads) || errors.Is(err, filesapi.ErrBusy) || errors.Is(err, filesapi.ErrNotFound) ||
		errors.Is(err, filesapi.ErrInvalid)
}

// writeError answers err (classify); a client that went away gets nothing.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		h.log.DebugContext(r.Context(), "backups: request ended", "err", err)
		return
	}
	status, code, msg, ok := classify(err)
	if !ok {
		httpx.Internal(w, r, err)
		return
	}
	if status >= 500 {
		h.log.WarnContext(r.Context(), "backups: request failed", "code", code, "err", err)
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "5")
	}
	httpx.WriteError(w, r, status, code, msg)
}

// noConsole stands in for a missing agent console client.
type noConsole struct{}

func (noConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}
func (noConsole) Rcon(context.Context, int64, string) (string, error) {
	return "", agentapi.ErrUnavailable
}
func (noConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrUnavailable
}
