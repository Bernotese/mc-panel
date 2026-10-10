package rbac

import (
	"context"
	"fmt"

	"mcpanel/internal/store"
)

// DBChecker answers per-server permission questions from the permissions table.
// Admins never reach the checker: the router lets them pass before asking it.
// server.view is stored explicitly (see users: granting any permission implies view),
// so the checker only needs an exact lookup.
type DBChecker struct {
	q *store.Queries
}

var _ Checker = (*DBChecker)(nil)

func NewDBChecker(db store.DBTX) *DBChecker {
	return &DBChecker{q: store.New(db)}
}

func (c *DBChecker) HasServerPermission(ctx context.Context, userID, serverID int64, p Permission) (bool, error) {
	if !p.Valid() {
		return false, nil
	}
	n, err := c.q.RbacHasPermission(ctx, store.RbacHasPermissionParams{
		UserID:     userID,
		ServerID:   serverID,
		Permission: string(p),
	})
	if err != nil {
		return false, fmt.Errorf("rbac: check permission: %w", err)
	}
	return n > 0, nil
}
