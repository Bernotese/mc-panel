# mc-panel – Teilprojekt 3c: Plugins und Mods (Design-Spec)

- **Datum:** 2026-10-12
- **Status:** entworfen
- **Autor:** Jannes (mit Claude)
- **Baut auf:** `2026-10-08-mc-panel-foundation-design.md` (TP1), `2026-10-10-mc-panel-instances-design.md` (TP2), `2026-10-11-mc-panel-console-players-design.md` (TP3a), `2026-10-11-mc-panel-files-design.md` (TP3b)

## 1. Ziel und Kontext

Teilprojekt 3 („Live-Betrieb“) ist dreigeteilt: 3a Konsole/Logs/Spieler und 3b Dateibrowser sind umgesetzt, **3c Plugins/Mods** ist dieses Dokument.

### 1.1 Erfolg

Ein Nutzer mit `server.plugins` sucht auf einem Paper- oder Purpur-Server Plugins in Modrinth und Hangar, auf einem Fabric- oder NeoForge-Server Mods in Modrinth, und installiert sie samt Pflicht-Abhängigkeiten mit einem Klick. Er sieht alle installierten Jars – auch per Hand hochgeladene –, prüft und installiert Updates, deaktiviert und entfernt Jars.

### 1.2 Leitlinie

Unverändert: Ein kompromittiertes panel darf keinen Root auf dem Host bekommen (TP1 §3), und Dateien in Datenverzeichnissen schreibt nur der unprivilegierte Dienst `mcpanel-files` (TP3b §1.2). Das panel lädt Jars aus dem Internet und reicht sie als Stream an den Dienst weiter; der Dienst bekommt keinen Netzzugang, der root-agent ändert sich nicht.

### 1.3 Nicht-Ziele (3c)

- CurseForge-Mods, Spigot/SpigotMC, Bukkit.org und andere Quellen als Modrinth und Hangar.
- Plugins/Mods auf vanilla- und Modpack-Servern (`modrinth`, `curseforge`): dort bestimmt das Pack den Inhalt.
- Datapacks, Resourcepacks, Shader, Proxy-Plugins (Velocity, BungeeCord).
- Herunterladen von nur extern verlinkten Dateien (Hangar `externalUrl`): keine Prüfsumme, beliebige Hosts.
- Plugin-Konfiguration bearbeiten (dafür gibt es den Dateien-Tab), Plugins im laufenden Server laden oder entladen (kein Hot-Reload).
- Automatische Updates ohne Klick.

## 2. Entscheidungen

| Bereich | Entscheidung | Begründung |
|---|---|---|
| Quellen | paper/purpur: Plugins aus Modrinth und Hangar; fabric/neoforge: Mods aus Modrinth | Wunsch des Betreibers; Hangar hat keine Mods |
| Download | panel lädt von festen CDN-Hosts, streamt an `mcpanel-files`, der Dienst prüft die Prüfsumme vor dem `rename` | Einziger Weg, der Hangar abdeckt und sofort wirkt, ohne einem Dienst neue Rechte zu geben (§2.1) |
| Quelle der Wahrheit | Die Platte (`plugins/` bzw. `mods/`); die DB merkt sich nur die Herkunft panel-installierter Jars | Jars können auch über den Dateien-Tab, im Spiel oder vom Server selbst kommen und gehen |
| Zuordnung | DB-Eintrag (Dateiname + sha512), sonst Modrinth per Hash, sonst „manuell“ mit Metadaten aus dem Jar | Hangar hat keine Hash-Suche; Modrinth erkennt die meisten manuell hochgeladenen Jars |
| Ablauf | Installieren und Aktualisieren als Job im panel (einer pro Server), Abfrage des Fortschritts | Mehrere Downloads samt Abhängigkeiten passen nicht in eine Anfrage |
| Laufender Server | Alles jederzeit erlaubt; Banner „Neustart nötig“ | Jars werden nur beim Start geladen; Linux ersetzt Dateien per `rename`, ohne dem laufenden Server die offene Datei zu nehmen |
| Recht | `server.plugins` (in TP1 bereits angelegt), volles Vertrauen, Hinweis beim Vergeben | Ein Plugin führt Code im Container aus |
| Icons | Proxy im panel | CSP (`img-src 'self'`) bleibt unverändert, der Browser kontaktiert keine Dritten |

### 2.1 Verworfene Alternativen

- **Deklarativ über itzg (`MODRINTH_PROJECTS`):** passt zum Soll-Zustand-Modell, aber itzg kennt Hangar nicht, Änderungen wirken erst beim Neustart, und manuelle Jars blieben außen vor. Außerdem erlaubt TP2 §5.4 bewusst keine weiteren itzg-Variablen.
- **`mcpanel-files` lädt selbst herunter:** bräuchte Netzzugang (heute `RestrictAddressFamilies=AF_UNIX`) und machte den Dateidienst zum Internet-Client.
- **Prüfsumme nur im panel:** das panel müsste das letzte Byte zurückhalten, bis die Prüfsumme stimmt. Die Prüfung im Dienst vor dem `rename` ist einfacher und schützt auch gegen Fehler auf dem Weg panel → Dienst.

## 3. Architektur

```
Browser ──/api/servers/{id}/plugins/*──▶ panel: internal/plugins ──HTTPS, feste Hosts──▶ api.modrinth.com, hangar.papermc.io
                                           │ Suche, Auflösen, Jobs, Inventar,          ◀── Jar/Icon von cdn.modrinth.com,
                                           │ DB, Audit, Icon-Proxy                          hangarcdn.papermc.io
                                           └──Unix-Socket──▶ mcpanel-files: /v1/jars, /v1/upload (+Prüfsumme), rename, delete, mkdir
```

- **panel:** neues Paket `internal/plugins` (Handler, Routen, Inventar, Auflösen, Jobs, Icon-Proxy) mit Unterpaket `internal/plugins/upstream` (Clients für Modrinth und Hangar, Download, Cache). Verdrahtung in `cmd/panel/app.go`.
- **Dienst:** neue Operation `jars` in `internal/files` (Auflisten mit Prüfsummen und Metadaten), Erweiterung von `upload` um eine Prüfsumme; Draht-Typen und Client in `internal/files/filesapi`.
- **agent:** keine Änderung. Das panel nutzt nur den vorhandenen Ist-Zustand (`state`, `started_at`).

### 3.1 Server-Arten und Kompatibilität

| Art | Ordner | Art im UI | Modrinth `project_type` | Modrinth-Loader | Hangar |
|---|---|---|---|---|---|
| paper | `plugins/` | Plugins | `plugin` | `paper`, `spigot`, `bukkit` | Plattform `PAPER` |
| purpur | `plugins/` | Plugins | `plugin` | `purpur`, `paper`, `spigot`, `bukkit` | Plattform `PAPER` |
| fabric | `mods/` | Mods | `mod` | `fabric` | – |
| neoforge | `mods/` | Mods | `mod` | `neoforge` | – |
| vanilla, modrinth, curseforge | – | kein Tab | – | – | – |

- **Spielversion** = `version` der Instanz. `LATEST` löst das panel über den vorhandenen Versionskatalog (`internal/servers/catalog`, neueste Version der Art) auf; ist der Katalog nicht erreichbar, liefern Suche, Versionen, Auflösen, Installieren und Updates `502 catalog_unavailable`, das Inventar funktioniert weiter (`game_version: null`).
- **Client-Mods:** Bei Mods filtert die Suche auf `server_side` ∈ `required|optional`; Projekte mit `server_side: unsupported` gelten als inkompatibel (auch als Abhängigkeit).
- **Kanäle:** Modrinth `version_type` (`release|beta|alpha`) wird übernommen; bei Hangar wird der Kanal `Release` (Groß-/Kleinschreibung egal) zu `release`, jeder andere zu `beta`.

## 4. Upstreams (`internal/plugins/upstream`)

Feste Basis-URLs (in Tests überschreibbar), geprüft gegen die Live-Dienste am 2026-10-12; gekürzte echte Antworten liegen in `testdata/`. User-Agent wie bei den Katalogen: `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`.

| Zweck | Modrinth (`https://api.modrinth.com`) | Hangar (`https://hangar.papermc.io`) |
|---|---|---|
| Suche | `GET /v2/search?query=&facets=&index=&offset=&limit=20`; Facets `project_type:<t>`, `categories:<loader>` (ODER über die Loader), `versions:<gv>`, bei Mods `server_side:required` ODER `server_side:optional`; `index=relevance` mit Suchbegriff, sonst `downloads` | `GET /api/v1/projects?q=&platform=PAPER&version=<gv>&sort=-downloads&offset=&limit=20` |
| Projekt | `GET /v2/project/{id}` | `GET /api/v1/projects/{slugOrId}` |
| Versionen | `GET /v2/project/{id}/version?loaders=[…]&game_versions=[<gv>]` | `GET /api/v1/projects/{slugOrId}/versions?platform=PAPER&platformVersion=<gv>&limit=25` |
| Version | `GET /v2/version/{id}` | `GET /api/v1/projects/{slugOrId}/versions/{name}` (der Versionsname ist die ID) |
| Hash-Zuordnung | `POST /v2/version_files {hashes, algorithm: "sha512"}` (bis 500 Hashes) | – |
| Updates | `POST /v2/version_files/update {hashes, algorithm, loaders, game_versions, version_types}` | Versionsliste, neueste im passenden Kanal |
| Abhängigkeiten | `dependencies[]` mit `dependency_type = required`, `project_id`, optional `version_id` | `pluginDependencies.PAPER[]` mit `required = true`; `projectId` (Hangar) oder `externalUrl` (extern) |
| Datei | `files[]`: `primary = true`, sonst die erste `.jar`; `url`, `filename`, `size`, `hashes.sha512` | `downloads.PAPER.fileInfo` (`name`, `sizeBytes`, `sha256Hash`) und `downloadUrl`; `fileInfo = null` mit `externalUrl` → nur extern |

- **Grenzen:** Timeout 10 s pro API-Anfrage, Antworten ≤ 5 MiB, Fehler/Status ≠ 2xx/kaputtes JSON → `502 plugin_source_unavailable`. Modrinth-Ratelimit (300/min pro IP) wird respektiert: Antwort `429` des Upstreams → ebenfalls `502` mit Meldung.
- **Cache im Speicher:** Suche 5 min, Projekte und Versionslisten 15 min, Hash-Zuordnung 1 h (auch „unbekannt“), Updates 1 h pro Server. Gleichzeitige gleiche Abfragen teilen sich einen Aufruf (wie `catalog`). Höchstens 1000 Einträge je Cache, älteste zuerst verdrängt.
- **Downloads und Icons** nur von `https://cdn.modrinth.com/` und `https://hangarcdn.papermc.io/`; Weiterleitungen nur auf diese Hosts und nur über HTTPS (höchstens 3). Andere Hosts (auch aus Upstream-Antworten) → Schritt-Fehler bzw. `400 validation_failed`.
- **Jar-Download:** ≤ 256 MiB (`size` aus der API, sonst Abbruch), Frist 60 s pro Lesevorgang, keine Gesamtfrist. Weicht der `Content-Length` des CDN von der API-Größe ab, bricht das panel vor dem Weiterreichen ab.
- **Dateiname:** Der Upstream-Name muss `^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}\.jar$` erfüllen und `filesapi.CleanPath` bestehen; sonst `<slug>-<version>.jar`, unzulässige Zeichen durch `-` ersetzt und auf 128 Zeichen gekürzt.

## 5. Dienst `mcpanel-files`

### 5.1 `POST /v1/jars`

Body `{id, dir}`, `dir` ∈ `plugins|mods`. Antwort `{jars: [{name, enabled, size, mtime, sha256, sha512, meta}], truncated}`.

- Listet reguläre Dateien der obersten Ebene von `dir` mit Endung `.jar` (`enabled: true`) oder `.jar.disabled` (`enabled: false`), Groß-/Kleinschreibung egal; Symlinks, Ordner und Sonderdateien werden ausgelassen (Regeln aus TP3b §3.2: `Lstat`, `O_NOFOLLOW|O_NONBLOCK`, `os.SameFile`). Höchstens 500 Jars (`truncated: true`). Fehlt `dir` oder ist es kein Ordner → leere Liste.
- **Prüfsummen** sha256 und sha512 in einem Durchlauf; Jars > 256 MiB ohne Prüfsummen (`""`). Cache im Speicher nach (Gerät, Inode, Größe, mtime), höchstens 4096 Einträge. Höchstens 2 gleichzeitige Hash-Durchläufe im Dienst (sonst wartet die Anfrage); Client-Frist 2 min.
- **Metadaten** (`meta`, best-effort, sonst `null`): Der Dienst öffnet das Jar als Zip (Zentralverzeichnis ≤ 100 000 Einträge) und liest höchstens eine Datei ≤ 64 KiB, in dieser Reihenfolge:

| Datei | `kind` | Felder |
|---|---|---|
| `paper-plugin.yml`, `plugin.yml` | `plugin` | `name`, `version` (Schlüssel der obersten Ebene, einfache Zeilen `key: value`, Anführungszeichen entfernt; kein YAML-Parser) |
| `fabric.mod.json` | `mod` | `id`, `name`, `version` |
| `META-INF/neoforge.mods.toml` | `mod` | erster `[[mods]]`-Eintrag: `modId`, `displayName`, `version`; `${file.jarVersion}` → `Implementation-Version` aus `META-INF/MANIFEST.MF` (≤ 64 KiB), sonst leer |

  Werte werden auf 128 Zeichen gekürzt, Steuerzeichen entfernt. Ein kaputtes Jar liefert `meta: null`, nie einen Fehler der ganzen Liste.

### 5.2 Upload mit Prüfsumme

`PUT /v1/upload` bekommt die optionalen Query-Parameter `sha512` (128 Hex-Zeichen) und `sha256` (64 Hex-Zeichen), höchstens einer. Ist einer gesetzt, berechnet der Dienst beim Schreiben der Temp-Datei sha256 und sha512, vergleicht vor `fsync`/`rename` und antwortet bei Abweichung `400 hash_mismatch` (Temp-Datei weg, Ziel unverändert). Erfolg: `201 {size, sha256, sha512}` (ohne Parameter wie bisher `{size}`). Neuer Fehlercode des Dienstes `hash_mismatch` (400).

## 6. panel

### 6.1 Datenmodell

Migration `internal/store/migrations/00005_plugins.sql`, Queries in `internal/store/queries/plugins.sql` (Präfix `Plugins…`):

```sql
CREATE TABLE plugin_installs (
    server_id    INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,   -- Dateiname ohne ".disabled"
    sha512       TEXT    NOT NULL,
    source       TEXT    NOT NULL CHECK (source IN ('modrinth', 'hangar')),
    project_id   TEXT    NOT NULL,   -- Modrinth-ID bzw. Hangar-ID (Zahl als Text)
    project_slug TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    version_id   TEXT    NOT NULL,   -- Modrinth-Versions-ID bzw. Hangar-Versionsname
    version_name TEXT    NOT NULL,
    channel      TEXT    NOT NULL CHECK (channel IN ('release', 'beta', 'alpha')),
    installed_at INTEGER NOT NULL,
    PRIMARY KEY (server_id, filename)
);
```

Ein Eintrag gilt nur, wenn ein Jar dieses Namens (mit oder ohne `.disabled`) mit genau dieser sha512 auf der Platte liegt; sonst löscht das Inventar ihn beim Lesen.

### 6.2 Inventar

`GET /servers/{id}/plugins` fragt `/v1/jars` und ordnet jedes Jar zu:

1. DB-Eintrag mit gleichem Namen und gleicher sha512 → Quelle aus der DB (`installed_by_panel: true`).
2. sonst Modrinth `version_files` per sha512 (Cache 1 h) → Quelle `modrinth` mit Projekt (Titel, Slug über `GET /v2/project`, gecacht) und Version.
3. sonst `manual` mit `meta`.

Ist Modrinth nicht erreichbar, bleiben die betroffenen Jars `manual` und die Antwort trägt `source_errors: ["modrinth"]`. `restart_needed` ist `true`, wenn der Server läuft und das panel seit `started_at` des Containers eine Plugin-Änderung für ihn ausgeführt hat (Zeitpunkt im Speicher; nach einem Neustart des panels vergessen).

### 6.3 Auflösen

`resolve` und jeder Job bauen denselben Plan aus Projekt und gewünschter Version (fehlt sie: neueste kompatible `release`, sonst die neueste kompatible überhaupt):

- Pflicht-Abhängigkeiten rekursiv bis Tiefe 5, höchstens 20 Pakete im Plan; Zyklen werden erkannt. Modrinth-Abhängigkeit mit `version_id` → genau diese Version (muss kompatibel sein), sonst neueste kompatible `release`. Hangar-Abhängigkeit mit `projectId` → neueste kompatible Version im Kanal `Release`, sonst die neueste.
- Bereits installiert ist ein Projekt, wenn das Inventar ein Jar mit derselben Quelle und Projekt-ID hat (aktiv oder deaktiviert) → `already`, wird nicht angefasst. Abhängigkeiten über Quellen hinweg werden nicht abgeglichen.
- Extern verlinkte Abhängigkeit (Hangar `externalUrl`) → `external` mit Name und Link. Abhängigkeit ohne kompatible herunterladbare Version → `incompatible`; dann ist der Plan nicht ausführbar (`409 plugin_incompatible`). Gewünschte Version nur extern → `409 plugin_not_downloadable`.
- Plan-Eintrag: `{source, project_id, slug, title, version_id, version_name, channel, filename, size, required_by}`.

### 6.4 Jobs

- Im Speicher, höchstens einer pro Server (sonst `409 conflict`) und vier im panel (sonst `429 rate_limited`); das letzte Ergebnis bleibt bis zum nächsten Job oder Neustart des panels. Ein Neustart des panels bricht laufende Jobs ab; Temp-Dateien räumt der Dienst ohnehin auf.
- **install:** Plan neu berechnen (der Vorschau wird nicht vertraut), Zielordner bei Bedarf per `mkdir` anlegen, dann je Plan-Eintrag (Abhängigkeiten zuerst): herunterladen → `PUT /v1/upload` mit Prüfsumme (Modrinth `sha512`, Hangar `sha256`) → DB-Eintrag mit der sha512 aus der Antwort. Existiert der Dateiname schon (mit oder ohne `.disabled`) und gehört zu einem anderen Projekt oder ist manuell → Schritt-Fehler `file_exists`, der Job bricht ab. Bereits installierte Schritte bleiben installiert.
- **update:** pro gewähltem Jar (leer = alle mit Update): neue Version herunterladen, unter neuem Namen hochladen (gleicher Name → `overwrite=1`), deaktivierte Jars bleiben deaktiviert (Upload als `<name>.jar.disabled`), danach altes Jar löschen und DB-Eintrag ersetzen. Neue Pflicht-Abhängigkeiten der neuen Version werden wie bei `install` mitinstalliert. Ein Fehler betrifft nur den Schritt; der Job läuft weiter und endet `failed`, wenn ein Schritt fehlschlug.
- Zustände: Job `running|done|failed`, Schritt `pending|downloading|done|failed|skipped`; Schritt-Fehler als Code (`download_failed`, `hash_mismatch`, `file_exists`, `insufficient_storage`, `files_unavailable`, `source_unavailable`) plus Text ohne Host-Pfade.

### 6.5 Updates

`GET /servers/{id}/plugins/updates` (Cache 1 h pro Server, `?refresh=1` umgeht ihn) liefert `{updates: [{file, from: {version_id, version_name}, to: {version_id, version_name, channel}}], checked_at}`. Modrinth-Jars (DB oder Hash-Zuordnung) über `version_files/update` mit `version_types = [release]` bzw. `[release, beta]` bei einem Beta-Jar bzw. allen dreien bei Alpha; Hangar-Jars aus der DB über die Versionsliste, neueste herunterladbare Version im gleichen oder stabileren Kanal, nur wenn sie neuer veröffentlicht wurde als die installierte. Manuelle Jars bekommen keine Updates.

### 6.6 API

Alle Routen über `*rbac.Router` mit `rbac.Server(rbac.PermServerPlugins, "id")`, in `api/openapi.yaml` beschrieben, mit MSW-Mock. Server ohne Instanz → `404 not_found`; Art ohne Plugins/Mods → `409 plugins_unsupported` (alle Routen außer `icon`).

| Route | Inhalt |
|---|---|
| `GET /servers/{id}/plugins` | `{kind: plugin\|mod, dir, game_version, sources, jars: [{file, enabled, size, mtime, source: modrinth\|hangar\|manual, project: {id, slug, title, page_url, icon_url}?, version: {id, name, channel}?, meta?, installed_by_panel}], truncated, restart_needed, source_errors}` |
| `GET /servers/{id}/plugins/search?source=&q=&offset=` | `{hits: [{source, project_id, slug, title, description, author, downloads, icon_url, page_url, installed}], total, offset}`; `q` ≤ 100 Zeichen, `offset` ≤ 1000 |
| `GET /servers/{id}/plugins/versions?source=&project=` | `{versions: [{id, name, channel, published_at, downloadable, external_url?}]}` (kompatibel, neueste zuerst, höchstens 25) |
| `GET /servers/{id}/plugins/resolve?source=&project=&version=` | `{install: [PlanEntry], already: [...], external: [{name, url}], incompatible: [{title, reason}]}` |
| `POST /servers/{id}/plugins/install` | `{source, project, version?}` → `202 {job}` |
| `GET /servers/{id}/plugins/updates` | §6.5 |
| `POST /servers/{id}/plugins/update` | `{files: [string]}` → `202 {job}` |
| `POST /servers/{id}/plugins/toggle` | `{file, enabled}` → `204`; `rename` im Dienst |
| `POST /servers/{id}/plugins/delete` | `{file}` → `204`; löscht auch den DB-Eintrag |
| `GET /servers/{id}/plugins/jobs` | `{job: null \| {id, kind: install\|update, state, steps: [{title, file, state, error_code, error}], started_at, finished_at}}` |
| `GET /servers/{id}/plugins/icon?url=` | Bild (§6.8) |

- `source` ∈ `modrinth|hangar` (`hangar` nur bei Plugins, sonst `400 validation_failed`); `project`/`version` ≤ 128 Zeichen, `^[A-Za-z0-9._+-]+$`. `file` ist ein reiner Dateiname ohne `/`, endet auf `.jar` oder `.jar.disabled` und besteht `filesapi.CleanPath`; der Pfad im Dienst ist immer `<dir>/<file>`.
- `icon_url` und `project.icon_url` in Antworten zeigen bereits auf den Icon-Proxy (`/api/servers/{id}/plugins/icon?url=…`), `page_url` auf die Projektseite (`https://modrinth.com/<plugin|mod>/<slug>` bzw. `https://hangar.papermc.io/<owner>/<slug>`).
- **CSRF** gilt für alle `POST`. **Rate-Limit** pro Konto: 60 Aufrufe pro Minute über `search`, `versions`, `resolve`, `updates` mit `refresh=1` (sonst `429 rate_limited` mit `Retry-After`).
- **Fehler-Mapping** des Dienstes wie TP3b §5.1 (`files_unavailable`, `file_exists`, `not_found`, `insufficient_storage` …).

### 6.7 Fehlercodes

Neu in `internal/httpx/errors.go` und im Enum des Vertrags:

| Code | Status | Wann |
|---|---|---|
| `plugins_unsupported` | 409 | Server-Art ohne Plugins/Mods |
| `plugin_source_unavailable` | 502 | Modrinth oder Hangar nicht erreichbar oder antwortet unbrauchbar |
| `plugin_not_downloadable` | 409 | gewählte Version nur extern verfügbar |
| `plugin_incompatible` | 409 | Projekt oder Pflicht-Abhängigkeit ohne kompatible Version |

Vorhanden und genutzt: `conflict` (Job läuft), `catalog_unavailable` (`LATEST` nicht auflösbar), `rate_limited`, `validation_failed`, `not_found`.

### 6.8 Icon-Proxy

- `url` muss mit `https://cdn.modrinth.com/` oder `https://hangarcdn.papermc.io/` beginnen (≤ 512 Zeichen), sonst `400 validation_failed`; Weiterleitungen wie §4.
- Bild ≤ 256 KiB, Typ per Magic Bytes: nur PNG, JPEG, WebP, GIF (kein SVG) → sonst `502 plugin_source_unavailable`. Antwort mit erkanntem `Content-Type`, `X-Content-Type-Options: nosniff`, `Content-Security-Policy: default-src 'none'`, `Cache-Control: private, max-age=86400`.
- Cache im Speicher (LRU, 32 MiB, 24 h). Nicht im Rate-Limit aus §6.6, aber höchstens 8 gleichzeitige Upstream-Abrufe im panel.

### 6.9 Audit

Neue Aktionen in `internal/audit/types.go`: `plugin.install` `{source, project, version, file}`, `plugin.update` `{file, from, to}`, `plugin.remove` `{file}`, `plugin.toggle` `{file, enabled}`. Ziel `server:<id>`, Ergebnis `success`/`failure`, ein Eintrag pro Jar (auch für Abhängigkeiten); Akteur ist der Nutzer, der den Job gestartet hat. Inventar, Suche, Versionen, Auflösen, Updatecheck und Icons werden nicht auditiert, ebenso Ablehnungen durch Rechte, Validierung und Rate-Limit (wie TP1–3b).

## 7. Frontend

Bestehende shadcn-Komponenten; Texte `de`/`en`; mobil bedienbar.

### 7.1 Tab „Plugins“ bzw. „Mods“

Sichtbar mit `server.plugins` bei paper, purpur, fabric und neoforge; Titel nach `kind`.

- **Installiert:** Liste mit Icon, Name (Projekt-Titel, sonst `meta.name`, sonst Dateiname), Version, Quelle (Modrinth/Hangar/manuell), Schalter aktiv/inaktiv, Menü (Projektseite öffnen, Version wechseln → Installieren-Dialog mit vorgewählter Version, Entfernen mit Bestätigung), Update-Badge. Manuelle Jars ohne Zuordnung: Hinweis „Nicht zugeordnet – Updates nicht möglich“. Kopfzeile: „Nach Updates suchen“, „Alle aktualisieren (n)“. Auf dem Handy Karten statt Tabelle. `source_errors` → Hinweis „Modrinth gerade nicht erreichbar – Zuordnung unvollständig“.
- **Entdecken:** Suchfeld (verzögert), Quellen-Umschalter Modrinth | Hangar (bei Mods nur Modrinth), Treffer mit Icon, Titel, Autor, Downloads, Kurzbeschreibung, Badge „installiert“, Knopf „Installieren“; ohne Suchbegriff die beliebtesten passenden Projekte; „Mehr laden“. Beschreibungen (Markdown/HTML) werden nicht gerendert, nur die Kurzbeschreibung und ein Link zur Projektseite.
- **Installieren-Dialog:** Versionsauswahl (Standard: neueste `release`; Beta/Alpha markiert; nur extern verfügbare ausgegraut mit „Nur extern erhältlich“, Link und Hinweis auf den Dateien-Tab), darunter die Vorschau aus `resolve`: „Wird mitinstalliert“, „Bereits vorhanden“, „Manuell nötig“ (mit Links), „Inkompatibel“ (sperrt „Installieren“).
- **Job:** Fortschritt pro Jar (wartet, lädt, fertig, übersprungen, Fehler mit übersetztem Grund `plugins.step.error.*`), Abfrage alle 2 s, solange `running`; Installieren-, Update-, Schalter- und Entfernen-Knöpfe gesperrt. Nach Ende wird das Inventar neu geladen.
- **Banner** bei `restart_needed`: „Neustart nötig, damit die Änderungen wirken“, mit Neustart-Knopf bei `server.power`.
- Upstream nicht erreichbar: Entdecken zeigt einen Fehlerzustand mit „Erneut versuchen“; Installiert bleibt bedienbar.

### 7.2 Benutzerverwaltung

Beim Anhaken von `server.plugins`: Hinweis „„Plugins/Mods“ erlaubt Code-Ausführung im Minecraft-Server. Vergib es nur an Personen, denen du voll vertraust.“

### 7.3 Mocks

MSW-Mocks pro Operation: kleiner Katalog (Modrinth und Hangar, ein Hangar-Projekt nur extern, eins mit Pflicht-Abhängigkeit, eine Beta-Version, eine Client-only-Mod, die nicht erscheint), Inventar mit panel-installiertem, per Hash zugeordnetem und manuellem Jar, simulierter Job mit Fortschritt, ein verfügbares Update, Icons als kleine PNGs aus dem Mock. `make preview` zeigt alles ohne Backend.

## 8. Entwicklung, Betrieb und CI

- **Mac:** nur `make check` (CLAUDE.md). `make dev` braucht nichts Neues; das panel spricht im Dev-Modus mit den echten Upstreams.
- **Test-VM / Produktion:** keine neue Konfiguration. Das panel braucht ausgehendes HTTPS zu `api.modrinth.com`, `cdn.modrinth.com`, `hangar.papermc.io`, `hangarcdn.papermc.io` (wie für die Kataloge).
- **Release:** unverändert.

## 9. Tests

| Ebene | Inhalt |
|---|---|
| `plugins/upstream` | `httptest` mit echten gekürzten Antworten aus `testdata/` (Suche beider Quellen, Versionen, `version_files`, `version_files/update`, Hangar mit internem und externem Download und Abhängigkeiten); Facets und Query-Parameter, Host-Allowlist inkl. Weiterleitungen auf fremde Hosts und `http`, Größenlimit, abweichender `Content-Length`, Timeout, Cache und geteilte Aufrufe, Dateinamen-Fallback |
| Auflösen | Tiefe und Paketlimit, Zyklus, bereits installiert (auch deaktiviert), extern, inkompatibel, feste `version_id`, Kanalwahl, Client-only-Mod |
| `files` | `/v1/jars`: `.jar`/`.jar.disabled`, Symlink-Jar und Ordner ausgelassen, Grenze 500, Prüfsummen und Cache (geänderte mtime → neu berechnet), Metadaten aus `paper-plugin.yml`, `plugin.yml`, `fabric.mod.json`, `neoforge.mods.toml` mit `${file.jarVersion}`, kaputtes und übergroßes Jar → `meta: null`; Upload mit falscher Prüfsumme hinterlässt keine Datei und lässt das Ziel unverändert, beide Parameter zugleich → `400 invalid` |
| panel `plugins` | Fake-Upstream + Fake-Dienst + `storetest`: Rechte (`plugins`, nur `view`, Admin, fremder Server), CSRF, Art ohne Plugins, `LATEST`-Auflösung, Inventar-Zuordnung (DB, Hash, manuell, verwaister Eintrag), Install-Job mit Abhängigkeit, Abbruch bei `file_exists`, Update mit neuem Dateinamen und deaktiviertem Jar, ein Job pro Server, Job-Limit, `restart_needed`, Audit-Einträge, Rate-Limit, Icon-Proxy (SVG, fremder Host, zu groß) |
| Vertrag | OpenAPI ↔ Routen, Fehlercodes ↔ Enum, Mock pro Operation |
| Frontend | Vitest: Inventar-Darstellung (Namensfallback, Badges), Auflösungs-Vorschau, Job-Fortschritt; Playwright gegen Mock: suchen → installieren mit Abhängigkeit → deaktivieren → Update → entfernen |
| CI-Abnahme | `scripts/ci-instances.sh` gegen die echten Upstreams und den Paper-Server: ein kleines Modrinth-Plugin und ein Hangar-Plugin mit Pflicht-Abhängigkeit installieren, Server neu starten, per RCON `plugins` prüfen, ein Jar deaktivieren und erneut prüfen. Upstream-Fehler werden einmal wiederholt und brechen dann mit klarer Meldung ab (kein stilles Überspringen). |

Konventionen aus TP1–3b gelten unverändert.

## 10. Abnahmekriterien

Automatisch in der CI: 1 sowie die Dienst-Seite von 2 und 5. Manuell durch den Nutzer auf der Test-VM: 2–7 (`docs/dev/acceptance-tp3c.md`).

1. Alle CI-Jobs grün.
2. Auf einem Paper-Server LuckPerms (Modrinth) und ViaBackwards (Hangar, zieht ViaVersion mit) installieren; nach dem Neustart sind sie aktiv.
3. Auf einem Fabric-Server eine Mod installieren, die Fabric API mitbringt; der Server startet damit.
4. Ein per Dateien-Tab hochgeladenes Modrinth-Jar einer älteren Version wird zugeordnet und lässt sich aktualisieren.
5. Ein Jar deaktivieren; nach dem Neustart ist es nicht geladen; wieder aktivieren lädt es erneut.
6. Ein Nutzer mit nur `server.view` sieht keinen Plugins-Tab, die API antwortet `403`.
7. Alle Installationen, Updates, Schalter und Löschungen stehen im Audit-Log; `panel audit verify` meldet OK.
