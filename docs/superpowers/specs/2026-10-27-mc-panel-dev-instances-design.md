# mc-panel: dev instances (design spec)

- **Date:** 2026-10-27
- **Status:** implemented (2026-10-27)
- **Author:** Jannes Göbel (with Claude)
- **Builds on:** instances (`2026-10-10-…-instances-design.md`), console and players (`2026-10-11-…-console-players-design.md`), backups (`2026-10-12-…-backups-design.md`), monitoring (`2026-10-13-…-monitoring-design.md`). Everything there stays in force unless this document changes it.

## 1. Goal and context

An operator runs one real server ("prod") and wants a throwaway copy of it ("dev") to try mods and small builds without touching prod. Players get creative rights on dev that they do not have on prod. Tests are small: realistically nobody needs more than a day, so dev is overwritten from prod every night and starts each day as a fresh copy. For the rare longer test, the nightly sync can be paused.

The host is small. The motivating case is a 16 GiB VPS with an 8–10 GiB prod server, so dev must run with the minimum it needs and must not hold memory while nobody plays on it.

### 1.1 Success

- An admin creates a dev instance of a server with one action. It appears at `dev-<name>.<base domain>` with the world, mods/plugins and configs of prod.
- Every night (while the sync is not paused) dev is replaced by the current state of prod. Changes made on dev are gone afterwards, except the dev's own ops list and dev settings.
- Dev sleeps when nobody plays and wakes when a player connects: the first connection is refused with "Dev is starting, reconnect in about a minute", the second gets in. Nobody has to start it in the panel.
- Everywhere in the UI it is obvious that a server is a dev instance, which prod it copies, and when it will be overwritten next.

### 1.2 Guiding principle (anti-goal)

**Prod must never be harmed by dev.** No feature of this spec may write to prod's data directory, stop prod, change prod's spec, or noticeably slow prod down. The worst outcome is a prod world damaged or lost because of a sync, and every design choice below is weighed against it. Concretely:

- Data flows only prod → dev, and only from a restic snapshot of prod. The sync never opens prod's data directory itself; the snapshot is taken by the existing backup path (`save-off` / `save-all flush` / `save-on`, spec 4 §5.4), which prod already tolerates.
- Prod keeps running during the sync. It is never stopped for it.
- Every component that writes (files service restore, panel spec update) checks that the target is a dev instance and that source ≠ target, independently of the caller.

### 1.3 Non-goals

- Promoting dev to prod, syncing dev → prod, or copying single files/mods back. Changes on dev that should go to prod are made again on prod by hand.
- More than one dev per prod, a dev of a dev, dev instances of servers on other hosts.
- Partial syncs (only mods, only the world).
- Backups of dev: dev has no restic repository, no schedule and no Backups area.
- Web ports (BlueMap etc.) on dev.
- Per-instance CPU limits (the CPU cap stays global, `cpu_per_instance`).
- New panel users or a new permission: the people who manage prod manage dev.

## 2. Decisions

| Question | Decision |
|---|---|
| Name | Always `dev-<prod name>`, so the address is `dev-<name>.<base domain>` without any routing special case. Prod names longer than 28 characters cannot get a dev (the name limit is 32). New ordinary servers may no longer be called `dev-…`; existing ones keep working, and an existing `dev-<x>` blocks a dev of `<x>` (`409 name_taken`). |
| How many | At most one dev per prod. |
| Who creates/deletes | Admins with sudo, like creating and deleting servers. Deleting a prod that has a dev is refused (`409 dev_exists`); the dev is deleted first. *(Builder's call; the alternative "delete both in one dialog" was rejected as easier to get wrong.)* |
| What is copied | The whole visible data directory (world, `mods/`/`plugins/`, `config/`, `server.properties`, `ops.json`, `whitelist.json`, …) from a snapshot of prod, plus the instance spec (type, version, loader, modpack, java, properties). `rcon.password` stays dev's own (the restore already keeps it). |
| What survives the sync | Dev's memory, its property overrides (§3.2), its ops list (§5), its sync and sleep settings. Everything else is overwritten. |
| Sync source | A restic snapshot of prod, never prod's live directory. The prod is not stopped. |
| Sync time | Daily at a configurable time in `instance_settings.backup_timezone`, default 05:00 *(builder's call)*, plus a "Sync now" button. |
| Pause | A switch on the dev, unlimited until switched back on. While paused, the UI says so prominently. |
| Resources | Memory default 2 GiB for vanilla/paper/purpur, 3 GiB for fabric/neoforge/modpacks *(builder's call; the user asked for "the minimal requirements")*, adjustable per dev. |
| Permissions | Dev has no permission rows of its own: every `Server(perm, "id")` check on a dev resolves against its prod. `server.backups` grants nothing on dev (no Backups area). |
| Sleep | Dev stops after 15 minutes without players *(builder's call; adjustable 5–120 min)* and wakes on a connection attempt through a waker (§6). |
| Alerts | No `server.down`, `server.recovered` or `server.crash_loop` alerts for dev. |
| Creative | Players get creative through the dev ops list (§5): ops switch with `/gamemode` themselves. The default game mode is not changed, so no new property (`force-gamemode`) is needed. |
| Editing a dev | The admin `PATCH /servers/{id}` accepts only `memory_mib` on a dev; any other field (version, type, properties, web routes) answers `409 dev_managed` *(builder's call)*. Dev settings go through `PATCH /servers/{id}/dev`. |
| Stop and start by hand | A manual stop of dev equals falling asleep (waker active). A manual start is allowed and the idle timer applies from that start *(builder's call)*. |
| Players online at sync time | A scheduled sync waits while players are on dev: it warns in chat and is retried every 15 minutes, for at most 2 hours, then runs anyway with a final warning. Waiting does not hold the backup queue (the job is re-queued, not parked). "Sync now" warns and runs after 60 seconds *(builder's call)*. |

## 3. Data and spec

### 3.1 Data model (new migration)

- `dev_instances`: `server_id` (PK, the dev, `REFERENCES servers ON DELETE CASCADE`), `source_id` (`REFERENCES servers`, `UNIQUE`, no cascade: deleting a prod with a dev is refused in the handler), `sync_paused` (bool), `sync_time` (minutes 0–1439), `idle_minutes` (5–120), `next_sync_at`, `last_sync_at`, `last_sync_status` (`ok|failed|running`), `last_sync_error`, `last_sync_snapshot` (restic id), `sleeping` (bool), `last_wake_at`, `property_overrides` (JSON, see §3.2), `created_at`, `updated_at`. Dev's memory stays in `instances.memory_mib`, which the sync never overwrites. A CHECK forbids `server_id = source_id`.
- `dev_ops`: `server_id` (the dev, cascade), `name` (Minecraft player name, validated like the Players tab), `created_at`; primary key `(server_id, name)`.
- Queries in a new `internal/store/queries/dev.sql` with the prefix `Dev…`.

Whether a server is a dev is decided only by a row in `dev_instances`. Server list and server views (`GET /servers`, `GET /servers/{id}`) gain `dev: {source_id, source_name, sync_paused, next_sync_at, last_sync_at, last_sync_status, sleeping} | null` and, on a prod, `dev_id`.

### 3.2 Dev overrides

After copying prod's spec, the panel applies the dev overrides and bumps the dev's `revision`:

- `memory_mib`: the dev's own value (never prod's).
- `property_overrides`: only `view_distance` (default 4) and `simulation_distance` (default 3) *(builder's call)*, editable on the dev and merged over prod's properties. The set is fixed; other properties always follow prod.
- `motd` prefixed with `[DEV] ` (fixed, not editable).

All other properties come from prod, including `whitelist_enabled` (so the whitelist of prod also protects dev).

A prod type or version change reaches dev at the next sync (spec copy + revision bump = the dev container is recreated at its next start).

## 4. Sync

### 4.1 Flow

A sync (scheduled or "Sync now") runs as a job in the existing panel-wide backup queue (spec 4 §5.3: FIFO, one job at a time), so it never overlaps a backup, restore or forget of prod or any other server. Steps:

1. **Snapshot of prod.** If prod has a snapshot of kind `scheduled` or `dev-sync` (never `manual` or `pre-restore`: a manual snapshot may hold a state the operator did not want to keep) that is newer than the last sync and at most 6 hours old *(builder's call)*, use it. Otherwise take a new one through the normal backup flow of spec 4 §5.4 with the new snapshot kind `dev-sync`. After a successful sync, older `dev-sync` snapshots of that prod are forgotten; retention of scheduled snapshots is not affected. Without restic (`available: false`) the sync fails with a clear message; dev keeps its state.
2. **Players online?** See "Players online at sync time" in §2. Then **stop dev** and hold the data gate on dev (`servers.DataGate`): no start, no wake, no reconcile start while the sync runs.
3. **Restore into dev.** The files service restores the snapshot from **prod's** repository into **dev's** data directory with the existing restore machinery (temp dir, swap, journal under dev's id). No `pre-restore` snapshot (dev has no repository).
4. **Apply the spec** of prod plus the dev overrides (§3.2); copy prod's `plugin_installs` rows to dev so the Plugins area shows the same origins.
5. **Release** the gate. Dev stays asleep; it starts on the next connection. On every start, the panel re-applies the dev ops list (§5).

A failed sync leaves dev with whatever the restore left (the restore journal guarantees either the old or the new state), sets `last_sync_status = failed`, notifies `dev.sync_failed` (at most once per 6 h per dev, like `backup.failed`) and is retried at the next scheduled time. A paused dev is skipped and keeps `next_sync_at` moving forward.

### 4.2 Files service

A new request field or endpoint for "restore snapshot `S` of repository `source_id` into data directory `target_id`" (`filesapi`, shared with the panel). The service:

- refuses `source_id == target_id`;
- opens the source repository only for `restic dump` (no write to it beyond restic's own non-exclusive lock);
- never opens the source's data directory;
- otherwise behaves exactly like the existing restore (reserve check `507`, hidden entries stay, `rcon.password` of the target kept, one job per target instance, journal under the target's id).

The files service does not know which server is a dev; the panel enforces that the target is the dev of the source. That is acceptable because a compromised panel can already read every file and restore any server; the check in the service is against mistakes (wrong direction), not against the panel.

### 4.3 Impact on prod

- Prod is touched only through the snapshot of step 1, which is the same operation as a normal backup. When a fresh scheduled backup exists, prod is not touched at all.
- Restore I/O happens on dev's directory; the files service runs restic and the extraction at reduced CPU and I/O priority if the platform allows it *(builder decides how; must not need new privileges)*.
- Disk: dev doubles the disk use of prod. The existing reserve check (`507 insufficient_storage`) and the `host.disk_low` alert cover it; the create dialog shows prod's current size.
- Acceptance proves it (§9): prod's data directory is byte-for-byte unchanged by a sync except what prod itself wrote, and prod stays reachable during it.

## 5. Dev ops

Dev has its own ops list in `dev_ops`, managed in the dev's Players area (`server.console`, like other player actions). Adding a name stores it and, if dev is running, sends `op <name>` through the fixed RCON mapping; removing it stores the removal and sends `deop <name>`. After every start of dev (also after a wake), the panel sends `op <name>` for each entry once the server answers RCON. Prod's ops come along with `ops.json` and are ops on dev as well. User text never becomes a command beyond the validated player name (console spec rules apply).

## 6. Sleep and wake

### 6.1 Sleep

The monitoring sampler already reads the player count every 30 s. When a running dev has had zero players for `idle_minutes` (counting from its start, so a fresh wake is not put back to sleep before anyone joins), the panel stops it and sets `sleeping`. `desired_state` becomes `stopped`, so the reconcile loop does not start it again. A failed ping counts as "unknown", not as "zero players".

A manual stop also sets `sleeping`. While sleeping, the sampler does not ping dev (the router would answer from the waker): the series has gaps and the status is "sleeping", not "down".

### 6.2 Waker

mc-router's own auto-start would need the Docker socket, which the router deliberately does not get (`internal/agent/instances/template.go`, `RouterContainer`). Instead, while dev sleeps, the route of `dev-<name>.<base domain>` points to a **waker**:

- It speaks just enough of the Minecraft protocol: handshake, status (server list shows the MOTD "Dev sleeps – connect to wake it", version text, 0 players) and login start, answered with a disconnect message "Dev is starting, reconnect in about a minute".
- A login attempt (not a status ping) asks the panel to wake the dev: the panel sets `desired_state = running`, clears `sleeping`, records `last_wake_at` and the reconcile loop starts it. Wakes are rate-limited (at most one per dev per 2 minutes, plus a global cap) and ignored while the data gate is held. The waker cannot know about a running sync, so its one disconnect text covers both cases ("Dev is starting (or being synced), reconnect in about a minute").
- Any connection may wake dev (the waker cannot check the whitelist before login). The whitelist still decides who can join.
- Once dev runs, the route points back to the dev container.
- Today the agent builds mc-router's routes from container labels alone. The agent API therefore gains a way for the panel to say which dev hostnames are asleep and must point to the waker's fixed address (validated in `agentapi` like every request: hostnames of existing dev containers only, the waker address from `agent.toml`, never from the panel).

Constraints for the waker (how it is built is the builder's call):

- It must not run in the root agent and must not get Docker access. It handles untrusted input from the internet with strict limits (packet sizes, timeouts, connections per IP, total connections).
- It is reachable only from `mcpanel-router`, not from the internet directly.
- Chosen shape (decided while planning): a container `mcpanel-waker` that runs the agent binary in an unprivileged `waker` mode (bind-mounted read-only into the pinned mc-router image, uid 65534, no capabilities, read-only root, 32 MiB, only on mc-router's network `mcpanel-mc`, no published port). It logs `wake <fqdn>` lines; the agent reads them from the container log, keeps only FQDNs of sleeping devs the panel named, and returns the server ids to the panel's 2-second poll (`PUT /v1/waker`). No host listener, no firewall change, no Docker access for the waker. A listener in the panel process was rejected because the router would need a path to the host through the host firewall.

## 7. API (sketch)

| Method and path | Rule | Purpose |
|---|---|---|
| `POST /servers/{id}/dev` | Admin, sudo | Create the dev of prod `{id}` (body: optional memory); starts an initial sync. |
| `GET /servers/{id}/dev` | `server.view` | Dev settings and sync status (on the dev's id). |
| `PATCH /servers/{id}/dev` | `server.files` | Pause/resume the sync, sync time, idle minutes, property overrides (memory: Admin, sudo). |
| `POST /servers/{id}/dev/sync` | `server.files` | Sync now (queued). |
| `GET/POST/DELETE /servers/{id}/dev/ops[/{name}]` | `server.console` | Dev ops list. |
| `DELETE /servers/{id}` | Admin, sudo | Unchanged for dev; `409 dev_exists` on a prod with a dev. |

`{id}` is the dev's id except on `POST /servers/{id}/dev`. Endpoints on a server that is not a dev answer `409 not_dev`. New error codes (`dev_exists`, `not_dev`, `dev_managed`, `name_taken` if not present) go into `internal/httpx/errors.go` and the OpenAPI enum. Backups endpoints on a dev answer `409 dev_no_backups` *(or the area is simply absent; builder's call)*.

Audit: `dev.create`, `dev.delete`, `dev.sync` (scheduled/manual, result, snapshot id), `dev.settings_update`, `dev.op_add`, `dev.op_remove`, `dev.wake`.

## 8. Frontend

- **Badge:** a "DEV" badge (Silkscreen, own status colour token in `:root` and `.dark`, AA-checked) next to the name in the server tree, breadcrumb, server list and server header.
- **Tree:** dev is listed directly under its prod.
- **Banner** in the dev's server header: "Copy of <prod> – changes are overwritten at the next sync (today 05:00)" or, when paused, "Sync paused since <date> – dev keeps its changes", plus "Sync now" and the pause switch. Shows last sync result and, when sleeping, "Sleeps – connect to wake it".
- **Areas:** `serverSections` hides Backups on a dev. Settings of a dev show memory, sync time, idle minutes and the overrides; the danger zone deletes the dev.
- **Prod:** its overview/settings has "Create dev instance" (admin) or a link to the existing dev.
- **Players** on dev: an extra "Dev ops" list.
- de and en strings, MSW mocks for every new operation.

## 9. Tests and acceptance

- Unit tests for the name rules, the overrides, permission resolution through the prod, the sleep timer (fake clock), the waker's protocol parsing (fuzz) and rate limits, and the files service's refusal of `source == target`.
- Acceptance shard (CI, real Docker and restic): create prod with a world, create dev, change prod, run a sync, check dev has prod's state, dev ops re-applied, overrides kept; take a checksum of prod's data directory before and after the sync (only files prod writes itself may differ) and ping prod throughout; let dev fall asleep (timescale), connect once (refused, dev starts), connect again (joins).
- `docs/dev/acceptance.md` gets a "Dev instances" section; `docs/install.md` only if the waker needs a firewall or installer change.

## 10. Left to the builder

The waker's exact placement and network setup within the constraints of §6.2; the exact snapshot reuse window; how restic and extraction get reduced priority; the shape of the sync job inside the backup queue; the UI layout details within the design system.
