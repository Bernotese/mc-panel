# Teilprojekt 3c – CI-Abnahme, Doku, Gesamt-Review und Abschluss (Aufgaben 17–18) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plugins und Mods auf einem CI-Runner gegen die echten Upstreams (Modrinth, Hangar) und einen echten Paper-Server abnehmen – über die panel-API mit echter Session (Passwort, TOTP, Sudo, CSRF), den unprivilegierten Dateidienst und RCON –, die Doku nachziehen (Abnahme-Checkliste, Entwickler- und Betriebsdoku, `CLAUDE.md`, `README.md`) und Teilprojekt 3c abschließen (Gesamt-Review, Abnahme auf der Test-VM, Pläne löschen).

**Architecture:** Aufgabe 17 berührt nur `scripts/ci-instances.sh` und `.github/workflows/ci.yml`. Das Skript bekommt ein drittes Argument (panel-Binary) und hängt nach dem bestehenden agent-/Dateidienst-Teil einen Abschnitt „plugins“ an: Es startet das panel im Produktionsmodus als Benutzer `mcpanel` (TCP `127.0.0.1:8080`, `public_url = https://mcpanel.test`, `agent_socket` und `files_socket` auf die bereits laufenden Dienste), legt per CLI einen Admin an, meldet sich per `curl` an (Pflicht-TOTP einrichten, Sudo per TOTP, Cookie `__Host-sid` und `X-CSRF-Token` von Hand, weil `curl` `Secure`-Cookies über `http` nicht zurückschickt), legt über die panel-API einen zweiten Paper-Server an und prüft daran Suche, Auflösen, Installieren (Job), Inventar, Dateidienst (`/v1/jars`, Upload mit Prüfsumme), Icon-Proxy, Neustart mit RCON `plugins`, Deaktivieren, Entfernen, Updatecheck und Audit. Der bestehende Server `9001` ist zu diesem Zeitpunkt schon entfernt; das panel sieht ihn nie. Aufgabe 18 schreibt die Doku; den Gesamt-Review und den Abschluss koordiniert der Lead.

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, immer `python3 -I`), `curl` (TCP zum panel, `--unix-socket` zu agent und Dateidienst), `setpriv` (util-linux), GitHub Actions (`ubuntu-24.04`).

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` (§4, §5, §6, §8–§10) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-12-plugins-00-overview.md` (V1, V3; Abschnitte „Ausführung“ und „Global Constraints“ gelten vollständig)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | C, Teammate „ci-docs“, `feat/plugins-ci-docs` (Aufgabe 17 und die Doku-Schritte von 18); Gesamt-Review, Fix-Runden und Abschluss (Rest von 18) koordiniert der Lead, Abschluss auf `docs/tp3c-done` |
| Startet, wenn | Phasen A, B1, B2, B3 auf `main` gemerged |
| Dateieigentum | nur `scripts/**`, `Makefile`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md` |
| Wo getestet wird | lokal nur `make check`, `bash -n`, `shellcheck` (falls installiert) und `actionlint` per `go run`; alles mit root, Docker, Minecraft oder Upstream-Downloads nur in der CI bzw. auf der Test-VM |
| Test-Server (plugins) | über die panel-API: Name `ciplugins`, Paper `1.21.11`, `java21`, 2048 MiB, Basisdomain `mc.localhost`; ID vergibt das panel (frische DB → `1`), Daten in `$work/mc/instances/<id>` |
| Test-Plugins | **Modrinth:** Chunky (`fALzjamp`, Slug `chunky`, Loader `bukkit/paper/spigot/folia`, für 1.21.11 Version `1.4.40` `release`, `Chunky-Bukkit-1.4.40.jar`, 296 244 Bytes, keine Abhängigkeiten, lädt zur Laufzeit nichts nach). **Hangar:** ViaBackwards (ID `12`, Owner `ViaVersion`, für 1.21.11 neueste `Release` `5.12.1`, 1 436 079 Bytes) mit Pflicht-Abhängigkeit ViaVersion (ID `31`, `5.12.1`, 6 503 216 Bytes; `pluginDependencies.PAPER[]` mit `required: true`, `projectId: 31`, ohne `externalUrl`), beide mit `fileInfo` auf `hangarcdn.papermc.io`; die optionale Abhängigkeit ViaRewind kommt nicht in den Plan. Stand der Live-Abfrage 2026-10-12; das Skript liest Versionen, Dateinamen und Größen immer aus den Antworten, nie fest verdrahtet |
| Admin im Skript | `ci-admin`, Passwort `ci plugins admin password 1`; Header bei jedem Aufruf `Origin: https://mcpanel.test`, `Sec-Fetch-Site: same-origin`, `Cookie: __Host-sid=<token>`, `X-CSRF-Token: <csrf_token aus Me>`; neue Cookies aus `Set-Cookie` übernehmen (Login, TOTP-Aktivierung, Sudo rotieren das Token) |
| Upstream-Fehler | synchrone Aufrufe mit `502` und Install-Jobs, die nur mit `download_failed`/`source_unavailable` scheitern: genau eine Wiederholung nach 20 s, dann Abbruch mit `FAIL: upstream still unavailable after one retry …` bzw. `FAIL: install … job failed …` und der Antwort; nie still überspringen |
| Dienst-Aufrufe | wie in 3b immer als panel-Benutzer über `fcurl`/`fpost`; JSON mit Werten aus Upstream-Antworten immer per `json.dumps` (`jobj`) |
| Workflow | Job `instances`: zusätzlich `go build -o bin/panel ./cmd/panel` (ohne Web-Build, die API reicht), Aufruf mit drittem Argument, `timeout-minutes` 30 → 40 (zweiter Server-Start, zwei Neustarts, Upstream-Wiederholungen; Lauf bisher ~3 min, danach erwartet ~9 min) |

---

### Task 17: CI-Abnahme gegen echte Upstreams

**Files:**
- Modify: `scripts/ci-instances.sh` (drittes Argument, panel starten, Helfer, Abschnitte „plugins …“)
- Modify: `.github/workflows/ci.yml` (Job `instances`)

**Interfaces:**
- Consumes: panel-API aus V3 (alle elf Plugin-Routen; `202 PluginJob` direkt als Body, `GET …/plugins/jobs` → `{job}`), Auth-Routen (`/auth/login` → `Me` mit `stage` und `csrf_token`; `/me/totp/setup` → `{secret}`; `/me/totp/enable` mit `{code}`; `/auth/sudo/totp` mit `{code}`; `/auth/me`), `PUT /servers/settings`, `POST /servers` (Sudo), `POST /servers/{id}/power`, `POST /servers/{id}/console` (`{output}`), `GET /audit?action=<präfix>&limit=` (Präfix-Suche, neueste zuerst); Dienst-API aus V1 (`/v1/jars`, `PUT /v1/upload?…&sha256=|sha512=`, Codes `hash_mismatch`, `invalid`); CLI `panel admin create -config … -username …` (Passwort von stdin) und `panel audit verify -config …`; agent: `started_at` in Unix-ms, Log-`since` in ns.
- Produces: `scripts/ci-instances.sh AGENT_BINARY FILES_BINARY PANEL_BINARY` endet mit `PASS: instance acceptance (agent, files and plugins path)`.

- [ ] **Step 1: Worktree anlegen**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel worktree add ../mc-panel-wt/feat-plugins-ci-docs -b feat/plugins-ci-docs origin/main
cd /Users/jannes/Workspace/mc-panel-wt/feat-plugins-ci-docs
(cd web && npm ci)
```

Vorab prüfen, dass B2 den Vertrag so umgesetzt hat, wie das Skript ihn nutzt (sonst den Lead fragen, nicht das Skript an Abweichungen anpassen):

```bash
grep -n "plugins/install\|plugins/jobs\|plugins/icon" api/openapi.yaml
grep -n "PluginJob:\|PluginJobResponse:\|InstalledJar:" api/openapi.yaml
grep -rn "\"server:\"" internal/plugins/*.go | head -3
```

Expected: `installPlugin` antwortet `202` mit Schema `PluginJob`, `getPluginJob` mit `PluginJobResponse`; Audit-Ziel `server:<id>`.

- [ ] **Step 2: `scripts/ci-instances.sh` – Kopf, Argumente, Variablen, Aufräumen** (Reihenfolge wie im Skript)

(1) Kopfkommentar (Zeilen 2–13) ersetzen durch:

```bash
# Instance acceptance for a Linux CI runner with Docker (sub-project 2, spec §12 criteria 2,
# 6, 7, 8 on the agent path; sub-project 3a, spec §10 criteria 2 and 4 on the agent path;
# sub-project 3b, spec §9 criteria 2-4 on the files-service path; sub-project 3c, spec §10
# criteria 2 and 5 on the panel/files-service path): starts the real agent as root and the
# files service as mcpanel-mc, creates a Paper server through the agent socket, waits until it
# answers a status ping routed by hostname through mc-router, checks the container hardening,
# the console endpoints and the files service (credentials, peer UID, upload, redaction,
# editing, symlinks, directory zip), crashes it, waits for Docker's restart, checks the console
# on the stopped server, planted player files and archive extraction (incl. zip slip), and
# removes it. Then it starts the panel in production mode as mcpanel, signs in (password,
# mandatory TOTP, sudo), creates a second Paper server through the panel API and installs
# plugins from the real Modrinth and Hangar (Chunky; ViaBackwards with its required dependency
# ViaVersion), checks inventory, checksums, icon proxy, loading after a restart, disabling,
# removal and the audit log.
#
# Usage (as root): scripts/ci-instances.sh AGENT_BINARY FILES_BINARY PANEL_BINARY
# Changes the host (users, Docker containers/network, ports 25565 and 8080) and needs internet
# (Mojang, PaperMC, Modrinth, Hangar): disposable CI runners only.
```

(2) Argumentprüfung ersetzen:

```bash
if [ "$#" -ne 3 ]; then
  echo "usage: scripts/ci-instances.sh AGENT_BINARY FILES_BINARY PANEL_BINARY" >&2
  exit 2
fi
```

(3) Nach `files_pid=""` einfügen:

```bash
# Panel (plugins part): production mode on TCP; the browser headers are sent by hand.
pbase="http://127.0.0.1:8080"
porigin="https://mcpanel.test"
po="$work/pout"
panel_pid=""
psid=""       # value of the __Host-sid cookie (curl does not return Secure cookies over http)
pcsrf=""      # csrf_token of the session
pcode=""      # HTTP status of the last papi call
psrv=""       # server id of the plugins test server (assigned by the panel)
pdata=""      # its data directory
pjob=""       # id of the plugin job being waited for
pjob_state="" # its last seen state
p_before=""   # started_at (Unix ms) before a restart
p_started=""  # started_at (Unix ms) after the last restart
```

(4) In `cleanup()` als ersten Block einfügen (das panel zuerst beenden, damit sein Abgleich keine Container neu anlegt, während das Skript sie entfernt):

```bash
  if [ -n "$panel_pid" ]; then
    kill "$panel_pid" 2>/dev/null || true
    wait "$panel_pid" 2>/dev/null || true
  fi
```

(5) In `fail()` nach `cat "$work/files.log" >&2 || true` einfügen:

```bash
  echo "--- panel log" >&2
  cat "$work/panel.log" >&2 || true
  if [ -n "$psrv" ]; then
    docker logs --tail 80 "mcpanel-mc-$psrv" >&2 2>/dev/null || true
  fi
```

- [ ] **Step 3: `scripts/ci-instances.sh` – Helfer** – nach der Funktion `extract_rejected()` (vor `check_players`) einfügen:

```bash
# no_tmp_in <dir>: no temp file of the files service below <dir> (spec 3b §3.3).
no_tmp_in() { [ -z "$(find "$1" -name '.mcpanel-tmp-*' -print -quit)" ]; }

# papi <method> <path below /api> <outfile> [json body]: calls the panel as the CI admin with the
# headers a browser sends (Origin, Sec-Fetch-Site, session cookie, CSRF token). Sets pcode to the
# HTTP status and takes over a rotated session cookie (login, TOTP enable, sudo). Never call it
# in a subshell: callers read pcode afterwards.
papi() {
  local m=$1 p=$2 out=$3 c
  local args=(-sS --max-time 60 -o "$out" -D "$out.headers" -w '%{http_code}' -X "$m"
    -H "Origin: $porigin" -H 'Sec-Fetch-Site: same-origin')
  [ -n "$psid" ] && args+=(-H "Cookie: __Host-sid=$psid")
  [ -n "$pcsrf" ] && args+=(-H "X-CSRF-Token: $pcsrf")
  [ "$#" -ge 4 ] && args+=(-H 'Content-Type: application/json' --data-binary "$4")
  pcode="$(curl "${args[@]}" "$pbase/api$p")" || fail "panel $m $p: curl exit $?"
  if grep -qi '^set-cookie: __Host-sid=' "$out.headers"; then
    c="$(tr -d '\r' <"$out.headers" | sed -n 's/^[Ss]et-[Cc]ookie: __Host-sid=\([^;]*\).*$/\1/p' | tail -n 1)"
    psid="$c"
  fi
}

# papi_up <papi args…>: papi for calls that reach Modrinth or Hangar. A 502 (upstream down or
# rate-limited, spec §4) is retried once after 20 s; a second 502 fails the run with the panel's
# message. No silent skip: this acceptance needs the real upstreams.
papi_up() {
  papi "$@"
  if [ "$pcode" = 502 ]; then
    echo "WARN: $1 $2: HTTP 502 $(cat "$3"), retrying once in 20 s" >&2
    sleep 20
    papi "$@"
    [ "$pcode" != 502 ] || fail "upstream still unavailable after one retry: $1 $2: $(cat "$3")"
  fi
}

# is_perror <file> <got status> <want status> <want code>: panel error {"error": "<code>", …}.
is_perror() { [ "$2" = "$3" ] && grep -q "\"error\":\"$4\"" "$1"; }

# jget <file> <python expression on d (JSON) and a (extra args)> [args…]: prints the value.
jget() {
  python3 -I -c '
import json, sys
print(eval(sys.argv[2], {"d": json.load(open(sys.argv[1])), "a": sys.argv[3:]}))' "$@"
}

# jcheck <file> <what> <python expression on d and a> [args…]: fails the run if it is false.
# d and a live in the globals, so generator expressions can use them.
jcheck() {
  python3 -I -c '
import json, sys
d = json.load(open(sys.argv[1]))
if not eval("(" + sys.argv[3] + "\n)", {"d": d, "a": sys.argv[4:]}):
    sys.exit("%s: %s\n%s" % (sys.argv[2], sys.argv[3].strip(), json.dumps(d)[:4000]))' "$@" || fail "$2"
}

# jobj <key> <value> […]: a JSON object of string values, encoded by Python (never by hand).
jobj() { python3 -I -c 'import json, sys; a = sys.argv[1:]; print(json.dumps(dict(zip(a[::2], a[1::2]))))' "$@"; }

# totp <base32 secret> <step offset>: RFC 6238 code (SHA-1, 6 digits, 30 s) for now + offset steps.
totp() {
  python3 -I -c '
import base64, hashlib, hmac, struct, sys, time
s = sys.argv[1].rstrip("=").upper()
key = base64.b32decode(s + "=" * (-len(s) % 8))
mac = hmac.new(key, struct.pack(">Q", int(time.time()) // 30 + int(sys.argv[2])), hashlib.sha1).digest()
o = mac[-1] & 0x0F
print("%06d" % ((struct.unpack(">I", mac[o:o + 4])[0] & 0x7FFFFFFF) % 1000000))' "$1" "$2"
}

# inst_field <id> <python expression on instance dict i>: status_field for any instance.
inst_field() {
  agent http://agent/v1/instances | python3 -I -c '
import json, sys
for i in json.load(sys.stdin)["instances"]:
    if i["id"] == int(sys.argv[1]):
        print(eval(sys.argv[2], {"i": i}))
        break
else:
    print("absent")' "$1" "$2"
}

pwait_for() { # pwait_for <seconds> <description> <command...>: wait_for for the plugins server
  local deadline=$((SECONDS + $1)) what=$2
  shift 2
  until "$@"; do
    [ "$SECONDS" -lt "$deadline" ] || fail "timeout waiting for: $what (instance: $(inst_field "$psrv" i || true))"
    sleep 3
  done
}

p_running() { [ "$(inst_field "$psrv" 'i["state"]')" = running ]; }
# RCON readiness through the agent (root), so polling does not count against the panel's
# console rate limit (30/min) or fill the audit log.
p_rcon_ready() { [ "$(post_status /v1/instances/rcon "{\"id\": $psrv, \"command\": \"list\"}" "$po/rcon.json")" = 200 ]; }
p_restarted() {
  local a
  a="$(inst_field "$psrv" 'i["started_at"]')"
  [ "$a" != 0 ] && [ "$a" != "$p_before" ] && p_running
}

# p_restart <why>: restarts the plugins server through the panel (server.power) and waits until
# RCON answers again; p_started is the new start time (Unix ms) for the log check.
p_restart() {
  p_before="$(inst_field "$psrv" 'i["started_at"]')"
  papi POST "/servers/$psrv/power" "$po/power.json" '{"action": "restart"}'
  [ "$pcode" = 202 ] || fail "restart ($1): HTTP $pcode $(cat "$po/power.json")"
  pwait_for 240 "restart ($1)" p_restarted
  pwait_for 240 "rcon after restart ($1)" p_rcon_ready
  p_started="$(inst_field "$psrv" 'i["started_at"]')"
}

# check_plugins <listed names, comma-separated> <absent names, comma-separated>: runs "plugins"
# through the panel console (RCON) and checks the answer: listed names must appear and must not
# be marked disabled (§c, red); absent names must not appear.
check_plugins() {
  papi POST "/servers/$psrv/console" "$po/plugins.json" '{"command": "plugins"}'
  [ "$pcode" = 200 ] || fail "console plugins: HTTP $pcode $(cat "$po/plugins.json")"
  python3 -I -c '
import json, re, sys
raw = json.load(open(sys.argv[1]))["output"]
plain = re.sub("§.", "", raw)
def listed(n):
    return re.search(r"(?<![A-Za-z0-9_])" + re.escape(n) + r"(?![A-Za-z0-9_])", plain) is not None
want = [n for n in sys.argv[2].split(",") if n]
gone = [n for n in sys.argv[3].split(",") if n]
bad = [n for n in want if not listed(n) or "§c" + n in raw] + [n for n in gone if listed(n)]
if "Plugins" not in plain or bad:
    sys.exit("plugins list: unexpected %r in %r" % (bad, plain))
print("plugins: " + " | ".join(l.strip() for l in plain.splitlines() if l.strip()))' "$po/plugins.json" "$1" "$2" ||
    fail "plugins list (want $1, without ${2:-nothing})"
}

# check_enabled_in_log <names…>: the container log since the last start (p_started) has
# "Enabling <name> v" for every name and no "Error occurred while enabling".
check_enabled_in_log() {
  logs "{\"id\": $psrv, \"tail\": 1000, \"since\": $((p_started * 1000000))}" "$po/start.ndjson" 5
  python3 -I -c '
import json, sys
lines = [json.loads(x)["line"] for x in open(sys.argv[1]) if x.strip()]
missing = [n for n in sys.argv[2:] if not any("Enabling " + n + " v" in l for l in lines)]
errors = [l for l in lines if "Error occurred while enabling" in l]
if missing or errors:
    sys.exit("log since start: no \"Enabling\" for %r, errors %r" % (missing, errors[:3]))' "$po/start.ndjson" "$@" ||
    fail "plugin start in the log"
}

# p_job_finished: polls the plugin job of the plugins server; true once the job pjob is done or
# failed (pjob_state; "none"/"other" keep waiting).
p_job_finished() {
  papi GET "/servers/$psrv/plugins/jobs" "$po/job.json"
  [ "$pcode" = 200 ] || fail "plugin jobs: HTTP $pcode $(cat "$po/job.json")"
  pjob_state="$(jget "$po/job.json" '"none" if d["job"] is None else d["job"]["state"] if d["job"]["id"] == a[0] else "other"' "$pjob")"
  [ "$pjob_state" = "done" ] || [ "$pjob_state" = failed ]
}

# install_plugin <source> <project id> <label>: starts an install job through the panel and waits
# for it (5 min). A job that failed only with upstream step errors (download_failed,
# source_unavailable) is retried once (finished steps stay installed and count as "already");
# anything else fails the run with the job as JSON.
install_plugin() {
  local body attempt
  body="$(jobj source "$1" project "$2")"
  for attempt in 1 2; do
    papi_up POST "/servers/$psrv/plugins/install" "$po/install.json" "$body"
    [ "$pcode" = 202 ] || fail "install $3: HTTP $pcode $(cat "$po/install.json")"
    pjob="$(jget "$po/install.json" 'd["id"]')" || fail "install $3: no job id in $(cat "$po/install.json")"
    pwait_for 300 "plugin job ($3)" p_job_finished
    if [ "$pjob_state" = "done" ]; then
      jcheck "$po/job.json" "install $3: job steps" '
        d["job"]["kind"] == "install" and d["job"]["steps"]
        and all(s["state"] == "done" and s["error_code"] is None for s in d["job"]["steps"])
        and d["job"]["finished_at"] >= d["job"]["started_at"] > 0'
      return 0
    fi
    if [ "$attempt" = 1 ] && [ "$(jget "$po/job.json" 'all(s["error_code"] in ("download_failed", "source_unavailable") for s in d["job"]["steps"] if s["state"] == "failed")')" = True ]; then
      echo "WARN: install $3: upstream step error, retrying once in 20 s: $(cat "$po/job.json")" >&2
      sleep 20
      continue
    fi
    fail "install $3: job failed after $attempt attempt(s): $(cat "$po/job.json")"
  done
}

# plan_field <resolve file> <slug, lowercase> <field>: one field of a plan entry.
plan_field() { jget "$1" 'next(e[a[1]] for e in d["install"] if e["slug"].lower() == a[0])' "$2" "$3"; }

# fputsum <file> <query after path> <outfile>: uploads <file> to ci-sum.bin of the plugins server
# through the files service, prints the HTTP status.
fputsum() {
  fcurl -o "$3" -w '%{http_code}' -H 'Content-Type: application/octet-stream' -T "$1" \
    "http://files/v1/upload?id=$psrv&path=ci-sum.bin&$2"
}
```

Hinweise: `papi` bekommt den Body als String; ein Body, der mit `@` beginnt, würde `curl` als Datei lesen – kein Aufruf im Skript erzeugt so etwas (`jobj` liefert immer `{…}`). `p_job_finished` und `p_restarted` laufen über `until` im aktuellen Shell-Prozess, Cookie-Rotation geht dadurch nie verloren.

- [ ] **Step 4: `scripts/ci-instances.sh` – Abschnitt „plugins“** – vor der letzten Zeile `echo "PASS: instance acceptance (agent and files path)"` einfügen:

```bash
echo "== plugins: panel (production mode, user mcpanel) on the agent and the files service"
install -m 0755 "$3" "$work/bin/panel"
install -d -m 0700 -o mcpanel -g mcpanel "$work/pdata"
install -d -m 0755 "$po"
cat >"$work/panel.toml" <<EOF
listen = "127.0.0.1:8080"
public_url = "$porigin"
data_dir = "$work/pdata"
agent_socket = "$sock"
files_socket = "$fsock"
geoip_path = "$work/pdata/none.mmdb"
EOF
chmod 0644 "$work/panel.toml"
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" serve -config "$work/panel.toml" >"$work/panel.log" 2>&1 &
panel_pid=$!
for _ in $(seq 1 50); do curl -fsS "$pbase/api/health" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "$pbase/api/health" >/dev/null || fail "panel health"
padmin_pw='ci plugins admin password 1'
printf '%s\n' "$padmin_pw" | setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" admin create -config "$work/panel.toml" -username ci-admin >"$work/admin.log" 2>&1 ||
  fail "panel admin create: $(cat "$work/admin.log")"
echo "OK: panel up as mcpanel, admin ci-admin created"

echo "== plugins: sign in (password, mandatory TOTP setup, sudo by TOTP)"
papi POST /auth/login "$po/login.json" "$(jobj username ci-admin password "$padmin_pw")"
[ "$pcode" = 200 ] || fail "login: HTTP $pcode $(cat "$po/login.json")"
jcheck "$po/login.json" "an admin without 2FA must land in stage setup" 'd["stage"] == "setup"'
pcsrf="$(jget "$po/login.json" 'd["csrf_token"]')"
papi POST /me/totp/setup "$po/totp.json"
[ "$pcode" = 200 ] || fail "totp setup: HTTP $pcode $(cat "$po/totp.json")"
psecret="$(jget "$po/totp.json" 'd["secret"]')"
papi POST /me/totp/enable "$po/enable.json" "{\"code\": \"$(totp "$psecret" 0)\"}"
[ "$pcode" = 200 ] || fail "totp enable: HTTP $pcode $(cat "$po/enable.json")"
# The enable code consumed the current step (replay protection). The panel accepts one step of
# skew, so the code of the next step is valid right away.
papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" 1)\"}"
[ "$pcode" = 200 ] || fail "sudo: HTTP $pcode $(cat "$po/sudo.json")"
papi GET /auth/me "$po/me.json"
[ "$pcode" = 200 ] || fail "me: HTTP $pcode $(cat "$po/me.json")"
jcheck "$po/me.json" "session after TOTP setup and sudo" 'd["stage"] == "full" and d["totp_enabled"] and d["sudo_until"] > 0'
pcsrf="$(jget "$po/me.json" 'd["csrf_token"]')"
echo "OK: signed in as ci-admin with TOTP, sudo active"

echo "== plugins: Paper server created and started through the panel"
papi PUT /servers/settings "$po/settings.json" '{"base_domain": "mc.localhost"}'
[ "$pcode" = 200 ] || fail "base domain: HTTP $pcode $(cat "$po/settings.json")"
papi POST /servers "$po/server.json" '{"name": "ciplugins", "type": "paper", "version": "1.21.11", "java": "java21", "memory_mib": 2048, "eula_accepted": true}'
[ "$pcode" = 201 ] || fail "create server: HTTP $pcode $(cat "$po/server.json")"
psrv="$(jget "$po/server.json" 'd["id"]')"
pdata="$work/mc/instances/$psrv"
papi POST "/servers/$psrv/power" "$po/power.json" '{"action": "start"}'
[ "$pcode" = 202 ] || fail "start: HTTP $pcode $(cat "$po/power.json")"
pwait_for 900 "plugins server running (reconcile, image, Paper download)" p_running
pwait_for 300 "rcon on the plugins server" p_rcon_ready
echo "OK: server $psrv (ciplugins, Paper 1.21.11) running"

echo "== plugins: empty inventory; CSRF and foreign icon hosts refused"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "empty inventory of a fresh Paper server" '
  d["kind"] == "plugin" and d["dir"] == "plugins" and d["game_version"] == "1.21.11"
  and sorted(d["sources"]) == ["hangar", "modrinth"] and d["jars"] == [] and not d["truncated"]'
code="$(curl -sS --max-time 10 -o "$po/csrf.json" -w '%{http_code}' -X POST -H "Origin: $porigin" \
  -H 'Sec-Fetch-Site: same-origin' -H "Cookie: __Host-sid=$psid" -H 'Content-Type: application/json' \
  --data-binary '{"source": "modrinth", "project": "fALzjamp"}' "$pbase/api/servers/$psrv/plugins/install")"
is_perror "$po/csrf.json" "$code" 403 csrf_invalid || fail "install without CSRF token: HTTP $code $(cat "$po/csrf.json")"
papi GET "/servers/$psrv/plugins/jobs" "$po/job.json"
jcheck "$po/job.json" "a refused install must not start a job" 'd["job"] is None'
# Spec §6.8: only https://cdn.modrinth.com/ and https://hangarcdn.papermc.io/ (prefix incl. the
# slash), so look-alike hosts, userinfo tricks and plain http are refused before any request.
for u in https://example.com/icon.png http://cdn.modrinth.com/data/fALzjamp/icon.webp \
  https://cdn.modrinth.com.evil.example/icon.png https://hangarcdn.papermc.io@example.com/icon.png; do
  q="$(python3 -I -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$u")"
  papi GET "/servers/$psrv/plugins/icon?url=$q" "$po/icon.json"
  is_perror "$po/icon.json" "$pcode" 400 validation_failed || fail "icon proxy with $u: HTTP $pcode $(head -c 300 "$po/icon.json")"
done
echo "OK: inventory empty (plugin, plugins/, 1.21.11, modrinth+hangar); 403 csrf_invalid without job; icon proxy refuses foreign hosts and http (400)"

echo "== plugins: search Modrinth and Hangar (real upstreams)"
papi_up GET "/servers/$psrv/plugins/search?source=modrinth&q=chunky" "$po/search-m.json"
[ "$pcode" = 200 ] || fail "search modrinth: HTTP $pcode $(cat "$po/search-m.json")"
jcheck "$po/search-m.json" "Modrinth search finds Chunky" '
  any(h["slug"] == "chunky" and h["source"] == "modrinth" and not h["installed"]
      and h["page_url"] == "https://modrinth.com/plugin/chunky"
      and (h["icon_url"] or "").startswith("/api/servers/%s/plugins/icon?url=" % a[0]) for h in d["hits"])' "$psrv"
p_chunky="$(jget "$po/search-m.json" 'next(h["project_id"] for h in d["hits"] if h["slug"] == "chunky")')"
icon_chunky="$(jget "$po/search-m.json" 'next(h["icon_url"] for h in d["hits"] if h["slug"] == "chunky")')"
papi_up GET "/servers/$psrv/plugins/search?source=hangar&q=ViaBackwards" "$po/search-h.json"
[ "$pcode" = 200 ] || fail "search hangar: HTTP $pcode $(cat "$po/search-h.json")"
jcheck "$po/search-h.json" "Hangar search finds ViaBackwards" '
  any(h["slug"].lower() == "viabackwards" and h["source"] == "hangar" and not h["installed"]
      and h["page_url"] == "https://hangar.papermc.io/ViaVersion/ViaBackwards" for h in d["hits"])'
p_viaback="$(jget "$po/search-h.json" 'next(h["project_id"] for h in d["hits"] if h["slug"].lower() == "viabackwards")')"
echo "OK: search hits chunky ($p_chunky, Modrinth) and ViaBackwards ($p_viaback, Hangar) with page and proxied icon URLs"

echo "== plugins: icon proxy delivers the Chunky icon"
papi GET "${icon_chunky#/api}" "$po/icon.bin"
[ "$pcode" = 200 ] || fail "icon proxy (Chunky): HTTP $pcode $(head -c 300 "$po/icon.bin")"
tr -d '\r' <"$po/icon.bin.headers" >"$po/icon.h"
grep -qiE '^content-type: image/(png|jpeg|webp|gif)$' "$po/icon.h" || fail "icon content type: $(cat "$po/icon.h")"
grep -qix 'x-content-type-options: nosniff' "$po/icon.h" || fail "icon without nosniff: $(cat "$po/icon.h")"
grep -qix "content-security-policy: default-src 'none'" "$po/icon.h" || fail "icon CSP: $(cat "$po/icon.h")"
[ "$(stat -c %s "$po/icon.bin")" -le 262144 ] || fail "icon larger than 256 KiB"
echo "OK: icon via proxy ($(grep -i '^content-type:' "$po/icon.h"), nosniff, CSP default-src 'none')"

echo "== plugins: resolve (Chunky alone; ViaBackwards pulls in ViaVersion)"
papi_up GET "/servers/$psrv/plugins/resolve?source=modrinth&project=$p_chunky" "$po/resolve-m.json"
[ "$pcode" = 200 ] || fail "resolve chunky: HTTP $pcode $(cat "$po/resolve-m.json")"
jcheck "$po/resolve-m.json" "plan for Chunky" '
  [(e["slug"], e["source"], e["required_by"]) for e in d["install"]] == [("chunky", "modrinth", None)]
  and d["install"][0]["filename"].endswith(".jar") and d["install"][0]["size"] > 0
  and d["already"] == [] and d["external"] == [] and d["incompatible"] == []'
f_chunky="$(plan_field "$po/resolve-m.json" chunky filename)"
s_chunky="$(plan_field "$po/resolve-m.json" chunky size)"
papi_up GET "/servers/$psrv/plugins/resolve?source=hangar&project=$p_viaback" "$po/resolve-h.json"
[ "$pcode" = 200 ] || fail "resolve ViaBackwards: HTTP $pcode $(cat "$po/resolve-h.json")"
jcheck "$po/resolve-h.json" "plan for ViaBackwards with ViaVersion as required dependency" '
  sorted(e["slug"].lower() for e in d["install"]) == ["viabackwards", "viaversion"]
  and all(e["source"] == "hangar" and e["channel"] == "release" and e["size"] > 0 and e["filename"].endswith(".jar") for e in d["install"])
  and [e["required_by"] for e in d["install"] if e["slug"].lower() == "viabackwards"] == [None]
  and [e["required_by"] for e in d["install"] if e["slug"].lower() == "viaversion"] == [e["title"] for e in d["install"] if e["slug"].lower() == "viabackwards"]
  and d["already"] == [] and d["external"] == [] and d["incompatible"] == []'
f_viaver="$(plan_field "$po/resolve-h.json" viaversion filename)"
s_viaver="$(plan_field "$po/resolve-h.json" viaversion size)"
p_viaver="$(plan_field "$po/resolve-h.json" viaversion project_id)"
f_viaback="$(plan_field "$po/resolve-h.json" viabackwards filename)"
s_viaback="$(plan_field "$po/resolve-h.json" viabackwards size)"
echo "OK: plans: $f_chunky; $f_viaver (required by ViaBackwards) + $f_viaback; nothing external or incompatible"

echo "== plugins: install while the server runs (jobs, checksums checked by the files service)"
install_plugin modrinth "$p_chunky" "Chunky (Modrinth)"
install_plugin hangar "$p_viaback" "ViaBackwards (Hangar)"
# Spec §6.4: dependencies first; after an upstream retry only the missing step is left.
jcheck "$po/job.json" "ViaVersion installed before ViaBackwards" '[s["file"] for s in d["job"]["steps"]] in ([a[0], a[1]], [a[1]])' "$f_viaver" "$f_viaback"
for e in "$f_chunky:$s_chunky" "$f_viaver:$s_viaver" "$f_viaback:$s_viaback"; do
  n="${e%%:*}"
  s="${e##*:}"
  [ -f "$pdata/plugins/$n" ] && [ ! -L "$pdata/plugins/$n" ] || fail "jar missing on disk: plugins/$n"
  [ "$(stat -c '%U %s' "$pdata/plugins/$n")" = "mcpanel-mc $s" ] ||
    fail "plugins/$n: $(stat -c '%U %s' "$pdata/plugins/$n") (want mcpanel-mc $s: written by the files service, size from the upstream)"
done
no_tmp_in "$pdata" || fail "install left a .mcpanel-tmp-* file"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "inventory after install" '
  sorted(j["file"] for j in d["jars"]) == sorted(a[0:3])
  and all(j["enabled"] and j["installed_by_panel"] and j["project"] and j["version"] for j in d["jars"])
  and {j["file"]: (j["source"], j["project"]["id"]) for j in d["jars"]} == {a[0]: ("modrinth", a[3]), a[1]: ("hangar", a[4]), a[2]: ("hangar", a[5])}
  and d["restart_needed"]' "$f_chunky" "$f_viaver" "$f_viaback" "$p_chunky" "$p_viaver" "$p_viaback"
papi_up GET "/servers/$psrv/plugins/search?source=hangar&q=ViaBackwards" "$po/search-h.json"
[ "$pcode" = 200 ] || fail "search hangar: HTTP $pcode $(cat "$po/search-h.json")"
jcheck "$po/search-h.json" "search marks ViaBackwards as installed" 'any(h["project_id"] == a[0] and h["installed"] for h in d["hits"])' "$p_viaback"
echo "OK: three jars in plugins/ (owner mcpanel-mc, upstream sizes), inventory with source, project, installed_by_panel and restart_needed"

echo "== plugins: files service lists the jars with checksums and metadata"
code="$(fpost /v1/jars "{\"id\": $psrv, \"dir\": \"plugins\"}" "$fo/jars.json")"
[ "$code" = 200 ] || fail "files /v1/jars: HTTP $code $(cat "$fo/jars.json")"
python3 -I -c '
import hashlib, json, os, sys
d = json.load(open(sys.argv[1]))
got = {j["name"]: j for j in d["jars"]}
if d["truncated"]:
    sys.exit("jars truncated")
for name in sys.argv[3:]:
    j = got.get(name)
    if j is None:
        sys.exit("jars lacks %s: %r" % (name, sorted(got)))
    data = open(os.path.join(sys.argv[2], name), "rb").read()
    if (j["sha256"], j["sha512"], j["size"], j["enabled"]) != (hashlib.sha256(data).hexdigest(), hashlib.sha512(data).hexdigest(), len(data), True):
        sys.exit("jars entry %s: checksums, size or enabled differ from the disk" % name)
    m = j["meta"]
    if not m or m["kind"] != "plugin" or not m["name"] or m["name"].lower() not in name.lower():
        sys.exit("jars entry %s: meta %r" % (name, m))' "$fo/jars.json" "$pdata/plugins" "$f_chunky" "$f_viaver" "$f_viaback" ||
  fail "files /v1/jars content"
code="$(fpost /v1/jars "{\"id\": $psrv, \"dir\": \"mods\"}" "$fo/jars.json")"
[ "$code" = 200 ] || fail "files /v1/jars mods: HTTP $code $(cat "$fo/jars.json")"
jcheck "$fo/jars.json" "a missing mods/ is an empty list" 'd["jars"] == [] and not d["truncated"]'
code="$(fpost /v1/jars "{\"id\": $psrv, \"dir\": \"world\"}" "$fo/jars.json")"
is_error "$fo/jars.json" "$code" 400 invalid || fail "files /v1/jars with dir world: HTTP $code $(cat "$fo/jars.json")"
echo "OK: /v1/jars: sha256/sha512/size match the disk, metadata from plugin.yml; mods/ empty; dir world -> 400 invalid"

echo "== plugins: upload with checksum (files service)"
head -c 65536 /dev/urandom >"$work/ci-sum.bin"
head -c 1024 /dev/urandom >"$work/ci-sum.other"
chmod 0644 "$work/ci-sum.bin" "$work/ci-sum.other"
sum256="$(sha256sum "$work/ci-sum.bin" | cut -d' ' -f1)"
sum512="$(sha512sum "$work/ci-sum.bin" | cut -d' ' -f1)"
zero256="$(printf '%064d' 0)"
code="$(fputsum "$work/ci-sum.bin" "overwrite=0&sha256=$zero256" "$fo/sum.json")"
is_error "$fo/sum.json" "$code" 400 hash_mismatch || fail "upload with a wrong sha256: HTTP $code $(cat "$fo/sum.json")"
[ ! -e "$pdata/ci-sum.bin" ] || fail "upload with a wrong checksum left ci-sum.bin behind"
code="$(fputsum "$work/ci-sum.bin" "overwrite=0&sha256=$sum256&sha512=$sum512" "$fo/sum.json")"
is_error "$fo/sum.json" "$code" 400 invalid || fail "upload with sha256 and sha512: HTTP $code $(cat "$fo/sum.json")"
code="$(fputsum "$work/ci-sum.bin" "overwrite=0&sha512=$sum512" "$fo/sum.json")"
[ "$code" = 201 ] || fail "upload with the right sha512: HTTP $code $(cat "$fo/sum.json")"
jcheck "$fo/sum.json" "upload answer with both checksums" 'd["size"] == 65536 and d["sha256"] == a[0] and d["sha512"] == a[1]' "$sum256" "$sum512"
cmp -s "$work/ci-sum.bin" "$pdata/ci-sum.bin" || fail "uploaded ci-sum.bin differs"
code="$(fputsum "$work/ci-sum.other" "overwrite=1&sha256=$sum256" "$fo/sum.json")"
is_error "$fo/sum.json" "$code" 400 hash_mismatch || fail "overwrite with a wrong checksum: HTTP $code $(cat "$fo/sum.json")"
cmp -s "$work/ci-sum.bin" "$pdata/ci-sum.bin" || fail "a refused overwrite changed ci-sum.bin"
no_tmp_in "$pdata" || fail "checksum uploads left a .mcpanel-tmp-* file"
rm -f "$pdata/ci-sum.bin"
echo "OK: wrong checksum -> 400 hash_mismatch (no file, target unchanged), both parameters -> 400 invalid, right one -> 201 with sha256/sha512"

echo "== plugins: restart, all three plugins load"
p_restart "after install"
check_plugins "Chunky,ViaVersion,ViaBackwards" ""
check_enabled_in_log Chunky ViaVersion ViaBackwards
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "no restart needed after the restart" 'not d["restart_needed"]'
echo "OK: Chunky, ViaVersion and ViaBackwards enabled after the restart (RCON plugins, log); restart_needed false"

echo "== plugins: disable Chunky, restart, it is not loaded"
papi POST "/servers/$psrv/plugins/toggle" "$po/toggle.json" \
  "$(python3 -I -c 'import json, sys; print(json.dumps({"file": sys.argv[1], "enabled": False}))' "$f_chunky")"
[ "$pcode" = 204 ] || fail "disable Chunky: HTTP $pcode $(cat "$po/toggle.json")"
[ -f "$pdata/plugins/$f_chunky.disabled" ] && [ ! -e "$pdata/plugins/$f_chunky" ] || fail "disable not on disk: $(ls -a "$pdata/plugins")"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "Chunky disabled in the inventory, origin kept" '
  [(j["enabled"], j["source"], j["installed_by_panel"]) for j in d["jars"] if j["file"] == a[0] + ".disabled"] == [(False, "modrinth", True)]
  and d["restart_needed"]' "$f_chunky"
p_restart "after disabling Chunky"
check_plugins "ViaVersion,ViaBackwards" "Chunky"
echo "OK: $f_chunky.disabled is not loaded after the restart, ViaVersion and ViaBackwards still are"

echo "== plugins: enable and remove Chunky"
papi POST "/servers/$psrv/plugins/toggle" "$po/toggle.json" \
  "$(python3 -I -c 'import json, sys; print(json.dumps({"file": sys.argv[1], "enabled": True}))' "$f_chunky.disabled")"
[ "$pcode" = 204 ] || fail "enable Chunky: HTTP $pcode $(cat "$po/toggle.json")"
[ -f "$pdata/plugins/$f_chunky" ] && [ ! -e "$pdata/plugins/$f_chunky.disabled" ] || fail "enable not on disk: $(ls -a "$pdata/plugins")"
papi POST "/servers/$psrv/plugins/delete" "$po/delete.json" "$(jobj file "$f_chunky")"
[ "$pcode" = 204 ] || fail "delete Chunky: HTTP $pcode $(cat "$po/delete.json")"
[ ! -e "$pdata/plugins/$f_chunky" ] || fail "deleted jar still on disk"
papi GET "/servers/$psrv/plugins" "$po/inv.json"
[ "$pcode" = 200 ] || fail "inventory: HTTP $pcode $(cat "$po/inv.json")"
jcheck "$po/inv.json" "inventory after removal" 'sorted(j["file"] for j in d["jars"]) == sorted(a)' "$f_viaver" "$f_viaback"
echo "OK: toggle back to .jar, delete removes the jar and its inventory entry"

echo "== plugins: update check against the real upstreams"
papi_up GET "/servers/$psrv/plugins/updates?refresh=1" "$po/updates.json"
[ "$pcode" = 200 ] || fail "updates: HTTP $pcode $(cat "$po/updates.json")"
jcheck "$po/updates.json" "updates answer" 'd["checked_at"] > 0 and all(u["file"] in a for u in d["updates"])' "$f_viaver" "$f_viaback"
echo "OK: update check answers ($(jget "$po/updates.json" 'len(d["updates"])') update(s))"

echo "== plugins: audit entries and hash chain"
papi GET "/audit?action=plugin.&limit=200" "$po/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/audit.json")"
jcheck "$po/audit.json" "plugin.* audit entries" '
  sorted(e["details"].get("file") for e in d["items"] if e["action"] == "plugin.install" and e["result"] == "success") == sorted(a[0:3])
  and sorted(e["details"].get("enabled") for e in d["items"] if e["action"] == "plugin.toggle") == [False, True]
  and [e["details"].get("file") for e in d["items"] if e["action"] == "plugin.remove"] == [a[0]]
  and all(e["target"] == "server:" + a[3] and e["actor_name"] == "ci-admin" for e in d["items"])' \
  "$f_chunky" "$f_viaver" "$f_viaback" "$psrv"
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" audit verify -config "$work/panel.toml" >"$po/verify.txt" 2>&1 ||
  fail "panel audit verify: $(cat "$po/verify.txt")"
echo "OK: plugin.install x3 (incl. the dependency), plugin.toggle x2, plugin.remove x1 on server:$psrv by ci-admin; audit verify OK"

```

Und die letzte Zeile `echo "PASS: instance acceptance (agent and files path)"` ersetzen durch:

```bash
echo "PASS: instance acceptance (agent, files and plugins path)"
```

Hinweise zu Step 4:

- Reihenfolge: Der Abschnitt läuft nach `== remove`, also nachdem `9001` samt Route entfernt ist; agent, Dateidienst und mc-router laufen weiter. Das panel hat eine frische Datenbank und kennt `9001` nie.
- `jget`-Aufrufe direkt nach einem `jcheck`, der genau dieses Element verlangt, können nicht leer ausgehen; ein `StopIteration` würde trotzdem über `set -e` abbrechen, nur ohne `FAIL:`-Zeile.
- Warum Chunky statt LuckPerms in der CI: LuckPerms lädt beim ersten Start seine Bibliotheken aus dem Netz nach (zusätzliche Hosts, längerer Start). LuckPerms bleibt Teil der manuellen Abnahme (Spec §10 Kriterium 2). Modrinths v2-API meldet `project_type: "mod"` auch für Plugin-Projekte (Chunky, LuckPerms, ViaVersion); die Suche filtert über die Facette `project_type:plugin`, und das Skript prüft nur Loader-Kompatibilität und Slug, nicht `project_type`.
- Paper färbt die Liste von `plugins` (grün aktiv, rot deaktiviert). Ob RCON `§`-Codes mitliefert, hängt von der Paper-Version ab; `check_plugins` entfernt sie für die Namensprüfung und wertet `§c` nur aus, wenn vorhanden. Der Log-Check (`Enabling <Name> v…`, kein `Error occurred while enabling`) belegt das Laden unabhängig davon.
- `check_enabled_in_log` nutzt `since = started_at · 10⁶` (ms → ns), damit nur der Lauf seit dem letzten Neustart zählt.

- [ ] **Step 5: `.github/workflows/ci.yml`** – Job `instances`.

`timeout-minutes: 30` → `timeout-minutes: 40`.

Die Schritte „Build agent and files service“ und „Instance acceptance“ ersetzen durch:

```yaml
      - name: Build agent, files service and panel
        run: |
          go build -o bin/agent ./cmd/agent
          go build -o bin/files ./cmd/files
          go build -o bin/panel ./cmd/panel

      # Real agent + Docker + itzg Paper server: routing by hostname, hardening, console
      # (rcon, log stream, player lists, planted player files), automatic restart after a
      # crash, removal; the files service as mcpanel-mc (peer UID, os.Root limits,
      # redaction, upload/download, extract incl. zip slip); and the plugins path: the panel
      # in production mode (TOTP sign-in, sudo, CSRF) installs Chunky from Modrinth and
      # ViaBackwards with its dependency ViaVersion from Hangar (real upstreams, one retry),
      # the server loads them after a restart, a disabled jar is not loaded.
      - name: Instance acceptance
        run: sudo scripts/ci-instances.sh bin/agent bin/files bin/panel
```

Kein Netz-Schritt nötig: GitHub-Runner haben ausgehendes Internet (Mojang-Lookup und Paper-Download brauchen es schon heute). Das panel wird ohne Web-Build gebaut (`internal/webui/dist` enthält nur `.gitkeep`, wie im Job `nftables`); die Abnahme nutzt nur `/api`.

- [ ] **Step 6: Lokal prüfen**

```bash
bash -n scripts/ci-instances.sh
command -v shellcheck >/dev/null && shellcheck scripts/ci-instances.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
make check
```

Expected: keine Ausgabe von `bash -n`/`shellcheck`/`actionlint`, `make check` grün. `shellcheck` ist nur vorhanden, wenn der Nutzer es installiert hat; nicht installieren. `scripts/testvm-deploy.sh` bleibt unverändert (siehe Aufgabe 18, Step 2 (c)).

- [ ] **Step 7: Commit, Push, Draft-PR, CI**

```bash
git add scripts/ci-instances.sh .github/workflows/ci.yml
git commit -m "ci(plugins): accept plugin installs from Modrinth and Hangar on a real Paper server

The instance acceptance starts the panel in production mode, signs in with
TOTP and sudo, creates a Paper server through the API and installs Chunky
(Modrinth) and ViaBackwards with its dependency ViaVersion (Hangar) from the
real upstreams; checks checksums in the files service, loading after a
restart, disabling, removal, the icon proxy and the audit log.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/plugins-ci-docs
gh pr create --draft --title "ci,docs: TP3c plugin acceptance against real upstreams and documentation" --body "$(cat <<'EOF'
Phase C of docs/superpowers/plans/2026-10-12-plugins-00-overview.md (Tasks 17–18, see docs/superpowers/plans/2026-10-12-plugins-05-ci-docs.md).

- `scripts/ci-instances.sh`: panel in production mode, TOTP sign-in, Paper server through the API, plugins from Modrinth (Chunky) and Hangar (ViaBackwards + ViaVersion), restart with RCON `plugins`, disable, remove, icon proxy, audit
- docs: `docs/dev/acceptance-tp3c.md`, `docs/dev/README.md`, `CLAUDE.md`, `README.md`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün; im Job „Instance acceptance“ erscheinen alle `OK: … plugins …`-Zeilen, die Zeile `plugins: …` mit Chunky, ViaVersion und ViaBackwards und `PASS: instance acceptance (agent, files and plugins path)`. Bei Fehlern `gh run view <id> --log-failed`. `WARN: … retrying once` im Log ist erlaubt, ein zweiter Fehlschlag bricht mit `FAIL: upstream still unavailable after one retry …` ab – dann den Job einmal neu starten (`gh run rerun <id> --failed`) und, falls er erneut scheitert, den Lead informieren (Upstream-Störung vs. Fehler im panel anhand der Meldung unterscheiden). Skriptfehler dürfen auf der Test-VM nachgestellt werden (Branch dort nach `~/wt-plugins` klonen, Binaries bauen, `sudo bash scripts/ci-instances.sh bin/agent bin/files bin/panel` – das Skript verändert den Host und belegt Port 8080, also vorher `sudo systemctl stop mcpanel-panel` und danach wieder starten; auf der VM erlaubt, SSH nur mit abgeschalteter Sandbox).

---

### Task 18: Doku, Gesamt-Review, Abnahme, Abschluss

**Files:**
- Create: `docs/dev/acceptance-tp3c.md`
- Modify: `docs/dev/README.md`, `CLAUDE.md`, `README.md`
- Modify (Lead, nach Befund): Spec `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` (Angleichung an Fixes; Status erst beim Abschluss)
- Modify (Abschluss): Roadmap in `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`, `README.md`, `CLAUDE.md`
- Delete (Abschluss): `docs/superpowers/plans/2026-10-12-plugins-0*.md`

**Interfaces:**
- Consumes: Spec §1–§10, das tatsächlich gemergte Verhalten (vor dem Schreiben jede Zahl und jeden UI-Text gegen den Code auf `main` prüfen: `internal/plugins`, `internal/plugins/upstream`, `internal/files/jars.go`, `web/src/features/plugins`, Locale-Dateien unter `web/src/i18n`), Aufgabe 17.
- Produces: Checkliste für die manuelle Abnahme (Kriterien 2–7), aktualisierte Entwickler- und Betriebsdoku; nach dem Merge: abgenommenes, abgeschlossenes Teilprojekt 3c.

Steps 1–5 erledigt der Teammate „ci-docs“ auf `feat/plugins-ci-docs`, Steps 6–11 koordiniert der Lead.

- [ ] **Step 1: `docs/dev/acceptance-tp3c.md` anlegen** (vollständiger Inhalt; UI-Texte in Anführungszeichen vor dem Commit gegen `web/src/i18n/de` abgleichen und bei Abweichung den Text hier anpassen):

```markdown
# Abnahme Teilprojekt 3c (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` (§10).

Automatisch geprüft (CI): Unit-Tests der Upstream-Clients mit echten, gekürzten Antworten (Modrinth, Hangar, Host-Allowlist inkl. Weiterleitungen, Größen- und `Content-Length`-Prüfung, Caches, Dateinamen-Fallback), Auflösen (Tiefe, Paketlimit, Zyklen, extern, inkompatibel, Client-only-Mods), Dateidienst (`/v1/jars` mit Prüfsummen, Cache und Metadaten, Symlinks/FIFOs/Ordner ausgelassen, Upload mit falscher Prüfsumme ohne Rückstände), panel-Tests (Rechte, CSRF, Inventar-Zuordnung, Jobs, `file_exists`, Updates, Rate-Limit, Icon-Proxy, Audit), Vertrag (OpenAPI ↔ Routen, Fehlercodes, Mock pro Operation), Vitest und Playwright gegen den Mock (suchen → installieren mit Abhängigkeit → deaktivieren → Update → entfernen) und die Instanz-Abnahme (`scripts/ci-instances.sh`: panel im Produktionsmodus mit TOTP-Login, Chunky von Modrinth und ViaBackwards samt ViaVersion von Hangar auf einem echten Paper-Server installiert, nach dem Neustart per RCON `plugins` geladen, deaktiviert nicht geladen, Prüfsummen im Dateidienst, Icon-Proxy, Audit).

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client und echter Session.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP3c-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`.
2. Ausgehende Verbindungen des panels prüfen: `ssh claude@192.168.1.117 'for h in api.modrinth.com cdn.modrinth.com hangar.papermc.io hangarcdn.papermc.io; do sudo -u mcpanel curl -sS -o /dev/null -w "$h %{http_code}\n" -A "mc-panel/acceptance (https://github.com/Bernotese/mc-panel)" "https://$h/"; done'` → vier Antworten mit HTTP-Status (kein `000`).
3. Als `admin` anmelden. Server `survival` (Paper, aktuelle Version) existiert und läuft. Neuen Server `modded` anlegen: Art Fabric, Spielversion `1.21.11`, 2048 MiB, starten (erster Start legt `mods/` noch nicht unbedingt an – das panel legt den Ordner bei der ersten Installation an).
4. Benutzer `freund` hat nur `server.view` auf `survival` (aus TP3a). Benutzer `helfer` bekommt auf `survival` und `modded` zusätzlich `server.plugins` und `server.power` (Benutzer → Rechte; beim Anhaken von „Plugins/Mods“ erscheint der Hinweis „„Plugins/Mods“ erlaubt Code-Ausführung im Minecraft-Server. Vergib es nur an Personen, denen du voll vertraust.“).
5. Auf dem Mac vorbereiten: eine ältere LuckPerms-Version für Bukkit/Paper von <https://modrinth.com/plugin/luckperms/versions> herunterladen (z. B. `LuckPerms-Bukkit-5.5.53.jar`, Filter Loader Paper und die Spielversion von `survival`).
6. Minecraft Java Edition im selben Netz, Adressen `survival.mc.192-168-1-117.sslip.io` und `modded.mc.192-168-1-117.sslip.io`.

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 2a | Plugin von Modrinth | als `helfer` Server `survival` → Tab „Plugins“ → Entdecken, Quelle Modrinth, Suche „LuckPerms“ → „Installieren“ → Dialog mit Standardversion (neueste `release`) → „Installieren“ | Fortschritt pro Jar (wartet, lädt, fertig), danach steht LuckPerms unter „Installiert“ (Quelle Modrinth, Version, Icon); Banner „Neustart nötig, damit die Änderungen wirken“ mit Neustart-Knopf |
| 2b | Plugin von Hangar mit Abhängigkeit | Quelle Hangar, Suche „ViaBackwards“ → „Installieren“ | Vorschau im Dialog: „Wird mitinstalliert: ViaVersion“; nach dem Job stehen ViaBackwards und ViaVersion unter „Installiert“ (Quelle Hangar); der Treffer zeigt danach „installiert“ |
| 2c | Aktiv nach Neustart | Neustart über das Banner; danach Tab „Konsole“ → `plugins` | LuckPerms, ViaBackwards und ViaVersion grün in der Liste; Banner weg; Client verbindet sich (ViaBackwards: auch mit einer älteren Client-Version) |
| 2d | Nur extern erhältlich | Quelle Hangar, ein Projekt mit nur externem Download suchen (z. B. „EssentialsX“, falls dort nur verlinkt) → „Installieren“ | Version ausgegraut mit „Nur extern erhältlich“, Link zur Projektseite und Hinweis auf den Dateien-Tab; „Installieren“ gesperrt. Gibt es kein solches Projekt mehr, Zeile als „nicht prüfbar“ vermerken |
| 3 | Mod mit Fabric API | als `helfer` Server `modded` → Tab „Mods“ → Entdecken (nur Modrinth, kein Hangar-Umschalter) → „Chunky“ → „Installieren“; danach Neustart | Vorschau „Wird mitinstalliert: Fabric API“; beide Jars in `mods/`; der Server startet (Status „läuft“, Konsole `chunky help` antwortet); reine Client-Mods (z. B. „Sodium“) erscheinen in der Suche nicht |
| 4a | Manuelles Jar wird zugeordnet | als `admin` Tab „Dateien“ von `survival` → `plugins/`: LuckPerms-Jar löschen (oder über „Plugins“ entfernen), dann das ältere `LuckPerms-Bukkit-5.5.53.jar` hochladen; Tab „Plugins“ öffnen | das Jar steht mit Quelle Modrinth, Projekt LuckPerms und Version 5.5.53 da (nicht „manuell“), ohne „vom panel installiert“ |
| 4b | Update | „Nach Updates suchen“ → Update-Badge an LuckPerms → „Alle aktualisieren (1)“ | Job lädt die neue Version unter neuem Dateinamen, das alte Jar ist weg (Dateien-Tab), Version in der Liste aktualisiert; nach Neustart läuft die neue Version (`plugins`, `lp info` in der Konsole) |
| 4c | Nicht zugeordnet | ein beliebiges eigenes Jar ohne Modrinth-Eintrag (z. B. ein umbenanntes Test-Jar aus dem Dateien-Tab) nach `plugins/` hochladen | Eintrag „manuell“ mit Name/Version aus `plugin.yml` und Hinweis „Nicht zugeordnet – Updates nicht möglich“ |
| 5a | Deaktivieren | Schalter von ViaBackwards aus → Neustart → Konsole `plugins` | Datei heißt `ViaBackwards-….jar.disabled` (Dateien-Tab); ViaBackwards fehlt in der Liste, ViaVersion läuft weiter |
| 5b | Wieder aktivieren | Schalter wieder an → Neustart → `plugins` | ViaBackwards wieder grün |
| 5c | Entfernen | Menü an Chunky (`modded`) → „Entfernen“ → Bestätigen | Jar weg aus `mods/` und aus der Liste; Fabric API bleibt (Abhängigkeiten werden nicht mit entfernt) |
| 6a | nur `server.view` | als `freund` Server `survival` öffnen | kein Tab „Plugins“ |
| 6b | API-Sperre | als `freund` in den DevTools: `await fetch('/api/servers/<id>/plugins').then(r => r.status)` | `403` |
| 7 | Audit | als `admin` Audit-Log, Filter auf `plugin.`; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | je ein Eintrag `plugin.install` pro Jar (auch ViaVersion und Fabric API), `plugin.update` (von/nach), `plugin.toggle` (mit `enabled`), `plugin.remove`, Ziel `server:<id>`, Benutzer `helfer` bzw. `admin`; Suche, Inventar, Updatecheck und Icons erscheinen nicht; `verify` meldet OK |
| 8a | Ein Job pro Server | zwei Browser-Tabs, in beiden gleichzeitig eine Installation auf `survival` starten | der zweite Tab meldet, dass bereits ein Vorgang läuft (`409`); Knöpfe sind während des Jobs gesperrt |
| 8b | Upstream nicht erreichbar | auf der VM ausgehend sperren: `ssh claude@192.168.1.117 'sudo nft add table inet tp3c; sudo nft add chain inet tp3c out "{ type filter hook output priority 0; }"; sudo nft add rule inet tp3c out meta skuid mcpanel tcp dport 443 reject'`, Tab „Plugins“ neu laden und „Entdecken“ öffnen; danach `ssh claude@192.168.1.117 sudo nft delete table inet tp3c` | „Installiert“ bleibt bedienbar (Schalter, Entfernen), Hinweis „Modrinth gerade nicht erreichbar – Zuordnung unvollständig“ höchstens für nicht vom panel installierte Jars; „Entdecken“ zeigt einen Fehlerzustand mit „Erneut versuchen“, das nach dem Entsperren wieder Treffer liefert |
| 8c | Namenskollision | im Dateien-Tab eine Kopie eines installierten Jars unter dem Dateinamen anlegen, den ein anderes Projekt bei der Installation bekäme (z. B. ein beliebiges Jar als `Chunky-Bukkit-<version>.jar` auf `survival` hochladen), dann Chunky über „Plugins“ installieren | Job-Schritt scheitert mit „Datei existiert bereits“ (`file_exists`), das vorhandene Jar bleibt unverändert |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px): Installiert-Liste als Karten, Suche und Installieren-Dialog bedienbar, Job-Fortschritt sichtbar, kein horizontales Scrollen der Seite.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
```

- [ ] **Step 2: `docs/dev/README.md`** – sechs Stellen.

(a) Abschnitt „Minecraft-Server lokal (optional)“: nach dem letzten Spiegelstrich (Dateidienst im Dev-Modus) einfügen:

```markdown
- Plugins und Mods (Tab „Plugins“ bzw. „Mods“ bei Paper, Purpur, Fabric und NeoForge) brauchen nichts Zusätzliches: Das panel spricht auch im Dev-Modus mit den echten Upstreams (Modrinth, Hangar) und lädt Jars über den Dateidienst nach `.dev/mc/instances/<id>/plugins` bzw. `mods`. Ohne Docker funktionieren Suche, Installieren, Inventar, Schalter und Entfernen; „Neustart nötig“ erscheint nur bei laufendem Container. Ohne Internet zeigt „Entdecken“ einen Fehlerzustand, „Installiert“ bleibt bedienbar. Wer Upstreams schonen will, nimmt `npm run dev:mock` (Katalog, Jobs und Icons aus dem Mock).
```

und den Satz „die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus), `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler) und `docs/dev/acceptance-tp3b.md` (Dateibrowser).“ ersetzen durch „die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus), `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler), `docs/dev/acceptance-tp3b.md` (Dateibrowser) und `docs/dev/acceptance-tp3c.md` (Plugins und Mods).“

(b) Abschnitt „End-to-End-Tests (Playwright)“, Tabellenzeile `npm run e2e`: die Liste der Spec-Dateien um `e2e/plugins.spec.ts` ergänzen (vorher mit `ls web/e2e` prüfen, wie B3 die Datei genannt hat).

(c) Abschnitt „Test-VM“, nach dem Spiegelstrich „Das panel lauscht auf der VM …“ einfügen:

```markdown
- Plugins/Mods brauchen auf der VM keine eigene Konfiguration: `scripts/testvm-deploy.sh` bleibt unverändert, weil die panel-Unit ausgehende Verbindungen nicht einschränkt (wie für die Versionskataloge) und der Dateidienst bewusst ohne Netz bleibt (`RestrictAddressFamilies=AF_UNIX`). Die Checkliste steht in `docs/dev/acceptance-tp3c.md`.
```

(d) Abschnitt „Produktion: Listen-Adresse und Reverse Proxy“: nach dem Spiegelstrich „Datei-Uploads und -Downloads (bis 4 GiB) …“ einfügen:

```markdown
- **Ausgehende Verbindungen des panels** (nur HTTPS, Port 443, User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`): Versionskataloge `piston-meta.mojang.com`, `fill.papermc.io`, `api.purpurmc.org`, `meta.fabricmc.net`, `maven.neoforged.net`; Plugins/Mods `api.modrinth.com`, `hangar.papermc.io` (API) sowie `cdn.modrinth.com`, `hangarcdn.papermc.io` (Jars und Icons; andere Hosts lehnt das panel ab, auch als Weiterleitung). Eine Egress-Firewall muss diese Hosts für den Benutzer `mcpanel` erlauben. Der Dateidienst und der agent brauchen für Plugins kein Netz; die Minecraft-Container selbst laden weiterhin, was itzg beim Start braucht (Server-Jars, Modpacks).
- **Plugins/Mods** (Spec 3c): Das panel lädt Jars als Stream (≤ 256 MiB) und reicht sie an den Dateidienst weiter, der die Prüfsumme des Upstreams (Modrinth sha512, Hangar sha256) vor dem `rename` prüft (`PUT /v1/upload?…&sha512=…`); bei Abweichung bleibt nichts liegen. Quelle der Wahrheit ist der Ordner `plugins/` bzw. `mods/`; die Tabelle `plugin_installs` merkt sich nur die Herkunft panel-installierter Jars. Jobs (Installieren, Aktualisieren) laufen im Speicher, einer pro Server und vier im panel; ein Neustart des panels bricht laufende Jobs ab, bereits installierte Jars bleiben. Icons gehen über den Proxy `/api/servers/{id}/plugins/icon` (CSP bleibt `img-src 'self'`). Modrinth drosselt auf 300 Anfragen/min pro IP; das panel cacht Suche (5 min), Projekte/Versionen (15 min), Hash-Zuordnung und Updates (1 h).
```

(e) Abschnitt „CI lokal nachstellen“: Zeile `instances` ersetzen durch:

```markdown
| `instances` | nur in der CI: `sudo scripts/ci-instances.sh bin/agent bin/files bin/panel` (echter agent + Docker + Paper-Server: Routing, Härtung, Konsole mit RCON, Log-Stream und Spielerlisten, präparierte Spielerdateien, Absturz-Neustart, Löschen; Dateidienst als `mcpanel-mc`: Peer-UID (panel-Benutzer und root erlaubt, `nobody` abgewiesen), Upload, Schwärzen, Editor-Sperre, Symlinks, Ordner-Zip, Welt entpacken, bösartige Archive; Plugins: panel im Produktionsmodus mit TOTP-Login und Sudo, zweiter Paper-Server über die API, Chunky von Modrinth und ViaBackwards + ViaVersion von Hangar über die echten Upstreams (eine Wiederholung bei Upstream-Fehlern), Prüfsummen im Dateidienst, Icon-Proxy, Neustart mit RCON `plugins`, Deaktivieren, Entfernen, Audit; verändert den Host und braucht Internet) |
```

(f) Abschnitt „Erster Start“: nach der CLI-Tabelle nichts ändern; im Abschnitt „Schnellstart“ nichts ändern (`make dev` startet keine neuen Prozesse).

- [ ] **Step 3: `CLAUDE.md`** (Englisch, knapp) – sechs Stellen.

(a) Spec-Liste: den Roadmap-Satz „(1 foundation, 2 instances and 3a console are done)“ korrigieren zu „(1 foundation, 2 instances, 3a console and 3b files are done)“ (3b wurde beim Abschluss von 3b hier vergessen; 3c kommt erst im Abschluss-Step 11 dazu). Neue Zeile nach der 3b-Zeile:

```markdown
- `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md`: plugins and mods from Modrinth and Hangar, installed through the files service (sub-project 3c); manual acceptance checklist in `docs/dev/acceptance-tp3c.md`.
```

(b) Architektur, files-Spiegelstrich: vor „The root agent never touches user-supplied file paths or archives.“ einfügen:

```markdown
`POST /v1/jars` lists the top-level `.jar`/`.jar.disabled` files of `plugins/` or `mods/` with sha256/sha512 (cached by inode, size and mtime) and best-effort metadata from `plugin.yml`/`paper-plugin.yml`/`fabric.mod.json`/`neoforge.mods.toml`; `PUT /v1/upload` takes an optional `sha256` or `sha512` and compares before `fsync`/`rename` (`400 hash_mismatch`, nothing left behind). The service has no network access.
```

(c) „Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `serverfiles`, `audit/auditapi`)“ → „Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `serverfiles`, `plugins`, `audit/auditapi`)“.

(d) Abschnitt „Data“: „Query names carry a domain prefix (`Auth…`, `Defense…`, `Rbac…`, `Audit…`, `Servers…`).“ → „Query names carry a domain prefix (`Auth…`, `Defense…`, `Rbac…`, `Audit…`, `Servers…`, `Plugins…`).“

(e) Abschnitt „Minecraft instances“, nach dem `internal/serverfiles`-Spiegelstrich einfügen:

```markdown
- `internal/plugins` exposes `/servers/{id}/plugins/*` (`server.plugins`, full trust) for paper/purpur (plugins from Modrinth and Hangar, `plugins/`) and fabric/neoforge (mods from Modrinth, `mods/`); other types answer `409 plugins_unsupported`. `internal/plugins/upstream` talks to fixed bases (`api.modrinth.com`, `hangar.papermc.io`) and downloads jars and icons only from `cdn.modrinth.com`/`hangarcdn.papermc.io` (https, redirects only there). The panel streams each jar to the files service with the upstream checksum (`UploadChecked`); it never writes plugin files itself. The disk is the source of truth; `plugin_installs` only remembers the origin of panel-installed jars (filename + sha512), other jars are matched via Modrinth `version_files` or shown as manual. Install/update run as in-memory jobs (one per server, four per panel, dependencies first); icons go through the proxy `/servers/{id}/plugins/icon` (CSP unchanged). Installs, updates, toggles and removals are audited as `plugin.*`, one entry per jar.
```

(f) Letzter Spiegelstrich „Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints and the files service) run only in CI.“ → „Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints, the files service and plugin installs from the real Modrinth and Hangar through the panel in production mode) run only in CI.“

Zusätzlich im Spiegelstrich „Version catalogs (`internal/servers/catalog`) fetch fixed upstream URLs with a contact User-Agent and a 1 h cache.“ → „Version catalogs (`internal/servers/catalog`) and the plugin upstreams (`internal/plugins/upstream`) fetch fixed upstream URLs with a contact User-Agent and in-memory caches.“

- [ ] **Step 4: `README.md`** – vier Stellen.

(a) „Funktionen“: nach dem Spiegelstrich „Dateien“ einfügen:

```markdown
- **Plugins und Mods:** Plugins für Paper/Purpur aus Modrinth und Hangar, Mods für Fabric/NeoForge aus Modrinth suchen und mit einem Klick samt Pflicht-Abhängigkeiten installieren; installierte Jars (auch per Hand hochgeladene) sehen, aktualisieren, deaktivieren und entfernen. Das panel prüft jede Datei gegen die Prüfsumme der Quelle; geschrieben wird sie vom unprivilegierten Dateidienst.
```

und im Spiegelstrich „Rechte“: „(`server.view`, `server.power`, `server.console`, `server.files`)“ → „(`server.view`, `server.power`, `server.console`, `server.files`, `server.plugins`)“.

(b) Tabelle „Aufbau“, Zeile `panel`: „Weboberfläche und `/api`, SQLite-Datenbank, CLI (…)“ → „Weboberfläche und `/api`, SQLite-Datenbank, Downloads von Modrinth/Hangar, CLI (`serve`, `admin create`, `admin unlock`, `audit verify`)“.

(c) „Dokumentation“: Zeilen nach der 3b-Spec-Zeile ergänzen:

```markdown
| [`docs/dev/acceptance-tp3c.md`](docs/dev/acceptance-tp3c.md) | manuelle Abnahme von Plugins und Mods auf der Test-VM |
| [`docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md`](docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md) | Plugins und Mods aus Modrinth und Hangar |
```

(d) „Stand“: Zeile `| 3c | Live-Betrieb: Plugins/Mods (Modrinth, Hangar) | offen |` → `| 3c | Live-Betrieb: Plugins/Mods (Modrinth, Hangar) | in Arbeit |`.

- [ ] **Step 5: Prüfen und committen**

Jede Zahl, jeden Namen und jeden UI-Text gegen den Code auf dem Branch prüfen, z. B.:

```bash
grep -rn "maxJarBytes\|256 << 20\|MaxJars\b" internal/plugins internal/files | head
grep -rn "5 \* time.Minute\|15 \* time.Minute\|time.Hour" internal/plugins | head
grep -rn "ActionPlugin" internal/audit/types.go
grep -rn "Nur extern erhältlich\|Neustart nötig\|Nicht zugeordnet\|Code-Ausführung\|Wird mitinstalliert\|Erneut versuchen" web/src/i18n | head -20
ls web/e2e
```

Abweichungen im Text korrigieren, nicht im Code (Code-Abweichungen von der Spec meldet der Teammate dem Lead).

```bash
make check
git add docs/dev/acceptance-tp3c.md docs/dev/README.md CLAUDE.md README.md
git commit -m "docs: plugins and mods for dev, operation, CI and acceptance

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: CI grün. Danach dem Lead melden: Commit-Bereiche beider Aufgaben, CI-Run-URL, Laufzeit des Jobs „Instance acceptance“, ob `WARN: … retrying` vorkam, Abweichungen vom Plan.

- [ ] **Step 6 (Lead): Gesamt-Review parallel starten** – sobald B1–B3 gemerged sind (unabhängig von Aufgabe 17), drei Review-Subagenten auf dem höchsten Modell, Basis = Commit vor dem Merge von `feat/plugins-contracts`, Ende = aktueller `main` (`git diff <basis>..origin/main`), jeder mit Spec, Übersicht (Verträge, Global Constraints, **Review Focus 1–5**) und der Liste der aufgeschobenen Minors aus den Ledgern der Phasen:
  1. **Security** – Reihenfolge: (a) Review Focus 1: feindliche Upstream-Antworten (fremder Download-/Icon-Host, Weiterleitung auf `http` oder fremde Hosts, Dateiname mit `/`, `..`, `\`, Steuerzeichen, `size` ≠ Länge, riesiges JSON, `null`-Felder) – nie ein Byte außerhalb von `plugins/`/`mods/`, keine Teil-Datei; (b) Review Focus 2: Namenskollisionen und Groß-/Kleinschreibung (`Foo.jar`/`foo.JAR`, `x.jar` neben `x.jar.disabled`, Schalter auf vorhandenen Namen, Update auf fremden Namen) – nie ein fremdes Jar überschrieben oder gelöscht; (c) Review Focus 5 im Dienst: > 500 Jars, > 256 MiB, Zip-Bombe in `plugin.yml`, 100 001 Einträge, Symlink-Jar, FIFO – Grenzen, `meta: null`, keine Hänger, kein Lesen außerhalb von `os.Root`; Upload mit Prüfsumme (Vergleich vor `fsync`/`rename`, Temp-Datei weg); (d) Icon-Proxy: Präfixprüfung inkl. Schrägstrich, Magic Bytes statt `Content-Type`, kein SVG, Antwort-Header, Größen- und Gleichzeitigkeitsgrenze; (e) panel: Rechte aller elf Routen (`server.plugins`, `409 plugins_unsupported` außer `icon`), CSRF, Rate-Limit, `file`-Parameter (reiner Name, `CleanPath`, nicht verborgen), Audit (ein Eintrag pro Jar, Akteur des Job-Starters); (f) Frontend: kein `dangerouslySetInnerHTML`, Upstream-Texte nur als Text, Bilder nur über den Proxy, Links mit `rel="noopener noreferrer"`.
  2. **Backend** – Querschnitt Upstream ↔ Auflösen ↔ Jobs ↔ Dienst ↔ OpenAPI: Review Focus 3 (gleichzeitige Änderungen: Jar im Dateien-Tab gelöscht/ersetzt während eines Jobs, zwei Tabs, panel-Neustart während eines Jobs; genau ein Job pro Server, verwaiste DB-Einträge verschwinden), Review Focus 4 (Upstream weg oder `429` mitten im Ablauf: `source_errors`, Schritt `source_unavailable`, installierte Schritte bleiben, keine Hänger), `LATEST` → `catalog_unavailable`, Caches (Größe, Verdrängung, geteilte Aufrufe, „unbekannt“ gecacht), Updates (Kanäle, Hangar „neuer veröffentlicht“, deaktivierte Jars bleiben deaktiviert), `restart_needed`, Fristen (10 s API, 60 s pro Lesevorgang, 2 min `Jars`), Shutdown (`Run`), Spec-Treue §3–§6.
  3. **Frontend** – gegen das echte Backend (Test-VM oder `make dev`, nicht nur den Mock): Installiert-Liste (Namensfallback, Badges, manuelle Jars), Entdecken (verzögerte Suche, Quellen-Umschalter, „Mehr laden“, Fehlerzustand), Installieren-Dialog (Versionen, Beta/Alpha, nur extern, Vorschau, Sperre bei `incompatible`), Job-Fortschritt (Polling nur solange `running`, gesperrte Knöpfe, übersetzte Fehlercodes), Banner mit Neustart-Knopf nur bei `server.power`, Rechte-Hinweis, i18n vollständig (`de`/`en`), Mobil, Barrierefreiheit.
  Befunde mit `superpowers:receiving-code-review` bewerten und priorisieren; jeder Befund nennt Datei, Zeile, Szenario und erwartetes Verhalten.

- [ ] **Step 7 (Lead): Fix-Runden** – berechtigte Befunde (Critical/Important und die als „jetzt beheben“ eingestuften Minors) gehen an die zuständigen Teammates nach Dateieigentum der Phase B (B1 `internal/files/**`, B2 `internal/plugins/**`/`cmd/panel/**`, B3 `web/**`; Vertragsänderungen an `filesapi`, Store, Fehlercodes, OpenAPI, Mocks über den Lead wie in Phase A), je in eigenem Worktree auf `fix/plugins-review-<bereich>` von `origin/main`, Muster wie TP2–3b: `test(<bereich>): cover whole-feature review findings of TP3c (red)`, dann `fix(<bereich>): address whole-feature review findings of TP3c`. Die Teammates ändern keine Doku, sondern liefern eine Liste nötiger Spec-Änderungen. Danach ein Re-Review nur der Fixes; offene Punkte entscheidet der Lead mit Begründung im Ledger. Nicht berechtigte Befunde mit Begründung als PR-Kommentar festhalten.

- [ ] **Step 8 (Lead): Spec angleichen und CI auf dem Gesamtstand** – der Teammate „ci-docs“ führt auf `feat/plugins-ci-docs` `git merge origin/main` aus (damit die CI-Abnahme gegen alle Fixes läuft), übernimmt die Liste aus Step 7 mit eigenem Commit `docs(spec): bring the TP3c spec in line with the review fixes` (Status-Zeile bleibt „entworfen“) und passt Doku-Texte an geänderte Zahlen/Texte an. Danach Re-Review der Doku aus Steps 1–4 gegen den Code.

- [ ] **Step 9 (Lead): Test-VM-Deploy und Übergabe**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
sha="$(git -C /Users/jannes/Workspace/mc-panel rev-parse origin/feat/plugins-ci-docs)"
make deploy-testvm REF="$sha"
ssh claude@192.168.1.117 'systemctl is-active mcpanel-agent mcpanel-files.socket mcpanel-files.service mcpanel-panel caddy; for h in api.modrinth.com cdn.modrinth.com hangar.papermc.io hangarcdn.papermc.io; do sudo -u mcpanel curl -sS -o /dev/null -w "$h %{http_code}\n" "https://$h/"; done; systemctl show mcpanel-files -p RestrictAddressFamilies'
```

(SSH nur mit abgeschalteter Sandbox.) Expected: fünfmal `active`; vier Hosts mit HTTP-Status (kein `000`); `RestrictAddressFamilies=AF_UNIX` beim Dateidienst. PRs `fix/plugins-review-*` und `feat/plugins-ci-docs` mit grüner CI auf „ready“ setzen (`gh pr ready <pr>`); dem Nutzer melden: PR-Links, Zusammenfassung der Befunde und Entscheidungen, Merge-Reihenfolge (erst Fixes, dann CI/Doku), und dass `docs/dev/acceptance-tp3c.md` auf der VM (Stand `$sha`) abgenommen werden kann. Nicht selbst mergen.

- [ ] **Step 10 (Lead): Abnahme begleiten** – nach dem Merge durch den Nutzer:

```bash
git -C /Users/jannes/Workspace/mc-panel switch main && git -C /Users/jannes/Workspace/mc-panel pull --ff-only
make deploy-testvm
```

Dann dem Nutzer `docs/dev/acceptance-tp3c.md` übergeben. Befunde: Issue, Fix auf eigenem `fix/…`-Branch mit PR (Teammate), CI grün, erneut deployen, betroffene Zeilen der Checkliste wiederholen.

- [ ] **Step 11 (Lead): Abschluss nach erfolgreicher Abnahme („docs: close sub-project 3c“)** – auf neuem Branch `docs/tp3c-done` von `origin/main`:

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel switch -c docs/tp3c-done origin/main
gh pr list --state merged --search "plugins in:title" --json number,title,mergedAt
```

- Spec `2026-10-12-mc-panel-plugins-design.md`: Status-Zeile `- **Status:** entworfen` → `- **Status:** umgesetzt (PRs #<erste>–#<letzte>, <Merge-Datum der letzten, JJJJ-MM-TT>)` (Nummern aus der Liste, inkl. Spec-/Plan-PR und Fix-PRs).
- Roadmap in `2026-10-08-mc-panel-foundation-design.md`: Zeile `| 3c | Live-Betrieb: Plugins/Mods | Plugins/Mods (Modrinth, Hangar) |` → `| 3c | Live-Betrieb: Plugins/Mods (umgesetzt, Spec `2026-10-12-mc-panel-plugins-design.md`) | Plugins/Mods (Modrinth, Hangar) |`.
- `README.md`, „Stand“: 3c `in Arbeit` → `fertig`; `CLAUDE.md`: „(1 foundation, 2 instances, 3a console and 3b files are done)“ → „(1 foundation, 2 instances, 3a console, 3b files and 3c plugins are done)“.
- Pläne löschen und committen:

```bash
cd /Users/jannes/Workspace/mc-panel
git rm docs/superpowers/plans/2026-10-12-plugins-0*.md
git add docs/superpowers/specs README.md CLAUDE.md
make check
git commit -m "docs: drop the completed sub-project 3c plans, mark the spec done

All plan tasks are merged and the manual acceptance on the test VM passed; git
history keeps the plans. The spec stays as the reference and says so in its
status line.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin docs/tp3c-done
gh pr create --title "docs: close sub-project 3c" --body "$(cat <<'EOF'
Drops the completed TP3c plans (git history keeps them) and marks the spec, the roadmap row, README and CLAUDE.md done. Manual acceptance on the test VM passed.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
gh pr checks <pr> --watch --interval 30
```

- Worktrees der Teammates entfernen (`git worktree remove ../mc-panel-wt/<branch>`), lokale Branches löschen, Teammates herunterfahren; Remote-Branches nur nach Freigabe des Nutzers (GitHub löscht gemergte Branches ggf. selbst). SDD-Workspaces unter `.superpowers/sdd/2026-10-12-plugins-*` löschen, nachdem die „Rulings“ dem Nutzer berichtet wurden. Mergen des Abschluss-PRs nur durch den Nutzer.

## Self-Review (dieser Datei)

- Spec §9 Zeile „CI-Abnahme“: kleines Modrinth-Plugin (Chunky, ohne Abhängigkeiten) und Hangar-Plugin mit Pflicht-Abhängigkeit (ViaBackwards → ViaVersion) installieren → Aufgabe 17 Step 4 („install“); Neustart und RCON `plugins` → „restart“; ein Jar deaktivieren und erneut prüfen → „disable Chunky“; Upstream-Fehler einmal wiederholen, dann klar abbrechen → `papi_up`, `install_plugin`; zusätzlich Icon-Proxy mit fremdem Host/`http`/Look-alike → 400, CSRF → 403 ohne Job, Audit `plugin.*` + `audit verify`, Updatecheck, Entfernen.
- Spec §10: Kriterium 1 → CI-Läufe in Aufgaben 17–18; Dienst-Seite von 2 (Jars im Ordner, Besitzer `mcpanel-mc`, Prüfsummen und Metadaten über `/v1/jars`, Upload mit Prüfsumme) und 5 (`.jar.disabled` nicht geladen) → Aufgabe 17 Step 4; 2–7 manuell → `docs/dev/acceptance-tp3c.md` (Zeilen 2a–7, dazu 8a–8c für Review Focus 2–4).
- Spec §8 (keine neue Konfiguration, ausgehende Hosts) → Aufgabe 18 Step 2 (c)/(d); `scripts/testvm-deploy.sh` und `Makefile` bleiben unverändert (panel-Unit ohne Netz-Einschränkung, Dateidienst bleibt `AF_UNIX`, keine neuen Prozesse in `make dev`).
- V3 genutzt wie festgelegt: `installPlugin` → `202 PluginJob` (Feld `id` direkt), `getPluginJob` → `{job}`, `InstalledJar.file` mit `.jar.disabled`, `project.id` (Hangar-ID als Text), `required_by` = Titel bzw. `null`, `icon_url` als Proxy-Pfad, Fehler `validation_failed`/`csrf_invalid`; V1: `/v1/jars`, `sha256`/`sha512` höchstens einer, `hash_mismatch`/`invalid`.
- Kein Schritt startet Container, braucht root oder lädt Jars auf dem Mac; Host-verändernde Prüfungen nur in der CI und auf der Test-VM. Mergen, Tags und Löschen von Remote-Branches nur durch bzw. nach Freigabe des Nutzers.
