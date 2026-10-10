#!/usr/bin/env bash
# Instance acceptance for a Linux CI runner with Docker (sub-project 2, spec §12 criteria 2,
# 6, 7, 8 on the agent path): starts the real agent as root, creates a Paper server through
# its socket, waits until it answers a status ping routed by hostname through mc-router,
# checks the container hardening, crashes it, waits for Docker's restart and removes it.
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

echo "== crash: stop the server from inside, Docker must restart it (unless-stopped)"
before="$(status_field 'i["started_at"]')"
docker exec mcpanel-mc-9001 rcon-cli stop >/dev/null
restarted() { a="$(status_field 'i["started_at"]')"; [ "$a" != 0 ] && [ "$a" != "$before" ] && running; }
wait_for 180 "automatic restart" restarted
echo "OK: restarted (started_at $before -> $(status_field 'i["started_at"]'))"

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
