# Development

Development happens locally on macOS (Apple Silicon); releases target `linux/amd64` and `linux/arm64`.
Architecture and requirements: `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`. The operator guide (installation, update, uninstall) is `docs/install.md`.

## Toolchain

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck
```

| Tool | Version | Note |
|---|---|---|
| Go | from `go.mod` | CI reads the same file (`go-version-file`) |
| Node.js | 24 (Active LTS) | CI pins the major version `24`; Node 26 becomes LTS on 2026-10-28. When moving to the next LTS line, raise all `node-version` entries in `.github/workflows/*.yml` together (and `NODE_MAJOR` in `scripts/testvm-deploy.sh`) |
| golangci-lint | v2.14.0 | CI version; `scripts/vm-test.sh` installs exactly this one on the test VM (release archive with checksum) |
| restic | 0.19.1 (CI, test VM), at least 0.17 | `scripts/install-restic.sh` installs exactly this version with a checksum (CI, `make test-vm`, `make deploy-testvm`); optional on the Mac for backups in dev mode (`brew install restic`, installed by the user only) |
| GoReleaser | v2.18.2 (CI), locally ≥ v2.8 | `make release` uses the locally installed `goreleaser` |
| sqlc | version in the header of `internal/store/db.go` | CI downloads exactly this version as a release binary; if you generate with a newer version, commit the changed header too |
| govulncheck | v1.8.0 | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| Redocly CLI | pinned in `tools/redocly/package-lock.json` | `(cd tools/redocly && npm ci --ignore-scripts) && tools/redocly/node_modules/.bin/redocly lint api/openapi.yaml --extends=minimal` |
| actionlint | v1.7.12 | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` (uses `shellcheck` if installed) |

## Quickstart

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck   # once (see "Toolchain")
(cd web && npm ci)                                                        # once
make dev        # agent + files + panel (-dev) + Vite; UI at http://localhost:5173
printf '%s\n' '<password>' | .dev/bin/panel admin create -dev -username admin   # second terminal
cd web && npm run dev:mock   # alternative: frontend only, against the mock API
```

`make dev` builds `.dev/bin/panel`, `.dev/bin/agent` and `.dev/bin/files` and starts the agent, the files service (`files -dev`), `panel serve -dev` and Vite. If one of the processes ends, `scripts/dev.sh` stops the others and exits with a non-zero code (the output names the process). Ctrl+C stops everything.

Before every push: `make check` on the Mac (format, vet for macOS and Linux, Linux build, TypeScript, installer syntax; ~10 s) and `make test-vm` on the test VM (see "Tests on the test VM"). The Mac never runs the tests itself.

## First start

A fresh development environment has no user. The CLI creates the first admin (in a second terminal while `make dev` runs):

```bash
printf '%s\n' '<password>' | .dev/bin/panel admin create -dev -username admin
```

Without a pipe, `admin create` asks for the password twice without echo. Then sign in at <http://localhost:5173>. Admins must set up a second factor (authenticator app or passkey) at their first sign-in and confirm the recovery codes. All data lives under `.dev/` (`make clean` resets it).

Other CLI commands (they act directly on the database; the panel may be running or stopped):

| Command | Effect |
|---|---|
| `.dev/bin/panel admin unlock -dev` | Emergency exit after a lockout: lifts all active jails, turns off "Admin access only from the allowlist" and clears the country filter (audited) |
| `.dev/bin/panel admin list -dev` | Lists the admins; opens the database read-only (no migration, no new files) and prints nothing if there is no database yet |
| `.dev/bin/panel audit verify -dev` | Checks the hash chain of the audit log and reports the first broken entry (exit code 1) |

Without `-dev`, all commands read `/etc/mc-panel/panel.toml` (or `-config <path>`).

## Frontend without backend (mock)

```bash
cd web && npm run dev:mock    # Vite with the MSW mock API, no Go process needed
```

The mock covers every operation in `api/openapi.yaml` (`src/mocks/handlers.test.ts` checks this). `npm run build` refuses to build with the mock active.

## UI preview in Docker

For clicking through the UI without a local Node installation: the same mock mode, but in a container (Docker Desktop). No backend and no database: the data comes from the MSW mock API and resets on every reload.

```bash
make preview         # builds the image and starts the container in the background
make preview-logs    # logs of the Vite server
make preview-down    # stops and removes the container
```

- Address: http://localhost:5180 (reachable only from this Mac; port 5180 keeps 5173 free for `make dev`).
- Sign-in: `admin` / `correct-horse-battery`, TOTP code `123456`; restricted user: `alice` (same password).
- `web/src` is mounted into the container: frontend changes show up immediately. After changes to `package.json`, run `make preview` again (it rebuilds).
- This is not a functional test against the real backend; use `make dev` or a test server for that.
- CI builds the image in the `web` job when its inputs change (see "CI"), but never starts it. Claude never starts the preview itself.

## Minecraft servers locally (optional)

`make dev` starts the agent without Docker: servers can be created and configured, the status page shows Docker as "Unreachable", and no containers appear on the Mac. To see real Minecraft containers locally, start deliberately with:

```bash
MCPANEL_DEV_DOCKER=1 make dev
```

- The dev agent then uses the Docker socket of OrbStack, Docker Desktop or `/var/run/docker.sock` (in that order), creates the network `mcpanel-mc` and the container `mcpanel-router` and writes instance data to `.dev/mc/`. The router publishes port 25565 on all network interfaces of the Mac, so it is reachable from the local network.
- Base domain in the settings: `mc.localhost`; for the Minecraft client, add `127.0.0.1 <name>.mc.localhost` to `/etc/hosts`.
- Cleanup: `docker rm -f $(docker ps -aq --filter label=mcpanel.instance) mcpanel-router; docker network rm mcpanel-mc`.
- Console commands that start with `-` (after optional spaces and one `/`) are rejected by panel and agent (`400 validation_failed`): `rcon-cli` would read them as an option. The agent calls `rcon-cli -- <command>`.
- The console and the Players tab need a running container. Without Docker, commands and player lists answer `503 docker_unavailable` and the "Online players" card shows "Not available"; the log stream still answers 200, and the console shows "Console unavailable" or "Reconnecting …". The UI preview (`make preview`) and `npm run dev:mock` instead deliver simulated log lines (a new one every 2 s, with ANSI and `§` colours and RCON noise), command responses and player lists.
- In dev mode the files service runs as a process of the developer and works on the same instance directory as the dev agent (`.dev/mc/instances/<id>`); if a server's directory is missing, it creates it. The file browser therefore works without Docker too. The privilege separation (own user `mcpanel-mc`, no capabilities) exists only on Linux (test VM, CI).
- Plugins and mods (tab "Plugins" or "Mods" for Paper, Purpur, Fabric and NeoForge) need nothing extra: in dev mode, too, the panel talks to the real upstreams (Modrinth, Hangar) and loads jars through the files service into `.dev/mc/instances/<id>/plugins` or `mods`. Without Docker, search, install, inventory, toggles and removal work; "Restart required" only appears with a running container. Without internet, "Discover" shows an error state and "Installed" stays usable. To spare the upstreams, use `npm run dev:mock` (catalog, jobs and icons from the mock).
- Backups (tab "Backups") need a restic in the `PATH` of the files service (dev: `restic_binary = "restic"`). Without restic the tab only shows that backups are not available on this host; everything else works normally. On the Mac the user installs restic if needed (`brew install restic`). Repos and keys then live under `.dev/mc/backups/` (`make clean` deletes them too). Backup and restore need the container state from the agent: without reachable Docker they fail with "Docker is not reachable" (job code `agent_unavailable`, "The agent is unreachable."), because a hanging dockerd would otherwise let a running server be backed up without `save-off`. In `make dev`, backups therefore work only with `MCPANEL_DEV_DOCKER=1` (and restic in the `PATH`); listing, schedule, pinning, download and deleting snapshots work without Docker too. `npm run dev:mock` simulates snapshots, jobs and schedules without restic.

Functional tests against real servers (clients, modpacks, restarts, console) run separately on the test VM (next section). The release acceptance checklist, run on the test VM before a release, is `docs/dev/acceptance.md`.

## Test VM

For manual tests against the real backend with Docker, nftables and Caddy there is an internal VM: Ubuntu 26.04, `claude@192.168.1.117`, passwordless `sudo`, outbound internet access, not reachable from outside. Unlike on the Mac, Claude may install and change anything there.

```bash
make deploy-testvm                         # main from GitHub
make deploy-testvm REF=feat/my-branch      # any branch, tag or commit (must be pushed)
make deploy-testvm TESTVM=user@other-vm    # another VM
```

- `scripts/testvm-deploy.sh` runs on the VM via `ssh … bash -s` (it is the local copy of the script, so pull `main` first if it changed). On the first run it installs the build tools (`ca-certificates`, `curl`, `git`, `jq`, `make`, `xz-utils`, `bzip2`), Go (version from `go.mod`, to `/usr/local/go`) and Node.js 24 (to `/usr/local/lib/nodejs`). Then it fetches the repo with the VM's deploy key into `~/mc-panel`, builds UI, panel, agent and files (version `0.0.0-testvm+<commit>`), stages them with `deploy/` and the assembled `install.sh` in `~/mc-panel/build/testvm/` (the `--from-dir` format), installs the pinned restic (`scripts/install-restic.sh`, idempotent, `/usr/local/bin/restic`; the installer takes it as `restic_binary` because `/usr/local/bin` comes first in `PATH`) and calls the installer: `install --from-dir … --tls internal --domain panel.<ip>.sslip.io --no-firewall --yes` until one `install` has succeeded (root-owned marker `/etc/mc-panel/.testvm-installed`, set only afterwards) and while `/etc/caddy/mc-panel.caddy` is missing; otherwise `update --from-dir … --force --yes` (the same build a second time needs `--force`). A ref without an installer (before sub-project 6) aborts with a clear message. Packages (Docker, Caddy, nftables, restic from Ubuntu, sqlite3, tzdata …), users, permissions, configuration, units and the Caddy site are written by the installer (section "Installer"). The script only adds the VM's Caddyfile frame (`skip_install_trust`, the marked line `import mc-panel.caddy # managed by mc-panel`, a redirect from the IP to the panel name) and copies the Caddy CA to `~/caddy-root.crt`. The frame is written before the first `install` (the old Caddyfile contains the panel site inline; the import next to it would define the site twice). The previous file stays as `/etc/caddy/Caddyfile.testvm-prev` and is put back if the installer fails and the new file does not validate (for example the import of a `mc-panel.caddy` that was never written), or if Caddy rejects a changed frame. Data is kept (`/var/lib/mc-panel`, `/srv/mc-panel`). At the end, `install.sh doctor` runs: warnings (exit code 1) about the file system, the host firewall (`--no-firewall`), a newer release and possibly SSH are normal on the VM; a failure (exit code 2) fails the deploy.
- The first run after sub-project 6 migrates the old heredoc installation once: if there is no marker yet, but an `/etc/mc-panel/panel.toml` with `listen = "127.0.0.1:8080"` and no `panel.toml.pre-installer`, the three config files are moved to `*.pre-installer` (an existing copy is never overwritten; otherwise the installer would only add missing keys and keep `listen = "127.0.0.1:8080"`) and the services are stopped; the Minecraft containers keep running. The installer writes the files anew, and the panel then listens on the socket `/run/mc-panel-web/panel.sock` instead of `127.0.0.1:8080`. The existing admin stays (the installer does not ask; the VM has no terminal anyway). Because containers are running, the installer writes `/etc/docker/daemon.json` but does not restart Docker (`[warn]`): log rotation and `live-restore` apply at the next Docker restart.
- An update through the installer replaces binaries, units and missing config keys (with a backup under `/var/lib/mc-panel-rollback/` and rollback); it does not touch the Caddy site or the firewall. If a branch changes `deploy/caddy/`, run `install` by hand: `ssh claude@192.168.1.117 'sudo ~/mc-panel/build/testvm/install.sh install --from-dir ~/mc-panel/build/testvm --tls internal --domain panel.192-168-1-117.sslip.io --no-firewall --yes'`.
- Addresses via [sslip.io](https://sslip.io) (public wildcard DNS that resolves to the IP in the name): panel <https://panel.192-168-1-117.sslip.io/>, base domain for Minecraft `mc.192-168-1-117.sslip.io`, so `<server>.mc.192-168-1-117.sslip.io` in the client. `https://192.168.1.117` redirects to the panel name.
- TLS: Let's Encrypt cannot reach the VM, so Caddy signs with its internal CA (`tls internal`). Until the CA is trusted, the browser warns. The warning can be clicked away (TOTP sign-in works), but passkeys cannot (WebAuthn refuses pages with certificate errors). To trust the CA: `scp claude@192.168.1.117:caddy-root.crt /tmp/ && sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain /tmp/caddy-root.crt` (system-wide, so the user runs it). The root certificate stays the same across deploys.
- First admin (only needed on a fresh VM; without a terminal the installer skips this step with `[warn]`): `ssh claude@192.168.1.117 "printf '%s\n' '<password>' | sudo runuser -u mcpanel -- panel admin create -config /etc/mc-panel/panel.toml -username admin"`.
- Logs: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`. Installer log: `/var/log/mc-panel-install.log`. Configuration: `/etc/mc-panel/{panel,agent}.toml` and `/etc/mc-panel-files/files.toml`; the installer only adds missing keys there, so manual changes survive deploys (templates in `deploy/config/`).
- As in production, the panel listens on the socket `/run/mc-panel-web/panel.sock` (`mcpanel-panel.socket`); Caddy talks to it.
- Plugins and mods need no extra configuration on the VM: the installer's panel unit does not restrict outbound connections (as for the version catalogs), and the files service deliberately has no network (`RestrictAddressFamilies=AF_UNIX`). Checklist: `docs/dev/acceptance.md` ("Plugins and mods").
- Backups: the installer creates `/srv/mc-panel/backups` (`mcpanel-mc 0700`), sets `restic_binary` to the restic from `command -v restic` (version ≥ 0.17; on the VM the pinned `/usr/local/bin/restic` from `scripts/install-restic.sh`, on a VPS the Ubuntu package `/usr/bin/restic`, 26.04 ships 0.18) and writes the files service unit from `deploy/systemd/` (`ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups`, `MemoryMax=1G`, `TasksMax=512`, `IOWeight=50`, `CPUWeight=50`); `install.sh doctor` checks restic and the permissions. Checklist: `docs/dev/acceptance.md` ("Backups").

## Tests on the test VM

The Mac stays clean (only `make check` there); tests run on the test VM before every push:

```bash
make test-vm                 # all suites: lint, go, web, e2e
make test-vm SUITE=go        # go test -race ./... (CGO_ENABLED=1)
make test-vm SUITE=lint      # golangci-lint v2.14.0 (on Linux, so the _linux files too), eslint, tsc, installer shellcheck + Bash tests
make test-vm SUITE=web       # Vitest
make test-vm SUITE=e2e       # Playwright: real backend (dev mode) and mock
make test-vm SUITE=shots     # screenshots of all web/e2e/shots/*.shots.ts against the mock (not part of all)
```

On its first run, the `go` suite installs restic via `scripts/install-restic.sh` and sets `MCPANEL_TEST_RESTIC=/usr/local/bin/restic`; without this variable (always on the Mac) the files service's integration tests with a real restic skip themselves.

- `scripts/test-vm.sh` copies the working tree with `rsync --delete` to `~/mc-panel-test/<name of the checkout>/` on the VM: tracked and new files, without `.git` and without what `.gitignore` excludes (the excludes are listed in the script). No push is needed. Each checkout or worktree gets its own directory. The timing-sensitive parts of all checkouts (race tests, Vitest, Playwright, shots) are serialised with `flock` (parallel runs of several teammates overload the VM and make those tests fail); lint, `npm ci` and builds (including compiling the race test binaries) run outside the lock. `all` runs lint first and then holds the lock from the race tests to the end of e2e. A waiting run reports "waiting for another test run on this VM". Vitest runs with a 15 s test and hook timeout because the VM is shared. `node_modules` and build output survive between runs.
- Then `scripts/vm-test.sh <suite>` runs there as the ordinary user `claude`. It sets up what it needs on first use: golangci-lint (release archive with checksum to `~/.local/bin`), shellcheck (apt), `npm ci` (again only when `package-lock.json` changes) and Playwright Chromium with its system packages (`sudo`, once per Playwright version). Go and Node come from `make deploy-testvm`; if the Go version does not match `go.mod`, the script aborts with a hint.
- `lint` also assembles the installer (`make installer`), runs shellcheck over `scripts/install/*.sh`, `scripts/install/tests/*.sh` and `build/install.sh`, and runs `scripts/install/test.sh` (like the CI job `installer-lint`).
- `e2e` builds UI and binaries, starts agent, `files -dev` and `panel serve -dev` with its own configuration (`.dev/panel-vm.toml`: port 18080, the panel serves the built UI itself), creates `e2e-admin` and runs `npm run e2e:real` and `npm run e2e` (mock, port 5174). The deployed panel (systemd, socket `/run/mc-panel-web/panel.sock`) is not touched.
- `shots` runs the Playwright project `screenshots` (`web/e2e/shots/*.shots.ts`, helpers `shoot()`/`signInMock()` in `web/e2e/shots/shoot.ts`) against the mock: every page in dark and light, desktop (1440 px) and mobile (390 px). `make test-vm` copies the PNGs back to `web/screenshots/` (gitignored). For visual review only; not part of `all` and not in CI.
- Single tests after a `make test-vm`: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/<dir> && PATH=/usr/local/go/bin:$PATH go test ./internal/rbac -run TestName'`, or in the `web` subdirectory with `PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run <file>`.
- SSH to the VM needs a Bash without sandbox in Claude Code.
- What runs only in CI: the root acceptance jobs (`nftables`, `instances`, `installer`; they change the host), `govulncheck`, `npm audit`, the sqlc and `gen:api` freshness checks, the OpenAPI lint and the GoReleaser snapshot.

## End-to-end tests (Playwright)

Install the browser once: `cd web && npx playwright install chromium`.

| Command | Effect |
|---|---|
| `npm run e2e` | Project `mock`: starts `vite --mode mock` on port 5174 itself and tests `e2e/smoke.spec.ts`, `e2e/totp.spec.ts`, `e2e/servers.spec.ts`, `e2e/plugins.spec.ts`, `e2e/backups.spec.ts` and `e2e/monitoring.spec.ts` against the mock API |
| `npm run e2e:real` | Project `real`: `e2e/login-totp.spec.ts` against a running backend; needs `E2E_BASE_URL`, `E2E_ADMIN_USER` and `E2E_ADMIN_PASSWORD` of a **fresh** admin without 2FA (no retries) |
| `npm run shots` | Project `screenshots`: `e2e/shots/*.shots.ts` against the mock (see `make test-vm SUITE=shots`) |

The real test sets up TOTP, signs in again with TOTP, registers a passkey in the profile through a virtual authenticator (Chrome DevTools Protocol, Chromium only; sudo via TOTP) and finally signs in with the passkey. It may wait for the next 30 s TOTP step. Example:

```bash
make dev   # terminal 1
printf '%s\n' 'e2e admin password 1' | .dev/bin/panel admin create -dev -username e2e-admin
cd web && E2E_BASE_URL=http://localhost:5173 E2E_ADMIN_USER=e2e-admin E2E_ADMIN_PASSWORD='e2e admin password 1' npm run e2e:real
```

A second run needs a new admin (different username) or a fresh `.dev/` (`make clean`).

In CI, the job `e2e` (`.github/workflows/ci.yml`) runs `scripts/vm-test.sh e2e`, the same suite as `make test-vm SUITE=e2e`: it builds UI and binaries, starts agent, `files -dev` and `panel serve -dev` (port 18080, the panel serves the built UI), creates `e2e-admin`, runs `npm run e2e:real` and then `npm run e2e`, and on failure prints the logs and uploads `test-results`/`playwright-report` as an artifact.

## Production

In production the installer (`install.sh`, section "Installer"; operator guide in `docs/install.md`) sets everything up. The following points describe what it implements and what applies if you deviate from it. The templates in `deploy/` are authoritative for units, Caddy, firewall and configuration.

- The agent needs Docker (Engine ≥ 25, API ≥ 1.44) and the system user `mcpanel-mc` (own UID/GID, not the panel's); the installer sets up both. `instances_dir` (default `/srv/mc-panel`) must not be a symlink and must lie only below root-owned directories that are not group- or world-writable.
- systemd sandboxing (`deploy/systemd/*.service`; each directive is checked against what the service does, see the comments there): the panel has no capabilities, `ProtectSystem=strict`, `RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK` (netlink: `net.InterfaceAddrs` of the notifier SSRF filter), `IPAddressDeny=169.254.0.0/16 fe80::/10`, `SystemCallFilter=@system-service ~@privileged @resources` and `MemoryMax=1G`/`TasksMax=512`, but no `ProcSubset=pid` (host values from `/proc/meminfo` and `/proc/stat`). The agent keeps `CAP_CHOWN CAP_DAC_OVERRIDE CAP_DAC_READ_SEARCH CAP_FOWNER CAP_NET_ADMIN`, writes only `instances_dir` and `/run/mc-panel`, and has only `AF_UNIX AF_NETLINK` with `IPAddressDeny=any`. All three use `SystemCallErrorNumber=EPERM` (Go's `setrlimit` at start is refused, not killed), `MemoryDenyWriteExecute`, `RestrictNamespaces`, `ProtectProc=invisible` and the `Protect*` kernel options. Check a change with `systemd-analyze security mcpanel-<unit>.service` on the test VM (panel 1.2, agent 2.0, files 0.6) and the end-to-end path (server start, console, plugin install, backup, restore, download).
- The files service `files` (spec 3b §3.1) runs as `mcpanel-mc` without supplementary groups and without capabilities, with `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateNetwork=yes`, `ProcSubset=pid` (also for restic), `ReadWritePaths=<instances_dir>/instances <instances_dir>/backups`, `MemoryMax=1G`, `TasksMax=512` (a limit against crafted archives, with room for restic), `IOWeight=50` and `CPUWeight=50`. It accepts connections only from the panel UID and from root (`panel_uid`; the peer UID check is the actual access control, as for the agent). systemd creates the socket (socket activation, `mcpanel-files.socket`: `ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660`, `DirectoryMode=0755`, `RemoveOnStop=yes`), and the service takes it over via `LISTEN_FDS`. Without activation (dev, CI) it creates `socket` itself with `socket_mode` (default `0660`; group = its primary group). With `UMask=0027`, files are created with 0640 and directories with 0750 in production (the service requests 0644/0755). Requirements the installer's units implement: own socket directory `/run/mc-panel-files` (never the agent's `/run/mc-panel`), no `SupplementaryGroups`, configuration in `/etc/mc-panel-files/files.toml` (`root:mcpanel-mc`, directory 0750, file 0640; `/etc/mc-panel` is unreadable for `mcpanel-mc`), `instances/` as `root:mcpanel-mc 0750` (set by the agent at start), `deleted/` stays `root:root 0700`, `backups/` is `mcpanel-mc:mcpanel-mc 0700` (set by the agent at start; it must exist before the service starts, otherwise `ReadWritePaths` fails). The panel finds the service through `files_socket` in `panel.toml`; `deploy/systemd/` is the reference.
- File uploads and downloads (up to 4 GiB, `max_upload_bytes` in `files.toml`) run as one stream through Caddy and the panel; Caddy does not limit the body size by default. Another proxy needs a body limit ≥ 4 GiB and must not buffer uploads.
- **Outbound connections of the panel** (HTTPS only, port 443, User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`): version catalogs `piston-meta.mojang.com`, `fill.papermc.io`, `api.purpurmc.org`, `meta.fabricmc.net`, `maven.neoforged.net`; plugins and mods `api.modrinth.com`, `hangar.papermc.io` (API) and `cdn.modrinth.com`, `hangarcdn.papermc.io` (jars and icons; the panel rejects other hosts, also as a redirect target). An egress firewall must allow these hosts for the user `mcpanel`. The files service and the agent need no network for plugins; the Minecraft containers themselves still download what itzg needs at start (server jars, modpacks).
- **Plugins and mods** (spec 3c): the panel downloads jars as a stream (≤ 256 MiB) and passes them to the files service, which checks the upstream checksum (Modrinth sha512, Hangar sha256) before the `rename` (`PUT /v1/upload?…&sha512=…`); on a mismatch nothing is left behind. The `plugins/` or `mods/` directory is the source of truth; the table `plugin_installs` only remembers the origin of panel-installed jars. Jobs (install, update) run in memory, one per server and four per panel; a panel restart aborts running jobs, jars already installed stay. Icons go through the proxy `/api/servers/{id}/plugins/icon` (the CSP stays `img-src 'self'`, no foreign image host). Modrinth throttles to 300 requests/min per IP; the panel limits upstream queries (search, versions, resolve, update check with `refresh`) to 60/min per account (burst 20) and caches search (5 min), projects and versions (15 min), hash matches and updates (1 h).
- Backups: see the section "Backups" below.
- The installer sets `listen = "unix:/run/mc-panel-web/panel.sock"`. systemd creates the socket (socket activation, `mcpanel-panel.socket`: `ListenStream=/run/mc-panel-web/panel.sock`, `SocketUser=mcpanel`, `SocketGroup=caddy`, `SocketMode=0660`, `DirectoryMode=0755`, `RemoveOnStop=yes`), and the panel takes it over via `LISTEN_FDS` (`internal/unixsock`, like the files service); the path in `listen` must match the activated socket, otherwise the panel does not start. The directory `/run/mc-panel-web` belongs to the panel socket only, never to the agent's `/run/mc-panel` (systemd removes a unit's socket and directory on stop, and `mcpanel` must not be able to replace the agent socket). Without activation (dev, own units) the panel creates the socket itself (mode 0660, group = primary group of the panel user; the default without `-dev` is `unix:/run/mc-panel/panel.sock`); a stale socket is removed at start only if nobody listens on it any more.
- Caddy terminates TLS and talks to the socket (`/etc/caddy/mc-panel.caddy` from `deploy/caddy/`, included through the line `import mc-panel.caddy # managed by mc-panel` in the `Caddyfile`; other sites there stay untouched):

  ```
  panel.example.com {
      tls you@example.com
      log {
          output file /var/log/caddy/mc-panel.log {
              roll_size 50MiB
              roll_keep 14
          }
          format filter {
              wrap json
              fields {
                  request>headers>X-Csrf-Token delete
              }
          }
      }
      import /etc/caddy/mc-panel.d/*.caddy
      reverse_proxy unix//run/mc-panel-web/panel.sock
  }
  ```

  Caddy gets access through the socket group `caddy`, **not** by joining the group `mcpanel` (otherwise Caddy could delete or replace the agent socket). Caddy comes from its official repository (pinned signing key, `packages.sh`); a Caddyfile the installer owns puts the admin API on `unix//run/caddy/admin.sock` (the drop-in `caddy.service.d/mc-panel.conf` creates `/run/caddy` and `/var/log/caddy`). Changing the admin address needs `systemctl restart caddy`, because `caddy reload` sends the new configuration to the address that configuration names.
- Requests over the Unix socket count as coming from the proxy: the client IP is the rightmost untrusted entry in `X-Forwarded-For` (Caddy sets it). Without a usable entry the request is rejected (`403 ip_blocked`), so health checks directly on the socket need a header, for example `curl --unix-socket /run/mc-panel-web/panel.sock -H 'X-Forwarded-For: 127.0.0.1' http://panel/api/health` (this is how `install` and `update` check it). Active Caddy health checks (`health_uri`) send no `X-Forwarded-For` and would fail with `403`: either set `health_headers` or do without active checks. Other proxies must set `X-Forwarded-For` without a port, otherwise `403`.
- TCP remains possible (`listen = "127.0.0.1:8080"`); then `X-Forwarded-For` is only taken from `trusted_proxies`. Loopback is exempt from "Admin access only from the allowlist" only in dev mode.
- The console streams the server log as Server-Sent Events (`GET /api/servers/{id}/console/stream`, `text/event-stream`, a heartbeat comment every 15 s, one stream per open console tab, at most 4 per user; the 5th gets `429 rate_limited`). Caddy passes SSE through without buffering and without extra configuration. Another proxy must not buffer the response (the panel sets `X-Accel-Buffering: no` for nginx) and needs a read timeout above 15 s.
- Outside dev mode the panel sends `Strict-Transport-Security: max-age=31536000`.
- Always run CLI commands on the server as the panel user (`sudo runuser -u mcpanel -- panel admin unlock -config /etc/mc-panel/panel.toml …`); otherwise root-owned `panel.db-wal`/`-shm` files appear that the service cannot open.
- A 2FA lockout (10 wrong codes in 15 min) is not lifted by `admin unlock`; it ends after 15 min or with a panel restart.
- The nftables sets hold 65,536 entries each; if a set is full, the firewall ban fails (logged), but the in-app jail still applies.

## Backups

Spec: `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (spec 4); manual acceptance `docs/dev/acceptance.md` ("Backups").

- The files service starts restic (at least 0.17, reference 0.19.1 via `scripts/install-restic.sh`; path `restic_binary` in `files.toml`, default `/usr/bin/restic`) as a child process without network. restic never opens paths in a data directory: backups are a tar stream that the service produces through `os.Root` (without symlinks, hidden files and the RCON password); restore and download read `restic dump`.
- One repo per server under `<instances_dir>/backups/repos/<id>`, its password in `backups/keys/<id>`. **`backups/keys/` belongs in every backup of the host**: without the key a repo is unreadable.
- There are no offsite targets yet. Repos are self-contained and can be copied with their keys to another machine with `rsync -a <instances_dir>/backups/ <target>/`.
- `backups/` lives on the same file system as `instances/`; a restore needs free space there equal to the snapshot plus `reserve_bytes`. A snapshot with more than 16 MiB of path names (the zip limit from sub-project 3b; backup and restore allow 64 MiB) can be restored but not downloaded as a zip.
- Schedules run in the time zone from the settings (default `Europe/Berlin`); the panel catches up missed runs (panel off) once. **The package `tzdata` must be installed** (the installer installs it, `doctor` checks it): restic embeds no zone data and silently falls back to UTC without `/usr/share/zoneinfo/<zone>`. Snapshot times and the daily, weekly and monthly retention groups are then wrong, without any error.

**Emergency restore without the panel** (stop the server first):

```bash
sudo -u mcpanel-mc env RESTIC_REPOSITORY=/srv/mc-panel/backups/repos/<id> RESTIC_PASSWORD_FILE=/srv/mc-panel/backups/keys/<id> restic --no-cache snapshots
sudo -u mcpanel-mc env RESTIC_REPOSITORY=/srv/mc-panel/backups/repos/<id> RESTIC_PASSWORD_FILE=/srv/mc-panel/backups/keys/<id> restic --no-cache dump <snapshot> /data.tar > /tmp/world.tar
```

Then extract the tar into an empty directory and check it. The last entry, `.mcpanel-end` with the service's counters, is not part of the server data and can be removed.

**Restore journal.** A restore swaps the top level of the data directory with `rename` and keeps a journal in `<instances_dir>/backups/restore-journal/<id>` (only during the renames). If the files service dies in the middle, it finishes the swap synchronously at its next start: back in phase `old_moving`, forward in phase `new_moving`; afterwards the temp directory and the journal entry are gone.

**Phase `stuck`** (both the swap and the swap back failed): new restores of this server are rejected. A restore attempt still stops the server and creates a "Before restore" snapshot before it fails with "The file service is busy with another job for this server (for example an extract). Try again later." (`files_busy`); the actual cause is only in the files service's log. Runbook:

1. Read the files service's log (`journalctl -u mcpanel-files`, the message with `folder` and `journal`).
2. Keep the server stopped and inspect the hidden directory `.mcpanel-tmp-…` in the data directory (`sudo ls -la /srv/mc-panel/instances/<id>/.mcpanel-tmp-*/`): `old/` holds the previous state, `new/` the rest of the snapshot.
3. Decide, move the contents up by hand (as `mcpanel-mc`, for example `sudo -u mcpanel-mc mv …`), then delete the temp directory.
4. `sudo rm /srv/mc-panel/backups/restore-journal/<id>`.

"Delete server" with "Delete backups too" removes the journal entry as well.

## Installer

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md`; manual acceptance `docs/dev/acceptance.md` ("Installer"); operator guide `docs/install.md`.

- Sources in `scripts/install/`: `lib.sh` (helpers, paths, output `[ok]`/`[changed]`/`[skip]`/`[warn]`, `sys_*` wrappers for system commands), one file per step (`preflight.sh`, `packages.sh`, `layout.sh`, `binaries.sh`, `config.sh`, `units.sh`, `caddy.sh`, `firewall.sh`, `upgrades.sh`, `admin.sh`, `update.sh`, `doctor.sh`, `uninstall.sh`) and `main.sh` (arguments, subcommands, logging). Templates in `deploy/` (`systemd/`, `caddy/`, `nftables/`, `config/`). Functions carry a prefix per file (`pre_`, `pkg_`, `lay_`, `bin_`, `cfg_`, `unit_`, `cad_`, `fw_`, `upg_`, `adm_`, `upd_`, `doc_`, `uni_`).
- `make installer` assembles everything into `build/install.sh` (`scripts/install/assemble.sh <version> <out>`; templates as heredoc functions, the version as `MCPANEL_VERSION`; the last line calls `main`, so an interrupted download runs nothing). goreleaser attaches `install.sh` to every release and lists it in `checksums.txt`; the archives contain `deploy/`. With `--from-dir DIR` (binaries, `deploy/` and `install.sh` of a local build) the installer downloads nothing; templates then come from `DIR/deploy/`.
- The installer never runs on the Mac; `make check` only checks the syntax (`bash -n` over the sources and the assembled `build/install.sh`).
- Tests: `scripts/install/test.sh [FILE|PATTERN]` (pure functions, templates, checksums and the steps against a root directory `MCPANEL_INSTALL_ROOT=<tmp>` with `MCPANEL_INSTALL_DRYRUN=1`, which only records system commands; test files in `scripts/install/tests/`) runs with shellcheck in the CI job `installer-lint` and in `make test-vm SUITE=lint`. The CI job `installer` checks the whole lifecycle (`scripts/ci-installer.sh`, runners only, changes the system; steps in the table under "CI"). Every `make deploy-testvm` goes through the installer too. `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1` allows Ubuntu 24.04 in CI; this is not a supported path for operators.
- Exit codes: 0 ok, 1 error (or `doctor` with warnings), 2 `doctor` with failures, 3 update rolled back, 4 rollback failed (all services stopped, instructions in the output). Log: `/var/log/mc-panel-install.log` (0600). `doctor --json` prints `{"status": "ok|warn|fail", "checks": [{"id", "status", "message", "hint"}]}`; the check IDs are listed at the top of `doctor.sh`.
- Update: backup of binaries, units, the three TOML files and the DB to `/var/lib/mc-panel-rollback/<old-version>-<time>/` (`root:root 0700`, the three newest are kept) with a state file (`phase=backed_up|applying|verified|rolling_back|rolled_back|aborted|rollback_failed`). If an update stops hard (power loss, `kill -9`), the next `update` finishes it first: nothing swapped → `aborted`; target version installed and healthy → `verified`; otherwise rollback with the DB copy, only after confirmation (or `--force`/`--yes`), because changes since the backup are lost. `doctor` reports such directories (`update_state`).
- After a healthy swap `update` runs `pkg_caddy_ensure` (Caddy repository and package), `cad_ensure` (site, drop-in, admin socket) and `upg_ensure`, each in its own subshell; a failure there is only a `[warn]` (`upd_system`).
- Firewall: own table `inet mcpanel_fw` (`/etc/mc-panel/mcpanel_fw.nft`, loaded by `mcpanel-fw.service`), separate from the agent's table `inet mcpanel`. Before loading, the installer checks the SSH ports (`--ssh-port`, otherwise `sshd -T` against the sockets actually listening), the running SSH sessions and all other ports the rules would block (it asks; `--allow-port PORT[/udp]` opens them, `--yes` blocks them).

## Monitoring and alerts

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md`; manual acceptance `docs/dev/acceptance.md` ("Monitoring and alerts").

- **Collector in the panel** (`internal/monitoring`), no new service: every 30 s one pass over all servers with an instance. Sources: the agent status (state, `started_at`, `restart_count`, CPU, RAM, limit; one call for all), a Server List Ping per running server through mc-router at `monitoring_ping_addr` with the server's FQDN in the handshake (deadline 3 s, at most 4 at once, response strictly bounded: packets and JSON ≤ 64 KiB, ≤ 12 names; the ping never goes through the root agent), TPS/MSPT via RCON over the existing agent route (once a minute, at most one call per server at a time) and the host from `/proc/stat`, `/proc/meminfo`, `/proc/loadavg` and `statfs(instances_dir)` (free = blocks available to non-root). A pass longer than 30 s skips the next tick.
- **TPS source** per server and `revision`, found by trial: first `tick query` (Vanilla ≥ 1.20.3 and all derivatives), otherwise for Paper/Purpur only `mspt` (`tps = min(20, 1000 / mspt)`, so it stays at one query per minute), otherwise `none` ("TPS not available for this version"). The source becomes fixed after three valid or three invalid answers; until then `tps_source = unknown`. Every query produces two lines in the server log (`Thread RCON Client … started`/`shutting down`): without viewers, two lines per minute.
- **Live mode:** every call of `GET /api/servers/{id}/metrics/live` (the "Monitoring" area polls every 5 s) switches TPS/MSPT of that server to 5 s for 30 s; these values are not stored. Players, CPU and RAM stay at the 30 s rate.
- **`panel.toml`:** `monitoring_ping_addr` (default `"127.0.0.1:25565"`, the host port of mc-router; `""` turns the ping off: then no player counts, "Unreachable" only from the container state, "back online" after 2 min `running`) and `instances_dir` (default `"/srv/mc-panel"`, the same path as in `agent.toml`; `""` = no disk values). The panel only needs search permission on `instances_dir` (0755) and read access to `/proc` (`ProtectProc=invisible` does no harm, it only hides other users' PIDs). The template `deploy/config/panel.toml` names both keys explicitly.
- **Storage:** three tables in the panel DB, `metrics_raw` (30 s raw values, 24 h), `metrics_5m` (7 d) and `metrics_1h` (30 d, means weighted by `samples`); missing values are not written (a gap instead of 0). Aggregation runs every 5 min idempotently (`INSERT OR REPLACE`) and then deletes what is older. Queries: `1h` → raw values (`step_ms` 30,000), `24h` → 5 min, `7d`/`30d` → 1 h (at most 721 points per metric). Size: about 23,000 raw rows per server and day (8 metrics × 2,880 ticks); 10 servers over 30 days stay well below 50 MB. When a server is deleted, the panel removes its series and alert states in the same transaction.
- **Alerts** (Settings → "Monitoring & alerts", `PUT /api/monitoring/settings` with sudo, audit `monitoring.settings_update`; switch "Down alerts for this server" in the server's Monitoring area, `PUT /api/servers/{id}/alerts`, audit `server.alerts_update`):
  - `server.down` fires when a server should be running (`desired_state = running`), the switch is on, no data gate applies (extract, restore) and it has been unreachable continuously for `down_after_min` (default 3) minutes (container not `running` or ping without answer). After every start and restart (according to `started_at`) a 5 min grace period applies; while `server.crash_loop` fires, no additional `server.down` is sent. Resolved with `server.recovered` ("Server back online: …"). Known limit: the grace period counts from the container's `started_at`; while it is not running at all yet (image pull or re-creation after a change) there is no grace period, and if that takes longer than `down_after_min`, the panel reports the server as down (possible with a short `down_after_min` and a slow image download).
  - `server.crash_loop` fires when Docker's `restart_count` rises by at least `crash_restarts` (default 3) within `crash_window_min` (default 10); a newly created container (counter back at 0) does not count. Resolved after a full window without a restart with `server.recovered` ("Server stable again: …"). The switch covers both server alerts.
  - `host.disk_low` fires when the free space falls below `disk_min_bytes` (default 10 GiB) or below `disk_min_percent` (default 10 %); resolved with `host.disk_ok` only with a margin of 2 GiB **and** 2 percentage points (hysteresis).
  - Delivery through the notifications of the Security Center (Discord/ntfy, notify dispatcher): one message when firing and when resolving, a reminder every 24 h ("Server still down: …", "Server still in a crash loop: …", "Disk space still low"). The state lives in `alert_state` and survives panel restarts (no second message). A stopped or deleted server resolves its alerts without a message. Without an answer from the agent or Docker, server alerts stay frozen. Messages never name host paths.
- **Dev mode (`make dev`):** `monitoring_ping_addr = ""` (no mc-router on the Mac), host values only for the disk (`statfs` on `.dev/mc`, no `/proc`), server values and TPS only with `MCPANEL_DEV_DOCKER=1`. The mock (`cd web && npm run dev:mock`) shows everything: `survival` with players and a TPS drop, `creative` stopped, `modded` unreachable with `server.down`, `lobby` without an instance; `db.monitoring.diskLow` makes `host.disk_low` fire.
- **CI** (`scripts/ci-instances.sh`, job `instances`): a panel in production mode rejects ntfy targets on localhost and private addresses, so a local ntfy mock is not reachable. Instead the acceptance creates an ntfy notifier at `https://ntfy.invalid` (`.invalid` never resolves per RFC 6761, and validation allows it): every event that reaches the dispatcher ends, after the retries, in exactly one JSON log line `"msg":"notify: delivery failed"` with `"type":"<event>"`. The acceptance checks these lines in the panel log and the alert state via `GET /api/monitoring/settings` (`alerts`) and `/api/monitoring/overview`. "Unreachable" is produced without stopping the container (which reconciliation would undo immediately) by an nftables rule that drops only the panel's pings: table `inet mcpanel_citest`, chain `output` (hook output, priority 0), `meta skuid mcpanel tcp dport 25565 drop`. The windows are shortened (`down_after_min = 1`, `crash_window_min = 10`, 1 to resolve the crash loop); the disk part is skipped with a `SKIP` line if the runner has less than 3 GiB or 3 % free (hysteresis above the minimum thresholds).

## Make targets

| Target | Effect |
|---|---|
| `make check` | the only local check on the Mac (~10 s): gofmt, `go vet` for macOS and Linux (Linux incl. the `nftintegration`/`dockerintegration` test files), `go build` for linux/arm64, `tsc`, and `bash -n` over the installer sources and the assembled `build/install.sh`. Tests, race, lint and E2E run on the test VM (`make test-vm`) and in CI |
| `make dev` | builds agent, files and panel, starts agent + `files -dev` + `panel serve -dev` + the Vite dev server, everything under `./.dev/`; if one process ends, the script stops all |
| `make preview` / `preview-logs` / `preview-down` | UI preview in Docker against the mock (see "UI preview in Docker"); only the user starts it |
| `make test` | Go tests (`test-go`) and frontend tests (`test-web`) |
| `make test-race` | Go tests with the race detector (`CGO_ENABLED=1`) |
| `make lint` | `golangci-lint run ./...` plus ESLint and the TypeScript check in the frontend |
| `make generate` | `sqlc generate` and `npm run gen:api` |
| `make web-build` | builds the frontend into `internal/webui/dist` |
| `make build` | builds the frontend (`web-build`), then `bin/panel`, `bin/agent` and `bin/files` for the host system |
| `make release` | GoReleaser snapshot to `dist/`: archives for linux/amd64 and linux/arm64 (with `deploy/`), `install.sh` and `checksums.txt` |
| `make installer` | assembles `scripts/install/` and `deploy/` into `build/install.sh` (`INSTALLER_VERSION`, default from `git describe`); never run the result on the Mac |
| `make deploy-testvm` | builds `REF` (default `main`) on the test VM from GitHub and deploys it there through the installer (`install` the first time, `update` afterwards; see "Test VM") |
| `make test-vm` | copies the working tree to the test VM and runs tests there, `SUITE=all\|go\|lint\|web\|e2e\|shots` (see "Tests on the test VM") |
| `make clean` | removes `bin/`, `build/`, `dist/`, `.dev/` and the built frontend |

## CI

A pull request runs only the jobs its changed files need; the first job, `changes`, decides this with `git diff` against the merge base. The weekly run (Mondays) and manual runs always run everything, as does any change to `.github/workflows/ci.yml`. Pushes to `main` run all jobs on the VM runners, but not the root jobs `nftables`, `instances` and `installer`: the pull request already ran them on the same tree, and they account for most of the billed GitHub minutes.

**Runners:** all jobs without root (`changes`, `go`, `web`, `openapi`, `e2e`, `build`, `installer-lint`, `CI OK`) run on four self-hosted runners on the test VM (`mcpanel-vm-1` to `mcpanel-vm-4`, label `mcpanel-vm`, as systemd services `actions.runner.Bernotese-mc-panel.mcpanel-vm-<n>` under the user `claude`, working directories `~/actions-runner-<n>/_work`). They cost no GitHub minutes; for them the VM additionally needs `gh`, `jq`, `file` and `shellcheck` (GitHub's runners have them preinstalled). The timing-sensitive test steps (race tests, Vitest, E2E) take the same lock as `make test-vm` (`~/.cache/mcpanel-vm-test.lock`), so CI and teammates never test at the same time; the E2E job is `scripts/vm-test.sh e2e` (own ports 18080/5174; the deployed panel on its socket is not touched). The root jobs change the host (nftables, Docker, port 25565) and run on GitHub-hosted runners (`ubuntu-24.04`). The VM jobs do not use the GitHub cache (`setup-go` with `cache: false`, `setup-node` without `cache`, golangci-lint with `skip-cache`): the runners share the persistent `~/.cache/go-build`, `~/go/pkg/mod`, `~/.npm` and `~/.cache/golangci-lint` of the user `claude`, and restoring and saving the multi-GB caches over the uplink took minutes per job; the GitHub-hosted root jobs keep the cache. If the VM is off, the VM jobs wait until it is back. There are four runners because several runs at once (teammates' PRs, `main`) would otherwise keep jobs waiting so long that GitHub cancels them with "not acquired by Runner"; the lock serialises the test steps anyway. To re-register a runner: get a token with `gh api -X POST repos/Bernotese/mc-panel/actions/runners/registration-token -q .token`, then on the VM in `~/actions-runner-<n>` run `./config.sh --unattended --url https://github.com/Bernotese/mc-panel --token <token> --name mcpanel-vm-<n> --labels mcpanel-vm --work _work --replace` and `sudo ./svc.sh install claude && sudo ./svc.sh start`. Only for this private repository: a self-hosted runner executes code from pull requests and must never be attached to a public repository. The job `CI OK` summarises (green when every job passed or was not needed); `gh pr checks <n> --watch` waits for it. The jobs run in parallel; none waits for another.

**Hard rule before the repository becomes public:** pull-request jobs run only on GitHub-hosted runners. The VM runners run as `claude` with passwordless sudo, are not ephemeral and share the LAN with the deployed panel; one pull request from a fork would get root on the VM and a foothold in the network. Before the switch, move every job with `runs-on: [self-hosted, mcpanel-vm]` that a `pull_request` can trigger to `ubuntu-24.04` (or to ephemeral, isolated runners under a user without sudo), and deregister the VM runners from the repository.

| CI job | Runs in a PR on changes to | On the test VM or by hand |
|---|---|---|
| `go` | `cmd/`, `internal/`, `api/`, `deploy/`, `go.mod`/`go.sum`, `sqlc.yaml`, `.golangci.yml`, `Makefile`, `scripts/install-restic.sh` | `make test-vm SUITE=go` and `SUITE=lint`; CI only: `go mod verify && go mod tidy -diff`, sqlc freshness (release binary of the version in the header of `internal/store/db.go`), `govulncheck` |
| `web` | `web/`, `api/` (preview image only on changes to `web/Dockerfile.preview`, `compose.preview.yaml`, `web/package*.json`, `web/vite.config.ts`) | `make test-vm SUITE=web` and `SUITE=lint`; CI only: build, `gen:api` freshness, `npm audit`, preview image |
| `openapi` | `api/`, `tools/redocly/` | `(cd tools/redocly && npm ci --ignore-scripts) && tools/redocly/node_modules/.bin/redocly lint api/openapi.yaml --extends=minimal` |
| `e2e` | `cmd/`, `internal/`, `api/`, `web/`, `go.mod`/`go.sum` | `make test-vm SUITE=e2e` |
| `nftables` | `internal/agent/`, `internal/defense/`, `internal/auth/`, `cmd/agent/`, `cmd/panel/`, `scripts/ci-acceptance.sh`, `go.mod`/`go.sum` | CI only (see below) |
| `instances` | `cmd/`, `internal/` (the acceptance starts agent, files service and panel including sign-in, TOTP, monitoring and audit), `scripts/ci-instances.sh`, `scripts/mc-status.py`, `scripts/install-restic.sh`, `go.mod`/`go.sum` | CI only (see below) |
| `build` | `cmd/`, `internal/webui/`, `deploy/`, `scripts/install/`, `.goreleaser.yaml`, `Makefile`, `web/package*.json`, `web/vite.config.ts`, `go.mod`/`go.sum` | `make build`, `make release` |
| `installer-lint`, `installer` | `scripts/install/`, `deploy/`, `scripts/ci-installer.sh`, `scripts/install-restic.sh`, `scripts/testvm-deploy.sh`, `cmd/`, `internal/config/`, `internal/unixsock/`, `.goreleaser.yaml`, `Makefile`, `go.mod`/`go.sum`; `installer` also on any change under `internal/` (like `instances`) and not on pushes to `main` | `installer-lint`: `make test-vm SUITE=lint`; `installer`: CI only (changes the system), single functions with `scripts/install/test.sh` |

The `go` job runs the tests only with the race detector (`make test-race` covers all tests); the release workflow still runs both `make test` and `make test-race`.

CI job details:

| CI job | Content |
|---|---|
| `go` | `go mod verify && go mod tidy -diff`, `sqlc generate && git status --porcelain internal/store`, restic via `scripts/install-restic.sh` (`MCPANEL_TEST_RESTIC`), `make test-race`, golangci-lint v2.14.0, `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| `web` | `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && npm run gen:api && git status --porcelain . && npm audit --audit-level=high`; builds the preview image (`web/Dockerfile.preview`) when its inputs changed |
| `openapi` | `(cd tools/redocly && npm ci --ignore-scripts) && tools/redocly/node_modules/.bin/redocly lint api/openapi.yaml --extends=minimal` |
| `e2e` | `scripts/vm-test.sh e2e` (see "End-to-end tests (Playwright)"); on failure the dev logs and `web/test-results`/`web/playwright-report` as an artifact |
| `nftables` | CI only (Linux, root): `go test -tags nftintegration -exec sudo ./internal/agent/...` and the acceptance test `sudo scripts/ci-acceptance.sh bin/panel bin/agent` (5 failed sign-ins → IP in the nftables jail). The script changes the host (users, network namespace, nftables) and is meant for disposable runners only. |
| `nftables` (Docker integration) | CI only: `go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" ./internal/agent/...` |
| `instances` | CI only: `sudo scripts/ci-instances.sh bin/agent bin/files bin/panel` (real agent + Docker + Paper server: routing, hardening, console with RCON, log stream and player lists, planted player files, crash restart, deletion; files service as `mcpanel-mc`: peer UID (panel user and root allowed, `nobody` rejected), upload, redaction, editor lock, symlinks, folder zip, extracting a world, malicious archives; plugins: panel in production mode with TOTP sign-in and sudo, a second Paper server through the API, Chunky from Modrinth and ViaBackwards + ViaVersion from Hangar via the real upstreams (one retry on upstream errors), checksums in the files service, icon proxy, restart with RCON `plugins`, disable, remove, audit; backups with a real restic on the same server: manual backup with `save-off`/`save-on` in the log, repo and tar contents, restore with sudo (start blocked, safety snapshot, restart), zip download, schedule in the panel's time zone, pin, delete, purge when deleting the server, audit; monitoring on the same server: live values, history, host values, RCON noise without viewers, `server.down`/`server.recovered` via an nftables rule against the panel's pings, `server.crash_loop` after three crashes without `server.down`, `host.disk_low`/`host.disk_ok`, the down-alerts switch, delivery to the dispatcher via `https://ntfy.invalid` (see "Monitoring and alerts"), audit; installs nftables, changes the host and needs internet; takes about 25 min (monitoring part about 15 min), timeout 70 min) |
| `build` | `make build`, checks of the host binary (static, no cgo, embedded UI), cross-compile for linux/arm64, `goreleaser check` and a GoReleaser snapshot (v2.18.2), then checks of the six binaries (architecture, static, no cgo, version string, embedded UI in `panel`), the archive contents (binaries and `deploy/`) and `install.sh` in `checksums.txt` |
| `installer-lint` | `make installer INSTALLER_VERSION=0.0.0-ci`, shellcheck over `scripts/install/*.sh`, `scripts/install/tests/*.sh`, `build/install.sh`, `scripts/ci-installer.sh` and `scripts/testvm-deploy.sh`, then `bash scripts/install/test.sh` |
| `installer` | CI only (GitHub runner `ubuntu-24.04`, root, `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1`): `sudo scripts/ci-installer.sh .` builds three stages in the `--from-dir` format (`0.0.1-ci`, `0.0.2-ci`, `0.0.3-ci` with a panel that exits at once), installs restic 0.19.1 (Ubuntu 24.04 ships 0.16) and runs through the lifecycle: `install` with `--tls internal`, domain `panel.ci.test` (`/etc/hosts`), firewall and `--ssh-port 22`, admin via stdin; units, sockets, firewall rules and outbound HTTPS; `doctor --json` without failures; a second `install` without `[changed]`; sign-in through Caddy (internal CA); `update` to `0.0.2-ci` with a backup under `/var/lib/mc-panel-rollback/`, DB unchanged; `update` to the broken stage → exit code 3, `0.0.2-ci` runs again, DB unchanged, `doctor` without failures; `uninstall` (units, binaries, Caddy site, tables `inet mcpanel_fw`/`inet mcpanel` and a container with label `mcpanel.instance` gone; data, configuration, users, a foreign container and a foreign table stay); a new `install` finds the admin and the data; `uninstall --purge` through a pseudo-terminal (`script`): a wrong domain aborts, the right one removes everything. Timeout 30 min |

`make test-vm SUITE=lint` lints the Linux-specific files (golangci-lint runs on Linux there).

**Supply chain:** the self-hosted CI jobs and `scripts/vm-test.sh` install npm packages with `npm ci --ignore-scripts` (no lifecycle scripts). Dependabot opens weekly, grouped updates for Go modules and npm. `npm run build` ends with `scripts/check-dist.mjs`, which fails if `internal/webui/dist` contains the MSW worker, source maps or mock code.

**CSP in tests:** e2e specs import `test` from `web/e2e/test.ts` (eslint enforces it); every test fails on a CSP or Trusted Types violation report. The Vite dev server (`make dev`, the e2e mock) sends the panel's CSP too.

## Lint conventions

- The configuration is in `.golangci.yml`; gosec runs as part of golangci-lint.
- Exceptions go into `.golangci.yml` as a targeted rule (path + linter + text, with a reason) or as `//nolint:<linter> // <reason>` directly on the line. Never disable a linter globally.
- Active exclusion presets: only `std-error-handling` (ignores unchecked errors of `Close`, `Flush`, `os.Remove`, `fmt.Fprint*` and similar). `common-false-positives` is deliberately **not** active: gosec G103 (`unsafe`), G204 (subprocess with a variable) and G304 (file path from a variable) stay visible and need a targeted rule or `//nolint:gosec // <reason>` each time.
- gosec rules disabled globally (`linters.settings.gosec.excludes`): G104 (unchecked errors, covered by errcheck), G115 (integer conversion; fires on every `uint32(len(x))`/`int(uid)` conversion, and bounds are checked where input is untrusted) and G117 (field names such as `password`/`secret` in API DTOs are part of the OpenAPI contract).
- Requests and DB calls always take a context: `http.NewRequestWithContext`, `QueryContext`/`ExecContext`/`PingContext`. The exception is `net.Listen` in the agent (`cmd/agent/`, `internal/agent/`).
- File mode `0660` (agent socket, spec §3) is allowed for gosec G302 only in `cmd/agent/` and `internal/agent/`, and only as a literal (`0o660`) on the line; everywhere else, and for other modes above `0600`, a justification is needed (for example `internal/unixsock`, which sets the mode of the agent, panel and files service sockets). The files service (`internal/files`) creates files with 0644 and directories with 0755 (like the Minecraft server itself; the unit's `UMask=0027` applies); those lines carry `//nolint:gosec // G302/G304 …` or `G301` with a reason; there is no path rule.
- In tests, fixed test passwords, `math/rand` and loose file modes are allowed.
- Formatting: `gofmt` + `goimports` with the local prefix `github.com/Bernotese/mc-panel` (`golangci-lint fmt ./...`).

## Release

```bash
git switch main && git pull --ff-only
make test-vm                    # all suites on the test VM
# work through docs/dev/acceptance.md on the test VM (make deploy-testvm)
git tag -a v0.1.0 -m "mc-panel v0.1.0"
git push origin v0.1.0
```

The workflow `.github/workflows/release.yml` first checks that the tagged commit is contained in `main` (otherwise it aborts; both jobs check this). The job "Lint and tests" runs `make lint`, `make test` and `make test-race` on the self-hosted runners of the test VM (read access only, same lock as `make test-vm`; on a GitHub-hosted runner Vitest alone took more than 13 minutes). Only then does the job "GoReleaser", on a fresh GitHub-hosted runner without caches, build the frontend (`make web-build`), assemble the installer (`make installer`) and publish with GoReleaser (`--skip=before`; only this last step sees `GITHUB_TOKEN`):

- `mc-panel_<version>_linux_amd64.tar.gz`, `mc-panel_<version>_linux_arm64.tar.gz` (each with `panel`, `agent` and `files` plus `deploy/` with the installer's templates)
- `install.sh` (the assembled installer of this version)
- `checksums.txt` (SHA-256 over all assets, format `<hash>  <file>`)

`<version>` is the tag without the leading `v`. Tags with a suffix (for example `v0.2.0-rc.1`) are marked as pre-releases.
Check locally before tagging: `make release && (cd dist && shasum -a 256 -c checksums.txt)`.

Notes on GoReleaser:

- Milestone tags such as `phase0` are not SemVer and are ignored via `git.ignore_tags: ["phase*"]` in `.goreleaser.yaml`.
- `goreleaser check` and the Git status detection need a configured Git remote. Without a remote, `make release` still works in snapshot mode (version `0.0.1-snapshot+none`); `goreleaser check` aborts with `no remote configured`. Without changing the repo's remote, this helps: `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=remote.origin.url GIT_CONFIG_VALUE_0=https://github.com/example/mc-panel.git goreleaser check`.
- From Go 1.27 on, `go version -m` no longer records the `-ldflags`. Only the string itself shows whether the version was linked in: `grep -aF "$(jq -r .version dist/metadata.json)" dist/agent_linux_arm64_v8.0/agent` (requires `var version` in `package main`).
