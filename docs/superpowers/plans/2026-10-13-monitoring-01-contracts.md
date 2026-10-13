# mc-panel Teilprojekt 5 – Phase A: Verträge (Aufgaben 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die gemeinsamen Grundlagen für B1 (Sammler), B2 (Alerts + API), B3 (Frontend) und C (CI/Doku) auf `main` bringen:
- Der agent meldet `restart_count` pro Instanz; `panel.toml` kennt `monitoring_ping_addr` und `instances_dir`.
- Fünf neue `notify`-Ereignisse und zwei Audit-Aktionen.
- Migration 00007 mit den Monitoring-Tabellen, `Monitoring…`-Queries; `servers` räumt beim Löschen eines Servers seine Monitoring-Daten weg und liefert `down_alerts` im `Server`-Schema.
- Der panel-Vertrag steht: sieben OpenAPI-Operationen, TS-Typen, das Paket `internal/monitoring` mit festen Typen, Handler-Gerüst, fertigen Einstellungs-Routen, Stubs für Sammler/Alerts/Lese-Routen, gemeinsamen Test-Fakes, verdrahtet in `cmd/panel`, dazu MSW-Mocks.

**Architecture:** Drei Aufgaben auf einem Branch `feat/monitoring-contracts` (Teammate „mon-contracts“, nacheinander).

| Aufgabe | Inhalt |
|---|---|
| 1 | agent `restart_count` (Docker-Inspect, dockertest-Fake), `panel.toml`-Schlüssel, Notify-Ereignisse, Audit-Aktionen |
| 2 | Migration 00007, Queries, `servers` (Löschen räumt auf, `down_alerts`) samt OpenAPI-Feld und Mock |
| 3 | Sieben Monitoring-Operationen in OpenAPI, Paket `internal/monitoring` (Typen, Handler, Einstellungen, Stubs, Fakes), Verdrahtung, TS-Aliase, MSW-Mock |

**Tech Stack:** Go (Standardbibliothek, chi, sqlc 1.31.1, goose), OpenAPI 3.1 + `openapi-typescript`, MSW 2, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md` · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-13-monitoring-00-overview.md` (V1–V6; „Ausführung“, „Global Constraints“ und „Review Focus“ gelten hier vollständig).

## Ausführung dieser Phase

- **Worktree:** `git fetch origin && git worktree add ../mc-panel-wt/feat-monitoring-contracts -b feat/monitoring-contracts origin/main`, dann alle Befehle dort ausführen (einmalig `(cd web && npm ci)`). Ein Draft-PR für alle drei Aufgaben, ein Commit pro Aufgabe (Fix-Commits erlaubt).
- **Lokal nur `make check`** und `make generate` (Aufgaben 2 und 3). Neue Dateien vor `make check` mit `git add -N <pfad>` vormerken. Meldet `make check` `gofmt needed`: `gofmt -w <datei>`.
- **„Run: go test …“ / „npx vitest …“** heißt: auf der Test-VM. Ganze Suite `make test-vm SUITE=go|web|lint`; Einzeltests `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-monitoring-contracts && PATH=/usr/local/go/bin:$PATH go test ./internal/… -run …'` bzw. `…/web && PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run …'`. SSH braucht eine Bash ohne Sandbox; lange Läufe detached starten (`nohup … > log 2>&1 &`) und das Log abfragen. Vor jedem Push die betroffenen Suites, vor „ready for review“ `make test-vm` (alle).
- **Fertig:** VM-Lauf und CI (`gh pr checks <pr> --watch --interval 30`, Fehler über `gh run view <id> --log-failed`) grün → Lead informieren. Der Nutzer mergt, erst dann starten B1, B2, B3.
- **Dateien:** Phase A ändert ausschließlich die in den „Files“-Blöcken genannten Dateien. TP4 läuft parallel aus (#55 `internal/files/**`, #56 `scripts/**`/`docs/**`); diese Dateien fasst Phase A nicht an. Liegt beim Push ein neuer `main` vor, `git merge origin/main` (keine Überschneidung erwartet).

## Abweichungen von der Übersicht (bewusst, vom Lead zu bestätigen)

1. **Benannte sqlc-Argumente** (`sqlc.arg(...)`) in `MonitoringList*` und `MonitoringPrune*` statt `?`. Gleiche SQL-Semantik, aber lesbare Parameter: `MonitoringListRawParams{Series, FromAt, ToAt}`, `MonitoringPruneRaw(ctx, before int64)`.
2. **Zusätzliche Query `MonitoringGetServerAlerts :one`** (ein Server). Die `PATCH /servers/{id}`-Antwort braucht den Wert eines Servers; `GET`-Liste und -Detail laden alle über `MonitoringListServerAlerts`.
3. **`down_alerts` im gemeinsamen Schema `Server`** (Liste, Detail, Anlegen, Ändern), nicht nur im Detail: Die OpenAPI hat kein eigenes Detail-Schema. Für Admins der Wert (fehlende Zeile = `true`), sonst `null`.
4. **Neue Datei `internal/monitoring/settings.go` (+ `settings_test.go`), Eigentum Phase A und danach fest:** `GET`/`PUT /monitoring/settings` und `PUT /servers/{id}/alerts` sind schon vollständig (Validierung, Speichern, Audit). `api.go` (B2) registriert alle sieben Routen und ruft diese Handler auf; die vier Lese-Routen sind in Phase A Stubs mit gültigen leeren Antworten. `api_test.go` mit den Stub-Tests gehört B2 (B2 ersetzt die Stub-Tests). `routes_test.go` (Routenvertrag) gehört Phase A.
5. **Audit-Ziel der Einstellungen ist `monitoring`** (wie `security`, `servers`), nicht `settings` (Spec §6.3).
6. **`web/src/features/servers/status.test.ts`** (sonst B3): Die typisierte `Server`-Vorlage bekommt `down_alerts: null`, sonst schlägt `tsc` fehl. Nur diese Zeile.
7. **Mock-Szenario `modded`:** Im Monitoring-Mock „soll laufen, nicht erreichbar“ (`up: false`, Alert `server.down`, `tps_source: 'none'`), obwohl sein Server-Status im Mock `stopped` bleibt; die übrigen Server-Mocks bleiben unverändert. `creative` ist im Seed gestoppt und liefert deshalb nur `null`-Werte (statt „läuft, `tps_source = none`“ aus V6).
8. **`internal/monitoring` ist ein internes Testpaket:** Alle Tests liegen in `package monitoring` (B1/B2 testen unexportierte Typen). `helpers_test.go` definiert `t0`, `env`, `newEnv`, `decode`, `errCode` und alle Fakes; B1 und B2 definieren diese Namen nicht neu.

### Nachträge des Leads (Schnittstellen-Review)

- Die Abweichungen 4 und 7 sind bestätigt und in der Übersicht nachgezogen (Dateieigentum, V4, V6).
- `types.go`, die Stubs und `helpers_test.go` sichern jede Konstante, jedes Stub-Feld und jede Fake-Methode per `var _` ab, damit `unused` in Phase A nicht anschlägt (B1/B2 nutzen sie).
- `fakeConsole` zählt gleichzeitige `Rcon`-Aufrufe pro Server (`peak(id)`), damit B1 „höchstens 1 RCON pro Server“ ohne eigenen Fake prüfen kann.
- Mock: `survival` liefert live immer mindestens einen Spieler mit Namen; Server ohne Werte liefern alle Metrik-Schlüssel mit `[]` (wie B2).
- Default-Titel in `sender.go` sind die Titel aus `alerts.go` (Plan 03): „Server down“, „Server back online“, „Crash loop“, „Low disk space on the host“, „Disk space OK again“.
- sqlc-Zeilentyp für `MonitoringListServerAlerts` ist voraussichtlich das Modell `store.ServerAlert`.

---

### Task 1: agent `restart_count`, `panel.toml`, Notify, Audit

**Files:**
- Modify: `internal/agent/docker/types.go` (`ContainerInfo.RestartCount`)
- Modify: `internal/agent/docker/dockertest/daemon.go` (`Container.RestartCount`, `SetRestartCount`, Inspect liefert das Feld)
- Modify: `internal/agent/agentapi/instances.go` (`InstanceStatus.RestartCount`)
- Modify: `internal/agent/agentapi/instances_test.go`
- Modify: `internal/agent/instances/manager.go` (`Status` übernimmt `RestartCount`)
- Modify: `internal/agent/instances/manager_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/notify/notify.go`, `internal/notify/sender.go`, `internal/notify/sender_test.go`
- Modify: `internal/audit/types.go`

**Interfaces:**
- Consumes: vorhandene `instances.Manager.Status`, `dockertest.Daemon`, `config.Panel`, `notify.EventType`, `audit`-Konstanten.
- Produces (V1):

```go
// docker
type ContainerInfo struct { /* … */ RestartCount int `json:"RestartCount"` }
// dockertest
type Container struct { /* … */ RestartCount int }
func (d *Daemon) SetRestartCount(name string, n int)
// agentapi
type InstanceStatus struct { /* … nach ExitCode */ RestartCount int `json:"restart_count"` }
// config
type Panel struct { /* … */ MonitoringPingAddr string `toml:"monitoring_ping_addr"`; InstancesDir string `toml:"instances_dir"` }
// DefaultPanel: MonitoringPingAddr "127.0.0.1:25565", InstancesDir DefaultInstancesDir ("/srv/mc-panel")
// DevPanel(root): MonitoringPingAddr "", InstancesDir <root>/.dev/mc
// notify
const EventServerDown, EventServerRecovered, EventServerCrashLoop, EventHostDiskLow, EventHostDiskOK EventType
// audit
const ActionMonitoringSettingsUpdate = "monitoring.settings_update"; ActionServerAlertsUpdate = "server.alerts_update"
```

- [ ] **Step 1: Tests schreiben (rot)**

`internal/agent/agentapi/instances_test.go` anhängen (Imports `encoding/json`, `strings` ergänzen, falls nicht vorhanden):

```go
// TP5 V1: the restart count travels as restart_count.
func TestInstanceStatusJSONHasRestartCount(t *testing.T) {
	b, err := json.Marshal(agentapi.InstanceStatus{ID: 1, RestartCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"restart_count":3`) {
		t.Fatalf("json = %s", b)
	}
}
```

`internal/agent/instances/manager_test.go` anhängen:

```go
// TP5 V1: Status reports Docker's RestartCount of the container (crash-loop detection).
func TestStatusReportsRestartCount(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	if s := e.status(t, 1); s.RestartCount != 0 {
		t.Fatalf("restart_count of a fresh container = %d", s.RestartCount)
	}
	e.d.SetRestartCount("mcpanel-mc-1", 3)
	if s := e.status(t, 1); s.RestartCount != 3 {
		t.Fatalf("restart_count = %d, want 3", s.RestartCount)
	}
}
```

`internal/config/config_test.go` anhängen:

```go
// TP5: monitoring keys with production and dev defaults.
func TestMonitoringDefaults(t *testing.T) {
	p := config.DefaultPanel()
	if p.MonitoringPingAddr != "127.0.0.1:25565" || p.InstancesDir != "/srv/mc-panel" {
		t.Fatalf("defaults = %q %q", p.MonitoringPingAddr, p.InstancesDir)
	}
	d := config.DevPanel("/repo")
	if d.MonitoringPingAddr != "" || d.InstancesDir != "/repo/.dev/mc" {
		t.Fatalf("dev = %q %q", d.MonitoringPingAddr, d.InstancesDir)
	}
}

func TestLoadPanelMonitoringKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	content := "public_url = \"https://mc.example.org\"\nmonitoring_ping_addr = \"[::1]:25566\"\ninstances_dir = \"/data/mc\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadPanel(path, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.MonitoringPingAddr != "[::1]:25566" || p.InstancesDir != "/data/mc" {
		t.Fatalf("loaded = %q %q", p.MonitoringPingAddr, p.InstancesDir)
	}
}

func TestMonitoringKeyValidation(t *testing.T) {
	for _, tc := range []struct {
		ping, dir string
		ok        bool
	}{
		{"", "", true},
		{"127.0.0.1:25565", "/srv/mc-panel", true},
		{"localhost:1", "/x", true},
		{"127.0.0.1", "/x", false},       // no port
		{":25565", "/x", false},          // no host
		{"127.0.0.1:0", "/x", false},     // port 0
		{"127.0.0.1:65536", "/x", false}, // port too large
		{"127.0.0.1:http", "/x", false},  // named port
		{"127.0.0.1:25565", "relative", false},
		{"127.0.0.1:25565", "/srv/../etc", false},
		{"127.0.0.1:25565", "/srv/mc-panel/", false},
	} {
		p := config.DefaultPanel()
		p.PublicURL = "https://panel.example.com"
		p.MonitoringPingAddr, p.InstancesDir = tc.ping, tc.dir
		err := p.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("ping %q dir %q: err = %v, want ok=%v", tc.ping, tc.dir, err, tc.ok)
		}
	}
}
```

`internal/notify/sender_test.go` anhängen (Paket `notify`):

```go
// TP5: every monitoring event has its own title.
func TestDefaultTitlesCoverMonitoringEvents(t *testing.T) {
	for _, e := range []EventType{EventServerDown, EventServerRecovered, EventServerCrashLoop, EventHostDiskLow, EventHostDiskOK} {
		if defaultTitles[e] == "" {
			t.Errorf("no default title for %s", e)
		}
	}
	if EventServerDown != "server.down" || EventServerRecovered != "server.recovered" || EventServerCrashLoop != "server.crash_loop" ||
		EventHostDiskLow != "host.disk_low" || EventHostDiskOK != "host.disk_ok" {
		t.Fatal("event type values differ from the contract (V1)")
	}
}
```

- [ ] **Step 2: `make check` (rot)**

Run: `make check`
Expected: FAIL (`RestartCount`, `SetRestartCount`, `MonitoringPingAddr`, `InstancesDir`, `EventServerDown` … undefined).

- [ ] **Step 3: agent implementieren**

`internal/agent/docker/types.go`, in `ContainerInfo` nach `State`:

```go
	// RestartCount counts restarts by the restart policy since the container was created
	// (Docker inspect "RestartCount"; TP5 crash-loop detection).
	RestartCount int `json:"RestartCount"`
```

`internal/agent/docker/dockertest/daemon.go`: in `Container` nach `ExitCode` das Feld `RestartCount int // reported by inspect`; im Inspect-Zweig (`case OpInspect:`) beim `docker.ContainerInfo{…}` zusätzlich `RestartCount: c.RestartCount,`; neue Methode nach `SetState`:

```go
// SetRestartCount sets the RestartCount inspect reports for the container called name.
func (d *Daemon) SetRestartCount(name string, n int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.containers[name]; ok {
		c.RestartCount = n
	}
}
```

`internal/agent/agentapi/instances.go`, in `InstanceStatus` nach `ExitCode`:

```go
	RestartCount     int            `json:"restart_count"` // Docker restarts since creation (TP5)
```

`internal/agent/instances/manager.go`, in `Status` im Zweig `case err == nil:` nach `st.ExitCode = info.State.ExitCode`:

```go
			st.RestartCount = info.RestartCount
```

- [ ] **Step 4: Konfiguration implementieren**

`internal/config/config.go`, in `Panel` nach `GeoIPPath`:

```go
	// MonitoringPingAddr is where the panel sends Server List Pings (mc-router); "" = no ping
	// (TP5 §3.2).
	MonitoringPingAddr string `toml:"monitoring_ping_addr"`
	// InstancesDir is statfs'ed for the host disk values (TP5 §3.4); "" = no disk values.
	InstancesDir string `toml:"instances_dir"`
```

In `DefaultPanel()` ergänzen: `MonitoringPingAddr: "127.0.0.1:25565", InstancesDir: DefaultInstancesDir,`. In `DevPanel` nach `p.GeoIPPath = …`:

```go
	p.MonitoringPingAddr = "" // no mc-router on the Mac
	p.InstancesDir = filepath.Join(root, ".dev", "mc")
```

In `Validate()` vor `p.trusted = …`:

```go
	if err := validatePingAddr(p.MonitoringPingAddr); err != nil {
		errs = append(errs, err)
	}
	if p.InstancesDir != "" && (!filepath.IsAbs(p.InstancesDir) || filepath.Clean(p.InstancesDir) != p.InstancesDir) {
		errs = append(errs, fmt.Errorf("instances_dir: %q must be a clean absolute path", p.InstancesDir))
	}
```

Nach `validateListen` (Import `strconv` ergänzen):

```go
// validatePingAddr accepts "" (no ping) or host:port with a numeric port 1–65535.
func validatePingAddr(a string) error {
	if a == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(a)
	n, perr := strconv.Atoi(port)
	if err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("monitoring_ping_addr: %q must be host:port (port 1–65535) or empty", a)
	}
	return nil
}
```

- [ ] **Step 5: Notify und Audit**

`internal/notify/notify.go`, in der `const`-Liste nach `EventBackupFailed`:

```go
	EventServerDown         EventType = "server.down"
	EventServerRecovered    EventType = "server.recovered"
	EventServerCrashLoop    EventType = "server.crash_loop"
	EventHostDiskLow        EventType = "host.disk_low"
	EventHostDiskOK         EventType = "host.disk_ok"
```

`internal/notify/sender.go`, in `defaultTitles` nach `EventBackupFailed`:

```go
	EventServerDown:         "Server down",
	EventServerRecovered:    "Server back online",
	EventServerCrashLoop:    "Crash loop",
	EventHostDiskLow:        "Low disk space on the host",
	EventHostDiskOK:         "Disk space OK again",
```

`internal/audit/types.go`, nach `ActionBackupScheduleUpdate`:

```go
	ActionMonitoringSettingsUpdate = "monitoring.settings_update"
	ActionServerAlertsUpdate       = "server.alerts_update"
```

(Spaltenausrichtung: `gofmt -w` der drei Dateien.)

- [ ] **Step 6: `make check` (grün), Tests auf der VM**

Run: `make check`, dann `make test-vm SUITE=go`
Expected: PASS, insbesondere `TestInstanceStatusJSONHasRestartCount`, `TestStatusReportsRestartCount`, `TestMonitoring*`, `TestLoadPanelMonitoringKeys`, `TestDefaultTitlesCoverMonitoringEvents`.

- [ ] **Step 7: Commit, Push, Draft-PR, CI**

```bash
git add internal/agent internal/config internal/notify internal/audit/types.go
git commit -m "feat(monitoring): restart count, monitoring config keys, events and audit actions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/monitoring-contracts
gh pr create --draft --title "feat(monitoring): contracts for sub-project 5" --body "Phase A of sub-project 5 (plan docs/superpowers/plans/2026-10-13-monitoring-01-contracts.md). Body is completed with task 3.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
gh pr checks --watch --interval 30
```

---

### Task 2: Migration, Queries, `servers`

**Files:**
- Create: `internal/store/migrations/00007_monitoring.sql`
- Create: `internal/store/queries/monitoring.sql` → generiert `internal/store/monitoring.sql.go` (und Änderungen in `internal/store/models.go`)
- Modify: `internal/store/store_test.go` (Tabellenliste)
- Create: `internal/store/monitoring_test.go`
- Modify: `internal/servers/servers.go` (Löschen räumt Monitoring-Daten, `down_alerts` in Antworten)
- Modify: `internal/servers/view.go` (`serverJSON.DownAlerts`, `viewData.downAlerts`)
- Create: `internal/servers/monitoring_test.go`
- Modify: `api/openapi.yaml` (`Server.down_alerts`)
- Generated: `web/src/api/schema.d.ts`
- Modify: `web/src/mocks/handlers/servers.ts` (`toServer` liefert `down_alerts`), `web/src/mocks/db.ts` (`monitoring`-Zustand, nur `downAlerts` in dieser Aufgabe)
- Modify: `web/src/features/servers/status.test.ts` (Abweichung 6)

**Interfaces:**
- Consumes: `storetest.Open/CreateServer`, `servers`-Test-Env (`newEnv`, `admin`, `userP`, `grant`, `createInstance`, `do`, `decode`).
- Produces (V2 + Abweichungen 1–3): Queries `MonitoringInsertRaw`, `MonitoringListRaw/5m/1h(ctx, MonitoringList…Params{Series, FromAt, ToAt})`, `MonitoringRollup5m/1h(ctx, MonitoringRollup…Params{FromAt, ToAt})`, `MonitoringPruneRaw/5m/1h(ctx, before int64)`, `MonitoringDeleteSeriesRaw/5m/1h(ctx, series int64)`, `MonitoringGetSettings`, `MonitoringUpdateSettings`, `MonitoringListServerAlerts`, `MonitoringGetServerAlerts(ctx, serverID) (int64, error)`, `MonitoringSetServerAlerts`, `MonitoringListAlertState`, `MonitoringUpsertAlertState`, `MonitoringDeleteAlertState(ctx, key string)`. Typen (nach sqlc; bei Abweichung Tests anpassen und im PR vermerken): `store.MonitoringSetting`, `store.AlertState`, `store.MonitoringListRawRow{Metric string; At int64; Value float64}`, `store.MonitoringList5mRow`/`MonitoringList1hRow{Metric string; At int64; Avg, Min, Max float64}`, `store.ServerAlert{ServerID, DownAlerts int64}` (Modell, weil `MonitoringListServerAlerts` alle Spalten wählt; erwarteter Name, der Teammate gleicht nach `make generate` ab).
- `serverJSON.DownAlerts *bool \`json:"down_alerts"\``.

- [ ] **Step 1: Store-Tests schreiben (rot)**

`internal/store/store_test.go`: In `TestMigrateIsIdempotentAndCreatesTables` die Tabellenliste nach `"backup_schedules"` um `"metrics_raw", "metrics_5m", "metrics_1h", "monitoring_settings", "server_alerts", "alert_state"` erweitern.

Neue Datei `internal/store/monitoring_test.go`:

```go
package store_test

import (
	"math"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const (
	min5 = int64(300_000)
	hour = int64(3_600_000)
)

func insertRaw(t *testing.T, q *store.Queries, series int64, metric string, at int64, v float64) {
	t.Helper()
	if err := q.MonitoringInsertRaw(t.Context(), store.MonitoringInsertRawParams{Series: series, Metric: metric, At: at, Value: v}); err != nil {
		t.Fatal(err)
	}
}

func TestMonitoringRawRollupAndPrune(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	base := 100 * hour // aligned to an hour
	// series 7 players: 1, 3 in the first 5 min; 5 in the second; host cpu in the first
	insertRaw(t, q, 7, "players", base, 1)
	insertRaw(t, q, 7, "players", base+30_000, 3)
	insertRaw(t, q, 7, "players", base+min5, 5)
	insertRaw(t, q, 0, "cpu_percent", base, 50)
	// INSERT OR REPLACE: the same point twice keeps the newer value
	insertRaw(t, q, 7, "players", base+30_000, 3)

	raw, err := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: 7, FromAt: base, ToAt: base + hour})
	if err != nil || len(raw) != 3 || raw[0].At != base || raw[2].Value != 5 {
		t.Fatalf("raw = %+v %v", raw, err)
	}

	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: base, ToAt: base + hour}); err != nil {
		t.Fatal(err)
	}
	// idempotent
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: base, ToAt: base + hour}); err != nil {
		t.Fatal(err)
	}
	m5, err := q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: 7, FromAt: base, ToAt: base + hour})
	if err != nil || len(m5) != 2 {
		t.Fatalf("5m = %+v %v", m5, err)
	}
	if m5[0].At != base || m5[0].Avg != 2 || m5[0].Min != 1 || m5[0].Max != 3 || m5[1].At != base+min5 || m5[1].Avg != 5 {
		t.Fatalf("5m values = %+v", m5)
	}

	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: base, ToAt: base + hour}); err != nil {
		t.Fatal(err)
	}
	h1, err := q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: 7, FromAt: base, ToAt: base + hour})
	if err != nil || len(h1) != 1 {
		t.Fatalf("1h = %+v %v", h1, err)
	}
	// weighted by samples: (2*2 + 5*1) / 3 = 3
	if math.Abs(h1[0].Avg-3) > 1e-9 || h1[0].Min != 1 || h1[0].Max != 5 {
		t.Fatalf("1h values = %+v", h1[0])
	}

	if err := q.MonitoringPruneRaw(ctx, base+min5); err != nil {
		t.Fatal(err)
	}
	raw, _ = q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: 7, FromAt: 0, ToAt: math.MaxInt64})
	if len(raw) != 1 || raw[0].At != base+min5 {
		t.Fatalf("after prune: %+v", raw)
	}
	if err := q.MonitoringPrune5m(ctx, base+hour); err != nil {
		t.Fatal(err)
	}
	if m5, _ = q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: 7, FromAt: 0, ToAt: math.MaxInt64}); len(m5) != 0 {
		t.Fatalf("5m after prune: %+v", m5)
	}
	if err := q.MonitoringPrune1h(ctx, base+hour); err != nil {
		t.Fatal(err)
	}
	if h1, _ = q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: 7, FromAt: 0, ToAt: math.MaxInt64}); len(h1) != 0 {
		t.Fatalf("1h after prune: %+v", h1)
	}
}

func TestMonitoringDeleteSeriesKeepsOthers(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	insertRaw(t, q, 7, "players", 0, 1)
	insertRaw(t, q, 8, "players", 0, 2)
	insertRaw(t, q, 0, "load1", 0, 0.5)
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: 0, ToAt: hour}); err != nil {
		t.Fatal(err)
	}
	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: 0, ToAt: hour}); err != nil {
		t.Fatal(err)
	}
	for _, del := range []func() error{
		func() error { return q.MonitoringDeleteSeriesRaw(ctx, 7) },
		func() error { return q.MonitoringDeleteSeries5m(ctx, 7) },
		func() error { return q.MonitoringDeleteSeries1h(ctx, 7) },
	} {
		if err := del(); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []int64{7, 8, 0} {
		raw, _ := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: s, FromAt: 0, ToAt: hour})
		m5, _ := q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: s, FromAt: 0, ToAt: hour})
		h1, _ := q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: s, FromAt: 0, ToAt: hour})
		want := 1
		if s == 7 {
			want = 0
		}
		if len(raw) != want || len(m5) != want || len(h1) != want {
			t.Errorf("series %d: raw %d 5m %d 1h %d, want %d", s, len(raw), len(m5), len(h1), want)
		}
	}
}

func TestMonitoringSettingsServerAlertsAndState(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	s, err := q.MonitoringGetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := store.MonitoringSetting{ID: 1, DiskMinPercent: 10, DiskMinBytes: 10 << 30, DownAfterMin: 3, CrashRestarts: 3, CrashWindowMin: 10, UpdatedAt: 0}
	if s != want {
		t.Fatalf("defaults = %+v, want %+v", s, want)
	}
	if err := q.MonitoringUpdateSettings(ctx, store.MonitoringUpdateSettingsParams{
		DiskMinPercent: 20, DiskMinBytes: 5 << 30, DownAfterMin: 1, CrashRestarts: 2, CrashWindowMin: 5, UpdatedAt: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if s, _ = q.MonitoringGetSettings(ctx); s.DiskMinPercent != 20 || s.CrashWindowMin != 5 || s.UpdatedAt != 9 {
		t.Fatalf("updated = %+v", s)
	}

	a := storetest.CreateServer(t, db, "survival")
	if _, err := q.MonitoringGetServerAlerts(ctx, a); err == nil {
		t.Fatal("a server without row must answer sql.ErrNoRows")
	}
	for _, v := range []int64{0, 1, 0} {
		if err := q.MonitoringSetServerAlerts(ctx, store.MonitoringSetServerAlertsParams{ServerID: a, DownAlerts: v}); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := q.MonitoringGetServerAlerts(ctx, a); err != nil || v != 0 {
		t.Fatalf("down_alerts = %d %v", v, err)
	}
	if rows, _ := q.MonitoringListServerAlerts(ctx); len(rows) != 1 || rows[0].ServerID != a || rows[0].DownAlerts != 0 {
		t.Fatalf("list = %+v", rows)
	}
	// ON DELETE CASCADE
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", a); err != nil {
		t.Fatal(err)
	}
	if rows, _ := q.MonitoringListServerAlerts(ctx); len(rows) != 0 {
		t.Fatalf("server_alerts survived the server: %+v", rows)
	}

	key := "server.down:7"
	if err := q.MonitoringUpsertAlertState(ctx, store.MonitoringUpsertAlertStateParams{Key: key, Firing: 1, Since: 5, LastNotified: 5, Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := q.MonitoringUpsertAlertState(ctx, store.MonitoringUpsertAlertStateParams{Key: key, Firing: 0, Since: 5, LastNotified: 8, Detail: ""}); err != nil {
		t.Fatal(err)
	}
	st, err := q.MonitoringListAlertState(ctx)
	if err != nil || len(st) != 1 || st[0] != (store.AlertState{Key: key, Firing: 0, Since: 5, LastNotified: 8, Detail: ""}) {
		t.Fatalf("state = %+v %v", st, err)
	}
	if err := q.MonitoringDeleteAlertState(ctx, key); err != nil {
		t.Fatal(err)
	}
	if st, _ = q.MonitoringListAlertState(ctx); len(st) != 0 {
		t.Fatalf("state after delete = %+v", st)
	}
}
```

- [ ] **Step 2: servers-Tests schreiben (rot)**

Neue Datei `internal/servers/monitoring_test.go`:

```go
package servers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type alertsView struct {
	ID         int64 `json:"id"`
	DownAlerts *bool `json:"down_alerts"`
}

// TP5 Abweichung 3: admins see down_alerts (missing row = true), users null.
func TestServerJSONCarriesDownAlertsForAdmins(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	on := createInstance(t, e.db, "survival")
	off := createInstance(t, e.db, "creative")
	if err := store.New(e.db).MonitoringSetServerAlerts(t.Context(), store.MonitoringSetServerAlertsParams{ServerID: off, DownAlerts: 0}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]bool{on: true, off: false} {
		got := decode[alertsView](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &admin))
		if got.DownAlerts == nil || *got.DownAlerts != want {
			t.Errorf("server %d: down_alerts = %v, want %v", id, got.DownAlerts, want)
		}
	}
	list := decode[[]alertsView](t, e.do(http.MethodGet, "/servers", nil, &admin))
	for _, v := range list {
		if v.DownAlerts == nil || *v.DownAlerts != (v.ID != off) {
			t.Errorf("list %d: down_alerts = %v", v.ID, v.DownAlerts)
		}
	}

	uid := storetest.CreateUser(t, e.db, "bob", "user")
	grant(t, e.db, uid, on, "server.view")
	bob := userP(uid, "bob")
	if got := decode[alertsView](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", on), nil, &bob)); got.DownAlerts != nil {
		t.Fatalf("user sees down_alerts = %v", *got.DownAlerts)
	}
}

// TP5 V2: deleting a server removes its monitoring series and alert states, nothing else.
func TestDeleteRemovesMonitoringData(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	gone := storetest.CreateServer(t, e.db, "gone")
	keep := storetest.CreateServer(t, e.db, "keep")
	q := store.New(e.db)
	ctx := t.Context()
	for _, s := range []int64{gone, keep, 0} {
		if err := q.MonitoringInsertRaw(ctx, store.MonitoringInsertRawParams{Series: s, Metric: "players", At: 1, Value: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: 0, ToAt: 300_000}); err != nil {
		t.Fatal(err)
	}
	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: 0, ToAt: 3_600_000}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{fmt.Sprintf("server.down:%d", gone), fmt.Sprintf("server.crash_loop:%d", gone), fmt.Sprintf("server.down:%d", keep), "host.disk_low"} {
		if err := q.MonitoringUpsertAlertState(ctx, store.MonitoringUpsertAlertStateParams{Key: k, Firing: 1, Since: 1, LastNotified: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", gone), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		table  string
		series int64
		want   int
	}{
		{"metrics_raw", gone, 0}, {"metrics_5m", gone, 0}, {"metrics_1h", gone, 0},
		{"metrics_raw", keep, 1}, {"metrics_5m", keep, 1}, {"metrics_1h", keep, 1},
		{"metrics_raw", 0, 1},
	} {
		var n int
		//nolint:gosec // G202: table name comes from the fixed list above
		if err := e.db.QueryRowContext(ctx, "SELECT count(*) FROM "+tc.table+" WHERE series = ?", tc.series).Scan(&n); err != nil || n != tc.want {
			t.Errorf("%s series %d: %d rows (err %v), want %d", tc.table, tc.series, n, err, tc.want)
		}
	}
	st, _ := q.MonitoringListAlertState(ctx)
	keys := []string{}
	for _, s := range st {
		keys = append(keys, s.Key)
	}
	if fmt.Sprint(keys) != fmt.Sprintf("[host.disk_low server.down:%d]", keep) {
		t.Fatalf("alert states after delete: %v", keys)
	}
}
```

- [ ] **Step 3: `make check` (rot)**

Run: `make check`
Expected: FAIL (`MonitoringInsertRaw` … undefined).

- [ ] **Step 4: Migration und Queries**

`internal/store/migrations/00007_monitoring.sql`:

```sql
-- +goose Up
CREATE TABLE metrics_raw (
    series INTEGER NOT NULL,   -- 0 = host, otherwise servers.id
    metric TEXT    NOT NULL,
    at     INTEGER NOT NULL,   -- Unix ms, tick time
    value  REAL    NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_5m (
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL, -- start of the interval
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_1h (
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL,
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE monitoring_settings (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    disk_min_percent INTEGER NOT NULL DEFAULT 10,           -- 1–50
    disk_min_bytes   INTEGER NOT NULL DEFAULT 10737418240,  -- 1 GiB – 10 TiB
    down_after_min   INTEGER NOT NULL DEFAULT 3,            -- 1–60
    crash_restarts   INTEGER NOT NULL DEFAULT 3,            -- 2–20
    crash_window_min INTEGER NOT NULL DEFAULT 10,           -- 1–120
    updated_at       INTEGER NOT NULL
);
INSERT INTO monitoring_settings (id, updated_at) VALUES (1, 0);
CREATE TABLE server_alerts (
    server_id   INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    down_alerts INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE alert_state (
    key           TEXT PRIMARY KEY,  -- e.g. "server.down:7", "server.crash_loop:7", "host.disk_low"
    firing        INTEGER NOT NULL,
    since         INTEGER NOT NULL,
    last_notified INTEGER NOT NULL,
    detail        TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE alert_state;
DROP TABLE server_alerts;
DROP TABLE monitoring_settings;
DROP TABLE metrics_1h;
DROP TABLE metrics_5m;
DROP TABLE metrics_raw;
```

`internal/store/queries/monitoring.sql` (V2 mit Abweichungen 1 und 2):

```sql
-- name: MonitoringInsertRaw :exec
INSERT OR REPLACE INTO metrics_raw (series, metric, at, value) VALUES (?, ?, ?, ?);

-- name: MonitoringListRaw :many
SELECT metric, at, value FROM metrics_raw
WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY metric, at;

-- name: MonitoringList5m :many
SELECT metric, at, avg, min, max FROM metrics_5m
WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY metric, at;

-- name: MonitoringList1h :many
SELECT metric, at, avg, min, max FROM metrics_1h
WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY metric, at;

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
DELETE FROM metrics_raw WHERE at < sqlc.arg(before);

-- name: MonitoringPrune5m :exec
DELETE FROM metrics_5m WHERE at < sqlc.arg(before);

-- name: MonitoringPrune1h :exec
DELETE FROM metrics_1h WHERE at < sqlc.arg(before);

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

-- name: MonitoringGetServerAlerts :one
SELECT down_alerts FROM server_alerts WHERE server_id = ?;

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

Run: `make generate`
Expected: `internal/store/monitoring.sql.go` neu, `internal/store/models.go` mit `MetricsRaw`, `Metrics5m`, `Metrics1h`, `MonitoringSetting`, `ServerAlert`, `AlertState`. Weichen generierte Namen (z. B. Feld `Min` bei `min(min)`) ab, Tests aus Step 1/2 anpassen und im PR vermerken. Die aggregierten Spalten von `MonitoringRollup*` sind `:exec` – sie erzeugen keine Row-Typen.

- [ ] **Step 5: servers implementieren**

`internal/servers/view.go`: In `serverJSON` nach `Status`:

```go
	// DownAlerts is the per-server switch of the TP5 down/crash-loop alerts; admins only
	// (missing row = on), null for other users.
	DownAlerts *bool `json:"down_alerts"`
```

In `viewData` das Feld `downAlerts map[int64]bool // server_alerts rows (missing = on)` ergänzen; in `loadViewData` nach dem Laden der Instanzen:

```go
	alerts, err := h.q.MonitoringListServerAlerts(ctx)
	if err != nil {
		return viewData{}, err
	}
	d.downAlerts = make(map[int64]bool, len(alerts))
	for _, a := range alerts {
		d.downAlerts[a.ServerID] = a.DownAlerts != 0
	}
```

`func (d viewData) server(...)` setzt nach `view(…)`:

```go
	out, err := view(s, perms, in, d.baseDomain, d.snap)
	if err != nil {
		return serverJSON{}, err
	}
	on, ok := d.downAlerts[s.ID]
	if !ok {
		on = true
	}
	out.DownAlerts = &on
	return out, nil
```

`internal/servers/servers.go`:
- `redactFor`: zusätzlich `v.DownAlerts = nil` für Nicht-Admins.
- Antwort von `POST /servers` (Zeile mit `view(s, allPerms(), &in, …)` im Anlegen): danach `on := true; out.DownAlerts = &on` (neuer Server, keine Zeile).
- Antwort von `PATCH /servers/{id}` (zweiter `view(…)`-Aufruf): danach

```go
	on, err := h.downAlerts(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out.DownAlerts = &on
```

  mit neuer Hilfsfunktion (Import `errors`, `database/sql` vorhanden):

```go
// downAlerts is the TP5 per-server alert switch; a missing row means on.
func (h *Handler) downAlerts(ctx context.Context, id int64) (bool, error) {
	v, err := h.q.MonitoringGetServerAlerts(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return v != 0, err
}
```

- `deleteServerRows`: in der Transaktion direkt nach `RbacDeleteServer` (vor `h.inst.Remove`):

```go
	if err := deleteMonitoring(ctx, h.q.WithTx(tx), s.ID); err != nil {
		httpx.Internal(w, r, err)
		return false, false
	}
```

  mit (Import `fmt` vorhanden):

```go
// deleteMonitoring removes the TP5 series and alert states of a deleted server (server_alerts
// goes by ON DELETE CASCADE). The keys follow internal/monitoring's alertKey; servers cannot
// import monitoring (monitoring imports servers for DataGate).
func deleteMonitoring(ctx context.Context, q *store.Queries, id int64) error {
	for _, del := range []func(context.Context, int64) error{
		q.MonitoringDeleteSeriesRaw, q.MonitoringDeleteSeries5m, q.MonitoringDeleteSeries1h,
	} {
		if err := del(ctx, id); err != nil {
			return err
		}
	}
	for _, kind := range []string{"server.down", "server.crash_loop"} {
		if err := q.MonitoringDeleteAlertState(ctx, fmt.Sprintf("%s:%d", kind, id)); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 6: OpenAPI, Mock, Frontend-Vorlage**

`api/openapi.yaml`, Schema `Server`: `required` um `down_alerts` erweitern und als letzte Eigenschaft:

```yaml
        down_alerts: { type: [boolean, 'null'], description: 'Down/crash-loop alerts for this server (TP5); admins only, null for other users' }
```

Run: `make generate`

`web/src/mocks/db.ts`: In `MockDb` ein Feld `monitoring: MockMonitoring` und vor `MockDb`:

```ts
/** Monitoring state of the mock (TP5 V6); series are generated, not stored. */
export interface MockMonitoring {
  downAlerts: Record<number, boolean> // missing = on
  settings: { disk_min_percent: number; disk_min_bytes: number; down_after_min: number; crash_restarts: number; crash_window_min: number }
  diskLow: boolean // lets host.disk_low fire
}

export function defaultMonitoring(): MockMonitoring {
  return {
    downAlerts: {},
    settings: { disk_min_percent: 10, disk_min_bytes: 10 * GIB, down_after_min: 3, crash_restarts: 3, crash_window_min: 10 },
    diskLow: false,
  }
}
```

In `seed()` nach `backupJobs: {},`: `monitoring: defaultMonitoring(),`.

`web/src/mocks/handlers/servers.ts`, `toServer`: als letzte Eigenschaft `down_alerts: admin ? (db.monitoring.downAlerts[s.id] ?? true) : null,`.

`web/src/features/servers/status.test.ts` Zeile 24: im Objekt `down_alerts: null,` ergänzen (Abweichung 6).

- [ ] **Step 7: `make check`, Tests auf der VM**

Run: `make check`, dann `make test-vm SUITE=go` und `SUITE=web`
Expected: PASS (`TestMonitoring*` in `internal/store`, `TestServerJSONCarriesDownAlertsForAdmins`, `TestDeleteRemovesMonitoringData`, alle bestehenden `servers`- und Web-Tests).

- [ ] **Step 8: Commit, Push, CI**

```bash
git add internal/store internal/servers api/openapi.yaml web/src/api/schema.d.ts web/src/mocks web/src/features/servers/status.test.ts
git commit -m "feat(monitoring): metrics tables and queries, cleanup on server delete, down_alerts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks --watch --interval 30
```

---

### Task 3: panel-Vertrag – OpenAPI, Paket `internal/monitoring`, Verdrahtung, Mocks

**Files:**
- Modify: `api/openapi.yaml` (Tag `monitoring`, sieben Operationen, Schemas aus V3)
- Generated: `web/src/api/schema.d.ts`
- Modify: `web/src/api/types.ts` (Aliase)
- Create: `internal/monitoring/types.go` (V4-Typen, Konstanten, `alertKey`) – fest ab Phase A
- Create: `internal/monitoring/handler.go` (`Deps`, `Handler`, `NewHandler`, `Routes`, `Run`, `record`) – fest
- Create: `internal/monitoring/settings.go` (Einstellungen, Server-Schalter) – fest (Abweichung 4)
- Create: `internal/monitoring/collector.go` (Stub; B1 ersetzt)
- Create: `internal/monitoring/rollup.go` (Stub; B1 ersetzt)
- Create: `internal/monitoring/alerts.go` (Stub; B2 ersetzt)
- Create: `internal/monitoring/api.go` (Routen + Stub-Lese-Routen; B2 ersetzt die Lese-Routen)
- Create: `internal/monitoring/helpers_test.go` (gemeinsame Fakes und Env) – fest
- Create: `internal/monitoring/routes_test.go` (Routenvertrag) – fest
- Create: `internal/monitoring/settings_test.go` – fest
- Create: `internal/monitoring/api_test.go` (Stub-Tests; B2 ersetzt)
- Modify: `cmd/panel/app.go` (Handler bauen, `Routes`, `Run`)
- Modify: `cmd/panel/integration_test.go` (`TestRouteCoverage` 95 → 102)
- Create: `web/src/mocks/handlers/monitoring.ts`
- Modify: `web/src/mocks/handlers.ts` (`monitoringOperations`)
- Modify: `web/src/mocks/handlers.test.ts` (95 → 102, Mock-Verhalten)

**Interfaces:**
- Consumes: Task 1 (`RestartCount`, Config-Schlüssel, Ereignisse, Aktionen), Task 2 (Queries), `servers.DataGate`/`AnyGate`, `rbac`, `httpx`, `audit`, `notify`, `agentapi`.
- Produces: V3 (OpenAPI), V4 vollständig (`types.go`, `handler.go`), dazu die folgenden Namen, auf die B1/B2 bauen:

```go
// types.go (Konstanten; Werte aus den Global Constraints)
const (
	HostSeries int64 = 0
	MetricUp, MetricPlayers, MetricPlayersMax, MetricTPS, MetricMSPT, MetricCPU, MetricMemory, MetricMemoryLimit string
	MetricHostCPU, MetricMemUsed, MetricMemTotal, MetricLoad1, MetricDiskFree, MetricDiskTotal string
	TPSSourceTickQuery, TPSSourcePaper, TPSSourceNone, TPSSourceUnknown string // "tick_query", "paper", "none", "unknown"
	alertServerDown, alertCrashLoop, alertDiskLow string // "server.down", "server.crash_loop", "host.disk_low"
	collectEvery = 30 * time.Second; tpsEvery = time.Minute; liveTPSEvery = 5 * time.Second; liveWindow = 30 * time.Second
	rollupEvery = 5 * time.Minute; pingTimeout = 3 * time.Second; maxParallelPings = 4
	retainRaw = 24 * time.Hour; retain5m = 7 * 24 * time.Hour; retain1h = 30 * 24 * time.Hour
	startGrace = 5 * time.Minute; remindEvery = 24 * time.Hour
)
func alertKey(kind string, serverID int64) string // "server.down:7"; serverID 0 → kind (host alerts)

// handler.go
type Handler struct { d Deps; q *store.Queries; log *slog.Logger; coll *collector; alerts *alerter }
func (h *Handler) record(ctx context.Context, action, target string, res audit.Result, details map[string]any)
func serverTarget(id int64) string // "server:<id>"
const auditTarget = "monitoring"
// collector.Run (B1): erster Durchlauf sofort beim Start, danach alle collectEvery (30 s).
// NewHandler normalisiert Deps (Notifier nil → notify.Nop{}, Log nil → slog.Default()) und
// übergibt die normalisierten Deps an newCollector/newAlerter/runRollup: dort sind Notifier
// und Log nie nil; Instances, Console, Gate dürfen nil sein.

// settings.go (fest)
func (h *Handler) handleGetSettings(w http.ResponseWriter, r *http.Request)
func (h *Handler) handlePutSettings(w http.ResponseWriter, r *http.Request)
func (h *Handler) handlePutServerAlerts(w http.ResponseWriter, r *http.Request)
type settingsJSON struct { DiskMinPercent, DiskMinBytes, DownAfterMin, CrashRestarts, CrashWindowMin int64; Alerts []firingAlertJSON }
type firingAlertJSON struct { Kind string; ServerID *int64; ServerName *string; Since int64; Detail string }
func firingJSON(fs []FiringAlert) []firingAlertJSON // never nil

// api.go (B2 ersetzt die Lese-Handler, behält die Signatur von routes)
func (h *Handler) routes(api *rbac.Router)
```

Gemeinsame Test-Helfer (`helpers_test.go`, Paket `monitoring`; B1/B2 nutzen sie, definieren sie nicht neu):

```go
var t0 = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

// fakeAgent implements agentapi.InstancesClient.
func newFakeAgent() *fakeAgent                        // Docker ok, no instances
func (f *fakeAgent) set(st ...agentapi.InstanceStatus) // replaces the instance list (Docker ok)
func (f *fakeAgent) setDocker(state string)            // agentapi.DockerOK / DockerUnavailable
func (f *fakeAgent) setErr(err error)                  // Instances fails (nil = answer again)
func (f *fakeAgent) calls() int                        // number of Instances calls

// fakeConsole implements agentapi.ConsoleClient; RCON answers are scripted per server+command.
func newFakeConsole() *fakeConsole
func (f *fakeConsole) reply(id int64, cmd, out string, err error) // unknown commands: agentapi.ErrRconUnavailable
func (f *fakeConsole) setDelay(d time.Duration)                  // every Rcon waits d (or ctx)
func (f *fakeConsole) commands() []string                        // "7 tick query", in call order
func (f *fakeConsole) peak(id int64) int                          // most Rcon calls for id that ran at the same time

// pingServer is a real TCP listener speaking the Server List Ping status exchange.
func newPingServer(t *testing.T) *pingServer       // 127.0.0.1:<random>, closed on cleanup
func (p *pingServer) addr() string
func (p *pingServer) set(fqdn string, a pingAnswer) // fqdn = handshake server address
func (p *pingServer) hits(fqdn string) int
type pingAnswer struct {
	json string        // status JSON (framed as a proper packet)
	raw  []byte        // written as is instead of json (broken answers)
	hang bool          // read the request, never answer
	drop bool          // close right after the handshake
}

// env: real DB, rbac router, handler (no Run); opt may change Deps before NewHandler.
type env struct {
	t       *testing.T
	db      *sql.DB
	clk     *clock.Fake
	rec     *audit.MemoryRecorder
	agent   *fakeAgent
	console *fakeConsole
	notes   *notify.Recording
	h       *Handler
	mux     *chi.Mux
	rt      *rbac.Router
	rootID  int64
}
func newEnv(t *testing.T, opts ...func(*Deps)) *env
func (e *env) admin() principal.Principal       // with sudo
func (e *env) adminNoSudo() principal.Principal
func (e *env) user(name string, perms map[int64][]string) principal.Principal // creates the user and grants
func (e *env) server(name string) int64         // server + instance row (paper 1.21.11, desired running)
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder
func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T
func errCode(t *testing.T, rec *httptest.ResponseRecorder) string
func eventually(t *testing.T, what string, cond func() bool) // polls up to 5 s
```

- [ ] **Step 1: OpenAPI erweitern, `make generate`**

`api/openapi.yaml`: In `tags` nach `backups`:

```yaml
  - { name: monitoring, description: 'Monitoring: live values and history of servers and host, alert settings' }
```

Pfade (nach den Backup-Pfaden einfügen):

```yaml
  /servers/{id}/metrics:
    get:
      operationId: getServerMetrics
      tags: [monitoring]
      summary: History of a server (1h raw 30 s, 24h 5-min, 7d/30d 1-h averages)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/MetricsRangeQuery'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/MetricsSeries' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
  /servers/{id}/metrics/live:
    get:
      operationId: getServerLiveMetrics
      tags: [monitoring]
      summary: Current values of a server; each call keeps TPS sampling at 5 s for 30 s
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/LiveMetrics' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/alerts:
    put:
      operationId: updateServerAlerts
      tags: [monitoring]
      summary: Switch the down and crash-loop alerts of one server (admin; audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/UpdateServerAlertsRequest' } } }
      responses:
        '204': { description: Saved }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
  /monitoring/overview:
    get:
      operationId: getMonitoringOverview
      tags: [monitoring]
      summary: Compact values of every server the caller can view; host values for admins only
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/MonitoringOverview' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
  /monitoring/host:
    get:
      operationId: getHostMetrics
      tags: [monitoring]
      summary: History of the host (admin)
      parameters:
        - $ref: '#/components/parameters/MetricsRangeQuery'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/MetricsSeries' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
  /monitoring/settings:
    get:
      operationId: getMonitoringSettings
      tags: [monitoring]
      summary: Alert thresholds and the alerts firing right now (admin)
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/MonitoringSettings' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
    put:
      operationId: updateMonitoringSettings
      tags: [monitoring]
      summary: Set the alert thresholds (admin, sudo; audited)
      parameters:
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/UpdateMonitoringSettingsRequest' } } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/MonitoringSettings' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
```

Parameter (in `components/parameters`):

```yaml
    MetricsRangeQuery: { name: range, in: query, required: true, schema: { $ref: '#/components/schemas/MetricsRange' } }
```

Schemas (in `components/schemas`, nach den Backup-Schemas):

```yaml
    MetricsRange: { type: string, enum: [1h, 24h, 7d, 30d] }
    MetricPoint:
      type: array
      description: '[at (Unix ms), avg, min, max]; for 1h min = max = avg'
      items: { type: number }
      minItems: 4
      maxItems: 4
    MetricsSeries:
      type: object
      required: [range, step_ms, series]
      properties:
        range: { $ref: '#/components/schemas/MetricsRange' }
        step_ms: { type: integer, format: int64, description: '30000 (1h), 300000 (24h), 3600000 (7d, 30d)' }
        series:
          type: object
          description: 'Per metric (server: up, players, players_max, tps, mspt, cpu_percent, memory_bytes, memory_limit_bytes; host: cpu_percent, memory_used, memory_total, load1, disk_free, disk_total); missing values are gaps'
          additionalProperties: { type: array, items: { $ref: '#/components/schemas/MetricPoint' } }
    TpsSource: { type: string, enum: [tick_query, paper, none, unknown] }
    LiveMetrics:
      type: object
      required: [at, up, players, players_max, player_names, tps, mspt, cpu_percent, memory_bytes, memory_limit_bytes, tps_source]
      properties:
        at: { type: [integer, 'null'], format: int64 }
        up: { type: [boolean, 'null'] }
        players: { type: [integer, 'null'] }
        players_max: { type: [integer, 'null'] }
        player_names: { type: array, items: { type: string }, maxItems: 12 }
        tps: { type: [number, 'null'] }
        mspt: { type: [number, 'null'] }
        cpu_percent: { type: [number, 'null'] }
        memory_bytes: { type: [integer, 'null'], format: int64 }
        memory_limit_bytes: { type: [integer, 'null'], format: int64 }
        tps_source: { $ref: '#/components/schemas/TpsSource' }
    AlertKind: { type: string, enum: [server.down, server.crash_loop, host.disk_low] }
    SparklinePoint:
      type: array
      description: '[at (Unix ms), players]'
      items: { type: number }
      minItems: 2
      maxItems: 2
    OverviewServer:
      type: object
      required: [id, up, players, players_max, tps, cpu_percent, memory_bytes, memory_limit_bytes, sparkline, alerts]
      properties:
        id: { type: integer, format: int64 }
        up: { type: [boolean, 'null'] }
        players: { type: [integer, 'null'] }
        players_max: { type: [integer, 'null'] }
        tps: { type: [number, 'null'] }
        cpu_percent: { type: [number, 'null'] }
        memory_bytes: { type: [integer, 'null'], format: int64 }
        memory_limit_bytes: { type: [integer, 'null'], format: int64 }
        sparkline: { type: array, items: { $ref: '#/components/schemas/SparklinePoint' }, maxItems: 120 }
        alerts: { type: array, items: { $ref: '#/components/schemas/AlertKind' } }
    HostSnapshot:
      type: object
      required: [at, cpu_percent, load1, memory_used, memory_total, disk_free, disk_total, alerts]
      properties:
        at: { type: [integer, 'null'], format: int64 }
        cpu_percent: { type: [number, 'null'] }
        load1: { type: [number, 'null'] }
        memory_used: { type: [integer, 'null'], format: int64 }
        memory_total: { type: [integer, 'null'], format: int64 }
        disk_free: { type: [integer, 'null'], format: int64 }
        disk_total: { type: [integer, 'null'], format: int64 }
        alerts: { type: array, items: { $ref: '#/components/schemas/AlertKind' } }
    MonitoringOverview:
      type: object
      required: [servers, host]
      properties:
        servers: { type: array, items: { $ref: '#/components/schemas/OverviewServer' } }
        host: { anyOf: [{ $ref: '#/components/schemas/HostSnapshot' }, { type: 'null' }], description: null for non-admins }
    FiringAlert:
      type: object
      required: [kind, server_id, server_name, since, detail]
      properties:
        kind: { $ref: '#/components/schemas/AlertKind' }
        server_id: { type: [integer, 'null'], format: int64 }
        server_name: { type: [string, 'null'] }
        since: { type: integer, format: int64 }
        detail: { type: string }
    MonitoringSettings:
      type: object
      required: [disk_min_percent, disk_min_bytes, down_after_min, crash_restarts, crash_window_min, alerts]
      properties:
        disk_min_percent: { type: integer, minimum: 1, maximum: 50 }
        disk_min_bytes: { type: integer, format: int64, minimum: 1073741824, maximum: 10995116277760 }
        down_after_min: { type: integer, minimum: 1, maximum: 60 }
        crash_restarts: { type: integer, minimum: 2, maximum: 20 }
        crash_window_min: { type: integer, minimum: 1, maximum: 120 }
        alerts: { type: array, items: { $ref: '#/components/schemas/FiringAlert' } }
    UpdateMonitoringSettingsRequest:
      type: object
      additionalProperties: false
      required: [disk_min_percent, disk_min_bytes, down_after_min, crash_restarts, crash_window_min]
      properties:
        disk_min_percent: { type: integer, minimum: 1, maximum: 50 }
        disk_min_bytes: { type: integer, format: int64, minimum: 1073741824, maximum: 10995116277760 }
        down_after_min: { type: integer, minimum: 1, maximum: 60 }
        crash_restarts: { type: integer, minimum: 2, maximum: 20 }
        crash_window_min: { type: integer, minimum: 1, maximum: 120 }
    UpdateServerAlertsRequest:
      type: object
      additionalProperties: false
      required: [down_alerts]
      properties:
        down_alerts: { type: boolean }
```

Run: `make generate`
Expected: `web/src/api/schema.d.ts` enthält die sieben Operationen.

- [ ] **Step 2: Go-Tests schreiben (rot)**

`internal/monitoring/helpers_test.go`:

```go
package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// t0 is a fixed start for the fake clock (a Tuesday, 12:00 in Europe/Berlin).
var t0 = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)

// --- agent -------------------------------------------------------------------------------

type fakeAgent struct {
	mu     sync.Mutex
	resp   agentapi.InstancesResponse
	err    error
	ncalls int
}

var _ agentapi.InstancesClient = (*fakeAgent)(nil)

func newFakeAgent() *fakeAgent {
	return &fakeAgent{resp: agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: []agentapi.InstanceStatus{}}}
}

func (f *fakeAgent) set(st ...agentapi.InstanceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resp.Docker = agentapi.DockerOK
	f.resp.Instances = append([]agentapi.InstanceStatus{}, st...)
}

func (f *fakeAgent) setDocker(state string) { f.mu.Lock(); f.resp.Docker = state; f.mu.Unlock() }
func (f *fakeAgent) setErr(err error)       { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) calls() int             { f.mu.Lock(); defer f.mu.Unlock(); return f.ncalls }

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ncalls++
	if f.err != nil {
		return agentapi.InstancesResponse{}, f.err
	}
	resp := f.resp
	resp.Instances = append([]agentapi.InstanceStatus{}, f.resp.Instances...)
	return resp, nil
}

func (f *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error            { return nil }
func (f *fakeAgent) Power(context.Context, int64, agentapi.PowerAction) error        { return nil }
func (f *fakeAgent) Remove(context.Context, int64) error                             { return nil }

// --- RCON --------------------------------------------------------------------------------

type rconReply struct {
	out string
	err error
}

type fakeConsole struct {
	mu      sync.Mutex
	replies map[string]rconReply
	calls   []string
	delay   time.Duration
	active  map[int64]int
	maxAct  map[int64]int
}

var _ agentapi.ConsoleClient = (*fakeConsole)(nil)

func newFakeConsole() *fakeConsole {
	return &fakeConsole{replies: map[string]rconReply{}, active: map[int64]int{}, maxAct: map[int64]int{}}
}

func (f *fakeConsole) peak(id int64) int { f.mu.Lock(); defer f.mu.Unlock(); return f.maxAct[id] }

func (f *fakeConsole) reply(id int64, cmd, out string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[fmt.Sprintf("%d %s", id, cmd)] = rconReply{out: out, err: err}
}

func (f *fakeConsole) setDelay(d time.Duration) { f.mu.Lock(); f.delay = d; f.mu.Unlock() }

func (f *fakeConsole) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *fakeConsole) Rcon(ctx context.Context, id int64, command string) (string, error) {
	key := fmt.Sprintf("%d %s", id, command)
	f.mu.Lock()
	f.calls = append(f.calls, key)
	r, ok := f.replies[key]
	d := f.delay
	f.active[id]++
	f.maxAct[id] = max(f.maxAct[id], f.active[id])
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active[id]--; f.mu.Unlock() }()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if !ok {
		return "", agentapi.ErrRconUnavailable
	}
	return r.out, r.err
}

func (f *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, errors.New("fakeConsole: no logs")
}

func (f *fakeConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{Ops: []agentapi.PlayerOp{}, Whitelist: []agentapi.PlayerEntry{}, Bans: []agentapi.PlayerBan{}, Errors: []string{}}, nil
}

// --- Server List Ping --------------------------------------------------------------------

type pingAnswer struct {
	json string
	raw  []byte
	hang bool
	drop bool
}

type pingServer struct {
	ln      net.Listener
	mu      sync.Mutex
	answers map[string]pingAnswer
	nhits   map[string]int
}

func newPingServer(t *testing.T) *pingServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &pingServer{ln: ln, answers: map[string]pingAnswer{}, nhits: map[string]int{}}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); p.serve(c, done) }()
		}
	}()
	t.Cleanup(func() { close(done); _ = ln.Close(); wg.Wait() })
	return p
}

func (p *pingServer) addr() string { return p.ln.Addr().String() }

func (p *pingServer) set(fqdn string, a pingAnswer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[fqdn] = a
}

func (p *pingServer) hits(fqdn string) int { p.mu.Lock(); defer p.mu.Unlock(); return p.nhits[fqdn] }

// serve reads handshake (id 0, protocol, address, port, next state 1) and status request
// (id 0), then answers as configured for the handshake address.
func (p *pingServer) serve(c net.Conn, done <-chan struct{}) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	hs, err := readTestPacket(r)
	if err != nil {
		return
	}
	hr := bytes.NewReader(hs)
	if id, _ := binary.ReadUvarint(hr); id != 0 {
		return
	}
	_, _ = binary.ReadUvarint(hr) // protocol version
	n, err := binary.ReadUvarint(hr)
	if err != nil || n > 255 {
		return
	}
	host := make([]byte, n)
	if _, err := io.ReadFull(hr, host); err != nil {
		return
	}
	fqdn := string(host)
	p.mu.Lock()
	a, ok := p.answers[fqdn]
	p.nhits[fqdn]++
	p.mu.Unlock()
	if !ok || a.drop {
		return
	}
	if _, err := readTestPacket(r); err != nil { // status request
		return
	}
	switch {
	case a.hang:
		<-done
	case a.raw != nil:
		_, _ = c.Write(a.raw)
	default:
		var body bytes.Buffer
		body.Write(binary.AppendUvarint(nil, 0))
		body.Write(binary.AppendUvarint(nil, uint64(len(a.json))))
		body.WriteString(a.json)
		_, _ = c.Write(append(binary.AppendUvarint(nil, uint64(body.Len())), body.Bytes()...))
	}
}

func readTestPacket(r *bufio.Reader) ([]byte, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil || n > 1<<16 {
		return nil, fmt.Errorf("bad packet length: %w", err)
	}
	b := make([]byte, n)
	_, err = io.ReadFull(r, b)
	return b, err
}

// statusJSON is a minimal valid status answer.
func statusJSON(online, max int, names ...string) string {
	sample := make([]map[string]string, 0, len(names))
	for i, n := range names {
		sample = append(sample, map[string]string{"name": n, "id": fmt.Sprintf("00000000-0000-0000-0000-%012d", i)})
	}
	b, _ := json.Marshal(map[string]any{
		"version":     map[string]any{"name": "1.21.11", "protocol": 774},
		"players":     map[string]any{"online": online, "max": max, "sample": sample},
		"description": map[string]any{"text": "A Minecraft Server"},
	})
	return string(b)
}

// --- HTTP env ----------------------------------------------------------------------------

type env struct {
	t       *testing.T
	db      *sql.DB
	clk     *clock.Fake
	rec     *audit.MemoryRecorder
	agent   *fakeAgent
	console *fakeConsole
	notes   *notify.Recording
	h       *Handler
	mux     *chi.Mux
	rt      *rbac.Router
	rootID  int64
}

// newEnv builds the handler on a real DB and rbac router without starting Run.
func newEnv(t *testing.T, opts ...func(*Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, agent: newFakeAgent(),
		console: newFakeConsole(), notes: &notify.Recording{},
	}
	d := Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Notifier: e.notes, Instances: e.agent, Console: e.console,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, o := range opts {
		o(&d)
	}
	e.h = NewHandler(d)
	e.mux = chi.NewRouter()
	e.rt = rbac.NewRouter(e.mux, rbac.NewDBChecker(db), e.clk)
	e.h.Routes(e.rt)
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

func (e *env) admin() principal.Principal {
	p := e.adminNoSudo()
	p.SudoUntil = t0.Add(5 * time.Minute)
	return p
}

func (e *env) adminNoSudo() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull}
}

// user creates a user with the given per-server permissions.
func (e *env) user(name string, perms map[int64][]string) principal.Principal {
	e.t.Helper()
	id := storetest.CreateUser(e.t, e.db, name, "user")
	for sid, ps := range perms {
		for _, p := range ps {
			if _, err := e.db.Exec("INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)", id, sid, p); err != nil {
				e.t.Fatal(err)
			}
		}
	}
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// server creates a server with an instance (paper 1.21.11, desired_state running).
func (e *env) server(name string) int64 {
	e.t.Helper()
	id := storetest.CreateServer(e.t, e.db, name)
	if _, err := store.New(e.db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: "paper", Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
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

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakes compile-check (also keeps unused-helper lint quiet until B1/B2 use them).
// Every helper B1/B2 rely on is listed, so the unused linter stays quiet in Phase A.
var _ = []any{newPingServer, statusJSON, eventually,
	(*fakeAgent).set, (*fakeAgent).setDocker, (*fakeAgent).setErr, (*fakeAgent).calls,
	(*fakeConsole).reply, (*fakeConsole).setDelay, (*fakeConsole).commands, (*fakeConsole).peak,
	(*pingServer).addr, (*pingServer).set, (*pingServer).hits}
```

`internal/monitoring/routes_test.go`:

```go
package monitoring

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/rbac"
)

// wantRoutes are exactly the monitoring operations of api/openapi.yaml (V3); the test router
// has no /api prefix.
var wantRoutes = map[string]rbac.Rule{
	"GET /servers/{id}/metrics":      rbac.Server(rbac.PermServerView, "id"),
	"GET /servers/{id}/metrics/live": rbac.Server(rbac.PermServerView, "id"),
	"PUT /servers/{id}/alerts":       rbac.Admin(),
	"GET /monitoring/overview":       rbac.Authenticated(),
	"GET /monitoring/host":           rbac.Admin(),
	"GET /monitoring/settings":       rbac.Admin(),
	"PUT /monitoring/settings":       rbac.Admin().WithSudo(),
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	got := e.rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	if un := rbac.Uncovered(e.mux, got, func(string, string) bool { return false }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Anonymous requests are 401; a user without server.view is 403 on server routes and on the
// admin routes; overview is open to every signed-in user.
func TestRoutesCheckPermissions(t *testing.T) {
	e := newEnv(t)
	id := e.server("survival")
	bob := e.user("bob", nil)
	path := func(p string) string { return strings.ReplaceAll(p, "{id}", "1") }
	_ = id
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		p := path(pattern)
		if strings.Contains(p, "metrics") || strings.Contains(p, "/monitoring/host") {
			p += "?range=1h"
			if strings.HasSuffix(p, "live?range=1h") {
				p = strings.TrimSuffix(p, "?range=1h")
			}
		}
		if rec := e.do(method, p, map[string]any{}, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
		want := http.StatusForbidden
		if key == "GET /monitoring/overview" {
			want = http.StatusOK
		}
		if rec := e.do(method, p, map[string]any{}, &bob); rec.Code != want {
			t.Errorf("%s as user without rights: %d, want %d", key, rec.Code, want)
		}
	}
	noSudo := e.adminNoSudo()
	body := map[string]any{"disk_min_percent": 10, "disk_min_bytes": 10 << 30, "down_after_min": 3, "crash_restarts": 3, "crash_window_min": 10}
	if rec := e.do(http.MethodPut, "/monitoring/settings", body, &noSudo); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("PUT settings without sudo: %d %s", rec.Code, rec.Body)
	}
}
```

`internal/monitoring/settings_test.go`:

```go
package monitoring

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store"
)

func validSettings() map[string]any {
	return map[string]any{"disk_min_percent": 15, "disk_min_bytes": 20 << 30, "down_after_min": 5, "crash_restarts": 4, "crash_window_min": 15}
}

func TestSettingsDefaultsAndUpdate(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	got := decode[settingsJSON](t, e.do(http.MethodGet, "/monitoring/settings", nil, &admin))
	if got.DiskMinPercent != 10 || got.DiskMinBytes != 10<<30 || got.DownAfterMin != 3 || got.CrashRestarts != 3 ||
		got.CrashWindowMin != 10 || got.Alerts == nil || len(got.Alerts) != 0 {
		t.Fatalf("defaults = %+v", got)
	}
	rec := e.do(http.MethodPut, "/monitoring/settings", validSettings(), &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	got = decode[settingsJSON](t, rec)
	if got.DiskMinPercent != 15 || got.DiskMinBytes != 20<<30 || got.DownAfterMin != 5 || got.CrashRestarts != 4 || got.CrashWindowMin != 15 {
		t.Fatalf("after put = %+v", got)
	}
	row, err := store.New(e.db).MonitoringGetSettings(t.Context())
	if err != nil || row.DiskMinPercent != 15 || row.UpdatedAt != t0.UnixMilli() {
		t.Fatalf("row = %+v %v", row, err)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionMonitoringSettingsUpdate || last.Target != auditTarget || last.Result != audit.ResultSuccess {
		t.Fatalf("audit = %+v", last)
	}
	if _, ok := last.Details["from"]; !ok {
		t.Fatalf("audit details = %v", last.Details)
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	for field, bad := range map[string][]int64{
		"disk_min_percent": {0, 51},
		"disk_min_bytes":   {1<<30 - 1, 10<<40 + 1},
		"down_after_min":   {0, 61},
		"crash_restarts":   {1, 21},
		"crash_window_min": {0, 121},
	} {
		for _, v := range bad {
			body := validSettings()
			body[field] = v
			rec := e.do(http.MethodPut, "/monitoring/settings", body, &admin)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
				t.Errorf("%s=%d: %d %s", field, v, rec.Code, rec.Body)
			}
		}
	}
	body := validSettings()
	body["extra"] = 1
	if rec := e.do(http.MethodPut, "/monitoring/settings", body, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("rejected updates were audited: %d", n)
	}
}

func TestServerAlertsSwitch(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := e.server("survival")
	path := fmt.Sprintf("/servers/%d/alerts", id)
	if rec := e.do(http.MethodPut, path, map[string]any{"down_alerts": false}, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if v, err := store.New(e.db).MonitoringGetServerAlerts(t.Context(), id); err != nil || v != 0 {
		t.Fatalf("row = %d %v", v, err)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerAlertsUpdate || last.Target != serverTarget(id) || last.Details["down_alerts"] != false {
		t.Fatalf("audit = %+v", last)
	}
	if rec := e.do(http.MethodPut, path, map[string]any{}, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing down_alerts: %d", rec.Code)
	}
	if rec := e.do(http.MethodPut, "/servers/999/alerts", map[string]any{"down_alerts": true}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server: %d", rec.Code)
	}
}

func TestFiringJSON(t *testing.T) {
	since := t0.Add(-time.Minute)
	got := firingJSON([]FiringAlert{
		{Kind: alertServerDown, ServerID: 7, ServerName: "survival", Since: since, Detail: "not reachable"},
		{Kind: alertDiskLow, Since: since, Detail: "3 GiB free"},
	})
	if len(got) != 2 || *got[0].ServerID != 7 || *got[0].ServerName != "survival" || got[0].Since != since.UnixMilli() ||
		got[1].ServerID != nil || got[1].ServerName != nil {
		t.Fatalf("firingJSON = %+v", got)
	}
	if firingJSON(nil) == nil {
		t.Fatal("firingJSON(nil) must be an empty slice")
	}
	if alertKey(alertServerDown, 7) != "server.down:7" || alertKey(alertDiskLow, 0) != "host.disk_low" {
		t.Fatal("alertKey format differs from the keys servers deletes (V2)")
	}
}
```

`internal/monitoring/api_test.go` (Stub-Verhalten; B2 ersetzt diese Datei):

```go
package monitoring

import (
	"fmt"
	"net/http"
	"testing"
)

// Phase A stubs answer valid empty structures (B2 replaces this file).
func TestStubReadRoutes(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := e.server("survival")
	for _, r := range []string{"1h", "24h", "7d", "30d"} {
		got := decode[seriesJSON](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=%s", id, r), nil, &admin))
		if got.Range != r || got.StepMS != stepFor(r).Milliseconds() || got.Series == nil {
			t.Errorf("metrics %s = %+v", r, got)
		}
	}
	if rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=2h", id), nil, &admin); rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("bad range: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodGet, "/servers/999/metrics?range=1h", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server: %d", rec.Code)
	}
	live := decode[liveJSON](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics/live", id), nil, &admin))
	if live.At != nil || live.Up != nil || live.PlayerNames == nil || live.TPSSource != TPSSourceUnknown {
		t.Fatalf("live = %+v", live)
	}
	ov := decode[overviewJSON](t, e.do(http.MethodGet, "/monitoring/overview", nil, &admin))
	if ov.Servers == nil || ov.Host == nil || ov.Host.Alerts == nil {
		t.Fatalf("overview (admin) = %+v", ov)
	}
	bob := e.user("bob", map[int64][]string{id: {"server.view"}})
	if ov := decode[overviewJSON](t, e.do(http.MethodGet, "/monitoring/overview", nil, &bob)); ov.Host != nil {
		t.Fatalf("overview (user) has host values: %+v", ov.Host)
	}
	if got := decode[seriesJSON](t, e.do(http.MethodGet, "/monitoring/host?range=24h", nil, &admin)); got.StepMS != 300_000 {
		t.Fatalf("host = %+v", got)
	}
}
```

- [ ] **Step 3: `make check` (rot)**

Run: `git add -N internal/monitoring && make check`
Expected: FAIL (Paket `internal/monitoring` fehlt).

- [ ] **Step 4: `internal/monitoring/types.go`**

```go
// Package monitoring implements sub-project 5: it samples the servers (agent status, Server
// List Ping through mc-router, TPS/MSPT over RCON) and the host (/proc, statfs) every 30 s,
// keeps 30 days of history in SQLite in three resolutions, evaluates the down, crash-loop and
// disk alerts and serves the values to the dashboard and the monitoring tab.
package monitoring

import (
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// Snapshot is the result of one collector pass (V4).
type Snapshot struct {
	At      time.Time
	Agent   bool // the agent answered this pass
	Docker  bool // the agent reported Docker ok
	Servers []ServerSample
	Host    HostSample
}

// ServerSample is one server in a pass; measurements are nil when unknown (V5).
type ServerSample struct {
	ID             int64
	Name           string
	DesiredRunning bool
	Gated          bool // DataGate held during the pass
	State          agentapi.ContainerState
	StartedAt      int64 // Unix ms, 0 = not running
	RestartCount   int
	Revision       int64
	Up             *bool // true = valid ping answer, false = running but no answer, nil = no ping
	Players        *int
	PlayersMax     *int
	PlayerNames    []string
	TPS            *float64
	MSPT           *float64
	TPSSource      string // TPSSourceTickQuery | TPSSourcePaper | TPSSourceNone | TPSSourceUnknown
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

// FiringAlert is an alert that fires right now (alerter.Firing); ServerID 0 = host.
type FiringAlert struct {
	Kind       string
	ServerID   int64
	ServerName string
	Since      time.Time
	Detail     string
}

// HostSeries is the series of the host values; server series are their IDs.
const HostSeries int64 = 0

// Metric names (spec §4); server and host share cpu_percent as a name in different series.
const (
	MetricUp          = "up"
	MetricPlayers     = "players"
	MetricPlayersMax  = "players_max"
	MetricTPS         = "tps"
	MetricMSPT        = "mspt"
	MetricCPU         = "cpu_percent"
	MetricMemory      = "memory_bytes"
	MetricMemoryLimit = "memory_limit_bytes"

	MetricHostCPU   = "cpu_percent"
	MetricMemUsed   = "memory_used"
	MetricMemTotal  = "memory_total"
	MetricLoad1     = "load1"
	MetricDiskFree  = "disk_free"
	MetricDiskTotal = "disk_total"
)

// TPS sources (V3 TpsSource). Paper/purpur fall back to `mspt` only (tps = min(20, 1000/mspt)).
const (
	TPSSourceTickQuery = "tick_query"
	TPSSourcePaper     = "paper"
	TPSSourceNone      = "none"
	TPSSourceUnknown   = "unknown"
)

// Alert kinds (V3 AlertKind) and alert_state keys.
const (
	alertServerDown = "server.down"
	alertCrashLoop  = "server.crash_loop"
	alertDiskLow    = "host.disk_low"
)

// Timings and retention (Global Constraints). Waits run on wall time; clock.Clock drives
// timestamps and alert logic.
const (
	collectEvery     = 30 * time.Second
	tpsEvery         = time.Minute
	liveTPSEvery     = 5 * time.Second
	liveWindow       = 30 * time.Second
	rollupEvery      = 5 * time.Minute
	pingTimeout      = 3 * time.Second
	maxParallelPings = 4
	retainRaw        = 24 * time.Hour
	retain5m         = 7 * 24 * time.Hour
	retain1h         = 30 * 24 * time.Hour
	startGrace       = 5 * time.Minute
	remindEvery      = 24 * time.Hour
)

// alertKey is the alert_state key: "<kind>:<server id>" for server alerts, "<kind>" for the
// host. internal/servers deletes "server.down:<id>" and "server.crash_loop:<id>" literally.
func alertKey(kind string, serverID int64) string {
	if serverID == 0 {
		return kind
	}
	return kind + ":" + strconv.FormatInt(serverID, 10)
}

// Phase A: the constants above are used by B1 (collector, rollup) and B2 (alerts); this keeps
// the unused linter quiet until then. B1/B2 leave it in place (harmless).
var _ = []any{alertServerDown, alertCrashLoop, alertDiskLow, collectEvery, tpsEvery, liveTPSEvery,
	liveWindow, rollupEvery, pingTimeout, maxParallelPings, retainRaw, retain5m, retain1h,
	startGrace, remindEvery, alertKey}
```

- [ ] **Step 5: `internal/monitoring/handler.go`**

```go
package monitoring

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
)

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
	Log          *slog.Logger             // nil = slog.Default()
}

// auditTarget is the audit target of the monitoring settings.
const auditTarget = "monitoring"

type Handler struct {
	d      Deps
	q      *store.Queries
	log    *slog.Logger
	coll   *collector
	alerts *alerter
}

// NewHandler normalises Deps (Notifier and Log are never nil afterwards) and builds the
// collector and the alerter with them.
func NewHandler(d Deps) *Handler {
	if d.Notifier == nil {
		d.Notifier = notify.Nop{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Handler{d: d, q: store.New(d.DB), log: d.Log, coll: newCollector(d), alerts: newAlerter(d)}
}

// Routes registers the monitoring routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) { h.routes(api) }

// Run blocks until ctx is done: the collector (each pass goes to the alerter) and the rollup
// loop.
func (h *Handler) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.coll.Run(ctx, h.alerts.Evaluate) }()
	go func() { defer wg.Done(); runRollup(ctx, h.d) }()
	wg.Wait()
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry (actor and IP from the context); a failing write is logged.
func (h *Handler) record(ctx context.Context, action, target string, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.d.Audit.Record(ctx, audit.Entry{Action: action, Target: target, Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}
```

- [ ] **Step 6: Stubs für B1/B2**

`internal/monitoring/collector.go` (Stub; B1 ersetzt die Datei vollständig):

```go
package monitoring

import (
	"context"
	"sync"
)

// collector samples servers and host (Phase A stub: no passes; B1 implements it).
type collector struct {
	d    Deps
	mu   sync.Mutex
	last Snapshot
}

func newCollector(d Deps) *collector { return &collector{d: d} }

var _ = (*collector).deps // Phase A stub: d is used once B1 implements the collector

func (c *collector) deps() Deps { return c.d }

// Run blocks until ctx is done: the first pass runs right at the start, then one every
// collectEvery; every pass goes to onPass (V4).
func (c *collector) Run(ctx context.Context, _ func(context.Context, Snapshot)) { <-ctx.Done() }

// Latest is a copy of the last pass (zero value before the first).
func (c *collector) Latest() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Watch switches the server to live mode for liveWindow.
func (c *collector) Watch(int64) {}

// Live is the freshest sample of the server, including 5-s TPS in live mode.
func (c *collector) Live(int64) (ServerSample, bool) { return ServerSample{}, false }
```

`internal/monitoring/rollup.go` (Stub; B1 ersetzt):

```go
package monitoring

import "context"

// runRollup aggregates and prunes the history every rollupEvery (Phase A stub; B1).
func runRollup(ctx context.Context, _ Deps) { <-ctx.Done() }
```

`internal/monitoring/alerts.go` (Stub; B2 ersetzt):

```go
package monitoring

import "context"

// alerter evaluates the alert rules after every pass (Phase A stub; B2 implements it).
type alerter struct{ d Deps }

func newAlerter(d Deps) *alerter { return &alerter{d: d} }

var _ = (*alerter).deps // Phase A stub: d is used once B2 implements the alerter

func (a *alerter) deps() Deps { return a.d }

// Evaluate checks the rules against a pass and notifies on transitions.
func (a *alerter) Evaluate(context.Context, Snapshot) {}

// Firing lists the alerts firing right now.
func (a *alerter) Firing(context.Context) []FiringAlert { return nil }
```

`internal/monitoring/api.go` (Routen fest; Lese-Handler Stubs, B2 ersetzt sie und darf die Datei umbauen, solange `routes` alle sieben Routen mit diesen Regeln registriert und die drei Handler aus `settings.go` nutzt):

```go
package monitoring

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

func (h *Handler) routes(api *rbac.Router) {
	view := rbac.Server(rbac.PermServerView, "id")
	api.Get("/servers/{id}/metrics", view, h.handleMetrics)
	api.Get("/servers/{id}/metrics/live", view, h.handleLive)
	api.Put("/servers/{id}/alerts", rbac.Admin(), h.handlePutServerAlerts)
	api.Get("/monitoring/overview", rbac.Authenticated(), h.handleOverview)
	api.Get("/monitoring/host", rbac.Admin(), h.handleHost)
	api.Get("/monitoring/settings", rbac.Admin(), h.handleGetSettings)
	api.Put("/monitoring/settings", rbac.Admin().WithSudo(), h.handlePutSettings)
}

// seriesJSON is the OpenAPI schema MetricsSeries.
type seriesJSON struct {
	Range  string                  `json:"range"`
	StepMS int64                   `json:"step_ms"`
	Series map[string][][4]float64 `json:"series"`
}

// liveJSON is the OpenAPI schema LiveMetrics.
type liveJSON struct {
	At          *int64   `json:"at"`
	Up          *bool    `json:"up"`
	Players     *int     `json:"players"`
	PlayersMax  *int     `json:"players_max"`
	PlayerNames []string `json:"player_names"`
	TPS         *float64 `json:"tps"`
	MSPT        *float64 `json:"mspt"`
	CPUPercent  *float64 `json:"cpu_percent"`
	MemoryBytes *int64   `json:"memory_bytes"`
	MemoryLimit *int64   `json:"memory_limit_bytes"`
	TPSSource   string   `json:"tps_source"`
}

// overviewServerJSON is OverviewServer, hostJSON HostSnapshot, overviewJSON MonitoringOverview.
type overviewServerJSON struct {
	ID          int64        `json:"id"`
	Up          *bool        `json:"up"`
	Players     *int         `json:"players"`
	PlayersMax  *int         `json:"players_max"`
	TPS         *float64     `json:"tps"`
	CPUPercent  *float64     `json:"cpu_percent"`
	MemoryBytes *int64       `json:"memory_bytes"`
	MemoryLimit *int64       `json:"memory_limit_bytes"`
	Sparkline   [][2]float64 `json:"sparkline"`
	Alerts      []string     `json:"alerts"`
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

// stepFor is the resolution of a range (V3); 0 for an unknown range.
func stepFor(r string) time.Duration {
	switch r {
	case "1h":
		return collectEvery
	case "24h":
		return 5 * time.Minute
	case "7d", "30d":
		return time.Hour
	}
	return 0
}

// parseRange reads ?range= and answers 400 for unknown values.
func parseRange(w http.ResponseWriter, r *http.Request) (string, bool) {
	rg := r.URL.Query().Get("range")
	if stepFor(rg) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "range must be 1h, 24h, 7d or 30d")
		return "", false
	}
	return rg, true
}

// serverExists answers 404 for an unknown {id}.
func (h *Handler) serverExists(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id > 0 {
		_, err = h.q.RbacGetServer(r.Context(), id)
	}
	switch {
	case err == nil:
		return id, true
	case errors.Is(err, sql.ErrNoRows) || id <= 0:
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
	default:
		httpx.Internal(w, r, err)
	}
	return 0, false
}

// --- Phase A stubs (B2 replaces them) ----------------------------------------------------

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.serverExists(w, r); !ok {
		return
	}
	rg, ok := parseRange(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, seriesJSON{Range: rg, StepMS: stepFor(rg).Milliseconds(), Series: map[string][][4]float64{}})
}

func (h *Handler) handleLive(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.serverExists(w, r); !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, liveJSON{PlayerNames: []string{}, TPSSource: TPSSourceUnknown})
}

func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	out := overviewJSON{Servers: []overviewServerJSON{}}
	if p, _ := principal.From(r.Context()); p.IsAdmin() {
		out.Host = &hostJSON{Alerts: []string{}}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleHost(w http.ResponseWriter, r *http.Request) {
	rg, ok := parseRange(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, seriesJSON{Range: rg, StepMS: stepFor(rg).Milliseconds(), Series: map[string][][4]float64{}})
}
```

- [ ] **Step 7: `internal/monitoring/settings.go`**

```go
package monitoring

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Limits of the alert settings (Global Constraints).
const (
	minDiskPercent, maxDiskPercent = 1, 50
	minDiskBytes, maxDiskBytes     = 1 << 30, 10 << 40
	minDownAfter, maxDownAfter     = 1, 60
	minCrashRestarts, maxCrashRest = 2, 20
	minCrashWindow, maxCrashWindow = 1, 120
)

// settingsJSON is MonitoringSettings; updateSettingsRequest UpdateMonitoringSettingsRequest.
type settingsJSON struct {
	DiskMinPercent int64             `json:"disk_min_percent"`
	DiskMinBytes   int64             `json:"disk_min_bytes"`
	DownAfterMin   int64             `json:"down_after_min"`
	CrashRestarts  int64             `json:"crash_restarts"`
	CrashWindowMin int64             `json:"crash_window_min"`
	Alerts         []firingAlertJSON `json:"alerts"`
}

type updateSettingsRequest struct {
	DiskMinPercent int64 `json:"disk_min_percent"`
	DiskMinBytes   int64 `json:"disk_min_bytes"`
	DownAfterMin   int64 `json:"down_after_min"`
	CrashRestarts  int64 `json:"crash_restarts"`
	CrashWindowMin int64 `json:"crash_window_min"`
}

// firingAlertJSON is FiringAlert of the API.
type firingAlertJSON struct {
	Kind       string  `json:"kind"`
	ServerID   *int64  `json:"server_id"`
	ServerName *string `json:"server_name"`
	Since      int64   `json:"since"`
	Detail     string  `json:"detail"`
}

func firingJSON(fs []FiringAlert) []firingAlertJSON {
	out := make([]firingAlertJSON, 0, len(fs))
	for _, f := range fs {
		j := firingAlertJSON{Kind: f.Kind, Since: f.Since.UnixMilli(), Detail: f.Detail}
		if f.ServerID != 0 {
			id, name := f.ServerID, f.ServerName
			j.ServerID, j.ServerName = &id, &name
		}
		out = append(out, j)
	}
	return out
}

func (req updateSettingsRequest) validate() error {
	for _, c := range []struct {
		name     string
		v, lo, hi int64
	}{
		{"disk_min_percent", req.DiskMinPercent, minDiskPercent, maxDiskPercent},
		{"disk_min_bytes", req.DiskMinBytes, minDiskBytes, maxDiskBytes},
		{"down_after_min", req.DownAfterMin, minDownAfter, maxDownAfter},
		{"crash_restarts", req.CrashRestarts, minCrashRestarts, maxCrashRest},
		{"crash_window_min", req.CrashWindowMin, minCrashWindow, maxCrashWindow},
	} {
		if c.v < c.lo || c.v > c.hi {
			return fmt.Errorf("%s must be between %d and %d", c.name, c.lo, c.hi)
		}
	}
	return nil
}

func settingsView(s store.MonitoringSetting, firing []FiringAlert) settingsJSON {
	return settingsJSON{
		DiskMinPercent: s.DiskMinPercent, DiskMinBytes: s.DiskMinBytes, DownAfterMin: s.DownAfterMin,
		CrashRestarts: s.CrashRestarts, CrashWindowMin: s.CrashWindowMin, Alerts: firingJSON(firing),
	}
}

func (h *Handler) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s, err := h.q.MonitoringGetSettings(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsView(s, h.alerts.Firing(r.Context())))
}

func (h *Handler) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateSettingsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, err.Error())
		return
	}
	old, err := h.q.MonitoringGetSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := h.q.MonitoringUpdateSettings(ctx, store.MonitoringUpdateSettingsParams{
		DiskMinPercent: req.DiskMinPercent, DiskMinBytes: req.DiskMinBytes, DownAfterMin: req.DownAfterMin,
		CrashRestarts: req.CrashRestarts, CrashWindowMin: req.CrashWindowMin, UpdatedAt: h.d.Clock.Now().UnixMilli(),
	}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	s, err := h.q.MonitoringGetSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	from, to := settingsView(old, nil), settingsView(s, nil)
	from.Alerts, to.Alerts = nil, nil
	h.record(ctx, audit.ActionMonitoringSettingsUpdate, auditTarget, audit.ResultSuccess, map[string]any{"from": from, "to": to})
	httpx.WriteJSON(w, http.StatusOK, settingsView(s, h.alerts.Firing(ctx)))
}

func (h *Handler) handlePutServerAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err == nil && id > 0 {
		_, err = h.q.RbacGetServer(ctx, id)
	}
	if errors.Is(err, sql.ErrNoRows) || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var req struct {
		DownAlerts *bool `json:"down_alerts"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.DownAlerts == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, "down_alerts is required")
		return
	}
	v := int64(0)
	if *req.DownAlerts {
		v = 1
	}
	if err := h.q.MonitoringSetServerAlerts(ctx, store.MonitoringSetServerAlertsParams{ServerID: id, DownAlerts: v}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerAlertsUpdate, serverTarget(id), audit.ResultSuccess, map[string]any{"down_alerts": *req.DownAlerts})
	w.WriteHeader(http.StatusNoContent)
}
```

(`settingsJSON` hat für das Audit `Alerts: nil` → JSON `null`; der Test prüft nur, dass `from` existiert. Falls `revive`/`gofmt` die Konstantenreihe anders ausrichten will: `gofmt -w`.)

- [ ] **Step 8: Verdrahtung in `cmd/panel`**

`cmd/panel/app.go`: Import `github.com/Bernotese/mc-panel/internal/monitoring`; nach `serversH = servers.NewHandler(…)`:

```go
	// Monitoring reads the agent status, pings through mc-router and asks RCON for TPS; the
	// data gates keep down alerts quiet while an extract or a restore holds the server.
	monitoringH := monitoring.NewHandler(monitoring.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Notifier: disp, Instances: d.Instances, Console: d.Console,
		Gate: servers.AnyGate(backupsH, filesH), PingAddr: cfg.MonitoringPingAddr, InstancesDir: cfg.InstancesDir,
	})
```

In `rt.Route("/api", …)` nach `backupsH.Routes(api)`: `monitoringH.Routes(api)`. In `run`: `wg.Add(7)` und `go func() { defer wg.Done(); monitoringH.Run(ctx) }()`.

Hinweis zu nil-Interfaces: Ist `d.Instances` bzw. `d.Console` in Tests ein typisiertes nil (z. B. `(*agent.Client)(nil)`), prüfen die B1-Aufrufer mit `== nil` vergeblich. `appDeps` übergibt Interfaces; die bestehenden Tests setzen echte Fakes oder lassen das Feld weg – nichts ändern, aber so belassen.

`cmd/panel/integration_test.go`, `TestRouteCoverage`: `95` → `102` (Kommentartext ebenso).

- [ ] **Step 9: TS-Aliase**

`web/src/api/types.ts`, nach den Backup-Aliasen:

```ts
export type MetricsRange = Schemas['MetricsRange']
export type MetricPoint = Schemas['MetricPoint']
export type MetricsSeries = Schemas['MetricsSeries']
export type TpsSource = Schemas['TpsSource']
export type LiveMetrics = Schemas['LiveMetrics']
export type AlertKind = Schemas['AlertKind']
export type SparklinePoint = Schemas['SparklinePoint']
export type OverviewServer = Schemas['OverviewServer']
export type HostSnapshot = Schemas['HostSnapshot']
export type MonitoringOverview = Schemas['MonitoringOverview']
export type FiringAlert = Schemas['FiringAlert']
export type MonitoringSettings = Schemas['MonitoringSettings']
export type UpdateMonitoringSettingsRequest = Schemas['UpdateMonitoringSettingsRequest']
export type UpdateServerAlertsRequest = Schemas['UpdateServerAlertsRequest']
```

- [ ] **Step 10: Mock-Tests schreiben (rot)**

`web/src/mocks/handlers.test.ts`: `toHaveLength(95)` → `toHaveLength(102)`; Import `import type { LiveMetrics, MetricsSeries, MonitoringOverview, MonitoringSettings } from '@/api/types'`; im `describe('mock behaviour', …)` anhängen:

```ts
  it('serves deterministic monitoring series and live values', async () => {
    loginAs('admin')
    const s = (await callApi('GET', '/servers/1/metrics?range=24h')).body as MetricsSeries
    expect(s.range).toBe('24h')
    expect(s.step_ms).toBe(300_000)
    expect(s.series.players).toHaveLength(288)
    expect(s.series.tps.every(([, avg]) => avg > 0 && avg <= 20)).toBe(true)
    const again = (await callApi('GET', '/servers/1/metrics?range=24h')).body as MetricsSeries
    expect(again.series.players.map((p) => p[1])).toEqual(s.series.players.map((p) => p[1]))
    expect(((await callApi('GET', '/servers/1/metrics?range=30d')).body as MetricsSeries).series.players).toHaveLength(720)
    expect(field((await callApi('GET', '/servers/1/metrics?range=2h')).body, 'error')).toBe('validation_failed')
    expect(Object.values(((await callApi('GET', '/servers/3/metrics?range=1h')).body as MetricsSeries).series).every((v) => v.length === 0)).toBe(true)
    const live = (await callApi('GET', '/servers/1/metrics/live')).body as LiveMetrics
    expect(live).toMatchObject({ up: true, tps_source: 'tick_query' })
    expect(live.player_names.length).toBeGreaterThan(0)
    expect(((await callApi('GET', '/servers/4/metrics/live')).body as LiveMetrics)).toMatchObject({ up: false, tps_source: 'none' })
  })

  it('filters the overview and hides host values from users', async () => {
    loginAs('admin')
    const ov = (await callApi('GET', '/monitoring/overview')).body as MonitoringOverview
    expect(ov.servers.map((s) => s.id).sort()).toEqual([1, 2, 3, 4])
    expect(ov.servers.find((s) => s.id === 4)?.alerts).toEqual(['server.down'])
    expect(ov.host?.alerts).toEqual([])
    db.monitoring.diskLow = true
    expect(((await callApi('GET', '/monitoring/overview')).body as MonitoringOverview).host?.alerts).toEqual(['host.disk_low'])
    db.monitoring.diskLow = false
    loginAs('alice')
    const user = (await callApi('GET', '/monitoring/overview')).body as MonitoringOverview
    expect(user.host).toBeNull()
    expect(user.servers.map((s) => s.id).sort()).toEqual([1, 2])
    expect((await callApi('GET', '/monitoring/host?range=1h')).status).toBe(403)
  })

  it('saves monitoring settings with sudo and validates them', async () => {
    const plain = loginAs('admin')
    const body = { disk_min_percent: 20, disk_min_bytes: 5 * 1024 ** 3, down_after_min: 2, crash_restarts: 4, crash_window_min: 15 }
    expect(field((await callApi('PUT', '/monitoring/settings', { body, csrf: plain.csrf_token })).body, 'error')).toBe('sudo_required')
    const me = loginAs('admin', { sudo: true })
    const saved = (await callApi('PUT', '/monitoring/settings', { body, csrf: me.csrf_token })).body as MonitoringSettings
    expect(saved).toMatchObject(body)
    expect(field((await callApi('PUT', '/monitoring/settings', { body: { ...body, crash_restarts: 1 }, csrf: me.csrf_token })).body, 'error')).toBe('validation_failed')
    const settings = (await callApi('GET', '/monitoring/settings')).body as MonitoringSettings
    expect(settings.alerts.map((a) => a.kind)).toEqual(['server.down'])
    expect((await callApi('PUT', '/servers/4/alerts', { body: { down_alerts: false }, csrf: me.csrf_token })).status).toBe(204)
    expect(field((await callApi('GET', '/servers/4')).body, 'down_alerts')).toBe(false)
    expect(((await callApi('GET', '/monitoring/overview')).body as MonitoringOverview).servers.find((s) => s.id === 4)?.alerts).toEqual([])
    db.monitoring = defaultMonitoring()
  })
```

(Import `defaultMonitoring` aus `./db` ergänzen.)

- [ ] **Step 11: Mock implementieren**

`web/src/mocks/handlers/monitoring.ts`:

```ts
import { http, HttpResponse } from 'msw'
import type { AlertKind, FiringAlert, HostSnapshot, LiveMetrics, MetricPoint, MetricsRange, MetricsSeries, MonitoringOverview, MonitoringSettings, OverviewServer, UpdateMonitoringSettingsRequest } from '@/api/types'
import { db, memoryLimitBytes, recordAudit, type MockServer } from '../db'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type MockOperation } from './util'

const GIB = 1024 ** 3
const STEP: Record<MetricsRange, number> = { '1h': 30_000, '24h': 300_000, '7d': 3_600_000, '30d': 3_600_000 }
const SPAN: Record<MetricsRange, number> = { '1h': 3_600_000, '24h': 86_400_000, '7d': 7 * 86_400_000, '30d': 30 * 86_400_000 }
const RANGES = Object.keys(STEP) as MetricsRange[]
const LIMITS: Record<keyof UpdateMonitoringSettingsRequest, [number, number]> = {
  disk_min_percent: [1, 50],
  disk_min_bytes: [GIB, 10 * 1024 * GIB],
  down_after_min: [1, 60],
  crash_restarts: [2, 20],
  crash_window_min: [1, 120],
}
const NAMES = ['Alex', 'Steve', 'Notch_Fan', 'Builder42', 'Creeper_X']

/** Deterministic noise in [0, 1) for a seed and an integer index. */
function noise(seed: number, i: number): number {
  const x = Math.sin(seed * 12.9898 + i * 78.233) * 43758.5453
  return x - Math.floor(x)
}

/** The mock scenario of a server (Abweichung 7): survival up, modded down, others without values. */
type Scenario = 'up' | 'down' | 'off'
function scenario(s: MockServer): Scenario {
  if (!s.instance) return 'off'
  if (s.name === 'modded') return 'down'
  return s.status.state === 'running' ? 'up' : 'off'
}

/** Value of a metric at time t for a running server (players follow the time of day). */
function value(s: MockServer, metric: string, t: number, now: number): number | null {
  const seed = s.id
  const hour = new Date(t).getUTCHours() + new Date(t).getUTCMinutes() / 60
  const i = Math.floor(t / 30_000)
  switch (metric) {
    case 'up':
      return 1
    case 'players':
      return Math.max(0, Math.round(6 + 6 * Math.sin(((hour - 14) / 24) * 2 * Math.PI) + 2 * noise(seed, i) - 1))
    case 'players_max':
      return s.instance?.properties.max_players ?? 20
    case 'tps': {
      const dip = Math.abs(t - (now - 3 * 3_600_000)) < 600_000 ? 6 : 0 // a dip 3 h ago
      return Math.min(20, 19.5 + 0.5 * noise(seed + 1, i)) - dip
    }
    case 'mspt':
      return 18 + 12 * noise(seed + 2, i)
    case 'cpu_percent':
      return 8 + 10 * noise(seed + 3, i)
    case 'memory_bytes':
      return Math.round(memoryLimitBytes(s.instance?.memory_mib ?? 2048) * (0.45 + 0.1 * noise(seed + 4, i)))
    case 'memory_limit_bytes':
      return memoryLimitBytes(s.instance?.memory_mib ?? 2048)
  }
  return null
}

const SERVER_METRICS = ['up', 'players', 'players_max', 'tps', 'mspt', 'cpu_percent', 'memory_bytes', 'memory_limit_bytes']

function points(range: MetricsRange, now: number, f: (t: number) => number | null): MetricPoint[] {
  const step = STEP[range]
  const end = Math.floor(now / step) * step
  const out: MetricPoint[] = []
  for (let t = end - SPAN[range] + step; t <= end; t += step) {
    const v = f(t)
    if (v === null) continue
    const spread = range === '1h' ? 0 : Math.abs(v) * 0.08
    out.push([t, v, v - spread, v + spread])
  }
  return out
}

function serverSeries(s: MockServer, range: MetricsRange, now: number): MetricsSeries['series'] {
  const sc = scenario(s)
  const series: MetricsSeries['series'] = {}
  // like the backend: every metric key is present, empty without values (V6)
  if (sc === 'off') return Object.fromEntries(SERVER_METRICS.map((m) => [m, []]))
  for (const m of SERVER_METRICS) {
    // modded went down 5 minutes ago: up = 0 since then, no other values since then
    series[m] = points(range, now, (t) => {
      if (sc === 'down' && t > now - 5 * 60_000) return m === 'up' ? 0 : null
      if (sc === 'down' && (m === 'tps' || m === 'mspt')) return null // tps_source none
      return value(s, m, t, now)
    })
  }
  return series
}

function hostValues(now: number): Omit<HostSnapshot, 'alerts'> {
  const total = 512 * GIB
  const settings = db.monitoring.settings
  const threshold = Math.max(settings.disk_min_bytes, (total * settings.disk_min_percent) / 100)
  return {
    at: Math.floor(now / 30_000) * 30_000,
    cpu_percent: 23 + 10 * noise(99, Math.floor(now / 30_000)),
    load1: 1.4,
    memory_used: 9 * GIB,
    memory_total: 16 * GIB,
    disk_free: db.monitoring.diskLow ? threshold - 2 * GIB : threshold + 6 * GIB,
    disk_total: total,
  }
}

function serverAlerts(s: MockServer): AlertKind[] {
  return scenario(s) === 'down' && (db.monitoring.downAlerts[s.id] ?? true) ? ['server.down'] : []
}

function firing(now: number): FiringAlert[] {
  const out: FiringAlert[] = db.servers
    .filter((s) => serverAlerts(s).length > 0)
    .map((s) => ({ kind: 'server.down', server_id: s.id, server_name: s.name, since: now - 5 * 60_000, detail: 'not reachable for 5 minutes' }))
  if (db.monitoring.diskLow) out.push({ kind: 'host.disk_low', server_id: null, server_name: null, since: now - 10 * 60_000, detail: 'less free space than the threshold' })
  return out
}

function settingsBody(now: number): MonitoringSettings {
  return { ...db.monitoring.settings, alerts: firing(now) }
}

function rangeParam(request: Request): MetricsRange | null {
  const r = new URL(request.url).searchParams.get('range')
  return RANGES.includes(r as MetricsRange) ? (r as MetricsRange) : null
}

export const monitoringOperations: MockOperation[] = [
  op(
    'getServerMetrics',
    http.get(`${API}/servers/:id/metrics`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const s = db.servers.find((x) => x.id === id)
      if (!s) return apiError(404, 'not_found', 'server not found')
      const range = rangeParam(request)
      if (!range) return apiError(400, 'validation_failed', 'range must be 1h, 24h, 7d or 30d')
      return HttpResponse.json<MetricsSeries>({ range, step_ms: STEP[range], series: serverSeries(s, range, Date.now()) })
    }),
  ),
  op(
    'getServerLiveMetrics',
    http.get(`${API}/servers/:id/metrics/live`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const s = db.servers.find((x) => x.id === id)
      if (!s) return apiError(404, 'not_found', 'server not found')
      const now = Date.now()
      const sc = scenario(s)
      const empty: LiveMetrics = { at: null, up: null, players: null, players_max: null, player_names: [], tps: null, mspt: null, cpu_percent: null, memory_bytes: null, memory_limit_bytes: null, tps_source: 'unknown' }
      if (sc === 'off') return HttpResponse.json<LiveMetrics>(empty)
      if (sc === 'down') return HttpResponse.json<LiveMetrics>({ ...empty, at: now, up: false, tps_source: 'none' })
      // live always shows at least one named player (V6), whatever the time of day
      const players = Math.max(1, value(s, 'players', now, now) ?? 0)
      return HttpResponse.json<LiveMetrics>({
        at: now,
        up: true,
        players,
        players_max: value(s, 'players_max', now, now),
        player_names: NAMES.slice(0, Math.min(players, 12)),
        tps: value(s, 'tps', now, now),
        mspt: value(s, 'mspt', now, now),
        cpu_percent: value(s, 'cpu_percent', now, now),
        memory_bytes: value(s, 'memory_bytes', now, now),
        memory_limit_bytes: value(s, 'memory_limit_bytes', now, now),
        tps_source: 'tick_query',
      })
    }),
  ),
  op(
    'updateServerAlerts',
    http.put(`${API}/servers/:id/alerts`, async ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const s = db.servers.find((x) => x.id === idParam(params))
      if (!s) return apiError(404, 'not_found', 'server not found')
      const body = await readJson<{ down_alerts?: unknown }>(request)
      if (!body || typeof body.down_alerts !== 'boolean') return apiError(400, 'validation_failed', 'down_alerts is required')
      db.monitoring.downAlerts[s.id] = body.down_alerts
      recordAudit(g.user, 'server.alerts_update', `server:${s.id}`, 'success', { down_alerts: body.down_alerts })
      return new HttpResponse(null, { status: 204 })
    }),
  ),
  op(
    'getMonitoringOverview',
    http.get(`${API}/monitoring/overview`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const now = Date.now()
      const servers: OverviewServer[] = db.servers
        .filter((s) => serverPermissions(g.user, s.id).includes('server.view'))
        .map((s) => {
          const sc = scenario(s)
          const at = (m: string) => (sc === 'up' ? value(s, m, now, now) : null)
          return {
            id: s.id,
            up: sc === 'off' ? null : sc === 'up',
            players: at('players'),
            players_max: sc === 'off' ? null : value(s, 'players_max', now, now),
            tps: at('tps'),
            cpu_percent: at('cpu_percent'),
            memory_bytes: at('memory_bytes'),
            memory_limit_bytes: at('memory_limit_bytes'),
            sparkline: sc === 'up' ? points('1h', now, (t) => value(s, 'players', t, now)).map(([t, v]) => [t, v]) : [],
            alerts: serverAlerts(s),
          }
        })
      const host: HostSnapshot | null = g.user.role === 'admin' ? { ...hostValues(now), alerts: db.monitoring.diskLow ? ['host.disk_low'] : [] } : null
      return HttpResponse.json<MonitoringOverview>({ servers, host })
    }),
  ),
  op(
    'getHostMetrics',
    http.get(`${API}/monitoring/host`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const range = rangeParam(request)
      if (!range) return apiError(400, 'validation_failed', 'range must be 1h, 24h, 7d or 30d')
      const now = Date.now()
      const h = hostValues(now)
      const series: MetricsSeries['series'] = {
        cpu_percent: points(range, now, (t) => 20 + 15 * noise(99, Math.floor(t / 30_000))),
        load1: points(range, now, (t) => 1 + noise(98, Math.floor(t / 30_000))),
        memory_used: points(range, now, (t) => 8 * GIB + GIB * noise(97, Math.floor(t / 30_000))),
        memory_total: points(range, now, () => h.memory_total),
        disk_free: points(range, now, (t) => (h.disk_free ?? 0) + Math.round(((now - t) / SPAN['30d']) * 20 * GIB)),
        disk_total: points(range, now, () => h.disk_total),
      }
      return HttpResponse.json<MetricsSeries>({ range, step_ms: STEP[range], series })
    }),
  ),
  op(
    'getMonitoringSettings',
    http.get(`${API}/monitoring/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<MonitoringSettings>(settingsBody(Date.now()))
    }),
  ),
  op(
    'updateMonitoringSettings',
    http.put(`${API}/monitoring/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<Record<string, unknown>>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      const keys = Object.keys(LIMITS) as (keyof UpdateMonitoringSettingsRequest)[]
      if (Object.keys(body).some((k) => !keys.includes(k as keyof UpdateMonitoringSettingsRequest))) return apiError(400, 'bad_request', 'unknown field')
      for (const k of keys) {
        const v = body[k]
        const [lo, hi] = LIMITS[k]
        if (typeof v !== 'number' || !Number.isInteger(v) || v < lo || v > hi) return apiError(400, 'validation_failed', `${k} must be between ${lo} and ${hi}`)
      }
      const from = { ...db.monitoring.settings }
      db.monitoring.settings = body as unknown as UpdateMonitoringSettingsRequest
      recordAudit(g.user, 'monitoring.settings_update', 'monitoring', 'success', { from, to: db.monitoring.settings })
      return HttpResponse.json<MonitoringSettings>(settingsBody(Date.now()))
    }),
  ),
]
```

`web/src/mocks/handlers.ts`: Import `import { monitoringOperations } from './handlers/monitoring'` und nach `...backupOperations,` die Zeile `...monitoringOperations,`.

`db.monitoring.settings` hat den Typ aus Task 2 (`MockMonitoring['settings']`); er entspricht `UpdateMonitoringSettingsRequest` strukturell. Falls `tsc` die Zuweisung im Mock nicht annimmt, `MockMonitoring.settings` als `UpdateMonitoringSettingsRequest` typisieren (Import in `db.ts`).

- [ ] **Step 12: `make check`, Tests auf der VM**

Run: `make check`, dann `make test-vm` (alle Suites)
Expected: PASS – `internal/monitoring` (`TestRouteContract`, `TestRoutesCheckPermissions`, `TestSettings*`, `TestServerAlertsSwitch`, `TestFiringJSON`, `TestStubReadRoutes`), `cmd/panel` (`TestRouteCoverage` mit 102, OpenAPI-Abgleich), Vitest inkl. der drei neuen Mock-Tests, lint (golangci-lint 0, eslint, tsc).

- [ ] **Step 13: Commit, Push, CI, PR fertig**

```bash
git add api/openapi.yaml web/src/api internal/monitoring cmd/panel web/src/mocks
git commit -m "feat(monitoring): API contract, package skeleton with settings routes, wiring and mocks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks --watch --interval 30
```

PR-Text vervollständigen (`gh pr edit --body …`): Zusammenfassung der drei Aufgaben, die Abweichungen 1–8 dieses Plans plus eigene, Testergebnisse (VM + CI), Ende `🤖 Generated with [Claude Code](https://claude.com/claude-code)` + Leerzeile + `https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`. Dem Lead PR-Nummer und CI-Stand melden; PR bleibt Draft.

---

## Self-Review

- **Abdeckung V1–V6:** V1 → Aufgabe 1; V2 → Aufgabe 2 (mit Abweichungen 1–3); V3 → Aufgabe 3 Step 1 (+ `Server.down_alerts` in Aufgabe 2); V4 → `types.go`/`handler.go` wortgleich, Stubs für `collector`/`alerter`/`runRollup`/`routes`; V5 ist Verhalten von B1/B2 (Typen und Kommentare stehen in `types.go`); V6 → Mock (Abweichung 7 zu `creative`/`modded`).
- **Global Constraints:** Regeln pro Route in `routes_test.go` wortgleich; Grenzen der Einstellungen in `settings.go` und Mock identisch; Takte/Aufbewahrung als Konstanten in `types.go`.
- **Lead-Nachträge eingearbeitet:** exportierte Metrik-/Quellen-Konstanten (`HostSeries`, `Metric…`, `TPSSource…`), benannte `from_at`/`to_at`-Argumente, sprechende Fake-Namen im Interfaces-Block; Paper/Purpur-Fallback nur `mspt` (Kommentar in `types.go`, Umsetzung B1).
- **Für B1/B2 fest:** `types.go`, `handler.go`, `settings.go`, `helpers_test.go`, `routes_test.go`, `settings_test.go`. B1 ersetzt `collector.go` und `rollup.go`; B2 ersetzt `alerts.go`, die Lese-Handler in `api.go` und `api_test.go`.
- **Import-Zyklus:** `monitoring` → `servers` (nur `DataGate`); `servers` importiert `monitoring` nicht (Schlüssel der Alert-Zustände literal in `deleteMonitoring`, `TestFiringJSON` prüft das Format).
