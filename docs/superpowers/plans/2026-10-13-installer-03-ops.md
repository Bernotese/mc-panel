# Teilprojekt 6 – `update`, `doctor`, `uninstall` (Aufgaben 8–10) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die drei Betriebs-Unterbefehle des Installers: `update` (Zielversion bestimmen, Übergabe an das `install.sh` der Zielversion, Sicherung von DB, Binaries und Units, Austausch, Health-Check über 90 s, automatischer Rollback mit Exit-Code 3 bzw. 4, sicher gegen Abbruch), `doctor` (alle Prüfungen aus Spec §6 mit ✓ / ! / ✗, Abhilfe-Satz, `--json`, Exit-Codes 0/1/2) und `uninstall` (nur mc-panel-Objekte, Daten bleiben; `--purge` mit unumgehbarer Bestätigung durch Abtippen der Domain).

**Architecture:**
- Drei Dateien in `scripts/install/`: `update.sh` (Präfix `upd_`, Einstieg `cmd_update`), `doctor.sh` (`doc_`, `cmd_doctor`), `uninstall.sh` (`uni_`, `cmd_uninstall`). Sie ersetzen die Stubs aus Phase A (V4) und nutzen nur die Helfer aus `lib.sh` (V2), die Pfad-Konstanten mit `$ROOT` und die `sys_*`-Wrapper.
- Alles, was das echte System *liest* und nicht über einen `sys_*`-Wrapper läuft (HTTP über Unix-Sockets, `sqlite3`, `sshd -T`, `findmnt`, `openssl`, `getent`, `ip`, `timedatectl`, `dpkg-query`, `stat` von `/proc/<pid>/status`), steckt in je einer kleinen **Probe-Funktion** (`upd_probe_*`, `doc_probe_*`, `uni_probe_*`). Die Tests definieren genau diese Funktionen nach dem Sourcen neu (Bash erlaubt das) und brauchen so weder root noch echte Dienste. So bleibt V2 unverändert; siehe „Vertragsfragen“ 2.
- `update` schreibt pro Lauf ein Rollback-Verzeichnis `$ROLLBACK_DIR/<alte-version>-<UTC-Zeit>/` mit `state` (eine Datei `key=value`): `phase=backed_up|applying|verified|rolled_back|rollback_failed`, `from=<version>`, `to=<version>`. Ein Lauf, der in `applying` hängen geblieben ist (Strg-C ohne trap, Stromausfall), wird beim nächsten `update` **zuerst** zurückgerollt; `doctor` meldet ihn als ✗.
- `doctor` sammelt Ergebnisse in drei parallelen Arrays und gibt sie am Ende aus (Text oder JSON); jede Prüfung ist eine Funktion `doc_chk_<name>`, die genau einmal `doc_result` aufruft (bzw. einmal pro geprüftem Objekt).
- `uninstall` trennt strikt „mc-panel-Objekt“ (Units mit Präfix `mcpanel-`, unsere Binaries, `mc-panel.caddy` + markierte Import-Zeile, Tabellen `inet mcpanel_fw` und `inet mcpanel`, Container mit Label `mcpanel.instance`, Container `mcpanel-router`, Netz `mcpanel-mc`) von allem anderen.

**Tech Stack:** Bash 5, coreutils, `curl`, `sqlite3`, `systemctl`, `nft`, `docker`, `caddy`, `openssl` (Ubuntu-Grundsystem), shellcheck; Test-Harness aus Phase A (`scripts/install/test.sh`).

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` (§3.1, §5, §6, §7, §10) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-13-installer-00-overview.md` (V2–V5, Global Constraints, Review Focus 3 und 5; Abschnitt „Ausführung“ gilt vollständig)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | B2, Teammate „inst-ops“, `feat/installer-ops` |
| Startet, wenn | Phase A gemerged (lib.sh, main.sh, Stubs, test.sh, assemble.sh, Vorlagen) |
| Dateieigentum | nur `scripts/install/update.sh`, `doctor.sh`, `uninstall.sh`, `scripts/install/tests/ops_*.sh` |
| Nutzt aus B1 (Namen laut V4, in Phase A als Stubs vorhanden) | `bin_install`, `cfg_ensure`, `unit_ensure` – für den Austausch beim Update; B1 muss sie mit `TARGET_VERSION`/`FROM_DIR` wiederholt aufrufbar machen (Vertragsfragen 6) |
| Wo getestet wird | Mac: `make check` (enthält `bash -n` und das Zusammenfügen); VM: `make test-vm SUITE=lint` (shellcheck + `scripts/install/test.sh`); echte Läufe nur im CI-Job `installer` (Phase C) |
| Update-Konstanten | `UPD_KEEP=3`, `UPD_HEALTH_SECS=90`, API `https://api.github.com/repos/Bernotese/mc-panel/releases/latest`, Downloads `https://github.com/Bernotese/mc-panel/releases/download/<tag>/<asset>` |
| DB | `$DATA_DIR/panel.db` (`config.Panel.DBPath`), Besitzer `mcpanel:mcpanel`, 0600; WAL-Dateien `panel.db-wal`/`-shm` |
| Health-Endpunkte | panel `GET /api/health` über `/run/mc-panel-web/panel.sock` → 200 `{"status":"ok"}`; agent `GET /v1/health` über `/run/mc-panel/agent.sock` → `{"status":"ok","version":…}`; files `GET /v1/health` über `/run/mc-panel-files/files.sock` → `{"status":"ok","version":…}` (root ist bei agent und files zugelassen) |
| Versionsausgabe | `panel version`, `agent -version`, `files -version` (je eine Zeile) |
| Caddy-Import-Zeile | exakt `import mc-panel.caddy # managed by mc-panel` (B1 schreibt sie, B2 entfernt sie; Vertragsfragen 3) |
| Purge-Bestätigung | Domain aus `public_url` in `panel.toml` (Host ohne Port); steht in `mc-panel.caddy` `tls internal` und ist die Domain eine `sslip.io`-/IP-Domain, gilt trotzdem die Domain; ohne lesbare Domain das Wort `mc-panel` |

## Review Focus (aus der Übersicht, in dieser Phase)

- **3 – Fehlgeschlagenes Update:** Binary startet nicht (Health ✗), Migration scheitert (panel bleibt nicht `active`), Strg-C/SIGTERM mitten im Austausch, Stromausfall (Zustand `applying` beim nächsten Lauf), DB-Kopie scheitert wegen Platz → Tests in Aufgabe 8 (`test_update_rolls_back_on_failed_health`, `test_update_rolls_back_on_signal`, `test_update_finishes_unfinished_first`, `test_update_refuses_without_space`, `test_update_rollback_failure_exits_4`).
- **5 – Uninstall:** fremde Container, fremde nftables-Tabellen, fremde Caddy-Sites, `--purge --yes` ohne Abtippen → Tests in Aufgabe 10 (`test_uninstall_leaves_foreign_objects`, `test_purge_needs_typed_domain_even_with_yes`).

## Vertragsfragen – vom Lead entschieden (Übersicht V2–V5, verbindlich)

1. **`panel.toml`:** Schlüssel `public_url`.
2. **Lesende Systemzugriffe:** Probe-Funktionen je Datei, Tests ersetzen sie per `stub NAME BODY` bzw. durch eine eigene Funktionsdefinition.
3. **Caddy-Import-Zeile:** genau `import mc-panel.caddy # managed by mc-panel`; `uninstall` entfernt genau diese Zeile und lädt Caddy per `sys_systemctl reload caddy` neu (nicht über die Admin-API).
4. **Harness:** die API aus Phase A (V2): `assert_called`/`assert_not_called` vergleichen **Festtext** mit den echten `calls.log`-Zeilen (`systemctl …`, `nft …`, `docker …`, `userdel …`, `exec …`); `fake_exec` (Phase A) zeichnet ein `exec` auf. Ausführbare Fake-Binaries legt dieser Plan mit dem eigenen Testhelfer `ops_fake_bin PATH BODY` an. `test.sh tests/ops_update.sh` wählt die Tests einer Datei.
5. **Übergabe an das Ziel-`install.sh`:** `release_tag "$TARGET_VERSION"` liefert den Tag `vX.Y.Z`; das Aufräumen des Eltern-`WORK` macht nur `setup_work` in `main.sh` (`MCPANEL_HANDOFF_WORK`); `MCPANEL_UPDATE_HANDOFF=1` sagt dem Kind, dass es nicht erneut übergibt.
6. **B1-Funktionen beim Update:** Reihenfolge `bin_install → cfg_ensure → unit_ensure` in `upd_swap` (Subshell mit `set -e`); bei `CMD=update` schreibt `unit_ensure` nur Dateien und lädt systemd neu, Start und Health-Check macht `update`.
7. **`cmd_doctor`:** `return <code>`; `DOC_SUMMARY=1` für die Kurzform; JSON-Schema `{"status","checks":[{"id","status","message","hint"}]}` ist Vertrag für Phase C. Ohne Netz liefert `upd_latest_tag` nur Rückgabewert 1 (kein `die`), die Versionsprüfung meldet dann `warn`.
8. **Zielversion mit `--from-dir`:** setzt `validate_args` aus `"$FROM_DIR/panel" version` (ohne Netz); `cmd_update` fragt die GitHub-API nur ohne `--version` und ohne `--from-dir`.
9. **Rollback-Verzeichnis:** `ROLLBACK_DIR=/var/lib/mc-panel-rollback` (`root:root 0700`). DB-Sicherung und -Rückspielen laufen vollständig als `mcpanel` (`upd_db_backup`, `upd_db_restore` über `run_as`); root öffnet und schreibt nichts unter `/var/lib/mc-panel`.
10. **Rechte, die `doctor` erwartet:** wie B1 sie setzt – `agent.toml` `root:root 600`, `instances/` `root:mcpanel-mc 750`, `backups/` `mcpanel-mc:mcpanel-mc 700`; Pflichtschlüssel = `CFG_REQUIRED_*` aus `config.sh` (ein Test vergleicht beide Listen).

## Dateistruktur

```
scripts/install/update.sh          (8)  cmd_update, upd_* (Zielversion, Übergabe, Sicherung, Austausch, Health, Rollback, Aufräumen)
scripts/install/tests/ops_update.sh (8)
scripts/install/doctor.sh          (9)  cmd_doctor, doc_result, doc_chk_*, doc_probe_*, Ausgabe Text/JSON
scripts/install/tests/ops_doctor.sh (9)
scripts/install/uninstall.sh       (10) cmd_uninstall, uni_* (Bestätigung, Units, Binaries, Caddy, Firewall, Docker, Sockets, Purge)
scripts/install/tests/ops_uninstall.sh (10)
```

---

### Task 8: `update` – Sicherung, Austausch, Health-Check, Rollback

**Files:**
- Modify: `scripts/install/update.sh` (Stub aus Phase A ersetzen)
- Create: `scripts/install/tests/ops_update.sh`

**Interfaces:**
- Consumes (V2): `log_step`, `log_ok`, `log_changed`, `log_skip`, `warn`, `die`, `version_ge`, `fetch`, `sha256_verify`, `systemd_wait_active`, `sys_systemctl`; Konstanten `BIN_DIR`, `UNIT_DIR`, `DATA_DIR`, `ROLLBACK_DIR`, `ETC_PANEL`; Variablen `MCPANEL_VERSION`, `TARGET_VERSION`, `FROM_DIR`, `CHECK_ONLY`, `FORCE`, `YES`, `ARCH`, `WORK`, `ROOT`. Aus B1 (V4): `bin_install`, `cfg_ensure`, `unit_ensure`.
- Produces:
  - `cmd_update` → Exit-Code über `return`: 0 (aktuell / aktualisiert), 1 (Fehler vor jeder Änderung), 3 (zurückgerollt), 4 (Rollback gescheitert).
  - `upd_installed_version` (stdout: Version aus `$ROOT$BIN_DIR/panel version` oder `none`).
  - Zustand in `$ROOT$ROLLBACK_DIR/<name>/state`, von `doctor` (Aufgabe 9) gelesen: Funktion `upd_unfinished_dirs` (stdout: Verzeichnisse mit `phase=applying` oder `phase=rollback_failed`, je eine Zeile).
  - Konstanten `UPD_UNITS`, `UPD_BINARIES` (von `uninstall` mitbenutzt).

- [ ] **Step 0: Harness abgleichen**

Phase A ist gemerged. Lies `scripts/install/test.sh`, `lib.sh` und `main.sh` auf `main` und vergleiche mit „Vertragsfragen“ 4 und 7. Weichen Namen ab (Assert-Helfer, Probe-Konvention, wie `cmd_*` aufgerufen wird), benenne in diesem Plan beim Abtippen konsequent um und vermerke es für den PR.

```bash
cd /Users/jannes/Workspace/mc-panel-wt/feat/installer-ops
grep -n '^[a-z_]*()' scripts/install/lib.sh scripts/install/test.sh scripts/install/main.sh
grep -n 'cmd_update\|cmd_doctor\|cmd_uninstall' scripts/install/*.sh
```

- [ ] **Step 1: Tests schreiben (rot)**

`scripts/install/tests/ops_update.sh`:

```bash
# shellcheck shell=bash
# Tests for update.sh (plan 03, task 8). Run by scripts/install/test.sh with
# MCPANEL_INSTALL_DRYRUN=1, a fresh ROOT and WORK per test function.

# ops_fake_bin PATH BODY writes an executable shell script (fake binary). Not fake_exec: that
# helper of the harness records an exec call (overview V2).
ops_fake_bin() {
	mkdir -p -- "${1%/*}"
	printf '#!/bin/sh\n%s\n' "$2" >"$1"
	chmod +x -- "$1"
}

# ops_fake_install creates an installed system in $ROOT: three fake binaries that print
# VERSION, the units and a database file.
ops_fake_install() {
	local version="$1" b u
	mkdir -p "$ROOT$BIN_DIR" "$ROOT$UNIT_DIR" "$ROOT$DATA_DIR"
	for b in panel agent files; do
		ops_fake_bin "$ROOT$BIN_DIR/$b" "echo $version"
	done
	for u in "${UPD_UNITS[@]}"; do
		printf '# %s %s\n' "$u" "$version" >"$ROOT$UNIT_DIR/$u"
	done
	printf 'db %s\n' "$version" >"$ROOT$DATA_DIR/panel.db"
}

# ops_fake_new_build makes bin_install/unit_ensure/cfg_ensure install VERSION (stands in for B1).
ops_fake_new_build() {
	OPS_NEW_VERSION="$1"
	bin_install() {
		local b
		for b in panel agent files; do
			ops_fake_bin "$ROOT$BIN_DIR/$b" "echo $OPS_NEW_VERSION"
		done
	}
	unit_ensure() {
		local u
		for u in "${UPD_UNITS[@]}"; do
			printf '# %s %s\n' "$u" "$OPS_NEW_VERSION" >"$ROOT$UNIT_DIR/$u"
		done
	}
	cfg_ensure() { :; }
}

# Probes: healthy unless OPS_UNHEALTHY is set; versions come from the fake binaries.
ops_probes() {
	upd_probe_health() {
		[ -z "${OPS_UNHEALTHY:-}" ] || return 1
		return 0
	}
	upd_db_backup() { cp -- "$ROOT$DATA_DIR/panel.db" "$1"; }
	upd_db_restore() { cp -- "$1" "$ROOT$DATA_DIR/panel.db"; }
	upd_free_bytes() { echo "${OPS_FREE:-100000000000}"; }
	systemd_wait_active() { [ -z "${OPS_UNHEALTHY:-}" ]; }
	sleep() { :; }
}

test_update_check_only_reports_newer() {
	ops_fake_install 1.0.0
	ops_probes
	upd_latest_tag() { echo v1.1.0; }
	CHECK_ONLY=1
	local out
	out="$(cmd_update 2>&1)"
	assert_contains "$out" "1.1.0"
	assert_not_called 'systemctl'
	assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_update_same_version_without_force_is_a_noop() {
	ops_fake_install 1.1.0
	ops_probes
	TARGET_VERSION=v1.1.0
	FROM_DIR="$WORK/build"
	cmd_update
	assert_eq 0 "$?"
	assert_no_file "$ROOT$ROLLBACK_DIR"
	assert_not_called 'systemctl restart'
}

test_update_hands_over_to_target_installer() {
	ops_fake_install 1.0.0
	ops_probes
	MCPANEL_VERSION=1.0.0
	TARGET_VERSION=v1.1.0
	fetch() { printf '#!/bin/bash\necho child "$@"\n' >"$2"; }
	sha256_verify() { return 0; }
	upd_exec() { echo "exec $*" >>"$WORK/calls.log"; }
	cmd_update
	assert_called "exec bash $WORK/install.sh update --version v1.1.0"
	assert_no_file "$ROOT$ROLLBACK_DIR"
}

test_update_refuses_bad_checksum_before_any_change() {
	ops_fake_install 1.0.0
	ops_probes
	MCPANEL_VERSION=1.0.0
	TARGET_VERSION=v1.1.0
	fetch() { echo x >"$2"; }
	sha256_verify() { die "checksum mismatch for $3"; }
	local rc=0
	(cmd_update) || rc=$?
	assert_eq 1 "$rc"
	assert_no_file "$ROOT$ROLLBACK_DIR"
	assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_update_success_keeps_backup_and_marks_verified() {
	ops_fake_install 1.0.0
	ops_probes
	ops_fake_new_build 1.1.0
	TARGET_VERSION=v1.1.0
	FROM_DIR="$WORK/build"
	cmd_update
	assert_eq 0 "$?"
	local dir
	dir="$(find "$ROOT$ROLLBACK_DIR" -mindepth 1 -maxdepth 1 -type d)"
	assert_contains "$(cat "$dir/state")" "phase=verified"
	assert_contains "$(cat "$dir/state")" "from=1.0.0"
	assert_eq "db 1.0.0" "$(cat "$dir/panel.db")"
	assert_eq "1.0.0" "$("$dir/bin/panel" version)"
	assert_eq "1.1.0" "$("$ROOT$BIN_DIR/panel" version)"
	assert_called 'systemctl daemon-reload'
	assert_called 'systemctl restart mcpanel-agent.service'
	assert_called 'systemctl restart mcpanel-panel.service'
}

test_update_rolls_back_on_failed_health() {
	ops_fake_install 1.0.0
	ops_probes
	ops_fake_new_build 1.1.0
	TARGET_VERSION=v1.1.0
	FROM_DIR="$WORK/build"
	# Unhealthy after the swap, healthy again once the old version is back.
	upd_probe_health() { [ "$("$ROOT$BIN_DIR/panel" version)" = "1.0.0" ]; }
	systemd_wait_active() { return 0; }
	UPD_HEALTH_SECS=0
	echo "db changed by migration" >"$ROOT$DATA_DIR/panel.db.new"
	local rc=0
	cmd_update || rc=$?
	assert_eq 3 "$rc"
	assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
	assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
	assert_contains "$(cat "$ROOT$UNIT_DIR/mcpanel-panel.service")" "1.0.0"
	local dir
	dir="$(find "$ROOT$ROLLBACK_DIR" -mindepth 1 -maxdepth 1 -type d)"
	assert_contains "$(cat "$dir/state")" "phase=rolled_back"
}

test_update_rollback_failure_exits_4() {
	ops_fake_install 1.0.0
	ops_probes
	ops_fake_new_build 1.1.0
	TARGET_VERSION=v1.1.0
	FROM_DIR="$WORK/build"
	OPS_UNHEALTHY=1
	UPD_HEALTH_SECS=0
	local rc=0 out
	out="$(cmd_update 2>&1)" || rc=$?
	assert_eq 4 "$rc"
	assert_contains "$out" "rollback"
	local dir
	dir="$(find "$ROOT$ROLLBACK_DIR" -mindepth 1 -maxdepth 1 -type d)"
	assert_contains "$(cat "$dir/state")" "phase=rollback_failed"
	assert_called 'systemctl stop mcpanel-panel.service'
}

test_update_rolls_back_on_signal() {
	ops_fake_install 1.0.0
	ops_probes
	ops_fake_new_build 1.1.0
	TARGET_VERSION=v1.1.0
	FROM_DIR="$WORK/build"
	# A TERM arrives right after the binaries were swapped.
	cfg_ensure() { kill -TERM $BASHPID; }
	local rc=0
	(cmd_update) || rc=$?
	assert_eq 3 "$rc"
	assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
}

test_update_finishes_unfinished_first() {
	ops_fake_install 1.1.0
	ops_probes
	# A previous run died in phase applying: its backup holds 1.0.0.
	local old="$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z"
	mkdir -p "$old/bin" "$old/units"
	ops_fake_bin "$old/bin/panel" "echo 1.0.0"
	ops_fake_bin "$old/bin/agent" "echo 1.0.0"
	ops_fake_bin "$old/bin/files" "echo 1.0.0"
	cp "$ROOT$UNIT_DIR"/* "$old/units/"
	echo "db 1.0.0" >"$old/panel.db"
	printf 'phase=applying\nfrom=1.0.0\nto=1.1.0\n' >"$old/state"
	upd_latest_tag() { echo v1.0.0; }
	CHECK_ONLY=1
	cmd_update >/dev/null 2>&1 || true
	assert_contains "$(cat "$old/state")" "phase=rolled_back"
	assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
	assert_eq "db 1.0.0" "$(cat "$ROOT$DATA_DIR/panel.db")"
}

test_update_refuses_without_space() {
	ops_fake_install 1.0.0
	ops_probes
	ops_fake_new_build 1.1.0
	TARGET_VERSION=v1.1.0
	FROM_DIR="$WORK/build"
	OPS_FREE=10
	local rc=0
	(cmd_update) || rc=$?
	assert_eq 1 "$rc"
	assert_eq "1.0.0" "$("$ROOT$BIN_DIR/panel" version)"
	assert_not_called 'systemctl'
}

test_update_keeps_three_backups() {
	ops_fake_install 1.0.0
	ops_probes
	local i
	for i in 1 2 3 4; do
		mkdir -p "$ROOT$ROLLBACK_DIR/0.$i.0-2026101${i}T000000Z"
		printf 'phase=verified\n' >"$ROOT$ROLLBACK_DIR/0.$i.0-2026101${i}T000000Z/state"
		touch -d "2026-10-1$i" "$ROOT$ROLLBACK_DIR/0.$i.0-2026101${i}T000000Z"
	done
	mkdir -p "$ROOT$ROLLBACK_DIR/0.0.9-20261001T000000Z"
	printf 'phase=rollback_failed\n' >"$ROOT$ROLLBACK_DIR/0.0.9-20261001T000000Z/state"
	touch -d "2026-10-01" "$ROOT$ROLLBACK_DIR/0.0.9-20261001T000000Z"
	upd_prune
	assert_no_file "$ROOT$ROLLBACK_DIR/0.1.0-20261011T000000Z"
	assert_file "$ROOT$ROLLBACK_DIR/0.2.0-20261012T000000Z"
	assert_file "$ROOT$ROLLBACK_DIR/0.4.0-20261014T000000Z"
	# A failed rollback is never pruned: it is the only copy of the old state.
	assert_file "$ROOT$ROLLBACK_DIR/0.0.9-20261001T000000Z"
}

test_update_rejects_unexpected_tag() {
	upd_fetch_latest_json() { printf '{"tag_name": "v1.2.3; rm -rf /"}' >"$1"; }
	local rc=0
	(upd_latest_tag >/dev/null) || rc=$?
	assert_eq 1 "$rc"
}
```

- [ ] **Step 2: Lauf, rot**

Run (auf der VM, nachdem `make test-vm` den Baum kopiert hat): `ssh claude@192.168.1.117 'cd ~/mc-panel-test/installer-ops && bash scripts/install/test.sh tests/ops_update.sh'`
Expected: FAIL – `cmd_update` ist noch der Stub („not implemented yet“), `upd_prune`/`upd_latest_tag` fehlen.

- [ ] **Step 3: `update.sh` schreiben**

```bash
# shellcheck shell=bash
# update.sh – `install.sh update` (spec §5): target version, hand-over to the target's
# install.sh, backup of DB, binaries and units, swap, health check, automatic rollback.

UPD_KEEP=3
UPD_HEALTH_SECS=90
UPD_API_LATEST="https://api.github.com/repos/Bernotese/mc-panel/releases/latest"
UPD_RELEASE_BASE="https://github.com/Bernotese/mc-panel/releases/download"
UPD_UNITS=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service)
UPD_BINARIES=(panel agent files)
# Restart order of spec §5 step 4: agent, files, panel (sockets before their services).
UPD_RESTART=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service)
UPD_DIR=""     # rollback dir of this run
UPD_FROM=""    # installed version before the swap

# --- probes (tests redefine these) -------------------------------------------------------

upd_fetch_latest_json() { fetch "$UPD_API_LATEST" "$1"; }

# upd_probe_health checks the three sockets; 0 when panel, agent and files answer "ok".
upd_probe_health() {
	curl -fsS --max-time 5 --unix-socket /run/mc-panel-web/panel.sock http://panel/api/health >/dev/null 2>&1 &&
		curl -fsS --max-time 5 --unix-socket /run/mc-panel/agent.sock http://agent/v1/health 2>/dev/null | grep -q '"status":"ok"' &&
		curl -fsS --max-time 5 --unix-socket /run/mc-panel-files/files.sock http://files/v1/health 2>/dev/null | grep -q '"status":"ok"'
}

# upd_db_backup DEST: consistent copy of the panel DB while the panel runs (sqlite online
# backup) into DEST inside the root-only rollback dir. Everything under DATA_DIR is done as
# mcpanel (the copy, reading it back, removing it): root never opens a path mcpanel controls,
# so a compromised panel cannot plant a symlink or a file that root installs (overview).
upd_db_backup() {
	local tmp
	tmp="$DATA_DIR/.rollback-$(date -u +%Y%m%dT%H%M%SZ)-$$.db"
	run_as mcpanel sqlite3 "$DATA_DIR/panel.db" ".backup '$tmp'" || return 1
	run_as mcpanel cat -- "$tmp" >"$1" || { run_as mcpanel rm -f -- "$tmp"; return 1; }
	run_as mcpanel rm -f -- "$tmp"
	[ -s "$1" ]
}

# upd_db_restore SRC: puts SRC (in the rollback dir) back as the panel DB, written by mcpanel.
upd_db_restore() {
	run_as mcpanel rm -f -- "$DATA_DIR/panel.db-wal" "$DATA_DIR/panel.db-shm" || return 1
	# shellcheck disable=SC2016 # $1 is expanded by the inner sh
	run_as mcpanel sh -c 'umask 077 && cat >"$1.restore" && mv -f -- "$1.restore" "$1"' sh "$DATA_DIR/panel.db" <"$1"
}

# upd_free_bytes DIR: free bytes for non-root users on DIR's file system.
upd_free_bytes() { df -B1 --output=avail "$1" | tail -n 1 | tr -d ' '; }

upd_exec() { exec "$@"; }

# --- versions ----------------------------------------------------------------------------

upd_installed_version() {
	local v
	v="$("$ROOT$BIN_DIR/panel" version 2>/dev/null)" || v=""
	echo "${v:-none}"
}

# upd_binary_versions prints the versions of panel, agent and files, space-separated.
upd_binary_versions() {
	local p a f
	p="$("$ROOT$BIN_DIR/panel" version 2>/dev/null || echo "?")"
	a="$("$ROOT$BIN_DIR/agent" -version 2>/dev/null || echo "?")"
	f="$("$ROOT$BIN_DIR/files" -version 2>/dev/null || echo "?")"
	echo "$p $a $f"
}

# upd_latest_tag prints the tag of the newest release (vX.Y.Z); returns 1 (no die) when the
# API is unreachable or answers nonsense, so doctor can use it offline (review #25).
upd_latest_tag() {
	local json="$WORK/latest.json" tag
	upd_fetch_latest_json "$json" 2>/dev/null || return 1
	tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$json" | head -n 1)"
	[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
	echo "$tag"
}

upd_strip_v() { echo "${1#v}"; }

# --- rollback dirs -----------------------------------------------------------------------

upd_state_get() { sed -n "s/^$2=//p" "$1/state" 2>/dev/null | head -n 1; }

upd_state_set_phase() {
	local dir="$1" phase="$2" from to
	from="$(upd_state_get "$dir" from)"
	to="$(upd_state_get "$dir" to)"
	printf 'phase=%s\nfrom=%s\nto=%s\n' "$phase" "$from" "$to" >"$dir/state.tmp"
	mv -f -- "$dir/state.tmp" "$dir/state"
}

# upd_unfinished_dirs prints rollback dirs whose update never finished (applying) or whose
# rollback failed; doctor reports both.
upd_unfinished_dirs() {
	local d phase
	[ -d "$ROOT$ROLLBACK_DIR" ] || return 0
	for d in "$ROOT$ROLLBACK_DIR"/*/; do
		[ -d "$d" ] || continue
		d="${d%/}"
		phase="$(upd_state_get "$d" phase)"
		case "$phase" in applying | rollback_failed) echo "$d" ;; esac
	done
}

# upd_prune keeps the UPD_KEEP newest dirs (by mtime); a dir in phase rollback_failed or
# applying is never removed.
upd_prune() {
	local d phase keep=0
	[ -d "$ROOT$ROLLBACK_DIR" ] || return 0
	while IFS= read -r d; do
		[ -n "$d" ] || continue
		phase="$(upd_state_get "$d" phase)"
		case "$phase" in applying | rollback_failed) continue ;; esac
		keep=$((keep + 1))
		if [ "$keep" -gt "$UPD_KEEP" ]; then
			rm -rf -- "$d"
			log_changed "removed old rollback copy $(basename -- "$d")"
		fi
	done < <(ls -1dt -- "$ROOT$ROLLBACK_DIR"/*/ 2>/dev/null | sed 's:/$::')
}

# --- steps -------------------------------------------------------------------------------

# upd_backup copies DB, binaries and units into a new rollback dir (phase backed_up).
upd_backup() {
	local db="$ROOT$DATA_DIR/panel.db" need free b u
	UPD_FROM="$(upd_installed_version)"
	UPD_DIR="$ROOT$ROLLBACK_DIR/$UPD_FROM-$(date -u +%Y%m%dT%H%M%SZ)"
	need=$((100 * 1024 * 1024))
	# size of the DB as seen by mcpanel's own directory listing is enough for an estimate
	[ -f "$db" ] && [ ! -L "$db" ] && need=$((need + $(stat -c %s -- "$db") * 11 / 10))
	ensure_dir "$ROLLBACK_DIR" 0700 root root >/dev/null # root:root 0700, outside DATA_DIR
	free="$(upd_free_bytes "$ROOT$ROLLBACK_DIR")"
	[ "$free" -ge "$need" ] || die "not enough free space for the backup in $ROLLBACK_DIR ($need bytes needed, $free free); nothing was changed"
	install -d -m 0700 "$UPD_DIR" "$UPD_DIR/bin" "$UPD_DIR/units"
	if [ -e "$db" ]; then
		upd_db_backup "$UPD_DIR/panel.db" || { rm -rf -- "$UPD_DIR"; die "database backup failed; nothing was changed"; }
	fi
	for b in "${UPD_BINARIES[@]}"; do
		cp -p -- "$ROOT$BIN_DIR/$b" "$UPD_DIR/bin/$b"
	done
	for u in "${UPD_UNITS[@]}"; do
		[ -f "$ROOT$UNIT_DIR/$u" ] && cp -p -- "$ROOT$UNIT_DIR/$u" "$UPD_DIR/units/$u"
	done
	printf 'phase=backed_up\nfrom=%s\nto=%s\n' "$UPD_FROM" "$(upd_strip_v "$TARGET_VERSION")" >"$UPD_DIR/state"
	log_ok "backup of $UPD_FROM in $(basename -- "$UPD_DIR")"
}

upd_restart_all() {
	local u
	sys_systemctl daemon-reload
	for u in "${UPD_RESTART[@]}"; do
		sys_systemctl restart "$u"
	done
}

# upd_healthy VERSION: all units active, all three binaries report VERSION and the health
# endpoints answer, within UPD_HEALTH_SECS.
upd_units_active() {
	local u
	for u in mcpanel-agent.service mcpanel-files.service mcpanel-panel.service; do
		systemd_wait_active "$u" 1 || return 1
	done
}

upd_healthy() {
	local want="$1" deadline
	deadline=$(($(date +%s) + UPD_HEALTH_SECS))
	while :; do
		if [ "$(upd_binary_versions)" = "$want $want $want" ] && upd_units_active && upd_probe_health; then
			return 0
		fi
		[ "$(date +%s)" -lt "$deadline" ] || return 1
		sleep 2
	done
}

# upd_rollback DIR puts the binaries, units and DB of DIR back and checks the old version.
# Returns 0 (rolled back) or 1 (rollback failed).
upd_rollback() {
	local dir="$1" from b u
	from="$(upd_state_get "$dir" from)"
	warn "rolling back to $from"
	for u in mcpanel-panel.service mcpanel-panel.socket mcpanel-files.service mcpanel-files.socket mcpanel-agent.service; do
		sys_systemctl stop "$u" || true
	done
	for b in "${UPD_BINARIES[@]}"; do
		[ -f "$dir/bin/$b" ] && install -m 0755 -- "$dir/bin/$b" "$ROOT$BIN_DIR/.$b.rollback" &&
			mv -f -- "$ROOT$BIN_DIR/.$b.rollback" "$ROOT$BIN_DIR/$b"
	done
	for u in "${UPD_UNITS[@]}"; do
		[ -f "$dir/units/$u" ] && cp -p -- "$dir/units/$u" "$ROOT$UNIT_DIR/$u"
	done
	if [ -f "$dir/panel.db" ]; then
		upd_db_restore "$dir/panel.db" || warn "could not put the database back"
	fi
	upd_restart_all
	if upd_healthy "$from"; then
		upd_state_set_phase "$dir" rolled_back
		return 0
	fi
	upd_state_set_phase "$dir" rollback_failed
	return 1
}

UPD_SIGNALED=0
upd_on_signal() { UPD_SIGNALED=1; }

# upd_swap runs the B1 steps in the order bin → cfg → unit (cfg_ensure sets the KEY_* that the
# unit templates need). It runs in a subshell with errexit on and outside any conditional
# context, so any failing command stops it and the caller rolls back (overview V4). With
# CMD=update, unit_ensure only writes the units and reloads systemd.
upd_swap() {
	local rc
	set +e
	(
		set -e
		bin_install
		cfg_ensure
		unit_ensure
	)
	rc=$?
	set -e
	return "$rc"
}

# upd_apply swaps in the target version; returns 0 when healthy.
upd_apply() {
	UPD_SIGNALED=0
	trap upd_on_signal INT TERM HUP
	upd_state_set_phase "$UPD_DIR" applying
	local ok=1
	if upd_swap && [ "$UPD_SIGNALED" = 0 ]; then
		upd_restart_all
		if [ "$UPD_SIGNALED" = 0 ] && upd_healthy "$(upd_strip_v "$TARGET_VERSION")"; then
			ok=0
		fi
	fi
	trap - INT TERM HUP
	[ "$UPD_SIGNALED" = 0 ] || warn "update interrupted"
	return "$ok"
}

# upd_handover downloads and verifies the target's install.sh and execs it (spec §5 step 2).
upd_handover() {
	local tag script="$WORK/install.sh" sums="$WORK/checksums.txt"
	tag="$(release_tag "$TARGET_VERSION")" # always vX.Y.Z (TARGET_VERSION has no v, review #10)
	fetch "$UPD_RELEASE_BASE/$tag/checksums.txt" "$sums" || die "cannot download checksums.txt of $tag"
	fetch "$UPD_RELEASE_BASE/$tag/install.sh" "$script" || die "cannot download install.sh of $tag"
	sha256_verify "$script" "$sums" install.sh
	local args=(update --version "$tag")
	[ "$FORCE" = 1 ] && args+=(--force)
	[ "$YES" = 1 ] && args+=(--yes)
	log_ok "handing over to install.sh $tag"
	MCPANEL_UPDATE_HANDOFF=1 MCPANEL_HANDOFF_WORK="$WORK" upd_exec bash "$script" "${args[@]}"
}

cmd_update() {
	log_step "update"
	# The parent's WORK after a hand-over is cleaned by setup_work in main.sh (overview V4).
	local d
	while IFS= read -r d; do
		[ -n "$d" ] || continue
		if [ "$(upd_state_get "$d" phase)" = applying ]; then
			warn "a previous update did not finish ($(basename -- "$d")); rolling it back first"
			upd_rollback "$d" || { warn "rollback of the unfinished update failed"; return 4; }
		fi
	done < <(upd_unfinished_dirs)

	# With --from-dir, validate_args already took the version of the build (no network).
	if [ -z "$TARGET_VERSION" ]; then
		TARGET_VERSION="$(upd_latest_tag)" || die "cannot reach the GitHub API to find the latest release; pass --version"
		TARGET_VERSION="${TARGET_VERSION#v}"
	fi
	local installed target
	installed="$(upd_installed_version)"
	target="$(upd_strip_v "$TARGET_VERSION")"
	if [ "$CHECK_ONLY" = 1 ]; then
		if [ "$installed" = "$target" ]; then
			log_ok "up to date ($installed)"
		else
			log_ok "installed $installed, available $target"
		fi
		return 0
	fi
	if [ "$installed" = "$target" ] && [ "$FORCE" != 1 ]; then
		log_skip "already at $installed (use --force to reinstall)"
		return 0
	fi
	if [ -z "$FROM_DIR" ] && [ "${MCPANEL_UPDATE_HANDOFF:-0}" != 1 ] && [ "$target" != "$MCPANEL_VERSION" ]; then
		upd_handover
		return 0 # only reached when upd_exec is a test double
	fi

	upd_backup
	if upd_apply; then
		upd_state_set_phase "$UPD_DIR" verified
		log_ok "updated $UPD_FROM -> $target"
		upd_prune
		return 0
	fi
	if upd_rollback "$UPD_DIR"; then
		warn "update to $target failed and was rolled back to $UPD_FROM; see $LOG_FILE"
		return 3
	fi
	warn "update to $target failed AND the rollback to $UPD_FROM failed."
	warn "All services are stopped. The old binaries, units and database are in $UPD_DIR;"
	warn "copy them back by hand (bin/* to $BIN_DIR, units/* to $UNIT_DIR, panel.db to $DATA_DIR) and run: systemctl daemon-reload && systemctl start mcpanel-agent mcpanel-files.socket mcpanel-panel.socket"
	return 4
}
```

Anmerkungen für die Umsetzung:
- `ls -1dt` in `upd_prune` ist für die Sortierung nach mtime bewusst (die Namen enthalten keine Leerzeichen, Zeitstempel und Versionen sind kontrolliert); shellcheck SC2012 dort gezielt mit Begründung ausnehmen (`# shellcheck disable=SC2012 # names are ours: <version>-<UTC time>`).
- `upd_healthy` wiederholt alle 2 s Versionen, Unit-Zustand (1 s Frist je Unit) und Health-Endpunkte, bis alles stimmt oder `UPD_HEALTH_SECS` abgelaufen ist; Tests ersetzen `sleep` durch einen No-op und machen die Probes sofort entscheidend, damit der Test nicht 90 s läuft (bei dauerhaft ungesund: `UPD_HEALTH_SECS=0` im Test setzen).
- Root öffnet nichts unter `$DATA_DIR`: DB-Sicherung und -Rückspielen laufen über `run_as mcpanel …` (`upd_db_backup`, `upd_db_restore`); im Trockenlauf protokolliert `run_as` nur, die Tests ersetzen beide Funktionen per `stub`.

- [ ] **Step 4: Lauf, grün**

Run: `make check` (Mac), dann `make test-vm SUITE=lint` (detached, Log pollen).
Expected: shellcheck ohne Befund für `update.sh` und `tests/ops_update.sh`; `test.sh` meldet alle `test_update_*` als ok.

- [ ] **Step 5: Commit**

```bash
git add scripts/install/update.sh scripts/install/tests/ops_update.sh
git commit -m "feat(installer): update with backup, health check and automatic rollback

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 9: `doctor`

**Files:**
- Modify: `scripts/install/doctor.sh` (Stub ersetzen)
- Create: `scripts/install/tests/ops_doctor.sh`

**Interfaces:**
- Consumes: V2-Helfer, Konstanten aus `lib.sh`, `version_ge`; aus Aufgabe 8 `upd_unfinished_dirs`, `upd_binary_versions`, `upd_latest_tag`; Variablen `JSON`, `DOC_SUMMARY` (neu, Default 0).
- Produces:
  - `cmd_doctor` → `return 0|1|2`.
  - `doc_result STATUS ID MSG [HINT]` mit `STATUS` ∈ `ok|warn|fail`.
  - Prüf-IDs (stabil, für `--json` und Phase C): `services`, `sockets`, `files_process`, `versions`, `update_state`, `update_available`, `config`, `permissions`, `caddy_config`, `caddy_import`, `certificate`, `dns`, `firewall`, `docker`, `restic`, `tzdata`, `time_sync`, `upgrades`, `storage_fs`, `storage_free`, `backup_keys`, `ssh_password`, `ssh_root`.
  - JSON: `{"status":"ok|warn|fail","checks":[{"id":…,"status":…,"message":…,"hint":…}]}`.

- [ ] **Step 0: Harness abgleichen** (wie Aufgabe 8, Step 0)

- [ ] **Step 1: Tests schreiben (rot)**

`scripts/install/tests/ops_doctor.sh`:

```bash
# shellcheck shell=bash
# Tests for doctor.sh (plan 03, task 9).

# ops_doctor_healthy sets every probe to a healthy system.
ops_doctor_healthy() {
	doc_probe_unit_active() { return 0; }
	doc_probe_stat() {
		case "$1" in
		/run/mc-panel-files/files.sock) echo "mcpanel-mc:mcpanel 660" ;;
		/run/mc-panel-web/panel.sock) echo "mcpanel:caddy 660" ;;
		/run/mc-panel/agent.sock) echo "root:mcpanel 660" ;;
		/etc/mc-panel) echo "root:mcpanel 750" ;;
		/etc/mc-panel-files) echo "root:mcpanel-mc 750" ;;
		/etc/mc-panel/panel.toml) echo "root:mcpanel 640" ;;
		/etc/mc-panel/agent.toml) echo "root:root 600" ;;
		/srv/mc-panel/instances) echo "root:mcpanel-mc 750" ;;
		/srv/mc-panel/backups) echo "mcpanel-mc:mcpanel-mc 700" ;;
		/etc/mc-panel-files/files.toml) echo "root:mcpanel-mc 640" ;;
		/var/lib/mc-panel) echo "mcpanel:mcpanel 700" ;;
		*) echo "root:root 755" ;;
		esac
	}
	doc_probe_files_creds() { echo "ok"; }
	upd_binary_versions() { echo "1.2.0 1.2.0 1.2.0"; }
	upd_latest_tag() { echo v1.2.0; }
	doc_probe_caddy_validate() { return 0; }
	doc_probe_cert_days() { echo 60; }
	doc_probe_resolve() { echo "203.0.113.5"; }
	doc_probe_local_addrs() { printf '127.0.0.1\n203.0.113.5\n'; }
	doc_probe_nft_table() { printf 'table inet mcpanel_fw {\n tcp dport { 22, 80, 443, 25565 } accept\n}\n'; }
	doc_probe_ssh_port() { echo 22; }
	doc_probe_docker() { return 0; }
	doc_probe_restic_version() { echo "0.18.1"; }
	doc_probe_pkg() { return 0; }
	doc_probe_ntp() { echo yes; }
	doc_probe_unit_enabled() { return 0; }
	doc_probe_mountpoint() { echo /srv/mc-panel; }
	doc_probe_free_percent() { echo 40; }
	doc_probe_sshd() { printf 'passwordauthentication no\npermitrootlogin prohibit-password\n'; }
	mkdir -p "$ROOT$ETC_PANEL" "$ROOT$ETC_FILES" "$ROOT$INSTANCES_DIR/backups/keys" "$ROOT$CADDY_DIR"
	printf 'listen = "unix:/run/mc-panel-web/panel.sock"\npublic_url = "https://panel.example.com"\ndata_dir = "/var/lib/mc-panel"\nagent_socket = "/run/mc-panel/agent.sock"\nfiles_socket = "/run/mc-panel-files/files.sock"\ntrusted_proxies = ["127.0.0.1/32", "::1/128"]\nmonitoring_ping_addr = "127.0.0.1:25565"\ninstances_dir = "/srv/mc-panel"\n' >"$ROOT$ETC_PANEL/panel.toml"
	printf 'socket = "/run/mc-panel/agent.sock"\npanel_uid = 998\nfirewall = "nftables"\ninstances_dir = "/srv/mc-panel"\nmc_user = "mcpanel-mc"\n' >"$ROOT$ETC_PANEL/agent.toml"
	printf 'socket = "/run/mc-panel-files/files.sock"\npanel_uid = 998\ninstances_dir = "/srv/mc-panel"\nrestic_binary = "/usr/bin/restic"\n' >"$ROOT$ETC_FILES/files.toml"
	printf 'panel.example.com {\n\ttls you@example.com\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
	printf ':80 {\n}\nimport mc-panel.caddy # managed by mc-panel\n' >"$ROOT$CADDY_DIR/Caddyfile"
}

test_doctor_required_keys_match_install() {
	# doctor checks exactly the keys install adds (config.sh CFG_REQUIRED_*, review #15)
	stub id 'echo 998'
	DOMAIN=panel.example.com TLS_MODE=internal PKG_RESTIC_BIN=/usr/bin/restic
	cfg_keys
	assert_eq "$(printf '%s\n' "${!CFG_REQUIRED_PANEL[@]}" | sort | xargs)" "$(printf '%s\n' $DOC_REQ_PANEL | sort | xargs)"
	assert_eq "$(printf '%s\n' "${!CFG_REQUIRED_AGENT[@]}" | sort | xargs)" "$(printf '%s\n' $DOC_REQ_AGENT | sort | xargs)"
	assert_eq "$(printf '%s\n' "${!CFG_REQUIRED_FILES[@]}" | sort | xargs)" "$(printf '%s\n' $DOC_REQ_FILES | sort | xargs)"
}

test_doctor_reports_missing_instances_dir_key() {
	ops_doctor_healthy
	grep -v '^instances_dir' "$ROOT$ETC_PANEL/panel.toml" >"$WORK/p" && mv "$WORK/p" "$ROOT$ETC_PANEL/panel.toml"
	local rc=0 out
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 2 "$rc"
	assert_contains "$out" "instances_dir"
}

test_doctor_healthy_system_has_only_expected_warnings() {
	ops_doctor_healthy
	local rc=0 out
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 0 "$rc" "$out"
	assert_contains "$out" "✓"
	assert_not_contains "$out" "✗"
}

test_doctor_warns_on_shared_filesystem_and_ssh() {
	ops_doctor_healthy
	doc_probe_mountpoint() { echo /; }
	doc_probe_sshd() { printf 'passwordauthentication yes\npermitrootlogin yes\n'; }
	local rc=0 out
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 1 "$rc"
	assert_contains "$out" "own file system"
	assert_contains "$out" "PasswordAuthentication"
	assert_contains "$out" "PermitRootLogin"
}

test_doctor_fails_on_stopped_service() {
	ops_doctor_healthy
	doc_probe_unit_active() { [ "$1" != mcpanel-panel.service ]; }
	local rc=0 out
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 2 "$rc"
	assert_contains "$out" "mcpanel-panel.service"
	assert_contains "$out" "systemctl"
}

test_doctor_fails_on_socket_mode() {
	ops_doctor_healthy
	local orig
	orig="$(declare -f doc_probe_stat)"
	eval "${orig/doc_probe_stat/doc_probe_stat_orig}"
	doc_probe_stat() { [ "$1" = /run/mc-panel-files/files.sock ] && echo "mcpanel-mc:mcpanel 666" || doc_probe_stat_orig "$1"; }
	local out
	out="$(cmd_doctor 2>&1)" || true
	assert_contains "$out" "files.sock"
	assert_contains "$out" "666"
}

test_doctor_fails_on_missing_config_key() {
	ops_doctor_healthy
	sed -i '/public_url/d' "$ROOT$ETC_PANEL/panel.toml"
	local rc=0 out
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 2 "$rc"
	assert_contains "$out" "public_url"
	assert_contains "$out" "install.sh install"
}

test_doctor_reports_version_mismatch_and_newer_release() {
	ops_doctor_healthy
	upd_binary_versions() { echo "1.2.0 1.1.0 1.2.0"; }
	upd_latest_tag() { echo v1.3.0; }
	local out
	out="$(cmd_doctor 2>&1)" || true
	assert_contains "$out" "1.1.0"
	assert_contains "$out" "1.3.0"
}

test_doctor_certificate_thresholds() {
	ops_doctor_healthy
	doc_probe_cert_days() { echo 10; }
	local out
	out="$(cmd_doctor 2>&1)" || true
	assert_contains "$out" "10 days"
	doc_probe_cert_days() { echo -1; }
	local rc=0
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 2 "$rc"
}

test_doctor_firewall_without_ssh_port_fails() {
	ops_doctor_healthy
	doc_probe_ssh_port() { echo 2222; }
	local rc=0
	cmd_doctor >/dev/null 2>&1 || rc=$?
	assert_eq 2 "$rc"
}

test_doctor_unfinished_update_fails() {
	ops_doctor_healthy
	upd_unfinished_dirs() { echo "$ROOT$ROLLBACK_DIR/1.0.0-20261013T000000Z"; }
	local rc=0 out
	out="$(cmd_doctor 2>&1)" || rc=$?
	assert_eq 2 "$rc"
	assert_contains "$out" "install.sh update"
}

test_doctor_json_is_valid_and_complete() {
	ops_doctor_healthy
	doc_probe_sshd() { printf 'passwordauthentication yes\n'; }
	JSON=1
	local out
	out="$(cmd_doctor 2>/dev/null)" || true
	assert_contains "$out" '"status":"warn"'
	assert_contains "$out" '"id":"ssh_password"'
	assert_contains "$out" '"id":"services"'
	if command -v python3 >/dev/null; then
		printf '%s' "$out" | python3 -I -c 'import json,sys; d=json.load(sys.stdin); assert len(d["checks"]) >= 20'
	fi
}

test_doctor_json_escapes_messages() {
	assert_eq '"a \"quoted\"\\ path"' "$(doc_json_str 'a "quoted"\ path')"
}

test_doctor_summary_prints_only_failures() {
	ops_doctor_healthy
	doc_probe_docker() { return 1; }
	DOC_SUMMARY=1
	local out
	out="$(cmd_doctor 2>&1)" || true
	assert_contains "$out" "✗"
	assert_not_contains "$out" "✓ restic"
}
```

Hinweis: `test_doctor_fails_on_socket_mode` nutzt `eval` nur im Test, um die Original-Probe zu behalten; falls das Harness einen Helfer zum Umbenennen von Funktionen bietet, diesen nehmen (Global Constraint „kein eval“ gilt für Produktionscode; shellcheck-Ausnahme im Test mit Begründung).

- [ ] **Step 2: Lauf, rot**

Run: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/installer-ops && bash scripts/install/test.sh tests/ops_doctor.sh'`
Expected: FAIL – Stub.

- [ ] **Step 3: `doctor.sh` schreiben**

```bash
# shellcheck shell=bash
# doctor.sh – `install.sh doctor` (spec §6): ✓ / ! / ✗ per check with a one-line hint;
# exit code 0 (all ok), 1 (warnings), 2 (failures); --json for scripts.

DOC_IDS=()
DOC_STATUS=()
DOC_MSG=()
DOC_HINT=()
DOC_SUMMARY="${DOC_SUMMARY:-0}"
DOC_UNITS=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service caddy.service)

doc_result() {
	DOC_IDS+=("$2")
	DOC_STATUS+=("$1")
	DOC_MSG+=("$3")
	DOC_HINT+=("${4:-}")
}

# --- probes (tests redefine these) -------------------------------------------------------

doc_probe_unit_active() { systemctl is-active --quiet "$1"; }
doc_probe_unit_enabled() { systemctl is-enabled --quiet "$1"; }
doc_probe_stat() { stat -c '%U:%G %a' "$1" 2>/dev/null || echo "missing"; }
doc_probe_files_creds() {
	local pid uid status
	pid="$(systemctl show -p MainPID --value mcpanel-files 2>/dev/null)"
	[ -n "$pid" ] && [ "$pid" != 0 ] || { echo "not running"; return; }
	uid="$(id -u mcpanel-mc)"
	status="$(cat "/proc/$pid/status")"
	if ! grep -qxP "Uid:\t$uid\t$uid\t$uid\t$uid" <<<"$status"; then echo "wrong uid"; return; fi
	if ! grep -qxP 'CapEff:\t0000000000000000' <<<"$status"; then echo "has capabilities"; return; fi
	if ! grep -qxP 'NoNewPrivs:\t1' <<<"$status"; then echo "no_new_privs missing"; return; fi
	if grep -P '^Groups:' <<<"$status" | grep -qw "$(getent group mcpanel | cut -d: -f3)"; then echo "member of mcpanel"; return; fi
	echo "ok"
}
doc_probe_caddy_validate() { caddy validate --config "$CADDY_DIR/Caddyfile" --adapter caddyfile >/dev/null 2>&1; }
# doc_probe_cert_days DOMAIN: days until the certificate Caddy stores for DOMAIN expires
# (negative = expired), empty if none was found.
doc_probe_cert_days() {
	local crt end
	crt="$(find /var/lib/caddy/.local/share/caddy/certificates -name "$1.crt" 2>/dev/null | head -n 1)"
	[ -n "$crt" ] || return 0
	end="$(openssl x509 -enddate -noout -in "$crt" 2>/dev/null | cut -d= -f2)" || return 0
	[ -n "$end" ] || return 0
	echo $((($(date -d "$end" +%s) - $(date +%s)) / 86400))
}
doc_probe_resolve() { getent ahosts "$1" 2>/dev/null | awk '{print $1}' | sort -u; }
doc_probe_local_addrs() { ip -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1; }
doc_probe_nft_table() { nft list table inet mcpanel_fw 2>/dev/null; }
doc_probe_ssh_port() { sshd -T 2>/dev/null | awk '$1 == "port" {print $2; exit}'; }
doc_probe_docker() { docker info >/dev/null 2>&1; }
doc_probe_restic_version() { restic version 2>/dev/null | awk '{print $2}'; }
doc_probe_pkg() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'; }
doc_probe_ntp() { timedatectl show -p NTPSynchronized --value 2>/dev/null; }
doc_probe_mountpoint() { findmnt -n -o TARGET --target "$INSTANCES_DIR" 2>/dev/null; }
doc_probe_free_percent() { df --output=pcent "$INSTANCES_DIR" 2>/dev/null | tail -n 1 | tr -dc '0-9' | awk '{print 100 - $1}'; }
doc_probe_sshd() { sshd -T 2>/dev/null; }

# --- helpers -----------------------------------------------------------------------------

# doc_toml_value FILE KEY prints the raw value of a top-level KEY (quotes removed).
doc_toml_value() {
	sed -n "s/^[[:space:]]*$2[[:space:]]*=[[:space:]]*//p" "$1" 2>/dev/null | head -n 1 | sed 's/^"\(.*\)"$/\1/'
}

doc_domain() {
	local url
	url="$(doc_toml_value "$ROOT$ETC_PANEL/panel.toml" public_url)"
	url="${url#https://}"
	url="${url#http://}"
	echo "${url%%[:/]*}"
}

doc_json_str() {
	local s="$1"
	s="${s//\\/\\\\}"
	s="${s//\"/\\\"}"
	s="${s//$'\n'/\\n}"
	s="${s//$'\t'/\\t}"
	printf '"%s"' "$s"
}

# --- checks ------------------------------------------------------------------------------

doc_chk_services() {
	local u bad=()
	for u in "${DOC_UNITS[@]}"; do
		doc_probe_unit_active "$u" || bad+=("$u")
	done
	if [ ${#bad[@]} -eq 0 ]; then
		doc_result ok services "all mc-panel services are running"
	else
		doc_result fail services "not running: ${bad[*]}" "systemctl status ${bad[*]}; journalctl -u ${bad[0]}"
	fi
}

doc_chk_sockets() {
	local want=("/run/mc-panel-files/files.sock|mcpanel-mc:mcpanel 660" "/run/mc-panel-web/panel.sock|mcpanel:caddy 660") e path exp got bad=()
	for e in "${want[@]}"; do
		path="${e%%|*}"
		exp="${e#*|}"
		got="$(doc_probe_stat "$path")"
		[ "$got" = "$exp" ] || bad+=("$path is $got (expected $exp)")
	done
	if [ ${#bad[@]} -eq 0 ]; then
		doc_result ok sockets "sockets have the expected owner and mode"
	else
		doc_result fail sockets "${bad[*]}" "systemctl restart mcpanel-files.socket mcpanel-panel.socket (the socket units set owner and mode)"
	fi
}

doc_chk_files_process() {
	local got
	got="$(doc_probe_files_creds)"
	if [ "$got" = ok ]; then
		doc_result ok files_process "files service runs as mcpanel-mc without capabilities"
	else
		doc_result fail files_process "files service: $got" "reinstall the unit: install.sh install, then systemctl restart mcpanel-files"
	fi
}

doc_chk_versions() {
	local v p a f
	v="$(upd_binary_versions)"
	read -r p a f <<<"$v"
	if [ "$p" = "$a" ] && [ "$a" = "$f" ] && [ "$p" != "?" ]; then
		doc_result ok versions "panel, agent and files are $p"
	else
		doc_result fail versions "versions differ: panel $p, agent $a, files $f" "install.sh update --force"
	fi
	local latest
	if latest="$(upd_latest_tag 2>/dev/null)" && [ -n "$latest" ]; then
		if version_ge "$p" "$latest"; then
			doc_result ok update_available "no newer release"
		else
			doc_result warn update_available "release ${latest#v} is available (installed $p)" "install.sh update"
		fi
	else
		doc_result warn update_available "could not check for a newer release (no network?)"
	fi
}

doc_chk_update_state() {
	local dirs
	dirs="$(upd_unfinished_dirs)"
	if [ -z "$dirs" ]; then
		doc_result ok update_state "no unfinished update"
	else
		doc_result fail update_state "unfinished update or failed rollback: $(basename -a $dirs | tr '\n' ' ')" "install.sh update (rolls an unfinished update back first); see $LOG_FILE"
	fi
}

# Required keys: the same lists install adds (config.sh, CFG_REQUIRED_*; a test compares them).
DOC_REQ_PANEL="listen public_url data_dir agent_socket files_socket trusted_proxies monitoring_ping_addr instances_dir"
DOC_REQ_AGENT="socket panel_uid firewall instances_dir mc_user"
DOC_REQ_FILES="socket panel_uid instances_dir restic_binary"

doc_chk_config() {
	local f k missing=()
	declare -A req=(
		["$ROOT$ETC_PANEL/panel.toml"]="$DOC_REQ_PANEL"
		["$ROOT$ETC_PANEL/agent.toml"]="$DOC_REQ_AGENT"
		["$ROOT$ETC_FILES/files.toml"]="$DOC_REQ_FILES"
	)
	for f in "${!req[@]}"; do
		if [ ! -f "$f" ]; then
			missing+=("${f#"$ROOT"} (file)")
			continue
		fi
		for k in ${req[$f]}; do
			grep -qE "^[[:space:]]*$k[[:space:]]*=" "$f" || missing+=("${f#"$ROOT"}: $k")
		done
	done
	if [ ${#missing[@]} -eq 0 ]; then
		doc_result ok config "configuration complete"
	else
		doc_result fail config "missing: ${missing[*]}" "install.sh install (adds missing keys without changing existing ones)"
	fi
}

doc_chk_permissions() {
	local want=(
		"$ETC_PANEL|root:mcpanel 750" "$ETC_FILES|root:mcpanel-mc 750"
		"$ETC_PANEL/panel.toml|root:mcpanel 640" "$ETC_PANEL/agent.toml|root:root 600"
		"$ETC_FILES/files.toml|root:mcpanel-mc 640" "$DATA_DIR|mcpanel:mcpanel 700"
		"$INSTANCES_DIR/instances|root:mcpanel-mc 750" "$INSTANCES_DIR/backups|mcpanel-mc:mcpanel-mc 700"
	) e path exp got bad=()
	for e in "${want[@]}"; do
		path="${e%%|*}"
		exp="${e#*|}"
		got="$(doc_probe_stat "$path")"
		[ "$got" = "$exp" ] || bad+=("$path is $got (expected $exp)")
	done
	if [ ${#bad[@]} -eq 0 ]; then
		doc_result ok permissions "configuration and data permissions as expected"
	else
		doc_result fail permissions "${bad[*]}" "install.sh install (resets owners and modes)"
	fi
}

doc_chk_caddy() {
	if doc_probe_caddy_validate; then
		doc_result ok caddy_config "Caddy configuration is valid"
	else
		doc_result fail caddy_config "Caddy configuration is invalid" "caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile"
	fi
	if grep -qxF 'import mc-panel.caddy # managed by mc-panel' "$ROOT$CADDY_DIR/Caddyfile" 2>/dev/null && [ -f "$ROOT$CADDY_DIR/mc-panel.caddy" ]; then
		doc_result ok caddy_import "Caddy serves the panel site"
	else
		doc_result fail caddy_import "the panel site is not imported in /etc/caddy/Caddyfile" "install.sh install"
	fi
	local domain days
	domain="$(doc_domain)"
	if grep -q 'tls internal' "$ROOT$CADDY_DIR/mc-panel.caddy" 2>/dev/null; then
		doc_result ok certificate "local certificate (tls internal)"
	else
		days="$(doc_probe_cert_days "$domain")"
		if [ -z "$days" ]; then
			doc_result fail certificate "no certificate for $domain yet" "check DNS and port 80, then journalctl -u caddy"
		elif [ "$days" -lt 0 ]; then
			doc_result fail certificate "the certificate for $domain has expired" "journalctl -u caddy (renewal failed?)"
		elif [ "$days" -lt 14 ]; then
			doc_result warn certificate "the certificate for $domain expires in $days days" "journalctl -u caddy (renewal should start 30 days before)"
		else
			doc_result ok certificate "certificate for $domain valid for $days days"
		fi
	fi
	local addrs locals a match=0
	addrs="$(doc_probe_resolve "$domain")"
	locals="$(doc_probe_local_addrs)"
	for a in $addrs; do
		grep -qxF "$a" <<<"$locals" && match=1
	done
	if [ "$match" = 1 ]; then
		doc_result ok dns "$domain points to this host"
	else
		doc_result warn dns "$domain resolves to ${addrs:-nothing}, not to this host" "set the A/AAAA record of $domain to this server"
	fi
}

doc_chk_firewall() {
	local table port
	table="$(doc_probe_nft_table)"
	if [ -z "$table" ]; then
		doc_result warn firewall "host firewall (table inet mcpanel_fw) is not loaded" "install.sh install (without --no-firewall)"
		return
	fi
	port="$(doc_probe_ssh_port)"
	if grep -Eq "(^|[^0-9])${port:-22}([^0-9]|$)" <<<"$table"; then
		doc_result ok firewall "host firewall active, SSH port ${port:-22} allowed"
	else
		doc_result fail firewall "host firewall does not allow the SSH port ${port:-22}" "install.sh install --ssh-port ${port:-22}"
	fi
}

doc_chk_system() {
	if doc_probe_docker; then
		doc_result ok docker "Docker is reachable"
	else
		doc_result fail docker "Docker is not reachable" "systemctl status docker"
	fi
	local rv
	rv="$(doc_probe_restic_version)"
	if [ -n "$rv" ] && version_ge "$rv" 0.17.0; then
		doc_result ok restic "restic $rv"
	else
		doc_result fail restic "restic ${rv:-missing} (needs 0.17 or newer)" "apt-get install restic"
	fi
	if doc_probe_pkg tzdata; then
		doc_result ok tzdata "tzdata installed"
	else
		doc_result fail tzdata "tzdata is missing (backup retention would use UTC)" "apt-get install tzdata"
	fi
	if [ "$(doc_probe_ntp)" = yes ]; then
		doc_result ok time_sync "system clock is synchronized"
	else
		doc_result warn time_sync "system clock is not synchronized (TOTP and schedules need it)" "timedatectl set-ntp true"
	fi
	if doc_probe_unit_enabled unattended-upgrades.service; then
		doc_result ok upgrades "security updates are installed automatically"
	else
		doc_result warn upgrades "unattended-upgrades is not enabled" "install.sh install"
	fi
}

doc_chk_storage() {
	local mp free
	mp="$(doc_probe_mountpoint)"
	if [ "$mp" = "$INSTANCES_DIR" ]; then
		doc_result ok storage_fs "$INSTANCES_DIR is its own file system"
	else
		doc_result warn storage_fs "$INSTANCES_DIR is not its own file system (shared with $mp)" "a full Minecraft server can then fill the panel's disk; see spec TP2 §3.3"
	fi
	free="$(doc_probe_free_percent)"
	if [ -n "$free" ] && [ "$free" -lt 10 ]; then
		doc_result warn storage_free "only $free % free on $INSTANCES_DIR" "remove old backups or worlds"
	else
		doc_result ok storage_free "${free:-?} % free on $INSTANCES_DIR"
	fi
	if [ -d "$ROOT$INSTANCES_DIR/backups/keys" ]; then
		doc_result ok backup_keys "backup keys present (include $INSTANCES_DIR/backups/keys in your host backups)"
	else
		doc_result ok backup_keys "no backup keys yet"
	fi
}

doc_chk_ssh() {
	local cfg
	cfg="$(doc_probe_sshd)"
	if grep -qi '^passwordauthentication yes' <<<"$cfg"; then
		doc_result warn ssh_password "SSH allows password logins (PasswordAuthentication yes)" "use SSH keys and set PasswordAuthentication no"
	else
		doc_result ok ssh_password "SSH password logins are off"
	fi
	if grep -qi '^permitrootlogin yes' <<<"$cfg"; then
		doc_result warn ssh_root "SSH allows root logins with a password (PermitRootLogin yes)" "set PermitRootLogin prohibit-password or no"
	else
		doc_result ok ssh_root "SSH root password login is off"
	fi
}

# --- output ------------------------------------------------------------------------------

doc_print() {
	local i n=${#DOC_IDS[@]} worst=ok fails=0 warns=0 sym
	for ((i = 0; i < n; i++)); do
		case "${DOC_STATUS[$i]}" in
		fail) fails=$((fails + 1)); worst=fail ;;
		warn) warns=$((warns + 1)); [ "$worst" = fail ] || worst=warn ;;
		esac
	done
	if [ "${JSON:-0}" = 1 ]; then
		printf '{"status":"%s","checks":[' "$worst"
		for ((i = 0; i < n; i++)); do
			[ "$i" = 0 ] || printf ','
			printf '{"id":%s,"status":%s,"message":%s,"hint":%s}' \
				"$(doc_json_str "${DOC_IDS[$i]}")" "$(doc_json_str "${DOC_STATUS[$i]}")" \
				"$(doc_json_str "${DOC_MSG[$i]}")" "$(doc_json_str "${DOC_HINT[$i]}")"
		done
		printf ']}\n'
	else
		for ((i = 0; i < n; i++)); do
			case "${DOC_STATUS[$i]}" in ok) sym="✓" ;; warn) sym="!" ;; *) sym="✗" ;; esac
			if [ "$DOC_SUMMARY" = 1 ] && [ "${DOC_STATUS[$i]}" = ok ]; then
				continue
			fi
			printf '%s %s\n' "$sym" "${DOC_MSG[$i]}"
			[ -z "${DOC_HINT[$i]}" ] || [ "${DOC_STATUS[$i]}" = ok ] || printf '    → %s\n' "${DOC_HINT[$i]}"
		done
		printf '%d checks: %d failed, %d warnings\n' "$n" "$fails" "$warns"
	fi
	case "$worst" in ok) return 0 ;; warn) return 1 ;; *) return 2 ;; esac
}

cmd_doctor() {
	DOC_IDS=()
	DOC_STATUS=()
	DOC_MSG=()
	DOC_HINT=()
	[ "${JSON:-0}" = 1 ] || log_step "doctor"
	doc_chk_services
	doc_chk_sockets
	doc_chk_files_process
	doc_chk_versions
	doc_chk_update_state
	doc_chk_config
	doc_chk_permissions
	doc_chk_caddy
	doc_chk_firewall
	doc_chk_system
	doc_chk_storage
	doc_chk_ssh
	doc_print
}
```

Anmerkungen:
- `basename -a $dirs` in `doc_chk_update_state` bewusst ungequotet (Liste von Pfaden ohne Leerzeichen, eigene Namen); shellcheck SC2086 dort gezielt mit Begründung ausnehmen oder mit `mapfile` + Schleife ersetzen (vorzuziehen).
- `doc_probe_free_percent` rechnet `100 - pcent` (Belegung) um.
- Ohne root (`doctor --json` ohne root, V4) liefern einige Probes nichts; die Prüfungen melden dann `warn` statt `fail` nur bei `files_process` und `certificate` („needs root“) – ergänze in `doc_chk_files_process` und `doc_chk_caddy` je eine Abfrage `[ "$(id -u)" = 0 ] || { doc_result warn … "run as root for this check"; return; }` (mit Test `test_doctor_without_root_warns`, Probe `doc_probe_euid` statt `id -u`).

- [ ] **Step 4: Lauf, grün** – wie Aufgabe 8 Step 4.

- [ ] **Step 5: Commit**

```bash
git add scripts/install/doctor.sh scripts/install/tests/ops_doctor.sh
git commit -m "feat(installer): doctor with hints, JSON output and exit codes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 10: `uninstall` und `--purge`

**Files:**
- Modify: `scripts/install/uninstall.sh` (Stub ersetzen)
- Create: `scripts/install/tests/ops_uninstall.sh`

**Interfaces:**
- Consumes: V2 (`sys_systemctl`, `sys_nft`, `sys_docker`, `sys_caddy`, `sys_useradd` – für `--purge` braucht es `userdel`: Probe `uni_userdel` ruft `userdel` bzw. einen `sys_userdel`-Wrapper, falls Phase A ihn liefert), `confirm`, Konstanten; aus Aufgabe 8 `UPD_UNITS`, `UPD_BINARIES`; aus Aufgabe 9 `doc_domain`.
- Produces: `cmd_uninstall` → `return 0` (entfernt) bzw. `1` (abgebrochen/Fehler).

- [ ] **Step 0: Harness abgleichen** (wie Aufgabe 8)

- [ ] **Step 1: Tests schreiben (rot)**

`scripts/install/tests/ops_uninstall.sh`:

```bash
# shellcheck shell=bash
# Tests for uninstall.sh (plan 03, task 10).

ops_uninstall_system() {
	local u b
	mkdir -p "$ROOT$BIN_DIR" "$ROOT$UNIT_DIR" "$ROOT$CADDY_DIR" "$ROOT$ETC_PANEL" "$ROOT$ETC_FILES" "$ROOT$DATA_DIR" "$ROOT$ROLLBACK_DIR" "$ROOT$INSTANCES_DIR/instances/1"
	for b in panel agent files; do ops_fake_bin "$ROOT$BIN_DIR/$b" "echo 1.0.0"; done
	ops_fake_bin "$ROOT$BIN_DIR/other-tool" "echo other"
	for u in "${UPD_UNITS[@]}"; do echo "# $u" >"$ROOT$UNIT_DIR/$u"; done
	echo "# foreign" >"$ROOT$UNIT_DIR/nginx.service"
	printf 'other.example.org {\n\trespond "hi"\n}\nimport mc-panel.caddy # managed by mc-panel\n' >"$ROOT$CADDY_DIR/Caddyfile"
	printf 'panel.example.com {\n}\n' >"$ROOT$CADDY_DIR/mc-panel.caddy"
	printf 'public_url = "https://panel.example.com"\n' >"$ROOT$ETC_PANEL/panel.toml"
	echo world >"$ROOT$INSTANCES_DIR/instances/1/level.dat"
	echo db >"$ROOT$DATA_DIR/panel.db"
	uni_probe_containers() { printf 'abc123\ndef456\n'; }
	uni_probe_has_router() { return 0; }
	uni_probe_has_network() { return 0; }
	uni_probe_has_table() { return 0; }
	uni_userdel() { echo "userdel $*" >>"$WORK/calls.log"; }
}

test_uninstall_removes_program_and_keeps_data() {
	ops_uninstall_system
	YES=1
	cmd_uninstall
	assert_eq 0 "$?"
	assert_no_file "$ROOT$BIN_DIR/panel"
	assert_no_file "$ROOT$UNIT_DIR/mcpanel-panel.service"
	assert_no_file "$ROOT$CADDY_DIR/mc-panel.caddy"
	assert_not_contains "$(cat "$ROOT$CADDY_DIR/Caddyfile")" "mc-panel.caddy"
	assert_called 'systemctl stop mcpanel-panel.service'
	assert_called 'systemctl disable mcpanel-agent.service'
	assert_called 'nft delete table inet mcpanel_fw'
	assert_called 'nft delete table inet mcpanel'
	assert_called 'docker rm -f abc123 def456'
	assert_called 'docker rm -f mcpanel-router'
	assert_called 'docker network rm mcpanel-mc'
	assert_called 'systemctl reload caddy'
	assert_file "$ROOT$INSTANCES_DIR/instances/1/level.dat"
	assert_file "$ROOT$DATA_DIR/panel.db"
	assert_file "$ROOT$ETC_PANEL/panel.toml"
	assert_not_called 'userdel'
}

test_uninstall_leaves_foreign_objects() {
	ops_uninstall_system
	YES=1
	cmd_uninstall
	assert_file "$ROOT$BIN_DIR/other-tool"
	assert_file "$ROOT$UNIT_DIR/nginx.service"
	assert_contains "$(cat "$ROOT$CADDY_DIR/Caddyfile")" "other.example.org"
	assert_not_called 'apt-get'
	assert_not_called 'docker rm -f foreign'
	assert_not_called 'nft flush ruleset'
	assert_not_called 'nft delete ruleset'
}

test_uninstall_without_yes_asks_and_can_abort() {
	ops_uninstall_system
	confirm() { return 1; }
	local rc=0
	cmd_uninstall || rc=$?
	assert_eq 1 "$rc"
	assert_file "$ROOT$BIN_DIR/panel"
	assert_not_called 'systemctl'
}

test_uninstall_is_idempotent() {
	ops_uninstall_system
	YES=1
	cmd_uninstall
	uni_probe_containers() { :; }
	uni_probe_has_router() { return 1; }
	uni_probe_has_network() { return 1; }
	uni_probe_has_table() { return 1; }
	: >"$WORK/calls.log"
	cmd_uninstall
	assert_eq 0 "$?"
	assert_not_called 'docker rm'
	assert_not_called 'nft delete'
}

test_purge_needs_typed_domain_even_with_yes() {
	ops_uninstall_system
	YES=1
	PURGE=1
	uni_read_tty() { echo "yes"; }
	local rc=0
	cmd_uninstall || rc=$?
	assert_eq 1 "$rc"
	assert_file "$ROOT$INSTANCES_DIR/instances/1/level.dat"
	assert_file "$ROOT$BIN_DIR/panel"
}

test_purge_with_typed_domain_removes_data_and_users() {
	ops_uninstall_system
	PURGE=1
	uni_read_tty() { echo "panel.example.com"; }
	cmd_uninstall
	assert_eq 0 "$?"
	assert_no_file "$ROOT$INSTANCES_DIR"
	assert_no_file "$ROOT$DATA_DIR"
	assert_no_file "$ROOT$ROLLBACK_DIR"
	assert_no_file "$ROOT$ETC_PANEL"
	assert_no_file "$ROOT$ETC_FILES"
	assert_called 'userdel mcpanel-mc'
	assert_called 'userdel mcpanel'
}

test_purge_without_tty_aborts() {
	ops_uninstall_system
	PURGE=1
	YES=1
	uni_read_tty() { return 1; }
	local rc=0
	cmd_uninstall || rc=$?
	assert_eq 1 "$rc"
	assert_file "$ROOT$DATA_DIR/panel.db"
}

test_purge_confirm_word_without_domain() {
	ops_uninstall_system
	rm -f "$ROOT$ETC_PANEL/panel.toml"
	assert_eq "mc-panel" "$(uni_purge_word)"
}
```

- [ ] **Step 2: Lauf, rot** – wie Aufgabe 8.

- [ ] **Step 3: `uninstall.sh` schreiben**

```bash
# shellcheck shell=bash
# uninstall.sh – `install.sh uninstall [--purge]` (spec §7): removes mc-panel's units,
# binaries, Caddy site, firewall tables and containers; data stays unless --purge, which
# needs the panel domain typed in (also with --yes).

UNI_IMPORT_LINE='import mc-panel.caddy # managed by mc-panel'

# --- probes (tests redefine these) -------------------------------------------------------

uni_probe_containers() { docker ps -aq --filter label=mcpanel.instance 2>/dev/null; }
uni_probe_has_router() { docker container inspect mcpanel-router >/dev/null 2>&1; }
uni_probe_has_network() { docker network inspect mcpanel-mc >/dev/null 2>&1; }
uni_probe_has_table() { nft list table inet "$1" >/dev/null 2>&1; }
uni_userdel() { sys_userdel "$1"; }
# uni_read_tty prints one line typed on the terminal; fails without a terminal.
uni_read_tty() {
	local line
	[ -r "$TTY" ] || return 1
	IFS= read -r line <"$TTY" || return 1
	echo "$line"
}

# --- steps -------------------------------------------------------------------------------

uni_purge_word() {
	local d
	d="$(doc_domain 2>/dev/null)"
	echo "${d:-mc-panel}"
}

# uni_confirm: plain uninstall asks once (or YES=1); --purge always needs the word typed.
uni_confirm() {
	if [ "$PURGE" = 1 ]; then
		local word typed
		word="$(uni_purge_word)"
		warn "--purge deletes all worlds, backups, backup keys, the panel database and the configuration."
		printf 'Type "%s" to confirm: ' "$word" >&2
		typed="$(uni_read_tty)" || { warn "no terminal to confirm --purge; nothing was removed"; return 1; }
		[ "$typed" = "$word" ] || { warn "confirmation did not match; nothing was removed"; return 1; }
		return 0
	fi
	confirm "Remove mc-panel (worlds, backups, database and configuration stay)?"
}

uni_units() {
	local u
	for u in mcpanel-panel.service mcpanel-panel.socket mcpanel-files.service mcpanel-files.socket mcpanel-agent.service mcpanel-fw.service; do
		[ -f "$ROOT$UNIT_DIR/$u" ] || continue
		sys_systemctl stop "$u" || true
		sys_systemctl disable "$u" || true
		rm -f -- "$ROOT$UNIT_DIR/$u"
		log_changed "removed $u"
	done
	sys_systemctl daemon-reload
}

uni_binaries() {
	local b
	for b in "${UPD_BINARIES[@]}"; do
		[ -e "$ROOT$BIN_DIR/$b" ] || continue
		rm -f -- "$ROOT$BIN_DIR/$b"
		log_changed "removed $BIN_DIR/$b"
	done
}

uni_caddy() {
	local file="$ROOT$CADDY_DIR/Caddyfile" changed=0
	if [ -f "$file" ] && grep -qxF "$UNI_IMPORT_LINE" "$file"; then
		grep -vxF "$UNI_IMPORT_LINE" "$file" >"$file.tmp" || true
		mv -f -- "$file.tmp" "$file"
		changed=1
	fi
	if [ -f "$ROOT$CADDY_DIR/mc-panel.caddy" ]; then
		rm -f -- "$ROOT$CADDY_DIR/mc-panel.caddy"
		changed=1
	fi
	if [ "$changed" = 1 ]; then
		sys_systemctl reload caddy || warn "caddy reload failed; check /etc/caddy/Caddyfile"
		log_changed "removed the panel site from Caddy"
	fi
}

uni_firewall() {
	local t
	for t in mcpanel_fw mcpanel; do
		uni_probe_has_table "$t" || continue
		sys_nft delete table inet "$t"
		log_changed "removed nftables table inet $t"
	done
}

uni_docker() {
	local ids
	ids="$(uni_probe_containers | tr '\n' ' ')"
	ids="${ids% }"
	if [ -n "$ids" ]; then
		# shellcheck disable=SC2086 # ids are container IDs from docker ps -q, one word each
		sys_docker rm -f $ids
		log_changed "removed Minecraft containers"
	fi
	if uni_probe_has_router; then
		sys_docker rm -f mcpanel-router
		log_changed "removed mcpanel-router"
	fi
	if uni_probe_has_network; then
		sys_docker network rm mcpanel-mc
		log_changed "removed network mcpanel-mc"
	fi
}

uni_sockets() {
	rm -rf -- "$ROOT/run/mc-panel" "$ROOT/run/mc-panel-files" "$ROOT/run/mc-panel-web"
}

uni_purge() {
	local d
	for d in "$INSTANCES_DIR" "$DATA_DIR" "$ROLLBACK_DIR" "$ETC_PANEL" "$ETC_FILES"; do
		[ -e "$ROOT$d" ] || continue
		rm -rf -- "${ROOT:?}$d" 2>/dev/null || rm -rf -- "$ROOT$d"
		log_changed "deleted $d"
	done
	local u
	for u in mcpanel-mc mcpanel; do
		if [ -n "$ROOT" ] || id "$u" >/dev/null 2>&1; then
			uni_userdel "$u" || warn "could not delete user $u"
		fi
	done
}

cmd_uninstall() {
	log_step "uninstall"
	uni_confirm || return 1
	uni_units
	uni_binaries
	uni_caddy
	uni_firewall
	uni_docker
	uni_sockets
	if [ "$PURGE" = 1 ]; then
		uni_purge
		log_ok "mc-panel and all its data were removed"
	else
		log_ok "mc-panel was removed; worlds, backups, database and configuration are kept in $INSTANCES_DIR, $DATA_DIR and /etc/mc-panel*"
	fi
	return 0
}
```

Anmerkungen:
- Die Reihenfolge entfernt zuerst die Units (der agent darf die Container nicht neu anlegen, während sie entfernt werden), dann Docker-Objekte direkt.
- `rm -rf -- "${ROOT:?}$d"`: Die Variante mit `:?` schützt nur, wenn `ROOT` gesetzt sein *muss*; in Produktion ist `ROOT=""`, deshalb fällt die Zeile auf die zweite Form zurück. Besser für shellcheck: eine Hilfsfunktion `uni_rm_tree PATH`, die `PATH` gegen eine feste Liste erlaubter Ziele prüft (`/srv/mc-panel`, `/var/lib/mc-panel`, `/etc/mc-panel`, `/etc/mc-panel-files` mit `$ROOT`-Präfix) und sonst `die` – so umsetzen, mit Test `test_purge_refuses_unexpected_path` (ruft `uni_rm_tree /` → Exit 1).
- `uni_userdel` nutzt `sys_userdel` aus `lib.sh` (in Tests überschrieben).

- [ ] **Step 4: Lauf, grün** – wie Aufgabe 8.

- [ ] **Step 5: Commit**

```bash
git add scripts/install/uninstall.sh scripts/install/tests/ops_uninstall.sh
git commit -m "feat(installer): uninstall that keeps data unless --purge is confirmed

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

- [ ] **Step 6: Phase abschließen**

```bash
make check
make test-vm   # alle Suites, detached auf der VM, Log pollen
git push -u origin feat/installer-ops
gh pr create --draft --title "feat(installer): update, doctor and uninstall (sub-project 6)" --body "…Zusammenfassung, Abweichungen (Vertragsfragen 1–7), Testergebnisse…

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
gh pr checks <n> --watch
```

Dem Lead per SendMessage melden (PR-Nummer, CI, Abweichungen). Echte Läufe von `update`/`doctor`/`uninstall` prüft der CI-Job `installer` aus Phase C.

## Self-Review

- **Spec-Abdeckung:** §5 Schritte 1–6 → Aufgabe 8 (Zielversion, Übergabe, Sicherung inkl. 3 behalten, Austausch mit Reihenfolge agent → files → panel, Health 90 s mit Versionen und drei Endpunkten, Rollback mit DB, Exit 3/4); §6 alle Prüfgruppen → Aufgabe 9 (Dienste, Sockets, Prozess-Härtung, Versionen + neuere Release, Konfiguration + Rechte, Caddy + Zertifikat 14 Tage + DNS, Firewall + SSH-Port, Docker, restic, tzdata, Zeit, unattended-upgrades, Speicher, Schlüssel, SSH-Warnungen; `--json`; Exit 0/1/2); §7 → Aufgabe 10 (Objektliste, Daten bleiben, Pakete nie, `--purge` mit Abtippen auch bei `--yes`, Neuinstallation findet Daten – durch „Daten bleiben“ gegeben).
- **Review Focus 3/5:** Tests benannt im Abschnitt Review Focus.
- **Zusätzlich zur Spec:** Zustand `applying` wird beim nächsten `update` zuerst zurückgerollt und von `doctor` gemeldet (Stromausfall-Fall aus Review Focus 3); Platzprüfung vor der Sicherung.
- **Offen beim Lead:** Vertragsfragen 1–7.
