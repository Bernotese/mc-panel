#!/usr/bin/env bash
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
set -euo pipefail
cd "$(dirname "$0")/.."

if [ "$#" -ne 3 ]; then
  echo "usage: scripts/ci-instances.sh AGENT_BINARY FILES_BINARY PANEL_BINARY" >&2
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
fsock="$work/frun/files.sock"
fo="$work/fout"
files_pid=""
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

cleanup() {
  if [ -n "$panel_pid" ]; then
    kill "$panel_pid" 2>/dev/null || true
    wait "$panel_pid" 2>/dev/null || true
  fi
  if [ -n "$files_pid" ]; then
    kill "$files_pid" 2>/dev/null || true
    wait "$files_pid" 2>/dev/null || true
  fi
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
  echo "--- files log" >&2
  cat "$work/files.log" >&2 || true
  echo "--- panel log" >&2
  cat "$work/panel.log" >&2 || true
  if [ -n "$psrv" ]; then
    docker logs --tail 80 "mcpanel-mc-$psrv" >&2 2>/dev/null || true
  fi
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
[ -d "$work/mc/router" ] && [ -d "$work/mc/deleted" ] || fail "router/ or deleted/ missing (precondition of the checks below)"
# curl exit 52 (empty reply) or 56 (reset): the service closed the connection of the wrong peer.
nobody_rc=0
setpriv --reuid=nobody --regid=nogroup --clear-groups \
  curl -fsS --max-time 5 --unix-socket "$fsock" http://files/v1/health >/dev/null 2>&1 || nobody_rc=$?
case "$nobody_rc" in
  0) fail "user nobody reached the files service" ;;
  52 | 56) ;;
  *) fail "user nobody: curl exit $nobody_rc, expected 52 or 56 (connection refused by the peer check)" ;;
esac
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
is_error "$fo/m.json" "$code" 400 invalid || fail "rename a directory into itself: HTTP $code $(cat "$fo/m.json")"
[ -d "$data/ci-dir" ] && [ -f "$data/ci-dir/moved.txt" ] || fail "rejected rename changed ci-dir"
code="$(fpost /v1/delete '{"id": 9001, "path": "ci-dir"}' "$fo/m.json")"
[ "$code" = 204 ] || fail "delete: HTTP $code $(cat "$fo/m.json")"
[ ! -e "$data/ci-dir" ] || fail "deleted directory still there"
code="$(fpost /v1/delete '{"id": 9001, "path": ""}' "$fo/m.json")"
is_error "$fo/m.json" "$code" 400 invalid || fail "delete the root: HTTP $code $(cat "$fo/m.json")"
[ -d "$data" ] && [ -f "$data/server.properties" ] || fail "rejected delete of the root removed data"
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
is_error "$fo/s.dl" "$code" 400 not_regular || fail "download symlink: HTTP $code $(cat "$fo/s.dl")"
if grep -q 'root:' "$fo/s.dl" "$fo/s.json"; then fail "symlink leaked /etc/shadow"; fi
code="$(fpost /v1/list '{"id": 9001, "path": "ci-router"}' "$fo/s.json")"
is_error "$fo/s.json" "$code" 400 not_regular || fail "list through a symlinked directory: HTTP $code $(cat "$fo/s.json")"
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
# Remove what the files checks planted, so the restart below runs on the original data.
rm -f "$data/plugins/ci-plugin.jar" "$data/ci-new.txt"
sed -i '/^# ci-edit$/d' "$data/bukkit.yml"
echo "OK: directory zip without symlinks, hidden or temp files, server.properties redacted"

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
[ ! -e "$work/mc/instances/evil.txt" ] && [ ! -e "$data/evil.txt" ] && [ ! -e "$data/ok.txt" ] ||
  fail "a refused archive wrote files (instances/evil.txt, evil.txt or ok.txt)"
[ ! -e /tmp/ci-abs-evil.txt ] || fail "absolute zip path was written"
no_tmp_left || fail "refused archives left a .mcpanel-tmp-* directory"
rm -rf "$data"/world-ci "$data"/world-ci.zip "$data"/{slip,abs,link,hidden,many,huge}.zip
echo "OK: zip slip, absolute path, symlink entry, hidden name -> 400 invalid; 1001 entries, 101 MiB declared -> 413 too_large; no job, nothing written"

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

echo "PASS: instance acceptance (agent, files and plugins path)"
