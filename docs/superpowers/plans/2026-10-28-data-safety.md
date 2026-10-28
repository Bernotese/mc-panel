# Per-server Data Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. In this repo the tasks go to agent-team teammates (CLAUDE.md, "Implementation with agent teams"). Steps use checkbox (`- [ ]`) syntax.

**Goal:** One fail-closed data state per server (`safe | busy | recovery_required`, panel-side `unknown`) owned by the files service, one per-server admission lock in the panel, a mutation lease in the files service and an explicit resolve for stuck restores (#145, #146, #147).

**Architecture:** The files service answers `GET /v1/state` from its job slots and the restore journal on disk and refuses plain file mutations while a restore or extract holds a server. The agent reports its queued power actions so "stopped" means "stopped and settled". The panel replaces `DataGate.DataJobRunning` (fail-open bool) with `DataStates.State` (fail-closed, with errors) and admits every start and data job under `servers.Admission`.

**Tech Stack:** Go (stdlib tests), SQLite unchanged (no migration), OpenAPI + generated TS, React.

**Spec:** `docs/superpowers/specs/2026-10-28-mc-panel-data-safety-design.md` (approved with D1–D4 as proposed). Read it first; § references point there.

## Global Constraints

- Fail closed: only `safe` admits start, restart, reconcile start, devinst wake, restore, dev sync, extract. Stop, reads, diagnostics and downloads are never blocked by the data state.
- Files `GET /v1/state` timeout in the panel: 2 s; transport error, timeout or 5xx → `unknown`. No configured files client → `safe`.
- Mutation lease: `restore`/`extract` wait up to 30 s for leases to drain, then `409 busy`; while they hold a server, a new lease → `409 busy` immediately.
- Resolve moves the restore temp folder to `<instances_dir>/backups/recovery/<id>-<unix>` and removes the journal entry; never deletes; refuses unless `recovery_required`.
- Error codes (exact): `data_state_unknown` (503), `recovery_required` (409). Audit action `server.recovery_resolve`. Notify event `server.recovery_required` (operational queue, reminder 24 h like other alerts).
- Panel lock order: `reconcile.mu` → `Admission(id)` → (`queue.mu` | extract pending) → files/agent I/O. Reconcile uses `TryLock` per server; handlers wait at most 5 s then `409 conflict`.
- Monitoring: `busy`/`recovery_required` silence `server.down`/`server.crash_loop`; `unknown` does not.
- Owner name in any file: "Jannes" only (CLAUDE.md).
- CLAUDE.md rules: `make check` on the Mac, `make test-vm` before reporting, stdlib Go tests, `clock.Clock`.

## Review Focus

1. **A queued start runs after a restore saw the container stopped.** Expected: impossible; restore waits for "not running and settled". Test: Task 4 Step 1 `TestRestoreWaitsForQueuedStart`.
2. **A file write lands inside a restore swap** (editor save, upload, plugin install, rename/delete of an input). Expected: either completed before admission or `409 busy`. Test: Task 2 Step 1 `TestLeaseVsRestore*`.
3. **Files service unreachable during reconcile after a panel restart.** Expected: no starts, servers stay as they are, alerts still fire; recovery when `safe` returns. Test: Task 4 Step 1 `TestReconcileUnknownDoesNotStart`.
4. **Unreadable journal entry / journal dir.** Expected: `recovery_required` for that id / `503` → `unknown` for all; temp folders kept. Test: Task 2 Step 1 `TestStateUnreadableJournal*`.
5. **Delete of a broken server.** Expected: allowed in `recovery_required`, recovery folder kept and reported; refused in `busy`/`unknown`. Test: Task 4 Step 1 `TestDeleteDataStates`.

---

## Team

| Teammate | Branch | Tasks | Owns |
|---|---|---|---|
| lead | `feat/data-safety` | 1, 7 | contracts, final docs |
| `files` | `feat/data-safety-files` | 2 | `internal/files/**` |
| `agent` | `feat/data-safety-agent` | 3 | `internal/agent/instances/{worker,manager}.go` (Status/queue only), `internal/agent/server*.go` if needed |
| `panel` | `feat/data-safety-panel` | 4, 5 | `internal/servers/**`, `internal/backups/**`, `internal/serverfiles/**`, `internal/plugins/**` (callers), `internal/devinst/**`, `internal/monitoring/**`, `cmd/panel/**` |
| `web` | `feat/data-safety-web` | 6 | `web/**` except `schema.d.ts` |

Teammates branch from `origin/feat/data-safety` after Task 1; the lead merges them and opens one PR to `main`.

---

### Task 1: Contracts (lead)

**Files:** `internal/files/filesapi/state.go` (create), `internal/files/filesapi/client.go` (+ `State`, `ResolveRecovery`), `internal/agent/agentapi/instances.go` (`InstanceStatus.Queued`), `internal/servers/datastate.go` (create: types only), `internal/httpx/errors.go`, `internal/audit/types.go`, `internal/notify/{notify,sender,dispatcher}.go`, `api/openapi.yaml` + `web/src/api/schema.d.ts` + `web/src/api/errors.ts` + i18n error strings + mock stubs, `cmd/panel/openapi_test.go` counts.

**Interfaces (produced):**
- filesapi: `PathState = "/v1/state"` (GET, query `id`), `PathRecoveryResolve = "/v1/recovery/resolve"` (POST `IDRequest`), `type DataState string` with `StateSafe "safe"`, `StateBusy "busy"`, `StateRecoveryRequired "recovery_required"`; `type StateResponse struct{ State DataState; Job *Job; Recovery *Recovery }`, `type Recovery struct{ Phase string; Since int64; Folder string }` (json snake_case); client `State(ctx, id) (StateResponse, error)`, `ResolveRecovery(ctx, id) (Recovery, error)`; both on a new interface `StateClient`.
- agentapi: `InstanceStatus.Queued []PowerAction \`json:"queued"\`` (never null), `func (s InstanceStatus) Settled() bool` (`Operation == OpNone && len(Queued) == 0`).
- servers: `type State string` (`StateSafe`, `StateBusy`, `StateRecoveryRequired`, `StateUnknown`), `type DataStates interface{ State(ctx context.Context, id int64) (State, error) }`.
- OpenAPI: `ServerDataState` enum; `Server.data_state` (required), `Server.data_job` (string|null); `POST /servers/{id}/recovery/resolve` (Admin, sudo) → `Recovery`; error codes `data_state_unknown`, `recovery_required`.
- Panel route registered with a stub answering `409 recovery_required` "not available yet"; view fields filled with `"safe"`/null until Task 4.

- [ ] Step 1: tests for `InstanceStatus.Settled`, filesapi request/response round trips, client happy/5xx paths.
- [ ] Step 2: `make check` fails.
- [ ] Step 3: implement contracts + stubs; `make generate`.
- [ ] Step 4: `make check`; VM `go`, `web`, `lint` green.
- [ ] Step 5: commit `feat(data-safety): contracts`; push `feat/data-safety`.

### Task 2: Files service (teammate `files`)

**Files:** `internal/files/state.go` (create), `internal/files/lease.go` (create), `internal/files/recovery.go` (create), `internal/files/{jobs,restore,restorejournal,cleanup,ops,transfer,extract,server}.go`, tests.

**Interfaces:** consumes Task 1 filesapi. Produces `(*Service) State(ctx, id) (filesapi.StateResponse, error)`; `(*jobs) lease(id) (release func(), err error)`; `(*Service) ResolveRecovery(ctx, id) (filesapi.Recovery, error)`; HTTP handlers for both paths (peer-UID check as all others).

- [ ] Step 1: tests — `TestStateSafeBusyRecovery`, `TestStateUnreadableJournalEntry` (→ recovery_required, temp kept by CleanupTemp), `TestStateUnreadableJournalDir` (→ 503), `TestStateSurvivesRestart` (stuck journal → recovery_required after `New`+`RecoverRestores`), `TestLeaseVsRestoreWaitsThenBusy` (lease held 31 s → restore 409 busy; released within 30 s → restore admitted), `TestLeaseRefusedDuringRestore` (Write/Upload/Rename/Delete/Mkdir → 409), `TestLeaseUploadHeldWhileStreaming`, `TestResolveMovesFolders` (journal removed, folders under backups/recovery, data dir untouched), `TestResolveRefusesWhenNotRecovery`, `TestBackupNotBlockedByLease`.
- [ ] Step 2: fail on the VM.
- [ ] Step 3: implement (journal read per call, directory listing cached ≤ 1 s; lease counter in `jobs` under `jobs.mu`; restore/extract acquisition waits with a 30 s deadline on a condition variable, never holding `jobs.mu` across I/O).
- [ ] Step 4: `make check`; VM `go` (-race), `lint` green.
- [ ] Step 5: commit, push, report.

### Task 3: Agent queue in status (teammate `agent`)

**Files:** `internal/agent/instances/{worker,manager}.go`, tests.

- [ ] Step 1: tests — `TestStatusShowsQueuedStartBehindStop` (stop running, start queued → `Operation=stopping`, `Queued=[start]`), `TestStatusSettledAfterQueueDrains`, JSON never `null` for `queued`.
- [ ] Step 2–4: implement in `Status` from `instanceState.pending` (copy under `m.mu`), VM `go` + `lint`.
- [ ] Step 5: commit, push, report.

### Task 4: Panel core: DataStates, Admission, consumers (teammate `panel`)

**Files:** `internal/servers/{datastate,admission,gate,power,reconcile,servers,view,dev}.go`, `internal/monitoring/{collector,alerts,types}.go`, `cmd/panel/app.go`, tests.

**Interfaces:** produces `servers.NewDataStates(files filesapi.StateClient, holds ...Hold) *DataStatesImpl` where `type Hold interface{ Holding(id int64) (kind string, ok bool) }` (backups queue, extract pending); `servers.Admission` with `Lock(ctx, id) (unlock func(), err error)` (5 s, `ErrAdmissionBusy`) and `TryLock(id) (unlock func(), ok bool)`; `(*Handler) Admit(ctx, id, fn func(ctx) error) error` helper used by backups/serverfiles/devinst.
- `servers.DataGate`/`AnyGate` are removed; every consumer switches to `DataStates`.

- [ ] Step 1: tests — `TestDataStatesFailClosed` (timeout, transport error, 5xx → unknown), `TestStartRefusedUnlessSafe` (handler: busy → 409 conflict, recovery → 409 recovery_required, unknown → 503 data_state_unknown), `TestReconcileUnknownDoesNotStart`, `TestReconcileSkipsLockedServer`, `TestRestartAdmittedUnderLock` (barrier: a restore admission in progress blocks restart), `TestDeleteDataStates`, `TestMonitoringUnknownNotSilenced`, `TestRecoveryAlertFiresAndResolves`, `TestServerViewDataState`.
- [ ] Step 2–4: implement; wire in `cmd/panel/app.go` (holds from backups and serverfiles); VM `go` (-race) + `lint`.
- [ ] Step 5: commit, push, report.

### Task 5: Panel data jobs adopt admission (teammate `panel`, after Task 4)

**Files:** `internal/backups/{restore,devsync,backup}.go`, `internal/serverfiles/extract.go`, `internal/devinst/{wake,api}.go`, `internal/servers/recovery.go` (resolve endpoint), tests.

- [ ] Step 1: tests — `TestRestoreWaitsForQueuedStart` (fake agent: queued start behind stop; restore admits only after `Settled`), `TestRestoreAdmittedOnlyWhenSafe`, `TestDevSyncUsesAdmission`, `TestExtractUsesAdmission`, `TestWakeRefusedUnlessSafe`, `TestResolveEndpoint` (admin + sudo, audited, 409 when not recovery), `TestEndToEndStuckRestoreBlocksStart` (in-process real `files.Service` on a temp dir with fake restic + panel handlers: stuck journal → start refused across a panel restart → resolve → start ok).
- [ ] Step 2–4: implement; VM `go` (-race) + `lint`.
- [ ] Step 5: commit, push, report.

### Task 6: UI (teammate `web`)

**Files:** `web/src/features/servers/{server-header,data-state-banner}.tsx`, recovery dialog, i18n, mocks.

- [ ] Step 1: tests — banner per state (busy: "Ein Auftrag schreibt gerade die Daten um"; unknown: "Datenzustand unbekannt – der Files-Dienst antwortet nicht"; recovery: explanation, moved-folder path after resolve, runbook link, resolve button admin-only with confirmation checkbox "Ich habe das Datenverzeichnis geprüft"), power buttons disabled with reason, sudo retry.
- [ ] Step 2–4: implement; VM `web`, `lint`, `e2e`; screenshots reviewed.
- [ ] Step 5: commit, push, report.

### Task 7: Acceptance, docs (lead)

**Files:** `scripts/ci-instances/backups.sh` (plant a `stuck` journal for a stopped server → start 409 recovery_required → resolve → start ok), `docs/install.md` ("A restore could not be completed" runbook), `docs/dev/acceptance.md`, `CLAUDE.md` (gate → data state paragraph), spec status.

- [ ] Steps: shard change, docs, VM `all`, PR to `main` with "Closes #145, closes #146, closes #147", CI green, ready.
