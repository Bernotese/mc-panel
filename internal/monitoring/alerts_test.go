package monitoring

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var alertT0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

const aGib = int64(1) << 30

type alertEnv struct {
	t     *testing.T
	db    *sql.DB
	clk   *clock.Fake
	notes *notify.Recording
	a     *alerter
}

func newAlertEnv(t *testing.T) *alertEnv {
	t.Helper()
	db := storetest.Open(t)
	e := &alertEnv{t: t, db: db, clk: clock.NewFake(alertT0), notes: &notify.Recording{}}
	e.a = e.newAlerter(e.notes)
	return e
}

func (e *alertEnv) newAlerter(n notify.Notifier) *alerter {
	return newAlerter(Deps{DB: e.db, Clock: e.clk, Notifier: n, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func (e *alertEnv) server(name string) int64 { return storetest.CreateServer(e.t, e.db, name) }

// pass evaluates one snapshot at alertT0+off with the agent answering.
func (e *alertEnv) pass(off time.Duration, servers ...ServerSample) {
	e.a.Evaluate(context.Background(), Snapshot{At: alertT0.Add(off), Agent: true, Docker: true, Servers: servers})
}

// passHost evaluates a snapshot with only host values.
func (e *alertEnv) passHost(off time.Duration, free, total int64) {
	e.a.Evaluate(context.Background(), Snapshot{At: alertT0.Add(off), Agent: true, Docker: true,
		Host: HostSample{DiskFree: &free, DiskTotal: &total}})
}

func (e *alertEnv) events() []notify.EventType { return e.notes.Types() }

func (e *alertEnv) stateKeys() []string {
	e.t.Helper()
	rows, err := store.New(e.db).MonitoringListAlertState(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Key)
	}
	return keys
}

func (e *alertEnv) setSettings(downAfter, restarts, window int64) {
	e.t.Helper()
	if err := store.New(e.db).MonitoringUpdateSettings(context.Background(), store.MonitoringUpdateSettingsParams{
		DiskMinPercent: 10, DiskMinBytes: 10 * aGib, DownAfterMin: downAfter, CrashRestarts: restarts, CrashWindowMin: window,
		UpdatedAt: alertT0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *alertEnv) setDownAlerts(id int64, on bool) {
	e.t.Helper()
	v := int64(0)
	if on {
		v = 1
	}
	if err := store.New(e.db).MonitoringSetServerAlerts(context.Background(), store.MonitoringSetServerAlertsParams{ServerID: id, DownAlerts: v}); err != nil {
		e.t.Fatal(err)
	}
}

// sample is a server that should run and runs, started an hour before alertT0, without ping.
func aSample(id int64, name string) ServerSample {
	return ServerSample{ID: id, Name: name, DesiredRunning: true, State: agentapi.StateRunning,
		StartedAt: alertT0.Add(-time.Hour).UnixMilli()}
}

func aWithUp(s ServerSample, up bool) ServerSample { s.Up = &up; return s }

func aExited(s ServerSample) ServerSample {
	s.State, s.StartedAt, s.Up = agentapi.StateExited, 0, nil
	return s
}

func aWantEvents(t *testing.T, got []notify.EventType, want ...notify.EventType) {
	t.Helper()
	if want == nil {
		want = []notify.EventType{}
	}
	if got == nil {
		got = []notify.EventType{}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestDownFiresAfterDelay(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	s := aWithUp(aSample(id, "survival"), false)
	for m := range 3 {
		e.pass(time.Duration(m)*time.Minute, s)
	}
	aWantEvents(t, e.events())
	e.pass(3*time.Minute, s)
	e.pass(4*time.Minute, s)
	aWantEvents(t, e.events(), notify.EventServerDown)
	if ev := e.notes.Events[0]; !strings.Contains(ev.Title, "survival") || !strings.Contains(ev.Message, "3 minutes") {
		t.Fatalf("event = %+v", ev)
	}
	if got := e.stateKeys(); !slices.Equal(got, []string{alertKey(alertServerDown, id)}) {
		t.Fatalf("state = %v", got)
	}
}

func TestDownExitedContainerHasNoGrace(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	s := aExited(aSample(id, "survival"))
	e.pass(0, s)
	e.pass(2*time.Minute, s)
	aWantEvents(t, e.events())
	e.pass(3*time.Minute, s)
	aWantEvents(t, e.events(), notify.EventServerDown)
}

func TestGraceRestartsWithEveryStart(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	// the server crashes and starts again every 4 minutes: every start opens a new grace period
	for m := 0; m <= 20; m++ {
		s := aWithUp(aSample(id, "survival"), false)
		s.StartedAt = alertT0.Add(time.Duration(m/4*4) * time.Minute).UnixMilli()
		e.pass(time.Duration(m)*time.Minute, s)
	}
	aWantEvents(t, e.events())
}

func TestCrashLoopSuppressesDown(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m, c := range []int{0, 1, 2, 3} { // crash loop fires at minute 3
		e.pass(time.Duration(m)*time.Minute, aCrashing(id, c))
	}
	for m := 4; m <= 9; m++ { // then unreachable long after any grace period
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	aWantEvents(t, e.events(), notify.EventServerCrashLoop) // no extra server.down
}

func TestDownGraceAfterStart(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	s := aWithUp(aSample(id, "survival"), false)
	s.StartedAt = alertT0.UnixMilli() // just started: 5 min grace, counting starts after it
	for m := 0; m <= 7; m++ {
		e.pass(time.Duration(m)*time.Minute, s)
	}
	aWantEvents(t, e.events())
	e.pass(8*time.Minute, s)
	aWantEvents(t, e.events(), notify.EventServerDown)
}

func TestDownRecovers(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	down := aWithUp(aSample(id, "survival"), false)
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, down)
	}
	e.pass(4*time.Minute, aWithUp(aSample(id, "survival"), true))
	e.pass(5*time.Minute, aWithUp(aSample(id, "survival"), true))
	aWantEvents(t, e.events(), notify.EventServerDown, notify.EventServerRecovered)
	if got := e.stateKeys(); len(got) != 0 {
		t.Fatalf("state after recovery = %v", got)
	}
}

func TestRecoveryWithoutPingNeedsTwoMinutes(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	back := aSample(id, "survival") // Up nil: no ping configured
	back.StartedAt = alertT0.Add(4 * time.Minute).UnixMilli()
	e.pass(5*time.Minute, back)
	aWantEvents(t, e.events(), notify.EventServerDown)
	e.pass(6*time.Minute, back)
	aWantEvents(t, e.events(), notify.EventServerDown, notify.EventServerRecovered)
}

func TestGatedNeverAlerts(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	s := aExited(aSample(id, "survival"))
	s.Gated = true // restore or extract running
	for m := 0; m <= 10; m++ {
		e.pass(time.Duration(m)*time.Minute, s)
	}
	aWantEvents(t, e.events())
}

func TestDownAlertsOffSuppressesAndResolvesSilently(t *testing.T) {
	e := newAlertEnv(t)
	quiet := e.server("test")
	e.setDownAlerts(quiet, false)
	for m := 0; m <= 10; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(quiet, "test")))
	}
	aWantEvents(t, e.events())

	loud := e.server("survival")
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(loud, "survival")))
	}
	aWantEvents(t, e.events(), notify.EventServerDown)
	e.setDownAlerts(loud, false)
	e.pass(4*time.Minute, aExited(aSample(loud, "survival")))
	aWantEvents(t, e.events(), notify.EventServerDown) // no "recovered" for a switched-off alert
	if got := e.stateKeys(); len(got) != 0 {
		t.Fatalf("state = %v", got)
	}
}

func TestStoppedResolvesSilently(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	stopped := aExited(aSample(id, "survival"))
	stopped.DesiredRunning = false
	e.pass(4*time.Minute, stopped)
	aWantEvents(t, e.events(), notify.EventServerDown)
	if got := e.stateKeys(); len(got) != 0 {
		t.Fatalf("state = %v", got)
	}
}

func TestAgentMissingFreezesServerAlerts(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	e.pass(0, aExited(aSample(id, "survival")))
	for m := 1; m <= 10; m++ { // the agent does not answer: neither firing nor resolving
		e.a.Evaluate(context.Background(), Snapshot{At: alertT0.Add(time.Duration(m) * time.Minute)})
	}
	aWantEvents(t, e.events())
	for m := 11; m <= 14; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	aWantEvents(t, e.events(), notify.EventServerDown)
	e.a.Evaluate(context.Background(), Snapshot{At: alertT0.Add(20 * time.Minute)})
	if got := e.stateKeys(); len(got) != 1 {
		t.Fatalf("state after agent loss = %v, want the firing alert kept", got)
	}
}

func TestDockerUnavailableFreezesServerAlerts(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	missing := aSample(id, "survival")
	missing.State, missing.StartedAt = agentapi.StateMissing, 0
	for m := 0; m <= 10; m++ { // the agent answers, Docker does not: every server looks missing
		e.a.Evaluate(context.Background(), Snapshot{At: alertT0.Add(time.Duration(m) * time.Minute), Agent: true, Servers: []ServerSample{missing}})
	}
	aWantEvents(t, e.events())
	for m := 11; m <= 14; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	aWantEvents(t, e.events(), notify.EventServerDown)
	for m := 15; m <= 30; m++ { // a firing alert neither resolves nor repeats while Docker is gone
		e.a.Evaluate(context.Background(), Snapshot{At: alertT0.Add(time.Duration(m) * time.Minute), Agent: true, Servers: []ServerSample{missing}})
	}
	aWantEvents(t, e.events(), notify.EventServerDown)
	if got := e.stateKeys(); len(got) != 1 {
		t.Fatalf("state = %v, want the firing alert kept", got)
	}
}

func TestDownFlappingNeverFires(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	down, up := aWithUp(aSample(id, "survival"), false), aWithUp(aSample(id, "survival"), true)
	steps := []struct {
		at time.Duration
		s  ServerSample
	}{
		{0, down}, {time.Minute, down}, {2 * time.Minute, down}, {150 * time.Second, up},
		{3 * time.Minute, down}, {4 * time.Minute, down}, {5 * time.Minute, down}, {330 * time.Second, up},
	}
	for _, st := range steps {
		e.pass(st.at, st.s)
	}
	aWantEvents(t, e.events())
	for m := 6; m <= 9; m++ {
		e.pass(time.Duration(m)*time.Minute, down)
	}
	aWantEvents(t, e.events(), notify.EventServerDown) // 3 minutes of uninterrupted outage (6–9)
}

func TestStateSurvivesPanelRestart(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	aWantEvents(t, e.events(), notify.EventServerDown)

	second := &notify.Recording{}
	e.a = e.newAlerter(second) // a new panel process on the same database
	for m := 10; m <= 15; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	aWantEvents(t, second.Types())
	e.pass(16*time.Minute, aWithUp(aSample(id, "survival"), true))
	aWantEvents(t, second.Types(), notify.EventServerRecovered)
}

func TestReminderEvery24h(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	s := aExited(aSample(id, "survival"))
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, s)
	}
	fired := 3 * time.Minute
	e.pass(fired+24*time.Hour-time.Minute, s)
	aWantEvents(t, e.events(), notify.EventServerDown)
	e.pass(fired+24*time.Hour, s)
	aWantEvents(t, e.events(), notify.EventServerDown, notify.EventServerDown)
	if ev := e.notes.Events[1]; !strings.HasPrefix(ev.Title, "Server still down") {
		t.Fatalf("reminder title = %q", ev.Title)
	}
	e.pass(fired+47*time.Hour, s)
	aWantEvents(t, e.events(), notify.EventServerDown, notify.EventServerDown)
	e.pass(fired+48*time.Hour, s)
	aWantEvents(t, e.events(), notify.EventServerDown, notify.EventServerDown, notify.EventServerDown)
}

func TestSettingsAreReadEveryPass(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	e.setSettings(1, 3, 10)
	e.pass(0, aExited(aSample(id, "survival")))
	e.pass(time.Minute, aExited(aSample(id, "survival")))
	aWantEvents(t, e.events(), notify.EventServerDown)
}

func aCrashing(id int64, count int) ServerSample {
	s := aWithUp(aSample(id, "survival"), true)
	s.RestartCount = count
	return s
}

func TestCrashLoopFiresAndResolves(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m, c := range []int{0, 1, 2} {
		e.pass(time.Duration(m)*time.Minute, aCrashing(id, c))
	}
	aWantEvents(t, e.events())
	e.pass(3*time.Minute, aCrashing(id, 3))
	aWantEvents(t, e.events(), notify.EventServerCrashLoop)
	if ev := e.notes.Events[0]; !strings.Contains(ev.Message, "restarted 3 times") {
		t.Fatalf("message = %q", ev.Message)
	}
	for m := 4; m <= 12; m++ {
		e.pass(time.Duration(m)*time.Minute, aCrashing(id, 3))
	}
	aWantEvents(t, e.events(), notify.EventServerCrashLoop) // the 2→3 restart is still inside the window
	e.pass(13*time.Minute, aCrashing(id, 3))
	aWantEvents(t, e.events(), notify.EventServerCrashLoop, notify.EventServerRecovered)
	if ev := e.notes.Events[1]; !strings.HasPrefix(ev.Title, "Server stable again") {
		t.Fatalf("title = %q", ev.Title)
	}
}

func TestCrashCounterResetIsNotARestart(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m, c := range []int{5, 0, 1, 2} { // 5 → 0: container recreated
		e.pass(time.Duration(m)*time.Minute, aCrashing(id, c))
	}
	aWantEvents(t, e.events())
	e.pass(4*time.Minute, aCrashing(id, 3))
	aWantEvents(t, e.events(), notify.EventServerCrashLoop)
}

func TestDeletedServerDropsAlertsSilently(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	e.pass(4 * time.Minute) // the server is gone from the snapshot
	aWantEvents(t, e.events(), notify.EventServerDown)
	if got := e.stateKeys(); len(got) != 0 {
		t.Fatalf("state = %v", got)
	}
	if got := e.a.Firing(context.Background()); len(got) != 0 {
		t.Fatalf("firing = %+v", got)
	}
}

func TestDiskLowWithHysteresis(t *testing.T) {
	e := newAlertEnv(t)
	e.passHost(0, 9*aGib, 100*aGib) // below 10 GiB
	e.passHost(time.Minute, 11*aGib, 100*aGib)
	aWantEvents(t, e.events(), notify.EventHostDiskLow)
	e.passHost(2*time.Minute, 12*aGib, 100*aGib) // 12 GiB and 12 %: both margins reached
	aWantEvents(t, e.events(), notify.EventHostDiskLow, notify.EventHostDiskOK)
	if ev := e.notes.Events[0]; strings.Contains(ev.Message, "/") {
		t.Fatalf("disk message carries a path: %q", ev.Message)
	}
}

func TestDiskLowByPercent(t *testing.T) {
	e := newAlertEnv(t)
	e.passHost(0, 50*aGib, 1000*aGib) // 5 % < 10 %, although > 10 GiB
	aWantEvents(t, e.events(), notify.EventHostDiskLow)
}

func TestDiskWithoutValues(t *testing.T) {
	e := newAlertEnv(t)
	e.a.Evaluate(context.Background(), Snapshot{At: alertT0, Agent: true})
	aWantEvents(t, e.events())
}

func TestFiringListsKindsNamesAndSince(t *testing.T) {
	e := newAlertEnv(t)
	id := e.server("survival")
	e.passHost(0, aGib, 100*aGib)
	for m := 0; m <= 3; m++ {
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(id, "survival")))
	}
	e.a = e.newAlerter(&notify.Recording{}) // names come from the database after a restart
	got := e.a.Firing(context.Background())
	if len(got) != 2 {
		t.Fatalf("firing = %+v", got)
	}
	if got[0].Kind != alertDiskLow || got[0].ServerID != 0 || !got[0].Since.Equal(alertT0) {
		t.Fatalf("disk = %+v", got[0])
	}
	if got[1].Kind != alertServerDown || got[1].ServerID != id || got[1].ServerName != "survival" ||
		!got[1].Since.Equal(alertT0.Add(3*time.Minute)) {
		t.Fatalf("down = %+v", got[1])
	}
}

// aDeleteServer deletes a server the way internal/servers does: the row and its alert_state.
func (e *alertEnv) aDeleteServer(id int64) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := e.db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", id); err != nil {
		e.t.Fatal(err)
	}
	for _, kind := range []string{alertServerDown, alertCrashLoop} {
		if err := store.New(e.db).MonitoringDeleteAlertState(ctx, alertKey(kind, id)); err != nil {
			e.t.Fatal(err)
		}
	}
}

// A pass that started before a server was deleted still contains it: the alerter neither
// fires for it nor keeps (or writes back) any state for it (forgetMissing).
func TestPassWithDeletedServerLeavesNoState(t *testing.T) {
	e := newAlertEnv(t)
	firing := e.server("survival")
	counting := e.server("creative")
	for m := 0; m <= 3; m++ {
		s := aExited(aSample(counting, "creative"))
		if m == 3 {
			s = aWithUp(aSample(counting, "creative"), true) // keeps creative's outage short
		}
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(firing, "survival")), s)
	}
	for m := 4; m <= 5; m++ { // creative is down again since minute 4: minute 7 would fire
		e.pass(time.Duration(m)*time.Minute, aExited(aSample(firing, "survival")), aExited(aSample(counting, "creative")))
	}
	aWantEvents(t, e.events(), notify.EventServerDown)

	e.aDeleteServer(firing)
	e.aDeleteServer(counting)
	stale := []ServerSample{aExited(aSample(firing, "survival")), aExited(aCrashing(counting, 9))}
	for m := 6; m <= 8; m++ { // stale passes: both servers are still in the snapshot
		e.pass(time.Duration(m)*time.Minute, stale...)
	}
	aWantEvents(t, e.events(), notify.EventServerDown)
	if got := e.stateKeys(); len(got) != 0 {
		t.Fatalf("state = %v", got)
	}
	if got := e.a.Firing(context.Background()); len(got) != 0 {
		t.Fatalf("firing = %+v", got)
	}
	e.a.mu.Lock()
	defer e.a.mu.Unlock()
	if len(e.a.downSince) != 0 || len(e.a.restarts) != 0 || len(e.a.names) != 0 {
		t.Fatalf("memory state left: down %v restarts %v names %v", e.a.downSince, e.a.restarts, e.a.names)
	}
}
