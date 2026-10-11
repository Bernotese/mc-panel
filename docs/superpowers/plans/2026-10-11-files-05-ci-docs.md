# Teilprojekt 3b – Betrieb, CI-Abnahme, Doku und Abschluss (Aufgaben 18–20) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Den neuen Dienst `mcpanel-files` in Entwicklung (`make dev`), Test-VM, Release und CI einbinden, den Dateibrowser auf einem CI-Runner gegen einen echten Paper-Server abnehmen (Dienst als `mcpanel-mc`, Peer-UID, `os.Root`-Grenzen, Schwärzen, Entpacken), die Doku nachziehen und Teilprojekt 3b abschließen (Gesamt-Review, Abnahme auf der Test-VM, Pläne löschen).

**Architecture:** Aufgabe 18 berührt nur Betriebs- und CI-Dateien: `scripts/dev.sh` startet `files -dev`; `scripts/testvm-deploy.sh` installiert das dritte Binary mit systemd-Socket-Aktivierung (`mcpanel-files.socket`) und gehärteter Service-Unit (Spec §3.1); `.goreleaser.yaml` baut und packt `files`; `.github/workflows/ci.yml` startet den Dienst im E2E-Job und gibt ihn an `scripts/ci-instances.sh` weiter, das ihn ohne Socket-Aktivierung als `mcpanel-mc` startet (der Dienst legt den Socket selbst an, `socket_mode`) und als panel-Benutzer über den Socket anspricht. Aufgabe 19 schreibt die Doku. Aufgabe 20 koordiniert der Lead (Reviews, Fixes über einen eigenen Teammate, Abnahme, Abschluss).

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, immer `python3 -I`), `curl --unix-socket`, `setpriv` (util-linux), systemd, GoReleaser v2, GitHub Actions (`ubuntu-24.04`).

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§3.1, §3.3, §7–§9) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-11-files-00-overview.md` (V1, V2; Abschnitte „Ausführung“ und „Global Constraints“ gelten vollständig)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | C, Teammate „ci-docs“, `feat/files-ci-docs` (Aufgaben 18–19); Aufgabe 20 koordiniert der Lead, Abschluss auf `docs/tp3b-done` |
| Startet, wenn | Phasen A, B1, B2, B3 auf `main` gemerged |
| Dateieigentum | nur `scripts/**`, `Makefile`, `.goreleaser.yaml`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md` |
| Wo getestet wird | lokal nur `make check`, `bash -n`, `shellcheck` (falls installiert) und `actionlint` per `go run`; alles mit root, Docker oder Minecraft nur in der CI bzw. auf der Test-VM |
| Socket des Dienstes | Test-VM/Produktion: systemd-Socket-Aktivierung `mcpanel-files.socket` (`ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660`, `DirectoryMode=0755`); der Dienst übernimmt ihn per `LISTEN_FDS`/`LISTEN_PID`. Eigenes Verzeichnis `/run/mc-panel-files`, nie `/run/mc-panel` des agents. CI (ohne Aktivierung): der Dienst legt den Socket selbst an (`socket`, `socket_mode`) |
| Prozess des Dienstes | `User=mcpanel-mc`, `Group=mcpanel-mc`, **keine** Zusatzgruppen (kein Zugriff auf `/etc/mc-panel`), kein `RuntimeDirectory`, Härtung exakt nach Spec §3.1; Konfiguration in `/etc/mc-panel-files/files.toml` (`root:mcpanel-mc`, Verzeichnis 0750, Datei 0640), weil `/etc/mc-panel` (`root:mcpanel 0750`) für `mcpanel-mc` unlesbar ist |
| CI-Socket | `socket = "$work/frun/files.sock"`, `socket_mode = "0666"` in `$work/frun` (`mcpanel-mc:mcpanel-mc 0755`): ohne Socket-Aktivierung bekommt der Socket die primäre Gruppe des Dienstes (`mcpanel-mc`), der panel-Testbenutzer `mcpanel` ist nicht Mitglied; 0666 ist hier vertretbar, weil die Peer-UID-Prüfung die Zugangskontrolle ist – das Skript beweist das, indem `nobody` trotz 0666 abgewiesen wird (root ist wie beim agent zugelassen) |
| CI-Grenzen (`files.toml` im Skript) | `max_upload_bytes = 10485760` (10 MiB), `max_extract_bytes = 104857600`, `max_extract_entries = 1000`, `reserve_bytes = 1048576`, `max_text_bytes = 5242880`, `max_list_entries = 10000` |
| Test-Server | wie TP2/3a: ID `9001`, Name `citest`, Paper `1.21.11`, java21, 2048 MiB |
| Dienst-Aufrufe im Skript | immer als panel-Benutzer: `setpriv --reuid=mcpanel --regid=mcpanel --init-groups curl … --unix-socket <sock> http://files/…`; Ausgaben nach `$work/fout` (Besitzer `mcpanel`); JSON mit Nutzerinhalt immer per `json.dumps` |

---

### Task 18: Betrieb und CI-Abnahme des Dateidienstes

**Files:**
- Modify: `scripts/dev.sh` (startet `files -dev`)
- Modify: `scripts/testvm-deploy.sh` (Binary, `files.toml`, `files_socket`, Unit, Neustart, Prüfung)
- Modify: `.goreleaser.yaml` (Build `files`, Archiv)
- Modify: `.github/workflows/ci.yml` (Jobs `e2e`, `instances`, `build`)
- Modify: `scripts/ci-instances.sh` (zweites Argument, Dienst starten, Abschnitte „files …“)

**Interfaces:**
- Consumes: Binary `cmd/files` (B1) mit `-dev` und `-config <pfad>`, `var version` in `package main` (GoReleaser-Prüfung im Job `build` sucht den Versions-String), Übernahme eines aktivierten Sockets per `LISTEN_FDS`/`LISTEN_PID`, sonst eigener Socket mit `socket_mode` (Gruppe = primäre Gruppe des Dienstes); `files.toml`-Schlüssel aus V2 (`socket`, `socket_mode`, `panel_uid`, `instances_dir`, `dev`, `[limits]` mit `max_upload_bytes`, `max_extract_bytes`, `max_extract_entries`, `reserve_bytes`, `max_text_bytes`, `max_list_entries`); `panel.toml`-Schlüssel `files_socket` (V2); Dienst-API aus V1 (`/v1/health|list|read|write|upload|download|mkdir|rename|delete|extract|jobs`, Fehler `{error, code}` mit den Codes aus V1, Header `X-Files-Kind`); agent setzt `instances/` auf `root:mcpanel-mc 0750`, `deleted/` bleibt `root:root 0700` (Aufgabe 1).
- Produces: `scripts/ci-instances.sh AGENT_BINARY FILES_BINARY` endet mit `PASS: instance acceptance (agent and files path)`; `make deploy-testvm` installiert und startet `mcpanel-files`; Release-Archive enthalten `agent files panel`.

- [ ] **Step 1: Worktree anlegen**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel worktree add ../mc-panel-wt/feat-files-ci-docs -b feat/files-ci-docs origin/main
cd /Users/jannes/Workspace/mc-panel-wt/feat-files-ci-docs
(cd web && npm ci)
```

- [ ] **Step 2: `scripts/dev.sh`** – den Kopfkommentar und den Start-Block ändern.

Kopfkommentar (Zeile 2) ersetzen durch:

```bash
# Starts agent (if present), the files service (if present), panel and the Vite dev server for
# local development.
```

Den Block

```bash
if [ -x .dev/bin/agent ]; then
  start agent .dev/bin/agent -dev
fi
start panel .dev/bin/panel serve -dev
```

ersetzen durch:

```bash
if [ -x .dev/bin/agent ]; then
  start agent .dev/bin/agent -dev
fi
# The files service runs as the developer in dev mode (instances below .dev/, the same tree the
# dev agent uses); in production it is the unprivileged user mcpanel-mc.
if [ -x .dev/bin/files ]; then
  start files .dev/bin/files -dev
fi
start panel .dev/bin/panel serve -dev
```

- [ ] **Step 3: `scripts/testvm-deploy.sh`** – fünf Stellen.

(a) Kopfkommentar Zeile 4: `builds panel and agent, installs them with systemd units` → `builds panel, agent and files, installs them with systemd units`.

(b) In `main` die Zeile `Logs:         journalctl -u mcpanel-agent -u mcpanel-panel -f` ersetzen durch:

```bash
  Logs:         journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f
```

(c) In `install_system` die Zeile

```bash
  sudo install -m 0755 "$SRC/bin/panel" "$SRC/bin/agent" /usr/local/bin/
```

ersetzen durch:

```bash
  sudo install -m 0755 "$SRC/bin/panel" "$SRC/bin/agent" "$SRC/bin/files" /usr/local/bin/
  # Spec 3b §3.3: the files service (mcpanel-mc) may enter instances/, never write it; the
  # agent enforces the same owner and mode at start.
  sudo install -d -m 0750 -o root -g mcpanel-mc /srv/mc-panel/instances
```

(d) Direkt nach dem `agent.toml`-Block (nach `sudo chmod 0600 /etc/mc-panel/agent.toml`) einfügen:

```bash
  # The files service runs as mcpanel-mc without supplementary groups, so it cannot read
  # /etc/mc-panel (root:mcpanel 0750): its config lives in a directory of its own. socket and
  # socket_mode are unused here (systemd hands over the socket, see mcpanel-files.socket).
  sudo install -d -m 0750 -o root -g mcpanel-mc /etc/mc-panel-files
  sudo tee /etc/mc-panel-files/files.toml >/dev/null <<EOF
socket = "/run/mc-panel-files/files.sock"
panel_uid = $(id -u mcpanel)
instances_dir = "/srv/mc-panel"
EOF
  sudo chown root:mcpanel-mc /etc/mc-panel-files/files.toml
  sudo chmod 0640 /etc/mc-panel-files/files.toml
```

Im `panel.toml`-Block die Zeile `agent_socket = "/run/mc-panel/agent.sock"` ergänzen um eine Folgezeile:

```toml
agent_socket = "/run/mc-panel/agent.sock"
files_socket = "/run/mc-panel-files/files.sock"
```

Nach dem Block der Unit `mcpanel-agent.service` (vor `mcpanel-panel.service`) einfügen:

```bash
  # Spec 3b §3.1: the files service never runs as root and has no supplementary groups. systemd
  # creates the socket (owner mcpanel-mc, group mcpanel, 0660) in a directory of its own and
  # hands it over (LISTEN_FDS); /run/mc-panel stays the agent's, so stopping one unit never
  # removes the other's socket.
  sudo tee /etc/systemd/system/mcpanel-files.socket >/dev/null <<'EOF'
[Unit]
Description=mc-panel files service socket

[Socket]
ListenStream=/run/mc-panel-files/files.sock
SocketUser=mcpanel-mc
SocketGroup=mcpanel
SocketMode=0660
DirectoryMode=0755
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
EOF

  sudo tee /etc/systemd/system/mcpanel-files.service >/dev/null <<'EOF'
[Unit]
Description=mc-panel files service (unprivileged, Minecraft data)
Requires=mcpanel-files.socket
Wants=mcpanel-agent.service
After=network-online.target mcpanel-files.socket mcpanel-agent.service

[Service]
User=mcpanel-mc
Group=mcpanel-mc
ExecStart=/usr/local/bin/files -config /etc/mc-panel-files/files.toml
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ReadWritePaths=/srv/mc-panel/instances
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_UNIX
SystemCallArchitectures=native
UMask=0027

[Install]
WantedBy=multi-user.target
EOF
```

Hinweis: `DirectoryMode=0755` gilt für `/run/mc-panel-files`, das systemd für den Socket anlegt; den Zugriff regeln Modus 0660 und Gruppe `mcpanel` des Sockets sowie die Peer-UID-Prüfung des Dienstes. `RestrictAddressFamilies=AF_UNIX` verträgt sich mit dem übernommenen Unix-Socket.

In der Unit `mcpanel-panel.service` die Zeilen

```ini
Wants=mcpanel-agent.service
After=network-online.target mcpanel-agent.service
```

ersetzen durch:

```ini
Wants=mcpanel-agent.service mcpanel-files.socket
After=network-online.target mcpanel-agent.service mcpanel-files.socket
```

und am Ende von `install_system` die Zeile `sudo systemctl enable mcpanel-agent mcpanel-panel caddy >/dev/null 2>&1` ersetzen durch:

```bash
  sudo systemctl enable mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel caddy >/dev/null 2>&1
```

(e) In `restart_and_check` nach `sudo systemctl restart mcpanel-agent` einfügen (erst den Dienst stoppen, dann den Socket neu anlegen, dann den Dienst ausdrücklich starten, damit die Prüfung nicht auf die erste Verbindung warten muss):

```bash
  sudo systemctl stop mcpanel-files.service
  sudo systemctl restart mcpanel-files.socket
  sudo systemctl start mcpanel-files.service
```

und vor `return 0` (innerhalb des erfolgreichen `if`) einfügen:

```bash
      check_files_service || exit 1
```

Neue Funktion vor `restart_and_check` einfügen:

```bash
# check_files_service: socket unit and service active, socket mcpanel-mc:mcpanel 0660; the
# service runs as mcpanel-mc without supplementary groups, without capabilities and with
# no_new_privs (spec 3b §3.1); prints what is wrong otherwise.
check_files_service() {
  local pid uid status
  if ! sudo systemctl is-active --quiet mcpanel-files.socket || ! sudo systemctl is-active --quiet mcpanel-files.service; then
    echo "testvm-deploy: mcpanel-files.socket/.service not active" >&2
    sudo systemctl --no-pager --lines=20 status mcpanel-files.socket mcpanel-files.service >&2 || true
    return 1
  fi
  if [ "$(sudo stat -c '%U:%G %a' /run/mc-panel-files/files.sock)" != "mcpanel-mc:mcpanel 660" ] ||
    [ "$(sudo stat -c '%a' /run/mc-panel-files)" != 755 ]; then
    echo "testvm-deploy: files socket: $(sudo stat -c '%U:%G %a %n' /run/mc-panel-files /run/mc-panel-files/files.sock)" >&2
    return 1
  fi
  pid="$(systemctl show -p MainPID --value mcpanel-files)"
  uid="$(id -u mcpanel-mc)"
  status="$(sudo cat "/proc/$pid/status")"
  if ! grep -qxP "Uid:\t$uid\t$uid\t$uid\t$uid" <<<"$status" ||
    ! grep -qxP 'CapEff:\t0000000000000000' <<<"$status" ||
    ! grep -qxP 'NoNewPrivs:\t1' <<<"$status" ||
    grep -P '^Groups:' <<<"$status" | grep -qw "$(getent group mcpanel | cut -d: -f3)"; then
    echo "testvm-deploy: mcpanel-files runs with unexpected credentials:" >&2
    grep -E '^(Uid|Gid|Groups|CapEff|NoNewPrivs):' <<<"$status" >&2
    return 1
  fi
}
```

- [ ] **Step 4: `.goreleaser.yaml`** – nach dem Build-Eintrag `agent` (vor `archives:`) einfügen:

```yaml
  - id: files
    main: ./cmd/files
    binary: files
    env:
      - CGO_ENABLED=0
    goos:
      - linux
    goarch:
      - amd64
      - arm64
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.version={{ .Version }}
    mod_timestamp: "{{ .CommitTimestamp }}"
    hooks:
      post:
        - cmd: >-
            sh -c 'go version -m "{{ .Path }}" | grep -q "CGO_ENABLED=0" || { echo "cgo detected in {{ .Path }}" >&2; exit 1; }'
          output: true
```

und unter `archives[0].ids` `- files` ergänzen:

```yaml
    ids:
      - panel
      - agent
      - files
```

- [ ] **Step 5: `.github/workflows/ci.yml`**

Job `e2e`, Schritt „Start stack“: nach der agent-Zeile einfügen:

```yaml
          .dev/bin/files -dev > files.log 2>&1 &
```

Schritt „Logs on failure“: `run: tail -n 200 agent.log panel.log vite.log` → `run: tail -n 200 agent.log files.log panel.log vite.log`.

Job `instances`: Schritt „Build agent“ und „Instance acceptance“ ersetzen durch:

```yaml
      - name: Build agent and files service
        run: |
          go build -o bin/agent ./cmd/agent
          go build -o bin/files ./cmd/files

      # Real agent + Docker + itzg Paper server: routing by hostname, hardening, console
      # (rcon, log stream, player lists, planted player files), automatic restart after a
      # crash, removal; plus the files service as mcpanel-mc (peer UID, os.Root limits,
      # redaction, upload/download, extract incl. zip slip).
      - name: Instance acceptance
        run: sudo scripts/ci-instances.sh bin/agent bin/files
```

Job `build`, Schritt „Verify snapshot artifacts“:

```bash
          if [ "${count}" -ne 4 ]; then
            echo "::error::expected 4 binaries (panel+agent x amd64+arm64), got ${count}"
```

ersetzen durch:

```bash
          if [ "${count}" -ne 6 ]; then
            echo "::error::expected 6 binaries (panel+agent+files x amd64+arm64), got ${count}"
```

und

```bash
            if [ "${contents}" != "agent panel" ]; then
              echo "::error::$1 contains '${contents}', expected 'agent panel'"
```

ersetzen durch:

```bash
            if [ "${contents}" != "agent files panel" ]; then
              echo "::error::$1 contains '${contents}', expected 'agent files panel'"
```

- [ ] **Step 6: `scripts/ci-instances.sh`** – neun Stellen (Reihenfolge wie im Skript).

(1) Kopfkommentar (Zeilen 2–10) ersetzen durch:

```bash
# Instance acceptance for a Linux CI runner with Docker (sub-project 2, spec §12 criteria 2,
# 6, 7, 8 on the agent path; sub-project 3a, spec §10 criteria 2 and 4 on the agent path;
# sub-project 3b, spec §9 criteria 2-4 on the files-service path): starts the real agent as
# root and the files service as mcpanel-mc, creates a Paper server through the agent socket,
# waits until it answers a status ping routed by hostname through mc-router, checks the
# container hardening, the console endpoints and the files service (credentials, peer UID,
# upload, redaction, editing, symlinks, directory zip), crashes it, waits for Docker's
# restart, checks the console on the stopped server, planted player files and archive
# extraction (incl. zip slip), and removes it.
#
# Usage (as root): scripts/ci-instances.sh AGENT_BINARY FILES_BINARY
# Changes the host (users, Docker containers/network, port 25565): disposable CI runners only.
```

(2) Argumentprüfung ersetzen:

```bash
if [ "$#" -ne 2 ]; then
  echo "usage: scripts/ci-instances.sh AGENT_BINARY FILES_BINARY" >&2
  exit 2
fi
```

(3) Nach `agent_pid=""` einfügen:

```bash
fsock="$work/frun/files.sock"
fo="$work/fout"
files_pid=""
```

(4) In `cleanup()` vor dem agent-Block einfügen:

```bash
  if [ -n "$files_pid" ]; then
    kill "$files_pid" 2>/dev/null || true
    wait "$files_pid" 2>/dev/null || true
  fi
```

(5) In `fail()` nach `cat "$work/agent.log" >&2 || true` einfügen:

```bash
  echo "--- files log" >&2
  cat "$work/files.log" >&2 || true
```

(6) Nach der Funktion `players()` (vor `check_players`) einfügen:

```bash
# fcurl <curl args…>: talks to the files service as the panel user, the only peer it accepts.
fcurl() { setpriv --reuid=mcpanel --regid=mcpanel --init-groups curl -sS --max-time 60 --unix-socket "$fsock" "$@"; }

# fpost <path> <json or @file> <outfile>: POSTs JSON to the files service, prints the HTTP status.
fpost() {
  fcurl -o "$3" -w '%{http_code}' -H 'Content-Type: application/json' -X POST "http://files$1" --data-binary "$2"
}

# fput <local file> <path in data dir> <overwrite 0|1> <outfile>: uploads, prints the HTTP status.
fput() {
  fcurl -o "$4" -w '%{http_code}' -H 'Content-Type: application/octet-stream' -T "$1" \
    "http://files/v1/upload?id=9001&path=$2&overwrite=$3"
}

# fget <path in data dir> <outfile>: downloads (file or directory zip), prints the HTTP status;
# the response headers land in <outfile>.headers.
fget() { fcurl --max-time 300 -D "$2.headers" -o "$2" -w '%{http_code}' "http://files/v1/download?id=9001&path=$1"; }

# is_error <file> <got status> <want status> <want code>: true if status and error code match.
is_error() { [ "$2" = "$3" ] && grep -q "\"code\":\"$4\"" "$1"; }

# job_state: state of the last extract job of instance 9001 ("none" without job).
job_state() {
  fcurl -o "$fo/job.json" "http://files/v1/jobs?id=9001" >/dev/null || return 1
  python3 -I -c 'import json, sys; j = json.load(open(sys.argv[1]))["job"]; print(j["state"] if j else "none")' "$fo/job.json"
}
job_finished() { [ "$(job_state)" != running ]; }

# no_tmp_left: the service never leaves its temp files behind (spec §3.3).
no_tmp_left() { [ -z "$(find "$work/mc/instances/9001" -name '.mcpanel-tmp-*' -print -quit)" ]; }

# extract_rejected <zip in data dir> <dest> <status> <code>: the pre-check runs synchronously
# in the request (spec §4.2), so a bad archive must fail with exactly <status>/<code>, no job
# may start, and nothing may exist at <dest>.
extract_rejected() {
  local code before
  before="$(fcurl "http://files/v1/jobs?id=9001")" || return 1
  code="$(fpost /v1/extract "{\"id\": 9001, \"path\": \"$1\", \"dest\": \"$2\"}" "$fo/extract.json")"
  is_error "$fo/extract.json" "$code" "$3" "$4" || return 1
  [ "$(fcurl "http://files/v1/jobs?id=9001")" = "$before" ] || return 1 # no new job
  [ ! -e "$work/mc/instances/9001/$2" ]
}
```

(7) Nach dem Block `docker_ok || fail "agent reports docker unavailable"` einfügen (der agent hat `instances/` beim Start angelegt):

```bash
echo "== setup: files service as mcpanel-mc"
install -m 0755 "$2" "$work/bin/files"
# No socket activation here: the service creates its socket itself in a directory it owns. The
# socket then gets the service's primary group (mcpanel-mc), which the panel test user is not in,
# so socket_mode is 0666: the peer-UID check is the access control (nobody is refused below;
# root is allowed, like at the agent). Production uses mcpanel-files.socket with group mcpanel and 0660 instead.
install -d -m 0755 -o mcpanel-mc -g mcpanel-mc "$work/frun"
install -d -m 0750 -o mcpanel -g mcpanel "$fo"
cat >"$work/files.toml" <<EOF
socket = "$fsock"
socket_mode = "0666"
panel_uid = $(id -u mcpanel)
instances_dir = "$work/mc"
[limits]
max_upload_bytes = 10485760
max_extract_bytes = 104857600
max_extract_entries = 1000
reserve_bytes = 1048576
max_text_bytes = 5242880
max_list_entries = 10000
EOF
chmod 0644 "$work/files.toml"
[ "$(stat -c '%U:%G %a' "$work/mc/instances")" = "root:mcpanel-mc 750" ] ||
  fail "instances/ owner/mode: $(stat -c '%U:%G %a' "$work/mc/instances") (want root:mcpanel-mc 750)"
setpriv --reuid=mcpanel-mc --regid=mcpanel-mc --clear-groups \
  --inh-caps=-all --bounding-set=-all --no-new-privs \
  "$work/bin/files" -config "$work/files.toml" >"$work/files.log" 2>&1 &
files_pid=$!
for _ in $(seq 1 50); do [ -S "$fsock" ] && break; sleep 0.2; done
[ -S "$fsock" ] || fail "files socket missing"
[ "$(stat -c '%U:%G %a' "$fsock")" = "mcpanel-mc:mcpanel-mc 666" ] || fail "files socket owner/mode: $(stat -c '%U:%G %a' "$fsock")"
fcurl -f http://files/v1/health >"$fo/health.json" || fail "files health as panel user"
grep -q '"status":"ok"' "$fo/health.json" || fail "files health: $(cat "$fo/health.json")"
echo "OK: files service up"
```

(8) Vor `echo "== crash: stop the server from inside, Docker must restart it (unless-stopped)"` einfügen:

```bash
echo "== files: service runs as mcpanel-mc without privileges, only the panel user and root may connect"
data="$work/mc/instances/9001"
uid_mc="$(id -u mcpanel-mc)"
grep -qxP "Uid:\t$uid_mc\t$uid_mc\t$uid_mc\t$uid_mc" "/proc/$files_pid/status" ||
  fail "files service uid: $(grep '^Uid:' "/proc/$files_pid/status")"
grep -qxP 'CapEff:\t0000000000000000' "/proc/$files_pid/status" || fail "files service has capabilities"
grep -qxP 'NoNewPrivs:\t1' "/proc/$files_pid/status" || fail "files service without no_new_privs"
# root may connect (like at the agent): operators can call the service from a root shell.
curl -fsS --max-time 5 --unix-socket "$fsock" http://files/v1/health >/dev/null || fail "root was refused by the files service"
# The CI socket is 0666 (see setup): any local user can connect, so this proves that the
# peer-UID check, not the file mode, keeps other users out.
if setpriv --reuid=nobody --regid=nogroup --clear-groups \
  curl -fsS --max-time 5 --unix-socket "$fsock" http://files/v1/health >/dev/null 2>&1; then
  fail "user nobody reached the files service"
fi
if setpriv --reuid=mcpanel-mc --regid=mcpanel-mc --clear-groups touch "$work/mc/router/ci-x" 2>/dev/null; then
  fail "mcpanel-mc can write the router directory"
fi
if setpriv --reuid=mcpanel-mc --regid=mcpanel-mc --clear-groups ls "$work/mc/deleted" >/dev/null 2>&1; then
  fail "mcpanel-mc can read deleted/"
fi
echo "OK: uid mcpanel-mc, no capabilities, no_new_privs; root allowed, nobody refused; router/ and deleted/ out of reach"

echo "== files: upload a plugin jar"
head -c 65536 /dev/urandom >"$work/ci-plugin.jar"
chmod 0644 "$work/ci-plugin.jar"
code="$(fput "$work/ci-plugin.jar" plugins/ci-plugin.jar 0 "$fo/up.json")"
[ "$code" = 201 ] || fail "upload plugin: HTTP $code $(cat "$fo/up.json")"
cmp -s "$work/ci-plugin.jar" "$data/plugins/ci-plugin.jar" || fail "uploaded jar differs"
[ "$(stat -c '%U' "$data/plugins/ci-plugin.jar")" = mcpanel-mc ] || fail "uploaded jar owner: $(stat -c '%U' "$data/plugins/ci-plugin.jar")"
code="$(fput "$work/ci-plugin.jar" plugins/ci-plugin.jar 0 "$fo/up.json")"
is_error "$fo/up.json" "$code" 409 exists || fail "upload without overwrite: HTTP $code $(cat "$fo/up.json")"
code="$(fput "$work/ci-plugin.jar" plugins/ci-plugin.jar 1 "$fo/up.json")"
[ "$code" = 201 ] || fail "upload with overwrite: HTTP $code $(cat "$fo/up.json")"
head -c $((11 * 1024 * 1024)) /dev/zero >"$work/big.bin"
chmod 0644 "$work/big.bin"
code="$(fput "$work/big.bin" big.bin 0 "$fo/up.json")"
is_error "$fo/up.json" "$code" 413 too_large || fail "upload over max_upload_bytes: HTTP $code $(cat "$fo/up.json")"
[ ! -e "$data/big.bin" ] || fail "rejected upload left big.bin behind"
no_tmp_left || fail "upload left a .mcpanel-tmp-* file"
echo "OK: upload (201, owner mcpanel-mc, 409 exists, overwrite, 413 too_large without leftovers)"

echo "== files: server.properties redacted, RCON files hidden"
real_pw="$(sed -n 's/^rcon\.password=//p' "$data/server.properties")"
[ -n "$real_pw" ] && [ "$real_pw" != '********' ] || fail "server.properties has no real rcon.password to protect"
code="$(fpost /v1/read '{"id": 9001, "path": "server.properties"}' "$fo/props.json")"
[ "$code" = 200 ] || fail "read server.properties: HTTP $code $(cat "$fo/props.json")"
python3 -I -c '
import json, sys
c = json.load(open(sys.argv[1]))["content"]
if "rcon.password=********" not in c.splitlines() or sys.argv[2] in c:
    sys.exit("server.properties not redacted")' "$fo/props.json" "$real_pw" || fail "read redaction"
code="$(fget server.properties "$fo/props.dl")"
[ "$code" = 200 ] || fail "download server.properties: HTTP $code"
grep -qx 'rcon.password=\*\*\*\*\*\*\*\*' "$fo/props.dl" || fail "download not redacted"
if grep -qF -- "$real_pw" "$fo/props.dl"; then fail "download leaks the RCON password"; fi
[ -f "$data/.rcon-cli.env" ] || fail "itzg did not write .rcon-cli.env (test precondition)"
code="$(fpost /v1/list '{"id": 9001, "path": ""}' "$fo/list.json")"
[ "$code" = 200 ] || fail "list root: HTTP $code"
python3 -I -c '
import json, sys
names = [e["name"] for e in json.load(open(sys.argv[1]))["entries"]]
bad = [n for n in names if n.startswith((".rcon-cli", ".mcpanel-"))]
if bad or "server.properties" not in names:
    sys.exit("list shows %r / lacks server.properties" % (bad,))' "$fo/list.json" || fail "hidden files in the list"
for p in .rcon-cli.env .rcon-cli.yaml; do
  code="$(fpost /v1/read "{\"id\": 9001, \"path\": \"$p\"}" "$fo/hidden.json")"
  is_error "$fo/hidden.json" "$code" 404 not_found || fail "read $p: HTTP $code $(cat "$fo/hidden.json")"
  code="$(fget "$p" "$fo/hidden.dl")"
  [ "$code" = 404 ] || fail "download $p: HTTP $code"
done
echo "OK: rcon.password redacted (read, download), .rcon-cli.* invisible and unreadable"

echo "== files: edit a config with optimistic locking"
code="$(fpost /v1/read '{"id": 9001, "path": "bukkit.yml"}' "$fo/read.json")"
[ "$code" = 200 ] || fail "read bukkit.yml: HTTP $code $(cat "$fo/read.json")"
python3 -I -c '
import json, sys
r = json.load(open(sys.argv[1]))
print(json.dumps({"id": 9001, "path": "bukkit.yml", "content": r["content"] + "\n# ci-edit\n", "expected_mtime": r["mtime"]}))' "$fo/read.json" >"$work/write.json"
chmod 0644 "$work/write.json"
code="$(fpost /v1/write "@$work/write.json" "$fo/write.out")"
[ "$code" = 200 ] || fail "write bukkit.yml: HTTP $code $(cat "$fo/write.out")"
grep -qx '# ci-edit' "$data/bukkit.yml" || fail "edit not on disk"
[ "$(stat -c '%U' "$data/bukkit.yml")" = mcpanel-mc ] || fail "edited file owner: $(stat -c '%U' "$data/bukkit.yml")"
code="$(fpost /v1/write "@$work/write.json" "$fo/write.out")"
is_error "$fo/write.out" "$code" 409 modified || fail "write with stale mtime: HTTP $code $(cat "$fo/write.out")"
code="$(fpost /v1/write '{"id": 9001, "path": "ci-new.txt", "content": "hello\n", "expected_mtime": null}' "$fo/write.out")"
[ "$code" = 200 ] || fail "create ci-new.txt: HTTP $code $(cat "$fo/write.out")"
code="$(fpost /v1/write '{"id": 9001, "path": "ci-new.txt", "content": "again\n", "expected_mtime": null}' "$fo/write.out")"
is_error "$fo/write.out" "$code" 409 exists || fail "create existing file: HTTP $code $(cat "$fo/write.out")"
code="$(fpost /v1/read '{"id": 9001, "path": "plugins/ci-plugin.jar"}' "$fo/bin.json")"
is_error "$fo/bin.json" "$code" 415 not_text || fail "read a binary file: HTTP $code $(cat "$fo/bin.json")"
no_tmp_left || fail "write left a .mcpanel-tmp-* file"
echo "OK: write with expected_mtime, 409 modified, create-only 409 exists, 415 not_text"

echo "== files: mkdir, rename, delete"
code="$(fpost /v1/mkdir '{"id": 9001, "path": "ci-dir"}' "$fo/m.json")"
[ "$code" = 201 ] || fail "mkdir: HTTP $code $(cat "$fo/m.json")"
code="$(fpost /v1/mkdir '{"id": 9001, "path": "ci-dir"}' "$fo/m.json")"
is_error "$fo/m.json" "$code" 409 exists || fail "mkdir existing: HTTP $code $(cat "$fo/m.json")"
code="$(fpost /v1/rename '{"id": 9001, "from": "ci-new.txt", "to": "ci-dir/moved.txt"}' "$fo/m.json")"
[ "$code" = 204 ] || fail "rename: HTTP $code $(cat "$fo/m.json")"
[ -f "$data/ci-dir/moved.txt" ] && [ ! -e "$data/ci-new.txt" ] || fail "rename not on disk"
code="$(fpost /v1/rename '{"id": 9001, "from": "ci-dir", "to": "ci-dir/inner"}' "$fo/m.json")"
[ "$code" = 400 ] || fail "rename a directory into itself: HTTP $code $(cat "$fo/m.json")"
code="$(fpost /v1/delete '{"id": 9001, "path": "ci-dir"}' "$fo/m.json")"
[ "$code" = 204 ] || fail "delete: HTTP $code $(cat "$fo/m.json")"
[ ! -e "$data/ci-dir" ] || fail "deleted directory still there"
code="$(fpost /v1/delete '{"id": 9001, "path": ""}' "$fo/m.json")"
[ "$code" = 400 ] || fail "delete the root: HTTP $code $(cat "$fo/m.json")"
echo "OK: mkdir (201/409), rename (204, not into itself), delete (204, root refused)"

echo "== files: symlinks are never followed, nothing outside the data dir is reachable"
ln -s /etc/shadow "$data/ci-shadow"
ln -s ../../router "$data/ci-router" # relative symlink out of the data dir (to <instances_dir>/router)
code="$(fpost /v1/list '{"id": 9001, "path": ""}' "$fo/list.json")"
python3 -I -c '
import json, sys
t = {e["name"]: e["type"] for e in json.load(open(sys.argv[1]))["entries"]}
if t.get("ci-shadow") != "symlink" or t.get("ci-router") != "symlink":
    sys.exit("symlinks not listed as symlink: %r" % {k: t.get(k) for k in ("ci-shadow", "ci-router")})' "$fo/list.json" || fail "symlink listing"
code="$(fpost /v1/read '{"id": 9001, "path": "ci-shadow"}' "$fo/s.json")"
is_error "$fo/s.json" "$code" 400 not_regular || fail "read symlink: HTTP $code $(cat "$fo/s.json")"
code="$(fget ci-shadow "$fo/s.dl")"
[ "$code" = 400 ] || fail "download symlink: HTTP $code"
if grep -q 'root:' "$fo/s.dl" "$fo/s.json"; then fail "symlink leaked /etc/shadow"; fi
code="$(fpost /v1/list '{"id": 9001, "path": "ci-router"}' "$fo/s.json")"
[ "$code" = 400 ] || [ "$code" = 404 ] || fail "list through a symlinked directory: HTTP $code $(cat "$fo/s.json")"
code="$(fput "$work/ci-plugin.jar" ci-router/evil.jar 0 "$fo/s.json")"
is_error "$fo/s.json" "$code" 400 not_regular || fail "upload through a symlinked directory: HTTP $code $(cat "$fo/s.json")"
code="$(fpost /v1/write '{"id": 9001, "path": "ci-router/routes.json", "content": "{}", "expected_mtime": null}' "$fo/s.json")"
is_error "$fo/s.json" "$code" 400 not_regular || fail "write through a symlinked directory: HTTP $code $(cat "$fo/s.json")"
[ ! -e "$work/mc/router/evil.jar" ] || fail "upload escaped into the router directory"
grep -q 'mappings' "$work/mc/router/routes.json" || fail "routes.json was overwritten"
code="$(fpost /v1/read '{"id": 9001, "path": "../../router/routes.json"}' "$fo/s.json")"
is_error "$fo/s.json" "$code" 400 invalid || fail "read with ..: HTTP $code $(cat "$fo/s.json")"
code="$(fpost /v1/rename '{"id": 9001, "from": "ci-shadow", "to": "ci-shadow2"}' "$fo/s.json")"
[ "$code" = 204 ] && [ -L "$data/ci-shadow2" ] || fail "rename of a symlink must move the link itself: HTTP $code"
echo "OK: symlinks listed but refused (read, download, list into, upload/write through); .. refused; rename moves the link"

echo "== files: directory zip download"
code="$(fget "" "$fo/root.zip")"
[ "$code" = 200 ] || fail "download root as zip: HTTP $code"
grep -qi '^x-files-kind: dir' "$fo/root.zip.headers" || fail "root download kind: $(cat "$fo/root.zip.headers")"
python3 -I -c '
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = z.namelist()
bad = [n for n in names if n.startswith(("/", "../")) or "/../" in n or n.split("/")[0] in (".rcon-cli.env", ".rcon-cli.yaml", "ci-shadow2", "ci-router") or ".mcpanel-" in n]
if bad:
    sys.exit("zip contains %r" % bad[:5])
if "bukkit.yml" not in names or "plugins/ci-plugin.jar" not in names:
    sys.exit("zip lacks expected files")
props = z.read("server.properties").decode()
if "rcon.password=********" not in props.splitlines() or sys.argv[2] in props:
    sys.exit("server.properties in the zip is not redacted")' "$fo/root.zip" "$real_pw" || fail "directory zip content"
rm -f "$data/ci-shadow2" "$data/ci-router"
echo "OK: directory zip without symlinks, hidden or temp files, server.properties redacted"

```

(9) Vor `echo "== remove"` einfügen:

```bash
echo "== files: world zip upload and extract (server stopped)"
python3 -I -c '
import os, sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr("world/level.dat", b"\x0a\x00\x00ci-level")
    z.writestr("world/region/r.0.0.mca", os.urandom(65536))
    z.writestr("world/datapacks/", b"")' "$work/world-ci.zip"
chmod 0644 "$work/world-ci.zip"
code="$(fput "$work/world-ci.zip" world-ci.zip 0 "$fo/up.json")"
[ "$code" = 201 ] || fail "upload world zip: HTTP $code $(cat "$fo/up.json")"
code="$(fpost /v1/extract '{"id": 9001, "path": "world-ci.zip", "dest": "world-ci"}' "$fo/extract.json")"
[ "$code" = 202 ] || fail "extract: HTTP $code $(cat "$fo/extract.json")"
wait_for 120 "extract job finished" job_finished
[ "$(job_state)" = "done" ] || fail "extract job: $(cat "$fo/job.json")"
python3 -I -c '
import json, sys
j = json.load(open(sys.argv[1]))["job"]
if j["entries"] < 2 or j["bytes"] < 65536 or j["dest"] != "world-ci":
    sys.exit("job counters: %r" % j)' "$fo/job.json" || fail "extract job counters"
[ -f "$data/world-ci/world/level.dat" ] && [ -f "$data/world-ci/world/region/r.0.0.mca" ] || fail "extracted files missing"
[ -z "$(find "$data/world-ci" ! -user mcpanel-mc -print -quit)" ] || fail "extracted files not owned by mcpanel-mc"
no_tmp_left || fail "extract left a .mcpanel-tmp-* directory"
code="$(fpost /v1/extract '{"id": 9001, "path": "world-ci.zip", "dest": "world-ci"}' "$fo/extract.json")"
is_error "$fo/extract.json" "$code" 409 exists || fail "extract into a non-empty directory: HTTP $code $(cat "$fo/extract.json")"
echo "OK: world zip extracted (job done, owner mcpanel-mc, no temp left); non-empty destination refused"

echo "== files: malicious archives are refused synchronously, before anything is written"
python3 -I -c '
import sys, zipfile
d = sys.argv[1]
with zipfile.ZipFile(d + "/slip.zip", "w") as z:
    z.writestr("ok.txt", "ok")
    z.writestr("../evil.txt", "evil")
with zipfile.ZipFile(d + "/abs.zip", "w") as z:
    z.writestr("ok.txt", "ok")
    z.writestr(zipfile.ZipInfo("/tmp/ci-abs-evil.txt"), "evil")
with zipfile.ZipFile(d + "/link.zip", "w") as z:
    i = zipfile.ZipInfo("link")
    i.create_system = 3
    i.external_attr = 0o120777 << 16
    z.writestr(i, "/etc/shadow")
with zipfile.ZipFile(d + "/hidden.zip", "w") as z:
    z.writestr(".rcon-cli.env", "password=x")
with zipfile.ZipFile(d + "/many.zip", "w") as z:
    for n in range(1001):
        z.writestr("f%04d.txt" % n, "")
with zipfile.ZipFile(d + "/huge.zip", "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr("zeros.bin", bytes(101 * 1024 * 1024))' "$work"
# Path rules and entry types -> 400 invalid; limits (entries, declared size) -> 413 too_large.
for c in "slip 400 invalid" "abs 400 invalid" "link 400 invalid" "hidden 400 invalid" "many 413 too_large" "huge 413 too_large"; do
  read -r z status ecode <<<"$c"
  chmod 0644 "$work/$z.zip"
  code="$(fput "$work/$z.zip" "$z.zip" 0 "$fo/up.json")"
  [ "$code" = 201 ] || fail "upload $z.zip: HTTP $code $(cat "$fo/up.json")"
  extract_rejected "$z.zip" "evil-$z" "$status" "$ecode" ||
    fail "$z.zip: want $status $ecode without a job and without evil-$z, got $(cat "$fo/extract.json")"
done
[ ! -e "$work/mc/instances/evil.txt" ] || fail "zip slip wrote outside the data directory"
[ ! -e /tmp/ci-abs-evil.txt ] || fail "absolute zip path was written"
no_tmp_left || fail "refused archives left a .mcpanel-tmp-* directory"
echo "OK: zip slip, absolute path, symlink entry, hidden name -> 400 invalid; 1001 entries, 101 MiB declared -> 413 too_large; no job, nothing written"

```

Und die letzte Zeile `echo "PASS: instance acceptance (agent path)"` ersetzen durch:

```bash
echo "PASS: instance acceptance (agent and files path)"
```

Hinweis zu (8): Der Abschnitt läuft bei laufendem Server (nach den Konsolen-Prüfungen, vor dem Absturz-Test); `$data` wird dort gesetzt und in (9) sowie im bestehenden Abschnitt „player files“ weiterverwendet (dort steht bereits `data="$work/mc/instances/9001"` – die Zuweisung ist identisch und darf bleiben).

- [ ] **Step 7: Lokal prüfen**

```bash
bash -n scripts/ci-instances.sh scripts/testvm-deploy.sh scripts/dev.sh
command -v shellcheck >/dev/null && shellcheck scripts/ci-instances.sh scripts/testvm-deploy.sh scripts/dev.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
make check
```

Expected: keine Ausgabe von `bash -n`/`shellcheck`/`actionlint`, `make check` grün. `shellcheck` ist nur vorhanden, wenn der Nutzer es installiert hat; nicht installieren.

- [ ] **Step 8: Commit, Push, Draft-PR, CI**

```bash
git add scripts/dev.sh scripts/testvm-deploy.sh scripts/ci-instances.sh .goreleaser.yaml .github/workflows/ci.yml
git commit -m "ci(files): run the files service in dev, CI and on the test VM; accept it on a real server

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/files-ci-docs
gh pr create --draft --title "ci,docs: TP3b files service in operation, CI acceptance and documentation" --body "$(cat <<'EOF'
Phase C of docs/superpowers/plans/2026-10-11-files-00-overview.md (Tasks 18–19).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
EOF
)"
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün; im Job „Instance acceptance“ erscheinen alle `OK: … files …`-Zeilen und `PASS: instance acceptance (agent and files path)`; im Job `build` 6 Binaries und Archive mit `agent files panel`. Bei Fehlern `gh run view <id> --log-failed`; Skriptfehler dürfen auf der Test-VM nachgestellt werden (Kopie nach `~/wt-files`, dort `sudo bash scripts/ci-instances.sh …` – das Skript verändert den Host, auf der VM erlaubt).

- [ ] **Step 9: Test-VM-Deploy des Branches prüfen**

```bash
make deploy-testvm REF=feat/files-ci-docs
ssh claude@192.168.1.117 'systemctl is-active mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel caddy; sudo stat -c "%U:%G %a %n" /srv/mc-panel/instances /etc/mc-panel-files/files.toml /run/mc-panel-files /run/mc-panel-files/files.sock; sudo -u mcpanel curl -s --unix-socket /run/mc-panel-files/files.sock http://files/v1/health'
```

(SSH nur mit abgeschalteter Sandbox.) Expected: fünfmal `active`; `root:mcpanel-mc 750 /srv/mc-panel/instances`, `root:mcpanel-mc 640 /etc/mc-panel-files/files.toml`, `mcpanel-mc:mcpanel-mc 755 /run/mc-panel-files`, `mcpanel-mc:mcpanel 660 /run/mc-panel-files/files.sock`; Health `{"status":"ok",…}`. Das Deploy-Skript prüft Socket, UID, Zusatzgruppen, Capabilities und `no_new_privs` selbst (`check_files_service`). Danach `make deploy-testvm` (ohne REF) ist nicht nötig; der Lead deployt `main` nach dem Merge.

---

### Task 19: Doku

**Files:**
- Create: `docs/dev/acceptance-tp3b.md`
- Modify: `docs/dev/README.md`
- Modify: `CLAUDE.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: Spec §1–§9, das tatsächlich gemergte Verhalten (vor dem Schreiben jede Zahl gegen den Code auf `main` prüfen: `internal/files`, `internal/serverfiles`, `internal/config/files.go`, `web/src/features/files`), Aufgabe 18.
- Produces: Checkliste für die manuelle Abnahme (Kriterien 2–7), aktualisierte Entwickler- und Betriebsdoku.

- [ ] **Step 1: `docs/dev/acceptance-tp3b.md` anlegen** (vollständiger Inhalt):

```markdown
# Abnahme Teilprojekt 3b (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§9).

Automatisch geprüft (CI): Unit- und Linux-Tests des Dienstes (Pfadregeln, `os.Root`-Grenzen, Symlinks, FIFO, atomares Schreiben, Upload-Grenzen, Schwärzen, Ordner-Zip, Entpacken mit Zip-Slip/Zip-Bombe/Symlink-Einträgen), panel-Tests (Rechte, CSRF, Streaming ohne Puffern, Audit, Entpacken nur gestoppt, Power-Sperre), Instanz-Abnahme (`scripts/ci-instances.sh`: Dienst als `mcpanel-mc` ohne Capabilities, nur die panel-UID darf verbinden, Upload, Schwärzen, Editor-Sperre, Symlinks, Ordner-Zip, Welt entpacken, bösartige Archive), Playwright gegen den Mock (Dateien-Tab).

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client und echter Session.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP3b-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>.
2. Dienst prüfen: `ssh claude@192.168.1.117 'systemctl is-active mcpanel-files.socket mcpanel-files.service; ps -o user=,group=,supgrp= -C files; sudo stat -c "%U:%G %a" /run/mc-panel-files/files.sock'` → zweimal `active`, Benutzer `mcpanel-mc`, Gruppe `mcpanel-mc`, keine Zusatzgruppe `mcpanel`; Socket `mcpanel-mc:mcpanel 660`. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`.
3. Als `admin` anmelden. Server `survival` (Paper) existiert und wurde mindestens einmal gestartet (sonst gibt es noch kein Datenverzeichnis).
4. Benutzer `freund` hat nur `server.view` auf `survival` (aus TP3a); Benutzer `helfer` bekommt zusätzlich `server.files` (Benutzer → Rechte; beim Anhaken erscheint der Hinweis „Erlaubt Code-Ausführung im Minecraft-Server (z. B. über Plugins)“).
5. Auf dem Mac vorbereiten: eine Welt als Zip (z. B. eine Einzelspieler-Welt aus `~/Library/Application Support/minecraft/saves/<welt>` mit Finder → „Komprimieren“; die Zip enthält den Ordner `<welt>/`), ein kleines Paper-Plugin als `.jar` (z. B. von <https://hangar.papermc.io>, etwas Harmloses wie „Chunky“) und eine Datei größer als 5 MiB, die kein Text ist.
6. Minecraft Java Edition im selben Netz, Adresse `survival.mc.192-168-1-117.sslip.io`.

## Kriterien

| # | Kriterium (Spec §9) | Vorgehen | Erwartet |
|---|---|---|---|
| 2a | Welt hochladen | als `helfer` Server `survival` → Tab „Dateien“ → „Hochladen“ → Welt-Zip; Server läuft dabei | Fortschritt in der Upload-Warteschlange, danach steht die Zip in der Liste; Hinweis-Leiste „Änderungen wirken meist erst nach einem Neustart“ |
| 2b | Entpacken gesperrt bei laufendem Server | Menü der Zip → „Entpacken …“ | Aktion gesperrt mit Hinweis „nur bei gestopptem Server“ |
| 2c | Entpacken | als `admin` Server stoppen; als `helfer` „Entpacken …“ → Ziel `neue-welt` | Fortschrittsbalken (Bytes, Einträge), Power-Buttons der Detailseite während des Jobs gesperrt; danach Ordner `neue-welt/<welt>/` mit `level.dat`; ein Start während des Jobs (zweites Fenster, `admin`) wird mit „Entpacken läuft“ abgelehnt |
| 2d | Server startet mit der Welt | Ordner `world` umbenennen in `world-alt`, `neue-welt/<welt>` verschieben/umbenennen nach `world` (Dialog „Umbenennen/Verschieben“), Server starten, mit dem Client verbinden | Spieler steht in der hochgeladenen Welt |
| 3 | Plugin | Plugin-Jar nach `plugins/` hochladen; Server neu starten; in der Konsole `plugins` | Plugin erscheint in der Liste (grün) |
| 4a | Config bearbeiten | `plugins/<Plugin>/config.yml` (oder `bukkit.yml`) öffnen, Wert ändern, Strg/Cmd+S | Speichern bestätigt; nach erneutem Öffnen ist die Änderung da |
| 4b | Konflikt | dieselbe Datei in zwei Tabs öffnen; in Tab A ändern und speichern; in Tab B ändern und speichern | Tab B zeigt den Dialog „Datei wurde geändert“ mit „Neu laden“ und „Überschreiben“; beide Wege funktionieren |
| 4c | `server.properties` | `server.properties` öffnen | Hinweis „wird beim Start aus den panel-Einstellungen überschrieben“; `rcon.password=********`; die Dateien `.rcon-cli.env`/`.rcon-cli.yaml` sind in der Liste nicht zu sehen; nach Speichern und Neustart funktioniert die Konsole (RCON) weiter |
| 4d | Kein Text | die >5-MiB-Binärdatei hochladen und öffnen | statt Editor ein Download-Knopf |
| 5 | Ordner herunterladen | Ordner `world` → „Herunterladen“ | Browser lädt `world.zip`; die Zip lässt sich öffnen und enthält die Welt |
| 6a | nur `server.view` | als `freund` Server `survival` öffnen | kein Tab „Dateien“ |
| 6b | API-Sperre | als `freund` in den DevTools: `await fetch('/api/servers/<id>/files').then(r => r.status)` | `403` |
| 7 | Audit | als `admin` Audit-Log, Filter auf `file.`-Aktionen; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | Einträge für Upload (mit Größe), Entpacken (mit Ziel), Umbenennen (von/nach), Speichern, Löschen und Download (2a–5) mit Benutzer `helfer`; Auflisten/Öffnen erscheinen nicht; `verify` meldet OK |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px): Dateiliste als Karten, Upload über die Dateiauswahl, Editor bedienbar, kein horizontales Scrollen der Seite.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
```

- [ ] **Step 2: `docs/dev/README.md`** – sieben Stellen.

(a) Abschnitt „Schnellstart“: den Absatz ab `` `make dev` baut `` ersetzen durch:

```markdown
`make dev` baut `.dev/bin/panel`, `.dev/bin/agent` und `.dev/bin/files` und startet agent, den Dateidienst (`files -dev`), `panel serve -dev` und Vite. Endet einer der Prozesse, beendet `scripts/dev.sh` die anderen und bricht mit Exit-Code ≠ 0 ab (die Ausgabe nennt den Prozess). Strg+C stoppt alles.
```

(b) Abschnitt „Minecraft-Server lokal (optional)“: am Ende der Aufzählung (vor „Funktionstests gegen echte Server …“) einfügen:

```markdown
- Der Dateidienst läuft im Dev-Modus als eigener Benutzer und arbeitet auf demselben Instanz-Verzeichnis wie der dev-agent (`.dev/mc/instances/<id>`); fehlt das Verzeichnis eines Servers, legt er es an. Der Dateibrowser funktioniert dadurch auch ohne Docker. Die Rechte-Trennung (eigener Benutzer `mcpanel-mc`, keine Capabilities) gibt es nur auf Linux (Test-VM, CI).
```

und den Satz „die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus) und `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler).“ ersetzen durch „die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus), `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler) und `docs/dev/acceptance-tp3b.md` (Dateibrowser).“

(c) Abschnitt „Test-VM“, erster Spiegelstrich: „baut panel und agent“ → „baut panel, agent und files“; „systemd-Units (`mcpanel-agent`, `mcpanel-panel`, starten auch nach einem Neustart)“ → „systemd-Units (`mcpanel-agent`, `mcpanel-files.socket` + `mcpanel-files.service`, `mcpanel-panel`, starten auch nach einem Neustart)“; ans Ende des Spiegelstrichs anfügen: „Danach prüft es Socket (`mcpanel-mc:mcpanel 0660`) und Prozess (`mcpanel-mc`, ohne Zusatzgruppe `mcpanel`, ohne Capabilities, `no_new_privs`) von `mcpanel-files`.“

Spiegelstrich „Logs:“: `journalctl -u mcpanel-agent -u mcpanel-panel -f` → `journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`; „Konfiguration: `/etc/mc-panel/{panel,agent}.toml`“ → „Konfiguration: `/etc/mc-panel/{panel,agent}.toml` und `/etc/mc-panel-files/files.toml`“.

(d) Abschnitt „End-to-End-Tests (Playwright)“, letzter Absatz: „startet agent, `panel serve -dev` und Vite“ → „startet agent, `files -dev`, `panel serve -dev` und Vite“.

(e) Abschnitt „Produktion: Listen-Adresse und Reverse Proxy“: nach dem ersten Spiegelstrich (agent braucht Docker …) einfügen:

```markdown
- Der Dateidienst `files` (Spec 3b §3.1) läuft als `mcpanel-mc` ohne Zusatzgruppen und ohne Capabilities, mit `NoNewPrivileges`, `ProtectSystem=strict` und `ReadWritePaths=<instances_dir>/instances`, und nimmt nur Verbindungen der panel-UID an (`panel_uid`; die Peer-UID-Prüfung ist die eigentliche Zugangskontrolle). Den Socket legt systemd an (Socket-Aktivierung, `mcpanel-files.socket`: `ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660`, `DirectoryMode=0755`), der Dienst übernimmt ihn über `LISTEN_FDS`. Ohne Aktivierung (Dev, CI) legt er `socket` selbst mit `socket_mode` an (Gruppe = seine primäre Gruppe). Verbindliche Vorgaben für die Units des Installers (Teilprojekt 6): eigenes Socket-Verzeichnis `/run/mc-panel-files` (nie `/run/mc-panel` des agents), keine `SupplementaryGroups`, Konfiguration in `/etc/mc-panel-files/files.toml` (`root:mcpanel-mc`, 0750/0640 – `/etc/mc-panel` ist für `mcpanel-mc` unlesbar), `instances/` als `root:mcpanel-mc 0750` (setzt der agent beim Start), `deleted/` bleibt `root:root 0700`. Das panel findet den Dienst über `files_socket` in `panel.toml`.
- Datei-Uploads und -Downloads (bis 4 GiB) laufen als ein Stream durch Caddy und das panel; Caddy begrenzt die Body-Größe standardmäßig nicht. Ein anderer Proxy braucht ein Body-Limit ≥ 4 GiB und darf Uploads nicht puffern.
```

(f) Abschnitt „Make-Targets“: Zeile `make dev` → „baut agent, files und panel, startet agent + `files -dev` + `panel serve -dev` + Vite-Dev-Server, alles unter `./.dev/`; endet ein Prozess, stoppt das Skript alle“; Zeile `make build` → „Frontend bauen, dann `bin/panel`, `bin/agent` und `bin/files` für das Host-System“.

(g) Abschnitt „CI lokal nachstellen“: Zeile `instances` ersetzen durch:

```markdown
| `instances` | nur in der CI: `sudo scripts/ci-instances.sh bin/agent bin/files` (echter agent + Docker + Paper-Server: Routing, Härtung, Konsole mit RCON, Log-Stream und Spielerlisten, präparierte Spielerdateien, Absturz-Neustart, Löschen; Dateidienst als `mcpanel-mc`: Peer-UID, Upload, Schwärzen, Editor-Sperre, Symlinks, Ordner-Zip, Welt entpacken, bösartige Archive; verändert den Host) |
```

und die Zeile `build`: „dann `file dist/*/panel dist/*/agent`“ → „dann `file dist/*/panel dist/*/agent dist/*/files`“.

Abschnitt „Lint-Konventionen“, Spiegelstrich zu Dateimodus `0660`: Den Satz um den Dateidienst ergänzen, je nachdem, wie B1 die G302-Ausnahme gelöst hat (in `.golangci.yml` nachsehen: Pfad `cmd/files/`/`internal/files/` mit Literal `0o660` oder `//nolint:gosec` an der Zeile) – den Text exakt an die gemergte Regel anpassen.

Abschnitt „Release“: „(je `panel` und `agent`, ohne Unterordner)“ → „(je `panel`, `agent` und `files`, ohne Unterordner)“.

- [ ] **Step 3: `CLAUDE.md`** (Englisch, knapp) – sechs Stellen.

(a) Erster Absatz: „production is an Ubuntu 26.04 LTS VPS (panel and agent as systemd services behind Caddy)“ → „production is an Ubuntu 26.04 LTS VPS (panel, agent and the files service as systemd services behind Caddy)“.

(b) Spec-Liste: „(1 foundation, 2 instances and 3a console are done)“ bleibt bis Aufgabe 20; neue Zeile nach der 3a-Zeile:

```markdown
- `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md`: file browser through the unprivileged files service (sub-project 3b); manual acceptance checklist in `docs/dev/acceptance-tp3b.md`.
```

(c) Commands: `make dev                    # agent + \`panel serve -dev\` + Vite; …` → `make dev                    # agent + files -dev + \`panel serve -dev\` + Vite; UI at http://localhost:5173, all state in ./.dev/`; `make build                  # web build, then bin/panel and bin/agent` → `make build                  # web build, then bin/panel, bin/agent and bin/files`.

(d) Architektur: „There are two binaries, built from `cmd/panel` and `cmd/agent`, and they are separated by privilege:“ → „There are three binaries, built from `cmd/panel`, `cmd/agent` and `cmd/files`, and they are separated by privilege:“. Nach dem agent-Spiegelstrich einfügen:

```markdown
- **files** runs as `mcpanel-mc` (the Minecraft containers' user) with no capabilities and `no_new_privs`, never as root. It serves the file browser over its own Unix socket (`internal/files`, contract and path rules in `internal/files/filesapi`, shared with the panel) and accepts only the panel UID; in production systemd creates the socket (`mcpanel-files.socket`, group `mcpanel`, 0660) and hands it over via `LISTEN_FDS`, so the service needs no supplementary groups. Every request opens `<instances_dir>/instances/<id>` with `os.Root`; symlinks are never followed (`Lstat` before, `Stat` + `os.SameFile` after opening), `.rcon-cli.*` and `.mcpanel-*` are hidden, `rcon.password` in `server.properties` is redacted. Uploads/downloads are streams, zip extraction is a job (pre-checked central directory, temp dir, re-counted sizes). The root agent never touches user-supplied paths or archives.
```

(e) „Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `audit/auditapi`)“ → „Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `serverfiles`, `audit/auditapi`)“.

(f) Abschnitt „Minecraft instances“, vor dem letzten Spiegelstrich (Docker integration …) einfügen:

```markdown
- `internal/serverfiles` exposes `/servers/{id}/files/*` (`server.files`, which means full trust: uploading a plugin runs code in the container). Upload and download bypass the 1 MiB body limit and the server timeouts (per-operation deadlines, no buffering, 2 uploads per user); writes, uploads, renames, deletes, extracts and downloads are audited as `file.*`. Extracting needs a stopped server (agent state); while an extract job runs, power start/restart answer `409` and the reconcile loop does not start the server.
```

und den letzten Spiegelstrich „Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints) run only in CI.“ → „Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints and the files service) run only in CI.“

- [ ] **Step 4: `README.md`** – vier Stellen.

(a) „Funktionen“: nach dem Spiegelstrich „Konsole und Spieler“ einfügen:

```markdown
- **Dateien:** Dateibrowser pro Server mit Editor für Konfigurationsdateien, Up- und Download (bis 4 GiB, Ordner als Zip), Anlegen, Umbenennen, Verschieben, Löschen und Entpacken von Zips (z. B. Welt-Upload bei gestopptem Server). Ausgeführt von einem eigenen, unprivilegierten Dienst; das RCON-Passwort bleibt verborgen.
```

und im Spiegelstrich „Rechte“: „(`server.view`, `server.power`, `server.console`)“ → „(`server.view`, `server.power`, `server.console`, `server.files`)“.

(b) Tabelle „Aufbau“: Zeile ergänzen:

```markdown
| `files` | Benutzer `mcpanel-mc` (wie die Minecraft-Container, ohne Capabilities) | Dateibrowser: Dateioperationen im Datenverzeichnis eines Servers, nur für das panel, über einen eigenen Unix-Socket |
```

und den Satz danach ergänzen: „Die Trennung soll verhindern, dass ein kompromittiertes panel Root-Rechte auf dem Host bekommt; Dateioperationen auf Daten, die ein Minecraft-Server kontrolliert, laufen nie als root.“

(c) Schnellstart-Kommentar: `make dev                    # agent + panel (-dev) + Vite, …` → `make dev                    # agent + files + panel (-dev) + Vite, UI unter http://localhost:5173`.

(d) Dokumentation: Zeilen ergänzen

```markdown
| [`docs/dev/acceptance-tp3b.md`](docs/dev/acceptance-tp3b.md) | manuelle Abnahme des Dateibrowsers auf der Test-VM |
| [`docs/superpowers/specs/2026-10-11-mc-panel-files-design.md`](docs/superpowers/specs/2026-10-11-mc-panel-files-design.md) | Dateibrowser über den unprivilegierten Dienst `mcpanel-files` |
```

„Stand“: Zeile `| 3b | Live-Betrieb: Dateien, Plugins/Mods | offen |` ersetzen durch

```markdown
| 3b | Live-Betrieb: Dateibrowser | in Arbeit |
| 3c | Live-Betrieb: Plugins/Mods (Modrinth, Hangar) | offen |
```

„Release“: „(je `panel` und `agent`)“ → „(je `panel`, `agent` und `files`)“.

- [ ] **Step 5: Prüfen und committen**

Jede Zahl und jeden Namen in den Texten gegen den Code auf dem Branch prüfen (z. B. `grep -rn "max_upload_bytes\|MaxUploadBytes" internal/`, `grep -n "file\.\(write\|upload\|download\)" internal/audit/types.go`, Texte im Frontend `grep -rn "Code-Ausführung" web/src/i18n`). Abweichungen im Text korrigieren, nicht im Code.

```bash
make check
git add docs/dev/acceptance-tp3b.md docs/dev/README.md CLAUDE.md README.md
git commit -m "docs: files service and file browser for dev, operation, CI and acceptance

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: CI grün. Danach dem Lead melden (Commit-Bereiche beider Aufgaben, CI-Run-URL, Abweichungen vom Plan).

---

### Task 20: Gesamt-Review, Abnahme, Abschluss (Lead)

**Files:**
- Modify: nach Befund (Fixes durch einen eigenen Teammate auf `fix/files-review-findings`)
- Modify: `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (Angleichung an Fixes; Status erst nach Abnahme)
- Modify: `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (Roadmap-Zeile 3b), `README.md` („Stand“ 3b → fertig), `CLAUDE.md` (Spec-Liste: „3b files“ als done)
- Delete: `docs/superpowers/plans/2026-10-11-files-0*.md` (nach Merge und Abnahme)

**Interfaces:**
- Consumes: Phasen A, B1–B3 auf `main`, Aufgaben 18–19 auf `feat/files-ci-docs`.
- Produces: behobene Review-Befunde, grüne CI auf dem Gesamtstand, abgenommenes Teilprojekt 3b.

- [ ] **Step 1: Gesamt-Review parallel starten** (sobald B1–B3 gemerged sind, unabhängig von Aufgabe 18–19), drei Reviewer auf dem höchsten Modell, Basis = Commit vor dem Merge von `feat/files-contracts`, Ende = aktueller `main`:
  1. **Security** – Reihenfolge: (a) `os.Root`-Eingrenzung und Symlink-Regeln in allen Operationen (auch Rename-Ziel, Upload in Symlink-Ordner, Ordner-Zip, Temp-Dateien), kein `os.Open`/`os.ReadFile`/`os.Create` außerhalb des Roots in `internal/files`; (b) Entpacken: Zip-Slip, absolute Pfade, `\`, Symlink-Einträge, gefälschte Größen, Eintragszahl, doppelte Namen, verborgene Namen, Aufräumen; (c) Peer-UID, Socket-Modus/Gruppe, Prozessrechte (Unit, CI-Start); (d) RCON-Geheimnis: `.rcon-cli.*` in Liste/Lesen/Download/Ordner-Zip/Rename/Upload-Überschreiben, Schwärzen und Zurückschreiben von `rcon.password`; (e) panel: Rechte aller zehn Routen, CSRF, Streaming ohne Puffern, Upload-Limit, Fristen, `Content-Disposition`-Dateiname (Header-Injection), Audit; (f) Frontend: kein `dangerouslySetInnerHTML`, Dateinamen nur als Text, Downloads Same-Origin.
  2. **Backend** – Querschnitt Dienst ↔ `filesapi`-Client ↔ panel ↔ OpenAPI: Fehlercodes durch alle Schichten, Grenzen und Fristen (Dienst 60 s, panel 60 s, Client-Header-Timeout 10 s), Shutdown beider Prozesse während laufender Uploads/Jobs, Entpacken ↔ Power-Sperre ↔ Abgleich, Plattenplatz-Prüfung, Dev-Modus, Spec-Treue §3–§5.
  3. **Frontend** – gegen das echte Backend (nicht nur den Mock): Upload-Fortschritt und Abbruch, Konflikt-Dialog, Editor-Sprachen und Bundle-Größe (CodeMirror lazy), Entpack-Fortschritt und Power-Sperre, Rechte-Hinweis, i18n vollständig, Mobil, Barrierefreiheit.
  Jeder Reviewer bekommt die Liste der aufgeschobenen Minors aus den Ledgern der Phasen; Befunde mit `superpowers:receiving-code-review` bewerten und priorisieren.

- [ ] **Step 2: Befunde beheben** – berechtigte Befunde (Critical/Important und die als „jetzt beheben“ eingestuften Minors) gehen gesammelt an einen neuen Teammate „review-fixes“ in eigenem Worktree auf `fix/files-review-findings` (von `origin/main`), Muster wie TP2/3a: pro Bereich `test(<bereich>): cover whole-feature review findings of TP3b (red)`, dann `fix(<bereich>): address whole-feature review findings of TP3b`; Bereiche `files`, `serverfiles`, `web`. Der Teammate ändert keine Doku, sondern liefert eine Liste nötiger Spec-Änderungen. Danach ein Re-Review nur der Fixes; offene Punkte entscheidet der Lead mit Begründung im Ledger. Nicht berechtigte Befunde mit Begründung als PR-Kommentar festhalten.

- [ ] **Step 3: Spec angleichen** – die Liste aus Step 2 übernimmt der Teammate „ci-docs“ auf `feat/files-ci-docs` (nach `git merge origin/main`, damit die CI-Abnahme gegen den Gesamtstand läuft) mit eigenem Commit `docs(spec): bring the TP3b spec in line with the review fixes`; die Status-Zeile bleibt „Entwurf“. Danach Re-Review von Aufgaben 18–20-Doku gegen den Code.

- [ ] **Step 4: Übergabe** – PRs `fix/files-review-findings` und `feat/files-ci-docs` mit grüner CI auf „ready“ setzen (`gh pr ready <pr>`); dem Nutzer melden: PR-Links, Zusammenfassung der Befunde und Entscheidungen, Reihenfolge (erst Fixes, dann CI/Doku). Nicht selbst mergen.

- [ ] **Step 5: Abnahme begleiten** – nach dem Merge durch den Nutzer:

```bash
git -C /Users/jannes/Workspace/mc-panel switch main && git -C /Users/jannes/Workspace/mc-panel pull --ff-only
make deploy-testvm
ssh claude@192.168.1.117 'systemctl is-active mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel caddy; sudo stat -c "%U:%G %a" /run/mc-panel-files/files.sock; pid=$(systemctl show -p MainPID --value mcpanel-files); sudo grep -E "^(Uid|Gid|Groups|CapEff|NoNewPrivs):" /proc/$pid/status; id -u mcpanel-mc; sudo -u mcpanel curl -s --unix-socket /run/mc-panel/agent.sock http://agent/v1/health; sudo -u mcpanel curl -s --unix-socket /run/mc-panel-files/files.sock http://files/v1/health; sudo curl -s --max-time 3 --unix-socket /run/mc-panel-files/files.sock http://files/v1/health'
```

(SSH nur mit abgeschalteter Sandbox.) Expected: fünfmal `active`; Socket `mcpanel-mc:mcpanel 660`; `Uid` viermal die UID von `mcpanel-mc`, `Groups` ohne die GID von `mcpanel`, `CapEff: 0000000000000000`, `NoNewPrivs: 1`; alle drei Health-Antworten `ok` (auch der Aufruf als root). Dann dem Nutzer `docs/dev/acceptance-tp3b.md` übergeben. Befunde: Issue, Fix auf eigenem `fix/…`-Branch mit PR (Teammate), erneut deployen.

- [ ] **Step 6: Abschluss nach erfolgreicher Abnahme** – auf neuem Branch `docs/tp3b-done` von `origin/main`:

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel switch -c docs/tp3b-done origin/main
gh pr list --state merged --search "files in:title" --json number,title,mergedAt
```

- Spec `2026-10-11-mc-panel-files-design.md`: Status-Zeile → `- **Status:** umgesetzt (PRs #<erste>–#<letzte>, <Merge-Datum der letzten, JJJJ-MM-TT>)` (Nummern aus der Liste, inkl. Spec/Plan-PR und Fix-PRs).
- Roadmap in `2026-10-08-mc-panel-foundation-design.md`: Zeile 3b „Live-Betrieb: Dateien (Spec …)“ → „Live-Betrieb: Dateien (umgesetzt, Spec …)“.
- `README.md`, „Stand“: 3b → `fertig`; `CLAUDE.md`: „(1 foundation, 2 instances and 3a console are done)“ → „(1 foundation, 2 instances, 3a console and 3b files are done)“.
- Pläne löschen und committen:

```bash
cd /Users/jannes/Workspace/mc-panel
git rm docs/superpowers/plans/2026-10-11-files-0*.md
git add docs/superpowers/specs README.md CLAUDE.md
make check
git commit -m "docs: drop the completed sub-project 3b plans, mark the spec done

All plan tasks are merged and the manual acceptance on the test VM passed; git
history keeps the plans. The spec stays as the reference and says so in its
status line.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin docs/tp3b-done
gh pr create --title "docs: close sub-project 3b" --body "$(cat <<'EOF'
Drops the completed TP3b plans (git history keeps them) and marks the spec, the roadmap row, README and CLAUDE.md done. Manual acceptance on the test VM passed.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
EOF
)"
gh pr checks <pr> --watch --interval 30
```

- Worktrees der Teammates entfernen (`git worktree remove ../mc-panel-wt/<branch>`), lokale Branches löschen; Remote-Branches nur nach Freigabe des Nutzers (GitHub löscht gemergte Branches ggf. selbst). SDD-Workspaces unter `.superpowers/sdd/2026-10-11-files-*` löschen, nachdem die „Rulings“ dem Nutzer berichtet wurden.

## Self-Review (dieser Datei)

- Spec §3.1 (Prozess, Unit-Härtung, Socket-Aktivierung mit `mcpanel-files.socket`, `socket`/`socket_mode` ohne Aktivierung, `files.toml`, `files_socket`, Dev-Modus) → Aufgabe 18 Steps 2–3 (Test-VM mit Aktivierung), 6 (CI ohne Aktivierung, `socket_mode = "0666"`, Peer-UID-Beweis mit `nobody` (root zugelassen)), Doku Aufgabe 19 Step 2(e).
- Spec §4.2 (synchrone Vorprüfung): Aufgabe 18 Step 6 (9) erwartet für jedes bösartige Archiv exakt `400 invalid` bzw. `413 too_large` ohne neuen Job und ohne Ziel; `409 exists` beim nicht leeren Ziel.
- Spec §3.3 (`instances/` `root:mcpanel-mc 0750`, `deleted/` 0700, Temp-Dateien) → Aufgabe 18 Step 6 (7), (8) und `no_tmp_left`.
- Spec §7 (Mac, `make dev`, Test-VM, Release, Installer-Vorgaben, CI) → Aufgabe 18 Steps 2–5, Aufgabe 19 Step 2(e).
- Spec §8 Zeile „CI-Abnahme“: Dienst als `mcpanel-mc` → (7)/(8) Prozessprüfung; Plugin-Jar hochladen → (8); `server.properties` geschwärzt → (8); Config ändern → (8); Server stoppen, Welt-Zip hochladen und entpacken → (9); Zip-Slip abgelehnt → (9); Symlink auf `/etc/shadow` liefert keinen Inhalt → (8); Schreibversuch nach `<instances_dir>/router` scheitert → (8) (Prozessrechte und Symlink-Ordner); zusätzlich Peer-UID (`nobody` abgewiesen, root zugelassen), Socket-Modus, 409/413/415, Ordner-Zip ohne Geheimnisse, bösartige Archive (absolut, Symlink-Eintrag, zu viele Einträge).
- Spec §9: Kriterium 1 → CI-Läufe in Aufgaben 18–20; Dienst-Seite von 2–4 → Aufgabe 18 Step 6; 2–7 manuell → `docs/dev/acceptance-tp3b.md`.
- Nichts startet Container oder braucht root auf dem Mac; Host-verändernde Prüfungen nur in der CI und auf der Test-VM.
- Mergen, Tags und Löschen von Remote-Branches nur durch bzw. nach Freigabe des Nutzers.
