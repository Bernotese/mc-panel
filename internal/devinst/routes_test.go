package devinst_test

import (
	"context"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/devinst"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// wantRoutes are exactly the dev operations of api/openapi.yaml; the test router has no /api prefix.
var wantRoutes = map[string]rbac.Rule{
	"POST /servers/{id}/dev":              rbac.Admin().WithSudo(),
	"GET /servers/{id}/dev":               rbac.Server(rbac.PermServerView, "id"),
	"PATCH /servers/{id}/dev":             rbac.Server(rbac.PermServerFiles, "id"),
	"POST /servers/{id}/dev/sync":         rbac.Server(rbac.PermServerFiles, "id"),
	"GET /servers/{id}/dev/ops":           rbac.Server(rbac.PermServerConsole, "id"),
	"POST /servers/{id}/dev/ops":          rbac.Server(rbac.PermServerConsole, "id"),
	"DELETE /servers/{id}/dev/ops/{name}": rbac.Server(rbac.PermServerConsole, "id"),
}

type noPerms struct{}

func (noPerms) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

func TestRouteContract(t *testing.T) {
	rt := rbac.NewRouter(chi.NewRouter(), noPerms{}, clock.Real{})
	devinst.NewHandler(devinst.Deps{}).Routes(rt)
	got := rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
}
