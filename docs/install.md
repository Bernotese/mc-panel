# Installing and operating mc-panel

This guide covers a production installation on a VPS with the installer `install.sh`: requirements, DNS, firewall, all installer options, the first admin, updates and rollback, `doctor`, uninstalling, what to back up and troubleshooting. For development see [`docs/dev/README.md`](dev/README.md).

## Requirements

- A VPS (or any host) with **Ubuntu 26.04 LTS**, `amd64` or `arm64`, with systemd. The installer refuses other releases.
- Root access (`sudo`).
- TCP ports **80**, **443** and **25565** free. The installer aborts if another program listens on one of them (Caddy may already own 80/443).
- A DNS name for the panel, e.g. `panel.example.com`, and a wildcard name for the Minecraft servers, e.g. `*.mc.example.com` (see below).
- Enough memory and disk for the Minecraft servers you plan to run. Worlds and backups live under `/srv/mc-panel`.

The installer sets up everything else: Docker (`docker.io`, unless a Docker daemon already answers), Caddy (from the official Caddy repository, see [Caddy](#caddy)), nftables, restic (≥ 0.17), `tzdata`, `sqlite3`, `unattended-upgrades`, the system users `mcpanel` and `mcpanel-mc`, the three services and the host firewall.

## DNS and base domain

Create these records before you install, all pointing to the VPS:

| Record | Example | Purpose |
|---|---|---|
| `A` (and `AAAA` if the VPS has IPv6) | `panel.example.com` | the panel; Let's Encrypt needs it to resolve to this host |
| wildcard `A`/`AAAA` | `*.mc.example.com` | the Minecraft servers |

Every Minecraft server is reachable as `<server name>.<base domain>` on the shared port 25565; mc-router forwards each connection by hostname. After the installation, enter the base domain (here `mc.example.com`) in the panel under Settings. Players then connect to e.g. `survival.mc.example.com` without a port.

With `--tls letsencrypt` (the default) the installer checks that the panel name resolves to this host and asks before it continues if it does not; Let's Encrypt fails until DNS is right.

## Install

```bash
curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
  | sudo bash -s -- install --domain panel.example.com --email you@example.com
```

`install.sh` is attached to every release (`…/releases/download/<tag>/install.sh`, its SHA-256 is in `checksums.txt`). It downloads the release archive for the host architecture and verifies it against `checksums.txt` of the same release.

The steps, in order: preflight, packages (with the Caddy repository), layout (users, directories), binaries, configuration, systemd units, Caddy site, firewall, security updates, first admin, and a final `doctor` run. Each step prints `[ok]`, `[changed]`, `[skip]` or `[warn]`. A second run with the same options changes nothing. At the end the installer prints the panel URL, the admin and the next steps. The log is in `/var/log/mc-panel-install.log` (0600).

For `doctor`, `update` and `uninstall`, download the script once:

```bash
curl -fsSLO https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh
sudo bash install.sh doctor
```

### Options of `install`

| Option | Effect |
|---|---|
| `--domain FQDN` | DNS name of the panel (required with Let's Encrypt). Must match `public_url` of an existing `/etc/mc-panel/panel.toml`. |
| `--email ADDRESS` | contact address for Let's Encrypt (required with Let's Encrypt) |
| `--tls letsencrypt\|internal` | `letsencrypt` (default) or Caddy's own CA (`internal`, e.g. for a host without public DNS; browsers warn until you trust the CA, and passkeys do not work with certificate errors). With `internal` and no `--domain` the panel runs on `localhost`. |
| `--version vX.Y.Z` | install this release instead of the version of the script |
| `--from-dir DIR` | install a local build (binaries, `deploy/` and `install.sh` in `DIR`) without network downloads; excludes `--version` |
| `--ssh-port N` | SSH port to keep open; default: read from `sshd -T` and compared with the ports sshd really listens on (a mismatch aborts) |
| `--allow-port PORT[/tcp\|/udp]` | open another port in the host firewall; repeatable. Give it again on every later `install`, or the port is closed again. |
| `--no-firewall` | skip the host firewall step |
| `--admin-user NAME` | create the first admin without asking for the name |
| `--admin-password-file FILE` | password for `--admin-user`, read from a file owned by root with mode 0600 or 0400 |
| `--yes` | answer yes to confirmations (it never replaces the typed domain of `uninstall --purge`) |

`install.sh --help` prints the same list.

## Firewall

The host firewall is its own nftables table `inet mcpanel_fw` (rules in `/etc/mc-panel/mcpanel_fw.nft`, loaded by `mcpanel-fw.service`), separate from the table `inet mcpanel` the agent uses for jails. It accepts loopback, established connections, ICMP/ICMPv6, DHCPv6 replies, the SSH port(s), TCP 80, 443 and 25565 and UDP 443 (HTTP/3), plus every `--allow-port`; everything else inbound is dropped.

Before it loads the rules the installer checks that the SSH ports are right and that every running SSH session uses one of them. Other listening ports that the rules would block are listed and must be confirmed (or opened with `--allow-port`); without a terminal and without `--yes` the installer aborts. Keep a second SSH session open while you install, and have the provider's web console at hand. With `--no-firewall` you manage the firewall yourself; then open the same ports.

The agent's jails (5 failed sign-ins, IP rules; table `inet mcpanel`) drop **all** traffic from a jailed address at the host, SSH included, and they act before `mcpanel_fw`. If you share an address with an attacker (NAT, CGNAT, a VPN exit) you can lock yourself out of SSH too: keep the provider's web console at hand, then `sudo runuser -u mcpanel -- panel admin unlock -config /etc/mc-panel/panel.toml` (see [Troubleshooting](#troubleshooting)). The jails do not cover Minecraft over IPv4: Docker forwards the published port 25565 (mc-router) through its own DNAT and forward rules, past the jail and past `mcpanel_fw`'s input chain (over IPv6 it goes through docker-proxy and is jailed). The same holds for any port a container publishes, so do not publish other container ports on this host.

## Containers and their network

The Minecraft containers run with a read-only root file system, without capabilities, as `mcpanel-mc`, with memory, process and file limits. Their `/tmp` is `/srv/mc-panel/tmp/<id>` on the host (root-only `tmp/`, not backed up, emptied at every container recreate; modpacks unpack their pack there, so it needs disk space next to the data).

A plugin or mod is code, so the agent filters what containers can reach (egress filter, chain `forward` in the agent's table `inet mcpanel`): new connections from the containers' network to private, link-local and cloud-metadata addresses are dropped. The list is `container_egress_deny` in `/etc/mc-panel/agent.toml`; the default is `169.254.0.0/16`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, `fc00::/7`, `fe80::/10`, and `container_egress_deny = []` turns the filter off. Restart `mcpanel-agent` after a change. What still works: the internet (server jars, modpacks, plugins' own downloads), replies to players in a private network, and connections to the host's own addresses (those are governed by the host firewall `inet mcpanel_fw`, not by this filter). DNS goes through Docker's embedded resolver. With Ubuntu's default `/etc/resolv.conf` (the systemd-resolved stub `nameserver 127.0.0.53`) Docker asks the stub from the host's own network, so DNS works whatever resolver systemd-resolved uses upstream. If `/etc/resolv.conf` lists a resolver in a private range directly (without the stub), or `/etc/docker/daemon.json` sets `dns` to one, the containers' DNS queries are dropped: switch back to the stub, use a public resolver, or remove that range from `container_egress_deny`. `doctor` warns in that case (`docker_dns`).

**CurseForge API key.** CurseForge modpacks need the API key from the panel settings. The agent passes this one panel-wide key to every CurseForge server as `CF_API_KEY` in its environment, where every mod of that server can read it. Use a key created only for this panel, and replace it if a modpack seems untrustworthy.

Further options in `agent.toml`:

| Key | Default | Effect |
|---|---|---|
| `router_connection_rate_limit` | `50` | new connections per second through mc-router (port 25565), all clients together |
| `cpu_per_instance` | `0` (no cap) | CPUs per Minecraft server, e.g. `2` or `1.5` (at most the host's CPU count) |

After an update the egress filter and the router's new settings apply at once. An existing server gets the hardened container (read-only root, `/tmp`, file limit, CPU cap) only at its next recreate, e.g. after a change of its settings.

## Caddy

**Package and updates.** Ubuntu's `caddy` (universe) gets no security updates. The installer therefore adds Caddy's official repository (`https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main`, list `/etc/apt/sources.list.d/caddy-stable.list`, `signed-by=/usr/share/keyrings/caddy-stable-archive-keyring.gpg`). It trusts the downloaded key only if it is exactly the pinned key `65760C51EDEA2017CEA2CA15155B6D79CA56EA34` ("Caddy Web Server"), and installs Caddy from there; `update` switches an existing install. `unattended-upgrades` installs new Caddy releases daily (origin `cloudsmith/caddy/stable`). It skips a release whose default Caddyfile changed while yours differs (a conffile prompt); `doctor` (`caddy_package`) then reports an old version, and `apt-get install caddy` updates it by hand. If the new Caddy does not accept your Caddyfile or does not stay up after the switch, the installer goes back to the previous version, holds it (`apt-mark hold caddy`, so `unattended-upgrades` leaves it alone) and says so; fix the Caddyfile and run `install.sh update --force`, which tries again and releases the hold.

**Admin API.** In a Caddyfile the installer owns (it replaces only a missing or unchanged package default), Caddy's admin API is a Unix socket, `admin unix//run/caddy/admin.sock`, that only `caddy` and root can open. On `127.0.0.1:2019` every local user, the panel included, could reconfigure Caddy and read its TLS keys. The drop-in `/etc/systemd/system/caddy.service.d/mc-panel.conf` creates `/run/caddy` and `/var/log/caddy`. Your own Caddyfile stays as it is; `doctor` (`caddy_admin`) warns until you add the line to its global options and restart Caddy (`systemctl restart caddy`; a reload cannot move the admin address). `systemctl reload caddy` works with the socket as before.

**Access log.** The panel site logs every request to `/var/log/caddy/mc-panel.log` (JSON, rotated at 50 MiB, 14 old files; directory and files readable only by `caddy` and root). It contains client IP addresses, URLs, user agents and request headers; Caddy redacts cookies and `Authorization`, and the site drops the CSRF token. IP addresses are personal data: tell your users. The rotation is set in `mc-panel.caddy`, which `install` and `update` rewrite; to keep less, delete old files with a timer of your own.

**Behind a CDN or another proxy.** The panel takes the client address from `X-Forwarded-For` as Caddy passes it. Behind a CDN (or another proxy in front of Caddy) every request then seems to come from the CDN: jails, rate limits and bans hit the CDN's addresses, i.e. all users at once. Add the CDN's address ranges to Caddy (global option `servers { trusted_proxies static <ranges> }`, so Caddy keeps the client entry) **and** to `trusted_proxies` in `/etc/mc-panel/panel.toml` (so the panel skips them), then restart Caddy and `mcpanel-panel`. The host jail cannot block clients behind a CDN; only the in-app jail applies.

**Optional: hide the panel.** The login page and the app bundle are public (as for any web app), so anyone can see that an mc-panel runs at the address. To hide it, put Caddy's `basic_auth` or an IP allowlist into a file of your own in `/etc/caddy/mc-panel.d/` (e.g. `allow.caddy` with `@out not remote_ip 203.0.113.0/24` and `respond @out 403`), then `systemctl reload caddy`. The panel site imports `/etc/caddy/mc-panel.d/*.caddy`; `install` and `update` rewrite `mc-panel.caddy` but never touch that directory. Check sign-in with passkeys and the live console afterwards.

## Service sandboxing

The three services run under systemd sandboxing (`systemd-analyze security` scores about 1.2 for the panel, 2.0 for the agent, 0.6 for the files service):

- **panel** (`mcpanel`): no capabilities, writes only `/var/lib/mc-panel`, only Unix, IPv4, IPv6 and netlink sockets (netlink to list its own addresses for the notifier filter), no link-local addresses (cloud metadata), a system-call filter, `MemoryMax=1G`, `TasksMax=512`.
- **agent** (root): only `CAP_CHOWN`, `CAP_DAC_OVERRIDE`, `CAP_DAC_READ_SEARCH`, `CAP_FOWNER` and `CAP_NET_ADMIN`; writes only `/srv/mc-panel` and `/run/mc-panel`; only Unix and netlink sockets (Docker socket, nftables), no IP traffic. The Docker socket still makes it root-equivalent.
- **files** (`mcpanel-mc`) and its child restic: no capabilities, no network namespace of the host, no `/proc` beyond its own processes, writes only `/srv/mc-panel/instances` and `/srv/mc-panel/backups`.

The units in `/etc/systemd/system/mcpanel-*.service` come from the installer and `update` rewrites them; a change of your own belongs in a drop-in (`systemctl edit mcpanel-agent`), and loosening one weakens the separation of privileges. `instances_dir` must stay `/srv/mc-panel` (the units only allow writes there); mount another disk at that path instead.

## First admin

The installer asks for the admin's name and password (twice) on the terminal, also with `curl | sudo bash`. Without a terminal it skips the step with `[warn]`, and you create the admin later:

```bash
sudo runuser -u mcpanel -- /usr/local/bin/panel admin create -config /etc/mc-panel/panel.toml -username admin
```

`panel admin create` asks for the password twice without echo (or reads one line from stdin). If an admin exists, the installer does not ask again.

After the first login:

1. Set up the second factor (authenticator app or passkey; mandatory for admins) and store the recovery codes.
2. Settings: enter the base domain for the Minecraft servers.
3. Security Center → notifications: add a Discord or ntfy channel and send a test. Failed backups and monitoring alerts go there.
4. Add the files listed under [Backups of keys and data](#backups-of-keys-and-data) to your own host backups.

Always run panel CLI commands as the user `mcpanel` (`sudo runuser -u mcpanel -- panel …`), otherwise root-owned `panel.db-wal`/`-shm` files appear that the service cannot open.

## Update

```bash
sudo bash install.sh update --check   # show installed and available version
sudo bash install.sh update           # update to the latest release
```

| Option | Effect |
|---|---|
| `--version vX.Y.Z` | update to this release instead of the latest |
| `--from-dir DIR` | update to a local build |
| `--check` | only report the installed and the available version |
| `--force` | reinstall the installed version; also rolls back an interrupted update with the database copy without asking |
| `--yes` | answer yes to confirmations |

`update` hands over to the `install.sh` of the target version. It backs up the binaries, the units, the three configuration files and the database (consistent SQLite copy) to `/var/lib/mc-panel-rollback/<old version>-<time>/` (root only; the three newest are kept), swaps binaries and units, adds missing configuration keys, restarts the services and checks their health. Minecraft containers keep running; players stay connected.

Once the new version runs healthy, `update` also brings Caddy up to date: it adds the Caddy repository and installs Caddy from it (an older install still has Ubuntu's package), rewrites `/etc/caddy/mc-panel.caddy` and the drop-in `caddy.service.d/mc-panel.conf`, moves the admin API of an installer-owned Caddyfile to a socket, and writes the `unattended-upgrades` settings (see [Caddy](#caddy)). These steps are outside the rollback copy: if one fails, `update` prints a `[warn]`, the update itself stays done, and `update --force` retries it. `update` does not change the firewall; run `install` again for that.

### Rollback

If the new version does not come up healthy, `update` rolls back automatically to the old binaries, units, configuration and database and exits with code **3**. If the rollback fails too, it stops all services, prints instructions and exits with code **4**.

If an update was cut off hard (power loss, `kill -9`, dropped SSH session), the next `update` finishes it first: nothing swapped yet → aborted; target version installed and healthy → done; otherwise it rolls back with the database copy after asking (or with `--force`/`--yes`), because changes since the backup are lost. `doctor` reports such directories (`update_state`).

## doctor

```bash
sudo bash install.sh doctor          # human-readable: ✓ ok, ! warning, ✗ failure, each with a hint
sudo bash install.sh doctor --json   # {"status": "ok|warn|fail", "checks": [{"id", "status", "message", "hint"}]}
```

Exit code 0 means all ok, 1 warnings, 2 failures. Without root the checks that need root report "skipped". The checks: `services`, `sockets`, `files_process`, `versions`, `update_available`, `update_state`, `config`, `permissions`, `caddy_config`, `caddy_import`, `caddy_package`, `caddy_admin`, `certificate`, `dns`, `firewall`, `docker`, `docker_version`, `docker_live_restore`, `docker_dns`, `restic`, `tzdata`, `time_sync`, `upgrades`, `reboot_required`, `storage_fs`, `storage_free`, `backup_keys`, `ssh_password`, `ssh_root`, `ssh_kbdinteractive`.

Common warnings:

| Check | Meaning and fix |
|---|---|
| `storage_fs` | `/srv/mc-panel` shares a file system with `/var/lib/mc-panel` (database) or Docker's data root (`/var/lib/docker`). A server that fills the disk then also stops the panel or Docker. The installer does not create a file system; mount one at `/srv/mc-panel` before you install (or move the data there while everything is stopped). |
| `caddy_package` | Caddy is older than 2.10 or does not come from the Caddy repository (Ubuntu's package gets no security updates). `install.sh update --force` switches it. |
| `caddy_admin` | Caddy's admin API listens on TCP (default `127.0.0.1:2019`), so any local user, the panel included, can reconfigure Caddy and read its TLS keys. The installer moves it only in a Caddyfile it owns; for your own Caddyfile add `admin unix//run/caddy/admin.sock` to the global options and run `systemctl restart caddy`. |
| `docker_version` | Docker is older than Ubuntu 26.04's security update (29.1.3). Update `docker.io` (or `docker-ce`). |
| `docker_dns` | Container DNS would be blocked by the egress filter: `/etc/resolv.conf` names a private resolver without the systemd-resolved stub, or `/etc/docker/daemon.json` sets `dns` to one. Use the stub (`nameserver 127.0.0.53`) or adjust `container_egress_deny` (see [Containers and their network](#containers-and-their-network)). |
| `docker_live_restore` | `live-restore` is set in `/etc/docker/daemon.json` but not active, so the next Docker restart (e.g. a package update) stops every server. `systemctl reload docker` activates it; the servers keep running. |
| `reboot_required` | A kernel or library update waits for a reboot (with its age and the packages). `unattended-upgrades` never reboots on its own, because the Minecraft servers would stop; plan a reboot. |
| `update_available` | A newer release is available. |
| `ssh_password`, `ssh_root`, `ssh_kbdinteractive` | SSH allows password logins, root logins with a password, or password logins through PAM (`KbdInteractiveAuthentication yes` with `UsePAM yes`). The installer never changes SSH. |

## Uninstall

```bash
sudo bash install.sh uninstall           # asks once; --yes answers
sudo bash install.sh uninstall --purge   # deletes everything after you type the panel domain
```

`uninstall` stops and removes the units, the binaries, the Caddy site (`/etc/caddy/mc-panel.caddy` and the marked import line), the nftables tables `inet mcpanel_fw` and `inet mcpanel`, the Minecraft containers, mc-router and its network, and the sockets. It keeps worlds and backups (`/srv/mc-panel`), the database (`/var/lib/mc-panel`), the rollback copies, the configuration (`/etc/mc-panel*`) and the users, so a later `install` picks everything up again. Other Caddy sites, all packages, the Caddy repository and the drop-in `caddy.service.d/mc-panel.conf` stay (a Caddyfile of the installer still uses the admin socket in `/run/caddy`).

`--purge` also deletes all worlds, backups, backup keys, the database, the rollback copies, the configuration and the users `mcpanel` and `mcpanel-mc`. It needs the panel domain typed on the terminal; `--yes` does not replace that.

## Disk space and limits

A server must not fill the disk for everyone else. The files service keeps a reserve free on `/srv/mc-panel`: the larger of `reserve_bytes` (default 2 GiB) and `reserve_percent` (default 5 %, 0 to 50) under `[limits]` in `/etc/mc-panel-files/files.toml`. Below the reserve it refuses everything that grows data with "not enough space" (`507 insufficient_storage`): uploads, plugin installs, zip extraction, restores, new backups and editor saves that make a file larger. Reading, downloading, deleting, renaming, shrinking files and starting a server keep working, so you can always free space through the panel. A running backup checks the free space every 64 MiB and stops below half the reserve, without a snapshot. The reserve only covers what goes through the files service: a running Minecraft server writes its world itself, so give `/srv/mc-panel` its own file system (`doctor`: `storage_fs`).

Backups are limited, too:

- A backup that runs longer than `max_duration` under `[backups]` in `/etc/mc-panel/panel.toml` (default `"4h"`, allowed `"10m"` to `"7d"`) is cancelled; saving is switched on again and `backup.failed` is notified.
- A manual backup only 10 minutes after the previous one of the server ended (also a failed one; one refused before it started, e.g. for lack of space, does not count).
- Changing a server's backup schedule and pinning or unpinning a snapshot need sudo.

The 5 % can be a lot on a large disk (50 GiB of a 1 TB disk): if a fairly full disk suddenly refuses uploads, plugin installs or backups after an update, lower `reserve_percent` (or set it to 0, which leaves `reserve_bytes`). Restart `mcpanel-files` (or `mcpanel-panel`) after changing these keys.

## Backups of keys and data

The panel's backups are local restic repositories, one per server, in `/srv/mc-panel/backups/repos/<id>`, on the same disk as the worlds. They protect against mistakes, not against losing the host. They do not protect against an attacker on the host either: the Minecraft containers and the files service run as the same user `mcpanel-mc`, which owns the worlds, the repositories **and** their keys. A Minecraft server that escapes its container (a kernel or runc bug) or a compromised panel with `server.backups` can delete or encrypt every backup, and corruption is only noticed at a restore. Follow the 3-2-1 rule: three copies, on two kinds of storage, one of them off the host and out of reach of the host (pull it from a backup server, or push to append-only storage such as restic's rest-server with `--append-only` or S3 with object lock), and test a restore now and then.

Add these to your own host backups:

| Path | Why |
|---|---|
| `/srv/mc-panel/backups/keys/` | the password of each restic repository; **without it a repository cannot be read** |
| `/srv/mc-panel/backups/repos/` | the snapshots themselves (self-contained; copy them with their keys, e.g. `rsync -a /srv/mc-panel/backups/ <target>/`) |
| `/var/lib/mc-panel/panel.db` | users, permissions, server settings, audit log (copy it with `sqlite3 … ".backup …"` or while the panel is stopped) |
| `/var/lib/mc-panel/secret.key` | encrypts TOTP secrets and notification settings in the database; the database is of little use without it. Keep it **apart** from the database backups (another medium or a password manager): a stolen backup with both decrypts every TOTP secret and notifier token. |
| `/etc/mc-panel/`, `/etc/mc-panel-files/` | configuration |

How to restore a snapshot without the panel is described in [`docs/dev/README.md`](dev/README.md) ("Backups").

## Files and services

| Path | Content |
|---|---|
| `/usr/local/bin/{panel,agent,files}` | the binaries |
| `/etc/mc-panel/{panel,agent}.toml`, `/etc/mc-panel-files/files.toml` | configuration; later runs of the installer only add missing keys, your edits stay |
| `/var/lib/mc-panel/` | database, `secret.key`, optional GeoIP database |
| `/srv/mc-panel/instances/<id>` | data directory of each Minecraft server |
| `/srv/mc-panel/backups/` | restic repositories and keys |
| `/srv/mc-panel/tmp/<id>` | the `/tmp` of each Minecraft container (disposable, not backed up) |
| `/var/lib/mc-panel-rollback/` | update backups |
| `/var/log/mc-panel-install.log` | installer log |
| `/etc/caddy/mc-panel.caddy` | the panel's Caddy site (rewritten by `install` and `update`) |
| `/etc/caddy/mc-panel.d/*.caddy` | your additions to the panel site (imported inside it; never touched by the installer) |
| `/etc/systemd/system/caddy.service.d/mc-panel.conf` | drop-in for Caddy: creates `/run/caddy` (admin socket) and `/var/log/caddy` |
| `/var/log/caddy/mc-panel.log` | HTTP access log of the panel (see [Caddy](#caddy)) |
| `/etc/apt/sources.list.d/caddy-stable.list`, `/usr/share/keyrings/caddy-stable-archive-keyring.gpg` | the Caddy repository and its signing key |

Services: `mcpanel-panel.service` (+ `.socket`), `mcpanel-agent.service`, `mcpanel-files.service` (+ `.socket`), `mcpanel-fw.service`, and Caddy. Logs: `journalctl -u mcpanel-panel -u mcpanel-agent -u mcpanel-files -f`.

**Country filter:** the GeoIP country filter in the Security Center needs a DB-IP "IP to Country Lite" database in MMDB format at `/var/lib/mc-panel/dbip-country-lite.mmdb` (key `geoip_path` in `panel.toml`), readable by `mcpanel`. The installer does not download it. The panel loads it at start, so restart `mcpanel-panel` after adding it. Without it the country filter is disabled.

## Troubleshooting

- **First stop:** `sudo bash install.sh doctor`. Every failure comes with a hint.
- **The panel does not answer / certificate errors:** check DNS (`dig +short panel.example.com`), that ports 80 and 443 are reachable from outside, and `journalctl -u caddy`. The installer prints the last Caddy log lines when the panel does not answer within its wait time.
- **Health check by hand:** the panel only accepts requests with `X-Forwarded-For` on its socket: `curl --unix-socket /run/mc-panel-web/panel.sock -H 'X-Forwarded-For: 127.0.0.1' http://panel/api/health`.
- **A service fails:** `journalctl -u mcpanel-panel` (or `-agent`, `-files`). If the agent is down, the panel answers `503 agent_unavailable` for server actions; logins and in-app jails keep working.
- **Locked out** (jail, admin-login allowlist, country filter): `sudo runuser -u mcpanel -- panel admin unlock -config /etc/mc-panel/panel.toml` lifts all active jails, turns off "Admin access only from the allowlist" and clears the country filter (audited). A 2FA lockout (10 wrong codes in 15 minutes) ends after 15 minutes or with a panel restart.
- **Audit log integrity:** `sudo runuser -u mcpanel -- panel audit verify -config /etc/mc-panel/panel.toml`. To compare the database with the anchors in the root-owned journal: `sudo journalctl -u mcpanel-panel -o cat | sudo runuser -u mcpanel -- panel audit verify -config /etc/mc-panel/panel.toml -journal -` (exit 1: changed or missing entries; exit 3: entries match only a later anchor, which is expected after restoring the database; then pass `--since "<restore time>"` to `journalctl`). Entries written by the CLI, and entries older than the journal keeps, show as "without anchor". The panel itself checks the chain every 24 h, without the journal, and notifies `audit.chain_broken`.
- **Admin allowlist:** with "Admin access only from the allowlist" every admin request from another network is refused (`403`; logging out still works), and saving the setting ends admin sessions from outside it. `panel admin unlock` turns it off.
- **Update exit code 3 or 4:** see [Rollback](#rollback); the details are in `/var/log/mc-panel-install.log`.
- **A restore is stuck** ("the files service is busy for this server"): follow the runbook in [`docs/dev/README.md`](dev/README.md) ("Backups").
- **Another reverse proxy instead of Caddy:** it must set `X-Forwarded-For` without a port, must not buffer uploads, downloads or the console's Server-Sent Events, and needs a body limit of at least 4 GiB. Details in [`docs/dev/README.md`](dev/README.md) ("Production").
