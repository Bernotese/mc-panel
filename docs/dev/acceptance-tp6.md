# Abnahme Teilprojekt 6 (manuell, echter VPS und Test-VM)

Spec: `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` (§10).

Automatisch geprüft (CI, Job „Installer lifecycle (root)“, `scripts/ci-installer.sh`): Installation mit `tls internal` und Firewall auf einem Runner, `doctor` ohne Fehler, zweiter Lauf ohne `[changed]`, Login über Caddy, Update mit Sicherung unter `/var/lib/mc-panel-rollback/`, Update mit defektem panel und automatischem Rollback (Exit-Code 3, gleiche DB), `uninstall` (Dienste, Units, Caddy-Site, nftables-Tabellen und mc-panel-Container weg, Daten und Konfiguration bleiben, fremder Container und fremde Tabelle unberührt), Neuinstallation über die Daten, `uninstall --purge` nur mit eingetippter Domain. Bash-Tests (`scripts/install/test.sh`, Job `installer-lint`): Argumente, Versionsvergleich, `sshd -T`, Vorlagen, TOML-Ergänzung, Prüfsummen, Update-Phasen, `doctor`, `uninstall`. Jeder `make deploy-testvm` läuft über den Installer.

Manuell: echter VPS mit öffentlicher Domain (Let's Encrypt), echte SSH-Sitzung, Firewall von außen, Update auf ein echtes Release, Test-VM.

## Vorbereitung

1. Frischer VPS mit Ubuntu 26.04 LTS (z. B. kleinster Hetzner-Typ), SSH-Schlüssel, Zugang als root oder Benutzer mit `sudo`.
2. DNS: `panel.<deine-domain>` (A und, falls der VPS IPv6 hat, AAAA) auf den VPS; Wildcard `*.mc.<deine-domain>` auf den VPS. Prüfen: `dig +short panel.<deine-domain>` zeigt die VPS-Adresse.
3. Zwei veröffentlichte Releases mit `install.sh` (Tags `v*`, nach dem Merge von TP6 vom Nutzer gesetzt): eine ältere für die Installation, eine neuere für das Update (z. B. ein Patch-Release).
4. Zweites Terminal mit offener SSH-Sitzung als Rückfallebene während der Firewall-Schritte; Zugang zur Web-Konsole des Anbieters griffbereit.
5. `install.sh` der älteren Version auf den VPS laden: `curl -fsSLO https://github.com/Bernotese/mc-panel/releases/download/<ältere-version>/install.sh` (für `doctor`, `update`, `uninstall` unten).

## Kriterien

| # | Kriterium (Spec §10) | Vorgehen | Erwartet |
|---|---|---|---|
| 1a | Einzeiler | `curl -fsSL https://github.com/Bernotese/mc-panel/releases/download/<ältere-version>/install.sh \| sudo bash -s -- install --domain panel.<deine-domain> --email <deine-mail>`; Admin-Name und Passwort (zweimal) bei der Frage eingeben | Lauf endet nach wenigen Minuten mit „mc-panel is installed: https://panel.<deine-domain>/“, dem Admin-Namen und „Next steps“; `[ok] https://panel.<deine-domain>/ answers`; `https://panel.<deine-domain>/` zeigt die Anmeldung mit gültigem Let's-Encrypt-Zertifikat (Browser ohne Warnung) |
| 1b | Anmelden | mit dem Admin anmelden, 2FA einrichten | Anmeldung und 2FA wie gewohnt |
| 1c | Idempotenz | denselben Einzeiler noch einmal | jeder Schritt meldet `[ok]` oder `[skip]`, keine Zeile `[changed]`; keine Admin-Frage (`[skip] admin exists: …`); Panel läuft durch |
| 2a | `doctor` frisch | `sudo bash install.sh doctor; echo $?` | nur `!` bei: `/srv/mc-panel is not its own file system`, ggf. `SSH allows password logins` bzw. `root logins`, ggf. `the system clock is not synchronized`; Exit-Code 1 bei Warnungen, 0 ohne; `sudo bash install.sh doctor --json` liefert dasselbe als JSON |
| 2b | `doctor` findet Fehler | nacheinander, jeweils `doctor` und danach reparieren: (1) `sudo systemctl stop mcpanel-files.socket mcpanel-files.service` (die Socket-Unit würde den Dienst sonst sofort wieder starten); (2) `sudo chmod 0666 /run/mc-panel-web/panel.sock`; (3) in `/etc/mc-panel/panel.toml` die Zeile `instances_dir` löschen. Reparatur: die Abhilfe aus der Ausgabe bzw. den Einzeiler erneut (startet die Dienste, ergänzt den Schlüssel mit Kommentar „added by install.sh …“) | jeweils `✗` mit passender Abhilfe (`not running: …`, `… is mcpanel:caddy 666 (expected mcpanel:caddy 660)`, `absent: /etc/mc-panel/panel.toml: instances_dir`), Exit-Code 2; nach der Reparatur wieder ohne `✗` |
| 3a | Update | im Panel die Basisdomain `mc.<deine-domain>` eintragen, einen Paper-Server anlegen und starten, einmal joinen, ein Backup erstellen; im Spiel verbunden bleiben; dann `sudo bash install.sh update --check` und `sudo bash install.sh update` | `--check` meldet „installed <alt>, available <neu>“; das Update meldet `handing over to install.sh v<neu>`, `backup of <alt> in /var/lib/mc-panel-rollback/<alt>-<zeit>` und `updated <alt> -> <neu>`; die Verbindung im Spiel bleibt bestehen; Benutzer, Server, Welt und Backup unverändert; `sudo ls -ld /var/lib/mc-panel-rollback /var/lib/mc-panel-rollback/*` zeigt `root root` mit `drwx------` und die Sicherung mit `state` (`phase=verified`) |
| 3b | Rollback | CI-Lauf „Installer lifecycle (root)“ als Beleg nehmen; zusätzlich (optional) auf der Test-VM einen Branch deployen, dessen panel absichtlich nicht startet (nur zum Test, nie auf `main`) | Exit-Code 3, `update to … failed and was rolled back to …`, die alte Version läuft wieder, DB unverändert |
| 4a | Firewall | in der zweiten SSH-Sitzung `sudo nft list table inet mcpanel_fw`; von außen (anderer Rechner) `nmap -Pn -p 22,80,443,25565,8080,3306 <vps>` und `nmap -Pn -sU -p 443 <vps>` | nur SSH-Port, 80, 443, 25565 (TCP) und 443 (UDP) offen, der Rest `filtered`; die SSH-Sitzung brach beim Installieren nicht ab |
| 4b | SSH auf anderem Port | auf einem zweiten VPS (oder vor der Installation) `Port 2222` in `/etc/ssh/sshd_config`, `sudo systemctl restart ssh` (bzw. `ssh.socket`), neu verbinden, dann installieren; zusätzlich `sudo bash install.sh install … --allow-port 8443` ausprobieren | Firewall erlaubt 2222 (aus `sshd -T`), die laufende Sitzung bleibt; `--allow-port 8443` erscheint als eigene Regel `tcp dport { 8443 } accept # --allow-port`; ein Lauf, bei dem `sshd -T` und die lauschenden Sockets nicht zusammenpassen, bricht vor jeder Änderung ab |
| 5a | Uninstall | `sudo bash install.sh uninstall` (Frage mit `y` beantworten) | Dienste, Units, Caddy-Site (`/etc/caddy/mc-panel.caddy` und die markierte Import-Zeile), Firewall-Tabellen `inet mcpanel_fw`/`inet mcpanel` und die Minecraft-Container (Welten vorher gespeichert) weg; `/srv/mc-panel`, `/var/lib/mc-panel`, `/var/lib/mc-panel-rollback`, `/etc/mc-panel*` und die Benutzer bleiben; andere Caddy-Sites (falls vorhanden) laufen weiter; Pakete bleiben installiert |
| 5b | Neuinstallation | Einzeiler erneut | keine Admin-Frage, Anmeldung mit dem alten Passwort und 2FA, Server und Welt wieder da, Server startet |
| 5c | Purge | `sudo bash install.sh uninstall --purge`, zuerst eine falsche Domain eintippen, dann erneut mit der richtigen (`panel.<deine-domain>`) | falsche Eingabe: „the confirmation did not match …; nothing was removed“, nichts gelöscht; danach alles weg inklusive `/srv/mc-panel`, `/var/lib/mc-panel`, `/var/lib/mc-panel-rollback`, `/etc/mc-panel*` und der Benutzer `mcpanel`/`mcpanel-mc`; Pakete bleiben installiert; `--yes` ersetzt die Eingabe nicht |
| 6 | Test-VM | `make deploy-testvm` zweimal hintereinander (der erste Lauf nach dem Merge migriert die alte Heredoc-Installation) | erster Lauf: „migrate the pre-installer deployment“, dann `install.sh install`; zweiter Lauf: `install.sh update` mit `--force`; Panel unter `https://panel.192-168-1-117.sslip.io/` mit dem bestehenden Admin, Server, Welten und Backups wie vorher; `sudo ~/mc-panel/build/testvm/install.sh doctor` ohne `✗` |

## Ergebnis

| # | Ergebnis | Notiz |
|---|---|---|
| 1a | | |
| 1b | | |
| 1c | | |
| 2a | | |
| 2b | | |
| 3a | | |
| 3b | | |
| 4a | | |
| 4b | | |
| 5a | | |
| 5b | | |
| 5c | | |
| 6 | | |
