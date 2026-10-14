// Package plugins implements plugins and mods of a Minecraft server (sub-project 3c): search
// in Modrinth and Hangar, the inventory of the jars on disk, resolving required dependencies,
// install and update jobs, switching jars on and off, deleting them and the icon proxy. Files
// are only ever written by the unprivileged mcpanel-files service.
package plugins

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
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Catalog resolves LATEST to the newest version of an instance type (implemented by
// *catalog.Client; same method set as servers.CatalogSource).
type Catalog interface {
	Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error)
}

type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Files     filesapi.Client          // nil = no service: inventory/jobs/toggle/delete answer 503 files_unavailable
	Instances agentapi.InstancesClient // actual state for restart_needed; nil = treat as not running
	Catalog   Catalog                  // nil = catalog.New with the default upstreams
	Upstream  *upstream.Client         // nil = upstream.New(upstream.Options{Clock, Version})
	Version   string                   // panel version for the User-Agent; "" = "dev"
	Log       *slog.Logger             // nil = slog.Default()
}

type Handler struct {
	q       *store.Queries
	clock   clock.Clock
	rec     audit.Recorder
	files   filesapi.Client
	inst    agentapi.InstancesClient
	catalog Catalog
	up      *upstream.Client
	log     *slog.Logger
	restart *restartTracker
	limit   *userLimiter
	jobs    *jobRegistry
	updates *upstream.Cache[updatesResult]
	icons   *iconCache
	// busyBackoff is the first wait before a job step retries an upload the service refused
	// as busy (doubled per retry; tests shorten it).
	busyBackoff time.Duration
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
	cat := d.Catalog
	if cat == nil {
		cat = catalog.New(catalog.Options{Clock: d.Clock, Version: d.Version})
	}
	up := d.Upstream
	if up == nil {
		up = upstream.New(upstream.Options{Clock: d.Clock, Version: d.Version})
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: fc, inst: inst, catalog: cat, up: up, log: log,
		restart: newRestartTracker(), limit: newUserLimiter(), jobs: newJobRegistry(d.Clock, maxJobs),
		updates: upstream.NewCache[updatesResult](d.Clock, updatesTTL, upstream.MaxCacheEntries),
		icons:   newIconCache(d.Clock, up), busyBackoff: busyBackoff,
	}
}

// Routes registers the plugin routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerPlugins, "id")
	api.Get("/servers/{id}/plugins", rule, h.handleList)
	api.Get("/servers/{id}/plugins/search", rule, h.handleSearch)
	api.Get("/servers/{id}/plugins/versions", rule, h.handleVersions)
	api.Get("/servers/{id}/plugins/resolve", rule, h.handleResolve)
	api.Post("/servers/{id}/plugins/install", rule, h.handleInstall)
	api.Get("/servers/{id}/plugins/updates", rule, h.handleUpdates)
	api.Post("/servers/{id}/plugins/update", rule, h.handleUpdate)
	api.Post("/servers/{id}/plugins/toggle", rule, h.handleToggle)
	api.Post("/servers/{id}/plugins/delete", rule, h.handleDelete)
	api.Get("/servers/{id}/plugins/jobs", rule, h.handleJobs)
	api.Get("/servers/{id}/plugins/icon", rule, h.handleIcon)
	api.Post("/servers/{id}/plugins/mrpack/preview", rule, h.handleMrpackPreview)
	api.Post("/servers/{id}/plugins/mrpack/import", rule, h.handleMrpackImport)
}

// Run blocks until ctx is done; then it cancels running jobs and waits for them.
func (h *Handler) Run(ctx context.Context) {
	<-ctx.Done()
	h.jobs.shutdown()
}

// server is a loaded server with an instance.
type server struct {
	ID      int64
	Name    string
	Type    instancespec.Type
	Version string // the instance's version field, may be "LATEST"
	Loader  string // the instance's fixed loader version; "" = newest
	Kind    kindInfo
}

// loadServer returns the server of the {id} path parameter if it has an instance; it writes
// 404 for malformed ids, unknown servers and servers without instance, and with requireKind
// 409 plugins_unsupported for types without plugins or mods (spec §6.6).
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request, requireKind bool) (server, bool) {
	ctx := r.Context()
	h.extendWriteDeadline(w, r)
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
	inst, err := h.q.ServersGetInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return server{}, false
	}
	t := instancespec.Type(inst.Type)
	k, ok := kindOf(t)
	if requireKind && !ok { // a server without plugins keeps the zero kindInfo (icon route only)
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodePluginsUnsupported, "this server type has no plugins or mods")
		return server{}, false
	}
	return server{ID: s.ID, Name: s.Name, Type: t, Version: inst.Version, Loader: inst.LoaderVersion.String, Kind: k}, true
}

// slowWriteTimeout is the write deadline of every plugin route: a jars listing alone may take
// filesapi.JarsTimeout (a cold hash cache in the service), longer than the server's
// WriteTimeout, and resolving or updates add upstream calls on top.
const slowWriteTimeout = filesapi.JarsTimeout + 3*time.Minute

// extendWriteDeadline replaces the server's WriteTimeout for this request (pattern of
// console/serverfiles), so a slow listing still ends with a JSON answer. The deadline is wall
// time (clock.Real), not the logic clock.
func (h *Handler) extendWriteDeadline(w http.ResponseWriter, r *http.Request) {
	err := http.NewResponseController(w).SetWriteDeadline(clock.Real{}.Now().Add(slowWriteTimeout))
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.log.DebugContext(r.Context(), "plugins: set write deadline", "err", err)
	}
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

// record writes an audit entry; actor and IP come from ctx (a job's context keeps those of
// the request that started it). The change already happened, so the entry is written even if
// the request is gone, and a failing audit write is logged.
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: serverTarget(serverID), Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "server_id", serverID, "err", err)
	}
}

var (
	errCatalog          = errors.New("the version catalog is unavailable, LATEST cannot be resolved")
	errIncompatible     = errors.New("no compatible version")
	errNotDownloadable  = errors.New("only available externally")
	errAlreadyInstalled = errors.New("the project is already installed")
	errFileExists       = errors.New("a file with this name already exists")
	errJobRunning       = errors.New("a plugin job is running for this server")
	errTooManyJobs      = errors.New("too many plugin jobs are running, try again shortly")
	errShuttingDown     = errors.New("the panel is shutting down")
	errNoChecksum       = errors.New("the source announces no checksum")

	errMrpackIncompatible = errors.New("the modpack is for another mod loader")
	errMrpackMismatch     = errors.New("the modpack is for another Minecraft or loader version")
	errNothingToImport    = errors.New("the modpack has nothing to install or update")
)

// classify maps an error to its API answer; ok is false for an internal error.
func classify(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, errCatalog):
		return http.StatusBadGateway, httpx.CodeCatalogUnavailable, errCatalog.Error(), true
	case errors.Is(err, errIncompatible):
		return http.StatusConflict, httpx.CodePluginIncompatible, err.Error(), true
	case errors.Is(err, errNotDownloadable):
		return http.StatusConflict, httpx.CodePluginNotDownloadable, err.Error(), true
	case errors.Is(err, errAlreadyInstalled):
		return http.StatusConflict, httpx.CodeConflict, errAlreadyInstalled.Error(), true
	case errors.Is(err, errFileExists):
		return http.StatusConflict, httpx.CodeFileExists, err.Error(), true
	case errors.Is(err, errInvalidIndex):
		return http.StatusBadRequest, httpx.CodeValidation, err.Error(), true
	case errors.Is(err, errMrpackIncompatible):
		return http.StatusConflict, httpx.CodeMrpackIncompatible, err.Error(), true
	case errors.Is(err, errMrpackMismatch):
		return http.StatusConflict, httpx.CodeMrpackVersionMismatch, err.Error(), true
	case errors.Is(err, errNothingToImport):
		return http.StatusConflict, httpx.CodeConflict, err.Error(), true
	case errors.Is(err, errJobRunning):
		return http.StatusConflict, httpx.CodeConflict, errJobRunning.Error(), true
	case errors.Is(err, errTooManyJobs):
		return http.StatusTooManyRequests, httpx.CodeRateLimited, errTooManyJobs.Error(), true
	case errors.Is(err, errShuttingDown):
		return http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, errShuttingDown.Error(), true
	case errors.Is(err, upstream.ErrNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, "project or version not found", true
	case errors.Is(err, upstream.ErrUnavailable), errors.Is(err, upstream.ErrDownload):
		return http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "Modrinth or Hangar is not reachable or answered unusably", true
	case errors.Is(err, filesapi.ErrNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, "file not found", true
	case errors.Is(err, filesapi.ErrExists):
		return http.StatusConflict, httpx.CodeFileExists, errFileExists.Error(), true
	case errors.Is(err, filesapi.ErrInvalid), errors.Is(err, filesapi.ErrNotRegular):
		return http.StatusBadRequest, httpx.CodeValidation, "the file service refused the request", true
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, filesapi.ErrBusy), errors.Is(err, filesapi.ErrTooLarge),
		errors.Is(err, filesapi.ErrInsufficientStorage), errors.Is(err, filesapi.ErrHashMismatch), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the file service is not reachable", true
	}
	return http.StatusInternalServerError, httpx.CodeInternal, "internal server error", false
}

// ErrorStatus returns the HTTP status and error code err is answered with (the contract test in
// cmd/panel checks them against api/openapi.yaml).
func ErrorStatus(err error) (status int, code string) {
	status, code, _, _ = classify(err)
	return status, code
}

// writeError answers err (classify); a client that went away gets nothing.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		h.log.DebugContext(r.Context(), "plugins: request ended", "err", err)
		return
	}
	status, code, msg, ok := classify(err)
	if !ok {
		httpx.Internal(w, r, err)
		return
	}
	if status >= 500 {
		h.log.WarnContext(r.Context(), "plugins: request failed", "code", code, "err", err)
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "5")
	}
	httpx.WriteError(w, r, status, code, msg)
}

var errNoFiles = fmt.Errorf("%w: no file service configured", filesapi.ErrUnavailable)

// noFiles stands in for a missing service client.
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
func (noFiles) UploadChecked(context.Context, int64, string, bool, int64, filesapi.Checksum, io.Reader) (filesapi.UploadResponse, error) {
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
func (noFiles) Jars(context.Context, int64, string) (filesapi.JarsResponse, error) {
	return filesapi.JarsResponse{}, errNoFiles
}

// noAgent stands in for a missing agent client: no server counts as running.
type noAgent struct{}

func (noAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	return agentapi.InstancesResponse{}, agentapi.ErrUnavailable
}
func (noAgent) Ensure(context.Context, agentapi.EnsureRequest) error { return agentapi.ErrUnavailable }
func (noAgent) Power(context.Context, int64, agentapi.PowerAction) error {
	return agentapi.ErrUnavailable
}
func (noAgent) Remove(context.Context, int64) error { return agentapi.ErrUnavailable }
