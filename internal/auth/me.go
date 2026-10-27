package auth

import (
	"database/sql"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
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
	if !h.verifyCurrentPassword(w, r, user, req.CurrentPassword) {
		return
	}
	if req.NewPassword == req.CurrentPassword { // a forced change must not keep the password the admin knows
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "new_password must differ from the current password")
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
	sess, err := h.presentedSession(ctx, p)
	if err != nil {
		sessionError(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	// The new password, the end of a forced change and the revocation of every other session commit
	// together: a failure must not leave a changed password with the old sessions still alive (or
	// vice versa). A forced change (SEC-45) also rotates this session's token, because the session
	// leaves the password stage; the stored stage stays and decides about a pending 2FA setup.
	var (
		revoked int64
		forced  bool
		tok     string
	)
	// The change is only written while the session that verified the current password is active and
	// that password is still the stored one.
	err = h.withTx(ctx, func(q *store.Queries) error {
		if err := h.requireActiveSession(ctx, q, p); err != nil {
			return err
		}
		cur, err := q.AuthGetUserByID(ctx, user.ID)
		if err != nil {
			return err
		}
		forced = cur.MustChangePassword == 1
		n, err := q.AuthChangePassword(ctx, store.AuthChangePasswordParams{
			PasswordHash: hash, Now: now, ID: user.ID, OldPasswordHash: user.PasswordHash,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return errCredentialChanged
		}
		revoked, err = q.AuthRevokeOtherSessions(ctx, store.AuthRevokeOtherSessionsParams{
			RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: user.ID, KeepID: p.SessionID,
		})
		if err != nil || !forced {
			return err
		}
		tok, _, err = h.sessions.withQueries(q).Rotate(ctx, sess, Rotation{Stage: principal.Stage(sess.Stage)})
		return err
	})
	if err != nil {
		sessionError(w, r, err)
		return
	}
	if forced {
		setSessionCookie(w, tok)
	}
	h.ResetSecondFactorLockout(user.ID) // spec §7.2: a password change lifts the 2FA lockout
	details := map[string]any{"revoked_sessions": revoked}
	if forced {
		details["forced"] = true
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasswordChange, user.Username, audit.ResultSuccess, details)
	h.notifySecurity(ctx, notify.EventPasswordChanged, "Password changed",
		user.Username+" changed their password"+h.fromWhere(ctx)+".", user.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) regenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := h.clock.Now().UnixMilli()
	var codes []string
	err := h.withTx(ctx, func(q *store.Queries) error {
		if err := h.requireActiveSession(ctx, q, p); err != nil {
			return err
		}
		var err error
		codes, err = h.replaceRecoveryCodes(ctx, q, user.ID, now)
		return err
	})
	if err != nil {
		sessionError(w, r, err)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionRecoveryRegenerate, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "recovery codes regenerated")
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{Codes: codes})
}

// verifyCurrentPassword checks the user's current password under the login guard, so a stolen
// session cannot be used to brute-force it. A wrong password is recorded as a failed password
// attempt (it counts towards the jail) and answered with 401 invalid_credentials; the function
// writes every error response itself and returns false then.
func (h *Handler) verifyCurrentPassword(w http.ResponseWriter, r *http.Request, user store.User, pw string) bool {
	ctx := r.Context()
	if err := h.guard.CheckLogin(ctx, httpx.ClientIP(ctx), user.Username); err != nil {
		h.guardError(w, r, err)
		return false
	}
	valid, _, err := password.VerifyContext(ctx, pw, user.PasswordHash)
	if errors.Is(err, password.ErrBusy) {
		busy(w, r)
		return false
	}
	if err != nil {
		valid = false
	}
	if !valid {
		h.recordAttempt(r, user.Username, user.ID, defense.StagePassword, false, reasonBadCurrentPassword)
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials, "current password is incorrect")
		return false
	}
	if h.afterPasswordCheck != nil {
		h.afterPasswordCheck()
	}
	return true
}

// confirmFirstFactor implements SEC-15: enrolling the first second factor locks out every
// other session and hands the enrolling session the recovery codes, so it needs the current
// password (a hijacked password-only session must not be able to take over the account). Once
// a factor exists, adding another one needs sudo instead (guardAddFactor). It returns the
// hash the password was verified against ("" if none was needed); ok is false when an error
// response was written.
func (h *Handler) confirmFirstFactor(w http.ResponseWriter, r *http.Request, user store.User, pw string) (verifiedHash string, ok bool) {
	f, err := h.factors(r.Context(), h.q, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return "", false
	}
	if f.has2FA() {
		return "", true
	}
	if pw == "" || utf8.RuneCountInString(pw) > password.MaxLength {
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials,
			"current_password is required to set up the first two-factor method")
		return "", false
	}
	if !h.verifyCurrentPassword(w, r, user, pw) {
		return "", false
	}
	return user.PasswordHash, true
}
