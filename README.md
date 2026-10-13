# mc-panel

Selbst gehostetes Web-Panel für die eigenen Minecraft-Server auf einem Linux-VPS: ein Go-Backend mit eingebetteter React-Oberfläche, ausgeliefert als statische Binaries für `linux/amd64` und `linux/arm64`. Hobby- und Lernprojekt für einen Freundeskreis (ein VPS, wenige Server, keine Mandanten). Weil das Panel aus dem Internet erreichbar ist, hat Sicherheit Vorrang.

## Funktionen

- **Minecraft-Server** als gehärtete Docker-Container (`itzg/minecraft-server`): Vanilla, Paper, Purpur, Fabric, NeoForge sowie Modrinth- und CurseForge-Modpacks. Versionen, Java, RAM und freigegebene `server.properties` sind einstellbar, Start/Stopp/Neustart mit Auto-Restart. Das Routing läuft über den Hostnamen (`<server>.<basisdomain>`) und einen gemeinsamen Port 25565 (mc-router).
- **Konsole und Spieler:** Live-Log im Browser (läuft über Neustarts weiter), Befehle per RCON, Online-Spieler, Ops, Whitelist und Bans ohne Neustart pflegen (Kick, Ban, Entbannen, Op, Whitelist). Was im Spiel vergeben wird, bleibt erhalten.
- **Anmeldung:** Passwort plus zweiter Faktor (TOTP oder Passkey, für Admins Pflicht), Wiederherstellungscodes, Sessions mit Idle- und Absolut-Timeout, Sudo-Modus für kritische Aktionen.
- **Dateien:** Dateibrowser pro Server mit Editor für Konfigurationsdateien, Up- und Download (bis 4 GiB, Ordner als Zip), Anlegen, Umbenennen, Verschieben, Löschen und Entpacken von Zips (z. B. Welt-Upload bei gestopptem Server). Ausgeführt von einem eigenen, unprivilegierten Dienst; das RCON-Passwort bleibt verborgen.
- **Plugins und Mods:** Plugins für Paper/Purpur aus Modrinth und Hangar, Mods für Fabric/NeoForge aus Modrinth suchen und mit einem Klick samt Pflicht-Abhängigkeiten installieren; installierte Jars (auch per Hand hochgeladene) sehen, aktualisieren, deaktivieren und entfernen. Der unprivilegierte Dateidienst schreibt jede Datei und prüft sie vorher gegen die Prüfsumme der Quelle.
- **Backups:** Datenverzeichnis eines Servers per Klick oder nach Zeitplan (alle N Stunden, täglich, wöchentlich) sichern, auch im laufenden Betrieb (Speichern wird währenddessen angehalten). Aufbewahrung nach Stufen (letzte, täglich, wöchentlich, monatlich), wichtige Stände anpinnen, als Zip herunterladen und den ganzen Server auf einen Stand zurücksetzen; vor jedem Restore entsteht automatisch ein Sicherheits-Snapshot. Lokale, deduplizierte restic-Repos; fehlgeschlagene Läufe melden sich über Discord/ntfy.
- **Monitoring und Alerts:** Spieler (mit Namen), TPS/MSPT, CPU und RAM pro Server sowie CPU, RAM, Load und Platte des Hosts im Dashboard und im Tab „Monitoring“, mit Verlauf über 30 Tage (eigene SVG-Diagramme). Alerts bei „Server down“, Crash-Loop und knappem Plattenplatz über Discord/ntfy, mit Schonfrist nach dem Start und ohne Doppelmeldungen nach Neustarts. Das Monitoring fragt TPS nur einmal pro Minute per RCON ab, damit das Server-Log ruhig bleibt.
- **Rechte:** Admins und Benutzer mit Rechten pro Server (`server.view`, `server.power`, `server.console`, `server.files`, `server.plugins`, `server.backups`).
- **Abwehr:** Rate-Limits, Jail nach Fehlversuchen (in der App und per nftables auf dem Host), IP-Regeln, Länderfilter (GeoIP), Security Center in der Oberfläche, Benachrichtigungen über Discord und ntfy.
- **Audit-Log:** unveränderlich und hash-verkettet, prüfbar mit `panel audit verify`.

## Aufbau

| Binary | läuft als | Aufgabe |
|---|---|---|
| `panel` | Benutzer `mcpanel` (unprivilegiert) | Weboberfläche und `/api`, SQLite-Datenbank, Downloads von Modrinth/Hangar, CLI (`serve`, `admin create`, `admin unlock`, `audit verify`) |
| `agent` | root | schmale JSON-API über einen Unix-Socket, nur für das panel: nftables-Jails und Docker (Minecraft-Container, Router) |
| `files` | Benutzer `mcpanel-mc` (wie die Minecraft-Container, ohne Capabilities) | Dateibrowser und Backups (restic als Kindprozess, ohne Netz): Dateioperationen im Datenverzeichnis eines Servers, nur für das panel (und root), über einen eigenen Unix-Socket |

Die Trennung soll verhindern, dass ein kompromittiertes panel Root-Rechte auf dem Host bekommt; Dateioperationen auf Daten, die ein Minecraft-Server kontrolliert, laufen nie als root. In Produktion terminiert Caddy TLS vor dem panel.

## Schnellstart (Entwicklung, macOS)

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck
(cd web && npm ci)
make dev                    # agent + files + panel (-dev) + Vite, UI unter http://localhost:5173
printf '%s\n' '<passwort>' | .dev/bin/panel admin create -dev -username admin   # zweites Terminal
```

Danach mit `admin` anmelden und den zweiten Faktor einrichten. Ohne Backend geht es auch: `cd web && npm run dev:mock`.

Vor jedem Push: `make check` (Format, Vet, Build für macOS und Linux, TypeScript; ~10 s). Tests, Race-Detector, Lint und E2E laufen in der CI bei jedem PR.

Zum Ausprobieren gegen das echte Backend mit Docker gibt es eine interne Test-VM: `make deploy-testvm [REF=<branch>]`.

## Dokumentation

| Dokument | Inhalt |
|---|---|
| [`docs/dev/README.md`](docs/dev/README.md) | Toolchain, Make-Targets, Test-VM, CI lokal nachstellen, Lint-Konventionen, Release, Hinweise zur Produktion |
| [`docs/dev/acceptance-tp2.md`](docs/dev/acceptance-tp2.md) | manuelle Abnahme der Minecraft-Server auf einem Testserver |
| [`docs/dev/acceptance-tp3a.md`](docs/dev/acceptance-tp3a.md) | manuelle Abnahme von Konsole, Logs und Spieler-Verwaltung auf der Test-VM |
| [`docs/dev/acceptance-tp3b.md`](docs/dev/acceptance-tp3b.md) | manuelle Abnahme des Dateibrowsers auf der Test-VM |
| [`docs/superpowers/specs/2026-10-11-mc-panel-files-design.md`](docs/superpowers/specs/2026-10-11-mc-panel-files-design.md) | Dateibrowser über den unprivilegierten Dienst `mcpanel-files` |
| [`docs/dev/acceptance-tp3c.md`](docs/dev/acceptance-tp3c.md) | manuelle Abnahme von Plugins und Mods auf der Test-VM |
| [`docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md`](docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md) | Plugins und Mods aus Modrinth und Hangar |
| [`docs/dev/acceptance-tp4.md`](docs/dev/acceptance-tp4.md) | manuelle Abnahme der Backups auf der Test-VM |
| [`docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md`](docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md) | Backups mit lokalen restic-Repos über den Dateidienst |
| [`docs/dev/acceptance-tp5.md`](docs/dev/acceptance-tp5.md) | manuelle Abnahme von Monitoring und Alerts auf der Test-VM |
| [`docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md`](docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md) | Monitoring (Sammler, Verlauf, Dashboard) und Alerts über notify |
| [`docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`](docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md) | Architektur, Security-Anforderungen, Roadmap |
| [`docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md`](docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md) | Minecraft-Instanzen (Docker, Router, Reconcile) |
| [`docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md`](docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md) | Konsole, Live-Log, Spieler-Verwaltung (RCON, SSE) |
| [`api/openapi.yaml`](api/openapi.yaml) | API-Vertrag (Quelle für die TypeScript-Typen) |
| [`CLAUDE.md`](CLAUDE.md) | Arbeitsregeln und Architektur-Kurzfassung für Claude Code |

## Stand

| # | Teilprojekt | Stand |
|---|---|---|
| 1 | Fundament + Security | fertig |
| 2 | Instanzen-Lifecycle | fertig |
| 3a | Live-Betrieb: Konsole, Logs, Spieler | fertig |
| 3b | Live-Betrieb: Dateibrowser | fertig |
| 3c | Live-Betrieb: Plugins/Mods (Modrinth, Hangar) | fertig |
| 4 | Backups (lokal; Offsite später) | in Arbeit |
| 5 | Monitoring + Alerts | offen |
| 6 | Installer-CLI (`install`/`update`/`doctor`, Caddy, VPS-Härtung) | offen |

Bis der Installer existiert, beschreibt `docs/dev/acceptance-tp2.md` die Einrichtung eines Servers von Hand.

## Release

Ein Tag `v*` auf `main` veröffentlicht über GitHub Actions ein Release mit `mc-panel_<version>_linux_{amd64,arm64}.tar.gz` (je `panel`, `agent` und `files`) und `checksums.txt`. Ablauf: `docs/dev/README.md`, Abschnitt „Release“.
