package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"mcpanel/internal/store"
)

const (
	ceremonyLogin    = "login"
	ceremonySudo     = "sudo"
	ceremonyRegister = "register"
	ceremonyTTL      = 5 * time.Minute
)

var errNoCeremony = errors.New("auth: no pending webauthn ceremony")

// waUser adapts a user row and its stored credentials to webauthn.User.
type waUser struct {
	u     store.User
	creds []webauthn.Credential
}

func (w waUser) WebAuthnID() []byte                         { return w.u.WebauthnHandle }
func (w waUser) WebAuthnName() string                       { return w.u.Username }
func (w waUser) WebAuthnDisplayName() string                { return w.u.Username }
func (w waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func (h *Handler) loadWAUser(ctx context.Context, u store.User) (waUser, []store.WebauthnCredential, error) {
	rows, err := h.q.AuthListCredentials(ctx, u.ID)
	if err != nil {
		return waUser{}, nil, err
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(row.CredentialJson), &c); err != nil {
			return waUser{}, nil, fmt.Errorf("auth: decode credential %d: %w", row.ID, err)
		}
		creds = append(creds, c)
	}
	return waUser{u: u, creds: creds}, rows, nil
}

type ceremonyState struct {
	Kind      string               `json:"kind"`
	CreatedAt int64                `json:"created_at"`
	Data      webauthn.SessionData `json:"data"`
}

func (h *Handler) saveCeremony(ctx context.Context, sessionID int64, kind string, data *webauthn.SessionData) error {
	b, err := json.Marshal(ceremonyState{Kind: kind, CreatedAt: h.clock.Now().UnixMilli(), Data: *data})
	if err != nil {
		return err
	}
	return h.q.AuthSetWebAuthnState(ctx, store.AuthSetWebAuthnStateParams{
		WebauthnState: sql.NullString{String: string(b), Valid: true}, ID: sessionID,
	})
}

// takeWebAuthnState removes and returns the pending ceremony state. Of several
// concurrent callers exactly one wins: the clear is a compare-and-swap on the stored
// value (SQLite's UPDATE ... RETURNING would only yield the already-cleared row).
// Returns sql.ErrNoRows if there is no pending state or another caller took it.
func (h *Handler) takeWebAuthnState(ctx context.Context, sessionID int64) (sql.NullString, error) {
	state, err := h.q.AuthGetWebAuthnState(ctx, sessionID)
	if err != nil {
		return sql.NullString{}, err
	}
	if !state.Valid {
		return sql.NullString{}, sql.ErrNoRows
	}
	n, err := h.q.AuthClearWebAuthnState(ctx, store.AuthClearWebAuthnStateParams{ID: sessionID, State: state})
	if err != nil {
		return sql.NullString{}, err
	}
	if n != 1 {
		return sql.NullString{}, sql.ErrNoRows
	}
	return state, nil
}

// takeCeremony removes the pending state (one-shot) and only returns
// it if it belongs to the expected ceremony kind and is younger than ceremonyTTL.
func (h *Handler) takeCeremony(ctx context.Context, sessionID int64, kind string) (webauthn.SessionData, error) {
	raw, err := h.takeWebAuthnState(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return webauthn.SessionData{}, errNoCeremony
	}
	if err != nil {
		return webauthn.SessionData{}, err
	}
	var st ceremonyState
	if err := json.Unmarshal([]byte(raw.String), &st); err != nil || st.Kind != kind {
		return webauthn.SessionData{}, errNoCeremony
	}
	if h.clock.Now().Sub(time.UnixMilli(st.CreatedAt)) > ceremonyTTL {
		return webauthn.SessionData{}, errNoCeremony
	}
	return st.Data, nil
}
