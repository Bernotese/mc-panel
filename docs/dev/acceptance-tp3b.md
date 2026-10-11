# Abnahme Teilprojekt 3b (manuell, Test-VM)

Spec: `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§9).

Automatisch geprüft (CI): Unit- und Linux-Tests des Dienstes (Pfadregeln, `os.Root`-Grenzen, Symlinks, FIFO, atomares Schreiben, Upload-Grenzen, Schwärzen, Ordner-Zip, Entpacken mit Zip-Slip/Zip-Bombe/Symlink-Einträgen), panel-Tests (Rechte, CSRF, Streaming ohne Puffern, Audit, Entpacken nur gestoppt, Power-Sperre), Instanz-Abnahme (`scripts/ci-instances.sh`: Dienst als `mcpanel-mc` ohne Capabilities, nur die panel-UID und root dürfen verbinden, Upload, Schwärzen, Editor-Sperre, Symlinks, Ordner-Zip, Welt entpacken, bösartige Archive), Playwright gegen den Mock (Dateien-Tab).

Manuell auf der Test-VM: alles mit Browser, Minecraft-Client und echter Session.

## Vorbereitung

1. Deploy von `main`: `make deploy-testvm` (alle TP3b-PRs gemerged). Panel: <https://panel.192-168-1-117.sslip.io/>.
2. Dienst prüfen: `ssh claude@192.168.1.117 'systemctl is-active mcpanel-files.socket mcpanel-files.service; ps -o user:20=,group:20=,supgrp:30= -C files; sudo stat -c "%U:%G %a" /run/mc-panel-files/files.sock'` → zweimal `active`, Benutzer `mcpanel-mc`, Gruppe `mcpanel-mc`, Zusatzgruppen höchstens `mcpanel-mc` (nicht `mcpanel`); Socket `mcpanel-mc:mcpanel 660`. Speichergrenze: `ssh claude@192.168.1.117 systemctl show mcpanel-files -p MemoryMax -p TasksMax` → `MemoryMax=536870912`, `TasksMax=256`. Logs nebenher: `ssh claude@192.168.1.117 journalctl -u mcpanel-agent -u mcpanel-files -u mcpanel-panel -f`.
3. Als `admin` anmelden. Server `survival` (Paper) existiert und wurde mindestens einmal gestartet (sonst gibt es noch kein Datenverzeichnis).
4. Benutzer `freund` hat nur `server.view` auf `survival` (aus TP3a); Benutzer `helfer` bekommt zusätzlich `server.files` (Benutzer → Rechte; beim Anhaken erscheint der Hinweis „Erlaubt Code-Ausführung im Minecraft-Server (z. B. über Plugins)“).
5. Auf dem Mac vorbereiten: eine Welt als Zip (z. B. eine Einzelspieler-Welt aus `~/Library/Application Support/minecraft/saves/<welt>` mit Finder → „Komprimieren“; die Zip enthält den Ordner `<welt>/`), ein kleines Paper-Plugin als `.jar` (z. B. von <https://hangar.papermc.io>, etwas Harmloses wie „Chunky“) und eine Datei größer als 5 MiB, die kein Text ist.
6. Minecraft Java Edition im selben Netz, Adresse `survival.mc.192-168-1-117.sslip.io`.

## Kriterien

| # | Kriterium (Spec §9) | Vorgehen | Erwartet |
|---|---|---|---|
| 2a | Welt hochladen | als `helfer` Server `survival` → Tab „Dateien“ → „Hochladen“ → Welt-Zip; Server läuft dabei | Fortschritt in der Upload-Warteschlange, danach steht die Zip in der Liste; Hinweis-Leiste „Änderungen wirken meist erst nach einem Neustart“ |
| 2b | Entpacken gesperrt bei laufendem Server | Menü der Zip → „Entpacken …“ | Aktion gesperrt mit Hinweis „nur bei gestopptem Server“ |
| 2c | Entpacken | als `admin` Server stoppen; als `helfer` „Entpacken …“ → Ziel `neue-welt` | Fortschrittsbalken (Bytes, Einträge), Power-Buttons der Detailseite während des Jobs gesperrt; danach Ordner `neue-welt/<welt>/` mit `level.dat`; ein Start während des Jobs (zweites Fenster, `admin`) wird mit „Entpacken läuft“ abgelehnt |
| 2d | Server startet mit der Welt | Ordner `world` umbenennen in `world-alt`, `neue-welt/<welt>` verschieben/umbenennen nach `world` (Dialog „Umbenennen/Verschieben“), Server starten, mit dem Client verbinden | Spieler steht in der hochgeladenen Welt |
| 3 | Plugin | Plugin-Jar nach `plugins/` hochladen; Server neu starten; in der Konsole `plugins` | Plugin erscheint in der Liste (grün) |
| 4a | Config bearbeiten | `plugins/<Plugin>/config.yml` (oder `bukkit.yml`) öffnen, Wert ändern, Strg/Cmd+S | Speichern bestätigt; nach erneutem Öffnen ist die Änderung da |
| 4b | Konflikt | dieselbe Datei in zwei Tabs öffnen; in Tab A ändern und speichern; in Tab B ändern und speichern | Tab B zeigt den Dialog „Datei wurde geändert“ mit „Neu laden“ und „Überschreiben“; beide Wege funktionieren |
| 4c | `server.properties` | `server.properties` öffnen | Hinweis „wird beim Start aus den panel-Einstellungen überschrieben“; `rcon.password=********`; die Dateien `.rcon-cli.env`/`.rcon-cli.yaml` sind in der Liste nicht zu sehen; nach Speichern und Neustart funktioniert die Konsole (RCON) weiter |
| 4d | Kein Text | die >5-MiB-Binärdatei hochladen und öffnen | statt Editor ein Download-Knopf |
| 5 | Ordner herunterladen | Ordner `world` → „Herunterladen“ | Browser lädt `world.zip`; die Zip lässt sich öffnen und enthält die Welt |
| 6a | nur `server.view` | als `freund` Server `survival` öffnen | kein Tab „Dateien“ |
| 6b | API-Sperre | als `freund` in den DevTools: `await fetch('/api/servers/<id>/files').then(r => r.status)` | `403` |
| 7 | Audit | als `admin` Audit-Log, Filter auf `file.`-Aktionen; danach `ssh claude@192.168.1.117 "sudo -u mcpanel panel audit verify -config /etc/mc-panel/panel.toml"` | Einträge für Upload (mit Größe), Entpacken (mit Ziel), Umbenennen (von/nach), Speichern, Löschen und Download (2a–5) mit Benutzer `helfer`; Auflisten/Öffnen erscheinen nicht; `verify` meldet OK |

Zusätzlich auf dem Handy (oder DevTools-Gerätemodus, 390 px): Dateiliste als Karten, Upload über die Dateiauswahl, Editor bedienbar, kein horizontales Scrollen der Seite.

Befunde als GitHub-Issue anlegen (`gh issue create`), mit Schritten zum Nachstellen.
