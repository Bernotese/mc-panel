# mc-panel – Sub-project 1: Foundation + security (design spec)

- **Date:** 2026-10-08
- **Status:** implemented (PR #1, 2026-10-10)
- **Author:** Jannes (with Claude)

## 1. Goal and context

mc-panel is a self-built web panel (a learning and hobby project) for managing your own Minecraft servers on a publicly reachable Linux VPS. Because the panel is reachable from the internet, security has top priority and should be visible and analysable in the app itself.

Development happens locally on macOS; the finished product is deployed to the VPS by an installer (now: `install.sh`, sub-project 6).

### 1.1 Overall roadmap

| # | Sub-project | Content (short) |
|---|---|---|
| **1** | **Foundation + security** (implemented) | **Repo, panel/agent, auth, 2FA, sessions, permissions, audit, defense, Security Center** |
| 2 | Instance lifecycle (implemented, spec `2026-10-10-mc-panel-instances-design.md`) | Docker + `itzg/minecraft-server`, versions/engines, resource limits, ports/firewall, config editor, auto-restart |
| 3a | Live operation: console (implemented, spec `2026-10-11-mc-panel-console-players-design.md`) | Console (SSE + RCON), live log, player management |
| 3b | Live operation: files (implemented, spec `2026-10-11-mc-panel-files-design.md`) | File browser through a separate service `mcpanel-files` |
| 3c | Live operation: plugins/mods (implemented, spec `2026-10-12-mc-panel-plugins-design.md`) | Plugins/mods (Modrinth, Hangar) |
| 4 | Backups (implemented, spec `2026-10-12-mc-panel-backups-design.md`; offsite deferred) | Schedules, retention, local restic repos, consistent snapshots, restore, zip download |
| 5 | Monitoring + alerts (implemented, spec `2026-10-13-mc-panel-monitoring-design.md`) | Players, TPS/MSPT, resources, history, alerts (Discord, ntfy) |
| 6 | Installer (implemented, spec `2026-10-13-mc-panel-installer-design.md`) | `install`/`update`/`doctor`/`uninstall`, Caddy + Let's Encrypt, VPS hardening |
| 7 | UI redesign (implemented, spec `2026-10-14-mc-panel-ui-redesign-design.md`) | Own design (Minecraft/gaming, dark + light), icon rail + server tree, server areas as routes, multiplayer list |

Each sub-project has its own cycle of spec → plan → implementation. **This document specifies only sub-project 1.**

### 1.2 Non-goals (overall)

Multi-node operation, billing, tenants/customer accounts, public registration.

### 1.3 Non-goals (sub-project 1)

Docker/Minecraft instances, installer, Caddy configuration, backups, monitoring. Servers exist only as stub records (for permission tests).

## 2. Technology decisions

| Area | Choice | Reason |
|---|---|---|
| Backend | Go | Static binaries, good Docker SDK, light on resources |
| HTTP | `chi` | Lean, idiomatic, middleware ecosystem |
| DB | SQLite via `modernc.org/sqlite` | No CGO → easy cross-compiling; a VPS needs no DB server |
| Queries/migrations | `sqlc`, `goose` | Type-safe SQL, versioned migrations |
| Logging | `log/slog` (JSON in prod) | Standard library |
| 2FA | `pquerna/otp`, `go-webauthn/webauthn` | Established libraries |
| Password | `golang.org/x/crypto/argon2` (argon2id) | Current standard |
| Frontend | React + TypeScript + Vite | Largest ecosystem (xterm.js, Monaco, charts later) |
| Frontend libs | TanStack Router/Query, Tailwind, shadcn/ui, react-i18next | Type-safe routing, data caching, fast UI, de/en |
| API contract | OpenAPI 3.1 (`api/openapi.yaml`), TS types via `openapi-typescript` | Frontend and backend stay in sync |
| Release | GoReleaser | Cross-compiling, checksums, GitHub Releases |
| Reverse proxy (prod, sub-project 6) | Caddy | Automatic Let's Encrypt, HSTS, HTTP/3 |

## 3. Architecture

```
Internet ─443─▶ Caddy ─▶ /run/mc-panel/panel.sock  panel  (user mcpanel, SQLite, React via go:embed)
                                            │  Unix socket (0660, group mcpanel)
                                            │  + peer UID check
                                            ▼
                                          agent  (root or docker group)
                                            • Health
                                            • firewall.ban / unban / list (nftables set)
                                            • (sub-project 2+: Docker operations)
```

(Now: the agent runs as root; the panel socket is `/run/mc-panel-web/panel.sock`, created by systemd (`mcpanel-panel.socket`, group `caddy`) and set up by the installer, see `deploy/systemd/` and `deploy/config/panel.toml`. The agent socket stays `/run/mc-panel/agent.sock`. Sub-project 3b added a third binary, `files`, with its own socket `/run/mc-panel-files/files.sock`.)

### 3.1 Processes

- **panel**: public web app + API. Runs unprivileged and in prod listens on the Unix socket `/run/mc-panel/panel.sock` (0660, only Caddy reaches it; `listen = "unix:…"`, dev: `127.0.0.1:8080`). (Now: `/run/mc-panel-web/panel.sock`, see §3.) It does not know the Docker socket.
- **agent**: small, privileged component with a narrow, strictly validated JSON API over a Unix socket. Accepts connections only from the configured panel UID (peer credentials). Every request is validated against a schema; unknown fields are rejected.
- Goal: a vulnerability in the panel does not lead directly to root on the host.

### 3.2 Repo structure

```
cmd/panel/            main: HTTP server + CLI subcommands (serve, admin create, admin unlock, audit verify)
cmd/agent/            main: agent daemon
internal/config/      configuration (file + env), dev/prod profile
internal/store/       DB access (sqlc-generated) + store interfaces
internal/auth/        password, TOTP, WebAuthn, recovery, sessions, CSRF, sudo
internal/rbac/        roles, per-server permissions, middleware
internal/users/       user management
internal/audit/       audit log with hash chain
internal/defense/     rate limits, jail, IP rules, GeoIP
internal/notify/      notifier interface, Discord, ntfy
internal/agent/       agent server, agent client, firewall backends, peer creds
internal/httpx/       shared HTTP helpers (error format, client IP, security headers)
internal/clock/       clock interface (testable time)
migrations/           goose migrations
api/openapi.yaml      API contract
web/                  React app (Vite)
```

Convention: each feature package exports `Routes(r chi.Router, deps …)`; only `cmd/panel` wires the packages together.

(Now: migrations live in `internal/store/migrations`; feature handlers have a method `Routes(api *rbac.Router)` and `cmd/panel/app.go` (`buildApp`) wires them. Later sub-projects added `cmd/files`, `internal/files`, `internal/servers`, `internal/console`, `internal/serverfiles`, `internal/plugins`, `internal/backups` and `internal/monitoring`.)

### 3.3 Platform abstraction

Linux-specific code sits behind interfaces, implementations are chosen by build tag:

| Interface | Linux | macOS (dev) |
|---|---|---|
| `Firewall` | nftables (sets `mcpanel_jail4` and `mcpanel_jail6` in a separate table `inet mcpanel`) | No-op, only logs |
| Peer credentials | `SO_PEERCRED` | `LOCAL_PEERCRED` (`getpeereid`) |

### 3.4 Configuration

- Prod: `/etc/mc-panel/panel.toml`, `/etc/mc-panel/agent.toml`, data under `/var/lib/mc-panel/`, socket `/run/mc-panel/agent.sock`.
- Dev (`--dev`): everything under `./.dev/`, socket `./.dev/agent.sock`, logs as text.
- Secrets (e.g. webhook URLs) are stored in the DB and shown only masked in the UI.
- `trusted_proxies`: by default only `127.0.0.1/32` and `::1/128`; `X-Forwarded-For` is taken only from there. Connections over the Unix socket always count as coming from the proxy (X-Forwarded-For, rightmost untrusted entry; without a usable X-Forwarded-For the request is rejected). Loopback is exempt from the admin allowlist only in dev mode.

### 3.5 Development and release

- `make dev`: starts agent + panel with `--dev` and the Vite dev server (proxy `/api` → panel). Browsers treat `localhost` as a secure context, so `Secure`/`__Host-` cookies work. (Now: `make dev` also starts the files service.)
- Production-like tests (nftables, root) run in CI on Ubuntu (`scripts/ci-acceptance.sh`); the former Lima VM was removed on 2026-10-10.
- `make release`: frontend build → `go:embed` → GoReleaser for `linux/amd64` and `linux/arm64` with SHA256 checksums. (Now: `make release` builds a local GoReleaser snapshot; a published release comes from the `release` workflow on a `v*` tag, see `docs/dev/README.md` ("Release").)

## 4. Authentication and sessions

### 4.1 Passwords

- argon2id (m=64 MiB, t=3, p=2; parameters stored in the hash string, re-hash on the next login when the parameters change).
- Minimum length 12, maximum length 256, checked against an embedded list of common passwords (e.g. the top 100k).
  (Now, security review SEC-18: the list comes from SecLists (MIT licence); passwords with at most 3 distinct characters are rejected as well.)
- Unknown user: a dummy hash is computed anyway; the error message is always "wrong username or password".

### 4.2 Second factor

- **TOTP** (RFC 6238, 30 s, 6 digits, ±1 step tolerance, replay protection: the last used time step is stored). Secret encrypted in the DB (AES-GCM, key from a server secret file).
- **WebAuthn/passkeys**: several credentials per user, nameable, deletable. RP ID = configured domain (dev: `localhost`).
- **Recovery codes**: 10 codes, 10 characters each, stored only hashed, single use, can be regenerated (sudo).
- **Mandatory**: admins must set up 2FA before they can do anything else (forced setup flow). Can be enforced for `user` by a global setting.
  (Now, security review SEC-10: 2FA is also mandatory for every `user` who holds a full-trust permission (`server.console`, `server.files`, `server.plugins` or `server.backups`, `rbac.FullTrustPermissions`) on any server. The policy is evaluated on every request: a full session of a user without a factor who gets such a permission is downgraded to `setup` at the next request (`403 setup_2fa_required` on full routes; enrolment and logout stay possible), and losing the permission makes 2FA optional again (existing factors are kept, the last one may then be removed; a session still waiting in `setup` without a factor continues as `full`, like a new password login would). Open console streams follow at their 5 s recheck. `Me.two_factor_reason` (`none`, `admin`, `permissions`, `policy`) tells the setup page why; the permission matrix notes the requirement.)
- (Now, SEC-15: setting up the **first** second factor asks for the current password, because there is no factor yet to confirm sudo with.)

### 4.3 Login flow

1. `POST /api/auth/login` (username, password) → on success a pre-auth session (valid only for the 2FA step, 5 min).
2. `POST /api/auth/2fa/totp` | `/api/auth/2fa/webauthn/*` | `/api/auth/2fa/recovery` → full session.
3. New session ID after every privilege change (protection against session fixation).

Every attempt (success/failure, step, IP, user agent, username) is recorded in `login_attempts`.

### 4.4 Sessions

- Server-side in SQLite; the cookie holds a 256-bit random token, the DB stores only its SHA-256.
- Cookie: `__Host-sid`, `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`.
- Idle timeout 30 min, absolute lifetime 12 h (configurable).
- Stored: user, created, last active, IP, user agent, country (GeoIP), sudo until.
- Profile page: list of active sessions, revoke one or all others. A password change revokes all other sessions.
- (Now, SEC-19 and SEC-45: requests the UI sends in the background (polling) carry `X-MCPanel-Background: 1` and do not extend the idle time (honoured only on `GET`/`HEAD`); a role change ends all sessions of the target user.)

### 4.5 Sudo mode

Sensitive actions require a 2FA confirmation that is at most 10 min old. These include: creating/deleting users, changing roles and permissions, changing 2FA methods, regenerating recovery codes, lifting jails, changing IP rules and notifiers (later: writing files, installing plugins). Otherwise the API answers `403 {"error":"sudo_required"}` and the frontend shows the confirmation dialog.

(Now: writing files and installing plugins do not need sudo; `server.files` and `server.plugins` are full-trust permissions instead. Later sub-projects added sudo to creating, changing and deleting servers and changing instance settings (sub-project 2), restoring and deleting backups (sub-project 4) and monitoring settings (sub-project 5).)

### 4.6 CSRF and headers

- `SameSite=Strict` + check of `Sec-Fetch-Site` (only `same-origin`/`none`) or `Origin` on state-changing requests + synchronizer token (`X-CSRF-Token`, bound to the session).
- Security headers from the panel itself (independent of Caddy): strict CSP (`default-src 'self'`, no inline script), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, minimal `Permissions-Policy`, `frame-ancestors 'none'`.
  (Now, security review SEC-31, SEC-43, SEC-48: no `'unsafe-inline'` for styles either; `index.html` gets a per-request style nonce. `img-src 'self'` without `data:` (the TOTP QR code is rendered as SVG). Trusted Types run report-only (`require-trusted-types-for 'script'; trusted-types 'none'`, no report endpoint: violations appear in the browser console and fail the e2e tests), to be enforced later.)
- Request body limit (default 1 MiB), timeouts for header/read/write.

## 5. Permissions

- Roles: `admin` (all permissions, global) and `user` (only explicitly granted permissions).
- Per-server permissions: `server.view`, `server.power`; planned for later sub-projects: `server.console`, `server.files`, `server.plugins`, `server.backups`. (Now: all six exist.)
- Deny by default; new users have no permissions.
- (Now, SEC-40: user IDs are never reused (a counter table, like server IDs), so a new user does not inherit the last login or known devices of a deleted one, and audit actor IDs stay unambiguous.)
- Enforced in the middleware `rbac.Require(perm, serverIDFromPath)`: every route declares its permission; a test ensures that no route is registered without a permission declaration. (Now: routes are registered through `*rbac.Router` with a `Rule` (`rbac.Public()`, `Authenticated()`, `Admin()`, `Server(perm, "param")`, optionally `.WithSudo()`); `TestEveryRouteDeclaresARule` checks it.)
- The `servers` table exists as a stub (id, name) so that permissions and UI can be tested. (Now: sub-project 2 added the `instances` table with the real server configuration.)

## 6. Audit log

- Append-only table: id, time, actor (user ID or `system`/`cli`), action (e.g. `user.create`), target, IP, result, details (JSON), `prev_hash`, `hash`.
- `hash = SHA-256(prev_hash || canonical JSON of the entry)`; writes are serialized in a transaction.
- `panel audit verify` checks the whole chain and reports the first broken entry. (Now: with `-journal -` it also compares the DB with the anchors in the journal; exit 1 for changed or missing entries, 3 for entries that match only a later anchor, as after restoring the DB.)
- No update/delete through the API. An optional retention period is left to a later sub-project. (Now: there is still no retention; the audit log keeps every entry, and no query deletes from it.)

## 7. Defense and Security Center

### 7.1 Rate limits

Token bucket (in memory, per process):

| Target | Key | Limit (default) |
|---|---|---|
| Login | IP | 10/min, burst 5 |
| Login | Username | 5/min |
| 2FA | Pre-auth session + IP | 5/min |
| API in general | Session | 300/min |
| Failed 2FA/sudo attempts | User | 10 failures / 15 min → 2FA locked for 15 min (`429`) |
| `server.power` | User | 10/min |

Exceeding a limit → `429` with `Retry-After`.

(Now, SEC-09: the username bucket counts only failed logins, and a device that signed in successfully as that user within the last 30 days (known device) skips it and gets a reserved argon2 slot, so it can still sign in while the username is under attack; a new device still gets `429` (a rate limit, not a lock-out).)

### 7.2 Jail

- Failed attempts (password or 2FA) per IP: 5 in 10 min → ban for 15 min.
- Repeat offenders: the duration doubles with each new ban within 30 days, at most 7 days.
- (Now, SEC-09: against attackers rotating IPv6 addresses, failed logins and second factors also count per prefix: 20 failures in 10 min from one /56 or 50 from one /48 jail that prefix. These aggregate jails are app-level only (never in the firewall), apply only to login and second factor, and exempt known devices; a /64 is still jailed, in the app and the firewall, after its own 5 failures.)
- Jailed IPs get `403` for all requests (middleware before routing) and are added to the nftables set through the agent (with timeout = ban duration). If the agent fails, the in-app ban stays in effect; the sync is caught up later.
- (Now, SEC-45: a password change also lifts this 2FA lockout.)
- Accounts are never locked because of failed password logins (this prevents anonymous attackers from locking out the admin). The only exception: after 10 wrong 2FA/sudo codes in 15 min, this user's second factor is locked for 15 min. Only someone who already knows the password can trigger this; without this limit, guessing 2FA codes over rotating IPs would be unlimited. Lifting it: change the password (ends the other sessions) or restart the panel.
- Allowlisted IPs are never jailed.
- An admin can lift jails (sudo) or ban IPs manually.

### 7.3 IP rules and GeoIP

- Allow list and deny list with CIDR (IPv4/IPv6), optionally with a comment and expiry date.
- Optional "admin access only from the allowlist": users with role `admin` can only sign in from allowed networks.
  (Now, SEC-16: it applies to every request of an admin, not only to the sign-in (logout is exempt), and saving it ends admin sessions from outside the allowed networks.)
- Optional country filter (allowed countries) through a local DB-IP Lite database (MMDB, no API key needed). Without the database the feature is off and the country columns are empty.
- Protection against locking yourself out: a change is rejected if it would exclude the admin's current IP. Emergency: `panel admin unlock` on the CLI.

### 7.4 Security Center (UI)

- Chart of login attempts (success/failure) over 24 h / 7 d / 30 d.
- Top IPs and top countries of failed attempts.
- Active jails (IP, reason, since, until, count) with lifting.
- Logins from new devices/IPs.
- Active sessions of all users (admin) with revocation.
- Audit log with filters (actor, action, time range) and chain status.
- Configuration: IP rules, country filter, notifiers.

### 7.5 Notifications

- Interface `Notifier.Send(ctx, Event)`; implementations: Discord webhook, ntfy (URL + topic + optional token).
- Events: login from a new device/new IP, jail triggered, role/permissions changed, user created/deleted, 2FA changed, audit chain invalid.
- Sent asynchronously with retry and deduplication (same event type + IP at most every 5 min); errors go to the log, not to the user.
- "Send test" button in the UI.
- (Now, SEC-20 and SEC-21: further events `auth.2fa_lockout`, `auth.2fa_failed`, `auth.recovery_code_used`, `auth.password_changed`; jail notifications at most 5 per 10 min, then one summary; operational events (backups, monitoring) have a queue of their own, so a flood of security events cannot delay them; a `Retry-After` of the target is honoured (at most 30 s); the test endpoint allows 6 calls per minute and user and no longer shows the target's HTTP status. SEC-17: every audit entry is also written to the journal as `{"msg":"audit","id":N,"hash":"…"}` (an anchor outside the DB); the panel verifies the chain every 24 h (without the journal, which the service cannot read) and notifies `audit.chain_broken`. The comparison with the journal runs as root in `install.sh doctor` (check `audit_journal`: ✗ for changed or missing entries, ! for entries that match only a later anchor or when `audit verify` cannot run); there is deliberately no timer for it (issue #123, F11).)

## 8. Frontend

- Pages: login, 2FA challenge, forced 2FA setup (TOTP QR code, passkey, show recovery codes + confirmation), dashboard (placeholder with server stubs), user management with a permission matrix (user × server × permission), Security Center, audit log, profile (password, 2FA methods, sessions).
- Global sudo dialog: reacts to `sudo_required` and repeats the action after confirmation. (Now, SEC-36: the dialog names the action it confirms, or the number and names of the pending actions.)
- The fetch wrapper sends the CSRF token automatically.
- Dark mode (system default + toggle), usable on mobile, languages de/en. (Now: sub-project 7 replaced the look with the dark "M2" and light "Clean" themes.)
- No inline script, no external CDNs (CSP-compliant).
- API types generated from `api/openapi.yaml`; during phase 1, mocks run through MSW against this contract.

## 9. Error handling

- Uniform JSON error format: `{"error": "<code>", "message": "<readable>"}`; internal errors reveal no details but a request ID that appears in the log.
- Agent errors are reported by the panel as `503 agent_unavailable`; the app's security functions (in-app jail) keep working.
- DB migrations run at startup; if one fails, the panel does not start.

## 10. Tests

- **TDD** for all Go packages.
- Unit: password hashing/policy, TOTP incl. replay, recovery codes, session expiry (with a fake clock), rate limiter, jail escalation, CIDR rules, hash chain, RBAC decisions, notifier dedup.
- Integration (`httptest` + a real SQLite file): full login incl. TOTP, CSRF rejection, `403` without permission, jail after 5 failed attempts, sudo requirement, session revocation, route-without-permission check.
- agent: tests with a fake firewall; nftables integration test only on Linux (build tag, CI).
- Frontend: Vitest + Testing Library for logic/components; Playwright E2E for login + TOTP setup against the real backend.
- CI (GitHub Actions): `go test ./...`, `golangci-lint`, `govulncheck`, `gosec`, `npm ci && npm test && npm run build`, `npm audit --audit-level=high`.

## 11. Implementation with parallel teammates

**Phase 0 – contracts (sequential, lead):** repo scaffold, `go.mod`, Makefile, all migrations, complete `api/openapi.yaml`, interfaces (`Store` sub-interfaces, `Notifier`, `Firewall`, `AgentClient`, `Clock`), `internal/httpx`, Vite scaffold. After that the streams share no files.

**Phase 1 – parallel, each in its own git worktree, TDD:**

| Stream | Content | Paths |
|---|---|---|
| A Auth | Password, TOTP, WebAuthn, recovery, sessions, CSRF, sudo | `internal/auth/` |
| B Defense | Rate limiter, jail, IP rules, GeoIP, notifier | `internal/defense/`, `internal/notify/` |
| C Agent | Socket server, peer creds, firewall nftables/no-op, client | `cmd/agent/`, `internal/agent/` |
| D Permissions & audit | Users CRUD, RBAC middleware, audit hash chain, CLI commands | `internal/rbac/`, `internal/audit/`, `internal/users/` |
| E Frontend | All pages against OpenAPI with MSW mocks | `web/` |
| F CI/release | GitHub Actions, lint configuration, GoReleaser | `.github/`, `.golangci.yml`, `.goreleaser.yaml` |

**Phase 2 – integration (lead):** wire up `cmd/panel`, frontend against the real API, integration and E2E tests, security review, code review.

## 12. Acceptance criteria

1. `make test` green; `golangci-lint`, `govulncheck`, `gosec` without findings.
2. `make dev` on macOS → UI reachable at `http://localhost:5173`.
3. `panel admin create` → forced 2FA setup → login with password + TOTP and with a passkey works.
4. A restricted user with `server.power` for stub server A sees only server A; access to server B → `403`.
5. 5 wrong logins → jail visible in the Security Center, the ntfy/Discord test notification arrives; in CI (`scripts/ci-acceptance.sh`) `nft list set inet mcpanel mcpanel_jail4` (or `mcpanel_jail6`) shows the IP.
6. All actions appear in the audit log, `panel audit verify` reports OK; after manually tampering with a row, `verify` fails.
7. `make release` produces Linux binaries (amd64/arm64) with the embedded frontend; agent + panel start on Linux (CI acceptance test).
