package auth

import (
	"database/sql"
	"errors"
	"net/http"
	"unicode/utf8"

	"mcpanel/internal/audit"
	"mcpanel/internal/defense"
	"mcpanel/internal/httpx"
	"mcpanel/internal/password"
	"mcpanel/internal/store"
)

type updateMeRequest struct {
	Locale string `json:"locale"`
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req updateMeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Locale != "de" && req.Locale != "en" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, `locale must be "de" or "en"`)
		return
	}
	ctx := r.Context()
	if err := h.q.AuthUpdateLocale(ctx, store.AuthUpdateLocaleParams{
		Locale: req.Locale, Now: h.clock.Now().UnixMilli(), ID: user.ID,
	}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	user.Locale = req.Locale
	sess, err := h.q.AuthGetSessionByID(ctx, p.SessionID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.writeMe(w, r, user, sess)
}

type passwordChangeRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func passwordPolicyMessage(err error) string {
	switch {
	case errors.Is(err, password.ErrTooShort):
		return "password must have at least 12 characters"
	case errors.Is(err, password.ErrTooLong):
		return "password must have at most 256 characters"
	case errors.Is(err, password.ErrCommon):
		return "password is too common"
	case errors.Is(err, password.ErrContainsUsername):
		return "password must not contain the username"
	default:
		return "password does not meet the policy"
	}
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req passwordChangeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.CurrentPassword == "" || utf8.RuneCountInString(req.CurrentPassword) > password.MaxLength {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "current_password is required")
		return
	}
	ctx := r.Context()
	// A stolen session must not allow brute-forcing the current password.
	if err := h.guard.CheckLogin(ctx, httpx.ClientIP(ctx), user.Username); err != nil {
		h.guardError(w, r, err)
		return
	}
	valid, _, err := password.VerifyContext(ctx, req.CurrentPassword, user.PasswordHash)
	if errors.Is(err, password.ErrBusy) {
		busy(w, r)
		return
	}
	if err != nil {
		valid = false
	}
	if !valid {
		h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonBadCurrentPassword)
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "current password is incorrect")
		return
	}
	if err := password.Validate(req.NewPassword, user.Username); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, passwordPolicyMessage(err))
		return
	}
	hash, err := password.HashContext(ctx, req.NewPassword)
	if errors.Is(err, password.ErrBusy) {
		busy(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	if err := h.q.AuthChangePassword(ctx, store.AuthChangePasswordParams{PasswordHash: hash, Now: now, ID: user.ID}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	revoked, err := h.q.AuthRevokeOtherSessions(ctx, store.AuthRevokeOtherSessionsParams{
		RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: user.ID, KeepID: p.SessionID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasswordChange, user.Username, audit.ResultSuccess,
		map[string]any{"revoked_sessions": revoked})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := h.clock.Now().UnixMilli()
	var codes []string
	err := h.withTx(ctx, func(q *store.Queries) error {
		var err error
		codes, err = h.replaceRecoveryCodes(ctx, q, user.ID, now)
		return err
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionRecoveryRegenerate, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "recovery codes regenerated")
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{Codes: codes})
}
