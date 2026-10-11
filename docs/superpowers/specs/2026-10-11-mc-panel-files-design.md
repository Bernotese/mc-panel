# mc-panel – Teilprojekt 3b: Dateibrowser (Design-Spec)

- **Datum:** 2026-10-11
- **Status:** Entwurf
- **Autor:** Jannes (mit Claude)
- **Baut auf:** `2026-10-08-mc-panel-foundation-design.md` (TP1), `2026-10-10-mc-panel-instances-design.md` (TP2), `2026-10-11-mc-panel-console-players-design.md` (TP3a)

## 1. Ziel und Kontext

Teilprojekt 3 („Live-Betrieb“) ist dreigeteilt: 3a Konsole/Logs/Spieler (umgesetzt), **3b Dateibrowser** (dieses Dokument), 3c Plugins/Mods (Modrinth, Hangar). Jedes hat eigene Spec, eigenen Plan und eigene PRs.

### 1.1 Erfolg

Ein Nutzer mit `server.files` durchsucht das Datenverzeichnis eines Servers, bearbeitet Konfigurationsdateien im Browser, lädt Dateien hoch und herunter, legt an, benennt um, verschiebt und löscht. Bei gestopptem Server lädt er eine Welt als Zip hoch und entpackt sie; Ordner lädt er als Zip herunter.

### 1.2 Leitlinie

Ein kompromittiertes panel darf keinen Root auf dem Host bekommen (TP1 §3). Dateioperationen auf Daten, die ein (möglicherweise kompromittierter) Minecraft-Server kontrolliert, laufen deshalb **nie als root**: Ein eigener Dienst `mcpanel-files` arbeitet als Benutzer `mcpanel-mc` – derselbe Benutzer, dem die Daten ohnehin gehören. Der root-agent bleibt unverändert schmal und berührt keine Pfade, Uploads oder Archive aus Nutzerhand.

### 1.3 Nicht-Ziele (3b)

- Plugins/Mods mit Suche und Installation aus Modrinth/Hangar (3c).
- Fortsetzbare Uploads (ein Upload ist ein Stream; bei Abbruch beginnt er neu).
- Dateirechte oder Besitzer ändern (`chmod`/`chown`), Symlinks anlegen.
- Andere Archivformate als Zip (kein tar.gz, 7z, rar).
- Kopieren zwischen Servern, Papierkorb (Löschen ist endgültig, mit Bestätigung).
- Backups (TP4).

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Ausführender Prozess | Eigener Dienst `mcpanel-files` als `mcpanel-mc`, ohne Capabilities, systemd-gehärtet | Schreibrechte als root wären unnötig mächtig; eine Lücke im Dienst bringt höchstens die Rechte, die ein Minecraft-Server auf seine Daten ohnehin hat |
| Eingrenzung | Pro Anfrage `os.Root` auf genau `<instances_dir>/instances/<id>` | Alle Server teilen den Benutzer `mcpanel-mc`; ohne `os.Root` könnte ein Pfad- oder Symlink-Trick andere Server erreichen |
| Rechte | `server.files` = Lesen und Schreiben, volles Vertrauen; Hinweis beim Vergeben | Schreibzugriff erlaubt Code-Ausführung im Container (Plugin-Jar); eine Lese/Schreib-Trennung brächte keine echte Grenze |
| Laufender Server | Bearbeiten/Hochladen/Verwalten jederzeit; Entpacken nur gestoppt | Configs ändert man oft im laufenden Betrieb; ein Welt-Austausch im laufenden Betrieb zerstört Daten |
| Größen | Upload ≤ 4 GiB als ein Stream; Entpacken ≤ 20 GiB / 100 000 Einträge; 2 GiB Reserve | Reicht für übliche Welten, ohne Chunking-Protokoll |
| Editor | CodeMirror 6, lazy geladen | Leichter als Monaco, keine Worker, CSP-tauglich (kein CDN) |

### 2.1 Verworfene Alternativen

- **Hilfsprozess, vom root-agent pro Anfrage mit abgegebenen Rechten gestartet:** schützt die Dateiarbeit ebenso, aber der root-agent müsste Datei-Anfragen annehmen und Prozesse starten – mehr Angriffsfläche an der wichtigsten Stelle.
- **agent als root mit `os.Root`:** nur eine Schutzschicht; ein vergessenes `os.Open` oder ein Archiv-Pfad-Trick liefe mit root-Rechten.
- **Docker-Archive-API (`docker cp`):** braucht einen existierenden Container, schreibt als root mit UIDs aus dem tar, hatte Symlink-CVEs und bietet kein Umbenennen/Löschen.

## 3. Architektur

```
Browser ──/api/servers/{id}/files/*──▶ panel (mcpanel) ──HTTP/Unix-Socket──▶ mcpanel-files (mcpanel-mc, keine Caps)
                                         │ Rechte, CSRF, Audit,                 │ os.Root(<instances_dir>/instances/<id>)
                                         │ Streaming ohne Puffern               ▼
                                         └──▶ agent (root): Ist-Zustand    /srv/mc-panel/instances/<id>  (= /data im Container)
```

- **panel:** neues Paket `internal/serverfiles` (Handler, Routen, Audit), Client und Draht-Typen in `internal/files/filesapi` (gemeinsam mit dem Dienst, wie `agentapi`). Prüft Server, Recht und – fürs Entpacken – den Ist-Zustand beim agent.
- **Dienst:** neues Binary `cmd/files`, Paket `internal/files` (Server, Operationen, Entpacken, Zip-Download, Grenzen).
- **agent:** einzige Änderung: Rechte von `<instances_dir>/instances` (§3.3).

### 3.1 Prozess und Konfiguration

- Binary `files`, systemd-Unit `mcpanel-files.service`: `User=mcpanel-mc`, `Group=mcpanel-mc`, `NoNewPrivileges=yes`, `CapabilityBoundingSet=` (leer), `AmbientCapabilities=` (leer), `ProtectSystem=strict`, `ReadWritePaths=<instances_dir>/instances`, `ProtectHome=yes`, `PrivateTmp=yes`, `PrivateDevices=yes`, `RestrictAddressFamilies=AF_UNIX`, `SystemCallArchitectures=native`, `UMask=0027`, `MemoryMax=512M`, `TasksMax=256` (Begrenzung gegen präparierte Archive; der Dienst streamt, die Zip-Verzeichnisse einzelner Zips belegen höchstens rund 36 MiB).
- `files.toml` unter `/etc/mc-panel-files/` (Verzeichnis `root:mcpanel-mc 0750`, Datei 0640 – `mcpanel-mc` kann `/etc/mc-panel` nicht lesen und soll es nicht); unbekannte Schlüssel werden abgelehnt, wie beim agent:

```toml
socket        = "/run/mc-panel-files/files.sock" # nur ohne systemd-Socket-Aktivierung genutzt
socket_mode   = "0660"                           # dto.; Gruppe = primäre Gruppe des Dienstes
panel_uid     = 998                            # UID des Benutzers mcpanel; Pflicht in Produktion (zugelassen: diese UID und root)
instances_dir = "/srv/mc-panel"
dev           = false
[limits]
max_upload_bytes   = 4294967296    # 4 GiB
max_extract_bytes  = 21474836480   # 20 GiB
max_extract_entries = 100000
reserve_bytes      = 2147483648    # 2 GiB
max_text_bytes     = 5242880       # 5 MiB
max_list_entries   = 10000
```

- **Socket:** In Produktion per systemd-Socket-Aktivierung: `mcpanel-files.socket` mit `ListenStream=/run/mc-panel-files/files.sock`, `SocketUser=mcpanel-mc`, `SocketGroup=mcpanel`, `SocketMode=0660` – der Dienst muss dafür nicht Mitglied der Gruppe `mcpanel` sein und sieht `/etc/mc-panel` nicht. Der Dienst übernimmt den Socket über `LISTEN_FDS`/`LISTEN_PID`; ohne Aktivierung (Dev, CI) legt er `socket` selbst mit `socket_mode` an. Eigenes Verzeichnis `/run/mc-panel-files` (nicht `/run/mc-panel`), damit das Stoppen einer Unit nie den Socket der anderen löscht.
- Peer-UID-Prüfung über das vorhandene Paket `internal/agent/peercred` (Linux `SO_PEERCRED`, macOS `LOCAL_PEERCRED`) ist immer aktiv; sie ist die eigentliche Zugangskontrolle. Zugelassen sind der panel-Benutzer und – wie beim agent – root (Betreiber, Skripte); der Dienst selbst läuft nie als root.
- Fehlende Schlüssel unter `[limits]` erhalten die Defaults oben.
- `panel.toml` bekommt `files_socket` (Default `/run/mc-panel-files/files.sock`, Dev `.dev/files.sock`).
- **Dev-Modus** (`make dev`, macOS): Dienst läuft als aktueller Benutzer mit `instances_dir = .dev/mc` (wie der dev-agent), Peer-UID = aktueller Benutzer; fehlt `instances/<id>`, legt er es an (damit der Dateibrowser ohne Docker funktioniert).

### 3.2 Eingrenzung pro Anfrage

- Jede Anfrage nennt `id` (int64 > 0). Der Dienst öffnet `<instances_dir>/instances/<id>` mit `os.OpenRoot`; fehlt es (Server noch nie gestartet) → `404 not_found` (außer Dev). Alle weiteren Zugriffe nur über das `*os.Root`.
- Symlinks werden nie verfolgt: Vor jedem Öffnen `root.Lstat`; nach dem Öffnen `f.Stat` und `os.SameFile`-Vergleich (wie TP3a §5.4). Lesen/Schreiben/Download eines Symlinks oder einer Sonderdatei → `400 not_regular`; Hochladen/Anlegen in einen Pfad, dessen Ordner-Komponente ein Symlink ist → `400 not_regular`. Umbenennen und Löschen eines Symlinks wirken auf den Link selbst.
- Öffnen zum Lesen mit `O_NONBLOCK` (keine hängende FIFO).

### 3.3 Dateisystem

- Der agent legt `<instances_dir>/instances` künftig als `root:mcpanel-mc 0750` an (statt `root:root 0700`), damit der Dienst hineinkommt; `deleted/` bleibt `root:root 0700`. `ensureLayout` (TP2) wird entsprechend angepasst: Gruppe darf lesen/betreten, nie schreiben (`CheckInstancesDir` prüft nur `instances_dir` und dessen Eltern und bleibt unverändert).
- Die Container sehen davon nichts (nur ihr `/data`).
- Temp-Dateien für Upload, Schreiben und Entpacken liegen im Zielordner als `.mcpanel-tmp-<Prozess-Token>-<zufall>` (gleiches Dateisystem → atomares `rename`). Das Token ist pro Prozessstart zufällig. Nach dem Start entfernt ein Hintergrund-Durchlauf (der Dienst nimmt bereits Anfragen an) Temp-Einträge in allen Instanz-Verzeichnissen bis Tiefe 64; Einträge mit dem Token des eigenen Prozesses lässt er unangetastet, und beim Beenden bricht er ab.

### 3.4 Pfadregeln (gemeinsamer Code `filesapi.CleanPath`, panel und Dienst)

- Relativ, Trenner `/`; `""` oder `.` = Wurzel; kein führendes `/`, kein `\`, keine Komponente `..`, keine leeren Komponenten (`a//b`), keine Steuerzeichen (`< 0x20`, `0x7f`), gültiges UTF-8; gesamt ≤ 4096 Bytes, Komponente ≤ 255 Bytes.
- **Verborgen** (in Listen nicht sichtbar, jeder Zugriff → `404 not_found`): `.rcon-cli.env` und `.rcon-cli.yaml` auf oberster Ebene sowie jede Komponente mit Präfix `.mcpanel-`.
- **Geschwärzt:** Beim Lesen und Herunterladen von `server.properties` (oberste Ebene) wird der Wert jeder Zeile `rcon.password=…` durch `********` ersetzt. Ordner-Zip-Downloads schwärzen ebenso. Beim Speichern im Editor bleibt eine geschwärzte Zeile wirkungslos, weil itzg die Datei beim Start aus der Umgebung neu schreibt (`OVERRIDE_SERVER_PROPERTIES=true`); der Dienst ersetzt eine Zeile `rcon.password=********` beim Schreiben durch den aktuellen Wert aus der Datei, damit ein laufender Server sein Passwort behält.

## 4. API des Dienstes (Unix-Socket)

Feste Pfade, `DisallowUnknownFields`, Fehler `{error, code}` wie beim agent. JSON-Bodies ≤ 6 MiB (Editor-Inhalt + Rand), sonst 64 KiB.

| Methode, Pfad | Body / Query | Antwort |
|---|---|---|
| `POST /v1/list` | `{id, path}` | `{entries: [{name, type, size, mtime}], truncated}`; `type` ∈ `file\|dir\|symlink\|other`; `mtime` Unix-ms; sortiert: Ordner zuerst, dann Name; höchstens `max_list_entries` |
| `POST /v1/read` | `{id, path}` | `{content, mtime, size}`; nur reguläre Dateien ≤ `max_text_bytes` mit gültigem UTF-8 und ohne NUL, sonst `415 not_text` |
| `POST /v1/write` | `{id, path, content, expected_mtime}` | `{mtime, size}`; `expected_mtime` = `null` heißt „neue Datei“ (existiert sie → `409 exists`), sonst muss er dem aktuellen Wert entsprechen (`409 modified`); atomar: Temp-Datei, `fsync`, `rename`, Modus 0644 |
| `PUT /v1/upload` | Query `id`, `path`, `overwrite` (`0\|1`); Body = Datei, `Content-Length` Pflicht, ≤ `max_upload_bytes` | `201 {size}`; ohne `overwrite` und vorhandenem Ziel → `409 exists`; Abbruch/Fehler → Temp-Datei weg, Ziel unverändert |
| `GET /v1/download` | Query `id`, `path` | Datei: Stream mit `Content-Length`; Ordner: Zip-Stream (`application/zip`, on the fly, ohne Temp-Datei; Symlinks und Sonderdateien werden ausgelassen, verborgene Dateien ebenso, außerdem Namen, die sich nicht sicher in einer Zip speichern lassen: Komponenten mit `:`, mit `.` oder Leerzeichen am Ende und Windows-Gerätenamen wie `CON`, `NUL`, `COM1`–`COM9`, `LPT1`–`LPT9`, auch mit Endung). Ordner über `max_extract_entries` Einträgen oder 16 MiB Namen → `413 too_large`, Ordner mit unzulässigem eigenen Namen → `400 invalid`, jeweils vor dem ersten Byte; zu viele gleichzeitige Downloads → `429 busy` |
| `POST /v1/mkdir` | `{id, path}` | `201`; existiert → `409 exists` |
| `POST /v1/rename` | `{id, from, to}` | `204`; Ziel existiert → `409 exists`; Ordner nicht in sich selbst |
| `POST /v1/delete` | `{id, path}` | `204`; Ordner rekursiv (`root.RemoveAll`); Wurzel nicht löschbar (`400`) |
| `POST /v1/extract` | `{id, path, dest}` | Vorprüfung synchron (§4.2): Fehler → `400 invalid` (Archiv ungültig/unzulässig), `409 exists` (`dest` nicht leer), `413 too_large`, `507`; sonst `202 {job}`; `path` muss eine reguläre `.zip` sein, `dest` ein neuer oder leerer Ordner |
| `GET /v1/jobs` | Query `id` | `{job: null \| {id, state, path, dest, bytes, entries, total_bytes, total_entries, error, started_at, finished_at}}`; `state` ∈ `running\|done\|failed` |
| `GET /v1/health` | – | `{status, version}` |

Fehlercodes des Dienstes: `not_found` (404), `exists` (409), `modified` (409), `not_regular` (400), `not_text` (415), `too_large` (413), `insufficient_storage` (507), `busy` (429, Job- und Download-Limit), `invalid` (400). Fehlermeldungen und `job.error` enthalten nie Host-Pfade: der Dienst ersetzt `<instances_dir>/instances/<id>/` durch einen relativen Pfad (sonst `<instances_dir>`); das Log behält den vollen Text. Ein abgebrochener oder stockender Upload-Body (Stille > 60 s, Verbindungsabbruch) → `400 invalid`.

### 4.1 Grenzen und Plattenplatz

- Vor Upload (`Content-Length`), Schreiben und Entpacken (`total_bytes` aus dem Zentralverzeichnis) prüft der Dienst per `statfs` auf dem Instanz-Verzeichnis, dass danach noch `reserve_bytes` frei sind, sonst `507 insufficient_storage`. Beim Upload zählt er zusätzlich mit und bricht bei mehr Bytes als angekündigt ab.
- Streams: Frist pro Lese-/Schreibvorgang 60 s; keine Gesamtfrist.
- Höchstens 4 gleichzeitige Uploads im Dienst (sonst `429 busy`).
- Downloads: höchstens 4 gleichzeitige Ordner-Zips und 16 Datei-Downloads (sonst `429 busy`). Ein Ordner-Zip hält sein Zentralverzeichnis im Speicher (Namen höchstens 16 MiB); der Dienst zählt Einträge und Namen deshalb in einem ersten Durchlauf und belegt den Zip-Platz erst danach, sodass nur tatsächlich streamende Zips die 4 Plätze halten.
- `delete` und die Entpack-Vorprüfung sind ausdrücklich von der 30-s-Schreibfrist des Servers ausgenommen (Löschen einer großen Welt, Zentralverzeichnis mit bis zu 100 000 Einträgen); die Client-Fristen sind lang: Löschen 10 min, Entpack-Vorprüfung 5 min, Warten auf Download-Header 2 min (kleine Aufrufe 10 s).
- Beenden: Der Dienst bricht zuerst laufende Jobs ab (`svc.Close`), dann stoppt er HTTP (`Shutdown`). Hat der Aufrufer die Vorprüfung eines Entpackens verlassen, startet kein Job.

### 4.2 Entpacken

- Höchstens ein Job pro Instanz und zwei im Dienst insgesamt (sonst `429 busy`); Jobs leben im Speicher, das letzte Ergebnis pro Instanz bleibt bis zum nächsten Job oder Neustart des Dienstes.
- Vorprüfung **synchron in der Anfrage** über das Zentralverzeichnis, bevor ein Byte geschrieben wird (Fehler kommen als HTTP-Fehler, nicht als Job): jeder Eintragsname erfüllt die Pfadregeln (§3.4, ohne Verborgen-Ausnahme: verborgene Namen → abgelehnt), keine Symlink-Einträge (Modus im Extern-Attribut), keine Verschlüsselung, Kompressionsverfahren nur Store/Deflate, Summe der angegebenen Größen ≤ `max_extract_bytes` und Anzahl ≤ `max_extract_entries` (beides sonst `413 too_large`), keine doppelten Namen (sonst wie alle Regelverstöße `400 invalid`).
- Entpacken in einen Temp-Ordner `<dest-parent>/.mcpanel-tmp-<zufall>`, dabei erneutes Zählen der tatsächlich geschriebenen Bytes (Schutz gegen gefälschte Größen); Überschreitung oder Fehler → Temp-Ordner löschen, `state: failed`. Erfolg → `rename` des Temp-Ordners nach `dest` (existiert `dest` leer, wird er vorher entfernt).
- Dateien 0644, Ordner 0755 (umask 0027 des Dienstes wirkt zusätzlich), Zeitstempel aus dem Zip.

## 5. panel

### 5.1 API

Alle Routen über `*rbac.Router` mit `rbac.Server(rbac.PermServerFiles, "id")`, beschrieben in `api/openapi.yaml`, mit MSW-Mock. Nicht eingerichtete Server → `404 not_found`.

| Route | Inhalt |
|---|---|
| `GET /servers/{id}/files?path=` | Liste |
| `GET /servers/{id}/files/content?path=` | `{content, mtime, size}` |
| `PUT /servers/{id}/files/content` | `{path, content, expected_mtime}` → `{mtime, size}` |
| `POST /servers/{id}/files/upload?path=&overwrite=` | roher Body (`application/octet-stream`), `Content-Length` Pflicht → `201` |
| `GET /servers/{id}/files/download?path=` | Stream; `Content-Disposition: attachment; filename*=UTF-8''<name>` (Ordner: `<name>.zip`) |
| `POST /servers/{id}/files/mkdir` | `{path}` |
| `POST /servers/{id}/files/rename` | `{from, to}` |
| `POST /servers/{id}/files/delete` | `{path}` |
| `POST /servers/{id}/files/extract` | `{path, dest}` → `202 {job}` |
| `GET /servers/{id}/files/jobs` | `{job}` |

- **Streaming:** Upload und Download sind von Body-Limit (1 MiB), `ReadTimeout` und `WriteTimeout` ausgenommen; der Handler setzt per `http.ResponseController` Fristen pro Vorgang (60 s Stille → Abbruch) und reicht Bytes ohne Puffer an den Dienst weiter bzw. zurück. Grenzen pro Konto (sonst `429 rate_limited`): 2 Uploads, 4 Downloads, davon 1 Ordner-Zip; zusätzlich höchstens 8 gleichzeitige Editor-Aufrufe (Lesen und Speichern) im ganzen panel, davon höchstens 2 pro Konto (mit `Retry-After`). Ein Browser-Upload, der stockt (60 s ohne Daten) oder abbricht, antwortet `400 validation_failed` (wird als Fehlschlag auditiert). Das Speichern im Editor ist wie Upload und Download von `ReadTimeout` ausgenommen (60 s je Lesevorgang), der Body muss aber insgesamt binnen 2 min eintreffen, sonst `400`. Der `Content-Length` des Browsers wird an den Dienst weitergegeben; fehlt er → `400 validation_failed`.
- **CSRF** gilt für alle schreibenden Routen einschließlich Upload (der Browser sendet `X-CSRF-Token` per `XMLHttpRequest`). Download ist `GET` (Cookie `SameSite=Strict`).
- **Entpacken:** Vor dem Start fragt das panel den Ist-Zustand beim agent ab; Zustand ≠ `exited`/`created`/`missing` → `409 instance_running`; agent nicht erreichbar → `503 agent_unavailable`. Solange für den Server ein Job `running` ist, lehnt `POST /servers/{id}/power` mit `start`/`restart` mit `409 conflict` („Entpacken läuft“) ab, und der Abgleich (TP2 §5.3) startet den Server nicht (Soll `running` wird gemerkt und danach angewandt). Das panel erfährt laufende Jobs über `GET /v1/jobs` (Abfrage im Abgleich-Durchlauf und vor Power-Aktionen; bei nicht erreichbarem Dienst gilt „kein Job“).
- **Fehler-Mapping:** Dienst nicht erreichbar → `503 files_unavailable`; `exists` → `409 file_exists`; `modified` → `409 file_modified`; `not_text` → `415 not_text`; `not_regular` → `400 not_regular`; `too_large` → `413 payload_too_large`; `insufficient_storage` → `507 insufficient_storage`; `busy` → `429 rate_limited`; `invalid` → `400 validation_failed`; `not_found` → `404 not_found`. Bei `400` und `413` reicht das panel die Meldung des Dienstes durch (ohne technischen Präfix). Welche Route welche Status (insbesondere `413`, `429`, `507`) liefern kann, steht pro Operation in `api/openapi.yaml`; `TestFilesErrorsMatchOpenAPI` hält beides synchron.

### 5.2 Fehlercodes

Neu in `internal/httpx/errors.go` und im Enum des Vertrags:

| Code | Status | Wann |
|---|---|---|
| `files_unavailable` | 503 | Dienst `mcpanel-files` nicht erreichbar |
| `file_exists` | 409 | Ziel existiert (Upload ohne Überschreiben, Anlegen, Umbenennen) |
| `file_modified` | 409 | Datei wurde seit dem Öffnen geändert |
| `not_text` | 415 | zu groß für den Editor oder kein Text |
| `not_regular` | 400 | Symlink, Ordner oder Sonderdatei, wo eine Datei erwartet wird |
| `instance_running` | 409 | Entpacken bei laufendem Server |
| `insufficient_storage` | 507 | weniger als die Reserve frei danach |

### 5.3 Audit

Neue Aktionen in `internal/audit/types.go`: `file.write`, `file.upload`, `file.mkdir`, `file.rename`, `file.delete`, `file.extract`, `file.download`. Ziel `server:<id>`, Details `{path}` (bei `rename` `{from, to}`, bei `upload`/`extract` zusätzlich `{size}` bzw. `{dest}`), Ergebnis `success`/`failure`. Auflisten und Lesen im Editor werden nicht auditiert; Ablehnungen durch Rechte, Validierung und Rate-Limit ebenfalls nicht (wie TP1–3a); ebenso nicht ausgelastete Dienste (`429 busy`) und Downloads, die mit `400`/`404` abgelehnt werden.

## 6. Frontend

Bestehende shadcn-Komponenten; Texte `de`/`en`; mobil bedienbar.

### 6.1 Tab „Dateien“ (`server.files`)

- Brotkrumen-Pfad; Liste mit Name, Größe, Änderungsdatum (sortierbar, Ordner zuerst); Symlinks und Sonderdateien markiert; auf dem Handy Karten statt Tabelle.
- Pro Eintrag (Menü): Öffnen/Bearbeiten (Text), Herunterladen (Ordner als Zip), Umbenennen/Verschieben (Dialog mit Zielpfad), Löschen (Bestätigung; bei Ordnern „inklusive Inhalt, endgültig“), bei `.zip` „Entpacken …“.
- Kopfzeile: „Neuer Ordner“, „Neue Datei“, „Hochladen“ (auch Drag & Drop). Upload-Warteschlange mit Fortschritt und Abbrechen pro Datei. Zeigt die geladene Liste die Datei bereits, fragt der Tab „Überschreiben?“ vor dem Senden; `409 file_exists` löst dieselbe Frage nachträglich aus. Nach dem letzten Byte steht „Wird verarbeitet …“, bis der Dienst antwortet; bricht die Verbindung vor dem letzten Byte ab, steht „Upload abgebrochen – Verbindung getrennt (…)“.
- **Verlassen:** Tab-Wechsel, Routenwechsel und Neuladen bei laufenden Uploads oder ungespeicherten Editor-Änderungen fragen nach („Laufende Uploads abbrechen?“ / „Laufende Uploads werden abgebrochen.“); ein einziger Blocker im Tab deckt beides ab.
- Noch nie gestartet (keine Daten): keine Knöpfe „Neuer Ordner“, „Neue Datei“, „Hochladen“, Drops werden ignoriert.
- Fehlerdialoge für `413` (mit Meldung des Dienstes), `429` und `file_exists` haben eigene Texte.
- Hinweis-Leiste bei laufendem Server: „Änderungen wirken meist erst nach einem Neustart.“ Leerzustand bei noch nicht angelegten Daten („Der Server wurde noch nie gestartet“).

### 6.2 Editor

- CodeMirror 6 in einer lazy geladenen Komponente; Hervorhebung für YAML, JSON, TOML, Properties/INI, sonst Text; Zeilennummern, Suche.
- Speichern mit Strg/Cmd+S (auch bei Fokus auf einem Knopf des Editors) und Knopf; Warnung beim Verlassen mit ungespeicherten Änderungen. CRLF-Zeilenenden bleiben erhalten. Dateien auf den Editor zu ziehen tut nichts. `429 rate_limited` beim Öffnen oder Speichern → „Gerade werden zu viele Dateien gleichzeitig bearbeitet – bitte gleich noch einmal versuchen.“
- `409 file_modified` → Dialog „Datei wurde geändert: neu laden (eigene Änderungen verwerfen) oder überschreiben“.
- Bei `server.properties`: Hinweis „wird beim Start aus den panel-Einstellungen überschrieben“ mit Link zu den Einstellungen (Admin); `rcon.password` erscheint als `********`.
- `415 not_text` → statt Editor ein Download-Knopf.

### 6.3 Entpacken

Dialog mit Zielordner (Vorschlag: Zip-Name ohne Endung); bei laufendem Server gesperrt mit Hinweis; angeboten auch im Zustand `error` (das panel entscheidet nach dem Container-Zustand, z. B. nach einem Absturz). Während des Jobs Fortschrittsbalken (Bytes, Einträge; Abfrage alle 2 s); die Power-Buttons der Detailseite sind gesperrt. Ein fehlgeschlagener Job zeigt einen Hinweis „Entpacken von „x“ fehlgeschlagen: <Grund>“ mit der Meldung des Dienstes als Beschreibung; nur der Job-Titel wird Screenreadern angesagt.

### 6.4 Benutzerverwaltung

Beim Anhaken von `server.files`: Hinweis „„Dateien“ erlaubt Code-Ausführung im Minecraft-Server (z. B. über Plugins). Vergib es nur an Personen, denen du voll vertraust.“

### 6.5 Mocks

MSW-Mocks mit einem kleinen Datei-Baum pro Server im Speicher (Ordner, `server.properties` mit geschwärztem Passwort, YAML-Configs, eine Zip, ein Symlink), Upload mit simuliertem Fortschritt, Entpack-Job mit Fortschritt. `make preview` zeigt alles ohne Backend.

## 7. Entwicklung, Betrieb und CI

- **Mac:** nur `make check` (CLAUDE.md). `make dev` startet zusätzlich `files` im Dev-Modus (§3.1).
- **Test-VM:** `scripts/testvm-deploy.sh` baut `files`, legt `/etc/mc-panel/files.toml`, `mcpanel-files.socket` und `mcpanel-files.service` (§3.1) an und ergänzt `files_socket` in `panel.toml`.
- **Release:** GoReleaser baut `files` für `linux/amd64` und `linux/arm64`.
- **Installer (TP6):** übernimmt Unit, Konfiguration und die Rechte von `instances/` wie in §3.1/§3.3 beschrieben.
- **CI:** Unit-Tests wie bisher; Linux-spezifische Dateisystemtests (FIFO, `SO_PEERCRED`) laufen im Linux-Job; `scripts/ci-instances.sh` wird erweitert (§8).

## 8. Tests

| Ebene | Inhalt |
|---|---|
| `filesapi` | Tabellentests für `CleanPath`: `..`, absolut, `\`, leere Komponenten, `.`, Steuerzeichen, ungültiges UTF-8, Länge, verborgene Namen, Unicode |
| `files` Operationen | echtes Dateisystem (`t.TempDir`): Symlink nach außen/innen, Symlink als Ordner-Komponente, FIFO, Verzeichnis statt Datei; atomares Schreiben, `expected_mtime`; Upload mit Abbruch (keine Teil-Datei), zu groß, mehr Bytes als angekündigt; Schwärzen und Zurückschreiben von `rcon.password`; Liste mit Grenze; Löschen der Wurzel abgelehnt; Rename in sich selbst |
| `files` Entpacken | Zip-Slip (`../`, absolut, `\`), Symlink-Eintrag, verschlüsselt, unbekannte Kompression, gefälschte Größen (Zip-Bombe), zu viele Einträge, doppelte Namen, nicht leeres Ziel, Abbruch räumt auf, Job-Limits |
| `files` Download | Ordner-Zip lässt Symlinks, Sonderdateien und verborgene Dateien aus und schwärzt `server.properties` |
| `files` Server | Peer-UID, unbekannte Felder, Fristen, Plattenplatz über eine Fake-`statfs`-Schnittstelle, Temp-Aufräumen beim Start |
| agent | `instances/` mit `root:mcpanel-mc 0750`, `deleted/` weiter `0700`; `CheckInstancesDir` akzeptiert Gruppen-Lesen, lehnt Gruppen-Schreiben ab |
| panel `serverfiles` | Fake-Dienst + `storetest`: Rechte (`files`, nur `view`, Admin, fremder Server), CSRF, Streaming ohne Puffern (großer Upload-Body mit begrenztem Speicher), Upload-Limit pro Benutzer, Fehler-Mapping, Audit-Einträge, Entpacken nur gestoppt, Power-Sperre und Abgleich-Pause während eines Jobs |
| Vertrag | OpenAPI ↔ Routen, Fehlercodes ↔ Enum, Mock pro Operation |
| Frontend | Vitest: Pfad-Helfer, Upload-Warteschlange, Konflikt-Dialog, Dateityp-Erkennung für den Editor; Playwright gegen Mock: Ordner öffnen → Config bearbeiten → speichern; Upload; Umbenennen; Löschen; Zip entpacken bei gestopptem Server |
| CI-Abnahme | `scripts/ci-instances.sh`: Dienst als `mcpanel-mc` gegen den Paper-Server; Plugin-Jar hochladen; `server.properties` lesen (Passwort geschwärzt); Config ändern; Server stoppen, Welt-Zip hochladen und entpacken; Zip-Slip-Archiv abgelehnt; Symlink auf `/etc/shadow` liefert keinen Inhalt; Prozess läuft als `mcpanel-mc` (`/proc/<pid>/status`); Schreibversuch nach `<instances_dir>/router` scheitert |

Konventionen aus TP1–3a gelten unverändert.

## 9. Abnahmekriterien

Automatisch in der CI: 1 sowie die Dienst-Seite von 2–4. Manuell durch den Nutzer auf der Test-VM: 2–7 (`docs/dev/acceptance-tp3b.md`).

1. Alle CI-Jobs grün.
2. Eine Welt als Zip hochladen und bei gestopptem Server entpacken; nach dem Umstellen (z. B. Ordner umbenennen zu `world`) startet der Server damit.
3. Ein Plugin-Jar nach `plugins/` hochladen; nach dem Neustart ist es aktiv.
4. Eine Config im Editor ändern und speichern; ein zweiter Tab mit veraltetem Stand bekommt den Konflikt-Dialog.
5. Einen Ordner (z. B. `world`) als Zip herunterladen.
6. Ein Nutzer mit nur `server.view` sieht keinen Dateien-Tab, die API antwortet `403`.
7. Alle Schreibaktionen und Downloads stehen im Audit-Log; `panel audit verify` meldet OK.
