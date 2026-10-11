# Abnahme Teilprojekt 3a (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (§10).

Automatisch geprüft (CI): Unit- und Integrationstests (agent: Docker-Demultiplexer, Log-Stream, Exec, Spielerdateien; panel: SSE, Neuverbindung, erneute Prüfung, Rechte, Audit, Parser), Docker-Integration (`dockerintegration`), Instanz-Abnahme über den agent (`scripts/ci-instances.sh`: RCON, Log-Stream inkl. `since`, Spielerlisten nach `whitelist add`/`op`/`ban`/`pardon`, `409 not_running`, präparierte Spielerdateien), Playwright gegen den Mock (Konsole, Spieler-Tab).

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client und echter Session.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP3a-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>, Basisdomain `mc.192-168-1-117.sslip.io`.
2. Läuft dort schon ein Server aus TP2 mit Ops oder Whitelist in den Eigenschaften: vor dem Deploy notieren, welche Namen eingetragen waren (Prüfung M unten).
3. Als `admin` anmelden. Gibt es noch keinen Paper-Server `survival`, ihn anlegen (Paper, aktuelle Version, 4 GiB) und starten.
4. Zwei Benutzer anlegen (Benutzer → Neu, Sudo): `freund` mit nur `server.view` auf `survival`, `helfer` mit `server.view` und `server.console` auf `survival`. Beide melden sich einmal an und richten TOTP ein.
5. Minecraft Java Edition auf einem Rechner im selben Netz wie die VM; Server-Adresse `survival.mc.192-168-1-117.sslip.io`. Der eigene Spielername heißt unten `<ich>`.
6. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-panel -f`.

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 2a | Live-Log | als `helfer` Server `survival` → Tab „Konsole“ | die letzten Zeilen des Server-Logs erscheinen sofort, danach neue Zeilen live (z. B. beim Betreten des Servers mit dem Client: `<ich> joined the game`); Verbindungsanzeige „verbunden“ |
| 2b | Befehle | in der Konsole `say hallo` senden, danach `list` | `hallo` erscheint im Spiel-Chat (`[Rcon] hallo` bzw. `[Server] hallo`); `list` zeigt einen Antwort-Block `› list` mit `There are 1 of a max of 20 players online: <ich>` |
| 2c | Neustart ohne Abbruch | Konsole offen lassen, als `admin` in einem zweiten Fenster „Neu starten“ | Anzeige wechselt auf „Server gestoppt“ bzw. „verbinde neu“ und danach wieder „verbunden“; die Startzeilen des neuen Laufs erscheinen; die letzten Zeilen vor dem Neustart stehen genau einmal da (keine Dopplung); kein Neuladen der Seite nötig |
| 2d | RCON-Rauschen | Schalter „RCON-Meldungen ausblenden“ aus- und wieder einschalten | aus: Zeilen `Thread RCON Client … started/shutting down` sichtbar; an: ausgeblendet |
| 2e | `stop`-Hinweis | Hinweis unter der Befehlszeile lesen | Text nennt, dass `stop` den Server neu startet und der Power-Button stoppt |
| 3a | nur `server.view` | als `freund` anmelden, Server `survival` öffnen | Übersicht zeigt „Online-Spieler“ mit `<ich>`; keine Tabs „Konsole“ und „Spieler“ |
| 3b | API-Sperre | als `freund` in den Browser-DevTools (Konsole) ausführen: `await Promise.all(['console/stream','players/lists'].map(p => fetch('/api/servers/<id>/' + p).then(r => r.status)))` und `await fetch('/api/servers/<id>/console', {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': '<token>'}, body: '{"command":"list"}'}).then(r => r.status)` (`<id>` aus der URL, `<token>` aus `GET /api/auth/me` → `csrf_token`) | `[403, 403]` und `403` |
| 4a | Whitelist ohne Neustart | als `admin` Einstellungen → „Whitelist aktiv“ einschalten, speichern, „Jetzt neu starten“; danach als `helfer` Tab „Spieler“ → Whitelist → `<ich>` hinzufügen | Toast mit `Added <ich> to the whitelist`; `<ich>` steht in der Liste; Client kann beitreten, ein anderer Account nicht |
| 4b | Op ohne Neustart | Tab „Spieler“ → Online → `<ich>` → Op | Toast `Made <ich> a server operator`; im Spiel funktioniert `/gamemode creative` sofort |
| 4c | Kick, Ban, Entbannen | `<ich>` kicken (Grund „test“), dann bannen (Grund „test“), dann unter „Bans“ entbannen | Kick: Client wird mit „test“ getrennt; Ban: Beitritt abgelehnt, Eintrag mit Grund, „von“, „seit“ in „Bans“; Entbannen: Eintrag weg, Beitritt wieder möglich |
| 4d | Im Spiel vergebene Rechte bleiben | im Spiel (als Op) `/op <zweiter Account>` bzw. `/whitelist add <Name>`; Tab „Spieler“ neu laden; dann Server neu starten | Einträge erscheinen im Tab; nach dem Neustart unverändert vorhanden |
| 4e | gestoppter Server | Server stoppen, Tab „Spieler“ öffnen | Listen sichtbar, Ändern-Knöpfe gesperrt mit Hinweis; in der Konsole ist die Befehlszeile gesperrt |
| 5a | Recht entzogen | `helfer` hat die Konsole offen; `admin` entzieht `helfer` unter Benutzer → Rechte `server.console` | innerhalb von 30 s endet der Stream (Anzeige „keine Berechtigung“ bzw. Tab verschwindet nach dem nächsten Laden); `journalctl` zeigt keine Fehler |
| 5b | Session beendet | Recht wieder geben; `helfer` öffnet die Konsole; `admin` beendet unter Sicherheit → Sessions die Session von `helfer` | innerhalb von 30 s endet der Stream; `helfer` wird beim nächsten Request zur Anmeldung geleitet |
| 6 | Audit | als `admin` Audit-Log öffnen, Filter Aktion `server.console_command` bzw. `server.player_action`; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | jeder Befehl aus 2b mit Text und Ergebnis, jede Aktion aus 4a–4c mit Aktion, Name und Grund; `verify` meldet OK |
| M | Migration aus TP2 (nur wenn Vorbereitung 2 zutraf) | nach dem Deploy den alten Server ansehen; „Jetzt neu starten“; Tab „Spieler“ | vor dem Neustart „Neustart nötig“; danach stehen die notierten Ops und (bei aktiver Whitelist) Whitelist-Namen im Tab; in `ssh claude@192.168.1.117 "sudo docker inspect mcpanel-mc-<id> --format '{{json .Config.Env}}'"` fehlen `OPS`, `WHITELIST`, `EXISTING_OPS_FILE`, `EXISTING_WHITELIST_FILE` |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px breit): Konsole bedienbar, Befehlszeile bleibt unten sichtbar, Spieler-Tab ohne horizontales Scrollen.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
