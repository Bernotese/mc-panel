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
  local code
  code="$(post_status /v1/instances/rcon \
    "$(python3 -I -c 'import json, sys; print(json.dumps({"id": 9001, "command": sys.argv[1]}))' "$1")" "$2")" || return 1
  [ "$code" = 200 ] || { echo "rcon HTTP $code" >&2; return 1; }
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
if not eval("(" + sys.argv[2] + "\n)", {}, {"d": d}):
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
code="$(post_status /v1/instances/rcon '{"id": 9001, "command": "--help"}' "$work/bad.json")"
[ "$code" = 400 ] || fail "rcon with leading dash: HTTP $code $(cat "$work/bad.json")"
code="$(post_status /v1/instances/rcon '{"id": 9001, "command": "list", "shell": true}' "$work/bad.json")"
[ "$code" = 400 ] || fail "rcon with unknown field: HTTP $code $(cat "$work/bad.json")"
code="$(post_status /v1/instances/logs '{"id": 9001, "tail": 1001, "since": 0}' "$work/bad.json")"
[ "$code" = 400 ] || fail "logs with tail 1001: HTTP $code $(cat "$work/bad.json")"
code="$(post_status /v1/instances/rcon '{"id": 9002, "command": "list"}' "$work/bad.json")"
if [ "$code" != 404 ] || ! grep -q '"code":"not_found"' "$work/bad.json"; then
  fail "rcon on unknown instance: HTTP $code $(cat "$work/bad.json")"
fi
echo "OK: rcon validation (newline, leading dash, unknown field -> 400; unknown instance -> 404 not_found; tail 1001 -> 400)"

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
# (e.g. the RCON thread messages of the calls above) may appear; they are newer. tail 1000 is
# sent along on purpose: since must win over tail, otherwise the whole log would come back. A
# marker written after last_ts must show up, so an ignored or swallowing since fails too.
rcon "say tp3a-since-marker" "$work/rcon.json" || fail "rcon say marker"
logs "{\"id\": 9001, \"tail\": 1000, \"since\": $((last_ts + 1))}" "$work/since.ndjson" 3
python3 -I -c '
import json, sys
last = int(sys.argv[2])
lines = [json.loads(x) for x in open(sys.argv[1]) if x.strip()]
old = [l for l in lines if l["ts"] <= last]
if old:
    sys.exit("since returned %d already seen lines, first: %r" % (len(old), old[0]))
if not any("tp3a-since-marker" in l["line"] and l["ts"] > last for l in lines):
    sys.exit("since stream lacks the marker line written after the cut (%d lines)" % len(lines))' "$work/since.ndjson" "$last_ts" || fail "logs since"
echo "OK: logs since=<last ts>+1 (with tail 1000) repeats no line and delivers the newer marker"

echo "== console: player lists follow rcon changes (no restart)"
# whitelist add/op/ban Notch need a Mojang lookup (ONLINE_MODE=true): the runner needs internet.
for c in "whitelist add Notch" "op Notch" "ban Notch ci acceptance"; do
  rcon "$c" "$work/rcon.json" || fail "rcon $c failed (Mojang lookup?): $(cat "$work/rcon.json" 2>/dev/null)"
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
