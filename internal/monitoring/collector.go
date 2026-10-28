package monitoring

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/timescale"
)

// collectEvery, tpsEvery, liveTPSEvery, liveWindow and maxParallelPings come from types.go.
const (
	statusTimeout = 10 * time.Second
	// dataTimeout bounds all data state lookups of one pass together: a files service that
	// hangs makes the remaining servers unknown at once.
	dataTimeout     = 2 * time.Second
	rconTimeout     = 10 * time.Second
	maxParallelRcon = 4 // RCON calls at once, pass and live loop together
)

// Scaled with collectEvery in acceptance builds (types.go).
var (
	liveFresh = timescale.D(10 * time.Second) // a 5-s live reading counts as current this long
	tpsCarry  = timescale.D(90 * time.Second) // the last valid TPS/MSPT is carried this long (V5)
	// tpsDue tolerates ticker jitter: a pass a little early after tpsEvery still asks.
	tpsDue = tpsEvery - collectEvery/2
)

// collector runs the passes of spec §3.1. All state is guarded by mu; RCON per server is
// serialised by rconLocks, so a pass and the live loop never ask the same server at once.
type collector struct {
	d           Deps
	log         *slog.Logger
	ping        func(ctx context.Context, addr, host string) (slpResult, error)
	host        *hostReader
	every       time.Duration
	rconTimeout time.Duration

	mu        sync.Mutex
	latest    Snapshot
	known     map[int64]instRow // instances of the last pass (live mode)
	detectors map[int64]*tpsDetector
	lastTPS   map[int64]time.Time // last TPS question of a pass
	tps       map[int64]tpsSample // last valid reading (pass or live)
	watch     map[int64]time.Time // live mode until
	rconLocks map[int64]*sync.Mutex
	rconSem   chan struct{} // shared by pass and live loop, taken before the server lock
}

type tpsSample struct {
	at       time.Time
	r        tpsReading
	source   string
	revision int64 // container revision of the reading: a new container does not inherit it
}

type instRow struct {
	id       int64
	name     string
	typ      string
	desired  bool
	revision int64
	fqdn     string // "" without a base domain: no ping
}

func newCollector(d Deps) *collector {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &collector{
		d: d, log: log, ping: slpPing, host: newHostReader(d.InstancesDir),
		every: collectEvery, rconTimeout: rconTimeout,
		known: map[int64]instRow{}, detectors: map[int64]*tpsDetector{}, lastTPS: map[int64]time.Time{},
		tps: map[int64]tpsSample{}, watch: map[int64]time.Time{}, rconLocks: map[int64]*sync.Mutex{},
		rconSem: make(chan struct{}, maxParallelRcon),
	}
}

// Run passes every collectEvery (one right away) and runs the live loop; it returns when ctx
// ends. The waits run on time.Ticker (clock.Clock only has Now); the clock stamps the samples
// and drives the TPS and live-mode intervals.
func (c *collector) Run(ctx context.Context, onPass func(context.Context, Snapshot)) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.runLive(ctx) }()
	defer wg.Wait()
	t := time.NewTicker(c.every)
	defer t.Stop()
	for {
		s := c.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		if onPass != nil {
			onPass(ctx, s)
		}
		select { // a pass that overran drops the buffered tick: no burst (plan deviation 5)
		case <-t.C:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *collector) runLive(ctx context.Context) {
	t := time.NewTicker(liveTPSEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.liveTick(ctx)
		}
	}
}

// pass collects, stores and returns one snapshot.
func (c *collector) pass(ctx context.Context) Snapshot {
	now := c.d.Clock.Now()
	s := Snapshot{At: now}
	s.Servers, s.Agent, s.Docker = c.servers(ctx, now)
	s.Host = c.host.read()
	if err := writeSnapshot(ctx, c.d.DB, s); err != nil && ctx.Err() == nil {
		c.log.ErrorContext(ctx, "monitoring: write samples", "err", err)
	}
	c.mu.Lock()
	c.carryLocked(&s, now) // after writing: only fresh readings are stored (V5)
	c.latest = cloneSnapshot(s)
	c.mu.Unlock()
	return s
}

func cloneSnapshot(s Snapshot) Snapshot {
	s.Servers = append([]ServerSample(nil), s.Servers...)
	return s
}

// carryLocked gives reachable servers without a fresh reading in this pass the last valid
// reading (pass or live) if it is at most tpsCarry old, so Latest and Live do not flip between
// a value and null every other pass (V5).
func (c *collector) carryLocked(s *Snapshot, now time.Time) {
	for i := range s.Servers {
		sv := &s.Servers[i]
		if sv.TPS != nil || sv.Up == nil || !*sv.Up {
			continue
		}
		if t, ok := c.tps[sv.ID]; ok && t.revision == sv.Revision && !now.Before(t.at) && now.Sub(t.at) <= tpsCarry {
			tps, mspt := t.r.TPS, t.r.MSPT
			sv.TPS, sv.MSPT = &tps, &mspt
		}
	}
}

func (c *collector) listInstances(ctx context.Context) ([]instRow, error) {
	q := store.New(c.d.DB)
	list, err := q.ServersListInstances(ctx)
	if err != nil {
		return nil, err
	}
	set, err := q.ServersGetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]instRow, 0, len(list))
	for _, r := range list {
		row := instRow{id: r.ServerID, name: r.Name, typ: r.Type, desired: r.DesiredState == "running", revision: r.Revision}
		if set.BaseDomain.Valid {
			if f, err := instancespec.FQDN(r.Name, set.BaseDomain.String); err == nil {
				row.fqdn = f
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// servers samples every server with an instance; without an agent answer it returns none (V5).
func (c *collector) servers(ctx context.Context, now time.Time) ([]ServerSample, bool, bool) {
	if c.d.Instances == nil {
		return nil, false, false
	}
	rows, err := c.listInstances(ctx)
	if err != nil {
		// Without the server list the pass cannot tell which samples belong to which server, so
		// it reports Agent=false like a missing agent: B2 then evaluates no server alerts.
		if ctx.Err() == nil {
			c.log.ErrorContext(ctx, "monitoring: list instances", "err", err)
		}
		return nil, false, false
	}
	sctx, cancel := context.WithTimeout(ctx, statusTimeout)
	resp, err := c.d.Instances.Instances(sctx)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			c.log.DebugContext(ctx, "monitoring: agent status", "err", err)
		}
		return nil, false, false
	}
	docker := resp.Docker == agentapi.DockerOK
	status := make(map[int64]agentapi.InstanceStatus, len(resp.Instances))
	for _, st := range resp.Instances {
		status[st.ID] = st
	}
	out := make([]ServerSample, len(rows))
	dctx, dcancel := context.WithTimeout(ctx, dataTimeout)
	defer dcancel()
	for i, r := range rows {
		sv := ServerSample{ID: r.id, Name: r.name, DesiredRunning: r.desired, Revision: r.revision, State: agentapi.StateMissing}
		sv.DataState = servers.StateSafe
		if c.d.DataStates != nil {
			sv.DataState, _ = c.d.DataStates.State(dctx, r.id) // an error comes with StateUnknown
		}
		// A restore or extract stops the server on purpose, and one whose restore could not be
		// completed must not start. An unknown state does not silence alerts (data safety spec
		// §2.3): a data-safety error must not hide a real outage.
		sv.Gated = sv.DataState == servers.StateBusy || sv.DataState == servers.StateRecoveryRequired
		if st, ok := status[r.id]; ok && docker {
			sv.State, sv.StartedAt, sv.RestartCount = st.State, st.StartedAt, st.RestartCount
			if st.Revision > 0 { // the running container's revision (label), not the desired one
				sv.Revision = st.Revision
			}
			// The agent sets the memory limit only when it got container stats; without them CPU
			// and RAM are unknown, not 0. Known limit: the first sample after a start can report
			// CPU 0 (Docker has no previous reading to diff against yet).
			if st.State == agentapi.StateRunning && st.MemoryLimitBytes > 0 {
				cpu, mem, lim := st.CPUPercent, st.MemoryBytes, st.MemoryLimitBytes
				sv.CPUPercent, sv.MemoryBytes, sv.MemoryLimit = &cpu, &mem, &lim
			}
		}
		out[i] = sv
	}
	c.pingAll(ctx, rows, out)
	c.tpsAll(ctx, rows, out, now)
	c.mu.Lock()
	known := make(map[int64]instRow, len(rows))
	for i, r := range rows {
		known[r.id] = r
		out[i].TPSSource = TPSSourceUnknown // not running: unknown (V6)
		if d, ok := c.detectors[r.id]; ok && out[i].State == agentapi.StateRunning {
			out[i].TPSSource = d.source()
		}
	}
	c.known = known
	c.forgetLocked(known)
	c.mu.Unlock()
	return out, true, docker
}

// forgetLocked drops the state of deleted servers. An RCON lock is dropped only while free;
// a deleted server is never asked again, so a caller that fetched the old lock just before
// cannot race a new one.
func (c *collector) forgetLocked(known map[int64]instRow) {
	for id, l := range c.rconLocks {
		if _, ok := known[id]; !ok && l.TryLock() {
			delete(c.rconLocks, id)
			l.Unlock()
		}
	}
	for _, m := range []map[int64]time.Time{c.lastTPS, c.watch} {
		for id := range m {
			if _, ok := known[id]; !ok {
				delete(m, id)
			}
		}
	}
	for id := range c.detectors {
		if _, ok := known[id]; !ok {
			delete(c.detectors, id)
		}
	}
	for id := range c.tps {
		if _, ok := known[id]; !ok {
			delete(c.tps, id)
		}
	}
}

// parallel runs fn for every index in idx, at most limit at once, and waits for all.
func parallel(idx []int, limit int, fn func(i int)) {
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, i := range idx {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}()
	}
	wg.Wait()
}

func (c *collector) pingAll(ctx context.Context, rows []instRow, out []ServerSample) {
	if c.d.PingAddr == "" {
		return
	}
	var idx []int
	for i, r := range rows {
		if out[i].State == agentapi.StateRunning && r.fqdn != "" {
			idx = append(idx, i)
		}
	}
	parallel(idx, maxParallelPings, func(i int) {
		res, err := c.ping(ctx, c.d.PingAddr, rows[i].fqdn)
		up := err == nil
		out[i].Up = &up
		if up {
			out[i].Players, out[i].PlayersMax, out[i].PlayerNames = res.Online, res.Max, res.Names
		}
	})
}

// tpsAll asks TPS for every reachable server whose last question is tpsEvery old.
func (c *collector) tpsAll(ctx context.Context, rows []instRow, out []ServerSample, now time.Time) {
	var idx []int
	c.mu.Lock()
	for i, r := range rows {
		if out[i].Up == nil || !*out[i].Up {
			continue
		}
		if last, ok := c.lastTPS[r.id]; ok && now.Sub(last) < tpsDue && !now.Before(last) {
			continue
		}
		c.lastTPS[r.id] = now
		idx = append(idx, i)
	}
	c.mu.Unlock()
	parallel(idx, len(idx), func(i int) { // askTPS limits the RCON calls (rconSem)
		if reading, ok := c.askTPS(ctx, rows[i].id, rows[i].typ, out[i].Revision); ok {
			t, m := reading.TPS, reading.MSPT
			out[i].TPS, out[i].MSPT = &t, &m
		}
	})
}

func (c *collector) lockFor(id int64) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.rconLocks[id]
	if !ok {
		l = &sync.Mutex{}
		c.rconLocks[id] = l
	}
	return l
}

// askTPS sends the detector's next command; a valid answer also feeds the live view. RCON
// errors (timeout, server still starting) neither count for the detection nor are retried.
// The semaphore (shared by pass and live loop) is taken before the server lock; a call that
// waits for a busy server keeps its slot for at most one RCON deadline.
func (c *collector) askTPS(ctx context.Context, id int64, typ string, revision int64) (tpsReading, bool) {
	if c.d.Console == nil {
		return tpsReading{}, false
	}
	select {
	case c.rconSem <- struct{}{}:
	case <-ctx.Done():
		return tpsReading{}, false
	}
	defer func() { <-c.rconSem }()
	lock := c.lockFor(id)
	lock.Lock()
	defer lock.Unlock()
	c.mu.Lock()
	det, ok := c.detectors[id]
	if !ok {
		det = &tpsDetector{}
		c.detectors[id] = det
	}
	if t, ok := c.tps[id]; ok && t.revision != revision {
		delete(c.tps, id) // a new container: the old reading is not carried
	}
	cmd, ok := det.command(typ, revision)
	c.mu.Unlock()
	if !ok {
		return tpsReading{}, false
	}
	rctx, cancel := context.WithTimeout(ctx, c.rconTimeout)
	out, err := c.d.Console.Rcon(rctx, id, cmd)
	cancel()
	if err != nil {
		return tpsReading{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	reading, good := det.observe(cmd, out)
	if good {
		c.tps[id] = tpsSample{at: c.d.Clock.Now(), r: reading, source: det.source(), revision: revision}
	}
	return reading, good
}

// Watch switches the server to live mode for liveWindow from now (spec §6.2).
func (c *collector) Watch(id int64) {
	c.mu.Lock()
	c.watch[id] = c.d.Clock.Now().Add(liveWindow)
	c.mu.Unlock()
}

// liveTick asks TPS for every watched server that answered the last ping.
func (c *collector) liveTick(ctx context.Context) {
	now := c.d.Clock.Now()
	type job struct {
		r   instRow
		rev int64
	}
	var jobs []job
	c.mu.Lock()
	for id, until := range c.watch {
		if now.After(until) {
			delete(c.watch, id)
			continue
		}
		r, ok := c.known[id]
		sv, okS := findSample(c.latest, id)
		if ok && okS && sv.Up != nil && *sv.Up {
			jobs = append(jobs, job{r: r, rev: sv.Revision})
		}
	}
	c.mu.Unlock()
	idx := make([]int, len(jobs))
	for i := range jobs {
		idx[i] = i
	}
	parallel(idx, len(idx), func(i int) { c.askTPS(ctx, jobs[i].r.id, jobs[i].r.typ, jobs[i].rev) })
}

func findSample(s Snapshot, id int64) (ServerSample, bool) {
	for _, sv := range s.Servers {
		if sv.ID == id {
			return sv, true
		}
	}
	return ServerSample{}, false
}

// Latest returns a copy of the last pass (zero value before the first).
func (c *collector) Latest() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneSnapshot(c.latest)
}

// Live returns the server's sample of the last pass with the freshest TPS (≤ liveFresh old);
// ok=false for a server the last pass did not see.
func (c *collector) Live(id int64) (ServerSample, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sv, ok := findSample(c.latest, id)
	if !ok {
		return ServerSample{}, false
	}
	if t, ok := c.tps[id]; ok && t.revision == sv.Revision && sv.Up != nil && *sv.Up {
		if age := c.d.Clock.Now().Sub(t.at); age >= 0 && age <= liveFresh {
			tps, mspt := t.r.TPS, t.r.MSPT
			sv.TPS, sv.MSPT, sv.TPSSource = &tps, &mspt, t.source
		}
	}
	return sv, true
}
