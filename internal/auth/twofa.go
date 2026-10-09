package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"

	"mcpanel/internal/audit"
	"mcpanel/internal/defense"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

const maxCodeLen = 32

type codeRequest struct {
	Code string `json:"code"`
}

func decodeCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req codeRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return "", false
	}
	if req.Code == "" || len(req.Code) > maxCodeLen {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "code is required")
		return "", false
	}
	return req.Code, true
}

// secondFactorKey identifies the session for the 2FA rate limit (stream B adds the IP).
func secondFactorKey(p principal.Principal) string { return strconv.FormatInt(p.SessionID, 10) }

// consumeTOTP verifies code and atomically marks its time step as used. The step
// counter is shared by login, sudo and activation, so a code never works twice.
// Returns "" on success, otherwise the failure reason.
func (h *Handler) consumeTOTP(ctx context.Context, u store.User, code string) (string, error) {
	if u.TotpEnabled != 1 || len(u.TotpSecretEnc) == 0 {
		return reasonMethodNotEnabled, nil
	}
	secret, err := openTOTPSecret(h.box, u.ID, u.TotpSecretEnc)
	if err != nil {
		return "", err
	}
	step, ok := matchTOTP(secret, code, h.clock.Now())
	if !ok {
		return reasonBadCode, nil
	}
	if step <= u.TotpLastStep {
		return reasonReplayedCode, nil
	}
	n, err := h.q.AuthAdvanceTOTPStep(ctx, store.AuthAdvanceTOTPStepParams{Step: step, ID: u.ID})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return reasonReplayedCode, nil // a concurrent request consumed this step first
	}
	return "", nil
}

func (h *Handler) verifyTOTP(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	reason, err := h.consumeTOTP(ctx, user, code)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.recordAttempt(r, user.Username, user.ID, defense.StageTOTP, false, reason)
		invalidCode(w, r)
		return
	}
	h.completeSecondFactor(w, r, p, user, defense.StageTOTP, "totp")
}

func (h *Handler) verifyRecovery(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	code, ok := decodeCode(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if err := h.guard.CheckSecondFactor(ctx, httpx.ClientIP(ctx), secondFactorKey(p)); err != nil {
		h.guardError(w, r, err)
		return
	}
	n, err := h.q.AuthUseRecoveryCode(ctx, store.AuthUseRecoveryCodeParams{
		UsedAt: sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true}, UserID: user.ID, CodeHash: hashRecoveryCode(code),
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n != 1 {
		h.recordAttempt(r, user.Username, user.ID, defense.StageRecovery, false, reasonBadCode)
		invalidCode(w, r)
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionRecoveryUsed, user.Username, audit.ResultSuccess, nil)
	h.completeSecondFactor(w, r, p, user, defense.StageRecovery, "recovery")
}

// completeSecondFactor promotes a preauth session to full: "complete" attempt (stream B
// derives new-device notifications from it), token + CSRF rotation, audit, Me response.
func (h *Handler) completeSecondFactor(w http.ResponseWriter, r *http.Request, p principal.Principal, u store.User, stage defense.AttemptStage, method string) {
	ctx := r.Context()
	sess, err := h.presentedSession(ctx, p)
	if err != nil {
		sessionError(w, r, err)
		return
	}
	if sess.Stage != string(principal.StagePreAuth) { // a concurrent request already completed it
		sessionGone(w, r)
		return
	}
	tok, sess, err := h.sessions.Rotate(ctx, sess, Rotation{Stage: principal.StageFull, NewCSRF: true})
	if err != nil {
		sessionError(w, r, err)
		return
	}
	// Only after the rotation succeeded: stream B derives the new-device notification from "complete".
	h.recordAttempt(r, u.Username, u.ID, stage, true, reasonComplete)
	setSessionCookie(w, tok)
	h.auditLog(ctx, u.ID, u.Username, audit.ActionLogin, u.Username, audit.ResultSuccess, map[string]any{"method": method})
	h.writeMe(w, r, u, sess)
}
