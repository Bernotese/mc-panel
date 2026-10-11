# Abnahme Teilprojekt 3a (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (§10).

Automatisch geprüft (CI): Unit- und Integrationstests (agent: Docker-Demultiplexer, Log-Stream, Exec, Spielerdateien; panel: SSE, Neuverbindung, erneute Prüfung, Rechte, Audit, Parser), Docker-Integration (`dockerintegration`), Instanz-Abnahme über den agent (`scripts/ci-instances.sh`: RCON, Log-Stream inkl. `since`, Spielerlisten nach `whitelist add`/`op`/`ban`/`pardon`, `409 not_running`, präparierte Spielerdateien), Playwright gegen den Mock (Konsole, Spieler-Tab).

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client und echter Session.

## Vorbereitung

1. **Update aus TP2:** Nach dem Update jeden Server mit „Neustart nötig“ über das panel neu starten, bevor Ops/Whitelist im Spieler-Tab geändert werden. Bis dahin tragen laufende Container noch `OPS`/`WHITELIST`/`EXISTING_*_FILE=SYNCHRONIZE`; bei einem Absturz oder `stop` aus der Konsole startet Docker sie an Ort und Stelle neu, und itzg überschreibt `ops.json`/`whitelist.json` aus der alten Umgebung (Änderungen im Spieler-Tab gingen still verloren).
2. Deploy von `main`: `make deploy-testvm` (alle TP3a-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>, Basisdomain `mc.192-168-1-117.sslip.io`.
3. Läuft dort schon ein Server aus TP2 mit Ops oder Whitelist in den Eigenschaften: vor dem Deploy notieren, welche Namen eingetragen waren (Prüfung M unten).
4. Als `admin` anmelden. Gibt es noch keinen Paper-Server `survival`, ihn anlegen (Paper, aktuelle Version, 4 GiB) und starten.
5. Zwei Benutzer anlegen (Benutzer → Neu, Sudo): `freund` mit nur `server.view` auf `survival`, `helfer` mit `server.view` und `server.console` auf `survival`. Beide melden sich einmal an und richten TOTP ein.
6. Minecraft Java Edition auf einem Rechner im selben Netz wie die VM; Server-Adresse `survival.mc.192-168-1-117.sslip.io`. Der eigene Spielername heißt unten `<ich>`.
7. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-panel -f`.

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 2a | Live-Log | als `helfer` Server `survival` → Tab „Konsole“ | die letzten Zeilen des Server-Logs erscheinen sofort, danach neue Zeilen live (z. B. beim Betreten des Servers mit dem Client: `<ich> joined the game`); Verbindungsanzeige „verbunden“ |
| 2b | Befehle | in der Konsole `say hallo` senden, danach `list` | `hallo` erscheint im Spiel-Chat (`[Rcon] hallo` bzw. `[Server] hallo`); `list` zeigt einen Antwort-Block `› list` mit `There are 1 of a max of 20 players online: <ich>` |
| 2c | Neustart ohne Abbruch | Konsole offen lassen, als `admin` in einem zweiten Fenster „Neu starten“ | Anzeige wechselt auf „Server gestoppt“ bzw. „verbinde neu“ und danach wieder „verbunden“; die Startzeilen des neuen Laufs erscheinen; die letzten Zeilen vor dem Neustart stehen genau einmal da (keine Dopplung); kein Neuladen der Seite nötig; nach dem Start eines gestoppten Servers kann es bis zu ca. 10 s dauern, bis „Verbunden“ erscheint (Wiederverbindung mit Backoff 3 s, höchstens 10 s) |
| 2d | RCON-Rauschen | Schalter „RCON-Meldungen ausblenden“ aus- und wieder einschalten | aus: Zeilen `Thread RCON Client … started/shutting down` sichtbar; an: ausgeblendet |
| 2e | `stop`-Hinweis | Hinweis unter der Befehlszeile lesen | Text nennt, dass `stop` den Server neu startet und der Power-Button stoppt |
| 2f | Stream-Limit | als `helfer` fünf Browser-Tabs mit der Konsole von `survival` öffnen; danach einen Tab schließen | der 5. Tab zeigt „Zu viele offene Konsolen“ (der Stream antwortet `429 rate_limited`); nach dem Schließen verbindet er sich innerhalb von ca. 10 s |
| 2g | Option-Injection | in der Konsole `--help` senden | abgelehnt mit Validierungsfehler, nichts erscheint im Log; ebenso jeder Befehl, der mit `-` beginnt |
| 3a | nur `server.view` | als `freund` anmelden, Server `survival` öffnen | Übersicht zeigt „Online-Spieler“ mit `<ich>`; keine Tabs „Konsole“ und „Spieler“ |
| 3b | API-Sperre | als `freund` in den Browser-DevTools (Konsole) ausführen: `await Promise.all(['console/stream','players/lists'].map(p => fetch('/api/servers/<id>/' + p).then(r => r.status)))` und `await fetch('/api/servers/<id>/console', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': '<token>'}, body: '{"command":"list"}'}).then(r => r.status)` (`<id>` aus der URL, `<token>` aus `GET /api/auth/me` → `csrf_token`) | `[403, 403]` und `403` |
| 4a | Whitelist ohne Neustart | als `admin` Einstellungen → „Whitelist aktiv“ einschalten, speichern, „Jetzt neu starten“; danach als `helfer` Tab „Spieler“ → Whitelist → `<ich>` hinzufügen | Toast mit `Added <ich> to the whitelist`; `<ich>` steht in der Liste; Client kann beitreten, ein anderer Account nicht |
| 4b | Op ohne Neustart | Tab „Spieler“ → Online → `<ich>` → Op | Toast `Made <ich> a server operator`; im Spiel funktioniert `/gamemode creative` sofort |
| 4c | Kick, Ban, Entbannen | `<ich>` kicken (Grund „test“), dann bannen (Grund „test“), dann unter „Bans“ entbannen | Kick: Client wird mit „test“ getrennt; Ban: Beitritt abgelehnt, Eintrag mit Grund, „von“, „seit“ in „Bans“; Entbannen: Eintrag weg, Beitritt wieder möglich |
| 4d | Im Spiel vergebene Rechte bleiben | im Spiel (als Op) `/op <zweiter Account>` bzw. `/whitelist add <Name>`; Tab „Spieler“ neu laden; dann Server neu starten | Einträge erscheinen im Tab; nach dem Neustart unverändert vorhanden |
| 4e | gestoppter Server | Server stoppen, Tab „Spieler“ öffnen | Listen sichtbar, Ändern-Knöpfe gesperrt mit Hinweis; in der Konsole ist die Befehlszeile gesperrt |
| 5a | Recht entzogen | `helfer` hat die Konsole offen; `admin` entzieht `helfer` unter Benutzer → Rechte `server.console` | innerhalb von 30 s endet der Stream (Anzeige „keine Berechtigung“ bzw. Tab verschwindet nach dem nächsten Laden); `journalctl` zeigt keine Fehler |
| 5b | Session beendet | Recht wieder geben; `helfer` öffnet die Konsole; `admin` beendet unter Sicherheit → Sessions die Session von `helfer` | innerhalb von 30 s endet der Stream; `helfer` wird beim nächsten Request zur Anmeldung geleitet |
| 6 | Audit | als `admin` Audit-Log öffnen, Filter Aktion `server.console_command` bzw. `server.player_action`; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | jeder Befehl aus 2b mit Befehlstext und Erfolg/Fehler (nicht der Ausgabe), jede Aktion aus 4a–4c mit Aktion, Name und Grund; `verify` meldet OK |
| M | Migration aus TP2 (nur wenn Vorbereitung 2 zutraf) | nach dem Deploy den alten Server ansehen; „Jetzt neu starten“; Tab „Spieler“ | vor dem Neustart „Neustart nötig“; danach stehen die notierten Ops und (bei aktiver Whitelist) Whitelist-Namen im Tab; in `ssh claude@192.168.1.117 "sudo docker inspect mcpanel-mc-<id> --format '{{json .Config.Env}}'"` fehlen `OPS`, `WHITELIST`, `EXISTING_OPS_FILE`, `EXISTING_WHITELIST_FILE` |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px breit): Konsole bedienbar, Befehlszeile bleibt unten sichtbar, Spieler-Tab ohne horizontales Scrollen.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
