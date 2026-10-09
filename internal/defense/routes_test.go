package defense

import (
	"maps"
	"testing"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/rbac"
)

// TestRoutesDeclareRules pins every security route to the rule from the OpenAPI table.
func TestRoutesDeclareRules(t *testing.T) {
	e := newEnv(t)
	rt := rbac.NewRouter(chi.NewRouter(), denyChecker{}, e.clk)
	NewHandler(e.svc).Routes(rt)
	const admin, adminSudo = "admin[full]", "admin+sudo[full]"
	want := map[string]string{
		"GET /security/overview":         admin,
		"GET /security/attempts":         admin,
		"GET /security/jails":            admin,
		"POST /security/jails":           adminSudo,
		"DELETE /security/jails/{id}":    adminSudo,
		"GET /security/ip-rules":         admin,
		"POST /security/ip-rules":        adminSudo,
		"DELETE /security/ip-rules/{id}": adminSudo,
		"GET /security/settings":         admin,
		"PUT /security/settings":         adminSudo,
		"GET /security/agent":            admin,
	}
	got := map[string]string{}
	for _, ri := range rt.Routes() {
		got[ri.Method+" "+ri.Pattern] = ri.Rule.String()
	}
	if !maps.Equal(got, want) {
		t.Fatalf("routes =\n%v\nwant\n%v", got, want)
	}
}
