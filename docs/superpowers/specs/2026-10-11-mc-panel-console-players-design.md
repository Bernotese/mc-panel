# mc-panel – Teilprojekt 3a: Konsole, Logs, Spieler (Design-Spec)

- **Datum:** 2026-10-11
- **Status:** Entwurf
- **Autor:** Jannes Göbel (mit Claude)
- **Baut auf:** `2026-10-08-mc-panel-foundation-design.md` (TP1), `2026-10-10-mc-panel-instances-design.md` (TP2)

## 1. Ziel und Kontext

Teilprojekt 3 („Live-Betrieb“, TP1 §1.1) ist zweigeteilt: **3a** (dieses Dokument) bringt Konsole, Live-Log und Spieler-Verwaltung; **3b** bringt Dateibrowser und Plugins/Mods (Modrinth, Hangar). Beide sind einzeln nutzbar und haben je eigene Spec, eigenen Plan und eigene PRs.

### 1.1 Erfolg

Ein Nutzer mit `server.console` öffnet die Konsole eines Servers und sieht die letzten 500 Log-Zeilen und danach das Log live, auch über Neustarts des Containers hinweg. Er schickt Befehle und sieht deren Antwort. Im Tab „Spieler“ sieht er Online-Spieler, Ops, Whitelist und Bans und kann kicken, bannen, entbannen, Op vergeben und die Whitelist pflegen – ohne Neustart des Servers.

### 1.2 Unveränderte Leitlinie

Ein kompromittiertes panel darf keinen Root auf dem Host bekommen (TP1 §3). Das panel bekommt weiterhin weder den Docker-Socket noch Zugriff auf `/srv/mc-panel/instances`; Logs, Befehle und Spielerlisten laufen über den agent, der jede Anfrage selbst prüft.

### 1.3 Nicht-Ziele (3a)

- Dateibrowser, Plugins/Mods, Herunterladen älterer Logdateien (`logs/*.log.gz`) – Teilprojekt 3b.
- Tab-Vervollständigung von Befehlen, interaktives Terminal (stdin/TTY).
- Eigene UI für IP-Bans (über die Konsole weiterhin möglich: `ban-ip`, `pardon-ip`).
- Spielerzahl auf dem Dashboard (jede Abfrage erzeugt RCON-Zeilen im Server-Log, §3.3); sie steht nur auf der Detailseite.
- Backups (TP4), Monitoring/Alerts (TP5).

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Rechte | `server.view`: Online-Spieler. `server.console`: Live-Log, Befehle, Spielerlisten, Spieler-Aktionen | Logs enthalten Spieler-IPs; wer Befehle senden darf, kann sich ohnehin `op` geben. Keine neue Berechtigung |
| Befehle | RCON über `docker exec rcon-cli <befehl>` im Container | itzg bringt `rcon-cli` mit und erzeugt das RCON-Passwort selbst (`/data/.rcon-cli.env`); argv ohne Shell; keine Änderung an der Container-Vorlage nötig |
| Log | Docker-Container-Log (`follow`) über den agent | Enthält alles, was der Server ausgibt; Rotation regelt Docker (TP2 §5.4: 3 × 10 MiB) |
| Browser ↔ panel | Server-Sent Events für das Log, normale `POST`-Requests für Befehle | CSRF, Rechte, Rate-Limit und Audit laufen über die bestehende Middleware; keine WebSocket-Bibliothek; `EventSource` verbindet sich selbst neu; Caddy reicht SSE ohne Konfiguration durch |
| Spielerlisten | Der Server ist maßgeblich (`ops.json`, `whitelist.json`, `banned-players.json`); Änderungen per RCON | Wirken sofort, im Spiel vergebene Rechte bleiben erhalten, und ein Absturz-Neustart setzt nichts zurück |
| Log-Darstellung | Eigene virtualisierte Log-Komponente statt xterm.js | Leicht, mobil bedienbar, Suche/Filter; ein Terminal-Emulator bringt ohne stdin keinen Mehrwert |

### 2.1 Verworfene Alternativen

- **WebSocket + stdin-Attach + xterm.js:** verlangt `OpenStdin` in der Container-Vorlage (Neuanlage aller Container), eine WebSocket-Bibliothek und Origin-/Rechte-/Audit-Prüfung pro Nachricht neben der Middleware; Spieler-Aktionen bräuchten trotzdem RCON.
- **Befehle über stdin, Log per SSE:** Befehle hätten keine direkte Antwort, Spieler-Funktionen bräuchten trotzdem RCON – zwei Befehlswege.
- **agent spricht RCON direkt mit der Container-IP:** der agent müsste das Passwort als root aus `/data` lesen und das RCON-Protokoll selbst implementieren; `rcon-cli` im Container erledigt beides.
- **panel bleibt maßgeblich für Ops/Whitelist (wie TP2):** Startet Docker den Container nach einem Absturz neu, schreibt itzg die alten Listen aus der Container-Umgebung zurück; Bans wären trotzdem nur im Spiel gespeichert.

## 3. Änderungen an Teilprojekt 2

### 3.1 Ops und Whitelist verlassen die Eigenschaften

- Die Schlüssel `ops` und `whitelist` entfallen aus der Allowlist (TP2 §7) in `internal/agent/instancespec` (panel und agent); wer sie noch sendet, bekommt `400 validation_failed`. `whitelist_enabled` bleibt (`ENABLE_WHITELIST`, Änderung → „Neustart nötig“).
- `instancespec.Env` setzt `OPS`, `WHITELIST`, `EXISTING_OPS_FILE` und `EXISTING_WHITELIST_FILE` nicht mehr. Ohne `OPS`/`WHITELIST` lässt itzg `ops.json` und `whitelist.json` unverändert (`scripts/start-setupRbac` im itzg-Image, geprüft 2026-10-11).
- Der agent leert `ops.json`/`whitelist.json` nicht mehr (`clearStaleLists` in `internal/agent/instances/datadir.go` entfällt).
- Neue Migration `internal/store/migrations/00004_players_in_game.sql`: `properties = json_remove(properties, '$.ops', '$.whitelist')` und `revision = revision + 1`, `updated_at` = Migrationszeitpunkt – nur für Zeilen, die einen der beiden Schlüssel enthalten. Laufende Server zeigen danach „Neustart nötig“; gestoppte legt der Abgleich ohne die Variablen neu an.
- Die Dateien im Datenverzeichnis bleiben unverändert und entsprechen nach TP2 genau den bisherigen Listen. Einzige Ausnahme: Namen einer **ausgeschalteten** Whitelist standen nur in der DB (TP2 hat `whitelist.json` dann geleert) und gehen verloren; sie waren nicht wirksam.
- Im UI entfallen die Felder Ops und Whitelist in Formular und Einstellungen (§7).

### 3.2 Keine Änderung an der Container-Vorlage sonst

`ENABLE_RCON=true` bleibt (TP2 §5.4). RCON ist nur im internen Netz `mcpanel-mc` erreichbar und durch das zufällige, bei jedem Start neu erzeugte itzg-Passwort geschützt.

### 3.3 RCON-Rauschen im Log

Jeder RCON-Aufruf erzeugt im Server-Log zwei Zeilen (`Thread RCON Client /… started` und `… shutting down`; beobachtet mit Paper auf der Test-VM, Vanilla verhält sich gleich). Die Konsole blendet sie standardmäßig aus (§7.2); die Online-Spieler-Abfrage wird zwischengespeichert (§6.3).

## 4. Architektur

```
Browser ──SSE /console/stream──▶ panel internal/console ──NDJSON-Stream──▶ agent ──▶ Docker: GET /containers/{id}/logs?follow
        ──POST /console─────────▶   · Rechte, CSRF, Limits    ──JSON──────▶       ──▶ Docker: exec rcon-cli <befehl>
        ──GET/POST /players─────▶   · Audit, Parser            ──JSON──────▶       ──▶ /srv/mc-panel/instances/<id>/*.json (os.Root)
```

- **panel:** neues Paket `internal/console` mit `Handler` und `Routes(api *rbac.Router)`, verdrahtet in `cmd/panel/app.go`. Es nutzt den agent-Client (`internal/agent`) und für Server-Existenz/Zustand `internal/servers` bzw. die DB. `internal/servers` wächst dadurch nicht.
- **agent:** neue Methoden am Instanz-Manager (`internal/agent/instances`) für Logs, RCON und Spielerlisten; Docker-Aufrufe in `internal/agent/docker`; Anfragen und Validierung in `internal/agent/agentapi`.

## 5. agent

### 5.1 API

Muster wie TP2 §5.1: feste Pfade, `POST` mit JSON-Body, IDs im Body, `DisallowUnknownFields`, Peer-UID-Prüfung, Body-Limit 16 KiB.

| Methode, Pfad | Body | Antwort |
|---|---|---|
| `POST /v1/instances/logs` | `{id, tail, since}` | `200`, Stream `application/x-ndjson` |
| `POST /v1/instances/rcon` | `{id, command}` | `200 {output}` |
| `POST /v1/instances/players` | `{id}` | `200 {ops, whitelist, bans, errors}` |

Fehler wie bisher als `agentapi.ErrorResponse` (`{error, code}`); neue Werte für `code` als Konstanten in `agentapi`: `400` (Validierung, ohne Code), `404` mit `not_found` (Container `mcpanel-mc-<id>` bzw. Datenverzeichnis fehlt), `409` mit `not_running` (Container läuft nicht; nur `rcon`), `429` mit `busy` (Limit, vorhandener Code), `503` mit `docker_unavailable` (vorhanden), `503` mit `rcon_unavailable` (nur `rcon`).

### 5.2 Logs

- `id` > 0; `tail` 0–1000; `since` ≥ 0 = Docker-Zeitstempel in Nanosekunden seit Unix-Epoche (0 = ohne). Ist `since` > 0, wird `tail` ignoriert.
- Docker-Aufruf: `GET /containers/mcpanel-mc-<id>/logs?follow=1&stdout=1&stderr=1&timestamps=1&tail=<tail>[&since=<s>.<ns>]`. Der Container hat kein TTY, die Antwort ist der Docker-Multiplex-Stream (8-Byte-Header je Frame); ein gemeinsamer Demultiplexer (§5.5) trennt ihn.
- Jede Zeile wird als eine NDJSON-Zeile geschrieben: `{"ts": <ns>, "stream": "out"|"err", "line": "<text>"}`. `ts` stammt aus dem Docker-Zeitstempel; `line` ohne Zeitstempel und ohne abschließendes `\n`/`\r`, auf 8 KiB gekürzt (an einer UTF-8-Grenze), ungültiges UTF-8 ersetzt durch U+FFFD.
- Der Stream endet, wenn Docker ihn beendet (Container gestoppt, entfernt) oder der Aufrufer trennt (Kontext). Docker beendet `follow` auch, wenn der Container neu angelegt wird; der Aufrufer (panel) verbindet sich neu (§6.2).
- Fristen: Die globalen `ReadTimeout`/`WriteTimeout` des agent-Servers (5 s/10 s) gelten für diesen Pfad nicht; der Handler setzt per `http.ResponseController` vor jedem Schreiben eine Frist von 30 s und flusht nach jedem Schreiben.
- Höchstens 16 gleichzeitige Log-Streams im agent, sonst `429`.

### 5.3 RCON

- `id` > 0; `command`: 1–1000 Bytes, gültiges UTF-8, kein Zeichen < `0x20` und kein `0x7f` (also auch kein Zeilenumbruch oder Tab), nicht nur Leerzeichen, und kein führendes `-` (nach optionalen Leerzeichen und höchstens einem `/`), damit der Befehl nie als Option von `rcon-cli` gelesen wird.
- Ablauf: Container inspizieren (fehlt er → `404 not_found`, läuft er nicht → `409 not_running`), `POST /containers/{id}/exec` mit `{"Cmd": ["rcon-cli", "--", command], "User": "<uid>:<gid>", "WorkingDir": "/data", "Env": ["HOME=/data"], "AttachStdout": true, "AttachStderr": true, "AttachStdin": false, "Tty": false}`, dann `POST /exec/{id}/start` mit `{"Detach": false, "Tty": false}` (Antwort-Body ist der Multiplex-Stream, kein Hijack), danach `GET /exec/{id}/json` für den Exit-Code.
- `uid:gid` ist `mcpanel-mc` (wie der Container selbst). `rcon-cli` steht fest im Code; nur die Integrationstests tauschen es über die Manager-Konfiguration (`RconCommand`, Default `rcon-cli`) gegen ein busybox-Programm.
- Timeout 10 s für den gesamten Ablauf; Ausgabe (stdout und stderr zusammen) höchstens 64 KiB, danach abgeschnitten mit Hinweis `[… gekürzt]`.
- Exit-Code ≠ 0 (z. B. RCON während des Starts noch nicht bereit) → `503` mit Code `rcon_unavailable` und den ersten 500 Zeichen der Ausgabe in `error`. Exit-Code 0 → `200 {output}` mit der Ausgabe als Text (Minecraft-`§`-Formatcodes bleiben erhalten).
- RCON-Aufrufe laufen nicht über die serielle Warteschlange der Instanz (TP2 §5.1), weil sie den Container-Zustand nicht ändern. Höchstens 4 gleichzeitige Aufrufe pro Instanz, sonst `429`. Beim Herunterfahren bricht der agent laufende Konsolen-Aufrufe und Spielerlisten-Lesungen ab (das panel sieht eine abgebrochene Verbindung und meldet `503 agent_unavailable`).

### 5.4 Spielerlisten

- Liest `ops.json`, `whitelist.json` und `banned-players.json` aus `/srv/mc-panel/instances/<id>`; funktioniert auch bei gestopptem oder fehlendem Container (fehlt das Verzeichnis → `404`).
- Das Verzeichnis gehört dem Container-Benutzer; der root-agent liest also Dateien, die ein kompromittierter Server kontrolliert. Deshalb:
  - Öffnen über `os.Root` auf dem Datenverzeichnis (keine Auflösung von Symlinks aus dem Verzeichnis heraus, kein `..`), mit `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` (kein Hängen an einer FIFO, kein Folgen eines Symlinks als letztem Element).
  - Nach dem Öffnen `fstat`: nur reguläre Dateien, höchstens 1 MiB; gelesen werden höchstens 1 MiB + 1 Byte.
  - Inhalte werden geparst und nur die Felder unten weitergegeben; Strings auf 256 Zeichen gekürzt, Listen auf 1000 Einträge (das Dekodieren endet nach 1000 Einträgen).
  - Höchstens 2 Lesungen gleichzeitig im agent, sonst `429 busy`.
- Antwort:
  - `ops`: `[{uuid, name, level}]`
  - `whitelist`: `[{uuid, name}]`
  - `bans`: `[{uuid, name, reason, source, created, expires}]` (`created`/`expires` als Text wie in der Datei, z. B. `2026-10-11 09:32:51 +0000` bzw. `forever`)
  - `errors`: Namen der Dateien, die vorhanden, aber unlesbar sind (Symlink, keine reguläre Datei, zu groß, kein gültiges JSON); die zugehörige Liste ist dann leer. Eine fehlende Datei ist kein Fehler, sondern eine leere Liste.

### 5.5 Docker-Client

Neu in `internal/agent/docker`, sonst keine weiteren Aufrufe (kein Attach, kein Exec mit TTY oder stdin):

| Methode | Docker-API |
|---|---|
| `ContainerLogs(ctx, id, opts) (io.ReadCloser, error)` | `GET /containers/{id}/logs` (Stream, ohne Client-Timeout; nur Kontext) |
| `ExecCreate(ctx, id, cfg) (string, error)` | `POST /containers/{id}/exec` |
| `ExecStart(ctx, execID) (io.ReadCloser, error)` | `POST /exec/{id}/start` |
| `ExecInspect(ctx, execID) (ExecInfo, error)` | `GET /exec/{id}/json` |
| `Demux(r io.Reader) (Frame, error)` bzw. Leser pro Frame | Multiplex-Format: 1 Byte Stream (1 = stdout, 2 = stderr), 3 Byte Null, 4 Byte Länge (Big Endian), dann Nutzdaten; Frame-Länge höchstens 1 MiB, sonst Fehler |

## 6. panel

### 6.1 API

Alle Routen über `*rbac.Router`, beschrieben in `api/openapi.yaml`, mit MSW-Mock.

| Route | Regel | Inhalt |
|---|---|---|
| `GET /servers/{id}/console/stream` | `Server(server.console, "id")` | SSE (§6.2) |
| `POST /servers/{id}/console` | `Server(server.console, "id")` | `{command}` → `200 {output}` (§6.4) |
| `GET /servers/{id}/players` | `Server(server.view, "id")` | `{available, online: [{name, uuid}], max}` (§6.3) |
| `GET /servers/{id}/players/lists` | `Server(server.console, "id")` | `{ops, whitelist, bans, errors}` wie §5.4 |
| `POST /servers/{id}/players/actions` | `Server(server.console, "id")` | `{action, name, reason?}` → `200 {output}` (§6.5) |

- Nicht eingerichtete Server (ohne `instances`-Zeile) → `404 not_found` auf allen fünf Routen.
- agent nicht erreichbar → `503 agent_unavailable`; Docker nicht verfügbar → `503 docker_unavailable` (Ausnahme: `GET /players` antwortet dann `available: false`).

### 6.2 Log-Stream (SSE)

- Antwort `text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no`. Die Fristen des panel-Servers (`WriteTimeout` 60 s) gelten nicht; vor jedem Schreiben setzt der Handler per `ResponseController` 30 s und flusht danach.
- Events:
  - `event: line`, `id: <ts>` (Docker-Zeitstempel in ns), `data: {"ts": <unix-ms>, "stream": "out"|"err", "line": "…"}`.
  - `event: state`, `data: {"running": true|false}` – beim ersten erfolgreichen Öffnen des agent-Streams `true`, bei dessen Ende `false`.
  - Alle 15 s ein Kommentar (`: ping`) als Heartbeat.
- Start: Mit Header `Last-Event-ID` (gültige Zahl) öffnet das panel den agent-Stream mit `since` = diesem Wert + 1 ns, sonst mit `tail` = 500. Der Query-Parameter `tail` (0–1000) überschreibt den Default.
- Endet der agent-Stream oder schlägt das Öffnen fehl (`404`, `docker_unavailable`, agent weg), sendet das panel einmalig `state {running: false}` und versucht es erneut mit `since` = letzter gesehener Zeitstempel. So läuft das Log über Neustarts und Neuanlagen des Containers weiter, ohne doppelte Zeilen. Die Wartezeit beginnt bei 3 s und verdoppelt sich bis 10 s, solange weder eine Zeile noch ein Zustandswechsel ankommt (gestoppter Server, agent weg oder ausgelastet); jede Zeile bzw. jeder Zustandswechsel setzt sie zurück. Ein `429 busy` des agent wird auf Info-Ebene geloggt.
- Prüfung während des Streams (über `clock.Clock`, alle 30 s): Session gültig (nicht abgelaufen, nicht widerrufen, Stufe voll angemeldet), Benutzer existiert, Recht `server.console` (bzw. Admin) besteht noch, Server existiert noch. Fällt eine Prüfung durch, schließt das panel den Stream. Der Stream verlängert die Session nicht (`Touch` nur beim Öffnen wie bei jedem Request).
- Höchstens 4 gleichzeitige Streams pro Benutzer im panel, sonst `429 rate_limited`. Beim Herunterfahren schließt das panel alle Streams; das Herunterfahren wartet höchstens 20 s (länger als der RCON-Aufruf mit 15 s).

### 6.3 Online-Spieler

- Das panel ruft über den agent `rcon` mit `list uuids` auf und parst die Antwort. Formate (Beispiele aus echten Servern in `internal/console/testdata/`):
  - `There are 2 of a max of 20 players online: Alex (uuid), Steve (uuid)`
  - `There are 0 of a max of 20 players online: `
  - ohne UUIDs (ältere Versionen ignorieren `uuids`): `… online: Alex, Steve`
  - `§`-Formatcodes werden vor dem Parsen entfernt.
- Antwort: `available: true`, `online` (Name; `uuid` oder `null`), `max`. Läuft der Server nicht, antwortet RCON nicht oder ist das Format unbekannt → `available: false`, `online: []`, `max: null` (kein HTTP-Fehler).
- Zwischenspeicher pro Server 10 s (Erfolg und `available: false`), damit mehrere Betrachter kein zusätzliches RCON-Rauschen erzeugen.

### 6.4 Befehle

- Das panel entfernt ein führendes `/` und prüft wie der agent (§5.3); Verstoß → `400 validation_failed`.
- Rate-Limit: 30 Befehle pro Minute pro Benutzer (Token-Bucket, Burst 10, Muster wie `internal/servers/powerlimit.go`), gemeinsam für Befehle und Spieler-Aktionen; Überschreitung → `429 rate_limited` mit `Retry-After`.
- Fehler: `409 instance_not_running` (agent `not_running`), `503 rcon_unavailable`.
- Der RCON-Aufruf wird nicht abgebrochen, wenn der Browser die Verbindung verlässt; der Audit-Eintrag (`server.console_command`, §6.7) folgt der Antwort des agent, nicht dem Verbleib des Clients. Das gilt ebenso für Spieler-Aktionen (§6.5).
- Kein Sudo (würde bei jedem Befehl stören).

### 6.5 Spieler-Aktionen

`action` ist ein Enum; das panel baut den RCON-Befehl, Nutzertext wird nie als Befehl übernommen:

| `action` | RCON-Befehl |
|---|---|
| `kick` | `kick <name>[ <reason>]` |
| `ban` | `ban <name>[ <reason>]` |
| `pardon` | `pardon <name>` |
| `op` | `op <name>` |
| `deop` | `deop <name>` |
| `whitelist_add` | `whitelist add <name>` |
| `whitelist_remove` | `whitelist remove <name>` |

- `name`: `^[A-Za-z0-9_]{3,16}$`. `reason`: nur bei `kick`/`ban` erlaubt, sonst `400`; höchstens 200 Zeichen, druckbar, ohne Steuerzeichen.
- Es gelten Rate-Limit und Fehler wie §6.4. Die Antwort enthält die RCON-Ausgabe (z. B. `Made Steve a server operator`).
- `whitelist add` und `op` lösen den Namen beim Server über Mojang auf (Online-Modus); unbekannte Namen beantwortet der Server mit einem Text, der unverändert zurückkommt.

### 6.6 Fehlercodes

Neu in `internal/httpx/errors.go` und im Enum des Vertrags:

| Code | Status | Wann |
|---|---|---|
| `instance_not_running` | 409 | Befehl oder Spieler-Aktion bei nicht laufendem Server |
| `rcon_unavailable` | 503 | RCON antwortet nicht (Server startet noch, RCON aus) |

### 6.7 Audit

- `server.console_command`: Ziel Server, Details `{command}` (wie gesendet, ohne führendes `/`), Ergebnis `ok` oder `failed` (bei `instance_not_running`, `rcon_unavailable`, agent-Fehlern).
- `server.player_action`: Ziel Server, Details `{action, name, reason?}`, Ergebnis wie oben.
- Neu als Konstanten in `internal/audit/types.go`: `ActionServerConsoleCommand`, `ActionServerPlayerAction`. Ablehnungen durch Rechte, Validierung oder Rate-Limit werden nicht auditiert (wie TP1/TP2).
- Öffnen des Log-Streams wird nicht auditiert.

## 7. Frontend

Bestehende shadcn-Komponenten; Texte `de`/`en`; mobil bedienbar.

### 7.1 Detailseite `/servers/$id`

Tabs (nur mit passendem Recht sichtbar): Übersicht · **Konsole** · **Spieler** · Einstellungen · Löschen.

- **Übersicht** (`server.view`): neue Karte „Online-Spieler“ mit `Anzahl / Max` und Namen; Abfrage alle 15 s, nur solange die Seite sichtbar ist; bei `available: false` „nicht verfügbar“.
- **Einstellungen** (Admin): Felder Ops und Whitelist entfallen; `whitelist_enabled` bleibt mit Verweis auf den Spieler-Tab. Dasselbe gilt für `/servers/new`.

### 7.2 Konsole (`server.console`)

- **Log-Ansicht:** virtualisierte Liste (`@tanstack/react-virtual`, einzige neue Abhängigkeit), Ringpuffer von 5000 Zeilen.
- **Darstellung:** eigener Parser für ANSI-SGR-Farben (16 Farben, fett) und Minecraft-`§`-Codes zu Spans; kein `dangerouslySetInnerHTML`. Zeilen mit `WARN` bzw. `ERROR` sind farblich hervorgehoben; stderr-Zeilen ebenso.
- **Werkzeuge:** Textfilter; Schalter „RCON-Meldungen ausblenden“ (Standard an, filtert die Zeilen aus §3.3); Auto-Scroll, der beim Hochscrollen pausiert, mit Knopf „Neue Zeilen ↓“; Verbindungsanzeige (verbunden, Server gestoppt, verbinde neu, „Zu viele offene Konsolen – schließe eine andere.“ bei `429` des Streams); „Kopieren“ der sichtbaren Zeilen.
- **Befehlszeile:** am unteren Rand (auch mobil), Verlauf mit ↑/↓ (nur im Speicher, 100 Einträge). Ein gesendeter Befehl erscheint als eigener, abgesetzter Block `› befehl` mit der RCON-Antwort darunter, eingereiht in die Log-Ansicht. Bei gestopptem Server sowie bei `forbidden`/`not_found` des Streams gesperrt, mit Hinweis. Unter der Zeile: „`stop` beendet den Server, Docker startet ihn sofort neu – zum Stoppen den Power-Button verwenden.“
- **Verbindung:** `EventSource` (Same-Origin, Cookie). Bei Abbruch verbindet der Browser selbst neu (mit `Last-Event-ID`); bei `401`/`403` schließt die Ansicht die Verbindung und zeigt den passenden Hinweis.

### 7.3 Spieler (`server.console`)

- **Online:** Name, Aktionen Kick, Ban (Dialog mit optionalem Grund), Op/Deop.
- **Whitelist:** Name hinzufügen, entfernen; Hinweis, ob die Whitelist aktiv ist, mit Link zur Einstellung (Admin).
- **Ops:** hinzufügen, entfernen; Level anzeigen.
- **Bans:** Name, Grund, von, seit, bis; Entbannen.
- Ops und Whitelist zeigen eine Warnung „Neustart nötig“, solange der Server noch mit der alten Umgebung (TP2) läuft; die Karte „Online“ zeigt bei gestopptem Server keine Liste. Die Beschriftung der Op-Aktionen lautet „Op“ bzw. „Deop“. Die Tab-Leiste scrollt auf dem Handy horizontal.
- Listen sind auch bei gestopptem Server lesbar; Ändern nur bei laufendem Server (Knöpfe sonst gesperrt, Hinweis). Unlesbare Dateien (`errors`) zeigen einen Hinweis an der betroffenen Liste.
- Nach jeder Aktion: Toast mit der RCON-Antwort, danach Neuladen von Listen und Online-Spielern.

### 7.4 Mocks

MSW-Mocks für alle neuen Operationen. Der Stream-Mock liefert über einen `ReadableStream` einige Startzeilen (inkl. ANSI-, `§`- und RCON-Zeilen) und danach alle 2 s eine Zeile; Befehle antworten mit Beispieltext. `make preview` zeigt so Konsole und Spieler-Tab ohne Backend.

## 8. Entwicklung, CI und Abnahme

- **Mac:** nur `make check` (CLAUDE.md). `make dev` läuft ohne Docker: Konsole und Spieler zeigen „Docker nicht verfügbar“; die UI-Vorschau (Mock-API) zeigt alles.
- **CI:** wie bisher; zusätzlich neue Fälle in `dockerintegration` (§9) und in `scripts/ci-instances.sh`.
- **Manuelle Abnahme:** auf der Test-VM nach `docs/dev/acceptance-tp3a.md` (neu).

## 9. Tests

| Ebene | Inhalt |
|---|---|
| agent `docker` | Fake-Docker-Server (`httptest` auf Unix-Socket): Demultiplexer (Header über Lesegrenzen geteilt, Zeilen über Frame-Grenzen, Frame > 1 MiB, unbekannter Stream-Typ), `ContainerLogs` mit follow/since/tail und Ende beim Stopp, Exec-Lebenszyklus mit Exit-Code, Timeouts |
| agent `agentapi`/Server | Tabellentests für `rcon` (Steuerzeichen, Zeilenumbruch, Tab, ungültiges UTF-8, 0 und 1001 Bytes, nur Leerzeichen, unbekannte Felder) und `logs` (`tail`/`since`-Grenzen); Stream-Limit `429`; RCON-Limit pro Instanz; Frist pro Schreibvorgang; `not_running`; `rcon_unavailable`; Zeilenkürzung und UTF-8-Ersetzung |
| agent Spielerlisten | Symlink aus dem Verzeichnis heraus, Symlink als letztes Element (z. B. auf `/etc/shadow`), FIFO, Verzeichnis statt Datei, > 1 MiB, kaputtes JSON, fehlende Datei (kein Fehler), Kürzung von Strings und Listen |
| agent Integration | `dockerintegration` (Linux, busybox): Log-Stream eines echten Containers inkl. Ende beim Stopp und `since`; Exec über `RconCommand` = busybox-Programm mit Ausgabe und Exit-Code ≠ 0 |
| `instances` (CI) | `scripts/ci-instances.sh`: Paper-Server liefert über den agent `list`; der Log-Stream enthält `Done (`; nach `whitelist add` und `op` zeigt `players` die Einträge; der Container hat kein `OPS`/`WHITELIST`/`EXISTING_*_FILE` mehr |
| panel `console` | Fake-agent + `storetest`: Rechte (view vs. console, Admin, fremder Server → `403`), SSE-Format, `Last-Event-ID` → `since`, Neuverbindung nach Stream-Ende mit `state`-Events, Schließen bei widerrufener Session/entzogenem Recht/gelöschtem Server (Fake-Clock), Stream-Limit, Rate-Limit, Audit-Einträge, Tabelle Aktion → RCON-Befehl, Validierung von `name`/`reason`, Parser `list uuids` mit Beispielen aus `testdata/`, Zwischenspeicher 10 s |
| Migration | `ops`/`whitelist` entfernt, `revision` nur bei betroffenen Zeilen erhöht, andere Eigenschaften unverändert |
| `instancespec` | `ops`/`whitelist` werden abgelehnt; `Env` enthält kein `OPS`/`WHITELIST`/`EXISTING_*_FILE` (Golden-Test angepasst) |
| Vertrag | bestehende Tests: OpenAPI ↔ Routen, Fehlercodes ↔ Enum, Mock pro Operation |
| Frontend | Vitest: ANSI-/`§`-Parser, Ringpuffer, Befehlsverlauf, RCON-Filter, Online-Karte; Playwright gegen Mock: Konsole öffnen → Zeilen erscheinen → Befehl senden → Antwort-Block; Spieler-Tab → Kick → Toast |

Konventionen aus TP1/TP2 gelten unverändert (nur Standardbibliothek in Go-Tests, `clock.Clock`, Kontexte überall).

## 10. Abnahmekriterien

Automatisch in der CI: 1 sowie die agent-Seite von 2 und 4 (`scripts/ci-instances.sh`). Manuell durch den Nutzer auf der Test-VM: 2–6 (`docs/dev/acceptance-tp3a.md`).

1. Alle CI-Jobs grün.
2. Die Konsole zeigt das Live-Log; `say hallo` erscheint im Spiel, `list` liefert eine Antwort. Ein Neustart des Servers unterbricht die Ansicht nicht dauerhaft, und es erscheinen keine doppelten Zeilen.
3. Ein Nutzer mit nur `server.view` sieht die Online-Spieler, aber weder Konsole noch Spielerlisten (UI ohne Tabs, API `403`).
4. Whitelist, Op, Kick, Ban und Entbannen wirken ohne Neustart. Im Spiel vergebene Ops erscheinen im Spieler-Tab und bleiben nach einem Neustart des Servers erhalten.
5. Entzieht ein Admin einem Nutzer bei offener Konsole das Recht oder wird dessen Session beendet, endet der Stream innerhalb von 30 s.
6. Alle Befehle und Spieler-Aktionen stehen im Audit-Log; `panel audit verify` meldet OK.
