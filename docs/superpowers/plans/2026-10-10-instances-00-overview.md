# mc-panel Teilprojekt 2: Instanzen-Lifecycle – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Echte Minecraft-Server als Docker-Container (`itzg/minecraft-server`) anlegen, starten, stoppen, konfigurieren und automatisch neu starten, erreichbar über `<name>.<basisdomain>` via `itzg/mc-router` – ohne dass das panel je Zugriff auf den Docker-Socket bekommt.

**Architecture:** Das panel speichert den Soll-Zustand jeder Instanz in SQLite und gleicht ihn in einer Schleife mit dem Ist-Zustand ab, den es beim agent abfragt. Der agent (root) ist der einzige Docker-Client: er validiert jede Anfrage mit dem gemeinsamen Paket `instancespec`, baut daraus eine feste, gehärtete Container-Vorlage und arbeitet schreibende Operationen asynchron in einer Warteschlange pro Instanz ab. mc-router routet nach Hostname; seine Routen-Datei schreibt der agent und lädt sie per SIGHUP neu.

**Tech Stack:** Go (Stand TP1, `go.mod`), `net/http` über Unix-Socket zur Docker Engine API **v1.44**, `modernc.org/sqlite` + goose + sqlc, chi; React 19 + TanStack Router/Query, shadcn/ui, react-i18next, MSW, Vitest, Playwright. Keine neuen Go-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md`

## Plan-Dateien und Reihenfolge

Die Aufgaben bauen in dieser Nummerierung aufeinander auf; welche parallel laufen, regelt der Abschnitt „Ausführung“. Jede Aufgabe endet mit grüner CI auf ihrem PR-Branch:

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-10-instances-00-overview.md` (diese) | 1–3 | Verträge: Migration + Queries, `instancespec`, agentapi + Client |
| `2026-10-10-instances-01-agent.md` | 4–10 | agent: Konfiguration, Docker-Client, Vorlage, Router, Manager, Endpunkte, Integrationstest |
| `2026-10-10-instances-02-panel.md` | 11–16 | panel: Paket `servers`, Einstellungen, CRUD, Power + Abgleich, Kataloge, Verdrahtung |
| `2026-10-10-instances-03-frontend.md` | 17–22 | Frontend: Grundlagen, Dashboard, Anlegen, Detailseite, Einstellungen, Playwright |
| `2026-10-10-instances-04-ci-docs.md` | 23–25 | CI-Abnahme mit echtem Docker, Doku, Checkliste für die manuelle Abnahme, Reviews |

## Ausführung (Agent-Teams, CI statt lokaler Tests)

Es gelten die Arbeitsregeln aus `CLAUDE.md`. Daraus folgt für alle Teilpläne:

- **Lokal nur `make check`.** Schritte der Form „Run: `go test …`“, `make test`, `make lint`, `make test-race`, `golangci-lint …`, `npm test`, `npx vitest …`, `npm run lint`, `npx tsc …`, Playwright oder `shellcheck` gegen Docker/root werden **nicht** auf dem Mac ausgeführt. Stattdessen: `make check` lokal (Format, Vet, Build für macOS und Linux, `tsc`), dann Commit, Push auf den PR-Branch und `gh pr checks <pr> --watch`. Die „Expected: PASS“-Angaben der Schritte werden in den CI-Logs geprüft (`gh run view <id> --log-failed` bei Fehlern). `make generate` und `shellcheck` sind erlaubt (keine Tests).
- **TDD bleibt:** Test zuerst schreiben, dann implementieren; die „rot“-Phase zeigt lokal `make check` (Kompilierfehler), die „grün“-Phase die CI. Eine Aufgabe ist erst fertig, wenn die CI ihres Commits grün ist.
- **Nichts Systemnahes auf dem Mac:** keine Container, kein `MCPANEL_DEV_DOCKER=1`, kein `sudo`; Docker- und root-Tests laufen nur in der CI.
- **Git:** nie auf `main`; ein Branch und Draft-PR pro Phase; jeder Teammate arbeitet in einem eigenen Worktree (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`). Mergen nur durch den Nutzer.

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/instances-contracts` | Lead | 1–3 | – |
| B1 | `feat/instances-agent` | Teammate „agent“ | 4–10 | A gemerged |
| B2 | `feat/instances-panel` | Teammate „panel“ | 11–16 | A gemerged |
| C | `feat/instances-frontend` | Teammate „frontend“ | 17–22 | B2 gemerged |
| D | `feat/instances-ci-docs` | Lead | 23–25 | B1 und C gemerged |

B1 und B2 laufen parallel und berühren disjunkte Dateien: B1 nur `internal/config/`, `internal/agent/**` (außer `instancespec/` und `agentapi/`), `cmd/agent/`, `.golangci.yml`, `.github/workflows/ci.yml` und `scripts/ci-acceptance.sh`; B2 nur `internal/servers/`, `internal/users/`, `internal/httpx/`, `internal/audit/`, `api/openapi.yaml`, `cmd/panel/`, `web/src/api/`, `web/src/mocks/` und minimal `web/src/features/dashboard/`. Der Spawn-Prompt jedes Teammates nennt die Plan-Datei, die Aufgabennummern, den Branch und diesen Abschnitt.

## Global Constraints

- Alle Global Constraints aus TP1 gelten weiter (`docs/superpowers/plans/2026-10-08-foundation-00-overview.md`, Abschnitt „Global Constraints“) und alle Konventionen aus `CLAUDE.md`: `clock.Clock` statt `time.Now()`, Kontext überall, Go-Tests nur mit Standardbibliothek, keine neuen Go-Abhängigkeiten, Lint-Ausnahmen nur gezielt mit Begründung, Conventional Commits mit Abschluss `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Schema-Änderungen nur in `internal/store/migrations/00002_instances.sql` (Aufgabe 1). Queries nur in `internal/store/queries/servers.sql`, Namenspräfix `Servers…`.
- Servername = Subdomain: `^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$` (`instancespec.ValidName`).
- `type` ∈ `vanilla|paper|purpur|fabric|neoforge|modrinth|curseforge`; `java` ∈ `java8|java17|java21|java25`; `memory_mib` 1024–32768, Vielfaches von 256.
- itzg-`TYPE`: vanilla→`VANILLA`, paper→`PAPER`, purpur→`PURPUR`, fabric→`FABRIC`, neoforge→`NEOFORGE`, modrinth→`MODRINTH` (Pack in `MODRINTH_MODPACK`), curseforge→`AUTO_CURSEFORGE` (`CF_SLUG` bzw. `CF_PAGE_URL`, `CF_API_KEY`).
- Container: Name `mcpanel-mc-<id>`, `User "<uid>:<gid>"` von `mcpanel-mc`, `CapDrop ["ALL"]` ohne `CapAdd`, `SecurityOpt ["no-new-privileges:true"]`, `Memory = memory_mib × 1.25 MiB`, `MemorySwap = Memory`, `PidsLimit 1024`, `RestartPolicy unless-stopped`, `StopTimeout 60`, Log `json-file` `max-size=10m` `max-file=3`, einziger Mount `<instances_dir>/instances/<id>` → `/data`, Netz `mcpanel-mc`, keine Ports.
- Labels: `mcpanel.instance=<id>`, `mcpanel.revision=<revision>`, `mcpanel.name=<name>`, `mcpanel.fqdn=<fqdn>`. Container ohne `mcpanel.instance` fasst der agent nie an.
- Router: Container `mcpanel-router`, Port `25565/tcp`, Mount `<instances_dir>/router` → `/config` (ro), `-routes-config /config/routes.json`, **kein** `-routes-config-watch`, Neuladen per `POST /containers/mcpanel-router/kill?signal=SIGHUP`. Routen-Datei: `{"mappings":{"<fqdn>":"mcpanel-mc-<id>:25565"}}`.
- Docker Engine API: Pfadpräfix `/v1.44`, Socket aus `docker_socket`.
- Image-Referenz an Docker: immer `repo@sha256:<digest>` (Tag aus der Konfiguration entfernt), weil der containerd-Image-Store `name:tag@digest` beim Inspect nicht zuverlässig auflöst (Aufgabe 5 `docker.ParseImageRef`/`ImageRef.Pinned`).
- Image-Defaults (gepinnt, verifiziert 2026-10-10):
  - `java8  = "itzg/minecraft-server:2026.9.2-java8@sha256:a23a59bc9bd520d2c1bae97225b8672771cf82c90073ea36dc18f18c199f325b"`
  - `java17 = "itzg/minecraft-server:2026.9.2-java17@sha256:0198cb9f0c7e7b1747645040e1245a6915c122662f3106f3b0979473a953ab61"`
  - `java21 = "itzg/minecraft-server:2026.9.2-java21@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa"`
  - `java25 = "itzg/minecraft-server:2026.9.2-java25@sha256:de5d1b1a83eba576f6c8a688fac2a3523ce457724cdebc8ea48d7818b74cdf6e"`
  - `router = "itzg/mc-router:1.47.2@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e"`
- agent-Draht: schreibende Instanz-Endpunkte antworten `202`, Body-Limit `/v1/instances/*` 16 KiB, Warteschlange pro Instanz max. 4 offene Operationen (sonst `429` mit `code: "busy"`), Docker nicht erreichbar → `503` mit `code: "docker_unavailable"`.
- Abgleich im panel: alle 10 s und sofort nach jeder Nutzeraktion.
- Katalog-Abrufe: Timeout 10 s, Antwort ≤ 5 MiB, Cache 1 h, User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`.
- Neue Fehlercodes: `docker_unavailable` (503), `base_domain_missing` (409), `catalog_unavailable` (502). Neue Audit-Aktionen: `server.update`, `server.settings_update`.
- Frontend: Texte `de` (Standard) und `en`, keine Inline-Skripte, keine CDNs, MSW-Mock für jede OpenAPI-Operation (`web/src/mocks/handlers.test.ts` zählt die Operationen exakt), kein visuelles Redesign.

## Review Focus

1. **Env-Injektion über Eigenschaften:** MOTD, Seed oder Spielernamen mit `\n`, `=`, Null-Byte oder Steuerzeichen dürfen keine zusätzliche Umgebungsvariable und keine kaputte `server.properties` erzeugen; erwartet: `400` im panel und im agent. → Tests in Aufgabe 2 (`instancespec`), Aufgabe 6 (Vorlage erzeugt nie andere Env-Schlüssel als die Allowlist).
2. **Fremde Container:** Ein Container ohne `mcpanel.instance`-Label (z. B. ein anderer Dienst des Betreibers) mit Namen `mcpanel-mc-7` darf vom agent weder gestoppt noch gelöscht noch überschrieben werden; erwartet: Operation scheitert mit `last_error`, Container bleibt unverändert. → Test in Aufgabe 8 (Manager mit Fake-Docker).
3. **Name schon vergeben/Server ohne Basisdomain:** Anlegen ohne Basisdomain → `409 base_domain_missing` ohne DB-Zeile; doppelter Name → `409 conflict` ohne halbe `instances`-Zeile. → Tests in Aufgabe 13.
4. **agent fällt während einer Operation aus:** panel zeigt `unknown`, speichert Power-Wünsche und holt sie nach, sobald der agent wieder antwortet; kein doppelter `ensure` pro Durchlauf, keine Endlosschleife bei dauerhaftem `last_error`. → Tests in Aufgabe 14 (Abgleich).
5. **Upstream-Katalog liefert Müll:** riesige Antwort, falsches JSON, Timeout, Version mit Leerzeichen → `502 catalog_unavailable` bzw. Eintrag verworfen; das Formular fällt auf Freitext zurück. → Tests in Aufgabe 15 (Katalog).

## Dateistruktur

```
internal/store/migrations/00002_instances.sql      (1)  Schema
internal/store/queries/servers.sql                 (1)  Queries Servers…
internal/store/servers.sql.go                      (1)  generiert
internal/agent/instancespec/                       (2)  Spec-Typen, Validierung, Java-Ableitung, itzg-Env
  instancespec.go, properties.go, java.go, env.go, testdata/java_cases.json
internal/agent/agentapi/instances.go               (3)  Draht-Typen + InstancesClient
internal/agent/client.go                           (3)  Instanz-Methoden, Fehler-Codes
internal/config/config.go                          (4)  agent: docker_socket, instances_dir, mc_user, [images]
internal/agent/docker/                             (5)  schmaler Docker-Engine-Client
internal/agent/instances/template.go               (6)  Spec → Container-Konfiguration
internal/agent/instances/router.go                 (7)  Routen-Datei
internal/agent/instances/manager.go, worker.go     (8)  Warteschlangen, ensure/power/remove, Status, Stats
internal/agent/server.go                           (9)  Endpunkte /v1/instances*
cmd/agent/main.go                                  (9)  Verdrahtung
internal/agent/docker/integration_linux_test.go    (10) Build-Tag dockerintegration
internal/servers/                                  (11–15) panel: Handler, Einstellungen, Abgleich, Katalog
internal/httpx/errors.go, internal/audit/types.go  (12, 13) Codes, Aktionen
api/openapi.yaml, web/src/api/schema.d.ts          (12–15) Vertrag
cmd/panel/app.go                                   (16) Verdrahtung
web/src/features/servers/, settings/               (17–22) Frontend
web/src/mocks/handlers/servers.ts, db.ts           (12–15) Mocks
scripts/mc-status.py, scripts/ci-instances.sh     (23) CI-Abnahme
docs/dev/*.md, CLAUDE.md                          (24–25)
```

## Verträge (für alle Teilpläne verbindlich)

### V1 – Datenbank

Siehe Aufgabe 1 (vollständiges SQL). Generierte Typen (sqlc, Paket `store`):

```go
type Instance struct {
	ServerID      int64
	Type          string
	Version       string
	LoaderVersion sql.NullString
	Modpack       sql.NullString
	Java          string
	MemoryMib     int64
	Properties    string // JSON von instancespec.Properties
	DesiredState  string // "running" | "stopped"
	Revision      int64
	CreatedAt     int64
	UpdatedAt     int64
}
type InstanceSetting struct {
	ID               int64
	BaseDomain       sql.NullString
	CurseforgeApiKey []byte // secretbox.Seal(key, aad "instance_settings.curseforge_api_key")
	UpdatedAt        int64
}
func (q *Queries) ServersCreateInstance(ctx, ServersCreateInstanceParams) (Instance, error)
func (q *Queries) ServersGetInstance(ctx, serverID int64) (Instance, error)
func (q *Queries) ServersListInstances(ctx) ([]ServersListInstancesRow, error) // Instance-Felder + Name
func (q *Queries) ServersUpdateInstance(ctx, ServersUpdateInstanceParams) (Instance, error) // revision+1
func (q *Queries) ServersSetDesiredState(ctx, ServersSetDesiredStateParams) (int64, error)
func (q *Queries) ServersBumpAllRevisions(ctx, updatedAt int64) error
func (q *Queries) ServersGetSettings(ctx) (InstanceSetting, error)
func (q *Queries) ServersUpdateSettings(ctx, ServersUpdateSettingsParams) error
```

### V2 – `instancespec` (Aufgabe 2)

```go
package instancespec // github.com/Bernotese/mc-panel/internal/agent/instancespec

type Type string // TypeVanilla … TypeCurseForge; var Types []Type
type Java string // Java8, Java17, Java21, Java25; var Javas []Java
const MinMemoryMiB, MaxMemoryMiB, MemoryStepMiB = 1024, 32768, 256

var ErrInvalid error // alle Validierungsfehler wrappen ErrInvalid; Meldung "<feld>: <grund>"

type Properties struct { /* Zeiger/Slices, json-Namen wie §7 der Spec, omitempty */
}
type Spec struct {
	Type             Type       `json:"type"`
	Version          string     `json:"version"`
	LoaderVersion    *string    `json:"loader_version"`
	Modpack          *string    `json:"modpack"`
	Java             Java       `json:"java"`
	MemoryMiB        int        `json:"memory_mib"`
	Properties       Properties `json:"properties"`
	CurseForgeAPIKey *string    `json:"curseforge_api_key"`
}

func (s Spec) Validate() error
func (p Properties) Validate() error
func Resolve(name string, p Properties) Properties // alle Felder gesetzt (Defaults aus §7)
func Env(name string, s Spec) ([]string, error)    // validiert, dann "KEY=VALUE" in fester Reihenfolge
func JavaFor(t Type, version string) Java
func ValidName(name string) bool
func ValidBaseDomain(d string) error
func FQDN(name, baseDomain string) (string, error)
func CheckFQDN(name, fqdn string) error
```

### V3 – agentapi (Aufgabe 3)

```go
const (
	PathInstances      = "/v1/instances"
	PathInstanceEnsure = "/v1/instances/ensure"
	PathInstancePower  = "/v1/instances/power"
	PathInstanceRemove = "/v1/instances/remove"
	MaxInstanceRequestBytes = 16 << 10
	ErrorCodeBusy              = "busy"
	ErrorCodeDockerUnavailable = "docker_unavailable"
	DockerOK, DockerUnavailable = "ok", "unavailable"
)
// HealthResponse bekommt: Docker string `json:"docker"` ("ok" | "unavailable")
// ErrorResponse bekommt:  Code string `json:"code,omitempty"`
var ErrBusy, ErrDockerUnavailable error
type PowerAction string // PowerStart, PowerStop, PowerRestart; func ValidPowerAction(PowerAction) bool
type EnsureRequest struct { ID int64; Name, FQDN string; Revision int64; Spec instancespec.Spec; Recreate bool }
type PowerRequest struct { ID int64; Action PowerAction }
type RemoveRequest struct { ID int64 }
type ContainerState string // created, running, restarting, exited, missing
type Operation string      // "", pulling, creating, starting, stopping, removing
type InstanceStatus struct {
	ID int64; Name string; Revision int64; State ContainerState; ExitCode int; StartedAt int64
	MemoryBytes, MemoryLimitBytes int64; CPUPercent float64; Operation Operation; LastError string
}
type RouterStatus struct { State ContainerState; LastError string }
type InstancesResponse struct { Docker string; Instances []InstanceStatus; Router RouterStatus }
type InstancesClient interface {
	Instances(ctx context.Context) (InstancesResponse, error)
	Ensure(ctx context.Context, req EnsureRequest) error
	Power(ctx context.Context, id int64, action PowerAction) error
	Remove(ctx context.Context, id int64) error
}
func (r EnsureRequest) Validate() error // id>0, ValidName, CheckFQDN, revision>0, Spec.Validate
```

`*agent.Client` implementiert `agentapi.Client` **und** `agentapi.InstancesClient`. `agentapi.Client` bleibt unverändert (bestehende Fakes in `defense` und `cmd/panel` brechen nicht).

### V4 – panel-API (OpenAPI, eingeführt in Aufgaben 12–15)

Neue/geänderte Schemas in `api/openapi.yaml` (unter `components.schemas`), verbindlich für Backend, Mocks und Frontend:

```yaml
    InstanceType: { type: string, enum: [vanilla, paper, purpur, fabric, neoforge, modrinth, curseforge] }
    JavaVersion: { type: string, enum: [java8, java17, java21, java25] }
    ServerState: { type: string, enum: [not_configured, unknown, creating, starting, running, stopping, stopped, restarting, error] }
    ServerProperties:
      type: object
      additionalProperties: false
      properties:
        motd: { type: string, maxLength: 150 }
        difficulty: { type: string, enum: [peaceful, easy, normal, hard] }
        gamemode: { type: string, enum: [survival, creative, adventure, spectator] }
        max_players: { type: integer, minimum: 1, maximum: 500 }
        pvp: { type: boolean }
        hardcore: { type: boolean }
        allow_flight: { type: boolean }
        enable_command_block: { type: boolean }
        online_mode: { type: boolean }
        whitelist_enabled: { type: boolean }
        whitelist: { type: array, maxItems: 200, items: { type: string, pattern: '^[A-Za-z0-9_]{3,16}$' } }
        ops: { type: array, maxItems: 50, items: { type: string, pattern: '^[A-Za-z0-9_]{3,16}$' } }
        view_distance: { type: integer, minimum: 3, maximum: 32 }
        simulation_distance: { type: integer, minimum: 3, maximum: 32 }
        spawn_protection: { type: integer, minimum: 0, maximum: 256 }
        seed: { type: string, maxLength: 64 }
        level_type: { type: string, enum: [normal, flat, large_biomes, amplified] }
    InstanceConfig:
      type: object
      required: [type, version, loader_version, modpack, java, memory_mib, properties]
      properties:
        type: { $ref: '#/components/schemas/InstanceType' }
        version: { type: string, description: "'LATEST', a version, or '' for modpacks" }
        loader_version: { type: [string, 'null'] }
        modpack: { type: [string, 'null'] }
        java: { $ref: '#/components/schemas/JavaVersion' }
        memory_mib: { type: integer, minimum: 1024, maximum: 32768, multipleOf: 256 }
        properties: { $ref: '#/components/schemas/ServerProperties', description: All fields set (defaults resolved) }
    ServerStatus:
      type: object
      required: [state, desired_state, restart_required, operation, last_error, started_at, memory_bytes, memory_limit_bytes, cpu_percent]
      properties:
        state: { $ref: '#/components/schemas/ServerState' }
        desired_state: { type: [string, 'null'], enum: [running, stopped, null] }
        restart_required: { type: boolean }
        operation: { type: string, enum: ['', pulling, creating, starting, stopping, removing] }
        last_error: { type: string }
        started_at: { type: integer, format: int64, description: Unix ms; 0 = not running }
        memory_bytes: { type: integer, format: int64 }
        memory_limit_bytes: { type: integer, format: int64 }
        cpu_percent: { type: number }
    Server:
      type: object
      required: [id, name, permissions, address, instance, status]
      properties:
        id: { type: integer, format: int64 }
        name: { type: string }
        permissions: { type: array, items: { $ref: '#/components/schemas/Permission' }, description: Effective permissions of the caller (admins get all) }
        address: { type: [string, 'null'], description: '<name>.<base_domain>; null without base domain' }
        instance: { anyOf: [{ $ref: '#/components/schemas/InstanceConfig' }, { type: 'null' }] }
        status: { $ref: '#/components/schemas/ServerStatus' }
    CreateServerRequest:
      type: object
      additionalProperties: false
      required: [name, type, version, memory_mib, eula_accepted]
      properties:
        name: { type: string, pattern: '^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$' }
        type: { $ref: '#/components/schemas/InstanceType' }
        version: { type: string }
        loader_version: { type: [string, 'null'] }
        modpack: { type: [string, 'null'] }
        java: { $ref: '#/components/schemas/JavaVersion', description: Derived from type/version if omitted }
        memory_mib: { type: integer, minimum: 1024, maximum: 32768, multipleOf: 256 }
        properties: { $ref: '#/components/schemas/ServerProperties' }
        eula_accepted: { type: boolean, const: true }
    UpdateServerRequest:
      type: object
      additionalProperties: false
      description: Omitted fields stay unchanged; properties replaces the whole object.
      properties:
        version: { type: string }
        loader_version: { type: [string, 'null'] }
        modpack: { type: [string, 'null'] }
        java: { $ref: '#/components/schemas/JavaVersion' }
        memory_mib: { type: integer, minimum: 1024, maximum: 32768, multipleOf: 256 }
        properties: { $ref: '#/components/schemas/ServerProperties' }
    CatalogVersion:
      type: object
      required: [id, java]
      properties:
        id: { type: string }
        java: { $ref: '#/components/schemas/JavaVersion' }
    Catalog:
      type: object
      required: [versions, loaders]
      properties:
        versions: { type: array, items: { $ref: '#/components/schemas/CatalogVersion' }, description: Newest first }
        loaders: { type: array, items: { type: string }, description: 'fabric: stable loader versions, newest first; all other types: []' }
    UnknownContainer:
      type: object
      required: [id, name]
      properties:
        id: { type: integer, format: int64 }
        name: { type: string }
    ServerSettings:
      type: object
      required: [base_domain, curseforge_api_key_set, docker, unknown_containers]
      properties:
        base_domain: { type: [string, 'null'] }
        curseforge_api_key_set: { type: boolean }
        docker: { type: string, enum: [ok, unavailable, unknown] }
        unknown_containers: { type: array, items: { $ref: '#/components/schemas/UnknownContainer' } }
    UpdateServerSettingsRequest:
      type: object
      additionalProperties: false
      description: Omitted fields stay unchanged; null removes the value.
      properties:
        base_domain: { type: [string, 'null'] }
        curseforge_api_key: { type: [string, 'null'] }
```

**NeoForge im Katalog:** `versions` sind **Minecraft-Versionen** (aus den stabilen NeoForge-Versionen abgeleitet, dedupliziert, neueste zuerst), `loaders` ist `[]`. `loader_version` bleibt `null`, außer der Admin trägt unter „Erweitert“ eine NeoForge-Version ein; ohne sie wählt itzg die neueste stabile NeoForge-Version für die MC-Version.

Neue/geänderte Operationen:

| Methode, Pfad | operationId | Regel | Request | Antwort |
|---|---|---|---|---|
| `GET /servers` | `listServers` | Authenticated | – | `200 Server[]` |
| `POST /servers` | `createServer` | Admin + Sudo | `CreateServerRequest` | `201 Server`; 400, 409 (`conflict`, `base_domain_missing`) |
| `GET /servers/{id}` | `getServer` | `server.view` | – | `200 Server` |
| `PATCH /servers/{id}` | `updateServer` | Admin + Sudo | `UpdateServerRequest` | `200 Server`; 400, 404, 409 (nicht eingerichtet) |
| `DELETE /servers/{id}` | `deleteServer` | Admin + Sudo | – | `204`; 404, 429 (agent-Warteschlange voll), 503 (`agent_unavailable`, `docker_unavailable`; Response-Komponente `ServiceUnavailable`) |
| `POST /servers/{id}/power` | `serverPower` | `server.power` | `PowerRequest` | `202 PowerResponse`; 400, 404, 409 (nicht eingerichtet), 429, 503 (nur `restart` ohne agent) |
| `GET /servers/catalog/{type}` | `getServerCatalog` | Admin | Pfad `type: InstanceType` | `200 Catalog`; 404 (modrinth/curseforge), 502 `catalog_unavailable` |
| `GET /servers/settings` | `getServerSettings` | Admin | – | `200 ServerSettings` |
| `PUT /servers/settings` | `updateServerSettings` | Admin + Sudo | `UpdateServerSettingsRequest` | `200 ServerSettings`; 400 |

Damit steigt die Zahl der Operationen von 57 auf 61 (`web/src/mocks/handlers.test.ts`).

**`ServerStatus.state` – Ableitung im panel (`internal/servers/state.go` und Status-Cache, Aufgabe 13; der Abgleich aus Aufgabe 14 füllt den Cache):**

| Bedingung (in dieser Reihenfolge) | `state` |
|---|---|
| keine `instances`-Zeile | `not_configured` (`desired_state: null`) |
| agent nicht erreichbar oder Docker `unavailable` | `unknown` |
| Container `missing`, keine `operation`, `last_error != ""` (agent- oder panel-seitig) | `error` |
| agent meldet einen Container, dessen `name` nicht zur DB-Zeile passt | `error` (fremder Container, keine Aktionen) |
| `operation` ∈ {pulling, creating} oder Container `missing` | `creating` |
| `operation = starting` | `starting` |
| `operation` ∈ {stopping, removing} | `stopping` |
| Container `restarting` | `restarting` |
| Container `running` | `running` |
| Container `exited`/`created`, `desired_state = running`, (`exit_code != 0` oder `last_error != ""`) | `error` |
| Container `exited`/`created` | `stopped` |

`restart_required` = Container existiert, läuft, und seine Label-Revision < `instances.revision`.

### V5 – MSW-Mock (eingeführt in Aufgaben 12–15, genutzt von Aufgaben 17–22)

`web/src/mocks/db.ts`:

```ts
export interface MockServer {
  id: number
  name: string
  instance: InstanceConfig | null // properties immer vollständig aufgelöst (Defaults aus Spec §7)
  status: ServerStatus
  revision: number // Soll-Revision
  runningRevision: number // Revision des „laufenden Containers“
}
export interface MockServerSettings {
  base_domain: string | null
  curseforge_api_key_set: boolean
  docker: 'ok' | 'unavailable' | 'unknown'
}
// MockDb bekommt: serverSettings: MockServerSettings; catalogs: Partial<Record<InstanceType, Catalog>>
```

Seed (ersetzt die bisherigen drei Stub-Server, IDs und Namen bleiben):

| id | name | instance | status.state | desired_state |
|---|---|---|---|---|
| 1 | survival | paper `1.21.11`, java21, 4096 MiB | `running` (memory 2.1 GiB von 5 GiB, cpu 12.5) | running |
| 2 | creative | vanilla `26.3`, java25, 2048 MiB, gamemode creative | `stopped` | stopped |
| 3 | lobby | `null` | `not_configured` | `null` |

`serverSettings`: `{ base_domain: 'mc.example.com', curseforge_api_key_set: false, docker: 'ok' }`.
`catalogs`: `vanilla` (`26.3`, `26.2`, `1.21.11`, `1.20.4`, `1.16.5` mit passendem `java`), `paper` (`26.2`, `1.21.11`, `1.20.4`), `purpur` (`26.2`, `1.21.11`), `fabric` (Versionen `26.3`, `1.21.11`; `loaders: ['0.19.5', '0.19.4']`), `neoforge` (Minecraft-Versionen `26.2` → java25, `1.21.11` → java21; `loaders: []`).

Verhalten (in `web/src/mocks/handlers/servers.ts`):

- `createServer`: legt den Server mit `status.state = 'creating'`, `operation = 'pulling'` an; der **nächste** `getServer`/`listServers` liefert ihn als `running` (`operation: ''`, `started_at = now`). Ohne `base_domain` → `409 base_domain_missing`. `eula_accepted !== true` → `400 validation_failed`. `java` fehlt → aus derselben Tabelle abgeleitet (`web/src/features/servers/java.ts`, Aufgabe 17 – bis dahin inline im Mock; Aufgabe 17 stellt auf den Import um).
- `updateServer`: `revision += 1`; ist der Server `running`, setzt er `restart_required = true`, sonst übernimmt er die Revision sofort.
- `serverPower`: `start` → `running`, `stop` → `stopped`, `restart` → `running` mit `runningRevision = revision`, `restart_required = false`. Nicht eingerichtet → `409 conflict`.
- `deleteServer`: entfernt den Server.
- `getServerCatalog`: liefert `catalogs[type]`; `modrinth`/`curseforge` → `404 not_found`; fehlender Eintrag → `502 catalog_unavailable`.
- `getServerSettings`/`updateServerSettings`: wie V4; `curseforge_api_key: 'x'` setzt `curseforge_api_key_set = true`, `null` setzt es auf `false`.
- Rechte wie bisher über `serverPermissions` (`guard`, Admin + Sudo für Anlegen/Ändern/Löschen/Einstellungen).

---

### Task 1: Migration und Queries

**Files:**
- Create: `internal/store/migrations/00002_instances.sql`
- Create: `internal/store/queries/servers.sql`
- Create (generiert): `internal/store/servers.sql.go`; geändert (generiert): `internal/store/models.go`
- Test: `internal/store/servers_test.go`

**Interfaces:**
- Consumes: `storetest.Open`, `storetest.CreateServer` (TP1)
- Produces: V1

- [ ] **Step 1: Failing test schreiben** – `internal/store/servers_test.go`

```go
package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func createInstance(t *testing.T, q *store.Queries, serverID int64) store.Instance {
	t.Helper()
	in, err := q.ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: serverID, Type: "paper", Version: "1.21.4", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: 10, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestInstanceLifecycle(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	sid := storetest.CreateServer(t, db, "survival")

	in := createInstance(t, q, sid)
	if in.Revision != 1 || in.DesiredState != "running" || in.LoaderVersion.Valid || in.Modpack.Valid {
		t.Fatalf("created = %+v", in)
	}
	up, err := q.ServersUpdateInstance(ctx, store.ServersUpdateInstanceParams{
		Version: "1.21.5", Java: "java21", MemoryMib: 6144, Properties: `{"motd":"x"}`, UpdatedAt: 20, ServerID: sid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Revision != 2 || up.Version != "1.21.5" || up.MemoryMib != 6144 || up.UpdatedAt != 20 {
		t.Fatalf("updated = %+v", up)
	}
	n, err := q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{DesiredState: "stopped", UpdatedAt: 30, ServerID: sid})
	if err != nil || n != 1 {
		t.Fatalf("set desired: n=%d err=%v", n, err)
	}
	if err := q.ServersBumpAllRevisions(ctx, 40); err != nil {
		t.Fatal(err)
	}
	got, err := q.ServersGetInstance(ctx, sid)
	if err != nil || got.Revision != 3 || got.DesiredState != "stopped" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	list, err := q.ServersListInstances(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "survival" || list[0].ServerID != sid {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", sid); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ServersGetInstance(ctx, sid); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("instance must be deleted with its server, got %v", err)
	}
}

func TestInstanceChecks(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	sid := storetest.CreateServer(t, db, "survival")
	bad := []string{
		`INSERT INTO instances VALUES (?, 'bukkit', 'x', NULL, NULL, 'java21', 4096, '{}', 'running', 1, 0, 0)`,
		`INSERT INTO instances VALUES (?, 'paper', 'x', NULL, NULL, 'java11', 4096, '{}', 'running', 1, 0, 0)`,
		`INSERT INTO instances VALUES (?, 'paper', 'x', NULL, NULL, 'java21', 512, '{}', 'running', 1, 0, 0)`,
		`INSERT INTO instances VALUES (?, 'paper', 'x', NULL, NULL, 'java21', 4096, '{}', 'paused', 1, 0, 0)`,
	}
	for _, stmt := range bad {
		if _, err := db.ExecContext(ctx, stmt, sid); err == nil {
			t.Errorf("CHECK must reject: %s", stmt)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO instances VALUES (999, 'paper', 'x', NULL, NULL, 'java21', 4096, '{}', 'running', 1, 0, 0)`); err == nil {
		t.Error("instance without server must be rejected")
	}
}

func TestInstanceSettingsRow(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	s, err := q.ServersGetSettings(ctx)
	if err != nil || s.ID != 1 || s.BaseDomain.Valid || s.CurseforgeApiKey != nil {
		t.Fatalf("default settings = %+v, %v", s, err)
	}
	if err := q.ServersUpdateSettings(ctx, store.ServersUpdateSettingsParams{
		BaseDomain: sql.NullString{String: "mc.example.com", Valid: true}, CurseforgeApiKey: []byte{1, 2}, UpdatedAt: 5,
	}); err != nil {
		t.Fatal(err)
	}
	s, err = q.ServersGetSettings(ctx)
	if err != nil || s.BaseDomain.String != "mc.example.com" || len(s.CurseforgeApiKey) != 2 || s.UpdatedAt != 5 {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO instance_settings (id, updated_at) VALUES (2, 0)"); err == nil {
		t.Fatal("only one settings row (id = 1) is allowed")
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/store -run 'TestInstance' -count=1`
Expected: FAIL beim Kompilieren (`store.ServersCreateInstanceParams` undefiniert).

- [ ] **Step 3: Migration schreiben** – `internal/store/migrations/00002_instances.sql`

```sql
-- +goose Up
CREATE TABLE instances (
    server_id      INTEGER PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    type           TEXT    NOT NULL CHECK (type IN ('vanilla', 'paper', 'purpur', 'fabric',
                                                    'neoforge', 'modrinth', 'curseforge')),
    version        TEXT    NOT NULL,
    loader_version TEXT,
    modpack        TEXT,
    java           TEXT    NOT NULL CHECK (java IN ('java8', 'java17', 'java21', 'java25')),
    memory_mib     INTEGER NOT NULL CHECK (memory_mib BETWEEN 1024 AND 32768),
    properties     TEXT    NOT NULL,
    desired_state  TEXT    NOT NULL CHECK (desired_state IN ('running', 'stopped')),
    revision       INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE instance_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    base_domain        TEXT,
    curseforge_api_key BLOB,
    updated_at         INTEGER NOT NULL
);
INSERT INTO instance_settings (id, base_domain, curseforge_api_key, updated_at) VALUES (1, NULL, NULL, 0);

-- +goose Down
DROP TABLE instance_settings;
DROP TABLE instances;
```

- [ ] **Step 4: Queries schreiben** – `internal/store/queries/servers.sql`

```sql
-- Sub-project 2: Minecraft instances and their settings. All query names start with "Servers".

-- name: ServersCreateInstance :one
INSERT INTO instances (server_id, type, version, loader_version, modpack, java, memory_mib, properties,
                       desired_state, revision, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'running', 1, ?, ?)
RETURNING *;

-- name: ServersGetInstance :one
SELECT * FROM instances WHERE server_id = ?;

-- name: ServersListInstances :many
SELECT s.name, i.server_id, i.type, i.version, i.loader_version, i.modpack, i.java, i.memory_mib,
       i.properties, i.desired_state, i.revision, i.created_at, i.updated_at
FROM instances i JOIN servers s ON s.id = i.server_id
ORDER BY s.name;

-- name: ServersUpdateInstance :one
UPDATE instances
SET version = ?, loader_version = ?, modpack = ?, java = ?, memory_mib = ?, properties = ?,
    revision = revision + 1, updated_at = ?
WHERE server_id = ?
RETURNING *;

-- name: ServersSetDesiredState :execrows
UPDATE instances SET desired_state = ?, updated_at = ? WHERE server_id = ?;

-- name: ServersBumpAllRevisions :exec
UPDATE instances SET revision = revision + 1, updated_at = ?;

-- name: ServersGetSettings :one
SELECT * FROM instance_settings WHERE id = 1;

-- name: ServersUpdateSettings :exec
UPDATE instance_settings SET base_domain = ?, curseforge_api_key = ?, updated_at = ? WHERE id = 1;
```

- [ ] **Step 5: Code generieren**

Run: `make generate`
Expected: neue Datei `internal/store/servers.sql.go`, `internal/store/models.go` enthält `Instance` und `InstanceSetting`. Prüfen, dass die Feldnamen V1 entsprechen (`MemoryMib`, `CurseforgeApiKey`, `ServersListInstancesRow.Name`); weichen sie ab, die Tests an die generierten Namen anpassen und V1 in dieser Datei korrigieren.

- [ ] **Step 6: Tests laufen lassen (grün)**

Run: `go test ./internal/store -count=1`
Expected: PASS (inkl. bestehender Tests; `TestMigrationsCreateTables` o. ä. bleibt grün).

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/00002_instances.sql internal/store/queries/servers.sql internal/store/servers.sql.go internal/store/models.go internal/store/servers_test.go
git commit -m "feat(store): add instances and instance_settings tables

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `instancespec` – gemeinsame Validierung, Java-Ableitung, itzg-Umgebung

**Files:**
- Create: `internal/agent/instancespec/instancespec.go`, `properties.go`, `java.go`, `env.go`
- Create: `internal/agent/instancespec/testdata/java_cases.json` (wird in Aufgabe 17 auch vom Frontend gelesen)
- Test: `internal/agent/instancespec/instancespec_test.go`, `properties_test.go`, `java_test.go`, `env_test.go`

**Interfaces:**
- Consumes: nichts (reines Paket, nur Standardbibliothek)
- Produces: V2

- [ ] **Step 1: Java-Fälle als gemeinsame Testdaten** – `internal/agent/instancespec/testdata/java_cases.json`

```json
[
  { "type": "vanilla", "version": "1.8.9", "java": "java8" },
  { "type": "paper", "version": "1.16.5", "java": "java8" },
  { "type": "paper", "version": "1.17", "java": "java17" },
  { "type": "paper", "version": "1.17.1", "java": "java17" },
  { "type": "fabric", "version": "1.18.2", "java": "java17" },
  { "type": "paper", "version": "1.20.4", "java": "java17" },
  { "type": "paper", "version": "1.20.5", "java": "java21" },
  { "type": "neoforge", "version": "1.20.6", "java": "java21" },
  { "type": "purpur", "version": "1.21", "java": "java21" },
  { "type": "paper", "version": "1.21.11", "java": "java21" },
  { "type": "vanilla", "version": "26.1", "java": "java25" },
  { "type": "paper", "version": "26.1.2", "java": "java25" },
  { "type": "vanilla", "version": "26.3", "java": "java25" },
  { "type": "vanilla", "version": "LATEST", "java": "java25" },
  { "type": "fabric", "version": "26.4-snapshot-3", "java": "java25" },
  { "type": "paper", "version": "1.20.4-rc1", "java": "java17" },
  { "type": "modrinth", "version": "", "java": "java21" },
  { "type": "curseforge", "version": "", "java": "java21" }
]
```

- [ ] **Step 2: Failing tests schreiben**

`internal/agent/instancespec/java_test.go`:

```go
package instancespec_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestJavaForSharedCases(t *testing.T) {
	raw, err := os.ReadFile("testdata/java_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Type    instancespec.Type `json:"type"`
		Version string            `json:"version"`
		Java    instancespec.Java `json:"java"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		if got := instancespec.JavaFor(c.Type, c.Version); got != c.Java {
			t.Errorf("JavaFor(%s, %q) = %s, want %s", c.Type, c.Version, got, c.Java)
		}
	}
}
```

`internal/agent/instancespec/instancespec_test.go`:

```go
package instancespec_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func ptr[T any](v T) *T { return &v }

func validSpec() instancespec.Spec {
	return instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096}
}

func TestValidSpecs(t *testing.T) {
	cases := map[string]func(*instancespec.Spec){
		"paper":         func(*instancespec.Spec) {},
		"latest":        func(s *instancespec.Spec) { s.Version = "LATEST" },
		"fabric loader": func(s *instancespec.Spec) { s.Type = instancespec.TypeFabric; s.LoaderVersion = ptr("0.19.5") },
		"neoforge":      func(s *instancespec.Spec) { s.Type = instancespec.TypeNeoForge; s.LoaderVersion = ptr("21.4.140") },
		"modrinth": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeModrinth
			s.Version = ""
			s.Modpack = ptr("cobblemon-fabric")
		},
		"curseforge url": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("https://www.curseforge.com/minecraft/modpacks/all-the-mods-10")
			s.CurseForgeAPIKey = ptr("$2a$10$abcdefghijklmnopqrstuv")
		},
		"min memory": func(s *instancespec.Spec) { s.MemoryMiB = 1024 },
		"max memory": func(s *instancespec.Spec) { s.MemoryMiB = 32768 },
	}
	for name, mod := range cases {
		s := validSpec()
		mod(&s)
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestInvalidSpecs(t *testing.T) {
	cases := map[string]func(*instancespec.Spec){
		"unknown type":       func(s *instancespec.Spec) { s.Type = "bukkit" },
		"empty version":      func(s *instancespec.Spec) { s.Version = "" },
		"version with space": func(s *instancespec.Spec) { s.Version = "1.21 4" },
		"version newline":    func(s *instancespec.Spec) { s.Version = "1.21\nEULA=FALSE" },
		"version too long":   func(s *instancespec.Spec) { s.Version = strings.Repeat("1", 65) },
		"loader on paper":    func(s *instancespec.Spec) { s.LoaderVersion = ptr("1") },
		"bad loader":         func(s *instancespec.Spec) { s.Type = instancespec.TypeFabric; s.LoaderVersion = ptr("0.19 5") },
		"modpack on paper":   func(s *instancespec.Spec) { s.Modpack = ptr("x") },
		"modrinth no pack":   func(s *instancespec.Spec) { s.Type = instancespec.TypeModrinth; s.Version = "" },
		"modrinth version":   func(s *instancespec.Spec) { s.Type = instancespec.TypeModrinth; s.Modpack = ptr("x") },
		"modrinth bad slug": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeModrinth
			s.Version = ""
			s.Modpack = ptr("Bad Slug")
		},
		"curseforge other url": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("https://evil.example/minecraft/modpacks/x")
			s.CurseForgeAPIKey = ptr("k")
		},
		"curseforge no key": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("atm10")
		},
		"curseforge java8": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("atm10")
			s.CurseForgeAPIKey = ptr("k")
			s.Java = instancespec.Java8
		},
		"key on paper": func(s *instancespec.Spec) { s.CurseForgeAPIKey = ptr("k") },
		"key with newline": func(s *instancespec.Spec) {
			s.Type = instancespec.TypeCurseForge
			s.Version = ""
			s.Modpack = ptr("atm10")
			s.CurseForgeAPIKey = ptr("k\nX=1")
		},
		"unknown java":     func(s *instancespec.Spec) { s.Java = "java11" },
		"memory too small": func(s *instancespec.Spec) { s.MemoryMiB = 768 },
		"memory too large": func(s *instancespec.Spec) { s.MemoryMiB = 33024 },
		"memory step":      func(s *instancespec.Spec) { s.MemoryMiB = 4000 },
		"bad property":     func(s *instancespec.Spec) { s.Properties.MaxPlayers = ptr(0) },
	}
	for name, mod := range cases {
		s := validSpec()
		mod(&s)
		err := s.Validate()
		if !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestNamesAndDomains(t *testing.T) {
	for _, n := range []string{"survival", "a1", "creative-2", strings.Repeat("a", 32)} {
		if !instancespec.ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	for _, n := range []string{"", "a", "-x", "Upper", "a_b", "a.b", strings.Repeat("a", 33)} {
		if instancespec.ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
	for _, d := range []string{"mc.example.com", "example.de", "mc.localhost", "a-b.c-d.example"} {
		if err := instancespec.ValidBaseDomain(d); err != nil {
			t.Errorf("ValidBaseDomain(%q): %v", d, err)
		}
	}
	for _, d := range []string{"", "localhost", "MC.example.com", "-a.example", "a..b", "a.b.", ".a.b", "a_b.example", "a.b:25565", strings.Repeat("a.", 101) + "de"} {
		if err := instancespec.ValidBaseDomain(d); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("ValidBaseDomain(%q) = %v", d, err)
		}
	}
	fqdn, err := instancespec.FQDN("survival", "mc.example.com")
	if err != nil || fqdn != "survival.mc.example.com" {
		t.Fatalf("FQDN = %q, %v", fqdn, err)
	}
	if err := instancespec.CheckFQDN("survival", "survival.mc.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"creative.mc.example.com", "survival", "survival.", "survivalx.example.com", "survival.mc.example.com\n"} {
		if err := instancespec.CheckFQDN("survival", f); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("CheckFQDN(survival, %q) = %v", f, err)
		}
	}
}
```

`internal/agent/instancespec/properties_test.go`:

```go
package instancespec_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func TestPropertiesValidation(t *testing.T) {
	names200 := make([]string, 200)
	for i := range names200 {
		names200[i] = "player" + strings.Repeat("x", i%8)
	}
	valid := []instancespec.Properties{
		{},
		{MOTD: ptr("§aWillkommen auf unserem Server äöü"), Difficulty: ptr("hard"), Gamemode: ptr("creative")},
		{MaxPlayers: ptr(1), ViewDistance: ptr(3), SimulationDistance: ptr(32), SpawnProtection: ptr(0)},
		{Whitelist: names200, Ops: []string{"Notch", "jeb_"}, WhitelistEnabled: ptr(true)},
		{Seed: ptr("-123456789"), LevelType: ptr("large_biomes")},
	}
	for i, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("valid[%d]: %v", i, err)
		}
	}
	invalid := map[string]instancespec.Properties{
		"motd newline":       {MOTD: ptr("a\nENABLE_RCON=false")},
		"motd nul":           {MOTD: ptr("a\x00b")},
		"motd tab":           {MOTD: ptr("a\tb")},
		"motd too long":      {MOTD: ptr(strings.Repeat("ä", 151))},
		"difficulty":         {Difficulty: ptr("insane")},
		"gamemode":           {Gamemode: ptr("1")},
		"max players 0":      {MaxPlayers: ptr(0)},
		"max players 501":    {MaxPlayers: ptr(501)},
		"view distance":      {ViewDistance: ptr(2)},
		"simulation":         {SimulationDistance: ptr(33)},
		"spawn protection":   {SpawnProtection: ptr(257)},
		"whitelist name":     {Whitelist: []string{"ab"}},
		"whitelist comma":    {Whitelist: []string{"a,b,c"}},
		"whitelist too many": {Whitelist: append(names200, "extra")},
		"ops too many":       {Ops: names200[:51]},
		"seed too long":      {Seed: ptr(strings.Repeat("1", 65))},
		"seed newline":       {Seed: ptr("1\n2")},
		"level type":         {LevelType: ptr("minecraft:flat")},
	}
	for name, p := range invalid {
		if err := p.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestResolveDefaults(t *testing.T) {
	r := instancespec.Resolve("survival", instancespec.Properties{MaxPlayers: ptr(8)})
	if *r.MOTD != "survival" || *r.Difficulty != "normal" || *r.Gamemode != "survival" || *r.MaxPlayers != 8 ||
		!*r.PVP || *r.Hardcore || *r.AllowFlight || *r.EnableCommandBlock || !*r.OnlineMode || *r.WhitelistEnabled ||
		r.Whitelist == nil || r.Ops == nil || *r.ViewDistance != 10 || *r.SimulationDistance != 10 ||
		*r.SpawnProtection != 16 || *r.Seed != "" || *r.LevelType != "normal" {
		t.Fatalf("resolved = %+v", r)
	}
}
```

`internal/agent/instancespec/env_test.go`:

```go
package instancespec_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// allowedKeys is every environment variable Env may ever produce. Anything else
// (JVM_OPTS, GENERIC_PACK, MODS, PLUGINS, ...) would let the panel smuggle in code.
var allowedKeys = []string{
	"EULA", "TYPE", "VERSION", "MEMORY", "ENABLE_AUTOPAUSE", "ENABLE_RCON", "OVERRIDE_SERVER_PROPERTIES",
	"FABRIC_LOADER_VERSION", "NEOFORGE_VERSION", "MODRINTH_MODPACK", "CF_SLUG", "CF_PAGE_URL", "CF_API_KEY",
	"MOTD", "DIFFICULTY", "MODE", "MAX_PLAYERS", "PVP", "HARDCORE", "ALLOW_FLIGHT", "ENABLE_COMMAND_BLOCK",
	"ONLINE_MODE", "ENABLE_WHITELIST", "WHITELIST", "OPS", "VIEW_DISTANCE", "SIMULATION_DISTANCE",
	"SPAWN_PROTECTION", "SEED", "LEVEL_TYPE",
}

func TestEnvPaper(t *testing.T) {
	s := validSpec()
	s.Properties = instancespec.Properties{MOTD: ptr("Hallo"), Whitelist: []string{"Notch", "jeb_"}, WhitelistEnabled: ptr(true)}
	env, err := instancespec.Env("survival", s)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true",
		"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
		"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=true",
		"WHITELIST=Notch,jeb_", "VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
		"LEVEL_TYPE=minecraft:normal",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("env =\n%q\nwant\n%q", env, want)
	}
}

func TestEnvPerType(t *testing.T) {
	cases := []struct {
		spec instancespec.Spec
		has  []string
		not  []string
	}{
		{
			spec: instancespec.Spec{Type: instancespec.TypeFabric, Version: "26.3", LoaderVersion: ptr("0.19.5"), Java: instancespec.Java25, MemoryMiB: 2048},
			has:  []string{"TYPE=FABRIC", "VERSION=26.3", "FABRIC_LOADER_VERSION=0.19.5", "MEMORY=2048M"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeNeoForge, Version: "1.21.4", LoaderVersion: ptr("21.4.140"), Java: instancespec.Java21, MemoryMiB: 6144},
			has:  []string{"TYPE=NEOFORGE", "NEOFORGE_VERSION=21.4.140"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeModrinth, Modpack: ptr("cobblemon-fabric"), Java: instancespec.Java21, MemoryMiB: 6144},
			has:  []string{"TYPE=MODRINTH", "MODRINTH_MODPACK=cobblemon-fabric"},
			not:  []string{"VERSION"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeCurseForge, Modpack: ptr("atm10"), CurseForgeAPIKey: ptr("$2a$10$k"), Java: instancespec.Java21, MemoryMiB: 8192},
			has:  []string{"TYPE=AUTO_CURSEFORGE", "CF_SLUG=atm10", "CF_API_KEY=$2a$10$k"},
			not:  []string{"VERSION", "CF_PAGE_URL"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeCurseForge, Modpack: ptr("https://www.curseforge.com/minecraft/modpacks/atm10"), CurseForgeAPIKey: ptr("k"), Java: instancespec.Java21, MemoryMiB: 8192},
			has:  []string{"CF_PAGE_URL=https://www.curseforge.com/minecraft/modpacks/atm10"},
			not:  []string{"CF_SLUG"},
		},
		{
			spec: instancespec.Spec{Type: instancespec.TypeVanilla, Version: "LATEST", Java: instancespec.Java25, MemoryMiB: 1024,
				Properties: instancespec.Properties{Seed: ptr("42"), LevelType: ptr("amplified"), Ops: []string{"Notch"}}},
			has: []string{"TYPE=VANILLA", "VERSION=LATEST", "SEED=42", "LEVEL_TYPE=minecraft:amplified", "OPS=Notch"},
			not: []string{"WHITELIST"},
		},
	}
	for _, c := range cases {
		env, err := instancespec.Env("srv", c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec.Type, err)
		}
		keys := map[string]bool{}
		for _, kv := range env {
			k, _, ok := strings.Cut(kv, "=")
			if !ok || !slices.Contains(allowedKeys, k) || keys[k] {
				t.Errorf("%s: unexpected or duplicate env entry %q", c.spec.Type, kv)
			}
			keys[k] = true
		}
		for _, h := range c.has {
			if !slices.Contains(env, h) {
				t.Errorf("%s: missing %q in %q", c.spec.Type, h, env)
			}
		}
		for _, n := range c.not {
			if keys[n] {
				t.Errorf("%s: must not set %s", c.spec.Type, n)
			}
		}
	}
}

func TestEnvRejectsInvalidSpec(t *testing.T) {
	s := validSpec()
	s.Properties.MOTD = ptr("x\nJVM_OPTS=-Dfoo")
	if _, err := instancespec.Env("survival", s); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if _, err := instancespec.Env("Bad Name", validSpec()); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("invalid name: want ErrInvalid, got %v", err)
	}
}
```

- [ ] **Step 3: Tests laufen lassen (rot)**

Run: `go test ./internal/agent/instancespec -count=1`
Expected: FAIL beim Kompilieren (Paket existiert nicht).

- [ ] **Step 4: Implementieren**

`internal/agent/instancespec/instancespec.go`:

```go
// Package instancespec holds the rules for Minecraft instances that panel and agent share:
// the spec types, their validation, the Java image derivation and the mapping to
// itzg/minecraft-server environment variables. The panel validates with it before storing;
// the agent validates every request again with the same code because it never trusts the panel.
package instancespec

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type Type string

const (
	TypeVanilla    Type = "vanilla"
	TypePaper      Type = "paper"
	TypePurpur     Type = "purpur"
	TypeFabric     Type = "fabric"
	TypeNeoForge   Type = "neoforge"
	TypeModrinth   Type = "modrinth"
	TypeCurseForge Type = "curseforge"
)

var Types = []Type{TypeVanilla, TypePaper, TypePurpur, TypeFabric, TypeNeoForge, TypeModrinth, TypeCurseForge}

// IsModpack reports whether the pack, not the version field, decides the Minecraft version.
func (t Type) IsModpack() bool { return t == TypeModrinth || t == TypeCurseForge }

// HasLoader reports whether the type takes an optional mod loader version.
func (t Type) HasLoader() bool { return t == TypeFabric || t == TypeNeoForge }

type Java string

const (
	Java8  Java = "java8"
	Java17 Java = "java17"
	Java21 Java = "java21"
	Java25 Java = "java25"
)

var Javas = []Java{Java8, Java17, Java21, Java25}

const (
	MinMemoryMiB  = 1024
	MaxMemoryMiB  = 32768
	MemoryStepMiB = 256
)

// ErrInvalid wraps every validation error; the message names the field.
var ErrInvalid = errors.New("invalid instance spec")

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)
	labelRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$`)
	slugRe    = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
	cfURLRe   = regexp.MustCompile(`^https://www\.curseforge\.com/minecraft/modpacks/([a-z0-9-]{1,64})$`)
	cfKeyRe   = regexp.MustCompile(`^[A-Za-z0-9$./_-]{1,128}$`)
)

const maxBaseDomainLen = 200

func invalid(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, field, fmt.Sprintf(format, args...))
}

type Spec struct {
	Type             Type       `json:"type"`
	Version          string     `json:"version"`
	LoaderVersion    *string    `json:"loader_version"`
	Modpack          *string    `json:"modpack"`
	Java             Java       `json:"java"`
	MemoryMiB        int        `json:"memory_mib"`
	Properties       Properties `json:"properties"`
	CurseForgeAPIKey *string    `json:"curseforge_api_key"`
}

func (s Spec) Validate() error {
	if !slices.Contains(Types, s.Type) {
		return invalid("type", "unknown type %q", s.Type)
	}
	if s.Type.IsModpack() {
		if s.Version != "" {
			return invalid("version", "must be empty for modpacks (the pack decides)")
		}
	} else if s.Version != "LATEST" && !versionRe.MatchString(s.Version) {
		return invalid("version", "must be LATEST or match %s", versionRe)
	}
	if s.LoaderVersion != nil {
		if !s.Type.HasLoader() {
			return invalid("loader_version", "only allowed for fabric and neoforge")
		}
		if !versionRe.MatchString(*s.LoaderVersion) {
			return invalid("loader_version", "must match %s", versionRe)
		}
	}
	if err := s.validateModpack(); err != nil {
		return err
	}
	if !slices.Contains(Javas, s.Java) {
		return invalid("java", "unknown java %q", s.Java)
	}
	if s.MemoryMiB < MinMemoryMiB || s.MemoryMiB > MaxMemoryMiB || s.MemoryMiB%MemoryStepMiB != 0 {
		return invalid("memory_mib", "must be %d..%d in steps of %d", MinMemoryMiB, MaxMemoryMiB, MemoryStepMiB)
	}
	if err := s.Properties.Validate(); err != nil {
		return err
	}
	if s.Type == TypeCurseForge {
		if s.CurseForgeAPIKey == nil || !cfKeyRe.MatchString(*s.CurseForgeAPIKey) {
			return invalid("curseforge_api_key", "required for curseforge and must match %s", cfKeyRe)
		}
		if s.Java == Java8 {
			return invalid("java", "curseforge needs java17 or newer")
		}
	} else if s.CurseForgeAPIKey != nil {
		return invalid("curseforge_api_key", "only allowed for curseforge")
	}
	return nil
}

func (s Spec) validateModpack() error {
	switch {
	case !s.Type.IsModpack():
		if s.Modpack != nil {
			return invalid("modpack", "only allowed for modrinth and curseforge")
		}
	case s.Modpack == nil:
		return invalid("modpack", "required for %s", s.Type)
	case s.Type == TypeModrinth && !slugRe.MatchString(*s.Modpack):
		return invalid("modpack", "must be a Modrinth project slug matching %s", slugRe)
	case s.Type == TypeCurseForge && !slugRe.MatchString(*s.Modpack) && !cfURLRe.MatchString(*s.Modpack):
		return invalid("modpack", "must be a CurseForge slug or https://www.curseforge.com/minecraft/modpacks/<slug>")
	}
	return nil
}

func ValidName(name string) bool { return nameRe.MatchString(name) }

// ValidBaseDomain accepts lowercase DNS names with at least two labels.
func ValidBaseDomain(d string) error {
	if d == "" || len(d) > maxBaseDomainLen {
		return invalid("base_domain", "must be 1..%d characters", maxBaseDomainLen)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return invalid("base_domain", "needs at least two labels, e.g. mc.example.com")
	}
	for _, l := range labels {
		if !labelRe.MatchString(l) {
			return invalid("base_domain", "label %q must match %s", l, labelRe)
		}
	}
	return nil
}

func FQDN(name, baseDomain string) (string, error) {
	if !ValidName(name) {
		return "", invalid("name", "must match %s", nameRe)
	}
	if err := ValidBaseDomain(baseDomain); err != nil {
		return "", err
	}
	return name + "." + baseDomain, nil
}

// CheckFQDN verifies that fqdn is exactly "<name>.<valid base domain>".
func CheckFQDN(name, fqdn string) error {
	if !ValidName(name) {
		return invalid("name", "must match %s", nameRe)
	}
	base, ok := strings.CutPrefix(fqdn, name+".")
	if !ok {
		return invalid("fqdn", "must start with %q", name+".")
	}
	if err := ValidBaseDomain(base); err != nil {
		return invalid("fqdn", "%v", err)
	}
	return nil
}
```

`internal/agent/instancespec/properties.go`:

```go
package instancespec

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var playerRe = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// Properties are the allowlisted server.properties (spec §7). nil/empty = default.
type Properties struct {
	MOTD               *string  `json:"motd,omitempty"`
	Difficulty         *string  `json:"difficulty,omitempty"`
	Gamemode           *string  `json:"gamemode,omitempty"`
	MaxPlayers         *int     `json:"max_players,omitempty"`
	PVP                *bool    `json:"pvp,omitempty"`
	Hardcore           *bool    `json:"hardcore,omitempty"`
	AllowFlight        *bool    `json:"allow_flight,omitempty"`
	EnableCommandBlock *bool    `json:"enable_command_block,omitempty"`
	OnlineMode         *bool    `json:"online_mode,omitempty"`
	WhitelistEnabled   *bool    `json:"whitelist_enabled,omitempty"`
	Whitelist          []string `json:"whitelist,omitempty"`
	Ops                []string `json:"ops,omitempty"`
	ViewDistance       *int     `json:"view_distance,omitempty"`
	SimulationDistance *int     `json:"simulation_distance,omitempty"`
	SpawnProtection    *int     `json:"spawn_protection,omitempty"`
	Seed               *string  `json:"seed,omitempty"`
	LevelType          *string  `json:"level_type,omitempty"`
}

var (
	difficulties = []string{"peaceful", "easy", "normal", "hard"}
	gamemodes    = []string{"survival", "creative", "adventure", "spectator"}
	levelTypes   = []string{"normal", "flat", "large_biomes", "amplified"}
)

const (
	maxMOTDRunes   = 150
	maxSeedRunes   = 64
	maxWhitelisted = 200
	maxOps         = 50
)

func (p Properties) Validate() error {
	if p.MOTD != nil {
		if err := printable("motd", *p.MOTD, maxMOTDRunes); err != nil {
			return err
		}
	}
	if err := oneOf("difficulty", p.Difficulty, difficulties); err != nil {
		return err
	}
	if err := oneOf("gamemode", p.Gamemode, gamemodes); err != nil {
		return err
	}
	if err := oneOf("level_type", p.LevelType, levelTypes); err != nil {
		return err
	}
	for _, r := range []struct {
		field    string
		v        *int
		min, max int
	}{
		{"max_players", p.MaxPlayers, 1, 500},
		{"view_distance", p.ViewDistance, 3, 32},
		{"simulation_distance", p.SimulationDistance, 3, 32},
		{"spawn_protection", p.SpawnProtection, 0, 256},
	} {
		if r.v != nil && (*r.v < r.min || *r.v > r.max) {
			return invalid(r.field, "must be %d..%d", r.min, r.max)
		}
	}
	if err := players("whitelist", p.Whitelist, maxWhitelisted); err != nil {
		return err
	}
	if err := players("ops", p.Ops, maxOps); err != nil {
		return err
	}
	if p.Seed != nil {
		if err := printable("seed", *p.Seed, maxSeedRunes); err != nil {
			return err
		}
	}
	return nil
}

// printable allows any printable Unicode (including § color codes) but no control
// characters, so a value can never break out of its environment variable.
func printable(field, s string, maxRunes int) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxRunes {
		return invalid(field, "must be valid UTF-8 with at most %d characters", maxRunes)
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return invalid(field, "must not contain control characters")
		}
	}
	return nil
}

func oneOf(field string, v *string, allowed []string) error {
	if v != nil && !slices.Contains(allowed, *v) {
		return invalid(field, "must be one of %s", strings.Join(allowed, ", "))
	}
	return nil
}

func players(field string, names []string, maxN int) error {
	if len(names) > maxN {
		return invalid(field, "at most %d names", maxN)
	}
	for _, n := range names {
		if !playerRe.MatchString(n) {
			return invalid(field, "name %q must match %s", n, playerRe)
		}
	}
	return nil
}

// Resolve returns p with every unset field replaced by its default (spec §7).
func Resolve(name string, p Properties) Properties {
	str := func(v *string, d string) *string {
		if v != nil {
			return v
		}
		return &d
	}
	num := func(v *int, d int) *int {
		if v != nil {
			return v
		}
		return &d
	}
	flag := func(v *bool, d bool) *bool {
		if v != nil {
			return v
		}
		return &d
	}
	list := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return Properties{
		MOTD:               str(p.MOTD, name),
		Difficulty:         str(p.Difficulty, "normal"),
		Gamemode:           str(p.Gamemode, "survival"),
		MaxPlayers:         num(p.MaxPlayers, 20),
		PVP:                flag(p.PVP, true),
		Hardcore:           flag(p.Hardcore, false),
		AllowFlight:        flag(p.AllowFlight, false),
		EnableCommandBlock: flag(p.EnableCommandBlock, false),
		OnlineMode:         flag(p.OnlineMode, true),
		WhitelistEnabled:   flag(p.WhitelistEnabled, false),
		Whitelist:          list(p.Whitelist),
		Ops:                list(p.Ops),
		ViewDistance:       num(p.ViewDistance, 10),
		SimulationDistance: num(p.SimulationDistance, 10),
		SpawnProtection:    num(p.SpawnProtection, 16),
		Seed:               str(p.Seed, ""),
		LevelType:          str(p.LevelType, "normal"),
	}
}
```

`internal/agent/instancespec/java.go`:

```go
package instancespec

import (
	"strconv"
	"strings"
)

// JavaFor derives the itzg image variant from the Minecraft version (spec §6.1). Modpacks
// default to java21; unknown formats (snapshots, LATEST) get the newest image.
func JavaFor(t Type, version string) Java {
	if t.IsModpack() {
		return Java21
	}
	parts := strings.Split(version, ".")
	major, ok := leadingInt(parts[0])
	if !ok || major >= 26 || major != 1 || len(parts) < 2 {
		return Java25
	}
	minor, ok := leadingInt(parts[1])
	if !ok {
		return Java25
	}
	patch := 0
	if len(parts) >= 3 {
		patch, _ = leadingInt(parts[2])
	}
	switch {
	case minor <= 16:
		return Java8
	case minor <= 19, minor == 20 && patch <= 4:
		return Java17
	case minor <= 21:
		return Java21
	default:
		return Java25
	}
}

// leadingInt parses the leading decimal digits of s ("4-rc1" -> 4).
func leadingInt(s string) (int, bool) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:end])
	return n, err == nil
}
```

Prüfen: `26.4-snapshot-3` → major 26 → java25 ✓; `1.20.4-rc1` → patch 4 → java17 ✓; `LATEST` → kein Integer → java25 ✓.

`internal/agent/instancespec/env.go`:

```go
package instancespec

import (
	"strconv"
	"strings"
)

var itzgType = map[Type]string{
	TypeVanilla: "VANILLA", TypePaper: "PAPER", TypePurpur: "PURPUR", TypeFabric: "FABRIC",
	TypeNeoForge: "NEOFORGE", TypeModrinth: "MODRINTH", TypeCurseForge: "AUTO_CURSEFORGE",
}

// Env validates the spec and returns the complete itzg environment in a fixed order.
// Nothing outside this function decides which variables a container gets.
func Env(name string, s Spec) ([]string, error) {
	if !ValidName(name) {
		return nil, invalid("name", "must match %s", nameRe)
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	env := []string{"EULA=TRUE", "TYPE=" + itzgType[s.Type]}
	if !s.Type.IsModpack() {
		env = append(env, "VERSION="+s.Version)
	}
	env = append(env,
		"MEMORY="+strconv.Itoa(s.MemoryMiB)+"M",
		"ENABLE_AUTOPAUSE=false",
		"ENABLE_RCON=true",
		"OVERRIDE_SERVER_PROPERTIES=true",
	)
	switch s.Type {
	case TypeFabric:
		if s.LoaderVersion != nil {
			env = append(env, "FABRIC_LOADER_VERSION="+*s.LoaderVersion)
		}
	case TypeNeoForge:
		if s.LoaderVersion != nil {
			env = append(env, "NEOFORGE_VERSION="+*s.LoaderVersion)
		}
	case TypeModrinth:
		env = append(env, "MODRINTH_MODPACK="+*s.Modpack)
	case TypeCurseForge:
		if strings.HasPrefix(*s.Modpack, "https://") {
			env = append(env, "CF_PAGE_URL="+*s.Modpack)
		} else {
			env = append(env, "CF_SLUG="+*s.Modpack)
		}
		env = append(env, "CF_API_KEY="+*s.CurseForgeAPIKey)
	}
	p := Resolve(name, s.Properties)
	b := strconv.FormatBool
	env = append(env,
		"MOTD="+*p.MOTD,
		"DIFFICULTY="+*p.Difficulty,
		"MODE="+*p.Gamemode,
		"MAX_PLAYERS="+strconv.Itoa(*p.MaxPlayers),
		"PVP="+b(*p.PVP),
		"HARDCORE="+b(*p.Hardcore),
		"ALLOW_FLIGHT="+b(*p.AllowFlight),
		"ENABLE_COMMAND_BLOCK="+b(*p.EnableCommandBlock),
		"ONLINE_MODE="+b(*p.OnlineMode),
		"ENABLE_WHITELIST="+b(*p.WhitelistEnabled),
	)
	// Empty lists are omitted: itzg clears a property for an empty value but leaves it
	// alone when the variable is unset.
	if len(p.Whitelist) > 0 {
		env = append(env, "WHITELIST="+strings.Join(p.Whitelist, ","))
	}
	if len(p.Ops) > 0 {
		env = append(env, "OPS="+strings.Join(p.Ops, ","))
	}
	env = append(env,
		"VIEW_DISTANCE="+strconv.Itoa(*p.ViewDistance),
		"SIMULATION_DISTANCE="+strconv.Itoa(*p.SimulationDistance),
		"SPAWN_PROTECTION="+strconv.Itoa(*p.SpawnProtection),
	)
	if *p.Seed != "" {
		env = append(env, "SEED="+*p.Seed)
	}
	env = append(env, "LEVEL_TYPE=minecraft:"+*p.LevelType)
	return env, nil
}
```

- [ ] **Step 5: Tests laufen lassen (grün)**

Run: `go test ./internal/agent/instancespec -count=1 -v`
Expected: PASS für alle vier Testdateien.

- [ ] **Step 6: Lint**

Run: `golangci-lint run ./internal/agent/instancespec/...`
Expected: keine Befunde.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/instancespec
git commit -m "feat(instancespec): shared validation, java derivation and itzg environment

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: agentapi-Vertrag und Client für Instanzen

**Files:**
- Create: `internal/agent/agentapi/instances.go`
- Modify: `internal/agent/agentapi/agentapi.go` (Felder `HealthResponse.Docker`, `ErrorResponse.Code`)
- Modify: `internal/agent/client.go` (Instanz-Methoden, `StatusError.Code`, Fehlerabbildung)
- Modify: `internal/agent/server.go:94` (Health setzt vorläufig `Docker: agentapi.DockerUnavailable`; Aufgabe 9 ersetzt das)
- Test: `internal/agent/agentapi/instances_test.go`, `internal/agent/client_instances_test.go`; anpassen: `internal/agent/server_test.go:42`, `internal/agent/client_test.go:59` (Health-Vergleich mit neuem Feld), `internal/agent/client_test.go` `TestStatusErrorMapping`

**Interfaces:**
- Consumes: V2 (`instancespec`)
- Produces: V3

- [ ] **Step 1: Failing tests schreiben**

`internal/agent/agentapi/instances_test.go`:

```go
package agentapi_test

import (
	"errors"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func goodEnsure() agentapi.EnsureRequest {
	return agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	}
}

func TestEnsureRequestValidate(t *testing.T) {
	if err := goodEnsure().Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*agentapi.EnsureRequest){
		"id":       func(r *agentapi.EnsureRequest) { r.ID = 0 },
		"name":     func(r *agentapi.EnsureRequest) { r.Name = "Bad" },
		"fqdn":     func(r *agentapi.EnsureRequest) { r.FQDN = "other.mc.example.com" },
		"revision": func(r *agentapi.EnsureRequest) { r.Revision = 0 },
		"spec":     func(r *agentapi.EnsureRequest) { r.Spec.MemoryMiB = 1 },
	}
	for name, mod := range bad {
		r := goodEnsure()
		mod(&r)
		if err := r.Validate(); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestPowerActions(t *testing.T) {
	for _, a := range []agentapi.PowerAction{agentapi.PowerStart, agentapi.PowerStop, agentapi.PowerRestart} {
		if !agentapi.ValidPowerAction(a) {
			t.Errorf("%s must be valid", a)
		}
	}
	if agentapi.ValidPowerAction("kill") {
		t.Error("kill must be invalid")
	}
}
```

`internal/agent/client_instances_test.go` – benutzt einen eigenen `httptest`-Server auf einem Unix-Socket (wie `rawServer` in `client_test.go`, aber mit `http.Handler`):

```go
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// handlerServer serves h on a fresh Unix socket and returns the socket path.
func handlerServer(t *testing.T, h http.Handler) string {
	t.Helper()
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return path
}

func TestClientInstanceCalls(t *testing.T) {
	var got []string
	var lastBody []byte
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		lastBody, _ = io.ReadAll(r.Body)
		if r.URL.Path == agentapi.PathInstances {
			_ = json.NewEncoder(w).Encode(agentapi.InstancesResponse{
				Docker:    agentapi.DockerOK,
				Instances: []agentapi.InstanceStatus{{ID: 1, Name: "survival", Revision: 2, State: agentapi.StateRunning}},
				Router:    agentapi.RouterStatus{State: agentapi.StateRunning},
			})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	c := agent.NewClient(path)
	ctx := context.Background()

	list, err := c.Instances(ctx)
	if err != nil || list.Docker != "ok" || len(list.Instances) != 1 || list.Instances[0].Revision != 2 {
		t.Fatalf("Instances = %+v, %v", list, err)
	}
	req := agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 2, Recreate: true,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	}
	if err := c.Ensure(ctx, req); err != nil {
		t.Fatal(err)
	}
	var sent agentapi.EnsureRequest
	if err := json.Unmarshal(lastBody, &sent); err != nil || sent.Revision != 2 || !sent.Recreate || sent.Spec.MemoryMiB != 4096 {
		t.Fatalf("sent %s (%v)", lastBody, err)
	}
	if err := c.Power(ctx, 1, agentapi.PowerStop); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, 1); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/instances", "POST /v1/instances/ensure", "POST /v1/instances/power", "POST /v1/instances/remove"}
	if len(got) != len(want) {
		t.Fatalf("calls = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}

func TestClientInstanceCallsValidateBeforeSending(t *testing.T) {
	calls := 0
	path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusAccepted) }))
	c := agent.NewClient(path)
	ctx := context.Background()
	if err := c.Ensure(ctx, agentapi.EnsureRequest{ID: 1, Name: "survival"}); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("Ensure invalid: %v", err)
	}
	if err := c.Power(ctx, 1, "kill"); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Power invalid: %v", err)
	}
	if err := c.Remove(ctx, 0); !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("Remove invalid: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached the agent (%d calls)", calls)
	}
}

func TestClientInstanceErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusServiceUnavailable, `{"error":"docker unavailable","code":"docker_unavailable"}`, agentapi.ErrDockerUnavailable},
		{http.StatusTooManyRequests, `{"error":"queue full","code":"busy"}`, agentapi.ErrBusy},
		{http.StatusBadRequest, `{"error":"bad"}`, agent.ErrInvalid},
	} {
		path := handlerServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		err := agent.NewClient(path).Power(context.Background(), 1, agentapi.PowerStart)
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: got %v, want %v", tc.status, err, tc.want)
		}
	}
}
```

In `internal/agent/client_test.go` `TestStatusErrorMapping` um zwei Fälle erweitern (die Struktur bekommt Felder `body string` nicht nötig – stattdessen eigene Prüfungen unterhalb der Schleife):

```go
	busy := error(&agent.StatusError{StatusCode: 429, Message: "queue full", Code: agentapi.ErrorCodeBusy})
	if !errors.Is(busy, agentapi.ErrBusy) || errors.Is(busy, agent.ErrInvalid) {
		t.Errorf("429 busy must map to ErrBusy only")
	}
	docker := error(&agent.StatusError{StatusCode: 503, Message: "docker unavailable", Code: agentapi.ErrorCodeDockerUnavailable})
	if !errors.Is(docker, agentapi.ErrDockerUnavailable) || !errors.Is(docker, agentapi.ErrUnavailable) {
		t.Errorf("503 docker_unavailable must map to ErrDockerUnavailable and ErrUnavailable")
	}
	if errors.Is(&agent.StatusError{StatusCode: 503, Message: "x"}, agentapi.ErrDockerUnavailable) {
		t.Errorf("503 without code must not be ErrDockerUnavailable")
	}
```

Die Health-Vergleiche in `server_test.go:42` und `client_test.go:59` bekommen `Docker: "unavailable"` (Server ohne Docker-Backend, bis Aufgabe 9).

- [ ] **Step 2: Tests laufen lassen (rot)**

Run: `go test ./internal/agent/... -count=1`
Expected: FAIL beim Kompilieren (`agentapi.EnsureRequest` undefiniert).

- [ ] **Step 3: Implementieren**

`internal/agent/agentapi/agentapi.go`: `HealthResponse` und `ErrorResponse` erweitern:

```go
type HealthResponse struct {
	Status   string `json:"status"` // "ok"
	Version  string `json:"version"`
	Firewall string `json:"firewall"` // "nftables" | "noop"
	Docker   string `json:"docker"`   // DockerOK | DockerUnavailable
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"` // ErrorCodeBusy | ErrorCodeDockerUnavailable
}
```

`internal/agent/agentapi/instances.go`:

```go
package agentapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	PathInstances      = "/v1/instances"
	PathInstanceEnsure = "/v1/instances/ensure"
	PathInstancePower  = "/v1/instances/power"
	PathInstanceRemove = "/v1/instances/remove"

	// MaxInstanceRequestBytes is the body limit of the /v1/instances/* endpoints
	// (MOTD, whitelist and ops need more than the 4 KiB of the firewall endpoints).
	MaxInstanceRequestBytes = 16 << 10

	ErrorCodeBusy              = "busy"
	ErrorCodeDockerUnavailable = "docker_unavailable"

	DockerOK          = "ok"
	DockerUnavailable = "unavailable"
)

var (
	// ErrBusy: the instance queue is full; retry later.
	ErrBusy = errors.New("agent busy")
	// ErrDockerUnavailable: the agent runs but cannot reach Docker.
	ErrDockerUnavailable = errors.New("docker unavailable")
)

type PowerAction string

const (
	PowerStart   PowerAction = "start"
	PowerStop    PowerAction = "stop"
	PowerRestart PowerAction = "restart"
)

func ValidPowerAction(a PowerAction) bool {
	return a == PowerStart || a == PowerStop || a == PowerRestart
}

type ContainerState string

const (
	StateCreated    ContainerState = "created"
	StateRunning    ContainerState = "running"
	StateRestarting ContainerState = "restarting"
	StateExited     ContainerState = "exited"
	StateMissing    ContainerState = "missing"
)

type Operation string

const (
	OpNone     Operation = ""
	OpPulling  Operation = "pulling"
	OpCreating Operation = "creating"
	OpStarting Operation = "starting"
	OpStopping Operation = "stopping"
	OpRemoving Operation = "removing"
)

type EnsureRequest struct {
	ID       int64             `json:"id"`
	Name     string            `json:"name"`
	FQDN     string            `json:"fqdn"`
	Revision int64             `json:"revision"`
	Spec     instancespec.Spec `json:"spec"`
	Recreate bool              `json:"recreate"`
}

func (r EnsureRequest) Validate() error {
	if r.ID <= 0 {
		return fmt.Errorf("%w: id: must be > 0", instancespec.ErrInvalid)
	}
	if !instancespec.ValidName(r.Name) {
		return fmt.Errorf("%w: name: invalid", instancespec.ErrInvalid)
	}
	if err := instancespec.CheckFQDN(r.Name, r.FQDN); err != nil {
		return err
	}
	if r.Revision <= 0 {
		return fmt.Errorf("%w: revision: must be > 0", instancespec.ErrInvalid)
	}
	return r.Spec.Validate()
}

type PowerRequest struct {
	ID     int64       `json:"id"`
	Action PowerAction `json:"action"`
}

type RemoveRequest struct {
	ID int64 `json:"id"`
}

type InstanceStatus struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Revision         int64          `json:"revision"`
	State            ContainerState `json:"state"`
	ExitCode         int            `json:"exit_code"`
	StartedAt        int64          `json:"started_at"` // Unix ms, 0 = not running
	MemoryBytes      int64          `json:"memory_bytes"`
	MemoryLimitBytes int64          `json:"memory_limit_bytes"`
	CPUPercent       float64        `json:"cpu_percent"`
	Operation        Operation      `json:"operation"`
	LastError        string         `json:"last_error"`
}

type RouterStatus struct {
	State     ContainerState `json:"state"`
	LastError string         `json:"last_error"`
}

type InstancesResponse struct {
	Docker    string           `json:"docker"`
	Instances []InstanceStatus `json:"instances"`
	Router    RouterStatus     `json:"router"`
}

// InstancesClient is the panel-side view of the instance endpoints. It is separate from
// Client so that firewall-only fakes keep compiling.
type InstancesClient interface {
	Instances(ctx context.Context) (InstancesResponse, error)
	Ensure(ctx context.Context, req EnsureRequest) error
	Power(ctx context.Context, id int64, action PowerAction) error
	Remove(ctx context.Context, id int64) error
}
```

`internal/agent/client.go` – `StatusError` um `Code` erweitern, `Is` anpassen, `do` liest `Code` mit, neue Methoden:

```go
type StatusError struct {
	StatusCode int
	Message    string
	Code       string // agentapi.ErrorCode*, may be empty
}

// Is maps status codes to the sentinel errors: 409 -> ErrConflict, 429 -> agentapi.ErrBusy,
// other 4xx -> ErrInvalid, everything else non-2xx -> agentapi.ErrUnavailable; a 503 with
// code docker_unavailable additionally matches agentapi.ErrDockerUnavailable.
func (e *StatusError) Is(target error) bool {
	switch {
	case target == agentapi.ErrDockerUnavailable:
		return e.StatusCode == http.StatusServiceUnavailable && e.Code == agentapi.ErrorCodeDockerUnavailable
	case e.StatusCode == http.StatusConflict:
		return target == banrules.ErrConflict
	case e.StatusCode == http.StatusTooManyRequests:
		return target == agentapi.ErrBusy
	case e.StatusCode >= 400 && e.StatusCode < 500:
		return target == banrules.ErrInvalid
	default:
		// 1xx, 3xx, 5xx and anything else the agent never sends: treat as
		// a transient/unknown condition, never as a permanent rejection.
		return target == agentapi.ErrUnavailable
	}
}

var _ agentapi.InstancesClient = (*Client)(nil)

func (c *Client) Instances(ctx context.Context) (agentapi.InstancesResponse, error) {
	var out agentapi.InstancesResponse
	if err := c.do(ctx, http.MethodGet, agentapi.PathInstances, nil, &out); err != nil {
		return agentapi.InstancesResponse{}, err
	}
	return out, nil
}

func (c *Client) Ensure(ctx context.Context, req agentapi.EnsureRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, agentapi.PathInstanceEnsure, req, nil)
}

func (c *Client) Power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	if id <= 0 || !agentapi.ValidPowerAction(action) {
		return fmt.Errorf("%w: power: id must be > 0 and action start, stop or restart", ErrInvalid)
	}
	return c.do(ctx, http.MethodPost, agentapi.PathInstancePower, agentapi.PowerRequest{ID: id, Action: action}, nil)
}

func (c *Client) Remove(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: remove: id must be > 0", ErrInvalid)
	}
	return c.do(ctx, http.MethodPost, agentapi.PathInstanceRemove, agentapi.RemoveRequest{ID: id}, nil)
}
```

In `do` beim Fehlerfall den Code übernehmen:

```go
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := http.StatusText(resp.StatusCode)
		var er agentapi.ErrorResponse
		if json.Unmarshal(data, &er) == nil && er.Error != "" {
			msg = er.Error
		}
		return &StatusError{StatusCode: resp.StatusCode, Message: msg, Code: er.Code}
	}
```

`errors.Is(err, instancespec.ErrInvalid)` für `Ensure` funktioniert, weil `req.Validate()` direkt den `instancespec`-Fehler zurückgibt. `agent.ErrInvalid` (= `banrules.ErrInvalid`) bleibt für Power/Remove und für 4xx-Antworten.

`internal/agent/server.go` `health`: `Docker: agentapi.DockerUnavailable` ergänzen (Aufgabe 9 setzt den echten Wert).

- [ ] **Step 4: Tests laufen lassen (grün)**

Run: `go test ./internal/agent/... ./internal/defense/... ./cmd/... -count=1`
Expected: PASS (die Fakes in `defense` und `cmd/panel` sind unberührt).

- [ ] **Step 5: Lint**

Run: `make lint`
Expected: keine Befunde.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/agentapi internal/agent/client.go internal/agent/client_test.go internal/agent/client_instances_test.go internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agentapi): instance wire contract and client methods

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
