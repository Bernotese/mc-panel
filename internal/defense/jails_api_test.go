package defense

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/principal"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
)

func TestListJails(t *testing.T) {
	e := newEnv(t)
	active := e.insertJail(t, "203.0.113.1/32", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.2/32", t0.Add(-2*time.Hour), t0.Add(-time.Hour))
	lifted := e.insertJail(t, "203.0.113.3/32", t0, t0.Add(time.Hour))
	if _, err := e.db.Exec("UPDATE jails SET lifted_at = ?, lifted_by = 'admin' WHERE id = ?", t0.UnixMilli(), lifted); err != nil {
		t.Fatal(err)
	}
	rec := e.call(t, http.MethodGet, "/security/jails", nil, callerIP)
	wantStatus(t, rec, http.StatusOK, "")
	items := decode[[]map[string]any](t, rec)
	if len(items) != 1 || items[0]["id"] != float64(active) || items[0]["firewall_synced"] != jsonFalse ||
		items[0]["lifted_at"] != nil || items[0]["manual"] != jsonFalse || items[0]["ip_key"] != "203.0.113.1/32" {
		t.Fatalf("active jails = %v", items)
	}
	for _, k := range []string{"id", "ip_key", "reason", "level", "manual", "created_at", "expires_at", "lifted_at", "lifted_by", "firewall_synced"} {
		if _, ok := items[0][k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	all := decode[[]map[string]any](t, e.call(t, http.MethodGet, "/security/jails?active=false", nil, callerIP))
	if len(all) != 3 {
		t.Fatalf("all jails = %v", all)
	}
	for _, j := range all {
		if j["id"] == float64(lifted) && (j["lifted_by"] != "admin" || j["firewall_synced"] != jsonTrue) {
			t.Fatalf("lifted jail = %v", j)
		}
	}
	wantStatus(t, e.call(t, http.MethodGet, "/security/jails?active=maybe", nil, callerIP), http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestCreateJail(t *testing.T) {
	e := newEnv(t)
	rec := e.call(t, http.MethodPost, "/security/jails",
		map[string]any{"ip": "2001:db8:1:2::5", "duration_minutes": 60, "reason": "scanner"}, callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	j := decode[map[string]any](t, rec)
	if j["ip_key"] != "2001:db8:1:2::/64" || j["manual"] != jsonTrue || j["level"] != float64(1) ||
		j["expires_at"] != float64(t0.Add(time.Hour).UnixMilli()) || j["reason"] != "scanner" {
		t.Fatalf("jail = %v", j)
	}
	if rec := e.filter("2001:db8:1:2::99", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("manual jail must block immediately, code=%d", rec.Code)
	}
	if !slices.Contains(e.audit.Actions(), audit.ActionJailCreate) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails",
		map[string]any{"ip": "2001:db8:1:2::6", "duration_minutes": 5, "reason": ""}, callerIP), http.StatusConflict, httpx.CodeConflict)
}

func TestCreateJailValidation(t *testing.T) {
	e := newEnv(t)
	cases := []any{
		map[string]any{"ip": "nope", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "203.0.113.0/24", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "127.0.0.1", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "::", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "203.0.113.7", "duration_minutes": 0, "reason": ""},
		map[string]any{"ip": "203.0.113.7", "duration_minutes": 10081, "reason": ""},
		map[string]any{"ip": "203.0.113.7", "duration_minutes": 5, "reason": strings.Repeat("x", 201)},
	}
	for i, body := range cases {
		rec := e.call(t, http.MethodPost, "/security/jails", body, callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", `{"ip":"203.0.113.7","duration_minutes":5,"reason":"","x":1}`, callerIP),
		http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestCreateJailRejectsOwnIPAndAllowlisted(t *testing.T) {
	e := newEnv(t)
	body := func(ip string) map[string]any { return map[string]any{"ip": ip, "duration_minutes": 5, "reason": ""} }
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", body(callerIP), callerIP), http.StatusConflict, httpx.CodeConflict)
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", body("2001:db8:9:9::2"), "2001:db8:9:9::1"), http.StatusConflict, httpx.CodeConflict)
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", body("203.0.113.7"), callerIP), http.StatusConflict, httpx.CodeConflict)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("no jail may be created: %+v", js)
	}
}

func TestLiftJail(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 5)
	js := e.activeJails(t)
	if len(js) != 1 {
		t.Fatalf("precondition: %+v", js)
	}
	path := fmt.Sprintf("/security/jails/%d", js[0].ID)
	wantStatus(t, e.call(t, http.MethodDelete, path, nil, callerIP), http.StatusNoContent, "")
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("lifted jail must stop blocking immediately, code=%d", rec.Code)
	}
	j, err := e.q.DefenseGetJail(t.Context(), js[0].ID)
	if err != nil || !j.LiftedAt.Valid || j.LiftedBy.String != "admin" {
		t.Fatalf("jail = %+v err=%v", j, err)
	}
	if !slices.Contains(e.audit.Actions(), audit.ActionJailLift) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	wantStatus(t, e.call(t, http.MethodDelete, path, nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
	wantStatus(t, e.call(t, http.MethodDelete, "/security/jails/999", nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
	wantStatus(t, e.call(t, http.MethodDelete, "/security/jails/abc", nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
}

func TestCreateJailFailsClosedWithoutCallerIP(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/security/jails",
		strings.NewReader(`{"ip":"203.0.113.7","duration_minutes":5,"reason":""}`))
	ctx := principal.With(req.Context(), principal.Principal{
		UserID: e.adminID, Username: "admin", Role: principal.RoleAdmin, Stage: principal.StageFull,
		SessionID: 1, SudoUntil: e.clk.Now().Add(5 * time.Minute),
	}) // no client IP in the context
	rec := httptest.NewRecorder()
	e.router().ServeHTTP(rec, req.WithContext(ctx))
	wantStatus(t, rec, http.StatusConflict, httpx.CodeConflict)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("no jail may be created: %+v", js)
	}
}
