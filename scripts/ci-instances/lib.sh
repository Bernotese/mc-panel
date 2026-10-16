# shellcheck shell=bash
# shellcheck disable=SC2154 # agent_bin, files_bin and panel_bin come from scripts/ci-instances.sh
# Shared part of the instance acceptance, sourced by scripts/ci-instances.sh before the shard:
# state, cleanup, helpers, and the setup functions setup_agent_files (agent as root, files
# service as mcpanel-mc) and setup_panel_server (panel in production mode as mcpanel, signed in
# with TOTP and sudo, the Paper server "ciplugins" created and running through the panel API).

# Time scale of the acceptance panel build (internal/timescale, -tags acceptance): monitoring
# passes, alert windows and the backup tick run this many times faster; waits below divide by it.
ts="${MCPANEL_ACCEPTANCE_TIMESCALE:-1}"
case "$ts" in '' | *[!0-9]* | 0*)
  echo "ci-instances: MCPANEL_ACCEPTANCE_TIMESCALE=$ts is not a positive integer" >&2
  exit 2
  ;;
esac

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
p_totp_step=0 # last TOTP step the panel accepted (replay protection: the next must be greater)
pcode=""      # HTTP status of the last papi call
psrv=""       # server id of the plugins test server (assigned by the panel)
pdata=""      # its data directory
pjob=""       # id of the plugin job being waited for
pjob_state="" # its last seen state
p_before=""   # started_at (Unix ms) before a restart
p_started=""  # started_at (Unix ms) after the last restart
# Backups part (sub-project 4), on the plugins server.
bjob=""       # id of the backup job being waited for
bjob_state="" # its last seen state
restic_bin="/usr/local/bin/restic"
# Monitoring part (sub-project 5), on the plugins server before the backups part.
mo="$work/mout"     # monitoring outputs
mon_nft=""          # set while the ping-blocking nftables table exists
mon_puts=0          # successful PUT /monitoring/settings (audit check)
mon_before=""       # started_at (Unix ms) before a crash
egress_ns=""        # set while the network namespace of the private-network client exists
routes="$work/mc/router/routes.json" # mc-router config written by the agent

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
  if [ -n "$mon_nft" ]; then
    nft delete table inet mcpanel_citest 2>/dev/null || true
  fi
  if [ -n "$egress_ns" ]; then
    ip netns del mcpcitest 2>/dev/null || true
    ip link del mcpcit0 2>/dev/null || true
  fi
  ids="$(docker ps -aq --filter label=mcpanel.instance; docker ps -aq --filter label=mcpanel.router)"
  if [ -n "$ids" ]; then
    # shellcheck disable=SC2086 # one container id per word
    docker rm -f $ids >/dev/null 2>&1 || true
  fi
  docker network rm mcpanel-mc >/dev/null 2>&1 || true
  nft delete table inet mcpanel 2>/dev/null || true # the agent's jail and egress filter
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

# totp <base32 secret> <step>: RFC 6238 code (SHA-1, 6 digits, 30 s) of an absolute time step
# (Unix seconds / 30).
totp() {
  python3 -I -c '
import base64, hashlib, hmac, struct, sys
s = sys.argv[1].rstrip("=").upper()
key = base64.b32decode(s + "=" * (-len(s) % 8))
mac = hmac.new(key, struct.pack(">Q", int(sys.argv[2])), hashlib.sha1).digest()
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
  p_before="$(inst_field "$psrv" 'i["started_at"]')" || fail "restart ($1): agent instance list"
  papi POST "/servers/$psrv/power" "$po/power.json" '{"action": "restart"}'
  [ "$pcode" = 202 ] || fail "restart ($1): HTTP $pcode $(cat "$po/power.json")"
  pwait_for 240 "restart ($1)" p_restarted
  pwait_for 240 "rcon after restart ($1)" p_rcon_ready
  p_started="$(inst_field "$psrv" 'i["started_at"]')" || fail "restart ($1): agent instance list"
}

# check_plugins <listed names, comma-separated> <absent names, comma-separated>: runs "plugins"
# through the panel console (RCON) and checks the answer: listed names must appear and must not
# be marked disabled (red); absent names must not appear. rcon-cli turns Paper's colour codes
# into ANSI escapes (green \e[32m active, red \e[31m disabled); raw § codes are handled too.
check_plugins() {
  papi POST "/servers/$psrv/console" "$po/plugins.json" '{"command": "plugins"}'
  [ "$pcode" = 200 ] || fail "console plugins: HTTP $pcode $(cat "$po/plugins.json")"
  python3 -I -c '
import json, re, sys
raw = json.load(open(sys.argv[1]))["output"]
plain = re.sub("\x1b\\[[0-9;]*m|§.", "", raw)
def listed(n):
    return re.search(r"(?<![A-Za-z0-9_])" + re.escape(n) + r"(?![A-Za-z0-9_])", plain) is not None
def red(n):
    return re.search("(§c|\x1b\\[(?:0;)?(?:31|91)m)" + re.escape(n) + r"(?![A-Za-z0-9_])", raw) is not None
want = [n for n in sys.argv[2].split(",") if n]
gone = [n for n in sys.argv[3].split(",") if n]
bad = [n for n in want if not listed(n) or red(n)] + [n for n in gone if listed(n)]
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

# p_next_step: the next TOTP step the panel accepts: the current one, or the one after the last
# used step (the panel accepts one step ahead of its clock); waits only when that is further.
p_next_step() {
  local step=$((EPOCHSECONDS / 30))
  [ "$step" -gt "$p_totp_step" ] || step=$((p_totp_step + 1))
  while [ "$step" -gt $((EPOCHSECONDS / 30 + 1)) ]; do sleep 1; done
  echo "$step"
}

# p_sudo: fresh sudo by TOTP before restore, delete and settings changes (the window from the
# sign-in may have run out). A code of an already used step is refused (replay protection), so
# it takes the next unused step (p_next_step); on a refusal it waits for the next step once.
# Takes over the rotated session and CSRF token.
p_sudo() {
  local step
  step="$(p_next_step)"
  papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" "$step")\"}"
  if [ "$pcode" != 200 ]; then
    sleep 31
    step="$(p_next_step)"
    papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" "$step")\"}"
  fi
  [ "$pcode" = 200 ] || fail "sudo: HTTP $pcode $(cat "$po/sudo.json")"
  p_totp_step="$step"
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
  [ "$bjob_state" = "done" ] || [ "$bjob_state" = failed ]
}

# b_sched_finished: true once the current job is a scheduled one that is done or failed.
b_sched_finished() {
  papi GET "/servers/$psrv/backups/job" "$po/bjob.json"
  [ "$pcode" = 200 ] || fail "backup job: HTTP $pcode $(cat "$po/bjob.json")"
  bjob_state="$(jget "$po/bjob.json" '"none" if d["job"] is None or d["job"]["trigger"] != "scheduled" else d["job"]["state"]')"
  [ "$bjob_state" = "done" ] || [ "$bjob_state" = failed ]
}

# b_wait <label> <seconds>: waits for bjob and fails the run unless it ended "done".
b_wait() {
  pwait_for "$2" "backup job ($1)" b_job_finished
  [ "$bjob_state" = "done" ] || fail "backup job ($1) failed: $(cat "$po/bjob.json")"
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

# mon_settings <json body>: PUT /monitoring/settings with a fresh sudo (the route needs sudo);
# fails on != 200, counts the successful updates for the audit check.
mon_settings() {
  p_sudo
  papi PUT /monitoring/settings "$mo/settings.json" "$1"
  [ "$pcode" = 200 ] || fail "monitoring settings: HTTP $pcode $(cat "$mo/settings.json")"
  mon_puts=$((mon_puts + 1))
}

# mon_firing <kind> [server id]: true if the alert is in the firing list of /monitoring/settings
# (server_id null for host.disk_low). Never call it in a subshell (papi).
mon_firing() {
  papi GET /monitoring/settings "$mo/alerts.json"
  [ "$pcode" = 200 ] || fail "monitoring settings: HTTP $pcode $(cat "$mo/alerts.json")"
  python3 -I -c '
import json, sys
d = json.load(open(sys.argv[1]))
want = int(sys.argv[3]) if len(sys.argv) > 3 else None
sys.exit(0 if any(a["kind"] == sys.argv[2] and a["server_id"] == want for a in d["alerts"]) else 1)' \
    "$mo/alerts.json" "$@"
}
mon_not_firing() { ! mon_firing "$@"; }

# mon_up: the overview shows the plugins server as reachable (last ping answered).
mon_up() {
  papi GET /monitoring/overview "$mo/overview.json"
  [ "$pcode" = 200 ] || fail "overview: HTTP $pcode $(cat "$mo/overview.json")"
  [ "$(jget "$mo/overview.json" 'any(s["id"] == int(a[0]) and s["up"] is True for s in d["servers"])' "$psrv")" = True ]
}

# notified_count <event type>: delivery attempts of that event type in the panel log. The CI
# notifier points at https://ntfy.invalid (never resolves, RFC 6761), so every event that
# reaches the dispatcher ends in exactly one "notify: delivery failed" line (JSON log, after
# the retries).
notified_count() {
  python3 -I -c '
import json, sys
n = 0
for line in open(sys.argv[1], errors="replace"):
    try:
        e = json.loads(line)
    except ValueError:
        continue
    if isinstance(e, dict) and e.get("msg") == "notify: delivery failed" and e.get("type") == sys.argv[2]:
        n += 1
print(n)' "$work/panel.log" "$1"
}
# notified <event type> <count>: the panel log has at least <count> delivery attempts of it.
notified() { [ "$(notified_count "$1")" -ge "$2" ]; }

# p_crash: stops Paper from inside (rcon-cli stop) and waits until Docker restarted it and RCON
# answers again (restart policy unless-stopped, same path as the crash test of sub-project 2).
p_restarted_since() {
  local a
  a="$(inst_field "$psrv" 'i["started_at"]')"
  [ "$a" != 0 ] && [ "$a" != "$mon_before" ] && p_running
}
p_crash() {
  mon_before="$(inst_field "$psrv" 'i["started_at"]')" || fail "crash: agent instance list"
  docker exec "mcpanel-mc-$psrv" rcon-cli stop >/dev/null
  pwait_for 300 "plugins server restarted by Docker" p_restarted_since
  pwait_for 300 "rcon on the plugins server after the crash" p_rcon_ready
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

# The function bodies below are not indented: they hold heredocs and python -c programs.

# setup_agent_files: users, the agent as root (nftables firewall, Docker) and the files service
# as mcpanel-mc with restic.
setup_agent_files() {
echo "== setup: users, config, agent"
for u in mcpanel mcpanel-mc; do
  if ! id "$u" >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin "$u"
  fi
done
install -d -m 0755 "$work/bin"
install -m 0755 "$agent_bin" "$work/bin/agent"
install -d -m 0750 -o root -g mcpanel "$work/run"
# nftables (not noop): the container egress filter (default container_egress_deny) is checked below.
cat >"$work/agent.toml" <<EOF
socket = "$sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
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
[ -x "$restic_bin" ] || fail "restic missing at $restic_bin (CI step \"Install restic\": scripts/install-restic.sh)"
install -m 0755 "$files_bin" "$work/bin/files"
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
restic_binary = "$restic_bin"
[limits]
# 64 MiB: headroom for real plugin jars (ViaVersion is about 6.5 MB); the 413 check uploads 65 MiB.
max_upload_bytes = 67108864
max_extract_bytes = 104857600
max_extract_entries = 1000
reserve_bytes = 1048576
max_text_bytes = 5242880
max_list_entries = 10000
EOF
chmod 0644 "$work/files.toml"
[ "$(stat -c '%U:%G %a' "$work/mc/instances")" = "root:mcpanel-mc 750" ] ||
  fail "instances/ owner/mode: $(stat -c '%U:%G %a' "$work/mc/instances") (want root:mcpanel-mc 750)"
[ "$(stat -c '%U:%G %a' "$work/mc/backups")" = "mcpanel-mc:mcpanel-mc 700" ] ||
  fail "backups/ owner/mode: $(stat -c '%U:%G %a' "$work/mc/backups" 2>&1) (want mcpanel-mc:mcpanel-mc 700, agent ensureLayout)"
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
fcurl -f http://files/v1/backups/status >"$fo/bstatus.json" || fail "backups status as panel user"
python3 -I -c '
import json, sys
d = json.load(open(sys.argv[1]))
if not (d["available"] and d["version"].startswith("0.19.1") and d["reason"] == ""):
    sys.exit("backups status: %r" % d)' "$fo/bstatus.json" || fail "restic not available in the files service"
echo "OK: files service runs restic $(python3 -I -c 'import json, sys; print(json.load(open(sys.argv[1]))["version"])' "$fo/bstatus.json")"
}

# setup_panel_server: the panel in production mode as mcpanel on TCP, the admin ci-admin signed
# in with TOTP and sudo, and the Paper server ciplugins ($psrv) running with RCON ready.
setup_panel_server() {
echo "== panel (production mode, user mcpanel) on the agent and the files service"
install -m 0755 "$panel_bin" "$work/bin/panel"
install -d -m 0700 -o mcpanel -g mcpanel "$work/pdata"
install -d -m 0755 "$po"
cat >"$work/panel.toml" <<EOF
listen = "127.0.0.1:8080"
public_url = "$porigin"
data_dir = "$work/pdata"
agent_socket = "$sock"
files_socket = "$fsock"
geoip_path = "$work/pdata/none.mmdb"
monitoring_ping_addr = "127.0.0.1:25565"
instances_dir = "$work/mc"
EOF
chmod 0644 "$work/panel.toml"
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  env "MCPANEL_ACCEPTANCE_TIMESCALE=$ts" "$work/bin/panel" serve -config "$work/panel.toml" >"$work/panel.log" 2>&1 &
panel_pid=$!
for _ in $(seq 1 50); do curl -fsS "$pbase/api/health" >/dev/null 2>&1 && break; sleep 0.2; done
curl -fsS "$pbase/api/health" >/dev/null || fail "panel health"
if [ "$ts" -gt 1 ]; then
  # Only a panel built with -tags acceptance logs the factor; a production build ignores it and
  # every scaled wait below would run into its timeout.
  grep -q '"time_scale":'"$ts" "$work/panel.log" ||
    fail "panel ignores MCPANEL_ACCEPTANCE_TIMESCALE=$ts (build with -tags acceptance)"
  echo "OK: panel runs with time scale $ts"
fi
padmin_pw='ci plugins admin password 1'
printf '%s\n' "$padmin_pw" | setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" admin create -config "$work/panel.toml" -username ci-admin >"$work/admin.log" 2>&1 ||
  fail "panel admin create: $(cat "$work/admin.log")"
echo "OK: panel up as mcpanel, admin ci-admin created"

echo "== panel: sign in (password, mandatory TOTP setup, sudo by TOTP)"
papi POST /auth/login "$po/login.json" "$(jobj username ci-admin password "$padmin_pw")"
[ "$pcode" = 200 ] || fail "login: HTTP $pcode $(cat "$po/login.json")"
jcheck "$po/login.json" "an admin without 2FA must land in stage setup" 'd["stage"] == "setup"'
pcsrf="$(jget "$po/login.json" 'd["csrf_token"]')"
papi POST /me/totp/setup "$po/totp.json"
[ "$pcode" = 200 ] || fail "totp setup: HTTP $pcode $(cat "$po/totp.json")"
psecret="$(jget "$po/totp.json" 'd["secret"]')"
# the first second factor needs the current password (SEC-15)
p_totp_step=$((EPOCHSECONDS / 30))
papi POST /me/totp/enable "$po/enable.json" "$(jobj code "$(totp "$psecret" "$p_totp_step")" current_password "$padmin_pw")"
[ "$pcode" = 200 ] || fail "totp enable: HTTP $pcode $(cat "$po/enable.json")"
# The enable code consumed the current step (replay protection). The panel accepts one step of
# skew, so the code of the next step is valid right away.
p_totp_step=$((p_totp_step + 1))
papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" "$p_totp_step")\"}"
[ "$pcode" = 200 ] || fail "sudo: HTTP $pcode $(cat "$po/sudo.json")"
papi GET /auth/me "$po/me.json"
[ "$pcode" = 200 ] || fail "me: HTTP $pcode $(cat "$po/me.json")"
jcheck "$po/me.json" "session after TOTP setup and sudo" 'd["stage"] == "full" and d["totp_enabled"] and d["sudo_until"] > 0'
pcsrf="$(jget "$po/me.json" 'd["csrf_token"]')"
echo "OK: signed in as ci-admin with TOTP, sudo active"

echo "== panel: Paper server created and started through the panel"
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
}

# mon_notifier: ntfy notifier to https://ntfy.invalid and short alert windows (both monitoring
# shards).
mon_notifier() {
install -d -m 0755 "$mo"
echo "== monitoring: notifier (unreachable on purpose) and short alert windows"
# Spec §8 names 5 min for the crash window; 30 alert minutes (3 min at time scale 10) leave three
# Paper restarts on a slow runner enough room. Disk thresholds at the minimum: runners often have less than 10 GiB free.
mon_settings '{"disk_min_percent": 1, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 30}'
jcheck "$mo/settings.json" "settings stored, no server alert so far" '
  d["disk_min_percent"] == 1 and d["disk_min_bytes"] == 1073741824 and d["down_after_min"] == 1
  and d["crash_restarts"] == 3 and d["crash_window_min"] == 30
  and [x for x in d["alerts"] if x["kind"] != "host.disk_low"] == []'
# The sudo of mon_settings still holds for the notifier and the refused update.
papi POST /security/notifiers "$mo/notifier.json" \
  '{"kind": "ntfy", "name": "ci-monitoring", "enabled": true, "url": "https://ntfy.invalid", "topic": "citest"}'
[ "$pcode" = 201 ] || fail "notifier: HTTP $pcode $(cat "$mo/notifier.json")"
papi PUT /monitoring/settings "$mo/bad.json" '{"disk_min_percent": 0, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 30}'
is_perror "$mo/bad.json" "$pcode" 400 validation_failed || fail "disk_min_percent 0: HTTP $pcode $(cat "$mo/bad.json")"
papi PUT /monitoring/settings "$mo/bad.json" '{"disk_min_percent": 1, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 30, "x": 1}'
is_perror "$mo/bad.json" "$pcode" 400 bad_request || fail "unknown field: HTTP $pcode $(cat "$mo/bad.json")"
echo "OK: ntfy notifier to https://ntfy.invalid, down after 1 min, crash loop 3 restarts in 30 min, invalid values refused"
}

# mon_audit_settings: every successful PUT /monitoring/settings of this shard is audited.
mon_audit_settings() {
echo "== monitoring: audit"
papi GET "/audit?action=monitoring.&limit=50" "$mo/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$mo/audit.json")"
jcheck "$mo/audit.json" "monitoring.settings_update x$mon_puts by ci-admin" '
  len([e for e in d["items"] if e["action"] == "monitoring.settings_update" and e["result"] == "success"
       and e["actor_name"] == "ci-admin" and "from" in e["details"] and "to" in e["details"]]) == int(a[0])' "$mon_puts"
echo "OK: monitoring.settings_update x$mon_puts by ci-admin audited"
}
