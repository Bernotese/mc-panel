// Package console implements the live console of a Minecraft server (sub-project 3a): the log
// stream (SSE), RCON commands and player management. The agent does all Docker and file work.
package console

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// Deps are the dependencies of the console handlers.
type Deps struct {
	DB     *sql.DB // store.Open
	Clock  clock.Clock
	Audit  audit.Recorder
	Agent  agentapi.ConsoleClient // nil = no agent: every call fails with agentapi.ErrUnavailable
	Access Access                 // nil = deny: every recheck fails and open streams close (Phase A stub never streams)
	Log    *slog.Logger           // nil = slog.Default()
}

// Access re-checks the caller of an open stream (spec §6.2). Implemented in B2.
type Access interface {
	// Recheck returns nil while the session p.SessionID is valid (not expired, not revoked,
	// stage full), the user exists and still holds perm on serverID (admins always do).
	Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error
}

type Handler struct {
	deps Deps
}

func NewHandler(d Deps) *Handler {
	if d.Agent == nil {
		d.Agent = noAgent{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Handler{deps: d}
}

// Routes registers the console routes (paths relative to /api, rules per V4).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers/{id}/console/stream", rbac.Server(rbac.PermServerConsole, "id"), h.handleStream)
	api.Post("/servers/{id}/console", rbac.Server(rbac.PermServerConsole, "id"), h.handleCommand)
	api.Get("/servers/{id}/players", rbac.Server(rbac.PermServerView, "id"), h.handleOnlinePlayers)
	api.Get("/servers/{id}/players/lists", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerLists)
	api.Post("/servers/{id}/players/actions", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerAction)
}

// Shutdown closes all open streams (called from cmd/panel on shutdown). Phase A has none.
func (h *Handler) Shutdown() {}

// notImplemented answers every console route until phase B2 replaces the handlers.
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	h.deps.Log.DebugContext(r.Context(), "console route not implemented yet", "path", r.URL.Path)
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "console not implemented yet")
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request)        { h.notImplemented(w, r) }
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request)       { h.notImplemented(w, r) }
func (h *Handler) handleOnlinePlayers(w http.ResponseWriter, r *http.Request) { h.notImplemented(w, r) }
func (h *Handler) handlePlayerLists(w http.ResponseWriter, r *http.Request)   { h.notImplemented(w, r) }
func (h *Handler) handlePlayerAction(w http.ResponseWriter, r *http.Request)  { h.notImplemented(w, r) }

// noAgent stands in for a missing agent client: every call fails as if the agent were down.
type noAgent struct{}

func (noAgent) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}

func (noAgent) Rcon(context.Context, int64, string) (string, error) {
	return "", agentapi.ErrUnavailable
}

func (noAgent) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrUnavailable
}
