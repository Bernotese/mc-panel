package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type assertionRequest struct {
	Credential json.RawMessage `json:"credential"`
}

func (h *Handler) beginWebAuthnLogin(w http.ResponseWriter, r *http.Request) {
	h.beginAssertion(w, r, ceremonyLogin)
}

func (h *Handler) beginSudoWebAuthn(w http.ResponseWriter, r *http.Request) {
	h.beginAssertion(w, r, ceremonySudo)
}

func (h *Handler) beginAssertion(w http.ResponseWriter, r *http.Request, kind string) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	wu, _, err := h.loadWAUser(ctx, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if len(wu.creds) == 0 {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "no passkeys registered")
		return
	}
	assertion, data, err := h.wa.BeginLogin(wu)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.saveCeremony(ctx, p.SessionID, kind, data); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, assertion)
}

func decodeAssertion(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	var req assertionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return nil, false
	}
	if len(req.Credential) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "credential is required")
		return nil, false
	}
	return req.Credential, true
}

// verifyAssertion validates an assertion against the pending ceremony of the given
// kind, rejects non-increasing sign counters and persists the updated credential.
// Returns "" on success, otherwise the failure reason.
func (h *Handler) verifyAssertion(ctx context.Context, p principal.Principal, u store.User, kind string, raw json.RawMessage) (string, error) {
	data, err := h.takeCeremony(ctx, p.SessionID, kind)
	if errors.Is(err, errNoCeremony) {
		return reasonNoCeremony, nil
	}
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(raw)
	if err != nil {
		return reasonBadAssertion, nil //nolint:nilerr // a malformed assertion is a failed attempt, not a server error
	}
	wu, rows, err := h.loadWAUser(ctx, u)
	if err != nil {
		return "", err
	}
	idx := -1
	for i, row := range rows {
		if bytes.Equal(row.CredentialID, parsed.RawID) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return reasonBadAssertion, nil
	}
	prevCount := wu.creds[idx].Authenticator.SignCount
	cred, err := h.wa.ValidateLogin(wu, data, parsed)
	if err != nil {
		return reasonBadAssertion, nil //nolint:nilerr // a failed validation is a failed attempt, not a server error
	}
	newCount := parsed.Response.AuthenticatorData.Counter
	if cred.Authenticator.CloneWarning || ((newCount != 0 || prevCount != 0) && newCount <= prevCount) {
		return reasonCloneWarning, nil
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return "", err
	}
	// Conditional on the counter state we validated against: a concurrent assertion with the same
	// counter loses and is treated like a replay.
	n, err := h.q.AuthUpdateCredentialUse(ctx, store.AuthUpdateCredentialUseParams{
		CredentialJson:    string(b),
		OldCredentialJson: rows[idx].CredentialJson,
		LastUsedAt:        sql.NullInt64{Int64: h.clock.Now().UnixMilli(), Valid: true},
		ID:                rows[idx].ID,
	})
	if err != nil {
		return "", err
	}
	if n != 1 {
		return reasonCloneWarning, nil
	}
	return "", nil
}

func (h *Handler) finishWebAuthnLogin(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	raw, ok := decodeAssertion(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !h.checkSecondFactor(w, r, p, user) {
		return
	}
	reason, err := h.verifyAssertion(ctx, p, user, ceremonyLogin, raw)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.recordAttempt(r, user.Username, user.ID, defense.StageWebAuthn, false, reason)
		invalidCode(w, r)
		return
	}
	h.completeSecondFactor(w, r, p, user, defense.StageWebAuthn, "webauthn")
}

func (h *Handler) finishSudoWebAuthn(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	raw, ok := decodeAssertion(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if !h.checkSecondFactor(w, r, p, user) {
		return
	}
	reason, err := h.verifyAssertion(ctx, p, user, ceremonySudo, raw)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if reason != "" {
		h.sudoFailed(r, user, "webauthn", reason)
		invalidCode(w, r)
		return
	}
	h.grantSudo(w, r, p, user, "webauthn")
}
