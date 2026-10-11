# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

mc-panel is a self-hosted Minecraft server panel: a Go backend with an embedded React SPA, released as static `linux/amd64` and `linux/arm64` binaries. Development happens locally on macOS (Apple Silicon); production is an Ubuntu 26.04 LTS VPS (panel and agent as systemd services behind Caddy). Project docs are written in German:

- `docs/dev/README.md`: toolchain versions, CLI commands, CI jobs and how to reproduce them locally, lint conventions, release process, production setup.
- `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`: architecture, security requirements and the roadmap of sub-projects (1 foundation, 2 instances and 3a console are done).
- `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md`: Minecraft instances (sub-project 2); manual acceptance checklist in `docs/dev/acceptance-tp2.md`.
- `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md`: console, live log and player management (sub-project 3a); manual acceptance checklist in `docs/dev/acceptance-tp3a.md`.

## Working rules

- **Checks on the Mac:** only `make check` (gofmt, `go vet` and `go build` for macOS and Linux incl. the root-only integration test files, TypeScript types; ~10 s). Do not run `make test`, `make test-race`, `make lint`, Playwright or `make release` locally unless the user asks: CI runs all of them on every PR. After pushing, wait for CI with `gh pr checks <n> --watch` and fix failures from the CI logs (`gh run view <id> --log-failed`).
- **Nothing system-level on the Mac without asking:** never install or uninstall software (brew, npm -g), create VMs or Docker containers/images, use `sudo`, or change system settings automatically. Propose the command and let the user run it (`! <command>`). Scripts that change the host (`scripts/ci-acceptance.sh`) are for CI runners only.
- **Test VM:** `claude@192.168.1.117` is an internal Ubuntu 26.04 VM for manual testing (passwordless sudo, outbound internet only). Unlike the Mac, Claude may install and change anything there. `make deploy-testvm [REF=<pushed branch, tag or sha>]` builds that ref on the VM from GitHub and redeploys it; the UI is at `https://panel.192-168-1-117.sslip.io/` (Caddy `tls internal`). Details in `docs/dev/README.md` ("Test-VM").
- **Git and GitHub:** the repo is `Bernotese/mc-panel`; use `gh`. Never commit to or push `main`. Every change goes on a branch (`feat/…`, `fix/…`, `docs/…`, `chore/…`) and into a PR whose CI must be green. Merging PRs, deleting remote branches and pushing tags (a `v*` tag publishes a GitHub release) only after the user says so.
- **Implementation with agent teams:** plan tasks (`docs/superpowers/plans/`; a sub-project's plans are deleted once it is merged, its spec stays) are implemented by Claude Code agent teams (enabled in `.claude/settings.json`). The lead splits the work into tasks with disjoint file sets and integrates; teammates load this file but not the lead's conversation, so each spawn prompt names the plan file, the task numbers and the branch. Each teammate works in its own git worktree on its own branch (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`), runs `make check` before reporting a task done, and never edits files assigned to another teammate. Keep teams small (3–5 teammates) and shut teammates down when their work is merged.

## Commands

```bash
(cd web && npm ci)          # once
make dev                    # agent + `panel serve -dev` + Vite; UI at http://localhost:5173, all state in ./.dev/
printf '%s\n' '<pw>' | .dev/bin/panel admin create -dev -username admin   # first admin (while make dev runs)

make check                  # the local check (format, vet, build for macOS + Linux, tsc)
make preview                # UI preview in Docker at http://localhost:5180 (mock API); the user starts it, never Claude
make generate               # sqlc generate + regenerate web/src/api/schema.d.ts from api/openapi.yaml

# CI runs these on every PR; locally only on request:
make test                   # go test ./... + vitest
make test-race              # needs CGO_ENABLED=1 (Makefile sets it)
make lint                   # golangci-lint + eslint + tsc
make build                  # web build, then bin/panel and bin/agent
make clean                  # also wipes .dev/ (resets the dev DB)

go test ./internal/rbac -run TestName                 # single Go test
cd web && npx vitest run src/lib/theme.test.ts        # single frontend test file
cd web && npm run dev:mock                            # frontend against the MSW mock API, no Go needed
cd web && npm run e2e                                 # Playwright against the mock (port 5174)
GOOS=linux golangci-lint run ./...                    # lint Linux-only files from macOS
```

CI uses golangci-lint v2.14.0 (`docs/dev/README.md` describes an exact local install). nftables integration tests and the end-to-end jail check (`scripts/ci-acceptance.sh`) run only in CI on Linux as root.

CI fails if generated code is out of date. Commit regenerated files after you change SQL queries (`internal/store/*.sql.go`) or `api/openapi.yaml` (`web/src/api/schema.d.ts`).

## Architecture

There are two binaries, built from `cmd/panel` and `cmd/agent`, and they are separated by privilege:

- **panel** runs unprivileged as user `mcpanel`. It serves the web app and `/api`, and in production it listens on a Unix socket behind Caddy. It owns the SQLite DB (`modernc.org/sqlite`, no CGO). It also provides the CLI subcommands `serve`, `version`, `admin create`, `admin unlock` and `audit verify`.
- **agent** runs as root and exposes a narrow JSON API over a Unix socket (`internal/agent`). It accepts connections only from the configured panel UID, checked via peer credentials. It validates every request strictly, rejecting unknown fields, and checks ban prefixes in `agentapi`. It manages the nftables sets `mcpanel_jail4`/`mcpanel_jail6` in table `inet mcpanel` and is the only Docker client: Minecraft servers are containers built from a fixed, hardened template (`internal/agent/instances`), validated with `internal/agent/instancespec` (shared with the panel), and routed by hostname through the `mcpanel-router` container (mc-router, reloaded via SIGHUP). For the console it streams container logs (Docker `logs?follow`, sent to the panel as NDJSON), runs `rcon-cli` in the container via Docker exec (argv, no shell) and reads an instance's player list files through `os.Root` with `O_NOFOLLOW|O_NONBLOCK` (regular files ≤ 1 MiB only): the data directory is controlled by the container, so the root agent never follows a path out of it. In dev mode Docker is off unless `MCPANEL_DEV_DOCKER=1`. The design goal is that a compromised panel does not give root on the host. If the agent fails, the panel reports `503 agent_unavailable`; in-app jails still apply.

Linux-specific code sits behind interfaces and is chosen with build tags (`_linux.go` / `_other.go` / `_darwin.go`). On macOS the firewall is a no-op that only logs, and peer credentials use `LOCAL_PEERCRED`.

### Request path and routing

`cmd/panel/app.go` (`buildApp`) is the only place that wires the packages together. Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `audit/auditapi`) has a `Handler` with a `Routes(api *rbac.Router)` method.

- Register routes only through `*rbac.Router`, with paths relative to `/api`, and give every route a `Rule`: `rbac.Public()`, `Authenticated()`, `Admin()` or `Server(perm, "param")`, optionally with `.WithSudo()`. Registering a route without a rule panics, and `TestEveryRouteDeclaresARule` catches any route added to chi directly. Feature code never calls `Route()`; it is used once, for `/api`.
- `cmd/panel/openapi_test.go` requires the registered routes to match the operations in `api/openapi.yaml` exactly. It also requires the `httpx` `Code*` error constants to match the contract's error enum. A new endpoint therefore needs a handler, an OpenAPI entry, regenerated TS types and an MSW mock handler (`web/src/mocks/handlers.test.ts` checks that every operation has a mock).
- Errors are always `{"error":"<code>","message":"..."}`, written via `httpx.WriteError`, with codes only from `internal/httpx/errors.go`. JSON is `snake_case` and IDs are int64.
- Everything outside `/api` goes to the embedded SPA (`internal/webui`, `go:embed` of `internal/webui/dist`, which the Vite build fills).

### Data

- There is one goose migration set in `internal/store/migrations`. Migrations run at startup, and the panel refuses to start if one fails.
- sqlc queries are split by domain in `internal/store/queries/*.sql`, and each generates its own `internal/store/<name>.sql.go` (`emit_interface: false`). Query names carry a domain prefix (`Auth…`, `Defense…`, `Rbac…`, `Audit…`, `Servers…`).
- Timestamps are `INTEGER` Unix milliseconds (`*_at` columns); convert with `time.UnixMilli` / `t.UnixMilli()`.
- The audit log is append-only and hash-chained: `hash = SHA-256(prev_hash || canonical JSON)`, written serially in a transaction. The API never updates or deletes audit entries.
- Tests use `storetest.Open(t)`, which returns a migrated temporary SQLite file, plus fixtures such as `storetest.CreateUser`.

### Minecraft instances

- The panel stores the desired state per server (`instances` table: type, version, java, memory, allowlisted properties, `desired_state`, `revision`) and `internal/servers` reconciles it with the agent every 10 s and after each user action. Spec changes bump `revision`; a running container with an older `mcpanel.revision` label shows "restart required".
- Server name = subdomain (`<name>.<base_domain>`, base domain in `instance_settings`). Container `mcpanel-mc-<id>`, data in `<instances_dir>/instances/<id>`.
- Only `instancespec.Env` decides container environment variables; never add itzg variables elsewhere.
- Version catalogs (`internal/servers/catalog`) fetch fixed upstream URLs with a contact User-Agent and a 1 h cache.
- Ops, whitelist and bans belong to the server itself (`ops.json`, `whitelist.json`, `banned-players.json` in its data directory): the panel never sets `OPS`/`WHITELIST`; the Players tab reads the files through the agent and changes them through RCON. `whitelist_enabled` stays an instance property.
- `internal/console` serves the live log as Server-Sent Events (`/servers/{id}/console/stream`: per-write deadlines via `http.ResponseController` instead of the server's `WriteTimeout`, reconnects to the agent across container restarts, re-checks session and permission every 30 s) and console commands and player actions as normal POST requests (CSRF, 30/min per user, audited as `server.console_command`/`server.player_action`). Player actions map an enum to fixed RCON commands; user text never becomes a command. `server.view` sees online players, `server.console` everything else.
- Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints) run only in CI.

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
