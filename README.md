# mc-panel

A self-hosted web panel for your own Minecraft servers on a Linux VPS: a Go backend with an embedded React UI, shipped as static binaries for `linux/amd64` and `linux/arm64` and set up by a single installer. It is a hobby and learning project for a circle of friends (one VPS, a few servers, no multi-tenancy). Because the panel faces the internet, security comes first.

## Features

- **Minecraft servers** as hardened Docker containers (`itzg/minecraft-server`): Vanilla, Paper, Purpur, Fabric, NeoForge, and Modrinth and CurseForge modpacks. Version, Java, memory (capped to what the host can hold, with a recommendation per server type) and an allowlist of `server.properties` are configurable; start, stop and restart with automatic crash restarts. Every server is reachable by hostname (`<server>.<base domain>`) on the shared port 25565 (mc-router).
- **Console and players:** live log in the browser (survives restarts), commands over RCON, online players, and ops, whitelist and bans without a restart (kick, ban, pardon, op, whitelist). What is changed in the game is kept.
- **Files:** a file browser per server with an editor for configuration files, upload and download up to 4 GiB (folders as zip), create, rename, move, delete and extract zips (e.g. a world upload while the server is stopped). An unprivileged service does the work; the RCON password stays hidden.
- **Plugins and mods:** search plugins for Paper/Purpur on Modrinth and Hangar and mods for Fabric/NeoForge on Modrinth, and install them with their required dependencies in one click. See installed jars (also uploaded ones) with filters, update, disable and remove them. Import the mods of a Modrinth modpack (`.mrpack`) into an existing Fabric/NeoForge server with a preview of what is added, updated or skipped. Every jar comes from Modrinth or Hangar and is checked against the upstream checksum before it is written.
- **Backups:** back up a server's data directory on demand or on a schedule (every N hours, daily, weekly), also while it runs (saving is paused meanwhile). Tiered retention (last, daily, weekly, monthly), pinning, zip download and restoring a whole server; every restore first takes a safety snapshot. Local, deduplicated restic repositories; failed runs are reported via Discord or ntfy.
- **Monitoring and alerts:** players, TPS/MSPT, CPU and memory per server and CPU, memory, load and disk of the host, with 30 days of history. Alerts for "server down", crash loops and low disk space via Discord or ntfy, with a grace period after starts and no duplicates after restarts.
- **Sign-in and permissions:** password plus a second factor (TOTP or passkey, mandatory for admins and for every user with console, files, plugins or backups permission on a server), recovery codes, sessions with idle and absolute timeouts, sudo mode for critical actions. Admins and users with per-server permissions (`server.view`, `server.power`, `server.console`, `server.files`, `server.plugins`, `server.backups`).
- **Defense:** rate limits, jails after failed logins (in the app and in nftables on the host), IP rules, a country filter (GeoIP), a Security Center in the UI, and notifications via Discord and ntfy.
- **Audit log:** append-only and hash-chained, verifiable with `panel audit verify`.
- **Installer:** one command installs Docker, Caddy with Let's Encrypt, a host firewall, automatic security updates and the services; `update` rolls back automatically if the new version does not come up healthy, and `doctor` checks the whole setup.

## Security model

mc-panel consists of three binaries separated by privilege:

| Binary | Runs as | Job |
|---|---|---|
| `panel` | user `mcpanel` (unprivileged) | web UI and `/api`, SQLite database, downloads from Modrinth/Hangar, CLI |
| `agent` | root | a narrow JSON API on a Unix socket, for the panel only: nftables jails and Docker (Minecraft containers, router) |
| `files` | user `mcpanel-mc` (like the Minecraft containers; no capabilities, no network) | file browser and backups inside a server's data directory, for the panel only |

The goal: a compromised panel does not get root on the host, and file operations on data a Minecraft server controls never run as root. The agent validates every request strictly and accepts connections only from the panel's user. All three run under systemd sandboxing (the agent keeps only the capabilities it needs and opens no IP sockets). In production Caddy, from its official repository with automatic updates, terminates TLS in front of the panel, which listens on a Unix socket. Details are in the [foundation spec](docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md) and in [docs/install.md](docs/install.md).

What mc-panel trusts, and what it does not protect against:

- **Plugins, mods and modpacks are code.** A user with `server.files` or `server.plugins` on a server can run any code in its container. Jars come only from Modrinth and Hangar and are checked against the checksum their API reports. That protects against a manipulated download, not against a compromised project or author account.
- **Downloads at run time.** The Minecraft image is pinned by digest, but inside the container it downloads server jars, loaders and modpacks from their upstreams at start (Mojang, PaperMC, Fabric, NeoForge, Modrinth, CurseForge). The panel cannot verify those.
- **Checksums while a server runs.** A plugin or mod jar is checked against the upstream checksum before it is moved into place. The temporary file sits in a folder the server's container can write to, so a running server could change it in between: "verified" is no integrity guarantee against the server itself.
- **Browser:** a strict Content Security Policy without `'unsafe-inline'` (styles of `index.html` carry a per-request nonce; images only from the panel, the TOTP QR code is SVG) and Trusted Types in report-only mode, to be enforced later. Background polling (`GET`) sends `X-MCPanel-Background: 1` and does not keep an idle session alive.
- **Signed releases.** From v0.5.0 on, `checksums.txt` of every release is signed with the maintainer's release key, which is kept outside GitHub; `install.sh` embeds the public key and refuses an unsigned or wrongly signed release on `install` and `update`. A stolen GitHub account therefore cannot ship code to existing installations. The first `install.sh` you download is the exception: `curl … | sudo bash` trusts GitHub at that moment; [docs/install.md](docs/install.md#signed-releases) shows how to verify it before running it.
- **Backups live on the same host** under the user the containers run as; see [docs/install.md](docs/install.md#backups-of-keys-and-data) for the residual risk and off-host copies.

## Requirements

- A VPS with **Ubuntu 26.04 LTS** (`amd64` or `arm64`) and root access.
- TCP ports 80, 443 and 25565 reachable from outside.
- A DNS name for the panel (e.g. `panel.example.com`) and a wildcard name for the Minecraft servers (e.g. `*.mc.example.com`), both pointing to the VPS.

## Quickstart

```bash
curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
  | sudo bash -s -- install --domain panel.example.com --email you@example.com
```

To verify the installer before running it, follow [Signed releases](docs/install.md#signed-releases). The installer asks for the first admin's name and password and checks everything with `doctor` at the end. Then:

1. Open `https://panel.example.com/`, log in and set up the second factor.
2. Settings: enter the base domain of the Minecraft servers (`mc.example.com`).
3. Security Center: add a Discord or ntfy notification channel.
4. Add `/srv/mc-panel/backups/keys/` and `/var/lib/mc-panel/` to your own host backups.
5. Create a server; players join it at `<server>.mc.example.com`.

All installer options, updates, rollback, `doctor`, uninstalling and troubleshooting: **[docs/install.md](docs/install.md)**.

## Documentation

| Document | Content |
|---|---|
| [`docs/install.md`](docs/install.md) | installation and operation: DNS, firewall, installer options, update, rollback, `doctor`, uninstall, backups, troubleshooting |
| [`docs/dev/README.md`](docs/dev/README.md) | development: toolchain, build and dev setup, test VM, tests, CI, lint conventions, release, production details |
| [`docs/dev/acceptance.md`](docs/dev/acceptance.md) | manual release acceptance checklist |
| [`docs/superpowers/specs/`](docs/superpowers/specs/) | design specs of all sub-projects (architecture and security, instances, console, files, plugins, backups, monitoring, installer, UI) |
| [`api/openapi.yaml`](api/openapi.yaml) | API contract (source of the TypeScript types) |
| [`CLAUDE.md`](CLAUDE.md) | working rules and a short architecture overview for Claude Code |

## Releases

Each `v*` tag on `main` publishes a GitHub release with `mc-panel_<version>_linux_{amd64,arm64}.tar.gz` (`panel`, `agent`, `files` and the installer templates in `deploy/`), `install.sh`, `checksums.txt` (SHA-256 of all assets) and, from v0.5.0 on, `checksums.txt.sig` (SSH signature of `checksums.txt` by the release key). The workflow creates a draft; the maintainer signs and publishes it with `make sign-release`.
