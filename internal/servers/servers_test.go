package servers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type statusResp struct {
	State            string  `json:"state"`
	DesiredState     *string `json:"desired_state"`
	RestartRequired  bool    `json:"restart_required"`
	Operation        string  `json:"operation"`
	LastError        string  `json:"last_error"`
	StartedAt        int64   `json:"started_at"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUPercent       float64 `json:"cpu_percent"`
}

type instanceResp struct {
	Type          string         `json:"type"`
	Version       string         `json:"version"`
	LoaderVersion *string        `json:"loader_version"`
	Modpack       *string        `json:"modpack"`
	Java          string         `json:"java"`
	MemoryMiB     int            `json:"memory_mib"`
	Properties    map[string]any `json:"properties"`
}

type serverResp struct {
	ID          int64         `json:"id"`
	Name        string        `json:"name"`
	Permissions []string      `json:"permissions"`
	Address     *string       `json:"address"`
	Instance    *instanceResp `json:"instance"`
	Status      statusResp    `json:"status"`
}

var allPermStrings = []string{"server.view", "server.power", "server.console", "server.files", "server.plugins", "server.backups"}

// paper returns a valid CreateServerRequest body.
func paper(name string) map[string]any {
	return map[string]any{"name": name, "type": "paper", "version": "1.21.11", "memory_mib": 4096, "eula_accepted": true}
}

// with returns a copy of body with key set to val (nil deletes the key).
func with(body map[string]any, key string, val any) map[string]any {
	out := maps.Clone(body)
	if val == nil {
		delete(out, key)
	} else {
		out[key] = val
	}
	return out
}

func (e *env) create(t *testing.T, body map[string]any) serverResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", body, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %v: %d %s", body, rec.Code, rec.Body)
	}
	return decode[serverResp](t, rec)
}

func (e *env) get(t *testing.T, id int64) serverResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d: %d %s", id, rec.Code, rec.Body)
	}
	return decode[serverResp](t, rec)
}

func (e *env) refresh() { servers.RefreshStatus(context.Background(), e.h) }

func (e *env) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServerCRUDAsAdmin(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	if s.ID == 0 || s.Name != "survival" || !slices.Equal(s.Permissions, allPermStrings) {
		t.Fatalf("created = %+v", s)
	}
	if rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin); rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"-bad", "x-", "a", "Upper", "has_underscore", "with space", strings.Repeat("a", 33)} {
		rec := e.do(http.MethodPost, "/servers", paper(bad), &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("name %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	if len(list) != 1 || list[0].ID != s.ID || !slices.Equal(list[0].Permissions, allPermStrings) {
		t.Fatalf("list = %+v", list)
	}
	path := fmt.Sprintf("/servers/%d", s.ID)
	if got := e.get(t, s.ID); got.Name != "survival" {
		t.Fatalf("get = %+v", got)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rec.Code)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerCreate, audit.ActionServerDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if en := e.rec.Entries[0]; en.Target != fmt.Sprintf("server:%d", s.ID) || en.Details["name"] != "survival" {
		t.Fatalf("audit entry = %+v", en)
	}
}

func TestCreateServerStoresInstance(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	e.refresh() // agent reachable, no containers yet
	s := e.create(t, with(paper("survival"), "properties", map[string]any{"motd": "Hallo", "max_players": 8}))

	if s.Address == nil || *s.Address != "survival.mc.example.com" {
		t.Fatalf("address = %v", s.Address)
	}
	in := s.Instance
	if in == nil || in.Type != "paper" || in.Version != "1.21.11" || in.Java != "java21" || in.MemoryMiB != 4096 ||
		in.LoaderVersion != nil || in.Modpack != nil {
		t.Fatalf("instance = %+v", in)
	}
	// properties come back fully resolved
	p := in.Properties
	if pvp, _ := p["pvp"].(bool); p["motd"] != "Hallo" || p["max_players"] != float64(8) || p["difficulty"] != "normal" || !pvp ||
		p["view_distance"] != float64(10) || p["level_type"] != "normal" {
		t.Fatalf("properties = %v", p)
	}
	for _, k := range []string{"whitelist", "ops"} {
		if _, ok := p[k]; ok {
			t.Fatalf("properties must not contain %s since sub-project 3a: %v", k, p)
		}
	}
	if s.Status.State != "creating" || s.Status.DesiredState == nil || *s.Status.DesiredState != "running" || s.Status.RestartRequired {
		t.Fatalf("status = %+v", s.Status)
	}
	// the database keeps the properties as given, not resolved
	row := instanceRow(t, e.db, s.ID)
	if row.Properties != `{"motd":"Hallo","max_players":8}` || row.Revision != 1 || row.DesiredState != "running" {
		t.Fatalf("row = %+v", row)
	}
	en := e.rec.Entries[0]
	if en.Action != audit.ActionServerCreate || en.Details["type"] != "paper" || en.Details["java"] != "java21" ||
		en.Details["memory_mib"] != 4096 || en.Details["name"] != "survival" {
		t.Fatalf("audit = %+v", en)
	}
}

func TestCreateServerDerivesOrKeepsJava(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	if got := e.create(t, with(with(paper("old"), "type", "vanilla"), "version", "1.16.5")).Instance.Java; got != "java8" {
		t.Fatalf("1.16.5 -> %s, want java8", got)
	}
	if got := e.create(t, with(paper("latest"), "version", "LATEST")).Instance.Java; got != "java25" {
		t.Fatalf("LATEST -> %s, want java25", got)
	}
	if got := e.create(t, with(paper("pinned"), "java", "java25")).Instance.Java; got != "java25" {
		t.Fatalf("explicit java ignored: %s", got)
	}
}

// Review focus 3: no base domain -> 409 base_domain_missing, nothing written.
func TestCreateServerWithoutBaseDomain(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "base_domain_missing" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if n, m := e.count(t, "servers"), e.count(t, "instances"); n != 0 || m != 0 || len(e.rec.Entries) != 0 {
		t.Fatalf("rows servers=%d instances=%d audit=%v", n, m, e.rec.Actions())
	}
}

// Review focus 3: a taken name -> 409 conflict and no half-written instance row.
func TestCreateServerNameTakenLeavesNoHalfRow(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	e.create(t, paper("survival"))
	storetest.CreateServer(t, e.db, "lobby") // TP1 stub without instance
	for _, name := range []string{"survival", "lobby"} {
		rec := e.do(http.MethodPost, "/servers", with(paper(name), "type", "vanilla"), &admin)
		if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if n, m := e.count(t, "servers"), e.count(t, "instances"); n != 2 || m != 1 {
		t.Fatalf("servers=%d instances=%d, want 2 and 1", n, m)
	}
}

func TestCreateServerValidation(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	cases := map[string]struct {
		body map[string]any
		code string
	}{
		"eula false":          {with(paper("a1"), "eula_accepted", false), "validation_failed"},
		"eula missing":        {with(paper("a1"), "eula_accepted", nil), "validation_failed"},
		"unknown type":        {with(paper("a1"), "type", "bukkit"), "validation_failed"},
		"type missing":        {with(paper("a1"), "type", nil), "validation_failed"},
		"memory step":         {with(paper("a1"), "memory_mib", 4000), "validation_failed"},
		"memory missing":      {with(paper("a1"), "memory_mib", nil), "validation_failed"},
		"version with space":  {with(paper("a1"), "version", "1.21 4"), "validation_failed"},
		"version newline":     {with(paper("a1"), "version", "1.21\nEULA=FALSE"), "validation_failed"},
		"loader on paper":     {with(paper("a1"), "loader_version", "0.19.5"), "validation_failed"},
		"modpack on paper":    {with(paper("a1"), "modpack", "atm10"), "validation_failed"},
		"java unknown":        {with(paper("a1"), "java", "java11"), "validation_failed"},
		"motd newline":        {with(paper("a1"), "properties", map[string]any{"motd": "a\nENABLE_RCON=false"}), "validation_failed"},
		"unknown property":    {with(paper("a1"), "properties", map[string]any{"jvm_opts": "-Xmx99G"}), "validation_failed"},
		"property wrong type": {with(paper("a1"), "properties", map[string]any{"max_players": "20"}), "validation_failed"},
		"curseforge no key":   {map[string]any{"name": "a1", "type": "curseforge", "version": "", "modpack": "atm10", "memory_mib": 8192, "eula_accepted": true}, "validation_failed"},
		"unknown field":       {with(paper("a1"), "privileged", true), "bad_request"},
	}
	for name, tc := range cases {
		rec := e.do(http.MethodPost, "/servers", tc.body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want 400 %s", name, rec.Code, rec.Body, tc.code)
		}
	}
	if n := e.count(t, "servers"); n != 0 {
		t.Fatalf("invalid requests created %d servers", n)
	}
}

func TestCreateCurseForgeUsesKeyFromSettings(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	const key = "$2a$10$abcdefghijklmnopqrstuv"
	e.putSettings(t, `{"curseforge_api_key":"`+key+`"}`)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", map[string]any{
		"name": "atm", "type": "curseforge", "version": "", "modpack": "all-the-mods-10", "memory_mib": 8192, "eula_accepted": true,
	}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), key) {
		t.Fatal("response leaks the CurseForge key")
	}
	s := decode[serverResp](t, rec)
	if s.Instance.Java != "java21" || s.Instance.Modpack == nil || *s.Instance.Modpack != "all-the-mods-10" {
		t.Fatalf("instance = %+v", s.Instance)
	}
	raw, _ := json.Marshal(e.rec.Entries)
	if strings.Contains(string(raw), key) {
		t.Fatalf("audit leaks the key: %s", raw)
	}
}

func TestGetServerShowsActualState(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	stub := storetest.CreateServer(t, e.db, "lobby")
	e.agent.set(agentapi.InstanceStatus{
		ID: id, Name: "survival", Revision: 1, State: agentapi.StateRunning, StartedAt: t0.UnixMilli(),
		MemoryBytes: 2 << 30, MemoryLimitBytes: 5 << 30, CPUPercent: 12.5,
	})
	e.refresh()
	got := e.get(t, id).Status
	if got.State != "running" || got.StartedAt != t0.UnixMilli() || got.MemoryBytes != 2<<30 ||
		got.MemoryLimitBytes != 5<<30 || got.CPUPercent != 12.5 || got.RestartRequired {
		t.Fatalf("status = %+v", got)
	}
	ns := e.get(t, stub)
	if ns.Status.State != "not_configured" || ns.Status.DesiredState != nil || ns.Instance != nil || ns.Address != nil {
		t.Fatalf("stub = %+v", ns)
	}

	// agent down: the desired state is still shown, the actual state is unknown
	e.agent.setErr(agentapi.ErrUnavailable)
	e.refresh()
	got = e.get(t, id).Status
	if got.State != "unknown" || got.DesiredState == nil || *got.DesiredState != "running" {
		t.Fatalf("agent down: %+v", got)
	}
	// a snapshot older than a minute counts as unknown as well
	e.agent.setErr(nil)
	e.refresh()
	e.clk.Advance(2 * time.Minute)
	if got := e.get(t, id).Status.State; got != "unknown" {
		t.Fatalf("stale snapshot: %s", got)
	}
}

func TestUpdateServer(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, with(paper("survival"), "properties", map[string]any{"max_players": 8}))
	e.agent.set(agentapi.InstanceStatus{ID: s.ID, Name: "survival", Revision: 1, State: agentapi.StateRunning})
	e.refresh()
	path := fmt.Sprintf("/servers/%d", s.ID)

	rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "properties": map[string]any{"motd": "Neu"}}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	got := decode[serverResp](t, rec)
	// properties replace the whole object: max_players falls back to its default
	if got.Instance.MemoryMiB != 6144 || got.Instance.Properties["motd"] != "Neu" || got.Instance.Properties["max_players"] != float64(20) {
		t.Fatalf("instance = %+v", got.Instance)
	}
	if !got.Status.RestartRequired || got.Status.State != "running" {
		t.Fatalf("running container with old revision must need a restart: %+v", got.Status)
	}
	if row := instanceRow(t, e.db, s.ID); row.Revision != 2 || row.Properties != `{"motd":"Neu"}` {
		t.Fatalf("row = %+v", row)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	changes, _ := en.Details["changes"].(map[string]any)
	mem, _ := changes["memory_mib"].(map[string]any)
	motd, _ := changes["properties.motd"].(map[string]any)
	if en.Action != audit.ActionServerUpdate || mem["old"] != 4096 || mem["new"] != 6144 || motd["old"] != nil ||
		string(motd["new"].(json.RawMessage)) != `"Neu"` || changes["properties.max_players"] == nil || len(changes) != 3 {
		t.Fatalf("audit = %+v", en.Details)
	}

	// unchanged values: no new revision, no audit entry
	before := len(e.rec.Entries)
	rec = e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "loader_version": nil}, &admin)
	if rec.Code != http.StatusOK || instanceRow(t, e.db, s.ID).Revision != 2 || len(e.rec.Entries) != before {
		t.Fatalf("no-op patch: %d, revision %d, audit %v", rec.Code, instanceRow(t, e.db, s.ID).Revision, e.rec.Actions())
	}

	// a stopped container takes the new revision on the next reconcile: no restart needed
	e.agent.set(agentapi.InstanceStatus{ID: s.ID, Name: "survival", Revision: 1, State: agentapi.StateExited})
	e.refresh()
	if st := e.get(t, s.ID).Status; st.RestartRequired || st.State != "stopped" {
		t.Fatalf("stopped: %+v", st)
	}
}

func TestUpdateServerRejects(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	stub := storetest.CreateServer(t, e.db, "lobby")
	path := fmt.Sprintf("/servers/%d", s.ID)
	for name, tc := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"version null":       {map[string]any{"version": nil}, 400, "validation_failed"},
		"memory null":        {map[string]any{"memory_mib": nil}, 400, "validation_failed"},
		"properties null":    {map[string]any{"properties": nil}, 400, "validation_failed"},
		"memory invalid":     {map[string]any{"memory_mib": 512}, 400, "validation_failed"},
		"loader on paper":    {map[string]any{"loader_version": "1"}, 400, "validation_failed"},
		"unknown property":   {map[string]any{"properties": map[string]any{"jvm_opts": "x"}}, 400, "validation_failed"},
		"ops property":       {map[string]any{"properties": map[string]any{"ops": []string{"Notch"}}}, 400, "validation_failed"},
		"whitelist property": {map[string]any{"properties": map[string]any{"whitelist": []string{"Notch"}}}, 400, "validation_failed"},
		"type not allowed":   {map[string]any{"type": "vanilla"}, 400, "bad_request"},
		"name not allowed":   {map[string]any{"name": "other"}, 400, "bad_request"},
	} {
		rec := e.do(http.MethodPatch, path, tc.body, &admin)
		if rec.Code != tc.status || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
	if rev := instanceRow(t, e.db, s.ID).Revision; rev != 1 {
		t.Fatalf("rejected patches changed the revision to %d", rev)
	}
	rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", stub), map[string]any{"memory_mib": 2048}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("patch stub: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPatch, "/servers/9999", map[string]any{"memory_mib": 2048}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("patch unknown: %d", rec.Code)
	}
}

func TestDeleteServerRemovesContainerFirst(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	path := fmt.Sprintf("/servers/%d", s.ID)

	e.agent.setErr(agentapi.ErrUnavailable)
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "agent_unavailable" {
		t.Fatalf("agent down: %d %s", rec.Code, rec.Body)
	}
	e.agent.setErr(nil)
	e.agent.setActionErr(agentapi.ErrDockerUnavailable)
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "docker_unavailable" {
		t.Fatalf("docker down: %d %s", rec.Code, rec.Body)
	}
	if n := e.count(t, "instances"); n != 1 {
		t.Fatalf("failed deletes removed rows (instances=%d)", n)
	}
	e.agent.setActionErr(nil)
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("remove %d", s.ID)}) {
		t.Fatalf("agent calls = %v", calls)
	}
	if n, m := e.count(t, "servers"), e.count(t, "instances"); n != 0 || m != 0 {
		t.Fatalf("rows left: servers=%d instances=%d", n, m)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if configured, _ := en.Details["configured"].(bool); en.Action != audit.ActionServerDelete || !configured {
		t.Fatalf("audit = %+v", en)
	}

	// a server without instance needs no agent
	stub := storetest.CreateServer(t, e.db, "lobby")
	e.agent.setErr(agentapi.ErrUnavailable)
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", stub), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete stub without agent: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("stub delete called the agent: %v", calls)
	}
}

// Spec §12.4: a restricted user with server.power on server A sees only A; B → 403.
func TestServerVisibilityForRestrictedUser(t *testing.T) {
	e := newEnv(t)
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	grant(t, e.db, alice, a, "server.view", "server.power")
	ap := userP(alice, "alice")

	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &ap))
	want := []string{"server.view", "server.power"}
	if len(list) != 1 || list[0].ID != a || list[0].Name != "alpha" || !slices.Equal(list[0].Permissions, want) {
		t.Fatalf("list = %+v", list)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", a), nil, &ap)
	if rec.Code != http.StatusOK || !slices.Equal(decode[serverResp](t, rec).Permissions, want) {
		t.Fatalf("get A: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/servers/%d", b), nil},
		{http.MethodPost, fmt.Sprintf("/servers/%d/power", b), map[string]string{"action": "start"}},
		{http.MethodPost, "/servers", paper("gamma")},
		{http.MethodPatch, fmt.Sprintf("/servers/%d", a), map[string]any{"memory_mib": 2048}},
		{http.MethodDelete, fmt.Sprintf("/servers/%d", a), nil},
		{http.MethodGet, "/servers/settings", nil},
	} {
		if rec := e.do(tc.method, tc.path, tc.body, &ap); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &ap)
	if rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != `{"status":"accepted"}` {
		t.Fatalf("power A: %d %s", rec.Code, rec.Body)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerPower || last.Target != fmt.Sprintf("server:%d", a) ||
		last.Details["action"] != "start" || last.Result != audit.ResultSuccess {
		t.Fatalf("power audit = %+v", last)
	}
	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "explode"}, &ap)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("invalid action: %d %s", rec.Code, rec.Body)
	}
}

func TestServerListWithoutPermissionsIsEmptyArray(t *testing.T) {
	e := newEnv(t)
	storetest.CreateServer(t, e.db, "alpha")
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	rec := e.do(http.MethodGet, "/servers", nil, &bob)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
}

func TestServerPowerUnknownServerAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodPost, "/servers/9999/power", map[string]string{"action": "stop"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("power: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/servers/9999", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get: %d", rec.Code)
	}
}

// If the row vanishes between the lookup and the delete (concurrent delete), the handler must
// answer 404 and not audit a deletion that did not happen. A BEFORE DELETE trigger with
// RAISE(IGNORE) makes the DELETE affect 0 rows deterministically.
func TestDeleteServerZeroRowsIs404(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := storetest.CreateServer(t, e.db, "alpha")
	mustExec(t, e.db, `CREATE TRIGGER servers_ignore_delete BEFORE DELETE ON servers BEGIN SELECT RAISE(IGNORE); END`)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if got := e.rec.Actions(); slices.Contains(got, audit.ActionServerDelete) {
		t.Fatalf("unexpected audit entries: %v", got)
	}
}

// SEC-28: the world seed is shown only to admins and to users with server.files (they can read
// server.properties anyway); server.view alone does not reveal it, in the list and the detail.
func TestSeedOnlyForAdminsAndFilesUsers(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "alpha")
	mustExec(t, e.db, `UPDATE instances SET properties = ? WHERE server_id = ?`, `{"seed":"8675309"}`, id)
	viewer := userP(storetest.CreateUser(t, e.db, "viewer", "user"), "viewer")
	grant(t, e.db, viewer.UserID, id, "server.view", "server.power", "server.console", "server.plugins", "server.backups")
	filer := userP(storetest.CreateUser(t, e.db, "filer", "user"), "filer")
	grant(t, e.db, filer.UserID, id, "server.view", "server.files")
	admin := e.admin()
	for _, tc := range []struct {
		name string
		p    principal.Principal
		want any // nil = no seed key
	}{
		{"admin", admin, "8675309"},
		{"server.files", filer, "8675309"},
		{"server.view", viewer, nil},
	} {
		list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &tc.p))
		one := decode[serverResp](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &tc.p))
		if len(list) != 1 {
			t.Fatalf("%s: list = %+v", tc.name, list)
		}
		for where, s := range map[string]serverResp{"list": list[0], "get": one} {
			if s.Instance == nil {
				t.Fatalf("%s %s: no instance", tc.name, where)
			}
			seed, ok := s.Instance.Properties["seed"]
			if (tc.want == nil && ok) || (tc.want != nil && seed != tc.want) {
				t.Errorf("%s %s: seed = %v (present %v), want %v", tc.name, where, seed, ok, tc.want)
			}
			if s.Instance.Properties["level_type"] != "normal" {
				t.Errorf("%s %s: other properties changed: %v", tc.name, where, s.Instance.Properties)
			}
		}
	}
}
