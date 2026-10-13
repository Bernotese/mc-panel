package monitoring

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/rbac"
)

// wantRoutes are exactly the monitoring operations of api/openapi.yaml (V3); the test router
// has no /api prefix.
var wantRoutes = map[string]rbac.Rule{
	"GET /servers/{id}/metrics":      rbac.Server(rbac.PermServerView, "id"),
	"GET /servers/{id}/metrics/live": rbac.Server(rbac.PermServerView, "id"),
	"PUT /servers/{id}/alerts":       rbac.Admin(),
	"GET /monitoring/overview":       rbac.Authenticated(),
	"GET /monitoring/host":           rbac.Admin(),
	"GET /monitoring/settings":       rbac.Admin(),
	"PUT /monitoring/settings":       rbac.Admin().WithSudo(),
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	got := e.rt.Routes()
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
	if un := rbac.Uncovered(e.mux, got, func(string, string) bool { return false }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Anonymous requests are 401; a user without server.view is 403 on server routes and on the
// admin routes; overview is open to every signed-in user.
func TestRoutesCheckPermissions(t *testing.T) {
	e := newEnv(t)
	id := e.server("survival")
	bob := e.user("bob", nil)
	path := func(p string) string { return strings.ReplaceAll(p, "{id}", "1") }
	_ = id
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		p := path(pattern)
		if strings.Contains(p, "metrics") || strings.Contains(p, "/monitoring/host") {
			p += "?range=1h"
			if strings.HasSuffix(p, "live?range=1h") {
				p = strings.TrimSuffix(p, "?range=1h")
			}
		}
		if rec := e.do(method, p, map[string]any{}, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
		want := http.StatusForbidden
		if key == "GET /monitoring/overview" {
			want = http.StatusOK
		}
		if rec := e.do(method, p, map[string]any{}, &bob); rec.Code != want {
			t.Errorf("%s as user without rights: %d, want %d", key, rec.Code, want)
		}
	}
	noSudo := e.adminNoSudo()
	body := map[string]any{"disk_min_percent": 10, "disk_min_bytes": 10 << 30, "down_after_min": 3, "crash_restarts": 3, "crash_window_min": 10}
	if rec := e.do(http.MethodPut, "/monitoring/settings", body, &noSudo); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("PUT settings without sudo: %d %s", rec.Code, rec.Body)
	}
}
