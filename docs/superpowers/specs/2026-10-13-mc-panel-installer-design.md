# mc-panel – Sub-project 6: Installer (design spec)

- **Date:** 2026-10-13
- **Status:** implemented (PRs #69, #71, #72, #75, 2026-10-14)
- **Author:** Jannes Göbel (with Claude)
- **Builds on:** all previous specs (sub-projects 1–5); collects their requirements "for the installer (sub-project 6)"

## 1. Goal and context

One command sets up mc-panel completely on a fresh Ubuntu 26.04 VPS: packages, system users, directories, binaries, configuration, systemd units, Caddy with a Let's Encrypt certificate, host firewall, security updates and the first admin. The same installer updates (with a DB backup and automatic rollback), checks the state (`doctor`) and removes the panel again (`uninstall`; data stays).

### 1.1 Success

```
curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
  | sudo bash -s -- install --domain panel.example.com --email you@example.com
```

on a fresh Ubuntu 26.04 leads within a few minutes to a reachable panel at `https://panel.example.com` with a valid certificate and an admin account. A second run changes nothing. `update` brings a new version without data loss and rolls back on its own if the health check fails. `doctor` shows every configuration error with a fix. The test VM is deployed with exactly this installer.

### 1.2 Guiding principle

The installer is the single source of truth for the system setup (users, permissions, units, configuration). It implements the security requirements of all sub-projects (privilege separation panel/agent/files, socket permissions, unit hardening) and changes only what the panel needs on the system. It does not touch SSH, kernel parameters or other services.

### 1.3 Non-goals (sub-project 6)

- Other distributions and Ubuntu 24.04 (24.04 only through the test override, §3.1).
- SSH hardening, fail2ban, sysctl tuning (only warnings in `doctor`).
- A separate file system or loop image for `/srv/mc-panel` (only a warning in `doctor`; the requirement from sub-project 2 §3.3 stays a recommendation to the operator).
- Wildcard certificates via DNS challenge.
- Signed releases; the installer checks the SHA-256 from `checksums.txt` of the same release over HTTPS.
- Automatic updates by timer; migration of manual installations (except the test VM).

## 2. Decisions

| Area | Decision | Reason |
|---|---|---|
| Form | One Bash script `install.sh` with `install`, `update`, `doctor`, `uninstall`; sources per step in `scripts/install/`, assembled at release | One command via `curl \| bash`, no runtime dependencies on the VPS |
| Origin | Release tarball + `checksums.txt` from GitHub (HTTPS), SHA-256 check before unpacking; `--from-dir` for local builds | Simple; signatures are deliberately postponed |
| System | Ubuntu 26.04 LTS only, packages from the Ubuntu repos | One package baseline, testable |
| TLS | Caddy (Ubuntu package) with Let's Encrypt via HTTP challenge, panel domain + e-mail as parameters | Standard path without plugins |
| Hardening | Host firewall (nftables, own table), unattended-upgrades for security updates; SSH only checked | What the panel needs, without the risk of locking anyone out |
| Storage | Installation on the existing file system; `doctor` warns if `/srv/mc-panel` has no file system of its own | No changes to partitions |
| Update | Back up a DB copy + old binaries/units, swap, health check, automatic rollback | Migrations are forward-only; a failed update must not leave the panel down |
| Uninstall | Program gone, data stays; `--purge` with confirmation by typing the domain | Worlds and backups are worth more than tidiness |
| Test VM | `testvm-deploy.sh` calls the installer with `--from-dir` | Every deploy tests the installer and the update path |

### 2.1 Rejected alternatives

- **Go binary `mcpanelctl` with a bootstrap script:** easier to test, but much more code; the script stays the same for the user.
- **Ansible:** an additional dependency, no one-command experience.
- **Docker Compose installation of the panel:** contradicts the privilege separation (agent as root on the host, files as `mcpanel-mc`).
- **Docker CE and Caddy from vendor repos:** newer versions, but third-party APT sources and keys; the Ubuntu packages are enough.

## 3. Structure

```
scripts/install/
├── main.sh        arguments, subcommands, logging, error handling (set -euo pipefail, trap)
├── lib.sh         helper functions (log, die, confirm, version_ge, render_template, toml_ensure_key)
├── preflight.sh   §4.1
├── packages.sh    §4.2
├── layout.sh      §4.3 users and directories
├── binaries.sh    §4.4 download, verify, install
├── config.sh      §4.5
├── units.sh       §4.6
├── caddy.sh       §4.7
├── firewall.sh    §4.8
├── admin.sh       §4.10
├── update.sh      §5
├── doctor.sh      §6
├── uninstall.sh   §7
└── test.sh        Bash tests of the pure functions (§9)
deploy/
├── systemd/*.service, *.socket                  unit templates
├── caddy/mc-panel.caddy                         Caddy template
└── nftables/mcpanel_fw.nft                      firewall template
```

(Now: `scripts/install/` also contains `upgrades.sh` (§4.9), `assemble.sh` (the assembly behind `make installer`) and `tests/` (the test cases that `test.sh` runs); `deploy/config/` holds the templates for `panel.toml`, `agent.toml` and `files.toml`. `lib.sh` also provides the `sys_*` wrappers through which the steps call system commands, so the tests can run them as a dry run.)

- `make installer` joins `lib.sh`, the step files and `main.sh` into `build/install.sh` (not `dist/`, which goreleaser empties) and embeds the templates from `deploy/` as heredocs; the release version is written in as `MCPANEL_VERSION`. goreleaser attaches `install.sh` as a release asset and adds it to `checksums.txt`; the release tarball also contains `deploy/`.
- Logging to `/var/log/mc-panel-install.log` (0600) and briefly to stdout; every step reports "ok / changed / skipped". Errors exit with a non-zero exit code and a pointer to the log.
- All steps are idempotent: they check the current state and change only what differs.

### 3.1 Parameters

`install`: `--domain <fqdn>` (required except with `--tls internal`), `--email <address>` (required with Let's Encrypt), `--version vX.Y.Z` (default: the script's version), `--from-dir <path>` (binaries + `deploy/` locally), `--tls letsencrypt|internal` (default `letsencrypt`), `--ssh-port <n>` (default from `sshd -T`), `--allow-port PORT[/tcp|/udp]` (further open ports, repeatable, §4.8), `--no-firewall`, `--admin-user <name>` (the password then comes from stdin when `install.sh` runs as a file, or from `--admin-password-file <file>`, owned by root, 0600/0400; with `curl | bash` stdin is the script), `--yes` (do not ask about warnings). `update`: `--version`, `--from-dir`, `--check`, `--force`. `doctor`: `--json`. `uninstall`: `--purge`, `--yes`.

Environment for tests only: `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1` allows other Ubuntu versions (CI runner 24.04).

Output: every step starts with `==> <step>`, every result with `[ok]`, `[changed]`, `[skip]` or `[warn]` (CI checks that a second run reports no `[changed]`).

## 4. `install`

### 4.1 Preflight

root; `/etc/os-release` = Ubuntu 26.04 (otherwise abort, except with the override); architecture `x86_64`/`aarch64`; systemd as PID 1; ports 80, 443 and 25565 free or held by `caddy` or the router; the domain's resolution (A/AAAA) contains an address of this host (compared with the local addresses and, if reachable, the public IP via the source address of the route). A mismatch is a warning (Let's Encrypt would fail); with `--yes` the installer continues.

### 4.2 Packages

`apt-get install` (only missing ones): `docker.io` (skipped if `docker info` already answers, e.g. a preinstalled Docker CE), `caddy`, `nftables`, `restic`, `tzdata`, `sqlite3`, `unattended-upgrades`, `curl`, `ca-certificates`, `bzip2`. Then version checks: restic ≥ 0.17 (sub-project 4), Docker daemon reachable. Docker gets `/etc/docker/daemon.json` only if it is missing: `{"log-driver": "json-file", "log-opts": {"max-size": "10m", "max-file": "3"}, "live-restore": true}`. Docker is restarted for this only if no mcpanel containers are running; otherwise `[warn]` that the setting takes effect at the next Docker restart. `restic_binary` is the path from `command -v restic` (version ≥ 0.17, otherwise abort with a hint to install a newer restic).

(Now, security review SEC-01: `caddy` comes from Caddy's official repository instead of universe, which gets no security updates. Before the package install the installer downloads `https://dl.cloudsmith.io/public/caddy/stable/gpg.key`, dearmors it with gpg (installed from the archive if missing) in an empty home in the work directory and trusts it only if it holds exactly the pinned primary key `65760C51EDEA2017CEA2CA15155B6D79CA56EA34`; then it writes `/usr/share/keyrings/caddy-stable-archive-keyring.gpg` and `/etc/apt/sources.list.d/caddy-stable.list` (`signed-by=`, the file names of Caddy's own guide). A caddy installed from elsewhere is upgraded from the repository with `--force-confold`; then the current Caddyfile is validated as caddy and Caddy restarted and checked. If that fails, the previous version is reinstalled and held, with a warning; the next run tries again and releases the hold. While containers run, a new `daemon.json` is applied with `systemctl reload docker` (live-restore is reloadable; log options wait for the next restart).)

### 4.3 Users and directories

As in the specs and `testvm-deploy.sh` at the time: system users `mcpanel` and `mcpanel-mc` (no shell, no home login); `/etc/mc-panel` `root:mcpanel 0750`, `/etc/mc-panel-files` `root:mcpanel-mc 0750`, `/var/lib/mc-panel` `mcpanel:mcpanel 0700`, `/srv/mc-panel` `root:root 0755`. In addition the installer creates `/srv/mc-panel/instances` (`root:mcpanel-mc 0750`) and `/srv/mc-panel/backups` (`mcpanel-mc:mcpanel-mc 0700`) before the units start: `mcpanel-files.service` lists both in `ReadWritePaths`, and systemd requires them to exist (otherwise `226/NAMESPACE`). The agent creates the further subdirectories (sub-project 2 §3.3, sub-project 3b §3.3, sub-project 4 §3.1).

### 4.4 Binaries

Download the tarball `mc-panel_<version>_linux_<arch>.tar.gz` and `checksums.txt` of the same release (`curl --proto =https --tlsv1.2 -fsSL`, 3 retries), compare the SHA-256, unpack into a temporary directory, install `panel`, `agent`, `files` to `/usr/local/bin` (0755, atomically via `install` + `mv`) and the tarball's `LICENSE`, if it has one, to `/usr/local/share/doc/mc-panel/LICENSE` (0644; removed by `uninstall`). `--from-dir` skips the download and only checks that the three binaries and `deploy/` exist and that `panel version` answers.

### 4.5 Configuration

`panel.toml`, `agent.toml`, `files.toml` are created from the templates if they are missing: sockets, `panel_uid` (UID of `mcpanel`), `instances_dir`, `restic_binary` (§4.2), `monitoring_ping_addr`, `public_url = "https://<domain>"`, `listen = "unix:/run/mc-panel-web/panel.sock"` (§4.6). Existing files stay unchanged; if required keys are missing, `install`/`update` appends only those keys at the end (with a comment "added by install.sh <version>") and never changes existing values. Permissions: `panel.toml` `root:mcpanel 0640`, `agent.toml` `root:root 0600` (the agent runs as root, nobody else needs to read it), `files.toml` `root:mcpanel-mc 0640`.

### 4.6 systemd

Units from `deploy/systemd/`: `mcpanel-agent.service`, `mcpanel-files.socket` + `mcpanel-files.service`, `mcpanel-panel.socket` + `mcpanel-panel.service`, `mcpanel-fw.service` (§4.8); hardening as in the specs (sub-project 1, sub-project 3b §3.1, sub-project 4 §3.2). New: the panel socket comes through systemd socket activation (`ListenStream=/run/mc-panel-web/panel.sock`, `SocketUser=mcpanel`, `SocketGroup=caddy`, `SocketMode=0660`, `DirectoryMode=0755`), so that Caddy can reach it without being a member of the group `mcpanel`; the panel takes it over via `LISTEN_FDS` like the files service (sub-project 6 adds this to `cmd/panel`, code from `internal/unixsock`). Then `daemon-reload`, `enable`, start in the order agent → files → panel, wait for `active` (60 s).

(Now, SEC-12: all three services are sandboxed beyond the specs: capabilities (none for panel and files; the agent keeps `CAP_CHOWN CAP_DAC_OVERRIDE CAP_DAC_READ_SEARCH CAP_FOWNER CAP_NET_ADMIN`), address families (agent `AF_UNIX AF_NETLINK` with `IPAddressDeny=any`; files `PrivateNetwork=yes`), `ProtectSystem=strict` with only the needed writable paths, `SystemCallFilter=@system-service` with `SystemCallErrorNumber=EPERM`, `ProtectProc=invisible` (files also `ProcSubset=pid`), `MemoryDenyWriteExecute`, `RestrictNamespaces` and the `Protect*` kernel options; the units explain every directive.)

### 4.7 Caddy

`/etc/caddy/mc-panel.caddy` from the template: site `<domain>` with `tls <email>` (or `tls internal`), `reverse_proxy unix//run/mc-panel-web/panel.sock`; the security headers come from the panel itself. `/etc/caddy/Caddyfile` gets the line `import mc-panel.caddy` once (marked with a comment); existing sites stay. `caddy validate`, then `systemctl reload caddy`. With `letsencrypt` the installer waits up to 120 s for a certificate (`curl https://<domain>/api/health`) and otherwise reports a warning with the last Caddy log lines (most common cause: DNS, or port 80 blocked).

(Now, SEC-14 and SEC-35: a Caddyfile the installer owns (missing, the unmodified package default, or exactly a Caddyfile an older installer wrote) has the global option `admin unix//run/caddy/admin.sock`; the drop-in `/etc/systemd/system/caddy.service.d/mc-panel.conf` (`RuntimeDirectory=caddy`, `LogsDirectory=caddy`, both 0750, no version in the file) creates its directory. When the Caddyfile or the drop-in changed, Caddy is restarted instead of reloaded, because `caddy reload` sends to the admin address of the new configuration. A foreign Caddyfile keeps its admin setting (`doctor` warns). The site writes an access log to `/var/log/caddy/mc-panel.log` (`roll_size 50MiB`, `roll_keep 14`, the filter encoder drops `X-Csrf-Token`). Uninstall keeps the drop-in and the repository. The site imports `/etc/caddy/mc-panel.d/*.caddy` for the operator's additions; the installer creates that directory (`root:root 0755`) only when it is missing and never touches it otherwise.)

### 4.8 Firewall

Own table `inet mcpanel_fw` (never the agent's `inet mcpanel`) from `deploy/nftables/mcpanel_fw.nft`: chain `input` (hook input, priority 0, policy drop) allows `iif lo`, `ct state established,related`, ICMP/ICMPv6 (incl. neighbor discovery), TCP on the SSH port, 80, 443, 25565 and UDP 443 (HTTP/3 from Caddy). Forwarded traffic (Docker) is not affected. `mcpanel-fw.service` (oneshot, `Before=network-pre.target` and `Wants=network-pre.target` so that the rules are in place before the network, `RemainAfterExit`) loads the table at boot; `ExecStop` removes it. Before activating (already in the preflight and once more right before `nft -c`) the installer determines the SSH ports: `--ssh-port`, otherwise the ports from `sshd -T` (`listenaddress` with a port before `port`), compared with the sockets that sshd or `ssh.socket` actually listens on; if they differ, abort; only if both are missing, the port of the current session with a warning. Every port of a running SSH session (`$SSH_CONNECTION`, which `sudo` drops, and the existing connections on an sshd port) must be part of the rule, otherwise abort. The installer lists all other listening ports outside loopback that the rules would block and asks (without a terminal and without `--yes`: abort). `--allow-port PORT[/tcp|/udp]` (repeatable, default `tcp`) opens such ports as separate rule lines (`# --allow-port`); the rules are rebuilt from the arguments on every `install`, so a later `install` without the same `--allow-port` closes the ports again; `--yes` blocks them without asking. The rules live in `/etc/mc-panel/mcpanel_fw.nft` and are checked with `nft -c` before writing and before loading. `--no-firewall` skips the step (the unit is written but never enabled); `doctor` then warns.

### 4.9 Security updates

`unattended-upgrades` only with the origin `${distro_id}:${distro_codename}-security`, without automatic reboot; `/etc/apt/apt.conf.d/20auto-upgrades` with a daily run.

(Now: `scripts/install/upgrades.sh` writes its own files `/etc/apt/apt.conf.d/21mc-panel-auto-upgrades` (daily package list update and unattended upgrade) and `/etc/apt/apt.conf.d/52mc-panel-unattended-upgrades` (`Automatic-Reboot "false"`) and enables `unattended-upgrades.service`. It keeps the allowed origins of Ubuntu's `50unattended-upgrades` (the release and its `-security` pocket, ESM security; `-updates` is off), because apt appends list entries and a file of ours could only widen them. It widens them by exactly the Caddy repository: `Unattended-Upgrade::Origins-Pattern { "origin=cloudsmith/caddy/stable,archive=any-version"; }`.)

### 4.10 First admin and completion

If there is no admin yet (`panel admin list`, new and read-only, or a check via the exit code of `admin create`), the installer asks for user name and password on `/dev/tty` (twice, hidden) or takes `--admin-user` with the password on stdin, and runs `panel admin create -config /etc/mc-panel/panel.toml` as user `mcpanel`. Finally `doctor` runs; the output names the URL, the admin name and the next steps (enter the base domain with wildcard DNS in the panel, set up 2FA, set up notifications, include `backups/keys/` in your own backups).

## 5. `update`

1. Target version: `--version`; with `--from-dir` the version of the build (`panel version`, no network); otherwise the latest release (GitHub API `releases/latest`; the tag is always `v<version>`). `--check` only prints "up to date / newer version X". Same version without `--force` → done.
2. The `install.sh` of the target version is downloaded too (and verified) and takes over from here (`exec`), so that the update logic matches the new version.
3. Backup to `/var/lib/mc-panel-rollback/<old-version>-<time>/` (`root:root 0700`, deliberately outside `/var/lib/mc-panel`, which belongs to `mcpanel`: a compromised panel must not be able to place anything there that root installs during a rollback): the three binaries, the unit files, the three configuration files (`panel.toml`, `agent.toml`, `files.toml`, with owner and mode in `config/files`; `update` may add keys to them, and the rollback writes the old files back atomically) and the DB. `mcpanel` creates the DB copy itself (`runuser -u mcpanel -- sqlite3 <db> ".backup '<data_dir>/.rollback-<time>.db'"`, consistent while running); root checks the intermediate file (regular file, no symlink, owner `mcpanel`), reads it with `cat` into the rollback directory, and then `mcpanel` deletes it. Root never writes into `/var/lib/mc-panel`. The three newest backups are kept (backups of a failed rollback are never deleted).
4. Install the new binaries and units, add missing configuration keys (§4.5), `daemon-reload`, restart agent → files → panel. Minecraft containers keep running (restarting the agent stops no containers; `live-restore` covers a Docker restart caused by package updates).
5. Health check (at most 90 s): all units `active`, `panel version`/`agent -version`/`files -version` report the target version, `GET /api/health` over the socket returns `200`, the agent and files sockets answer (`/v1/health`). (Now: once the target is healthy, `update` also runs the Caddy package step (repository, switch of an existing install), the Caddy step (§4.7) and the security-update step (§4.9), each in its own subshell; they are outside the rollback copy, and a failure there is only a warning.)
6. Rollback on failure: stop the services, restore the old binaries, units and configuration files; root reads the DB copy from the rollback directory and pipes it into `runuser -u mcpanel -- sh -c 'cat > <data_dir>/panel.db.restore && mv -f <data_dir>/panel.db.restore <data_dir>/panel.db'` (so root follows no symlinks there), `daemon-reload`, start, health check of the old version; a message with the cause and a pointer to the log, exit code 3. If that fails too, everything stays stopped with clear instructions (exit code 4).
7. Incomplete updates: every rollback directory has a state file `state` (`phase=backed_up|applying|verified|rolling_back|rolled_back|aborted|rollback_failed`, `from=`, `to=`), written atomically and with `sync` before every stage. If a run ended in `applying` or `rolling_back` (power failure, `kill -9`, a dropped SSH session that still ended the run), the next `update` handles that first (`--check` only reports it, `doctor` shows it as ✗ `update_state`): `rolling_back` → the rollback runs again; nothing of the swap happened (binaries, units and configuration equal to the backup) → `aborted`, nothing to do; target version installed and healthy → `verified`, the DB stays; otherwise a rollback including the DB copy, which discards all changes since the backup, only after explicit consent (a prompt with the age of the copy, `--yes` or `--force`); without consent exit code 1 and nothing changed. Ctrl-C, `TERM` and `HUP` during the backup abort without changes; during the swap they lead to the rollback (the rollback itself ignores them). The cleanup rule (three newest) never deletes directories in `applying`, `rolling_back` or `rollback_failed`.

## 6. `doctor`

Checks with ✓ / ! / ✗ and one sentence of remedy; exit code 0 (all ok), 1 (warnings), 2 (errors); `--json` for scripts with the fixed schema `{"status": "ok|warn|fail", "checks": [{"id", "status", "message", "hint"}]}`. Without root only the checks that need no root privileges run. The required configuration keys are the same lists that `install` adds.

- Services: units active, sockets with the expected owner and mode, the `files` process running as `mcpanel-mc` without capabilities and with `no_new_privs` (as `testvm-deploy.sh` checked it at the time).
- Versions: the three binaries match; a newer release available (!, only with network).
- Configuration: required keys present, permissions of the files and directories (§4.3, §4.5).
- Caddy: configuration valid, import line present, certificate for the domain valid and > 14 days (! below that, ✗ expired), domain points to the host.
- Firewall: table `mcpanel_fw` loaded, SSH port allowed; without the firewall !.
- System: Docker reachable, restic ≥ 0.17, `tzdata` installed, time synchronization active (`timedatectl show -p NTPSynchronized`), unattended-upgrades active.
- Storage: `/srv/mc-panel` on its own file system (otherwise ! with a reference to sub-project 2 §3.3), free space (! below 10 %), `backups/keys/` present (hint: include it in backups).
- SSH (! only): `PasswordAuthentication yes`, `PermitRootLogin yes`.
- (Now, SEC-01, SEC-04, SEC-14, SEC-33; all warnings: `caddy_package` (Caddy < 2.10 or not from the Caddy repository), `caddy_admin` (admin API on TCP), `docker_version` (< 29.1.3, Ubuntu 26.04's security version), `docker_live_restore` (set in `daemon.json` but not active; hint `systemctl reload docker`), `docker_isolation` (SEC-03: Minecraft containers still on the shared network `mcpanel-mc`; hint: restart them once), `docker_dns` (a nameserver in the egress deny list, from `/etc/resolv.conf` without the systemd-resolved stub or from `dns` in `daemon.json`), `reboot_required` (with age and packages), `ssh_kbdinteractive` (`KbdInteractiveAuthentication yes` with `UsePAM yes`); `storage_fs` warns when `/srv/mc-panel` shares its file system with `/var/lib/mc-panel` or Docker's data root.)

## 7. `uninstall`

- Removes: units (stop, disable, delete), binaries, `/etc/caddy/mc-panel.caddy` and the import line (then a Caddy reload), the nftables tables `inet mcpanel_fw` and `inet mcpanel`, all containers with the label `mcpanel.instance`, the container `mcpanel-router` and the network `mcpanel-mc` (directly via `docker`; the agent is stopped by then), the sockets.
- Keeps: `/srv/mc-panel` (worlds, backups, keys), `/var/lib/mc-panel` (DB), `/var/lib/mc-panel-rollback` (backups; `--purge` removes it), `/etc/mc-panel*`, users `mcpanel`/`mcpanel-mc`, all packages (the installer never removes packages).
- `--purge`: also data, configuration and users; requires typing the panel domain (host of `public_url` in `panel.toml`, also with `tls internal`; only without a readable domain the word "mc-panel") on the terminal, even with `--yes`; without a terminal `--purge` aborts. Whatever `--purge` cannot delete it names at the end (exit code 1); a mount point (e.g. `/srv/mc-panel` on its own file system) stays, its content goes.
- A later reinstallation finds the DB, configuration and data again and runs like an update on the same state.

## 8. Test VM and CI

- **Test VM:** `scripts/testvm-deploy.sh` builds on the VM as before and then runs `install.sh` from the build: `install --from-dir <build> --tls internal --domain panel.192-168-1-117.sslip.io --no-firewall --yes` the first time, otherwise `update --from-dir <build> --force`. The heredocs for units, configuration and Caddy go away there. The VM keeps its firewall as before (`--no-firewall`, the VM is internal).
- **CI job `installer`** (`scripts/ci-installer.sh`; GitHub runner `ubuntu-24.04`, root, `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1`, not on pushes to `main`; three own builds in the format of `--from-dir`; restic 0.19.1 via `scripts/install-restic.sh`, because 24.04 only has 0.16; `--tls internal` with the domain `panel.ci.test` via `/etc/hosts`, firewall on with `--ssh-port 22`): `install` → `doctor` (no ✗) → second `install` (no change reported) → login through Caddy with the created admin → `update --from-dir` to the same build with a raised version (target version from the build's `panel version`, no network) → `update` with a deliberately broken `panel` (health check fails) → rollback, old version runs, DB unchanged → `uninstall` (data stays, services and tables gone) → `install` finds the data again → `uninstall --purge`. Let's Encrypt cannot be tested in CI (manual acceptance on the VPS).
- **Lint:** shellcheck for `scripts/install/*.sh` and the assembled `install.sh` in the lint job. (Now: a CI job of its own, `installer-lint` ("Installer (shellcheck, Bash tests)"), which also runs `scripts/install/test.sh`; locally `make test-vm SUITE=lint`.)

## 9. Tests

- `scripts/install/test.sh` (Bash, no new dependency, runs in the lint job and on the VM): argument parsing and error messages, `version_ge`, evaluation of `sshd -T`, template rendering (Caddy, units, nftables) against expected files, `toml_ensure_key` (adds missing keys, never changes existing ones), checksum verification with a prepared `checksums.txt` (matches / does not match / missing), assembly (`make installer`) produces a working script.
- Go: socket activation of the panel (`LISTEN_FDS`, tested like the files service), `panel admin list`.
- End to end: CI job `installer` (§8) and every `make deploy-testvm`.
- Manual checklist: `docs/dev/acceptance.md` ("Installer").

## 10. Acceptance criteria

1. On a fresh Ubuntu 26.04 VPS with a domain, the one-liner leads to a panel with a valid Let's Encrypt certificate and admin login; a second run reports no changes.
2. On the fresh system `doctor` reports only the expected warnings (own file system, possibly SSH), and after deliberate faults (service stopped, socket permissions changed, configuration key deleted) a ✗ each with a fitting remedy.
3. `update` to a newer version keeps users, servers, worlds and backups; running Minecraft servers keep running. An update with a broken binary rolls back on its own.
4. The firewall allows only SSH, 80, 443 and 25565; the running SSH session does not drop when it is activated.
5. `uninstall` removes services, containers, the Caddy site and the firewall tables and leaves data and configuration in place; a new installation takes them over. `--purge` removes everything after confirmation.
6. `make deploy-testvm` runs through the installer, and the test VM works as before.
