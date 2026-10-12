// Package backups implements the backups of a Minecraft server (sub-project 4): manual and
// scheduled restic snapshots that are consistent while the server runs (save-off, save-all
// flush, save-on over RCON), retention, pinning, a full restore with a safety snapshot and the
// zip download. restic runs only inside the unprivileged mcpanel-files service; this package
// orchestrates it (queue, jobs, schedule ticker) and never touches files itself.
//
// Phase A of the plan registers the routes as stubs; phase B2 implements them.
package backups

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the backup handlers (contract V4).
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

type Handler struct {
	q         *store.Queries
	clock     clock.Clock
	rec       audit.Recorder
	notifier  notify.Notifier
	files     filesapi.BackupClient
	inst      agentapi.InstancesClient
	console   agentapi.ConsoleClient
	reconcile func()
	log       *slog.Logger
}

func NewHandler(d Deps) *Handler {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	n := d.Notifier
	if n == nil {
		n = notify.Nop{}
	}
	rc := d.Reconcile
	if rc == nil {
		rc = func() {}
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, notifier: n, files: d.Files,
		inst: d.Instances, console: d.Console, reconcile: rc, log: log,
	}
}

// Routes registers the backup routes (paths relative to /api, contract V3), all with
// server.backups (admins always); restore and delete additionally need sudo.
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerBackups, "id")
	api.Get("/servers/{id}/backups", rule, h.notImplemented)
	api.Post("/servers/{id}/backups", rule, h.notImplemented)
	api.Get("/servers/{id}/backups/job", rule, h.notImplemented)
	api.Put("/servers/{id}/backups/schedule", rule, h.notImplemented)
	api.Post("/servers/{id}/backups/{snapshot}/pin", rule, h.notImplemented)
	api.Post("/servers/{id}/backups/{snapshot}/restore", rule.WithSudo(), h.notImplemented)
	api.Delete("/servers/{id}/backups/{snapshot}", rule.WithSudo(), h.notImplemented)
	api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.notImplemented)
}

// Run blocks until ctx is done (phase B2: recovery, ticker, queue worker).
func (h *Handler) Run(ctx context.Context) {
	<-ctx.Done()
}

// DataJobRunning is true while the panel holds a restore job for the server (servers.DataGate;
// phase A has no jobs).
func (h *Handler) DataJobRunning(context.Context, int64) bool { return false }

// PurgeBackups deletes the backups of a deleted server (servers.BackupPurger; phase A has
// nothing to delete).
func (h *Handler) PurgeBackups(context.Context, int64) error { return nil }

// notImplemented answers every route until phase B2 replaces the handlers.
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, "backups not implemented yet")
}
