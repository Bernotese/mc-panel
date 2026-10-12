package backups_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var (
	backupsRule = rbac.Server(rbac.PermServerBackups, "id")
	snap        = strings.Repeat("ab", 32)
)

// wantRoutes are exactly the backups operations of api/openapi.yaml (V3); the test router has
// no /api prefix.
var wantRoutes = map[string]rbac.Rule{
	"GET /servers/{id}/backups":                     backupsRule,
	"POST /servers/{id}/backups":                    backupsRule,
	"GET /servers/{id}/backups/job":                 backupsRule,
	"PUT /servers/{id}/backups/schedule":            backupsRule,
	"POST /servers/{id}/backups/{snapshot}/pin":     backupsRule,
	"POST /servers/{id}/backups/{snapshot}/restore": backupsRule.WithSudo(),
	"DELETE /servers/{id}/backups/{snapshot}":       backupsRule.WithSudo(),
	"GET /servers/{id}/backups/{snapshot}/download": backupsRule,
}

func expand(pattern string) string {
	return strings.NewReplacer("{id}", "1", "{snapshot}", snap).Replace(pattern)
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	got := e.rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range wantRoutes {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(e.mux, got, func(string, string) bool { return false }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// A user without server.backups is refused; restore and delete need sudo even for admins;
// anonymous requests are 401.
func TestRoutesCheckPermissionAndSudo(t *testing.T) {
	e := newEnv(t)
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		if rec := e.do(method, expand(pattern), "{}", &bob); rec.Code != http.StatusForbidden {
			t.Errorf("%s without server.backups: %d", key, rec.Code)
		}
		if rec := e.do(method, expand(pattern), "{}", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
	}
	noSudo := e.admin()
	for _, key := range []string{"POST /servers/{id}/backups/{snapshot}/restore", "DELETE /servers/{id}/backups/{snapshot}"} {
		method, pattern, _ := strings.Cut(key, " ")
		rec := e.do(method, expand(pattern), "{}", &noSudo)
		if rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
			t.Errorf("%s without sudo: %d %s", key, rec.Code, rec.Body)
		}
	}
}

// stubbed are the routes that still answer 503 backups_unavailable; each task of phase B2
// removes its routes, task 13 deletes the list and this test.
var stubbed = []struct{ method, path string }{
	{http.MethodGet, "/backups"},
	{http.MethodPost, "/backups/" + strings.Repeat("a", 64) + "/pin"},
	{http.MethodPost, "/backups/" + strings.Repeat("a", 64) + "/restore"},
	{http.MethodDelete, "/backups/" + strings.Repeat("a", 64)},
	{http.MethodGet, "/backups/" + strings.Repeat("a", 64) + "/download"},
}

func TestRemainingStubs(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	for _, s := range stubbed {
		p := e.sudoAdmin()
		rec := e.do(s.method, fmt.Sprintf("/servers/%d%s", id, s.path), "{}", &p)
		if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "backups_unavailable" {
			t.Errorf("%s %s = %d %s", s.method, s.path, rec.Code, rec.Body)
		}
	}
}
