# Teilprojekt 3a – CI, Doku und Abnahme (Aufgaben 19–21) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Konsole, Log-Stream und Spielerlisten automatisiert gegen einen echten Paper-Server auf einem CI-Runner prüfen (agent-Pfad), die Entwickler-Doku, `CLAUDE.md` und `README.md` nachziehen und Teilprojekt 3a abschließen: Gesamt-Review in dieser Session, Funktionsabnahme durch den Nutzer auf der Test-VM, danach Pläne löschen.

**Architecture:** `scripts/ci-instances.sh` (CI-Job `instances`, root auf einem Wegwerf-Runner) startet schon in TP2 den echten agent und einen Paper-Server. Aufgabe 19 hängt an zwei Stellen neue Prüfungen an: nach der Härtungsprüfung (Server läuft) RCON, Log-Stream (inkl. `since`), Spielerlisten nach `whitelist add`/`op`/`ban`/`pardon`, Validierung und Container-Umgebung; nach der Absturz-Prüfung (Server gestoppt) `409 not_running`, das selbstständige Ende des Log-Streams, Spielerlisten bei gestopptem Server und präparierte Spielerdateien (Symlink auf `/etc/shadow`, FIFO, Symlink aus dem Datenverzeichnis heraus). Der panel-Pfad (SSE, Rechte, Audit) ist durch Go-Tests (Aufgaben 9–13) und Playwright (Aufgabe 18) abgedeckt und wird manuell auf der Test-VM abgenommen.

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, immer `python3 -I`), curl mit `--unix-socket`, GitHub Actions (`ubuntu-24.04`, Docker vorinstalliert).

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (§8, §9 Zeile „`instances` (CI)“, §10)

## Global Constraints

Es gelten alle Global Constraints aus `2026-10-11-console-00-overview.md` und die Arbeitsregeln aus `CLAUDE.md`. Zusätzlich:

| Thema | Wert |
|---|---|
| Phase / Branch | C, Lead, `feat/console-ci-docs` (Aufgaben 19–21 Step 1–5); Abschluss nach Merge auf `docs/tp3a-done` (Aufgabe 21 Step 7) |
| Startet, wenn | Phasen A, B1, B2, B3 auf `main` gemerged |
| Wo getestet wird | Lokal nur `make check` und `shellcheck`; alles mit root, Docker oder Minecraft ausschließlich im CI-Job `instances` |
| Test-Server | wie TP2: ID `9001`, Name `citest`, FQDN `citest.mc.localhost`, Paper `1.21.11`, java21, 2048 MiB, Online-Modus (Default) |
| Test-Spieler | `Notch` (existiert bei Mojang; `whitelist add`, `op`, `ban` lösen den Namen online auf, der Runner hat Internet) |
| agent-Aufrufe im Skript | als root über `curl --unix-socket` (der agent erlaubt panel-UID und 0); Bodies, die Nutzertext enthalten, immer per `json.dumps` kodieren |
| agent-Antworten | Fehler-JSON `{"error":"…","code":"…"}` ohne Leerzeichen (`encoding/json`), Log-Stream `Content-Type: application/x-ndjson` |
| Zeitlimits | RCON bereit ≤ 2 min nach dem Status-Ping, Stopp ≤ 2 min, Log-Stream eines laufenden Containers wird nach 3–5 s per `--max-time` beendet (curl-Exit 28 ist dort erwartet), Spielerlisten mit präparierten Dateien ≤ 10 s; Job-Timeout bleibt 30 min |

---

### Task 19: CI-Abnahme der Konsole (agent-Pfad)

**Files:**
- Modify: `scripts/ci-instances.sh` (ganze Datei ersetzen, Inhalt unten)
- Modify: `.github/workflows/ci.yml` (nur der Kommentar über dem Schritt „Instance acceptance“ im Job `instances`)

**Interfaces:**
- Consumes: agent-Endpunkte aus Aufgabe 7 (`POST /v1/instances/logs|rcon|players`, Verträge V1/V2 der Übersicht: `LogsRequest{id, tail, since}`, NDJSON `LogLine{ts, stream, line}`, `RconResponse{output}`, `PlayersResponse{ops, whitelist, bans, errors}`, Fehlercodes `not_found`/`not_running`/`rcon_unavailable`), Container-Umgebung aus Aufgabe 1 (kein `OPS`/`WHITELIST`/`EXISTING_*_FILE`), Spielerdatei-Prüfung aus Aufgabe 6 (`os.Root`, `O_NOFOLLOW|O_NONBLOCK`, nur reguläre Dateien).
- Produces: `scripts/ci-instances.sh AGENT_BINARY` endet weiterhin mit `PASS: instance acceptance (agent path)` und gibt zusätzlich die `OK: …`-Zeilen der Konsole aus (Step 4).

- [ ] **Step 1: Branch anlegen**

```bash
git fetch origin
git switch -c feat/console-ci-docs origin/main
```

- [ ] **Step 2: Skript ersetzen** – `scripts/ci-instances.sh` vollständig durch diesen Inhalt ersetzen (bis „== hardening“ unverändert bis auf Kopfkommentar und Hilfsfunktionen; neu sind die Abschnitte „== console …“, „== stopped server …“ und „== player files …“):

```bash
#!/usr/bin/env bash
# Instance acceptance for a Linux CI runner with Docker (sub-project 2, spec §12 criteria 2,
# 6, 7, 8 on the agent path; sub-project 3a, spec §10 criteria 2 and 4 on the agent path):
# starts the real agent as root, creates a Paper server through its socket, waits until it
# answers a status ping routed by hostname through mc-router, checks the container hardening
# and the console endpoints (rcon, log stream, player lists), crashes it, waits for Docker's
# restart, checks the console on the stopped server and planted player files, and removes it.
#
# Usage (as root): scripts/ci-instances.sh AGENT_BINARY
# Changes the host (users, Docker containers/network, port 25565): disposable CI runners only.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "$#" -ne 1 ]; then
  echo "usage: scripts/ci-instances.sh AGENT_BINARY" >&2
  exit 2
fi
if [ "$(id -u)" -ne 0 ] || [ "$(uname -s)" != Linux ]; then
  echo "ci-instances: Linux and root only" >&2
  exit 1
fi

work="$(mktemp -d)"
chmod 0755 "$work"
sock="$work/run/agent.sock"
agent_pid=""

cleanup() {
  if [ -n "$agent_pid" ]; then
    kill "$agent_pid" 2>/dev/null || true
    wait "$agent_pid" 2>/dev/null || true
  fi
  ids="$(docker ps -aq --filter label=mcpanel.instance; docker ps -aq --filter label=mcpanel.router)"
  if [ -n "$ids" ]; then
    # shellcheck disable=SC2086 # one container id per word
    docker rm -f $ids >/dev/null 2>&1 || true
  fi
  docker network rm mcpanel-mc >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  echo "--- agent log" >&2
  cat "$work/agent.log" >&2 || true
  docker logs --tail 80 mcpanel-mc-9001 >&2 2>/dev/null || true
  docker logs --tail 40 mcpanel-router >&2 2>/dev/null || true
  exit 1
}

agent() { curl -fsS --unix-socket "$sock" -H 'Content-Type: application/json' "$@"; }

# post_status <path> <json> <outfile>: POST without -f; prints the HTTP status, body to outfile.
post_status() {
  curl -sS -o "$3" -w '%{http_code}' --unix-socket "$sock" -H 'Content-Type: application/json' \
    -X POST "http://agent$1" -d "$2"
}

# rcon <command> <outfile>: one console command through the agent; the body is JSON-encoded
# by Python, so the command text never ends up in the shell or in hand-written JSON.
rcon() {
  agent -X POST http://agent/v1/instances/rcon \
    -d "$(python3 -I -c 'import json, sys; print(json.dumps({"id": 9001, "command": sys.argv[1]}))' "$1")" >"$2"
}

# rcon_output <file>: prints the "output" field of an rcon answer.
rcon_output() { python3 -I -c 'import json, sys; print(json.load(open(sys.argv[1]))["output"])' "$1"; }

# logs <json> <outfile> <seconds>: reads the NDJSON log stream (headers to <outfile>.headers).
# For a running container the stream follows the log, so curl ends it after <seconds> with
# exit 28; logs_rc keeps curl's exit code (0 = the agent ended the stream itself).
logs_rc=0
logs() {
  logs_rc=0
  curl -sS -N --max-time "$3" -D "$2.headers" --unix-socket "$sock" \
    -H 'Content-Type: application/json' -X POST http://agent/v1/instances/logs -d "$1" >"$2" || logs_rc=$?
  if [ "$logs_rc" -ne 0 ] && [ "$logs_rc" -ne 28 ]; then
    fail "logs stream: curl exit $logs_rc"
  fi
}

# players <outfile>: the player lists of instance 9001.
players() { agent -X POST http://agent/v1/instances/players -d '{"id": 9001}' >"$1"; }

# check_players <file> <python expression on d>: exit 1 with the lists if the expression is false.
check_players() {
  python3 -I -c '
import json, sys
d = json.load(open(sys.argv[1]))
if not eval(sys.argv[2], {}, {"d": d}):
    sys.exit("players check failed: %s\n%s" % (sys.argv[2], json.dumps(d)))' "$1" "$2"
}

# status_field <python expression on instance dict i>; prints "absent" without instance 9001.
status_field() {
  agent http://agent/v1/instances | python3 -I -c '
import json, sys
expr = sys.argv[1]
for i in json.load(sys.stdin)["instances"]:
    if i["id"] == 9001:
        print(eval(expr, {}, {"i": i}))
        break
else:
    print("absent")' "$1"
}

wait_for() { # wait_for <seconds> <description> <command...>
  local deadline=$((SECONDS + $1)) what=$2
  shift 2
  until "$@"; do
    [ "$SECONDS" -lt "$deadline" ] || fail "timeout waiting for: $what (status: $(status_field i || true))"
    sleep 5
  done
}

echo "== setup: users, config, agent"
for u in mcpanel mcpanel-mc; do
  if ! id "$u" >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$u"
  fi
done
install -d -m 0755 "$work/bin"
install -m 0755 "$1" "$work/bin/agent"
install -d -m 0750 -o root -g mcpanel "$work/run"
cat >"$work/agent.toml" <<EOF
socket = "$sock"
panel_uid = $(id -u mcpanel)
firewall = "noop"
docker_socket = "/var/run/docker.sock"
instances_dir = "$work/mc"
mc_user = "mcpanel-mc"
EOF
"$work/bin/agent" -config "$work/agent.toml" >"$work/agent.log" 2>&1 &
agent_pid=$!
for _ in $(seq 1 50); do [ -S "$sock" ] && break; sleep 0.2; done
[ -S "$sock" ] || fail "agent socket missing"
# The agent reports Docker as unavailable until its first background check; poll briefly.
docker_ok() { agent http://agent/v1/health | grep -q '"docker":"ok"'; }
for _ in $(seq 1 30); do docker_ok && break; sleep 1; done
docker_ok || fail "agent reports docker unavailable"

echo "== ensure citest (Paper 1.21.11, 2048 MiB) and start"
agent -X POST http://agent/v1/instances/ensure -d '{
  "id": 9001, "name": "citest", "fqdn": "citest.mc.localhost", "revision": 1, "recreate": false,
  "spec": {"type": "paper", "version": "1.21.11", "loader_version": null, "modpack": null,
           "java": "java21", "memory_mib": 2048, "properties": {"motd": "ci acceptance"},
           "curseforge_api_key": null}}'
created() { [ "$(status_field 'i["state"]')" = created ] && [ -z "$(status_field 'i["operation"]')" ]; }
wait_for 900 "container created (image pull)" created
agent -X POST http://agent/v1/instances/power -d '{"id": 9001, "action": "start"}'
running() { [ "$(status_field 'i["state"]')" = running ]; }
wait_for 120 "container running" running
echo "OK: container running"

routes="$work/mc/router/routes.json"
grep -q '"citest.mc.localhost"' "$routes" || fail "route missing in $routes"
grep -q '"mcpanel-mc-9001:25565"' "$routes" || fail "route target missing in $routes"
echo "OK: route present"

routed() { python3 -I scripts/mc-status.py 127.0.0.1 25565 citest.mc.localhost >"$work/status.json" 2>/dev/null; }
wait_for 600 "status ping through mc-router" routed
grep -q 'ci acceptance' "$work/status.json" || fail "status ping answered without our MOTD: $(cat "$work/status.json")"
echo "OK: status ping via citest.mc.localhost: $(head -c 200 "$work/status.json")"

echo "== hardening (docker inspect)"
docker inspect mcpanel-mc-9001 | python3 -I -c '
import json, sys
c = json.load(sys.stdin)[0]
h, cfg = c["HostConfig"], c["Config"]
want_user = sys.argv[1] + ":" + sys.argv[2]
checks = {
    "user": cfg["User"] == want_user,
    "cap_drop_all": h["CapDrop"] == ["ALL"],
    "no_cap_add": not h.get("CapAdd"),
    "no_new_privileges": h["SecurityOpt"] == ["no-new-privileges:true"],
    "not_privileged": h["Privileged"] is False,
    "memory": h["Memory"] == 2048 * 1024 * 1024 * 5 // 4 and h["MemorySwap"] == h["Memory"],
    "pids": h["PidsLimit"] == 1024,
    "restart": h["RestartPolicy"]["Name"] == "unless-stopped",
    "only_data_mount": [m["Destination"] for m in c["Mounts"]] == ["/data"],
    "no_ports": not h.get("PortBindings"),
    "network": list(c["NetworkSettings"]["Networks"]) == ["mcpanel-mc"],
    "label": cfg["Labels"].get("mcpanel.instance") == "9001",
}
bad = [k for k, ok in checks.items() if not ok]
if bad:
    print("hardening failed:", bad, file=sys.stderr)
    sys.exit(1)
print("OK: hardening", sorted(checks))' "$(id -u mcpanel-mc)" "$(id -g mcpanel-mc)" || fail "hardening"
[ "$(stat -c '%U:%G %a' "$work/mc/instances/9001")" = "mcpanel-mc:mcpanel-mc 750" ] || fail "data dir owner/mode: $(stat -c '%U:%G %a' "$work/mc/instances/9001")"
echo "OK: data dir owned by mcpanel-mc (0750)"
if setpriv --reuid=mcpanel --regid=mcpanel --init-groups test -r "$work/mc/instances/9001"; then
  fail "panel user can read instance data"
fi
if setpriv --reuid=mcpanel --regid=mcpanel --init-groups test -w /var/run/docker.sock; then
  fail "panel user can use docker.sock"
fi
echo "OK: panel user has no access to instance data or docker.sock"

echo "== console: container env without ops/whitelist (sub-project 3a)"
docker inspect mcpanel-mc-9001 | python3 -I -c '
import json, sys
env = json.load(sys.stdin)[0]["Config"]["Env"]
gone = ("OPS=", "WHITELIST=", "EXISTING_OPS_FILE=", "EXISTING_WHITELIST_FILE=")
bad = sorted(e.split("=", 1)[0] for e in env if e.startswith(gone))
if bad or "ENABLE_RCON=true" not in env:
    print("container env: unexpected", bad, "or ENABLE_RCON missing", file=sys.stderr)
    sys.exit(1)
print("OK: container env has no OPS/WHITELIST/EXISTING_*_FILE, RCON enabled")' || fail "container env"

echo "== console: rcon"
# The status ping can answer before the RCON listener is up; rcon-cli then exits non-zero and
# the agent answers 503 rcon_unavailable (curl -f fails), so poll.
rcon_ready() { rcon list "$work/rcon.json" 2>/dev/null; }
wait_for 120 "rcon answers" rcon_ready
rcon_output "$work/rcon.json" | grep -q 'players online' || fail "rcon list: $(cat "$work/rcon.json")"
echo "OK: rcon list: $(rcon_output "$work/rcon.json")"

code="$(post_status /v1/instances/rcon '{"id": 9001, "command": "list\nop Mallory"}' "$work/bad.json")"
[ "$code" = 400 ] || fail "rcon with newline: HTTP $code $(cat "$work/bad.json")"
code="$(post_status /v1/instances/rcon '{"id": 9001, "command": "list", "shell": true}' "$work/bad.json")"
[ "$code" = 400 ] || fail "rcon with unknown field: HTTP $code $(cat "$work/bad.json")"
code="$(post_status /v1/instances/logs '{"id": 9001, "tail": 1001, "since": 0}' "$work/bad.json")"
[ "$code" = 400 ] || fail "logs with tail 1001: HTTP $code $(cat "$work/bad.json")"
code="$(post_status /v1/instances/rcon '{"id": 9002, "command": "list"}' "$work/bad.json")"
if [ "$code" != 404 ] || ! grep -q '"code":"not_found"' "$work/bad.json"; then
  fail "rcon on unknown instance: HTTP $code $(cat "$work/bad.json")"
fi
echo "OK: rcon validation (newline, unknown field -> 400; unknown instance -> 404 not_found; tail 1001 -> 400)"

echo "== console: log stream"
logs '{"id": 9001, "tail": 1000, "since": 0}' "$work/logs.ndjson" 5
grep -qi '^content-type: application/x-ndjson' "$work/logs.ndjson.headers" || fail "logs content type: $(cat "$work/logs.ndjson.headers")"
last_ts="$(python3 -I -c '
import json, sys
lines = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
if not lines:
    sys.exit("empty log stream")
for l in lines:
    if set(l) != {"ts", "stream", "line"} or l["stream"] not in ("out", "err") \
            or not isinstance(l["ts"], int) or len(l["line"].encode()) > 8192 or l["line"].endswith(("\n", "\r")):
        sys.exit("bad line: %r" % (l,))
if not any("Done (" in l["line"] for l in lines):
    sys.exit("no \"Done (\" line in %d lines" % len(lines))
print(max(l["ts"] for l in lines))' "$work/logs.ndjson")" || fail "logs stream content"
echo "OK: logs stream (NDJSON, $(wc -l <"$work/logs.ndjson") lines, contains \"Done (\")"

# since = last timestamp + 1 ns must not repeat any line already seen (spec §6.2). New lines
# (e.g. the RCON thread messages of the calls above) may appear; they are newer.
logs "{\"id\": 9001, \"tail\": 0, \"since\": $((last_ts + 1))}" "$work/since.ndjson" 3
python3 -I -c '
import json, sys
last = int(sys.argv[2])
old = [l for l in (json.loads(x) for x in open(sys.argv[1]) if x.strip()) if l["ts"] <= last]
if old:
    sys.exit("since returned %d already seen lines, first: %r" % (len(old), old[0]))' "$work/since.ndjson" "$last_ts" || fail "logs since"
echo "OK: logs since=<last ts>+1 repeats no line"

echo "== console: player lists follow rcon changes (no restart)"
for c in "whitelist add Notch" "op Notch" "ban Notch ci acceptance"; do
  rcon "$c" "$work/rcon.json" || fail "rcon $c: $(cat "$work/rcon.json" 2>/dev/null)"
  echo "rcon $c: $(rcon_output "$work/rcon.json")"
done
players "$work/players.json"
check_players "$work/players.json" '
  any(o["name"] == "Notch" and o["level"] >= 1 for o in d["ops"])
  and "Notch" in [e["name"] for e in d["whitelist"]]
  and any(b["name"] == "Notch" and b["reason"] == "ci acceptance" for b in d["bans"])
  and d["errors"] == []' || fail "player lists after whitelist add/op/ban"
rcon "pardon Notch" "$work/rcon.json" || fail "rcon pardon"
players "$work/players.json"
check_players "$work/players.json" '"Notch" not in [b["name"] for b in d["bans"]]' || fail "player lists after pardon"
echo "OK: whitelist add, op, ban and pardon visible in the player lists without a restart"

echo "== crash: stop the server from inside, Docker must restart it (unless-stopped)"
before="$(status_field 'i["started_at"]')"
docker exec mcpanel-mc-9001 rcon-cli stop >/dev/null
restarted() { a="$(status_field 'i["started_at"]')"; [ "$a" != 0 ] && [ "$a" != "$before" ] && running; }
wait_for 180 "automatic restart" restarted
echo "OK: restarted (started_at $before -> $(status_field 'i["started_at"]'))"

echo "== stopped server: rcon refused, log stream ends, player lists readable"
agent -X POST http://agent/v1/instances/power -d '{"id": 9001, "action": "stop"}'
stopped() { [ "$(status_field 'i["state"]')" = exited ] && [ -z "$(status_field 'i["operation"]')" ]; }
wait_for 120 "container stopped" stopped
code="$(post_status /v1/instances/rcon '{"id": 9001, "command": "list"}' "$work/nr.json")"
if [ "$code" != 409 ] || ! grep -q '"code":"not_running"' "$work/nr.json"; then
  fail "rcon on stopped server: HTTP $code $(cat "$work/nr.json")"
fi
echo "OK: rcon on stopped server -> 409 not_running"
logs '{"id": 9001, "tail": 5, "since": 0}' "$work/stopped.ndjson" 20
[ "$logs_rc" -eq 0 ] || fail "log stream of a stopped container did not end by itself (curl exit $logs_rc)"
[ -s "$work/stopped.ndjson" ] || fail "log stream of a stopped container is empty"
echo "OK: log stream of a stopped container ends by itself"
players "$work/players.json"
check_players "$work/players.json" 'any(o["name"] == "Notch" for o in d["ops"]) and d["errors"] == []' || fail "player lists of a stopped server"
echo "OK: player lists readable while stopped (in-game op kept across the restart)"

echo "== player files controlled by the server: symlinks and FIFOs are refused"
data="$work/mc/instances/9001"
for f in ops.json whitelist.json banned-players.json; do
  mv "$data/$f" "$work/$f.orig"
done
ln -s /etc/shadow "$data/ops.json"                         # absolute symlink to a root-only file
mkfifo "$data/whitelist.json"                              # would block a plain open()
ln -s ../../router/routes.json "$data/banned-players.json" # relative symlink out of the data dir
# --max-time turns a hang on the FIFO into a failure instead of a stuck job.
curl -fsS --max-time 10 --unix-socket "$sock" -H 'Content-Type: application/json' \
  -X POST http://agent/v1/instances/players -d '{"id": 9001}' >"$work/evil.json" || fail "players with planted files: curl exit $?"
check_players "$work/evil.json" '
  sorted(d["errors"]) == ["banned-players.json", "ops.json", "whitelist.json"]
  and d["ops"] == [] and d["whitelist"] == [] and d["bans"] == []' || fail "planted player files not refused"
if grep -q 'root:' "$work/evil.json" || grep -q 'mappings' "$work/evil.json"; then
  fail "player lists leak a file outside the data directory: $(cat "$work/evil.json")"
fi
rm -f "$data/ops.json" "$data/whitelist.json" "$data/banned-players.json"
for f in ops.json whitelist.json banned-players.json; do
  mv "$work/$f.orig" "$data/$f"
done
echo "OK: symlink to /etc/shadow, FIFO and symlink out of the data dir reported in errors, nothing leaked"

echo "== remove"
agent -X POST http://agent/v1/instances/remove -d '{"id": 9001}'
# The instance leaves GET /v1/instances only after the whole remove (container, routes,
# data move) has finished; docker inspect alone would race the routes rewrite.
gone() { [ "$(status_field i)" = absent ] && ! docker inspect mcpanel-mc-9001 >/dev/null 2>&1; }
wait_for 180 "remove finished" gone
if grep -q 'citest.mc.localhost' "$routes"; then
  fail "route still present after remove"
fi
ls -d "$work"/mc/deleted/citest-* >/dev/null 2>&1 || fail "data not moved to deleted/"
echo "OK: container and route removed, data moved to deleted/"
echo "PASS: instance acceptance (agent path)"
```

Hinweise:
- Reihenfolge: Die Konsole wird geprüft, solange der Server nach dem Status-Ping läuft; die Fälle für den gestoppten Server kommen nach der Absturz-Prüfung, die einen laufenden Server braucht. So prüft „player lists readable while stopped“ zugleich, dass ein per RCON vergebener Op einen Container-Neustart übersteht (Spec §10, Kriterium 4, agent-Seite).
- Der Absturz bleibt bei `docker exec … rcon-cli stop`: über den agent würde `stop` die RCON-Verbindung beim Beenden kappen und je nach Zeitpunkt `503 rcon_unavailable` liefern.
- Alle `python3 -I -c`-Ausdrücke stehen fest im Skript; `eval` sieht nur die geparste agent-Antwort (`d`/`i`), nie Eingaben von außen.
- Schlägt `whitelist add Notch` mit „That player does not exist“ fehl, ist die Mojang-API gestört: Job neu starten, nicht den Test aufweichen.

- [ ] **Step 3: Kommentar im CI-Job** – in `.github/workflows/ci.yml`, Job `instances`, den Kommentar über dem Schritt „Instance acceptance“ ersetzen:

```yaml
      # Real agent + Docker + itzg Paper server: routing by hostname, hardening, console
      # (rcon, log stream, player lists, planted player files), automatic restart after a
      # crash, removal.
      - name: Instance acceptance
        run: sudo scripts/ci-instances.sh bin/agent
```

- [ ] **Step 4: Lokal prüfen (nur statisch)**

Run: `shellcheck scripts/ci-instances.sh scripts/ci-acceptance.sh && make check`
Expected: keine Befunde, `make check` grün.

- [ ] **Step 5: Commit, Push, Draft-PR, CI abwarten**

```bash
git add scripts/ci-instances.sh .github/workflows/ci.yml
git commit -m "ci(instances): accept console, log stream and player lists on a real server

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/console-ci-docs
gh pr create --draft --title "ci,docs: sub-project 3a acceptance and docs" --body "$(cat <<'EOF'
Phase C of sub-project 3a (plan docs/superpowers/plans/2026-10-11-console-05-ci-docs.md):
CI acceptance of the console endpoints on a real Paper server, developer docs, manual
acceptance checklist for the test VM.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
gh pr checks <pr> --watch
gh run view <run-id> --log | grep -E '(OK:|PASS:|FAIL:)'
```
Expected: Job `instances` grün; im Log zusätzlich zu den TP2-Zeilen `OK: container env has no OPS/WHITELIST/EXISTING_*_FILE, RCON enabled`, `OK: rcon list: There are 0 of a max of 20 players online:`, `OK: rcon validation (…)`, `OK: logs stream (NDJSON, … lines, contains "Done (")`, `OK: logs since=<last ts>+1 repeats no line`, `OK: whitelist add, op, ban and pardon visible …`, `OK: rcon on stopped server -> 409 not_running`, `OK: log stream of a stopped container ends by itself`, `OK: player lists readable while stopped …`, `OK: symlink to /etc/shadow, FIFO and symlink out of the data dir reported in errors, nothing leaked`, `PASS: instance acceptance (agent path)`. Schlägt eine Prüfung fehl, nennt `FAIL:` die Antwort des agents; dann den agent (Aufgaben 4–7) korrigieren, nicht den Test aufweichen.

---

### Task 20: Doku, Abnahme-Checkliste, CLAUDE.md, README

**Files:**
- Create: `docs/dev/acceptance-tp3a.md`
- Modify: `docs/dev/README.md` (Abschnitte „Minecraft-Server lokal (optional)“, „Produktion: Listen-Adresse und Reverse Proxy“, „CI lokal nachstellen“)
- Modify: `CLAUDE.md` (Spec-Liste, „Architecture“, „Request path and routing“, „Minecraft instances“, „Frontend“)
- Modify: `README.md` („Funktionen“, „Dokumentation“, „Stand“)

**Interfaces:**
- Consumes: Ergebnisse der Aufgaben 1–19.
- Produces: Doku; keine Code-Schnittstellen.

- [ ] **Step 1: Checkliste schreiben** – `docs/dev/acceptance-tp3a.md`

````markdown
# Abnahme Teilprojekt 3a (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (§10).

Automatisch geprüft (CI): Unit- und Integrationstests (agent: Docker-Demultiplexer, Log-Stream, Exec, Spielerdateien; panel: SSE, Neuverbindung, erneute Prüfung, Rechte, Audit, Parser), Docker-Integration (`dockerintegration`), Instanz-Abnahme über den agent (`scripts/ci-instances.sh`: RCON, Log-Stream inkl. `since`, Spielerlisten nach `whitelist add`/`op`/`ban`/`pardon`, `409 not_running`, präparierte Spielerdateien), Playwright gegen den Mock (Konsole, Spieler-Tab).

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client und echter Session.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP3a-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>, Basisdomain `mc.192-168-1-117.sslip.io`.
2. Läuft dort schon ein Server aus TP2 mit Ops oder Whitelist in den Eigenschaften: vor dem Deploy notieren, welche Namen eingetragen waren (Prüfung M unten).
3. Als `admin` anmelden. Gibt es noch keinen Paper-Server `survival`, ihn anlegen (Paper, aktuelle Version, 4 GiB) und starten.
4. Zwei Benutzer anlegen (Benutzer → Neu, Sudo): `freund` mit nur `server.view` auf `survival`, `helfer` mit `server.view` und `server.console` auf `survival`. Beide melden sich einmal an und richten TOTP ein.
5. Minecraft Java Edition auf einem Rechner im selben Netz wie die VM; Server-Adresse `survival.mc.192-168-1-117.sslip.io`. Der eigene Spielername heißt unten `<ich>`.
6. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-panel -f`.

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 2a | Live-Log | als `helfer` Server `survival` → Tab „Konsole“ | die letzten Zeilen des Server-Logs erscheinen sofort, danach neue Zeilen live (z. B. beim Betreten des Servers mit dem Client: `<ich> joined the game`); Verbindungsanzeige „verbunden“ |
| 2b | Befehle | in der Konsole `say hallo` senden, danach `list` | `hallo` erscheint im Spiel-Chat (`[Rcon] hallo` bzw. `[Server] hallo`); `list` zeigt einen Antwort-Block `› list` mit `There are 1 of a max of 20 players online: <ich>` |
| 2c | Neustart ohne Abbruch | Konsole offen lassen, als `admin` in einem zweiten Fenster „Neu starten“ | Anzeige wechselt auf „Server gestoppt“ bzw. „verbinde neu“ und danach wieder „verbunden“; die Startzeilen des neuen Laufs erscheinen; die letzten Zeilen vor dem Neustart stehen genau einmal da (keine Dopplung); kein Neuladen der Seite nötig |
| 2d | RCON-Rauschen | Schalter „RCON-Meldungen ausblenden“ aus- und wieder einschalten | aus: Zeilen `Thread RCON Client … started/shutting down` sichtbar; an: ausgeblendet |
| 2e | `stop`-Hinweis | Hinweis unter der Befehlszeile lesen | Text nennt, dass `stop` den Server neu startet und der Power-Button stoppt |
| 3a | nur `server.view` | als `freund` anmelden, Server `survival` öffnen | Übersicht zeigt „Online-Spieler“ mit `<ich>`; keine Tabs „Konsole“ und „Spieler“ |
| 3b | API-Sperre | als `freund` in den Browser-DevTools (Konsole) ausführen: `await Promise.all(['console/stream','players/lists'].map(p => fetch('/api/servers/<id>/' + p).then(r => r.status)))` und `await fetch('/api/servers/<id>/console', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': '<token>'}, body: '{"command":"list"}'}).then(r => r.status)` (`<id>` aus der URL, `<token>` aus `GET /api/auth/me` → `csrf_token`) | `[403, 403]` und `403` |
| 4a | Whitelist ohne Neustart | als `admin` Einstellungen → „Whitelist aktiv“ einschalten, speichern, „Jetzt neu starten“; danach als `helfer` Tab „Spieler“ → Whitelist → `<ich>` hinzufügen | Toast mit `Added <ich> to the whitelist`; `<ich>` steht in der Liste; Client kann beitreten, ein anderer Account nicht |
| 4b | Op ohne Neustart | Tab „Spieler“ → Online → `<ich>` → Op | Toast `Made <ich> a server operator`; im Spiel funktioniert `/gamemode creative` sofort |
| 4c | Kick, Ban, Entbannen | `<ich>` kicken (Grund „test“), dann bannen (Grund „test“), dann unter „Bans“ entbannen | Kick: Client wird mit „test“ getrennt; Ban: Beitritt abgelehnt, Eintrag mit Grund, „von“, „seit“ in „Bans“; Entbannen: Eintrag weg, Beitritt wieder möglich |
| 4d | Im Spiel vergebene Rechte bleiben | im Spiel (als Op) `/op <zweiter Account>` bzw. `/whitelist add <Name>`; Tab „Spieler“ neu laden; dann Server neu starten | Einträge erscheinen im Tab; nach dem Neustart unverändert vorhanden |
| 4e | gestoppter Server | Server stoppen, Tab „Spieler“ öffnen | Listen sichtbar, Ändern-Knöpfe gesperrt mit Hinweis; in der Konsole ist die Befehlszeile gesperrt |
| 5a | Recht entzogen | `helfer` hat die Konsole offen; `admin` entzieht `helfer` unter Benutzer → Rechte `server.console` | innerhalb von 30 s endet der Stream (Anzeige „keine Berechtigung“ bzw. Tab verschwindet nach dem nächsten Laden); `journalctl` zeigt keine Fehler |
| 5b | Session beendet | Recht wieder geben; `helfer` öffnet die Konsole; `admin` beendet unter Sicherheit → Sessions die Session von `helfer` | innerhalb von 30 s endet der Stream; `helfer` wird beim nächsten Request zur Anmeldung geleitet |
| 6 | Audit | als `admin` Audit-Log öffnen, Filter Aktion `server.console_command` bzw. `server.player_action`; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | jeder Befehl aus 2b mit Text und Ergebnis, jede Aktion aus 4a–4c mit Aktion, Name und Grund; `verify` meldet OK |
| M | Migration aus TP2 (nur wenn Vorbereitung 2 zutraf) | nach dem Deploy den alten Server ansehen; „Jetzt neu starten“; Tab „Spieler“ | vor dem Neustart „Neustart nötig“; danach stehen die notierten Ops und (bei aktiver Whitelist) Whitelist-Namen im Tab; in `ssh claude@192.168.1.117 "sudo docker inspect mcpanel-mc-<id> --format '{{json .Config.Env}}'"` fehlen `OPS`, `WHITELIST`, `EXISTING_OPS_FILE`, `EXISTING_WHITELIST_FILE` |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px breit): Konsole bedienbar, Befehlszeile bleibt unten sichtbar, Spieler-Tab ohne horizontales Scrollen.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
````

- [ ] **Step 2: `docs/dev/README.md` ergänzen**

Im Abschnitt „Minecraft-Server lokal (optional)“ nach dem Aufzählungspunkt „Aufräumen: …“ einen Punkt anhängen:

```markdown
- Konsole und Spieler-Tab brauchen einen laufenden Container. Ohne Docker antworten Log-Stream, Befehle und Spielerlisten mit `503 docker_unavailable`, die Karte „Online-Spieler“ zeigt „nicht verfügbar“. Die UI-Vorschau (`make preview`) und `npm run dev:mock` liefern dagegen simulierte Log-Zeilen (alle 2 s eine neue, mit ANSI-/`§`-Farben und RCON-Rauschen), Befehlsantworten und Spielerlisten.
```

Den letzten Satz des Abschnitts ersetzen:

```markdown
Funktionstests gegen echte Server (Clients, Modpacks, Neustarts, Konsole) laufen getrennt auf der Test-VM (nächster Abschnitt); die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus) und `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler).
```

Im Abschnitt „Produktion: Listen-Adresse und Reverse Proxy“ nach dem Punkt „TCP bleibt möglich …“ einfügen:

```markdown
- Die Konsole streamt das Server-Log als Server-Sent Events (`GET /api/servers/{id}/console/stream`, `text/event-stream`, Heartbeat-Kommentar alle 15 s, ein Stream pro offenem Konsolen-Tab, höchstens 4 pro Benutzer). Caddy reicht SSE ohne Pufferung und ohne Zusatzkonfiguration durch. Ein anderer Proxy darf die Antwort nicht puffern (das panel setzt `X-Accel-Buffering: no` für nginx) und braucht ein Lese-Timeout über 15 s.
```

In der Tabelle „CI lokal nachstellen“ die Zeile `instances` ersetzen:

```markdown
| `instances` | nur in der CI: `sudo scripts/ci-instances.sh bin/agent` (echter agent + Docker + Paper-Server: Routing, Härtung, Konsole mit RCON, Log-Stream und Spielerlisten, präparierte Spielerdateien, Absturz-Neustart, Löschen; verändert den Host) |
```

- [ ] **Step 3: `CLAUDE.md` aktualisieren**

In der Liste der Projekt-Doku die Spec-Zeilen ersetzen durch:

```markdown
- `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`: architecture, security requirements and the roadmap of sub-projects (1 foundation, 2 instances and 3a console are done).
- `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md`: Minecraft instances (sub-project 2); manual acceptance checklist in `docs/dev/acceptance-tp2.md`.
- `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md`: console, live log and player management (sub-project 3a); manual acceptance checklist in `docs/dev/acceptance-tp3a.md`.
```

Im Abschnitt „Architecture“ den agent-Absatz um diesen Satz ergänzen (direkt vor „In dev mode Docker is off …“):

```markdown
For the console it streams container logs (Docker `logs?follow`, sent to the panel as NDJSON), runs `rcon-cli` in the container via Docker exec (argv, no shell) and reads an instance's player list files through `os.Root` with `O_NOFOLLOW|O_NONBLOCK` (regular files ≤ 1 MiB only): the data directory is controlled by the container, so the root agent never follows a path out of it.
```

Im Abschnitt „Request path and routing“ die Paketliste ersetzen:

```markdown
`cmd/panel/app.go` (`buildApp`) is the only place that wires the packages together. Each feature package (`auth`, `defense`, `notify`, `users`, `servers`, `console`, `audit/auditapi`) has a `Handler` with a `Routes(api *rbac.Router)` method.
```

Im Abschnitt „Minecraft instances“ vor dem letzten Punkt („Docker integration …“) einfügen und den letzten Punkt ersetzen:

```markdown
- Ops, whitelist and bans belong to the server itself (`ops.json`, `whitelist.json`, `banned-players.json` in its data directory): the panel never sets `OPS`/`WHITELIST`; the Players tab reads the files through the agent and changes them through RCON. `whitelist_enabled` stays an instance property.
- `internal/console` serves the live log as Server-Sent Events (`/servers/{id}/console/stream`: per-write deadlines via `http.ResponseController` instead of the server's `WriteTimeout`, reconnects to the agent across container restarts, re-checks session and permission every 30 s) and console commands and player actions as normal POST requests (CSRF, 30/min per user, audited as `server.console_command`/`server.player_action`). Player actions map an enum to fixed RCON commands; user text never becomes a command. `server.view` sees online players, `server.console` everything else.
- Docker integration and the instance acceptance (`scripts/ci-instances.sh`, including the console endpoints) run only in CI.
```

Im Abschnitt „Frontend (`web/`)“ am Ende anhängen:

```markdown
The console tab reads the log with `EventSource` and renders it with `@tanstack/react-virtual`; ANSI and `§` colour codes become spans, never HTML.
```

- [ ] **Step 4: `README.md` aktualisieren**

Unter „Funktionen“ nach dem Punkt „**Minecraft-Server** …“ einfügen:

```markdown
- **Konsole und Spieler:** Live-Log im Browser (läuft über Neustarts weiter), Befehle per RCON, Online-Spieler, Ops, Whitelist und Bans ohne Neustart pflegen (Kick, Ban, Entbannen, Op, Whitelist). Was im Spiel vergeben wird, bleibt erhalten.
```

Den Punkt „**Rechte:** …“ ersetzen:

```markdown
- **Rechte:** Admins und Benutzer mit Rechten pro Server (`server.view`, `server.power`, `server.console`).
```

In der Tabelle „Dokumentation“ nach der Zeile `acceptance-tp2.md` bzw. der Instanzen-Spec je eine Zeile einfügen:

```markdown
| [`docs/dev/acceptance-tp3a.md`](docs/dev/acceptance-tp3a.md) | manuelle Abnahme von Konsole, Logs und Spieler-Verwaltung auf der Test-VM |
| [`docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md`](docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md) | Konsole, Live-Log, Spieler-Verwaltung (RCON, SSE) |
```

In der Tabelle „Stand“ die Zeile 3 ersetzen durch:

```markdown
| 3a | Live-Betrieb: Konsole, Logs, Spieler | fertig |
| 3b | Live-Betrieb: Dateien, Plugins/Mods | offen |
```

- [ ] **Step 5: Prüfen, committen, CI**

Run: `make check`
Expected: grün (nur Doku geändert).

```bash
git add docs/dev/acceptance-tp3a.md docs/dev/README.md CLAUDE.md README.md
git commit -m "docs: console, logs and players for dev, CI, CLAUDE.md and the acceptance checklist

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks <pr> --watch
```
Expected: alle Jobs grün.

---

### Task 21: Gesamt-Review, Abnahme, Abschluss

**Files:**
- Modify: nach Befund (Review-Fixes jeweils in den Dateien der betroffenen Aufgabe)
- Modify: `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (Angleichung an Review-Fixes; Status erst nach Merge)
- Modify: `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (Roadmap-Zeile 3a: „(umgesetzt)“)
- Delete: `docs/superpowers/plans/2026-10-11-console-0*.md` (erst nach Merge und Abnahme)

**Interfaces:**
- Consumes: alle vorherigen Aufgaben (A, B1, B2, B3 auf `main`, Aufgaben 19–20 auf `feat/console-ci-docs`).
- Produces: behobene Review-Befunde, grüne CI auf dem Gesamtstand, abgenommenes Teilprojekt 3a.

- [ ] **Step 1: Security-Review** – Skill `security-review` auf `feat/console-ci-docs` gegen den Stand vor Phase A (`git merge-base origin/main <erster TP3a-Merge>^`, d. h. der Commit vor dem Merge von `feat/console-contracts`). Schwerpunkte, in dieser Reihenfolge:
  1. Spielerdateien (Aufgabe 6): `os.Root` auf dem Datenverzeichnis, `O_NOFOLLOW|O_NONBLOCK`, `fstat` auf regulär und ≤ 1 MiB, Lesen begrenzt; kein `os.Open`/`os.ReadFile` auf Pfade unter `instances/<id>` an anderer Stelle des agents.
  2. Befehlsweg: `ValidateCommand` in panel **und** agent, exec mit argv ohne Shell, feste `User`/`WorkingDir`/`Env`, keine weiteren exec-Aufrufe im Docker-Client; Spieler-Aktionen bauen Befehle nur aus Enum + validiertem Namen/Grund.
  3. Rechte und Session: alle fünf Routen mit der Regel aus V4; der Stream prüft alle 30 s erneut und verlängert die Session nicht; `last_error`/RCON-Fehlertexte enthalten keine Secrets (RCON-Passwort, Pfade) für Nicht-Admins.
  4. Ressourcen: Stream-Limits in agent (16) und panel (4 pro Benutzer) zählen bei Abbruch herunter; Kontexte beenden Docker-Verbindungen; Zeilen-/Ausgabe-/Frame-Grenzen.
  5. Frontend: kein `dangerouslySetInnerHTML`, Log-Text nur als Text-Spans, `EventSource` nur Same-Origin.
  Jeden Befund mit `superpowers:receiving-code-review` bewerten.

- [ ] **Step 2: Code-Review** – Skill `superpowers:requesting-code-review` für den Gesamtstand (gleiche Basis wie Step 1), mit Spec, Übersicht und den Review-Focus-Punkten 1–5 der Übersicht als Prüfauftrag. Befunde ebenfalls mit `superpowers:receiving-code-review` bewerten.

- [ ] **Step 3: Befunde beheben (rot/grün wie in TP2)** – auf `feat/console-ci-docs`, pro Bereich zwei Commits:

```bash
# 1. Tests, die jeden berechtigten Befund nachstellen (rot in der CI)
git commit -m "test(agent): cover whole-feature review findings of TP3a (red)

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
# 2. Fixes (grün)
git commit -m "fix(agent): address whole-feature review findings of TP3a

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

Gleiches Muster für `console`/`web` (`test(console,web): …`, `fix(console,web): …`). Nach jedem Paar `make check`, Push, `gh pr checks <pr> --watch`. Befunde, die nicht berechtigt sind, mit Begründung im PR-Kommentar festhalten (`gh pr comment <pr> --body …`), nicht stillschweigend übergehen.

- [ ] **Step 4: Spec angleichen** – Weicht die Umsetzung nach den Fixes von der Spec ab (z. B. geänderte Grenze, zusätzlicher Fehlercode), die Spec im selben PR anpassen:

```bash
git add docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md
git commit -m "docs(spec): bring the TP3a spec in line with the review fixes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 5: Gesamtstand in der CI, Übergabe**

```bash
gh pr ready <pr>
gh pr checks <pr> --watch
```
Expected: alle Jobs grün (`go`, `web`, `openapi`, `nftables` inkl. Docker-Integration, `instances`, `e2e`, `build`). Dann dem Nutzer melden: PR-Link, Zusammenfassung der Review-Befunde, Hinweis „Mergen durch dich; danach `make deploy-testvm` und Abnahme nach `docs/dev/acceptance-tp3a.md`“. Nicht selbst mergen.

- [ ] **Step 6: Abnahme auf der Test-VM begleiten** – Nach dem Merge durch den Nutzer: `make deploy-testvm` (Claude darf auf der VM alles ändern; ssh nur mit abgeschalteter Sandbox), danach prüfen, dass beide Dienste laufen:

```bash
ssh claude@192.168.1.117 'systemctl is-active mcpanel-agent mcpanel-panel caddy; curl -s --unix-socket /run/mc-panel/agent.sock http://agent/v1/health'
```
Expected: dreimal `active`, Health mit `"docker":"ok"`. Die Kriterien der Checkliste prüft der Nutzer selbst (Browser, Minecraft-Client). Befunde als Issue → Fix auf eigenem `fix/…`-Branch mit PR.

- [ ] **Step 7: Abschluss nach erfolgreicher Abnahme** – auf neuem Branch `docs/tp3a-done` von `origin/main`:

```bash
git fetch origin
git switch -c docs/tp3a-done origin/main
gh pr list --state merged --search "console in:title" --json number,title,mergedAt
```

In `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` die Status-Zeile ersetzen durch `- **Status:** umgesetzt (PRs #<erste>–#<letzte>, <Merge-Datum der letzten, JJJJ-MM-TT>)` mit den Nummern aus der Liste. In `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` in der Roadmap-Zeile 3a hinter „Live-Betrieb: Konsole“ ` (umgesetzt)` ergänzen. Dann:

```bash
git rm docs/superpowers/plans/2026-10-11-console-0*.md
git add docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md
make check
git commit -m "docs: drop the completed sub-project 3a plans, mark the spec done

All plan tasks are merged; git history keeps the plans. The spec stays as the
reference and now says so in its status line.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin docs/tp3a-done
gh pr create --title "docs: close sub-project 3a" --body "$(cat <<'EOF'
Drops the completed TP3a plans (git history keeps them) and marks the spec and the roadmap row done.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
gh pr checks <pr> --watch
```

Teammates herunterfahren und ihre Worktrees entfernen (`git worktree remove ../mc-panel-wt/<branch>` für `feat/console-agent`, `feat/console-panel`, `feat/console-frontend`); Remote-Branches erst löschen, wenn der Nutzer es sagt.

## Self-Review (dieser Datei)

- Spec §8 (Mac nur `make check`, CI, manuelle Abnahme auf der Test-VM) → Aufgaben 19 (CI), 20 (Checkliste, Doku), 21 (Abnahme begleiten).
- Spec §9 Zeile „`instances` (CI)“: `list` über den agent, Log-Stream mit `Done (`, `players` nach `whitelist add`/`op`, Container ohne `OPS`/`WHITELIST`/`EXISTING_*_FILE` → Aufgabe 19 Step 2; zusätzlich `ban`/`pardon`, `since`, `409 not_running`, Ende des Streams beim gestoppten Container, Validierung, präparierte Spielerdateien (Review Focus 1 der Übersicht auf echtem Linux).
- Spec §10: Kriterium 1 → CI-Läufe in Aufgaben 19–21; agent-Seite von 2 und 4 → Aufgabe 19; 2–6 manuell → `docs/dev/acceptance-tp3a.md` (Zeilen 2a–6), plus Migration (Spec §3.1) als Zeile M.
- Nichts in dieser Datei startet Container oder braucht root auf dem Mac (CLAUDE.md); das Skript räumt weiterhin nur Container mit `mcpanel.instance`/`mcpanel.router`-Label und das Netz `mcpanel-mc` ab und stellt die präparierten Spielerdateien vor dem Löschen wieder her.
- Mergen, Tags und Löschen von Remote-Branches nur durch bzw. nach Freigabe des Nutzers.
