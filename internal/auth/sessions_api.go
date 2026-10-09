package auth

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

type sessionJSON struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	Username   string `json:"username"`
	Current    bool   `json:"current"`
	IP         string `json:"ip"`
	Country    string `json:"country"`
	UserAgent  string `json:"user_agent"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
	ExpiresAt  int64  `json:"expires_at"`
}

// activeWindow matches Sessions.valid: a session is idle once last_seen_at <= now - Idle.
func (h *Handler) activeWindow() (now, idleCutoff int64) {
	n := h.clock.Now().UnixMilli()
	return n, n - h.cfg.Session.Idle.Milliseconds()
}

func sessionIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return 0, false
	}
	return id, true
}

func (h *Handler) listMySessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	now, cutoff := h.activeWindow()
	rows, err := h.q.AuthListActiveSessionsForUser(r.Context(), store.AuthListActiveSessionsForUserParams{
		Now: now, IdleCutoff: cutoff, UserID: p.UserID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]sessionJSON, 0, len(rows))
	for _, s := range rows {
		out = append(out, sessionJSON{
			ID: s.ID, UserID: s.UserID, Username: s.Username, Current: s.ID == p.SessionID, IP: s.Ip,
			Country: s.Country, UserAgent: s.UserAgent, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) listAllSessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	now, cutoff := h.activeWindow()
	rows, err := h.q.AuthListAllActiveSessions(r.Context(), store.AuthListAllActiveSessionsParams{Now: now, IdleCutoff: cutoff})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]sessionJSON, 0, len(rows))
	for _, s := range rows {
		out = append(out, sessionJSON{
			ID: s.ID, UserID: s.UserID, Username: s.Username, Current: s.ID == p.SessionID, IP: s.Ip,
			Country: s.Country, UserAgent: s.UserAgent, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) revokedAt() sql.NullInt64 {
	return sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true}
}

func (h *Handler) afterRevoke(w http.ResponseWriter, r *http.Request, p principal.Principal, id int64, details map[string]any) {
	if id == p.SessionID {
		clearSessionCookie(w)
	}
	h.auditLog(r.Context(), p.UserID, p.Username, audit.ActionSessionRevoke, fmt.Sprintf("session:%d", id), audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeMySession(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := sessionIDParam(w, r)
	if !ok {
		return
	}
	n, err := h.q.AuthRevokeUserSession(r.Context(), store.AuthRevokeUserSessionParams{
		RevokedAt: h.revokedAt(), ID: id, UserID: p.UserID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	h.afterRevoke(w, r, p, id, nil)
}

func (h *Handler) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	n, err := h.q.AuthRevokeOtherSessions(r.Context(), store.AuthRevokeOtherSessionsParams{
		RevokedAt: h.revokedAt(), UserID: p.UserID, KeepID: p.SessionID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(r.Context(), p.UserID, p.Username, audit.ActionSessionRevoke, "sessions:others", audit.ResultSuccess,
		map[string]any{"count": n})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeAnySession(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := sessionIDParam(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	sess, err := h.q.AuthGetSessionByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n, err := h.q.AuthRevokeSession(ctx, store.AuthRevokeSessionParams{RevokedAt: h.revokedAt(), ID: id})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "session not found")
		return
	}
	h.afterRevoke(w, r, p, id, map[string]any{"user_id": sess.UserID})
}
