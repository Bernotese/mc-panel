# shellcheck shell=bash
# shellcheck disable=SC2154 # work, pdata, psrv, pcode, ts and the helpers come from lib.sh
# Shard dev, sourced by scripts/ci-instances.sh (dev instances spec §9): the running Paper server
# ciplugins is the prod. A dev instance of it is created (sudo) and synced from a restic snapshot
# of the prod; it then has the prod's files, the dev overrides ([DEV] MOTD, view distance 4) and
# its own RCON password, and its own leftovers are gone. A second sync ("Sync now") brings a
# new marker of the prod. Around both syncs the prod's data directory is checksummed (everything
# except what a running Paper server writes itself, listed in dev_prod_excludes) and the prod is
# pinged through mc-router every 2 s: the prod must be byte-for-byte unchanged, never restarted
# and always reachable. Then a dev op is re-applied at the dev's start, the dev falls asleep
# after its idle time (scaled), a status ping through mc-router gets the waker's MOTD, a login
# attempt gets the waker's disconnect and wakes the dev, and a second login reaches the server.
# Last: the backup routes refuse the dev, the prod cannot be deleted while it has a dev, the dev
# can, and the dev.* audit entries.

setup_agent_files
setup_panel_server

dout="$work/dout"    # outputs of this shard
install -d -m 0755 "$dout"
dsrv=""              # server id of the dev
ddata=""             # its data directory
dprod_fqdn="ciplugins.mc.localhost"
ddev_fqdn="dev-ciplugins.mc.localhost"
dping_stop="$dout/ping.stop"
dping_fail="$dout/ping.fail"
dping_pid=""
dlast_sync=0         # last_sync_at (Unix ms) of the previous sync
dwaker_motd='Dev schläft – verbinden zum Aufwecken'
dwaker_kick='Dev startet (oder wird gerade synchronisiert). Bitte in ca. 1 Minute neu verbinden.'

# Top-level entries and paths of the prod's data directory that the running Paper server (or the
# backup's save-all flush) writes by itself. Everything else must stay byte-for-byte as it was:
# configs, jars, server.properties, ops/whitelist/bans, the markers of this shard, hidden files.
dev_prod_excludes=(world world_nether world_the_end logs cache crash-reports usercache.json)

# dev_prod_sums <outfile>: owner, mode, size and sha256 of every entry of the prod's data
# directory except dev_prod_excludes (top-level names), sorted.
dev_prod_sums() {
  python3 -I -c '
import hashlib, os, stat, sys
root, skip = sys.argv[1], set(sys.argv[3:])
out = []
for top in sorted(os.listdir(root)):
    if top in skip:
        continue
    paths = [os.path.join(root, top)]
    if os.path.isdir(paths[0]) and not os.path.islink(paths[0]):
        for d, dirs, files in os.walk(paths[0]):
            dirs.sort()
            paths += [os.path.join(d, n) for n in sorted(dirs + files)]
    for p in paths:
        st = os.lstat(p)
        line = "%s %o %d:%d %d" % (os.path.relpath(p, root), st.st_mode, st.st_uid, st.st_gid, st.st_size)
        if stat.S_ISREG(st.st_mode):
            with open(p, "rb") as f:
                line += " " + hashlib.file_digest(f, "sha256").hexdigest()
        elif stat.S_ISLNK(st.st_mode):
            line += " -> " + os.readlink(p)
        out.append(line)
open(sys.argv[2], "w").write("\n".join(sorted(out)) + "\n")' "$pdata" "$1" "${dev_prod_excludes[@]}"
}

# dev_ping_start / dev_ping_stop: pings the prod through mc-router every 2 s in the background;
# every failed ping adds a line to $dping_fail. The loop also ends when $work is removed.
dev_ping_start() {
  rm -f "$dping_stop" "$dping_fail"
  : >"$dping_fail"
  (
    while [ -d "$work" ] && [ ! -e "$dping_stop" ]; do
      if ! python3 -I scripts/mc-status.py 127.0.0.1 25565 "$dprod_fqdn" >"$dout/ping.json" 2>"$dout/ping.err"; then
        echo "$(date +%T) $(cat "$dout/ping.err" 2>/dev/null)" >>"$dping_fail"
      fi
      sleep 2
    done
  ) &
  dping_pid=$!
}
dev_ping_stop() {
  touch "$dping_stop"
  wait "$dping_pid" 2>/dev/null || true
  dping_pid=""
  [ ! -s "$dping_fail" ] || fail "the prod did not answer every ping during the sync: $(cat "$dping_fail")"
}

# dev_get: GET /servers/<dev>/dev into $dout/dev.json.
dev_get() {
  papi GET "/servers/$dsrv/dev" "$dout/dev.json"
  [ "$pcode" = 200 ] || fail "dev settings: HTTP $pcode $(cat "$dout/dev.json")"
}

# dev_synced: true once a sync newer than dlast_sync has ended (ok or failed).
dev_synced() {
  dev_get
  [ "$(jget "$dout/dev.json" 'not d["sync_pending"] and d["last_sync_status"] in ("ok", "failed") and (d["last_sync_at"] or 0) > int(a[0])' "$dlast_sync")" = True ]
}

# dev_wait_sync <label>: waits for the sync and fails the run unless it ended ok.
dev_wait_sync() {
  pwait_for 900 "dev sync ($1)" dev_synced
  jcheck "$dout/dev.json" "dev sync ($1) ok" 'd["last_sync_status"] == "ok" and d["last_sync_error"] is None'
  dlast_sync="$(jget "$dout/dev.json" 'd["last_sync_at"]')"
}

d_state() { inst_field "$dsrv" 'i["state"]'; }
d_running() { [ "$(d_state)" = running ]; }
d_stopped() { local s; s="$(d_state)"; [ "$s" != running ] && [ "$s" != restarting ]; }
d_rcon_ready() { [ "$(post_status /v1/instances/rcon "{\"id\": $dsrv, \"command\": \"list\"}" "$dout/rcon.json")" = 200 ]; }
d_sleeping() { dev_get; [ "$(jget "$dout/dev.json" 'd["sleeping"]')" = True ] && d_stopped; }
p_started_at() { inst_field "$psrv" 'i["started_at"]'; }

# d_status <outfile>: server list ping of the dev's address through mc-router.
d_status() { python3 -I scripts/mc-status.py 127.0.0.1 25565 "$ddev_fqdn" >"$1" 2>"$1.err"; }
d_waker_answers() {
  d_status "$dout/dstatus.json" && [ "$(jget "$dout/dstatus.json" '(d["description"]["text"] if isinstance(d["description"], dict) else d["description"]) == a[0] and d["players"]["online"] == 0' "$dwaker_motd")" = True ]
}

# d_login: a login attempt at the dev's address; the answer lands in $dout/login.txt.
d_login() { python3 -I scripts/mc-status.py 127.0.0.1 25565 "$ddev_fqdn" --login CiDevLogin >"$dout/login.txt" 2>&1; }

# d_props <key>: value of key in the dev's server.properties.
d_props() { sed -n "s/^$1=//p" "$ddata/server.properties" | tail -n 1; }

echo "== dev: prod data before the dev exists"
printf 'ci dev marker: one\n' >"$pdata/ci-dev-marker.txt"
install -d -m 0755 -o mcpanel-mc -g mcpanel-mc "$pdata/config"
printf 'ci dev config\n' >"$pdata/config/ci-dev.txt"
chown mcpanel-mc:mcpanel-mc "$pdata/ci-dev-marker.txt" "$pdata/config/ci-dev.txt"
prod_rcon_pw="$(sed -n 's/^rcon\.password=//p' "$pdata/server.properties" | tail -n 1)"
[ -n "$prod_rcon_pw" ] || fail "the prod's server.properties has no rcon.password"
papi GET /servers/memory "$dout/memory.json"
[ "$pcode" = 200 ] || fail "memory limits: HTTP $pcode $(cat "$dout/memory.json")"

echo "== dev: create dev-ciplugins (admin, sudo); the first sync runs while the prod keeps running"
p_before="$(p_started_at)"
dev_prod_sums "$dout/prod-before-1.txt"
dev_ping_start
p_sudo
papi POST "/servers/$psrv/dev" "$dout/create.json" '{}'
[ "$pcode" = 201 ] || fail "create dev: HTTP $pcode $(cat "$dout/create.json")"
jcheck "$dout/create.json" "the dev server" '
  d["name"] == "dev-ciplugins" and d["dev"]["source_id"] == int(a[0]) and d["dev"]["source_name"] == "ciplugins"
  and d["dev"]["sleeping"] and d["instance"]["type"] == "paper" and d["instance"]["memory_mib"] == 2048
  and d["instance"]["properties"]["motd"].startswith("[DEV] ")
  and d["instance"]["properties"]["view_distance"] == 4 and d["instance"]["properties"]["simulation_distance"] == 3' "$psrv"
dsrv="$(jget "$dout/create.json" 'd["id"]')"
ddata="$work/mc/instances/$dsrv"
papi POST "/servers/$psrv/dev" "$dout/create2.json" '{}'
is_perror "$dout/create2.json" "$pcode" 409 dev_exists || fail "second dev: HTTP $pcode $(cat "$dout/create2.json")"
papi GET "/servers/$psrv" "$dout/prod.json"
[ "$pcode" = 200 ] || fail "get prod: HTTP $pcode $(cat "$dout/prod.json")"
jcheck "$dout/prod.json" "the prod names its dev" 'd["dev_id"] == int(a[0]) and d.get("dev") is None' "$dsrv"
dev_wait_sync "first"
dev_ping_stop
dev_prod_sums "$dout/prod-after-1.txt"
diff -u "$dout/prod-before-1.txt" "$dout/prod-after-1.txt" >"$dout/prod-diff-1.txt" ||
  fail "the first sync changed the prod's data directory: $(cat "$dout/prod-diff-1.txt")"
if [ "$(p_started_at)" != "$p_before" ] || ! p_running; then fail "the prod was stopped or restarted by the sync"; fi
no_tmp_in "$pdata" || fail "temp entries in the prod's data directory"
echo "OK: dev $dsrv created asleep, first sync ok; prod unchanged (outside ${dev_prod_excludes[*]}), running and pingable throughout"

echo "== dev: the dev holds the prod's data and keeps its own RCON password"
[ "$(cat "$ddata/ci-dev-marker.txt")" = "ci dev marker: one" ] || fail "dev marker: $(cat "$ddata/ci-dev-marker.txt" 2>&1)"
[ "$(cat "$ddata/config/ci-dev.txt")" = "ci dev config" ] || fail "dev config: $(cat "$ddata/config/ci-dev.txt" 2>&1)"
[ -d "$ddata/world" ] || fail "the dev has no world"
[ "$(stat -c '%U:%G' "$ddata/ci-dev-marker.txt")" = "mcpanel-mc:mcpanel-mc" ] || fail "dev file owner: $(stat -c '%U:%G' "$ddata/ci-dev-marker.txt")"
[ ! -e "$work/mc/backups/repos/$dsrv" ] || fail "the dev got a backup repository"
papi GET "/servers/$psrv/backups" "$dout/pbackups.json"
[ "$pcode" = 200 ] || fail "prod backups: HTTP $pcode $(cat "$dout/pbackups.json")"
jcheck "$dout/pbackups.json" "one dev-sync snapshot of the prod" '[s["kind"] for s in d["snapshots"]] == ["dev-sync"]'
snap_sync1="$(jget "$dout/pbackups.json" 'd["snapshots"][0]["id"]')"
echo "OK: marker and config on the dev, world copied, no repo for the dev, snapshot $snap_sync1 of kind dev-sync on the prod"

echo "== dev: asleep, the waker answers its address"
pwait_for 120 "status ping of the sleeping dev answered by the waker" d_waker_answers
docker inspect mcpanel-waker >"$dout/waker.json" || fail "no mcpanel-waker container"
jcheck "$dout/waker.json" "waker hardening" '
  len(d) == 1 and d[0]["State"]["Running"] and d[0]["Config"]["User"] == "65534:65534"
  and d[0]["HostConfig"]["ReadonlyRootfs"] and d[0]["HostConfig"]["CapDrop"] == ["ALL"]
  and not d[0]["HostConfig"]["Privileged"] and not d[0]["HostConfig"]["PortBindings"]
  and list(d[0]["NetworkSettings"]["Networks"]) == ["mcpanel-mc"]
  and not any(m.get("Source") == "/var/run/docker.sock" for m in d[0]["Mounts"])'
d_stopped || fail "the dev runs before anyone connected: $(d_state)"
echo "OK: status ping of $ddev_fqdn: \"$dwaker_motd\", 0 players; mcpanel-waker unprivileged on mcpanel-mc only"

echo "== dev: Sync now brings the prod's new state; the dev's own changes are gone"
printf 'ci dev marker: two\n' >"$pdata/ci-dev-marker.txt"
printf 'only on the dev\n' >"$ddata/ci-dev-only.txt"
chown mcpanel-mc:mcpanel-mc "$pdata/ci-dev-marker.txt" "$ddata/ci-dev-only.txt"
p_before="$(p_started_at)"
dev_prod_sums "$dout/prod-before-2.txt"
dev_ping_start
papi POST "/servers/$dsrv/dev/sync" "$dout/sync.json"
[ "$pcode" = 202 ] || fail "sync now: HTTP $pcode $(cat "$dout/sync.json")"
papi POST "/servers/$dsrv/power" "$dout/power.json" '{"action": "start"}'
is_perror "$dout/power.json" "$pcode" 409 conflict || fail "start during a sync: HTTP $pcode $(cat "$dout/power.json")"
dev_wait_sync "sync now"
dev_ping_stop
dev_prod_sums "$dout/prod-after-2.txt"
diff -u "$dout/prod-before-2.txt" "$dout/prod-after-2.txt" >"$dout/prod-diff-2.txt" ||
  fail "Sync now changed the prod's data directory: $(cat "$dout/prod-diff-2.txt")"
if [ "$(p_started_at)" != "$p_before" ] || ! p_running; then fail "the prod was stopped or restarted by Sync now"; fi
no_tmp_in "$pdata" || fail "temp entries in the prod's data directory"
[ "$(cat "$ddata/ci-dev-marker.txt")" = "ci dev marker: two" ] || fail "dev marker after Sync now: $(cat "$ddata/ci-dev-marker.txt" 2>&1)"
[ ! -e "$ddata/ci-dev-only.txt" ] || fail "a file of the dev survived the sync"
no_tmp_in "$ddata" || fail "temp entries in the dev's data directory"
papi GET "/servers/$psrv/backups" "$dout/pbackups.json"
[ "$pcode" = 200 ] || fail "prod backups: HTTP $pcode $(cat "$dout/pbackups.json")"
jcheck "$dout/pbackups.json" "a fresh dev-sync snapshot replaced the old one" '
  [s["kind"] for s in d["snapshots"]] == ["dev-sync"] and d["snapshots"][0]["id"] != a[0]' "$snap_sync1"
echo "OK: Sync now took a fresh snapshot (the older dev-sync one forgotten); dev has the new marker, its own file is gone; prod unchanged and pingable"

echo "== dev: a dev op is applied at the start; overrides and own RCON password in server.properties"
papi POST "/servers/$dsrv/dev/ops" "$dout/ops.json" '{"name": "Notch"}'
[ "$pcode" = 200 ] || fail "add dev op: HTTP $pcode $(cat "$dout/ops.json")"
jcheck "$dout/ops.json" "dev ops list" 'd["names"] == ["Notch"]'
papi POST "/servers/$dsrv/dev/ops" "$dout/badop.json" '{"name": "x; stop"}'
if ! is_perror "$dout/badop.json" "$pcode" 400 validation_failed && ! is_perror "$dout/badop.json" "$pcode" 400 bad_request; then
  fail "invalid op name: HTTP $pcode $(cat "$dout/badop.json")"
fi
# The dev keeps the default idle_minutes (15) while it starts: once it answers pings without
# players, it falls asleep after idle_minutes (scaled: 15 min / ts = 90 s), so 5 (30 s) would
# leave the op far less time than the wait below and race the sleep. Set to 5 after the checks.
papi POST "/servers/$dsrv/power" "$dout/power.json" '{"action": "start"}'
[ "$pcode" = 202 ] || fail "start dev: HTTP $pcode $(cat "$dout/power.json")"
pwait_for 600 "dev running" d_running
pwait_for 300 "rcon on the dev" d_rcon_ready
d_has_op() { grep -q '"name" *: *"Notch"' "$ddata/ops.json" 2>/dev/null; }
pwait_for $((120 + 300 / ts)) "op Notch in the dev's ops.json" d_has_op
[ "$(d_props motd | cut -c1-6)" = "[DEV] " ] || fail "dev motd: $(d_props motd)"
[ "$(d_props view-distance)" = 4 ] || fail "dev view-distance: $(d_props view-distance)"
[ "$(d_props simulation-distance)" = 3 ] || fail "dev simulation-distance: $(d_props simulation-distance)"
dev_rcon_pw="$(d_props 'rcon\.password')"
if [ -z "$dev_rcon_pw" ] || [ "$dev_rcon_pw" = "$prod_rcon_pw" ] || [ "$dev_rcon_pw" = '********' ]; then
  fail "the dev's rcon.password is not its own"
fi
echo "OK: op Notch in ops.json after the start, motd \"$(d_props motd)\", view/simulation distance 4/3, own RCON password"
papi PATCH "/servers/$dsrv/dev" "$dout/patch.json" '{"idle_minutes": 5}'
[ "$pcode" = 200 ] || fail "idle minutes: HTTP $pcode $(cat "$dout/patch.json")"

echo "== dev: falls asleep without players (idle 5 alert minutes, scaled)"
pwait_for $((120 + 3000 / ts)) "dev asleep" d_sleeping
pwait_for 120 "status ping of the sleeping dev answered by the waker again" d_waker_answers
echo "OK: dev stopped and sleeping; the waker answers its address again"

echo "== dev: a login attempt is refused with the waker's text and wakes the dev; the next one gets in"
if d_login; then
  fail "the first login reached a server: $(cat "$dout/login.txt")"
fi
grep -qF "$dwaker_kick" "$dout/login.txt" || fail "first login: $(cat "$dout/login.txt")"
pwait_for 120 "the dev woke up" d_running
dev_get
jcheck "$dout/dev.json" "awake" 'not d["sleeping"]'
pwait_for 600 "a login that reaches the dev" d_login
grep -qE '^login: (encryption_request|set_compression|login_success)$' "$dout/login.txt" || fail "second login: $(cat "$dout/login.txt")"
echo "OK: first login: \"$dwaker_kick\"; dev woke; second login: $(cat "$dout/login.txt")"

echo "== dev: no backups on the dev; delete order (dev before prod)"
papi GET "/servers/$dsrv/backups" "$dout/dbackups.json"
is_perror "$dout/dbackups.json" "$pcode" 409 dev_managed || fail "backups of the dev: HTTP $pcode $(cat "$dout/dbackups.json")"
p_sudo
papi DELETE "/servers/$psrv" "$dout/pdel.json"
is_perror "$dout/pdel.json" "$pcode" 409 dev_exists || fail "delete prod with a dev: HTTP $pcode $(cat "$dout/pdel.json")"
p_running || fail "the refused delete touched the prod"
papi DELETE "/servers/$dsrv" "$dout/ddel.json"
[ "$pcode" = 204 ] || fail "delete dev: HTTP $pcode $(cat "$dout/ddel.json")"
d_gone() { [ "$(inst_field "$dsrv" i)" = absent ] && ! docker inspect "mcpanel-mc-$dsrv" >/dev/null 2>&1; }
pwait_for 180 "dev container removed" d_gone
papi GET "/servers/$psrv" "$dout/prod.json"
[ "$pcode" = 200 ] || fail "get prod: HTTP $pcode $(cat "$dout/prod.json")"
jcheck "$dout/prod.json" "the prod has no dev any more" 'd.get("dev_id") is None and d["status"]["state"] == "running"'
echo "OK: backups 409 dev_managed on the dev; delete prod 409 dev_exists; delete dev 204; prod still running"

echo "== dev: audit entries"
papi GET "/audit?action=dev.&limit=200" "$dout/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$dout/audit.json")"
jcheck "$dout/audit.json" "dev.* audit entries" '
  {"dev.create", "dev.sync", "dev.op_add", "dev.settings_update", "dev.wake", "dev.delete"}
  <= {e["action"] for e in d["items"] if e["result"] == "success"}
  and len([e for e in d["items"] if e["action"] == "dev.sync" and e["result"] == "success"]) >= 2'
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" audit verify -config "$work/panel.toml" >"$dout/verify.txt" 2>&1 ||
  fail "panel audit verify: $(cat "$dout/verify.txt")"
echo "OK: dev.create/sync x2/op_add/settings_update/wake/delete audited; audit verify OK"
