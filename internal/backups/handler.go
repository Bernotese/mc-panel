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
	"sync"
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
	// MaxDuration bounds the service job of one backup (panel.toml backups.max_duration;
	// 0 = defaultMaxDuration).
	MaxDuration time.Duration
}

// defaultMaxDuration is backups.max_duration when Deps leaves it 0 (SEC-25).
const defaultMaxDuration = 4 * time.Hour

// Timings (spec §5, Global Constraints). The waits use wall time; clock.Clock only drives
// logic (schedules, timestamps, the notification throttle). Tests shorten them.
const (
	tickEvery    = time.Minute
	pollEvery    = 2 * time.Second
	rconTimeout  = 60 * time.Second
	stopTimeout  = 120 * time.Second
	shutdownWait = 20 * time.Second
	// rconMargin is added to rconTimeout for the panel side of a console command (the agent
	// client itself waits the agent's timeout plus 5 s).
	rconMargin = 10 * time.Second
	// stallTimeout fails a service job whose byte count did not change for that long (logic
	// time, h.clock).
	stallTimeout = 10 * time.Minute
	// forgetTimeout bounds a forget job (it reports no byte progress; logic time, h.clock).
	forgetTimeout  = 2 * time.Hour
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
	rconT     agentapi.RconTimeoutClient // the console with per-command timeouts (*agent.Client); nil = Rcon with the agent's 10 s
	saveLocks keyedLocks                 // per server: save-off ... save-on of a job vs. the save-on retry of a tick
	reconcile func()
	log       *slog.Logger
	queue     *queue
	tickMu    sync.Mutex // serialises ticks (Run and tests)
	alerts    *alerts
	downloads *userSlots
	manual    *cooldowns    // manual backups per server (manualEvery)
	maxBackup time.Duration // limit of a backup's service job (logic time, h.clock)
	lastTZ    string        // panel time zone of the previous tick ("" before the first; guarded by tickMu)
	ticked    chan struct{} // closed after the first tick of Run (tests wait for it)

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
	h := &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, notifier: n, files: d.Files, inst: d.Instances,
		console: con, reconcile: rf, log: log, queue: newQueue(d.Clock), alerts: newAlerts(), downloads: newUserSlots(maxDownloadsPerUser),
		manual:    newCooldowns(),
		tickEvery: tickEvery, pollEvery: pollEvery, rconTimeout: rconTimeout, stopTimeout: stopTimeout,
		shutdownWait: shutdownWait, ioTimeout: ioTimeout, ioClock: clock.Real{},
	}
	h.maxBackup = d.MaxDuration
	if h.maxBackup <= 0 {
		h.maxBackup = defaultMaxDuration
	}
	h.ticked = make(chan struct{})
	h.rconT, _ = con.(agentapi.RconTimeoutClient)
	h.queue.gone = h.serverGone
	h.queue.log = log
	return h
}

// Routes registers the backup routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerBackups, "id")
	sudo := rule.WithSudo()
	api.Get("/servers/{id}/backups", rule, h.slow(h.handleOverview))
	api.Post("/servers/{id}/backups", rule, h.handleCreate)
	api.Get("/servers/{id}/backups/job", rule, h.handleJob)
	// SEC-05: a lowered retention or an unpin lets the next scheduled run prune snapshots, so
	// both need sudo like restore and delete.
	api.Put("/servers/{id}/backups/schedule", sudo, h.handleUpdateSchedule)
	api.Post("/servers/{id}/backups/{snapshot}/pin", sudo, h.slow(h.handlePin))
	api.Post("/servers/{id}/backups/{snapshot}/restore", sudo, h.slow(h.handleRestore))
	api.Delete("/servers/{id}/backups/{snapshot}", sudo, h.slow(h.handleDelete))
	api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.slow(h.handleDownload))
}

// Run blocks until ctx is done: recovery first, then the worker and the ticker. On ctx.Done it
// refuses new jobs at once (also while a tick still waits for the agent), cancels the running
// job (which cancels its service job and sends save-on) and waits up to shutdownWait for the
// worker and the ticker.
func (h *Handler) Run(ctx context.Context) {
	h.recover(ctx)
	workDone := make(chan struct{})
	go func() {
		defer close(workDone)
		h.queue.work(ctx)
	}()
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		t := time.NewTicker(h.tickEvery)
		defer t.Stop()
		first := true
		for {
			h.tick(ctx)
			if first {
				first = false
				close(h.ticked)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	<-ctx.Done()
	h.queue.close()
	wait := time.NewTimer(h.shutdownWait)
	defer wait.Stop()
	for _, done := range []chan struct{}{workDone, tickDone} {
		select {
		case <-done:
		case <-wait.C:
			h.log.Warn("backups: the running job or tick did not end within the shutdown wait")
			return
		}
	}
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

// slowWriteTimeout is the write deadline of the routes that wait for a synchronous restic call
// (overview, pin, restore and delete pre-checks): the client timeout plus a margin.
const slowWriteTimeout = filesapi.BackupCallTimeout + 30*time.Second

// slow extends the write deadline of a route beyond the server's WriteTimeout (pattern of the
// serverfiles downloads); recorders without deadline support are fine.
func (h *Handler) slow(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ignoreUnsupported(http.NewResponseController(w).SetWriteDeadline(h.ioClock.Now().Add(slowWriteTimeout))); err != nil {
			h.log.WarnContext(r.Context(), "backups: extend write deadline", "err", err)
		}
		next(w, r)
	}
}

func ignoreUnsupported(err error) error {
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// keyedLocks is a mutex per server id (entries are kept: one per server ever backed up).
type keyedLocks struct {
	mu sync.Mutex
	m  map[int64]*sync.Mutex
}

func (k *keyedLocks) get(id int64) *sync.Mutex {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[int64]*sync.Mutex{}
	}
	l := k.m[id]
	if l == nil {
		l = &sync.Mutex{}
		k.m[id] = l
	}
	return l
}
