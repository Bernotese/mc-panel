package servers

import (
	"context"
	"slices"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

func allPerms() []rbac.Permission { return slices.Clone(rbac.AllPermissions) }

// orderedPerms returns the permissions in set in rbac.AllPermissions order (never nil).
func orderedPerms(set map[rbac.Permission]bool) []rbac.Permission {
	out := make([]rbac.Permission, 0, len(set))
	for _, p := range rbac.AllPermissions {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// userPerms returns the stored permissions of a (non-admin) user per server id.
func userPerms(ctx context.Context, q *store.Queries, userID int64) (map[int64][]rbac.Permission, error) {
	rows, err := q.RbacListUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	sets := map[int64]map[rbac.Permission]bool{}
	for _, row := range rows {
		if sets[row.ServerID] == nil {
			sets[row.ServerID] = map[rbac.Permission]bool{}
		}
		sets[row.ServerID][rbac.Permission(row.Permission)] = true
	}
	out := make(map[int64][]rbac.Permission, len(sets))
	for id, set := range sets {
		out[id] = orderedPerms(set)
	}
	return out, nil
}

// serverPerms returns the caller's effective permissions on one server (admins get all).
func serverPerms(ctx context.Context, q *store.Queries, p principal.Principal, serverID int64) ([]rbac.Permission, error) {
	if p.IsAdmin() {
		return allPerms(), nil
	}
	rows, err := q.RbacListUserServerPermissions(ctx, store.RbacListUserServerPermissionsParams{UserID: p.UserID, ServerID: serverID})
	if err != nil {
		return nil, err
	}
	set := make(map[rbac.Permission]bool, len(rows))
	for _, row := range rows {
		set[rbac.Permission(row)] = true
	}
	return orderedPerms(set), nil
}
