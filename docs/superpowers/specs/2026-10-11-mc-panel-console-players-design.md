# mc-panel – Sub-project 3a: Console, logs, players (design spec)

- **Date:** 2026-10-11
- **Status:** implemented (PRs #18–#24, 2026-10-11)
- **Author:** Jannes Göbel (with Claude)
- **Builds on:** `2026-10-08-mc-panel-foundation-design.md` (sub-project 1), `2026-10-10-mc-panel-instances-design.md` (sub-project 2)

## 1. Goal and context

Sub-project 3 ("live operation", sub-project 1 §1.1) is split in two: **3a** (this document) brings the console, the live log and player management; **3b** brings the file browser and plugins/mods (Modrinth, Hangar). Each is usable on its own and has its own spec, plan and PRs. (Now: 3b was split further into 3b, file browser, and 3c, plugins and mods; both are done.)

### 1.1 Success

A user with `server.console` opens a server's console and sees the last 500 log lines, then the log live, also across container restarts. They send commands and see the response. In the "Players" tab they see online players, ops, whitelist and bans, and can kick, ban, unban, grant op and maintain the whitelist – without restarting the server.

### 1.2 Unchanged guideline

A compromised panel must not get root on the host (sub-project 1 §3). The panel still gets neither the Docker socket nor access to `/srv/mc-panel/instances`; logs, commands and player lists go through the agent, which checks every request itself.

### 1.3 Non-goals (3a)

- File browser, plugins/mods, downloading older log files (`logs/*.log.gz`) – sub-project 3b. (Now: the file browser is sub-project 3b, plugins and mods are sub-project 3c; old log files under `logs/` can be downloaded like any other file in the file browser.)
- Tab completion of commands, interactive terminal (stdin/TTY).
- A dedicated UI for IP bans (still possible through the console: `ban-ip`, `pardon-ip`).
- Player count on the dashboard (every query produces RCON lines in the server log, §3.3); it is shown only on the detail page. (Now: since sub-project 5 the dashboard shows the player count from monitoring, which uses the Server List Ping through mc-router, not RCON.)
- Backups (sub-project 4), monitoring/alerts (sub-project 5). (Now: both done.)

## 2. Decisions

| Area | Decision | Rationale |
|---|---|---|
| Permissions | `server.view`: online players. `server.console`: live log, commands, player lists, player actions | Logs contain player IPs; whoever may send commands can give themselves `op` anyway. No new permission |
| Commands | RCON via `docker exec rcon-cli <command>` in the container | itzg ships `rcon-cli` and generates the RCON password itself (`/data/.rcon-cli.env`); argv without a shell; no change to the container template needed |
| Log | Docker container log (`follow`) through the agent | Contains everything the server prints; Docker handles rotation (sub-project 2 §5.4: 3 × 10 MiB) |
| Browser ↔ panel | Server-Sent Events for the log, normal `POST` requests for commands | CSRF, permissions, rate limit and audit go through the existing middleware; no WebSocket library; `EventSource` reconnects by itself; Caddy passes SSE through without configuration |
| Player lists | The server is authoritative (`ops.json`, `whitelist.json`, `banned-players.json`); changes via RCON | Take effect immediately, permissions granted in-game are kept, and a crash restart resets nothing |
| Log rendering | Own virtualised log component instead of xterm.js | Lightweight, usable on mobile, search/filter; a terminal emulator adds nothing without stdin |

### 2.1 Rejected alternatives

- **WebSocket + stdin attach + xterm.js:** requires `OpenStdin` in the container template (recreating all containers), a WebSocket library and origin/permission/audit checks per message alongside the middleware; player actions would still need RCON.
- **Commands via stdin, log via SSE:** commands would have no direct response, player functions would still need RCON – two command paths.
- **agent speaks RCON directly to the container IP:** the agent would have to read the password from `/data` as root and implement the RCON protocol itself; `rcon-cli` in the container does both.
- **panel stays authoritative for ops/whitelist (as in sub-project 2):** if Docker restarts the container after a crash, itzg writes the old lists back from the container environment; bans would still be stored only in the game.

## 3. Changes to sub-project 2

### 3.1 Ops and whitelist leave the properties

- The keys `ops` and `whitelist` are removed from the allowlist (sub-project 2 §7) in `internal/agent/instancespec` (panel and agent); whoever still sends them gets `400 validation_failed`. `whitelist_enabled` stays (`ENABLE_WHITELIST`, change → "restart required").
- `instancespec.Env` no longer sets `OPS`, `WHITELIST`, `EXISTING_OPS_FILE` and `EXISTING_WHITELIST_FILE`. Without `OPS`/`WHITELIST`, itzg leaves `ops.json` and `whitelist.json` unchanged (`scripts/start-setupRbac` in the itzg image, checked 2026-10-11).
- The agent no longer empties `ops.json`/`whitelist.json` (`clearStaleLists` in `internal/agent/instances/datadir.go` is removed).
- New migration `internal/store/migrations/00004_players_in_game.sql`: `properties = json_remove(properties, '$.ops', '$.whitelist')` and `revision = revision + 1`, `updated_at` = time of the migration – only for rows that contain one of the two keys. Running servers then show "restart required"; the reconcile loop recreates stopped ones without the variables.
- The files in the data directory stay unchanged and, after sub-project 2, match the previous lists exactly. Only exception: names of a **disabled** whitelist were stored only in the DB (sub-project 2 emptied `whitelist.json` in that case) and are lost; they had no effect.
- In the UI the ops and whitelist fields are removed from the form and the settings (§7).

### 3.2 No other change to the container template

`ENABLE_RCON=true` stays (sub-project 2 §5.4). RCON is reachable only in the internal network `mcpanel-mc` and protected by the random itzg password that is regenerated on every start.

### 3.3 RCON noise in the log

Every RCON call produces two lines in the server log (`Thread RCON Client /… started` and `… shutting down`; observed with Paper on the test VM, Vanilla behaves the same). The console hides them by default (§7.2); the online players query is cached (§6.3).

## 4. Architecture

```
Browser ──SSE /console/stream──▶ panel internal/console ──NDJSON-Stream──▶ agent ──▶ Docker: GET /containers/{id}/logs?follow
        ──POST /console─────────▶   · permissions, CSRF, limits ──JSON────▶       ──▶ Docker: exec rcon-cli <command>
        ──GET/POST /players─────▶   · audit, parser            ──JSON──────▶       ──▶ /srv/mc-panel/instances/<id>/*.json (os.Root)
```

- **panel:** new package `internal/console` with `Handler` and `Routes(api *rbac.Router)`, wired in `cmd/panel/app.go`. It uses the agent client (`internal/agent`) and, for server existence/state, `internal/servers` or the DB. `internal/servers` does not grow because of it.
- **agent:** new methods on the instance manager (`internal/agent/instances`) for logs, RCON and player lists; Docker calls in `internal/agent/docker`; requests and validation in `internal/agent/agentapi`.

## 5. agent

### 5.1 API

Pattern as in sub-project 2 §5.1: fixed paths, `POST` with a JSON body, IDs in the body, `DisallowUnknownFields`, peer UID check, body limit 16 KiB.

| Method, path | Body | Response |
|---|---|---|
| `POST /v1/instances/logs` | `{id, tail, since}` | `200`, stream `application/x-ndjson` |
| `POST /v1/instances/rcon` | `{id, command}`, since sub-project 4 optionally `timeout_ms` (§5.3) | `200 {output}` |
| `POST /v1/instances/players` | `{id}` | `200 {ops, whitelist, bans, errors}` |

Errors as before as `agentapi.ErrorResponse` (`{error, code}`); new values for `code` as constants in `agentapi`: `400` (validation, without code), `404` with `not_found` (container `mcpanel-mc-<id>` or data directory missing), `409` with `not_running` (container not running; `rcon` only), `429` with `busy` (limit, existing code), `503` with `docker_unavailable` (existing), `503` with `rcon_unavailable` (`rcon` only).

### 5.2 Logs

- `id` > 0; `tail` 0–1000; `since` ≥ 0 = Docker timestamp in nanoseconds since the Unix epoch (0 = none). If `since` > 0, `tail` is ignored.
- Docker call: `GET /containers/mcpanel-mc-<id>/logs?follow=1&stdout=1&stderr=1&timestamps=1&tail=<tail>[&since=<s>.<ns>]`. The container has no TTY, the response is the Docker multiplex stream (8-byte header per frame); a shared demultiplexer (§5.5) splits it.
- Each line is written as one NDJSON line: `{"ts": <ns>, "stream": "out"|"err", "line": "<text>"}`. `ts` comes from the Docker timestamp; `line` without the timestamp and without a trailing `\n`/`\r`, truncated to 8 KiB (at a UTF-8 boundary), invalid UTF-8 replaced by U+FFFD.
- The stream ends when Docker ends it (container stopped, removed) or the caller disconnects (context). Docker also ends `follow` when the container is recreated; the caller (panel) reconnects (§6.2).
- Deadlines: the global `ReadTimeout`/`WriteTimeout` of the agent server (5 s/10 s) do not apply to this path; the handler sets a 30 s deadline via `http.ResponseController` before every write and flushes after every write.
- At most 16 concurrent log streams in the agent, otherwise `429`.

### 5.3 RCON

- `id` > 0; `command`: 1–1000 bytes, valid UTF-8, no character < `0x20` and no `0x7f` (so no line break or tab either), not only spaces, and no leading `-` (after optional spaces and at most one `/`), so that the command is never read as an option of `rcon-cli`.
- Flow: inspect the container (missing → `404 not_found`, not running → `409 not_running`), `POST /containers/{id}/exec` with `{"Cmd": ["rcon-cli", "--", command], "User": "<uid>:<gid>", "WorkingDir": "/data", "Env": ["HOME=/data"], "AttachStdout": true, "AttachStderr": true, "AttachStdin": false, "Tty": false}`, then `POST /exec/{id}/start` with `{"Detach": false, "Tty": false}` (the response body is the multiplex stream, no hijack), then `GET /exec/{id}/json` for the exit code.
- `uid:gid` is `mcpanel-mc` (like the container itself). `rcon-cli` is fixed in the code; only the integration tests swap it for a busybox program through the manager configuration (`RconCommand`, default `rcon-cli`).
- Timeout 10 s for the whole flow (addendum sub-project 4: configurable via `timeout_ms`, see below); output (stdout and stderr together) at most 64 KiB, then truncated with the note `[… gekürzt]`.
- Exit code ≠ 0 (e.g. RCON not ready yet during startup) → `503` with code `rcon_unavailable` and the first 500 characters of the output in `error`. Exit code 0 → `200 {output}` with the output as text (Minecraft `§` format codes are kept).
- RCON calls do not go through the instance's serial queue (sub-project 2 §5.1) because they do not change the container state. At most 4 concurrent calls per instance, otherwise `429`. On shutdown the agent aborts running console calls and player list reads (the panel sees an aborted connection and reports `503 agent_unavailable`).
- **Addendum sub-project 4 (backups):** the body optionally takes `timeout_ms` (integer; missing or `0` = 10 s as before, otherwise 1–120000, strictly checked: other values → `400`). It bounds the whole flow in the agent; the panel's client waits for the deadline plus 5 s. The panel's console sends no value (10 s); backups send 60 s for `save-off`, `save-all flush` and `save-on` (`save-all flush` can take longer than 10 s for large worlds). Spec: `2026-10-12-mc-panel-backups-design.md`.

### 5.4 Player lists

- Reads `ops.json`, `whitelist.json` and `banned-players.json` from `/srv/mc-panel/instances/<id>`; works also with a stopped or missing container (directory missing → `404`).
- The directory belongs to the container user; the root agent therefore reads files that a compromised server controls. Hence:
  - Opened through `os.Root` on the data directory (no resolution of symlinks out of the directory, no `..`), with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` (no hanging on a FIFO, no following of a symlink as the last element).
  - After opening, `fstat`: regular files only, at most 1 MiB; at most 1 MiB + 1 byte is read.
  - Contents are parsed and only the fields below are passed on; strings truncated to 256 characters, lists to 1000 entries (decoding stops after 1000 entries).
  - At most 2 concurrent reads in the agent, otherwise `429 busy`.
- Response:
  - `ops`: `[{uuid, name, level}]`
  - `whitelist`: `[{uuid, name}]`
  - `bans`: `[{uuid, name, reason, source, created, expires}]` (`created`/`expires` as text as in the file, e.g. `2026-10-11 09:32:51 +0000` or `forever`)
  - `errors`: names of the files that exist but are unreadable (symlink, not a regular file, too large, not valid JSON); the corresponding list is then empty. A missing file is not an error but an empty list.

### 5.5 Docker client

New in `internal/agent/docker`, no further calls otherwise (no attach, no exec with TTY or stdin):

| Method | Docker API |
|---|---|
| `ContainerLogs(ctx, id, opts) (io.ReadCloser, error)` | `GET /containers/{id}/logs` (stream, without client timeout; context only) |
| `ExecCreate(ctx, id, cfg) (string, error)` | `POST /containers/{id}/exec` |
| `ExecStart(ctx, execID) (io.ReadCloser, error)` | `POST /exec/{id}/start` |
| `ExecInspect(ctx, execID) (ExecInfo, error)` | `GET /exec/{id}/json` |
| `Demux(r io.Reader) (Frame, error)` or a reader per frame | Multiplex format: 1 byte stream (1 = stdout, 2 = stderr), 3 bytes zero, 4 bytes length (big endian), then payload; frame length at most 1 MiB, otherwise error |

## 6. panel

### 6.1 API

All routes through `*rbac.Router`, described in `api/openapi.yaml`, with an MSW mock.

| Route | Rule | Content |
|---|---|---|
| `GET /servers/{id}/console/stream` | `Server(server.console, "id")` | SSE (§6.2) |
| `POST /servers/{id}/console` | `Server(server.console, "id")` | `{command}` → `200 {output}` (§6.4) |
| `GET /servers/{id}/players` | `Server(server.view, "id")` | `{available, online: [{name, uuid}], max}` (§6.3) |
| `GET /servers/{id}/players/lists` | `Server(server.console, "id")` | `{ops, whitelist, bans, errors}` as in §5.4 |
| `POST /servers/{id}/players/actions` | `Server(server.console, "id")` | `{action, name, reason?}` → `200 {output}` (§6.5) |

- Servers that are not set up (without an `instances` row) → `404 not_found` on all five routes.
- agent unreachable → `503 agent_unavailable`; Docker unavailable → `503 docker_unavailable` (exception: `GET /players` then answers `available: false`).

### 6.2 Log stream (SSE)

- Response `text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no`. The panel server's deadlines (`WriteTimeout` 60 s) do not apply; before every write the handler sets 30 s via `ResponseController` and flushes afterwards.
- Events:
  - `event: line`, `id: <ts>` (Docker timestamp in ns), `data: {"ts": <unix-ms>, "stream": "out"|"err", "line": "…"}`.
  - `event: state`, `data: {"running": true|false}` – `true` when the agent stream is first opened successfully, `false` when it ends.
  - Every 15 s a comment (`: ping`) as a heartbeat.
- Start: with a `Last-Event-ID` header (valid number) the panel opens the agent stream with `since` = that value + 1 ns, otherwise with `tail` = 500. The query parameter `tail` (0–1000) overrides the default.
- If the agent stream ends or opening it fails (`404`, `docker_unavailable`, agent gone), the panel sends `state {running: false}` once and retries with `since` = the last timestamp seen. This way the log continues across restarts and recreations of the container, without duplicate lines. After an attempt with a line or a state change the panel waits 3 s; if an attempt has neither (stopped server, agent gone or busy), the wait doubles up to at most 10 s (3 → 6 → 10 s); every line or state change resets it. A `429 busy` from the agent is logged at info level.
- Checks during the stream (via `clock.Clock`, every 30 s; now every 5 s, SEC-29): session valid (not expired, not revoked, fully signed in), user exists, permission `server.console` (or admin) still holds, server still exists. If a check fails, the panel closes the stream. The stream does not extend the session (`Touch` only on opening, as with every request).
- At most 4 concurrent streams per user in the panel, otherwise `429 rate_limited`. (Now, SEC-24: panel-wide at most 12 log streams plus 2 slots only for admins, otherwise `429`.) On shutdown the panel closes all streams; shutdown waits at most 20 s (longer than the RCON call with 15 s).

### 6.3 Online players

- The panel calls `rcon` with `list uuids` through the agent and parses the response. Formats (examples from real servers in `internal/console/testdata/`):
  - `There are 2 of a max of 20 players online: Alex (uuid), Steve (uuid)`
  - `There are 0 of a max of 20 players online: `
  - without UUIDs (older versions ignore `uuids`): `… online: Alex, Steve`
  - `§` format codes are removed before parsing.
- Response: `available: true`, `online` (name; `uuid` or `null`), `max`. If the server is not running, RCON does not answer or the format is unknown → `available: false`, `online: []`, `max: null` (no HTTP error).
- Cache per server 10 s (success and `available: false`), so that several viewers produce no additional RCON noise.

### 6.4 Commands

- The panel removes a leading `/` and checks like the agent (§5.3); violation → `400 validation_failed`.
- Rate limit: 30 commands per minute per user (token bucket, burst 10, pattern as in `internal/servers/powerlimit.go`), shared by commands and player actions; exceeding it → `429 rate_limited` with `Retry-After`.
- Errors: `409 instance_not_running` (agent `not_running`), `503 rcon_unavailable`.
- The RCON call is not aborted when the browser leaves the connection; the audit entry (`server.console_command`, §6.7) follows the agent's response, not whether the client is still there. The same applies to player actions (§6.5).
- No sudo (it would get in the way on every command).

### 6.5 Player actions

`action` is an enum; the panel builds the RCON command, user text is never taken over as a command:

| `action` | RCON command |
|---|---|
| `kick` | `kick <name>[ <reason>]` |
| `ban` | `ban <name>[ <reason>]` |
| `pardon` | `pardon <name>` |
| `op` | `op <name>` |
| `deop` | `deop <name>` |
| `whitelist_add` | `whitelist add <name>` |
| `whitelist_remove` | `whitelist remove <name>` |

- `name`: `^[A-Za-z0-9_]{3,16}$`. `reason`: only allowed for `kick`/`ban`, otherwise `400`; at most 200 characters, printable, without control characters.
- Rate limit and errors as in §6.4 apply. The response contains the RCON output (e.g. `Made Steve a server operator`).
- `whitelist add` and `op` resolve the name on the server via Mojang (online mode); the server answers unknown names with a text that is returned unchanged.

### 6.6 Error codes

New in `internal/httpx/errors.go` and in the contract's enum:

| Code | Status | When |
|---|---|---|
| `instance_not_running` | 409 | Command or player action while the server is not running |
| `rcon_unavailable` | 503 | RCON does not answer (server still starting, RCON off) |

### 6.7 Audit

- `server.console_command`: target server, details `{command}` (as sent, without the leading `/`), result `ok` or `failed` (on `instance_not_running`, `rcon_unavailable`, agent errors).
- `server.player_action`: target server, details `{action, name, reason?}`, result as above.
- New as constants in `internal/audit/types.go`: `ActionServerConsoleCommand`, `ActionServerPlayerAction`. Rejections by permissions, validation or rate limit are not audited (as in sub-projects 1 and 2).
- Opening the log stream is not audited.

## 7. Frontend

Existing shadcn components; texts `de`/`en`; usable on mobile.

### 7.1 Detail page `/servers/$id`

Tabs (visible only with the matching permission): Overview · **Console** · **Players** · Settings · Delete. (Now: since sub-project 7 the server areas are routes under `routes/_app/servers/$id` (`console`, `players` and others) in the server tree instead of tabs; deleting a server is the danger zone at the end of its settings.)

- **Overview** (`server.view`): new card "Online players" with `count / max` and names; queried every 15 s, only while the page is visible; with `available: false` "not available".
- **Settings** (admin): the ops and whitelist fields are removed; `whitelist_enabled` stays, with a reference to the players tab. The same applies to `/servers/new`.

### 7.2 Console (`server.console`)

- **Log view:** virtualised list (`@tanstack/react-virtual`, the only new dependency), ring buffer of 5000 lines.
- **Rendering:** own parser for ANSI SGR colours (16 colours, bold) and Minecraft `§` codes into spans; no `dangerouslySetInnerHTML`. Lines with `WARN` or `ERROR` are highlighted in colour; so are stderr lines.
- **Tools:** text filter; switch "Hide RCON messages" (on by default, filters the lines from §3.3); auto-scroll that pauses when scrolling up, with a "New lines ↓" button; connection indicator (connected, server stopped, reconnecting, "Too many open consoles – close another one." on a `429` of the stream); "Copy" of the visible lines.
- **Command line:** at the bottom (also on mobile), history with ↑/↓ (in memory only, 100 entries). A sent command appears as its own, separated block `› command` with the RCON response below, placed in the log view. Disabled with a note when the server is stopped and on `forbidden`/`not_found` of the stream. Below the line: "`stop` ends the server, Docker restarts it immediately – use the power button to stop it."
- **Connection:** `EventSource` (same origin, cookie). On a drop the browser reconnects by itself (with `Last-Event-ID`); on `401`/`403` the view closes the connection and shows the matching note.

### 7.3 Players (`server.console`)

- **Online:** name, actions kick, ban (dialog with optional reason), make op/remove op.
- **Whitelist:** add name, remove; note whether the whitelist is active, with a link to the setting (admin).
- **Ops:** add, remove; show level.
- **Bans:** name, reason, by, since, until; unban.
- Ops and whitelist show a "restart required" warning as long as the server still runs with the old environment (sub-project 2); the "Online" card shows no list while the server is stopped. The op actions are called "Op geben" and "Op entziehen" (en "Make op"/"Remove op"); their accessible name contains the visible text plus the player name ("Op geben: Steve"). The tab bar scrolls horizontally on phones.
- Lists are readable also while the server is stopped; changes only while the server is running (buttons disabled otherwise, with a note). Unreadable files (`errors`) show a note on the affected list.
- After every action: toast with the RCON response, then the lists and online players are reloaded.

### 7.4 Mocks

MSW mocks for all new operations. The stream mock delivers some initial lines (including ANSI, `§` and RCON lines) through a `ReadableStream` and then one line every 2 s; commands answer with sample text. `make preview` thus shows the console and the players tab without a backend.

## 8. Development, CI and acceptance

- **Mac:** only `make check` (CLAUDE.md). `make dev` runs without Docker: console and players show "Docker not available"; the UI preview (mock API) shows everything.
- **CI:** as before; additionally new cases in `dockerintegration` (§9) and in `scripts/ci-instances.sh`.
- **Manual acceptance:** on the test VM following `docs/dev/acceptance.md` ("Console and players").

## 9. Tests

| Level | Content |
|---|---|
| agent `docker` | Fake Docker server (`httptest` on a Unix socket): demultiplexer (header split across read boundaries, lines across frame boundaries, frame > 1 MiB, unknown stream type), `ContainerLogs` with follow/since/tail and end on stop, exec lifecycle with exit code, timeouts |
| agent `agentapi`/server | Table tests for `rcon` (control characters, line break, tab, invalid UTF-8, 0 and 1001 bytes, only spaces, unknown fields) and `logs` (`tail`/`since` bounds); stream limit `429`; RCON limit per instance; deadline per write; `not_running`; `rcon_unavailable`; line truncation and UTF-8 replacement |
| agent player lists | Symlink out of the directory, symlink as the last element (e.g. to `/etc/shadow`), FIFO, directory instead of file, > 1 MiB, broken JSON, missing file (no error), truncation of strings and lists |
| agent integration | `dockerintegration` (Linux, busybox): log stream of a real container including end on stop and `since`; exec via `RconCommand` = busybox program with output and exit code ≠ 0 |
| `instances` (CI) | `scripts/ci-instances.sh`: a Paper server delivers `list` through the agent; the log stream contains `Done (`; after `whitelist add` and `op`, `players` shows the entries; the container no longer has `OPS`/`WHITELIST`/`EXISTING_*_FILE` |
| panel `console` | Fake agent + `storetest`: permissions (view vs. console, admin, foreign server → `403`), SSE format, `Last-Event-ID` → `since`, reconnect after stream end with `state` events, closing on revoked session/withdrawn permission/deleted server (fake clock), stream limit, rate limit, audit entries, table action → RCON command, validation of `name`/`reason`, parser `list uuids` with examples from `testdata/`, 10 s cache |
| Migration | `ops`/`whitelist` removed, `revision` bumped only for affected rows, other properties unchanged |
| `instancespec` | `ops`/`whitelist` are rejected; `Env` contains no `OPS`/`WHITELIST`/`EXISTING_*_FILE` (golden test adjusted) |
| Contract | existing tests: OpenAPI ↔ routes, error codes ↔ enum, mock per operation |
| Frontend | Vitest: ANSI/`§` parser, ring buffer, command history, RCON filter, online card; Playwright against the mock: open console → lines appear → send command → response block; players tab → kick → toast |

Conventions from sub-projects 1 and 2 apply unchanged (only the standard library in Go tests, `clock.Clock`, contexts everywhere).

## 10. Acceptance criteria

Automated in CI: 1 as well as the agent side of 2 and 4 (`scripts/ci-instances.sh`). Manually by the user on the test VM: 2–6 (`docs/dev/acceptance.md`, "Console and players").

1. All CI jobs green.
2. The console shows the live log; `say hallo` appears in the game, `list` returns a response. A server restart does not interrupt the view permanently, and no duplicate lines appear.
3. A user with only `server.view` sees the online players, but neither the console nor the player lists (UI without the tabs, API `403`).
4. Whitelist, op, kick, ban and unban take effect without a restart. Ops granted in-game appear in the players tab and are kept after a server restart.
5. If an admin withdraws a user's permission while the console is open, or the user's session is ended, the stream ends within 30 s.
6. All commands and player actions are in the audit log; `panel audit verify` reports OK.
