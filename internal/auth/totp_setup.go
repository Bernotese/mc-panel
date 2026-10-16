package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

var (
	errLastSecondFactor = errors.New("auth: would remove the last mandatory second factor")
	errNotEnabled       = errors.New("auth: method not enabled")
	errNotFound         = errors.New("auth: not found")
	errSudoRequired     = errors.New("auth: sudo required to add another second factor")
	errPasswordRequired = errors.New("auth: current password required for the first second factor")
)

// pendingTOTPTTL is how long a secret from POST /me/totp/setup can be activated.
const pendingTOTPTTL = 10 * time.Minute

type totpSetupResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
}

type recoveryCodesResponse struct {
	Codes []string `json:"codes"`
}

// withTx runs fn in a write transaction. store.Open sets _txlock=immediate, so the
// transaction takes the write lock at BEGIN and concurrent factor removals serialise.
// Never call h.auditLog inside fn (the audit recorder opens its own transaction).
func (h *Handler) withTx(ctx context.Context, fn func(q *store.Queries) error) error {
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

// requireSudoIf2FA: once a user has any second factor, adding another one needs sudo.
func (h *Handler) requireSudoIf2FA(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User) bool {
	f, err := h.factors(r.Context(), h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return false
	}
	if f.has2FA() && !h.clock.Now().Before(p.SudoUntil) {
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSudoRequired, "re-authentication required")
		return false
	}
	return true
}

func (h *Handler) replaceRecoveryCodes(ctx context.Context, q *store.Queries, userID int64, now int64) ([]string, error) {
	codes, err := generateRecoveryCodes()
	if err != nil {
		return nil, err
	}
	if err := q.AuthDeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	for _, c := range codes {
		if err := q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{
			UserID: userID, CodeHash: h.hashRecoveryCode(c), CreatedAt: now,
		}); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

// issueRecoveryCodesIfNone issues a fresh set when the user has no unused codes
// (first 2FA method); otherwise it returns an empty list.
func (h *Handler) issueRecoveryCodesIfNone(ctx context.Context, q *store.Queries, userID int64, now int64) ([]string, error) {
	n, err := q.AuthCountUnusedRecoveryCodes(ctx, userID)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return []string{}, nil
	}
	return h.replaceRecoveryCodes(ctx, q, userID, now)
}

// promoteSetup upgrades a setup-stage session to full once a 2FA method exists.
// The token rotates; the CSRF token is kept (the response is not a Me object).
// A session whose stored stage is already full (downgraded to setup only by the loader because
// 2FA became mandatory) needs no rotation.
func (h *Handler) promoteSetup(w http.ResponseWriter, r *http.Request, p principal.Principal) error {
	if p.Stage != principal.StageSetup {
		return nil
	}
	ctx := r.Context()
	sess, err := h.presentedSession(ctx, p)
	if err != nil {
		return err
	}
	if sess.Stage != string(principal.StageSetup) {
		return nil
	}
	tok, _, err := h.sessions.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, SudoUntil: sess.SudoUntil})
	if err != nil {
		return err
	}
	setSessionCookie(w, tok)
	return nil
}

// promoteSetupBestEffort runs after the factor transaction committed. A failure must never hide the
// freshly issued recovery codes, so it is only logged; the user simply logs in again.
func (h *Handler) promoteSetupBestEffort(w http.ResponseWriter, r *http.Request, p principal.Principal) {
	if err := h.promoteSetup(w, r, p); err != nil {
		slog.ErrorContext(r.Context(), "promote setup session", "err", err, "session_id", p.SessionID)
	}
}

// guardAddFactor runs inside the write transaction that adds a second factor. If the user already
// has any factor, adding another needs fresh sudo (a setup-stage session can never hold sudo, so a
// session that raced with another enrolment is locked out). Otherwise this is the first factor and
// ALL other sessions of the user are revoked: each of them was authenticated by password only, and
// even a stored "full" session may only look harmless (the loader downgrades it to setup when 2FA
// is mandatory), so it must not regain access without presenting a second factor.
//
// passwordVerified reports whether the request presented the correct current password
// (confirmFirstFactor). The first factor needs it; it is checked here again, inside the
// transaction, because another method may have been removed since confirmFirstFactor looked.
func (h *Handler) guardAddFactor(ctx context.Context, q *store.Queries, p principal.Principal, userID int64, passwordVerified bool) error {
	// The enrolling session must still be active: a session revoked (or expired) after the loader
	// authenticated the request must not be able to establish a permanent factor.
	sess, err := q.AuthGetSessionByID(ctx, p.SessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	if sess.UserID != userID || !h.sessions.valid(sess) {
		return ErrNoSession
	}
	u, err := q.AuthGetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	passkeys, err := q.AuthCountCredentials(ctx, userID)
	if err != nil {
		return err
	}
	if u.TotpEnabled == 1 || passkeys > 0 {
		if !h.clock.Now().Before(p.SudoUntil) {
			return errSudoRequired
		}
		return nil
	}
	if !passwordVerified {
		return errPasswordRequired
	}
	_, err = q.AuthRevokeOtherSessions(ctx, store.AuthRevokeOtherSessionsParams{
		RevokedAt: sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true}, UserID: userID, KeepID: p.SessionID,
	})
	return err
}

func (h *Handler) factorRemovalError(w http.ResponseWriter, r *http.Request, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errLastSecondFactor):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "cannot remove the last two-factor method")
	case errors.Is(err, errNotEnabled):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "TOTP is not enabled")
	case errors.Is(err, errNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "passkey not found")
	default:
		httpx.Internal(w, r, err)
	}
	return true
}

func (h *Handler) notify2FAChanged(ctx context.Context, u store.User, change string) {
	ip := ""
	if a := httpx.ClientIP(ctx); a.IsValid() {
		ip = a.String()
	}
	h.notifier.Notify(ctx, notify.Event{
		Type: notify.Event2FAChanged, Title: "Two-factor settings changed",
		Message: fmt.Sprintf("%s: %s", u.Username, change), IP: ip, Username: u.Username, Time: h.clock.Now(),
	})
}

func (h *Handler) setupTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok || !h.requireSudoIf2FA(w, r, p, user) {
		return
	}
	if user.TotpEnabled == 1 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "TOTP is already enabled; disable it first")
		return
	}
	secret, url, err := newTOTPKey(user.Username)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	enc, err := sealPendingTOTPSecret(h.box, user.ID, secret, h.clock.Now())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n, err := h.q.AuthSetPendingTOTP(r.Context(), store.AuthSetPendingTOTPParams{
		TotpSecretEnc: enc, Now: h.clock.Now().UnixMilli(), ID: user.ID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n != 1 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "TOTP is already enabled; disable it first")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, totpSetupResponse{Secret: secret, OtpauthURL: url})
}

type totpEnableRequest struct {
	Code            string `json:"code"`
	CurrentPassword string `json:"current_password"`
}

func (h *Handler) enableTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req totpEnableRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	code := req.Code
	if code == "" || len(code) > maxCodeLen {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "code is required")
		return
	}
	if user.TotpEnabled == 1 || len(user.TotpSecretEnc) == 0 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no TOTP setup pending")
		return
	}
	pwVerified, ok := h.confirmFirstFactor(w, r, user, req.CurrentPassword)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p), user.Username); err != nil {
		h.guardError(w, r, err)
		return
	}
	secret, created, err := openPendingTOTPSecret(h.box, user.ID, user.TotpSecretEnc)
	if errors.Is(err, errNoPendingTOTP) || (err == nil && h.clock.Now().Sub(created) > pendingTOTPTTL) {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no TOTP setup pending or it expired; start again")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	step, matched := matchTOTP(secret, code, h.clock.Now())
	if !matched || step <= user.TotpLastStep {
		invalidCode(w, r)
		return
	}
	enc, err := sealTOTPSecret(h.box, user.ID, secret)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	var codes []string
	err = h.withTx(ctx, func(q *store.Queries) error {
		if err := h.guardAddFactor(ctx, q, p, user.ID, pwVerified); err != nil {
			return err
		}
		n, err := q.AuthEnableTOTP(ctx, store.AuthEnableTOTPParams{
			SecretEnc: enc, PendingEnc: user.TotpSecretEnc, Step: step, Now: now, ID: user.ID,
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return errNotEnabled
		}
		codes, err = h.issueRecoveryCodesIfNone(ctx, q, user.ID, now)
		return err
	})
	if errors.Is(err, ErrNoSession) {
		sessionGone(w, r)
		return
	}
	if errors.Is(err, errSudoRequired) {
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSudoRequired, "re-authentication required")
		return
	}
	if errors.Is(err, errPasswordRequired) {
		passwordRequired(w, r)
		return
	}
	if errors.Is(err, errNotEnabled) {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no TOTP setup pending")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.promoteSetupBestEffort(w, r, p)
	h.auditLog(ctx, user.ID, user.Username, audit.ActionTOTPEnable, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "TOTP enabled")
	httpx.WriteJSON(w, http.StatusOK, recoveryCodesResponse{Codes: codes})
}

func (h *Handler) disableTOTP(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	now := h.clock.Now().UnixMilli()
	err := h.withTx(ctx, func(q *store.Queries) error {
		u, err := q.AuthGetUserByID(ctx, user.ID)
		if err != nil {
			return err
		}
		if u.TotpEnabled != 1 {
			return errNotEnabled
		}
		passkeys, err := q.AuthCountCredentials(ctx, u.ID)
		if err != nil {
			return err
		}
		if passkeys == 0 {
			mandatory, err := h.twoFactorMandatory(ctx, q, u)
			if err != nil {
				return err
			}
			if mandatory {
				return errLastSecondFactor
			}
		}
		if _, err := q.AuthDisableTOTP(ctx, store.AuthDisableTOTPParams{Now: now, ID: u.ID}); err != nil {
			return err
		}
		if passkeys == 0 {
			return q.AuthDeleteRecoveryCodes(ctx, u.ID)
		}
		return nil
	})
	if h.factorRemovalError(w, r, err) {
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionTOTPDisable, user.Username, audit.ResultSuccess, nil)
	h.notify2FAChanged(ctx, user, "TOTP disabled")
	w.WriteHeader(http.StatusNoContent)
}

func passwordRequired(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeInvalidCredentials,
		"current_password is required to set up the first two-factor method")
}
