# mc-panel Teilprojekt 5: Monitoring + Alerts – Phase B2: Alerts und API (Aufgaben 8–10)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die Alert-Auswertung (`server.down`/`server.recovered`, `server.crash_loop`, `host.disk_low`/`host.disk_ok` mit persistentem Zustand, Schonfrist, Daten-Sperre, Hysterese und 24-h-Erinnerung), die Lese-Schicht für Verläufe und Sparklines und alle sieben Monitoring-Routen bauen und mit einem Gesamttest gegen den echten Sammler absichern.

**Architecture:** Alles im Paket `internal/monitoring`. `alerts.go` ersetzt den Stub aus Phase A: `alerter.Evaluate` bekommt nach jedem Sammel-Durchlauf einen `Snapshot` (V4), hält den feuernden Zustand im Speicher und in `alert_state`, liest Schwellen und Server-Schalter pro Durchlauf aus der DB und meldet über `notify.Notifier`. `read.go` liest `metrics_raw`/`metrics_5m`/`metrics_1h` je nach Bereich. `api.go` und `overview.go` registrieren die Routen über einen kleinen Typ `apiHandlers`, der den Sammler nur über das Interface `sampler` sieht – so lassen sich die Routen ohne echten Sammler testen. `e2e_test.go` startet `NewHandler` + `Run` mit den gemeinsamen Fakes aus `helpers_test.go` (`fakeAgent`, `fakeConsole`, `pingServer`).

**Tech Stack:** Go (Standardbibliothek, chi, `modernc.org/sqlite` über `store`), `internal/rbac`, `internal/notify`, `internal/audit`, `internal/clock`.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md` (§5 Alerts, §6 API). **Übersicht/Verträge:** `docs/superpowers/plans/2026-10-13-monitoring-00-overview.md` (V2–V5, Global Constraints, Dateieigentum B2).

## Ausführung dieser Phase

- Branch `feat/monitoring-alerts-api`, Worktree `git worktree add ../mc-panel-wt/feat/monitoring-alerts-api -b feat/monitoring-alerts-api origin/main` (nach dem Merge von Phase A).
- Auf dem Mac nur `make check` (neue Dateien vorher `git add -N`); Tests per `make test-vm SUITE=go` bzw. einzeln:
  `ssh claude@192.168.1.117 'cd ~/mc-panel-test/monitoring-alerts-api && PATH=/usr/local/go/bin:$PATH go test -race ./internal/monitoring -run TestName'` (SSH ohne Sandbox; lange Läufe detached starten und das Log abfragen).
- Dateien dieser Phase (nur diese): `internal/monitoring/alerts.go`, `alerts_test.go`, `read.go`, `read_test.go`, `api.go`, `api_test.go`, `overview.go`, `overview_test.go`, `e2e_test.go`. `types.go`, `handler.go`, `helpers_test.go` sind ab Phase A fest (Bedarf → Lead).
- **Aufgaben 8 und 9** sind unabhängig vom Sammler (B1) und laufen sofort. **Aufgabe 10** (Gesamttest) braucht den echten Sammler: Sie beginnt, wenn B1 auf `main` ist (`git merge origin/main`). Bis dahin bleibt der PR Draft mit dem Hinweis „Aufgabe 10 wartet auf B1“.
- Jede Commit-Nachricht endet mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`.

## Global Constraints

Alle aus der Übersicht. Für diese Phase besonders:

- Rechte: `GET /servers/{id}/metrics` und `GET /servers/{id}/metrics/live` → `rbac.Server(rbac.PermServerView, "id")`; `GET /monitoring/overview` → `rbac.Authenticated()`; `GET /monitoring/host`, `GET /monitoring/settings`, `PUT /servers/{id}/alerts` → `rbac.Admin()`; `PUT /monitoring/settings` → `rbac.Admin().WithSudo()`.
- Alert-Einstellungen (Grenzen): `disk_min_percent` 1–50, `disk_min_bytes` 1 GiB–10 TiB, `down_after_min` 1–60, `crash_restarts` 2–20, `crash_window_min` 1–120. Schonfrist nach Start 5 min; Erinnerung alle 24 h; Plattenhysterese 2 Prozentpunkte **und** 2 GiB.
- `step_ms`: 30 000 (1h), 300 000 (24h), 3 600 000 (7d, 30d); für `1h` gilt `min = max = avg`. `sparkline` höchstens 120 Punkte.
- Notify-Ereignisse: `notify.EventServerDown`, `EventServerRecovered`, `EventServerCrashLoop`, `EventHostDiskLow`, `EventHostDiskOK` (V1). Audit: `audit.ActionMonitoringSettingsUpdate` (Ziel `monitoring`), `audit.ActionServerAlertsUpdate` (Ziel `server:<id>`).
- Meldungen enthalten nie Host-Pfade (der Plattentext nennt nur Größen).
- Alles Zeitabhängige über `clock.Clock` bzw. `Snapshot.At`; Go-Tests nur Standardbibliothek.

## Review Focus (in dieser Phase)

3. **Alert-Flattern und Neustarts** (Übersicht Punkt 3): pendelnder Server, panel-Neustart mit feuerndem Alert, Erinnerungszeitpunkt, gelöschter/gestoppter Server, `restart_count`-Reset → Tests `TestDownFlappingNeverFires`, `TestStateSurvivesPanelRestart`, `TestReminderEvery24h`, `TestDeletedServerDropsAlertsSilently`, `TestStoppedResolvesSilently`, `TestCrashCounterResetIsNotARestart` (Aufgabe 8).
5. **Rechte und Datenmenge** (Übersicht Punkt 5): `overview` nur mit `server.view`, Host nur für Admins, 30-d-Abfrage ≤ 721 Punkte → `TestOverviewFiltersByPermission`, `TestRouteRules`, `TestThirtyDaysAtMost721Points` (Aufgabe 9).

Zusätzlich aus Punkt 2: kein Down-Alert während Schonfrist (`TestDownGraceAfterStart`), Daten-Sperre (`TestGatedNeverAlerts`), ohne agent (`TestAgentMissingFreezesServerAlerts`) oder ohne Docker (`TestDockerUnavailableFreezesServerAlerts`).

## Annahmen und Präzisierungen

1. **sqlc-Namen (V2):** Der Plan nimmt die von sqlc für die Queries aus V2 erzeugten Namen an: `store.MonitoringSetting` (`ID, DiskMinPercent, DiskMinBytes, DownAfterMin, CrashRestarts, CrashWindowMin, UpdatedAt int64`), `store.AlertState` (`Key string, Firing, Since, LastNotified int64, Detail string`), `store.ServerAlert` (`ServerID, DownAlerts int64`), `store.MonitoringListRawParams{Series, FromAt, ToAt int64}` (ebenso `…5m…`, `…1h…`; die Listen-Queries benennen ihre Grenzen per `sqlc.arg(from_at)`/`sqlc.arg(to_at)`), Zeilen `MonitoringListRawRow{Metric string; At int64; Value float64}` und `MonitoringList5mRow`/`MonitoringList1hRow{Metric string; At int64; Avg, Min, Max float64}`, `MonitoringUpsertAlertStateParams{Key, Firing, Since, LastNotified, Detail}`, `MonitoringUpdateSettingsParams{DiskMinPercent, DiskMinBytes, DownAfterMin, CrashRestarts, CrashWindowMin, UpdatedAt}`, `MonitoringSetServerAlertsParams{ServerID, DownAlerts}`, `MonitoringInsertRawParams{Series, Metric, At, Value}`. **Step 0 von Aufgabe 8** gleicht sie mit `internal/store/monitoring.sql.go` ab; Abweichungen werden im Code übernommen und im PR vermerkt.
2. **Test-Helfer (Lead-Vorgabe):** Phase A legt `helpers_test.go` mit `fakeAgent`, `fakeConsole` (inkl. `peak(id)`), `pingServer`, `statusJSON`, `env`/`newEnv`, `eventually` an. Diese Phase nutzt sie, statt eigene Nachbauten zu schreiben: Der Gesamttest (Aufgabe 10) nutzt `fakeAgent`, `fakeConsole` und `pingServer` (Code unten); passt die Fake-API beim Abgleich nicht genau, gleicht der Teammate die Aufrufe an `helpers_test.go` an. Alle eigenen Testhelfer dieser Phase tragen das Präfix `a` (`alertEnv`, `apiEnv`, `aSample`, `aWithUp`, `aExited`, `aCrashing`, `aWantEvents`, `aGib`, `aInsertRaw`, `aSampler`, `aFiring`, `aPtr`, `aLiveRunning`, `aReadRoutes`), B1 nutzt `c`. Nach dem Streichen der Einstellungs-Tests ungenutzte Helfer (z. B. `sudoAdmin`, `apiAudit`) entfernt der Teammate, damit `unused` nicht anschlägt.
10. **Einstellungs-Routen (Lead-Vorgabe):** `settings.go`, `settings_test.go` und `routes_test.go` sind seit Phase A fest. `api.go` dieser Phase registriert die vier Lese-Routen über `apiHandlers.register` und die drei Einstellungs-Routen mit den Handlern aus `settings.go`; keine eigenen Settings-Typen, -Handler, -Audits oder -Tests.
3. **Crash-Loop beim Lösen:** Die Spec nennt für das Lösen eines Crash-Loops kein eigenes Ereignis. Der Plan sendet `server.recovered` mit dem Titel „Server stable again: <name>“.
4. **Neustart-Verlauf im Speicher:** `restart_count` ist keine gespeicherte Metrik (Spec §4); die Crash-Loop-Erkennung hält die letzten Werte pro Server im Speicher (höchstens 512). Nach einem panel-Neustart beginnt das Fenster neu; ein schon feuernder Crash-Loop bleibt feuernd (Zustand in `alert_state`) und löst sich erst nach einem vollen Fenster ohne Neustart.
5. **Gesamttest (Aufgabe 10):** setzt voraus, dass `collector.Run` den ersten Durchlauf **sofort** beim Start macht (nicht erst nach 30 s) – sonst bräuchte der Test 30 s Wartezeit. Hält B1 das nicht ein, meldet der Teammate das dem Lead (Hook statt Annahme).
6. **Metrik-Konstanten (Lead-Vorgabe):** Metriknamen und TPS-Quellen kommen aus `types.go` (Phase A): `HostSeries` (Serie des Hosts, 0), `MetricUp`, `MetricPlayers`, `MetricPlayersMax`, `MetricTPS`, `MetricMSPT`, `MetricCPU`, `MetricMemory`, `MetricMemoryLimit`, `MetricMemUsed`, `MetricMemTotal`, `MetricLoad1`, `MetricDiskFree`, `MetricDiskTotal` sowie `TPSSourceTickQuery`, `TPSSourcePaper`, `TPSSourceNone`, `TPSSourceUnknown`. Step 0 von Aufgabe 8 gleicht die genauen Namen ab; dieser Plan nutzt nur sie, nie String-Literale.
7. **Docker nicht verfügbar (V5, Lead-Vorgabe):** Ist `Snapshot.Docker == false` (agent antwortet, Docker nicht), liefert B1 die Server mit `State = missing`. B2 behandelt das wie `Agent == false`: keine Server-Regeln, nichts feuert, nichts löst sich, bestehende Alerts bleiben (`TestDockerUnavailableFreezesServerAlerts`).
8. **TPS bei Paper (Lead-Vorgabe):** Der Fallback ist nur `mspt` (kein `tps`); das betrifft B1, der Gesamttest nutzt `tick query`.
9. **Schonfrist und Zähler (Lead-Vorgabe, V5):** `restarting` und `exited` zählen als nicht erreichbar. Jeder (Neu-)Start (`started_at` jünger als 5 min) öffnet eine neue Schonfrist und setzt den Down-Zähler zurück. Solange `server.crash_loop` für den Server feuert, wird `server.down` nicht zusätzlich gemeldet (Zähler ruht). Tests: `TestGraceRestartsWithEveryStart`, `TestCrashLoopSuppressesDown`.

## Dateistruktur

```
internal/monitoring/alerts.go        (8)  alerter: Regeln, Zustand, Erinnerung, Versand, Firing
internal/monitoring/alerts_test.go   (8)  Regeln mit handgebauten Snapshots
internal/monitoring/read.go          (9)  Bereiche, Verlauf aus raw/5m/1h, Sparkline
internal/monitoring/read_test.go     (9)
internal/monitoring/api.go           (9)  apiHandlers, sampler, Routen metrics/live/host/settings/alerts
internal/monitoring/overview.go      (9)  GET /monitoring/overview
internal/monitoring/api_test.go      (9)  Routen-Vertrag, Rechte, Validierung, Audit
internal/monitoring/overview_test.go (9)  Filter, Host, Alerts, Sparkline
internal/monitoring/e2e_test.go      (10) NewHandler + Run mit Fakes und SLP-Fake-Server
```

---

### Aufgabe 8: Alerts – Regeln, Zustand, Versand

**Files:**
- Create (ersetzt Stub): `internal/monitoring/alerts.go`
- Test: `internal/monitoring/alerts_test.go`

**Interfaces:**
- Consumes: `Deps`, `Snapshot`, `ServerSample`, `HostSample`, `FiringAlert` (V4, `types.go`); `store.Queries` mit `MonitoringListAlertState`, `MonitoringUpsertAlertState`, `MonitoringDeleteAlertState`, `MonitoringGetSettings`, `MonitoringListServerAlerts`, `RbacGetServer` (V2); `notify.Event*` (V1).
- Produces (V4): `func newAlerter(d Deps) *alerter`, `func (a *alerter) Evaluate(ctx context.Context, s Snapshot)`, `func (a *alerter) Firing(ctx context.Context) []FiringAlert`. Paket-intern zusätzlich `alertServerDown = "server.down"`, `alertCrashLoop = "server.crash_loop"`, `alertDiskLow = "host.disk_low"` (Aufgabe 9 nutzt sie).

- [ ] **Step 0: Namen abgleichen**

```bash
grep -n "^type \(MonitoringSetting\|AlertState\|ServerAlert\|Monitoring[A-Za-z0-9]*\(Params\|Row\)\) " internal/store/monitoring.sql.go
grep -n "func newAlerter\|func (a \*alerter)" internal/monitoring/*.go
grep -n "^func \|^type " internal/monitoring/helpers_test.go
grep -n "HostSeries\|Metric[A-Z][A-Za-z]* *=\|TPSSource[A-Z][A-Za-z]* *=" internal/monitoring/types.go
```

Erwartet: die Typen aus „Annahmen 1“, die Konstanten aus „Annahmen 6“, der Stub `newAlerter`/`Evaluate`/`Firing` in `alerts.go`, keine Helfer namens `alertEnv`, `apiEnv`, `sample`, `withUp`. Abweichungen notieren und in den folgenden Schritten übernehmen.

- [ ] **Step 1: Tests schreiben (rot)**

`internal/monitoring/alerts_test.go`:

```go
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

func aExited(s ServerSample) ServerSample { s.State, s.StartedAt, s.Up = agentapi.StateExited, 0, nil; return s }

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
```

- [ ] **Step 2: Rot prüfen**

Run: `make check` – Kompilierfehler (`alerter`-Felder und -Methoden wie `evalDown`, `evalCrash`, `downSince` fehlen); danach `make test-vm SUITE=go` bzw. `go test ./internal/monitoring -run 'Test(Down|Recovery|Gated|Stopped|Agent|Docker|State|Reminder|Settings|Crash|Deleted|Disk|Firing)'` – FAIL.

- [ ] **Step 3: Implementieren**

`internal/monitoring/alerts.go` (ersetzt den Stub vollständig):

```go
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
	noPingStable      = 2 * time.Minute  // without ping, "running that long" counts as recovered
	diskHystPoints    = 2                // percentage points above the threshold to clear host.disk_low
	diskHystBytes     = int64(2) << 30   // and bytes above disk_min_bytes
	maxRestartSamples = 512              // per server (120 min window / 30 s passes = 240)
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
	if s.Agent && s.Docker {
		off, err := a.downAlertsOff(ctx)
		if err != nil {
			a.log.ErrorContext(ctx, "monitoring: load server alert switches", "err", err)
			return
		}
		seen := make(map[int64]bool, len(s.Servers))
		for _, srv := range s.Servers {
			seen[srv.ID] = true
			a.names[srv.ID] = srv.Name
			a.evalCrash(ctx, now, set, srv, off[srv.ID])
			a.evalDown(ctx, now, set, srv, off[srv.ID])
		}
		a.forgetMissing(ctx, seen)
	}
	a.evalDisk(ctx, now, set, s.Host)
	a.remind(ctx, now)
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
	down := !running || srv.Up != nil && !*srv.Up // restarting and exited count as down
	if !down {
		return // running without ping, started less than noPingStable ago
	}
	if srv.StartedAt > 0 && now.Sub(started) < startGrace {
		// every (re)start opens a new grace period: the count starts again afterwards
		delete(a.downSince, srv.ID)
		return
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
func (a *alerter) remind(ctx context.Context, now time.Time) {
	for _, key := range slices.Sorted(maps.Keys(a.firing)) {
		st := a.firing[key]
		if now.Sub(st.lastNotified) < remindEvery {
			continue
		}
		kind, id := parseKey(key)
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

// forgetMissing drops the state of servers that are no longer in the pass (deleted servers;
// their alert_state rows are already gone with the server, spec §5).
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
```

- [ ] **Step 4: Grün prüfen**

Run: `make check`, dann `make test-vm SUITE=go` (oder gezielt `go test -race ./internal/monitoring -run 'Test(Down|Recovery|Gated|Stopped|Agent|Docker|State|Reminder|Settings|Crash|Deleted|Disk|Firing)' -count=3`).
Expected: PASS, stabil.

- [ ] **Step 5: Commit**

```bash
git add internal/monitoring/alerts.go internal/monitoring/alerts_test.go
git commit -m "feat(monitoring): down, crash loop and disk alerts with persistent state

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Aufgabe 9: Lese-Schicht und API-Routen

**Files:**
- Create: `internal/monitoring/read.go`, `internal/monitoring/overview.go`
- Create (ersetzt Stub): `internal/monitoring/api.go`
- Test: `internal/monitoring/read_test.go`, `internal/monitoring/api_test.go`, `internal/monitoring/overview_test.go`

**Interfaces:**
- Consumes: `Handler{d Deps; coll *collector; alerts *alerter}` und dessen `Routes` → `h.routes(api)` (V4, `handler.go`); `collector.Latest/Watch/Live` (V4); `alerter.Firing` und die `kind…`-Konstanten (Aufgabe 8); Queries aus V2; `RbacGetServer`, `RbacListServers`, `RbacListViewableServers`.
- Produces: `func (h *Handler) routes(api *rbac.Router)` (V4); paket-intern `type sampler interface{ Latest() Snapshot; Watch(int64); Live(int64) (ServerSample, bool) }`, `type firingSource interface{ Firing(context.Context) []FiringAlert }`, `type apiHandlers struct{…}` mit `register(*rbac.Router)`; `func parseRange(string) (metricsRange, bool)`, `func readSeries(...)`, `func readSparkline(...)`.

- [ ] **Step 1: Tests für die Lese-Schicht schreiben (rot)**

`internal/monitoring/read_test.go`:

```go
package monitoring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var readNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func aInsertRaw(t *testing.T, db *sql.DB, series int64, metric string, at time.Time, v float64) {
	t.Helper()
	if err := store.New(db).MonitoringInsertRaw(context.Background(), store.MonitoringInsertRawParams{
		Series: series, Metric: metric, At: at.UnixMilli(), Value: v,
	}); err != nil {
		t.Fatal(err)
	}
}

func insertAgg(t *testing.T, db *sql.DB, table string, series int64, metric string, at time.Time, avg, lo, hi float64) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), //nolint:gosec // G202: table is one of two fixed test names
		`INSERT INTO `+table+` (series, metric, at, avg, min, max, samples) VALUES (?, ?, ?, ?, ?, ?, 10)`,
		series, metric, at.UnixMilli(), avg, lo, hi); err != nil {
		t.Fatal(err)
	}
}

func TestParseRange(t *testing.T) {
	for name, step := range map[string]time.Duration{"1h": 30 * time.Second, "24h": 5 * time.Minute, "7d": time.Hour, "30d": time.Hour} {
		rg, ok := parseRange(name)
		if !ok || rg.step != step || rg.name != name {
			t.Errorf("parseRange(%q) = %+v %v", name, rg, ok)
		}
	}
	for _, bad := range []string{"", "2h", "1H", "30d ", "all"} {
		if _, ok := parseRange(bad); ok {
			t.Errorf("parseRange(%q) accepted", bad)
		}
	}
}

func TestReadSeriesPicksTableAndWindow(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	aInsertRaw(t, db, 7, MetricPlayers, readNow.Add(-30*time.Minute), 3)
	aInsertRaw(t, db, 7, MetricPlayers, readNow.Add(-2*time.Hour), 5) // outside 1h
	aInsertRaw(t, db, 7, "foo", readNow.Add(-time.Minute), 1) // unknown metric
	aInsertRaw(t, db, 8, MetricPlayers, readNow.Add(-time.Minute), 9) // other series
	insertAgg(t, db, "metrics_5m", 7, MetricTPS, readNow.Add(-23*time.Hour), 19.5, 18, 20)
	insertAgg(t, db, "metrics_5m", 7, MetricTPS, readNow.Add(-25*time.Hour), 10, 10, 10) // outside 24h
	insertAgg(t, db, "metrics_1h", 7, MetricCPU, readNow.Add(-6*24*time.Hour), 40, 10, 90)
	insertAgg(t, db, "metrics_1h", 7, MetricCPU, readNow.Add(-20*24*time.Hour), 30, 5, 60)

	rg, _ := parseRange("1h")
	got, err := readSeries(context.Background(), q, 7, rg, readNow, serverMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(serverMetrics) {
		t.Fatalf("metrics = %v", got)
	}
	if p := got[MetricPlayers]; len(p) != 1 || p[0] != (point{float64(readNow.Add(-30 * time.Minute).UnixMilli()), 3, 3, 3}) {
		t.Fatalf("players 1h = %v", p)
	}
	if _, ok := got["foo"]; ok {
		t.Fatal("unknown metric returned")
	}

	rg, _ = parseRange("24h")
	got, _ = readSeries(context.Background(), q, 7, rg, readNow, serverMetrics)
	if p := got[MetricTPS]; len(p) != 1 || p[0][1] != 19.5 || p[0][2] != 18 || p[0][3] != 20 {
		t.Fatalf("tps 24h = %v", p)
	}
	if len(got[MetricPlayers]) != 0 {
		t.Fatalf("24h reads raw values: %v", got[MetricPlayers])
	}

	rg, _ = parseRange("7d")
	got, _ = readSeries(context.Background(), q, 7, rg, readNow, serverMetrics)
	if p := got[MetricCPU]; len(p) != 1 || p[0][1] != 40 {
		t.Fatalf("cpu 7d = %v", p)
	}
	rg, _ = parseRange("30d")
	got, _ = readSeries(context.Background(), q, 7, rg, readNow, serverMetrics)
	if p := got[MetricCPU]; len(p) != 2 || p[0][1] != 30 {
		t.Fatalf("cpu 30d = %v", p)
	}
}

func TestThirtyDaysAtMost721Points(t *testing.T) {
	db := storetest.Open(t)
	start := readNow.Add(-31 * 24 * time.Hour)
	for at := start; at.Before(readNow); at = at.Add(time.Hour) {
		insertAgg(t, db, "metrics_1h", HostSeries, MetricLoad1, at, 1, 1, 1)
	}
	rg, _ := parseRange("30d")
	got, err := readSeries(context.Background(), store.New(db), HostSeries, rg, readNow, hostMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got[MetricLoad1]); n == 0 || n > 721 {
		t.Fatalf("points = %d", n)
	}
}

func TestSparklineOnlyPlayersAtMost120(t *testing.T) {
	db := storetest.Open(t)
	for i := range 200 {
		at := readNow.Add(-time.Duration(i) * 18 * time.Second)
		aInsertRaw(t, db, 3, MetricPlayers, at, float64(i%5))
		aInsertRaw(t, db, 3, MetricTPS, at, 20)
	}
	got, err := readSparkline(context.Background(), store.New(db), 3, readNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxSparkline {
		t.Fatalf("sparkline has %d points", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i][0] <= got[i-1][0] {
			t.Fatalf("not ascending at %d: %v", i, got[i-1:i+1])
		}
	}
	if got[len(got)-1][0] != float64(readNow.UnixMilli()) {
		t.Fatalf("newest point = %v", got[len(got)-1])
	}
}
```

- [ ] **Step 2: Tests für die Routen schreiben (rot)**

`internal/monitoring/api_test.go`:

```go
package monitoring

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var apiT0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

type aSampler struct {
	mu      sync.Mutex
	latest  Snapshot
	live    map[int64]ServerSample
	watched []int64
}

func (f *aSampler) Latest() Snapshot { f.mu.Lock(); defer f.mu.Unlock(); return f.latest }

func (f *aSampler) Watch(id int64) { f.mu.Lock(); defer f.mu.Unlock(); f.watched = append(f.watched, id) }

func (f *aSampler) Live(id int64) (ServerSample, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.live[id]
	return s, ok
}

type aFiring struct{ list []FiringAlert }

func (f *aFiring) Firing(context.Context) []FiringAlert { return f.list }

type apiAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (a *apiAudit) Record(_ context.Context, e audit.Entry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, e)
	return nil
}

func (a *apiAudit) all() []audit.Entry { a.mu.Lock(); defer a.mu.Unlock(); return slices.Clone(a.entries) }

type apiEnv struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	src    *aSampler
	firing *aFiring
	audit  *apiAudit
	mux    *chi.Mux
	rt     *rbac.Router
	rootID int64
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	db := storetest.Open(t)
	e := &apiEnv{t: t, db: db, clk: clock.NewFake(apiT0), src: &aSampler{live: map[int64]ServerSample{}},
		firing: &aFiring{}, audit: &apiAudit{}}
	h := &apiHandlers{q: store.New(db), clock: e.clk, rec: e.audit, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		src: e.src, alerts: e.firing}
	e.mux = chi.NewRouter()
	e.rt = rbac.NewRouter(e.mux, rbac.NewDBChecker(db), e.clk)
	h.register(e.rt)
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

func (e *apiEnv) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

func (e *apiEnv) sudoAdmin() principal.Principal {
	p := e.admin()
	p.SudoUntil = apiT0.Add(time.Hour)
	return p
}

func (e *apiEnv) user(name string) principal.Principal {
	id := storetest.CreateUser(e.t, e.db, name, "user")
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func (e *apiEnv) grant(userID, serverID int64, perm string) {
	e.t.Helper()
	if _, err := e.db.ExecContext(context.Background(),
		`INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, perm); err != nil {
		e.t.Fatal(err)
	}
}

func (e *apiEnv) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func (e *apiEnv) must(want int, method, path string, body any, p principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := e.do(method, path, body, &p)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
	}
	return rec
}

func apiDecode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

// aReadRoutes are the routes this phase implements; the route contract of all seven
// (routes_test.go) and the settings tests (settings_test.go) are fixed since phase A.
var aReadRoutes = map[string]rbac.Rule{
	"GET /servers/{id}/metrics":      rbac.Server(rbac.PermServerView, "id"),
	"GET /servers/{id}/metrics/live": rbac.Server(rbac.PermServerView, "id"),
	"GET /monitoring/overview":       rbac.Authenticated(),
	"GET /monitoring/host":           rbac.Admin(),
}

func TestRouteRules(t *testing.T) {
	e := newAPIEnv(t)
	id := storetest.CreateServer(t, e.db, "survival")
	bob := e.user("bob")
	for key := range aReadRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		path := strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id)) + "?range=1h"
		if rec := e.do(method, path, "{}", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
		if key == "GET /monitoring/overview" {
			continue
		}
		if rec := e.do(method, path, "{}", &bob); rec.Code != http.StatusForbidden {
			t.Errorf("%s without rights: %d", key, rec.Code)
		}
	}
	e.grant(bob.UserID, id, "server.view")
	e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", id), nil, bob)
	e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics/live", id), nil, bob)
}

func aPtr[T any](v T) *T { return &v }

func TestMetricsValidatesRangeAndServer(t *testing.T) {
	e := newAPIEnv(t)
	id := storetest.CreateServer(t, e.db, "survival")
	rec := e.must(http.StatusBadRequest, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=2h", id), nil, e.admin())
	if !strings.Contains(rec.Body.String(), httpx.CodeValidation) {
		t.Fatalf("body = %s", rec.Body)
	}
	e.must(http.StatusBadRequest, http.MethodGet, fmt.Sprintf("/servers/%d/metrics", id), nil, e.admin())
	e.must(http.StatusNotFound, http.MethodGet, "/servers/999/metrics?range=1h", nil, e.admin())
	e.must(http.StatusNotFound, http.MethodGet, "/servers/999/metrics/live", nil, e.admin())
}

func TestMetricsReturnsSeries(t *testing.T) {
	e := newAPIEnv(t)
	id := storetest.CreateServer(t, e.db, "survival")
	aInsertRaw(t, e.db, id, MetricPlayers, apiT0.Add(-time.Minute), 4)
	got := apiDecode[metricsJSON](t, e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", id), nil, e.admin()))
	if got.Range != "1h" || got.StepMS != 30_000 || len(got.Series[MetricPlayers]) != 1 || got.Series[MetricPlayers][0][1] != 4 {
		t.Fatalf("metrics = %+v", got)
	}
	if s, ok := got.Series[MetricTPS]; !ok || s == nil || len(s) != 0 {
		t.Fatalf("missing empty tps series: %+v", got.Series)
	}
	host := apiDecode[metricsJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/host?range=7d", nil, e.admin()))
	if host.StepMS != 3_600_000 || len(host.Series) != len(hostMetrics) {
		t.Fatalf("host = %+v", host)
	}
}

func TestLiveWatchesAndMapsValues(t *testing.T) {
	e := newAPIEnv(t)
	id := storetest.CreateServer(t, e.db, "survival")
	path := fmt.Sprintf("/servers/%d/metrics/live", id)

	// before the first pass: all null
	got := apiDecode[liveJSON](t, e.must(http.StatusOK, http.MethodGet, path, nil, e.admin()))
	if got.At != nil || got.Players != nil || got.TPSSource != TPSSourceUnknown || got.PlayerNames == nil {
		t.Fatalf("empty live = %+v", got)
	}

	up, players, tps := true, 3, 19.8
	e.src.latest = Snapshot{At: apiT0, Agent: true}
	e.src.live[id] = ServerSample{ID: id, Up: &up, Players: &players, PlayerNames: []string{"Alex"}, TPS: &tps, TPSSource: TPSSourceTickQuery}
	got = apiDecode[liveJSON](t, e.must(http.StatusOK, http.MethodGet, path, nil, e.admin()))
	if got.At == nil || *got.At != apiT0.UnixMilli() || *got.Players != 3 || *got.TPS != 19.8 || got.TPSSource != TPSSourceTickQuery ||
		!slices.Equal(got.PlayerNames, []string{"Alex"}) {
		t.Fatalf("live = %+v", got)
	}
	if !slices.Equal(e.src.watched, []int64{id, id}) {
		t.Fatalf("watched = %v", e.src.watched)
	}

	e.src.latest = Snapshot{At: apiT0.Add(30 * time.Second), Agent: false}
	rec := e.must(http.StatusServiceUnavailable, http.MethodGet, path, nil, e.admin())
	if !strings.Contains(rec.Body.String(), httpx.CodeAgentUnavailable) {
		t.Fatalf("body = %s", rec.Body)
	}
}

func aLiveRunning(id int64, players int) ServerSample {
	up, cpu, mem, limit := true, 12.5, int64(2<<30), int64(5<<30)
	return ServerSample{ID: id, DesiredRunning: true, State: agentapi.StateRunning, Up: &up, Players: &players,
		CPUPercent: &cpu, MemoryBytes: &mem, MemoryLimit: &limit}
}
```

`internal/monitoring/overview_test.go`:

```go
package monitoring

import (
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestOverviewFiltersByPermission(t *testing.T) {
	e := newAPIEnv(t)
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	disk := int64(50 << 30)
	e.src.latest = Snapshot{At: apiT0, Agent: true, Servers: []ServerSample{aLiveRunning(a, 2), aLiveRunning(b, 5)},
		Host: HostSample{DiskFree: &disk}}
	aInsertRaw(t, e.db, a, MetricPlayers, apiT0.Add(-time.Minute), 2)
	e.firing.list = []FiringAlert{
		{Kind: alertDiskLow, Since: apiT0},
		{Kind: alertServerDown, ServerID: b, ServerName: "beta", Since: apiT0},
	}

	bob := e.user("bob")
	e.grant(bob.UserID, a, "server.view")
	got := apiDecode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", nil, bob))
	if len(got.Servers) != 1 || got.Servers[0].ID != a || *got.Servers[0].Players != 2 || got.Host != nil {
		t.Fatalf("bob overview = %+v", got)
	}
	if len(got.Servers[0].Sparkline) != 1 || got.Servers[0].Sparkline[0][1] != 2 || len(got.Servers[0].Alerts) != 0 {
		t.Fatalf("bob server = %+v", got.Servers[0])
	}

	got = apiDecode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", nil, e.admin()))
	if len(got.Servers) != 2 || got.Host == nil || *got.Host.DiskFree != disk || len(got.Host.Alerts) != 1 || got.Host.Alerts[0] != alertDiskLow {
		t.Fatalf("admin overview = %+v", got)
	}
	for _, s := range got.Servers {
		if s.ID == b && (len(s.Alerts) != 1 || s.Alerts[0] != alertServerDown) {
			t.Fatalf("beta alerts = %+v", s.Alerts)
		}
	}
}

func TestOverviewServerWithoutSample(t *testing.T) {
	e := newAPIEnv(t)
	storetest.CreateServer(t, e.db, "lobby") // no instance, not in the snapshot
	got := apiDecode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", nil, e.admin()))
	if len(got.Servers) != 1 || got.Servers[0].Up != nil || got.Servers[0].Players != nil || got.Servers[0].Sparkline == nil ||
		got.Servers[0].Alerts == nil {
		t.Fatalf("overview = %+v", got)
	}
	if got.Host == nil || got.Host.At != nil {
		t.Fatalf("host before the first pass = %+v", got.Host)
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `make check` – Kompilierfehler (`parseRange`, `apiHandlers`, `metricsJSON`, … fehlen). Danach `make test-vm SUITE=go` – FAIL.

- [ ] **Step 4: Lese-Schicht implementieren**

`internal/monitoring/read.go`:

```go
package monitoring

import (
	"context"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// metricsRange is one of the history ranges of the API (spec §6.1).
type metricsRange struct {
	name string
	span time.Duration
	step time.Duration // 30 s = metrics_raw, 5 min = metrics_5m, 1 h = metrics_1h
}

var ranges = map[string]metricsRange{
	"1h":  {name: "1h", span: time.Hour, step: 30 * time.Second},
	"24h": {name: "24h", span: 24 * time.Hour, step: 5 * time.Minute},
	"7d":  {name: "7d", span: 7 * 24 * time.Hour, step: time.Hour},
	"30d": {name: "30d", span: 30 * 24 * time.Hour, step: time.Hour},
}

func parseRange(s string) (metricsRange, bool) {
	rg, ok := ranges[s]
	return rg, ok
}

// The metrics of the API (V3); other names in the tables are never returned.
var (
	serverMetrics = []string{MetricUp, MetricPlayers, MetricPlayersMax, MetricTPS, MetricMSPT, MetricCPU, MetricMemory, MetricMemoryLimit}
	hostMetrics   = []string{MetricHostCPU, MetricMemUsed, MetricMemTotal, MetricLoad1, MetricDiskFree, MetricDiskTotal}
)

const (
	sparklineSpan = time.Hour
	maxSparkline  = 120
)

// point is [at, avg, min, max] (V3 MetricPoint).
type point [4]float64

// readSeries returns every allowed metric of series in (now-span, now], each with at least an
// empty slice.
func readSeries(ctx context.Context, q *store.Queries, series int64, rg metricsRange, now time.Time, allowed []string) (map[string][]point, error) {
	out := make(map[string][]point, len(allowed))
	for _, m := range allowed {
		out[m] = []point{}
	}
	add := func(metric string, p point) {
		if s, ok := out[metric]; ok {
			out[metric] = append(s, p)
		}
	}
	from, to := now.Add(-rg.span).UnixMilli(), now.UnixMilli()+1
	switch rg.step {
	case 30 * time.Second:
		rows, err := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: series, FromAt: from, ToAt: to})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.Metric, point{float64(r.At), r.Value, r.Value, r.Value})
		}
	case 5 * time.Minute:
		rows, err := q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: series, FromAt: from, ToAt: to})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.Metric, point{float64(r.At), r.Avg, r.Min, r.Max})
		}
	default:
		rows, err := q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: series, FromAt: from, ToAt: to})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.Metric, point{float64(r.At), r.Avg, r.Min, r.Max})
		}
	}
	return out, nil
}

// readSparkline returns the players of the last hour as [at, players], oldest first, at most
// maxSparkline points (the newest).
func readSparkline(ctx context.Context, q *store.Queries, id int64, now time.Time) ([][2]float64, error) {
	rows, err := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{
		Series: id, FromAt: now.Add(-sparklineSpan).UnixMilli(), ToAt: now.UnixMilli() + 1,
	})
	if err != nil {
		return nil, err
	}
	out := [][2]float64{}
	for _, r := range rows {
		if r.Metric == MetricPlayers {
			out = append(out, [2]float64{float64(r.At), r.Value})
		}
	}
	if len(out) > maxSparkline {
		out = out[len(out)-maxSparkline:]
	}
	return out, nil
}
```

- [ ] **Step 5: Routen implementieren**

`internal/monitoring/api.go` (ersetzt den Stub vollständig; die Einstellungs-Routen bleiben die fertigen Handler aus `settings.go`, Phase A):

```go
package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// sampler is what the routes need from the collector (*collector implements it).
type sampler interface {
	Latest() Snapshot
	Watch(serverID int64)
	Live(serverID int64) (ServerSample, bool)
}

// firingSource is what the routes need from the alerter (*alerter implements it).
type firingSource interface {
	Firing(ctx context.Context) []FiringAlert
}

type apiHandlers struct {
	q      *store.Queries
	clock  clock.Clock
	log    *slog.Logger
	src    sampler
	alerts firingSource
}

// routes registers the monitoring routes (V3, paths relative to /api).
func (h *Handler) routes(api *rbac.Router) {
	log := h.d.Log
	if log == nil {
		log = slog.Default()
	}
	(&apiHandlers{q: store.New(h.d.DB), clock: h.d.Clock, log: log, src: h.coll, alerts: h.alerts}).register(api)
	// fixed since phase A (settings.go): settings and the per-server switch
	api.Get("/monitoring/settings", rbac.Admin(), h.handleGetSettings)
	api.Put("/monitoring/settings", rbac.Admin().WithSudo(), h.handlePutSettings)
	api.Put("/servers/{id}/alerts", rbac.Admin(), h.handlePutServerAlerts)
}

func (a *apiHandlers) register(api *rbac.Router) {
	view := rbac.Server(rbac.PermServerView, "id")
	api.Get("/servers/{id}/metrics", view, a.handleMetrics)
	api.Get("/servers/{id}/metrics/live", view, a.handleLive)
	api.Get("/monitoring/overview", rbac.Authenticated(), a.handleOverview)
	api.Get("/monitoring/host", rbac.Admin(), a.handleHost)
}

type metricsJSON struct {
	Range  string             `json:"range"`
	StepMS int64              `json:"step_ms"`
	Series map[string][]point `json:"series"`
}

type liveJSON struct {
	At               *int64   `json:"at"`
	Up               *bool    `json:"up"`
	Players          *int     `json:"players"`
	PlayersMax       *int     `json:"players_max"`
	PlayerNames      []string `json:"player_names"`
	TPS              *float64 `json:"tps"`
	MSPT             *float64 `json:"mspt"`
	CPUPercent       *float64 `json:"cpu_percent"`
	MemoryBytes      *int64   `json:"memory_bytes"`
	MemoryLimitBytes *int64   `json:"memory_limit_bytes"`
	TPSSource        string   `json:"tps_source"`
}

// loadServerID returns the {id} of an existing server; it writes 404 otherwise (admins pass
// the rbac rule for unknown ids).
func (a *apiHandlers) loadServerID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id > 0 {
		_, err = a.q.RbacGetServer(r.Context(), id)
	} else {
		err = sql.ErrNoRows
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
		return 0, false
	case err != nil:
		httpx.Internal(w, r, err)
		return 0, false
	}
	return id, true
}

func (a *apiHandlers) writeSeries(w http.ResponseWriter, r *http.Request, series int64, allowed []string) {
	rg, ok := parseRange(r.URL.Query().Get("range"))
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "range must be 1h, 24h, 7d or 30d")
		return
	}
	s, err := readSeries(r.Context(), a.q, series, rg, a.clock.Now(), allowed)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, metricsJSON{Range: rg.name, StepMS: rg.step.Milliseconds(), Series: s})
}

func (a *apiHandlers) handleMetrics(w http.ResponseWriter, r *http.Request) {
	id, ok := a.loadServerID(w, r)
	if !ok {
		return
	}
	a.writeSeries(w, r, id, serverMetrics)
}

func (a *apiHandlers) handleHost(w http.ResponseWriter, r *http.Request) {
	a.writeSeries(w, r, HostSeries, hostMetrics)
}

// handleLive marks the server as watched (5-s TPS for 30 s, spec §6.2) and returns the freshest
// values; 503 agent_unavailable when the agent did not answer the last pass.
func (a *apiHandlers) handleLive(w http.ResponseWriter, r *http.Request) {
	id, ok := a.loadServerID(w, r)
	if !ok {
		return
	}
	a.src.Watch(id)
	snap := a.src.Latest()
	if !snap.At.IsZero() && !snap.Agent {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the agent did not answer the last monitoring pass")
		return
	}
	out := liveJSON{PlayerNames: []string{}, TPSSource: TPSSourceUnknown}
	if !snap.At.IsZero() {
		at := snap.At.UnixMilli()
		out.At = &at
	}
	if s, ok := a.src.Live(id); ok {
		out.Up, out.Players, out.PlayersMax = s.Up, s.Players, s.PlayersMax
		out.TPS, out.MSPT, out.CPUPercent = s.TPS, s.MSPT, s.CPUPercent
		out.MemoryBytes, out.MemoryLimitBytes = s.MemoryBytes, s.MemoryLimit
		if s.PlayerNames != nil {
			out.PlayerNames = s.PlayerNames
		}
		if s.TPSSource != "" {
			out.TPSSource = s.TPSSource
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

```

`internal/monitoring/overview.go`:

```go
package monitoring

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type overviewServerJSON struct {
	ID               int64        `json:"id"`
	Up               *bool        `json:"up"`
	Players          *int         `json:"players"`
	PlayersMax       *int         `json:"players_max"`
	TPS              *float64     `json:"tps"`
	CPUPercent       *float64     `json:"cpu_percent"`
	MemoryBytes      *int64       `json:"memory_bytes"`
	MemoryLimitBytes *int64       `json:"memory_limit_bytes"`
	Sparkline        [][2]float64 `json:"sparkline"`
	Alerts           []string     `json:"alerts"`
}

type hostJSON struct {
	At          *int64   `json:"at"`
	CPUPercent  *float64 `json:"cpu_percent"`
	Load1       *float64 `json:"load1"`
	MemoryUsed  *int64   `json:"memory_used"`
	MemoryTotal *int64   `json:"memory_total"`
	DiskFree    *int64   `json:"disk_free"`
	DiskTotal   *int64   `json:"disk_total"`
	Alerts      []string `json:"alerts"`
}

type overviewJSON struct {
	Servers []overviewServerJSON `json:"servers"`
	Host    *hostJSON            `json:"host"`
}

// handleOverview returns compact values for every server the user may view (server.view; all
// for admins) and the host block for admins only (spec §6.1).
func (a *apiHandlers) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	var list []store.Server
	var err error
	if p.IsAdmin() {
		list, err = a.q.RbacListServers(ctx)
	} else {
		list, err = a.q.RbacListViewableServers(ctx, p.UserID)
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	snap := a.src.Latest()
	samples := make(map[int64]ServerSample, len(snap.Servers))
	for _, s := range snap.Servers {
		samples[s.ID] = s
	}
	serverAlerts := map[int64][]string{}
	hostAlerts := []string{}
	for _, f := range a.alerts.Firing(ctx) {
		if f.ServerID == 0 {
			hostAlerts = append(hostAlerts, f.Kind)
		} else {
			serverAlerts[f.ServerID] = append(serverAlerts[f.ServerID], f.Kind)
		}
	}
	now := a.clock.Now()
	out := overviewJSON{Servers: make([]overviewServerJSON, 0, len(list))}
	for _, s := range list {
		spark, err := readSparkline(ctx, a.q, s.ID, now)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		js := overviewServerJSON{ID: s.ID, Sparkline: spark, Alerts: serverAlerts[s.ID]}
		if js.Alerts == nil {
			js.Alerts = []string{}
		}
		if smp, ok := samples[s.ID]; ok {
			js.Up, js.Players, js.PlayersMax, js.TPS = smp.Up, smp.Players, smp.PlayersMax, smp.TPS
			js.CPUPercent, js.MemoryBytes, js.MemoryLimitBytes = smp.CPUPercent, smp.MemoryBytes, smp.MemoryLimit
		}
		out.Servers = append(out.Servers, js)
	}
	if p.IsAdmin() {
		h := snap.Host
		hj := &hostJSON{CPUPercent: h.CPUPercent, Load1: h.Load1, MemoryUsed: h.MemoryUsed, MemoryTotal: h.MemoryTotal,
			DiskFree: h.DiskFree, DiskTotal: h.DiskTotal, Alerts: hostAlerts}
		if !snap.At.IsZero() {
			at := snap.At.UnixMilli()
			hj.At = &at
		}
		out.Host = hj
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

Hinweis: `store.Server` ist der Zeilentyp von `RbacListServers`/`RbacListViewableServers` (wie in `internal/servers/servers.go`); Step 0 aus Aufgabe 8 prüft das mit.

- [ ] **Step 6: Grün prüfen**

Run: `make check`, dann `make test-vm SUITE=go` (gezielt: `go test -race ./internal/monitoring -run 'Test(ParseRange|ReadSeries|ThirtyDays|Sparkline|Route|Metrics|Live|Settings|ServerAlerts|Overview)'`) und `make test-vm SUITE=lint`.
Expected: PASS; golangci-lint ohne Befund (die `//nolint:gosec // G202`-Zeile in `read_test.go` ist begründet).

- [ ] **Step 7: Commit**

```bash
git add internal/monitoring/read.go internal/monitoring/read_test.go internal/monitoring/api.go internal/monitoring/api_test.go \
  internal/monitoring/overview.go internal/monitoring/overview_test.go
git commit -m "feat(monitoring): history, live, overview and alert settings routes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

- [ ] **Step 8: Push und Draft-PR**

Vor dem Push `make check` und `make test-vm` (alle Suites). Dann:

```bash
git push -u origin feat/monitoring-alerts-api
gh pr create --draft --title "feat(monitoring): alerts and API (sub-project 5)" --body "$(cat <<'EOF'
Phase B2 of sub-project 5: alerts (server.down/recovered, crash loop, host disk with hysteresis, 24 h reminders, persistent state) and all seven monitoring routes.

Task 10 (end-to-end test with the real collector) waits for the collector PR (B1).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
EOF
)"
gh pr checks <n> --watch
```

---

### Aufgabe 10: Gesamttest mit echtem Sammler

Startet erst, wenn B1 (`feat/monitoring-collector`) auf `main` ist: `git fetch origin && git merge origin/main`.

**Files:**
- Test: `internal/monitoring/e2e_test.go`

**Interfaces:**
- Consumes: `NewHandler`, `Routes`, `Run` (V4, `handler.go`); der echte `collector` aus B1 (erster Durchlauf sofort beim Start, siehe Annahmen 5); `agentapi.InstancesClient`, `agentapi.ConsoleClient`; `config`-unabhängig (Deps direkt).
- Produces: nichts.

- [ ] **Step 1: Test schreiben**

`internal/monitoring/e2e_test.go`:

```go
package monitoring

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// The fakes come from helpers_test.go (phase A): fakeAgent, fakeConsole, pingServer, statusJSON.

func TestEndToEndFirstPass(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	id := storetest.CreateServer(t, db, "survival")
	if _, err := store.New(db).ServersCreateInstance(ctx, store.ServersCreateInstanceParams{
		ServerID: id, Type: "paper", Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: apiT0.UnixMilli(), UpdatedAt: apiT0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE instances SET desired_state = 'running' WHERE server_id = ` + fmt.Sprint(id),
		`UPDATE instance_settings SET base_domain = 'mc.example.com' WHERE id = 1`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	slp := newPingServer(t)
	slp.set("survival.mc.example.com", pingAnswer{json: statusJSON(3, 20, "Alex")})
	cpu := 12.5
	ag := newFakeAgent()
	ag.set(agentapi.InstanceStatus{
		ID: id, Name: "survival", Revision: 1, State: agentapi.StateRunning,
		StartedAt: time.Now().Add(-time.Hour).UnixMilli(), MemoryBytes: 2 << 30, MemoryLimitBytes: 5 << 30, CPUPercent: cpu,
	})
	con := newFakeConsole()
	con.reply(id, "tick query", "The game is running normally\nTarget tick rate: 20.0 per second.\nAverage time per tick: 1.2ms (Target: 50.0ms)", nil)
	notes := &notify.Recording{}
	h := NewHandler(Deps{
		DB: db, Clock: clock.Real{}, Audit: &apiAudit{}, Notifier: notes, Instances: ag, Console: con,
		PingAddr: slp.addr(), InstancesDir: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clock.Real{})
	h.Routes(rt)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { h.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	e := &apiEnv{t: t, db: db, mux: mux, rt: rt, rootID: storetest.CreateUser(t, db, "root", "admin")}
	livePath := fmt.Sprintf("/servers/%d/metrics/live", id)
	deadline := time.Now().Add(10 * time.Second)
	var live liveJSON
	for {
		live = apiDecode[liveJSON](t, e.must(http.StatusOK, http.MethodGet, livePath, nil, e.admin()))
		if live.Players != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if live.Players == nil || *live.Players != 3 || live.Up == nil || !*live.Up || live.CPUPercent == nil || *live.CPUPercent != cpu ||
		!slices.Equal(live.PlayerNames, []string{"Alex"}) {
		t.Fatalf("live = %+v", live)
	}
	if slp.hits("survival.mc.example.com") == 0 {
		t.Fatal("the collector never pinged survival.mc.example.com")
	}
	metrics := apiDecode[metricsJSON](t, e.must(http.StatusOK, http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", id), nil, e.admin()))
	if len(metrics.Series[MetricPlayers]) == 0 || metrics.Series[MetricPlayers][0][1] != 3 {
		t.Fatalf("stored players = %+v", metrics.Series[MetricPlayers])
	}
	ov := apiDecode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", nil, e.admin()))
	if len(ov.Servers) != 1 || ov.Servers[0].Players == nil || *ov.Servers[0].Players != 3 ||
		ov.Host == nil || ov.Host.DiskTotal == nil || *ov.Host.DiskTotal <= 0 {
		t.Fatalf("overview = %+v", ov)
	}
	if got := notes.Types(); len(got) != 0 {
		t.Fatalf("a healthy server sent notifications: %v", got)
	}
}
```

- [ ] **Step 2: Laufen lassen**

Run: `make check`, dann `ssh claude@192.168.1.117 'cd ~/mc-panel-test/monitoring-alerts-api && PATH=/usr/local/go/bin:$PATH go test -race ./internal/monitoring -run TestEndToEndFirstPass -count=5'` (nach `make test-vm SUITE=go`, das den Baum kopiert).
Expected: PASS fünfmal. Schlägt er fehl, weil der erste Durchlauf erst nach 30 s kommt (Annahmen 5), nicht die Frist verlängern, sondern den Lead informieren.

- [ ] **Step 3: Commit, Push, CI**

```bash
git add internal/monitoring/e2e_test.go
git commit -m "test(monitoring): end-to-end first pass with ping, agent and API

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
make test-vm
git push
gh pr checks <n> --watch
```

PR-Text um „Aufgabe 10 erledigt“ und die Abweichungen ergänzen; dem Lead PR-Nummer, CI-Stand und Abweichungen melden. Nicht mergen, nicht auf ready setzen.

## Self-Review

- **Spec §5:** Down (Schonfrist, Daten-Sperre, `down_alerts`, Lösen per Ping bzw. 2 min ohne Ping) → Aufgabe 8 `evalDown` + Tests; Crash-Loop (Fenster, Reset) → `evalCrash`; Platte mit Hysterese → `evalDisk`; Versand beim Auslösen/Lösen/alle 24 h → `fireLocked`/`resolveLocked`/`remind`; Zustand über Neustarts → `loadLocked` + `TestStateSurvivesPanelRestart`; gelöschter Server ohne Meldung → `forgetMissing`; gestoppter Server löst ohne Meldung → `dropLocked`.
- **Spec §6:** Routenvertrag aller sieben Routen liegt in `routes_test.go` (Phase A), Settings/Server-Schalter in `settings.go`/`settings_test.go` (Phase A); hier die vier Lese-Routen (`TestRouteRules`), Bereiche/`step_ms` (`TestParseRange`, `TestMetricsReturnsSeries`), Live-Modus (`Watch` + 503), Overview-Filter und Host nur für Admins. `GET /servers/{id}` mit `down_alerts` liegt in Phase A (`internal/servers`).
- **Typen:** `alertServerDown/alertCrashLoop/alertDiskLow` (Aufgabe 8) in Aufgabe 9 genutzt; `FiringAlert` (V4) überall gleich; `point` = `[4]float64`.
