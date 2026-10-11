# mc-panel Teilprojekt 3b – Phase A: Verträge (Aufgaben 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die gemeinsamen Grundlagen für B1/B2/B3 auf `main` bringen: Der agent legt `instances/` so an, dass der Dienst `mcpanel-files` (Benutzer `mcpanel-mc`) hineinkommt; das Paket `filesapi` (Pfadregeln, Draht-Typen, Fehler, Client) und die Konfiguration (`files.toml`, `files_socket`) stehen; der panel-Vertrag (OpenAPI, Fehlercodes, Audit-Aktionen, Stub-Handler, Body-Limit-Ausnahme, Verdrahtung, MSW-Mocks) ist so weit, dass Dienst, panel und Frontend parallel weiterbauen können.

**Architecture:** Drei Aufgaben auf einem Branch `feat/files-contracts` (Teammate „contracts“, sequenziell). Aufgabe 1 ändert nur `internal/agent/instances/datadir.go`. Aufgabe 2 fügt `internal/files/filesapi` und `internal/config/files.go` hinzu. Aufgabe 3 fügt den OpenAPI-Vertrag, Fehlercodes, Audit-Konstanten, `httpx.BodyLimitExcept`, das Paket `internal/serverfiles` mit Stub-Handlern und die MSW-Mocks hinzu; alle neuen Routen antworten bis Phase B2 `503 files_unavailable`.

**Tech Stack:** Go (Standardbibliothek, chi, BurntSushi/toml), OpenAPI 3.1 + `openapi-typescript`, MSW 2, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-11-files-00-overview.md` (V1–V5; „Ausführung“ und „Global Constraints“ gelten hier vollständig).

## Ausführung dieser Phase

- Worktree: `git worktree add ../mc-panel-wt/feat-files-contracts -b feat/files-contracts origin/main`. Ein Draft-PR für alle drei Aufgaben, ein Commit pro Aufgabe (Fix-Commits erlaubt).
- Lokal nur `make check` (neue Dateien vorher mit `git add -N` vormerken, sonst prüft gofmt sie nicht) und `make generate` in Aufgabe 3. „Run: go test …“ / „npx vitest …“ heißt: nicht lokal, sondern Push und `gh pr checks <pr> --watch`; Ergebnis in den CI-Logs prüfen.
- Eine Aufgabe ist fertig, wenn die CI ihres Commits grün ist. Danach mergt der Nutzer den PR; erst dann starten B1, B2, B3.

## Abweichungen von der Übersicht (bewusst, vom Lead zu bestätigen)

1. *(entfällt – inzwischen Vertrag: Dev-`instances_dir` = `<root>/.dev/mc` wie beim dev-agent.)*
2. **`filesapi.MaxJSONBytes` = 32 MiB** statt 6 MiB (V1): Ein 5-MiB-Editor-Inhalt wird beim JSON-Kodieren größer (Steuerzeichen werden zu `\u00XX`, sechs Bytes pro Zeichen). Der Client kodiert ohne HTML-Escaping (`SetEscapeHTML(false)`), trotzdem braucht eine gültige Datei mit vielen Tabs/Steuerzeichen mehr als 6 MiB. 32 MiB > 6 × 5 MiB deckt den schlimmsten Fall.
3. **`Upload` ohne Header-Timeout** (V1 „höchstens 10 s bis zu den Antwort-Headern“): Beim Upload kommen die Antwort-Header erst nach dem vollständigen Body (bis 4 GiB). Ein Header-Timeout würde jeden größeren Upload abbrechen. `Upload` endet nur über `ctx` (Abbruch des Browsers) oder die Fristen pro Vorgang, die das panel (B2) an der Verbindung setzt.
4. **`httpx.BodyLimitExcept`** (neu, nicht in der Übersicht): Das globale Body-Limit von 1 MiB (`httpx.BodyLimit` in `cmd/panel/app.go`) wickelt `r.Body` in einen `MaxBytesReader`, den ein Handler nicht mehr aufheben kann. B2 darf `internal/httpx` nicht ändern, deshalb liefert Phase A die Ausnahme: `BodyLimitExcept(n, skip)` plus `serverfiles.UnlimitedBody` (nur `POST /api/servers/{id}/files/upload`).
5. **`CheckInstancesDir` bleibt unverändert:** Die Funktion prüft nur `instances_dir` und dessen Eltern (kein Schreibrecht für Gruppe/andere) – `instances/` darunter prüft sie nicht. Gruppen-Lesen war dort schon erlaubt. Aufgabe 1 ergänzt nur einen Test, der genau das festhält.

---

### Task 1: agent – `instances/` für den Dateidienst öffnen

**Files:**
- Modify: `internal/agent/instances/datadir.go` (`ensureLayout`, neue Hilfsfunktion `ensureDir`, Kommentare)
- Modify: `internal/agent/instances/players.go:75` (Kommentar)
- Modify: `internal/agent/instances/manager_test.go:156` (Erwartung `instances/` 0750)
- Modify: `internal/agent/instances/manager_hardening_test.go:67-95` (`TestPrivateDirectoriesAreEnforced` → zwei Tests)
- Create: `internal/agent/instances/datadir_test.go` (Gruppe von `instances/`, `CheckInstancesDir` mit 0750)

**Interfaces:**
- Consumes: `instances.Manager` mit `m.host.GID` (GID von `mc_user`, Tests: `os.Getgid()`).
- Produces: `<instances_dir>/instances` = Besitzer agent-Prozess (root), Gruppe = GID von `mc_user`, Modus `0750`; `<instances_dir>/deleted` unverändert `0700` mit Gruppe des agent-Prozesses. Bei jedem `ensure`/`remove` erzwungen (auch wenn vorher lockerer oder strenger).

- [ ] **Step 1: Tests anpassen und ergänzen (rot)**

In `internal/agent/instances/manager_test.go` die Erwartung an `instances/` ändern (Zeile ~156):

```go
	if fi, err := os.Stat(filepath.Join(e.dir, "instances")); err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("instances dir = %v, %v; want 0750", fi, err)
	}
```

In `internal/agent/instances/manager_hardening_test.go` `TestPrivateDirectoriesAreEnforced` ersetzen durch:

```go
// TestLayoutModesAreEnforced: instances/ is 0750 (group = mc_user, so mcpanel-files can enter it)
// and deleted/ stays 0700 on every ensure/remove, also when they existed with other modes.
func TestLayoutModesAreEnforced(t *testing.T) {
	e := newEnv(t)
	for _, d := range []string{"instances", "deleted"} {
		p := filepath.Join(e.dir, d)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if got := perm(t, filepath.Join(e.dir, "instances")); got != 0o750 {
		t.Fatalf("instances/ = %v after ensure, want 0750", got)
	}
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if got := perm(t, filepath.Join(e.dir, "deleted")); got != 0o700 {
		t.Fatalf("deleted/ = %v after remove, want 0700", got)
	}
	for _, mode := range []os.FileMode{0o777, 0o700} {
		if err := os.Chmod(filepath.Join(e.dir, "instances"), mode); err != nil {
			t.Fatal(err)
		}
		e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
		if got := perm(t, filepath.Join(e.dir, "instances")); got != 0o750 {
			t.Fatalf("instances/ = %v after remove (was %v), want 0750", got, mode)
		}
	}
}
```

Neue Datei `internal/agent/instances/datadir_test.go`:

```go
package instances_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

// TestInstancesDirBelongsToMCGroup: instances/ carries the gid of the container user (the files
// service runs as that user and must traverse it); deleted/ keeps the agent's own group.
func TestInstancesDirBelongsToMCGroup(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	fi, err := os.Stat(filepath.Join(e.dir, "instances"))
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no stat_t on this platform")
	}
	if int(st.Gid) != os.Getgid() || int(st.Uid) != os.Geteuid() {
		t.Fatalf("instances/ owner = %d:%d, want %d:%d", st.Uid, st.Gid, os.Geteuid(), os.Getgid())
	}
	if fi.Mode().Perm()&0o027 != 0o020&0o027 && fi.Mode().Perm() != 0o750 {
		t.Fatalf("instances/ mode = %v, want 0750", fi.Mode().Perm())
	}
}

// TestCheckInstancesDirAcceptsGroupReadable: a group-readable (but not group-writable) tree is
// fine for the strict start check; only write bits for group/others are refused.
func TestCheckInstancesDirAcceptsGroupReadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "srv")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := instances.CheckInstancesDir(dir, false, 0); err != nil {
		t.Fatalf("non-strict: %v", err)
	}
	if err := os.Chmod(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	// Strict mode also walks the parents (t.TempDir lives below a world-writable sticky /tmp or
	// a user-owned folder), so only the last component's group-write bit is asserted here via the
	// error text.
	err := instances.CheckInstancesDir(dir, true, uint32(os.Getuid())) //nolint:gosec // G115: test uid is non-negative
	if err == nil {
		t.Fatal("strict check accepted a group-writable instances_dir")
	}
}
```

Hinweis zur zweiten Bedingung in `TestInstancesDirBelongsToMCGroup`: sie ist absichtlich nur `!= 0o750` – die erste Teilbedingung ist immer falsch und kann beim Implementieren weggelassen werden; gemeint ist schlicht:

```go
	if fi.Mode().Perm() != 0o750 {
		t.Fatalf("instances/ mode = %v, want 0750", fi.Mode().Perm())
	}
```

(So in den Test übernehmen.)

- [ ] **Step 2: `make check`**

Run: `git add -N internal/agent/instances/datadir_test.go && make check`
Expected: kompiliert; die Tests sind rot (erst in der CI sichtbar: `instances/ = -rwx------ after ensure, want 0750`).

- [ ] **Step 3: Implementieren**

In `internal/agent/instances/datadir.go` `ensureLayout` und `ensurePrivateDir` ersetzen:

```go
// ensureLayout makes sure <instances_dir> exists (0755), that instances/ is root:<mc_user gid>
// 0750 and deleted/ root:root 0700 (in production). The panel user must not read world data;
// the files service (running as mc_user, spec 3b §3.3) may enter instances/ but never write
// into it; no container user may swap either directory.
func (m *Manager) ensureLayout() error {
	if err := os.MkdirAll(m.host.InstancesDir, 0o755); err != nil { //nolint:gosec // G301: spec §3.3, root:root 0755; contains no secrets itself
		return err
	}
	if err := ensureDir(filepath.Join(m.host.InstancesDir, "instances"), m.host.GID, 0o750); err != nil {
		return err
	}
	return ensureDir(DeletedDir(m.host.InstancesDir), os.Getegid(), 0o700)
}

// ensureDir creates dir if needed and enforces owner = agent process (root in production),
// group gid and mode perm through a descriptor opened with O_NOFOLLOW: a symlink in its place
// is refused, never followed.
func ensureDir(dir string, gid int, perm os.FileMode) error {
	if err := os.Mkdir(dir, perm); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	f, err := openDirNoFollow(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chown(os.Geteuid(), gid); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}
	return f.Chmod(perm)
}
```

`ensurePrivateDir` hat keine weiteren Aufrufer (`grep -rn ensurePrivateDir internal` prüfen) und entfällt.

In `internal/agent/instances/players.go:75` den Kommentar anpassen:

```go
	// instances/ is not writable by the container user (root:<mc gid> 0750), so <id> cannot be
	// swapped after the Lstat; check anyway.
```

- [ ] **Step 4: `make check`, Commit, Push, CI**

```bash
make check
git add internal/agent/instances
git commit -m "feat(agent): let the files service enter instances/ (root:<mc gid> 0750)

The files service of sub-project 3b runs as the container user and must traverse
instances/ to reach a server's data; deleted/ stays root-only.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/files-contracts
gh pr create --draft --title "feat: TP3b contracts (instances dir, filesapi, panel contract)" --body "Phase A of docs/superpowers/plans/2026-10-11-files-00-overview.md (Tasks 1–3).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün (Go-Tests: `TestLayoutModesAreEnforced`, `TestInstancesDirBelongsToMCGroup`, `TestCheckInstancesDirAcceptsGroupReadable` PASS; Instance-Abnahme unverändert grün).

---

### Task 2: `filesapi` und Konfiguration

**Files:**
- Create: `internal/files/filesapi/doc.go`, `path.go`, `types.go`, `errors.go`, `client.go`
- Create: `internal/files/filesapi/path_test.go`, `errors_test.go`, `client_test.go`
- Create: `internal/config/files.go`, `internal/config/files_test.go`
- Modify: `internal/config/config.go` (`Panel.FilesSocket`, Defaults, Validierung)
- Modify: `internal/config/config_test.go` (`TestDevPanel`: `FilesSocket`)

**Interfaces:**
- Consumes: –
- Produces (exakt V1 mit den Abweichungen 2 und 3 oben):
  - Konstanten `PathHealth … PathJobs`, `HeaderKind`, `MaxPathBytes`, `MaxComponentBytes`, `MaxJSONBytes` (32 MiB), `MaxSmallJSONBytes`, `Code*`.
  - Sentinels `ErrNotFound, ErrExists, ErrModified, ErrNotRegular, ErrNotText, ErrTooLarge, ErrInsufficientStorage, ErrBusy, ErrInvalid, ErrUnavailable`.
  - `func CleanPath(p string) (string, error)`, `func IsHidden(clean string) bool`, `func Join(dir, name string) string`.
  - Typen `EntryType`, `Entry`, `PathRequest`, `ListResponse`, `ReadResponse`, `WriteRequest`, `WriteResponse`, `RenameRequest`, `UploadResponse`, `ExtractRequest`, `JobState` (+ Konstanten `JobRunning/JobDone/JobFailed`), `Job`, `JobsResponse`, `ErrorResponse`, `HealthResponse`, `Download`.
  - Für den Dienst (B1): `func ErrorFromCode(code string) error` (unbekannt → `nil`) und `func CodeOf(err error) (code string, status int)` (erstes passendes Sentinel; sonst `"", 500`).
  - `type StatusError struct{ Status int; Code, Message string }` mit `Is` (Code → Sentinel; unbekannter Code oder 5xx ohne Code → `ErrUnavailable`).
  - `type Client interface{…}` (V1; `Extract` prüft das Archiv beim Dienst synchron – Fehler `ErrInvalid`, `ErrNotRegular`, `ErrExists` (Ziel nicht leer), `ErrTooLarge`, `ErrInsufficientStorage`, `ErrBusy` kommen direkt als Fehler, erst danach `202` mit Job), `type UnixClient`, `func NewClient(socketPath string) *UnixClient`, `var _ Client = (*UnixClient)(nil)`. Timeouts: `CallTimeout = 10*time.Second` (kleine Aufrufe), `HealthTimeout = 2*time.Second`, `DownloadHeaderTimeout = 10*time.Second`; `Upload` ohne Timeout (nur `ctx`).
  - `config.Files` (inkl. `SocketMode string` `toml:"socket_mode"`, Default `"0660"`, oktal 0–0777), `(Files).SocketFileMode() os.FileMode`, `config.FilesLimits`, `config.DefaultFiles()` (Socket `/run/mc-panel-files/files.sock`), `config.DevFiles(root)` (Socket `<root>/.dev/files.sock`, `instances_dir` `<root>/.dev/mc`), `config.LoadFiles(path)` (fehlende `[limits]`-Schlüssel behalten ihre Defaults), `(Files).Validate()`; `config.Panel.FilesSocket` (`toml:"files_socket"`, Default `/run/mc-panel-files/files.sock`).

- [ ] **Step 1: Pfadtests schreiben (rot)**

`internal/files/filesapi/path_test.go`:

```go
package filesapi_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestCleanPath(t *testing.T) {
	ok := map[string]string{
		"":                        "",
		".":                       "",
		"server.properties":       "server.properties",
		"plugins/Essentials":      "plugins/Essentials",
		"plugins/Essentials/":     "plugins/Essentials", // one trailing slash (UI folder paths) is fine
		"world/region/r.0.0.mca":  "world/region/r.0.0.mca",
		".hidden":                 ".hidden",
		"Ünïcödé/日本語.yml":         "Ünïcödé/日本語.yml",
		"a..b":                    "a..b",
		strings.Repeat("a", 255): strings.Repeat("a", 255),
	}
	for in, want := range ok {
		got, err := filesapi.CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"/etc/passwd", "../x", "a/../b", "a/..", "..", "a//b", "a/./b", "./a", "a\\b", "a/b//",
		"a\x00b", "a\nb", "a\tb", "a\x7fb", "\xff\xfe", strings.Repeat("a", 256),
		strings.Repeat("a/", 2049) + "a", // > 4096 bytes
	}
	for _, in := range bad {
		if got, err := filesapi.CleanPath(in); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("CleanPath(%q) = %q, %v; want ErrInvalid", in, got, err)
		}
	}
}

func TestIsHidden(t *testing.T) {
	for in, want := range map[string]bool{
		".rcon-cli.env":            true,
		".rcon-cli.yaml":           true,
		"plugins/.rcon-cli.env":    false, // only the top-level files hold the RCON password
		".mcpanel-tmp-abc":         true,
		"world/.mcpanel-tmp-1/x":   true,
		"plugins/.mcpanel-upload":  true,
		".rcon-cli.envx":           false,
		"server.properties":        false,
		"":                         false,
		".mcpanelx":                false,
	} {
		if got := filesapi.IsHidden(in); got != want {
			t.Errorf("IsHidden(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestJoin(t *testing.T) {
	if got := filesapi.Join("", "a"); got != "a" {
		t.Fatalf("Join(\"\", a) = %q", got)
	}
	if got := filesapi.Join("plugins", "x.jar"); got != "plugins/x.jar" {
		t.Fatalf("Join = %q", got)
	}
}
```

- [ ] **Step 2: Fehlertests schreiben (rot)**

`internal/files/filesapi/errors_test.go`:

```go
package filesapi_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestCodesRoundTrip(t *testing.T) {
	for code, status := range map[string]int{
		filesapi.CodeNotFound:            http.StatusNotFound,
		filesapi.CodeExists:              http.StatusConflict,
		filesapi.CodeModified:            http.StatusConflict,
		filesapi.CodeNotRegular:          http.StatusBadRequest,
		filesapi.CodeNotText:             http.StatusUnsupportedMediaType,
		filesapi.CodeTooLarge:            http.StatusRequestEntityTooLarge,
		filesapi.CodeInsufficientStorage: http.StatusInsufficientStorage,
		filesapi.CodeBusy:                http.StatusTooManyRequests,
		filesapi.CodeInvalid:             http.StatusBadRequest,
	} {
		err := filesapi.ErrorFromCode(code)
		if err == nil {
			t.Fatalf("ErrorFromCode(%q) = nil", code)
		}
		gotCode, gotStatus := filesapi.CodeOf(fmt.Errorf("wrapped: %w", err))
		if gotCode != code || gotStatus != status {
			t.Errorf("CodeOf(%s) = %q, %d; want %q, %d", code, gotCode, gotStatus, code, status)
		}
	}
	if filesapi.ErrorFromCode("nope") != nil {
		t.Fatal("unknown code must map to nil")
	}
	if code, status := filesapi.CodeOf(errors.New("boom")); code != "" || status != http.StatusInternalServerError {
		t.Fatalf("CodeOf(other) = %q, %d", code, status)
	}
}

func TestStatusErrorIs(t *testing.T) {
	cases := []struct {
		err  *filesapi.StatusError
		want error
	}{
		{&filesapi.StatusError{Status: 409, Code: "exists"}, filesapi.ErrExists},
		{&filesapi.StatusError{Status: 409, Code: "modified"}, filesapi.ErrModified},
		{&filesapi.StatusError{Status: 507, Code: "insufficient_storage"}, filesapi.ErrInsufficientStorage},
		{&filesapi.StatusError{Status: 500}, filesapi.ErrUnavailable},
		{&filesapi.StatusError{Status: 418, Code: "teapot"}, filesapi.ErrUnavailable},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%+v is not %v", c.err, c.want)
		}
	}
	if errors.Is(&filesapi.StatusError{Status: 507, Code: "insufficient_storage"}, filesapi.ErrUnavailable) {
		t.Fatal("a known code must not also match ErrUnavailable")
	}
}
```

- [ ] **Step 3: Client-Tests schreiben (rot)**

`internal/files/filesapi/client_test.go` – ein `httptest.Server` auf einem Unix-Socket spielt den Dienst:

```go
package filesapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// serve starts h on a short unix socket path (macOS limits socket paths to 104 bytes).
func serve(t *testing.T, h http.Handler) *filesapi.UnixClient {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "f.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return filesapi.NewClient(sock)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestClientList(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req filesapi.PathRequest
		if r.Method != http.MethodPost || r.URL.Path != filesapi.PathList || r.Header.Get("Content-Type") != "application/json" ||
			json.NewDecoder(r.Body).Decode(&req) != nil || req.ID != 7 || req.Path != "plugins" {
			http.Error(w, "bad request", 400)
			return
		}
		writeJSON(w, 200, filesapi.ListResponse{Entries: []filesapi.Entry{{Name: "x.jar", Type: filesapi.TypeFile, Size: 3, MTime: 5}}})
	}))
	got, err := c.List(context.Background(), 7, "plugins/")
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Name != "x.jar" {
		t.Fatalf("List = %+v, %v", got, err)
	}
}

func TestClientRejectsBadPathWithoutRequest(t *testing.T) {
	called := false
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	if _, err := c.Read(context.Background(), 1, "../etc/shadow"); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if err := c.Rename(context.Background(), filesapi.RenameRequest{ID: 1, From: "a", To: "/b"}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("rename err = %v, want ErrInvalid", err)
	}
	if _, err := c.List(context.Background(), 0, ""); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: err = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("an invalid request reached the service")
	}
}

func TestClientErrorMapping(t *testing.T) {
	for code, want := range map[string]error{
		"exists": filesapi.ErrExists, "modified": filesapi.ErrModified, "not_text": filesapi.ErrNotText,
		"insufficient_storage": filesapi.ErrInsufficientStorage, "busy": filesapi.ErrBusy, "not_found": filesapi.ErrNotFound,
	} {
		c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			status, _ := map[string]int{"exists": 409, "modified": 409, "not_text": 415, "insufficient_storage": 507, "busy": 429, "not_found": 404}[code]
			writeJSON(w, status, filesapi.ErrorResponse{Error: "x", Code: code})
		}))
		mt := int64(1)
		_, err := c.Write(context.Background(), filesapi.WriteRequest{ID: 1, Path: "a.yml", Content: "x", ExpectedMTime: &mt})
		if !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", code, err, want)
		}
	}
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "boom", 500) }))
	if err := c.Mkdir(context.Background(), 1, "x"); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("500: err = %v, want ErrUnavailable", err)
	}
	gone := filesapi.NewClient(filepath.Join(t.TempDir(), "missing.sock"))
	if err := gone.Health(context.Background()); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("no socket: err = %v, want ErrUnavailable", err)
	}
}

func TestClientWriteSendsNullForNewFiles(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Contains(raw, []byte(`"expected_mtime":null`)) || !bytes.Contains(raw, []byte(`"content":"a<b"`)) {
			http.Error(w, string(raw), 400)
			return
		}
		writeJSON(w, 200, filesapi.WriteResponse{MTime: 9, Size: 3})
	}))
	got, err := c.Write(context.Background(), filesapi.WriteRequest{ID: 1, Path: "new.yml", Content: "a<b"})
	if err != nil || got.MTime != 9 {
		t.Fatalf("Write = %+v, %v", got, err)
	}
}

func TestClientUploadStreams(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodPut || r.URL.Path != filesapi.PathUpload || q.Get("id") != "3" ||
			q.Get("path") != "plugins/x.jar" || q.Get("overwrite") != "1" || r.ContentLength != 5 {
			http.Error(w, "bad request "+r.URL.String(), 400)
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		writeJSON(w, 201, filesapi.UploadResponse{Size: n})
	}))
	got, err := c.Upload(context.Background(), 3, "plugins/x.jar", true, 5, strings.NewReader("hello"))
	if err != nil || got.Size != 5 {
		t.Fatalf("Upload = %+v, %v", got, err)
	}
	if _, err := c.Upload(context.Background(), 3, "x", false, -1, strings.NewReader("")); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("negative size: err = %v, want ErrInvalid", err)
	}
}

func TestClientDownload(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("path") {
		case "world":
			w.Header().Set(filesapi.HeaderKind, "dir")
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK"))
		default:
			w.Header().Set(filesapi.HeaderKind, "file")
			w.Header().Set("Content-Length", "4")
			_, _ = w.Write([]byte("data"))
		}
	}))
	d, err := c.Download(context.Background(), 1, "logs/latest.log")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if string(body) != "data" || d.Size != 4 || d.IsDir || d.Name != "latest.log" {
		t.Fatalf("file download = %+v %q", d, body)
	}
	d, err = c.Download(context.Background(), 1, "world")
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Body.Close()
	if !d.IsDir || d.Size != -1 || d.Name != "world" {
		t.Fatalf("dir download = %+v", d)
	}
	d, err = c.Download(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Body.Close()
	if d.Name != "" {
		t.Fatalf("root name = %q, want empty", d.Name)
	}
}

func TestClientJob(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == filesapi.PathJobs {
			if r.URL.Query().Get("id") == "2" {
				writeJSON(w, 200, filesapi.JobsResponse{})
				return
			}
			writeJSON(w, 200, filesapi.JobsResponse{Job: &filesapi.Job{ID: "j1", State: filesapi.JobRunning}})
			return
		}
		writeJSON(w, 202, filesapi.Job{ID: "j1", State: filesapi.JobRunning, Path: "w.zip", Dest: "w"})
	}))
	j, err := c.Extract(context.Background(), filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"})
	if err != nil || j.ID != "j1" {
		t.Fatalf("Extract = %+v, %v", j, err)
	}
	if got, err := c.Job(context.Background(), 1); err != nil || got == nil || got.State != filesapi.JobRunning {
		t.Fatalf("Job(1) = %+v, %v", got, err)
	}
	if got, err := c.Job(context.Background(), 2); err != nil || got != nil {
		t.Fatalf("Job(2) = %+v, %v; want nil", got, err)
	}
}
```

- [ ] **Step 4: Konfigurationstests schreiben (rot)**

`internal/config/files_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
)

func TestDefaultFiles(t *testing.T) {
	f := config.DefaultFiles()
	if f.Socket != "/run/mc-panel-files/files.sock" || f.SocketMode != "0660" || f.SocketFileMode() != 0o660 ||
		f.InstancesDir != "/srv/mc-panel" || f.PanelUID != -1 || f.Dev {
		t.Fatalf("defaults = %+v", f)
	}
	l := f.Limits
	if l.MaxUploadBytes != 4<<30 || l.MaxExtractBytes != 20<<30 || l.MaxExtractEntries != 100000 ||
		l.ReserveBytes != 2<<30 || l.MaxTextBytes != 5<<20 || l.MaxListEntries != 10000 {
		t.Fatalf("limits = %+v", l)
	}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "panel_uid") {
		t.Fatalf("Validate without panel_uid = %v", err)
	}
	f.PanelUID = 998
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDevFiles(t *testing.T) {
	root := t.TempDir()
	f := config.DevFiles(root)
	if !f.Dev || f.Socket != filepath.Join(root, ".dev", "files.sock") || f.InstancesDir != filepath.Join(root, ".dev", "mc") || f.PanelUID != os.Getuid() {
		t.Fatalf("dev = %+v", f)
	}
	// Same instance directories as the dev agent (MCPANEL_DEV_DOCKER=1 shares them).
	if a := config.DevAgent(root); a.InstancesDir != f.InstancesDir {
		t.Fatalf("agent %s != files %s", a.InstancesDir, f.InstancesDir)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestLoadFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.toml")
	src := "panel_uid = 998\ninstances_dir = \"/data/mc\"\nsocket_mode = \"0600\"\n[limits]\nmax_upload_bytes = 1048576\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := config.LoadFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.PanelUID != 998 || f.InstancesDir != "/data/mc" || f.SocketFileMode() != 0o600 || f.Limits.MaxUploadBytes != 1<<20 {
		t.Fatalf("loaded = %+v", f)
	}
	// Limits not named in the file keep their defaults.
	want := config.DefaultFiles().Limits
	want.MaxUploadBytes = 1 << 20
	if f.Limits != want {
		t.Fatalf("limits = %+v, want %+v", f.Limits, want)
	}
}

func TestLoadFilesRejects(t *testing.T) {
	for name, src := range map[string]string{
		"unknown key":       "panel_uid = 998\nmystery = 1\n",
		"unknown limit":     "panel_uid = 998\n[limits]\nmax_foo = 1\n",
		"relative dir":      "panel_uid = 998\ninstances_dir = \"srv\"\n",
		"root dir":          "panel_uid = 998\ninstances_dir = \"/\"\n",
		"zero limit":        "panel_uid = 998\n[limits]\nmax_text_bytes = 0\n",
		"negative reserve":  "panel_uid = 998\n[limits]\nreserve_bytes = -1\n",
		"no panel uid":      "instances_dir = \"/srv/mc-panel\"\n",
		"relative socket":   "panel_uid = 998\nsocket = \"files.sock\"\n",
		"text over json":    "panel_uid = 998\n[limits]\nmax_text_bytes = 10485760\n", // > MaxJSONBytes/6
		"mode not octal":    "panel_uid = 998\nsocket_mode = \"660x\"\n",
		"mode too wide":     "panel_uid = 998\nsocket_mode = \"1777\"\n",
	} {
		path := filepath.Join(t.TempDir(), "files.toml")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFiles(path); err == nil {
			t.Errorf("%s: LoadFiles accepted %q", name, src)
		}
	}
}

func TestPanelFilesSocket(t *testing.T) {
	if got := config.DefaultPanel().FilesSocket; got != "/run/mc-panel-files/files.sock" {
		t.Fatalf("default files_socket = %q", got)
	}
	root := t.TempDir()
	if got := config.DevPanel(root).FilesSocket; got != filepath.Join(root, ".dev", "files.sock") {
		t.Fatalf("dev files_socket = %q", got)
	}
	p := config.DefaultPanel()
	p.PublicURL = "https://panel.example.com"
	p.FilesSocket = ""
	if err := p.Validate(); err == nil {
		t.Fatal("empty files_socket accepted")
	}
}
```

Die Grenze „text over json“ (`max_text_bytes` ≤ `MaxJSONBytes/6`) stellt sicher, dass jeder Editor-Inhalt nach JSON-Kodierung in den Request passt (Abweichung 2).

- [ ] **Step 5: `make check` (rot)**

Run: `git add -N internal/files internal/config/files.go internal/config/files_test.go && make check`
Expected: Kompilierfehler `undefined: filesapi.CleanPath`, `config.DefaultFiles` usw.

- [ ] **Step 6: `filesapi` implementieren**

`internal/files/filesapi/doc.go`:

```go
// Package filesapi is the contract between the panel and the files service (mcpanel-files,
// sub-project 3b): path rules shared by both sides, wire types, error codes and the panel's
// client over the service's unix socket.
package filesapi
```

`internal/files/filesapi/path.go`:

```go
package filesapi

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MaxPathBytes      = 4096
	MaxComponentBytes = 255
	hiddenPrefix      = ".mcpanel-"
)

// topHidden are the itzg files holding the RCON password (spec §3.4).
var topHidden = map[string]bool{".rcon-cli.env": true, ".rcon-cli.yaml": true}

// CleanPath validates p per spec §3.4 and returns it normalised: "" for the root, no trailing
// "/". Errors wrap ErrInvalid. Hidden paths are not rejected here (IsHidden decides).
func CleanPath(p string) (string, error) {
	if p == "" || p == "." {
		return "", nil
	}
	if len(p) > MaxPathBytes {
		return "", fmt.Errorf("%w: path longer than %d bytes", ErrInvalid, MaxPathBytes)
	}
	if !utf8.ValidString(p) {
		return "", fmt.Errorf("%w: path is not valid UTF-8", ErrInvalid)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return "", fmt.Errorf("%w: path contains a control character or a backslash", ErrInvalid)
		}
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: path must be relative", ErrInvalid)
	}
	p = strings.TrimSuffix(p, "/")
	for _, c := range strings.Split(p, "/") {
		switch {
		case c == "" || c == "." || c == "..":
			return "", fmt.Errorf("%w: path component %q is not allowed", ErrInvalid, c)
		case len(c) > MaxComponentBytes:
			return "", fmt.Errorf("%w: path component longer than %d bytes", ErrInvalid, MaxComponentBytes)
		}
	}
	return p, nil
}

// IsHidden reports whether a cleaned path is or lies below a hidden name: the top-level RCON
// files or any component starting with ".mcpanel-" (temporary files of the service).
func IsHidden(clean string) bool {
	if clean == "" {
		return false
	}
	parts := strings.Split(clean, "/")
	if topHidden[parts[0]] {
		return true
	}
	for _, c := range parts {
		if strings.HasPrefix(c, hiddenPrefix) {
			return true
		}
	}
	return false
}

// Join joins a cleaned directory path and an entry name ("" + "a" = "a").
func Join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}
```

`internal/files/filesapi/errors.go`:

```go
package filesapi

import (
	"errors"
	"net/http"
)

const (
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
	ErrNotFound            = errors.New(CodeNotFound)
	ErrExists              = errors.New(CodeExists)
	ErrModified            = errors.New(CodeModified)
	ErrNotRegular          = errors.New(CodeNotRegular)
	ErrNotText             = errors.New(CodeNotText)
	ErrTooLarge            = errors.New(CodeTooLarge)
	ErrInsufficientStorage = errors.New(CodeInsufficientStorage)
	ErrBusy                = errors.New(CodeBusy)
	ErrInvalid             = errors.New(CodeInvalid)
	// ErrUnavailable: the service is unreachable, broke the protocol or failed internally.
	ErrUnavailable = errors.New("files service unavailable")
)

type codeInfo struct {
	err    error
	status int
}

// codes is ordered: CodeOf returns the first sentinel an error matches.
var codes = []struct {
	code string
	codeInfo
}{
	{CodeNotFound, codeInfo{ErrNotFound, http.StatusNotFound}},
	{CodeExists, codeInfo{ErrExists, http.StatusConflict}},
	{CodeModified, codeInfo{ErrModified, http.StatusConflict}},
	{CodeNotRegular, codeInfo{ErrNotRegular, http.StatusBadRequest}},
	{CodeNotText, codeInfo{ErrNotText, http.StatusUnsupportedMediaType}},
	{CodeTooLarge, codeInfo{ErrTooLarge, http.StatusRequestEntityTooLarge}},
	{CodeInsufficientStorage, codeInfo{ErrInsufficientStorage, http.StatusInsufficientStorage}},
	{CodeBusy, codeInfo{ErrBusy, http.StatusTooManyRequests}},
	{CodeInvalid, codeInfo{ErrInvalid, http.StatusBadRequest}},
}

// ErrorFromCode returns the sentinel of a wire code (nil for unknown codes).
func ErrorFromCode(code string) error {
	for _, c := range codes {
		if c.code == code {
			return c.err
		}
	}
	return nil
}

// CodeOf maps err to its wire code and HTTP status (used by the service); errors without a
// sentinel are internal: "", 500.
func CodeOf(err error) (code string, status int) {
	for _, c := range codes {
		if errors.Is(err, c.err) {
			return c.code, c.status
		}
	}
	return "", http.StatusInternalServerError
}

// StatusError is a non-2xx answer of the service.
type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	return "files: HTTP " + http.StatusText(e.Status) + ": " + e.Message
}

// Is maps a known code to its sentinel; unknown codes and answers without a code match
// ErrUnavailable.
func (e *StatusError) Is(target error) bool {
	if s := ErrorFromCode(e.Code); s != nil {
		return target == s
	}
	return target == ErrUnavailable
}
```

`internal/files/filesapi/types.go`:

```go
package filesapi

import (
	"context"
	"io"
)

const (
	PathHealth   = "/v1/health"
	PathList     = "/v1/list"
	PathRead     = "/v1/read"
	PathWrite    = "/v1/write"
	PathUpload   = "/v1/upload"   // PUT, query id, path, overwrite=0|1; body = file; Content-Length required
	PathDownload = "/v1/download" // GET, query id, path
	PathMkdir    = "/v1/mkdir"
	PathRename   = "/v1/rename"
	PathDelete   = "/v1/delete"
	PathExtract  = "/v1/extract"
	PathJobs     = "/v1/jobs" // GET, query id

	// HeaderKind is set on download answers: "file" or "dir" (a zip stream).
	HeaderKind = "X-Files-Kind"

	// MaxJSONBytes bounds a write request: a 5 MiB text can grow up to 6x by JSON escaping
	// (control characters become \u00XX), so the limit must exceed 6 × max_text_bytes.
	MaxJSONBytes      = 32 << 20
	MaxSmallJSONBytes = 64 << 10
)

type EntryType string

const (
	TypeFile    EntryType = "file"
	TypeDir     EntryType = "dir"
	TypeSymlink EntryType = "symlink"
	TypeOther   EntryType = "other"
)

type Entry struct {
	Name  string    `json:"name"`
	Type  EntryType `json:"type"`
	Size  int64     `json:"size"`
	MTime int64     `json:"mtime"` // Unix ms
}

// PathRequest is the body of list, read, mkdir and delete.
type PathRequest struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

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

type JobState string

const (
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

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
	Body  io.ReadCloser
	Size  int64  // -1 for a directory zip
	IsDir bool   // HeaderKind == "dir"
	Name  string // base name of the path ("" for the root: the caller uses the server name)
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
```

`internal/files/filesapi/client.go`:

```go
package filesapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"
)

const (
	// CallTimeout bounds every small call (list, read, write, mkdir, rename, delete, extract, jobs).
	CallTimeout = 10 * time.Second
	// HealthTimeout bounds the health check.
	HealthTimeout = 2 * time.Second
	// DownloadHeaderTimeout bounds the wait for download headers; the stream itself has no
	// deadline. Uploads have no header timeout: the service answers after the whole body.
	DownloadHeaderTimeout = 10 * time.Second

	baseURL          = "http://files"
	maxResponseBytes = 32 << 20 // a read answer carries up to 5 MiB of escaped text
	maxErrorBytes    = 64 << 10
)

// UnixClient talks to mcpanel-files over its unix socket. Safe for concurrent use.
type UnixClient struct {
	hc *http.Client
}

var _ Client = (*UnixClient)(nil)

func NewClient(socketPath string) *UnixClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		// Proxy stays nil: HTTP(S)_PROXY must never redirect file traffic.
	}
	return &UnixClient{hc: &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func checkID(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", ErrInvalid)
	}
	return nil
}

// clean validates id and every path before anything is sent.
func clean(id int64, paths ...*string) error {
	if err := checkID(id); err != nil {
		return err
	}
	for _, p := range paths {
		c, err := CleanPath(*p)
		if err != nil {
			return err
		}
		*p = c
	}
	return nil
}

func (c *UnixClient) Health(ctx context.Context) error {
	var out HealthResponse
	if err := c.call(ctx, HealthTimeout, http.MethodGet, PathHealth, nil, &out); err != nil {
		return err
	}
	if out.Status != "ok" {
		return fmt.Errorf("%w: health status %q", ErrUnavailable, out.Status)
	}
	return nil
}

func (c *UnixClient) List(ctx context.Context, id int64, p string) (ListResponse, error) {
	if err := clean(id, &p); err != nil {
		return ListResponse{}, err
	}
	var out ListResponse
	err := c.call(ctx, CallTimeout, http.MethodPost, PathList, PathRequest{ID: id, Path: p}, &out)
	if out.Entries == nil {
		out.Entries = []Entry{}
	}
	return out, err
}

func (c *UnixClient) Read(ctx context.Context, id int64, p string) (ReadResponse, error) {
	if err := clean(id, &p); err != nil {
		return ReadResponse{}, err
	}
	var out ReadResponse
	err := c.call(ctx, CallTimeout, http.MethodPost, PathRead, PathRequest{ID: id, Path: p}, &out)
	return out, err
}

func (c *UnixClient) Write(ctx context.Context, req WriteRequest) (WriteResponse, error) {
	if err := clean(req.ID, &req.Path); err != nil {
		return WriteResponse{}, err
	}
	var out WriteResponse
	err := c.call(ctx, CallTimeout, http.MethodPost, PathWrite, req, &out)
	return out, err
}

func (c *UnixClient) Mkdir(ctx context.Context, id int64, p string) error {
	if err := clean(id, &p); err != nil {
		return err
	}
	return c.call(ctx, CallTimeout, http.MethodPost, PathMkdir, PathRequest{ID: id, Path: p}, nil)
}

func (c *UnixClient) Rename(ctx context.Context, req RenameRequest) error {
	if err := clean(req.ID, &req.From, &req.To); err != nil {
		return err
	}
	return c.call(ctx, CallTimeout, http.MethodPost, PathRename, req, nil)
}

func (c *UnixClient) Delete(ctx context.Context, id int64, p string) error {
	if err := clean(id, &p); err != nil {
		return err
	}
	return c.call(ctx, CallTimeout, http.MethodPost, PathDelete, PathRequest{ID: id, Path: p}, nil)
}

// Extract starts an extract job. The service checks the archive synchronously (central
// directory: names, symlinks, sizes, entries, destination, disk space); those errors come back
// directly (ErrInvalid, ErrNotRegular, ErrExists, ErrTooLarge, ErrInsufficientStorage, ErrBusy)
// and no job is created. Only a passed check answers 202 with the running job.
func (c *UnixClient) Extract(ctx context.Context, req ExtractRequest) (Job, error) {
	if err := clean(req.ID, &req.Path, &req.Dest); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, CallTimeout, http.MethodPost, PathExtract, req, &out)
	return out, err
}

func (c *UnixClient) Job(ctx context.Context, id int64) (*Job, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	var out JobsResponse
	q := url.Values{"id": {strconv.FormatInt(id, 10)}}
	if err := c.call(ctx, CallTimeout, http.MethodGet, PathJobs+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out.Job, nil
}

// Upload streams body (exactly size bytes) to path. There is no timeout besides ctx: the
// service answers only after the whole body arrived (spec §4, up to 4 GiB).
func (c *UnixClient) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (UploadResponse, error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, baseURL+PathUpload+"?"+q.Encode(), io.NopCloser(body))
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

// Download opens path for streaming. Headers must arrive within DownloadHeaderTimeout; the body
// has no deadline (ctx only). Closing the body releases the connection.
func (c *UnixClient) Download(ctx context.Context, id int64, p string) (Download, error) {
	if err := clean(id, &p); err != nil {
		return Download{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "path": {p}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+PathDownload+"?"+q.Encode(), nil)
	if err != nil {
		cancel()
		return Download{}, fmt.Errorf("files: build request: %w", err)
	}
	timer := time.AfterFunc(DownloadHeaderTimeout, cancel)
	resp, err := c.hc.Do(req)
	if !timer.Stop() {
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		return Download{}, fmt.Errorf("%w: download: no response within %s", ErrUnavailable, DownloadHeaderTimeout)
	}
	if err != nil {
		cancel()
		return Download{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		return Download{}, decode(resp, nil)
	}
	d := Download{Body: &cancelBody{ReadCloser: resp.Body, cancel: cancel}, Size: resp.ContentLength}
	if resp.Header.Get(HeaderKind) == "dir" {
		d.IsDir, d.Size = true, -1
	}
	if p != "" {
		d.Name = path.Base(p)
	}
	return d, nil
}

// cancelBody releases the request context when the body is closed.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// call sends one small JSON request (in may be nil) and decodes the answer into out (may be nil).
func (c *UnixClient) call(ctx context.Context, timeout time.Duration, method, target string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if in != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // keep config text compact (see MaxJSONBytes)
		if err := enc.Encode(in); err != nil {
			return fmt.Errorf("files: encode request: %w", err)
		}
		body = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+target, body)
	if err != nil {
		return fmt.Errorf("files: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decode(resp, out)
}

// decode turns a 2xx answer into out (nil = ignore the body) and anything else into a
// *StatusError.
func decode(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		var er ErrorResponse
		msg := http.StatusText(resp.StatusCode)
		if json.Unmarshal(data, &er) == nil && er.Error != "" {
			msg = er.Error
		}
		return &StatusError{Status: resp.StatusCode, Code: er.Code, Message: msg}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBytes))
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", ErrUnavailable, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", ErrUnavailable, maxResponseBytes)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decode response: %w", ErrUnavailable, err)
	}
	return nil
}
```

- [ ] **Step 7: Konfiguration implementieren**

`internal/config/files.go`:

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
)

// maxTextBytesLimit keeps every editor text within one write request after JSON escaping
// (filesapi.MaxJSONBytes = 32 MiB, escaping grows text up to 6x).
const maxTextBytesLimit = (32 << 20) / 6

// FilesLimits are the limits of the files service (spec 3b §3.1).
type FilesLimits struct {
	MaxUploadBytes    int64 `toml:"max_upload_bytes"`
	MaxExtractBytes   int64 `toml:"max_extract_bytes"`
	MaxExtractEntries int64 `toml:"max_extract_entries"`
	ReserveBytes      int64 `toml:"reserve_bytes"`
	MaxTextBytes      int64 `toml:"max_text_bytes"`
	MaxListEntries    int   `toml:"max_list_entries"`
}

// Files is the configuration of mcpanel-files (files.toml).
type Files struct {
	Dev          bool        `toml:"dev"`
	Socket       string      `toml:"socket"`      // used only without systemd socket activation
	SocketMode   string      `toml:"socket_mode"` // octal, default "0660"; used only without socket activation
	PanelUID     int         `toml:"panel_uid"`   // -1 = unset (only allowed with dev)
	InstancesDir string      `toml:"instances_dir"`
	Limits       FilesLimits `toml:"limits"`
}

func defaultFilesLimits() FilesLimits {
	return FilesLimits{
		MaxUploadBytes:    4 << 30,
		MaxExtractBytes:   20 << 30,
		MaxExtractEntries: 100000,
		ReserveBytes:      2 << 30,
		MaxTextBytes:      5 << 20,
		MaxListEntries:    10000,
	}
}

func DefaultFiles() Files {
	return Files{
		Socket:       "/run/mc-panel-files/files.sock",
		SocketMode:   "0660",
		PanelUID:     -1,
		InstancesDir: DefaultInstancesDir,
		Limits:       defaultFilesLimits(),
	}
}

// DevFiles is the profile of `make dev`: same instance directories as the dev agent (so
// MCPANEL_DEV_DOCKER=1 shares them), the developer's own uid as the panel uid.
func DevFiles(root string) Files {
	f := DefaultFiles()
	f.Dev = true
	f.Socket = filepath.Join(root, ".dev", "files.sock")
	f.PanelUID = os.Getuid()
	f.InstancesDir = filepath.Join(root, ".dev", "mc")
	return f
}

// SocketFileMode returns socket_mode as a file mode (0 if invalid; Validate rejects that).
func (f Files) SocketFileMode() os.FileMode {
	m, err := strconv.ParseUint(f.SocketMode, 8, 32)
	if err != nil || m > 0o777 {
		return 0
	}
	return os.FileMode(m)
}

// LoadFiles starts from DefaultFiles and overlays the TOML file at path; unknown keys are refused.
// Keys missing in the file (also single [limits] keys) keep their defaults: toml decodes into the
// pre-filled struct.
func LoadFiles(path string) (Files, error) {
	f := DefaultFiles()
	md, err := toml.DecodeFile(path, &f)
	if err != nil {
		return Files{}, fmt.Errorf("config: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return Files{}, fmt.Errorf("config: unknown keys: %v", und)
	}
	if err := f.Validate(); err != nil {
		return Files{}, err
	}
	return f, nil
}

func (f Files) Validate() error {
	var errs []error
	if err := cleanAbsPath("socket", f.Socket); err != nil {
		errs = append(errs, err)
	}
	if m, err := strconv.ParseUint(f.SocketMode, 8, 32); err != nil || m > 0o777 {
		errs = append(errs, fmt.Errorf("socket_mode: %q must be an octal mode between 0 and 0777", f.SocketMode))
	}
	if f.PanelUID < 0 && !f.Dev {
		errs = append(errs, errors.New("panel_uid must be set to the uid of the panel user"))
	}
	if err := cleanAbsPath("instances_dir", f.InstancesDir); err != nil {
		errs = append(errs, err)
	} else if f.InstancesDir == "/" {
		errs = append(errs, errors.New("instances_dir must not be /"))
	}
	l := f.Limits
	for name, v := range map[string]int64{
		"max_upload_bytes": l.MaxUploadBytes, "max_extract_bytes": l.MaxExtractBytes,
		"max_extract_entries": l.MaxExtractEntries, "max_text_bytes": l.MaxTextBytes,
		"max_list_entries": int64(l.MaxListEntries),
	} {
		if v <= 0 {
			errs = append(errs, fmt.Errorf("limits.%s must be > 0", name))
		}
	}
	if l.ReserveBytes < 0 {
		errs = append(errs, errors.New("limits.reserve_bytes must be >= 0"))
	}
	if l.MaxTextBytes > maxTextBytesLimit {
		errs = append(errs, fmt.Errorf("limits.max_text_bytes must be <= %d", maxTextBytesLimit))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}
```

In `internal/config/config.go`:

```go
type Panel struct {
	…
	AgentSocket    string   `toml:"agent_socket"`
	FilesSocket    string   `toml:"files_socket"`
	…
}
```

`DefaultPanel`: `FilesSocket: "/run/mc-panel-files/files.sock",`; `DevPanel`: `p.FilesSocket = filepath.Join(root, ".dev", "files.sock")`; in `Validate`:

```go
	if p.DataDir == "" || p.AgentSocket == "" || p.FilesSocket == "" {
		errs = append(errs, errors.New("data_dir, agent_socket and files_socket must be set"))
	}
```

Falls ein bestehender Test auf den alten Fehlertext prüft (`grep -rn "data_dir and agent_socket" internal cmd`), mit anpassen.

- [ ] **Step 8: `make check`, Commit, Push, CI**

```bash
make check
git add internal/files internal/config
git commit -m "feat(files): filesapi contract, path rules, client and files.toml

Path rules shared by panel and files service, wire types, error codes with
sentinels, a unix-socket client with streaming upload/download, and the
configuration of mcpanel-files plus files_socket for the panel.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: `TestCleanPath`, `TestIsHidden`, `TestJoin`, `TestCodesRoundTrip`, `TestStatusErrorIs`, `TestClient*`, `TestDefaultFiles`, `TestDevFiles`, `TestLoadFiles*`, `TestPanelFilesSocket` PASS, auch mit `-race`.

---

### Task 3: panel-Vertrag – OpenAPI, Fehlercodes, Audit, Body-Limit-Ausnahme, Stub-Handler, Mocks

**Files:**
- Modify: `api/openapi.yaml` (Tag `files`, 10 Operationen nach `/servers/{id}/players/actions`, Schemas nach `PlayerActionRequest`, Error-Enum, `ServiceUnavailable`-Beschreibung, `serverPower` 409); generiert: `web/src/api/schema.d.ts`
- Modify: `internal/httpx/errors.go` (7 Codes), `internal/httpx/middleware.go` (`BodyLimitExcept`), `internal/httpx/httpx_test.go`
- Modify: `internal/audit/types.go` (7 Aktionen)
- Create: `internal/serverfiles/handler.go`, `internal/serverfiles/routes_test.go`
- Modify: `cmd/panel/app.go` (`appDeps.Files`, Handler, `BodyLimitExcept`), `cmd/panel/main.go` (`filesapi.NewClient(cfg.FilesSocket)`), `cmd/panel/integration_test.go` (Routen 66 → 76)
- Modify: `web/src/api/types.ts`, `web/src/api/errors.ts` (`KNOWN_CODES`)
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts` (`ERROR_CODES`)
- Create: `web/src/mocks/handlers/files.ts`
- Modify: `web/src/mocks/db.ts` (`MockNode`, `db.files`, `db.fileJobs`, Seed), `web/src/mocks/handlers.ts`, `web/src/mocks/handlers.test.ts` (66 → 76, Verhaltenstests)

**Interfaces:**
- Consumes: `filesapi.Client` (Aufgabe 2), `agentapi.InstancesClient`, `config.Panel.FilesSocket`.
- Produces:
  - httpx: `CodeFilesUnavailable = "files_unavailable"`, `CodeFileExists = "file_exists"`, `CodeFileModified = "file_modified"`, `CodeNotText = "not_text"`, `CodeNotRegular = "not_regular"`, `CodeInstanceRunning = "instance_running"`, `CodeInsufficientStorage = "insufficient_storage"`; `func BodyLimitExcept(n int64, skip func(*http.Request) bool) func(http.Handler) http.Handler`.
  - audit: `ActionFileWrite = "file.write"`, `ActionFileUpload = "file.upload"`, `ActionFileMkdir = "file.mkdir"`, `ActionFileRename = "file.rename"`, `ActionFileDelete = "file.delete"`, `ActionFileExtract = "file.extract"`, `ActionFileDownload = "file.download"`.
  - `serverfiles.Deps`, `serverfiles.NewHandler`, `(*Handler).Routes`, `(*Handler).ExtractRunning` (Stub: immer `false`), `serverfiles.UnlimitedBody(r *http.Request) bool` (true nur für `POST` auf `/api/servers/<zahl>/files/upload`).
  - `cmd/panel`: `appDeps.Files filesapi.Client` (nil = kein Dienst), Handler in `buildApp` verdrahtet; B2 ersetzt nur Inhalte von `internal/serverfiles` und ergänzt `servers.Deps.Extract`.
  - TS: `FileEntry`, `FileEntryType`, `FileList`, `FileContent`, `WriteFileRequest`, `FileWriteResult`, `UploadResult`, `FilePathRequest`, `RenameFileRequest`, `ExtractRequest`, `FileJob`, `FileJobState`, `FileJobResponse`; i18n `errors.<code>` für die sieben Codes.
  - MSW: `filesOperations` (10), `db.files: Record<number, MockNode>`, `db.fileJobs: Record<number, MockFileJob>`, Export `MockNode` und `mockTree()` aus `db.ts`.

- [ ] **Step 1: OpenAPI erweitern**

Unter `tags:` nach `console` einfügen:

```yaml
  - { name: files, description: 'File browser of a server: list, edit, upload, download, manage, extract' }
```

Nach der Operation `/servers/{id}/players/actions` (vor `/audit:`) einfügen:

```yaml
  /servers/{id}/files:
    get:
      operationId: listFiles
      tags: [files]
      summary: List a directory of the server's data
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/FilePathQuery'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/FileList' } } } }
        '400': { description: 'validation_failed or not_regular (not a directory)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/files/content:
    get:
      operationId: readFile
      tags: [files]
      summary: Read a text file for the editor (rcon.password redacted)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/FilePathQueryRequired'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/FileContent' } } } }
        '400': { description: 'validation_failed or not_regular', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '415': { description: not_text (too large for the editor or not text), content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
    put:
      operationId: writeFile
      tags: [files]
      summary: Save a text file (atomic, with conflict check; audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/WriteFileRequest' } } }
      responses:
        '200': { description: Saved, content: { application/json: { schema: { $ref: '#/components/schemas/FileWriteResult' } } } }
        '400': { description: 'validation_failed or not_regular', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'file_exists (new file) or file_modified (changed since it was opened)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '413': { description: payload_too_large, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
        '507': { $ref: '#/components/responses/InsufficientStorage' }
  /servers/{id}/files/upload:
    post:
      operationId: uploadFile
      tags: [files]
      summary: Upload one file as a raw stream (up to 4 GiB; audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
        - $ref: '#/components/parameters/FilePathQueryRequired'
        - { name: overwrite, in: query, required: false, schema: { type: boolean, default: false } }
      requestBody:
        required: true
        content: { application/octet-stream: { schema: { type: string, format: binary } } }
      responses:
        '201': { description: Uploaded, content: { application/json: { schema: { $ref: '#/components/schemas/UploadResult' } } } }
        '400': { description: 'validation_failed (also without Content-Length) or not_regular', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: file_exists (without overwrite), content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '413': { description: payload_too_large, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
        '507': { $ref: '#/components/responses/InsufficientStorage' }
  /servers/{id}/files/download:
    get:
      operationId: downloadFile
      tags: [files]
      summary: Download a file, or a directory as a zip stream (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/FilePathQuery'
      responses:
        '200':
          description: File or zip stream with Content-Disposition attachment
          content:
            application/octet-stream: { schema: { type: string, format: binary } }
            application/zip: { schema: { type: string, format: binary } }
        '400': { description: 'validation_failed or not_regular (symlink or special file)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/files/mkdir:
    post:
      operationId: makeDirectory
      tags: [files]
      summary: Create a directory (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/FilePathRequest' } } }
      responses:
        '201': { description: Created }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: file_exists, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/files/rename:
    post:
      operationId: renameFile
      tags: [files]
      summary: Rename or move a file or directory (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/RenameFileRequest' } } }
      responses:
        '204': { description: Renamed }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: file_exists, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/files/delete:
    post:
      operationId: deleteFile
      tags: [files]
      summary: Delete a file or a directory with its content (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/FilePathRequest' } } }
      responses:
        '204': { description: Deleted }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
  /servers/{id}/files/extract:
    post:
      operationId: extractArchive
      tags: [files]
      summary: Start extracting a zip into a new or empty directory (server must be stopped; audited)
      description: The archive is checked before the job starts (entry names, symlinks, encryption, compression, total size and entries, destination, free space); a failed check answers 400/409/413/507 and starts no job.
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/ExtractRequest' } } }
      responses:
        '202': { description: Job started, content: { application/json: { schema: { $ref: '#/components/schemas/FileJob' } } } }
        '400': { description: 'validation_failed or not_regular', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'instance_running (server must be stopped) or file_exists (destination not empty)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '413': { description: 'payload_too_large (archive exceeds the size or entry limit)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
        '507': { $ref: '#/components/responses/InsufficientStorage' }
  /servers/{id}/files/jobs:
    get:
      operationId: getFileJob
      tags: [files]
      summary: The running or last extract job of the server
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/FileJobResponse' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
```

Unter `components.parameters` ergänzen:

```yaml
    FilePathQuery: { name: path, in: query, required: false, description: 'Relative path in the data directory; empty = root', schema: { type: string, maxLength: 4096, default: '' } }
    FilePathQueryRequired: { name: path, in: query, required: true, description: 'Relative path in the data directory', schema: { type: string, minLength: 1, maxLength: 4096 } }
```

Unter `components.responses`: `ServiceUnavailable` ersetzen und `InsufficientStorage` ergänzen:

```yaml
    ServiceUnavailable: { description: 'Agent, Docker, RCON or the files service not available (agent_unavailable, docker_unavailable, rcon_unavailable, files_unavailable)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    InsufficientStorage: { description: insufficient_storage (less than the reserve would be left), content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

Im `Error`-Schema das Enum um `files_unavailable, file_exists, file_modified, not_text, not_regular, instance_running, insufficient_storage` erweitern (vor `internal`):

```yaml
        error: { type: string, enum: [bad_request, validation_failed, unauthorized, invalid_credentials, invalid_code, forbidden, sudo_required, setup_2fa_required, csrf_invalid, not_found, conflict, payload_too_large, rate_limited, jailed, ip_blocked, agent_unavailable, docker_unavailable, base_domain_missing, catalog_unavailable, instance_not_running, rcon_unavailable, files_unavailable, file_exists, file_modified, not_text, not_regular, instance_running, insufficient_storage, internal] }
```

In `serverPower` die `409`-Beschreibung ersetzen:

```yaml
        '409': { description: 'conflict (server not configured, restart of a container that belongs to another server, or an archive is being extracted) or base_domain_missing (restart)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

Nach dem Schema `PlayerActionRequest` einfügen:

```yaml
    FileEntryType: { type: string, enum: [file, dir, symlink, other] }
    FileEntry:
      type: object
      required: [name, type, size, mtime]
      properties:
        name: { type: string }
        type: { $ref: '#/components/schemas/FileEntryType' }
        size: { type: integer, format: int64 }
        mtime: { type: integer, format: int64, description: Unix ms }
    FileList:
      type: object
      required: [path, entries, truncated]
      properties:
        path: { type: string, description: Cleaned path of the listed directory ('' = root) }
        entries: { type: array, items: { $ref: '#/components/schemas/FileEntry' } }
        truncated: { type: boolean, description: More than 10 000 entries; only the first are listed }
    FileContent:
      type: object
      required: [path, content, mtime, size]
      properties:
        path: { type: string }
        content: { type: string }
        mtime: { type: integer, format: int64 }
        size: { type: integer, format: int64 }
    WriteFileRequest:
      type: object
      additionalProperties: false
      required: [path, content, expected_mtime]
      properties:
        path: { type: string, minLength: 1, maxLength: 4096 }
        content: { type: string }
        expected_mtime: { type: [integer, 'null'], format: int64, description: mtime from readFile; null = create a new file }
    FileWriteResult:
      type: object
      required: [mtime, size]
      properties:
        mtime: { type: integer, format: int64 }
        size: { type: integer, format: int64 }
    UploadResult:
      type: object
      required: [size]
      properties:
        size: { type: integer, format: int64 }
    FilePathRequest:
      type: object
      additionalProperties: false
      required: [path]
      properties:
        path: { type: string, minLength: 1, maxLength: 4096 }
    RenameFileRequest:
      type: object
      additionalProperties: false
      required: [from, to]
      properties:
        from: { type: string, minLength: 1, maxLength: 4096 }
        to: { type: string, minLength: 1, maxLength: 4096 }
    ExtractRequest:
      type: object
      additionalProperties: false
      required: [path, dest]
      properties:
        path: { type: string, minLength: 1, maxLength: 4096, description: A .zip file }
        dest: { type: string, minLength: 1, maxLength: 4096, description: New or empty directory }
    FileJobState: { type: string, enum: [running, done, failed] }
    FileJob:
      type: object
      required: [id, state, path, dest, bytes, entries, total_bytes, total_entries, error, started_at, finished_at]
      properties:
        id: { type: string }
        state: { $ref: '#/components/schemas/FileJobState' }
        path: { type: string }
        dest: { type: string }
        bytes: { type: integer, format: int64 }
        entries: { type: integer, format: int64 }
        total_bytes: { type: integer, format: int64 }
        total_entries: { type: integer, format: int64 }
        error: { type: string }
        started_at: { type: integer, format: int64 }
        finished_at: { type: integer, format: int64, description: 0 while running }
    FileJobResponse:
      type: object
      required: [job]
      properties:
        job:
          oneOf:
            - $ref: '#/components/schemas/FileJob'
            - type: 'null'
```

Dann: `make generate` (regeneriert `web/src/api/schema.d.ts`; `sqlc` ändert nichts).

- [ ] **Step 2: Go-Tests schreiben (rot)**

`internal/httpx/httpx_test.go` ergänzen:

```go
func TestBodyLimitExcept(t *testing.T) {
	read := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := httpx.BodyLimitExcept(16, func(r *http.Request) bool { return r.URL.Path == "/upload" })(read)
	big := strings.Repeat("x", 64)
	for path, want := range map[string]int{"/upload": http.StatusNoContent, "/other": http.StatusRequestEntityTooLarge} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(big)))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", path, rec.Code, want)
		}
	}
}
```

(`io` zum Import ergänzen.)

`internal/serverfiles/routes_test.go`:

```go
package serverfiles_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// wantRoutes are exactly the files operations of api/openapi.yaml with their rule (V3/V4).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/files":           rbac.Server(rbac.PermServerFiles, "id"),
	"GET /api/servers/{id}/files/content":   rbac.Server(rbac.PermServerFiles, "id"),
	"PUT /api/servers/{id}/files/content":   rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/upload":   rbac.Server(rbac.PermServerFiles, "id"),
	"GET /api/servers/{id}/files/download":  rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/mkdir":    rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/rename":   rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/delete":   rbac.Server(rbac.PermServerFiles, "id"),
	"POST /api/servers/{id}/files/extract":  rbac.Server(rbac.PermServerFiles, "id"),
	"GET /api/servers/{id}/files/jobs":      rbac.Server(rbac.PermServerFiles, "id"),
}

func newRouter(t *testing.T) (*chi.Mux, *rbac.Router) {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		serverfiles.NewHandler(serverfiles.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())
	return mux, rt
}

func TestRouteContract(t *testing.T) {
	mux, rt := newRouter(t)
	got := rt.Routes()
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
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

func TestUnlimitedBody(t *testing.T) {
	for target, want := range map[string]bool{
		"POST /api/servers/7/files/upload":       true,
		"POST /api/servers/7/files/upload?path=x": true,
		"PUT /api/servers/7/files/upload":        false,
		"POST /api/servers/x/files/upload":       false,
		"POST /api/servers/7/files/upload/":      false,
		"POST /api/servers/7/files/mkdir":        false,
		"POST /api/servers/7/console":            false,
	} {
		var method, path string
		for i := range target {
			if target[i] == ' ' {
				method, path = target[:i], target[i+1:]
				break
			}
		}
		r := httptest.NewRequest(method, path, nil)
		if got := serverfiles.UnlimitedBody(r); got != want {
			t.Errorf("UnlimitedBody(%s) = %v, want %v", target, got, want)
		}
	}
}

// The stub (Phase A) answers 503 files_unavailable on every route until B2.
func TestStubAnswersFilesUnavailable(t *testing.T) {
	h := serverfiles.NewHandler(serverfiles.Deps{DB: storetest.Open(t), Clock: clock.NewFake(t0), Audit: &audit.MemoryRecorder{}})
	rec := httptest.NewRecorder()
	h.ServeStub(rec, httptest.NewRequest(http.MethodGet, "/api/servers/1/files", nil))
	var body struct{ Error string }
	if rec.Code != http.StatusServiceUnavailable || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error != "files_unavailable" {
		t.Fatalf("stub = %d %s", rec.Code, rec.Body)
	}
	if h.ExtractRunning(t.Context(), 1) {
		t.Fatal("stub ExtractRunning must be false")
	}
}
```

`cmd/panel/integration_test.go:185` – Zähler auf 76 setzen:

```go
	if n := len(e.app.Routes); n != 76 {
		t.Fatalf("registered %d API routes, OpenAPI defines 76 operations (incl. GET /health)", n)
	}
```

- [ ] **Step 3: `make check` (rot)**

Run: `git add -N internal/serverfiles && make check`
Expected: Kompilierfehler (`undefined: httpx.BodyLimitExcept`, `serverfiles`).

- [ ] **Step 4: Codes, Aktionen, Body-Limit implementieren**

`internal/httpx/errors.go` – im `const`-Block vor `CodeInternal`:

```go
	CodeFilesUnavailable    = "files_unavailable"
	CodeFileExists          = "file_exists"
	CodeFileModified        = "file_modified"
	CodeNotText             = "not_text"
	CodeNotRegular          = "not_regular"
	CodeInstanceRunning     = "instance_running"
	CodeInsufficientStorage = "insufficient_storage"
```

`internal/httpx/middleware.go` nach `BodyLimit`:

```go
// BodyLimitExcept is BodyLimit for every request except those skip accepts. A streaming
// endpoint (the file upload, spec 3b §5.1) enforces its own limit downstream.
func BodyLimitExcept(n int64, skip func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !skip(r) {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

`internal/audit/types.go` – nach `ActionServerPlayerAction`:

```go
	ActionFileWrite            = "file.write"
	ActionFileUpload           = "file.upload"
	ActionFileMkdir            = "file.mkdir"
	ActionFileRename           = "file.rename"
	ActionFileDelete           = "file.delete"
	ActionFileExtract          = "file.extract"
	ActionFileDownload         = "file.download"
```

(gofmt richtet die `=` aus.)

- [ ] **Step 5: Stub-Handler**

`internal/serverfiles/handler.go`:

```go
// Package serverfiles implements the file browser API of a Minecraft server (sub-project 3b).
// Every file operation runs in the unprivileged files service (mcpanel-files); the panel checks
// server, permission, CSRF and audit and streams uploads/downloads through without buffering.
package serverfiles

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// Deps are the dependencies of the file handlers.
type Deps struct {
	DB    *sql.DB // from store.Open
	Clock clock.Clock
	Audit audit.Recorder
	// Files is the files service; nil = no service: every route answers 503 files_unavailable.
	Files filesapi.Client
	// Instances is the agent's instance API (actual state for extract); nil = no agent.
	Instances agentapi.InstancesClient
	Log       *slog.Logger // nil = slog.Default()
}

type Handler struct {
	db    *sql.DB
	clock clock.Clock
	rec   audit.Recorder
	files filesapi.Client
	inst  agentapi.InstancesClient
	log   *slog.Logger
}

func NewHandler(d Deps) *Handler {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{db: d.DB, clock: d.Clock, rec: d.Audit, files: d.Files, inst: d.Instances, log: log}
}

// Routes registers the file routes (paths relative to /api, rule from contract V4).
func (h *Handler) Routes(api *rbac.Router) {
	files := rbac.Server(rbac.PermServerFiles, "id")
	api.Get("/servers/{id}/files", files, h.ServeStub)
	api.Get("/servers/{id}/files/content", files, h.ServeStub)
	api.Put("/servers/{id}/files/content", files, h.ServeStub)
	api.Post("/servers/{id}/files/upload", files, h.ServeStub)
	api.Get("/servers/{id}/files/download", files, h.ServeStub)
	api.Post("/servers/{id}/files/mkdir", files, h.ServeStub)
	api.Post("/servers/{id}/files/rename", files, h.ServeStub)
	api.Post("/servers/{id}/files/delete", files, h.ServeStub)
	api.Post("/servers/{id}/files/extract", files, h.ServeStub)
	api.Get("/servers/{id}/files/jobs", files, h.ServeStub)
}

// ServeStub answers every route until Phase B2 replaces the handlers.
func (h *Handler) ServeStub(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "files not implemented yet")
}

// ExtractRunning reports whether an extract job runs for the server (servers.ExtractGate).
// Stub: never.
func (h *Handler) ExtractRunning(_ context.Context, _ int64) bool { return false }

var uploadPath = regexp.MustCompile(`^/api/servers/[0-9]+/files/upload$`)

// UnlimitedBody selects the requests exempt from the global 1 MiB body limit: only the upload
// stream (its own limit is enforced by the files service, spec 3b §5.1).
func UnlimitedBody(r *http.Request) bool {
	return r.Method == http.MethodPost && uploadPath.MatchString(r.URL.Path)
}
```

- [ ] **Step 6: Verdrahtung**

`cmd/panel/app.go`:

```go
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
```

In `appDeps` nach `Console`:

```go
	// Files is the files service (mcpanel-files); nil means no service: file routes answer
	// files_unavailable.
	Files filesapi.Client
```

In `buildApp` nach `consoleH`:

```go
	filesH := serverfiles.NewHandler(serverfiles.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Files: d.Files, Instances: d.Instances,
	})
```

Middleware-Kette: `httpx.BodyLimit(1<<20),` ersetzen durch `httpx.BodyLimitExcept(1<<20, serverfiles.UnlimitedBody),` und den Kommentar über `buildApp` anpassen (`… -> BodyLimit (except the file upload stream) -> …`). In `rt.Route` nach `consoleH.Routes(api)`: `filesH.Routes(api)`.

`cmd/panel/main.go` (Zeile ~239):

```go
	ac := agent.NewClient(cfg.AgentSocket) // firewall and instance API share one client
	fc := filesapi.NewClient(cfg.FilesSocket)
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac, Console: ac, Files: fc, Version: version})
```

(`filesapi` importieren.)

- [ ] **Step 7: Frontend-Typen, Codes, Texte**

`web/src/api/types.ts` nach `PlayerActionRequest`:

```ts
export type FileEntryType = Schemas['FileEntryType']
export type FileEntry = Schemas['FileEntry']
export type FileList = Schemas['FileList']
export type FileContent = Schemas['FileContent']
export type WriteFileRequest = Schemas['WriteFileRequest']
export type FileWriteResult = Schemas['FileWriteResult']
export type UploadResult = Schemas['UploadResult']
export type FilePathRequest = Schemas['FilePathRequest']
export type RenameFileRequest = Schemas['RenameFileRequest']
export type ExtractRequest = Schemas['ExtractRequest']
export type FileJobState = Schemas['FileJobState']
export type FileJob = Schemas['FileJob']
export type FileJobResponse = Schemas['FileJobResponse']
```

`web/src/api/errors.ts` – `KNOWN_CODES` vor `'internal'`:

```ts
  'files_unavailable',
  'file_exists',
  'file_modified',
  'not_text',
  'not_regular',
  'instance_running',
  'insufficient_storage',
```

`web/src/i18n/i18n.test.ts` – dieselben sieben Codes in `ERROR_CODES` vor `'internal'`.

`web/src/i18n/de.ts` unter `errors` (nach `rcon_unavailable`):

```ts
    files_unavailable: 'Der Dateidienst ist nicht erreichbar.',
    file_exists: 'Dort existiert bereits eine Datei oder ein Ordner mit diesem Namen.',
    file_modified: 'Die Datei wurde inzwischen geändert.',
    not_text: 'Die Datei ist zu groß für den Editor oder keine Textdatei.',
    not_regular: 'Das ist keine normale Datei (Ordner, Verknüpfung oder Sonderdatei).',
    instance_running: 'Der Server muss dafür gestoppt sein.',
    insufficient_storage: 'Nicht genug freier Speicherplatz auf dem Server.',
```

`web/src/i18n/en.ts` analog:

```ts
    files_unavailable: 'The file service is not reachable.',
    file_exists: 'A file or folder with this name already exists there.',
    file_modified: 'The file has been changed in the meantime.',
    not_text: 'The file is too large for the editor or not a text file.',
    not_regular: 'This is not a regular file (folder, link or special file).',
    instance_running: 'The server must be stopped for this.',
    insufficient_storage: 'Not enough free disk space on the server.',
```

- [ ] **Step 8: Mock-Tests schreiben (rot)**

`web/src/mocks/handlers.test.ts`: Zählung `66` → `76` und neuer Block in `describe('mock behaviour')`:

```ts
  it('lists files without the RCON files and edits with a conflict check', async () => {
    const me = loginAs('admin')
    const root = (await callApi('GET', '/servers/1/files')).body as { entries: Array<{ name: string; type: string }> }
    const names = root.entries.map((e) => e.name)
    expect(names).toContain('server.properties')
    expect(names).not.toContain('.rcon-cli.env')
    expect(root.entries[0].type).toBe('dir') // folders first
    const read = await callApi('GET', '/servers/1/files/content?path=server.properties')
    expect(field(read.body, 'content')).toContain('rcon.password=********')
    const mtime = field(read.body, 'mtime') as number
    const save = (expected: number | null) =>
      callApi('PUT', '/servers/1/files/content', { body: { path: 'server.properties', content: 'motd=x\n', expected_mtime: expected }, csrf: me.csrf_token })
    expect((await save(mtime - 1)).status).toBe(409)
    expect(field((await save(mtime - 1)).body, 'error')).toBe('file_modified')
    expect((await save(mtime)).status).toBe(200)
    expect(field((await save(null)).body, 'error')).toBe('file_exists')
    expect(db.audit.at(-1)).toMatchObject({ action: 'file.write', details: { path: 'server.properties' } })
    expect(field((await callApi('GET', '/servers/1/files/content?path=plugins/EssentialsX.jar')).body, 'error')).toBe('not_text')
    expect((await callApi('GET', '/servers/1/files/content?path=../x')).status).toBe(400)
  })

  it('uploads, manages and extracts files', async () => {
    const me = loginAs('admin')
    const upload = (path: string, overwrite = false) =>
      fetch(`${window.location.origin}/api/servers/1/files/upload?path=${encodeURIComponent(path)}&overwrite=${overwrite}`, {
        method: 'POST',
        headers: { 'X-CSRF-Token': me.csrf_token, 'Content-Type': 'application/octet-stream' },
        body: new Uint8Array([1, 2, 3]),
      })
    expect((await upload('plugins/new.jar')).status).toBe(201)
    expect((await upload('plugins/new.jar')).status).toBe(409)
    expect((await upload('plugins/new.jar', true)).status).toBe(201)
    const post = (path: string, body: unknown) => callApi('POST', `/servers/1/files/${path}`, { body, csrf: me.csrf_token })
    expect((await post('mkdir', { path: 'backups' })).status).toBe(201)
    expect((await post('mkdir', { path: 'backups' })).status).toBe(409)
    expect((await post('rename', { from: 'backups', to: 'old-backups' })).status).toBe(204)
    expect((await post('delete', { path: 'old-backups' })).status).toBe(204)
    expect((await post('delete', { path: '' })).status).toBe(400)
    // The running server refuses extraction; the stopped one runs a job.
    expect(field((await post('extract', { path: 'world-backup.zip', dest: 'restored' })).body, 'error')).toBe('instance_running')
    const job = await callApi('POST', '/servers/2/files/extract', { body: { path: 'world-backup.zip', dest: 'restored' }, csrf: me.csrf_token })
    expect(job.status).toBe(202)
    expect(field(job.body, 'state')).toBe('running')
    const status = await callApi('GET', '/servers/2/files/jobs')
    expect(field(field(status.body, 'job'), 'dest')).toBe('restored')
  })

  it('requires server.files for the file browser', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/servers/1/files')).status).toBe(403)
  })
```

- [ ] **Step 9: Mock implementieren**

`web/src/mocks/db.ts` – Typen, Seed und Felder ergänzen:

```ts
import type { FileJob } from '@/api/types' // zu den bestehenden Typ-Importen ergänzen

/** A node of a server's mock data directory. Text files carry content; binary files only a size. */
export interface MockNode {
  type: 'file' | 'dir' | 'symlink' | 'other'
  mtime: number
  size: number
  content?: string // text files only
  children?: Record<string, MockNode> // dirs only
  target?: string // symlinks only
}

export interface MockFileJob extends FileJob {
  startedMs: number
}

function textNode(content: string, mtime: number): MockNode {
  return { type: 'file', mtime, size: new TextEncoder().encode(content).length, content }
}

function binNode(size: number, mtime: number): MockNode {
  return { type: 'file', mtime, size }
}

function dirNode(mtime: number, children: Record<string, MockNode> = {}): MockNode {
  return { type: 'dir', mtime, size: 0, children }
}

/** The data directories of the seeded servers (spec 3b mock V5). */
export function mockTree(now: number, running: boolean): MockNode {
  const t = now - 2 * HOUR
  const props = 'motd=Unser Server\ndifficulty=normal\nrcon.password=********\nenable-rcon=true\n'
  const tree: Record<string, MockNode> = {
    'server.properties': textNode(props, t),
    'ops.json': textNode('[]\n', t),
    '.rcon-cli.env': textNode('password=secret\n', t), // hidden by the API
    world: dirNode(t, { 'level.dat': binNode(4096, t) }),
    logs: dirNode(t, { 'latest.log': textNode('[12:00:00 INFO]: Done (3.2s)! For help, type "help"\n', t) }),
    'world-backup.zip': binNode(30 * MIB, t - DAY),
  }
  if (running) {
    tree['bukkit.yml'] = textNode('settings:\n  allow-end: true\n', t)
    tree.plugins = dirNode(t, {
      'EssentialsX.jar': binNode(2 * MIB, t),
      Essentials: dirNode(t, { 'config.yml': textNode('ops-name-color: "4"\n', t) }),
    })
    tree.current = { type: 'symlink', mtime: t, size: 0, target: 'world' }
  }
  return dirNode(t, tree)
}
```

In `MockDb` ergänzen:

```ts
  files: Record<number, MockNode> // data directory per server id (missing = never started)
  fileJobs: Record<number, MockFileJob>
```

In `seed()` ergänzen (z. B. nach `servers`):

```ts
    files: { 1: mockTree(now, true), 2: mockTree(now, false) },
    fileJobs: {},
```

(`HOUR`, `DAY`, `MIB` existieren in `db.ts` bereits; falls `MIB` erst nach `mockTree` definiert ist, die Funktion unter die Konstanten setzen.)

`web/src/mocks/handlers/files.ts`:

```ts
import { http, HttpResponse } from 'msw'
import type { ExtractRequest, FileEntry, FileJob, RenameFileRequest, WriteFileRequest } from '@/api/types'
import { db, recordAudit, type MockNode, type MockServer } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

const JOB_MS = 6000
const TEXT_EXT = /\.(properties|ya?ml|json|toml|txt|cfg|conf|ini|log|md|sk|mcmeta)$/i
// eslint-disable-next-line no-control-regex -- matches control characters on purpose (filesapi.CleanPath)
const CONTROL_RE = /[\u0000-\u001f\u007f\\]/
const TOP_HIDDEN = new Set(['.rcon-cli.env', '.rcon-cli.yaml'])

type Params = Parameters<typeof idParam>[0]

/** Same rules as filesapi.CleanPath; null if invalid. "" = root. */
export function cleanPath(raw: unknown): string | null {
  if (raw === undefined || raw === null || raw === '' || raw === '.') return ''
  if (typeof raw !== 'string' || new TextEncoder().encode(raw).length > 4096 || CONTROL_RE.test(raw) || raw.startsWith('/')) return null
  const p = raw.endsWith('/') ? raw.slice(0, -1) : raw
  const parts = p.split('/')
  if (parts.some((c) => c === '' || c === '.' || c === '..' || new TextEncoder().encode(c).length > 255)) return null
  return p
}

function isHidden(p: string): boolean {
  if (p === '') return false
  const parts = p.split('/')
  return TOP_HIDDEN.has(parts[0]) || parts.some((c) => c.startsWith('.mcpanel-'))
}

/** Session, server.files and server lookup shared by every file operation. */
function access(request: Request, params: Params): { g: Authed; server: MockServer; root: MockNode | undefined } | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes('server.files')) return apiError(403, 'forbidden', 'missing permission server.files')
  const server = db.servers.find((s) => s.id === id)
  if (!server?.instance) return apiError(404, 'not_found', 'server not found')
  return { g, server, root: db.files[id] }
}

/** Resolves a cleaned path without following symlinks. */
function lookup(root: MockNode, p: string): MockNode | undefined {
  let node: MockNode | undefined = root
  for (const c of p === '' ? [] : p.split('/')) {
    if (node?.type !== 'dir') return undefined
    node = node.children?.[c]
  }
  return node
}

function split(p: string): { dir: string; name: string } {
  const i = p.lastIndexOf('/')
  return i < 0 ? { dir: '', name: p } : { dir: p.slice(0, i), name: p.slice(i + 1) }
}

function parentDir(root: MockNode, p: string): MockNode | undefined {
  const parent = lookup(root, split(p).dir)
  return parent?.type === 'dir' ? parent : undefined
}

function isText(name: string, node: MockNode): boolean {
  return node.content !== undefined || TEXT_EXT.test(name)
}

function noData(): ErrorResponse {
  return apiError(404, 'not_found', 'the server has never been started (no data yet)')
}

function invalid(): ErrorResponse {
  return apiError(400, 'validation_failed', 'invalid path')
}

function target(server: MockServer): string {
  return `server:${server.id}`
}

function entries(dir: MockNode): FileEntry[] {
  return Object.entries(dir.children ?? {})
    .filter(([name]) => !isHidden(name))
    .map(([name, n]) => ({ name, type: n.type, size: n.size, mtime: n.mtime }))
    .sort((a, b) => (a.type === 'dir') === (b.type === 'dir') ? a.name.localeCompare(b.name) : a.type === 'dir' ? -1 : 1)
}

/** Progress of a mock extract job: linear over JOB_MS, then done with the new folder created once. */
function jobNow(id: number): FileJob | null {
  const job = db.fileJobs[id]
  if (!job) return null
  const share = Math.min(1, (Date.now() - job.startedMs) / JOB_MS)
  if (job.state === 'running') {
    job.bytes = Math.round(job.total_bytes * share)
    job.entries = Math.round(job.total_entries * share)
    if (share >= 1) {
      job.state = 'done'
      job.finished_at = Date.now()
      const root = db.files[id]
      const parent = root && parentDir(root, job.dest)
      if (parent?.children) {
        parent.children[split(job.dest).name] = {
          type: 'dir',
          mtime: Date.now(),
          size: 0,
          children: { 'level.dat': { type: 'file', mtime: Date.now(), size: 4096 }, region: { type: 'dir', mtime: Date.now(), size: 0, children: {} } },
        }
      }
    }
  }
  const { startedMs: _started, ...wire } = job
  return wire
}

export const filesOperations: MockOperation[] = [
  op(
    'listFiles',
    http.get(`${API}/servers/:id/files`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath(new URL(request.url).searchParams.get('path'))
      if (p === null) return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return apiError(404, 'not_found', 'no such file')
      const node = lookup(a.root, p)
      if (!node) return apiError(404, 'not_found', 'no such directory')
      if (node.type !== 'dir') return apiError(400, 'not_regular', 'not a directory')
      return HttpResponse.json({ path: p, entries: entries(node), truncated: false })
    }),
  ),
  op(
    'readFile',
    http.get(`${API}/servers/:id/files/content`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath(new URL(request.url).searchParams.get('path'))
      if (p === null || p === '') return invalid()
      if (!a.root) return noData()
      const node = isHidden(p) ? undefined : lookup(a.root, p)
      if (!node) return apiError(404, 'not_found', 'no such file')
      if (node.type !== 'file') return apiError(400, 'not_regular', 'not a regular file')
      if (node.content === undefined) return apiError(415, 'not_text', 'not a text file')
      return HttpResponse.json({ path: p, content: node.content, mtime: node.mtime, size: node.size })
    }),
  ),
  op(
    'writeFile',
    http.put(`${API}/servers/:id/files/content`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const body = await readJson<WriteFileRequest>(request)
      const p = cleanPath(body?.path)
      if (!body || p === null || p === '' || typeof body.content !== 'string') return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return apiError(404, 'not_found', 'no such file')
      const parent = parentDir(a.root, p)
      if (!parent?.children) return apiError(404, 'not_found', 'no such directory')
      const { name } = split(p)
      const existing = parent.children[name]
      if (body.expected_mtime === null && existing) return apiError(409, 'file_exists', 'file exists')
      if (body.expected_mtime !== null) {
        if (!existing) return apiError(404, 'not_found', 'no such file')
        if (existing.type !== 'file') return apiError(400, 'not_regular', 'not a regular file')
        if (existing.mtime !== body.expected_mtime) {
          recordAudit(a.g.user, 'file.write', target(a.server), 'failure', { path: p })
          return apiError(409, 'file_modified', 'file changed since it was opened')
        }
      }
      const mtime = Math.max(Date.now(), (existing?.mtime ?? 0) + 1)
      const size = new TextEncoder().encode(body.content).length
      parent.children[name] = { type: 'file', mtime, size, content: body.content }
      recordAudit(a.g.user, 'file.write', target(a.server), 'success', { path: p })
      return HttpResponse.json({ mtime, size })
    }),
  ),
  op(
    'uploadFile',
    http.post(`${API}/servers/:id/files/upload`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const url = new URL(request.url)
      const p = cleanPath(url.searchParams.get('path'))
      if (p === null || p === '') return invalid()
      if (!a.root) return noData()
      if (isHidden(p)) return invalid()
      const parent = parentDir(a.root, p)
      if (!parent?.children) return apiError(404, 'not_found', 'no such directory')
      const { name } = split(p)
      const overwrite = url.searchParams.get('overwrite') === 'true'
      const existing = parent.children[name]
      if (existing && !overwrite) return apiError(409, 'file_exists', 'file exists')
      if (existing && existing.type !== 'file') return apiError(400, 'not_regular', 'not a regular file')
      const data = new Uint8Array(await request.arrayBuffer())
      parent.children[name] = TEXT_EXT.test(name)
        ? { type: 'file', mtime: Date.now(), size: data.length, content: new TextDecoder().decode(data) }
        : { type: 'file', mtime: Date.now(), size: data.length }
      recordAudit(a.g.user, 'file.upload', target(a.server), 'success', { path: p, size: data.length })
      return HttpResponse.json({ size: data.length }, { status: 201 })
    }),
  ),
  op(
    'downloadFile',
    http.get(`${API}/servers/:id/files/download`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath(new URL(request.url).searchParams.get('path'))
      if (p === null) return invalid()
      if (!a.root) return noData()
      const node = isHidden(p) ? undefined : lookup(a.root, p)
      if (!node) return apiError(404, 'not_found', 'no such file')
      if (node.type === 'symlink' || node.type === 'other') return apiError(400, 'not_regular', 'not a regular file')
      const base = p === '' ? a.server.name : split(p).name
      recordAudit(a.g.user, 'file.download', target(a.server), 'success', { path: p })
      if (node.type === 'dir') {
        const emptyZip = new Uint8Array([0x50, 0x4b, 0x05, 0x06, ...new Array<number>(18).fill(0)])
        return new HttpResponse(emptyZip, {
          headers: { 'Content-Type': 'application/zip', 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(`${base}.zip`)}` },
        })
      }
      const bytes = node.content !== undefined ? new TextEncoder().encode(node.content) : new Uint8Array(Math.min(node.size, 1024))
      return new HttpResponse(bytes, {
        headers: { 'Content-Type': 'application/octet-stream', 'Content-Disposition': `attachment; filename*=UTF-8''${encodeURIComponent(base)}` },
      })
    }),
  ),
  op(
    'makeDirectory',
    http.post(`${API}/servers/:id/files/mkdir`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath((await readJson<{ path?: string }>(request))?.path)
      if (p === null || p === '' || isHidden(p)) return invalid()
      if (!a.root) return noData()
      const parent = parentDir(a.root, p)
      if (!parent?.children) return apiError(404, 'not_found', 'no such directory')
      const { name } = split(p)
      if (parent.children[name]) return apiError(409, 'file_exists', 'exists')
      parent.children[name] = { type: 'dir', mtime: Date.now(), size: 0, children: {} }
      recordAudit(a.g.user, 'file.mkdir', target(a.server), 'success', { path: p })
      return new HttpResponse(null, { status: 201 })
    }),
  ),
  op(
    'renameFile',
    http.post(`${API}/servers/:id/files/rename`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const body = await readJson<RenameFileRequest>(request)
      const from = cleanPath(body?.from)
      const to = cleanPath(body?.to)
      if (from === null || to === null || from === '' || to === '' || isHidden(from) || isHidden(to)) return invalid()
      if (to === from || to.startsWith(`${from}/`)) return apiError(400, 'validation_failed', 'cannot move a folder into itself')
      if (!a.root) return noData()
      const src = parentDir(a.root, from)
      const dst = parentDir(a.root, to)
      const node = src?.children?.[split(from).name]
      if (!node || !dst?.children) return apiError(404, 'not_found', 'no such file')
      if (dst.children[split(to).name]) return apiError(409, 'file_exists', 'exists')
      delete src!.children![split(from).name]
      dst.children[split(to).name] = node
      recordAudit(a.g.user, 'file.rename', target(a.server), 'success', { from, to })
      return noContent()
    }),
  ),
  op(
    'deleteFile',
    http.post(`${API}/servers/:id/files/delete`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const p = cleanPath((await readJson<{ path?: string }>(request))?.path)
      if (p === null || p === '' || isHidden(p)) return invalid()
      if (!a.root) return noData()
      const parent = parentDir(a.root, p)
      const { name } = split(p)
      if (!parent?.children?.[name]) return apiError(404, 'not_found', 'no such file')
      delete parent.children[name]
      recordAudit(a.g.user, 'file.delete', target(a.server), 'success', { path: p })
      return noContent()
    }),
  ),
  op(
    'extractArchive',
    http.post(`${API}/servers/:id/files/extract`, async ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      const body = await readJson<ExtractRequest>(request)
      const p = cleanPath(body?.path)
      const dest = cleanPath(body?.dest)
      if (p === null || dest === null || p === '' || dest === '' || isHidden(p) || isHidden(dest)) return invalid()
      if (!a.root) return noData()
      if (a.server.status.state !== 'stopped') return apiError(409, 'instance_running', 'stop the server first')
      const zip = lookup(a.root, p)
      if (!zip) return apiError(404, 'not_found', 'no such file')
      if (zip.type !== 'file' || !p.toLowerCase().endsWith('.zip')) return apiError(400, 'not_regular', 'not a zip file')
      const existing = lookup(a.root, dest)
      if (existing && (existing.type !== 'dir' || Object.keys(existing.children ?? {}).length > 0)) {
        return apiError(409, 'file_exists', 'destination is not empty')
      }
      if (!parentDir(a.root, dest)) return apiError(404, 'not_found', 'no such directory')
      if (db.fileJobs[a.server.id]?.state === 'running') return apiError(429, 'rate_limited', 'an extract job is running')
      const now = Date.now()
      db.fileJobs[a.server.id] = {
        id: `job-${now}`,
        state: 'running',
        path: p,
        dest,
        bytes: 0,
        entries: 0,
        total_bytes: zip.size * 3,
        total_entries: 1200,
        error: '',
        started_at: now,
        finished_at: 0,
        startedMs: now,
      }
      recordAudit(a.g.user, 'file.extract', target(a.server), 'success', { path: p, dest })
      return HttpResponse.json(jobNow(a.server.id), { status: 202 })
    }),
  ),
  op(
    'getFileJob',
    http.get(`${API}/servers/:id/files/jobs`, ({ request, params }) => {
      const a = access(request, params)
      if (a instanceof Response) return a
      return HttpResponse.json({ job: jobNow(a.server.id) })
    }),
  ),
]
```

Anmerkung zum Status: Der Mock prüft `status.state !== 'stopped'`; prüfe die tatsächlichen Werte von `ServerState` in `schema.d.ts` (`stoppedStatus()` in `db.ts`) und passe die Bedingung an, falls gestoppte Server dort anders heißen.

`web/src/mocks/handlers.ts`: `import { filesOperations } from './handlers/files'` und `...filesOperations,` nach `...consoleOperations,`.

- [ ] **Step 10: `make check`, Commit, Push, CI**

```bash
make check
git add api/openapi.yaml web/src/api internal/httpx internal/audit/types.go internal/serverfiles cmd/panel web/src/i18n web/src/mocks
git commit -m "feat(serverfiles): panel contract for the file browser, stub routes and mocks

OpenAPI operations and error codes of sub-project 3b, audit actions, a body-limit
exception for the upload stream, the serverfiles package with stub handlers
wired into the panel, TypeScript types and MSW mocks with a data tree per server.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks <pr> --watch --interval 30
```

Expected: alle Jobs grün – insbesondere `TestRoutesMatchOpenAPI`, `TestErrorCodesMatchOpenAPI`, Routen-Zähler 76, `TestRouteContract`/`TestUnlimitedBody`/`TestStubAnswersFilesUnavailable` in `internal/serverfiles`, `TestBodyLimitExcept`, die Vitest-Fälle in `handlers.test.ts` (76 Operationen, Datei-Verhalten) und `i18n.test.ts`; OpenAPI-Lint grün.

Danach den PR auf „ready“ setzen lassen (Lead) – nicht selbst mergen.

## Self-Review (dieser Datei)

- Spec §3.3 (`instances/` `root:mcpanel-mc 0750`, `deleted/` `0700`) → Aufgabe 1; `CheckInstancesDir` bleibt (Abweichung 5), Test hält Gruppen-Lesen fest.
- Spec §3.1 (`files.toml`, `files_socket`), §3.4 (Pfadregeln, Verborgen) → Aufgabe 2; Schwärzen ist Sache des Dienstes (B1), hier nur Vertrag.
- Spec §4 (Dienst-API: Pfade, Codes, Typen) → Aufgabe 2 (`filesapi`); §5.1–5.3 (panel-API, Codes, Audit) → Aufgabe 3; §6.5 (Mocks) → Aufgabe 3.
- V1–V5 der Übersicht: alle Namen und Typen übernommen; Abweichungen 1–4 oben begründet.
- Routen-Zähler (`cmd/panel/integration_test.go`) und Mock-Zähler (`handlers.test.ts`) auf 76 gesetzt; `TestRoutesMatchOpenAPI` und `TestErrorCodesMatchOpenAPI` decken Routen und Enum ab.
- Phase A berührt keine Dateien von B1 (`cmd/files`, `internal/files` außer `filesapi`), B3 (`web/src/features/**`) oder C.
