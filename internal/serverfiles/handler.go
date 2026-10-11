// Package serverfiles implements the file browser API of a Minecraft server (sub-project 3b).
// Every file operation runs in the unprivileged files service (mcpanel-files); the panel checks
// server, permission, CSRF and audit and streams uploads/downloads through without buffering.
package serverfiles

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// Deps are the dependencies of the file handlers.
type Deps struct {
	DB    *sql.DB // from store.Open
	Clock clock.Clock
	Audit audit.Recorder
	// Files is the files service; nil = no service: every route answers 503 files_unavailable.
	Files filesapi.Client
	// Instances is the agent's instance API (actual state for extract); nil = no agent.
	Instances agentapi.InstancesClient
	Log       *slog.Logger // nil = slog.Default()
}

type Handler struct {
	db    *sql.DB
	clock clock.Clock
	rec   audit.Recorder
	files filesapi.Client
	inst  agentapi.InstancesClient
	log   *slog.Logger
}

func NewHandler(d Deps) *Handler {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{db: d.DB, clock: d.Clock, rec: d.Audit, files: d.Files, inst: d.Instances, log: log}
}

// Routes registers the file routes (paths relative to /api, rule from contract V4).
func (h *Handler) Routes(api *rbac.Router) {
	files := rbac.Server(rbac.PermServerFiles, "id")
	api.Get("/servers/{id}/files", files, h.ServeStub)
	api.Get("/servers/{id}/files/content", files, h.ServeStub)
	api.Put("/servers/{id}/files/content", files, h.ServeStub)
	api.Post("/servers/{id}/files/upload", files, h.ServeStub)
	api.Get("/servers/{id}/files/download", files, h.ServeStub)
	api.Post("/servers/{id}/files/mkdir", files, h.ServeStub)
	api.Post("/servers/{id}/files/rename", files, h.ServeStub)
	api.Post("/servers/{id}/files/delete", files, h.ServeStub)
	api.Post("/servers/{id}/files/extract", files, h.ServeStub)
	api.Get("/servers/{id}/files/jobs", files, h.ServeStub)
}

// ServeStub answers every route until Phase B2 replaces the handlers.
func (h *Handler) ServeStub(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "files not implemented yet")
}

// ExtractRunning reports whether an extract job runs for the server (servers.ExtractGate).
// Stub: never.
func (h *Handler) ExtractRunning(_ context.Context, _ int64) bool { return false }

var uploadPath = regexp.MustCompile(`^/api/servers/[0-9]+/files/upload$`)

// UnlimitedBody selects the requests exempt from the global 1 MiB body limit: only the upload
// stream (its own limit is enforced by the files service, spec 3b §5.1).
func UnlimitedBody(r *http.Request) bool {
	return r.Method == http.MethodPost && uploadPath.MatchString(r.URL.Path)
}
