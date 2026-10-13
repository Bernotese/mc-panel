# Teilprojekt 5 – CI-Abnahme, Test-VM-Konfiguration, Doku (Aufgaben 15–16) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Monitoring und Alerts auf einem CI-Runner gegen einen echten Paper-Server abnehmen – über die panel-API im Produktionsmodus (TOTP, Sudo, CSRF): Werte im Verlauf und live, Host-Werte, RCON-Rauschen, `server.down`/`server.recovered`, `server.crash_loop`, `host.disk_low`/`host.disk_ok` inklusive Zustellung an `notify` und Audit. Dazu die Test-VM-Konfiguration ergänzen und die Doku nachziehen (Abnahme-Checkliste, Entwickler- und Betriebsdoku, `CLAUDE.md`, `README.md`). Gesamt-Review und Abschluss koordiniert der Lead.

**Architecture:**
- Aufgabe 15 berührt nur `scripts/**` und `.github/workflows/ci.yml`.
- `scripts/ci-instances.sh` bekommt im `panel.toml` der Abnahme `monitoring_ping_addr = "127.0.0.1:25565"` und `instances_dir = "$work/mc"` und einen Abschnitt „monitoring“. Er läuft auf dem Plugin-Server `ciplugins` **nach dem Plugin-Teil und vor dem Backup-Teil aus TP4** (der Backup-Teil löscht den Server am Ende mit `purge_backups=1`).
- **Zustellung prüfen ohne Empfänger:** Ein panel im Produktionsmodus lehnt ntfy-Ziele auf localhost und privaten Adressen ab (`notify.ValidateConfig` ohne `dev`, und der Dialer blockt interne Ziele, `notify/sender.go`). Ein lokaler ntfy-Mock ist deshalb nicht erreichbar. Stattdessen richtet die Abnahme einen ntfy-Notifier auf `https://ntfy.invalid` ein (`.invalid` löst nach RFC 6761 nie auf, die Validierung lässt ihn zu). Jedes Ereignis, das den Dispatcher erreicht, erzeugt nach den Wiederholungen (1 s + 2 s + 4 s) die JSON-Logzeile `"msg":"notify: delivery failed"` mit `"type":"<ereignis>"`. Die Abnahme prüft genau diese Zeilen im panel-Log und zusätzlich den Alert-Zustand über `GET /monitoring/settings` (`alerts`) und `GET /monitoring/overview`.
- **„Nicht erreichbar“ ohne Container-Stopp:** Ein gestoppter Container würde vom Abgleich sofort neu gestartet (`desired_state = running`). Stattdessen sperrt eine nftables-Regel nur die Pings des panels: Tabelle `inet mcpanel_citest`, Kette `output` (Hook output, Priorität 0), Regel `meta skuid mcpanel tcp dport 25565 drop`. Der Container läuft weiter, `up = 0`, nach `down_after_min = 1` feuert `server.down`; Regel löschen → `server.recovered`. Der Server läuft zu diesem Zeitpunkt schon lange (Schonfrist von 5 min nach dem Start ist vorbei).
- **Crash-Loop:** dreimal `rcon-cli stop` im Container (derselbe Weg wie der bestehende Crash-Test); Docker startet neu (`unless-stopped`), `restart_count` steigt um 3, `server.crash_loop` feuert. Weil jeder Neustart eine neue Schonfrist öffnet und `server.down` nicht zusätzlich gemeldet wird, solange `server.crash_loop` feuert (V5), darf dabei **kein** `server.down` feuern – das prüft die Abnahme mit. „Genau eine Meldung“ prüft nur der Down/Recovery-Abschnitt.
- **Platte:** Schwellen per `PUT /monitoring/settings` über den freien Platz heben (`disk_min_bytes` = 10 TiB) → `host.disk_low`; danach auf 1 % / 1 GiB senken → `host.disk_ok`.
- `scripts/testvm-deploy.sh` bekommt die beiden neuen `panel.toml`-Schlüssel ausdrücklich (die Defaults stimmen zwar, die Datei soll aber den Betrieb dokumentieren).
- Aufgabe 16 schreibt die Doku. Gesamt-Review, Deploy auf die Test-VM, manuelle Abnahme und Abschluss koordiniert der Lead.

**Tech Stack:** Bash (shellcheck-sauber), Python 3 (nur Standardbibliothek, immer `python3 -I`), `curl`, `setpriv`, `nft`, Docker, GitHub Actions (`ubuntu-24.04`).

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md` (§3, §5, §6, §8, §10) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-13-monitoring-00-overview.md` (V1, V3, V5, Global Constraints; Abschnitt „Ausführung“ gilt vollständig)

## Global Constraints (zusätzlich zur Übersicht)

| Thema | Wert |
|---|---|
| Phase / Branch | C, Teammate „mon-ci-docs“, `feat/monitoring-ci-docs`; Aufgabe 15 und die Doku-Schritte von 16 |
| Startet, wenn | TP5-Phase A gemerged **und** TP4-PR #56 (CI-Abnahme Backups, ändert dieselben Dateien) gemerged; die CI-Abnahme wird erst grün, wenn B1 und B2 gemerged sind (dann `git merge origin/main`, pushen) |
| Dateieigentum | nur `scripts/**`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md` |
| Wo getestet wird | lokal nur `make check`, `bash -n`, `shellcheck` (falls installiert) und `actionlint` per `go run`; alles mit root, Docker, nftables oder Minecraft nur in der CI |
| Test-Server | `ciplugins` (`$psrv`), läuft am Ende des Plugin-Teils; `max-players` Default 20 |
| Alert-Einstellungen in der CI | `down_after_min = 1`, `crash_restarts = 3`, `crash_window_min = 10` (Spec §8 nennt 5 min; 10 min geben drei Paper-Neustarts auf einem langsamen Runner Luft), Platte zuletzt `disk_min_percent = 1`, `disk_min_bytes = 1073741824` (bleibt so: der Runner hat oft weniger als 10 GiB frei, die Defaults würden am Ende wieder feuern) |
| Notifier | ntfy, Name `ci-monitoring`, URL `https://ntfy.invalid`, Topic `citest`, aktiviert |
| Sudo | vor `PUT /monitoring/settings` und dem Anlegen des Notifiers frisch per TOTP (`p_sudo` aus dem TP4-Teil, falls vorhanden; sonst wie der Login-Teil mit `totp "$psecret" 0`) |
| Workflow | Job `instances`: `timeout-minutes` +15 (Monitoring-Teil erwartet ~10 min: 1 min Verlauf, 2 min RCON-Zählung, ~2 min Down + Recovery, ~4 min Crash-Loop, ~1 min Platte); Pfadfilter `instances` um `^internal/monitoring/` ergänzen |

---

### Task 15: CI-Abnahme Monitoring, Test-VM-Schlüssel

**Files:**
- Modify: `scripts/ci-instances.sh` (Kopfkommentar, Variablen, `cleanup`, `panel.toml`, Helfer, Abschnitte „monitoring …“, `PASS`-Zeile)
- Modify: `.github/workflows/ci.yml` (Filter `instances`, `timeout-minutes` des Jobs `instances`)
- Modify: `scripts/testvm-deploy.sh` (`panel.toml`)

**Interfaces:**
- Consumes:
  - panel-API aus V3: `getServerMetrics` (`?range=1h`) → `MetricsSeries {range, step_ms, series: {<metric>: [[at, avg, min, max] …]}}`; `getServerLiveMetrics` → `LiveMetrics`; `getMonitoringOverview` → `{servers: [OverviewServer], host: HostSnapshot|null}`; `getHostMetrics`; `getMonitoringSettings` → `MonitoringSettings {…, alerts: [FiringAlert {kind, server_id, server_name, since, detail}]}`; `updateMonitoringSettings` (Sudo) → `200 MonitoringSettings`; `updateServerAlerts` → `204`.
  - Notifier-API (vorhanden): `POST /security/notifiers` `{kind, name, enabled, url, topic}` (Admin, Sudo) → `201`.
  - agent-Status mit `restart_count` (V1).
  - Notify-Ereignistypen (V1): `server.down`, `server.recovered`, `server.crash_loop`, `host.disk_low`, `host.disk_ok`; Audit `monitoring.settings_update` (V1).
  - Helfer aus `ci-instances.sh`: `papi`, `is_perror`, `jget`, `jcheck`, `jobj`, `totp`, `inst_field`, `pwait_for`, `p_running`, `p_rcon_ready`, `fail`; Variablen `psecret`, `psrv`, `pdata`, `work`; aus TP4 ggf. `p_sudo`.
- Produces:
  - `scripts/ci-instances.sh AGENT_BINARY FILES_BINARY PANEL_BINARY` endet mit `PASS: instance acceptance (agent, files, plugins, monitoring and backups path)`.

- [ ] **Step 1: Worktree anlegen und Vertrag prüfen**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel worktree add ../mc-panel-wt/feat/monitoring-ci-docs -b feat/monitoring-ci-docs origin/main
cd /Users/jannes/Workspace/mc-panel-wt/feat/monitoring-ci-docs
(cd web && npm ci)
```

Vorab prüfen, dass Phase A den Vertrag so umgesetzt hat, wie das Skript ihn nutzt, und dass TP4 (#56) gemerged ist. Bei Abweichungen den Lead fragen und das Skript nicht anpassen.

```bash
gh pr view 56 --json state -q .state          # MERGED, sonst warten
grep -n "== backups" scripts/ci-instances.sh | head -3   # Einfügepunkt (erster Backup-Abschnitt)
grep -n "^p_sudo()" scripts/ci-instances.sh
grep -n "operationId: \(getServerMetrics\|getServerLiveMetrics\|getMonitoringOverview\|getHostMetrics\|getMonitoringSettings\|updateMonitoringSettings\|updateServerAlerts\)" api/openapi.yaml
grep -n "monitoring_ping_addr\|instances_dir" internal/config/config.go
grep -n "RestartCount\|restart_count" internal/agent/agentapi/instances.go
grep -n "EventServerDown\|EventServerRecovered\|EventServerCrashLoop\|EventHostDiskLow\|EventHostDiskOK" internal/notify/notify.go
grep -n "ActionMonitoringSettingsUpdate" internal/audit/types.go
```

Erwartet: alle sieben `operationId`s, beide Konfigurationsschlüssel, `restart_count`, alle fünf Ereignisse, die Audit-Aktion. Fehlt `p_sudo`, legt Step 3 ihn an.

- [ ] **Step 2: Kopf, Variablen, Aufräumen, `panel.toml`**

Kopfkommentar: im Satz „Then it starts the panel …“ nach den Plugins ergänzen: „…, then checks monitoring (history, live values, host values, RCON noise) and the alerts server.down/recovered, server.crash_loop and host.disk_low/ok through the notify dispatcher, …“; den Absatz „(sub-project 3c, spec §10 criteria 2 and 5 …)“ um „sub-project 5, spec §10 criteria 1–6 on the panel path“ erweitern. In der Zeile „Changes the host (…)“ „nftables table inet mcpanel_citest“ ergänzen.

Variablen (nach `p_started=""`):

```bash
mo="$work/mout"     # monitoring outputs
mon_nft=""          # set while the ping-blocking nftables table exists
```

`cleanup()` vor `ids="$(docker ps …`:

```bash
  if [ -n "$mon_nft" ]; then
    nft delete table inet mcpanel_citest 2>/dev/null || true
  fi
```

`panel.toml` der Abnahme (Heredoc nach `geoip_path = …`) um zwei Zeilen ergänzen:

```bash
monitoring_ping_addr = "127.0.0.1:25565"
instances_dir = "$work/mc"
```

- [ ] **Step 3: Helfer**

Nach den TP4-Helfern (bzw. nach `check_players`) einfügen:

```bash
# p_sudo: fresh sudo by TOTP (the window from the login may have expired). Defined by the
# backups part of sub-project 4; only add it here if it is missing.
# p_sudo() { papi POST /auth/sudo/totp "$po/sudo.json" "{\"code\": \"$(totp "$psecret" 0)\"}"; [ "$pcode" = 200 ] || fail "sudo: HTTP $pcode $(cat "$po/sudo.json")"; }

# mon_settings <json body>: PUT /monitoring/settings with a fresh sudo; fails on != 200.
mon_settings() {
  p_sudo
  papi PUT /monitoring/settings "$mo/settings.json" "$1"
  [ "$pcode" = 200 ] || fail "monitoring settings: HTTP $pcode $(cat "$mo/settings.json")"
}

# mon_firing <kind> [server id]: true if the alert is in the firing list of /monitoring/settings.
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

# notified <event type> [count]: the panel log has at least <count> (default 1) delivery
# attempts of that event type. The CI notifier points at https://ntfy.invalid, so every event
# that reaches the dispatcher ends in exactly one "notify: delivery failed" line (JSON log).
notified() {
  python3 -I -c '
import json, sys
n = 0
for line in open(sys.argv[1], errors="replace"):
    try:
        e = json.loads(line)
    except ValueError:
        continue
    if e.get("msg") == "notify: delivery failed" and e.get("type") == sys.argv[2]:
        n += 1
sys.exit(0 if n >= int(sys.argv[3]) else 1)' "$work/panel.log" "$1" "${2:-1}"
}
notified_count() {
  python3 -I -c '
import json, sys
n = 0
for line in open(sys.argv[1], errors="replace"):
    try:
        e = json.loads(line)
    except ValueError:
        continue
    n += e.get("msg") == "notify: delivery failed" and e.get("type") == sys.argv[2]
print(n)' "$work/panel.log" "$1"
}

# last_point <metrics file> <metric>: avg of the newest point of a metric ("none" if absent).
last_point() { jget "$1" 'd["series"][a[0]][-1][1] if d["series"].get(a[0]) else "none"' "$2"; }

# p_crash: stops Paper from inside (rcon-cli stop) and waits until Docker restarted it and RCON
# answers again (restart policy unless-stopped, same path as the crash test of sub-project 2).
p_crash() {
  local before
  before="$(inst_field "$psrv" 'i["started_at"]')"
  docker exec "mcpanel-mc-$psrv" rcon-cli stop >/dev/null
  p_restarted_since() { a="$(inst_field "$psrv" 'i["started_at"]')"; [ "$a" != 0 ] && [ "$a" != "$before" ] && p_running; }
  pwait_for 300 "plugins server restarted by Docker" p_restarted_since
  pwait_for 300 "rcon on the plugins server after the crash" p_rcon_ready
}
```

`p_restarted_since` als eigene Funktion vor `p_crash` definieren, wenn shellcheck die verschachtelte Definition bemängelt (SC2329/Scope); `before` dann als globale Variable `mon_before`.

- [ ] **Step 4: Abschnitt „monitoring: history and live values“**

Unmittelbar **vor** dem ersten Backup-Abschnitt (`echo "== backups …"`) einfügen:

```bash
echo "== monitoring: history, live values and host values"
install -d -m 0755 "$mo"
# The collector samples every 30 s and polls TPS once a minute; the live call switches TPS to
# 5 s for 30 s, so the TPS source (3 valid answers) is known quickly.
papi GET "/servers/$psrv/metrics/live" "$mo/live.json"
[ "$pcode" = 200 ] || fail "live: HTTP $pcode $(cat "$mo/live.json")"
sleep 75
papi GET "/servers/$psrv/metrics/live" "$mo/live.json"
[ "$pcode" = 200 ] || fail "live: HTTP $pcode $(cat "$mo/live.json")"
jcheck "$mo/live.json" "live values of a running Paper server" '
  d["up"] is True and d["players"] == 0 and d["players_max"] == 20 and d["player_names"] == []
  and d["tps_source"] in ("tick_query", "paper") and 15 <= d["tps"] <= 20.5 and 0 < d["mspt"] < 1000
  and d["cpu_percent"] is not None and d["memory_bytes"] > 0 and d["memory_limit_bytes"] > d["memory_bytes"]'
papi GET "/servers/$psrv/metrics?range=1h" "$mo/m1h.json"
[ "$pcode" = 200 ] || fail "metrics 1h: HTTP $pcode $(cat "$mo/m1h.json")"
jcheck "$mo/m1h.json" "1 h history" '
  d["range"] == "1h" and d["step_ms"] == 30000
  and len(d["series"]["up"]) >= 2 and all(p[1] == 1 for p in d["series"]["up"][-2:])
  and d["series"]["players"][-1][1] == 0 and d["series"]["players_max"][-1][1] == 20
  and 15 <= d["series"]["tps"][-1][1] <= 20.5 and d["series"]["memory_bytes"][-1][1] > 0
  and all(p[1] == p[2] == p[3] for p in d["series"]["up"])'
papi GET "/servers/$psrv/metrics?range=30d" "$mo/m30d.json"
[ "$pcode" = 200 ] || fail "metrics 30d: HTTP $pcode $(cat "$mo/m30d.json")"
jcheck "$mo/m30d.json" "30 d history answers (rollup may still be empty)" 'd["range"] == "30d" and d["step_ms"] == 3600000'
papi GET "/servers/$psrv/metrics?range=2h" "$mo/bad.json"
is_perror "$mo/bad.json" "$pcode" 400 validation_failed || fail "range=2h: HTTP $pcode $(cat "$mo/bad.json")"
papi GET /monitoring/overview "$mo/overview.json"
[ "$pcode" = 200 ] || fail "overview: HTTP $pcode $(cat "$mo/overview.json")"
jcheck "$mo/overview.json" "overview with the plugins server and host values (admin)" '
  any(s["id"] == int(a[0]) and s["up"] is True and s["players"] == 0 and len(s["sparkline"]) >= 2 and s["alerts"] == []
      for s in d["servers"])
  and d["host"] is not None and d["host"]["disk_total"] > 0 and 0 < d["host"]["disk_free"] <= d["host"]["disk_total"]
  and d["host"]["memory_total"] > 0 and d["host"]["cpu_percent"] is not None and d["host"]["load1"] is not None' "$psrv"
papi GET "/monitoring/host?range=1h" "$mo/host.json"
[ "$pcode" = 200 ] || fail "host metrics: HTTP $pcode $(cat "$mo/host.json")"
jcheck "$mo/host.json" "host history" 'len(d["series"]["disk_free"]) >= 1 and len(d["series"]["memory_used"]) >= 1'
echo "OK: up, players 0/20, TPS $(jget "$mo/live.json" 'd["tps"]') ($(jget "$mo/live.json" 'd["tps_source"]')), CPU/RAM, history 1h/30d, overview, host values"
```

- [ ] **Step 5: Abschnitt „monitoring: RCON noise“** (Spec §10 Kriterium 2)

```bash
echo "== monitoring: RCON noise without a viewer"
# The live call above keeps TPS at 5 s for 30 s; wait until that ended, then count the RCON
# lines Paper writes for two minutes in which this script sends no RCON command itself.
sleep 40
docker logs --since 1s "mcpanel-mc-$psrv" >/dev/null 2>&1 # warm-up, no output needed
sleep 120
rcon_lines="$(docker logs --since 120s "mcpanel-mc-$psrv" 2>&1 | grep -c 'RCON Client' || true)"
tps_source="$(jget "$mo/live.json" 'd["tps_source"]')"
# tick_query and the paper fallback (mspt only) ask one command per minute = 2 lines per minute;
# 6 in 120 s leaves room for one pass that falls on the window edge.
max_lines=6
[ "$rcon_lines" -le "$max_lines" ] || fail "RCON noise: $rcon_lines lines in 120 s (source $tps_source, max $max_lines)"
echo "OK: $rcon_lines RCON log lines in 120 s without a viewer (source $tps_source)"
```

- [ ] **Step 6: Abschnitt „monitoring: notifier and alert settings“**

```bash
echo "== monitoring: notifier (unreachable on purpose) and short alert windows"
p_sudo
papi POST /security/notifiers "$mo/notifier.json" \
  '{"kind": "ntfy", "name": "ci-monitoring", "enabled": true, "url": "https://ntfy.invalid", "topic": "citest"}'
[ "$pcode" = 201 ] || fail "notifier: HTTP $pcode $(cat "$mo/notifier.json")"
mon_settings '{"disk_min_percent": 1, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 10}'
jcheck "$mo/settings.json" "settings stored" 'd["down_after_min"] == 1 and d["crash_window_min"] == 10 and d["alerts"] == []'
papi PUT /monitoring/settings "$mo/bad.json" '{"disk_min_percent": 0, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 10}'
is_perror "$mo/bad.json" "$pcode" 400 validation_failed || fail "disk_min_percent 0: HTTP $pcode $(cat "$mo/bad.json")"
echo "OK: ntfy notifier to https://ntfy.invalid, down after 1 min, crash loop 3 restarts in 10 min, invalid values refused"
```

- [ ] **Step 7: Abschnitt „monitoring: server.down and server.recovered“** (Kriterium 3)

```bash
echo "== monitoring: server unreachable (pings of the panel dropped) -> server.down -> server.recovered"
command -v nft >/dev/null || { apt-get install -y -qq nftables >/dev/null || fail "install nftables"; }
nft add table inet mcpanel_citest
mon_nft=1
nft add chain inet mcpanel_citest output '{ type filter hook output priority 0; policy accept; }'
nft add rule inet mcpanel_citest output meta skuid mcpanel tcp dport 25565 drop
pwait_for 360 "server.down firing" mon_firing server.down "$psrv" # rest of the start grace + 1 min + one pass
pwait_for 60 "server.down delivered to the dispatcher" notified server.down
p_running || fail "the container must keep running while only the pings are dropped"
papi GET /monitoring/overview "$mo/overview.json"
jcheck "$mo/overview.json" "overview: unreachable with alert" '
  any(s["id"] == int(a[0]) and s["up"] is False and "server.down" in s["alerts"] for s in d["servers"])' "$psrv"
nft delete table inet mcpanel_citest
mon_nft=""
pwait_for 120 "server.down resolved" mon_not_firing server.down "$psrv"
pwait_for 60 "server.recovered delivered" notified server.recovered
[ "$(notified_count server.down)" = 1 ] || fail "server.down delivered $(notified_count server.down) times, want exactly 1"
echo "OK: pings dropped -> server.down once (container still running), pings back -> server.recovered"
```

- [ ] **Step 8: Abschnitt „monitoring: crash loop“** (Kriterium 4)

```bash
echo "== monitoring: three crashes within the window -> server.crash_loop, no server.down (grace)"
rc_before="$(inst_field "$psrv" 'i["restart_count"]')"
for n in 1 2 3; do
  p_crash
  echo "crash $n done (restart_count $(inst_field "$psrv" 'i["restart_count"]'))"
done
rc_after="$(inst_field "$psrv" 'i["restart_count"]')"
[ "$((rc_after - rc_before))" -ge 3 ] || fail "restart_count $rc_before -> $rc_after, want +3"
pwait_for 120 "server.crash_loop firing" mon_firing server.crash_loop "$psrv"
pwait_for 60 "server.crash_loop delivered" notified server.crash_loop
mon_not_firing server.down "$psrv" || fail "server.down must not fire while the crash loop fires (and within the start grace)"
echo "OK: restart_count +$((rc_after - rc_before)), server.crash_loop fired and delivered, no server.down"
```

- [ ] **Step 9: Abschnitt „monitoring: disk thresholds“** (Kriterium 5)

```bash
echo "== monitoring: disk threshold above the free space -> host.disk_low -> host.disk_ok"
disk_dir="$work/mc" # instances_dir of the acceptance panel.toml
free_b="$(df -B1 --output=avail "$disk_dir" | tail -1 | tr -d ' ')"
size_b="$(df -B1 --output=size "$disk_dir" | tail -1 | tr -d ' ')"
# host.disk_ok needs the lowest thresholds (1 GiB, 1 %) plus the hysteresis (2 GiB, 2 points)
if [ "$free_b" -lt $((3 << 30)) ] || [ $((free_b * 100 / size_b)) -lt 3 ]; then
  echo "SKIP: disk thresholds – only $((free_b >> 20)) MiB free on $disk_dir; host.disk_ok needs 3 GiB and 3 % (hysteresis)"
else
  mon_settings '{"disk_min_percent": 50, "disk_min_bytes": 10995116277760, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 10}'
  pwait_for 90 "host.disk_low firing" mon_firing host.disk_low
  pwait_for 60 "host.disk_low delivered" notified host.disk_low
  papi GET /monitoring/overview "$mo/overview.json"
  jcheck "$mo/overview.json" "host card shows the alert" '"host.disk_low" in d["host"]["alerts"]'
  mon_settings '{"disk_min_percent": 1, "disk_min_bytes": 1073741824, "down_after_min": 1, "crash_restarts": 3, "crash_window_min": 10}'
  pwait_for 90 "host.disk_low resolved" mon_not_firing host.disk_low
  pwait_for 60 "host.disk_ok delivered" notified host.disk_ok
  echo "OK: host.disk_low fired and delivered above the free space, host.disk_ok after lowering the thresholds"
fi
```

- [ ] **Step 10: Abschnitt „monitoring: per-server switch, rights, audit“** (Kriterium 6)

```bash
echo "== monitoring: down alerts switch, non-admin view, audit"
papi PUT "/servers/$psrv/alerts" "$mo/switch.json" '{"down_alerts": false}'
[ "$pcode" = 204 ] || fail "down alerts off: HTTP $pcode $(cat "$mo/switch.json")"
papi GET "/servers/$psrv" "$mo/detail.json"
jcheck "$mo/detail.json" "detail shows down_alerts for the admin" 'd["down_alerts"] is False'
papi PUT "/servers/$psrv/alerts" "$mo/switch.json" '{"down_alerts": true}'
[ "$pcode" = 204 ] || fail "down alerts on: HTTP $pcode $(cat "$mo/switch.json")"
papi GET "/audit?action=monitoring.&limit=50" "$mo/audit.json"
[ "$pcode" = 200 ] || fail "audit: HTTP $pcode $(cat "$mo/audit.json")"
jcheck "$mo/audit.json" "monitoring.settings_update x3 by ci-admin" '
  len([e for e in d["items"] if e["action"] == "monitoring.settings_update" and e["result"] == "success" and e["actor_name"] == "ci-admin"]) == 3'
papi GET "/audit?action=server.alerts_update&limit=10" "$mo/audit2.json"
jcheck "$mo/audit2.json" "server.alerts_update x2 on server:<id>" '
  sorted(e["details"]["down_alerts"] for e in d["items"]) == [False, True] and all(e["target"] == "server:" + a[0] for e in d["items"])' "$psrv"
echo "OK: down alerts switch stored and audited, settings changes audited"
```

Der Nicht-Admin-Teil (Kriterium 6: keine Host-Werte, nur Server mit `server.view`) braucht einen zweiten Benutzer mit TOTP-Pflicht-Setup; er ist in den Go-Tests (Aufgabe 9/10) und in Playwright (Aufgabe 14) abgedeckt und bleibt hier bewusst weg.

- [ ] **Step 11: `PASS`-Zeile, Workflow, Test-VM**

Letzte Zeile: `echo "PASS: instance acceptance (agent, files, plugins, monitoring and backups path)"` (die TP4-Fassung entsprechend erweitern).

`.github/workflows/ci.yml`:
- im Job `changes` den Filter `instances` um `^internal/monitoring/` ergänzen (Muster der übrigen Einträge);
- im Job `instances` `timeout-minutes` um 15 erhöhen (Stand nach TP4: 55 → 70).

`scripts/testvm-deploy.sh`, Heredoc von `/etc/mc-panel/panel.toml` nach `files_socket = …`:

```bash
monitoring_ping_addr = "127.0.0.1:25565"
instances_dir = "/srv/mc-panel"
```

- [ ] **Step 12: Lokal prüfen**

```bash
bash -n scripts/ci-instances.sh scripts/testvm-deploy.sh
command -v shellcheck >/dev/null && shellcheck scripts/ci-instances.sh scripts/testvm-deploy.sh
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
make check
```

Erwartet: alles grün. Die Abnahme selbst läuft nur in der CI (root, Docker, nftables).

- [ ] **Step 13: Commit, Draft-PR, CI**

```bash
git add scripts/ci-instances.sh .github/workflows/ci.yml scripts/testvm-deploy.sh
git commit -m "ci(monitoring): acceptance for history, live values, RCON noise and alerts" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/monitoring-ci-docs
gh pr create --draft --title "ci,docs: TP5 monitoring acceptance and documentation" --body-file <(cat <<'EOF'
CI acceptance for sub-project 5 on the plugins server of `scripts/ci-instances.sh` (before the backups part): history and live values, host values, RCON noise without a viewer, `server.down`/`server.recovered` (pings of the panel dropped by an nftables rule), `server.crash_loop` (three restarts, no down alert in the grace period), `host.disk_low`/`host.disk_ok`, delivery to the notify dispatcher (ntfy notifier to `https://ntfy.invalid`, checked in the panel log), audit. Test VM gets the two new `panel.toml` keys. Docs follow in the next commit.

Waits for the monitoring collector (B1) and alerts/API (B2) PRs: until both are on main the `instances` job fails by design.

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
EOF
)
```

Sobald B1 und B2 auf `main` sind: `git merge origin/main`, pushen, `gh pr checks <n> --watch`; Fehler aus `gh run view <id> --log-failed` beheben (häufigste Kandidaten: Wartezeiten auf langsamen Runnern, Logformat der Notify-Zeile, `tps_source` noch `unknown` → längere erste Wartezeit).

---

### Task 16: Doku

**Files:**
- Create: `docs/dev/acceptance-tp5.md`
- Modify: `docs/dev/README.md`, `CLAUDE.md`, `README.md`

**Interfaces:**
- Consumes: Spec §1–§10, die fertigen Phasen (Routen, Konfigurationsschlüssel, UI-Texte aus `web/src/i18n/de.ts` von B3).
- Produces: Checkliste für die manuelle Abnahme (Lead), Entwickler-/Betriebsdoku.

- [ ] **Step 1: `docs/dev/acceptance-tp5.md`**

Aufbau wie `docs/dev/acceptance-tp4.md` (Vorbereitung, nummerierte Prüfungen mit Erwartung, Abschluss). Inhalt:

```markdown
# Abnahme Teilprojekt 5: Monitoring + Alerts

Manuelle Abnahme auf der Test-VM (`make deploy-testvm`, UI unter https://panel.192-168-1-117.sslip.io/).
Spec: `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md`.

## Vorbereitung

1. Deploy des Stands auf `main`; `journalctl -u mcpanel-panel` zeigt keinen Fehler des Sammlers.
2. Ein laufender Paper-Server (z. B. „survival“) und ein gestoppter Server; ein zweiter Benutzer mit `server.view` nur auf „survival“.
3. Eine Benachrichtigung (ntfy, z. B. ntfy.sh mit eigenem Topic) eingerichtet und per „Test senden“ geprüft.

## Prüfungen

| # | Schritt | Erwartung |
|---|---|---|
| 1 | Dashboard als Admin öffnen | Host-Karte mit CPU, RAM, Load, Platte; pro Server Spieler x/max, TPS, RAM-Balken, CPU, Sparkline, Statuspunkt |
| 2 | Dashboard als zweiter Benutzer | keine Host-Karte, nur „survival“ |
| 3 | Mit einem Minecraft-Client verbinden | Dashboard und Tab „Monitoring“ zeigen 1 Spieler mit Namen (nach ≤ 30 s) |
| 4 | Tab „Monitoring“, Bereiche 1 h / 24 h / 7 d / 30 d | Diagramme Spieler, TPS/MSPT, CPU, RAM mit Limitlinie; Tooltip per Maus und Tastatur; „Als Tabelle anzeigen“ |
| 5 | Tab geschlossen, `docker logs --since 5m mcpanel-mc-<id> \| grep -c "RCON Client"` | höchstens 2 Zeilen pro Minute (auch beim Paper-Fallback, der nur `mspt` fragt) |
| 6 | Einstellungen → „Monitoring & Alerts“: Down nach 1 min; auf der VM `sudo nft add table inet t; sudo nft add chain inet t o '{ type filter hook output priority 0; }'; sudo nft add rule inet t o meta skuid mcpanel tcp dport 25565 drop` | nach 1–2 min Meldung „Server down“, Badge im Dashboard; Container läuft weiter |
| 7 | `sudo nft delete table inet t` | Meldung „wieder online“, Badge weg |
| 8 | Panel neu starten, während ein Down-Alert feuert | keine zweite Down-Meldung |
| 9 | Dreimal `docker exec mcpanel-mc-<id> rcon-cli stop` (jeweils Neustart abwarten) | Meldung „Crash loop“; keine Down-Meldung (Schonfrist nach jedem Neustart, Crash-Loop unterdrückt Down); nach einem Fenster ohne Neustart „Server stable again“ (`server.recovered`) |
| 10 | Down-Alerts für den Server aus, Schritt 6 wiederholen | keine Meldung |
| 11 | Platte: Schwelle über den freien Platz setzen, danach zurück | „Platte knapp“, danach „Platz wieder ok“ |
| 12 | Gestoppter Server | kein Down-Alert; Tab zeigt „gestoppt“ |
| 13 | Monitoring-Einstellungen mit ungültigen Werten speichern | Validierungsfehler; gültige Werte verlangen Sudo und erscheinen im Audit-Log |
| 14 | `du -h /var/lib/mc-panel/*.db` nach einigen Tagen | Wachstum im erwarteten Rahmen (< 50 MB bei 10 Servern über 30 Tage) |

## Abschluss

Ergebnis (bestanden / Befunde) im Abschluss-PR von TP5 vermerken.
```

UI-Texte (Kartentitel, Schalter) beim Schreiben gegen `web/src/i18n/de.ts`, Meldungstitel gegen `internal/monitoring/alerts.go` (dort setzt B2 die Titel; `internal/notify/sender.go` hat dieselben als Default) abgleichen und wörtlich übernehmen.

- [ ] **Step 2: `docs/dev/README.md`**

Neuer Abschnitt „Monitoring“ nach dem Backups-Abschnitt aus TP4 (sonst nach „Produktion: Listen-Adresse und Reverse Proxy“):
- Sammler im panel, Takte (30 s, TPS 60 s, live 5 s für 30 s), Quellen (agent-Status, Server-List-Ping über mc-router an `monitoring_ping_addr`, RCON, `/proc` + `statfs(instances_dir)`), TPS-Quelle per Erkennung (`tick query`, Paper-Fallback, `none`);
- `panel.toml`: `monitoring_ping_addr` (Default `127.0.0.1:25565`, Dev aus), `instances_dir` (Default `/srv/mc-panel`);
- Aufbewahrung (24 h / 7 d / 30 d), Verdichtung alle 5 min, ungefähre DB-Größe;
- Alerts und ihre Regeln (Schonfrist 5 min, Daten-Sperre, Hysterese, Erinnerung 24 h, Zustand überlebt Neustarts), Versand über die Benachrichtigungen;
- Dev-Modus: kein Ping, Host nur Platte, TPS nur mit `MCPANEL_DEV_DOCKER=1`; der Mock (`npm run dev:mock`) zeigt alles;
- CI: wie die Abnahme Zustellung ohne Empfänger prüft (`https://ntfy.invalid`, Logzeile) und „nicht erreichbar“ per nftables-Regel erzeugt.
Im Abschnitt „CI“ den Job `instances` (Dauer, neuer Pfad `internal/monitoring/`) nachziehen.

- [ ] **Step 3: `CLAUDE.md`**

- In der Spec-Liste nach dem TP4-Eintrag: ``- `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md`: monitoring and alerts (sub-project 5); manual acceptance checklist in `docs/dev/acceptance-tp5.md`.``
- Unter „Minecraft instances“ einen Absatz ergänzen: `internal/monitoring` samples every 30 s (agent status, Server List Ping through mc-router at `monitoring_ping_addr`, never through the root agent; TPS/MSPT via RCON once a minute, every 5 s only while someone watches; host values from `/proc` and `statfs`), stores raw/5-min/1-h series in SQLite (24 h / 7 d / 30 d), evaluates `server.down`/`server.recovered`, `server.crash_loop` and `host.disk_low`/`host.disk_ok` with persistent state and sends them through `notify`. The agent only adds `restart_count`. Charts are own SVG components (no chart library).
- In „Request path and routing“ die Paketliste um `monitoring` ergänzen.

- [ ] **Step 4: `README.md`**

- „Funktionen“: Monitoring (Spieler, TPS/MSPT, CPU/RAM, Host, 30 Tage Verlauf) und Alerts (Server down / Crash-Loop, Platte knapp über Discord/ntfy).
- Tabelle „Stand“: Zeile 5 auf „umgesetzt“ setzen erst im Abschluss-PR (Lead); hier unverändert lassen.

- [ ] **Step 5: Prüfen, Commit, Push**

```bash
make check
git add docs/dev/acceptance-tp5.md docs/dev/README.md CLAUDE.md README.md
git commit -m "docs(monitoring): acceptance checklist, developer and operations docs" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" \
  -m "Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
```

Danach beim Lead melden (PR-Nummer, Stand der CI, offene Abhängigkeiten). Spec-Status, Roadmap-Zeile, Löschen der Pläne und der Abschluss-PR sind Sache des Leads.

---

## Self-Review

- **Spec-Abdeckung:** §8 CI-Abnahme → Steps 4–10 (Werte nach ≥ 1 min, Host, Down/Recovery, Crash-Loop, Zustellung an einen Test-Empfänger – hier über die Dispatcher-Logzeile statt eines ntfy-Mocks, Begründung im Architekturteil); §10 Kriterien 1–5 in der CI, 6 teilweise (Admin-Sicht; Nicht-Admin in Go/Playwright), 7 (präparierte Ping-Antwort) und 8 (DB-Größe) in den Go-Tests bzw. der manuellen Checkliste; `acceptance-tp5.md`, README, CLAUDE.md → Aufgabe 16.
- **Abweichungen von der Spec, für den Lead:** (1) kein lokaler ntfy-Mock, weil das panel im Produktionsmodus interne Ziele ablehnt; (2) `crash_window_min = 10` statt 5 (Spec §8 ist angepasst); (3) der Platten-Teil wird mit Log-Zeile übersprungen, wenn der Runner zu wenig Platz für die Hysterese hat.
- **Platzhalter:** keine; `p_sudo` ist konditional beschrieben, weil es aus dem TP4-Teil kommt.
