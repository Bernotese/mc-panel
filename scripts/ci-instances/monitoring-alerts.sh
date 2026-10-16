# shellcheck shell=bash
# Shard monitoring-alerts, sourced by scripts/ci-instances.sh (sub-project 5, spec §10 criteria
# 1-6 on the panel path): the alerts server.down/recovered (the panel's pings dropped by an
# nftables rule), server.crash_loop (three crashes, no down alert; stable again after a window
# without restarts) through the notify dispatcher (ntfy to https://ntfy.invalid, checked in the
# panel log), the per-server switch (no down alert while it is off) and the audit log.

setup_agent_files
# shellcheck disable=SC2034 # read by setup_panel_server (lib.sh)
mon_boot_margin=1 # no server.down from a slow first boot (setup_panel_server)
setup_panel_server

mon_notifier

echo "== monitoring: server unreachable (pings of the panel dropped) -> server.down -> server.recovered"
command -v nft >/dev/null || fail "nft missing (CI step \"Install nftables\")"
down0="$(notified_count server.down)"
rec0="$(notified_count server.recovered)"
mon_nft=1
nft add table inet mcpanel_citest
nft add chain inet mcpanel_citest output '{ type filter hook output priority 0; policy accept; }'
nft add rule inet mcpanel_citest output meta skuid mcpanel tcp dport 25565 drop
# rest of the start grace (5 min / time scale after the last restart) + down_after_min + one pass
pwait_for 420 "server.down firing" mon_firing server.down "$psrv"
pwait_for 60 "server.down delivered to the dispatcher" notified server.down $((down0 + 1))
p_running || fail "the container must keep running while only the pings are dropped"
papi GET /monitoring/overview "$mo/overview.json"
[ "$pcode" = 200 ] || fail "overview: HTTP $pcode $(cat "$mo/overview.json")"
jcheck "$mo/overview.json" "overview: unreachable with alert" '
  any(s["id"] == int(a[0]) and s["up"] is False and "server.down" in s["alerts"] for s in d["servers"])' "$psrv"
jcheck "$mo/alerts.json" "firing alert names the server" '
  any(x["kind"] == "server.down" and x["server_id"] == int(a[0]) and x["server_name"] == "ciplugins"
      and x["since"] > 0 and x["detail"] != "" for x in d["alerts"])' "$psrv"
nft delete table inet mcpanel_citest
mon_nft=""
pwait_for 120 "server.down resolved" mon_not_firing server.down "$psrv"
pwait_for 60 "server.recovered delivered" notified server.recovered $((rec0 + 1))
[ "$(notified_count server.down)" = $((down0 + 1)) ] ||
  fail "server.down delivered $(($(notified_count server.down) - down0)) times, want exactly 1"
echo "OK: pings dropped -> server.down once (container still running), pings back -> server.recovered"

echo "== monitoring: three crashes within the window -> server.crash_loop, no server.down (grace)"
down0="$(notified_count server.down)"
crash0="$(notified_count server.crash_loop)"
rc_before="$(inst_field "$psrv" 'i["restart_count"]')"
# The crashes are real Paper shutdowns and boots (about 40 s each) that the time scale does not
# shorten. Keep the real-time margin of an unscaled run: down_after_min 1 (60 s) covers the
# shutdown before Docker's restart, so it is $ts alert minutes (60 s) during the crashes.
mon_settings "{\"disk_min_percent\": 1, \"disk_min_bytes\": 1073741824, \"down_after_min\": $ts, \"crash_restarts\": 3, \"crash_window_min\": $mon_crash_window}"
for n in 1 2 3; do
  p_crash
  echo "crash $n done (restart_count $(inst_field "$psrv" 'i["restart_count"]'))"
done
rc_after="$(inst_field "$psrv" 'i["restart_count"]')"
[ "$((rc_after - rc_before))" -ge 3 ] || fail "restart_count $rc_before -> $rc_after, want +3"
pwait_for 120 "server.crash_loop firing" mon_firing server.crash_loop "$psrv"
pwait_for 60 "server.crash_loop delivered" notified server.crash_loop $((crash0 + 1))
mon_not_firing server.down "$psrv" || fail "server.down must not fire while the crash loop fires (and within the start grace)"
# Resolving needs a full window without restarts: shorten it to 1 min instead of waiting the whole window
# (down_after_min back to 1: the crash loop fires, so no server.down meanwhile).
rec0="$(notified_count server.recovered)"
mon_settings '{"disk_min_percent": 1, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 1}'
pwait_for 180 "server.crash_loop resolved after a window without restarts" mon_not_firing server.crash_loop "$psrv"
pwait_for 60 "server.recovered (stable again) delivered" notified server.recovered $((rec0 + 1))
mon_not_firing server.down "$psrv" || fail "server.down must not fire after the crash loop (start grace)"
[ "$(notified_count server.down)" = "$down0" ] || fail "server.down delivered during the crash loop"
echo "OK: restart_count +$((rc_after - rc_before)), server.crash_loop fired and delivered, no server.down; server.recovered after a window without restarts"

echo "== monitoring: down alerts switched off -> no server.down while unreachable"
papi PUT "/servers/$psrv/alerts" "$mo/switch.json" '{"down_alerts": false}'
[ "$pcode" = 204 ] || fail "down alerts off: HTTP $pcode $(cat "$mo/switch.json")"
papi GET "/servers/$psrv" "$mo/detail.json"
[ "$pcode" = 200 ] || fail "server detail: HTTP $pcode $(cat "$mo/detail.json")"
jcheck "$mo/detail.json" "detail shows down_alerts false for the admin" 'd["down_alerts"] is False'
down0="$(notified_count server.down)"
mon_nft=1
nft add table inet mcpanel_citest
nft add chain inet mcpanel_citest output '{ type filter hook output priority 0; policy accept; }'
nft add rule inet mcpanel_citest output meta skuid mcpanel tcp dport 25565 drop
# Stay unreachable past the start grace of the last crash + down_after_min + three passes, and
# at least 150 s (both / time scale, at least 30 s): with the switch on, server.down would fire
# within that time.
started_s=$(($(inst_field "$psrv" 'i["started_at"]') / 1000))
quiet_min=$((150 / ts))
[ "$quiet_min" -ge 30 ] || quiet_min=30
quiet_until=$((started_s + (300 + 60 + 90) / ts))
[ "$quiet_until" -ge $((EPOCHSECONDS + quiet_min)) ] || quiet_until=$((EPOCHSECONDS + quiet_min))
while [ "$EPOCHSECONDS" -lt "$quiet_until" ]; do
  mon_not_firing server.down "$psrv" || fail "server.down fired although down alerts are off"
  sleep 10
done
p_running || fail "the container must keep running while only the pings are dropped"
papi GET /monitoring/overview "$mo/overview.json"
[ "$pcode" = 200 ] || fail "overview: HTTP $pcode $(cat "$mo/overview.json")"
jcheck "$mo/overview.json" "overview: unreachable without alert" '
  any(s["id"] == int(a[0]) and s["up"] is False and s["alerts"] == [] for s in d["servers"])' "$psrv"
nft delete table inet mcpanel_citest
mon_nft=""
pwait_for 90 "plugins server reachable again" mon_up
papi PUT "/servers/$psrv/alerts" "$mo/switch.json" '{"down_alerts": true}'
[ "$pcode" = 204 ] || fail "down alerts on: HTTP $pcode $(cat "$mo/switch.json")"
papi GET "/servers/$psrv" "$mo/detail.json"
[ "$pcode" = 200 ] || fail "server detail: HTTP $pcode $(cat "$mo/detail.json")"
jcheck "$mo/detail.json" "detail shows down_alerts true again" 'd["down_alerts"] is True'
[ "$(notified_count server.down)" = "$down0" ] || fail "server.down delivered although down alerts were off"
echo "OK: down alerts off -> unreachable for $((quiet_until - started_s)) s after the last start without server.down"

echo "== monitoring: audit"
papi GET "/audit?action=monitoring.&limit=50" "$mo/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$mo/audit.json")"
jcheck "$mo/audit.json" "monitoring.settings_update x$mon_puts by ci-admin" '
  len([e for e in d["items"] if e["action"] == "monitoring.settings_update" and e["result"] == "success"
       and e["actor_name"] == "ci-admin" and "from" in e["details"] and "to" in e["details"]]) == int(a[0])' "$mon_puts"
papi GET "/audit?action=server.alerts_update&limit=10" "$mo/audit2.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$mo/audit2.json")"
jcheck "$mo/audit2.json" "server.alerts_update x2 on server:<id>" '
  sorted(e["details"]["down_alerts"] for e in d["items"]) == [False, True]
  and all(e["target"] == "server:" + a[0] and e["actor_name"] == "ci-admin" for e in d["items"])' "$psrv"
echo "OK: server.alerts_update x2 and monitoring.settings_update x$mon_puts by ci-admin audited"
