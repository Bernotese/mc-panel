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
- Backups (Tab „Backups“) brauchen ein restic im `PATH` des Dateidienstes (Dev: `restic_binary = "restic"`). Ohne restic zeigt der Tab nur den Hinweis „nicht verfügbar“, alles andere läuft normal; auf dem Mac installiert der Nutzer restic bei Bedarf selbst (`brew install restic`). Repos und Schlüssel liegen dann unter `.dev/mc/backups/` (`make clean` löscht sie mit). Backup und Restore fragen den agent nach dem Zustand des Containers: Ohne Docker (`make dev` ohne `MCPANEL_DEV_DOCKER=1`) scheitern sie deshalb mit „Der Agent ist nicht erreichbar.“; Liste, Zeitplan, Anpinnen, Download und Löschen von Snapshots gehen trotzdem. `npm run dev:mock` simuliert Snapshots, Jobs und Zeitpläne ohne restic.

Funktionstests gegen echte Server (Clients, Modpacks, Neustarts, Konsole) laufen getrennt auf der Test-VM (nächster Abschnitt); die Checklisten stehen in `docs/dev/acceptance-tp2.md` (Server-Lebenszyklus), `docs/dev/acceptance-tp3a.md` (Konsole, Logs, Spieler), `docs/dev/acceptance-tp3b.md` (Dateibrowser), `docs/dev/acceptance-tp3c.md` (Plugins und Mods) und `docs/dev/acceptance-tp4.md` (Backups).

## Test-VM

Für manuelle Tests gegen das echte Backend mit Docker, nftables und Caddy gibt es eine interne VM: Ubuntu 26.04, `claude@192.168.1.117`, passwortloses `sudo`, ausgehender Internetzugang, aber von außen nicht erreichbar. Anders als auf dem Mac darf Claude dort alles installieren und umbauen.

```bash
make deploy-testvm                         # main von GitHub
make deploy-testvm REF=feat/mein-branch    # beliebiger Branch, Tag oder Commit (muss gepusht sein)
make deploy-testvm TESTVM=user@andere-vm   # andere VM
```

- `scripts/testvm-deploy.sh` läuft per `ssh … bash -s` auf der VM. Beim ersten Lauf installiert es Docker, Caddy, nftables, Go (Version aus `go.mod`, nach `/usr/local/go`), Node.js 24 (nach `/usr/local/lib/nodejs`) und restic (gepinnt, `/usr/local/bin/restic`). Danach holt es das Repo mit dem Deploy-Key der VM nach `~/mc-panel`, baut panel, agent und files (Version `0.0.0-testvm+<commit>`), installiert sie nach `/usr/local/bin`, schreibt Konfiguration, systemd-Units (`mcpanel-agent`, `mcpanel-files.socket` + `mcpanel-files.service`, `mcpanel-panel`, starten auch nach einem Neustart) und Caddyfile neu und startet die Dienste neu. Daten bleiben erhalten (`/var/lib/mc-panel`, `/srv/mc-panel`). Danach prüft es Socket (`mcpanel-mc:mcpanel 0660`) und Prozess (`mcpanel-mc`, ohne Zusatzgruppe `mcpanel`, ohne Capabilities, `no_new_privs`) von `mcpanel-files`.
- Adressen über [sslip.io](https://sslip.io) (öffentliches Wildcard-DNS, das auf die IP im Namen auflöst): Panel <https://panel.192-168-1-117.sslip.io/>, Basisdomain für Minecraft `mc.192-168-1-117.sslip.io`, im Client also `<server>.mc.192-168-1-117.sslip.io`. `https://192.168.1.117` leitet auf den Panel-Namen um.
- TLS: Let's Encrypt erreicht die VM nicht, deshalb signiert Caddy mit seiner internen CA (`tls internal`). Bis die CA vertraut ist, warnt der Browser; die Warnung lässt sich wegklicken (TOTP-Login geht), Passkeys aber nicht (WebAuthn verweigert Seiten mit Zertifikatsfehlern). Die CA vertrauen: `scp claude@192.168.1.117:caddy-root.crt /tmp/ && sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain /tmp/caddy-root.crt` (systemweit, also nur durch den Nutzer). Das Root-Zertifikat bleibt über Deploys gleich.
- Erster Admin: `ssh claude@192.168.1.117 "printf '%s\n' '<passwort>' | sudo -u mcpanel panel admin create -config /etc/mc-panel/panel.toml -username admin"`.
- Logs: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`. Konfiguration: `/etc/mc-panel/{panel,agent}.toml` und `/etc/mc-panel-files/files.toml`; Änderungen dort überschreibt der nächste Deploy, also im Skript ändern.
- Das panel lauscht auf der VM auf `127.0.0.1:8080` (wie in `docs/dev/acceptance-tp2.md`), nicht auf dem Unix-Socket aus dem Produktionsprofil; die endgültigen Units liefert der Installer (Teilprojekt 6).
- Plugins/Mods brauchen auf der VM keine eigene Konfiguration: `scripts/testvm-deploy.sh` bleibt unverändert, weil die panel-Unit ausgehende Verbindungen nicht einschränkt (wie für die Versionskataloge) und der Dateidienst bewusst ohne Netz bleibt (`RestrictAddressFamilies=AF_UNIX`). Die Checkliste steht in `docs/dev/acceptance-tp3c.md`.
- Backups: Das Deploy legt `/srv/mc-panel/backups` (`mcpanel-mc 0700`) an, setzt `restic_binary = "/usr/local/bin/restic"` in `files.toml` und gibt der Unit des Dateidienstes `ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups`, `MemoryMax=1G`, `TasksMax=512`, `IOWeight=50` und `CPUWeight=50`; danach prüft es, dass `GET /v1/backups/status` `available: true` meldet. Die Checkliste steht in `docs/dev/acceptance-tp4.md`.

## Tests auf der Test-VM

Der Mac bleibt sauber (dort nur `make check`); Tests laufen vor jedem Push auf der Test-VM:

```bash
make test-vm                 # alle Suites: lint, go, web, e2e
make test-vm SUITE=go        # go test -race ./... (CGO_ENABLED=1)
make test-vm SUITE=lint      # golangci-lint v2.14.0 (Linux, also auch die _linux-Dateien), eslint, tsc
make test-vm SUITE=web       # Vitest
make test-vm SUITE=e2e       # Playwright: echtes Backend (Dev-Modus) und Mock
```

Die Suite `go` installiert beim ersten Lauf restic per `scripts/install-restic.sh` und setzt `MCPANEL_TEST_RESTIC=/usr/local/bin/restic`; ohne diese Variable (auf dem Mac immer) überspringen sich die Integrationstests des Dateidienstes mit echtem restic.

- `scripts/test-vm.sh` kopiert den Arbeitsstand per `rsync --delete` nach `~/mc-panel-test/<Name des Checkouts>/` auf der VM: getrackte und neue Dateien, ohne `.git` und ohne das, was `.gitignore` ausschließt (die Ausschlüsse stehen im Skript). Ein Push ist nicht nötig. Jeder Checkout bzw. Worktree bekommt sein eigenes Verzeichnis. Die Läufe aller Checkouts sind per `flock` serialisiert (parallele Race- und Vitest-Läufe mehrerer Teammates überlasten die VM und lassen zeitkritische Tests scheitern); ein wartender Lauf meldet „waiting for another test run on this VM“. `node_modules` und Build-Ausgaben bleiben zwischen Läufen erhalten.
- Dann läuft dort `scripts/vm-test.sh <suite>` als normaler Benutzer `claude`. Beim ersten Bedarf richtet es ein: golangci-lint (Release-Archiv mit Prüfsumme nach `~/.local/bin`), `npm ci` (erneut nur, wenn sich `package-lock.json` ändert) und Playwright-Chromium samt Systempaketen (`sudo`, einmal pro Playwright-Version). Go und Node kommen aus `make deploy-testvm`; passt die Go-Version nicht zu `go.mod`, bricht das Skript mit einem Hinweis ab.
- `e2e` baut UI und Binaries, startet agent, `files -dev` und `panel serve -dev` mit eigener Konfiguration (`.dev/panel-vm.toml`: Port 18080, das panel liefert die gebaute UI selbst aus), legt `e2e-admin` an und führt `npm run e2e:real` und `npm run e2e` (Mock, Port 5174) aus. Das deployte panel (`127.0.0.1:8080`, systemd) bleibt unberührt.
- Einzelne Tests nach einem `make test-vm`: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/<dir> && PATH=/usr/local/go/bin:$PATH go test ./internal/rbac -run TestName'` bzw. im Unterordner `web` mit `PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run <datei>`.
- SSH zur VM braucht in Claude Code eine Bash ohne Sandbox.
- Was nur in der CI läuft: die Root-Abnahmen (`nftables`, `instances`; sie verändern den Host), `govulncheck`, `npm audit`, die sqlc- und `gen:api`-Aktualitätsprüfungen, OpenAPI-Lint und der GoReleaser-Snapshot.

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

## Produktion: Listen-Adresse und Reverse Proxy

- Der agent braucht Docker (Engine ≥ 25, API ≥ 1.44) und den Systembenutzer `mcpanel-mc` (eigene UID/GID, nicht die des panels); beides richtet später der Installer (Teilprojekt 6) ein. `instances_dir` (Standard `/srv/mc-panel`) darf kein Symlink sein und nur unter root-eigenen, nicht gruppen-/weltbeschreibbaren Verzeichnissen liegen.

- Der Dateidienst `files` (Spec 3b §3.1) läuft als `mcpanel-mc` ohne Zusatzgruppen und ohne Capabilities, mit `NoNewPrivileges`, `ProtectSystem=strict`, `ReadWritePaths=<instances_dir>/instances <instances_dir>/backups`, `MemoryMax=1G`, `TasksMax=512` (Begrenzung gegen präparierte Archive, Platz für restic), `IOWeight=50` und `CPUWeight=50`. Er nimmt nur Verbindungen der panel-UID und von root an (`panel_uid`; die Peer-UID-Prüfung ist die eigentliche Zugangskontrolle, wie beim agent). Den Socket legt systemd an (Socket-Aktivierung, `mcpanel-files.socket`: `ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660`, `DirectoryMode=0755`), der Dienst übernimmt ihn über `LISTEN_FDS`. Ohne Aktivierung (Dev, CI) legt er `socket` selbst mit `socket_mode` an (Gruppe = seine primäre Gruppe). Mit `UMask=0027` entstehen in Produktion Dateien mit 0640 und Ordner mit 0750 (der Dienst fordert 0644/0755 an). Verbindliche Vorgaben für die Units des Installers (Teilprojekt 6): eigenes Socket-Verzeichnis `/run/mc-panel-files` (nie `/run/mc-panel` des agents), keine `SupplementaryGroups`, Konfiguration in `/etc/mc-panel-files/files.toml` (`root:mcpanel-mc`, Verzeichnis 0750, Datei 0640 – `/etc/mc-panel` ist für `mcpanel-mc` unlesbar), `instances/` als `root:mcpanel-mc 0750` (setzt der agent beim Start), `deleted/` bleibt `root:root 0700`, `backups/` ist `mcpanel-mc:mcpanel-mc 0700` (setzt der agent beim Start; muss vor dem Start des Dienstes existieren, sonst scheitert `ReadWritePaths`). Das panel findet den Dienst über `files_socket` in `panel.toml`; `scripts/testvm-deploy.sh` ist die Referenz.
- Datei-Uploads und -Downloads (bis 4 GiB) laufen als ein Stream durch Caddy und das panel; Caddy begrenzt die Body-Größe standardmäßig nicht. Ein anderer Proxy braucht ein Body-Limit ≥ 4 GiB und darf Uploads nicht puffern.
- **Ausgehende Verbindungen des panels** (nur HTTPS, Port 443, User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`): Versionskataloge `piston-meta.mojang.com`, `fill.papermc.io`, `api.purpurmc.org`, `meta.fabricmc.net`, `maven.neoforged.net`; Plugins/Mods `api.modrinth.com`, `hangar.papermc.io` (API) sowie `cdn.modrinth.com`, `hangarcdn.papermc.io` (Jars und Icons; andere Hosts lehnt das panel ab, auch als Weiterleitung). Eine Egress-Firewall muss diese Hosts für den Benutzer `mcpanel` erlauben. Der Dateidienst und der agent brauchen für Plugins kein Netz; die Minecraft-Container selbst laden weiterhin, was itzg beim Start braucht (Server-Jars, Modpacks).
- **Plugins/Mods** (Spec 3c): Das panel lädt Jars als Stream (≤ 256 MiB) und reicht sie an den Dateidienst weiter, der die Prüfsumme des Upstreams (Modrinth sha512, Hangar sha256) vor dem `rename` prüft (`PUT /v1/upload?…&sha512=…`); bei Abweichung bleibt nichts liegen. Quelle der Wahrheit ist der Ordner `plugins/` bzw. `mods/`; die Tabelle `plugin_installs` merkt sich nur die Herkunft panel-installierter Jars. Jobs (Installieren, Aktualisieren) laufen im Speicher, einer pro Server und vier im panel; ein Neustart des panels bricht laufende Jobs ab, bereits installierte Jars bleiben. Icons gehen über den Proxy `/api/servers/{id}/plugins/icon` (die CSP bleibt `img-src 'self' data:`, kein fremder Bild-Host). Modrinth drosselt auf 300 Anfragen/min pro IP; das panel begrenzt Upstream-Abfragen (Suche, Versionen, Auflösen, Updatecheck mit `refresh`) auf 60/min pro Konto (Burst 20) und cacht Suche (5 min), Projekte/Versionen (15 min), Hash-Zuordnung und Updates (1 h).
- **Backups** (Spec 4): Der Dateidienst startet restic (mindestens 0.17, Referenz 0.19.1 über `scripts/install-restic.sh`; Pfad `restic_binary` in `files.toml`, Default `/usr/bin/restic`) als Kindprozess ohne Netz. restic öffnet nie Pfade in einem Datenverzeichnis: Backups sind ein tar-Stream, den der Dienst über `os.Root` erzeugt (ohne Symlinks, verborgene Dateien und RCON-Passwort), Restore und Download lesen `restic dump`. Ein Repo pro Server unter `<instances_dir>/backups/repos/<id>`, das Passwort in `backups/keys/<id>`. **`backups/keys/` gehört in jede Sicherung des Hosts**: Ohne Schlüssel ist ein Repo unlesbar. Offsite-Ziele gibt es noch nicht; Repos sind in sich geschlossen und lassen sich samt Schlüssel per `rsync -a <instances_dir>/backups/ <ziel>/` auf einen anderen Rechner kopieren. `backups/` liegt auf demselben Dateisystem wie `instances/`; ein Restore braucht dort freien Platz in Höhe des Snapshots plus `reserve_bytes`. Notfall-Restore ohne panel (Server vorher stoppen): `sudo -u mcpanel-mc env RESTIC_REPOSITORY=/srv/mc-panel/backups/repos/<id> RESTIC_PASSWORD_FILE=/srv/mc-panel/backups/keys/<id> restic --no-cache snapshots` und `… restic --no-cache dump <snapshot> /data.tar > /tmp/welt.tar`, dann in ein leeres Verzeichnis entpacken und prüfen. Zeitpläne laufen in der Zeitzone aus den Einstellungen (Default `Europe/Berlin`); verpasste Termine (panel aus) holt das panel einmal nach.
- Standard ohne `-dev` ist `listen = "unix:/run/mc-panel/panel.sock"`: das panel lauscht nur auf einem Unix-Socket (Modus 0660, Gruppe = primäre Gruppe des panel-Benutzers `mcpanel`). Ein verwaister Socket wird beim Start nur entfernt, wenn niemand mehr darauf lauscht. Das Socket-Verzeichnis liefern die systemd-Units des Installers (Teilprojekt 6). Verbindliche Vorgabe dafür: Das panel bekommt ein **eigenes** Runtime-Verzeichnis (z. B. `RuntimeDirectory=mc-panel-web`, Besitzer `mcpanel`), nicht `/run/mc-panel` des agents — systemd löscht das Runtime-Verzeichnis einer Unit bei deren Stopp, und `mcpanel` darf den agent-Socket nicht ersetzen können. Der Pfad im Beispiel unten ändert sich entsprechend.
- Caddy terminiert TLS und spricht den Socket an:

  ```
  panel.example.com {
      reverse_proxy unix//run/mc-panel/panel.sock
  }
  ```

  Caddy bekommt Zugriff über eine **eigene** Socket-Gruppe (z. B. Gruppe `caddy` als Socket-Gruppe), **nicht** durch Aufnahme in die Gruppe `mcpanel` (sonst könnte Caddy den agent-Socket löschen oder ersetzen).
- Requests über den Unix-Socket gelten als vom Proxy kommend: die Client-IP ist der rechteste nicht vertrauenswürdige Eintrag in `X-Forwarded-For` (Caddy setzt ihn). Fehlt ein verwertbarer Eintrag, wird der Request abgewiesen (`403 ip_blocked`); Health-Checks direkt am Socket brauchen deshalb einen Header, z. B. `curl --unix-socket /run/mc-panel/panel.sock -H 'X-Forwarded-For: 127.0.0.1' http://panel/api/health`. Aktive Caddy-Health-Checks (`health_uri`) senden kein `X-Forwarded-For` und würden mit `403` scheitern — entweder `health_headers` setzen oder auf aktive Checks verzichten. Andere Proxys müssen `X-Forwarded-For` ohne Port setzen, sonst `403`.
- TCP bleibt möglich (`listen = "127.0.0.1:8080"`); dann wird `X-Forwarded-For` nur von `trusted_proxies` übernommen. Loopback ist nur im Dev-Modus von „Admin-Login nur aus Allowlist“ ausgenommen.
- Die Konsole streamt das Server-Log als Server-Sent Events (`GET /api/servers/{id}/console/stream`, `text/event-stream`, Heartbeat-Kommentar alle 15 s, ein Stream pro offenem Konsolen-Tab, höchstens 4 pro Benutzer, der 5. bekommt `429 rate_limited`). Caddy reicht SSE ohne Pufferung und ohne Zusatzkonfiguration durch. Ein anderer Proxy darf die Antwort nicht puffern (das panel setzt `X-Accel-Buffering: no` für nginx) und braucht ein Lese-Timeout über 15 s.
- Außerhalb des Dev-Modus sendet das panel `Strict-Transport-Security: max-age=31536000`.
- CLI-Befehle auf dem Server immer als panel-Benutzer ausführen (`sudo -u mcpanel panel admin unlock …`), sonst entstehen root-eigene `panel.db-wal`/`-shm`, die der Dienst nicht öffnen kann.
- Eine 2FA-Sperre (10 falsche Codes in 15 min) hebt `admin unlock` nicht auf; sie endet nach 15 min oder mit einem panel-Neustart.
- Die nftables-Sets fassen je 65 536 Einträge; ist ein Set voll, schlägt der Firewall-Ban fehl (wird geloggt), die Sperre in der App gilt trotzdem.

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
| `make release` | GoReleaser-Snapshot nach `dist/`: Archive für linux/amd64 und linux/arm64 plus `checksums.txt` |
| `make deploy-testvm` | baut `REF` (Standard `main`) auf der Test-VM aus GitHub und deployt ihn dort (siehe „Test-VM“) |
| `make test-vm` | kopiert den Arbeitsstand auf die Test-VM und führt dort Tests aus, `SUITE=all\|go\|lint\|web\|e2e` (siehe „Tests auf der Test-VM“) |
| `make clean` | entfernt `bin/`, `dist/`, `.dev/` und das gebaute Frontend |

## CI

Ein Pull Request führt nur die Jobs aus, die seine geänderten Dateien brauchen; der erste Job `changes` vergleicht dafür mit `git diff` gegen die Merge-Basis. Pushes auf `main`, der wöchentliche Lauf und manuelle Starts führen immer alles aus, ebenso jede Änderung an `.github/workflows/ci.yml`. Der Job `CI OK` fasst zusammen (grün, wenn jeder Job bestanden hat oder nicht nötig war); auf ihn wartet `gh pr checks <n> --watch`. Die Jobs laufen parallel, keiner wartet auf einen anderen.

| CI-Job | Läuft im PR bei Änderungen an | Auf der Test-VM bzw. von Hand |
|---|---|---|
| `go` | `cmd/`, `internal/`, `api/`, `go.mod`/`go.sum`, `sqlc.yaml`, `.golangci.yml`, `Makefile`, `scripts/install-restic.sh` | `make test-vm SUITE=go` und `SUITE=lint`; nur CI: `go mod verify && go mod tidy -diff`, sqlc-Aktualität (Release-Binary der Version aus dem Header von `internal/store/db.go`), `govulncheck` |
| `web` | `web/`, `api/` (Preview-Image nur bei `web/Dockerfile.preview`, `compose.preview.yaml`, `web/package*.json`, `web/vite.config.ts`) | `make test-vm SUITE=web` und `SUITE=lint`; nur CI: Build, `gen:api`-Aktualität, `npm audit`, Preview-Image |
| `openapi` | `api/` | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| `e2e` | `cmd/`, `internal/`, `api/`, `web/`, `go.mod`/`go.sum` | `make test-vm SUITE=e2e` |
| `nftables` | `internal/agent/`, `internal/defense/`, `internal/auth/`, `cmd/agent/`, `cmd/panel/`, `scripts/ci-acceptance.sh`, `go.mod`/`go.sum` | nur CI (siehe unten) |
| `instances` | `cmd/`, `internal/` (die Abnahme startet agent, Dateidienst und panel samt Login, TOTP und Audit), `scripts/ci-instances.sh`, `scripts/mc-status.py`, `scripts/install-restic.sh`, `go.mod`/`go.sum` | nur CI (siehe unten) |
| `build` | `cmd/`, `internal/webui/`, `.goreleaser.yaml`, `Makefile`, `web/package*.json`, `web/vite.config.ts`, `go.mod`/`go.sum` | `make build`, `make release` |

Der Job `go` führt die Tests nur mit Race-Detector aus (`make test-race` deckt alle Tests ab); der Release-Workflow führt weiterhin `make test` und `make test-race` aus.

Details der CI-Jobs:

| CI-Job | Inhalt |
|---|---|
| `go` | `go mod verify && go mod tidy -diff`, `sqlc generate && git status --porcelain internal/store`, restic per `scripts/install-restic.sh` (`MCPANEL_TEST_RESTIC`), `make test-race`, golangci-lint v2.14.0, `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| `web` | `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && npm run gen:api && git status --porcelain . && npm audit --audit-level=high` |
| `openapi` | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| `nftables` | nur in der CI (Linux, root): `go test -tags nftintegration -exec sudo ./internal/agent/...` und der Abnahmetest `sudo scripts/ci-acceptance.sh bin/panel bin/agent` (5 Fehlversuche → IP im nftables-Jail). Das Skript verändert den Host (Benutzer, Netz-Namespace, nftables) und ist nur für Wegwerf-Runner gedacht. |
| `nftables` (Docker-Integration) | nur in der CI: `go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" ./internal/agent/...` |
| `instances` | nur in der CI: `sudo scripts/ci-instances.sh bin/agent bin/files bin/panel` (echter agent + Docker + Paper-Server: Routing, Härtung, Konsole mit RCON, Log-Stream und Spielerlisten, präparierte Spielerdateien, Absturz-Neustart, Löschen; Dateidienst als `mcpanel-mc`: Peer-UID (panel-Benutzer und root erlaubt, `nobody` abgewiesen), Upload, Schwärzen, Editor-Sperre, Symlinks, Ordner-Zip, Welt entpacken, bösartige Archive; Plugins: panel im Produktionsmodus mit TOTP-Login und Sudo, zweiter Paper-Server über die API, Chunky von Modrinth und ViaBackwards + ViaVersion von Hangar über die echten Upstreams (eine Wiederholung bei Upstream-Fehlern), Prüfsummen im Dateidienst, Icon-Proxy, Neustart mit RCON `plugins`, Deaktivieren, Entfernen, Audit; Backups mit echtem restic auf demselben Server: manuelles Backup mit `save-off`/`save-on` im Log, Repo und tar-Inhalt, Restore mit Sudo (Start gesperrt, Sicherheits-Snapshot, Neustart), Zip-Download, Zeitplan in der Zeitzone des panels, Pin, Löschen, Purge beim Löschen des Servers, Audit; verändert den Host und braucht Internet) |
| `build` | `make build`, `make release`, dann `file dist/*/panel dist/*/agent dist/*/files` |

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

Der Workflow `.github/workflows/release.yml` prüft zuerst, dass der getaggte Commit in `main` enthalten ist (sonst Abbruch), führt `make lint`, `make test` und `make test-race` aus, baut das Frontend und veröffentlicht mit GoReleaser (nur dieser letzte Schritt sieht `GITHUB_TOKEN`):

- `mc-panel_<version>_linux_amd64.tar.gz`, `mc-panel_<version>_linux_arm64.tar.gz` (je `panel`, `agent` und `files`, ohne Unterordner)
- `checksums.txt` (SHA-256, Format `<hash>  <datei>`)

`<version>` ist der Tag ohne führendes `v`. Tags mit Suffix (z. B. `v0.2.0-rc.1`) werden als Pre-Release markiert.
Vor dem Taggen lokal prüfen: `make release && (cd dist && shasum -a 256 -c checksums.txt)`.

Hinweise zu GoReleaser:

- Meilenstein-Tags wie `phase0` sind kein SemVer und werden per `git.ignore_tags: ["phase*"]` in `.goreleaser.yaml` ignoriert.
- `goreleaser check` und die Git-Statusermittlung brauchen einen konfigurierten Git-Remote. Ohne Remote funktioniert `make release` im Snapshot-Modus trotzdem (Version `0.0.1-snapshot+none`); `goreleaser check` bricht mit `no remote configured` ab. Ohne den Repo-Remote zu ändern, hilft: `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=remote.origin.url GIT_CONFIG_VALUE_0=https://github.com/example/mc-panel.git goreleaser check`.
- Ab Go 1.27 speichert `go version -m` die `-ldflags` nicht mehr. Ob die Version eingelinkt wurde, zeigt nur der String selbst: `grep -aF "$(jq -r .version dist/metadata.json)" dist/agent_linux_arm64_v8.0/agent` (setzt `var version` in `package main` voraus).

