// Package plugins manages the plugins (paper, purpur) and mods (fabric, neoforge) of a Minecraft
// server (sub-project 3c): inventory, search in Modrinth and Hangar, install and update jobs,
// toggling, removing and an icon proxy. Jars are only written by the unprivileged files
// service; the panel downloads them from fixed CDN hosts and streams them through with their
// checksum.
//
// Phase A of the plan registers the routes as stubs; phase B2 implements them.
package plugins

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

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
// *catalog.Client from catalog.New; same method set as servers.CatalogSource).
type Catalog interface {
	Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error)
}

var _ Catalog = (*catalog.Client)(nil)

// Deps are the dependencies of the plugin handlers (contract V4).
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
}

func NewHandler(d Deps) *Handler {
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
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: d.Files, inst: d.Instances,
		catalog: cat, up: up, log: log,
	}
}

// Routes registers the plugin routes (paths relative to /api, contract V3), all with
// server.plugins (admins always).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerPlugins, "id")
	api.Get("/servers/{id}/plugins", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/search", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/versions", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/resolve", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/install", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/updates", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/update", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/toggle", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/delete", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/jobs", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/icon", rule, h.notImplemented)
}

// Run blocks until ctx is done; then it cancels running jobs and waits for them (phase A has
// no jobs).
func (h *Handler) Run(ctx context.Context) {
	<-ctx.Done()
}

// notImplemented answers every route until phase B2 replaces the handlers.
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "plugins not implemented yet")
}
