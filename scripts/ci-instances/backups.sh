# shellcheck shell=bash
# Shard backups, sourced by scripts/ci-instances.sh (sub-project 4, spec §9 criteria 1-5 and 9):
# a manual backup of the running server with save-off/save-all flush/save-on, the restic repo
# as mcpanel-mc (tags, tar content without symlinks, hidden files or the RCON password), a
# restore with sudo (start refused meanwhile, pre-restore snapshot, data back, server running
# again), the zip download, a daily schedule firing in the panel's time zone, pinning, deleting
# a snapshot, deleting the server with purge_backups=1 and the audit log.

setup_agent_files
setup_panel_server

echo "== backups: empty overview on the plugins server"
b_overview
jcheck "$po/bover.json" "empty backup overview" '
  d["available"] and d["snapshots"] == [] and d["job"] is None
  and d["schedule"]["mode"] == "off" and d["schedule"]["timezone"] == "Europe/Berlin"
  and d["schedule"]["next_run_at"] is None'
echo "OK: backups available, no snapshots, schedule off (Europe/Berlin)"

echo "== backups: data to back up (marker, symlink out of the data dir, hidden file)"
pmarker="$pdata/world/ci-backup-marker.txt"
printf 'ci backup marker: before\n' >"$pmarker"
ln -s ../../router "$pdata/ci-backup-link" # resolves to <instances_dir>/router on the host
printf 'kept by restore\n' >"$pdata/.mcpanel-ci-keep"
chown -h mcpanel-mc:mcpanel-mc "$pmarker" "$pdata/ci-backup-link" "$pdata/.mcpanel-ci-keep"
grep -q '^rcon\.password=..*' "$pdata/server.properties" || fail "server.properties without rcon.password"
if grep -qx 'rcon\.password=\*\*\*\*\*\*\*\*' "$pdata/server.properties"; then
  fail "rcon.password is redacted on disk (the files service must only redact copies)"
fi

echo "== backups: manual backup of the running server (save-off, save-all flush, save-on)"
p_running || fail "plugins server not running before the backup"
b_start POST "" "manual backup" '{"note": "ci before change"}'
jcheck "$po/bstart.json" "manual backup job" '
  d["job"]["kind"] == "backup" and d["job"]["trigger"] == "manual" and d["job"]["state"] in ("queued", "running")'
b_since="$(jget "$po/bstart.json" 'd["job"]["queued_at"]')"
papi POST "/servers/$psrv/backups" "$po/bsecond.json" '{"note": null}'
is_perror "$po/bsecond.json" "$pcode" 409 backup_running || fail "second backup while one runs: HTTP $pcode $(cat "$po/bsecond.json")"
b_wait "manual backup" 600
jcheck "$po/bjob.json" "finished manual backup" '
  d["job"]["step"] == "done" and d["job"]["error_code"] is None and len(d["job"]["snapshot"] or "") == 64
  and d["job"]["finished_at"] >= d["job"]["started_at"] > 0'
snap_manual="$(jget "$po/bjob.json" 'd["job"]["snapshot"]')"
# The agent allows tail <= 1000; since limits the stream to the lines after the job was queued
# (far fewer than 1000).
logs "{\"id\": $psrv, \"tail\": 1000, \"since\": $((b_since * 1000000))}" "$po/blog.ndjson" 5
python3 -I -c '
import json, sys
lines = [json.loads(x)["line"].lower() for x in open(sys.argv[1]) if x.strip()]
def first(s):
    return next((i for i, l in enumerate(lines) if s in l), -1)
off, saved, on = first("automatic saving is now disabled"), first("saved the game"), first("automatic saving is now enabled")
if not 0 <= off < saved < on:
    sys.exit("server log since the backup: save-off at %d, saved at %d, save-on at %d (want this order)\n%s"
             % (off, saved, on, "\n".join(lines[-40:])))' "$po/blog.ndjson" ||
  fail "save-off/save-all flush/save-on in the server log"
p_running || fail "the backup stopped the server"
b_overview
jcheck "$po/bover.json" "manual snapshot in the list" '
  [(s["id"], s["kind"], s["note"], s["pinned"], s["bytes"] > 0, len(s["short_id"])) for s in d["snapshots"]]
  == [(a[0], "manual", "ci before change", False, True, 8)]' "$snap_manual"
echo "OK: manual backup $snap_manual while running; save-off < saved < save-on in the log; 409 backup_running for a second one"

echo "== backups: repo layout and snapshot content (restic as mcpanel-mc)"
[ -f "$work/mc/backups/repos/$psrv/config" ] || fail "no restic repo for server $psrv"
[ "$(stat -c '%U:%G %a' "$work/mc/backups/keys/$psrv")" = "mcpanel-mc:mcpanel-mc 600" ] ||
  fail "key file: $(stat -c '%U:%G %a' "$work/mc/backups/keys/$psrv")"
brestic snapshots --json --no-lock >"$po/rsnap.json" || fail "restic snapshots"
note_tag="note=$(printf 'ci before change' | python3 -I -c 'import base64, sys; print(base64.urlsafe_b64encode(sys.stdin.buffer.read()).rstrip(b"=").decode())')"
jcheck "$po/rsnap.json" "restic tags, host and path" '
  [(s["id"], sorted(s["tags"]), s["hostname"], s["paths"] in (["/data.tar"], ["data.tar"])) for s in d]
  == [(a[0], sorted(["mcpanel", "kind=manual", a[1]]), "mcpanel", True)]' "$snap_manual" "$note_tag"
brestic dump "$snap_manual" /data.tar >"$po/snap.tar" || fail "restic dump"
# Every backup stream ends with the service's own entry .mcpanel-end (entries and content bytes
# before it, JSON); restore and download accept a snapshot only with it. It is checked here and
# is the only hidden name allowed in the tar.
python3 -I -c '
import json, sys, tarfile
t = tarfile.open(sys.argv[1])
all_members = t.getmembers()
end = all_members[-1] if all_members else None
if end is None or end.name != ".mcpanel-end" or not end.isfile():
    sys.exit("snapshot tar: last entry %r, want the end entry .mcpanel-end" % (end.name if end else None,))
counts = json.loads(t.extractfile(end).read())
body = all_members[:-1]
if counts != {"entries": len(body), "bytes": sum(m.size for m in body if m.isfile())}:
    sys.exit("snapshot tar: end entry %r does not match %d entries, %d bytes"
             % (counts, len(body), sum(m.size for m in body if m.isfile())))
members = {m.name.removeprefix("./").rstrip("/"): m for m in body}
special = sorted(n for n, m in members.items() if not (m.isfile() or m.isdir()))
escape = sorted(n for n in members if n.startswith("/") or n == ".." or n.startswith("../") or "/../" in n)
missing = [n for n in ("world/ci-backup-marker.txt", "server.properties") if n not in members]
hidden = sorted(n for n in members if n.split("/")[-1].lower().startswith((".mcpanel-", ".rcon-cli.")) or n == "ci-backup-link")
if special or escape or missing or hidden:
    sys.exit("snapshot tar: special %r, escaping %r, missing %r, must be absent %r" % (special, escape, missing, hidden))
pw = [l for l in t.extractfile(members["server.properties"]).read().decode().splitlines() if l.startswith("rcon.password=")]
marker = t.extractfile(members["world/ci-backup-marker.txt"]).read().decode()
if pw != ["rcon.password=********"] or marker != "ci backup marker: before\n":
    sys.exit("snapshot tar: password lines %r, marker %r" % (pw, marker))
print("snapshot tar: %d entries and the end entry" % len(members))' "$po/snap.tar" || fail "snapshot content"
rm -f "$po/snap.tar"
echo "OK: repo and key (mcpanel-mc, 0600), tags mcpanel/kind=manual/note, tar without symlink, hidden files or RCON password, end entry matches"

echo "== backups: restore with sudo (start refused meanwhile, pre-restore snapshot, data back, server runs again)"
printf 'ci backup marker: after\n' >"$pmarker"
printf 'created after the backup\n' >"$pdata/world/ci-after-backup.txt"
chown mcpanel-mc:mcpanel-mc "$pmarker" "$pdata/world/ci-after-backup.txt"
p_before="$(inst_field "$psrv" 'i["started_at"]')"
p_sudo
b_start POST "/$snap_manual/restore" "restore"
jcheck "$po/bstart.json" "restore job" 'd["job"]["kind"] == "restore" and d["job"]["snapshot"] == a[0]' "$snap_manual"
papi POST "/servers/$psrv/power" "$po/power.json" '{"action": "start"}'
is_perror "$po/power.json" "$pcode" 409 conflict || fail "start during a restore: HTTP $pcode $(cat "$po/power.json")"
papi POST "/servers/$psrv/backups" "$po/bsecond.json" '{}'
is_perror "$po/bsecond.json" "$pcode" 409 backup_running || fail "backup during a restore: HTTP $pcode $(cat "$po/bsecond.json")"
b_wait "restore" 900
jcheck "$po/bjob.json" "finished restore" '
  d["job"]["step"] == "done" and d["job"]["error_code"] is None and d["job"]["snapshot"] == a[0]' "$snap_manual"
[ "$(cat "$pmarker")" = "ci backup marker: before" ] || fail "marker after the restore: $(cat "$pmarker")"
[ "$(stat -c '%U:%G' "$pmarker")" = "mcpanel-mc:mcpanel-mc" ] || fail "restored file owner: $(stat -c '%U:%G' "$pmarker")"
[ ! -e "$pdata/world/ci-after-backup.txt" ] || fail "a file created after the backup survived the restore"
if [ -e "$pdata/ci-backup-link" ] || [ -L "$pdata/ci-backup-link" ]; then
  fail "the symlink survived the restore (it is not in the snapshot)"
fi
[ "$(cat "$pdata/.mcpanel-ci-keep")" = "kept by restore" ] || fail "the restore changed a hidden file"
grep -q 'mappings' "$routes" || fail "router config changed (the restore reached outside the data dir)"
no_tmp_in "$pdata" || fail "temp entries left after the restore: $(find "$pdata" -maxdepth 2 -name '.mcpanel-tmp-*')"
pwait_for 300 "server running again after the restore" p_restarted
pwait_for 240 "rcon after the restore" p_rcon_ready
b_overview
jcheck "$po/bover.json" "pre-restore snapshot listed first" '
  sorted(s["kind"] for s in d["snapshots"]) == ["manual", "pre-restore"] and d["snapshots"][0]["kind"] == "pre-restore"'
snap_pre="$(jget "$po/bover.json" 'next(s["id"] for s in d["snapshots"] if s["kind"] == "pre-restore")')"
brestic dump "$snap_pre" /data.tar | tar -xO --wildcards '*world/ci-backup-marker.txt' >"$po/pre-marker.txt" ||
  fail "marker not in the pre-restore snapshot"
[ "$(cat "$po/pre-marker.txt")" = "ci backup marker: after" ] || fail "pre-restore snapshot: marker $(cat "$po/pre-marker.txt")"
echo "OK: restore with sudo: start 409 meanwhile, data back, link gone, hidden file kept, pre-restore snapshot holds the old state, server restarted"

echo "== backups: download a snapshot as zip"
papi_dl "/servers/$psrv/backups/$snap_manual/download" "$po/snap.zip"
[ "$pcode" = 200 ] || fail "download: HTTP $pcode $(head -c 500 "$po/snap.zip")"
grep -qi '^content-type: application/zip' "$po/snap.zip.headers" || fail "download headers: $(cat "$po/snap.zip.headers")"
grep -qiE '^content-disposition: attachment;.*ciplugins-[0-9]{8}-[0-9]{6}\.zip' "$po/snap.zip.headers" ||
  fail "download file name: $(grep -i '^content-disposition' "$po/snap.zip.headers")"
python3 -I -c '
import sys, zipfile
z = zipfile.ZipFile(sys.argv[1])
names = {n.rstrip("/") for n in z.namelist()}
bad = sorted(n for n in names if n.split("/")[-1].lower().startswith((".mcpanel-", ".rcon-cli.")) or n == "ci-backup-link")
pw = [l for l in z.read("server.properties").decode().splitlines() if l.startswith("rcon.password=")]
marker = z.read("world/ci-backup-marker.txt").decode()
if bad or pw != ["rcon.password=********"] or marker != "ci backup marker: before\n" or z.testzip() is not None:
    sys.exit("download zip: must be absent %r, password lines %r, marker %r" % (bad, pw, marker))
print("download zip: %d entries" % len(names))' "$po/snap.zip" || fail "download content"
rm -f "$po/snap.zip"
echo "OK: zip with the marker, without RCON password, symlink or hidden files"

echo "== backups: schedule (validation; a daily run fires in the panel's time zone, retention runs)"
papi PUT "/servers/$psrv/backups/schedule" "$po/bsched.json" \
  '{"mode": "daily", "interval_hours": 24, "time_of_day": 240, "weekday": 0, "keep_last": 0, "keep_daily": 0, "keep_weekly": 0, "keep_monthly": 0}'
is_perror "$po/bsched.json" "$pcode" 400 validation_failed || fail "schedule without retention: HTTP $pcode $(cat "$po/bsched.json")"
papi GET /servers/settings "$po/settings.json"
[ "$pcode" = 200 ] || fail "settings: HTTP $pcode $(cat "$po/settings.json")"
ptz="$(jget "$po/settings.json" 'd["backup_timezone"]')"
# The next minute when at least 15 s remain until it (the scheduler ticks every minute / time
# scale), else the one after.
tod_off=1
[ "$((EPOCHSECONDS % 60))" -le 45 ] || tod_off=2
tod="$(minute_in "$ptz" "$tod_off")"
now_ms="$(date +%s%3N)"
papi PUT "/servers/$psrv/backups/schedule" "$po/bsched.json" \
  "{\"mode\": \"daily\", \"interval_hours\": 24, \"time_of_day\": $tod, \"weekday\": 0, \"keep_last\": 1, \"keep_daily\": 7, \"keep_weekly\": 4, \"keep_monthly\": 6}"
[ "$pcode" = 200 ] || fail "schedule: HTTP $pcode $(cat "$po/bsched.json")"
jcheck "$po/bsched.json" "saved schedule, next run within 3 minutes" '
  d["schedule"]["mode"] == "daily" and d["schedule"]["time_of_day"] == int(a[0]) and d["schedule"]["timezone"] == a[1]
  and int(a[2]) < d["schedule"]["next_run_at"] <= int(a[2]) + 3 * 60000' "$tod" "$ptz" "$now_ms"
pwait_for 420 "scheduled backup (due at minute $tod in $ptz, ticker every minute)" b_sched_finished
[ "$bjob_state" = "done" ] || fail "scheduled backup failed: $(cat "$po/bjob.json")"
b_overview
jcheck "$po/bover.json" "scheduled snapshot, last run, next run tomorrow, others kept by the retention" '
  sorted(s["kind"] for s in d["snapshots"]) == ["manual", "pre-restore", "scheduled"]
  and d["schedule"]["last_status"] == "ok" and d["schedule"]["last_error"] is None
  and d["schedule"]["last_snapshot_id"] == next(s["id"] for s in d["snapshots"] if s["kind"] == "scheduled")
  and d["schedule"]["next_run_at"] > int(a[0]) + 23 * 3600000' "$now_ms"
snap_sched="$(jget "$po/bover.json" 'd["schedule"]["last_snapshot_id"]')"
echo "OK: daily run at minute $tod ($ptz) fired; forget with keep_last 1 kept the manual and pre-restore snapshots"

echo "== backups: pin (new snapshot id); manual snapshots cannot be pinned"
papi POST "/servers/$psrv/backups/$snap_manual/pin" "$po/bpin.json" '{"pinned": true}'
is_perror "$po/bpin.json" "$pcode" 400 validation_failed || fail "pin a manual snapshot: HTTP $pcode $(cat "$po/bpin.json")"
papi POST "/servers/$psrv/backups/$snap_sched/pin" "$po/bpin.json" '{"pinned": true}'
[ "$pcode" = 200 ] || fail "pin: HTTP $pcode $(cat "$po/bpin.json")"
jcheck "$po/bpin.json" "pinned snapshot with a new id" '
  d["snapshot"]["pinned"] and d["snapshot"]["kind"] == "scheduled"
  and d["snapshot"]["id"] != a[0] and len(d["snapshot"]["id"]) == 64' "$snap_sched"
snap_pinned="$(jget "$po/bpin.json" 'd["snapshot"]["id"]')"
b_overview
jcheck "$po/bover.json" "list after pinning" '
  a[0] not in [s["id"] for s in d["snapshots"]]
  and [s["pinned"] for s in d["snapshots"] if s["id"] == a[1]] == [True]' "$snap_sched" "$snap_pinned"
brestic snapshots --json --no-lock >"$po/rsnap.json" || fail "restic snapshots"
jcheck "$po/rsnap.json" "tag pinned in the repo" '
  [sorted(s["tags"]) for s in d if s["id"] == a[0]] == [sorted(["mcpanel", "kind=scheduled", "pinned"])]' "$snap_pinned"
echo "OK: pin $snap_sched -> $snap_pinned (tag pinned), manual snapshot -> 400"

echo "== backups: delete a snapshot (sudo)"
p_sudo
b_start DELETE "/$snap_manual" "delete snapshot"
jcheck "$po/bstart.json" "delete job" 'd["job"]["kind"] == "delete" and d["job"]["snapshot"] == a[0]' "$snap_manual"
b_wait "delete snapshot" 300
b_overview
jcheck "$po/bover.json" "list after deleting" '
  sorted(s["kind"] for s in d["snapshots"]) == ["pre-restore", "scheduled"] and a[0] not in [s["id"] for s in d["snapshots"]]' "$snap_manual"
echo "OK: manual snapshot deleted, pre-restore and pinned scheduled snapshot kept"

echo "== backups: delete the server with purge_backups=1 removes repo and key"
[ -d "$work/mc/backups/repos/$psrv" ] && [ -f "$work/mc/backups/keys/$psrv" ] || fail "repo or key missing before the purge"
p_sudo
papi DELETE "/servers/$psrv?purge_backups=1" "$po/sdel.json"
[ "$pcode" = 204 ] || fail "delete server: HTTP $pcode $(cat "$po/sdel.json")"
pwait_for 180 "container of the deleted server removed" p_gone
if [ -e "$work/mc/backups/repos/$psrv" ] || [ -e "$work/mc/backups/keys/$psrv" ]; then
  fail "repo or key left after purge_backups: $(ls -la "$work/mc/backups/repos" "$work/mc/backups/keys")"
fi
echo "OK: server $psrv deleted, repo and key purged"

echo "== backups: audit entries and hash chain"
papi GET "/audit?action=backup.&limit=200" "$po/baudit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/baudit.json")"
jcheck "$po/baudit.json" "backup.* audit entries (scheduled runs are not audited)" '
  sorted(e["action"] for e in d["items"] if e["result"] == "success")
  == sorted(["backup.create", "backup.restore", "backup.download", "backup.schedule_update", "backup.pin", "backup.delete"])
  and all(e["target"] == "server:" + a[0] and e["actor_name"] == "ci-admin" for e in d["items"])' "$psrv"
papi GET "/audit?action=server.delete&limit=10" "$po/sdaudit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$po/sdaudit.json")"
jcheck "$po/sdaudit.json" "server.delete with backups_purged" '
  [e["details"].get("backups_purged") for e in d["items"] if e["target"] == "server:" + a[0]] == [True]' "$psrv"
setpriv --reuid=mcpanel --regid=mcpanel --init-groups \
  "$work/bin/panel" audit verify -config "$work/panel.toml" >"$po/verify.txt" 2>&1 ||
  fail "panel audit verify: $(cat "$po/verify.txt")"
echo "OK: backup.create/restore/download/schedule_update/pin/delete by ci-admin on server:$psrv, server.delete backups_purged=true; audit verify OK"

