# mc-panel Teilprojekt 3c – Phase A: Verträge (Aufgaben 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die gemeinsamen Grundlagen für B1/B2/B3 auf `main` bringen. Das Paket `filesapi` kennt `POST /v1/jars`, Uploads mit Prüfsumme und den Code `hash_mismatch` (Client, Hilfsfunktionen, Fakes). Die Tabelle `plugin_installs` mit ihren sqlc-Queries, die vier neuen panel-Fehlercodes und die vier Audit-Aktionen stehen. Dazu kommt der panel-Vertrag: OpenAPI mit elf Operationen, TS-Typen, das Paket `internal/plugins` mit Stub-Routen und das Gerüst `internal/plugins/upstream`, verdrahtet in `cmd/panel`, plus MSW-Mocks. Damit können Dienst, panel und Frontend parallel weiterbauen.

**Architecture:** Drei Aufgaben auf einem Branch `feat/plugins-contracts` (Teammate „contracts“, sequenziell). Aufgabe 1 ändert nur `internal/files/filesapi/**` und die Client-Implementierungen, die es außerhalb gibt: die zwei Fakes und `noFiles` in `internal/serverfiles/handler.go`. Aufgabe 2 fügt Migration 00005, `internal/store/queries/plugins.sql` (+ generiertes `plugins.sql.go`), die Fehlercodes (Go, OpenAPI-Enum, TS, i18n) und die Audit-Konstanten hinzu. Aufgabe 3 fügt die Plugin-Operationen in `api/openapi.yaml` ein, dazu `internal/plugins/handler.go` (Deps/NewHandler/Routes/Run nach V4, alle Routen antworten bis B2 `502 plugin_source_unavailable`), `internal/plugins/upstream/http.go` (V4-Deklarationen), die Verdrahtung in `cmd/panel/app.go` und `web/src/mocks/handlers/plugins.ts` nach V5.

**Tech Stack:** Go (Standardbibliothek, chi, `crypto/sha256`, `crypto/sha512`, sqlc 1.31.1, goose), OpenAPI 3.1 + `openapi-typescript`, MSW 2, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-12-plugins-00-overview.md` (V1–V5; „Ausführung“, „Global Constraints“ und „Review Focus“ gelten hier vollständig).

## Ausführung dieser Phase

- Worktree: `git fetch origin && git worktree add ../mc-panel-wt/feat-plugins-contracts -b feat/plugins-contracts origin/main`, dann alle Befehle in `../mc-panel-wt/feat-plugins-contracts` ausführen (einmalig `(cd web && npm ci)`). Ein Draft-PR für alle drei Aufgaben, ein Commit pro Aufgabe (Fix-Commits erlaubt).
- Lokal nur `make check` und `make generate` (Aufgaben 2 und 3). Meldet `make check` `gofmt needed`, dann `gofmt -w <datei>` (Spaltenausrichtung) und erneut prüfen. Neue Dateien vor `make check` mit `git add -N <pfad>` vormerken, sonst prüft gofmt sie nicht. „Run: go test …“ / „npx vitest …“ bedeutet: nicht lokal ausführen, sondern pushen, `gh pr checks <pr> --watch --interval 30` abwarten und das Ergebnis in den CI-Logs prüfen (`gh run view <id> --log-failed`).
- Eine Aufgabe ist fertig, wenn die CI ihres Commits grün ist. Danach meldet der Teammate dem Lead den PR. Der Nutzer mergt, erst dann starten B1, B2, B3.
- Phase A ändert ausschließlich: `internal/files/filesapi/**`, `internal/serverfiles/handler.go` (nur `noFiles`), `internal/serverfiles/helpers_test.go`, `cmd/panel/fakefiles_test.go`, `cmd/panel/app.go`, `cmd/panel/integration_test.go`, `internal/store/**`, `internal/httpx/errors.go`, `internal/audit/types.go`, `internal/plugins/**` (neu), `api/openapi.yaml`, `web/src/api/**`, `web/src/i18n/{de,en,i18n.test}.ts` (nur `errors.*`/`ERROR_CODES`), `web/src/mocks/**`.

## Abweichungen von der Übersicht (bewusst, vom Lead zu bestätigen)

1. **`internal/serverfiles/handler.go` wird angefasst** (V1 nennt nur die Test-Fakes). Der Platzhalter `noFiles` in dieser Datei implementiert `filesapi.Client`. Ohne `Jars`/`UploadChecked` kompiliert das Paket nicht mehr. Phase A ergänzt deshalb nur diese zwei Methoden, sonst nichts.
2. **Zusätzliche, rein additive `filesapi`-Helfer** (über V1 hinaus, B1/B2 sind informiert): `AlgoSHA256`, `AlgoSHA512`, `QuerySHA256`, `QuerySHA512`, `JarSuffix`, `DisabledSuffix`, `JarsTimeout`, `(Checksum).Validate`, `(Checksum).Matches`, `ChecksumFromQuery`, `ValidJarDir`, `IsJarName`. Damit nutzen Dienst (B1), panel (B2) und Fakes dieselben Format- und Namensregeln, statt sie dreimal zu schreiben.
3. **Error-Enum, `KNOWN_CODES` und i18n schon in Aufgabe 2:** `TestErrorCodesMatchOpenAPI` vergleicht die `Code*`-Konstanten mit dem Enum in `api/openapi.yaml`. Kämen die Codes ohne Enum, wäre die CI von Aufgabe 2 rot. Darum ergänzt Aufgabe 2 auch das Enum und `make generate`. `web/src/i18n/**` gehört laut Übersicht in Phase B zu B3, Phase A ändert dort aber nur die vier `errors.*`-Schlüssel und `ERROR_CODES` (wie in 3b). B3 startet erst nach dem Merge, es gibt also keinen Konflikt.
4. **Gerüst `internal/plugins/upstream` schon in Phase A:** V4 verlangt `Deps.Upstream *upstream.Client`, das Paket muss also existieren. Phase A legt `upstream/http.go` mit genau den V4-Deklarationen an (`Bases`, `DefaultBases`, `AllowedHosts`, `Options`, `ErrUnavailable`, `Client`, `New`), dazu `(*Client).UserAgent`, `(*Client).Bases`, `(*Client).HostAllowed` und `DefaultHostAllowed`. `New` übernimmt in Phase A nur `Version`, `Bases` und `AllowHost`; `Clock` und `HTTPClient` wertet erst B2 aus. B2 darf alles in `internal/plugins/**` ersetzen.
5. **`appDeps.Upstream *upstream.Client`** (nil = `upstream.New` mit den Standard-Basen) kommt schon in Phase A dazu, damit B2 in `cmd/panel` Test-Upstreams einsetzen kann, ohne die Verdrahtung umzubauen. `main.go` bleibt unverändert.
6. **Benannte Enum-Schemas** in OpenAPI statt Inline-Enums (gleiche Wire-Form wie V3): `PluginJarSource`, `PluginIncompatibleReason`, `PluginJobKind`, `PluginJobState`, `PluginJobStepState`, `PluginStepErrorCode`, dazu die geteilte Antwort `PluginSourceUnavailable` (502). Das Frontend bekommt so eigene TS-Aliase für Übersetzungsschlüssel (`plugins.step.error.*`).
7. **Mock-Ergänzungen zu V5** (Teammate „frontend“/Plan 04 ist informiert):
   - Neuer Server `4 'modded'` (fabric 1.21.11, gestoppt). V5 verlangt einen Fabric-Server, und die Server 1–3 sind belegt.
   - Modrinth-Projekt `chunkyborder` mit der Pflicht-Abhängigkeit `chunky`. Sonst gäbe es keinen „Wird mitinstalliert“-Fall: ViaVersion ist auf survival schon installiert, fabric-api auf modded.
   - `Disabled.jar.disabled` gehört zu BlueMap und `fabric-api-0.110.0.jar` gilt als per Hash zugeordnet (`installed_by_panel: false`). So zeigt das Inventar alle drei Herkunftsarten aus Spec §7.3.
   - Das angefragte Projekt selbst landet in `already`, wenn es ohne `version` angefragt wird oder genau diese Version schon installiert ist (Spec §6.3, wie Plan 03 Abweichung 4). Mit einer anderen `version` wird es installiert und ersetzt sein altes Jar („Version wechseln“). `install` ohne etwas zu tun → `409 conflict` (Plan 03 Abweichung 5).
   - Audit-Details wie in Plan 03: `plugin.install` `{source, project: <Projekt-ID>, version: <Versions-ID>, file}`, `plugin.update` `{file: <neuer Dateiname>, from, to}` (Versionsnamen). `update` mit einer Datei ohne verfügbares Update bzw. ohne jedes Update → `400 validation_failed` (wie Plan 03).
   - Das Rate-Limit (60/min) bildet der Mock nicht nach.
8. **Migration mit englischen SQL-Kommentaren:** Die Tabelle ist wortgleich zu Spec §6.1, nur die zwei Spaltenkommentare stehen auf Englisch wie im übrigen Code.

---

### Task 1: `filesapi` – Jars, Upload mit Prüfsumme, `hash_mismatch`, Fakes

**Files:**
- Modify: `internal/files/filesapi/types.go` (Konstanten, `UploadResponse`, Jar-Typen, `Checksum`, `Client`-Interface)
- Modify: `internal/files/filesapi/errors.go` (`CodeHashMismatch`, `ErrHashMismatch`, Tabelle `codes`)
- Create: `internal/files/filesapi/jars.go` (`Validate`, `Matches`, `ChecksumFromQuery`, `ValidJarDir`, `IsJarName`)
- Modify: `internal/files/filesapi/client.go` (`JarsTimeout`, Feld `jarsTimeout`, `Jars`, `UploadChecked`, gemeinsames `upload`)
- Modify: `internal/files/filesapi/export_test.go` (`SetJarsTimeout`)
- Modify: `internal/files/filesapi/errors_test.go` (`hash_mismatch` in beiden Tabellen)
- Create: `internal/files/filesapi/jars_test.go` (Helfer und Client-Tests)
- Modify: `internal/serverfiles/handler.go:310-337` (`noFiles`: zwei Methoden)
- Modify: `internal/serverfiles/helpers_test.go` (Imports; `fakeFiles.Jars`, `fakeFiles.UploadChecked`, Helfer `digests`)
- Modify: `cmd/panel/fakefiles_test.go` (Imports; `Jars`, `UploadChecked`, `digests`, `TestFakeFilesJarsAndChecksums`)

**Interfaces:**
- Consumes: bestehendes `filesapi` (`CleanPath`, `checkID`, `clean`, `call`, `decode`, `StatusError`, Tabelle `codes`).
- Produces (V1 plus Abweichung 2):

```go
const (
	PathJars        = "/v1/jars" // POST JarsRequest -> JarsResponse
	DirPlugins      = "plugins"
	DirMods         = "mods"
	MaxJars         = 500
	MaxJarHashBytes = 256 << 20
	AlgoSHA256      = "sha256"
	AlgoSHA512      = "sha512"
	QuerySHA256     = AlgoSHA256 // upload query parameter
	QuerySHA512     = AlgoSHA512
	JarSuffix       = ".jar"
	DisabledSuffix  = ".disabled"
	CodeHashMismatch = "hash_mismatch" // 400
	JarsTimeout     = 2 * time.Minute
)
var ErrHashMismatch = errors.New(CodeHashMismatch)
type JarsRequest struct{ ID int64; Dir string }
type JarKind string // JarKindPlugin, JarKindMod
type JarMeta struct{ Kind JarKind; ID, Name, Version string }
type Jar struct{ Name string; Enabled bool; Size, MTime int64; SHA256, SHA512 string; Meta *JarMeta }
type JarsResponse struct{ Jars []Jar; Truncated bool }
type Checksum struct{ Algo, Hex string }
type UploadResponse struct{ Size int64; SHA256, SHA512 string } // json omitempty for both digests
func (s Checksum) Validate() error                        // ErrInvalid
func (s Checksum) Matches(sha256Hex, sha512Hex string) bool
func ChecksumFromQuery(q url.Values) (sum Checksum, ok bool, err error)
func ValidJarDir(dir string) bool
func IsJarName(name string) (enabled, ok bool)
// Client gains:
//	Jars(ctx context.Context, id int64, dir string) (JarsResponse, error)
//	UploadChecked(ctx context.Context, id int64, path string, overwrite bool, size int64, sum Checksum, body io.Reader) (UploadResponse, error)
```

- [ ] **Step 1: Fehlertests erweitern (rot)**

In `internal/files/filesapi/errors_test.go` in `TestCodesRoundTrip` die Map um einen Eintrag ergänzen (nach `filesapi.CodeInvalid`):

```go
		filesapi.CodeHashMismatch:        http.StatusBadRequest,
```

In `TestStatusErrorIs` die `cases` um einen Fall ergänzen und am Ende eine Prüfung anhängen:

```go
		{&filesapi.StatusError{Status: 400, Code: "hash_mismatch"}, filesapi.ErrHashMismatch},
```

```go
	// hash_mismatch and invalid share the status 400 but are different sentinels.
	if errors.Is(&filesapi.StatusError{Status: 400, Code: "hash_mismatch"}, filesapi.ErrInvalid) {
		t.Fatal("hash_mismatch must not match ErrInvalid")
	}
	if errors.Is(&filesapi.StatusError{Status: 400, Code: "invalid"}, filesapi.ErrHashMismatch) {
		t.Fatal("invalid must not match ErrHashMismatch")
	}
```

- [ ] **Step 2: Helfer- und Client-Tests schreiben (rot)**

`internal/files/filesapi/export_test.go` – nach `SetCallTimeouts` anfügen:

```go
// SetJarsTimeout sets the timeout of a jars listing.
func SetJarsTimeout(c *UnixClient, d time.Duration) { c.jarsTimeout = d }
```

Neue Datei `internal/files/filesapi/jars_test.go` (nutzt `serve` und `writeJSON` aus `client_test.go`):

```go
package filesapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

var (
	hex64  = strings.Repeat("ab", 32)
	hex128 = strings.Repeat("cd", 64)
)

func TestChecksumValidate(t *testing.T) {
	for _, ok := range []filesapi.Checksum{
		{Algo: filesapi.AlgoSHA256, Hex: hex64},
		{Algo: filesapi.AlgoSHA512, Hex: hex128},
		{Algo: "sha512", Hex: strings.Repeat("0123456789abcdef", 8)},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []filesapi.Checksum{
		{},
		{Algo: "md5", Hex: strings.Repeat("a", 32)},
		{Algo: "SHA512", Hex: hex128},                            // algorithm names are lower case
		{Algo: filesapi.AlgoSHA256, Hex: hex128},                 // wrong length for the algorithm
		{Algo: filesapi.AlgoSHA512, Hex: hex64},                  // wrong length for the algorithm
		{Algo: filesapi.AlgoSHA256, Hex: strings.ToUpper(hex64)}, // lowercase hex only
		{Algo: filesapi.AlgoSHA256, Hex: strings.Repeat("g", 64)},
		{Algo: filesapi.AlgoSHA256, Hex: hex64[:63] + " "},
		{Algo: filesapi.AlgoSHA512, Hex: ""},
	} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestChecksumMatches(t *testing.T) {
	s256 := filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: hex64}
	s512 := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}
	if !s256.Matches(hex64, "other") || s256.Matches(strings.Repeat("00", 32), hex128) {
		t.Fatal("sha256 must compare the sha256 digest only")
	}
	if !s512.Matches("other", hex128) || s512.Matches(hex64, strings.Repeat("00", 64)) {
		t.Fatal("sha512 must compare the sha512 digest only")
	}
	if (filesapi.Checksum{}).Matches("", "") {
		t.Fatal("an empty checksum matches nothing")
	}
}

func TestChecksumFromQuery(t *testing.T) {
	sum, ok, err := filesapi.ChecksumFromQuery(url.Values{"id": {"1"}, "path": {"plugins/x.jar"}})
	if err != nil || ok || sum != (filesapi.Checksum{}) {
		t.Fatalf("no parameter: %+v %v %v", sum, ok, err)
	}
	sum, ok, err = filesapi.ChecksumFromQuery(url.Values{"sha512": {hex128}})
	if err != nil || !ok || sum != (filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}) {
		t.Fatalf("sha512: %+v %v %v", sum, ok, err)
	}
	sum, ok, err = filesapi.ChecksumFromQuery(url.Values{"sha256": {hex64}})
	if err != nil || !ok || sum.Algo != filesapi.AlgoSHA256 {
		t.Fatalf("sha256: %+v %v %v", sum, ok, err)
	}
	for name, q := range map[string]url.Values{
		"both":      {"sha256": {hex64}, "sha512": {hex128}},
		"twice":     {"sha512": {hex128, hex128}},
		"empty":     {"sha256": {""}},
		"bad hex":   {"sha512": {strings.ToUpper(hex128)}},
		"short":     {"sha256": {hex64[:10]}},
		"wrong len": {"sha256": {hex128}},
	} {
		if _, ok, err := filesapi.ChecksumFromQuery(q); !errors.Is(err, filesapi.ErrInvalid) || ok {
			t.Errorf("%s: ok=%v err=%v, want ErrInvalid", name, ok, err)
		}
	}
}

func TestValidJarDir(t *testing.T) {
	for dir, want := range map[string]bool{
		"plugins": true, "mods": true, "": false, "Plugins": false, "MODS": false,
		"plugins/": false, "plugins/x": false, "../plugins": false, "world": false, "config": false,
	} {
		if got := filesapi.ValidJarDir(dir); got != want {
			t.Errorf("ValidJarDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestIsJarName(t *testing.T) {
	type res struct{ enabled, ok bool }
	for name, want := range map[string]res{
		"LuckPerms-Bukkit-5.5.71.jar": {true, true},
		"foo.JAR":                     {true, true},
		"x.jar.disabled":              {false, true},
		"x.JAR.Disabled":              {false, true},
		"a.jar":                       {true, true},
		".jar":                        {false, false}, // nothing before the suffix
		".jar.disabled":               {false, false},
		"x.jar.disabled.jar":          {true, true},
		"x.disabled":                  {false, false},
		"x.jar.old":                   {false, false},
		"x.zip":                       {false, false},
		"jar":                         {false, false},
		"":                            {false, false},
	} {
		enabled, ok := filesapi.IsJarName(name)
		if enabled != want.enabled || ok != want.ok {
			t.Errorf("IsJarName(%q) = %v, %v; want %v, %v", name, enabled, ok, want.enabled, want.ok)
		}
	}
}

func TestClientUploadCheckedSendsTheChecksum(t *testing.T) {
	for _, sum := range []filesapi.Checksum{
		{Algo: filesapi.AlgoSHA512, Hex: hex128},
		{Algo: filesapi.AlgoSHA256, Hex: hex64},
	} {
		other := filesapi.QuerySHA256
		if sum.Algo == filesapi.AlgoSHA256 {
			other = filesapi.QuerySHA512
		}
		c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if r.Method != http.MethodPut || r.URL.Path != filesapi.PathUpload || q.Get("id") != "4" ||
				q.Get("path") != "mods/x.jar" || q.Get("overwrite") != "0" || q.Get(sum.Algo) != sum.Hex ||
				q.Has(other) || r.ContentLength != 5 {
				http.Error(w, "bad request "+r.URL.String(), 400)
				return
			}
			n, _ := io.Copy(io.Discard, r.Body)
			writeJSON(w, 201, filesapi.UploadResponse{Size: n, SHA256: hex64, SHA512: hex128})
		}))
		got, err := c.UploadChecked(context.Background(), 4, "mods/x.jar", false, 5, sum, strings.NewReader("hello"))
		if err != nil || got != (filesapi.UploadResponse{Size: 5, SHA256: hex64, SHA512: hex128}) {
			t.Fatalf("%s: UploadChecked = %+v, %v", sum.Algo, got, err)
		}
	}
}

func TestClientUploadWithoutChecksumSendsNoDigestParameter(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has(filesapi.QuerySHA256) || q.Has(filesapi.QuerySHA512) {
			http.Error(w, "unexpected digest parameter", 400)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		// The service leaves the digests out without a checksum (omitempty).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"size":3}`))
	}))
	got, err := c.Upload(context.Background(), 1, "plugins/x.jar", true, 3, strings.NewReader("abc"))
	if err != nil || got != (filesapi.UploadResponse{Size: 3}) {
		t.Fatalf("Upload = %+v, %v", got, err)
	}
	raw, _ := json.Marshal(filesapi.UploadResponse{Size: 3})
	if string(raw) != `{"size":3}` {
		t.Fatalf("UploadResponse without digests = %s, want {\"size\":3}", raw)
	}
}

func TestClientUploadCheckedRejectsBadInputWithoutRequest(t *testing.T) {
	called := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	ctx := context.Background()
	for _, sum := range []filesapi.Checksum{
		{},
		{Algo: "md5", Hex: strings.Repeat("a", 32)},
		{Algo: filesapi.AlgoSHA512, Hex: hex64},
		{Algo: filesapi.AlgoSHA256, Hex: strings.ToUpper(hex64)},
	} {
		if _, err := c.UploadChecked(ctx, 1, "plugins/x.jar", false, 1, sum, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", sum, err)
		}
	}
	good := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}
	if _, err := c.UploadChecked(ctx, 1, "../x.jar", false, 1, good, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("bad path: err = %v, want ErrInvalid", err)
	}
	if _, err := c.UploadChecked(ctx, 0, "plugins/x.jar", false, 1, good, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("id 0: err = %v, want ErrInvalid", err)
	}
	if _, err := c.UploadChecked(ctx, 1, "plugins/x.jar", false, -1, good, strings.NewReader("x")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("negative size: err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("an invalid upload reached the service")
	}
}

func TestClientUploadCheckedHashMismatch(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeJSON(w, http.StatusBadRequest, filesapi.ErrorResponse{Error: "checksum does not match", Code: filesapi.CodeHashMismatch})
	}))
	sum := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: hex128}
	_, err := c.UploadChecked(context.Background(), 1, "plugins/x.jar", false, 3, sum, strings.NewReader("abc"))
	if !errors.Is(err, filesapi.ErrHashMismatch) || errors.Is(err, filesapi.ErrInvalid) || errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrHashMismatch only", err)
	}
}

func TestClientJars(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req filesapi.JarsRequest
		if r.Method != http.MethodPost || r.URL.Path != filesapi.PathJars || r.Header.Get("Content-Type") != "application/json" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.ID != 7 || req.Dir != filesapi.DirPlugins {
			http.Error(w, "bad request", 400)
			return
		}
		writeJSON(w, 200, filesapi.JarsResponse{Truncated: true, Jars: []filesapi.Jar{
			{Name: "A.jar", Enabled: true, Size: 3, MTime: 5, SHA256: hex64, SHA512: hex128,
				Meta: &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: "A", Version: "1.0"}},
			{Name: "big.jar.disabled", Enabled: false, Size: filesapi.MaxJarHashBytes + 1, MTime: 6},
		}})
	}))
	got, err := c.Jars(context.Background(), 7, filesapi.DirPlugins)
	if err != nil || !got.Truncated || len(got.Jars) != 2 {
		t.Fatalf("Jars = %+v, %v", got, err)
	}
	a, big := got.Jars[0], got.Jars[1]
	if a.Meta == nil || a.Meta.Kind != filesapi.JarKindPlugin || a.Meta.Name != "A" || a.SHA512 != hex128 || !a.Enabled {
		t.Fatalf("first jar = %+v (meta %+v)", a, a.Meta)
	}
	if big.Meta != nil || big.SHA256 != "" || big.SHA512 != "" || big.Enabled {
		t.Fatalf("second jar = %+v", big)
	}
	raw, _ := json.Marshal(filesapi.Jar{Name: "x.jar"})
	if !strings.Contains(string(raw), `"meta":null`) || !strings.Contains(string(raw), `"sha512":""`) {
		t.Fatalf("Jar wire form = %s", raw)
	}
}

func TestClientJarsNeverReturnsNil(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jars":null,"truncated":false}`))
	}))
	got, err := c.Jars(context.Background(), 1, filesapi.DirMods)
	if err != nil || got.Jars == nil || len(got.Jars) != 0 {
		t.Fatalf("Jars = %#v, %v; want an empty, non-nil list", got, err)
	}
}

func TestClientJarsRejectsBadInputWithoutRequest(t *testing.T) {
	called := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	for _, dir := range []string{"", "world", "Plugins", "plugins/", "plugins/x", "../plugins", "/plugins"} {
		if _, err := c.Jars(context.Background(), 1, dir); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("dir %q: err = %v, want ErrInvalid", dir, err)
		}
	}
	if _, err := c.Jars(context.Background(), 0, filesapi.DirPlugins); !errors.Is(err, filesapi.ErrInvalid) {
		t.Errorf("id 0: err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("an invalid jars request reached the service")
	}
}

// Hashing up to 500 jars on a cache miss takes longer than the 10 s of the small calls; the
// listing has its own timeout (2 min, V1).
func TestClientJarsHasItsOwnTimeout(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		writeJSON(w, 200, filesapi.JarsResponse{Jars: []filesapi.Jar{}})
	}))
	filesapi.SetCallTimeouts(c, 50*time.Millisecond, 50*time.Millisecond)
	filesapi.SetJarsTimeout(c, 5*time.Second)
	if _, err := c.Jars(context.Background(), 1, filesapi.DirPlugins); err != nil {
		t.Fatalf("slow listing within the jars timeout: %v", err)
	}
	filesapi.SetJarsTimeout(c, 50*time.Millisecond)
	if _, err := c.Jars(context.Background(), 1, filesapi.DirPlugins); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("listing past the jars timeout: err = %v, want ErrUnavailable", err)
	}
	if filesapi.JarsTimeout != 2*time.Minute {
		t.Fatalf("JarsTimeout = %s, want 2m", filesapi.JarsTimeout)
	}
}
```

- [ ] **Step 3: `make check` (rot)**

Run: `git add -N internal/files/filesapi/jars_test.go && make check`
Expected: Kompilierfehler, z. B. `undefined: filesapi.CodeHashMismatch`, `undefined: filesapi.Checksum`, `c.jarsTimeout undefined`.

- [ ] **Step 4: Typen und Fehler implementieren**

`internal/files/filesapi/types.go`:

Im `const`-Block die Zeile `PathUpload` ersetzen und nach `PathJobs` ergänzen:

```go
	PathUpload   = "/v1/upload"   // PUT, query id, path, overwrite=0|1 and at most one of sha256/sha512; body = file; Content-Length required
```

```go
	PathJars     = "/v1/jars" // POST JarsRequest -> JarsResponse (sub-project 3c)
```

Nach dem `const`-Block (vor `type EntryType`) einen neuen Block einfügen:

```go
// Plugins and mods (sub-project 3c §5).
const (
	// DirPlugins and DirMods are the only directories /v1/jars lists.
	DirPlugins = "plugins"
	DirMods    = "mods"
	// MaxJars bounds one listing (JarsResponse.Truncated beyond it).
	MaxJars = 500
	// MaxJarHashBytes: larger jars are listed without checksums.
	MaxJarHashBytes = 256 << 20

	// AlgoSHA256 and AlgoSHA512 are the checksum algorithms of an upload.
	AlgoSHA256 = "sha256"
	AlgoSHA512 = "sha512"
	// QuerySHA256 and QuerySHA512 are the upload query parameters carrying the expected digest
	// (lowercase hex); at most one of them per upload.
	QuerySHA256 = AlgoSHA256
	QuerySHA512 = AlgoSHA512

	// JarSuffix and DisabledSuffix make up jar names: "x.jar" is enabled, "x.jar.disabled" not.
	JarSuffix      = ".jar"
	DisabledSuffix = ".disabled"
)
```

`UploadResponse` ersetzen:

```go
// UploadResponse is the answer of an upload; the digests are only filled when the upload
// carried a Checksum (UploadChecked).
type UploadResponse struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
}
```

Nach `JobsResponse` einfügen:

```go
// JarsRequest is the body of /v1/jars.
type JarsRequest struct {
	ID  int64  `json:"id"`
	Dir string `json:"dir"` // DirPlugins | DirMods
}

// JarKind is what a jar's metadata file says it is.
type JarKind string

const (
	JarKindPlugin JarKind = "plugin"
	JarKindMod    JarKind = "mod"
)

// JarMeta is read best-effort from the jar (plugin.yml, fabric.mod.json, neoforge.mods.toml).
type JarMeta struct {
	Kind    JarKind `json:"kind"`
	ID      string  `json:"id"` // fabric/neoforge mod id; "" for plugins
	Name    string  `json:"name"`
	Version string  `json:"version"`
}

// Jar is one regular file of the listed directory named *.jar or *.jar.disabled.
type Jar struct {
	Name    string   `json:"name"`    // base name incl. ".jar" / ".jar.disabled"
	Enabled bool     `json:"enabled"` // false for ".jar.disabled"
	Size    int64    `json:"size"`
	MTime   int64    `json:"mtime"`  // Unix ms
	SHA256  string   `json:"sha256"` // lowercase hex; "" above MaxJarHashBytes
	SHA512  string   `json:"sha512"` // lowercase hex; "" above MaxJarHashBytes
	Meta    *JarMeta `json:"meta"`   // nil = not readable
}

// JarsResponse lists the jars of one directory.
type JarsResponse struct {
	Jars      []Jar `json:"jars"` // never nil; sorted by name (byte order)
	Truncated bool  `json:"truncated"`
}

// Checksum is the expected digest of an upload (UploadChecked).
type Checksum struct {
	Algo string // AlgoSHA256 | AlgoSHA512
	Hex  string // lowercase hex, 64 resp. 128 characters
}
```

Im `Client`-Interface nach `Upload` und nach `Job` ergänzen:

```go
	// UploadChecked is Upload with an expected digest: the service compares it before the file
	// replaces anything and answers ErrHashMismatch otherwise (an invalid sum is ErrInvalid
	// without a call).
	UploadChecked(ctx context.Context, id int64, path string, overwrite bool, size int64, sum Checksum, body io.Reader) (UploadResponse, error)
```

```go
	// Jars lists the jars of DirPlugins or DirMods with checksums and metadata.
	Jars(ctx context.Context, id int64, dir string) (JarsResponse, error)
```

`internal/files/filesapi/errors.go`: im `const`-Block nach `CodeInvalid`:

```go
	CodeHashMismatch        = "hash_mismatch"        // 400
```

im `var`-Block nach `ErrInvalid`:

```go
	ErrHashMismatch        = errors.New(CodeHashMismatch)
```

und in `codes` als letzten Eintrag:

```go
	{CodeHashMismatch, codeInfo{ErrHashMismatch, http.StatusBadRequest}},
```

(gofmt richtet die Spalten aus.)

- [ ] **Step 5: Helfer implementieren**

Neue Datei `internal/files/filesapi/jars.go`:

```go
package filesapi

import (
	"fmt"
	"net/url"
	"strings"
)

// checksumHexLen is the digest length in hex characters per algorithm.
var checksumHexLen = map[string]int{AlgoSHA256: 64, AlgoSHA512: 128}

// Validate reports ErrInvalid unless s names AlgoSHA256 with 64 or AlgoSHA512 with 128
// lowercase hex characters.
func (s Checksum) Validate() error {
	n, ok := checksumHexLen[s.Algo]
	if !ok {
		return fmt.Errorf("%w: checksum algorithm must be %s or %s", ErrInvalid, AlgoSHA256, AlgoSHA512)
	}
	if len(s.Hex) != n || !isLowerHex(s.Hex) {
		return fmt.Errorf("%w: %s must be %d lowercase hex characters", ErrInvalid, s.Algo, n)
	}
	return nil
}

// Matches reports whether the digests computed while writing (lowercase hex) satisfy s; only
// the digest of s.Algo is compared. An invalid s matches nothing.
func (s Checksum) Matches(sha256Hex, sha512Hex string) bool {
	if s.Validate() != nil {
		return false
	}
	switch s.Algo {
	case AlgoSHA256:
		return s.Hex == sha256Hex
	case AlgoSHA512:
		return s.Hex == sha512Hex
	}
	return false
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ChecksumFromQuery reads the optional digest of an upload request (QuerySHA256 or
// QuerySHA512). ok is false when neither is present. Both at once, a repeated parameter or a
// malformed value are ErrInvalid.
func ChecksumFromQuery(q url.Values) (sum Checksum, ok bool, err error) {
	var found []Checksum
	for _, algo := range []string{QuerySHA256, QuerySHA512} {
		vs, present := q[algo]
		if !present {
			continue
		}
		if len(vs) != 1 {
			return Checksum{}, false, fmt.Errorf("%w: %s given more than once", ErrInvalid, algo)
		}
		found = append(found, Checksum{Algo: algo, Hex: vs[0]})
	}
	switch len(found) {
	case 0:
		return Checksum{}, false, nil
	case 1:
		if err := found[0].Validate(); err != nil {
			return Checksum{}, false, err
		}
		return found[0], true, nil
	}
	return Checksum{}, false, fmt.Errorf("%w: at most one of %s and %s", ErrInvalid, QuerySHA256, QuerySHA512)
}

// ValidJarDir reports whether dir is one of the directories /v1/jars lists.
func ValidJarDir(dir string) bool { return dir == DirPlugins || dir == DirMods }

// IsJarName reports whether a base name is a jar: "<x>.jar" (enabled) or "<x>.jar.disabled"
// (disabled), compared case-insensitively, with at least one character before ".jar". It does
// not check for "/" or hidden names; callers use CleanPath and IsHidden for that.
func IsJarName(name string) (enabled, ok bool) {
	switch {
	case hasSuffixFold(name, JarSuffix+DisabledSuffix):
		return false, true
	case hasSuffixFold(name, JarSuffix):
		return true, true
	}
	return false, false
}

// hasSuffixFold: name ends with suffix (ASCII case-insensitive) and has something before it.
func hasSuffixFold(name, suffix string) bool {
	return len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix)
}
```

- [ ] **Step 6: Client implementieren**

`internal/files/filesapi/client.go`:

Im `const`-Block nach `ExtractTimeout`:

```go
	// JarsTimeout bounds a jars listing: on a cache miss the service hashes up to MaxJars jars
	// (each up to MaxJarHashBytes, two at a time).
	JarsTimeout = 2 * time.Minute
```

`UnixClient` um ein Feld ergänzen (nach `extractTimeout`):

```go
	jarsTimeout           time.Duration
```

In `NewClient` im Rückgabewert nach `extractTimeout: ExtractTimeout,`:

```go
		jarsTimeout:           JarsTimeout,
```

`Upload` (Zeilen 183–217) ersetzen durch:

```go
// Upload streams body (exactly size bytes) to path. There is no timeout besides ctx: the
// service answers only after the whole body arrived (spec §4, up to 4 GiB).
func (c *UnixClient) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (UploadResponse, error) {
	return c.upload(ctx, id, p, overwrite, size, nil, body)
}

// UploadChecked is Upload with an expected digest (sub-project 3c §5.2): the service computes
// sha256 and sha512 while writing and refuses a mismatch with hash_mismatch before the file is
// renamed into place. An invalid sum fails with ErrInvalid before anything is sent.
func (c *UnixClient) UploadChecked(ctx context.Context, id int64, p string, overwrite bool, size int64, sum Checksum, body io.Reader) (UploadResponse, error) {
	if err := sum.Validate(); err != nil {
		return UploadResponse{}, err
	}
	return c.upload(ctx, id, p, overwrite, size, &sum, body)
}

func (c *UnixClient) upload(ctx context.Context, id int64, p string, overwrite bool, size int64, sum *Checksum, body io.Reader) (UploadResponse, error) {
	if err := clean(id, &p); err != nil {
		return UploadResponse{}, err
	}
	if size < 0 {
		return UploadResponse{}, fmt.Errorf("%w: upload size must be known", ErrInvalid)
	}
	ow := "0"
	if overwrite {
		ow = "1"
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "path": {p}, "overwrite": {ow}}
	if sum != nil {
		q.Set(sum.Algo, sum.Hex) // QuerySHA256 / QuerySHA512 equal the algorithm names
	}
	// An empty file goes out as http.NoBody: net/http would otherwise probe the reader and could
	// fall back to chunked encoding, which the service refuses (Content-Length is required).
	var rb io.Reader = http.NoBody
	if size > 0 {
		rb = io.NopCloser(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+PathUpload+"?"+q.Encode(), rb)
	if err != nil {
		return UploadResponse{}, fmt.Errorf("files: build request: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return UploadResponse{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out UploadResponse
	return out, decode(resp, &out)
}
```

Nach `Job` (vor dem `Upload`-Block) einfügen:

```go
// Jars lists DirPlugins or DirMods of a server with checksums and metadata (sub-project 3c
// §5.1). Any other dir fails with ErrInvalid before anything is sent.
func (c *UnixClient) Jars(ctx context.Context, id int64, dir string) (JarsResponse, error) {
	if err := checkID(id); err != nil {
		return JarsResponse{}, err
	}
	if !ValidJarDir(dir) {
		return JarsResponse{}, fmt.Errorf("%w: jar directory must be %q or %q", ErrInvalid, DirPlugins, DirMods)
	}
	var out JarsResponse
	err := c.call(ctx, c.jarsTimeout, http.MethodPost, PathJars, JarsRequest{ID: id, Dir: dir}, &out)
	if out.Jars == nil {
		out.Jars = []Jar{}
	}
	return out, err
}
```

`maxResponseBytes` (32 MiB) reicht für 500 Jars locker (pro Jar < 1 KiB).

- [ ] **Step 7: `noFiles` und Fakes ergänzen**

`internal/serverfiles/handler.go` – nach `func (noFiles) Job(...)` (Zeile 337):

```go
func (noFiles) UploadChecked(context.Context, int64, string, bool, int64, filesapi.Checksum, io.Reader) (filesapi.UploadResponse, error) {
	return filesapi.UploadResponse{}, errNoFiles
}
func (noFiles) Jars(context.Context, int64, string) (filesapi.JarsResponse, error) {
	return filesapi.JarsResponse{}, errNoFiles
}
```

`internal/serverfiles/helpers_test.go`:

Imports ergänzen: `"crypto/sha256"` und `"crypto/sha512"` nach `"context"`, `"encoding/hex"` nach `"database/sql"`. Nach `fakeFiles.Upload` einfügen:

```go
// UploadChecked stores the upload like Upload, but only if the digest really matches.
func (f *fakeFiles) UploadChecked(ctx context.Context, id int64, path string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	if err := sum.Validate(); err != nil {
		return filesapi.UploadResponse{}, err
	}
	if err := f.begin(ctx, fmt.Sprintf("upload-checked %d %s overwrite=%t size=%d %s", id, path, overwrite, size, sum.Algo)); err != nil {
		return filesapi.UploadResponse{}, err
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: got %d bytes, announced %d", filesapi.ErrInvalid, len(b), size)
	}
	h256, h512 := digests(b)
	if !sum.Matches(h256, h512) {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s differs", filesapi.ErrHashMismatch, sum.Algo)
	}
	f.mu.Lock()
	f.uploads[path] = b
	f.mu.Unlock()
	return filesapi.UploadResponse{Size: size, SHA256: h256, SHA512: h512}, nil
}

// Jars lists nothing: the file browser never asks for jars.
func (f *fakeFiles) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if err := f.begin(ctx, fmt.Sprintf("jars %d %s", id, dir)); err != nil {
		return filesapi.JarsResponse{}, err
	}
	return filesapi.JarsResponse{Jars: []filesapi.Jar{}}, nil
}

// digests returns the lowercase hex sha256 and sha512 of b.
func digests(b []byte) (sha256Hex, sha512Hex string) {
	s256, s512 := sha256.Sum256(b), sha512.Sum512(b)
	return hex.EncodeToString(s256[:]), hex.EncodeToString(s512[:])
}
```

`cmd/panel/fakefiles_test.go`:

Imports ersetzen durch:

```go
import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)
```

Am Dateiende anfügen:

```go
// UploadChecked stores the upload only if the digest really matches; without overwrite an
// existing file is ErrExists (like the service).
func (f *fakeFiles) UploadChecked(_ context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	if err := sum.Validate(); err != nil {
		return filesapi.UploadResponse{}, err
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %w", filesapi.ErrUnavailable, err)
	}
	if int64(len(b)) != size {
		return filesapi.UploadResponse{}, filesapi.ErrInvalid
	}
	h256, h512 := digests(b)
	if !sum.Matches(h256, h512) {
		return filesapi.UploadResponse{}, filesapi.ErrHashMismatch
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[key(id, p)]; ok && !overwrite {
		return filesapi.UploadResponse{}, filesapi.ErrExists
	}
	f.files[key(id, p)] = b
	return filesapi.UploadResponse{Size: size, SHA256: h256, SHA512: h512}, nil
}

// Jars lists the files directly in <dir> whose names are jars, with real digests and without
// metadata, sorted by name.
func (f *fakeFiles) Jars(_ context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if !filesapi.ValidJarDir(dir) {
		return filesapi.JarsResponse{}, filesapi.ErrInvalid
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []filesapi.Jar{}
	for k, b := range f.files {
		name, ok := strings.CutPrefix(k, key(id, dir+"/"))
		if !ok || strings.Contains(name, "/") {
			continue
		}
		enabled, isJar := filesapi.IsJarName(name)
		if !isJar {
			continue
		}
		h256, h512 := digests(b)
		out = append(out, filesapi.Jar{Name: name, Enabled: enabled, Size: int64(len(b)), MTime: f.mtime, SHA256: h256, SHA512: h512})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return filesapi.JarsResponse{Jars: out}, nil
}

// digests returns the lowercase hex sha256 and sha512 of b.
func digests(b []byte) (sha256Hex, sha512Hex string) {
	s256, s512 := sha256.Sum256(b), sha512.Sum512(b)
	return hex.EncodeToString(s256[:]), hex.EncodeToString(s512[:])
}

// The fake behaves like the service for what phase B2 relies on: real digests, a refused
// mismatch leaves nothing behind, only top-level jars are listed.
func TestFakeFilesJarsAndChecksums(t *testing.T) {
	f := newFakeFiles()
	ctx := t.Context()
	data := []byte("jar bytes")
	h256, h512 := digests(data)
	wrong := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: strings.Repeat("0", 128)}
	if _, err := f.UploadChecked(ctx, 1, "plugins/a.jar", false, int64(len(data)), wrong, bytes.NewReader(data)); !errors.Is(err, filesapi.ErrHashMismatch) {
		t.Fatalf("wrong digest: %v", err)
	}
	if _, ok := f.files[key(1, "plugins/a.jar")]; ok {
		t.Fatal("a mismatching upload was stored")
	}
	got, err := f.UploadChecked(ctx, 1, "plugins/a.jar", false, int64(len(data)), filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: h256}, bytes.NewReader(data))
	if err != nil || got.SHA512 != h512 || got.SHA256 != h256 || got.Size != int64(len(data)) {
		t.Fatalf("UploadChecked = %+v, %v", got, err)
	}
	if _, err := f.UploadChecked(ctx, 1, "plugins/a.jar", false, int64(len(data)), filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: h512}, bytes.NewReader(data)); !errors.Is(err, filesapi.ErrExists) {
		t.Fatalf("existing without overwrite: %v", err)
	}
	f.files[key(1, "plugins/B.jar.disabled")] = []byte("b")
	f.files[key(1, "plugins/readme.txt")] = []byte("x")
	f.files[key(1, "plugins/LuckPerms/config.yml")] = []byte("x")
	f.files[key(2, "plugins/other.jar")] = []byte("x")
	list, err := f.Jars(ctx, 1, filesapi.DirPlugins)
	if err != nil || len(list.Jars) != 2 || list.Jars[0].Name != "B.jar.disabled" || list.Jars[0].Enabled ||
		list.Jars[1].Name != "a.jar" || !list.Jars[1].Enabled || list.Jars[1].SHA512 != h512 {
		t.Fatalf("Jars = %+v, %v", list, err)
	}
	if _, err := f.Jars(ctx, 1, "world"); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("bad dir: %v", err)
	}
}
```

- [ ] **Step 8: `make check`, Commit, Push, Draft-PR, CI**

```bash
make check
git add internal/files/filesapi internal/serverfiles/handler.go internal/serverfiles/helpers_test.go cmd/panel/fakefiles_test.go
git commit -m "feat(filesapi): jar listing, uploads with checksum and hash_mismatch

The files service contract of sub-project 3c: POST /v1/jars with checksums and
metadata, an optional sha256/sha512 on uploads that the service verifies before
the rename, the hash_mismatch code, shared helpers for checksum and jar names,
and both client fakes.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/plugins-contracts
gh pr create --draft --title "feat: TP3c contracts (filesapi jars, plugin_installs, plugin API stubs)" --body "Phase A of docs/superpowers/plans/2026-10-12-plugins-00-overview.md (Tasks 1–3, plan 2026-10-12-plugins-01-contracts.md).

🤖 Generated with [Claude Code](https://claude.com/claude-code)"
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün. In den Go-Logs `TestCodesRoundTrip`, `TestStatusErrorIs`, `TestChecksum*`, `TestValidJarDir`, `TestIsJarName`, `TestClientUploadChecked*`, `TestClientUploadWithoutChecksumSendsNoDigestParameter`, `TestClientJars*` und `TestFakeFilesJarsAndChecksums` PASS (auch mit `-race`). Die bestehenden Tests in `internal/serverfiles` und `cmd/panel` sind unverändert grün.

---

### Task 2: Daten, Fehlercodes, Audit-Aktionen

**Files:**
- Create: `internal/store/migrations/00005_plugins.sql`
- Create: `internal/store/queries/plugins.sql`; generiert: `internal/store/plugins.sql.go`, `internal/store/models.go` (neuer Typ `PluginInstall`)
- Create: `internal/store/plugins_test.go`
- Modify: `internal/store/store_test.go:18-19` (Tabellenliste um `plugin_installs`)
- Modify: `internal/httpx/errors.go` (4 Codes vor `CodeInternal`)
- Modify: `internal/audit/types.go` (4 Aktionen nach `ActionFileDownload`)
- Modify: `api/openapi.yaml` (nur die Zeile `error: { type: string, enum: [...] }` im Schema `Error`); generiert: `web/src/api/schema.d.ts`
- Modify: `web/src/api/errors.ts` (`KNOWN_CODES`), `web/src/i18n/de.ts`, `web/src/i18n/en.ts` (`errors.*`), `web/src/i18n/i18n.test.ts` (`ERROR_CODES`)

**Interfaces:**
- Consumes: Tabelle `servers` (Migration 00001), `storetest.Open`, `storetest.CreateServer`.
- Produces (V2, Namen per sqlc 1.31.1 geprüft):

```go
type PluginInstall struct {
	ServerID    int64
	Filename    string
	Sha512      string
	Source      string
	ProjectID   string
	ProjectSlug string
	Title       string
	VersionID   string
	VersionName string
	Channel     string
	InstalledAt int64
}
func (q *Queries) PluginsListInstalls(ctx context.Context, serverID int64) ([]PluginInstall, error) // never nil
func (q *Queries) PluginsUpsertInstall(ctx context.Context, arg PluginsUpsertInstallParams) error   // same 11 fields
func (q *Queries) PluginsDeleteInstall(ctx context.Context, arg PluginsDeleteInstallParams) error   // {ServerID, Filename}

// httpx
CodePluginsUnsupported      = "plugins_unsupported"       // 409
CodePluginSourceUnavailable = "plugin_source_unavailable" // 502
CodePluginNotDownloadable   = "plugin_not_downloadable"   // 409
CodePluginIncompatible      = "plugin_incompatible"       // 409
// audit
ActionPluginInstall = "plugin.install"
ActionPluginUpdate  = "plugin.update"
ActionPluginRemove  = "plugin.remove"
ActionPluginToggle  = "plugin.toggle"
```

  TS: `ErrorCode` umfasst die vier Codes, `errors.<code>` existiert in `de`/`en`.

- [ ] **Step 1: Store-Tests schreiben (rot)**

`internal/store/store_test.go` – in `TestMigrateIsIdempotentAndCreatesTables` die Tabellenliste ersetzen:

```go
	for _, table := range []string{"users", "webauthn_credentials", "recovery_codes", "sessions", "servers",
		"permissions", "audit_log", "login_attempts", "jails", "ip_rules", "settings", "notifiers", "plugin_installs"} {
```

Neue Datei `internal/store/plugins_test.go`:

```go
package store_test

import (
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// pluginInstall is a valid row for serverID/filename (LuckPerms from Modrinth).
func pluginInstall(serverID int64, filename string) store.PluginsUpsertInstallParams {
	return store.PluginsUpsertInstallParams{
		ServerID: serverID, Filename: filename, Sha512: strings.Repeat("a", 128), Source: "modrinth",
		ProjectID: "Vebnzrzj", ProjectSlug: "luckperms", Title: "LuckPerms", VersionID: "lp5571",
		VersionName: "5.5.71", Channel: "release", InstalledAt: 10,
	}
}

func upsertInstall(t *testing.T, q *store.Queries, p store.PluginsUpsertInstallParams) {
	t.Helper()
	if err := q.PluginsUpsertInstall(t.Context(), p); err != nil {
		t.Fatalf("upsert %s: %v", p.Filename, err)
	}
}

func listInstalls(t *testing.T, q *store.Queries, serverID int64) []store.PluginInstall {
	t.Helper()
	list, err := q.PluginsListInstalls(t.Context(), serverID)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestPluginInstallsRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	a := storetest.CreateServer(t, db, "survival")
	b := storetest.CreateServer(t, db, "creative")

	if list := listInstalls(t, q, a); list == nil || len(list) != 0 {
		t.Fatalf("empty server: %#v, want an empty, non-nil list", list)
	}
	upsertInstall(t, q, pluginInstall(a, "b.jar"))
	upsertInstall(t, q, pluginInstall(a, "A.jar"))
	upsertInstall(t, q, pluginInstall(b, "c.jar"))
	list := listInstalls(t, q, a)
	if len(list) != 2 || list[0].Filename != "A.jar" || list[1].Filename != "b.jar" {
		t.Fatalf("list = %+v, want A.jar, b.jar (byte order)", list)
	}

	// An upsert on the same (server, filename) replaces every other column.
	upd := pluginInstall(a, "b.jar")
	upd.Sha512, upd.Source, upd.ProjectID, upd.ProjectSlug = strings.Repeat("b", 128), "hangar", "2", "ViaVersion"
	upd.Title, upd.VersionID, upd.VersionName, upd.Channel, upd.InstalledAt = "ViaVersion", "5.5.1", "5.5.1", "beta", 20
	upsertInstall(t, q, upd)
	list = listInstalls(t, q, a)
	want := store.PluginInstall{
		ServerID: a, Filename: "b.jar", Sha512: upd.Sha512, Source: "hangar", ProjectID: "2", ProjectSlug: "ViaVersion",
		Title: "ViaVersion", VersionID: "5.5.1", VersionName: "5.5.1", Channel: "beta", InstalledAt: 20,
	}
	if len(list) != 2 || list[1] != want {
		t.Fatalf("after upsert: %+v, want second row %+v", list, want)
	}

	if err := q.PluginsDeleteInstall(t.Context(), store.PluginsDeleteInstallParams{ServerID: a, Filename: "b.jar"}); err != nil {
		t.Fatal(err)
	}
	if err := q.PluginsDeleteInstall(t.Context(), store.PluginsDeleteInstallParams{ServerID: a, Filename: "b.jar"}); err != nil {
		t.Fatalf("deleting a missing row: %v", err)
	}
	if list := listInstalls(t, q, a); len(list) != 1 || list[0].Filename != "A.jar" {
		t.Fatalf("after delete: %+v", list)
	}
	if list := listInstalls(t, q, b); len(list) != 1 || list[0].Filename != "c.jar" {
		t.Fatalf("other server touched: %+v", list)
	}
}

func TestPluginInstallsChecks(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	sid := storetest.CreateServer(t, db, "survival")
	for name, mutate := range map[string]func(*store.PluginsUpsertInstallParams){
		"source curseforge": func(p *store.PluginsUpsertInstallParams) { p.Source = "curseforge" },
		"source manual":     func(p *store.PluginsUpsertInstallParams) { p.Source = "manual" },
		"channel snapshot":  func(p *store.PluginsUpsertInstallParams) { p.Channel = "snapshot" },
		"unknown server":    func(p *store.PluginsUpsertInstallParams) { p.ServerID = sid + 1000 },
	} {
		p := pluginInstall(sid, "x.jar")
		mutate(&p)
		if err := q.PluginsUpsertInstall(ctx, p); err == nil {
			t.Errorf("%s: row accepted", name)
		}
	}
	upsertInstall(t, q, pluginInstall(sid, "x.jar"))
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", sid); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM plugin_installs").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left after deleting the server: n=%d err=%v", n, err)
	}
}
```

- [ ] **Step 2: `make check` (rot)**

Run: `git add -N internal/store/plugins_test.go && make check`
Expected: `undefined: store.PluginsUpsertInstallParams`, `q.PluginsListInstalls undefined` usw.

- [ ] **Step 3: Migration und Queries**

`internal/store/migrations/00005_plugins.sql`:

```sql
-- +goose Up
-- Sub-project 3c: origin of the jars the panel installed. The disk is the source of truth; a row
-- only counts while a jar of this name (with or without ".disabled") with exactly this sha512
-- lies in plugins/ resp. mods/ (spec §6.1), otherwise the inventory deletes it.
CREATE TABLE plugin_installs (
    server_id    INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,   -- file name without ".disabled"
    sha512       TEXT    NOT NULL,
    source       TEXT    NOT NULL CHECK (source IN ('modrinth', 'hangar')),
    project_id   TEXT    NOT NULL,   -- Modrinth ID resp. Hangar ID (number as text)
    project_slug TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    version_id   TEXT    NOT NULL,   -- Modrinth version ID resp. Hangar version name
    version_name TEXT    NOT NULL,
    channel      TEXT    NOT NULL CHECK (channel IN ('release', 'beta', 'alpha')),
    installed_at INTEGER NOT NULL,
    PRIMARY KEY (server_id, filename)
);

-- +goose Down
DROP TABLE plugin_installs;
```

`internal/store/queries/plugins.sql`:

```sql
-- Sub-project 3c: origin of panel-installed plugins and mods. All query names start with "Plugins".

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

- [ ] **Step 4: Fehlercodes und Audit-Aktionen**

`internal/httpx/errors.go` – im `const`-Block vor `CodeInternal`:

```go
	CodePluginsUnsupported      = "plugins_unsupported"
	CodePluginSourceUnavailable = "plugin_source_unavailable"
	CodePluginNotDownloadable   = "plugin_not_downloadable"
	CodePluginIncompatible      = "plugin_incompatible"
```

Danach `gofmt -w internal/httpx/errors.go`: Die längeren Namen verschieben die Ausrichtung des ganzen `const`-Blocks, und `make check` meldet sonst `gofmt needed`.

`internal/audit/types.go` – nach `ActionFileDownload`:

```go
	ActionPluginInstall        = "plugin.install"
	ActionPluginUpdate         = "plugin.update"
	ActionPluginRemove         = "plugin.remove"
	ActionPluginToggle         = "plugin.toggle"
```

(gofmt richtet die `=` aus.)

`api/openapi.yaml` – im Schema `Error` die `error`-Zeile ersetzen (eine Zeile, `TestErrorCodesMatchOpenAPI` liest sie per Regex):

```yaml
        error: { type: string, enum: [bad_request, validation_failed, unauthorized, invalid_credentials, invalid_code, forbidden, sudo_required, setup_2fa_required, csrf_invalid, not_found, conflict, payload_too_large, rate_limited, jailed, ip_blocked, agent_unavailable, docker_unavailable, base_domain_missing, catalog_unavailable, instance_not_running, rcon_unavailable, files_unavailable, file_exists, file_modified, not_text, not_regular, instance_running, insufficient_storage, plugins_unsupported, plugin_source_unavailable, plugin_not_downloadable, plugin_incompatible, internal] }
```

- [ ] **Step 5: Frontend-Codes und Texte**

`web/src/api/errors.ts` – in `KNOWN_CODES` nach `'insufficient_storage',`:

```ts
  'plugins_unsupported',
  'plugin_source_unavailable',
  'plugin_not_downloadable',
  'plugin_incompatible',
```

`web/src/i18n/i18n.test.ts` – dieselben vier Einträge in `ERROR_CODES` nach `'insufficient_storage',`.

`web/src/i18n/de.ts` – unter `errors` nach `insufficient_storage`:

```ts
    plugins_unsupported: 'Dieser Servertyp unterstützt keine Plugins oder Mods.',
    plugin_source_unavailable: 'Modrinth oder Hangar ist gerade nicht erreichbar.',
    plugin_not_downloadable: 'Diese Version ist nur extern erhältlich und kann nicht direkt installiert werden.',
    plugin_incompatible: 'Für diesen Server gibt es keine kompatible Version (auch nicht für eine Pflicht-Abhängigkeit).',
```

`web/src/i18n/en.ts` – unter `errors` nach `insufficient_storage`:

```ts
    plugins_unsupported: 'This server type does not support plugins or mods.',
    plugin_source_unavailable: 'Modrinth or Hangar is not reachable right now.',
    plugin_not_downloadable: 'This version is only available externally and cannot be installed directly.',
    plugin_incompatible: 'There is no compatible version for this server (or for a required dependency).',
```

- [ ] **Step 6: Generieren und `make check`**

```bash
make generate
git status --short internal/store web/src/api
make check
```

Expected: `make generate` erzeugt `internal/store/plugins.sql.go`, ergänzt `PluginInstall` in `internal/store/models.go` und das Enum in `web/src/api/schema.d.ts`; der sqlc-Header bleibt `sqlc v1.31.1` (sonst den Header mit committen, siehe `docs/dev/README.md`). `make check` grün.

- [ ] **Step 7: Commit, Push, CI**

```bash
git add internal/store internal/httpx/errors.go internal/audit/types.go api/openapi.yaml web/src/api web/src/i18n
git commit -m "feat(store): plugin_installs, plugin error codes and audit actions

Migration 00005 with the origin of panel-installed jars and its Plugins* queries,
the four plugin error codes in Go, the OpenAPI enum, the frontend and both
locales, and the plugin.* audit actions.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün. Insbesondere laufen `TestPluginInstallsRoundTrip`, `TestPluginInstallsChecks`, `TestMigrateIsIdempotentAndCreatesTables` und `TestErrorCodesMatchOpenAPI` durch, der Schritt „Check sqlc output is up to date“ ist grün und `i18n.test.ts` besteht (die vier Codes in `de` und `en`).

---

### Task 3: panel-Vertrag – OpenAPI, Stub-Paket `internal/plugins`, Verdrahtung, Mocks

**Files:**
- Modify: `api/openapi.yaml`:
  - Tag `plugins` nach `files`
  - elf Operationen nach `getFileJob` (vor `/audit:`)
  - Parameter `PluginSourceQuery`, `PluginProjectQuery` nach `FilePathQueryRequired`
  - Responses `PluginsUnsupported`, `PluginSourceUnavailable` nach `InsufficientStorage`
  - Schemas nach `FileJobResponse`
  - generiert: `web/src/api/schema.d.ts`
- Create: `internal/plugins/handler.go`, `internal/plugins/routes_test.go`
- Create: `internal/plugins/upstream/http.go`, `internal/plugins/upstream/http_test.go`
- Modify: `cmd/panel/app.go` (Imports, `appDeps.Upstream`, gemeinsamer Katalog, `pluginsH`, Routen, `run`)
- Modify: `cmd/panel/integration_test.go:187-188` (Routen 76 → 87)
- Modify: `web/src/api/types.ts` (32 Aliase)
- Modify: `web/src/mocks/db.ts` (Typ-Importe, Server 4, `MockPluginInventory`, `MockPluginChange`, `MockPluginJob`, `pluginIconUrl`, `seedPlugins`, Felder `plugins`/`pluginJobs`)
- Create: `web/src/mocks/handlers/plugins.ts`
- Modify: `web/src/mocks/handlers.ts` (`pluginsOperations`), `web/src/mocks/handlers.test.ts` (76 → 87, sechs Verhaltenstests)

**Interfaces:**
- Consumes: Aufgabe 1 (`filesapi.Client` mit `Jars`/`UploadChecked`), Aufgabe 2 (`httpx.CodePlugin*`, `store.New`), `catalog.New`/`catalog.UserAgent`, `instancespec.Type`, `agentapi.InstancesClient`, `rbac.PermServerPlugins`.
- Produces:
  - `plugins.Catalog`, `plugins.Deps`, `plugins.NewHandler(d Deps) *Handler`, `(*Handler).Routes(api *rbac.Router)`, `(*Handler).Run(ctx context.Context)` exakt nach V4. Alle elf Routen gehen in Phase A an `h.notImplemented` (`502 plugin_source_unavailable`, Text `plugins not implemented yet`). `Run` blockiert bis `ctx.Done()`.
  - `upstream.Bases`, `upstream.DefaultBases`, `upstream.AllowedHosts`, `upstream.Options`, `upstream.ErrUnavailable`, `upstream.Client`, `upstream.New(o Options) *Client` nach V4. Dazu (Abweichung 4) `(*Client).UserAgent() string`, `(*Client).Bases() Bases`, `(*Client).HostAllowed(u *url.URL) bool`, `upstream.DefaultHostAllowed(u *url.URL) bool`.
  - `cmd/panel`: `appDeps.Upstream *upstream.Client`, `cat := catalog.New(...)` für `servers` und `plugins`, `pluginsH.Run` in der WaitGroup.
  - TS (`web/src/api/types.ts`), die 26 Schemas aus V3: `PluginSource`, `PluginKind`, `PluginChannel`, `PluginProjectRef`, `PluginVersionRef`, `PluginJarMeta`, `InstalledJar`, `PluginInventory`, `PluginSearchHit`, `PluginSearchResult`, `PluginVersion`, `PluginVersionList`, `PluginPlanEntry`, `PluginAlready`, `PluginExternal`, `PluginIncompatible`, `PluginResolution`, `InstallPluginRequest`, `PluginUpdate`, `PluginUpdates`, `UpdatePluginsRequest`, `TogglePluginRequest`, `PluginFileRequest`, `PluginJobStep`, `PluginJob`, `PluginJobResponse`.
  - TS, dazu die sechs benannten Enums aus Abweichung 6: `PluginJarSource`, `PluginIncompatibleReason`, `PluginJobKind`, `PluginJobState`, `PluginJobStepState`, `PluginStepErrorCode`.
  - MSW: `pluginsOperations` (11), `PLUGIN_JOB_MS = 4000`, `db.plugins: Record<number, MockPluginInventory>`, `db.pluginJobs: Record<number, MockPluginJob>`, `pluginIconUrl(serverId, cdnUrl)`; Server 4 `modded` (fabric).

**Mock-Daten (V5 + Abweichung 7) auf einen Blick**

| Server | Art | Inventar (`file` → Herkunft) |
|---|---|---|
| 1 `survival` (paper 1.21.11, läuft) | plugin, `plugins` | `Disabled.jar.disabled` → Modrinth BlueMap 5.7, per Hash, deaktiviert · `LuckPerms-Bukkit-5.5.71.jar` → Modrinth, panel, Update auf 5.5.72 · `OldTool.jar` → manuell, `meta {plugin, '', OldTool, 1.0}` · `ViaVersion-5.5.1.jar` → Hangar, panel |
| 2 `creative` (vanilla) | – | `409 plugins_unsupported` |
| 3 `lobby` (ohne Instanz) | – | `404 not_found` |
| 4 `modded` (fabric 1.21.11, gestoppt) | mod, `mods` | `fabric-api-0.110.0.jar` → Modrinth Fabric API, per Hash |

Katalog:
- Modrinth-Plugins: `luckperms`, `chunky`, `chunkyborder` (→ `chunky`), `bluemap`.
- Hangar-Plugins: `ViaVersion`, `ViaBackwards` (→ `ViaVersion`), `Essentials` (nur extern), `Chunky`.
- Modrinth-Mods: `sodium-extra` (client-only, erscheint nie), `lithium`, `fabric-api`, `servercore` (→ `fabric-api`).
- Jedes Projekt hat genau eine `beta` als neueste Version, die Standardwahl ist die neueste `release`.

- [ ] **Step 1: OpenAPI erweitern, `make generate`**

Unter `tags:` nach `files` einfügen:

```yaml
  - { name: plugins, description: 'Plugins and mods of a server: inventory, search in Modrinth and Hangar, install, update, toggle, remove' }
```

Nach der Operation `/servers/{id}/files/jobs` (vor `/audit:`) einfügen:

```yaml
  /servers/{id}/plugins:
    get:
      operationId: listPlugins
      tags: [plugins]
      summary: Installed plugins or mods (jars on disk, matched to their source)
      description: 'Every jar in plugins/ resp. mods/ is matched by the install record of the panel (same name and sha512), else by a Modrinth hash lookup, else it is manual. If Modrinth is unreachable the affected jars stay manual and source_errors names it.'
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PluginInventory' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/PluginsUnsupported' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/search:
    get:
      operationId: searchPlugins
      tags: [plugins]
      summary: Search compatible projects of one source (20 per page; the most downloaded without q)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/PluginSourceQuery'
        - { name: q, in: query, required: false, schema: { type: string, maxLength: 100, default: '' } }
        - { name: offset, in: query, required: false, schema: { type: integer, minimum: 0, maximum: 1000, default: 0 } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PluginSearchResult' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/PluginsUnsupported' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '502': { $ref: '#/components/responses/PluginSourceUnavailable' }
  /servers/{id}/plugins/versions:
    get:
      operationId: listPluginVersions
      tags: [plugins]
      summary: Compatible versions of a project, newest first (at most 25)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/PluginSourceQuery'
        - $ref: '#/components/parameters/PluginProjectQuery'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PluginVersionList' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/PluginsUnsupported' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '502': { $ref: '#/components/responses/PluginSourceUnavailable' }
  /servers/{id}/plugins/resolve:
    get:
      operationId: resolvePlugin
      tags: [plugins]
      summary: Preview of an installation with its required dependencies
      description: 'Dependencies without a compatible downloadable version are listed in incompatible (200); the plan is then not installable. Only a problem of the requested project itself answers 409.'
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/PluginSourceQuery'
        - $ref: '#/components/parameters/PluginProjectQuery'
        - { name: version, in: query, required: false, description: 'Modrinth version ID or Hangar version name; omitted = newest compatible release, else newest compatible version', schema: { type: string, minLength: 1, maxLength: 128, pattern: '^[A-Za-z0-9._+-]+$' } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PluginResolution' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'plugins_unsupported, plugin_incompatible (the requested project has no compatible version) or plugin_not_downloadable (the requested version is only available externally)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '502': { $ref: '#/components/responses/PluginSourceUnavailable' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/install:
    post:
      operationId: installPlugin
      tags: [plugins]
      summary: Start a job that installs a project with its required dependencies (audited per jar)
      description: The plan is computed again (the preview is not trusted). Dependencies are installed first; a failed step ends the job, steps already done stay installed.
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/InstallPluginRequest' } } }
      responses:
        '202': { description: Job started, content: { application/json: { schema: { $ref: '#/components/schemas/PluginJob' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'plugins_unsupported, conflict (a plugin job of this server is running, or the requested project is already installed in this version), plugin_incompatible (the plan has incompatible entries), plugin_not_downloadable (the requested version is only available externally) or file_exists (a target file name belongs to another jar)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '502': { $ref: '#/components/responses/PluginSourceUnavailable' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/updates:
    get:
      operationId: getPluginUpdates
      tags: [plugins]
      summary: Available updates of the installed jars (cached for 1 h per server)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - { name: refresh, in: query, required: false, description: 'true (or 1) bypasses the cache; rate limited', schema: { type: boolean, default: false } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PluginUpdates' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/PluginsUnsupported' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '502': { $ref: '#/components/responses/PluginSourceUnavailable' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/update:
    post:
      operationId: updatePlugins
      tags: [plugins]
      summary: Start a job that updates jars (audited per jar)
      description: A failed step does not stop the job; it then ends failed. Disabled jars stay disabled, new required dependencies are installed as well. A file without an available update, or no update at all, is 400 validation_failed.
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/UpdatePluginsRequest' } } }
      responses:
        '202': { description: Job started, content: { application/json: { schema: { $ref: '#/components/schemas/PluginJob' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'plugins_unsupported or conflict (a plugin job of this server is running)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '502': { $ref: '#/components/responses/PluginSourceUnavailable' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/toggle:
    post:
      operationId: togglePlugin
      tags: [plugins]
      summary: Enable or disable a jar by renaming it to or from .jar.disabled (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/TogglePluginRequest' } } }
      responses:
        '204': { description: Done (also when the jar already had the requested state) }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'plugins_unsupported, file_exists (a jar with the target name exists) or conflict (a plugin job of this server is running)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/delete:
    post:
      operationId: deletePlugin
      tags: [plugins]
      summary: Remove a jar and its install record (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/PluginFileRequest' } } }
      responses:
        '204': { description: Removed }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'plugins_unsupported or conflict (a plugin job of this server is running)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/plugins/jobs:
    get:
      operationId: getPluginJob
      tags: [plugins]
      summary: The running or last plugin job of the server (kept in memory until the next job or a panel restart)
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/PluginJobResponse' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/PluginsUnsupported' }
  /servers/{id}/plugins/icon:
    get:
      operationId: getPluginIcon
      tags: [plugins]
      summary: Project icon through the panel (CSP img-src self); only from cdn.modrinth.com and hangarcdn.papermc.io
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - { name: url, in: query, required: true, description: 'https://cdn.modrinth.com/... or https://hangarcdn.papermc.io/...', schema: { type: string, minLength: 1, maxLength: 512 } }
      responses:
        '200':
          description: 'The image with its detected Content-Type (Cache-Control: private, max-age=86400; X-Content-Type-Options: nosniff)'
          content:
            image/png: { schema: { type: string, format: binary } }
            image/jpeg: { schema: { type: string, format: binary } }
            image/webp: { schema: { type: string, format: binary } }
            image/gif: { schema: { type: string, format: binary } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '502': { description: 'plugin_source_unavailable (fetch failed, larger than 256 KiB or not a PNG, JPEG, WebP or GIF image)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

Unter `components.parameters` nach `FilePathQueryRequired`:

```yaml
    PluginSourceQuery: { name: source, in: query, required: true, description: 'hangar only for plugins (paper, purpur)', schema: { $ref: '#/components/schemas/PluginSource' } }
    PluginProjectQuery: { name: project, in: query, required: true, description: 'Modrinth project ID or slug, Hangar slug or ID', schema: { type: string, minLength: 1, maxLength: 128, pattern: '^[A-Za-z0-9._+-]+$' } }
```

Unter `components.responses` nach `InsufficientStorage`:

```yaml
    PluginsUnsupported: { description: 'plugins_unsupported (the server type has no plugins or mods: vanilla and modpacks)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    PluginSourceUnavailable: { description: 'plugin_source_unavailable (Modrinth or Hangar unreachable, rate limited or answering unusably) or catalog_unavailable (the newest game version for LATEST cannot be resolved)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

Nach dem Schema `FileJobResponse` einfügen:

```yaml
    PluginSource: { type: string, enum: [modrinth, hangar] }
    PluginKind: { type: string, enum: [plugin, mod] }
    PluginChannel: { type: string, enum: [release, beta, alpha] }
    PluginJarSource: { type: string, enum: [modrinth, hangar, manual] }
    PluginProjectRef:
      type: object
      required: [id, slug, title, page_url, icon_url]
      properties:
        id: { type: string, description: 'Modrinth project ID resp. Hangar ID (number as text)' }
        slug: { type: string }
        title: { type: string }
        page_url: { type: string, description: 'https://modrinth.com/<plugin|mod>/<slug> resp. https://hangar.papermc.io/<owner>/<slug>' }
        icon_url: { type: [string, 'null'], description: 'Icon proxy /api/servers/{id}/plugins/icon?url=<encoded>; null without icon' }
    PluginVersionRef:
      type: object
      required: [id, name, channel]
      properties:
        id: { type: string, description: 'Modrinth version ID resp. Hangar version name' }
        name: { type: string }
        channel: { $ref: '#/components/schemas/PluginChannel' }
    PluginJarMeta:
      type: object
      required: [kind, id, name, version]
      properties:
        kind: { $ref: '#/components/schemas/PluginKind' }
        id: { type: string, description: 'Mod id (fabric, neoforge); empty for plugins' }
        name: { type: string }
        version: { type: string }
    InstalledJar:
      type: object
      required: [file, enabled, size, mtime, source, project, version, meta, installed_by_panel]
      properties:
        file: { type: string, description: 'Base name incl. .jar resp. .jar.disabled' }
        enabled: { type: boolean }
        size: { type: integer, format: int64 }
        mtime: { type: integer, format: int64, description: Unix ms }
        source: { $ref: '#/components/schemas/PluginJarSource' }
        project: { anyOf: [{ $ref: '#/components/schemas/PluginProjectRef' }, { type: 'null' }] }
        version: { anyOf: [{ $ref: '#/components/schemas/PluginVersionRef' }, { type: 'null' }] }
        meta: { anyOf: [{ $ref: '#/components/schemas/PluginJarMeta' }, { type: 'null' }], description: 'Read from the jar; null if not readable' }
        installed_by_panel: { type: boolean, description: 'The install record of the panel matches (name and sha512)' }
    PluginInventory:
      type: object
      required: [kind, dir, game_version, sources, jars, truncated, restart_needed, source_errors]
      properties:
        kind: { $ref: '#/components/schemas/PluginKind' }
        dir: { type: string, enum: [plugins, mods] }
        game_version: { type: [string, 'null'], description: 'Game version of the instance (LATEST resolved); null if the version catalog is unavailable' }
        sources: { type: array, items: { $ref: '#/components/schemas/PluginSource' } }
        jars: { type: array, items: { $ref: '#/components/schemas/InstalledJar' }, description: Sorted by file name }
        truncated: { type: boolean, description: 'More than 500 jars; only the first are listed' }
        restart_needed: { type: boolean, description: 'The server runs and the panel changed its plugins since it started' }
        source_errors: { type: array, items: { $ref: '#/components/schemas/PluginSource' }, description: 'Sources that failed while matching; their jars may show as manual' }
    PluginSearchHit:
      type: object
      required: [source, project_id, slug, title, description, author, downloads, icon_url, page_url, installed]
      properties:
        source: { $ref: '#/components/schemas/PluginSource' }
        project_id: { type: string }
        slug: { type: string }
        title: { type: string }
        description: { type: string, description: 'Short description as plain text (never rendered as Markdown or HTML)' }
        author: { type: string }
        downloads: { type: integer, format: int64 }
        icon_url: { type: [string, 'null'] }
        page_url: { type: string }
        installed: { type: boolean }
    PluginSearchResult:
      type: object
      required: [hits, total, offset]
      properties:
        hits: { type: array, items: { $ref: '#/components/schemas/PluginSearchHit' } }
        total: { type: integer, format: int64 }
        offset: { type: integer }
    PluginVersion:
      type: object
      required: [id, name, channel, published_at, downloadable, external_url]
      properties:
        id: { type: string }
        name: { type: string }
        channel: { $ref: '#/components/schemas/PluginChannel' }
        published_at: { type: integer, format: int64, description: Unix ms }
        downloadable: { type: boolean, description: 'false = only available externally (external_url)' }
        external_url: { type: [string, 'null'] }
    PluginVersionList:
      type: object
      required: [versions]
      properties:
        versions: { type: array, items: { $ref: '#/components/schemas/PluginVersion' } }
    PluginPlanEntry:
      type: object
      required: [source, project_id, slug, title, version_id, version_name, channel, filename, size, required_by]
      properties:
        source: { $ref: '#/components/schemas/PluginSource' }
        project_id: { type: string }
        slug: { type: string }
        title: { type: string }
        version_id: { type: string }
        version_name: { type: string }
        channel: { $ref: '#/components/schemas/PluginChannel' }
        filename: { type: string }
        size: { type: integer, format: int64 }
        required_by: { type: [string, 'null'], description: 'Title of the plan entry that requires it; null for the requested project' }
    PluginAlready:
      type: object
      required: [source, project_id, title, file]
      properties:
        source: { $ref: '#/components/schemas/PluginSource' }
        project_id: { type: string }
        title: { type: string }
        file: { type: string }
    PluginExternal:
      type: object
      required: [name, url]
      properties:
        name: { type: string }
        url: { type: string }
    PluginIncompatibleReason: { type: string, enum: [no_compatible_version, client_only, not_downloadable, too_deep, too_many] }
    PluginIncompatible:
      type: object
      required: [title, reason]
      properties:
        title: { type: string }
        reason: { $ref: '#/components/schemas/PluginIncompatibleReason' }
    PluginResolution:
      type: object
      required: [install, already, external, incompatible]
      properties:
        install: { type: array, items: { $ref: '#/components/schemas/PluginPlanEntry' }, description: Dependencies first }
        already: { type: array, items: { $ref: '#/components/schemas/PluginAlready' } }
        external: { type: array, items: { $ref: '#/components/schemas/PluginExternal' } }
        incompatible: { type: array, items: { $ref: '#/components/schemas/PluginIncompatible' }, description: 'Not empty = the plan cannot be installed' }
    InstallPluginRequest:
      type: object
      additionalProperties: false
      required: [source, project]
      properties:
        source: { $ref: '#/components/schemas/PluginSource' }
        project: { type: string, minLength: 1, maxLength: 128, pattern: '^[A-Za-z0-9._+-]+$' }
        version: { type: [string, 'null'], minLength: 1, maxLength: 128, pattern: '^[A-Za-z0-9._+-]+$', description: 'Omitted or null = newest compatible release' }
    PluginUpdate:
      type: object
      required: [file, from, to]
      properties:
        file: { type: string }
        from: { $ref: '#/components/schemas/PluginVersionRef' }
        to: { $ref: '#/components/schemas/PluginVersionRef' }
    PluginUpdates:
      type: object
      required: [updates, checked_at]
      properties:
        updates: { type: array, items: { $ref: '#/components/schemas/PluginUpdate' } }
        checked_at: { type: integer, format: int64, description: Unix ms }
    UpdatePluginsRequest:
      type: object
      additionalProperties: false
      required: [files]
      properties:
        files: { type: array, maxItems: 500, items: { type: string, minLength: 5, maxLength: 255 }, description: 'Jar file names; empty = every jar with an update' }
    TogglePluginRequest:
      type: object
      additionalProperties: false
      required: [file, enabled]
      properties:
        file: { type: string, minLength: 5, maxLength: 255, description: 'Base name ending in .jar or .jar.disabled' }
        enabled: { type: boolean }
    PluginFileRequest:
      type: object
      additionalProperties: false
      required: [file]
      properties:
        file: { type: string, minLength: 5, maxLength: 255, description: 'Base name ending in .jar or .jar.disabled' }
    PluginJobKind: { type: string, enum: [install, update] }
    PluginJobState: { type: string, enum: [running, done, failed] }
    PluginJobStepState: { type: string, enum: [pending, downloading, done, failed, skipped] }
    PluginStepErrorCode: { type: string, enum: [download_failed, hash_mismatch, file_exists, insufficient_storage, files_unavailable, source_unavailable] }
    PluginJobStep:
      type: object
      required: [title, file, state, error_code, error]
      properties:
        title: { type: string }
        file: { type: string, description: 'Target file name (for an update the new name)' }
        state: { $ref: '#/components/schemas/PluginJobStepState' }
        error_code: { anyOf: [{ $ref: '#/components/schemas/PluginStepErrorCode' }, { type: 'null' }] }
        error: { type: string, description: 'Reason without host paths; empty unless failed' }
    PluginJob:
      type: object
      required: [id, kind, state, steps, started_at, finished_at]
      properties:
        id: { type: string }
        kind: { $ref: '#/components/schemas/PluginJobKind' }
        state: { $ref: '#/components/schemas/PluginJobState' }
        steps: { type: array, items: { $ref: '#/components/schemas/PluginJobStep' } }
        started_at: { type: integer, format: int64, description: Unix ms }
        finished_at: { type: integer, format: int64, description: 0 while running }
    PluginJobResponse:
      type: object
      required: [job]
      properties:
        job: { anyOf: [{ $ref: '#/components/schemas/PluginJob' }, { type: 'null' }] }
```

Dann: `make generate` (regeneriert `web/src/api/schema.d.ts`; sqlc ändert nichts mehr). Prüfen: `grep -c "operationId:" api/openapi.yaml` ergibt `87`.

- [ ] **Step 2: Go-Tests schreiben (rot)**

Neue Datei `internal/plugins/routes_test.go`:

```go
package plugins_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var pluginsRule = rbac.Server(rbac.PermServerPlugins, "id")

// wantRoutes are exactly the plugins operations of api/openapi.yaml (V3), all with server.plugins.
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

type env struct {
	db  *sql.DB
	mux *chi.Mux
	rt  *rbac.Router
	h   *plugins.Handler
}

// newEnv mounts the handler under /api on a real rbac.Router with the DB-backed checker. No
// Catalog, Upstream or Files: the defaults must build without touching the network.
func newEnv(t *testing.T) env {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	h := plugins.NewHandler(plugins.Deps{
		DB: db, Clock: clk, Audit: &audit.MemoryRecorder{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) { h.Routes(api) })
	mux.Handle("/*", http.NotFoundHandler())
	return env{db: db, mux: mux, rt: rt, h: h}
}

// do sends a request as p (nil = anonymous).
func (e env) do(method, path string, p *principal.Principal) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{}`)
	}
	req := httptest.NewRequest(method, path, body)
	if p != nil {
		req = req.WithContext(principal.With(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	got := e.rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range wantRoutes {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(e.mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Phase A: every route answers 502 plugin_source_unavailable until phase B2 implements it.
func TestStubsAnswerNotImplemented(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "root", "admin")
	admin := principal.Principal{UserID: uid, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		path := strings.Replace(pattern, "{id}", "1", 1)
		rec := e.do(method, path, &admin)
		var body httpx.ErrorBody
		if rec.Code != http.StatusBadGateway || json.Unmarshal(rec.Body.Bytes(), &body) != nil ||
			body.Error != httpx.CodePluginSourceUnavailable || body.Message != "plugins not implemented yet" {
			t.Errorf("%s: %d %s", key, rec.Code, rec.Body)
		}
		if rec := e.do(method, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d, want 401", key, rec.Code)
		}
	}
}

// The rule asks for server.plugins: server.view alone is 403.
func TestRoutesNeedServerPlugins(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	sid := storetest.CreateServer(t, e.db, "survival")
	alice := principal.Principal{UserID: uid, Username: "alice", Role: principal.RoleUser, Stage: principal.StageFull, SessionID: 2}
	grant := func(perm rbac.Permission) {
		if _, err := e.db.ExecContext(t.Context(), `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, uid, sid, string(perm)); err != nil {
			t.Fatal(err)
		}
	}
	path := fmt.Sprintf("/api/servers/%d/plugins", sid)
	grant(rbac.PermServerView)
	if rec := e.do(http.MethodGet, path, &alice); rec.Code != http.StatusForbidden {
		t.Fatalf("server.view only: %d, want 403", rec.Code)
	}
	grant(rbac.PermServerPlugins)
	if rec := e.do(http.MethodGet, path, &alice); rec.Code != http.StatusBadGateway {
		t.Fatalf("server.plugins: %d, want the stub's 502", rec.Code)
	}
}

func TestRunReturnsWhenCancelled(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	select {
	case <-done:
		t.Fatal("Run returned before ctx was cancelled")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
```

Neue Datei `internal/plugins/upstream/http_test.go`:

```go
package upstream_test

import (
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

func TestNewDefaults(t *testing.T) {
	if upstream.DefaultBases != (upstream.Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}) {
		t.Fatalf("DefaultBases = %+v", upstream.DefaultBases)
	}
	if !slices.Equal(upstream.AllowedHosts, []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}) {
		t.Fatalf("AllowedHosts = %v", upstream.AllowedHosts)
	}
	c := upstream.New(upstream.Options{Clock: clock.NewFake(time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))})
	if c.Bases() != upstream.DefaultBases {
		t.Fatalf("Bases() = %+v, want the defaults", c.Bases())
	}
	if c.UserAgent() != "mc-panel/dev (https://github.com/Bernotese/mc-panel)" {
		t.Fatalf("UserAgent() = %q", c.UserAgent())
	}
	test := upstream.Bases{Modrinth: "https://127.0.0.1:1", Hangar: "https://127.0.0.1:2"}
	c = upstream.New(upstream.Options{Version: "1.2.3", Bases: test})
	if c.Bases() != test || c.UserAgent() != "mc-panel/1.2.3 (https://github.com/Bernotese/mc-panel)" {
		t.Fatalf("Bases() = %+v, UserAgent() = %q", c.Bases(), c.UserAgent())
	}
}

func TestDefaultHostAllowed(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://cdn.modrinth.com/data/Vebnzrzj/versions/abc/LuckPerms-Bukkit-5.5.72.jar":                      true,
		"https://hangarcdn.papermc.io/plugins/ViaVersion/ViaVersion/versions/5.5.1/PAPER/ViaVersion-5.5.1.jar": true,
		"https://CDN.Modrinth.com/data/x/icon.png":                                                             true,
		"https://cdn.modrinth.com:443/x.jar":                                                                   true,
		"http://cdn.modrinth.com/x.jar":                                                                        false,
		"https://cdn.modrinth.com:8443/x.jar":                                                                  false,
		"https://user:pw@cdn.modrinth.com/x.jar":                                                               false,
		"https://cdn.modrinth.com.evil.example/x.jar":                                                          false,
		"https://evil.example/cdn.modrinth.com/x.jar":                                                          false,
		"https://modrinth.com/plugin/luckperms":                                                                false,
		"https://api.modrinth.com/v2/project/luckperms":                                                        false, // API host, not a download host
		"https://hangar.papermc.io/api/v1/projects/ViaVersion":                                                 false,
		"ftp://cdn.modrinth.com/x.jar":                                                                         false,
		"//cdn.modrinth.com/x.jar":                                                                             false,
		"cdn.modrinth.com/x.jar":                                                                               false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := upstream.DefaultHostAllowed(u); got != want {
			t.Errorf("DefaultHostAllowed(%s) = %v, want %v", raw, got, want)
		}
	}
	if upstream.DefaultHostAllowed(nil) {
		t.Fatal("nil URL allowed")
	}
}

// Tests inject AllowHost for their httptest TLS servers; it replaces the default check.
func TestHostAllowedOverride(t *testing.T) {
	c := upstream.New(upstream.Options{AllowHost: func(u *url.URL) bool { return u.Host == "127.0.0.1:8443" }})
	local, _ := url.Parse("https://127.0.0.1:8443/x.jar")
	cdn, _ := url.Parse("https://cdn.modrinth.com/x.jar")
	if !c.HostAllowed(local) || c.HostAllowed(cdn) || c.HostAllowed(nil) {
		t.Fatal("AllowHost does not replace the default check")
	}
	if d := upstream.New(upstream.Options{}); !d.HostAllowed(cdn) || d.HostAllowed(local) {
		t.Fatal("default client must use DefaultHostAllowed")
	}
}
```

`cmd/panel/integration_test.go:187-188` – Zähler auf 87 setzen:

```go
	if n := len(e.app.Routes); n != 87 {
		t.Fatalf("registered %d API routes, OpenAPI defines 87 operations (incl. GET /health)", n)
	}
```

- [ ] **Step 3: `make check` (rot)**

Run: `git add -N internal/plugins && make check`
Expected: Kompilierfehler, z. B. `package github.com/Bernotese/mc-panel/internal/plugins is not in std`, `undefined: upstream.New`.

- [ ] **Step 4: Gerüst `internal/plugins/upstream`**

Neue Datei `internal/plugins/upstream/http.go`:

```go
// Package upstream talks to the plugin sources Modrinth and Hangar (sub-project 3c §4): fixed
// API base URLs, a contact User-Agent, and jars and icons only from AllowedHosts over https.
//
// Phase A of the plan only declares the contract (V4); phase B2 adds the clients, the download,
// the icon fetch and the caches.
package upstream

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

// Bases are the API base URLs (scheme and host, no trailing slash).
type Bases struct{ Modrinth, Hangar string }

// DefaultBases are the production APIs, checked against the live services 2026-10-12.
var DefaultBases = Bases{Modrinth: "https://api.modrinth.com", Hangar: "https://hangar.papermc.io"}

// AllowedHosts are the only hosts jars and icons are fetched from (https only).
var AllowedHosts = []string{"cdn.modrinth.com", "hangarcdn.papermc.io"}

// Options configure a Client.
type Options struct {
	Clock      clock.Clock
	Version    string       // panel version for the User-Agent; "" = "dev"
	Bases      Bases        // zero = DefaultBases
	HTTPClient *http.Client // nil = default; tests inject httptest TLS clients
	// AllowHost overrides the download/icon host check in tests (nil = DefaultHostAllowed).
	AllowHost func(u *url.URL) bool
}

// ErrUnavailable: network, status, size, JSON or a 429 of the upstream.
var ErrUnavailable = errors.New("upstream: unavailable")

// Client talks to Modrinth and Hangar. It is safe for concurrent use.
type Client struct {
	ua        string
	bases     Bases
	allowHost func(u *url.URL) bool
}

// New returns a client with the defaults of Options filled in.
func New(o Options) *Client {
	c := &Client{ua: catalog.UserAgent(o.Version), bases: o.Bases, allowHost: o.AllowHost}
	if c.bases == (Bases{}) {
		c.bases = DefaultBases
	}
	if c.allowHost == nil {
		c.allowHost = DefaultHostAllowed
	}
	return c
}

// UserAgent is sent on every upstream request (same contact format as the version catalogs).
func (c *Client) UserAgent() string { return c.ua }

// Bases returns the API base URLs in use.
func (c *Client) Bases() Bases { return c.bases }

// HostAllowed reports whether a jar or icon (or a redirect on the way) may be fetched from u.
func (c *Client) HostAllowed(u *url.URL) bool { return u != nil && c.allowHost(u) }

// DefaultHostAllowed accepts only https URLs without user info on one of AllowedHosts (host
// compared case-insensitively, port empty or 443).
func DefaultHostAllowed(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" {
		return false
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	return slices.Contains(AllowedHosts, strings.ToLower(u.Hostname()))
}
```

- [ ] **Step 5: Stub-Paket `internal/plugins`**

Neue Datei `internal/plugins/handler.go`:

```go
// Package plugins manages the plugins (paper, purpur) and mods (fabric, neoforge) of a Minecraft
// server (sub-project 3c): inventory, search in Modrinth and Hangar, install and update jobs,
// toggling, removing and an icon proxy. Jars are only written by the unprivileged files
// service; the panel downloads them from fixed CDN hosts and streams them through with their
// checksum.
//
// Phase A of the plan registers the routes as stubs; phase B2 implements them.
package plugins

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

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
// *catalog.Client from catalog.New; same method set as servers.CatalogSource).
type Catalog interface {
	Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error)
}

var _ Catalog = (*catalog.Client)(nil)

// Deps are the dependencies of the plugin handlers (contract V4).
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
}

func NewHandler(d Deps) *Handler {
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
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, files: d.Files, inst: d.Instances,
		catalog: cat, up: up, log: log,
	}
}

// Routes registers the plugin routes (paths relative to /api, contract V3), all with
// server.plugins (admins always).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerPlugins, "id")
	api.Get("/servers/{id}/plugins", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/search", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/versions", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/resolve", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/install", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/updates", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/update", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/toggle", rule, h.notImplemented)
	api.Post("/servers/{id}/plugins/delete", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/jobs", rule, h.notImplemented)
	api.Get("/servers/{id}/plugins/icon", rule, h.notImplemented)
}

// Run blocks until ctx is done; then it cancels running jobs and waits for them (phase A has
// no jobs).
func (h *Handler) Run(ctx context.Context) {
	<-ctx.Done()
}

// notImplemented answers every route until phase B2 replaces the handlers.
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodePluginSourceUnavailable, "plugins not implemented yet")
}
```

Die Felder `q`, `clock`, `rec`, `files`, `inst`, `catalog`, `up` und `log` liest Phase A noch nicht. Sie werden nur im Composite-Literal gesetzt, das reicht `unused` (wie beim Stub von 3b). Meldet golangci-lint trotzdem `U1000`, dann bekommt nur dieses Feld `//nolint:unused // read by phase B2 (plan 03)`.

- [ ] **Step 6: Verdrahtung in `cmd/panel/app.go`**

Imports ergänzen:

```go
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
```

`appDeps` ersetzen (neues Feld `Upstream`; gofmt richtet `Files`/`Version` neu aus):

```go
type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
	// Instances is the agent's instance API (in production the same *agent.Client as Agent);
	// nil means no agent: the panel works, instance states are "unknown".
	Instances agentapi.InstancesClient
	// Console is the agent's console API (logs, RCON, player lists; the same *agent.Client);
	// nil means no agent: console routes answer agent_unavailable.
	Console agentapi.ConsoleClient
	// Files is the files service (mcpanel-files); nil means no service: file routes answer
	// files_unavailable.
	Files filesapi.Client
	// Upstream talks to Modrinth and Hangar; nil means upstream.New with the default bases (the
	// real services). Tests inject httptest servers.
	Upstream *upstream.Client
	Version  string // panel version, sent in the User-Agent of catalog and plugin requests
}
```

In `buildApp` den Block `serversH := servers.NewHandler(...)` ersetzen durch:

```go
	// One version catalog for the create form and for resolving LATEST in the plugin search.
	cat := catalog.New(catalog.Options{Clock: d.Clock, Version: d.Version})
	serversH := servers.NewHandler(servers.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Box: box, Instances: d.Instances,
		Catalog: cat,
		Extract: filesH,
	})
	pluginsH := plugins.NewHandler(plugins.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Files: d.Files, Instances: d.Instances,
		Catalog: cat, Upstream: d.Upstream, Version: d.Version,
	})
```

In `rt.Route` nach `filesH.Routes(api)`:

```go
		pluginsH.Routes(api)
```

`run` ersetzen:

```go
	run := func(ctx context.Context) {
		var wg sync.WaitGroup
		wg.Add(5)
		go func() { defer wg.Done(); disp.Run(ctx) }()
		go func() { defer wg.Done(); svc.Run(ctx) }()
		go func() { defer wg.Done(); serversH.Run(ctx) }()
		go func() { defer wg.Done(); pluginsH.Run(ctx) }()
		go func() { defer wg.Done(); pruneSessionsLoop(ctx, authH.PruneSessions, sessionPruneInterval) }()
		wg.Wait()
	}
```

`cmd/panel/main.go` bleibt unverändert (`Upstream` nil).

- [ ] **Step 7: TS-Typ-Aliase**

`web/src/api/types.ts` nach `FileJobResponse`:

```ts
export type PluginSource = Schemas['PluginSource']
export type PluginKind = Schemas['PluginKind']
export type PluginChannel = Schemas['PluginChannel']
export type PluginJarSource = Schemas['PluginJarSource']
export type PluginProjectRef = Schemas['PluginProjectRef']
export type PluginVersionRef = Schemas['PluginVersionRef']
export type PluginJarMeta = Schemas['PluginJarMeta']
export type InstalledJar = Schemas['InstalledJar']
export type PluginInventory = Schemas['PluginInventory']
export type PluginSearchHit = Schemas['PluginSearchHit']
export type PluginSearchResult = Schemas['PluginSearchResult']
export type PluginVersion = Schemas['PluginVersion']
export type PluginVersionList = Schemas['PluginVersionList']
export type PluginPlanEntry = Schemas['PluginPlanEntry']
export type PluginAlready = Schemas['PluginAlready']
export type PluginExternal = Schemas['PluginExternal']
export type PluginIncompatibleReason = Schemas['PluginIncompatibleReason']
export type PluginIncompatible = Schemas['PluginIncompatible']
export type PluginResolution = Schemas['PluginResolution']
export type InstallPluginRequest = Schemas['InstallPluginRequest']
export type PluginUpdate = Schemas['PluginUpdate']
export type PluginUpdates = Schemas['PluginUpdates']
export type UpdatePluginsRequest = Schemas['UpdatePluginsRequest']
export type TogglePluginRequest = Schemas['TogglePluginRequest']
export type PluginFileRequest = Schemas['PluginFileRequest']
export type PluginJobKind = Schemas['PluginJobKind']
export type PluginJobState = Schemas['PluginJobState']
export type PluginJobStepState = Schemas['PluginJobStepState']
export type PluginStepErrorCode = Schemas['PluginStepErrorCode']
export type PluginJobStep = Schemas['PluginJobStep']
export type PluginJob = Schemas['PluginJob']
export type PluginJobResponse = Schemas['PluginJobResponse']
```

- [ ] **Step 8: Mock-Tests schreiben (rot)**

`web/src/mocks/handlers.test.ts`:

- Typ-Import nach dem `vitest`-Import ergänzen:

```ts
import type { PluginInventory, PluginJob, PluginResolution, PluginSearchResult, PluginUpdates, PluginVersionList } from '@/api/types'
```

- Zählung `expect(ids).toHaveLength(76)` → `expect(ids).toHaveLength(87)`.
- In `describe('mock behaviour')` vor `it('requires server.files for the file browser', …)` einfügen:

```ts
  it('lists the plugin inventory per server type', async () => {
    loginAs('admin')
    const inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv).toMatchObject({ kind: 'plugin', dir: 'plugins', game_version: '1.21.11', sources: ['modrinth', 'hangar'], truncated: false, restart_needed: false, source_errors: [] })
    expect(inv.jars.map((j) => j.file)).toEqual(['Disabled.jar.disabled', 'LuckPerms-Bukkit-5.5.71.jar', 'OldTool.jar', 'ViaVersion-5.5.1.jar'])
    expect(inv.jars.find((j) => j.file === 'OldTool.jar')).toMatchObject({ source: 'manual', project: null, version: null, meta: { name: 'OldTool', version: '1.0' }, installed_by_panel: false })
    expect(inv.jars.find((j) => j.file === 'Disabled.jar.disabled')).toMatchObject({ enabled: false, source: 'modrinth', installed_by_panel: false })
    expect(inv.jars.map((j) => j.project?.title)).toEqual(['BlueMap', 'LuckPerms', undefined, 'ViaVersion'])
    expect(inv.jars[1].project?.icon_url).toMatch(/^\/api\/servers\/1\/plugins\/icon\?url=https%3A%2F%2Fcdn\.modrinth\.com%2F/)
    const mods = (await callApi('GET', '/servers/4/plugins')).body as PluginInventory
    expect(mods).toMatchObject({ kind: 'mod', dir: 'mods', sources: ['modrinth'] })
    expect(mods.jars.map((j) => j.file)).toEqual(['fabric-api-0.110.0.jar'])
    expect(field((await callApi('GET', '/servers/2/plugins')).body, 'error')).toBe('plugins_unsupported')
    expect((await callApi('GET', '/servers/3/plugins')).status).toBe(404)
  })

  it('searches compatible projects per source and kind', async () => {
    loginAs('admin')
    const search = async (id: number, query: string) => (await callApi('GET', `/servers/${id}/plugins/search?${query}`)).body as PluginSearchResult
    const modrinth = await search(1, 'source=modrinth')
    expect(modrinth.hits.map((h) => h.slug).sort()).toEqual(['bluemap', 'chunky', 'chunkyborder', 'luckperms'])
    expect(modrinth.total).toBe(4)
    expect(modrinth.hits.find((h) => h.slug === 'luckperms')?.installed).toBe(true)
    expect(modrinth.hits.find((h) => h.slug === 'chunky')?.installed).toBe(false)
    expect((await search(1, 'source=modrinth&offset=2')).hits).toHaveLength(2)
    const via = await search(1, 'source=hangar&q=ViaBack')
    expect(via.hits.map((h) => h.title)).toEqual(['ViaBackwards'])
    expect(via.hits[0]).toMatchObject({ installed: false, page_url: 'https://hangar.papermc.io/ViaVersion/ViaBackwards' })
    const mods = await search(4, 'source=modrinth')
    expect(mods.hits.map((h) => h.slug).sort()).toEqual(['fabric-api', 'lithium', 'servercore']) // sodium-extra is client-only
    expect(field((await callApi('GET', '/servers/4/plugins/search?source=hangar')).body, 'error')).toBe('validation_failed')
    expect((await callApi('GET', '/servers/1/plugins/search?source=modrinth&offset=1001')).status).toBe(400)
    expect((await callApi('GET', `/servers/1/plugins/search?source=modrinth&q=${'x'.repeat(101)}`)).status).toBe(400)
  })

  it('resolves dependencies and refuses versions that are only available externally', async () => {
    loginAs('admin')
    const resolve = (id: number, query: string) => callApi('GET', `/servers/${id}/plugins/resolve?${query}`)
    const viaBackwards = (await resolve(1, 'source=hangar&project=ViaBackwards')).body as PluginResolution
    expect(viaBackwards.install.map((e) => e.title)).toEqual(['ViaBackwards'])
    expect(viaBackwards.already).toEqual([{ source: 'hangar', project_id: '2', title: 'ViaVersion', file: 'ViaVersion-5.5.1.jar' }])
    const border = (await resolve(1, 'source=modrinth&project=chunkyborder')).body as PluginResolution
    expect(border.install.map((e) => [e.title, e.required_by])).toEqual([
      ['Chunky', 'ChunkyBorder'],
      ['ChunkyBorder', null],
    ])
    expect(border.incompatible).toEqual([])
    // project accepts the slug and the project id; the same installed version is "already", another one is installed
    expect(((await resolve(1, 'source=modrinth&project=Vebnzrzj&version=lp5571')).body as PluginResolution).already.map((a) => a.file)).toEqual(['LuckPerms-Bukkit-5.5.71.jar'])
    expect(((await resolve(1, 'source=modrinth&project=luckperms&version=lp5572')).body as PluginResolution).install.map((e) => e.filename)).toEqual(['LuckPerms-Bukkit-5.5.72.jar'])
    expect(field((await resolve(1, 'source=hangar&project=Essentials')).body, 'error')).toBe('plugin_not_downloadable')
    const essentials = (await callApi('GET', '/servers/1/plugins/versions?source=hangar&project=Essentials')).body as PluginVersionList
    expect(essentials.versions.length).toBeGreaterThan(0)
    expect(essentials.versions.every((x) => !x.downloadable && x.external_url !== null)).toBe(true)
    const lp = (await callApi('GET', '/servers/1/plugins/versions?source=modrinth&project=luckperms')).body as PluginVersionList
    expect(lp.versions.map((x) => x.channel)).toEqual(['beta', 'release', 'release'])
    expect(field((await resolve(4, 'source=modrinth&project=sodium-extra')).body, 'error')).toBe('plugin_incompatible')
    expect(((await resolve(4, 'source=modrinth&project=servercore')).body as PluginResolution).already.map((a) => a.title)).toEqual(['Fabric API'])
    expect((await resolve(1, 'source=modrinth&project=../x')).status).toBe(400)
  })

  it('installs with a job, one job per server', async () => {
    const me = loginAs('admin')
    const install = (body: object) => callApi('POST', '/servers/1/plugins/install', { body, csrf: me.csrf_token })
    const started = await install({ source: 'modrinth', project: 'chunkyborder' })
    expect(started.status).toBe(202)
    const job = started.body as PluginJob
    expect(job).toMatchObject({ kind: 'install', state: 'running', finished_at: 0 })
    expect(job.steps.map((s) => [s.file, s.state])).toEqual([
      ['Chunky-Bukkit-1.4.40.jar', 'downloading'],
      ['ChunkyBorder-Bukkit-1.2.23.jar', 'pending'],
    ])
    expect(field((await install({ source: 'hangar', project: 'ViaBackwards' })).body, 'error')).toBe('conflict')
    expect(field((await install({ source: 'modrinth', project: 'luckperms', extra: 1 })).body, 'error')).toBe('validation_failed')
    db.pluginJobs[1].startedMs -= 60_000
    const done = field((await callApi('GET', '/servers/1/plugins/jobs')).body, 'job') as PluginJob
    expect(done.state).toBe('done')
    expect(done.steps.every((s) => s.state === 'done')).toBe(true)
    const inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv.jars.map((j) => j.file)).toEqual(expect.arrayContaining(['Chunky-Bukkit-1.4.40.jar', 'ChunkyBorder-Bukkit-1.2.23.jar']))
    expect(inv.restart_needed).toBe(true) // survival runs and the panel changed its plugins
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.install', target: 'server:1', details: { project: 's86X568j', version: 'cb1223', file: 'ChunkyBorder-Bukkit-1.2.23.jar' } })
    const via = await install({ source: 'hangar', project: 'ViaBackwards' })
    expect((via.body as PluginJob).steps.map((s) => s.title)).toEqual(['ViaBackwards'])
    db.pluginJobs[1].startedMs -= 60_000
    // nothing to do: the requested project is installed (no version, or the installed one) -> 409 conflict
    expect(field((await install({ source: 'modrinth', project: 'chunkyborder' })).body, 'error')).toBe('conflict')
    expect(((await callApi('GET', '/servers/1/plugins/resolve?source=modrinth&project=luckperms')).body as PluginResolution).already.map((a) => a.file)).toEqual(['LuckPerms-Bukkit-5.5.71.jar'])
  })

  it('updates, toggles and removes jars', async () => {
    const me = loginAs('admin')
    const post = (path: string, body: unknown) => callApi('POST', `/servers/1/plugins/${path}`, { body, csrf: me.csrf_token })
    const updates = (await callApi('GET', '/servers/1/plugins/updates')).body as PluginUpdates
    expect(updates.updates).toEqual([
      { file: 'LuckPerms-Bukkit-5.5.71.jar', from: { id: 'lp5571', name: '5.5.71', channel: 'release' }, to: { id: 'lp5572', name: '5.5.72', channel: 'release' } },
    ])
    for (const refresh of ['true', '1']) {
      const refreshed = (await callApi('GET', `/servers/1/plugins/updates?refresh=${refresh}`)).body as PluginUpdates
      expect(refreshed.checked_at).toBeGreaterThan(updates.checked_at)
    }
    expect(field((await post('update', { files: ['OldTool.jar'] })).body, 'error')).toBe('validation_failed') // no update for a manual jar
    expect((await post('update', { files: [] })).status).toBe(202)
    expect(field((await post('toggle', { file: 'OldTool.jar', enabled: false })).body, 'error')).toBe('conflict')
    db.pluginJobs[1].startedMs -= 60_000
    let inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv.jars.find((j) => j.project?.slug === 'luckperms')).toMatchObject({ file: 'LuckPerms-Bukkit-5.5.72.jar', version: { name: '5.5.72' } })
    expect(inv.jars.map((j) => j.file)).not.toContain('LuckPerms-Bukkit-5.5.71.jar')
    expect(((await callApi('GET', '/servers/1/plugins/updates')).body as PluginUpdates).updates).toEqual([])
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.update', details: { file: 'LuckPerms-Bukkit-5.5.72.jar', from: '5.5.71', to: '5.5.72' } })

    expect((await post('toggle', { file: 'OldTool.jar', enabled: false })).status).toBe(204)
    inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    const old = inv.jars.find((j) => j.file === 'OldTool.jar.disabled')
    expect(old).toMatchObject({ enabled: false, meta: { name: 'OldTool' } })
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.toggle', details: { file: 'OldTool.jar', enabled: false } })
    expect((await post('toggle', { file: 'OldTool.jar', enabled: true })).status).toBe(404)
    expect((await post('toggle', { file: '../x.jar', enabled: true })).status).toBe(400)
    expect((await post('toggle', { file: 'server.properties', enabled: true })).status).toBe(400)
    // Enabling must not overwrite another jar with the target name (case-insensitive).
    if (!old) throw new Error('OldTool.jar.disabled missing')
    db.plugins[1].jars.push({ ...old, file: 'oldtool.JAR', enabled: true })
    expect(field((await post('toggle', { file: 'OldTool.jar.disabled', enabled: true })).body, 'error')).toBe('file_exists')

    expect((await post('delete', { file: 'OldTool.jar.disabled' })).status).toBe(204)
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.remove', details: { file: 'OldTool.jar.disabled' } })
    inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv.jars.map((j) => j.file)).not.toContain('OldTool.jar.disabled')
    expect(inv.restart_needed).toBe(true)
    expect((await post('delete', { file: 'OldTool.jar.disabled' })).status).toBe(404)
  })

  it('requires server.plugins and serves icons only from the CDNs', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/servers/1/plugins')).status).toBe(403)
    expect((await callApi('GET', `/servers/1/plugins/icon?url=${encodeURIComponent('https://cdn.modrinth.com/x.png')}`)).status).toBe(403)
    loginAs('admin')
    const icon = await fetch(`${window.location.origin}/api/servers/1/plugins/icon?url=${encodeURIComponent('https://cdn.modrinth.com/data/Vebnzrzj/icon.png')}`)
    expect(icon.status).toBe(200)
    expect(icon.headers.get('Content-Type')).toBe('image/png')
    expect([...new Uint8Array(await icon.arrayBuffer()).slice(0, 4)]).toEqual([0x89, 0x50, 0x4e, 0x47])
    expect((await callApi('GET', `/servers/1/plugins/icon?url=${encodeURIComponent('https://evil.example/x.png')}`)).status).toBe(400)
    expect((await callApi('GET', `/servers/1/plugins/icon?url=${encodeURIComponent('http://cdn.modrinth.com/x.png')}`)).status).toBe(400)
    // The icon route has no plugins_unsupported check (V3).
    expect((await fetch(`${window.location.origin}/api/servers/2/plugins/icon?url=${encodeURIComponent('https://cdn.modrinth.com/x.png')}`)).status).toBe(200)
  })
```

`npx vitest run src/mocks/handlers.test.ts` läuft nur in der CI (`db` wird vor jedem Test durch `resetDb()` in `web/src/test/setup.ts` neu gesät; die Tests beenden Jobs, indem sie `db.pluginJobs[1].startedMs` zurückdatieren).

- [ ] **Step 9: Mock implementieren**

`web/src/mocks/db.ts`:

1. Im Typ-Import aus `@/api/types` `InstalledJar` (nach `FileJob`) sowie `PluginJob` und `PluginKind` (nach `PlayerOp`) ergänzen.
2. Direkt vor `/** Every property set: spec §7 defaults …` (nach `mockTree`, damit `MIB`, `DAY` und `MINUTE` definiert sind) einfügen:

```ts
/** Plugins or mods of a mock server: the jars "on disk" plus what the panel remembers about them. */
export interface MockPluginInventory {
  kind: PluginKind
  dir: 'plugins' | 'mods'
  jars: InstalledJar[]
  changedAt: number // last plugin change by the panel (Unix ms, 0 = none); restart_needed compares it with started_at
  checkedAt: number // last update check (Unix ms, 0 = never)
}

/** What a finished job step does: remove the old jar (if any), add the new one, write its audit entry. */
export interface MockPluginChange {
  remove: string | null
  add: InstalledJar
  action: 'plugin.install' | 'plugin.update'
  details: Record<string, unknown>
}

export interface MockPluginJob {
  job: PluginJob
  actorId: number
  startedMs: number
  changes: Array<MockPluginChange | null> // per step; null = skipped or failed at the start
}

/** icon_url as the panel sends it: the icon proxy with the CDN URL as query parameter. */
export function pluginIconUrl(serverId: number, cdnUrl: string | null): string | null {
  return cdnUrl === null ? null : `/api/servers/${serverId}/plugins/icon?url=${encodeURIComponent(cdnUrl)}`
}

/** Inventories of the seeded servers (V5): survival (paper) and modded (fabric). */
function seedPlugins(now: number): Record<number, MockPluginInventory> {
  const t = now - 3 * DAY
  const modrinthIcon = (serverId: number, projectId: string) => pluginIconUrl(serverId, `https://cdn.modrinth.com/data/${projectId}/icon.png`)
  return {
    1: {
      kind: 'plugin',
      dir: 'plugins',
      changedAt: 0,
      checkedAt: now - 10 * MINUTE,
      jars: [
        {
          file: 'Disabled.jar.disabled',
          enabled: false,
          size: 3 * MIB,
          mtime: t,
          source: 'modrinth',
          project: { id: 'swbUV1cr', slug: 'bluemap', title: 'BlueMap', page_url: 'https://modrinth.com/plugin/bluemap', icon_url: modrinthIcon(1, 'swbUV1cr') },
          version: { id: 'bm57', name: '5.7', channel: 'release' },
          meta: { kind: 'plugin', id: '', name: 'BlueMap', version: '5.7' },
          installed_by_panel: false, // matched by its hash
        },
        {
          file: 'LuckPerms-Bukkit-5.5.71.jar',
          enabled: true,
          size: 2 * MIB,
          mtime: t,
          source: 'modrinth',
          project: { id: 'Vebnzrzj', slug: 'luckperms', title: 'LuckPerms', page_url: 'https://modrinth.com/plugin/luckperms', icon_url: modrinthIcon(1, 'Vebnzrzj') },
          version: { id: 'lp5571', name: '5.5.71', channel: 'release' },
          meta: { kind: 'plugin', id: '', name: 'LuckPerms', version: '5.5.71' },
          installed_by_panel: true,
        },
        {
          file: 'OldTool.jar',
          enabled: true,
          size: 48 * 1024,
          mtime: t - 30 * DAY,
          source: 'manual',
          project: null,
          version: null,
          meta: { kind: 'plugin', id: '', name: 'OldTool', version: '1.0' },
          installed_by_panel: false,
        },
        {
          file: 'ViaVersion-5.5.1.jar',
          enabled: true,
          size: 5 * MIB,
          mtime: t,
          source: 'hangar',
          project: {
            id: '2',
            slug: 'ViaVersion',
            title: 'ViaVersion',
            page_url: 'https://hangar.papermc.io/ViaVersion/ViaVersion',
            icon_url: pluginIconUrl(1, 'https://hangarcdn.papermc.io/avatars/project/2.webp'),
          },
          version: { id: '5.5.1', name: '5.5.1', channel: 'release' },
          meta: { kind: 'plugin', id: '', name: 'ViaVersion', version: '5.5.1' },
          installed_by_panel: true,
        },
      ],
    },
    4: {
      kind: 'mod',
      dir: 'mods',
      changedAt: 0,
      checkedAt: now - 10 * MINUTE,
      jars: [
        {
          file: 'fabric-api-0.110.0.jar',
          enabled: true,
          size: 2 * MIB,
          mtime: t,
          source: 'modrinth',
          project: { id: 'P7dR8mSH', slug: 'fabric-api', title: 'Fabric API', page_url: 'https://modrinth.com/mod/fabric-api', icon_url: modrinthIcon(4, 'P7dR8mSH') },
          version: { id: 'fapi0110', name: '0.110.0+1.21.11', channel: 'release' },
          meta: { kind: 'mod', id: 'fabric-api', name: 'Fabric API', version: '0.110.0+1.21.11' },
          installed_by_panel: false, // matched by its hash
        },
      ],
    },
  }
}

```

3. In `MockDb` nach `fileJobs`:

```ts
  plugins: Record<number, MockPluginInventory> // plugins/mods per server id (created on first access)
  pluginJobs: Record<number, MockPluginJob> // running or last plugin job per server id
```

4. In `seed()` nach `fileJobs: {},`:

```ts
    plugins: seedPlugins(now),
    pluginJobs: {},
```

5. In `seed()` unter `servers` nach dem Server `lobby` (id 3) den Fabric-Server anfügen:

```ts
      {
        id: 4,
        name: 'modded',
        instance: { type: 'fabric', version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: resolveProperties('modded') },
        status: stoppedStatus(),
        revision: 1,
        runningRevision: 1,
        players: emptyPlayers(),
      },
```

   alice hat auf Server 4 keine Rechte, `shows a restricted user only the servers they hold permissions on` bleibt also bei `['survival', 'creative']`. Die Dashboard-Tests und Playwright prüfen Server nur über ihren Namen.

Neue Datei `web/src/mocks/handlers/plugins.ts`:

```ts
import { http, HttpResponse } from 'msw'
import type {
  InstallPluginRequest,
  InstalledJar,
  PluginChannel,
  PluginFileRequest,
  PluginInventory,
  PluginJob,
  PluginJobStep,
  PluginKind,
  PluginPlanEntry,
  PluginProjectRef,
  PluginResolution,
  PluginSearchHit,
  PluginSource,
  PluginUpdate,
  PluginVersion,
  TogglePluginRequest,
  UpdatePluginsRequest,
} from '@/api/types'
import { DAY, db, findUser, pluginIconUrl, recordAudit, type MockPluginChange, type MockPluginInventory, type MockServer } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

/** A mock job takes about this long; its steps get equal shares. */
export const PLUGIN_JOB_MS = 4000
const PAGE = 20
const MAX_OFFSET = 1000
const MAX_QUERY = 100
const MAX_VERSIONS = 25
const MAX_JOBS = 4 // panel-wide (V3: 429 rate_limited)
const MAX_DEPTH = 5
const MAX_PACKAGES = 20
const MIB = 1024 * 1024
const NAME_RE = /^[A-Za-z0-9._+-]{1,128}$/
const JAR_RE = /^.+\.jar(\.disabled)?$/i
// eslint-disable-next-line no-control-regex -- matches control characters on purpose (filesapi.CleanPath)
const BAD_FILE_RE = /[\u0000-\u001f\u007f\\/]/
const ICON_PREFIXES = ['https://cdn.modrinth.com/', 'https://hangarcdn.papermc.io/']
const CHANNEL_RANK: Record<PluginChannel, number> = { release: 0, beta: 1, alpha: 2 }
/** A 1×1 PNG, served for every icon. */
const ICON_PNG = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII='), (c) => c.charCodeAt(0))

type Params = Parameters<typeof idParam>[0]

interface MockVersion {
  id: string
  name: string
  channel: PluginChannel
  daysAgo: number
  filename: string
  size: number
  external: string | null // only available externally (Hangar externalUrl)
}

interface MockProject {
  source: PluginSource
  kind: PluginKind
  id: string
  slug: string
  title: string
  author: string
  description: string
  downloads: number
  clientOnly: boolean // Modrinth server_side: unsupported
  deps: string[] // required dependencies: project ids of the same source
  versions: MockVersion[] // newest first; every one is compatible with every game version
}

function v(id: string, name: string, channel: PluginChannel, daysAgo: number, filename: string, external: string | null = null): MockVersion {
  return { id, name, channel, daysAgo, filename, size: 2 * MIB, external }
}

/** The mock catalog (V5 plus chunkyborder, deviation 7 of plan 01). */
const CATALOG: MockProject[] = [
  {
    source: 'modrinth', kind: 'plugin', id: 'Vebnzrzj', slug: 'luckperms', title: 'LuckPerms', author: 'Luck',
    description: 'A permissions plugin for Minecraft servers.', downloads: 3_400_000, clientOnly: false, deps: [],
    versions: [
      v('lp560b1', '5.6.0-beta.1', 'beta', 2, 'LuckPerms-Bukkit-5.6.0-beta.1.jar'),
      v('lp5572', '5.5.72', 'release', 5, 'LuckPerms-Bukkit-5.5.72.jar'),
      v('lp5571', '5.5.71', 'release', 40, 'LuckPerms-Bukkit-5.5.71.jar'),
    ],
  },
  {
    source: 'modrinth', kind: 'plugin', id: 'fALzjamp', slug: 'chunky', title: 'Chunky', author: 'pop4959',
    description: 'Pre-generates chunks, quickly and efficiently.', downloads: 2_100_000, clientOnly: false, deps: [],
    versions: [v('ch1441b', '1.4.41-beta', 'beta', 3, 'Chunky-Bukkit-1.4.41-beta.jar'), v('ch1440', '1.4.40', 'release', 20, 'Chunky-Bukkit-1.4.40.jar')],
  },
  {
    source: 'modrinth', kind: 'plugin', id: 's86X568j', slug: 'chunkyborder', title: 'ChunkyBorder', author: 'pop4959',
    description: 'A world border plugin that works with Chunky.', downloads: 600_000, clientOnly: false, deps: ['fALzjamp'],
    versions: [v('cb1224b', '1.2.24-beta', 'beta', 4, 'ChunkyBorder-Bukkit-1.2.24-beta.jar'), v('cb1223', '1.2.23', 'release', 25, 'ChunkyBorder-Bukkit-1.2.23.jar')],
  },
  {
    source: 'modrinth', kind: 'plugin', id: 'swbUV1cr', slug: 'bluemap', title: 'BlueMap', author: 'TBlueF',
    description: 'A 3D map of your Minecraft world in the browser.', downloads: 1_500_000, clientOnly: false, deps: [],
    versions: [v('bm58b', '5.8-beta', 'beta', 6, 'bluemap-5.8-beta-paper.jar'), v('bm57', '5.7', 'release', 30, 'bluemap-5.7-paper.jar')],
  },
  {
    source: 'hangar', kind: 'plugin', id: '2', slug: 'ViaVersion', title: 'ViaVersion', author: 'ViaVersion',
    description: 'Allows newer clients to connect to older servers.', downloads: 9_000_000, clientOnly: false, deps: [],
    versions: [v('5.6.0-beta', '5.6.0-beta', 'beta', 1, 'ViaVersion-5.6.0-beta.jar'), v('5.5.1', '5.5.1', 'release', 12, 'ViaVersion-5.5.1.jar')],
  },
  {
    source: 'hangar', kind: 'plugin', id: '3', slug: 'ViaBackwards', title: 'ViaBackwards', author: 'ViaVersion',
    description: 'Allows older clients to connect to newer servers.', downloads: 4_000_000, clientOnly: false, deps: ['2'],
    versions: [v('5.6.0-beta', '5.6.0-beta', 'beta', 1, 'ViaBackwards-5.6.0-beta.jar'), v('5.5.1', '5.5.1', 'release', 12, 'ViaBackwards-5.5.1.jar')],
  },
  {
    source: 'hangar', kind: 'plugin', id: '50', slug: 'Essentials', title: 'EssentialsX', author: 'EssentialsX',
    description: 'The essential plugin suite for Minecraft servers.', downloads: 5_000_000, clientOnly: false, deps: [],
    versions: [
      v('2.22.0-dev', '2.22.0-dev', 'beta', 2, 'EssentialsX-2.22.0-dev.jar', 'https://essentialsx.net/downloads.html'),
      v('2.21.2', '2.21.2', 'release', 60, 'EssentialsX-2.21.2.jar', 'https://github.com/EssentialsX/Essentials/releases/tag/2.21.2'),
    ],
  },
  {
    source: 'hangar', kind: 'plugin', id: '25', slug: 'Chunky', title: 'Chunky', author: 'pop4959',
    description: 'Pre-generates chunks, quickly and efficiently.', downloads: 800_000, clientOnly: false, deps: [],
    versions: [v('1.4.41-beta', '1.4.41-beta', 'beta', 3, 'Chunky-Bukkit-1.4.41-beta.jar'), v('1.4.40', '1.4.40', 'release', 20, 'Chunky-Bukkit-1.4.40.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: 'PtjYWJkn', slug: 'sodium-extra', title: 'Sodium Extra', author: 'FlashyReese',
    description: 'Extra client-side options for Sodium.', downloads: 8_000_000, clientOnly: true, deps: [],
    versions: [v('se0613b', '0.6.13-beta', 'beta', 2, 'sodium-extra-0.6.13-beta.jar'), v('se0612', '0.6.12', 'release', 15, 'sodium-extra-0.6.12.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: 'gvQqBUqZ', slug: 'lithium', title: 'Lithium', author: 'CaffeineMC',
    description: 'Optimizes game physics, mob AI and block ticking on the server.', downloads: 30_000_000, clientOnly: false, deps: [],
    versions: [v('li0151b', '0.15.1-beta', 'beta', 2, 'lithium-fabric-0.15.1-beta.jar'), v('li0150', '0.15.0', 'release', 14, 'lithium-fabric-0.15.0.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: 'P7dR8mSH', slug: 'fabric-api', title: 'Fabric API', author: 'modmuss50',
    description: 'Core library for most Fabric mods.', downloads: 90_000_000, clientOnly: false, deps: [],
    versions: [v('fapi0111b', '0.111.0-beta+1.21.11', 'beta', 1, 'fabric-api-0.111.0-beta.jar'), v('fapi0110', '0.110.0+1.21.11', 'release', 10, 'fabric-api-0.110.0.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: '4WWQxlQP', slug: 'servercore', title: 'ServerCore', author: 'Wesley1808',
    description: 'Performance improvements and configuration for Fabric servers.', downloads: 1_200_000, clientOnly: false, deps: ['P7dR8mSH'],
    versions: [v('sc159b', '1.5.9-beta', 'beta', 2, 'servercore-fabric-1.5.9-beta.jar'), v('sc158', '1.5.8', 'release', 18, 'servercore-fabric-1.5.8.jar')],
  },
]

interface Ctx {
  g: Authed
  server: MockServer
  kind: PluginKind
  inv: MockPluginInventory
}

function kindOf(server: MockServer): PluginKind | null {
  switch (server.instance?.type) {
    case 'paper':
    case 'purpur':
      return 'plugin'
    case 'fabric':
    case 'neoforge':
      return 'mod'
    default:
      return null
  }
}

/** Session, server.plugins, server lookup and type check shared by every operation except the icon. */
function access(request: Request, params: Params): Ctx | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes('server.plugins')) return apiError(403, 'forbidden', 'missing permission server.plugins')
  const server = db.servers.find((s) => s.id === id)
  if (!server?.instance) return apiError(404, 'not_found', 'server not found')
  const kind = kindOf(server)
  if (!kind) return apiError(409, 'plugins_unsupported', 'this server type has no plugins or mods')
  db.plugins[id] ??= { kind, dir: kind === 'plugin' ? 'plugins' : 'mods', jars: [], changedAt: 0, checkedAt: 0 }
  jobNow(id) // apply finished steps before anything reads the inventory
  return { g, server, kind, inv: db.plugins[id] }
}

function validation(message: string): ErrorResponse {
  return apiError(400, 'validation_failed', message)
}

/** The instance's game version, LATEST resolved through the mock catalog (null = catalog unavailable). */
function gameVersion(server: MockServer): string | null {
  const inst = server.instance
  if (!inst) return null
  if (inst.version !== 'LATEST') return inst.version
  return db.catalogs[inst.type]?.versions[0]?.id ?? null
}

function catalogUnavailable(): ErrorResponse {
  return apiError(502, 'catalog_unavailable', 'the newest game version cannot be resolved')
}

function sourcesOf(kind: PluginKind): PluginSource[] {
  return kind === 'plugin' ? ['modrinth', 'hangar'] : ['modrinth']
}

/** source query/body value; hangar only for plugins. */
function parseSource(raw: unknown, kind: PluginKind): PluginSource | null {
  if (raw === 'modrinth') return raw
  if (raw === 'hangar' && kind === 'plugin') return raw
  return null
}

function findProject(source: PluginSource, kind: PluginKind, ref: string): MockProject | undefined {
  return CATALOG.find((p) => p.source === source && p.kind === kind && (p.id === ref || p.slug.toLowerCase() === ref.toLowerCase()))
}

function iconCdn(p: MockProject): string {
  return p.source === 'modrinth' ? `https://cdn.modrinth.com/data/${p.id}/icon.png` : `https://hangarcdn.papermc.io/avatars/project/${p.id}.webp`
}

function pageUrl(p: MockProject): string {
  return p.source === 'modrinth' ? `https://modrinth.com/${p.kind}/${p.slug}` : `https://hangar.papermc.io/${p.author}/${p.slug}`
}

function projectRef(serverId: number, p: MockProject): PluginProjectRef {
  return { id: p.id, slug: p.slug, title: p.title, page_url: pageUrl(p), icon_url: pluginIconUrl(serverId, iconCdn(p)) }
}

/** The jar of a project in the inventory (enabled or disabled). */
function installedJar(inv: MockPluginInventory, source: PluginSource, projectId: string): InstalledJar | undefined {
  return inv.jars.find((j) => j.source === source && j.project?.id === projectId)
}

/** Lower-case name without ".disabled": two jars with the same base collide. */
function baseName(file: string): string {
  return file.toLowerCase().replace(/\.disabled$/, '')
}

function jarTitle(jar: InstalledJar): string {
  return jar.project?.title ?? jar.meta?.name ?? jar.file
}

/** Same rules as the panel's file parameter: a plain name ending in .jar or .jar.disabled. */
function validJarFile(file: unknown): file is string {
  if (typeof file !== 'string' || new TextEncoder().encode(file).length > 255 || BAD_FILE_RE.test(file) || !JAR_RE.test(file)) return false
  const lower = file.toLowerCase()
  return !lower.startsWith('.mcpanel-') && !lower.startsWith('.rcon-cli.')
}

function onlyKeys(body: object, keys: string[]): boolean {
  return Object.keys(body).every((k) => keys.includes(k))
}

function newestDownloadable(p: MockProject): MockVersion | undefined {
  return p.versions.find((x) => x.external === null && x.channel === 'release') ?? p.versions.find((x) => x.external === null)
}

function newJar(serverId: number, p: MockProject, ver: MockVersion, enabled: boolean): InstalledJar {
  return {
    file: enabled ? ver.filename : `${ver.filename}.disabled`,
    enabled,
    size: ver.size,
    mtime: Date.now(),
    source: p.source,
    project: projectRef(serverId, p),
    version: { id: ver.id, name: ver.name, channel: ver.channel },
    meta: { kind: p.kind, id: p.kind === 'mod' ? p.slug : '', name: p.title, version: ver.name },
    installed_by_panel: true,
  }
}

function planEntry(p: MockProject, ver: MockVersion, requiredBy: string | null): PluginPlanEntry {
  return {
    source: p.source,
    project_id: p.id,
    slug: p.slug,
    title: p.title,
    version_id: ver.id,
    version_name: ver.name,
    channel: ver.channel,
    filename: ver.filename,
    size: ver.size,
    required_by: requiredBy,
  }
}

interface PlanItem {
  entry: PluginPlanEntry
  project: MockProject
  version: MockVersion
}

/**
 * The plan of resolve and install (spec §6.3): required dependencies first (depth 5, 20 packages, cycles
 * ignored), installed projects as `already`. The requested project itself is `already` when it is installed
 * and no version or exactly the installed version is requested (like plan 03, deviation 4); another version
 * is installed and replaces its old jar (switching versions).
 */
function buildPlan(c: Ctx, source: PluginSource, ref: string, versionId: string | null): { resolution: PluginResolution; items: PlanItem[] } | ErrorResponse {
  const root = findProject(source, c.kind, ref)
  if (!root) return apiError(404, 'not_found', 'project not found')
  if (root.clientOnly) return apiError(409, 'plugin_incompatible', `${root.title} is a client-side mod`)
  let version: MockVersion | undefined
  if (versionId !== null) {
    version = root.versions.find((x) => x.id === versionId)
    if (!version) return apiError(404, 'not_found', 'version not found')
    if (version.external !== null) return apiError(409, 'plugin_not_downloadable', 'this version is only available externally')
  } else {
    version = newestDownloadable(root)
    if (!version) {
      return root.versions.length > 0
        ? apiError(409, 'plugin_not_downloadable', 'every compatible version is only available externally')
        : apiError(409, 'plugin_incompatible', 'no compatible version')
    }
  }
  const resolution: PluginResolution = { install: [], already: [], external: [], incompatible: [] }
  const items: PlanItem[] = []
  const have = installedJar(c.inv, root.source, root.id)
  if (have && (versionId === null || have.version?.id === version.id)) {
    resolution.already.push({ source: root.source, project_id: root.id, title: root.title, file: have.file })
    return { resolution, items }
  }
  const seen = new Set<string>([root.id])
  const visit = (p: MockProject, depth: number) => {
    for (const depId of p.deps) {
      if (seen.has(depId)) continue
      seen.add(depId)
      const dep = CATALOG.find((d) => d.source === p.source && d.id === depId)
      if (!dep) {
        resolution.incompatible.push({ title: depId, reason: 'no_compatible_version' })
        continue
      }
      const have = installedJar(c.inv, dep.source, dep.id)
      if (have) {
        resolution.already.push({ source: dep.source, project_id: dep.id, title: dep.title, file: have.file })
        continue
      }
      if (dep.clientOnly) {
        resolution.incompatible.push({ title: dep.title, reason: 'client_only' })
        continue
      }
      if (depth >= MAX_DEPTH) {
        resolution.incompatible.push({ title: dep.title, reason: 'too_deep' })
        continue
      }
      if (items.length + 1 >= MAX_PACKAGES) {
        resolution.incompatible.push({ title: dep.title, reason: 'too_many' })
        continue
      }
      const depVersion = newestDownloadable(dep)
      if (!depVersion) {
        resolution.incompatible.push({ title: dep.title, reason: dep.versions.length > 0 ? 'not_downloadable' : 'no_compatible_version' })
        continue
      }
      visit(dep, depth + 1) // its own dependencies come first
      items.push({ entry: planEntry(dep, depVersion, p.title), project: dep, version: depVersion })
    }
  }
  visit(root, 1)
  items.push({ entry: planEntry(root, version, null), project: root, version })
  resolution.install = items.map((i) => i.entry)
  return { resolution, items }
}

interface MockUpdate {
  jar: InstalledJar
  project: MockProject
  to: MockVersion
}

/** Updates of Modrinth/Hangar jars: the newest downloadable version in the same or a more stable channel. */
function availableUpdates(inv: MockPluginInventory): MockUpdate[] {
  const out: MockUpdate[] = []
  for (const jar of inv.jars) {
    const { project: ref, version: current } = jar
    if (jar.source === 'manual' || !ref || !current) continue
    const project = CATALOG.find((p) => p.source === jar.source && p.id === ref.id)
    if (!project) continue
    const to = project.versions.find((x) => x.external === null && CHANNEL_RANK[x.channel] <= CHANNEL_RANK[current.channel])
    if (!to || to.id === current.id) continue
    const currentIndex = project.versions.findIndex((x) => x.id === current.id)
    if (currentIndex >= 0 && project.versions.indexOf(to) > currentIndex) continue // only newer versions
    out.push({ jar, project, to })
  }
  return out.sort((a, b) => (a.jar.file < b.jar.file ? -1 : a.jar.file > b.jar.file ? 1 : 0))
}

/** Applies a finished step to the inventory and audits it as the user who started the job. */
function applyChange(serverId: number, actorId: number, change: MockPluginChange | null): void {
  const inv = db.plugins[serverId]
  if (!change || !inv) return
  const addBase = baseName(change.add.file)
  inv.jars = inv.jars.filter((j) => j.file !== change.remove && baseName(j.file) !== addBase)
  inv.jars.push(change.add)
  inv.changedAt = Date.now()
  recordAudit(findUser(actorId) ?? null, change.action, `server:${serverId}`, 'success', change.details)
}

/** Progress of a mock job: every step takes PLUGIN_JOB_MS / steps, pending → downloading → done. */
function jobNow(serverId: number): PluginJob | null {
  const mj = db.pluginJobs[serverId]
  if (!mj) return null
  const { job } = mj
  if (job.state === 'running') {
    const stepMs = PLUGIN_JOB_MS / Math.max(1, job.steps.length)
    const elapsed = Date.now() - mj.startedMs
    job.steps.forEach((step, i) => {
      if (step.state !== 'pending' && step.state !== 'downloading') return
      if (elapsed >= (i + 1) * stepMs) {
        applyChange(serverId, mj.actorId, mj.changes[i])
        step.state = 'done'
      } else if (elapsed >= i * stepMs) {
        step.state = 'downloading'
      }
    })
    if (job.steps.every((s) => s.state !== 'pending' && s.state !== 'downloading')) {
      job.state = job.steps.some((s) => s.state === 'failed') ? 'failed' : 'done'
      job.finished_at = Date.now()
    }
  }
  return structuredClone(job)
}

function runningJobs(): number {
  return Object.keys(db.pluginJobs).filter((id) => jobNow(Number(id))?.state === 'running').length
}

/** 409 conflict while this server runs a job, 429 when four run panel-wide. */
function jobBlocked(c: Ctx): ErrorResponse | null {
  if (jobNow(c.server.id)?.state === 'running') return apiError(409, 'conflict', 'a plugin job of this server is running')
  if (runningJobs() >= MAX_JOBS) return apiError(429, 'rate_limited', 'too many plugin jobs are running, try again shortly')
  return null
}

function startJob(c: Ctx, kind: PluginJob['kind'], steps: PluginJobStep[], changes: Array<MockPluginChange | null>): PluginJob | null {
  const now = Date.now()
  db.pluginJobs[c.server.id] = {
    job: { id: `pjob-${now}`, kind, state: 'running', steps, started_at: now, finished_at: 0 },
    actorId: c.g.user.id,
    startedMs: now,
    changes,
  }
  return jobNow(c.server.id)
}

function step(title: string, file: string): PluginJobStep {
  return { title, file, state: 'pending', error_code: null, error: '' }
}

function inventory(c: Ctx): PluginInventory {
  const jars = [...c.inv.jars].sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : 0))
  return {
    kind: c.kind,
    dir: c.inv.dir,
    game_version: gameVersion(c.server),
    sources: sourcesOf(c.kind),
    jars: structuredClone(jars),
    truncated: false,
    restart_needed: c.server.status.state === 'running' && c.inv.changedAt > c.server.status.started_at,
    source_errors: [],
  }
}

function toVersion(x: MockVersion): PluginVersion {
  return {
    id: x.id,
    name: x.name,
    channel: x.channel,
    published_at: Date.now() - x.daysAgo * DAY,
    downloadable: x.external === null,
    external_url: x.external,
  }
}

/** Shared query checks of versions and resolve. */
function projectQuery(c: Ctx, url: URL): { source: PluginSource; project: string } | ErrorResponse {
  const source = parseSource(url.searchParams.get('source'), c.kind)
  if (!source) return validation('source must be modrinth or hangar (hangar only for plugins)')
  const project = url.searchParams.get('project') ?? ''
  if (!NAME_RE.test(project)) return validation('project: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
  return { source, project }
}

export const pluginsOperations: MockOperation[] = [
  op(
    'listPlugins',
    http.get(`${API}/servers/:id/plugins`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      return HttpResponse.json(inventory(c))
    }),
  ),
  op(
    'searchPlugins',
    http.get(`${API}/servers/:id/plugins/search`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const url = new URL(request.url)
      const source = parseSource(url.searchParams.get('source'), c.kind)
      if (!source) return validation('source must be modrinth or hangar (hangar only for plugins)')
      const q = url.searchParams.get('q') ?? ''
      if (q.length > MAX_QUERY) return validation(`q: at most ${MAX_QUERY} characters`)
      const rawOffset = url.searchParams.get('offset') ?? '0'
      const offset = Number(rawOffset)
      if (!/^\d+$/.test(rawOffset) || offset > MAX_OFFSET) return validation(`offset: 0 to ${MAX_OFFSET}`)
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const needle = q.trim().toLowerCase()
      const matches = CATALOG.filter(
        (p) => p.source === source && p.kind === c.kind && !p.clientOnly && (needle === '' || [p.title, p.slug, p.description].some((s) => s.toLowerCase().includes(needle))),
      ).sort((a, b) => b.downloads - a.downloads)
      const hits: PluginSearchHit[] = matches.slice(offset, offset + PAGE).map((p) => ({
        source: p.source,
        project_id: p.id,
        slug: p.slug,
        title: p.title,
        description: p.description,
        author: p.author,
        downloads: p.downloads,
        icon_url: pluginIconUrl(c.server.id, iconCdn(p)),
        page_url: pageUrl(p),
        installed: installedJar(c.inv, p.source, p.id) !== undefined,
      }))
      return HttpResponse.json({ hits, total: matches.length, offset })
    }),
  ),
  op(
    'listPluginVersions',
    http.get(`${API}/servers/:id/plugins/versions`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const q = projectQuery(c, new URL(request.url))
      if (q instanceof Response) return q
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const p = findProject(q.source, c.kind, q.project)
      if (!p) return apiError(404, 'not_found', 'project not found')
      return HttpResponse.json({ versions: p.clientOnly ? [] : p.versions.slice(0, MAX_VERSIONS).map(toVersion) })
    }),
  ),
  op(
    'resolvePlugin',
    http.get(`${API}/servers/:id/plugins/resolve`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const url = new URL(request.url)
      const q = projectQuery(c, url)
      if (q instanceof Response) return q
      const version = url.searchParams.get('version')
      if (version !== null && !NAME_RE.test(version)) return validation('version: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const plan = buildPlan(c, q.source, q.project, version)
      if (plan instanceof Response) return plan
      return HttpResponse.json(plan.resolution)
    }),
  ),
  op(
    'installPlugin',
    http.post(`${API}/servers/:id/plugins/install`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<InstallPluginRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['source', 'project', 'version'])) return validation('invalid body')
      const source = parseSource(body.source, c.kind)
      if (!source) return validation('source must be modrinth or hangar (hangar only for plugins)')
      if (typeof body.project !== 'string' || !NAME_RE.test(body.project)) return validation('project: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
      const version = body.version ?? null
      if (version !== null && (typeof version !== 'string' || !NAME_RE.test(version))) return validation('version: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const blocked = jobBlocked(c)
      if (blocked) return blocked
      const plan = buildPlan(c, source, body.project, version)
      if (plan instanceof Response) return plan
      if (plan.resolution.incompatible.length > 0) return apiError(409, 'plugin_incompatible', 'a required dependency has no compatible version')
      if (plan.items.length === 0) return apiError(409, 'conflict', 'the project is already installed')
      const changes: MockPluginChange[] = []
      for (const item of plan.items) {
        const old = installedJar(c.inv, item.project.source, item.project.id)
        const add = newJar(c.server.id, item.project, item.version, old ? old.enabled : true)
        const taken = c.inv.jars.find((j) => j !== old && baseName(j.file) === baseName(add.file))
        if (taken) return apiError(409, 'file_exists', `${add.file} belongs to another jar`)
        changes.push({
          remove: old?.file ?? null,
          add,
          action: 'plugin.install',
          details: { source: item.project.source, project: item.project.id, version: item.version.id, file: add.file },
        })
      }
      const steps = changes.map((ch, i) => step(plan.items[i].project.title, ch.add.file))
      return HttpResponse.json(startJob(c, 'install', steps, changes), { status: 202 })
    }),
  ),
  op(
    'getPluginUpdates',
    http.get(`${API}/servers/:id/plugins/updates`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const refresh = new URL(request.url).searchParams.get('refresh')
      if (refresh === 'true' || refresh === '1' || c.inv.checkedAt === 0) c.inv.checkedAt = Date.now()
      const updates: PluginUpdate[] = availableUpdates(c.inv).map((u) => ({
        file: u.jar.file,
        from: { id: u.jar.version?.id ?? '', name: u.jar.version?.name ?? '', channel: u.jar.version?.channel ?? 'release' },
        to: { id: u.to.id, name: u.to.name, channel: u.to.channel },
      }))
      return HttpResponse.json({ updates, checked_at: c.inv.checkedAt })
    }),
  ),
  op(
    'updatePlugins',
    http.post(`${API}/servers/:id/plugins/update`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<UpdatePluginsRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['files']) || !Array.isArray(body.files) || body.files.length > 500 || !body.files.every(validJarFile)) {
        return validation('files: up to 500 jar file names')
      }
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const blocked = jobBlocked(c)
      if (blocked) return blocked
      const all = availableUpdates(c.inv)
      const missing = body.files.find((f) => !all.some((u) => u.jar.file === f))
      if (missing !== undefined) return validation(`no update available for ${missing}`)
      const targets = body.files.length > 0 ? body.files : all.map((u) => u.jar.file)
      if (targets.length === 0) return validation('no updates available')
      const steps: PluginJobStep[] = []
      const changes: Array<MockPluginChange | null> = []
      for (const file of targets) {
        const jar = c.inv.jars.find((j) => j.file === file)
        const u = all.find((x) => x.jar.file === file)
        if (!jar || !u) {
          steps.push({ ...step(jar ? jarTitle(jar) : file, file), state: 'skipped' })
          changes.push(null)
          continue
        }
        const add = newJar(c.server.id, u.project, u.to, jar.enabled)
        if (c.inv.jars.some((j) => j !== jar && baseName(j.file) === baseName(add.file))) {
          steps.push({ ...step(u.project.title, add.file), state: 'failed', error_code: 'file_exists', error: `${add.file} belongs to another jar` })
          changes.push(null)
          continue
        }
        steps.push(step(u.project.title, add.file))
        changes.push({ remove: jar.file, add, action: 'plugin.update', details: { file: add.file, from: jar.version?.name ?? '', to: u.to.name } })
      }
      return HttpResponse.json(startJob(c, 'update', steps, changes), { status: 202 })
    }),
  ),
  op(
    'togglePlugin',
    http.post(`${API}/servers/:id/plugins/toggle`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<TogglePluginRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['file', 'enabled']) || !validJarFile(body.file) || typeof body.enabled !== 'boolean') {
        return validation('file must be a jar name, enabled a boolean')
      }
      const jar = c.inv.jars.find((j) => j.file === body.file)
      if (!jar) return apiError(404, 'not_found', 'jar not found')
      if (jobNow(c.server.id)?.state === 'running') return apiError(409, 'conflict', 'a plugin job of this server is running')
      if (jar.enabled === body.enabled) return noContent()
      const target = body.enabled ? jar.file.replace(/\.disabled$/i, '') : `${jar.file}.disabled`
      if (c.inv.jars.some((j) => j !== jar && j.file.toLowerCase() === target.toLowerCase())) return apiError(409, 'file_exists', `${target} exists`)
      jar.file = target
      jar.enabled = body.enabled
      c.inv.changedAt = Date.now()
      recordAudit(c.g.user, 'plugin.toggle', `server:${c.server.id}`, 'success', { file: body.file, enabled: body.enabled })
      return noContent()
    }),
  ),
  op(
    'deletePlugin',
    http.post(`${API}/servers/:id/plugins/delete`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<PluginFileRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['file']) || !validJarFile(body.file)) return validation('file must be a jar name')
      const jar = c.inv.jars.find((j) => j.file === body.file)
      if (!jar) return apiError(404, 'not_found', 'jar not found')
      if (jobNow(c.server.id)?.state === 'running') return apiError(409, 'conflict', 'a plugin job of this server is running')
      c.inv.jars = c.inv.jars.filter((j) => j !== jar)
      c.inv.changedAt = Date.now()
      recordAudit(c.g.user, 'plugin.remove', `server:${c.server.id}`, 'success', { file: body.file })
      return noContent()
    }),
  ),
  op(
    'getPluginJob',
    http.get(`${API}/servers/:id/plugins/jobs`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      return HttpResponse.json({ job: jobNow(c.server.id) })
    }),
  ),
  op(
    'getPluginIcon',
    http.get(`${API}/servers/:id/plugins/icon`, ({ request, params }) => {
      // Like the panel: permission and server, but no plugins_unsupported check.
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.plugins')) return apiError(403, 'forbidden', 'missing permission server.plugins')
      if (!db.servers.find((s) => s.id === id)?.instance) return apiError(404, 'not_found', 'server not found')
      const raw = new URL(request.url).searchParams.get('url') ?? ''
      if (raw.length > 512 || !ICON_PREFIXES.some((p) => raw.startsWith(p))) return validation('url must point to cdn.modrinth.com or hangarcdn.papermc.io')
      return new HttpResponse(ICON_PNG.slice(), {
        headers: { 'Content-Type': 'image/png', 'Cache-Control': 'private, max-age=86400', 'X-Content-Type-Options': 'nosniff' },
      })
    }),
  ),
]
```

`web/src/mocks/handlers.ts`: `import { pluginsOperations } from './handlers/plugins'` (nach dem `meOperations`-Import) und `...pluginsOperations,` nach `...filesOperations,`.

Das Verhalten im Überblick (bindend für Plan 04/Playwright):
- `GET …/updates` liefert auf survival auch ohne `refresh` das LuckPerms-Update auf `5.5.72`. `refresh=true` und `refresh=1` sind gleichwertig. Nach dem Update-Job verschwindet das Update, und das Inventar zeigt `version.name` `5.5.72`.
- Titel im Inventar: `LuckPerms`, `ViaVersion`; nach der Installation `ViaBackwards` bzw. `Chunky`/`ChunkyBorder`.
- `toggle` benennt nur um, `meta` bleibt.
- `project` akzeptiert Slug und Projekt-ID.
- Versionswechsel: Ist das angefragte Projekt installiert und kommt die Anfrage ohne `version` oder mit derselben `version`, steht es in `already` (ohne Abhängigkeiten aufzulösen) und `install` antwortet `409 conflict`. Eine andere `version` landet in `install` und ersetzt das alte Jar, aktiv bzw. deaktiviert wie zuvor.
- Jobs: Jeder Schritt läuft `pending` → `downloading` → `done`, ein Job endet nach `PLUGIN_JOB_MS` = 4 s mit `done`. Danach steht auf einem laufenden Server `restart_needed: true`, ebenso nach `toggle`/`delete`.
- `install`/`update` antworten während eines Jobs desselben Servers `409 conflict`, `toggle`/`delete` ebenso, und bei vier laufenden Jobs `429 rate_limited`. Das Rate-Limit von 60/min bildet der Mock nicht nach.
- `game_version` ist die Version der Instanz. `LATEST` wird über `db.catalogs` aufgelöst, fehlt der Katalog, antworten Suche, Versionen, Auflösen, Installieren und Updates `502 catalog_unavailable`.

- [ ] **Step 10: `make check`, Commit, Push, CI**

```bash
git add -N internal/plugins web/src/mocks/handlers/plugins.ts
make check
git add api/openapi.yaml web/src/api internal/plugins cmd/panel/app.go cmd/panel/integration_test.go web/src/mocks
git commit -m "feat(plugins): panel contract for plugins and mods, stub routes and mocks

The eleven plugin operations of sub-project 3c with their schemas, TypeScript
types, the plugins package with stub routes (502 until phase B2) and the upstream
skeleton, both wired into the panel with one shared version catalog, and MSW
mocks with a catalog, inventories, jobs and icons.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün, insbesondere:
- `TestRoutesMatchOpenAPI`, `TestErrorCodesMatchOpenAPI`, `TestFilesErrorsMatchOpenAPI` und `TestRouteCoverage` (87).
- In `internal/plugins`: `TestRouteContract`, `TestStubsAnswerNotImplemented`, `TestRoutesNeedServerPlugins`, `TestRunReturnsWhenCancelled`.
- In `internal/plugins/upstream`: `TestNewDefaults`, `TestDefaultHostAllowed`, `TestHostAllowedOverride`.
- Vitest `handlers.test.ts` (87 Operationen, die sechs Plugin-Fälle) und `i18n.test.ts`.
- OpenAPI-Lint (`redocly … --extends=minimal`), ESLint, Typecheck und der Schritt „Generated files are committed“.

Danach meldet der Teammate dem Lead den PR („ready for review“). Mergen darf nur der Nutzer.

## Self-Review (dieser Datei)

- **V1** → Aufgabe 1: Typen, Konstanten, `ErrHashMismatch`, `Jars` (2-min-Timeout, `dir`-Prüfung ohne Aufruf), `UploadChecked` (Query `sha256`/`sha512`, ungültige Prüfsumme ohne Aufruf, `hash_mismatch` → `ErrHashMismatch`). Dazu beide Fakes (echte Prüfsummen, flacher Baum) und `noFiles` (Abweichung 1). Die Helfer aus Abweichung 2 nutzen B1 (Query-Parsing, Jar-Namen) und B2 (Dateiparameter).
- **V2** → Aufgabe 2: Tabelle wortgleich zu Spec §6.1, Queries wortgleich, `PluginInstall`-Felder mit sqlc 1.31.1 geprüft. Dazu Codes, Aktionen, Enum und Frontend-Codes (Abweichung 3).
- **V3** → Aufgabe 3:
  - Elf Operationen mit den Antworten aus der Tabelle, jede Fehlerbeschreibung nennt ihre Codes (Grundlage für einen `TestPluginsErrorsMatchOpenAPI` in B2).
  - Request-Bodies mit `additionalProperties: false`, nullable als `type: [X, 'null']` bzw. `anyOf` mit `null` wie im übrigen Vertrag.
  - Der Ausschnitt ist mit `openapi-typescript` 7.13.0 gegen eine Kopie von `api/openapi.yaml` generiert (87 Operationen). Die Mock-Dateien sind gegen die erzeugten Typen mit `tsc` geprüft.
- **V4** → Aufgabe 3: `Catalog`, `Deps` und `NewHandler`/`Routes`/`Run` exakt nach V4; `upstream` mit den V4-Deklarationen (Abweichung 4); `appDeps.Upstream` (Abweichung 5); gemeinsamer Katalog; `pluginsH.Run` in der WaitGroup.
- **V5** → Aufgabe 3: Inventare, Katalog, Suche (Teilstring, `offset`, 20 pro Seite, `installed`), Jobs über ~4 s mit `409 conflict`, `toggle`/`delete`, PNG-Icon, Rechte über `serverPermissions`. Ergänzungen in Abweichung 7.
- **Review Focus:**
  - 1 (feindliche Upstream-Antworten): `DefaultHostAllowed` mit Tests gegen `http`, fremde Hosts, Userinfo, Ports und Host-Suffixe.
  - 2 (Namenskollisionen): `IsJarName` (Groß-/Kleinschreibung) und die Fake-Liste (nur oberste Ebene); im Mock `file_exists` bei `toggle` auf einen vorhandenen Zielnamen (case-insensitiv) und bei `install`/`update`.
  - 3 (gleichzeitige Jobs): ein Job pro Server im Mock und im Vertrag (`409 conflict`).
  - 4 und 5 sind Sache von B1/B2. Phase A liefert dafür `ErrUnavailable`, `MaxJars`, `MaxJarHashBytes`, `JarsTimeout` und `meta: null`.
- **Vorab geprüft (Planung, in Scratch-Kopien, nicht im Repo):**
  - Der Go-Code aller drei Aufgaben ist in eine Kopie des Repos eingespielt. Nach `gofmt -w internal/httpx/errors.go` meldet `gofmt -l` nichts, `sqlc generate` erzeugt exakt die Typen aus Aufgabe 2, und `go vet ./...` sowie `go build ./...` laufen für macOS und für Linux (inkl. `nftintegration,dockerintegration`) durch.
  - Die Mock-Dateien und `types.ts` bestehen `tsc -p tsconfig.app.json` gegen das generierte `schema.d.ts`.
  - Die Tests selbst laufen erst in der CI.
- Phase A berührt keine Dateien von B1 (`internal/files` außer `filesapi`), B3 (`web/src/features/**`, `web/e2e/**`; aus `web/src/i18n/**` nur `errors.*`) oder C (`scripts`, `docs`, `Makefile`, Workflows).
