# Teilprojekt 6 – Schritte von `install` (Aufgaben 4–7) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die Schritt-Funktionen von `install.sh install` (Spec §4.1–§4.10) in den B1-Dateien unter `scripts/install/` umsetzen: Vorprüfung, Pakete, Benutzer/Verzeichnisse, Binaries, Konfiguration, Units, Caddy, Firewall, Sicherheitsupdates, erster Admin – jeweils idempotent und mit Bash-Tests gegen ein `$ROOT`-Präfix im Trockenlauf.

**Architecture:** Jede Datei enthält die Funktionen eines Schritts mit eigenem Präfix (`pre_`, `pkg_`, `lay_`, `bin_`, `cfg_`, `unit_`, `cad_`, `fw_`, `upg_`, `adm_`). Systemändernde Befehle laufen ausschließlich über die `sys_*`-Wrapper aus `lib.sh` (V2), Dateien über `write_file`/`toml_ensure_key`, Vorlagen über `render_template` (V3). Abfragen (`id`, `getent`, `ss`, `ip`, `dpkg-query`, `sshd -T`, `restic version`, `stat`) sind gewöhnliche Befehle, die die Tests als Bash-Funktionen überschreiben (`stub`). `main.sh` (Phase A, V4) ruft die Funktionen in fester Reihenfolge auf; dieser Plan ändert `main.sh` nicht.

**Tech Stack:** Bash 5 (`set -euo pipefail` aus `main.sh`), coreutils, `tar`, `curl`, `sha256sum`, `systemctl`, `nft`, `caddy` 2.6 (Ubuntu-Paket), `dpkg-query`, `getent`, `iproute2`; Tests mit dem Harness `scripts/install/test.sh` aus Phase A.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` (§3.1, §4) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-13-installer-00-overview.md` (V2–V4, Global Constraints, Dateieigentum B1)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | B1, Teammate „inst-install“, `feat/installer-install` (Worktree `../mc-panel-wt/feat/installer-install` von `origin/main`) |
| Startet, wenn | Phase A gemerged (`lib.sh`, `main.sh`, `test.sh`, `assemble.sh`, Vorlagen in `deploy/`) |
| Dateieigentum | nur `scripts/install/{preflight,packages,layout,binaries,config,units,caddy,firewall,upgrades,admin}.sh` und `scripts/install/tests/install_*.sh` |
| Wo getestet wird | Mac: `make check` (enthält `bash -n` und `make installer`); VM: `make test-vm SUITE=lint` (shellcheck + `scripts/install/test.sh`). Kein echter `install`-Lauf in dieser Phase (der kommt in Phase C in der CI) |
| Ubuntu 26.04 (`resolute`), geprüft auf packages.ubuntu.com am 2026-10-13 | `restic` 0.18.1-3ubuntu1 (≥ 0.17 ✓), `caddy` 2.6.2-14 (Dienst `caddy.service`, Benutzer/Gruppe `caddy`, `/etc/caddy/Caddyfile` ist Conffile), `docker.io` 29.1.3-0ubuntu4.1 (`live-restore` ✓), außerdem `nftables`, `tzdata`, `sqlite3`, `unattended-upgrades`, `curl`, `ca-certificates`, `bzip2` |
| Release-URL | `https://github.com/Bernotese/mc-panel/releases/download/v<ver>/mc-panel_<ver>_linux_<arch>.tar.gz` und `…/v<ver>/checksums.txt` (`<ver>` ohne führendes `v`, wie goreleaser es schreibt) |
| Rechte (Spec §4.3, TP1–TP4, `scripts/testvm-deploy.sh`) | `/srv/mc-panel` `root:root 0755`; `/etc/mc-panel` `root:mcpanel 0750`; `/etc/mc-panel-files` `root:mcpanel-mc 0750`; `/var/lib/mc-panel` `mcpanel:mcpanel 0700`; `panel.toml` `root:mcpanel 0640`; `files.toml` `root:mcpanel-mc 0640`; `agent.toml` `root:root 0600` (wie heute auf der Test-VM; siehe Abweichung 2); Units und `mc-panel.caddy` `root:root 0644`; Firewall-Regeln `/etc/mc-panel/mcpanel_fw.nft` `root:root 0644` |

## Abweichungen und Annahmen (Lead bitte bestätigen)

1. **Test-Harness:** genau die API aus Phase A (Übersicht V2): `stub NAME BODY` (BODY ist Shell-Code, z. B. `stub id 'echo 0'`), `assert_called`/`assert_not_called` als **Festtext** gegen die echten `calls.log`-Zeilen (`systemctl …`, `env DEBIAN_FRONTEND=noninteractive apt-get -y -q …`, `useradd …`, `nft …`, `caddy …`), dazu `assert_fails`, `assert_file`, `assert_no_file`, `assert_file_contains`, `assert_exit`; Besitz im Trockenlauf nur über `ensure_dir`/`write_file` (`$WORK/owners`, `_owner_get`). Systempfade gehen **ohne** `$ROOT` an `write_file`, `ensure_dir`, `toml_ensure_key`.
2. **`agent.toml` bleibt `root:root 0600`** (Test-VM heute), nicht `root:mcpanel 0640` wie Spec §4.5 pauschal sagt: nur der agent (root) liest sie, und sie enthält `panel_uid`/Images. Der Plan setzt 0600.
3. **`panel.toml`:** Schlüssel `public_url`, Platzhalter `{{PUBLIC_URL}}`/`KEY_PUBLIC_URL` (Übersicht V3).
4. **Admin-Passwort:** Quelle in dieser Reihenfolge `--admin-password-file`, stdin (nur mit `--admin-user` und wenn `install.sh` aus einer Datei läuft), Terminal `$TTY`; ohne alles `[warn]` und weiter (Übersicht V4).
5. **SSH-Ports:** `sshd -T` kann mehrere `port`-Zeilen liefern; alle werden freigegeben. `KEY_SSH_PORT` enthält deshalb eine nft-Elementliste (`22` oder `22, 2222`), die Vorlage nutzt `tcp dport { {{SSH_PORT}} } accept`.
6. **Caddy 2.6 und `tls internal`:** Ohne die globale Option `skip_install_trust` versucht Caddy, seine Root-CA in den System-Trust zu installieren (scheitert als Benutzer `caddy` mit einer Warnung, sonst harmlos). Der Installer ändert fremde globale Optionen nicht; nur wenn er das Caddyfile selbst anlegt bzw. ersetzt (frisches Paket-Default), setzt er bei `TLS_MODE=internal` einen globalen Block mit `skip_install_trust`.
7. **Nicht veränderte Paket-Default-Datei `/etc/caddy/Caddyfile`:** Ubuntu liefert eine Default-Site `:80` (Willkommensseite). Ist die Datei unverändert (MD5 = Conffile-Prüfsumme aus `dpkg-query -W -f='${Conffiles}' caddy`), ersetzt der Installer sie durch eine verwaltete Datei mit nur der Import-Zeile; jede veränderte Datei bekommt nur die Import-Zeile angehängt (Review Focus 2).
8. **Firewall-Regeldatei** liegt unter `/etc/mc-panel/mcpanel_fw.nft` (gerendert aus `deploy/nftables/mcpanel_fw.nft`); `mcpanel-fw.service` (Vorlage aus Phase A) lädt sie mit `nft -f`. Die Datei beginnt mit `table inet mcpanel_fw` + `delete table inet mcpanel_fw` vor der eigentlichen Tabelle, damit ein erneutes Laden atomar ersetzt. Phase A muss die Vorlage so anlegen (oder dieser Plan rendert genau diesen Kopf selbst, siehe Aufgabe 6, Step 5).

## Nachträge nach dem Schnittstellen-Review (verbindlich, im Code unten umgesetzt)

- **Update (Übersicht V4):** `update` (B2) ruft `bin_install → cfg_ensure → unit_ensure` in einer Subshell mit `set -e` auf. Bei `CMD=update` schreibt `unit_ensure` nur Dateien und führt `daemon-reload` aus (kein `enable`/`start`/Warten). Nach der ersten Änderung am System melden B1-Schritte Fehler per `return 1` statt `die`; `die` nur in Vorprüfungen davor.
- **Firewall-Unit:** `unit_ensure` schreibt `mcpanel-fw.service`, aktiviert sie aber nie; das macht `fw_ensure` (`enable` + `restart` bzw. `enable --now`). `main.sh` ruft `fw_ensure` bei `--no-firewall` gar nicht auf.
- **Docker/restic:** Docker-Pakete nur, wenn `docker info` nicht antwortet; `restic_binary` aus `command -v restic` (≥ 0.17, sonst Abbruch); Docker-Neustart für eine neue `daemon.json` nur ohne laufende mc-panel-Container, sonst `[warn]`.
- **Verzeichnisse:** `lay_ensure` legt zusätzlich `/srv/mc-panel/instances` (`root:mcpanel-mc 0750`) und `/srv/mc-panel/backups` (`mcpanel-mc:mcpanel-mc 0700`) an (systemd verlangt sie für `ReadWritePaths`).
- **Caddy:** Import-Zeile genau `import mc-panel.caddy # managed by mc-panel`; eine unmarkierte Zeile wird markiert statt verdoppelt.
- **Keine Dopplungen mit Phase A:** root-, Architektur- und Argumentprüfungen macht `main.sh`; die Abschlussausgabe macht `install_summary`.

## Review Focus (aus der Übersicht, in dieser Phase)

1. **Aussperren** (Übersicht RF 1) → Aufgabe 6: SSH-Port aus `sshd -T`, alle Ports, `$SSH_CONNECTION` (IPv4 und IPv6) muss im Regelsatz enthalten sein, `nft -c` vor dem Laden, sonst Abbruch ohne Änderung; `ct state established,related accept` vor allem anderen, damit die laufende Sitzung bleibt.
2. **Idempotenz/fremde Änderungen** (RF 2) → Aufgaben 4–6: zweiter Lauf meldet überall `unchanged`; vorhandene Konfiguration bekommt nur fehlende Schlüssel; vorhandenes `daemon.json` bleibt; Caddyfile mit fremden Sites bleibt bis auf eine angehängte Import-Zeile unverändert; Import-Zeile genau einmal.
3. **Integrität** (RF 4) → Aufgabe 5: Prüfsumme falsch / Zeile fehlt / Download bricht ab → Abbruch, bevor irgendetwas unter `$BIN_DIR` geschrieben wird; Tarball mit `..`/absoluten Pfaden wird abgelehnt.

## Dateistruktur

```
scripts/install/preflight.sh        (4)  pre_check und Teilprüfungen
scripts/install/packages.sh         (4)  pkg_install, Docker-daemon.json, restic-Version
scripts/install/upgrades.sh         (4)  upg_ensure
scripts/install/layout.sh           (5)  lay_ensure (nutzt ensure_dir)
scripts/install/binaries.sh         (5)  bin_install, Download/Prüfung/Entpacken
scripts/install/config.sh           (5)  cfg_keys, cfg_ensure
scripts/install/units.sh            (6)  unit_ensure
scripts/install/caddy.sh            (6)  cad_ensure, cad_wait
scripts/install/firewall.sh         (6)  fw_ensure, fw_ssh_ports
scripts/install/admin.sh            (7)  adm_ensure
scripts/install/tests/install_*.sh  (4–7) Tests je Datei
```

---

### Task 4: Vorprüfung, Pakete, Sicherheitsupdates

**Files:**
- Create: `scripts/install/preflight.sh`, `scripts/install/packages.sh`, `scripts/install/upgrades.sh` (ersetzen die Stubs aus Phase A; falls Phase A die Stubs in diesen Dateien angelegt hat, werden sie überschrieben)
- Create: `scripts/install/tests/install_preflight.sh`, `scripts/install/tests/install_packages.sh`, `scripts/install/tests/install_upgrades.sh`

**Interfaces:**
- Consumes (Phase A): V2 (`log_step`, `log_ok`, `log_changed`, `log_skip`, `warn`, `die`, `confirm`, `version_ge`, `write_file`, `sys_apt`, `sys_systemctl`, `sys_docker`), globale Variablen aus V2/V4 (`CMD`, `DOMAIN`, `EMAIL`, `TLS_MODE`, `YES`, `ARCH`, `ROOT`, `WORK`), Harness (Abweichung 1).
- Produces:
  - `pre_check` – prüft root, OS, Architektur (setzt `ARCH`), systemd, Pflichtargumente, Ports, Domain-Auflösung; `die` bei hartem Fehler, `warn` + `confirm` bei Domain-Abweichung.
  - `pre_os_ok` (0/1), `pre_port_owner PORT` (gibt den Prozessnamen aus, leer = frei), `pre_domain_matches` (0/1).
  - `pkg_install` – installiert fehlende Pakete, prüft restic ≥ 0.17 und Docker, legt `daemon.json` an, wenn sie fehlt.
  - `PKG_LIST` (Array), `pkg_missing` (gibt fehlende Paketnamen aus), `pkg_restic_version` (gibt `X.Y.Z` aus).
  - `upg_ensure` – schaltet unattended-upgrades für Sicherheitsupdates ein, ohne automatischen Neustart.

- [ ] **Step 0: Harness-Namen abgleichen**

`sed -n '1,200p' scripts/install/test.sh scripts/install/lib.sh` lesen. Weichen die Namen von Abweichung 1 ab, die Tests in diesem Plan beim Abtippen entsprechend umbenennen und im PR vermerken.

- [ ] **Step 1: Tests für die Vorprüfung schreiben (rot)**

`scripts/install/tests/install_preflight.sh`:

```bash
# shellcheck shell=bash
# Tests for preflight.sh (spec §4.1). Run by scripts/install/test.sh.

pre_fixture_os() { # ID VERSION_ID
  mkdir -p "$ROOT/etc" "$ROOT/proc/1"
  printf 'ID=%s\nVERSION_ID="%s"\n' "$1" "$2" >"$ROOT/etc/os-release"
  printf 'systemd\n' >"$ROOT/proc/1/comm"
}

test_pre_os_ok_accepts_ubuntu_2604() {
  pre_fixture_os ubuntu 26.04
  pre_os_ok
}

test_pre_os_ok_rejects_2404_without_override() {
  pre_fixture_os ubuntu 24.04
  assert_fails pre_os_ok
}

test_pre_os_ok_accepts_2404_with_override() {
  pre_fixture_os ubuntu 24.04
  MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1 pre_os_ok
}

test_pre_os_ok_rejects_debian() {
  pre_fixture_os debian 13
  MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1
  assert_fails pre_os_ok
}

test_pre_port_owner_reads_ss() {
  stub ss "printf '%s\\n' 'LISTEN 0 4096 *:443 *:* users:((\"caddy\",pid=812,fd=8))'"
  assert_eq caddy "$(pre_port_owner 443)"
  stub ss ':'
  assert_eq '' "$(pre_port_owner 443)"
}

test_pre_check_refuses_foreign_port_user() {
  pre_fixture_os ubuntu 26.04
  stub id 'echo 0'
  stub uname 'echo x86_64'
  DOMAIN=panel.example.com EMAIL=a@example.com TLS_MODE=letsencrypt CMD=install
  stub ss "printf '%s\\n' 'LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:((\"nginx\",pid=99,fd=6))'"
  assert_fails pre_check
}

test_pre_check_allows_own_services_on_ports() {
  pre_fixture_os ubuntu 26.04
  stub id 'echo 0'
  stub uname 'echo x86_64'
  DOMAIN=panel.example.com EMAIL=a@example.com TLS_MODE=letsencrypt CMD=install YES=1
  pre_port_owner() { case "$1" in 80 | 443) echo caddy ;; 25565) echo docker-proxy ;; esac; }
  pre_domain_matches() { return 0; }
  pre_check
}

test_pre_domain_matches_local_address() {
  stub getent "echo '203.0.113.7     STREAM panel.example.com'"
  stub ip "echo '2: eth0    inet 203.0.113.7/24 brd 203.0.113.255 scope global eth0'"
  DOMAIN=panel.example.com
  pre_domain_matches
}

test_pre_domain_mismatch_warns_and_needs_yes() {
  pre_fixture_os ubuntu 26.04
  stub id 'echo 0'
  stub uname 'echo x86_64'
  pre_port_owner() { :; }
  pre_domain_matches() { return 1; }
  DOMAIN=panel.example.com EMAIL=a@example.com TLS_MODE=letsencrypt CMD=install YES=0
  confirm() { return 1; }
  assert_fails pre_check
  YES=1
  confirm() { return 0; }
  pre_check
}

test_pre_check_internal_tls_skips_dns() {
  pre_fixture_os ubuntu 26.04
  stub id 'echo 0'
  stub uname 'echo aarch64'
  pre_port_owner() { :; }
  pre_domain_matches() { echo "dns must not be checked" >&2; return 1; }
  DOMAIN=panel.192-168-1-117.sslip.io EMAIL='' TLS_MODE=internal CMD=install YES=0
  pre_check
}
```

Run: `make check` (Mac) → `bash -n` ok, `make installer` ok; dann `make test-vm SUITE=lint`
Expected: FAIL – `pre_os_ok: command not found` (bzw. „not implemented yet“ aus dem Stub)

- [ ] **Step 2: `preflight.sh` schreiben**

```bash
# shellcheck shell=bash
# preflight.sh – spec §4.1: refuse to touch a system the installer does not support.

# Ports the panel needs; the value is the process name allowed to hold the port already.
declare -gA PRE_PORTS=([80]=caddy [443]=caddy [25565]=docker-proxy)

# pre_os_ok: 0 on Ubuntu 26.04; other Ubuntu releases only with the CI override.
pre_os_ok() {
  local id='' version=''
  [[ -r "$ROOT/etc/os-release" ]] || return 1
  # shellcheck disable=SC1091 # read in a subshell, only two variables leave it
  read -r id version < <(. "$ROOT/etc/os-release" && printf '%s %s\n' "${ID:-}" "${VERSION_ID:-}")
  [[ "$id" == ubuntu ]] || return 1
  [[ "$version" == 26.04 ]] && return 0
  [[ "${MCPANEL_INSTALL_ALLOW_UNSUPPORTED:-0}" == 1 ]]
}


# pre_port_owner PORT prints the name of the process listening on TCP PORT ("" = free).
pre_port_owner() {
  local line
  line="$(ss -Hltnp "sport = :$1" 2>/dev/null | head -n 1)"
  [[ -n "$line" ]] || return 0
  if [[ "$line" =~ users:\(\(\"([^\"]+)\" ]]; then
    echo "${BASH_REMATCH[1]}"
  else
    echo unknown
  fi
}

# pre_domain_matches: 0 if DOMAIN resolves to an address of this host (A/AAAA against the
# global addresses of the interfaces and the source address of the default route).
pre_domain_matches() {
  local resolved local_addrs
  resolved="$(getent ahosts "$DOMAIN" 2>/dev/null | awk '{print $1}' | sort -u)"
  [[ -n "$resolved" ]] || return 1
  local_addrs="$({
    ip -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1
    ip route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}'
  } | sort -u)"
  [[ -n "$local_addrs" ]] || return 1
  [[ -n "$(comm -12 <(printf '%s\n' "$resolved") <(printf '%s\n' "$local_addrs"))" ]]
}

pre_check() {
  log_step "preflight"
  # root, architecture and the arguments (--tls/--domain/--email) are checked by main.sh
  # (require_root, detect_arch, validate_args – Phase A); no duplicates here.
  pre_os_ok || die "this installer supports Ubuntu 26.04 LTS only"
  [[ "$(cat "$ROOT/proc/1/comm" 2>/dev/null)" == systemd ]] || die "systemd must be PID 1"
  local port owner
  for port in "${!PRE_PORTS[@]}"; do
    owner="$(pre_port_owner "$port")"
    if [[ -n "$owner" && "$owner" != "${PRE_PORTS[$port]}" ]]; then
      die "port $port is in use by $owner"
    fi
  done
  if [[ "$TLS_MODE" == letsencrypt ]] && ! pre_domain_matches; then
    warn "$DOMAIN does not resolve to an address of this host; Let's Encrypt will fail until DNS points here"
    confirm "continue anyway?" || die "aborted: fix the DNS record of $DOMAIN first"
  fi
  log_ok "Ubuntu, systemd, ports"
}
```

- [ ] **Step 3: Tests für Pakete und Updates schreiben (rot)**

`scripts/install/tests/install_packages.sh`:

```bash
# shellcheck shell=bash
# Tests for packages.sh (spec §4.2).

pkg_fake_dpkg() { # names that count as installed
  local installed=" $* "
  dpkg-query() {
    local pkg="${!#}"
    if [[ "$installed" == *" $pkg "* ]]; then
      echo "install ok installed"
    else
      echo "unknown ok not-installed"
      return 1
    fi
  }
}

# pkg_env: Docker answers, restic 0.18.1 at /usr/bin/restic, no running mc-panel containers.
pkg_env() {
  stub pkg_docker_answers 'return 0'
  stub pkg_restic_path 'echo /usr/bin/restic'
  stub pkg_restic_version 'echo 0.18.1'
  stub pkg_mc_containers ':'
}

test_pkg_missing_lists_only_missing() {
  pkg_fake_dpkg caddy nftables restic tzdata sqlite3 unattended-upgrades curl ca-certificates
  assert_eq bzip2 "$(pkg_missing "${PKG_LIST[@]}" | xargs)"
}

test_pkg_install_installs_missing_once() {
  pkg_fake_dpkg curl ca-certificates
  pkg_env
  stub pkg_docker_answers 'return 1' # no Docker yet: docker.io is installed too
  pkg_install || true                # the final Docker check fails in the stubbed world
  assert_called 'apt-get -y -q update'
  assert_called 'apt-get -y -q install -y --no-install-recommends caddy nftables restic tzdata sqlite3 unattended-upgrades bzip2 docker.io'
}

test_pkg_install_keeps_preinstalled_docker() {
  pkg_fake_dpkg caddy curl ca-certificates
  pkg_env
  pkg_install
  assert_not_called 'docker.io'
}

test_pkg_install_second_run_installs_nothing() {
  pkg_fake_dpkg "${PKG_LIST[@]}" docker.io
  pkg_env
  mkdir -p "$ROOT/etc/docker"
  echo '{}' >"$ROOT/etc/docker/daemon.json"
  pkg_install
  assert_not_called 'apt-get'
}

test_pkg_install_refuses_old_restic() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_restic_version 'echo 0.16.4'
  assert_fails pkg_install
  assert_contains "$(cat "$WORK/out")" "too old"
}

test_pkg_install_takes_restic_from_path() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_restic_path 'echo /usr/local/bin/restic'
  stub pkg_restic_version 'echo 0.19.1'
  mkdir -p "$ROOT/etc/docker" && echo '{}' >"$ROOT/etc/docker/daemon.json"
  pkg_install
  assert_eq /usr/local/bin/restic "$PKG_RESTIC_BIN"
}

test_pkg_restic_version_parses_output() {
  stub restic 'echo "restic 0.18.1 compiled with go1.24.4 on linux/amd64"'
  assert_eq 0.18.1 "$(pkg_restic_version restic)"
}

test_pkg_daemon_json_only_when_missing() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  pkg_install
  assert_file_contains "$ROOT/etc/docker/daemon.json" '"live-restore": true'
  assert_called 'systemctl restart docker'
  printf '{"data-root": "/mnt/docker"}\n' >"$ROOT/etc/docker/daemon.json"
  : >"$WORK/calls.log"
  pkg_install
  assert_file_contains "$ROOT/etc/docker/daemon.json" '"data-root": "/mnt/docker"'
  assert_not_called 'systemctl restart docker'
}

test_pkg_daemon_json_no_restart_with_running_servers() {
  pkg_fake_dpkg "${PKG_LIST[@]}"
  pkg_env
  stub pkg_mc_containers 'echo 3f2a1b'
  local out
  out="$(pkg_install)"
  assert_file_contains "$ROOT/etc/docker/daemon.json" '"live-restore": true'
  assert_not_called 'systemctl restart docker'
  assert_contains "$out" "[warn]"
}

test_pkg_install_fails_without_docker() {
  pkg_fake_dpkg "${PKG_LIST[@]}" docker.io
  pkg_env
  stub pkg_docker_answers 'return 1'
  mkdir -p "$ROOT/etc/docker" && echo '{}' >"$ROOT/etc/docker/daemon.json"
  assert_fails pkg_install
}
```

`scripts/install/tests/install_upgrades.sh`:

```bash
# shellcheck shell=bash
# Tests for upgrades.sh (spec §4.9).

test_upg_ensure_writes_both_files_and_enables() {
  upg_ensure
  assert_file_contains "$ROOT/etc/apt/apt.conf.d/21mc-panel-auto-upgrades" 'APT::Periodic::Unattended-Upgrade "1";'
  assert_file_contains "$ROOT/etc/apt/apt.conf.d/52mc-panel-unattended-upgrades" 'Unattended-Upgrade::Automatic-Reboot "false";'
  assert_called 'systemctl enable --now unattended-upgrades.service'
}

test_upg_ensure_second_run_unchanged() {
  upg_ensure
  local out
  out="$(upg_ensure)"
  assert_contains "$out" unchanged
}
```

Run: `make test-vm SUITE=lint`
Expected: FAIL – `pkg_missing`/`upg_ensure` nicht definiert bzw. Stub.

- [ ] **Step 4: `packages.sh` und `upgrades.sh` schreiben**

`scripts/install/packages.sh`:

```bash
# shellcheck shell=bash
# packages.sh – spec §4.2: packages from the Ubuntu archive, Docker defaults, restic version.

PKG_LIST=(caddy nftables restic tzdata sqlite3 unattended-upgrades curl ca-certificates bzip2)
PKG_DOCKER=(docker.io)
PKG_RESTIC_MIN=0.17.0
PKG_RESTIC_BIN="" # set by pkg_install: path of a restic >= PKG_RESTIC_MIN (cfg_keys uses it)

# pkg_missing NAME... prints the given packages that are not installed, one per line.
pkg_missing() {
  local p
  for p in "$@"; do
    if [[ "$(dpkg-query -W -f='${Status}' "$p" 2>/dev/null)" != "install ok installed" ]]; then
      echo "$p"
    fi
  done
}

# pkg_docker_answers is 0 when a Docker daemon answers (also a preinstalled Docker CE, whose
# containerd.io conflicts with docker.io). A query: tests stub it.
pkg_docker_answers() {
  docker info >/dev/null 2>&1
}

# pkg_mc_containers prints the IDs of running mc-panel containers (query, tests stub it).
pkg_mc_containers() {
  docker ps -q --filter label=mcpanel.instance 2>/dev/null || true
}

# pkg_restic_path prints the path of restic from PATH ("" if there is none).
pkg_restic_path() {
  command -v restic 2>/dev/null || true
}

# pkg_restic_version BIN prints the version of that restic ("" if it does not run).
pkg_restic_version() {
  "$1" version 2>/dev/null | awk '$1 == "restic" {print $2; exit}'
}

pkg_daemon_json() {
  cat <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true
}
EOF
}

pkg_install() {
  log_step "packages"
  local missing=() want=("${PKG_LIST[@]}")
  if ! pkg_docker_answers; then
    want+=("${PKG_DOCKER[@]}")
  fi
  mapfile -t missing < <(pkg_missing "${want[@]}")
  if ((${#missing[@]} > 0)); then
    sys_apt update
    DEBIAN_FRONTEND=noninteractive sys_apt install -y --no-install-recommends "${missing[@]}"
    log_changed "installed ${missing[*]}"
  else
    log_skip "all packages present"
  fi
  local bin v
  bin="$(pkg_restic_path)"
  [[ -n "$bin" ]] || die "restic is not installed"
  v="$(pkg_restic_version "$bin")"
  [[ -n "$v" ]] || die "restic ($bin) does not run"
  version_ge "$v" "$PKG_RESTIC_MIN" ||
    die "restic $v at $bin is too old (need >= $PKG_RESTIC_MIN; install a newer restic first)"
  PKG_RESTIC_BIN="$bin"
  if [[ ! -e "$ROOT/etc/docker/daemon.json" ]]; then
    mkdir -p "$ROOT/etc/docker" # write_file needs the parent directory
    pkg_daemon_json | write_file /etc/docker/daemon.json 0644 root root >/dev/null
    if [[ -n "$(pkg_mc_containers)" ]]; then
      # A restart now would stop running Minecraft servers (live-restore is not active yet).
      warn "docker: log rotation and live-restore take effect on the next Docker restart (mc-panel containers are running)"
    else
      sys_systemctl restart docker
      log_changed "docker: log rotation and live-restore"
    fi
  fi
  sys_systemctl enable --now docker.service
  pkg_docker_answers || die "the Docker daemon does not answer"
  log_ok "restic $v ($bin), Docker"
}
```

`DEBIAN_FRONTEND=noninteractive` vor einer Funktion wird in Bash als temporäre Umgebung für diesen Aufruf gesetzt; `sys_apt` (Phase A) übernimmt die Umgebung. Im Trockenlauf protokolliert `sys_apt` nur die Argumente – deshalb prüft der Test die Zeile ohne die Variable.

`scripts/install/upgrades.sh`:

```bash
# shellcheck shell=bash
# upgrades.sh – spec §4.9: security updates daily, never an automatic reboot. The allowed
# origins stay those of Ubuntu's 50unattended-upgrades (security only by default); apt lists
# would append, so this file does not set them.

upg_periodic() {
  cat <<'EOF'
// mc-panel (install.sh): run unattended-upgrades daily.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
EOF
}

upg_options() {
  cat <<'EOF'
// mc-panel (install.sh): never reboot on its own; Minecraft servers would stop.
Unattended-Upgrade::Automatic-Reboot "false";
EOF
}

upg_ensure() {
  log_step "security updates"
  local d=/etc/apt/apt.conf.d a b # without $ROOT: write_file adds it (overview V2)
  mkdir -p "$ROOT$d"
  a="$(upg_periodic | write_file "$d/21mc-panel-auto-upgrades" 0644 root root)"
  b="$(upg_options | write_file "$d/52mc-panel-unattended-upgrades" 0644 root root)"
  sys_systemctl enable --now unattended-upgrades.service
  if [[ "$a" == changed || "$b" == changed ]]; then
    log_changed "unattended-upgrades enabled (security, no reboot)"
  else
    log_skip "unchanged"
  fi
}
```

- [ ] **Step 5: Tests grün**

Run: `make check`, dann `make test-vm SUITE=lint`
Expected: PASS (shellcheck ohne Befund, alle `test_pre_*`, `test_pkg_*`, `test_upg_*` grün)

- [ ] **Step 6: Commit**

```bash
git add scripts/install/preflight.sh scripts/install/packages.sh scripts/install/upgrades.sh scripts/install/tests/install_preflight.sh scripts/install/tests/install_packages.sh scripts/install/tests/install_upgrades.sh
git commit -m "feat(installer): preflight, packages and security updates

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 5: Benutzer, Verzeichnisse, Binaries, Konfiguration

**Files:**
- Create: `scripts/install/layout.sh`, `scripts/install/binaries.sh`, `scripts/install/config.sh`
- Create: `scripts/install/tests/install_layout.sh`, `scripts/install/tests/install_binaries.sh`, `scripts/install/tests/install_config.sh`

**Interfaces:**
- Consumes: V2 (`write_file`, `toml_ensure_key`, `fetch`, `sha256_verify`, `render_template`, `sys_useradd`), V3 (Vorlagen `config/panel.toml`, `config/agent.toml`, `config/files.toml`, Platzhalter `KEY_*`), Variablen `TARGET_VERSION`, `MCPANEL_VERSION`, `FROM_DIR`, `ARCH`, `DOMAIN`, `TLS_MODE`, Pfad-Konstanten (`BIN_DIR`, `ETC_PANEL`, `ETC_FILES`, `DATA_DIR`, `INSTANCES_DIR`).
- Produces:
  - `lay_ensure` (Verzeichnisse über `ensure_dir` aus `lib.sh`; kein eigenes `lay_dir`/`lay_chown`/`lay_owner_of`).
  - `bin_install`; `bin_version` (Zielversion ohne `v`); `bin_url NAME` (Release-URL eines Assets); `bin_tar_safe TARBALL` (0, wenn kein Eintrag absolut ist oder `..` enthält); `BIN_NAMES=(panel agent files)`.
  - `cfg_keys` (exportiert alle `KEY_*` aus V3); `cfg_ensure`; `CFG_REQUIRED_PANEL`, `CFG_REQUIRED_AGENT`, `CFG_REQUIRED_FILES` (Assoziativ-Arrays Schlüssel → TOML-Wert).

- [ ] **Step 1: Tests für Benutzer und Verzeichnisse (rot)**

`scripts/install/tests/install_layout.sh`:

```bash
# shellcheck shell=bash
# Tests for layout.sh (spec §4.3).

test_lay_ensure_creates_users_when_missing() {
  stub id '[[ "$1" == -u && "${2:-}" == root ]] && echo 0 || return 1'
  lay_ensure
  assert_called 'useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel'
  assert_called 'useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc'
}

test_lay_ensure_dirs_modes_and_owners() {
  stub id 'echo 998'
  lay_ensure
  assert_eq root:mcpanel "$(_owner_get /etc/mc-panel)"
  assert_eq root:mcpanel-mc "$(_owner_get /etc/mc-panel-files)"
  assert_eq mcpanel:mcpanel "$(_owner_get /var/lib/mc-panel)"
  assert_eq root:root "$(_owner_get /srv/mc-panel)"
  # instances/ and backups/ must exist before mcpanel-files starts (ReadWritePaths, review #16)
  assert_eq root:mcpanel-mc "$(_owner_get /srv/mc-panel/instances)"
  assert_eq mcpanel-mc:mcpanel-mc "$(_owner_get /srv/mc-panel/backups)"
  assert_eq 700 "$(stat -c '%a' "$ROOT/srv/mc-panel/backups")"
}

test_lay_ensure_keeps_existing_users() {
  stub id 'echo 998'
  lay_ensure
  assert_not_called 'useradd'
}

test_lay_ensure_second_run_unchanged() {
  stub id 'echo 998'
  lay_ensure >/dev/null
  local out
  out="$(lay_ensure)"
  assert_contains "$out" "[skip]"
}

test_lay_ensure_refuses_symlink() {
  stub id 'echo 998'
  mkdir -p "$ROOT/elsewhere" "$ROOT/etc"
  ln -s "$ROOT/elsewhere" "$ROOT/etc/mc-panel"
  assert_fails lay_ensure
}
```

- [ ] **Step 2: `layout.sh` schreiben**

Verzeichnisse legt ausschließlich `ensure_dir` aus `lib.sh` an (Pfade **ohne** `$ROOT`, Besitz im Trockenlauf über `$WORK/owners`); `ensure_dir` lehnt Symlinks ab und bricht mit `die` ab.

```bash
# shellcheck shell=bash
# layout.sh – spec §4.3: system users and the directories the services need. instances/ and
# backups/ are created here because mcpanel-files.service names them in ReadWritePaths (systemd
# refuses to start without them); the agent creates the other subdirectories of /srv/mc-panel.

lay_user() { # NAME HOME – 0 if the user was created
  id -u "$1" >/dev/null 2>&1 && return 1
  sys_useradd --system --user-group --home-dir "$2" --no-create-home --shell /usr/sbin/nologin "$1"
}

lay_ensure() {
  log_step "users and directories"
  local any=0 spec path mode owner group
  if lay_user mcpanel /var/lib/mc-panel; then any=1; fi
  if lay_user mcpanel-mc /nonexistent; then any=1; fi
  for spec in \
    "$INSTANCES_DIR 0755 root root" \
    "$INSTANCES_DIR/instances 0750 root mcpanel-mc" \
    "$INSTANCES_DIR/backups 0700 mcpanel-mc mcpanel-mc" \
    "$ETC_PANEL 0750 root mcpanel" \
    "$ETC_FILES 0750 root mcpanel-mc" \
    "$DATA_DIR 0700 mcpanel mcpanel"; do
    read -r path mode owner group <<<"$spec"
    # Not in a $(…): ensure_dir's die must stop the installer, not a subshell.
    ensure_dir "$path" "$mode" "$owner" "$group" >"$WORK/lay.out"
    [[ "$(cat "$WORK/lay.out")" == changed ]] && any=1
  done
  if ((any)); then log_changed "users mcpanel, mcpanel-mc and directories"; else log_skip "unchanged"; fi
}
```

- [ ] **Step 3: Tests für Binaries (rot)**

`scripts/install/tests/install_binaries.sh`:

```bash
# shellcheck shell=bash
# Tests for binaries.sh (spec §4.4, review focus 3).

bin_fixture_release() { # VERSION -> tarball + checksums in $WORK/srv
  local v="$1" d="$WORK/rel-src"
  mkdir -p "$d/deploy" "$WORK/srv"
  for b in panel agent files; do
    printf '#!/bin/sh\necho "%s %s"\n' "$b" "$v" >"$d/$b"
    chmod 0755 "$d/$b"
  done
  echo tpl >"$d/deploy/x"
  tar -C "$d" -czf "$WORK/srv/mc-panel_${v}_linux_amd64.tar.gz" panel agent files deploy
  (cd "$WORK/srv" && sha256sum "mc-panel_${v}_linux_amd64.tar.gz" >checksums.txt)
  fetch() { cp "$WORK/srv/${1##*/}" "$2"; }
}

test_bin_url_uses_release_layout() {
  TARGET_VERSION=v1.2.3 ARCH=arm64
  assert_eq https://github.com/Bernotese/mc-panel/releases/download/v1.2.3/mc-panel_1.2.3_linux_arm64.tar.gz \
    "$(bin_url "mc-panel_$(bin_version)_linux_$ARCH.tar.gz")"
}

test_bin_install_from_release() {
  bin_fixture_release 1.2.3
  TARGET_VERSION=1.2.3 ARCH=amd64 FROM_DIR=''
  bin_install
  assert_file_contains "$ROOT$BIN_DIR/panel" 'panel 1.2.3'
  assert_eq 755 "$(stat -c '%a' "$ROOT$BIN_DIR/agent")"
}

test_bin_install_second_run_unchanged() {
  bin_fixture_release 1.2.3
  TARGET_VERSION=1.2.3 ARCH=amd64 FROM_DIR=''
  bin_install >/dev/null
  assert_contains "$(bin_install)" unchanged
}

test_bin_install_refuses_bad_checksum() {
  bin_fixture_release 1.2.3
  echo "0000000000000000000000000000000000000000000000000000000000000000  mc-panel_1.2.3_linux_amd64.tar.gz" >"$WORK/srv/checksums.txt"
  TARGET_VERSION=1.2.3 ARCH=amd64 FROM_DIR=''
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR/panel"
}

test_bin_install_refuses_missing_checksum_line() {
  bin_fixture_release 1.2.3
  : >"$WORK/srv/checksums.txt"
  TARGET_VERSION=1.2.3 ARCH=amd64 FROM_DIR=''
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR/panel"
}

test_bin_install_refuses_failed_download() {
  TARGET_VERSION=1.2.3 ARCH=amd64 FROM_DIR=''
  fetch() { return 22; }
  assert_fails bin_install
  assert_no_file "$ROOT$BIN_DIR/panel"
}

test_bin_tar_safe_rejects_traversal() {
  local d="$WORK/evil"
  mkdir -p "$d/a"
  echo x >"$d/a/f"
  tar -C "$d" -czf "$WORK/evil.tar.gz" --transform 's,^a/f,../../etc/passwd,' a/f
  assert_fails bin_tar_safe "$WORK/evil.tar.gz"
}

test_bin_install_from_dir() {
  local d="$WORK/build"
  mkdir -p "$d/deploy"
  for b in panel agent files; do
    printf '#!/bin/sh\necho "%s 0.0.0-testvm+abc"\n' "$b" >"$d/$b"
    chmod 0755 "$d/$b"
  done
  FROM_DIR="$d" TARGET_VERSION=''
  bin_install
  assert_file_contains "$ROOT$BIN_DIR/files" 'files 0.0.0-testvm+abc'
}

test_bin_install_from_dir_requires_all_parts() {
  local d="$WORK/build"
  mkdir -p "$d"
  printf '#!/bin/sh\n' >"$d/panel"
  FROM_DIR="$d"
  assert_fails bin_install
}
```

- [ ] **Step 4: `binaries.sh` schreiben**

```bash
# shellcheck shell=bash
# binaries.sh – spec §4.4: release tarball, SHA-256 against checksums.txt of the same release,
# atomic install into BIN_DIR. Nothing is written to the system before the check passed.

BIN_NAMES=(panel agent files)
BIN_REPO_URL=https://github.com/Bernotese/mc-panel/releases/download

# bin_version prints the target version without a leading "v".
bin_version() {
  local v="${TARGET_VERSION:-${MCPANEL_VERSION:-dev}}"
  echo "${v#v}"
}

bin_url() { echo "$BIN_REPO_URL/v$(bin_version)/$1"; }

# bin_tar_safe TARBALL: 0 if every member is relative and has no ".." component.
bin_tar_safe() {
  local name
  while IFS= read -r name; do
    case "/$name/" in
    //* | */../*) return 1 ;;
    esac
  done < <(tar -tzf "$1")
}

# bin_source prints the directory holding panel, agent, files and deploy/.
bin_source() {
  if [[ -n "$FROM_DIR" ]]; then
    echo "$FROM_DIR"
    return 0
  fi
  local v tarball dir
  v="$(bin_version)"
  [[ "$v" != dev ]] || die "this install.sh has no release version; use --version or --from-dir"
  tarball="mc-panel_${v}_linux_${ARCH}.tar.gz"
  fetch "$(bin_url "$tarball")" "$WORK/$tarball" || die "download of $tarball failed"
  fetch "$(bin_url checksums.txt)" "$WORK/checksums.txt" || die "download of checksums.txt failed"
  sha256_verify "$WORK/$tarball" "$WORK/checksums.txt" "$tarball"
  bin_tar_safe "$WORK/$tarball" || die "$tarball contains unsafe paths"
  dir="$WORK/release"
  mkdir -p "$dir"
  tar -xzf "$WORK/$tarball" -C "$dir" --no-same-owner --no-same-permissions
  echo "$dir"
}

bin_install() {
  log_step "binaries"
  local src b any=0
  src="$(bin_source)" || exit 1
  for b in "${BIN_NAMES[@]}"; do
    [[ -f "$src/$b" && -x "$src/$b" ]] || die "$src/$b is missing or not executable"
  done
  [[ -d "$src/deploy" ]] || die "$src/deploy is missing"
  mkdir -p "$ROOT$BIN_DIR"
  for b in "${BIN_NAMES[@]}"; do
    if [[ -f "$ROOT$BIN_DIR/$b" ]] && cmp -s "$src/$b" "$ROOT$BIN_DIR/$b"; then
      continue
    fi
    install -m 0755 "$src/$b" "$ROOT$BIN_DIR/.$b.new"
    mv -f "$ROOT$BIN_DIR/.$b.new" "$ROOT$BIN_DIR/$b"
    any=1
  done
  BIN_SOURCE_DIR="$src" # render_template prefers FROM_DIR; update.sh (B2) reads this
  ((any)) && log_changed "panel, agent, files installed" || log_skip "unchanged"
}
```

`bin_source` läuft in einer Subshell (`$(…)`); scheitert dort `die`, gibt `bin_install` mit `|| exit 1` den Fehler weiter, bevor eine Datei unter `$BIN_DIR` entsteht. `install` setzt im Trockenlauf keinen Besitzer (Dateien gehören dem Testbenutzer; in echt root, weil der Installer als root läuft).

- [ ] **Step 5: Tests für die Konfiguration (rot)**

`scripts/install/tests/install_config.sh`:

```bash
# shellcheck shell=bash
# Tests for config.sh (spec §4.5, review focus 2).

cfg_fixture() {
  stub id '[[ "${1:-}" == -u && "${2:-}" == mcpanel ]] && echo 998 || echo 0'
  PKG_RESTIC_BIN=/usr/bin/restic
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com
  mkdir -p "$ROOT$ETC_PANEL" "$ROOT$ETC_FILES"
}

test_cfg_keys_exports_template_values() {
  cfg_fixture
  cfg_keys
  assert_eq 998 "$KEY_PANEL_UID"
  assert_eq https://panel.example.com "$KEY_PUBLIC_URL"
  assert_eq /srv/mc-panel "$KEY_INSTANCES_DIR"
  assert_eq /usr/bin/restic "$KEY_RESTIC_BINARY"
  assert_eq 127.0.0.1:25565 "$KEY_PING_ADDR"
}

test_cfg_ensure_writes_missing_files() {
  cfg_fixture
  cfg_ensure
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'public_url = "https://panel.example.com"'
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'listen = "unix:/run/mc-panel-web/panel.sock"'
  assert_file_contains "$ROOT$ETC_PANEL/agent.toml" 'panel_uid = 998'
  assert_file_contains "$ROOT$ETC_FILES/files.toml" 'restic_binary = "/usr/bin/restic"'
  assert_eq 600 "$(stat -c '%a' "$ROOT$ETC_PANEL/agent.toml")"
  assert_eq 640 "$(stat -c '%a' "$ROOT$ETC_FILES/files.toml")"
}

test_cfg_ensure_keeps_operator_values() {
  cfg_fixture
  cfg_ensure
  sed -i 's|^public_url = .*|public_url = "https://other.example.org"|' "$ROOT$ETC_PANEL/panel.toml"
  cfg_ensure
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'public_url = "https://other.example.org"'
}

test_cfg_ensure_adds_only_missing_keys() {
  cfg_fixture
  cfg_ensure
  grep -v '^monitoring_ping_addr' "$ROOT$ETC_PANEL/panel.toml" >"$WORK/p" && mv "$WORK/p" "$ROOT$ETC_PANEL/panel.toml"
  cfg_ensure
  assert_file_contains "$ROOT$ETC_PANEL/panel.toml" 'monitoring_ping_addr = "127.0.0.1:25565"'
  assert_eq 1 "$(grep -c '^listen' "$ROOT$ETC_PANEL/panel.toml")"
}

test_cfg_restic_binary_from_path_on_update() {
  cfg_fixture
  PKG_RESTIC_BIN="" # update does not run pkg_install
  stub cfg_restic_path 'echo /usr/local/bin/restic'
  cfg_keys
  assert_eq /usr/local/bin/restic "$KEY_RESTIC_BINARY"
}

test_cfg_ensure_second_run_unchanged() {
  cfg_fixture
  cfg_ensure >/dev/null
  assert_contains "$(cfg_ensure)" unchanged
}
```

- [ ] **Step 6: `config.sh` schreiben**

```bash
# shellcheck shell=bash
# config.sh – spec §4.5: create panel.toml, agent.toml and files.toml from the templates when
# missing; in existing files only append missing required keys (never change a value).

# Required top-level keys and their TOML values (rendered from KEY_* by cfg_keys).
declare -gA CFG_REQUIRED_PANEL CFG_REQUIRED_AGENT CFG_REQUIRED_FILES

cfg_keys() {
  local uid
  uid="$(id -u mcpanel 2>/dev/null)" || die "user mcpanel is missing (layout step did not run?)"
  export KEY_DOMAIN="$DOMAIN"
  export KEY_PUBLIC_URL="https://$DOMAIN"
  export KEY_PANEL_UID="$uid"
  export KEY_INSTANCES_DIR="$INSTANCES_DIR"
  # pkg_install found it (install); on update the restic from PATH, else the Ubuntu path.
  export KEY_RESTIC_BINARY="${PKG_RESTIC_BIN:-$(cfg_restic_path)}"
  KEY_RESTIC_BINARY="${KEY_RESTIC_BINARY:-/usr/bin/restic}"
  export KEY_PING_ADDR=127.0.0.1:25565
  export KEY_VERSION
  KEY_VERSION="$(bin_version)"
  if [[ "$TLS_MODE" == internal ]]; then
    export KEY_TLS="tls internal"
  else
    export KEY_TLS="tls $EMAIL"
  fi
  CFG_REQUIRED_PANEL=(
    [listen]='"unix:/run/mc-panel-web/panel.sock"'
    [public_url]="\"$KEY_PUBLIC_URL\""
    [data_dir]="\"$DATA_DIR\""
    [agent_socket]='"/run/mc-panel/agent.sock"'
    [files_socket]='"/run/mc-panel-files/files.sock"'
    [trusted_proxies]='["127.0.0.1/32", "::1/128"]'
    [monitoring_ping_addr]="\"$KEY_PING_ADDR\""
    [instances_dir]="\"$INSTANCES_DIR\""
  )
  CFG_REQUIRED_AGENT=(
    [socket]='"/run/mc-panel/agent.sock"'
    [panel_uid]="$uid"
    [firewall]='"nftables"'
    [instances_dir]="\"$INSTANCES_DIR\""
    [mc_user]='"mcpanel-mc"'
  )
  CFG_REQUIRED_FILES=(
    [socket]='"/run/mc-panel-files/files.sock"'
    [panel_uid]="$uid"
    [instances_dir]="\"$INSTANCES_DIR\""
    [restic_binary]="\"$KEY_RESTIC_BINARY\""
  )
}

# cfg_restic_path prints the restic from PATH ("" if none; a query, tests stub it).
cfg_restic_path() {
  command -v restic 2>/dev/null || true
}

# cfg_file TEMPLATE DEST MODE OWNER GROUP ARRAYNAME prints changed/unchanged. DEST is a system
# path WITHOUT $ROOT (write_file and toml_ensure_key add it, overview V2).
cfg_file() {
  local tpl="$1" dest="$2" mode="$3" owner="$4" group="$5" key r changed=0
  local -n req="$6"
  if [[ ! -e "$ROOT$dest" ]]; then
    render_template "$tpl" | write_file "$dest" "$mode" "$owner" "$group" >/dev/null
    echo changed
    return 0
  fi
  for key in $(printf '%s\n' "${!req[@]}" | sort); do
    r="$(toml_ensure_key "$dest" "$key" "${req[$key]}")"
    [[ "$r" == changed ]] && changed=1
  done
  # mode/owner of an existing file: write_file with the unchanged content fixes them.
  r="$(write_file "$dest" "$mode" "$owner" "$group" <"$ROOT$dest")"
  [[ "$r" == changed ]] && changed=1
  ((changed)) && echo changed || echo unchanged
}

cfg_ensure() {
  log_step "configuration"
  cfg_keys
  local a b c
  a="$(cfg_file config/panel.toml "$ETC_PANEL/panel.toml" 0640 root mcpanel CFG_REQUIRED_PANEL)" || return 1
  b="$(cfg_file config/agent.toml "$ETC_PANEL/agent.toml" 0600 root root CFG_REQUIRED_AGENT)" || return 1
  c="$(cfg_file config/files.toml "$ETC_FILES/files.toml" 0640 root mcpanel-mc CFG_REQUIRED_FILES)" || return 1
  if [[ "$a$b$c" == *changed* && "$a$b$c" != unchangedunchangedunchanged ]]; then
    log_changed "panel.toml: $a, agent.toml: $b, files.toml: $c"
  else
    log_skip "unchanged"
  fi
}
```

Hinweis: `cfg_keys` und die Arrays `CFG_REQUIRED_*` nutzt auch `doctor` (B2) für die Prüfung der Pflichtschlüssel – dieselben Listen. `cfg_keys` setzt die `KEY_*` als exportierte Variablen, damit `render_template` (Phase A) sie in Subshells sieht. Die Liste der Pflichtschlüssel ist mit `internal/config` abgeglichen (Panel: `listen`, `public_url`, `data_dir`, `agent_socket`, `files_socket`, `trusted_proxies`; TP5: `monitoring_ping_addr`, `instances_dir`; agent: `socket`, `panel_uid`, `firewall`, `instances_dir`, `mc_user`; files: `socket`, `panel_uid`, `instances_dir`, `restic_binary`). Vor dem Abtippen in Step 0 von Aufgabe 4 gegen `internal/config/*.go` auf `main` prüfen – TP5 muss gemerged sein.

- [ ] **Step 7: Tests grün**

Run: `make check`, dann `make test-vm SUITE=lint`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add scripts/install/layout.sh scripts/install/binaries.sh scripts/install/config.sh scripts/install/tests/install_layout.sh scripts/install/tests/install_binaries.sh scripts/install/tests/install_config.sh
git commit -m "feat(installer): users, directories, verified binaries and configuration

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 6: Units, Caddy, Firewall

**Files:**
- Create: `scripts/install/units.sh`, `scripts/install/caddy.sh`, `scripts/install/firewall.sh`
- Create: `scripts/install/tests/install_units.sh`, `scripts/install/tests/install_caddy.sh`, `scripts/install/tests/install_firewall.sh`

**Interfaces:**
- Consumes: V2 (`render_template`, `write_file`, `sys_systemctl`, `sys_caddy`, `sys_nft`, `systemd_wait_active`, `confirm`), V3 (Vorlagen `systemd/*`, `caddy/mc-panel.caddy`, `nftables/mcpanel_fw.nft`, `KEY_TLS`, `KEY_DOMAIN`, `KEY_SSH_PORT`), `cfg_keys` (Aufgabe 5), Variablen `SSH_PORT`, `NO_FIREWALL`, `TLS_MODE`, `DOMAIN`.
- Produces:
  - `UNIT_FILES=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service)`; `UNIT_START_ORDER=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service)`; `unit_ensure`.
  - `cad_ensure`; `cad_is_package_default FILE` (0/1); `cad_wait` (Warnung, wenn nach 120 s kein Zertifikat).
  - `fw_ssh_ports` (gibt die Ports durch Leerzeichen getrennt aus); `fw_session_port` (Server-Port aus `$SSH_CONNECTION`, leer ohne Sitzung); `fw_ensure`.

- [ ] **Step 1: Tests für Units (rot)**

`scripts/install/tests/install_units.sh`:

```bash
# shellcheck shell=bash
# Tests for units.sh (spec §4.6).

unit_fixture() {
  render_template() { printf '# %s\n' "$1"; }
  systemd_wait_active() { return 0; }
}

test_unit_ensure_writes_reloads_enables_starts_in_order() {
  unit_fixture
  unit_ensure
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-panel.socket" '# systemd/mcpanel-panel.socket'
  assert_file_contains "$ROOT$UNIT_DIR/mcpanel-fw.service" '# systemd/mcpanel-fw.service'
  assert_called 'systemctl daemon-reload'
  assert_called 'systemctl enable mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service'
  local order
  order="$(grep -E '^systemctl (restart|start) ' "$WORK/calls.log" | awk '{print $3}' | xargs)"
  assert_eq "mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service" "$order"
}

test_unit_ensure_never_enables_the_firewall_unit() {
  # Only fw_ensure enables/starts mcpanel-fw.service (never with --no-firewall, review #11).
  unit_fixture
  unit_ensure
  assert_not_called 'mcpanel-fw.service'
}

test_unit_ensure_on_update_only_writes_and_reloads() {
  # update restarts and checks health itself (overview V4, review #2).
  unit_fixture
  CMD=update
  unit_ensure
  assert_called 'systemctl daemon-reload'
  assert_not_called 'systemctl enable'
  assert_not_called 'systemctl start'
  assert_not_called 'systemctl restart'
}

test_unit_ensure_second_run_does_not_restart() {
  unit_fixture
  unit_ensure >/dev/null
  : >"$WORK/calls.log"
  unit_ensure
  assert_not_called 'daemon-reload'
  assert_not_called 'systemctl restart'
  assert_called 'systemctl start mcpanel-agent.service'
}

test_unit_ensure_restarts_only_changed() {
  unit_fixture
  unit_ensure >/dev/null
  : >"$WORK/calls.log"
  render_template() { [[ "$1" == systemd/mcpanel-files.service ]] && echo changed || printf '# %s\n' "$1"; }
  unit_ensure
  assert_called 'systemctl restart mcpanel-files.service'
  assert_not_called 'systemctl restart mcpanel-agent.service'
}

test_unit_ensure_fails_when_unit_does_not_come_up() {
  unit_fixture
  systemd_wait_active() { [[ "$1" != mcpanel-panel.service ]]; }
  assert_fails unit_ensure
}
```

- [ ] **Step 2: `units.sh` schreiben**

```bash
# shellcheck shell=bash
# units.sh – spec §4.6: systemd units from deploy/systemd/, started agent → files → panel.
# mcpanel-fw.service is only written here; fw_ensure enables and starts it (never with
# --no-firewall). With CMD=update this step only writes and reloads: update restarts the
# services and checks their health itself (overview V4).

UNIT_FILES=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service
  mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service)
UNIT_START_ORDER=(mcpanel-agent.service mcpanel-files.socket mcpanel-files.service
  mcpanel-panel.socket mcpanel-panel.service)

unit_ensure() {
  log_step "systemd units"
  local u r changed=()
  mkdir -p "$ROOT$UNIT_DIR"
  for u in "${UNIT_FILES[@]}"; do
    # Paths without $ROOT: write_file adds it (overview V2). A failure returns 1 so that
    # update's subshell rolls back instead of dying halfway (overview V4).
    r="$(render_template "systemd/$u" | write_file "$UNIT_DIR/$u" 0644 root root)" || return 1
    [[ "$r" == changed ]] && changed+=("$u")
  done
  if ((${#changed[@]} > 0)); then
    sys_systemctl daemon-reload
  fi
  if [[ "${CMD:-install}" == update ]]; then
    if ((${#changed[@]} > 0)); then log_changed "${changed[*]} (restart by update)"; else log_skip "unchanged"; fi
    return 0
  fi
  sys_systemctl enable "${UNIT_START_ORDER[@]}"
  for u in "${UNIT_START_ORDER[@]}"; do
    if [[ " ${changed[*]} " == *" $u "* ]]; then
      sys_systemctl restart "$u"
    else
      sys_systemctl start "$u"
    fi
    if ! systemd_wait_active "$u" 60; then
      warn "$u did not become active (journalctl -u ${u%.*})"
      return 1
    fi
  done
  if ((${#changed[@]} > 0)); then
    log_changed "${changed[*]}"
  else
    log_skip "unchanged, all active"
  fi
}
```

Ein geänderter Socket (`*.socket`) wird mit `restart` neu angelegt; der zugehörige Dienst folgt in der Reihenfolge direkt danach. `start` auf einen laufenden Dienst ist ein No-op.

- [ ] **Step 3: Tests für Caddy (rot)**

`scripts/install/tests/install_caddy.sh`:

```bash
# shellcheck shell=bash
# Tests for caddy.sh (spec §4.7, review focus 2).

cad_fixture() {
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com
  id() { echo 998; }
  render_template() { printf '%s {\n\t%s\n\treverse_proxy unix//run/mc-panel-web/panel.sock\n}\n' "$KEY_DOMAIN" "$KEY_TLS"; }
  cad_wait() { :; }
  dpkg-query() { echo " /etc/caddy/Caddyfile 0123456789abcdef0123456789abcdef"; }
  mkdir -p "$ROOT$CADDY_DIR"
}

test_cad_ensure_creates_caddyfile_when_missing() {
  cad_fixture
  cad_ensure
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy # managed by mc-panel'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" 'tls a@example.com'
  assert_called 'caddy validate --config '"$ROOT$CADDY_DIR"'/Caddyfile --adapter caddyfile'
  assert_called 'systemctl reload-or-restart caddy'
}

test_cad_ensure_appends_import_once_to_foreign_caddyfile() {
  cad_fixture
  printf 'blog.example.com {\n\troot * /srv/blog\n\tfile_server\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure
  cad_ensure
  assert_eq 1 "$(grep -c '^import mc-panel.caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy # managed by mc-panel'
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'root * /srv/blog'
  assert_eq 'blog.example.com {' "$(head -n 1 "$ROOT$CADDY_DIR/Caddyfile")"
}

test_cad_ensure_marks_an_existing_unmarked_import() {
  # e.g. the test VM frame or an operator wrote the line without the marker: replaced, not doubled
  cad_fixture
  printf 'blog.example.com {\n\tfile_server\n}\nimport mc-panel.caddy\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cad_ensure
  assert_eq 1 "$(grep -c '^import mc-panel.caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy # managed by mc-panel'
}

test_cad_ensure_replaces_untouched_package_default() {
  cad_fixture
  printf ':80 {\n\troot * /usr/share/caddy\n\tfile_server\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
  local md5
  md5="$(md5sum "$ROOT$CADDY_DIR/Caddyfile" | cut -d' ' -f1)"
  dpkg-query() { echo " /etc/caddy/Caddyfile $md5"; }
  cad_ensure
  assert_eq 0 "$(grep -c '/usr/share/caddy' "$ROOT$CADDY_DIR/Caddyfile")"
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'import mc-panel.caddy'
}

test_cad_ensure_internal_sets_skip_install_trust_only_in_own_file() {
  cad_fixture
  TLS_MODE=internal EMAIL=''
  cad_ensure
  assert_file_contains "$ROOT$CADDY_DIR/Caddyfile" 'skip_install_trust'
  assert_file_contains "$ROOT$CADDY_DIR/mc-panel.caddy" 'tls internal'
}

test_cad_ensure_restores_caddyfile_when_validate_fails() {
  cad_fixture
  printf 'blog.example.com {\n\tfile_server\n}\n' >"$ROOT$CADDY_DIR/Caddyfile"
  cp "$ROOT$CADDY_DIR/Caddyfile" "$WORK/before"
  sys_caddy() { return 1; }
  assert_fails cad_ensure
  cmp -s "$WORK/before" "$ROOT$CADDY_DIR/Caddyfile" || assert_eq same different "Caddyfile restored"
  assert_no_file "$ROOT$CADDY_DIR/mc-panel.caddy"
}

test_cad_ensure_second_run_does_not_reload() {
  cad_fixture
  cad_ensure >/dev/null
  : >"$WORK/calls.log"
  cad_ensure
  assert_not_called 'reload-or-restart caddy'
}
```

- [ ] **Step 4: `caddy.sh` schreiben**

```bash
# shellcheck shell=bash
# caddy.sh – spec §4.7: the panel site lives in /etc/caddy/mc-panel.caddy; the Caddyfile only
# gets one import line. Foreign sites stay untouched; an untouched package default is replaced.

# The exact line of the contract (overview V3); uninstall removes exactly this line.
CAD_IMPORT='import mc-panel.caddy # managed by mc-panel'
CAD_IMPORT_RE='^import mc-panel\.caddy([[:space:]]|$)'

# cad_is_package_default FILE: 0 if FILE is Ubuntu's unmodified Caddyfile (conffile md5).
cad_is_package_default() {
  local want got
  want="$(dpkg-query -W -f='${Conffiles}\n' caddy 2>/dev/null | awk '$1 == "/etc/caddy/Caddyfile" {print $2}')"
  [[ -n "$want" ]] || return 1
  got="$(md5sum "$1" | cut -d' ' -f1)"
  [[ "$got" == "$want" ]]
}

cad_own_caddyfile() {
  if [[ "$TLS_MODE" == internal ]]; then
    printf '{\n\tskip_install_trust\n}\n\n'
  fi
  printf '%s\n' "$CAD_IMPORT"
}

# cad_wait polls the panel through Caddy for up to 120 s; only a warning on failure.
cad_wait() {
  local i opts=(-fsS -o /dev/null --max-time 5)
  [[ "$TLS_MODE" == internal ]] && opts+=(-k --resolve "$DOMAIN:443:127.0.0.1")
  for ((i = 0; i < 24; i++)); do
    curl "${opts[@]}" "https://$DOMAIN/api/health" 2>/dev/null && return 0
    sleep 5
  done
  warn "https://$DOMAIN/api/health does not answer after 120 s (DNS, port 80/443 or Let's Encrypt?):"
  journalctl -u caddy -n 20 --no-pager >&2 || true
}

cad_ensure() {
  log_step "caddy"
  cfg_keys
  local dir="$ROOT$CADDY_DIR" file site_r file_r=unchanged
  ensure_dir "$CADDY_DIR" 0755 root root >/dev/null
  cp -p "$dir/Caddyfile" "$WORK/Caddyfile.before" 2>/dev/null || rm -f "$WORK/Caddyfile.before"
  cp -p "$dir/mc-panel.caddy" "$WORK/site.before" 2>/dev/null || rm -f "$WORK/site.before"
  # write_file gets paths without $ROOT (overview V2)
  site_r="$(render_template caddy/mc-panel.caddy | write_file "$CADDY_DIR/mc-panel.caddy" 0644 root root)"
  file="$dir/Caddyfile"
  if [[ ! -e "$file" ]] || cad_is_package_default "$file"; then
    file_r="$(cad_own_caddyfile | write_file "$CADDY_DIR/Caddyfile" 0644 root root)"
  elif ! grep -qxF "$CAD_IMPORT" "$file"; then
    if grep -qE "$CAD_IMPORT_RE" "$file"; then
      # an unmarked import line: mark it instead of adding a second one
      awk -v re="$CAD_IMPORT_RE" -v line="$CAD_IMPORT" '$0 ~ re && !done { print line; done = 1; next } $0 ~ re { next } { print }' "$file" |
        write_file "$CADDY_DIR/Caddyfile" 0644 root root >/dev/null
    else
      { cat "$file"; printf '\n%s\n' "$CAD_IMPORT"; } | write_file "$CADDY_DIR/Caddyfile" 0644 root root >/dev/null
    fi
    file_r=changed
  fi
  if [[ "$site_r" == unchanged && "$file_r" == unchanged ]]; then
    log_skip "unchanged"
    return 0
  fi
  if ! sys_caddy validate --config "$file" --adapter caddyfile; then
    if [[ -e "$WORK/Caddyfile.before" ]]; then cp -p "$WORK/Caddyfile.before" "$file"; else rm -f "$file"; fi
    if [[ -e "$WORK/site.before" ]]; then cp -p "$WORK/site.before" "$dir/mc-panel.caddy"; else rm -f "$dir/mc-panel.caddy"; fi
    die "caddy rejected the configuration; the previous Caddyfile is back in place"
  fi
  sys_systemctl reload-or-restart caddy
  log_changed "site $DOMAIN ($TLS_MODE)"
  cad_wait
}
```

`cad_wait` läuft im Trockenlauf nicht wirklich (Tests überschreiben es); im echten Lauf nutzt er `curl`, `sleep` und `journalctl` direkt, weil sie nichts verändern.

- [ ] **Step 5: Tests für die Firewall (rot)**

`scripts/install/tests/install_firewall.sh`:

```bash
# shellcheck shell=bash
# Tests for firewall.sh (spec §4.8, review focus 1: never lock out the running session).

fw_fixture() {
  render_template() { printf 'table inet mcpanel_fw {}\ndelete table inet mcpanel_fw\ntable inet mcpanel_fw { chain input { tcp dport { %s } accept } }\n' "$KEY_SSH_PORT"; }
  id() { echo 998; }
  DOMAIN=panel.example.com TLS_MODE=letsencrypt EMAIL=a@example.com NO_FIREWALL=0 SSH_PORT=''
  mkdir -p "$ROOT$ETC_PANEL"
}

test_fw_ssh_ports_reads_all_ports() {
  sshd() { printf 'port 22\nport 2222\naddressfamily any\n'; }
  assert_eq "22 2222" "$(fw_ssh_ports)"
}

test_fw_ssh_ports_prefers_argument() {
  SSH_PORT=2200
  sshd() { echo "port 22"; }
  assert_eq 2200 "$(fw_ssh_ports)"
}

test_fw_session_port_ipv4_and_ipv6() {
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  assert_eq 2222 "$(fw_session_port)"
  SSH_CONNECTION="2001:db8::4 50122 2001:db8::7 22"
  assert_eq 22 "$(fw_session_port)"
  unset SSH_CONNECTION
  assert_eq '' "$(fw_session_port)"
}

test_fw_ensure_loads_rules_with_ssh_ports() {
  fw_fixture
  sshd() { echo "port 2222"; }
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  fw_ensure
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 2222 } accept'
  assert_called 'nft -c -f '"$ROOT$ETC_PANEL"'/mcpanel_fw.nft'
  assert_called 'systemctl restart mcpanel-fw.service'
}

test_fw_ensure_aborts_when_session_port_not_allowed() {
  fw_fixture
  sshd() { echo "port 22"; }
  SSH_CONNECTION="198.51.100.4 50122 203.0.113.7 2222"
  assert_fails fw_ensure
  assert_not_called 'mcpanel-fw'
  assert_no_file "$ROOT$ETC_PANEL/mcpanel_fw.nft"
}

test_fw_ensure_aborts_without_known_ssh_port() {
  fw_fixture
  sshd() { return 1; }
  unset SSH_CONNECTION
  assert_fails fw_ensure
  assert_not_called 'mcpanel-fw'
}

test_fw_ensure_uses_session_port_when_sshd_unavailable() {
  fw_fixture
  sshd() { return 1; }
  SSH_CONNECTION="2001:db8::4 50122 2001:db8::7 2022"
  fw_ensure
  assert_file_contains "$ROOT$ETC_PANEL/mcpanel_fw.nft" 'tcp dport { 2022 } accept'
}

test_fw_ensure_aborts_when_ruleset_invalid() {
  fw_fixture
  sshd() { echo "port 22"; }
  sys_nft() { return 1; }
  assert_fails fw_ensure
  assert_not_called 'restart mcpanel-fw'
}

# main never calls fw_ensure with --no-firewall (overview V4); unit_ensure only writes the
# unit, so no firewall is enabled then (review #11 – see test_unit_ensure_never_enables_the_firewall_unit).

test_fw_ensure_second_run_does_not_reload() {
  fw_fixture
  sshd() { echo "port 22"; }
  fw_ensure >/dev/null
  : >"$WORK/calls.log"
  fw_ensure
  assert_not_called 'restart mcpanel-fw'
  assert_called 'systemctl enable --now mcpanel-fw.service'
}
```

- [ ] **Step 6: `firewall.sh` schreiben**

```bash
# shellcheck shell=bash
# firewall.sh – spec §4.8: own nftables table inet mcpanel_fw (never inet mcpanel of the agent).
# Order of safety: know the SSH port(s), make sure the current session's port is among them,
# check the ruleset with nft -c, and only then load it. Established connections are accepted
# first in the ruleset, so the running session survives the load.

FW_RULES_FILE="$ETC_PANEL/mcpanel_fw.nft"

# fw_ssh_ports prints the SSH ports to allow (space separated): --ssh-port, else every port of
# sshd -T, else the port of the current session; nothing if none is known.
fw_ssh_ports() {
  if [[ -n "$SSH_PORT" ]]; then
    echo "$SSH_PORT"
    return 0
  fi
  local ports
  ports="$(sshd -T 2>/dev/null | awk '$1 == "port" {print $2}' | sort -un | xargs)"
  if [[ -z "$ports" ]]; then
    ports="$(fw_session_port)"
  fi
  echo "$ports"
}

# fw_session_port prints the server port of the SSH session this installer runs in.
fw_session_port() {
  [[ -n "${SSH_CONNECTION:-}" ]] || return 0
  local _client _cport _server sport
  read -r _client _cport _server sport <<<"$SSH_CONNECTION"
  echo "${sport:-}"
}

fw_ensure() {
  log_step "firewall"
  local ports p session list
  ports="$(fw_ssh_ports)"
  [[ -n "$ports" ]] || die "cannot determine the SSH port; pass --ssh-port"
  for p in $ports; do
    [[ "$p" =~ ^[0-9]+$ ]] && ((p >= 1 && p <= 65535)) || die "invalid SSH port $p"
  done
  session="$(fw_session_port)"
  if [[ -n "$session" && " $ports " != *" $session "* ]]; then
    die "this SSH session uses port $session, which the firewall would block (allowed: $ports); pass --ssh-port"
  fi
  list="${ports// /, }"
  export KEY_SSH_PORT="$list"
  local tmp="$WORK/mcpanel_fw.nft" r
  render_template nftables/mcpanel_fw.nft >"$tmp"
  sys_nft -c -f "$tmp" || die "nft rejected the ruleset; nothing was loaded"
  r="$(write_file "$FW_RULES_FILE" 0644 root root <"$tmp")"
  sys_nft -c -f "$ROOT$FW_RULES_FILE" || die "nft rejected the ruleset; nothing was loaded"
  # Only here is the firewall unit enabled (unit_ensure just writes it).
  if [[ "$r" == changed ]]; then
    sys_systemctl enable mcpanel-fw.service
    sys_systemctl restart mcpanel-fw.service
    log_changed "allowed: SSH $list, 80, 443, 25565 (tcp), 443 (udp)"
  else
    sys_systemctl enable --now mcpanel-fw.service
    log_skip "unchanged"
  fi
}
```

Erwartete Vorlage `deploy/nftables/mcpanel_fw.nft` (Phase A, V3; hier zur Prüfung beim Abtippen):

```
table inet mcpanel_fw {}
delete table inet mcpanel_fw
table inet mcpanel_fw {
	chain input {
		type filter hook input priority 0; policy drop;
		ct state established,related accept
		ct state invalid drop
		iif "lo" accept
		meta l4proto icmp accept
		meta l4proto ipv6-icmp accept
		tcp dport { {{SSH_PORT}} } accept
		tcp dport { 80, 443, 25565 } accept
		udp dport 443 accept
	}
}
```

Weicht die Vorlage aus Phase A ab (z. B. fehlender `delete`-Kopf), im PR vermerken und den Lead fragen (Datei gehört Phase A). Der Testlauf in der Firewall-Prüfung ist zweimal `nft -c`: einmal auf die Rohfassung in `$WORK`, einmal auf die geschriebene Datei – so bleibt bei einem Fehler nichts Halbes unter `/etc/mc-panel` liegen (die erste Prüfung scheitert vor `write_file`).

- [ ] **Step 7: Tests grün**

Run: `make check`, dann `make test-vm SUITE=lint`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add scripts/install/units.sh scripts/install/caddy.sh scripts/install/firewall.sh scripts/install/tests/install_units.sh scripts/install/tests/install_caddy.sh scripts/install/tests/install_firewall.sh
git commit -m "feat(installer): systemd units, Caddy site and host firewall

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 7: Erster Admin

**Files:**
- Create: `scripts/install/admin.sh`
- Create: `scripts/install/tests/install_admin.sh`

**Interfaces:**
- Consumes: V1 (`panel admin list`, `panel admin create -config … -username NAME` mit Passwort auf stdin), V2 (`run_as`, `die`, `warn`, `log_*`, `$TTY`), V4 (`ADMIN_USER`, `ADMIN_PASSWORD_FILE` – `main.sh` hat geprüft, dass die Datei 0600 ist und `--admin-user` gesetzt ist), Variablen `ETC_PANEL`, `BIN_DIR`.
- Produces:
  - `adm_ensure` – legt den ersten Admin an, wenn `panel admin list` nichts ausgibt. Passwortquelle in dieser Reihenfolge: `ADMIN_PASSWORD_FILE`, stdin (nur mit `--admin-user`, wenn `install.sh` aus einer Datei läuft und stdin kein Terminal ist), Terminal `$TTY`. Ohne Admin-Angaben und ohne Terminal: `[warn]` und weiter (V4) – die Abschlussausgabe von Phase A (`install_summary`) nennt dann den Befehl zum Nachholen.
  - `adm_read_line PROMPT`, `adm_read_secret PROMPT` (lesen von `$TTY`), `adm_has_tty`, `adm_script_is_file`.
  - Keine eigene Abschlussausgabe: die macht `install_summary` aus Phase A.

- [ ] **Step 1: Tests (rot)**

`scripts/install/tests/install_admin.sh`:

```bash
# shellcheck shell=bash
# Tests for admin.sh (spec §4.10, overview V4).

adm_fixture() {
  run_as() {
    shift
    printf 'run_as %s\n' "$*" >>"$WORK/calls.log"
    case "$*" in
    *"admin list"*) printf '%s' "${ADM_EXISTING:-}" ;;
    *"admin create"*) cat >"$WORK/created.pw" ;;
    esac
  }
}

test_adm_ensure_skips_when_admin_exists() {
  adm_fixture
  ADM_EXISTING=$'jannes\n'
  adm_ensure
  assert_not_called 'admin create'
}

test_adm_ensure_takes_password_file() {
  adm_fixture
  ADMIN_USER=admin
  printf 'file-secret-1234\n' >"$WORK/pw"
  chmod 0600 "$WORK/pw"
  ADMIN_PASSWORD_FILE="$WORK/pw"
  adm_ensure </dev/null
  assert_called 'admin create -config /etc/mc-panel/panel.toml -username admin'
  assert_eq 'file-secret-1234' "$(cat "$WORK/created.pw")"
}

test_adm_ensure_creates_with_user_and_stdin_password() {
  adm_fixture
  ADMIN_USER=admin
  stub adm_script_is_file 'return 0'
  adm_ensure <<<'s3cret-pass-1234'
  assert_eq 's3cret-pass-1234' "$(cat "$WORK/created.pw")"
}

test_adm_ensure_never_reads_stdin_of_piped_script() {
  adm_fixture
  ADMIN_USER=admin
  stub adm_script_is_file 'return 1'
  stub adm_has_tty 'return 0'
  stub adm_read_secret 'echo from-tty-1234567'
  adm_ensure <<<'#!/usr/bin/env bash'
  assert_eq 'from-tty-1234567' "$(cat "$WORK/created.pw")"
}

test_adm_ensure_prompts_when_interactive() {
  adm_fixture
  ADMIN_USER=''
  stub adm_has_tty 'return 0'
  stub adm_read_line 'echo admin2'
  stub adm_read_secret 'echo tty-secret-98765'
  adm_ensure
  assert_called '-username admin2'
}

test_adm_ensure_retries_mismatched_passwords() {
  adm_fixture
  ADMIN_USER=''
  stub adm_has_tty 'return 0'
  stub adm_read_line 'echo admin3'
  # each call runs in a subshell: count in a file, not in a variable
  echo 0 >"$WORK/n"
  adm_read_secret() {
    local n
    n=$(($(cat "$WORK/n") + 1))
    echo "$n" >"$WORK/n"
    case "$n" in 1) echo aaaaaaaaaaaa ;; 2) echo bbbbbbbbbbbb ;; *) echo cccccccccccc ;; esac
  }
  adm_ensure
  assert_eq cccccccccccc "$(cat "$WORK/created.pw")"
}

test_adm_ensure_warns_and_continues_without_tty_and_user() {
  # CI and other non-interactive runs must not hang or fail (overview V4).
  adm_fixture
  ADMIN_USER=''
  stub adm_has_tty 'return 1'
  local out
  out="$(adm_ensure </dev/null)"
  assert_contains "$out" "[warn]"
  assert_not_called 'admin create'
}
```

- [ ] **Step 2: `admin.sh` schreiben**

```bash
# shellcheck shell=bash
# admin.sh – spec §4.10: the first admin. With `curl | bash` stdin is the script itself, so the
# password comes from --admin-password-file, from stdin only when install.sh runs from a file,
# or from the terminal ($TTY from lib.sh). Without any of them the step warns and continues.

adm_script_is_file() { [[ -f "${BASH_SOURCE[-1]:-}" ]]; }
adm_has_tty() { [[ -r "$TTY" && -w "$TTY" ]] && : <"$TTY" 2>/dev/null; }

adm_read_line() { # PROMPT
  local v
  printf '%s' "$1" >"$TTY"
  IFS= read -r v <"$TTY"
  echo "$v"
}

adm_read_secret() { # PROMPT
  local v
  printf '%s' "$1" >"$TTY"
  IFS= read -rs v <"$TTY"
  printf '\n' >"$TTY"
  echo "$v"
}

# adm_password USER prints the password of the new admin (non-zero if there is none).
adm_password() {
  local a b i
  if [[ -n "${ADMIN_PASSWORD_FILE:-}" ]]; then
    IFS= read -r a <"$ADMIN_PASSWORD_FILE" || true
    [[ -n "$a" ]] || { warn "--admin-password-file is empty"; return 1; }
    echo "$a"
    return 0
  fi
  if [[ -n "$ADMIN_USER" ]] && adm_script_is_file && [[ ! -t 0 ]]; then
    IFS= read -r a || true
    if [[ -n "$a" ]]; then
      echo "$a"
      return 0
    fi
  fi
  adm_has_tty || return 1
  for ((i = 0; i < 3; i++)); do
    a="$(adm_read_secret "password for $1: ")"
    b="$(adm_read_secret "repeat: ")"
    if [[ -n "$a" && "$a" == "$b" ]]; then
      echo "$a"
      return 0
    fi
    printf 'passwords do not match, try again\n' >"$TTY"
  done
  warn "no matching password after 3 attempts"
  return 1
}

adm_ensure() {
  log_step "first admin"
  local existing user pw
  existing="$(run_as mcpanel "$BIN_DIR/panel" admin list -config "$ETC_PANEL/panel.toml")" ||
    { warn "panel admin list failed; create the admin later with: panel admin create"; return 0; }
  if [[ -n "$existing" ]]; then
    log_skip "admin exists: $(head -n 1 <<<"$existing")"
    return 0
  fi
  user="$ADMIN_USER"
  if [[ -z "$user" ]]; then
    if ! adm_has_tty; then
      warn "no admin yet and no terminal: create one with: printf '%s\n' '<password>' | sudo -u mcpanel $BIN_DIR/panel admin create -config $ETC_PANEL/panel.toml -username <name>"
      return 0
    fi
    user="$(adm_read_line "admin user name: ")"
  fi
  if [[ -z "$user" ]] || ! pw="$(adm_password "$user")"; then
    warn "no admin was created; create one later with: sudo -u mcpanel $BIN_DIR/panel admin create -config $ETC_PANEL/panel.toml -username <name>"
    return 0
  fi
  if ! printf '%s\n' "$pw" | run_as mcpanel "$BIN_DIR/panel" admin create -config "$ETC_PANEL/panel.toml" -username "$user"; then
    warn "panel admin create failed (password policy? see the message above)"
    return 1
  fi
  log_changed "admin $user created"
}
```

`BASH_SOURCE[-1]` ist beim Lauf aus einer Datei deren Pfad (das zusammengefügte `install.sh` hat nur eine Quelle); bei `curl … | bash` ist es leer bzw. `main`. Die Tests überschreiben `adm_script_is_file`/`adm_has_tty` per `stub`.

- [ ] **Step 3: Tests grün**

Run: `make check`, dann `make test-vm SUITE=lint`
Expected: PASS

- [ ] **Step 4: Zusammengefügtes Skript prüfen**

Run: `make installer && bash -n build/install.sh && grep -c '^pre_check()\|^adm_ensure()' build/install.sh`
Expected: `2` (beide Funktionen sind im zusammengefügten Skript)

- [ ] **Step 5: Commit**

```bash
git add scripts/install/admin.sh scripts/install/tests/install_admin.sh
git commit -m "feat(installer): first admin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

- [ ] **Step 6: Vor dem PR**

Run: `make check` und `make test-vm` (alle Suites)
Dann: `git push -u origin feat/installer-install`, Draft-PR (`gh pr create --draft`, Body mit Zusammenfassung, Abweichungen 1–8, Testergebnissen; Ende `🤖 Generated with [Claude Code](https://claude.com/claude-code)` + Leerzeile + `https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`), `gh pr checks <n> --watch` bis `CI OK` grün, beim Lead melden.

## Self-Review

- **Spec-Abdeckung:** §4.1 → Aufgabe 4 (`pre_check`); §4.2 → Aufgabe 4 (`pkg_install`, `daemon.json`, restic ≥ 0.17, Docker); §4.3 → Aufgabe 5 (`lay_ensure`); §4.4 → Aufgabe 5 (`bin_install`, SHA-256, `--from-dir`, atomar); §4.5 → Aufgabe 5 (`cfg_ensure`, nur fehlende Schlüssel); §4.6 → Aufgabe 6 (`unit_ensure`, Reihenfolge, 60 s); §4.7 → Aufgabe 6 (`cad_ensure`, Import einmal, validate, Rückbau, 120-s-Warten); §4.8 → Aufgabe 6 (`fw_ensure`, eigene Tabelle, Sitzungsport, `--no-firewall`); §4.9 → Aufgabe 4 (`upg_ensure`); §4.10 → Aufgabe 7 (`adm_ensure`; Abschlussausgabe `install_summary` aus Phase A).
- **Review Focus:** RF 1 → `test_fw_ensure_aborts_when_session_port_not_allowed`, `…_without_known_ssh_port`, `…_ruleset_invalid`, IPv6-Test; RF 2 → je Schritt ein „second run unchanged“-Test, `test_cfg_ensure_keeps_operator_values`, `test_cad_ensure_appends_import_once_to_foreign_caddyfile`, `test_pkg_daemon_json_only_when_missing`; RF 4 → `test_bin_install_refuses_bad_checksum`, `…_missing_checksum_line`, `…_failed_download`, `test_bin_tar_safe_rejects_traversal`.
- **Platzhalter:** keine; alle Schritte enthalten den vollständigen Code.
- **Typen/Namen:** Präfixe je Datei wie in den Global Constraints der Übersicht; `cfg_keys` wird von `cad_ensure` und indirekt von `fw_ensure` (über `KEY_SSH_PORT`) genutzt; `bin_version` von `cfg_keys`.
