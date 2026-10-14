# mc-panel – Teilprojekt 6: Installer (Design-Spec)

- **Datum:** 2026-10-13
- **Status:** entworfen
- **Autor:** Jannes Göbel (mit Claude)
- **Baut auf:** allen vorherigen Specs (TP1–TP5); sammelt deren Vorgaben „für den Installer (TP6)“

## 1. Ziel und Kontext

Ein Befehl richtet mc-panel auf einem frischen Ubuntu-26.04-VPS vollständig ein: Pakete, Systembenutzer, Verzeichnisse, Binaries, Konfiguration, systemd-Units, Caddy mit Let's-Encrypt-Zertifikat, Host-Firewall, Sicherheitsupdates und den ersten Admin. Derselbe Installer aktualisiert (mit DB-Sicherung und automatischem Rollback), prüft den Zustand (`doctor`) und entfernt das Panel wieder (`uninstall`, Daten bleiben).

### 1.1 Erfolg

```
curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
  | sudo bash -s -- install --domain panel.example.com --email you@example.com
```

führt auf einem frischen Ubuntu 26.04 nach wenigen Minuten zu einem erreichbaren Panel unter `https://panel.example.com` mit gültigem Zertifikat und angelegtem Admin. Ein zweiter Lauf ändert nichts. `update` bringt eine neue Version ohne Datenverlust und rollt bei einem fehlgeschlagenen Health-Check selbst zurück. `doctor` zeigt jeden Konfigurationsfehler mit Abhilfe. Die Test-VM wird mit genau diesem Installer deployt.

### 1.2 Leitlinie

Der Installer ist die einzige Quelle der Wahrheit für die Systemeinrichtung (Benutzer, Rechte, Units, Konfiguration). Er setzt die Sicherheitsvorgaben aller Teilprojekte um (Privilegientrennung panel/agent/files, Socket-Rechte, Unit-Härtung) und ändert am System nur, was das Panel braucht. SSH, Kernel-Parameter und fremde Dienste fasst er nicht an.

### 1.3 Nicht-Ziele (TP6)

- Andere Distributionen und Ubuntu 24.04 (24.04 nur über den Test-Override, §3.1).
- SSH-Härtung, fail2ban, sysctl-Tuning (nur Warnungen in `doctor`).
- Eigenes Dateisystem bzw. Loop-Image für `/srv/mc-panel` (nur Warnung in `doctor`; die Vorgabe aus TP2 §3.3 bleibt eine Empfehlung an den Betreiber).
- Wildcard-Zertifikate per DNS-Challenge.
- Signierte Releases; geprüft wird die SHA-256 aus `checksums.txt` desselben Releases über HTTPS.
- Automatische Updates per Timer; Migration manueller Installationen (außer der Test-VM).

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Form | Ein Bash-Skript `install.sh` mit `install`, `update`, `doctor`, `uninstall`; Quellen pro Schritt in `scripts/install/`, zusammengefügt beim Release | Ein Befehl per `curl \| bash`, keine Laufzeitabhängigkeiten auf dem VPS |
| Herkunft | Release-Tarball + `checksums.txt` von GitHub (HTTPS), SHA-256-Prüfung vor dem Entpacken; `--from-dir` für lokale Builds | Einfach; Signaturen sind bewusst zurückgestellt |
| System | Nur Ubuntu 26.04 LTS, Pakete aus den Ubuntu-Repos | Ein Paketstand, testbar |
| TLS | Caddy (Ubuntu-Paket) mit Let's Encrypt über HTTP-Challenge, Panel-Domain + E-Mail als Parameter | Standardweg ohne Plugins |
| Härtung | Host-Firewall (nftables, eigene Tabelle), unattended-upgrades für Sicherheitsupdates; SSH nur prüfen | Was das Panel braucht, ohne Aussperr-Risiko |
| Speicher | Installation auf das vorhandene Dateisystem; `doctor` warnt ohne eigenes Dateisystem für `/srv/mc-panel` | Kein Eingriff in Partitionen |
| Update | DB-Kopie + alte Binaries/Units sichern, tauschen, Health-Check, automatischer Rollback | Migrationen sind vorwärts-only; ein fehlgeschlagenes Update darf das Panel nicht liegen lassen |
| Uninstall | Programm weg, Daten bleiben; `--purge` mit Bestätigung durch Abtippen der Domain | Welten und Backups sind wertvoller als Ordnung |
| Test-VM | `testvm-deploy.sh` ruft den Installer mit `--from-dir` auf | Jeder Deploy testet Installer und Update-Pfad |

### 2.1 Verworfene Alternativen

- **Go-Binary `mcpanelctl` mit Bootstrap-Skript:** besser testbar, aber deutlich mehr Code; das Skript bleibt für den Nutzer gleich.
- **Ansible:** zusätzliche Abhängigkeit, kein Ein-Befehl-Erlebnis.
- **Docker-Compose-Installation des Panels:** widerspricht der Privilegientrennung (agent als root auf dem Host, files als `mcpanel-mc`).
- **Docker CE und Caddy aus Hersteller-Repos:** neuere Versionen, aber fremde APT-Quellen und Schlüssel; die Ubuntu-Pakete reichen.

## 3. Aufbau

```
scripts/install/
├── main.sh        Argumente, Unterbefehle, Logging, Fehlerbehandlung (set -euo pipefail, trap)
├── lib.sh         Hilfsfunktionen (log, die, confirm, version_ge, render_template, toml_ensure_key)
├── preflight.sh   §4.1
├── packages.sh    §4.2
├── layout.sh      §4.3 Benutzer und Verzeichnisse
├── binaries.sh    §4.4 Laden, Prüfen, Installieren
├── config.sh      §4.5
├── units.sh       §4.6
├── caddy.sh       §4.7
├── firewall.sh    §4.8
├── admin.sh       §4.10
├── update.sh      §5
├── doctor.sh      §6
├── uninstall.sh   §7
└── test.sh        Bash-Tests der reinen Funktionen (§9)
deploy/
├── systemd/*.service, *.socket                  Unit-Vorlagen
├── caddy/mc-panel.caddy                         Caddy-Vorlage
└── nftables/mcpanel_fw.nft                      Firewall-Vorlage
```

- `make installer` fügt `lib.sh`, die Schritt-Dateien und `main.sh` zu `build/install.sh` zusammen (nicht `dist/`, das goreleaser leert) und bettet die Vorlagen aus `deploy/` als Heredocs ein; die Release-Version wird als `MCPANEL_VERSION` eingetragen. goreleaser hängt `install.sh` als Release-Asset an und nimmt es in `checksums.txt` auf; der Release-Tarball enthält zusätzlich `deploy/`.
- Logging nach `/var/log/mc-panel-install.log` (0600) und knapp auf stdout; jeder Schritt meldet „ok / geändert / übersprungen“. Fehler beenden mit Exit-Code ≠ 0 und Verweis auf das Log.
- Alle Schritte sind idempotent: sie prüfen den Ist-Zustand und ändern nur Abweichungen.

### 3.1 Parameter

`install`: `--domain <fqdn>` (Pflicht außer mit `--tls internal`), `--email <adresse>` (Pflicht bei Let's Encrypt), `--version vX.Y.Z` (Default: Version des Skripts), `--from-dir <pfad>` (Binaries + `deploy/` lokal), `--tls letsencrypt|internal` (Default `letsencrypt`), `--ssh-port <n>` (Default aus `sshd -T`), `--allow-port PORT[/tcp|/udp]` (weitere offene Ports, mehrfach, §4.8), `--no-firewall`, `--admin-user <name>` (Passwort dann über stdin, wenn `install.sh` als Datei läuft, oder aus `--admin-password-file <datei>`, root-eigen, 0600/0400 – bei `curl | bash` ist stdin das Skript), `--yes` (Warnungen nicht nachfragen). `update`: `--version`, `--from-dir`, `--check`, `--force`. `doctor`: `--json`. `uninstall`: `--purge`, `--yes`.

Umgebung nur für Tests: `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1` erlaubt andere Ubuntu-Versionen (CI-Runner 24.04).

Ausgabe: jeder Schritt beginnt mit `==> <schritt>`, jedes Ergebnis mit `[ok]`, `[changed]`, `[skip]` oder `[warn]` (die CI prüft, dass ein zweiter Lauf kein `[changed]` meldet).

## 4. `install`

### 4.1 Vorprüfung

root; `/etc/os-release` = Ubuntu 26.04 (sonst Abbruch, außer Override); Architektur `x86_64`/`aarch64`; systemd als PID 1; Ports 80, 443 und 25565 frei oder von `caddy` bzw. dem Router belegt; Domain-Auflösung (A/AAAA) enthält eine Adresse dieses Hosts (Abgleich mit lokalen Adressen und, wenn erreichbar, der öffentlichen IP über die Quelladresse der Route) – Abweichung ist eine Warnung (Let's Encrypt würde scheitern), mit `--yes` geht es weiter.

### 4.2 Pakete

`apt-get install` (nur fehlende): `docker.io` (übersprungen, wenn `docker info` bereits antwortet, z. B. vorinstalliertes Docker CE), `caddy`, `nftables`, `restic`, `tzdata`, `sqlite3`, `unattended-upgrades`, `curl`, `ca-certificates`, `bzip2`. Danach Versionsprüfungen: restic ≥ 0.17 (TP4), Docker-Daemon erreichbar. Docker bekommt `/etc/docker/daemon.json` nur, wenn es fehlt: `{"log-driver": "json-file", "log-opts": {"max-size": "10m", "max-file": "3"}, "live-restore": true}`. Docker wird dafür nur neu gestartet, wenn keine mcpanel-Container laufen; sonst `[warn]`, dass die Einstellung beim nächsten Docker-Neustart greift. `restic_binary` ist der Pfad aus `command -v restic` (Version ≥ 0.17, sonst Abbruch mit Hinweis auf ein neueres restic).

### 4.3 Benutzer und Verzeichnisse

Wie die Spezifikationen und `testvm-deploy.sh` heute: Systembenutzer `mcpanel` und `mcpanel-mc` (keine Shell, kein Home-Login); `/etc/mc-panel` `root:mcpanel 0750`, `/etc/mc-panel-files` `root:mcpanel-mc 0750`, `/var/lib/mc-panel` `mcpanel:mcpanel 0700`, `/srv/mc-panel` `root:root 0755`. Zusätzlich legt der Installer `/srv/mc-panel/instances` (`root:mcpanel-mc 0750`) und `/srv/mc-panel/backups` (`mcpanel-mc:mcpanel-mc 0700`) an, bevor die Units starten: `mcpanel-files.service` nennt beide in `ReadWritePaths`, und systemd verlangt, dass sie existieren (sonst `226/NAMESPACE`). Die weiteren Unterverzeichnisse legt der agent an (TP2 §3.3, TP3b §3.3, TP4 §3.1).

### 4.4 Binaries

Tarball `mc-panel_<version>_linux_<arch>.tar.gz` und `checksums.txt` desselben Releases laden (`curl --proto =https --tlsv1.2 -fsSL`, Wiederholung 3×), SHA-256 vergleichen, in ein temporäres Verzeichnis entpacken, `panel`, `agent`, `files` nach `/usr/local/bin` (0755, atomar per `install` + `mv`). `--from-dir` überspringt den Download und prüft nur, dass die drei Binaries und `deploy/` vorhanden sind und `panel version` antwortet.

### 4.5 Konfiguration

`panel.toml`, `agent.toml`, `files.toml` werden aus den Vorlagen angelegt, wenn sie fehlen: Sockets, `panel_uid` (UID von `mcpanel`), `instances_dir`, `restic_binary` (§4.2), `monitoring_ping_addr`, `public_url = "https://<domain>"`, `listen = "unix:/run/mc-panel-web/panel.sock"` (§4.6). Vorhandene Dateien bleiben unverändert; fehlen darin Pflichtschlüssel, ergänzt `install`/`update` nur diese Schlüssel am Ende (mit Kommentar „ergänzt von install.sh <version>“) und ändert nie bestehende Werte. Rechte: `panel.toml` `root:mcpanel 0640`, `agent.toml` `root:root 0600` (der agent läuft als root, niemand sonst muss sie lesen), `files.toml` `root:mcpanel-mc 0640`.

### 4.6 systemd

Units aus `deploy/systemd/`: `mcpanel-agent.service`, `mcpanel-files.socket` + `mcpanel-files.service`, `mcpanel-panel.socket` + `mcpanel-panel.service`, `mcpanel-fw.service` (§4.8); Härtung wie in den Specs (TP1, TP3b §3.1, TP4 §3.2). Neu: der Panel-Socket kommt per systemd-Socket-Aktivierung (`ListenStream=/run/mc-panel-web/panel.sock`, `SocketUser=mcpanel`, `SocketGroup=caddy`, `SocketMode=0660`, `DirectoryMode=0755`), damit Caddy ihn erreicht, ohne Mitglied der Gruppe `mcpanel` zu sein; das panel übernimmt ihn über `LISTEN_FDS` wie der Dateidienst (TP6 ergänzt das in `cmd/panel`, Code aus `internal/unixsock`). Danach `daemon-reload`, `enable`, Start in der Reihenfolge agent → files → panel, warten auf `active` (60 s).

### 4.7 Caddy

`/etc/caddy/mc-panel.caddy` aus der Vorlage: Site `<domain>` mit `tls <email>` (bzw. `tls internal`), `reverse_proxy unix//run/mc-panel-web/panel.sock`, Sicherheits-Header kommen vom panel selbst. `/etc/caddy/Caddyfile` bekommt einmalig die Zeile `import mc-panel.caddy` (markiert mit Kommentar); bestehende Sites bleiben. `caddy validate`, dann `systemctl reload caddy`. Bei `letsencrypt` wartet der Installer bis 120 s auf ein Zertifikat (`curl https://<domain>/api/health`) und meldet sonst eine Warnung mit den letzten Caddy-Log-Zeilen (häufigste Ursache: DNS oder Port 80 blockiert).

### 4.8 Firewall

Eigene Tabelle `inet mcpanel_fw` (nie `inet mcpanel` des agent) aus `deploy/nftables/mcpanel_fw.nft`: Kette `input` (Hook input, Priorität 0, Policy drop) erlaubt `iif lo`, `ct state established,related`, ICMP/ICMPv6 (inkl. Neighbor Discovery), TCP auf SSH-Port, 80, 443, 25565 und UDP 443 (HTTP/3 von Caddy). Weitergeleiteter Verkehr (Docker) ist nicht betroffen. `mcpanel-fw.service` (oneshot, `Before=network-pre.target` und `Wants=network-pre.target`, damit die Regeln vor dem Netz stehen, `RemainAfterExit`) lädt die Tabelle beim Booten; `ExecStop` entfernt sie. Vor dem Aktivieren (schon in der Vorprüfung und noch einmal direkt vor `nft -c`) bestimmt der Installer die SSH-Ports: `--ssh-port`, sonst die Ports aus `sshd -T` (`listenaddress` mit Port vor `port`), abgeglichen mit den Sockets, auf denen sshd bzw. `ssh.socket` tatsächlich lauscht – weichen sie ab, Abbruch; nur wenn beides fehlt, der Port der laufenden Sitzung mit Warnung. Jeder Port einer laufenden SSH-Sitzung (`$SSH_CONNECTION`, das `sudo` verwirft, und die bestehenden Verbindungen auf einem sshd-Port) muss in der Regel enthalten sein, sonst Abbruch. Alle übrigen lauschenden Ports außerhalb von Loopback, die die Regeln sperren würden, listet der Installer auf und fragt nach (ohne Terminal und ohne `--yes`: Abbruch). `--allow-port PORT[/tcp|/udp]` (mehrfach, Default `tcp`) öffnet solche Ports als eigene Regelzeilen (`# --allow-port`); die Regeln entstehen bei jedem `install` neu aus den Argumenten, ein späteres `install` ohne dieselben `--allow-port` schließt die Ports also wieder; `--yes` sperrt sie ohne Rückfrage. Die Regeln liegen in `/etc/mc-panel/mcpanel_fw.nft` und werden vor dem Schreiben und vor dem Laden mit `nft -c` geprüft. `--no-firewall` überspringt den Schritt (die Unit wird geschrieben, aber nie aktiviert); `doctor` warnt dann.

### 4.9 Sicherheitsupdates

`unattended-upgrades` nur mit dem Ursprung `${distro_id}:${distro_codename}-security`, ohne automatischen Neustart; `/etc/apt/apt.conf.d/20auto-upgrades` mit täglichem Lauf.

### 4.10 Erster Admin und Abschluss

Gibt es noch keinen Admin (`panel admin list`, neu und lesend, oder Prüfung über den Exit-Code von `admin create`), fragt der Installer Benutzername und Passwort über `/dev/tty` ab (zweimal, nicht sichtbar) bzw. nimmt `--admin-user` mit Passwort über stdin, und ruft `panel admin create -config /etc/mc-panel/panel.toml` als Benutzer `mcpanel` auf. Zum Schluss läuft `doctor`; die Ausgabe nennt URL, Admin-Namen und die nächsten Schritte (Basisdomain mit Wildcard-DNS im Panel eintragen, 2FA einrichten, Benachrichtigungen einrichten, `backups/keys/` mitsichern).

## 5. `update`

1. Zielversion: `--version`, mit `--from-dir` die Version des Builds (`panel version`, ohne Netz), sonst die neueste Release (GitHub-API `releases/latest`; Tag immer `v<version>`); `--check` gibt nur „aktuell / neuere Version X“ aus. Gleiche Version ohne `--force` → Ende.
2. Das `install.sh` der Zielversion wird mit geladen (und geprüft) und übernimmt ab hier (`exec`), damit die Update-Logik zur neuen Version passt.
3. Sicherung nach `/var/lib/mc-panel-rollback/<alte-version>-<zeit>/` (`root:root 0700`, bewusst außerhalb von `/var/lib/mc-panel`, das `mcpanel` gehört: ein kompromittiertes panel darf dort nichts ablegen, was root beim Rollback installiert): die drei Binaries, die Unit-Dateien, die drei Konfigurationsdateien (`panel.toml`, `agent.toml`, `files.toml`, mit Besitzer und Modus in `config/files`; `update` ergänzt dort ggf. Schlüssel, der Rollback schreibt die alten Dateien atomar zurück) und die DB. Die DB-Kopie erstellt `mcpanel` selbst (`runuser -u mcpanel -- sqlite3 <db> ".backup '<data_dir>/.rollback-<zeit>.db'"`, konsistent im laufenden Betrieb); root prüft die Zwischendatei (reguläre Datei, kein Symlink, Besitzer `mcpanel`), liest sie per `cat` ins Rollback-Verzeichnis, danach löscht `mcpanel` sie. Root schreibt nie in `/var/lib/mc-panel`. Die drei neuesten Sicherungen bleiben (Sicherungen eines gescheiterten Rollbacks nie gelöscht).
4. Neue Binaries und Units installieren, fehlende Konfigurationsschlüssel ergänzen (§4.5), `daemon-reload`, Neustart agent → files → panel. Minecraft-Container laufen weiter (Neustart des agent stoppt keine Container; `live-restore` deckt einen Docker-Neustart durch Paket-Updates ab).
5. Health-Check (höchstens 90 s): alle Units `active`, `panel version`/`agent -version`/`files -version` melden die Zielversion, `GET /api/health` über den Socket `200`, agent- und files-Socket antworten (`/v1/health`).
6. Rollback bei Fehlschlag: Dienste stoppen, alte Binaries, Units und Konfigurationsdateien zurück, die DB-Kopie liest root aus dem Rollback-Verzeichnis und gibt sie per Pipe an `runuser -u mcpanel -- sh -c 'cat > <data_dir>/panel.db.restore && mv -f <data_dir>/panel.db.restore <data_dir>/panel.db'` (root folgt dort keinen Symlinks), `daemon-reload`, starten, Health-Check der alten Version; Meldung mit Ursache und Log-Verweis, Exit-Code 3. Scheitert auch das, bleibt alles gestoppt mit klarer Anleitung (Exit-Code 4).
7. Unvollständige Updates: Jedes Rollback-Verzeichnis hat eine Zustandsdatei `state` (`phase=backed_up|applying|verified|rolling_back|rolled_back|aborted|rollback_failed`, `from=`, `to=`), die vor jedem Abschnitt atomar und per `sync` geschrieben wird. Endete ein Lauf in `applying` oder `rolling_back` (Stromausfall, `kill -9`, abgebrochene SSH-Sitzung, die den Lauf trotzdem beendet), erledigt das nächste `update` das zuerst (`--check` meldet es nur, `doctor` zeigt es als ✗ `update_state`): `rolling_back` → der Rollback läuft erneut; nichts vom Austausch geschehen (Binaries, Units und Konfiguration gleich der Sicherung) → `aborted`, nichts zu tun; Zielversion installiert und gesund → `verified`, die DB bleibt; sonst ein Rollback inklusive DB-Kopie, der alle Änderungen seit der Sicherung verwirft – nur nach ausdrücklicher Zustimmung (Rückfrage mit dem Alter der Kopie, `--yes` oder `--force`), ohne Zustimmung Exit-Code 1 und nichts geändert. Strg-C, `TERM` und `HUP` während der Sicherung brechen ohne Änderung ab, während des Austauschs führen sie zum Rollback (der Rollback selbst ignoriert sie); Verzeichnisse in `applying`, `rolling_back` oder `rollback_failed` löscht die Aufräumregel (drei neueste) nie.

## 6. `doctor`

Prüfungen mit ✓ / ! / ✗ und einem Satz Abhilfe; Exit-Code 0 (alles ok), 1 (Warnungen), 2 (Fehler); `--json` für Skripte mit dem festen Schema `{"status": "ok|warn|fail", "checks": [{"id", "status", "message", "hint"}]}`. Ohne root laufen nur die Prüfungen, die keine root-Rechte brauchen. Die Pflichtschlüssel der Konfiguration sind dieselben Listen, die `install` ergänzt.

- Dienste: Units aktiv, Sockets mit erwartetem Besitzer und Modus, Prozess von `files` als `mcpanel-mc` ohne Capabilities und mit `no_new_privs` (wie heute in `testvm-deploy.sh`).
- Versionen: drei Binaries gleich; neuere Release verfügbar (!, nur mit Netz).
- Konfiguration: Pflichtschlüssel vorhanden, Rechte der Dateien und Verzeichnisse (§4.3, §4.5).
- Caddy: Konfiguration gültig, Import-Zeile vorhanden, Zertifikat für die Domain gültig und > 14 Tage (! darunter, ✗ abgelaufen), Domain zeigt auf den Host.
- Firewall: Tabelle `mcpanel_fw` geladen, SSH-Port erlaubt; ohne Firewall !.
- System: Docker erreichbar, restic ≥ 0.17, `tzdata` installiert, Zeitsynchronisation aktiv (`timedatectl show -p NTPSynchronized`), unattended-upgrades aktiv.
- Speicher: `/srv/mc-panel` auf eigenem Dateisystem (sonst ! mit Verweis auf TP2 §3.3), freier Platz (! unter 10 %), `backups/keys/` vorhanden (Hinweis: mitsichern).
- SSH (nur !): `PasswordAuthentication yes`, `PermitRootLogin yes`.

## 7. `uninstall`

- Entfernt: Units (stoppen, deaktivieren, löschen), Binaries, `/etc/caddy/mc-panel.caddy` und die Import-Zeile (danach Caddy-Reload), die nftables-Tabellen `inet mcpanel_fw` und `inet mcpanel`, alle Container mit Label `mcpanel.instance`, den Container `mcpanel-router` und das Netz `mcpanel-mc` (direkt per `docker`, der agent ist dann gestoppt), die Sockets.
- Bleiben: `/srv/mc-panel` (Welten, Backups, Schlüssel), `/var/lib/mc-panel` (DB), `/var/lib/mc-panel-rollback` (Sicherungen; `--purge` entfernt es), `/etc/mc-panel*`, Benutzer `mcpanel`/`mcpanel-mc`, alle Pakete (der Installer entfernt nie Pakete).
- `--purge`: zusätzlich Daten, Konfiguration und Benutzer; verlangt das Abtippen der Panel-Domain (Host von `public_url` in `panel.toml`, auch bei `tls internal`; nur ohne lesbare Domain das Wort „mc-panel“) auf dem Terminal, auch mit `--yes`; ohne Terminal bricht `--purge` ab. Was `--purge` nicht löschen kann, nennt es am Ende (Exit-Code 1); ein Mountpoint (z. B. `/srv/mc-panel` auf eigenem Dateisystem) bleibt, sein Inhalt geht.
- Eine spätere Neuinstallation findet DB, Konfiguration und Daten wieder und läuft wie ein Update auf demselben Stand.

## 8. Test-VM und CI

- **Test-VM:** `scripts/testvm-deploy.sh` baut wie bisher auf der VM und ruft danach `install.sh` aus dem Build auf: `install --from-dir <build> --tls internal --domain panel.192-168-1-117.sslip.io --no-firewall --yes` beim ersten Mal, sonst `update --from-dir <build> --force`. Die Heredocs für Units, Konfiguration und Caddy entfallen dort. Die VM behält ihre Firewall wie bisher (`--no-firewall`, die VM ist intern).
- **CI-Job `installer`** (`scripts/ci-installer.sh`; GitHub-Runner `ubuntu-24.04`, root, `MCPANEL_INSTALL_ALLOW_UNSUPPORTED=1`, nicht bei Pushes auf `main`; drei eigene Builds im Format von `--from-dir`; restic 0.19.1 per `scripts/install-restic.sh`, weil 24.04 nur 0.16 hat; `--tls internal` mit Domain `panel.ci.test` über `/etc/hosts`, Firewall an mit `--ssh-port 22`): `install` → `doctor` (keine ✗) → zweites `install` (keine Änderung gemeldet) → Login über Caddy mit dem angelegten Admin → `update --from-dir` auf denselben Build mit erhöhter Version (Zielversion aus `panel version` des Builds, ohne Netz) → `update` mit einem absichtlich defekten `panel` (Health-Check scheitert) → Rollback, alte Version läuft, DB unverändert → `uninstall` (Daten bleiben, Dienste und Tabellen weg) → `install` findet die Daten wieder → `uninstall --purge`. Let's Encrypt lässt sich in der CI nicht testen (manuelle Abnahme auf dem VPS).
- **Lint:** shellcheck für `scripts/install/*.sh` und das zusammengefügte `install.sh` im Lint-Job.

## 9. Tests

- `scripts/install/test.sh` (Bash, ohne neue Abhängigkeit, läuft im Lint-Job und auf der VM): Argument-Parsing und Fehlermeldungen, `version_ge`, Auswertung von `sshd -T`, Vorlagen-Rendering (Caddy, Units, nftables) gegen erwartete Dateien, `toml_ensure_key` (ergänzt fehlende Schlüssel, ändert nie vorhandene), Prüfsummenprüfung mit präparierter `checksums.txt` (passt / passt nicht / fehlt), Zusammenfügen (`make installer`) erzeugt ein lauffähiges Skript.
- Go: Socket-Aktivierung des panels (`LISTEN_FDS`, wie beim Dateidienst getestet), `panel admin list`.
- Ende-zu-Ende: CI-Job `installer` (§8) und jedes `make deploy-testvm`.

## 10. Abnahmekriterien

1. Auf einem frischen Ubuntu-26.04-VPS mit Domain führt der Einzeiler zu einem Panel mit gültigem Let's-Encrypt-Zertifikat und Admin-Login; ein zweiter Lauf meldet keine Änderungen.
2. `doctor` meldet auf dem frischen System nur die erwarteten Warnungen (eigenes Dateisystem, ggf. SSH) und nach absichtlichen Fehlern (Dienst gestoppt, Socket-Rechte geändert, Konfigurationsschlüssel gelöscht) jeweils ✗ mit passender Abhilfe.
3. `update` auf eine neuere Version behält Benutzer, Server, Welten und Backups; laufende Minecraft-Server laufen weiter. Ein Update mit defektem Binary rollt selbst zurück.
4. Die Firewall lässt nur SSH, 80, 443 und 25565 zu; die laufende SSH-Sitzung bricht beim Aktivieren nicht ab.
5. `uninstall` entfernt Dienste, Container, Caddy-Site und Firewall-Tabellen und lässt Daten und Konfiguration stehen; eine erneute Installation übernimmt sie. `--purge` entfernt alles nach Bestätigung.
6. `make deploy-testvm` läuft über den Installer und die Test-VM funktioniert wie vorher.
