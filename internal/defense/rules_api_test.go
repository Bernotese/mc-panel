package defense

import (
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/store"
)

func ruleBody(cidr, kind string) map[string]any {
	return map[string]any{"cidr": cidr, "kind": kind, "comment": ""}
}

func TestCreateAndListIPRules(t *testing.T) {
	e := newEnv(t)
	rec := e.call(t, http.MethodPost, "/security/ip-rules",
		map[string]any{"cidr": "203.0.113.77/24", "kind": "deny", "comment": " bots ", "expires_at": nil}, callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	r := decode[map[string]any](t, rec)
	if r["cidr"] != "203.0.113.0/24" || r["kind"] != "deny" || r["comment"] != "bots" || r["created_by"] != "admin" ||
		r["expires_at"] != nil || r["created_at"] != float64(t0.UnixMilli()) {
		t.Fatalf("rule = %v", r)
	}
	rec = e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("2001:db8::1", "allow"), callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	if got := decode[map[string]any](t, rec)["cidr"]; got != "2001:db8::1/128" {
		t.Fatalf("bare IPv6 must become /128, got %v", got)
	}
	exp := t0.Add(time.Hour).UnixMilli()
	rec = e.call(t, http.MethodPost, "/security/ip-rules",
		map[string]any{"cidr": "192.0.2.0/24", "kind": "allow", "comment": "", "expires_at": exp}, callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	if got := decode[map[string]any](t, rec)["expires_at"]; got != float64(exp) {
		t.Fatalf("expires_at = %v", got)
	}
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.113.0/24", "deny"), callerIP),
		http.StatusConflict, httpx.CodeConflict)
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.113.0/24", "allow"), callerIP),
		http.StatusCreated, "")
	items := decode[[]map[string]any](t, e.call(t, http.MethodGet, "/security/ip-rules", nil, callerIP))
	if len(items) != 4 {
		t.Fatalf("rules = %v", items)
	}
	if n := strings.Count(strings.Join(e.audit.Actions(), ","), audit.ActionIPRuleCreate); n != 4 {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestCreateIPRuleValidation(t *testing.T) {
	e := newEnv(t)
	past := t0.Add(-time.Second).UnixMilli()
	cases := []map[string]any{
		ruleBody("nope", "deny"),
		ruleBody("::ffff:203.0.113.7/128", "deny"),
		ruleBody("fe80::1%en0", "deny"),
		ruleBody("203.0.113.0/33", "deny"),
		ruleBody("203.0.113.0/24", "block"),
		{"cidr": "203.0.113.0/24", "kind": "deny", "comment": strings.Repeat("x", 201)},
		{"cidr": "203.0.113.0/24", "kind": "deny", "comment": "", "expires_at": past},
	}
	for i, body := range cases {
		rec := e.call(t, http.MethodPost, "/security/ip-rules", body, callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

func TestCreatedDenyRuleBlocksImmediately(t *testing.T) {
	e := newEnv(t)
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.113.0/24", "deny"), callerIP), http.StatusCreated, "")
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeIPBlocked {
		t.Fatalf("code=%d err=%q", rec.Code, errCode(rec))
	}
}

func TestCreateIPRuleRejectsSelfLockout(t *testing.T) {
	e := newEnv(t)
	for _, cidr := range []string{"198.51.100.0/24", "0.0.0.0/0", callerIP} {
		wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody(cidr, "deny"), callerIP),
			http.StatusConflict, httpx.CodeConflict)
	}
	// an IPv6 deny does not affect an IPv4 caller
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("::/0", "deny"), callerIP), http.StatusCreated, "")
	// once the caller is allowlisted, a covering deny rule is fine
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody(callerIP, "allow"), callerIP), http.StatusCreated, "")
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("198.51.100.0/24", "deny"), callerIP), http.StatusCreated, "")
}

func TestDeleteIPRuleRejectsSelfLockout(t *testing.T) {
	e := newEnv(t)
	own := e.addRule(t, callerIP+"/32", "allow", nil)
	other := e.addRule(t, "203.0.113.0/24", "allow", nil)
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{}})
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", own), nil, callerIP),
		http.StatusConflict, httpx.CodeConflict)
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", other), nil, callerIP),
		http.StatusNoContent, "")
	if !slices.Contains(e.audit.Actions(), audit.ActionIPRuleDelete) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	wantStatus(t, e.call(t, http.MethodDelete, "/security/ip-rules/999", nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
	// from loopback the admin can always remove the rule
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", own), nil, "127.0.0.1"),
		http.StatusNoContent, "")
	if rules, _ := e.q.DefenseListIPRules(t.Context()); len(rules) != 0 {
		t.Fatalf("rules left: %+v", rules)
	}
}

func TestLockoutCheckFailsClosedWithoutCallerIP(t *testing.T) {
	e := newEnv(t)
	if r := e.svc.snap.Load().locksOut(netip.Addr{}, e.clk.Now(), e.geo); r == "" {
		t.Fatal("an unknown caller IP cannot be proven safe; locksOut must report a reason")
	}
}

func TestDeleteAllowRuleMaskingOwnJailIsRejected(t *testing.T) {
	e := newEnv(t)
	own := e.addRule(t, callerIP+"/32", "allow", nil)
	// a jail on the caller's key exists but is masked by the allow rule
	e.insertJail(t, callerIP+"/32", e.clk.Now(), e.clk.Now().Add(time.Hour))
	e.reload(t)
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", own), nil, callerIP),
		http.StatusConflict, httpx.CodeConflict)
}

func TestCreateAllowRuleRejectsOverlyBroadPrefixes(t *testing.T) {
	e := newEnv(t)
	for _, cidr := range []string{"0.0.0.0/0", "10.0.0.0/7", "::/0", "2000::/31"} {
		rec := e.call(t, http.MethodPost, "/security/ip-rules", ruleBody(cidr, "allow"), callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("%s: %d %s", cidr, rec.Code, rec.Body)
		}
	}
	for _, cidr := range []string{"10.0.0.0/8", "2001:db8::/32"} {
		wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody(cidr, "allow"), callerIP), http.StatusCreated, "")
	}
	// deny rules are not restricted by this limit
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.0.0/8", "deny"), callerIP), http.StatusCreated, "")
}
