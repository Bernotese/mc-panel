# mc-panel – Sub-project 2: Instance lifecycle (design spec)

- **Date:** 2026-10-10
- **Status:** implemented (PRs #9–#16, 2026-10-11)
- **Author:** Jannes Göbel (with Claude)
- **Builds on:** `2026-10-08-mc-panel-foundation-design.md` (sub-project 1)

## 1. Goal and context

Sub-project 1 delivered the foundation and security; there, servers exist only as stub records for permission tests. Sub-project 2 turns them into real Minecraft servers: create, start, stop, configure, restart automatically, as Docker containers with `itzg/minecraft-server`.

### 1.1 Use case

mc-panel runs the operator's own servers for a circle of friends: one VPS, about 1–5 instances. Other users get at most power permissions (and, from sub-project 3 on, console permissions) on individual servers. It follows that convenience when creating a server matters, while tenant separation and hard quotas do not (spec of sub-project 1 §1.2: no tenants, no billing).

### 1.2 Success

An admin creates a server in the UI (type, version, RAM, name), starts and stops it, sees its status and resources and changes the most important `server.properties`. Friends connect through `<name>.<base domain>`. A crashed server restarts by itself; after a host reboot, the servers that should run are running again.

### 1.3 Unchanged guideline

A compromised panel must not get root on the host (sub-project 1 §3). Access to the Docker socket is equivalent to root, so the panel does not get it.

### 1.4 Non-goals (sub-project 2)

- Console, logs, file browser, plugin/mod management, player management (sub-project 3).
- Backups (sub-project 4), player/TPS monitoring and alerts (sub-project 5).
- Installing Docker on the VPS (sub-project 6; in CI, Docker is preinstalled on the runner). (Now: the installer installs `docker.io` if no Docker daemon answers, see `scripts/install/packages.sh`.)
- Bedrock Edition (UDP, no routing by hostname).
- A separate port per server, sleeping servers (auto-pause/wake), renaming servers.
- Applying the jails from sub-project 1 to Minecraft traffic (Docker passes it through the forward chain; the jail rule only applies to the panel).

## 2. Decisions

| Area | Decision | Reason |
|---|---|---|
| Runtime | Docker + `itzg/minecraft-server` | Covers all wanted types, maintains Java images, sets `server.properties` from environment variables |
| Docker access | Only the agent (root) talks to Docker | Keeps the security model of sub-project 1; alternatives in §2.1 |
| Docker client | Own narrow client over `net/http` on the Unix socket | Only the calls we need, no large SDK dependency |
| Reachability | Subdomain per server through `itzg/mc-router` on port 25565 | One port for all, no port management; needs wildcard DNS |
| Subdomain | = server name (`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$` is a valid DNS label) | No extra field |
| Configuration | Allowlisted properties in the DB, set as itzg environment variables | No file access for the panel; free editing comes with sub-project 3 |
| Types | vanilla, paper, purpur, fabric, neoforge, modrinth, curseforge | Operator's wish |

### 2.1 Rejected alternatives

- **The panel talks through a filtering Docker socket proxy:** whoever may create containers can set `privileged` or mount `/`; path filters do not prevent that.
- **Rootless Podman/Docker under a separate user, addressed directly by the panel:** keeps the host safe, but rootless networking adds latency to game traffic, memory limits need delegated cgroups, the host setup (sub-project 6) gets harder, and it departs from the model "panel unprivileged, agent privileged".

## 3. Architecture

```
Browser ──▶ panel (mcpanel, unprivileged)              agent (root)
            internal/servers                  ──JSON──▶ internal/agent/instances
            · DB: instances, settings          socket   · validates InstanceSpec
            · version catalogs (HTTP, cache)            · builds fixed container template
            · reconcile desired ↔ actual                · Docker Engine API (/var/run/docker.sock)
                                                        · writes route file for mc-router
                                                                 │
                    Docker network "mcpanel-mc" (no port to the outside except the router)
            ┌──────────────┐     ┌────────────────────┐ ┌──────────────────┐
 :25565 ───▶│ mcpanel-router│──▶ │ mcpanel-mc-1 (itzg)│ │ mcpanel-mc-2 …   │
            └──────────────┘     └────────────────────┘ └──────────────────┘
                                  /srv/mc-panel/instances/<id> → /data
```

### 3.1 panel: package `internal/servers`

- Takes over the server handlers from `internal/users/servers.go` (list, create, details, delete, power) together with the power limiter (`internal/users/powerlimit.go`, used only by the power route). Granting permissions (`/users/{id}/permissions`) stays in `internal/users`. (Now: both files are gone from `internal/users`; the server handlers live in `internal/servers` (`servers.go`, `power.go`) and the limiter in `internal/servers/powerlimit.go`.)
- The DB is the source of truth for the **desired state** of each instance.
- A reconcile loop (§5.3) brings the actual state at the agent there.
- The panel fetches the version catalogs (§6) itself; the agent needs no internet access except for image pulls by Docker.
- If the agent is unreachable, the UI stays usable; the actual state is shown as "unknown", changes to the desired state are stored and applied later.

### 3.2 agent: package `internal/agent/instances`

- **`spec`:** validation of `InstanceSpec` (§5.2) in its own package `internal/agent/instancespec`, like `banrules`: the panel checks with it before saving, and the agent checks every request again with the same code (defense in depth: the agent does not trust the panel).
- **`docker`:** narrow client for the Docker Engine API over the Unix socket, pinned to API version `/v1.44` (the oldest that Docker 29 accepts; Ubuntu 26.04 ships Docker 29.1.3). Supports only: inspect/pull image, inspect/create network, create/start/stop/remove/inspect/list containers (filter on label), stats (one snapshot, no stream).
- **`template`:** pure function `Spec → Docker container configuration` (§5.4).
- **`router`:** generates the route file for mc-router and writes it atomically (temporary file + `rename`).
- **Worker:** one serial queue per instance for all writing operations (§5.1).

The agent keeps no persistent state of its own. It reads which containers it manages from Docker (label `mcpanel.instance`). It never touches containers without this label.

### 3.3 File system on the host

```
/srv/mc-panel/                     root:root 0755   (instances_dir)
├── instances/<id>/                mcpanel-mc 0750  → /data in the container
├── router/                        root:root 0755   → /config (ro) in the router container
│   └── routes.json                root:root 0644
├── patches/                       root:root 0755   → /mcpanel-patches (ro) in every instance container (F9)
└── deleted/<name>-<timestamp>/    moved data of deleted servers
```

- `mcpanel-mc` is a separate system user (no login, no shell), created in CI by `scripts/ci-acceptance.sh` or `scripts/ci-instances.sh`, and later on the VPS by the installer. (Now: the installer creates it, see `scripts/install/layout.sh`.) The containers run directly as `<uid>:<gid>` of `mcpanel-mc` (Docker `User`); itzg then skips switching users and `chown`, so the agent creates `instances/<id>` with this owner.
- The panel (`mcpanel`) has no access to `/srv/mc-panel/instances`.
- `deleted/` is not emptied automatically; the UI says so when deleting.
- The panel does not limit disk space itself: in production, `instances_dir` and Docker's data directory sit on a separate file system (or with quotas), so that a server filling up does not block the panel database and logs. This is a requirement for the installer (sub-project 6).

(Now: the agent also keeps `instances/` at `root:mcpanel-mc` 0750 and `deleted/` at `root:root` 0700; sub-project 4 added `backups/` (`mcpanel-mc` 0700), see `internal/agent/instances/datadir.go`. Security review SEC-04 added `tmp/` (`root:root` 0700) with `tmp/<id>` (`mcpanel-mc` 0700) per server: the container's `/tmp`, outside `instances/<id>` so that the files service and backups never see it; the agent empties it before every create or recreate and removes it with the server.)

### 3.4 Configuration

New keys in `agent.toml` (all with defaults; the validator still rejects unknown keys):

```toml
docker_socket = "/var/run/docker.sock"
instances_dir = "/srv/mc-panel"
mc_user       = "mcpanel-mc"
[images]                                  # defaults in the code, pinned by digest
java8  = "itzg/minecraft-server:java8@sha256:…"
java17 = "itzg/minecraft-server:java17@sha256:…"
java21 = "itzg/minecraft-server:java21@sha256:…"
java25 = "itzg/minecraft-server:java25@sha256:…"
router = "itzg/mc-router@sha256:…"
```

- Image references must contain a digest; without one, the agent does not start.
- The panel only picks a key from the enum `java8|java17|java21|java25`, never an image.
- In dev mode (`make dev` on macOS), Docker is off: no manager, no containers, status "Docker not available" (nothing is created on the Mac without asking). Only with `MCPANEL_DEV_DOCKER=1` does the dev agent use the Docker socket of OrbStack/Docker Desktop, if present.

`panel.toml` gets no new required keys; the base domain and the CurseForge key are runtime settings in the DB (§4).

(Now, security review: further optional keys in `agent.toml`: `container_egress_deny` (SEC-02, see §5.4), `cpu_per_instance` (SEC-26, CPUs per server as `NanoCpus`, default `0` = no cap, allowed `0.01` to the host's CPU count) and `router_connection_rate_limit` (SEC-11, new connections per second through mc-router for all clients together, default 50; mc-router's own default of 1/s queued every join and ping behind one client).)

## 4. Data model

New migration `internal/store/migrations/00002_instances.sql`. `servers` stays unchanged.

```sql
CREATE TABLE instances (
    server_id      INTEGER PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    type           TEXT    NOT NULL CHECK (type IN ('vanilla', 'paper', 'purpur', 'fabric',
                                                    'neoforge', 'modrinth', 'curseforge')),
    version        TEXT    NOT NULL,          -- 'LATEST' or e.g. '1.21.4'
    loader_version TEXT,                      -- only fabric/neoforge; NULL = latest
    modpack        TEXT,                      -- only modrinth/curseforge
    java           TEXT    NOT NULL CHECK (java IN ('java8', 'java17', 'java21', 'java25')),
    memory_mib     INTEGER NOT NULL CHECK (memory_mib BETWEEN 1024 AND 32768),
    properties     TEXT    NOT NULL,          -- JSON object, allowlisted keys only (§7)
    desired_state  TEXT    NOT NULL CHECK (desired_state IN ('running', 'stopped')),
    revision       INTEGER NOT NULL,          -- +1 on every spec change
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE instance_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    base_domain        TEXT,                  -- e.g. 'mc.example.com'
    curseforge_api_key BLOB,                  -- encrypted with internal/secretbox
    updated_at         INTEGER NOT NULL
);
```

- sqlc queries in `internal/store/queries/servers.sql`, prefix `Servers…`.
- A server without an `instances` row (stub from sub-project 1) is called "not set up" in the UI and can only be deleted.
- `revision` starts at 1. The container carries the revision as a label; a mismatch = "restart required" (as does an outdated template label, §5.3).
- Server IDs are never reused (counter table from migration `00003`): otherwise a new server could inherit the container and world of a deleted server whose removal failed.
- `base_domain`: valid DNS name made of labels matching `^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`, at least two labels, ≤ 200 characters in total, stored in lower case. A server's FQDN = `<name>.<base_domain>` (≤ 253 characters).

## 5. Lifecycle

### 5.1 agent API

Fixed paths as in sub-project 1, IDs in the body, `DisallowUnknownFields`, peer UID check unchanged. Body limit for `/v1/instances/*` 16 KiB (other paths still 4 KiB).

| Method, path | Body | Response |
|---|---|---|
| `GET /v1/health` | – | as before plus `docker: "ok" \| "unavailable"` |
| `GET /v1/instances` | – | `{instances: [InstanceStatus], router: RouterStatus}` |
| `POST /v1/instances/ensure` | `{id, name, fqdn, revision, spec, recreate}` | `202` |
| `POST /v1/instances/power` | `{id, action}` with `start\|stop\|restart` | `202` |
| `POST /v1/instances/remove` | `{id}` | `202` |

`InstanceStatus` (RAM = `memory_stats.usage` minus `memory_stats.stats.inactive_file` or `cache`, CPU by the Docker API formula from `cpu_stats`/`precpu_stats`): `id`, `revision`, `state` (`created\|running\|restarting\|exited\|missing`), `exit_code`, `started_at` (Unix ms), `memory_bytes`, `memory_limit_bytes`, `cpu_percent`, `operation` (`""\|pulling\|creating\|starting\|stopping\|removing`), `last_error` (text, ≤ 500 characters, without secrets).

- **ensure:** makes sure that `mcpanel-mc-<id>` exists in exactly this revision. If the image is missing, it is pulled. If a container with an older revision exists and `recreate` is `false`, nothing happens. With `recreate: true` it is stopped, removed and created again; if it was running before, it is started afterwards. Creates the data directory (owner `mcpanel-mc`) if it is missing. Then rewrites the route file.
- **power:** acts on the existing container; if it is missing, the worker sets `last_error`.
- **remove:** stops and removes the container, moves `instances/<id>` to `deleted/<name>-<UTC timestamp>`, rewrites the route file.
- All writing calls return `202` immediately and go into the instance's queue. A new operation does not replace a running one; the queue holds at most 4 entries (otherwise `429`).
- `id` is the server ID (int64 > 0); `name` matches the name regex; `fqdn` starts with `<name>.` and is a valid DNS name (≤ 253 characters).

The router container `mcpanel-router` is ensured at agent start and on every ensure, as soon as Docker is available. `RouterStatus`: `state`, `last_error`.

### 5.2 InstanceSpec

```json
{
  "type": "paper",
  "version": "1.21.4",
  "loader_version": null,
  "modpack": null,
  "java": "java21",
  "memory_mib": 4096,
  "properties": { "motd": "Unser Server", "difficulty": "normal" },
  "curseforge_api_key": null
}
```

Rules (panel and agent):

| Field | Rule |
|---|---|
| `type` | Enum from §4 |
| `version` | `LATEST` or `^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$`; ignored and empty for modrinth/curseforge |
| `loader_version` | only fabric/neoforge, otherwise `null`; format like `version` |
| `modpack` | only modrinth: `^[a-z0-9-]{1,64}$`; only curseforge: slug like Modrinth or `https://www.curseforge.com/minecraft/modpacks/<slug>`; otherwise `null` |
| `java` | Enum from §4 |
| `memory_mib` | 1024–32768, multiple of 256 (Now, issue #79: the panel also caps it at the host's memory, `(MemTotal − 1 GiB) / 1.25` rounded down to 256 MiB, in `servers.MaxMemoryMiB` (`internal/servers/memory.go`, MemTotal from `internal/hostinfo`); create and update reject more with `400 validation_failed`. `GET /servers/memory` (admin) returns the limits, the memory already given to all servers and a recommended size per type for the form. Issue #158: one budget model for all of it: each container gets `memory_mib × 1.25` (`instancespec.ContainerMemoryNumerator/Denominator`, used by the agent's template), and the containers of all servers share `MemTotal − 1 GiB`; the endpoint returns the reserve, the overhead, the container budget and the containers' sum, recommends from half of the free container budget (`null` when not even the minimum fits) and the form warns, only warns, when the configured container limits exceed the budget, replacing the edited server's own allocation.) |
| `properties` | only keys and types from §7 |
| `curseforge_api_key` | only with `type = curseforge`, required there; `^[A-Za-z0-9$./_-]{1,128}$` |
| Combination | `type = curseforge` requires `java != java8` (the java8 image does not support CurseForge) |

In addition, the agent checks every value that goes into an environment variable for keys without `=` (fixed in the code) and values without control characters (`\x00`–`\x1f`, `\x7f`).

### 5.3 Reconciliation in the panel

A loop runs every 10 s (through `clock.Clock`) and additionally right after every user action (signal over a channel, non-blocking). Per instance:

| Desired | Actual | Action |
|---|---|---|
| any | `missing` | `ensure` (recreate `false`), then `start` if needed |
| any | older revision, not running | `ensure` with `recreate: true` (a server never starts with an old configuration; with desired `running`, `start` follows in the next pass) |
| `running` | older revision, running | nothing; `restart_required = true` |
| `running` | `exited`/`created` | `power start` |
| `stopped` | `running`/`restarting` | `power stop` |
| any | `operation != ""` | nothing (agent is working) |

- "Restart" by a user: if the revision is current → `power restart`; otherwise `ensure` with `recreate: true` (this starts it again because the container was running before).
- Template (Now, issue #123 F10): besides the revision, every container carries `mcpanel.template`, the SHA-256 over `TemplateEpoch` and the agent's container template for a canonical spec of its type and Java (`InstanceTemplateHash` in `internal/agent/instances`: image, user, mounts, limits, hardening, the shape of the environment; labels and spec values excluded), plus `mcpanel.type` and `mcpanel.java`. The agent reports `template_outdated` when the label is missing or differs from what it would create now (agent upgrade, changed host settings). The panel treats such a container like an older revision in the same revision: running → `restart_required = true`; not running → `ensure` with `recreate: true` before it starts; "Restart" → `ensure` with `recreate: true`, which the agent accepts for the same revision only when the template is outdated (an older revision stays a no-op). A container with a newer revision than the panel's (restored database) is never recreated for its template. A template change that shows only for non-canonical spec values bumps `TemplateEpoch`.
- Containers with a `mcpanel.instance` label that the DB does not know: log warning and shown in the settings; no automatic deletion.
- Agent errors (unreachable, `docker_unavailable`) abort the pass and are logged; the next pass tries again.
- If a pass triggered something, the next one follows immediately (e.g. `ensure`, then `start`). If the same action repeats for an instance without success (persistent `last_error`), the reconcile loop backs off exponentially: 10 s, 20 s, 40 s … at most 5 min.
- Crashes and automatic restarts appear only in the log, not in the audit log.

### 5.4 Container template

From a valid spec the agent generates exactly this configuration; none of it can be controlled by the panel:

- Name `mcpanel-mc-<id>`, hostname `<name>`.
- Image from `[images].<java>`.
- `User: "<uid>:<gid>"` of `mcpanel-mc`: the container never starts as root (itzg docs "Running as alternate user/group ID").
- Labels: `mcpanel.instance=<id>`, `mcpanel.revision=<revision>`, `mcpanel.name=<name>`, `mcpanel.fqdn=<fqdn>`.
- Network: only `mcpanel-mc` (bridge, created by the agent). No published ports.
- (Now, security review SEC-03: every instance has its own bridge network `mcpanel-net-<id>` (labels `mcpanel.network=1`, `mcpanel.instance=<id>`) and is on no other network, so a server cannot reach another one's RCON, game or plugin ports. The agent creates it before the container (on create and recreate; a recreate keeps it and its subnet) with an explicit /28 from `instance_network_pool` (`agent.toml`, IPv4 /8 to /24, default `10.213.0.0/16`, i.e. 4096 networks): the first block that overlaps no Docker network (any, not only its own), no host interface address and no route of the host (`/proc/net/route`; Docker's own bridges are skipped by their network IDs); networks are created with `EnableIPv4: true, EnableIPv6: false`, and a managed network that reports IPv6 is not used (`doctor` warns about an older `mcpanel-mc` with IPv6); Docker's "pool overlaps" answer moves on to the next block, one mutex serialises creation. A full pool fails the ensure (`instance network pool exhausted`, logged once per change). At start the agent warns when the pool overlaps a host network. `mcpanel-router` keeps `mcpanel-mc` as its home network (published port) and is connected to every instance network: right after the network is created, on every router refresh (also after the router is replaced), and disconnected from networks without an instance; only to networks the egress filter covers, and only a container that carries `mcpanel.router=1`; it reaches the backend by container name through Docker's embedded DNS. A start or restart through the agent first ensures the server's network: a network deleted from outside is created again, and an endpoint that still points to the deleted network (Docker 29 refuses to start such a container) is disconnected and connected again. Removing an instance disconnects the router and removes the network; the 10 s loop also removes labelled instance networks whose instance has no container, checked again under the network mutex (no queued or running operation, `mcpanel-mc-<id>` not found by name: a created container has no endpoint in Docker's network inspect) and never one that another container uses; a network called `mcpanel-net-<id>` without the labels for that ID is never disconnected or removed. The router is attached to N networks; at a few dozen servers this is no concern, the practical limit is Docker's iptables rules per bridge (a few hundred networks), far below the pool. Migration: a container of an older agent (on `mcpanel-mc`) is connected to its own network at once by the loop (players stay connected) and disconnected from `mcpanel-mc` only while stopped: by the loop when it sees it stopped, and by the agent's own stop, restart (between stop and start) and start; no recreate is needed (Docker keeps the stale `NetworkMode`). A crash restart by Docker's restart policy does not count. The loop logs the running instances still on `mcpanel-mc` whenever that list changes; `doctor` warns about them (`docker_isolation`). Monitoring pings go to mc-router's published port and RCON runs through `docker exec`, so neither needs a shared network.)
- Mount: `/srv/mc-panel/instances/<id>` → `/data` (bind, rw). No other mounts.
- `HostConfig`: `CapDrop: ["ALL"]` without `CapAdd`, `SecurityOpt: ["no-new-privileges:true"]`, `Privileged: false`, `ReadonlyRootfs: false` (itzg writes outside `/data`), `Memory = memory_mib × 1.25 MiB`, `MemorySwap = Memory`, `PidsLimit: 1024`, `RestartPolicy: unless-stopped`, `LogConfig`: `json-file` with `max-size=10m`, `max-file=3`.
- `StopTimeout: 60`.
- `CPUShares: 256` (cgroup v2 weight below that of the host services), so that a busy server does not crowd out the panel and agent.
- (Now, security review SEC-04 and SEC-26: `ReadonlyRootfs: true`; `/tmp` is a bind mount of `<instances_dir>/tmp/<id>` (disk-backed, not a tmpfs, which would be charged to the memory limit; modpacks stage the whole `.mrpack` there). Docker cannot set `nosuid,nodev` on a bind mount; `no-new-privileges` and `CapDrop ALL` (no `CAP_MKNOD`) cover that, as for `/data`. `Ulimits`: `nofile` 65536. Optional `NanoCpus` from `cpu_per_instance`. An existing container keeps the old template until its next recreate (e.g. a settings change).)
- (Now, SEC-02: egress filter. (Since SEC-03: the subnets of `mcpanel-mc` and of every `mcpanel-net-<id>`, one rule per network, so the exemption "destination inside the source's own network" never spans two servers; a server's network is covered before its container is created on it or connected to it.) The agent reads the subnets of the network `mcpanel-mc` and keeps a chain `forward` (priority -10, before Docker's) in its table `inet mcpanel`: new connections (`ct state new`) from the container subnet to a destination outside it that is in the sets `mcpanel_egress_deny4/6` are dropped. Default list `container_egress_deny`: `169.254.0.0/16`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, `fc00::/7`, `fe80::/10`; `[]` removes chain and sets. `ct state new` keeps replies to players from private networks working. The rules are in place before a container or the router is created on the network (fail closed) and follow subnet changes in the 10 s loop; an agent restart refreshes them without recreating containers. Containers still reach the internet and the host's own addresses (the input hook; `inet mcpanel_fw` governs those); DNS goes through Docker's embedded resolver.)
- An existing `mcpanel-mc-<id>` whose label `mcpanel.name` does not match the request (the FQDN may change, e.g. after a change of the base domain) belongs to a different server: the agent never adopts, starts or replaces it, but reports `last_error`. If two containers share an FQDN, mc-router routes to the container with the highest ID.
- Environment, fixed: `EULA=TRUE`, `TYPE`, `VERSION`, `MEMORY=<memory_mib>M`, `ENABLE_AUTOPAUSE=false`, `ENABLE_RCON=true` (reachable only in the internal network; for sub-project 3), `OVERRIDE_SERVER_PROPERTIES=true`; depending on the type `FABRIC_LOADER_VERSION`, `NEOFORGE_VERSION`, `MODRINTH_MODPACK` (with `TYPE=MODRINTH`), `CF_SLUG`/`CF_PAGE_URL` and `CF_API_KEY` (with `TYPE=AUTO_CURSEFORGE`; `type` → `TYPE`: vanilla→`VANILLA`, paper→`PAPER`, purpur→`PURPUR`, fabric→`FABRIC`, neoforge→`NEOFORGE`, modrinth→`MODRINTH`, curseforge→`AUTO_CURSEFORGE`).
- (Now, security review F9/SEC-11: paper and purpur also get `PATCH_DEFINITIONS=/mcpanel-patches/bukkit-connection-throttle.json`, an itzg patch definition that sets `settings.connection-throttle: -1` in `bukkit.yml`. Every player reaches the server through mc-router and so has the router's address; Bukkit's default throttle of 4000 ms refused a second player joining within 4 s ("Connection throttled"). The agent writes the patch definitions (`instancespec.Patches`) atomically into `<instances_dir>/patches/` (`root:root` 0755, files 0644) at start and before every create and start, never into a data directory, and every instance container mounts that directory read-only at `/mcpanel-patches`. itzg downloads the default `bukkit.yml` before it patches, so the setting applies from the first start, and the patch runs before every start, so a hand edit does not survive a restart. Like every template change it reaches an existing container at its next recreate. Behind the router the servers see only the router's address: IP bans (`ban-ip`) and IP-based anti-bot plugins or throttles do not work; ban players by name.)
- Environment from `properties` according to §7, plus fixed `EXISTING_OPS_FILE=SYNCHRONIZE` and `EXISTING_WHITELIST_FILE=SYNCHRONIZE`: after every (re)start, ops and whitelist match exactly the lists in the panel (entries granted in-game are overwritten; the UI points this out). If the lists are empty or the whitelist is off, the agent empties `ops.json` or `whitelist.json` before creating the container. All other itzg variables (`JVM_OPTS`, `GENERIC_PACK`, `MODS`, `PLUGINS`, …) cannot be set.
  (Now: since sub-project 3a the panel sets neither `OPS`/`WHITELIST` nor `EXISTING_*_FILE`; ops and whitelist belong to the server's own `ops.json`/`whitelist.json` and are changed through RCON, see `internal/agent/instancespec/env.go`. Only `whitelist_enabled` (`ENABLE_WHITELIST`) is still a property. The agent no longer empties `ops.json` or `whitelist.json` before creating a container; it only reads them for the players area (`internal/agent/instances/players.go`).)

(Now: the router also runs with a read-only rootfs, `PidsLimit: 128` and `-connection-rate-limit <router_connection_rate_limit>`; its changed template replaces it once after an update.)

Router container `mcpanel-router`: image `[images].router`, network `mcpanel-mc`, port `25565/tcp` published on the host, mount `/srv/mc-panel/router` → `/config` (ro, a directory instead of a single file so that a `rename` becomes visible), started with `-routes-config /config/routes.json` **without** `-routes-config-watch`, same `HostConfig` hardening, `RestartPolicy: unless-stopped`; the image already runs as UID 65532. Route file: `{"mappings": {"<fqdn>": "mcpanel-mc-<id>:25565"}}`. After every write the agent sends `SIGHUP` (`POST /containers/mcpanel-router/kill?signal=SIGHUP`): only reloading via SIGHUP removes deleted routes, mc-router's file watching only adds (verified against mc-router 1.47.2). The router gets no Docker socket.

(Now, web ports spec `2026-10-17-mc-panel-web-ports-design.md`: the web router `mcpanel-web` publishes one HTTP port of a server under its own name. It is a Caddy container (`[images].web`, uid 65532, read-only rootfs, tmpfs `/data` and `/config`, `CapDrop ALL` plus only `NET_BIND_SERVICE`, which the image's file capability on `/usr/bin/caddy` needs for `exec`; label `mcpanel.web=1`, template hash `mcpanel.web-template`) on its own network `mcpanel-web` (labels `mcpanel.network=1`, `mcpanel.web=1`, a /28 from `instance_network_pool`, covered by the egress filter like every managed network), publishing `web_proxy_port` and `web_ask_port` on `127.0.0.1` only. It reads `<instances_dir>/web/caddy.json` (root-owned, mounted read-only, Caddy's file watch, no admin API). The panel sends the complete route list (`PUT /v1/web/routes`); the agent stores it, answers at once and applies it asynchronously: without a list since the agent started it changes nothing, an empty list removes container, network and rules. `mcpanel-web` is connected only to the networks `mcpanel-net-<id>` of servers with a route, with the fixed address `WebAddress` (the last usable host of the /28), and only after table `bridge mcpanel_web` allows that address exactly the published port in that subnet and drops everything else from it; it leaves a network before its rule goes, and if the rules cannot be applied it is disconnected from every server network. Isolation between servers is unchanged: `mcpanel-web` reaches a server only on its published port, and no server reaches another through it. Removing an instance and the orphan cleanup disconnect `mcpanel-web` like the router.)

### 5.5 Automatic restart

- Crash: Docker restarts the container (`unless-stopped`, with Docker's own backoff).
- Host reboot: Docker restarts containers with `unless-stopped` that were not explicitly stopped; the reconcile loop corrects everything else based on `desired_state`.
- Only a user changes `desired_state` (power action), never the reconcile loop.

## 6. Version catalogs

`GET /api/servers/catalog/{type}` (admin). The panel queries fixed upstream URLs; only the enum `type` picks the source.

| Type | Source | Returns |
|---|---|---|
| vanilla | `https://piston-meta.mojang.com/mc/game/version_manifest_v2.json` | Releases, newest first |
| paper | PaperMC Fill API v3 (`https://fill.papermc.io/v3/projects/paper/versions`, per version `…/versions/{v}/builds?channel=STABLE`) | MC versions with at least one STABLE build (itzg by default uses only STABLE/RECOMMENDED; e.g. 26.3 currently has none) |
| purpur | `https://api.purpurmc.org/v2/purpur` | MC versions up to and including `metadata.current` (newer ones count as experimental) |
| fabric | `https://meta.fabricmc.net/v2/versions/game`, `…/loader` | stable game versions, loader versions |
| neoforge | NeoForged Maven API (`https://maven.neoforged.net/api/maven/versions/releases/net/neoforged/neoforge`) | NeoForge versions without `-beta`/`-alpha`, grouped by MC version (`20.4.x` → 1.20.4, `21.0.x` → 1.21, `26.1.2.x` → 26.1.2, `26.2.0.x` → 26.2) |
| modrinth, curseforge | – | `404`; the pack determines the version |

- Response: `{versions: [{id, java}], loaders?: [string]}`; `java` is the derived recommendation (§6.1).
- In-memory cache of 1 h per type; timeout 10 s, response size ≤ 5 MiB; errors → `502 catalog_unavailable` (the form then offers free text).
- The endpoints and formats were checked against the live services (2026-10-10); shortened real responses are kept as sample responses in `testdata/`. The Fill API requires a User-Agent with contact details: `mc-panel/<version> (https://github.com/Bernotese/mc-panel)` (used for all catalog requests). Paper queries the STABLE builds only for the newest 10 versions (older ones all have STABLE builds).

### 6.1 Java derivation

| MC version | Java |
|---|---|
| ≤ 1.16.5 | java8 |
| 1.17 – 1.20.4 | java17 |
| 1.20.5 – 1.21.x | java21 |
| ≥ 26.1 (year scheme) and `LATEST` | java25 |
| Modpacks | java21 (default) |

An admin can override the value under "Advanced". The derivation lives in the panel (Go) and is delivered with the catalog for the form; the frontend computes free-text versions with the same table (shared test cases).

## 7. Properties (allowlist)

| Key (`properties`) | Type / limits | itzg variable | Default |
|---|---|---|---|
| `motd` | text ≤ 150 characters, printable, no line break | `MOTD` | server name |
| `difficulty` | `peaceful\|easy\|normal\|hard` | `DIFFICULTY` | `normal` |
| `gamemode` | `survival\|creative\|adventure\|spectator` | `MODE` | `survival` |
| `max_players` | 1–500 | `MAX_PLAYERS` | 20 |
| `pvp` | bool | `PVP` | `true` |
| `hardcore` | bool | `HARDCORE` | `false` |
| `allow_flight` | bool | `ALLOW_FLIGHT` | `false` |
| `enable_command_block` | bool | `ENABLE_COMMAND_BLOCK` | `false` |
| `online_mode` | bool (UI warns on `false`) | `ONLINE_MODE` | `true` |
| `whitelist_enabled` | bool | `ENABLE_WHITELIST` | `false` |
| `whitelist` | ≤ 200 names `^[A-Za-z0-9_]{3,16}$` | `WHITELIST` (comma-separated) | empty |
| `ops` | ≤ 50 names, as above | `OPS` | empty |
| `view_distance` | 3–32 | `VIEW_DISTANCE` | 10 |
| `simulation_distance` | 3–32 | `SIMULATION_DISTANCE` | 10 |
| `spawn_protection` | 0–256 | `SPAWN_PROTECTION` | 16 |
| `seed` | text ≤ 64, printable | `SEED` | empty |
| `level_type` | `normal\|flat\|large_biomes\|amplified` | `LEVEL_TYPE` (`minecraft:…`) | `normal` |

- Missing keys = default; unknown keys → `400 validation_failed` (panel) or `400` (agent).
- `seed` and `level_type` only take effect for a new world; the UI says so.
- (Now, SEC-28: the API returns `seed` only to admins and users with `server.files`, who can read it from the world files anyway; a `server.view`-only user does not see it.)
- (Now: sub-project 3a removed `whitelist` and `ops` from the allowlist; see the note in §5.4 and `internal/agent/instancespec/properties.go`.)

## 8. panel API

All routes go through `*rbac.Router`, are described in `api/openapi.yaml` and have an MSW mock.

| Route | Rule | Content |
|---|---|---|
| `GET /servers` | Authenticated | as before plus `type`, `version`, `state`, `address`, `restart_required`; `null` fields for servers that are not set up |
| `POST /servers` | Admin + sudo | name + instance configuration + `eula_accepted: true` (required); `201` with `state: "creating"` (or `"unknown"` if the agent is currently unreachable); `409 base_domain_missing` without a base domain, `409 conflict` if the name is taken |
| `GET /servers/{id}` | `server.view` | plus configuration, `properties`, actual state (`InstanceStatus` without internal fields), `restart_required`, `last_error` |
| `PATCH /servers/{id}` | Admin + sudo | `version`, `loader_version`, `modpack`, `java`, `memory_mib`, `properties` (partial); increments `revision`; the response contains `restart_required` |
| `DELETE /servers/{id}` | Admin + sudo | `remove` to the agent; DB rows are gone as soon as the agent has answered `202`; data goes to `deleted/` |
| `POST /servers/{id}/power` | `server.power` | `start\|stop\|restart` sets `desired_state` (or triggers a restart, §5.3); `202`; power limiter as in sub-project 1 |
| `GET /servers/catalog/{type}` | Admin | §6 |
| `GET /servers/settings` | Admin | `base_domain`, `curseforge_api_key_set`, `docker` (status), `unknown_containers` |
| `PUT /servers/settings` | Admin + sudo | `base_domain`, `curseforge_api_key` (a string sets it, `null` removes it, missing = unchanged) |

- `address` = `<name>.<base_domain>`; `null` without a base domain.
- If the agent is unreachable, read endpoints return the desired state with `state: "unknown"`; writing endpoints store the desired state and answer normally (the reconcile loop catches up). Exception: `DELETE` of a set-up server answers `503 agent_unavailable` without the agent, so that no orphaned containers remain. Servers that are not set up (without an `instances` row) are deleted by the panel without the agent.
- If a `remove` fails asynchronously after the `202`, the container stays behind and shows up as an unknown container in the settings.
- Only admins see `last_error` in plain text; other users get an empty text and the UI a general notice (Docker errors contain paths, UIDs and image digests).
- Changing the CurseForge key increments the `revision` of all CurseForge instances; running ones show "restart required".
- The FQDN is part of the container configuration (label, route file). A change of the base domain therefore increments the `revision` of all instances: the reconcile loop recreates stopped servers, running ones show "restart required" and stay reachable under the old address until then.

### 8.1 Error codes

New in `internal/httpx/errors.go` and in the contract's enum:

| Code | Status | When |
|---|---|---|
| `docker_unavailable` | 503 | agent reachable, Docker not |
| `base_domain_missing` | 409 | creating a server without a base domain |
| `catalog_unavailable` | 502 | upstream catalog unreachable |

Missing CurseForge key → `400 validation_failed`. Asynchronous agent errors appear as `last_error`, not as HTTP errors.

### 8.2 Audit

`server.create` (configuration without secrets), `server.update` (diff of the changed fields), `server.delete`, `server.power` (as in sub-project 1), `server.settings_update` (base domain old/new, `curseforge_api_key` only as `set`/`removed`). New as constants in `internal/audit/types.go`: `ActionServerUpdate`, `ActionServerSettingsUpdate`.

## 9. Frontend

Existing shadcn components; no visual redesign in this sub-project.

- **Dashboard (`/`):** `ServerCard` with name, address (copy), status badge (running, stopped, starting, error, unknown, not set up), type/version, RAM usage, power buttons with `server.power`, notice "restart required", link to the detail page. If the base domain is missing, admins see a notice with a link to the settings, and "Create server" is disabled.
- **Create server (`/servers/new`, admin):** replaces `create-server-dialog.tsx`. Sections: name (with live address), type (with short description), version/loader/modpack (catalog, free text as fallback, Java derived, "Advanced"), RAM (1–32 GiB, default 4 GiB), properties (collapsed, defaults from §7), EULA checkbox with link (required). Button "Create server" → sudo → detail page.
- **Detail page (`/servers/$id`):** tabs Overview (`server.view`: status, address, RAM/CPU, start time, `last_error`, power buttons, banner "restart required" with "Restart now"), Settings (admin: form without name/type, "Save changes", notice while the server is running), Delete (admin: type the name, the text names the target folder).
- **Settings (`/settings`, admin, new navigation item):** section "Minecraft servers" with base domain (example for the DNS record `*.<base domain>`), CurseForge key (write-only, "stored", remove), Docker/agent status, unknown containers.
- **Refresh:** TanStack Query every 3 s while an operation is running or the state differs from `desired_state`, otherwise every 15 s.
- Texts `de`/`en`, usable on mobile, MSW mocks for all new operations.

(Now: sub-project 7 replaced the tabs with routes under `/servers/$id` (overview, `monitoring`, `console`, `players`, `files`, `plugins`, `backups`, `settings`); deleting a server is the danger zone at the end of its settings.)

## 10. Development, CI and acceptance

- **Mac:** only `make check` (CLAUDE.md). `make dev` runs without Docker (§3.4); the UI preview (`make preview`, mock API) shows the new pages without a backend.
- **CI:** unit, integration and Playwright tests as before; in addition the agent's Docker integration tests (`dockerintegration`, job `nftables`) and the new job `instances`: `scripts/ci-instances.sh` starts the real agent with Docker on the throwaway runner, creates a Paper server and checks routing through mc-router (Minecraft status ping with hostname, `scripts/mc-status.py`), hardening, crash restart and deletion.
- **Manual acceptance:** the user checks real Minecraft clients, several server types and host reboots separately on a test server with `docs/dev/acceptance.md` ("Instances").

## 11. Tests

| Level | Content |
|---|---|
| agent `spec` | Table tests for every rule from §5.2 and §7, incl. injection attempts (`=`, line break, null byte, overlong values, unknown keys) |
| agent `template` | Golden test: spec → exactly the expected container configuration (caps, mounts, limits, labels, env); negative test: no spec produces extra mounts, ports or env variables |
| agent `docker` | Fake Docker server (`httptest` on a Unix socket): lifecycle, label filter (foreign containers are never touched), error paths, timeouts |
| agent `router` | Route file from the container list, atomic write, empty list |
| agent worker | Serial processing per instance, queue limit, `last_error` |
| agent integration | Build tag `dockerintegration` (Linux only): real Docker daemon, a small pinned test image instead of itzg (through the `[images]` configuration); new step in the existing Linux CI job |
| panel `servers` | Handlers with `storetest` + fake agent: permissions (view/power/admin), sudo, validation, audit entries, agent unreachable |
| panel reconcile | Fake clock + fake agent: every row of the table in §5.3, agent fails and comes back, outdated revision, base domain change |
| panel catalog | `httptest` upstreams with sample responses from `testdata/`, cache, timeout, size limit, Java derivation |
| Contract | existing tests: OpenAPI ↔ routes, error codes ↔ enum, mock per operation |
| Frontend | Vitest: Java derivation (same cases as Go), form validation, status badge; Playwright against the mock: create server → detail page → change a setting → "restart required" → restart |

The conventions from sub-project 1 apply unchanged (only the standard library in Go tests, `clock.Clock`, contexts everywhere).

## 12. Acceptance criteria

Automatically in CI: 1, 7 and the agent side of 2, 6 and 8 (`scripts/ci-instances.sh`). Manually by the user on a test server: 2–6 and 8 with a real client (`docs/dev/acceptance.md` ("Instances")).

1. All CI jobs green, including `dockerintegration` and `instances`.
2. An admin creates a Paper server; it starts, and a Minecraft client connects through `<name>.<base domain>`.
3. One server each with Vanilla, Fabric, NeoForge and a Modrinth pack starts.
4. A user with `server.power` on server A can start and stop A but not change it; access to server B → `403`.
5. Changing RAM or MOTD → "restart required"; after the restart the new configuration applies and the world is kept.
6. A server crash (e.g. `rcon-cli stop` in the container) → automatic restart; after a host reboot, all servers with `desired_state = running` are running again.
7. `docker inspect` shows: caps reduced to the minimum from §5.4, `no-new-privileges`, memory limit, only `/data` mounted, no published ports except at the router. The panel user has no access to `docker.sock` and `/srv/mc-panel/instances`.
8. Deleting removes the container and the route, the data is under `deleted/`; all admin actions appear in the audit log, `panel audit verify` reports OK.
