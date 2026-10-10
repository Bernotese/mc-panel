package auth

import (
	"net/http"

	"mcpanel/internal/audit"
	"mcpanel/internal/defense"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

type sudoResponse struct {
	SudoUntil int64 `json:"sudo_until"`
}

func (h *Handler) sudoTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !h.checkSecondFactor(w, r, p, user) {
		return
	}
	reason, err := h.consumeTOTP(ctx, user, code)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.sudoFailed(r, user, "totp", reason)
		invalidCode(w, r)
		return
	}
	h.grantSudo(w, r, p, user, "totp")
}

func (h *Handler) sudoFailed(r *http.Request, u store.User, method, reason string) {
	h.recordAttempt(r, u.Username, u.ID, defense.StageSudo, false, reason)
	h.auditLog(r.Context(), u.ID, u.Username, audit.ActionSudo, u.Username, audit.ResultFailure,
		map[string]any{"method": method, "reason": reason})
}

// grantSudo sets sudo_until = now + cfg.Session.Sudo and rotates the session token.
func (h *Handler) grantSudo(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User, method string) {
	ctx := r.Context()
	sess, err := h.presentedSession(ctx, p)
	if err != nil {
		sessionError(w, r, err)
		return
	}
	until := h.clock.Now().Add(h.cfg.Session.Sudo.Duration).UnixMilli()
	tok, _, err := h.sessions.Rotate(ctx, sess, Rotation{Stage: principal.Stage(sess.Stage), SudoUntil: until})
	if err != nil {
		sessionError(w, r, err)
		return
	}
	setSessionCookie(w, tok)
	h.recordAttempt(r, u.Username, u.ID, defense.StageSudo, true, reasonSudo)
	h.auditLog(ctx, u.ID, u.Username, audit.ActionSudo, u.Username, audit.ResultSuccess, map[string]any{"method": method})
	httpx.WriteJSON(w, http.StatusOK, sudoResponse{SudoUntil: until})
}
