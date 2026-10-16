# mc-panel – Sub-project 5: Monitoring and Alerts (design spec)

- **Date:** 2026-10-13
- **Status:** implemented (PRs #61, #62, #65–#67, 2026-10-14)
- **Author:** Jannes Göbel (with Claude)
- **Builds on:** `2026-10-08-mc-panel-foundation-design.md` (sub-project 1), `2026-10-10-mc-panel-instances-design.md` (sub-project 2), `2026-10-11-mc-panel-console-players-design.md` (sub-project 3a), `2026-10-11-mc-panel-files-design.md` (sub-project 3b), `2026-10-12-mc-panel-backups-design.md` (sub-project 4)

## 1. Goal and context

Sub-project 5 shows how the servers and the host are doing and reports critical states. The panel collects players online, TPS/MSPT, CPU and RAM per server, and CPU, RAM, load and free disk space of the host. It stores 30 days of history at tiered resolution and shows both on the dashboard and in a new "Monitoring" tab. Alerts exist for "server down / crash loop" and "disk low", sent through the existing notifications (Discord, ntfy).

### 1.1 Success

- A user with `server.view` sees, per server, the live player count with names, TPS, MSPT, CPU and RAM, and the history over 1 h / 24 h / 7 d / 30 d.
- The dashboard shows compact values per server with a player sparkline of the last hour; admins also see a host card.
- If a server that should be running goes down, or restarts repeatedly, a notification arrives within a few minutes, and a second one when it runs again. If space on `/srv/mc-panel` runs low, a notification arrives as well.
- Monitoring adds at most two RCON lines per minute to the server log (except while someone has the Monitoring tab open).

### 1.2 Guiding principle

Unchanged from sub-project 1 §3 and sub-project 3b §1.2: the root agent stays narrow and does not process data that a container controls. The player count therefore does not come through the agent but from a Server List Ping that the unprivileged panel sends through mc-router; the panel reads the answer of the (possibly malicious) server with fixed limits. The agent only gets one additional field in its existing status (`restart_count`).

### 1.3 Non-goals (sub-project 5)

- Player sessions, playtime per player, player names in the history.
- Alerts for performance (TPS/MSPT) and resources (RAM/CPU); the data model allows them later without migrating the time series.
- Prometheus/OpenMetrics export, external dashboards.
- Network traffic, disk load (IOPS), per-process metrics.
- Per-server alert rules other than the "down alerts" switch; muting for a period of time.

## 2. Decisions

| Area | Decision | Reason |
|---|---|---|
| Collector | Panel package `internal/monitoring`, interval 30 s (TPS 60 s) via `clock.Clock` | Only the panel knows servers, permissions, alerts and `notify`; no new privileges |
| Players | Server List Ping to `127.0.0.1:25565` with the server's FQDN in the handshake; mc-router forwards it | No log entry per query (unlike RCON), also checks reachability through the router; root parses nothing from the container |
| TPS/MSPT | RCON through the existing agent route, once a minute, live every 5 s only while someone watches | No other way without a plugin; 60 s keeps the RCON noise low |
| CPU/RAM per server | existing agent status (`SampleStats`) | Already collected, just not stored |
| Host | The panel reads `/proc/stat`, `/proc/meminfo`, `/proc/loadavg` and `statfs(<instances_dir>)` itself | Readable without privileges; no new agent endpoint |
| Storage | SQLite, raw values every 30 s (24 h), 5-min averages (7 d), 1-h averages (30 d) | Small (< 50 MB for 10 servers), no new dependency |
| Alerts | Configurable globally (admin), per server only "down alerts on/off"; persistent state; notification when firing, when resolving and every 24 h | Easy to use; no spam, nothing forgotten |
| Charts | Own SVG components (`LineChart`, `Sparkline`) | No new dependency, CSP-compatible, only lines needed |

### 2.1 Rejected alternatives

- **Everything via RCON every 30 s:** simplest to build, but about 11,000 RCON lines per server and day in the log.
- **Prometheus `/metrics` + Grafana:** standard, but an additional service on the VPS; history and alerts would live outside the panel.
- **Collector in the agent:** the root agent would grow and would have to parse ping answers from containers.
- **Ping via the container IP:** the panel is not in the Docker network `mcpanel-mc`; only the agent knows the IP. The route through mc-router needs none of that.
- **uPlot / Recharts:** more features (zoom), but a new dependency for four line charts.

## 3. Architecture

```
Browser ─/api/monitoring/*, /api/servers/{id}/metrics*─▶ panel: internal/monitoring
                                                          · collector (30 s; TPS 60 s / live 5 s)
                                                          · rollup (5 min), deletion of old values
                                                          · alert evaluation → notify
           ┌──────────────────────────────┬───────────────┴───────────────┬──────────────────────────┐
  agent (existing):               mc-router (existing)             agent RCON (existing)      host (panel itself)
  GET /v1/instances               127.0.0.1:25565, SLP with FQDN   tick query / mspt / tps    /proc, statfs
  + restart_count (new)           → players, max, name sample
```

- **panel:** new package `internal/monitoring` (collector, ping client `slp.go`, TPS parser, host reader with `_linux.go`/`_other.go`, storage, rollup, alerts, handlers).
- **agent:** `InstanceStatus` gets `restart_count` (from Docker's `RestartCount` in the inspect response that the status already fetches). No new route.
- **Configuration:** `panel.toml` gets `monitoring_ping_addr` (default `127.0.0.1:25565`, dev `""` = off). Without a ping address the player count is "unknown", and "not reachable" is derived from the container state only.

### 3.1 Collecting

- **Interval 30 s** (ticker via `clock.Clock`), one pass for all servers with an instance:
  1. Agent status (one call for all): state, `started_at`, `restart_count`, `cpu_percent`, `memory_bytes`, `memory_limit_bytes`.
  2. For running containers, in parallel (at most 4 at a time), the ping (§3.2).
  3. Host values (§3.4).
  4. Write the values (one transaction), evaluate alerts (§5).
- **TPS/MSPT** every 60 s for running containers whose ping answers; additionally every 5 s for servers with a viewer (§6.2). At most one RCON call per server at a time; errors are not retried, the value is then missing.
- A pass that takes longer than 30 s skips the next tick (no backlog). On shutdown the collector cancels running calls.
- Without the agent (`503`) no server values are written for that pass; host values are.

### 3.2 Server List Ping

- TCP to `monitoring_ping_addr`, a 3 s deadline for the whole exchange. Handshake with protocol version `-1` (status), server address = the server's FQDN, port 25565, next state 1; then a status request and reading the response. No ping/pong pair.
- Limits: VarInt lengths at most 5 bytes, packet length ≤ 64 KiB, JSON string ≤ 64 KiB; otherwise an error. Only `players.online` (0–100,000), `players.max` (0–100,000) and `players.sample[].name` (at most 12, each ≤ 16 characters, only `[A-Za-z0-9_]`, otherwise skipped) are evaluated. `description`, `favicon` and everything else are ignored.
- Result: `up = 1` for a valid answer, otherwise `up = 0` (container running but not reachable, e.g. server still starting, router error).
- Names from the ping may be anonymized (`hide-online-players`); the UI shows them as they come.

### 3.3 TPS/MSPT

- Minecraft ≥ 1.20.3 (from the instance's resolved version, all types): RCON `tick query`. Evaluated: "Target tick rate: X per second" and "Average time per tick: Y ms"; `mspt = Y`, `tps = min(X, 1000 / Y)`.
- The collector detects the source per server by trying: first `tick query`, otherwise for paper/purpur `mspt` (average of the last 5 s; `tps = min(20, 1000 / mspt)`), otherwise no TPS values ("not available"). A frozen game (`frozen`) → `tps = 0`.
- Output is read without `§` formatting codes; the parser accepts only numbers in the expected pattern and range (`tps` 0–1000, `mspt` 0–60,000), otherwise the value is missing. Test data comes from real servers on the test VM.
- RCON deadlines: 10 s (the agent's default).

### 3.4 Host

- CPU: difference of two `/proc/stat` readings (line `cpu`) between two ticks, as a percentage.
- RAM: `/proc/meminfo` `MemTotal` and `MemAvailable` → `memory_total`, `memory_used = total − available`.
- Load: `/proc/loadavg`, first value.
- Disk: `statfs(<instances_dir>)`: `disk_total`, `disk_free` (blocks available to non-root). `instances_dir` comes from `panel.toml` (new key `instances_dir`, default `/srv/mc-panel`, dev `.dev/mc`); the panel only needs read access to the directory itself (0755).
- macOS (`_other.go`): only the disk via `statfs`, everything else "not available".

## 4. Data model (migration 00007)

```sql
CREATE TABLE metrics_raw (
    series INTEGER NOT NULL,   -- 0 = host, otherwise servers.id
    metric TEXT    NOT NULL,
    at     INTEGER NOT NULL,   -- Unix ms, time of the tick
    value  REAL    NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_5m (
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL, -- start of the interval
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_1h (  -- like metrics_5m
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
```

- Metrics per server: `up`, `players`, `players_max`, `tps`, `mspt`, `cpu_percent`, `memory_bytes`, `memory_limit_bytes`. Host (`series = 0`): `cpu_percent`, `memory_used`, `memory_total`, `load1`, `disk_free`, `disk_total`. Missing values are not written (a gap instead of 0).
- Rollup every 5 min: completed 5-min intervals from `metrics_raw` → `metrics_5m`, completed hours from `metrics_5m` → `metrics_1h` (weighted by `samples`), idempotent (`INSERT OR REPLACE`). Deletion: `metrics_raw` older than 24 h, `metrics_5m` older than 7 d, `metrics_1h` older than 30 d. When a server is deleted, the panel removes its series (its own query in the delete path; no foreign key because of `series = 0`).
- Queries in `internal/store/queries/monitoring.sql`, prefix `Monitoring…`.

## 5. Alerts

- **`server.down:<id>`** fires when `desired_state = running`, `down_alerts = 1`, there is no data gate (extract/restore, sub-projects 3b/4) and the server has been unreachable without interruption for `down_after_min`: container not `running` (also `restarting`, `exited`) or `up = 0`. After every start/restart (according to `started_at`, so also after every restart following a crash) a grace period of 5 min applies (Minecraft needs time to start). While `server.crash_loop` fires for the server, `server.down` is not reported in addition. It resolves on the first valid ping answer (or, without a ping address: container `running` for 2 min) with `server.recovered`.
- **`server.crash_loop:<id>`** fires when `restart_count` rises by at least `crash_restarts` within `crash_window_min` (measured on the stored values; recreating the container resets the counter and does not count). It resolves with `server.recovered` ("Server stable again") once there was no restart within the window. With `down_alerts = 0` this alert is off as well (the switch covers both server alerts).
- **`host.disk_low`** fires when `disk_free < disk_min_bytes` or `disk_free < disk_total × disk_min_percent / 100`; it resolves with `host.disk_ok` once both are met again with a margin of 2 percentage points and 2 GiB respectively (hysteresis).
- **Delivery:** new `notify.EventType`s `server.down`, `server.recovered`, `server.crash_loop`, `host.disk_low`, `host.disk_ok`. A notification when firing and when resolving; while an alert fires, a reminder every 24 h. The state lives in `alert_state` and survives panel restarts (no new "down" after a restart if the alert was already reported). Titles name the server or the host, messages the reason without host paths.
- A deleted server removes its alert states without a notification. A server with `desired_state = stopped` resolves a firing down alert without a notification.

## 6. API

### 6.1 Routes

| Method, path | Rule | Response |
|---|---|---|
| `GET /servers/{id}/metrics?range=1h\|24h\|7d\|30d` | `Server(server.view)` | `{range, step_ms, series: {<metric>: [[at, avg, min, max] …]}}`; 1h → raw values (min = max = avg), 24h → 5 min, 7d and 30d → 1 h |
| `GET /servers/{id}/metrics/live` | `Server(server.view)` | `{at, up, players, players_max, player_names, tps, mspt, cpu_percent, memory_bytes, memory_limit_bytes, tps_source}`; missing values `null`; `tps_source` ∈ `tick_query\|paper\|none` |
| `GET /monitoring/overview` | `Authenticated()` | `{servers: [{id, up, players, players_max, tps, cpu_percent, memory_bytes, memory_limit_bytes, sparkline: [[at, players] …], alerts: [<key>]}], host: {…}\|null}`; only servers with `server.view`; `host` only for admins |
| `GET /monitoring/host?range=…` | `Admin()` | like `metrics`, host series |
| `GET /monitoring/settings` | `Admin()` | `{disk_min_percent, disk_min_bytes, down_after_min, crash_restarts, crash_window_min, alerts: [{key, since, detail}]}` |
| `PUT /monitoring/settings` | `Admin().WithSudo()` | values as above (all required) → `200` with the new state |
| `PUT /servers/{id}/alerts` | `Admin()` | `{down_alerts}` → `204` |

- `GET /servers/{id}` (existing) gets `down_alerts` for admins (for the switch in the tab).
- Invalid `range` → `400 validation_failed`; server without an instance → empty series.

### 6.2 Live mode

- Every call to `metrics/live` marks the server as "watched" for 30 s: the collector then queries TPS/MSPT every 5 s (instead of 60 s) and returns the freshest values in `live`; players, CPU and RAM stay at the 30 s interval (the agent samples CPU/RAM anyway). Live TPS values are not stored in addition (the history keeps the 60 s interval).
- The normal API rate limit applies; the tab polls every 5 s.

### 6.3 Audit and error codes

- Audit: `monitoring.settings_update` (old/new), `server.alerts_update` (`{down_alerts}`), target as usual (`settings`, `server:<id>`).
- No new error codes; `agent_unavailable` only for `live` when the agent did not answer for the current tick (the other routes return stored values).

## 7. Frontend

(Now: sub-project 7 restyled these views. The host card is a row of `StatSlot`s, each dashboard row shows `PingBars`, and the Monitoring tab is the server area `/servers/$id/monitoring`; see `2026-10-14-mc-panel-ui-redesign-design.md` §4.3 and §5.)

### 7.1 Dashboard

- Host card (admin only) at the top: CPU, RAM (used/total), load, disk (free/total) as bars; warning colour when `host.disk_low` fires; link to the monitoring settings.
- Per server in the existing list: players `x/max`, TPS, RAM (bar to the limit), CPU, player sparkline of the last hour, status dot (running / not reachable / stopped), badge for a firing alert. Polled every 30 s.

### 7.2 "Monitoring" tab

- Visible with `server.view` for servers with an instance. Live header (every 5 s, only while the tab is open): players with a list of names, TPS, MSPT, CPU, RAM; "TPS not available for this version" when `tps_source = none`.
- Range selection 1 h / 24 h / 7 d / 30 d; charts for players, TPS + MSPT (two axes), CPU %, RAM (with a limit line). For 24 h and longer the chart shows the average and a min–max band.
- Admins: switch "Down alerts for this server".

### 7.3 Settings

- Card "Monitoring & Alerts" (admin): the five values with explanations and validation as in §4; list of the currently firing alerts; a note that notifications go through the configured notification channels (link), and a warning if none is configured.

### 7.4 Charts

- Own components `Sparkline` and `LineChart` (SVG, `viewBox`, responsive): time axis in browser time, y axis with "nice" ticks, grid, gaps for missing values (no connecting line across gaps > 2 steps), tooltip by mouse and keyboard (focus on the chart, arrow keys), colours from the theme tokens, light/dark.
- Accessibility: `role="img"` with an `aria-label` summary (current, min, max) and a toggle "Show as table".
- Client-side thinning to at most 1 point per pixel column (min/max preserved).
- i18n `de`/`en`; MSW mocks with generated, deterministic time series for every server and the host.

## 8. Development, operations and CI

- **Mac (`make dev`):** no ping (no ping address), host only disk; TPS only with `MCPANEL_DEV_DOCKER=1`. The mock shows everything.
- **Production:** `panel.toml` `monitoring_ping_addr = "127.0.0.1:25565"` and `instances_dir` (now both written by the installer from `deploy/config/panel.toml`, sub-project 6); the panel needs `statfs` on `instances_dir` (readable, 0755) and read access to `/proc` (systemd `ProtectProc=invisible` must not hide `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`; it does not, it only affects other users' PIDs).
- **CI acceptance** (`scripts/ci-instances.sh`, shards `monitoring-metrics` and `monitoring-alerts`, real Paper server): after the start `up = 1`, `players = 0`, `players_max` from `server.properties`, TPS between 15 and 20 in the history (after ≥ 1 min), CPU/RAM > 0, host values present; then provoke restarts (`stop` via RCON three times with `desired_state = running`) → `server.crash_loop` is reported (no additional `server.down`), and `server.recovered` once there was no restart within the window; separately "not reachable" (see below) → exactly one `server.down` and then `server.recovered`. Times shortened via `down_after_min = 1` and `crash_window_min = 10 × time scale` (at most 120), and by the acceptance build's time scale (`-tags acceptance`, `MCPANEL_ACCEPTANCE_TIMESCALE=10`: passes, TPS cadence, start grace and the alert minute ten times faster; during the three real crashes `down_after_min` = time scale, i.e. 60 s). "Not reachable" is produced by an nftables rule that drops only the panel's pings (a stopped container would be restarted by the reconcile loop immediately). An ntfy mock is not possible (the panel rejects targets on localhost/private addresses); CI checks the delivery attempts to a notifier `https://ntfy.invalid` in the panel log and the alert state via `/monitoring/settings`.
- Manual checklist: `docs/dev/acceptance.md` ("Monitoring and alerts").

## 9. Tests

- **Go:** SLP client against a fake server (valid, oversized, broken VarInts, wrong types, slow, connection drop, 13+ names, invalid names); TPS parser with real output (vanilla, Paper, Purpur, Fabric, old/new) and garbage; host reader with prepared `/proc` files; collector with a fake clock and fakes for agent, ping and RCON (interval, skipping on overrun, live mode on/off after 30 s, concurrency limit); rollup and deletion (interval boundaries, idempotency, weighted averages); alerts (grace period, data gate, `down_alerts = 0`, hysteresis, reminder after 24 h, panel restart with a firing alert, server deleted/stopped, crash counter reset); routes/permissions/OpenAPI, audit.
- **agent:** `restart_count` from inspect.
- **Vitest:** scales and ticks, gaps, thinning, tooltip keyboard, tabs, settings, dashboard values.
- **Playwright** against the mock: dashboard with host card (admin) and without (user), tab with range switch and table, saving settings with sudo.

## 10. Acceptance criteria

1. Dashboard and tab show players, TPS/MSPT, CPU and RAM for a running Paper server; the history fills up and can be viewed across all four ranges.
2. Without an open Monitoring tab, the server log shows at most two RCON lines per minute caused by monitoring.
3. A server that should be running and is not triggers exactly one notification after `down_after_min` (plus the grace period after a start), and a "back online" notification after it comes back; a panel restart in between does not cause a duplicate notification.
4. Three crashes within the window trigger `server.crash_loop`; "down alerts off" suppresses both.
5. If free space drops below the threshold, `host.disk_low` arrives, and `host.disk_ok` after space is freed with margin.
6. Users without the admin role see no host values and only servers with `server.view`.
7. A crafted ping answer (huge, broken, slow) affects neither the panel nor other servers.
8. With 10 servers, monitoring data does not grow the DB beyond 50 MB over 30 days.
