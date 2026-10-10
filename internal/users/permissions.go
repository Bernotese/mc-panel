package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// grantJSON is the OpenAPI schema PermissionGrant.
type grantJSON struct {
	ServerID    int64             `json:"server_id"`
	Permissions []rbac.Permission `json:"permissions"`
}

// userPermissionsJSON is the OpenAPI schema UserPermissions.
type userPermissionsJSON struct {
	Grants []grantJSON `json:"grants"`
}

// setPermissionsRequest is the PUT body; pointers detect missing (required) fields so that
// a body like {} cannot silently wipe all permissions.
type setPermissionsRequest struct {
	Grants *[]struct {
		ServerID    int64              `json:"server_id"`
		Permissions *[]rbac.Permission `json:"permissions"`
	} `json:"grants"`
}

type unknownServerError struct{ id int64 }

func (e unknownServerError) Error() string { return fmt.Sprintf("unknown server_id %d", e.id) }

func (h *Handler) permissionRoutes(api *rbac.Router) {
	api.Get("/users/{id}/permissions", rbac.Admin(), h.handleGetPermissions)
	api.Put("/users/{id}/permissions", rbac.Admin().WithSudo(), h.handleSetPermissions)
}

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

// loadGrants returns the stored grants of a user, ascending by server id (never nil).
func loadGrants(ctx context.Context, q *store.Queries, userID int64) ([]grantJSON, error) {
	rows, err := q.RbacListUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	sets := map[int64]map[rbac.Permission]bool{}
	var ids []int64 // rows are ordered by server_id
	for _, row := range rows {
		if sets[row.ServerID] == nil {
			sets[row.ServerID] = map[rbac.Permission]bool{}
			ids = append(ids, row.ServerID)
		}
		sets[row.ServerID][rbac.Permission(row.Permission)] = true
	}
	grants := make([]grantJSON, 0, len(ids))
	for _, id := range ids {
		grants = append(grants, grantJSON{ServerID: id, Permissions: orderedPerms(sets[id])})
	}
	return grants, nil
}

// normalizeGrants validates the request, merges duplicate server entries, drops servers
// without permissions and adds the implied server.view to every remaining server.
func normalizeGrants(in []grantJSON) (map[int64]map[rbac.Permission]bool, error) {
	want := map[int64]map[rbac.Permission]bool{}
	for _, g := range in {
		if g.ServerID <= 0 {
			return nil, fmt.Errorf("invalid server_id %d", g.ServerID)
		}
		set := want[g.ServerID]
		if set == nil {
			set = map[rbac.Permission]bool{}
			want[g.ServerID] = set
		}
		for _, p := range g.Permissions {
			if !p.Valid() {
				return nil, fmt.Errorf("unknown permission %q", p)
			}
			set[p] = true
		}
	}
	for id, set := range want {
		if len(set) == 0 {
			delete(want, id)
			continue
		}
		// Any server permission is useless without seeing the server.
		set[rbac.PermServerView] = true
	}
	return want, nil
}

func (h *Handler) handleGetPermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	if _, err := getUser(ctx, h.q, id); errors.Is(err, ErrNotFound) {
		writeNotFound(w, r, "user")
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	grants, err := loadGrants(ctx, h.q, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, userPermissionsJSON{Grants: grants})
}

func (h *Handler) handleSetPermissions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r, "user")
		return
	}
	var req setPermissionsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Grants == nil {
		writeValidation(w, r, "grants is required")
		return
	}
	in := make([]grantJSON, 0, len(*req.Grants))
	for _, g := range *req.Grants {
		if g.Permissions == nil {
			writeValidation(w, r, "permissions is required for every grant")
			return
		}
		in = append(in, grantJSON{ServerID: g.ServerID, Permissions: *g.Permissions})
	}
	want, err := normalizeGrants(in)
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	var (
		u      store.User
		grants []grantJSON
	)
	// Validation of server ids and the replacement run in one transaction: all or nothing.
	err = h.inTx(ctx, func(q *store.Queries) error {
		var err error
		if u, err = getUser(ctx, q, id); err != nil {
			return err
		}
		serverIDs := slices.Sorted(maps.Keys(want))
		for _, sid := range serverIDs {
			if _, err := q.RbacGetServer(ctx, sid); errors.Is(err, sql.ErrNoRows) {
				return unknownServerError{id: sid}
			} else if err != nil {
				return err
			}
		}
		if err := q.RbacDeleteUserPermissions(ctx, id); err != nil {
			return err
		}
		for _, sid := range serverIDs {
			for _, p := range orderedPerms(want[sid]) {
				if err := q.RbacInsertPermission(ctx, store.RbacInsertPermissionParams{
					UserID: id, ServerID: sid, Permission: string(p),
				}); err != nil {
					return err
				}
			}
		}
		grants, err = loadGrants(ctx, q, id)
		return err
	})
	var use unknownServerError
	switch {
	case errors.Is(err, ErrNotFound):
		writeNotFound(w, r, "user")
		return
	case errors.As(err, &use):
		writeValidation(w, r, use.Error())
		return
	case err != nil:
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionPermissionsSet, userTarget(id), audit.ResultSuccess,
		map[string]any{"username": u.Username, "grants": grants})
	h.emit(r, notify.EventPermissionsChanged, "Permissions changed",
		fmt.Sprintf("%s changed the server permissions of %s", callerName(r), u.Username), u.Username)
	httpx.WriteJSON(w, http.StatusOK, userPermissionsJSON{Grants: grants})
}
