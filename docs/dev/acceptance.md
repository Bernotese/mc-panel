# Release acceptance checklist

Manual checks before a release, on the test VM (or a real VPS for the installer section). They cover what CI cannot: a real browser, a real Minecraft client, real notifications, restarts and time. Everything else is automated (see "What CI covers" in each section and [`docs/dev/README.md`](README.md), "CI").

Run the sections a release touches; run all of them for a release that changes many areas. File findings as GitHub issues (`gh issue create`) with steps to reproduce.

## Setup

1. Deploy the release candidate: `make deploy-testvm REF=<tag or branch>` ([`docs/dev/README.md`](README.md), "Test VM"). Panel: <https://panel.192-168-1-117.sslip.io/>, base domain `mc.192-168-1-117.sslip.io`.
2. Logs alongside: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`.
3. `ssh claude@192.168.1.117 'sudo ~/mc-panel/build/testvm/install.sh doctor'` shows no `✗`.
4. Log in as `admin`. A Paper server `survival` (current version) runs; a second server (e.g. `creative`) is stopped.
5. Users: `friend` with only `server.view` on `survival`; `helper` with `server.view`, `server.power`, `server.console`, `server.files` and `server.backups` on `survival`. Both have set up TOTP.
6. Security Center → notifications: an ntfy channel (`https://ntfy.sh`, your own topic, subscribed in a browser or the app) is active and "send test" arrives.
7. Minecraft Java Edition on a machine in the same network; address `survival.mc.192-168-1-117.sslip.io`. Your player name is `<me>` below.
8. A phone or the DevTools device mode (390 px) for the mobile checks.

Most API checks below run in the browser DevTools console as `friend`, e.g. `await fetch('/api/servers/<id>/files').then(r => r.status)`; `<id>` comes from the URL.

## Instances

What CI covers: unit and integration tests, Docker integration (`dockerintegration`), the instance acceptance through the agent (`scripts/ci-instances.sh`: routing, hardening, crash restart, delete), Playwright against the mock.

- [ ] **Server types:** create and start one server each of Vanilla, Paper, Fabric, NeoForge and a small Modrinth pack. Each reaches "running" and the client can join it via `<name>.<base domain>`.
- [ ] **Permissions:** a user with only `server.power` on `survival` sees only `survival`, can start and stop it, sees no settings area; `PATCH /api/servers/<id>` answers `403`.
- [ ] **Memory limit:** the server form shows the host's RAM, allows at most (host RAM − 1 GiB) / 1.25 and recommends a size per type when creating; `PATCH /api/servers/<id>` with a larger `memory_mib` answers `400 validation_failed`; if all servers together exceed the host RAM, the form warns.
- [ ] **Change and restart:** change MOTD and memory → "restart required"; after restarting, the client shows the new MOTD, `sudo docker inspect mcpanel-mc-<id>` shows the new limit, the world is kept.
- [ ] **Host reboot:** with servers running, `sudo reboot` the VM. Servers whose desired state is "running" run again afterwards.
- [ ] **Delete and audit:** delete a throwaway server. Container and route are gone, its data is under `/srv/mc-panel/deleted/`, the audit log has the entry, and `ssh claude@192.168.1.117 "sudo runuser -u mcpanel -- panel audit verify -config /etc/mc-panel/panel.toml"` reports OK.

## Console and players

What CI covers: agent (Docker demultiplexer, log stream, exec, player files) and panel tests (SSE, reconnect, re-checks, permissions, audit), the instance acceptance (RCON, log stream with `since`, player lists after `whitelist add`/`op`/`ban`/`pardon`, `409 not_running`, crafted player files), Playwright against the mock.

- [ ] **Live log:** as `helper`, open the console of `survival`. The last log lines appear at once, new lines live (e.g. `<me> joined the game`); the status shows connected.
- [ ] **Commands:** `say hello` appears in the game chat; `list` shows a reply block with `<me>`.
- [ ] **Restart without losing the stream:** keep the console open while `admin` restarts the server. The status goes to stopped/reconnecting and back to connected (up to about 10 s), the new start lines appear, no line is duplicated, no page reload is needed.
- [ ] **RCON noise toggle:** hiding RCON messages hides the `Thread RCON Client … started/shutting down` lines; showing them brings them back.
- [ ] **Stream limit:** a fifth console tab of the same user shows "too many open consoles" (`429 rate_limited`); after closing one, it connects within about 10 s.
- [ ] **Option injection:** `--help` (any command starting with `-`) is rejected with a validation error; nothing appears in the log.
- [ ] **Only `server.view`:** `friend` sees online players with `<me>` in the overview but no console or players area; `console/stream` and `players/lists` answer `403`, `POST /api/servers/<id>/console` answers `403`.
- [ ] **Whitelist and op without restart:** enable the whitelist property (restart once), then add `<me>` in the players area → toast, entry in the list, `<me>` can join, another account cannot. Op `<me>` → `/gamemode creative` works at once.
- [ ] **Kick, ban, pardon:** kick (reason shown in the client), ban (join refused; entry with reason, by, since), pardon (join works again).
- [ ] **In-game changes stay:** `/op <other>` or `/whitelist add <name>` in the game appear in the players area and survive a restart.
- [ ] **Stopped server:** lists stay visible, change buttons and the console input are disabled with a hint.
- [ ] **Revoked access:** while `helper` has the console open, remove `server.console` (or end the session in the Security Center). The stream ends within 30 s.
- [ ] **Audit:** `server.console_command` (command text and result, not the output) and `server.player_action` (action, name, reason) entries; `panel audit verify` reports OK.
- [ ] **Mobile:** console usable, the input stays visible at the bottom, players area without horizontal scrolling.

## Files

What CI covers: service unit and Linux tests (path rules, `os.Root` limits, symlinks, FIFOs, atomic writes, upload limits, redaction, folder zip, extraction with zip slip, zip bomb and symlink entries), panel tests (permissions, CSRF, streaming without buffering, audit, extract only when stopped, power gate), the instance acceptance (service as `mcpanel-mc` without capabilities, only the panel UID and root may connect, upload, redaction, editor lock, symlinks, folder zip, world extract, malicious archives), Playwright against the mock.

- [ ] **Service:** `ssh claude@192.168.1.117 'systemctl is-active mcpanel-files.socket mcpanel-files.service; ps -o user:20=,group:20=,supgrp:30= -C files; sudo stat -c "%U:%G %a" /run/mc-panel-files/files.sock'` → `active` twice, user and group `mcpanel-mc` (no supplementary group `mcpanel`), socket `mcpanel-mc:mcpanel 660`.
- [ ] **World upload and extract:** as `helper`, upload a world zip while the server runs (progress, then listed). "Extract" is disabled while the server runs. Stop it, extract into `new-world`: progress bar, power buttons disabled, a start attempt by `admin` is refused. Swap `world` with the extracted folder (rename/move dialog), start, join: you stand in the uploaded world.
- [ ] **Editor:** edit a config file and save with Ctrl/Cmd+S; the change persists. The same file saved in two tabs shows the conflict dialog (reload or overwrite; both work).
- [ ] **`server.properties`:** shows the hint that the panel settings overwrite it on start; `rcon.password=********`; `.rcon-cli.*` files are not listed; after saving and restarting, the console (RCON) still works.
- [ ] **Binary file:** a non-text file > 5 MiB opens as a download button instead of the editor.
- [ ] **Folder download:** downloading `world` gives `world.zip` that opens and contains the world.
- [ ] **Overwrite prompt:** uploading a file that exists asks before sending (overwrite or skip).
- [ ] **Leaving during an upload:** navigating away or reloading during a large upload asks first; leaving cancels it.
- [ ] **Unsaved edits:** with unsaved changes in the editor, switching server or area asks first; cancel stays, discard leaves.
- [ ] **Only `server.view`:** `friend` has no files area; `/api/servers/<id>/files` answers `403`.
- [ ] **Audit:** `file.*` entries for upload (size), extract (target), rename (from/to), save, delete and download, by `helper`; listing and opening are not audited.
- [ ] **Mobile:** file list as cards, upload via the file picker, the editor is usable.

## Plugins and mods

What CI covers: upstream clients with real, shortened responses (Modrinth, Hangar, host allowlist incl. redirects, size and `Content-Length` checks, caches, filename fallback), resolving (depth, package limit, cycles, external, incompatible, client-only mods), the files service (`/v1/jars` with checksums, cache and metadata; symlinks, FIFOs and folders skipped; an upload with a wrong checksum leaves nothing behind), panel tests (permissions, CSRF, inventory matching, jobs, `file_exists`, updates, rate limit, icon proxy, audit), the mrpack index validation incl. path attacks, classification and import job, Vitest and Playwright against the mock (search → install with dependency → disable → update → remove; mrpack import), and the instance acceptance (`scripts/ci-instances.sh`: Chunky from Modrinth and ViaBackwards with ViaVersion from Hangar on a real Paper server, loaded after a restart, disabled not loaded, checksums, icon proxy, audit; an mrpack import on a Fabric server against the real Modrinth).

Extra setup: a Fabric server `modded` (current game version, 2 GiB, started once); `helper` also has `server.plugins` and `server.power` on `modded`. On the Mac: an older LuckPerms jar for Paper from <https://modrinth.com/plugin/luckperms/versions>, and an `.mrpack` exported from the Modrinth app with Fabric API, Lithium, Sodium, Mod Menu and one own jar that is not on Modrinth, for the game version of `modded`.

- [ ] **Outbound access:** `ssh claude@192.168.1.117 'for h in api.modrinth.com cdn.modrinth.com hangar.papermc.io hangarcdn.papermc.io; do sudo -u mcpanel curl -sS -o /dev/null -w "$h %{http_code}\n" "https://$h/"; done'` → four HTTP status codes (no `000`).
- [ ] **Plugin from Modrinth:** as `helper` on `survival`, Discover → Modrinth → "LuckPerms" → install (default: newest release). Progress per jar; LuckPerms appears under Installed (source, version, icon); the restart banner appears.
- [ ] **Plugin from Hangar with dependency:** Hangar → "ViaBackwards": the preview lists ViaVersion as installed along; both appear under Installed.
- [ ] **Discover:** every hit carries a source badge, "Results from <source>" links to the source; "Hide installed" (on by default) hides the installed hits and says how many; scrolling to the end loads more.
- [ ] **Active after restart:** restart from the banner; `plugins` in the console lists LuckPerms, ViaBackwards and ViaVersion; an older client can join (ViaBackwards).
- [ ] **External only:** a Hangar project that is only linked externally shows "Only available externally" with a link and the hint to the files area; install is disabled (if none exists any more, note "not checkable").
- [ ] **Mod with Fabric API:** on `modded`, Mods → Discover (Modrinth only) → "Chunky": Fabric API is installed along; the server starts and `chunky help` answers; client-only mods (e.g. "Sodium") do not appear in the search.
- [ ] **Manual jar is matched:** remove LuckPerms, upload the older LuckPerms jar to `plugins/` in the files area. It shows as Modrinth/LuckPerms with its version, not as manual.
- [ ] **Update:** "Check for updates" → update badge → "Update all (1)": the new version replaces the old jar (new file name); after a restart the new version runs.
- [ ] **Not matched:** an own jar with `plugin.yml` and no Modrinth entry shows as manual with name and version from `plugin.yml` and the hint that updates are not possible.
- [ ] **Installed filters:** the name filter, status (enabled/disabled) and origin filters narrow the list, the counter shows "x of y jars", "Reset filters" restores it.
- [ ] **Disable, enable, remove:** disabling ViaBackwards renames it to `….jar.disabled` and it is not loaded after a restart (ViaVersion still is); enabling brings it back; removing Chunky on `modded` deletes the jar, Fabric API stays.
- [ ] **One job per server:** starting an install on `survival` in two tabs at once: the second gets `409` and shows the running job; buttons are disabled meanwhile.
- [ ] **Upstream unreachable:** block the panel's outbound HTTPS (`sudo nft add table inet plugtest; sudo nft add chain inet plugtest out "{ type filter hook output priority 0; }"; sudo nft add rule inet plugtest out meta skuid mcpanel tcp dport 443 reject`), search a new term: Discover shows the error with "Try again", Installed stays usable; `sudo nft delete table inet plugtest` restores it.
- [ ] **Name collision:** upload another jar under the file name Chunky would get, then install Chunky: refused with `409 file_exists` (or the step fails if the file appears during the job); the existing jar is unchanged.
- [ ] **mrpack preview:** upload an older Lithium to `mods/` of `modded`, then "Import modpack (.mrpack)" with the exported pack. The diff lists Fabric API as new (or unchanged), Lithium as updated (old → new), Sodium and Mod Menu as skipped (client only), the own jar as skipped (not on Modrinth, upload it by hand), other installed mods as staying installed, and ignored files outside `mods/`. The network tab shows that only the index was sent (request < 1 MiB).
- [ ] **mrpack import:** import: progress per jar, new jars in `mods/`, the old Lithium is gone, restart banner; after the restart the server starts without mod errors. Optional entries can be deselected and are then not installed.
- [ ] **mrpack version mismatch:** a pack for another Minecraft version shows a warning with both versions. As `admin`, "switch the server version" (sudo) reloads the preview without the warning; as `helper` only "Import anyway" is offered, and without it the import is refused (`409 mrpack_version_mismatch`).
- [ ] **mrpack wrong loader:** a NeoForge pack on `modded` answers `409 mrpack_incompatible` with a clear message; a Paper server has no import button.
- [ ] **Only `server.view`:** `friend` has no plugins area; `/api/servers/<id>/plugins` answers `403`.
- [ ] **Audit:** one `plugin.install` per jar (also the dependencies), `plugin.update` (from/to), `plugin.toggle` (enabled), `plugin.remove`; mrpack imports add `via: mrpack` per jar and one `plugin.mrpack_import` with pack name, version and counts; search, inventory, update checks and icons are not audited.
- [ ] **Mobile:** Installed as cards, search, install dialog, mrpack dialog and job progress usable.

## Backups

What CI covers: files service (tar over `os.Root` with symlinks, special files, hidden entries, redaction and a symlink swap during the walk; tar extraction with the zip rules; top-level swap; cancel; tar → zip; one job per instance; real restic: init, backup, list, pin, forget with policy and time zone, dump, restore, purge), panel (schedule math with a fake clock incl. DST and missed runs, queue, `save-on` after every failure and after a crash, restore gate against power and reconcile, permissions incl. sudo, audit, throttled `backup.failed`, `purge_backups`), Playwright against the mock, and the instance acceptance on a real Paper server (backup with `save-off`/`save-on` in the log, restore with sudo, zip download, a schedule firing in the panel's time zone, pin, delete, purge, audit).

- [ ] **Host:** `ssh claude@192.168.1.117 'restic version; sudo stat -c "%U:%G %a" /srv/mc-panel/backups; systemctl show mcpanel-files -p ReadWritePaths -p MemoryMax -p TasksMax -p RestrictAddressFamilies; dpkg-query -W -f="\${Status}\n" tzdata'` → restic ≥ 0.17, `mcpanel-mc:mcpanel-mc 700`, both paths in `ReadWritePaths`, `MemoryMax=1073741824`, `TasksMax=512`, `RestrictAddressFamilies=AF_UNIX`, `install ok installed`.
- [ ] **Manual backup while running:** build a wooden tower in the game. As `helper`, create a backup with the note "wood". The job shows its steps; the console shows saving disabled, saved and enabled again; the snapshot is listed as manual with note, time and size; players stay connected.
- [ ] **Schedule:** daily at now + 3 min, keep last 2, other tiers 0. The next run shows the time and the panel's time zone; the run starts on time (± 1 min) and the last scheduled run reads successful.
- [ ] **Retention and pin:** pin the first scheduled snapshot, then let three more scheduled runs happen. The pinned plus the two newest scheduled snapshots remain; manual and pre-restore snapshots are untouched.
- [ ] **Time zone:** switching the backup time zone (Settings) to `UTC` shifts the next run by the offset within a minute; switch back.
- [ ] **Restore:** replace the tower with stone. Restore "wood" (type the server name, sudo). The job stops the server, takes a pre-restore snapshot, restores and starts it again; start is blocked meanwhile; in the game the wooden tower is back. Restoring the pre-restore snapshot brings the stone back.
- [ ] **Hidden files and stopped servers:** `.rcon-cli.*` survives a restore; a stopped server stays stopped after a restore.
- [ ] **Download:** "download as zip" gives `survival-<YYYYMMDD-HHMMSS>.zip` with `world/`, `plugins/` and `server.properties` with `rcon.password=********`, without `.rcon-cli.*`.
- [ ] **Symlinks:** `sudo -u mcpanel-mc ln -s ../<other id> /srv/mc-panel/instances/<id>/foreign`; back up, download, restore. The zip has no `foreign` entry, after the restore `foreign` is gone, the other server's data is unchanged.
- [ ] **Failure notification:** move `restic` away and let a scheduled run fail: the status shows the error without a host path, ntfy gets "Backup failed: survival"; a second failure sends nothing (one per 6 h); move restic back, the next run succeeds.
- [ ] **Without restic:** with restic moved away and `mcpanel-files` restarted, the backups area shows only the "not available" hint; the file browser still works.
- [ ] **Panel crash during a backup:** create a 3 GiB file in the data directory to slow the backup, start one, `sudo systemctl kill -s KILL mcpanel-panel` during "backing up data". After systemd restarts the panel, `save-on` in the console answers "Saving is already turned on"; no job is left; delete the big file afterwards.
- [ ] **Concurrency:** a second backup or restore while one runs is refused (`409 backup_running`); extracting a zip during a backup is refused (`429 busy`).
- [ ] **Disk full:** fill the disk to ~1 GiB free (`fallocate`) and restore a snapshot > 1 GiB: "not enough space on the host", data unchanged, the server starts again; after removing the fill file the restore works.
- [ ] **Delete with and without backups:** deleting a throwaway server with "also delete backups" removes `backups/repos/<id>` and `backups/keys/<id>` (audit `server.delete` with `backups_purged: true`); without it, both stay and the dialog says so.
- [ ] **Only `server.view`:** `friend` has no backups area; `/api/servers/<id>/backups` answers `403`.
- [ ] **Audit:** `backup.create` (note), `backup.restore`, `backup.download`, `backup.pin`, `backup.schedule_update`, `backup.delete`; scheduled runs are not audited; the time zone change is in `server.settings_update`.
- [ ] **Mobile:** snapshot list as cards, schedule form and restore dialog usable.

## Monitoring and alerts

What CI covers: Server List Ping against a fake server (valid, oversized, broken VarInts, wrong types, slow, silent, too many or invalid names), TPS detection and parsers, host readers with crafted `/proc` files, the collector with a fake clock, rollups and retention, alerts (grace period, data gate, toggle, hysteresis, 24 h reminder, panel restart with a firing alert, deleted/stopped server, crash counter reset), permissions, at most 721 points per metric, audit, Vitest and Playwright (`web/e2e/monitoring.spec.ts`), and the instance acceptance on a real Paper server (live values, history, host values, RCON noise, `server.down`/`server.recovered` via an nftables rule, `server.crash_loop` without `server.down`, `host.disk_low`/`host.disk_ok`, the toggle, delivery to the dispatcher).

Helper for "unreachable" (drops only the panel's pings to mc-router; the container keeps running, players stay connected; never use the agent's table `inet mcpanel`):

```bash
ssh claude@192.168.1.117 'sudo nft add table inet mcpanel_test && sudo nft add chain inet mcpanel_test output "{ type filter hook output priority 0; policy accept; }" && sudo nft add rule inet mcpanel_test output meta skuid mcpanel tcp dport 25565 drop'
ssh claude@192.168.1.117 'sudo nft delete table inet mcpanel_test'   # reachable again
```

- [ ] **Config:** `/etc/mc-panel/panel.toml` has `monitoring_ping_addr = "127.0.0.1:25565"` and `instances_dir = "/srv/mc-panel"`; no `monitoring:` errors in the panel log.
- [ ] **Dashboard (admin):** host slots (CPU, RAM, load, disk); per server players `x/max`, TPS, RAM against the limit, CPU, ping bars; the stopped server shows "–", never 0. Joining with the client shows `1/20` within 30 s.
- [ ] **Monitoring area:** live header refreshes every 5 s (TPS, MSPT, CPU, RAM, TPS source); charts for players, TPS/MSPT, CPU and memory with the limit line over 1 h, 24 h, 7 d and 30 d; gaps instead of zeros while stopped; tooltip by mouse and keyboard; "show as table".
- [ ] **History fills:** after a day, 24 h covers the whole day; 7 d and 30 d show hourly points.
- [ ] **RCON noise:** with no monitoring area open, at most 10 `RCON Client` lines in 5 min (`sudo docker logs --since 5m mcpanel-mc-<id> 2>&1 | grep -c "RCON Client"`); more while the area is open, back to two per minute 30 s after closing it.
- [ ] **Server down:** set "server down after" to 1 min (sudo) and apply the helper. Within 1–2 min ntfy gets "Server down: survival"; dashboard badge and active alert; the container still runs. Restarting the panel meanwhile sends no second message. Removing the rule sends "Server back online: survival" within 30 s.
- [ ] **Crash loop:** `sudo docker exec mcpanel-mc-<id> rcon-cli stop` three times (wait for each restart). "Crash loop: survival", no "Server down"; 10 min after the last restart "Server stable again".
- [ ] **Toggle and gates:** with down alerts off for the server, neither down nor crash-loop messages arrive. A stopped server and a server being restored raise no down alert.
- [ ] **Disk low:** set the GiB threshold above the free space: "Low disk space on the host" (no host path), host slot in warning colour; reset: "Disk space OK again".
- [ ] **Only `server.view`:** `friend` sees no host values, no down-alert toggle and no monitoring settings; `/api/monitoring/host?range=1h` answers `403`, `/api/monitoring/overview` lists only `survival` with `host: null`, metrics of `creative` answer `403`.
- [ ] **Settings and audit:** invalid values (e.g. 0 or 51 %) are rejected with the allowed range; valid ones need sudo; audit `monitoring.settings_update` (from/to) and `server.alerts_update`.
- [ ] **Without a notification channel:** the monitoring settings warn that alerts are detected but not sent.
- [ ] **Deleted server:** its rows are gone (`sudo sqlite3 /var/lib/mc-panel/panel.db 'SELECT count(*) FROM metrics_raw WHERE series = <id>'` → `0`).
- [ ] **DB size:** after a few days, about 23 000 raw rows per server and day that disappear after 24 h; 10 servers over 30 days stay well below 50 MB.
- [ ] **Mobile:** host and server values readable, charts full width, tooltip by tap.

## Installer

What CI covers (job `installer`, `scripts/ci-installer.sh`): install with `tls internal` and the firewall, `doctor` without failures, second run without `[changed]`, login through Caddy, update with backup, update to a broken panel with automatic rollback (exit code 3, same DB), uninstall (only mc-panel objects removed, data and configuration kept), reinstall over the data, `uninstall --purge` only with the typed domain. Bash tests (`scripts/install/test.sh`, job `installer-lint`). Every `make deploy-testvm` runs through the installer.

Manual, on a fresh **real VPS** with Ubuntu 26.04, a public domain (`panel.<domain>` and `*.mc.<domain>`), two published releases (an older one to install, a newer one to update to), and a second SSH session as a fallback during the firewall steps. Details of every command: [`docs/install.md`](../install.md).

- [ ] **One-liner:** `curl -fsSL …/releases/download/<older>/install.sh | sudo bash -s -- install --domain panel.<domain> --email <mail>`; enter admin name and password. Ends with "mc-panel is installed: https://panel.<domain>/"; the panel has a valid Let's Encrypt certificate; login and 2FA work.
- [ ] **Idempotent:** the same one-liner again prints no `[changed]` and no admin prompt.
- [ ] **`doctor`:** only expected warnings (own file system, newer release, SSH password/root login, clock); exit code 1 with warnings, 0 without; `--json` matches.
- [ ] **`doctor` new checks:** `touch /run/reboot-required` gives `! a reboot is pending since …` (remove it again); with `KbdInteractiveAuthentication yes` in sshd (and `UsePAM yes`) `ssh_kbdinteractive` warns; on a host where `live-restore` was added while containers ran, `docker_live_restore` warns until Docker is restarted; with `/srv/mc-panel` on the root file system `storage_fs` names `/var/lib/mc-panel` and `/var/lib/docker`.
- [ ] **`doctor` finds faults:** stop `mcpanel-files.socket` and `.service`; `chmod 0666` the panel socket; delete `instances_dir` from `panel.toml`. Each gives `✗` with a fitting hint and exit code 2; re-running `install` repairs it.
- [ ] **Update:** with a Paper server running, a player connected and a backup made: `update --check` reports old and new version; `update` hands over, backs up to `/var/lib/mc-panel-rollback/<old>-<time>` (root, 0700, `phase=verified`) and ends with `updated <old> -> <new>`; the player stays connected; users, servers, world and backup unchanged.
- [ ] **Rollback:** the CI job "Installer lifecycle (root)" is the evidence (exit code 3, old version runs, DB unchanged).
- [ ] **Caddy switched by the update:** after an update from a release before the Caddy repository, `apt-cache policy caddy` shows the installed version from `dl.cloudsmith.io/public/caddy/stable`, `caddy version` ≥ 2.10, the Caddyfile (if the installer owns it) has `admin unix//run/caddy/admin.sock`, the panel and other Caddy sites answer. `sudo unattended-upgrade --dry-run --debug` lists `origin=cloudsmith/caddy/stable,archive=any-version` among the allowed origins.
- [ ] **Caddy admin API:** `sudo -u mcpanel curl -m2 http://127.0.0.1:2019/config/` fails; `/run/caddy/admin.sock` is `caddy:caddy` mode 0200; `systemctl reload caddy` works.
- [ ] **Access log:** after a sign-in, `/var/log/caddy/mc-panel.log` (only `caddy` and root can read it) has the request with the client IP; cookies are `REDACTED`, no `X-Csrf-Token`.
- [ ] **Sandboxing:** `systemd-analyze security mcpanel-panel.service` (and `-agent`, `-files`) is below 4, 6 and 3; with the sandbox a server starts, the console streams, a plugin installs, and backup, restore and download work; `journalctl -u 'mcpanel-*'` shows no `operation not permitted` or `read-only file system`.
- [ ] **Firewall from outside:** `nmap -Pn -p 22,80,443,25565,8080,3306 <vps>` and `nmap -Pn -sU -p 443 <vps>`: only the SSH port, 80, 443, 25565/tcp and 443/udp are open; the SSH session survived the install.
- [ ] **Other SSH port:** with `Port 2222` in sshd, the firewall allows 2222 and the session stays; `--allow-port 8443` adds a rule `tcp dport { 8443 } accept # --allow-port`, a later `install` without it removes it.
- [ ] **Uninstall:** services, units, Caddy site and import line, firewall tables and the Minecraft containers are gone; `/srv/mc-panel`, `/var/lib/mc-panel`, `/var/lib/mc-panel-rollback`, `/etc/mc-panel*` and the users stay; packages stay.
- [ ] **Reinstall:** the one-liner again: no admin prompt, old password and 2FA work, server and world are back.
- [ ] **Purge:** `uninstall --purge` with a wrong domain removes nothing; with the right one everything incl. data and users is gone; `--yes` does not skip the prompt.
- [ ] **Test VM:** `make deploy-testvm` twice: the second run is an `update --force`; `doctor` without `✗`.

## UI

What CI covers (CI and `make test-vm`): token contrast in light and dark (`web/src/styles/tokens.test.ts`), the shared blocks (`StatSlot`, `PingBars`, `PixelIcon`, `McText`), `serverSections` for all permission combinations, redirects of disallowed areas, tree state, tablet flyout, mobile drawer, page tests and Playwright against the mock and the real backend.

- [ ] **Look:** light and dark (theme switch at the bottom of the icon rail) use the design on every page; the console is dark in both.
- [ ] **Dashboard:** multiplayer-style list with icon, copyable address, player count, ping bars, MOTD; power buttons with tooltips; stop asks first; admins see the host slots.
- [ ] **Navigation:** the server tree shows every server with its status; switching servers keeps the area (console → console); a collapsed tree stays collapsed after reload.
- [ ] **URLs:** reloading `/servers/<id>/console` stays in the console; `friend` opening `/servers/<id>/files` lands on the overview.
- [ ] **Server header:** status, address, power and slots in every area; "restart required" after a settings change.
- [ ] **Settings:** the danger zone "delete server" is at the end, with confirmation and sudo.
- [ ] **Mobile** (390 px): drawer with rail and tree, no horizontal scrolling except tables and the console, slots 2×2. **Tablet** (~900 px): icon rail only; the server icon opens the tree as a flyout.
- [ ] **Rail and layout:** on desktop the rail expands with labels and stays expanded after reload; all pages share the same content width; the server form, panel settings and profile use two columns on a wide window; stat values are readable (no pixel font).
- [ ] **Keyboard:** tab through rail, tree and pages; focus is always visible.
- [ ] **Login/2FA and sudo dialog** in the new style.
- [ ] **CSP:** no CSP errors in the browser console; fonts load from the panel itself (no foreign hosts in the network tab).
