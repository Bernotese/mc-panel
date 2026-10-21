package servers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// webRouteResp is one entry of web_routes.
type webRouteResp struct {
	Path string  `json:"path"`
	Port int     `json:"port"`
	URL  *string `json:"url"`
}

// webResp holds the web fields of the Server schema; raw keeps every key for absence checks.
type webResp struct {
	WebURL    *string         `json:"web_url"`
	WebStatus string          `json:"web_status"`
	WebRoutes *[]webRouteResp `json:"web_routes"`
	WebError  *string         `json:"web_error"`
}

// routes returns the web_routes of r as "path=port url" strings ("-" without url).
func (r webResp) routes() []string {
	if r.WebRoutes == nil {
		return nil
	}
	out := []string{}
	for _, x := range *r.WebRoutes {
		u := "-"
		if x.URL != nil {
			u = *x.URL
		}
		out = append(out, fmt.Sprintf("%s=%d %s", x.Path, x.Port, u))
	}
	return out
}

func (e *env) webOf(t *testing.T, id int64, p *principal.Principal) (webResp, map[string]json.RawMessage) {
	t.Helper()
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, p)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d: %d %s", id, rec.Code, rec.Body)
	}
	return decode[webResp](t, rec), decode[map[string]json.RawMessage](t, rec)
}

// tg is a shorthand for one web target.
func tg(path string, port int) agentapi.WebTarget { return agentapi.WebTarget{Path: path, Port: port} }

func (e *env) setWebRoutes(t *testing.T, id int64, routes ...agentapi.WebTarget) webResp {
	t.Helper()
	admin := e.admin()
	if routes == nil {
		routes = []agentapi.WebTarget{}
	}
	rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", id), map[string]any{"web_routes": routes}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch web_routes %v: %d %s", routes, rec.Code, rec.Body)
	}
	return decode[webResp](t, rec)
}

// storedRoutes reads the web_routes rows of id.
func storedRoutes(t *testing.T, e *env, id int64) []store.WebRoute {
	t.Helper()
	rows, err := store.New(e.db).ServersListServerWebRoutes(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// jsonOf marshals an audit detail value for comparison.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// running marks the container of id as up to date and running, so a pass has no instance action.
func (e *env) running(id int64, name string) {
	e.agent.set(container(name, id, 1, agentapi.StateRunning, ""))
}

func TestWebRoutesPatchRules(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	path := fmt.Sprintf("/servers/%d", id)
	admin := e.admin()
	before := instanceRow(t, e.db, id)
	root := []agentapi.WebTarget{tg("", 8100)}

	uid := storetest.CreateUser(t, e.db, "alice", "user")
	grant(t, e.db, uid, id, allPermStrings...)
	alice := userP(uid, "alice")
	if rec := e.do(http.MethodPatch, path, map[string]any{"web_routes": root}, &alice); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d %s", rec.Code, rec.Body)
	}
	noSudo := e.admin()
	noSudo.SudoUntil = t0.Add(-time.Second)
	if rec := e.do(http.MethodPatch, path, map[string]any{"web_routes": root}, &noSudo); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("no sudo: %d %s", rec.Code, rec.Body)
	}

	// No base domain: publishing is refused, turning it off is fine.
	rec := e.do(http.MethodPatch, path, map[string]any{"web_routes": root}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" || decode[map[string]string](t, rec)["message"] != "base domain required" {
		t.Fatalf("no base domain: %d %s", rec.Code, rec.Body)
	}
	if got := e.setWebRoutes(t, id); got.WebStatus != "off" || got.WebURL != nil || got.WebRoutes == nil || len(*got.WebRoutes) != 0 {
		t.Fatalf("off without base domain: %+v", got)
	}

	setBaseDomain(t, e.db, "mc.example.com")
	nine := make([]agentapi.WebTarget, 0, 9)
	for i := range 9 {
		nine = append(nine, tg(fmt.Sprintf("p%d", i), 8100+i))
	}
	for name, body := range map[string]any{
		"null":           nil,
		"nine routes":    nine,
		"uppercase path": []agentapi.WebTarget{tg("Afk", 8200)},
		"dash first":     []agentapi.WebTarget{tg("-a", 8200)},
		"dash last":      []agentapi.WebTarget{tg("a-", 8200)},
		"slash":          []agentapi.WebTarget{tg("a/b", 8200)},
		"dot":            []agentapi.WebTarget{tg("a.b", 8200)},
		"too long":       []agentapi.WebTarget{tg(strings.Repeat("a", 33), 8200)},
		"minecraft port": []agentapi.WebTarget{tg("", 25565)},
		"rcon port":      []agentapi.WebTarget{tg("", 25575)},
		"low port":       []agentapi.WebTarget{tg("", 1023)},
		"duplicate port": []agentapi.WebTarget{tg("", 8100), tg("afk-smoke", 8100)},
		"two roots":      []agentapi.WebTarget{tg("", 8100), tg("", 8200)},
		"duplicate path": []agentapi.WebTarget{tg("map", 8100), tg("map", 8200)},
	} {
		rec := e.do(http.MethodPatch, path, map[string]any{"web_routes": body}, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	for _, body := range []any{8100, "x", []any{map[string]any{"path": "", "port": "8100"}}, []any{map[string]any{"path": "", "port": 8100, "url": "x"}}} {
		if rec := e.do(http.MethodPatch, path, map[string]any{"web_routes": body}, &admin); rec.Code != http.StatusBadRequest {
			t.Errorf("web_routes %v: %d %s", body, rec.Code, rec.Body)
		}
	}
	// Bad routes refuse the whole request: the spec change is not applied either.
	if rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "web_routes": []agentapi.WebTarget{tg("", 25565)}}, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed: %d %s", rec.Code, rec.Body)
	}
	if row := instanceRow(t, e.db, id); row.MemoryMib != 4096 || len(storedRoutes(t, e, id)) != 0 {
		t.Fatalf("after refused requests: row %+v, routes %+v", row, storedRoutes(t, e, id))
	}
	if acts := e.rec.Actions(); len(acts) != 0 {
		t.Fatalf("audit after refused requests = %v", acts)
	}

	// An unsorted list is accepted and stored sorted, root first (review focus 4).
	got := e.setWebRoutes(t, id, tg("afk-smoke", 8200), tg("", 8100))
	wantRoutes := []string{"=8100 https://survival.mc.example.com/", "afk-smoke=8200 https://survival.mc.example.com/afk-smoke/"}
	if got.WebStatus != "pending" || got.WebURL == nil || *got.WebURL != "https://survival.mc.example.com" || !slices.Equal(got.routes(), wantRoutes) {
		t.Fatalf("published = %+v %v", got, got.routes())
	}
	if rows := storedRoutes(t, e, id); !slices.Equal(rows, []store.WebRoute{{ServerID: id, Path: "", Port: 8100}, {ServerID: id, Path: "afk-smoke", Port: 8200}}) {
		t.Fatalf("stored = %+v", rows)
	}
	if row := instanceRow(t, e.db, id); row.Revision != before.Revision || row.UpdatedAt != before.UpdatedAt {
		t.Fatalf("row = %+v (before %+v)", row, before)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if en.Action != audit.ActionServerWebRoutesUpdate || en.Target != fmt.Sprintf("server:%d", id) || en.Details["name"] != "survival" ||
		jsonOf(t, en.Details["old"]) != `[]` || jsonOf(t, en.Details["new"]) != `[{"path":"","port":8100},{"path":"afk-smoke","port":8200}]` || len(e.rec.Entries) != 1 {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}

	// The same list again, in either order: nothing written, no audit entry.
	e.setWebRoutes(t, id, tg("", 8100), tg("afk-smoke", 8200))
	e.setWebRoutes(t, id, tg("afk-smoke", 8200), tg("", 8100))
	if len(e.rec.Entries) != 1 {
		t.Fatalf("no-op audit = %v", e.rec.Actions())
	}

	// Together with a spec change: one entry each, the routes alone never bump the revision.
	admin = e.admin()
	if rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "web_routes": []agentapi.WebTarget{tg("afk-smoke", 8200)}}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("mixed: %d %s", rec.Code, rec.Body)
	} else if got := decode[webResp](t, rec); got.WebURL != nil || !slices.Equal(got.routes(), wantRoutes[1:]) || got.WebStatus != "pending" {
		t.Fatalf("no root = %+v %v", got, got.routes())
	}
	if row := instanceRow(t, e.db, id); row.Revision != before.Revision+1 {
		t.Fatalf("mixed row = %+v", row)
	}
	if acts := e.rec.Actions(); !slices.Equal(acts, []string{audit.ActionServerWebRoutesUpdate, audit.ActionServerUpdate, audit.ActionServerWebRoutesUpdate}) {
		t.Fatalf("mixed audit = %v", acts)
	}
	// A request without web_routes keeps them and still shows them.
	admin = e.admin()
	if rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 4096}, &admin); rec.Code != http.StatusOK || !slices.Equal(decode[webResp](t, rec).routes(), wantRoutes[1:]) {
		t.Fatalf("without web_routes: %d %s", rec.Code, rec.Body)
	}

	got = e.setWebRoutes(t, id)
	if got.WebStatus != "off" || got.WebURL != nil || got.WebRoutes == nil || len(*got.WebRoutes) != 0 {
		t.Fatalf("unpublished = %+v", got)
	}
	en = e.rec.Entries[len(e.rec.Entries)-1]
	if en.Action != audit.ActionServerWebRoutesUpdate || jsonOf(t, en.Details["old"]) != `[{"path":"afk-smoke","port":8200}]` || jsonOf(t, en.Details["new"]) != `[]` {
		t.Fatalf("unpublish audit = %+v", en.Details)
	}
	if rows := storedRoutes(t, e, id); len(rows) != 0 {
		t.Fatalf("routes still stored: %+v", rows)
	}
}

func TestWebRoutesView(t *testing.T) {
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

	got, raw := e.webOf(t, id, &admin)
	if got.WebStatus != "off" || got.WebURL != nil || string(raw["web_routes"]) != `[]` {
		t.Fatalf("before = %+v %s", got, raw["web_routes"])
	}
	e.setWebRoutes(t, id, tg("", 8100), tg("afk-smoke", 8200))
	e.pass()

	wantRoutes := []string{"=8100 https://survival.mc.example.com/", "afk-smoke=8200 https://survival.mc.example.com/afk-smoke/"}
	got, _ = e.webOf(t, id, &admin)
	if got.WebStatus != "active" || got.WebURL == nil || *got.WebURL != "https://survival.mc.example.com" || !slices.Equal(got.routes(), wantRoutes) || got.WebError != nil {
		t.Fatalf("admin = %+v %v", got, got.routes())
	}
	got, raw = e.webOf(t, id, &viewer)
	if got.WebStatus != "active" || got.WebURL == nil || *got.WebURL != "https://survival.mc.example.com" {
		t.Fatalf("viewer = %+v", got)
	}
	for _, k := range []string{"web_routes", "web_port", "web_error"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("viewer sees %s: %s", k, raw[k])
		}
	}
	if got, _ := e.webOf(t, other, &admin); got.WebStatus != "off" {
		t.Fatalf("other = %+v", got)
	}

	// The agent's status counts only for exactly the stored list: a changed port is pending
	// until the agent confirmed it.
	e.setWebRoutes(t, id, tg("", 8101), tg("afk-smoke", 8200))
	if got, _ := e.webOf(t, id, &admin); got.WebStatus != "pending" {
		t.Fatalf("after port change = %+v", got)
	}
	e.pass()
	if got, _ := e.webOf(t, id, &admin); got.WebStatus != "active" {
		t.Fatalf("confirmed = %+v", got)
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
	if len(list) != 1 || string(list[0]["web_status"]) != `"error"` || list[0]["web_url"] == nil || list[0]["web_routes"] != nil || list[0]["web_error"] != nil {
		t.Fatalf("viewer list = %s", rec.Body)
	}
	rec = e.do(http.MethodGet, "/servers", nil, &admin)
	if l := decode[[]webResp](t, rec); len(l) != 2 || l[0].WebStatus != "off" || l[0].WebRoutes == nil || len(*l[0].WebRoutes) != 0 ||
		l[1].WebStatus != "error" || len(l[1].routes()) != 2 {
		t.Fatalf("admin list = %s", rec.Body)
	}
	e.agent.setWebStatus(nil)

	// No root (review focus 1): no web_url, the routes stay listed and the status is the agent's.
	e.setWebRoutes(t, id, tg("afk-smoke", 8200))
	e.pass()
	got, _ = e.webOf(t, id, &admin)
	if got.WebStatus != "active" || got.WebURL != nil || !slices.Equal(got.routes(), wantRoutes[1:]) {
		t.Fatalf("no root = %+v %v", got, got.routes())
	}
	if got, raw := e.webOf(t, id, &viewer); got.WebStatus != "active" || raw["web_url"] != nil || raw["web_routes"] != nil {
		t.Fatalf("viewer no root = %+v", raw)
	}

	// No base domain: off, the stored routes without url.
	mustExec(t, e.db, `UPDATE instance_settings SET base_domain = NULL WHERE id = 1`)
	got, _ = e.webOf(t, id, &admin)
	if got.WebStatus != "off" || got.WebURL != nil || !slices.Equal(got.routes(), []string{"afk-smoke=8200 -"}) {
		t.Fatalf("no base domain = %+v %v", got, got.routes())
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

	// No web route anywhere: an empty list (never null), even when nothing else is to do.
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("instance calls = %v", calls)
	}
	reqs := e.agent.takeWeb()
	if len(reqs) != 1 || reqs[0].Routes == nil || len(reqs[0].Routes) != 0 {
		t.Fatalf("first pass = %+v", reqs)
	}

	// One route per server with all its targets, sorted by path; a server without root too.
	e.setWebRoutes(t, c, tg("map", 8123))
	e.setWebRoutes(t, a, tg("afk-smoke", 8200), tg("", 8100), tg("b", 8300))
	e.pass()
	want := []agentapi.WebRoute{
		{ID: a, FQDN: "survival.mc.example.com", Targets: []agentapi.WebTarget{tg("", 8100), tg("afk-smoke", 8200), tg("b", 8300)}},
		{ID: c, FQDN: "lobby.mc.example.com", Targets: []agentapi.WebTarget{tg("map", 8123)}},
	}
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.EqualFunc(reqs[0].Routes, want, agentapi.WebRoute.Equal) {
		t.Fatalf("routes = %+v, want %+v", reqs, want)
	}
	// Every pass sends the complete list again (the agent may have restarted).
	e.pass()
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.EqualFunc(reqs[0].Routes, want, agentapi.WebRoute.Equal) {
		t.Fatalf("second pass = %+v", reqs)
	}

	// Deleting a server drops its route.
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", c), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	e.pass()
	want = want[:1]
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.EqualFunc(reqs[0].Routes, want, agentapi.WebRoute.Equal) {
		t.Fatalf("after delete = %+v", reqs)
	}

	// A new base domain changes the fqdn; until the agent confirmed it the route is pending.
	setBaseDomain(t, e.db, "play.example.org")
	if got, _ := e.webOf(t, a, &admin); got.WebStatus != "pending" || *got.WebURL != "https://survival.play.example.org" {
		t.Fatalf("after base domain change = %+v", got)
	}
	e.pass()
	want = []agentapi.WebRoute{{ID: a, FQDN: "survival.play.example.org", Targets: want[0].Targets}}
	if reqs := e.agent.takeWeb(); len(reqs) != 1 || !slices.EqualFunc(reqs[0].Routes, want, agentapi.WebRoute.Equal) {
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
	if got, _ := e.webOf(t, a, &admin); got.WebStatus != "off" || got.WebURL != nil || len(got.routes()) != 3 {
		t.Fatalf("without base domain after pass = %+v", got)
	}
}

func TestWebRoutesAgentErrors(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	e.running(id, "survival")
	admin := e.admin()
	e.setWebRoutes(t, id, tg("", 8100), tg("afk-smoke", 8200))

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
