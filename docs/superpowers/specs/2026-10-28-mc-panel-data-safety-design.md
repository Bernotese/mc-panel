# mc-panel: per-server data safety (design spec)

- **Date:** 2026-10-28
- **Status:** implemented (2026-10-28)
- **Author:** Jannes (with Claude)
- **Issues:** #145 (A02 stuck restore), #146 (A03 unknown job state), #147 (A04 atomic coordination); reused by #149 (A06) and #153 (A10)
- **Builds on:** files (`2026-10-11-…-files-design.md`), backups (`2026-10-12-…-backups-design.md`), dev instances (`2026-10-27-…-dev-instances-design.md`)

## 1. Goal

Today "may this server start, and may this data job run?" is answered by several in-memory checks that are taken separately from the action they protect:

- the panel's `servers.DataGate` (backup queue hold, extract pending marker, files `Job` lookup), which **fails open** when the files service does not answer (#146);
- the files service's in-memory job slots, which forget everything on restart, while a **stuck restore journal** on disk is consulted only by the next restore, not by starts (#145);
- a check-then-act gap between the gate and the agent: a start that the agent has already queued can still run after a restore saw the container "stopped", and plain file writes (editor, upload, rename, delete, plugin installs) never take the job exclusion a restore holds (#147).

This spec defines **one contract** for the safety of a server's data directory, owned where the data lives, and **one admission protocol** that every power action and data job goes through.

### 1.1 Guiding rules

1. **Fail closed.** Only a positive "safe" answer allows a start or a destructive data job. Unknown is never safe.
2. **Stop is always allowed.** Stopping a server, reading state, diagnostics and reads of files stay possible in every state.
3. **The data owner decides.** The files service knows best whether a data directory is being rewritten or is in an unrecovered state; the panel asks it and never infers safety from the absence of an answer.
4. **Recovery files are never deleted automatically**, and a stuck swap is never retried blindly.

### 1.2 Non-goals

- Strict consistency against the running Minecraft process itself: a service-side lock cannot stop Minecraft from writing a file. Where strict consistency matters (restore, extract), the server is stopped first, as today.
- Changing the backup/restore algorithm, the journal format's phases, or the agent's Docker handling beyond reporting its queue.
- Multi-host or distributed locking.

## 2. The data state contract

Every server's data directory has exactly one **data state**, computed by the files service:

| State | Meaning | Start / restart | Data jobs (restore, extract, dev sync target) | Plain file mutations | Stop, reads |
|---|---|---|---|---|---|
| `safe` | no job rewrites the directory, no unresolved journal | yes | yes | yes | yes |
| `busy` | a job holds the directory (`restore`, `extract`; or a dev sync's restore) | no | no (409) | no (409) | yes |
| `recovery_required` | a restore journal is `stuck`, unreadable, or could not be recovered at start | no | no | no | yes |
| `unknown` | *panel-side only*: the files service did not give a valid answer | no | no | — | yes |

Backups, forget, pin, purge and downloads do not rewrite the data directory and keep their current exclusions; they neither make a server `busy` nor are they blocked by plain file mutations.

### 2.1 Files service

- New endpoint `GET /v1/state?id=<id>` → `{"state": "safe"|"busy"|"recovery_required", "job": <Job|null>, "recovery": {"phase": "...", "since": <unix ms>}|null}`. It answers from the in-memory slots **and** the journal on disk, read on every call (one small file per server; the directory listing is cached for at most 1 s).
  - A journal entry that cannot be read or parsed makes that server `recovery_required` (today it is skipped). A journal directory that cannot be read at all makes the endpoint answer `503` for every id, which the panel maps to `unknown`.
  - `CleanupTemp` keeps the temp folder of every journal entry that exists on disk, readable or not (today an unreadable entry's folder can be removed).
- **Mutation lease.** `Write`, `Mkdir`, `Rename`, `Delete`, `Upload`, `UploadChecked` (and so every plugin install, toggle and removal) take a shared per-server lease for their whole duration (an upload holds it while it streams). Acquiring `restore` or `extract` waits for leases to drain up to 30 s and then answers `409 busy`; while `restore`/`extract` holds the server, a new lease is refused with `409 busy` at once. Lock order inside the service: `jobs.mu` (leaf, never held across I/O); the lease is taken before the instance root is opened.
- **Resolve.** New endpoint `POST /v1/recovery/resolve {id}` for a `recovery_required` server: it moves the restore temp folder (old and new halves) out of the data directory to `<instances_dir>/backups/recovery/<id>-<unix>` (owned by `mcpanel-mc`, never deleted by the service) and removes the journal entry. It does not touch the visible data directory. It refuses when the state is not `recovery_required`.

### 2.2 Agent

- `InstanceStatus` gains `queued: [<power action>…]` (the per-instance worker's pending queue, in order) next to `operation`. A container is **settled** when `operation` is `none` and `queued` is empty. Restore, extract and dev sync wait for "not running and settled" before they are admitted (§3), instead of looking at `state` alone.

### 2.3 Panel

- `servers.DataGate` is replaced by `servers.DataState(ctx, id) (State, error)`, implemented by one `DataStates` type that combines the panel's own holds (the backup queue's queued/running restore and dev sync, the extract request in flight) with the files service's `GET /v1/state`. A transport error, a timeout (2 s) or a 5xx gives `unknown`; there is no fallback to "safe". Without a configured files service (dev mode without it) the state is `safe`, because no data job can exist.
- Server views gain `data_state` (`safe|busy|unknown|recovery_required`) and `data_job` (kind) for everyone who can view the server.
- **Monitoring:** `busy` and `recovery_required` keep silencing `server.down` / `server.crash_loop` (a stopped server is expected). `unknown` does **not** silence them: a data-safety error must not hide a real outage. A new alert `server.recovery_required` fires once when a server enters that state (reminder every 24 h like the others) and resolves when it leaves it.
- **Resolve:** `POST /servers/{id}/recovery/resolve` (Admin, sudo, audited `server.recovery_resolve`) calls the files endpoint. The UI shows a recovery banner on the server with what happened, where the moved folders are, and the runbook link (`docs/install.md`, new section "A restore could not be completed"), and the resolve button with a confirmation that the operator checked the data directory.

## 3. Admission protocol

Every action that can start a container or rewrite a data directory is **admitted** under a per-server panel lock, `servers.Admission`, a keyed mutex:

| Action | Under the server's admission lock |
|---|---|
| start, restart (handler and reconcile `actStart`/restart path), devinst wake → start | `DataState == safe`, then send the agent call |
| restore, dev sync (restore into the dev), extract | mark the panel hold; stop the container; wait until "not running and settled" (agent, §2.2; existing stop timeout); call the files service, which takes its job slot (now `busy`) ; release the lock |
| delete | `DataState ∈ {safe, recovery_required}` (deleting a broken server is allowed; its recovery folder is kept and reported) |
| stop | no lock needed (always allowed) |

- **Lock order (panel):** `reconcile.mu` → `Admission(id)` → (`queue.mu` | extract pending) → files/agent I/O. The reconcile pass takes a server's admission lock with `TryLock` and skips that server for this pass when it is held, so a long stop-wait of a restore does not stall the pass for all servers. Handlers take it with a 5 s timeout and answer `409 conflict` ("another operation on this server is being admitted") on timeout.
- After the files service has accepted a job, the files state `busy` carries the exclusion; the panel lock is released. A panel restart therefore loses nothing that matters: a job that was admitted is visible as `busy`, a job that was not admitted never touched data.
- A files service restart cancels running jobs (as today); a restore in its commit phase is rolled forward/back by `RecoverRestores` or ends `recovery_required`.

## 4. Errors and API

- New error code `data_state_unknown` (`503`): "the files service did not report whether this server's data is safe; try again".
- New error code `recovery_required` (`409`): "a restore of this server could not be completed; resolve it first".
- `busy` keeps the existing codes (`409 conflict` for power, `409 backup_running` for the backup queue).
- OpenAPI: `ServerDataState` enum, `Server.data_state`, `Server.data_job`, the resolve operation; `filesapi`: `StateResponse`, `PathState`, `PathRecoveryResolve`; `agentapi.InstanceStatus.Queued`.

## 5. Tests that prove it

- **Barrier tests (race):** start vs. restore admission (the queued start in the agent fake must run before the restore's settled wait ends, never after); upload/editor save vs. restore (a save either completes before restore admission or gets 409, never lands in the restored tree); rename/delete vs. extract.
- **Fail-closed:** files timeout, transport error, 5xx and a files restart in the middle of a restore → no start, no data job; recovery after the next `safe` answer.
- **Persistence (A02):** a `stuck` journal blocks start, restart, reconcile start and data jobs across a panel restart and a files restart; resolve clears it and moves the folders; a failed backup without a journal blocks nothing.
- **End to end:** an in-process test with the real files service (`files.Service` on a temp dir with the fake restic) plus the panel handlers, spanning service state and panel gate; and the CI acceptance shard `backups` gains a check that a server with a planted `stuck` journal does not start and starts after resolve.

## 6. Implementation split (for the plan)

1. **files** (filesapi + internal/files): `/v1/state`, mutation lease, resolve, journal/cleanup fixes.
2. **agent** (agentapi + internal/agent/instances): `queued` in `InstanceStatus`.
3. **panel** (servers, backups, serverfiles, plugins callers, devinst, monitoring, OpenAPI): `DataState`, `Admission`, consumers, alert, resolve endpoint, error codes.
4. **web**: data state badge/banner, recovery banner with resolve, unknown warning.
5. **docs**: runbook "A restore could not be completed" in `docs/install.md`, acceptance checks.

#149 (A06) then reuses the mutation lease for commit-time version checks, and #153 (A10) the same lease for coherent reservation accounting.

## 7. Decisions for the reviewer

| Question | Proposal |
|---|---|
| Servers do not start while the files service is unreachable (`unknown`) — also after a host reboot until `mcpanel-files` is up. | ⭐ yes, fail closed; the files socket is socket-activated, so this is normally seconds. |
| Resolve moves the half-swapped folders out of the data directory instead of deleting them. | ⭐ yes; the operator deletes `backups/recovery/…` by hand. |
| `server.recovery_required` as a new notification. | ⭐ yes, operational queue like `backup.failed`. |
| Deleting a server in `recovery_required` is allowed. | ⭐ yes, the recovery folder stays and the response says where. |
