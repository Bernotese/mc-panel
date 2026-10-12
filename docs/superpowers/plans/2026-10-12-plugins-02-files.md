# Teilprojekt 3c – Dienst `mcpanel-files`: Prüfsummen-Upload, `/v1/jars`, Jar-Metadaten (Aufgaben 4–6) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der unprivilegierte Dienst `mcpanel-files` bekommt die Dienst-Seite von Spec 3c §5: `PUT /v1/upload` prüft auf Wunsch eine Prüfsumme (sha256 oder sha512) vor `fsync`/`rename`, und die neue Operation `POST /v1/jars` listet die Jars in `plugins/` bzw. `mods/` einer Instanz mit sha256, sha512 (Cache nach Datei-Identität) und Metadaten aus `paper-plugin.yml`, `plugin.yml`, `fabric.mod.json` bzw. `META-INF/neoforge.mods.toml`.

**Architecture:** Alles bleibt im vorhandenen Muster von `internal/files`: jede Anfrage öffnet `<instances_dir>/instances/<id>` per `s.open` (`os.Root`), Pfade gehen nur über `lstat` (Komponenten einzeln, nie einem Symlink folgen), Dateien nur über `openRegular` (`O_NOFOLLOW|O_NONBLOCK`, `os.SameFile`). Der Upload rechnet die Digests per `io.MultiWriter` beim Schreiben der Temp-Datei mit; ein Fehlschlag ist `filesapi.ErrHashMismatch` und läuft durch den vorhandenen Aufräumpfad (Temp-Datei weg, Ziel unverändert). `Jars` liest die oberste Ebene des Ordners (wie `List`, höchstens `filesapi.MaxJars`), öffnet jedes Jar einmal und berechnet unter einer Semaphore (`maxHashRuns = 2`) beide Digests in einem Durchlauf sowie die Metadaten (`readJarMeta`: `checkCentralDirectory` aus `zipcheck.go` vor `zip.NewReader`, jede Datei mit `io.LimitReader`). Ergebnisse liegen in einem LRU-Cache (`hashCache`, 4096 Einträge) mit dem Schlüssel (Gerät, Inode, Größe, mtime), plattformabhängig über `filekey_linux.go`/`filekey_darwin.go`/`filekey_other.go`. Die Route nutzt `longOp`, weil das Hashen vieler Jars die 30-s-Schreibfrist des Servers überschreiten kann.

**Tech Stack:** Go 1.27 (Standardbibliothek: `archive/zip`, `crypto/sha256`, `crypto/sha512`, `container/list`, `encoding/json`, `strings.Lines`, `syscall.Stat_t`), `github.com/BurntSushi/toml` (bereits direkte Abhängigkeit, v1.6.0), vorhandene Helfer aus `internal/files` und `internal/files/filesapi` (V1 + Ergänzungen aus Phase A). Tests nur Standardbibliothek, Test-Jars werden mit `archive/zip` im Test gebaut. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` (§5, §9 Zeile `files`), für den Dienst selbst `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§3.2 Symlink-Regeln, §4.1 Upload).

## Ausführung dieser Phase

Es gelten alle Global Constraints, die Verträge (vor allem V1) und das Dateieigentum aus `docs/superpowers/plans/2026-10-12-plugins-00-overview.md` sowie `CLAUDE.md`. Phase A (Aufgaben 1–3, Branch `feat/plugins-contracts`) ist auf `main` gemerged, bevor diese Phase startet.

- **Branch und Worktree:** `git fetch origin && git worktree add ../mc-panel-wt/feat-plugins-files -b feat/plugins-files origin/main`; alle Befehle unten laufen in `../mc-panel-wt/feat-plugins-files`.
- **Lokal nur `make check`** (gofmt, `go vet`, Build macOS + Linux, tsc). Neue Dateien vorher mit `git add -N internal/files` vormerken, sonst prüft gofmt sie nicht. Tests, `-race`, Lint laufen in der CI (`gh pr checks <pr> --watch`, Fehler mit `gh run view <id> --log-failed`). „Expected: PASS“ wird in den CI-Logs geprüft.
- **Draft-PR** beim ersten Push (Aufgabe 4), danach jede Aufgabe als eigener Commit auf demselben Branch. PR nicht selbst auf „ready“ setzen und nicht mergen; nach Aufgabe 6 dem Lead Bescheid geben.
- **Dateieigentum:** nur `internal/files/**` **außer** `internal/files/filesapi/**`. `filesapi` (V1 plus die Phase-A-Helfer unten) wird nur benutzt. Passt etwas aus `filesapi` nicht zu diesem Plan (Name, Signatur, Verhalten), **nicht** `filesapi` ändern, sondern dem Lead melden.
- Zum Debuggen von CI-Fehlern darf die Test-VM `claude@192.168.1.117` genutzt werden (Kopie in ein Scratch-Verzeichnis, `go test ./internal/files/ -run <Name>`; nie dort deployen).

### Was Phase A in `filesapi` liefert (benutzt, nicht geändert)

V1 aus der Übersicht und zusätzlich (von Phase A angekündigt, additiv):

```go
const (
	PathJars        = "/v1/jars"
	DirPlugins      = "plugins"
	DirMods         = "mods"
	MaxJars         = 500
	MaxJarHashBytes = 256 << 20
	CodeHashMismatch = "hash_mismatch" // in der codes-Tabelle mit 400: CodeOf(ErrHashMismatch) = ("hash_mismatch", 400)

	AlgoSHA256  = "sha256"
	AlgoSHA512  = "sha512"
	QuerySHA256 = "sha256" // Query-Parameter von PUT /v1/upload
	QuerySHA512 = "sha512"
	JarsTimeout = 2 * time.Minute
)
var ErrHashMismatch = errors.New(CodeHashMismatch)

type JarsRequest struct{ ID int64; Dir string }               // json: id, dir
type JarKind string                                            // JarKindPlugin | JarKindMod
type JarMeta struct{ Kind JarKind; ID, Name, Version string }
type Jar struct{ Name string; Enabled bool; Size, MTime int64; SHA256, SHA512 string; Meta *JarMeta }
type JarsResponse struct{ Jars []Jar; Truncated bool }
type Checksum struct{ Algo, Hex string }
type UploadResponse struct{ Size int64; SHA256, SHA512 string } // sha256/sha512 omitempty

// ChecksumFromQuery: ok=false ohne Parameter; beide, einer mehrfach, leer oder falsches Format
// (nicht genau 64 bzw. 128 Kleinbuchstaben-Hex) → ErrInvalid.
func ChecksumFromQuery(q url.Values) (sum Checksum, ok bool, err error)
func (s Checksum) Validate() error                          // ErrInvalid
func (s Checksum) Matches(sha256Hex, sha512Hex string) bool // vergleicht den Digest von s.Algo
func ValidJarDir(dir string) bool                           // nur "plugins" | "mods"
// IsJarName: ".jar" (enabled) bzw. ".jar.disabled" (disabled), Groß/Klein egal, mindestens ein
// Zeichen vor ".jar". Prüft weder '/' noch verborgene Namen (CleanPath/filesapi.IsHidden selbst prüfen).
func IsJarName(name string) (enabled, ok bool)

// Client (von *UnixClient implementiert), hier nur in e2e-Tests benutzt:
//	Jars(ctx context.Context, id int64, dir string) (JarsResponse, error)
//	UploadChecked(ctx context.Context, id int64, path string, overwrite bool, size int64, sum Checksum, body io.Reader) (UploadResponse, error)
```

### Verbindliche Werte dieser Phase

| Thema | Wert |
|---|---|
| Upload-Parameter | optional `sha256=<64 hex>` **oder** `sha512=<128 hex>` (Kleinbuchstaben); beide, mehrfach, leer, falsches Format → `400 invalid` (`filesapi.ChecksumFromQuery`), bevor ein Byte des Bodys gelesen wird |
| Upload-Prüfung | mit Prüfsumme: sha256 + sha512 per `io.MultiWriter` beim Schreiben der Temp-Datei; Vergleich nach dem letzten Byte, **vor** `f.Sync()` und `commit`; Abweichung → `filesapi.ErrHashMismatch` (`400 hash_mismatch`), Temp-Datei entfernt, Ziel unverändert, Reservierung freigegeben |
| Upload-Antwort | `201 {size, sha256, sha512}` nur mit Prüfsumme, sonst wie bisher `{size}` (omitempty) |
| `/v1/jars` | `POST`, `application/json`, Body `{id, dir}` ≤ `filesapi.MaxSmallJSONBytes`, `DisallowUnknownFields`; `dir` ∉ {`plugins`, `mods`} → `400 invalid`; Handler über `longOp` (Schreibfrist aufgehoben, Client wartet `filesapi.JarsTimeout`) |
| Auswahl | oberste Ebene von `dir`, `filesapi.IsJarName` und nicht `filesapi.IsHidden`; nur reguläre Dateien laut `lstat` (Symlinks, Ordner, FIFOs, Geräte ausgelassen); Öffnen per `openRegular` (`O_NOFOLLOW\|O_NONBLOCK`, `os.SameFile`), inzwischen getauscht → ausgelassen |
| Grenzen | höchstens `filesapi.MaxJars` (500) Jars, darüber `truncated: true` (Abbruch beim Lesen wie `List`, Auswahl folgt der Verzeichnisreihenfolge); Antwort sortiert nach Name (Byte-Reihenfolge); `jars` nie `null` |
| Leere Liste | `dir` fehlt, ist eine Datei oder ein Symlink; Instanz ohne Datenverzeichnis (Server lief nie) |
| Prüfsummen | sha256 + sha512 in einem Durchlauf über genau `size` Bytes; Jars > `s.jarHashMax` (Default `filesapi.MaxJarHashBytes` = 256 MiB, Tests senken ihn) ohne Prüfsummen (`""`); Jar während des Lesens verändert (Größe/mtime) → weder Prüfsummen noch Metadaten, nicht gecacht |
| Cache | `hashCache`, LRU, `hashCacheSize = 4096` Einträge; Schlüssel `jarKey{dev, ino, size, mtime (ns)}` aus `syscall.Stat_t` (Linux `Dev uint64`, macOS `Dev int32`, sonst kein Cache); Wert: beide Digests und Metadaten; Antworten bekommen eine Kopie der Metadaten |
| Parallelität | `maxHashRuns = 2` Berechnungen gleichzeitig im ganzen Dienst (Kanal `s.hashSlots`); Cache-Treffer brauchen keinen Platz; eine Anfrage wartet auf einen Platz, solange ihr Kontext lebt (sonst `ctx.Err()`, Server antwortet nicht mehr) |
| Metadaten | erste vorhandene Datei in der Reihenfolge `paper-plugin.yml`, `plugin.yml` (kind `plugin`), `fabric.mod.json`, `META-INF/neoforge.mods.toml` (kind `mod`) entscheidet; Zentralverzeichnis ≤ `maxJarEntries = 100_000` (`checkCentralDirectory` vor `zip.NewReader`); jede Datei: deklarierte Größe ≤ `maxMetaBytes = 64 KiB` **und** gelesen über `io.LimitReader(…, maxMetaBytes+1)`; Werte ≤ `maxMetaValue = 128` Zeichen, ungültiges UTF-8, Steuer- (Cc) und Formatzeichen (Cf) entfernt; jeder Fehler → `nil` |
| Pflichtfelder | Plugin ohne `name` → `nil`; Mod ohne `id`/`modId` → `nil` |

## Review Focus (aus der Übersicht, in dieser Phase)

- **#2 Namenskollisionen und Groß-/Kleinschreibung (Liste):** `Foo.jar` und `foo.JAR` sind zwei Jars mit eigener Prüfsumme; `x.jar` und `x.jar.disabled` erscheinen beide, `enabled` richtig. → `TestJarsKeepsCaseVariantsApart`, `TestJarsListsJarFiles` (Aufgabe 5).
- **#5 Große oder präparierte Plugin-Ordner:** > 500 Jars → 500 + `truncated`; Jar > 256 MiB → ohne Prüfsummen (über `s.jarHashMax` simuliert), Metadaten trotzdem; Zip-Bombe in `plugin.yml` → `meta: null` ohne große Allokation; 100 001 Einträge → `meta: null`; Symlink-Jar (nach außen und innen), FIFO mit `.jar`-Endung, Ordner mit `.jar`-Namen, symlinkter `plugins`-Ordner → ausgelassen, kein Hänger, kein Byte außerhalb gelesen. → `TestJarsTruncates`, `TestJarsChecksumLimit`, `TestJarsSkipsLinksAndSpecialFiles`, `TestJarsIgnoresSymlinkedFolder`, `TestDescribeJarSkipsSwappedFIFO` (Aufgabe 5), `TestJarMetaRefusesBrokenAndHostileJars`, `TestJarMetaEntryLimit`, `TestJarsMetaIsBestEffort` (Aufgabe 6).
- **Prüfsumme vor dem `rename`** (Spec §5.2): falsche Prüfsumme hinterlässt keine Datei, lässt das Ziel unverändert, gibt die Reservierung frei, kommt beim Client als `ErrHashMismatch` an (nicht `ErrInvalid`). → `TestUploadCheckedMismatchLeavesNothing`, `TestServerUploadChecksumParameters`, `TestUploadCheckedEndToEnd` (Aufgabe 4).

## Abweichungen und Präzisierungen

- **Instanz ohne Datenverzeichnis → leere Liste** statt `404 not_found`: Spec §5.1 sagt „Fehlt `dir` … → leere Liste“; ohne Datenverzeichnis fehlt `dir` erst recht, und das Inventar eines nie gestarteten Servers soll leer sein statt fehlschlagen. Alle anderen Operationen bleiben bei `ErrNotFound`. (Für Phase B2: `Jars` liefert hier nie `ErrNotFound`.)
- **`statusFor`/`fail` bleiben unverändert:** Phase A trägt `hash_mismatch` mit Status 400 in die `codes`-Tabelle von `filesapi` ein, also liefert `filesapi.CodeOf` (in `statusFor`) bereits `400 hash_mismatch`. Aufgabe 4 ergänzt nur `fsErr` (Sentinel-Durchreichliste) und `TestStatusFor` um eine Zeile, die das Mapping festnagelt.
- **Phase-A-Helfer über V1 hinaus** (`ChecksumFromQuery`, `Checksum.Validate`/`Matches`, `ValidJarDir`, `IsJarName`, `AlgoSHA*`, `QuerySHA*`, `JarsTimeout`) werden benutzt, damit Dienst und Client dieselben Regeln haben. Fehlen sie nach dem Merge von Phase A, dem Lead melden (nicht nachbauen).
- **„Älteste raus“** im Hash-Cache ist als LRU umgesetzt (zuletzt benutzte Einträge bleiben); ein umbenanntes Jar (Schalter aktiv/inaktiv) behält Inode, Größe und mtime und wird nicht neu gehasht.
- **Metadaten auch für Jars > 256 MiB** (nur die Prüfsummen entfallen, Spec §5.1 begrenzt nur diese); das Zentralverzeichnis ist ohnehin begrenzt.
- **Nicht lesbares Jar** (z. B. fremde Rechte) erscheint ohne Prüfsummen und Metadaten, statt die Liste scheitern zu lassen oder es zu verschweigen.
- **Neue Dateien über die Übersicht hinaus:** `filekey_linux.go`, `filekey_darwin.go`, `filekey_other.go` (Plattform-Schlüssel des Caches, Muster `disk_*.go`); Tests in eigenen Dateien, damit vorhandene Testdateien kaum berührt werden.

## Dateistruktur

```
internal/files/transfer.go             (4)  Upload → UploadChecked mit Prüfsumme
internal/files/server.go               (4)  receive: Prüfsummen-Parameter; (5) Route + Handler jars
internal/files/service.go              (4)  fsErr: ErrHashMismatch; (5) hashSlots, jarCache, jarHashMax, jarHook
internal/files/service_test.go         (4)  TestStatusFor: hash_mismatch
internal/files/checksum_test.go        (4)  Upload mit Prüfsumme (Service + HTTP)
internal/files/plugins_e2e_test.go     (4, 5) über den echten Unix-Socket mit dem filesapi-Client
internal/files/hashcache.go            (5)  jarKey, jarInfo, hashCache (LRU)
internal/files/filekey_linux.go        (5)  fileKeyOf (Stat_t.Dev uint64)
internal/files/filekey_darwin.go       (5)  fileKeyOf (Stat_t.Dev int32)
internal/files/filekey_other.go        (5)  !linux && !darwin: kein Cache
internal/files/jars.go                 (5)  Jars, findJars, describeJar, jarInfo, hashJar; (6) Metadaten einhängen
internal/files/hashcache_test.go       (5)
internal/files/jars_test.go            (5)
internal/files/jars_linux_test.go      (5)  Symlinks, FIFO (nur Linux, wie umask_linux_test.go)
internal/files/jarmeta.go              (6)  readJarMeta und Parser
internal/files/jarmeta_test.go         (6)
```

---

### Task 4: Upload mit Prüfsumme

**Files:**
- Modify: `internal/files/transfer.go` (`Upload` wird Hülle um das neue `UploadChecked`)
- Modify: `internal/files/server.go` (`receive` liest `sha256`/`sha512`, Kommentar von `upload`)
- Modify: `internal/files/service.go` (`fsErr`: `ErrHashMismatch` durchreichen)
- Modify: `internal/files/service_test.go` (`TestStatusFor`: eine Zeile)
- Test: `internal/files/checksum_test.go`, `internal/files/plugins_e2e_test.go`

**Interfaces:**
- Consumes: `filesapi.Checksum`, `Checksum.Validate`, `Checksum.Matches`, `filesapi.ChecksumFromQuery`, `filesapi.ErrHashMismatch`, `filesapi.CodeHashMismatch`, `filesapi.AlgoSHA256/AlgoSHA512`, `filesapi.QuerySHA256/QuerySHA512`, `filesapi.UploadResponse{Size, SHA256, SHA512}`, `(*filesapi.UnixClient).UploadChecked` (e2e); vorhanden in `internal/files`: `cleanVisible`, `s.open`, `requireDir`, `lstat`, `s.reserve`, `landingWriter`, `s.tempName`, `commit`, `fsErr`, `bodyReader`, `s.uploads`; Test-Helfer `newService`, `put`, `get`, `noTemp`, `upload`, `zeroReader`, `startHTTP`, `errCode`, `startUnix`, `ctx`.
- Produces:

```go
// unverändert für alle bisherigen Aufrufer:
func (s *Service) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (filesapi.UploadResponse, error)
// neu; die Null-Checksum bedeutet „keine Prüfung“:
func (s *Service) UploadChecked(ctx context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error)
// Test-Helfer (checksum_test.go), auch von Aufgaben 5–6 benutzt:
func sums(content string) (string, string) // lowercase hex sha256, sha512
```
Draht: `PUT /v1/upload?id=&path=&overwrite=0|1[&sha256=<hex>|&sha512=<hex>]` → `201 {size[, sha256, sha512]}`, `400 invalid`, `400 hash_mismatch`, sonst wie bisher.

- [ ] **Step 1: Tests schreiben (rot)**

`internal/files/checksum_test.go`:

```go
package files

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// sums returns the lowercase hex sha256 and sha512 of content.
func sums(content string) (string, string) {
	a := sha256.Sum256([]byte(content))
	b := sha512.Sum512([]byte(content))
	return hex.EncodeToString(a[:]), hex.EncodeToString(b[:])
}

func uploadChecked(s *Service, path string, overwrite bool, content string, sum filesapi.Checksum) (filesapi.UploadResponse, error) {
	return s.UploadChecked(ctx, 1, path, overwrite, int64(len(content)), sum, strings.NewReader(content))
}

func TestUploadCheckedAcceptsMatchingDigest(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/keep.txt", "")
	const content = "jar bytes"
	h256, h512 := sums(content)
	for _, sum := range []filesapi.Checksum{
		{Algo: filesapi.AlgoSHA256, Hex: h256},
		{Algo: filesapi.AlgoSHA512, Hex: h512},
	} {
		target := "plugins/" + sum.Algo + ".jar"
		res, err := uploadChecked(s, target, false, content, sum)
		if err != nil {
			t.Fatalf("%s: %v", sum.Algo, err)
		}
		if res.Size != int64(len(content)) || res.SHA256 != h256 || res.SHA512 != h512 {
			t.Fatalf("%s: answer %+v, want both digests", sum.Algo, res)
		}
		if v, _ := get(t, data, target); v != content {
			t.Fatalf("%s: stored %q", sum.Algo, v)
		}
	}
	// Overwriting with a matching digest replaces the file.
	h256b, _ := sums("v2")
	if _, err := uploadChecked(s, "plugins/sha256.jar", true, "v2", filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: h256b}); err != nil {
		t.Fatalf("checked overwrite: %v", err)
	}
	if v, _ := get(t, data, "plugins/sha256.jar"); v != "v2" {
		t.Fatalf("checked overwrite stored %q", v)
	}
	// Without a checksum the answer stays {size}.
	res, err := upload(s, "plugins/plain.jar", false, content)
	if err != nil || res.SHA256 != "" || res.SHA512 != "" || res.Size != int64(len(content)) {
		t.Fatalf("plain upload: %+v %v", res, err)
	}
	noTemp(t, data)
}

func TestUploadCheckedMismatchLeavesNothing(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/Old.jar", "old content")
	other256, other512 := sums("something else")
	for name, c := range map[string]struct {
		path      string
		overwrite bool
		sum       filesapi.Checksum
	}{
		"new file, sha512":  {"plugins/New.jar", false, filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: other512}},
		"overwrite, sha256": {"plugins/Old.jar", true, filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: other256}},
	} {
		_, err := uploadChecked(s, c.path, c.overwrite, "new content", c.sum)
		if !errors.Is(err, filesapi.ErrHashMismatch) || errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrHashMismatch", name, err)
		}
	}
	if _, ok := get(t, data, "plugins/New.jar"); ok {
		t.Fatal("a mismatching upload became visible")
	}
	if v, _ := get(t, data, "plugins/Old.jar"); v != "old content" {
		t.Fatalf("a mismatching overwrite changed the target: %q", v)
	}
	noTemp(t, data)
	// The reservation is given back.
	if s.pending != 0 {
		t.Fatalf("pending after mismatch: %d", s.pending)
	}
}

func TestUploadCheckedRefusesInvalidChecksums(t *testing.T) {
	s, data := newService(t)
	h256, h512 := sums("x")
	for name, sum := range map[string]filesapi.Checksum{
		"unknown algo":   {Algo: "md5", Hex: h256},
		"algo only":      {Algo: filesapi.AlgoSHA256},
		"hex only":       {Hex: h256},
		"uppercase":      {Algo: filesapi.AlgoSHA256, Hex: strings.ToUpper(h256)},
		"sha512 of 64":   {Algo: filesapi.AlgoSHA512, Hex: h256},
		"sha256 of 128":  {Algo: filesapi.AlgoSHA256, Hex: h512},
		"not hex":        {Algo: filesapi.AlgoSHA256, Hex: strings.Repeat("g", 64)},
		"trailing space": {Algo: filesapi.AlgoSHA256, Hex: h256[:63] + " "},
	} {
		if _, err := uploadChecked(s, "a.jar", false, "x", sum); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if _, ok := get(t, data, "a.jar"); ok {
		t.Fatal("an upload with an invalid checksum was stored")
	}
	noTemp(t, data)
}

// Computing both digests keeps the upload a stream.
func TestUploadCheckedStreamsWithoutBuffering(t *testing.T) {
	const size = 64 << 20
	s, data := newService(t, func(c *Config) { c.Limits.MaxUploadBytes = size })
	h := sha512.New()
	if _, err := io.Copy(h, &zeroReader{n: size}); err != nil {
		t.Fatal(err)
	}
	sum := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex.EncodeToString(h.Sum(nil))}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	res, err := s.UploadChecked(ctx, 1, "world.zip", false, size, sum, &zeroReader{n: size})
	runtime.ReadMemStats(&after)
	if err != nil || res.SHA512 != sum.Hex {
		t.Fatalf("checked 64 MiB upload: %+v %v", res, err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 16<<20 {
		t.Fatalf("a %d MiB checked upload allocated %d MiB", size>>20, grew>>20)
	}
	if fi, err := os.Stat(filepath.Join(data, "world.zip")); err != nil || fi.Size() != size {
		t.Fatalf("stored size: %v", err)
	}
}

// putUpload sends content as PUT /v1/upload with query q.
func putUpload(t *testing.T, ts *httptest.Server, q url.Values, content string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, ts.URL+filesapi.PathUpload+"?"+q.Encode(), strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestServerUploadChecksumParameters(t *testing.T) {
	s, data := newService(t)
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	const content = "plugin"
	h256, h512 := sums(content)
	query := func(path string, kv ...string) url.Values {
		v := url.Values{"id": {"1"}, "path": {path}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Add(kv[i], kv[i+1])
		}
		return v
	}

	status, body := putUpload(t, ts, query("a.jar", filesapi.QuerySHA512, h512), content)
	if status != http.StatusCreated {
		t.Fatalf("checked upload: %d %s", status, body)
	}
	var res filesapi.UploadResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if res.Size != int64(len(content)) || res.SHA256 != h256 || res.SHA512 != h512 {
		t.Fatalf("checked upload answer: %s", body)
	}
	status, body = putUpload(t, ts, query("plain.jar"), content)
	if status != http.StatusCreated || strings.Contains(string(body), "sha") {
		t.Fatalf("plain upload: %d %s, want {size} only", status, body)
	}

	for name, q := range map[string]url.Values{
		"both":      query("b.jar", filesapi.QuerySHA256, h256, filesapi.QuerySHA512, h512),
		"twice":     query("b.jar", filesapi.QuerySHA256, h256, filesapi.QuerySHA256, h256),
		"empty":     query("b.jar", filesapi.QuerySHA256, ""),
		"uppercase": query("b.jar", filesapi.QuerySHA256, strings.ToUpper(h256)),
		"too short": query("b.jar", filesapi.QuerySHA512, h256),
		"not hex":   query("b.jar", filesapi.QuerySHA256, strings.Repeat("z", 64)),
	} {
		status, body := putUpload(t, ts, q, content)
		if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
			t.Errorf("%s: %d %s, want 400 invalid", name, status, body)
		}
	}

	status, body = putUpload(t, ts, query("c.jar", filesapi.QuerySHA512, strings.Repeat("0", 128)), content)
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeHashMismatch {
		t.Fatalf("mismatch: %d %s, want 400 hash_mismatch", status, body)
	}
	for _, n := range []string{"b.jar", "c.jar"} {
		if _, ok := get(t, data, n); ok {
			t.Errorf("%s was stored", n)
		}
	}
	noTemp(t, data)
}
```

`internal/files/plugins_e2e_test.go` (Aufgabe 5 hängt `TestJarsEndToEnd` an):

```go
package files

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// The checksum parameters and hash_mismatch reach the filesapi client (V1) over the socket.
func TestUploadCheckedEndToEnd(t *testing.T) {
	c, data := startUnix(t, uint32(os.Getuid()))
	bg := context.Background()
	put(t, data, "plugins/keep.txt", "")
	const content = "end to end jar"
	h256, h512 := sums(content)

	res, err := c.UploadChecked(bg, 1, "plugins/E2E.jar", false, int64(len(content)),
		filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: h512}, strings.NewReader(content))
	if err != nil || res.Size != int64(len(content)) || res.SHA256 != h256 || res.SHA512 != h512 {
		t.Fatalf("checked upload: %+v %v", res, err)
	}
	if v, _ := get(t, data, "plugins/E2E.jar"); v != content {
		t.Fatalf("stored %q", v)
	}
	_, err = c.UploadChecked(bg, 1, "plugins/Bad.jar", false, int64(len(content)),
		filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: strings.Repeat("0", 64)}, strings.NewReader(content))
	if !errors.Is(err, filesapi.ErrHashMismatch) || errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("mismatch over the client: %v, want ErrHashMismatch", err)
	}
	if _, ok := get(t, data, "plugins/Bad.jar"); ok {
		t.Fatal("a mismatching upload became visible")
	}
	noTemp(t, data)
}
```

In `internal/files/service_test.go`, `TestStatusFor`, nach der Zeile mit `filesapi.ErrInvalid` einfügen:

```go
		{filesapi.ErrHashMismatch, http.StatusBadRequest, filesapi.CodeHashMismatch},
```

- [ ] **Step 2: `make check` – rot**

Run: `git add -N internal/files && make check`
Expected: FAIL mit `s.UploadChecked undefined (type *Service has no field or method UploadChecked)`.

- [ ] **Step 3: Implementieren**

`internal/files/transfer.go`: im Import-Block `"crypto/sha256"`, `"crypto/sha512"` und `"encoding/hex"` ergänzen (nach `"context"`) und die Funktion `Upload` (vom Kommentar `// Upload stores body …` bis vor `// bodyReader records …`) vollständig ersetzen durch:

```go
// Upload stores body (exactly size bytes) at path via a temp file next to it; the target
// only changes when every byte arrived. At most maxUploads run at the same time.
func (s *Service) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (filesapi.UploadResponse, error) {
	return s.UploadChecked(ctx, id, p, overwrite, size, filesapi.Checksum{}, body)
}

// UploadChecked is Upload with an expected digest (spec 3c §5.2). With a non-zero sum it
// computes sha256 and sha512 while it writes the temp file and compares them before fsync and
// rename: a mismatch is ErrHashMismatch, the temp file is removed and the target stays as it
// was. Only a checked upload answers with both digests; the zero Checksum means no check.
func (s *Service) UploadChecked(ctx context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	checked := sum != (filesapi.Checksum{})
	if checked {
		if err := sum.Validate(); err != nil {
			return filesapi.UploadResponse{}, err
		}
	}
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
	res, err := s.reserve(id, size)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	defer res.release()

	tmp, err := s.tempName(parentOf(clean))
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
	src := &bodyReader{r: body}
	h256, h512 := sha256.New(), sha512.New()
	var dst io.Writer = landingWriter{w: f, res: res}
	if checked {
		dst = io.MultiWriter(dst, h256, h512) // one pass: both digests while the bytes land
	}
	n, err := io.Copy(dst, io.LimitReader(src, size+1))
	if src.err != nil {
		// The caller's body broke off or stalled: its problem, not an internal error (the panel
		// would otherwise report the service as unavailable).
		err = src.problem()
	}
	if err == nil && n != size {
		err = fmt.Errorf("%w: received %d bytes, Content-Length announced %d", filesapi.ErrInvalid, n, size)
	}
	if err == nil {
		err = ctx.Err()
	}
	out := filesapi.UploadResponse{Size: n}
	if err == nil && checked {
		out.SHA256, out.SHA512 = hex.EncodeToString(h256.Sum(nil)), hex.EncodeToString(h512.Sum(nil))
		if !sum.Matches(out.SHA256, out.SHA512) {
			err = fmt.Errorf("%w: the upload does not match the expected %s checksum", filesapi.ErrHashMismatch, sum.Algo)
		}
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
	return out, nil
}
```

`internal/files/server.go`: den Kommentar über `func (s *Server) upload` ersetzen durch

```go
// upload: PUT ?id=&path=&overwrite=0|1[&sha256=<hex>|&sha512=<hex>], body = file. The
// server-wide deadlines do not apply; every read gets ioTimeout. A checksum mismatch is only
// known after the whole body: it is answered like any late error (400 hash_mismatch).
```

und in `receive` die letzten beiden Zeilen

```go
	_ = rc.SetWriteDeadline(time.Time{})
	return s.svc.Upload(r.Context(), id, q.Get("path"), ow == "1", r.ContentLength, body)
```

ersetzen durch

```go
	sum, ok, err := filesapi.ChecksumFromQuery(q)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	if !ok {
		sum = filesapi.Checksum{}
	}
	_ = rc.SetWriteDeadline(time.Time{})
	return s.svc.UploadChecked(r.Context(), id, q.Get("path"), ow == "1", r.ContentLength, sum, body)
```

Ein ungültiger Parameter fällt so vor dem Lesen des Bodys auf; `upload` antwortet dann über `failEarly` (wie bei `413`/`409`). Die Abweichung der Prüfsumme kommt erst nach dem letzten Byte (`body.eof`), also über `s.fail` – der Status kommt aus `statusFor` → `filesapi.CodeOf` (`400 hash_mismatch`, Phase A).

`internal/files/service.go`, in `fsErr` die Sentinel-Liste ergänzen:

```go
	case errors.Is(err, filesapi.ErrNotFound), errors.Is(err, filesapi.ErrExists), errors.Is(err, filesapi.ErrModified),
		errors.Is(err, filesapi.ErrNotRegular), errors.Is(err, filesapi.ErrNotText), errors.Is(err, filesapi.ErrTooLarge),
		errors.Is(err, filesapi.ErrInsufficientStorage), errors.Is(err, filesapi.ErrBusy), errors.Is(err, filesapi.ErrInvalid),
		errors.Is(err, filesapi.ErrHashMismatch):
		return err
```

- [ ] **Step 4: `make check` – grün**

Run: `make check`
Expected: PASS (Format, Vet, Build macOS + Linux, tsc).

- [ ] **Step 5: Commit, Push, Draft-PR, CI**

```bash
git add internal/files
git commit -m "feat(files): verify an upload's sha256 or sha512 before the rename

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/plugins-files
gh pr create --draft --title "feat(files): checked uploads, jar listing and jar metadata for plugins" --body "Phase B1 of docs/superpowers/plans/2026-10-12-plugins-00-overview.md (Tasks 4–6, plan docs/superpowers/plans/2026-10-12-plugins-02-files.md).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
gh pr checks <pr> --watch
```
Expected: CI grün; `TestUploadCheckedAcceptsMatchingDigest`, `TestUploadCheckedMismatchLeavesNothing`, `TestUploadCheckedRefusesInvalidChecksums`, `TestUploadCheckedStreamsWithoutBuffering`, `TestServerUploadChecksumParameters`, `TestUploadCheckedEndToEnd`, `TestStatusFor` PASS (auch unter `-race`); alle bisherigen Upload-Tests (`TestUploadWritesAtomically`, `TestUploadLimits`, `TestUploadStreamsWithoutBuffering`, `TestUploadEarlyErrorReachesClient` …) bleiben grün. Schlägt `TestUploadCheckedEndToEnd` fehl, weil der Client `400 hash_mismatch` nicht als `ErrHashMismatch` meldet, ist das Phase A: dem Lead melden.

---

### Task 5: `POST /v1/jars` mit Prüfsummen-Cache

**Files:**
- Create: `internal/files/hashcache.go`, `internal/files/filekey_linux.go`, `internal/files/filekey_darwin.go`, `internal/files/filekey_other.go`, `internal/files/jars.go`
- Modify: `internal/files/service.go` (Konstanten, Felder, `New`), `internal/files/server.go` (Route + Handler)
- Test: `internal/files/hashcache_test.go`, `internal/files/jars_test.go`, `internal/files/jars_linux_test.go`, `internal/files/plugins_e2e_test.go` (anhängen)

**Interfaces:**
- Consumes: `filesapi.PathJars`, `DirPlugins`, `DirMods`, `MaxJars`, `MaxJarHashBytes`, `ValidJarDir`, `IsJarName`, `IsHidden`, `Join`, `JarsRequest`, `JarsResponse`, `Jar`, `JarMeta`, `MaxSmallJSONBytes`, `ErrInvalid`, `ErrNotFound`, `ErrNotRegular`, `(*filesapi.UnixClient).Jars` (e2e); vorhanden in `internal/files`: `s.open`, `openDir`, `lstat`, `openRegular`, `fsErr`, `listBatch`, `ctxReader` (extract.go), `Server.decode`, `Server.longOp`, `Server.fail`, `writeJSON`, `Server.testHook`; Test-Helfer `newService`, `put`, `layout`, `postJSON`, `errCode`, `startHTTP`, `startUnix`, `testStart`, `sums` (Aufgabe 4), `ctx`.
- Produces (für Aufgabe 6 und die Tests):

```go
func (s *Service) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error)

// intern:
const maxHashRuns = 2
const hashCacheSize = 4096
type devID = uint64 // linux, other; darwin: int32
type jarKey struct{ dev devID; ino uint64; size, mtime int64 }
type jarInfo struct{ sha256, sha512 string; meta *filesapi.JarMeta }
func newHashCache(limit int) *hashCache
func (c *hashCache) get(k jarKey) (jarInfo, bool)
func (c *hashCache) put(k jarKey, info jarInfo)
func (c *hashCache) size() int
func fileKeyOf(fi fs.FileInfo) (jarKey, bool)
type jarFile struct{ name string; fi fs.FileInfo }
func findJars(root *os.Root, dir string) ([]jarFile, bool, error)
func (s *Service) describeJar(ctx context.Context, root *os.Root, dir string, jf jarFile) (filesapi.Jar, bool, error)
func (s *Service) jarInfo(ctx context.Context, name string, f *os.File, st fs.FileInfo) (jarInfo, error)
func hashJar(ctx context.Context, r io.ReaderAt, size int64) (string, string, error)
// Service-Felder: hashSlots chan struct{}, jarCache *hashCache, jarHashMax int64, jarHook func(name string)
func (s *Server) jars(w http.ResponseWriter, r *http.Request)

// Test-Helfer (jars_test.go), auch von Aufgabe 6 benutzt:
func listJars(t *testing.T, s *Service, dir string) filesapi.JarsResponse
func jarNames(res filesapi.JarsResponse) []string
func jarByName(t *testing.T, res filesapi.JarsResponse, name string) filesapi.Jar
type countingHook struct{ … } // hook(name), count(name)
```
Draht: `POST /v1/jars {id, dir}` → `200 JarsResponse`, `400 invalid`, `405` bei `GET`.

- [ ] **Step 1: Tests schreiben (rot)**

`internal/files/hashcache_test.go`:

```go
package files

import "testing"

func TestHashCacheEvictsLeastRecentlyUsed(t *testing.T) {
	if hashCacheSize != 4096 || maxHashRuns != 2 {
		t.Fatalf("limits changed: cache %d, runs %d (spec 3c §5.1: 4096, 2)", hashCacheSize, maxHashRuns)
	}
	c := newHashCache(2)
	key := func(ino uint64) jarKey { return jarKey{ino: ino, size: 1, mtime: 1} }
	c.put(key(1), jarInfo{sha256: "1"})
	c.put(key(2), jarInfo{sha256: "2"})
	if _, ok := c.get(key(1)); !ok { // 1 is now the most recently used
		t.Fatal("entry 1 missing")
	}
	c.put(key(3), jarInfo{sha256: "3"})
	if _, ok := c.get(key(2)); ok {
		t.Fatal("the least recently used entry was not evicted")
	}
	for _, k := range []uint64{1, 3} {
		if info, ok := c.get(key(k)); !ok || info.sha256 == "" {
			t.Fatalf("entry %d missing", k)
		}
	}
	c.put(key(3), jarInfo{sha256: "3b"}) // update in place
	if info, _ := c.get(key(3)); info.sha256 != "3b" || c.size() != 2 {
		t.Fatalf("update: %+v, size %d", info, c.size())
	}
	// Any part of the key distinguishes entries.
	if _, ok := c.get(jarKey{ino: 3, size: 1, mtime: 2}); ok {
		t.Fatal("another mtime hit the cache")
	}
	if _, ok := c.get(jarKey{ino: 3, size: 2, mtime: 1}); ok {
		t.Fatal("another size hit the cache")
	}
}
```

`internal/files/jars_test.go`:

```go
package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func listJars(t *testing.T, s *Service, dir string) filesapi.JarsResponse {
	t.Helper()
	res, err := s.Jars(ctx, 1, dir)
	if err != nil {
		t.Fatalf("jars %s: %v", dir, err)
	}
	if res.Jars == nil {
		t.Fatal("jars is nil, want a list")
	}
	return res
}

func jarNames(res filesapi.JarsResponse) []string {
	out := make([]string, 0, len(res.Jars))
	for _, j := range res.Jars {
		out = append(out, j.Name)
	}
	return out
}

func jarByName(t *testing.T, res filesapi.JarsResponse, name string) filesapi.Jar {
	t.Helper()
	for _, j := range res.Jars {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("%s not listed: %v", name, jarNames(res))
	return filesapi.Jar{}
}

// countingHook counts the computed (not cached) jars by name.
type countingHook struct {
	mu sync.Mutex
	n  map[string]int
}

func (h *countingHook) hook(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == nil {
		h.n = map[string]int{}
	}
	h.n[name]++
}

func (h *countingHook) count(name string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n[name]
}

func TestJarsListsJarFiles(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/LuckPerms.jar", "lp")
	put(t, data, "plugins/x.jar", "x-on")
	put(t, data, "plugins/x.jar.disabled", "x-off")
	put(t, data, "plugins/OLD.JAR.DISABLED", "old")
	put(t, data, "plugins/readme.txt", "no")
	put(t, data, "plugins/notes.jar.bak", "no")
	put(t, data, "plugins/.jar", "no name")
	put(t, data, "plugins/.mcpanel-tmp-abc.jar", "hidden temp file")
	put(t, data, "plugins/LuckPerms/config.yml", "folder content")
	put(t, data, "plugins/sub/deep.jar", "not on the top level")
	if err := os.Mkdir(filepath.Join(data, "plugins", "folder.jar"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := listJars(t, s, filesapi.DirPlugins)
	if want := []string{"LuckPerms.jar", "OLD.JAR.DISABLED", "x.jar", "x.jar.disabled"}; !reflect.DeepEqual(jarNames(res), want) {
		t.Fatalf("jars %v, want %v", jarNames(res), want)
	}
	if res.Truncated {
		t.Fatal("truncated without reason")
	}
	for name, c := range map[string]struct {
		content string
		enabled bool
	}{
		"LuckPerms.jar": {"lp", true}, "OLD.JAR.DISABLED": {"old", false},
		"x.jar": {"x-on", true}, "x.jar.disabled": {"x-off", false},
	} {
		j := jarByName(t, res, name)
		h256, h512 := sums(c.content)
		fi, err := os.Stat(filepath.Join(data, "plugins", name))
		if err != nil {
			t.Fatal(err)
		}
		if j.Enabled != c.enabled || j.Size != int64(len(c.content)) || j.MTime != fi.ModTime().UnixMilli() ||
			j.SHA256 != h256 || j.SHA512 != h512 || j.Meta != nil {
			t.Errorf("%s: %+v", name, j)
		}
	}
	// Review focus 2: x.jar and x.jar.disabled are two jars with their own content.
	if jarByName(t, res, "x.jar").SHA512 == jarByName(t, res, "x.jar.disabled").SHA512 {
		t.Fatal("x.jar and x.jar.disabled share a checksum")
	}
}

// Review focus 2: names that differ only in case are separate jars (Linux is case-sensitive).
func TestJarsKeepsCaseVariantsApart(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/Foo.jar", "upper")
	if _, err := os.Stat(filepath.Join(data, "plugins", "foo.JAR")); err == nil {
		t.Skip("case-insensitive file system")
	}
	put(t, data, "plugins/foo.JAR", "lower")
	res := listJars(t, s, filesapi.DirPlugins)
	if want := []string{"Foo.jar", "foo.JAR"}; !reflect.DeepEqual(jarNames(res), want) {
		t.Fatalf("jars %v, want %v", jarNames(res), want)
	}
	up256, _ := sums("upper")
	low256, _ := sums("lower")
	if jarByName(t, res, "Foo.jar").SHA256 != up256 || jarByName(t, res, "foo.JAR").SHA256 != low256 {
		t.Fatalf("checksums mixed up: %+v", res.Jars)
	}
	if !jarByName(t, res, "foo.JAR").Enabled {
		t.Fatal(".JAR is an enabled jar")
	}
}

func TestJarsMissingOrOddFolder(t *testing.T) {
	s, data := newService(t)
	if res := listJars(t, s, filesapi.DirPlugins); len(res.Jars) != 0 || res.Truncated {
		t.Fatalf("missing folder: %+v", res)
	}
	put(t, data, "mods", "a file, not a folder")
	if res := listJars(t, s, filesapi.DirMods); len(res.Jars) != 0 {
		t.Fatalf("file instead of a folder: %+v", res)
	}
	// A server that never ran has no data directory: no jars, no error.
	res, err := s.Jars(ctx, 2, filesapi.DirPlugins)
	if err != nil || res.Jars == nil || len(res.Jars) != 0 {
		t.Fatalf("server without data dir: %+v %v", res, err)
	}
	for _, dir := range []string{"", "world", "Plugins", "plugins/", "../plugins", "plugins/sub"} {
		if _, err := s.Jars(ctx, 1, dir); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("dir %q: %v, want ErrInvalid", dir, err)
		}
	}
	if _, err := s.Jars(ctx, 0, filesapi.DirPlugins); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v, want ErrInvalid", err)
	}
}

// Review focus 5: more than filesapi.MaxJars jars.
func TestJarsTruncates(t *testing.T) {
	s, data := newService(t)
	for i := range filesapi.MaxJars {
		put(t, data, fmt.Sprintf("mods/m%03d.jar", i), "m"+strconv.Itoa(i))
	}
	for i := range 5 {
		put(t, data, fmt.Sprintf("mods/readme%d.txt", i), "not a jar") // never counted
	}
	res := listJars(t, s, filesapi.DirMods)
	if len(res.Jars) != filesapi.MaxJars || res.Truncated {
		t.Fatalf("exactly %d jars: %d listed, truncated=%v", filesapi.MaxJars, len(res.Jars), res.Truncated)
	}
	put(t, data, "mods/zzz.jar", "one more")
	res = listJars(t, s, filesapi.DirMods)
	if len(res.Jars) != filesapi.MaxJars || !res.Truncated {
		t.Fatalf("%d jars: %d listed, truncated=%v", filesapi.MaxJars+1, len(res.Jars), res.Truncated)
	}
	if !sort.StringsAreSorted(jarNames(res)) {
		t.Fatal("not sorted by name")
	}
}

// Review focus 5: jars above the checksum limit (256 MiB, here lowered) come without checksums.
func TestJarsChecksumLimit(t *testing.T) {
	s, data := newService(t)
	if s.jarHashMax != filesapi.MaxJarHashBytes {
		t.Fatalf("default checksum limit %d, want filesapi.MaxJarHashBytes", s.jarHashMax)
	}
	s.jarHashMax = 8
	put(t, data, "plugins/small.jar", "12345678")
	put(t, data, "plugins/big.jar", "123456789")
	res := listJars(t, s, filesapi.DirPlugins)
	small, big := jarByName(t, res, "small.jar"), jarByName(t, res, "big.jar")
	if h256, h512 := sums("12345678"); small.SHA256 != h256 || small.SHA512 != h512 {
		t.Fatalf("jar at the limit: %+v", small)
	}
	if big.SHA256 != "" || big.SHA512 != "" || big.Size != 9 {
		t.Fatalf("jar above the limit: %+v, want no checksums", big)
	}
}

func TestJarsCachesByFileIdentity(t *testing.T) {
	s, data := newService(t)
	var h countingHook
	s.jarHook = h.hook
	put(t, data, "plugins/a.jar", "aaaa")
	put(t, data, "plugins/b.jar", "bbbb")
	first := listJars(t, s, filesapi.DirPlugins)
	if h.count("a.jar") != 1 || h.count("b.jar") != 1 {
		t.Fatalf("first listing computed %v", h.n)
	}
	if second := listJars(t, s, filesapi.DirPlugins); !reflect.DeepEqual(first, second) {
		t.Fatalf("cached listing differs: %+v vs %+v", first, second)
	}
	if h.count("a.jar") != 1 || h.count("b.jar") != 1 {
		t.Fatalf("second listing computed again: %v", h.n)
	}

	// Same size, new content and a new mtime: computed again.
	put(t, data, "plugins/a.jar", "AAAA")
	later := testStart.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(data, "plugins", "a.jar"), later, later); err != nil {
		t.Fatal(err)
	}
	third := listJars(t, s, filesapi.DirPlugins)
	if h256, _ := sums("AAAA"); jarByName(t, third, "a.jar").SHA256 != h256 || h.count("a.jar") != 2 {
		t.Fatalf("changed jar: %+v, computed %d times", jarByName(t, third, "a.jar"), h.count("a.jar"))
	}

	// Toggling renames the jar: same inode, size and mtime, so the cache still applies.
	if err := os.Rename(filepath.Join(data, "plugins", "b.jar"), filepath.Join(data, "plugins", "b.jar.disabled")); err != nil {
		t.Fatal(err)
	}
	fourth := listJars(t, s, filesapi.DirPlugins)
	off := jarByName(t, fourth, "b.jar.disabled")
	if b256, _ := sums("bbbb"); off.SHA256 != b256 || off.Enabled || h.count("b.jar.disabled") != 0 {
		t.Fatalf("renamed jar: %+v, computed %d times", off, h.count("b.jar.disabled"))
	}
	if n := s.jarCache.size(); n > hashCacheSize {
		t.Fatalf("cache holds %d entries", n)
	}
}

func TestJarsWaitForAHashSlot(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/a.jar", "a")
	listJars(t, s, filesapi.DirPlugins) // a.jar is cached now

	// Both slots taken: a cached jar needs none ...
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	if res := listJars(t, s, filesapi.DirPlugins); len(res.Jars) != 1 || res.Jars[0].SHA256 == "" {
		t.Fatalf("cached jar while the slots are taken: %+v", res)
	}
	// ... a new one waits and gives up with its context.
	put(t, data, "plugins/b.jar", "b")
	wait, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := s.Jars(wait, 1, filesapi.DirPlugins); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("no slot: %v, want the context's error", err)
	}
	<-s.hashSlots
	<-s.hashSlots
	if res := listJars(t, s, filesapi.DirPlugins); len(res.Jars) != 2 {
		t.Fatalf("after the slots are free: %+v", res)
	}

	// Never more than maxHashRuns passes at once, however many requests run.
	var active, peak atomic.Int32
	s.jarHook = func(string) {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for id := int64(2); id <= 7; id++ {
		dir := filepath.Join(s.cfg.InstancesDir, "instances", strconv.FormatInt(id, 10))
		for i := range 3 {
			put(t, dir, fmt.Sprintf("plugins/p%d.jar", i), fmt.Sprintf("server %d jar %d", id, i))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Jars(ctx, id, filesapi.DirPlugins)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent listing: %v", err)
		}
	}
	if p := peak.Load(); p > maxHashRuns || p == 0 {
		t.Fatalf("%d passes ran at once, want 1..%d", p, maxHashRuns)
	}
}

func TestServerJars(t *testing.T) {
	s, data := newService(t)
	put(t, data, "mods/lithium.jar", "lith")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	status, body := postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirMods})
	if status != http.StatusOK {
		t.Fatalf("jars: %d %s", status, body)
	}
	var res filesapi.JarsResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if _, h512 := sums("lith"); len(res.Jars) != 1 || res.Jars[0].Name != "lithium.jar" || res.Jars[0].SHA512 != h512 {
		t.Fatalf("jars answer: %s", body)
	}
	status, body = postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirPlugins})
	if status != http.StatusOK || !strings.Contains(string(body), `"jars":[]`) {
		t.Fatalf("empty folder: %d %s, want \"jars\":[]", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: "world"})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("bad dir: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathJars, map[string]any{"id": 1, "dir": "mods", "path": "x"})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("unknown field: %d %s", status, body)
	}
	resp, err := http.Get(ts.URL + filesapi.PathJars)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d, want 405", resp.StatusCode)
	}
}

// Hashing many jars may take longer than the server's WriteTimeout; the answer still arrives.
func TestJarsOutliveWriteTimeout(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/a.jar", "a")
	srv := NewServer(s, "test", discardLog())
	srv.testHook = func(op string) {
		if op == "jars" {
			time.Sleep(300 * time.Millisecond)
		}
	}
	ts := httptest.NewUnstartedServer(srv)
	ts.Config.WriteTimeout = 100 * time.Millisecond
	ts.Start()
	t.Cleanup(ts.Close)
	if status, body := postJSON(t, ts, filesapi.PathJars, filesapi.JarsRequest{ID: 1, Dir: filesapi.DirPlugins}); status != http.StatusOK {
		t.Fatalf("slow jars: %d %s", status, body)
	}
}
```

`internal/files/jars_linux_test.go` (Symlinks und FIFOs nur unter Linux, wie `umask_linux_test.go`; die CI läuft auf Linux):

```go
//go:build linux

package files

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Review focus 5: symlinked jars (out of the instance and inside it), a FIFO and a folder
// with a .jar name are left out; nothing outside the data directory is read, nothing blocks.
func TestJarsSkipsLinksAndSpecialFiles(t *testing.T) {
	s, data := newService(t)
	outside := layout(t, data) // plugins/EssentialsX.jar is a regular file
	plugins := filepath.Join(data, "plugins")
	if err := os.Symlink(outside, filepath.Join(plugins, "secret.jar")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("EssentialsX.jar", filepath.Join(plugins, "alias.jar")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(plugins, "pipe.jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(plugins, "dir.jar.disabled"), 0o755); err != nil {
		t.Fatal(err)
	}
	type result struct {
		res filesapi.JarsResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := s.Jars(ctx, 1, filesapi.DirPlugins)
		done <- result{res, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("jars blocks on a FIFO")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	if want := []string{"EssentialsX.jar"}; !reflect.DeepEqual(jarNames(r.res), want) {
		t.Fatalf("jars %v, want %v", jarNames(r.res), want)
	}
	if h256, _ := sums("PK\x00\x01binary"); r.res.Jars[0].SHA256 != h256 {
		t.Fatalf("checksum of the regular jar: %+v", r.res.Jars[0])
	}
}

// A plugins or mods folder that is a symlink is never read, wherever it points.
func TestJarsIgnoresSymlinkedFolder(t *testing.T) {
	s, data := newService(t)
	put(t, data, "real/a.jar", "a")
	if err := os.Symlink("real", filepath.Join(data, "plugins")); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(s.cfg.InstancesDir, "elsewhere")
	put(t, elsewhere, "b.jar", "b")
	if err := os.Symlink(elsewhere, filepath.Join(data, "mods")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filesapi.DirPlugins, filesapi.DirMods} {
		if res := listJars(t, s, dir); len(res.Jars) != 0 {
			t.Errorf("%s through a symlink: %v", dir, jarNames(res))
		}
	}
}

// A jar swapped for a FIFO after findJars saw it: describeJar skips it without blocking.
func TestDescribeJarSkipsSwappedFIFO(t *testing.T) {
	s, data := newService(t)
	put(t, data, "plugins/real.jar", "x")
	if err := syscall.Mkfifo(filepath.Join(data, "plugins", "pipe.jar"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fi, err := root.Lstat("plugins/real.jar")
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		_, ok, err := s.describeJar(ctx, root, filesapi.DirPlugins, jarFile{name: "pipe.jar", fi: fi})
		done <- result{ok, err}
	}()
	select {
	case r := <-done:
		if r.ok || r.err != nil {
			t.Fatalf("swapped FIFO: ok=%v err=%v, want skipped", r.ok, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("describeJar blocks on a FIFO")
	}
}
```

An `internal/files/plugins_e2e_test.go` anhängen (die Importe aus Aufgabe 4 reichen):

```go
// Jars over the socket: the client gets the service's list (V1 wire types).
func TestJarsEndToEnd(t *testing.T) {
	c, data := startUnix(t, uint32(os.Getuid()))
	bg := context.Background()
	put(t, data, "plugins/A.jar", "a")
	put(t, data, "plugins/B.jar.disabled", "b")
	res, err := c.Jars(bg, 1, filesapi.DirPlugins)
	if err != nil {
		t.Fatalf("jars: %v", err)
	}
	if len(res.Jars) != 2 || res.Truncated {
		t.Fatalf("jars: %+v", res)
	}
	_, a512 := sums("a")
	_, b512 := sums("b")
	if a, b := res.Jars[0], res.Jars[1]; a.Name != "A.jar" || !a.Enabled || a.SHA512 != a512 ||
		b.Name != "B.jar.disabled" || b.Enabled || b.SHA512 != b512 {
		t.Fatalf("jars: %+v", res.Jars)
	}
	empty, err := c.Jars(bg, 1, filesapi.DirMods)
	if err != nil || empty.Jars == nil || len(empty.Jars) != 0 {
		t.Fatalf("empty mods: %+v %v", empty, err)
	}
	if _, err := c.Jars(bg, 1, "world"); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("bad dir: %v, want ErrInvalid", err)
	}
}
```

- [ ] **Step 2: `make check` – rot**

Run: `git add -N internal/files && make check`
Expected: FAIL mit u. a. `undefined: newHashCache`, `undefined: jarKey`, `s.Jars undefined`, `s.jarHook undefined`, `undefined: jarFile`.

- [ ] **Step 3: Cache und Plattform-Schlüssel implementieren**

`internal/files/hashcache.go`:

```go
package files

import (
	"container/list"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// jarKey identifies the content of a jar without reading it (spec 3c §5.1): device and inode
// (fileKeyOf, per platform), size and mtime in nanoseconds. A replaced jar (new upload,
// rename over it) or one changed in place (new size or mtime) gets a new key; a renamed one
// (toggle) keeps its key.
type jarKey struct {
	dev   devID
	ino   uint64
	size  int64
	mtime int64
}

// jarInfo is what the service computes per jar.
type jarInfo struct {
	sha256, sha512 string            // lowercase hex; "" above jarHashMax
	meta           *filesapi.JarMeta // nil = not readable; shared between answers, never modified
}

type cacheEntry struct {
	key  jarKey
	info jarInfo
}

// hashCache keeps the jarInfo of at most limit jars in memory; when it is full, the least
// recently used entry goes. Safe for concurrent use.
type hashCache struct {
	mu    sync.Mutex
	limit int
	order *list.List // of *cacheEntry, front = most recently used
	items map[jarKey]*list.Element
}

func newHashCache(limit int) *hashCache {
	return &hashCache{limit: limit, order: list.New(), items: make(map[jarKey]*list.Element)}
}

func (c *hashCache) get(k jarKey) (jarInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return jarInfo{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).info, true
}

func (c *hashCache) put(k jarKey, info jarInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*cacheEntry).info = info
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&cacheEntry{key: k, info: info})
	for c.order.Len() > c.limit {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.items, old.Value.(*cacheEntry).key)
	}
}

// size returns the number of cached jars.
func (c *hashCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
```

`internal/files/filekey_linux.go`:

```go
//go:build linux

package files

import (
	"io/fs"
	"syscall"
)

// devID is the type of syscall.Stat_t.Dev on this platform.
type devID = uint64

// fileKeyOf returns the cache key of a file from its stat data; ok is false when the
// platform does not provide device and inode.
func fileKeyOf(fi fs.FileInfo) (jarKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return jarKey{}, false
	}
	return jarKey{dev: st.Dev, ino: st.Ino, size: fi.Size(), mtime: fi.ModTime().UnixNano()}, true
}
```

`internal/files/filekey_darwin.go`:

```go
//go:build darwin

package files

import (
	"io/fs"
	"syscall"
)

// devID is the type of syscall.Stat_t.Dev on this platform.
type devID = int32

// fileKeyOf returns the cache key of a file from its stat data; ok is false when the
// platform does not provide device and inode.
func fileKeyOf(fi fs.FileInfo) (jarKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return jarKey{}, false
	}
	return jarKey{dev: st.Dev, ino: st.Ino, size: fi.Size(), mtime: fi.ModTime().UnixNano()}, true
}
```

`internal/files/filekey_other.go`:

```go
//go:build !linux && !darwin

package files

import "io/fs"

type devID = uint64

// fileKeyOf: no device and inode here, so jars are never cached (always computed).
func fileKeyOf(fs.FileInfo) (jarKey, bool) { return jarKey{}, false }
```

`devID` ist pro Plattform ein Alias auf den Typ von `syscall.Stat_t.Dev`, deshalb braucht `fileKeyOf` keine Konvertierung (kein `unconvert`/G115-Thema) und `jarKey` bleibt vergleichbar (Map-Schlüssel).

- [ ] **Step 4: Service-Felder ergänzen**

`internal/files/service.go`, im `const`-Block nach `cleanupMaxDepth`:

```go
	maxHashRuns      = 2    // concurrent jar checksum and metadata passes (spec 3c §5.1)
	hashCacheSize    = 4096 // cached jars (spec 3c §5.1)
```

in `type Service struct` nach `jobs          *jobs`:

```go
	hashSlots     chan struct{}     // at most maxHashRuns jar passes at a time, for all requests
	jarCache      *hashCache        // checksums and metadata by file identity
	jarHashMax    int64             // larger jars get no checksums (filesapi.MaxJarHashBytes; tests lower it)
	jarHook       func(name string) // tests: runs before every jar that is computed, not cached
```

und in `New` im Literal nach `fileDownloads: make(chan struct{}, maxFileDownloads),`:

```go
		hashSlots:     make(chan struct{}, maxHashRuns),
		jarCache:      newHashCache(hashCacheSize),
		jarHashMax:    filesapi.MaxJarHashBytes,
```

(Danach `gofmt -w internal/files/service.go`, falls die Ausrichtung abweicht.)

- [ ] **Step 5: `Jars` implementieren**

`internal/files/jars.go`:

```go
package files

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// jarFile is a jar found on the top level of the folder.
type jarFile struct {
	name string
	fi   fs.FileInfo // Lstat when it was found
}

// Jars lists the plugin or mod jars on the top level of dir (spec 3c §5.1): regular files
// named *.jar or *.jar.disabled (any case), never symlinks, folders or special files, never
// hidden names; at most filesapi.MaxJars (Truncated), sorted by name. Each comes with sha256
// and sha512 (not above jarHashMax) and best-effort metadata, cached by file identity. A
// missing data directory or dir, or a dir that is not a real folder, is an empty list.
func (s *Service) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.JarsResponse{}, err
	}
	if !filesapi.ValidJarDir(dir) {
		return filesapi.JarsResponse{}, fmt.Errorf("%w: dir must be %q or %q", filesapi.ErrInvalid, filesapi.DirPlugins, filesapi.DirMods)
	}
	res := filesapi.JarsResponse{Jars: []filesapi.Jar{}}
	root, err := s.open(id)
	if errors.Is(err, filesapi.ErrNotFound) {
		return res, nil // the server never ran: no data directory, no jars
	}
	if err != nil {
		return filesapi.JarsResponse{}, err
	}
	defer root.Close()
	found, truncated, err := findJars(root, dir)
	if err != nil {
		return filesapi.JarsResponse{}, err
	}
	res.Truncated = truncated
	for _, jf := range found {
		jar, ok, err := s.describeJar(ctx, root, dir, jf)
		if err != nil {
			return filesapi.JarsResponse{}, err
		}
		if ok {
			res.Jars = append(res.Jars, jar)
		}
	}
	return res, nil
}

// findJars returns at most filesapi.MaxJars regular jar files on the top level of dir, sorted
// by name (byte order); truncated reports that there were more. Like List it stops reading
// at the limit, so which jars are left out follows the directory order.
func findJars(root *os.Root, dir string) ([]jarFile, bool, error) {
	d, _, err := openDir(root, dir)
	if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
		return []jarFile{}, false, nil // no folder, a file or a symlink: no jars
	}
	if err != nil {
		return nil, false, err
	}
	defer d.Close()
	out := []jarFile{}
	truncated := false
	for !truncated {
		batch, rerr := d.ReadDir(listBatch)
		for _, e := range batch {
			p := filesapi.Join(dir, e.Name())
			if _, ok := filesapi.IsJarName(e.Name()); !ok || filesapi.IsHidden(p) {
				continue
			}
			fi, lerr := lstat(root, p)
			if lerr != nil || !fi.Mode().IsRegular() {
				continue // vanished meanwhile, a symlink, a folder, a FIFO or a device
			}
			if len(out) == filesapi.MaxJars {
				truncated = true
				break
			}
			out = append(out, jarFile{name: e.Name(), fi: fi})
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(batch) == 0) {
			break
		}
		if rerr != nil {
			return nil, false, fsErr(rerr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, truncated, nil
}

// describeJar returns the entry of one jar; ok is false when it is no longer a regular file
// (removed, or swapped for a symlink or FIFO after findJars saw it). A jar the service cannot
// open for another reason (permissions) is listed without checksums and metadata.
func (s *Service) describeJar(ctx context.Context, root *os.Root, dir string, jf jarFile) (filesapi.Jar, bool, error) {
	enabled, _ := filesapi.IsJarName(jf.name)
	jar := filesapi.Jar{Name: jf.name, Enabled: enabled, Size: jf.fi.Size(), MTime: jf.fi.ModTime().UnixMilli()}
	p := filesapi.Join(dir, jf.name)
	f, st, err := openRegular(root, p, os.O_RDONLY)
	switch {
	case errors.Is(err, filesapi.ErrNotFound), errors.Is(err, filesapi.ErrNotRegular):
		return filesapi.Jar{}, false, nil
	case err != nil:
		s.log.Warn("files: cannot open jar", "path", p, "err", err)
		return jar, true, nil
	}
	defer f.Close()
	jar.Size, jar.MTime = st.Size(), st.ModTime().UnixMilli()
	info, err := s.jarInfo(ctx, jf.name, f, st)
	if err != nil {
		return filesapi.Jar{}, false, err
	}
	jar.SHA256, jar.SHA512 = info.sha256, info.sha512
	if info.meta != nil {
		m := *info.meta // the cached value stays untouched
		jar.Meta = &m
	}
	return jar, true, nil
}

// jarInfo returns checksums and metadata of the open jar f, from the cache when its identity
// is known. A computation takes one of maxHashRuns slots; a request waits for a slot as long
// as its context lives. A jar that changed while it was read gets nothing and is not cached.
func (s *Service) jarInfo(ctx context.Context, name string, f *os.File, st fs.FileInfo) (jarInfo, error) {
	key, keyed := fileKeyOf(st)
	if keyed {
		if info, ok := s.jarCache.get(key); ok {
			return info, nil
		}
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return jarInfo{}, ctx.Err()
	}
	if keyed { // computed by another request while this one waited
		if info, ok := s.jarCache.get(key); ok {
			return info, nil
		}
	}
	if s.jarHook != nil {
		s.jarHook(name)
	}
	var info jarInfo
	cacheable := keyed
	if st.Size() <= s.jarHashMax {
		h256, h512, err := hashJar(ctx, f, st.Size())
		switch {
		case ctx.Err() != nil:
			return jarInfo{}, ctx.Err()
		case err != nil:
			s.log.Warn("files: cannot hash jar", "name", name, "err", err)
			cacheable = false
		default:
			info.sha256, info.sha512 = h256, h512
		}
	}
	// A jar that is written to while it is read has no reliable checksum.
	if now, err := f.Stat(); err != nil || now.Size() != st.Size() || !now.ModTime().Equal(st.ModTime()) {
		return jarInfo{}, nil
	}
	if cacheable {
		s.jarCache.put(key, info)
	}
	return info, nil
}

// hashJar computes sha256 and sha512 of the first size bytes of r in one pass; it stops as
// soon as ctx ends (ctxReader from extract.go; the caller checks ctx.Err()).
func hashJar(ctx context.Context, r io.ReaderAt, size int64) (string, string, error) {
	h256, h512 := sha256.New(), sha512.New()
	src := ctxReader{ctx: ctx, r: io.NewSectionReader(r, 0, size)}
	if _, err := io.CopyN(io.MultiWriter(h256, h512), src, size); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h256.Sum(nil)), hex.EncodeToString(h512.Sum(nil)), nil
}
```

Hinweise:
- `findJars` ruft `lstat` (nicht `root.Lstat`) für jeden Kandidaten: ist `dir` nach `openDir` gegen einen Symlink getauscht worden, scheitert die Komponentenprüfung und das Jar fällt weg; `describeJar` prüft beim Öffnen über `openRegular` erneut (Lstat, `O_NOFOLLOW|O_NONBLOCK`, `os.SameFile`), eine untergeschobene FIFO blockiert nicht.
- Der Cache wird **vor** dem Warten auf einen Platz gefragt (Treffer kosten nichts) und **nach** dem Erhalt noch einmal (eine parallele Anfrage hat das Jar vielleicht gerade berechnet).
- `ctxReader` ist der vorhandene aus `extract.go` (liefert `errShuttingDown`, sobald `ctx` endet); `jarInfo` gibt in dem Fall `ctx.Err()` zurück, `Server.fail` antwortet einem gegangenen Aufrufer nicht mehr.

- [ ] **Step 6: Route und Handler**

`internal/files/server.go`, in `NewServer` die Routen-Map nach `filesapi.PathJobs` ergänzen:

```go
		filesapi.PathJars:     {method: http.MethodPost, handler: s.jars},
```

und am Ende der Datei:

```go
// jars lists the plugin or mod jars of a server. Hashing up to filesapi.MaxJars jars can take
// longer than the server's WriteTimeout (the client waits filesapi.JarsTimeout): longOp.
func (s *Server) jars(w http.ResponseWriter, r *http.Request) {
	var req filesapi.JarsRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var res filesapi.JarsResponse
	err := s.longOp(w, "jars", func() error {
		var err error
		res, err = s.svc.Jars(r.Context(), req.ID, req.Dir)
		return err
	})
	if err != nil {
		s.fail(w, r, "jars", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
```

- [ ] **Step 7: `make check` – grün**

Run: `make check`
Expected: PASS (Build macOS mit `filekey_darwin.go`, Linux mit `filekey_linux.go`).

- [ ] **Step 8: Commit, CI**

```bash
git add internal/files
git commit -m "feat(files): list plugin and mod jars with cached sha256 and sha512

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch
```
Expected: CI grün; `TestHashCacheEvictsLeastRecentlyUsed`, `TestJarsListsJarFiles`, `TestJarsKeepsCaseVariantsApart` (läuft auf Linux, überspringt sich nur auf case-insensitiven Dateisystemen), `TestJarsMissingOrOddFolder`, `TestJarsTruncates`, `TestJarsChecksumLimit`, `TestJarsCachesByFileIdentity`, `TestJarsWaitForAHashSlot`, `TestServerJars`, `TestJarsOutliveWriteTimeout`, `TestJarsSkipsLinksAndSpecialFiles`, `TestJarsIgnoresSymlinkedFolder`, `TestDescribeJarSkipsSwappedFIFO`, `TestJarsEndToEnd` PASS (auch unter `-race`). Meldet golangci-lint etwas (z. B. gosec in `jars.go`), gezielt mit `//nolint:<linter> // <Grund>` auf der Zeile oder einer Regel in `.golangci.yml` – letztere gehört nicht zu B1: dann dem Lead melden.

---

### Task 6: Metadaten aus dem Jar

**Files:**
- Create: `internal/files/jarmeta.go`
- Modify: `internal/files/jars.go` (`jarInfo` liest die Metadaten mit)
- Test: `internal/files/jarmeta_test.go`

**Interfaces:**
- Consumes: `filesapi.JarMeta`, `filesapi.JarKindPlugin`, `filesapi.JarKindMod`; `checkCentralDirectory` (zipcheck.go), `jarInfo`/`hashCache` (Aufgabe 5), `github.com/BurntSushi/toml` (`toml.Decode`); Test-Helfer `makeZip`, `zipEntry`, `rawZip`, `cdBomb`, `putBytes` (extract_test.go), `randomHex` (service.go), `listJars`, `jarNames`, `jarByName`, `countingHook` (Aufgabe 5).
- Produces:

```go
const (
	maxJarEntries = 100_000
	maxMetaBytes  = 64 << 10
	maxMetaValue  = 128
)
// readJarMeta never fails: nil for anything it cannot read.
func readJarMeta(r io.ReaderAt, size, maxEntries int64) *filesapi.JarMeta
func readEntry(zf *zip.File) ([]byte, error)
func topLevelValues(text string, keys ...string) map[string]string
func metaValue(v string) string
```
`Jar.Meta` in `/v1/jars` ist ab hier gefüllt (gecacht mit den Prüfsummen).

- [ ] **Step 1: Tests schreiben (rot)**

`internal/files/jarmeta_test.go`:

```go
package files

import (
	"archive/zip"
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// metaOf builds a jar from entries (makeZip, extract_test.go) and reads its metadata.
func metaOf(t *testing.T, entries ...zipEntry) *filesapi.JarMeta {
	t.Helper()
	b := makeZip(t, entries...)
	return readJarMeta(bytes.NewReader(b), int64(len(b)), maxJarEntries)
}

func wantMeta(t *testing.T, name string, got, want *filesapi.JarMeta) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s: %+v, want nil", name, *got)
	case want != nil && (got == nil || *got != *want):
		t.Errorf("%s: %+v, want %+v", name, got, *want)
	}
}

func TestJarMetaPlugins(t *testing.T) {
	plugin := func(name, version string) *filesapi.JarMeta {
		return &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: name, Version: version}
	}
	for name, c := range map[string]struct {
		entries []zipEntry
		want    *filesapi.JarMeta
	}{
		"plugin.yml": {[]zipEntry{{name: "plugin.yml", body: "\xef\xbb\xbf# LuckPerms\r\nname: LuckPerms\r\nversion: \"5.5.71\" # build 1\r\n" +
			"main: me.lucko.luckperms.bukkit.loader.BukkitLoaderPlugin\r\ncommands:\r\n  name: nested\r\n  version: 0\r\nname: Second\r\n"}},
			plugin("LuckPerms", "5.5.71")},
		"single quotes and comment": {[]zipEntry{{name: "plugin.yml", body: "version: 2.21.0-dev+123 # git\nname: 'Essentials X'\n"}},
			plugin("Essentials X", "2.21.0-dev+123")},
		"no version": {[]zipEntry{{name: "plugin.yml", body: "name: Tiny\n"}}, plugin("Tiny", "")},
		"paper-plugin.yml first": {[]zipEntry{
			{name: "plugin.yml", body: "name: Legacy\nversion: 1\n"},
			{name: "paper-plugin.yml", body: "name: Modern\nversion: 2\n"},
		}, plugin("Modern", "2")},
		"plugin before mod": {[]zipEntry{
			{name: "fabric.mod.json", body: `{"id":"hybrid","version":"1"}`},
			{name: "plugin.yml", body: "name: Hybrid\nversion: 1\n"},
		}, plugin("Hybrid", "1")},
		"no name":          {[]zipEntry{{name: "plugin.yml", body: "version: 1\n"}}, nil},
		"empty name":       {[]zipEntry{{name: "plugin.yml", body: "name:\nversion: 1\n"}}, nil},
		"only nested name": {[]zipEntry{{name: "plugin.yml", body: "commands:\n  name: x\n"}}, nil},
		"in a subfolder":   {[]zipEntry{{name: "x/plugin.yml", body: "name: Nested\n"}}, nil},
		"no metadata":      {[]zipEntry{{name: "a.class", body: "cafebabe"}}, nil},
	} {
		wantMeta(t, name, metaOf(t, c.entries...), c.want)
	}
}

func TestJarMetaFabric(t *testing.T) {
	mod := func(id, name, version string) *filesapi.JarMeta {
		return &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: id, Name: name, Version: version}
	}
	for name, c := range map[string]struct {
		body string
		want *filesapi.JarMeta
	}{
		"complete": {`{"schemaVersion":1,"id":"lithium","name":"Lithium","version":"mc1.21.4-0.14.3","depends":{"fabricloader":">=0.16"}}`,
			mod("lithium", "Lithium", "mc1.21.4-0.14.3")},
		"with BOM":       {"\xef\xbb\xbf" + `{"id":"fabric-api","version":"0.110.0"}`, mod("fabric-api", "", "0.110.0")},
		"no id":          {`{"name":"Nameless","version":"1"}`, nil},
		"number version": {`{"id":"x","version":1}`, nil},
		"broken JSON":    {`{"id":"x",`, nil},
	} {
		wantMeta(t, name, metaOf(t, zipEntry{name: "fabric.mod.json", body: c.body}), c.want)
	}
}

const neoToml = `modLoader="javafml"
loaderVersion="[4,)"
license="MIT"

[[mods]]
modId="examplemod"
displayName="Example Mod"
version="${file.jarVersion}"

[[mods]]
modId="second"
displayName="Second"
version="9"
`

func TestJarMetaNeoForge(t *testing.T) {
	mod := func(version string) *filesapi.JarMeta {
		return &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: "examplemod", Name: "Example Mod", Version: version}
	}
	manifest := "Manifest-Version: 1.0\r\nImplementation-Title: examplemod\r\nimplementation-version: 2.0.1\r\n\r\nName: x/\r\nImplementation-Version: 9.9\r\n"
	for name, c := range map[string]struct {
		entries []zipEntry
		want    *filesapi.JarMeta
	}{
		"jarVersion from the manifest": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}, {name: "META-INF/MANIFEST.MF", body: manifest}}, mod("2.0.1")},
		"continuation line": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml},
			{name: "META-INF/MANIFEST.MF", body: "Manifest-Version: 1.0\nImplementation-Version: 2.0.1-very-lo\n ng\n"}}, mod("2.0.1-very-long")},
		"no manifest":         {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}}, mod("")},
		"manifest without it": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}, {name: "META-INF/MANIFEST.MF", body: "Manifest-Version: 1.0\n\nName: a\nImplementation-Version: 3\n"}}, mod("")},
		"literal version":     {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: strings.Replace(neoToml, "${file.jarVersion}", "1.2.3", 1)}}, mod("1.2.3")},
		"fabric first": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}, {name: "fabric.mod.json", body: `{"id":"both","version":"1"}`}},
			&filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: "both", Version: "1"}},
		"no mods":     {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: "modLoader=\"javafml\"\n"}}, nil},
		"broken toml": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: "[[mods]\nmodId=\n"}}, nil},
		"no modId":    {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: "[[mods]]\ndisplayName=\"X\"\n"}}, nil},
	} {
		wantMeta(t, name, metaOf(t, c.entries...), c.want)
	}
}

func TestJarMetaCleansValues(t *testing.T) {
	m := metaOf(t, zipEntry{name: "plugin.yml", body: "name: Evil\x1b[31mRed\u202e\xff\x07\nversion: " + strings.Repeat("9", 300) + "\n"})
	if m == nil || m.Name != "Evil[31mRed" {
		t.Fatalf("control characters: %+v", m)
	}
	if len(m.Version) != maxMetaValue {
		t.Fatalf("version cut to %d characters, want %d", len(m.Version), maxMetaValue)
	}
	m = metaOf(t, zipEntry{name: "fabric.mod.json", body: `{"id":"x","name":"` + strings.Repeat("é", 200) + `"}`})
	if m == nil || utf8.RuneCountInString(m.Name) != maxMetaValue || !utf8.ValidString(m.Name) {
		t.Fatalf("multi-byte name: %+v", m)
	}
}

// Review focus 5: broken jars, oversized metadata, a deflate bomb in plugin.yml and a huge
// central directory are nil - never an error, never a large allocation.
func TestJarMetaRefusesBrokenAndHostileJars(t *testing.T) {
	good := makeZip(t, zipEntry{name: "plugin.yml", body: "name: Good\n"})
	for name, b := range map[string][]byte{
		"empty":       {},
		"not a zip":   []byte("PK\x03\x04 and nothing else"),
		"truncated":   good[:len(good)-10],
		"honest 64k+": makeZip(t, zipEntry{name: "plugin.yml", body: "name: Big\n" + strings.Repeat("#", maxMetaBytes)}),
		"stored lie":  rawZip(t, "plugin.yml", []byte("name: Lie\n"+strings.Repeat("a", 1<<20)), 64, zip.Store, 0),
		"cd bomb":     cdBomb(1000),
	} {
		if m := readJarMeta(bytes.NewReader(b), int64(len(b)), 100); m != nil {
			t.Errorf("%s: %+v, want nil", name, *m)
		}
	}

	bomb := rawZip(t, "plugin.yml", []byte("name: Bomb\n"+strings.Repeat("a", 32<<20)), 64, zip.Deflate, 0)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	m := readJarMeta(bytes.NewReader(bomb), int64(len(bomb)), maxJarEntries)
	runtime.ReadMemStats(&after)
	if m != nil {
		t.Fatalf("deflate bomb: %+v, want nil", *m)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 4<<20 {
		t.Fatalf("reading a bomb allocated %d MiB", grew>>20)
	}
}

func TestJarMetaEntryLimit(t *testing.T) {
	if maxJarEntries != 100_000 || maxMetaBytes != 64<<10 || maxMetaValue != 128 {
		t.Fatal("limits differ from spec 3c §5.1")
	}
	b := makeZip(t, zipEntry{name: "plugin.yml", body: "name: Many\n"}, zipEntry{name: "a.class"}, zipEntry{name: "b.class"})
	if m := readJarMeta(bytes.NewReader(b), int64(len(b)), 3); m == nil || m.Name != "Many" {
		t.Fatalf("at the limit: %+v", m)
	}
	if m := readJarMeta(bytes.NewReader(b), int64(len(b)), 2); m != nil {
		t.Fatalf("above the limit: %+v, want nil", *m)
	}
}

// manyEntries is a jar with plugin.yml and n-1 further empty entries (stored: cheap to build).
func manyEntries(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("plugin.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("name: Many\n")); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < n; i++ {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "c/" + strconv.Itoa(i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Through the service: metadata next to checksums, also for jars above the checksum limit,
// nil for hostile jars without failing the list, and cached with the checksums.
func TestJarsMetaIsBestEffort(t *testing.T) {
	s, data := newService(t)
	s.jarHashMax = 1 << 10
	var h countingHook
	s.jarHook = h.hook
	putBytes(t, data, "plugins/Good.jar", makeZip(t, zipEntry{name: "plugin.yml", body: "name: Good\nversion: 1.0\n"}))
	putBytes(t, data, "plugins/Big.jar.disabled", makeZip(t,
		zipEntry{name: "plugin.yml", body: "name: Big\nversion: 2\n"},
		zipEntry{name: "pad.bin", body: randomHex(4 << 10)})) // incompressible: above jarHashMax
	putBytes(t, data, "plugins/Bomb.jar", rawZip(t, "plugin.yml", []byte("name: Bomb\n"+strings.Repeat("a", 4<<20)), 64, zip.Deflate, 0))
	putBytes(t, data, "plugins/Broken.jar", []byte("not a zip"))
	putBytes(t, data, "plugins/Many.jar", manyEntries(t, maxJarEntries+1))

	res := listJars(t, s, filesapi.DirPlugins)
	if want := []string{"Big.jar.disabled", "Bomb.jar", "Broken.jar", "Good.jar", "Many.jar"}; strings.Join(jarNames(res), ",") != strings.Join(want, ",") {
		t.Fatalf("jars %v, want %v", jarNames(res), want)
	}
	good, big := jarByName(t, res, "Good.jar"), jarByName(t, res, "Big.jar.disabled")
	if good.Meta == nil || *good.Meta != (filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: "Good", Version: "1.0"}) || good.SHA512 == "" {
		t.Fatalf("Good.jar: %+v", good)
	}
	if big.Meta == nil || big.Meta.Name != "Big" || big.SHA512 != "" || big.Enabled {
		t.Fatalf("Big.jar.disabled: %+v, want metadata without checksums", big)
	}
	for _, n := range []string{"Bomb.jar", "Broken.jar", "Many.jar"} {
		if j := jarByName(t, res, n); j.Meta != nil {
			t.Errorf("%s: meta %+v, want nil", n, *j.Meta)
		}
	}

	// The answer holds a copy: changing it does not change the cache, and the second listing
	// computes nothing.
	good.Meta.Name = "changed"
	again := listJars(t, s, filesapi.DirPlugins)
	if m := jarByName(t, again, "Good.jar").Meta; m == nil || m.Name != "Good" {
		t.Fatalf("cached meta changed: %+v", m)
	}
	for _, n := range []string{"Good.jar", "Big.jar.disabled", "Bomb.jar", "Broken.jar", "Many.jar"} {
		if c := h.count(n); c != 1 {
			t.Errorf("%s computed %d times, want 1", n, c)
		}
	}
}
```

- [ ] **Step 2: `make check` – rot**

Run: `git add -N internal/files && make check`
Expected: FAIL mit `undefined: readJarMeta`, `undefined: maxJarEntries`, `undefined: maxMetaBytes`, `undefined: maxMetaValue`.

- [ ] **Step 3: Implementieren**

`internal/files/jarmeta.go`:

```go
package files

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	maxJarEntries = 100_000  // central directory of a jar (spec 3c §5.1)
	maxMetaBytes  = 64 << 10 // one metadata file, uncompressed
	maxMetaValue  = 128      // characters per value
	jarVersionVar = "${file.jarVersion}"

	paperPluginYML = "paper-plugin.yml"
	pluginYML      = "plugin.yml"
	fabricModJSON  = "fabric.mod.json"
	neoForgeTOML   = "META-INF/neoforge.mods.toml"
	manifestMF     = "META-INF/MANIFEST.MF"
)

var errMetaTooLarge = errors.New("metadata file too large")

// readJarMeta reads kind, id, name and version of a plugin or mod from the jar r (size bytes),
// best effort (spec 3c §5.1): the first of paper-plugin.yml, plugin.yml, fabric.mod.json and
// META-INF/neoforge.mods.toml that exists decides (plus MANIFEST.MF for ${file.jarVersion}).
// The central directory is checked before archive/zip reads it (at most maxEntries entries),
// every file is read with a hard limit of maxMetaBytes whatever its header claims. Anything it
// cannot read is nil, never an error.
func readJarMeta(r io.ReaderAt, size, maxEntries int64) *filesapi.JarMeta {
	if err := checkCentralDirectory(r, size, maxEntries); err != nil {
		return nil
	}
	zr, err := zip.NewReader(r, size)
	if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
		return nil // an insecure name elsewhere does not matter: only fixed names are read
	}
	if int64(len(zr.File)) > maxEntries {
		return nil
	}
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		switch zf.Name {
		case paperPluginYML, pluginYML, fabricModJSON, neoForgeTOML, manifestMF:
			if _, dup := files[zf.Name]; !dup {
				files[zf.Name] = zf
			}
		}
	}
	switch {
	case files[paperPluginYML] != nil:
		return pluginMeta(files[paperPluginYML])
	case files[pluginYML] != nil:
		return pluginMeta(files[pluginYML])
	case files[fabricModJSON] != nil:
		return fabricMeta(files[fabricModJSON])
	case files[neoForgeTOML] != nil:
		return neoForgeMeta(files[neoForgeTOML], files[manifestMF])
	}
	return nil
}

// readEntry reads one metadata file: its declared size and the bytes actually read are both
// bounded by maxMetaBytes (a deflate bomb with a small declared size stops at the limit).
func readEntry(zf *zip.File) ([]byte, error) {
	if zf.UncompressedSize64 > maxMetaBytes {
		return nil, errMetaTooLarge
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxMetaBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxMetaBytes {
		return nil, errMetaTooLarge
	}
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), nil
}

// pluginMeta reads name and version of a Bukkit/Paper plugin; a plugin without a name is nil.
func pluginMeta(zf *zip.File) *filesapi.JarMeta {
	b, err := readEntry(zf)
	if err != nil {
		return nil
	}
	v := topLevelValues(string(b), "name", "version")
	if v["name"] == "" {
		return nil
	}
	return &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: v["name"], Version: v["version"]}
}

// topLevelValues reads simple top-level "key: value" lines of a YAML file - no YAML parser,
// plugin.yml only needs two scalars. Indented lines (nested keys), comments and lines without
// a colon are skipped; the first line of a key wins. Values are unquoted and cleaned.
func topLevelValues(text string, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, seen := out[key]; seen || !slices.Contains(keys, key) {
			continue
		}
		out[key] = metaValue(unquote(strings.TrimSpace(val)))
	}
	return out
}

// unquote removes YAML quotes ('x' or "x", up to the closing quote) or, for a plain value, a
// trailing " # comment".
func unquote(v string) string {
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if i := strings.IndexByte(v[1:], q); i >= 0 {
			return v[1 : 1+i]
		}
		return v[1:]
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// fabricMeta reads id, name and version of a Fabric mod; a mod without an id is nil.
func fabricMeta(zf *zip.File) *filesapi.JarMeta {
	b, err := readEntry(zf)
	if err != nil {
		return nil
	}
	var m struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	meta := &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: metaValue(m.ID), Name: metaValue(m.Name), Version: metaValue(m.Version)}
	if meta.ID == "" {
		return nil
	}
	return meta
}

// neoForgeMeta reads the first [[mods]] entry of a NeoForge mod; "${file.jarVersion}" in its
// version is the Implementation-Version of MANIFEST.MF (empty without one).
func neoForgeMeta(zf, manifest *zip.File) *filesapi.JarMeta {
	b, err := readEntry(zf)
	if err != nil {
		return nil
	}
	var doc struct {
		Mods []struct {
			ModID       string `toml:"modId"`
			DisplayName string `toml:"displayName"`
			Version     string `toml:"version"`
		} `toml:"mods"`
	}
	if _, err := toml.Decode(string(b), &doc); err != nil || len(doc.Mods) == 0 {
		return nil
	}
	m := doc.Mods[0]
	version := m.Version
	if strings.Contains(version, jarVersionVar) {
		impl := ""
		if manifest != nil {
			impl = manifestValue(manifest, "Implementation-Version")
		}
		if impl == "" {
			version = ""
		} else {
			version = strings.ReplaceAll(version, jarVersionVar, impl)
		}
	}
	meta := &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: metaValue(m.ModID), Name: metaValue(m.DisplayName), Version: metaValue(version)}
	if meta.ID == "" {
		return nil
	}
	return meta
}

// manifestValue returns an attribute of the main section of MANIFEST.MF (names are
// case-insensitive, continuation lines start with one space); "" if missing or unreadable.
func manifestValue(zf *zip.File, name string) string {
	b, err := readEntry(zf)
	if err != nil {
		return ""
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	text = strings.ReplaceAll(text, "\n ", "")
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			break // end of the main section
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// metaValue makes a value from a jar safe to show: invalid UTF-8, control characters (Cc)
// and format characters (Cf, e.g. bidi overrides) removed, trimmed, at most maxMetaValue
// characters.
func metaValue(v string) string {
	v = strings.ToValidUTF8(v, "")
	v = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxMetaValue {
		v = strings.TrimSpace(string([]rune(v)[:maxMetaValue]))
	}
	return v
}
```

`internal/files/jars.go`, in `jarInfo` die Zeile

```go
	// A jar that is written to while it is read has no reliable checksum.
```

ersetzen durch

```go
	info.meta = readJarMeta(f, st.Size(), maxJarEntries)
	// A jar that is written to while it is read has no reliable checksum or metadata.
```

Damit laufen Metadaten im selben Hash-Platz wie die Prüfsummen, auch für Jars über `jarHashMax`, und werden mit ihnen gecacht. `readJarMeta` liest über `ReadAt` (der Durchlauf von `hashJar` über `io.NewSectionReader` ist davon unabhängig).

Warum zwei Grenzen beim Lesen: `archive/zip` meldet zwar `ErrFormat`, sobald mehr Bytes kommen als deklariert, aber `readEntry` verlässt sich nicht darauf – `io.LimitReader(…, maxMetaBytes+1)` begrenzt die entpackten Bytes in jedem Fall (Review Focus #5, Zip-Bombe). `checkCentralDirectory` zählt die Einträge, **bevor** `zip.NewReader` für jeden Header ein `*zip.File` anlegt.

- [ ] **Step 4: `make check` – grün**

Run: `make check`
Expected: PASS.

- [ ] **Step 5: Commit, CI, Lead informieren**

```bash
git add internal/files
git commit -m "feat(files): read plugin and mod metadata from jars

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch
```
Expected: CI grün; `TestJarMetaPlugins`, `TestJarMetaFabric`, `TestJarMetaNeoForge`, `TestJarMetaCleansValues`, `TestJarMetaRefusesBrokenAndHostileJars`, `TestJarMetaEntryLimit`, `TestJarsMetaIsBestEffort` PASS (auch unter `-race`); alle Tests aus Aufgaben 4–5 bleiben grün. Ist die Allokationsgrenze in `TestJarMetaRefusesBrokenAndHostileJars` (4 MiB) unter `-race` zu knapp, die Grenze nicht erhöhen, sondern melden. Danach dem Lead Bescheid geben (PR bleibt Draft, Mergen nur durch den Nutzer).

