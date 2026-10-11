# Teilprojekt 3b – Dienst `mcpanel-files` (Aufgaben 4–8) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der unprivilegierte Dienst `mcpanel-files` (Binary `cmd/files`, Paket `internal/files`) führt alle Dateioperationen des Dateibrowsers aus Spec §3–§4 aus – Liste, Lesen, atomares Schreiben, Anlegen, Umbenennen, Löschen, Upload, Download (Datei und Ordner-Zip) und Entpack-Jobs –, ausschließlich als Benutzer `mcpanel-mc`, pro Anfrage eingesperrt in genau ein Instanz-Verzeichnis und ohne je einem Symlink zu folgen.

**Architecture:** `files.Service` kapselt die Operationen: Jede Anfrage öffnet `<instances_dir>/instances/<id>` per `os.OpenRoot` (nach `Lstat` + `os.SameFile`, damit das Datenverzeichnis selbst kein Symlink ist) und arbeitet nur über dieses `*os.Root`. Pfade kommen ausschließlich über `filesapi.CleanPath` herein; vor jedem Zugriff prüft `lstat` jede Ordner-Komponente einzeln (kein Symlink, echter Ordner), nach jedem Öffnen vergleicht `os.SameFile` mit dem `Lstat`-Ergebnis. Schreiben, Upload und Entpacken gehen über Temp-Dateien bzw. -Ordner `.mcpanel-tmp-*` im Zielordner und werden atomar per `rename` (Überschreiben) oder `link` (ohne Überschreiben) übernommen. `files.Server` ist ein schmaler HTTP-Server auf dem Unix-Socket (Muster `agent.Server`): feste Routen, strenge JSON-Dekodierung, Fehler `{error, code}`, Fristen pro I/O-Vorgang statt Gesamt-Timeouts für Streams. `cmd/files` lädt `files.toml` (Produktion `/etc/mc-panel-files/files.toml`), weigert sich als root zu laufen, übernimmt den Socket per systemd-Socket-Aktivierung (`LISTEN_FDS`/`LISTEN_PID`, fd 3) oder legt ihn sonst selbst über `unixsock` mit `socket_mode` an und lässt immer nur die Peer-UID des panels (und root) über `peercred` herein.

**Tech Stack:** Go (Stand `go.mod`, 1.27: `os.Root` mit `Lstat`, `OpenFile`, `Mkdir`, `MkdirAll`, `Rename`, `Link`, `Remove`, `RemoveAll`, `Chtimes`, `FS`; `http.ResponseController`), `archive/zip`, `io/fs.WalkDir`, `golang.org/x/sys/unix` (`Statfs`, bereits Abhängigkeit), vorhandene Pakete `internal/unixsock`, `internal/agent/peercred`, `internal/clock`, `internal/config` (V2), `internal/files/filesapi` (V1). Tests nur Standardbibliothek. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§1.2, §2, §3, §4, §8)

## Global Constraints

Es gelten **alle** Global Constraints, die Verträge V1/V2 und das Dateieigentum aus `docs/superpowers/plans/2026-10-11-files-00-overview.md` (und damit auch die aus TP1–3a und `CLAUDE.md`). Phase A (Aufgaben 1–3) ist auf `main`: `internal/files/filesapi` (Pfadregeln, Draht-Typen, Fehler, Client) und `internal/config` (`config.Files`, `config.FilesLimits`, `config.LoadFiles`, `config.DevFiles`) existieren nach V1/V2.

**Arbeitsweise:** Branch `feat/files-service`, eigener Worktree: `git worktree add ../mc-panel-wt/feat-files-service -b feat/files-service origin/main`. Lokal nur `make check` (neue Dateien vorher mit `git add -N` vormerken, sonst prüft gofmt sie nicht); Tests laufen in der CI (`gh pr checks <pr> --watch`, Fehler mit `gh run view <id> --log-failed`). Draft-PR beim ersten Push (Aufgabe 4), danach jede Aufgabe als eigener Commit auf demselben Branch.

Dienst-spezifische, verbindliche Werte dieses Teilplans:

| Thema | Wert |
|---|---|
| Eigene Pfade | `cmd/files/**`, `internal/files/**` **außer** `internal/files/filesapi/**` |
| Fremde Dateien | `internal/files/filesapi/**`, `internal/config/**`, `internal/agent/**`, `internal/unixsock/**` gehören anderen Phasen: nur lesen/benutzen; Bedarf → Lead |
| Benutzer | Prozess verweigert den Start als root (`geteuid() == 0` → Fehler), auch im Dev-Modus |
| Konfiguration | Flag `-config` (Default `/etc/mc-panel-files/files.toml`), `-dev` (ohne `-config`: `config.DevFiles(cwd)`, d. h. Socket `.dev/files.sock`, `instances_dir` `.dev/mc`), `-version`; `var version = "dev"` (per `-ldflags -X main.version=…`, der Release-Snapshot prüft das) |
| Peer-UID | immer `peercred.AllowUIDs(panel_uid, 0)` – panel-Benutzer und root (wie beim agent; Betreiber und Skripte dürfen als root zugreifen, root kann ohnehin alles); `panel_uid = -1` nur mit `dev` (dann eigene UID; `DevFiles` setzt sie ohnehin) |
| Socket | bevorzugt systemd-Socket-Aktivierung: `LISTEN_PID == getpid()` und `LISTEN_FDS == 1` → fd 3 als Listener (muss ein Unix-Socket sein), danach `LISTEN_PID`, `LISTEN_FDS`, `LISTEN_FDNAMES` aus der Umgebung entfernen; sonst `unixsock.Listen(cfg.Socket, cfg.SocketFileMode(), -1)` (Gruppe = primäre Gruppe des Prozesses, kein `chown`); Dev: Socket-Ordner wird angelegt |
| Instanz-Verzeichnis | `<instances_dir>/instances/<id>`; `os.Lstat` muss ein echter Ordner sein (Symlink → Fehler 500), danach `os.OpenRoot` und `os.SameFile(lstat, root.Stat("."))`; fehlt → `ErrNotFound` („server <id> has no data directory yet“); Dev: vorher `os.MkdirAll(..., 0o750)` |
| Pfade | jede Anfrage: `filesapi.CleanPath`, danach `filesapi.IsHidden` → `ErrNotFound`; Wurzel `""` nur bei `list`/`download` erlaubt, sonst `ErrInvalid` bzw. `ErrNotRegular` (`read`) |
| Symlinks | `lstat`: jede Ordner-Komponente per `root.Lstat` (Symlink oder kein Ordner → `ErrNotRegular`), dann das Ziel per `root.Lstat`; Öffnen mit `O_NOFOLLOW\|O_NONBLOCK`, danach `f.Stat()` regulär **und** `os.SameFile` mit dem `Lstat`-Ergebnis, sonst `ErrNotRegular` |
| Temp-Namen | `<zielordner>/.mcpanel-tmp-<16 hex>` (crypto/rand); Start räumt alle `.mcpanel-tmp-*` bis Tiefe 64 in allen Instanzen ab |
| Modi | Dateien `0644`, Ordner `0755` (systemd-`UMask=0027` wirkt zusätzlich) |
| Liste | `ReadDir` in Blöcken zu 256, verborgene Namen ausgelassen, höchstens `max_list_entries`, darüber `truncated: true`; Typ `dir\|file\|symlink\|other`; `size` nur für Dateien, sonst 0; `mtime` Unix-ms; sortiert: Ordner zuerst, dann Name ohne Groß/Klein, dann Name |
| Lesen | nur reguläre Dateien ≤ `max_text_bytes`, gültiges UTF-8, ohne NUL, sonst `ErrNotText`; `server.properties` (oberste Ebene) geschwärzt |
| Schwärzen | Zeile `^[ \t\f]*rcon\.password(?:[ \t\f]*[=:][ \t\f]*\|[ \t\f]+)(.*)$` → Wert `********` (leerer Wert bleibt leer); beim Schreiben: Wert `********` → aktueller Wert aus der Datei, fehlt dort einer → Zeile entfällt |
| Schreiben | `len(content) > max_text_bytes` → `ErrTooLarge`; `expected_mtime == nil` → Datei darf nicht existieren (`ErrExists`); sonst muss sie existieren (`ErrModified` wenn nicht) und `mtime` (ms) gleich sein (`ErrModified`); Temp-Datei, `fsync`, `rename`; neue Datei per `root.Link` (atomar ohne Überschreiben) |
| Upload | `Content-Length` Pflicht (`< 0` → `ErrInvalid`), `> max_upload_bytes` → `ErrTooLarge`; höchstens **4** gleichzeitig (`ErrBusy`); Ziel existiert und nicht regulär → `ErrNotRegular`, existiert ohne `overwrite` → `ErrExists`; Byte-Zahl ≠ `Content-Length` → `ErrInvalid`, Temp-Datei weg, Ziel unverändert |
| Download | Datei: `X-Files-Kind: file`, `Content-Length`, `io.CopyN` genau `size` Bytes; Ordner: `X-Files-Kind: dir`, `application/zip`, Einträge relativ zum Elternordner (Ordner selbst ist oberste Ebene), Symlinks/Sonderdateien/verborgene Namen ausgelassen, `server.properties` geschwärzt; Fehler nach dem Start der Antwort → `panic(http.ErrAbortHandler)` |
| Plattenplatz | `DiskSpace.Available(<instanz-verzeichnis>)` (statfs: `Bavail × Bsize`) muss ≥ benötigte Bytes + `reserve_bytes` sein, sonst `ErrInsufficientStorage`; geprüft vor Schreiben, Upload und Entpacken |
| Entpacken | Quelle reguläre `.zip`; Ziel neu oder leerer Ordner (`ErrExists` sonst), Elternordner existiert; Vorprüfung aller Einträge (Pfadregeln, kanonisch, nicht verborgen, nur Ordner/reguläre Dateien, nicht verschlüsselt, nur Store/Deflate, keine Duplikate, keine Datei als Elternteil, ≤ `max_extract_entries`, Summe ≤ `max_extract_bytes`); Entpacken in `<eltern>/.mcpanel-tmp-*`, tatsächliche Bytes erneut gezählt; Erfolg → leeres Ziel entfernen, `rename`; Fehler → Temp-Ordner weg; höchstens 1 Job pro Instanz, 2 insgesamt (`ErrBusy`); Jobs im Speicher, letzter pro Instanz bleibt |
| HTTP | `ReadHeaderTimeout 5 s`, `ReadTimeout`/`WriteTimeout 30 s`, `IdleTimeout 60 s`, `MaxHeaderBytes 8 KiB`; Upload/Download: Server-Fristen aufgehoben, Frist **60 s** pro Lese-/Schreibvorgang; `POST` nur mit `application/json`; JSON ≤ `filesapi.MaxSmallJSONBytes` (64 KiB), `write` ≤ `filesapi.MaxJSONBytes` (6 MiB); `DisallowUnknownFields`, nichts nach dem JSON-Objekt |
| Fehler-Mapping | `ErrNotFound` 404 `not_found`, `ErrExists` 409 `exists`, `ErrModified` 409 `modified`, `ErrNotRegular` 400 `not_regular`, `ErrNotText` 415 `not_text`, `ErrTooLarge` 413 `too_large`, `ErrInsufficientStorage` 507 `insufficient_storage`, `ErrBusy` 429 `busy`, `ErrInvalid` 400 `invalid`; sonst 500 `file operation failed` (Details nur im Log); Aufrufer schon weg → nur Debug-Log, keine Antwort |
| Shutdown | `Server.Shutdown(ctx)`; läuft die Frist ab, `http.Server.Close()`; danach `Service.Close()` bricht laufende Jobs ab (Zustand `failed`) und wartet auf sie |

### Warum `lstat` selbst die Komponenten prüft (Go 1.27, `os.Root`)

`go doc os.Root` (1.27.2) bietet `Lstat`, `Open`, `OpenFile`, `OpenRoot`, `Mkdir`, `MkdirAll`, `Rename`, `Link`, `Symlink`, `Readlink`, `Remove`, `RemoveAll`, `Chmod`, `Chown`, `Lchown`, `Chtimes`, `ReadFile`, `WriteFile`, `Stat`, `FS`. Dabei gilt: `os.Root` verhindert jeden Ausbruch aus dem Wurzelordner (auch über `..` und Symlinks nach außen), **folgt aber Symlinks innerhalb der Wurzel** – auch bei `OpenFile(…|O_NOFOLLOW)`, weil `os.Root` intern selbst mit `O_NOFOLLOW` arbeitet und Links nach `ELOOP` innerhalb der Wurzel auflöst (`os/root_unix.go`, in TP3a-Review gegen den Quelltext geprüft). `root.Lstat(name)` folgt dem letzten Element nicht, Zwischen-Komponenten aber schon. Die Spec verlangt „Symlinks werden nie verfolgt“ (§3.2) – deshalb:

1. **Harte Grenze:** `os.Root` pro Instanz (kein Byte außerhalb von `instances/<id>`), Dienst als `mcpanel-mc` ohne Capabilities.
2. **„Nie folgen“:** `lstat` prüft jede Ordner-Komponente einzeln mit `root.Lstat`; nach dem Öffnen `os.SameFile`. Ein Rennen (Komponente wird zwischen Prüfung und Öffnen gegen einen Symlink getauscht) kann den Zugriff höchstens auf eine andere Stelle **derselben** Instanz lenken – Schicht 1 bleibt bestehen; für geöffnete Dateien fängt der `SameFile`-Vergleich auch das ab.

`O_NOFOLLOW` wird trotzdem übergeben (dokumentiert die Absicht, schadet nicht); `O_NONBLOCK` verhindert, dass eine nach der Prüfung untergeschobene FIFO den Dienst blockiert.

## Review Focus

1. **Ausbruch aus dem Instanz-Verzeichnis (Übersicht #1):** Symlink als Datei (nach außen absolut/relativ, nach innen), Symlink als Ordner-Komponente, Upload/Write/Rename auf Symlink-Ziele, Datenverzeichnis selbst als Symlink, FIFO, Zip-Einträge mit `../`, absolut, `\`, Symlink-Einträge → kein Byte außerhalb gelesen/geschrieben, keine Hänger, `not_regular`/`invalid`. → `TestReadRefusesUntrustedFiles`, `TestWriteRefusesSymlinks`, `TestUploadRefusesSymlinks`, `TestOpenInstance` (Aufgaben 4–6), `TestExtractRejectsArchives` (Aufgabe 7).
2. **Ressourcen-Erschöpfung (Übersicht #2):** Zip-Bombe mit gefälschter Größe, 101 Einträge bei Limit 100, Upload mit falscher Länge, 5. paralleler Upload, 3. Job, volle Platte → `413`/`429`/`507`/`400`, keine Teil-Dateien, kein Temp-Rest. → `TestExtractDetectsLyingSizes`, `TestExtractJobLimits`, `TestUploadLimits`, `TestUploadBusy` (Aufgaben 6–7).
3. **Durchreichen ohne Puffern (Übersicht #3):** 64-MiB-Upload kostet höchstens wenige MiB Speicher; Download über `io.CopyN`/Zip-Stream. → `TestUploadStreamsWithoutBuffering` (Aufgabe 6).
4. **RCON-Geheimnis (Übersicht #4):** `.rcon-cli.*` nie in Liste, Lesen, Download, Ordner-Zip, Rename-Ziel, Upload-Ziel; `rcon.password` geschwärzt in Lesen, Download und Zip; Speichern mit geschwärzter Zeile behält das Passwort. → `TestHiddenNamesAreNotFound`, `TestReadRedactsRconPassword`, `TestWriteKeepsRconPassword` (Aufgabe 5), `TestDownloadRedacts`, `TestDownloadDirZip` (Aufgabe 6).
5. **Vertrag zum Client:** Fehlercodes kommen beim `filesapi`-Client als die richtigen Sentinels an, Upload/Download/Extract funktionieren über den echten Unix-Socket mit Peer-UID-Prüfung. → `TestEndToEndWithClient`, `TestPeerUIDIsChecked` (Aufgabe 8).

## Dateistruktur

```
cmd/files/main.go                     (4)  Flags, Konfig, root-Verbot, Socket, Peer-UID, Start/Shutdown
cmd/files/main_test.go                (4)  -version, root-Verbot, Argumente, panel_uid
internal/files/service.go             (4)  Config, Service, open, lstat, openRegular, Temp, commit, fsErr, reserve
internal/files/disk.go                (4)  DiskSpace, statfsDisk
internal/files/disk_linux.go          (4)  statfs (Bsize int64)
internal/files/disk_darwin.go         (4)  statfs (Bsize uint32)
internal/files/disk_other.go          (4)  !linux && !darwin: nicht unterstützt
internal/files/cleanup.go             (4)  CleanupTemp
internal/files/server.go              (4)  Server, Routen, decode, Fehler-Mapping, Fristen-Wrapper
internal/files/helpers_test.go        (4)  newService, fakeDisk, put/get, startHTTP
internal/files/service_test.go        (4)  open, lstat, cleanup, reserve, statusFor
internal/files/server_test.go         (4)  Health, 404, 405
internal/files/ops.go                 (5)  List, Read, Write, Mkdir, Rename, Delete
internal/files/redact.go              (5)  redactProperties, restorePassword
internal/files/ops_test.go            (5)
internal/files/redact_test.go         (5)
internal/files/server_ops_test.go     (5)
internal/files/transfer.go            (6)  Upload, Download
internal/files/zipdir.go              (6)  Ordner als Zip-Stream
internal/files/transfer_test.go       (6)
internal/files/server_transfer_test.go (6)
internal/files/jobs.go                (7)  Job-Verwaltung
internal/files/extract.go             (7)  Extract, checkArchive, extract
internal/files/extract_test.go        (7)
internal/files/e2e_test.go            (8)  echter Unix-Socket + filesapi-Client + peercred
internal/files/umask_linux_test.go    (8)  Modi unter Umask 0027
```

---

### Task 4: Dienst-Grundgerüst – Service, Server, `cmd/files`

**Files:**
- Create: `internal/files/service.go`, `internal/files/disk.go`, `internal/files/disk_linux.go`, `internal/files/disk_darwin.go`, `internal/files/disk_other.go`, `internal/files/cleanup.go`, `internal/files/server.go`
- Create: `cmd/files/main.go` (inkl. Socket-Aktivierung)
- Modify (nur falls gosec meldet): `.golangci.yml` – gezielte, begründete Regel
- Test: `internal/files/helpers_test.go`, `internal/files/service_test.go`, `internal/files/server_test.go`, `cmd/files/main_test.go`

**Interfaces:**
- Consumes: `filesapi` (V1/Phase A: Pfade, Codes, Sentinels, `CodeOf`, `CleanPath`, `IsHidden`, `Join`, `JobRunning/JobDone/JobFailed`, `ErrorResponse`, `HealthResponse`, `MaxJSONBytes` = 32 MiB, `MaxSmallJSONBytes`), `config.Files` (inkl. `SocketMode`, `SocketFileMode()`), `config.FilesLimits`, `config.LoadFiles`, `config.DevFiles` (V2/Phase A), `unixsock.Listen`, `peercred.Wrap`/`AllowUIDs`/`ConnContext`/`FromContext`, `clock.Clock`.
- Produces (für Aufgaben 5–8):

```go
package files

type Config struct {
	InstancesDir string
	Limits       config.FilesLimits
	Dev          bool
	Clock        clock.Clock  // nil = clock.Real{}
	Log          *slog.Logger // nil = slog.Default()
	Disk         DiskSpace    // nil = statfs
}
type DiskSpace interface{ Available(dir string) (uint64, error) }

func New(cfg Config) *Service
func (s *Service) Close()
func (s *Service) CleanupTemp() int

// intern, von Aufgaben 5–7 benutzt:
func (s *Service) open(id int64) (*os.Root, error)
func (s *Service) instanceDir(id int64) string
func (s *Service) reserve(id, need int64) error
func cleanVisible(p string) (string, error)          // CleanPath + IsHidden → ErrNotFound
func lstat(root *os.Root, clean string) (fs.FileInfo, error)
func requireDir(root *os.Root, clean string) error
func openRegular(root *os.Root, clean string, flag int) (*os.File, fs.FileInfo, error)
func parentOf(clean string) string
func tempName(dir string) (string, error)
func commit(root *os.Root, tmp, clean string, overwrite bool) error
func fsErr(err error) error

func NewServer(svc *Service, version string, log *slog.Logger) *Server
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (s *Server) Serve(l net.Listener) error
func (s *Server) Shutdown(ctx context.Context) error
// intern:
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool
func (s *Server) fail(w http.ResponseWriter, r *http.Request, op string, err error)
func statusFor(err error) (int, string)
func parseID(v string) (int64, error)
func writeJSON(w http.ResponseWriter, status int, v any)
```

- [ ] **Step 1: Test-Helfer schreiben**

`internal/files/helpers_test.go`:

```go
package files

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// fakeDisk reports a fixed amount of free space.
type fakeDisk struct {
	mu    sync.Mutex
	avail uint64
	err   error
}

func (d *fakeDisk) Available(string) (uint64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.avail, d.err
}

func (d *fakeDisk) set(avail uint64) {
	d.mu.Lock()
	d.avail = avail
	d.mu.Unlock()
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testLimits() config.FilesLimits {
	return config.FilesLimits{
		MaxUploadBytes:    1 << 20,
		MaxExtractBytes:   1 << 20,
		MaxExtractEntries: 100,
		ReserveBytes:      1 << 10,
		MaxTextBytes:      64 << 10,
		MaxListEntries:    50,
	}
}

var testStart = time.UnixMilli(1_760_000_000_000)

// newService returns a Service on a fresh instances_dir that already contains the data
// directory of server 1, and the path of that data directory.
func newService(t *testing.T, mod ...func(*Config)) (*Service, string) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "instances", "1")
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		InstancesDir: base,
		Limits:       testLimits(),
		Clock:        clock.NewFake(testStart),
		Log:          discardLog(),
		Disk:         &fakeDisk{avail: 1 << 40},
	}
	for _, m := range mod {
		m(&cfg)
	}
	s := New(cfg)
	t.Cleanup(s.Close)
	return s, data
}

// put writes content to rel (slash-separated) below dir, creating folders.
func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// get reads rel below dir; ok is false if it does not exist.
func get(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

// names lists the entry names of a directory on disk (including hidden ones).
func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// startHTTP serves srv over a TCP httptest server (no peer credentials).
func startHTTP(t *testing.T, srv *Server) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

// postJSON posts body as JSON to ts+path and returns status and body.
func postJSON(t *testing.T, ts *httptest.Server, path string, body any) (int, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

// errCode decodes the code of an error answer.
func errCode(t *testing.T, body []byte) string {
	t.Helper()
	var er struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(body, &er); err != nil {
		t.Fatalf("not an error body: %s", body)
	}
	return er.Code
}
```

- [ ] **Step 2: Tests für Service-Grundlagen und Server schreiben (rot)**

`internal/files/service_test.go`:

```go
package files

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestOpenInstance(t *testing.T) {
	s, data := newService(t)
	root, err := s.open(1)
	if err != nil {
		t.Fatalf("open existing: %v", err)
	}
	_ = root.Close()

	if _, err := s.open(2); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("missing data dir: %v, want ErrNotFound", err)
	} else if strings.Contains(err.Error(), s.cfg.InstancesDir) {
		t.Fatalf("not-found message leaks the host path: %v", err)
	}
	if _, err := s.open(0); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v, want ErrInvalid", err)
	}

	// A data directory that is a symlink is never opened, not even to a sibling instance.
	if err := os.Symlink(data, filepath.Join(s.cfg.InstancesDir, "instances", "3")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.open(3); err == nil || errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("symlinked data dir: %v, want a hard error", err)
	}
}

func TestOpenInstanceDevCreatesDataDir(t *testing.T) {
	s, _ := newService(t, func(c *Config) { c.Dev = true })
	root, err := s.open(7)
	if err != nil {
		t.Fatalf("dev open: %v", err)
	}
	_ = root.Close()
	if fi, err := os.Stat(s.instanceDir(7)); err != nil || !fi.IsDir() {
		t.Fatalf("dev mode did not create the data dir: %v", err)
	}
}

func TestLstatNeverFollowsComponents(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "x")
	if err := os.Symlink("world", filepath.Join(data, "current")); err != nil {
		t.Fatal(err)
	}
	put(t, data, "file", "x")
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if fi, err := lstat(root, "world/level.dat"); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("plain path: %v %v", fi, err)
	}
	if fi, err := lstat(root, "current"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink itself must be reported as symlink: %v %v", fi, err)
	}
	if _, err := lstat(root, "current/level.dat"); !errors.Is(err, filesapi.ErrNotRegular) {
		t.Fatalf("path through a symlinked folder: %v, want ErrNotRegular", err)
	}
	if _, err := lstat(root, "file/x"); !errors.Is(err, filesapi.ErrNotRegular) && !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("path through a file: %v", err)
	}
	if _, err := lstat(root, "missing/x"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("missing parent: %v, want ErrNotFound", err)
	}
	if fi, err := lstat(root, ""); err != nil || !fi.IsDir() {
		t.Fatalf("root: %v %v", fi, err)
	}
}

func TestCleanVisible(t *testing.T) {
	for _, p := range []string{".rcon-cli.env", ".rcon-cli.yaml", ".mcpanel-tmp-ab", "plugins/.mcpanel-x/y"} {
		if _, err := cleanVisible(p); !errors.Is(err, filesapi.ErrNotFound) {
			t.Errorf("%q: %v, want ErrNotFound", p, err)
		}
	}
	for _, p := range []string{"../x", "/abs", `a\b`, "a//b"} {
		if _, err := cleanVisible(p); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%q: %v, want ErrInvalid", p, err)
		}
	}
	if c, err := cleanVisible("plugins/.rcon-cli.env"); err != nil || c != "plugins/.rcon-cli.env" {
		t.Errorf("nested .rcon-cli.env is a normal file: %q %v", c, err)
	}
}

func TestCleanupTempRemovesLeftovers(t *testing.T) {
	s, data := newService(t)
	put(t, data, ".mcpanel-tmp-1", "partial upload")
	put(t, data, "world/.mcpanel-tmp-2/region/r.mca", "partial extract")
	put(t, data, "world/level.dat", "keep")
	put(t, data, ".mcpanel-other", "not a temp name")
	if n := s.CleanupTemp(); n != 2 {
		t.Fatalf("removed %d entries, want 2", n)
	}
	if _, ok := get(t, data, ".mcpanel-tmp-1"); ok {
		t.Fatal("temp file survived")
	}
	if _, err := os.Stat(filepath.Join(data, "world", ".mcpanel-tmp-2")); !os.IsNotExist(err) {
		t.Fatalf("temp folder survived: %v", err)
	}
	if v, ok := get(t, data, "world/level.dat"); !ok || v != "keep" {
		t.Fatal("cleanup touched a normal file")
	}
	if _, ok := get(t, data, ".mcpanel-other"); !ok {
		t.Fatal("cleanup removed a file that is not a temp name")
	}
}

func TestReserve(t *testing.T) {
	disk := &fakeDisk{avail: 10 << 10}
	s, _ := newService(t, func(c *Config) { c.Disk = disk })
	if err := s.reserve(1, 9<<10); err != nil { // 9 KiB + 1 KiB reserve = 10 KiB
		t.Fatalf("exactly enough: %v", err)
	}
	if err := s.reserve(1, 9<<10+1); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("one byte too much: %v", err)
	}
	disk.err = errors.New("statfs broke")
	if err := s.reserve(1, 1); err == nil || errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("statfs error must surface as an internal error: %v", err)
	}
}

func TestStatusFor(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{filesapi.ErrNotFound, http.StatusNotFound, filesapi.CodeNotFound},
		{filesapi.ErrExists, http.StatusConflict, filesapi.CodeExists},
		{filesapi.ErrModified, http.StatusConflict, filesapi.CodeModified},
		{filesapi.ErrNotRegular, http.StatusBadRequest, filesapi.CodeNotRegular},
		{filesapi.ErrNotText, http.StatusUnsupportedMediaType, filesapi.CodeNotText},
		{filesapi.ErrTooLarge, http.StatusRequestEntityTooLarge, filesapi.CodeTooLarge},
		{filesapi.ErrInsufficientStorage, http.StatusInsufficientStorage, filesapi.CodeInsufficientStorage},
		{filesapi.ErrBusy, http.StatusTooManyRequests, filesapi.CodeBusy},
		{filesapi.ErrInvalid, http.StatusBadRequest, filesapi.CodeInvalid},
		{errors.New("boom"), http.StatusInternalServerError, ""},
	} {
		status, code := statusFor(fsErr(c.err))
		if status != c.status || code != c.code {
			t.Errorf("%v: %d %q, want %d %q", c.err, status, code, c.status, c.code)
		}
	}
}
```

`internal/files/server_test.go`:

```go
package files

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerHealthAndRouting(t *testing.T) {
	s, _ := newService(t)
	ts := startHTTP(t, NewServer(s, "1.2.3", discardLog()))

	resp, err := http.Get(ts.URL + filesapi.PathHealth)
	if err != nil {
		t.Fatal(err)
	}
	var h filesapi.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || h.Status != "ok" || h.Version != "1.2.3" {
		t.Fatalf("health: %d %+v", resp.StatusCode, h)
	}

	resp, err = http.Get(ts.URL + "/v1/nope")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path: %d", resp.StatusCode)
	}

	resp, err = http.Post(ts.URL+filesapi.PathHealth, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodGet {
		t.Fatalf("wrong method: %d allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}
```

`cmd/files/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
)

// The real fd-3 path of socket activation is exercised on the test VM and in
// scripts/ci-instances.sh (Phase C), not here: a unit test cannot own fd 3 safely.

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, &out, &bytes.Buffer{}, func() int { return 0 }); err != nil {
		t.Fatalf("-version: %v", err)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Fatalf("version output %q", out.String())
	}
}

func TestRunRefusesRoot(t *testing.T) {
	err := run(context.Background(), []string{"-dev"}, &bytes.Buffer{}, &bytes.Buffer{}, func() int { return 0 })
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root start: %v, want refusal", err)
	}
}

func TestRunRejectsArguments(t *testing.T) {
	if err := run(context.Background(), []string{"extra"}, &bytes.Buffer{}, &bytes.Buffer{}, func() int { return 1000 }); err == nil {
		t.Fatal("unexpected positional argument accepted")
	}
}

func TestRunRejectsBrokenConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "files.toml")
	if err := os.WriteFile(p, []byte("unknown_key = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-config", p}, &bytes.Buffer{}, &bytes.Buffer{}, func() int { return 1000 }); err == nil {
		t.Fatal("unknown config key accepted")
	}
}

func TestActivatedListener(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var unset []string
	unsetenv := func(k string) error { unset = append(unset, k); return nil }

	l, err := activatedListener(env(nil), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("no activation: %v %v", l, err)
	}
	if len(unset) != 3 {
		t.Fatalf("activation variables not removed: %v", unset)
	}
	l, err = activatedListener(env(map[string]string{"LISTEN_PID": "7", "LISTEN_FDS": "1"}), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("activation for another pid must be ignored: %v %v", l, err)
	}
	if _, err := activatedListener(env(map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2"}), unsetenv, 42); err == nil {
		t.Fatal("two sockets accepted")
	}
}

func TestPanelUID(t *testing.T) {
	if _, err := panelUID(config.Files{PanelUID: -1}); err == nil {
		t.Fatal("unset panel_uid accepted outside dev mode")
	}
	if uid, err := panelUID(config.Files{PanelUID: -1, Dev: true}); err != nil || int(uid) != os.Getuid() {
		t.Fatalf("dev default: %d %v", uid, err)
	}
	if uid, err := panelUID(config.Files{PanelUID: 998}); err != nil || uid != 998 {
		t.Fatalf("configured uid: %d %v", uid, err)
	}
	if _, err := panelUID(config.Files{PanelUID: 1 << 40}); err == nil {
		t.Fatal("out-of-range uid accepted")
	}
}
```

- [ ] **Step 3: `make check` – rot (Kompilierfehler: `New`, `NewServer`, `run`, … fehlen)**

Run: `git add -N internal/files cmd/files && make check`
Expected: FAIL mit `undefined: New`, `undefined: run` usw.

- [ ] **Step 4: Service-Grundlagen implementieren**

`internal/files/service.go`:

```go
// Package files implements mcpanel-files, the unprivileged service behind the file browser
// (spec docs/superpowers/specs/2026-10-11-mc-panel-files-design.md). It runs as the user that
// owns the Minecraft data (mcpanel-mc, never root); every request works on exactly one
// instance data directory through an *os.Root and never follows a symlink.
package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	tempPrefix      = ".mcpanel-tmp-"
	maxUploads      = 4
	maxJobsTotal    = 2
	cleanupMaxDepth = 64
	fileMode        = 0o644
	dirMode         = 0o755
	serverProps     = "server.properties"
)

// Config configures a Service.
type Config struct {
	InstancesDir string             // <instances_dir>; data dirs are <InstancesDir>/instances/<id>
	Limits       config.FilesLimits // spec §3.1
	Dev          bool               // make dev: create missing data directories
	Clock        clock.Clock        // nil = clock.Real{}
	Log          *slog.Logger       // nil = slog.Default()
	Disk         DiskSpace          // nil = statfs
}

// Service performs the file operations of all instances.
type Service struct {
	cfg     Config
	clock   clock.Clock
	log     *slog.Logger
	disk    DiskSpace
	uploads chan struct{} // at most maxUploads concurrent uploads
}

func New(cfg Config) *Service {
	s := &Service{cfg: cfg, clock: cfg.Clock, log: cfg.Log, disk: cfg.Disk, uploads: make(chan struct{}, maxUploads)}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.disk == nil {
		s.disk = statfsDisk{}
	}
	return s
}

// Close releases background work (extract jobs, Task 7).
func (s *Service) Close() {}

func (s *Service) instanceDir(id int64) string {
	return filepath.Join(s.cfg.InstancesDir, "instances", strconv.FormatInt(id, 10))
}

// open returns an *os.Root on the data directory of server id. The directory itself must be
// a real directory (a symlink is refused even if it points to another instance).
func (s *Service) open(id int64) (*os.Root, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	dir := s.instanceDir(id)
	if s.cfg.Dev {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: server %d has no data directory yet", filesapi.ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() { // Lstat: a symlink is not a directory here
		return nil, fmt.Errorf("data directory %s is not a real directory", dir)
	}
	root, err := os.OpenRoot(dir) //nolint:gosec // G304: <instances_dir>/instances/<id>, id is an int64 > 0
	if err != nil {
		return nil, err
	}
	st, err := root.Stat(".")
	if err != nil || !os.SameFile(fi, st) {
		_ = root.Close()
		return nil, fmt.Errorf("data directory %s changed while it was opened", dir)
	}
	return root, nil
}

// cleanVisible validates p (filesapi.CleanPath) and treats hidden names as missing.
func cleanVisible(p string) (string, error) {
	clean, err := filesapi.CleanPath(p)
	if err != nil {
		return "", err
	}
	if filesapi.IsHidden(clean) {
		return "", fmt.Errorf("%w: %s", filesapi.ErrNotFound, clean)
	}
	return clean, nil
}

// parentOf returns the parent of a cleaned path ("" for top-level names and the root).
func parentOf(clean string) string {
	i := strings.LastIndexByte(clean, '/')
	if i < 0 {
		return ""
	}
	return clean[:i]
}

func rootName(clean string) string {
	if clean == "" {
		return "."
	}
	return clean
}

// lstat returns the Lstat info of clean after checking that every folder component on the
// way is a real directory, never a symlink (os.Root itself would follow symlinks that stay
// inside the root). "" is the root folder.
func lstat(root *os.Root, clean string) (fs.FileInfo, error) {
	if clean == "" {
		return root.Stat(".")
	}
	parts := strings.Split(clean, "/")
	for i := 1; i < len(parts); i++ {
		p := strings.Join(parts[:i], "/")
		fi, err := root.Lstat(p)
		if err != nil {
			return nil, fsErr(err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a folder", filesapi.ErrNotRegular, p)
		}
	}
	fi, err := root.Lstat(clean)
	if err != nil {
		return nil, fsErr(err)
	}
	return fi, nil
}

// requireDir checks that clean exists and is a real folder.
func requireDir(root *os.Root, clean string) error {
	fi, err := lstat(root, clean)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a folder", filesapi.ErrNotRegular, rootName(clean))
	}
	return nil
}

// openRegular opens a regular file without following symlinks: Lstat first, then
// O_NOFOLLOW|O_NONBLOCK (a FIFO swapped in meanwhile cannot block), then the opened file
// must be the very file that Lstat saw.
func openRegular(root *os.Root, clean string, flag int) (*os.File, fs.FileInfo, error) {
	fi, err := lstat(root, clean)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, rootName(clean))
	}
	f, err := root.OpenFile(clean, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: cleaned path inside the instance's os.Root
	if err != nil {
		return nil, nil, fsErr(err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(fi, st) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: %s changed while it was opened", filesapi.ErrNotRegular, clean)
	}
	return f, st, nil
}

// tempName returns a fresh temp name inside folder dir.
func tempName(dir string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filesapi.Join(dir, tempPrefix+hex.EncodeToString(b[:])), nil
}

// commit moves the finished temp file tmp to clean. With overwrite it renames over clean;
// without, it hard-links (fails atomically if clean exists meanwhile) and removes tmp.
func commit(root *os.Root, tmp, clean string, overwrite bool) error {
	if overwrite {
		return fsErr(root.Rename(tmp, clean))
	}
	if err := root.Link(tmp, clean); err != nil {
		return fsErr(err)
	}
	_ = root.Remove(tmp) // a leftover is removed at the next start (CleanupTemp)
	return nil
}

// fsErr maps file system errors to the filesapi sentinels; other errors stay internal.
func fsErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, filesapi.ErrNotFound), errors.Is(err, filesapi.ErrExists), errors.Is(err, filesapi.ErrModified),
		errors.Is(err, filesapi.ErrNotRegular), errors.Is(err, filesapi.ErrNotText), errors.Is(err, filesapi.ErrTooLarge),
		errors.Is(err, filesapi.ErrInsufficientStorage), errors.Is(err, filesapi.ErrBusy), errors.Is(err, filesapi.ErrInvalid):
		return err
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%w: %w", filesapi.ErrNotFound, err)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %w", filesapi.ErrExists, err)
	case errors.Is(err, syscall.EISDIR), errors.Is(err, syscall.ELOOP):
		return fmt.Errorf("%w: %w", filesapi.ErrNotRegular, err)
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return fmt.Errorf("%w: %w", filesapi.ErrInsufficientStorage, err)
	}
	return err
}

// reserve checks that need more bytes still leave the configured reserve free.
func (s *Service) reserve(id, need int64) error {
	avail, err := s.disk.Available(s.instanceDir(id))
	if err != nil {
		return fmt.Errorf("check free space: %w", err)
	}
	want := uint64(max(need, 0)) + uint64(max(s.cfg.Limits.ReserveBytes, 0))
	if avail < want {
		return fmt.Errorf("%w: %d bytes free, %d bytes needed including the reserve", filesapi.ErrInsufficientStorage, avail, want)
	}
	return nil
}
```

`internal/files/disk.go`:

```go
package files

// DiskSpace reports the free space of the file system that holds dir.
type DiskSpace interface {
	Available(dir string) (uint64, error)
}

// statfsDisk asks the kernel (statfs: blocks available to unprivileged users × block size).
type statfsDisk struct{}

func (statfsDisk) Available(dir string) (uint64, error) { return availableBytes(dir) }
```

`internal/files/disk_linux.go`:

```go
//go:build linux

package files

import "golang.org/x/sys/unix"

func availableBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
```

`internal/files/disk_darwin.go`:

```go
//go:build darwin

package files

import "golang.org/x/sys/unix"

func availableBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
```

`internal/files/disk_other.go`:

```go
//go:build !linux && !darwin

package files

import "errors"

func availableBytes(string) (uint64, error) {
	return 0, errors.ErrUnsupported
}
```

`internal/files/cleanup.go`:

```go
package files

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CleanupTemp removes leftover temp files and folders (".mcpanel-tmp-*", up to depth 64) of
// every instance; an aborted upload, write or extract never survives a restart. It runs once
// before the socket accepts requests and returns the number of removed entries. Problems of
// single instances are logged and skipped.
func (s *Service) CleanupTemp() int {
	entries, err := os.ReadDir(filepath.Join(s.cfg.InstancesDir, "instances"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("files: cannot list instances for temp cleanup", "err", err)
		}
		return 0
	}
	removed := 0
	for _, e := range entries {
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil || id <= 0 || !e.IsDir() {
			continue
		}
		root, err := s.open(id)
		if err != nil {
			s.log.Warn("files: temp cleanup skipped an instance", "id", id, "err", err)
			continue
		}
		removed += cleanupRoot(root, s.log, id)
		_ = root.Close()
	}
	return removed
}

func cleanupRoot(root *os.Root, log *slog.Logger, id int64) int {
	var victims []string
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable parts are skipped
		}
		if p != "." && strings.HasPrefix(d.Name(), tempPrefix) {
			victims = append(victims, p)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() && p != "." && strings.Count(p, "/") >= cleanupMaxDepth-1 {
			return fs.SkipDir
		}
		return nil
	})
	n := 0
	for _, v := range victims {
		if err := root.RemoveAll(v); err != nil {
			log.Warn("files: cannot remove a leftover temp entry", "id", id, "path", v, "err", err)
			continue
		}
		n++
	}
	return n
}
```

- [ ] **Step 5: Server implementieren**

`internal/files/server.go`:

```go
package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	ioTimeout         = 60 * time.Second // stream deadline per read or write (spec §4.1)
	readHeaderTimeout = 5 * time.Second
	requestTimeout    = 30 * time.Second
)

type route struct {
	method  string
	handler http.HandlerFunc
}

// Server serves the filesapi endpoints on the Unix socket.
type Server struct {
	svc     *Service
	version string
	log     *slog.Logger
	io      clock.Clock // stream deadlines: always real time
	routes  map[string]route
	srv     *http.Server
}

func NewServer(svc *Service, version string, log *slog.Logger) *Server {
	s := &Server{svc: svc, version: version, log: log, io: clock.Real{}}
	s.routes = map[string]route{
		filesapi.PathHealth: {method: http.MethodGet, handler: s.health},
	}
	s.srv = &http.Server{
		Handler:           s,
		ConnContext:       peercred.ConnContext,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       requestTimeout,
		WriteTimeout:      requestTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// Serve accepts connections on l until Shutdown; it then returns nil.
func (s *Server) Serve(l net.Listener) error {
	if err := s.srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting and waits for running requests; when ctx ends first, open
// uploads and downloads are cut off.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		_ = s.srv.Close()
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.routes[r.URL.Path]
	if !ok {
		s.fail(w, r, "route", fmt.Errorf("%w: no such endpoint %s", filesapi.ErrNotFound, r.URL.Path))
		return
	}
	if r.Method != rt.method {
		w.Header().Set("Allow", rt.method)
		writeJSON(w, http.StatusMethodNotAllowed, filesapi.ErrorResponse{Error: "method " + r.Method + " not allowed"})
		return
	}
	if r.Method == http.MethodPost {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, filesapi.ErrorResponse{Error: "content type must be application/json", Code: filesapi.CodeInvalid})
			return
		}
	}
	rt.handler(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, filesapi.HealthResponse{Status: "ok", Version: s.version})
}

// decode strictly decodes exactly one JSON object of at most limit bytes; on failure it has
// answered already.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		if _, terr := dec.Token(); !errors.Is(terr, io.EOF) {
			err = errors.New("unexpected data after the JSON body")
		}
	}
	if err == nil {
		return true
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		s.fail(w, r, "decode", fmt.Errorf("%w: request body exceeds %d bytes", filesapi.ErrTooLarge, limit))
	} else {
		s.fail(w, r, "decode", fmt.Errorf("%w: invalid JSON body: %v", filesapi.ErrInvalid, err))
	}
	return false
}

// fail answers err: sentinel errors with their code, everything else as a generic 500 whose
// details only go to the log. A caller that has already gone gets no answer.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	if r.Context().Err() != nil {
		s.log.Debug("files request ended by the caller", "op", op, "path", r.URL.Path)
		return
	}
	status, code := statusFor(err)
	if status == http.StatusInternalServerError {
		s.log.Error("file operation failed", "op", op, "err", err, "peer_uid", peerUID(r))
		writeJSON(w, status, filesapi.ErrorResponse{Error: "file operation failed"})
		return
	}
	s.log.Info("file request rejected", "op", op, "status", status, "code", code, "err", err.Error(), "peer_uid", peerUID(r))
	writeJSON(w, status, filesapi.ErrorResponse{Error: err.Error(), Code: code})
}

// statusFor maps an error to the wire code and HTTP status (filesapi.CodeOf; other errors
// are internal: 500 without a code).
func statusFor(err error) (int, string) {
	code, status := filesapi.CodeOf(err)
	return status, code
}

// parseID parses the id query parameter of the streaming and job endpoints.
func parseID(v string) (int64, error) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: id must be an integer > 0", filesapi.ErrInvalid)
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// peerUID returns the UID stored by peercred.ConnContext, or -1 if unknown.
func peerUID(r *http.Request) int64 {
	if uid, ok := peercred.FromContext(r.Context()); ok {
		return int64(uid)
	}
	return -1
}

// deadlineReader renews the connection's read deadline before every read, so an upload may
// take hours but never stalls for longer than ioTimeout.
type deadlineReader struct {
	r   io.Reader
	rc  *http.ResponseController
	now func() time.Time
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	_ = d.rc.SetReadDeadline(d.now().Add(ioTimeout))
	return d.r.Read(p)
}

// deadlineWriter renews the write deadline before every write (downloads).
type deadlineWriter struct {
	w   io.Writer
	rc  *http.ResponseController
	now func() time.Time
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(d.now().Add(ioTimeout))
	return d.w.Write(p)
}
```

- [ ] **Step 6: `cmd/files` implementieren**

`cmd/files/main.go`:

```go
// Command files is mcpanel-files, the unprivileged file service of the file browser. It runs
// as the user that owns the Minecraft data (mcpanel-mc), serves a small JSON/stream API on a
// Unix socket (systemd socket activation in production) that only the panel user (and root)
// may connect to, and works on one instance data directory per request through an os.Root.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	defaultConfigPath = "/etc/mc-panel-files/files.toml"
	shutdownTimeout   = 10 * time.Second
	activationFD      = 3 // SD_LISTEN_FDS_START
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Geteuid)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "files:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, geteuid func() int) error {
	fs := flag.NewFlagSet("files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to files.toml (default "+defaultConfigPath+" unless -dev)")
	dev := fs.Bool("dev", false, "development mode: socket ./.dev/files.sock (0600), data in ./.dev/mc, missing data directories are created, text logs")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}
	// Spec §1.2: file operations never run as root.
	if geteuid() == 0 {
		return errors.New("files must not run as root: start it as the user that owns the Minecraft data (mc_user, e.g. mcpanel-mc)")
	}

	cfg, err := loadConfig(*configPath, *dev)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Dev, stderr)
	uid, err := panelUID(cfg)
	if err != nil {
		return err
	}
	l, activated, err := listen(cfg)
	if err != nil {
		return err
	}

	svc := files.New(files.Config{InstancesDir: cfg.InstancesDir, Limits: cfg.Limits, Dev: cfg.Dev, Clock: clock.Real{}, Log: log})
	if n := svc.CleanupTemp(); n > 0 {
		log.Info("files: removed leftover temp entries", "count", n)
	}
	srv := files.NewServer(svc, version, log)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(uid, 0), log)) }() // the panel and root, like the agent
	log.Info("files started", "version", version, "socket", cfg.Socket, "socket_activation", activated,
		"panel_uid", uid, "instances_dir", cfg.InstancesDir, "dev", cfg.Dev, "uid", os.Getuid())

	select {
	case err := <-errc:
		svc.Close()
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
	}
	log.Info("files shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	svc.Close()
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-errc
}

// loadConfig reads files.toml; -dev without -config uses the dev defaults (V2).
func loadConfig(path string, dev bool) (config.Files, error) {
	if dev && path == "" {
		root, err := os.Getwd()
		if err != nil {
			return config.Files{}, err
		}
		return config.DevFiles(root), nil
	}
	if path == "" {
		path = defaultConfigPath
	}
	cfg, err := config.LoadFiles(path)
	if err != nil {
		return config.Files{}, err
	}
	if dev {
		cfg.Dev = true
	}
	return cfg, nil
}

// panelUID returns the peer UID the panel connects with; -1 means "unset" and is only
// allowed in dev mode (then the service's own UID).
func panelUID(cfg config.Files) (uint32, error) {
	if cfg.PanelUID < 0 {
		if !cfg.Dev {
			return 0, errors.New("panel_uid must be set")
		}
		return uint32(os.Getuid()), nil
	}
	if int64(cfg.PanelUID) > math.MaxUint32 {
		return 0, fmt.Errorf("panel_uid %d out of range", cfg.PanelUID)
	}
	return uint32(cfg.PanelUID), nil
}

// listen prefers the socket systemd passed (spec §3.1: mcpanel-files.socket owns path, mode
// and group, so the service needs no membership in the panel's group); without socket
// activation it creates cfg.Socket itself with socket_mode and its own primary group.
func listen(cfg config.Files) (net.Listener, bool, error) {
	l, err := activatedListener(os.Getenv, os.Unsetenv, os.Getpid())
	if err != nil || l != nil {
		return l, l != nil, err
	}
	if cfg.Dev {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o750); err != nil {
			return nil, false, err
		}
	}
	l, err = unixsock.Listen(cfg.Socket, cfg.SocketFileMode(), -1)
	return l, false, err
}

// activatedListener returns the listener systemd passed as fd 3 when LISTEN_PID is this
// process and LISTEN_FDS is 1, otherwise nil. It always removes the activation variables, so
// they never leak into child processes.
func activatedListener(getenv func(string) string, unsetenv func(string) error, pid int) (net.Listener, error) {
	lpid, lfds := getenv("LISTEN_PID"), getenv("LISTEN_FDS")
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = unsetenv(k)
	}
	if lpid == "" && lfds == "" {
		return nil, nil
	}
	if p, err := strconv.Atoi(lpid); err != nil || p != pid {
		return nil, nil // meant for another process
	}
	if n, err := strconv.Atoi(lfds); err != nil || n != 1 {
		return nil, fmt.Errorf("socket activation: expected exactly one socket, got LISTEN_FDS=%q", lfds)
	}
	syscall.CloseOnExec(activationFD)
	f := os.NewFile(activationFD, "files.sock")
	l, err := net.FileListener(f)
	_ = f.Close() // FileListener holds its own duplicate
	if err != nil {
		return nil, fmt.Errorf("socket activation: %w", err)
	}
	if _, ok := l.(*net.UnixListener); !ok {
		_ = l.Close()
		return nil, errors.New("socket activation: fd 3 is not a Unix socket")
	}
	return l, nil
}

func newLogger(dev bool, w io.Writer) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}
```

Hinweis zu gosec: Der Socket-Modus kommt aus der Konfiguration (`cfg.SocketFileMode()`), gosec sollte dort nichts melden. Meldet die CI doch etwas (z. B. `G301` für `MkdirAll(…, 0o750)`), ist eine gezielte Regel in `.golangci.yml` (Pfad `cmd/files/` bzw. `internal/files/`, Linter `gosec`, Text `G30x`, mit Begründung als Kommentar) oder ein `//nolint:gosec // <Grund>` auf genau der Zeile erlaubt.

- [ ] **Step 7: `make check` – grün**

Run: `make check`
Expected: PASS (Format, Vet, Build macOS + Linux, tsc).

- [ ] **Step 8: Commit, Push, Draft-PR, CI**

```bash
git add internal/files cmd/files
git commit -m "feat(files): service skeleton with per-instance os.Root, socket server and cmd/files

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/files-service
gh pr create --draft --title "feat(files): mcpanel-files service" --body "Phase B1 of docs/superpowers/plans/2026-10-11-files-00-overview.md (Tasks 4–8).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
gh pr checks <pr> --watch
```
Expected: CI grün; `TestOpenInstance`, `TestLstatNeverFollowsComponents`, `TestCleanupTempRemovesLeftovers`, `TestReserve`, `TestStatusFor`, `TestServerHealthAndRouting`, `TestRunRefusesRoot`, `TestActivatedListener`, `TestPanelUID` PASS.

---

### Task 5: Liste, Lesen, Schreiben, Anlegen, Umbenennen, Löschen

**Files:**
- Create: `internal/files/ops.go`, `internal/files/redact.go`
- Modify: `internal/files/server.go` (Routen + Handler)
- Test: `internal/files/ops_test.go`, `internal/files/redact_test.go`, `internal/files/server_ops_test.go`

**Interfaces:**
- Consumes: Aufgabe 4 (`open`, `cleanVisible`, `lstat`, `requireDir`, `openRegular`, `parentOf`, `tempName`, `commit`, `fsErr`, `reserve`), `filesapi.PathRequest`, `ListResponse`, `ReadResponse`, `WriteRequest`, `WriteResponse`, `RenameRequest`, `Entry`, `TypeFile/TypeDir/TypeSymlink/TypeOther`.
- Produces:

```go
func (s *Service) List(ctx context.Context, id int64, path string) (filesapi.ListResponse, error)
func (s *Service) Read(ctx context.Context, id int64, path string) (filesapi.ReadResponse, error)
func (s *Service) Write(ctx context.Context, req filesapi.WriteRequest) (filesapi.WriteResponse, error)
func (s *Service) Mkdir(ctx context.Context, id int64, path string) error
func (s *Service) Rename(ctx context.Context, req filesapi.RenameRequest) error
func (s *Service) Delete(ctx context.Context, id int64, path string) error
// intern, auch von Aufgabe 6 benutzt:
func readSmall(root *os.Root, clean string, limit int64) ([]byte, fs.FileInfo, error)
func redactProperties(s string) string
func restorePassword(next, current string) string
const redacted = "********"
```
Routen: `POST /v1/list` (200 `ListResponse`), `/v1/read` (200 `ReadResponse`), `/v1/write` (200 `WriteResponse`, Body ≤ `MaxJSONBytes`), `/v1/mkdir` (201, leerer Body), `/v1/rename` (204), `/v1/delete` (204).

- [ ] **Step 1: Tests für Schwärzen (rot)**

`internal/files/redact_test.go`:

```go
package files

import "testing"

func TestRedactProperties(t *testing.T) {
	in := "motd=Hi\nrcon.password=s3cret\r\n  rcon.password : other\nrcon.password\tspaced\nrcon.passwordless=keep\nrcon.port=25575\nrcon.password=\n"
	want := "motd=Hi\nrcon.password=********\r\n  rcon.password : ********\nrcon.password\t********\nrcon.passwordless=keep\nrcon.port=25575\nrcon.password=\n"
	if got := redactProperties(in); got != want {
		t.Fatalf("redact:\n got %q\nwant %q", got, want)
	}
}

func TestRestorePassword(t *testing.T) {
	current := "rcon.password=s3cret\nmotd=old\n"
	next := "rcon.password=********\nmotd=new\n"
	if got := restorePassword(next, current); got != "rcon.password=s3cret\nmotd=new\n" {
		t.Fatalf("restore: %q", got)
	}
	// A deliberately changed value is kept.
	if got := restorePassword("rcon.password=mine\n", current); got != "rcon.password=mine\n" {
		t.Fatalf("changed value: %q", got)
	}
	// No password in the file: the placeholder line is dropped instead of setting "********".
	if got := restorePassword("rcon.password=********\nmotd=x\n", "motd=x\n"); got != "motd=x\n" {
		t.Fatalf("no current password: %q", got)
	}
	// The last definition wins, as in java.util.Properties.
	if got := restorePassword("rcon.password : ********", "rcon.password=a\nrcon.password=b\n"); got != "rcon.password : b" {
		t.Fatalf("last definition: %q", got)
	}
}
```

- [ ] **Step 2: Tests für die Operationen (rot)**

`internal/files/ops_test.go`:

```go
package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

var ctx = context.Background()

// layout creates a typical data directory plus a secret outside of it.
func layout(t *testing.T, data string) (outside string) {
	t.Helper()
	put(t, data, "server.properties", "motd=Hi\nrcon.password=s3cret\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")
	put(t, data, ".rcon-cli.yaml", "password: s3cret\n")
	put(t, data, ".mcpanel-tmp-dead", "partial")
	put(t, data, "plugins/EssentialsX.jar", "PK\x00\x01binary")
	put(t, data, "plugins/.rcon-cli.env", "nested, visible")
	put(t, data, "plugins/Essentials/config.yml", "spawn: true\n")
	put(t, data, "world/level.dat", "\x00\x01")
	if err := os.Symlink("world", filepath.Join(data, "current")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(data, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(filepath.Dir(filepath.Dir(data)), "secret.txt")
	if err := os.WriteFile(outside, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(data, "abs-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../secret.txt", filepath.Join(data, "rel-link")); err != nil {
		t.Fatal(err)
	}
	return outside
}

func TestListShowsTypesAndHidesReserved(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	res, err := s.List(ctx, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range res.Entries {
		got = append(got, e.Name+":"+string(e.Type))
	}
	want := "plugins:dir world:dir abs-link:symlink current:symlink pipe:other rel-link:symlink server.properties:file"
	if strings.Join(got, " ") != want {
		t.Fatalf("root listing:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if res.Truncated {
		t.Fatal("small folder reported as truncated")
	}
	for _, e := range res.Entries {
		if e.Name == "server.properties" && (e.Size != int64(len("motd=Hi\nrcon.password=s3cret\n")) || e.MTime <= 0) {
			t.Fatalf("file entry: %+v", e)
		}
		if e.Type == filesapi.TypeDir && e.Size != 0 {
			t.Fatalf("folder size must be 0: %+v", e)
		}
	}

	res, err = s.List(ctx, 1, "plugins")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 3 || res.Entries[0].Name != "Essentials" || res.Entries[1].Name != ".rcon-cli.env" {
		t.Fatalf("plugins listing: %+v", res.Entries)
	}

	for p, want := range map[string]error{
		"current":       filesapi.ErrNotRegular, // symlinked folder is never listed
		"missing":       filesapi.ErrNotFound,
		".rcon-cli.env": filesapi.ErrNotFound,
		"server.properties": filesapi.ErrNotRegular,
		"../x":          filesapi.ErrInvalid,
	} {
		if _, err := s.List(ctx, 1, p); !errors.Is(err, want) {
			t.Errorf("list %q: %v, want %v", p, err, want)
		}
	}
}

func TestListTruncates(t *testing.T) {
	s, data := newService(t)
	for i := range 60 {
		put(t, data, fmt.Sprintf("many/f%02d", i), "x")
	}
	res, err := s.List(ctx, 1, "many")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 50 || !res.Truncated {
		t.Fatalf("got %d entries truncated=%v, want 50 true", len(res.Entries), res.Truncated)
	}
}

func TestReadText(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	res, err := s.Read(ctx, 1, "plugins/Essentials/config.yml")
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(data, "plugins", "Essentials", "config.yml"))
	if res.Content != "spawn: true\n" || res.Size != 12 || res.MTime != fi.ModTime().UnixMilli() {
		t.Fatalf("read: %+v", res)
	}
	put(t, data, "big.txt", strings.Repeat("a", 64<<10+1))
	put(t, data, "latin1.txt", "caf\xe9")
	for p, want := range map[string]error{
		"plugins/EssentialsX.jar": filesapi.ErrNotText, // NUL byte
		"latin1.txt":              filesapi.ErrNotText,
		"big.txt":                 filesapi.ErrNotText,
		"":                        filesapi.ErrNotRegular,
	} {
		if _, err := s.Read(ctx, 1, p); !errors.Is(err, want) {
			t.Errorf("read %q: %v, want %v", p, err, want)
		}
	}
}

func TestReadRefusesUntrustedFiles(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	for p, want := range map[string]error{
		"abs-link":        filesapi.ErrNotRegular,
		"rel-link":        filesapi.ErrNotRegular,
		"current":         filesapi.ErrNotRegular,
		"current/level.dat": filesapi.ErrNotRegular,
		"pipe":            filesapi.ErrNotRegular,
		"world":           filesapi.ErrNotRegular,
		".rcon-cli.env":   filesapi.ErrNotFound,
		".rcon-cli.yaml":  filesapi.ErrNotFound,
		".mcpanel-tmp-dead": filesapi.ErrNotFound,
	} {
		done := make(chan error, 1)
		go func() {
			_, err := s.Read(ctx, 1, p)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, want) {
				t.Errorf("read %q: %v, want %v", p, err, want)
			}
			if err != nil && strings.Contains(err.Error(), "host secret") {
				t.Errorf("read %q leaked content: %v", p, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("read %q hangs", p)
		}
	}
}

func TestReadRedactsRconPassword(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	put(t, data, "plugins/server.properties", "rcon.password=nested\n")
	res, err := s.Read(ctx, 1, "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Content, "s3cret") || !strings.Contains(res.Content, "rcon.password=********") {
		t.Fatalf("not redacted: %q", res.Content)
	}
	res, err = s.Read(ctx, 1, "plugins/server.properties")
	if err != nil || res.Content != "rcon.password=nested\n" {
		t.Fatalf("only the top-level server.properties is redacted: %q %v", res.Content, err)
	}
}

func TestWriteCreatesAndDetectsConflicts(t *testing.T) {
	s, data := newService(t)
	res, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v, _ := get(t, data, "notes.txt"); v != "one" || res.Size != 3 {
		t.Fatalf("created %q %+v", v, res)
	}
	if fi, _ := os.Stat(filepath.Join(data, "notes.txt")); fi.Mode().Perm()&0o022 != 0 {
		t.Fatalf("new file mode %v is group/world writable", fi.Mode())
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "dup"}); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("create over existing: %v, want ErrExists", err)
	}
	m := res.MTime
	res2, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "two", ExpectedMTime: &m})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v, _ := get(t, data, "notes.txt"); v != "two" {
		t.Fatalf("updated %q", v)
	}
	stale := res2.MTime - 1000
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "notes.txt", Content: "three", ExpectedMTime: &stale}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("stale mtime: %v, want ErrModified", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "gone.txt", Content: "x", ExpectedMTime: &m}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("file vanished: %v, want ErrModified", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: strings.Repeat("a", 64<<10+1)}); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "missing/x.txt", Content: "x"}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("missing parent: %v", err)
	}
	for _, n := range names(t, data) {
		if strings.HasPrefix(n, tempPrefix) {
			t.Fatalf("temp file left behind: %s", n)
		}
	}
}

func TestWriteRefusesSymlinks(t *testing.T) {
	s, data := newService(t)
	outside := layout(t, data)
	fi, _ := os.Lstat(filepath.Join(data, "abs-link"))
	m := fi.ModTime().UnixMilli()
	for _, p := range []string{"abs-link", "rel-link", "current/new.txt", "pipe"} {
		_, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: p, Content: "pwned", ExpectedMTime: &m})
		if !errors.Is(err, filesapi.ErrNotRegular) {
			t.Errorf("write %q: %v, want ErrNotRegular", p, err)
		}
		if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: p, Content: "pwned"}); err == nil {
			t.Errorf("create %q succeeded", p)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "host secret" {
		t.Fatalf("file outside the data dir changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(data, "world", "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("write through a symlinked folder created a file: %v", err)
	}
}

func TestWriteKeepsRconPassword(t *testing.T) {
	s, data := newService(t)
	put(t, data, "server.properties", "rcon.password=s3cret\nmotd=old\n")
	r, err := s.Read(ctx, 1, "server.properties")
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(r.Content, "motd=old", "motd=new", 1)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "server.properties", Content: edited, ExpectedMTime: &r.MTime}); err != nil {
		t.Fatal(err)
	}
	if v, _ := get(t, data, "server.properties"); v != "rcon.password=s3cret\nmotd=new\n" {
		t.Fatalf("password lost: %q", v)
	}
}

func TestHiddenNamesAreNotFound(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	m := int64(1)
	for _, err := range []error{
		func() error { _, err := s.Read(ctx, 1, ".rcon-cli.env"); return err }(),
		func() error {
			_, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: ".rcon-cli.env", Content: "x", ExpectedMTime: &m})
			return err
		}(),
		s.Mkdir(ctx, 1, ".mcpanel-tmp-x"),
		s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: ".rcon-cli.env", To: "stolen.env"}),
		s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "server.properties", To: ".rcon-cli.yaml"}),
		s.Delete(ctx, 1, ".rcon-cli.yaml"),
	} {
		if !errors.Is(err, filesapi.ErrNotFound) {
			t.Errorf("hidden name: %v, want ErrNotFound", err)
		}
	}
	if v, ok := get(t, data, ".rcon-cli.env"); !ok || v != "password=s3cret\n" {
		t.Fatal(".rcon-cli.env was touched")
	}
}

func TestMkdirRenameDelete(t *testing.T) {
	s, data := newService(t)
	outside := layout(t, data)

	if err := s.Mkdir(ctx, 1, "plugins/new"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, 1, "plugins/new"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("mkdir twice: %v", err)
	}
	if err := s.Mkdir(ctx, 1, "nope/new"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("mkdir without parent: %v", err)
	}
	if err := s.Mkdir(ctx, 1, ""); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := s.Mkdir(ctx, 1, "current/new"); !errors.Is(err, filesapi.ErrNotRegular) {
		t.Fatalf("mkdir through symlink: %v", err)
	}

	if err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "plugins/Essentials/config.yml", To: "plugins/new/config.yml"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := get(t, data, "plugins/new/config.yml"); !ok {
		t.Fatal("rename did not move the file")
	}
	for req, want := range map[filesapi.RenameRequest]error{
		{ID: 1, From: "plugins/new/config.yml", To: "server.properties"}: filesapi.ErrExists,
		{ID: 1, From: "plugins", To: "plugins/inner"}:                   filesapi.ErrInvalid,
		{ID: 1, From: "plugins", To: "plugins"}:                         filesapi.ErrInvalid,
		{ID: 1, From: "missing", To: "x"}:                               filesapi.ErrNotFound,
		{ID: 1, From: "server.properties", To: "nope/x"}:                filesapi.ErrNotFound,
		{ID: 1, From: "server.properties", To: "current/x"}:             filesapi.ErrNotRegular,
		{ID: 1, From: "", To: "x"}:                                      filesapi.ErrInvalid,
	} {
		if err := s.Rename(ctx, req); !errors.Is(err, want) {
			t.Errorf("rename %+v: %v, want %v", req, err, want)
		}
	}
	// Renaming a symlink moves the link, not its target.
	if err := s.Rename(ctx, filesapi.RenameRequest{ID: 1, From: "abs-link", To: "moved-link"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(data, "moved-link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link not moved as a link: %v", err)
	}

	if err := s.Delete(ctx, 1, "plugins"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "plugins")); !os.IsNotExist(err) {
		t.Fatal("folder not deleted recursively")
	}
	if err := s.Delete(ctx, 1, "moved-link"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "host secret" {
		t.Fatalf("deleting a symlink touched its target: %v", err)
	}
	if err := s.Delete(ctx, 1, "current"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "world", "level.dat")); err != nil {
		t.Fatal("deleting a symlinked folder deleted the real folder's content")
	}
	if err := s.Delete(ctx, 1, ""); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("delete root: %v", err)
	}
	if err := s.Delete(ctx, 1, "missing"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
```

`internal/files/server_ops_test.go`:

```go
package files

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerOps(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "hello")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	status, body := postJSON(t, ts, filesapi.PathList, filesapi.PathRequest{ID: 1, Path: ""})
	var list filesapi.ListResponse
	if status != http.StatusOK || json.Unmarshal(body, &list) != nil || len(list.Entries) != 1 {
		t.Fatalf("list: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathRead, filesapi.PathRequest{ID: 1, Path: "a.txt"})
	var read filesapi.ReadResponse
	if status != http.StatusOK || json.Unmarshal(body, &read) != nil || read.Content != "hello" {
		t.Fatalf("read: %d %s", status, body)
	}
	stale := read.MTime - 5
	status, body = postJSON(t, ts, filesapi.PathWrite, filesapi.WriteRequest{ID: 1, Path: "a.txt", Content: "x", ExpectedMTime: &stale})
	if status != http.StatusConflict || errCode(t, body) != filesapi.CodeModified {
		t.Fatalf("stale write: %d %s", status, body)
	}
	if status, _ := postJSON(t, ts, filesapi.PathMkdir, filesapi.PathRequest{ID: 1, Path: "dir"}); status != http.StatusCreated {
		t.Fatalf("mkdir: %d", status)
	}
	if status, _ := postJSON(t, ts, filesapi.PathRename, filesapi.RenameRequest{ID: 1, From: "a.txt", To: "dir/a.txt"}); status != http.StatusNoContent {
		t.Fatalf("rename: %d", status)
	}
	if status, _ := postJSON(t, ts, filesapi.PathDelete, filesapi.PathRequest{ID: 1, Path: "dir"}); status != http.StatusNoContent {
		t.Fatalf("delete: %d", status)
	}
	status, body = postJSON(t, ts, filesapi.PathRead, filesapi.PathRequest{ID: 1, Path: "a.txt"})
	if status != http.StatusNotFound || errCode(t, body) != filesapi.CodeNotFound {
		t.Fatalf("read deleted: %d %s", status, body)
	}

	// Strict decoding and limits.
	status, body = postJSON(t, ts, filesapi.PathList, map[string]any{"id": 1, "path": "", "extra": true})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("unknown field: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathList, filesapi.PathRequest{ID: 0})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("id 0: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathList, filesapi.PathRequest{ID: 1, Path: strings.Repeat("a", 70<<10)})
	if status != http.StatusRequestEntityTooLarge || errCode(t, body) != filesapi.CodeTooLarge {
		t.Fatalf("oversized body: %d %s", status, body)
	}
	resp, err := http.Post(ts.URL+filesapi.PathList, "text/plain", strings.NewReader(`{"id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: %d", resp.StatusCode)
	}
	// A large editor save fits (write allows MaxJSONBytes).
	status, body = postJSON(t, ts, filesapi.PathWrite, filesapi.WriteRequest{ID: 1, Path: "big.txt", Content: strings.Repeat("b", 60<<10)})
	if status != http.StatusOK {
		t.Fatalf("large write: %d %s", status, body)
	}
}
```

- [ ] **Step 3: `make check` – rot**

Run: `git add -N internal/files && make check`
Expected: FAIL (`s.List undefined`, `redactProperties undefined`, …).

- [ ] **Step 4: Schwärzen implementieren**

`internal/files/redact.go`:

```go
package files

import (
	"regexp"
	"strings"
)

// redacted replaces the RCON password in server.properties (spec §3.4).
const redacted = "********"

// passwordLine matches a java.util.Properties line defining rcon.password: key, then "=" or
// ":" with optional blanks, or only blanks. Group 1 is everything up to the value.
var passwordLine = regexp.MustCompile(`^([ \t\f]*rcon\.password(?:[ \t\f]*[=:][ \t\f]*|[ \t\f]+))(.*)$`)

// mapPasswordLines calls f for every rcon.password line (without its line ending); f returns
// the replacement line and whether to keep the line at all. Other lines stay untouched.
func mapPasswordLines(s string, f func(prefix, value string) (string, bool)) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, line := range strings.SplitAfter(s, "\n") {
		body := strings.TrimRight(line, "\r\n")
		end := line[len(body):]
		m := passwordLine.FindStringSubmatch(body)
		if m == nil {
			b.WriteString(line)
			continue
		}
		if out, keep := f(m[1], m[2]); keep {
			b.WriteString(out)
			b.WriteString(end)
		}
	}
	return b.String()
}

// redactProperties hides every non-empty rcon.password value.
func redactProperties(s string) string {
	return mapPasswordLines(s, func(prefix, value string) (string, bool) {
		if value == "" {
			return prefix, true
		}
		return prefix + redacted, true
	})
}

// currentPassword returns the value of the last rcon.password line (last definition wins).
func currentPassword(s string) (string, bool) {
	var value string
	found := false
	mapPasswordLines(s, func(prefix, v string) (string, bool) {
		value, found = v, true
		return prefix + v, true
	})
	return value, found
}

// restorePassword puts the current password back into lines that still carry the redacted
// placeholder, so saving server.properties in the editor never sets the password to
// "********". Without a current password such a line is dropped.
func restorePassword(next, current string) string {
	cur, ok := currentPassword(current)
	return mapPasswordLines(next, func(prefix, value string) (string, bool) {
		if value != redacted {
			return prefix + value, true
		}
		if !ok {
			return "", false
		}
		return prefix + cur, true
	})
}
```

- [ ] **Step 5: Operationen implementieren**

`internal/files/ops.go`:

```go
package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const listBatch = 256

func entryType(m fs.FileMode) filesapi.EntryType {
	switch {
	case m.IsDir():
		return filesapi.TypeDir
	case m.IsRegular():
		return filesapi.TypeFile
	case m&fs.ModeSymlink != 0:
		return filesapi.TypeSymlink
	}
	return filesapi.TypeOther
}

// List lists a folder: hidden names left out, at most max_list_entries entries.
func (s *Service) List(ctx context.Context, id int64, p string) (filesapi.ListResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.ListResponse{}, err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return filesapi.ListResponse{}, err
	}
	root, err := s.open(id)
	if err != nil {
		return filesapi.ListResponse{}, err
	}
	defer root.Close()
	fi, err := lstat(root, clean)
	if err != nil {
		return filesapi.ListResponse{}, err
	}
	if !fi.IsDir() {
		return filesapi.ListResponse{}, fmt.Errorf("%w: %s is not a folder", filesapi.ErrNotRegular, rootName(clean))
	}
	d, err := root.Open(rootName(clean))
	if err != nil {
		return filesapi.ListResponse{}, fsErr(err)
	}
	defer d.Close()
	if st, err := d.Stat(); err != nil || !os.SameFile(fi, st) {
		return filesapi.ListResponse{}, fmt.Errorf("%w: %s changed while it was opened", filesapi.ErrNotRegular, rootName(clean))
	}

	limit := s.cfg.Limits.MaxListEntries
	res := filesapi.ListResponse{Entries: []filesapi.Entry{}}
	for !res.Truncated {
		batch, err := d.ReadDir(listBatch)
		for _, e := range batch {
			if filesapi.IsHidden(filesapi.Join(clean, e.Name())) {
				continue
			}
			if len(res.Entries) >= limit {
				res.Truncated = true
				break
			}
			info, ierr := e.Info()
			if ierr != nil {
				continue // vanished meanwhile
			}
			ent := filesapi.Entry{Name: e.Name(), Type: entryType(info.Mode()), MTime: info.ModTime().UnixMilli()}
			if ent.Type == filesapi.TypeFile {
				ent.Size = info.Size()
			}
			res.Entries = append(res.Entries, ent)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return filesapi.ListResponse{}, fsErr(err)
		}
	}
	sort.Slice(res.Entries, func(i, j int) bool {
		a, b := res.Entries[i], res.Entries[j]
		if (a.Type == filesapi.TypeDir) != (b.Type == filesapi.TypeDir) {
			return a.Type == filesapi.TypeDir
		}
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
	return res, nil
}

// readSmall reads a regular file of at most limit bytes (ErrTooLarge otherwise).
func readSmall(root *os.Root, clean string, limit int64) ([]byte, fs.FileInfo, error) {
	f, st, err := openRegular(root, clean, os.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if st.Size() > limit {
		return nil, st, fmt.Errorf("%w: %s is larger than %d bytes", filesapi.ErrTooLarge, clean, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, st, fsErr(err)
	}
	if int64(len(data)) > limit {
		return nil, st, fmt.Errorf("%w: %s is larger than %d bytes", filesapi.ErrTooLarge, clean, limit)
	}
	return data, st, nil
}

// Read returns a text file for the editor (spec §4: ≤ max_text_bytes, UTF-8, no NUL).
func (s *Service) Read(ctx context.Context, id int64, p string) (filesapi.ReadResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.ReadResponse{}, err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return filesapi.ReadResponse{}, err
	}
	if clean == "" {
		return filesapi.ReadResponse{}, fmt.Errorf("%w: the root is a folder", filesapi.ErrNotRegular)
	}
	root, err := s.open(id)
	if err != nil {
		return filesapi.ReadResponse{}, err
	}
	defer root.Close()
	data, st, err := readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
	if errors.Is(err, filesapi.ErrTooLarge) {
		return filesapi.ReadResponse{}, fmt.Errorf("%w: %s is too large for the editor", filesapi.ErrNotText, clean)
	}
	if err != nil {
		return filesapi.ReadResponse{}, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return filesapi.ReadResponse{}, fmt.Errorf("%w: %s is not a text file", filesapi.ErrNotText, clean)
	}
	content := string(data)
	if clean == serverProps {
		content = redactProperties(content)
	}
	return filesapi.ReadResponse{Content: content, MTime: st.ModTime().UnixMilli(), Size: st.Size()}, nil
}

// Write saves editor content atomically (temp file, fsync, rename/link) with an mtime check.
func (s *Service) Write(ctx context.Context, req filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.WriteResponse{}, err
	}
	clean, err := cleanVisible(req.Path)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	if clean == "" {
		return filesapi.WriteResponse{}, fmt.Errorf("%w: the root is a folder", filesapi.ErrNotRegular)
	}
	if int64(len(req.Content)) > s.cfg.Limits.MaxTextBytes {
		return filesapi.WriteResponse{}, fmt.Errorf("%w: content exceeds %d bytes", filesapi.ErrTooLarge, s.cfg.Limits.MaxTextBytes)
	}
	root, err := s.open(req.ID)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return filesapi.WriteResponse{}, err
	}

	content := req.Content
	fi, err := lstat(root, clean)
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		if req.ExpectedMTime != nil {
			return filesapi.WriteResponse{}, fmt.Errorf("%w: %s was deleted meanwhile", filesapi.ErrModified, clean)
		}
	case err != nil:
		return filesapi.WriteResponse{}, err
	case !fi.Mode().IsRegular():
		return filesapi.WriteResponse{}, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case req.ExpectedMTime == nil:
		return filesapi.WriteResponse{}, fmt.Errorf("%w: %s", filesapi.ErrExists, clean)
	case *req.ExpectedMTime != fi.ModTime().UnixMilli():
		return filesapi.WriteResponse{}, fmt.Errorf("%w: %s was changed meanwhile", filesapi.ErrModified, clean)
	case clean == serverProps:
		cur, _, rerr := readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
		if rerr != nil && !errors.Is(rerr, filesapi.ErrTooLarge) {
			return filesapi.WriteResponse{}, rerr
		}
		content = restorePassword(content, string(cur))
	}
	if err := s.reserve(req.ID, int64(len(content))); err != nil {
		return filesapi.WriteResponse{}, err
	}
	if err := writeAtomic(root, clean, []byte(content), req.ExpectedMTime != nil); err != nil {
		return filesapi.WriteResponse{}, err
	}
	st, err := lstat(root, clean)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	return filesapi.WriteResponse{MTime: st.ModTime().UnixMilli(), Size: st.Size()}, nil
}

// writeAtomic writes data to a temp file next to clean, syncs it and commits it.
func writeAtomic(root *os.Root, clean string, data []byte, overwrite bool) error {
	tmp, err := tempName(parentOf(clean))
	if err != nil {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new temp file in the instance's os.Root, 0644 like the server's own files
	if err != nil {
		return fsErr(err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = commit(root, tmp, clean, overwrite)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return fsErr(err)
	}
	return nil
}

// Mkdir creates one folder (the parent must exist).
func (s *Service) Mkdir(ctx context.Context, id int64, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("%w: the root exists", filesapi.ErrInvalid)
	}
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return err
	}
	return fsErr(root.Mkdir(clean, dirMode)) //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
}

// Rename moves a file, folder or symlink (the link itself); the target must not exist.
func (s *Service) Rename(ctx context.Context, req filesapi.RenameRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	from, err := cleanVisible(req.From)
	if err != nil {
		return err
	}
	to, err := cleanVisible(req.To)
	if err != nil {
		return err
	}
	if from == "" || to == "" {
		return fmt.Errorf("%w: the root cannot be renamed", filesapi.ErrInvalid)
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("%w: %s cannot be moved into itself", filesapi.ErrInvalid, from)
	}
	root, err := s.open(req.ID)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := lstat(root, from); err != nil {
		return err
	}
	if err := requireDir(root, parentOf(to)); err != nil {
		return err
	}
	if _, err := lstat(root, to); err == nil {
		return fmt.Errorf("%w: %s", filesapi.ErrExists, to)
	} else if !errors.Is(err, filesapi.ErrNotFound) {
		return err
	}
	return fsErr(root.Rename(from, to))
}

// Delete removes a file, symlink (the link itself) or folder with its content.
func (s *Service) Delete(ctx context.Context, id int64, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("%w: the root cannot be deleted", filesapi.ErrInvalid)
	}
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := lstat(root, clean); err != nil {
		return err
	}
	return fsErr(root.RemoveAll(clean))
}
```

- [ ] **Step 6: Routen und Handler ergänzen**

In `internal/files/server.go` die Routen-Tabelle in `NewServer` ersetzen durch:

```go
	s.routes = map[string]route{
		filesapi.PathHealth: {method: http.MethodGet, handler: s.health},
		filesapi.PathList:   {method: http.MethodPost, handler: s.list},
		filesapi.PathRead:   {method: http.MethodPost, handler: s.read},
		filesapi.PathWrite:  {method: http.MethodPost, handler: s.write},
		filesapi.PathMkdir:  {method: http.MethodPost, handler: s.mkdir},
		filesapi.PathRename: {method: http.MethodPost, handler: s.rename},
		filesapi.PathDelete: {method: http.MethodPost, handler: s.delete},
	}
```

und am Ende von `server.go` anfügen:

```go
func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	res, err := s.svc.List(r.Context(), req.ID, req.Path)
	if err != nil {
		s.fail(w, r, "list", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	res, err := s.svc.Read(r.Context(), req.ID, req.Path)
	if err != nil {
		s.fail(w, r, "read", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) write(w http.ResponseWriter, r *http.Request) {
	var req filesapi.WriteRequest
	if !s.decode(w, r, &req, filesapi.MaxJSONBytes) {
		return
	}
	res, err := s.svc.Write(r.Context(), req)
	if err != nil {
		s.fail(w, r, "write", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.Mkdir(r.Context(), req.ID, req.Path); err != nil {
		s.fail(w, r, "mkdir", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) rename(w http.ResponseWriter, r *http.Request) {
	var req filesapi.RenameRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.Rename(r.Context(), req); err != nil {
		s.fail(w, r, "rename", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PathRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.Delete(r.Context(), req.ID, req.Path); err != nil {
		s.fail(w, r, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 7: `make check` – grün, Commit, CI**

```bash
make check
git add internal/files
git commit -m "feat(files): list, read, atomic write, mkdir, rename and delete without following symlinks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch
```
Expected: CI grün; `TestRedactProperties`, `TestRestorePassword`, `TestListShowsTypesAndHidesReserved`, `TestListTruncates`, `TestReadText`, `TestReadRefusesUntrustedFiles`, `TestReadRedactsRconPassword`, `TestWriteCreatesAndDetectsConflicts`, `TestWriteRefusesSymlinks`, `TestWriteKeepsRconPassword`, `TestHiddenNamesAreNotFound`, `TestMkdirRenameDelete`, `TestServerOps` PASS.

---

### Task 6: Upload und Download (Datei und Ordner-Zip)

**Files:**
- Create: `internal/files/transfer.go`, `internal/files/zipdir.go`
- Modify: `internal/files/server.go` (Routen `upload`, `download`)
- Test: `internal/files/transfer_test.go`, `internal/files/server_transfer_test.go`

**Interfaces:**
- Consumes: Aufgaben 4–5 (`open`, `cleanVisible`, `lstat`, `requireDir`, `openRegular`, `readSmall`, `redactProperties`, `tempName`, `commit`, `reserve`, `uploads`, `deadlineReader`, `deadlineWriter`), `filesapi.UploadResponse`, `filesapi.HeaderKind`.
- Produces:

```go
func (s *Service) Upload(ctx context.Context, id int64, path string, overwrite bool, size int64, body io.Reader) (filesapi.UploadResponse, error)
// Download calls start exactly once before the first byte (isDir: zip stream, size -1).
func (s *Service) Download(ctx context.Context, id int64, path string, start func(isDir bool, size int64), w io.Writer) error
func writeDirZip(ctx context.Context, root *os.Root, dir string, w io.Writer, maxSmall int64) error
```
Routen: `PUT /v1/upload?id=&path=&overwrite=0|1` (201 `UploadResponse`), `GET /v1/download?id=&path=` (200 Stream, `X-Files-Kind`, bei Dateien `Content-Length`).

- [ ] **Step 1: Tests (rot)**

`internal/files/transfer_test.go`:

```go
package files

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func upload(s *Service, path string, overwrite bool, content string) (filesapi.UploadResponse, error) {
	return s.Upload(ctx, 1, path, overwrite, int64(len(content)), strings.NewReader(content))
}

func TestUploadWritesAtomically(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/old.jar", "old")
	res, err := upload(s, "plugins/new.jar", false, "jar bytes")
	if err != nil || res.Size != 9 {
		t.Fatalf("upload: %+v %v", res, err)
	}
	if v, _ := get(t, data, "plugins/new.jar"); v != "jar bytes" {
		t.Fatalf("content %q", v)
	}
	if _, err := upload(s, "plugins/old.jar", false, "new"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("exists without overwrite: %v", err)
	}
	if v, _ := get(t, data, "plugins/old.jar"); v != "old" {
		t.Fatal("refused upload changed the file")
	}
	if _, err := upload(s, "plugins/old.jar", true, "new"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if v, _ := get(t, data, "plugins/old.jar"); v != "new" {
		t.Fatalf("overwrite content %q", v)
	}
	// Body shorter than Content-Length (client aborted): no file, no temp file.
	if _, err := s.Upload(ctx, 1, "short.bin", false, 10, strings.NewReader("abc")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("short body: %v", err)
	}
	if _, ok := get(t, data, "short.bin"); ok {
		t.Fatal("partial upload became visible")
	}
	for _, n := range append(names(t, data), names(t, filepath.Join(data, "plugins"))...) {
		if strings.HasPrefix(n, tempPrefix) {
			t.Fatalf("temp file left behind: %s", n)
		}
	}
}

func TestUploadLimits(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	outside := layout(t, data)
	for name, c := range map[string]struct {
		path string
		size int64
		want error
	}{
		"no length":     {"a.bin", -1, filesapi.ErrInvalid},
		"too large":     {"a.bin", 1<<20 + 1, filesapi.ErrTooLarge},
		"root":          {"", 1, filesapi.ErrInvalid},
		"hidden":        {".rcon-cli.env", 1, filesapi.ErrNotFound},
		"missing dir":   {"nope/a.bin", 1, filesapi.ErrNotFound},
		"symlink dir":   {"current/a.bin", 1, filesapi.ErrNotRegular},
		"symlink file":  {"abs-link", 1, filesapi.ErrNotRegular},
		"fifo":          {"pipe", 1, filesapi.ErrNotRegular},
		"onto a folder": {"world", 1, filesapi.ErrNotRegular},
	} {
		if _, err := s.Upload(ctx, 1, c.path, true, c.size, strings.NewReader("x")); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "host secret" {
		t.Fatal("upload changed a file outside the data dir")
	}
	disk.set(2 << 10)
	if _, err := upload(s, "big.bin", false, strings.Repeat("x", 1500)); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("reserve: %v", err)
	}
}

// blockingReader blocks until released, then reports EOF.
type blockingReader struct{ release chan struct{} }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.release
	return 0, io.EOF
}

func TestUploadBusy(t *testing.T) {
	s, _ := newService(t)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := range maxUploads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Upload(ctx, 1, "slow"+string(rune('a'+i)), false, 5, blockingReader{release})
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.uploads) < maxUploads {
		if time.Now().After(deadline) {
			t.Fatal("uploads did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := upload(s, "fifth", false, "x"); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("fifth upload: %v, want ErrBusy", err)
	}
	close(release)
	wg.Wait()
	if _, err := upload(s, "after", false, "x"); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
}

// zeroReader yields n zero bytes.
type zeroReader struct{ n int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.n == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.n {
		p = p[:z.n]
	}
	clear(p)
	z.n -= int64(len(p))
	return len(p), nil
}

func TestUploadStreamsWithoutBuffering(t *testing.T) {
	const size = 64 << 20
	s, data := newService(t, func(c *Config) { c.Limits.MaxUploadBytes = size })
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if _, err := s.Upload(ctx, 1, "world.zip", false, size, &zeroReader{n: size}); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
		t.Fatalf("a %d MiB upload allocated %d MiB", size>>20, grew>>20)
	}
	if fi, err := os.Stat(filepath.Join(data, "world.zip")); err != nil || fi.Size() != size {
		t.Fatalf("stored size: %v", err)
	}
}

type download struct {
	started int
	isDir   bool
	size    int64
	body    bytes.Buffer
}

func (d *download) start(isDir bool, size int64) {
	d.started++
	d.isDir, d.size = isDir, size
}

func TestDownloadFile(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	var d download
	if err := s.Download(ctx, 1, "plugins/Essentials/config.yml", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	if d.started != 1 || d.isDir || d.size != 12 || d.body.String() != "spawn: true\n" {
		t.Fatalf("download: %+v %q", d, d.body.String())
	}
	for p, want := range map[string]error{
		"abs-link": filesapi.ErrNotRegular, "pipe": filesapi.ErrNotRegular, "current": filesapi.ErrNotRegular,
		".rcon-cli.env": filesapi.ErrNotFound, "missing": filesapi.ErrNotFound,
	} {
		var d download
		if err := s.Download(ctx, 1, p, d.start, &d.body); !errors.Is(err, want) || d.started != 0 {
			t.Errorf("download %q: %v started=%d, want %v before start", p, err, d.started, want)
		}
	}
}

func TestDownloadRedacts(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	var d download
	if err := s.Download(ctx, 1, "server.properties", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.body.String(), "s3cret") || d.size != int64(d.body.Len()) {
		t.Fatalf("download of server.properties: size %d body %q", d.size, d.body.String())
	}
}

func zipNames(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(c)
	}
	return out
}

func TestDownloadDirZip(t *testing.T) {
	s, data := newService(t)
	layout(t, data)
	put(t, data, "world/region/r.0.0.mca", "region")
	put(t, data, "world/.mcpanel-tmp-x/partial", "temp")
	if err := os.Symlink("../../secret.txt", filepath.Join(data, "world", "escape")); err != nil {
		t.Fatal(err)
	}

	var d download
	if err := s.Download(ctx, 1, "world", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	if !d.isDir || d.size != -1 {
		t.Fatalf("folder download header: %+v", d)
	}
	got := zipNames(t, d.body.Bytes())
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, " ") != "world/ world/level.dat world/region/ world/region/r.0.0.mca" {
		t.Fatalf("zip entries: %v", keys)
	}

	d = download{}
	if err := s.Download(ctx, 1, "", d.start, &d.body); err != nil {
		t.Fatal(err)
	}
	got = zipNames(t, d.body.Bytes())
	for name, content := range got {
		if strings.Contains(content, "s3cret") || strings.Contains(content, "host secret") {
			t.Fatalf("zip leaks a secret in %s", name)
		}
		if strings.HasPrefix(name, ".rcon-cli") || strings.Contains(name, tempPrefix) || name == "abs-link" || name == "pipe" || name == "current" {
			t.Fatalf("zip contains %s", name)
		}
	}
	if !strings.Contains(got["server.properties"], "rcon.password=********") || got["plugins/.rcon-cli.env"] != "nested, visible" {
		t.Fatalf("root zip: %v", got)
	}
}
```

`internal/files/server_transfer_test.go`:

```go
package files

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerUploadDownload(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/level.dat", "lvl")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	q := url.Values{"id": {"1"}, "path": {"plugins.jar"}, "overwrite": {"0"}}
	req, _ := http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), strings.NewReader("jar"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"size":3`) {
		t.Fatalf("upload: %d %s", resp.StatusCode, body)
	}
	// Same again without overwrite: 409 exists.
	req, _ = http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), strings.NewReader("jar"))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || errCode(t, body) != filesapi.CodeExists {
		t.Fatalf("second upload: %d %s", resp.StatusCode, body)
	}
	// Chunked body (no Content-Length): 400 invalid.
	pr, pw := io.Pipe()
	go func() { _, _ = pw.Write([]byte("x")); _ = pw.Close() }()
	q.Set("path", "chunked.bin")
	req, _ = http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), pr)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("chunked upload: %d %s", resp.StatusCode, body)
	}

	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=plugins.jar")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "jar" || resp.Header.Get(filesapi.HeaderKind) != "file" || resp.ContentLength != 3 {
		t.Fatalf("download file: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=world")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(filesapi.HeaderKind) != "dir" || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download dir: %d %v", resp.StatusCode, resp.Header)
	}
	if got := zipNames(t, body); got["world/level.dat"] != "lvl" {
		t.Fatalf("dir zip: %v", got)
	}
	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=1&path=missing")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || errCode(t, body) != filesapi.CodeNotFound {
		t.Fatalf("download missing: %d %s", resp.StatusCode, body)
	}
	resp, err = http.Get(ts.URL + filesapi.PathDownload + "?id=x&path=a")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad id: %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: `make check` – rot**

Run: `git add -N internal/files && make check`
Expected: FAIL (`s.Upload undefined`, `s.Download undefined`).

- [ ] **Step 3: Implementieren**

`internal/files/transfer.go`:

```go
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Upload stores body (exactly size bytes) at path via a temp file next to it; the target
// only changes when every byte arrived. At most maxUploads run at the same time.
func (s *Service) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (filesapi.UploadResponse, error) {
	if size < 0 {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: Content-Length is required", filesapi.ErrInvalid)
	}
	if size > s.cfg.Limits.MaxUploadBytes {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: upload exceeds %d bytes", filesapi.ErrTooLarge, s.cfg.Limits.MaxUploadBytes)
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	if clean == "" {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: a file name is required", filesapi.ErrInvalid)
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		return filesapi.UploadResponse{}, fmt.Errorf("%w: too many uploads at the same time", filesapi.ErrBusy)
	}
	root, err := s.open(id)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return filesapi.UploadResponse{}, err
	}
	switch fi, err := lstat(root, clean); {
	case errors.Is(err, filesapi.ErrNotFound):
	case err != nil:
		return filesapi.UploadResponse{}, err
	case !fi.Mode().IsRegular():
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case !overwrite:
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s", filesapi.ErrExists, clean)
	}
	if err := s.reserve(id, size); err != nil {
		return filesapi.UploadResponse{}, err
	}

	tmp, err := tempName(parentOf(clean))
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new temp file in the instance's os.Root, 0644 like the server's own files
	if err != nil {
		return filesapi.UploadResponse{}, fsErr(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(tmp)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(body, size+1))
	if err == nil && n != size {
		err = fmt.Errorf("%w: received %d bytes, Content-Length announced %d", filesapi.ErrInvalid, n, size)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return filesapi.UploadResponse{}, fsErr(err)
	}
	if err := commit(root, tmp, clean, overwrite); err != nil {
		return filesapi.UploadResponse{}, err
	}
	committed = true
	return filesapi.UploadResponse{Size: n}, nil
}

// Download streams a file (exactly its size at open time) or a folder as zip. start is called
// once before the first byte; an error before start can still be answered as JSON.
func (s *Service) Download(ctx context.Context, id int64, p string, start func(isDir bool, size int64), w io.Writer) error {
	clean, err := cleanVisible(p)
	if err != nil {
		return err
	}
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	fi, err := lstat(root, clean)
	if err != nil {
		return err
	}
	switch {
	case fi.IsDir():
		start(true, -1)
		return writeDirZip(ctx, root, clean, w, s.cfg.Limits.MaxTextBytes)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case clean == serverProps:
		data, _, err := readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
		if err != nil {
			return err
		}
		red := redactProperties(string(data))
		start(false, int64(len(red)))
		_, err = io.Copy(w, strings.NewReader(red))
		return err
	}
	f, st, err := openRegular(root, clean, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	start(false, st.Size())
	_, err = io.CopyN(w, f, st.Size()) // Content-Length is fixed: a file that shrank aborts the response
	return err
}
```

`internal/files/zipdir.go`:

```go
package files

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// writeDirZip streams folder dir (root-relative, "" = everything) as a zip without a temp
// file. Entries are named relative to dir's parent, so the folder itself is the top level.
// Symlinks, FIFOs, devices, sockets and hidden names are left out; server.properties at the
// top level is redacted.
func writeDirZip(ctx context.Context, root *os.Root, dir string, w io.Writer, maxSmall int64) error {
	zw := zip.NewWriter(w)
	strip := parentOf(dir)
	err := fs.WalkDir(root.FS(), rootName(dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p == "." {
			return nil
		}
		if filesapi.IsHidden(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := p
		if strip != "" {
			name = strings.TrimPrefix(p, strip+"/")
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // vanished during the walk
		}
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Method: zip.Store, Modified: info.ModTime()})
			return err
		case d.Type().IsRegular():
			return addZipFile(zw, root, p, name, info, maxSmall)
		}
		return nil // symlink, FIFO, device, socket
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func addZipFile(zw *zip.Writer, root *os.Root, p, name string, info fs.FileInfo, maxSmall int64) error {
	var src io.Reader
	if p == serverProps {
		data, _, err := readSmall(root, p, maxSmall)
		if err != nil {
			return err
		}
		src = strings.NewReader(redactProperties(string(data)))
	} else {
		f, _, err := openRegular(root, p, os.O_RDONLY)
		if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
			return nil // removed or swapped during the walk: left out
		}
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: info.ModTime()})
	if err != nil {
		return err
	}
	_, err = io.Copy(zf, src)
	return err
}
```

In `internal/files/server.go` die Routen-Tabelle um

```go
		filesapi.PathUpload:   {method: http.MethodPut, handler: s.upload},
		filesapi.PathDownload: {method: http.MethodGet, handler: s.download},
```

ergänzen und anfügen:

```go
// upload: PUT ?id=&path=&overwrite=0|1, body = file. The server-wide deadlines do not apply;
// every read gets ioTimeout.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, err := parseID(q.Get("id"))
	if err != nil {
		s.fail(w, r, "upload", err)
		return
	}
	ow := q.Get("overwrite")
	if ow != "" && ow != "0" && ow != "1" {
		s.fail(w, r, "upload", fmt.Errorf("%w: overwrite must be 0 or 1", filesapi.ErrInvalid))
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	body := &deadlineReader{r: r.Body, rc: rc, now: s.io.Now}
	res, err := s.svc.Upload(r.Context(), id, q.Get("path"), ow == "1", r.ContentLength, body)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
	if err != nil {
		s.fail(w, r, "upload", err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// download: GET ?id=&path=. Errors after the first byte abort the response (the client must
// not take a cut file for a complete one).
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, err := parseID(q.Get("id"))
	if err != nil {
		s.fail(w, r, "download", err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	started := false
	start := func(isDir bool, size int64) {
		started = true
		h := w.Header()
		if isDir {
			h.Set(filesapi.HeaderKind, "dir")
			h.Set("Content-Type", "application/zip")
		} else {
			h.Set(filesapi.HeaderKind, "file")
			h.Set("Content-Type", "application/octet-stream")
			h.Set("Content-Length", strconv.FormatInt(size, 10))
		}
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		w.WriteHeader(http.StatusOK)
	}
	err = s.svc.Download(r.Context(), id, q.Get("path"), start, &deadlineWriter{w: w, rc: rc, now: s.io.Now})
	if err == nil {
		return
	}
	if !started {
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		s.fail(w, r, "download", err)
		return
	}
	if r.Context().Err() == nil {
		s.log.Warn("files: download aborted", "path", q.Get("path"), "id", id, "err", err)
	}
	panic(http.ErrAbortHandler)
}
```

- [ ] **Step 4: `make check` – grün, Commit, CI**

```bash
make check
git add internal/files
git commit -m "feat(files): streaming upload and download with folder zip, redaction and disk reserve

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch
```
Expected: CI grün; `TestUploadWritesAtomically`, `TestUploadLimits`, `TestUploadBusy`, `TestUploadStreamsWithoutBuffering`, `TestDownloadFile`, `TestDownloadRedacts`, `TestDownloadDirZip`, `TestServerUploadDownload` PASS (auch unter `-race`). Ist `TestUploadStreamsWithoutBuffering` unter `-race` zu knapp (Race-Detector verdoppelt Allokationen nicht, aber prüfe die CI-Ausgabe), die Grenze nicht erhöhen, sondern melden.

---

### Task 7: Entpack-Jobs

**Files:**
- Create: `internal/files/jobs.go`, `internal/files/extract.go`
- Modify: `internal/files/service.go` (`Service.jobs`, `New`, `Close`), `internal/files/server.go` (Routen `extract`, `jobs`)
- Test: `internal/files/extract_test.go`

**Interfaces:**
- Consumes: Aufgaben 4–6, `filesapi.ExtractRequest`, `filesapi.Job`, `filesapi.JobState`, `filesapi.JobsResponse`.
- Produces (Spec §4.2: die komplette Vorprüfung läuft **synchron** in `POST /v1/extract`; Regelverstöße `400 invalid`, zu viele Einträge oder zu große Summe `413 too_large`, nicht leeres Ziel `409 exists`, zu wenig Platz `507`, Job-Limit `429 busy`; erst danach `202` mit Job):

```go
func (s *Service) Extract(ctx context.Context, req filesapi.ExtractRequest) (filesapi.Job, error)
func (s *Service) Job(id int64) *filesapi.Job // copy; nil if this instance never had a job
// Close (Aufgabe 4) bricht nun laufende Jobs ab und wartet auf sie.
```
Routen: `POST /v1/extract` (202 `Job`), `GET /v1/jobs?id=` (200 `JobsResponse`).

- [ ] **Step 1: Tests (rot)**

`internal/files/extract_test.go`:

```go
package files

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type zipEntry struct {
	name string
	body string
	mode fs.FileMode // 0 = derive from name
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: time.Unix(1_700_000_000, 0)}
		if e.mode != 0 {
			fh.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rawZip writes one entry with a lying header (declared size, method, flags).
func rawZip(t *testing.T, name string, payload []byte, declared uint64, method uint16, flags uint16) []byte {
	t.Helper()
	var comp bytes.Buffer
	data := payload
	if method == zip.Deflate {
		fw, _ := flate.NewWriter(&comp, flate.BestSpeed)
		_, _ = fw.Write(payload)
		_ = fw.Close()
		data = comp.Bytes()
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name: name, Method: method, Flags: flags, CRC32: crc32.ChecksumIEEE(payload),
		CompressedSize64: uint64(len(data)), UncompressedSize64: declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func putBytes(t *testing.T, dir, rel string, b []byte) {
	t.Helper()
	put(t, dir, rel, string(b))
}

func waitJob(t *testing.T, s *Service, id int64) filesapi.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		j := s.Job(id)
		if j != nil && j.State != jobRunning {
			return *j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still running: %+v", j)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func noTemp(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), tempPrefix) {
			t.Fatalf("temp entry left behind: %s", p)
		}
		return nil
	})
}

func TestExtractSuccess(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "uploads/world.zip", makeZip(t,
		zipEntry{name: "world/"},
		zipEntry{name: "world/level.dat", body: "lvl"},
		zipEntry{name: "world/region/r.0.0.mca", body: strings.Repeat("r", 5000)},
		zipEntry{name: "readme.txt", body: "hi"},
	))
	job, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "uploads/world.zip", Dest: "imported"})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != jobRunning || job.TotalEntries != 4 || job.TotalBytes != 5005 || job.ID == "" {
		t.Fatalf("started job: %+v", job)
	}
	done := waitJob(t, s, 1)
	if done.State != jobDone || done.Bytes != 5005 || done.Entries != 4 || done.FinishedAt == 0 || done.Error != "" {
		t.Fatalf("finished job: %+v", done)
	}
	if v, _ := get(t, data, "imported/world/level.dat"); v != "lvl" {
		t.Fatalf("extracted content %q", v)
	}
	fi, err := os.Stat(filepath.Join(data, "imported", "readme.txt"))
	if err != nil || !fi.ModTime().Equal(time.Unix(1_700_000_000, 0)) {
		t.Fatalf("mtime from the zip: %v %v", fi, err)
	}
	noTemp(t, data)
}

func TestExtractDestination(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	if err := os.Mkdir(filepath.Join(data, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "empty"}); err != nil {
		t.Fatalf("empty destination: %v", err)
	}
	if j := waitJob(t, s, 1); j.State != jobDone {
		t.Fatalf("job: %+v", j)
	}
	if v, _ := get(t, data, "empty/f.txt"); v != "x" {
		t.Fatal("not extracted into the empty folder")
	}
	for req, want := range map[filesapi.ExtractRequest]error{
		{ID: 1, Path: "a.zip", Dest: "empty"}:        filesapi.ErrExists, // no longer empty
		{ID: 1, Path: "a.zip", Dest: "a.zip"}:        filesapi.ErrInvalid,
		{ID: 1, Path: "a.zip", Dest: ""}:             filesapi.ErrInvalid,
		{ID: 1, Path: "a.zip", Dest: "nope/x"}:       filesapi.ErrNotFound,
		{ID: 1, Path: "empty/f.txt", Dest: "y"}:      filesapi.ErrInvalid, // not a .zip
		{ID: 1, Path: "missing.zip", Dest: "y"}:      filesapi.ErrNotFound,
		{ID: 1, Path: "a.zip", Dest: ".mcpanel-tmp-x"}: filesapi.ErrNotFound,
	} {
		if _, err := s.Extract(ctx, req); !errors.Is(err, want) {
			t.Errorf("extract %+v: %v, want %v", req, err, want)
		}
	}
	put(t, data, "garbage.zip", "this is not a zip")
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "garbage.zip", Dest: "g"}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("broken zip: %v", err)
	}
}

func TestExtractRejectsArchives(t *testing.T) {
	s, data := newService(t)
	many := make([]zipEntry, 101)
	for i := range many {
		many[i] = zipEntry{name: "f" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "/" + time.Duration(i).String(), body: "x"}
	}
	cases := map[string]struct {
		zip  []byte
		want error
	}{
		"dotdot":         {makeZip(t, zipEntry{name: "../evil.txt", body: "x"}), filesapi.ErrInvalid},
		"nested dotdot":  {makeZip(t, zipEntry{name: "a/../../evil.txt", body: "x"}), filesapi.ErrInvalid},
		"absolute":       {makeZip(t, zipEntry{name: "/etc/evil", body: "x"}), filesapi.ErrInvalid},
		"backslash":      {makeZip(t, zipEntry{name: `a\..\evil`, body: "x"}), filesapi.ErrInvalid},
		"symlink entry":  {makeZip(t, zipEntry{name: "link", body: "/etc/shadow", mode: fs.ModeSymlink | 0o777}), filesapi.ErrInvalid},
		"hidden":         {makeZip(t, zipEntry{name: ".rcon-cli.env", body: "x"}), filesapi.ErrInvalid},
		"reserved":       {makeZip(t, zipEntry{name: "w/.mcpanel-tmp-1", body: "x"}), filesapi.ErrInvalid},
		"duplicate":      {makeZip(t, zipEntry{name: "a.txt", body: "1"}, zipEntry{name: "a.txt", body: "2"}), filesapi.ErrInvalid},
		"file as parent": {makeZip(t, zipEntry{name: "a", body: "1"}, zipEntry{name: "a/b", body: "2"}), filesapi.ErrInvalid},
		"too many":       {makeZip(t, many...), filesapi.ErrTooLarge},
		"declared large": {makeZip(t, zipEntry{name: "big.bin", body: strings.Repeat("z", 1<<20+1)}), filesapi.ErrTooLarge},
		"encrypted":      {rawZip(t, "e.txt", []byte("secret"), 6, zip.Store, 0x1), filesapi.ErrInvalid},
		"unknown method": {rawZip(t, "m.txt", []byte("data"), 4, 99, 0), filesapi.ErrInvalid},
	}
	for name, c := range cases {
		putBytes(t, data, "bad.zip", c.zip)
		if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "bad.zip", Dest: "out"}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
		if _, err := os.Stat(filepath.Join(data, "out")); !os.IsNotExist(err) {
			t.Fatalf("%s: rejected archive created output", name)
		}
	}
	if s.Job(1) != nil {
		t.Fatal("a rejected archive started a job")
	}
	noTemp(t, data)
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(data)), "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("zip slip wrote outside")
	}
}

func TestExtractDetectsLyingSizes(t *testing.T) {
	s, data := newService(t)
	// Declares 10 bytes, inflates to 200 KiB: a zip bomb in miniature.
	putBytes(t, data, "bomb.zip", rawZip(t, "bomb.bin", bytes.Repeat([]byte{0}, 200<<10), 10, zip.Deflate, 0))
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "bomb.zip", Dest: "out"}); err != nil {
		t.Fatalf("pre-check passes on declared sizes: %v", err)
	}
	j := waitJob(t, s, 1)
	if j.State != jobFailed || j.Error == "" {
		t.Fatalf("bomb job: %+v", j)
	}
	if _, err := os.Stat(filepath.Join(data, "out")); !os.IsNotExist(err) {
		t.Fatal("failed job left its destination")
	}
	noTemp(t, data)
}

func TestExtractInsufficientStorage(t *testing.T) {
	disk := &fakeDisk{avail: 2 << 10}
	s, data := newService(t, func(c *Config) { c.Disk = disk })
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.bin", body: strings.Repeat("x", 4<<10)}))
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("reserve: %v", err)
	}
}

func TestExtractJobLimits(t *testing.T) {
	s, data := newService(t)
	release := make(chan struct{})
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	z := makeZip(t, zipEntry{name: "f.txt", body: "x"})
	for _, id := range []int64{1, 2, 3} {
		d := s.instanceDir(id)
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
		putBytes(t, d, "a.zip", z)
	}
	_ = data
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "o1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "o2"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("second job on the same server: %v, want ErrBusy", err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 2, Path: "a.zip", Dest: "o1"}); err != nil {
		t.Fatalf("job on another server: %v", err)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 3, Path: "a.zip", Dest: "o1"}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("third job overall: %v, want ErrBusy", err)
	}
	close(release)
	if j := waitJob(t, s, 1); j.State != jobDone {
		t.Fatalf("job 1: %+v", j)
	}
	if j := waitJob(t, s, 2); j.State != jobDone {
		t.Fatalf("job 2: %+v", j)
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 3, Path: "a.zip", Dest: "o1"}); err != nil {
		t.Fatalf("slot not released: %v", err)
	}
	waitJob(t, s, 3)
}

func TestCloseCancelsRunningJobs(t *testing.T) {
	s, data := newService(t)
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) { <-ctx.Done() }
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	j := s.Job(1)
	if j == nil || j.State != jobFailed {
		t.Fatalf("job after Close: %+v", j)
	}
	if _, err := os.Stat(filepath.Join(data, "out")); !os.IsNotExist(err) {
		t.Fatal("cancelled job left output")
	}
	noTemp(t, data)
}

func TestServerExtract(t *testing.T) {
	s, data := newService(t)
	putBytes(t, data, "a.zip", makeZip(t, zipEntry{name: "f.txt", body: "x"}))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	status, body := postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "a.zip", Dest: "out"})
	var job filesapi.Job
	if status != http.StatusAccepted || json.Unmarshal(body, &job) != nil || job.ID == "" {
		t.Fatalf("extract: %d %s", status, body)
	}
	waitJob(t, s, 1)
	resp, err := http.Get(ts.URL + filesapi.PathJobs + "?id=1")
	if err != nil {
		t.Fatal(err)
	}
	var jr filesapi.JobsResponse
	_ = json.NewDecoder(resp.Body).Decode(&jr)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || jr.Job == nil || jr.Job.State != jobDone {
		t.Fatalf("jobs: %d %+v", resp.StatusCode, jr.Job)
	}
	resp, err = http.Get(ts.URL + filesapi.PathJobs + "?id=2")
	if err != nil {
		t.Fatal(err)
	}
	b := new(bytes.Buffer)
	_, _ = b.ReadFrom(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(b.String()) != `{"job":null}` {
		t.Fatalf("jobs of a server without job: %s", b)
	}
	status, body = postJSON(t, ts, filesapi.PathExtract, filesapi.ExtractRequest{ID: 1, Path: "../x.zip", Dest: "o"})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("invalid path: %d %s", status, body)
	}
}
```

Hinweis zu `"too many"`: die erzeugten Namen müssen eindeutig und gültig sein; enthält einer ein unzulässiges Zeichen (z. B. `µ` aus `time.Duration.String()` ist gültiges UTF-8 und erlaubt), bleibt das Ergebnis `ErrTooLarge`, weil die Anzahl **vor** der Namensprüfung des 101. Eintrags greift – die Reihenfolge der Prüfungen in `checkArchive` (Zählen zuerst) ist deshalb verbindlich.

- [ ] **Step 2: `make check` – rot**

Run: `git add -N internal/files && make check`
Expected: FAIL (`s.Extract undefined`, `jobRunning undefined`, `s.jobs undefined`).

- [ ] **Step 3: Jobs implementieren**

`internal/files/jobs.go`:

```go
package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Short local names for the job states of filesapi (V1/Phase A).
const (
	jobRunning = filesapi.JobRunning
	jobDone    = filesapi.JobDone
	jobFailed  = filesapi.JobFailed
)

// jobs tracks extract jobs: the last one per instance, at most one running per instance and
// maxJobsTotal overall. Jobs live in memory; a restart forgets them (and CleanupTemp removes
// their temp folders).
type jobs struct {
	mu      sync.Mutex
	last    map[int64]*filesapi.Job
	running int
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	hook    func(ctx context.Context, id int64, entry string) // tests: runs before each entry
}

func newJobs() *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobs{last: map[int64]*filesapi.Job{}, ctx: ctx, cancel: cancel}
}

func (j *jobs) start(id int64, src, dest string, plan archivePlan, now time.Time) (filesapi.Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if cur := j.last[id]; cur != nil && cur.State == jobRunning {
		return filesapi.Job{}, fmt.Errorf("%w: an archive is already being extracted for this server", filesapi.ErrBusy)
	}
	if j.running >= maxJobsTotal {
		return filesapi.Job{}, fmt.Errorf("%w: too many archives are being extracted", filesapi.ErrBusy)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return filesapi.Job{}, err
	}
	job := &filesapi.Job{
		ID: hex.EncodeToString(b[:]), State: jobRunning, Path: src, Dest: dest,
		TotalBytes: plan.bytes, TotalEntries: plan.entries, StartedAt: now.UnixMilli(),
	}
	j.last[id] = job
	j.running++
	return *job, nil
}

func (j *jobs) progress(id, bytes int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		job.Bytes += bytes
		job.Entries++
	}
}

func (j *jobs) finish(id int64, err error, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.last[id]
	if job == nil {
		return
	}
	job.FinishedAt = now.UnixMilli()
	if err != nil {
		job.State = jobFailed
		job.Error = err.Error()
	} else {
		job.State = jobDone
	}
	j.running--
}

func (j *jobs) get(id int64) *filesapi.Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.last[id]
	if job == nil {
		return nil
	}
	c := *job
	return &c
}

// Job returns a copy of the last extract job of server id, or nil.
func (s *Service) Job(id int64) *filesapi.Job { return s.jobs.get(id) }
```

In `internal/files/service.go`: das Feld `jobs *jobs` zu `Service` hinzufügen, in `New` `jobs: newJobs()` setzen und `Close` ersetzen durch:

```go
// Close cancels running extract jobs (they end as failed and remove their temp folders) and
// waits for them.
func (s *Service) Close() {
	s.jobs.cancel()
	s.jobs.wg.Wait()
}
```

(`Close` muss mehrfach aufrufbar sein – `TestCloseCancelsRunningJobs` ruft es und `t.Cleanup` noch einmal; `cancel` und `Wait` sind idempotent.)

- [ ] **Step 4: Entpacken implementieren**

`internal/files/extract.go`:

```go
package files

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type archivePlan struct {
	bytes   int64 // sum of the declared sizes of all files
	entries int64
}

// Extract checks the archive and starts a job that unpacks it into dest (new or empty
// folder). Everything that can be checked up front is answered synchronously.
func (s *Service) Extract(ctx context.Context, req filesapi.ExtractRequest) (filesapi.Job, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.Job{}, err
	}
	src, err := cleanVisible(req.Path)
	if err != nil {
		return filesapi.Job{}, err
	}
	dest, err := cleanVisible(req.Dest)
	if err != nil {
		return filesapi.Job{}, err
	}
	switch {
	case src == "" || dest == "":
		return filesapi.Job{}, fmt.Errorf("%w: archive and destination are required", filesapi.ErrInvalid)
	case !strings.HasSuffix(strings.ToLower(src), ".zip"):
		return filesapi.Job{}, fmt.Errorf("%w: only .zip archives can be extracted", filesapi.ErrInvalid)
	case dest == src || strings.HasPrefix(src, dest+"/"):
		return filesapi.Job{}, fmt.Errorf("%w: the destination must not contain the archive", filesapi.ErrInvalid)
	}
	root, err := s.open(req.ID)
	if err != nil {
		return filesapi.Job{}, err
	}
	handedOver := false
	defer func() {
		if !handedOver {
			_ = root.Close()
		}
	}()
	if err := requireDir(root, parentOf(dest)); err != nil {
		return filesapi.Job{}, err
	}
	if err := destUsable(root, dest); err != nil {
		return filesapi.Job{}, err
	}
	f, st, err := openRegular(root, src, os.O_RDONLY)
	if err != nil {
		return filesapi.Job{}, err
	}
	defer func() {
		if !handedOver {
			_ = f.Close()
		}
	}()
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return filesapi.Job{}, fmt.Errorf("%w: %s is not a valid zip archive: %v", filesapi.ErrInvalid, src, err)
	}
	plan, err := checkArchive(zr, s.cfg.Limits)
	if err != nil {
		return filesapi.Job{}, err
	}
	if err := s.reserve(req.ID, plan.bytes); err != nil {
		return filesapi.Job{}, err
	}
	job, err := s.jobs.start(req.ID, src, dest, plan, s.clock.Now())
	if err != nil {
		return filesapi.Job{}, err
	}
	handedOver = true
	s.jobs.wg.Add(1)
	go func() {
		defer s.jobs.wg.Done()
		defer root.Close()
		defer f.Close()
		err := s.extract(s.jobs.ctx, root, zr, req.ID, dest)
		if err != nil {
			s.log.Warn("files: extract failed", "id", req.ID, "archive", src, "dest", dest, "err", err)
		}
		s.jobs.finish(req.ID, err, s.clock.Now())
	}()
	return job, nil
}

// destUsable: dest must not exist, or be an empty real folder.
func destUsable(root *os.Root, dest string) error {
	fi, err := lstat(root, dest)
	if errors.Is(err, filesapi.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s exists", filesapi.ErrExists, dest)
	}
	d, err := root.Open(dest)
	if err != nil {
		return fsErr(err)
	}
	defer d.Close()
	if names, err := d.Readdirnames(1); len(names) > 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return fmt.Errorf("%w: %s is not empty", filesapi.ErrExists, dest)
	}
	return nil
}

// checkArchive validates every entry before a byte is written (spec §4.2). The entry count
// is checked first, so a huge archive is refused without validating all names.
func checkArchive(zr *zip.Reader, lim config.FilesLimits) (archivePlan, error) {
	var plan archivePlan
	isDir := make(map[string]bool, len(zr.File))
	for _, zf := range zr.File {
		plan.entries++
		if plan.entries > lim.MaxExtractEntries {
			return plan, fmt.Errorf("%w: the archive has more than %d entries", filesapi.ErrTooLarge, lim.MaxExtractEntries)
		}
		name := strings.TrimSuffix(zf.Name, "/")
		clean, err := filesapi.CleanPath(name)
		switch {
		case err != nil:
			return plan, fmt.Errorf("%w: entry %q: %v", filesapi.ErrInvalid, zf.Name, err)
		case clean == "" || clean != name:
			return plan, fmt.Errorf("%w: entry %q is not a clean relative path", filesapi.ErrInvalid, zf.Name)
		case filesapi.IsHidden(clean):
			return plan, fmt.Errorf("%w: entry %q uses a reserved name", filesapi.ErrInvalid, zf.Name)
		}
		mode := zf.Mode()
		dir := mode.IsDir()
		switch {
		case mode&fs.ModeSymlink != 0 || (!dir && !mode.IsRegular()):
			return plan, fmt.Errorf("%w: entry %q is neither a file nor a folder", filesapi.ErrInvalid, zf.Name)
		case zf.Flags&0x1 != 0:
			return plan, fmt.Errorf("%w: entry %q is encrypted", filesapi.ErrInvalid, zf.Name)
		case zf.Method != zip.Store && zf.Method != zip.Deflate:
			return plan, fmt.Errorf("%w: entry %q uses an unsupported compression method", filesapi.ErrInvalid, zf.Name)
		}
		if _, dup := isDir[clean]; dup {
			return plan, fmt.Errorf("%w: entry %q appears twice", filesapi.ErrInvalid, zf.Name)
		}
		isDir[clean] = dir
		if !dir {
			size := zf.UncompressedSize64
			if size > uint64(lim.MaxExtractBytes) || plan.bytes > lim.MaxExtractBytes-int64(size) {
				return plan, fmt.Errorf("%w: the archive expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxExtractBytes)
			}
			plan.bytes += int64(size)
		}
	}
	for name := range isDir {
		for p := parentOf(name); p != ""; p = parentOf(p) {
			if d, ok := isDir[p]; ok && !d {
				return plan, fmt.Errorf("%w: %q is a file and the parent of %q", filesapi.ErrInvalid, p, name)
			}
		}
	}
	return plan, nil
}

// extract unpacks into a temp folder next to dest, counting the bytes actually written, and
// renames it to dest when everything succeeded. On any error the temp folder is removed.
func (s *Service) extract(ctx context.Context, root *os.Root, zr *zip.Reader, id int64, dest string) error {
	tmp, err := tempName(parentOf(dest))
	if err != nil {
		return err
	}
	if err := root.Mkdir(tmp, dirMode); err != nil { //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
		return fsErr(err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = root.RemoveAll(tmp)
		}
	}()
	budget := s.cfg.Limits.MaxExtractBytes
	for _, zf := range zr.File {
		name := strings.TrimSuffix(zf.Name, "/")
		if h := s.jobs.hook; h != nil {
			h(ctx, id, name)
		}
		if ctx.Err() != nil {
			return errors.New("the files service is shutting down")
		}
		target := tmp + "/" + name
		if zf.Mode().IsDir() {
			if err := root.MkdirAll(target, dirMode); err != nil { //nolint:gosec // G301: see above
				return fsErr(err)
			}
			s.jobs.progress(id, 0)
			continue
		}
		if err := root.MkdirAll(parentOf(target), dirMode); err != nil { //nolint:gosec // G301: see above
			return fsErr(err)
		}
		n, err := extractFile(root, zf, target, budget)
		budget -= n
		if err != nil {
			return err
		}
		_ = root.Chtimes(target, zf.Modified, zf.Modified)
		s.jobs.progress(id, n)
	}
	if err := destUsable(root, dest); err != nil { // something appeared meanwhile
		return err
	}
	if fi, err := root.Lstat(dest); err == nil && fi.IsDir() {
		if err := root.Remove(dest); err != nil {
			return fsErr(err)
		}
	}
	if err := root.Rename(tmp, dest); err != nil {
		return fsErr(err)
	}
	ok = true
	return nil
}

// extractFile writes one entry, refusing more bytes than declared or than the budget left.
func extractFile(root *os.Root, zf *zip.File, target string, budget int64) (int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %v", filesapi.ErrInvalid, zf.Name, err)
	}
	defer rc.Close()
	out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new file in the job's temp folder inside the instance's os.Root
	if err != nil {
		return 0, fsErr(err)
	}
	declared := int64(zf.UncompressedSize64) // ≤ MaxExtractBytes, checked by checkArchive
	limit := min(declared, budget)
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	cerr := out.Close()
	switch {
	case err != nil:
		return n, fmt.Errorf("%w: %s: %v", filesapi.ErrInvalid, zf.Name, err)
	case n > declared:
		return n, fmt.Errorf("%w: %s is larger than its header says", filesapi.ErrInvalid, zf.Name)
	case n > limit:
		return n, fmt.Errorf("%w: the archive expands to more than allowed", filesapi.ErrTooLarge)
	case cerr != nil:
		return n, fsErr(cerr)
	}
	return n, nil
}
```

In `internal/files/server.go` die Routen-Tabelle um

```go
		filesapi.PathExtract: {method: http.MethodPost, handler: s.extract},
		filesapi.PathJobs:    {method: http.MethodGet, handler: s.jobs},
```

ergänzen und anfügen:

```go
func (s *Server) extract(w http.ResponseWriter, r *http.Request) {
	var req filesapi.ExtractRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	job, err := s.svc.Extract(r.Context(), req)
	if err != nil {
		s.fail(w, r, "extract", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.URL.Query().Get("id"))
	if err != nil {
		s.fail(w, r, "jobs", err)
		return
	}
	writeJSON(w, http.StatusOK, filesapi.JobsResponse{Job: s.svc.Job(id)})
}
```

- [ ] **Step 5: `make check` – grün, Commit, CI**

```bash
make check
git add internal/files
git commit -m "feat(files): extract zip archives as checked background jobs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch
```
Expected: CI grün; `TestExtractSuccess`, `TestExtractDestination`, `TestExtractRejectsArchives`, `TestExtractDetectsLyingSizes`, `TestExtractInsufficientStorage`, `TestExtractJobLimits`, `TestCloseCancelsRunningJobs`, `TestServerExtract` PASS (auch unter `-race`). Meldet `archive/zip` beim Fall „dotdot“/„absolute“/„backslash“ schon in `zip.NewReader` `zip.ErrInsecurePath` (GODEBUG `zipinsecurepath=0`), ist das Ergebnis trotzdem `ErrInvalid` (Zweig „not a valid zip archive“) – richtig so.

---

### Task 8: Ende-zu-Ende über den echten Client und Linux-Sonderfälle

**Files:**
- Create: `internal/files/e2e_test.go`, `internal/files/umask_linux_test.go`

**Interfaces:**
- Consumes: alles aus Aufgaben 4–7; `filesapi.NewClient(socketPath) *UnixClient` (V1, Phase A) mit `Health`, `List`, `Read`, `Write`, `Upload`, `Download`, `Mkdir`, `Rename`, `Delete`, `Extract`, `Job`; `unixsock.Listen`; `peercred.Wrap`/`AllowUIDs`.
- Produces: keine neuen Schnittstellen; belegt den Vertrag Dienst ↔ Client (Codes → Sentinels, Streams, Header).

- [ ] **Step 1: Tests schreiben**

`internal/files/e2e_test.go`:

```go
package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// startUnix serves a Service on a real Unix socket with peer-UID checking (allow) and returns
// a filesapi client for it. The socket lives in a short temp dir (macOS limits socket paths
// to 104 bytes; t.TempDir is too long there).
func startUnix(t *testing.T, allow uint32) (*filesapi.UnixClient, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	svc, data := newService(t)
	srv := NewServer(svc, "e2e", discardLog())
	sock := filepath.Join(dir, "f.sock")
	l, err := unixsock.Listen(sock, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(allow), discardLog())) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return filesapi.NewClient(sock), data
}

func TestEndToEndWithClient(t *testing.T) {
	c, data := startUnix(t, uint32(os.Getuid()))
	bg := context.Background()
	if err := c.Health(bg); err != nil {
		t.Fatalf("health: %v", err)
	}
	put(t, data, "server.properties", "rcon.password=s3cret\nmotd=x\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")

	if err := c.Mkdir(bg, 1, "plugins"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := c.Mkdir(bg, 1, "plugins"); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("mkdir twice: %v, want ErrExists", err)
	}
	w, err := c.Write(bg, filesapi.WriteRequest{ID: 1, Path: "plugins/config.yml", Content: "a: 1\n"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	stale := w.MTime - 1
	if _, err := c.Write(bg, filesapi.WriteRequest{ID: 1, Path: "plugins/config.yml", Content: "a: 2\n", ExpectedMTime: &stale}); !errors.Is(err, filesapi.ErrModified) {
		t.Fatalf("stale write: %v, want ErrModified", err)
	}
	r, err := c.Read(bg, 1, "server.properties")
	if err != nil || strings.Contains(r.Content, "s3cret") {
		t.Fatalf("read: %q %v", r.Content, err)
	}
	if _, err := c.Read(bg, 1, ".rcon-cli.env"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("hidden: %v, want ErrNotFound", err)
	}
	if _, err := c.Read(bg, 1, "plugins"); !errors.Is(err, filesapi.ErrNotRegular) {
		t.Fatalf("read folder: %v, want ErrNotRegular", err)
	}

	payload := bytes.Repeat([]byte("jar"), 10000)
	up, err := c.Upload(bg, 1, "plugins/Big.jar", false, int64(len(payload)), bytes.NewReader(payload))
	if err != nil || up.Size != int64(len(payload)) {
		t.Fatalf("upload: %+v %v", up, err)
	}
	if _, err := c.Upload(bg, 1, "plugins/Big.jar", false, 3, strings.NewReader("abc")); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("upload exists: %v, want ErrExists", err)
	}
	d, err := c.Download(bg, 1, "plugins/Big.jar")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if !bytes.Equal(got, payload) || d.IsDir || d.Size != int64(len(payload)) {
		t.Fatalf("download content/size: %d bytes, %+v", len(got), d)
	}
	d, err = c.Download(bg, 1, "plugins")
	if err != nil {
		t.Fatalf("download folder: %v", err)
	}
	zipped, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if !d.IsDir || zipNames(t, zipped)["plugins/config.yml"] != "a: 1\n" {
		t.Fatalf("folder zip: %+v", d)
	}

	list, err := c.List(bg, 1, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range list.Entries {
		if strings.HasPrefix(e.Name, ".rcon-cli") {
			t.Fatalf("list shows %s", e.Name)
		}
	}
	if err := c.Rename(bg, filesapi.RenameRequest{ID: 1, From: "plugins/Big.jar", To: "Big.jar"}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := c.Delete(bg, 1, "plugins"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	putBytes(t, data, "w.zip", makeZip(t, zipEntry{name: "world/level.dat", body: "lvl"}))
	job, err := c.Extract(bg, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "imported"})
	if err != nil || job.ID == "" {
		t.Fatalf("extract: %+v %v", job, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := c.Job(bg, 1)
		if err != nil {
			t.Fatalf("job: %v", err)
		}
		if j != nil && j.State == jobDone {
			break
		}
		if time.Now().After(deadline) || (j != nil && j.State == jobFailed) {
			t.Fatalf("job did not finish: %+v", j)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v, _ := get(t, data, "imported/world/level.dat"); v != "lvl" {
		t.Fatal("extract over the client produced nothing")
	}
	if j, err := c.Job(bg, 2); err != nil || j != nil {
		t.Fatalf("job of a server without jobs: %+v %v", j, err)
	}
	if _, err := c.List(bg, 2, ""); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("server without data dir: %v, want ErrNotFound", err)
	}
}

func TestPeerUIDIsChecked(t *testing.T) {
	c, _ := startUnix(t, uint32(os.Getuid())+1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Health(ctx); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("disallowed peer: %v, want ErrUnavailable", err)
	}
}
```

`internal/files/umask_linux_test.go`:

```go
//go:build linux

package files

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// The systemd unit runs the service with UMask=0027; new files and folders must then not be
// readable by others (spec §3.1), whatever mode the code asks for.
func TestCreatedModesHonourUmask(t *testing.T) {
	old := syscall.Umask(0o027)
	defer syscall.Umask(old)
	s, data := newService(t)
	if _, err := s.Write(ctx, filesapi.WriteRequest{ID: 1, Path: "a.txt", Content: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, 1, "dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := upload(s, "dir/b.bin", false, "y"); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]os.FileMode{"a.txt": 0o640, "dir": 0o750, "dir/b.bin": 0o640} {
		fi, err := os.Stat(filepath.Join(data, rel))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", rel, fi.Mode().Perm(), want)
		}
	}
}

// A FIFO swapped in after the Lstat check must not block a reader: openRegular opens with
// O_NONBLOCK and then rejects the non-regular file.
func TestOpenRegularDoesNotBlockOnFIFO(t *testing.T) {
	s, data := newService(t)
	if err := syscall.Mkfifo(filepath.Join(data, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	f, err := root.OpenFile("pipe", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("O_NONBLOCK open of a FIFO must return at once: %v", err)
	}
	_ = f.Close()
	if _, _, err := openRegular(root, "pipe", os.O_RDONLY); err == nil {
		t.Fatal("openRegular accepted a FIFO")
	}
}
```

- [ ] **Step 2: `make check`**

Run: `git add -N internal/files && make check`
Expected: PASS (die Tests nutzen nur vorhandene Schnittstellen). Kompiliert `e2e_test.go` nicht, weil `filesapi.UnixClient` eine andere Signatur hat als V1, **nicht** den Client ändern (Phase A), sondern dem Lead melden.

- [ ] **Step 3: Commit, CI, PR bereit melden**

```bash
git add internal/files
git commit -m "test(files): end-to-end over the Unix socket with the filesapi client, umask and FIFO on Linux

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch
```
Expected: CI grün; `TestEndToEndWithClient`, `TestPeerUIDIsChecked`, `TestCreatedModesHonourUmask`, `TestOpenRegularDoesNotBlockOnFIFO` PASS. Danach dem Lead Bescheid geben (PR nicht selbst auf „ready“ setzen).

## Self-Review (dieser Datei)

- **Spec §3.1:** Binary `files`, `files.toml` (über `config.LoadFiles`/`DevFiles`), Peer-UID über `peercred`, `files_socket` (panel, Phase A/B2), Dev-Modus legt Datenverzeichnisse an → Aufgabe 4. Unit-Härtung ist Phase C.
- **§3.2:** `os.OpenRoot` pro Anfrage, fehlend → 404, `Lstat` vor jedem Öffnen, `Stat`+`SameFile` danach, Symlinks nie verfolgt (eigene Komponentenprüfung, begründet oben), `O_NONBLOCK` → Aufgaben 4–5, 8.
- **§3.3:** Temp-Namen `.mcpanel-tmp-*`, Aufräumen beim Start bis Tiefe 64 → Aufgabe 4. Rechte von `instances/` sind Phase A (Aufgabe 1).
- **§3.4:** `CleanPath`/`IsHidden` (Phase A) an jedem Eingang; Schwärzen bei Lesen, Download, Ordner-Zip; Zurückschreiben beim Speichern → Aufgaben 5–6.
- **§4:** alle elf Endpunkte mit den Statuscodes aus der Tabelle, Fehlercodes, Body-Grenzen → Aufgaben 4–7; §4.1 Plattenplatz, Fristen, 4 Uploads → Aufgabe 6; §4.2 Entpacken komplett → Aufgabe 7.
- **§8 Zeile „`files` …“:** alle genannten Fälle haben einen Test (siehe Review Focus); „Server: Peer-UID, unbekannte Felder, Fristen, statfs-Fake, Temp-Aufräumen“ → Aufgaben 4, 5, 8. Fristen pro Vorgang sind implementiert, aber nicht automatisch getestet (eine echte 60-s-Stille ist für die CI zu langsam); Review prüft `deadlineReader`/`deadlineWriter`.

## Vertragsprobleme / offene Punkte für den Lead

1. **Peer-UID:** Entschieden (Nutzer): `panel_uid` und root (`AllowUIDs(panel_uid, 0)`), wie beim agent; Spec §3.1 und das CI-Skript in Plan 05 sind angeglichen.
2. **`peercred`-Logtext:** `peercred.Wrap` loggt „agent: rejecting connection …“ – im Dienst irreführend; Anpassung wäre eine Änderung in `internal/agent/peercred` (nicht B1). Kosmetisch.
3. **Hardlinks:** „ohne Überschreiben“ (neue Datei im Editor, Upload ohne `overwrite`) nutzt `root.Link` (atomar). Dateisysteme ohne Hardlinks würden das scheitern lassen; für ext4/xfs/APFS kein Thema – Vorgabe für den Installer (lokales Dateisystem).
4. **Socket-Aktivierung im Test:** Der fd-3-Pfad (`activatedListener` mit echtem Socket) ist nur auf der Test-VM bzw. in Phase C (`mcpanel-files.socket`) wirklich prüfbar; die Unit-Tests decken nur die Umgebungs-Logik ab.
5. **Download-Name:** `filesapi.Download.Name` (V1) berechnet der Client aus dem Pfad; der Dienst sendet keinen Namen.
6. **`ErrInvalid` statt 411:** Ein Upload ohne `Content-Length` antwortet `400 invalid` (wie Spec §5.1).
