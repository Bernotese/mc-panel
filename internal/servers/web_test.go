package servers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// webResp holds the web fields of the Server schema; raw keeps every key for absence checks.
type webResp struct {
	WebURL    *string `json:"web_url"`
	WebStatus string  `json:"web_status"`
	WebPort   *int    `json:"web_port"`
	WebError  *string `json:"web_error"`
}

func (e *env) webOf(t *testing.T, id int64, p *principal.Principal) (webResp, map[string]json.RawMessage) {
	t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, p)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d: %d %s", id, rec.Code, rec.Body)
	}
	return decode[webResp](t, rec), decode[map[string]json.RawMessage](t, rec)
}

func (e *env) setWebPort(t *testing.T, id int64, port any) webResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", id), map[string]any{"web_port": port}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch web_port %v: %d %s", port, rec.Code, rec.Body)
	}
	return decode[webResp](t, rec)
}

// running marks the container of id as up to date and running, so a pass has no instance action.
func (e *env) running(id int64, name string) {
	e.agent.set(container(name, id, 1, agentapi.StateRunning, ""))
}

func TestWebPortPatchRules(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	path := fmt.Sprintf("/servers/%d", id)
	admin := e.admin()
	before := instanceRow(t, e.db, id)

	uid := storetest.CreateUser(t, e.db, "alice", "user")
	grant(t, e.db, uid, id, allPermStrings...)
	alice := userP(uid, "alice")
	if rec := e.do(http.MethodPatch, path, map[string]any{"web_port": 8100}, &alice); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d %s", rec.Code, rec.Body)
	}
	noSudo := e.admin()
	noSudo.SudoUntil = t0.Add(-time.Second)
	if rec := e.do(http.MethodPatch, path, map[string]any{"web_port": 8100}, &noSudo); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("no sudo: %d %s", rec.Code, rec.Body)
	}

	// No base domain: setting a port is refused, turning it off is fine.
	rec := e.do(http.MethodPatch, path, map[string]any{"web_port": 8100}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" || decode[map[string]string](t, rec)["message"] != "base domain required" {
		t.Fatalf("no base domain: %d %s", rec.Code, rec.Body)
	}
	if got := e.setWebPort(t, id, nil); got.WebStatus != "off" || got.WebURL != nil {
		t.Fatalf("off without base domain: %+v", got)
	}

	setBaseDomain(t, e.db, "mc.example.com")
	for _, p := range []any{1023, 25565, 25575, 65536, 0, -1} {
		rec := e.do(http.MethodPatch, path, map[string]any{"web_port": p}, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Fatalf("web_port %v: %d %s", p, rec.Code, rec.Body)
		}
	}
	for _, p := range []any{"8100", 8100.5, true} {
		if rec := e.do(http.MethodPatch, path, map[string]any{"web_port": p}, &admin); rec.Code != http.StatusBadRequest {
			t.Fatalf("web_port %v: %d %s", p, rec.Code, rec.Body)
		}
	}
	// A bad web port refuses the whole request: the spec change is not applied either.
	if rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "web_port": 25565}, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed: %d %s", rec.Code, rec.Body)
	}
	if row := instanceRow(t, e.db, id); row.MemoryMib != 4096 || row.WebPort.Valid {
		t.Fatalf("row after refused requests = %+v", row)
	}
	if acts := e.rec.Actions(); len(acts) != 0 {
		t.Fatalf("audit after refused requests = %v", acts)
	}

	got := e.setWebPort(t, id, 8100)
	if got.WebStatus != "pending" || got.WebURL == nil || *got.WebURL != "https://survival.mc.example.com" || got.WebPort == nil || *got.WebPort != 8100 {
		t.Fatalf("published = %+v", got)
	}
	row := instanceRow(t, e.db, id)
	if !row.WebPort.Valid || row.WebPort.Int64 != 8100 || row.Revision != before.Revision || row.UpdatedAt != before.UpdatedAt {
		t.Fatalf("row = %+v (before %+v)", row, before)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if en.Action != audit.ActionServerWebPortUpdate || en.Target != fmt.Sprintf("server:%d", id) || en.Details["name"] != "survival" ||
		en.Details["old"] != nil || en.Details["new"] != 8100 || len(e.rec.Entries) != 1 {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}

	// The same value again: nothing written, no audit entry.
	e.setWebPort(t, id, 8100)
	if len(e.rec.Entries) != 1 {
		t.Fatalf("no-op audit = %v", e.rec.Actions())
	}

	// Together with a spec change: one entry each, the web port alone never bumps the revision.
	admin = e.admin()
	if rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "web_port": 8123}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("mixed: %d %s", rec.Code, rec.Body)
	}
	if row := instanceRow(t, e.db, id); row.Revision != before.Revision+1 || row.WebPort.Int64 != 8123 {
		t.Fatalf("mixed row = %+v", row)
	}
	if acts := e.rec.Actions(); !slices.Equal(acts, []string{audit.ActionServerWebPortUpdate, audit.ActionServerUpdate, audit.ActionServerWebPortUpdate}) {
		t.Fatalf("mixed audit = %v", acts)
	}

	if got := e.setWebPort(t, id, nil); got.WebStatus != "off" || got.WebURL != nil || got.WebPort != nil {
		t.Fatalf("unpublished = %+v", got)
	}
	en = e.rec.Entries[len(e.rec.Entries)-1]
	if en.Action != audit.ActionServerWebPortUpdate || en.Details["old"] != 8123 || en.Details["new"] != nil {
		t.Fatalf("unpublish audit = %+v", en.Details)
	}
	if instanceRow(t, e.db, id).WebPort.Valid {
		t.Fatal("web port still stored")
	}
}

func TestWebPortView(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	other := createInstance(t, e.db, "creative")
	e.running(id, "survival")
	e.running(other, "creative")
	admin := e.admin()
	uid := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, uid, id, "server.view")
	viewer := userP(uid, "viewer")

	if got, _ := e.webOf(t, id, &admin); got.WebStatus != "off" || got.WebURL != nil || got.WebPort != nil {
		t.Fatalf("before = %+v", got)
	}
	e.setWebPort(t, id, 8100)
	e.pass()

	got, _ := e.webOf(t, id, &admin)
	if got.WebStatus != "active" || got.WebURL == nil || *got.WebURL != "https://survival.mc.example.com" || *got.WebPort != 8100 || got.WebError != nil {
		t.Fatalf("admin = %+v", got)
	}
	got, raw := e.webOf(t, id, &viewer)
	if got.WebStatus != "active" || got.WebURL == nil || *got.WebURL != "https://survival.mc.example.com" {
		t.Fatalf("viewer = %+v", got)
	}
	if _, ok := raw["web_port"]; ok {
		t.Fatalf("viewer sees web_port: %v", raw)
	}
	if got, _ := e.webOf(t, other, &admin); got.WebStatus != "off" {
		t.Fatalf("other = %+v", got)
	}

	// An error of the agent: the reason for admins only.
	e.agent.setWebStatus(func(r agentapi.WebRoute) agentapi.WebRouteStatus {
		return agentapi.WebRouteStatus{ID: r.ID, Status: agentapi.WebError, Error: "server network missing"}
	})
	e.pass()
	if got, _ := e.webOf(t, id, &admin); got.WebStatus != "error" || got.WebError == nil || *got.WebError != "server network missing" {
		t.Fatalf("admin error = %+v", got)
	}
	got, raw = e.webOf(t, id, &viewer)
	if _, ok := raw["web_error"]; ok || got.WebStatus != "error" {
		t.Fatalf("viewer error = %s", raw["web_error"])
	}

	// The list endpoint carries the same fields.
	rec := e.do(http.MethodGet, "/servers", nil, &viewer)
	list := decode[[]map[string]json.RawMessage](t, rec)
	if len(list) != 1 || string(list[0]["web_status"]) != `"error"` || list[0]["web_url"] == nil || list[0]["web_port"] != nil || list[0]["web_error"] != nil {
		t.Fatalf("viewer list = %s", rec.Body)
	}
	rec = e.do(http.MethodGet, "/servers", nil, &admin)
	if list := decode[[]webResp](t, rec); len(list) != 2 || list[0].WebStatus != "off" || list[1].WebStatus != "error" || *list[1].WebPort != 8100 {
		t.Fatalf("admin list = %s", rec.Body)
	}
}

func TestWebRoutesReconcile(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	a := createInstance(t, e.db, "survival")
	b := createInstance(t, e.db, "creative")
	c := createInstance(t, e.db, "lobby")
	for id, name := range map[int64]string{a: "survival", b: "creative", c: "lobby"} {
		e.running(id, name)
	}
	admin := e.admin()

	// No web port anywhere: an empty list (never null), even when nothing else is to do.
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("instance calls = %v", calls)
	}
	reqs := e.agent.takeWeb()
	if len(reqs) != 1 || reqs[0].Routes == nil || len(reqs[0].Routes) != 0 {
		t.Fatalf("first pass = %+v", reqs)
	}

	e.setWebPort(t, c, 8123)
	e.setWebPort(t, a, 8100)
	e.pass()
	want := []agentapi.WebRoute{{ID: a, FQDN: "survival.mc.example.com", Port: 8100}, {ID: c, FQDN: "lobby.mc.example.com", Port: 8123}}
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.Equal(reqs[0].Routes, want) {
		t.Fatalf("routes = %+v, want %+v", reqs, want)
	}
	// Every pass sends the complete list again (the agent may have restarted).
	e.pass()
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.Equal(reqs[0].Routes, want) {
		t.Fatalf("second pass = %+v", reqs)
	}

	// Deleting a server drops its route.
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", c), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	e.pass()
	want = want[:1]
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.Equal(reqs[0].Routes, want) {
		t.Fatalf("after delete = %+v", reqs)
	}

	// A new base domain changes the fqdn; until the agent confirmed it the route is pending.
	setBaseDomain(t, e.db, "play.example.org")
	if got, _ := e.webOf(t, a, &admin); got.WebStatus != "pending" || *got.WebURL != "https://survival.play.example.org" {
		t.Fatalf("after base domain change = %+v", got)
	}
	e.pass()
	want = []agentapi.WebRoute{{ID: a, FQDN: "survival.play.example.org", Port: 8100}}
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.Equal(reqs[0].Routes, want) {
		t.Fatalf("after base domain change = %+v", reqs)
	}
	if got, _ := e.webOf(t, a, &admin); got.WebStatus != "active" {
		t.Fatalf("confirmed = %+v", got)
	}

	// Removing the base domain removes every route (review focus 5): empty list, status off.
	mustExec(t, e.db, `UPDATE instance_settings SET base_domain = NULL WHERE id = 1`)
	if got, _ := e.webOf(t, a, &admin); got.WebStatus != "off" || got.WebURL != nil {
		t.Fatalf("without base domain = %+v", got)
	}
	e.pass()
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || reqs[0].Routes == nil || len(reqs[0].Routes) != 0 {
		t.Fatalf("without base domain = %+v", reqs)
	}
	if got, _ := e.webOf(t, a, &admin); got.WebStatus != "off" || got.WebURL != nil || *got.WebPort != 8100 {
		t.Fatalf("without base domain after pass = %+v", got)
	}
}

func TestWebRoutesAgentErrors(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	e.running(id, "survival")
	admin := e.admin()
	e.setWebPort(t, id, 8100)

	for _, tc := range []struct {
		name  string
		setup func()
		want  string
	}{
		{"web call fails", func() { e.agent.setWebErr(agentapi.ErrUnavailable) }, "agent unavailable"},
		{"agent down", func() { e.agent.setErr(agentapi.ErrUnavailable) }, "agent unavailable"},
		{"docker down", func() {
			e.agent.setDocker(agentapi.DockerUnavailable)
			e.agent.setWebErr(agentapi.ErrDockerUnavailable)
		}, "docker unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			e.pass()
			// The list is still sent on the early return of a pass without agent or Docker.
			if reqs := e.agent.takeWeb(); len(reqs) != 1 || len(reqs[0].Routes) != 1 {
				t.Fatalf("sent = %+v", reqs)
			}
			got, _ := e.webOf(t, id, &admin)
			if got.WebStatus != "error" || got.WebError == nil || *got.WebError != tc.want {
				t.Fatalf("status = %+v", got)
			}
			e.agent.setErr(nil)
			e.agent.setWebErr(nil)
			e.agent.setDocker(agentapi.DockerOK)
			e.pass()
			e.agent.takeWeb()
			if got, _ := e.webOf(t, id, &admin); got.WebStatus != "active" || got.WebError != nil {
				t.Fatalf("recovered = %+v", got)
			}
		})
	}
}
