# mc-panel

Selbst gehostetes Web-Panel für die eigenen Minecraft-Server auf einem Linux-VPS: ein Go-Backend mit eingebetteter React-Oberfläche, ausgeliefert als statische Binaries für `linux/amd64` und `linux/arm64`. Hobby- und Lernprojekt für einen Freundeskreis (ein VPS, wenige Server, keine Mandanten). Weil das Panel aus dem Internet erreichbar ist, hat Sicherheit Vorrang.

## Funktionen

- **Minecraft-Server** als gehärtete Docker-Container (`itzg/minecraft-server`): Vanilla, Paper, Purpur, Fabric, NeoForge sowie Modrinth- und CurseForge-Modpacks. Versionen, Java, RAM und freigegebene `server.properties` sind einstellbar, Start/Stopp/Neustart mit Auto-Restart. Das Routing läuft über den Hostnamen (`<server>.<basisdomain>`) und einen gemeinsamen Port 25565 (mc-router).
- **Anmeldung:** Passwort plus zweiter Faktor (TOTP oder Passkey, für Admins Pflicht), Wiederherstellungscodes, Sessions mit Idle- und Absolut-Timeout, Sudo-Modus für kritische Aktionen.
- **Rechte:** Admins und Benutzer mit Rechten pro Server (`server.view`, `server.power`).
- **Abwehr:** Rate-Limits, Jail nach Fehlversuchen (in der App und per nftables auf dem Host), IP-Regeln, Länderfilter (GeoIP), Security Center in der Oberfläche, Benachrichtigungen über Discord und ntfy.
- **Audit-Log:** unveränderlich und hash-verkettet, prüfbar mit `panel audit verify`.

## Aufbau

| Binary | läuft als | Aufgabe |
|---|---|---|
| `panel` | Benutzer `mcpanel` (unprivilegiert) | Weboberfläche und `/api`, SQLite-Datenbank, CLI (`serve`, `admin create`, `admin unlock`, `audit verify`) |
| `agent` | root | schmale JSON-API über einen Unix-Socket, nur für das panel: nftables-Jails und Docker (Minecraft-Container, Router) |

Die Trennung soll verhindern, dass ein kompromittiertes panel Root-Rechte auf dem Host bekommt. In Produktion terminiert Caddy TLS vor dem panel.

## Schnellstart (Entwicklung, macOS)

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck
(cd web && npm ci)
make dev                    # agent + panel (-dev) + Vite, UI unter http://localhost:5173
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
| [`docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`](docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md) | Architektur, Security-Anforderungen, Roadmap |
| [`docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md`](docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md) | Minecraft-Instanzen (Docker, Router, Reconcile) |
| [`api/openapi.yaml`](api/openapi.yaml) | API-Vertrag (Quelle für die TypeScript-Typen) |
| [`CLAUDE.md`](CLAUDE.md) | Arbeitsregeln und Architektur-Kurzfassung für Claude Code |

## Stand

| # | Teilprojekt | Stand |
|---|---|---|
| 1 | Fundament + Security | fertig |
| 2 | Instanzen-Lifecycle | fertig |
| 3 | Live-Betrieb (Konsole, Logs, Dateien, Plugins/Mods, Spieler) | offen |
| 4 | Backups | offen |
| 5 | Monitoring + Alerts | offen |
| 6 | Installer-CLI (`install`/`update`/`doctor`, Caddy, VPS-Härtung) | offen |

Bis der Installer existiert, beschreibt `docs/dev/acceptance-tp2.md` die Einrichtung eines Servers von Hand.

## Release

Ein Tag `v*` auf `main` veröffentlicht über GitHub Actions ein Release mit `mc-panel_<version>_linux_{amd64,arm64}.tar.gz` (je `panel` und `agent`) und `checksums.txt`. Ablauf: `docs/dev/README.md`, Abschnitt „Release“.
