# Teilprojekt 6 – Test-VM über den Installer, CI-Job `installer`, Doku und Abschluss (Aufgaben 11–12) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der Installer wird bei jedem `make deploy-testvm` und in einem eigenen CI-Job wirklich ausgeführt. `scripts/testvm-deploy.sh` baut wie bisher und überlässt danach Benutzer, Rechte, Konfiguration, Units und Caddy dem Installer (`install` beim ersten Mal, sonst `update`). Der CI-Job `installer` spielt auf einem frischen Runner den ganzen Lebenszyklus durch: Installation, `doctor`, Idempotenz, Login über Caddy, Update, Update mit defektem Binary und automatischem Rollback, `uninstall`, Neuinstallation über vorhandene Daten, `uninstall --purge`. Dazu die Doku (Installation für Betreiber, Struktur und Tests für Entwickler, `CLAUDE.md`, Checkliste `docs/dev/acceptance-tp6.md`) und der Abschluss von Teilprojekt 6.

**Architecture:**
- Aufgabe 11 berührt nur `scripts/testvm-deploy.sh`, `scripts/ci-installer.sh` (neu) und `.github/workflows/ci.yml` (Filter `installer`, Job `installer`, `ci-ok`).
- `scripts/testvm-deploy.sh` behält, was nur die VM braucht (Build-Werkzeuge, Go, Node, Checkout, Build, Caddyfile-Rahmen mit Weiterleitung der IP, Kopie der Caddy-CA). Neu: ein Staging-Verzeichnis im Format von `--from-dir` (`panel`, `agent`, `files`, `deploy/`, `install.sh`), eine einmalige Migration der alten VM-Installation (TCP `127.0.0.1:8080`, Heredoc-Units) und der Aufruf des Installers.
- `scripts/ci-installer.sh` läuft als root auf `ubuntu-24.04` mit `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1`, `--tls internal` und Domain `panel.ci.test` (Eintrag in `/etc/hosts`). Drei Staging-Verzeichnisse: `v1` (Version `0.0.1-ci`), `v2` (`0.0.2-ci`), `broken` (`0.0.3-ci`, `panel serve` beendet sich sofort).
- Aufgabe 12 schreibt die Doku. Gesamt-Review, Deploy, Abnahme und Abschluss koordiniert der Lead.

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, `python3 -I`), `curl`, `sqlite3`, `nft`, `systemctl`, `script` (util-linux, für die Bestätigung von `--purge` über ein Pseudo-Terminal), GitHub Actions (`ubuntu-24.04`).

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` (§3.1, §4, §5, §6, §7, §8, §10) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-13-installer-00-overview.md` (V1–V5; Abschnitte „Ausführung“ und „Global Constraints“ gelten vollständig)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | C, Teammate „inst-ci-docs“, `feat/installer-ci-docs` (Aufgabe 11 und die Doku-Schritte von 12); Gesamt-Review, Fix-Runden und Abschluss koordiniert der Lead, Abschluss auf `docs/tp6-done` |
| Startet, wenn | Phasen A, B1 und B2 auf `main` gemerged |
| Dateieigentum | nur `scripts/testvm-deploy.sh`, `scripts/ci-installer.sh`, `.github/workflows/ci.yml`, `docs/**`, `README.md`, `CLAUDE.md` |
| Wo getestet wird | lokal nur `make check`, `bash -n` und `actionlint` (per `go run`); shellcheck auf der Test-VM (`ssh claude@192.168.1.117 shellcheck …` nach `make test-vm SUITE=lint`, dort ist es installiert); der ganze Installer läuft nur in der CI (Job `installer`) und – vom Lead angestoßen – per `make deploy-testvm` |
| CI-Domain | `panel.ci.test` → `127.0.0.1` über `/etc/hosts`; `--tls internal`; Caddy-CA unter `/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt` |
| CI-Versionen | `0.0.1-ci`, `0.0.2-ci`, `0.0.3-ci` (defekt), gesetzt per `-ldflags "-X main.version=…"` |
| Test-VM | Domain `panel.<ip mit Bindestrichen>.sslip.io`, `--tls internal`, `--no-firewall` (die VM ist intern, ihre Firewall bleibt wie bisher) |
| Workflow | vorhandener Filter `installer` aus Phase A, erweitert um (`^scripts/install/`, `^deploy/`, `^scripts/ci-installer\.sh$`, `^cmd/`, `^internal/(config\|unixsock)/`, `^\.goreleaser\.yaml$`, `^Makefile$`, `go.mod`/`go.sum`), neuer Job `installer` (Timeout 30 min), `ci-ok` wartet auf `installer-lint` (A) und `installer` |

## Nachträge nach dem Schnittstellen-Review (verbindlich)

Die Annahmen unten sind nach dem Review entschieden und in den Plänen 00–03 so festgelegt; die früher hier gemeldeten Probleme (Schlüssel `public_url`, JSON-Schema von `doctor`, Docker/restic auf dem Runner, `update --from-dir`, Lint-Job und Filter, Admin ohne TTY, `listen` der VM) sind damit erledigt. Neu dazu: Rollback unter `/var/lib/mc-panel-rollback/`, `--ssh-port 22` in der CI, die markierte Import-Zeile im Caddyfile-Rahmen der VM, Ausgaben unter `build/` statt `dist/`.

## Annahmen an Phase A/B1/B2 (in Schritt 1 von Aufgabe 11 gegen `main` prüfen)

1. **Ausgabeformat** (V2 legt es nicht fest): `log_changed` beginnt jede Zeile mit `[changed]`, `log_ok` mit `[ok]`, `log_skip` mit `[skip]`, `warn` mit `[warn]`. Weicht `main` ab, passt Aufgabe 11 nur den regulären Ausdruck `changed_re` an.
2. **`doctor --json`** gibt genau `{"status": "ok|warn|fail", "checks": [{"id": …, "status": "ok"|"warn"|"fail", "message": …, "hint": …}]}` aus (Vertrag, Übersicht V4); `doctor_fails()` zählt die Einträge mit `status == "fail"`.
3. **`update --from-dir DIR`** lädt nichts aus dem Netz; die Zielversion ist `DIR/panel version` (Übersicht V4). Die CI ruft immer `DIR/install.sh` selbst auf.
4. **Docker und restic auf dem Runner:** Der Runner hat Docker (moby/docker-ce) vorinstalliert; `pkg_install` installiert `docker.io` nur, wenn kein funktionierender `docker` vorhanden ist (sonst Paketkonflikt mit `containerd.io`). `restic` aus Ubuntu 24.04 ist 0.16 (< 0.17): Der Job installiert vorher restic 0.19.1 mit `scripts/install-restic.sh` nach `/usr/local/bin`, und der Installer schreibt `restic_binary` als den Pfad, den `command -v restic` nach dem Paketschritt liefert, sofern diese Version ≥ 0.17 ist. Tut B1 das nicht, scheitert der Job in der Vorprüfung – dann klärt der Lead das mit B1 (siehe „Gefundene Probleme“ am Ende).
5. **Admin ohne TTY:** `--admin-user NAME` liest das Passwort als erste Zeile von stdin (das Skript läuft hier als Datei) oder aus `--admin-password-file`; gibt es schon einen Admin, fragt der Installer nicht. Ohne Admin-Angaben und ohne Terminal meldet der Schritt `[warn]` und läuft weiter (kein Hängen).
6. **Bestätigung bei `--purge`** liest vom Terminal (`$TTY`, Default `/dev/tty`); die CI liefert sie über `script -qec` (Pseudo-Terminal) mit der Domain als Eingabe.
7. **Firewall in der CI:** ohne `sshd -T`-Ergebnis und ohne `SSH_CONNECTION` bricht `fw_ensure` ab; alle `install`-Aufrufe der CI übergeben deshalb `--ssh-port 22`.
8. **Rollback-Verzeichnis:** `/var/lib/mc-panel-rollback/` (`root:root 0700`), nicht unter `/var/lib/mc-panel`.

---

### Task 11: Test-VM über den Installer, CI-Job `installer`

**Files:**
- Create: `scripts/ci-installer.sh`
- Modify: `scripts/testvm-deploy.sh`
- Modify: `.github/workflows/ci.yml` (Job `changes`: Ausgabe und Filter `installer`; neuer Job `installer`; `ci-ok`: `needs` um `installer`)

**Interfaces:**
- Consumes:
  - `scripts/install/assemble.sh <version> <out>` (V5) → zusammengefügtes `install.sh`.
  - `install.sh install|update|doctor|uninstall` mit den Optionen aus Spec §3.1 (`--from-dir`, `--tls internal`, `--domain`, `--no-firewall`, `--admin-user`, `--yes`, `--force`, `--json`, `--purge`); Exit-Codes aus den Global Constraints der Übersicht (0, 1, 2, 3, 4).
  - Layout und Namen aus der Übersicht: Units `mcpanel-agent.service`, `mcpanel-files.socket`/`.service`, `mcpanel-panel.socket`/`.service`, `mcpanel-fw.service`; Sockets `/run/mc-panel/agent.sock`, `/run/mc-panel-files/files.sock`, `/run/mc-panel-web/panel.sock`; `/etc/caddy/mc-panel.caddy` mit Import-Zeile im Caddyfile; Tabelle `inet mcpanel_fw`; Rollback unter `/var/lib/mc-panel-rollback/` (`root:root 0700`).
  - panel-API: `POST /api/auth/login` (`{username, password}` → `200 {stage: "setup", csrf_token}` für einen Admin ohne 2FA), `GET /api/health`.
  - `panel admin list` (V1), `panel version`.
  - `scripts/install-restic.sh` (aus TP4) für restic ≥ 0.17 auf dem Runner.
- Produces:
  - `scripts/ci-installer.sh <REPO_DIR>` (als root) endet mit `PASS: installer lifecycle (install, doctor, idempotence, login, update, rollback, uninstall, reinstall, purge)`.
  - `make deploy-testvm` installiert bzw. aktualisiert über `install.sh`.

- [ ] **Step 1: Voraussetzungen auf `main` prüfen**

```bash
cd ../mc-panel-wt/feat/installer-ci-docs
git merge origin/main
ls scripts/install/ deploy/
bash scripts/install/assemble.sh 0.0.0-check /tmp/install-check.sh && bash -n /tmp/install-check.sh
grep -nE 'log_(ok|changed|skip)\(\)|^warn\(\)' scripts/install/lib.sh
grep -nE -- '--from-dir|--admin-user|--json|--purge|--force' scripts/install/main.sh
grep -nE 'command -v restic|restic_binary|docker\.io' scripts/install/packages.sh scripts/install/config.sh
grep -n 'json' scripts/install/doctor.sh | head
```

Erwartet: alle Dateien aus der Übersicht (Dateistruktur) vorhanden; `bash -n` ohne Fehler. Die Annahmen 1–6 oben gegen den Code abgleichen. Jede Abweichung im PR-Text vermerken und nur die betroffenen Helfer in `ci-installer.sh` anpassen (Abweichung bei Annahme 4 → Lead fragen, nicht selbst in B1-Dateien ändern).

- [ ] **Step 2: `scripts/ci-installer.sh` schreiben**

```bash
#!/usr/bin/env bash
# Installer acceptance for a disposable Linux CI runner (sub-project 6, spec §8, §10 criteria
# 1-5 without Let's Encrypt): builds three staging directories in the format of --from-dir
# (v1 0.0.1-ci, v2 0.0.2-ci, broken 0.0.3-ci whose panel exits at once), then runs the whole
# lifecycle of install.sh as root: install (tls internal, firewall on), doctor without
# failures, a second install that changes nothing, sign-in through Caddy, update to v2,
# update to the broken build (health check fails, automatic rollback to v2 with the same
# database), uninstall (services, units, Caddy site, nftables tables and mc-panel containers
# gone, data and config kept, foreign container and foreign table untouched), install again
# over the kept data (no admin prompt, old password still valid), and uninstall --purge with
# the domain typed in (a wrong answer keeps everything).
#
# Usage (as root): scripts/ci-installer.sh REPO_DIR
# Changes the host (packages, users, systemd units, Caddy, nftables, /etc/hosts, ports 80, 443
# and 25565): disposable CI runners only.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: scripts/ci-installer.sh REPO_DIR" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ] || [ "$(uname -s)" != Linux ]; then
  echo "ci-installer: Linux and root only" >&2
  exit 1
fi
repo="$(cd "$1" && pwd)"
work="$(mktemp -d)"
chmod 0755 "$work"
domain="panel.ci.test"
admin="ci-admin"
admin_pw='ci installer admin password 1'
ca=/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt
changed_re='^\[changed\]'
export MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1

cleanup() {
  docker rm -f ci-foreign >/dev/null 2>&1 || true
  nft delete table inet ci_foreign 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  echo "--- install log (last 80 lines)" >&2
  tail -n 80 /var/log/mc-panel-install.log >&2 2>/dev/null || true
  echo "--- units" >&2
  systemctl --no-pager --lines=30 status 'mcpanel-*' caddy >&2 2>/dev/null || true
  exit 1
}

# stage <dir> <version> [broken]: binaries, deploy/ and the assembled install.sh in the format
# of --from-dir. "broken" replaces panel with a script that reports the version but whose
# serve exits at once, so the update health check fails.
stage() {
  local dir="$1" version="$2"
  mkdir -p "$dir"
  (cd "$repo" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$dir/" ./cmd/agent ./cmd/files ./cmd/panel)
  cp -r "$repo/deploy" "$dir/deploy"
  bash "$repo/scripts/install/assemble.sh" "$version" "$dir/install.sh"
  chmod 0755 "$dir/install.sh"
  if [ "${3:-}" = broken ]; then
    mv "$dir/panel" "$dir/panel.real"
    cat >"$dir/panel" <<EOF
#!/bin/sh
case "\$1" in
  version) echo "$version" ;;
  serve) echo "ci-installer: broken panel" >&2; exit 1 ;;
  *) exec "$dir/panel.real" "\$@" ;;
esac
EOF
    chmod 0755 "$dir/panel"
  fi
}

# inst <dir> <args…>: runs install.sh of a staging directory; output also in $work/last.out.
inst() {
  local dir="$1"
  shift
  "$dir/install.sh" "$@" 2>&1 | tee "$work/last.out"
  return "${PIPESTATUS[0]}"
}

# https <path> [curl args…]: request through Caddy with its internal CA.
https() {
  local p="$1"
  shift
  curl -sS --max-time 20 --cacert "$ca" --resolve "$domain:443:127.0.0.1" "$@" "https://$domain$p"
}

login_ok() {
  local out="$work/login.json" code
  code="$(https /api/auth/login -o "$out" -w '%{http_code}' -X POST \
    -H "Origin: https://$domain" -H 'Sec-Fetch-Site: same-origin' -H 'Content-Type: application/json' \
    --data-binary "$(python3 -I -c 'import json, sys; print(json.dumps({"username": sys.argv[1], "password": sys.argv[2]}))' "$admin" "$admin_pw")")"
  [ "$code" = 200 ] || fail "login through Caddy: HTTP $code $(cat "$out")"
  python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); sys.exit(0 if d.get("stage") == "setup" and d.get("csrf_token") else 1)' "$out" ||
    fail "login answer: $(cat "$out")"
}

# doctor_fails: number of failed doctor checks (--json, assumption 2).
doctor_fails() {
  "$v1/install.sh" doctor --json >"$work/doctor.json" 2>"$work/doctor.err" || true
  python3 -I -c 'import json, sys; d = json.load(open(sys.argv[1])); print(sum(c["status"] == "fail" for c in d["checks"]))' "$work/doctor.json" ||
    fail "doctor --json: $(cat "$work/doctor.err") $(cat "$work/doctor.json")"
}

db_fingerprint() { # users and schema version, stable across restarts
  sqlite3 /var/lib/mc-panel/panel.db 'SELECT group_concat(username) FROM (SELECT username FROM users ORDER BY username); SELECT max(version_id) FROM goose_db_version;'
}

active() { systemctl is-active --quiet "$1"; }

echo "== prepare: hosts entry, restic, staging"
grep -q " $domain\$" /etc/hosts || echo "127.0.0.1 $domain" >>/etc/hosts
bash "$repo/scripts/install-restic.sh"
v1="$work/v1" v2="$work/v2" broken="$work/broken"
stage "$v1" 0.0.1-ci
stage "$v2" 0.0.2-ci
stage "$broken" 0.0.3-ci broken
echo "OK: staged 0.0.1-ci, 0.0.2-ci and the broken 0.0.3-ci"

echo "== install (tls internal, firewall on)"
printf '%s\n' "$admin_pw" | inst "$v1" install --from-dir "$v1" --tls internal --domain "$domain" --ssh-port 22 --admin-user "$admin" --yes ||
  fail "install: exit $?"
for u in mcpanel-agent.service mcpanel-files.socket mcpanel-files.service mcpanel-panel.socket mcpanel-panel.service mcpanel-fw.service caddy.service; do
  active "$u" || fail "$u not active after install"
done
[ "$(stat -c '%U:%G %a' /run/mc-panel-web/panel.sock)" = "mcpanel:caddy 660" ] ||
  fail "panel socket: $(stat -c '%U:%G %a' /run/mc-panel-web/panel.sock)"
[ "$(stat -c '%U:%G %a' /run/mc-panel-files/files.sock)" = "mcpanel-mc:mcpanel 660" ] ||
  fail "files socket: $(stat -c '%U:%G %a' /run/mc-panel-files/files.sock)"
[ "$(/usr/local/bin/panel version)" = 0.0.1-ci ] || fail "installed panel version: $(/usr/local/bin/panel version)"
[ "$(runuser -u mcpanel -- /usr/local/bin/panel admin list -config /etc/mc-panel/panel.toml)" = "$admin" ] || fail "admin list"
grep -qx 'import mc-panel.caddy # managed by mc-panel' /etc/caddy/Caddyfile || fail "Caddyfile without the managed import line"
nft list table inet mcpanel_fw >"$work/fw.nft" || fail "table inet mcpanel_fw missing"
grep -q 'policy drop' "$work/fw.nft" || fail "mcpanel_fw input policy is not drop"
for p in 80 443 25565; do grep -qE "tcp dport .*\\b$p\\b" "$work/fw.nft" || fail "mcpanel_fw does not allow tcp $p"; done
curl -sS --max-time 20 -o /dev/null https://github.com/ || fail "outbound HTTPS broken by the firewall"
echo "OK: installed, units active, sockets and firewall as specified"

echo "== doctor"
n="$(doctor_fails)"
[ "$n" = 0 ] || fail "doctor reports $n failures: $(cat "$work/doctor.json")"
echo "OK: doctor without failures"

echo "== second install changes nothing"
inst "$v1" install --from-dir "$v1" --tls internal --domain "$domain" --ssh-port 22 --yes </dev/null || fail "second install: exit $?"
if grep -E "$changed_re" "$work/last.out"; then
  fail "second install reported changes (see above)"
fi
echo "OK: idempotent"

echo "== sign-in through Caddy"
for _ in $(seq 1 30); do https /api/health -o /dev/null -f 2>/dev/null && break; sleep 1; done
https /api/health -o /dev/null -f || fail "health through Caddy"
login_ok
echo "OK: panel reachable through Caddy, admin signs in"

echo "== update to 0.0.2-ci"
before="$(db_fingerprint)"
inst "$v2" update --from-dir "$v2" --yes </dev/null || fail "update: exit $?"
[ "$(/usr/local/bin/panel version)" = 0.0.2-ci ] || fail "panel version after update: $(/usr/local/bin/panel version)"
for b in agent files; do
  [ "$("/usr/local/bin/$b" -version)" = 0.0.2-ci ] || fail "$b version after update"
done
[ "$(db_fingerprint)" = "$before" ] || fail "database changed by the update"
ls -d /var/lib/mc-panel-rollback/0.0.1-ci-* >/dev/null 2>&1 || fail "no rollback backup of 0.0.1-ci"
[ "$(stat -c '%U:%G %a' /var/lib/mc-panel-rollback)" = "root:root 700" ] || fail "rollback dir: $(stat -c '%U:%G %a' /var/lib/mc-panel-rollback)"
active mcpanel-panel.service || fail "panel not active after update"
login_ok
echo "OK: updated, data kept, backup written"

echo "== update to a broken build rolls back"
before="$(db_fingerprint)"
set +e
inst "$broken" update --from-dir "$broken" --yes </dev/null
rc=$?
set -e
[ "$rc" = 3 ] || fail "broken update: exit $rc, want 3 (rolled back)"
[ "$(/usr/local/bin/panel version)" = 0.0.2-ci ] || fail "panel version after rollback: $(/usr/local/bin/panel version)"
active mcpanel-panel.service || fail "panel not active after rollback"
[ "$(db_fingerprint)" = "$before" ] || fail "database differs after rollback"
login_ok
echo "OK: failed health check rolled back to 0.0.2-ci"

echo "== uninstall keeps data"
docker run -d --name ci-foreign --label ci.foreign=1 busybox:1.37 sleep 600 >/dev/null
docker run -d --name ci-fake-instance --label mcpanel.instance=999 busybox:1.37 sleep 600 >/dev/null
nft add table inet ci_foreign
inst "$v2" uninstall --yes </dev/null || fail "uninstall: exit $?"
for u in mcpanel-agent.service mcpanel-files.socket mcpanel-panel.service mcpanel-fw.service; do
  [ ! -e "/etc/systemd/system/$u" ] || fail "$u still installed"
  ! active "$u" || fail "$u still active"
done
for b in panel agent files; do [ ! -e "/usr/local/bin/$b" ] || fail "/usr/local/bin/$b still there"; done
[ ! -e /etc/caddy/mc-panel.caddy ] || fail "mc-panel.caddy still there"
! grep -q '^import mc-panel.caddy' /etc/caddy/Caddyfile || fail "import line still in the Caddyfile"
! nft list table inet mcpanel_fw >/dev/null 2>&1 || fail "table inet mcpanel_fw still there"
! nft list table inet mcpanel >/dev/null 2>&1 || fail "table inet mcpanel still there"
nft list table inet ci_foreign >/dev/null || fail "foreign nftables table removed"
[ -z "$(docker ps -aq --filter label=mcpanel.instance)" ] || fail "mc-panel containers left"
[ -n "$(docker ps -q --filter name=ci-foreign)" ] || fail "foreign container removed"
for f in /var/lib/mc-panel/panel.db /etc/mc-panel/panel.toml /etc/mc-panel/agent.toml /etc/mc-panel-files/files.toml; do
  [ -e "$f" ] || fail "$f removed by uninstall"
done
id mcpanel >/dev/null && id mcpanel-mc >/dev/null || fail "users removed by uninstall"
echo "OK: uninstall removed the program only"

echo "== install again over kept data"
inst "$v2" install --from-dir "$v2" --tls internal --domain "$domain" --ssh-port 22 --yes </dev/null || fail "reinstall: exit $?"
for _ in $(seq 1 30); do https /api/health -o /dev/null -f 2>/dev/null && break; sleep 1; done
login_ok
echo "OK: reinstall found DB and config, old password valid"

echo "== uninstall --purge needs the domain"
set +e
printf 'wrong.example\n' | script -qec "$v2/install.sh uninstall --purge --yes" /dev/null >"$work/purge1.out" 2>&1
rc=$?
set -e
[ "$rc" != 0 ] || fail "purge with a wrong confirmation succeeded"
[ -e /var/lib/mc-panel/panel.db ] || fail "data removed despite a wrong confirmation"
printf '%s\n' "$domain" | script -qec "$v2/install.sh uninstall --purge" /dev/null >"$work/purge2.out" 2>&1 ||
  fail "purge: $(cat "$work/purge2.out")"
for p in /var/lib/mc-panel /srv/mc-panel /etc/mc-panel /etc/mc-panel-files; do
  [ ! -e "$p" ] || fail "$p still there after --purge"
done
! id mcpanel >/dev/null 2>&1 || fail "user mcpanel still there after --purge"
! id mcpanel-mc >/dev/null 2>&1 || fail "user mcpanel-mc still there after --purge"
echo "OK: purge only with the typed domain, then everything gone"

echo "PASS: installer lifecycle (install, doctor, idempotence, login, update, rollback, uninstall, reinstall, purge)"
```

`chmod +x scripts/ci-installer.sh`.

- [ ] **Step 3: `.github/workflows/ci.yml` ergänzen**

Phase A hat im Job `changes` schon die Ausgabe `installer` (und den Job `installer-lint`) angelegt – **keine zweite Ausgabe**. Im Schritt „Classify changed files“ den regulären Ausdruck der vorhandenen Zeile `out installer …` so erweitern, dass er mindestens Folgendes abdeckt:

```bash
          out installer "^scripts/install/|^deploy/|^scripts/(ci-installer|install-restic|testvm-deploy)\.sh$|^cmd/|^internal/(config|unixsock)/|^\.goreleaser\.yaml$|^Makefile$|${gomod}"
```

Der Job `ci-ok` wartet auf beide Jobs, `installer-lint` (Phase A) und `installer` (hier).

Neuer Job nach `instances`:

```yaml
  installer:
    name: Installer lifecycle (root)
    needs: changes
    if: needs.changes.outputs.installer == 'true'
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: true

      # Install, doctor, idempotence, sign-in through Caddy (tls internal), update, update with a
      # broken panel (automatic rollback), uninstall (data kept, foreign objects untouched),
      # reinstall over the data, uninstall --purge with the typed domain. The panel is built
      # without the web build: the checks use /api only.
      - name: Installer lifecycle
        run: sudo env "PATH=$PATH" scripts/ci-installer.sh .
```

`ci-ok`: `- installer` in `needs` ergänzen (nach `instances`).

- [ ] **Step 4: Lokal prüfen**

```bash
bash -n scripts/ci-installer.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
make check
```

Erwartet: keine Fehler. Danach `make test-vm SUITE=lint` und `ssh claude@192.168.1.117 "cd ~/mc-panel-test/$(basename "$PWD") && shellcheck scripts/ci-installer.sh scripts/testvm-deploy.sh"` (Sandbox aus): keine Befunde.

- [ ] **Step 5: `scripts/testvm-deploy.sh` auf den Installer umstellen**

Kopfkommentar ersetzen (zweiter Absatz):

```bash
# Deploys mc-panel from a git ref onto the internal test VM (Ubuntu 26.04, no inbound internet):
# installs the build toolchain on first use, clones/fetches the repo with the VM's GitHub
# deploy key, builds panel, agent and files and hands them to the installer
# (scripts/install, sub-project 6): `install` on the first run, `update` afterwards. The
# installer owns users, permissions, config, systemd units and the Caddy site; this script only
# adds the VM's Caddyfile frame (tls internal, the bare IP redirects to the panel name).
# Names resolve through sslip.io: panel.<ip>.sslip.io for the UI, <server>.mc.<ip>.sslip.io
# for Minecraft (dots as dashes).
```

`main()` nach `build`:

```bash
  local version stage
  version="0.0.0-testvm+$(git -C "$SRC" rev-parse --short HEAD)"
  stage="$(stage_installer "$version")"
  migrate_once
  write_caddy_frame "$panel_host" "$ip"
  run_installer "$stage" "$panel_host"
  finish "$panel_host"
```

(die Aufrufe `install_system` und `restart_and_check` entfallen; die Ausgabe am Ende bleibt). `install_packages` installiert nur noch, was der Build braucht: `pkgs=(ca-certificates curl git jq make xz-utils)` (Docker, Caddy, nftables usw. installiert der Installer); die Zeile `sudo systemctl enable --now docker` entfällt. `build` übernimmt die Version als Argument statt sie selbst zu berechnen (`build "$version"`, im Rumpf `-X main.version=$1`).

Neue Funktionen (ersetzen `install_system`, `check_files_service`, `restart_and_check`):

```bash
# stage_installer <version>: binaries, deploy/ and the assembled install.sh in the format of
# install.sh --from-dir; prints the directory.
stage_installer() {
  local version="$1" dir="$SRC/build/testvm"
  rm -rf "$dir"
  mkdir -p "$dir"
  cp "$SRC/bin/panel" "$SRC/bin/agent" "$SRC/bin/files" "$dir/"
  cp -r "$SRC/deploy" "$dir/deploy"
  bash "$SRC/scripts/install/assemble.sh" "$version" "$dir/install.sh"
  chmod 0755 "$dir/install.sh"
  printf '%s\n' "$dir"
}

# migrate_once: the VM was deployed by heredocs before sub-project 6 (panel on TCP
# 127.0.0.1:8080, no panel socket unit). The configs held nothing hand-made, so they are moved
# aside and the installer writes them anew; the data (DB, secret key, worlds, backups) stays.
migrate_once() {
  [ -e /etc/systemd/system/mcpanel-panel.socket ] && return 0
  [ -e /etc/mc-panel/panel.toml ] || return 0
  log "migrate the pre-installer deployment"
  local f
  for f in /etc/mc-panel/panel.toml /etc/mc-panel/agent.toml /etc/mc-panel-files/files.toml; do
    [ -e "$f" ] && sudo mv "$f" "$f.pre-installer"
  done
  sudo systemctl stop mcpanel-panel mcpanel-files.service mcpanel-files.socket mcpanel-agent 2>/dev/null || true
  # Note: the VM has no /etc/docker/daemon.json yet; the installer writes it but does not restart
  # Docker while mc-panel containers run (it warns), so the Minecraft servers keep running.
}

# write_caddy_frame <panel_host> <ip>: the VM's Caddyfile around the installer's site: no trust
# store changes, the bare IP redirects to the panel name, the installer's import line already
# present in its exact managed form (the installer leaves it as it is; uninstall removes it).
write_caddy_frame() {
  local panel_host="$1" ip="$2"
  sudo tee /etc/caddy/Caddyfile.new >/dev/null <<EOF
{
	skip_install_trust
}

import mc-panel.caddy # managed by mc-panel

$ip {
	tls internal
	redir https://$panel_host{uri}
}
EOF
  sudo install -d -m 0755 /etc/caddy
  if ! sudo cmp -s /etc/caddy/Caddyfile.new /etc/caddy/Caddyfile 2>/dev/null; then
    sudo mv /etc/caddy/Caddyfile.new /etc/caddy/Caddyfile
  else
    sudo rm -f /etc/caddy/Caddyfile.new
  fi
}

# run_installer <stage> <panel_host>: install on the first run, update afterwards (same build
# again → --force). The VM keeps its own firewall (internal network).
run_installer() {
  local stage="$1" panel_host="$2"
  if [ -e /etc/systemd/system/mcpanel-panel.socket ]; then
    log "install.sh update"
    sudo "$stage/install.sh" update --from-dir "$stage" --force --yes
  else
    log "install.sh install"
    sudo "$stage/install.sh" install --from-dir "$stage" --tls internal --domain "$panel_host" --no-firewall --yes
  fi
}

# finish <panel_host>: health through Caddy, copy of the Caddy CA for the browser, doctor.
finish() {
  local panel_host="$1" ca=/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt _
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null --cacert <(sudo cat "$ca" 2>/dev/null) \
      --resolve "$panel_host:443:127.0.0.1" "https://$panel_host/api/health" 2>/dev/null; then
      sudo install -m 0644 -o "$(id -un)" "$ca" "$HOME/caddy-root.crt"
      sudo "$SRC/build/testvm/install.sh" doctor || true # warnings (no own filesystem, SSH) are expected
      return 0
    fi
    sleep 1
  done
  echo "testvm-deploy: https://$panel_host/api/health not healthy after 30 s" >&2
  sudo systemctl --no-pager --lines=20 status 'mcpanel-*' caddy >&2 || true
  exit 1
}
```

Hinweise für die Umsetzung:
- Eine erste Installation ohne vorhandenen Admin fragt über `/dev/tty`; über `ssh … bash -s` gibt es kein TTY, `adm_ensure` überspringt dann mit Hinweis (Annahme 5). Auf der VM existiert der Admin `admin` bereits in der übernommenen DB.
- Zeilen aus TP4/TP5 in `install_system` (restic, `backups/`, `restic_binary`, Monitoring-Schlüssel, Unit-Werte) entfallen mit der Funktion; der Installer schreibt sie aus `deploy/`. `scripts/install-restic.sh` wird auf der VM nicht mehr gebraucht, wenn das Ubuntu-26.04-Paket `restic` ≥ 0.17 ist (in Schritt 6 mit `restic version` prüfen; sonst bleibt der Aufruf vor `run_installer` stehen).

- [ ] **Step 6: Commit, Push, Draft-PR, Lead um Test-Deploy bitten**

```bash
git add scripts/ci-installer.sh scripts/testvm-deploy.sh .github/workflows/ci.yml
git commit -m "ci(installer): lifecycle job on a runner and the test VM deployed through install.sh

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/installer-ci-docs
gh pr create --draft --title "ci,docs: installer lifecycle in CI, test VM through install.sh, documentation" --body-file <(…)
gh pr checks <n> --watch
```

Erwartet: `Installer lifecycle (root)` grün, `CI OK` grün. Fehler aus `gh run view <id> --log-failed` beheben (Abweichungen von den Annahmen zuerst prüfen). Danach den Lead per SendMessage bitten, `make deploy-testvm REF=feat/installer-ci-docs` auszuführen (Migration der VM, Panel danach unter `https://panel.192-168-1-117.sslip.io/` mit dem bestehenden Admin, Welten und Backups unverändert). Ein zweiter Deploy desselben Refs muss als `update --force` durchlaufen.

---

### Task 12: Doku, Checkliste, Abschluss

**Files:**
- Create: `docs/dev/acceptance-tp6.md`
- Modify: `README.md` (neuer Abschnitt „Installation“, Satz zur Einrichtung von Hand entfernen)
- Modify: `docs/dev/README.md` (Abschnitte „Test-VM“, „Produktion: Listen-Adresse und Reverse Proxy“, neuer Abschnitt „Installer“, „Make-Targets“, „CI“)
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: Optionen, Exit-Codes, Pfade und Units aus Spec §3–§7 und der Übersicht; die tatsächlichen Texte der Ausgabe aus `scripts/install/*.sh` auf `main`.
- Produces: Doku, die der Lead im Abschluss nur noch um Status und Roadmap ergänzt.

- [ ] **Step 1: `docs/dev/acceptance-tp6.md` schreiben**

```markdown
# Abnahme Teilprojekt 6 (manuell, echter VPS und Test-VM)

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` (§10).

Automatisch geprüft (CI, Job „Installer lifecycle“): Installation mit `tls internal` und Firewall auf einem Runner, `doctor` ohne Fehler, zweiter Lauf ohne Änderungen, Login über Caddy, Update mit Sicherung, Update mit defektem panel und automatischem Rollback (gleiche DB), `uninstall` (Dienste, Units, Caddy-Site, nftables-Tabellen und mc-panel-Container weg, Daten und Konfiguration bleiben, fremder Container und fremde Tabelle unberührt), Neuinstallation über die Daten, `uninstall --purge` nur mit eingetippter Domain. Bash-Tests (`scripts/install/test.sh`): Argumente, Versionsvergleich, `sshd -T`, Vorlagen, TOML-Ergänzung, Prüfsummen. Jeder `make deploy-testvm` läuft über den Installer.

Manuell: echter VPS mit öffentlicher Domain (Let's Encrypt), SSH-Sitzung, Firewall, Update auf ein echtes Release.

## Vorbereitung

1. Frischer VPS mit Ubuntu 26.04 LTS (z. B. kleinster Hetzner-Typ), SSH-Schlüssel, Zugang als root oder Benutzer mit `sudo`.
2. DNS: `panel.<deine-domain>` (A und, falls der VPS IPv6 hat, AAAA) auf den VPS; Wildcard `*.mc.<deine-domain>` auf den VPS. Prüfen: `dig +short panel.<deine-domain>` zeigt die VPS-Adresse.
3. Ein veröffentlichtes Release mit `install.sh` (Tag `v*`, nach dem Merge von TP6 vom Nutzer gesetzt) und ein zweites, neueres für das Update (z. B. ein Patch-Release).
4. Zweites Terminal mit offener SSH-Sitzung als Rückfallebene während der Firewall-Schritte.

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 1a | Einzeiler | `curl -fsSL https://github.com/Bernotese/mc-panel/releases/download/<vorletzte-version>/install.sh \| sudo bash -s -- install --domain panel.<deine-domain> --email <deine-mail>`; Admin-Name und Passwort bei der Frage eingeben | Lauf endet nach wenigen Minuten mit URL, Admin-Name und nächsten Schritten; `https://panel.<deine-domain>/` zeigt die Anmeldung mit gültigem Let's-Encrypt-Zertifikat (Browser ohne Warnung) |
| 1b | Anmelden | mit dem Admin anmelden, 2FA einrichten | Anmeldung und 2FA wie gewohnt |
| 1c | Idempotenz | denselben Einzeiler noch einmal | jeder Schritt meldet `[ok]` oder `[skip]`, kein `[changed]`; Panel läuft durch |
| 2a | `doctor` frisch | `sudo bash install.sh doctor` (das Skript aus dem Release) | höchstens Warnungen: eigenes Dateisystem für `/srv/mc-panel` fehlt; ggf. SSH mit Passwort-Login; Exit-Code 1 bei Warnungen, 0 ohne |
| 2b | `doctor` findet Fehler | nacheinander: `systemctl stop mcpanel-files.socket mcpanel-files.service` (die Socket-Unit würde den Dienst sonst sofort wieder starten); `chmod 0666 /run/mc-panel-web/panel.sock`; in `/etc/mc-panel/panel.toml` die Zeile `instances_dir` löschen – jeweils `doctor` und danach rückgängig machen (`install` repariert Socket und Unit, der Schlüssel wird ergänzt) | jeweils ✗ mit passender Abhilfe, Exit-Code 2; nach der Reparatur wieder ohne ✗ |
| 3a | Update | Basisdomain eintragen, einen Paper-Server anlegen und starten, einmal joinen, ein Backup erstellen; dann `sudo bash install.sh update --check` und `sudo bash install.sh update` | `--check` nennt die neuere Version; das Update meldet Sicherung, Austausch und erfolgreichen Health-Check; der Minecraft-Server lief durchgehend (verbunden bleiben); Benutzer, Server, Welt und Backup unverändert; `/var/lib/mc-panel-rollback/` (`root:root 0700`) enthält die alte Version |
| 3b | Rollback | auf der Test-VM: `make deploy-testvm` eines Branches, dessen panel absichtlich nicht startet (nur zum Test, nicht pushen auf `main`), oder die CI-Prüfung als Beleg nehmen | Exit-Code 3, alte Version läuft wieder, DB unverändert |
| 4a | Firewall | in der zweiten SSH-Sitzung `sudo nft list table inet mcpanel_fw`; von außen `nmap -Pn -p 22,80,443,25565,8080,3306 <vps>` | nur SSH-Port, 80, 443, 25565 offen; die SSH-Sitzung brach beim Installieren nicht ab |
| 4b | SSH auf anderem Port | auf einem zweiten VPS (oder vor der Installation) `Port 2222` in `sshd_config`, `systemctl reload ssh`, dann installieren | Firewall erlaubt 2222 (aus `sshd -T`), die laufende Sitzung bleibt |
| 5a | Uninstall | `sudo bash install.sh uninstall` | Dienste, Units, Caddy-Site, Firewall-Tabellen und Container weg; `/srv/mc-panel`, `/var/lib/mc-panel`, `/etc/mc-panel*` bleiben; andere Caddy-Sites (falls vorhanden) laufen weiter |
| 5b | Neuinstallation | Einzeiler erneut | keine Admin-Frage, Anmeldung mit dem alten Passwort, Server und Welt wieder da, Server startet |
| 5c | Purge | `sudo bash install.sh uninstall --purge`, zuerst eine falsche Domain eintippen, dann die richtige | falsche Eingabe bricht ab, nichts gelöscht; danach alles weg inklusive Benutzer `mcpanel`/`mcpanel-mc`; Pakete bleiben installiert |
| 6 | Test-VM | `make deploy-testvm` zweimal hintereinander | erster Lauf migriert bzw. installiert, zweiter läuft als `update --force`; Panel unter `https://panel.192-168-1-117.sslip.io/` wie vorher |

## Ergebnis

| # | Ergebnis | Notiz |
|---|---|---|
| 1a–6 | | |
```

(Die Texte `[ok]`/`[skip]`/`[changed]` und die Fehlermeldungen gegen die tatsächliche Ausgabe auf `main` abgleichen.)

- [ ] **Step 2: `README.md`**

Nach „Aufbau“ einen Abschnitt einfügen:

~~~markdown
## Installation (Produktion)

Voraussetzungen: ein VPS mit Ubuntu 26.04 LTS, root bzw. `sudo`, eine Domain für das Panel (A/AAAA-Eintrag auf den VPS, Port 80 und 443 von außen erreichbar für Let's Encrypt) und ein Wildcard-Eintrag für die Minecraft-Server (z. B. `*.mc.example.com`).

```bash
curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
  | sudo bash -s -- install --domain panel.example.com --email you@example.com
```

Der Installer richtet Docker, Caddy (mit Let's-Encrypt-Zertifikat), eine Host-Firewall (SSH, 80, 443, 25565), Sicherheitsupdates, die drei Dienste und den ersten Admin ein und prüft am Ende alles mit `doctor`. Danach im Panel die Basisdomain der Minecraft-Server eintragen, 2FA und Benachrichtigungen einrichten und `/srv/mc-panel/backups/keys/` in die eigene Sicherung des Hosts aufnehmen.

| Befehl | Wirkung |
|---|---|
| `sudo bash install.sh doctor` | prüft Dienste, Rechte, Zertifikat, Firewall, Speicher und warnt bei unsicherer SSH-Konfiguration |
| `sudo bash install.sh update` | neueste Version mit Sicherung der DB und automatischem Rollback (`--check` zeigt nur an, `--version vX.Y.Z` wählt eine Version) |
| `sudo bash install.sh uninstall` | entfernt das Programm, behält Daten und Konfiguration (`--purge` löscht nach Bestätigung alles) |

`install.sh` liegt jedem Release bei (`…/releases/download/<version>/install.sh`). Der Installer ändert SSH nicht; ein eigenes Dateisystem für `/srv/mc-panel` (damit volle Welten die Panel-Datenbank nicht blockieren) richtet er nicht ein, `doctor` weist darauf hin.
~~~

Den Satz „Bis der Installer existiert, beschreibt `docs/dev/acceptance-tp2.md` die Einrichtung eines Servers von Hand.“ streichen. Im Abschnitt „Release“ ergänzen: „… und `checksums.txt` sowie `install.sh`; die Archive enthalten zusätzlich `deploy/` (Vorlagen für Units, Caddy, nftables, Konfiguration).“ Die Tabelle „Stand“ ändert der Lead im Abschluss.

- [ ] **Step 3: `docs/dev/README.md`**

1. Abschnitt „Test-VM“, Absatz zu `scripts/testvm-deploy.sh` ersetzen:

```markdown
- `scripts/testvm-deploy.sh` läuft per `ssh … bash -s` auf der VM. Beim ersten Lauf installiert es die Build-Werkzeuge, Go (Version aus `go.mod`, nach `/usr/local/go`) und Node.js 24 (nach `/usr/local/lib/nodejs`). Danach holt es das Repo mit dem Deploy-Key der VM nach `~/mc-panel`, baut panel, agent und files (Version `0.0.0-testvm+<commit>`), legt sie mit `deploy/` und dem zusammengefügten `install.sh` in `~/mc-panel/build/testvm/` ab und ruft den Installer auf: `install --from-dir … --tls internal --no-firewall` beim ersten Mal, sonst `update --from-dir … --force`. Benutzer, Rechte, Konfiguration, Units und die Caddy-Site schreibt der Installer (Abschnitt „Installer“); das Skript ergänzt nur den Caddyfile-Rahmen der VM (`skip_install_trust`, Weiterleitung der IP auf den Panel-Namen) und kopiert die Caddy-CA nach `~/caddy-root.crt`. Daten bleiben erhalten (`/var/lib/mc-panel`, `/srv/mc-panel`). Zum Schluss läuft `install.sh doctor` (Warnungen zu Dateisystem und SSH sind auf der VM normal).
- Die erste Ausführung nach Teilprojekt 6 migriert die alte Heredoc-Installation einmalig: die drei Konfigurationsdateien werden nach `*.pre-installer` verschoben und vom Installer neu geschrieben, das panel lauscht danach auf dem Socket `/run/mc-panel-web/panel.sock` statt auf `127.0.0.1:8080`.
```

Den Punkt „Das panel lauscht auf der VM auf `127.0.0.1:8080` …“ und den Satz „`scripts/testvm-deploy.sh` bleibt unverändert, weil …“ (Plugins) streichen bzw. auf den Installer umformulieren („Plugins/Mods brauchen keine eigene Konfiguration: die panel-Unit des Installers schränkt ausgehende Verbindungen nicht ein …“).

2. Abschnitt „Produktion: Listen-Adresse und Reverse Proxy“ → „Produktion“; erster Absatz neu: „Produktiv richtet der Installer (`install.sh`, Abschnitt „Installer“) alles ein; die folgenden Punkte beschreiben, was er umsetzt und was bei Abweichungen gilt. Maßgeblich für Units, Caddy, Firewall und Konfiguration sind die Vorlagen in `deploy/`.“ In den Punkten zum Dateidienst „`scripts/testvm-deploy.sh` ist die Referenz“ durch „`deploy/systemd/` ist die Referenz“ ersetzen; den Punkt zum panel-Socket auf den Stand bringen: Socket-Aktivierung über `mcpanel-panel.socket` (`/run/mc-panel-web/panel.sock`, `mcpanel:caddy 0660`, `DirectoryMode=0755`), das panel übernimmt ihn per `LISTEN_FDS`; Caddy-Beispiel mit `reverse_proxy unix//run/mc-panel-web/panel.sock`; Health-Check-Beispiel mit dem neuen Pfad. Die Sätze „richtet später der Installer (Teilprojekt 6) ein“ durch „richtet der Installer ein“ ersetzen.

3. Neuer Abschnitt „Installer“ (nach „Produktion“):

```markdown
## Installer

- Quellen in `scripts/install/`: `lib.sh` (Helfer, Pfade, Wrapper für Systembefehle), je Schritt eine Datei (`preflight.sh`, `packages.sh`, `layout.sh`, `binaries.sh`, `config.sh`, `units.sh`, `caddy.sh`, `firewall.sh`, `upgrades.sh`, `admin.sh`, `update.sh`, `doctor.sh`, `uninstall.sh`) und `main.sh` (Argumente, Unterbefehle, Logging). Vorlagen in `deploy/` (`systemd/`, `caddy/`, `nftables/`, `config/`).
- `make installer` fügt alles zu `build/install.sh` zusammen (`scripts/install/assemble.sh <version> <out>`; Vorlagen als Heredocs, Version eingetragen). goreleaser hängt `install.sh` an jedes Release und nimmt es in `checksums.txt` auf; die Archive enthalten `deploy/`.
- Auf dem Mac läuft der Installer nie; `make check` prüft nur die Syntax (`bash -n`).
- Tests: `scripts/install/test.sh` (reine Funktionen, Vorlagen, Prüfsummen; mit `ROOT=<tmp>` und `MCPANEL_INSTALL_DRYRUN=1` schreiben die Schritte in ein temporäres Verzeichnis und protokollieren Systembefehle nur) läuft im Lint der CI und mit `make test-vm SUITE=lint`. Den ganzen Lebenszyklus prüft der CI-Job `installer` (`scripts/ci-installer.sh`, nur Runner, ändert das System). `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1` erlaubt dort Ubuntu 24.04; für Betreiber ist das kein unterstützter Weg.
- Exit-Codes: 0 ok, 1 Fehler, 2 `doctor` mit Fehlern (1 bei Warnungen), 3 Update zurückgerollt, 4 Rollback gescheitert. Protokoll: `/var/log/mc-panel-install.log`.
```

4. „Make-Targets“: Zeile `make installer` („fügt `scripts/install/` und `deploy/` zu `build/install.sh` zusammen“); `make deploy-testvm` beschreibt „… baut auf der VM und deployt über den Installer“.

5. „CI“-Tabelle: Zeile `installer` („`scripts/install/`, `deploy/`, `scripts/ci-installer.sh`, `scripts/install-restic.sh`, `cmd/`, `internal/config/`, `internal/unixsock/`, `.goreleaser.yaml`, `Makefile`, `go.mod`/`go.sum`“ | „nur CI (ändert das System); einzelne Funktionen mit `scripts/install/test.sh`“).

- [ ] **Step 4: `CLAUDE.md`**

- Einleitung: „production is an Ubuntu 26.04 LTS VPS set up by the installer (`install.sh`, sub-project 6: panel, agent and the files service as systemd services behind Caddy)“.
- Spec-Liste: Zeile für `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` („installer `install.sh` with install/update/doctor/uninstall (sub-project 6); manual acceptance checklist in `docs/dev/acceptance-tp6.md`“).
- Working rules, „Nothing system-level on the Mac“: „Scripts that change the host (`scripts/ci-acceptance.sh`, `scripts/ci-installer.sh`, the installer itself) are for CI runners and the test VM only.“
- Working rules, „Test VM“: „`make deploy-testvm …` builds that ref on the VM from GitHub and deploys it through the installer (`install` first, `update` afterwards)“.
- Commands: `make installer               # assemble scripts/install + deploy/ into build/install.sh (never run it on the Mac)`.
- Architecture: nach der Beschreibung der drei Binaries ein Absatz: „The installer (`scripts/install/*.sh`, assembled into `build/install.sh`, released with every version) is the single source of truth for users, permissions, config files, systemd units (`deploy/systemd/`), the Caddy site and the host firewall (table `inet mcpanel_fw`, separate from the agent's `inet mcpanel`). The panel takes its socket from systemd (`mcpanel-panel.socket`, `/run/mc-panel-web/panel.sock`, group `caddy`), like the files service.“

- [ ] **Step 5: Prüfen und committen**

```bash
make check
git add docs/dev/acceptance-tp6.md docs/dev/README.md README.md CLAUDE.md
git commit -m "docs: installation, installer structure and tests, acceptance checklist for sub-project 6

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <n> --watch
```

Erwartet: `CI OK` grün. Dann dem Lead per SendMessage PR-Nummer, CI-Stand und Abweichungen melden. PR bleibt Draft.

- [ ] **Step 6 (Lead): Gesamt-Review, Deploy, Abnahme, Abschluss**

1. Review aller TP6-PRs gegen Spec und Übersicht (Reviewer-Subagent je PR, Fix-Runden über den jeweiligen Teammate), dann `gh pr ready <n>`.
2. Nach dem Merge: `make deploy-testvm` zweimal (Migration bzw. Installation, dann Update), Checkliste Zeile 6 auf der VM.
3. Nach dem ersten Release mit `install.sh` (Tag setzt der Nutzer): Abnahme auf einem echten VPS mit dem Nutzer nach `docs/dev/acceptance-tp6.md`.
4. Abschluss auf `docs/tp6-done`: Spec-Status „umgesetzt (PRs …)“, Roadmap-Zeile 6 in der Foundation-Spec, „Stand“ in `README.md` (6 fertig), `CLAUDE.md` „(… 6 installer are done)“, Pläne `docs/superpowers/plans/2026-10-13-installer-*.md` löschen.

## Self-Review

- **Spec-Abdeckung:** §8 Test-VM (Task 11 Step 5), CI-Ablauf vollständig (Step 2: install, doctor, zweites install, Login über Caddy, Update, defektes Update mit Rollback, uninstall, reinstall, purge), Lint (shellcheck auf der VM, Step 4; der Lint-Job selbst kommt aus Phase A); §10 Kriterien 1–6 in der Checkliste, Let's Encrypt nur manuell.
- **Review Focus der Übersicht:** 1 Aussperren (Checkliste 4a/4b; in der CI ohne SSH-Sitzung nur Firewall-Inhalt und ausgehendes HTTPS), 2 Idempotenz (CI zweites `install` ohne `[changed]`; fremder Caddyfile-Inhalt auf der VM durch den Rahmen), 3 Update-Fehlschlag (CI defektes panel, Exit 3, DB-Fingerabdruck), 4 Integrität (nur über `--from-dir` in der CI; Prüfsummen deckt `test.sh` aus Phase A/B1 ab), 5 Uninstall (fremder Container, fremde nftables-Tabelle, Bestätigung bei `--purge` falsch/richtig).
- **Platzhalter:** keine; Abweichungen der Annahmen 1–6 werden in Step 1 gegen `main` geprüft.

## Gefundene Probleme (für den Lead)

1. **`panel.toml` kennt `origin` nicht:** Übersicht V3 nennt den Schlüssel `origin`; der Config-Parser und alle bisherigen Konfigurationen (`testvm-deploy.sh`, `ci-instances.sh`) heißen `public_url`. Phase A muss die Vorlage mit `public_url = "https://{{DOMAIN}}"` schreiben (oder V3 korrigieren).
2. **Ausgabeformat und `doctor --json` sind nicht festgelegt:** V2 definiert `log_changed` usw. ohne Format, Spec §6 `--json` ohne Schema. Die CI braucht beides maschinenlesbar (Annahmen 1 und 2). Vorschlag: V2 um die Präfixe `[ok]`/`[changed]`/`[skip]`/`[warn]` und V4 um das JSON-Schema ergänzen.
3. **Docker und restic auf dem Runner (Annahme 4):** GitHub-Runner haben docker-ce vorinstalliert, `apt-get install docker.io` kollidiert damit; Ubuntu 24.04 liefert restic 0.16 (< 0.17). B1 muss „funktionierendes Docker vorhanden → kein `docker.io`“ und „restic aus `command -v` ≥ 0.17 → `restic_binary` = dieser Pfad“ umsetzen, sonst kann die CI nicht grün werden. Auf Ubuntu 26.04 (Produktion) ändert das nichts.
4. **`update --from-dir` und Selbst-Ersetzung (Spec §5 Schritt 2):** Mit `--from-dir` darf `update` nichts aus dem Netz laden und muss `DIR/install.sh` (oder sich selbst) verwenden (Annahme 3); in V4 nicht geregelt.
5. **Build-Job prüft den Tarball-Inhalt:** `.github/workflows/ci.yml` (Job `build`, „Verify snapshot artifacts“) verlangt heute genau `agent files panel` im Archiv. Phase A muss das auf `deploy/` und das zusätzliche Asset `install.sh` erweitern (gehört zu V5, liegt aber im Build-Job).
6. **Kein eigener Lint-Job:** V5 spricht vom „Lint-Job“; in der CI liegt golangci-lint im Job `go`, ESLint im Job `web`. shellcheck und `test.sh` brauchen einen Schritt im Job `go` (oder einen neuen Job) und einen Filter, der auf `scripts/install/` und `deploy/` reagiert – Phase A.
7. **Admin-Prompt ohne TTY:** Über `ssh … bash -s` (Test-VM) und in der CI gibt es kein `/dev/tty`. B1 muss ohne TTY und ohne `--admin-user` überspringen (mit Hinweis), statt zu hängen oder abzubrechen (Annahme 5).
8. **Migration der Test-VM:** Die bestehende VM-Konfiguration (`listen = "127.0.0.1:8080"`) würde der Installer nicht ändern (er ergänzt nur fehlende Schlüssel), Caddy würde dann auf einen Socket ohne Dienst zeigen. Task 11 löst das einmalig in `testvm-deploy.sh` (Konfigurationen beiseitelegen, Installer schreibt neu); gilt nur für die VM.
