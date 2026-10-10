# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

mc-panel is a self-hosted Minecraft server panel: a Go backend with an embedded React SPA, released as static `linux/amd64` and `linux/arm64` binaries. Development happens on macOS (Apple Silicon). Project docs are written in German:

- `docs/dev/README.md`: toolchain versions, CLI commands, CI jobs and how to reproduce them locally, lint conventions, release process, production setup.
- `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`: architecture and security requirements.

## Commands

```bash
(cd web && npm ci)          # once
make dev                    # agent + `panel serve -dev` + Vite; UI at http://localhost:5173, all state in ./.dev/
printf '%s\n' '<pw>' | .dev/bin/panel admin create -dev -username admin   # first admin (while make dev runs)

make test                   # go test ./... + vitest
make test-race              # needs CGO_ENABLED=1 (Makefile sets it)
make lint                   # golangci-lint + eslint + tsc
make generate               # sqlc generate + regenerate web/src/api/schema.d.ts from api/openapi.yaml
make build                  # web build, then bin/panel and bin/agent
make clean                  # also wipes .dev/ (resets the dev DB)

go test ./internal/rbac -run TestName                 # single Go test
cd web && npx vitest run src/lib/theme.test.ts        # single frontend test file
cd web && npm run dev:mock                            # frontend against the MSW mock API, no Go needed
cd web && npm run e2e                                 # Playwright against the mock (port 5174)
GOOS=linux golangci-lint run ./...                    # lint Linux-only files from macOS
```

CI uses golangci-lint v2.14.0. For an exact local match, install it into `.dev/tools` as described in `docs/dev/README.md`. nftables integration tests run only on Linux as root: `go test -tags nftintegration -exec sudo ./internal/agent/...`.

CI fails if generated code is out of date. Commit regenerated files after you change SQL queries (`internal/store/*.sql.go`) or `api/openapi.yaml` (`web/src/api/schema.d.ts`).

## Architecture

There are two binaries, built from `cmd/panel` and `cmd/agent`, and they are separated by privilege:

- **panel** runs unprivileged as user `mcpanel`. It serves the web app and `/api`, and in production it listens on a Unix socket behind Caddy. It owns the SQLite DB (`modernc.org/sqlite`, no CGO). It also provides the CLI subcommands `serve`, `admin create`, `admin unlock` and `audit verify`.
- **agent** runs as root and exposes a narrow JSON API over a Unix socket (`internal/agent`). It accepts connections only from the configured panel UID, checked via peer credentials. It validates every request strictly, rejecting unknown fields, and checks ban prefixes in `agentapi`. It manages the nftables sets `mcpanel_jail4`/`mcpanel_jail6` in table `inet mcpanel`. The design goal is that a compromised panel does not give root on the host. If the agent fails, the panel reports `503 agent_unavailable`; in-app jails still apply.

Linux-specific code sits behind interfaces and is chosen with build tags (`_linux.go` / `_other.go` / `_darwin.go`). On macOS the firewall is a no-op that only logs, and peer credentials use `LOCAL_PEERCRED`.

### Request path and routing

`cmd/panel/app.go` (`buildApp`) is the only place that wires the packages together. Each feature package (`auth`, `defense`, `notify`, `users`, `audit/auditapi`) has a `Handler` with a `Routes(api *rbac.Router)` method.

- Register routes only through `*rbac.Router`, with paths relative to `/api`, and give every route a `Rule`: `rbac.Public()`, `Authenticated()`, `Admin()` or `Server(perm, "param")`, optionally with `.WithSudo()`. Registering a route without a rule panics, and `TestEveryRouteDeclaresARule` catches any route added to chi directly. Feature code never calls `Route()`; it is used once, for `/api`.
- `cmd/panel/openapi_test.go` requires the registered routes to match the operations in `api/openapi.yaml` exactly. It also requires the `httpx` `Code*` error constants to match the contract's error enum. A new endpoint therefore needs a handler, an OpenAPI entry, regenerated TS types and an MSW mock handler (`web/src/mocks/handlers.test.ts` checks that every operation has a mock).
- Errors are always `{"error":"<code>","message":"..."}`, written via `httpx.WriteError`, with codes only from `internal/httpx/errors.go`. JSON is `snake_case` and IDs are int64.
- Everything outside `/api` goes to the embedded SPA (`internal/webui`, `go:embed` of `internal/webui/dist`, which the Vite build fills).

### Data

- There is one goose migration set in `internal/store/migrations`. Migrations run at startup, and the panel refuses to start if one fails.
- sqlc queries are split by domain in `internal/store/queries/*.sql`, and each generates its own `internal/store/<name>.sql.go` (`emit_interface: false`). Query names carry a domain prefix (`Auth…`, `Defense…`, `Rbac…`, `Audit…`).
- Timestamps are `INTEGER` Unix milliseconds (`*_at` columns); convert with `time.UnixMilli` / `t.UnixMilli()`.
- The audit log is append-only and hash-chained: `hash = SHA-256(prev_hash || canonical JSON)`, written serially in a transaction. The API never updates or deletes audit entries.
- Tests use `storetest.Open(t)`, which returns a migrated temporary SQLite file, plus fixtures such as `storetest.CreateUser`.

### Frontend (`web/`)

React + TypeScript + Vite, TanStack Router (file-based in `src/routes`) and Query, Tailwind and shadcn/ui (`src/components/ui`), react-i18next with `de` as the default and `en`. API types are generated from the OpenAPI spec, and the fetch wrapper in `src/api` sends the CSRF token (`X-CSRF-Token`) automatically. A global sudo dialog handles `sudo_required` and retries the action. No inline scripts or external CDNs are allowed (CSP). `npm run build` refuses to build with MSW active.

## Conventions

- All time-dependent logic uses `clock.Clock` (`internal/clock`), never `time.Now()` directly, so tests can use a fake clock.
- Go tests use only the standard library (no testify etc.).
- Requests and DB calls always take a context (`NewRequestWithContext`, `QueryContext`/`ExecContext`).
- Lint exceptions go into `.golangci.yml` as a targeted rule (path, linter and text, with a reason) or into `//nolint:<linter> // <reason>` on the line. Never disable a linter globally. The `common-false-positives` preset is deliberately off, so gosec G103/G204/G304 need a justification each time.
- Imports are formatted with gofmt + goimports, with local prefix `github.com/Bernotese/mc-panel` (`golangci-lint fmt ./...`).
- The string `mcpanel` (nftables table and sets, system user, `mcpanel.theme` localStorage key) is a runtime name, not the module path. Do not rename it.
- Commits use Conventional Commits.
