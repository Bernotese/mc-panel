// Package servers implements the server API (list, create, details, delete, power control)
// and, from sub-project 2 on, the Minecraft instances behind it.
package servers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the server handlers.
type Deps struct {
	DB    *sql.DB // from store.Open (BEGIN IMMEDIATE transactions)
	Clock clock.Clock
	Audit audit.Recorder
	Box   *secretbox.Box // seals the CurseForge API key at rest
	// Instances is the agent's instance API; nil means "no agent" (every call fails with
	// agentapi.ErrUnavailable).
	Instances agentapi.InstancesClient
}

type Handler struct {
	db     *sql.DB
	q      *store.Queries
	clock  clock.Clock
	rec    audit.Recorder
	box    *secretbox.Box
	inst   agentapi.InstancesClient
	power  *powerLimiter
	kick   chan struct{} // Trigger -> reconcile loop (buffer 1, never blocks)
	status statusCache   // last actual state reported by the agent
}

func NewHandler(d Deps) *Handler {
	inst := d.Instances
	if inst == nil {
		inst = noAgent{}
	}
	return &Handler{
		db: d.DB, q: store.New(d.DB), clock: d.Clock, rec: d.Audit, box: d.Box, inst: inst,
		power: newPowerLimiter(), kick: make(chan struct{}, 1),
	}
}

// Trigger asks the reconcile loop for an immediate pass. It never blocks; several triggers
// before the next pass collapse into one.
func (h *Handler) Trigger() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// Routes registers all server routes (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers", rbac.Authenticated(), h.handleListServers)
	api.Post("/servers", rbac.Admin().WithSudo(), h.handleCreateServer)
	api.Get("/servers/settings", rbac.Admin(), h.handleGetSettings)
	api.Put("/servers/settings", rbac.Admin().WithSudo(), h.handleUpdateSettings)
	api.Get("/servers/{id}", rbac.Server(rbac.PermServerView, "id"), h.handleGetServer)
	api.Patch("/servers/{id}", rbac.Admin().WithSudo(), h.handleUpdateServer)
	api.Delete("/servers/{id}", rbac.Admin().WithSudo(), h.handleDeleteServer)
	api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), h.handleServerPower)
}

// pathID parses the {id} URL parameter; false for non-numeric or non-positive values.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry; actor and IP come from the request context. It must only be
// called after any write transaction of the handler has finished (SQLRecorder opens its own).
// The action already happened, so a failing audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		slog.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeConflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}

// inTx runs fn in a write transaction (BEGIN IMMEDIATE, see store.Open). Never call h.record
// inside fn: the SQL recorder opens its own transaction.
func (h *Handler) inTx(ctx context.Context, fn func(q *store.Queries) error) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(h.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// noAgent stands in for a missing agent client: every call fails as if the agent were down.
type noAgent struct{}

func (noAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	return agentapi.InstancesResponse{}, agentapi.ErrUnavailable
}
func (noAgent) Ensure(context.Context, agentapi.EnsureRequest) error { return agentapi.ErrUnavailable }
func (noAgent) Power(context.Context, int64, agentapi.PowerAction) error {
	return agentapi.ErrUnavailable
}
func (noAgent) Remove(context.Context, int64) error { return agentapi.ErrUnavailable }
