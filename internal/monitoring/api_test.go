package monitoring

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// aSampler is a collector stand-in for the read routes.
type aSampler struct {
	mu      sync.Mutex
	latest  Snapshot
	live    map[int64]ServerSample
	watched []int64
}

func (f *aSampler) Latest() Snapshot { f.mu.Lock(); defer f.mu.Unlock(); return f.latest }

func (f *aSampler) Watch(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watched = append(f.watched, id)
}

func (f *aSampler) Live(id int64) (ServerSample, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.live[id]
	return s, ok
}

func (f *aSampler) setLatest(s Snapshot) { f.mu.Lock(); defer f.mu.Unlock(); f.latest = s }

func (f *aSampler) setLive(s ServerSample) { f.mu.Lock(); defer f.mu.Unlock(); f.live[s.ID] = s }

func (f *aSampler) watchedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.watched)
}

// aFiring is an alerter stand-in.
type aFiring struct{ list []FiringAlert }

func (f *aFiring) Firing(context.Context) []FiringAlert { return f.list }

// apiEnv is the shared env (helpers_test.go) with a router that holds only the read routes,
// served from the stand-ins.
type apiEnv struct {
	*env
	src    *aSampler
	firing *aFiring
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := &apiEnv{env: newEnv(t), src: &aSampler{live: map[int64]ServerSample{}}, firing: &aFiring{}}
	e.mux = chi.NewRouter()
	e.rt = rbac.NewRouter(e.mux, rbac.NewDBChecker(e.db), e.clk)
	(&apiHandlers{q: store.New(e.db), clock: e.clk, src: e.src, alerts: e.firing}).register(e.rt)
	return e
}

func (e *apiEnv) must(want int, method, path string, p principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := e.do(method, path, nil, &p)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec
}

// aReadRoutes are the routes apiHandlers.register adds; the route contract of all seven
// (routes_test.go) and the settings tests (settings_test.go) are fixed since phase A.
var aReadRoutes = map[string]rbac.Rule{
	"GET /servers/{id}/metrics":      rbac.Server(rbac.PermServerView, "id"),
	"GET /servers/{id}/metrics/live": rbac.Server(rbac.PermServerView, "id"),
	"GET /monitoring/overview":       rbac.Authenticated(),
	"GET /monitoring/host":           rbac.Admin(),
}

func TestRouteRules(t *testing.T) {
	e := newAPIEnv(t)
	got := e.rt.Routes()
	if len(got) != len(aReadRoutes) {
		t.Fatalf("register adds %d routes, want %d", len(got), len(aReadRoutes))
	}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		if w, ok := aReadRoutes[key]; !ok || w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %v", key, r.Rule, w)
		}
	}

	mine, other := e.server("survival"), e.server("creative")
	bob := e.user("bob", map[int64][]string{mine: {"server.view"}})
	e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", mine), bob)
	e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics/live", mine), bob)
	e.must(http.StatusForbidden, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", other), bob)
	e.must(http.StatusForbidden, http.MethodGet, fmt.Sprintf("/servers/%d/metrics/live", other), bob)
	e.must(http.StatusForbidden, http.MethodGet, "/monitoring/host?range=1h", bob)
	if w := e.src.watchedIDs(); !slices.Equal(w, []int64{mine}) {
		t.Fatalf("watched = %v: a forbidden live call must not switch on live mode", w)
	}
}

func TestMetricsValidatesRangeAndServer(t *testing.T) {
	e := newAPIEnv(t)
	id := e.server("survival")
	admin := e.admin()
	for _, path := range []string{
		fmt.Sprintf("/servers/%d/metrics?range=2h", id), fmt.Sprintf("/servers/%d/metrics", id), "/monitoring/host?range=all",
	} {
		if code := errCode(t, e.must(http.StatusBadRequest, http.MethodGet, path, admin)); code != httpx.CodeValidation {
			t.Fatalf("%s: %s", path, code)
		}
	}
	e.must(http.StatusNotFound, http.MethodGet, "/servers/999/metrics?range=1h", admin)
	e.must(http.StatusNotFound, http.MethodGet, "/servers/999/metrics/live", admin)
	if w := e.src.watchedIDs(); len(w) != 0 {
		t.Fatalf("watched = %v: an unknown server must not be watched", w)
	}
}

func TestMetricsReturnsSeries(t *testing.T) {
	e := newAPIEnv(t)
	id := e.server("survival")
	admin := e.admin()
	aInsertRaw(t, e.db, id, MetricPlayers, t0.Add(-time.Minute), 4)
	aInsertRaw(t, e.db, HostSeries, MetricLoad1, t0.Add(-time.Minute), 0.5) // not a server series
	got := decode[metricsJSON](t, e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", id), admin))
	if got.Range != "1h" || got.StepMS != 30_000 || len(got.Series) != len(serverMetrics) ||
		len(got.Series[MetricPlayers]) != 1 || got.Series[MetricPlayers][0] != (point{float64(t0.Add(-time.Minute).UnixMilli()), 4, 4, 4}) {
		t.Fatalf("metrics = %+v", got)
	}
	if s, ok := got.Series[MetricTPS]; !ok || s == nil || len(s) != 0 {
		t.Fatalf("missing empty tps series: %+v", got.Series)
	}
	for rg, step := range map[string]int64{"24h": 300_000, "7d": 3_600_000, "30d": 3_600_000} {
		if got := decode[metricsJSON](t, e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=%s", id, rg), admin)); got.Range != rg || got.StepMS != step {
			t.Fatalf("%s = %+v", rg, got)
		}
	}
	host := decode[metricsJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/host?range=1h", admin))
	if host.StepMS != 30_000 || len(host.Series) != len(hostMetrics) || len(host.Series[MetricLoad1]) != 1 || len(host.Series[MetricMemUsed]) != 0 {
		t.Fatalf("host = %+v", host)
	}
}

// The raw JSON keeps every value null before the first pass and player_names an array.
func TestLiveBeforeFirstPass(t *testing.T) {
	e := newAPIEnv(t)
	id := e.server("survival")
	rec := e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics/live", id), e.admin())
	want := `{"at":null,"up":null,"players":null,"players_max":null,"player_names":[],"tps":null,"mspt":null,` +
		`"cpu_percent":null,"memory_bytes":null,"memory_limit_bytes":null,"tps_source":"unknown"}`
	if got := rec.Body.String(); got != want+"\n" && got != want {
		t.Fatalf("live = %s", got)
	}
}

func TestLiveWatchesAndMapsValues(t *testing.T) {
	e := newAPIEnv(t)
	id := e.server("survival")
	path := fmt.Sprintf("/servers/%d/metrics/live", id)
	admin := e.admin()

	e.src.setLatest(Snapshot{At: t0, Agent: true, Docker: true})
	running := aLiveRunning(id, 3)
	tps, mspt := 19.8, 12.5
	running.TPS, running.MSPT, running.TPSSource, running.PlayerNames = &tps, &mspt, TPSSourceTickQuery, []string{"Alex"}
	e.src.setLive(running)
	got := decode[liveJSON](t, e.must(http.StatusOK, http.MethodGet, path, admin))
	if got.At == nil || *got.At != t0.UnixMilli() || got.Up == nil || !*got.Up || *got.Players != 3 || *got.TPS != 19.8 ||
		*got.MSPT != 12.5 || *got.CPUPercent != 12.5 || *got.MemoryBytes != 2<<30 || *got.MemoryLimitBytes != 5<<30 ||
		got.TPSSource != TPSSourceTickQuery || !slices.Equal(got.PlayerNames, []string{"Alex"}) {
		t.Fatalf("live = %+v", got)
	}

	// a stopped server: null values, player_names [], tps_source unknown (V6)
	stopped := ServerSample{ID: id, State: agentapi.StateExited, TPSSource: TPSSourceNone}
	e.src.setLive(stopped)
	got = decode[liveJSON](t, e.must(http.StatusOK, http.MethodGet, path, admin))
	if got.Up != nil || got.Players != nil || got.TPS != nil || got.PlayerNames == nil || len(got.PlayerNames) != 0 ||
		got.TPSSource != TPSSourceUnknown {
		t.Fatalf("stopped live = %+v", got)
	}
	if w := e.src.watchedIDs(); !slices.Equal(w, []int64{id, id}) {
		t.Fatalf("watched = %v", w)
	}

	// the agent did not answer the last pass
	e.src.setLatest(Snapshot{At: t0.Add(30 * time.Second)})
	rec := e.must(http.StatusServiceUnavailable, http.MethodGet, path, admin)
	if code := errCode(t, rec); code != httpx.CodeAgentUnavailable {
		t.Fatalf("code = %s", code)
	}
}

func aLiveRunning(id int64, players int) ServerSample {
	up, cpu, mem, limit := true, 12.5, int64(2<<30), int64(5<<30)
	return ServerSample{ID: id, DesiredRunning: true, State: agentapi.StateRunning, Up: &up, Players: &players,
		CPUPercent: &cpu, MemoryBytes: &mem, MemoryLimit: &limit}
}
