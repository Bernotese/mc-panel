# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

mc-panel is a self-hosted Minecraft server panel: a Go backend with an embedded React SPA, released as static `linux/amd64` and `linux/arm64` binaries. Development happens locally on macOS (Apple Silicon); production is an Ubuntu 26.04 LTS VPS (panel, agent and the files service as systemd services behind Caddy). Project docs are written in German:

- `docs/dev/README.md`: toolchain versions, CLI commands, CI jobs and how to reproduce them locally, lint conventions, release process, production setup.
- `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`: architecture, security requirements and the roadmap of sub-projects (1 foundation, 2 instances, 3a console, 3b files, 3c plugins and 4 backups are done; 5 monitoring and 6 installer are specified).
- `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md`: Minecraft instances (sub-project 2); manual acceptance checklist in `docs/dev/acceptance-tp2.md`.
- `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md`: console, live log and player management (sub-project 3a); manual acceptance checklist in `docs/dev/acceptance-tp3a.md`.
- `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md`: file browser through the unprivileged files service (sub-project 3b); manual acceptance checklist in `docs/dev/acceptance-tp3b.md`.
- `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md`: plugins and mods from Modrinth and Hangar, installed through the files service (sub-project 3c); manual acceptance checklist in `docs/dev/acceptance-tp3c.md`.
- `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md`: backups of the server data directories into local restic repositories through the files service (sub-project 4); manual acceptance checklist in `docs/dev/acceptance-tp4.md`.

## Working rules

- **Checks on the Mac:** only `make check` (gofmt, `go vet` and `go build` for macOS and Linux incl. the root-only integration test files, TypeScript types; ~10 s). Never run `make test`, `make test-race`, `make lint`, Playwright or `make release` on the Mac: the Mac stays clean.
- **Tests on the test VM before every push:** `make test-vm [SUITE=all|go|lint|web|e2e]` copies the working tree (tracked and new files, no push needed) to its own directory on the test VM and runs the suites there as an ordinary user (race tests, golangci-lint + eslint + tsc, Vitest, Playwright against the mock and the real dev backend; the deployed panel is untouched). Run at least the suites your change touches; `all` before a PR is marked ready. SSH to the VM needs the Bash sandbox disabled. The root-only acceptance (nftables, Docker, instances) runs only in CI.
- **CI is the gate before merging:** a pull request runs only the jobs its changed files need (job `changes`), pushes to `main` and the weekly run run everything. After pushing, wait for the summary check with `gh pr checks <n> --watch` (job `CI OK`) and fix failures from the CI logs (`gh run view <id> --log-failed`).
- **Nothing system-level on the Mac without asking:** never install or uninstall software (brew, npm -g), create VMs or Docker containers/images, use `sudo`, or change system settings automatically. Propose the command and let the user run it (`! <command>`). Scripts that change the host (`scripts/ci-acceptance.sh`) are for CI runners only.
- **Test VM:** `claude@192.168.1.117` is an internal Ubuntu 26.04 VM for manual testing (passwordless sudo, outbound internet only). Unlike the Mac, Claude may install and change anything there. `make deploy-testvm [REF=<pushed branch, tag or sha>]` builds that ref on the VM from GitHub and redeploys it; the UI is at `https://panel.192-168-1-117.sslip.io/` (Caddy `tls internal`). Details in `docs/dev/README.md` ("Test-VM").
- **Git and GitHub:** the repo is `Bernotese/mc-panel`; use `gh`. Never commit to or push `main`. Every change goes on a branch (`feat/…`, `fix/…`, `docs/…`, `chore/…`) and into a PR whose CI must be green. Merging PRs, deleting remote branches and pushing tags (a `v*` tag publishes a GitHub release) only after the user says so.
- **Implementation with agent teams:** plan tasks (`docs/superpowers/plans/`; a sub-project's plans are deleted once it is merged, its spec stays) are implemented by Claude Code agent teams (enabled in `.claude/settings.json`). The lead splits the work into tasks with disjoint file sets and integrates; teammates load this file but not the lead's conversation, so each spawn prompt names the plan file, the task numbers and the branch. Each teammate works in its own git worktree on its own branch (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`), runs `make check` and `make test-vm` before reporting a task done, and never edits files assigned to another teammate. Keep teams small (3–5 teammates) and shut teammates down when their work is merged.

## Commands

```bash
(cd web && npm ci)          # once
make dev                    # agent + files -dev + `panel serve -dev` + Vite; UI at http://localhost:5173, all state in ./.dev/
printf '%s\n' '<pw>' | .dev/bin/panel admin create -dev -username admin   # first admin (while make dev runs)

make check                  # the local check (format, vet, build for macOS + Linux, tsc)
make preview                # UI preview in Docker at http://localhost:5180 (mock API); the user starts it, never Claude
make generate               # sqlc generate + regenerate web/src/api/schema.d.ts from api/openapi.yaml

make test-vm [SUITE=…]       # tests on the test VM (all|go|lint|web|e2e); the Mac never runs them

# CI and the test VM (via make test-vm) run these; never on the Mac:
make test                   # go test ./... + vitest
make test-race              # needs CGO_ENABLED=1 (Makefile sets it)
make lint                   # golangci-lint + eslint + tsc

make build                  # web build, then bin/panel, bin/agent and bin/files
make clean                  # also wipes .dev/ (resets the dev DB)

# A single test on the VM, after make test-vm has copied the tree (dir = basename of the checkout):
ssh claude@192.168.1.117 'cd ~/mc-panel-test/<dir> && PATH=/usr/local/go/bin:$PATH go test ./internal/rbac -run TestName'
ssh claude@192.168.1.117 'cd ~/mc-panel-test/<dir>/web && PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run src/lib/theme.test.ts'

cd web && npm run dev:mock  # frontend against the MSW mock API, no Go needed
```

CI and the test VM use golangci-lint v2.14.0 (`scripts/vm-test.sh` installs it on the VM; Linux-only files are linted there). nftables integration tests and the end-to-end jail check (`scripts/ci-acceptance.sh`) run only in CI on Linux as root.

CI fails if generated code is out of date. Commit regenerated files after you change SQL queries (`internal/store/*.sql.go`) or `api/openapi.yaml` (`web/src/api/schema.d.ts`).

## Architecture

There are three binaries, built from `cmd/panel`, `cmd/agent` and `cmd/files`, and they are separated by privilege:

- **panel** runs unprivileged as user `mcpanel`. It serves the web app and `/api`, and in production it listens on a Unix socket behind Caddy. It owns the SQLite DB (`modernc.org/sqlite`, no CGO). It also provides the CLI subcommands `serve`, `version`, `admin create`, `admin unlock` and `audit verify`.
- **agent** runs as root and exposes a narrow JSON API over a Unix socket (`internal/agent`). It accepts connections only from the configured panel UID, checked via peer credentials. It validates every request strictly, rejecting unknown fields, and checks ban prefixes in `agentapi`. It manages the nftables sets `mcpanel_jail4`/`mcpanel_jail6` in table `inet mcpanel` and is the only Docker client: Minecraft servers are containers built from a fixed, hardened template (`internal/agent/instances`), validated with `internal/agent/instancespec` (shared with the panel), and routed by hostname through the `mcpanel-router` container (mc-router, reloaded via SIGHUP). For the console it streams container logs (Docker `logs?follow`, sent to the panel as NDJSON), runs `rcon-cli` in the container via Docker exec (argv, no shell) and reads an instance's player list files through `os.Root` with `O_NOFOLLOW|O_NONBLOCK` (regular files ≤ 1 MiB only): the data directory is controlled by the container, so the root agent never follows a path out of it. In dev mode Docker is off unless `MCPANEL_DEV_DOCKER=1`. The design goal is that a compromised panel does not give root on the host. If the agent fails, the panel reports `503 agent_unavailable`; in-app jails still apply.
- **files** runs as `mcpanel-mc` (the Minecraft containers' user) with no capabilities and `no_new_privs`, never as root (it refuses to start as root). It serves the file browser over its own Unix socket (`internal/files`; contract and path rules in `internal/files/filesapi`, shared with the panel) and accepts only the panel UID and root (like the agent); in production systemd creates the socket (`mcpanel-files.socket`, group `mcpanel`, 0660) and hands it over via `LISTEN_FDS`, so the service needs no supplementary groups. Every request opens `<instances_dir>/instances/<id>` with `os.Root`; symlinks are never followed (`Lstat` before, `Stat` + `os.SameFile` after opening), `.rcon-cli.*` and `.mcpanel-*` are hidden (case-insensitive), `rcon.password` in `server.properties` is redacted on read, download and folder zip (and kept on write). Uploads and downloads are streams, zip extraction is a job (pre-checked central directory, temp dir, re-counted sizes; one job per instance). `POST /v1/jars` lists the top-level `.jar`/`.jar.disabled` files of `plugins/` or `mods/` with sha256/sha512 (cached by device/inode, size and mtime) and best-effort metadata from `plugin.yml`/`paper-plugin.yml`/`fabric.mod.json`/`neoforge.mods.toml`; `PUT /v1/upload` takes an optional `sha256` or `sha512` and compares before `fsync`/`rename` (`400 hash_mismatch`, nothing left behind). It also runs backups (`/v1/backups/*`) with restic as a child process (`restic_binary`, empty environment, `--no-cache`) that never opens a path in a data directory: a backup is a tar stream the service writes from its `os.Root` walk (no symlinks or special files, hidden entries skipped, `rcon.password` redacted) into `restic backup --stdin`, ending with a `.mcpanel-end` entry (entry and byte counts) without which restore and download refuse the snapshot; restore and download read `restic dump` as a tar stream (restore: extracted with the zip rules into a temp dir, then the visible top-level entries are swapped by `rename`, journaled in `backups/restore-journal/<id>` so that the next start rolls an interrupted swap back or forward, and a swap that cannot be undone stays `stuck` and blocks further restores of that server until an operator resolves it; download: converted to a zip on the fly). One repo per server in `<instances_dir>/backups/repos/<id>` with its key in `backups/keys/<id>` (`backups/` is `mcpanel-mc` 0700, created by the agent); snapshot kind, pin and note are restic tags. One job per instance across extract/backup/restore/forget; without restic `/v1/backups/status` reports `available: false` and the other backup endpoints except purge answer `503 unavailable`. The service has no network access. The root agent never touches user-supplied file paths or archives.

Linux-specific code sits behind interfaces and is chosen with build tags (`_linux.go` / `_other.go` / `_darwin.go`). On macOS the firewall is a no-op that only logs, and peer credentials use `LOCAL_PEERCRED`.

### Request path and routing

`cmd/panel/app.go` (`buildApp`) is the only place that wires the packages together. Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `serverfiles`, `plugins`, `backups`, `audit/auditapi`) has a `Handler` with a `Routes(api *rbac.Router)` method.

- Register routes only through `*rbac.Router`, with paths relative to `/api`, and give every route a `Rule`: `rbac.Public()`, `Authenticated()`, `Admin()` or `Server(perm, "param")`, optionally with `.WithSudo()`. Registering a route without a rule panics, and `TestEveryRouteDeclaresARule` catches any route added to chi directly. Feature code never calls `Route()`; it is used once, for `/api`.
- `cmd/panel/openapi_test.go` requires the registered routes to match the operations in `api/openapi.yaml` exactly. It also requires the `httpx` `Code*` error constants to match the contract's error enum. A new endpoint therefore needs a handler, an OpenAPI entry, regenerated TS types and an MSW mock handler (`web/src/mocks/handlers.test.ts` checks that every operation has a mock).
- Errors are always `{"error":"<code>","message":"..."}`, written via `httpx.WriteError`, with codes only from `internal/httpx/errors.go`. JSON is `snake_case` and IDs are int64.
- Everything outside `/api` goes to the embedded SPA (`internal/webui`, `go:embed` of `internal/webui/dist`, which the Vite build fills).

### Data

- There is one goose migration set in `internal/store/migrations`. Migrations run at startup, and the panel refuses to start if one fails.
- sqlc queries are split by domain in `internal/store/queries/*.sql`, and each generates its own `internal/store/<name>.sql.go` (`emit_interface: false`). Query names carry a domain prefix (`Auth…`, `Defense…`, `Rbac…`, `Audit…`, `Servers…`, `Plugins…`, `Backups…`).
- Timestamps are `INTEGER` Unix milliseconds (`*_at` columns); convert with `time.UnixMilli` / `t.UnixMilli()`.
- The audit log is append-only and hash-chained: `hash = SHA-256(prev_hash || canonical JSON)`, written serially in a transaction. The API never updates or deletes audit entries.
- Tests use `storetest.Open(t)`, which returns a migrated temporary SQLite file, plus fixtures such as `storetest.CreateUser`.

### Minecraft instances

- The panel stores the desired state per server (`instances` table: type, version, java, memory, allowlisted properties, `desired_state`, `revision`) and `internal/servers` reconciles it with the agent every 10 s and after each user action. Spec changes bump `revision`; a running container with an older `mcpanel.revision` label shows "restart required".
- Server name = subdomain (`<name>.<base_domain>`, base domain in `instance_settings`). Container `mcpanel-mc-<id>`, data in `<instances_dir>/instances/<id>`.
- Only `instancespec.Env` decides container environment variables; never add itzg variables elsewhere.
- Version catalogs (`internal/servers/catalog`) and the plugin upstreams (`internal/plugins/upstream`) fetch fixed upstream URLs with a contact User-Agent and in-memory caches.
- Ops, whitelist and bans belong to the server itself (`ops.json`, `whitelist.json`, `banned-players.json` in its data directory): the panel never sets `OPS`/`WHITELIST`; the Players tab reads the files through the agent and changes them through RCON. `whitelist_enabled` stays an instance property.
- `internal/console` serves the live log as Server-Sent Events (`/servers/{id}/console/stream`: per-write deadlines via `http.ResponseController` instead of the server's `WriteTimeout`, reconnects to the agent across container restarts, re-checks session and permission every 30 s) and console commands and player actions as normal POST requests (CSRF, 30/min per user, audited as `server.console_command`/`server.player_action`). Player actions map an enum to fixed RCON commands; user text never becomes a command. `server.view` sees online players, `server.console` everything else.
- `internal/serverfiles` exposes `/servers/{id}/files/*` (`server.files`, which means full trust: uploading a plugin runs code in the container). `POST …/files/upload` and `PUT …/files/content` are exempt from the 1 MiB body limit; upload and download run with per-operation deadlines instead of the server timeouts (no buffering, 2 uploads per user); writes, uploads, renames, deletes, extracts and downloads are audited as `file.*`. Extracting needs a stopped server (agent state); while an extract job runs, power start/restart answer `409` and the reconcile loop does not start the server.
- `internal/plugins` exposes `/servers/{id}/plugins/*` (`server.plugins`, full trust) for paper/purpur (plugins from Modrinth and Hangar, `plugins/`) and fabric/neoforge (mods from Modrinth, `mods/`); other types answer `409 plugins_unsupported`. `internal/plugins/upstream` talks to fixed bases (`api.modrinth.com`, `hangar.papermc.io`) and downloads jars and icons only from `cdn.modrinth.com`/`hangarcdn.papermc.io` (https, redirects only there). The panel streams each jar to the files service with the upstream checksum (`UploadChecked`); it never writes plugin files itself. The disk is the source of truth; `plugin_installs` only remembers the origin of panel-installed jars (filename + sha512), other jars are matched via Modrinth `version_files` or shown as manual. Install/update run as in-memory jobs (one per server, four per panel, dependencies first); icons go through the proxy `/servers/{id}/plugins/icon` (CSP unchanged). Installs, updates, toggles and removals are audited as `plugin.*`, one entry per jar.
- `internal/backups` exposes `/servers/{id}/backups/*` (`server.backups`: reads every file via the download and can roll the server back; restore and snapshot delete need sudo). restic is the source of truth for snapshots; `backup_schedules` holds schedule (`off|interval|daily|weekly`, time zone `instance_settings.backup_timezone`), retention (`keep_*`, applied with `restic forget` to scheduled snapshots only, `pinned` kept), the last scheduled run and `saveoff_since`. A minute ticker (`clock.Clock`) queues due runs; one backup/restore/delete job runs panel-wide (FIFO, one per server, `409 backup_running`). A running server is backed up between RCON `save-off`/`save-all flush` and `save-on`; `save-on` follows on every path, and after a crash the panel sends it at start (`saveoff_since`). A restore holds the data gate (`servers.DataGate`, shared with extract: no start, no reconcile start), stops the server, takes a `pre-restore` snapshot, restores, and lets the reconcile loop start it again. Failed scheduled backups and restores notify `backup.failed` (once per 6 h per server); user actions are audited as `backup.*`; `DELETE /servers/{id}?purge_backups=1` removes the repo.
- Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints, the files service, plugin installs from the real Modrinth and Hangar and backup/restore/download with a real restic through the panel in production mode) run only in CI.

### Frontend (`web/`)

React + TypeScript + Vite, TanStack Router (file-based in `src/routes`) and Query, Tailwind and shadcn/ui (`src/components/ui`), react-i18next with `de` as the default and `en`. API types are generated from the OpenAPI spec, and the fetch wrapper in `src/api` sends the CSRF token (`X-CSRF-Token`) automatically. A global sudo dialog handles `sudo_required` and retries the action. No inline scripts or external CDNs are allowed (CSP). `npm run build` refuses to build with MSW active. The console tab reads the log with `EventSource` and renders it with `@tanstack/react-virtual`; ANSI and `§` colour codes become spans, never HTML.

## Conventions

- All time-dependent logic uses `clock.Clock` (`internal/clock`), never `time.Now()` directly, so tests can use a fake clock.
- Go tests use only the standard library (no testify etc.).
- Requests and DB calls always take a context (`NewRequestWithContext`, `QueryContext`/`ExecContext`).
- Lint exceptions go into `.golangci.yml` as a targeted rule (path, linter and text, with a reason) or into `//nolint:<linter> // <reason>` on the line. Never disable a linter globally. The `common-false-positives` preset is deliberately off, so gosec G103/G204/G304 need a justification each time.
- Imports are formatted with gofmt + goimports, with local prefix `github.com/Bernotese/mc-panel` (`golangci-lint fmt ./...`).
- The string `mcpanel` (nftables table and sets, system users `mcpanel`/`mcpanel-mc`, Docker names `mcpanel-mc-<id>`/`mcpanel-router`/network `mcpanel-mc`, labels `mcpanel.*`, `mcpanel.theme` localStorage key) is a runtime name, not the module path. Do not rename it.
- Commits use Conventional Commits.
