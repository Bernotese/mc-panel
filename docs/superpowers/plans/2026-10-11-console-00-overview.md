# mc-panel Teilprojekt 3a: Konsole, Logs, Spieler – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live-Log, Konsolenbefehle (RCON) und Spieler-Verwaltung für jeden Minecraft-Server im panel – ohne dass das panel Docker-Socket oder Datenverzeichnisse der Server berührt.

**Architecture:** Der agent liest das Container-Log per Docker-API (`follow`) und streamt es als NDJSON über den Unix-Socket; Befehle führt er als `docker exec rcon-cli <befehl>` aus; Spielerlisten liest er per `os.Root` aus dem Datenverzeichnis. Das panel (neues Paket `internal/console`) reicht das Log per Server-Sent Events an den Browser, nimmt Befehle und Spieler-Aktionen als normale `POST`-Requests entgegen (CSRF, Rechte, Rate-Limit, Audit über die bestehende Kette) und prüft offene Streams alle 30 s erneut. Ops und Whitelist verlassen dafür die panel-Eigenschaften aus TP2; maßgeblich sind die Dateien des Servers.

**Tech Stack:** Go (Stand `go.mod`, ≥ 1.24 für `os.Root`), Docker Engine API v1.44 über den vorhandenen schmalen Client, `modernc.org/sqlite` + goose + sqlc, chi; React 19 + TanStack Router/Query, shadcn/ui, react-i18next, MSW 2 (SSE als `http.get` mit `ReadableStream`), Vitest, Playwright; neue Frontend-Abhängigkeit nur `@tanstack/react-virtual`. Keine neuen Go-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-11-console-00-overview.md` (diese) | – | Ausführung, Constraints, Review Focus, Dateistruktur, Verträge V1–V4 |
| `2026-10-11-console-01-contracts.md` | 1–3 | Ops/Whitelist raus aus den Eigenschaften (Migration, `instancespec`, agent, panel, Frontend-Formular); agentapi-Vertrag + Client; panel-Vertrag (OpenAPI, Fehlercodes, Audit, Stub-Handler, MSW-Mocks) |
| `2026-10-11-console-02-agent.md` | 4–8 | agent: Docker-Demultiplexer, Logs, Exec; Manager (Logs, RCON, Spielerlisten); Endpunkte mit Streaming und Limits; Integrationstest |
| `2026-10-11-console-03-panel.md` | 9–13 | panel: Paket `internal/console` – Befehle, Spieler (Online, Listen, Aktionen), SSE-Stream mit Neuverbindung und erneuter Prüfung, Verdrahtung |
| `2026-10-11-console-04-frontend.md` | 14–18 | Frontend: Log-Parser und Puffer, Konsole-Tab, Spieler-Tab, Online-Karte, Playwright |
| `2026-10-11-console-05-ci-docs.md` | 19–21 | CI-Abnahme (`scripts/ci-instances.sh`), Doku, Checkliste `docs/dev/acceptance-tp3a.md`, Gesamt-Review |

## Ausführung (Agent-Teams, CI statt lokaler Tests)

Es gelten die Arbeitsregeln aus `CLAUDE.md`. Daraus folgt für alle Teilpläne:

- **Lokal nur `make check`.** Schritte der Form „Run: `go test …`“, `make test`, `make lint`, `npx vitest …`, Playwright oder Docker/root-Tests werden **nicht** auf dem Mac ausgeführt. Stattdessen: `make check` lokal, dann Commit, Push auf den PR-Branch und `gh pr checks <pr> --watch`; „Expected: PASS“ wird in den CI-Logs geprüft (`gh run view <id> --log-failed`). `make generate` ist erlaubt.
- **TDD bleibt:** Test zuerst, dann Implementierung; „rot“ zeigt lokal `make check` (Kompilierfehler) bzw. die CI, „grün“ die CI. Eine Aufgabe ist erst fertig, wenn die CI ihres Commits grün ist.
- **Nichts Systemnahes auf dem Mac:** keine Container, kein `MCPANEL_DEV_DOCKER=1`, kein `sudo`, kein `npm -g`. `npm install @tanstack/react-virtual` im Projekt (`web/`) ist erlaubt (Projektabhängigkeit, kein globales Paket).
- **Git:** nie auf `main`; ein Branch und Draft-PR pro Phase; jeder Teammate in eigenem Worktree (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`). Mergen nur durch den Nutzer.

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/console-contracts` | Lead | 1–3 | – |
| B1 | `feat/console-agent` | Teammate „agent“ | 4–8 | A gemerged |
| B2 | `feat/console-panel` | Teammate „panel“ | 9–13 | A gemerged |
| B3 | `feat/console-frontend` | Teammate „frontend“ | 14–18 | A gemerged |
| C | `feat/console-ci-docs` | Lead | 19–21 | B1, B2, B3 gemerged |

B1, B2 und B3 laufen parallel und berühren disjunkte Dateien:

- **B1 (agent):** nur `internal/agent/docker/**`, `internal/agent/instances/**`, `internal/agent/server.go`, `internal/agent/server_*_test.go`, `internal/agent/helpers_test.go`, `cmd/agent/**`, `internal/config/**` (falls nötig), `.golangci.yml` (nur eigene, gezielte Regeln).
- **B2 (panel):** nur `internal/console/**`, `internal/auth/**` (nur additive Lese-Methoden für die erneute Prüfung), `cmd/panel/**`.
- **B3 (frontend):** nur `web/src/features/servers/**`, `web/src/features/console/**` (neu), `web/src/features/players/**` (neu), `web/src/i18n/**` bzw. die Locale-Dateien, `web/e2e/**`, `web/package.json`, `web/package-lock.json`.
- **Nur Phase A** ändert `internal/agent/agentapi/**`, `internal/agent/client.go`, `internal/agent/instancespec/**`, `internal/store/**`, `internal/httpx/errors.go`, `internal/audit/types.go`, `api/openapi.yaml`, `web/src/api/**`, `web/src/mocks/**`. Braucht ein Teammate dort eine Änderung, fragt er den Lead (der sie auf `main` bringt, danach `git rebase origin/main`).

Der Spawn-Prompt jedes Teammates nennt die Plan-Datei, die Aufgabennummern, den Branch, diese Übersicht und diesen Abschnitt.

## Global Constraints

- Alle Global Constraints aus TP1 und TP2 gelten weiter (`git show 010be18^:docs/superpowers/plans/2026-10-10-instances-00-overview.md`, Abschnitt „Global Constraints“) sowie alle Konventionen aus `CLAUDE.md`: `clock.Clock` statt `time.Now()`, Kontext überall, Go-Tests nur mit Standardbibliothek, keine neuen Go-Abhängigkeiten, Lint-Ausnahmen nur gezielt mit Begründung, Conventional Commits mit Abschluss `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Rechte: `server.view` → `GET /servers/{id}/players`; `server.console` → Stream, Befehle, Spielerlisten, Spieler-Aktionen. Keine neue Berechtigung.
- Befehl: 1–1000 Bytes, gültiges UTF-8, kein Zeichen `< 0x20`, kein `0x7f`, nicht nur Leerzeichen (`agentapi.ValidateCommand`; panel entfernt vorher genau ein führendes `/`).
- Spielername `^[A-Za-z0-9_]{3,16}$`; Grund ≤ 200 Zeichen (Runen), druckbar, ohne Steuerzeichen, nur bei `kick`/`ban`.
- agent: Log-Stream `tail` 0–1000, `since` ≥ 0 (ns), Zeile ≤ 8 KiB, Frist pro Schreibvorgang 30 s, höchstens 16 Streams; RCON Timeout 10 s, Ausgabe ≤ 64 KiB, höchstens 4 parallel pro Instanz, exec als `mcpanel-mc`-UID mit `WorkingDir /data`, `Env ["HOME=/data"]`, `Cmd ["rcon-cli", command]`; Spielerdateien per `os.Root`, `O_RDONLY|O_NOFOLLOW|O_NONBLOCK`, nur reguläre Dateien ≤ 1 MiB, Strings ≤ 256 Zeichen, Listen ≤ 1000 Einträge.
- panel: SSE-Heartbeat 15 s, Neuverbindung zum agent alle 3 s, erneute Prüfung alle 30 s, Default `tail` 500, höchstens 4 Streams pro Benutzer; Befehls-/Aktions-Limit 30/min pro Benutzer, Burst 10; Online-Spieler-Cache 10 s.
- Neue panel-Fehlercodes: `instance_not_running` (409), `rcon_unavailable` (503). Neue Audit-Aktionen: `server.console_command`, `server.player_action`.
- Neue agent-Fehlercodes (`agentapi.ErrorResponse.Code`): `not_found` (404), `not_running` (409), `rcon_unavailable` (503); weiter `busy` (429), `docker_unavailable` (503).
- Container-Umgebung: kein `OPS`, `WHITELIST`, `EXISTING_OPS_FILE`, `EXISTING_WHITELIST_FILE` mehr; `ENABLE_WHITELIST` bleibt.
- Frontend: Texte `de` (Standard) und `en`, keine Inline-Skripte, keine CDNs, kein `dangerouslySetInnerHTML`, MSW-Mock für jede OpenAPI-Operation (`web/src/mocks/handlers.test.ts` zählt exakt).

## Review Focus

1. **Spielerdateien als Angriffsfläche des root-agents:** Ein kompromittierter Minecraft-Server ersetzt `ops.json` durch einen Symlink auf `/etc/shadow`, eine FIFO, ein Verzeichnis oder eine 2-GiB-Datei bzw. biegt `/data` über einen Symlink im Pfad um; erwartet: der agent liest nichts außerhalb des Datenverzeichnisses, hängt nicht und meldet die Datei in `errors`. → Tests in Aufgabe 6.
2. **Befehls-Injektion über Spieler-Aktionen:** `name` mit Leerzeichen/`;`/Zeilenumbruch oder `reason` mit `\n`/`\r`/`\x00` darf keinen zweiten Befehl erzeugen; erwartet: `400 validation_failed`, kein agent-Aufruf, kein Audit. → Tests in Aufgabe 11 (panel) und Aufgabe 2 (`ValidateCommand`).
3. **Hängende oder endlose Streams:** Der Browser verschwindet ohne sauberes Schließen, der agent-Stream hängt, der Container wird neu angelegt, der Nutzer verliert das Recht oder wird abgemeldet; erwartet: panel und agent geben Goroutinen und Docker-Verbindungen frei (Kontext-Abbruch), Limits zählen korrekt herunter, der Stream endet spätestens nach 30 s. → Tests in Aufgabe 7 (agent: Abbruch gibt Slot frei) und Aufgabe 12 (panel: Fake-Clock, widerrufene Session, entzogenes Recht, gelöschter Server).
4. **Doppelte oder verlorene Zeilen bei Neuverbindung:** Neustart des Containers, Wiederverbinden mit `Last-Event-ID`, mehrere Zeilen mit identischem Zeitstempel; erwartet: keine doppelten Zeilen, `since` = letzter Zeitstempel + 1 ns, ungültige `Last-Event-ID` → wie ohne (tail 500). → Tests in Aufgabe 12.
5. **Unerwartete RCON-Ausgaben:** `list uuids` mit 0 Spielern, ohne UUIDs, mit `§`-Codes, mit Komma-Namen-Grenzfällen, fremdes Format (Plugin überschreibt `list`); erwartet: Parser liefert korrektes Ergebnis oder `available: false`, nie einen 500er. → Tests in Aufgabe 10.

## Dateistruktur

```
internal/store/migrations/00004_players_in_game.sql   (1)  ops/whitelist raus, revision+1
internal/agent/instancespec/properties.go, env.go     (1)  Allowlist + Env ohne Ops/Whitelist
internal/agent/instances/datadir.go                   (1)  clearStaleLists entfällt
internal/servers/** (Tests), web/src/features/servers/properties-fields.tsx, form-model.ts, web/src/mocks/db.ts  (1)
internal/agent/agentapi/console.go                    (2)  Draht-Typen, Validierung, ConsoleClient, LogStream
internal/agent/client.go, client_console.go           (2)  Client: Logs (Stream), Rcon, Players; StatusError.Is
internal/httpx/errors.go, internal/audit/types.go     (3)  Codes, Aktionen
api/openapi.yaml, web/src/api/schema.d.ts             (3)  Vertrag
internal/console/handler.go                           (3)  Deps, Handler, Routes (Stubs, ab B2 echt)
cmd/panel/app.go                                      (3)  Verdrahtung des Handlers
web/src/mocks/handlers/console.ts, db.ts              (3)  Mocks inkl. SSE
internal/agent/docker/stream.go, logs.go, exec.go     (4)  Demultiplexer, ContainerLogs, Exec*
internal/agent/instances/console.go                   (5)  Manager.Logs, Manager.Rcon
internal/agent/instances/players.go                   (6)  Manager.Players (os.Root)
internal/agent/server.go, server_console.go           (7)  Endpunkte, Streaming, Limits
internal/agent/docker/integration_linux_test.go       (8)  dockerintegration
internal/console/command.go                           (9)  POST /console, Limiter, Audit
internal/console/players.go, listparse.go             (10–11) Online, Listen, Aktionen
internal/console/stream.go, access.go                 (12) SSE, Neuverbindung, erneute Prüfung
cmd/panel/app.go                                      (13) echte Deps
web/src/features/console/**                           (14–15) Parser, Puffer, Konsole-Tab
web/src/features/players/**                           (16–17) Spieler-Tab, Online-Karte
web/e2e/servers.spec.ts (erweitert)                   (18) Playwright
scripts/ci-instances.sh                               (19) CI-Abnahme
docs/dev/acceptance-tp3a.md, docs/dev/README.md, CLAUDE.md, README.md (20–21)
```

## Verträge (für alle Teilpläne verbindlich)

### V1 – agentapi (Aufgabe 2, Datei `internal/agent/agentapi/console.go`)

```go
package agentapi

const (
	PathInstanceLogs    = "/v1/instances/logs"
	PathInstanceRcon    = "/v1/instances/rcon"
	PathInstancePlayers = "/v1/instances/players"

	ErrorCodeNotFound        = "not_found"        // 404: container or data directory missing
	ErrorCodeNotRunning      = "not_running"      // 409: rcon on a container that is not running
	ErrorCodeRconUnavailable = "rcon_unavailable" // 503: rcon-cli exited non-zero

	MaxLogTail         = 1000
	MaxCommandBytes    = 1000
	MaxLogLineBytes    = 8 << 10
	MaxRconOutputBytes = 64 << 10

	StreamOut = "out"
	StreamErr = "err"

	HeaderContainerRunning = "X-Container-Running" // logs response: "true" | "false"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrNotRunning      = errors.New("instance not running")
	ErrRconUnavailable = errors.New("rcon unavailable")
)

// LogsRequest: Since > 0 (Docker timestamp in ns) wins over Tail.
type LogsRequest struct {
	ID    int64 `json:"id"`
	Tail  int   `json:"tail"`
	Since int64 `json:"since"`
}
func (r LogsRequest) Validate() error // id > 0, 0 <= tail <= MaxLogTail, since >= 0; errors wrap instancespec.ErrInvalid

// LogLine is one NDJSON line of the logs stream.
type LogLine struct {
	TS     int64  `json:"ts"`     // Docker timestamp, ns since epoch
	Stream string `json:"stream"` // StreamOut | StreamErr
	Line   string `json:"line"`   // without timestamp and trailing \r\n, <= MaxLogLineBytes, valid UTF-8
}

type RconRequest struct {
	ID      int64  `json:"id"`
	Command string `json:"command"`
}
func (r RconRequest) Validate() error // id > 0 and ValidateCommand
type RconResponse struct {
	Output string `json:"output"`
}

// ValidateCommand: 1..MaxCommandBytes bytes, valid UTF-8, no rune < 0x20 and no 0x7f, not only
// spaces. Errors wrap instancespec.ErrInvalid. Shared by panel and agent.
func ValidateCommand(cmd string) error

type PlayersRequest struct {
	ID int64 `json:"id"`
}
func (r PlayersRequest) Validate() error // id > 0

type PlayerOp struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}
type PlayerEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}
type PlayerBan struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	Created string `json:"created"`
	Expires string `json:"expires"`
}
// PlayersResponse: slices are never nil (JSON []); Errors lists unreadable file names in this
// fixed order: "ops.json", "whitelist.json", "banned-players.json". A missing file is no error.
type PlayersResponse struct {
	Ops       []PlayerOp    `json:"ops"`
	Whitelist []PlayerEntry `json:"whitelist"`
	Bans      []PlayerBan   `json:"bans"`
	Errors    []string      `json:"errors"`
}

// LogStream is an open logs stream. Next returns io.EOF when the agent ended the stream
// (container stopped/removed); any other error means the stream broke. Close is idempotent.
// Running reports the container state the agent saw when it opened the stream (response
// header X-Container-Running: "true" | "false"); a stopped container still delivers its
// history (tail/since) and then ends the stream.
type LogStream interface {
	Next() (LogLine, error)
	Running() bool
	Close() error
}

// ConsoleClient is the panel-side view of the console endpoints (implemented by *agent.Client).
type ConsoleClient interface {
	Logs(ctx context.Context, req LogsRequest) (LogStream, error)
	Rcon(ctx context.Context, id int64, command string) (string, error)
	Players(ctx context.Context, id int64) (PlayersResponse, error)
}
```

Client (`internal/agent/client_console.go`, Aufgabe 2):

- `Logs`: kein Gesamt-Timeout (nur `ctx`), aber höchstens 5 s bis zu den Antwort-Headern. Nicht-2xx → `*StatusError` wie `do`. Zeilen werden mit einem `bufio.Reader` gelesen, eine NDJSON-Zeile ≤ 64 KiB, sonst Fehler (`ErrUnavailable`).
- `Rcon`: Timeout `RconTimeout = 15 * time.Second`; prüft vorher `RconRequest.Validate`.
- `Players`: Timeout 5 s.
- `StatusError.Is` prüft zuerst die neuen Codes: `ErrNotFound` ⇔ 404 + `not_found`; `ErrNotRunning` ⇔ 409 + `not_running`; `ErrRconUnavailable` ⇔ 503 + `rcon_unavailable`; danach unverändert die bestehenden Regeln. Folge: `503 rcon_unavailable` passt zusätzlich auf `agentapi.ErrUnavailable`, `404 not_found` auf `instancespec.ErrInvalid`, `409 not_running` auf `banrules.ErrConflict` – Aufrufer (B2) prüfen deshalb immer zuerst die spezifischen Fehler (`ErrNotRunning`, `ErrRconUnavailable`, `ErrNotFound`, `ErrDockerUnavailable`, `ErrBusy`) und erst danach `ErrUnavailable`.
- `var _ agentapi.ConsoleClient = (*Client)(nil)`.

Draht: `POST` mit `Content-Type: application/json`; `logs` inspiziert den Container zuerst (fehlt er → `404 not_found`) und antwortet `200` mit `Content-Type: application/x-ndjson` und Header `X-Container-Running: true|false` (Zustand beim Öffnen), eine `LogLine` pro Zeile, Flush nach jeder Zeile; `rcon` → `200 RconResponse`; `players` → `200 PlayersResponse`; Fehler als `ErrorResponse{error, code}`.

### V2 – agent-intern (Aufgaben 4–7)

```go
// internal/agent/server.go — Option wie WithInstances
type Console interface {
	// Logs streams the container log and calls emit for each line until the container's log
	// ends, ctx is cancelled or emit returns an error. agentapi.ErrNotFound if the container is
	// missing, agentapi.ErrDockerUnavailable if Docker is down.
	// start is called exactly once before the first line, with the container state seen when
	// the stream opened; the server sets X-Container-Running from it and writes the header.
	Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error
	Rcon(ctx context.Context, id int64, command string) (string, error) // ErrNotFound, ErrNotRunning, ErrRconUnavailable (message = first 500 chars of output), agentapi.ErrBusy
	Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error) // ErrNotFound if the data directory is missing
}
func WithConsole(c Console) Option
```

`*instances.Manager` implementiert `Console`. Ohne `WithConsole` antworten die drei Endpunkte `503 docker_unavailable`.

### V3 – panel-API (Aufgabe 3, `api/openapi.yaml`)

Neue Operationen (Tag `console`):

| Pfad | Methode | operationId | Regel | Request | Antworten |
|---|---|---|---|---|---|
| `/servers/{id}/console/stream` | get | `streamConsole` | console | Query `tail` (integer 0–1000, optional) | `200 text/event-stream` (string), 401, 403, 404, 429, 503 |
| `/servers/{id}/console` | post | `sendConsoleCommand` | console | `ConsoleCommandRequest` | `200 ConsoleCommandResponse`, 400, 401, 403, 404, 409, 429, 503 |
| `/servers/{id}/players` | get | `getOnlinePlayers` | view | – | `200 OnlinePlayers`, 401, 403, 404 |
| `/servers/{id}/players/lists` | get | `getPlayerLists` | console | – | `200 PlayerLists`, 401, 403, 404, 503 |
| `/servers/{id}/players/actions` | post | `playerAction` | console | `PlayerActionRequest` | `200 ConsoleCommandResponse`, 400, 401, 403, 404, 409, 429, 503 |

Schemas:

```yaml
ConsoleCommandRequest:
  type: object
  additionalProperties: false
  required: [command]
  properties:
    command: { type: string, minLength: 1, maxLength: 1001 }   # 1000 + optional leading "/"
ConsoleCommandResponse:
  type: object
  required: [output]
  properties:
    output: { type: string }
OnlinePlayer:
  type: object
  required: [name, uuid]
  properties:
    name: { type: string }
    uuid: { type: [string, 'null'] }
OnlinePlayers:
  type: object
  required: [available, online, max]
  properties:
    available: { type: boolean }
    online: { type: array, items: { $ref: '#/components/schemas/OnlinePlayer' } }
    max: { type: [integer, 'null'] }
PlayerOp:
  type: object
  required: [uuid, name, level]
  properties: { uuid: { type: string }, name: { type: string }, level: { type: integer } }
PlayerEntry:
  type: object
  required: [uuid, name]
  properties: { uuid: { type: string }, name: { type: string } }
PlayerBan:
  type: object
  required: [uuid, name, reason, source, created, expires]
  properties:
    uuid: { type: string }
    name: { type: string }
    reason: { type: string }
    source: { type: string }
    created: { type: string }
    expires: { type: string }
PlayerLists:
  type: object
  required: [ops, whitelist, bans, errors]
  properties:
    ops: { type: array, items: { $ref: '#/components/schemas/PlayerOp' } }
    whitelist: { type: array, items: { $ref: '#/components/schemas/PlayerEntry' } }
    bans: { type: array, items: { $ref: '#/components/schemas/PlayerBan' } }
    errors: { type: array, items: { type: string, enum: [ops.json, whitelist.json, banned-players.json] } }
PlayerAction: { type: string, enum: [kick, ban, pardon, op, deop, whitelist_add, whitelist_remove] }
PlayerActionRequest:
  type: object
  additionalProperties: false
  required: [action, name]
  properties:
    action: { $ref: '#/components/schemas/PlayerAction' }
    name: { type: string, pattern: '^[A-Za-z0-9_]{3,16}$' }
    reason: { type: string, maxLength: 200 }
```

- Error-Enum um `instance_not_running` und `rcon_unavailable` erweitert.
- `ServerProperties` ohne `whitelist` und `ops` (Aufgabe 1).
- SSE-Ereignisse (im `description` der Stream-Operation dokumentiert): `event: line` mit `id: <ts ns>` und `data: {"ts": <unix ms>, "stream": "out"|"err", "line": "…"}`; `event: state` mit `data: {"running": bool}`; Kommentar `: ping` alle 15 s. Das Frontend definiert dafür eigene TS-Typen `ConsoleLineEvent`, `ConsoleStateEvent` in `web/src/api/types.ts` (Aufgabe 3).

### V4 – panel-intern (Aufgaben 3, 9–13)

```go
package console // github.com/Bernotese/mc-panel/internal/console

type Deps struct {
	DB     *sql.DB                // store.Open
	Clock  clock.Clock
	Audit  audit.Recorder
	Agent  agentapi.ConsoleClient // nil = no agent: every call fails with agentapi.ErrUnavailable
	Access Access                 // nil = deny all (open streams end at the next 30 s recheck); cmd/panel always sets it from B2 on
	Log    *slog.Logger           // nil = slog.Default()
}

// Access re-checks the caller of an open stream (spec §6.2). Implemented in B2.
type Access interface {
	// Recheck returns nil while the session p.SessionID is valid (not expired, not revoked,
	// stage full), the user exists and still holds perm on serverID (admins always do).
	Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error
}

func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
// Shutdown closes all open streams. B2 registers it with http.Server.RegisterOnShutdown in
// cmd/panel/main.go, so open SSE streams never hold up the 10 s graceful shutdown.
func (h *Handler) Shutdown()
```

Routen (Aufgabe 3 registriert sie mit diesen Regeln; bis B2 antworten alle Handler `503 agent_unavailable` mit Meldung `"console not implemented yet"`):

```go
api.Get("/servers/{id}/console/stream", rbac.Server(rbac.PermServerConsole, "id"), h.handleStream)
api.Post("/servers/{id}/console", rbac.Server(rbac.PermServerConsole, "id"), h.handleCommand)
api.Get("/servers/{id}/players", rbac.Server(rbac.PermServerView, "id"), h.handleOnlinePlayers)
api.Get("/servers/{id}/players/lists", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerLists)
api.Post("/servers/{id}/players/actions", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerAction)
```

### V5 – MSW-Mock (Aufgabe 3, `web/src/mocks/handlers/console.ts`)

- Zustand pro Server in `db` (`web/src/mocks/db.ts`): `players: { online: OnlinePlayer[]; ops: PlayerOp[]; whitelist: PlayerEntry[]; bans: PlayerBan[] }`, Startwerte: Server „survival“ (läuft) mit 2 Online-Spielern (`Alex`, `Steve`), 1 Op, 2 Whitelist-Einträgen, 1 Ban.
- `streamConsole`: `http.get` mit einer `ReadableStream`-Antwort (`Content-Type: text/event-stream`) – nicht `sse()` aus `msw`, das in Vitest/jsdom schon beim Import wirft und keine 401/403/404 liefern kann; sendet sofort 12 Startzeilen (darunter je eine mit ANSI-Farbe `\u001b[33m…\u001b[0m`, mit `§a…`, eine `WARN`, eine `ERROR`, zwei RCON-Rauschzeilen `Thread RCON Client /127.0.0.1 started|shutting down`), danach alle 2 s eine Zeile `[HH:MM:SS INFO]: Tick <n>`; `id` steigt monoton (ns). Gestoppter Server: nur `state {running:false}`.
- `sendConsoleCommand`: Validierung wie V1 (`400`), gestoppter Server → `409 instance_not_running`; `list` → `There are N of a max of 20 players online: …`; sonst `Unknown or incomplete command…` außer `say …` → `""`. Audit-Eintrag im Mock-Audit wie bei Power.
- `getOnlinePlayers`: laufend → `available: true` mit `online`/`max: 20`; gestoppt → `available: false, online: [], max: null`.
- `getPlayerLists`: Listen aus `db`, `errors: []`.
- `playerAction`: wendet die Aktion auf `db` an (kick entfernt aus `online`, ban fügt `bans` hinzu und entfernt aus `online`, pardon entfernt, op/deop, whitelist_add/remove) und antwortet mit dem Minecraft-Text (z. B. `Made Steve a server operator`); gestoppt → `409 instance_not_running`; ungültiger Name/Grund → `400`.
- Rechte im Mock über `serverPermissions` wie bei den bestehenden Server-Mocks (`server.console` bzw. `server.view`).
