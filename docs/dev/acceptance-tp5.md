# Abnahme Teilprojekt 5 (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md` (§10).

Automatisch geprüft (CI):
- **panel (Go):** Server-List-Ping gegen einen Fake-Server (gültig, überlang, kaputte VarInts, falsche Typen, langsam, offen ohne Daten, zu viele oder ungültige Namen), TPS-Erkennung und -Parser (`tick query`, Paper `mspt`, Müll), Host-Leser mit präparierten `/proc`-Dateien, Sammler mit Fake-Clock (Takt, Auslassen bei Überlauf, Live-Modus, Parallelitätsgrenzen), Verdichtung und Löschen (Intervallgrenzen, Idempotenz, gewichtete Mittel), Alerts (Schonfrist, Daten-Sperre, Schalter, Hysterese, Erinnerung nach 24 h, panel-Neustart mit feuerndem Alert, gelöschter/gestoppter Server, Crash-Zähler-Reset), Rechte (`overview` nur mit `server.view`, Host nur für Admins), höchstens 721 Punkte pro Metrik, Audit.
- **agent:** `restart_count` aus Docker-Inspect.
- **Vertrag:** OpenAPI ↔ Routen, Fehlercodes, ein Mock pro Operation.
- **Frontend:** Vitest (Skalen, Lücken, Ausdünnen, Tooltip per Tastatur, Tab, Einstellungen, Dashboard) und Playwright gegen den Mock (`web/e2e/monitoring.spec.ts`).
- **Instanz-Abnahme** (`scripts/ci-instances.sh`) auf einem echten Paper-Server über die panel-API im Produktionsmodus: Live-Werte, Verlauf 1 h/30 d, Übersicht und Host-Werte; höchstens 6 RCON-Zeilen in 120 s ohne Zuschauer; `server.down` genau einmal, solange eine nftables-Regel nur die Pings des panels verwirft (Container läuft weiter), danach `server.recovered`; drei Abstürze → `server.crash_loop` ohne `server.down`, nach einem Fenster ohne Neustart `server.recovered`; `host.disk_low`/`host.disk_ok` über die Schwellen; mit ausgeschalteten Down-Alerts keine Meldung; Zustellung an den notify-Dispatcher (ntfy-Ziel `https://ntfy.invalid`, geprüft an der Logzeile `notify: delivery failed`); Audit.

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client, echter Zustellung über ntfy, panel-Neustarts und mehreren Tagen Laufzeit.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP5-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-panel -f`; nach dem Start keine Fehlermeldung mit `monitoring:`.
2. Konfiguration prüfen: `ssh claude@192.168.1.117 'sudo grep -E "monitoring_ping_addr|instances_dir" /etc/mc-panel/panel.toml'` → `monitoring_ping_addr = "127.0.0.1:25565"` und `instances_dir = "/srv/mc-panel"`.
3. Als `admin` anmelden. Server `survival` (Paper, aktuelle Version) läuft seit mindestens 5 min; ein zweiter Server (z. B. `creative`) ist gestoppt.
4. Benutzer `freund` hat nur `server.view` auf `survival` (aus TP3a).
5. Security Center → „Benachrichtigungen“: eine ntfy-Benachrichtigung (`https://ntfy.sh`, eigenes Topic, im Browser oder in der App abonniert) ist aktiv, „Test senden“ kommt an (aus TP4).
6. Einstellungen → Karte „Monitoring & Alerts“: Defaults 10 % / 10 GiB, „Server down nach (Minuten)“ 3, „Crash-Loop ab Neustarts“ 3 „innerhalb von (Minuten)“ 10; „Keine aktiven Alerts.“

Hilfsbefehle für „nicht erreichbar“ (verwirft nur die Pings des panels an mc-router; der Container läuft weiter, Spieler bleiben verbunden). Nie die Tabelle `inet mcpanel` des agents verwenden:

```bash
ssh claude@192.168.1.117 'sudo nft add table inet mcpanel_test && sudo nft add chain inet mcpanel_test output "{ type filter hook output priority 0; policy accept; }" && sudo nft add rule inet mcpanel_test output meta skuid mcpanel tcp dport 25565 drop'
ssh claude@192.168.1.117 'sudo nft delete table inet mcpanel_test'   # wieder erreichbar
```

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 1a | Dashboard (Admin) | als `admin` Dashboard öffnen | oben die Karte „Host“ mit CPU, RAM („x / y GiB“), „Load (1 min)“ und „Platte“ („x GiB frei von y GiB“) und dem Link „Monitoring-Einstellungen“; pro Server Spieler `x/max`, TPS, RAM-Balken zum Limit, CPU, Spieler-Sparkline der letzten Stunde und Statuspunkt; `creative` ohne Werte („–“, nie 0) |
| 1b | Werte mit Spieler | im Minecraft-Client mit `survival.mc.192-168-1-117.sslip.io` verbinden | nach ≤ 30 s zeigt das Dashboard `1/20` (bzw. `max-players`), der Tab „Monitoring“ im Live-Kopf den Spielernamen unter „Online-Spieler“; nach dem Trennen „Niemand online.“ |
| 1c | Tab „Monitoring“ | Server `survival` → Tab „Monitoring“; „Zeitraum“ 1 Stunde, 24 Stunden, 7 Tage, 30 Tage | Live-Kopf aktualisiert sich alle 5 s (TPS, MSPT, CPU, RAM, „Quelle: tick query“ bzw. „Quelle: Paper“); Diagramme „Spieler“ (mit „Plätze“), „TPS und MSPT“ (zwei Achsen), „CPU-Auslastung“, „Arbeitsspeicher“ mit Linie „Limit“; ab 24 Stunden Mittelwert mit Fläche min–max; Lücken statt Nullen, wo der Server gestoppt war; Tooltip per Maus und per Tastatur (Pfeiltasten, Esc); „Als Tabelle anzeigen“ zeigt dieselben Werte als Tabelle |
| 1d | Verlauf füllt sich | nach einem Tag erneut 24 Stunden und 7 Tage ansehen | der Verlauf reicht über den ganzen Tag; 7 Tage und 30 Tage zeigen Stundenpunkte |
| 2 | RCON-Rauschen | alle Monitoring-Tabs schließen, 5 min warten: `ssh claude@192.168.1.117 'sudo docker logs --since 5m mcpanel-mc-<id> 2>&1 \| grep -c "RCON Client"'`; danach den Tab „Monitoring“ öffnen und dasselbe für 1 min | ohne Zuschauer höchstens 10 Zeilen in 5 min (zwei pro Minute, eine Abfrage); mit offenem Tab mehr (TPS alle 5 s), 30 s nach dem Schließen wieder zwei pro Minute |
| 3a | Server down | Einstellungen → „Server down nach (Minuten)“ 1 → „Einstellungen speichern“ (Sudo); Hilfsbefehl „nicht erreichbar“ | nach 1–2 min eine ntfy-Nachricht „Server down: survival“ („… should be running but has not been reachable for 1 minutes.“); Dashboard: Statuspunkt „Nicht erreichbar“ und Badge „Server down“; die Karte „Monitoring & Alerts“ listet den Alert unter „Aktive Alerts“ („seit …“); `sudo docker ps` zeigt den Container weiter als laufend; der Spieler im Client bleibt verbunden |
| 3b | panel-Neustart mit feuerndem Alert | während 3a feuert: `ssh claude@192.168.1.117 'sudo systemctl restart mcpanel-panel'`, 2 min warten | keine zweite „Server down“-Nachricht; der Alert steht nach dem Neustart weiter unter „Aktive Alerts“ mit derselben Zeit „seit …“ |
| 3c | wieder online | Hilfsbefehl „wieder erreichbar“ | nach ≤ 30 s eine Nachricht „Server back online: survival“; Badge und Eintrag unter „Aktive Alerts“ verschwinden |
| 4a | Crash-Loop | dreimal `ssh claude@192.168.1.117 'sudo docker exec mcpanel-mc-<id> rcon-cli stop'`, jeweils warten, bis der Server wieder läuft (Docker startet ihn neu) | nach dem dritten Neustart eine Nachricht „Crash loop: survival“ („… restarted 3 times in the last 10 minutes.“), Badge „Crash-Loop“; **keine** „Server down“-Nachricht (nach jedem Neustart 5 min Schonfrist, solange der Crash-Loop feuert, kommt kein Down); 10 min nach dem letzten Neustart „Server stable again: survival“ |
| 4b | Down-Alerts aus | Tab „Monitoring“ → Schalter „Down-Alerts für diesen Server“ aus („Down-Alerts ausgeschaltet.“); 3a wiederholen und 3 min warten; dann 4a wiederholen | keine Nachricht, kein Badge (der Schalter betrifft „Server down“ und „Crash-Loop“); danach Hilfsbefehl „wieder erreichbar“, Schalter wieder an („Down-Alerts eingeschaltet.“) |
| 4c | Gestoppter Server | `survival` über „Stoppen“ anhalten, 5 min warten; danach starten | kein Down-Alert (der Server soll nicht laufen); der Tab zeigt den Server als gestoppt, ohne Live-Werte; nach dem Start 5 min Schonfrist |
| 4d | Daten-Sperre | `creative` gestoppt lassen; `survival` ein Backup wiederherstellen (TP4, dauert mit großer Welt lange genug) | während des Restores keine „Server down“-Meldung, obwohl der Server kurz gestoppt ist |
| 5 | Platte knapp | `ssh claude@192.168.1.117 'df -h /srv/mc-panel'`; in der Karte „Monitoring & Alerts“ „oder unter (GiB)“ über den freien Platz setzen (z. B. freier Platz + 5) → speichern; danach wieder 10 GiB | nach ≤ 1 min „Low disk space on the host“ (Nachricht mit „… GiB free of … GiB (… %) on the file system of the server data.“, ohne Host-Pfad); die Host-Karte im Dashboard in Warnfarbe mit „Der freie Platz liegt unter der Alert-Schwelle.“ und Badge „Platte knapp“; nach dem Zurücksetzen (mindestens 2 GiB und 2 Prozentpunkte Abstand) „Disk space OK again“ |
| 6a | Nur `server.view` | als `freund` Dashboard und Server `survival` öffnen | keine Host-Karte; nur `survival` mit Werten; Tab „Monitoring“ sichtbar, aber ohne Schalter „Down-Alerts für diesen Server“; keine Karte „Monitoring & Alerts“ in den Einstellungen |
| 6b | API-Sperre | als `freund` in den DevTools: `await fetch('/api/monitoring/host?range=1h').then(r => r.status)`, `await fetch('/api/monitoring/overview').then(r => r.json())` und `await fetch('/api/servers/<id von creative>/metrics?range=1h').then(r => r.status)` | `403`; die Übersicht enthält nur `survival` und `host: null`; `403` für `creative` |
| 7 | Einstellungen und Audit | in der Karte „Monitoring & Alerts“ „Platte knapp unter (%)“ 0 bzw. 51 und „innerhalb von (Minuten)“ 0 eingeben → speichern; dann gültige Werte speichern; Audit-Log mit Filter `monitoring.` bzw. `server.alerts_update` | ungültige Werte: Hinweis „Erlaubt sind 1 bis 50.“ bzw. „Erlaubt sind 1 bis 120.“, nichts gespeichert; gültige Werte verlangen Sudo und zeigen „Monitoring-Einstellungen gespeichert.“; Audit `monitoring.settings_update` mit `from`/`to` und `server.alerts_update` mit `down_alerts` und Ziel `server:<id>`; `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` meldet OK |
| 8 | Ohne Benachrichtigung | Benachrichtigung im Security Center deaktivieren, Karte „Monitoring & Alerts“ öffnen; danach wieder aktivieren | Warnung „Es ist keine aktive Benachrichtigung eingerichtet – Alerts werden erkannt, aber nicht verschickt.“ |
| 9 | Server löschen | einen Wegwerf-Server anlegen, starten, 5 min laufen lassen, dann löschen | keine Down-Meldung; `ssh claude@192.168.1.117 "sudo sqlite3 /var/lib/mc-panel/panel.db 'SELECT count(*) FROM metrics_raw WHERE series = <id>'"` → `0` (falls `sqlite3` fehlt: `sudo apt-get install -y sqlite3`) |
| 10 | DB-Größe | nach einigen Tagen: `ssh claude@192.168.1.117 'sudo du -h /var/lib/mc-panel/panel.db*'` und `sudo sqlite3 /var/lib/mc-panel/panel.db "SELECT count(*) FROM metrics_raw; SELECT count(*) FROM metrics_5m; SELECT count(*) FROM metrics_1h"` | Wachstum im erwarteten Rahmen: pro Server und Tag rund 23 000 Rohzeilen (8 Metriken × 2 880 Takte), die nach 24 h verschwinden; hochgerechnet auf 10 Server über 30 Tage deutlich unter 50 MB |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px): Host-Karte und Server-Karten mit Werten lesbar, Tab „Monitoring“ mit Bereichsauswahl und Diagrammen in voller Breite, Tooltip per Tippen, kein horizontales Scrollen der Seite.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
