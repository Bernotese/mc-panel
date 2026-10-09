package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/store"
)

const maxPasskeyName = 64

type passkeyRegistrationRequest struct {
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

type passkeyJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt *int64 `json:"last_used_at"`
}

type passkeyCreatedResponse struct {
	Passkey       passkeyJSON `json:"passkey"`
	RecoveryCodes []string    `json:"recovery_codes"`
}

func toPasskeyJSON(c store.WebauthnCredential) passkeyJSON {
	p := passkeyJSON{ID: c.ID, Name: c.Name, CreatedAt: c.CreatedAt}
	if c.LastUsedAt.Valid {
		v := c.LastUsedAt.Int64
		p.LastUsedAt = &v
	}
	return p
}

func (h *Handler) beginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok || !h.requireSudoIf2FA(w, r, p, user) {
		return
	}
	ctx := r.Context()
	wu, _, err := h.loadWAUser(ctx, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	exclusions := make([]protocol.CredentialDescriptor, 0, len(wu.creds))
	for i := range wu.creds {
		exclusions = append(exclusions, wu.creds[i].Descriptor())
	}
	creation, data, err := h.wa.BeginRegistration(wu, webauthn.WithExclusions(exclusions))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.saveCeremony(ctx, p.SessionID, ceremonyRegister, data); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, creation)
}

func (h *Handler) finishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	p, user, ok := h.current(w, r)
	if !ok {
		return
	}
	var req passkeyRegistrationRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > maxPasskeyName || len(req.Credential) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "name (1-64 characters) and credential are required")
		return
	}
	ctx := r.Context()
	data, err := h.takeCeremony(ctx, p.SessionID, ceremonyRegister)
	if errors.Is(err, errNoCeremony) {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "no passkey registration in progress")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid passkey response")
		return
	}
	wu, _, err := h.loadWAUser(ctx, user)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	cred, err := h.wa.CreateCredential(wu, data, parsed)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "passkey verification failed")
		return
	}
	credJSON, err := json.Marshal(cred)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	var row store.WebauthnCredential
	var codes []string
	err = h.withTx(ctx, func(q *store.Queries) error {
		if err := h.guardAddFactor(ctx, q, p, user.ID); err != nil {
			return err
		}
		var err error
		row, err = q.AuthInsertCredential(ctx, store.AuthInsertCredentialParams{
			UserID: user.ID, CredentialID: cred.ID, Name: name, CredentialJson: string(credJSON), CreatedAt: now,
		})
		if err != nil {
			return err
		}
		codes, err = h.issueRecoveryCodesIfNone(ctx, q, user.ID, now)
		return err
	})
	switch {
	case errors.Is(err, ErrNoSession):
		sessionGone(w, r)
		return
	case errors.Is(err, errSudoRequired):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSudoRequired, "re-authentication required")
		return
	case store.IsUniqueViolation(err):
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "this passkey is already registered")
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.promoteSetupBestEffort(w, r, p)
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasskeyAdd, name, audit.ResultSuccess, map[string]any{"passkey_id": row.ID})
	h.notify2FAChanged(ctx, user, "passkey added: "+name)
	httpx.WriteJSON(w, http.StatusCreated, passkeyCreatedResponse{Passkey: toPasskeyJSON(row), RecoveryCodes: codes})
}

func (h *Handler) listPasskeys(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	rows, err := h.q.AuthListCredentials(r.Context(), user.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]passkeyJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPasskeyJSON(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) deletePasskey(w http.ResponseWriter, r *http.Request) {
	_, user, ok := h.current(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "passkey not found")
		return
	}
	ctx := r.Context()
	var name string
	err = h.withTx(ctx, func(q *store.Queries) error {
		u, err := q.AuthGetUserByID(ctx, user.ID)
		if err != nil {
			return err
		}
		cred, err := q.AuthGetCredential(ctx, store.AuthGetCredentialParams{ID: id, UserID: u.ID})
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		count, err := q.AuthCountCredentials(ctx, u.ID)
		if err != nil {
			return err
		}
		remaining := count - 1
		if u.TotpEnabled == 1 {
			remaining++
		}
		if remaining == 0 {
			mandatory, err := h.twoFactorMandatory(ctx, q, u)
			if err != nil {
				return err
			}
			if mandatory {
				return errLastSecondFactor
			}
		}
		if _, err := q.AuthDeleteCredential(ctx, store.AuthDeleteCredentialParams{ID: id, UserID: u.ID}); err != nil {
			return err
		}
		name = cred.Name
		if remaining == 0 {
			return q.AuthDeleteRecoveryCodes(ctx, u.ID)
		}
		return nil
	})
	if h.factorRemovalError(w, r, err) {
		return
	}
	h.auditLog(ctx, user.ID, user.Username, audit.ActionPasskeyRemove, name, audit.ResultSuccess, map[string]any{"passkey_id": id})
	h.notify2FAChanged(ctx, user, "passkey removed: "+name)
	w.WriteHeader(http.StatusNoContent)
}
