package rbac_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

type fakeChecker map[[2]int64][]rbac.Permission

func (f fakeChecker) HasServerPermission(_ context.Context, uid, sid int64, p rbac.Permission) (bool, error) {
	for _, have := range f[[2]int64{uid, sid}] {
		if have == p {
			return true, nil
		}
	}
	return false, nil
}

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (http.Handler, *rbac.Router) {
	t.Helper()
	mux := chi.NewRouter()
	checker := fakeChecker{{2, 10}: {rbac.PermServerView, rbac.PermServerPower}}
	rt := rbac.NewRouter(mux, checker, clock.NewFake(now))
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), ok)
		api.Get("/me", rbac.Authenticated().AllowStages(principal.StagePreAuth, principal.StageSetup, principal.StageFull), ok)
		api.Post("/me/password", rbac.Authenticated().AllowStages(principal.StagePassword, principal.StageFull), ok)
		api.Get("/servers", rbac.Authenticated(), ok)
		api.Post("/users", rbac.Admin().WithSudo(), ok)
		api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), ok)
	})
	return mux, rt
}

func do(h http.Handler, method, path string, p *principal.Principal) (int, string) {
	req := httptest.NewRequest(method, path, nil)
	if p != nil {
		req = req.WithContext(principal.With(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Error
}

func TestEnforcement(t *testing.T) {
	h, _ := setup(t)
	admin := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageFull}
	adminSudo := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: now.Add(time.Minute)}
	adminSudoExpired := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: now}
	user := &principal.Principal{UserID: 2, Role: principal.RoleUser, Stage: principal.StageFull}
	other := &principal.Principal{UserID: 3, Role: principal.RoleUser, Stage: principal.StageFull}
	pre := &principal.Principal{UserID: 2, Role: principal.RoleUser, Stage: principal.StagePreAuth}
	setupStage := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageSetup}
	passwordStage := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StagePassword, SudoUntil: now.Add(time.Minute)}

	cases := []struct {
		name, method, path string
		p                  *principal.Principal
		code               int
		err                string
	}{
		{"public without session", "GET", "/api/health", nil, 204, ""},
		{"auth route without session", "GET", "/api/servers", nil, 401, "unauthorized"},
		{"preauth cannot use full route", "GET", "/api/servers", pre, 401, "unauthorized"},
		{"setup stage gets setup error", "GET", "/api/servers", setupStage, 403, "setup_2fa_required"},
		{"preauth allowed on /me", "GET", "/api/me", pre, 204, ""},
		{"password stage gets password error", "GET", "/api/servers", passwordStage, 403, "password_change_required"},
		{"password stage on a route of the other stages", "GET", "/api/me", passwordStage, 403, "password_change_required"},
		{"password stage on an admin route even with sudo", "POST", "/api/users", passwordStage, 403, "password_change_required"},
		{"password stage may change the password", "POST", "/api/me/password", passwordStage, 204, ""},
		{"setup stage cannot use the password route", "POST", "/api/me/password", setupStage, 403, "setup_2fa_required"},
		{"user on admin route", "POST", "/api/users", user, 403, "forbidden"},
		{"admin without sudo", "POST", "/api/users", admin, 403, "sudo_required"},
		{"admin with expired sudo", "POST", "/api/users", adminSudoExpired, 403, "sudo_required"},
		{"admin with sudo", "POST", "/api/users", adminSudo, 204, ""},
		{"user with power on server 10", "POST", "/api/servers/10/power", user, 204, ""},
		{"user without permission on server 11", "POST", "/api/servers/11/power", user, 403, "forbidden"},
		{"other user on server 10", "POST", "/api/servers/10/power", other, 403, "forbidden"},
		{"admin on any server", "POST", "/api/servers/11/power", admin, 204, ""},
		{"non-numeric server id", "POST", "/api/servers/abc/power", user, 404, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, errCode := do(h, tc.method, tc.path, tc.p)
			if code != tc.code || errCode != tc.err {
				t.Fatalf("got %d %q, want %d %q", code, errCode, tc.code, tc.err)
			}
		})
	}
}

func TestRoutesRegistry(t *testing.T) {
	_, rt := setup(t)
	routes := rt.Routes()
	if len(routes) != 6 {
		t.Fatalf("len(routes) = %d", len(routes))
	}
	found := false
	for _, r := range routes {
		if r.Method == "POST" && r.Pattern == "/api/users" && r.Rule.RequiresSudo() && !r.Rule.IsPublic() {
			found = true
		}
	}
	if !found {
		t.Fatalf("POST /api/users not registered correctly: %+v", routes)
	}
}

func TestZeroRulePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for zero Rule")
		}
	}()
	rt := rbac.NewRouter(chi.NewRouter(), fakeChecker{}, clock.Real{})
	rt.Get("/x", rbac.Rule{}, func(http.ResponseWriter, *http.Request) {})
}

func mustPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected panic", what)
		}
	}()
	fn()
}

func TestServerRuleParamMissingFromPatternPanics(t *testing.T) {
	mustPanic(t, "server rule with param not in pattern", func() {
		rt := rbac.NewRouter(chi.NewRouter(), fakeChecker{}, clock.Real{})
		rt.Route("/api", func(api *rbac.Router) {
			api.Post("/servers/{idx}/power", rbac.Server(rbac.PermServerPower, "id"), func(http.ResponseWriter, *http.Request) {})
		})
	})
}

func TestDuplicateRoutePanics(t *testing.T) {
	mustPanic(t, "duplicate GET /x", func() {
		rt := rbac.NewRouter(chi.NewRouter(), fakeChecker{}, clock.Real{})
		h := func(http.ResponseWriter, *http.Request) {}
		rt.Get("/x", rbac.Public(), h)
		rt.Get("/x", rbac.Public(), h)
	})
}

func TestSamePatternDifferentMethodAllowed(t *testing.T) {
	rt := rbac.NewRouter(chi.NewRouter(), fakeChecker{}, clock.Real{})
	h := func(http.ResponseWriter, *http.Request) {}
	rt.Get("/x", rbac.Public(), h)
	rt.Post("/x", rbac.Public(), h)
	if n := len(rt.Routes()); n != 2 {
		t.Fatalf("len(routes) = %d, want 2", n)
	}
}

func TestAllowStagesEmptyPanics(t *testing.T) {
	mustPanic(t, "AllowStages() with no stages", func() {
		_ = rbac.Authenticated().AllowStages()
	})
}
