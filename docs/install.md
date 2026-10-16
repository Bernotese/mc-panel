# Installing and operating mc-panel

This guide covers a production installation on a VPS with the installer `install.sh`: requirements, DNS, firewall, all installer options, the first admin, updates and rollback, `doctor`, uninstalling, what to back up and troubleshooting. For development see [`docs/dev/README.md`](dev/README.md).

## Requirements

- A VPS (or any host) with **Ubuntu 26.04 LTS**, `amd64` or `arm64`, with systemd. The installer refuses other releases.
- Root access (`sudo`).
- TCP ports **80**, **443** and **25565** free. The installer aborts if another program listens on one of them (Caddy may already own 80/443).
- A DNS name for the panel, e.g. `panel.example.com`, and a wildcard name for the Minecraft servers, e.g. `*.mc.example.com` (see below).
- Enough memory and disk for the Minecraft servers you plan to run. Worlds and backups live under `/srv/mc-panel`.

The installer sets up everything else: Docker (`docker.io`, unless a Docker daemon already answers), Caddy, nftables, restic (≥ 0.17), `tzdata`, `sqlite3`, `unattended-upgrades`, the system users `mcpanel` and `mcpanel-mc`, the three services and the host firewall.

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

The steps, in order: preflight, packages, layout (users, directories), binaries, configuration, systemd units, Caddy site, firewall, security updates, first admin, and a final `doctor` run. Each step prints `[ok]`, `[changed]`, `[skip]` or `[warn]`. A second run with the same options changes nothing. At the end the installer prints the panel URL, the admin and the next steps. The log is in `/var/log/mc-panel-install.log` (0600).

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

`update` hands over to the `install.sh` of the target version. It backs up the binaries, the units, the three configuration files and the database (consistent SQLite copy) to `/var/lib/mc-panel-rollback/<old version>-<time>/` (root only; the three newest are kept), swaps binaries and units, adds missing configuration keys, restarts the services and checks their health. Minecraft containers keep running; players stay connected. `update` does not change the Caddy site or the firewall; run `install` again for that.

### Rollback

If the new version does not come up healthy, `update` rolls back automatically to the old binaries, units, configuration and database and exits with code **3**. If the rollback fails too, it stops all services, prints instructions and exits with code **4**.

If an update was cut off hard (power loss, `kill -9`, dropped SSH session), the next `update` finishes it first: nothing swapped yet → aborted; target version installed and healthy → done; otherwise it rolls back with the database copy after asking (or with `--force`/`--yes`), because changes since the backup are lost. `doctor` reports such directories (`update_state`).

## doctor

```bash
sudo bash install.sh doctor          # human-readable: ✓ ok, ! warning, ✗ failure, each with a hint
sudo bash install.sh doctor --json   # {"status": "ok|warn|fail", "checks": [{"id", "status", "message", "hint"}]}
```

Exit code 0 means all ok, 1 warnings, 2 failures. Without root the checks that need root report "skipped". The checks: `services`, `sockets`, `files_process`, `versions`, `update_available`, `update_state`, `config`, `permissions`, `caddy_config`, `caddy_import`, `certificate`, `dns`, `firewall`, `docker`, `restic`, `tzdata`, `time_sync`, `upgrades`, `storage_fs`, `storage_free`, `backup_keys`, `ssh_password`, `ssh_root`.

Common warnings: `/srv/mc-panel` is not its own file system (a full world can then fill the disk of the panel; the installer does not create a separate file system), a newer release is available, SSH allows password or root logins (the installer never changes SSH).

## Uninstall

```bash
sudo bash install.sh uninstall           # asks once; --yes answers
sudo bash install.sh uninstall --purge   # deletes everything after you type the panel domain
```

`uninstall` stops and removes the units, the binaries, the Caddy site (`/etc/caddy/mc-panel.caddy` and the marked import line), the nftables tables `inet mcpanel_fw` and `inet mcpanel`, the Minecraft containers, mc-router and its network, and the sockets. It keeps worlds and backups (`/srv/mc-panel`), the database (`/var/lib/mc-panel`), the rollback copies, the configuration (`/etc/mc-panel*`) and the users, so a later `install` picks everything up again. Other Caddy sites and all packages stay.

`--purge` also deletes all worlds, backups, backup keys, the database, the rollback copies, the configuration and the users `mcpanel` and `mcpanel-mc`. It needs the panel domain typed on the terminal; `--yes` does not replace that.

## Backups of keys and data

The panel's backups are local restic repositories, one per server, in `/srv/mc-panel/backups/repos/<id>`, on the same disk as the worlds. They protect against mistakes, not against losing the host. Add these to your own host backups:

| Path | Why |
|---|---|
| `/srv/mc-panel/backups/keys/` | the password of each restic repository; **without it a repository cannot be read** |
| `/srv/mc-panel/backups/repos/` | the snapshots themselves (self-contained; copy them with their keys, e.g. `rsync -a /srv/mc-panel/backups/ <target>/`) |
| `/var/lib/mc-panel/panel.db` | users, permissions, server settings, audit log (copy it with `sqlite3 … ".backup …"` or while the panel is stopped) |
| `/var/lib/mc-panel/secret.key` | encrypts TOTP secrets and notification settings in the database; the database is of little use without it |
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
| `/var/lib/mc-panel-rollback/` | update backups |
| `/var/log/mc-panel-install.log` | installer log |

Services: `mcpanel-panel.service` (+ `.socket`), `mcpanel-agent.service`, `mcpanel-files.service` (+ `.socket`), `mcpanel-fw.service`, and Caddy. Logs: `journalctl -u mcpanel-panel -u mcpanel-agent -u mcpanel-files -f`.

**Country filter:** the GeoIP country filter in the Security Center needs a DB-IP "IP to Country Lite" database in MMDB format at `/var/lib/mc-panel/dbip-country-lite.mmdb` (key `geoip_path` in `panel.toml`), readable by `mcpanel`. The installer does not download it. The panel loads it at start, so restart `mcpanel-panel` after adding it. Without it the country filter is disabled.

## Troubleshooting

- **First stop:** `sudo bash install.sh doctor`. Every failure comes with a hint.
- **The panel does not answer / certificate errors:** check DNS (`dig +short panel.example.com`), that ports 80 and 443 are reachable from outside, and `journalctl -u caddy`. The installer prints the last Caddy log lines when the panel does not answer within its wait time.
- **Health check by hand:** the panel only accepts requests with `X-Forwarded-For` on its socket: `curl --unix-socket /run/mc-panel-web/panel.sock -H 'X-Forwarded-For: 127.0.0.1' http://panel/api/health`.
- **A service fails:** `journalctl -u mcpanel-panel` (or `-agent`, `-files`). If the agent is down, the panel answers `503 agent_unavailable` for server actions; logins and in-app jails keep working.
- **Locked out** (jail, admin-login allowlist, country filter): `sudo runuser -u mcpanel -- panel admin unlock -config /etc/mc-panel/panel.toml` lifts all active jails, turns off "Admin access only from the allowlist" and clears the country filter (audited). A 2FA lockout (10 wrong codes in 15 minutes) ends after 15 minutes or with a panel restart.
- **Audit log integrity:** `sudo runuser -u mcpanel -- panel audit verify -config /etc/mc-panel/panel.toml`. To compare the database with the anchors in the root-owned journal: `sudo journalctl -u mcpanel-panel -o cat | sudo runuser -u mcpanel -- panel audit verify -config /etc/mc-panel/panel.toml -journal -` (exit 1: changed or missing entries; exit 3: entries match only a later anchor, which is expected after restoring the database; then pass `--since "<restore time>"` to `journalctl`).
- **Update exit code 3 or 4:** see [Rollback](#rollback); the details are in `/var/log/mc-panel-install.log`.
- **A restore is stuck** ("the files service is busy for this server"): follow the runbook in [`docs/dev/README.md`](dev/README.md) ("Backups").
- **Another reverse proxy instead of Caddy:** it must set `X-Forwarded-For` without a port, must not buffer uploads, downloads or the console's Server-Sent Events, and needs a body limit of at least 4 GiB. Details in [`docs/dev/README.md`](dev/README.md) ("Production").
