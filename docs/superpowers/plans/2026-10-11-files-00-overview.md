# mc-panel Teilprojekt 3b: Dateibrowser – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ein Dateibrowser pro Minecraft-Server (Liste, Editor, Up-/Download, Verwalten, Zip entpacken/herunterladen), dessen Dateioperationen ausschließlich ein unprivilegierter Dienst `mcpanel-files` (Benutzer `mcpanel-mc`) ausführt.

**Architecture:** Neues Binary `cmd/files` (Paket `internal/files`) lauscht auf einem Unix-Socket, prüft die Peer-UID des panels und arbeitet pro Anfrage über `os.Root` auf genau `<instances_dir>/instances/<id>`. Das panel (neues Paket `internal/serverfiles`) prüft Server, Recht `server.files`, CSRF und Audit, streamt Up- und Downloads ohne Puffer durch und fragt fürs Entpacken den Ist-Zustand beim agent ab. Der agent ändert nur die Rechte von `instances/` (`root:mcpanel-mc 0750`).

**Tech Stack:** Go (Stand `go.mod`, `os.Root`, `archive/zip`), chi, `modernc.org/sqlite`; React 19, TanStack Router/Query, shadcn/ui, react-i18next, MSW 2, Vitest, Playwright; neue Frontend-Abhängigkeiten nur CodeMirror 6 (`codemirror` und die nötigen `@codemirror/lang-*`/`@codemirror/legacy-modes`-Pakete, ohne React-Wrapper). Keine neuen Go-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-11-files-00-overview.md` (diese) | – | Ausführung, Constraints, Review Focus, Dateistruktur, Verträge V1–V5 |
| `2026-10-11-files-01-contracts.md` | 1–3 | agent: Rechte von `instances/`; `filesapi` (Pfadregeln, Draht-Typen, Client) + `files.toml`/`files_socket`-Konfiguration; panel-Vertrag (OpenAPI, Fehlercodes, Audit, Stub-Handler, Verdrahtung, MSW-Mocks) |
| `2026-10-11-files-02-service.md` | 4–8 | Dienst: Server/Peer-UID/Grenzen/Dev-Modus; Liste/Lesen/Schreiben/Verwalten; Upload/Download inkl. Ordner-Zip; Entpack-Jobs; Linux-Integrationstests |
| `2026-10-11-files-03-panel.md` | 9–12 | panel: Handler mit Streaming, Upload-Limit, Fehler-Mapping, Audit; Entpacken mit agent-Zustand; Power-Sperre und Abgleich-Pause; Verdrahtung |
| `2026-10-11-files-04-frontend.md` | 13–17 | Frontend: Dateiliste + Verwalten, Upload-Warteschlange, Editor (CodeMirror), Entpacken + Power-Sperre, Rechte-Hinweis, Playwright |
| `2026-10-11-files-05-ci-docs.md` | 18–20 | Betrieb (dev.sh, Test-VM-Unit, GoReleaser, Makefile), CI-Abnahme, Doku, Checkliste `docs/dev/acceptance-tp3b.md`, Gesamt-Review |

## Ausführung (Agent-Teams, CI statt lokaler Tests)

Es gelten die Arbeitsregeln aus `CLAUDE.md`:

- **Lokal nur `make check`** (und `make generate`, wo der Plan es sagt). Tests, Lint, Playwright nur in der CI; „Expected: PASS“ wird in den CI-Logs geprüft. Neue, noch ungetrackte Dateien vor `make check` mit `git add -N` vormerken (sonst prüft gofmt sie nicht).
- **TDD:** Test zuerst; „rot“ = `make check`-Kompilierfehler bzw. CI, „grün“ = CI.
- **Nichts Systemnahes auf dem Mac.** `npm install <paket>` in `web/` ist erlaubt (Projektabhängigkeit). Zum Debuggen von CI-Fehlern darf die Test-VM `claude@192.168.1.117` genutzt werden (Kopie in ein Scratch-Verzeichnis, nie dort deployen).
- **Git:** nie auf `main`; je Phase ein Branch und Draft-PR; jeder Teammate in eigenem Worktree (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`). Mergen nur durch den Nutzer.

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/files-contracts` | Teammate „contracts“ | 1–3 | – |
| B1 | `feat/files-service` | Teammate „files“ | 4–8 | A gemerged |
| B2 | `feat/files-panel` | Teammate „panel“ | 9–12 | A gemerged |
| B3 | `feat/files-frontend` | Teammate „frontend“ | 13–17 | A gemerged |
| C | `feat/files-ci-docs` | Teammate „ci-docs“ | 18–19 (20 koordiniert der Lead) | B1, B2, B3 gemerged |

Dateieigentum in Phase B (disjunkt):

- **B1:** `cmd/files/**` (mit `var version` wie panel/agent), `internal/files/**` außer `internal/files/filesapi/**`, `.golangci.yml` (nur gezielte eigene Regeln).
- **Socket des Dienstes:** systemd-Socket-Aktivierung (`LISTEN_FDS`/`LISTEN_PID`, ein Socket) bevorzugt; sonst selbst anlegen mit `socket_mode`. Peer-UID-Prüfung immer.
- **B2:** `internal/serverfiles/**`, `internal/servers/**` (nur Power-Sperre und Abgleich-Pause), `cmd/panel/**`.
- **B3:** `web/src/features/files/**` (neu), `web/src/features/servers/**`, `web/src/features/users/**`, Locale-Dateien unter `web/src/i18n/**`, `web/e2e/**`, `web/package.json`, `web/package-lock.json`.
- **Nur Phase A** ändert `internal/files/filesapi/**`, `internal/config/**`, `internal/agent/instances/datadir.go`, `internal/httpx/errors.go`, `internal/audit/types.go`, `api/openapi.yaml`, `web/src/api/**`, `web/src/mocks/**`. Bedarf → Lead fragen (Änderung kommt per eigenem PR auf `main`, danach `git merge origin/main`).
- **Nur Phase C** ändert `scripts/**`, `Makefile`, `.goreleaser.yaml`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md`.

## Global Constraints

- Alle Global Constraints aus TP1–3a und alle Konventionen aus `CLAUDE.md` gelten (`clock.Clock`, Kontext überall, Go-Tests nur Standardbibliothek, keine neuen Go-Abhängigkeiten, Lint-Ausnahmen gezielt mit Begründung, Conventional Commits mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`).
- Peer-UID des Dienstes: nur `panel_uid`, nie root.
- Recht: alle Datei-Routen `rbac.Server(rbac.PermServerFiles, "id")`; Admins immer.
- Dienst läuft als `mcpanel-mc`, nie als root; jede Dateioperation über `*os.Root` auf `<instances_dir>/instances/<id>`; Symlinks werden nie verfolgt (`Lstat` vor dem Öffnen, `Stat` + `os.SameFile` danach), Lesen mit `O_NONBLOCK`.
- Pfadregeln `filesapi.CleanPath` (Spec §3.4): relativ, `/`, kein `..`, kein `\`, keine leeren Komponenten, keine Steuerzeichen, gültiges UTF-8, ≤ 4096 Bytes, Komponente ≤ 255 Bytes. Verborgen: `.rcon-cli.env`, `.rcon-cli.yaml` (oberste Ebene), jede Komponente mit Präfix `.mcpanel-`.
- Schwärzen: `rcon.password=…` in `server.properties` (oberste Ebene) → `********` beim Lesen, Download und Ordner-Zip; beim Schreiben wird eine Zeile `rcon.password=********` durch den aktuellen Wert ersetzt.
- Grenzen (Default, `files.toml [limits]`): Upload 4 GiB, Entpacken 20 GiB / 100 000 Einträge, Reserve 2 GiB, Editor 5 MiB, Liste 10 000 Einträge; Dienst: 4 Uploads gleichzeitig, 1 Entpack-Job pro Instanz, 2 insgesamt; panel: 2 Uploads pro Benutzer; Stream-Fristen 60 s pro Vorgang.
- Entpacken: Vorprüfung des Archivs synchron in `POST /v1/extract` (Fehler `400 invalid`, `409 exists` bei nicht leerem Ziel, `413`, `507`), erst danach `202` + Job. Entpacken nur bei gestopptem Server (`exited`/`created`/`missing`); während eines Jobs: `power start/restart` → `409 conflict`, Abgleich startet nicht.
- Neue panel-Fehlercodes: `files_unavailable` (503), `file_exists` (409), `file_modified` (409), `not_text` (415), `not_regular` (400), `instance_running` (409), `insufficient_storage` (507). Neue Audit-Aktionen: `file.write`, `file.upload`, `file.mkdir`, `file.rename`, `file.delete`, `file.extract`, `file.download`.
- Frontend: Texte `de`/`en`, keine Inline-Skripte, keine CDNs, kein `dangerouslySetInnerHTML`, MSW-Mock für jede OpenAPI-Operation.

## Review Focus

1. **Ausbruch aus dem Instanz-Verzeichnis:** Symlink (Datei, Ordner im Pfad, Ziel des Uploads/Renames), `..` in Pfaden und Zip-Einträgen, absolute Zip-Pfade, Hardlinks, FIFO/Geräte, Rennen zwischen Prüfung und Öffnen; erwartet: kein Byte außerhalb von `instances/<id>` gelesen oder geschrieben, keine Hänger. → Aufgaben 5–7 (Dienst), Aufgabe 2 (`CleanPath`).
2. **Ressourcen-Erschöpfung:** Zip-Bombe mit gefälschten Größen, 100 001 Einträge, Upload mit falscher `Content-Length`, viele parallele Uploads/Jobs, volle Platte; erwartet: saubere Fehler (`413`/`429`/`507`), keine Teil-Dateien, Reserve bleibt frei. → Aufgaben 6–7, 9.
3. **Durchreichen ohne Puffern:** Ein 4-GiB-Upload/Download darf weder panel noch Dienst mehr als wenige MiB Speicher kosten; abgebrochene Verbindungen beenden beide Seiten. → Aufgaben 6, 9.
4. **RCON-Geheimnis:** `.rcon-cli.*` nie sichtbar/lesbar (auch nicht über Ordner-Zip, Rename, Upload-Überschreiben), `rcon.password` geschwärzt in Lesen, Download und Zip; Speichern mit geschwärzter Zeile zerstört das Passwort nicht. → Aufgaben 5–6.
5. **Entpacken bei laufendem Server:** Start zwischen Zustandsprüfung und Job, Neustart durch den Abgleich während des Jobs, Dienst nicht erreichbar; erwartet: kein Start während eines Jobs, klare Fehler. → Aufgabe 11.

## Dateistruktur

```
internal/agent/instances/datadir.go (+_test)          (1)  instances/ root:<mc gid> 0750, CheckInstancesDir
internal/files/filesapi/path.go, types.go, errors.go  (2)  Pfadregeln, Draht-Typen, Fehler
internal/files/filesapi/client.go                     (2)  Client über Unix-Socket (Streams)
internal/config/files.go                              (2)  files.toml; panel: files_socket
internal/httpx/errors.go, internal/audit/types.go     (3)  Codes, Aktionen
api/openapi.yaml, web/src/api/schema.d.ts, types.ts   (3)  Vertrag
internal/serverfiles/handler.go                       (3)  Deps, Routes (Stubs), ab B2 echt
cmd/panel/app.go, main.go                             (3)  Verdrahtung (filesapi-Client aus files_socket)
web/src/mocks/handlers/files.ts, db.ts                (3)  Mocks
cmd/files/main.go, internal/files/server.go           (4)  Dienst, Peer-UID, Grenzen, Dev-Modus, Temp-Aufräumen
internal/files/ops.go, redact.go                      (5)  list/read/write/mkdir/rename/delete, Schwärzen
internal/files/transfer.go, zipdir.go                 (6)  Upload, Download, Ordner-Zip, statfs
internal/files/extract.go, jobs.go                    (7)  Entpack-Jobs
internal/files/*_linux_test.go                        (8)  FIFO, Peer-UID, echte Rechte
internal/serverfiles/*.go                             (9–10) Handler, Streaming, Audit
internal/servers/power.go, reconcile.go, deps         (11) Power-Sperre, Abgleich-Pause
cmd/panel/app.go                                      (12) echte Deps (ExtractGate)
web/src/features/files/**                             (13–16) Liste, Upload, Editor, Entpacken
web/src/features/servers/**, users/**                 (15–16) Tab, Power-Sperre, Rechte-Hinweis
web/e2e/servers.spec.ts                               (17) Playwright
scripts/dev.sh, scripts/testvm-deploy.sh, Makefile, .goreleaser.yaml (18)
scripts/ci-instances.sh, .github/workflows/ci.yml     (18) CI-Abnahme
docs/dev/acceptance-tp3b.md, docs/dev/README.md, CLAUDE.md, README.md (19)
```

## Verträge (verbindlich)

### V1 – `filesapi` (Aufgabe 2, Paket `github.com/Bernotese/mc-panel/internal/files/filesapi`)

```go
const (
	PathHealth   = "/v1/health"
	PathList     = "/v1/list"
	PathRead     = "/v1/read"
	PathWrite    = "/v1/write"
	PathUpload   = "/v1/upload"   // PUT, Query id, path, overwrite=0|1; Body = Datei; Content-Length Pflicht
	PathDownload = "/v1/download" // GET, Query id, path
	PathMkdir    = "/v1/mkdir"
	PathRename   = "/v1/rename"
	PathDelete   = "/v1/delete"
	PathExtract  = "/v1/extract"
	PathJobs     = "/v1/jobs"     // GET, Query id

	HeaderKind = "X-Files-Kind" // Download-Antwort: "file" | "dir"

	MaxPathBytes      = 4096
	MaxComponentBytes = 255
	MaxJSONBytes      = 32 << 20 // write: 5 MiB text can grow to 6× when JSON-escaped; config validates max_text_bytes ≤ MaxJSONBytes/6
	MaxSmallJSONBytes = 64 << 10

	CodeNotFound            = "not_found"            // 404
	CodeExists              = "exists"               // 409
	CodeModified            = "modified"             // 409
	CodeNotRegular          = "not_regular"          // 400
	CodeNotText             = "not_text"             // 415
	CodeTooLarge            = "too_large"            // 413
	CodeInsufficientStorage = "insufficient_storage" // 507
	CodeBusy                = "busy"                 // 429
	CodeInvalid             = "invalid"              // 400
)

var (
	ErrNotFound, ErrExists, ErrModified, ErrNotRegular, ErrNotText,
	ErrTooLarge, ErrInsufficientStorage, ErrBusy, ErrInvalid error // je errors.New(<code>)
	ErrUnavailable error // Dienst nicht erreichbar, Protokollfehler, 5xx
)

// CleanPath validates p per spec §3.4 and returns it normalised ("" = root, no trailing "/").
// Errors wrap ErrInvalid. Hidden paths are NOT rejected here (IsHidden decides).
func CleanPath(p string) (string, error)
// IsHidden reports whether a cleaned path is or lies below a hidden name (spec §3.4).
func IsHidden(clean string) bool
// Join joins cleaned path components ("" + "a" = "a").
func Join(dir, name string) string

type EntryType string // "file" | "dir" | "symlink" | "other"
const (TypeFile EntryType = "file"; TypeDir = "dir"; TypeSymlink = "symlink"; TypeOther = "other")

type Entry struct {
	Name  string    `json:"name"`
	Type  EntryType `json:"type"`
	Size  int64     `json:"size"`
	MTime int64     `json:"mtime"` // Unix ms
}
type PathRequest struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}                                          // list, read, mkdir, delete
type ListResponse struct {
	Entries   []Entry `json:"entries"` // never nil
	Truncated bool    `json:"truncated"`
}
type ReadResponse struct {
	Content string `json:"content"`
	MTime   int64  `json:"mtime"`
	Size    int64  `json:"size"`
}
type WriteRequest struct {
	ID            int64  `json:"id"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	ExpectedMTime *int64 `json:"expected_mtime"` // nil = must not exist yet
}
type WriteResponse struct {
	MTime int64 `json:"mtime"`
	Size  int64 `json:"size"`
}
type RenameRequest struct {
	ID   int64  `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}
type UploadResponse struct {
	Size int64 `json:"size"`
}
type ExtractRequest struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
	Dest string `json:"dest"`
}
type JobState string // "running" | "done" | "failed"
type Job struct {
	ID           string   `json:"id"`
	State        JobState `json:"state"`
	Path         string   `json:"path"`
	Dest         string   `json:"dest"`
	Bytes        int64    `json:"bytes"`
	Entries      int64    `json:"entries"`
	TotalBytes   int64    `json:"total_bytes"`
	TotalEntries int64    `json:"total_entries"`
	Error        string   `json:"error"`
	StartedAt    int64    `json:"started_at"`  // Unix ms
	FinishedAt   int64    `json:"finished_at"` // 0 while running
}
type JobsResponse struct {
	Job *Job `json:"job"`
}
type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Download is an open download; the caller must Close Body.
type Download struct {
	Body   io.ReadCloser
	Size   int64  // -1 for a directory zip
	IsDir  bool   // HeaderKind == "dir"
	Name   string // base name of the path ("" for the root → caller uses the server name)
}

// Client is the panel's view of the service (implemented by *UnixClient).
type Client interface {
	Health(ctx context.Context) error
	List(ctx context.Context, id int64, path string) (ListResponse, error)
	Read(ctx context.Context, id int64, path string) (ReadResponse, error)
	Write(ctx context.Context, req WriteRequest) (WriteResponse, error)
	Upload(ctx context.Context, id int64, path string, overwrite bool, size int64, body io.Reader) (UploadResponse, error)
	Download(ctx context.Context, id int64, path string) (Download, error)
	Mkdir(ctx context.Context, id int64, path string) error
	Rename(ctx context.Context, req RenameRequest) error
	Delete(ctx context.Context, id int64, path string) error
	Extract(ctx context.Context, req ExtractRequest) (Job, error)
	Job(ctx context.Context, id int64) (*Job, error)
}
func NewClient(socketPath string) *UnixClient
```

- Client: kleine Aufrufe mit 10 s Timeout; `Upload` ohne Timeout (nur `ctx`; die Antwort kommt erst nach dem ganzen Body); `Download` ohne Gesamt-Timeout, aber höchstens 10 s bis zu den Antwort-Headern; Fehlerantworten → Fehler, die per `errors.Is` den Sentinels entsprechen (Code → Sentinel); Verbindungs-/Protokollfehler und 5xx → `ErrUnavailable`. Proxy nie verwenden (wie `agent.Client`).

### V2 – Konfiguration (Aufgabe 2, `internal/config`)

```go
type FilesLimits struct {
	MaxUploadBytes    int64 `toml:"max_upload_bytes"`
	MaxExtractBytes   int64 `toml:"max_extract_bytes"`
	MaxExtractEntries int64 `toml:"max_extract_entries"`
	ReserveBytes      int64 `toml:"reserve_bytes"`
	MaxTextBytes      int64 `toml:"max_text_bytes"`
	MaxListEntries    int   `toml:"max_list_entries"`
}
type Files struct {
	Dev          bool        `toml:"dev"`
	Socket       string      `toml:"socket"`
	SocketMode   string      `toml:"socket_mode"` // octal, default "0660"; only used without socket activation
	PanelUID     int         `toml:"panel_uid"` // -1 = unset (only allowed with dev)
	InstancesDir string      `toml:"instances_dir"`
	Limits       FilesLimits `toml:"limits"`
}
func DefaultFiles() Files      // Werte aus Spec §3.1; Socket /run/mc-panel-files/files.sock, socket_mode 0660
func DevFiles(root string) Files // socket <root>/.dev/files.sock, instances_dir <root>/.dev/mc (wie der dev-agent), dev true
func LoadFiles(path string) (Files, error) // unbekannte Schlüssel → Fehler; fehlende [limits]-Schlüssel = Defaults; Validierung
// Panel: neues Feld FilesSocket string `toml:"files_socket"` (Default /run/mc-panel-files/files.sock, Dev <root>/.dev/files.sock)
```

### V3 – panel-API (Aufgabe 3, `api/openapi.yaml`, Tag `files`)

| Pfad | Methode | operationId | Request | Antworten |
|---|---|---|---|---|
| `/servers/{id}/files` | get | `listFiles` | Query `path` (string, optional) | `200 FileList`, 400, 401, 403, 404, 503 |
| `/servers/{id}/files/content` | get | `readFile` | Query `path` (Pflicht) | `200 FileContent`, 400, 401, 403, 404, 415, 503 |
| `/servers/{id}/files/content` | put | `writeFile` | `WriteFileRequest` | `200 FileWriteResult`, 400, 401, 403, 404, 409, 413, 503, 507 |
| `/servers/{id}/files/upload` | post | `uploadFile` | Query `path` (Pflicht), `overwrite` (boolean); Body `application/octet-stream` | `201 UploadResult`, 400, 401, 403, 404, 409, 413, 429, 503, 507 |
| `/servers/{id}/files/download` | get | `downloadFile` | Query `path` | `200` `application/octet-stream` bzw. `application/zip` (binary), 400, 401, 403, 404, 503 |
| `/servers/{id}/files/mkdir` | post | `makeDirectory` | `FilePathRequest` | `201`, 400, 401, 403, 404, 409, 503 |
| `/servers/{id}/files/rename` | post | `renameFile` | `RenameFileRequest` | `204`, 400, 401, 403, 404, 409, 503 |
| `/servers/{id}/files/delete` | post | `deleteFile` | `FilePathRequest` | `204`, 400, 401, 403, 404, 503 |
| `/servers/{id}/files/extract` | post | `extractArchive` | `ExtractRequest` | `202 FileJob`, 400, 401, 403, 404, 409, 429, 503, 507 |
| `/servers/{id}/files/jobs` | get | `getFileJob` | – | `200 FileJobResponse`, 401, 403, 404, 503 |

Schemas: `FileEntry {name, type (enum file|dir|symlink|other), size, mtime}`, `FileList {path, entries[], truncated}`, `FileContent {path, content, mtime, size}`, `WriteFileRequest {path, content, expected_mtime: integer|null}` (additionalProperties false), `FileWriteResult {mtime, size}`, `UploadResult {size}`, `FilePathRequest {path}`, `RenameFileRequest {from, to}`, `ExtractRequest {path, dest}`, `FileJob {id, state (enum running|done|failed), path, dest, bytes, entries, total_bytes, total_entries, error, started_at, finished_at}`, `FileJobResponse {job: FileJob|null}`. Error-Enum um die sieben Codes erweitert. `POST /servers/{id}/power`: `409` beschreibt zusätzlich „Entpacken läuft“.

### V4 – panel-intern (Aufgaben 3, 9–12)

```go
package serverfiles // github.com/Bernotese/mc-panel/internal/serverfiles

type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Files     filesapi.Client          // nil = Dienst fehlt: alles 503 files_unavailable
	Instances agentapi.InstancesClient // Ist-Zustand fürs Entpacken; nil = agent fehlt
	Log       *slog.Logger
}
func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
// ExtractRunning reports whether an extract job is running for the server (asks the service;
// service unreachable → false). Implements servers.ExtractGate.
func (h *Handler) ExtractRunning(ctx context.Context, serverID int64) bool
```

```go
package servers
// ExtractGate is consulted before power start/restart and by the reconcile loop (B2, Aufgabe 11).
type ExtractGate interface {
	ExtractRunning(ctx context.Context, serverID int64) bool
}
// servers.Deps bekommt das Feld Extract ExtractGate (nil = nie gesperrt).
```

Phase A liefert außerdem `httpx.BodyLimitExcept` (globales 1-MiB-Body-Limit mit Ausnahmen) und `serverfiles.UnlimitedBody` (die Upload-Route ist ausgenommen; B2 nimmt zusätzlich `PUT …/files/content` aus und begrenzt dort selbst auf `filesapi.MaxJSONBytes`), weil ein `MaxBytesReader` im Handler nicht mehr aufzuheben ist.

Phase A registriert alle zehn Routen mit `rbac.Server(rbac.PermServerFiles, "id")`; bis B2 antworten sie `503 files_unavailable` („files not implemented yet“).

### V5 – MSW-Mock (Aufgabe 3, `web/src/mocks/handlers/files.ts`)

- Pro Server ein Baum im Speicher (`db.files: Record<serverId, MockNode>`), Start für den laufenden Server „survival“: `server.properties` (mit `rcon.password=********`), `bukkit.yml`, `ops.json`, `plugins/` (mit `EssentialsX.jar`, 2 MB), `plugins/Essentials/config.yml`, `world/` (mit `level.dat`), `logs/latest.log`, `world-backup.zip` (30 MB), ein Symlink `current -> world`; gestoppter Server mit kleinerem Baum.
- Operationen wie V3 mit Pfadregeln wie `CleanPath`, `409 file_exists`/`file_modified`, `415 not_text` für Binärdateien (`.jar`, `.dat`, `.zip`), `instance_running` beim Entpacken eines laufenden Servers, Entpack-Job, der über ~6 s in Schritten fortschreitet und dann `done` ist und einen neuen Ordner anlegt.
- Upload: liest den Body, legt die Datei an; Download: liefert einen kleinen Blob mit `Content-Disposition`.
- Rechte über `serverPermissions` (`server.files`) wie die übrigen Server-Mocks.
