# mc-panel Teilprojekt 6: Installer – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ein Bash-Installer `install.sh` (`install`, `update`, `doctor`, `uninstall`) richtet mc-panel auf Ubuntu 26.04 vollständig ein, aktualisiert mit DB-Sicherung und Rollback, prüft den Zustand und entfernt das Panel wieder; die Test-VM und ein neuer CI-Job laufen über ihn.

**Architecture:** Quellen pro Schritt in `scripts/install/*.sh`, Vorlagen in `deploy/` (systemd, Caddy, nftables, TOML), zusammengefügt von `make installer` zu `build/install.sh` (Vorlagen als Heredocs eingebettet, Version eingetragen) und als Release-Asset veröffentlicht. Go-Änderungen nur dort, wo der Installer sie braucht: Socket-Aktivierung für das panel (gemeinsamer Code mit dem Dateidienst in `internal/unixsock`) und `panel admin list`.

**Tech Stack:** Bash 5 (`set -euo pipefail`), coreutils, `curl`, `sha256sum`, `systemctl`, `nft`, `caddy`, `sqlite3`; Go (Standardbibliothek); goreleaser; shellcheck (CI und VM). Keine neuen Go- oder npm-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-13-installer-00-overview.md` (diese) | – | Ausführung, Constraints, Review Focus, Dateistruktur, Verträge V1–V5 |
| `2026-10-13-installer-01-foundation.md` | 1–3 | Go: `internal/unixsock.Activated`, Socket-Aktivierung im panel, `panel admin list`; Vorlagen in `deploy/` (aus `scripts/testvm-deploy.sh` und den Specs), `lib.sh`, `main.sh` (Argumente, Unterbefehle, Logging), Test-Harness `test.sh`, `make installer`, goreleaser-Asset, shellcheck im Lint |
| `2026-10-13-installer-02-install.md` | 4–7 | Schritte von `install`: Vorprüfung, Pakete, Benutzer/Verzeichnisse, Binaries, Konfiguration, Units, Caddy, Firewall, Sicherheitsupdates, erster Admin |
| `2026-10-13-installer-03-ops.md` | 8–10 | `update` (Sicherung, Health-Check, Rollback), `doctor`, `uninstall` |
| `2026-10-13-installer-04-ci-docs.md` | 11–12 | `testvm-deploy.sh` auf den Installer umstellen, CI-Job `installer`, Doku, Checkliste `docs/dev/acceptance-tp6.md` |

## Ausführung

Wie TP4/TP5 (`CLAUDE.md`, Übersichten der TP4/TP5-Pläne): auf dem Mac nur `make check` (der Installer läuft auf dem Mac nie; `make check` bekommt `bash -n` für `scripts/install/*.sh`), Tests auf der Test-VM (`make test-vm`; Installer-Tests über `scripts/install/test.sh` im Lint-Suite-Lauf; echte Installationen nur in der CI und – nach Abstimmung mit dem Lead – auf der Test-VM), CI `CI OK` als Gate, ein Worktree pro Teammate, Mergen nur durch den Nutzer.

**Start erst nach TP5:** Der Installer schreibt die Konfiguration aller Dienste inklusive der Schlüssel aus TP4 (`restic_binary`, Backup-Grenzen) und TP5 (`monitoring_ping_addr`, `instances_dir`), und Phase A ändert `cmd/panel/main.go`, das TP5 Phase A ebenfalls ändert. Phase A startet, wenn TP5 vollständig gemerged ist.

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/installer-foundation` | Teammate „inst-foundation“ | 1–3 | TP5 gemerged |
| B1 | `feat/installer-install` | Teammate „inst-install“ | 4–7 | A gemerged |
| B2 | `feat/installer-ops` | Teammate „inst-ops“ | 8–10 | A gemerged |
| C | `feat/installer-ci-docs` | Teammate „inst-ci-docs“ | 11 (12 koordiniert der Lead) | B1 und B2 gemerged |

Dateieigentum (disjunkt):

- **A:** `internal/unixsock/**`, `cmd/files/main.go` (nur Umzug von `activatedListener`), `cmd/panel/**`, `internal/config/config.go` (nur falls `listen`-Validierung für Aktivierung nötig), `deploy/**`, `scripts/install/lib.sh`, `scripts/install/main.sh`, `scripts/install/test.sh`, `scripts/install/assemble.sh`, `scripts/install/.shellcheckrc`, die Stub-Dateien aller Schritte (B1/B2 ersetzen sie), `Makefile`, `.goreleaser.yaml`, `.gitignore` (`build/`), `.github/workflows/release.yml` (`make installer` vor goreleaser), `scripts/vm-test.sh` (shellcheck + `test.sh` in der Lint-Suite), `internal/config/deploy_test.go` (Vorlagen gegen die echten Parser), `.github/workflows/ci.yml` (Filter `installer`, Job `installer-lint`, `make installer` im Build-Job).
- **B1:** `scripts/install/preflight.sh`, `packages.sh`, `layout.sh`, `binaries.sh`, `config.sh`, `units.sh`, `caddy.sh`, `firewall.sh`, `upgrades.sh`, `admin.sh` und Testdateien `scripts/install/tests/install_*.sh`.
- **B2:** `scripts/install/update.sh`, `doctor.sh`, `uninstall.sh` und `scripts/install/tests/ops_*.sh`.
- **C:** `scripts/testvm-deploy.sh`, `scripts/ci-installer.sh` (neu), `.github/workflows/ci.yml` (neuer Job `installer` mit dem Filter `installer` aus Phase A – kein zweiter Filter; `ci-ok` braucht `installer-lint` und `installer`), `docs/**`, `README.md`, `CLAUDE.md`.

## Global Constraints

- Alle Constraints aus TP1–TP5 und `CLAUDE.md` gelten (Commit-Trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`; keine neuen Abhängigkeiten).
- Bash: jede Datei beginnt mit `# shellcheck shell=bash`, `set -euo pipefail` nur in `main.sh`; keine `eval`, keine ungequoteten Variablen, Funktionen klein-geschrieben mit Präfix je Datei (`pre_`, `pkg_`, `lay_`, `bin_`, `cfg_`, `unit_`, `cad_`, `fw_`, `upg_`, `adm_`, `upd_`, `doc_`, `uni_`; gemeinsame Helfer aus `lib.sh` ohne Präfix wie in V2). shellcheck ohne globale Ausnahmen.
- Pfade (Konstanten in `lib.sh`, alle mit Präfix `$ROOT`, Default `""`, nur Tests setzen ihn): `BIN_DIR=/usr/local/bin`, `ETC_PANEL=/etc/mc-panel`, `ETC_FILES=/etc/mc-panel-files`, `DATA_DIR=/var/lib/mc-panel`, `INSTANCES_DIR=/srv/mc-panel`, `UNIT_DIR=/etc/systemd/system`, `CADDY_DIR=/etc/caddy`, `LOG_FILE=/var/log/mc-panel-install.log`, `ROLLBACK_DIR=/var/lib/mc-panel-rollback` (`root:root 0700`, bewusst **außerhalb** des Datenverzeichnisses von `mcpanel`: ein kompromittiertes panel darf nichts ablegen, was root beim Rollback installiert).
- Sockets: agent `/run/mc-panel/agent.sock`, files `/run/mc-panel-files/files.sock` (Socket-Unit, `mcpanel-mc:mcpanel 0660`), panel `/run/mc-panel-web/panel.sock` (Socket-Unit, `SocketUser=mcpanel`, `SocketGroup=caddy`, `SocketMode=0660`, `DirectoryMode=0755`).
- Release: Tarball `mc-panel_<version>_linux_<amd64|arm64>.tar.gz` enthält `panel`, `agent`, `files` und `deploy/`; `install.sh` ist eigenes Asset; `checksums.txt` enthält alle Assets.
- Exit-Codes: 0 ok, 1 Fehler, 2 `doctor` mit ✗ (1 bei nur Warnungen), 3 Update zurückgerollt, 4 Rollback gescheitert.
- Root schreibt nie in Verzeichnisse, die `mcpanel` oder `mcpanel-mc` gehören, und folgt dort keinen Symlinks: Dateien in `/var/lib/mc-panel` (DB-Sicherung/-Rückspielen) bearbeitet der Installer nur per `run_as mcpanel …` (§V2), Prüfungen von root dort nur mit `lstat`-Semantik (`[ -L ]` zuerst).
- Test-Override `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1` (nur CI); nie in Doku für Betreiber empfehlen.

## Review Focus

1. **Aussperren:** Firewall aktiviert, SSH auf ungewöhnlichem Port, IPv6-Sitzung, `sshd -T` nicht verfügbar; erwartet: Abbruch statt Aussperren, laufende Sitzung bleibt. → Aufgabe 6.
2. **Idempotenz und fremde Änderungen:** zweiter Lauf, vom Betreiber geänderte Konfiguration, eigene Sites im Caddyfile, vorhandenes `/etc/docker/daemon.json`; erwartet: keine Änderung bzw. nur fehlende Schlüssel ergänzt, fremde Inhalte unberührt. → Aufgaben 5, 6.
3. **Fehlgeschlagenes Update:** neue Binaries starten nicht, Migration scheitert, Abbruch mitten im Austausch (Strg-C, Stromausfall), volle Platte bei der DB-Kopie; erwartet: automatischer Rollback auf alten Stand inklusive DB, klare Meldung, nie ein halbes System ohne Hinweis. → Aufgabe 8.
4. **Herkunft und Integrität:** Prüfsumme passt nicht, `checksums.txt` fehlt, Download bricht ab, Weiterleitung auf HTTP; erwartet: Abbruch vor jedem Schreiben ins System. → Aufgaben 2, 4.
5. **Uninstall:** laufende Minecraft-Server, fremde Docker-Container, fremde nftables-Tabellen, `--purge` ohne Bestätigung; erwartet: nur mc-panel-Objekte entfernt, Daten bleiben ohne `--purge`, Bestätigung unumgehbar. → Aufgabe 10.

## Dateistruktur

```
internal/unixsock/activation.go (+_test)                 (1)  Activated(getenv, unsetenv, pid) (net.Listener, error)
cmd/files/main.go, cmd/panel/main.go                     (1)  nutzen Activated
cmd/panel/admin_list.go                                  (1)  `panel admin list`
deploy/systemd/*.service, *.socket                       (2)  Unit-Vorlagen
deploy/caddy/mc-panel.caddy                              (2)  Caddy-Vorlage
deploy/nftables/mcpanel_fw.nft                           (2)  Firewall-Vorlage
deploy/config/panel.toml, agent.toml, files.toml         (2)  Konfigurations-Vorlagen
scripts/install/lib.sh, main.sh, assemble.sh, test.sh    (2–3) Gerüst, Helfer, Zusammenfügen, Tests
Makefile (installer), .goreleaser.yaml, ci.yml (Lint/Build) (3)
scripts/install/preflight.sh … admin.sh, tests/install_*.sh (4–7)
scripts/install/update.sh, doctor.sh, uninstall.sh, tests/ops_*.sh (8–10)
scripts/testvm-deploy.sh, scripts/ci-installer.sh, ci.yml (installer) (11)
docs/dev/acceptance-tp6.md, README.md, docs/dev/README.md, CLAUDE.md (12)
```

## Verträge (verbindlich)

### V1 – Go (Aufgabe 1)

```go
package unixsock
// Activated returns the listener systemd passed as fd 3 when LISTEN_PID is pid and
// LISTEN_FDS is 1, otherwise nil; it always unsets LISTEN_PID, LISTEN_FDS, LISTEN_FDNAMES.
func Activated(getenv func(string) string, unsetenv func(string) error, pid int) (net.Listener, error)
```

- `cmd/files` nutzt `unixsock.Activated` (Verhalten unverändert, Tests ziehen mit um).
- `panel serve`: Ist `listen` ein `unix:`-Pfad und liefert `Activated` einen Listener, nimmt das panel ihn (der Pfad in `listen` muss dem aktivierten Socket entsprechen; sonst Fehler beim Start). Ohne Aktivierung wie bisher (eigener Socket bzw. TCP).
- `panel admin list [-config path] [-dev]`: gibt pro Admin eine Zeile `<username>` aus (sortiert), Exit-Code 0; keine Admins → keine Ausgabe, Exit-Code 0. Liest nur.

### V2 – `lib.sh` (Aufgabe 2; von B1, B2 und C genutzt)

```bash
log_step NAME                  # Überschrift eines Schritts (stdout + Log)
log_ok MSG; log_changed MSG; log_skip MSG; warn MSG
die MSG [CODE]                 # Meldung, Log-Verweis, exit CODE (Default 1)
confirm PROMPT                 # 0 = ja; YES=1 → ja ohne Frage; liest von /dev/tty; ohne TTY → nein
version_ge A B                 # 0, wenn Version A >= B (semver, führendes v erlaubt)
render_template NAME           # Vorlage NAME (Pfad relativ zu deploy/) auf stdout, {{KEY}} → Wert von KEY_* aus der Umgebung (V3); unbekannter Platzhalter → die
write_file DEST MODE OWNER GROUP   # stdin nach DEST (atomar: tmp + mv), nur wenn Inhalt/Modus/Besitzer abweichen; Ausgabe "changed" oder "unchanged"
toml_ensure_key FILE KEY VALUE     # hängt `KEY = VALUE` an, wenn KEY auf oberster Ebene fehlt (Kommentar "added by install.sh <version>"); vorhandene Werte nie ändern; Ausgabe changed/unchanged
fetch URL DEST                 # curl --proto =https --tlsv1.2 -fsSL --retry 3 nach DEST
sha256_verify FILE CHECKSUMS NAME  # 0, wenn die Zeile für NAME in CHECKSUMS zur Datei passt; sonst die
run_as USER CMD...             # runuser -u USER -- CMD...
systemd_wait_active UNIT SECS  # wartet, bis UNIT active ist; sonst 1
```

- Zusätzlich in `lib.sh`: `ensure_dir PATH MODE OWNER GROUP` (changed/unchanged), `sys_useradd`, `sys_userdel`, `sys_chown`, `sys_runuser`, `reset_globals`, `_owner_get`/`_owner_set`, `$TTY` (Pfad des Terminals, Tests setzen ihn; B1/B2 lesen nie direkt `/dev/tty`). **`$ROOT` setzen `write_file`, `ensure_dir`, `toml_ensure_key` (und `_owner_*`) selbst davor – Aufrufer übergeben Systempfade OHNE `$ROOT`.** `$WORK/owners` hat das Format `pfad owner group mode` (Pfad ohne `$ROOT`); nur `lib.sh` schreibt/liest es. Ausgabeformat aller Schritte auf stdout: `[ok] …`, `[changed] …`, `[skip] …`, `[warn] …` (die CI prüft darauf).
- Test-Harness `test.sh`: entdeckt `test_*`-Funktionen (auch aus `scripts/install/tests/*.sh`), je Test eine Subshell mit frischem `$ROOT`/`$WORK`; Asserts `assert_eq`, `assert_contains`, `assert_file_eq`, `assert_file`, `assert_no_file`, `assert_fails CMD…`, `assert_called`/`assert_not_called` (**Festtext** `grep -F` gegen die echten Zeilen in `$WORK/calls.log`, also `systemctl …`, `apt-get …`, `nft …`, nicht `sys_…`), `assert_exit`; `fake_exec` (zeichnet ein `exec` auf statt es auszuführen); `test.sh [DATEI|MUSTER]` – ein Argument mit `/` oder `.sh` wählt eine Testdatei, sonst ein Namensmuster; `stub NAME BODY` (BODY ist Shell-Code, z. B. `stub uname 'echo x86_64'`) ersetzt Funktionen oder Befehle für reine Abfragen (`id`, `ss`, `ip`, `getent`, `dpkg-query`, `sshd -T`, `restic`, `curl --unix-socket`, `sqlite3`, `findmnt`, `openssl`); Besitz/Modus im Trockenlauf über `$WORK/owners`.
- Globale Variablen (gesetzt in `main.sh` aus den Argumenten, V4): `MCPANEL_VERSION`, `CMD`, `DOMAIN`, `EMAIL`, `TLS_MODE` (`letsencrypt|internal`), `TARGET_VERSION`, `FROM_DIR`, `SSH_PORT`, `NO_FIREWALL` (0/1), `ADMIN_USER`, `YES` (0/1), `PURGE` (0/1), `CHECK_ONLY` (0/1), `FORCE` (0/1), `JSON` (0/1), `ARCH` (`amd64|arm64`), `WORK` (temporäres Verzeichnis, per `trap` entfernt), `ROOT` (Test-Präfix).
- `$ROOT` steht vor jedem Systempfad, den ein Schritt **selbst** liest oder anlegt (`[ -f "$ROOT$ETC_PANEL/panel.toml" ]`); an die Helfer aus `lib.sh` geht der Pfad ohne `$ROOT` (siehe oben). Befehle, die das echte System ändern (`apt-get`, `systemctl`, `useradd`, `nft`, `docker`, `caddy`), laufen über die Wrapper `sys_apt`, `sys_systemctl`, `sys_useradd`, `sys_nft`, `sys_docker`, `sys_caddy` in `lib.sh`, die in Tests (`MCPANEL_INSTALL_DRYRUN=1`) nur den Aufruf in `$WORK/calls.log` protokollieren.

### V3 – Vorlagen (Aufgabe 2)

- Platzhalter `{{NAME}}` mit Werten aus `KEY_NAME`: `KEY_DOMAIN`, `KEY_TLS` (Caddy-Zeile: `tls <email>` bzw. `tls internal`), `KEY_PANEL_UID`, `KEY_PUBLIC_URL` (`https://<domain>`, Schlüssel `public_url` in `panel.toml`), `KEY_INSTANCES_DIR`, `KEY_SSH_PORT`, `KEY_VERSION`, `KEY_RESTIC_BINARY` (aus `command -v restic`, Version ≥ 0.17, sonst Abbruch mit Hinweis; Ubuntu 26.04: `/usr/bin/restic`, CI: `/usr/local/bin/restic` aus `scripts/install-restic.sh`), `KEY_PING_ADDR` (`127.0.0.1:25565`).
- Firewall: `KEY_SSH_PORT` ist eine nft-Liste (`22, 2222`), Vorlage `tcp dport { {{SSH_PORT}} }`; die Datei beginnt mit `table inet mcpanel_fw {}` + `delete table inet mcpanel_fw` (atomares Ersetzen) und liegt unter `/etc/mc-panel/mcpanel_fw.nft`. `agent.toml` ist `root:root 0600`.
- Caddy: die Import-Zeile in `/etc/caddy/Caddyfile` lautet genau `import mc-panel.caddy # managed by mc-panel` (B1 fügt sie einmal an; eine vorhandene Zeile, die auf `^import mc-panel\.caddy([[:space:]]|$)` passt, ersetzt B1 durch die markierte statt anzuhängen; B2 entfernt genau die markierte Zeile; C schreibt im VM-Rahmen ebenfalls die markierte Zeile). `skip_install_trust` (Test-VM, `tls internal`) ist eine globale Option und gehört nicht in `mc-panel.caddy`.
- Units: `mcpanel-agent.service`, `mcpanel-files.socket`, `mcpanel-files.service`, `mcpanel-panel.socket`, `mcpanel-panel.service`, `mcpanel-fw.service` – Inhalte inhaltlich wie heute in `scripts/testvm-deploy.sh` (Härtung, TP4-Werte `MemoryMax=1G`, `TasksMax=512`, `IOWeight=50`, `CPUWeight=50`, `ReadWritePaths` inkl. `backups`) plus die neuen Panel-Socket- und Firewall-Units (Spec §4.6, §4.8; `mcpanel-fw.service` mit `Before=network-pre.target`). `unit_ensure` **schreibt** `mcpanel-fw.service` nur; aktiviert/gestartet wird sie ausschließlich von `fw_ensure` (also nie bei `--no-firewall`).
- `panel.toml` mit `listen = "unix:/run/mc-panel-web/panel.sock"`, `public_url`, `trusted_proxies`, `monitoring_ping_addr`, `instances_dir`; `agent.toml`, `files.toml` mit `panel_uid`, `instances_dir`, `restic_binary` – jeweils nur Schlüssel, die die Config-Parser kennen (der Plan prüft gegen `internal/config`).

### V4 – `main.sh` (Aufgabe 2)

- `install.sh <install|update|doctor|uninstall> [Optionen]` mit den Optionen aus Spec §3.1; zusätzlich `--admin-password-file PATH` (0600; bei `curl | bash` ist stdin das Skript). Ohne TTY und ohne Admin-Angaben überspringt der Admin-Schritt mit `[warn]`. `cmd_doctor` liefert seinen Code per `return` (`DOC_SUMMARY=1` für die Kurzform aus `cmd_install`). `update --from-dir` nutzt `FROM_DIR/install.sh`; die Übergabe an das Ziel-Skript per `exec` setzt `MCPANEL_HANDOFF_WORK`, damit das Kind das `WORK` des Elternprozesses aufräumt. unbekannte Option oder fehlender Pflichtwert → Usage + Exit 1. `--version` verlangt das Format `vX.Y.Z`/`X.Y.Z`; `TARGET_VERSION` ist ohne `v`, Release-URLs bauen den Tag als `v${TARGET_VERSION#v}`. `--version` mit `--from-dir` ist verboten; mit `--from-dir` gilt `TARGET_VERSION=$("$FROM_DIR/panel" version)` (ohne Netz). Die Übergabe an das Ziel-Skript nutzt zusätzlich `MCPANEL_UPDATE_HANDOFF=1` (das Kind überspringt Download und Übergabe); das Aufräumen von `MCPANEL_HANDOFF_WORK` macht ausschließlich `setup_work` in `main.sh`.
- Ablauf: Argumente → root-Prüfung (außer `doctor` ohne root mit eingeschränkten Prüfungen) → `WORK=$(mktemp -d)` mit `trap` → Logging nach `$LOG_FILE` (0600) → `cmd_install`/`cmd_update`/`cmd_doctor`/`cmd_uninstall` (aus B1/B2).
- `cmd_install` ruft der Reihe nach: `pre_check`, `pkg_install`, `lay_ensure`, `bin_install`, `cfg_ensure`, `unit_ensure`, `cad_ensure`, `fw_ensure` (außer `NO_FIREWALL=1`), `upg_ensure`, `adm_ensure`, dann `cmd_doctor` (Ergebnis nur als Zusammenfassung) und die Abschlussausgabe `install_summary` (Phase A; B1 liefert keine eigene).
- `cmd_update` (B2) führt den Austausch in einer Subshell außerhalb eines Bedingungskontexts aus, damit `set -e` dort greift und ein Fehler den Rollback auslöst statt hart zu enden: `set +e; ( set -e; bin_install; cfg_ensure; unit_ensure ); rc=$?; set -e`. Reihenfolge immer `bin_install → cfg_ensure → unit_ensure` (`cfg_ensure` setzt die `KEY_*`, die `unit_ensure` braucht). Bei `CMD=update` schreibt `unit_ensure` nur Dateien und führt `daemon-reload` aus – kein `enable`/`start`/Warten; Neustart und Health-Check macht `update`. B1-Schritte rufen `die` nur in Vorprüfungen vor jeder Änderung; danach melden sie Fehler per `return 1`.
- `doctor --json` gibt genau `{"status": "ok|warn|fail", "checks": [{"id": …, "status": "ok|warn|fail", "message": …, "hint": …}]}` aus (Vertrag für C). Ohne Netz meldet die Versionsprüfung nur `warn`, nie `die`.
- Phase A liefert für alle Schritt-Funktionen Stubs (`log_skip "not implemented yet"`), damit `make installer` und `test.sh` von Anfang an laufen; B1/B2 ersetzen sie in ihren Dateien.

### V5 – Zusammenfügen und Release (Aufgabe 3)

- `scripts/install/assemble.sh <version> <out>` fügt in fester Reihenfolge `lib.sh`, alle Schritt-Dateien (alphabetisch), `main.sh` zusammen, bettet jede Datei unter `deploy/` als Funktion `tpl_<pfad_mit_unterstrichen>` (Heredoc mit `'EOF'`) ein und ersetzt `MCPANEL_VERSION=dev` durch die Version; `render_template` nutzt die eingebetteten Vorlagen, wenn `FROM_DIR` nicht gesetzt ist, sonst `FROM_DIR/deploy/`.
- `make installer` → `build/install.sh` (nicht `dist/`, das goreleaser mit `--clean` leert; `build/` steht in `.gitignore`; Version aus `git describe` wie beim Build); `make check` führt zusätzlich `bash -n` für `scripts/install/*.sh` und das zusammengefügte Skript aus (auch unter Bash 3.2 des Macs).
- goreleaser: `install.sh` als zusätzliches Asset (Hook vor dem Build ruft `assemble.sh`), `deploy/**` im Tarball, `checksums.txt` über alle Assets.
- CI: Job `installer-lint` (Filter `installer`: `scripts/install/**`, `deploy/**`, `scripts/ci-installer.sh`, `scripts/testvm-deploy.sh`, `Makefile`, `.goreleaser.yaml`) führt shellcheck über `scripts/install/*.sh`, `scripts/install/tests/*.sh` und `build/install.sh` sowie `scripts/install/test.sh` aus. Der Build-Job prüft `deploy/` im Tarball und `install.sh` in `checksums.txt`.
