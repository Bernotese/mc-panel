# Teilprojekt 3c – panel (Aufgaben 7–12) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das panel bekommt das Paket `internal/plugins` mit dem Unterpaket `internal/plugins/upstream`: Suche in Modrinth und Hangar, Inventar aller Jars (auch manuell hochgeladener), Auflösen von Pflicht-Abhängigkeiten, Install- und Update-Jobs, Updatecheck, Schalter aktiv/inaktiv, Löschen und Icon-Proxy. Jars kommen nur von `cdn.modrinth.com`/`hangarcdn.papermc.io` und gehen als Stream mit Prüfsumme an den Dienst `mcpanel-files`.

**Architecture:** `upstream.Client` kapselt beide APIs (feste Basis-URLs, Timeout 10 s, ≤ 5 MiB, Weiterleitungen nur same-host/https), Downloads und Icons (nur `AllowedHosts`, Weiterleitungen nur dorthin, höchstens 3) und die Caches (`upstream.Cache[V]`: TTL, geteilte Aufrufe, ≤ 1000 Einträge). `plugins.Handler` besitzt die elf Routen aus V3: Jede Route lädt den Server (ohne Instanz `404`, Art ohne Plugins `409 plugins_unsupported`), baut bei Bedarf das Inventar (Dienst `/v1/jars` → DB → Modrinth-Hash → manuell), löst über den `planner` auf und startet Jobs in einer Registry im Speicher (einer pro Server, vier gesamt). Jobs laufen mit `context.WithoutCancel(r.Context())` (Akteur und IP bleiben für das Audit erhalten) und werden von `Run` beim Beenden abgebrochen.

**Tech Stack:** Go (Standardbibliothek, chi, sqlc/`store`, `golang.org/x/time/rate`, `container/list`), `httptest` mit TLS für die Upstream-Fakes. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` (§3, §4, §6). Verträge V1–V4, Global Constraints, Review Focus und Dateieigentum B2: `docs/superpowers/plans/2026-10-12-plugins-00-overview.md`.

## Ausführung dieser Phase

- **Branch/Worktree:** `git fetch origin && git worktree add ../mc-panel-wt/feat-plugins-panel -b feat/plugins-panel origin/main` (erst nachdem Phase A gemerged ist). Alle Befehle laufen im Worktree.
- **Draft-PR** gleich nach dem ersten Commit: `gh pr create --draft --base main --head feat/plugins-panel --title "feat(plugins): panel side of plugins and mods (sub-project 3c)" --body "<Inhalt, endet mit der Attributionszeile aus CLAUDE.md>"`. Nach jedem Push: `gh pr checks <n> --watch`, Fehler aus `gh run view <id> --log-failed` beheben.
- **Lokal nur `make check`** (CLAUDE.md). Neue Dateien vor `make check` mit `git add -N` vormerken. „Rot“ heißt hier: `make check` scheitert am Kompilieren der Tests; „Grün“ heißt: der CI-Job `go test` ist grün (Expected: PASS in den CI-Logs). Kein `make test`, kein `go test` lokal.
- **Formatierung:** Die Codeblöcke sind inhaltlich verbindlich; Ausrichtung und Importgruppen erledigt `gofmt -w` bzw. `golangci-lint fmt ./internal/plugins/... ./cmd/panel/...` vor `make check`.
- **Dateieigentum:** nur `internal/plugins/**` und `cmd/panel/**`. Fehlt etwas aus Phase A (`filesapi`, `store`, `httpx`, `audit`, `api/openapi.yaml`), den Lead fragen; nicht selbst ändern.
- **Upstreams:** Die `testdata/`-Dateien aus Aufgabe 7/8 sind echte, gekürzte Antworten vom 2026-10-12 (UA `mc-panel/dev (https://github.com/Bernotese/mc-panel)`). Wer sie erneuern will, darf lesend `curl` gegen `api.modrinth.com`/`hangar.papermc.io` nutzen.
- **Commits:** Conventional Commits, eine Aufgabe = mindestens ein Commit, Nachricht endet mit
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  ```

## Was Phase A liefert (Annahmen, in Step 0 von Aufgabe 7 geprüft)

- `internal/files/filesapi`: V1 plus (laut Plan 01, additiv) `AlgoSHA256 = "sha256"`, `AlgoSHA512 = "sha512"`, `(Checksum).Validate() error`, `(Checksum).Matches(sha256Hex, sha512Hex string) bool`, `ValidJarDir(dir string) bool`, `IsJarName(name string) (enabled, ok bool)` (Endung `.jar` bzw. `.jar.disabled`, Groß-/Kleinschreibung egal, mindestens ein Zeichen davor), `JarsTimeout`. `filesapi.Client` hat `Jars` und `UploadChecked`; die Fakes in `internal/serverfiles/helpers_test.go` und `cmd/panel/fakefiles_test.go` implementieren beide.
- `store`: `PluginInstall`, `PluginsListInstalls(ctx, serverID int64) ([]PluginInstall, error)`, `PluginsUpsertInstall(ctx, PluginsUpsertInstallParams) error`, `PluginsDeleteInstall(ctx, PluginsDeleteInstallParams{ServerID, Filename}) error`.
- `httpx.CodePluginsUnsupported`, `CodePluginSourceUnavailable`, `CodePluginNotDownloadable`, `CodePluginIncompatible`; `audit.ActionPluginInstall`, `ActionPluginUpdate`, `ActionPluginRemove`, `ActionPluginToggle`.
- `internal/plugins/handler.go` (Stub: `Catalog`, `Deps`, `Handler`, `NewHandler`, `Routes` mit elf Routen auf `h.notImplemented` → `502 plugin_source_unavailable` „plugins not implemented yet“, `Run`), `internal/plugins/routes_test.go` (`wantRoutes`, `TestRouteContract`, `TestStubsAnswerNotImplemented`, `TestRoutesNeedServerPlugins`, `TestRunReturnsWhenCancelled`).
- `internal/plugins/upstream/http.go` (+ `http_test.go`): `Bases`, `DefaultBases`, `AllowedHosts`, `Options`, `ErrUnavailable`, `Client`, `New`, `(*Client).UserAgent()`, `(*Client).Bases()`, `(*Client).HostAllowed(u)`, `DefaultHostAllowed(u)`. Diese Phase ersetzt beide Dateien vollständig.
- `cmd/panel/app.go`: `appDeps.Upstream *upstream.Client`, `cat := catalog.New(...)` für `servers` und `plugins`, `pluginsH := plugins.NewHandler(plugins.Deps{DB, Clock, Audit: auditRec, Files: d.Files, Instances: d.Instances, Catalog: cat, Upstream: d.Upstream, Version: d.Version})`, `pluginsH.Routes(api)` nach `filesH.Routes(api)`, `pluginsH.Run(ctx)` in `run` (`wg.Add(5)`). `TestRouteCoverage` zählt 87 Routen.

**Weichen Namen aus Phase A ab, gelten die Namen aus Phase A; die Werte (Codes, Aktionen, Pfade) aus V1–V4 sind verbindlich.**

## Festlegungen dieses Teilplans (interne Typen und Signaturen)

Einmal zentral festgelegt und in allen Aufgaben genau so verwendet.

### Paket `upstream` (`github.com/Bernotese/mc-panel/internal/plugins/upstream`)

```go
const (
	APITimeout    = 10 * time.Second // per API request (spec §4)
	ReadTimeout   = 60 * time.Second // per read of a jar or icon body
	MaxJSONBytes  = 5 << 20
	MaxJarBytes   = 256 << 20
	MaxIconBytes  = 256 << 10
	MaxIconURLLen = 512
	MaxRedirects  = 3
	PageSize      = 20 // search hits per page
	MaxVersions   = 25 // versions per list
	MaxHashBatch  = 500
	MaxCacheEntries = 1000
	MaxFileNameLen  = 128 // fallback file name incl. ".jar"
)

type Source string  // Modrinth = "modrinth", Hangar = "hangar"; func (s Source) Valid() bool
type Channel string // Release = "release", Beta = "beta", Alpha = "alpha"

type Bases struct{ Modrinth, Hangar string }
var DefaultBases = Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}
var AllowedHosts = []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}

type Options struct {
	Clock       clock.Clock
	Version     string
	Bases       Bases
	HTTPClient  *http.Client           // only its Transport is used (tests: httptest TLS)
	AllowHost   func(u *url.URL) bool  // nil = DefaultHostAllowed
	APITimeout  time.Duration          // 0 = APITimeout (tests shorten it)
	ReadTimeout time.Duration          // 0 = ReadTimeout (tests shorten it)
}

var (
	ErrUnavailable    = errors.New("upstream: unavailable") // network, status, size, JSON, 429
	ErrNotFound       = errors.New("upstream: not found")   // 404 of a project or version
	ErrDownload       = errors.New("upstream: download failed")
	ErrHostNotAllowed = errors.New("upstream: host not allowed")
	ErrNotImage       = errors.New("upstream: not a supported image")
)

func New(o Options) *Client
func DefaultHostAllowed(u *url.URL) bool
func (c *Client) UserAgent() string
func (c *Client) Bases() Bases
func (c *Client) HostAllowed(u *url.URL) bool
func (c *Client) IconURLAllowed(raw string) bool

type SearchQuery struct {
	Query          string
	Offset         int
	ProjectType    string   // Modrinth project_type facet: "plugin" | "mod"
	Loaders        []string // Modrinth categories facet (OR)
	GameVersion    string
	ServerSideOnly bool     // Modrinth: server_side required OR optional (mods)
}
type SearchHit struct {
	ProjectID, Slug, Owner, Title, Description, Author, IconURL string
	Downloads int64
}
type SearchResult struct {
	Hits  []SearchHit // never nil
	Total int
}
type Project struct {
	Source                                          Source
	ID, Slug, Owner, Title, Description, IconURL    string // Hangar: ID is the numeric id as text
	ServerSide                                      string // Modrinth server_side; "" for Hangar
}
func (p Project) ClientOnly() bool // ServerSide == "unsupported"
type File struct {
	URL, Name      string
	Size           int64
	SHA512, SHA256 string // lowercase hex; Modrinth: SHA512, Hangar: SHA256
}
type Dependency struct {
	ProjectID   string // "" = not a project (external link or Modrinth file name only)
	VersionID   string // Modrinth fixed version; "" = newest compatible
	Name        string // Hangar dependency name or Modrinth file_name
	ExternalURL string // Hangar externalUrl
}
type Version struct {
	Source       Source
	ID           string // Modrinth version id, Hangar version name
	ProjectID    string
	Name         string // Modrinth version_number, Hangar name
	Channel      Channel
	PublishedAt  time.Time
	GameVersions []string // Modrinth game_versions, Hangar platformDependencies.PAPER
	Loaders      []string // Modrinth only
	File         *File    // nil = no downloadable jar
	ExternalURL  string   // Hangar: only externally available
	Dependencies []Dependency // required ones only
}
func (v Version) Downloadable() bool
type VersionFilter struct {
	Loaders     []string // Modrinth only
	GameVersion string
	ReleaseOnly bool     // Hangar: channel=Release; Modrinth: filtered locally
}
type Icon struct {
	Data        []byte
	ContentType string // image/png | image/jpeg | image/webp | image/gif
}

func (c *Client) Search(ctx context.Context, src Source, q SearchQuery) (SearchResult, error)
func (c *Client) Project(ctx context.Context, src Source, id string) (Project, error)
func (c *Client) Versions(ctx context.Context, src Source, project string, f VersionFilter) ([]Version, error) // newest first, ≤ MaxVersions
func (c *Client) Version(ctx context.Context, src Source, project, id string) (Version, error)
func (c *Client) VersionsByHash(ctx context.Context, sha512s []string) (map[string]Version, error) // Modrinth
func (c *Client) LatestByHash(ctx context.Context, sha512s, loaders, gameVersions []string, types []Channel) (map[string]Version, error) // Modrinth, uncached
func (c *Client) Download(ctx context.Context, f File) (io.ReadCloser, error)
func (c *Client) Icon(ctx context.Context, raw string) (Icon, error)

type Cache[V any] struct{ /* … */ }
func NewCache[V any](clk clock.Clock, ttl time.Duration, maxEntries int) *Cache[V] // ttl 0 = only shares calls
func (c *Cache[V]) Get(ctx context.Context, key string, fetch func(context.Context) (V, error)) (V, error)
func (c *Cache[V]) Peek(key string) (V, bool)
func (c *Cache[V]) Put(key string, v V)
func (c *Cache[V]) Delete(key string)
func (c *Cache[V]) Len() int

func DetectImage(b []byte) string               // "" = not PNG/JPEG/WebP/GIF
func ValidJarName(name string) bool             // spec §4 rule + filesapi.CleanPath
func FileName(upstreamName, slug, version string) string
```

Caches im `Client`: Suche 5 min, Projekte 15 min, Versionslisten 15 min, Einzelversionen 15 min, Hash-Zuordnung 1 h (auch „unbekannt“), dazu ein Cache mit TTL 0, der gleichzeitige gleiche Hash-Anfragen teilt.

### Paket `plugins` (`github.com/Bernotese/mc-panel/internal/plugins`)

```go
type Catalog interface { Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error) } // V4
type Deps struct { DB; Clock; Audit; Files; Instances; Catalog; Upstream; Version; Log }          // V4, exakt
func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
func (h *Handler) Run(ctx context.Context)
func ErrorStatus(err error) (status int, code string) // for the contract test in cmd/panel

// handler.go
type server struct { ID int64; Name string; Type instancespec.Type; Version string; Kind kindInfo }
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request, requireKind bool) (server, bool)
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any)
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error)
var errCatalog, errIncompatible, errNotDownloadable, errAlreadyInstalled, errFileExists,
	errJobRunning, errTooManyJobs, errShuttingDown, errNoChecksum error

// compat.go
type kindInfo struct { Kind, Dir, ProjectType string; Loaders []string; Sources []upstream.Source }
func kindOf(t instancespec.Type) (kindInfo, bool)
func (k kindInfo) hasSource(s upstream.Source) bool
func (k kindInfo) isMod() bool
func pageURL(src upstream.Source, kind, owner, slug string) string
func compatible(v upstream.Version, k kindInfo, gv string) bool
func (h *Handler) gameVersion(ctx context.Context, srv server) (string, error) // errCatalog
func (h *Handler) iconURL(serverID int64, raw string) *string

// inventory.go
type projectRef struct { ID, Slug, Title, PageURL string; IconURL *string }
type versionRef struct { ID, Name string; Channel upstream.Channel }
type installedJar struct { File string; Enabled bool; Size, MTime int64; Source string; Project *projectRef;
	Version *versionRef; Meta *filesapi.JarMeta; InstalledByPanel bool; base, sha512, owner string }
type inventory struct { Kind, Dir string; GameVersion *string; Sources []upstream.Source; Jars []installedJar;
	Truncated, RestartNeeded bool; SourceErrors []upstream.Source }
func (h *Handler) inventory(ctx context.Context, srv server) (inventory, error)
func installedByProject(inv inventory) map[string]installedJar // key projectKey(src, id)
func projectKey(src upstream.Source, id string) string

// restart.go
type restartTracker struct { … }
func (h *Handler) markChanged(serverID int64)
func (h *Handler) restartNeeded(ctx context.Context, serverID int64) bool

// resolve.go
type planEntry struct { Source upstream.Source; ProjectID, Slug, Title, VersionID, VersionName string;
	Channel upstream.Channel; Filename string; Size int64; RequiredBy *string;
	file upstream.File; replaces string; replacesEnabled bool }
type resolution struct { Install []planEntry; Already []alreadyEntry; External []externalEntry; Incompatible []incompatibleEntry }
type resolveRequest struct { Source upstream.Source; Project, Version string }
type planner struct { up *upstream.Client; kind kindInfo; gv string; installed map[string]installedJar; seen map[string]bool }
func (h *Handler) resolve(ctx context.Context, srv server, gv string, inv inventory, req resolveRequest) (resolution, error)
func (p *planner) dependencies(ctx context.Context, res *resolution, v upstream.Version, src upstream.Source, parent string) ([]planEntry, error)

// ratelimit.go
type userLimiter struct { … }; func (h *Handler) allow(w http.ResponseWriter, r *http.Request) bool

// jobs.go
type jobStep struct { Title, File string; State stepState; ErrorCode *string; Error string }
type jobView struct { ID, Kind, State string; Steps []jobStep; StartedAt, FinishedAt int64 }
type job struct { … }; func (j *job) set(i int, s stepState); func (j *job) fail(i int, code, msg string)
type jobRegistry struct { … }
func (r *jobRegistry) start(serverID int64, kind string, base context.Context, steps []jobStep, run func(ctx context.Context, j *job)) (jobView, error)
func (r *jobRegistry) get(serverID int64) *jobView
func (r *jobRegistry) running(serverID int64) bool
func (r *jobRegistry) shutdown()

// install.go
type jobTask struct { entry planEntry; group int; from *versionRef; err *taskError }
func (h *Handler) runJob(ctx context.Context, j *job, srv server, tasks []jobTask, stopOnError bool)
func (h *Handler) installTask(ctx context.Context, srv server, t jobTask) error
func stepError(err error) (code, msg string)

// updates.go
type updateEntry struct { File string; From, To versionRef; jar installedJar; source upstream.Source }
type updatesResult struct { Updates []updateEntry; CheckedAt int64 }
func (h *Handler) checkUpdates(ctx context.Context, srv server, gv string, refresh bool) (updatesResult, error)

// manage.go
func cleanJarFile(name string) (base string, enabled, ok bool)
// icon.go
type iconCache struct { … }
```

## Abweichungen und Präzisierungen (begründet)

1. **`upstream.ErrNotFound` → `404 not_found`:** Spec §4 bildet jeden Status ≠ 2xx auf `502` ab. Ein unbekanntes Projekt oder eine unbekannte Version (`404` des Upstreams) ist aber kein Ausfall der Quelle; `resolve`, `versions` und `install` antworten dann `404 not_found` („project or version not found“), was jede Plugin-Operation laut V3 deklariert. Als Abhängigkeit gilt ein unbekanntes Projekt als `incompatible` (`no_compatible_version`).
2. **Hangar-Versionsliste mit `channel=Release`:** Für „neueste Version im Kanal Release“ fragt das panel `…/versions?platform=PAPER&platformVersion=<gv>&limit=25&channel=Release` (geprüft am 2026-10-12: ViaBackwards hat unter den neuesten 25 Versionen nur eine Release). Ohne den Filter fände die Auswahl bei Projekten mit vielen Snapshots oft keine Release.
3. **Versionsname bei Modrinth = `version_number`** (z. B. `v5.5.71-bukkit`), nicht `name` (`v5.5.71 (Bukkit)`): kurz, eindeutig und für den Fallback-Dateinamen geeignet.
4. **Versionswechsel des angefragten Projekts** (Absprache mit Plan 04): Ist das angefragte Projekt installiert und `version` explizit angegeben und verschieden von der installierten, steht es in `install` (nicht `already`). Der Install-Job ersetzt dann wie ein Update (neuer Name, `overwrite` bei gleichem Namen, deaktiviert bleibt deaktiviert, altes Jar und alter DB-Eintrag weg), Audit `plugin.install`. Ohne `version` oder bei gleicher Version → `already` (Spec §6.3).
5. **Install, wenn nichts zu tun ist:** Ist das angefragte Projekt schon installiert (Fall `already`), antwortet `install` `409 conflict` („already installed“).
6. **Update-Abhängigkeiten werden vor dem Job aufgelöst:** `POST …/update` löst die Pflicht-Abhängigkeiten jeder gewählten neuen Version synchron auf; die Job-Schritte stehen damit von Anfang an fest (Abhängigkeiten direkt vor ihrem Update). Scheitert die Auflösung eines Updates (Upstream weg, Abhängigkeit inkompatibel), ist genau dieser Schritt von Anfang an `failed` (`source_unavailable` bzw. `download_failed` mit Text), die übrigen laufen.
7. **Rate-Limit als Token-Bucket** wie `internal/console/limit.go`: 1 Token pro Sekunde (= 60/min), Burst 20. Ein gleitendes Fenster wäre exakter, der Bucket ist das etablierte Muster und begrenzt Spitzen stärker.
8. **Netzfristen über echte Timer:** API-Timeout, 60 s pro Lesevorgang und Icon-Gesamtfrist laufen über `context.WithTimeout`/`time.AfterFunc` (wie `http.Client.Timeout` im Katalog); `clock.Clock` steuert nur Logik (Cache-TTL, Rate-Limit, Zeitstempel). Die Tests verkürzen die Fristen über `upstream.Options`.
9. **Orphan-Bereinigung nur, wenn sicher:** Das Inventar löscht verwaiste DB-Einträge nicht, solange ein Job des Servers läuft oder die Liste `truncated` ist (ein Jar jenseits von 500 wäre sonst „verwaist“). Die DB-Zeilen werden vor der Jar-Liste gelesen, damit ein Eintrag, den ein Job gerade schreibt, nie als verwaist gilt.
10. **Toggle/Delete prüfen per Liste:** Beide fragen erst `/v1/jars` (Datei vorhanden? anderer Name, der sich nur in Groß-/Kleinschreibung unterscheidet?); fehlt die Datei → `404` ohne Dienst-Änderung, Zielname belegt → `409 file_exists`. `delete` löscht den DB-Eintrag nur, wenn seine sha512 zum gelöschten Jar passt (sonst gehört er zu `x.jar.disabled`). Toggle in den schon bestehenden Zustand → `204` ohne Dienstaufruf und ohne Audit.
11. **`refresh`** gilt bei `1` und `true`; jeder andere Wert ist `false` (V3 deklariert für `getPluginUpdates` kein `400`). Jeder Job (an seinem Ende), Toggle und Delete verwerfen den Updates-Cache des Servers.
12. **Audit-Details `plugin.update`:** `{file: <neuer Dateiname>, from: <alter Versionsname>, to: <neuer Versionsname>}`.

## Review Focus (dieser Teilplan)

- **#1 Feindliche/kaputte Upstream-Antworten:** `TestAPIRedirects`, `TestAPIErrors` (Größe, JSON, Status), `TestModrinthNullFields` (Aufgabe 7); `TestDownloadRejectsForeignHost`, `TestDownloadRedirects`, `TestDownloadSizeChecks`, `TestFileName`, `TestHangarExternalOnly` (Aufgabe 8); `TestInstallDownloadFailed`, `TestInstallForeignHost` (Aufgabe 11: Schritt `download_failed`/`hash_mismatch`, keine Datei, kein DB-Eintrag).
- **#2 Namenskollisionen:** `TestInstallFileExists` (inkl. `foo.JAR`), `TestUpdateTargetNameTaken` (Aufgabe 11); `TestToggleTargetExists` (inkl. Groß-/Kleinschreibung), `TestDeleteKeepsOtherRow` (Aufgabe 12).
- **#3 Gleichzeitige Änderungen:** `TestInventoryDropsOrphans`, `TestInventoryKeepsRowsWhenTruncated` (Aufgabe 9), `TestInventoryKeepsRowsWhileJobRuns` (Aufgabe 11); `TestOneJobPerServer` (zwei gleichzeitige Starts), `TestJobLimit`, `TestRunCancelsJobs`, `TestInstallFileAppearsDuringJob` (Aufgabe 11).
- **#4 Upstream weg/gedrosselt:** `TestAPIErrors` (429), `TestAPITimeout` (Aufgabe 7); `TestInventorySourceErrors` (Aufgabe 9); `TestSearchSourceDown` (Aufgabe 10); `TestInstallSourceUnavailableMidway`, `TestUpdateSourceUnavailable` (Aufgabe 11).

## Dateistruktur

```
internal/plugins/upstream/http.go          (7)  Konstanten, Typen Source/Channel, Options, Fehler, Client, New, doJSON, Redirect-Regeln
internal/plugins/upstream/cache.go         (7)  Cache[V]
internal/plugins/upstream/types.go         (7)  SearchQuery … Icon, validRef
internal/plugins/upstream/modrinth.go      (7)  Modrinth-Endpunkte, Dispatcher Search/Project/Versions/Version
internal/plugins/upstream/hangar.go        (8)  Hangar-Endpunkte
internal/plugins/upstream/download.go      (8)  open, idle, Download, sizedBody
internal/plugins/upstream/icon.go          (8)  Icon, IconURLAllowed, DetectImage
internal/plugins/upstream/filename.go      (8)  ValidJarName, FileName
internal/plugins/upstream/testdata/*.json  (7, 8) echte gekürzte Antworten
internal/plugins/handler.go                (9; 11, 12 ändern)  Deps, Handler, Routes, Run, loadServer, Fehler, noFiles/noAgent
internal/plugins/compat.go                 (9)  Arten, Spielversion, page_url, Icon-URL
internal/plugins/inventory.go              (9)  Inventar, GET /plugins
internal/plugins/restart.go                (9)  Neustart-Merker
internal/plugins/stubs.go                  (9–12) Übergangs-Handler, in 10/11 gekürzt, in 12 gelöscht
internal/plugins/export_test.go            (9, 11) Test-Hooks
internal/plugins/resolve.go                (10) Planer
internal/plugins/search.go                 (10) search, versions, resolve
internal/plugins/ratelimit.go              (10) 60/min pro Konto
internal/plugins/jobs.go                   (11) Registry
internal/plugins/install.go                (11) install-Handler, runJob, installTask, stepError
internal/plugins/updates.go                (11) Updatecheck, update-Handler, jobs-Handler
internal/plugins/manage.go                 (12) toggle, delete
internal/plugins/icon.go                   (12) Icon-Proxy, LRU
cmd/panel/integration_test.go, fakefiles_test.go, fakeupstream_test.go, plugins_test.go, openapi_test.go (12)
Tests: internal/plugins/upstream/{helpers,http,cache,modrinth,hangar,download,icon,filename}_test.go,
       internal/plugins/{helpers,fakeup,routes,compat_internal,inventory,search,resolve,install,updates,manage,icon}_test.go
```

---

### Task 7: Upstream-Basis: HTTP, Cache und Modrinth

Ersetzt den Phase-A-Stub von `upstream/http.go` durch den echten Client (Timeout, Größenlimit, Weiterleitungsregel, Fehler), führt den generischen `Cache[V]` ein und implementiert alle Modrinth-Endpunkte aus Spec §4. Hangar folgt in Aufgabe 8; bis dahin liefern die Hangar-Zweige `ErrUnavailable`.

**Files:**
- Modify (vollständig ersetzen): `internal/plugins/upstream/http.go`, `internal/plugins/upstream/http_test.go` (beide aus Phase A)
- Create: `internal/plugins/upstream/types.go`, `internal/plugins/upstream/cache.go`, `internal/plugins/upstream/api.go`, `internal/plugins/upstream/modrinth.go`, `internal/plugins/upstream/hangar.go` (Übergang, in Aufgabe 8 ersetzt)
- Test: `internal/plugins/upstream/helpers_test.go`, `http_test.go`, `cache_test.go`, `modrinth_test.go`, `testdata/modrinth_*.json`

**Interfaces:**
- Consumes: `clock.Clock`, `clock.NewFake`, `clock.Real`; `catalog.UserAgent(version string) string` (`internal/servers/catalog`).
- Produces: alles aus „Festlegungen → Paket upstream“ außer `Download`, `Icon`, `IconURLAllowed`, `DetectImage`, `ValidJarName`, `FileName` (Aufgabe 8). Insbesondere:

```go
func New(o Options) *Client
func DefaultHostAllowed(u *url.URL) bool
func (c *Client) UserAgent() string
func (c *Client) Bases() Bases
func (c *Client) HostAllowed(u *url.URL) bool
func (c *Client) Search(ctx context.Context, src Source, q SearchQuery) (SearchResult, error)
func (c *Client) Project(ctx context.Context, src Source, id string) (Project, error)
func (c *Client) Versions(ctx context.Context, src Source, project string, f VersionFilter) ([]Version, error)
func (c *Client) Version(ctx context.Context, src Source, project, id string) (Version, error)
func (c *Client) VersionsByHash(ctx context.Context, sha512s []string) (map[string]Version, error)
func (c *Client) LatestByHash(ctx context.Context, sha512s, loaders, gameVersions []string, types []Channel) (map[string]Version, error)
func NewCache[V any](clk clock.Clock, ttl time.Duration, maxEntries int) *Cache[V]
```

- [ ] **Step 0: Worktree, Phase-A-Stand prüfen, Draft-PR**

```bash
git fetch origin
git worktree add ../mc-panel-wt/feat-plugins-panel -b feat/plugins-panel origin/main
cd ../mc-panel-wt/feat-plugins-panel
ls internal/plugins internal/plugins/upstream
grep -n "func \|^type\|^var\|^const" internal/plugins/upstream/http.go internal/plugins/handler.go
grep -n "Jars\|UploadChecked\|IsJarName\|AlgoSHA\|Matches\|ErrHashMismatch" internal/files/filesapi/*.go | grep -v _test
grep -n "Plugins" internal/store/plugins.sql.go | head
grep -n "Plugin" internal/httpx/errors.go internal/audit/types.go
grep -n "plugins\|upstream" cmd/panel/app.go
```

Erwartet: die Liste aus „Was Phase A liefert“. Fehlt etwas, den Lead fragen, bevor es weitergeht.

- [ ] **Step 1: Testdaten anlegen (echte, gekürzte Antworten vom 2026-10-12)**

Gekürzt wurden lange Listen (`game_versions`/`versions` auf die für die Tests relevanten Einträge, Kategorien, Galerie, `body`, `changelog`); alle übrigen Felder sind wörtlich, damit die Decoder auch unbekannte Felder sehen.

```bash
mkdir -p internal/plugins/upstream/testdata
cat > internal/plugins/upstream/testdata/modrinth_project_chunky.json <<'EOF'
{"client_side":"optional","server_side":"optional","game_versions":["1.21.10","1.21.11","26.2","26.3"],"environment":["client_or_server_prefers_both"],"id":"fALzjamp","slug":"chunky","project_type":"mod","team":"CohgUIPJ","organization":null,"title":"Chunky","description":"Pre-generates chunks, quickly and efficiently","body_url":null,"published":"2021-04-04T21:18:30.574917Z","updated":"2026-07-23T04:22:28.142267Z","approved":"2021-04-04T21:18:30.574917Z","queued":null,"status":"approved","requested_status":null,"moderator_message":null,"license":{"id":"GPL-3.0-only","name":"GNU General Public License v3.0 only","url":null},"downloads":19200421,"followers":6418,"categories":["optimization","utility","worldgen"],"additional_categories":[],"loaders":["bukkit","fabric","folia","forge","neoforge","paper","spigot","sponge"],"versions":["iwbcBiUq","BszkqxWc","KJ2eDK9s"],"icon_url":"https://cdn.modrinth.com/data/fALzjamp/e1954413665e57b7bae1feef44eda530270c7d47_96.webp","raw_icon_url":"https://cdn.modrinth.com/data/fALzjamp/icon.png","issues_url":"https://github.com/pop4959/Chunky/issues","source_url":"https://github.com/pop4959/Chunky","wiki_url":"https://github.com/pop4959/Chunky/wiki","discord_url":"https://discord.gg/ZwVJukcNQG","color":2391573,"thread_id":"fALzjamp","monetization_status":"monetized"}
EOF
cat > internal/plugins/upstream/testdata/modrinth_project_fabric_api.json <<'EOF'
{"client_side":"optional","server_side":"optional","game_versions":["1.21.10","1.21.11","26.2","26.3"],"environment":["client_or_server_prefers_both"],"id":"P7dR8mSH","slug":"fabric-api","project_type":"mod","team":"BZoBsPo6","organization":null,"title":"Fabric API","description":"Lightweight and modular API providing common hooks and intercompatibility measures utilized by mods using the Fabric toolchain.","body_url":null,"published":"2021-01-22T11:04:41.419169Z","updated":"2026-10-06T15:50:00.575961Z","approved":"2021-01-22T11:04:41.419169Z","queued":null,"status":"approved","requested_status":null,"moderator_message":null,"license":{"id":"Apache-2.0","name":"Apache License 2.0","url":null},"downloads":275515905,"followers":36812,"categories":["library"],"additional_categories":[],"loaders":["fabric"],"versions":["KZS9tylY","EW33COvi","Vz2fCyj1"],"icon_url":"https://cdn.modrinth.com/data/P7dR8mSH/icon.png","raw_icon_url":"https://cdn.modrinth.com/data/P7dR8mSH/icon.png","issues_url":"https://github.com/FabricMC/fabric/issues","source_url":"https://github.com/FabricMC/fabric","wiki_url":"https://fabricmc.net/wiki/","discord_url":"https://discord.gg/v6v4pMv","color":12367004,"thread_id":"P7dR8mSH","monetization_status":"monetized"}
EOF
cat > internal/plugins/upstream/testdata/modrinth_project_luckperms.json <<'EOF'
{"client_side":"unsupported","server_side":"required","game_versions":["1.21.10","1.21.11","26.2","26.3"],"environment":["server_only"],"id":"Vebnzrzj","slug":"luckperms","project_type":"mod","team":"R4rvuFPx","organization":null,"title":"LuckPerms","description":"A permissions plugin/mod for Minecraft servers.","body_url":null,"published":"2021-01-05T13:18:36.857352Z","updated":"2026-10-06T07:14:55.653185Z","approved":"2021-01-05T13:18:36.857352Z","queued":null,"status":"approved","requested_status":null,"moderator_message":null,"license":{"id":"MIT","name":"MIT License","url":"https://github.com/LuckPerms/LuckPerms/blob/master/LICENSE.txt"},"downloads":2911813,"followers":1708,"categories":["management","utility"],"additional_categories":[],"loaders":["bukkit","bungeecord","fabric","folia","forge","neoforge","paper","spigot","velocity","waterfall"],"versions":["FidxKRFj","AB34dkXL","AFeyDid4"],"icon_url":"https://cdn.modrinth.com/data/Vebnzrzj/90943902cc650e95b167265b8f2d7c893f82c8f4_96.webp","raw_icon_url":"https://cdn.modrinth.com/data/Vebnzrzj/icon.png","issues_url":"https://github.com/lucko/LuckPerms/issues","source_url":"https://github.com/lucko/LuckPerms","wiki_url":"https://luckperms.net/wiki","discord_url":"https://discord.gg/luckperms","color":9623556,"thread_id":"Vebnzrzj","monetization_status":"monetized"}
EOF
cat > internal/plugins/upstream/testdata/modrinth_project_sodium_extra.json <<'EOF'
{"client_side":"required","server_side":"unsupported","game_versions":["1.21.10","1.21.11","26.2","26.3"],"environment":["client_only"],"id":"PtjYWJkn","slug":"sodium-extra","project_type":"mod","team":"EfhDgRdO","organization":null,"title":"Sodium Extra","description":"A Sodium addon that adds features that shouldn't be in Sodium.","body_url":null,"published":"2021-02-17T04:37:04.928784Z","updated":"2026-09-16T02:48:18.058397Z","approved":"2021-02-17T04:37:04.928784Z","queued":null,"status":"approved","requested_status":null,"moderator_message":null,"license":{"id":"LGPL-3.0-only","name":"GNU Lesser General Public License v3.0 only","url":null},"downloads":102078660,"followers":13676,"categories":["cursed","optimization","utility"],"additional_categories":[],"loaders":["fabric","neoforge","quilt"],"versions":["uFWdXpRc","fQlbGqah","E1Chj4yH"],"icon_url":"https://cdn.modrinth.com/data/PtjYWJkn/0df4fb22a11e1dcb5e83cb0aadd275b571aca7a9_96.webp","raw_icon_url":"https://cdn.modrinth.com/data/PtjYWJkn/icon.png","issues_url":"https://github.com/FlashyReese/sodium-extra-fabric/issues","source_url":"https://github.com/FlashyReese/sodium-extra-fabric","wiki_url":"https://github.com/FlashyReese/sodium-extra-fabric/wiki","discord_url":"https://flashyreese.me/discord","color":16577485,"thread_id":"PtjYWJkn","monetization_status":"monetized"}
EOF
cat > internal/plugins/upstream/testdata/modrinth_search_mod.json <<'EOF'
{"hits":[{"project_id":"fALzjamp","project_type":"mod","all_project_types":["mod","plugin"],"slug":"chunky","author":"pop4959","author_id":"nfBsvT1J","organization":null,"organization_id":null,"title":"Chunky","description":"Pre-generates chunks, quickly and efficiently","categories":["bukkit","fabric","folia","forge","neoforge","optimization","paper","spigot","sponge","utility","worldgen"],"display_categories":["bukkit","fabric","folia","forge","neoforge","optimization","paper","spigot","sponge","utility","worldgen"],"versions":["1.21.10","1.21.11","26.2","26.3"],"downloads":19099176,"follows":6414,"icon_url":"https://cdn.modrinth.com/data/fALzjamp/e1954413665e57b7bae1feef44eda530270c7d47_96.webp","date_created":"2021-04-04T21:18:30.574917+00:00","date_modified":"2026-07-23T04:22:27.486544+00:00","latest_version":"1CpEkmcD","license":"GPL-3.0-only","client_side":"optional","server_side":"optional","environment":["client_or_server_prefers_both"],"disclosure_types":[],"gallery":[],"featured_gallery":null,"color":2391573},{"project_id":"NFASTzJC","project_type":"mod","all_project_types":["mod"],"slug":"auto-chunky","author":"tollg92","author_id":"Qad9WzaC","organization":null,"organization_id":null,"title":"Auto Chunky","description":"A lightweight, server-side utility mod that automatically pauses world pre-generation when players are online and resumes when they leave.","categories":["fabric","management","optimization","storage","technology","utility","worldgen"],"display_categories":["fabric","optimization","utility","worldgen"],"versions":["1.21.10","1.21.11"],"downloads":9333,"follows":14,"icon_url":"https://cdn.modrinth.com/data/NFASTzJC/f305460cb7011659571ea8b86fc0d70af12b2726_96.webp","date_created":"2026-03-27T06:52:22.631735+00:00","date_modified":"2026-03-26T19:53:05.455189+00:00","latest_version":"9vX4DQE0","license":"MIT","client_side":"unsupported","server_side":"required","environment":["dedicated_server_only"],"disclosure_types":[],"gallery":[],"featured_gallery":null,"color":3039304}],"offset":0,"limit":2,"total_hits":5}
EOF
cat > internal/plugins/upstream/testdata/modrinth_search_plugin.json <<'EOF'
{"hits":[{"project_id":"Vebnzrzj","project_type":"mod","all_project_types":["mod","plugin"],"slug":"luckperms","author":"lucko","author_id":"iRtG8gdL","organization":null,"organization_id":null,"title":"LuckPerms","description":"A permissions plugin/mod for Minecraft servers.","categories":["bukkit","bungeecord","fabric","folia","forge","management"],"display_categories":["bukkit","bungeecord","fabric","folia","forge","management"],"versions":["1.21.10","1.21.11","26.2","26.3"],"downloads":2898442,"follows":1709,"icon_url":"https://cdn.modrinth.com/data/Vebnzrzj/90943902cc650e95b167265b8f2d7c893f82c8f4_96.webp","date_created":"2021-01-05T13:18:36.857352+00:00","date_modified":"2026-10-06T07:14:53.685966+00:00","latest_version":"b0mk8uS6","license":"MIT","client_side":"unsupported","server_side":"required","environment":["server_only"],"disclosure_types":[],"gallery":[],"featured_gallery":null,"color":9623556},{"project_id":"oDiIYfIa","project_type":"mod","all_project_types":["plugin"],"slug":"lockiprefixes","author":"leifiyoo","author_id":"GzzitL82","organization":"niro","organization_id":"lxtzUvQw","title":"LuckPermsPrefixes","description":"A Minecraft plugin for LuckPerms that formats chat messages and the TAB list with prefixes, colors, and animated gradients.","categories":["bukkit","folia","game-mechanics","management","paper","purpur"],"display_categories":["bukkit","folia","game-mechanics","management","paper","purpur"],"versions":["1.21.10","1.21.11"],"downloads":4024,"follows":2,"icon_url":"https://cdn.modrinth.com/data/oDiIYfIa/57edb5e0c27118fc7bca2ffef21d6dd80510d0c1_96.webp","date_created":"2026-01-25T21:23:08.733229+00:00","date_modified":"2026-05-18T16:56:13.424073+00:00","latest_version":"9LbGFZGj","license":"MIT","client_side":"unsupported","server_side":"required","environment":[],"disclosure_types":["telemetry","telemetry_always_active","ai_content","ai_content_code","ai_content_assets","ai_content_text"],"gallery":[],"featured_gallery":null,"color":6579450}],"offset":0,"limit":2,"total_hits":99}
EOF
cat > internal/plugins/upstream/testdata/modrinth_version_files.json <<'EOF'
{"a0e087adfc1c7b9fab8fdb5a430a3331a2ca30bfc72818bb7e65ce9baff051a1490834be8cbb9cbeda292f2e92c44cf03fb829ebeb233ade9d666ed908e49ad5":{"game_versions":["1.21.10","1.21.11","26.2"],"loaders":["bukkit","folia","paper","spigot"],"environment":"unknown","id":"MBSY8toc","project_id":"Vebnzrzj","author_id":"iRtG8gdL","featured":true,"name":"v5.5.53 (Bukkit)","version_number":"v5.5.53-bukkit","changelog_url":null,"date_published":"2026-05-27T07:14:37.365816Z","downloads":171922,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"a29SxWwn","hashes":{"sha1":"dad091fbabe7cbb1db3dc1478eb1fe413520a014","sha512":"a0e087adfc1c7b9fab8fdb5a430a3331a2ca30bfc72818bb7e65ce9baff051a1490834be8cbb9cbeda292f2e92c44cf03fb829ebeb233ade9d666ed908e49ad5"},"url":"https://cdn.modrinth.com/data/Vebnzrzj/versions/MBSY8toc/LuckPerms-Bukkit-5.5.53.jar","filename":"LuckPerms-Bukkit-5.5.53.jar","primary":true,"size":1490252,"file_type":null}],"dependencies":[]}}
EOF
cat > internal/plugins/upstream/testdata/modrinth_version_files_update.json <<'EOF'
{"a0e087adfc1c7b9fab8fdb5a430a3331a2ca30bfc72818bb7e65ce9baff051a1490834be8cbb9cbeda292f2e92c44cf03fb829ebeb233ade9d666ed908e49ad5":{"game_versions":["1.21.10","1.21.11","26.2","26.3"],"loaders":["bukkit","folia","paper","spigot"],"environment":"unknown","id":"b0mk8uS6","project_id":"Vebnzrzj","author_id":"iRtG8gdL","featured":true,"name":"v5.5.71 (Bukkit)","version_number":"v5.5.71-bukkit","changelog_url":null,"date_published":"2026-08-06T19:40:10.637955Z","downloads":211270,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"rbEmZznl","hashes":{"sha512":"188a91f0a543d23bfda32385fca6db63d61e49c8a422bd452a260bd9cbc6a7d7fe45071199e9fca8f3ce43c2b41ee84fd315bd15464577028ff3951a7d4fab27","sha1":"fc8bb9919fa77c670b7ed5c284bcb66770ea71fc"},"url":"https://cdn.modrinth.com/data/Vebnzrzj/versions/b0mk8uS6/LuckPerms-Bukkit-5.5.71.jar","filename":"LuckPerms-Bukkit-5.5.71.jar","primary":true,"size":1501521,"file_type":null}],"dependencies":[]}}
EOF
cat > internal/plugins/upstream/testdata/modrinth_versions_chunky_fabric.json <<'EOF'
[{"game_versions":["1.21.11"],"loaders":["fabric"],"environment":"client_or_server_prefers_both","id":"1CpEkmcD","project_id":"fALzjamp","author_id":"nfBsvT1J","featured":false,"name":"Chunky 1.4.55","version_number":"1.4.55","changelog_url":null,"date_published":"2025-12-22T10:43:31.045916Z","downloads":1799865,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"jglYvIgn","hashes":{"sha512":"3be0e049e3dea6256b395ccb1f7dccc9c6b23cb7b1f6a717a7cd1ca55f9dbda489679df32868c72664ebb28ca05f2c366590d1e1a11f0dc5f69f947903bad833","sha1":"19c34a23a5c1b2f6c73117b8eb4524b521e1926b"},"url":"https://cdn.modrinth.com/data/fALzjamp/versions/1CpEkmcD/Chunky-Fabric-1.4.55.jar","filename":"Chunky-Fabric-1.4.55.jar","primary":true,"size":355288,"file_type":null}],"dependencies":[{"version_id":null,"project_id":"P7dR8mSH","file_name":null,"dependency_type":"required"}]}]
EOF
cat > internal/plugins/upstream/testdata/modrinth_versions_fabric_api.json <<'EOF'
[{"game_versions":["1.21.11"],"loaders":["fabric"],"environment":"client_or_server_prefers_both","id":"6qAuTtLR","project_id":"P7dR8mSH","author_id":"JZA4dW8o","featured":false,"name":"[1.21.11] Fabric API 0.141.6+1.21.11","version_number":"0.141.6+1.21.11","changelog_url":null,"date_published":"2026-07-28T08:48:35.091772Z","downloads":8310932,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"GiXTTEvo","hashes":{"sha1":"c98467cbbaf4d197377266795ae015f4130d65b6","sha512":"852d3682c4f353fd0ca86546af8578513e8bcf628377fa9d49c556bc59c5024c69ab56c92fc6dfba11fed19e4316a3f5636e7e07fd3d96f7043b531aef59b637"},"url":"https://cdn.modrinth.com/data/P7dR8mSH/versions/6qAuTtLR/fabric-api-0.141.6%2B1.21.11.jar","filename":"fabric-api-0.141.6+1.21.11.jar","primary":true,"size":2426039,"file_type":null}],"dependencies":[]},{"game_versions":["1.21.11"],"loaders":["fabric"],"environment":"client_or_server_prefers_both","id":"zGF3drOQ","project_id":"P7dR8mSH","author_id":"JZA4dW8o","featured":false,"name":"[1.21.11] Fabric API 0.141.5+1.21.11","version_number":"0.141.5+1.21.11","changelog_url":null,"date_published":"2026-07-16T10:33:08.605197Z","downloads":1591015,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"I2nMzwOl","hashes":{"sha1":"f56956fc14c6e1af380e80fce7e9cfd4db050eef","sha512":"66f9ac506a269fbd3738dd22a21ca8995d3a56b97ee1514392ab8058abb6187851a5ac51dbffd71744ba0c904dfea65497593b5e357f30ac266c01f4b9b323ee"},"url":"https://cdn.modrinth.com/data/P7dR8mSH/versions/zGF3drOQ/fabric-api-0.141.5%2B1.21.11.jar","filename":"fabric-api-0.141.5+1.21.11.jar","primary":true,"size":2425434,"file_type":null}],"dependencies":[]}]
EOF
cat > internal/plugins/upstream/testdata/modrinth_versions_luckperms.json <<'EOF'
[{"game_versions":["1.21.10","1.21.11","26.2","26.3"],"loaders":["bukkit","folia","paper","spigot"],"environment":"unknown","id":"b0mk8uS6","project_id":"Vebnzrzj","author_id":"iRtG8gdL","featured":true,"name":"v5.5.71 (Bukkit)","version_number":"v5.5.71-bukkit","changelog_url":null,"date_published":"2026-08-06T19:40:10.637955Z","downloads":211270,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"rbEmZznl","hashes":{"sha1":"fc8bb9919fa77c670b7ed5c284bcb66770ea71fc","sha512":"188a91f0a543d23bfda32385fca6db63d61e49c8a422bd452a260bd9cbc6a7d7fe45071199e9fca8f3ce43c2b41ee84fd315bd15464577028ff3951a7d4fab27"},"url":"https://cdn.modrinth.com/data/Vebnzrzj/versions/b0mk8uS6/LuckPerms-Bukkit-5.5.71.jar","filename":"LuckPerms-Bukkit-5.5.71.jar","primary":true,"size":1501521,"file_type":null}],"dependencies":[]},{"game_versions":["1.21.10","1.21.11","26.2"],"loaders":["bukkit","folia","paper","spigot"],"environment":"unknown","id":"MBSY8toc","project_id":"Vebnzrzj","author_id":"iRtG8gdL","featured":true,"name":"v5.5.53 (Bukkit)","version_number":"v5.5.53-bukkit","changelog_url":null,"date_published":"2026-05-27T07:14:37.365816Z","downloads":171922,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"a29SxWwn","hashes":{"sha512":"a0e087adfc1c7b9fab8fdb5a430a3331a2ca30bfc72818bb7e65ce9baff051a1490834be8cbb9cbeda292f2e92c44cf03fb829ebeb233ade9d666ed908e49ad5","sha1":"dad091fbabe7cbb1db3dc1478eb1fe413520a014"},"url":"https://cdn.modrinth.com/data/Vebnzrzj/versions/MBSY8toc/LuckPerms-Bukkit-5.5.53.jar","filename":"LuckPerms-Bukkit-5.5.53.jar","primary":true,"size":1490252,"file_type":null}],"dependencies":[]},{"game_versions":["1.21.10","1.21.11"],"loaders":["bukkit","paper","spigot"],"environment":"unknown","id":"OrIs0S6b","project_id":"Vebnzrzj","author_id":"iRtG8gdL","featured":true,"name":"v5.5.17 (Bukkit)","version_number":"v5.5.17-bukkit","changelog_url":null,"date_published":"2025-10-11T12:26:55.608887Z","downloads":272706,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"Rwl4pLtw","hashes":{"sha1":"af24a4fc61c4ae814ce9a280179b9c88ec614013","sha512":"773895644260b338818bfeff0c78f8d4f590f56b0f711c378a4eec91be6e8b37354099b5db1ea5b2dce4c02486213297a6da09675c9bf6f014f9a400b5772cf3"},"url":"https://cdn.modrinth.com/data/Vebnzrzj/versions/OrIs0S6b/LuckPerms-Bukkit-5.5.17.jar","filename":"LuckPerms-Bukkit-5.5.17.jar","primary":true,"size":1480518,"file_type":null}],"dependencies":[]}]
EOF
```

- [ ] **Step 2: Testhelfer schreiben**

`internal/plugins/upstream/helpers_test.go`:

```go
package upstream_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var bg = context.Background()

const testUA = "mc-panel/test (https://github.com/Bernotese/mc-panel)"

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// serveFile answers every request with a testdata file as JSON.
func serveFile(t *testing.T, name string) http.HandlerFunc {
	b := testdata(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

// apiServer is a TLS test server that counts requests per "METHOD /path" and keeps the last
// request (with its body) for assertions.
type apiServer struct {
	*httptest.Server
	mu       sync.Mutex
	hits     map[string]int
	last     *http.Request
	lastBody []byte
}

func newAPI(t *testing.T, h http.HandlerFunc) *apiServer {
	t.Helper()
	a := &apiServer{hits: map[string]int{}}
	a.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		a.mu.Lock()
		a.hits[r.Method+" "+r.URL.Path]++
		a.last = r.Clone(context.Background())
		a.lastBody = body
		a.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		h(w, r)
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *apiServer) count(key string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hits[key]
}

func (a *apiServer) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, v := range a.hits {
		n += v
	}
	return n
}

func (a *apiServer) lastRequest() (*http.Request, []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.lastBody
}

// newClient builds a client whose API bases point at api and whose download host check accepts
// exactly the host of cdn (https only). Either may be nil (not both).
func newClient(t *testing.T, api *apiServer, cdn *httptest.Server, clk clock.Clock, opts ...func(*upstream.Options)) *upstream.Client {
	t.Helper()
	o := upstream.Options{Clock: clk, Version: "test"}
	switch {
	case api != nil:
		o.HTTPClient = api.Client()
		o.Bases = upstream.Bases{Modrinth: api.URL, Hangar: api.URL}
	case cdn != nil:
		o.HTTPClient = cdn.Client()
		o.Bases = upstream.Bases{Modrinth: cdn.URL, Hangar: cdn.URL}
	}
	if cdn != nil {
		host := cdn.Listener.Addr().String()
		o.AllowHost = func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host && u.User == nil }
	}
	for _, f := range opts {
		f(&o)
	}
	return upstream.New(o)
}
```

Alle httptest-TLS-Server nutzen dasselbe eingebaute Testzertifikat; der Client eines Servers vertraut daher auch jedem anderen (`cdn`, `other`).

- [ ] **Step 3: Tests für HTTP-Basis und Cache schreiben**

`internal/plugins/upstream/http_test.go` (ersetzt die Phase-A-Datei):

```go
package upstream_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestDefaultHostAllowed(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://cdn.modrinth.com/data/x/versions/y/a.jar":   true,
		"https://hangarcdn.papermc.io/plugins/a/b/PAPER/a.jar": true,
		"https://CDN.Modrinth.com/x":                         true,
		"https://cdn.modrinth.com:443/x":                     true,
		"https://cdn.modrinth.com:8443/x":                    false,
		"http://cdn.modrinth.com/x":                          false,
		"https://user@cdn.modrinth.com/x":                    false,
		"https://cdn.modrinth.com.evil.example/x":            false,
		"https://evil.example/cdn.modrinth.com/x":            false,
		"https://api.modrinth.com/v2/project/x":              false,
		"https://hangar.papermc.io/api/v1/projects/x":        false,
		"ftp://cdn.modrinth.com/x":                           false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := upstream.DefaultHostAllowed(u); got != want {
			t.Errorf("DefaultHostAllowed(%s) = %v, want %v", raw, got, want)
		}
	}
	if upstream.DefaultHostAllowed(nil) {
		t.Error("nil URL allowed")
	}
}

func TestUserAgentAndBases(t *testing.T) {
	c := upstream.New(upstream.Options{Clock: clock.NewFake(t0), Version: "1.2.3"})
	if got := c.UserAgent(); got != "mc-panel/1.2.3 (https://github.com/Bernotese/mc-panel)" {
		t.Errorf("UserAgent = %q", got)
	}
	if c.Bases() != upstream.DefaultBases {
		t.Errorf("Bases = %+v", c.Bases())
	}
	if got := upstream.New(upstream.Options{Clock: clock.NewFake(t0)}).UserAgent(); got != "mc-panel/dev (https://github.com/Bernotese/mc-panel)" {
		t.Errorf("default UserAgent = %q", got)
	}
	u, _ := url.Parse("https://cdn.modrinth.com/x.png")
	if !c.HostAllowed(u) {
		t.Error("default host check refuses cdn.modrinth.com")
	}
}

// Review Focus #1: an API may redirect only to https on its own host, at most 3 times.
func TestAPIRedirects(t *testing.T) {
	project := testdata(t, "modrinth_project_luckperms.json")
	var otherHits atomic.Int64
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		_, _ = w.Write(project)
	}))
	t.Cleanup(other.Close)
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v2/project/")
		switch {
		case p == "luckperms":
			if ua := r.Header.Get("User-Agent"); ua != testUA {
				t.Errorf("User-Agent = %q", ua)
			}
			_, _ = w.Write(project)
		case p == "same":
			http.Redirect(w, r, "/v2/project/luckperms", http.StatusFound)
		case p == "foreign":
			http.Redirect(w, r, other.URL+"/v2/project/luckperms", http.StatusFound)
		case p == "plain":
			http.Redirect(w, r, "http://"+r.Host+"/v2/project/luckperms", http.StatusFound)
		case strings.HasPrefix(p, "hop"):
			n, _ := strconv.Atoi(strings.TrimPrefix(p, "hop"))
			if n >= 3 {
				http.Redirect(w, r, "/v2/project/luckperms", http.StatusFound)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/v2/project/hop%d", n+1), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	for _, id := range []string{"same", "hop1"} { // hop1: exactly 3 redirects
		if p, err := c.Project(bg, upstream.Modrinth, id); err != nil || p.ID != "Vebnzrzj" {
			t.Fatalf("%s: %+v, %v", id, p, err)
		}
	}
	for _, id := range []string{"foreign", "plain", "hop0"} { // hop0: 4 redirects
		if _, err := c.Project(bg, upstream.Modrinth, id); !errors.Is(err, upstream.ErrUnavailable) {
			t.Errorf("%s: err = %v, want ErrUnavailable", id, err)
		}
	}
	if n := otherHits.Load(); n != 0 {
		t.Fatalf("the foreign host was contacted %d times", n)
	}
}

// Review Focus #1/#4: status codes, rate limits, broken and huge answers.
func TestAPIErrors(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/v2/project/") {
		case "missing":
			http.NotFound(w, r)
		case "limited":
			w.WriteHeader(http.StatusTooManyRequests)
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
		case "badjson":
			_, _ = w.Write([]byte(`{"id":`))
		case "huge":
			_, _ = w.Write([]byte(`{"id":"huge","pad":"` + strings.Repeat("a", upstream.MaxJSONBytes) + `"}`))
		case "noid":
			_, _ = w.Write([]byte(`{"slug":"x"}`))
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	_, err := c.Project(bg, upstream.Modrinth, "missing")
	if !errors.Is(err, upstream.ErrNotFound) || errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("404: %v", err)
	}
	_, err = c.Project(bg, upstream.Modrinth, "limited")
	if !errors.Is(err, upstream.ErrUnavailable) || !strings.Contains(err.Error(), "429") {
		t.Fatalf("429: %v", err)
	}
	for _, id := range []string{"broken", "badjson", "huge", "noid"} {
		if _, err := c.Project(bg, upstream.Modrinth, id); !errors.Is(err, upstream.ErrUnavailable) {
			t.Errorf("%s: %v, want ErrUnavailable", id, err)
		}
	}
	// failures are not cached
	_, _ = c.Project(bg, upstream.Modrinth, "broken")
	if n := api.count("GET /v2/project/broken"); n != 2 {
		t.Fatalf("broken fetched %d times, want 2", n)
	}
}

// Review Focus #4: a hanging upstream ends with ErrUnavailable after the API timeout.
func TestAPITimeout(t *testing.T) {
	api := newAPI(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	c := newClient(t, api, nil, clock.NewFake(t0), func(o *upstream.Options) { o.APITimeout = 100 * time.Millisecond })
	start := time.Now()
	if _, err := c.Project(bg, upstream.Modrinth, "slow"); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeout took %s", d)
	}
}
```

`internal/plugins/upstream/cache_test.go`:

```go
package upstream_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestCacheTTL(t *testing.T) {
	clk := clock.NewFake(t0)
	c := upstream.NewCache[int](clk, time.Minute, 10)
	var calls atomic.Int64
	fetch := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	for range 2 {
		if v, err := c.Get(bg, "a", fetch); err != nil || v != 1 {
			t.Fatalf("Get = %d, %v", v, err)
		}
	}
	clk.Advance(time.Minute)
	if v, _ := c.Get(bg, "a", fetch); v != 2 {
		t.Fatalf("after the TTL Get = %d, want a new fetch", v)
	}
	if v, ok := c.Peek("a"); !ok || v != 2 {
		t.Fatalf("Peek = %d, %v", v, ok)
	}
	c.Delete("a")
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Delete kept the entry")
	}
}

func TestCacheSharedCall(t *testing.T) {
	c := upstream.NewCache[string](clock.NewFake(t0), time.Minute, 10)
	release := make(chan struct{})
	var calls atomic.Int64
	fetch := func(context.Context) (string, error) {
		calls.Add(1)
		<-release
		return "v", nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if v, err := c.Get(bg, "k", fetch); err != nil || v != "v" {
				t.Errorf("Get = %q, %v", v, err)
			}
		})
	}
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetch ran %d times, want 1", n)
	}
}

// A caller that gives up gets ErrUnavailable at once; the shared fetch still fills the cache.
func TestCacheCallerCancel(t *testing.T) {
	c := upstream.NewCache[string](clock.NewFake(t0), time.Minute, 10)
	release := make(chan struct{})
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := c.Get(ctx, "k", func(context.Context) (string, error) { <-release; return "v", nil }); !errors.Is(err, upstream.ErrUnavailable) {
		t.Fatalf("cancelled Get: %v", err)
	}
	close(release)
	if v, err := c.Get(bg, "k", func(context.Context) (string, error) { return "v", nil }); err != nil || v != "v" {
		t.Fatalf("Get = %q, %v", v, err)
	}
}

func TestCacheErrorsNotCached(t *testing.T) {
	c := upstream.NewCache[int](clock.NewFake(t0), time.Minute, 10)
	boom := errors.New("boom")
	if _, err := c.Get(bg, "a", func(context.Context) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if v, err := c.Get(bg, "a", func(context.Context) (int, error) { return 7, nil }); err != nil || v != 7 {
		t.Fatalf("after an error Get = %d, %v", v, err)
	}
}

// TTL 0 never stores: every Get after the previous one ended fetches again.
func TestCacheZeroTTL(t *testing.T) {
	c := upstream.NewCache[int](clock.NewFake(t0), 0, 10)
	var calls atomic.Int64
	fetch := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	_, _ = c.Get(bg, "a", fetch)
	_, _ = c.Get(bg, "a", fetch)
	if calls.Load() != 2 || c.Len() != 0 {
		t.Fatalf("calls = %d, len = %d", calls.Load(), c.Len())
	}
}

func TestCacheEvictsOldest(t *testing.T) {
	clk := clock.NewFake(t0)
	c := upstream.NewCache[int](clk, time.Hour, 3)
	for i, k := range []string{"a", "b", "c", "d"} {
		c.Put(k, i)
		clk.Advance(time.Second)
	}
	if c.Len() != 3 {
		t.Fatalf("Len = %d", c.Len())
	}
	if _, ok := c.Peek("a"); ok {
		t.Fatal("the oldest entry was kept")
	}
	for _, k := range []string{"b", "c", "d"} {
		if _, ok := c.Peek(k); !ok {
			t.Errorf("%s evicted", k)
		}
	}
	if d := upstream.NewCache[int](clk, time.Hour, 0); d == nil {
		t.Fatal("max 0 must fall back to MaxCacheEntries")
	}
}
```

- [ ] **Step 4: Tests für Modrinth schreiben**

`internal/plugins/upstream/modrinth_test.go`:

```go
package upstream_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

var paperLoaders = []string{"paper", "spigot", "bukkit"}

const (
	luckperms553 = "a0e087adfc1c7b9fab8fdb5a430a3331a2ca30bfc72818bb7e65ce9baff051a1490834be8cbb9cbeda292f2e92c44cf03fb829ebeb233ade9d666ed908e49ad5"
	unknownHash  = "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
)

func TestModrinthSearchPlugin(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_search_plugin.json"))
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	q := upstream.SearchQuery{Query: "luckperms", Offset: 20, ProjectType: "plugin", Loaders: paperLoaders, GameVersion: "1.21.11"}
	res, err := c.Search(bg, upstream.Modrinth, q)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	if req.URL.Path != "/v2/search" {
		t.Fatalf("path = %s", req.URL.Path)
	}
	got := req.URL.Query()
	wantFacets := `[["project_type:plugin"],["categories:paper","categories:spigot","categories:bukkit"],["versions:1.21.11"]]`
	if got.Get("facets") != wantFacets || got.Get("index") != "relevance" || got.Get("offset") != "20" ||
		got.Get("limit") != "20" || got.Get("query") != "luckperms" {
		t.Fatalf("query = %v", got)
	}
	if res.Total != 99 || len(res.Hits) != 2 {
		t.Fatalf("result = %+v", res)
	}
	want := upstream.SearchHit{
		ProjectID: "Vebnzrzj", Slug: "luckperms", Title: "LuckPerms", Description: "A permissions plugin/mod for Minecraft servers.",
		Author: "lucko", Downloads: 2898442,
		IconURL: "https://cdn.modrinth.com/data/Vebnzrzj/90943902cc650e95b167265b8f2d7c893f82c8f4_96.webp",
	}
	if res.Hits[0] != want {
		t.Fatalf("hit = %+v", res.Hits[0])
	}
	// cached for 5 minutes
	_, _ = c.Search(bg, upstream.Modrinth, q)
	clk.Advance(4 * time.Minute)
	_, _ = c.Search(bg, upstream.Modrinth, q)
	if n := api.count("GET /v2/search"); n != 1 {
		t.Fatalf("searched %d times within 5 min", n)
	}
	clk.Advance(time.Minute)
	_, _ = c.Search(bg, upstream.Modrinth, q)
	if n := api.count("GET /v2/search"); n != 2 {
		t.Fatalf("searched %d times after 5 min", n)
	}
}

func TestModrinthSearchMod(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_search_mod.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	res, err := c.Search(bg, upstream.Modrinth, upstream.SearchQuery{ProjectType: "mod", Loaders: []string{"fabric"}, GameVersion: "1.21.11", ServerSideOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	want := `[["project_type:mod"],["categories:fabric"],["versions:1.21.11"],["server_side:required","server_side:optional"]]`
	if q.Get("facets") != want || q.Get("index") != "downloads" || q.Get("offset") != "0" {
		t.Fatalf("query = %v", q)
	}
	if res.Total != 5 || res.Hits[0].Slug != "chunky" || res.Hits[1].Author != "tollg92" {
		t.Fatalf("result = %+v", res)
	}
}

func TestModrinthProject(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/project/luckperms":
			_, _ = w.Write(testdata(t, "modrinth_project_luckperms.json"))
		case "/v2/project/sodium-extra":
			_, _ = w.Write(testdata(t, "modrinth_project_sodium_extra.json"))
		default:
			http.NotFound(w, r)
		}
	})
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	p, err := c.Project(bg, upstream.Modrinth, "luckperms")
	if err != nil {
		t.Fatal(err)
	}
	want := upstream.Project{
		Source: upstream.Modrinth, ID: "Vebnzrzj", Slug: "luckperms", Title: "LuckPerms",
		Description: "A permissions plugin/mod for Minecraft servers.", ServerSide: "required",
		IconURL: "https://cdn.modrinth.com/data/Vebnzrzj/90943902cc650e95b167265b8f2d7c893f82c8f4_96.webp",
	}
	if p != want || p.ClientOnly() {
		t.Fatalf("project = %+v", p)
	}
	s, err := c.Project(bg, upstream.Modrinth, "sodium-extra")
	if err != nil || !s.ClientOnly() {
		t.Fatalf("sodium-extra = %+v, %v", s, err)
	}
	// 15 minutes cache
	clk.Advance(14 * time.Minute)
	_, _ = c.Project(bg, upstream.Modrinth, "luckperms")
	if n := api.count("GET /v2/project/luckperms"); n != 1 {
		t.Fatalf("fetched %d times", n)
	}
}

func TestModrinthVersions(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_versions_luckperms.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Modrinth, "Vebnzrzj", upstream.VersionFilter{Loaders: paperLoaders, GameVersion: "1.21.11"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	if req.URL.Path != "/v2/project/Vebnzrzj/version" || q.Get("loaders") != `["paper","spigot","bukkit"]` || q.Get("game_versions") != `["1.21.11"]` {
		t.Fatalf("request = %s %v", req.URL.Path, q)
	}
	if len(vs) != 3 {
		t.Fatalf("versions = %d", len(vs))
	}
	v := vs[0]
	wantFile := upstream.File{
		URL:    "https://cdn.modrinth.com/data/Vebnzrzj/versions/b0mk8uS6/LuckPerms-Bukkit-5.5.71.jar",
		Name:   "LuckPerms-Bukkit-5.5.71.jar", Size: 1501521,
		SHA512: "188a91f0a543d23bfda32385fca6db63d61e49c8a422bd452a260bd9cbc6a7d7fe45071199e9fca8f3ce43c2b41ee84fd315bd15464577028ff3951a7d4fab27",
	}
	if v.Source != upstream.Modrinth || v.ID != "b0mk8uS6" || v.ProjectID != "Vebnzrzj" || v.Name != "v5.5.71-bukkit" ||
		v.Channel != upstream.Release || !v.Downloadable() || *v.File != wantFile || len(v.Dependencies) != 0 ||
		!v.PublishedAt.Equal(time.Date(2026, 8, 6, 19, 40, 10, 637955000, time.UTC)) ||
		!slices.Contains(v.GameVersions, "1.21.11") || !slices.Contains(v.Loaders, "paper") {
		t.Fatalf("version = %+v", v)
	}
	for i := 1; i < len(vs); i++ {
		if vs[i].PublishedAt.After(vs[i-1].PublishedAt) {
			t.Fatal("not newest first")
		}
	}
	// ReleaseOnly filters the cached list locally (no second request)
	if rs, _ := c.Versions(bg, upstream.Modrinth, "Vebnzrzj", upstream.VersionFilter{Loaders: paperLoaders, GameVersion: "1.21.11", ReleaseOnly: true}); len(rs) != 3 {
		t.Fatalf("release only = %d", len(rs))
	}
	if n := api.total(); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

// mVersionJSON is a minimal Modrinth version answer for the channel and dependency tests.
func mVersionJSON(id, typ, published string, files, deps string) string {
	return `{"id":"` + id + `","project_id":"P","version_number":"` + id + `","version_type":"` + typ +
		`","date_published":"` + published + `","game_versions":["1.21.11"],"loaders":["fabric"],"files":` + files +
		`,"dependencies":` + deps + `}`
}

const oneJar = `[{"url":"https://cdn.modrinth.com/data/P/a.jar","filename":"a.jar","primary":true,"size":3,"hashes":{"sha512":"ABC"}}]`

func TestModrinthChannelsAndOrder(t *testing.T) {
	body := "[" + strings.Join([]string{
		mVersionJSON("old", "release", "2026-01-01T00:00:00Z", oneJar, "[]"),
		mVersionJSON("newbeta", "beta", "2026-03-01T00:00:00Z", oneJar, "[]"),
		mVersionJSON("alpha", "alpha", "2026-02-01T00:00:00Z", oneJar, "[]"),
		mVersionJSON("weird", "nightly", "2026-02-15T00:00:00Z", oneJar, "[]"),
	}, ",") + "]"
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Modrinth, "P", upstream.VersionFilter{Loaders: []string{"fabric"}, GameVersion: "1.21.11"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range vs {
		got = append(got, v.ID+":"+string(v.Channel))
	}
	if want := []string{"newbeta:beta", "weird:alpha", "alpha:alpha", "old:release"}; !slices.Equal(got, want) {
		t.Fatalf("versions = %v, want %v", got, want)
	}
	if vs[0].File.SHA512 != "abc" {
		t.Fatalf("hash not lower-cased: %q", vs[0].File.SHA512)
	}
	rs, _ := c.Versions(bg, upstream.Modrinth, "P", upstream.VersionFilter{Loaders: []string{"fabric"}, GameVersion: "1.21.11", ReleaseOnly: true})
	if len(rs) != 1 || rs[0].ID != "old" {
		t.Fatalf("release only = %+v", rs)
	}
}

func TestModrinthDependencies(t *testing.T) {
	deps := `[{"project_id":"REQ","version_id":null,"file_name":null,"dependency_type":"required"},
	{"project_id":null,"version_id":"VONLY","file_name":null,"dependency_type":"required"},
	{"project_id":null,"version_id":null,"file_name":"lib.jar","dependency_type":"required"},
	{"project_id":"OPT","version_id":null,"file_name":null,"dependency_type":"optional"},
	{"project_id":"BAD","version_id":null,"file_name":null,"dependency_type":"incompatible"},
	{"project_id":"EMB","version_id":null,"file_name":null,"dependency_type":"embedded"}]`
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(mVersionJSON("v1", "release", "2026-01-01T00:00:00Z", oneJar, deps)))
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	v, err := c.Version(bg, upstream.Modrinth, "", "v1")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	if req.URL.Path != "/v2/version/v1" {
		t.Fatalf("path = %s", req.URL.Path)
	}
	want := []upstream.Dependency{{ProjectID: "REQ"}, {VersionID: "VONLY"}, {Name: "lib.jar"}}
	if !slices.Equal(v.Dependencies, want) {
		t.Fatalf("dependencies = %+v", v.Dependencies)
	}
}

func TestModrinthRealDependency(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_versions_chunky_fabric.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Modrinth, "fALzjamp", upstream.VersionFilter{Loaders: []string{"fabric"}, GameVersion: "1.21.11"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	if !slices.Equal(vs[0].Dependencies, []upstream.Dependency{{ProjectID: "P7dR8mSH"}}) || vs[0].File.Name != "Chunky-Fabric-1.4.55.jar" {
		t.Fatalf("version = %+v", vs[0])
	}
}

// Review Focus #1: null fields and files without a jar never break the decoder.
func TestModrinthNullFields(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/version/nulls":
			_, _ = w.Write([]byte(`{"id":"nulls","project_id":"P","version_number":null,"version_type":null,"date_published":null,
				"game_versions":null,"loaders":null,"files":null,"dependencies":null}`))
		case "/v2/version/zip":
			_, _ = w.Write([]byte(mVersionJSON("zip", "release", "2026-01-01T00:00:00Z",
				`[{"url":"https://cdn.modrinth.com/a.zip","filename":"a.zip","primary":true,"size":1,"hashes":{}},
				  {"url":"https://cdn.modrinth.com/b.JAR","filename":"b.JAR","primary":false,"size":2,"hashes":{"sha512":"x"}}]`, "[]")))
		case "/v2/version/nojar":
			_, _ = w.Write([]byte(mVersionJSON("nojar", "release", "2026-01-01T00:00:00Z",
				`[{"url":"https://cdn.modrinth.com/a.zip","filename":"a.zip","primary":true,"size":1,"hashes":{}}]`, "[]")))
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	v, err := c.Version(bg, upstream.Modrinth, "", "nulls")
	if err != nil || v.Downloadable() || v.Name != "nulls" || v.Channel != upstream.Alpha || len(v.Dependencies) != 0 {
		t.Fatalf("nulls = %+v, %v", v, err)
	}
	if v, _ := c.Version(bg, upstream.Modrinth, "", "zip"); v.File == nil || v.File.Name != "b.JAR" {
		t.Fatalf("zip = %+v", v.File)
	}
	if v, _ := c.Version(bg, upstream.Modrinth, "", "nojar"); v.Downloadable() {
		t.Fatalf("nojar downloadable: %+v", v.File)
	}
}

func TestModrinthInvalidRefs(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	if _, err := c.Project(bg, upstream.Modrinth, "../x"); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("project: %v", err)
	}
	if _, err := c.Version(bg, upstream.Modrinth, "", "a/b"); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("version: %v", err)
	}
	if _, err := c.Versions(bg, upstream.Modrinth, "", upstream.VersionFilter{}); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("versions: %v", err)
	}
	if _, err := c.Project(bg, upstream.Source("curseforge"), "x"); !errors.Is(err, upstream.ErrNotFound) {
		t.Errorf("unknown source: %v", err)
	}
	if n := api.total(); n != 0 {
		t.Fatalf("invalid refs reached the upstream %d times", n)
	}
}

type hashBody struct {
	Hashes       []string `json:"hashes"`
	Algorithm    string   `json:"algorithm"`
	Loaders      []string `json:"loaders"`
	GameVersions []string `json:"game_versions"`
	VersionTypes []string `json:"version_types"`
}

func TestModrinthVersionsByHash(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		var in hashBody
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Algorithm != "sha512" {
			t.Errorf("body: %+v, %v", in, err)
		}
		if slices.Contains(in.Hashes, luckperms553) {
			_, _ = w.Write(testdata(t, "modrinth_version_files.json"))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	clk := clock.NewFake(t0)
	c := newClient(t, api, nil, clk)
	got, err := c.VersionsByHash(bg, []string{strings.ToUpper(luckperms553), unknownHash, "not-a-hash", luckperms553})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[luckperms553].ID != "MBSY8toc" || got[luckperms553].ProjectID != "Vebnzrzj" {
		t.Fatalf("result = %+v", got)
	}
	_, body := api.lastRequest()
	var in hashBody
	_ = json.Unmarshal(body, &in)
	if !slices.Equal(in.Hashes, []string{unknownHash, luckperms553}) {
		t.Fatalf("hashes sent = %v (want sorted, unique, lower case, valid only)", in.Hashes)
	}
	// known and unknown hashes are cached for an hour
	clk.Advance(59 * time.Minute)
	if again, _ := c.VersionsByHash(bg, []string{luckperms553, unknownHash}); len(again) != 1 {
		t.Fatalf("cached result = %+v", again)
	}
	if n := api.count("POST /v2/version_files"); n != 1 {
		t.Fatalf("posted %d times", n)
	}
	clk.Advance(time.Minute)
	_, _ = c.VersionsByHash(bg, []string{unknownHash})
	if n := api.count("POST /v2/version_files"); n != 2 {
		t.Fatalf("posted %d times after an hour", n)
	}
}

func TestModrinthHashBatches(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	hashes := make([]string, 0, 600)
	for i := range 600 {
		hashes = append(hashes, hexHash(i))
	}
	if _, err := c.VersionsByHash(bg, hashes); err != nil {
		t.Fatal(err)
	}
	if n := api.count("POST /v2/version_files"); n != 2 {
		t.Fatalf("posted %d times for 600 hashes, want 2 (500 per batch)", n)
	}
	if _, err := c.VersionsByHash(bg, []string{"nope"}); err != nil || api.count("POST /v2/version_files") != 2 {
		t.Fatalf("invalid hashes only must not call the upstream: %v", err)
	}
}

// hexHash returns a valid, distinct sha512 hex string for i.
func hexHash(i int) string {
	const digits = "0123456789abcdef"
	b := []byte(strings.Repeat("0", 128))
	for p := 127; i > 0; p-- {
		b[p] = digits[i%16]
		i /= 16
	}
	return string(b)
}

func TestModrinthLatestByHash(t *testing.T) {
	api := newAPI(t, serveFile(t, "modrinth_version_files_update.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	got, err := c.LatestByHash(bg, []string{luckperms553}, paperLoaders, []string{"1.21.11"}, []upstream.Channel{upstream.Release, upstream.Beta})
	if err != nil {
		t.Fatal(err)
	}
	if v := got[luckperms553]; v.ID != "b0mk8uS6" || v.Name != "v5.5.71-bukkit" || !v.Downloadable() {
		t.Fatalf("latest = %+v", v)
	}
	req, body := api.lastRequest()
	var in hashBody
	_ = json.Unmarshal(body, &in)
	if req.URL.Path != "/v2/version_files/update" || in.Algorithm != "sha512" || !slices.Equal(in.Loaders, paperLoaders) ||
		!slices.Equal(in.GameVersions, []string{"1.21.11"}) || !slices.Equal(in.VersionTypes, []string{"release", "beta"}) {
		t.Fatalf("request = %s %+v", req.URL.Path, in)
	}
	_, _ = c.LatestByHash(bg, []string{luckperms553}, paperLoaders, []string{"1.21.11"}, []upstream.Channel{upstream.Release})
	if n := api.count("POST /v2/version_files/update"); n != 2 {
		t.Fatalf("update lookups are not cached here (the plugins package caches per server): %d", n)
	}
}
```

- [ ] **Step 5: Rot prüfen**

Run: `git add -N internal/plugins/upstream && make check`
Expected: FAIL beim Kompilieren der Tests (`undefined: upstream.NewCache`, `upstream.SearchQuery`, `(*Client).Search` …).

- [ ] **Step 6: Implementierung schreiben**

`internal/plugins/upstream/http.go` (ersetzt die Phase-A-Datei):

```go
// Package upstream talks to Modrinth and Hangar (sub-project 3c, spec §4): search, projects,
// versions, hash lookups, jar downloads and icons. Only the fixed API bases are queried; jars
// and icons come only from AllowedHosts over https.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

const (
	// APITimeout bounds every API request (spec §4).
	APITimeout = 10 * time.Second
	// ReadTimeout bounds every single read of a jar or icon body; downloads have no overall
	// deadline.
	ReadTimeout = 60 * time.Second
	// MaxJSONBytes limits every API answer.
	MaxJSONBytes = 5 << 20
	// MaxJarBytes limits a jar (the size the API announces).
	MaxJarBytes = 256 << 20
	// MaxIconBytes limits an icon.
	MaxIconBytes = 256 << 10
	// MaxIconURLLen limits the icon proxy's url parameter.
	MaxIconURLLen = 512
	// MaxRedirects is the most redirects any request follows.
	MaxRedirects = 3
	// PageSize is the number of search hits per page.
	PageSize = 20
	// MaxVersions is the length of a version list.
	MaxVersions = 25
	// MaxHashBatch is the most hashes per Modrinth version_files request.
	MaxHashBatch = 500
	// MaxCacheEntries bounds every cache (oldest first out).
	MaxCacheEntries = 1000

	searchTTL  = 5 * time.Minute
	projectTTL = 15 * time.Minute
	hashTTL    = time.Hour
)

// Source is a plugin source.
type Source string

const (
	Modrinth Source = "modrinth"
	Hangar   Source = "hangar"
)

func (s Source) Valid() bool { return s == Modrinth || s == Hangar }

// Channel is the stability of a version.
type Channel string

const (
	Release Channel = "release"
	Beta    Channel = "beta"
	Alpha   Channel = "alpha"
)

// Bases are the API base URLs (scheme and host, no trailing slash).
type Bases struct{ Modrinth, Hangar string }

// DefaultBases are the production APIs, checked against the live services 2026-10-12.
var DefaultBases = Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}

// AllowedHosts are the only hosts jars and icons are fetched from (https only).
var AllowedHosts = []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}

type Options struct {
	Clock   clock.Clock
	Version string // panel version for the User-Agent; "" = "dev"
	Bases   Bases  // zero = DefaultBases
	// HTTPClient: only its Transport is used (nil = http.DefaultTransport); tests inject the
	// transport of an httptest TLS server.
	HTTPClient *http.Client
	// AllowHost overrides the download/icon host check in tests (nil = DefaultHostAllowed).
	AllowHost   func(u *url.URL) bool
	APITimeout  time.Duration // 0 = APITimeout
	ReadTimeout time.Duration // 0 = ReadTimeout
}

var (
	// ErrUnavailable: network, status, size, JSON or rate limit (429) of an upstream.
	ErrUnavailable = errors.New("upstream: unavailable")
	// ErrNotFound: the upstream does not know the project or version (404), or the reference
	// is malformed.
	ErrNotFound = errors.New("upstream: not found")
	// ErrDownload: a jar or icon could not be fetched as announced (host, size, status, data).
	ErrDownload = errors.New("upstream: download failed")
	// ErrHostNotAllowed: a jar or icon URL (or a redirect) points outside AllowedHosts.
	ErrHostNotAllowed = errors.New("upstream: host not allowed")
	// ErrNotImage: an icon is not PNG, JPEG, WebP or GIF.
	ErrNotImage = errors.New("upstream: not a supported image")
)

// Client queries Modrinth and Hangar. It is safe for concurrent use.
type Client struct {
	clock       clock.Clock
	ua          string
	bases       Bases
	allowHost   func(*url.URL) bool
	api         *http.Client
	dl          *http.Client
	apiTimeout  time.Duration
	readTimeout time.Duration

	search    *Cache[SearchResult]
	projects  *Cache[Project]
	versions  *Cache[[]Version]
	version   *Cache[Version]
	hashes    *Cache[hashEntry]
	hashCalls *Cache[map[string]Version]
}

// hashEntry caches one hash lookup; known false means Modrinth does not know the file.
type hashEntry struct {
	v     Version
	known bool
}

func New(o Options) *Client {
	clk := o.Clock
	if clk == nil {
		clk = clock.Real{}
	}
	c := &Client{
		clock: clk, ua: catalog.UserAgent(o.Version), bases: o.Bases, allowHost: o.AllowHost,
		apiTimeout: o.APITimeout, readTimeout: o.ReadTimeout,
	}
	if c.bases == (Bases{}) {
		c.bases = DefaultBases
	}
	if c.allowHost == nil {
		c.allowHost = DefaultHostAllowed
	}
	if c.apiTimeout <= 0 {
		c.apiTimeout = APITimeout
	}
	if c.readTimeout <= 0 {
		c.readTimeout = ReadTimeout
	}
	tr := http.DefaultTransport
	if o.HTTPClient != nil && o.HTTPClient.Transport != nil {
		tr = o.HTTPClient.Transport
	}
	c.api = &http.Client{Transport: tr, Timeout: c.apiTimeout, CheckRedirect: sameHostHTTPS}
	c.dl = &http.Client{Transport: tr, CheckRedirect: c.allowedRedirect}
	c.search = NewCache[SearchResult](clk, searchTTL, MaxCacheEntries)
	c.projects = NewCache[Project](clk, projectTTL, MaxCacheEntries)
	c.versions = NewCache[[]Version](clk, projectTTL, MaxCacheEntries)
	c.version = NewCache[Version](clk, projectTTL, MaxCacheEntries)
	c.hashes = NewCache[hashEntry](clk, hashTTL, MaxCacheEntries)
	c.hashCalls = NewCache[map[string]Version](clk, 0, MaxCacheEntries)
	return c
}

// UserAgent is sent on every request (like the version catalogs).
func (c *Client) UserAgent() string { return c.ua }

func (c *Client) Bases() Bases { return c.bases }

// HostAllowed reports whether jars and icons may be fetched from u.
func (c *Client) HostAllowed(u *url.URL) bool { return u != nil && c.allowHost(u) }

// DefaultHostAllowed accepts https URLs on exactly one of AllowedHosts (default port, no user
// info).
func DefaultHostAllowed(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return slices.Contains(AllowedHosts, strings.ToLower(u.Hostname()))
}

// sameHostHTTPS follows an API redirect only to https on the host of the original request, at
// most MaxRedirects times.
func sameHostHTTPS(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("stopped after %d redirects", MaxRedirects)
	}
	if req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("redirect to %s refused: only https on the same host", req.URL.Redacted())
	}
	return nil
}

// allowedRedirect follows a download redirect only to an allowed host, at most MaxRedirects
// times.
func (c *Client) allowedRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > MaxRedirects {
		return fmt.Errorf("%w: more than %d redirects", ErrDownload, MaxRedirects)
	}
	if !c.allowHost(req.URL) {
		return fmt.Errorf("%w: %w: redirect to %s", ErrDownload, ErrHostNotAllowed, req.URL.Redacted())
	}
	return nil
}

// doJSON sends a request to one of the fixed APIs and decodes at most MaxJSONBytes of JSON
// into out; in (if not nil) is sent as a JSON body.
func (c *Client) doJSON(ctx context.Context, method, rawURL string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.apiTimeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%w: encode request: %w", ErrUnavailable, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrUnavailable, err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.api.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%w: %s %s", ErrNotFound, method, rawURL)
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s %s: rate limited by the source (HTTP 429)", ErrUnavailable, method, rawURL)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: %s %s: HTTP %d", ErrUnavailable, method, rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxJSONBytes+1))
	if err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, rawURL, err)
	}
	if len(b) > MaxJSONBytes {
		return fmt.Errorf("%w: %s %s: answer larger than %d bytes", ErrUnavailable, method, rawURL, MaxJSONBytes)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, rawURL, err)
	}
	return nil
}
```

`internal/plugins/upstream/types.go`:

```go
package upstream

import (
	"regexp"
	"slices"
	"time"
)

// SearchQuery is one search; ProjectType, Loaders and ServerSideOnly apply to Modrinth.
type SearchQuery struct {
	Query          string
	Offset         int
	ProjectType    string   // "plugin" | "mod"
	Loaders        []string // OR-ed categories facet
	GameVersion    string
	ServerSideOnly bool // server_side required OR optional (mods)
}

type SearchHit struct {
	ProjectID, Slug, Owner, Title, Description, Author, IconURL string
	Downloads                                                   int64
}

type SearchResult struct {
	Hits  []SearchHit // never nil
	Total int
}

// Project is a Modrinth or Hangar project; Hangar's ID is its numeric id as text.
type Project struct {
	Source                                       Source
	ID, Slug, Owner, Title, Description, IconURL string
	ServerSide                                   string // Modrinth server_side; "" for Hangar
}

// ClientOnly reports a Modrinth project that does not run on a server.
func (p Project) ClientOnly() bool { return p.ServerSide == "unsupported" }

// File is the downloadable jar of a version.
type File struct {
	URL, Name      string
	Size           int64
	SHA512, SHA256 string // lowercase hex; Modrinth sets SHA512, Hangar SHA256
}

// Dependency is a required dependency of a version.
type Dependency struct {
	ProjectID   string // "" = not a project (external link or Modrinth file name only)
	VersionID   string // Modrinth: exactly this version; "" = newest compatible
	Name        string // Hangar dependency name or Modrinth file_name
	ExternalURL string // Hangar externalUrl
}

type Version struct {
	Source       Source
	ID           string // Modrinth version id, Hangar version name
	ProjectID    string
	Name         string // Modrinth version_number, Hangar name
	Channel      Channel
	PublishedAt  time.Time
	GameVersions []string // Modrinth game_versions, Hangar platformDependencies.PAPER
	Loaders      []string // Modrinth only
	File         *File    // nil = no downloadable jar
	ExternalURL  string   // Hangar: only available externally
	Dependencies []Dependency
}

func (v Version) Downloadable() bool { return v.File != nil }

// VersionFilter selects compatible versions.
type VersionFilter struct {
	Loaders     []string // Modrinth only
	GameVersion string
	ReleaseOnly bool // Hangar: channel=Release; Modrinth: filtered locally
}

// Icon is a fetched project icon.
type Icon struct {
	Data        []byte
	ContentType string
}

// refRe bounds every project, version and hash reference before it becomes part of a URL.
var refRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)

func validRef(s string) bool { return refRe.MatchString(s) }

func sortNewestFirst(vs []Version) {
	slices.SortStableFunc(vs, func(a, b Version) int { return b.PublishedAt.Compare(a.PublishedAt) })
}
```

`internal/plugins/upstream/cache.go`:

```go
package upstream

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// Cache keeps values for a TTL and lets concurrent Gets of the same key share one fetch. At
// most max entries are kept; the oldest is evicted first. A TTL of 0 stores nothing and only
// shares concurrent calls. Failures are never stored.
type Cache[V any] struct {
	clock clock.Clock
	ttl   time.Duration
	max   int

	mu       sync.Mutex
	entries  map[string]cacheEntry[V]
	inflight map[string]*cacheCall[V]
}

type cacheEntry[V any] struct {
	at time.Time
	v  V
}

type cacheCall[V any] struct {
	done chan struct{}
	v    V
	err  error
}

// NewCache returns an empty cache; maxEntries <= 0 means MaxCacheEntries.
func NewCache[V any](clk clock.Clock, ttl time.Duration, maxEntries int) *Cache[V] {
	if maxEntries <= 0 {
		maxEntries = MaxCacheEntries
	}
	return &Cache[V]{clock: clk, ttl: ttl, max: maxEntries, entries: map[string]cacheEntry[V]{}, inflight: map[string]*cacheCall[V]{}}
}

// Get returns the value of key from the cache or from fetch. The fetch runs detached from the
// caller (context.WithoutCancel, values kept) so that other callers waiting for it are not hurt
// when the first one goes away; a caller whose ctx ends gets ErrUnavailable at once.
func (c *Cache[V]) Get(ctx context.Context, key string, fetch func(context.Context) (V, error)) (V, error) {
	c.mu.Lock()
	if v, ok := c.peekLocked(key); ok {
		c.mu.Unlock()
		return v, nil
	}
	cl, ok := c.inflight[key]
	if !ok {
		cl = &cacheCall[V]{done: make(chan struct{})}
		c.inflight[key] = cl
		go c.run(context.WithoutCancel(ctx), key, fetch, cl)
	}
	c.mu.Unlock()
	select {
	case <-cl.done:
		return cl.v, cl.err
	case <-ctx.Done():
		var zero V
		return zero, fmt.Errorf("%w: %w", ErrUnavailable, ctx.Err())
	}
}

func (c *Cache[V]) run(ctx context.Context, key string, fetch func(context.Context) (V, error), cl *cacheCall[V]) {
	v, err := fetch(ctx)
	c.mu.Lock()
	if err == nil && c.ttl > 0 {
		c.putLocked(key, v)
	}
	delete(c.inflight, key)
	c.mu.Unlock()
	cl.v, cl.err = v, err
	close(cl.done)
}

// Peek returns a fresh cached value without fetching.
func (c *Cache[V]) Peek(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peekLocked(key)
}

// Put stores v under key (a TTL of 0 still stores nothing).
func (c *Cache[V]) Put(key string, v V) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, v)
}

func (c *Cache[V]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *Cache[V]) peekLocked(key string) (V, bool) {
	e, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if c.clock.Now().Sub(e.at) >= c.ttl {
		delete(c.entries, key)
		var zero V
		return zero, false
	}
	return e.v, true
}

func (c *Cache[V]) putLocked(key string, v V) {
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.max {
		oldest, first := "", true
		var at time.Time
		for k, e := range c.entries {
			if first || e.at.Before(at) {
				oldest, at, first = k, e.at, false
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = cacheEntry[V]{at: c.clock.Now(), v: v}
}
```

`internal/plugins/upstream/api.go`:

```go
package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func errSource(src Source) error { return fmt.Errorf("%w: unknown source %q", ErrNotFound, src) }

// cacheKey is a collision-free cache key for any JSON-encodable value.
func cacheKey(parts ...any) string {
	b, _ := json.Marshal(parts) // only strings, bools, ints and string slices
	return string(b)
}

// Search runs a search (cached 5 min).
func (c *Client) Search(ctx context.Context, src Source, q SearchQuery) (SearchResult, error) {
	if !src.Valid() {
		return SearchResult{}, errSource(src)
	}
	return c.search.Get(ctx, cacheKey(src, q), func(ctx context.Context) (SearchResult, error) {
		if src == Hangar {
			return c.hangarSearch(ctx, q)
		}
		return c.modrinthSearch(ctx, q)
	})
}

// Project returns a project by id or slug (cached 15 min).
func (c *Client) Project(ctx context.Context, src Source, id string) (Project, error) {
	if !src.Valid() {
		return Project{}, errSource(src)
	}
	if !validRef(id) {
		return Project{}, fmt.Errorf("%w: invalid project %q", ErrNotFound, id)
	}
	return c.projects.Get(ctx, cacheKey(src, id), func(ctx context.Context) (Project, error) {
		if src == Hangar {
			return c.hangarProject(ctx, id)
		}
		return c.modrinthProject(ctx, id)
	})
}

// Versions returns the versions of project that match f, newest first, at most MaxVersions
// (the full list is cached 15 min).
func (c *Client) Versions(ctx context.Context, src Source, project string, f VersionFilter) ([]Version, error) {
	if !src.Valid() {
		return nil, errSource(src)
	}
	if !validRef(project) {
		return nil, fmt.Errorf("%w: invalid project %q", ErrNotFound, project)
	}
	hangarRelease := src == Hangar && f.ReleaseOnly
	loaders := f.Loaders
	if src == Hangar {
		loaders = nil
	}
	vs, err := c.versions.Get(ctx, cacheKey(src, project, loaders, f.GameVersion, hangarRelease), func(ctx context.Context) ([]Version, error) {
		if src == Hangar {
			return c.hangarVersions(ctx, project, f.GameVersion, hangarRelease)
		}
		return c.modrinthVersions(ctx, project, loaders, f.GameVersion)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, min(len(vs), MaxVersions))
	for _, v := range vs {
		if f.ReleaseOnly && v.Channel != Release {
			continue
		}
		out = append(out, v)
		if len(out) == MaxVersions {
			break
		}
	}
	return out, nil
}

// Version returns one version (cached 15 min). Modrinth ignores project; Hangar needs it.
func (c *Client) Version(ctx context.Context, src Source, project, id string) (Version, error) {
	if !src.Valid() {
		return Version{}, errSource(src)
	}
	if !validRef(id) || (src == Hangar && !validRef(project)) {
		return Version{}, fmt.Errorf("%w: invalid version %q of %q", ErrNotFound, id, project)
	}
	if src == Modrinth {
		project = ""
	}
	return c.version.Get(ctx, cacheKey(src, project, id), func(ctx context.Context) (Version, error) {
		if src == Hangar {
			return c.hangarVersion(ctx, project, id)
		}
		return c.modrinthVersion(ctx, id)
	})
}

// normHashes returns the valid sha512 hex strings of hs, lower case, sorted and unique.
func normHashes(hs []string) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		h = strings.ToLower(h)
		if len(h) == 128 && strings.Trim(h, "0123456789abcdef") == "" {
			out = append(out, h)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// VersionsByHash maps sha512 hashes to the Modrinth versions they belong to; unknown hashes are
// missing from the map. Known and unknown hashes are cached for an hour; concurrent lookups of
// the same batch share one request.
func (c *Client) VersionsByHash(ctx context.Context, sha512s []string) (map[string]Version, error) {
	out := map[string]Version{}
	var missing []string
	for _, h := range normHashes(sha512s) {
		if e, ok := c.hashes.Peek(h); ok {
			if e.known {
				out[h] = e.v
			}
			continue
		}
		missing = append(missing, h)
	}
	for chunk := range slices.Chunk(missing, MaxHashBatch) {
		sum := sha256.Sum256([]byte(strings.Join(chunk, ",")))
		got, err := c.hashCalls.Get(ctx, hex.EncodeToString(sum[:]), func(ctx context.Context) (map[string]Version, error) {
			return c.modrinthVersionFiles(ctx, chunk)
		})
		if err != nil {
			return nil, err
		}
		for _, h := range chunk {
			v, ok := got[h]
			c.hashes.Put(h, hashEntry{v: v, known: ok})
			if ok {
				out[h] = v
			}
		}
	}
	return out, nil
}

// LatestByHash asks Modrinth for the newest version per hash that matches loaders, game
// versions and channels (not cached: the plugins package caches the result per server).
func (c *Client) LatestByHash(ctx context.Context, sha512s, loaders, gameVersions []string, types []Channel) (map[string]Version, error) {
	out := map[string]Version{}
	ts := make([]string, 0, len(types))
	for _, t := range types {
		ts = append(ts, string(t))
	}
	for chunk := range slices.Chunk(normHashes(sha512s), MaxHashBatch) {
		got, err := c.modrinthLatest(ctx, chunk, loaders, gameVersions, ts)
		if err != nil {
			return nil, err
		}
		for h, v := range got {
			out[h] = v
		}
	}
	return out, nil
}
```

`internal/plugins/upstream/modrinth.go`:

```go
package upstream

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Modrinth wire types: only the fields the panel uses (unknown fields are ignored, null leaves
// the zero value).
type mSearch struct {
	Hits []struct {
		ProjectID   string `json:"project_id"`
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Author      string `json:"author"`
		Downloads   int64  `json:"downloads"`
		IconURL     string `json:"icon_url"`
	} `json:"hits"`
	TotalHits int `json:"total_hits"`
}

type mProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	ServerSide  string `json:"server_side"`
}

type mFile struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	Hashes   struct {
		SHA512 string `json:"sha512"`
	} `json:"hashes"`
}

type mVersion struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	VersionNumber string    `json:"version_number"`
	VersionType   string    `json:"version_type"`
	DatePublished time.Time `json:"date_published"`
	GameVersions  []string  `json:"game_versions"`
	Loaders       []string  `json:"loaders"`
	Files         []mFile   `json:"files"`
	Dependencies  []struct {
		ProjectID      string `json:"project_id"`
		VersionID      string `json:"version_id"`
		FileName       string `json:"file_name"`
		DependencyType string `json:"dependency_type"`
	} `json:"dependencies"`
}

func modrinthChannel(t string) Channel {
	switch t {
	case "release":
		return Release
	case "beta":
		return Beta
	}
	return Alpha // unknown types count as the least stable
}

// pickModrinthFile returns the primary jar, else the first jar, else nil.
func pickModrinthFile(files []mFile) *File {
	isJar := func(f mFile) bool { return f.URL != "" && strings.HasSuffix(strings.ToLower(f.Filename), ".jar") }
	i := slices.IndexFunc(files, func(f mFile) bool { return f.Primary && isJar(f) })
	if i < 0 {
		i = slices.IndexFunc(files, isJar)
	}
	if i < 0 {
		return nil
	}
	f := files[i]
	return &File{URL: f.URL, Name: f.Filename, Size: f.Size, SHA512: strings.ToLower(f.Hashes.SHA512)}
}

func (m mVersion) norm() Version {
	v := Version{
		Source: Modrinth, ID: m.ID, ProjectID: m.ProjectID, Name: cmp.Or(m.VersionNumber, m.ID),
		Channel: modrinthChannel(m.VersionType), PublishedAt: m.DatePublished,
		GameVersions: m.GameVersions, Loaders: m.Loaders, File: pickModrinthFile(m.Files),
	}
	for _, d := range m.Dependencies {
		if d.DependencyType == "required" {
			v.Dependencies = append(v.Dependencies, Dependency{ProjectID: d.ProjectID, VersionID: d.VersionID, Name: d.FileName})
		}
	}
	return v
}

func (c *Client) modrinthSearch(ctx context.Context, q SearchQuery) (SearchResult, error) {
	facets := [][]string{{"project_type:" + q.ProjectType}}
	if len(q.Loaders) > 0 {
		or := make([]string, 0, len(q.Loaders))
		for _, l := range q.Loaders {
			or = append(or, "categories:"+l)
		}
		facets = append(facets, or)
	}
	if q.GameVersion != "" {
		facets = append(facets, []string{"versions:" + q.GameVersion})
	}
	if q.ServerSideOnly {
		facets = append(facets, []string{"server_side:required", "server_side:optional"})
	}
	fb, err := json.Marshal(facets)
	if err != nil {
		return SearchResult{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	index := "downloads"
	if q.Query != "" {
		index = "relevance"
	}
	v := url.Values{
		"query": {q.Query}, "facets": {string(fb)}, "index": {index},
		"offset": {strconv.Itoa(q.Offset)}, "limit": {strconv.Itoa(PageSize)},
	}
	var resp mSearch
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/search?"+v.Encode(), nil, &resp); err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Hits: make([]SearchHit, 0, len(resp.Hits)), Total: resp.TotalHits}
	for _, h := range resp.Hits {
		if !validRef(h.ProjectID) {
			continue
		}
		out.Hits = append(out.Hits, SearchHit{
			ProjectID: h.ProjectID, Slug: h.Slug, Title: h.Title, Description: h.Description,
			Author: h.Author, IconURL: h.IconURL, Downloads: h.Downloads,
		})
	}
	return out, nil
}

func (c *Client) modrinthProject(ctx context.Context, id string) (Project, error) {
	var m mProject
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/project/"+url.PathEscape(id), nil, &m); err != nil {
		return Project{}, err
	}
	if !validRef(m.ID) {
		return Project{}, fmt.Errorf("%w: modrinth project %q without a valid id", ErrUnavailable, id)
	}
	return Project{
		Source: Modrinth, ID: m.ID, Slug: m.Slug, Title: cmp.Or(m.Title, m.Slug, m.ID), Description: m.Description,
		IconURL: m.IconURL, ServerSide: m.ServerSide,
	}, nil
}

func (c *Client) modrinthVersions(ctx context.Context, project string, loaders []string, gv string) ([]Version, error) {
	q := url.Values{}
	if len(loaders) > 0 {
		lb, _ := json.Marshal(loaders)
		q.Set("loaders", string(lb))
	}
	if gv != "" {
		gb, _ := json.Marshal([]string{gv})
		q.Set("game_versions", string(gb))
	}
	u := c.bases.Modrinth + "/v2/project/" + url.PathEscape(project) + "/version"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var resp []mVersion
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(resp))
	for _, m := range resp {
		if validRef(m.ID) {
			out = append(out, m.norm())
		}
	}
	sortNewestFirst(out)
	return out, nil
}

func (c *Client) modrinthVersion(ctx context.Context, id string) (Version, error) {
	var m mVersion
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/version/"+url.PathEscape(id), nil, &m); err != nil {
		return Version{}, err
	}
	if !validRef(m.ID) {
		return Version{}, fmt.Errorf("%w: modrinth version %q without a valid id", ErrUnavailable, id)
	}
	return m.norm(), nil
}

func (c *Client) modrinthVersionFiles(ctx context.Context, hashes []string) (map[string]Version, error) {
	var resp map[string]mVersion
	in := map[string]any{"hashes": hashes, "algorithm": "sha512"}
	if err := c.doJSON(ctx, http.MethodPost, c.bases.Modrinth+"/v2/version_files", in, &resp); err != nil {
		return nil, err
	}
	return normByHash(resp), nil
}

func (c *Client) modrinthLatest(ctx context.Context, hashes, loaders, gameVersions, types []string) (map[string]Version, error) {
	var resp map[string]mVersion
	in := map[string]any{
		"hashes": hashes, "algorithm": "sha512", "loaders": loaders,
		"game_versions": gameVersions, "version_types": types,
	}
	if err := c.doJSON(ctx, http.MethodPost, c.bases.Modrinth+"/v2/version_files/update", in, &resp); err != nil {
		return nil, err
	}
	return normByHash(resp), nil
}

func normByHash(resp map[string]mVersion) map[string]Version {
	out := make(map[string]Version, len(resp))
	for h, m := range resp {
		if validRef(m.ID) {
			out[strings.ToLower(h)] = m.norm()
		}
	}
	return out
}
```

`internal/plugins/upstream/hangar.go` (Übergang bis Aufgabe 8):

```go
package upstream

import (
	"context"
	"fmt"
)

var errHangarPending = fmt.Errorf("%w: the hangar client follows in task 8", ErrUnavailable)

func (c *Client) hangarSearch(context.Context, SearchQuery) (SearchResult, error) {
	return SearchResult{}, errHangarPending
}

func (c *Client) hangarProject(context.Context, string) (Project, error) { return Project{}, errHangarPending }

func (c *Client) hangarVersions(context.Context, string, string, bool) ([]Version, error) {
	return nil, errHangarPending
}

func (c *Client) hangarVersion(context.Context, string, string) (Version, error) {
	return Version{}, errHangarPending
}
```

- [ ] **Step 7: Checks und CI**

Run: `make check`
Expected: PASS (gofmt, vet, Build für macOS und Linux, tsc).

```bash
git add internal/plugins/upstream
git commit -m "feat(plugins): modrinth client with cache and redirect rules

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/plugins-panel
gh pr create --draft --base main --head feat/plugins-panel --title "feat(plugins): panel side of plugins and mods (sub-project 3c)" --body "Phase B2 of docs/superpowers/plans/2026-10-12-plugins-03-panel.md (tasks 7-12).

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
gh pr checks --watch
```

Expected: CI `go test` PASS für `internal/plugins/upstream` (`TestDefaultHostAllowed`, `TestAPIRedirects`, `TestAPIErrors`, `TestAPITimeout`, `TestCache*`, `TestModrinth*`).

---

### Task 8: Hangar, Jar-Download, Icons und Dateinamen

Ersetzt die Hangar-Übergangsdatei durch den echten Client und ergänzt den Download (Host-Allowlist, Größen, Fristen), den Icon-Abruf und die Dateinamen-Regel.

**Files:**
- Modify (vollständig ersetzen): `internal/plugins/upstream/hangar.go`
- Create: `internal/plugins/upstream/download.go`, `icon.go`, `filename.go`
- Test: `internal/plugins/upstream/hangar_test.go`, `download_test.go`, `icon_test.go`, `filename_test.go`, `testdata/hangar_*.json`

**Interfaces:**
- Consumes: Aufgabe 7 (`Client`, `doJSON`, `validRef`, `sortNewestFirst`, `ErrDownload`, `ErrHostNotAllowed`, `ErrUnavailable`, `ErrNotImage`, `PageSize`, `MaxVersions`, `MaxJarBytes`, `MaxIconBytes`, `MaxIconURLLen`); `filesapi.CleanPath`, `filesapi.IsHidden`.
- Produces:

```go
func (c *Client) Download(ctx context.Context, f File) (io.ReadCloser, error)
func (c *Client) Icon(ctx context.Context, raw string) (Icon, error)
func (c *Client) IconURLAllowed(raw string) bool
func DetectImage(b []byte) string
func ValidJarName(name string) bool
func FileName(upstreamName, slug, version string) string
const MaxFileNameLen = 128
```

- [ ] **Step 1: Testdaten anlegen**

```bash
cat > internal/plugins/upstream/testdata/hangar_project_viabackwards.json <<'EOF'
{"createdAt":"2022-12-23T20:47:00.582094Z","publishedAt":"2022-12-23T20:57:21.569868Z","id":12,"name":"ViaBackwards","namespace":{"owner":"ViaVersion","slug":"ViaBackwards"},"stats":{"views":1253902,"downloads":754154,"recentViews":41176,"recentDownloads":22366,"stars":285,"watchers":165},"category":"misc","description":"Allow Java Edition clients with older versions to connect to your Minecraft server","lastUpdated":"2026-10-11T18:39:24.372355Z","visibility":"public","supportedPlatforms":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]},"memberNames":[],"avatarUrl":"https://hangarcdn.papermc.io/avatars/project/12.webp?v=1"}
EOF
cat > internal/plugins/upstream/testdata/hangar_project_viaversion.json <<'EOF'
{"createdAt":"2022-12-31T20:04:25.895662Z","publishedAt":"2023-01-05T16:21:22.608816Z","id":31,"name":"ViaVersion","namespace":{"owner":"ViaVersion","slug":"ViaVersion"},"stats":{"views":1166351,"downloads":508879,"recentViews":48091,"recentDownloads":23287,"stars":470,"watchers":271},"category":"misc","description":"Allow newer Java Edition versions to connect, why lose players after a Minecraft update?","lastUpdated":"2026-10-11T18:09:55.513247Z","visibility":"public","supportedPlatforms":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]},"memberNames":[],"avatarUrl":"https://hangarcdn.papermc.io/avatars/project/31.webp?v=1"}
EOF
cat > internal/plugins/upstream/testdata/hangar_search.json <<'EOF'
{"pagination":{"count":377,"limit":2,"offset":0},"result":[{"createdAt":"2022-12-23T20:47:00.582094Z","publishedAt":"2022-12-23T20:57:21.569868Z","id":12,"name":"ViaBackwards","namespace":{"owner":"ViaVersion","slug":"ViaBackwards"},"stats":{"views":1253895,"downloads":754151,"recentViews":41169,"recentDownloads":22363,"stars":285,"watchers":165},"category":"misc","description":"Allow Java Edition clients with older versions to connect to your Minecraft server","lastUpdated":"2026-10-11T18:39:24.372355Z","visibility":"public","supportedPlatforms":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]},"memberNames":["kennytv","viaversion"],"avatarUrl":"https://hangarcdn.papermc.io/avatars/project/12.webp?v=1"},{"createdAt":"2022-12-31T20:04:25.895662Z","publishedAt":"2023-01-05T16:21:22.608816Z","id":31,"name":"ViaVersion","namespace":{"owner":"ViaVersion","slug":"ViaVersion"},"stats":{"views":1166341,"downloads":508874,"recentViews":48081,"recentDownloads":23282,"stars":470,"watchers":271},"category":"misc","description":"Allow newer Java Edition versions to connect, why lose players after a Minecraft update?","lastUpdated":"2026-10-11T18:09:55.513247Z","visibility":"public","supportedPlatforms":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]},"memberNames":["kennytv","viaversion"],"avatarUrl":"https://hangarcdn.papermc.io/avatars/project/31.webp?v=1"}]}
EOF
cat > internal/plugins/upstream/testdata/hangar_version_viabackwards_5.12.1.json <<'EOF'
{"createdAt":"2026-10-08T13:45:17.08896Z","id":31500,"projectId":12,"name":"5.12.1","visibility":"public","stats":{"totalDownloads":2121,"platformDownloads":{"PAPER":1976,"VELOCITY":145}},"author":"kennytv","reviewState":"reviewed","channel":{"createdAt":"2022-12-23T20:47:00.590915Z","name":"Release","description":null,"color":"#14b8a6","flags":["PINNED","SENDS_NOTIFICATIONS","FROZEN"]},"pinnedStatus":"CHANNEL","downloads":{"PAPER":{"fileInfo":{"name":"ViaBackwards-5.12.1.jar","sizeBytes":1436079,"sha256Hash":"4b9dd5cb7662f532b44627f8491255bf96f586cbd00280d1480442590ad4450d"},"externalUrl":null,"downloadUrl":"https://hangarcdn.papermc.io/plugins/ViaVersion/ViaBackwards/versions/5.12.1/PAPER/ViaBackwards-5.12.1.jar"}},"pluginDependencies":{"PAPER":[{"name":"ViaRewind","projectId":112,"required":false,"externalUrl":null,"platform":"PAPER"},{"name":"ViaVersion","projectId":31,"required":true,"externalUrl":null,"platform":"PAPER"}]},"platformDependencies":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]}}
EOF
cat > internal/plugins/upstream/testdata/hangar_versions_essentials.json <<'EOF'
{"pagination":{"count":7,"limit":1,"offset":0},"result":[{"createdAt":"2026-05-31T15:48:54.559387Z","id":25952,"projectId":23,"name":"2.22.0","visibility":"public","stats":{"totalDownloads":12885,"platformDownloads":{"PAPER":12885}},"author":"mdcfe","reviewState":"reviewed","channel":{"createdAt":"2022-12-24T16:00:15.758715Z","name":"Release","description":null,"color":"#14b8a6","flags":["SENDS_NOTIFICATIONS","FROZEN","PINNED"]},"pinnedStatus":"CHANNEL","downloads":{"PAPER":{"fileInfo":null,"externalUrl":"https://github.com/EssentialsX/Essentials/releases/tag/2.22.0","downloadUrl":null}},"pluginDependencies":{"PAPER":[{"name":"Vault","projectId":null,"required":false,"externalUrl":"https://www.spigotmc.org/resources/vault.34315/","platform":"PAPER"}]},"platformDependencies":{"PAPER":["1.21.11"]}}]}
EOF
cat > internal/plugins/upstream/testdata/hangar_versions_viabackwards.json <<'EOF'
{"pagination":{"count":138,"limit":25,"offset":0},"result":[{"createdAt":"2026-10-11T18:39:24.372355Z","id":31667,"projectId":12,"name":"5.12.2-SNAPSHOT+657","visibility":"public","stats":{"totalDownloads":24,"platformDownloads":{"PAPER":23,"VELOCITY":1}},"author":"kennytv","reviewState":"unreviewed","channel":{"createdAt":"2023-08-10T12:49:29.210401Z","name":"Snapshot","description":"Development builds","color":"#84cc16","flags":[]},"pinnedStatus":"NONE","downloads":{"PAPER":{"fileInfo":{"name":"ViaBackwards-5.12.2-SNAPSHOT.jar","sizeBytes":1437280,"sha256Hash":"4f2f2b37e16cbd8936257e9dcbd2179ac82002cfe1998c408056c563f8d02c8d"},"externalUrl":null,"downloadUrl":"https://hangarcdn.papermc.io/plugins/ViaVersion/ViaBackwards/versions/5.12.2-SNAPSHOT%2B657/PAPER/ViaBackwards-5.12.2-SNAPSHOT.jar"}},"pluginDependencies":{"PAPER":[{"name":"ViaVersion","projectId":31,"required":true,"externalUrl":null,"platform":"PAPER"},{"name":"ViaRewind","projectId":112,"required":false,"externalUrl":null,"platform":"PAPER"}]},"platformDependencies":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]}},{"createdAt":"2026-10-08T13:45:17.08896Z","id":31500,"projectId":12,"name":"5.12.1","visibility":"public","stats":{"totalDownloads":2104,"platformDownloads":{"PAPER":1962,"VELOCITY":142}},"author":"kennytv","reviewState":"reviewed","channel":{"createdAt":"2022-12-23T20:47:00.590915Z","name":"Release","description":null,"color":"#14b8a6","flags":["SENDS_NOTIFICATIONS","FROZEN","PINNED"]},"pinnedStatus":"CHANNEL","downloads":{"PAPER":{"fileInfo":{"name":"ViaBackwards-5.12.1.jar","sizeBytes":1436079,"sha256Hash":"4b9dd5cb7662f532b44627f8491255bf96f586cbd00280d1480442590ad4450d"},"externalUrl":null,"downloadUrl":"https://hangarcdn.papermc.io/plugins/ViaVersion/ViaBackwards/versions/5.12.1/PAPER/ViaBackwards-5.12.1.jar"}},"pluginDependencies":{"PAPER":[{"name":"ViaVersion","projectId":31,"required":true,"externalUrl":null,"platform":"PAPER"},{"name":"ViaRewind","projectId":112,"required":false,"externalUrl":null,"platform":"PAPER"}]},"platformDependencies":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]}}]}
EOF
cat > internal/plugins/upstream/testdata/hangar_versions_viaversion_release.json <<'EOF'
{"pagination":{"count":12,"limit":2,"offset":0},"result":[{"createdAt":"2026-10-08T13:42:39.251141Z","id":31499,"projectId":31,"name":"5.12.1","visibility":"public","stats":{"totalDownloads":2462,"platformDownloads":{"PAPER":2258,"VELOCITY":204}},"author":"kennytv","reviewState":"reviewed","channel":{"createdAt":"2022-12-31T20:04:25.916259Z","name":"Release","description":null,"color":"#14b8a6","flags":["FROZEN","SENDS_NOTIFICATIONS","PINNED"]},"pinnedStatus":"CHANNEL","downloads":{"PAPER":{"fileInfo":{"name":"ViaVersion-5.12.1.jar","sizeBytes":6503216,"sha256Hash":"bab94b846e1d2bf516c070562d0271b32382afd41870bf0d3a7993b73a36f791"},"externalUrl":null,"downloadUrl":"https://hangarcdn.papermc.io/plugins/ViaVersion/ViaVersion/versions/5.12.1/PAPER/ViaVersion-5.12.1.jar"}},"pluginDependencies":{"PAPER":[{"name":"ViaBackwards","projectId":12,"required":false,"externalUrl":null,"platform":"PAPER"},{"name":"ViaRewind","projectId":112,"required":false,"externalUrl":null,"platform":"PAPER"}]},"platformDependencies":{"PAPER":["1.21.10","1.21.11","26.2","26.3"]}}]}
EOF
```

- [ ] **Step 2: Tests schreiben**

`internal/plugins/upstream/hangar_test.go`:

```go
package upstream_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestHangarSearch(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_search.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	res, err := c.Search(bg, upstream.Hangar, upstream.SearchQuery{Query: "via", GameVersion: "1.21.11", Offset: 40})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	if req.URL.Path != "/api/v1/projects" || q.Get("q") != "via" || q.Get("platform") != "PAPER" || q.Get("version") != "1.21.11" ||
		q.Get("sort") != "-downloads" || q.Get("offset") != "40" || q.Get("limit") != "20" {
		t.Fatalf("request = %s %v", req.URL.Path, q)
	}
	want := upstream.SearchHit{
		ProjectID: "12", Slug: "ViaBackwards", Owner: "ViaVersion", Title: "ViaBackwards", Author: "ViaVersion",
		Description: "Allow Java Edition clients with older versions to connect to your Minecraft server",
		IconURL:     "https://hangarcdn.papermc.io/avatars/project/12.webp?v=1", Downloads: 754151,
	}
	if res.Total != 377 || len(res.Hits) != 2 || res.Hits[0] != want || res.Hits[1].ProjectID != "31" {
		t.Fatalf("result = %+v", res)
	}
}

func TestHangarProject(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/ViaBackwards":
			_, _ = w.Write(testdata(t, "hangar_project_viabackwards.json"))
		case "/api/v1/projects/31":
			_, _ = w.Write(testdata(t, "hangar_project_viaversion.json"))
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	p, err := c.Project(bg, upstream.Hangar, "ViaBackwards")
	if err != nil {
		t.Fatal(err)
	}
	want := upstream.Project{
		Source: upstream.Hangar, ID: "12", Slug: "ViaBackwards", Owner: "ViaVersion", Title: "ViaBackwards",
		Description: "Allow Java Edition clients with older versions to connect to your Minecraft server",
		IconURL:     "https://hangarcdn.papermc.io/avatars/project/12.webp?v=1",
	}
	if p != want || p.ClientOnly() {
		t.Fatalf("project = %+v", p)
	}
	if v, err := c.Project(bg, upstream.Hangar, "31"); err != nil || v.Slug != "ViaVersion" || v.ID != "31" {
		t.Fatalf("by id = %+v, %v", v, err)
	}
}

func TestHangarVersions(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_versions_viabackwards.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "ViaBackwards", upstream.VersionFilter{Loaders: []string{"ignored"}, GameVersion: "1.21.11"})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	q := req.URL.Query()
	if req.URL.Path != "/api/v1/projects/ViaBackwards/versions" || q.Get("platform") != "PAPER" || q.Get("platformVersion") != "1.21.11" ||
		q.Get("limit") != "25" || q.Has("channel") || q.Has("loaders") {
		t.Fatalf("request = %s %v", req.URL.Path, q)
	}
	if len(vs) != 2 {
		t.Fatalf("versions = %+v", vs)
	}
	snap, rel := vs[0], vs[1]
	wantFile := upstream.File{
		URL:    "https://hangarcdn.papermc.io/plugins/ViaVersion/ViaBackwards/versions/5.12.2-SNAPSHOT%2B657/PAPER/ViaBackwards-5.12.2-SNAPSHOT.jar",
		Name:   "ViaBackwards-5.12.2-SNAPSHOT.jar", Size: 1437280,
		SHA256: "4f2f2b37e16cbd8936257e9dcbd2179ac82002cfe1998c408056c563f8d02c8d",
	}
	if snap.Source != upstream.Hangar || snap.ID != "5.12.2-SNAPSHOT+657" || snap.Name != snap.ID || snap.ProjectID != "12" ||
		snap.Channel != upstream.Beta || snap.File == nil || *snap.File != wantFile || !slices.Contains(snap.GameVersions, "1.21.11") {
		t.Fatalf("snapshot = %+v", snap)
	}
	// only the required dependency (ViaVersion), not the optional ViaRewind
	if !slices.Equal(snap.Dependencies, []upstream.Dependency{{ProjectID: "31", Name: "ViaVersion"}}) {
		t.Fatalf("dependencies = %+v", snap.Dependencies)
	}
	if rel.ID != "5.12.1" || rel.Channel != upstream.Release || !rel.PublishedAt.Before(snap.PublishedAt) {
		t.Fatalf("release = %+v", rel)
	}
}

func TestHangarVersionsReleaseOnly(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_versions_viaversion_release.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "31", upstream.VersionFilter{GameVersion: "1.21.11", ReleaseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := api.lastRequest()
	if req.URL.Query().Get("channel") != "Release" {
		t.Fatalf("query = %v", req.URL.Query())
	}
	if len(vs) != 1 || vs[0].ID != "5.12.1" || vs[0].File.Name != "ViaVersion-5.12.1.jar" || len(vs[0].Dependencies) != 0 {
		t.Fatalf("versions = %+v", vs)
	}
}

func TestHangarVersion(t *testing.T) {
	api := newAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects/ViaBackwards/versions/5.12.1":
			_, _ = w.Write(testdata(t, "hangar_version_viabackwards_5.12.1.json"))
		case "/api/v1/projects/ViaBackwards/versions/5.12.2-SNAPSHOT+657":
			_, _ = w.Write(testdata(t, "hangar_version_viabackwards_5.12.1.json"))
		default:
			http.NotFound(w, r)
		}
	})
	c := newClient(t, api, nil, clock.NewFake(t0))
	v, err := c.Version(bg, upstream.Hangar, "ViaBackwards", "5.12.1")
	if err != nil || v.ID != "5.12.1" || v.Channel != upstream.Release || v.File.SHA256 != "4b9dd5cb7662f532b44627f8491255bf96f586cbd00280d1480442590ad4450d" {
		t.Fatalf("version = %+v, %v", v, err)
	}
	if _, err := c.Version(bg, upstream.Hangar, "ViaBackwards", "5.12.2-SNAPSHOT+657"); err != nil {
		t.Fatalf("a name with + must reach the upstream unchanged: %v", err)
	}
}

// Review Focus #1: a version that is only linked externally has no File; its external URL is
// kept, and optional external dependencies are dropped.
func TestHangarExternalOnly(t *testing.T) {
	api := newAPI(t, serveFile(t, "hangar_versions_essentials.json"))
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "Essentials", upstream.VersionFilter{GameVersion: "1.21.11"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	v := vs[0]
	if v.Downloadable() || v.ExternalURL != "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0" || len(v.Dependencies) != 0 {
		t.Fatalf("version = %+v", v)
	}
}

func TestHangarExternalDependency(t *testing.T) {
	body := `{"result":[{"projectId":5,"name":"1.0","createdAt":"2026-01-01T00:00:00Z","channel":{"name":"release"},
		"downloads":{"PAPER":{"fileInfo":{"name":"a.jar","sizeBytes":3,"sha256Hash":"ABC"},"externalUrl":null,"downloadUrl":"https://hangarcdn.papermc.io/a.jar"}},
		"pluginDependencies":{"PAPER":[{"name":"Vault","projectId":null,"required":true,"externalUrl":"https://www.spigotmc.org/resources/vault.34315/"},
		{"name":"Other","projectId":7,"required":true,"externalUrl":null}],"VELOCITY":[{"name":"X","projectId":9,"required":true}]},
		"platformDependencies":{"PAPER":["1.21.11"]}}]}`
	api := newAPI(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
	c := newClient(t, api, nil, clock.NewFake(t0))
	vs, err := c.Versions(bg, upstream.Hangar, "5", upstream.VersionFilter{GameVersion: "1.21.11"})
	if err != nil || len(vs) != 1 {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
	want := []upstream.Dependency{
		{Name: "Vault", ExternalURL: "https://www.spigotmc.org/resources/vault.34315/"},
		{ProjectID: "7", Name: "Other"},
	}
	if !slices.Equal(vs[0].Dependencies, want) || vs[0].Channel != upstream.Release || vs[0].File.SHA256 != "abc" {
		t.Fatalf("version = %+v", vs[0])
	}
}
```

`internal/plugins/upstream/download_test.go`:

```go
package upstream_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// cdnServer is a TLS test server that counts its requests.
func cdnServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func readAll(t *testing.T, c *upstream.Client, f upstream.File) ([]byte, error) {
	t.Helper()
	body, err := c.Download(bg, f)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func TestDownloadStreams(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != testUA || r.URL.Path != "/data/a.jar" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("jar-bytes"))
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	got, err := readAll(t, c, upstream.File{URL: cdn.URL + "/data/a.jar", Size: 9})
	if err != nil || string(got) != "jar-bytes" {
		t.Fatalf("download = %q, %v", got, err)
	}
}

// Review Focus #1: jar URLs on foreign hosts or plain http are refused before any request.
func TestDownloadRejectsForeignHost(t *testing.T) {
	cdn, hits := cdnServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) })
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for _, raw := range []string{
		"https://evil.example/a.jar",
		"http://" + cdn.Listener.Addr().String() + "/a.jar",
		"https://user:pw@" + cdn.Listener.Addr().String() + "/a.jar",
		"::not a url",
	} {
		_, err := readAll(t, c, upstream.File{URL: raw, Size: 1})
		if !errors.Is(err, upstream.ErrDownload) || !errors.Is(err, upstream.ErrHostNotAllowed) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("refused URLs reached the CDN %d times", hits.Load())
	}
}

// Review Focus #1: redirects only to allowed hosts over https, at most 3.
func TestDownloadRedirects(t *testing.T) {
	other, otherHits := cdnServer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("evil")) })
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case p == "a.jar":
			_, _ = w.Write([]byte("good"))
		case p == "same":
			http.Redirect(w, r, "/a.jar", http.StatusFound)
		case p == "foreign":
			http.Redirect(w, r, other.URL+"/a.jar", http.StatusFound)
		case p == "plain":
			http.Redirect(w, r, "http://"+r.Host+"/a.jar", http.StatusFound)
		case strings.HasPrefix(p, "hop"):
			n, _ := strconv.Atoi(strings.TrimPrefix(p, "hop"))
			if n >= 3 {
				http.Redirect(w, r, "/a.jar", http.StatusFound)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/hop%d", n+1), http.StatusFound)
		}
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for _, p := range []string{"same", "hop1"} {
		if got, err := readAll(t, c, upstream.File{URL: cdn.URL + "/" + p, Size: 4}); err != nil || string(got) != "good" {
			t.Errorf("%s: %q, %v", p, got, err)
		}
	}
	for _, p := range []string{"foreign", "plain"} {
		if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/" + p, Size: 4}); !errors.Is(err, upstream.ErrHostNotAllowed) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/hop0", Size: 4}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("4 redirects: %v", err)
	}
	if otherHits.Load() != 0 {
		t.Fatal("the foreign host was contacted")
	}
}

// Review Focus #1: the API size bounds the jar, the CDN must send exactly that many bytes.
func TestDownloadSizeChecks(t *testing.T) {
	cdn, hits := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/length":
			w.Header().Set("Content-Length", "5")
			_, _ = w.Write([]byte("12345"))
		case "/more": // chunked: no Content-Length
			_, _ = w.Write([]byte("1234"))
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte("5678"))
		case "/fewer":
			_, _ = w.Write([]byte("12"))
			w.(http.Flusher).Flush()
		}
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for _, size := range []int64{0, -1, upstream.MaxJarBytes + 1} {
		if _, err := c.Download(bg, upstream.File{URL: cdn.URL + "/length", Size: size}); !errors.Is(err, upstream.ErrDownload) {
			t.Errorf("size %d: %v", size, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("an out-of-range size reached the CDN")
	}
	if _, err := c.Download(bg, upstream.File{URL: cdn.URL + "/length", Size: 4}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("Content-Length 5 for 4 announced bytes: %v", err)
	}
	got, err := readAll(t, c, upstream.File{URL: cdn.URL + "/more", Size: 4})
	if !errors.Is(err, upstream.ErrDownload) || len(got) > 4 {
		t.Errorf("more bytes: %q, %v", got, err)
	}
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/fewer", Size: 4}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("fewer bytes: %v", err)
	}
}

func TestDownloadStatus(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		code, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		w.WriteHeader(code)
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	for code, want := range map[int]error{404: upstream.ErrDownload, 403: upstream.ErrDownload, 429: upstream.ErrUnavailable, 503: upstream.ErrUnavailable} {
		if _, err := c.Download(bg, upstream.File{URL: fmt.Sprintf("%s/%d", cdn.URL, code), Size: 1}); !errors.Is(err, want) {
			t.Errorf("HTTP %d: %v, want %v", code, err, want)
		}
	}
}

// A CDN that stops sending ends the download after the read timeout, not after minutes.
func TestDownloadReadTimeout(t *testing.T) {
	cdn, _ := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stall" {
			_, _ = w.Write([]byte("12"))
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done() // "/silent": not even headers
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0), func(o *upstream.Options) { o.ReadTimeout = 100 * time.Millisecond })
	start := time.Now()
	if _, err := readAll(t, c, upstream.File{URL: cdn.URL + "/stall", Size: 10}); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("stall: %v", err)
	}
	if _, err := c.Download(bg, upstream.File{URL: cdn.URL + "/silent", Size: 10}); !errors.Is(err, upstream.ErrUnavailable) {
		t.Errorf("silent: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeouts took %s", d)
	}
}
```

`internal/plugins/upstream/icon_test.go`:

```go
package upstream_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

func TestDetectImage(t *testing.T) {
	for in, want := range map[string]string{
		string(pngMagic) + "rest":    "image/png",
		"\xff\xd8\xff\xe0rest":       "image/jpeg",
		"GIF89a....":                 "image/gif",
		"GIF87a....":                 "image/gif",
		"RIFF\x00\x00\x00\x00WEBPVP8": "image/webp",
		"RIFF\x00\x00\x00\x00WAVE":   "",
		`<svg xmlns="http://www.w3.org/2000/svg"/>`: "",
		"":                           "",
	} {
		if got := upstream.DetectImage([]byte(in)); got != want {
			t.Errorf("DetectImage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIcon(t *testing.T) {
	cdn, hits := cdnServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/i.png":
			_, _ = w.Write(append(bytes.Clone(pngMagic), "data"...))
		case "/i.svg":
			w.Header().Set("Content-Type", "image/png") // lies: the magic bytes decide
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		case "/big.png":
			_, _ = w.Write(append(bytes.Clone(pngMagic), make([]byte, upstream.MaxIconBytes)...))
		}
	})
	c := newClient(t, nil, cdn, clock.NewFake(t0))
	ic, err := c.Icon(bg, cdn.URL+"/i.png")
	if err != nil || ic.ContentType != "image/png" || !bytes.HasSuffix(ic.Data, []byte("data")) {
		t.Fatalf("png = %+v, %v", ic, err)
	}
	if _, err := c.Icon(bg, cdn.URL+"/i.svg"); !errors.Is(err, upstream.ErrNotImage) {
		t.Errorf("svg: %v", err)
	}
	if _, err := c.Icon(bg, cdn.URL+"/big.png"); !errors.Is(err, upstream.ErrDownload) {
		t.Errorf("too large: %v", err)
	}
	n := hits.Load()
	if _, err := c.Icon(bg, "https://evil.example/i.png"); !errors.Is(err, upstream.ErrHostNotAllowed) {
		t.Errorf("foreign host: %v", err)
	}
	if hits.Load() != n {
		t.Fatal("a foreign icon URL reached a server")
	}
}

func TestIconURLAllowed(t *testing.T) {
	c := upstream.New(upstream.Options{Clock: clock.NewFake(t0)})
	long := "https://cdn.modrinth.com/" + strings.Repeat("a", upstream.MaxIconURLLen)
	for raw, want := range map[string]bool{
		"https://cdn.modrinth.com/data/P/icon.png":           true,
		"https://hangarcdn.papermc.io/avatars/project/12.webp?v=1": true,
		"https://hangar.papermc.io/avatars/project/12.webp": false,
		"http://cdn.modrinth.com/data/P/icon.png":            false,
		"":                                                   false,
		long:                                                 false,
	} {
		if got := c.IconURLAllowed(raw); got != want {
			t.Errorf("IconURLAllowed(%.60s) = %v, want %v", raw, got, want)
		}
	}
}
```

`internal/plugins/upstream/filename_test.go`:

```go
package upstream_test

import (
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// Review Focus #1: an upstream file name never leaves plugins/ or mods/ and never hides a file.
func TestFileName(t *testing.T) {
	for _, tc := range []struct{ name, slug, version, want string }{
		{"LuckPerms-Bukkit-5.5.71.jar", "luckperms", "v5.5.71-bukkit", "LuckPerms-Bukkit-5.5.71.jar"},
		{"fabric-api-0.141.6+1.21.11.jar", "fabric-api", "0.141.6+1.21.11", "fabric-api-0.141.6+1.21.11.jar"},
		{"../evil.jar", "via", "1.0", "via-1.0.jar"},
		{"a/b.jar", "via", "1.0", "via-1.0.jar"},
		{`a\b.jar`, "via", "1.0", "via-1.0.jar"},
		{"x\x00.jar", "via", "1.0", "via-1.0.jar"},
		{".hidden.jar", "via", "1.0", "via-1.0.jar"},
		{".mcpanel-tmp.jar", "via", "1.0", "via-1.0.jar"},
		{"évil.jar", "via", "1.0", "via-1.0.jar"},
		{"x.JAR", "via", "1.0", "via-1.0.jar"},
		{"x.zip", "via", "1.0", "via-1.0.jar"},
		{"", "my plugin", "1.0 beta/2", "my-plugin-1.0-beta-2.jar"},
		{"", "ViaBackwards", "5.12.2-SNAPSHOT+657", "ViaBackwards-5.12.2-SNAPSHOT+657.jar"},
		{"", "", "", "plugin.jar"},
		{"", "-..-", "", "plugin.jar"},
		{"", "../..", "x", "x.jar"},
	} {
		if got := upstream.FileName(tc.name, tc.slug, tc.version); got != tc.want {
			t.Errorf("FileName(%q, %q, %q) = %q, want %q", tc.name, tc.slug, tc.version, got, tc.want)
		}
	}
	long := upstream.FileName(strings.Repeat("a", 200)+".jar", strings.Repeat("b", 200), "1")
	if len(long) != upstream.MaxFileNameLen || !strings.HasSuffix(long, ".jar") || !upstream.ValidJarName(long) {
		t.Fatalf("long name = %q (%d)", long, len(long))
	}
}

func TestValidJarName(t *testing.T) {
	for name, want := range map[string]bool{
		"a.jar":                             true,
		"A" + strings.Repeat("b", 127) + ".jar": true,
		"A" + strings.Repeat("b", 128) + ".jar": false,
		"-a.jar":                            false,
		"a.jar.disabled":                    false,
		"a b.jar":                           false,
	} {
		if got := upstream.ValidJarName(name); got != want {
			t.Errorf("ValidJarName(%.20q) = %v, want %v", name, got, want)
		}
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `git add -N internal/plugins/upstream && make check`
Expected: FAIL beim Kompilieren (`undefined: upstream.DetectImage`, `(*Client).Download`, `upstream.FileName` …).

- [ ] **Step 4: Implementierung schreiben**

`internal/plugins/upstream/hangar.go` (ersetzt die Übergangsdatei):

```go
package upstream

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// hangarPlatform is the only Hangar platform the panel installs for (Paper and Purpur).
const hangarPlatform = "PAPER"

type hProject struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Namespace struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
	Stats struct {
		Downloads int64 `json:"downloads"`
	} `json:"stats"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatarUrl"`
}

type hSearch struct {
	Pagination struct {
		Count int `json:"count"`
	} `json:"pagination"`
	Result []hProject `json:"result"`
}

type hDownload struct {
	FileInfo *struct {
		Name       string `json:"name"`
		SizeBytes  int64  `json:"sizeBytes"`
		SHA256Hash string `json:"sha256Hash"`
	} `json:"fileInfo"`
	ExternalURL string `json:"externalUrl"`
	DownloadURL string `json:"downloadUrl"`
}

type hDependency struct {
	Name        string `json:"name"`
	ProjectID   *int64 `json:"projectId"`
	Required    bool   `json:"required"`
	ExternalURL string `json:"externalUrl"`
}

type hVersion struct {
	ProjectID int64     `json:"projectId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Channel   struct {
		Name string `json:"name"`
	} `json:"channel"`
	Downloads            map[string]hDownload     `json:"downloads"`
	PluginDependencies   map[string][]hDependency `json:"pluginDependencies"`
	PlatformDependencies map[string][]string      `json:"platformDependencies"`
}

type hVersions struct {
	Result []hVersion `json:"result"`
}

// hangarChannel maps the channel "Release" (any case) to release and every other to beta
// (spec §3.1).
func hangarChannel(name string) Channel {
	if strings.EqualFold(name, "release") {
		return Release
	}
	return Beta
}

func (p hProject) project() Project {
	return Project{
		Source: Hangar, ID: strconv.FormatInt(p.ID, 10), Slug: p.Namespace.Slug, Owner: p.Namespace.Owner,
		Title: cmp.Or(p.Name, p.Namespace.Slug), Description: p.Description, IconURL: p.AvatarURL,
	}
}

func (h hVersion) norm() Version {
	v := Version{
		Source: Hangar, ID: h.Name, ProjectID: strconv.FormatInt(h.ProjectID, 10), Name: h.Name,
		Channel: hangarChannel(h.Channel.Name), PublishedAt: h.CreatedAt,
		GameVersions: h.PlatformDependencies[hangarPlatform],
	}
	if d, ok := h.Downloads[hangarPlatform]; ok {
		if d.FileInfo != nil && d.DownloadURL != "" {
			v.File = &File{URL: d.DownloadURL, Name: d.FileInfo.Name, Size: d.FileInfo.SizeBytes, SHA256: strings.ToLower(d.FileInfo.SHA256Hash)}
		} else {
			v.ExternalURL = d.ExternalURL
		}
	}
	for _, d := range h.PluginDependencies[hangarPlatform] {
		if !d.Required {
			continue
		}
		dep := Dependency{Name: d.Name, ExternalURL: d.ExternalURL}
		if d.ProjectID != nil && *d.ProjectID > 0 {
			dep.ProjectID, dep.ExternalURL = strconv.FormatInt(*d.ProjectID, 10), ""
		}
		v.Dependencies = append(v.Dependencies, dep)
	}
	return v
}

func (c *Client) hangarSearch(ctx context.Context, q SearchQuery) (SearchResult, error) {
	v := url.Values{
		"q": {q.Query}, "platform": {hangarPlatform}, "sort": {"-downloads"},
		"offset": {strconv.Itoa(q.Offset)}, "limit": {strconv.Itoa(PageSize)},
	}
	if q.GameVersion != "" {
		v.Set("version", q.GameVersion)
	}
	var resp hSearch
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Hangar+"/api/v1/projects?"+v.Encode(), nil, &resp); err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Hits: make([]SearchHit, 0, len(resp.Result)), Total: resp.Pagination.Count}
	for _, p := range resp.Result {
		if p.ID <= 0 {
			continue
		}
		pr := p.project()
		out.Hits = append(out.Hits, SearchHit{
			ProjectID: pr.ID, Slug: pr.Slug, Owner: pr.Owner, Title: pr.Title, Description: pr.Description,
			Author: pr.Owner, IconURL: pr.IconURL, Downloads: p.Stats.Downloads,
		})
	}
	return out, nil
}

func (c *Client) hangarProject(ctx context.Context, id string) (Project, error) {
	var p hProject
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Hangar+"/api/v1/projects/"+url.PathEscape(id), nil, &p); err != nil {
		return Project{}, err
	}
	if p.ID <= 0 {
		return Project{}, fmt.Errorf("%w: hangar project %q without an id", ErrUnavailable, id)
	}
	return p.project(), nil
}

func (c *Client) hangarVersions(ctx context.Context, project, gv string, releaseOnly bool) ([]Version, error) {
	v := url.Values{"platform": {hangarPlatform}, "limit": {strconv.Itoa(MaxVersions)}}
	if gv != "" {
		v.Set("platformVersion", gv)
	}
	if releaseOnly {
		v.Set("channel", "Release")
	}
	var resp hVersions
	u := c.bases.Hangar + "/api/v1/projects/" + url.PathEscape(project) + "/versions?" + v.Encode()
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(resp.Result))
	for _, h := range resp.Result {
		if validRef(h.Name) {
			out = append(out, h.norm())
		}
	}
	sortNewestFirst(out)
	return out, nil
}

func (c *Client) hangarVersion(ctx context.Context, project, name string) (Version, error) {
	var h hVersion
	u := c.bases.Hangar + "/api/v1/projects/" + url.PathEscape(project) + "/versions/" + url.PathEscape(name)
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &h); err != nil {
		return Version{}, err
	}
	if !validRef(h.Name) {
		return Version{}, fmt.Errorf("%w: hangar version %q without a valid name", ErrUnavailable, name)
	}
	return h.norm(), nil
}
```

`internal/plugins/upstream/download.go`:

```go
package upstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// idle cancels a request when it made no progress for d (headers or one read).
type idle struct {
	d      time.Duration
	t      *time.Timer
	cancel context.CancelFunc
	mu     sync.Mutex
	fired  bool
}

func newIdle(d time.Duration, cancel context.CancelFunc) *idle {
	i := &idle{d: d, cancel: cancel}
	i.t = time.AfterFunc(d, func() {
		i.mu.Lock()
		i.fired = true
		i.mu.Unlock()
		cancel()
	})
	return i
}

func (i *idle) reset() { i.t.Reset(i.d) }

func (i *idle) stop() {
	i.t.Stop()
	i.cancel()
}

func (i *idle) timedOut() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.fired
}

// open sends a GET to an allowed host (redirects are checked by allowedRedirect). The headers
// must arrive within the read timeout. On success the caller must close the body and stop the
// timer.
func (c *Client) open(ctx context.Context, raw, accept string) (*http.Response, *idle, error) {
	u, err := url.Parse(raw)
	if err != nil || !c.HostAllowed(u) {
		return nil, nil, fmt.Errorf("%w: %w: %q", ErrDownload, ErrHostNotAllowed, raw)
	}
	ctx, cancel := context.WithCancel(ctx)
	t := newIdle(c.readTimeout, cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		t.stop()
		return nil, nil, fmt.Errorf("%w: build request: %w", ErrDownload, err)
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", accept)
	resp, err := c.dl.Do(req)
	if err != nil {
		t.stop()
		switch {
		case errors.Is(err, ErrDownload):
			return nil, nil, err // refused redirect
		case t.timedOut():
			return nil, nil, fmt.Errorf("%w: no answer from %s within %s", ErrUnavailable, u.Host, c.readTimeout)
		}
		return nil, nil, fmt.Errorf("%w: GET %s: %w", ErrUnavailable, u.Redacted(), err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.stop()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, nil, fmt.Errorf("%w: GET %s: HTTP %d", ErrUnavailable, u.Redacted(), resp.StatusCode)
		}
		return nil, nil, fmt.Errorf("%w: GET %s: HTTP %d", ErrDownload, u.Redacted(), resp.StatusCode)
	}
	return resp, t, nil
}

// Download opens the jar f for streaming (spec §4): only from allowed hosts, the size the API
// announced must be 1..MaxJarBytes and equal the CDN's Content-Length, the body fails with
// ErrDownload if the CDN sends more or fewer bytes or stalls longer than the read timeout.
func (c *Client) Download(ctx context.Context, f File) (io.ReadCloser, error) {
	if f.Size <= 0 || f.Size > MaxJarBytes {
		return nil, fmt.Errorf("%w: announced size %d outside 1..%d bytes", ErrDownload, f.Size, MaxJarBytes)
	}
	resp, t, err := c.open(ctx, f.URL, "application/java-archive, application/octet-stream, */*")
	if err != nil {
		return nil, err
	}
	if resp.ContentLength >= 0 && resp.ContentLength != f.Size {
		_ = resp.Body.Close()
		t.stop()
		return nil, fmt.Errorf("%w: the source sends %d bytes, the API announced %d", ErrDownload, resp.ContentLength, f.Size)
	}
	return &sizedBody{rc: resp.Body, idle: t, want: f.Size}, nil
}

// sizedBody passes exactly want bytes through and fails on any deviation.
type sizedBody struct {
	rc   io.ReadCloser
	idle *idle
	want int64
	n    int64
	err  error
}

func (b *sizedBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	b.idle.reset()
	n, err := b.rc.Read(p)
	b.n += int64(n)
	switch {
	case b.n > b.want:
		n -= int(b.n - b.want)
		b.n = b.want
		b.err = fmt.Errorf("%w: the source sent more than the announced %d bytes", ErrDownload, b.want)
		return n, b.err
	case errors.Is(err, io.EOF) && b.n < b.want:
		b.err = fmt.Errorf("%w: the source sent %d of %d bytes", ErrDownload, b.n, b.want)
		return n, b.err
	case err != nil && !errors.Is(err, io.EOF):
		if b.idle.timedOut() {
			b.err = fmt.Errorf("%w: no data for %s", ErrDownload, b.idle.d)
		} else {
			b.err = fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return n, b.err
	}
	return n, err
}

func (b *sizedBody) Close() error {
	b.idle.stop()
	return b.rc.Close()
}
```

`internal/plugins/upstream/icon.go`:

```go
package upstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
)

// IconURLAllowed reports whether raw may be fetched by the icon proxy (spec §6.8).
func (c *Client) IconURLAllowed(raw string) bool {
	if raw == "" || len(raw) > MaxIconURLLen {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && c.HostAllowed(u)
}

// Icon fetches an icon of at most MaxIconBytes; only PNG, JPEG, WebP and GIF pass (by magic
// bytes, never by the Content-Type the CDN claims).
func (c *Client) Icon(ctx context.Context, raw string) (Icon, error) {
	if !c.IconURLAllowed(raw) {
		return Icon{}, fmt.Errorf("%w: %w: icon URL", ErrDownload, ErrHostNotAllowed)
	}
	resp, t, err := c.open(ctx, raw, "image/png, image/jpeg, image/webp, image/gif")
	if err != nil {
		return Icon{}, err
	}
	defer t.stop()
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > MaxIconBytes {
		return Icon{}, fmt.Errorf("%w: icon larger than %d bytes", ErrDownload, MaxIconBytes)
	}
	b, err := io.ReadAll(io.LimitReader(&idleReader{r: resp.Body, idle: t}, MaxIconBytes+1))
	if err != nil {
		return Icon{}, fmt.Errorf("%w: %w", ErrDownload, err)
	}
	if len(b) > MaxIconBytes {
		return Icon{}, fmt.Errorf("%w: icon larger than %d bytes", ErrDownload, MaxIconBytes)
	}
	ct := DetectImage(b)
	if ct == "" {
		return Icon{}, fmt.Errorf("%w: unknown format", ErrNotImage)
	}
	return Icon{Data: b, ContentType: ct}, nil
}

type idleReader struct {
	r    io.Reader
	idle *idle
}

func (r *idleReader) Read(p []byte) (int, error) {
	r.idle.reset()
	return r.r.Read(p)
}

// DetectImage returns the image type of b by its magic bytes, "" for anything else (SVG
// included).
func DetectImage(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}
```

`internal/plugins/upstream/filename.go`:

```go
package upstream

import (
	"regexp"
	"strings"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// MaxFileNameLen bounds a fallback file name including ".jar".
const MaxFileNameLen = 128

var jarNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}\.jar$`)

// ValidJarName reports whether an upstream file name may be used as is (spec §4).
func ValidJarName(name string) bool {
	if !jarNameRe.MatchString(name) {
		return false
	}
	c, err := filesapi.CleanPath(name)
	return err == nil && c == name && !filesapi.IsHidden(c)
}

func isAlnum(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') }

// FileName returns the upstream name if it is valid, else "<slug>-<version>.jar" with every
// character outside [A-Za-z0-9._+-] replaced by "-", leading non-alphanumerics dropped and cut
// to MaxFileNameLen; "plugin.jar" if nothing usable is left.
func FileName(upstreamName, slug, version string) string {
	if ValidJarName(upstreamName) {
		return upstreamName
	}
	var b strings.Builder
	for _, r := range slug + "-" + version {
		if isAlnum(r) || r == '.' || r == '_' || r == '+' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	base := strings.TrimLeftFunc(b.String(), func(r rune) bool { return !isAlnum(r) })
	if base == "" {
		base = "plugin"
	}
	if limit := MaxFileNameLen - len(".jar"); len(base) > limit {
		base = base[:limit]
	}
	if name := base + ".jar"; ValidJarName(name) {
		return name
	}
	return "plugin.jar"
}
```

- [ ] **Step 5: Checks und CI**

Run: `make check`
Expected: PASS.

```bash
git add internal/plugins/upstream
git commit -m "feat(plugins): hangar client, jar download, icons and file names

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI PASS für `TestHangar*`, `TestDownload*`, `TestIcon*`, `TestDetectImage`, `TestFileName`, `TestValidJarName`.

---

### Task 9: Grundgerüst, Kompatibilität, Inventar und Neustart-Merker

Ersetzt den Phase-A-Stub von `internal/plugins/handler.go` durch das echte Grundgerüst (Server laden, Art prüfen, Fehlerabbildung, Audit-Helfer) und implementiert `GET /servers/{id}/plugins`. Die übrigen zehn Routen bleiben bis Aufgabe 10–12 Übergangs-Handler in `stubs.go`.

**Files:**
- Modify (vollständig ersetzen): `internal/plugins/handler.go`, `internal/plugins/routes_test.go` (beide aus Phase A)
- Create: `internal/plugins/compat.go`, `internal/plugins/inventory.go`, `internal/plugins/restart.go`, `internal/plugins/stubs.go`, `internal/plugins/export_test.go`
- Test: `internal/plugins/helpers_test.go`, `internal/plugins/fakeup_test.go`, `internal/plugins/routes_test.go`, `internal/plugins/compat_internal_test.go`, `internal/plugins/inventory_test.go`

**Interfaces:**
- Consumes: Aufgaben 7–8 (`upstream.*`); Phase A: `filesapi.Client` (mit `Jars`, `UploadChecked`), `filesapi.JarsResponse`, `filesapi.Jar`, `filesapi.JarMeta`, `filesapi.DirPlugins`, `filesapi.DirMods`, `filesapi.IsJarName`, `filesapi.ValidJarDir`, `filesapi.Checksum` (`Validate`, `Matches`), `filesapi.ErrHashMismatch`; `store.Queries.RbacGetServer`, `ServersGetInstance`, `PluginsListInstalls`, `PluginsDeleteInstall`, `store.PluginsDeleteInstallParams`, `store.PluginsUpsertInstallParams`; `httpx.CodePluginsUnsupported`, `CodePluginSourceUnavailable`, `CodePluginIncompatible`, `CodePluginNotDownloadable`; `agentapi.InstancesClient`; `catalog.Catalog`.
- Produces:

```go
func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
func (h *Handler) Run(ctx context.Context)
func ErrorStatus(err error) (status int, code string)
func MarkChanged(h *Handler, serverID int64, t time.Time) // export_test.go
```

- [ ] **Step 1: Testhelfer und Upstream-Fake schreiben**

`internal/plugins/helpers_test.go`:

```go
package plugins_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var bg = context.Background()

// env is a real database plus the plugins handler on a real rbac.Router, a fake file service,
// a fake agent, a fake catalog and a fake Modrinth/Hangar/CDN (fakeup_test.go). Run runs in
// the background until the test ends (or stop is called).
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *ctxRecorder
	files  *fakeFiles
	agent  *fakeAgent
	cat    *fakeCatalog
	up     *fakeUp
	h      *plugins.Handler
	mux    http.Handler
	rootID int64
	stop   func()
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith lets a test change the dependencies (e.g. Files = nil) before the handler is built.
func newEnvWith(t *testing.T, opt func(*plugins.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &ctxRecorder{}, files: newFakeFiles(), agent: newFakeAgent(),
		cat: &fakeCatalog{cat: catalog.Catalog{Versions: []catalog.Version{{ID: "1.21.11", Java: instancespec.Java21}}}},
	}
	e.up = newFakeUp(t)
	d := plugins.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Files: e.files, Instances: e.agent, Catalog: e.cat,
		Upstream: e.up.client(e.clk), Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = plugins.NewHandler(d)
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	var once sync.Once
	e.stop = func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(e.stop)
	return e
}

func (e *env) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func srvPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d/plugins%s", id, suffix) }

// do sends a JSON request with client IP 203.0.113.7 as p (nil = anonymous).
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// get is do(GET) as admin that must answer 200.
func (e *env) get(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	a := e.admin()
	rec := e.do(http.MethodGet, path, nil, &a)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// createInstance inserts a server with an instance of type typ and version.
func createInstance(t *testing.T, db *sql.DB, name string, typ instancespec.Type, version string) int64 {
	t.Helper()
	id := storetest.CreateServer(t, db, name)
	if _, err := store.New(db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: string(typ), Version: version, Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *env) paper(name string) int64 { return createInstance(e.t, e.db, name, instancespec.TypePaper, "1.21.11") }

func (e *env) fabric(name string) int64 {
	return createInstance(e.t, e.db, name, instancespec.TypeFabric, "1.21.11")
}

// seedRow writes a plugin_installs row for a jar the panel installed.
func (e *env) seedRow(serverID int64, filename, content string, source, projectID, slug, title, versionID, versionName, channel string) {
	e.t.Helper()
	if err := store.New(e.db).PluginsUpsertInstall(context.Background(), store.PluginsUpsertInstallParams{
		ServerID: serverID, Filename: filename, Sha512: sha512Hex(content), Source: source, ProjectID: projectID,
		ProjectSlug: slug, Title: title, VersionID: versionID, VersionName: versionName, Channel: channel,
		InstalledAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) rows(serverID int64) []store.PluginInstall {
	e.t.Helper()
	rows, err := store.New(e.db).PluginsListInstalls(context.Background(), serverID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func sha512Hex(s string) string { b := sha512.Sum512([]byte(s)); return hex.EncodeToString(b[:]) }

func sha256Hex(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }

// ctxRecorder records audit entries together with the actor from the context (the SQL recorder
// takes the actor from there as well).
type ctxRecorder struct {
	mu      sync.Mutex
	entries []audit.Entry
	actors  []string
}

func (r *ctxRecorder) Record(ctx context.Context, e audit.Entry) error {
	p, _ := principal.From(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	r.actors = append(r.actors, p.Username)
	return nil
}

func (r *ctxRecorder) all() ([]audit.Entry, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.entries), slices.Clone(r.actors)
}

func (r *ctxRecorder) actions() []string {
	es, _ := r.all()
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Action + ":" + string(e.Result)
	}
	return out
}

// fakeFiles is an in-memory mcpanel-files: a flat map of file contents per server. Jars lists
// "<dir>/<name>" like the real service (hashes computed, Meta from meta), UploadChecked checks
// the checksum, Rename and Delete really move and remove.
type fakeFiles struct {
	mu        sync.Mutex
	files     map[string][]byte // "<id>/<path>"
	meta      map[string]*filesapi.JarMeta
	errs      map[string]error // by operation: jars, upload, rename, delete, mkdir
	calls     []string
	truncated bool
	onUpload  func(id int64, path string) // runs before an upload is stored (outside the lock)
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{files: map[string][]byte{}, meta: map[string]*filesapi.JarMeta{}, errs: map[string]error{}}
}

func fkey(id int64, p string) string { return fmt.Sprintf("%d/%s", id, p) }

func (f *fakeFiles) put(id int64, p, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fkey(id, p)] = []byte(content)
}

func (f *fakeFiles) get(id int64, p string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[fkey(id, p)]
	return string(b), ok
}

// names returns the sorted file names directly in dir.
func (f *fakeFiles) names(id int64, dir string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.files {
		if n, ok := strings.CutPrefix(k, fkey(id, dir+"/")); ok && !strings.Contains(n, "/") {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func (f *fakeFiles) setErr(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[op] = err
}

func (f *fakeFiles) setTruncated(v bool) { f.mu.Lock(); f.truncated = v; f.mu.Unlock() }

func (f *fakeFiles) setOnUpload(h func(id int64, path string)) { f.mu.Lock(); f.onUpload = h; f.mu.Unlock() }

func (f *fakeFiles) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

func (f *fakeFiles) begin(ctx context.Context, op, call string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return f.errs[op]
}

func (f *fakeFiles) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if err := f.begin(ctx, "jars", fmt.Sprintf("jars %d %s", id, dir)); err != nil {
		return filesapi.JarsResponse{}, err
	}
	if !filesapi.ValidJarDir(dir) {
		return filesapi.JarsResponse{}, filesapi.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := filesapi.JarsResponse{Jars: []filesapi.Jar{}, Truncated: f.truncated}
	for k, b := range f.files {
		name, ok := strings.CutPrefix(k, fkey(id, dir+"/"))
		if !ok || strings.Contains(name, "/") {
			continue
		}
		enabled, ok := filesapi.IsJarName(name)
		if !ok {
			continue
		}
		out.Jars = append(out.Jars, filesapi.Jar{
			Name: name, Enabled: enabled, Size: int64(len(b)), MTime: t0.UnixMilli(),
			SHA256: sha256Hex(string(b)), SHA512: sha512Hex(string(b)), Meta: f.meta[k],
		})
	}
	sort.Slice(out.Jars, func(i, j int) bool { return out.Jars[i].Name < out.Jars[j].Name })
	return out, nil
}

func (f *fakeFiles) UploadChecked(ctx context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	if err := f.begin(ctx, "upload", fmt.Sprintf("upload %d %s overwrite=%t", id, p, overwrite)); err != nil {
		return filesapi.UploadResponse{}, err
	}
	if err := sum.Validate(); err != nil {
		return filesapi.UploadResponse{}, err
	}
	b, err := io.ReadAll(body)
	if err != nil { // like the real client: a broken body ends the request
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: got %d bytes, announced %d", filesapi.ErrInvalid, len(b), size)
	}
	s256, s512 := sha256Hex(string(b)), sha512Hex(string(b))
	if !sum.Matches(s256, s512) {
		return filesapi.UploadResponse{}, filesapi.ErrHashMismatch
	}
	f.mu.Lock()
	hook := f.onUpload
	f.mu.Unlock()
	if hook != nil {
		hook(id, p)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.files[fkey(id, p)]; exists && !overwrite {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s", filesapi.ErrExists, p)
	}
	f.files[fkey(id, p)] = b
	return filesapi.UploadResponse{Size: size, SHA256: s256, SHA512: s512}, nil
}

func (f *fakeFiles) Rename(ctx context.Context, req filesapi.RenameRequest) error {
	if err := f.begin(ctx, "rename", fmt.Sprintf("rename %d %s %s", req.ID, req.From, req.To)); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to := fkey(req.ID, req.From), fkey(req.ID, req.To)
	b, ok := f.files[from]
	if !ok {
		return filesapi.ErrNotFound
	}
	if _, exists := f.files[to]; exists {
		return filesapi.ErrExists
	}
	delete(f.files, from)
	f.files[to] = b
	if m, ok := f.meta[from]; ok {
		delete(f.meta, from)
		f.meta[to] = m
	}
	return nil
}

func (f *fakeFiles) Delete(ctx context.Context, id int64, p string) error {
	if err := f.begin(ctx, "delete", fmt.Sprintf("delete %d %s", id, p)); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[fkey(id, p)]; !ok {
		return filesapi.ErrNotFound
	}
	delete(f.files, fkey(id, p))
	return nil
}

func (f *fakeFiles) Mkdir(ctx context.Context, id int64, p string) error {
	return f.begin(ctx, "mkdir", fmt.Sprintf("mkdir %d %s", id, p))
}

var errNotUsed = errors.New("fake files: not used by plugins")

func (f *fakeFiles) Health(context.Context) error { return errNotUsed }
func (f *fakeFiles) List(context.Context, int64, string) (filesapi.ListResponse, error) {
	return filesapi.ListResponse{}, errNotUsed
}
func (f *fakeFiles) Read(context.Context, int64, string) (filesapi.ReadResponse, error) {
	return filesapi.ReadResponse{}, errNotUsed
}
func (f *fakeFiles) Write(context.Context, filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	return filesapi.WriteResponse{}, errNotUsed
}
func (f *fakeFiles) Upload(context.Context, int64, string, bool, int64, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNotUsed
}
func (f *fakeFiles) Download(context.Context, int64, string) (filesapi.Download, error) {
	return filesapi.Download{}, errNotUsed
}
func (f *fakeFiles) Extract(context.Context, filesapi.ExtractRequest) (filesapi.Job, error) {
	return filesapi.Job{}, errNotUsed
}
func (f *fakeFiles) Job(context.Context, int64) (*filesapi.Job, error) { return nil, errNotUsed }

// fakeAgent is an in-memory agentapi.InstancesClient for restart_needed.
type fakeAgent struct {
	mu   sync.Mutex
	err  error
	inst map[int64]agentapi.InstanceStatus
}

func newFakeAgent() *fakeAgent { return &fakeAgent{inst: map[int64]agentapi.InstanceStatus{}} }

func (f *fakeAgent) set(st agentapi.InstanceStatus) { f.mu.Lock(); f.inst[st.ID] = st; f.mu.Unlock() }

func (f *fakeAgent) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentapi.InstancesResponse{}, f.err
	}
	list := make([]agentapi.InstanceStatus, 0, len(f.inst))
	for _, st := range f.inst {
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: list}, nil
}

func (f *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error    { return errNotUsed }
func (f *fakeAgent) Power(context.Context, int64, agentapi.PowerAction) error { return errNotUsed }
func (f *fakeAgent) Remove(context.Context, int64) error                      { return errNotUsed }

// fakeCatalog is a plugins.Catalog with a fixed answer.
type fakeCatalog struct {
	mu    sync.Mutex
	cat   catalog.Catalog
	err   error
	asked int
}

func (f *fakeCatalog) Get(context.Context, instancespec.Type) (catalog.Catalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	return f.cat, f.err
}

func (f *fakeCatalog) setErr(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }
```

`internal/plugins/fakeup_test.go`:

```go
package plugins_test

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// upFile is a jar the fake CDN serves.
type upFile struct {
	URL, Name      string
	Size           int64
	SHA512, SHA256 string
	content        string
}

// mv describes a Modrinth version of the fake. Zero values: Type release, published t0 minus
// the version's position in days (the first version is the newest), game versions {1.21.11},
// loaders {paper, purpur, fabric}.
type mv struct {
	ID, Number, Type      string
	Published             time.Time
	GameVersions, Loaders []string
	File                  *upFile
	Deps                  []map[string]any
}

// hv describes a Hangar version of the fake. Zero values: channel Release, published as for mv,
// game versions {1.21.11}.
type hv struct {
	Name, Channel string
	Published     time.Time
	GameVersions  []string
	File          *upFile
	External      string
	Deps          []map[string]any
}

// mdep is a required Modrinth dependency ("" = null).
func mdep(projectID, versionID string) map[string]any {
	null := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	return map[string]any{"project_id": null(projectID), "version_id": null(versionID), "file_name": nil, "dependency_type": "required"}
}

// hdep is a required Hangar dependency: projectID 0 = null (then external is the link).
func hdep(name string, projectID int64, external string) map[string]any {
	d := map[string]any{"name": name, "required": true, "platform": "PAPER", "projectId": nil, "externalUrl": nil}
	if projectID > 0 {
		d["projectId"] = projectID
	}
	if external != "" {
		d["externalUrl"] = external
	}
	return d
}

type mRec struct {
	v    mv
	body map[string]any
}

type mProj struct {
	ID, Slug, Title, ServerSide string
	recs                        []mRec
}

type hRec struct {
	v    hv
	body map[string]any
}

type hProj struct {
	ID          int64
	Owner, Slug string
	recs        []hRec
}

// fakeUp is a fake Modrinth + Hangar API (one TLS server, both bases) and a fake CDN (a second
// TLS server, the only allowed download host).
type fakeUp struct {
	api, cdn *httptest.Server

	mu          sync.Mutex
	mOrder      []string
	mProjects   map[string]*mProj // by id and slug
	hOrder      []int64
	hProjects   map[string]*hProj // by id (text) and slug
	cdnFiles    map[string][]byte // CDN path -> body
	n           int
	apiDown     bool
	cdnStatus   int
	block       chan struct{}
	hits        map[string]int
	lastQuery   url.Values
	updateTypes [][]string
	cdnActive   int
	cdnPeak     int
}

func newFakeUp(t *testing.T) *fakeUp {
	f := &fakeUp{mProjects: map[string]*mProj{}, hProjects: map[string]*hProj{}, cdnFiles: map[string][]byte{}, hits: map[string]int{}}
	f.api = httptest.NewTLSServer(http.HandlerFunc(f.serveAPI))
	f.cdn = httptest.NewTLSServer(http.HandlerFunc(f.serveCDN))
	t.Cleanup(f.api.Close)
	t.Cleanup(f.cdn.Close)
	return f
}

func (f *fakeUp) cdnHost() string { return f.cdn.Listener.Addr().String() }

// client returns an upstream client against the fake; only the fake CDN is an allowed host.
func (f *fakeUp) client(clk clock.Clock) *upstream.Client {
	host := f.cdnHost()
	return upstream.New(upstream.Options{
		Clock: clk, Version: "test", HTTPClient: f.api.Client(),
		Bases:     upstream.Bases{Modrinth: f.api.URL, Hangar: f.api.URL},
		AllowHost: func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host && u.User == nil },
	})
}

// jar registers a jar on the fake CDN.
func (f *fakeUp) jar(name, content string) *upFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	p := fmt.Sprintf("/f/%d/%s", f.n, name)
	f.cdnFiles[p] = []byte(content)
	return &upFile{URL: f.cdn.URL + p, Name: name, Size: int64(len(content)), SHA512: sha512Hex(content), SHA256: sha256Hex(content), content: content}
}

// serveInstead makes the CDN answer body for file (corrupted or wrong-sized downloads).
func (f *fakeUp) serveInstead(file *upFile, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cdnFiles[strings.TrimPrefix(file.URL, f.cdn.URL)] = []byte(body)
}

func (f *fakeUp) iconURL(id string) string { return f.cdn.URL + "/icons/" + id + ".png" }

func published(p time.Time, i int) time.Time {
	if p.IsZero() {
		return t0.Add(-time.Duration(i) * 24 * time.Hour)
	}
	return p
}

// modrinth registers a Modrinth project with its versions (newest first).
func (f *fakeUp) modrinth(id, slug, title, serverSide string, vs ...mv) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &mProj{ID: id, Slug: slug, Title: title, ServerSide: serverSide}
	for i, v := range vs {
		if v.GameVersions == nil {
			v.GameVersions = []string{"1.21.11"}
		}
		if v.Loaders == nil {
			v.Loaders = []string{"paper", "purpur", "fabric"}
		}
		v.Type = cmp.Or(v.Type, "release")
		v.Published = published(v.Published, i)
		files := []map[string]any{}
		if v.File != nil {
			files = append(files, map[string]any{
				"url": v.File.URL, "filename": v.File.Name, "primary": true, "size": v.File.Size,
				"hashes": map[string]string{"sha512": v.File.SHA512, "sha1": "unused"},
			})
		}
		deps := v.Deps
		if deps == nil {
			deps = []map[string]any{}
		}
		p.recs = append(p.recs, mRec{v: v, body: map[string]any{
			"id": v.ID, "project_id": id, "version_number": cmp.Or(v.Number, v.ID), "name": "Version " + v.ID,
			"version_type": v.Type, "date_published": v.Published.Format(time.RFC3339), "game_versions": v.GameVersions,
			"loaders": v.Loaders, "files": files, "dependencies": deps,
		}})
	}
	f.mOrder = append(f.mOrder, id)
	f.mProjects[id], f.mProjects[slug] = p, p
}

// hangar registers a Hangar project with its versions (newest first).
func (f *fakeUp) hangar(id int64, owner, slug string, vs ...hv) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := &hProj{ID: id, Owner: owner, Slug: slug}
	for i, v := range vs {
		if v.GameVersions == nil {
			v.GameVersions = []string{"1.21.11"}
		}
		v.Channel = cmp.Or(v.Channel, "Release")
		v.Published = published(v.Published, i)
		dl := map[string]any{"fileInfo": nil, "externalUrl": nil, "downloadUrl": nil}
		if v.File != nil {
			dl = map[string]any{
				"fileInfo":    map[string]any{"name": v.File.Name, "sizeBytes": v.File.Size, "sha256Hash": v.File.SHA256},
				"externalUrl": nil, "downloadUrl": v.File.URL,
			}
		} else if v.External != "" {
			dl["externalUrl"] = v.External
		}
		deps := v.Deps
		if deps == nil {
			deps = []map[string]any{}
		}
		p.recs = append(p.recs, hRec{v: v, body: map[string]any{
			"projectId": id, "name": v.Name, "createdAt": v.Published.Format(time.RFC3339),
			"channel": map[string]any{"name": v.Channel}, "downloads": map[string]any{"PAPER": dl},
			"pluginDependencies": map[string]any{"PAPER": deps}, "platformDependencies": map[string]any{"PAPER": v.GameVersions},
		}})
	}
	f.hOrder = append(f.hOrder, id)
	f.hProjects[strconv.FormatInt(id, 10)], f.hProjects[slug] = p, p
}

func (f *fakeUp) setDown(v bool)       { f.mu.Lock(); f.apiDown = v; f.mu.Unlock() }
func (f *fakeUp) setCDNStatus(code int) { f.mu.Lock(); f.cdnStatus = code; f.mu.Unlock() }
func (f *fakeUp) setBlock(ch chan struct{}) { f.mu.Lock(); f.block = ch; f.mu.Unlock() }

func (f *fakeUp) count(key string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.hits[key] }

func (f *fakeUp) query() url.Values { f.mu.Lock(); defer f.mu.Unlock(); return f.lastQuery }

func (f *fakeUp) types() [][]string { f.mu.Lock(); defer f.mu.Unlock(); return slices.Clone(f.updateTypes) }

func (f *fakeUp) peak() int { f.mu.Lock(); defer f.mu.Unlock(); return f.cdnPeak }

func overlaps(a, b []string) bool { return slices.ContainsFunc(a, func(s string) bool { return slices.Contains(b, s) }) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// byHash finds the Modrinth version whose jar has sha512 h (f.mu held).
func (f *fakeUp) byHash(h string) (*mProj, mRec, bool) {
	for _, id := range f.mOrder {
		p := f.mProjects[id]
		for _, r := range p.recs {
			if r.v.File != nil && r.v.File.SHA512 == h {
				return p, r, true
			}
		}
	}
	return nil, mRec{}, false
}

func (f *fakeUp) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits[r.Method+" "+r.URL.Path]++
	if f.apiDown {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	p, q := r.URL.Path, r.URL.Query()
	switch {
	case r.Method == http.MethodGet && p == "/v2/search":
		f.lastQuery = q
		hits := []map[string]any{}
		for _, id := range f.mOrder {
			pr := f.mProjects[id]
			hits = append(hits, map[string]any{
				"project_id": pr.ID, "slug": pr.Slug, "title": pr.Title, "description": pr.Title + " description",
				"author": "author-" + pr.Slug, "downloads": 100, "icon_url": f.iconURL(pr.ID),
			})
		}
		writeJSON(w, map[string]any{"hits": hits, "total_hits": len(hits), "offset": 0, "limit": 20})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/project/") && strings.HasSuffix(p, "/version"):
		pr, ok := f.mProjects[strings.TrimSuffix(strings.TrimPrefix(p, "/v2/project/"), "/version")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var loaders, gvs []string
		_ = json.Unmarshal([]byte(q.Get("loaders")), &loaders)
		_ = json.Unmarshal([]byte(q.Get("game_versions")), &gvs)
		out := []map[string]any{}
		for _, rec := range pr.recs {
			if (len(loaders) == 0 || overlaps(rec.v.Loaders, loaders)) && (len(gvs) == 0 || overlaps(rec.v.GameVersions, gvs)) {
				out = append(out, rec.body)
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/project/"):
		pr, ok := f.mProjects[strings.TrimPrefix(p, "/v2/project/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"id": pr.ID, "slug": pr.Slug, "title": pr.Title, "description": pr.Title + " description",
			"icon_url": f.iconURL(pr.ID), "server_side": pr.ServerSide, "client_side": "optional", "project_type": "mod",
		})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/v2/version/"):
		id := strings.TrimPrefix(p, "/v2/version/")
		for _, pid := range f.mOrder {
			for _, rec := range f.mProjects[pid].recs {
				if rec.v.ID == id {
					writeJSON(w, rec.body)
					return
				}
			}
		}
		http.NotFound(w, r)
	case r.Method == http.MethodPost && p == "/v2/version_files":
		var in struct {
			Hashes []string `json:"hashes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		for _, h := range in.Hashes {
			if _, rec, ok := f.byHash(h); ok {
				out[h] = rec.body
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodPost && p == "/v2/version_files/update":
		var in struct {
			Hashes       []string `json:"hashes"`
			Loaders      []string `json:"loaders"`
			GameVersions []string `json:"game_versions"`
			VersionTypes []string `json:"version_types"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.updateTypes = append(f.updateTypes, in.VersionTypes)
		out := map[string]any{}
		for _, h := range in.Hashes {
			pr, _, ok := f.byHash(h)
			if !ok {
				continue
			}
			for _, c := range pr.recs {
				if c.v.File != nil && overlaps(c.v.Loaders, in.Loaders) && overlaps(c.v.GameVersions, in.GameVersions) && slices.Contains(in.VersionTypes, c.v.Type) {
					out[h] = c.body
					break
				}
			}
		}
		writeJSON(w, out)
	case r.Method == http.MethodGet && p == "/api/v1/projects":
		f.lastQuery = q
		res := []map[string]any{}
		for _, id := range f.hOrder {
			res = append(res, f.hProjectJSON(f.hProjects[strconv.FormatInt(id, 10)]))
		}
		writeJSON(w, map[string]any{"pagination": map[string]any{"count": len(res), "limit": 20, "offset": 0}, "result": res})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/api/v1/projects/"):
		parts := strings.Split(strings.TrimPrefix(p, "/api/v1/projects/"), "/")
		pr, ok := f.hProjects[parts[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		switch {
		case len(parts) == 1:
			writeJSON(w, f.hProjectJSON(pr))
		case len(parts) == 2 && parts[1] == "versions":
			out := []map[string]any{}
			for _, rec := range pr.recs {
				gv := q.Get("platformVersion")
				ch := q.Get("channel")
				if (gv == "" || slices.Contains(rec.v.GameVersions, gv)) && (ch == "" || strings.EqualFold(ch, rec.v.Channel)) && len(out) < 25 {
					out = append(out, rec.body)
				}
			}
			writeJSON(w, map[string]any{"pagination": map[string]any{"count": len(out)}, "result": out})
		case len(parts) == 3 && parts[1] == "versions":
			for _, rec := range pr.recs {
				if rec.v.Name == parts[2] {
					writeJSON(w, rec.body)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeUp) hProjectJSON(p *hProj) map[string]any {
	id := strconv.FormatInt(p.ID, 10)
	return map[string]any{
		"id": p.ID, "name": p.Slug, "namespace": map[string]any{"owner": p.Owner, "slug": p.Slug},
		"stats": map[string]any{"downloads": 50}, "description": p.Slug + " description", "avatarUrl": f.iconURL("h" + id),
	}
}

// iconPNG is a PNG signature plus padding: enough for the magic-byte check.
var iconPNG = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 24)...)

func (f *fakeUp) serveCDN(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits["CDN "+r.URL.Path]++
	f.cdnActive++
	f.cdnPeak = max(f.cdnPeak, f.cdnActive)
	status, block := f.cdnStatus, f.block
	body, ok := f.cdnFiles[r.URL.Path]
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.cdnActive--; f.mu.Unlock() }()
	if block != nil {
		select {
		case <-block:
		case <-r.Context().Done():
			return
		}
	}
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/icons/") {
		switch {
		case strings.HasSuffix(r.URL.Path, ".svg"):
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`))
		case strings.Contains(r.URL.Path, "big"):
			_, _ = w.Write(append(bytes.Clone(iconPNG), make([]byte, upstream.MaxIconBytes)...))
		default:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(iconPNG)
		}
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
```

`internal/plugins/export_test.go`:

```go
package plugins

import "time"

// MarkChanged records a plugin change of serverID at t (restart_needed tests).
func MarkChanged(h *Handler, serverID int64, t time.Time) { h.restart.mark(serverID, t) }
```

- [ ] **Step 2: Tests für Routen, Kompatibilität und Inventar schreiben**

`internal/plugins/routes_test.go` (ersetzt die Phase-A-Datei):

```go
package plugins_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var pluginsRule = rbac.Server(rbac.PermServerPlugins, "id")

// wantRoutes are exactly the plugins operations of api/openapi.yaml (V3).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/plugins":          pluginsRule,
	"GET /api/servers/{id}/plugins/search":   pluginsRule,
	"GET /api/servers/{id}/plugins/versions": pluginsRule,
	"GET /api/servers/{id}/plugins/resolve":  pluginsRule,
	"POST /api/servers/{id}/plugins/install": pluginsRule,
	"GET /api/servers/{id}/plugins/updates":  pluginsRule,
	"POST /api/servers/{id}/plugins/update":  pluginsRule,
	"POST /api/servers/{id}/plugins/toggle":  pluginsRule,
	"POST /api/servers/{id}/plugins/delete":  pluginsRule,
	"GET /api/servers/{id}/plugins/jobs":     pluginsRule,
	"GET /api/servers/{id}/plugins/icon":     pluginsRule,
}

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		plugins.NewHandler(plugins.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())
	got := rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

func TestRunReturnsWhenCancelled(t *testing.T) {
	h := plugins.NewHandler(plugins.Deps{DB: storetest.Open(t), Clock: clock.NewFake(t0), Audit: &audit.MemoryRecorder{}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
```

`internal/plugins/compat_internal_test.go`:

```go
package plugins

import (
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestKindOf(t *testing.T) {
	for typ, want := range map[instancespec.Type]string{
		instancespec.TypePaper: "plugin plugins plugin paper,spigot,bukkit modrinth,hangar",
		instancespec.TypePurpur: "plugin plugins plugin purpur,paper,spigot,bukkit modrinth,hangar",
		instancespec.TypeFabric: "mod mods mod fabric modrinth",
		instancespec.TypeNeoForge: "mod mods mod neoforge modrinth",
	} {
		k, ok := kindOf(typ)
		var srcs []string
		for _, s := range k.Sources {
			srcs = append(srcs, string(s))
		}
		got := k.Kind + " " + k.Dir + " " + k.ProjectType + " " + strings.Join(k.Loaders, ",") + " " + strings.Join(srcs, ",")
		if !ok || got != want {
			t.Errorf("%s: %q (%v), want %q", typ, got, ok, want)
		}
	}
	for _, typ := range []instancespec.Type{instancespec.TypeVanilla, instancespec.TypeModrinth, instancespec.TypeCurseForge, "bukkit"} {
		if _, ok := kindOf(typ); ok {
			t.Errorf("%s has plugins", typ)
		}
	}
	k, _ := kindOf(instancespec.TypeFabric)
	if k.hasSource(upstream.Hangar) || !k.hasSource(upstream.Modrinth) || !k.isMod() {
		t.Errorf("fabric kind = %+v", k)
	}
}

func TestPageURL(t *testing.T) {
	for _, tc := range []struct {
		src                     upstream.Source
		kind, owner, slug, want string
	}{
		{upstream.Modrinth, "plugin", "", "luckperms", "https://modrinth.com/plugin/luckperms"},
		{upstream.Modrinth, "mod", "", "fabric-api", "https://modrinth.com/mod/fabric-api"},
		{upstream.Hangar, "plugin", "ViaVersion", "ViaBackwards", "https://hangar.papermc.io/ViaVersion/ViaBackwards"},
		{upstream.Hangar, "plugin", "", "ViaBackwards", "https://hangar.papermc.io/"},
		{upstream.Modrinth, "plugin", "", "a b/c", "https://modrinth.com/plugin/a%20b%2Fc"},
	} {
		if got := pageURL(tc.src, tc.kind, tc.owner, tc.slug); got != tc.want {
			t.Errorf("pageURL(%s, %s, %q, %q) = %q, want %q", tc.src, tc.kind, tc.owner, tc.slug, got, tc.want)
		}
	}
}

func TestCompatible(t *testing.T) {
	paper, _ := kindOf(instancespec.TypePaper)
	fabric, _ := kindOf(instancespec.TypeFabric)
	m := upstream.Version{Source: upstream.Modrinth, GameVersions: []string{"1.21.10", "1.21.11"}, Loaders: []string{"bukkit", "folia"}}
	if !compatible(m, paper, "1.21.11") || compatible(m, paper, "26.3") || compatible(m, fabric, "1.21.11") {
		t.Error("modrinth: loader or game version check wrong")
	}
	h := upstream.Version{Source: upstream.Hangar, GameVersions: []string{"1.21.11"}}
	if !compatible(h, paper, "1.21.11") || compatible(h, paper, "1.21.10") {
		t.Error("hangar: game version check wrong")
	}
	if slices.Contains(paper.Loaders, "fabric") {
		t.Error("paper loaders contain fabric")
	}
}
```

`internal/plugins/inventory_test.go`:

```go
package plugins_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type projectJSON struct {
	ID      string  `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	PageURL string  `json:"page_url"`
	IconURL *string `json:"icon_url"`
}

type versionRefJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Channel string `json:"channel"`
}

type jarJSON struct {
	File             string            `json:"file"`
	Enabled          bool              `json:"enabled"`
	Size             int64             `json:"size"`
	MTime            int64             `json:"mtime"`
	Source           string            `json:"source"`
	Project          *projectJSON      `json:"project"`
	Version          *versionRefJSON   `json:"version"`
	Meta             *filesapi.JarMeta `json:"meta"`
	InstalledByPanel bool              `json:"installed_by_panel"`
}

type invJSON struct {
	Kind          string    `json:"kind"`
	Dir           string    `json:"dir"`
	GameVersion   *string   `json:"game_version"`
	Sources       []string  `json:"sources"`
	Jars          []jarJSON `json:"jars"`
	Truncated     bool      `json:"truncated"`
	RestartNeeded bool      `json:"restart_needed"`
	SourceErrors  []string  `json:"source_errors"`
}

func (e *env) inventory(id int64) invJSON {
	e.t.Helper()
	return decode[invJSON](e.t, e.get(srvPath(id, "")))
}

func jarNamed(t *testing.T, inv invJSON, name string) jarJSON {
	t.Helper()
	for _, j := range inv.Jars {
		if j.File == name {
			return j
		}
	}
	t.Fatalf("no jar %s in %+v", name, inv.Jars)
	return jarJSON{}
}

func proxied(id int64, raw string) string {
	return fmt.Sprintf("/api/servers/%d/plugins/icon?url=%s", id, url.QueryEscape(raw))
}

func TestInventoryMapping(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	oldJar := e.up.jar("LuckPerms-Bukkit-5.5.53.jar", "luckperms 5.5.53")
	e.up.modrinth("Vebnzrzj", "luckperms", "LuckPerms", "required",
		mv{ID: "b0mk8uS6", Number: "v5.5.71-bukkit", File: e.up.jar("LuckPerms-Bukkit-5.5.71.jar", "luckperms 5.5.71")},
		mv{ID: "MBSY8toc", Number: "v5.5.53-bukkit", File: oldJar})
	e.up.modrinth("OFFproj", "offmod", "Off Plugin", "required")
	e.up.hangar(31, "ViaVersion", "ViaVersion")
	// hash-mapped (uploaded by hand), panel-installed from Hangar, manual, panel-installed and disabled
	e.files.put(id, "plugins/LuckPerms-old.jar", oldJar.content)
	e.files.put(id, "plugins/ViaVersion-5.12.1.jar", "viaversion 5.12.1")
	e.seedRow(id, "ViaVersion-5.12.1.jar", "viaversion 5.12.1", "hangar", "31", "ViaVersion", "ViaVersion", "5.12.1", "5.12.1", "release")
	e.files.put(id, "plugins/OldTool.jar", "old tool")
	e.files.meta[fkey(id, "plugins/OldTool.jar")] = &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: "OldTool", Version: "1.0"}
	e.files.put(id, "plugins/Off.jar.disabled", "off plugin")
	e.seedRow(id, "Off.jar", "off plugin", "modrinth", "OFFproj", "offmod", "Off Plugin", "v1", "1.0", "beta")
	e.files.put(id, "plugins/config/LuckPerms.yml", "not a jar")
	e.files.put(id, "mods/Other.jar", "other dir")

	inv := e.inventory(id)
	if inv.Kind != "plugin" || inv.Dir != "plugins" || inv.GameVersion == nil || *inv.GameVersion != "1.21.11" ||
		!slices.Equal(inv.Sources, []string{"modrinth", "hangar"}) || inv.Truncated || inv.RestartNeeded ||
		inv.SourceErrors == nil || len(inv.SourceErrors) != 0 {
		t.Fatalf("inventory = %+v", inv)
	}
	var names []string
	for _, j := range inv.Jars {
		names = append(names, j.File)
	}
	if !slices.Equal(names, []string{"LuckPerms-old.jar", "Off.jar.disabled", "OldTool.jar", "ViaVersion-5.12.1.jar"}) {
		t.Fatalf("jars = %v", names)
	}
	lp := jarNamed(t, inv, "LuckPerms-old.jar")
	icon := proxied(id, e.up.iconURL("Vebnzrzj"))
	if lp.Source != "modrinth" || lp.InstalledByPanel || lp.Project == nil || lp.Project.ID != "Vebnzrzj" ||
		lp.Project.Slug != "luckperms" || lp.Project.Title != "LuckPerms" || lp.Project.PageURL != "https://modrinth.com/plugin/luckperms" ||
		lp.Project.IconURL == nil || *lp.Project.IconURL != icon ||
		lp.Version == nil || *lp.Version != (versionRefJSON{ID: "MBSY8toc", Name: "v5.5.53-bukkit", Channel: "release"}) {
		t.Fatalf("hash-mapped jar = %+v / %+v / %+v", lp, lp.Project, lp.Version)
	}
	via := jarNamed(t, inv, "ViaVersion-5.12.1.jar")
	if via.Source != "hangar" || !via.InstalledByPanel || via.Project.PageURL != "https://hangar.papermc.io/ViaVersion/ViaVersion" ||
		via.Version.ID != "5.12.1" {
		t.Fatalf("hangar jar = %+v / %+v", via, via.Project)
	}
	tool := jarNamed(t, inv, "OldTool.jar")
	if tool.Source != "manual" || tool.Project != nil || tool.Version != nil || tool.Meta == nil || tool.Meta.Name != "OldTool" {
		t.Fatalf("manual jar = %+v", tool)
	}
	off := jarNamed(t, inv, "Off.jar.disabled")
	if off.Enabled || !off.InstalledByPanel || off.Source != "modrinth" || off.Version.Channel != "beta" {
		t.Fatalf("disabled jar = %+v", off)
	}
	raw := e.get(srvPath(id, "")).Body.String()
	if !strings.Contains(raw, `"project":null`) || !strings.Contains(raw, `"version":null`) {
		t.Fatalf("manual jar must carry explicit nulls: %s", raw)
	}
	if calls := e.files.takeCalls(); !slices.Contains(calls, fmt.Sprintf("jars %d plugins", id)) {
		t.Fatalf("calls = %v", calls)
	}
}

// Review Focus #3: rows whose jar is gone or was replaced disappear on the next inventory.
func TestInventoryDropsOrphans(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("K", "kept", "Kept", "required")
	e.files.put(id, "plugins/Kept.jar", "kept")
	e.seedRow(id, "Kept.jar", "kept", "modrinth", "K", "kept", "Kept", "v1", "1", "release")
	e.seedRow(id, "Gone.jar", "gone", "modrinth", "G", "gone", "Gone", "v1", "1", "release")
	e.files.put(id, "plugins/Replaced.jar", "new content from the files tab")
	e.seedRow(id, "Replaced.jar", "old content", "modrinth", "R", "replaced", "Replaced", "v1", "1", "release")

	inv := e.inventory(id)
	if j := jarNamed(t, inv, "Replaced.jar"); j.Source != "manual" || j.InstalledByPanel {
		t.Fatalf("replaced jar = %+v", j)
	}
	rows := e.rows(id)
	if len(rows) != 1 || rows[0].Filename != "Kept.jar" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #3: a truncated listing does not show every jar, so no row counts as orphaned.
func TestInventoryKeepsRowsWhenTruncated(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.seedRow(id, "Beyond500.jar", "x", "modrinth", "B", "b", "B", "v1", "1", "release")
	e.files.setTruncated(true)
	if inv := e.inventory(id); !inv.Truncated {
		t.Fatal("truncated not passed on")
	}
	if rows := e.rows(id); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #4: without Modrinth or Hangar the inventory still answers, jars stay manual.
func TestInventorySourceErrors(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	known := e.up.jar("Known.jar", "known")
	e.up.modrinth("KN", "known", "Known", "required", mv{ID: "v1", File: known})
	e.up.hangar(31, "ViaVersion", "ViaVersion")
	e.files.put(id, "plugins/Known.jar", "known")
	e.files.put(id, "plugins/ViaVersion.jar", "via")
	e.seedRow(id, "ViaVersion.jar", "via", "hangar", "31", "ViaVersion", "ViaVersion", "5.12.1", "5.12.1", "release")
	e.up.setDown(true)

	inv := e.inventory(id)
	if !slices.Equal(inv.SourceErrors, []string{"modrinth", "hangar"}) {
		t.Fatalf("source_errors = %v", inv.SourceErrors)
	}
	if j := jarNamed(t, inv, "Known.jar"); j.Source != "manual" {
		t.Fatalf("known jar = %+v", j)
	}
	via := jarNamed(t, inv, "ViaVersion.jar")
	if via.Source != "hangar" || via.Project.PageURL != "https://hangar.papermc.io/" || via.Project.IconURL != nil {
		t.Fatalf("hangar jar without upstream = %+v", via.Project)
	}
	// the hash lookup failure is not cached: once Modrinth is back, the jar is mapped
	e.up.setDown(false)
	if j := jarNamed(t, e.inventory(id), "Known.jar"); j.Source != "modrinth" {
		t.Fatalf("after recovery = %+v", j)
	}
}

func TestInventoryGameVersion(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "latest", instancespec.TypePaper, "LATEST")
	e.cat.cat.Versions[0].ID = "26.3"
	if inv := e.inventory(id); inv.GameVersion == nil || *inv.GameVersion != "26.3" {
		t.Fatalf("game_version = %v", inv.GameVersion)
	}
	e.cat.setErr(errors.New("catalog down"))
	if inv := e.inventory(id); inv.GameVersion != nil {
		t.Fatalf("game_version without catalog = %v", *inv.GameVersion)
	}
}

func TestInventoryKinds(t *testing.T) {
	e := newEnv(t)
	vanilla := createInstance(t, e.db, "plain", instancespec.TypeVanilla, "1.21.11")
	pack := createInstance(t, e.db, "pack", instancespec.TypeModrinth, "")
	stub := storetest.CreateServer(t, e.db, "lobby")
	fab := e.fabric("modded")
	a := e.admin()
	for _, id := range []int64{vanilla, pack} {
		rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a)
		if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodePluginsUnsupported {
			t.Errorf("server %d: %d %s", id, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{srvPath(stub, ""), "/servers/x/plugins", "/servers/999/plugins"} {
		rec := e.do(http.MethodGet, path, nil, &a)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	e.files.put(fab, "mods/fabric-api.jar", "fabric api")
	inv := e.inventory(fab)
	if inv.Kind != "mod" || inv.Dir != "mods" || !slices.Equal(inv.Sources, []string{"modrinth"}) || len(inv.Jars) != 1 {
		t.Fatalf("fabric inventory = %+v", inv)
	}
}

func TestPluginsPermissions(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	other := e.paper("creative")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view", "server.files", "server.console")
	plug := storetest.CreateUser(t, e.db, "plug", "user")
	grant(t, e.db, plug, id, "server.view", "server.plugins")
	pv, pp := userP(viewer, "viewer"), userP(plug, "plug")
	for _, tc := range []struct {
		name string
		path string
		p    *principal.Principal
		code int
	}{
		{"view, files and console only", srvPath(id, ""), &pv, http.StatusForbidden},
		{"plugins on own server", srvPath(id, ""), &pp, http.StatusOK},
		{"plugins on other server", srvPath(other, ""), &pp, http.StatusForbidden},
		{"anonymous", srvPath(id, ""), nil, http.StatusUnauthorized},
	} {
		if rec := e.do(http.MethodGet, tc.path, nil, tc.p); rec.Code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body, tc.code)
		}
	}
}

func TestInventoryFileServiceErrors(t *testing.T) {
	e := newEnvWith(t, func(d *plugins.Deps) { d.Files = nil })
	id := e.paper("survival")
	a := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != httpx.CodeFilesUnavailable {
		t.Fatalf("no service: %d %s", rec.Code, rec.Body)
	}
	e2 := newEnv(t)
	id2 := e2.paper("survival")
	e2.files.setErr("jars", fmt.Errorf("%w: down", filesapi.ErrUnavailable))
	a2 := e2.admin()
	if rec := e2.do(http.MethodGet, srvPath(id2, ""), nil, &a2); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("service down: %d %s", rec.Code, rec.Body)
	}
}

func TestRestartNeeded(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	if e.inventory(id).RestartNeeded {
		t.Fatal("restart_needed without a change")
	}
	plugins.MarkChanged(e.h, id, t0)
	for _, tc := range []struct {
		st   agentapi.InstanceStatus
		want bool
	}{
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Minute).UnixMilli()}, true},
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: 0}, true},
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(time.Minute).UnixMilli()}, false},
		{agentapi.InstanceStatus{ID: id, State: agentapi.StateExited}, false},
	} {
		e.agent.set(tc.st)
		if got := e.inventory(id).RestartNeeded; got != tc.want {
			t.Errorf("%+v: restart_needed = %v, want %v", tc.st, got, tc.want)
		}
	}
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Minute).UnixMilli()})
	e.agent.setErr(agentapi.ErrUnavailable)
	if e.inventory(id).RestartNeeded {
		t.Fatal("restart_needed without agent")
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `git add -N internal/plugins && make check`
Expected: FAIL beim Kompilieren der Tests (`undefined: plugins.MarkChanged`, Felder von `restartTracker` …).

- [ ] **Step 4: Implementierung schreiben**

`internal/plugins/handler.go` (ersetzt die Phase-A-Datei; Aufgaben 10–12 ändern `Handler`, `NewHandler` und `Run`):

```go
// Package plugins implements plugins and mods of a Minecraft server (sub-project 3c): search
// in Modrinth and Hangar, the inventory of the jars on disk, resolving required dependencies,
// install and update jobs, switching jars on and off, deleting them and the icon proxy. Files
// are only ever written by the unprivileged mcpanel-files service.
package plugins

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Catalog resolves LATEST to the newest version of an instance type (implemented by
// *catalog.Client; same method set as servers.CatalogSource).
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

type Handler struct {
	q       *store.Queries
	clock   clock.Clock
	rec     audit.Recorder
	files   filesapi.Client
	inst    agentapi.InstancesClient
	catalog Catalog
	up      *upstream.Client
	log     *slog.Logger
	restart *restartTracker
}

func NewHandler(d Deps) *Handler {
	fc := d.Files
	if fc == nil {
		fc = noFiles{}
	}
	inst := d.Instances
	if inst == nil {
		inst = noAgent{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	cat := d.Catalog
	if cat == nil {
		cat = catalog.New(catalog.Options{Clock: d.Clock, Version: d.Version})
	}
	up := d.Upstream
	if up == nil {
		up = upstream.New(upstream.Options{Clock: d.Clock, Version: d.Version})
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: fc, inst: inst, catalog: cat, up: up, log: log,
		restart: newRestartTracker(),
	}
}

// Routes registers the plugin routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerPlugins, "id")
	api.Get("/servers/{id}/plugins", rule, h.handleList)
	api.Get("/servers/{id}/plugins/search", rule, h.handleSearch)
	api.Get("/servers/{id}/plugins/versions", rule, h.handleVersions)
	api.Get("/servers/{id}/plugins/resolve", rule, h.handleResolve)
	api.Post("/servers/{id}/plugins/install", rule, h.handleInstall)
	api.Get("/servers/{id}/plugins/updates", rule, h.handleUpdates)
	api.Post("/servers/{id}/plugins/update", rule, h.handleUpdate)
	api.Post("/servers/{id}/plugins/toggle", rule, h.handleToggle)
	api.Post("/servers/{id}/plugins/delete", rule, h.handleDelete)
	api.Get("/servers/{id}/plugins/jobs", rule, h.handleJobs)
	api.Get("/servers/{id}/plugins/icon", rule, h.handleIcon)
}

// Run blocks until ctx is done (task 11: then it cancels running jobs and waits for them).
func (h *Handler) Run(ctx context.Context) { <-ctx.Done() }

// server is a loaded server with an instance.
type server struct {
	ID        int64
	Name      string
	Type      instancespec.Type
	Version   string // the instance's version field, may be "LATEST"
	Kind      kindInfo
}

// loadServer returns the server of the {id} path parameter if it has an instance; it writes
// 404 for malformed ids, unknown servers and servers without instance, and with requireKind
// 409 plugins_unsupported for types without plugins or mods (spec §6.6).
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request, requireKind bool) (server, bool) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeServerNotFound(w, r)
		return server{}, false
	}
	s, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return server{}, false
	}
	inst, err := h.q.ServersGetInstance(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeServerNotFound(w, r)
		return server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return server{}, false
	}
	t := instancespec.Type(inst.Type)
	k, ok := kindOf(t)
	if requireKind && !ok { // a server without plugins keeps the zero kindInfo (icon route only)
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodePluginsUnsupported, "this server type has no plugins or mods")
		return server{}, false
	}
	return server{ID: s.ID, Name: s.Name, Type: t, Version: inst.Version, Kind: k}, true
}

func writeServerNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

func result(err error) audit.Result {
	if err != nil {
		return audit.ResultFailure
	}
	return audit.ResultSuccess
}

// record writes an audit entry; actor and IP come from ctx (a job's context keeps those of
// the request that started it). The change already happened, so the entry is written even if
// the request is gone, and a failing audit write is logged.
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: serverTarget(serverID), Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "server_id", serverID, "err", err)
	}
}

var (
	errCatalog          = errors.New("the version catalog is unavailable, LATEST cannot be resolved")
	errIncompatible     = errors.New("no compatible version")
	errNotDownloadable  = errors.New("only available externally")
	errAlreadyInstalled = errors.New("the project is already installed")
	errFileExists       = errors.New("a file with this name already exists")
	errJobRunning       = errors.New("a plugin job is running for this server")
	errTooManyJobs      = errors.New("too many plugin jobs are running, try again shortly")
	errShuttingDown     = errors.New("the panel is shutting down")
	errNoChecksum       = errors.New("the source announces no checksum")
)

// classify maps an error to its API answer; ok is false for an internal error.
func classify(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, errCatalog):
		return http.StatusBadGateway, httpx.CodeCatalogUnavailable, errCatalog.Error(), true
	case errors.Is(err, errIncompatible):
		return http.StatusConflict, httpx.CodePluginIncompatible, err.Error(), true
	case errors.Is(err, errNotDownloadable):
		return http.StatusConflict, httpx.CodePluginNotDownloadable, err.Error(), true
	case errors.Is(err, errAlreadyInstalled):
		return http.StatusConflict, httpx.CodeConflict, errAlreadyInstalled.Error(), true
	case errors.Is(err, errFileExists):
		return http.StatusConflict, httpx.CodeFileExists, err.Error(), true
	case errors.Is(err, errJobRunning):
		return http.StatusConflict, httpx.CodeConflict, errJobRunning.Error(), true
	case errors.Is(err, errTooManyJobs):
		return http.StatusTooManyRequests, httpx.CodeRateLimited, errTooManyJobs.Error(), true
	case errors.Is(err, errShuttingDown):
		return http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, errShuttingDown.Error(), true
	case errors.Is(err, upstream.ErrNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, "project or version not found", true
	case errors.Is(err, upstream.ErrUnavailable), errors.Is(err, upstream.ErrDownload):
		return http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "Modrinth or Hangar is not reachable or answered unusably", true
	case errors.Is(err, filesapi.ErrNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, "file not found", true
	case errors.Is(err, filesapi.ErrExists):
		return http.StatusConflict, httpx.CodeFileExists, errFileExists.Error(), true
	case errors.Is(err, filesapi.ErrInvalid), errors.Is(err, filesapi.ErrNotRegular):
		return http.StatusBadRequest, httpx.CodeValidation, "the file service refused the request", true
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, filesapi.ErrBusy), errors.Is(err, filesapi.ErrTooLarge),
		errors.Is(err, filesapi.ErrInsufficientStorage), errors.Is(err, filesapi.ErrHashMismatch), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the file service is not reachable", true
	}
	return http.StatusInternalServerError, httpx.CodeInternal, "internal server error", false
}

// ErrorStatus returns the HTTP status and error code err is answered with (the contract test in
// cmd/panel checks them against api/openapi.yaml).
func ErrorStatus(err error) (status int, code string) {
	status, code, _, _ = classify(err)
	return status, code
}

// writeError answers err (classify); a client that went away gets nothing.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		h.log.DebugContext(r.Context(), "plugins: request ended", "err", err)
		return
	}
	status, code, msg, ok := classify(err)
	if !ok {
		httpx.Internal(w, r, err)
		return
	}
	if status >= 500 {
		h.log.WarnContext(r.Context(), "plugins: request failed", "code", code, "err", err)
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "5")
	}
	httpx.WriteError(w, r, status, code, msg)
}

var errNoFiles = fmt.Errorf("%w: no file service configured", filesapi.ErrUnavailable)

// noFiles stands in for a missing service client.
type noFiles struct{}

func (noFiles) Health(context.Context) error { return errNoFiles }
func (noFiles) List(context.Context, int64, string) (filesapi.ListResponse, error) {
	return filesapi.ListResponse{}, errNoFiles
}
func (noFiles) Read(context.Context, int64, string) (filesapi.ReadResponse, error) {
	return filesapi.ReadResponse{}, errNoFiles
}
func (noFiles) Write(context.Context, filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	return filesapi.WriteResponse{}, errNoFiles
}
func (noFiles) Upload(context.Context, int64, string, bool, int64, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNoFiles
}
func (noFiles) UploadChecked(context.Context, int64, string, bool, int64, filesapi.Checksum, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNoFiles
}
func (noFiles) Download(context.Context, int64, string) (filesapi.Download, error) {
	return filesapi.Download{}, errNoFiles
}
func (noFiles) Mkdir(context.Context, int64, string) error           { return errNoFiles }
func (noFiles) Rename(context.Context, filesapi.RenameRequest) error { return errNoFiles }
func (noFiles) Delete(context.Context, int64, string) error          { return errNoFiles }
func (noFiles) Extract(context.Context, filesapi.ExtractRequest) (filesapi.Job, error) {
	return filesapi.Job{}, errNoFiles
}
func (noFiles) Job(context.Context, int64) (*filesapi.Job, error) { return nil, errNoFiles }
func (noFiles) Jars(context.Context, int64, string) (filesapi.JarsResponse, error) {
	return filesapi.JarsResponse{}, errNoFiles
}

// noAgent stands in for a missing agent client: no server counts as running.
type noAgent struct{}

func (noAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	return agentapi.InstancesResponse{}, agentapi.ErrUnavailable
}
func (noAgent) Ensure(context.Context, agentapi.EnsureRequest) error     { return agentapi.ErrUnavailable }
func (noAgent) Power(context.Context, int64, agentapi.PowerAction) error { return agentapi.ErrUnavailable }
func (noAgent) Remove(context.Context, int64) error                      { return agentapi.ErrUnavailable }
```

`internal/plugins/compat.go`:

```go
package plugins

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// kindInfo describes what a server type installs (spec §3.1).
type kindInfo struct {
	Kind        string // "plugin" | "mod"
	Dir         string // filesapi.DirPlugins | filesapi.DirMods
	ProjectType string // Modrinth search facet
	Loaders     []string
	Sources     []upstream.Source
}

var bothSources = []upstream.Source{upstream.Modrinth, upstream.Hangar}

var kinds = map[instancespec.Type]kindInfo{
	instancespec.TypePaper: {Kind: "plugin", Dir: filesapi.DirPlugins, ProjectType: "plugin",
		Loaders: []string{"paper", "spigot", "bukkit"}, Sources: bothSources},
	instancespec.TypePurpur: {Kind: "plugin", Dir: filesapi.DirPlugins, ProjectType: "plugin",
		Loaders: []string{"purpur", "paper", "spigot", "bukkit"}, Sources: bothSources},
	instancespec.TypeFabric: {Kind: "mod", Dir: filesapi.DirMods, ProjectType: "mod",
		Loaders: []string{"fabric"}, Sources: []upstream.Source{upstream.Modrinth}},
	instancespec.TypeNeoForge: {Kind: "mod", Dir: filesapi.DirMods, ProjectType: "mod",
		Loaders: []string{"neoforge"}, Sources: []upstream.Source{upstream.Modrinth}},
}

func kindOf(t instancespec.Type) (kindInfo, bool) {
	k, ok := kinds[t]
	return k, ok
}

func (k kindInfo) hasSource(s upstream.Source) bool { return slices.Contains(k.Sources, s) }

func (k kindInfo) isMod() bool { return k.Kind == "mod" }

// pageURL is the project page (spec §6.6); a Hangar project without known owner links to the
// Hangar start page (its page URL needs the owner).
func pageURL(src upstream.Source, kind, owner, slug string) string {
	if src == upstream.Hangar {
		if owner == "" || slug == "" {
			return "https://hangar.papermc.io/"
		}
		return "https://hangar.papermc.io/" + url.PathEscape(owner) + "/" + url.PathEscape(slug)
	}
	return "https://modrinth.com/" + kind + "/" + url.PathEscape(slug)
}

// compatible reports whether v runs on a server of kind k with game version gv: the game
// version must be listed, and a Modrinth version must name one of the kind's loaders (never the
// project_type, which Modrinth reports as "mod" for plugins as well).
func compatible(v upstream.Version, k kindInfo, gv string) bool {
	if !slices.Contains(v.GameVersions, gv) {
		return false
	}
	if v.Source == upstream.Hangar {
		return true
	}
	return slices.ContainsFunc(v.Loaders, func(l string) bool { return slices.Contains(k.Loaders, l) })
}

// gameVersion is the instance's Minecraft version; LATEST is resolved over the version catalog
// (errCatalog if that fails).
func (h *Handler) gameVersion(ctx context.Context, srv server) (string, error) {
	if srv.Version != "LATEST" {
		return srv.Version, nil
	}
	cat, err := h.catalog.Get(ctx, srv.Type)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errCatalog, err)
	}
	if len(cat.Versions) == 0 {
		return "", fmt.Errorf("%w: empty catalog", errCatalog)
	}
	return cat.Versions[0].ID, nil
}

// iconURL is the icon proxy URL of an upstream icon, or nil if there is none or it is not
// allowed.
func (h *Handler) iconURL(serverID int64, raw string) *string {
	if !h.up.IconURLAllowed(raw) {
		return nil
	}
	s := fmt.Sprintf("/api/servers/%d/plugins/icon?url=%s", serverID, url.QueryEscape(raw))
	return &s
}
```

`internal/plugins/restart.go`:

```go
package plugins

import (
	"context"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// stateTimeout bounds the agent query of restart_needed.
const stateTimeout = 2 * time.Second

// restartTracker remembers the last plugin change per server (in memory: forgotten when the
// panel restarts, spec §6.2).
type restartTracker struct {
	mu      sync.Mutex
	changed map[int64]time.Time
}

func newRestartTracker() *restartTracker { return &restartTracker{changed: map[int64]time.Time{}} }

func (t *restartTracker) mark(id int64, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.changed[id] = at
}

func (t *restartTracker) since(id int64) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.changed[id]
	return at, ok
}

// markChanged records a plugin change of the server now.
func (h *Handler) markChanged(serverID int64) { h.restart.mark(serverID, h.clock.Now()) }

// restartNeeded reports a running server whose container started before the last plugin change
// of the panel (a start time of 0 counts as before). Without agent answer: false.
func (h *Handler) restartNeeded(ctx context.Context, serverID int64) bool {
	at, ok := h.restart.since(serverID)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, stateTimeout)
	defer cancel()
	resp, err := h.inst.Instances(ctx)
	if err != nil {
		return false
	}
	for _, st := range resp.Instances {
		if st.ID == serverID {
			return st.State == agentapi.StateRunning && (st.StartedAt == 0 || at.UnixMilli() >= st.StartedAt)
		}
	}
	return false
}
```

`internal/plugins/inventory.go`:

```go
package plugins

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	sourceManual = "manual"
	// projectFetches bounds the parallel project lookups of one inventory.
	projectFetches = 8
	disabledSuffix = ".disabled"
)

// projectRef is the OpenAPI schema PluginProjectRef.
type projectRef struct {
	ID      string  `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	PageURL string  `json:"page_url"`
	IconURL *string `json:"icon_url"`
}

// versionRef is the OpenAPI schema PluginVersionRef.
type versionRef struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Channel upstream.Channel `json:"channel"`
}

// installedJar is the OpenAPI schema InstalledJar plus internal fields.
type installedJar struct {
	File             string            `json:"file"`
	Enabled          bool              `json:"enabled"`
	Size             int64             `json:"size"`
	MTime            int64             `json:"mtime"`
	Source           string            `json:"source"`
	Project          *projectRef       `json:"project"`
	Version          *versionRef       `json:"version"`
	Meta             *filesapi.JarMeta `json:"meta"`
	InstalledByPanel bool              `json:"installed_by_panel"`

	base   string // file name without ".disabled"
	sha512 string
	owner  string // Hangar owner (page URL)
}

// inventory is the OpenAPI schema PluginInventory.
type inventory struct {
	Kind          string            `json:"kind"`
	Dir           string            `json:"dir"`
	GameVersion   *string           `json:"game_version"`
	Sources       []upstream.Source `json:"sources"`
	Jars          []installedJar    `json:"jars"`
	Truncated     bool              `json:"truncated"`
	RestartNeeded bool              `json:"restart_needed"`
	SourceErrors  []upstream.Source `json:"source_errors"`
}

func projectKey(src upstream.Source, id string) string { return string(src) + "/" + id }

// baseName strips ".disabled" (any case) from a jar name that filesapi.IsJarName accepted.
func baseName(name string, enabled bool) string {
	if enabled {
		return name
	}
	return name[:len(name)-len(disabledSuffix)]
}

func (inv *inventory) addSourceError(src upstream.Source) {
	if !slices.Contains(inv.SourceErrors, src) {
		inv.SourceErrors = append(inv.SourceErrors, src)
	}
}

// inventory lists the jars of the server and maps each (spec §6.2): DB row with the same name
// and sha512 → panel source; else Modrinth by hash; else manual. Rows without their jar are
// deleted, unless the listing is truncated or a job of the server runs (decision 9).
func (h *Handler) inventory(ctx context.Context, srv server) (inventory, error) {
	inv := inventory{
		Kind: srv.Kind.Kind, Dir: srv.Kind.Dir, Sources: srv.Kind.Sources,
		Jars: []installedJar{}, SourceErrors: []upstream.Source{},
	}
	// Rows first: a row a running job writes after this read is never taken for an orphan.
	rows, err := h.q.PluginsListInstalls(ctx, srv.ID)
	if err != nil {
		return inventory{}, err
	}
	list, err := h.files.Jars(ctx, srv.ID, srv.Kind.Dir)
	if err != nil {
		return inventory{}, err
	}
	inv.Truncated = list.Truncated
	byName := make(map[string]store.PluginInstall, len(rows))
	for _, r := range rows {
		byName[r.Filename] = r
	}
	used := map[string]bool{}
	var unknown []int
	for _, j := range list.Jars {
		enabled, ok := filesapi.IsJarName(j.Name)
		if !ok {
			continue
		}
		it := installedJar{
			File: j.Name, Enabled: enabled, Size: j.Size, MTime: j.MTime, Source: sourceManual, Meta: j.Meta,
			base: baseName(j.Name, enabled), sha512: j.SHA512,
		}
		if row, ok := byName[it.base]; ok && j.SHA512 != "" && row.Sha512 == j.SHA512 && !used[it.base] {
			used[it.base] = true
			it.Source, it.InstalledByPanel = row.Source, true
			it.Project = &projectRef{ID: row.ProjectID, Slug: row.ProjectSlug, Title: row.Title}
			it.Version = &versionRef{ID: row.VersionID, Name: row.VersionName, Channel: upstream.Channel(row.Channel)}
		} else if j.SHA512 != "" {
			unknown = append(unknown, len(inv.Jars))
		}
		inv.Jars = append(inv.Jars, it)
	}
	if !inv.Truncated && !h.jobRunning(srv.ID) {
		for _, r := range rows {
			if used[r.Filename] {
				continue
			}
			if err := h.q.PluginsDeleteInstall(ctx, store.PluginsDeleteInstallParams{ServerID: srv.ID, Filename: r.Filename}); err != nil {
				return inventory{}, err
			}
		}
	}
	if len(unknown) > 0 {
		h.mapByHash(ctx, &inv, unknown)
	}
	h.enrichProjects(ctx, srv, &inv)
	return inv, nil
}

// mapByHash asks Modrinth for the jars without a DB row; failures leave them manual and add a
// source error.
func (h *Handler) mapByHash(ctx context.Context, inv *inventory, idx []int) {
	hashes := make([]string, 0, len(idx))
	for _, i := range idx {
		hashes = append(hashes, inv.Jars[i].sha512)
	}
	found, err := h.up.VersionsByHash(ctx, hashes)
	if err != nil {
		h.log.WarnContext(ctx, "plugins: modrinth hash lookup failed", "err", err)
		inv.addSourceError(upstream.Modrinth)
		return
	}
	for _, i := range idx {
		j := &inv.Jars[i]
		v, ok := found[j.sha512]
		if !ok {
			continue
		}
		j.Source = string(upstream.Modrinth)
		j.Project = &projectRef{ID: v.ProjectID}
		j.Version = &versionRef{ID: v.ID, Name: v.Name, Channel: v.Channel}
	}
}

// enrichProjects fills slug, title, owner and icon of every mapped jar from the (cached)
// project lookups, at most projectFetches at a time; a failed lookup adds a source error.
func (h *Handler) enrichProjects(ctx context.Context, srv server, inv *inventory) {
	type key struct {
		src upstream.Source
		id  string
	}
	want := map[key]bool{}
	for _, j := range inv.Jars {
		if j.Project != nil {
			want[key{upstream.Source(j.Source), j.Project.ID}] = true
		}
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		got    = map[key]upstream.Project{}
		failed = map[upstream.Source]bool{}
		sem    = make(chan struct{}, projectFetches)
	)
	for k := range want {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			p, err := h.up.Project(ctx, k.src, k.id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed[k.src] = true
				return
			}
			got[k] = p
		})
	}
	wg.Wait()
	for i := range inv.Jars {
		j := &inv.Jars[i]
		if j.Project == nil {
			continue
		}
		src := upstream.Source(j.Source)
		if p, ok := got[key{src, j.Project.ID}]; ok {
			j.Project.Slug = cmp.Or(j.Project.Slug, p.Slug)
			j.Project.Title = cmp.Or(j.Project.Title, p.Title)
			j.owner = p.Owner
			j.Project.IconURL = h.iconURL(srv.ID, p.IconURL)
		}
		j.Project.PageURL = pageURL(src, srv.Kind.Kind, j.owner, cmp.Or(j.Project.Slug, j.Project.ID))
	}
	for _, src := range []upstream.Source{upstream.Modrinth, upstream.Hangar} {
		if failed[src] {
			inv.addSourceError(src)
		}
	}
}

// installedByProject indexes the mapped jars (enabled or not) by source and project id.
func installedByProject(inv inventory) map[string]installedJar {
	m := map[string]installedJar{}
	for _, j := range inv.Jars {
		if j.Project == nil || j.Source == sourceManual {
			continue
		}
		k := projectKey(upstream.Source(j.Source), j.Project.ID)
		if _, ok := m[k]; !ok {
			m[k] = j
		}
	}
	return m
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if gv, err := h.gameVersion(ctx, srv); err == nil {
		inv.GameVersion = &gv
	} else {
		h.log.DebugContext(ctx, "plugins: game version unknown", "server_id", srv.ID, "err", err)
	}
	inv.RestartNeeded = h.restartNeeded(ctx, srv.ID)
	httpx.WriteJSON(w, http.StatusOK, inv)
}
```

`internal/plugins/stubs.go` (Übergang; Aufgaben 10–12 löschen die Einträge, Aufgabe 12 die Datei):

```go
package plugins

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

func notYet(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "plugins not implemented yet")
}

// task 10
func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request)   { notYet(w, r) }
func (h *Handler) handleVersions(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleResolve(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }

// task 11
func (h *Handler) handleInstall(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleUpdates(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request)    { notYet(w, r) }
func (h *Handler) jobRunning(int64) bool                                 { return false }

// task 12
func (h *Handler) handleToggle(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handleIcon(w http.ResponseWriter, r *http.Request)   { notYet(w, r) }
```

- [ ] **Step 5: Checks und CI**

Run: `make check`
Expected: PASS.

```bash
git add internal/plugins
git commit -m "feat(plugins): handler skeleton, server kinds and jar inventory

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI PASS für `TestRouteContract`, `TestRunReturnsWhenCancelled`, `TestKindOf`, `TestPageURL`, `TestCompatible`, `TestInventory*`, `TestPluginsPermissions`, `TestRestartNeeded`.

---

### Task 10: Auflösen, Suche, Versionen und Rate-Limit

Der `planner` baut aus Projekt und gewünschter Version den Installationsplan (Spec §6.3); `search`, `versions` und `resolve` werden echt, alle drei laufen durch das Rate-Limit von 60 Aufrufen pro Minute und Konto.

**Files:**
- Create: `internal/plugins/resolve.go`, `internal/plugins/search.go`, `internal/plugins/ratelimit.go`
- Modify: `internal/plugins/handler.go` (Feld `limit`), `internal/plugins/stubs.go` (drei Einträge weg)
- Test: `internal/plugins/resolve_test.go`, `internal/plugins/search_test.go`

**Interfaces:**
- Consumes: Aufgabe 9 (`server`, `kindInfo`, `compatible`, `pageURL`, `iconURL`, `gameVersion`, `inventory`, `installedByProject`, `projectKey`, `writeError`, `writeValidation`, `errIncompatible`, `errNotDownloadable`); `upstream.Client` (`Search`, `Project`, `Versions`, `Version`), `upstream.FileName`; `golang.org/x/time/rate`.
- Produces:

```go
type planEntry struct {
	Source      upstream.Source  `json:"source"`
	ProjectID   string           `json:"project_id"`
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	VersionID   string           `json:"version_id"`
	VersionName string           `json:"version_name"`
	Channel     upstream.Channel `json:"channel"`
	Filename    string           `json:"filename"`
	Size        int64            `json:"size"`
	RequiredBy  *string          `json:"required_by"`

	file            upstream.File
	replaces        string // installed jar this entry replaces (version change, update)
	replacesEnabled bool
}
type resolution struct{ Install []planEntry; Already []alreadyEntry; External []externalEntry; Incompatible []incompatibleEntry }
type resolveRequest struct{ Source upstream.Source; Project, Version string }
func (h *Handler) resolve(ctx context.Context, srv server, gv string, inv inventory, req resolveRequest) (resolution, error)
func newPlanner(h *Handler, srv server, gv string, installed map[string]installedJar, seen map[string]bool) *planner
func (p *planner) dependencies(ctx context.Context, res *resolution, v upstream.Version, src upstream.Source, parent string) ([]planEntry, error)
func (p *planner) entry(proj upstream.Project, v upstream.Version, requiredBy *string) planEntry
func validRef(s string) bool
func safeLink(raw string) string
func (h *Handler) allow(w http.ResponseWriter, r *http.Request) bool
```

- [ ] **Step 1: Tests schreiben**

`internal/plugins/resolve_test.go`:

```go
package plugins_test

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

type planJSON struct {
	Source      string  `json:"source"`
	ProjectID   string  `json:"project_id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	VersionID   string  `json:"version_id"`
	VersionName string  `json:"version_name"`
	Channel     string  `json:"channel"`
	Filename    string  `json:"filename"`
	Size        int64   `json:"size"`
	RequiredBy  *string `json:"required_by"`
}

type resJSON struct {
	Install []planJSON `json:"install"`
	Already []struct {
		Source    string `json:"source"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		File      string `json:"file"`
	} `json:"already"`
	External []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"external"`
	Incompatible []struct {
		Title  string `json:"title"`
		Reason string `json:"reason"`
	} `json:"incompatible"`
}

// resolve asks the resolve route as admin; it returns the status and, for 200, the plan.
func (e *env) resolve(id int64, src, project, version string) (int, resJSON, string) {
	e.t.Helper()
	q := url.Values{"source": {src}, "project": {project}}
	if version != "" {
		q.Set("version", version)
	}
	a := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, "/resolve?"+q.Encode()), nil, &a)
	if rec.Code != http.StatusOK {
		return rec.Code, resJSON{}, errCode(e.t, rec)
	}
	return rec.Code, decode[resJSON](e.t, rec), ""
}

func (r resJSON) installTitles() []string {
	var out []string
	for _, p := range r.Install {
		out = append(out, p.Title)
	}
	return out
}

// fabricWorld registers Chunky (requires Fabric API) and Fabric API on the fake.
func fabricWorld(e *env) (chunky, fapi *upFile) {
	fapi = e.up.jar("fabric-api-0.141.6+1.21.11.jar", "fabric api 0.141.6")
	e.up.modrinth("P7dR8mSH", "fabric-api", "Fabric API", "optional",
		mv{ID: "6qAuTtLR", Number: "0.141.6+1.21.11", File: fapi, Loaders: []string{"fabric"}})
	chunky = e.up.jar("Chunky-Fabric-1.4.55.jar", "chunky 1.4.55")
	e.up.modrinth("fALzjamp", "chunky", "Chunky", "optional",
		mv{ID: "1CpEkmcD", Number: "1.4.55", File: chunky, Loaders: []string{"fabric"}, Deps: []map[string]any{mdep("P7dR8mSH", "")}})
	return chunky, fapi
}

func TestResolveWithDependency(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	code, res, ec := e.resolve(id, "modrinth", "chunky", "")
	if code != http.StatusOK {
		t.Fatalf("resolve: %d %s", code, ec)
	}
	if len(res.Install) != 2 {
		t.Fatalf("install = %+v", res.Install)
	}
	dep, root := res.Install[0], res.Install[1]
	if dep.ProjectID != "P7dR8mSH" || dep.Filename != fapi.Name || dep.Size != fapi.Size || dep.VersionID != "6qAuTtLR" ||
		dep.VersionName != "0.141.6+1.21.11" || dep.Channel != "release" || dep.RequiredBy == nil || *dep.RequiredBy != "Chunky" {
		t.Fatalf("dependency = %+v", dep)
	}
	if root.ProjectID != "fALzjamp" || root.Slug != "chunky" || root.Filename != chunky.Name || root.RequiredBy != nil {
		t.Fatalf("root = %+v", root)
	}
	if len(res.Already)+len(res.External)+len(res.Incompatible) != 0 {
		t.Fatalf("resolution = %+v", res)
	}
}

func TestResolveAlready(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	// Fabric API uploaded by hand and switched off: still installed (hash mapping)
	e.files.put(id, "mods/fapi.jar.disabled", fapi.content)
	_, res, _ := e.resolve(id, "modrinth", "chunky", "")
	if !slices.Equal(res.installTitles(), []string{"Chunky"}) || len(res.Already) != 1 ||
		res.Already[0].ProjectID != "P7dR8mSH" || res.Already[0].File != "fapi.jar.disabled" {
		t.Fatalf("resolution = %+v", res)
	}
	// the requested project itself is installed: nothing to do
	e.files.put(id, "mods/chunky.jar", chunky.content)
	_, res, _ = e.resolve(id, "modrinth", "chunky", "")
	if len(res.Install) != 0 || len(res.Already) != 1 || res.Already[0].File != "chunky.jar" {
		t.Fatalf("installed root = %+v", res)
	}
}

// Decision 4: another version of an installed project is a replacement, the same one is "already".
func TestResolveVersionChange(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	newJar, oldJar := e.up.jar("Tool-2.jar", "tool 2"), e.up.jar("Tool-1.jar", "tool 1")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: newJar}, mv{ID: "t1", File: oldJar})
	e.files.put(id, "plugins/Tool-1.jar", oldJar.content)
	e.seedRow(id, "Tool-1.jar", oldJar.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	_, res, _ := e.resolve(id, "modrinth", "TOOL", "t2")
	if len(res.Install) != 1 || res.Install[0].VersionID != "t2" || res.Install[0].Filename != "Tool-2.jar" || len(res.Already) != 0 {
		t.Fatalf("version change = %+v", res)
	}
	_, res, _ = e.resolve(id, "modrinth", "TOOL", "t1")
	if len(res.Install) != 0 || len(res.Already) != 1 {
		t.Fatalf("same version = %+v", res)
	}
}

func TestResolveHangarDependencyAndExternal(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.hangar(31, "ViaVersion", "ViaVersion",
		hv{Name: "5.12.2-SNAPSHOT+1", Channel: "Snapshot", File: e.up.jar("ViaVersion-5.12.2-SNAPSHOT.jar", "vv snap")},
		hv{Name: "5.12.1", File: e.up.jar("ViaVersion-5.12.1.jar", "vv 5.12.1")})
	e.up.hangar(12, "ViaVersion", "ViaBackwards",
		hv{Name: "5.12.1", File: e.up.jar("ViaBackwards-5.12.1.jar", "vb 5.12.1"), Deps: []map[string]any{
			hdep("ViaVersion", 31, ""),
			hdep("Vault", 0, "https://www.spigotmc.org/resources/vault.34315/"),
			hdep("Evil", 0, "javascript:alert(1)"),
		}})
	code, res, ec := e.resolve(id, "hangar", "12", "")
	if code != http.StatusOK {
		t.Fatalf("resolve: %d %s", code, ec)
	}
	if !slices.Equal(res.installTitles(), []string{"ViaVersion", "ViaBackwards"}) {
		t.Fatalf("install = %+v", res.Install)
	}
	if vv := res.Install[0]; vv.VersionID != "5.12.1" || vv.Channel != "release" || vv.Source != "hangar" || vv.ProjectID != "31" {
		t.Fatalf("ViaVersion must be the newest Release, got %+v", vv)
	}
	if len(res.External) != 2 || res.External[0].Name != "Vault" || res.External[0].URL != "https://www.spigotmc.org/resources/vault.34315/" ||
		res.External[1].Name != "Evil" || res.External[1].URL != "" {
		t.Fatalf("external = %+v (only http(s) links pass)", res.External)
	}
}

func TestResolveIncompatibleDependency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("NEWLIB", "newlib", "New Lib", "required", mv{ID: "n1", GameVersions: []string{"26.3"}, File: e.up.jar("NewLib.jar", "n")})
	e.up.modrinth("APP", "app", "App", "required",
		mv{ID: "a1", File: e.up.jar("App.jar", "a"), Deps: []map[string]any{mdep("NEWLIB", ""), mdep("MISSING", "")}})
	code, res, _ := e.resolve(id, "modrinth", "APP", "")
	if code != http.StatusOK || !slices.Equal(res.installTitles(), []string{"App"}) || len(res.Incompatible) != 2 {
		t.Fatalf("resolution = %d %+v", code, res)
	}
	if res.Incompatible[0].Title != "New Lib" || res.Incompatible[0].Reason != "no_compatible_version" ||
		res.Incompatible[1].Title != "MISSING" || res.Incompatible[1].Reason != "no_compatible_version" {
		t.Fatalf("incompatible = %+v", res.Incompatible)
	}
}

func TestResolveRequestedErrors(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("FUTURE", "future", "Future", "required", mv{ID: "f1", GameVersions: []string{"26.3"}, File: e.up.jar("F.jar", "f")})
	e.up.modrinth("OTHER", "other", "Other", "required", mv{ID: "o1", File: e.up.jar("O.jar", "o")})
	e.up.modrinth("FAB", "fab", "Fabric only", "required", mv{ID: "fab1", Loaders: []string{"fabric"}, File: e.up.jar("Fab.jar", "x")})
	e.up.hangar(23, "EssentialsX", "Essentials", hv{Name: "2.22.0", External: "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0"})
	for _, tc := range []struct {
		src, project, version string
		code                  int
		ec                    string
	}{
		{"modrinth", "FUTURE", "", http.StatusConflict, httpx.CodePluginIncompatible},
		{"modrinth", "FAB", "", http.StatusConflict, httpx.CodePluginIncompatible},
		{"modrinth", "FUTURE", "f1", http.StatusConflict, httpx.CodePluginIncompatible},
		{"modrinth", "FUTURE", "o1", http.StatusConflict, httpx.CodePluginIncompatible}, // version of another project
		{"hangar", "23", "", http.StatusConflict, httpx.CodePluginNotDownloadable},
		{"hangar", "Essentials", "2.22.0", http.StatusConflict, httpx.CodePluginNotDownloadable},
		{"modrinth", "NOPE", "", http.StatusNotFound, httpx.CodeNotFound},
		{"modrinth", "OTHER", "nope", http.StatusNotFound, httpx.CodeNotFound},
	} {
		if code, _, ec := e.resolve(id, tc.src, tc.project, tc.version); code != tc.code || ec != tc.ec {
			t.Errorf("%s %s %s: %d %s, want %d %s", tc.src, tc.project, tc.version, code, ec, tc.code, tc.ec)
		}
	}
}

func TestResolveFixedVersionID(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("LIB", "lib", "Lib", "required",
		mv{ID: "lib-new", File: e.up.jar("Lib-2.jar", "lib 2")},
		mv{ID: "lib-old", File: e.up.jar("Lib-1.jar", "lib 1")},
		mv{ID: "lib-26", GameVersions: []string{"26.3"}, File: e.up.jar("Lib-26.jar", "lib 26")})
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a1", File: e.up.jar("A.jar", "a"), Deps: []map[string]any{mdep("LIB", "lib-old")}})
	e.up.modrinth("B", "b", "B", "required", mv{ID: "b1", File: e.up.jar("B.jar", "b"), Deps: []map[string]any{mdep("", "lib-old")}})
	e.up.modrinth("C", "c", "C", "required", mv{ID: "c1", File: e.up.jar("C.jar", "c"), Deps: []map[string]any{mdep("LIB", "lib-26")}})
	for _, p := range []string{"A", "B"} {
		_, res, _ := e.resolve(id, "modrinth", p, "")
		if len(res.Install) != 2 || res.Install[0].VersionID != "lib-old" || res.Install[0].ProjectID != "LIB" {
			t.Errorf("%s: %+v", p, res.Install)
		}
	}
	_, res, _ := e.resolve(id, "modrinth", "C", "")
	if len(res.Incompatible) != 1 || res.Incompatible[0].Title != "Lib" || res.Incompatible[0].Reason != "no_compatible_version" {
		t.Fatalf("fixed incompatible version = %+v", res)
	}
}

func TestResolveChannel(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("MIX", "mix", "Mix", "required",
		mv{ID: "beta2", Type: "beta", File: e.up.jar("Mix-2b.jar", "2b")},
		mv{ID: "rel1", File: e.up.jar("Mix-1.jar", "1")})
	e.up.modrinth("BETA", "beta", "Beta only", "required", mv{ID: "b1", Type: "beta", File: e.up.jar("B.jar", "b")})
	if _, res, _ := e.resolve(id, "modrinth", "MIX", ""); res.Install[0].VersionID != "rel1" {
		t.Errorf("default must be the newest release: %+v", res.Install)
	}
	if _, res, _ := e.resolve(id, "modrinth", "BETA", ""); res.Install[0].VersionID != "b1" || res.Install[0].Channel != "beta" {
		t.Errorf("without release the newest version: %+v", res.Install)
	}
	if _, res, _ := e.resolve(id, "modrinth", "MIX", "beta2"); res.Install[0].VersionID != "beta2" {
		t.Errorf("explicit beta: %+v", res.Install)
	}
}

func TestResolveClientOnly(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	e.up.modrinth("PtjYWJkn", "sodium-extra", "Sodium Extra", "unsupported",
		mv{ID: "s1", Loaders: []string{"fabric"}, File: e.up.jar("sodium-extra.jar", "s")})
	e.up.modrinth("NEEDS", "needs", "Needs Sodium", "optional",
		mv{ID: "n1", Loaders: []string{"fabric"}, File: e.up.jar("Needs.jar", "n"), Deps: []map[string]any{mdep("PtjYWJkn", "")}})
	if code, _, ec := e.resolve(id, "modrinth", "sodium-extra", ""); code != http.StatusConflict || ec != httpx.CodePluginIncompatible {
		t.Fatalf("client-only root: %d %s", code, ec)
	}
	_, res, _ := e.resolve(id, "modrinth", "NEEDS", "")
	if len(res.Incompatible) != 1 || res.Incompatible[0].Reason != "client_only" || res.Incompatible[0].Title != "Sodium Extra" {
		t.Fatalf("client-only dependency = %+v", res)
	}
}

func TestResolveDepthAndLimit(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	// chain P0 -> P1 -> ... -> P7: depth 5 is the last one planned
	for i := 7; i >= 0; i-- {
		var deps []map[string]any
		if i < 7 {
			deps = []map[string]any{mdep(fmt.Sprintf("P%d", i+1), "")}
		}
		e.up.modrinth(fmt.Sprintf("P%d", i), fmt.Sprintf("p%d", i), fmt.Sprintf("P%d", i), "required",
			mv{ID: fmt.Sprintf("v%d", i), File: e.up.jar(fmt.Sprintf("P%d.jar", i), fmt.Sprint(i)), Deps: deps})
	}
	_, res, _ := e.resolve(id, "modrinth", "P0", "")
	if !slices.Equal(res.installTitles(), []string{"P5", "P4", "P3", "P2", "P1", "P0"}) ||
		len(res.Incompatible) != 1 || res.Incompatible[0].Title != "P6" || res.Incompatible[0].Reason != "too_deep" {
		t.Fatalf("chain = %v / %+v", res.installTitles(), res.Incompatible)
	}
	// 25 direct dependencies: 19 of them plus the root fill the 20 packages
	var deps []map[string]any
	for i := range 25 {
		pid := fmt.Sprintf("W%02d", i)
		e.up.modrinth(pid, "w"+pid, pid, "required", mv{ID: "wv" + pid, File: e.up.jar(pid+".jar", pid)})
		deps = append(deps, mdep(pid, ""))
	}
	e.up.modrinth("WIDE", "wide", "Wide", "required", mv{ID: "wide1", File: e.up.jar("Wide.jar", "wide"), Deps: deps})
	_, res, _ = e.resolve(id, "modrinth", "WIDE", "")
	if len(res.Install) != 20 || len(res.Incompatible) != 6 || res.Incompatible[0].Reason != "too_many" {
		t.Fatalf("wide = %d install, %+v", len(res.Install), res.Incompatible)
	}
}

func TestResolveCycle(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("CA", "ca", "Cycle A", "required", mv{ID: "a1", File: e.up.jar("CA.jar", "a"), Deps: []map[string]any{mdep("CB", "")}})
	e.up.modrinth("CB", "cb", "Cycle B", "required", mv{ID: "b1", File: e.up.jar("CB.jar", "b"), Deps: []map[string]any{mdep("CA", ""), mdep("CB", "")}})
	code, res, _ := e.resolve(id, "modrinth", "CA", "")
	if code != http.StatusOK || !slices.Equal(res.installTitles(), []string{"Cycle B", "Cycle A"}) || len(res.Incompatible) != 0 {
		t.Fatalf("cycle = %d %+v", code, res)
	}
}

func TestResolveValidation(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	a := e.admin()
	for _, q := range []string{
		"", "?source=modrinth", "?source=hangar&project=x", "?source=curseforge&project=x",
		"?source=modrinth&project=..%2Fx", "?source=modrinth&project=x&version=a%20b",
		"?source=modrinth&project=x%00",
	} {
		rec := e.do(http.MethodGet, srvPath(id, "/resolve"+q), nil, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%q: %d %s", q, rec.Code, rec.Body)
		}
	}
}
```

`internal/plugins/search_test.go`:

```go
package plugins_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type hitJSON struct {
	Source      string  `json:"source"`
	ProjectID   string  `json:"project_id"`
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Author      string  `json:"author"`
	Downloads   int64   `json:"downloads"`
	IconURL     *string `json:"icon_url"`
	PageURL     string  `json:"page_url"`
	Installed   bool    `json:"installed"`
}

type searchJSON struct {
	Hits   []hitJSON `json:"hits"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
}

func TestSearchModrinth(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("Vebnzrzj", "luckperms", "LuckPerms", "required")
	chunky := e.up.jar("Chunky.jar", "chunky")
	e.up.modrinth("fALzjamp", "chunky", "Chunky", "optional", mv{ID: "c1", File: chunky})
	e.files.put(id, "plugins/Chunky.jar", chunky.content)
	res := decode[searchJSON](t, e.get(srvPath(id, "/search?source=modrinth&q=perm&offset=20")))
	q := e.up.query()
	if q.Get("query") != "perm" || q.Get("offset") != "20" ||
		q.Get("facets") != `[["project_type:plugin"],["categories:paper","categories:spigot","categories:bukkit"],["versions:1.21.11"]]` {
		t.Fatalf("upstream query = %v", q)
	}
	if res.Total != 2 || res.Offset != 20 || len(res.Hits) != 2 {
		t.Fatalf("result = %+v", res)
	}
	lp, ch := res.Hits[0], res.Hits[1]
	if lp.Source != "modrinth" || lp.ProjectID != "Vebnzrzj" || lp.Installed || lp.Author != "author-luckperms" ||
		lp.PageURL != "https://modrinth.com/plugin/luckperms" || lp.IconURL == nil || *lp.IconURL != proxied(id, e.up.iconURL("Vebnzrzj")) {
		t.Fatalf("hit = %+v", lp)
	}
	if !ch.Installed {
		t.Fatalf("installed project not marked: %+v", ch)
	}
}

func TestSearchMods(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	e.up.modrinth("fALzjamp", "chunky", "Chunky", "optional")
	res := decode[searchJSON](t, e.get(srvPath(id, "/search?source=modrinth")))
	q := e.up.query()
	if q.Get("facets") != `[["project_type:mod"],["categories:fabric"],["versions:1.21.11"],["server_side:required","server_side:optional"]]` ||
		q.Get("index") != "downloads" {
		t.Fatalf("upstream query = %v", q)
	}
	if res.Hits[0].PageURL != "https://modrinth.com/mod/chunky" {
		t.Fatalf("hit = %+v", res.Hits[0])
	}
}

func TestSearchHangar(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.hangar(12, "ViaVersion", "ViaBackwards")
	res := decode[searchJSON](t, e.get(srvPath(id, "/search?source=hangar&q=via")))
	if q := e.up.query(); q.Get("q") != "via" || q.Get("platform") != "PAPER" || q.Get("version") != "1.21.11" {
		t.Fatalf("upstream query = %v", q)
	}
	if h := res.Hits[0]; h.Source != "hangar" || h.ProjectID != "12" || h.Author != "ViaVersion" ||
		h.PageURL != "https://hangar.papermc.io/ViaVersion/ViaBackwards" || h.IconURL == nil {
		t.Fatalf("hit = %+v", h)
	}
}

func TestSearchValidation(t *testing.T) {
	e := newEnv(t)
	paper := e.paper("survival")
	fab := e.fabric("modded")
	a := e.admin()
	long := url.QueryEscape(strings.Repeat("ä", 101))
	for _, tc := range []struct {
		id int64
		q  string
	}{
		{paper, ""}, {paper, "?source=curseforge"}, {fab, "?source=hangar"},
		{paper, "?source=modrinth&q=" + long}, {paper, "?source=modrinth&offset=-1"},
		{paper, "?source=modrinth&offset=1001"}, {paper, "?source=modrinth&offset=x"},
	} {
		rec := e.do(http.MethodGet, srvPath(tc.id, "/search"+tc.q), nil, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%d %q: %d %s", tc.id, tc.q, rec.Code, rec.Body)
		}
	}
	ok := url.QueryEscape(strings.Repeat("ä", 100))
	if rec := e.do(http.MethodGet, srvPath(paper, "/search?source=modrinth&offset=1000&q="+ok), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("limits themselves: %d %s", rec.Code, rec.Body)
	}
}

// Review Focus #4: upstream down → 502 plugin_source_unavailable; LATEST without catalog → 502
// catalog_unavailable; the file service is not needed for searching.
func TestSearchSourceDown(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	latest := createInstance(t, e.db, "latest", instancespec.TypePaper, "LATEST")
	a := e.admin()
	e.up.modrinth("X", "x", "X", "required")
	e.files.setErr("jars", filesapi.ErrUnavailable)
	if rec := e.do(http.MethodGet, srvPath(id, "/search?source=modrinth"), nil, &a); rec.Code != http.StatusOK ||
		decode[searchJSON](t, rec).Hits[0].Installed {
		t.Fatalf("without file service: %d %s", rec.Code, rec.Body)
	}
	e.up.setDown(true)
	rec := e.do(http.MethodGet, srvPath(id, "/search?source=modrinth&q=new"), nil, &a)
	if rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodePluginSourceUnavailable {
		t.Fatalf("upstream down: %d %s", rec.Code, rec.Body)
	}
	e.cat.setErr(errors.New("catalog down"))
	rec = e.do(http.MethodGet, srvPath(latest, "/search?source=modrinth"), nil, &a)
	if rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodeCatalogUnavailable {
		t.Fatalf("catalog down: %d %s", rec.Code, rec.Body)
	}
}

type versionsJSON struct {
	Versions []struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		Channel      string  `json:"channel"`
		PublishedAt  int64   `json:"published_at"`
		Downloadable bool    `json:"downloadable"`
		ExternalURL  *string `json:"external_url"`
	} `json:"versions"`
}

func TestVersionsList(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("MIX", "mix", "Mix", "required",
		mv{ID: "beta2", Number: "2.0-beta", Type: "beta", File: e.up.jar("Mix-2.jar", "2")},
		mv{ID: "rel1", Number: "1.0", File: e.up.jar("Mix-1.jar", "1")},
		mv{ID: "fut", GameVersions: []string{"26.3"}, File: e.up.jar("Mix-26.jar", "26")})
	e.up.hangar(23, "EssentialsX", "Essentials",
		hv{Name: "2.22.0", External: "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0"},
		hv{Name: "2.21.0", External: "javascript:alert(1)"})
	res := decode[versionsJSON](t, e.get(srvPath(id, "/versions?source=modrinth&project=MIX")))
	if len(res.Versions) != 2 || res.Versions[0].ID != "beta2" || res.Versions[0].Name != "2.0-beta" || res.Versions[0].Channel != "beta" ||
		!res.Versions[0].Downloadable || res.Versions[0].ExternalURL != nil || res.Versions[0].PublishedAt != t0.UnixMilli() ||
		res.Versions[1].ID != "rel1" {
		t.Fatalf("modrinth versions = %+v", res.Versions)
	}
	res = decode[versionsJSON](t, e.get(srvPath(id, "/versions?source=hangar&project=Essentials")))
	if len(res.Versions) != 2 || res.Versions[0].Downloadable || res.Versions[0].ExternalURL == nil ||
		*res.Versions[0].ExternalURL != "https://github.com/EssentialsX/Essentials/releases/tag/2.22.0" || res.Versions[1].ExternalURL != nil {
		t.Fatalf("hangar versions = %+v", res.Versions)
	}
	a := e.admin()
	if rec := e.do(http.MethodGet, srvPath(id, "/versions?source=modrinth"), nil, &a); rec.Code != http.StatusBadRequest {
		t.Fatalf("without project: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/versions?source=modrinth&project=NOPE"), nil, &a); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d %s", rec.Code, rec.Body)
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("X", "x", "X", "required", mv{ID: "x1", File: e.up.jar("X.jar", "x")})
	a := e.admin()
	paths := []string{"/search?source=modrinth", "/versions?source=modrinth&project=X", "/resolve?source=modrinth&project=X"}
	for i := range 20 {
		if rec := e.do(http.MethodGet, srvPath(id, paths[i%3]), nil, &a); rec.Code != http.StatusOK {
			t.Fatalf("call %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := e.do(http.MethodGet, srvPath(id, paths[0]), nil, &a)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("21st call: %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	// another account has its own bucket; the inventory is not limited
	other := storetest.CreateUser(t, e.db, "other", "user")
	grant(t, e.db, other, id, "server.view", "server.plugins")
	po := userP(other, "other")
	if rec := e.do(http.MethodGet, srvPath(id, paths[0]), nil, &po); rec.Code != http.StatusOK {
		t.Fatalf("other user: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, srvPath(id, ""), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("inventory limited: %d", rec.Code)
	}
	e.clk.Advance(time.Second)
	if rec := e.do(http.MethodGet, srvPath(id, paths[0]), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("after a second: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Rot prüfen**

Run: `git add -N internal/plugins && make check`
Expected: PASS beim Kompilieren (die Tests nutzen nur HTTP), CI-`go test` FAIL (`TestResolve*`, `TestSearch*`, `TestVersionsList`, `TestRateLimit`: die Stubs antworten `502`). Das ist hier der rote Zustand; der Commit aus Step 4 macht ihn grün.

- [ ] **Step 3: Implementierung schreiben**

In `internal/plugins/handler.go` die Typdefinition `Handler` und den `return` von `NewHandler` ersetzen:

```go
type Handler struct {
	q       *store.Queries
	clock   clock.Clock
	rec     audit.Recorder
	files   filesapi.Client
	inst    agentapi.InstancesClient
	catalog Catalog
	up      *upstream.Client
	log     *slog.Logger
	restart *restartTracker
	limit   *userLimiter
}
```

```go
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: fc, inst: inst, catalog: cat, up: up, log: log,
		restart: newRestartTracker(), limit: newUserLimiter(),
	}
```

In `internal/plugins/stubs.go` die drei Zeilen unter `// task 10` (`handleSearch`, `handleVersions`, `handleResolve`) samt Kommentar löschen.

`internal/plugins/ratelimit.go`:

```go
package plugins

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// Upstream lookups per account: 60 per minute, burst 20 (spec §6.6, decision 7) for search,
// versions, resolve and updates?refresh=1. Every lookup may reach Modrinth or Hangar, whose
// rate limit is per IP of the panel.
const (
	lookupEvery = time.Second
	lookupBurst = 20
)

// userLimiter keeps one token bucket per user id (bounded by the number of users).
type userLimiter struct {
	mu    sync.Mutex
	users map[int64]*rate.Limiter
}

func newUserLimiter() *userLimiter { return &userLimiter{users: map[int64]*rate.Limiter{}} }

// take consumes a token of userID at now; 0 = allowed, else the wait (no token consumed).
func (l *userLimiter) take(userID int64, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.users[userID]
	if !ok {
		lim = rate.NewLimiter(rate.Every(lookupEvery), lookupBurst)
		l.users[userID] = lim
	}
	r := lim.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}

// allow takes a lookup token of the caller; without one it writes 429 with Retry-After.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request) bool {
	pr, _ := principal.From(r.Context()) // guaranteed by the rbac rule
	d := h.limit.take(pr.UserID, h.clock.Now())
	if d <= 0 {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(int(math.Ceil(d.Seconds())), 1)))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many plugin lookups, try again later")
	return false
}
```

`internal/plugins/resolve.go`:

```go
package plugins

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

const (
	maxDepth    = 5  // dependency levels below the requested project
	maxPackages = 20 // packages in a plan, the requested one included

	reasonNoVersion       = "no_compatible_version"
	reasonClientOnly      = "client_only"
	reasonNotDownloadable = "not_downloadable"
	reasonTooDeep         = "too_deep"
	reasonTooMany         = "too_many"
)

// planEntry is the OpenAPI schema PluginPlanEntry plus what a job needs.
type planEntry struct {
	Source      upstream.Source  `json:"source"`
	ProjectID   string           `json:"project_id"`
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	VersionID   string           `json:"version_id"`
	VersionName string           `json:"version_name"`
	Channel     upstream.Channel `json:"channel"`
	Filename    string           `json:"filename"`
	Size        int64            `json:"size"`
	RequiredBy  *string          `json:"required_by"`

	file            upstream.File
	replaces        string // installed jar this entry replaces (version change, update)
	replacesEnabled bool
}

type alreadyEntry struct {
	Source    upstream.Source `json:"source"`
	ProjectID string          `json:"project_id"`
	Title     string          `json:"title"`
	File      string          `json:"file"`
}

type externalEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type incompatibleEntry struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// resolution is the OpenAPI schema PluginResolution.
type resolution struct {
	Install      []planEntry         `json:"install"`
	Already      []alreadyEntry      `json:"already"`
	External     []externalEntry     `json:"external"`
	Incompatible []incompatibleEntry `json:"incompatible"`
}

func newResolution() resolution {
	return resolution{Install: []planEntry{}, Already: []alreadyEntry{}, External: []externalEntry{}, Incompatible: []incompatibleEntry{}}
}

type resolveRequest struct {
	Source           upstream.Source
	Project, Version string // Version "" = newest compatible
}

// planner resolves required dependencies against the installed projects.
type planner struct {
	up        *upstream.Client
	kind      kindInfo
	gv        string
	installed map[string]installedJar // projectKey -> jar
	seen      map[string]bool         // projectKey of every project already looked at
}

func newPlanner(h *Handler, srv server, gv string, installed map[string]installedJar, seen map[string]bool) *planner {
	if seen == nil {
		seen = map[string]bool{}
	}
	return &planner{up: h.up, kind: srv.Kind, gv: gv, installed: installed, seen: seen}
}

// safeLink keeps only http(s) links of upstream texts (the browser renders them as links).
func safeLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return raw
}

// resolve builds the plan for the requested project (spec §6.3, decision 4). Errors: the
// requested project or version does not fit (errIncompatible), is only external
// (errNotDownloadable), is unknown (upstream.ErrNotFound) or a source is down.
func (h *Handler) resolve(ctx context.Context, srv server, gv string, inv inventory, req resolveRequest) (resolution, error) {
	p := newPlanner(h, srv, gv, installedByProject(inv), nil)
	res := newResolution()
	proj, err := p.up.Project(ctx, req.Source, req.Project)
	if err != nil {
		return resolution{}, err
	}
	if p.kind.isMod() && proj.ClientOnly() {
		return resolution{}, fmt.Errorf("%w: %s only runs on the client", errIncompatible, proj.Title)
	}
	v, err := p.rootVersion(ctx, proj, req.Version)
	if err != nil {
		return resolution{}, err
	}
	key := projectKey(proj.Source, proj.ID)
	p.seen[key] = true
	root := p.entry(proj, v, nil)
	if j, ok := p.installed[key]; ok {
		if req.Version == "" || (j.Version != nil && j.Version.ID == v.ID) {
			res.Already = append(res.Already, alreadyEntry{Source: proj.Source, ProjectID: proj.ID, Title: proj.Title, File: j.File})
			return res, nil
		}
		root.replaces, root.replacesEnabled = j.File, j.Enabled
	}
	deps, err := p.dependencies(ctx, &res, v, proj.Source, proj.Title)
	if err != nil {
		return resolution{}, err
	}
	res.Install = append(deps, root)
	return res, nil
}

// rootVersion picks the version of the requested project: want, or the newest compatible
// release, else the newest compatible version.
func (p *planner) rootVersion(ctx context.Context, proj upstream.Project, want string) (upstream.Version, error) {
	if want == "" {
		v, reason, err := p.pick(ctx, proj.Source, proj.ID)
		switch {
		case err != nil:
			return upstream.Version{}, err
		case reason == reasonNotDownloadable:
			return upstream.Version{}, fmt.Errorf("%w: %s offers compatible versions only as external downloads", errNotDownloadable, proj.Title)
		case reason != "":
			return upstream.Version{}, fmt.Errorf("%w: %s has no version for Minecraft %s", errIncompatible, proj.Title, p.gv)
		}
		return v, nil
	}
	v, err := p.up.Version(ctx, proj.Source, proj.ID, want)
	if err != nil {
		return upstream.Version{}, err
	}
	if v.ProjectID != proj.ID || !compatible(v, p.kind, p.gv) {
		return upstream.Version{}, fmt.Errorf("%w: version %s of %s does not fit this server", errIncompatible, want, proj.Title)
	}
	if !v.Downloadable() {
		return upstream.Version{}, fmt.Errorf("%w: version %s of %s is only available externally", errNotDownloadable, want, proj.Title)
	}
	return v, nil
}

// pick returns the newest compatible downloadable release, else the newest compatible
// downloadable version; reason tells why nothing fits.
func (p *planner) pick(ctx context.Context, src upstream.Source, projectID string) (upstream.Version, string, error) {
	external := false
	for _, releaseOnly := range []bool{true, false} {
		vs, err := p.up.Versions(ctx, src, projectID, upstream.VersionFilter{Loaders: p.kind.Loaders, GameVersion: p.gv, ReleaseOnly: releaseOnly})
		if err != nil {
			return upstream.Version{}, "", err
		}
		for _, v := range vs {
			if !compatible(v, p.kind, p.gv) {
				continue
			}
			if !v.Downloadable() {
				external = true
				continue
			}
			return v, "", nil
		}
	}
	if external {
		return upstream.Version{}, reasonNotDownloadable, nil
	}
	return upstream.Version{}, reasonNoVersion, nil
}

func (p *planner) entry(proj upstream.Project, v upstream.Version, requiredBy *string) planEntry {
	return planEntry{
		Source: proj.Source, ProjectID: proj.ID, Slug: proj.Slug, Title: proj.Title,
		VersionID: v.ID, VersionName: v.Name, Channel: v.Channel,
		Filename: upstream.FileName(v.File.Name, proj.Slug, v.Name), Size: v.File.Size,
		RequiredBy: requiredBy, file: *v.File,
	}
}

// dependencies plans the required dependencies of v breadth-first (depth ≤ maxDepth, plan ≤
// maxPackages including the requesting project) and returns them in install order, the deepest
// first. Installed, external and incompatible dependencies go into res.
func (p *planner) dependencies(ctx context.Context, res *resolution, v upstream.Version, src upstream.Source, parent string) ([]planEntry, error) {
	type item struct {
		dep    upstream.Dependency
		parent string
		depth  int
	}
	queue := make([]item, 0, len(v.Dependencies))
	for _, d := range v.Dependencies {
		queue = append(queue, item{d, parent, 1})
	}
	var planned []planEntry
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		e, next, err := p.dependency(ctx, res, it.dep, src, it.parent, it.depth, 1+len(planned))
		if err != nil {
			return nil, err
		}
		if e == nil {
			continue
		}
		planned = append(planned, *e)
		for _, d := range next {
			queue = append(queue, item{d, e.Title, it.depth + 1})
		}
	}
	slices.Reverse(planned)
	return planned, nil
}

// dependency plans one dependency; a nil entry means nothing to install (seen, installed,
// external or incompatible). planned is the size of the plan so far.
func (p *planner) dependency(ctx context.Context, res *resolution, d upstream.Dependency, src upstream.Source, parent string, depth, planned int) (*planEntry, []upstream.Dependency, error) {
	name := cmp.Or(d.Name, d.ProjectID, d.VersionID)
	incompatible := func(title, reason string) (*planEntry, []upstream.Dependency, error) {
		res.Incompatible = append(res.Incompatible, incompatibleEntry{Title: title, Reason: reason})
		return nil, nil, nil
	}
	if d.ProjectID == "" && d.VersionID == "" {
		res.External = append(res.External, externalEntry{Name: cmp.Or(d.Name, "unknown"), URL: safeLink(d.ExternalURL)})
		return nil, nil, nil
	}
	var fixed *upstream.Version
	if d.VersionID != "" {
		v, err := p.up.Version(ctx, src, d.ProjectID, d.VersionID)
		switch {
		case errors.Is(err, upstream.ErrNotFound):
			return incompatible(name, reasonNoVersion)
		case err != nil:
			return nil, nil, err
		}
		fixed = &v
		d.ProjectID = cmp.Or(d.ProjectID, v.ProjectID)
	}
	key := projectKey(src, d.ProjectID)
	if p.seen[key] {
		return nil, nil, nil // a cycle or a dependency shared by two projects
	}
	p.seen[key] = true
	if j, ok := p.installed[key]; ok {
		title := j.File
		if j.Project != nil {
			title = cmp.Or(j.Project.Title, j.File)
		}
		res.Already = append(res.Already, alreadyEntry{Source: src, ProjectID: d.ProjectID, Title: title, File: j.File})
		return nil, nil, nil
	}
	proj, err := p.up.Project(ctx, src, d.ProjectID)
	switch {
	case errors.Is(err, upstream.ErrNotFound):
		return incompatible(name, reasonNoVersion)
	case err != nil:
		return nil, nil, err
	}
	switch {
	case depth > maxDepth:
		return incompatible(proj.Title, reasonTooDeep)
	case planned >= maxPackages:
		return incompatible(proj.Title, reasonTooMany)
	case p.kind.isMod() && proj.ClientOnly():
		return incompatible(proj.Title, reasonClientOnly)
	}
	var v upstream.Version
	if fixed != nil {
		v = *fixed
		switch {
		case v.ProjectID != proj.ID || !compatible(v, p.kind, p.gv):
			return incompatible(proj.Title, reasonNoVersion)
		case !v.Downloadable():
			return incompatible(proj.Title, reasonNotDownloadable)
		}
	} else {
		var reason string
		v, reason, err = p.pick(ctx, src, proj.ID)
		if err != nil {
			return nil, nil, err
		}
		if reason != "" {
			return incompatible(proj.Title, reason)
		}
	}
	requiredBy := parent
	e := p.entry(proj, v, &requiredBy)
	return &e, v.Dependencies, nil
}
```

`internal/plugins/search.go`:

```go
package plugins

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

const (
	maxQueryLen = 100  // characters of q
	maxOffset   = 1000 // search offset
)

// refRe bounds project and version parameters (spec §6.6).
var refRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)

func validRef(s string) bool { return refRe.MatchString(s) }

// sourceParam validates source for the server's kind (hangar only for plugins).
func sourceParam(w http.ResponseWriter, r *http.Request, srv server, raw string) (upstream.Source, bool) {
	src := upstream.Source(raw)
	if !src.Valid() || !srv.Kind.hasSource(src) {
		names := make([]string, 0, len(srv.Kind.Sources))
		for _, s := range srv.Kind.Sources {
			names = append(names, string(s))
		}
		writeValidation(w, r, "source must be one of: "+strings.Join(names, ", "))
		return "", false
	}
	return src, true
}

// refParam validates a required (or, with optional, possibly empty) project/version parameter.
func refParam(w http.ResponseWriter, r *http.Request, name, v string, optional bool) bool {
	if (optional && v == "") || validRef(v) {
		return true
	}
	writeValidation(w, r, name+" must be 1-128 characters of A-Z, a-z, 0-9, . _ + -")
	return false
}

// searchHit is the OpenAPI schema PluginSearchHit.
type searchHit struct {
	Source      upstream.Source `json:"source"`
	ProjectID   string          `json:"project_id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Author      string          `json:"author"`
	Downloads   int64           `json:"downloads"`
	IconURL     *string         `json:"icon_url"`
	PageURL     string          `json:"page_url"`
	Installed   bool            `json:"installed"`
}

type searchResult struct {
	Hits   []searchHit `json:"hits"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok || !h.allow(w, r) {
		return
	}
	q := r.URL.Query()
	src, ok := sourceParam(w, r, srv, q.Get("source"))
	if !ok {
		return
	}
	text := q.Get("q")
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxQueryLen {
		writeValidation(w, r, "q must be at most 100 characters")
		return
	}
	offset := 0
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > maxOffset {
			writeValidation(w, r, "offset must be between 0 and 1000")
			return
		}
		offset = n
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.up.Search(ctx, src, upstream.SearchQuery{
		Query: text, Offset: offset, ProjectType: srv.Kind.ProjectType, Loaders: srv.Kind.Loaders,
		GameVersion: gv, ServerSideOnly: srv.Kind.isMod(),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	installed := map[string]installedJar{}
	if inv, err := h.inventory(ctx, srv); err == nil {
		installed = installedByProject(inv)
	} else {
		h.log.DebugContext(ctx, "plugins: search without inventory", "server_id", srv.ID, "err", err)
	}
	out := searchResult{Hits: make([]searchHit, 0, len(res.Hits)), Total: res.Total, Offset: offset}
	for _, hit := range res.Hits {
		_, inst := installed[projectKey(src, hit.ProjectID)]
		out.Hits = append(out.Hits, searchHit{
			Source: src, ProjectID: hit.ProjectID, Slug: hit.Slug, Title: hit.Title, Description: hit.Description,
			Author: hit.Author, Downloads: hit.Downloads, IconURL: h.iconURL(srv.ID, hit.IconURL),
			PageURL: pageURL(src, srv.Kind.Kind, hit.Owner, hit.Slug), Installed: inst,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// versionJSON is the OpenAPI schema PluginVersion.
type versionJSON struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Channel      upstream.Channel `json:"channel"`
	PublishedAt  int64            `json:"published_at"`
	Downloadable bool             `json:"downloadable"`
	ExternalURL  *string          `json:"external_url"`
}

type versionList struct {
	Versions []versionJSON `json:"versions"`
}

func (h *Handler) handleVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok || !h.allow(w, r) {
		return
	}
	q := r.URL.Query()
	src, ok := sourceParam(w, r, srv, q.Get("source"))
	if !ok || !refParam(w, r, "project", q.Get("project"), false) {
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	vs, err := h.up.Versions(ctx, src, q.Get("project"), upstream.VersionFilter{Loaders: srv.Kind.Loaders, GameVersion: gv})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := versionList{Versions: make([]versionJSON, 0, len(vs))}
	for _, v := range vs {
		if !compatible(v, srv.Kind, gv) {
			continue
		}
		vj := versionJSON{ID: v.ID, Name: v.Name, Channel: v.Channel, PublishedAt: v.PublishedAt.UnixMilli(), Downloadable: v.Downloadable()}
		if link := safeLink(v.ExternalURL); !v.Downloadable() && link != "" {
			vj.ExternalURL = &link
		}
		out.Versions = append(out.Versions, vj)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleResolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok || !h.allow(w, r) {
		return
	}
	q := r.URL.Query()
	src, ok := sourceParam(w, r, srv, q.Get("source"))
	if !ok || !refParam(w, r, "project", q.Get("project"), false) || !refParam(w, r, "version", q.Get("version"), true) {
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.resolve(ctx, srv, gv, inv, resolveRequest{Source: src, Project: q.Get("project"), Version: q.Get("version")})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
```

- [ ] **Step 4: Checks und CI**

Run: `make check`
Expected: PASS.

```bash
git add internal/plugins
git commit -m "feat(plugins): resolve dependencies, search, versions and lookup rate limit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI PASS für `TestResolve*`, `TestSearch*`, `TestVersionsList`, `TestRateLimit` (und alle bisherigen).

---

### Task 11: Jobs, Installieren, Updatecheck und Aktualisieren

Die Job-Registry (einer pro Server, vier gesamt, Abbruch durch `Run`), der Install-Job (Plan neu berechnen, Download → `UploadChecked` → DB, Abbruch beim ersten Fehler), der Updatecheck mit Cache pro Server und der Update-Job (Fehler pro Schritt, neue Pflicht-Abhängigkeiten, deaktiviert bleibt deaktiviert).

**Files:**
- Create: `internal/plugins/jobs.go`, `internal/plugins/install.go`, `internal/plugins/updates.go`
- Modify: `internal/plugins/handler.go` (Felder `jobs`, `updates`, `Run`), `internal/plugins/stubs.go` (Einträge `// task 11` weg), `internal/plugins/export_test.go` (`WaitJobs`)
- Test: `internal/plugins/install_test.go`, `internal/plugins/updates_test.go`

**Interfaces:**
- Consumes: Aufgaben 9–10 (`resolve`, `newPlanner`, `planner.dependencies`, `planner.entry`, `planEntry`, `inventory`, `installedByProject`, `baseName`, `refParam`, `allow`, `markChanged`, `record`, `writeError`, Fehler-Sentinels); `upstream.Client.Download`, `LatestByHash`, `Versions`, `Version`, `Project`, `upstream.NewCache`; `filesapi.Client.Jars`, `UploadChecked`, `Delete`, `Mkdir`, `filesapi.Checksum`, `AlgoSHA256`, `AlgoSHA512`, `ErrHashMismatch`, `IsJarName`, `CleanPath`, `IsHidden`, `Join`, `MaxJars`; `store.PluginsUpsertInstallParams`, `PluginsDeleteInstallParams`; `audit.ActionPluginInstall`, `ActionPluginUpdate`.
- Produces:

```go
func newJobRegistry(clk clock.Clock, maxJobs int) *jobRegistry
func (r *jobRegistry) start(serverID int64, kind string, base context.Context, steps []jobStep, run func(ctx context.Context, j *job)) (jobView, error)
func (r *jobRegistry) get(serverID int64) *jobView
func (r *jobRegistry) running(serverID int64) bool
func (r *jobRegistry) shutdown()
func (h *Handler) runJob(ctx context.Context, j *job, srv server, tasks []jobTask, stopOnError bool)
func (h *Handler) installTask(ctx context.Context, srv server, t jobTask) error
func stepError(err error) (code, msg string)
func cleanJarFile(name string) (base string, enabled, ok bool)
func (h *Handler) checkUpdates(ctx context.Context, srv server, gv string, refresh bool) (updatesResult, error)
func WaitJobs(h *Handler) // export_test.go
```

- [ ] **Step 1: Test-Hook ergänzen**

`internal/plugins/export_test.go` (vollständig):

```go
package plugins

import "time"

// MarkChanged records a plugin change of serverID at t (restart_needed tests).
func MarkChanged(h *Handler, serverID int64, t time.Time) { h.restart.mark(serverID, t) }

// WaitJobs waits until every started job has finished (only between sequential test steps).
func WaitJobs(h *Handler) { h.jobs.wg.Wait() }
```

- [ ] **Step 2: Tests schreiben**

`internal/plugins/install_test.go`:

```go
package plugins_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/store"
)

type stepJSON struct {
	Title     string  `json:"title"`
	File      string  `json:"file"`
	State     string  `json:"state"`
	ErrorCode *string `json:"error_code"`
	Error     string  `json:"error"`
}

type jobJSON struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	Steps      []stepJSON `json:"steps"`
	StartedAt  int64      `json:"started_at"`
	FinishedAt int64      `json:"finished_at"`
}

func (e *env) post(id int64, suffix string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	a := e.admin()
	return e.do(http.MethodPost, srvPath(id, suffix), body, &a)
}

// install starts an install job and fails unless it was accepted.
func (e *env) install(id int64, src, project string) jobJSON {
	e.t.Helper()
	rec := e.post(id, "/install", map[string]any{"source": src, "project": project})
	if rec.Code != http.StatusAccepted {
		e.t.Fatalf("install %s: %d %s", project, rec.Code, rec.Body)
	}
	return decode[jobJSON](e.t, rec)
}

func (e *env) job(id int64) *jobJSON {
	e.t.Helper()
	return decode[struct {
		Job *jobJSON `json:"job"`
	}](e.t, e.get(srvPath(id, "/jobs"))).Job
}

// waitJob waits for all jobs and returns the server's last one.
func (e *env) waitJob(id int64) jobJSON {
	e.t.Helper()
	plugins.WaitJobs(e.h)
	j := e.job(id)
	if j == nil {
		e.t.Fatal("no job")
	}
	return *j
}

// steps renders the steps as "file:state[:code]".
func steps(j jobJSON) []string {
	out := make([]string, 0, len(j.Steps))
	for _, s := range j.Steps {
		v := s.File + ":" + s.State
		if s.ErrorCode != nil {
			v += ":" + *s.ErrorCode
		}
		out = append(out, v)
	}
	return out
}

func TestInstallWithDependency(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Hour).UnixMilli()})
	started := e.install(id, "modrinth", "chunky")
	if started.Kind != "install" || started.State != "running" || started.StartedAt != t0.UnixMilli() || len(started.Steps) != 2 ||
		started.Steps[0].State != "pending" || started.Steps[0].Title != "Fabric API" {
		t.Fatalf("started = %+v", started)
	}
	j := e.waitJob(id)
	if j.ID != started.ID || j.State != "done" || j.FinishedAt == 0 ||
		!slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":done"}) {
		t.Fatalf("job = %+v", j)
	}
	if c, _ := e.files.get(id, "mods/"+fapi.Name); c != fapi.content {
		t.Fatalf("fabric api content = %q", c)
	}
	if c, _ := e.files.get(id, "mods/"+chunky.Name); c != chunky.content {
		t.Fatalf("chunky content = %q", c)
	}
	calls := e.files.takeCalls()
	if !slices.Contains(calls, fmt.Sprintf("mkdir %d mods", id)) ||
		!slices.Contains(calls, fmt.Sprintf("upload %d mods/%s overwrite=false", id, chunky.Name)) {
		t.Fatalf("calls = %v", calls)
	}
	rows := e.rows(id)
	if len(rows) != 2 || rows[0].Filename != chunky.Name || rows[0].Sha512 != chunky.SHA512 || rows[0].Source != "modrinth" ||
		rows[0].ProjectID != "fALzjamp" || rows[0].ProjectSlug != "chunky" || rows[0].VersionID != "1CpEkmcD" ||
		rows[0].VersionName != "1.4.55" || rows[0].Channel != "release" || rows[0].InstalledAt != t0.UnixMilli() {
		t.Fatalf("rows = %+v", rows)
	}
	entries, actors := e.rec.all()
	if !slices.Equal(e.rec.actions(), []string{"plugin.install:success", "plugin.install:success"}) || !slices.Equal(actors, []string{"root", "root"}) {
		t.Fatalf("audit = %v by %v", e.rec.actions(), actors)
	}
	d := entries[0].Details
	if entries[0].Target != fmt.Sprintf("server:%d", id) || d["source"] != "modrinth" || d["project"] != "P7dR8mSH" ||
		d["version"] != "6qAuTtLR" || d["file"] != fapi.Name {
		t.Fatalf("audit entry = %+v", entries[0])
	}
	inv := e.inventory(id)
	if !inv.RestartNeeded || !jarNamed(t, inv, chunky.Name).InstalledByPanel {
		t.Fatalf("inventory after install = %+v", inv)
	}
}

func TestInstallRejects(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	vanilla := createInstance(t, e.db, "plain", instancespec.TypeVanilla, "1.21.11")
	e.up.modrinth("BROKEN", "broken", "Broken", "optional",
		mv{ID: "b1", Loaders: []string{"fabric"}, File: e.up.jar("Broken.jar", "b"), Deps: []map[string]any{mdep("NOWHERE", "")}})
	for _, tc := range []struct {
		srv  int64
		body any
		code int
		ec   string
	}{
		{id, map[string]any{"source": "modrinth", "project": "chunky", "x": 1}, http.StatusBadRequest, httpx.CodeBadRequest},
		{id, map[string]any{"source": "hangar", "project": "chunky"}, http.StatusBadRequest, httpx.CodeValidation},
		{id, map[string]any{"source": "modrinth", "project": "../x"}, http.StatusBadRequest, httpx.CodeValidation},
		{id, map[string]any{"source": "modrinth", "project": "chunky", "version": "a b"}, http.StatusBadRequest, httpx.CodeValidation},
		{id, map[string]any{"source": "modrinth", "project": "BROKEN"}, http.StatusConflict, httpx.CodePluginIncompatible},
		{vanilla, map[string]any{"source": "modrinth", "project": "chunky"}, http.StatusConflict, httpx.CodePluginsUnsupported},
	} {
		if rec := e.post(tc.srv, "/install", tc.body); rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%v: %d %s, want %d %s", tc.body, rec.Code, rec.Body, tc.code, tc.ec)
		}
	}
	// the requested project is installed already
	e.install(id, "modrinth", "chunky")
	e.waitJob(id)
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky", "version": nil}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("already installed: %d %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.actions()); n != 2 {
		t.Fatalf("rejected installs were audited: %v", e.rec.actions())
	}
}

// Review Focus #2: a planned name that a manual jar (in any case, enabled or not) already uses
// is refused before anything is downloaded.
func TestInstallFileExists(t *testing.T) {
	for _, existing := range []string{"Chunky-Fabric-1.4.55.jar", "chunky-fabric-1.4.55.JAR", "Chunky-Fabric-1.4.55.jar.disabled"} {
		e := newEnv(t)
		id := e.fabric("modded")
		chunky, fapi := fabricWorld(e)
		e.files.put(id, "mods/"+fapi.Name, fapi.content) // dependency present
		e.files.put(id, "mods/"+existing, "someone else's jar")
		rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"})
		if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeFileExists {
			t.Errorf("%s: %d %s", existing, rec.Code, rec.Body)
		}
		if c, _ := e.files.get(id, "mods/"+existing); c != "someone else's jar" || e.up.count("CDN "+strings.TrimPrefix(chunky.URL, e.up.cdn.URL)) != 0 {
			t.Errorf("%s: the foreign jar was touched or the download started", existing)
		}
	}
}

// Review Focus #2/#3: a jar that appears under the planned name during the job ends the job
// with file_exists; the steps before stay installed.
func TestInstallFileAppearsDuringJob(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.files.setOnUpload(func(sid int64, p string) {
		if strings.HasSuffix(p, fapi.Name) {
			e.files.put(sid, "mods/"+chunky.Name, "uploaded in the files tab meanwhile")
		}
	})
	e.install(id, "modrinth", "chunky")
	j := e.waitJob(id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":failed:file_exists"}) {
		t.Fatalf("job = %+v", steps(j))
	}
	if c, _ := e.files.get(id, "mods/"+chunky.Name); c != "uploaded in the files tab meanwhile" {
		t.Fatalf("the foreign jar was overwritten: %q", c)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != fapi.Name {
		t.Fatalf("rows = %+v", rows)
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.install:failure"}) {
		t.Fatalf("audit = %v", got)
	}
}

// Review Focus #1: a corrupted or wrongly sized download leaves no file and no row.
func TestInstallDownloadFailed(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.up.serveInstead(fapi, strings.Repeat("x", len(fapi.content))) // same size, wrong bytes
	e.install(id, "modrinth", "chunky")
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{fapi.Name + ":failed:hash_mismatch", chunky.Name + ":skipped"}) {
		t.Fatalf("hash mismatch job = %v", steps(j))
	}
	if names := e.files.names(id, "mods"); len(names) != 0 || len(e.rows(id)) != 0 {
		t.Fatalf("files %v, rows %+v", names, e.rows(id))
	}
	e.up.serveInstead(fapi, fapi.content)
	e.up.serveInstead(chunky, "short")
	e.install(id, "modrinth", "chunky")
	j = e.waitJob(id)
	if !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":failed:download_failed"}) {
		t.Fatalf("size mismatch job = %v", steps(j))
	}
	if names := e.files.names(id, "mods"); !slices.Equal(names, []string{fapi.Name}) {
		t.Fatalf("files = %v", names)
	}
}

// Review Focus #1: a download URL on a foreign host is never fetched, and a hostile file name
// becomes the fallback name inside mods/.
func TestInstallForeignHost(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	e.up.modrinth("EVIL", "evilmod", "Evil", "optional",
		mv{ID: "e1", Loaders: []string{"fabric"}, File: &upFile{URL: "https://evil.example/x.jar", Name: "x.jar", Size: 3, SHA512: sha512Hex("abc")}})
	good := e.up.jar("evil.jar", "payload")
	good.Name = "../../../../etc/cron.d/evil.jar" // what the API claims; the CDN path stays harmless
	e.up.modrinth("PATH", "pathmod", "Path", "optional", mv{ID: "p1", Loaders: []string{"fabric"}, File: good})
	e.install(id, "modrinth", "EVIL")
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{"x.jar:failed:download_failed"}) {
		t.Fatalf("foreign host = %v", steps(j))
	}
	e.install(id, "modrinth", "PATH")
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{"pathmod-p1.jar:done"}) {
		t.Fatalf("hostile name = %v", steps(j))
	}
	if names := e.files.names(id, "mods"); !slices.Equal(names, []string{"pathmod-p1.jar"}) {
		t.Fatalf("files = %v", names)
	}
	for _, c := range e.files.takeCalls() {
		if strings.HasPrefix(c, "upload") && !strings.HasPrefix(c, fmt.Sprintf("upload %d mods/pathmod-p1.jar", id)) {
			t.Fatalf("unexpected upload %q", c)
		}
	}
}

// Review Focus #4: the CDN fails in the middle of a job: the step says source_unavailable, the
// installed dependency stays.
func TestInstallSourceUnavailableMidway(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	chunky, fapi := fabricWorld(e)
	e.files.setOnUpload(func(int64, string) { e.up.setCDNStatus(http.StatusServiceUnavailable) })
	e.install(id, "modrinth", "chunky")
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{fapi.Name + ":done", chunky.Name + ":failed:source_unavailable"}) || j.State != "failed" {
		t.Fatalf("job = %v", steps(j))
	}
	if j.Steps[1].Error == "" || strings.Contains(j.Steps[1].Error, "/") {
		t.Fatalf("step error = %q (text without paths)", j.Steps[1].Error)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != fapi.Name {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #3: two tabs start installs at the same time: exactly one job.
func TestOneJobPerServer(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	block := make(chan struct{})
	e.up.setBlock(block)
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range 2 {
		wg.Go(func() { codes[i] = e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"}).Code })
	}
	wg.Wait()
	slices.Sort(codes)
	if !slices.Equal(codes, []int{http.StatusAccepted, http.StatusConflict}) {
		t.Fatalf("codes = %v", codes)
	}
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("while running: %d %s", rec.Code, rec.Body)
	}
	if j := e.job(id); j == nil || j.State != "running" {
		t.Fatalf("running job = %+v", j)
	}
	close(block)
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
}

func TestJobLimit(t *testing.T) {
	e := newEnv(t)
	fabricWorld(e)
	block := make(chan struct{})
	e.up.setBlock(block)
	for i := range 4 {
		e.install(e.fabric(fmt.Sprintf("s%d", i)), "modrinth", "chunky")
	}
	rec := e.post(e.fabric("s4"), "/install", map[string]any{"source": "modrinth", "project": "chunky"})
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("fifth job: %d %s", rec.Code, rec.Body)
	}
	close(block)
	plugins.WaitJobs(e.h)
}

// Review Focus #3: stopping the panel cancels running jobs, Run waits for them, no file stays.
func TestRunCancelsJobs(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	e.up.setBlock(make(chan struct{})) // never released
	e.install(id, "modrinth", "chunky")
	e.stop()
	j := e.job(id)
	if j.State != "failed" || j.Steps[0].ErrorCode == nil || *j.Steps[0].ErrorCode != "files_unavailable" || j.Steps[1].State != "skipped" {
		t.Fatalf("cancelled job = %+v", steps(*j))
	}
	if names := e.files.names(id, "mods"); len(names) != 0 {
		t.Fatalf("files = %v", names)
	}
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "chunky"}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("install after shutdown: %d %s", rec.Code, rec.Body)
	}
}

// Review Focus #3: while a job runs the inventory deletes no rows.
func TestInventoryKeepsRowsWhileJobRuns(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	e.seedRow(id, "Gone.jar", "gone", "modrinth", "G", "gone", "Gone", "v1", "1", "release")
	block := make(chan struct{})
	e.up.setBlock(block)
	e.install(id, "modrinth", "chunky")
	e.inventory(id)
	if rows := e.rows(id); !slices.ContainsFunc(rows, func(r store.PluginInstall) bool { return r.Filename == "Gone.jar" }) {
		t.Fatalf("row deleted during a job: %+v", rows)
	}
	close(block)
	e.waitJob(id)
	e.inventory(id)
	if rows := e.rows(id); slices.ContainsFunc(rows, func(r store.PluginInstall) bool { return r.Filename == "Gone.jar" }) {
		t.Fatalf("orphan kept after the job: %+v", rows)
	}
}

// Decision 4: a version change replaces the jar; a disabled jar stays disabled, the same name
// is overwritten.
func TestInstallVersionChange(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	v1 := e.up.jar("Tool-1.jar", "tool 1")
	v2 := e.up.jar("Tool-2.jar", "tool 2")
	v3 := e.up.jar("Tool-2.jar", "tool 2 rebuilt")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t3", File: v3}, mv{ID: "t2", File: v2}, mv{ID: "t1", File: v1})
	e.files.put(id, "plugins/Tool-1.jar.disabled", v1.content)
	e.seedRow(id, "Tool-1.jar", v1.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "TOOL", "version": "t2"}); rec.Code != http.StatusAccepted {
		t.Fatalf("install t2: %d %s", rec.Code, rec.Body)
	}
	if j := e.waitJob(id); !slices.Equal(steps(j), []string{"Tool-2.jar.disabled:done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"Tool-2.jar.disabled"}) {
		t.Fatalf("files = %v", names)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != "Tool-2.jar" || rows[0].VersionID != "t2" {
		t.Fatalf("rows = %+v", rows)
	}
	e.files.takeCalls()
	if rec := e.post(id, "/install", map[string]any{"source": "modrinth", "project": "TOOL", "version": "t3"}); rec.Code != http.StatusAccepted {
		t.Fatalf("install t3: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(id)
	if c, _ := e.files.get(id, "plugins/Tool-2.jar.disabled"); c != "tool 2 rebuilt" {
		t.Fatalf("content = %q", c)
	}
	if !slices.Contains(e.files.takeCalls(), fmt.Sprintf("upload %d plugins/Tool-2.jar.disabled overwrite=true", id)) {
		t.Fatal("same name must be uploaded with overwrite")
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.install:success"}) {
		t.Fatalf("audit = %v", got)
	}
}
```

`internal/plugins/updates_test.go`:

```go
package plugins_test

import (
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

type updatesJSON struct {
	Updates []struct {
		File string         `json:"file"`
		From versionRefJSON `json:"from"`
		To   versionRefJSON `json:"to"`
	} `json:"updates"`
	CheckedAt int64 `json:"checked_at"`
}

func (e *env) updates(id int64, query string) updatesJSON {
	e.t.Helper()
	return decode[updatesJSON](e.t, e.get(srvPath(id, "/updates"+query)))
}

func (u updatesJSON) summary() []string {
	var out []string
	for _, x := range u.Updates {
		out = append(out, x.File+":"+x.From.ID+">"+x.To.ID+":"+x.To.Channel)
	}
	return out
}

// lpWorld registers LuckPerms with two releases and puts the old jar (uploaded by hand) on disk.
func lpWorld(e *env, id int64, file string) (oldJar, newJar *upFile) {
	newJar = e.up.jar("LuckPerms-Bukkit-5.5.71.jar", "luckperms 5.5.71")
	oldJar = e.up.jar("LuckPerms-Bukkit-5.5.53.jar", "luckperms 5.5.53")
	e.up.modrinth("Vebnzrzj", "luckperms", "LuckPerms", "required",
		mv{ID: "b0mk8uS6", Number: "v5.5.71-bukkit", File: newJar}, mv{ID: "MBSY8toc", Number: "v5.5.53-bukkit", File: oldJar})
	e.files.put(id, "plugins/"+file, oldJar.content)
	return oldJar, newJar
}

func TestUpdatesModrinth(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LuckPerms-old.jar")
	b1, b2 := e.up.jar("Beta-1.jar", "beta 1"), e.up.jar("Beta-2.jar", "beta 2")
	e.up.modrinth("BETA", "beta", "Beta", "required", mv{ID: "b2", Type: "beta", File: b2}, mv{ID: "b1", Type: "beta", File: b1})
	e.files.put(id, "plugins/Beta-1.jar", b1.content)
	e.seedRow(id, "Beta-1.jar", b1.content, "modrinth", "BETA", "beta", "Beta", "b1", "b1", "beta")
	a1, a2 := e.up.jar("Alpha-1.jar", "alpha 1"), e.up.jar("Alpha-2.jar", "alpha 2")
	e.up.modrinth("ALPHA", "alpha", "Alpha", "required", mv{ID: "a2", Type: "alpha", File: a2}, mv{ID: "a1", Type: "alpha", File: a1})
	e.files.put(id, "plugins/Alpha-1.jar", a1.content)
	e.files.put(id, "plugins/Manual.jar", "manual")
	got := e.updates(id, "")
	want := []string{"Alpha-1.jar:a1>a2:alpha", "Beta-1.jar:b1>b2:beta", "LuckPerms-old.jar:MBSY8toc>b0mk8uS6:release"}
	if !slices.Equal(got.summary(), want) || got.CheckedAt != t0.UnixMilli() {
		t.Fatalf("updates = %v (%d)", got.summary(), got.CheckedAt)
	}
	types := e.up.types()
	if !slices.ContainsFunc(types, func(ts []string) bool { return slices.Equal(ts, []string{"release"}) }) ||
		!slices.ContainsFunc(types, func(ts []string) bool { return slices.Equal(ts, []string{"release", "beta"}) }) ||
		!slices.ContainsFunc(types, func(ts []string) bool { return slices.Equal(ts, []string{"release", "beta", "alpha"}) }) {
		t.Fatalf("version_types = %v", types)
	}
}

func TestUpdatesHangar(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.hangar(31, "ViaVersion", "ViaVersion",
		hv{Name: "5.12.2-SNAPSHOT+657", Channel: "Snapshot", File: e.up.jar("VV-snap.jar", "vv snap")},
		hv{Name: "5.12.1", File: e.up.jar("ViaVersion-5.12.1.jar", "vv 5.12.1")},
		hv{Name: "5.12.0", File: e.up.jar("ViaVersion-5.12.0.jar", "vv 5.12.0")})
	e.files.put(id, "plugins/ViaVersion-5.12.0.jar", "vv 5.12.0")
	e.seedRow(id, "ViaVersion-5.12.0.jar", "vv 5.12.0", "hangar", "31", "ViaVersion", "ViaVersion", "5.12.0", "5.12.0", "release")
	e.up.hangar(40, "Dev", "Nightly",
		hv{Name: "2.1-SNAPSHOT", Channel: "Snapshot", File: e.up.jar("N-2.1.jar", "n 2.1")},
		hv{Name: "2.0-SNAPSHOT", Channel: "Snapshot", File: e.up.jar("N-2.0.jar", "n 2.0")})
	e.files.put(id, "plugins/N-2.0.jar", "n 2.0")
	e.seedRow(id, "N-2.0.jar", "n 2.0", "hangar", "40", "Nightly", "Nightly", "2.0-SNAPSHOT", "2.0-SNAPSHOT", "beta")
	// the installed version is newer than the newest release: no "update" to an older one
	e.up.hangar(50, "Dev", "Ahead",
		hv{Name: "3.1", Channel: "Snapshot", Published: t0},
		hv{Name: "3.0", Published: t0.Add(-48 * time.Hour), File: e.up.jar("Ahead-3.0.jar", "a 3.0")})
	e.files.put(id, "plugins/Ahead.jar", "ahead 3.1")
	e.seedRow(id, "Ahead.jar", "ahead 3.1", "hangar", "50", "Ahead", "Ahead", "3.1", "3.1", "release")
	got := e.updates(id, "")
	want := []string{"N-2.0.jar:2.0-SNAPSHOT>2.1-SNAPSHOT:beta", "ViaVersion-5.12.0.jar:5.12.0>5.12.1:release"}
	if !slices.Equal(got.summary(), want) {
		t.Fatalf("updates = %v", got.summary())
	}
}

func TestUpdatesCacheAndRefresh(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LuckPerms-old.jar")
	key := "POST /v2/version_files/update"
	e.updates(id, "")
	e.updates(id, "")
	e.updates(id, "?refresh=yes")
	if n := e.up.count(key); n != 1 {
		t.Fatalf("checks = %d, want 1 (cached)", n)
	}
	e.updates(id, "?refresh=true")
	e.updates(id, "?refresh=1")
	if n := e.up.count(key); n != 3 {
		t.Fatalf("checks = %d after two refreshes", n)
	}
	e.clk.Advance(time.Hour)
	e.updates(id, "")
	if n := e.up.count(key); n != 4 {
		t.Fatalf("checks = %d after an hour", n)
	}
	// refresh counts against the lookup limit (the hour refilled the 20 tokens), the plain check does not
	a := e.admin()
	for range 20 {
		e.updates(id, "?refresh=1")
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/updates?refresh=1"), nil, &a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("refresh beyond the limit: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("plain check limited: %d", rec.Code)
	}
	e.up.setDown(true)
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusOK {
		t.Fatalf("a cached check needs no upstream: %d", rec.Code)
	}
	e.clk.Advance(time.Hour)
	if rec := e.do(http.MethodGet, srvPath(id, "/updates"), nil, &a); rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodePluginSourceUnavailable {
		t.Fatalf("upstream down: %d %s", rec.Code, rec.Body)
	}
}

func TestUpdateJob(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	_, newJar := lpWorld(e, id, "LuckPerms-old.jar")
	if got := e.updates(id, "").summary(); len(got) != 1 {
		t.Fatalf("updates = %v", got)
	}
	rec := e.post(id, "/update", map[string]any{"files": []string{}})
	if rec.Code != http.StatusAccepted || decode[jobJSON](t, rec).Kind != "update" {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	j := e.waitJob(id)
	if j.State != "done" || !slices.Equal(steps(j), []string{newJar.Name + ":done"}) || j.Steps[0].Title != "LuckPerms" {
		t.Fatalf("job = %+v", j)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{newJar.Name}) {
		t.Fatalf("files = %v", names)
	}
	rows := e.rows(id)
	if len(rows) != 1 || rows[0].Filename != newJar.Name || rows[0].VersionID != "b0mk8uS6" || rows[0].Sha512 != newJar.SHA512 {
		t.Fatalf("rows = %+v", rows)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginUpdate || entries[0].Result != audit.ResultSuccess || actors[0] != "root" ||
		entries[0].Details["file"] != newJar.Name || entries[0].Details["from"] != "v5.5.53-bukkit" || entries[0].Details["to"] != "v5.5.71-bukkit" {
		t.Fatalf("audit = %+v by %v", entries, actors)
	}
	// the job dropped the cached check
	if got := e.updates(id, "").summary(); len(got) != 0 {
		t.Fatalf("updates after the job = %v", got)
	}
}

func TestUpdateDisabledStaysDisabled(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	_, newJar := lpWorld(e, id, "LP.jar.disabled")
	e.updates(id, "")
	if rec := e.post(id, "/update", map[string]any{"files": []string{"LP.jar.disabled"}}); rec.Code != http.StatusAccepted {
		t.Fatalf("update: %d %s", rec.Code, rec.Body)
	}
	e.waitJob(id)
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{newJar.Name + ".disabled"}) {
		t.Fatalf("files = %v", names)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != newJar.Name {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestUpdateSameNameOverwrite(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	t1, t2 := e.up.jar("Tool.jar", "tool 1"), e.up.jar("Tool.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool.jar", t1.content)
	e.seedRow(id, "Tool.jar", t1.content, "modrinth", "TOOL", "tool", "Tool", "t1", "t1", "release")
	e.post(id, "/update", map[string]any{"files": []string{"Tool.jar"}})
	e.waitJob(id)
	if c, _ := e.files.get(id, "plugins/Tool.jar"); c != "tool 2" {
		t.Fatalf("content = %q", c)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].VersionID != "t2" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Review Focus #2: an update onto a name another jar already uses fails only that step.
func TestUpdateTargetNameTaken(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	a1, a2 := e.up.jar("A-1.jar", "a 1"), e.up.jar("Taken.jar", "a 2")
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a2", File: a2}, mv{ID: "a1", File: a1})
	e.files.put(id, "plugins/A-1.jar", a1.content)
	e.files.put(id, "plugins/taken.JAR", "manual jar")
	_, lpNew := lpWorld(e, id, "LP-old.jar")
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{"Taken.jar:failed:file_exists", lpNew.Name + ":done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"A-1.jar", lpNew.Name, "taken.JAR"}) {
		t.Fatalf("files = %v", names)
	}
}

func TestUpdateNewDependency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lib := e.up.jar("Lib.jar", "lib")
	e.up.modrinth("LIB", "lib", "Lib", "required", mv{ID: "l1", File: lib})
	t1, t2 := e.up.jar("Tool-1.jar", "tool 1"), e.up.jar("Tool-2.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2, Deps: []map[string]any{mdep("LIB", "")}}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool-1.jar", t1.content)
	e.post(id, "/update", map[string]any{"files": []string{"Tool-1.jar"}})
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{"Lib.jar:done", "Tool-2.jar:done"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.install:success", "plugin.update:success"}) {
		t.Fatalf("audit = %v", got)
	}
}

func TestUpdateDependencyIncompatible(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	t1, t2 := e.up.jar("Tool-1.jar", "tool 1"), e.up.jar("Tool-2.jar", "tool 2")
	e.up.modrinth("TOOL", "tool", "Tool", "required", mv{ID: "t2", File: t2, Deps: []map[string]any{mdep("NOWHERE", "")}}, mv{ID: "t1", File: t1})
	e.files.put(id, "plugins/Tool-1.jar", t1.content)
	_, lpNew := lpWorld(e, id, "LP-old.jar")
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	if !slices.Equal(steps(j), []string{lpNew.Name + ":done", "Tool-2.jar:failed:download_failed"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if c, ok := e.files.get(id, "plugins/Tool-1.jar"); !ok || c != t1.content {
		t.Fatal("the old jar must stay when its update fails")
	}
}

// Review Focus #4: the CDN fails after the first update; the next update fails, the first stays.
func TestUpdateSourceUnavailable(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	a1, a2 := e.up.jar("A-1.jar", "a 1"), e.up.jar("A-2.jar", "a 2")
	e.up.modrinth("A", "a", "A", "required", mv{ID: "a2", File: a2}, mv{ID: "a1", File: a1})
	e.files.put(id, "plugins/A-1.jar", a1.content)
	_, lpNew := lpWorld(e, id, "LP-old.jar")
	e.files.setOnUpload(func(int64, string) { e.up.setCDNStatus(http.StatusServiceUnavailable) })
	e.post(id, "/update", map[string]any{"files": []string{}})
	j := e.waitJob(id)
	if j.State != "failed" || !slices.Equal(steps(j), []string{"A-2.jar:done", lpNew.Name + ":failed:source_unavailable"}) {
		t.Fatalf("job = %v", steps(j))
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"A-2.jar", "LP-old.jar"}) {
		t.Fatalf("files = %v", names)
	}
}

func TestUpdateValidation(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for _, files := range [][]string{{"../x.jar"}, {"a/b.jar"}, {"x.zip"}, {".mcpanel-x.jar"}, {"unknown.jar"}, {}} {
		if rec := e.post(id, "/update", map[string]any{"files": files}); rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%v: %d %s", files, rec.Code, rec.Body)
		}
	}
	// a fresh server (the empty check above is cached for an hour)
	busy := e.paper("busy")
	lpWorld(e, busy, "LP-old.jar")
	block := make(chan struct{})
	e.up.setBlock(block)
	if rec := e.post(busy, "/update", map[string]any{"files": []string{}}); rec.Code != http.StatusAccepted {
		t.Fatalf("first update: %d %s", rec.Code, rec.Body)
	}
	if rec := e.post(busy, "/update", map[string]any{"files": []string{}}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Fatalf("second update: %d %s", rec.Code, rec.Body)
	}
	close(block)
	e.waitJob(busy)
}
```

- [ ] **Step 3: Rot prüfen**

Run: `git add -N internal/plugins && make check`
Expected: FAIL beim Kompilieren (`undefined: h.jobs` in `export_test.go`).

- [ ] **Step 4: Implementierung schreiben**

In `internal/plugins/handler.go` die Typdefinition `Handler`, den `return` von `NewHandler` und `Run` ersetzen:

```go
type Handler struct {
	q       *store.Queries
	clock   clock.Clock
	rec     audit.Recorder
	files   filesapi.Client
	inst    agentapi.InstancesClient
	catalog Catalog
	up      *upstream.Client
	log     *slog.Logger
	restart *restartTracker
	limit   *userLimiter
	jobs    *jobRegistry
	updates *upstream.Cache[updatesResult]
}
```

```go
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: fc, inst: inst, catalog: cat, up: up, log: log,
		restart: newRestartTracker(), limit: newUserLimiter(), jobs: newJobRegistry(d.Clock, maxJobs),
		updates: upstream.NewCache[updatesResult](d.Clock, updatesTTL, upstream.MaxCacheEntries),
	}
```

```go
// Run blocks until ctx is done; then it cancels running jobs and waits for them.
func (h *Handler) Run(ctx context.Context) {
	<-ctx.Done()
	h.jobs.shutdown()
}
```

In `internal/plugins/stubs.go` den Block `// task 11` (`handleInstall`, `handleUpdates`, `handleUpdate`, `handleJobs`, `jobRunning`) löschen.

`internal/plugins/jobs.go`:

```go
package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// maxJobs bounds the plugin jobs running in the panel (spec §6.4).
const maxJobs = 4

type stepState string

const (
	stepPending     stepState = "pending"
	stepDownloading stepState = "downloading"
	stepDone        stepState = "done"
	stepFailed      stepState = "failed"
	stepSkipped     stepState = "skipped"
)

const (
	jobInstall = "install"
	jobUpdate  = "update"

	jobStateRunning = "running"
	jobStateDone    = "done"
	jobStateFailed  = "failed"

	codeDownloadFailed      = "download_failed"
	codeHashMismatch        = "hash_mismatch"
	codeFileExists          = "file_exists"
	codeInsufficientStorage = "insufficient_storage"
	codeFilesUnavailable    = "files_unavailable"
	codeSourceUnavailable   = "source_unavailable"
)

// jobStep is the OpenAPI schema PluginJobStep.
type jobStep struct {
	Title     string    `json:"title"`
	File      string    `json:"file"`
	State     stepState `json:"state"`
	ErrorCode *string   `json:"error_code"`
	Error     string    `json:"error"`
}

// jobView is the OpenAPI schema PluginJob.
type jobView struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	State      string    `json:"state"`
	Steps      []jobStep `json:"steps"`
	StartedAt  int64     `json:"started_at"`
	FinishedAt int64     `json:"finished_at"` // 0 while running
}

type job struct {
	reg    *jobRegistry
	view   jobView // guarded by reg.mu
	cancel context.CancelFunc
}

// jobRegistry keeps the newest job per server in memory: at most one running per server and
// maxJobs in the panel; the last result stays until the next job or a panel restart.
type jobRegistry struct {
	clock  clock.Clock
	max    int
	mu     sync.Mutex
	jobs   map[int64]*job
	active int
	closed bool
	wg     sync.WaitGroup
}

func newJobRegistry(clk clock.Clock, maxJobs int) *jobRegistry {
	return &jobRegistry{clock: clk, max: maxJobs, jobs: map[int64]*job{}}
}

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (v jobView) clone() jobView {
	v.Steps = slices.Clone(v.Steps)
	return v
}

// start runs run in the background as the server's job. base only passes its values (actor,
// IP) to the job; the job ends with run or when the registry shuts down.
func (r *jobRegistry) start(serverID int64, kind string, base context.Context, steps []jobStep, run func(ctx context.Context, j *job)) (jobView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		return jobView{}, errShuttingDown
	case r.jobs[serverID] != nil && r.jobs[serverID].view.State == jobStateRunning:
		return jobView{}, errJobRunning
	case r.active >= r.max:
		return jobView{}, errTooManyJobs
	}
	if steps == nil {
		steps = []jobStep{}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(base))
	j := &job{reg: r, cancel: cancel, view: jobView{
		ID: newJobID(), Kind: kind, State: jobStateRunning, Steps: steps, StartedAt: r.clock.Now().UnixMilli(),
	}}
	r.jobs[serverID] = j
	r.active++
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.finish(j)
		defer cancel()
		run(ctx, j)
	}()
	return j.view.clone(), nil
}

// finish ends a job: failed if a step failed, steps it never reached count as skipped.
func (r *jobRegistry) finish(j *job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j.view.State = jobStateDone
	for i := range j.view.Steps {
		switch j.view.Steps[i].State {
		case stepFailed:
			j.view.State = jobStateFailed
		case stepPending, stepDownloading:
			j.view.Steps[i].State = stepSkipped
		}
	}
	j.view.FinishedAt = r.clock.Now().UnixMilli()
	r.active--
}

func (r *jobRegistry) get(serverID int64) *jobView {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[serverID]
	if !ok {
		return nil
	}
	v := j.view.clone()
	return &v
}

func (r *jobRegistry) running(serverID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[serverID]
	return ok && j.view.State == jobStateRunning
}

// shutdown refuses new jobs, cancels the running ones and waits for them.
func (r *jobRegistry) shutdown() {
	r.mu.Lock()
	r.closed = true
	for _, j := range r.jobs {
		if j.view.State == jobStateRunning {
			j.cancel()
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
}

func (j *job) set(i int, s stepState) {
	j.reg.mu.Lock()
	defer j.reg.mu.Unlock()
	j.view.Steps[i].State = s
}

func (j *job) fail(i int, code, msg string) {
	j.reg.mu.Lock()
	defer j.reg.mu.Unlock()
	st := &j.view.Steps[i]
	st.State, st.ErrorCode, st.Error = stepFailed, &code, msg
}

func (h *Handler) jobRunning(serverID int64) bool { return h.jobs.running(serverID) }

type jobResponse struct {
	Job *jobView `json:"job"`
}

func (h *Handler) handleJobs(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, jobResponse{Job: h.jobs.get(srv.ID)})
}
```

`internal/plugins/install.go`:

```go
package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/store"
)

type installRequest struct {
	Source  upstream.Source `json:"source"`
	Project string          `json:"project"`
	Version *string         `json:"version"`
}

// jobTask is one jar of a job; its step has the same index.
type jobTask struct {
	entry planEntry
	group int         // a failed task skips the remaining tasks of its group
	from  *versionRef // update: the replaced version (audit plugin.update); nil = install
	err   *taskError  // the task failed before the job started (update resolution)
}

type taskError struct{ code, msg string }

// targetName is the file a task writes: the planned name, with ".disabled" when it replaces a
// disabled jar.
func targetName(e planEntry) string {
	if e.replaces != "" && !e.replacesEnabled {
		return e.Filename + disabledSuffix
	}
	return e.Filename
}

func stepsOf(tasks []jobTask) []jobStep {
	out := make([]jobStep, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, jobStep{Title: t.entry.Title, File: targetName(t.entry), State: stepPending})
	}
	return out
}

// cleanJarFile validates a file parameter (spec §6.6): a plain jar name; it returns the name
// without ".disabled" and whether the jar is enabled.
func cleanJarFile(name string) (base string, enabled, ok bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false, false
	}
	c, err := filesapi.CleanPath(name)
	if err != nil || c != name || filesapi.IsHidden(c) {
		return "", false, false
	}
	enabled, ok = filesapi.IsJarName(name)
	if !ok {
		return "", false, false
	}
	return baseName(name, enabled), enabled, true
}

// baseOf is baseName for a name that may not be a jar name.
func baseOf(name string) string {
	enabled, ok := filesapi.IsJarName(name)
	if !ok {
		return name
	}
	return baseName(name, enabled)
}

// nameConflict reports a planned file name another jar already uses, also only differing in
// case or by ".disabled"; the jar an entry replaces does not count (Review Focus #2).
func nameConflict(inv inventory, plan []planEntry) error {
	for _, e := range plan {
		for _, j := range inv.Jars {
			if j.File != e.replaces && strings.EqualFold(j.base, e.Filename) {
				return fmt.Errorf("%w: %s", errFileExists, j.File)
			}
		}
	}
	return nil
}

func (h *Handler) handleInstall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req installRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if _, ok := sourceParam(w, r, srv, string(req.Source)); !ok {
		return
	}
	version := ""
	if req.Version != nil {
		version = *req.Version
	}
	if !refParam(w, r, "project", req.Project, false) || !refParam(w, r, "version", version, true) {
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// The plan is computed again: the preview the browser showed is not trusted.
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.resolve(ctx, srv, gv, inv, resolveRequest{Source: req.Source, Project: req.Project, Version: version})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(res.Incompatible) > 0 {
		titles := make([]string, 0, len(res.Incompatible))
		for _, x := range res.Incompatible {
			titles = append(titles, x.Title)
		}
		h.writeError(w, r, fmt.Errorf("%w: required dependencies without a compatible version: %s", errIncompatible, strings.Join(titles, ", ")))
		return
	}
	if len(res.Install) == 0 {
		h.writeError(w, r, errAlreadyInstalled)
		return
	}
	if err := nameConflict(inv, res.Install); err != nil {
		h.writeError(w, r, err)
		return
	}
	tasks := make([]jobTask, 0, len(res.Install))
	for _, e := range res.Install {
		tasks = append(tasks, jobTask{entry: e})
	}
	view, err := h.jobs.start(srv.ID, jobInstall, ctx, stepsOf(tasks), func(ctx context.Context, j *job) {
		h.runJob(ctx, j, srv, tasks, true)
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, view)
}

// runJob runs the tasks in order. A failure ends the job (stopOnError, install) or skips the
// rest of the task's group (update). The updates check of the server is dropped at the end.
func (h *Handler) runJob(ctx context.Context, j *job, srv server, tasks []jobTask, stopOnError bool) {
	defer h.updates.Delete(updatesKey(srv.ID))
	if err := h.files.Mkdir(ctx, srv.ID, srv.Kind.Dir); err != nil && !errors.Is(err, filesapi.ErrExists) {
		h.log.WarnContext(ctx, "plugins: create folder", "server_id", srv.ID, "dir", srv.Kind.Dir, "err", err)
	}
	failed := map[int]bool{}
	stop := false
	for i, t := range tasks {
		switch {
		case stop || failed[t.group]:
			j.set(i, stepSkipped)
			continue
		case t.err != nil:
			h.auditTask(ctx, srv.ID, t, targetName(t.entry), errors.New(t.err.msg))
			j.fail(i, t.err.code, t.err.msg)
		default:
			j.set(i, stepDownloading)
			err := h.installTask(ctx, srv, t)
			if err == nil {
				j.set(i, stepDone)
				continue
			}
			code, msg := stepError(err)
			if ctx.Err() != nil {
				code, msg = codeFilesUnavailable, "the job was aborted because the panel is shutting down"
			}
			h.log.WarnContext(ctx, "plugins: job step failed", "server_id", srv.ID, "file", t.entry.Filename, "code", code, "err", err)
			j.fail(i, code, msg)
		}
		failed[t.group] = true
		stop = stopOnError
	}
}

// installTask downloads one jar, hands it to the service with its checksum and records it.
func (h *Handler) installTask(ctx context.Context, srv server, t jobTask) (err error) {
	e := t.entry
	target := targetName(e)
	defer func() { h.auditTask(ctx, srv.ID, t, target, err) }()
	sum, err := checksumOf(e.file)
	if err != nil {
		return err
	}
	// The name must still be free unless it is the jar this task replaces (Review Focus #2/#3).
	list, err := h.files.Jars(ctx, srv.ID, srv.Kind.Dir)
	if err != nil {
		return err
	}
	for _, jar := range list.Jars {
		if jar.Name != e.replaces && strings.EqualFold(baseOf(jar.Name), e.Filename) {
			return fmt.Errorf("%w: %s", errFileExists, jar.Name)
		}
	}
	body, err := h.up.Download(ctx, e.file)
	if err != nil {
		return err
	}
	tr := &trackReader{r: body}
	resp, err := h.files.UploadChecked(ctx, srv.ID, filesapi.Join(srv.Kind.Dir, target), e.replaces == target, e.Size, sum, tr)
	_ = body.Close()
	if err != nil {
		if rerr := tr.failure(); rerr != nil {
			return rerr // the download broke, not the service
		}
		return err
	}
	if resp.SHA512 == "" {
		return fmt.Errorf("%w: the file service returned no checksum", filesapi.ErrUnavailable)
	}
	// The jar is on disk: record it even if the job is being cancelled right now.
	ctx = context.WithoutCancel(ctx)
	if e.replaces != "" && e.replaces != target {
		if err := h.files.Delete(ctx, srv.ID, filesapi.Join(srv.Kind.Dir, e.replaces)); err != nil && !errors.Is(err, filesapi.ErrNotFound) {
			return err
		}
	}
	if old := baseOf(e.replaces); e.replaces != "" && old != e.Filename {
		if err := h.q.PluginsDeleteInstall(ctx, store.PluginsDeleteInstallParams{ServerID: srv.ID, Filename: old}); err != nil {
			return err
		}
	}
	if err := h.q.PluginsUpsertInstall(ctx, store.PluginsUpsertInstallParams{
		ServerID: srv.ID, Filename: e.Filename, Sha512: resp.SHA512, Source: string(e.Source), ProjectID: e.ProjectID,
		ProjectSlug: e.Slug, Title: e.Title, VersionID: e.VersionID, VersionName: e.VersionName, Channel: string(e.Channel),
		InstalledAt: h.clock.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	h.markChanged(srv.ID)
	return nil
}

// auditTask writes plugin.install or plugin.update for one jar; the actor is whoever started
// the job (its context carries the request's principal).
func (h *Handler) auditTask(ctx context.Context, serverID int64, t jobTask, target string, err error) {
	e := t.entry
	if t.from != nil {
		h.record(ctx, audit.ActionPluginUpdate, serverID, result(err), map[string]any{"file": target, "from": t.from.Name, "to": e.VersionName})
		return
	}
	h.record(ctx, audit.ActionPluginInstall, serverID, result(err), map[string]any{
		"source": string(e.Source), "project": e.ProjectID, "version": e.VersionID, "file": target,
	})
}

// checksumOf is the digest the service checks before it stores the jar (Modrinth sha512,
// Hangar sha256).
func checksumOf(f upstream.File) (filesapi.Checksum, error) {
	sum := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: f.SHA512}
	if f.SHA512 == "" {
		sum = filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: f.SHA256}
	}
	if err := sum.Validate(); err != nil {
		return filesapi.Checksum{}, fmt.Errorf("%w: %w", errNoChecksum, err)
	}
	return sum, nil
}

// trackReader remembers the first read error of the download, so that a broken download is
// not mistaken for a failing file service.
type trackReader struct {
	r   io.Reader
	mu  sync.Mutex
	err error
}

func (t *trackReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.mu.Lock()
		if t.err == nil {
			t.err = err
		}
		t.mu.Unlock()
	}
	return n, err
}

func (t *trackReader) failure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// stepError maps a failed task to its step code (spec §6.4) and a text without host paths.
func stepError(err error) (code, msg string) {
	switch {
	case errors.Is(err, errFileExists), errors.Is(err, filesapi.ErrExists):
		return codeFileExists, "a file with this name already exists in the folder"
	case errors.Is(err, filesapi.ErrHashMismatch):
		return codeHashMismatch, "the downloaded file does not match the checksum of the source"
	case errors.Is(err, filesapi.ErrInsufficientStorage):
		return codeInsufficientStorage, "not enough free disk space"
	case errors.Is(err, upstream.ErrDownload), errors.Is(err, errNoChecksum), errors.Is(err, errNotDownloadable),
		errors.Is(err, errIncompatible), errors.Is(err, filesapi.ErrInvalid), errors.Is(err, filesapi.ErrTooLarge):
		return codeDownloadFailed, "the download failed or the file was refused"
	case errors.Is(err, upstream.ErrUnavailable), errors.Is(err, upstream.ErrNotFound):
		return codeSourceUnavailable, "Modrinth or Hangar is not reachable"
	}
	return codeFilesUnavailable, "the file service is not reachable"
}
```

`internal/plugins/updates.go`:

```go
package plugins

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// updatesTTL is how long an updates check of a server is served from memory (spec §6.5).
const updatesTTL = time.Hour

// updateEntry is the OpenAPI schema PluginUpdate plus the jar it replaces.
type updateEntry struct {
	File string     `json:"file"`
	From versionRef `json:"from"`
	To   versionRef `json:"to"`

	jar    installedJar
	source upstream.Source
}

// updatesResult is the OpenAPI schema PluginUpdates.
type updatesResult struct {
	Updates   []updateEntry `json:"updates"`
	CheckedAt int64         `json:"checked_at"`
}

func updatesKey(serverID int64) string { return strconv.FormatInt(serverID, 10) }

// channelsUpTo are the Modrinth version types an installed channel accepts: the same or more
// stable.
func channelsUpTo(c upstream.Channel) []upstream.Channel {
	switch c {
	case upstream.Release:
		return []upstream.Channel{upstream.Release}
	case upstream.Beta:
		return []upstream.Channel{upstream.Release, upstream.Beta}
	}
	return []upstream.Channel{upstream.Release, upstream.Beta, upstream.Alpha}
}

// checkUpdates returns the updates of the server, cached for an hour (refresh drops the cache).
func (h *Handler) checkUpdates(ctx context.Context, srv server, gv string, refresh bool) (updatesResult, error) {
	key := updatesKey(srv.ID)
	if refresh {
		h.updates.Delete(key)
	}
	return h.updates.Get(ctx, key, func(ctx context.Context) (updatesResult, error) { return h.findUpdates(ctx, srv, gv) })
}

func (h *Handler) findUpdates(ctx context.Context, srv server, gv string) (updatesResult, error) {
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		return updatesResult{}, err
	}
	if slices.Contains(inv.SourceErrors, upstream.Modrinth) {
		return updatesResult{}, fmt.Errorf("%w: modrinth hash lookup failed", upstream.ErrUnavailable)
	}
	out := updatesResult{Updates: []updateEntry{}, CheckedAt: h.clock.Now().UnixMilli()}
	byChannel := map[upstream.Channel][]installedJar{}
	var hangar []installedJar
	for _, j := range inv.Jars {
		if j.Version == nil || j.Project == nil || j.sha512 == "" {
			continue // manual jars get no updates
		}
		switch upstream.Source(j.Source) {
		case upstream.Modrinth:
			byChannel[j.Version.Channel] = append(byChannel[j.Version.Channel], j)
		case upstream.Hangar:
			hangar = append(hangar, j)
		}
	}
	for _, ch := range []upstream.Channel{upstream.Release, upstream.Beta, upstream.Alpha} {
		jars := byChannel[ch]
		if len(jars) == 0 {
			continue
		}
		hashes := make([]string, 0, len(jars))
		for _, j := range jars {
			hashes = append(hashes, j.sha512)
		}
		latest, err := h.up.LatestByHash(ctx, hashes, srv.Kind.Loaders, []string{gv}, channelsUpTo(ch))
		if err != nil {
			return updatesResult{}, err
		}
		for _, j := range jars {
			if v, ok := latest[j.sha512]; ok && v.ID != j.Version.ID && v.Downloadable() {
				out.Updates = append(out.Updates, updateEntry{
					File: j.File, From: *j.Version, To: versionRef{ID: v.ID, Name: v.Name, Channel: v.Channel},
					jar: j, source: upstream.Modrinth,
				})
			}
		}
	}
	for _, j := range hangar {
		u, ok, err := h.hangarUpdate(ctx, srv, gv, j)
		if err != nil {
			return updatesResult{}, err
		}
		if ok {
			out.Updates = append(out.Updates, u)
		}
	}
	slices.SortFunc(out.Updates, func(a, b updateEntry) int { return strings.Compare(a.File, b.File) })
	return out, nil
}

// hangarUpdate finds the newest downloadable version in the same or a more stable channel that
// was published after the installed one.
func (h *Handler) hangarUpdate(ctx context.Context, srv server, gv string, j installedJar) (updateEntry, bool, error) {
	release := j.Version.Channel == upstream.Release
	vs, err := h.up.Versions(ctx, upstream.Hangar, j.Project.ID, upstream.VersionFilter{GameVersion: gv, ReleaseOnly: release})
	if err != nil {
		return updateEntry{}, false, err
	}
	i := slices.IndexFunc(vs, func(v upstream.Version) bool {
		return v.Downloadable() && compatible(v, srv.Kind, gv) && (!release || v.Channel == upstream.Release)
	})
	if i < 0 || vs[i].ID == j.Version.ID {
		return updateEntry{}, false, nil
	}
	cand := vs[i]
	cur, err := h.up.Version(ctx, upstream.Hangar, j.Project.ID, j.Version.ID)
	switch {
	case errors.Is(err, upstream.ErrNotFound):
		// the installed version is gone upstream: any candidate is newer
	case err != nil:
		return updateEntry{}, false, err
	case !cand.PublishedAt.After(cur.PublishedAt):
		return updateEntry{}, false, nil
	}
	return updateEntry{
		File: j.File, From: *j.Version, To: versionRef{ID: cand.ID, Name: cand.Name, Channel: cand.Channel},
		jar: j, source: upstream.Hangar,
	}, true, nil
}

func parseRefresh(s string) bool { return s == "1" || strings.EqualFold(s, "true") }

func (h *Handler) handleUpdates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	refresh := parseRefresh(r.URL.Query().Get("refresh"))
	if refresh && !h.allow(w, r) {
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.checkUpdates(ctx, srv, gv, refresh)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type updateRequest struct {
	Files []string `json:"files"`
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req updateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Files) > filesapi.MaxJars {
		writeValidation(w, r, "too many files")
		return
	}
	for _, f := range req.Files {
		if _, _, ok := cleanJarFile(f); !ok {
			writeValidation(w, r, "invalid file name: "+f)
			return
		}
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	upd, err := h.checkUpdates(ctx, srv, gv, false)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	selected := upd.Updates
	if len(req.Files) > 0 {
		selected = make([]updateEntry, 0, len(req.Files))
		for _, f := range req.Files {
			i := slices.IndexFunc(upd.Updates, func(u updateEntry) bool { return u.File == f })
			if i < 0 {
				writeValidation(w, r, "no update available for "+f)
				return
			}
			selected = append(selected, upd.Updates[i])
		}
	}
	if len(selected) == 0 {
		writeValidation(w, r, "no updates available")
		return
	}
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	tasks := h.updateTasks(ctx, srv, gv, inv, selected)
	view, err := h.jobs.start(srv.ID, jobUpdate, ctx, stepsOf(tasks), func(ctx context.Context, j *job) {
		h.runJob(ctx, j, srv, tasks, false)
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, view)
}

// updateTasks plans every selected update with its new required dependencies (decision 6): one
// group per update, its dependencies first.
func (h *Handler) updateTasks(ctx context.Context, srv server, gv string, inv inventory, selected []updateEntry) []jobTask {
	current := map[string]installedJar{}
	for _, j := range inv.Jars {
		current[j.File] = j
	}
	p := newPlanner(h, srv, gv, installedByProject(inv), nil)
	var tasks []jobTask
	for g, u := range selected {
		from := u.From
		root, deps, terr := h.planUpdate(ctx, p, u)
		if j, ok := current[u.File]; !ok || j.sha512 != u.jar.sha512 {
			deps, terr = nil, &taskError{codeFilesUnavailable, "the jar changed since the update check, check for updates again"}
		}
		for _, d := range deps {
			tasks = append(tasks, jobTask{entry: d, group: g})
		}
		tasks = append(tasks, jobTask{entry: root, group: g, from: &from, err: terr})
	}
	return tasks
}

func (h *Handler) planUpdate(ctx context.Context, p *planner, u updateEntry) (planEntry, []planEntry, *taskError) {
	fallback := planEntry{
		Source: u.source, ProjectID: u.jar.Project.ID, Slug: u.jar.Project.Slug, Title: cmp.Or(u.jar.Project.Title, u.File),
		VersionID: u.To.ID, VersionName: u.To.Name, Channel: u.To.Channel, Filename: u.jar.base,
		replaces: u.File, replacesEnabled: u.jar.Enabled,
	}
	fail := func(err error) (planEntry, []planEntry, *taskError) {
		code, msg := stepError(err)
		return fallback, nil, &taskError{code, msg}
	}
	proj, err := p.up.Project(ctx, u.source, u.jar.Project.ID)
	if err != nil {
		return fail(err)
	}
	v, err := p.up.Version(ctx, u.source, proj.ID, u.To.ID)
	if err != nil {
		return fail(err)
	}
	if !v.Downloadable() {
		return fail(errNotDownloadable)
	}
	root := p.entry(proj, v, nil)
	root.replaces, root.replacesEnabled = u.File, u.jar.Enabled
	p.seen[projectKey(u.source, proj.ID)] = true
	res := newResolution()
	deps, err := p.dependencies(ctx, &res, v, u.source, proj.Title)
	if err != nil {
		return fail(err)
	}
	if len(res.Incompatible) > 0 {
		titles := make([]string, 0, len(res.Incompatible))
		for _, x := range res.Incompatible {
			titles = append(titles, x.Title)
		}
		return root, nil, &taskError{codeDownloadFailed, "a required dependency has no compatible version: " + strings.Join(titles, ", ")}
	}
	return root, deps, nil
}
```

- [ ] **Step 5: Checks und CI**

Run: `make check`
Expected: PASS.

```bash
git add internal/plugins
git commit -m "feat(plugins): install and update jobs with update check

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI PASS für `TestInstall*`, `TestOneJobPerServer`, `TestJobLimit`, `TestRunCancelsJobs`, `TestInventoryKeepsRowsWhileJobRuns`, `TestUpdates*`, `TestUpdate*` (auch mit `-race` im CI-Job `test-race`).

---

### Task 12: Schalter, Löschen, Icon-Proxy, Verdrahtung und Ende-zu-Ende-Test

Die letzten drei Routen werden echt, `stubs.go` verschwindet. In `cmd/panel` bekommt die Testumgebung einen Fake-Upstream; ein Ende-zu-Ende-Test fährt Suche → Auflösen → Installieren (mit Abhängigkeit und CSRF) → Update → Schalter → Löschen → Icon → Rechte → Audit durch die echte Middleware-Kette, und ein Vertragstest prüft, dass jede Fehlerantwort der Plugin-Routen in `api/openapi.yaml` deklariert ist.

**Files:**
- Create: `internal/plugins/manage.go`, `internal/plugins/icon.go`
- Modify: `internal/plugins/handler.go` (Feld `icons`), `internal/plugins/export_test.go` (`SetIconCacheBytes`)
- Delete: `internal/plugins/stubs.go`
- Modify (`cmd/panel`): `integration_test.go` (Fake-Upstream in `newEnv`), `fakefiles_test.go` (echtes `Rename`/`Delete`), `openapi_test.go` (`TestPluginsErrorsMatchOpenAPI`); `app.go` nur falls Step 0 eine Lücke zeigt
- Create (`cmd/panel`): `fakeupstream_test.go`, `plugins_test.go`
- Test: `internal/plugins/manage_test.go`, `internal/plugins/icon_test.go`

**Interfaces:**
- Consumes: Aufgaben 9–11 (`loadServer`, `cleanJarFile`, `baseName`, `jobs.running`, `markChanged`, `updates`, `record`, `writeError`, `errFileExists`, `errJobRunning`, `disabledSuffix`, `updatesKey`, `ErrorStatus`); `upstream.Client.Icon`, `IconURLAllowed`, `upstream.Icon`; `filesapi.Client.Jars`, `Rename`, `Delete`, `filesapi.RenameRequest`; `audit.ActionPluginToggle`, `ActionPluginRemove`; Phase A in `cmd/panel`: `appDeps.Upstream`, `fakeFiles.Jars`/`UploadChecked`.
- Produces:

```go
func (h *Handler) handleToggle(w http.ResponseWriter, r *http.Request) // POST …/plugins/toggle → 204
func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) // POST …/plugins/delete → 204
func (h *Handler) handleIcon(w http.ResponseWriter, r *http.Request)   // GET  …/plugins/icon   → 200 image/*
func SetIconCacheBytes(h *Handler, n int64)                            // export_test.go
```

- [ ] **Step 0: Verdrahtung in `cmd/panel/app.go` prüfen**

```bash
grep -n "plugins\.\|upstream\.\|catalog.New\|wg.Add" cmd/panel/app.go
```

Erwartet (Phase A): `Upstream *upstream.Client` in `appDeps`, ein gemeinsames `cat := catalog.New(catalog.Options{Clock: d.Clock, Version: d.Version})`, `plugins.NewHandler(plugins.Deps{DB: db, Clock: d.Clock, Audit: auditRec, Files: d.Files, Instances: d.Instances, Catalog: cat, Upstream: d.Upstream, Version: d.Version})`, `pluginsH.Routes(api)` und `go func() { defer wg.Done(); pluginsH.Run(ctx) }()` bei `wg.Add(5)`. Fehlt `Upstream: d.Upstream` oder `pluginsH.Run`, genau diese Stelle in `app.go` ergänzen (gleicher Stil wie die übrigen Handler); sonst bleibt `app.go` unverändert.

- [ ] **Step 1: Tests im Paket `plugins` schreiben**

`internal/plugins/export_test.go` (vollständig):

```go
package plugins

import "time"

// MarkChanged records a plugin change of serverID at t (restart_needed tests).
func MarkChanged(h *Handler, serverID int64, t time.Time) { h.restart.mark(serverID, t) }

// WaitJobs waits until every started job has finished (only between sequential test steps).
func WaitJobs(h *Handler) { h.jobs.wg.Wait() }

// SetIconCacheBytes shrinks the icon cache (eviction test).
func SetIconCacheBytes(h *Handler, n int64) { h.icons.maxBytes = n }
```

`internal/plugins/manage_test.go`:

```go
package plugins_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

func (e *env) toggle(id int64, file string, enabled bool) (int, string) {
	e.t.Helper()
	rec := e.post(id, "/toggle", map[string]any{"file": file, "enabled": enabled})
	if rec.Code == http.StatusNoContent {
		return rec.Code, ""
	}
	return rec.Code, errCode(e.t, rec)
}

func TestToggle(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("TOOL", "tool", "Tool", "required")
	e.files.put(id, "plugins/Tool.jar", "tool")
	e.seedRow(id, "Tool.jar", "tool", "modrinth", "TOOL", "tool", "Tool", "t1", "1.0", "release")
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning, StartedAt: t0.Add(-time.Hour).UnixMilli()})
	if code, ec := e.toggle(id, "Tool.jar", false); code != http.StatusNoContent {
		t.Fatalf("disable: %d %s", code, ec)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"Tool.jar.disabled"}) {
		t.Fatalf("files = %v", names)
	}
	inv := e.inventory(id)
	if j := jarNamed(t, inv, "Tool.jar.disabled"); j.Enabled || !j.InstalledByPanel || !inv.RestartNeeded {
		t.Fatalf("inventory = %+v", inv)
	}
	if rows := e.rows(id); len(rows) != 1 || rows[0].Filename != "Tool.jar" {
		t.Fatalf("the row must survive switching: %+v", rows)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginToggle || entries[0].Result != audit.ResultSuccess ||
		entries[0].Details["file"] != "Tool.jar" || entries[0].Details["enabled"] != false || actors[0] != "root" {
		t.Fatalf("audit = %+v", entries)
	}
	if code, _ := e.toggle(id, "Tool.jar.disabled", true); code != http.StatusNoContent {
		t.Fatalf("enable: %d", code)
	}
	if names := e.files.names(id, "plugins"); !slices.Equal(names, []string{"Tool.jar"}) {
		t.Fatalf("files = %v", names)
	}
	// already in the wanted state: no rename, no audit entry
	e.files.takeCalls()
	if code, _ := e.toggle(id, "Tool.jar", true); code != http.StatusNoContent || len(e.files.takeCalls()) != 0 || len(e.rec.actions()) != 2 {
		t.Fatalf("no-op toggle: %d, audit %v", code, e.rec.actions())
	}
}

// Review Focus #2: switching onto a name that exists (in any case) is refused, nothing moves.
func TestToggleTargetExists(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for p, c := range map[string]string{
		"plugins/X.jar": "x", "plugins/X.jar.disabled": "old x",
		"plugins/Y.jar": "y", "plugins/y.JAR.disabled": "old y",
		"plugins/Z.jar.disabled": "z", "plugins/z.jar": "other z",
	} {
		e.files.put(id, p, c)
	}
	before := e.files.names(id, "plugins")
	for _, tc := range []struct {
		file    string
		enabled bool
	}{{"X.jar", false}, {"Y.jar", false}, {"Z.jar.disabled", true}} {
		if code, ec := e.toggle(id, tc.file, tc.enabled); code != http.StatusConflict || ec != httpx.CodeFileExists {
			t.Errorf("%s: %d %s", tc.file, code, ec)
		}
	}
	if after := e.files.names(id, "plugins"); !slices.Equal(after, before) {
		t.Fatalf("files changed: %v -> %v", before, after)
	}
	for _, c := range e.files.takeCalls() {
		if strings.HasPrefix(c, "rename") {
			t.Fatalf("rename reached the service: %s", c)
		}
	}
	if len(e.rec.actions()) != 0 {
		t.Fatalf("refused toggles were audited: %v", e.rec.actions())
	}
}

func TestToggleErrors(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.put(id, "plugins/A.jar", "a")
	for _, body := range []map[string]any{
		{"file": "../A.jar", "enabled": false}, {"file": "x/A.jar", "enabled": false}, {"file": "A.zip", "enabled": false},
		{"file": ".mcpanel-A.jar", "enabled": false}, {"file": "", "enabled": false}, {"file": "A.jar"},
	} {
		if rec := e.post(id, "/toggle", body); rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%v: %d %s", body, rec.Code, rec.Body)
		}
	}
	if code, ec := e.toggle(id, "Missing.jar", false); code != http.StatusNotFound || ec != httpx.CodeNotFound {
		t.Errorf("missing: %d %s", code, ec)
	}
	e.files.setErr("rename", fmt.Errorf("%w: down", filesapi.ErrUnavailable))
	if code, ec := e.toggle(id, "A.jar", false); code != http.StatusServiceUnavailable || ec != httpx.CodeFilesUnavailable {
		t.Errorf("service down: %d %s", code, ec)
	}
	if got := e.rec.actions(); !slices.Equal(got, []string{"plugin.toggle:failure"}) {
		t.Errorf("audit = %v", got)
	}
}

func TestManageDuringJob(t *testing.T) {
	e := newEnv(t)
	id := e.fabric("modded")
	fabricWorld(e)
	e.files.put(id, "mods/Other.jar", "other")
	block := make(chan struct{})
	e.up.setBlock(block)
	e.install(id, "modrinth", "chunky")
	if code, ec := e.toggle(id, "Other.jar", false); code != http.StatusConflict || ec != httpx.CodeConflict {
		t.Errorf("toggle during a job: %d %s", code, ec)
	}
	if rec := e.post(id, "/delete", map[string]any{"file": "Other.jar"}); rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeConflict {
		t.Errorf("delete during a job: %d %s", rec.Code, rec.Body)
	}
	close(block)
	e.waitJob(id)
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.files.put(id, "plugins/Tool.jar.disabled", "tool")
	e.seedRow(id, "Tool.jar", "tool", "modrinth", "TOOL", "tool", "Tool", "t1", "1.0", "release")
	if rec := e.post(id, "/delete", map[string]any{"file": "Tool.jar.disabled"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if names := e.files.names(id, "plugins"); len(names) != 0 || len(e.rows(id)) != 0 {
		t.Fatalf("files %v, rows %+v", names, e.rows(id))
	}
	entries, _ := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionPluginRemove || entries[0].Details["file"] != "Tool.jar.disabled" {
		t.Fatalf("audit = %+v", entries)
	}
	if rec := e.post(id, "/delete", map[string]any{"file": "Tool.jar.disabled"}); rec.Code != http.StatusNotFound {
		t.Fatalf("delete again: %d", rec.Code)
	}
	if rec := e.post(id, "/delete", map[string]any{"file": "../x.jar"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid name: %d", rec.Code)
	}
	if n := len(e.rec.actions()); n != 1 {
		t.Fatalf("refused deletes were audited: %v", e.rec.actions())
	}
}

// Review Focus #2: deleting x.jar keeps the row that belongs to x.jar.disabled.
func TestDeleteKeepsOtherRow(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	e.up.modrinth("X", "x", "X", "required")
	e.files.put(id, "plugins/X.jar", "manual copy")
	e.files.put(id, "plugins/X.jar.disabled", "panel copy")
	e.seedRow(id, "X.jar", "panel copy", "modrinth", "X", "x", "X", "x1", "1", "release")
	if rec := e.post(id, "/delete", map[string]any{"file": "X.jar"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rows := e.rows(id); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if j := jarNamed(t, e.inventory(id), "X.jar.disabled"); !j.InstalledByPanel {
		t.Fatalf("the remaining jar lost its origin: %+v", j)
	}
}

func TestManageDropsUpdatesCache(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	lpWorld(e, id, "LP-old.jar")
	e.files.put(id, "plugins/Other.jar", "other")
	key := "POST /v2/version_files/update"
	e.updates(id, "")
	if code, _ := e.toggle(id, "Other.jar", false); code != http.StatusNoContent {
		t.Fatalf("toggle: %d", code)
	}
	e.updates(id, "")
	if rec := e.post(id, "/delete", map[string]any{"file": "Other.jar.disabled"}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	e.updates(id, "")
	if n := e.up.count(key); n != 3 {
		t.Fatalf("checks = %d, want 3 (toggle and delete drop the cache)", n)
	}
}
```

`internal/plugins/icon_test.go`:

```go
package plugins_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
)

func (e *env) icon(id int64, raw string) *httptest.ResponseRecorder {
	e.t.Helper()
	a := e.admin()
	return e.do(http.MethodGet, srvPath(id, "/icon?url="+url.QueryEscape(raw)), nil, &a)
}

func TestIconProxy(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	vanilla := createInstance(t, e.db, "plain", instancespec.TypeVanilla, "1.21.11")
	for _, sid := range []int64{id, vanilla} { // the icon needs no plugin-capable type
		rec := e.icon(sid, e.up.iconURL("A"))
		h := rec.Header()
		if rec.Code != http.StatusOK || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") != "default-src 'none'" || h.Get("Cache-Control") != "private, max-age=86400" ||
			!bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
			t.Fatalf("icon: %d %v", rec.Code, h)
		}
	}
}

func TestIconRejects(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for _, raw := range []string{
		"https://evil.example/icon.png", "http://" + e.up.cdnHost() + "/icons/A.png",
		e.up.cdn.URL + "/icons/" + strings.Repeat("a", 512) + ".png", "",
	} {
		if rec := e.icon(id, raw); rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%.60s: %d %s", raw, rec.Code, rec.Body)
		}
	}
	for _, raw := range []string{e.up.cdn.URL + "/icons/evil.svg", e.up.cdn.URL + "/icons/big.png"} {
		if rec := e.icon(id, raw); rec.Code != http.StatusBadGateway || errCode(t, rec) != httpx.CodePluginSourceUnavailable {
			t.Errorf("%s: %d %s", raw, rec.Code, rec.Body)
		}
	}
	if rec := e.icon(999, e.up.iconURL("A")); rec.Code != http.StatusNotFound {
		t.Errorf("unknown server: %d", rec.Code)
	}
	if n := e.up.count("CDN /icons/" + strings.Repeat("a", 512) + ".png"); n != 0 {
		t.Fatal("a too long URL reached the CDN")
	}
}

func TestIconCache(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	for range 3 {
		e.icon(id, e.up.iconURL("A"))
	}
	if n := e.up.count("CDN /icons/A.png"); n != 1 {
		t.Fatalf("fetched %d times", n)
	}
	e.clk.Advance(24 * time.Hour)
	e.icon(id, e.up.iconURL("A"))
	if n := e.up.count("CDN /icons/A.png"); n != 2 {
		t.Fatalf("fetched %d times after 24 h", n)
	}
	// room for two icons: the least recently used one goes
	plugins.SetIconCacheBytes(e.h, int64(2*len(iconPNG)))
	e.icon(id, e.up.iconURL("B"))
	e.icon(id, e.up.iconURL("A"))
	e.icon(id, e.up.iconURL("C")) // evicts B
	e.icon(id, e.up.iconURL("A"))
	e.icon(id, e.up.iconURL("B"))
	if a, b := e.up.count("CDN /icons/A.png"), e.up.count("CDN /icons/B.png"); a != 2 || b != 2 {
		t.Fatalf("fetches A=%d B=%d", a, b)
	}
}

func TestIconConcurrency(t *testing.T) {
	e := newEnv(t)
	id := e.paper("survival")
	block := make(chan struct{})
	e.up.setBlock(block)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() { e.icon(id, e.up.iconURL(string(rune('a'+i)))) })
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.up.peak() < 8 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // a ninth fetch would show up now
	if p := e.up.peak(); p != 8 {
		t.Fatalf("parallel icon fetches = %d, want 8", p)
	}
	close(block)
	wg.Wait()
}
```

- [ ] **Step 2: Tests in `cmd/panel` schreiben**

`cmd/panel/fakefiles_test.go`: die drei No-op-Methoden `Mkdir`, `Rename`, `Delete` ersetzen (der Rest der Phase-A-Datei bleibt):

```go
// Mkdir succeeds: directories exist implicitly in the flat map.
func (f *fakeFiles) Mkdir(context.Context, int64, string) error { return nil }

func (f *fakeFiles) Rename(_ context.Context, req filesapi.RenameRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	from, to := key(req.ID, req.From), key(req.ID, req.To)
	b, ok := f.files[from]
	if !ok {
		return filesapi.ErrNotFound
	}
	if _, exists := f.files[to]; exists {
		return filesapi.ErrExists
	}
	delete(f.files, from)
	f.files[to] = b
	return nil
}

func (f *fakeFiles) Delete(_ context.Context, id int64, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[key(id, p)]; !ok {
		return filesapi.ErrNotFound
	}
	delete(f.files, key(id, p))
	return nil
}
```

`cmd/panel/fakeupstream_test.go`:

```go
package main

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// fakeUpstream is a small Modrinth (API and CDN, both TLS) for the end-to-end test; only the
// CDN host is an allowed download host.
type fakeUpstream struct {
	api, cdn *httptest.Server
	mu       sync.Mutex
	projects map[string]map[string]any   // by id and slug
	versions map[string][]map[string]any // by project id, newest first
	files    map[string][]byte           // CDN path -> content
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	f := &fakeUpstream{projects: map[string]map[string]any{}, versions: map[string][]map[string]any{}, files: map[string][]byte{}}
	f.api = httptest.NewTLSServer(http.HandlerFunc(f.serveAPI))
	f.cdn = httptest.NewTLSServer(http.HandlerFunc(f.serveCDN))
	t.Cleanup(f.api.Close)
	t.Cleanup(f.cdn.Close)
	return f
}

func (f *fakeUpstream) client(clk clock.Clock) *upstream.Client {
	host := f.cdn.Listener.Addr().String()
	return upstream.New(upstream.Options{
		Clock: clk, Version: "test", HTTPClient: f.api.Client(),
		Bases:     upstream.Bases{Modrinth: f.api.URL, Hangar: f.api.URL},
		AllowHost: func(u *url.URL) bool { return u.Scheme == "https" && u.Host == host && u.User == nil },
	})
}

// jar puts content on the CDN and returns the Modrinth file object.
func (f *fakeUpstream) jar(name, content string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := "/data/" + name
	f.files[p] = []byte(content)
	sum := sha512.Sum512([]byte(content))
	return map[string]any{
		"url": f.cdn.URL + p, "filename": name, "primary": true, "size": len(content),
		"hashes": map[string]string{"sha512": hex.EncodeToString(sum[:])},
	}
}

// mVersion is a Paper-compatible Modrinth release for 1.21.11; deps are required project ids.
func mVersion(id, number string, file map[string]any, published time.Time, deps ...string) map[string]any {
	ds := []map[string]any{}
	for _, d := range deps {
		ds = append(ds, map[string]any{"project_id": d, "version_id": nil, "file_name": nil, "dependency_type": "required"})
	}
	return map[string]any{
		"id": id, "version_number": number, "version_type": "release", "date_published": published.Format(time.RFC3339),
		"game_versions": []string{"1.21.11"}, "loaders": []string{"paper"}, "files": []any{file}, "dependencies": ds,
	}
}

func (f *fakeUpstream) project(id, slug, title string, versions ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := map[string]any{
		"id": id, "slug": slug, "title": title, "description": title + " description", "server_side": "required",
		"icon_url": f.cdn.URL + "/icons/" + id + ".png",
	}
	f.projects[id], f.projects[slug] = p, p
	for _, v := range versions {
		v["project_id"] = id
	}
	f.versions[id] = versions
}

func fileHash(v map[string]any) string {
	file := v["files"].([]any)[0].(map[string]any)
	return file["hashes"].(map[string]string)["sha512"]
}

func (f *fakeUpstream) serveAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	write := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	p := r.URL.Path
	switch {
	case p == "/v2/search":
		hits := []any{}
		for id, pr := range f.projects {
			if id == pr["id"] {
				hits = append(hits, map[string]any{
					"project_id": id, "slug": pr["slug"], "title": pr["title"], "description": pr["description"],
					"author": "someone", "downloads": 1, "icon_url": pr["icon_url"],
				})
			}
		}
		write(map[string]any{"hits": hits, "total_hits": len(hits)})
	case strings.HasPrefix(p, "/v2/project/") && strings.HasSuffix(p, "/version"):
		pr, ok := f.projects[strings.TrimSuffix(strings.TrimPrefix(p, "/v2/project/"), "/version")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(f.versions[pr["id"].(string)])
	case strings.HasPrefix(p, "/v2/project/"):
		pr, ok := f.projects[strings.TrimPrefix(p, "/v2/project/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(pr)
	case strings.HasPrefix(p, "/v2/version/"):
		for _, vs := range f.versions {
			for _, v := range vs {
				if v["id"] == strings.TrimPrefix(p, "/v2/version/") {
					write(v)
					return
				}
			}
		}
		http.NotFound(w, r)
	case p == "/v2/version_files" || p == "/v2/version_files/update":
		var in struct {
			Hashes []string `json:"hashes"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		for _, h := range in.Hashes {
			for _, vs := range f.versions {
				for _, v := range vs {
					if fileHash(v) != h {
						continue
					}
					out[h] = v
					if p == "/v2/version_files/update" {
						out[h] = vs[0] // newest of the project
					}
				}
			}
		}
		write(out)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeUpstream) serveCDN(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/icons/") {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 16)...))
		return
	}
	f.mu.Lock()
	b, ok := f.files[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}
```

`cmd/panel/integration_test.go`: `env` bekommt das Feld `up *fakeUpstream`; in `newEnv` direkt nach `ff := newFakeFiles()`:

```go
	up := newFakeUpstream(t)
```

im `appDeps`-Literal zusätzlich `Upstream: up.client(clk)` und im zurückgegebenen `env` `up: up`.

`cmd/panel/plugins_test.go`:

```go
package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
)

// raw sends a GET with the client's cookies and returns the response and body.
func (c *client) raw(path string) (*http.Response, []byte) {
	c.e.t.Helper()
	resp, err := c.hc.Get(c.e.srv.URL + "/api" + path)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// waitPluginJob polls the job of the server until it is no longer running.
func waitPluginJob(t *testing.T, c *client, id int64) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := c.must(200, "GET", fmt.Sprintf("/servers/%d/plugins/jobs", id), nil)
		if j, _ := out["job"].(map[string]any); j != nil && j["state"] != "running" {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin job never ended: %v", out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func jarFiles(inv map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, j := range inv["jars"].([]any) {
		m := j.(map[string]any)
		out[m["file"].(string)] = m
	}
	return out
}

// The plugin routes run through the real middleware chain (CSRF, rbac, audit) against the fake
// file service and a fake Modrinth.
func TestPluginsEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))
	base := fmt.Sprintf("/servers/%d/plugins", id)
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	e.up.project("LIB", "lib", "Lib", mVersion("lib1", "1.0", e.up.jar("Lib-1.0.jar", "lib 1.0"), day))
	e.up.project("TOOL", "tool", "Tool",
		mVersion("t2", "2.0", e.up.jar("Tool-2.0.jar", "tool 2.0"), day, "LIB"),
		mVersion("t1", "1.0", e.up.jar("Tool-1.0.jar", "tool 1.0"), day.Add(-24*time.Hour), "LIB"))

	if inv := admin.must(200, "GET", base, nil); inv["kind"] != "plugin" || inv["dir"] != "plugins" || inv["game_version"] != "1.21.11" {
		t.Fatalf("inventory = %v", inv)
	}
	if hits, _ := admin.must(200, "GET", base+"/search?source=modrinth&q=tool", nil)["hits"].([]any); len(hits) != 2 {
		t.Fatalf("hits = %v", hits)
	}
	if install, _ := admin.must(200, "GET", base+"/resolve?source=modrinth&project=tool&version=t1", nil)["install"].([]any); len(install) != 2 {
		t.Fatalf("plan = %v", install)
	}

	// installing needs the CSRF token like every POST
	csrf := admin.csrf
	admin.csrf = ""
	if code, out := admin.do("POST", base+"/install", map[string]any{"source": "modrinth", "project": "tool", "version": "t1"}); code != 403 {
		t.Fatalf("install without CSRF token = %d %v", code, out)
	}
	admin.csrf = csrf
	admin.must(202, "POST", base+"/install", map[string]any{"source": "modrinth", "project": "tool", "version": "t1"})
	if j := waitPluginJob(t, admin, id); j["state"] != "done" {
		t.Fatalf("install job = %v", j)
	}
	jars := jarFiles(admin.must(200, "GET", base, nil))
	if len(jars) != 2 || jars["Tool-1.0.jar"]["installed_by_panel"] != true || jars["Lib-1.0.jar"]["installed_by_panel"] != true {
		t.Fatalf("jars after install = %v", jars)
	}

	if ups, _ := admin.must(200, "GET", base+"/updates", nil)["updates"].([]any); len(ups) != 1 {
		t.Fatalf("updates = %v", ups)
	}
	admin.must(202, "POST", base+"/update", map[string]any{"files": []string{}})
	if j := waitPluginJob(t, admin, id); j["state"] != "done" {
		t.Fatalf("update job = %v", j)
	}
	admin.must(204, "POST", base+"/toggle", map[string]any{"file": "Tool-2.0.jar", "enabled": false})
	admin.must(204, "POST", base+"/delete", map[string]any{"file": "Lib-1.0.jar"})
	waitServer(t, admin, id, "running", stateIs("running"))
	inv := admin.must(200, "GET", base, nil)
	jars = jarFiles(inv)
	if len(jars) != 1 || jars["Tool-2.0.jar.disabled"]["enabled"] != false || inv["restart_needed"] != true {
		t.Fatalf("inventory at the end = %v", inv)
	}
	e.files.mu.Lock()
	_, onDisk := e.files.files[fmt.Sprintf("%d/plugins/Tool-2.0.jar.disabled", id)]
	e.files.mu.Unlock()
	if !onDisk {
		t.Fatal("the disabled jar is not in the file service")
	}

	// icon proxy with its own headers; foreign hosts are refused
	resp, body := admin.raw(base + "/icon?url=" + url.QueryEscape(e.up.cdn.URL+"/icons/TOOL.png"))
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Content-Security-Policy") != "default-src 'none'" ||
		resp.Header.Get("Cache-Control") != "private, max-age=86400" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || len(body) == 0 {
		t.Fatalf("icon = %d %v", resp.StatusCode, resp.Header)
	}
	if code, out := admin.do("GET", base+"/icon?url="+url.QueryEscape("https://evil.example/x.png"), nil); code != 400 {
		t.Fatalf("foreign icon = %d %v", code, out)
	}

	// a user with server.view only gets 403 (acceptance criterion 6)
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": id, "permissions": []string{"server.view"}}},
	})
	friend := e.client("")
	friend.must(200, "POST", "/auth/login", map[string]string{"username": "friend", "password": "another long secret 42"})
	if code, _ := friend.do("GET", base, nil); code != 403 {
		t.Fatalf("friend plugins = %d", code)
	}

	// acceptance criterion 7: every change is in the audit log and the chain verifies
	page := admin.must(200, "GET", "/audit?limit=200", nil)
	seen := map[string]bool{}
	for _, it := range page["items"].([]any) {
		seen[it.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{audit.ActionPluginInstall, audit.ActionPluginUpdate, audit.ActionPluginToggle, audit.ActionPluginRemove} {
		if !seen[want] {
			t.Errorf("audit log misses %s", want)
		}
	}
	if v := admin.must(200, "GET", "/audit/verify", nil); !isTrue(v["ok"]) {
		t.Fatalf("audit verify: %v", v)
	}
}
```

`cmd/panel/openapi_test.go`: Importe um `"github.com/Bernotese/mc-panel/internal/plugins"` und `"github.com/Bernotese/mc-panel/internal/plugins/upstream"` ergänzen und am Dateiende anfügen:

```go
// Every status and error code a plugin route can answer - the mapped service and upstream
// errors plus the panel's own checks - is declared by its operation in api/openapi.yaml, and
// the description names the code (shared responses: BadRequest = validation_failed, NotFound
// = not_found, TooManyRequests = rate_limited).
func TestPluginsErrorsMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	type answer struct {
		status int
		code   string
	}
	unsupported := []answer{{404, httpx.CodeNotFound}, {409, httpx.CodePluginsUnsupported}}
	lookup := []answer{
		{400, httpx.CodeValidation}, {429, httpx.CodeRateLimited},
		{502, httpx.CodePluginSourceUnavailable}, {502, httpx.CodeCatalogUnavailable},
	}
	ops := []struct {
		id    string
		errs  []error
		extra []answer
	}{
		{"listPlugins", []error{filesapi.ErrUnavailable}, unsupported},
		{"searchPlugins", []error{upstream.ErrUnavailable}, append(slices.Clone(unsupported), lookup...)},
		{"listPluginVersions", []error{upstream.ErrUnavailable, upstream.ErrNotFound}, append(slices.Clone(unsupported), lookup...)},
		{"resolvePlugin", []error{upstream.ErrUnavailable, upstream.ErrNotFound, filesapi.ErrUnavailable},
			append(append(slices.Clone(unsupported), lookup...), answer{409, httpx.CodePluginIncompatible}, answer{409, httpx.CodePluginNotDownloadable})},
		{"installPlugin", []error{upstream.ErrUnavailable, upstream.ErrNotFound, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict},
				answer{409, httpx.CodePluginIncompatible}, answer{409, httpx.CodePluginNotDownloadable}, answer{409, httpx.CodeFileExists},
				answer{429, httpx.CodeRateLimited}, answer{502, httpx.CodePluginSourceUnavailable}, answer{502, httpx.CodeCatalogUnavailable})},
		{"getPluginUpdates", []error{upstream.ErrUnavailable, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{429, httpx.CodeRateLimited}, answer{502, httpx.CodeCatalogUnavailable})},
		{"updatePlugins", []error{upstream.ErrUnavailable, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict},
				answer{429, httpx.CodeRateLimited}, answer{502, httpx.CodeCatalogUnavailable})},
		{"togglePlugin", []error{filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrInvalid, filesapi.ErrNotRegular, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict})},
		{"deletePlugin", []error{filesapi.ErrNotFound, filesapi.ErrInvalid, filesapi.ErrNotRegular, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict})},
		{"getPluginJob", nil, unsupported},
		{"getPluginIcon", nil, []answer{{400, httpx.CodeValidation}, {404, httpx.CodeNotFound}, {502, httpx.CodePluginSourceUnavailable}}},
	}
	defaults := map[string]string{"BadRequest": httpx.CodeValidation, "NotFound": httpx.CodeNotFound, "TooManyRequests": httpx.CodeRateLimited}
	for _, o := range ops {
		op := openAPIOperation(t, spec, o.id)
		want := slices.Clone(o.extra)
		for _, e := range o.errs {
			status, code := plugins.ErrorStatus(fmt.Errorf("wrapped: %w", e))
			want = append(want, answer{status, code})
		}
		for _, a := range want {
			text, ok := responseText(spec, op, a.status)
			switch {
			case !ok:
				t.Errorf("%s: answers %d %s, but declares no %d response", o.id, a.status, a.code, a.status)
			case strings.Contains(text, a.code):
			case defaults[strings.SplitN(text, ":", 2)[0]] == a.code:
			default:
				t.Errorf("%s: the %d response does not name %s: %s", o.id, a.status, a.code, text)
			}
		}
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `git add -N internal/plugins cmd/panel && make check`
Expected: FAIL beim Kompilieren (`h.icons` in `export_test.go`, `e.up` in `plugins_test.go`).

- [ ] **Step 4: Implementierung schreiben**

`internal/plugins/stubs.go` löschen (`git rm internal/plugins/stubs.go`).

In `internal/plugins/handler.go` die Typdefinition `Handler` und den `return` von `NewHandler` ersetzen:

```go
type Handler struct {
	q       *store.Queries
	clock   clock.Clock
	rec     audit.Recorder
	files   filesapi.Client
	inst    agentapi.InstancesClient
	catalog Catalog
	up      *upstream.Client
	log     *slog.Logger
	restart *restartTracker
	limit   *userLimiter
	jobs    *jobRegistry
	updates *upstream.Cache[updatesResult]
	icons   *iconCache
}
```

```go
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: fc, inst: inst, catalog: cat, up: up, log: log,
		restart: newRestartTracker(), limit: newUserLimiter(), jobs: newJobRegistry(d.Clock, maxJobs),
		updates: upstream.NewCache[updatesResult](d.Clock, updatesTTL, upstream.MaxCacheEntries),
		icons:   newIconCache(d.Clock, up),
	}
```

`internal/plugins/manage.go`:

```go
package plugins

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

type toggleRequest struct {
	File    string `json:"file"`
	Enabled *bool  `json:"enabled"`
}

type fileRequest struct {
	File string `json:"file"`
}

// changed records a plugin change: restart_needed and a new updates check.
func (h *Handler) changed(serverID int64) {
	h.markChanged(serverID)
	h.updates.Delete(updatesKey(serverID))
}

// findJar lists the jars and returns the one named file (known false if the listing is
// truncated and does not show it; the service decides then). A different jar named like
// target in any case is errFileExists (Review Focus #2); a missing file is filesapi.ErrNotFound.
func (h *Handler) findJar(ctx context.Context, srv server, file, target string) (jar filesapi.Jar, known bool, err error) {
	list, err := h.files.Jars(ctx, srv.ID, srv.Kind.Dir)
	if err != nil {
		return filesapi.Jar{}, false, err
	}
	for _, j := range list.Jars {
		switch {
		case j.Name == file:
			jar, known = j, true
		case target != "" && strings.EqualFold(j.Name, target):
			return filesapi.Jar{}, false, fmt.Errorf("%w: %s", errFileExists, j.Name)
		}
	}
	if !known && !list.Truncated {
		return filesapi.Jar{}, false, fmt.Errorf("%w: %s", filesapi.ErrNotFound, file)
	}
	return jar, known, nil
}

func (h *Handler) handleToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req toggleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	base, enabled, ok := cleanJarFile(req.File)
	if !ok || req.Enabled == nil {
		writeValidation(w, r, "file must be a jar name ending in .jar or .jar.disabled, enabled a boolean")
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	if enabled == *req.Enabled {
		w.WriteHeader(http.StatusNoContent) // already in the wanted state
		return
	}
	target := base
	if !*req.Enabled {
		target = base + disabledSuffix
	}
	if _, _, err := h.findJar(ctx, srv, req.File, target); err != nil {
		h.writeError(w, r, err)
		return
	}
	err := h.files.Rename(context.WithoutCancel(ctx), filesapi.RenameRequest{
		ID: srv.ID, From: filesapi.Join(srv.Kind.Dir, req.File), To: filesapi.Join(srv.Kind.Dir, target),
	})
	h.record(ctx, audit.ActionPluginToggle, srv.ID, result(err), map[string]any{"file": req.File, "enabled": *req.Enabled})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.changed(srv.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req fileRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	base, _, ok := cleanJarFile(req.File)
	if !ok {
		writeValidation(w, r, "file must be a jar name ending in .jar or .jar.disabled")
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	jar, known, err := h.findJar(ctx, srv, req.File, "")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	dctx := context.WithoutCancel(ctx)
	err = h.files.Delete(dctx, srv.ID, filesapi.Join(srv.Kind.Dir, req.File))
	if err == nil && known {
		h.dropRow(dctx, srv.ID, base, jar.SHA512)
	}
	h.record(ctx, audit.ActionPluginRemove, srv.ID, result(err), map[string]any{"file": req.File})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.changed(srv.ID)
	w.WriteHeader(http.StatusNoContent)
}

// dropRow deletes the row of a deleted jar, but only if it describes exactly that jar: the
// row of "x.jar" may belong to "x.jar.disabled" (Review Focus #2). A failure is logged; the
// next inventory removes an orphan anyway.
func (h *Handler) dropRow(ctx context.Context, serverID int64, base, sha512 string) {
	rows, err := h.q.PluginsListInstalls(ctx, serverID)
	if err != nil {
		h.log.WarnContext(ctx, "plugins: list rows after delete", "server_id", serverID, "err", err)
		return
	}
	for _, row := range rows {
		if row.Filename != base || row.Sha512 != sha512 {
			continue
		}
		if err := h.q.PluginsDeleteInstall(ctx, store.PluginsDeleteInstallParams{ServerID: serverID, Filename: base}); err != nil {
			h.log.WarnContext(ctx, "plugins: delete row", "server_id", serverID, "file", base, "err", err)
		}
	}
}
```

`internal/plugins/icon.go`:

```go
package plugins

import (
	"container/list"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

const (
	iconCacheBytes = 32 << 20         // LRU size (spec §6.8)
	iconTTL        = 24 * time.Hour   // per icon
	maxIconFetches = 8                // parallel upstream fetches in the panel
	iconTimeout    = 30 * time.Second // one fetch including the wait for a slot
)

// iconCache is an LRU of icons by bytes with shared fetches and a panel-wide fetch limit.
type iconCache struct {
	clock    clock.Clock
	up       *upstream.Client
	sem      chan struct{}
	maxBytes int64

	mu       sync.Mutex
	ll       *list.List // front = most recently used
	items    map[string]*list.Element
	bytes    int64
	inflight map[string]*iconCall
}

type iconItem struct {
	url  string
	icon upstream.Icon
	at   time.Time
}

type iconCall struct {
	done chan struct{}
	icon upstream.Icon
	err  error
}

func newIconCache(clk clock.Clock, up *upstream.Client) *iconCache {
	return &iconCache{
		clock: clk, up: up, sem: make(chan struct{}, maxIconFetches), maxBytes: iconCacheBytes,
		ll: list.New(), items: map[string]*list.Element{}, inflight: map[string]*iconCall{},
	}
}

func (c *iconCache) get(ctx context.Context, raw string) (upstream.Icon, error) {
	c.mu.Lock()
	if el, ok := c.items[raw]; ok {
		it := el.Value.(*iconItem)
		if c.clock.Now().Sub(it.at) < iconTTL {
			c.ll.MoveToFront(el)
			c.mu.Unlock()
			return it.icon, nil
		}
		c.removeLocked(el)
	}
	cl, ok := c.inflight[raw]
	if !ok {
		cl = &iconCall{done: make(chan struct{})}
		c.inflight[raw] = cl
		go c.fetch(context.WithoutCancel(ctx), raw, cl)
	}
	c.mu.Unlock()
	select {
	case <-cl.done:
		return cl.icon, cl.err
	case <-ctx.Done():
		return upstream.Icon{}, ctx.Err()
	}
}

func (c *iconCache) fetch(ctx context.Context, raw string, cl *iconCall) {
	ctx, cancel := context.WithTimeout(ctx, iconTimeout)
	defer cancel()
	var (
		icon upstream.Icon
		err  error
	)
	select {
	case c.sem <- struct{}{}:
		icon, err = c.up.Icon(ctx, raw)
		<-c.sem
	case <-ctx.Done():
		err = fmt.Errorf("%w: no free icon slot: %w", upstream.ErrUnavailable, ctx.Err())
	}
	c.mu.Lock()
	if err == nil {
		c.addLocked(raw, icon)
	}
	delete(c.inflight, raw)
	c.mu.Unlock()
	cl.icon, cl.err = icon, err
	close(cl.done)
}

func (c *iconCache) addLocked(raw string, icon upstream.Icon) {
	c.items[raw] = c.ll.PushFront(&iconItem{url: raw, icon: icon, at: c.clock.Now()})
	c.bytes += int64(len(icon.Data))
	for c.bytes > c.maxBytes && c.ll.Len() > 0 {
		c.removeLocked(c.ll.Back())
	}
}

func (c *iconCache) removeLocked(el *list.Element) {
	it := el.Value.(*iconItem)
	c.ll.Remove(el)
	delete(c.items, it.url)
	c.bytes -= int64(len(it.icon.Data))
}

// handleIcon proxies a project icon (spec §6.8); the browser never contacts a third party.
func (h *Handler) handleIcon(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.loadServer(w, r, false); !ok {
		return
	}
	raw := r.URL.Query().Get("url")
	if !h.up.IconURLAllowed(raw) {
		writeValidation(w, r, "url must be an https URL on cdn.modrinth.com or hangarcdn.papermc.io (at most 512 characters)")
		return
	}
	icon, err := h.icons.get(r.Context(), raw)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		h.log.WarnContext(r.Context(), "plugins: icon unavailable", "err", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "the icon could not be loaded")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", icon.ContentType)
	hd.Set("Content-Length", strconv.Itoa(len(icon.Data)))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Security-Policy", "default-src 'none'")
	hd.Set("Cache-Control", "private, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(icon.Data)
}
```

- [ ] **Step 5: Checks, CI, PR fertig**

Run: `make check`
Expected: PASS.

```bash
git add -A internal/plugins cmd/panel
git commit -m "feat(plugins): toggle, delete, icon proxy and end-to-end test

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: alle CI-Jobs grün (`go test` inkl. `TestToggle*`, `TestDelete*`, `TestManage*`, `TestIcon*`, `TestPluginsEndToEnd`, `TestPluginsErrorsMatchOpenAPI`, `TestRoutesMatchOpenAPI`, `TestRouteCoverage`; `test-race`; `lint`). Danach `gh pr ready` und dem Lead melden (Mergen nur durch den Nutzer).
