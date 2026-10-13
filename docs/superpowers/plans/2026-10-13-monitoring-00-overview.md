# mc-panel Teilprojekt 5: Monitoring + Alerts – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Spieler, TPS/MSPT, CPU und RAM pro Server sowie Host-CPU/RAM/Load/Platte sammeln, 30 Tage gestuft speichern, im Dashboard und in einem Tab „Monitoring“ zeigen und bei „Server down / Crash-Loop“ und „Platte knapp“ über `notify` melden.

**Architecture:** Neues panel-Paket `internal/monitoring`: ein Sammler (30-s-Takt, TPS 60 s, live 5 s) holt den agent-Status, pingt jeden laufenden Server per Server-List-Ping über mc-router (`127.0.0.1:25565`, FQDN im Handshake), fragt TPS/MSPT per RCON über die vorhandene agent-Route ab und liest Host-Werte selbst aus `/proc` und `statfs`. Jeder Durchlauf wird in SQLite geschrieben (Rohwerte, 5-min-, 1-h-Verdichtung) und an die Alert-Auswertung übergeben, die ihren Zustand persistent hält und über `notify` meldet. Der agent bekommt nur `restart_count`. Diagramme im Frontend sind eigene SVG-Komponenten.

**Tech Stack:** Go (Standardbibliothek, chi, `modernc.org/sqlite`, `net`, `encoding/binary`, `golang.org/x/sys/unix` nur falls schon im Modul – sonst `syscall`); React 19, TanStack Router/Query, shadcn/ui, react-i18next, MSW 2, Vitest, Playwright. Keine neuen Go- und keine neuen Frontend-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-13-monitoring-00-overview.md` (diese) | – | Ausführung, Constraints, Review Focus, Dateistruktur, Verträge V1–V6 |
| `2026-10-13-monitoring-01-contracts.md` | 1–3 | agent `restart_count`, panel-Konfiguration, Notify-Ereignisse, Audit; Migration + sqlc, `servers` (Löschen räumt Monitoring-Daten, `down_alerts` im Detail); OpenAPI, Paket-Gerüst `internal/monitoring` (Typen, Handler, Stubs), Verdrahtung, MSW-Mocks |
| `2026-10-13-monitoring-02-collector.md` | 4–7 | Sammler: SLP-Client, TPS-Erkennung/-Parser, Host-Leser, Sammel-Durchlauf + Live-Modus, Schreiben, Verdichtung + Löschen |
| `2026-10-13-monitoring-03-alerts-api.md` | 8–10 | Alerts (Regeln, Zustand, Versand), Lese-Schicht und alle API-Routen, Gesamttest |
| `2026-10-13-monitoring-04-frontend.md` | 11–14 | SVG-Diagramme, Dashboard (Werte, Sparkline, Host-Karte), Tab „Monitoring“, Einstellungs-Karte, Playwright |
| `2026-10-13-monitoring-05-ci-docs.md` | 15–16 | CI-Abnahme mit echtem Paper-Server (Alerts über Zustellversuche an `ntfy.invalid` im Log und den Alert-Zustand der API), Doku, Checkliste `docs/dev/acceptance-tp5.md` |

## Ausführung (Agent-Teams, Tests auf der Test-VM)

Wie TP4 (`docs/superpowers/plans/2026-10-12-backups-00-overview.md`, Abschnitt „Ausführung“) und `CLAUDE.md`: auf dem Mac nur `make check` (neue Dateien vorher `git add -N`), Tests per `make test-vm SUITE=…` (lange Läufe auf der VM detached starten und das Log abfragen; SSH ohne Sandbox), TDD, CI `CI OK` als Gate, nichts Systemnahes auf dem Mac, nie auf `main`, ein Worktree pro Teammate (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`), Mergen nur durch den Nutzer.

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/monitoring-contracts` | Teammate „mon-contracts“ | 1–3 | – |
| B1 | `feat/monitoring-collector` | Teammate „mon-collector“ | 4–7 | A gemerged |
| B2 | `feat/monitoring-alerts-api` | Teammate „mon-alerts“ | 8–10 | A gemerged |
| B3 | `feat/monitoring-frontend` | Teammate „mon-frontend“ | 11–14 | A gemerged |
| C | `feat/monitoring-ci-docs` | Teammate „mon-ci-docs“ | 15 (16 koordiniert der Lead) | A gemerged; CI-Abnahme grün erst nach B1+B2 |

Dateieigentum in Phase B (disjunkt):

- **B1:** `internal/monitoring/collector.go`, `slp.go`, `tps.go`, `host.go`, `host_linux.go`, `host_other.go`, `write.go`, `rollup.go` und deren Tests (`*_test.go` mit gleichem Präfix), `internal/monitoring/testdata/**`. Eigene Testhelfer und zusätzliche Fakes von B1 tragen das Präfix `c` (z. B. `cInsertRaw`, `cIntp`).
- **B2:** `internal/monitoring/alerts.go`, `read.go`, `api.go`, `overview.go` und deren Tests (ohne `routes_test.go`/`settings_test.go`), `internal/monitoring/e2e_test.go`. Eigene Testhelfer und zusätzliche Fakes von B2 tragen das Präfix `a` (z. B. `aInsertRaw`).
- **Gemeinsam (nur Phase A, danach unverändert):** `internal/monitoring/types.go` (inkl. aller Takt-, Grenz- und Alert-Konstanten), `handler.go`, `settings.go` (fertige Einstellungs-Routen `GET`/`PUT /monitoring/settings` und `PUT /servers/{id}/alerts`), `settings_test.go`, `routes_test.go` (Routenvertrag aller sieben Routen), `helpers_test.go` (Fakes `fakeAgent`, `fakeConsole` inkl. Zähler gleichzeitiger Aufrufe, `pingServer`, Recording-Notifier, `env`/`newEnv`, `eventually`). B1 und B2 nutzen diese Fakes und Konstanten statt eigener Nachbauten bzw. Parallelnamen. Bedarf → Lead.
- **B3:** `web/src/components/charts/**` (neu), `web/src/features/monitoring/**` (neu), `web/src/features/dashboard/**`, `web/src/features/servers/**` (nur Tab-Einbindung und zugehörige Tests), `web/src/features/settings/**`, Locale-Dateien (außer `errors.*`) inkl. `web/src/i18n/i18n.test.ts` (`DYNAMIC_KEYS`), `web/e2e/**`, `web/playwright.config.ts` (nur `testMatch`).
- **C:** `scripts/**`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md`.
- **Nur Phase A** ändert `internal/agent/**`, `internal/config/**`, `internal/notify/**`, `internal/audit/types.go`, `internal/store/**`, `internal/servers/**`, `api/openapi.yaml`, `web/src/api/**`, `web/src/mocks/**`, `cmd/panel/**`, außerdem `web/src/features/servers/status.test.ts` (nur `down_alerts: null` in Testdaten, damit `tsc` grün bleibt).
- TP4 läuft parallel aus (#55 `internal/files/**`, #56 `scripts/**`/`docs/**`): Phase A berührt diese Dateien nicht; Phase C startet erst nach dem Merge von #56.

## Global Constraints

- Alle Constraints aus TP1–TP4 und `CLAUDE.md` gelten (`clock.Clock`, Kontext überall, Go-Tests nur Standardbibliothek, keine neuen Abhängigkeiten, Lint-Ausnahmen gezielt, Conventional Commits; jede Commit-Nachricht endet mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`).
- Rechte: `metrics`, `metrics/live` → `rbac.Server(rbac.PermServerView, "id")`; `overview` → `rbac.Authenticated()` (filtert nach `server.view`, Host nur für Admins); `host`, `settings` (GET), `alerts` → `rbac.Admin()`; `settings` (PUT) → `rbac.Admin().WithSudo()`.
- Takte: Sammeln 30 s, TPS 60 s, live TPS 5 s für 30 s nach dem letzten `live`-Abruf, Verdichtung alle 5 min; Ping-Frist 3 s; höchstens 4 Pings und je Server 1 RCON gleichzeitig; ein Durchlauf > 30 s lässt den nächsten Takt aus.
- Aufbewahrung: `metrics_raw` 24 h, `metrics_5m` 7 d, `metrics_1h` 30 d.
- SLP-Grenzen: VarInt ≤ 5 Bytes, Paket ≤ 64 KiB, JSON ≤ 64 KiB; `players.online`/`max` 0–100 000; ≤ 12 Namen à ≤ 16 Zeichen `[A-Za-z0-9_]`.
- TPS: `tps` 0–1000, `mspt` 0–60 000; sonst Wert fehlt.
- Alert-Einstellungen (Defaults / Grenzen): `disk_min_percent` 10 (1–50), `disk_min_bytes` 10 GiB (1 GiB–10 TiB), `down_after_min` 3 (1–60), `crash_restarts` 3 (2–20), `crash_window_min` 10 (1–120); Schonfrist nach Start 5 min; Erinnerung alle 24 h; Plattenhysterese 2 Prozentpunkte bzw. 2 GiB.
- Neue Notify-Ereignisse: `server.down`, `server.recovered`, `server.crash_loop`, `host.disk_low`, `host.disk_ok`. Neue Audit-Aktionen: `monitoring.settings_update`, `server.alerts_update`. Keine neuen Fehlercodes.
- Neue `panel.toml`-Schlüssel: `monitoring_ping_addr` (Default `"127.0.0.1:25565"`, Dev `""` = aus; sonst `host:port`), `instances_dir` (Default `"/srv/mc-panel"`, Dev `<root>/.dev/mc`).
- Frontend: Texte `de`/`en`, keine Inline-Skripte, keine CDNs, kein `dangerouslySetInnerHTML`, MSW-Mock für jede Operation; Diagramme nur SVG aus eigenem Code.

### Präzisierung gegenüber der Spec

- **TPS-Quelle (Spec §3.3):** Statt die Minecraft-Version aufzulösen, erkennt der Sammler die Quelle pro Server durch Versuch: zuerst `tick query`; liefert das keine gültige Antwort, bei `paper`/`purpur` nur `mspt` (`tps = min(20, 1000 / mspt)`, damit es bei zwei RCON-Zeilen pro Minute bleibt); sonst `none`. Meldet `tick query` ein angehaltenes Spiel („frozen“), gilt `tps = 0`. Das Ergebnis gilt pro Server und `revision` (ein Versionswechsel erhöht die Revision) und wird erst nach drei gültigen bzw. drei ungültigen Antworten festgelegt; bis dahin `tps_source = unknown`.

## Review Focus

1. **Feindliche Ping-Antworten:** riesige oder endlose Pakete, VarInt mit 6+ Bytes, negatives `online`, Namen mit Steuerzeichen/HTML, JSON-Bombe, Verbindung bleibt offen ohne Daten; erwartet: Abbruch nach 3 s bzw. Grenzen, `up = 0`, keine Auswirkung auf andere Server oder den Takt. → Aufgabe 4.
2. **Ausfälle im Takt:** agent antwortet nicht, Docker hängt, RCON hängt 10 s, ein Server startet gerade; erwartet: kein Stau (Takt lässt aus), fehlende Werte statt falscher Nullen, kein Down-Alert während Schonfrist oder Daten-Sperre. → Aufgaben 6, 8.
3. **Alert-Flattern und Neustarts:** Server pendelt zwischen erreichbar/nicht erreichbar, panel startet mit feuerndem Alert neu, Zeitpunkt der Erinnerung, Server wird gelöscht oder gestoppt, `restart_count` springt durch Neuanlage auf 0; erwartet: genau eine Meldung pro Übergang, keine doppelte nach Neustart, kein Alert für gelöschte/gestoppte Server. → Aufgabe 8.
4. **Zeit und Verdichtung:** Uhrsprung, Lücken im Takt, Verdichtung über Intervallgrenzen, doppelte Ausführung, gewichtete Stundenmittel; erwartet: idempotent, keine doppelten oder verlorenen Intervalle. → Aufgabe 7.
5. **Rechte und Datenmenge:** Nutzer ohne `server.view` sieht Server in `overview` nicht, Nicht-Admin keine Host-Werte; 30-d-Abfrage liefert höchstens 721 Punkte pro Metrik; DB-Wachstum bleibt im Rahmen. → Aufgaben 9, 10.

## Dateistruktur

```
internal/agent/agentapi/instances.go, instances/manager.go, docker/types.go (1)  restart_count
internal/config/config.go                                     (1)  monitoring_ping_addr, instances_dir
internal/notify/notify.go, sender.go; internal/audit/types.go (1)  Ereignisse, Aktionen
internal/store/migrations/00007_monitoring.sql                (2)  Tabellen aus Spec §4
internal/store/queries/monitoring.sql → monitoring.sql.go     (2)  Monitoring…-Queries
internal/servers/servers.go, view.go                          (2)  Löschen räumt auf, down_alerts im Detail
api/openapi.yaml, web/src/api/schema.d.ts, types.ts           (3)  Vertrag
internal/monitoring/types.go, handler.go, helpers_test.go     (3)  Gerüst (V4)
internal/monitoring/collector.go, alerts.go, api.go (Stubs)   (3)  ab B1/B2 echt
cmd/panel/app.go, main.go                                     (3)  Verdrahtung
web/src/mocks/handlers/monitoring.ts, db.ts                   (3)  Mocks
internal/monitoring/slp.go                                    (4)  Server-List-Ping
internal/monitoring/tps.go                                    (5)  TPS-Erkennung und -Parser
internal/monitoring/host*.go                                  (5)  Host-Leser
internal/monitoring/collector.go, write.go                    (6)  Durchlauf, Live-Modus, Schreiben
internal/monitoring/rollup.go                                 (7)  Verdichtung, Löschen
internal/monitoring/alerts.go                                 (8)  Regeln, Zustand, Versand
internal/monitoring/read.go, api.go, overview.go              (9)  Lese-Schicht, Routen
internal/monitoring/e2e_test.go                               (10) Gesamttest über cmd/panel-ähnlichen Aufbau
web/src/components/charts/**                                  (11) LineChart, Sparkline, Skalen
web/src/features/dashboard/**                                 (12) Werte, Sparkline, Host-Karte
web/src/features/monitoring/**, servers/server-detail-page.tsx (13) Tab
web/src/features/settings/**                                  (13) Karte „Monitoring & Alerts“
web/e2e/monitoring.spec.ts                                    (14) Playwright
scripts/ci-instances.sh, .github/workflows/ci.yml             (15) Abnahme
docs/dev/acceptance-tp5.md, docs/dev/README.md, CLAUDE.md, README.md (16)
```

## Verträge (verbindlich)

### V1 – agent, Konfiguration, Querschnitt (Aufgabe 1)

- `agentapi.InstanceStatus` bekommt `RestartCount int \`json:"restart_count"\``; der agent füllt es aus `ContainerInfo.RestartCount` (Docker-Inspect-Feld `RestartCount`, neu in `docker.ContainerInfo`), 0 wenn Inspect fehlschlägt.
- `config.Panel` bekommt `MonitoringPingAddr string \`toml:"monitoring_ping_addr"\`` und `InstancesDir string \`toml:"instances_dir"\``; Defaults und Validierung wie in den Global Constraints (`net.SplitHostPort` + Port 1–65535, oder `""`; `InstancesDir` absolut und sauber).
- `internal/notify/notify.go`: `EventServerDown EventType = "server.down"`, `EventServerRecovered = "server.recovered"`, `EventServerCrashLoop = "server.crash_loop"`, `EventHostDiskLow = "host.disk_low"`, `EventHostDiskOK = "host.disk_ok"`; Titel in `sender.go` im Stil der übrigen.
- `internal/audit/types.go`: `ActionMonitoringSettingsUpdate = "monitoring.settings_update"`, `ActionServerAlertsUpdate = "server.alerts_update"`.

### V2 – Daten (Aufgabe 2)

Migration `internal/store/migrations/00007_monitoring.sql` mit den Tabellen aus Spec §4 (wortgleich; `Down` löscht sie in umgekehrter Reihenfolge). Queries `internal/store/queries/monitoring.sql`:

```sql
-- name: MonitoringInsertRaw :exec
INSERT OR REPLACE INTO metrics_raw (series, metric, at, value) VALUES (?, ?, ?, ?);

-- name: MonitoringListRaw :many
SELECT metric, at, value FROM metrics_raw WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at) ORDER BY metric, at;

-- name: MonitoringList5m :many
SELECT metric, at, avg, min, max FROM metrics_5m WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at) ORDER BY metric, at;

-- name: MonitoringList1h :many
SELECT metric, at, avg, min, max FROM metrics_1h WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at) ORDER BY metric, at;

-- name: MonitoringRollup5m :exec
INSERT OR REPLACE INTO metrics_5m (series, metric, at, avg, min, max, samples)
SELECT series, metric, (at / 300000) * 300000, avg(value), min(value), max(value), count(*)
FROM metrics_raw WHERE at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
GROUP BY series, metric, (at / 300000) * 300000;

-- name: MonitoringRollup1h :exec
INSERT OR REPLACE INTO metrics_1h (series, metric, at, avg, min, max, samples)
SELECT series, metric, (at / 3600000) * 3600000, sum(avg * samples) / sum(samples), min(min), max(max), sum(samples)
FROM metrics_5m WHERE at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
GROUP BY series, metric, (at / 3600000) * 3600000;

-- name: MonitoringPruneRaw :exec
DELETE FROM metrics_raw WHERE at < ?;
-- name: MonitoringPrune5m :exec
DELETE FROM metrics_5m WHERE at < ?;
-- name: MonitoringPrune1h :exec
DELETE FROM metrics_1h WHERE at < ?;

-- name: MonitoringDeleteSeriesRaw :exec
DELETE FROM metrics_raw WHERE series = ?;
-- name: MonitoringDeleteSeries5m :exec
DELETE FROM metrics_5m WHERE series = ?;
-- name: MonitoringDeleteSeries1h :exec
DELETE FROM metrics_1h WHERE series = ?;

-- name: MonitoringGetSettings :one
SELECT * FROM monitoring_settings WHERE id = 1;
-- name: MonitoringUpdateSettings :exec
UPDATE monitoring_settings SET disk_min_percent = ?, disk_min_bytes = ?, down_after_min = ?,
    crash_restarts = ?, crash_window_min = ?, updated_at = ? WHERE id = 1;

-- name: MonitoringListServerAlerts :many
SELECT server_id, down_alerts FROM server_alerts ORDER BY server_id;
-- name: MonitoringSetServerAlerts :exec
INSERT INTO server_alerts (server_id, down_alerts) VALUES (?, ?)
ON CONFLICT (server_id) DO UPDATE SET down_alerts = excluded.down_alerts;

-- name: MonitoringListAlertState :many
SELECT * FROM alert_state ORDER BY key;
-- name: MonitoringUpsertAlertState :exec
INSERT INTO alert_state (key, firing, since, last_notified, detail) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET firing = excluded.firing, since = excluded.since,
    last_notified = excluded.last_notified, detail = excluded.detail;
-- name: MonitoringDeleteAlertState :exec
DELETE FROM alert_state WHERE key = ?;
```

- `internal/servers` (Aufgabe 2): Das Löschen eines Servers ruft in derselben Transaktion `MonitoringDeleteSeriesRaw/5m/1h(id)` und `MonitoringDeleteAlertState` für `server.down:<id>` und `server.crash_loop:<id>` (`server_alerts` per `ON DELETE CASCADE`). `serverJSON` (Detail) bekommt `DownAlerts *bool \`json:"down_alerts"\``: für Admins der Wert (fehlende Zeile = `true`), sonst `null`.

### V3 – panel-API (Aufgabe 3, `api/openapi.yaml`, Tag `monitoring`)

| Pfad | Methode | operationId | Request | Antworten |
|---|---|---|---|---|
| `/servers/{id}/metrics` | get | `getServerMetrics` | Query `range` (`MetricsRange`, Pflicht) | `200 MetricsSeries`, 400, 401, 403, 404 |
| `/servers/{id}/metrics/live` | get | `getServerLiveMetrics` | – | `200 LiveMetrics`, 401, 403, 404, 503 (`agent_unavailable`) |
| `/monitoring/overview` | get | `getMonitoringOverview` | – | `200 MonitoringOverview`, 401 |
| `/monitoring/host` | get | `getHostMetrics` | Query `range` | `200 MetricsSeries`, 400, 401, 403 |
| `/monitoring/settings` | get | `getMonitoringSettings` | – | `200 MonitoringSettings`, 401, 403 |
| `/monitoring/settings` | put | `updateMonitoringSettings` | `UpdateMonitoringSettingsRequest` | `200 MonitoringSettings`, 400, 401, 403 (`sudo_required`) |
| `/servers/{id}/alerts` | put | `updateServerAlerts` | `UpdateServerAlertsRequest` | `204`, 400, 401, 403, 404 |

Schemas (Request-Bodies `additionalProperties: false`; Zeit Unix-ms `integer`/`int64`; nullable als `type: [X, 'null']`):

```
MetricsRange       enum 1h | 24h | 7d | 30d
MetricPoint        array of number, minItems 4, maxItems 4   ([at, avg, min, max])
MetricsSeries      {range: MetricsRange; step_ms: integer; series: object (additionalProperties: MetricPoint[])}
                   Server-Metriken: up, players, players_max, tps, mspt, cpu_percent, memory_bytes, memory_limit_bytes
                   Host-Metriken: cpu_percent, memory_used, memory_total, load1, disk_free, disk_total
TpsSource          enum tick_query | paper | none | unknown
LiveMetrics        {at: integer|null; up: boolean|null; players, players_max: integer|null; player_names: string[];
                    tps, mspt, cpu_percent: number|null; memory_bytes, memory_limit_bytes: integer|null; tps_source: TpsSource}
AlertKind          enum server.down | server.crash_loop | host.disk_low
OverviewServer     {id: integer; up: boolean|null; players, players_max: integer|null; tps, cpu_percent: number|null;
                    memory_bytes, memory_limit_bytes: integer|null; sparkline: array of [integer, number] (minItems/maxItems 2);
                    alerts: AlertKind[]}
HostSnapshot       {at: integer|null; cpu_percent, load1: number|null; memory_used, memory_total, disk_free, disk_total: integer|null;
                    alerts: AlertKind[]}
MonitoringOverview {servers: OverviewServer[]; host: HostSnapshot|null}
FiringAlert        {kind: AlertKind; server_id: integer|null; server_name: string|null; since: integer; detail: string}
MonitoringSettings {disk_min_percent, disk_min_bytes, down_after_min, crash_restarts, crash_window_min: integer; alerts: FiringAlert[]}
UpdateMonitoringSettingsRequest {disk_min_percent, disk_min_bytes, down_after_min, crash_restarts, crash_window_min: integer}
UpdateServerAlertsRequest {down_alerts: boolean}
```

- `step_ms`: 30 000 (1h), 300 000 (24h), 3 600 000 (7d, 30d). Für `1h` gilt `min = max = avg`.
- `ServerDetail` (vorhanden) bekommt `down_alerts: boolean|null`.
- `sparkline`: Spieler der letzten Stunde aus `metrics_raw`, höchstens 120 Punkte.

### V4 – panel-intern (Aufgabe 3; `internal/monitoring/types.go` und `handler.go` sind ab Phase A fest)

```go
package monitoring // github.com/Bernotese/mc-panel/internal/monitoring

type Deps struct {
	DB           *sql.DB
	Clock        clock.Clock
	Audit        audit.Recorder
	Notifier     notify.Notifier          // nil = notify.Nop{}
	Instances    agentapi.InstancesClient // nil = no agent: no server samples
	Console      agentapi.ConsoleClient   // RCON for TPS; nil = no TPS
	Gate         servers.DataGate         // nil = never gated
	PingAddr     string                   // "" = no ping
	InstancesDir string                   // statfs target; "" = no disk values
	Log          *slog.Logger
}

func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
// Run blocks until ctx is done: collector, rollup loop and alert evaluation.
func (h *Handler) Run(ctx context.Context)

// Snapshot is the result of one collector pass (types.go).
type Snapshot struct {
	At      time.Time
	Agent   bool // the agent answered this pass
	Docker  bool // the agent reported Docker ok
	Servers []ServerSample
	Host    HostSample
}

type ServerSample struct {
	ID             int64
	Name           string
	DesiredRunning bool
	Gated          bool // DataGate held during the pass
	State          agentapi.ContainerState
	StartedAt      int64 // Unix ms, 0 = not running
	RestartCount   int
	Revision       int64
	Up             *bool
	Players        *int
	PlayersMax     *int
	PlayerNames    []string
	TPS            *float64
	MSPT           *float64
	TPSSource      string // "tick_query" | "paper" | "none" | "unknown"
	CPUPercent     *float64
	MemoryBytes    *int64
	MemoryLimit    *int64
}

type HostSample struct {
	CPUPercent  *float64
	Load1       *float64
	MemoryUsed  *int64
	MemoryTotal *int64
	DiskFree    *int64
	DiskTotal   *int64
}

// collector (collector.go, B1) – methods the handler and B2 rely on:
func newCollector(d Deps) *collector
func (c *collector) Run(ctx context.Context, onPass func(context.Context, Snapshot)) // blocks; first pass right away, then every 30 s
func (c *collector) Latest() Snapshot                     // copy of the last pass (zero value before the first)
func (c *collector) Watch(serverID int64)                 // live mode for 30 s from now
func (c *collector) Live(serverID int64) (ServerSample, bool) // freshest sample incl. 5-s TPS
func runRollup(ctx context.Context, d Deps)               // rollup.go, B1; blocks

// alerter (alerts.go, B2):
func newAlerter(d Deps) *alerter
func (a *alerter) Evaluate(ctx context.Context, s Snapshot) // called after every pass
func (a *alerter) Firing(ctx context.Context) []FiringAlert
type FiringAlert struct { Kind string; ServerID int64; ServerName string; Since time.Time; Detail string }

// routes (api.go, B2): func (h *Handler) routes(api *rbac.Router)
```

`handler.go` (Phase A): `Handler{d Deps; coll *collector; alerts *alerter}`; `NewHandler` erzeugt beide; `Routes` ruft `h.routes(api)`; `Run` startet `coll.Run(ctx, alerts.Evaluate)` und `runRollup(ctx, d)` und wartet auf beide. Phase A liefert Stubs: `collector.go` (Run wartet auf ctx, `Latest` leer, `Live` false), `alerts.go` (no-op, `Firing` leer), `api.go` (`routes` registriert alle sieben Routen mit den Regeln aus den Global Constraints; die vier Lese-Routen `metrics`, `metrics/live`, `overview`, `host` sind Stubs mit gültigen leeren Strukturen, die drei Einstellungs-Routen zeigen auf die fertigen Handler in `settings.go`), `rollup.go` (`runRollup` wartet auf ctx). B1 ersetzt `collector.go` und `rollup.go`, B2 ersetzt `alerts.go` und `api.go`; in `api.go` ersetzt B2 nur die vier Lese-Routen (`register` für diese vier) und registriert die Einstellungs-Routen weiter mit `h.handleGetSettings`, `h.handlePutSettings`, `h.handlePutServerAlerts` aus `settings.go`. Audit-Details der Einstellungen: `{from, to}`.

`cmd/panel/app.go`: `monHandler := monitoring.NewHandler(monitoring.Deps{… Gate: servers.AnyGate(backupsH, filesH), PingAddr: cfg.MonitoringPingAddr, InstancesDir: cfg.InstancesDir})`, `Routes`, `Run` in `run`.

### V5 – Verhalten zwischen B1 und B2

- `Snapshot.Servers` enthält jeden Server mit Instanz, auch gestoppte (`State`, `DesiredRunning`, `Gated` gesetzt; Messwerte `nil`). Ohne agent-Antwort: `Agent = false`, `Servers` leer – B2 wertet dann keine Server-Alerts aus (kein Down-Alert, weil der agent fehlt).
- `Up`: `true` = gültige Ping-Antwort, `false` = Container läuft, Ping scheitert; `nil` = kein Ping (Container nicht `running` oder `PingAddr = ""`).
- Ist `Docker = false` (agent antwortet, Docker nicht), liefert B1 Server mit `State = missing`; B2 leitet daraus keine Server-Alerts ab (wie bei `Agent = false`), bestehende Alerts bleiben unverändert.
- B1 schreibt nur nicht-`nil` Werte; `up` als 0/1.
- Metrik-Konstanten (`HostSeries`, `MetricUp`, … , `TPSSource…`) stehen in `types.go` (Phase A); B1 und B2 nutzen nur diese.
- `Live` liefert nach `Watch` die TPS/MSPT des 5-s-Takts, sonst das letzte Sample aus `Latest`.
- TPS/MSPT in `Latest` und `Live`: B1 trägt den letzten gültigen Wert bis 90 s Alter weiter (auch in Durchläufen ohne TPS-Abfrage); gespeichert werden nur frisch gemessene Werte (also höchstens einer pro TPS-Takt).
- Down/Crash-Loop (B2): `restarting` und `exited` gelten als nicht erreichbar; die Schonfrist von 5 min gilt ab jedem `started_at` (also nach jedem Neustart neu); solange `server.crash_loop` für einen Server feuert, wird `server.down` für ihn nicht zusätzlich gemeldet (der Down-Zähler läuft nicht).

### V6 – MSW-Mock (Aufgabe 3, `web/src/mocks/handlers/monitoring.ts`)

- Deterministische Zeitreihen pro Server (Seed = Server-ID) für alle Bereiche: `survival` (läuft, im Verlauf 0–12 Spieler im Tagesverlauf, live immer mindestens 1 Spieler mit Namen, TPS 19,5–20, ein Einbruch auf 14 vor 3 h), `creative` (im Seed gestoppt → alle Werte `null`, `tps_source = none`), `modded` (im Monitoring-Mock „soll laufen, nicht erreichbar“: `up = false` seit 5 min, Alert `server.down` – bewusste Mock-Vereinfachung, sein Server-Status im übrigen Mock bleibt `stopped`), `lobby` (ohne Instanz → alle Metrik-Schlüssel mit `[]`, wie das Backend). Host mit `disk_free` knapp über der Schwelle; ein Schalter im Mock (`db.monitoring.diskLow`) lässt `host.disk_low` feuern.
- `live` wechselt alle 5 s leicht die Werte (Zeit aus `Date.now()`), Namensliste bei `survival` (nie leer); `down_alerts` ist für Admins `true` (außer nach `PUT /servers/{id}/alerts`).
- `overview` filtert nach `serverPermissions` (`server.view`), `host = null` für Nicht-Admins.
- `PUT /monitoring/settings` validiert wie die Global Constraints und verlangt Sudo wie die übrigen Sudo-Mocks; `PUT /servers/{id}/alerts` nur Admin.
