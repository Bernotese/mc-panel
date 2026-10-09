package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/notify"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store"
)

func (h *Handler) userRoutes(api *rbac.Router) {
	api.Get("/users", rbac.Admin(), h.handleListUsers)
	api.Post("/users", rbac.Admin().WithSudo(), h.handleCreateUser)
	api.Get("/users/{id}", rbac.Admin(), h.handleGetUser)
}

// userJSON is the OpenAPI schema User.
type userJSON struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	TotpEnabled  bool   `json:"totp_enabled"`
	PasskeyCount int64  `json:"passkey_count"`
	CreatedAt    int64  `json:"created_at"`
	LastLoginAt  *int64 `json:"last_login_at"`
}

func toUserJSON(ctx context.Context, q *store.Queries, u store.User) (userJSON, error) {
	n, err := q.RbacCountUserPasskeys(ctx, u.ID)
	if err != nil {
		return userJSON{}, err
	}
	out := userJSON{
		ID: u.ID, Username: u.Username, Role: u.Role, TotpEnabled: u.TotpEnabled != 0,
		PasskeyCount: n, CreatedAt: u.CreatedAt,
	}
	ts, err := q.RbacLastLogin(ctx, sql.NullInt64{Int64: u.ID, Valid: true})
	switch {
	case err == nil:
		out.LastLoginAt = &ts
	case !errors.Is(err, sql.ErrNoRows):
		return userJSON{}, err
	}
	return out, nil
}

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	list, err := h.q.RbacListUsers(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]userJSON, 0, len(list))
	for _, u := range list {
		j, err := toUserJSON(ctx, h.q, u)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out = append(out, j)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createUserRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	u, err := newUser(ctx, h.q, h.clock.Now(), req.Username, req.Password, req.Role)
	switch {
	case errors.Is(err, ErrUsernameTaken):
		writeConflict(w, r, err.Error())
		return
	case isValidationError(err):
		writeValidation(w, r, err.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionUserCreate, userTarget(u.ID), audit.ResultSuccess,
		map[string]any{"username": u.Username, "role": u.Role})
	h.emit(r, notify.EventUserCreated, "User created",
		fmt.Sprintf("%s created user %s with role %s", callerName(r), u.Username, u.Role), u.Username)
	out, err := toUserJSON(ctx, h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleGetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	u, err := h.q.RbacGetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r, "user")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := toUserJSON(ctx, h.q, u)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
