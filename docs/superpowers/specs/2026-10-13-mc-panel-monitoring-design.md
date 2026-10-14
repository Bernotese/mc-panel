# mc-panel – Teilprojekt 5: Monitoring + Alerts (Design-Spec)

- **Datum:** 2026-10-13
- **Status:** umgesetzt (PRs #61, #62, #65–#67, 2026-10-14)
- **Autor:** Jannes (mit Claude)
- **Baut auf:** `2026-10-08-mc-panel-foundation-design.md` (TP1), `2026-10-10-mc-panel-instances-design.md` (TP2), `2026-10-11-mc-panel-console-players-design.md` (TP3a), `2026-10-11-mc-panel-files-design.md` (TP3b), `2026-10-12-mc-panel-backups-design.md` (TP4)

## 1. Ziel und Kontext

Teilprojekt 5 macht sichtbar, wie es den Servern und dem Host geht, und meldet kritische Zustände. Das panel sammelt Spieler online, TPS/MSPT, CPU und RAM pro Server sowie CPU, RAM, Load und freien Plattenplatz des Hosts, speichert 30 Tage Verlauf in gestufter Auflösung und zeigt beides im Dashboard und in einem neuen Tab „Monitoring“. Alerts gibt es für „Server down / Crash-Loop“ und „Platte knapp“, verschickt über die vorhandenen Benachrichtigungen (Discord, ntfy).

### 1.1 Erfolg

- Ein Nutzer mit `server.view` sieht pro Server live Spielerzahl mit Namen, TPS, MSPT, CPU und RAM und den Verlauf über 1 h / 24 h / 7 d / 30 d.
- Das Dashboard zeigt pro Server Kompaktwerte mit einer Spieler-Sparkline der letzten Stunde; Admins sehen zusätzlich eine Host-Karte.
- Fällt ein Server aus, der laufen soll, oder startet er wiederholt neu, kommt nach wenigen Minuten eine Meldung, und eine zweite, wenn er wieder läuft. Wird der Platz auf `/srv/mc-panel` knapp, kommt ebenfalls eine Meldung.
- Das Server-Log bekommt durch das Monitoring höchstens zwei RCON-Zeilen pro Minute (außer während jemand den Monitoring-Tab offen hat).

### 1.2 Leitlinie

Unverändert TP1 §3 und TP3b §1.2: Der root-agent bleibt schmal und verarbeitet keine Daten, die ein Container kontrolliert. Die Spielerzahl kommt deshalb nicht über den agent, sondern per Server-List-Ping aus dem unprivilegierten panel über mc-router; die Antwort des (möglicherweise bösartigen) Servers wird dort mit festen Grenzen gelesen. Der agent bekommt nur ein zusätzliches Feld im vorhandenen Status (`restart_count`).

### 1.3 Nicht-Ziele (TP5)

- Spieler-Sitzungen, Spielzeiten pro Spieler, Spielernamen im Verlauf.
- Alerts für Performance (TPS/MSPT) und Ressourcen (RAM/CPU); das Datenmodell erlaubt sie später ohne Migration der Zeitreihen.
- Prometheus-/OpenMetrics-Export, externe Dashboards.
- Netzwerk-Traffic, Plattenlast (IOPS), Metriken pro Prozess.
- Alert-Regeln pro Server außer dem Schalter „Down-Alerts“; Stummschalten auf Zeit.

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Sammler | Panel-Paket `internal/monitoring`, Takt 30 s (TPS 60 s) über `clock.Clock` | Nur das panel kennt Server, Rechte, Alerts und `notify`; keine neue Privilegierung |
| Spieler | Server-List-Ping an `127.0.0.1:25565` mit der FQDN des Servers im Handshake; mc-router leitet weiter | Kein Log-Eintrag pro Abfrage (anders als RCON), prüft zugleich die Erreichbarkeit über den Router; root parst nichts aus dem Container |
| TPS/MSPT | RCON über die vorhandene agent-Route, 1×/min, live 5 s nur mit Zuschauer | Kein anderer Weg ohne Plugin; 60 s halten das RCON-Rauschen klein |
| CPU/RAM pro Server | vorhandener agent-Status (`SampleStats`) | Bereits gesammelt, nur nicht gespeichert |
| Host | panel liest `/proc/stat`, `/proc/meminfo`, `/proc/loadavg` und `statfs(<instances_dir>)` selbst | Unprivilegiert lesbar; kein neuer agent-Endpunkt |
| Speicher | SQLite, Rohwerte 30 s (24 h), 5-min-Mittel (7 d), 1-h-Mittel (30 d) | Klein (< 50 MB bei 10 Servern), keine neue Abhängigkeit |
| Alerts | Global einstellbar (Admin), pro Server nur „Down-Alerts an/aus“; Zustand persistent; Meldung beim Auslösen, beim Lösen und alle 24 h | Einfach zu bedienen; kein Spam, kein Vergessen |
| Diagramme | Eigene SVG-Komponenten (`LineChart`, `Sparkline`) | Keine neue Abhängigkeit, CSP-tauglich, nur Linien nötig |

### 2.1 Verworfene Alternativen

- **Alles per RCON alle 30 s:** einfachste Umsetzung, aber rund 11 000 RCON-Zeilen pro Server und Tag im Log.
- **Prometheus `/metrics` + Grafana:** Standard, aber ein zusätzlicher Dienst auf dem VPS; Verlauf und Alerts lägen außerhalb des panels.
- **Sammler im agent:** der root-agent würde breiter und müsste Ping-Antworten aus Containern parsen.
- **Ping über die Container-IP:** das panel ist nicht im Docker-Netz `mcpanel-mc`; die IP kennt nur der agent. Der Weg über mc-router braucht nichts davon.
- **uPlot / Recharts:** mehr Funktionen (Zoom), aber eine neue Abhängigkeit für vier Liniendiagramme.

## 3. Architektur

```
Browser ─/api/monitoring/*, /api/servers/{id}/metrics*─▶ panel: internal/monitoring
                                                          · Sammler (30 s; TPS 60 s / live 5 s)
                                                          · Verdichtung (5 min), Löschen alter Werte
                                                          · Alert-Auswertung → notify
           ┌──────────────────────────────┬───────────────┴───────────────┬──────────────────────────┐
  agent (vorhanden):              mc-router (vorhanden)            agent RCON (vorhanden)     Host (panel selbst)
  GET /v1/instances               127.0.0.1:25565, SLP mit FQDN    tick query / mspt / tps    /proc, statfs
  + restart_count (neu)           → Spieler, Max, Namen-Auszug
```

- **panel:** neues Paket `internal/monitoring` (Sammler, Ping-Client `slp.go`, TPS-Parser, Host-Leser mit `_linux.go`/`_other.go`, Speicher, Verdichtung, Alerts, Handler).
- **agent:** `InstanceStatus` bekommt `restart_count` (aus Docker `RestartCount` der Inspect-Antwort, die der Status ohnehin abfragt). Keine neue Route.
- **Konfiguration:** `panel.toml` bekommt `monitoring_ping_addr` (Default `127.0.0.1:25565`, Dev `""` = aus). Ohne Ping-Adresse ist die Spielerzahl „unbekannt“ und „nicht erreichbar“ wird nur aus dem Container-Zustand abgeleitet.

### 3.1 Sammeln

- **Takt 30 s** (Ticker über `clock.Clock`), ein Durchlauf für alle Server mit Instanz:
  1. agent-Status (ein Aufruf für alle): Zustand, `started_at`, `restart_count`, `cpu_percent`, `memory_bytes`, `memory_limit_bytes`.
  2. Für laufende Container parallel (höchstens 4 gleichzeitig) der Ping (§3.2).
  3. Host-Werte (§3.4).
  4. Werte schreiben (eine Transaktion), Alerts auswerten (§5).
- **TPS/MSPT** alle 60 s für laufende Container, deren Ping antwortet; zusätzlich alle 5 s für Server mit Zuschauer (§6.2). Höchstens ein RCON-Aufruf pro Server gleichzeitig; Fehler werden nicht wiederholt, der Wert fehlt dann.
- Ein Durchlauf, der länger als 30 s dauert, lässt den nächsten Takt aus (kein Stau). Beim Beenden bricht der Sammler laufende Aufrufe ab.
- Ohne agent (`503`) werden für den Durchlauf keine Server-Werte geschrieben; Host-Werte schon.

### 3.2 Server-List-Ping

- TCP zu `monitoring_ping_addr`, Frist 3 s für den ganzen Austausch. Handshake mit Protokoll-Version `-1` (Status), Server-Adresse = FQDN des Servers, Port 25565, nächster Zustand 1; dann Status-Request, Antwort lesen. Kein Ping/Pong-Paar.
- Grenzen: VarInt-Längen höchstens 5 Bytes, Paketlänge ≤ 64 KiB, JSON-String ≤ 64 KiB; sonst Fehler. Ausgewertet werden nur `players.online` (0–100 000), `players.max` (0–100 000) und `players.sample[].name` (höchstens 12, je ≤ 16 Zeichen, nur `[A-Za-z0-9_]`, sonst ausgelassen). `description`, `favicon` und alles andere werden ignoriert.
- Ergebnis: `up = 1` bei gültiger Antwort, sonst `up = 0` (Container läuft, aber nicht erreichbar, z. B. Server startet noch, Router-Fehler).
- Namen aus dem Ping können anonymisiert sein (`hide-online-players`); die UI zeigt sie, wie sie kommen.

### 3.3 TPS/MSPT

- Minecraft ≥ 1.20.3 (aus der aufgelösten Version der Instanz, alle Arten): RCON `tick query`. Ausgewertet: „Target tick rate: X per second“ und „Average time per tick: Y ms“; `mspt = Y`, `tps = min(X, 1000 / Y)`.
- Die Quelle erkennt der Sammler pro Server durch Versuch: zuerst `tick query`, sonst bei paper/purpur `mspt` (Durchschnitt der letzten 5 s; `tps = min(20, 1000 / mspt)`), sonst keine TPS-Werte („nicht verfügbar“). Angehaltenes Spiel (`frozen`) → `tps = 0`.
- Ausgaben werden ohne `§`-Formatcodes gelesen; der Parser akzeptiert nur Zahlen im erwarteten Muster und Bereich (`tps` 0–1000, `mspt` 0–60 000), sonst fehlt der Wert. Testdaten stammen von echten Servern der Test-VM.
- RCON-Fristen: 10 s (Default des agent).

### 3.4 Host

- CPU: Differenz zweier Messungen aus `/proc/stat` (Zeile `cpu`) zwischen zwei Takten, als Prozent.
- RAM: `/proc/meminfo` `MemTotal` und `MemAvailable` → `memory_total`, `memory_used = total − available`.
- Load: `/proc/loadavg`, erster Wert.
- Platte: `statfs(<instances_dir>)`: `disk_total`, `disk_free` (für Nicht-root verfügbare Blöcke). `instances_dir` aus `panel.toml` (neuer Schlüssel `instances_dir`, Default `/srv/mc-panel`, Dev `.dev/mc`); das panel braucht nur Lesezugriff auf das Verzeichnis selbst (0755).
- macOS (`_other.go`): nur Platte über `statfs`, sonst „nicht verfügbar“.

## 4. Datenmodell (Migration 00007)

```sql
CREATE TABLE metrics_raw (
    series INTEGER NOT NULL,   -- 0 = Host, sonst servers.id
    metric TEXT    NOT NULL,
    at     INTEGER NOT NULL,   -- Unix ms, Takt-Zeitpunkt
    value  REAL    NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_5m (
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL, -- Beginn des Intervalls
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_1h (  -- wie metrics_5m
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL,
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE monitoring_settings (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    disk_min_percent INTEGER NOT NULL DEFAULT 10,           -- 1–50
    disk_min_bytes   INTEGER NOT NULL DEFAULT 10737418240,  -- 1 GiB – 10 TiB
    down_after_min   INTEGER NOT NULL DEFAULT 3,            -- 1–60
    crash_restarts   INTEGER NOT NULL DEFAULT 3,            -- 2–20
    crash_window_min INTEGER NOT NULL DEFAULT 10,           -- 1–120
    updated_at       INTEGER NOT NULL
);
INSERT INTO monitoring_settings (id, updated_at) VALUES (1, 0);
CREATE TABLE server_alerts (
    server_id   INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    down_alerts INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE alert_state (
    key           TEXT PRIMARY KEY,  -- z. B. "server.down:7", "server.crash_loop:7", "host.disk_low"
    firing        INTEGER NOT NULL,
    since         INTEGER NOT NULL,
    last_notified INTEGER NOT NULL,
    detail        TEXT NOT NULL DEFAULT ''
);
```

- Metriken pro Server: `up`, `players`, `players_max`, `tps`, `mspt`, `cpu_percent`, `memory_bytes`, `memory_limit_bytes`. Host (`series = 0`): `cpu_percent`, `memory_used`, `memory_total`, `load1`, `disk_free`, `disk_total`. Fehlende Werte werden nicht geschrieben (Lücke statt 0).
- Verdichtung alle 5 min: abgeschlossene 5-min-Intervalle aus `metrics_raw` → `metrics_5m`, abgeschlossene Stunden aus `metrics_5m` → `metrics_1h` (gewichtet nach `samples`), idempotent (`INSERT OR REPLACE`). Löschen: `metrics_raw` älter als 24 h, `metrics_5m` älter als 7 d, `metrics_1h` älter als 30 d. Beim Löschen eines Servers entfernt das panel seine Reihen (eigene Query im Lösch-Pfad, kein Fremdschlüssel wegen `series = 0`).
- Queries in `internal/store/queries/monitoring.sql`, Präfix `Monitoring…`.

## 5. Alerts

- **`server.down:<id>`** feuert, wenn `desired_state = running`, `down_alerts = 1`, keine Daten-Sperre (Entpacken/Restore, TP3b/TP4) und der Server seit `down_after_min` durchgehend nicht erreichbar ist: Container nicht `running` (auch `restarting`, `exited`) oder `up = 0`. Nach jedem Start/Neustart (laut `started_at`, also auch nach jedem Absturz-Neustart) gilt eine Schonfrist von 5 min (Minecraft braucht zum Start). Solange `server.crash_loop` für den Server feuert, wird `server.down` nicht zusätzlich gemeldet. Löst sich bei der ersten gültigen Ping-Antwort (bzw. ohne Ping-Adresse: Container `running` seit 2 min) mit `server.recovered`.
- **`server.crash_loop:<id>`** feuert, wenn `restart_count` innerhalb von `crash_window_min` um mindestens `crash_restarts` steigt (gemessen an den gespeicherten Werten; ein Neuanlegen des Containers setzt den Zähler zurück und zählt nicht). Löst sich, wenn im Fenster kein Neustart mehr war, mit `server.recovered` („Server stable again“). Bei `down_alerts = 0` entfällt auch dieser Alert (der Schalter betrifft beide Server-Alerts).
- **`host.disk_low`** feuert, wenn `disk_free < disk_min_bytes` oder `disk_free < disk_total × disk_min_percent / 100`; löst sich, wenn beides mit 2 Prozentpunkten bzw. 2 GiB Abstand wieder erfüllt ist (Hysterese), mit `host.disk_ok`.
- **Versand:** neue `notify.EventType` `server.down`, `server.recovered`, `server.crash_loop`, `host.disk_low`, `host.disk_ok`. Meldung beim Auslösen und beim Lösen; solange ein Alert feuert, alle 24 h eine Erinnerung. Der Zustand steht in `alert_state` und überlebt Neustarts des panels (kein erneutes „down“ nach einem Neustart, wenn der Alert schon gemeldet war). Titel nennen Server bzw. Host, Nachrichten den Grund ohne Host-Pfade.
- Ein gelöschter Server entfernt seine Alert-Zustände ohne Meldung. Ein Server mit `desired_state = stopped` löst einen feuernden Down-Alert ohne Meldung auf.

## 6. API

### 6.1 Routen

| Methode, Pfad | Regel | Antwort |
|---|---|---|
| `GET /servers/{id}/metrics?range=1h\|24h\|7d\|30d` | `Server(server.view)` | `{range, step_ms, series: {<metric>: [[at, avg, min, max] …]}}`; 1h → Rohwerte (min = max = avg), 24h → 5 min, 7d und 30d → 1 h |
| `GET /servers/{id}/metrics/live` | `Server(server.view)` | `{at, up, players, players_max, player_names, tps, mspt, cpu_percent, memory_bytes, memory_limit_bytes, tps_source}`; fehlende Werte `null`; `tps_source` ∈ `tick_query\|paper\|none` |
| `GET /monitoring/overview` | `Authenticated()` | `{servers: [{id, up, players, players_max, tps, cpu_percent, memory_bytes, memory_limit_bytes, sparkline: [[at, players] …], alerts: [<key>]}], host: {…}\|null}`; nur Server mit `server.view`; `host` nur für Admins |
| `GET /monitoring/host?range=…` | `Admin()` | wie `metrics`, Host-Reihen |
| `GET /monitoring/settings` | `Admin()` | `{disk_min_percent, disk_min_bytes, down_after_min, crash_restarts, crash_window_min, alerts: [{key, since, detail}]}` |
| `PUT /monitoring/settings` | `Admin().WithSudo()` | Werte wie oben (alle Pflicht) → `200` mit neuem Stand |
| `PUT /servers/{id}/alerts` | `Admin()` | `{down_alerts}` → `204` |

- `GET /servers/{id}` (vorhanden) bekommt `down_alerts` für Admins (für den Schalter im Tab).
- Ungültiger `range` → `400 validation_failed`; Server ohne Instanz → leere Reihen.

### 6.2 Live-Modus

- Jeder Aufruf von `metrics/live` markiert den Server für 30 s als „mit Zuschauer“: Der Sammler fragt dann TPS/MSPT alle 5 s ab (statt 60 s) und liefert in `live` die frischesten Werte; Spieler, CPU und RAM bleiben im 30-s-Takt (CPU/RAM sampelt der agent ohnehin). Live-Werte von TPS werden nicht zusätzlich gespeichert (der Verlauf behält den 60-s-Takt).
- Das normale API-Rate-Limit gilt; der Tab fragt alle 5 s.

### 6.3 Audit und Fehlercodes

- Audit: `monitoring.settings_update` (alt/neu), `server.alerts_update` (`{down_alerts}`), Ziel wie üblich (`settings`, `server:<id>`).
- Keine neuen Fehlercodes; `agent_unavailable` nur für `live`, wenn der agent für den aktuellen Takt nicht antwortete (die übrigen Routen liefern gespeicherte Werte).

## 7. Frontend

### 7.1 Dashboard

- Host-Karte (nur Admin) oben: CPU, RAM (genutzt/gesamt), Load, Platte (frei/gesamt) als Balken; Warnfarbe, wenn `host.disk_low` feuert; Link zu den Monitoring-Einstellungen.
- Pro Server in der vorhandenen Liste: Spieler `x/max`, TPS, RAM (Balken zum Limit), CPU, Spieler-Sparkline der letzten Stunde, Statuspunkt (läuft / nicht erreichbar / gestoppt), Badge bei feuerndem Alert. Abfrage alle 30 s.

### 7.2 Tab „Monitoring“

- Sichtbar mit `server.view` für Server mit Instanz. Live-Kopf (alle 5 s, nur solange der Tab offen ist): Spieler mit Namensliste, TPS, MSPT, CPU, RAM; „TPS für diese Version nicht verfügbar“ bei `tps_source = none`.
- Bereichsauswahl 1 h / 24 h / 7 d / 30 d; Diagramme Spieler, TPS + MSPT (zwei Achsen), CPU %, RAM (mit Limitlinie). Bei 24 h und mehr zeigt das Diagramm den Mittelwert und eine Fläche min–max.
- Admins: Schalter „Down-Alerts für diesen Server“.

### 7.3 Einstellungen

- Karte „Monitoring & Alerts“ (Admin): die fünf Werte mit Erklärung und Validierung wie §4; Liste der gerade feuernden Alerts; Hinweis, dass Meldungen über die eingerichteten Benachrichtigungen gehen (Link), und Warnung, wenn keine eingerichtet ist.

### 7.4 Diagramme

- Eigene Komponenten `Sparkline` und `LineChart` (SVG, `viewBox`, responsiv): Zeitachse in Browser-Zeit, y-Achse mit „schönen“ Teilstrichen, Gitter, Lücken bei fehlenden Werten (kein Verbinden über Lücken > 2 Schritte), Tooltip per Maus und Tastatur (Fokus auf dem Diagramm, Pfeiltasten), Farben aus den Theme-Tokens, hell/dunkel.
- Barrierefreiheit: `role="img"` mit `aria-label`-Zusammenfassung (aktuell, min, max) und ein Umschalter „Als Tabelle anzeigen“.
- Clientseitiges Ausdünnen auf höchstens 1 Punkt pro Pixelspalte (min/max erhalten).
- i18n `de`/`en`; MSW-Mocks mit generierten, deterministischen Zeitreihen für jeden Server und den Host.

## 8. Entwicklung, Betrieb und CI

- **Mac (`make dev`):** kein Ping (keine Ping-Adresse), Host nur Platte; TPS nur mit `MCPANEL_DEV_DOCKER=1`. Der Mock zeigt alles.
- **Produktion:** `panel.toml` `monitoring_ping_addr = "127.0.0.1:25565"` und `instances_dir`; das panel braucht `statfs` auf `instances_dir` (lesbar, 0755) und Lesezugriff auf `/proc` (systemd `ProtectProc=invisible` darf `/proc/stat`, `/proc/meminfo`, `/proc/loadavg` nicht verbergen – das tut es nicht, es betrifft nur fremde PIDs).
- **CI-Abnahme** (`scripts/ci-instances.sh`, echter Paper-Server): nach dem Start `up = 1`, `players = 0`, `players_max` aus `server.properties`, TPS zwischen 15 und 20 im Verlauf (nach ≥ 1 min), CPU/RAM > 0, Host-Werte vorhanden; dann Neustarts provozieren (dreimal `stop` per RCON bei `desired_state = running`) → `server.crash_loop` wird gemeldet (kein zusätzliches `server.down`) und `server.recovered`, wenn im Fenster kein Neustart mehr war; getrennt davon „nicht erreichbar“ (siehe unten) → genau ein `server.down` und danach `server.recovered`. Zeiten über `down_after_min = 1` und `crash_window_min = 10` verkürzt. „Nicht erreichbar“ erzeugt eine nftables-Regel, die nur die Pings des panels verwirft (ein gestoppter Container würde vom Abgleich sofort neu gestartet). Ein ntfy-Mock ist nicht möglich (das panel lehnt Ziele auf localhost/privaten Adressen ab); die CI prüft die Zustellversuche an einen Notifier `https://ntfy.invalid` im panel-Log und den Alert-Zustand über `/monitoring/settings`.
- Manuelle Checkliste `docs/dev/acceptance-tp5.md`.

## 9. Tests

- **Go:** SLP-Client gegen einen Fake-Server (gültig, überlang, kaputte VarInts, falsche Typen, langsam, Verbindungsabbruch, 13+ Namen, ungültige Namen); TPS-Parser mit echten Ausgaben (Vanilla, Paper, Purpur, Fabric, alt/neu) und Müll; Host-Leser mit präparierten `/proc`-Dateien; Sammler mit Fake-Clock und Fakes von agent, Ping und RCON (Takt, Auslassen bei Überlauf, Live-Modus an/aus nach 30 s, Parallelitätsgrenze); Verdichtung und Löschen (Grenzen der Intervalle, Idempotenz, gewichtete Mittel); Alerts (Schonfrist, Daten-Sperre, `down_alerts = 0`, Hysterese, Erinnerung nach 24 h, Neustart des panels mit feuerndem Alert, Server gelöscht/gestoppt, Crash-Zähler-Reset); Routen/Rechte/OpenAPI, Audit.
- **agent:** `restart_count` aus Inspect.
- **Vitest:** Skalen und Teilstriche, Lücken, Ausdünnen, Tooltip-Tastatur, Tabs, Einstellungen, Dashboard-Werte.
- **Playwright** gegen den Mock: Dashboard mit Host-Karte (Admin) und ohne (Nutzer), Tab mit Bereichswechsel und Tabelle, Einstellungen speichern mit Sudo.

## 10. Abnahmekriterien

1. Dashboard und Tab zeigen für einen laufenden Paper-Server Spieler, TPS/MSPT, CPU und RAM; der Verlauf füllt sich und ist über alle vier Bereiche abrufbar.
2. Das Server-Log zeigt ohne offenen Monitoring-Tab höchstens zwei RCON-Zeilen pro Minute durch das Monitoring.
3. Ein Server, der laufen soll und nicht läuft, löst nach `down_after_min` (plus Schonfrist nach Start) genau eine Meldung aus und eine „wieder online“-Meldung nach dem Wiederanlauf; ein Neustart des panels dazwischen erzeugt keine doppelte Meldung.
4. Drei Abstürze im Fenster lösen `server.crash_loop` aus; „Down-Alerts aus“ unterdrückt beides.
5. Fällt der freie Platz unter die Schwelle, kommt `host.disk_low`, nach Freigabe mit Abstand `host.disk_ok`.
6. Nutzer ohne Admin-Rolle sehen keine Host-Werte und nur Server mit `server.view`.
7. Eine präparierte Ping-Antwort (riesig, kaputt, langsam) beeinträchtigt weder das panel noch andere Server.
8. Die DB wächst bei 10 Servern über 30 Tage nicht über 50 MB durch Monitoring-Daten.
