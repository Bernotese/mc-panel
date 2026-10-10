package users_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/audit/auditapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
	"github.com/Bernotese/mc-panel/internal/users"
)

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		users.NewHandler(db, clk, &audit.MemoryRecorder{}, notify.Nop{}).Routes(api)
		auditapi.NewHandler(db, clk, notify.Nop{}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())

	// Exactly the rules from the OpenAPI table in the Phase-0 plan (rows marked "D").
	want := map[string]rbac.Rule{
		"GET /api/users":                  rbac.Admin(),
		"POST /api/users":                 rbac.Admin().WithSudo(),
		"GET /api/users/{id}":             rbac.Admin(),
		"PATCH /api/users/{id}":           rbac.Admin().WithSudo(),
		"DELETE /api/users/{id}":          rbac.Admin().WithSudo(),
		"POST /api/users/{id}/reset-2fa":  rbac.Admin().WithSudo(),
		"GET /api/users/{id}/permissions": rbac.Admin(),
		"PUT /api/users/{id}/permissions": rbac.Admin().WithSudo(),
		"GET /api/servers":                rbac.Authenticated(),
		"POST /api/servers":               rbac.Admin().WithSudo(),
		"GET /api/servers/{id}":           rbac.Server(rbac.PermServerView, "id"),
		"DELETE /api/servers/{id}":        rbac.Admin().WithSudo(),
		"POST /api/servers/{id}/power":    rbac.Server(rbac.PermServerPower, "id"),
		"GET /api/audit":                  rbac.Admin(),
		"GET /api/audit/verify":           rbac.Admin(),
	}
	got := rt.Routes()
	if len(got) != len(want) {
		t.Errorf("registered %d routes, want %d", len(got), len(want))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := want[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}

	// Path parameters must still resolve below the /api prefix.
	rootID := storetest.CreateUser(t, db, "root", "admin")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/users/%d", rootID), nil)
	req = req.WithContext(principal.With(req.Context(), principal.Principal{
		UserID: rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: t0.Add(time.Minute),
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/users/{id}: %d %s", rec.Code, rec.Body)
	}
}
