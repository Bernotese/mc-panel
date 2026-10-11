// Package console implements the live console of a Minecraft server (sub-project 3a): the log
// stream (SSE), console commands and player management. Everything goes through the agent; the
// panel never touches Docker or a server's data directory.
package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the console handlers.
type Deps struct {
	DB    *sql.DB // from store.Open
	Clock clock.Clock
	Audit audit.Recorder
	// Agent is the agent's console API; nil means "no agent" (every call fails with
	// agentapi.ErrUnavailable).
	Agent agentapi.ConsoleClient
	// Access re-checks open streams every 30 s; nil = deny all (open streams end at the next
	// 30 s recheck). cmd/panel always sets it (NewAccess).
	Access Access
	Log    *slog.Logger // nil = slog.Default()
}

// Access re-checks the caller of an open stream (spec §6.2).
type Access interface {
	// Recheck returns nil while the session p.SessionID is valid (not expired, not revoked,
	// stage full), the user exists and still holds perm on serverID (admins always do).
	Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error
}

// ErrAccessRevoked: the caller of a stream lost the permission (or Access is not configured).
var ErrAccessRevoked = errors.New("console: access revoked")

type Handler struct {
	q      *store.Queries
	clock  clock.Clock
	rec    audit.Recorder
	agent  agentapi.ConsoleClient
	access Access
	log    *slog.Logger
	limit  *userLimiter
	online *onlineCache
}

func NewHandler(d Deps) *Handler {
	ag := d.Agent
	if ag == nil {
		ag = noAgent{}
	}
	acc := d.Access
	if acc == nil {
		acc = denyAll{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, agent: ag, access: acc, log: log,
		limit: newUserLimiter(), online: newOnlineCache(),
	}
}

// Routes registers the console routes (paths relative to /api, rules from contract V4).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers/{id}/console/stream", rbac.Server(rbac.PermServerConsole, "id"), h.handleStream)
	api.Post("/servers/{id}/console", rbac.Server(rbac.PermServerConsole, "id"), h.handleCommand)
	api.Get("/servers/{id}/players", rbac.Server(rbac.PermServerView, "id"), h.handleOnlinePlayers)
	api.Get("/servers/{id}/players/lists", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerLists)
	api.Post("/servers/{id}/players/actions", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerAction)
}

// Shutdown closes all open streams (there are none before the stream handler exists).
func (h *Handler) Shutdown() {}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry; actor and IP come from the request context. The action already
// happened, so the entry is written even if the client went away (WithoutCancel), and a failing
// audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

// loadServer returns the server of the {id} path parameter if it has an instance; it writes
// 404 itself for malformed ids, unknown servers and servers without instance (spec §6.1).
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	_, err = h.q.ServersGetInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

// allow takes a command token of userID; without one it writes 429 with Retry-After.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, userID int64) bool {
	d := h.limit.take(userID, h.clock.Now())
	if d <= 0 {
		return true
	}
	secs := int(math.Ceil(d.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many commands, try again later")
	return false
}

// writeAgentError maps a failed agent call to the API error. The order matters: a 404/409
// *agent.StatusError also matches instancespec.ErrInvalid, a 503 also agentapi.ErrUnavailable.
func writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agentapi.ErrNotRunning), errors.Is(err, agentapi.ErrNotFound):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeInstanceNotRunning, "the server is not running")
	case errors.Is(err, agentapi.ErrRconUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeRconUnavailable,
			"the server does not accept commands yet (RCON not ready)")
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeDockerUnavailable, "docker is not available on the host")
	case errors.Is(err, agentapi.ErrBusy):
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "the agent is busy with this server, try again shortly")
	case errors.Is(err, instancespec.ErrInvalid):
		writeValidation(w, r, err.Error())
	case errors.Is(err, agentapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the agent is not reachable")
	default:
		httpx.Internal(w, r, err)
	}
}

var errNoAgent = fmt.Errorf("%w: no agent configured", agentapi.ErrUnavailable)

// noAgent stands in for a missing agent client: every call fails as if the agent were down.
type noAgent struct{}

func (noAgent) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, errNoAgent
}

func (noAgent) Rcon(context.Context, int64, string) (string, error) { return "", errNoAgent }

func (noAgent) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, errNoAgent
}

// denyAll stands in for a missing Access: every recheck fails.
type denyAll struct{}

func (denyAll) Recheck(context.Context, principal.Principal, int64, rbac.Permission) error {
	return ErrAccessRevoked
}
