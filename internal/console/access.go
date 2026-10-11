package console

import (
	"context"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// SessionChecker is the part of *auth.Sessions the stream recheck needs.
type SessionChecker interface {
	// Recheck returns the current user row while sessionID still fully authenticates userID.
	Recheck(ctx context.Context, sessionID, userID int64) (store.User, error)
}

// NewAccess re-checks session and permission from the database (spec §6.2): the role comes from
// the current user row, so a demoted admin needs a real grant.
func NewAccess(s SessionChecker, c rbac.Checker) Access { return access{sessions: s, perms: c} }

type access struct {
	sessions SessionChecker
	perms    rbac.Checker
}

func (a access) Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error {
	u, err := a.sessions.Recheck(ctx, p.SessionID, p.UserID)
	if err != nil {
		return err
	}
	if principal.Role(u.Role) == principal.RoleAdmin {
		return nil
	}
	ok, err := a.perms.HasServerPermission(ctx, u.ID, serverID, perm)
	if err != nil {
		return fmt.Errorf("console: recheck permission: %w", err)
	}
	if !ok {
		return ErrAccessRevoked
	}
	return nil
}
