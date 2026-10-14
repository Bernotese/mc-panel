# mc-panel – Sub-project 4: Backups (design spec)

- **Date:** 2026-10-12
- **Status:** implemented (PRs #52–#57, 2026-10-13); offsite backends deferred
- **Author:** Jannes (with Claude)
- **Builds on:** `2026-10-08-mc-panel-foundation-design.md` (sub-project 1), `2026-10-10-mc-panel-instances-design.md` (sub-project 2), `2026-10-11-mc-panel-console-players-design.md` (sub-project 3a), `2026-10-11-mc-panel-files-design.md` (sub-project 3b), `2026-10-12-mc-panel-plugins-design.md` (sub-project 3c)

## 1. Goal and context

Sub-project 4 adds backups of the server data directories: manual and scheduled, with tiered retention, consistent while the server is running, and with a full restore. In this sub-project the backups live **only locally** on the VPS (restic repos); an offsite target is explicitly deferred. So that a world can still get off the VPS, every snapshot can be downloaded as a zip.

### 1.1 Success

A user with `server.backups` creates a backup for a server with an optional note, sets a schedule (every N hours, daily or weekly at a time of day) and a retention (last / daily / weekly / monthly), pins important scheduled snapshots against the retention, downloads a snapshot as a zip and rolls the server back to a snapshot. While the backup of a running server is being made, the server does not save; this makes the snapshot consistent. Failed scheduled backups and restores are reported through the existing notifications (Discord, ntfy).

### 1.2 Guiding principle

Sub-project 1 §3 still applies: a compromised panel does not gain root. Sub-project 3b §1.2 adds: data controlled by a Minecraft server is **never** touched by a root process. Backups go further: restic itself **never** reads or writes in the data directory. The file service `mcpanel-files` (user `mcpanel-mc`) produces a tar stream from its `os.Root` for a backup and extracts a tar stream for a restore with the same rules as zip extraction. A running, possibly malicious server can therefore neither smuggle another server's data into its snapshot by swapping in a symlink during the backup, nor write outside its directory during a restore.

### 1.3 Non-goals (sub-project 4)

- Offsite backends (S3, SFTP, …) and `restic copy`; a later sub-project. Then restic execution moves into a separate service with network access, and the file service stays without network.
- Restoring individual paths, a snapshot browser, restoring into a new server.
- Backup of the panel database.
- Cron expressions, several schedules per server, a time zone per server.
- Throttling beyond the unit's `IOWeight`/`CPUWeight`; quotas for the repo size.
- Uploading a backup (import); to bring in a world, the zip extraction from sub-project 3b remains the way.

## 2. Decisions

| Area | Decision | Reason |
|---|---|---|
| Tool | restic ≥ 0.17 as an external binary (CI/VM: 0.19.1, pinned by SHA-256) | Deduplication across snapshots (one world × many days stays small), encryption and checksums, later offsite targets without a new format; restic is not meant to be used as a Go library |
| Executing process | The file service `mcpanel-files` starts restic as a child process | No new service; same user, same hardening, no network; the service already has `os.Root`, jobs, extraction and zip logic |
| Data path | Backup = tar stream from the service → `restic backup --stdin`; restore/download = `restic dump` → tar stream → service | restic never opens paths in the data directory (§1.2); a restore only writes through the checked extraction logic |
| Repos | One repo per server: `<instances_dir>/backups/repos/<id>`, password `backups/keys/<id>` | Isolated locks (a prune of A does not block B), deleting a server = deleting the repo; server IDs are never reused (migration 00003) |
| Source of truth | The snapshot list always comes from restic; kind, pin and note are restic tags | As with plugins (sub-project 3c): the disk is authoritative, the DB only holds the schedule and the last run |
| Consistency | Running server: `save-off`, `save-all flush`, backup, then always `save-on` (RCON through the agent) | Standard procedure without a stop; if RCON fails, no snapshot is made |
| Restore | Whole state only; stop the server, safety snapshot `pre-restore`, extract, swap the top level, restart through the reconcile loop | Easy to verify; the safety snapshot makes every restore reversible |
| Schedule | Per server `off`/`interval`/`daily`/`weekly`, global time zone | Easy to use and to test; cron would be error-prone |
| Retention | `restic forget --keep-*` only for `kind=scheduled`, with `--keep-tag pinned` | Manual and pre-restore snapshots never disappear automatically |
| Orchestration | Panel package `internal/backups`: ticker, queue, jobs, RCON steps | Only the panel knows schedule, permissions, actual state and RCON |

### 2.1 Rejected alternatives

- **Separate service `mcpanel-backup`:** clean separation and ready for offsite, but the `os.Root` tar logic would have to exist twice or the panel would have to couple two services by stream; one more binary, unit, socket and configuration for "local only".
- **restic in a container, started by the agent:** the root agent would have to orchestrate restore targets and the directory swap (contradicts sub-project 3b §1.2).
- **restic reads the data directory directly (`restic backup <dir>`):** restic does not follow symlinks, but between reading a directory and opening it a running server can swap a folder for a symlink; another server's content would then be in the snapshot and readable through the download. Restoring directly into the data directory would have the same problem when writing.
- **Own tar.zst archives without restic:** no external dependency, but no deduplication (7 daily states of a 5 GiB world = 35 GiB) and an own format for later offsite targets.
- **Stop the server for every backup:** consistent, but downtime for players on every run.

## 3. Architecture

```
Browser ─/api/servers/{id}/backups/*─▶ panel: internal/backups
                                        · permissions (server.backups), CSRF, audit, sudo (restore, delete)
                                        · jobs per server, queue, schedule ticker (clock.Clock)
                                        ├─▶ agent (root): RCON save-off/save-all flush/save-on, power stop, actual state
                                        └─▶ mcpanel-files (mcpanel-mc): /v1/backups/*
                                              tar via os.Root ⇄ restic (child process, no network)
                                              /srv/mc-panel/backups/repos/<id>
```

- **panel:** new package `internal/backups` (handler, routes, schedule, queue, jobs, audit, notification); client methods and wire types in `internal/files/filesapi` (shared with the service).
- **Service:** new files in `internal/files` (restic execution behind an interface, tar creation, tar extraction, swap, tar→zip, jobs of kinds `backup|restore|forget`).
- **agent:** only change: `ensureLayout` creates `<instances_dir>/backups` (§3.1). No new agent API; RCON and power already exist.

### 3.1 File system

```
/srv/mc-panel/                     root:root 0755   (instances_dir)
├── instances/<id>/                mcpanel-mc 0750  (unchanged)
├── backups/                       mcpanel-mc 0700  (new, created by the agent)
│   ├── repos/<id>/                the server's restic repo
│   ├── keys/<id>                  repo password (32 random bytes, base64), 0600
│   └── restore-journal/<id>       restore journal (§4.3): only during the swap or when `stuck`
└── deleted/                       unchanged
```

- The agent creates `backups/` in `ensureLayout` with owner `mcpanel-mc` and mode 0700 (like `instances/<id>`) and corrects owner/mode if they differ; it never touches the content. The service creates `repos/`, `keys/` and `restore-journal/` itself.
- No container sees `backups/`; the panel (`mcpanel`) has no access.
- A repo is created at a server's first backup (`restic init`); before that the snapshot list is empty.
- **Key:** for a local repo the password does not protect against the host itself; it is needed because restic always encrypts, and it makes later offsite copies safe. Without `keys/<id>` the repo is unreadable: the operations docs say that `backups/keys/` belongs in every backup of the host.
- File system: in production `backups/` is on the same file system as `instances/` (a requirement for the installer, sub-project 6); a restore extracts into the data directory and needs space there (§4.3).

### 3.2 Configuration and systemd

New keys in `files.toml` (with defaults; unknown keys are still rejected):

```toml
restic_binary = "/usr/bin/restic"   # Dev: "restic" (looked up in PATH)
[limits]
max_backup_bytes   = 68719476736    # 64 GiB per snapshot (tar content)
max_backup_entries = 1000000
max_backup_jobs    = 1              # concurrent restic jobs in the service (backup, restore, forget)
```

- At start the service runs `restic version` (deadline 10 s). If the binary is missing or the version is < 0.17, the backup endpoints are off: `GET /v1/backups/status` reports `{available: false, reason}`, all other `/v1/backups/*` except `purge` answer `503 unavailable` (`purge` only deletes directories and does not need restic). The rest of the service runs normally (also on the Mac without restic).
- `mcpanel-files.service`: `ReadWritePaths` extended by `<instances_dir>/backups`, `MemoryMax=1G` (instead of 512M; restic keeps the index and pack buffers in memory), `TasksMax=512`, new `IOWeight=50` and `CPUWeight=50`. `RestrictAddressFamilies=AF_UNIX` stays: restic needs no network for local repos. (Now: the installer writes this unit from `deploy/systemd/mcpanel-files.service` and sets `restic_binary` in `files.toml` to the restic it found.)
- `ensureLayout` in **dev mode**: the dev file service creates `<instances_dir>/backups` itself if it is missing (like `instances/<id>` in sub-project 3b §3.1).

### 3.3 restic calls

- Fixed argv without a shell, through an interface (`resticRunner`) that the tests replace with a fake. The environment is **empty** except for `RESTIC_REPOSITORY=<backups>/repos/<id>`, `RESTIC_PASSWORD_FILE=<backups>/keys/<id>`, `HOME=<backups>`, `TZ=<backup_timezone>` (for `backup` and `forget`: restic stores the snapshot time in the backup process's zone and derives the daily/weekly/monthly groups of `forget` from it, §5.4) and `RESTIC_PROGRESS_FPS=1`. Every call with `--no-cache` (local repo) and `--json` where available.
- Before every job `restic unlock` (removes only stale locks of a crashed run). The snapshot list is read with `--no-lock` so that it also works during a prune.
- `backup --stdin --stdin-filename data.tar --host mcpanel --tag mcpanel --tag kind=<kind> [--tag note=<base64url>]`; the result (`snapshot_id`, `total_bytes_processed`) comes from the JSON summary.
- `snapshots --json --no-lock --host mcpanel`; only snapshots with the tag `mcpanel` count.
- `dump <snapshot> /data.tar` returns the tar on stdout.
- `tag --set …` for pin/unpin; restic rewrites the snapshot, and the **ID changes**. The service then reads the new ID from `snapshots` (same time, same kind).
- `forget --prune --max-unused 10% --host mcpanel --group-by host …` (§5.4), or `forget --prune <ids…>` for deleting single snapshots.
- restic's error output (stderr, cut to 4 KiB) goes to the log; `job.error` only gets a fixed short message plus restic's last line without host paths (as in sub-project 3b §4).
- Snapshot IDs from requests: exactly 64 characters `[0-9a-f]`; the service checks that the ID is in this repo's list before passing it to restic (otherwise `404 not_found`).

## 4. Service `mcpanel-files`

### 4.1 New endpoints

As before fixed paths, `DisallowUnknownFields`, errors `{error, code}`.

| Method, path | Body / query | Response |
|---|---|---|
| `GET /v1/backups/status` | – | `{available, version, reason}` |
| `POST /v1/backups/list` | `{id}` | `{snapshots: [{id, short_id, time, kind, pinned, note, bytes}]}`; newest first; without a repo `[]` |
| `POST /v1/backups/create` | `{id, kind, note, timezone}`; `kind` ∈ `manual\|scheduled\|pre-restore` | `202 {job}` |
| `POST /v1/backups/restore` | `{id, snapshot}` | Synchronous pre-check (snapshot exists, space §4.3), then `202 {job}` |
| `POST /v1/backups/forget` | `{id, snapshots}` **or** `{id, policy: {keep_last, keep_daily, keep_weekly, keep_monthly}, timezone}` | `202 {job}` |
| `POST /v1/backups/pin` | `{id, snapshot, pinned}` | `{snapshot}` (with the new ID); only `kind=scheduled`, otherwise `400 invalid` |
| `GET /v1/backups/download` | Query `id`, `snapshot` | Zip stream (§4.4) |
| `POST /v1/backups/purge` | `{id}` | `204`; deletes `repos/<id>` and `keys/<id>` |
| `POST /v1/jobs/cancel` | `{id}` | `204`; cancels the instance's running job (§4.5) |

- `GET /v1/jobs` (sub-project 3b) returns all job kinds. `filesapi.Job` gets `kind` (`extract|backup|restore|forget`), `snapshot` (result or source), `skipped` (skipped entries) and `code` (wire code of the error of a `failed` job, e.g. `too_large`, `insufficient_storage`); existing fields stay, extract jobs set `kind=extract`.
- One job per instance across all kinds (extract, backup, restore, forget exclude each other); `pin` and `purge` need an instance without a job (otherwise `429 busy`). In total at most `max_backup_jobs` restic jobs and still two extract jobs.
- `create` needs `instances/<id>` (otherwise `404 not_found`); `restore` does not create it.
- `note`: at most 64 characters, valid UTF-8, no control characters; as tag `note=<base64url without padding>`. `pinned` = tag `pinned`.
- New service error codes: `unavailable` (503, restic missing). Otherwise the existing ones: `not_found`, `busy`, `invalid`, `too_large`, `insufficient_storage`.

### 4.2 Backup job

- Walk over `os.Root(instances/<id>)` in a fixed order (names sorted), like the folder zip download (sub-project 3b §4): `Lstat` before every open, `Stat` + `os.SameFile` afterwards, open with `O_NONBLOCK`. Symlinks, special files, hidden entries (`.mcpanel-*` on every level, `.rcon-cli.*` at the top) and names that violate the path rules (`filesapi.CleanPath`) are skipped and counted in `skipped`.
- tar in PAX format, only folders and regular files, name relative to the root, mode `& 0777`, mtime; no owners (uid/gid 0, empty names).
- `server.properties` (top level) is redacted as for the download (`rcon.password=********`); itzg rewrites the password at start (sub-project 3b §3.4).
- A file that grows or shrinks while it is read is written with the size from the `Stat` (truncated or padded with zeros, `skipped` unchanged); with a stopped server or with `save-off` this does not happen to world data.
- Limits: more than `max_backup_entries` entries or `max_backup_bytes` of content → job `failed` (`too_large`), restic is aborted, no snapshot is made (restic only creates it at the end of the stream).
- Before the start: free space on `backups/` ≥ `reserve_bytes` (sub-project 3b), otherwise `507`. Because of deduplication the service cannot calculate further in advance; if the disk fills up anyway, restic fails and the job is `failed`.
- Progress: `bytes`/`entries` (tar content read).

### 4.3 Restore job

1. Pre-check in the request: the snapshot belongs to the repo; free space on `instances/<id>` ≥ the snapshot's `bytes` + `reserve_bytes` (otherwise `507`).
2. Temp folder `instances/<id>/.mcpanel-tmp-<token>-<random>/new` via `root.Mkdir`; `restic dump` → tar reader → extraction with the rules from sub-project 3b §4.2: path rules, no hidden names, only folders and regular files (other tar types → job `failed`), no duplicate names, bytes and entries re-counted against `max_backup_bytes`/`max_backup_entries`, files 0644 (0755 if an execute bit is set in the tar), folders 0755, the service's umask 0027 applies on top, mtime from the tar. Error → delete the temp folder, data directory unchanged.
3. Swap of the top level (only `rename` through the `os.Root`, no copying): all visible entries of the data directory to `<tmp>/old/`, then all entries from `<tmp>/new/` to the top, then delete `<tmp>` recursively. Hidden entries (`.rcon-cli.*`, `.mcpanel-*`) stay in place.
4. Cancelling (§4.5) only works until the swap begins; the swap itself runs through without a cancellation point. Only during the renames the service keeps a restore journal in `backups/restore-journal/<id>` (outside the data directory that the container controls; content: `<tmp>` and the phase `old_moving`, `new_moving` or `stuck`, each written atomically). If the service dies in the middle of the swap, it finishes the renames synchronously at the next start before it accepts requests: for `old_moving` backwards (the previous state comes back up from `<tmp>/old`), for `new_moving` forwards (the rest from `<tmp>/new`; the snapshot was fully checked). After that `<tmp>` (temp cleanup, sub-project 3b §3.3) and the journal entry disappear. A temp folder without a journal entry is an ordinary leftover and is only deleted, never rolled back.
   - **Phase `stuck`:** if the swap fails and the swap back fails too (or the recovery at start), `<tmp>` stays with the previous state in `old/`, the temp cleanup leaves it alone, and the entry stays with phase `stuck`. The service rejects new restores of this server until an operator has resolved the case (runbook in `docs/dev/README.md` ("Backups"): read the log, check `<tmp>`, move the contents back by hand, then delete the entry). Deleting the server with `purge_backups` also removes the entry.
   - The panel's pre-restore snapshot (§5.5) remains the fallback.

### 4.4 Download

- `restic dump` → tar reader → zip stream (`application/zip`, the panel sets the filename `<server>-<time>.zip`), with the same exclusions for names that cannot be stored safely in a zip as the folder zip download (sub-project 3b §4). The tar already contains only folders and regular files and a redacted `rcon.password`.
- Counts towards the 4 concurrent folder zips (sub-project 3b §4.1); if a `forget` job runs for the instance → `429 busy` (prune holds an exclusive lock). A download is possible during a backup.
- Deadlines as for the folder zip: 60 s per read/write, no overall deadline.
- Name limit: the zip stream takes over the folder zip's limit of 16 MiB for all path names together (sub-project 3b). Backup and restore allow 64 MiB; a snapshot with more than 16 MiB of visible path names can therefore be restored but not downloaded as a zip (the download fails with `too_large`).

### 4.5 Cancelling and shutdown

- `POST /v1/jobs/cancel` cancels `backup` and `forget` jobs immediately (restic gets `SIGINT`, `SIGKILL` after 10 s; a cancelled backup leaves no snapshot, a cancelled prune only unused packs that the next prune removes) and `restore` jobs until the swap begins. Nothing changes for extract jobs (`400 invalid`).
- At shutdown the service cancels running jobs as before (`svc.Close`), including restic children; a running swap is still finished.

## 5. panel

### 5.1 Data model (migration 00006)

```sql
CREATE TABLE backup_schedules (
    server_id        INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    mode             TEXT    NOT NULL DEFAULT 'off' CHECK (mode IN ('off','interval','daily','weekly')),
    interval_hours   INTEGER NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 168),
    time_of_day      INTEGER NOT NULL DEFAULT 240 CHECK (time_of_day BETWEEN 0 AND 1439), -- minutes
    weekday          INTEGER NOT NULL DEFAULT 0 CHECK (weekday BETWEEN 0 AND 6),          -- 0 = Sunday
    keep_last        INTEGER NOT NULL DEFAULT 24,
    keep_daily       INTEGER NOT NULL DEFAULT 7,
    keep_weekly      INTEGER NOT NULL DEFAULT 4,
    keep_monthly     INTEGER NOT NULL DEFAULT 6,
    next_run_at      INTEGER,          -- NULL when mode = 'off'
    last_run_at      INTEGER,
    last_status      TEXT CHECK (last_status IN ('ok','failed','running')),
    last_error       TEXT,
    last_snapshot_id TEXT,
    saveoff_since    INTEGER,          -- set while the panel has sent save-off
    updated_at       INTEGER NOT NULL
);
ALTER TABLE instance_settings ADD COLUMN backup_timezone TEXT NOT NULL DEFAULT 'Europe/Berlin';
```

- A row is created on the first read or write (defaults as above); `keep_*` 0–1000, with `mode ≠ off` at least one value > 0. `last_*` describes the last **scheduled** run.
- Queries in `internal/store/queries/backups.sql`, prefix `Backups…`.
- `backup_timezone` is an IANA name, checked with `time.LoadLocation`; the panel embeds `time/tzdata` (static binary, hosts without zoneinfo).

### 5.2 Schedule

- Ticker every minute through `clock.Clock`. Every row with `mode ≠ off` and `next_run_at ≤ now` is due; it goes into the queue (§5.3), unless the server already has a waiting or running job (then check again at the next tick).
- `next_run_at` is recomputed when the schedule is saved, when the time zone changes, and after every scheduled run (success or failure):
  - `interval`: start of the last run + N hours; without a previous run now + N hours.
  - `daily`: next point in time `time_of_day` after now in the time zone.
  - `weekly`: next `weekday` + `time_of_day` after now in the time zone.
  - Daylight saving time: if the time falls into a gap, the normalisation of `time.Date` applies (02:30 → 03:30); repeated hours count once (earliest point in time).
- Missed runs (panel was off): at start `next_run_at` is in the past, the run starts once, and then the next time is computed from now. Several missed runs are not caught up.
- Servers whose instance is missing or whose data directory does not exist yet (never started) run as `failed` with an understandable error, without a notification.

### 5.3 Queue and jobs

- One job per server in memory: `{id, kind: backup|restore|delete, trigger: manual|scheduled, state: queued|running|done|failed, step, bytes, snapshot, error, queued_at, started_at, finished_at}`; the last result stays until the next job or a panel restart.
- `step` ∈ `queued`, `saving` (save-off/flush), `backing_up`, `resuming` (save-on), `pruning`, `stopping`, `pre_restore`, `restoring`, `deleting`.
- **Panel-wide at most one job runs at a time** (FIFO queue across all servers); manual and scheduled jobs share it. A second job for the same server → `409 backup_running`.
- The panel polls the service job every 2 s (`GET /v1/jobs`) and mirrors `bytes` into its own job.
- At shutdown the panel cancels the running service job (`/v1/jobs/cancel`), sends `save-on` if needed, and waits at most 20 s.

### 5.4 Backup flow

1. Query the actual state from the agent. If the container is running: set `saveoff_since`, RCON `save-off`, then `save-all flush` (deadline 60 s each). If one of them fails → try `save-on` immediately, job `failed` with `rcon_unavailable`. Stopped: no RCON.
2. Service job `create` (kind `manual` or `scheduled`) until `done`/`failed`.
3. **Always** (also after an error, cancellation or timeout in step 2): RCON `save-on`, then clear `saveoff_since`. If `save-on` fails (server stopped in the meantime), that is harmless: a restart turns saving back on anyway.
4. Only for scheduled runs after success: service job `forget` with the server's policy and `backup_timezone`. restic gets `--tag kind=scheduled --keep-tag pinned --keep-last … --keep-daily … --keep-weekly … --keep-monthly …` (values of 0 left out). If only this step fails, the run counts as `ok` with a note in `last_error`.
5. Write `last_*` and `next_run_at` (scheduled only).

**Panel crash:** at start the panel looks for rows with `saveoff_since ≠ NULL`; for each it cancels a running service job of the instance, sends `save-on` (if the container is running) and clears the marker. Rows with `last_status = running` become `failed` ("panel restarted").

### 5.5 Restore flow

1. **Set the gate.** The former `servers.ExtractGate` becomes `servers.DataGate` (`DataJobRunning(ctx, id)`): true if an `extract` or `restore` job runs in the service (as before through `GET /v1/jobs`) **or** the panel holds a restore job for the server. While it is set, start/restart are blocked (`409 conflict`) and the reconcile loop does not start the server. Stop stays allowed.
2. If the container is running: stop through the agent (same path as the power action, `desired_state` stays unchanged), wait until stopped (deadline 120 s, otherwise `failed`).
3. Safety snapshot: service job `create` with `kind=pre-restore` (stopped, so without RCON). If it fails, the restore aborts; the data directory stays unchanged.
4. Service job `restore` until `done`/`failed`.
5. Release the gate. If `desired_state = running`, the reconcile loop starts the server on its next pass; the panel triggers it immediately.
- A panel crash in steps 2–3 leaves the data directory unchanged; after the restart the panel gate is gone and the reconcile loop starts the server again (the restore did not take place). In step 4 the service job keeps running; the gate holds through the service job until it ends.

### 5.6 Deletion

- Single snapshot: job `delete` → service job `forget` with exactly this ID; also pinned and pre-restore snapshots.
- Deleting a server (`DELETE /servers/{id}`, existing, admin + sudo) gets the optional query parameter `purge_backups=1`: after the removal at the agent the panel calls `POST /v1/backups/purge` (the server's waiting jobs are discarded first, a running one is cancelled). Without the parameter the repo stays behind orphaned (like `deleted/`); the schedule disappears with the server. The purge runs independently of the request being cancelled (own deadline, extended write deadline of the response). If it fails, the server is deleted anyway; the response stays `204`, and the audit detail records `backups_purged: false` (`true` after success).

### 5.7 API

All routes with `rbac.Server(server.backups, "id")`, writing ones with CSRF; restore and delete additionally `.WithSudo()`.

| Method, path | Body | Response |
|---|---|---|
| `GET /servers/{id}/backups` | – | `{available, snapshots, job, schedule}`; `schedule` incl. `next_run_at`, `last_*`, `timezone` |
| `POST /servers/{id}/backups` | `{note?}` | `202 {job}` (manual backup) |
| `GET /servers/{id}/backups/job` | – | `{job}` (`null` if none has run yet) |
| `PUT /servers/{id}/backups/schedule` | `{mode, interval_hours, time_of_day, weekday, keep_last, keep_daily, keep_weekly, keep_monthly}` | `{schedule}` |
| `POST /servers/{id}/backups/{snapshot}/pin` | `{pinned}` | `{snapshot}` (new ID) |
| `POST /servers/{id}/backups/{snapshot}/restore` | – | `202 {job}` |
| `DELETE /servers/{id}/backups/{snapshot}` | – | `202 {job}` |
| `GET /servers/{id}/backups/{snapshot}/download` | – | Zip stream, `Content-Disposition: attachment; filename="<name>-<YYYYMMDD-HHMMSS>.zip"` |

- `backup_timezone` goes into the existing settings (`GET`/`PUT /servers/settings`, admin, sudo for writing); no new route.
- `available = false` (restic missing, service unreachable): `snapshots: []`, actions answer `503 backups_unavailable` (restic missing) or `503 files_unavailable` (service gone).
- Download: like the file download (sub-project 3b) without buffering, with per-operation deadlines instead of the server timeouts; at most 2 concurrent backup downloads per user (`429 rate_limited`).
- Snapshot IDs in the path: 64 hex characters, otherwise `400 validation_failed`; unknown → `404 not_found`.

### 5.8 Error codes

New in `internal/httpx/errors.go` and in the OpenAPI enum: `backups_unavailable` (503), `backup_running` (409). Also used: `files_unavailable`, `agent_unavailable`, `rcon_unavailable` (only in the job error, not as an HTTP response, because backups run asynchronously), `insufficient_storage` (507, restore pre-check), `not_found`, `conflict` (server without an instance or data directory), `validation_failed`, `rate_limited`.

### 5.9 Audit and notification

- Audit (user actions): `backup.create` (note), `backup.restore` (snapshot, time), `backup.delete`, `backup.pin`, `backup.unpin`, `backup.download`, `backup.schedule_update` (old/new schedule), a change of `backup_timezone` in the existing settings entry. Scheduled runs are not user actions and are not audited; their result is in `last_*`.
- New `notify.EventType` `backup.failed`: for failed scheduled backups and for every failed restore (also when triggered manually). The title names the server, the message the error without host paths. The panel reports at most once in 6 h per server and kind (own throttle with `clock.Clock`); a success resets it. A failed `forget` step alone reports nothing.

### 5.10 Permissions

`server.backups` means: read all of the server's files (through the download, including plugin configurations with secrets) and roll back its state. User management shows a hint when granting it, as for `server.files`. As before, every server permission requires `server.view`.

## 6. Frontend

### 6.1 Tab "Backups"

- Visible with `server.backups`. Header: button "Create backup" (dialog with an optional note), below it the current job (step, bytes, queue position as "waiting") with polling every 2 s while it is `queued`/`running`.
- Snapshot list: time (browser time zone, relative + absolute), kind as a badge (scheduled / manual / before restore), note, size, pin icon. Actions: pin/unpin (scheduled only), download, restore, delete.
- Restore: confirmation dialog that explains the stop, the safety snapshot and the automatic restart and requires typing the server name; after that the global sudo dialog takes over. Delete: simple confirmation + sudo.
- Card "Schedule": mode, depending on the mode interval / time of day / weekday, retention fields with a short explanation, a note on the panel's time zone, next run, last status with error text.
- `available = false`: only a hint ("Backups are not available on this host: restic is missing"), no actions.

### 6.2 Other places

- Settings (admin): field "Time zone for backup schedules" (choice from `Intl.supportedValuesOf('timeZone')`).
- Delete server: checkbox "Also delete backups" (default off) with a hint that they otherwise stay on the host.
- User management: hint on `server.backups` (§5.10).
- i18n `de`/`en`; MSW mocks for all new operations, with a simulated job flow (queued → running → done) and an `available` switch.

## 7. Development, operation and CI

- **Mac (`make dev`):** backups only work with `MCPANEL_DEV_DOCKER=1` and a restic in `PATH` that the user installed themselves (`brew install restic`; Claude installs nothing). Without restic the tab shows the hint from §6.1. Without a reachable Docker, backup and restore fail with "Docker is not reachable" (job code `agent_unavailable`): the panel does not know the container's state, and a hanging dockerd must not count as a stopped server. Listing, schedule, pinning, download and deletion of snapshots only go through the file service and also work without Docker. Dev data under `.dev/mc/backups`.
- **Test VM and CI:** `scripts/vm-test.sh` and the CI jobs install restic 0.19.1 from the GitHub release (`restic_0.19.1_linux_<arch>.bz2`) with a hard-coded SHA-256 to `/usr/local/bin/restic` (shared script `scripts/install-restic.sh`) and set `MCPANEL_TEST_RESTIC` for the Go integration tests.
- **Production:** the installer (sub-project 6) installs restic (Ubuntu package `restic`, at least 0.17), sets `restic_binary` in `files.toml` and writes the unit as in §3.2; the agent creates `backups/`. `docs/dev/README.md` ("Backups") describes operation, including backing up `backups/keys/`. (Until the installer existed, the operator did this by hand.)
- **CI acceptance** (`scripts/ci-instances.sh`, production mode, real Paper server): create a file in the world, manual backup while the server is running (log shows `save-off`/`save-on`), change the file, restore (server stops, pre-restore snapshot exists, file has the old content, server runs again), the zip download contains the file and no `rcon.password`, pinning changes the ID, deleting removes the snapshot, deleting the server with `purge_backups=1` removes the repo.
- Manual acceptance checklist: `docs/dev/acceptance.md` ("Backups"). CLAUDE.md has a paragraph on backups under "Minecraft instances" and the spec link.

## 8. Tests

- **Service (Go, standard library):**
  - tar creation: skipping symlinks, special files, hidden entries and invalid names; a symlink swap of a folder during the walk (hook as in the extraction tests) reads nothing outside; redaction; limits.
  - tar extraction: all rules from sub-project 3b §4.2 carried over to tar (path tricks, symlink/hardlink/device entries, duplicate names, wrong sizes, limits); swap of the top level including hidden entries; cancelling before the swap leaves everything unchanged.
  - tar→zip: name rules, content.
  - Jobs: one job per instance across all kinds, `max_backup_jobs`, cancelling, shutdown with restic running.
  - restic calls against a fake `resticRunner` (argv, environment, tags, JSON parsing, error texts without paths); integration tests with a real restic (init, backup, list, pin, forget with policy and time zone, dump, restore, purge) if `MCPANEL_TEST_RESTIC` is set, otherwise `t.Skip`.
- **panel:**
  - Schedule computation with a fake clock: all modes, DST gap and repeated hour, missed runs, time zone change.
  - Queue and jobs against fakes of agent and service: order, one job panel-wide, `409 backup_running`, `save-on` after every error and after cancellation, recovery after a crash (`saveoff_since`), `forget` only after scheduled successes.
  - Restore: gate against power actions and the reconcile loop (also through a running service job), stop, pre-restore snapshot, error in every step.
  - Routes/OpenAPI match, permissions (incl. sudo), audit entries, `backup.failed` notification with throttling, `purge_backups` on deletion.
- **Frontend:** Vitest for the schedule form (validation, mode change) and the job display; Playwright against the mock: create a backup, pin, restore dialog with name confirmation and sudo, save the schedule, hint for `available=false`.

## 9. Acceptance criteria

1. A manual backup of a running Paper server is made with `save-off`/`save-all flush`/`save-on` in the log and appears in the list as "manual" with note and size.
2. A schedule (e.g. hourly) creates snapshots at the expected time in the configured time zone; `forget` keeps the retention, and manual, pre-restore and pinned snapshots stay.
3. A restore stops the server, creates a pre-restore snapshot, restores the state completely (hidden files untouched) and starts the server again if it is supposed to run; start/restart are blocked meanwhile.
4. The download returns a zip of the snapshot without `rcon.password`.
5. Symlinks in the data directory never end up in the snapshot, and no restore writes outside `instances/<id>`.
6. A failed scheduled backup appears with its error in the UI and as a notification (at most once in 6 h).
7. Without restic on the host everything else runs normally; the tab shows the hint.
8. A panel crash during a backup leaves no server with a permanent `save-off`.
9. Deleting a server with "Also delete backups" removes repo and key; without the option both stay.
