# shellcheck shell=bash
# Shard core, sourced by scripts/ci-instances.sh (sub-project 2, spec §12 criteria 2, 6, 7, 8 on
# the agent path; sub-project 3a, spec §10 criteria 2 and 4 on the agent path; sub-project 3b,
# spec §9 criteria 2-4 on the files-service path): creates a Paper server through the agent
# socket, waits until it answers a status ping routed by hostname through mc-router, checks the
# container hardening (read-only rootfs with a disk-backed /tmp, limits, the egress filter
# against metadata and private addresses, replies to a client in a private network through the
# router), the console endpoints and the files service (credentials, peer UID, upload, redaction,
# editing, symlinks, directory zip), crashes it, waits for Docker's restart, checks the console
# on the stopped server, planted player files and archive extraction (incl. zip slip), and
# removes it. No panel.

setup_agent_files

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
    "only_data_and_tmp_mounts": sorted((m["Destination"], m["Source"]) for m in c["Mounts"])
        == [("/data", sys.argv[3] + "/instances/9001"), ("/tmp", sys.argv[3] + "/tmp/9001")],
    "no_ports": not h.get("PortBindings"),
    "network": list(c["NetworkSettings"]["Networks"]) == ["mcpanel-mc"],
    "label": cfg["Labels"].get("mcpanel.instance") == "9001",
    "readonly_rootfs": h["ReadonlyRootfs"] is True,
    "no_tmpfs": not h.get("Tmpfs"),
    "nofile": h.get("Ulimits") == [{"Name": "nofile", "Soft": 65536, "Hard": 65536}],
}
bad = [k for k, ok in checks.items() if not ok]
if bad:
    print("hardening failed:", bad, file=sys.stderr)
    sys.exit(1)
print("OK: hardening", sorted(checks))' "$(id -u mcpanel-mc)" "$(id -g mcpanel-mc)" "$work/mc" || fail "hardening"
[ "$(stat -c '%U %a' "$work/mc/tmp")" = "root 700" ] || fail "tmp/ owner/mode: $(stat -c '%U:%G %a' "$work/mc/tmp")"
[ "$(stat -c '%U:%G %a' "$work/mc/tmp/9001")" = "mcpanel-mc:mcpanel-mc 700" ] ||
  fail "tmp/9001 owner/mode: $(stat -c '%U:%G %a' "$work/mc/tmp/9001")"
if setpriv --reuid=mcpanel-mc --regid=mcpanel-mc --clear-groups ls "$work/mc/tmp" >/dev/null 2>&1; then
  fail "mcpanel-mc (files service) can list tmp/"
fi
echo "OK: /tmp is tmp/9001 (mcpanel-mc 0700) below a root-only tmp/"
docker inspect mcpanel-router | python3 -I -c '
import json, sys
c = json.load(sys.stdin)[0]
h, args = c["HostConfig"], c["Args"]
checks = {
    "readonly_rootfs": h["ReadonlyRootfs"] is True,
    "pids": h["PidsLimit"] == 128,
    "rate_limit": args[args.index("-connection-rate-limit") + 1] == "50" if "-connection-rate-limit" in args else False,
}
bad = [k for k, ok in checks.items() if not ok]
if bad:
    print("router hardening failed:", bad, args, file=sys.stderr)
    sys.exit(1)
print("OK: router hardening", sorted(checks))' || fail "router hardening"

echo "== container egress filter (SEC-02)"
# The Paper download at the first start already went through the filter (public egress and DNS
# work). New connections to the metadata address and private networks must be dropped by the
# agent's forward rule for the network's subnet: its counter has to grow.
subnet="$(docker network inspect -f '{{(index .IPAM.Config 0).Subnet}}' mcpanel-mc)"
nft list chain inet mcpanel forward >"$work/egress.nft" 2>&1 || fail "egress chain: $(cat "$work/egress.nft")"
grep -q "ip saddr $subnet .*@mcpanel_egress_deny4 counter packets [0-9]* bytes [0-9]* drop" "$work/egress.nft" ||
  fail "no egress rule for $subnet: $(cat "$work/egress.nft")"
egress_drops() { nft list chain inet mcpanel forward | sed -n 's/.*@mcpanel_egress_deny4 counter packets \([0-9]*\) .*/\1/p'; }
drops_before="$(egress_drops)"
for target in 169.254.169.254 10.255.255.1 192.168.255.1; do
  if docker exec mcpanel-mc-9001 curl -sS -m 3 -o /dev/null "http://$target/" 2>"$work/egress.err"; then
    fail "container reached http://$target/ despite the egress filter"
  fi
done
drops_after="$(egress_drops)"
[ "$drops_after" -gt "$drops_before" ] || fail "egress rule dropped nothing ($drops_before -> $drops_after): $(cat "$work/egress.err")"
echo "OK: metadata and private addresses blocked from the container ($((drops_after - drops_before)) packets dropped)"
# Only new connections from the containers are dropped: a player in a private network (here a
# network namespace with 10.250.0.2) still gets the router's replies through the published port.
egress_ns=1
ip netns add mcpcitest
ip link add mcpcit0 type veth peer name mcpcit1
ip link set mcpcit1 netns mcpcitest
ip addr add 10.250.0.1/30 dev mcpcit0
ip link set mcpcit0 up
ip netns exec mcpcitest ip addr add 10.250.0.2/30 dev mcpcit1
ip netns exec mcpcitest ip link set mcpcit1 up
lan_ping() { ip netns exec mcpcitest python3 -I scripts/mc-status.py 10.250.0.1 25565 citest.mc.localhost >"$work/lan-status.json" 2>&1; }
wait_for 30 "status ping from a client in 10.250.0.0/30" lan_ping
grep -q 'ci acceptance' "$work/lan-status.json" || fail "status ping from the private network: $(cat "$work/lan-status.json")"
ip netns del mcpcitest
ip link del mcpcit0 2>/dev/null || true
egress_ns=""
echo "OK: a client in a private network reaches the server through the router"
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
head -c $((65 * 1024 * 1024)) /dev/zero >"$work/big.bin"
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
[ ! -e "$work/mc/tmp/9001" ] || fail "tmp/9001 still there after remove"
echo "OK: container and route removed, data moved to deleted/, tmp/9001 removed"
