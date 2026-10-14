# Entwicklung

Lokale Entwicklung auf macOS (Apple Silicon), Release-Ziele `linux/amd64` und `linux/arm64`.
Architektur und Anforderungen: `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`.

## Toolchain

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck
```

| Werkzeug | Version | Hinweis |
|---|---|---|
| Go | laut `go.mod` | CI liest dieselbe Datei (`go-version-file`) |
| Node.js | 24 (Active LTS) | CI pinnt die Hauptversion `24`; Node 26 wird erst am 2026-10-28 LTS. Beim Wechsel auf die nächste LTS-Linie alle `node-version`-Einträge in `.github/workflows/*.yml` gemeinsam anheben |
| golangci-lint | v2.14.0 | CI-Version; `scripts/vm-test.sh` installiert genau diese auf der Test-VM (Release-Archiv mit Prüfsumme) |
| restic | 0.19.1 (CI, Test-VM), mindestens 0.17 | `scripts/install-restic.sh` installiert genau diese Version mit Prüfsumme (CI, `make test-vm`, `make deploy-testvm`); auf dem Mac optional für Backups im Dev-Modus (`brew install restic`, nur durch den Nutzer) |
| GoReleaser | v2.18.2 (CI), lokal ≥ v2.8 | `make release` nutzt das lokal installierte `goreleaser` |
| sqlc | Version im Header von `internal/store/db.go` | CI lädt genau diese Version als Release-Binary; wer mit einer neueren Version generiert, committet den geänderten Header mit |
| govulncheck | v1.8.0 | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| Redocly CLI | 2.54.2 | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| actionlint | v1.7.12 | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` (nutzt `shellcheck`, falls installiert) |

## Schnellstart

```bash
(cd web && npm ci)
make dev        # agent + files + panel (-dev) + Vite; UI unter http://localhost:5173
```

`make dev` baut `.dev/bin/panel`, `.dev/bin/agent` und `.dev/bin/files` und startet agent, den Dateidienst (`files -dev`), `panel serve -dev` und Vite. Endet einer der Prozesse, beendet `scripts/dev.sh` die anderen und bricht mit Exit-Code ≠ 0 ab (die Ausgabe nennt den Prozess). Strg+C stoppt alles.

## Erster Start

Eine frische Entwicklungsumgebung hat noch keinen Benutzer. Den ersten Admin legt die CLI an (in einem zweiten Terminal, während `make dev` läuft):

```bash
printf '%s\n' '<passwort>' | .dev/bin/panel admin create -dev -username admin
```

Ohne Pipe fragt `admin create` das Passwort zweimal ohne Echo ab. Danach unter <http://localhost:5173> anmelden; Admins müssen beim ersten Login einen zweiten Faktor (Authenticator-App oder Passkey) einrichten und die Wiederherstellungscodes bestätigen. Alle Daten liegen unter `.dev/` (`make clean` setzt sie zurück).

Weitere CLI-Befehle (wirken direkt auf die Datenbank, das panel darf laufen oder gestoppt sein):

| Befehl | Wirkung |
|---|---|
| `.dev/bin/panel admin unlock -dev` | Notausgang bei Aussperrung: hebt alle aktiven Jails auf, schaltet „Admin-Login nur aus Allowlist“ ab und leert den Länderfilter (wird auditiert) |
| `.dev/bin/panel audit verify -dev` | prüft die Hash-Kette des Audit-Logs und meldet den ersten fehlerhaften Eintrag (Exit-Code 1) |

Ohne `-dev` lesen alle Befehle `/etc/mc-panel/panel.toml` (abweichend: `-config <pfad>`).

## Frontend ohne Backend (Mock)

```bash
cd web && npm run dev:mock    # Vite mit MSW-Mock-API, kein Go-Prozess nötig
```

Der Mock deckt jede Operation aus `api/openapi.yaml` ab (`src/mocks/handlers.test.ts` prüft das). `npm run build` verweigert einen Build mit aktivem Mock.

## UI-Vorschau in Docker

Zum Durchklicken der Oberfläche ohne lokale Node-Installation: derselbe Mock-Modus, aber in einem Container (Docker Desktop). Kein Backend, keine Datenbank – die Daten kommen aus der MSW-Mock-API und setzen sich bei jedem Neuladen zurück.

```bash
make preview         # baut das Image und startet den Container im Hintergrund
make preview-logs    # Logs des Vite-Servers
make preview-down    # stoppt und entfernt den Container
```

- Adresse: http://localhost:5180 (nur von diesem Mac erreichbar; Port 5180, damit `make dev` auf 5173 frei bleibt).
- Login: `admin` / `correct-horse-battery`, TOTP-Code `123456`; eingeschränkter Benutzer: `alice` (gleiches Passwort).
- `web/src` ist in den Container eingebunden: Änderungen am Frontend erscheinen sofort. Nach Änderungen an `package.json` erneut `make preview` (baut neu).
- Funktionstests gegen das echte Backend sind das nicht; dafür `make dev` oder ein Testserver.
- Die CI baut das Image bei jedem PR mit (Job `web`), startet es aber nicht. Claude startet die Vorschau nie selbst.

## Minecraft-Server lokal (optional)

`make dev` startet den agent ohne Docker: Server lassen sich anlegen und konfigurieren, der Status bleibt „Docker nicht verfügbar“, und auf dem Mac entstehen keine Container. Wer lokal echte Minecraft-Container sehen will, startet bewusst:

```bash
MCPANEL_DEV_DOCKER=1 make dev
```

- Der dev-agent nutzt dann den Docker-Socket von OrbStack, Docker Desktop oder `/var/run/docker.sock` (in dieser Reihenfolge), legt das Netz `mcpanel-mc` und den Container `mcpanel-router` an und schreibt Instanzdaten nach `.dev/mc/`. Der Router veröffentlicht Port 25565 auf allen Netzwerkschnittstellen des Macs, ist also im lokalen Netz erreichbar.
- Basisdomain in den Einstellungen: `mc.localhost`; für den Minecraft-Client `/etc/hosts` um `127.0.0.1 <name>.mc.localhost` ergänzen.
- Aufräumen: `docker rm -f $(docker ps -aq --filter label=mcpanel.instance) mcpanel-router; docker network rm mcpanel-mc`.
- Nach dem Update von TP2 jeden Server mit „Neustart nötig“ über das panel neu starten, bevor Ops/Whitelist im Spieler-Tab geändert werden: Laufende Container tragen sonst noch `OPS`/`WHITELIST`/`EXISTING_*_FILE=SYNCHRONIZE`, und nach einem Absturz oder `stop` überschreibt itzg `ops.json`/`whitelist.json` aus der alten Umgebung.
- Konsolenbefehle, die mit `-` beginnen (nach optionalen Leerzeichen und einem `/`), lehnen panel und agent ab (`400 validation_failed`): sonst würde `rcon-cli` sie als Option lesen; der agent ruft `rcon-cli -- <befehl>` auf.
- Konsole und Spieler-Tab brauchen einen laufenden Container. Ohne Docker antworten Befehle und Spielerlisten mit `503 docker_unavailable`, die Karte „Online-Spieler“ zeigt „nicht verfügbar“; der Log-Stream antwortet trotzdem mit 200, und die Konsole zeigt „Konsole nicht erreichbar“ bzw. „Verbinde neu …“. Die UI-Vorschau (`make preview`) und `npm run dev:mock` liefern dagegen simulierte Log-Zeilen (alle 2 s eine neue, mit ANSI-/`§`-Farben und RCON-Rauschen), Befehlsantworten und Spielerlisten.
- Der Dateidienst läuft im Dev-Modus als eigener Prozess des Entwicklers und arbeitet auf demselben Instanz-Verzeichnis wie der dev-agent (`.dev/mc/instances/<id>`); fehlt das Verzeichnis eines Servers, legt er es an. Der Dateibrowser funktioniert dadurch auch ohne Docker. Die Rechte-Trennung (eigener Benutzer `mcpanel-mc`, keine Capabilities) gibt es nur auf Linux (Test-VM, CI).
- Plugins und Mods (Tab „Plugins“ bzw. „Mods“ bei Paper, Purpur, Fabric und NeoForge) brauchen nichts Zusätzliches: Das panel spricht auch im Dev-Modus mit den echten Upstreams (Modrinth, Hangar) und lädt Jars über den Dateidienst nach `.dev/mc/instances/<id>/plugins` bzw. `mods`. Ohne Docker funktionieren Suche, Installieren, Inventar, Schalter und Entfernen; „Neustart nötig“ erscheint nur bei laufendem Container. Ohne Internet zeigt „Entdecken“ einen Fehlerzustand, „Installiert“ bleibt bedienbar. Wer Upstreams schonen will, nimmt `npm run dev:mock` (Katalog, Jobs und Icons aus dem Mock).
- Backups (Tab „Backups“) brauchen ein restic im `PATH` des Dateidienstes (Dev: `restic_binary = "restic"`). Ohne restic zeigt der Tab nur den Hinweis „nicht verfügbar“, alles andere läuft normal; auf dem Mac installiert der Nutzer restic bei Bedarf selbst (`brew install restic`). Repos und Schlüssel liegen dann unter `.dev/mc/backups/` (`make clean` löscht sie mit). Backup und Restore brauchen den Zustand des Containers vom agent: Ohne erreichbares Docker scheitern sie mit „Docker is not reachable“ (Job-Code `agent_unavailable`, „Der Agent ist nicht erreichbar.“), weil ein hängender dockerd sonst einen laufenden Server ohne `save-off` sichern ließe. In `make dev` funktionieren Backups deshalb nur mit `MCPANEL_DEV_DOCKER=1` (und restic im `PATH`); Liste, Zeitplan, Anpinnen, Download und Löschen von Snapshots gehen auch ohne Docker. `npm run dev:mock` simuliert Snapshots, Jobs und Zeitpläne ohne restic.

Funktionstests gegen echte Server (Clients, Modpacks, Neustarts, Konsole) laufen getrennt auf der Test-VM (nächster Abschnitt); die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus), `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler), `docs/dev/acceptance-tp3b.md` (Dateibrowser), `docs/dev/acceptance-tp3c.md` (Plugins und Mods) und `docs/dev/acceptance-tp4.md` (Backups).

## Test-VM

Für manuelle Tests gegen das echte Backend mit Docker, nftables und Caddy gibt es eine interne VM: Ubuntu 26.04, `claude@192.168.1.117`, passwortloses `sudo`, ausgehender Internetzugang, aber von außen nicht erreichbar. Anders als auf dem Mac darf Claude dort alles installieren und umbauen.

```bash
make deploy-testvm                         # main von GitHub
make deploy-testvm REF=feat/mein-branch    # beliebiger Branch, Tag oder Commit (muss gepusht sein)
make deploy-testvm TESTVM=user@andere-vm   # andere VM
```

- `scripts/testvm-deploy.sh` läuft per `ssh … bash -s` auf der VM. Beim ersten Lauf installiert es die Build-Werkzeuge (`git`, `jq`, `make`, `xz-utils`, `bzip2`), Go (Version aus `go.mod`, nach `/usr/local/go`) und Node.js 24 (nach `/usr/local/lib/nodejs`). Danach holt es das Repo mit dem Deploy-Key der VM nach `~/mc-panel`, baut UI, panel, agent und files (Version `0.0.0-testvm+<commit>`), legt sie mit `deploy/` und dem zusammengefügten `install.sh` in `~/mc-panel/build/testvm/` ab (Format von `--from-dir`) installiert das gepinnte restic (`scripts/install-restic.sh`, idempotent, `/usr/local/bin/restic`; der Installer übernimmt es als `restic_binary`, weil `/usr/local/bin` im `PATH` vorne steht) und ruft den Installer auf: `install --from-dir … --tls internal --domain panel.<ip>.sslip.io --no-firewall --yes`, bis ein `install` einmal erfolgreich war (root-eigener Marker `/etc/mc-panel/.testvm-installed`, erst danach gesetzt) und solange `/etc/caddy/mc-panel.caddy` fehlt; sonst `update --from-dir … --force --yes` (derselbe Build ein zweites Mal braucht `--force`). Ein Ref ohne Installer (vor Teilprojekt 6) bricht mit einer klaren Meldung ab. Pakete (Docker, Caddy, nftables, restic aus Ubuntu, sqlite3, tzdata …), Benutzer, Rechte, Konfiguration, Units und die Caddy-Site schreibt der Installer (Abschnitt „Installer“); das Skript ergänzt nur den Caddyfile-Rahmen der VM (`skip_install_trust`, die markierte Zeile `import mc-panel.caddy # managed by mc-panel`, Weiterleitung der IP auf den Panel-Namen) und kopiert die Caddy-CA nach `~/caddy-root.crt`. Der Rahmen steht vor dem ersten `install` (die alte Caddyfile enthält die Panel-Site direkt, daneben wäre sie doppelt definiert); die vorige Datei bleibt als `/etc/caddy/Caddyfile.testvm-prev` und kommt zurück, wenn der Installer scheitert und die neue Datei nicht gültig ist (z. B. Import einer nie geschriebenen `mc-panel.caddy`) oder Caddy einen geänderten Rahmen ablehnt. Daten bleiben erhalten (`/var/lib/mc-panel`, `/srv/mc-panel`). Zum Schluss läuft `install.sh doctor`; Warnungen (Exit-Code 1) zu eigenem Dateisystem, Host-Firewall (`--no-firewall`), neuerer Release und ggf. SSH sind auf der VM normal, ein Fehler (Exit-Code 2) lässt das Deploy scheitern.
- Die erste Ausführung nach Teilprojekt 6 migriert die alte Heredoc-Installation einmalig: Gibt es noch keinen Marker, aber eine `/etc/mc-panel/panel.toml` mit `listen = "127.0.0.1:8080"` und noch keine `panel.toml.pre-installer`, werden die drei Konfigurationsdateien nach `*.pre-installer` verschoben (eine vorhandene Kopie wird nie überschrieben) (der Installer ergänzt sonst nur fehlende Schlüssel und ließe `listen = "127.0.0.1:8080"` stehen) und die Dienste gestoppt; die Minecraft-Container laufen weiter. Der Installer schreibt die Dateien neu, das panel lauscht danach auf dem Socket `/run/mc-panel-web/panel.sock` statt auf `127.0.0.1:8080`. Der vorhandene Admin bleibt (der Installer fragt nicht, die VM hat ohnehin kein Terminal). Da Container laufen, schreibt der Installer `/etc/docker/daemon.json`, startet Docker aber nicht neu (`[warn]`): Log-Rotation und `live-restore` greifen beim nächsten Docker-Neustart.
- Ein Update über den Installer tauscht Binaries, Units und fehlende Konfigurationsschlüssel (mit Sicherung unter `/var/lib/mc-panel-rollback/` und Rollback); die Caddy-Site und die Firewall fasst es nicht an. Ändert ein Branch `deploy/caddy/`, hilft ein `install` von Hand: `ssh claude@192.168.1.117 'sudo ~/mc-panel/build/testvm/install.sh install --from-dir ~/mc-panel/build/testvm --tls internal --domain panel.192-168-1-117.sslip.io --no-firewall --yes'`.
- Adressen über [sslip.io](https://sslip.io) (öffentliches Wildcard-DNS, das auf die IP im Namen auflöst): Panel <https://panel.192-168-1-117.sslip.io/>, Basisdomain für Minecraft `mc.192-168-1-117.sslip.io`, im Client also `<server>.mc.192-168-1-117.sslip.io`. `https://192.168.1.117` leitet auf den Panel-Namen um.
- TLS: Let's Encrypt erreicht die VM nicht, deshalb signiert Caddy mit seiner internen CA (`tls internal`). Bis die CA vertraut ist, warnt der Browser; die Warnung lässt sich wegklicken (TOTP-Login geht), Passkeys aber nicht (WebAuthn verweigert Seiten mit Zertifikatsfehlern). Die CA vertrauen: `scp claude@192.168.1.117:caddy-root.crt /tmp/ && sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain /tmp/caddy-root.crt` (systemweit, also nur durch den Nutzer). Das Root-Zertifikat bleibt über Deploys gleich.
- Erster Admin (nur auf einer frischen VM nötig, der Installer überspringt den Schritt ohne Terminal mit `[warn]`): `ssh claude@192.168.1.117 "printf '%s\n' '<passwort>' | sudo runuser -u mcpanel -- panel admin create -config /etc/mc-panel/panel.toml -username admin"`.
- Logs: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`. Installer-Protokoll: `/var/log/mc-panel-install.log`. Konfiguration: `/etc/mc-panel/{panel,agent}.toml` und `/etc/mc-panel-files/files.toml`; der Installer ergänzt dort nur fehlende Schlüssel, Änderungen von Hand bleiben also über Deploys erhalten (Vorlagen in `deploy/config/`).
- Das panel lauscht wie in Produktion auf dem Socket `/run/mc-panel-web/panel.sock` (`mcpanel-panel.socket`); Caddy spricht ihn an.
- Plugins/Mods brauchen auf der VM keine eigene Konfiguration: die panel-Unit des Installers schränkt ausgehende Verbindungen nicht ein (wie für die Versionskataloge) und der Dateidienst bewusst ohne Netz bleibt (`RestrictAddressFamilies=AF_UNIX`). Die Checkliste steht in `docs/dev/acceptance-tp3c.md`.
- Backups: Der Installer legt `/srv/mc-panel/backups` (`mcpanel-mc 0700`) an, setzt `restic_binary` auf das restic aus `command -v restic` (Version ≥ 0.17; auf der VM das gepinnte `/usr/local/bin/restic` aus `scripts/install-restic.sh`, auf einem VPS das Ubuntu-Paket `/usr/bin/restic`, 26.04 hat 0.18) und schreibt die Unit des Dateidienstes aus `deploy/systemd/` (`ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups`, `MemoryMax=1G`, `TasksMax=512`, `IOWeight=50`, `CPUWeight=50`); `install.sh doctor` prüft restic und die Rechte. Die Checkliste steht in `docs/dev/acceptance-tp4.md`.

## Tests auf der Test-VM

Der Mac bleibt sauber (dort nur `make check`); Tests laufen vor jedem Push auf der Test-VM:

```bash
make test-vm                 # alle Suites: lint, go, web, e2e
make test-vm SUITE=go        # go test -race ./... (CGO_ENABLED=1)
make test-vm SUITE=lint      # golangci-lint v2.14.0 (Linux, also auch die _linux-Dateien), eslint, tsc
make test-vm SUITE=web       # Vitest
make test-vm SUITE=e2e       # Playwright: echtes Backend (Dev-Modus) und Mock
make test-vm SUITE=shots     # Screenshots aller web/e2e/shots/*.shots.ts gegen den Mock (nicht Teil von all)
```

Die Suite `go` installiert beim ersten Lauf restic per `scripts/install-restic.sh` und setzt `MCPANEL_TEST_RESTIC=/usr/local/bin/restic`; ohne diese Variable (auf dem Mac immer) überspringen sich die Integrationstests des Dateidienstes mit echtem restic.

- `scripts/test-vm.sh` kopiert den Arbeitsstand per `rsync --delete` nach `~/mc-panel-test/<Name des Checkouts>/` auf der VM: getrackte und neue Dateien, ohne `.git` und ohne das, was `.gitignore` ausschließt (die Ausschlüsse stehen im Skript). Ein Push ist nicht nötig. Jeder Checkout bzw. Worktree bekommt sein eigenes Verzeichnis. Die Läufe aller Checkouts sind per `flock` serialisiert (parallele Race- und Vitest-Läufe mehrerer Teammates überlasten die VM und lassen zeitkritische Tests scheitern); ein wartender Lauf meldet „waiting for another test run on this VM“. `node_modules` und Build-Ausgaben bleiben zwischen Läufen erhalten.
- Dann läuft dort `scripts/vm-test.sh <suite>` als normaler Benutzer `claude`. Beim ersten Bedarf richtet es ein: golangci-lint (Release-Archiv mit Prüfsumme nach `~/.local/bin`), `npm ci` (erneut nur, wenn sich `package-lock.json` ändert) und Playwright-Chromium samt Systempaketen (`sudo`, einmal pro Playwright-Version). Go und Node kommen aus `make deploy-testvm`; passt die Go-Version nicht zu `go.mod`, bricht das Skript mit einem Hinweis ab.
- `e2e` baut UI und Binaries, startet agent, `files -dev` und `panel serve -dev` mit eigener Konfiguration (`.dev/panel-vm.toml`: Port 18080, das panel liefert die gebaute UI selbst aus), legt `e2e-admin` an und führt `npm run e2e:real` und `npm run e2e` (Mock, Port 5174) aus. Das deployte panel (systemd, Socket `/run/mc-panel-web/panel.sock`) bleibt unberührt.
- `shots` führt das Playwright-Projekt `screenshots` (`web/e2e/shots/*.shots.ts`, Helfer `shoot()`/`signInMock()` in `web/e2e/shots/shoot.ts`) gegen den Mock aus: jede Seite in Dark und Light, Desktop (1440 px) und Mobil (390 px). Die PNGs kopiert `make test-vm` zurück nach `web/screenshots/` (gitignored). Nur zur Sichtprüfung beim UI-Redesign; nicht Teil von `all` und nicht in CI.
- Einzelne Tests nach einem `make test-vm`: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/<dir> && PATH=/usr/local/go/bin:$PATH go test ./internal/rbac -run TestName'` bzw. im Unterordner `web` mit `PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run <datei>`.
- SSH zur VM braucht in Claude Code eine Bash ohne Sandbox.
- Was nur in der CI läuft: die Root-Abnahmen (`nftables`, `instances`, `installer`; sie verändern den Host), `govulncheck`, `npm audit`, die sqlc- und `gen:api`-Aktualitätsprüfungen, OpenAPI-Lint und der GoReleaser-Snapshot.

## End-to-End-Tests (Playwright)

Einmalig den Browser installieren: `cd web && npx playwright install chromium`.

| Befehl | Wirkung |
|---|---|
| `npm run e2e` | Projekt `mock`: startet selbst `vite --mode mock` auf Port 5174 und testet `e2e/smoke.spec.ts`, `e2e/totp.spec.ts`, `e2e/servers.spec.ts`, `e2e/plugins.spec.ts` und `e2e/backups.spec.ts` gegen die Mock-API |
| `npm run e2e:real` | Projekt `real`: `e2e/login-totp.spec.ts` gegen ein laufendes `make dev`; braucht `E2E_BASE_URL`, `E2E_ADMIN_USER` und `E2E_ADMIN_PASSWORD` eines **frischen** Admins ohne 2FA |

Der Real-Test richtet TOTP ein, meldet sich mit TOTP neu an, registriert über einen virtuellen Authenticator (Chrome DevTools Protocol, nur Chromium) einen Passkey im Profil (Sudo per TOTP) und meldet sich zuletzt mit dem Passkey an. Er wartet dabei ggf. auf den nächsten 30-s-TOTP-Schritt. Beispiel:

```bash
make dev   # Terminal 1
printf '%s\n' 'e2e admin password 1' | .dev/bin/panel admin create -dev -username e2e-admin
cd web && E2E_BASE_URL=http://localhost:5173 E2E_ADMIN_USER=e2e-admin E2E_ADMIN_PASSWORD='e2e admin password 1' npm run e2e:real
```

Ein zweiter Lauf braucht einen neuen Admin (anderer Benutzername) oder ein frisches `.dev/` (`make clean`).

In CI läuft der Job `e2e` (`.github/workflows/ci.yml`) parallel zu den übrigen Jobs: er baut die Binaries, startet agent, `files -dev`, `panel serve -dev` und Vite, legt `e2e-admin` an, führt `npm run e2e:real` (ohne Retries) und danach `npm run e2e` aus und lädt bei einem Fehlschlag die Logs sowie `test-results`/`playwright-report` als Artefakt hoch.

## Produktion

Produktiv richtet der Installer (`install.sh`, Abschnitt „Installer“, Betreiber-Anleitung in `README.md`) alles ein; die folgenden Punkte beschreiben, was er umsetzt und was bei Abweichungen gilt. Maßgeblich für Units, Caddy, Firewall und Konfiguration sind die Vorlagen in `deploy/`.

- Der agent braucht Docker (Engine ≥ 25, API ≥ 1.44) und den Systembenutzer `mcpanel-mc` (eigene UID/GID, nicht die des panels); beides richtet der Installer ein. `instances_dir` (Standard `/srv/mc-panel`) darf kein Symlink sein und nur unter root-eigenen, nicht gruppen-/weltbeschreibbaren Verzeichnissen liegen.

- Der Dateidienst `files` (Spec 3b §3.1) läuft als `mcpanel-mc` ohne Zusatzgruppen und ohne Capabilities, mit `NoNewPrivileges`, `ProtectSystem=strict`, `ReadWritePaths=<instances_dir>/instances <instances_dir>/backups`, `MemoryMax=1G`, `TasksMax=512` (Begrenzung gegen präparierte Archive, Platz für restic), `IOWeight=50` und `CPUWeight=50`. Er nimmt nur Verbindungen der panel-UID und von root an (`panel_uid`; die Peer-UID-Prüfung ist die eigentliche Zugangskontrolle, wie beim agent). Den Socket legt systemd an (Socket-Aktivierung, `mcpanel-files.socket`: `ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660`, `DirectoryMode=0755`), der Dienst übernimmt ihn über `LISTEN_FDS`. Ohne Aktivierung (Dev, CI) legt er `socket` selbst mit `socket_mode` an (Gruppe = seine primäre Gruppe). Mit `UMask=0027` entstehen in Produktion Dateien mit 0640 und Ordner mit 0750 (der Dienst fordert 0644/0755 an). Vorgaben, die die Units des Installers umsetzen: eigenes Socket-Verzeichnis `/run/mc-panel-files` (nie `/run/mc-panel` des agents), keine `SupplementaryGroups`, Konfiguration in `/etc/mc-panel-files/files.toml` (`root:mcpanel-mc`, Verzeichnis 0750, Datei 0640 – `/etc/mc-panel` ist für `mcpanel-mc` unlesbar), `instances/` als `root:mcpanel-mc 0750` (setzt der agent beim Start), `deleted/` bleibt `root:root 0700`, `backups/` ist `mcpanel-mc:mcpanel-mc 0700` (setzt der agent beim Start; muss vor dem Start des Dienstes existieren, sonst scheitert `ReadWritePaths`). Das panel findet den Dienst über `files_socket` in `panel.toml`; `deploy/systemd/` ist die Referenz.
- Datei-Uploads und -Downloads (bis 4 GiB) laufen als ein Stream durch Caddy und das panel; Caddy begrenzt die Body-Größe standardmäßig nicht. Ein anderer Proxy braucht ein Body-Limit ≥ 4 GiB und darf Uploads nicht puffern.
- **Ausgehende Verbindungen des panels** (nur HTTPS, Port 443, User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`): Versionskataloge `piston-meta.mojang.com`, `fill.papermc.io`, `api.purpurmc.org`, `meta.fabricmc.net`, `maven.neoforged.net`; Plugins/Mods `api.modrinth.com`, `hangar.papermc.io` (API) sowie `cdn.modrinth.com`, `hangarcdn.papermc.io` (Jars und Icons; andere Hosts lehnt das panel ab, auch als Weiterleitung). Eine Egress-Firewall muss diese Hosts für den Benutzer `mcpanel` erlauben. Der Dateidienst und der agent brauchen für Plugins kein Netz; die Minecraft-Container selbst laden weiterhin, was itzg beim Start braucht (Server-Jars, Modpacks).
- **Plugins/Mods** (Spec 3c): Das panel lädt Jars als Stream (≤ 256 MiB) und reicht sie an den Dateidienst weiter, der die Prüfsumme des Upstreams (Modrinth sha512, Hangar sha256) vor dem `rename` prüft (`PUT /v1/upload?…&sha512=…`); bei Abweichung bleibt nichts liegen. Quelle der Wahrheit ist der Ordner `plugins/` bzw. `mods/`; die Tabelle `plugin_installs` merkt sich nur die Herkunft panel-installierter Jars. Jobs (Installieren, Aktualisieren) laufen im Speicher, einer pro Server und vier im panel; ein Neustart des panels bricht laufende Jobs ab, bereits installierte Jars bleiben. Icons gehen über den Proxy `/api/servers/{id}/plugins/icon` (die CSP bleibt `img-src 'self' data:`, kein fremder Bild-Host). Modrinth drosselt auf 300 Anfragen/min pro IP; das panel begrenzt Upstream-Abfragen (Suche, Versionen, Auflösen, Updatecheck mit `refresh`) auf 60/min pro Konto (Burst 20) und cacht Suche (5 min), Projekte/Versionen (15 min), Hash-Zuordnung und Updates (1 h).
- **Backups** (Spec 4): Der Dateidienst startet restic (mindestens 0.17, Referenz 0.19.1 über `scripts/install-restic.sh`; Pfad `restic_binary` in `files.toml`, Default `/usr/bin/restic`) als Kindprozess ohne Netz. restic öffnet nie Pfade in einem Datenverzeichnis: Backups sind ein tar-Stream, den der Dienst über `os.Root` erzeugt (ohne Symlinks, verborgene Dateien und RCON-Passwort), Restore und Download lesen `restic dump`. Ein Repo pro Server unter `<instances_dir>/backups/repos/<id>`, das Passwort in `backups/keys/<id>`. **`backups/keys/` gehört in jede Sicherung des Hosts**: Ohne Schlüssel ist ein Repo unlesbar. Offsite-Ziele gibt es noch nicht; Repos sind in sich geschlossen und lassen sich samt Schlüssel per `rsync -a <instances_dir>/backups/ <ziel>/` auf einen anderen Rechner kopieren. `backups/` liegt auf demselben Dateisystem wie `instances/`; ein Restore braucht dort freien Platz in Höhe des Snapshots plus `reserve_bytes`. Ein Snapshot mit mehr als 16 MiB Pfadnamen (Zip-Grenze aus TP3b; Backup und Restore erlauben 64 MiB) lässt sich wiederherstellen, aber nicht als Zip herunterladen. Notfall-Restore ohne panel (Server vorher stoppen): `sudo -u mcpanel-mc env RESTIC_REPOSITORY=/srv/mc-panel/backups/repos/<id> RESTIC_PASSWORD_FILE=/srv/mc-panel/backups/keys/<id> restic --no-cache snapshots` und `… restic --no-cache dump <snapshot> /data.tar > /tmp/welt.tar`, dann in ein leeres Verzeichnis entpacken und prüfen (der letzte Eintrag `.mcpanel-end` mit den Zählern des Dienstes gehört nicht zu den Serverdaten und kann weg). Ein Restore tauscht die oberste Ebene des Datenverzeichnisses per `rename` und führt dabei ein Journal in `<instances_dir>/backups/restore-journal/<id>` (nur während der Umbenennungen). Stirbt der Dateidienst mittendrin, schließt er den Tausch beim nächsten Start synchron ab: zurück bei Phase `old_moving`, vorwärts bei `new_moving`; danach sind Temp-Ordner und Eintrag weg. **Phase `stuck`** (der Tausch und auch der Rücktausch scheiterten): Neue Restores dieses Servers werden abgelehnt. Ein Restore-Versuch stoppt den Server trotzdem und legt einen „Vor Restore“-Snapshot an, bevor er mit „Der Dateidienst ist für diesen Server gerade beschäftigt …“ (`files_busy`) scheitert; die eigentliche Ursache steht nur im Log des Dateidienstes. Runbook: (1) Log des Dateidienstes lesen (`journalctl -u mcpanel-files`, Meldung mit `folder` und `journal`); (2) Server gestoppt lassen und den versteckten Ordner `.mcpanel-tmp-…` im Datenverzeichnis prüfen (`sudo ls -la /srv/mc-panel/instances/<id>/.mcpanel-tmp-*/`): `old/` enthält den vorigen Stand, `new/` den Rest des Snapshots; (3) entscheiden und die Inhalte von Hand nach oben verschieben (als `mcpanel-mc`, z. B. `sudo -u mcpanel-mc mv …`), danach den Temp-Ordner löschen; (4) `sudo rm /srv/mc-panel/backups/restore-journal/<id>`. „Server löschen“ mit „Backups ebenfalls löschen“ entfernt den Eintrag ebenfalls. Zeitpläne laufen in der Zeitzone aus den Einstellungen (Default `Europe/Berlin`); verpasste Termine (panel aus) holt das panel einmal nach. **Das Paket `tzdata` muss installiert sein** (der Installer installiert es, `doctor` prüft es): restic bettet keine Zonendaten ein und fällt ohne `/usr/share/zoneinfo/<Zone>` still auf UTC zurück. Dann stimmen Snapshot-Zeiten und die Tages-, Wochen- und Monatsgruppen der Aufbewahrung nicht, ohne dass ein Fehler erscheint.
- Der Installer setzt `listen = "unix:/run/mc-panel-web/panel.sock"`. Den Socket legt systemd an (Socket-Aktivierung, `mcpanel-panel.socket`: `ListenStream=/run/mc-panel-web/panel.sock`, `SocketUser=mcpanel`, `SocketGroup=caddy`, `SocketMode=0660`, `DirectoryMode=0755`, `RemoveOnStop=yes`), das panel übernimmt ihn über `LISTEN_FDS` (`internal/unixsock`, wie der Dateidienst); der Pfad in `listen` muss dem aktivierten Socket entsprechen, sonst startet das panel nicht. Das Verzeichnis `/run/mc-panel-web` gehört nur dem panel-Socket, nie `/run/mc-panel` des agents (systemd entfernt Socket und Verzeichnis einer Unit beim Stopp, und `mcpanel` darf den agent-Socket nicht ersetzen können). Ohne Aktivierung (Dev, eigene Units) legt das panel den Socket selbst an (Modus 0660, Gruppe = primäre Gruppe des panel-Benutzers; Default ohne `-dev` ist `unix:/run/mc-panel/panel.sock`); ein verwaister Socket wird beim Start nur entfernt, wenn niemand mehr darauf lauscht.
- Caddy terminiert TLS und spricht den Socket an (`/etc/caddy/mc-panel.caddy` aus `deploy/caddy/`, eingebunden über die Zeile `import mc-panel.caddy # managed by mc-panel` im `Caddyfile`; eigene Sites dort bleiben unberührt):

  ```
  panel.example.com {
      tls you@example.com
      reverse_proxy unix//run/mc-panel-web/panel.sock
  }
  ```

  Caddy bekommt Zugriff über die Socket-Gruppe `caddy`, **nicht** durch Aufnahme in die Gruppe `mcpanel` (sonst könnte Caddy den agent-Socket löschen oder ersetzen).
- Requests über den Unix-Socket gelten als vom Proxy kommend: die Client-IP ist der rechteste nicht vertrauenswürdige Eintrag in `X-Forwarded-For` (Caddy setzt ihn). Fehlt ein verwertbarer Eintrag, wird der Request abgewiesen (`403 ip_blocked`); Health-Checks direkt am Socket brauchen deshalb einen Header, z. B. `curl --unix-socket /run/mc-panel-web/panel.sock -H 'X-Forwarded-For: 127.0.0.1' http://panel/api/health` (so prüfen es auch `install` und `update`). Aktive Caddy-Health-Checks (`health_uri`) senden kein `X-Forwarded-For` und würden mit `403` scheitern — entweder `health_headers` setzen oder auf aktive Checks verzichten. Andere Proxys müssen `X-Forwarded-For` ohne Port setzen, sonst `403`.
- TCP bleibt möglich (`listen = "127.0.0.1:8080"`); dann wird `X-Forwarded-For` nur von `trusted_proxies` übernommen. Loopback ist nur im Dev-Modus von „Admin-Login nur aus Allowlist“ ausgenommen.
- Die Konsole streamt das Server-Log als Server-Sent Events (`GET /api/servers/{id}/console/stream`, `text/event-stream`, Heartbeat-Kommentar alle 15 s, ein Stream pro offenem Konsolen-Tab, höchstens 4 pro Benutzer, der 5. bekommt `429 rate_limited`). Caddy reicht SSE ohne Pufferung und ohne Zusatzkonfiguration durch. Ein anderer Proxy darf die Antwort nicht puffern (das panel setzt `X-Accel-Buffering: no` für nginx) und braucht ein Lese-Timeout über 15 s.
- Außerhalb des Dev-Modus sendet das panel `Strict-Transport-Security: max-age=31536000`.
- CLI-Befehle auf dem Server immer als panel-Benutzer ausführen (`sudo runuser -u mcpanel -- panel admin unlock -config /etc/mc-panel/panel.toml …`), sonst entstehen root-eigene `panel.db-wal`/`-shm`, die der Dienst nicht öffnen kann.
- Eine 2FA-Sperre (10 falsche Codes in 15 min) hebt `admin unlock` nicht auf; sie endet nach 15 min oder mit einem panel-Neustart.
- Die nftables-Sets fassen je 65 536 Einträge; ist ein Set voll, schlägt der Firewall-Ban fehl (wird geloggt), die Sperre in der App gilt trotzdem.

## Installer

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md`; manuelle Abnahme `docs/dev/acceptance-tp6.md`; Betreiber-Anleitung in `README.md` („Installation“).

- Quellen in `scripts/install/`: `lib.sh` (Helfer, Pfade, Ausgabe `[ok]`/`[changed]`/`[skip]`/`[warn]`, Wrapper `sys_*` für Systembefehle), je Schritt eine Datei (`preflight.sh`, `packages.sh`, `layout.sh`, `binaries.sh`, `config.sh`, `units.sh`, `caddy.sh`, `firewall.sh`, `upgrades.sh`, `admin.sh`, `update.sh`, `doctor.sh`, `uninstall.sh`) und `main.sh` (Argumente, Unterbefehle, Logging). Vorlagen in `deploy/` (`systemd/`, `caddy/`, `nftables/`, `config/`). Funktionen tragen ein Präfix je Datei (`pre_`, `pkg_`, `lay_`, `bin_`, `cfg_`, `unit_`, `cad_`, `fw_`, `upg_`, `adm_`, `upd_`, `doc_`, `uni_`).
- `make installer` fügt alles zu `build/install.sh` zusammen (`scripts/install/assemble.sh <version> <out>`; Vorlagen als Heredoc-Funktionen, Version als `MCPANEL_VERSION`; die letzte Zeile ruft `main`, ein abgebrochener Download führt also nichts aus). goreleaser hängt `install.sh` an jedes Release und nimmt es in `checksums.txt` auf; die Archive enthalten `deploy/`. Mit `--from-dir DIR` (Binaries, `deploy/` und `install.sh` eines lokalen Builds) lädt der Installer nichts aus dem Netz; Vorlagen kommen dann aus `DIR/deploy/`.
- Auf dem Mac läuft der Installer nie; `make check` prüft nur die Syntax (`bash -n`).
- Tests: `scripts/install/test.sh [DATEI|MUSTER]` (reine Funktionen, Vorlagen, Prüfsummen und die Schritte gegen ein Wurzelverzeichnis `MCPANEL_INSTALL_ROOT=<tmp>` mit `MCPANEL_INSTALL_DRYRUN=1`, das Systembefehle nur protokolliert; Testdateien in `scripts/install/tests/`) läuft mit shellcheck im CI-Job `installer-lint` und mit `make test-vm SUITE=lint`. Den ganzen Lebenszyklus prüft der CI-Job `installer` (`scripts/ci-installer.sh`, nur Runner, ändert das System; Ablauf in der Tabelle „CI“). Jeder `make deploy-testvm` läuft ebenfalls über den Installer. `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1` erlaubt in der CI Ubuntu 24.04; für Betreiber ist das kein unterstützter Weg.
- Exit-Codes: 0 ok, 1 Fehler (bzw. `doctor` mit Warnungen), 2 `doctor` mit Fehlern, 3 Update zurückgerollt, 4 Rollback gescheitert (alle Dienste gestoppt, Anleitung in der Ausgabe). Protokoll: `/var/log/mc-panel-install.log` (0600). `doctor --json` gibt `{"status": "ok|warn|fail", "checks": [{"id", "status", "message", "hint"}]}` aus; die Check-IDs stehen am Anfang von `doctor.sh`.
- Update: Sicherung von Binaries, Units, den drei TOML-Dateien und der DB nach `/var/lib/mc-panel-rollback/<alte-version>-<zeit>/` (`root:root 0700`, die drei neuesten bleiben) mit Zustandsdatei (`phase=backed_up|applying|verified|rolling_back|rolled_back|aborted|rollback_failed`). Bricht ein Update hart ab (Stromausfall, `kill -9`), erledigt das nächste `update` den Rest zuerst: nichts getauscht → `aborted`; Zielversion installiert und gesund → `verified`; sonst Rollback mit der DB-Kopie erst nach Rückfrage (oder `--force`/`--yes`), weil Änderungen seit der Sicherung verloren gehen. `doctor` meldet solche Verzeichnisse (`update_state`).
- Firewall: eigene Tabelle `inet mcpanel_fw` (`/etc/mc-panel/mcpanel_fw.nft`, geladen von `mcpanel-fw.service`), getrennt von der Tabelle `inet mcpanel` des agents. Vor dem Laden prüft der Installer die SSH-Ports (`--ssh-port`, sonst `sshd -T` gegen die tatsächlich lauschenden Sockets), die laufenden SSH-Sitzungen und alle anderen Ports, die die Regeln sperren würden (Rückfrage, `--allow-port PORT[/udp]` öffnet sie, `--yes` sperrt sie).

## Monitoring und Alerts

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md`; manuelle Abnahme `docs/dev/acceptance-tp5.md`.

- **Sammler im panel** (`internal/monitoring`), kein neuer Dienst: alle 30 s ein Durchlauf für alle Server mit Instanz. Quellen: der agent-Status (Zustand, `started_at`, `restart_count`, CPU, RAM, Limit; ein Aufruf für alle), ein Server-List-Ping je laufendem Server über mc-router an `monitoring_ping_addr` mit dem FQDN des Servers im Handshake (Frist 3 s, höchstens 4 gleichzeitig, Antwort hart begrenzt: Pakete und JSON ≤ 64 KiB, ≤ 12 Namen; der Ping läuft nie über den root-agent), TPS/MSPT per RCON über die vorhandene agent-Route (einmal pro Minute, je Server höchstens ein Aufruf gleichzeitig) und der Host aus `/proc/stat`, `/proc/meminfo`, `/proc/loadavg` sowie `statfs(instances_dir)` (frei = für Nicht-root verfügbare Blöcke). Ein Durchlauf über 30 s lässt den nächsten Takt aus.
- **TPS-Quelle** pro Server und `revision` durch Versuch: zuerst `tick query` (Vanilla ≥ 1.20.3 und alle Abkömmlinge), sonst bei Paper/Purpur nur `mspt` (`tps = min(20, 1000 / mspt)`, damit es bei einer Abfrage pro Minute bleibt), sonst `none` („TPS für diese Version nicht verfügbar“). Fest wird die Quelle nach drei gültigen bzw. drei ungültigen Antworten, bis dahin `tps_source = unknown`. Jede Abfrage erzeugt im Server-Log zwei Zeilen (`Thread RCON Client … started`/`shutting down`): ohne Zuschauer also zwei Zeilen pro Minute.
- **Live-Modus:** Jeder Aufruf von `GET /api/servers/{id}/metrics/live` (der Tab „Monitoring“ fragt alle 5 s) schaltet TPS/MSPT dieses Servers für 30 s auf 5 s; diese Werte werden nicht gespeichert. Spieler, CPU und RAM bleiben im 30-s-Takt.
- **`panel.toml`:** `monitoring_ping_addr` (Default `"127.0.0.1:25565"`, der Host-Port von mc-router; `""` schaltet den Ping ab: dann keine Spielerzahlen, „nicht erreichbar“ nur aus dem Container-Zustand, „wieder online“ nach 2 min `running`) und `instances_dir` (Default `"/srv/mc-panel"`, derselbe Pfad wie in `agent.toml`; `""` = keine Plattenwerte). Das panel braucht nur Suchrecht auf `instances_dir` (0755) und Lesezugriff auf `/proc` (`ProtectProc=invisible` stört nicht, es verbirgt nur fremde PIDs). Die Vorlage `deploy/config/panel.toml` nennt beide Schlüssel ausdrücklich.
- **Speicher:** drei Tabellen in der panel-DB, `metrics_raw` (30-s-Rohwerte, 24 h), `metrics_5m` (7 d) und `metrics_1h` (30 d, nach `samples` gewichtete Mittel); fehlende Werte werden nicht geschrieben (Lücke statt 0). Die Verdichtung läuft alle 5 min idempotent (`INSERT OR REPLACE`) und löscht danach, was älter ist. Abfragen: `1h` → Rohwerte (`step_ms` 30 000), `24h` → 5 min, `7d`/`30d` → 1 h (höchstens 721 Punkte pro Metrik). Größe: pro Server und Tag rund 23 000 Rohzeilen (8 Metriken × 2 880 Takte); 10 Server über 30 Tage bleiben deutlich unter 50 MB. Beim Löschen eines Servers entfernt das panel seine Reihen und Alert-Zustände in derselben Transaktion.
- **Alerts** (Einstellungen → „Monitoring & Alerts“, `PUT /api/monitoring/settings` mit Sudo, Audit `monitoring.settings_update`; Schalter „Down-Alerts für diesen Server“ im Tab, `PUT /api/servers/{id}/alerts`, Audit `server.alerts_update`):
  - `server.down` feuert, wenn ein Server laufen soll (`desired_state = running`), der Schalter an ist, keine Daten-Sperre gilt (Entpacken, Restore) und er seit `down_after_min` (Default 3) durchgehend nicht erreichbar ist (Container nicht `running` oder Ping ohne Antwort). Nach jedem Start und Neustart (laut `started_at`) gelten 5 min Schonfrist; solange `server.crash_loop` feuert, kommt kein zusätzliches `server.down`. Gelöst mit `server.recovered` („Server back online: …“). Bekannte Grenze: Die Schonfrist zählt ab `started_at` des Containers; solange er noch gar nicht läuft (Image-Pull oder Neuanlage nach einer Änderung), gibt es keine Schonfrist, und dauert das länger als `down_after_min`, meldet das panel den Server als down (bei kurzem `down_after_min` und langsamem Image-Download möglich).
  - `server.crash_loop` feuert, wenn Dockers `restart_count` innerhalb von `crash_window_min` (Default 10) um mindestens `crash_restarts` (Default 3) steigt; ein neu angelegter Container (Zähler wieder 0) zählt nicht. Gelöst nach einem vollen Fenster ohne Neustart mit `server.recovered` („Server stable again: …“). Der Schalter betrifft beide Server-Alerts.
  - `host.disk_low` feuert, wenn der freie Platz unter `disk_min_bytes` (Default 10 GiB) oder unter `disk_min_percent` (Default 10 %) fällt; gelöst mit `host.disk_ok` erst mit 2 GiB **und** 2 Prozentpunkten Abstand (Hysterese).
  - Versand über die Benachrichtigungen aus dem Security Center (Discord/ntfy, notify-Dispatcher): eine Meldung beim Auslösen und beim Lösen, alle 24 h eine Erinnerung („Server still down: …“, „Server still in a crash loop: …“, „Disk space still low“). Der Zustand steht in `alert_state` und überlebt Neustarts des panels (keine zweite Meldung). Ein gestoppter oder gelöschter Server löst seine Alerts ohne Meldung. Ohne agent- oder Docker-Antwort bleiben die Server-Alerts eingefroren. Meldungen nennen nie Host-Pfade.
- **Dev-Modus (`make dev`):** `monitoring_ping_addr = ""` (kein mc-router auf dem Mac), Host nur Platte (`statfs` auf `.dev/mc`, kein `/proc`), Server-Werte und TPS nur mit `MCPANEL_DEV_DOCKER=1`. Der Mock (`cd web && npm run dev:mock`) zeigt alles: `survival` mit Spielern und einem TPS-Einbruch, `creative` gestoppt, `modded` nicht erreichbar mit `server.down`, `lobby` ohne Instanz; `db.monitoring.diskLow` lässt `host.disk_low` feuern.
- **CI** (`scripts/ci-instances.sh`, Job `instances`): Ein panel im Produktionsmodus lehnt ntfy-Ziele auf localhost und privaten Adressen ab, ein lokaler ntfy-Mock ist deshalb nicht erreichbar. Die Abnahme legt stattdessen einen ntfy-Notifier auf `https://ntfy.invalid` an (`.invalid` löst nach RFC 6761 nie auf, die Validierung lässt ihn zu): Jedes Ereignis, das den Dispatcher erreicht, endet nach den Wiederholungen in genau einer JSON-Logzeile `"msg":"notify: delivery failed"` mit `"type":"<ereignis>"`. Geprüft werden diese Zeilen im panel-Log und der Alert-Zustand über `GET /api/monitoring/settings` (`alerts`) und `/api/monitoring/overview`. „Nicht erreichbar“ entsteht ohne Container-Stopp (den der Abgleich sofort rückgängig machen würde) durch eine nftables-Regel, die nur die Pings des panels verwirft: Tabelle `inet mcpanel_citest`, Kette `output` (Hook output, Priorität 0), `meta skuid mcpanel tcp dport 25565 drop`. Die Fenster sind verkürzt (`down_after_min = 1`, `crash_window_min = 10`, zum Lösen des Crash-Loops 1); der Plattenteil wird mit einer `SKIP`-Zeile übersprungen, wenn der Runner weniger als 3 GiB bzw. 3 % frei hat (Hysterese über den Mindestschwellen).

## Make-Targets

| Target | Wirkung |
|---|---|
| `make check` | einziger lokaler Check auf dem Mac (~10 s): gofmt, `go vet` + `go build` für macOS und Linux (inkl. Integrationstest-Dateien), `tsc`. Tests, Race, Lint und E2E laufen auf der Test-VM (`make test-vm`) und in der CI |
| `make dev` | baut agent, files und panel, startet agent + `files -dev` + `panel serve -dev` + Vite-Dev-Server, alles unter `./.dev/`; endet ein Prozess, stoppt das Skript alle |
| `make test` | Go-Tests (`test-go`) und Frontend-Tests (`test-web`) |
| `make test-race` | Go-Tests mit Race-Detector (`CGO_ENABLED=1`) |
| `make lint` | `golangci-lint run ./...` plus ESLint und TypeScript-Check im Frontend |
| `make generate` | `sqlc generate` und `npm run gen:api` |
| `make build` | Frontend bauen, dann `bin/panel`, `bin/agent` und `bin/files` für das Host-System |
| `make release` | GoReleaser-Snapshot nach `dist/`: Archive für linux/amd64 und linux/arm64 (mit `deploy/`), `install.sh` und `checksums.txt` |
| `make installer` | fügt `scripts/install/` und `deploy/` zu `build/install.sh` zusammen (`INSTALLER_VERSION`, Standard aus `git describe`); nie auf dem Mac ausführen |
| `make deploy-testvm` | baut `REF` (Standard `main`) auf der Test-VM aus GitHub und deployt ihn dort über den Installer (`install` beim ersten Mal, sonst `update`; siehe „Test-VM“) |
| `make test-vm` | kopiert den Arbeitsstand auf die Test-VM und führt dort Tests aus, `SUITE=all\|go\|lint\|web\|e2e\|shots` (siehe „Tests auf der Test-VM“) |
| `make clean` | entfernt `bin/`, `dist/`, `.dev/` und das gebaute Frontend |

## CI

Ein Pull Request führt nur die Jobs aus, die seine geänderten Dateien brauchen; der erste Job `changes` vergleicht dafür mit `git diff` gegen die Merge-Basis. Der wöchentliche Lauf und manuelle Starts führen immer alles aus, ebenso jede Änderung an `.github/workflows/ci.yml`. Pushes auf `main` führen alle Jobs auf den VM-Runnern aus, aber nicht die Root-Jobs `nftables`, `instances` und `installer`: Die hat der Pull Request auf demselben Stand schon ausgeführt, und sie sind fast alle bezahlten GitHub-Minuten.

**Runner:** Alle Jobs ohne root (`changes`, `go`, `web`, `openapi`, `e2e`, `build`, `installer-lint`, `CI OK`) laufen auf vier self-hosted Runnern auf der Test-VM (`mcpanel-vm-1` bis `mcpanel-vm-4`, Label `mcpanel-vm`, als systemd-Dienste `actions.runner.Bernotese-mc-panel.mcpanel-vm-<n>` unter dem Benutzer `claude`, Arbeitsverzeichnisse `~/actions-runner-<n>/_work`). Sie kosten keine GitHub-Minuten; die VM braucht dafür zusätzlich `gh`, `jq`, `file` und `shellcheck` (die Runner von GitHub haben sie vorinstalliert). Die zeitkritischen Testschritte (Race-Tests, Vitest, E2E) nehmen dasselbe Lock wie `make test-vm` (`~/.cache/mcpanel-vm-test.lock`), CI und Teammates testen also nie gleichzeitig; der E2E-Job ist `scripts/vm-test.sh e2e` (eigene Ports 18080/5174, das deployte panel auf 8080 bleibt unberührt). Die Root-Jobs verändern den Host (nftables, Docker, Port 25565) und laufen weiter auf GitHub-Runnern (`ubuntu-24.04`). Ist die VM aus, warten die VM-Jobs, bis sie wieder da ist. Vier Runner, weil mehrere Läufe gleichzeitig (PRs der Teammates, `main`) sonst Jobs so lange warten lassen, dass GitHub sie mit „not acquired by Runner“ abbricht; die Testschritte serialisiert ohnehin das Lock. Runner neu registrieren: Token mit `gh api -X POST repos/Bernotese/mc-panel/actions/runners/registration-token -q .token`, dann auf der VM in `~/actions-runner-<n>` `./config.sh --unattended --url https://github.com/Bernotese/mc-panel --token <token> --name mcpanel-vm-<n> --labels mcpanel-vm --work _work --replace` und `sudo ./svc.sh install claude && sudo ./svc.sh start`. Nur für dieses private Repository: Ein self-hosted Runner führt Code aus Pull Requests aus und darf nie an einem öffentlichen Repository hängen. Der Job `CI OK` fasst zusammen (grün, wenn jeder Job bestanden hat oder nicht nötig war); auf ihn wartet `gh pr checks <n> --watch`. Die Jobs laufen parallel, keiner wartet auf einen anderen.

| CI-Job | Läuft im PR bei Änderungen an | Auf der Test-VM bzw. von Hand |
|---|---|---|
| `go` | `cmd/`, `internal/`, `api/`, `go.mod`/`go.sum`, `sqlc.yaml`, `.golangci.yml`, `Makefile`, `scripts/install-restic.sh` | `make test-vm SUITE=go` und `SUITE=lint`; nur CI: `go mod verify && go mod tidy -diff`, sqlc-Aktualität (Release-Binary der Version aus dem Header von `internal/store/db.go`), `govulncheck` |
| `web` | `web/`, `api/` (Preview-Image nur bei `web/Dockerfile.preview`, `compose.preview.yaml`, `web/package*.json`, `web/vite.config.ts`) | `make test-vm SUITE=web` und `SUITE=lint`; nur CI: Build, `gen:api`-Aktualität, `npm audit`, Preview-Image |
| `openapi` | `api/` | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| `e2e` | `cmd/`, `internal/`, `api/`, `web/`, `go.mod`/`go.sum` | `make test-vm SUITE=e2e` |
| `nftables` | `internal/agent/`, `internal/defense/`, `internal/auth/`, `cmd/agent/`, `cmd/panel/`, `scripts/ci-acceptance.sh`, `go.mod`/`go.sum` | nur CI (siehe unten) |
| `instances` | `cmd/`, `internal/` (die Abnahme startet agent, Dateidienst und panel samt Login, TOTP, Monitoring und Audit), `scripts/ci-instances.sh`, `scripts/mc-status.py`, `scripts/install-restic.sh`, `go.mod`/`go.sum` | nur CI (siehe unten) |
| `build` | `cmd/`, `internal/webui/`, `deploy/`, `scripts/install/`, `.goreleaser.yaml`, `Makefile`, `web/package*.json`, `web/vite.config.ts`, `go.mod`/`go.sum` | `make build`, `make release` |
| `installer-lint`, `installer` | `scripts/install/`, `deploy/`, `scripts/ci-installer.sh`, `scripts/install-restic.sh`, `scripts/testvm-deploy.sh`, `cmd/`, `internal/config/`, `internal/unixsock/`, `.goreleaser.yaml`, `Makefile`, `go.mod`/`go.sum`; `installer` zusätzlich bei jeder Änderung unter `internal/` (wie `instances`) und nicht bei Pushes auf `main` | `installer-lint`: `make test-vm SUITE=lint`; `installer`: nur CI (ändert das System), einzelne Funktionen mit `scripts/install/test.sh` |

Der Job `go` führt die Tests nur mit Race-Detector aus (`make test-race` deckt alle Tests ab); der Release-Workflow führt weiterhin `make test` und `make test-race` aus.

Details der CI-Jobs:

| CI-Job | Inhalt |
|---|---|
| `go` | `go mod verify && go mod tidy -diff`, `sqlc generate && git status --porcelain internal/store`, restic per `scripts/install-restic.sh` (`MCPANEL_TEST_RESTIC`), `make test-race`, golangci-lint v2.14.0, `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| `web` | `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && npm run gen:api && git status --porcelain . && npm audit --audit-level=high` |
| `openapi` | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| `nftables` | nur in der CI (Linux, root): `go test -tags nftintegration -exec sudo ./internal/agent/...` und der Abnahmetest `sudo scripts/ci-acceptance.sh bin/panel bin/agent` (5 Fehlversuche → IP im nftables-Jail). Das Skript verändert den Host (Benutzer, Netz-Namespace, nftables) und ist nur für Wegwerf-Runner gedacht. |
| `nftables` (Docker-Integration) | nur in der CI: `go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" ./internal/agent/...` |
| `instances` | nur in der CI: `sudo scripts/ci-instances.sh bin/agent bin/files bin/panel` (echter agent + Docker + Paper-Server: Routing, Härtung, Konsole mit RCON, Log-Stream und Spielerlisten, präparierte Spielerdateien, Absturz-Neustart, Löschen; Dateidienst als `mcpanel-mc`: Peer-UID (panel-Benutzer und root erlaubt, `nobody` abgewiesen), Upload, Schwärzen, Editor-Sperre, Symlinks, Ordner-Zip, Welt entpacken, bösartige Archive; Plugins: panel im Produktionsmodus mit TOTP-Login und Sudo, zweiter Paper-Server über die API, Chunky von Modrinth und ViaBackwards + ViaVersion von Hangar über die echten Upstreams (eine Wiederholung bei Upstream-Fehlern), Prüfsummen im Dateidienst, Icon-Proxy, Neustart mit RCON `plugins`, Deaktivieren, Entfernen, Audit; Backups mit echtem restic auf demselben Server: manuelles Backup mit `save-off`/`save-on` im Log, Repo und tar-Inhalt, Restore mit Sudo (Start gesperrt, Sicherheits-Snapshot, Neustart), Zip-Download, Zeitplan in der Zeitzone des panels, Pin, Löschen, Purge beim Löschen des Servers, Audit; Monitoring auf demselben Server: Live-Werte, Verlauf, Host-Werte, RCON-Rauschen ohne Zuschauer, `server.down`/`server.recovered` per nftables-Regel gegen die Pings des panels, `server.crash_loop` nach drei Abstürzen ohne `server.down`, `host.disk_low`/`host.disk_ok`, Schalter „Down-Alerts“, Zustellung an den Dispatcher über `https://ntfy.invalid` (siehe „Monitoring und Alerts“), Audit; installiert nftables, verändert den Host und braucht Internet; dauert rund 25 min (Monitoring-Teil etwa 15 min), Timeout 70 min) |
| `build` | `make build`, `make release`, dann `file dist/*/panel dist/*/agent dist/*/files`, Inhalt der Archive (Binaries und `deploy/`) und `install.sh` in `checksums.txt` |
| `installer-lint` | `make installer INSTALLER_VERSION=0.0.0-ci`, shellcheck über `scripts/install/*.sh`, `scripts/install/tests/*.sh`, `build/install.sh`, `scripts/ci-installer.sh` und `scripts/testvm-deploy.sh`, dann `bash scripts/install/test.sh` |
| `installer` | nur in der CI (GitHub-Runner `ubuntu-24.04`, root, `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1`): `sudo scripts/ci-installer.sh .` baut drei Stände im Format von `--from-dir` (`0.0.1-ci`, `0.0.2-ci`, `0.0.3-ci` mit einem panel, das sofort endet), installiert restic 0.19.1 (Ubuntu 24.04 hat 0.16) und spielt den Lebenszyklus durch: `install` mit `--tls internal`, Domain `panel.ci.test` (`/etc/hosts`), Firewall und `--ssh-port 22`, Admin über stdin; Units, Sockets, Firewall-Regeln und ausgehendes HTTPS; `doctor --json` ohne Fehler; zweites `install` ohne `[changed]`; Login über Caddy (interne CA); `update` auf `0.0.2-ci` mit Sicherung unter `/var/lib/mc-panel-rollback/`, DB unverändert; `update` auf den defekten Stand → Exit-Code 3, `0.0.2-ci` läuft wieder, DB unverändert, `doctor` ohne Fehler; `uninstall` (Units, Binaries, Caddy-Site, Tabellen `inet mcpanel_fw`/`inet mcpanel` und ein Container mit Label `mcpanel.instance` weg; Daten, Konfiguration, Benutzer, fremder Container und fremde Tabelle bleiben); erneutes `install` findet Admin und Daten; `uninstall --purge` über ein Pseudo-Terminal (`script`): falsche Domain bricht ab, die richtige entfernt alles. Timeout 30 min |

Linux-spezifische Dateien lintet `make test-vm SUITE=lint` (golangci-lint läuft dort auf Linux).

## Lint-Konventionen

- Die Konfiguration steht in `.golangci.yml`; gosec läuft als Teil von golangci-lint.
- Ausnahmen gehören gezielt in `.golangci.yml` (Pfad + Linter + Text, mit Begründung) oder als `//nolint:<linter> // <Begründung>` direkt an die Zeile. Nie einen Linter global abschalten.
- Aktive Exclusion-Presets: nur `std-error-handling` (ignoriert ungeprüfte Fehler von `Close`, `Flush`, `os.Remove`, `fmt.Fprint*` u. Ä.). `common-false-positives` ist bewusst **nicht** aktiv: gosec G103 (`unsafe`), G204 (Subprozess mit Variable) und G304 (Datei-Pfad aus Variable) bleiben sichtbar und brauchen im Einzelfall eine gezielte Regel oder `//nolint:gosec // Begründung`.
- Global abgeschaltete gosec-Regeln (`linters.settings.gosec.excludes`): G104 (ungeprüfte Fehler, macht errcheck), G115 (Integer-Konvertierung, feuert bei jeder `uint32(len(x))`/`int(uid)`-Konvertierung; Grenzen werden dort geprüft, wo Eingaben nicht vertrauenswürdig sind) und G117 (Feldnamen wie `password`/`secret` in API-DTOs sind Teil des OpenAPI-Vertrags).
- Requests und DB-Zugriffe immer mit Kontext: `http.NewRequestWithContext`, `QueryContext`/`ExecContext`/`PingContext`. Ausnahme ist `net.Listen` im agent (`cmd/agent/`, `internal/agent/`).
- Dateimodus `0660` (agent-Socket, Spec §3) ist nur in `cmd/agent/` und `internal/agent/` für gosec G302 freigegeben, sofern er als Literal (`0o660`) in der Zeile steht; überall sonst und für andere Modi über `0600` ist eine Begründung nötig (z. B. `internal/unixsock`, das den Modus der Sockets von agent, panel und Dateidienst setzt). Der Dateidienst (`internal/files`) legt Dateien mit 0644 und Ordner mit 0755 an (wie der Minecraft-Server selbst; die `UMask=0027` der Unit greift); die Stellen tragen `//nolint:gosec // G302/G304 …` bzw. `G301` mit Begründung, eine Pfadregel gibt es nicht.
- In Tests sind feste Test-Passwörter, `math/rand` und lockere Dateimodi erlaubt.
- Formatierung: `gofmt` + `goimports` mit lokalem Präfix `github.com/Bernotese/mc-panel` (`golangci-lint fmt ./...`).

## Release

```bash
git switch main && git pull --ff-only
make test-vm                    # alle Suites auf der Test-VM
git tag -a v0.1.0 -m "mc-panel v0.1.0"
git push origin v0.1.0
```

Der Workflow `.github/workflows/release.yml` prüft zuerst, dass der getaggte Commit in `main` enthalten ist (sonst Abbruch). Der Job „Lint and tests“ führt `make lint`, `make test` und `make test-race` auf den selbst gehosteten Runnern der Test-VM aus (nur Lesezugriff, gleiche Sperre wie `make test-vm`; auf einem GitHub-Runner brauchte allein Vitest über 13 Minuten). Erst danach baut der Job „GoReleaser“ auf einem frischen GitHub-Runner ohne Caches das Frontend und veröffentlicht mit GoReleaser (nur dieser letzte Schritt sieht `GITHUB_TOKEN`):

- `mc-panel_<version>_linux_amd64.tar.gz`, `mc-panel_<version>_linux_arm64.tar.gz` (je `panel`, `agent` und `files` sowie `deploy/` mit den Vorlagen des Installers)
- `install.sh` (der zusammengefügte Installer dieser Version, `make installer` vor goreleaser)
- `checksums.txt` (SHA-256 über alle Assets, Format `<hash>  <datei>`)

`<version>` ist der Tag ohne führendes `v`. Tags mit Suffix (z. B. `v0.2.0-rc.1`) werden als Pre-Release markiert.
Vor dem Taggen lokal prüfen: `make release && (cd dist && shasum -a 256 -c checksums.txt)`.

Hinweise zu GoReleaser:

- Meilenstein-Tags wie `phase0` sind kein SemVer und werden per `git.ignore_tags: ["phase*"]` in `.goreleaser.yaml` ignoriert.
- `goreleaser check` und die Git-Statusermittlung brauchen einen konfigurierten Git-Remote. Ohne Remote funktioniert `make release` im Snapshot-Modus trotzdem (Version `0.0.1-snapshot+none`); `goreleaser check` bricht mit `no remote configured` ab. Ohne den Repo-Remote zu ändern, hilft: `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=remote.origin.url GIT_CONFIG_VALUE_0=https://github.com/example/mc-panel.git goreleaser check`.
- Ab Go 1.27 speichert `go version -m` die `-ldflags` nicht mehr. Ob die Version eingelinkt wurde, zeigt nur der String selbst: `grep -aF "$(jq -r .version dist/metadata.json)" dist/agent_linux_arm64_v8.0/agent` (setzt `var version` in `package main` voraus).

