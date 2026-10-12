# mc-panel Teilprojekt 3c: Plugins und Mods – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plugins (paper/purpur: Modrinth + Hangar) und Mods (fabric/neoforge: Modrinth) pro Server suchen, samt Pflicht-Abhängigkeiten installieren, aktualisieren, deaktivieren und entfernen; manuell hochgeladene Jars erkennen.

**Architecture:** Neues panel-Paket `internal/plugins` (Handler, Inventar, Auflösen, Jobs, Icon-Proxy) mit Unterpaket `internal/plugins/upstream` (Modrinth, Hangar, Download, Cache). Das panel lädt Jars nur von `cdn.modrinth.com`/`hangarcdn.papermc.io` und streamt sie an den unprivilegierten Dienst `mcpanel-files`, der die Prüfsumme vor dem `rename` prüft; der Dienst bekommt dafür `POST /v1/jars` (Auflisten mit Prüfsummen und Metadaten) und Prüfsummen-Parameter am Upload. Die Platte ist Quelle der Wahrheit, die neue Tabelle `plugin_installs` merkt sich nur die Herkunft panel-installierter Jars. Der agent ändert sich nicht.

**Tech Stack:** Go (Standardbibliothek, chi, `modernc.org/sqlite`, `BurntSushi/toml`, `archive/zip`, `crypto/sha256`, `crypto/sha512`); React 19, TanStack Router/Query, shadcn/ui, react-i18next, MSW 2, Vitest, Playwright. Keine neuen Go- und keine neuen Frontend-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-12-plugins-00-overview.md` (diese) | – | Ausführung, Constraints, Review Focus, Dateistruktur, Verträge V1–V5 |
| `2026-10-12-plugins-01-contracts.md` | 1–3 | `filesapi` (Jars, Upload mit Prüfsumme, `hash_mismatch`, Fakes); DB-Migration + sqlc, Fehlercodes, Audit-Aktionen; panel-Vertrag (OpenAPI, Stub-Handler `internal/plugins`, Verdrahtung, MSW-Mocks) |
| `2026-10-12-plugins-02-files.md` | 4–6 | Dienst: Upload mit Prüfsumme; `/v1/jars` mit Prüfsummen-Cache; Metadaten aus dem Jar |
| `2026-10-12-plugins-03-panel.md` | 7–12 | panel: Upstream-Clients (Modrinth, Hangar, Download, Icons); Kompatibilität + Inventar; Auflösen + Suche/Versionen; Jobs (Install/Update) + Updates; Schalter/Löschen/Icon-Proxy + Verdrahtung |
| `2026-10-12-plugins-04-frontend.md` | 13–16 | Frontend: Tab „Plugins/Mods“ mit Installiert-Liste, Entdecken + Installieren-Dialog, Job-Fortschritt + Updates + Rechte-Hinweis, Playwright |
| `2026-10-12-plugins-05-ci-docs.md` | 17–18 | CI-Abnahme in `scripts/ci-instances.sh`, Doku, Checkliste `docs/dev/acceptance-tp3c.md`, Gesamt-Review |

## Ausführung (Agent-Teams, CI statt lokaler Tests)

Es gelten die Arbeitsregeln aus `CLAUDE.md`:

- **Auf dem Mac nur `make check`** (und `make generate`, wo der Plan es sagt). Neue, noch ungetrackte Dateien vor `make check` mit `git add -N` vormerken (sonst prüft gofmt sie nicht).
- **Tests auf der Test-VM** (gilt ab PR „chore/ci-speed“; ersetzt „Tests nur in der CI“ in den Phasen-Plänen): „Run: go test …“ / „npx vitest …“ / Playwright heißt `make test-vm SUITE=go|web|e2e` (einzelne Tests per `ssh claude@192.168.1.117 'cd ~/mc-panel-test/<worktree-name> && …'`, siehe `docs/dev/README.md`, „Tests auf der Test-VM“). Vor jedem Push mindestens die betroffenen Suites, vor „ready for review“ `make test-vm` (alle). SSH braucht eine Bash ohne Sandbox. Die Root-Abnahmen (`nftables`, `instances`) laufen nur in der CI.
- **TDD:** Test zuerst; „rot“ = `make check`-Kompilierfehler bzw. roter VM-Lauf, „grün“ = grüner VM-Lauf. Die CI (`gh pr checks <n> --watch`, Job `CI OK`) ist das Gate vor dem Merge.
- **Nichts Systemnahes auf dem Mac.** Keine neuen npm-Pakete nötig. Auf der Test-VM nie deployen (`make deploy-testvm` macht nur der Lead).
- **Upstreams beim Planen/Implementieren:** Lesende `curl`-Abfragen gegen `api.modrinth.com` und `hangar.papermc.io` mit User-Agent `mc-panel/dev (https://github.com/Bernotese/mc-panel)` sind erlaubt, um `testdata/` mit echten, gekürzten Antworten zu füllen.
- **Git:** nie auf `main`; je Phase ein Branch und Draft-PR; jeder Teammate in eigenem Worktree (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`). Mergen nur durch den Nutzer.

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/plugins-contracts` | Teammate „contracts“ | 1–3 | – |
| B1 | `feat/plugins-files` | Teammate „files“ | 4–6 | A gemerged |
| B2 | `feat/plugins-panel` | Teammate „panel“ | 7–12 | A gemerged |
| B3 | `feat/plugins-frontend` | Teammate „frontend“ | 13–16 | A gemerged |
| C | `feat/plugins-ci-docs` | Teammate „ci-docs“ | 17 (18 koordiniert der Lead) | B1, B2, B3 gemerged |

Dateieigentum in Phase B (disjunkt):

- **B1:** `internal/files/**` außer `internal/files/filesapi/**`.
- **B2:** `internal/plugins/**`, `cmd/panel/**`.
- **B3:** `web/src/features/plugins/**` (neu), `web/src/features/servers/**`, `web/src/features/users/**`, Locale-Dateien unter `web/src/i18n/**`, `web/e2e/**`, `web/playwright.config.ts` (nur `testMatch` um `plugins` erweitern).
- **Nur Phase A** legt `internal/plugins/upstream/http.go` (Gerüst) an und ändert in `web/src/i18n/**` nur `errors.*`/`ERROR_CODES`; sonst ändert nur Phase A `internal/files/filesapi/**`, `internal/serverfiles/**` (nur Fakes in Tests und der Platzhalter `noFiles` in `handler.go`, der `filesapi.Client` implementiert), `internal/store/**`, `internal/httpx/errors.go`, `internal/audit/types.go`, `api/openapi.yaml`, `web/src/api/**`, `web/src/mocks/**`. Bedarf → Lead fragen (Änderung kommt per eigenem PR auf `main`, danach `git merge origin/main`).
- **Nur Phase C** ändert `scripts/**`, `Makefile`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md`.

## Global Constraints

- Alle Global Constraints aus TP1–3b und alle Konventionen aus `CLAUDE.md` gelten (`clock.Clock`, Kontext überall, Go-Tests nur Standardbibliothek, keine neuen Abhängigkeiten, Lint-Ausnahmen gezielt mit Begründung, Conventional Commits; jede Commit-Nachricht endet mit den Zeilen `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`, auch wo ein Phasen-Plan nur die erste Zeile zeigt).
- Recht: alle Plugin-Routen `rbac.Server(rbac.PermServerPlugins, "id")`; Admins immer.
- Arten: paper/purpur → `kind = plugin`, Ordner `plugins`, Quellen `modrinth` + `hangar`; fabric/neoforge → `kind = mod`, Ordner `mods`, Quelle `modrinth`; vanilla/modrinth/curseforge → `409 plugins_unsupported` (alle Routen außer `icon`).
- Modrinth-Loader: paper → `paper, spigot, bukkit`; purpur → `purpur, paper, spigot, bukkit`; fabric → `fabric`; neoforge → `neoforge`. Hangar immer Plattform `PAPER`.
- Upstream-Basis-URLs: `https://api.modrinth.com`, `https://hangar.papermc.io`; Download- und Icon-Hosts nur `https://cdn.modrinth.com/`, `https://hangarcdn.papermc.io/`; Weiterleitungen nur auf diese, nur HTTPS, höchstens 3. User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)`.
- Grenzen: API-Timeout 10 s, API-Antwort ≤ 5 MiB, Jar ≤ 256 MiB (Frist 60 s pro Lesevorgang), Icon ≤ 256 KiB (PNG/JPEG/WebP/GIF per Magic Bytes, kein SVG), Abhängigkeiten Tiefe 5 / 20 Pakete, Dienst: 500 Jars pro Liste, Prüfsummen nur für Jars ≤ 256 MiB, Metadaten-Datei ≤ 64 KiB, Zentralverzeichnis ≤ 100 000 Einträge, 2 gleichzeitige Hash-Durchläufe, Hash-Cache 4096 Einträge.
- Caches im panel: Suche 5 min, Projekte/Versionslisten 15 min, Hash-Zuordnung 1 h (auch „unbekannt“), Updates 1 h pro Server, Icons LRU 32 MiB / 24 h; je Cache ≤ 1000 Einträge (Icons nach Bytes).
- Jobs: einer pro Server (`409 conflict`), vier im panel (`429 rate_limited`), im Speicher. Rate-Limit pro Konto (Token-Bucket 1/s, Spitze 20) über `search`, `versions`, `resolve`, `updates?refresh`; Icons: 8 gleichzeitige Upstream-Abrufe im panel.
- Dateinamen: Upstream-Name muss `^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}\.jar$` erfüllen, sonst `<slug>-<version>.jar` (unzulässige Zeichen → `-`, ≤ 128 Zeichen inkl. `.jar`). `file`-Parameter der API: reiner Name ohne `/`, Endung `.jar` oder `.jar.disabled` (Groß-/Kleinschreibung egal), besteht `filesapi.CleanPath`, nicht verborgen.
- Neue panel-Fehlercodes: `plugins_unsupported` (409), `plugin_source_unavailable` (502), `plugin_not_downloadable` (409), `plugin_incompatible` (409). Neue Audit-Aktionen: `plugin.install`, `plugin.update`, `plugin.remove`, `plugin.toggle`. Neuer Dienst-Code: `hash_mismatch` (400).
- Frontend: Texte `de`/`en`, keine Inline-Skripte, keine CDNs, kein `dangerouslySetInnerHTML`, keine Markdown-/HTML-Darstellung von Upstream-Texten, Bilder nur über den Icon-Proxy, MSW-Mock für jede OpenAPI-Operation.

## Review Focus

1. **Feindliche oder kaputte Upstream-Antworten:** Download-URL auf fremdem Host, Weiterleitung auf `http` oder einen fremden Host, Dateiname mit `/`, `..`, `\` oder Steuerzeichen, `size` ≠ tatsächliche Länge, riesiges JSON, `null`-Felder; erwartet: abgelehnt bzw. Fallback-Name, nie ein Byte außerhalb von `plugins/`/`mods/`, keine Teil-Datei. → Aufgaben 7–8 (Tests mit präparierten Antworten), 11 (Job-Schritt `download_failed`).
2. **Namenskollisionen und Groß-/Kleinschreibung:** `Foo.jar` und `foo.JAR`, `x.jar` und `x.jar.disabled` gleichzeitig, Umschalten auf einen vorhandenen Zielnamen, Update auf einen Namen, den ein anderes Jar trägt; erwartet: `409 file_exists` bzw. Schritt-Fehler `file_exists`, nie wird ein fremdes Jar überschrieben oder gelöscht. → Aufgaben 5 (Liste), 11 (Jobs), 12 (Schalter).
3. **Gleichzeitige Änderungen:** Jar wird im Dateien-Tab gelöscht oder ersetzt, während ein Job läuft; zwei Browser-Tabs starten gleichzeitig Installationen; panel startet während eines Jobs neu; erwartet: genau ein Job pro Server, verwaiste DB-Einträge verschwinden beim nächsten Inventar, keine Teil-Dateien. → Aufgaben 9, 11.
4. **Upstream nicht erreichbar oder gedrosselt (Modrinth `429`) mitten im Ablauf:** erwartet: Inventar funktioniert weiter (`source_errors`), Job-Schritt `source_unavailable`, bereits installierte Schritte bleiben, klare Fehlermeldung statt Hänger. → Aufgaben 7, 9, 11.
5. **Große oder präparierte Plugin-Ordner:** > 500 Jars, Jar > 256 MiB, Jar mit Zip-Bombe in `plugin.yml`, Jar mit 100 001 Einträgen, Symlink-Jar auf eine fremde Datei, FIFO mit `.jar`-Endung; erwartet: Liste begrenzt, `meta: null` statt Fehler, keine Hänger, kein Lesen außerhalb der Instanz. → Aufgaben 5–6.

## Dateistruktur

```
internal/files/filesapi/types.go, errors.go, client.go (1)  Jars-Typen, Checksum, hash_mismatch, Client-Methoden
internal/serverfiles/helpers_test.go, cmd/panel/fakefiles_test.go (1) Fakes um Jars/UploadChecked ergänzt
internal/store/migrations/00005_plugins.sql             (2)  plugin_installs
internal/store/queries/plugins.sql → plugins.sql.go     (2)  Plugins…-Queries
internal/httpx/errors.go, internal/audit/types.go       (2)  Codes, Aktionen
api/openapi.yaml, web/src/api/schema.d.ts, types.ts     (3)  Vertrag
internal/plugins/handler.go                             (3)  Deps, Routes (Stubs), ab B2 echt
cmd/panel/app.go                                        (3)  Verdrahtung (gemeinsamer Katalog, Run)
web/src/mocks/handlers/plugins.ts, db.ts                (3)  Mocks
internal/files/transfer.go, server.go                   (4)  Upload mit Prüfsumme
internal/files/jars.go, hashcache.go                    (5)  /v1/jars, Prüfsummen-Cache
internal/files/jarmeta.go                               (6)  Metadaten aus dem Jar
internal/plugins/upstream/http.go, cache.go             (7)  HTTP-Basis, Allowlist, Cache
internal/plugins/upstream/modrinth.go                   (7)  Modrinth
internal/plugins/upstream/hangar.go, download.go, icon.go (8) Hangar, Jar-Download, Icon-Abruf
internal/plugins/compat.go, inventory.go, restart.go    (9)  Arten, Spielversion, Inventar, Neustart-Merker
internal/plugins/resolve.go, search.go, ratelimit.go    (10) Auflösen, Suche, Versionen, Rate-Limit
internal/plugins/jobs.go, install.go, updates.go        (11) Jobs, Install/Update, Updatecheck
internal/plugins/manage.go, icon.go                     (12) Schalter, Löschen, Icon-Proxy; cmd/panel Integration
web/src/features/plugins/**                             (13–15) Tab, Liste, Entdecken, Dialog, Job
web/src/features/servers/server-detail-page.tsx, users/** (15) Tab, Rechte-Hinweis
web/e2e/plugins.spec.ts                                 (16) Playwright
scripts/ci-instances.sh                                 (17) CI-Abnahme
docs/dev/acceptance-tp3c.md, docs/dev/README.md, CLAUDE.md, README.md, Specs (18)
```

## Verträge (verbindlich)

### V1 – `filesapi` (Aufgabe 1, Paket `github.com/Bernotese/mc-panel/internal/files/filesapi`)

```go
const (
	PathJars = "/v1/jars" // POST {id, dir}

	DirPlugins = "plugins"
	DirMods    = "mods"

	MaxJars         = 500
	MaxJarHashBytes = 256 << 20 // larger jars come without checksums

	CodeHashMismatch = "hash_mismatch" // 400
)

var ErrHashMismatch = errors.New(CodeHashMismatch)

type JarsRequest struct {
	ID  int64  `json:"id"`
	Dir string `json:"dir"` // DirPlugins | DirMods
}

type JarKind string // "plugin" | "mod"
const (
	JarKindPlugin JarKind = "plugin"
	JarKindMod    JarKind = "mod"
)

type JarMeta struct {
	Kind    JarKind `json:"kind"`
	ID      string  `json:"id"`      // fabric/neoforge mod id; "" for plugins
	Name    string  `json:"name"`
	Version string  `json:"version"`
}

type Jar struct {
	Name    string   `json:"name"`    // base name incl. ".jar" / ".jar.disabled"
	Enabled bool     `json:"enabled"` // false for ".jar.disabled"
	Size    int64    `json:"size"`
	MTime   int64    `json:"mtime"`   // Unix ms
	SHA256  string   `json:"sha256"`  // lowercase hex; "" above MaxJarHashBytes
	SHA512  string   `json:"sha512"`  // lowercase hex; "" above MaxJarHashBytes
	Meta    *JarMeta `json:"meta"`    // nil = not readable
}

type JarsResponse struct {
	Jars      []Jar `json:"jars"` // never nil; sorted by name (byte order)
	Truncated bool  `json:"truncated"`
}

// Checksum is the expected digest of an upload.
type Checksum struct {
	Algo string // "sha256" | "sha512"
	Hex  string // lowercase hex, 64 resp. 128 characters
}

// UploadResponse gains the computed digests (only filled when a Checksum was sent).
type UploadResponse struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
}

// Client gains two methods (implemented by *UnixClient):
//	Jars(ctx context.Context, id int64, dir string) (JarsResponse, error)
//	UploadChecked(ctx context.Context, id int64, path string, overwrite bool, size int64, sum Checksum, body io.Reader) (UploadResponse, error)
```

- `UploadChecked` sendet `PUT /v1/upload` mit dem zusätzlichen Query-Parameter `sha256=<hex>` bzw. `sha512=<hex>` (ungültige `Checksum` → `ErrInvalid` ohne Aufruf); sonst wie `Upload`. Antwort `400 {code: hash_mismatch}` → `ErrHashMismatch`.
- `Jars`: Client-Timeout 2 min; `dir` ∉ {`plugins`, `mods`} → `ErrInvalid` ohne Aufruf.
- Der Dienst nimmt bei `upload` höchstens einen der beiden Parameter an (beide → `400 invalid`, falsches Format → `400 invalid`).
- Die Fakes in `internal/serverfiles/helpers_test.go` und `cmd/panel/fakefiles_test.go` bekommen beide Methoden (Phase A: `Jars` liefert eine leere Liste bzw. die `.jar`-Dateien des flachen Fake-Baums, `UploadChecked` prüft die Prüfsumme echt).

### V2 – Daten (Aufgabe 2)

Migration `internal/store/migrations/00005_plugins.sql` (goose, `-- +goose Up`/`Down`) mit der Tabelle aus Spec §6.1 (wortgleich). Queries `internal/store/queries/plugins.sql`:

```sql
-- name: PluginsListInstalls :many
SELECT * FROM plugin_installs WHERE server_id = ? ORDER BY filename;

-- name: PluginsUpsertInstall :exec
INSERT INTO plugin_installs (server_id, filename, sha512, source, project_id, project_slug, title,
                             version_id, version_name, channel, installed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, filename) DO UPDATE SET
    sha512 = excluded.sha512, source = excluded.source, project_id = excluded.project_id,
    project_slug = excluded.project_slug, title = excluded.title, version_id = excluded.version_id,
    version_name = excluded.version_name, channel = excluded.channel, installed_at = excluded.installed_at;

-- name: PluginsDeleteInstall :exec
DELETE FROM plugin_installs WHERE server_id = ? AND filename = ?;
```

Generierter Typ `store.PluginInstall` mit den Feldern `ServerID int64, Filename, Sha512, Source, ProjectID, ProjectSlug, Title, VersionID, VersionName, Channel string, InstalledAt int64` (exakte Feldnamen nach sqlc-Generierung; Phase A committet `plugins.sql.go`).

`internal/httpx/errors.go`: `CodePluginsUnsupported = "plugins_unsupported"`, `CodePluginSourceUnavailable = "plugin_source_unavailable"`, `CodePluginNotDownloadable = "plugin_not_downloadable"`, `CodePluginIncompatible = "plugin_incompatible"`. `internal/audit/types.go`: `ActionPluginInstall = "plugin.install"`, `ActionPluginUpdate = "plugin.update"`, `ActionPluginRemove = "plugin.remove"`, `ActionPluginToggle = "plugin.toggle"`.

### V3 – panel-API (Aufgabe 3, `api/openapi.yaml`, Tag `plugins`)

Alle Pfade mit Pfad-Parameter `id` (int64). Jede Operation kann zusätzlich `401` und `403`; außer `getPluginIcon` alle zusätzlich `404 not_found` und `409 plugins_unsupported`.

| Pfad | Methode | operationId | Request | Antworten |
|---|---|---|---|---|
| `/servers/{id}/plugins` | get | `listPlugins` | – | `200 PluginInventory`, 503 (`files_unavailable`) |
| `/servers/{id}/plugins/search` | get | `searchPlugins` | Query `source` (PluginSource, Pflicht), `q` (string ≤ 100, optional), `offset` (int 0–1000, optional) | `200 PluginSearchResult`, 400, 429, 502 (`plugin_source_unavailable`, `catalog_unavailable`) |
| `/servers/{id}/plugins/versions` | get | `listPluginVersions` | Query `source`, `project` (Pflicht) | `200 PluginVersionList`, 400, 429, 502 |
| `/servers/{id}/plugins/resolve` | get | `resolvePlugin` | Query `source`, `project` (Pflicht), `version` (optional) | `200 PluginResolution`, 400, 409 (`plugin_incompatible`, `plugin_not_downloadable`), 429, 502, 503 |
| `/servers/{id}/plugins/install` | post | `installPlugin` | `InstallPluginRequest` | `202 PluginJob`, 400, 409 (`conflict`, `plugin_incompatible`, `plugin_not_downloadable`, `file_exists`), 429, 502, 503 |
| `/servers/{id}/plugins/updates` | get | `getPluginUpdates` | Query `refresh` (boolean, optional) | `200 PluginUpdates`, 429, 502, 503 |
| `/servers/{id}/plugins/update` | post | `updatePlugins` | `UpdatePluginsRequest` | `202 PluginJob`, 400, 409 (`conflict`), 429, 502, 503 |
| `/servers/{id}/plugins/toggle` | post | `togglePlugin` | `TogglePluginRequest` | `204`, 400, 409 (`file_exists`, `conflict`), 503 |
| `/servers/{id}/plugins/delete` | post | `deletePlugin` | `PluginFileRequest` | `204`, 400, 409 (`conflict`), 503 |
| `/servers/{id}/plugins/jobs` | get | `getPluginJob` | – | `200 PluginJobResponse` |
| `/servers/{id}/plugins/icon` | get | `getPluginIcon` | Query `url` (Pflicht) | `200` `image/png\|image/jpeg\|image/webp\|image/gif` (binary), 400, 404, 502 |

- `resolve`: Abhängigkeiten ohne kompatible Version stehen in `incompatible` (`200`); ist das **angefragte** Projekt nicht auflösbar → `409 plugin_incompatible` bzw. bei nur extern verfügbarer Version `409 plugin_not_downloadable`. `install` antwortet `409 plugin_incompatible`, sobald `incompatible` nicht leer wäre.
- `toggle`/`delete` während eines laufenden Jobs des Servers → `409 conflict`. Datei fehlt → `404 not_found`.
- Präzisierungen aus Plan 03 (B2), mit Plan 01 (Mock, OpenAPI-Beschreibungen) und Plan 04 abgeglichen:
  - Upstream-`404` (Projekt/Version unbekannt) → `404 not_found` bei `versions`, `resolve`, `install` (statt `502`); als Abhängigkeit → `incompatible` (`no_compatible_version`).
  - `resolve` ohne `version` oder mit der installierten Version eines installierten Projekts → nur `already`; `install` ohne etwas zu tun → `409 conflict` („already installed“). Das Frontend zeigt dafür bei `install` einen eigenen Text (`plugins.error.install_conflict`).
  - `update` mit einer Datei ohne verfügbares Update oder ganz ohne Updates → `400 validation_failed`.
  - `catalog_unavailable` (`502`) bei `search`, `versions`, `resolve`, `install`, `updates`, `update`; `getPluginUpdates` kennt kein `400` (`refresh` gilt bei `1`/`true`, sonst `false`).
  - Audit-Details: `plugin.install` `{source, project: <Projekt-ID>, version: <Versions-ID>, file}`, `plugin.update` `{file: <neuer Dateiname>, from: <alter Versionsname>, to: <neuer Versionsname>}`, `plugin.toggle` `{file: <angefragter Name>, enabled}`, `plugin.remove` `{file}`.

Schemas (alle `additionalProperties: false` bei Request-Bodies; Zeitpunkte Unix-ms als `integer` `int64`; nullable als `type: [X, 'null']` wie im übrigen Vertrag):

```
PluginSource      enum modrinth | hangar
PluginKind        enum plugin | mod
PluginChannel     enum release | beta | alpha
PluginProjectRef  {id, slug, title, page_url: string; icon_url: string|null}
PluginVersionRef  {id, name: string; channel: PluginChannel}
PluginJarMeta     {kind: PluginKind; id, name, version: string}
InstalledJar      {file: string; enabled: boolean; size, mtime: integer;
                   source: enum modrinth|hangar|manual; project: PluginProjectRef|null;
                   version: PluginVersionRef|null; meta: PluginJarMeta|null; installed_by_panel: boolean}
PluginInventory   {kind: PluginKind; dir: enum plugins|mods; game_version: string|null;
                   sources: PluginSource[]; jars: InstalledJar[]; truncated, restart_needed: boolean;
                   source_errors: PluginSource[]}
PluginSearchHit   {source: PluginSource; project_id, slug, title, description, author: string;
                   downloads: integer; icon_url: string|null; page_url: string; installed: boolean}
PluginSearchResult {hits: PluginSearchHit[]; total, offset: integer}
PluginVersion     {id, name: string; channel: PluginChannel; published_at: integer;
                   downloadable: boolean; external_url: string|null}
PluginVersionList {versions: PluginVersion[]}
PluginPlanEntry   {source: PluginSource; project_id, slug, title, version_id, version_name: string;
                   channel: PluginChannel; filename: string; size: integer; required_by: string|null}
PluginAlready     {source: PluginSource; project_id, title, file: string}
PluginExternal    {name, url: string}
PluginIncompatible {title: string; reason: enum no_compatible_version|client_only|not_downloadable|too_deep|too_many}
PluginResolution  {install: PluginPlanEntry[]; already: PluginAlready[]; external: PluginExternal[];
                   incompatible: PluginIncompatible[]}
InstallPluginRequest {source: PluginSource; project: string; version?: string|null}
PluginUpdate      {file: string; from: PluginVersionRef; to: PluginVersionRef}
PluginUpdates     {updates: PluginUpdate[]; checked_at: integer}
UpdatePluginsRequest {files: string[]}           (leer = alle mit Update)
TogglePluginRequest {file: string; enabled: boolean}
PluginFileRequest {file: string}
PluginJobStep     {title, file: string; state: enum pending|downloading|done|failed|skipped;
                   error_code: enum download_failed|hash_mismatch|file_exists|insufficient_storage|files_unavailable|source_unavailable | null;
                   error: string}
PluginJob         {id: string; kind: enum install|update; state: enum running|done|failed;
                   steps: PluginJobStep[]; started_at, finished_at: integer}   (finished_at 0 solange running)
PluginJobResponse {job: PluginJob|null}
```

- `required_by` = Titel des Plans-Eintrags, der die Abhängigkeit verlangt; `null` für das angefragte Projekt. `file` in `PluginJobStep` = Zieldateiname (bei Update der neue Name).
- `icon_url` in Antworten ist bereits die Proxy-URL `/api/servers/{id}/plugins/icon?url=<url-kodiert>` oder `null`.
- Error-Enum um die vier Codes aus V2 erweitert.

### V4 – panel-intern (Aufgaben 3, 7–12)

```go
package plugins // github.com/Bernotese/mc-panel/internal/plugins

// Catalog resolves LATEST to the newest version of an instance type (implemented by
// *catalog.Client from catalog.New; same method set as servers.CatalogSource).
type Catalog interface {
	Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error)
}

type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Files     filesapi.Client          // nil = no service: inventory/jobs/toggle/delete answer 503 files_unavailable
	Instances agentapi.InstancesClient // actual state for restart_needed; nil = treat as not running
	Catalog   Catalog                  // nil = catalog.New with the default upstreams
	Upstream  *upstream.Client         // nil = upstream.New(upstream.Options{Clock, Version})
	Version   string                   // panel version for the User-Agent; "" = "dev"
	Log       *slog.Logger             // nil = slog.Default()
}
func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
// Run blocks until ctx is done; then it cancels running jobs and waits for them.
func (h *Handler) Run(ctx context.Context)
```

```go
package upstream // github.com/Bernotese/mc-panel/internal/plugins/upstream

type Bases struct{ Modrinth, Hangar string }           // scheme+host, no trailing slash
var DefaultBases = Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}
// AllowedHosts are the only hosts jars and icons are fetched from (https only).
var AllowedHosts = []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}
type Options struct {
	Clock      clock.Clock
	Version    string
	Bases      Bases        // zero = DefaultBases
	HTTPClient *http.Client // nil = default; tests inject httptest TLS clients
	// AllowHost overrides the download/icon host check in tests (nil = AllowedHosts, https only).
	AllowHost func(u *url.URL) bool
}
var ErrUnavailable = errors.New("upstream: unavailable") // network, status, size, JSON, 429
func New(o Options) *Client
```

Die übrigen Typen und Methoden von `upstream` und die internen Dateien von `plugins` legt Phase B2 in ihrem Plan fest (nur B2 nutzt sie).

Phase A registriert alle elf Routen mit `rbac.Server(rbac.PermServerPlugins, "id")`; bis B2 antworten sie `502 plugin_source_unavailable` („plugins not implemented yet“), `Run` kehrt sofort nach `<-ctx.Done()` zurück. `cmd/panel/app.go` erzeugt den Katalog einmal (`cat := catalog.New(...)`) und gibt ihn `servers` und `plugins`; `pluginsH.Run` läuft in `run` mit.

### V5 – MSW-Mock (Aufgabe 3, `web/src/mocks/handlers/plugins.ts`)

- Pro Server Inventar im Speicher (`db.plugins: Record<serverId, MockInventory>`): Paper-Server „survival“ mit drei Jars: `LuckPerms-Bukkit-5.5.71.jar` (Modrinth, `installed_by_panel: true`, Update auf `5.5.72` verfügbar), `ViaVersion-5.5.1.jar` (Hangar, panel), `OldTool.jar` (manuell, `meta: {kind: plugin, id: '', name: 'OldTool', version: '1.0'}`), dazu `Disabled.jar.disabled` (Modrinth, deaktiviert); ein Fabric-Server mit `fabric-api-0.110.0.jar`; vanilla-Server → `409 plugins_unsupported`.
- Katalog: Modrinth `luckperms`, `chunky`, `bluemap`; Hangar `ViaBackwards` (Pflicht-Abhängigkeit `ViaVersion`), `Essentials` (nur extern, `external_url` gesetzt), `Chunky`; Mods `sodium-extra` erscheint nie (Client-only), `lithium`, `fabric-api`, `servercore` (braucht `fabric-api`). Versionen je Projekt mit einer `beta`.
- Suche mit `q` (Teilstring, case-insensitiv), `offset`, 20 pro Seite; `installed` aus dem Inventar.
- `install`/`update` erzeugen einen Job, der über ~4 s Schritt für Schritt `downloading` → `done` geht und dann das Inventar ändert; ein zweiter Job → `409 conflict`. `toggle` benennt um, `delete` entfernt.
- `icon`: kleines eingebettetes PNG (Uint8Array) mit `Content-Type: image/png`.
- Rechte über `serverPermissions` (`server.plugins`) wie die übrigen Server-Mocks.
