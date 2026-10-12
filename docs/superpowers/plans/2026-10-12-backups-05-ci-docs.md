# Teilprojekt 4 – CI-Abnahme, Units, Doku, Gesamt-Review und Abschluss (Aufgaben 18–19) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backups auf einem CI-Runner gegen einen echten Paper-Server abnehmen: über die panel-API im Produktionsmodus (TOTP, Sudo, CSRF), den unprivilegierten Dateidienst mit echtem restic und RCON über den agent. Dazu die Units der Test-VM für Backups vorbereiten, die Doku nachziehen (Abnahme-Checkliste, Entwickler- und Betriebsdoku, `CLAUDE.md`, `README.md`) und Teilprojekt 4 abschließen (Gesamt-Review, Abnahme auf der Test-VM, Pläne löschen).

**Architecture:**
- Aufgabe 18 berührt nur `scripts/**` und `.github/workflows/ci.yml`.
- Ein gemeinsames Installationsskript `scripts/install-restic.sh` installiert restic 0.19.1 mit fest eingetragener SHA-256. Phase A hat es angelegt (ohne Argument, Ziel `/usr/local/bin/restic`); diese Phase ruft es zusätzlich auf. Nutzer: Go-Job und Job `instances` der CI, `scripts/vm-test.sh`, `scripts/testvm-deploy.sh`.
- `scripts/ci-instances.sh` hängt nach dem Plugin-Teil einen Abschnitt „backups“ an. Er läuft auf dem bereits laufenden Plugin-Server `ciplugins` und prüft der Reihe nach:
  - manuelles Backup mit `save-off`/`save-all flush`/`save-on` im Server-Log
  - Repo-Layout und Snapshot-Inhalt, direkt mit restic als `mcpanel-mc`
  - Restore mit Sudo, Sperre gegen Start, Pre-Restore-Snapshot, alter Inhalt, Symlink weg, verborgene Datei unverändert, Server läuft wieder
  - Zip-Download
  - Zeitplan, der in der Zeitzone des panels wirklich auslöst
  - Pin mit neuer ID
  - Löschen eines Snapshots
  - Server löschen mit `purge_backups=1`
  - Audit
- `scripts/testvm-deploy.sh` bekommt restic, `backups/`, `restic_binary` und die Unit-Änderungen aus Spec §3.2.
- Aufgabe 19 schreibt die Doku. Den Gesamt-Review und den Abschluss koordiniert der Lead.

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, immer `python3 -I`; `tarfile`, `zipfile`, `zoneinfo`), `curl` (TCP zum panel, `--unix-socket` zu agent und Dateidienst), `setpriv` (util-linux), restic 0.19.1, GitHub Actions (`ubuntu-24.04`).

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (§3.1–§3.3, §4, §5.4–§5.9, §7, §9) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-12-backups-00-overview.md` (V1, V3, V5; Abschnitte „Ausführung“ und „Global Constraints“ gelten vollständig)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | C, Teammate „ci-docs“, `feat/backups-ci-docs` (Aufgabe 18 und die Doku-Schritte von 19); Gesamt-Review, Fix-Runden und Abschluss (Rest von 19) koordiniert der Lead, Abschluss auf `docs/tp4-done` |
| Startet, wenn | Phasen A, B1, B2, B3 auf `main` gemerged |
| Dateieigentum | nur `scripts/**`, `Makefile`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md` |
| Wo getestet wird | lokal nur `make check`, `bash -n`, `shellcheck` (falls installiert) und `actionlint` per `go run`; alles mit root, Docker, Minecraft oder restic nur in der CI bzw. auf der Test-VM |
| restic | Version `0.19.1`; `restic_0.19.1_linux_amd64.bz2` SHA-256 `f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c`, `restic_0.19.1_linux_arm64.bz2` SHA-256 `a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465` (aus `SHA256SUMS` des Releases, abgerufen 2026-10-12); Ziel `/usr/local/bin/restic`; in `files.toml` von CI und Test-VM `restic_binary = "/usr/local/bin/restic"` |
| Test-Server (backups) | der Plugin-Server `ciplugins` (`$psrv`, Daten `$pdata`), läuft am Ende des Plugin-Teils; Notiz des manuellen Backups `ci before change`; Zeitzone des panels unverändert `Europe/Berlin` (Default) |
| Sudo | vor Restore, Löschen eines Snapshots und Löschen des Servers frisch per TOTP (`p_sudo`), weil das Sudo-Fenster aus dem Login nach dem Plugin-Teil abgelaufen sein kann |
| Workflow | Job `instances`: neuer Schritt „Install restic“ vor der Abnahme, `timeout-minutes` 40 → 55 (Backup, Restore mit Stopp/Start, Warten auf einen geplanten Lauf; bisher ~9 min, danach erwartet ~16 min); `changes`: `scripts/install-restic.sh` in die Filter `go` und `instances` |

---

### Task 18: CI-Abnahme mit echtem restic, Units der Test-VM

**Files:**
- Modify: `scripts/ci-instances.sh` (Kopf, `files.toml`, Layout-Prüfung, Variablen, Helfer, Abschnitte „backups …“, `PASS`-Zeile)
- Modify: `.github/workflows/ci.yml` (Filter `go`/`instances`, Go-Job falls nötig, Job `instances`)
- Modify: `scripts/testvm-deploy.sh`

**Interfaces:**
- Consumes:
  - panel-API aus V3: `getBackups` → `BackupOverview`, `createBackup` → `202 {job}`, `getBackupJob` → `{job}`, `updateBackupSchedule` → `{schedule}`, `pinBackup` → `{snapshot}`, `restoreBackup`/`deleteBackup` → `202 {job}` (Sudo), `downloadBackup` → Zip; `GET /servers/settings` mit `backup_timezone`; `DELETE /servers/{id}?purge_backups=1`.
  - Fehler `backup_running`, `validation_failed`, `conflict` (Start während Restore).
  - Dienst aus V1/V5: `GET /v1/backups/status` → `{available, version, reason}`.
  - restic-Konventionen aus Global Constraints der Übersicht (Tags `mcpanel`, `kind=…`, `note=<base64url>`, `pinned`; `--stdin-filename data.tar`, `--host mcpanel`).
  - Layout `backups/` (`mcpanel-mc 0700`), `repos/<id>`, `keys/<id>` (0600).
  - Helfer des Plugin-Teils in `ci-instances.sh`: `papi`, `is_perror`, `jget`, `jcheck`, `totp`, `inst_field`, `pwait_for`, `p_running`, `p_rcon_ready`, `p_restarted`, `logs`, `fcurl`, `no_tmp_in`; Variablen `psecret`, `psrv`, `pdata`, `p_before`.
- Produces:
  - `scripts/ci-instances.sh AGENT_BINARY FILES_BINARY PANEL_BINARY` endet mit `PASS: instance acceptance (agent, files, plugins and backups path)`.
  - Test-VM-Deploy mit restic und Backup-fähigem Dateidienst.

- [ ] **Step 1: Worktree anlegen und Vertrag prüfen**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel worktree add ../mc-panel-wt/feat-backups-ci-docs -b feat/backups-ci-docs origin/main
cd /Users/jannes/Workspace/mc-panel-wt/feat-backups-ci-docs
(cd web && npm ci)
```

Vorab prüfen, dass A/B1/B2 den Vertrag so umgesetzt haben, wie das Skript ihn nutzt. Bei Abweichungen den Lead fragen und das Skript nicht anpassen.

```bash
ls scripts/install-restic.sh 2>/dev/null; grep -n "restic" .github/workflows/ci.yml scripts/vm-test.sh
grep -n "operationId: \(getBackups\|createBackup\|getBackupJob\|updateBackupSchedule\|pinBackup\|restoreBackup\|deleteBackup\|downloadBackup\)" api/openapi.yaml
grep -n "backup_timezone\|purge_backups" api/openapi.yaml | head
grep -rn "\"server:\"\|serverTarget" internal/backups/*.go | grep -v _test | head -3
grep -rn "restic_binary\|max_backup_jobs" internal/config/files.go
grep -rn "stdin-filename\|--host\|\"kind=\"\|\"note=\"\|\"pinned\"" internal/files/*.go | grep -v _test | head
```

Expected:
- alle acht operationIds vorhanden, `backup_timezone` in `ServerSettings`, `purge_backups` an `deleteServer`
- Audit-Ziel der Backup-Einträge `server:<id>`
- `restic_binary` in der Dateidienst-Konfiguration
- Tags und `--stdin-filename data.tar` wie in den Global Constraints

`scripts/install-restic.sh` stammt aus Phase A (Step 2 prüft es nur).

- [ ] **Step 2: `scripts/install-restic.sh` (aus Phase A, nur prüfen)**

Phase A hat das Skript angelegt (Plan 01, Aufgabe 3 Step 9) und ruft es aus `scripts/vm-test.sh` und dem Go-Job der CI auf. Es nimmt **kein Argument**, installiert immer nach `/usr/local/bin/restic`, braucht root (`sudo bash scripts/install-restic.sh`) und installiert `bzip2` bei Bedarf selbst. Diese Phase ändert es nicht; sie ruft es nur zusätzlich im Job `instances` und in `scripts/testvm-deploy.sh` auf.

```bash
grep -n 'RESTIC_VERSION=\|RESTIC_SHA256_\|BIN=' scripts/install-restic.sh
grep -n 'install-restic' scripts/vm-test.sh .github/workflows/ci.yml
```

Expected: `RESTIC_VERSION="0.19.1"`, beide SHA-256 wie in den Global Constraints der Übersicht, `BIN="/usr/local/bin/restic"`; Aufrufe in `vm-test.sh` und im Go-Job. Weicht etwas ab: Lead fragen, nicht selbst ändern (Datei gehört Phase A).

- [ ] **Step 3: `scripts/ci-instances.sh` – Kopf, Konfiguration, Layout, Variablen** (Reihenfolge wie im Skript)

(1) Im Kopfkommentar den letzten Satz „… checks inventory, checksums, icon proxy, loading after a restart, disabling, removal and the audit log.“ ersetzen durch:

```bash
# ViaVersion), checks inventory, checksums, icon proxy, loading after a restart, disabling,
# removal and the audit log. On the same server it checks backups (sub-project 4, spec §9
# criteria 1-5 and 9): a manual backup of the running server with save-off/save-all
# flush/save-on, the restic repo as mcpanel-mc (tags, tar content without symlinks, hidden files
# or the RCON password), a restore with sudo (start refused meanwhile, pre-restore snapshot, data
# back, server running again), the zip download, a daily schedule firing in the panel's time
# zone, pinning, deleting a snapshot, deleting the server with purge_backups=1 and the audit log.
```

Den Satz „Changes the host … needs internet (Mojang, PaperMC, Modrinth, Hangar)“ unverändert lassen. restic kommt aus dem Workflow-Schritt, nicht aus dem Skript.

(2) Nach der Variable `p_started=""` einfügen:

```bash
# Backups part (sub-project 4), on the plugins server.
bjob=""       # id of the backup job being waited for
bjob_state="" # its last seen state
restic_bin="/usr/local/bin/restic"
```

(3) Am Anfang von „== setup: files service as mcpanel-mc“ prüfen, dass restic da ist (vor `install -m 0755 "$2" …`):

```bash
[ -x "$restic_bin" ] || fail "restic missing at $restic_bin (CI step \"Install restic\": scripts/install-restic.sh)"
```

(4) In `files.toml` vor der Zeile `[limits]` einfügen:

```toml
restic_binary = "/usr/local/bin/restic"
```

Die Limits bleiben. `max_backup_*` gelten mit ihren Defaults; `reserve_bytes = 1048576` gilt auch für Backups.

(5) Direkt nach der Prüfung von `instances/` („instances/ owner/mode …“) einfügen:

```bash
[ "$(stat -c '%U:%G %a' "$work/mc/backups")" = "mcpanel-mc:mcpanel-mc 700" ] ||
  fail "backups/ owner/mode: $(stat -c '%U:%G %a' "$work/mc/backups" 2>&1) (want mcpanel-mc:mcpanel-mc 700, agent ensureLayout)"
```

(6) Nach `echo "OK: files service up"` einfügen:

```bash
fcurl -f http://files/v1/backups/status >"$fo/bstatus.json" || fail "backups status as panel user"
python3 -I -c '
import json, sys
d = json.load(open(sys.argv[1]))
if not (d["available"] and d["version"].startswith("0.19.1") and d["reason"] == ""):
    sys.exit("backups status: %r" % d)' "$fo/bstatus.json" || fail "restic not available in the files service"
echo "OK: files service runs restic $(python3 -I -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "$fo/bstatus.json")"
```

- [ ] **Step 4: `scripts/ci-instances.sh` – Helfer** – nach der Funktion `fputsum()` (vor `check_players`) einfügen:

```bash
# p_sudo: fresh sudo by TOTP before restore and delete (the window from the sign-in may have run
# out during the plugins part). A code of an already used step is refused (replay protection),
# so on a refusal wait for the next step once. Takes over the rotated session and CSRF token.
p_sudo() {
  papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" 0)\"}"
  if [ "$pcode" != 200 ]; then
    sleep 31
    papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" 0)\"}"
  fi
  [ "$pcode" = 200 ] || fail "sudo: HTTP $pcode $(cat "$po/sudo.json")"
  papi GET /auth/me "$po/me.json"
  [ "$pcode" = 200 ] || fail "me after sudo: HTTP $pcode $(cat "$po/me.json")"
  pcsrf="$(jget "$po/me.json" 'd["csrf_token"]')"
}

# papi_dl <path below /api> <outfile>: GET of a stream (zip download) without papi's 60 s limit;
# sets pcode, headers in <outfile>.headers.
papi_dl() {
  pcode="$(curl -sS --max-time 900 -o "$2" -D "$2.headers" -w '%{http_code}' \
    -H "Origin: $porigin" -H 'Sec-Fetch-Site: same-origin' -H "Cookie: __Host-sid=$psid" \
    "$pbase/api$1")" || fail "panel GET $1: curl exit $?"
}

# b_overview: GET /servers/<psrv>/backups into $po/bover.json.
b_overview() {
  papi GET "/servers/$psrv/backups" "$po/bover.json"
  [ "$pcode" = 200 ] || fail "backup overview: HTTP $pcode $(cat "$po/bover.json")"
}

# b_start <method> <path below /servers/<psrv>/backups> <label> [json body]: starts a backup
# job (202 {job}) and sets bjob. Never call it in a subshell (pcode, psid).
b_start() {
  papi "$1" "/servers/$psrv/backups$2" "$po/bstart.json" "${@:4}"
  [ "$pcode" = 202 ] || fail "$3: HTTP $pcode $(cat "$po/bstart.json")"
  bjob="$(jget "$po/bstart.json" 'd["job"]["id"]')" || fail "$3: no job in $(cat "$po/bstart.json")"
}

# b_job_finished: polls the backup job of the plugins server; true once the job bjob is done or
# failed (bjob_state; "none"/"other" keep waiting).
b_job_finished() {
  papi GET "/servers/$psrv/backups/job" "$po/bjob.json"
  [ "$pcode" = 200 ] || fail "backup job: HTTP $pcode $(cat "$po/bjob.json")"
  bjob_state="$(jget "$po/bjob.json" '"none" if d["job"] is None else d["job"]["state"] if d["job"]["id"] == a[0] else "other"' "$bjob")"
  [ "$bjob_state" = done ] || [ "$bjob_state" = failed ]
}

# b_sched_finished: true once the current job is a scheduled one that is done or failed.
b_sched_finished() {
  papi GET "/servers/$psrv/backups/job" "$po/bjob.json"
  [ "$pcode" = 200 ] || fail "backup job: HTTP $pcode $(cat "$po/bjob.json")"
  bjob_state="$(jget "$po/bjob.json" '"none" if d["job"] is None or d["job"]["trigger"] != "scheduled" else d["job"]["state"]')"
  [ "$bjob_state" = done ] || [ "$bjob_state" = failed ]
}

# b_wait <label> <seconds>: waits for bjob and fails the run unless it ended "done".
b_wait() {
  pwait_for "$2" "backup job ($1)" b_job_finished
  [ "$bjob_state" = done ] || fail "backup job ($1) failed: $(cat "$po/bjob.json")"
}

# brestic <args…>: restic on the plugins server's repo as mcpanel-mc with the service's
# environment (spec §3.3), for checks the API does not show (tags, tar content).
brestic() {
  setpriv --reuid=mcpanel-mc --regid=mcpanel-mc --clear-groups \
    env -i HOME="$work/mc/backups" RESTIC_REPOSITORY="$work/mc/backups/repos/$psrv" \
    RESTIC_PASSWORD_FILE="$work/mc/backups/keys/$psrv" "$restic_bin" --no-cache "$@"
}

# minute_in <IANA zone> <offset>: minutes since midnight in the zone, now + offset (mod 1440).
minute_in() {
  python3 -I -c '
import sys
from datetime import datetime
from zoneinfo import ZoneInfo
n = datetime.now(ZoneInfo(sys.argv[1]))
print((n.hour * 60 + n.minute + int(sys.argv[2])) % 1440)' "$1" "$2"
}

p_gone() { [ "$(inst_field "$psrv" i)" = absent ] && ! docker inspect "mcpanel-mc-$psrv" >/dev/null 2>&1; }
```

Hinweise:
- `b_start` reicht den optionalen Body per `"${@:4}"` weiter. Ohne vierten Parameter bekommt `papi` drei Argumente und sendet keinen Body (`DELETE`, Restore).
- Alle Poller laufen über `until` in `pwait_for` im aktuellen Shell-Prozess, damit Cookie-Rotationen nicht verloren gehen.

- [ ] **Step 5: `scripts/ci-instances.sh` – Abschnitte „backups“** – vor der letzten Zeile `echo "PASS: instance acceptance (agent, files and plugins path)"` einfügen und diese Zeile durch die neue `PASS`-Zeile am Ende des Blocks ersetzen:

```bash
echo "== backups: empty overview on the plugins server"
b_overview
jcheck "$po/bover.json" "empty backup overview" '
  d["available"] and d["snapshots"] == [] and d["job"] is None
  and d["schedule"]["mode"] == "off" and d["schedule"]["timezone"] == "Europe/Berlin"
  and d["schedule"]["next_run_at"] is None'
echo "OK: backups available, no snapshots, schedule off (Europe/Berlin)"

echo "== backups: data to back up (marker, symlink out of the data dir, hidden file)"
pmarker="$pdata/world/ci-backup-marker.txt"
printf 'ci backup marker: before\n' >"$pmarker"
ln -s ../../router "$pdata/ci-backup-link" # resolves to <instances_dir>/router on the host
printf 'kept by restore\n' >"$pdata/.mcpanel-ci-keep"
chown -h mcpanel-mc:mcpanel-mc "$pmarker" "$pdata/ci-backup-link" "$pdata/.mcpanel-ci-keep"
grep -q '^rcon\.password=..*' "$pdata/server.properties" || fail "server.properties without rcon.password"
if grep -qx 'rcon\.password=\*\*\*\*\*\*\*\*' "$pdata/server.properties"; then
  fail "rcon.password is redacted on disk (the files service must only redact copies)"
fi

echo "== backups: manual backup of the running server (save-off, save-all flush, save-on)"
p_running || fail "plugins server not running before the backup"
b_start POST "" "manual backup" '{"note": "ci before change"}'
jcheck "$po/bstart.json" "manual backup job" '
  d["job"]["kind"] == "backup" and d["job"]["trigger"] == "manual" and d["job"]["state"] in ("queued", "running")'
b_since="$(jget "$po/bstart.json" 'd["job"]["queued_at"]')"
papi POST "/servers/$psrv/backups" "$po/bsecond.json" '{"note": null}'
is_perror "$po/bsecond.json" "$pcode" 409 backup_running || fail "second backup while one runs: HTTP $pcode $(cat "$po/bsecond.json")"
b_wait "manual backup" 600
jcheck "$po/bjob.json" "finished manual backup" '
  d["job"]["step"] == "done" and d["job"]["error_code"] is None and len(d["job"]["snapshot"] or "") == 64
  and d["job"]["finished_at"] >= d["job"]["started_at"] > 0'
snap_manual="$(jget "$po/bjob.json" 'd["job"]["snapshot"]')"
logs "{\"id\": $psrv, \"tail\": 2000, \"since\": $((b_since * 1000000))}" "$po/blog.ndjson" 5
python3 -I -c '
import json, sys
lines = [json.loads(x)["line"].lower() for x in open(sys.argv[1]) if x.strip()]
def first(s):
    return next((i for i, l in enumerate(lines) if s in l), -1)
off, saved, on = first("automatic saving is now disabled"), first("saved the game"), first("automatic saving is now enabled")
if not 0 <= off < saved < on:
    sys.exit("server log since the backup: save-off at %d, saved at %d, save-on at %d (want this order)\n%s"
             % (off, saved, on, "\n".join(lines[-40:])))' "$po/blog.ndjson" ||
  fail "save-off/save-all flush/save-on in the server log"
p_running || fail "the backup stopped the server"
b_overview
jcheck "$po/bover.json" "manual snapshot in the list" '
  [(s["id"], s["kind"], s["note"], s["pinned"], s["bytes"] > 0, len(s["short_id"])) for s in d["snapshots"]]
  == [(a[0], "manual", "ci before change", False, True, 8)]' "$snap_manual"
echo "OK: manual backup $snap_manual while running; save-off < saved < save-on in the log; 409 backup_running for a second one"

echo "== backups: repo layout and snapshot content (restic as mcpanel-mc)"
[ -f "$work/mc/backups/repos/$psrv/config" ] || fail "no restic repo for server $psrv"
[ "$(stat -c '%U:%G %a' "$work/mc/backups/keys/$psrv")" = "mcpanel-mc:mcpanel-mc 600" ] ||
  fail "key file: $(stat -c '%U:%G %a' "$work/mc/backups/keys/$psrv")"
brestic snapshots --json --no-lock >"$po/rsnap.json" || fail "restic snapshots"
note_tag="note=$(printf 'ci before change' | python3 -I -c 'import base64, sys; print(base64.urlsafe_b64encode(sys.stdin.buffer.read()).rstrip(b"=").decode())')"
jcheck "$po/rsnap.json" "restic tags, host and path" '
  [(s["id"], sorted(s["tags"]), s["hostname"], s["paths"] in (["/data.tar"], ["data.tar"])) for s in d]
  == [(a[0], sorted(["mcpanel", "kind=manual", a[1]]), "mcpanel", True)]' "$snap_manual" "$note_tag"
brestic dump "$snap_manual" /data.tar >"$po/snap.tar" || fail "restic dump"
python3 -I -c '
import sys, tarfile
t = tarfile.open(sys.argv[1])
members = {m.name.removeprefix("./").rstrip("/"): m for m in t.getmembers()}
special = sorted(n for n, m in members.items() if not (m.isfile() or m.isdir()))
escape = sorted(n for n in members if n.startswith("/") or n == ".." or n.startswith("../") or "/../" in n)
missing = [n for n in ("world/ci-backup-marker.txt", "server.properties") if n not in members]
hidden = sorted(n for n in members if n.split("/")[-1].lower().startswith((".mcpanel-", ".rcon-cli.")) or n == "ci-backup-link")
if special or escape or missing or hidden:
    sys.exit("snapshot tar: special %r, escaping %r, missing %r, must be absent %r" % (special, escape, missing, hidden))
pw = [l for l in t.extractfile(members["server.properties"]).read().decode().splitlines() if l.startswith("rcon.password=")]
marker = t.extractfile(members["world/ci-backup-marker.txt"]).read().decode()
if pw != ["rcon.password=********"] or marker != "ci backup marker: before\n":
    sys.exit("snapshot tar: password lines %r, marker %r" % (pw, marker))
print("snapshot tar: %d entries" % len(members))' "$po/snap.tar" || fail "snapshot content"
rm -f "$po/snap.tar"
echo "OK: repo and key (mcpanel-mc, 0600), tags mcpanel/kind=manual/note, tar without symlink, hidden files or RCON password"

echo "== backups: restore with sudo (start refused meanwhile, pre-restore snapshot, data back, server runs again)"
printf 'ci backup marker: after\n' >"$pmarker"
printf 'created after the backup\n' >"$pdata/world/ci-after-backup.txt"
chown mcpanel-mc:mcpanel-mc "$pmarker" "$pdata/world/ci-after-backup.txt"
p_before="$(inst_field "$psrv" 'i["started_at"]')"
p_sudo
b_start POST "/$snap_manual/restore" "restore"
jcheck "$po/bstart.json" "restore job" 'd["job"]["kind"] == "restore" and d["job"]["snapshot"] == a[0]' "$snap_manual"
papi POST "/servers/$psrv/power" "$po/power.json" '{"action": "start"}'
is_perror "$po/power.json" "$pcode" 409 conflict || fail "start during a restore: HTTP $pcode $(cat "$po/power.json")"
papi POST "/servers/$psrv/backups" "$po/bsecond.json" '{}'
is_perror "$po/bsecond.json" "$pcode" 409 backup_running || fail "backup during a restore: HTTP $pcode $(cat "$po/bsecond.json")"
b_wait "restore" 900
jcheck "$po/bjob.json" "finished restore" '
  d["job"]["step"] == "done" and d["job"]["error_code"] is None and d["job"]["snapshot"] == a[0]' "$snap_manual"
[ "$(cat "$pmarker")" = "ci backup marker: before" ] || fail "marker after the restore: $(cat "$pmarker")"
[ "$(stat -c '%U:%G' "$pmarker")" = "mcpanel-mc:mcpanel-mc" ] || fail "restored file owner: $(stat -c '%U:%G' "$pmarker")"
[ ! -e "$pdata/world/ci-after-backup.txt" ] || fail "a file created after the backup survived the restore"
if [ -e "$pdata/ci-backup-link" ] || [ -L "$pdata/ci-backup-link" ]; then
  fail "the symlink survived the restore (it is not in the snapshot)"
fi
[ "$(cat "$pdata/.mcpanel-ci-keep")" = "kept by restore" ] || fail "the restore changed a hidden file"
grep -q 'mappings' "$routes" || fail "router config changed (the restore reached outside the data dir)"
no_tmp_in "$pdata" || fail "temp entries left after the restore: $(find "$pdata" -maxdepth 2 -name '.mcpanel-tmp-*')"
pwait_for 300 "server running again after the restore" p_restarted
pwait_for 240 "rcon after the restore" p_rcon_ready
b_overview
jcheck "$po/bover.json" "pre-restore snapshot listed first" '
  sorted(s["kind"] for s in d["snapshots"]) == ["manual", "pre-restore"] and d["snapshots"][0]["kind"] == "pre-restore"'
snap_pre="$(jget "$po/bover.json" 'next(s["id"] for s in d["snapshots"] if s["kind"] == "pre-restore")')"
brestic dump "$snap_pre" /data.tar | tar -xO --wildcards '*world/ci-backup-marker.txt' >"$po/pre-marker.txt" ||
  fail "marker not in the pre-restore snapshot"
[ "$(cat "$po/pre-marker.txt")" = "ci backup marker: after" ] || fail "pre-restore snapshot: marker $(cat "$po/pre-marker.txt")"
echo "OK: restore with sudo: start 409 meanwhile, data back, link gone, hidden file kept, pre-restore snapshot holds the old state, server restarted"

echo "== backups: download a snapshot as zip"
papi_dl "/servers/$psrv/backups/$snap_manual/download" "$po/snap.zip"
[ "$pcode" = 200 ] || fail "download: HTTP $pcode $(head -c 500 "$po/snap.zip")"
grep -qi '^content-type: application/zip' "$po/snap.zip.headers" || fail "download headers: $(cat "$po/snap.zip.headers")"
grep -qiE '^content-disposition: attachment;.*ciplugins-[0-9]{8}-[0-9]{6}\.zip' "$po/snap.zip.headers" ||
  fail "download file name: $(grep -i '^content-disposition' "$po/snap.zip.headers")"
python3 -I -c '
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = {n.rstrip("/") for n in z.namelist()}
bad = sorted(n for n in names if n.split("/")[-1].lower().startswith((".mcpanel-", ".rcon-cli.")) or n == "ci-backup-link")
pw = [l for l in z.read("server.properties").decode().splitlines() if l.startswith("rcon.password=")]
marker = z.read("world/ci-backup-marker.txt").decode()
if bad or pw != ["rcon.password=********"] or marker != "ci backup marker: before\n" or z.testzip() is not None:
    sys.exit("download zip: must be absent %r, password lines %r, marker %r" % (bad, pw, marker))
print("download zip: %d entries" % len(names))' "$po/snap.zip" || fail "download content"
rm -f "$po/snap.zip"
echo "OK: zip with the marker, without RCON password, symlink or hidden files"

echo "== backups: schedule (validation; a daily run fires in the panel's time zone, retention runs)"
papi PUT "/servers/$psrv/backups/schedule" "$po/bsched.json" \
  '{"mode": "daily", "interval_hours": 24, "time_of_day": 240, "weekday": 0, "keep_last": 0, "keep_daily": 0, "keep_weekly": 0, "keep_monthly": 0}'
is_perror "$po/bsched.json" "$pcode" 400 validation_failed || fail "schedule without retention: HTTP $pcode $(cat "$po/bsched.json")"
papi GET /servers/settings "$po/settings.json"
[ "$pcode" = 200 ] || fail "settings: HTTP $pcode $(cat "$po/settings.json")"
ptz="$(jget "$po/settings.json" 'd["backup_timezone"]')"
tod="$(minute_in "$ptz" 2)"
now_ms="$(date +%s%3N)"
papi PUT "/servers/$psrv/backups/schedule" "$po/bsched.json" \
  "{\"mode\": \"daily\", \"interval_hours\": 24, \"time_of_day\": $tod, \"weekday\": 0, \"keep_last\": 1, \"keep_daily\": 7, \"keep_weekly\": 4, \"keep_monthly\": 6}"
[ "$pcode" = 200 ] || fail "schedule: HTTP $pcode $(cat "$po/bsched.json")"
jcheck "$po/bsched.json" "saved schedule, next run within 3 minutes" '
  d["schedule"]["mode"] == "daily" and d["schedule"]["time_of_day"] == int(a[0]) and d["schedule"]["timezone"] == a[1]
  and int(a[2]) < d["schedule"]["next_run_at"] <= int(a[2]) + 3 * 60000' "$tod" "$ptz" "$now_ms"
pwait_for 420 "scheduled backup (due at minute $tod in $ptz, ticker every minute)" b_sched_finished
[ "$bjob_state" = done ] || fail "scheduled backup failed: $(cat "$po/bjob.json")"
b_overview
jcheck "$po/bover.json" "scheduled snapshot, last run, next run tomorrow, others kept by the retention" '
  sorted(s["kind"] for s in d["snapshots"]) == ["manual", "pre-restore", "scheduled"]
  and d["schedule"]["last_status"] == "ok" and d["schedule"]["last_error"] is None
  and d["schedule"]["last_snapshot_id"] == next(s["id"] for s in d["snapshots"] if s["kind"] == "scheduled")
  and d["schedule"]["next_run_at"] > int(a[0]) + 23 * 3600000' "$now_ms"
snap_sched="$(jget "$po/bover.json" 'd["schedule"]["last_snapshot_id"]')"
echo "OK: daily run at minute $tod ($ptz) fired; forget with keep_last 1 kept the manual and pre-restore snapshots"

echo "== backups: pin (new snapshot id); manual snapshots cannot be pinned"
papi POST "/servers/$psrv/backups/$snap_manual/pin" "$po/bpin.json" '{"pinned": true}'
is_perror "$po/bpin.json" "$pcode" 400 validation_failed || fail "pin a manual snapshot: HTTP $pcode $(cat "$po/bpin.json")"
papi POST "/servers/$psrv/backups/$snap_sched/pin" "$po/bpin.json" '{"pinned": true}'
[ "$pcode" = 200 ] || fail "pin: HTTP $pcode $(cat "$po/bpin.json")"
jcheck "$po/bpin.json" "pinned snapshot with a new id" '
  d["snapshot"]["pinned"] and d["snapshot"]["kind"] == "scheduled"
  and d["snapshot"]["id"] != a[0] and len(d["snapshot"]["id"]) == 64' "$snap_sched"
snap_pinned="$(jget "$po/bpin.json" 'd["snapshot"]["id"]')"
b_overview
jcheck "$po/bover.json" "list after pinning" '
  a[0] not in [s["id"] for s in d["snapshots"]]
  and [s["pinned"] for s in d["snapshots"] if s["id"] == a[1]] == [True]' "$snap_sched" "$snap_pinned"
brestic snapshots --json --no-lock >"$po/rsnap.json" || fail "restic snapshots"
jcheck "$po/rsnap.json" "tag pinned in the repo" '
  [sorted(s["tags"]) for s in d if s["id"] == a[0]] == [sorted(["mcpanel", "kind=scheduled", "pinned"])]' "$snap_pinned"
echo "OK: pin $snap_sched -> $snap_pinned (tag pinned), manual snapshot -> 400"

echo "== backups: delete a snapshot (sudo)"
p_sudo
b_start DELETE "/$snap_manual" "delete snapshot"
jcheck "$po/bstart.json" "delete job" 'd["job"]["kind"] == "delete" and d["job"]["snapshot"] == a[0]' "$snap_manual"
b_wait "delete snapshot" 300
b_overview
jcheck "$po/bover.json" "list after deleting" '
  sorted(s["kind"] for s in d["snapshots"]) == ["pre-restore", "scheduled"] and a[0] not in [s["id"] for s in d["snapshots"]]' "$snap_manual"
echo "OK: manual snapshot deleted, pre-restore and pinned scheduled snapshot kept"

echo "== backups: delete the server with purge_backups=1 removes repo and key"
[ -d "$work/mc/backups/repos/$psrv" ] && [ -f "$work/mc/backups/keys/$psrv" ] || fail "repo or key missing before the purge"
p_sudo
papi DELETE "/servers/$psrv?purge_backups=1" "$po/sdel.json"
[ "$pcode" = 204 ] || fail "delete server: HTTP $pcode $(cat "$po/sdel.json")"
pwait_for 180 "container of the deleted server removed" p_gone
if [ -e "$work/mc/backups/repos/$psrv" ] || [ -e "$work/mc/backups/keys/$psrv" ]; then
  fail "repo or key left after purge_backups: $(ls -la "$work/mc/backups/repos" "$work/mc/backups/keys")"
fi
echo "OK: server $psrv deleted, repo and key purged"

echo "== backups: audit entries and hash chain"
papi GET "/audit?action=backup.&limit=200" "$po/baudit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/baudit.json")"
jcheck "$po/baudit.json" "backup.* audit entries (scheduled runs are not audited)" '
  sorted(e["action"] for e in d["items"] if e["result"] == "success")
  == sorted(["backup.create", "backup.restore", "backup.download", "backup.schedule_update", "backup.pin", "backup.delete"])
  and all(e["target"] == "server:" + a[0] and e["actor_name"] == "ci-admin" for e in d["items"])' "$psrv"
papi GET "/audit?action=server.delete&limit=10" "$po/sdaudit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/sdaudit.json")"
jcheck "$po/sdaudit.json" "server.delete with backups_purged" '
  [e["details"].get("backups_purged") for e in d["items"] if e["target"] == "server:" + a[0]] == [True]' "$psrv"
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" audit verify -config "$work/panel.toml" >"$po/verify.txt" 2>&1 ||
  fail "panel audit verify: $(cat "$po/verify.txt")"
echo "OK: backup.create/restore/download/schedule_update/pin/delete by ci-admin on server:$psrv, server.delete backups_purged=true; audit verify OK"

echo "PASS: instance acceptance (agent, files, plugins and backups path)"
```

Hinweise zu Step 5:
- **Reihenfolge:** Der Abschnitt läuft nach dem Plugin-Teil auf demselben Server. Dessen Plugins (ViaVersion, ViaBackwards) stören nicht: Sie liegen im Snapshot und kommen beim Restore unverändert zurück.
- **Log-Prüfung:** Die Rückmeldungen von `save-off`/`save-all flush`/`save-on` erscheinen im Server-Log als `[Rcon: Automatic saving is now disabled]`, `[Rcon: Saved the game]`, `[Rcon: Automatic saving is now enabled]`. Voraussetzung: Vanilla-Defaults `broadcast-rcon-to-ops=true` und Spielregel `logAdminCommands=true`; itzg ändert beides nicht. Vor dem ersten Push einmal auf der Test-VM nachstellen (Step 8). Erscheinen die Zeilen nicht, den Lead fragen und die Prüfung nicht abschwächen.
- **Race:** Die Prüfungen „Start während Restore“ und „zweites Backup während Backup/Restore“ laufen Millisekunden nach dem `202`. Allein der Stopp dauert Sekunden, ein Backup liest das ganze Datenverzeichnis inklusive Server-Jar und Bibliotheken. Beide Prüfungen sind deshalb nicht zeitkritisch.
- **Zeitplan:** `time_of_day = jetzt + 2 min` in der Zone des panels. `next_run_at` liegt dadurch 1–2 min in der Zukunft, auch über Mitternacht (Modulo 1440). Der Ticker läuft jede Minute, der Lauf startet also spätestens ~3 min nach dem `PUT`. Die Retention (`keep_last 1`) hat genau einen geplanten Snapshot und löscht nichts. Dass manuelle und Pre-Restore-Snapshots bleiben, ist trotzdem geprüft. Die Retention über mehrere geplante Läufe decken die Go-Integrationstests mit echtem restic ab (Spec §8) sowie die manuelle Abnahme (Zeile 2b in Aufgabe 19).
- **`brestic`:** Läuft als `mcpanel-mc` mit leerer Umgebung wie der Dienst. `dump` nimmt eine nicht-exklusive Sperre, und zu diesem Zeitpunkt läuft kein Job der Instanz.

- [ ] **Step 6: `.github/workflows/ci.yml`**

(1) `changes`, Filter erweitern:

```bash
          out go "^(cmd|internal|api)/|${gomod}|^sqlc\.yaml$|^\.golangci\.yml$|^Makefile$|^scripts/install-restic\.sh$"
          out instances "^(cmd|internal)/|^scripts/(ci-instances\.sh|mc-status\.py|install-restic\.sh)$|${gomod}"
```

(2) Job `instances`: `timeout-minutes: 40` → `timeout-minutes: 55`. Vor „Instance acceptance“ einfügen und den Kommentar über „Instance acceptance“ ergänzen:

```yaml
      # The files service runs restic for backups (sub-project 4): the pinned release.
      - name: Install restic
        run: sudo bash scripts/install-restic.sh

      # Real agent + Docker + itzg Paper server: routing by hostname, hardening, console
      # (rcon, log stream, player lists, planted player files), automatic restart after a
      # crash, removal; the files service as mcpanel-mc (peer UID, os.Root limits,
      # redaction, upload/download, extract incl. zip slip); the plugins path: the panel
      # in production mode (TOTP sign-in, sudo, CSRF) installs Chunky from Modrinth and
      # ViaBackwards with its dependency ViaVersion from Hangar (real upstreams, one retry),
      # the server loads them after a restart, a disabled jar is not loaded; and backups with
      # a real restic: manual backup with save-off/save-on, restore with sudo, zip download,
      # a schedule firing in the panel's time zone, pin, delete, purge with the server.
      - name: Instance acceptance
        run: sudo scripts/ci-instances.sh bin/agent bin/files bin/panel
```

- [ ] **Step 7: `scripts/testvm-deploy.sh`**

(1) `install_packages`: `bzip2` in die Paketliste (`bunzip2` für `install-restic.sh`):

```bash
  local pkgs=(ca-certificates curl git jq make xz-utils bzip2 docker.io caddy nftables) missing=()
```

(2) In `main()` nach `install_node "$node_arch"` die Zeile `install_restic` einfügen. Neue Funktion nach `install_node()`:

```bash
# install_restic: the pinned restic release for the files service's backups (spec 4 §7), from
# the checked-out tree (the script is read after the checkout).
install_restic() {
  log "restic"
  quiet sudo bash "$SRC/scripts/install-restic.sh"
}
```

(3) `install_system`: nach `sudo install -d -m 0750 -o root -g mcpanel-mc /srv/mc-panel/instances` einfügen:

```bash
  # Spec 4 §3.1: restic repos and their keys belong to mcpanel-mc alone (the agent enforces the
  # same owner and mode at start). It must exist before the files service starts: systemd sets
  # up ReadWritePaths only for existing paths.
  sudo install -d -m 0700 -o mcpanel-mc -g mcpanel-mc /srv/mc-panel/backups
```

`files.toml` um `restic_binary` ergänzen:

```bash
  sudo tee /etc/mc-panel-files/files.toml >/dev/null <<EOF
socket = "/run/mc-panel-files/files.sock"
panel_uid = $(id -u mcpanel)
instances_dir = "/srv/mc-panel"
restic_binary = "/usr/local/bin/restic"
EOF
```

Unit `mcpanel-files.service`: Kommentar und Grenzen ersetzen:

```bash
  # MemoryMax/TasksMax: defense in depth against crafted archives (zip parsing); 1G and 512
  # tasks leave room for restic (index and pack buffers, spec 4 §3.2). IOWeight/CPUWeight keep
  # backups behind the Minecraft servers and the panel. No network: local repos only.
```

und im `[Service]`-Block:

```ini
ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups
MemoryMax=1G
TasksMax=512
IOWeight=50
CPUWeight=50
```

(`RestrictAddressFamilies=AF_UNIX` und alles andere bleiben.)

(4) `check_files_service`: vor dem abschließenden `}` einfügen:

```bash
  if ! sudo -u mcpanel curl -fsS --max-time 10 --unix-socket /run/mc-panel-files/files.sock \
    http://files/v1/backups/status | grep -q '"available":true'; then
    echo "testvm-deploy: backups unavailable in mcpanel-files (restic?): $(sudo -u mcpanel curl -sS --unix-socket /run/mc-panel-files/files.sock http://files/v1/backups/status 2>&1)" >&2
    return 1
  fi
  if [ "$(sudo stat -c '%U:%G %a' /srv/mc-panel/backups)" != "mcpanel-mc:mcpanel-mc 700" ]; then
    echo "testvm-deploy: /srv/mc-panel/backups: $(sudo stat -c '%U:%G %a' /srv/mc-panel/backups)" >&2
    return 1
  fi
```

und den Kommentar über der Funktion um „; backups available (restic) and `/srv/mc-panel/backups` `mcpanel-mc 0700` (spec 4 §3)“ ergänzen.

(5) Kopfkommentar: „builds panel, agent and files, installs them with systemd units“ → „builds panel, agent and files, installs them (and the pinned restic) with systemd units“.

- [ ] **Step 8: Lokal prüfen, dann auf der Test-VM nachstellen**

```bash
bash -n scripts/ci-instances.sh scripts/testvm-deploy.sh scripts/install-restic.sh scripts/vm-test.sh
command -v shellcheck >/dev/null && shellcheck scripts/ci-instances.sh scripts/testvm-deploy.sh scripts/install-restic.sh scripts/vm-test.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
make check
```

Expected: keine Ausgabe von `bash -n`/`shellcheck`/`actionlint`, `make check` grün. `shellcheck` ist nur vorhanden, wenn der Nutzer es installiert hat; nicht installieren.

Auf der Test-VM (SSH nur mit abgeschalteter Sandbox; dort darf alles verändert werden) das ganze Skript einmal laufen lassen, bevor die CI es tut. Es verändert den Host und belegt Port 8080, deshalb stoppt der Befehl das deployte panel vorher und startet es danach wieder:

```bash
git push -u origin feat/backups-ci-docs
ssh claude@192.168.1.117 'set -e
  rm -rf ~/wt-backups && git clone -q --branch feat/backups-ci-docs git@github.com:Bernotese/mc-panel.git ~/wt-backups
  cd ~/wt-backups && export PATH=/usr/local/go/bin:$PATH
  go build -o bin/agent ./cmd/agent && go build -o bin/files ./cmd/files && go build -o bin/panel ./cmd/panel
  sudo bash scripts/install-restic.sh
  sudo systemctl stop mcpanel-panel mcpanel-files.service mcpanel-files.socket mcpanel-agent
  sudo bash scripts/ci-instances.sh bin/agent bin/files bin/panel; rc=$?
  sudo systemctl start mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel
  exit $rc'
```

Expected: alle `OK: … backups …`-Zeilen und `PASS: instance acceptance (agent, files, plugins and backups path)`. Die VM nutzt dieselbe Docker-Instanz wie das deployte panel. Das Skript räumt nur Container mit Label `mcpanel.instance`/`mcpanel.router` ab, deshalb danach `make deploy-testvm` (Lead) bzw. dem Lead melden, damit er neu deployt.

- [ ] **Step 9: Commit, Draft-PR, CI**

```bash
git add scripts/ci-instances.sh scripts/testvm-deploy.sh .github/workflows/ci.yml
git commit -m "ci(backups): accept backup, restore and download on a real Paper server

The instance acceptance installs the pinned restic and, on the plugins
server, takes a manual backup while it runs (save-off, save-all flush,
save-on in the log), checks the repo as mcpanel-mc (tags, tar without
symlinks, hidden files or the RCON password), restores with sudo (start
refused meanwhile, pre-restore snapshot, data back, server running again),
downloads the zip, lets a daily schedule fire in the panel's time zone,
pins, deletes a snapshot and purges the repo with the server. The test VM
deploy installs restic and gives the files service its backups directory.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr create --draft --title "ci,docs: TP4 backup acceptance with a real restic and documentation" --body "$(cat <<'EOF'
Phase C of docs/superpowers/plans/2026-10-12-backups-00-overview.md (Tasks 18–19, see docs/superpowers/plans/2026-10-12-backups-05-ci-docs.md).

- `scripts/install-restic.sh` (from phase A) now also installs restic for the instances job and the test VM deploy
- `scripts/ci-instances.sh`: manual backup of a running Paper server (save-off/save-on), repo and tar content, restore with sudo, zip download, schedule firing in the panel's time zone, pin, delete, purge with the server, audit
- `scripts/testvm-deploy.sh`: restic, `/srv/mc-panel/backups`, files unit (`ReadWritePaths`, `MemoryMax=1G`, `TasksMax=512`, `IOWeight`/`CPUWeight`)
- docs: `docs/dev/acceptance-tp4.md`, `docs/dev/README.md`, `CLAUDE.md`, `README.md`

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
EOF
)"
gh pr checks <pr> --watch --interval 30
```

Expected:
- alle Jobs grün
- im Job „Instance acceptance“ die Zeile `OK: files service runs restic 0.19.1 …`, alle `OK: … backups …`-Zeilen und `PASS: … backups path)`

Bei Fehlern `gh run view <id> --log-failed`. Ein Flake beim Warten auf den geplanten Lauf (Ticker, Uhrzeit) darf nicht durch längere Fristen kaschiert werden: Ursache auf der Test-VM nachstellen und dem Lead melden.

---

### Task 19: Doku, Gesamt-Review, Abnahme, Abschluss

**Files:**
- Create: `docs/dev/acceptance-tp4.md`
- Modify: `docs/dev/README.md`, `CLAUDE.md`, `README.md`
- Modify (Lead, nach Befund): Spec `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (Angleichung an Fixes; Status erst beim Abschluss)
- Modify (Abschluss): Roadmap in `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`, `README.md`, `CLAUDE.md`
- Delete (Abschluss): `docs/superpowers/plans/2026-10-12-backups-0*.md`

**Interfaces:**
- Consumes:
  - Spec §1–§9.
  - Das tatsächlich gemergte Verhalten. Vor dem Schreiben jede Zahl und jeden UI-Text gegen den Code auf `main` prüfen: `internal/backups`, `internal/files` (Backup-Dateien), `internal/config/files.go`, `web/src/features/backups`, Locale-Dateien unter `web/src/i18n`.
  - Aufgabe 18.
- Produces: Checkliste für die manuelle Abnahme (Kriterien 1–9 plus Review Focus), aktualisierte Entwickler- und Betriebsdoku; nach dem Merge: abgenommenes, abgeschlossenes Teilprojekt 4.

Steps 1–5 erledigt der Teammate „ci-docs“ auf `feat/backups-ci-docs`, Steps 6–11 koordiniert der Lead.

- [ ] **Step 1: `docs/dev/acceptance-tp4.md` anlegen** (vollständiger Inhalt; UI-Texte in Anführungszeichen vor dem Commit gegen `web/src/i18n/de.ts` abgleichen und bei Abweichung den Text hier anpassen):

```markdown
# Abnahme Teilprojekt 4 (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (§9).

Automatisch geprüft (CI):
- **Dateidienst:** tar über `os.Root` (Symlinks, Sonderdateien, verborgene Einträge, Schwärzung, Symlink-Tausch während des Durchlaufs, Grenzen), tar-Entpacken mit den Zip-Regeln (Pfad-Tricks, Link- und Geräte-Einträge, doppelte Namen, falsche Größen), Tausch der obersten Ebene, Abbrechen, tar→Zip, Jobs (einer pro Instanz über alle Arten) und echtes restic (init, backup, list, pin, forget mit Policy und Zeitzone, dump, restore, purge).
- **panel:** Zeitplan-Rechnung mit Fake-Clock (Sommerzeit, verpasste Termine, Zeitzonenwechsel), Warteschlange, `save-on` nach jedem Fehler und nach einem Absturz, Restore-Sperre gegen Power und Abgleich, Rechte inklusive Sudo, Audit, `backup.failed` mit Drosselung, `purge_backups`.
- **Vertrag:** OpenAPI ↔ Routen, Fehlercodes, ein Mock pro Operation.
- **Frontend:** Vitest und Playwright gegen den Mock.
- **Instanz-Abnahme** (`scripts/ci-instances.sh`) auf einem echten Paper-Server: manuelles Backup mit `save-off`/`save-on` im Log, Repo und tar-Inhalt, Restore mit Sudo, Zip-Download, ein Zeitplan, der in der Zeitzone des panels auslöst, Pin, Löschen, Server löschen mit Purge, Audit.

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client, echter Session, Benachrichtigungen und gestörtem Betrieb.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP4-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`.
2. Host prüfen: `ssh claude@192.168.1.117 '/usr/local/bin/restic version; sudo stat -c "%U:%G %a %n" /srv/mc-panel/backups; systemctl show mcpanel-files -p ReadWritePaths -p MemoryMax -p TasksMax -p IOWeight -p RestrictAddressFamilies'` → `restic 0.19.1 …`, `mcpanel-mc:mcpanel-mc 700 /srv/mc-panel/backups`, beide Pfade in `ReadWritePaths`, `MemoryMax=1073741824`, `TasksMax=512`, `IOWeight=50`, `RestrictAddressFamilies=AF_UNIX`.
3. Als `admin` anmelden. Server `survival` (Paper, aktuelle Version) läuft; im Minecraft-Client verbinden (`survival.mc.192-168-1-117.sslip.io`) und neben dem Spawn einen Turm aus **Holz** bauen.
4. Benutzer `freund` hat nur `server.view` auf `survival` (aus TP3a). Benutzer `helfer` bekommt auf `survival` zusätzlich `server.backups` und `server.power` (Benutzer → Rechte). Beim Anhaken von „Backups“ erscheint der Hinweis, dass das Recht alle Dateien lesbar macht (Download) und den Stand zurücksetzen kann.
5. Security Center → Benachrichtigungen: einen ntfy-Empfänger anlegen (z. B. `https://ntfy.sh/<zufälliges-thema>`, im Browser oder in der ntfy-App abonnieren), „Test senden“ kommt an.
6. Zeitzone: Einstellungen → „Zeitzone für Backups“ steht auf `Europe/Berlin`.

## Kriterien

| # | Kriterium (Spec §9) | Vorgehen | Erwartet |
|---|---|---|---|
| 1 | Manuelles Backup bei laufendem Server | als `helfer` Server `survival` → Tab „Backups“ → „Backup erstellen“, Notiz „Holzturm“ → bestätigen; parallel Tab „Konsole“ | Job-Anzeige zeigt die Schritte (speichern aus, sichern, speichern an); die Konsole zeigt `Automatic saving is now disabled`, `Saved the game`, `Automatic saving is now enabled`; danach steht der Snapshot als „manuell“ mit Notiz „Holzturm“, Zeit und Größe in der Liste; Spieler bleiben verbunden |
| 2a | Zeitplan feuert | Karte „Zeitplan“: Modus „täglich“, Uhrzeit = jetzt + 3 min, Aufbewahrung „letzte“ 2, täglich/wöchentlich/monatlich 0 → Speichern | „Nächster Lauf“ zeigt die Uhrzeit (Hinweis auf die Zeitzone `Europe/Berlin`); zur Uhrzeit (± 1 min) startet ein Job, danach steht ein Snapshot „geplant“ da, „Letzter Lauf“ ist erfolgreich, „Nächster Lauf“ ist morgen |
| 2b | Retention und Anpinnen | den ersten geplanten Snapshot anpinnen; danach den Zeitplan dreimal hintereinander auf jetzt + 2 min stellen und jeden Lauf abwarten | nach dem Anpinnen hat der Snapshot eine neue Kurz-ID; am Ende: der angepinnte plus die zwei neuesten geplanten Snapshots, ein älterer geplanter ist verschwunden; manuelle und „vor Restore“-Snapshots unverändert |
| 2c | Zeitzone | als `admin` Einstellungen → Zeitzone `UTC` → Speichern; Tab „Backups“ nach ≤ 1 min neu laden (das panel rechnet die Termine beim nächsten Minuten-Tick neu); danach wieder `Europe/Berlin` | „Nächster Lauf“ verschiebt sich um den Abstand zwischen UTC und Berlin (1 bzw. 2 h); der Hinweis nennt die neue Zone; nach dem Zurückstellen stimmt der Termin wieder |
| 3a | Restore | im Spiel den Holzturm durch **Stein** ersetzen; als `helfer` am Snapshot „Holzturm“ → „Wiederherstellen“ → Dialog: Servername abtippen → Sudo (Passwort bzw. TOTP) | Dialog erklärt Stopp, Sicherheits-Snapshot und Neustart; währenddessen ist „Starten“ gesperrt (Meldung, dass ein Datei- oder Backup-Vorgang läuft); Spieler werden getrennt; danach läuft der Server wieder, im Spiel steht der **Holz**turm; in der Liste steht ein neuer Snapshot „vor Restore“ |
| 3b | Restore umkehrbar | den Snapshot „vor Restore“ wiederherstellen | im Spiel steht wieder der **Stein**turm |
| 3c | Verborgenes bleibt, gestoppter Server bleibt gestoppt | `ssh claude@192.168.1.117 'sudo ls -la /srv/mc-panel/instances/<id>/ \| grep -E "\.rcon-cli\|\.mcpanel-"'` vor und nach einem Restore; Server stoppen, Restore, Status ansehen | `.rcon-cli.*` vorhanden und nicht aus dem Snapshot überschrieben; ein gestoppter Server ist nach dem Restore weiterhin gestoppt |
| 4 | Download | am Snapshot „Herunterladen“ | Datei `survival-<JJJJMMTT-HHMMSS>.zip`; öffnet im Finder; enthält `world/`, `plugins/`, `server.properties` mit `rcon.password=********`; keine Datei `.rcon-cli.*` |
| 5 | Symlinks | `ssh claude@192.168.1.117 'sudo -u mcpanel-mc ln -s ../<id-eines-anderen-servers> /srv/mc-panel/instances/<id>/fremd'`; Backup erstellen, herunterladen, dann diesen Snapshot wiederherstellen | das Zip enthält keinen Eintrag `fremd` und keine Dateien des anderen Servers; nach dem Restore ist `fremd` weg; die Daten des anderen Servers sind unverändert (`ls -la`, Zeitstempel) |
| 6 | Fehler-Benachrichtigung | `ssh claude@192.168.1.117 'sudo mv /usr/local/bin/restic /usr/local/bin/restic.off'`; Zeitplan auf jetzt + 2 min; Lauf abwarten; Zeitplan erneut auf jetzt + 2 min, Lauf abwarten; dann `sudo mv /usr/local/bin/restic.off /usr/local/bin/restic`, Zeitplan auf jetzt + 2 min | erster Lauf: „Letzter Lauf“ fehlgeschlagen mit Fehlertext ohne Host-Pfad, eine ntfy-Nachricht „Backup failed“ mit Servername; zweiter Fehlschlag: keine zweite Nachricht (6 h); dritter Lauf erfolgreich, Status wieder ok |
| 7 | Ohne restic | `ssh claude@192.168.1.117 'sudo mv /usr/local/bin/restic /usr/local/bin/restic.off && sudo systemctl restart mcpanel-files'`; Tab „Backups“ und Tab „Dateien“ öffnen; danach `sudo mv /usr/local/bin/restic.off /usr/local/bin/restic && sudo systemctl restart mcpanel-files` | Tab „Backups“ zeigt nur den Hinweis, dass Backups auf diesem Host nicht verfügbar sind (restic fehlt), ohne Aktionen; der Dateibrowser funktioniert normal; nach dem Zurückstellen ist alles wieder da |
| 8 | Absturz des panels während eines Backups | `ssh claude@192.168.1.117 'sudo -u mcpanel-mc dd if=/dev/urandom of=/srv/mc-panel/instances/<id>/gross.bin bs=1M count=3072'` (macht das Backup langsam); Backup erstellen; während „sichern“: `ssh claude@192.168.1.117 'sudo systemctl kill -s KILL mcpanel-panel'`; nach dem automatischen Neustart des panels in der Konsole `save-on` | systemd startet das panel neu; die Konsole antwortet auf `save-on` mit `Saving is already turned on` (das panel hat beim Start `save-on` gesendet); kein laufender Job mehr, „Backup erstellen“ geht wieder; danach `gross.bin` im Dateien-Tab löschen |
| 9a | Server löschen mit Backups | neuen Paper-Server `wegwerf` anlegen und starten, Backup erstellen; als `admin` → Server löschen mit „Backups ebenfalls löschen“ | `ssh claude@192.168.1.117 'sudo ls /srv/mc-panel/backups/repos /srv/mc-panel/backups/keys'` enthält die ID von `wegwerf` nicht mehr; Audit `server.delete` mit `backups_purged: true` |
| 9b | Server löschen ohne Backups | dasselbe mit `wegwerf2`, ohne die Checkbox | der Dialog weist darauf hin, dass die Backups auf dem Host bleiben; Repo und Schlüssel der ID bleiben liegen |
| 10a | nur `server.view` | als `freund` Server `survival` öffnen | kein Tab „Backups“ |
| 10b | API-Sperre | als `freund` in den DevTools: `await fetch('/api/servers/<id>/backups').then(r => r.status)` | `403` |
| 11 | Audit | als `admin` Audit-Log, Filter auf `backup.`; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | `backup.create` (mit Notiz), `backup.restore`, `backup.download`, `backup.pin`, `backup.schedule_update`, `backup.delete` mit Ziel `server:<id>` und Benutzer `helfer` bzw. `admin`; geplante Läufe erscheinen nicht; die Zeitzonen-Änderung steht in `server.settings_update`; `verify` meldet OK |
| 12a | Gleichzeitigkeit | zwei Browser-Tabs: in Tab 1 „Backup erstellen“, sofort in Tab 2 „Wiederherstellen“ bzw. „Backup erstellen“ | Tab 2 meldet, dass bereits ein Backup-Vorgang läuft (`409`); keine zweite Job-Anzeige |
| 12b | Entpacken während eines Backups | während eines Backups im Tab „Dateien“ ein Zip entpacken (Server vorher stoppen, dann Backup starten) | Meldung, dass gerade ein Vorgang läuft (`429`); nach dem Backup geht das Entpacken |
| 13 | Platte voll beim Restore | `ssh claude@192.168.1.117 'df -B1 --output=avail /srv/mc-panel'`; eine Datei anlegen, die bis auf ~1 GiB alles belegt (`sudo fallocate -l <avail-1G> /srv/mc-panel/fill`); Restore eines Snapshots > 1 GiB starten; danach `sudo rm /srv/mc-panel/fill` | Job scheitert mit „nicht genug Speicherplatz“; die Server-Daten sind unverändert, der Server startet wieder; nach dem Löschen von `fill` geht der Restore |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px): Snapshot-Liste als Karten, Aktionen erreichbar, Zeitplan-Formular bedienbar, Restore-Dialog vollständig sichtbar, kein horizontales Scrollen der Seite.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
```

- [ ] **Step 2: `docs/dev/README.md`** – sieben Stellen.

(a) Abschnitt „Toolchain“, Tabelle: nach der Zeile `golangci-lint` einfügen:

```markdown
| restic | 0.19.1 (CI, Test-VM), mindestens 0.17 | `scripts/install-restic.sh` installiert genau diese Version mit Prüfsumme (CI, `make test-vm`, `make deploy-testvm`); auf dem Mac optional für Backups im Dev-Modus (`brew install restic`, nur durch den Nutzer) |
```

(b) Abschnitt „Minecraft-Server lokal (optional)“: nach dem Spiegelstrich „Plugins und Mods …“ einfügen:

```markdown
- Backups (Tab „Backups“) brauchen ein restic im `PATH` des Dateidienstes (Dev: `restic_binary = "restic"`). Ohne restic zeigt der Tab nur den Hinweis „nicht verfügbar“, alles andere läuft normal; auf dem Mac installiert der Nutzer restic bei Bedarf selbst (`brew install restic`). Repos und Schlüssel liegen dann unter `.dev/mc/backups/` (`make clean` löscht sie mit). Ohne Docker sichert der Dateidienst das Datenverzeichnis wie ein gestoppter Server (kein RCON); ein Restore funktioniert ebenso. `npm run dev:mock` simuliert Snapshots, Jobs und Zeitpläne ohne restic.
```

und im Satz „die Checklisten stehen in …“ „… und `docs/dev/acceptance-tp3c.md` (Plugins und Mods).“ ersetzen durch „…, `docs/dev/acceptance-tp3c.md` (Plugins und Mods) und `docs/dev/acceptance-tp4.md` (Backups).“

(c) Abschnitt „Test-VM“: im ersten Spiegelstrich „installiert es Docker, Caddy, nftables, Go (…) und Node.js 24 (…)“ → „installiert es Docker, Caddy, nftables, Go (…), Node.js 24 (…) und restic (gepinnt, `/usr/local/bin/restic`)“. Nach dem Spiegelstrich „Plugins/Mods brauchen auf der VM …“ einfügen:

```markdown
- Backups: Das Deploy legt `/srv/mc-panel/backups` (`mcpanel-mc 0700`) an, setzt `restic_binary = "/usr/local/bin/restic"` in `files.toml` und gibt der Unit des Dateidienstes `ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups`, `MemoryMax=1G`, `TasksMax=512`, `IOWeight=50` und `CPUWeight=50`; danach prüft es, dass `GET /v1/backups/status` `available: true` meldet. Die Checkliste steht in `docs/dev/acceptance-tp4.md`.
```

(d) Abschnitt „Tests auf der Test-VM“: nach dem ersten Absatz (Beschreibung von `make test-vm`) einfügen:

```markdown
Die Suite `go` installiert beim ersten Lauf restic per `scripts/install-restic.sh` und setzt `MCPANEL_TEST_RESTIC=/usr/local/bin/restic`; ohne diese Variable (auf dem Mac immer) überspringen sich die Integrationstests des Dateidienstes mit echtem restic.
```

(e) Abschnitt „Produktion: Listen-Adresse und Reverse Proxy“: im Spiegelstrich „Der Dateidienst `files` (Spec 3b §3.1) …“ „`ReadWritePaths=<instances_dir>/instances`, `MemoryMax=512M` und `TasksMax=256` (Begrenzung gegen präparierte Archive)“ → „`ReadWritePaths=<instances_dir>/instances <instances_dir>/backups`, `MemoryMax=1G`, `TasksMax=512` (Begrenzung gegen präparierte Archive, Platz für restic), `IOWeight=50` und `CPUWeight=50`“ und „`deleted/` bleibt `root:root 0700`.“ → „`deleted/` bleibt `root:root 0700`, `backups/` ist `mcpanel-mc:mcpanel-mc 0700` (setzt der agent beim Start; muss vor dem Start des Dienstes existieren, sonst scheitert `ReadWritePaths`).“ Nach dem Spiegelstrich „**Plugins/Mods** …“ einfügen:

```markdown
- **Backups** (Spec 4): Der Dateidienst startet restic (mindestens 0.17, Referenz 0.19.1 über `scripts/install-restic.sh`; Pfad `restic_binary` in `files.toml`, Default `/usr/bin/restic`) als Kindprozess ohne Netz. restic öffnet nie Pfade in einem Datenverzeichnis: Backups sind ein tar-Stream, den der Dienst über `os.Root` erzeugt (ohne Symlinks, verborgene Dateien und RCON-Passwort), Restore und Download lesen `restic dump`. Ein Repo pro Server unter `<instances_dir>/backups/repos/<id>`, das Passwort in `backups/keys/<id>`. **`backups/keys/` gehört in jede Sicherung des Hosts**: Ohne Schlüssel ist ein Repo unlesbar. Offsite-Ziele gibt es noch nicht; Repos sind in sich geschlossen und lassen sich samt Schlüssel per `rsync -a <instances_dir>/backups/ <ziel>/` auf einen anderen Rechner kopieren. `backups/` liegt auf demselben Dateisystem wie `instances/`; ein Restore braucht dort freien Platz in Höhe des Snapshots plus `reserve_bytes`. Notfall-Restore ohne panel (Server vorher stoppen): `sudo -u mcpanel-mc env RESTIC_REPOSITORY=/srv/mc-panel/backups/repos/<id> RESTIC_PASSWORD_FILE=/srv/mc-panel/backups/keys/<id> restic --no-cache snapshots` und `… restic --no-cache dump <snapshot> /data.tar > /tmp/welt.tar`, dann in ein leeres Verzeichnis entpacken und prüfen. Zeitpläne laufen in der Zeitzone aus den Einstellungen (Default `Europe/Berlin`); verpasste Termine (panel aus) holt das panel einmal nach.
```

(f) Abschnitt „CI“, erste Tabelle, Zeile `instances`: „`scripts/ci-instances.sh`, `scripts/mc-status.py`“ → „`scripts/ci-instances.sh`, `scripts/mc-status.py`, `scripts/install-restic.sh`“; Zeile `go`: „`sqlc.yaml`, `.golangci.yml`, `Makefile`“ → „`sqlc.yaml`, `.golangci.yml`, `Makefile`, `scripts/install-restic.sh`“. Zweite Tabelle, Zeile `go`: vor „`make test-race`“ „restic per `scripts/install-restic.sh` (`MCPANEL_TEST_RESTIC`),“ einfügen. Zeile `instances`: „…, Deaktivieren, Entfernen, Audit; verändert den Host und braucht Internet)“ → „…, Deaktivieren, Entfernen, Audit; Backups mit echtem restic auf demselben Server: manuelles Backup mit `save-off`/`save-on` im Log, Repo und tar-Inhalt, Restore mit Sudo (Start gesperrt, Sicherheits-Snapshot, Neustart), Zip-Download, Zeitplan in der Zeitzone des panels, Pin, Löschen, Purge beim Löschen des Servers, Audit; verändert den Host und braucht Internet)“.

(g) Abschnitt „End-to-End-Tests (Playwright)“, Zeile `npm run e2e`: die Liste der Spec-Dateien um `e2e/backups.spec.ts` ergänzen (vorher mit `ls web/e2e` prüfen, wie B3 die Datei genannt hat).

- [ ] **Step 3: `CLAUDE.md`** (Englisch, knapp) – sechs Stellen.

(a) Spec-Liste: nach der 3c-Zeile einfügen:

```markdown
- `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md`: backups of the server data directories into local restic repositories through the files service (sub-project 4); manual acceptance checklist in `docs/dev/acceptance-tp4.md`.
```

(Den Roadmap-Satz „(1 foundation, 2 instances, 3a console, 3b files and 3c plugins are done)“ ändert erst der Abschluss in Step 11.)

(b) Architektur, files-Spiegelstrich: vor „The service has no network access.“ einfügen:

```markdown
It also runs backups (`/v1/backups/*`) with restic as a child process (`restic_binary`, empty environment, `--no-cache`) that never opens a path in a data directory: a backup is a tar stream the service writes from its `os.Root` walk (no symlinks or special files, hidden entries skipped, `rcon.password` redacted) into `restic backup --stdin`; restore and download read `restic dump` as a tar stream (restore: extracted with the zip rules into a temp dir, then the visible top-level entries are swapped by `rename`; download: converted to a zip on the fly). One repo per server in `<instances_dir>/backups/repos/<id>` with its key in `backups/keys/<id>` (`backups/` is `mcpanel-mc` 0700, created by the agent); snapshot kind, pin and note are restic tags. One job per instance across extract/backup/restore/forget; without restic only `/v1/backups/status` answers (`available: false`).
```

(c) „Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `serverfiles`, `plugins`, `audit/auditapi`)“ → „Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `serverfiles`, `plugins`, `backups`, `audit/auditapi`)“.

(d) Abschnitt „Data“: „(`Auth…`, `Defense…`, `Rbac…`, `Audit…`, `Servers…`, `Plugins…`)“ → „(`Auth…`, `Defense…`, `Rbac…`, `Audit…`, `Servers…`, `Plugins…`, `Backups…`)“.

(e) Abschnitt „Minecraft instances“, nach dem `internal/plugins`-Spiegelstrich einfügen:

```markdown
- `internal/backups` exposes `/servers/{id}/backups/*` (`server.backups`: reads every file via the download and can roll the server back; restore and snapshot delete need sudo). restic is the source of truth for snapshots; `backup_schedules` holds schedule (`off|interval|daily|weekly`, time zone `instance_settings.backup_timezone`), retention (`keep_*`, applied with `restic forget` to scheduled snapshots only, `pinned` kept), the last scheduled run and `saveoff_since`. A minute ticker (`clock.Clock`) queues due runs; one backup/restore/delete job runs panel-wide (FIFO, one per server, `409 backup_running`). A running server is backed up between RCON `save-off`/`save-all flush` and `save-on`; `save-on` follows on every path, and after a crash the panel sends it at start (`saveoff_since`). A restore holds the data gate (`servers.DataGate`, shared with extract: no start, no reconcile start), stops the server, takes a `pre-restore` snapshot, restores, and lets the reconcile loop start it again. Failed scheduled backups and restores notify `backup.failed` (once per 6 h per server); user actions are audited as `backup.*`; `DELETE /servers/{id}?purge_backups=1` removes the repo.
```

(f) Letzter Spiegelstrich: „(`scripts/ci-instances.sh`, including the console endpoints, the files service and plugin installs from the real Modrinth and Hangar through the panel in production mode)“ → „(`scripts/ci-instances.sh`, including the console endpoints, the files service, plugin installs from the real Modrinth and Hangar and backup/restore/download with a real restic through the panel in production mode)“.

- [ ] **Step 4: `README.md`** – fünf Stellen.

(a) „Funktionen“: nach dem Spiegelstrich „Plugins und Mods“ einfügen:

```markdown
- **Backups:** Datenverzeichnis eines Servers per Klick oder nach Zeitplan (alle N Stunden, täglich, wöchentlich) sichern, auch im laufenden Betrieb (Speichern wird währenddessen angehalten). Aufbewahrung nach Stufen (letzte, täglich, wöchentlich, monatlich), wichtige Stände anpinnen, als Zip herunterladen und den ganzen Server auf einen Stand zurücksetzen; vor jedem Restore entsteht automatisch ein Sicherheits-Snapshot. Lokale, deduplizierte restic-Repos; fehlgeschlagene Läufe melden sich über Discord/ntfy.
```

und im Spiegelstrich „Rechte“: „(`server.view`, `server.power`, `server.console`, `server.files`, `server.plugins`)“ → „(`server.view`, `server.power`, `server.console`, `server.files`, `server.plugins`, `server.backups`)“.

(b) Tabelle „Aufbau“, Zeile `files`: „Dateibrowser: Dateioperationen im Datenverzeichnis eines Servers, nur für das panel (und root), über einen eigenen Unix-Socket“ → „Dateibrowser und Backups (restic als Kindprozess, ohne Netz): Dateioperationen im Datenverzeichnis eines Servers, nur für das panel (und root), über einen eigenen Unix-Socket“.

(c) „Dokumentation“: nach der 3c-Spec-Zeile einfügen:

```markdown
| [`docs/dev/acceptance-tp4.md`](docs/dev/acceptance-tp4.md) | manuelle Abnahme der Backups auf der Test-VM |
| [`docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md`](docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md) | Backups mit lokalen restic-Repos über den Dateidienst |
```

(d) „Stand“: `| 4 | Backups | offen |` → `| 4 | Backups (lokal; Offsite später) | in Arbeit |`.

(e) Abschnitt „Schnellstart“: nichts ändern (`make dev` startet keine neuen Prozesse; restic ist optional).

- [ ] **Step 5: Prüfen und committen**

Jede Zahl, jeden Namen und jeden UI-Text gegen den Code auf dem Branch prüfen, z. B.:

```bash
grep -rn "MaxNoteRunes\|max_backup_bytes\|max_backup_entries\|max_backup_jobs\|MemoryMax" internal/files/filesapi internal/config/files.go scripts/testvm-deploy.sh | head
grep -rn "6 \* time.Hour\|time.Minute\|120 \* time.Second\|60 \* time.Second" internal/backups/*.go | grep -v _test | head
grep -rn "ActionBackup" internal/audit/types.go
grep -rn "EventBackupFailed\|Backup failed" internal/notify/*.go
grep -rn "Backup erstellen\|Wiederherstellen\|Herunterladen\|Anpinnen\|Zeitzone für Backups\|Backups ebenfalls löschen\|nicht verfügbar" web/src/i18n/de.ts | head -20
ls web/e2e
```

Abweichungen im Text korrigieren, nicht im Code. Code-Abweichungen von der Spec meldet der Teammate dem Lead.

```bash
make check
git add docs/dev/acceptance-tp4.md docs/dev/README.md CLAUDE.md README.md
git commit -m "docs: backups for dev, operation, CI and acceptance

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: CI grün. Danach dem Lead melden: Commit-Bereiche beider Aufgaben, CI-Run-URL, Laufzeit des Jobs „Instance acceptance“, Ergebnis des VM-Laufs aus Aufgabe 18 Step 8, Abweichungen vom Plan.

- [ ] **Step 6 (Lead): Gesamt-Review parallel starten** – sobald B1–B3 gemerged sind (unabhängig von Aufgabe 18).

Drei Review-Subagenten auf dem höchsten Modell, Basis = Commit vor dem Merge von `feat/backups-contracts`, Ende = aktueller `main` (`git diff <basis>..origin/main`). Jeder bekommt: Spec, Übersicht (Verträge, Global Constraints, **Review Focus 1–5**) und die Liste der aufgeschobenen Minors aus den Ledgern der Phasen.

  1. **Security**, in dieser Reihenfolge:
     - (a) Review Focus 2: feindliches Datenverzeichnis beim Backup (Symlink-Tausch während des Durchlaufs, FIFO, Gerätedatei, wachsende/schrumpfende Datei, ungültige Namen, Grenzen). Nichts außerhalb von `os.Root` im tar, keine Hänger.
     - (b) Review Focus 3: feindlicher Snapshot beim Restore (Symlink-/Hardlink-/Geräte-Einträge, `../`, absolute Pfade, doppelte und verborgene Namen, falsche Größen, abgeschnittener Stream). Job `failed`, Datenverzeichnis unverändert, kein Byte außerhalb des Temp-Ordners; Tausch nur per `rename` im `os.Root`.
     - (c) restic-Aufruf: feste argv, leere Umgebung, Snapshot-ID vor der Übergabe gegen das Repo geprüft, Notiz nur als base64url-Tag, `TZ` validiert, keine Host-Pfade in `job.error`/Antworten, Schlüsseldateien 0600.
     - (d) panel: Rechte aller acht Routen (`server.backups`, Sudo für Restore/Löschen), CSRF, Snapshot-ID-Muster, Download ohne Puffern mit Limit pro Benutzer, Audit (Akteur, keine geplanten Läufe), `purge_backups` nur für Admins mit Sudo.
     - (e) Frontend: Restore-Dialog mit Namensbestätigung, Notizen nur als Text, kein `dangerouslySetInnerHTML`.
  2. **Backend**, Querschnitt Zeitplan ↔ Warteschlange ↔ Jobs ↔ agent ↔ Dienst ↔ OpenAPI:
     - Review Focus 1: `save-on` auf jedem Pfad, Wiederanlauf über `saveoff_since`, Abbruch beim Herunterfahren innerhalb von 20 s.
     - Review Focus 4: Gleichzeitigkeit (Restore während wartendem/laufendem Backup, Entpacken während Backup, Start während Restore auch nach panel-Neustart, Server gelöscht mit wartendem Job, Zeitzonenwechsel, doppeltes Pinnen).
     - Review Focus 5: Platte voll, restic fehlt/alt, verwaiste Sperre, fehlender Schlüssel.
     - Zeitplan-Rechnung (Sommerzeit, verpasste Termine einmal), Retention nur `kind=scheduled` mit `--keep-tag pinned` und `TZ`.
     - Abbildung der Dienst-Fehler auf `error_code` (V5), `DataGate`-Filter auf Job-Arten (laufendes Backup sperrt den Start nicht).
     - Spec-Treue §3–§5.
  3. **Frontend**, gegen das echte Backend (Test-VM oder `make dev` mit restic, nicht nur den Mock):
     - Snapshot-Liste (Badges, Notizen, Größe, Zeiten in Browser- vs. panel-Zeitzone)
     - Job-Anzeige (Polling nur solange `queued`/`running`, Warteschlangen-Position, übersetzte `error_code`)
     - Zeitplan-Formular (Moduswechsel, Validierung, Retention-Erklärung, nächster/letzter Lauf)
     - Restore- und Lösch-Dialoge mit Sudo-Wiederholung
     - Hinweis bei `available=false`, Zeitzone in den Einstellungen, Checkbox beim Server-Löschen, Rechte-Hinweis
     - i18n vollständig (`de`/`en`), Mobil, Barrierefreiheit

  Befunde mit `superpowers:receiving-code-review` bewerten und priorisieren; jeder Befund nennt Datei, Zeile, Szenario und erwartetes Verhalten.

- [ ] **Step 7 (Lead): Fix-Runden**
  - **Wer:** Berechtigte Befunde (Critical/Important und die als „jetzt beheben“ eingestuften Minors) gehen an die zuständigen Teammates nach Dateieigentum der Phase B:
    - B1: `internal/files/**`, `internal/config/files*.go`, `internal/agent/instances/datadir*.go`
    - B2: `internal/backups/**`, `cmd/panel/**`
    - B3: `web/**`
    - Vertragsänderungen an `filesapi`, `servers`, Store, Fehlercodes, OpenAPI und Mocks laufen über den Lead wie in Phase A.
  - **Wie:** je in eigenem Worktree auf `fix/backups-review-<bereich>` von `origin/main`, Muster wie TP2–3c: `test(<bereich>): cover whole-feature review findings of TP4 (red)`, dann `fix(<bereich>): address whole-feature review findings of TP4`.
  - **Doku:** Die Teammates ändern keine Doku, sondern liefern eine Liste nötiger Spec-Änderungen.
  - **Danach:** Re-Review nur der Fixes; offene Punkte entscheidet der Lead mit Begründung im Ledger. Nicht berechtigte Befunde mit Begründung als PR-Kommentar festhalten.

- [ ] **Step 8 (Lead): Spec angleichen und CI auf dem Gesamtstand**
  - Der Teammate „ci-docs“ führt auf `feat/backups-ci-docs` `git merge origin/main` aus, damit die CI-Abnahme gegen alle Fixes läuft.
  - Er übernimmt die Liste aus Step 7 mit eigenem Commit `docs(spec): bring the TP4 spec in line with the review fixes`. Die Status-Zeile bleibt „entworfen“.
  - Er passt Doku-Texte an geänderte Zahlen/Texte an.
  - Danach Re-Review der Doku aus Steps 1–4 gegen den Code.

- [ ] **Step 9 (Lead): Test-VM-Deploy und Übergabe**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
sha="$(git -C /Users/jannes/Workspace/mc-panel rev-parse origin/feat/backups-ci-docs)"
make deploy-testvm REF="$sha"
ssh claude@192.168.1.117 'systemctl is-active mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel caddy; /usr/local/bin/restic version; sudo stat -c "%U:%G %a" /srv/mc-panel/backups; sudo -u mcpanel curl -sS --unix-socket /run/mc-panel-files/files.sock http://files/v1/backups/status; systemctl show mcpanel-files -p RestrictAddressFamilies -p MemoryMax -p ReadWritePaths'
```

(SSH nur mit abgeschalteter Sandbox.)

Expected:
- fünfmal `active`
- `restic 0.19.1 …`
- `mcpanel-mc:mcpanel-mc 700`
- `{"available":true,"version":"0.19.1",…}`
- `RestrictAddressFamilies=AF_UNIX`, `MemoryMax=1073741824`, beide Pfade in `ReadWritePaths`

Danach die PRs `fix/backups-review-*` und `feat/backups-ci-docs` mit grüner CI auf „ready“ setzen (`gh pr ready <pr>`). Dem Nutzer melden:
- PR-Links
- Zusammenfassung der Befunde und Entscheidungen
- Merge-Reihenfolge (erst Fixes, dann CI/Doku)
- dass `docs/dev/acceptance-tp4.md` auf der VM (Stand `$sha`) abgenommen werden kann

Nicht selbst mergen.

- [ ] **Step 10 (Lead): Abnahme begleiten** – nach dem Merge durch den Nutzer:

```bash
git -C /Users/jannes/Workspace/mc-panel switch main && git -C /Users/jannes/Workspace/mc-panel pull --ff-only
make deploy-testvm
```

Dann dem Nutzer `docs/dev/acceptance-tp4.md` übergeben. Befunde: Issue, Fix auf eigenem `fix/…`-Branch mit PR (Teammate), CI grün, erneut deployen, betroffene Zeilen der Checkliste wiederholen.

- [ ] **Step 11 (Lead): Abschluss nach erfolgreicher Abnahme („docs: close sub-project 4“)** – auf neuem Branch `docs/tp4-done` von `origin/main`:

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel switch -c docs/tp4-done origin/main
gh pr list --state merged --search "backups in:title" --json number,title,mergedAt
```

- Spec `2026-10-12-mc-panel-backups-design.md`: Status-Zeile `- **Status:** entworfen` → `- **Status:** umgesetzt (PRs #<erste>–#<letzte>, <Merge-Datum der letzten, JJJJ-MM-TT>)`. Nummern aus der Liste, inklusive Spec-/Plan-PR und Fix-PRs.
- Roadmap in `2026-10-08-mc-panel-foundation-design.md`: Zeile `| 4 | Backups | Zeitpläne, Retention, Offsite via restic, konsistente Snapshots, Restore |` → `| 4 | Backups (umgesetzt, Spec `2026-10-12-mc-panel-backups-design.md`) | Zeitpläne, Retention, konsistente Snapshots, Restore, Download; lokale restic-Repos (Offsite zurückgestellt) |`.
- `README.md`, „Stand“: 4 `in Arbeit` → `fertig`.
- `CLAUDE.md`: „(1 foundation, 2 instances, 3a console, 3b files and 3c plugins are done)“ → „(1 foundation, 2 instances, 3a console, 3b files, 3c plugins and 4 backups are done)“.
- Pläne löschen und committen:

```bash
cd /Users/jannes/Workspace/mc-panel
git rm docs/superpowers/plans/2026-10-12-backups-0*.md
git add docs/superpowers/specs README.md CLAUDE.md
make check
git commit -m "docs: drop the completed sub-project 4 plans, mark the spec done

All plan tasks are merged and the manual acceptance on the test VM passed; git
history keeps the plans. The spec stays as the reference and says so in its
status line.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin docs/tp4-done
gh pr create --title "docs: close sub-project 4" --body "$(cat <<'EOF'
Drops the completed TP4 plans (git history keeps them) and marks the spec, the roadmap row, README and CLAUDE.md done. Manual acceptance on the test VM passed.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
EOF
)"
gh pr checks <pr> --watch --interval 30
```

- Aufräumen:
  - Worktrees der Teammates entfernen (`git worktree remove ../mc-panel-wt/<branch>`), lokale Branches löschen, Teammates herunterfahren.
  - Remote-Branches nur nach Freigabe des Nutzers löschen (GitHub löscht gemergte Branches ggf. selbst).
  - `~/wt-backups` auf der VM löschen.
  - SDD-Workspaces unter `.superpowers/sdd/2026-10-12-backups-*` löschen, nachdem die „Rulings“ dem Nutzer berichtet wurden.
  - Mergen des Abschluss-PRs nur durch den Nutzer.

## Self-Review (dieser Datei)

- **Spec §9 → CI:**
  - Kriterium 1 → Abschnitt „manual backup“ (Log-Reihenfolge `save-off` < `Saved the game` < `save-on`, Snapshot „manuell“ mit Notiz und Größe).
  - Kriterium 2 (Zeitplan zur erwarteten Zeit in der eingestellten Zeitzone; Retention lässt manuelle/Pre-Restore stehen) → „schedule“. Pin geschützt bzw. mehrere geplante Läufe nur manuell (Zeile 2b) und in den Go-Integrationstests.
  - Kriterium 3 → „restore“ (Start `409`, Pre-Restore-Snapshot mit altem Stand, verborgene Datei bleibt, Neustart).
  - Kriterium 4 → „download“.
  - Kriterium 5 → Symlink weder im tar noch im Zip, nach dem Restore weg, Router-Konfiguration unverändert.
  - Kriterium 9 (mit Purge) → „delete the server“; ohne Purge nur manuell (9b).
  - Kriterien 6–8 und Review Focus 4–5 → nur manuell (Zeilen 6–8, 12–13).
- **Spec §7:** restic per Prüfsumme in CI und VM (`install-restic.sh`), CI-Abnahme wie beschrieben, Checkliste, README/CLAUDE.md → Aufgaben 18–19. Units §3.2 → Aufgabe 18 Step 7.
- **Verträge:**
  - V3 genutzt wie festgelegt: `202 {job}`, `{job}`, `{schedule}`, `{snapshot}`, `queued_at`, `trigger`, `step`, `error_code`, `snapshot`, `backup_timezone`, `purge_backups`, Fehler `backup_running`/`validation_failed`/`conflict`.
  - V1: `/v1/backups/status`.
  - Tags aus den Global Constraints der Übersicht.
- **Annahmen** (in Step 1 geprüft, bei Abweichung Lead fragen):
  - Audit-Ziel `server:<id>` wie bei `plugin.*`.
  - `server.delete`-Detail `backups_purged: true` nach erfolgreichem Purge (V2 nennt nur `false` bei Fehler und „fehlt“ ohne Parameter).
  - restic zeigt `paths` als `/data.tar` (geprüft tolerant).
- Kein Schritt startet Container, braucht root oder installiert etwas auf dem Mac; Host-verändernde Prüfungen nur in der CI und auf der Test-VM. Mergen, Tags und Löschen von Remote-Branches nur durch bzw. nach Freigabe des Nutzers.
