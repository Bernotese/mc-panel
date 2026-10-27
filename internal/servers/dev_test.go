package servers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func ptr[T any](v T) *T { return &v }

func TestApplyDevOverrides(t *testing.T) {
	ov := servers.DevOverrides{ViewDistance: 4, SimulationDistance: 3}
	p := instancespec.Properties{Gamemode: ptr("adventure"), ViewDistance: ptr(12), WhitelistEnabled: ptr(true)}
	got := servers.ApplyDevOverrides("survival", p, ov)
	if *got.MOTD != "[DEV] survival" {
		t.Fatalf("motd = %q", *got.MOTD)
	}
	if *got.ViewDistance != 4 || *got.SimulationDistance != 3 {
		t.Fatalf("distances = %d/%d", *got.ViewDistance, *got.SimulationDistance)
	}
	if *got.Gamemode != "adventure" || !*got.WhitelistEnabled {
		t.Fatalf("prod's properties not kept: %+v", got)
	}
	if *p.ViewDistance != 12 || p.MOTD != nil {
		t.Fatalf("input modified: %+v", p)
	}
	long := strings.Repeat("ä", 150)
	got = servers.ApplyDevOverrides("survival", instancespec.Properties{MOTD: &long}, ov)
	if n := len([]rune(*got.MOTD)); n != 150 || !strings.HasPrefix(*got.MOTD, "[DEV] ä") {
		t.Fatalf("long motd: %d runes, %q", n, *got.MOTD)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("overridden properties invalid: %v", err)
	}
}

func TestParseDevOverrides(t *testing.T) {
	for raw, want := range map[string]servers.DevOverrides{
		`{}`:                  servers.DefaultDevOverrides(),
		`{"view_distance":8}`: {ViewDistance: 8, SimulationDistance: 3},
		`{"view_distance":5,"simulation_distance":6}`:   {ViewDistance: 5, SimulationDistance: 6},
		`{"simulation_distance":10,"view_distance":32}`: {ViewDistance: 32, SimulationDistance: 10},
	} {
		got, err := servers.ParseDevOverrides(raw)
		if err != nil || got != want {
			t.Errorf("%s = %+v, %v; want %+v", raw, got, err, want)
		}
	}
	if _, err := servers.ParseDevOverrides(`nope`); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestDevMemoryMiB(t *testing.T) {
	for _, tc := range []struct {
		typ  instancespec.Type
		max  int
		want int
	}{
		{instancespec.TypePaper, 32768, 2048},
		{instancespec.TypeVanilla, 32768, 2048},
		{instancespec.TypeFabric, 32768, 3072},
		{instancespec.TypeModrinth, 32768, 3072},
		{instancespec.TypePaper, 1536, 1536},
	} {
		if got := servers.DevMemoryMiB(tc.typ, tc.max); got != tc.want {
			t.Errorf("DevMemoryMiB(%s, %d) = %d, want %d", tc.typ, tc.max, got, tc.want)
		}
	}
}

// devEnv is an env with a base domain and a configured prod "survival".
func devEnv(t *testing.T) (*env, int64) {
	t.Helper()
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	prod := e.create(t, with(paper("survival"), "properties", map[string]any{"motd": "Survival!", "gamemode": "survival", "view_distance": 12}))
	return e, prod.ID
}

func (e *env) createDev(t *testing.T, prodID int64) int64 {
	t.Helper()
	s, err := e.h.CreateDev(context.Background(), prodID, nil)
	if err != nil {
		t.Fatalf("CreateDev(%d): %v", prodID, err)
	}
	return s.ID
}

func devRow(t *testing.T, e *env, id int64) store.DevInstance {
	t.Helper()
	d, err := store.New(e.db).DevGet(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func serverRow(t *testing.T, e *env, id int64) store.Server {
	t.Helper()
	s, err := store.New(e.db).RbacGetServer(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCreateDev(t *testing.T) {
	e, prodID := devEnv(t)
	e.agent.takeCalls()
	prodBefore, prodSrvBefore := instanceRow(t, e.db, prodID), serverRow(t, e, prodID)
	e.clk.Set(time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)) // after today's 05:00
	s, err := e.h.CreateDev(context.Background(), prodID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "dev-survival" || s.ID == prodID {
		t.Fatalf("dev = %+v", s)
	}
	in := instanceRow(t, e.db, s.ID)
	if in.Type != "paper" || in.Version != "1.21.11" || in.MemoryMib != 2048 || in.DesiredState != "stopped" || in.Revision != 1 {
		t.Fatalf("dev instance = %+v", in)
	}
	if !strings.Contains(in.Properties, `"motd":"[DEV] Survival!"`) || !strings.Contains(in.Properties, `"view_distance":4`) ||
		!strings.Contains(in.Properties, `"simulation_distance":3`) || !strings.Contains(in.Properties, `"gamemode":"survival"`) {
		t.Fatalf("dev properties = %s", in.Properties)
	}
	d := devRow(t, e, s.ID)
	wantNext := time.Date(2026, 10, 11, 5, 0, 0, 0, time.UTC).UnixMilli() // backup time zone UTC
	if d.SourceID != prodID || d.Sleeping != 1 || d.SyncTime != 300 || d.IdleMinutes != 15 || d.SyncPaused != 0 ||
		d.NextSyncAt.Int64 != wantNext || d.PropertyOverrides != `{"view_distance":4,"simulation_distance":3}` {
		t.Fatalf("dev row = %+v (next want %d)", d, wantNext)
	}
	if got := instanceRow(t, e.db, prodID); got != prodBefore {
		t.Fatalf("prod instance changed:\n%+v\n%+v", prodBefore, got)
	}
	if got := serverRow(t, e, prodID); got != prodSrvBefore {
		t.Fatalf("prod server changed: %+v", got)
	}

	// second dev of the same prod, dev of the dev
	for _, id := range []int64{prodID, s.ID} {
		if _, err := e.h.CreateDev(context.Background(), id, nil); !errors.Is(err, servers.ErrDevExists) {
			t.Errorf("CreateDev(%d) = %v, want ErrDevExists", id, err)
		}
	}
	// name too long: 29 characters
	long := e.create(t, paper(strings.Repeat("a", 29)))
	if _, err := e.h.CreateDev(context.Background(), long.ID, nil); !errors.Is(err, servers.ErrDevNameTooLong) || !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("29 chars: %v", err)
	}
	ok28 := e.create(t, paper(strings.Repeat("b", 28)))
	if dev, err := e.h.CreateDev(context.Background(), ok28.ID, nil); err != nil || len(dev.Name) != 32 {
		t.Fatalf("28 chars: %+v %v", dev, err)
	}
	// dev-<x> taken by an ordinary (pre-existing) server
	creative := e.create(t, paper("creative"))
	createInstance(t, e.db, "dev-creative")
	if _, err := e.h.CreateDev(context.Background(), creative.ID, nil); !errors.Is(err, servers.ErrNameTaken) {
		t.Fatalf("name taken: %v", err)
	}
	if n := e.count(t, "dev_instances"); n != 2 {
		t.Fatalf("dev_instances = %d rows", n)
	}
	// unknown server, server without instance
	if _, err := e.h.CreateDev(context.Background(), 9999, nil); !errors.Is(err, servers.ErrServerNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	stub := storetest.CreateServer(t, e.db, "lobby")
	if _, err := e.h.CreateDev(context.Background(), stub, nil); !errors.Is(err, servers.ErrNoInstance) {
		t.Fatalf("stub: %v", err)
	}
}

func TestCreateDevMemory(t *testing.T) {
	e, prodID := devEnv(t)
	e.hostMem = 4 << 30 // limit (4096-1024)*4/5 = 2457 → 2304
	for _, mib := range []int{4096, 1000, 1024 + 100, 0} {
		_, err := e.h.CreateDev(context.Background(), prodID, ptr(mib))
		if !errors.Is(err, servers.ErrInvalidMemory) || !errors.Is(err, instancespec.ErrInvalid) || !strings.Contains(err.Error(), "1024..2304 MiB") {
			t.Fatalf("memory %d: %v", mib, err)
		}
	}
	if n := e.count(t, "servers"); n != 1 {
		t.Fatalf("refused creates left %d servers", n)
	}
	s, err := e.h.CreateDev(context.Background(), prodID, ptr(1536))
	if err != nil || instanceRow(t, e.db, s.ID).MemoryMib != 1536 {
		t.Fatalf("1536: %v", err)
	}
}

func TestCreateDevWithoutBaseDomain(t *testing.T) {
	e := newEnv(t)
	prodID := createInstance(t, e.db, "survival")
	if _, err := e.h.CreateDev(context.Background(), prodID, nil); !errors.Is(err, servers.ErrBaseDomainMissing) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyDevSpecOnlyWritesDev(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	admin := e.admin()
	// prod changes after the dev was created; the dev's memory and overrides are its own
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", prodID), map[string]any{
		"version": "1.21.10", "memory_mib": 6144,
		"properties": map[string]any{"motd": "New", "gamemode": "creative", "difficulty": "hard"},
	}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("patch prod: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", devID), map[string]any{"memory_mib": 2560}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("patch dev memory: %d %s", rec.Code, rec.Body)
	}
	mustExec(t, e.db, `UPDATE dev_instances SET property_overrides = '{"view_distance":7,"simulation_distance":5}' WHERE server_id = ?`, devID)
	prodBefore, devBefore := instanceRow(t, e.db, prodID), instanceRow(t, e.db, devID)

	if err := e.h.ApplyDevSpec(context.Background(), devID); err != nil {
		t.Fatal(err)
	}
	if got := instanceRow(t, e.db, prodID); got != prodBefore {
		t.Fatalf("prod instance changed:\n%+v\n%+v", prodBefore, got)
	}
	dev := instanceRow(t, e.db, devID)
	if dev.Version != "1.21.10" || dev.MemoryMib != 2560 || dev.Revision != devBefore.Revision+1 || dev.DesiredState != devBefore.DesiredState {
		t.Fatalf("dev = %+v (before %+v)", dev, devBefore)
	}
	for _, want := range []string{`"motd":"[DEV] New"`, `"gamemode":"creative"`, `"difficulty":"hard"`, `"view_distance":7`, `"simulation_distance":5`} {
		if !strings.Contains(dev.Properties, want) {
			t.Errorf("dev properties %s lack %s", dev.Properties, want)
		}
	}
	if err := e.h.ApplyDevSpec(context.Background(), prodID); !errors.Is(err, servers.ErrNotDev) {
		t.Fatalf("ApplyDevSpec(prod) = %v", err)
	}
	if got := instanceRow(t, e.db, prodID); got != prodBefore {
		t.Fatalf("prod instance changed by ApplyDevSpec(prod): %+v", got)
	}
}

func TestApplyDevSpecCopiesPluginInstalls(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	other := e.create(t, paper("creative"))
	ins := `INSERT INTO plugin_installs (server_id, filename, sha512, source, project_id, project_slug, title,
		version_id, version_name, channel, installed_at) VALUES (?, ?, 'aa', 'modrinth', 'p', 's', 't', 'v', 'vn', 'release', 1)`
	mustExec(t, e.db, ins, prodID, "a.jar")
	mustExec(t, e.db, ins, prodID, "b.jar")
	mustExec(t, e.db, ins, devID, "stale.jar")
	mustExec(t, e.db, ins, other.ID, "c.jar")
	if err := e.h.ApplyDevSpec(context.Background(), devID); err != nil {
		t.Fatal(err)
	}
	files := func(id int64) []string {
		rows, err := store.New(e.db).PluginsListInstalls(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Filename)
		}
		return out
	}
	if got := files(devID); !slices.Equal(got, []string{"a.jar", "b.jar"}) {
		t.Fatalf("dev installs = %v", got)
	}
	if got := files(prodID); !slices.Equal(got, []string{"a.jar", "b.jar"}) {
		t.Fatalf("prod installs = %v", got)
	}
	if got := files(other.ID); !slices.Equal(got, []string{"c.jar"}) {
		t.Fatalf("other installs = %v", got)
	}
	// The copy queries refuse a target that is not the dev of the source.
	q := store.New(e.db)
	if err := q.PluginsCopyInstalls(context.Background(), store.PluginsCopyInstallsParams{TargetID: prodID, SourceID: devID}); err != nil {
		t.Fatal(err)
	}
	if err := q.PluginsDeleteDevInstalls(context.Background(), prodID); err != nil {
		t.Fatal(err)
	}
	if got := files(prodID); !slices.Equal(got, []string{"a.jar", "b.jar"}) {
		t.Fatalf("prod installs after a wrong-direction copy = %v", got)
	}
}

// The spec copy query itself only matches a dev of the given source.
func TestUpdateDevInstanceQueryRefusesProd(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	before := instanceRow(t, e.db, prodID)
	q := store.New(e.db)
	for _, p := range []store.ServersUpdateDevInstanceParams{
		{Type: "vanilla", Version: "1.20", Java: "java21", Properties: "{}", ServerID: prodID, SourceID: devID},
		{Type: "vanilla", Version: "1.20", Java: "java21", Properties: "{}", ServerID: prodID, SourceID: prodID},
	} {
		if _, err := q.ServersUpdateDevInstance(context.Background(), p); err == nil {
			t.Fatalf("update of prod with %+v succeeded", p)
		}
	}
	if _, err := q.ServersUpdateDevMemory(context.Background(), store.ServersUpdateDevMemoryParams{MemoryMib: 1024, ServerID: prodID}); err == nil {
		t.Fatal("memory update of prod succeeded")
	}
	if got := instanceRow(t, e.db, prodID); got != before {
		t.Fatalf("prod changed: %+v", got)
	}
}

func TestCreateServerReservedPrefix(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", paper("dev-survival"), &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" || !strings.Contains(rec.Body.String(), "reserved for dev instances") {
		t.Fatalf("dev-survival: %d %s", rec.Code, rec.Body)
	}
	e.create(t, paper("devsurvival")) // only the prefix with the hyphen is reserved
}

func TestUpdateDevOnlyMemory(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	admin := e.admin()
	path := fmt.Sprintf("/servers/%d", devID)
	before := instanceRow(t, e.db, devID)
	for _, body := range []map[string]any{
		{"version": "1.21.10"},
		{"memory_mib": 3072, "properties": map[string]any{}},
		{"java": "java21"},
		{"loader_version": nil},
		{"modpack": nil},
		{"web_routes": []any{}},
	} {
		rec := e.do(http.MethodPatch, path, body, &admin)
		if rec.Code != http.StatusConflict || errCode(t, rec) != "dev_managed" {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	if got := instanceRow(t, e.db, devID); got != before {
		t.Fatalf("refused patches changed the dev: %+v", got)
	}
	rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 3072}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("memory: %d %s", rec.Code, rec.Body)
	}
	got := decode[devServerResp](t, rec)
	if got.Instance.MemoryMiB != 3072 || got.Dev == nil || got.Dev.SourceID != prodID {
		t.Fatalf("answer = %+v", got)
	}
	in := instanceRow(t, e.db, devID)
	if in.MemoryMib != 3072 || in.Revision != before.Revision+1 || in.Properties != before.Properties {
		t.Fatalf("dev after memory patch = %+v", in)
	}
	// prod's PATCH is not restricted
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", prodID), map[string]any{"version": "1.21.10"}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("prod patch: %d %s", rec.Code, rec.Body)
	}
}

func TestDeleteProdWithDev(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	admin := e.admin()
	mustExec(t, e.db, `INSERT INTO dev_ops (server_id, name, created_at) VALUES (?, 'Steve', 0)`, devID)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", prodID), nil, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "dev_exists" {
		t.Fatalf("delete prod: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "remove") }) {
		t.Fatalf("agent asked to remove: %v", calls)
	}
	e.rec.Entries = nil
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", devID), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete dev: %d %s", rec.Code, rec.Body)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerDelete, audit.ActionDevDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if n := e.count(t, "dev_instances") + e.count(t, "dev_ops"); n != 0 {
		t.Fatalf("dev rows left: %d", n)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", prodID), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete prod after its dev: %d %s", rec.Code, rec.Body)
	}
}

func TestPowerStopSetsSleeping(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	admin := e.admin()
	wake := t0.Add(-time.Hour).UnixMilli()
	mustExec(t, e.db, `UPDATE dev_instances SET sleeping = 0, last_wake_at = ? WHERE server_id = ?`, wake, devID)
	if code, ec := e.power(devID, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop: %d %s", code, ec)
	}
	if d := devRow(t, e, devID); d.Sleeping != 1 || d.LastWakeAt.Int64 != wake {
		t.Fatalf("after stop: %+v", d)
	}
	if code, ec := e.power(devID, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("start: %d %s", code, ec)
	}
	if d := devRow(t, e, devID); d.Sleeping != 0 || d.LastWakeAt.Valid {
		t.Fatalf("after start: %+v", d)
	}
	if got := instanceRow(t, e.db, devID).DesiredState; got != "running" {
		t.Fatalf("desired = %s", got)
	}
	// prod's power is unaffected by the dev rules
	if code, _ := e.power(prodID, "stop", &admin); code != http.StatusAccepted || instanceRow(t, e.db, prodID).DesiredState != "stopped" {
		t.Fatalf("prod stop: %d", code)
	}
	if d := devRow(t, e, devID); d.Sleeping != 0 {
		t.Fatalf("prod stop changed the dev: %+v", d)
	}
}

func TestSetDevSleeping(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	prodBefore := instanceRow(t, e.db, prodID)
	now := t0.Add(time.Minute)
	if err := e.h.SetDevSleeping(context.Background(), devID, false, now); err != nil {
		t.Fatal(err)
	}
	if d := devRow(t, e, devID); d.Sleeping != 0 || d.LastWakeAt.Int64 != now.UnixMilli() {
		t.Fatalf("awake: %+v", d)
	}
	if got := instanceRow(t, e.db, devID).DesiredState; got != "running" {
		t.Fatalf("awake desired = %s", got)
	}
	if err := e.h.SetDevSleeping(context.Background(), devID, true, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if d := devRow(t, e, devID); d.Sleeping != 1 || d.LastWakeAt.Int64 != now.UnixMilli() {
		t.Fatalf("asleep: %+v", d)
	}
	if got := instanceRow(t, e.db, devID).DesiredState; got != "stopped" {
		t.Fatalf("asleep desired = %s", got)
	}
	if err := e.h.SetDevSleeping(context.Background(), prodID, true, now); !errors.Is(err, servers.ErrNotDev) {
		t.Fatalf("SetDevSleeping(prod) = %v", err)
	}
	if got := instanceRow(t, e.db, prodID); got != prodBefore {
		t.Fatalf("prod changed: %+v", got)
	}
}

type devSummaryResp struct {
	SourceID       int64   `json:"source_id"`
	SourceName     string  `json:"source_name"`
	SyncPaused     bool    `json:"sync_paused"`
	NextSyncAt     *int64  `json:"next_sync_at"`
	LastSyncAt     *int64  `json:"last_sync_at"`
	LastSyncStatus *string `json:"last_sync_status"`
	SyncPending    bool    `json:"sync_pending"`
	Sleeping       bool    `json:"sleeping"`
}

type devServerResp struct {
	serverResp
	Dev   *devSummaryResp `json:"dev"`
	DevID *int64          `json:"dev_id"`
}

func TestServerViewsShowDev(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	other := e.create(t, paper("creative"))
	admin := e.admin()

	list := decode[[]devServerResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	byID := map[int64]devServerResp{}
	for _, s := range list {
		byID[s.ID] = s
	}
	if p := byID[prodID]; p.DevID == nil || *p.DevID != devID || p.Dev != nil {
		t.Fatalf("prod = %+v", p)
	}
	d := byID[devID]
	if d.Dev == nil || d.DevID != nil || d.Dev.SourceID != prodID || d.Dev.SourceName != "survival" || !d.Dev.Sleeping ||
		d.Dev.NextSyncAt == nil || d.Dev.SyncPaused || d.Dev.SyncPending || d.Dev.LastSyncStatus != nil {
		t.Fatalf("dev = %+v %+v", d, d.Dev)
	}
	if o := byID[other.ID]; o.Dev != nil || o.DevID != nil {
		t.Fatalf("other = %+v", o)
	}
	// raw JSON: the fields are absent on ordinary servers
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", other.ID), nil, &admin)
	if strings.Contains(rec.Body.String(), `"dev`) {
		t.Fatalf("ordinary server has dev fields: %s", rec.Body)
	}

	// paused: no next sync; deferred or gated: pending
	mustExec(t, e.db, `UPDATE dev_instances SET sync_paused = 1, deferred_since = 1, last_sync_status = 'failed' WHERE server_id = ?`, devID)
	got := decode[devServerResp](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", devID), nil, &admin))
	if got.Dev.NextSyncAt != nil || !got.Dev.SyncPaused || !got.Dev.SyncPending || *got.Dev.LastSyncStatus != "failed" {
		t.Fatalf("paused dev = %+v", got.Dev)
	}
	mustExec(t, e.db, `UPDATE dev_instances SET deferred_since = NULL WHERE server_id = ?`, devID)
	e.gate.set(devID, true)
	if got := decode[devServerResp](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", devID), nil, &admin)); !got.Dev.SyncPending {
		t.Fatal("gated dev is not pending")
	}

	view, err := e.h.DevView(context.Background(), devID)
	if err != nil || view.Dev == nil || view.Name != "dev-survival" {
		t.Fatalf("DevView = %+v, %v", view, err)
	}
	if _, err := e.h.DevView(context.Background(), prodID); !errors.Is(err, servers.ErrNotDev) {
		t.Fatalf("DevView(prod) = %v", err)
	}
}

// A user with permissions on prod sees its dev with the same permissions except server.backups.
func TestServerViewsDevPermissions(t *testing.T) {
	e, prodID := devEnv(t)
	devID := e.createDev(t, prodID)
	other := e.create(t, paper("creative"))
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	grant(t, e.db, alice, prodID, "server.view", "server.files", "server.backups")
	p := userP(alice, "alice")

	list := decode[[]devServerResp](t, e.do(http.MethodGet, "/servers", nil, &p))
	got := map[int64][]string{}
	for _, s := range list {
		got[s.ID] = s.Permissions
	}
	want := map[int64][]string{
		prodID: {"server.view", "server.files", "server.backups"},
		devID:  {"server.view", "server.files"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("list permissions = %v, want %v", got, want)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", devID), nil, &p)
	if rec.Code != http.StatusOK {
		t.Fatalf("get dev: %d %s", rec.Code, rec.Body)
	}
	if perms := decode[devServerResp](t, rec).Permissions; !slices.Equal(perms, want[devID]) {
		t.Fatalf("get dev permissions = %v", perms)
	}
	if rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", other.ID), nil, &p); rec.Code != http.StatusForbidden {
		t.Fatalf("other server: %d", rec.Code)
	}
}
