package monitoring

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Alert kinds (alertServerDown, alertCrashLoop, alertDiskLow), alertKey, startGrace and
// remindEvery come from types.go (phase A).

const (
	noPingStable      = 2 * time.Minute // without ping, "running that long" counts as recovered
	diskHystPoints    = 2               // percentage points above the threshold to clear host.disk_low
	diskHystBytes     = int64(2) << 30  // and bytes above disk_min_bytes
	maxRestartSamples = 512             // per server (120 min window / 30 s passes = 240)
)

var kindEvent = map[string]notify.EventType{
	alertServerDown: notify.EventServerDown,
	alertCrashLoop:  notify.EventServerCrashLoop,
	alertDiskLow:    notify.EventHostDiskLow,
}

type alertSettings struct {
	diskMinPercent int64
	diskMinBytes   int64
	downAfter      time.Duration
	crashRestarts  int
	crashWindow    time.Duration
}

type firingState struct {
	since, lastNotified time.Time
	detail              string
}

type restartSample struct {
	at    time.Time
	count int
}

// alerter evaluates every collector pass (spec §5). The firing alerts live in memory and in
// alert_state, so a panel restart neither repeats nor forgets them; outage counters and the
// restart history are memory only. All methods are safe for concurrent use.
type alerter struct {
	q        *store.Queries
	clock    clock.Clock
	notifier notify.Notifier
	log      *slog.Logger

	mu        sync.Mutex
	loaded    bool
	firing    map[string]firingState
	downSince map[int64]time.Time
	restarts  map[int64][]restartSample
	names     map[int64]string
}

func newAlerter(d Deps) *alerter {
	n := d.Notifier
	if n == nil {
		n = notify.Nop{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &alerter{
		q: store.New(d.DB), clock: d.Clock, notifier: n, log: log,
		firing: map[string]firingState{}, downSince: map[int64]time.Time{},
		restarts: map[int64][]restartSample{}, names: map[int64]string{},
	}
}

// parseKey splits "server.down:7" into kind and server id; host keys have id 0.
func parseKey(key string) (string, int64) {
	kind, rest, ok := strings.Cut(key, ":")
	if !ok {
		return key, 0
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return kind, 0
	}
	return kind, id
}

// Evaluate applies the rules to one pass. Without an agent answer or without Docker (the agent
// then reports every server as missing, V5) the server rules are skipped entirely: nothing
// fires, nothing resolves. Host rules always run.
func (a *alerter) Evaluate(ctx context.Context, s Snapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadLocked(ctx); err != nil {
		a.log.ErrorContext(ctx, "monitoring: load alert state", "err", err)
		return
	}
	set, err := a.settings(ctx)
	if err != nil {
		a.log.ErrorContext(ctx, "monitoring: load alert settings", "err", err)
		return
	}
	now := s.At
	if now.IsZero() {
		now = a.clock.Now()
	}
	serversKnown := s.Agent && s.Docker
	if serversKnown {
		if err := a.evalServers(ctx, now, set, s.Servers); err != nil {
			a.log.ErrorContext(ctx, "monitoring: evaluate server alerts", "err", err)
		}
	}
	a.evalDisk(ctx, now, set, s.Host)
	gated := map[int64]bool{}
	for _, srv := range s.Servers {
		if srv.Gated {
			gated[srv.ID] = true
		}
	}
	a.remind(ctx, now, serversKnown, gated)
}

// evalServers applies the server rules. A server deleted while the pass ran is still in the
// snapshot but no longer in the database (its alert_state rows went with it): it is treated
// like a server missing from the pass, so it neither fires nor leaves state behind.
//
// Remaining race: a server deleted between RbacListServers and the alert_state write below can
// still fire once and leave an orphaned row. The next pass no longer lists the server, so
// forgetMissing drops the row and the memory state; server IDs are never reused (server_ids),
// so the orphan can never be attributed to a new server.
func (a *alerter) evalServers(ctx context.Context, now time.Time, set alertSettings, samples []ServerSample) error {
	off, err := a.downAlertsOff(ctx)
	if err != nil {
		return err
	}
	list, err := a.q.RbacListServers(ctx)
	if err != nil {
		return err
	}
	exists := make(map[int64]bool, len(list))
	for _, srv := range list {
		exists[srv.ID] = true
	}
	seen := make(map[int64]bool, len(samples))
	for _, srv := range samples {
		if !exists[srv.ID] {
			continue
		}
		seen[srv.ID] = true
		a.names[srv.ID] = srv.Name
		a.evalCrash(ctx, now, set, srv, off[srv.ID])
		a.evalDown(ctx, now, set, srv, off[srv.ID])
	}
	a.forgetMissing(ctx, seen)
	return nil
}

func (a *alerter) loadLocked(ctx context.Context) error {
	if a.loaded {
		return nil
	}
	rows, err := a.q.MonitoringListAlertState(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Firing != 0 {
			a.firing[r.Key] = firingState{since: time.UnixMilli(r.Since), lastNotified: time.UnixMilli(r.LastNotified), detail: r.Detail}
		}
	}
	a.loaded = true
	return nil
}

func (a *alerter) settings(ctx context.Context) (alertSettings, error) {
	r, err := a.q.MonitoringGetSettings(ctx)
	if err != nil {
		return alertSettings{}, err
	}
	return alertSettings{
		diskMinPercent: r.DiskMinPercent, diskMinBytes: r.DiskMinBytes,
		downAfter: time.Duration(r.DownAfterMin) * time.Minute, crashRestarts: int(r.CrashRestarts),
		crashWindow: time.Duration(r.CrashWindowMin) * time.Minute,
	}, nil
}

// downAlertsOff returns the servers whose down alerts are switched off (no row = on).
func (a *alerter) downAlertsOff(ctx context.Context) (map[int64]bool, error) {
	rows, err := a.q.MonitoringListServerAlerts(ctx)
	if err != nil {
		return nil, err
	}
	off := map[int64]bool{}
	for _, r := range rows {
		if r.DownAlerts == 0 {
			off[r.ServerID] = true
		}
	}
	return off, nil
}

func (a *alerter) evalDown(ctx context.Context, now time.Time, set alertSettings, srv ServerSample, off bool) {
	key := alertKey(alertServerDown, srv.ID)
	if !srv.DesiredRunning || off {
		delete(a.downSince, srv.ID)
		a.dropLocked(ctx, key) // stopped on purpose or switched off: no "recovered"
		return
	}
	if srv.Gated {
		delete(a.downSince, srv.ID) // a restore or extract stops the server on purpose
		return
	}
	running := srv.State == agentapi.StateRunning
	started := time.UnixMilli(srv.StartedAt)
	reachable := running && (srv.Up != nil && *srv.Up ||
		srv.Up == nil && srv.StartedAt > 0 && now.Sub(started) >= noPingStable)
	if reachable {
		delete(a.downSince, srv.ID)
		if _, ok := a.firing[key]; ok {
			a.resolveLocked(ctx, key, now, notify.Event{Type: notify.EventServerRecovered,
				Title: "Server back online: " + srv.Name, Message: srv.Name + " is reachable again."})
		}
		return
	}
	if srv.StartedAt > 0 && now.Sub(started) < startGrace {
		// every (re)start opens a new grace period: the count starts again afterwards, also for a
		// start without ping that is not yet noPingStable old
		delete(a.downSince, srv.ID)
		return
	}
	// Known limit: an image pull or container recreation that takes longer than down_after_min
	// counts as down (the container is missing meanwhile). A later agent operation state could
	// act as a gate like restore and extract.
	down := !running || srv.Up != nil && !*srv.Up // restarting and exited count as down
	if !down {
		return // running without ping, started less than noPingStable ago
	}
	if _, crashing := a.firing[alertKey(alertCrashLoop, srv.ID)]; crashing {
		delete(a.downSince, srv.ID) // a crash loop is already reported: no extra "down" (V5)
		return
	}
	first, ok := a.downSince[srv.ID]
	if !ok {
		a.downSince[srv.ID], first = now, now
	}
	if _, firing := a.firing[key]; firing || now.Sub(first) < set.downAfter {
		return
	}
	msg := fmt.Sprintf("%s should be running but has not been reachable for %d minutes.", srv.Name, int(set.downAfter/time.Minute))
	a.fireLocked(ctx, key, now, msg, notify.Event{Type: notify.EventServerDown, Title: "Server down: " + srv.Name, Message: msg})
}

func (a *alerter) evalCrash(ctx context.Context, now time.Time, set alertSettings, srv ServerSample, off bool) {
	key := alertKey(alertCrashLoop, srv.ID)
	if !srv.DesiredRunning || off {
		delete(a.restarts, srv.ID)
		a.dropLocked(ctx, key)
		return
	}
	hist := a.restarts[srv.ID]
	if n := len(hist); n > 0 && srv.RestartCount < hist[n-1].count {
		hist = nil // the container was recreated: Docker's counter starts at 0 again
	}
	hist = append(hist, restartSample{at: now, count: srv.RestartCount})
	cut := now.Add(-set.crashWindow)
	i := 0
	for i+1 < len(hist) && !hist[i+1].at.After(cut) {
		i++ // keep the newest sample at or before the window start as the base
	}
	hist = hist[i:]
	if len(hist) > maxRestartSamples {
		hist = hist[len(hist)-maxRestartSamples:]
	}
	a.restarts[srv.ID] = hist
	delta := srv.RestartCount - hist[0].count
	_, firing := a.firing[key]
	switch {
	case !firing && delta >= set.crashRestarts:
		msg := fmt.Sprintf("%s restarted %d times in the last %d minutes.", srv.Name, delta, int(set.crashWindow/time.Minute))
		a.fireLocked(ctx, key, now, msg, notify.Event{Type: notify.EventServerCrashLoop, Title: "Crash loop: " + srv.Name, Message: msg})
	case firing && delta == 0 && !hist[0].at.After(cut):
		a.resolveLocked(ctx, key, now, notify.Event{Type: notify.EventServerRecovered,
			Title: "Server stable again: " + srv.Name, Message: fmt.Sprintf("%s has not restarted for %d minutes.", srv.Name, int(set.crashWindow/time.Minute))})
	}
}

func (a *alerter) evalDisk(ctx context.Context, now time.Time, set alertSettings, h HostSample) {
	if h.DiskFree == nil || h.DiskTotal == nil || *h.DiskTotal <= 0 {
		return
	}
	free, total := *h.DiskFree, *h.DiskTotal
	msg := fmt.Sprintf("%.1f GiB free of %.1f GiB (%d %%) on the file system of the server data.",
		float64(free)/(1<<30), float64(total)/(1<<30), free*100/total)
	if _, firing := a.firing[alertDiskLow]; !firing {
		if free < set.diskMinBytes || free*100 < total*set.diskMinPercent {
			a.fireLocked(ctx, alertDiskLow, now, msg, notify.Event{Type: notify.EventHostDiskLow, Title: "Low disk space on the host", Message: msg})
		}
		return
	}
	if free >= set.diskMinBytes+diskHystBytes && free*100 >= total*(set.diskMinPercent+diskHystPoints) {
		a.resolveLocked(ctx, alertDiskLow, now, notify.Event{Type: notify.EventHostDiskOK, Title: "Disk space OK again", Message: msg})
	}
}

// remind repeats every alert that has been firing for remindEvery since its last message.
// Server alerts are only repeated in passes that know the servers (agent and Docker answered)
// and not while the server is gated; host alerts always.
func (a *alerter) remind(ctx context.Context, now time.Time, serversKnown bool, gated map[int64]bool) {
	for _, key := range slices.Sorted(maps.Keys(a.firing)) {
		st := a.firing[key]
		if now.Sub(st.lastNotified) < remindEvery {
			continue
		}
		kind, id := parseKey(key)
		if id != 0 && (!serversKnown || gated[id]) {
			continue
		}
		ev := notify.Event{Type: kindEvent[kind], Message: fmt.Sprintf("Active since %s. %s", st.since.UTC().Format(time.RFC3339), st.detail)}
		switch kind {
		case alertServerDown:
			ev.Title = "Server still down: " + a.nameLocked(ctx, id)
		case alertCrashLoop:
			ev.Title = "Server still in a crash loop: " + a.nameLocked(ctx, id)
		default:
			ev.Title = "Disk space still low"
		}
		st.lastNotified = now
		if err := a.save(ctx, key, st); err != nil {
			a.log.ErrorContext(ctx, "monitoring: save alert reminder", "key", key, "err", err)
			continue
		}
		a.firing[key] = st
		a.send(ctx, now, ev)
	}
}

// forgetMissing drops the state of servers that are not in the pass or no longer in the
// database (deleted servers; their alert_state rows are already gone with the server, spec §5).
func (a *alerter) forgetMissing(ctx context.Context, seen map[int64]bool) {
	for key := range a.firing {
		if kind, id := parseKey(key); id != 0 && !seen[id] && (kind == alertServerDown || kind == alertCrashLoop) {
			a.dropLocked(ctx, key)
		}
	}
	for id := range a.downSince {
		if !seen[id] {
			delete(a.downSince, id)
		}
	}
	for id := range a.restarts {
		if !seen[id] {
			delete(a.restarts, id)
		}
	}
	for id := range a.names {
		if !seen[id] {
			delete(a.names, id)
		}
	}
}

// fireLocked stores the alert first: if that fails, it is retried on the next pass.
func (a *alerter) fireLocked(ctx context.Context, key string, now time.Time, detail string, ev notify.Event) {
	st := firingState{since: now, lastNotified: now, detail: detail}
	if err := a.save(ctx, key, st); err != nil {
		a.log.ErrorContext(ctx, "monitoring: save alert", "key", key, "err", err)
		return
	}
	a.firing[key] = st
	a.send(ctx, now, ev)
}

func (a *alerter) resolveLocked(ctx context.Context, key string, now time.Time, ev notify.Event) {
	if err := a.q.MonitoringDeleteAlertState(context.WithoutCancel(ctx), key); err != nil {
		a.log.ErrorContext(ctx, "monitoring: clear alert", "key", key, "err", err)
		return
	}
	delete(a.firing, key)
	a.send(ctx, now, ev)
}

// dropLocked clears a firing alert without a message.
func (a *alerter) dropLocked(ctx context.Context, key string) {
	if _, ok := a.firing[key]; !ok {
		return
	}
	if err := a.q.MonitoringDeleteAlertState(context.WithoutCancel(ctx), key); err != nil {
		a.log.ErrorContext(ctx, "monitoring: drop alert", "key", key, "err", err)
		return
	}
	delete(a.firing, key)
}

func (a *alerter) save(ctx context.Context, key string, st firingState) error {
	return a.q.MonitoringUpsertAlertState(context.WithoutCancel(ctx), store.MonitoringUpsertAlertStateParams{
		Key: key, Firing: 1, Since: st.since.UnixMilli(), LastNotified: st.lastNotified.UnixMilli(), Detail: st.detail,
	})
}

func (a *alerter) send(ctx context.Context, now time.Time, ev notify.Event) {
	ev.Time = now
	a.notifier.Notify(context.WithoutCancel(ctx), ev)
}

// nameLocked returns a server's name from the last passes or the database ("" if deleted).
func (a *alerter) nameLocked(ctx context.Context, id int64) string {
	if n, ok := a.names[id]; ok {
		return n
	}
	s, err := a.q.RbacGetServer(ctx, id)
	if err != nil {
		return ""
	}
	a.names[id] = s.Name
	return s.Name
}

// Firing lists the firing alerts, sorted by key (host before servers).
func (a *alerter) Firing(ctx context.Context) []FiringAlert {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.loadLocked(ctx); err != nil {
		a.log.ErrorContext(ctx, "monitoring: load alert state", "err", err)
		return nil
	}
	out := make([]FiringAlert, 0, len(a.firing))
	for _, key := range slices.Sorted(maps.Keys(a.firing)) {
		st := a.firing[key]
		kind, id := parseKey(key)
		fa := FiringAlert{Kind: kind, ServerID: id, Since: st.since, Detail: st.detail}
		if id != 0 {
			fa.ServerName = a.nameLocked(ctx, id)
		}
		out = append(out, fa)
	}
	return out
}
