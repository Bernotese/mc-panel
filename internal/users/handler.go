// Package users implements user management, per-server permissions and the
// `panel admin create` CLI command (stream D). The server API lives in internal/servers.
package users

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

type Handler struct {
	db       *sql.DB
	q        *store.Queries
	clock    clock.Clock
	rec      audit.Recorder
	notifier notify.Notifier
	// passwordSet is called after an admin set a user's password (see OnPasswordSet).
	passwordSet func(userID int64)
}

// NewHandler expects db from store.Open (BEGIN IMMEDIATE transactions).
func NewHandler(db *sql.DB, c clock.Clock, rec audit.Recorder, n notify.Notifier) *Handler {
	return &Handler{db: db, q: store.New(db), clock: c, rec: rec, notifier: n}
}

// OnPasswordSet registers fn to run after an admin set a user's password. cmd/panel uses it to
// reset the user's second-factor lockout, which lives in the auth handler (spec §7.2).
func (h *Handler) OnPasswordSet(fn func(userID int64)) { h.passwordSet = fn }

// Routes registers all user and permission routes (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
	h.permissionRoutes(api)
}

// pathID parses the {id} URL parameter; false for non-numeric or non-positive values.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

// record writes an audit entry; actor and IP come from the request context. It must only be
// called after any write transaction of the handler has finished (SQLRecorder opens its own).
// The action already happened, so a failing audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		slog.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

// emit sends a security notification about the affected user.
func (h *Handler) emit(r *http.Request, t notify.EventType, title, msg, username string) {
	ip := ""
	if a := httpx.ClientIP(r.Context()); a.IsValid() {
		ip = a.String()
	}
	h.notifier.Notify(r.Context(), notify.Event{
		Type: t, Title: title, Message: msg, IP: ip, Username: username, Time: h.clock.Now(),
	})
}

func callerName(r *http.Request) string {
	p, _ := principal.From(r.Context())
	return p.Username
}

func userTarget(id int64) string { return "user:" + strconv.FormatInt(id, 10) }

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

// writeBusy answers when the password hash could not get an argon2 slot in time (password.ErrBusy).
func writeBusy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "1")
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "server busy, try again shortly")
}

func writeNotFound(w http.ResponseWriter, r *http.Request, what string) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, what+" not found")
}

func writeConflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}
