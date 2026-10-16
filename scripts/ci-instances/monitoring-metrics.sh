# shellcheck shell=bash
# Shard monitoring-metrics, sourced by scripts/ci-instances.sh (sub-project 5, spec §10 criteria
# 1-6 on the panel path): history, live values, host values, RCON noise without a viewer,
# settings validation and host.disk_low/ok through the notify dispatcher (an ntfy notifier to
# https://ntfy.invalid: every event ends in one "delivery failed" line of the panel log), audit.

setup_agent_files
setup_panel_server

echo "== monitoring: history, live values and host values"
install -d -m 0755 "$mo"
# The collector samples every 30 s / time scale and asks for TPS once a minute / time scale; a
# live call switches TPS to 5 s for 30 s (both scaled). The TPS source needs three valid answers
# and the history two reachable points, so poll (each poll is also a live call) until both hold.
live_ok='
  d["at"] is not None and d["up"] is True and d["players"] == 0 and d["players_max"] == 20 and d["player_names"] == []
  and d["tps_source"] in ("tick_query", "paper") and 15 <= d["tps"] <= 20.5 and 0 <= d["mspt"] < 1000
  and d["cpu_percent"] is not None and d["memory_bytes"] > 0 and d["memory_limit_bytes"] > d["memory_bytes"]'
history_ok='
  d["range"] == "1h" and d["step_ms"] == 30000
  and len(d["series"]["up"]) >= 2 and all(p[1] == 1 for p in d["series"]["up"][-2:])
  and d["series"]["players"][-1][1] == 0 and d["series"]["players_max"][-1][1] == 20
  and 15 <= d["series"]["tps"][-1][1] <= 20.5 and d["series"]["memory_bytes"][-1][1] > 0
  and all(len(p) == 4 and p[1] == p[2] == p[3] for m in d["series"].values() for p in m)'
mon_values() {
  papi GET "/servers/$psrv/metrics/live" "$mo/live.json"
  [ "$pcode" = 200 ] || fail "live: HTTP $pcode $(cat "$mo/live.json")"
  papi GET "/servers/$psrv/metrics?range=1h" "$mo/m1h.json"
  [ "$pcode" = 200 ] || fail "metrics 1h: HTTP $pcode $(cat "$mo/m1h.json")"
  [ "$(jget "$mo/live.json" "bool($live_ok)")" = True ] && [ "$(jget "$mo/m1h.json" "bool($history_ok)")" = True ]
}
pwait_for 300 "live values and 1 h history of the running server" mon_values
jcheck "$mo/live.json" "live values of a running Paper server" "$live_ok"
jcheck "$mo/m1h.json" "1 h history" "$history_ok"
papi GET "/servers/$psrv/metrics?range=30d" "$mo/m30d.json"
[ "$pcode" = 200 ] || fail "metrics 30d: HTTP $pcode $(cat "$mo/m30d.json")"
jcheck "$mo/m30d.json" "30 d history answers (the hourly rollup may still be empty)" '
  d["range"] == "30d" and d["step_ms"] == 3600000 and all(len(m) <= 721 for m in d["series"].values())'
papi GET "/servers/$psrv/metrics?range=2h" "$mo/bad.json"
is_perror "$mo/bad.json" "$pcode" 400 validation_failed || fail "range=2h: HTTP $pcode $(cat "$mo/bad.json")"
papi GET /monitoring/overview "$mo/overview.json"
[ "$pcode" = 200 ] || fail "overview: HTTP $pcode $(cat "$mo/overview.json")"
jcheck "$mo/overview.json" "overview with the plugins server and host values (admin)" '
  any(s["id"] == int(a[0]) and s["up"] is True and s["players"] == 0 and s["players_max"] == 20
      and len(s["sparkline"]) >= 2 and s["alerts"] == [] for s in d["servers"])
  and d["host"] is not None and d["host"]["disk_total"] > 0 and 0 < d["host"]["disk_free"] <= d["host"]["disk_total"]
  and d["host"]["memory_total"] > 0 and d["host"]["cpu_percent"] is not None and d["host"]["load1"] is not None' "$psrv"
papi GET "/monitoring/host?range=1h" "$mo/host.json"
[ "$pcode" = 200 ] || fail "host metrics: HTTP $pcode $(cat "$mo/host.json")"
jcheck "$mo/host.json" "host history" '
  d["range"] == "1h" and len(d["series"]["disk_free"]) >= 1 and len(d["series"]["memory_used"]) >= 1
  and len(d["series"]["cpu_percent"]) >= 1'
echo "OK: up, players 0/20, TPS $(jget "$mo/live.json" 'd["tps"]') ($(jget "$mo/live.json" 'd["tps_source"]')), CPU/RAM, history 1h/30d, overview, host values"

echo "== monitoring: RCON noise without a viewer"
# The live calls above keep TPS at 5 s for 30 s (scaled); wait until that ended, then count the
# RCON lines Paper writes in a window (120 s, at least 30 s with a time scale) in which this
# script sends no RCON command itself.
noise_window=$((120 / ts))
[ "$noise_window" -ge 30 ] || noise_window=30
sleep $((40 / ts + 1 + noise_window)) # the live mode ends, then the window
rcon_lines="$(docker logs --since "${noise_window}s" "mcpanel-mc-$psrv" 2>&1 | grep -c 'RCON Client' || true)"
tps_source="$(jget "$mo/live.json" 'd["tps_source"]')"
# tick_query and the paper fallback (mspt only) send one command per minute / time scale; Paper
# logs two lines per RCON connection ("started", "shutting down"): 4 in 120 s unscaled, plus
# one pass on the window edge. The live cadence (12x as often) would exceed it by far.
max_lines=$((2 * (noise_window * ts / 60 + 1)))
[ "$rcon_lines" -le "$max_lines" ] || fail "RCON noise: $rcon_lines lines in $noise_window s (source $tps_source, max $max_lines)"
echo "OK: $rcon_lines RCON log lines in $noise_window s without a viewer (source $tps_source, time scale $ts)"

mon_notifier

echo "== monitoring: disk threshold above the free space -> host.disk_low -> host.disk_ok"
disk_dir="$work/mc" # instances_dir of the acceptance panel.toml
free_b="$(df -B1 --output=avail "$disk_dir" | tail -1 | tr -d ' ')"
size_b="$(df -B1 --output=size "$disk_dir" | tail -1 | tr -d ' ')"
# host.disk_ok needs the lowest thresholds (1 GiB, 1 %) plus the hysteresis (2 GiB, 2 points)
if [ "$free_b" -lt $((3 << 30)) ] || [ $((free_b * 100 / size_b)) -lt 3 ]; then
  echo "SKIP: disk thresholds - only $((free_b >> 20)) MiB free on $disk_dir; host.disk_ok needs 3 GiB and 3 % (hysteresis)"
else
  if mon_firing host.disk_low; then
    # fired with the defaults before the thresholds were lowered: let it resolve first
    pwait_for 90 "host.disk_low from the defaults resolved" mon_not_firing host.disk_low
    sleep 10 # its host.disk_ok delivery attempt (retries) lands in the log before the baseline
  fi
  low0="$(notified_count host.disk_low)"
  ok0="$(notified_count host.disk_ok)"
  mon_settings '{"disk_min_percent": 50, "disk_min_bytes": 10995116277760, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 30}'
  pwait_for 90 "host.disk_low firing" mon_firing host.disk_low
  pwait_for 60 "host.disk_low delivered" notified host.disk_low $((low0 + 1))
  papi GET /monitoring/overview "$mo/overview.json"
  [ "$pcode" = 200 ] || fail "overview: HTTP $pcode $(cat "$mo/overview.json")"
  jcheck "$mo/overview.json" "host card shows the alert" '"host.disk_low" in d["host"]["alerts"]'
  mon_settings '{"disk_min_percent": 1, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 30}'
  pwait_for 90 "host.disk_low resolved" mon_not_firing host.disk_low
  pwait_for 60 "host.disk_ok delivered" notified host.disk_ok $((ok0 + 1))
  echo "OK: host.disk_low fired and delivered above the free space, host.disk_ok after lowering the thresholds"
fi

mon_audit_settings
