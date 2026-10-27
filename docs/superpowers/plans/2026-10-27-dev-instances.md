# Dev Instances Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. In this repo the tasks are implemented by agent-team teammates (CLAUDE.md, "Implementation with agent teams").

**Goal:** An admin turns a server ("prod") into the source of a dev instance `dev-<name>` that is overwritten from a restic snapshot of prod every night, sleeps when empty and wakes on a connection attempt, without ever writing to prod.

**Architecture:** The sync is a new job kind in the panel-wide backup queue (`internal/backups`): snapshot of prod (reused or new, kind `dev-sync`), stop dev, cross-repository restore by the files service into dev's data directory, then `servers` re-applies prod's spec plus the dev overrides. A new package `internal/devinst` owns the dev API, the nightly schedule, sleep detection (from monitoring passes), dev ops and the wake loop. Waking goes through a hardened waker container `mcpanel-waker` (the agent binary in `waker` mode, unprivileged, on mc-router's network only): the agent routes sleeping dev hostnames to it and reports the login attempts it logged.

**Tech Stack:** Go 1.x (stdlib tests), SQLite + goose + sqlc, chi via `rbac.Router`, OpenAPI + generated TS, React/TanStack/shadcn, MSW, Playwright, bash CI acceptance.

**Spec:** `docs/superpowers/specs/2026-10-27-mc-panel-dev-instances-design.md` (read it first; §-references below point there).

## Global Constraints

- Prod is never written by this feature: no path into prod's data directory, no stop of prod, no change of prod's `instances` row. Data flows only snapshot(prod) → dev (§1.2).
- Dev name = `"dev-" + prod.Name`; prod names longer than 28 characters get `400 validation_failed` ("the name of a dev instance would exceed 32 characters"). New ordinary servers starting with `dev-` get `400 validation_failed` ("names starting with dev- are reserved for dev instances").
- At most one dev per prod; no dev of a dev (`409 conflict`).
- Dev memory default: 2048 MiB for vanilla/paper/purpur, 3072 MiB when `Type.HasLoader() || Type.IsModpack()`, clamped to `servers.MaxMemoryMiB`.
- Dev overrides: `view_distance` default 4, `simulation_distance` default 3 (range 3–32 as in `instancespec`); `motd` = `"[DEV] " + <prod's resolved motd>`, truncated to 150 runes. Nothing else is overridden; `gamemode` stays prod's.
- Sync time default 300 (05:00, minutes of day) in `instance_settings.backup_timezone`; idle minutes default 15, range 5–120.
- Snapshot reuse: newest prod snapshot of kind `scheduled` or `dev-sync`, newer than `last_sync_at` and at most 6 h old; otherwise a new `dev-sync` snapshot. After a successful sync, every older `dev-sync` snapshot of that prod is forgotten.
- Players online at scheduled time: chat warning, retry every 15 min, at most 2 h after the scheduled time, then run with a final warning. "Sync now": warning, run after 60 s. Fixed RCON texts (no user text): `say [mc-panel] Dev wird in 60 Sekunden mit <prod name> synchronisiert, Änderungen gehen verloren.` and `say [mc-panel] Dev-Sync verschoben: es sind noch Spieler online (nächster Versuch in 15 Minuten).`
- Wake rate: at most one wake per dev per 2 min, at most 10 wakes per minute panel-wide; no wake while the dev's data gate is held.
- Waker texts (fixed): status MOTD `Dev schläft – verbinden zum Aufwecken`, login disconnect `Dev startet (oder wird gerade synchronisiert). Bitte in ca. 1 Minute neu verbinden.`
- Permissions on a dev resolve to its prod's rows; `server.backups` grants nothing on a dev; no permission rows may be stored for a dev id.
- New error codes (exact strings): `dev_exists`, `not_dev`, `dev_managed`.
- New audit actions: `dev.create`, `dev.delete`, `dev.sync`, `dev.settings_update`, `dev.op_add`, `dev.op_remove`, `dev.wake`. New notify event: `dev.sync_failed` (at most once per 6 h per dev).
- New snapshot kind (restic tag and API enum): `dev-sync`.
- Repo rules from CLAUDE.md apply: `clock.Clock` for all time logic, contexts on every request/DB call, stdlib-only Go tests, every route through `rbac.Router` with a rule, errors via `httpx.WriteError`, de + en strings, colours as tokens in `:root` and `.dark`, `make check` on the Mac and `make test-vm` before reporting a task done.

## Review Focus

1. **Wrong-direction restore** — a bug that swaps source and target would destroy prod. Expect: the files service refuses `source_id == id`, the panel refuses any target that is not the dev of the source, and an acceptance test hashes prod's data directory before/after a sync. Tests: Task 4 Step 1, Task 5 Step 1, Task 10 Step 1.
2. **Prod deleted or renamed while a dev exists / dev deleted mid-sync** — expect `409 dev_exists` on prod delete, and a dev delete during a queued/running sync cancels the job like a server delete cancels backup jobs (`errServerDeleted`). Tests: Task 6 Step 1, Task 5 Step 1.
3. **Malicious or broken clients against the waker** (oversized VarInts, slowloris, legacy 0xFE ping, garbage address, FML suffix `\x00FML\x00`, trailing dot, uppercase host) — expect bounded memory/time, no crash, wake only for a normalised known sleeping FQDN. Tests: Task 2 Step 1 (fuzz + table), Task 3 Step 1.
4. **Agent restart / panel restart while devs sleep** — expect the agent touches no routes until the panel's first `PUT /v1/waker`, and the panel re-sends the sleeping set within one loop tick; sleeping state survives panel restart (DB). Tests: Task 3 Step 1, Task 8 Step 1.
5. **Unknown player count** (ping fails, monitoring off, agent down) — expect dev is never put to sleep on unknown data and the idle clock does not advance past a gap of unknown samples. Test: Task 8 Step 1.

---

## Team, branches and order

- Feature branch: `feat/dev-instances` (from `origin/main`, pushed by the lead after Task 1). Teammate branches are created from it: `git worktree add ../mc-panel-wt/<branch> -b <branch> origin/feat/dev-instances`. The lead merges teammate branches into `feat/dev-instances` and opens one PR to `main`.
- Task 1 is done by the lead before any teammate starts. Then in parallel:

| Teammate | Branch | Tasks | Owns (only these files) |
|---|---|---|---|
| `waker` | `feat/dev-instances-waker` | 2, 3 | `internal/waker/**`, `cmd/agent/**`, `internal/agent/instances/waker*.go`, `internal/agent/server_waker*.go`, `internal/agent/instances/router.go` (`Routes` override only), `internal/agent/instances/manager.go` (wiring lines only), `internal/agent/server.go` (route registration only), `internal/config/config.go` (agent waker fields only) |
| `sync` | `feat/dev-instances-sync` | 4, 5 | `internal/files/**` (restore-from), `internal/backups/**` |
| `core` | `feat/dev-instances-core` | 6, 7 | `internal/servers/**`, `internal/store/queries/{servers,rbac,plugins}.sql` + generated, `internal/rbac/**`, `internal/users/**`, `internal/monitoring/**` |
| `devinst` | `feat/dev-instances-devinst` | 8 | `internal/devinst/**`, `cmd/panel/app.go`, `cmd/panel/*dev*_test.go` |
| `web` | `feat/dev-instances-web` | 9 | `web/**` except `web/src/api/schema.d.ts` |

- Task 8's final step (real wiring) runs after the lead merged Tasks 3, 5, 6 and 7; until then `devinst` builds against fakes of its own consumer interfaces.
- Task 10 (acceptance shard, CI, docs) is the lead's, after all merges.

---

### Task 1: Contracts and skeleton (lead)

**Files:**
- Create: `internal/store/migrations/00013_dev_instances.sql`, `internal/store/queries/dev.sql`, `internal/store/dev.sql.go` (generated), `internal/devinst/handler.go`, `internal/devinst/routes_test.go`
- Modify: `api/openapi.yaml`, `web/src/api/schema.d.ts` (generated), `internal/httpx/errors.go`, `internal/audit/types.go`, `internal/notify/notify.go`, `internal/notify/sender.go`, `internal/files/filesapi/backups.go`, `internal/files/filesapi/backups_client.go`, `internal/agent/agentapi/waker.go` (create), `internal/agent/client.go`, `cmd/panel/app.go`, `web/src/mocks/handlers.ts` (+ the mock data file it uses)
- Test: `cmd/panel/openapi_test.go` (existing, must stay green), `internal/agent/agentapi/waker_test.go`, `internal/files/filesapi/*_test.go` (existing)

**Interfaces:**
- Produces (store, migration 00013):
  - `dev_instances(server_id PK REFERENCES servers(id) ON DELETE CASCADE, source_id INTEGER NOT NULL UNIQUE REFERENCES servers(id), sync_paused INTEGER NOT NULL DEFAULT 0, paused_at INTEGER, sync_time INTEGER NOT NULL DEFAULT 300 CHECK (sync_time BETWEEN 0 AND 1439), idle_minutes INTEGER NOT NULL DEFAULT 15 CHECK (idle_minutes BETWEEN 5 AND 120), property_overrides TEXT NOT NULL, next_sync_at INTEGER, deferred_since INTEGER, last_sync_at INTEGER, last_sync_status TEXT CHECK (last_sync_status IN ('ok','failed','running')), last_sync_error TEXT, last_sync_snapshot TEXT, sleeping INTEGER NOT NULL DEFAULT 1, last_wake_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, CHECK (server_id <> source_id))`
  - `dev_ops(server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE, name TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY (server_id, name))`
  - Queries (`dev.sql`, prefix `Dev`): `DevCreate`, `DevGet(server_id)`, `DevGetBySource(source_id)`, `DevList` (joined with `servers.name` and the source's name), `DevListDue(now)` (`sync_paused = 0 AND next_sync_at <= now`), `DevUpdateSettings`, `DevSetNextSync`, `DevSetDeferred`, `DevSetSyncRunning`, `DevSetSyncResult(status, error, snapshot, last_sync_at)`, `DevSetSleeping(server_id, sleeping, last_wake_at)`, `DevListSleeping` (id + name), `DevOpsList(server_id)`, `DevOpsAdd`, `DevOpsRemove`.
- Produces (filesapi): `SnapshotRequest.SourceID int64 \`json:"source_id,omitempty"\`` (0 = the target's own repository); `SnapshotDevSync SnapshotKind = "dev-sync"` (accepted by `Valid()`); `func (c *UnixClient) RestoreFrom(ctx context.Context, targetID, sourceID int64, snapshot string) (Job, error)`; `BackupClient` interface gains `RestoreFrom`.
- Produces (agentapi, `waker.go`): `PathWaker = "/v1/waker"`, `MaxWakerTargets = 500`, `type WakerTarget struct{ ID int64 \`json:"id"\`; FQDN string \`json:"fqdn"\` }`, `type WakerRequest struct{ Sleeping []WakerTarget \`json:"sleeping"\` }` (nil rejected), `type WakerResponse struct{ Ready bool \`json:"ready"\`; Wakes []int64 \`json:"wakes"\` }`, `func (r WakerRequest) Validate() error` (≤ 500, ids > 0 and unique, FQDNs valid via `instancespec.CheckDNSName`, the name label starting with `dev-`), `type WakerClient interface{ Waker(ctx context.Context, req WakerRequest) (WakerResponse, error) }`, `func (c *Client) Waker(...)` in `internal/agent/client.go`.
- Produces (httpx): `CodeDevExists = "dev_exists"`, `CodeNotDev = "not_dev"`, `CodeDevManaged = "dev_managed"`.
- Produces (audit): `ActionDevCreate`, `ActionDevDelete`, `ActionDevSync`, `ActionDevSettingsUpdate`, `ActionDevOpAdd`, `ActionDevOpRemove`, `ActionDevWake` with the strings from Global Constraints. (notify): `EventDevSyncFailed EventType = "dev.sync_failed"`, title `"Dev sync failed"`.
- Produces (OpenAPI): paths `POST /servers/{id}/dev` (Admin, sudo; body `CreateDevRequest{memory_mib?}`; 201 `Server`), `GET /servers/{id}/dev` (`server.view`; `DevInstance`), `PATCH /servers/{id}/dev` (`server.files`; `UpdateDevRequest{sync_paused?, sync_time?, idle_minutes?, view_distance?, simulation_distance?}`; `DevInstance`), `POST /servers/{id}/dev/sync` (`server.files`; 202 `DevInstance`), `GET /servers/{id}/dev/ops` (`server.console`; `DevOps{names: string[]}`), `POST /servers/{id}/dev/ops` (`server.console`; body `{name}`; `DevOps`), `DELETE /servers/{id}/dev/ops/{name}` (`server.console`; `DevOps`). Schemas: `DevInstance{server_id, source_id, source_name, sync_paused, paused_at|null, sync_time, idle_minutes, view_distance, simulation_distance, next_sync_at|null, last_sync_at|null, last_sync_status: ok|failed|running|null, last_sync_error|null, sync_pending: bool, sleeping: bool}`; `Server` gains `dev: DevSummary|null` (`DevSummary{source_id, source_name, sync_paused, next_sync_at|null, last_sync_at|null, last_sync_status|null, sync_pending, sleeping}`) and `dev_id: integer|null`; `SnapshotKind` enum gains `dev-sync`; error enum gains the three codes; audit action enum (if listed) gains the seven actions; notify event enum gains `dev.sync_failed`. Memory in `PATCH /servers/{id}` stays the way to change a dev's memory.
- Produces (skeleton): `package devinst` with `type Handler struct{...}`, `func NewHandler(d Deps) *Handler`, `func (h *Handler) Routes(api *rbac.Router)` registering the seven routes above with exactly those rules; each handler answers `httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotDev, "dev instances are not available yet")`. Wired in `cmd/panel/app.go` next to the other `Routes` calls.

- [ ] **Step 1:** Write `internal/agent/agentapi/waker_test.go`: `TestWakerRequestValidate` with cases nil list (error), 501 targets (error), duplicate id (error), id 0 (error), `FQDN "survival.example.com"` (error: not `dev-`), `"dev-survival.example.com"` (ok), uppercase (error), 254-char name (error).
- [ ] **Step 2:** Run `make check`; expect the new test file not to compile (types missing).
- [ ] **Step 3:** Add migration, `dev.sql`, filesapi/agentapi/httpx/audit/notify additions, OpenAPI paths and schemas, the skeleton package and its wiring, MSW mocks for the seven operations (static fixture: one dev `id 2` of prod `id 1`), then `make generate`.
- [ ] **Step 4:** `make check` passes; `make test-vm SUITE=go` and `SUITE=web` pass (`openapi_test.go`, `handlers.test.ts` green).
- [ ] **Step 5:** Commit `feat(dev): contracts and skeleton for dev instances`, push `feat/dev-instances`.

---

### Task 2: Waker protocol and `agent waker` mode (teammate `waker`)

**Files:**
- Create: `internal/waker/protocol.go`, `internal/waker/server.go`, `internal/waker/protocol_test.go`, `internal/waker/server_test.go`
- Modify: `cmd/agent/main.go`, `cmd/agent/main_test.go`

**Interfaces:**
- Produces: `func ReadHandshake(r io.Reader) (Handshake, error)` with `type Handshake struct{ Protocol int32; Host string; Port uint16; Next int32 }` (`Host` normalised: cut at the first `\x00`, trailing `.` removed, lowercased; error if longer than 255 bytes, invalid UTF-8, or not a valid DNS name); `type Config struct{ Listen string; MOTD, Kick string; MaxConns, MaxPerIP int; Deadline time.Duration; WakeEvery time.Duration; Out io.Writer; Clock clock.Clock }`; `func Serve(ctx context.Context, ln net.Listener, c Config) error`. On a login attempt (`Next == 2`) it writes exactly one line `wake <host>\n` to `Out`, at most once per host per `WakeEvery` (10 s); status (`Next == 1`) answers the status JSON (`version.name "mc-panel"`, `protocol` echoed, `players {max 0, online 0}`, `description {"text": MOTD}`) and echoes a ping; login answers Login Disconnect (packet 0x00, JSON text component `{"text": Kick}`). Limits: packets > 1024 bytes, VarInts > 5 bytes, `Deadline` 5 s per connection, `MaxConns` 64, `MaxPerIP` 4; legacy `0xFE` closes the connection.
- Produces: `agent waker` subcommand: `run(ctx, ["waker", "-listen", ":25565"], …)` starts `waker.Serve` with the fixed texts from Global Constraints, refuses to run as uid 0, writes logs only to stdout, and reads no config file.

- [ ] **Step 1:** Tests: `TestReadHandshake` (table: valid 1.21 handshake; FML `"dev-a.example.com\x00FML3\x00"` → `dev-a.example.com`; `"DEV-A.example.com."` → `dev-a.example.com`; VarInt 6 bytes → error; length 1025 → error; truncated → error), `FuzzReadHandshake` (never panics, never allocates beyond 1 KiB per packet), `TestServeStatus` (net.Pipe client reads JSON with the MOTD and 0 players, ping echoed), `TestServeLoginWakes` (one `wake dev-a.example.com` line, client gets the disconnect text), `TestServeWakeDedup` (two logins within 10 s on a fake clock → one line), `TestServeLimits` (65th connection closed; 5th from one IP closed; idle client closed after the deadline), `TestServeLegacyPing` (closed, no line); `cmd/agent`: `TestWakerRefusesRoot` via an injectable uid func.
- [ ] **Step 2:** `make check` fails (package missing).
- [ ] **Step 3:** Implement the package and the subcommand (dispatch on `args[0] == "waker"` before the agent's flag parsing).
- [ ] **Step 4:** `make check`; `make test-vm SUITE=go` green (`go test ./internal/waker ./cmd/agent -race`).
- [ ] **Step 5:** Commit `feat(waker): minecraft wake listener`.

---

### Task 3: Agent waker container, routes and `PUT /v1/waker` (teammate `waker`)

**Files:**
- Create: `internal/agent/instances/waker.go`, `internal/agent/instances/waker_manager.go`, `internal/agent/instances/waker_test.go`, `internal/agent/server_waker.go`, `internal/agent/server_waker_test.go`
- Modify: `internal/agent/instances/router.go` (`Routes` gains the sleeping override), `internal/agent/instances/manager.go` (wiring), `internal/agent/server.go` (route), `internal/config/config.go` (none expected; only if a field is unavoidable, then also `deploy/config/agent.toml` via the lead)

**Interfaces:**
- Consumes: `agentapi.WakerRequest/Response/Validate` (Task 1), `waker` subcommand (Task 2).
- Produces: `const WakerName = "mcpanel-waker"`, `LabelWaker = "mcpanel.waker"`; `func WakerContainer(h Host, agentBinary string) (docker.ContainerConfig, error)`: image `h.Images.Router` (pinned), `Entrypoint ["/mcpanel-waker"]`, `Cmd ["waker", "-listen", ":25565"]`, bind `<agentBinary>:/mcpanel-waker:ro`, `User "65534:65534"`, network `RouterNetwork` only, no published ports, `CapDrop ALL`, `no-new-privileges`, read-only rootfs, memory 32 MiB (swap equal), `PidsLimit 32`, restart `unless-stopped`, `logConfig()`; template hash label includes the agent version so an update recreates it. `func RoutesWith(containers []docker.ContainerSummary, sleeping map[string]bool) map[string]string` (= `Routes`, then every FQDN in `sleeping` that `Routes` produced maps to `"mcpanel-waker:25565"`); `refreshRouter` uses it with the stored set. `func (m *Manager) SetWaker(ctx context.Context, req agentapi.WakerRequest) (agentapi.WakerResponse, error)`: validates, keeps only targets whose container exists with matching `mcpanel.fqdn` label and id, stores the set, ensures the waker container (created on the first non-empty set, kept afterwards), refreshes the router when the set changed, reads the waker container's logs since the last read (`ContainerLogs` with `since`, stdout only, at most 64 KiB per call), parses lines strictly `^wake ([a-z0-9.-]{1,253})$`, and returns the ids of sleeping targets whose FQDN matched (deduplicated). `Ready` is false when Docker or the waker container is not up, or on non-Linux builds. Before the first `PUT /v1/waker` since agent start, routes are built exactly as today.

- [ ] **Step 1:** Tests with the existing fake Docker of the package: `TestWakerContainerHardening` (every field above, golden), `TestRoutesWithSleeping` (sleeping FQDN → waker, unknown FQDN in set ignored, non-dev untouched), `TestSetWakerRejectsForeign` (target id whose container has another FQDN → dropped, not routed), `TestSetWakerParsesWakes` (log lines `wake dev-a.example.com`, `wake survival.example.com` (not sleeping), `garbage`, 300-char line → only dev-a's id), `TestSetWakerNoRequestNoChange` (fresh manager: routes equal `Routes(list)`), `TestWakerHandler` in `server_waker_test.go` (unknown field → 400, nil list → 400, body > 1 MiB → 413, peer UID check like the other endpoints).
- [ ] **Step 2:** `make check` fails.
- [ ] **Step 3:** Implement; register `PUT /v1/waker` in `internal/agent/server.go` beside `PathWebRoutes`.
- [ ] **Step 4:** `make check`; `make test-vm SUITE=go` and `SUITE=lint` green.
- [ ] **Step 5:** Commit `feat(agent): waker container and sleeping routes`.

---

### Task 4: Files service: restore from another server's repository (teammate `sync`)

**Files:**
- Modify: `internal/files/restore.go`, `internal/files/server_backups.go`, `internal/files/backupsvc.go` (if the job slot lives there)
- Test: `internal/files/restore_test.go`, `internal/files/server_backups_test.go`

**Interfaces:**
- Consumes: `filesapi.SnapshotRequest.SourceID` (Task 1).
- Produces: `RestoreBackup` honours `SourceID`: `SourceID == 0` → unchanged behaviour; `SourceID == ID` → `400 invalid`; otherwise `dump` reads repository `SourceID` with its key, the restore writes only into `<instances>/instances/<ID>` with the existing rules (reserve check, hidden entries kept, `rcon.password` of the target kept, journal under `ID`). The job slot of `ID` is taken; if `SourceID` has a running job the request answers `409` with the existing job-running code. The source's data directory is never opened. On Linux, the restic child of a restore-from gets nice 10 and I/O class idle right after start (`syscall.Setpriority`, `ioprio_set` on its pid; best effort, a failure is logged), so prod's disk keeps priority.

- [ ] **Step 1:** Tests: `TestRestoreFromRefusesSameID`, `TestRestoreFromOtherRepo` (fake restic: snapshot of server 1 restored into server 2; server 1's data directory byte-identical afterwards — compare a recursive sha256 listing; server 2 holds the snapshot's files and keeps its own `rcon.password`), `TestRestoreFromSourceBusy` (409), `TestRestoreFromMissingSourceRepo` (clean error, target untouched).
- [ ] **Step 2:** `make test-vm SUITE=go` shows the new tests failing.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `make check`; `make test-vm SUITE=go` green.
- [ ] **Step 5:** Commit `feat(files): restore a snapshot of another server`.

---

### Task 5: Backups queue: the dev sync job (teammate `sync`)

**Files:**
- Create: `internal/backups/devsync.go`, `internal/backups/devsync_test.go`
- Modify: `internal/backups/queue.go` (`restoreHeld` also for kind `dev_sync`), `internal/backups/backup.go` (`runBackup` takes the server id explicitly instead of `j.spec.serverID`), `internal/backups/jobs.go` (kind/step constants), `internal/backups/overview.go` (snapshot kind `dev-sync` passes through)

**Interfaces:**
- Consumes: `filesapi.BackupClient.RestoreFrom`, `SnapshotDevSync` (Task 1).
- Produces:
  ```go
  type DevSync struct {
      DevID, SourceID     int64
      DevName, SourceName string
      Trigger             string // "scheduled" | "manual" | "create"
      // Apply runs after a successful restore, before the gate opens (servers.ApplyDevSpec).
      Apply func(ctx context.Context) error
      // Done gets the snapshot used and the job's error (nil on success); never nil.
      Done func(snapshot string, err error)
  }
  func (h *Handler) QueueDevSync(ctx context.Context, s DevSync) error // errBackupRunning → 409 backup_running
  ```
  Job kind `dev_sync` keyed by `DevID`; while queued or running, `DataJobRunning(DevID)` is true. Steps: `snapshot` (reuse rule from Global Constraints via the source's snapshot list, else `runBackup(ctx, j, SourceID, SnapshotDevSync, "")` — its save-off/save-on path unchanged) → `stopping` (`h.stop(ctx, DevID)`) → `restoring` (`files.RestoreFrom(DevID, SourceID, snap)` + `waitService`) → `applying` (`Apply`) → `forget` (older `dev-sync` snapshots of `SourceID`; a failure here only logs). Without restic (`available: false`) the job fails with a fixed message. Deleting the dev cancels the job with `errServerDeleted` (existing path via `drop`). No `pre-restore` snapshot; no `backup.failed` notification for this kind (devinst notifies). Every `/servers/{id}/backups…` endpoint answers `409 dev_managed` ("dev instances have no backups") when `{id}` is a dev (admins bypass permissions, so the check is in the handler), and the schedule ticker skips devs.

- [ ] **Step 1:** Tests with the package's fakes: `TestDevSyncReusesFreshScheduled` (snapshot 2 h old, newer than last sync → no backup call), `TestDevSyncIgnoresManual` (only a manual snapshot → new `dev-sync` backup on the source), `TestDevSyncTooOld` (7 h → new backup), `TestDevSyncOrder` (calls in order: backup(source) → stop(dev) → RestoreFrom(dev, source) → Apply → forget(source, older dev-sync only); never `stop(source)`, never `RestoreBackup`), `TestDevSyncHoldsGate` (`DataJobRunning(dev)` true while queued and running, `DataJobRunning(source)` false), `TestDevSyncDevDeleted` (drop → `Done` with canceled error, no restore), `TestDevSyncNoRestic`, `TestDevSyncApplyFails` (`Done` gets the error, gate released), `TestBackupEndpointsRefuseDev` (create/list/schedule on a dev id → 409 `dev_managed`, also for an admin).
- [ ] **Step 2:** Tests fail on the VM.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `make check`; `make test-vm SUITE=go` and `SUITE=lint` green.
- [ ] **Step 5:** Commit `feat(backups): dev sync job`.

---

### Task 6: Servers, store and permissions (teammate `core`)

**Files:**
- Create: `internal/servers/dev.go`, `internal/servers/dev_test.go`
- Modify: `internal/servers/servers.go` (create: reserved prefix; update: dev restriction; delete: `dev_exists`), `internal/servers/power.go` (sleeping flag), `internal/servers/view.go` (`dev`, `dev_id`), `internal/store/queries/rbac.sql` + `internal/store/rbac.sql.go`, `internal/users/*.go` (refuse permission rows for a dev id), `internal/rbac/checker_test.go`

**Interfaces:**
- Consumes: `Dev…` queries (Task 1).
- Produces:
  ```go
  type DevOverrides struct{ ViewDistance, SimulationDistance int }
  func ApplyDevOverrides(prodName string, p instancespec.Properties, ov DevOverrides) instancespec.Properties
  func DevMemoryMiB(t instancespec.Type, maxMiB int) int
  var (ErrDevExists, ErrNotDev, ErrDevNameTooLong, ErrNoInstance, ErrBaseDomainMissing error)
  // CreateDev writes server, instance (prod's spec + overrides, desired stopped) and
  // dev_instances (sleeping, next_sync_at from sync_time) in one transaction.
  func (h *Handler) CreateDev(ctx context.Context, prodID int64, memoryMiB *int) (store.Server, error)
  // ApplyDevSpec copies prod's type/version/loader/modpack/java/properties onto the dev,
  // applies the overrides stored in dev_instances, keeps the dev's memory, bumps revision.
  func (h *Handler) ApplyDevSpec(ctx context.Context, devID int64) error
  func (h *Handler) DevView(ctx context.Context, devID int64) (serverJSON, error) // for 201 of POST /dev
  // SetDevSleeping sets dev_instances.sleeping and desired_state (sleeping → stopped,
  // awake → running) under the reconcile lock, then triggers a reconcile pass.
  func (h *Handler) SetDevSleeping(ctx context.Context, devID int64, sleeping bool, now time.Time) error
  ```
  `ApplyDevSpec` also replaces the dev's `plugin_installs` rows with copies of prod's (new query `PluginsCopyInstalls(source, target)` in `internal/store/queries/plugins.sql`, owned by `core` for this feature).
  `ApplyDevSpec` reads prod's row and writes only the dev's row (it takes the reconcile lock like other spec writes). Power: `stop` on a dev sets `sleeping = 1`; `start`/`restart` sets `sleeping = 0` and `last_wake_at = NULL`. `PATCH /servers/{id}` on a dev: only `memory_mib` allowed, any other present field → `409 dev_managed`. `DELETE` of a prod with a dev → `409 dev_exists`; delete of a dev works like any server (its `dev_instances`/`dev_ops` rows cascade) and additionally records `dev.delete`. Create with a name `dev-…` → `400 validation_failed`.
- Produces (rbac SQL): `RbacHasPermission` resolves `server_id` through `dev_instances` (`COALESCE((SELECT source_id FROM dev_instances WHERE server_id = ?), ?)`) and returns 0 for `server.backups` on a dev; `RbacListViewableServers` also lists devs whose source the user may view; `RbacListUserPermissions` / `RbacListUserServerPermissions` add the dev's id with the prod's permissions minus `server.backups`. Users handler: setting a permission on a dev id → `400 validation_failed`; the admin user editor's server list excludes devs (API field `dev` is enough for the UI).

- [ ] **Step 1:** Tests: `TestApplyDevOverrides` (prod motd nil → `"[DEV] survival"`; 150-rune motd → truncated to 150; view/sim from overrides; gamemode unchanged), `TestDevMemoryMiB` (paper 2048, fabric 3072, modrinth 3072, max 1536 → 1536), `TestCreateDev` (name `dev-survival`, 29-char prod → `ErrDevNameTooLong`, second dev → `ErrDevExists`, dev of a dev → `ErrDevExists`, `dev-survival` taken by a normal server → conflict, prod row unchanged byte-for-byte), `TestApplyDevSpecOnlyWritesDev` (prod `instances` row and revision unchanged; dev revision +1; dev memory kept), `TestCreateServerReservedPrefix`, `TestUpdateDevOnlyMemory` (409 `dev_managed` for `version`, ok for `memory_mib`), `TestDeleteProdWithDev` (409 `dev_exists`), `TestPowerStopSetsSleeping`, `TestDevPermissionsFollowProd` in `internal/rbac/checker_test.go` (user with `server.files` on prod → true on dev; `server.backups` → false on dev; no rows on dev itself needed), `TestUsersRefuseDevPermission`, `TestApplyDevSpecCopiesPluginInstalls`, `TestSetDevSleeping` (sleeping → desired stopped; awake → running; prod untouched).
- [ ] **Step 2:** Tests fail on the VM.
- [ ] **Step 3:** Implement; `make generate` for `rbac.sql.go`.
- [ ] **Step 4:** `make check`; `make test-vm SUITE=go` green.
- [ ] **Step 5:** Commit `feat(servers): dev instances in servers, store and permissions`.

---

### Task 7: Monitoring hook and alerts (teammate `core`)

**Files:**
- Modify: `internal/monitoring/handler.go` (`Deps.OnPass`), `internal/monitoring/alerts.go`
- Test: `internal/monitoring/alerts_test.go`, `internal/monitoring/collector_test.go`

**Interfaces:**
- Produces: `Deps.OnPass func(ctx context.Context, s Snapshot)` (nil = none), called after `alerts.Evaluate` on every pass with the same snapshot. For every server with a `dev_instances` row, `evalDown` and `evalCrash` behave as with down alerts off (no `server.down`, `server.recovered`, `server.crash_loop`, and existing firing state for it is dropped).

- [ ] **Step 1:** Tests: `TestOnPassCalledWithSnapshot`, `TestNoDownAlertForDev` (dev stopped with desired running → no event), `TestNoCrashLoopForDev`, `TestProdAlertsUnchanged`.
- [ ] **Step 2:** Fail on the VM.
- [ ] **Step 3:** Implement (`downAlertsOff` also returns true for dev ids).
- [ ] **Step 4:** `make check`; `make test-vm SUITE=go` green.
- [ ] **Step 5:** Commit `feat(monitoring): pass hook and no alerts for dev instances`.

---

### Task 8: `internal/devinst`: API, schedule, sleep, wake, ops (teammate `devinst`)

**Files:**
- Create: `internal/devinst/{api.go,schedule.go,sleep.go,wake.go,ops.go,deps.go}` and a `_test.go` per file
- Modify: `internal/devinst/handler.go` (from Task 1), `cmd/panel/app.go` (final step)

**Interfaces:**
- Consumes (declared in `deps.go` as consumer interfaces, satisfied later by the real types):
  ```go
  type Servers interface {
      CreateDev(ctx context.Context, prodID int64, memoryMiB *int) (store.Server, error)
      ApplyDevSpec(ctx context.Context, devID int64) error
      SetDevSleeping(ctx context.Context, devID int64, sleeping bool, now time.Time) error
  }
  type Syncer interface{ QueueDevSync(ctx context.Context, s backups.DevSync) error }
  // plus agentapi.WakerClient, agentapi.ConsoleClient (Rcon), servers.DataGate,
  // notify.Notifier, audit.Recorder, clock.Clock, *sql.DB
  ```
- Produces: `func (h *Handler) Run(ctx context.Context)` (minute ticker for due syncs and deferrals; 2-second wake loop), `func (h *Handler) OnPass(ctx context.Context, s monitoring.Snapshot)` (sleep detection and ops re-apply), `func (h *Handler) DataJobRunning(...)` is **not** needed (the backup queue holds the gate).
- Behaviour:
  - `POST /servers/{id}/dev`: `CreateDev`, audit `dev.create`, queue the first sync (trigger `create`, no player wait); errors map `ErrDevExists → 409 dev_exists`, `ErrDevNameTooLong`/validation → 400, `ErrBaseDomainMissing` → existing code, `ErrNoInstance → 409 conflict`.
  - `GET/PATCH /servers/{id}/dev`: `409 not_dev` on a non-dev; PATCH validates ranges, sets `paused_at` on pause, recomputes `next_sync_at` with `backups.NextRun(backups.Schedule{Mode: "daily", TimeOfDay: sync_time}, loc, now, last)`, calls `ApplyDevSpec` when an override changed, audits `dev.settings_update`.
  - `POST /servers/{id}/dev/sync`: if dev is running with players > 0 (last monitoring snapshot), send the 60-s warning and queue after 60 s; else queue now; `sync_pending` true until `Done`. `409 backup_running` passes through.
  - Schedule: each minute, for `DevListDue(now)`: if players > 0 and `now - next_sync_at < 2h` → warning `say`, `deferred_since`, `next_sync_at += 15m`; else (final) warning and queue. `Done`: `DevSetSyncResult`, next `next_sync_at` from `sync_time`, audit `dev.sync` (trigger, result, snapshot), on error notify `dev.sync_failed` unless one went out for this dev within 6 h. Paused devs: `next_sync_at` advances without a sync.
  - Sleep (`OnPass`): for a dev with `State == running`, not gated: `lastActive = max(StartedAt, last time Players > 0)`; if `Players != nil && *Players == 0 && now - lastActive >= idle_minutes` → `SetDevSleeping(devID, true, now)`. `Players == nil` never sleeps and does not update `lastActive`.
  - Ops (`OnPass`): when a dev sample has `Up == true` and a `StartedAt` not yet handled, send `op <name>` for every `dev_ops` row (names match `^[A-Za-z0-9_]{3,16}$`), remember the `StartedAt`. `POST …/dev/ops` stores and, if running, sends `op <name>`; `DELETE` removes and sends `deop <name>`; audits `dev.op_add`/`dev.op_remove`.
  - Wake loop (2 s): `Waker(ctx, {Sleeping: DevListSleeping as FQDNs})`; for each returned id: skip if gated or woken < 2 min ago or panel-wide cap (10/min) reached; else `SetDevSleeping(devID, false, now)`, audit `dev.wake` (system actor). `Ready == false` and agent errors are logged at debug and retried next tick.

- [ ] **Step 1:** Tests with fakes and `clock.Fake`: `TestCreateDevQueuesInitialSync`, `TestPatchPauseAndOverrides` (ApplyDevSpec called once; `paused_at` set), `TestNotDev409`, `TestSyncNowWarnsAndWaits60s`, `TestScheduledSyncDefersWhilePlayers` (15-min steps, forced after 2 h with final warning), `TestPausedSkips`, `TestSyncFailedNotifiesOncePer6h`, `TestSleepAfterIdle` (15 min of 0 players → sleeping, desired stopped), `TestSleepUnknownPlayersNeverSleeps` (Review Focus 5: 30 min of `Players == nil` → awake; then 0 players → sleeps only 15 min later), `TestManualStartIdleFromStart`, `TestOpsReappliedOncePerStart`, `TestWakeRateLimits` (2 min per dev, 10/min global), `TestWakeIgnoredWhileGated`, `TestWakeLoopResendsAfterRestart` (new handler with sleeping rows sends them on the first tick; Review Focus 4).
- [ ] **Step 2:** Fail on the VM.
- [ ] **Step 3:** Implement against the fakes.
- [ ] **Step 4:** After the lead has merged Tasks 3, 5, 6, 7 into `feat/dev-instances`: rebase, wire real deps in `cmd/panel/app.go` (`devinst.NewHandler(devinst.Deps{Servers: serversH, Syncer: backupsH, Waker: agentClient, Console: …})`, `monitoring.Deps.OnPass: devH.OnPass`, `go devH.Run(ctx)`), extend `cmd/panel/integration_test.go` with `TestDevInstanceLifecycle` (create → 201 with `dev`, list shows `dev_id` on prod, PATCH pause, delete prod 409, delete dev 204) using the existing fakes.
- [ ] **Step 5:** `make check`; `make test-vm SUITE=go` and `SUITE=lint` green. Commit `feat(devinst): dev instance api, schedule, sleep and wake`.

---

### Task 9: Frontend (teammate `web`)

**Files:**
- Create: `web/src/features/dev/{api.ts,dev-badge.tsx,dev-banner.tsx,dev-settings-section.tsx,dev-ops-section.tsx,create-dev-section.tsx}` with tests, `web/e2e/shots/dev.shots.ts`
- Modify: `web/src/index.css` (`--status-dev` in `:root` and `.dark`), `web/src/styles/tokens.test.ts`, `web/src/features/servers/sections.ts` (+ test), `web/src/features/layout/server-tree.tsx`, `web/src/features/layout/breadcrumb.tsx`, `web/src/features/servers/server-header.tsx`, `web/src/features/servers/server-settings-tab.tsx`, `web/src/features/servers/server-overview.tsx`, players area page, backups snapshot kind label, users permission editor (hide devs), `web/src/i18n/{de,en}.ts`, `web/src/mocks/handlers.ts` (richer fixtures)

**Interfaces:**
- Consumes: generated types `DevInstance`, `DevSummary`, `Server.dev`, `Server.dev_id`, `DevOps` (Task 1).
- Produces / behaviour (spec §8):
  - `DevBadge`: "DEV" in Silkscreen, colour `--status-dev` (AA in both modes), shown in tree, breadcrumb, server list, header.
  - Tree: devs are rendered directly under their prod (`dev.source_id`), not at top level.
  - `DevBanner` in the server header of a dev: "Kopie von <prod> – Änderungen werden beim nächsten Sync überschrieben (<relative day> <HH:mm>)" / paused: "Sync pausiert seit <date> – Dev behält seine Änderungen"; last result (`failed` with error text); "Schläft – verbinden zum Aufwecken" when `sleeping`; buttons "Jetzt syncen" (`server.files`) and the pause switch (`server.files`).
  - `serverSections(server, isAdmin)`: no `backups` for a dev.
  - Dev settings (`dev-settings-section`): sync time, idle minutes, view/simulation distance; memory stays in the normal settings form, whose other fields are hidden on a dev; danger zone "Dev-Instanz löschen".
  - Prod overview/settings: `create-dev-section` (admin) with memory field prefilled from `GET /servers/memory` rules (2048/3072) and the warning that disk use doubles; or a link to the existing dev.
  - Players area on a dev: `dev-ops-section` list with add/remove (`server.console`), name validated `^[A-Za-z0-9_]{3,16}$`.
  - Backups list of a prod labels kind `dev-sync` ("Dev-Sync").
  - Users editor: devs are not offered for permissions.

- [ ] **Step 1:** Tests: `sections.test.ts` (dev has no backups), `dev-banner.test.tsx` (running/paused/sleeping/failed texts in de), `dev-settings-section.test.tsx` (PATCH body), `dev-ops-section.test.tsx` (invalid name blocked), `server-tree` test (dev nested under prod), `tokens.test.ts` (`--status-dev` contrast).
- [ ] **Step 2:** Fail on the VM (`make test-vm SUITE=web`).
- [ ] **Step 3:** Implement; screenshots via `make test-vm SUITE=shots` (dark/light, desktop/mobile) and review them.
- [ ] **Step 4:** `make check`; `make test-vm SUITE=web` and `SUITE=lint` green; `SUITE=e2e` green.
- [ ] **Step 5:** Commit `feat(web): dev instances in the ui`.

---

### Task 10: Acceptance shard, CI and docs (lead)

**Files:**
- Create: `scripts/ci-instances/dev.sh`
- Modify: `.github/workflows/ci.yml` (shard `dev` with own files `^internal/(devinst|waker)/|^internal/agent/instances/waker`), `docs/dev/acceptance.md` ("Dev instances" section), `CLAUDE.md` (architecture: devinst, waker, spec link), `README.md` (feature list), the spec (status `implemented`, §6.2 records the chosen waker shape)

- [ ] **Step 1:** `dev.sh` (timescale 10): create prod (paper) with a world and a marker file, create dev; wait for the first sync; assert dev has the marker, `[DEV]` MOTD and view distance 4; record `find <prod dir> -type f -exec sha256sum` before a second sync, write a new marker on prod, sync now, assert dev has the new marker, prod's listing differs only in files the running prod wrote (world region/level/log files) and prod answered every ping during the sync; add a dev op, restart dev, assert `ops.json` contains it; wait for sleep (idle 5 min → 30 s at timescale 10), assert the container stopped and a status ping through mc-router returns the waker MOTD; send a login handshake, assert dev starts and a second login reaches the server; delete prod → 409; delete dev → 204.
- [ ] **Step 2:** Push; the `instances` job with shard `dev` fails before Tasks 2–9 are merged, passes after.
- [ ] **Step 3:** Docs updates.
- [ ] **Step 4:** `make test-vm SUITE=all` green; PR `feat/dev-instances` → `main`; `gh pr checks <n> --watch` until `CI OK`; `gh pr ready`.
- [ ] **Step 5:** Commit `test(dev): acceptance shard and docs`.
