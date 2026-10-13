# mc-panel – Teilprojekt 4: Backups (Design-Spec)

- **Datum:** 2026-10-12
- **Status:** entworfen
- **Autor:** Jannes (mit Claude)
- **Baut auf:** `2026-10-08-mc-panel-foundation-design.md` (TP1), `2026-10-10-mc-panel-instances-design.md` (TP2), `2026-10-11-mc-panel-console-players-design.md` (TP3a), `2026-10-11-mc-panel-files-design.md` (TP3b), `2026-10-12-mc-panel-plugins-design.md` (TP3c)

## 1. Ziel und Kontext

Teilprojekt 4 bringt Backups der Server-Datenverzeichnisse: manuell und nach Zeitplan, mit gestufter Aufbewahrung, konsistent bei laufendem Server und mit vollständigem Restore. Die Backups liegen in diesem Teilprojekt **nur lokal** auf dem VPS (restic-Repos); ein Offsite-Ziel ist ausdrücklich zurückgestellt. Damit eine Welt trotzdem vom VPS herunterkommt, lässt sich jeder Snapshot als Zip herunterladen.

### 1.1 Erfolg

Ein Nutzer mit `server.backups` erstellt für einen Server ein Backup mit optionaler Notiz, stellt einen Zeitplan (alle N Stunden, täglich oder wöchentlich zu einer Uhrzeit) und eine Aufbewahrung (letzte / täglich / wöchentlich / monatlich) ein, pinnt wichtige geplante Snapshots gegen die Aufbewahrung, lädt einen Snapshot als Zip herunter und setzt den Server auf einen Snapshot zurück. Während das Backup eines laufenden Servers entsteht, speichert der Server nicht; der Snapshot ist dadurch konsistent. Fehlgeschlagene geplante Backups und Restores melden sich über die vorhandenen Benachrichtigungen (Discord, ntfy).

### 1.2 Leitlinie

Unverändert gilt TP1 §3: Ein kompromittiertes panel bekommt keinen Root. Dazu kommt aus TP3b §1.2: Daten, die ein Minecraft-Server kontrolliert, berührt **nie** ein root-Prozess. Backups gehen darüber hinaus: restic selbst liest und schreibt **nie** im Datenverzeichnis. Der Dateidienst `mcpanel-files` (Benutzer `mcpanel-mc`) erzeugt beim Backup einen tar-Stream über sein `os.Root` und entpackt beim Restore einen tar-Stream mit denselben Regeln wie beim Zip-Entpacken. Ein laufender, möglicherweise bösartiger Server kann so weder durch einen Symlink-Tausch während des Backups Daten eines anderen Servers in seinen Snapshot schmuggeln noch beim Restore außerhalb seines Verzeichnisses schreiben.

### 1.3 Nicht-Ziele (TP4)

- Offsite-Backends (S3, SFTP, …) und `restic copy`; späteres Teilprojekt. Dann wandert die restic-Ausführung in einen eigenen Dienst mit Netz, der Dateidienst bleibt ohne Netz.
- Wiederherstellen einzelner Pfade, Snapshot-Browser, Restore in einen neuen Server.
- Backup der panel-Datenbank.
- Cron-Ausdrücke, mehrere Zeitpläne pro Server, Zeitzone pro Server.
- Drosselung über `IOWeight`/`CPUWeight` der Unit hinaus; Quoten für die Repo-Größe.
- Hochladen eines Backups (Import); zum Einspielen einer Welt dient weiterhin das Zip-Entpacken aus TP3b.

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Werkzeug | restic ≥ 0.17 als externes Binary (CI/VM: 0.19.1, per SHA-256 gepinnt) | Deduplizierung über Snapshots hinweg (eine Welt × viele Tage bleibt klein), Verschlüsselung und Prüfsummen, spätere Offsite-Ziele ohne neues Format; restic ist nicht als Go-Bibliothek gedacht |
| Ausführender Prozess | Dateidienst `mcpanel-files` startet restic als Kindprozess | Kein neuer Dienst; gleicher Benutzer, gleiche Härtung, kein Netz; der Dienst hat bereits `os.Root`, Jobs, Entpack- und Zip-Logik |
| Datenweg | Backup = tar-Stream aus dem Dienst → `restic backup --stdin`; Restore/Download = `restic dump` → tar-Stream → Dienst | restic öffnet nie Pfade im Datenverzeichnis (§1.2); Restore schreibt nur über die geprüfte Entpack-Logik |
| Repos | Ein Repo pro Server: `<instances_dir>/backups/repos/<id>`, Passwort `backups/keys/<id>` | Isolierte Sperren (Prune von A blockiert B nicht), Löschen eines Servers = Repo löschen; Server-IDs werden nie wiederverwendet (Migration 00003) |
| Wahrheit | Die Snapshot-Liste kommt immer aus restic; Art, Pin und Notiz sind restic-Tags | Wie bei Plugins (TP3c): die Platte ist maßgeblich, die DB hält nur Zeitplan und letzten Lauf |
| Konsistenz | Laufender Server: `save-off`, `save-all flush`, Backup, danach immer `save-on` (RCON über den agent) | Standardverfahren ohne Stopp; schlägt RCON fehl, entsteht kein Snapshot |
| Restore | Nur ganzer Stand; Server stoppen, Sicherheits-Snapshot `pre-restore`, entpacken, oberste Ebene tauschen, Neustart über den Abgleich | Einfach zu prüfen; der Sicherheits-Snapshot macht jeden Restore umkehrbar |
| Zeitplan | Pro Server `off`/`interval`/`daily`/`weekly`, globale Zeitzone | Leicht zu bedienen und zu testen; Cron wäre fehleranfällig |
| Retention | `restic forget --keep-*` nur für `kind=scheduled`, mit `--keep-tag pinned` | Manuelle und Pre-Restore-Snapshots verschwinden nie automatisch |
| Orchestrierung | Panel-Paket `internal/backups`: Ticker, Warteschlange, Jobs, RCON-Schritte | Nur das panel kennt Zeitplan, Rechte, Ist-Zustand und RCON |

### 2.1 Verworfene Alternativen

- **Eigener Dienst `mcpanel-backup`:** saubere Trennung und offsite-bereit, aber die `os.Root`-tar-Logik müsste doppelt existieren oder das panel zwei Dienste per Stream koppeln; ein Binary, eine Unit, ein Socket und eine Konfiguration mehr für „nur lokal“.
- **restic in einem Container, gestartet vom agent:** der root-agent müsste Restore-Ziele und den Verzeichnistausch orchestrieren (widerspricht TP3b §1.2).
- **restic liest das Datenverzeichnis direkt (`restic backup <dir>`):** restic folgt Symlinks zwar nicht, aber zwischen Verzeichnis-Lesen und Öffnen kann ein laufender Server einen Ordner gegen einen Symlink tauschen; der Inhalt eines anderen Servers läge dann im Snapshot und wäre per Download lesbar. Restore direkt ins Datenverzeichnis hätte dasselbe Problem beim Schreiben.
- **Eigene tar.zst-Archive ohne restic:** keine externe Abhängigkeit, aber keine Deduplizierung (7 tägliche Stände einer 5-GiB-Welt = 35 GiB) und ein eigenes Format für spätere Offsite-Ziele.
- **Server für jedes Backup stoppen:** konsistent, aber Ausfall für Spieler bei jedem Lauf.

## 3. Architektur

```
Browser ─/api/servers/{id}/backups/*─▶ panel: internal/backups
                                        · Rechte (server.backups), CSRF, Audit, Sudo (Restore, Löschen)
                                        · Jobs pro Server, Warteschlange, Zeitplan-Ticker (clock.Clock)
                                        ├─▶ agent (root): RCON save-off/save-all flush/save-on, power stop, Ist-Zustand
                                        └─▶ mcpanel-files (mcpanel-mc): /v1/backups/*
                                              tar über os.Root ⇄ restic (Kindprozess, kein Netz)
                                              /srv/mc-panel/backups/repos/<id>
```

- **panel:** neues Paket `internal/backups` (Handler, Routen, Zeitplan, Warteschlange, Jobs, Audit, Benachrichtigung); Client-Methoden und Draht-Typen in `internal/files/filesapi` (gemeinsam mit dem Dienst).
- **Dienst:** neue Dateien in `internal/files` (restic-Ausführung hinter einem Interface, tar-Erzeugung, tar-Entpacken, Tausch, tar→Zip, Jobs der Arten `backup|restore|forget`).
- **agent:** einzige Änderung: `ensureLayout` legt `<instances_dir>/backups` an (§3.1). Keine neue agent-API; RCON und Power gibt es schon.

### 3.1 Dateisystem

```
/srv/mc-panel/                     root:root 0755   (instances_dir)
├── instances/<id>/                mcpanel-mc 0750  (unverändert)
├── backups/                       mcpanel-mc 0700  (neu, vom agent angelegt)
│   ├── repos/<id>/                restic-Repo des Servers
│   ├── keys/<id>                  Repo-Passwort (32 Zufallsbytes, base64), 0600
│   └── restore-journal/<id>       Restore-Journal (§4.3): nur während des Tauschs oder bei `stuck`
└── deleted/                       unverändert
```

- Der agent legt `backups/` in `ensureLayout` mit Besitzer `mcpanel-mc` und Modus 0700 an (wie `instances/<id>`) und korrigiert Besitzer/Modus, falls abweichend; den Inhalt berührt er nie. `repos/`, `keys/` und `restore-journal/` legt der Dienst selbst an.
- Kein Container sieht `backups/`; das panel (`mcpanel`) hat keinen Zugriff.
- Ein Repo wird beim ersten Backup eines Servers angelegt (`restic init`); vorher ist die Snapshot-Liste leer.
- **Schlüssel:** Für ein lokales Repo schützt das Passwort nicht vor dem Host selbst; es wird gebraucht, weil restic immer verschlüsselt, und es macht spätere Offsite-Kopien sicher. Ohne `keys/<id>` ist das Repo unlesbar: Die Betriebsdoku sagt, dass `backups/keys/` bei jeder Sicherung des Hosts mitgehört.
- Dateisystem: `backups/` liegt in Produktion auf demselben Dateisystem wie `instances/` (Vorgabe für den Installer, TP6); der Restore entpackt ins Datenverzeichnis und braucht dort Platz (§4.3).

### 3.2 Konfiguration und systemd

Neue Schlüssel in `files.toml` (mit Defaults; unbekannte Schlüssel weiterhin abgelehnt):

```toml
restic_binary = "/usr/bin/restic"   # Dev: "restic" (Suche im PATH)
[limits]
max_backup_bytes   = 68719476736    # 64 GiB je Snapshot (tar-Inhalt)
max_backup_entries = 1000000
max_backup_jobs    = 1              # gleichzeitige restic-Jobs im Dienst (backup, restore, forget)
```

- Beim Start ruft der Dienst `restic version` auf (Frist 10 s). Fehlt das Binary oder ist die Version < 0.17, sind die Backup-Endpunkte aus: `GET /v1/backups/status` meldet `{available: false, reason}`, alle anderen `/v1/backups/*` außer `purge` antworten `503 unavailable` (`purge` löscht nur Verzeichnisse und braucht restic nicht). Der übrige Dienst läuft normal (so auch auf dem Mac ohne restic).
- `mcpanel-files.service`: `ReadWritePaths` um `<instances_dir>/backups` ergänzt, `MemoryMax=1G` (statt 512M; restic hält Index und Pack-Puffer im Speicher), `TasksMax=512`, neu `IOWeight=50` und `CPUWeight=50`. `RestrictAddressFamilies=AF_UNIX` bleibt: restic braucht für lokale Repos kein Netz.
- `ensureLayout` im **Dev-Modus**: Der dev-Dateidienst legt `<instances_dir>/backups` selbst an, wenn es fehlt (wie `instances/<id>` in TP3b §3.1).

### 3.3 restic-Aufrufe

- Feste argv ohne Shell, über ein Interface (`resticRunner`), das die Tests durch einen Fake ersetzen. Umgebung ist **leer** bis auf `RESTIC_REPOSITORY=<backups>/repos/<id>`, `RESTIC_PASSWORD_FILE=<backups>/keys/<id>`, `HOME=<backups>`, `TZ=<backup_timezone>` (bei `backup` und `forget`: restic speichert die Snapshot-Zeit in der Zone des Backup-Prozesses und bildet daraus die Tages-/Wochen-/Monatsgruppen von `forget`, §5.4) und `RESTIC_PROGRESS_FPS=1`. Jeder Aufruf mit `--no-cache` (lokales Repo) und `--json` wo verfügbar.
- Vor jedem Job `restic unlock` (entfernt nur verwaiste Sperren eines abgestürzten Laufs). Lesen der Snapshot-Liste mit `--no-lock`, damit sie auch während eines Prune funktioniert.
- `backup --stdin --stdin-filename data.tar --host mcpanel --tag mcpanel --tag kind=<kind> [--tag note=<base64url>]`; das Ergebnis (`snapshot_id`, `total_bytes_processed`) kommt aus der JSON-Zusammenfassung.
- `snapshots --json --no-lock --host mcpanel`; nur Snapshots mit Tag `mcpanel` zählen.
- `dump <snapshot> /data.tar` liefert den tar auf stdout.
- `tag --set …` für Pin/Unpin; restic schreibt den Snapshot dabei neu, die **ID ändert sich**. Der Dienst liest die neue ID danach aus `snapshots` (gleiche Zeit, gleiche Art).
- `forget --prune --max-unused 10% --host mcpanel --group-by host …` (§5.4) bzw. `forget --prune <ids…>` für einzelnes Löschen.
- Fehlerausgaben von restic (stderr, auf 4 KiB gekürzt) gehen ins Log; in `job.error` landet nur eine feste Kurzmeldung plus restics letzte Zeile ohne Host-Pfade (wie TP3b §4).
- Snapshot-IDs aus Anfragen: genau 64 Zeichen `[0-9a-f]`; der Dienst prüft, dass die ID in der Liste dieses Repos vorkommt, bevor er sie an restic gibt (sonst `404 not_found`).

## 4. Dienst `mcpanel-files`

### 4.1 Neue Endpunkte

Wie bisher feste Pfade, `DisallowUnknownFields`, Fehler `{error, code}`.

| Methode, Pfad | Body / Query | Antwort |
|---|---|---|
| `GET /v1/backups/status` | – | `{available, version, reason}` |
| `POST /v1/backups/list` | `{id}` | `{snapshots: [{id, short_id, time, kind, pinned, note, bytes}]}`; neueste zuerst; ohne Repo `[]` |
| `POST /v1/backups/create` | `{id, kind, note, timezone}`; `kind` ∈ `manual\|scheduled\|pre-restore` | `202 {job}` |
| `POST /v1/backups/restore` | `{id, snapshot}` | Vorprüfung synchron (Snapshot vorhanden, Platz §4.3), dann `202 {job}` |
| `POST /v1/backups/forget` | `{id, snapshots}` **oder** `{id, policy: {keep_last, keep_daily, keep_weekly, keep_monthly}, timezone}` | `202 {job}` |
| `POST /v1/backups/pin` | `{id, snapshot, pinned}` | `{snapshot}` (mit neuer ID); nur `kind=scheduled`, sonst `400 invalid` |
| `GET /v1/backups/download` | Query `id`, `snapshot` | Zip-Stream (§4.4) |
| `POST /v1/backups/purge` | `{id}` | `204`; löscht `repos/<id>` und `keys/<id>` |
| `POST /v1/jobs/cancel` | `{id}` | `204`; bricht den laufenden Job der Instanz ab (§4.5) |

- `GET /v1/jobs` (TP3b) liefert alle Job-Arten. `filesapi.Job` bekommt `kind` (`extract|backup|restore|forget`), `snapshot` (Ergebnis bzw. Quelle), `skipped` (ausgelassene Einträge) und `code` (Wire-Code des Fehlers eines `failed`-Jobs, z. B. `too_large`, `insufficient_storage`); vorhandene Felder bleiben, Extract-Jobs setzen `kind=extract`.
- Ein Job pro Instanz über alle Arten (Entpacken, Backup, Restore, Forget schließen sich gegenseitig aus); `pin` und `purge` brauchen eine Instanz ohne Job (sonst `429 busy`). Insgesamt höchstens `max_backup_jobs` restic-Jobs und weiterhin zwei Entpack-Jobs.
- `create` braucht `instances/<id>` (sonst `404 not_found`); `restore` legt es nicht an.
- `note`: höchstens 64 Zeichen, gültiges UTF-8, keine Steuerzeichen; als Tag `note=<base64url ohne Padding>`. `pinned` = Tag `pinned`.
- Neue Fehlercodes des Dienstes: `unavailable` (503, restic fehlt). Sonst die vorhandenen: `not_found`, `busy`, `invalid`, `too_large`, `insufficient_storage`.

### 4.2 Backup-Job

- Durchlauf über `os.Root(instances/<id>)` in fester Reihenfolge (Namen sortiert), wie der Ordner-Zip-Download (TP3b §4): vor jedem Öffnen `Lstat`, danach `Stat` + `os.SameFile`, Öffnen mit `O_NONBLOCK`. Symlinks, Sonderdateien, verborgene Einträge (`.mcpanel-*` auf jeder Ebene, `.rcon-cli.*` oben) und Namen, die die Pfadregeln (`filesapi.CleanPath`) verletzen, werden ausgelassen und in `skipped` gezählt.
- tar im PAX-Format, nur Ordner und reguläre Dateien, Name relativ zur Wurzel, Modus `& 0777`, mtime; keine Besitzer (uid/gid 0, leere Namen).
- `server.properties` (oberste Ebene) wird geschwärzt wie beim Download (`rcon.password=********`); itzg schreibt das Passwort beim Start neu (TP3b §3.4).
- Eine Datei, die während des Lesens wächst oder schrumpft, wird mit der Größe aus dem `Stat` geschrieben (abgeschnitten bzw. mit Nullen aufgefüllt, `skipped` unverändert); bei gestopptem Server oder mit `save-off` kommt das bei Weltdaten nicht vor.
- Grenzen: mehr als `max_backup_entries` Einträge oder `max_backup_bytes` Inhalt → Job `failed` (`too_large`), restic wird abgebrochen, ein Snapshot entsteht nicht (restic legt ihn erst beim Ende des Streams an).
- Vor dem Start: freier Platz auf `backups/` ≥ `reserve_bytes` (TP3b), sonst `507`. Weiter kann der Dienst wegen der Deduplizierung nicht vorab rechnen; läuft die Platte trotzdem voll, scheitert restic und der Job ist `failed`.
- Fortschritt: `bytes`/`entries` (gelesener tar-Inhalt).

### 4.3 Restore-Job

1. Vorprüfung in der Anfrage: Snapshot gehört zum Repo; freier Platz auf `instances/<id>` ≥ `bytes` des Snapshots + `reserve_bytes` (sonst `507`).
2. Temp-Ordner `instances/<id>/.mcpanel-tmp-<token>-<zufall>/new` per `root.Mkdir`; `restic dump` → tar-Leser → Entpacken mit den Regeln aus TP3b §4.2: Pfadregeln, keine verborgenen Namen, nur Ordner und reguläre Dateien (andere tar-Typen → Job `failed`), keine doppelten Namen, Bytes und Einträge neu gezählt gegen `max_backup_bytes`/`max_backup_entries`, Dateien 0644 (0755, wenn im tar ein Ausführbit gesetzt ist), Ordner 0755, umask 0027 des Dienstes wirkt zusätzlich, mtime aus dem tar. Fehler → Temp-Ordner löschen, Datenverzeichnis unverändert.
3. Tausch der obersten Ebene (nur `rename` über das `os.Root`, kein Kopieren): alle sichtbaren Einträge des Datenverzeichnisses nach `<tmp>/old/`, dann alle Einträge aus `<tmp>/new/` nach oben, dann `<tmp>` rekursiv löschen. Verborgene Einträge (`.rcon-cli.*`, `.mcpanel-*`) bleiben an Ort und Stelle.
4. Abbrechen (§4.5) wirkt nur bis zum Beginn des Tauschs; der Tausch selbst läuft ohne Abbruchpunkt durch. Nur während der Umbenennungen führt der Dienst ein Restore-Journal in `backups/restore-journal/<id>` (außerhalb des Datenverzeichnisses, das der Container kontrolliert; Inhalt: `<tmp>` und die Phase `old_moving`, `new_moving` oder `stuck`, jeweils atomar geschrieben). Stirbt der Dienst mitten im Tausch, schließt er die Umbenennungen beim nächsten Start synchron ab, bevor er Anfragen annimmt: bei `old_moving` zurück (der vorige Stand kommt aus `<tmp>/old` wieder nach oben), bei `new_moving` vorwärts (der Rest aus `<tmp>/new`; der Snapshot war vollständig geprüft). Danach verschwinden `<tmp>` (Temp-Bereinigung, TP3b §3.3) und der Journal-Eintrag. Ein Temp-Ordner ohne Journal-Eintrag ist ein gewöhnlicher Rest und wird nur gelöscht, nie zurückgerollt.
   - **Phase `stuck`:** Scheitert der Tausch und auch der Rücktausch (oder die Wiederherstellung beim Start), bleibt `<tmp>` mit dem vorigen Stand in `old/` liegen, die Temp-Bereinigung lässt ihn in Ruhe, und der Eintrag bleibt mit Phase `stuck`. Neue Restores dieses Servers lehnt der Dienst ab, bis ein Betreiber den Fall gelöst hat (Runbook in `docs/dev/README.md`: Log lesen, `<tmp>` prüfen, Inhalte von Hand zurückverschieben, dann den Eintrag löschen). Server löschen mit `purge_backups` entfernt den Eintrag ebenfalls.
   - Der Pre-Restore-Snapshot des panels (§5.5) bleibt die Rückfallebene.

### 4.4 Download

- `restic dump` → tar-Leser → Zip-Stream (`application/zip`, Dateiname `<server>-<zeit>.zip` setzt das panel), mit denselben Ausschlüssen für Namen, die sich nicht sicher in einer Zip speichern lassen, wie der Ordner-Zip-Download (TP3b §4). Der tar enthält bereits nur Ordner und reguläre Dateien und ein geschwärztes `rcon.password`.
- Zählt zu den 4 gleichzeitigen Ordner-Zips (TP3b §4.1); läuft für die Instanz ein `forget`-Job, → `429 busy` (Prune hält eine exklusive Sperre). Während eines Backups ist ein Download möglich.
- Fristen wie beim Ordner-Zip: 60 s pro Lese-/Schreibvorgang, keine Gesamtfrist.
- Namensgrenze: Der Zip-Stream übernimmt die Grenze des Ordner-Zips von 16 MiB für alle Pfadnamen zusammen (TP3b). Backup und Restore erlauben 64 MiB; ein Snapshot mit mehr als 16 MiB sichtbarer Pfadnamen lässt sich daher wiederherstellen, aber nicht als Zip herunterladen (der Download bricht ab).

### 4.5 Abbrechen und Beenden

- `POST /v1/jobs/cancel` bricht `backup`- und `forget`-Jobs sofort ab (restic bekommt `SIGINT`, nach 10 s `SIGKILL`; ein abgebrochenes Backup hinterlässt keinen Snapshot, ein abgebrochenes Prune nur ungenutzte Packs, die der nächste Prune entfernt) und `restore`-Jobs bis zum Beginn des Tauschs. Für Entpack-Jobs ändert sich nichts (`400 invalid`).
- Beim Beenden bricht der Dienst laufende Jobs ab wie bisher (`svc.Close`), restic-Kinder eingeschlossen; ein laufender Tausch wird noch beendet.

## 5. panel

### 5.1 Datenmodell (Migration 00006)

```sql
CREATE TABLE backup_schedules (
    server_id        INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    mode             TEXT    NOT NULL DEFAULT 'off' CHECK (mode IN ('off','interval','daily','weekly')),
    interval_hours   INTEGER NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 168),
    time_of_day      INTEGER NOT NULL DEFAULT 240 CHECK (time_of_day BETWEEN 0 AND 1439), -- Minuten
    weekday          INTEGER NOT NULL DEFAULT 0 CHECK (weekday BETWEEN 0 AND 6),          -- 0 = Sonntag
    keep_last        INTEGER NOT NULL DEFAULT 24,
    keep_daily       INTEGER NOT NULL DEFAULT 7,
    keep_weekly      INTEGER NOT NULL DEFAULT 4,
    keep_monthly     INTEGER NOT NULL DEFAULT 6,
    next_run_at      INTEGER,          -- NULL bei mode = 'off'
    last_run_at      INTEGER,
    last_status      TEXT CHECK (last_status IN ('ok','failed','running')),
    last_error       TEXT,
    last_snapshot_id TEXT,
    saveoff_since    INTEGER,          -- gesetzt, solange das panel save-off gesendet hat
    updated_at       INTEGER NOT NULL
);
ALTER TABLE instance_settings ADD COLUMN backup_timezone TEXT NOT NULL DEFAULT 'Europe/Berlin';
```

- Eine Zeile entsteht beim ersten Lesen oder Schreiben (Defaults wie oben); `keep_*` 0–1000, bei `mode ≠ off` mindestens ein Wert > 0. `last_*` beschreibt den letzten **geplanten** Lauf.
- Queries in `internal/store/queries/backups.sql`, Präfix `Backups…`.
- `backup_timezone` ist ein IANA-Name, geprüft mit `time.LoadLocation`; das panel bettet `time/tzdata` ein (statisches Binary, Hosts ohne Zoneinfo).

### 5.2 Zeitplan

- Ticker jede Minute über `clock.Clock`. Fällig ist jede Zeile mit `mode ≠ off` und `next_run_at ≤ jetzt`; sie kommt in die Warteschlange (§5.3), außer der Server hat schon einen wartenden oder laufenden Job (dann beim nächsten Tick wieder prüfen).
- `next_run_at` wird beim Speichern des Zeitplans, beim Ändern der Zeitzone und nach jedem geplanten Lauf (Erfolg oder Fehler) neu berechnet:
  - `interval`: Start des letzten Laufs + N Stunden; ohne bisherigen Lauf jetzt + N Stunden.
  - `daily`: nächster Zeitpunkt `time_of_day` nach jetzt in der Zeitzone.
  - `weekly`: nächster `weekday` + `time_of_day` nach jetzt in der Zeitzone.
  - Sommerzeit: fällt die Uhrzeit in eine Lücke, gilt die Normalisierung von `time.Date` (02:30 → 03:30); doppelte Stunden zählen einmal (frühester Zeitpunkt).
- Verpasste Termine (panel war aus): beim Start ist `next_run_at` in der Vergangenheit, der Lauf startet einmal, danach wird ab jetzt neu gerechnet. Es gibt kein Nachholen mehrerer Läufe.
- Server, deren Instanz fehlt oder deren Datenverzeichnis noch nicht existiert (nie gestartet), laufen als `failed` mit verständlichem Fehler, ohne Benachrichtigung.

### 5.3 Warteschlange und Jobs

- Ein Job pro Server im Speicher: `{id, kind: backup|restore|delete, trigger: manual|scheduled, state: queued|running|done|failed, step, bytes, snapshot, error, queued_at, started_at, finished_at}`; das letzte Ergebnis bleibt bis zum nächsten Job oder Neustart des panels.
- `step` ∈ `queued`, `saving` (save-off/flush), `backing_up`, `resuming` (save-on), `pruning`, `stopping`, `pre_restore`, `restoring`, `deleting`.
- **Panel-weit läuft höchstens ein Job gleichzeitig** (FIFO-Warteschlange über alle Server); manuelle und geplante Jobs teilen sie. Ein zweiter Job für denselben Server → `409 backup_running`.
- Das panel fragt den Dienst-Job alle 2 s ab (`GET /v1/jobs`) und spiegelt `bytes` in den eigenen Job.
- Beim Beenden bricht das panel den laufenden Dienst-Job ab (`/v1/jobs/cancel`), sendet `save-on`, falls nötig, und wartet höchstens 20 s.

### 5.4 Backup-Ablauf

1. Ist-Zustand beim agent abfragen. Läuft der Container: `saveoff_since` setzen, RCON `save-off`, dann `save-all flush` (Frist je 60 s). Schlägt eines fehl → sofort `save-on` versuchen, Job `failed` mit `rcon_unavailable`. Gestoppt: kein RCON.
2. Dienst-Job `create` (Art `manual` bzw. `scheduled`) bis `done`/`failed`.
3. **Immer** (auch nach Fehler, Abbruch oder Zeitüberschreitung in Schritt 2): RCON `save-on`, dann `saveoff_since` löschen. Schlägt `save-on` fehl (Server inzwischen gestoppt), ist das unschädlich: ein Neustart setzt das Speichern ohnehin wieder ein.
4. Nur bei geplanten Läufen nach Erfolg: Dienst-Job `forget` mit der Policy des Servers und `backup_timezone`. restic bekommt `--tag kind=scheduled --keep-tag pinned --keep-last … --keep-daily … --keep-weekly … --keep-monthly …` (Werte 0 weggelassen). Scheitert nur dieser Schritt, gilt der Lauf als `ok` mit Hinweis in `last_error`.
5. `last_*` und `next_run_at` schreiben (nur geplant).

**Absturz des panels:** Beim Start sucht das panel Zeilen mit `saveoff_since ≠ NULL`; für jede bricht es einen laufenden Dienst-Job der Instanz ab, sendet `save-on` (wenn der Container läuft) und löscht die Markierung. Zeilen mit `last_status = running` werden `failed` („panel neu gestartet“).

### 5.5 Restore-Ablauf

1. **Sperre setzen.** Das bisherige `servers.ExtractGate` wird zu `servers.DataGate` (`DataJobRunning(ctx, id)`): wahr, wenn beim Dienst ein `extract`- oder `restore`-Job läuft (wie bisher über `GET /v1/jobs`) **oder** das panel für den Server einen Restore-Job hält. Solange blockieren Start/Neustart (`409 conflict`) und der Abgleich startet den Server nicht. Stop bleibt erlaubt.
2. Läuft der Container: Stop über den agent (gleicher Weg wie die Power-Aktion, `desired_state` bleibt unverändert), warten bis gestoppt (Frist 120 s, sonst `failed`).
3. Sicherheits-Snapshot: Dienst-Job `create` mit `kind=pre-restore` (gestoppt, also ohne RCON). Scheitert er, bricht der Restore ab; das Datenverzeichnis bleibt unverändert.
4. Dienst-Job `restore` bis `done`/`failed`.
5. Sperre lösen. Ist `desired_state = running`, startet der Abgleich den Server beim nächsten Durchlauf; das panel stößt ihn sofort an.
- Ein Absturz des panels in Schritt 2–3 lässt das Datenverzeichnis unverändert; nach dem Neustart fehlt die panel-Sperre und der Abgleich startet den Server wieder (Restore fand nicht statt). In Schritt 4 läuft der Dienst-Job weiter; die Sperre greift über den Dienst-Job, bis er endet.

### 5.6 Löschen

- Einzelner Snapshot: Job `delete` → Dienst-Job `forget` mit genau dieser ID; auch angepinnte und Pre-Restore-Snapshots.
- Server löschen (`DELETE /servers/{id}`, vorhanden, Admin + Sudo) bekommt den optionalen Query-Parameter `purge_backups=1`: nach dem Entfernen beim agent ruft das panel `POST /v1/backups/purge` auf (wartende Jobs des Servers werden vorher verworfen, ein laufender abgebrochen). Ohne den Parameter bleibt das Repo verwaist liegen (wie `deleted/`); der Zeitplan verschwindet mit dem Server. Das Purge läuft unabhängig vom Abbruch der Anfrage (eigene Frist, verlängerte Schreibfrist der Antwort). Scheitert es, ist der Server trotzdem gelöscht; die Antwort bleibt `204`, das Audit-Detail vermerkt `backups_purged: false` (nach Erfolg `true`).

### 5.7 API

Alle Routen mit `rbac.Server(server.backups, "id")`, schreibende mit CSRF; Restore und Löschen zusätzlich `.WithSudo()`.

| Methode, Pfad | Body | Antwort |
|---|---|---|
| `GET /servers/{id}/backups` | – | `{available, snapshots, job, schedule}`; `schedule` inkl. `next_run_at`, `last_*`, `timezone` |
| `POST /servers/{id}/backups` | `{note?}` | `202 {job}` (manuelles Backup) |
| `GET /servers/{id}/backups/job` | – | `{job}` (`null`, wenn noch keiner lief) |
| `PUT /servers/{id}/backups/schedule` | `{mode, interval_hours, time_of_day, weekday, keep_last, keep_daily, keep_weekly, keep_monthly}` | `{schedule}` |
| `POST /servers/{id}/backups/{snapshot}/pin` | `{pinned}` | `{snapshot}` (neue ID) |
| `POST /servers/{id}/backups/{snapshot}/restore` | – | `202 {job}` |
| `DELETE /servers/{id}/backups/{snapshot}` | – | `202 {job}` |
| `GET /servers/{id}/backups/{snapshot}/download` | – | Zip-Stream, `Content-Disposition: attachment; filename="<name>-<YYYYMMDD-HHMMSS>.zip"` |

- `backup_timezone` kommt in die vorhandenen Einstellungen (`GET`/`PUT /servers/settings`, Admin, Sudo beim Schreiben); keine neue Route.
- `available = false` (restic fehlt, Dienst nicht erreichbar): `snapshots: []`, Aktionen antworten `503 backups_unavailable` (restic fehlt) bzw. `503 files_unavailable` (Dienst weg).
- Download: wie der Datei-Download (TP3b) ohne Puffern, mit Fristen pro Operation statt der Server-Timeouts; höchstens 2 gleichzeitige Backup-Downloads pro Benutzer (`429 rate_limited`).
- Snapshot-IDs im Pfad: 64 Hex-Zeichen, sonst `400 validation_failed`; unbekannt → `404 not_found`.

### 5.8 Fehlercodes

Neu in `internal/httpx/errors.go` und im OpenAPI-Enum: `backups_unavailable` (503), `backup_running` (409). Weiter genutzt: `files_unavailable`, `agent_unavailable`, `rcon_unavailable` (nur im Job-Fehler, nicht als HTTP-Antwort, weil Backups asynchron laufen), `insufficient_storage` (507, Restore-Vorprüfung), `not_found`, `conflict` (Server ohne Instanz oder Datenverzeichnis), `validation_failed`, `rate_limited`.

### 5.9 Audit und Benachrichtigung

- Audit (Nutzeraktionen): `backup.create` (Notiz), `backup.restore` (Snapshot, Zeit), `backup.delete`, `backup.pin`, `backup.unpin`, `backup.download`, `backup.schedule_update` (alter/neuer Zeitplan), Änderung von `backup_timezone` im vorhandenen Einstellungs-Eintrag. Geplante Läufe sind keine Nutzeraktion und werden nicht auditiert; ihr Ergebnis steht in `last_*`.
- Neues `notify.EventType` `backup.failed`: für fehlgeschlagene geplante Backups und für jeden fehlgeschlagenen Restore (auch manuell ausgelöst). Titel nennt den Server, Nachricht den Fehler ohne Host-Pfade. Das panel meldet pro Server und Art höchstens einmal in 6 h (eigene Sperre mit `clock.Clock`); ein Erfolg setzt sie zurück. Ein fehlgeschlagener `forget`-Schritt allein meldet nichts.

### 5.10 Rechte

`server.backups` bedeutet: alle Dateien des Servers lesen (über den Download, inkl. Plugin-Konfigurationen mit Geheimnissen) und den Stand zurücksetzen. Die Benutzerverwaltung zeigt beim Vergeben einen Hinweis wie bei `server.files`. Wie bisher setzt jedes Server-Recht `server.view` voraus.

## 6. Frontend

### 6.1 Tab „Backups“

- Sichtbar mit `server.backups`. Kopf: Button „Backup erstellen“ (Dialog mit optionaler Notiz), darunter der aktuelle Job (Schritt, Bytes, Warteschlangen-Position als „wartet“) mit Polling alle 2 s, solange er `queued`/`running` ist.
- Snapshot-Liste: Zeit (Browser-Zeitzone, relativ + absolut), Art als Badge (geplant / manuell / vor Restore), Notiz, Größe, Pin-Symbol. Aktionen: Anpinnen/Lösen (nur geplante), Herunterladen, Wiederherstellen, Löschen.
- Wiederherstellen: Bestätigungsdialog, der Stopp, Sicherheits-Snapshot und automatischen Neustart erklärt und das Abtippen des Servernamens verlangt; danach greift der globale Sudo-Dialog. Löschen: einfache Bestätigung + Sudo.
- Karte „Zeitplan“: Modus, je nach Modus Intervall / Uhrzeit / Wochentag, Retention-Felder mit Kurzerklärung, Hinweis auf die Zeitzone des panels, nächster Lauf, letzter Status mit Fehlertext.
- `available = false`: nur ein Hinweis („Backups sind auf diesem Host nicht verfügbar: restic fehlt“), keine Aktionen.

### 6.2 Weitere Stellen

- Einstellungen (Admin): Feld „Zeitzone für Backup-Zeitpläne“ (Auswahl aus `Intl.supportedValuesOf('timeZone')`).
- Server löschen: Checkbox „Backups ebenfalls löschen“ (Standard aus) mit Hinweis, dass sie sonst auf dem Host bleiben.
- Benutzerverwaltung: Hinweis zu `server.backups` (§5.10).
- i18n `de`/`en`; MSW-Mocks für alle neuen Operationen, mit simuliertem Job-Ablauf (queued → running → done) und `available`-Schalter.

## 7. Entwicklung, Betrieb und CI

- **Mac (`make dev`):** Backups funktionieren nur mit `MCPANEL_DEV_DOCKER=1` und einem restic im `PATH`, das der Nutzer selbst installiert hat (`brew install restic`; Claude installiert nichts). Ohne restic zeigt der Tab den Hinweis aus §6.1. Ohne erreichbares Docker scheitern Backup und Restore mit „Docker is not reachable“ (Job-Code `agent_unavailable`): Das panel kennt den Zustand des Containers nicht, und ein hängender dockerd darf nicht als gestoppter Server gelten. Liste, Zeitplan, Anpinnen, Download und Löschen von Snapshots laufen nur über den Dateidienst und gehen auch ohne Docker. Dev-Daten unter `.dev/mc/backups`.
- **Test-VM und CI:** `scripts/vm-test.sh` und die CI-Jobs installieren restic 0.19.1 aus dem GitHub-Release (`restic_0.19.1_linux_<arch>.bz2`) mit fest eingetragener SHA-256 nach `/usr/local/bin/restic` und setzen `MCPANEL_TEST_RESTIC` für die Go-Integrationstests.
- **Produktion:** Bis zum Installer (TP6) installiert der Betreiber restic (Ubuntu-Paket `restic` oder das gepinnte Release) und ergänzt die Unit gemäß §3.2; `docs/dev/README.md` beschreibt das, inklusive Mitsichern von `backups/keys/`.
- **CI-Abnahme** (`scripts/ci-instances.sh`, Produktionsmodus, echter Paper-Server): Datei in der Welt anlegen, manuelles Backup bei laufendem Server (Log zeigt `save-off`/`save-on`), Datei ändern, Restore (Server stoppt, Pre-Restore-Snapshot existiert, Datei hat alten Inhalt, Server läuft wieder), Download als Zip enthält die Datei und kein `rcon.password`, Pin ändert die ID, Löschen entfernt den Snapshot, Server löschen mit `purge_backups=1` entfernt das Repo.
- Manuelle Abnahme-Checkliste: `docs/dev/acceptance-tp4.md`. CLAUDE.md bekommt einen Absatz zu Backups unter „Minecraft instances“ und den neuen Spec-Link.

## 8. Tests

- **Dienst (Go, Standardbibliothek):**
  - tar-Erzeugung: Auslassen von Symlinks, Sonderdateien, verborgenen Einträgen und ungültigen Namen; Symlink-Tausch eines Ordners während des Durchlaufs (Hook wie bei den Entpack-Tests) liest nichts außerhalb; Schwärzung; Grenzen.
  - tar-Entpacken: alle Regeln aus TP3b §4.2 auf tar übertragen (Pfad-Tricks, Symlink-/Hardlink-/Gerät-Einträge, doppelte Namen, falsche Größen, Grenzen); Tausch der obersten Ebene inklusive verborgener Einträge; Abbruch vor dem Tausch lässt alles unverändert.
  - tar→Zip: Namensregeln, Inhalt.
  - Jobs: ein Job pro Instanz über alle Arten, `max_backup_jobs`, Abbrechen, Beenden mit laufendem restic.
  - restic-Aufrufe gegen einen Fake-`resticRunner` (argv, Umgebung, Tags, JSON-Auswertung, Fehlertexte ohne Pfade); Integrationstests mit echtem restic (init, backup, list, pin, forget mit Policy und Zeitzone, dump, restore, purge), wenn `MCPANEL_TEST_RESTIC` gesetzt ist, sonst `t.Skip`.
- **panel:**
  - Zeitplan-Berechnung mit Fake-Clock: alle Modi, Sommerzeit-Lücke und -Doppelung, verpasste Termine, Zeitzonenwechsel.
  - Warteschlange und Jobs gegen Fakes von agent und Dienst: Reihenfolge, ein Job panel-weit, `409 backup_running`, `save-on` nach jedem Fehler und nach Abbruch, Wiederanlauf nach Absturz (`saveoff_since`), `forget` nur nach geplanten Erfolgen.
  - Restore: Sperre gegen Power-Aktionen und Abgleich (auch über einen laufenden Dienst-Job), Stopp, Pre-Restore-Snapshot, Fehler in jedem Schritt.
  - Routen/OpenAPI-Abgleich, Rechte (inkl. Sudo), Audit-Einträge, `backup.failed`-Benachrichtigung mit Drosselung, `purge_backups` beim Löschen.
- **Frontend:** Vitest für Zeitplan-Formular (Validierung, Moduswechsel) und Job-Anzeige; Playwright gegen den Mock: Backup erstellen, Pin, Restore-Dialog mit Namensbestätigung und Sudo, Zeitplan speichern, Hinweis bei `available=false`.

## 9. Abnahmekriterien

1. Ein manuelles Backup eines laufenden Paper-Servers entsteht mit `save-off`/`save-all flush`/`save-on` im Log und erscheint in der Liste als „manuell“ mit Notiz und Größe.
2. Ein Zeitplan (z. B. stündlich) erzeugt Snapshots zur erwarteten Zeit in der eingestellten Zeitzone; `forget` hält die Retention ein, manuelle, Pre-Restore- und angepinnte Snapshots bleiben.
3. Restore stoppt den Server, legt einen Pre-Restore-Snapshot an, stellt den Stand vollständig wieder her (verborgene Dateien unberührt) und startet den Server wieder, wenn er laufen soll; währenddessen sind Start/Neustart gesperrt.
4. Der Download liefert ein Zip des Snapshots ohne `rcon.password`.
5. Symlinks im Datenverzeichnis landen nie im Snapshot, und kein Restore schreibt außerhalb von `instances/<id>`.
6. Ein fehlgeschlagenes geplantes Backup erscheint mit Fehler in der UI und als Benachrichtigung (höchstens einmal in 6 h).
7. Ohne restic auf dem Host läuft alles andere normal; der Tab zeigt den Hinweis.
8. Ein Absturz des panels während eines Backups hinterlässt keinen Server mit dauerhaftem `save-off`.
9. Löschen eines Servers mit „Backups ebenfalls löschen“ entfernt Repo und Schlüssel; ohne die Option bleiben beide.
