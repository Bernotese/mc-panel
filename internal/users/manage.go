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
	"mcpanel/internal/password"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store"
)

var (
	ErrNotFound  = errors.New("user not found")
	ErrLastAdmin = errors.New("cannot remove or demote the last admin")
)

func (h *Handler) manageRoutes(api *rbac.Router) {
	api.Patch("/users/{id}", rbac.Admin().WithSudo(), h.handleUpdateUser)
	api.Delete("/users/{id}", rbac.Admin().WithSudo(), h.handleDeleteUser)
	api.Post("/users/{id}/reset-2fa", rbac.Admin().WithSudo(), h.handleReset2FA)
}

// inTx runs fn in a write transaction. store.Open sets _txlock=immediate, so this is
// BEGIN IMMEDIATE: checks such as "is this the last admin?" cannot race.
// Never call h.record inside fn.
func (h *Handler) inTx(ctx context.Context, fn func(q *store.Queries) error) error {
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

func getUser(ctx context.Context, q *store.Queries, id int64) (store.User, error) {
	u, err := q.RbacGetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// requireNotLastAdmin returns ErrLastAdmin if u is an admin and no other admin exists.
func requireNotLastAdmin(ctx context.Context, q *store.Queries, u store.User) error {
	if u.Role != string(principal.RoleAdmin) {
		return nil
	}
	n, err := q.RbacCountAdmins(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	Password *string `json:"password"`
}

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	var req updateUserRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Role == nil && req.Password == nil {
		writeValidation(w, r, "nothing to update: set role and/or password")
		return
	}
	if req.Role != nil && !validRole(*req.Role) {
		writeValidation(w, r, ErrInvalidRole.Error())
		return
	}
	u, err := getUser(ctx, h.q, id)
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w, r, "user")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var newHash string
	if req.Password != nil {
		if err := password.Validate(*req.Password, u.Username); err != nil {
			writeValidation(w, r, err.Error())
			return
		}
		if newHash, err = password.Hash(*req.Password); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}

	now := h.clock.Now().UnixMilli()
	oldRole := u.Role
	err = h.inTx(ctx, func(q *store.Queries) error {
		cur, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		oldRole = cur.Role
		if req.Role != nil && *req.Role != cur.Role {
			if err := requireNotLastAdmin(ctx, q, cur); err != nil {
				return err
			}
			if err := q.RbacUpdateUserRole(ctx, store.RbacUpdateUserRoleParams{Role: *req.Role, UpdatedAt: now, ID: id}); err != nil {
				return err
			}
		}
		if req.Password != nil {
			if err := q.RbacUpdateUserPassword(ctx, store.RbacUpdateUserPasswordParams{
				PasswordHash: newHash, PasswordChangedAt: now, UpdatedAt: now, ID: id,
			}); err != nil {
				return err
			}
			// A password set by an admin invalidates every existing session of that user.
			if err := q.RbacRevokeUserSessions(ctx, store.RbacRevokeUserSessionsParams{
				RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: id,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r, "user")
		return
	case errors.Is(err, ErrLastAdmin):
		h.record(ctx, audit.ActionUserUpdate, userTarget(id), audit.ResultDenied,
			map[string]any{"username": u.Username, "reason": "last_admin"})
		writeConflict(w, r, ErrLastAdmin.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}

	roleChanged := req.Role != nil && *req.Role != oldRole
	details := map[string]any{"username": u.Username}
	if roleChanged {
		details["role_from"] = oldRole
		details["role_to"] = *req.Role
	}
	if req.Password != nil {
		details["password_changed"] = true
	}
	h.record(ctx, audit.ActionUserUpdate, userTarget(id), audit.ResultSuccess, details)
	if roleChanged {
		h.emit(r, notify.EventPermissionsChanged, "Role changed",
			fmt.Sprintf("%s changed the role of %s from %s to %s", callerName(r), u.Username, oldRole, *req.Role), u.Username)
	}

	updated, err := getUser(ctx, h.q, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := toUserJSON(ctx, h.q, updated)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	if p, _ := principal.From(ctx); p.UserID == id {
		h.record(ctx, audit.ActionUserDelete, userTarget(id), audit.ResultDenied, map[string]any{"reason": "self"})
		writeConflict(w, r, "you cannot delete your own account")
		return
	}
	var deleted store.User
	err := h.inTx(ctx, func(q *store.Queries) error {
		u, err := getUser(ctx, q, id)
		if err != nil {
			return err
		}
		if err := requireNotLastAdmin(ctx, q, u); err != nil {
			return err
		}
		if _, err := q.RbacDeleteUser(ctx, id); err != nil {
			return err
		}
		deleted = u
		return nil
	})
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r, "user")
		return
	case errors.Is(err, ErrLastAdmin):
		h.record(ctx, audit.ActionUserDelete, userTarget(id), audit.ResultDenied, map[string]any{"reason": "last_admin"})
		writeConflict(w, r, ErrLastAdmin.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionUserDelete, userTarget(id), audit.ResultSuccess,
		map[string]any{"username": deleted.Username, "role": deleted.Role})
	h.emit(r, notify.EventUserDeleted, "User deleted",
		fmt.Sprintf("%s deleted user %s", callerName(r), deleted.Username), deleted.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleReset2FA(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	now := h.clock.Now().UnixMilli()
	var u store.User
	err := h.inTx(ctx, func(q *store.Queries) error {
		var err error
		if u, err = getUser(ctx, q, id); err != nil {
			return err
		}
		if err := q.RbacClearTOTP(ctx, store.RbacClearTOTPParams{UpdatedAt: now, ID: id}); err != nil {
			return err
		}
		if err := q.RbacDeleteUserPasskeys(ctx, id); err != nil {
			return err
		}
		if err := q.RbacDeleteUserRecoveryCodes(ctx, id); err != nil {
			return err
		}
		return q.RbacRevokeUserSessions(ctx, store.RbacRevokeUserSessionsParams{
			RevokedAt: sql.NullInt64{Int64: now, Valid: true}, UserID: id,
		})
	})
	if errors.Is(err, ErrNotFound) {
		writeNotFound(w, r, "user")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionUserReset2FA, userTarget(id), audit.ResultSuccess, map[string]any{"username": u.Username})
	h.emit(r, notify.Event2FAChanged, "Two-factor authentication reset",
		fmt.Sprintf("%s reset all second factors of %s; all sessions were revoked", callerName(r), u.Username), u.Username)
	w.WriteHeader(http.StatusNoContent)
}
