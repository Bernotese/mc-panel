package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type cGate map[int64]bool

func (g cGate) DataJobRunning(_ context.Context, id int64) bool { return g[id] }

const cTickAnswer = "Target tick rate: 20.0 per second.\nAverage time per tick: 4.0ms (Target: 50.0ms)"

// cEnv is the collector of newEnv (shared fakes) with two servers under mc.example.com:
// paper "survival" running and vanilla "creative" stopped.
type cEnv struct {
	*env
	c     *collector
	pings atomic.Int32
	ids   map[string]int64
}

func newCEnv(t *testing.T) *cEnv {
	t.Helper()
	e := &cEnv{env: newEnv(t, func(d *Deps) { d.PingAddr = "127.0.0.1:25565"; d.Gate = cGate{} }), ids: map[string]int64{}}
	ctx := context.Background()
	q := store.New(e.db)
	e.ids["survival"] = e.server("survival")
	e.ids["creative"] = cAddInstance(t, e.db, "creative", "vanilla")
	if _, err := e.db.ExecContext(ctx, "UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?", e.ids["creative"]); err != nil {
		t.Fatal(err)
	}
	if err := q.ServersUpdateSettings(ctx, store.ServersUpdateSettingsParams{
		BaseDomain: sql.NullString{String: "mc.example.com", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.set(
		agentapi.InstanceStatus{ID: e.ids["survival"], State: agentapi.StateRunning, StartedAt: t0.UnixMilli() - 1_000_000,
			RestartCount: 2, CPUPercent: 12.5, MemoryBytes: 1 << 30, MemoryLimitBytes: 4 << 30, Revision: 1},
		agentapi.InstanceStatus{ID: e.ids["creative"], State: agentapi.StateExited},
	)
	e.console.reply(e.ids["survival"], cmdTickQuery, cTickAnswer, nil)
	e.c = e.h.coll
	e.c.ping = func(_ context.Context, addr, host string) (slpResult, error) {
		e.pings.Add(1)
		if addr != "127.0.0.1:25565" || host != "survival.mc.example.com" {
			t.Errorf("ping %s %s", addr, host)
		}
		return slpResult{Online: cIntp(2), Max: cIntp(20), Names: []string{"Alice", "Bob"}}, nil
	}
	e.c.host = &hostReader{proc: t.TempDir(), statfs: func(string) (uint64, uint64, error) { return 0, 0, errors.New("none") }}
	return e
}

// cAddInstance creates a server with an instance of the given type.
func cAddInstance(t *testing.T, db *sql.DB, name, typ string) int64 {
	t.Helper()
	id := storetest.CreateServer(t, db, name)
	if _, err := store.New(db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: typ, Version: "1.21.4", Java: "java21", MemoryMib: 2048, Properties: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *cEnv) sample(s Snapshot, name string) ServerSample {
	e.t.Helper()
	if sv, ok := findSample(s, e.ids[name]); ok {
		return sv
	}
	e.t.Fatalf("no sample for %s in %+v", name, s.Servers)
	return ServerSample{}
}

func (e *cEnv) rconCalls() int { return len(e.console.commands()) }

func TestPassSamplesRunningAndStoppedServers(t *testing.T) {
	e := newCEnv(t)
	s := e.c.pass(context.Background())
	if !s.Agent || !s.Docker || !s.At.Equal(e.clk.Now()) {
		t.Fatalf("snapshot head = %+v", s)
	}
	sv := e.sample(s, "survival")
	if !sv.DesiredRunning || sv.Name != "survival" || sv.State != agentapi.StateRunning || sv.RestartCount != 2 ||
		sv.Revision != 1 || sv.StartedAt != t0.UnixMilli()-1_000_000 || sv.Gated {
		t.Fatalf("survival state = %+v", sv)
	}
	if sv.Up == nil || !*sv.Up || *sv.Players != 2 || *sv.PlayersMax != 20 || len(sv.PlayerNames) != 2 {
		t.Fatalf("survival ping = %+v", sv)
	}
	if sv.TPS == nil || !cNear(*sv.TPS, 20) || !cNear(*sv.MSPT, 4) || sv.TPSSource != TPSSourceUnknown {
		t.Fatalf("survival tps = %v %v %s", sv.TPS, sv.MSPT, sv.TPSSource)
	}
	if *sv.CPUPercent != 12.5 || *sv.MemoryBytes != 1<<30 || *sv.MemoryLimit != 4<<30 {
		t.Fatalf("survival resources = %+v", sv)
	}
	cr := e.sample(s, "creative")
	if cr.DesiredRunning || cr.State != agentapi.StateExited || cr.TPSSource != TPSSourceUnknown {
		t.Fatalf("creative state = %+v", cr)
	}
	if cr.Up != nil || cr.Players != nil || cr.TPS != nil || cr.CPUPercent != nil || cr.MemoryBytes != nil {
		t.Fatalf("stopped server must have no values: %+v", cr)
	}
	if e.pings.Load() != 1 {
		t.Fatalf("pings = %d, only the running server is pinged", e.pings.Load())
	}
	if got := cCountRaw(t, e.db, e.ids["survival"]); got != 8 {
		t.Fatalf("raw rows = %d", got)
	}
	if got := cCountRaw(t, e.db, e.ids["creative"]); got != 0 {
		t.Fatalf("stopped server rows = %d", got)
	}
	if l := e.c.Latest(); !l.At.Equal(s.At) || len(l.Servers) != 2 {
		t.Fatal("Latest must return the last pass")
	}
}

func TestLatestIsACopy(t *testing.T) {
	e := newCEnv(t)
	if s := e.c.Latest(); !s.At.IsZero() || len(s.Servers) != 0 {
		t.Fatalf("before the first pass: %+v", s)
	}
	e.c.pass(context.Background())
	l := e.c.Latest()
	l.Servers[0].Name = "changed"
	if e.c.Latest().Servers[0].Name == "changed" {
		t.Fatal("Latest must not share its servers with the caller")
	}
}

func TestPassTPSOnlyEverySixtySeconds(t *testing.T) {
	e := newCEnv(t)
	first := e.sample(e.c.pass(context.Background()), "survival")
	e.clk.Advance(30 * time.Second)
	sv := e.sample(e.c.pass(context.Background()), "survival")
	if n := e.rconCalls(); n != 1 {
		t.Fatalf("rcon calls = %d after 30 s, want 1", n)
	}
	// V5: the last valid reading is carried (at most 90 s), but only fresh readings are stored
	if sv.TPS == nil || first.TPS == nil || *sv.TPS != *first.TPS {
		t.Fatalf("carried tps = %v, want %v", sv.TPS, first.TPS)
	}
	if n := cCountMetric(t, e.db, e.ids["survival"], MetricTPS); n != 1 {
		t.Fatalf("stored tps rows = %d, want 1", n)
	}
	e.clk.Advance(30 * time.Second)
	e.c.pass(context.Background())
	if n := e.rconCalls(); n != 2 {
		t.Fatalf("rcon calls = %d, want 2 in 60 s", n)
	}
	if n := cCountMetric(t, e.db, e.ids["survival"], MetricTPS); n != 2 {
		t.Fatalf("stored tps rows = %d, want 2", n)
	}
}

func TestPassCarriesTPSAtMost90s(t *testing.T) {
	e := newCEnv(t)
	e.c.pass(context.Background()) // valid reading at t0
	e.console.setDelay(time.Hour)  // from now on every TPS question times out
	e.c.rconTimeout = 50 * time.Millisecond
	e.clk.Advance(60 * time.Second)
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPS == nil {
		t.Fatal("a 60-s-old reading must be carried")
	}
	e.clk.Advance(60 * time.Second)
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPS != nil {
		t.Fatalf("a 120-s-old reading must not be carried: %v", *sv.TPS)
	}
	if n := cCountMetric(t, e.db, e.ids["survival"], MetricTPS); n != 1 {
		t.Fatalf("stored tps rows = %d, want only the fresh one", n)
	}
}

func TestPassDoesNotCarryTPSToAnUnreachableServer(t *testing.T) {
	e := newCEnv(t)
	e.c.pass(context.Background())
	e.c.ping = func(context.Context, string, string) (slpResult, error) { return slpResult{}, errSLP }
	e.clk.Advance(30 * time.Second)
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPS != nil {
		t.Fatal("no TPS for a server that does not answer the ping")
	}
}

func TestPassNoTPSWhenPingFails(t *testing.T) {
	e := newCEnv(t)
	e.c.ping = func(context.Context, string, string) (slpResult, error) { return slpResult{}, errSLP }
	s := e.c.pass(context.Background())
	sv := e.sample(s, "survival")
	if sv.Up == nil || *sv.Up || sv.TPS != nil || sv.Players != nil || e.rconCalls() != 0 {
		t.Fatalf("failed ping: up=%v tps=%v calls=%v", sv.Up, sv.TPS, e.console.commands())
	}
	if got := cCountRaw(t, e.db, e.ids["survival"]); got != 4 { // up, cpu_percent, memory_bytes, memory_limit_bytes
		t.Fatalf("raw rows = %d, want 4", got)
	}
}

func TestPassWithoutPingAddr(t *testing.T) {
	e := newCEnv(t)
	e.c.d.PingAddr = ""
	s := e.c.pass(context.Background())
	if sv := e.sample(s, "survival"); sv.Up != nil || sv.TPS != nil || e.pings.Load() != 0 {
		t.Fatalf("no ping address: up=%v pings=%d", sv.Up, e.pings.Load())
	}
}

func TestPassWithoutBaseDomain(t *testing.T) {
	e := newCEnv(t)
	if err := store.New(e.db).ServersUpdateSettings(context.Background(), store.ServersUpdateSettingsParams{}); err != nil {
		t.Fatal(err)
	}
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.Up != nil || e.pings.Load() != 0 {
		t.Fatalf("no base domain: up=%v pings=%d", sv.Up, e.pings.Load())
	}
}

func TestPassAgentDown(t *testing.T) {
	e := newCEnv(t)
	e.agent.setErr(agentapi.ErrUnavailable)
	e.c.host = &hostReader{proc: t.TempDir(), disk: "/x", statfs: func(string) (uint64, uint64, error) { return 1, 2, nil }}
	s := e.c.pass(context.Background())
	if s.Agent || s.Docker || len(s.Servers) != 0 {
		t.Fatalf("agent down: %+v", s)
	}
	if cCountRaw(t, e.db, e.ids["survival"]) != 0 {
		t.Fatal("no server values without the agent")
	}
	if cCountRaw(t, e.db, HostSeries) != 2 {
		t.Fatal("host values are written without the agent (spec §3.1)")
	}
}

func TestPassWithoutInstancesClient(t *testing.T) {
	e := newCEnv(t)
	e.c.d.Instances = nil
	if s := e.c.pass(context.Background()); s.Agent || len(s.Servers) != 0 {
		t.Fatalf("no agent client: %+v", s)
	}
}

func TestPassDockerDown(t *testing.T) {
	e := newCEnv(t)
	e.agent.setDocker(agentapi.DockerUnavailable)
	s := e.c.pass(context.Background())
	if !s.Agent || s.Docker {
		t.Fatalf("docker down: agent=%v docker=%v", s.Agent, s.Docker)
	}
	if sv := e.sample(s, "survival"); sv.State != agentapi.StateMissing || sv.Up != nil || sv.CPUPercent != nil {
		t.Fatalf("unknown state must not look like running: %+v", sv)
	}
}

func TestPassServerWithoutContainer(t *testing.T) {
	e := newCEnv(t)
	e.agent.set() // agent and Docker fine, no containers
	s := e.c.pass(context.Background())
	if sv := e.sample(s, "survival"); sv.State != agentapi.StateMissing || !sv.DesiredRunning || sv.Up != nil {
		t.Fatalf("missing container: %+v", sv)
	}
}

func TestPassGate(t *testing.T) {
	e := newCEnv(t)
	e.c.d.Gate = cGate{e.ids["survival"]: true}
	s := e.c.pass(context.Background())
	if !e.sample(s, "survival").Gated || e.sample(s, "creative").Gated {
		t.Fatal("Gated must reflect the data gate per server")
	}
}

func TestPassRCONHangDoesNotBlockLongerThanItsDeadline(t *testing.T) {
	e := newCEnv(t)
	e.console.setDelay(time.Hour)
	e.c.rconTimeout = 50 * time.Millisecond
	start := time.Now()
	s := e.c.pass(context.Background())
	if time.Since(start) > 2*time.Second {
		t.Fatalf("pass took %v", time.Since(start))
	}
	if sv := e.sample(s, "survival"); sv.TPS != nil || sv.TPSSource != TPSSourceUnknown {
		t.Fatal("hanging RCON must leave TPS unset")
	}
}

func TestPassRCONErrorDoesNotCountForDetection(t *testing.T) {
	e := newCEnv(t)
	e.console.reply(e.ids["survival"], cmdTickQuery, "", agentapi.ErrRconUnavailable)
	for range 4 {
		e.c.pass(context.Background())
		e.clk.Advance(tpsEvery)
	}
	for _, c := range e.console.commands() {
		if c != fmt.Sprintf("%d %s", e.ids["survival"], cmdTickQuery) {
			t.Fatalf("RCON errors must not move the detection to mspt: %v", e.console.commands())
		}
	}
}

func TestPassAsksServersInParallel(t *testing.T) {
	e := newCEnv(t)
	var inst []agentapi.InstanceStatus
	for i := range 9 {
		id := cAddInstance(t, e.db, fmt.Sprintf("srv-%d", i), "vanilla")
		inst = append(inst, agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning})
		e.console.reply(id, cmdTickQuery, cTickAnswer, nil)
	}
	e.agent.set(inst...)
	var cur, peak atomic.Int32
	e.c.ping = func(context.Context, string, string) (slpResult, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return slpResult{}, nil
	}
	e.console.setDelay(200 * time.Millisecond)
	start := time.Now()
	s := e.c.pass(context.Background())
	if peak.Load() > maxParallelPings || peak.Load() < 2 {
		t.Fatalf("peak concurrent pings = %d", peak.Load())
	}
	// nine 200-ms RCON questions, four at a time: three rounds, not nine
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("pass took %v: TPS questions must run in parallel", d)
	}
	for _, sv := range s.Servers {
		if sv.Up != nil && *sv.Up && sv.TPS == nil {
			t.Fatalf("server %d without TPS", sv.ID)
		}
	}
}

func TestPassForgetsDeletedServers(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	e.c.pass(context.Background())
	e.c.Watch(id)
	if _, err := e.db.ExecContext(context.Background(), "DELETE FROM servers WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	e.c.pass(context.Background())
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	if len(e.c.detectors)+len(e.c.tps)+len(e.c.lastTPS)+len(e.c.watch) != 0 {
		t.Fatalf("state of a deleted server kept: %v %v %v %v", e.c.detectors, e.c.tps, e.c.lastTPS, e.c.watch)
	}
}

func TestDetectionUsesTheRunningRevision(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	for range 3 {
		e.c.pass(context.Background())
		e.clk.Advance(tpsEvery)
	}
	// a spec change bumps the desired revision; the container still runs revision 1
	if _, err := e.db.ExecContext(context.Background(), "UPDATE instances SET revision = 2 WHERE server_id = ?", id); err != nil {
		t.Fatal(err)
	}
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPSSource != TPSSourceTickQuery || sv.Revision != 1 {
		t.Fatalf("source = %s revision = %d, the running container did not change", sv.TPSSource, sv.Revision)
	}
}

func TestLiveModeAsksTPSEveryFiveSeconds(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	e.c.pass(context.Background())
	e.c.liveTick(context.Background())
	if n := e.rconCalls(); n != 1 {
		t.Fatalf("live tick without Watch asked RCON: %d calls", n)
	}
	e.c.Watch(id)
	e.clk.Advance(5 * time.Second)
	e.c.liveTick(context.Background())
	e.clk.Advance(5 * time.Second)
	e.c.liveTick(context.Background())
	if n := e.rconCalls(); n != 3 {
		t.Fatalf("rcon calls = %d, want 1 (pass) + 2 (live)", n)
	}
	sv, ok := e.c.Live(id)
	if !ok || sv.TPS == nil || sv.Players == nil || *sv.Players != 2 {
		t.Fatalf("live = %+v %v", sv, ok)
	}
	e.clk.Advance(liveWindow) // nobody looked for 30 s
	e.c.liveTick(context.Background())
	if n := e.rconCalls(); n != 3 {
		t.Fatalf("live mode must end after %v without Watch: %d calls", liveWindow, n)
	}
	if _, ok := e.c.Live(9999); ok {
		t.Fatal("unknown server must report ok=false")
	}
}

func TestLiveShowsTheFreshestReading(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	e.c.pass(context.Background())
	e.c.Watch(id)
	e.console.reply(id, cmdTickQuery, "Target tick rate: 20.0 per second.\nAverage time per tick: 80.0ms (Target: 50.0ms)", nil)
	e.clk.Advance(5 * time.Second)
	e.c.liveTick(context.Background())
	if sv, _ := e.c.Live(id); sv.MSPT == nil || !cNear(*sv.MSPT, 80) || !cNear(*sv.TPS, 12.5) {
		t.Fatalf("live = %v %v, want the 5-s reading", sv.TPS, sv.MSPT)
	}
	if l := e.sample(e.c.Latest(), "survival"); !cNear(*l.MSPT, 4) {
		t.Fatalf("Latest must keep the pass value: %v", *l.MSPT)
	}
	if n := cCountMetric(t, e.db, id, MetricMSPT); n != 1 {
		t.Fatalf("live readings must not be stored: %d rows", n)
	}
}

func TestLiveAndPassNeverOverlapPerServer(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	e.console.setDelay(30 * time.Millisecond)
	e.c.pass(context.Background())
	e.clk.Advance(60 * time.Second) // the pass is due again
	e.c.Watch(id)                   // and the server is watched
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.c.pass(context.Background()) }()
	go func() { defer wg.Done(); e.c.liveTick(context.Background()) }()
	wg.Wait()
	if n := e.rconCalls(); n != 3 {
		t.Fatalf("rcon calls = %d, want 3", n)
	}
	if p := e.console.peak(id); p != 1 {
		t.Fatalf("%d RCON calls for one server at the same time", p)
	}
}

func TestDetectionFixesAfterThreeAnswers(t *testing.T) {
	e := newCEnv(t)
	for range 3 {
		e.c.pass(context.Background())
		e.clk.Advance(tpsEvery)
	}
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPSSource != TPSSourceTickQuery {
		t.Fatalf("source = %s", sv.TPSSource)
	}
}

func TestRunSkipsATickAfterALongPass(t *testing.T) {
	e := newCEnv(t)
	e.c.every = 40 * time.Millisecond
	var passes atomic.Int32
	e.c.ping = func(context.Context, string, string) (slpResult, error) {
		if passes.Add(1) == 1 {
			time.Sleep(140 * time.Millisecond) // overruns more than three ticks
		}
		return slpResult{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Millisecond)
	defer cancel()
	var calls atomic.Int32
	e.c.Run(ctx, func(context.Context, Snapshot) { calls.Add(1) })
	if n := calls.Load(); n > 3 || n < 1 {
		t.Fatalf("passes = %d: a long pass must not be followed by a burst", n)
	}
}

func TestRunStopsWithTheContext(t *testing.T) {
	e := newCEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var calls atomic.Int32
	go func() { e.c.Run(ctx, func(context.Context, Snapshot) { calls.Add(1) }); close(done) }()
	eventually(t, "first pass", func() bool { return calls.Load() == 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
