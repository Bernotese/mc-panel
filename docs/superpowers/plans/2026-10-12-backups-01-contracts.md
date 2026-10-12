# mc-panel Teilprojekt 4 – Phase A: Verträge (Aufgaben 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die gemeinsamen Grundlagen für B1, B2 und B3 auf `main` bringen:
- `filesapi` kennt die Backup-Endpunkte des Dienstes: Typen, `BackupClient`, `*UnixClient`-Methoden und den Code `unavailable`. `Job` trägt `kind`, `snapshot` und `skipped`.
- Die Extract-Sperre heißt `servers.DataGate`. Sie hält einen Start nur noch bei Jobs, die das Datenverzeichnis umschreiben (Entpacken, Restore), nicht bei einem Backup.
- Daten und Querschnitt stehen: Migration 00006, `Backups…`-Queries, die zwei panel-Fehlercodes, sieben Audit-Aktionen und `backup.failed`.
- `servers` bekommt die Backup-Zeitzone in den Einstellungen und `purge_backups` beim Löschen.
- Der panel-Vertrag steht: OpenAPI mit acht Operationen, TS-Typen, das Stub-Paket `internal/backups` (verdrahtet in `cmd/panel`), MSW-Mocks.
- restic läuft für die Tests auf der Test-VM und in der CI.

**Architecture:** Drei Aufgaben auf einem Branch `feat/backups-contracts` (Teammate „contracts“, nacheinander).

| Aufgabe | Inhalt |
|---|---|
| 1 | `internal/files/filesapi/**`, Job-Art-Filter und Umbenennung der Sperre in `internal/serverfiles` und `internal/servers`. Beides muss in einem Commit landen, sonst kompiliert `cmd/panel` nicht. |
| 2 | Migration, Queries, Codes, Audit, Notify, die zwei `servers`-Erweiterungen samt OpenAPI-Teil und Mocks für Einstellungen und Löschen |
| 3 | Backup-Operationen in OpenAPI, Stub-Handler, Verdrahtung, Mocks, restic-Installation |

**Tech Stack:** Go (Standardbibliothek, chi, sqlc 1.31.1, goose, `time/tzdata`), OpenAPI 3.1 + `openapi-typescript`, MSW 2, Vitest, Bash (vm-test), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-12-backups-00-overview.md` (V1–V6; „Ausführung“, „Global Constraints“ und „Review Focus“ gelten hier vollständig).

## Ausführung dieser Phase

- **Worktree:** `git fetch origin && git worktree add ../mc-panel-wt/feat-backups-contracts -b feat/backups-contracts origin/main`, dann alle Befehle in `../mc-panel-wt/feat-backups-contracts` ausführen (einmalig `(cd web && npm ci)`). Ein Draft-PR für alle drei Aufgaben, ein Commit pro Aufgabe (Fix-Commits erlaubt).
- **Lokal nur `make check`** und `make generate` (Aufgaben 2 und 3).
  - Meldet `make check` `gofmt needed`: `gofmt -w <datei>` (Spaltenausrichtung), dann erneut prüfen.
  - Neue Dateien vor `make check` mit `git add -N <pfad>` vormerken, sonst prüft gofmt sie nicht.
- **„Run: go test …“ / „npx vitest …“** heißt: auf der Test-VM ausführen.
  - Ganze Suite: `make test-vm SUITE=go` bzw. `SUITE=web`.
  - Einzeltests: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-contracts && PATH=/usr/local/go/bin:$PATH go test ./internal/… -run …'`, für Vitest `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-contracts/web && PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run …'`.
  - Für SSH muss die Bash ohne Sandbox laufen.
  - Vor jedem Push mindestens die betroffenen Suites, vor „ready for review“ `make test-vm` (alle).
- **Fertig:** Eine Aufgabe ist fertig, wenn VM-Lauf und CI ihres Commits grün sind (`gh pr checks <pr> --watch --interval 30`, Fehler über `gh run view <id> --log-failed`). Danach meldet der Teammate dem Lead den PR. Der Nutzer mergt, erst dann starten B1, B2 und B3.
- **Dateien:** Phase A ändert ausschließlich die in den „Files“-Blöcken genannten Dateien.

## Nachträge des Leads (nach dem Schnittstellen-Review, dem Teammate per Nachricht geschickt)

Diese Punkte gehen den Code-Schnipseln unten vor (die Übersicht ist nachgezogen):

1. `filesapi.Job` bekommt zusätzlich `Code string \`json:"code"\`` – Wire-Code des Fehlers eines `failed`-Jobs ("" sonst); der Dienst füllt ihn ab Phase B1 (V1).
2. `DELETE /servers/{id}?purge_backups=1`: Purge nach dem Commit mit `context.WithoutCancel(r.Context())` und Frist `filesapi.DeleteTimeout + time.Minute`; vorher Schreibfrist per `http.NewResponseController(w).SetWriteDeadline(now + Frist + 10 s)`; Test dafür (V2).
3. OpenAPI `createBackup` `409 conflict` = „server has no instance yet“.
4. Mock: `interval`-`next_run_at` wie das panel (`last_run_at + N h`, sonst `jetzt + N h`); Zip-Dateiname in der panel-Zeitzone; Audit-Details wie das panel (create `{note, snapshot}`, restore `{snapshot, time, kind}`, pin/unpin `{snapshot, new_snapshot}`, schedule_update `{from, to}`) (V6).

## Abweichungen von der Übersicht (bewusst, vom Lead zu bestätigen)

1. **`servers.DataGate` schon in Aufgabe 1** (V2 ordnet es Aufgabe 2 zu). `serverfiles.Handler` implementiert die Sperre über seine Methode, und `cmd/panel/app.go` übergibt `filesH` als Sperre. Methode und Interface müssen deshalb im selben Commit umbenannt werden. `BackupPurger` und `purge_backups` bleiben in Aufgabe 2.
2. **Zusätzliche, rein additive `filesapi`-Helfer:**
   - Konstanten `MaxKeep = 1000` und `MaxTimezoneLen = 64`
   - `ValidTimezone(tz string) bool`: IANA-Name per `time.LoadLocation`, nicht leer, nicht `Local`, ≤ 64 Bytes
   - `(CreateBackupRequest).Validate()`
   - Feld `backupTimeout` in `UnixClient` mit `SetBackupTimeout` in `export_test.go`
   - Hilfsfunktion `openDownload` (gemeinsamer Teil von `Download` und `DownloadBackup`)

   Panel (`servers`-Einstellungen, B2) und Dienst (B1) prüfen Zeitzonen damit nach derselben Regel, wie bei `CleanPath`.
3. **`ForgetBackups` nutzt `BackupCallTimeout`** statt `CallTimeout` (V1). Der Dienst prüft vor dem Job synchron, ob jede ID im Repo vorkommt (V5), und muss dafür restics Index lesen. Das dauert länger als 10 s.
4. **`GET /servers/{id}/files/jobs` und die 202-Antwort von `extract` zeigen nur Extract-Jobs**, und zwar in der bisherigen Form `FileJob` (ohne `kind`, `snapshot`, `skipped`). Ein Backup- oder Restore-Job desselben Servers erscheint im Dateien-Tab als `job: null`. Ohne diese Änderung zeigte der Dateien-Tab nach einem Backup einen „Entpack-Job“ ohne Pfad an.
5. **Audit-Detail der Zeitzone** ist `backup_timezone: {old, new}` statt `{from, to}` (V2). So passt es zu `base_domain: {old, new}` im selben Eintrag `server.settings_update`.
6. **Reihenfolge in `AnyGate`:** `servers.AnyGate(backupsH, filesH)` statt `(filesH, backupsH)` (V4). Die panel-eigene Sperre ist eine Map-Abfrage, die des Dateidienstes ein Socket-Aufruf mit bis zu 2 s Frist. Die billige Prüfung kommt zuerst; das Ergebnis ist dasselbe.
7. **OpenAPI:**
   - `'off'` steht in Anführungszeichen, damit YAML-1.1-Parser es nicht als `false` lesen.
   - `deleteServer` deklariert zusätzlich `400` (ungültiges `purge_backups`).
   - Die Beschreibung der Power-`409` nennt den Restore.
8. **Gemeinsames Skript `scripts/install-restic.sh`** (Vorgabe des Leads): installiert restic 0.19.1 idempotent nach `/usr/local/bin/restic`, mit SHA-256-Prüfung. `scripts/vm-test.sh` (per `sudo`) und der Go-Job der CI rufen es auf; Phase C nutzt es für den Instanzen-Job und `scripts/testvm-deploy.sh`. `MCPANEL_TEST_RESTIC=/usr/local/bin/restic` überall.
9. **Abhängige Dateien:**
   - **`web/src/features/settings/settings-page.test.tsx`** (sonst B3): eine typisierte `ServerSettings`-Vorlage bekommt `backup_timezone`, sonst schlägt `tsc` fehl. Nur diese Zeile.
   - **`delete-server-section.tsx`** bleibt unverändert; die Checkbox baut B3.
10. **`Kind: JobExtract` im Dienst setzt erst B1** (`internal/files/**` gehört B1). Bis dahin meldet der Dienst Extract-Jobs mit `kind: ""`, und das zählt laut V1 als Extract. Phase A setzt `Kind` nur in den Fakes.
11. **Löschen mit `purge_backups`:** Das Löschen der Backups läuft nach dem Commit **außerhalb** der Abgleich-Sperre. Ein großes Repo zu löschen dauert Minuten, und solange stünde sonst der Abgleich aller Server. Dafür wird der gesperrte Teil von `handleDeleteServer` in `deleteServerRows` ausgelagert (unverändertes Verhalten).
12. **Einstellungen und Löschen im OpenAPI und im Mock schon in Aufgabe 2:** `backup_timezone` in `ServerSettings`/`UpdateServerSettingsRequest` sowie `purge_backups` bei `deleteServer` kommen mit derselben Aufgabe, die das Go-Verhalten ändert. Die acht Backup-Operationen bleiben in Aufgabe 3.

---

### Task 1: `filesapi` – Backups, Job-Felder, `unavailable`; Sperre → `DataGate`

**Files:**
- Create: `internal/files/filesapi/backups.go` (Konstanten, Typen, Validierung, `BackupClient`)
- Create: `internal/files/filesapi/backups_client.go` (`*UnixClient`-Methoden)
- Modify: `internal/files/filesapi/types.go` (`Job`: `Kind`, `Snapshot`, `Skipped`)
- Modify: `internal/files/filesapi/errors.go` (`CodeUnavailable`, `ErrBackupsUnavailable`, Tabelle `codes`)
- Modify: `internal/files/filesapi/client.go` (Feld `backupTimeout`, `NewClient`, `Download` → `openDownload`)
- Modify: `internal/files/filesapi/export_test.go` (`SetBackupTimeout`)
- Modify: `internal/files/filesapi/errors_test.go`
- Create: `internal/files/filesapi/backups_test.go`
- Modify: `internal/serverfiles/extract.go` (`DataJobRunning`, Job-Art-Filter, `fileJobJSON`)
- Modify: `internal/serverfiles/extract_test.go` (Umbenennung, zwei neue Tests)
- Modify: `internal/serverfiles/helpers_test.go` (Fake-`Extract` setzt `Kind`)
- Modify: `internal/servers/gate.go` (`DataGate`, `noGate`, `AnyGate`)
- Modify: `internal/servers/handler.go` (`Deps.Gate`, Feld `gate`)
- Modify: `internal/servers/power.go:45-48`, `internal/servers/reconcile.go:161-165`
- Modify: `internal/servers/helpers_test.go` (`Gate: e.gate`, `fakeGate.DataJobRunning`)
- Create: `internal/servers/gate_test.go` (`TestAnyGate`)
- Modify: `cmd/panel/app.go` (`Gate: filesH`)
- Modify: `cmd/panel/fakefiles_test.go` (Fake-`Extract` setzt `Kind`)
- Modify: `api/openapi.yaml` (nur die Beschreibung der `409` von `serverPower`)

**Interfaces:**
- Consumes: vorhandenes `filesapi` (`checkID`, `call`, `decode`, `cancelBody`, `StatusError`, `codes`, `HealthTimeout`, `CallTimeout`, `DeleteTimeout`, `DownloadHeaderTimeout`).
- Produces: V1 vollständig plus Abweichung 2:

```go
// filesapi
const (
	PathBackupsStatus, PathBackupsList, PathBackupsCreate, PathBackupsRestore, PathBackupsForget,
	PathBackupsPin, PathBackupsDownload, PathBackupsPurge, PathJobsCancel string // V1
	MinResticVersion = "0.17.0"
	MaxNoteRunes = 64
	MaxForgetIDs = 100
	MaxKeep = 1000
	MaxTimezoneLen = 64
	BackupCallTimeout = 2 * time.Minute
	CodeUnavailable = "unavailable" // 503
)
var ErrBackupsUnavailable = errors.New(CodeUnavailable)
type JobKind string        // JobExtract, JobBackup, JobRestore, JobForget
type SnapshotKind string   // SnapshotScheduled, SnapshotManual, SnapshotPreRestore; (SnapshotKind).Valid() bool
type Snapshot, BackupsStatus, IDRequest, CreateBackupRequest, SnapshotRequest, ForgetPolicy, ForgetRequest,
	PinRequest, SnapshotsResponse, SnapshotResponse struct // V1
func (p ForgetPolicy) Validate() error
func (r ForgetRequest) Validate() error
func (r CreateBackupRequest) Validate() error
func ValidSnapshotID(s string) bool
func ValidNote(s string) bool
func ValidTimezone(tz string) bool
type BackupClient interface { /* V1, 10 methods incl. Job */ }
// Job gains: Kind JobKind `json:"kind"`, Snapshot string `json:"snapshot"`, Skipped int64 `json:"skipped"`

// servers
type DataGate interface{ DataJobRunning(ctx context.Context, serverID int64) bool }
func AnyGate(gates ...DataGate) DataGate
// Deps.Extract -> Deps.Gate DataGate

// serverfiles
func (h *Handler) DataJobRunning(ctx context.Context, serverID int64) bool // replaces ExtractRunning
```

- [ ] **Step 1: Fehlertests erweitern (rot)**

In `internal/files/filesapi/errors_test.go` in `TestCodesRoundTrip` die Map um einen Eintrag ergänzen (nach `filesapi.CodeHashMismatch`):

```go
		filesapi.CodeUnavailable:         http.StatusServiceUnavailable,
```

In `TestStatusErrorIs` die `cases` um einen Fall ergänzen:

```go
		{&filesapi.StatusError{Status: 503, Code: "unavailable"}, filesapi.ErrBackupsUnavailable},
```

und am Ende der Funktion anhängen:

```go
	// "restic is missing" is not "the service is unreachable": the panel answers
	// backups_unavailable for the first and files_unavailable for the second.
	if errors.Is(&filesapi.StatusError{Status: 503, Code: "unavailable"}, filesapi.ErrUnavailable) {
		t.Fatal("code unavailable must not match ErrUnavailable")
	}
	if errors.Is(&filesapi.StatusError{Status: 503}, filesapi.ErrBackupsUnavailable) {
		t.Fatal("a 503 without code must not match ErrBackupsUnavailable")
	}
```

- [ ] **Step 2: Typ-, Validierungs- und Client-Tests schreiben (rot)**

`internal/files/filesapi/export_test.go` – am Ende anfügen:

```go
// SetBackupTimeout sets the timeout of list, pin, restore and forget.
func SetBackupTimeout(c *UnixClient, d time.Duration) { c.backupTimeout = d }
```

Neue Datei `internal/files/filesapi/backups_test.go` (nutzt `serve` und `writeJSON` aus `client_test.go`):

```go
package filesapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	_ "time/tzdata" // ValidTimezone must not depend on the host's zoneinfo

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

var (
	snapA = strings.Repeat("a1", 32)
	snapB = strings.Repeat("b2", 32)
)

func TestValidSnapshotID(t *testing.T) {
	for _, ok := range []string{snapA, strings.Repeat("0", 64), "0123456789abcdef" + strings.Repeat("f", 48)} {
		if !filesapi.ValidSnapshotID(ok) {
			t.Errorf("ValidSnapshotID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", snapA[:63], snapA + "0", strings.ToUpper(snapA), strings.Repeat("g", 64),
		snapA[:63] + " ", "../" + snapA[3:], snapA[:8]} {
		if filesapi.ValidSnapshotID(bad) {
			t.Errorf("ValidSnapshotID(%q) = true", bad)
		}
	}
}

func TestValidNote(t *testing.T) {
	for _, ok := range []string{"", "vor Update auf 1.21.11", strings.Repeat("ä", 64), "a b", " leading"} {
		if !filesapi.ValidNote(ok) {
			t.Errorf("ValidNote(%q) = false", ok)
		}
	}
	for _, bad := range []string{strings.Repeat("x", 65), "line\nbreak", "tab\there", "del\x7f", "   ", "\xff\xfe", "nul\x00"} {
		if filesapi.ValidNote(bad) {
			t.Errorf("ValidNote(%q) = true", bad)
		}
	}
}

func TestValidTimezone(t *testing.T) {
	for _, ok := range []string{"Europe/Berlin", "UTC", "America/New_York", "Asia/Kolkata"} {
		if !filesapi.ValidTimezone(ok) {
			t.Errorf("ValidTimezone(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "Local", "Mars/Olympus", "../etc/passwd", "/etc/localtime", "Europe/Berlin\n", strings.Repeat("A", 65)} {
		if filesapi.ValidTimezone(bad) {
			t.Errorf("ValidTimezone(%q) = true", bad)
		}
	}
}

func TestSnapshotKindValid(t *testing.T) {
	for _, k := range []filesapi.SnapshotKind{filesapi.SnapshotScheduled, filesapi.SnapshotManual, filesapi.SnapshotPreRestore} {
		if !k.Valid() {
			t.Errorf("%q invalid", k)
		}
	}
	for _, k := range []filesapi.SnapshotKind{"", "weekly", "Manual", "pre_restore"} {
		if k.Valid() {
			t.Errorf("%q valid", k)
		}
	}
}

func TestForgetPolicyValidate(t *testing.T) {
	for _, ok := range []filesapi.ForgetPolicy{{KeepLast: 1}, {KeepMonthly: 1000}, {KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []filesapi.ForgetPolicy{{}, {KeepLast: -1, KeepDaily: 1}, {KeepWeekly: 1001}} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestForgetRequestValidate(t *testing.T) {
	pol := &filesapi.ForgetPolicy{KeepLast: 3}
	tooMany := make([]string, filesapi.MaxForgetIDs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("%064x", i)
	}
	for _, ok := range []filesapi.ForgetRequest{
		{ID: 1, Snapshots: []string{snapA}},
		{ID: 1, Snapshots: []string{snapA, snapB}},
		{ID: 1, Snapshots: tooMany[:filesapi.MaxForgetIDs]},
		{ID: 1, Policy: pol, Timezone: "Europe/Berlin"},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for i, bad := range []filesapi.ForgetRequest{
		{ID: 0, Snapshots: []string{snapA}},
		{ID: 1},
		{ID: 1, Snapshots: []string{snapA}, Policy: pol, Timezone: "UTC"},
		{ID: 1, Snapshots: []string{snapA, snapA}},
		{ID: 1, Snapshots: []string{"nope"}},
		{ID: 1, Snapshots: []string{snapA}, Timezone: "UTC"},
		{ID: 1, Snapshots: tooMany},
		{ID: 1, Policy: pol},
		{ID: 1, Policy: pol, Timezone: "Mars/Olympus"},
		{ID: 1, Policy: &filesapi.ForgetPolicy{}, Timezone: "UTC"},
	} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("case %d: Validate(%+v) = %v, want ErrInvalid", i, bad, err)
		}
	}
}

func TestCreateBackupRequestValidate(t *testing.T) {
	for _, ok := range []filesapi.CreateBackupRequest{
		{ID: 1, Kind: filesapi.SnapshotManual, Note: "vor Update"},
		{ID: 1, Kind: filesapi.SnapshotManual},
		{ID: 1, Kind: filesapi.SnapshotScheduled},
		{ID: 1, Kind: filesapi.SnapshotPreRestore},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v", ok, err)
		}
	}
	for _, bad := range []filesapi.CreateBackupRequest{
		{ID: 0, Kind: filesapi.SnapshotManual},
		{ID: 1, Kind: "weekly"},
		{ID: 1, Kind: filesapi.SnapshotScheduled, Note: "only manual snapshots have notes"},
		{ID: 1, Kind: filesapi.SnapshotManual, Note: "a\nb"},
	} {
		if err := bad.Validate(); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("Validate(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestJobCarriesKindSnapshotAndSkipped(t *testing.T) {
	b, err := json.Marshal(filesapi.Job{ID: "j1", Kind: filesapi.JobBackup, State: filesapi.JobDone, Snapshot: snapA, Skipped: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"backup"`, `"snapshot":"` + snapA + `"`, `"skipped":2`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %s", b, want)
		}
	}
	var old filesapi.Job // an extract job of a service before sub-project 4
	if err := json.Unmarshal([]byte(`{"id":"j","state":"running","path":"w.zip","dest":"w"}`), &old); err != nil || old.Kind != "" {
		t.Fatalf("old job = %+v, %v", old, err)
	}
}

// recorder notes "METHOD URI BODY" of every request (the handler runs on another goroutine).
type recorder struct {
	mu   sync.Mutex
	reqs []string
}

func (rc *recorder) add(r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.reqs = append(rc.reqs, r.Method+" "+r.URL.RequestURI()+" "+strings.TrimSpace(string(body)))
}

func (rc *recorder) all() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return append([]string(nil), rc.reqs...)
}

// backupService answers every backup endpoint like the service would.
func backupService(t *testing.T, rc *recorder) *filesapi.UnixClient {
	return serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.add(r)
		switch r.URL.Path {
		case filesapi.PathBackupsStatus:
			writeJSON(w, 200, filesapi.BackupsStatus{Available: true, Version: "0.19.1"})
		case filesapi.PathBackupsList:
			writeJSON(w, 200, filesapi.SnapshotsResponse{Snapshots: []filesapi.Snapshot{
				{ID: snapA, ShortID: snapA[:8], Time: 5, Kind: filesapi.SnapshotManual, Note: "n", Bytes: 9},
			}})
		case filesapi.PathBackupsCreate, filesapi.PathBackupsRestore, filesapi.PathBackupsForget:
			writeJSON(w, 202, filesapi.Job{ID: "j1", Kind: filesapi.JobBackup, State: filesapi.JobRunning})
		case filesapi.PathBackupsPin:
			writeJSON(w, 200, filesapi.SnapshotResponse{Snapshot: filesapi.Snapshot{ID: snapB, Kind: filesapi.SnapshotScheduled, Pinned: true}})
		case filesapi.PathBackupsPurge, filesapi.PathJobsCancel:
			w.WriteHeader(http.StatusNoContent)
		case filesapi.PathBackupsDownload:
			w.Header().Set(filesapi.HeaderKind, "dir")
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("PK"))
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	}))
}

func TestClientBackupCalls(t *testing.T) {
	rc := &recorder{}
	c := backupService(t, rc)
	ctx := context.Background()

	if st, err := c.BackupsStatus(ctx); err != nil || !st.Available || st.Version != "0.19.1" {
		t.Fatalf("BackupsStatus = %+v, %v", st, err)
	}
	if list, err := c.Snapshots(ctx, 7); err != nil || len(list) != 1 || list[0].ID != snapA || list[0].Note != "n" {
		t.Fatalf("Snapshots = %+v, %v", list, err)
	}
	if j, err := c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 7, Kind: filesapi.SnapshotManual, Note: "vor Update"}); err != nil || j.ID != "j1" {
		t.Fatalf("CreateBackup = %+v, %v", j, err)
	}
	if _, err := c.RestoreBackup(ctx, 7, snapA); err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	pol := &filesapi.ForgetPolicy{KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6}
	if _, err := c.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 7, Policy: pol, Timezone: "Europe/Berlin"}); err != nil {
		t.Fatalf("ForgetBackups(policy): %v", err)
	}
	if _, err := c.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 7, Snapshots: []string{snapA}}); err != nil {
		t.Fatalf("ForgetBackups(ids): %v", err)
	}
	if s, err := c.PinBackup(ctx, 7, snapA, true); err != nil || s.ID != snapB || !s.Pinned {
		t.Fatalf("PinBackup = %+v, %v", s, err)
	}
	d, err := c.DownloadBackup(ctx, 7, snapA)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if string(body) != "PK" || !d.IsDir || d.Size != -1 || d.Name != "" {
		t.Fatalf("DownloadBackup = %+v %q", d, body)
	}
	if err := c.PurgeBackups(ctx, 7); err != nil {
		t.Fatalf("PurgeBackups: %v", err)
	}
	if err := c.CancelJob(ctx, 7); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	want := []string{
		"GET /v1/backups/status ",
		`POST /v1/backups/list {"id":7}`,
		`POST /v1/backups/create {"id":7,"kind":"manual","note":"vor Update"}`,
		`POST /v1/backups/restore {"id":7,"snapshot":"` + snapA + `"}`,
		`POST /v1/backups/forget {"id":7,"policy":{"keep_last":24,"keep_daily":7,"keep_weekly":4,"keep_monthly":6},"timezone":"Europe/Berlin"}`,
		`POST /v1/backups/forget {"id":7,"snapshots":["` + snapA + `"]}`,
		`POST /v1/backups/pin {"id":7,"snapshot":"` + snapA + `","pinned":true}`,
		"GET /v1/backups/download?id=7&snapshot=" + snapA + " ",
		`POST /v1/backups/purge {"id":7}`,
		`POST /v1/jobs/cancel {"id":7}`,
	}
	got := rc.all()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestClientBackupsRejectBadInputWithoutRequest(t *testing.T) {
	var calls atomic.Int32
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx := context.Background()
	errs := map[string]error{}
	_, errs["snapshots id 0"] = c.Snapshots(ctx, 0)
	_, errs["create kind"] = c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: "weekly"})
	_, errs["create note on scheduled"] = c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled, Note: "x"})
	_, errs["restore snapshot"] = c.RestoreBackup(ctx, 1, "abc")
	_, errs["forget empty"] = c.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1})
	_, errs["pin upper case"] = c.PinBackup(ctx, 1, strings.ToUpper(snapA), true)
	_, errs["download snapshot"] = c.DownloadBackup(ctx, 1, "x")
	errs["purge id 0"] = c.PurgeBackups(ctx, 0)
	errs["cancel id -1"] = c.CancelJob(ctx, -1)
	for name, err := range errs {
		if !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d requests sent for invalid input", n)
	}
}

func TestClientBackupsUnavailable(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, filesapi.ErrorResponse{Error: "restic not found", Code: filesapi.CodeUnavailable})
	}))
	_, err := c.Snapshots(context.Background(), 1)
	if !errors.Is(err, filesapi.ErrBackupsUnavailable) || errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("Snapshots = %v, want ErrBackupsUnavailable only", err)
	}
}

func TestClientSnapshotsNeverNil(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"snapshots":null}`))
	}))
	list, err := c.Snapshots(context.Background(), 1)
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("Snapshots = %#v, %v; want an empty, non-nil list", list, err)
	}
}

// A service that answers a pin without a valid snapshot ID is broken: the panel would
// otherwise lose track of the snapshot (restic gives it a new ID).
func TestClientPinNeedsANewSnapshotID(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, filesapi.SnapshotResponse{})
	}))
	if _, err := c.PinBackup(context.Background(), 1, snapA, true); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("PinBackup = %v, want ErrUnavailable", err)
	}
}

func TestClientDownloadBackupNeedsAZip(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(filesapi.HeaderKind, "file")
		_, _ = w.Write([]byte("data"))
	}))
	if _, err := c.DownloadBackup(context.Background(), 1, snapA); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("DownloadBackup = %v, want ErrUnavailable", err)
	}
}

// List, pin, restore and forget read restic's index and get BackupCallTimeout, not the 10 s of
// the small calls.
func TestClientBackupCallsHaveTheirOwnTimeout(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		writeJSON(w, 200, filesapi.SnapshotsResponse{Snapshots: []filesapi.Snapshot{}})
	}))
	filesapi.SetCallTimeouts(c, 50*time.Millisecond, 50*time.Millisecond)
	filesapi.SetBackupTimeout(c, 5*time.Second)
	if _, err := c.Snapshots(context.Background(), 1); err != nil {
		t.Fatalf("Snapshots with a long backup timeout: %v", err)
	}
	filesapi.SetBackupTimeout(c, 50*time.Millisecond)
	if _, err := c.Snapshots(context.Background(), 1); !errors.Is(err, filesapi.ErrUnavailable) {
		t.Fatalf("Snapshots past the backup timeout = %v, want ErrUnavailable", err)
	}
}
```

- [ ] **Step 3: Sperren-Tests schreiben (rot)**

In `internal/serverfiles/extract_test.go` alle Aufrufe `e.h.ExtractRunning(` durch `e.h.DataJobRunning(` ersetzen (`sed -i '' 's/e\.h\.ExtractRunning(/e.h.DataJobRunning(/g' internal/serverfiles/extract_test.go`) und die Testnamen `TestExtractRunning` → `TestDataJobRunning`, `TestExtractRunningLogsUnreachableService` → `TestDataJobRunningLogsUnreachableService` umbenennen. Dann am Ende anfügen:

```go
// Backups spec §5.5: an extract and a restore rewrite the data directory and hold starts; a
// backup only reads it (the server keeps running with save-off), a forget does not touch it.
func TestDataJobRunningOnlyForJobsThatRewriteData(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	ctx := context.Background()
	for kind, want := range map[filesapi.JobKind]bool{
		"":                  true, // an extract job of a service before sub-project 4
		filesapi.JobExtract: true,
		filesapi.JobRestore: true,
		filesapi.JobBackup:  false,
		filesapi.JobForget:  false,
	} {
		e.files.setJob(&filesapi.Job{ID: "j1", Kind: kind, State: filesapi.JobRunning})
		if got := e.h.DataJobRunning(ctx, id); got != want {
			t.Errorf("running %q job: DataJobRunning = %v, want %v", kind, got, want)
		}
		e.files.setJob(&filesapi.Job{ID: "j1", Kind: kind, State: filesapi.JobDone})
		if e.h.DataJobRunning(ctx, id) {
			t.Errorf("finished %q job holds starts", kind)
		}
	}
}

// The file browser shows only extract jobs, in the FileJob shape of the contract.
func TestFileJobsShowOnlyExtractJobs(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, kind := range []filesapi.JobKind{filesapi.JobBackup, filesapi.JobRestore, filesapi.JobForget} {
		e.files.setJob(&filesapi.Job{ID: "j1", Kind: kind, State: filesapi.JobRunning, Snapshot: strings.Repeat("a", 64)})
		rec := e.do(http.MethodGet, srvPath(id, "/files/jobs"), nil, &admin)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"job":null}` {
			t.Errorf("%s job: %d %s, want {\"job\":null}", kind, rec.Code, rec.Body)
		}
	}
	e.files.setJob(&filesapi.Job{ID: "j2", Kind: filesapi.JobExtract, State: filesapi.JobRunning, Path: "w.zip", Dest: "w"})
	rec := e.do(http.MethodGet, srvPath(id, "/files/jobs"), nil, &admin)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"dest":"w"`) || strings.Contains(body, `"kind"`) ||
		strings.Contains(body, `"snapshot"`) || strings.Contains(body, `"skipped"`) {
		t.Fatalf("extract job: %d %s", rec.Code, body)
	}
}
```

(Die Imports `strings` und `net/http` sind in `extract_test.go` bereits vorhanden; fehlen sie, ergänzen.)

Neue Datei `internal/servers/gate_test.go`:

```go
package servers_test

import (
	"context"
	"testing"

	"github.com/Bernotese/mc-panel/internal/servers"
)

func TestAnyGate(t *testing.T) {
	ctx := context.Background()
	a := &fakeGate{running: map[int64]bool{}}
	b := &fakeGate{running: map[int64]bool{}}
	g := servers.AnyGate(a, nil, b)
	if g.DataJobRunning(ctx, 1) {
		t.Fatal("no gate holds, AnyGate does")
	}
	b.set(2, true)
	if g.DataJobRunning(ctx, 1) || !g.DataJobRunning(ctx, 2) {
		t.Fatal("AnyGate must hold exactly the servers one of its gates holds")
	}
	a.set(2, true)
	asked := b.askedCount()
	if !g.DataJobRunning(ctx, 2) || b.askedCount() != asked {
		t.Fatal("the first holding gate decides; later gates are not asked")
	}
	if servers.AnyGate().DataJobRunning(ctx, 2) || servers.AnyGate(nil).DataJobRunning(ctx, 2) {
		t.Fatal("an empty AnyGate never holds")
	}
}
```

In `internal/servers/helpers_test.go`:
- in `newEnv` `Extract: e.gate` durch `Gate: e.gate` ersetzen;
- den Kommentar über `fakeGate` zu `// fakeGate is a servers.DataGate with a switch per server.` ändern;
- `func (g *fakeGate) ExtractRunning(` in `func (g *fakeGate) DataJobRunning(` umbenennen.

- [ ] **Step 4: `make check` (rot)**

Run: `git add -N internal/files/filesapi/backups_test.go internal/servers/gate_test.go && make check`
Expected: FAIL – `undefined: filesapi.ValidSnapshotID`, `filesapi.CodeUnavailable`, `e.h.DataJobRunning`, `servers.AnyGate`, unknown field `Gate` in `servers.Deps` usw.

- [ ] **Step 5: `filesapi` implementieren**

`internal/files/filesapi/types.go` – `Job` ersetzen:

```go
type Job struct {
	ID           string   `json:"id"`
	Kind         JobKind  `json:"kind"` // "" (a service before sub-project 4) counts as JobExtract
	State        JobState `json:"state"`
	Path         string   `json:"path"`
	Dest         string   `json:"dest"`
	Bytes        int64    `json:"bytes"`
	Entries      int64    `json:"entries"`
	TotalBytes   int64    `json:"total_bytes"`
	TotalEntries int64    `json:"total_entries"`
	Snapshot     string   `json:"snapshot"` // backup: the new snapshot once done; restore: its source; else ""
	Skipped      int64    `json:"skipped"`  // entries a backup left out (symlinks, special files, hidden or invalid names)
	Error        string   `json:"error"`
	StartedAt    int64    `json:"started_at"`  // Unix ms
	FinishedAt   int64    `json:"finished_at"` // 0 while running
}
```

`internal/files/filesapi/errors.go` – in den Konstantenblock nach `CodeHashMismatch`:

```go
	CodeUnavailable         = "unavailable"          // 503: restic is missing or too old (backups only)
```

in den `var`-Block nach `ErrHashMismatch`:

```go
	// ErrBackupsUnavailable: the service runs, but without a usable restic (sub-project 4).
	ErrBackupsUnavailable = errors.New(CodeUnavailable)
```

und in `codes` nach dem `CodeHashMismatch`-Eintrag:

```go
	{CodeUnavailable, codeInfo{ErrBackupsUnavailable, http.StatusServiceUnavailable}},
```

Neue Datei `internal/files/filesapi/backups.go`:

```go
package filesapi

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Backups (sub-project 4 §4): restic snapshots of a server's data directory. The service makes
// and reads them as tar streams; restic never opens a path in the data directory.
const (
	PathBackupsStatus   = "/v1/backups/status"   // GET -> BackupsStatus
	PathBackupsList     = "/v1/backups/list"     // POST IDRequest -> SnapshotsResponse
	PathBackupsCreate   = "/v1/backups/create"   // POST CreateBackupRequest -> 202 Job
	PathBackupsRestore  = "/v1/backups/restore"  // POST SnapshotRequest -> 202 Job (pre-check synchronous)
	PathBackupsForget   = "/v1/backups/forget"   // POST ForgetRequest -> 202 Job
	PathBackupsPin      = "/v1/backups/pin"      // POST PinRequest -> SnapshotResponse
	PathBackupsDownload = "/v1/backups/download" // GET query id, snapshot -> zip stream (HeaderKind "dir")
	PathBackupsPurge    = "/v1/backups/purge"    // POST IDRequest -> 204
	PathJobsCancel      = "/v1/jobs/cancel"      // POST IDRequest -> 204

	MinResticVersion = "0.17.0"
	// MaxNoteRunes bounds the note of a manual snapshot (a restic tag, base64url encoded).
	MaxNoteRunes = 64
	// MaxForgetIDs bounds the snapshots of one forget request.
	MaxForgetIDs = 100
	// MaxKeep bounds every keep_* value of a retention policy.
	MaxKeep = 1000
	// MaxTimezoneLen bounds an IANA time zone name.
	MaxTimezoneLen = 64

	// BackupCallTimeout bounds list, pin, forget and the restore pre-check (restic reads the
	// repository index).
	BackupCallTimeout = 2 * time.Minute
)

// JobKind is the kind of a service job; "" (an older service) counts as JobExtract.
type JobKind string

const (
	JobExtract JobKind = "extract"
	JobBackup  JobKind = "backup"
	JobRestore JobKind = "restore"
	JobForget  JobKind = "forget"
)

// SnapshotKind is the restic tag kind=<kind> of a snapshot.
type SnapshotKind string

const (
	SnapshotScheduled  SnapshotKind = "scheduled"
	SnapshotManual     SnapshotKind = "manual"
	SnapshotPreRestore SnapshotKind = "pre-restore"
)

func (k SnapshotKind) Valid() bool {
	switch k {
	case SnapshotScheduled, SnapshotManual, SnapshotPreRestore:
		return true
	}
	return false
}

// Snapshot is one restic snapshot of a server's repository.
type Snapshot struct {
	ID      string       `json:"id"`       // 64 hex
	ShortID string       `json:"short_id"` // 8 hex
	Time    int64        `json:"time"`     // Unix ms
	Kind    SnapshotKind `json:"kind"`
	Pinned  bool         `json:"pinned"`
	Note    string       `json:"note"`  // "" = none
	Bytes   int64        `json:"bytes"` // size of data.tar (summary.total_bytes_processed)
}

// BackupsStatus says whether the service can run restic.
type BackupsStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version"` // restic version, "" if unavailable
	Reason    string `json:"reason"`  // "" if available; never a host path
}

type IDRequest struct {
	ID int64 `json:"id"`
}

type CreateBackupRequest struct {
	ID       int64        `json:"id"`
	Kind     SnapshotKind `json:"kind"`
	Note     string       `json:"note"`               // only for SnapshotManual; "" = none
	Timezone string       `json:"timezone,omitempty"` // backup_timezone: restic stores the snapshot time in this zone, which sets the day/week/month buckets of forget; "" = the service's zone
}

type SnapshotRequest struct {
	ID       int64  `json:"id"`
	Snapshot string `json:"snapshot"`
}

// ForgetPolicy is the retention of the scheduled snapshots (restic forget --keep-*).
type ForgetPolicy struct {
	KeepLast    int `json:"keep_last"`
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

// ForgetRequest carries exactly one of Snapshots (1..MaxForgetIDs IDs) or Policy (then Timezone
// is required, an IANA name accepted by ValidTimezone).
type ForgetRequest struct {
	ID        int64         `json:"id"`
	Snapshots []string      `json:"snapshots,omitempty"`
	Policy    *ForgetPolicy `json:"policy,omitempty"`
	Timezone  string        `json:"timezone,omitempty"`
}

type PinRequest struct {
	ID       int64  `json:"id"`
	Snapshot string `json:"snapshot"`
	Pinned   bool   `json:"pinned"`
}

type SnapshotsResponse struct {
	Snapshots []Snapshot `json:"snapshots"` // never nil; newest first
}

type SnapshotResponse struct {
	Snapshot Snapshot `json:"snapshot"`
}

// ValidSnapshotID reports whether s is a full restic snapshot ID: exactly 64 lowercase hex
// characters. Only such IDs are ever passed to restic.
func ValidSnapshotID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ValidNote reports whether s may be the note of a manual snapshot: "" (none), or at most
// MaxNoteRunes runes of valid UTF-8 without control characters and not only spaces.
func ValidNote(s string) bool {
	if s == "" {
		return true
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxNoteRunes || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidTimezone reports whether tz is an IANA time zone name the schedules and restic's
// retention buckets can use. "" and "Local" are refused: both mean the host's zone, which the
// panel and the service may not share.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" || len(tz) > MaxTimezoneLen {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// Validate checks every keep value (0..MaxKeep) and that at least one is > 0 (restic forget
// without any keep option would remove every snapshot of the group).
func (p ForgetPolicy) Validate() error {
	some := false
	for _, v := range []int{p.KeepLast, p.KeepDaily, p.KeepWeekly, p.KeepMonthly} {
		if v < 0 || v > MaxKeep {
			return fmt.Errorf("%w: keep values must be 0..%d", ErrInvalid, MaxKeep)
		}
		if v > 0 {
			some = true
		}
	}
	if !some {
		return fmt.Errorf("%w: at least one keep value must be > 0", ErrInvalid)
	}
	return nil
}

func (r ForgetRequest) Validate() error {
	if err := checkID(r.ID); err != nil {
		return err
	}
	switch {
	case len(r.Snapshots) > 0 && r.Policy != nil:
		return fmt.Errorf("%w: snapshots and policy exclude each other", ErrInvalid)
	case r.Policy != nil:
		if err := r.Policy.Validate(); err != nil {
			return err
		}
		if !ValidTimezone(r.Timezone) {
			return fmt.Errorf("%w: a policy needs an IANA time zone", ErrInvalid)
		}
		return nil
	case len(r.Snapshots) == 0:
		return fmt.Errorf("%w: snapshots or policy required", ErrInvalid)
	case len(r.Snapshots) > MaxForgetIDs:
		return fmt.Errorf("%w: at most %d snapshots per request", ErrInvalid, MaxForgetIDs)
	case r.Timezone != "":
		return fmt.Errorf("%w: a time zone only goes with a policy", ErrInvalid)
	}
	seen := make(map[string]bool, len(r.Snapshots))
	for _, s := range r.Snapshots {
		if !ValidSnapshotID(s) || seen[s] {
			return fmt.Errorf("%w: snapshots must be distinct 64-character hex IDs", ErrInvalid)
		}
		seen[s] = true
	}
	return nil
}

func (r CreateBackupRequest) Validate() error {
	if err := checkID(r.ID); err != nil {
		return err
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("%w: kind must be scheduled, manual or pre-restore", ErrInvalid)
	}
	if !ValidNote(r.Note) {
		return fmt.Errorf("%w: note: at most %d characters, no control characters", ErrInvalid, MaxNoteRunes)
	}
	if r.Note != "" && r.Kind != SnapshotManual {
		return fmt.Errorf("%w: only manual snapshots have a note", ErrInvalid)
	}
	if r.Timezone != "" && !ValidTimezone(r.Timezone) {
		return fmt.Errorf("%w: timezone must be an IANA time zone name", ErrInvalid)
	}
	return nil
}

func checkSnapshot(id int64, snapshot string) error {
	if err := checkID(id); err != nil {
		return err
	}
	if !ValidSnapshotID(snapshot) {
		return fmt.Errorf("%w: snapshot must be 64 lowercase hex characters", ErrInvalid)
	}
	return nil
}

// BackupClient is the panel's view of the backup endpoints (implemented by *UnixClient).
type BackupClient interface {
	BackupsStatus(ctx context.Context) (BackupsStatus, error)
	Snapshots(ctx context.Context, id int64) ([]Snapshot, error)
	CreateBackup(ctx context.Context, req CreateBackupRequest) (Job, error)
	RestoreBackup(ctx context.Context, id int64, snapshot string) (Job, error)
	ForgetBackups(ctx context.Context, req ForgetRequest) (Job, error)
	PinBackup(ctx context.Context, id int64, snapshot string, pinned bool) (Snapshot, error)
	DownloadBackup(ctx context.Context, id int64, snapshot string) (Download, error)
	PurgeBackups(ctx context.Context, id int64) error
	CancelJob(ctx context.Context, id int64) error
	Job(ctx context.Context, id int64) (*Job, error)
}
```

`internal/files/filesapi/client.go`:

- im Kommentarblock der Konstanten ist nichts zu ändern; in `UnixClient` nach `jarsTimeout` das Feld `backupTimeout time.Duration` einfügen, in `NewClient` nach `jarsTimeout: JarsTimeout,` die Zeile `backupTimeout: BackupCallTimeout,`;
- `Download` ersetzen durch:

```go
// Download opens path for streaming. Headers must arrive within DownloadHeaderTimeout; the body
// has no deadline (ctx only). Closing the body releases the connection.
func (c *UnixClient) Download(ctx context.Context, id int64, p string) (Download, error) {
	if err := clean(id, &p); err != nil {
		return Download{}, err
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "path": {p}}
	d, err := c.openDownload(ctx, PathDownload+"?"+q.Encode())
	if err != nil {
		return Download{}, err
	}
	if p != "" {
		d.Name = path.Base(p)
	}
	return d, nil
}

// openDownload sends GET target and returns the stream of a 200 answer with a known kind (file
// or dir); every other answer is an error. Headers must arrive within DownloadHeaderTimeout.
func (c *UnixClient) openDownload(ctx context.Context, target string) (Download, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+target, nil)
	if err != nil {
		cancel()
		return Download{}, fmt.Errorf("files: build request: %w", err)
	}
	timer := time.AfterFunc(c.downloadHeaderTimeout, cancel)
	resp, err := c.hc.Do(req)
	if !timer.Stop() {
		if err == nil {
			_ = resp.Body.Close()
		}
		cancel()
		return Download{}, fmt.Errorf("%w: download: no response within %s", ErrUnavailable, c.downloadHeaderTimeout)
	}
	if err != nil {
		cancel()
		return Download{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	// Only a 200 with a known kind is a download; every other answer (also 204/206) is an error,
	// so a caller never gets a Download without a Body.
	kind := resp.Header.Get(HeaderKind)
	if resp.StatusCode != http.StatusOK || (kind != "file" && kind != "dir") {
		defer cancel()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return Download{}, decode(resp, nil)
		}
		return Download{}, fmt.Errorf("%w: download: unexpected answer %d (kind %q)", ErrUnavailable, resp.StatusCode, kind)
	}
	d := Download{Body: &cancelBody{ReadCloser: resp.Body, cancel: cancel}, Size: resp.ContentLength}
	if kind == "dir" {
		d.IsDir, d.Size = true, -1
	}
	return d, nil
}
```

Neue Datei `internal/files/filesapi/backups_client.go`:

```go
package filesapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

var _ BackupClient = (*UnixClient)(nil)

// BackupsStatus asks whether the service can run restic (always answered, also without it).
func (c *UnixClient) BackupsStatus(ctx context.Context) (BackupsStatus, error) {
	var out BackupsStatus
	err := c.call(ctx, HealthTimeout, http.MethodGet, PathBackupsStatus, nil, &out)
	return out, err
}

// Snapshots lists the snapshots of a server, newest first; never nil.
func (c *UnixClient) Snapshots(ctx context.Context, id int64) ([]Snapshot, error) {
	if err := checkID(id); err != nil {
		return []Snapshot{}, err
	}
	var out SnapshotsResponse
	if err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsList, IDRequest{ID: id}, &out); err != nil {
		return []Snapshot{}, err
	}
	if out.Snapshots == nil {
		out.Snapshots = []Snapshot{}
	}
	return out.Snapshots, nil
}

// CreateBackup starts a backup job (ErrNotFound without data directory, ErrBusy with a job of
// the server running, ErrInsufficientStorage).
func (c *UnixClient) CreateBackup(ctx context.Context, req CreateBackupRequest) (Job, error) {
	if err := req.Validate(); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.callTimeout, http.MethodPost, PathBackupsCreate, req, &out)
	return out, err
}

// RestoreBackup checks snapshot and free space synchronously and then starts the restore job.
func (c *UnixClient) RestoreBackup(ctx context.Context, id int64, snapshot string) (Job, error) {
	if err := checkSnapshot(id, snapshot); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsRestore, SnapshotRequest{ID: id, Snapshot: snapshot}, &out)
	return out, err
}

// ForgetBackups starts a forget job (retention policy or explicit IDs; restic forget --prune).
func (c *UnixClient) ForgetBackups(ctx context.Context, req ForgetRequest) (Job, error) {
	if err := req.Validate(); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsForget, req, &out)
	return out, err
}

// PinBackup sets or clears the tag "pinned" of a scheduled snapshot. restic rewrites the
// snapshot: the answer carries its new ID.
func (c *UnixClient) PinBackup(ctx context.Context, id int64, snapshot string, pinned bool) (Snapshot, error) {
	if err := checkSnapshot(id, snapshot); err != nil {
		return Snapshot{}, err
	}
	var out SnapshotResponse
	if err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsPin, PinRequest{ID: id, Snapshot: snapshot, Pinned: pinned}, &out); err != nil {
		return Snapshot{}, err
	}
	if !ValidSnapshotID(out.Snapshot.ID) {
		return Snapshot{}, fmt.Errorf("%w: pin answer without a valid snapshot id", ErrUnavailable)
	}
	return out.Snapshot, nil
}

// DownloadBackup opens a snapshot as a zip stream (like Download of a folder; Name stays "",
// the caller names the file).
func (c *UnixClient) DownloadBackup(ctx context.Context, id int64, snapshot string) (Download, error) {
	if err := checkSnapshot(id, snapshot); err != nil {
		return Download{}, err
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "snapshot": {snapshot}}
	d, err := c.openDownload(ctx, PathBackupsDownload+"?"+q.Encode())
	if err != nil {
		return Download{}, err
	}
	if !d.IsDir {
		_ = d.Body.Close()
		return Download{}, fmt.Errorf("%w: a backup download must be a zip stream", ErrUnavailable)
	}
	return d, nil
}

// PurgeBackups deletes the repository and key of a server (a missing one is no error).
func (c *UnixClient) PurgeBackups(ctx context.Context, id int64) error {
	if err := checkID(id); err != nil {
		return err
	}
	return c.call(ctx, c.deleteTimeout, http.MethodPost, PathBackupsPurge, IDRequest{ID: id}, nil)
}

// CancelJob cancels the running backup, restore (until its swap) or forget job of a server.
func (c *UnixClient) CancelJob(ctx context.Context, id int64) error {
	if err := checkID(id); err != nil {
		return err
	}
	return c.call(ctx, c.callTimeout, http.MethodPost, PathJobsCancel, IDRequest{ID: id}, nil)
}
```

(`Job` gibt es schon; es erfüllt auch `BackupClient`.)

- [ ] **Step 6: `serverfiles` und `servers` implementieren**

`internal/serverfiles/extract.go`:

- Kommentar der Konstante: `// gateTimeout bounds the job query of DataJobRunning: power actions and the reconcile loop`;
- `jobsJSON` ersetzen und `fileJobJSON` mit Helfern dazu:

```go
// jobsJSON is the OpenAPI schema FileJobResponse.
type jobsJSON struct {
	Job *fileJobJSON `json:"job"`
}

// fileJobJSON is the OpenAPI schema FileJob: an extract job of the file browser. Backup,
// restore and forget jobs of the same service slot are not shown here (backups spec §4.1).
type fileJobJSON struct {
	ID           string            `json:"id"`
	State        filesapi.JobState `json:"state"`
	Path         string            `json:"path"`
	Dest         string            `json:"dest"`
	Bytes        int64             `json:"bytes"`
	Entries      int64             `json:"entries"`
	TotalBytes   int64             `json:"total_bytes"`
	TotalEntries int64             `json:"total_entries"`
	Error        string            `json:"error"`
	StartedAt    int64             `json:"started_at"`
	FinishedAt   int64             `json:"finished_at"`
}

func toFileJob(j filesapi.Job) fileJobJSON {
	return fileJobJSON{
		ID: j.ID, State: j.State, Path: j.Path, Dest: j.Dest, Bytes: j.Bytes, Entries: j.Entries,
		TotalBytes: j.TotalBytes, TotalEntries: j.TotalEntries, Error: j.Error,
		StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
	}
}

// isExtract: "" is an extract job of a service before sub-project 4.
func isExtract(k filesapi.JobKind) bool { return k == "" || k == filesapi.JobExtract }

// rewritesData reports whether a job of this kind writes into the data directory: an extract
// or a restore. A backup only reads it, a forget never touches it.
func rewritesData(k filesapi.JobKind) bool { return isExtract(k) || k == filesapi.JobRestore }
```

- in `handleExtract` die letzte Zeile `httpx.WriteJSON(w, http.StatusAccepted, job)` durch `httpx.WriteJSON(w, http.StatusAccepted, toFileJob(job))` ersetzen;
- in `handleJobs` die letzte Zeile ersetzen durch:

```go
	var out jobsJSON
	if job != nil && isExtract(job.Kind) {
		fj := toFileJob(*job)
		out.Job = &fj
	}
	httpx.WriteJSON(w, http.StatusOK, out)
```

- `ExtractRunning` ersetzen durch:

```go
// DataJobRunning reports whether a job that rewrites the data directory runs for the server (an
// extract, files spec §5.1, or a restore, backups spec §5.5; a backup does not count), or an
// extract request is between its state check and the service's answer. An unreachable service
// counts as "no job". It implements servers.DataGate.
func (h *Handler) DataJobRunning(ctx context.Context, serverID int64) bool {
	if h.pending.has(serverID) {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()
	job, err := h.files.Job(ctx, serverID)
	if err != nil {
		h.log.WarnContext(ctx, "files: data gate open, job state unknown (file service unreachable)",
			"server_id", serverID, "err", err)
		return false
	}
	return job != nil && job.State == filesapi.JobRunning && rewritesData(job.Kind)
}
```

`internal/serverfiles/helpers_test.go` und `cmd/panel/fakefiles_test.go`: im Fake-`Extract` die Job-Zeile um `Kind: filesapi.JobExtract,` ergänzen:

```go
	j := filesapi.Job{ID: "j1", Kind: filesapi.JobExtract, State: "running", Path: req.Path, Dest: req.Dest, StartedAt: t0.UnixMilli()}
```

(in `cmd/panel/fakefiles_test.go` ohne `StartedAt`: `filesapi.Job{ID: "j1", Kind: filesapi.JobExtract, State: "running", Path: req.Path, Dest: req.Dest}`).

`internal/servers/gate.go` komplett ersetzen:

```go
package servers

import "context"

// DataGate is consulted before a user's power start/restart and by the reconcile loop before
// it starts a server: while a job rewrites the data directory (an archive is extracted, files
// spec §5.1, or a backup is restored, backups spec §5.5), the server must not start.
// serverfiles.Handler and backups.Handler implement it.
type DataGate interface {
	DataJobRunning(ctx context.Context, serverID int64) bool
}

// noGate never blocks (nothing wired).
type noGate struct{}

func (noGate) DataJobRunning(context.Context, int64) bool { return false }

// anyGate holds a start while one of its gates does; the gates are asked in order.
type anyGate []DataGate

func (g anyGate) DataJobRunning(ctx context.Context, serverID int64) bool {
	for _, gate := range g {
		if gate.DataJobRunning(ctx, serverID) {
			return true
		}
	}
	return false
}

// AnyGate combines gates (nil entries are skipped). Put cheap gates first: the first one that
// holds decides, later ones are not asked.
func AnyGate(gates ...DataGate) DataGate {
	out := make(anyGate, 0, len(gates))
	for _, g := range gates {
		if g != nil {
			out = append(out, g)
		}
	}
	return out
}
```

`internal/servers/handler.go`:

```go
	// Gate holds starts while a job rewrites a server's data directory (extract, restore);
	// nil = never blocked.
	Gate DataGate
```

statt des `Extract`-Felds in `Deps`; im `Handler` das Feld `extract ExtractGate` durch `gate DataGate` ersetzen; in `NewHandler`:

```go
	gate := d.Gate
	if gate == nil {
		gate = noGate{}
	}
```

und im Literal `extract: gate,` → `gate: gate,` (gofmt richtet aus).

`internal/servers/power.go:45-48`:

```go
	if req.Action != agentapi.PowerStop && h.gate.DataJobRunning(ctx, s.ID) {
		writeConflict(w, r, "a file or backup job is rewriting this server's data; try again when it is done")
		return
	}
```

`internal/servers/reconcile.go:161-165`:

```go
		if act == actStart && h.gate.DataJobRunning(ctx, in.ServerID) {
			// A job rewrites the data directory (extract or restore): the wish to run stays and
			// is applied on the first pass after the job; no backoff.
			continue
		}
```

`cmd/panel/app.go`: im Block `servers.NewHandler(servers.Deps{…})` `Extract: filesH,` durch `Gate: filesH,` ersetzen und den Kommentar darüber zu `// The files handler is one of the servers handler's data gates: no start while an archive is extracted into a server (files spec §5.1).` anpassen.

`api/openapi.yaml`, Operation `serverPower`, `409`-Beschreibung ersetzen durch:

```yaml
        '409': { description: 'conflict (server not configured, restart of a container that belongs to another server, or a job rewrites the data directory: an archive is extracted or a backup restored) or base_domain_missing (restart)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

- [ ] **Step 7: `make check` (grün), Tests auf der VM**

Run: `make check`
Expected: PASS.

Run: `make test-vm SUITE=go` (oder gezielt `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-contracts && PATH=/usr/local/go/bin:$PATH go test ./internal/files/filesapi ./internal/serverfiles ./internal/servers ./cmd/panel'` nach einem `make test-vm SUITE=go`, das den Baum kopiert)
Expected: PASS, insbesondere `TestClientBackupCalls`, `TestClientBackupsRejectBadInputWithoutRequest`, `TestDataJobRunningOnlyForJobsThatRewriteData`, `TestFileJobsShowOnlyExtractJobs`, `TestAnyGate`, `TestPowerBlockedWhileExtracting`, `TestRoutesMatchOpenAPI`.

- [ ] **Step 8: Commit, Push, Draft-PR, CI**

```bash
git add internal/files/filesapi internal/serverfiles internal/servers cmd/panel/app.go cmd/panel/fakefiles_test.go api/openapi.yaml
git commit -m "feat(files): backup contract in filesapi, data gate for extract and restore

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/backups-contracts
gh pr create --draft --title "feat: contracts for sub-project 4 (backups)" --body "Phase A of docs/superpowers/plans/2026-10-12-backups-01-contracts.md (tasks 1-3).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
gh pr checks --watch --interval 30
```

Expected: `CI OK` grün (Web-Jobs laufen mit, weil `api/openapi.yaml` geändert ist; `schema.d.ts` ändert sich durch die Beschreibung: vorher `make generate` ausführen und `web/src/api/schema.d.ts` mit committen, falls `git status` es zeigt).

---

### Task 2: Daten, Fehlercodes, Audit, Notify; Zeitzone und `purge_backups` in `servers`

**Files:**
- Create: `internal/store/migrations/00006_backups.sql`
- Create: `internal/store/queries/backups.sql` → generiert `internal/store/backups.sql.go`
- Modify: `internal/store/queries/servers.sql` (`ServersSetBackupTimezone`) → `internal/store/servers.sql.go`, `internal/store/models.go` (generiert)
- Create: `internal/store/backups_test.go`
- Modify: `internal/store/store_test.go` (Tabellenliste)
- Modify: `internal/httpx/errors.go`, `internal/audit/types.go`, `internal/notify/notify.go`, `internal/notify/sender.go`
- Modify: `internal/servers/gate.go` (`BackupPurger`, `noPurger`), `internal/servers/handler.go` (`Deps.Backups`, Feld `backups`)
- Modify: `internal/servers/settings.go` (`backup_timezone`)
- Modify: `internal/servers/servers.go` (`handleDeleteServer`, `deleteServerRows`, `purgeBackupsParam`)
- Modify: `internal/servers/helpers_test.go` (`fakePurger`, `env.purger`)
- Modify: `internal/servers/settings_test.go`
- Create: `internal/servers/purge_test.go`
- Modify: `api/openapi.yaml` (Error-Enum, `ServerSettings`, `UpdateServerSettingsRequest`, `deleteServer`), `web/src/api/schema.d.ts` (generiert)
- Modify: `web/src/api/errors.ts` (`KNOWN_CODES`), `web/src/i18n/de.ts`, `web/src/i18n/en.ts` (nur `errors.*`), `web/src/i18n/i18n.test.ts` (`ERROR_CODES`)
- Modify: `web/src/mocks/db.ts` (`MockServerSettings.backup_timezone`, Seed), `web/src/mocks/handlers/servers.ts` (Einstellungen, Löschen), `web/src/mocks/handlers.test.ts` (Einstellungs-Test)
- Modify: `web/src/features/settings/settings-page.test.tsx` (Vorlage, Abweichung 9)

**Interfaces:**
- Consumes: `filesapi.ValidTimezone` (Aufgabe 1).
- Produces (V2):

```go
// store (names as sqlc generates them; check after `make generate`)
type BackupSchedule struct {
	ServerID int64; Mode string; IntervalHours, TimeOfDay, Weekday, KeepLast, KeepDaily, KeepWeekly, KeepMonthly int64
	NextRunAt, LastRunAt sql.NullInt64; LastStatus, LastError, LastSnapshotID sql.NullString
	SaveoffSince sql.NullInt64; UpdatedAt int64
}
func (q *Queries) BackupsEnsureSchedule(ctx, BackupsEnsureScheduleParams{ServerID, UpdatedAt}) error
func (q *Queries) BackupsGetSchedule(ctx, serverID int64) (BackupSchedule, error)
func (q *Queries) BackupsUpdateSchedule(ctx, BackupsUpdateScheduleParams{Mode, IntervalHours, TimeOfDay, Weekday, KeepLast, KeepDaily, KeepWeekly, KeepMonthly, NextRunAt, UpdatedAt, ServerID}) error
func (q *Queries) BackupsListDue(ctx, nextRunAt sql.NullInt64) ([]BackupSchedule, error)
func (q *Queries) BackupsListEnabled(ctx) ([]BackupSchedule, error)
func (q *Queries) BackupsSetNextRun(ctx, BackupsSetNextRunParams{NextRunAt, ServerID}) error
func (q *Queries) BackupsStartRun(ctx, BackupsStartRunParams{LastRunAt, ServerID}) error
func (q *Queries) BackupsFinishRun(ctx, BackupsFinishRunParams{LastStatus, LastError, LastSnapshotID, NextRunAt, ServerID}) error
func (q *Queries) BackupsFailRunning(ctx, lastError sql.NullString) error
func (q *Queries) BackupsSetSaveOff(ctx, BackupsSetSaveOffParams{SaveoffSince, ServerID}) error
func (q *Queries) BackupsListSaveOff(ctx) ([]BackupsListSaveOffRow{ServerID, SaveoffSince}, error)
func (q *Queries) BackupsGetTimezone(ctx) (string, error)
func (q *Queries) ServersSetBackupTimezone(ctx, ServersSetBackupTimezoneParams{BackupTimezone, UpdatedAt}) error
// InstanceSetting gains BackupTimezone string

// httpx: CodeBackupsUnavailable = "backups_unavailable", CodeBackupRunning = "backup_running"
// audit: ActionBackupCreate … ActionBackupScheduleUpdate (V2)
// notify: EventBackupFailed EventType = "backup.failed"

// servers
type BackupPurger interface{ PurgeBackups(ctx context.Context, serverID int64) error }
// Deps.Backups BackupPurger (nil = nothing to purge)
// ServerSettings JSON gains "backup_timezone"; UpdateServerSettingsRequest gains optional "backup_timezone"
// DELETE /servers/{id}?purge_backups=1|true|0|false
```

- [ ] **Step 1: Store-Tests schreiben (rot)**

`internal/store/store_test.go`, in `TestMigrateIsIdempotentAndCreatesTables` die Tabellenliste um `"backup_schedules"` erweitern (nach `"plugin_installs"`).

Neue Datei `internal/store/backups_test.go`:

```go
package store_test

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func i64(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
func str(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }

func dueIDs(t *testing.T, q *store.Queries, now int64) []int64 {
	t.Helper()
	rows, err := q.BackupsListDue(t.Context(), i64(now))
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for _, r := range rows {
		ids = append(ids, r.ServerID)
	}
	return ids
}

func TestBackupScheduleDefaultsAndRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	a := storetest.CreateServer(t, db, "survival")
	b := storetest.CreateServer(t, db, "creative")

	if err := q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: a, UpdatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	// a second ensure keeps the row as it is
	if err := q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: a, UpdatedAt: 9}); err != nil {
		t.Fatal(err)
	}
	got, err := q.BackupsGetSchedule(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	want := store.BackupSchedule{
		ServerID: a, Mode: "off", IntervalHours: 24, TimeOfDay: 240, Weekday: 0,
		KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6, UpdatedAt: 5,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}

	upd := store.BackupsUpdateScheduleParams{
		Mode: "daily", IntervalHours: 6, TimeOfDay: 180, Weekday: 3, KeepLast: 5, KeepDaily: 0,
		KeepWeekly: 2, KeepMonthly: 1, NextRunAt: i64(1000), UpdatedAt: 20, ServerID: a,
	}
	if err := q.BackupsUpdateSchedule(ctx, upd); err != nil {
		t.Fatal(err)
	}
	if err := q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: b, UpdatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	// b is off: never due, even with a next run in the past
	if err := q.BackupsSetNextRun(ctx, store.BackupsSetNextRunParams{NextRunAt: i64(1), ServerID: b}); err != nil {
		t.Fatal(err)
	}
	if ids := dueIDs(t, q, 999); len(ids) != 0 {
		t.Fatalf("due before next_run_at: %v", ids)
	}
	if ids := dueIDs(t, q, 1000); !slices.Equal(ids, []int64{a}) {
		t.Fatalf("due at next_run_at: %v, want [%d]", ids, a)
	}
	enabled, err := q.BackupsListEnabled(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].ServerID != a {
		t.Fatalf("enabled = %+v, %v", enabled, err)
	}

	// a scheduled run: start, finish
	if err := q.BackupsStartRun(ctx, store.BackupsStartRunParams{LastRunAt: i64(1000), ServerID: a}); err != nil {
		t.Fatal(err)
	}
	got, _ = q.BackupsGetSchedule(ctx, a)
	if got.LastStatus != str("running") || got.LastRunAt != i64(1000) || got.LastError.Valid {
		t.Fatalf("after start: %+v", got)
	}
	if err := q.BackupsFinishRun(ctx, store.BackupsFinishRunParams{
		LastStatus: str("ok"), LastError: sql.NullString{}, LastSnapshotID: str("abc"), NextRunAt: i64(2000), ServerID: a,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = q.BackupsGetSchedule(ctx, a)
	if got.LastStatus != str("ok") || got.LastSnapshotID != str("abc") || got.NextRunAt != i64(2000) {
		t.Fatalf("after finish: %+v", got)
	}

	// a panel crash: running rows become failed
	if err := q.BackupsStartRun(ctx, store.BackupsStartRunParams{LastRunAt: i64(2000), ServerID: a}); err != nil {
		t.Fatal(err)
	}
	if err := q.BackupsFailRunning(ctx, str("panel restarted")); err != nil {
		t.Fatal(err)
	}
	got, _ = q.BackupsGetSchedule(ctx, a)
	if got.LastStatus != str("failed") || got.LastError != str("panel restarted") {
		t.Fatalf("after FailRunning: %+v", got)
	}

	// save-off markers
	if err := q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: i64(3000), ServerID: b}); err != nil {
		t.Fatal(err)
	}
	marks, err := q.BackupsListSaveOff(ctx)
	if err != nil || len(marks) != 1 || marks[0].ServerID != b || marks[0].SaveoffSince != i64(3000) {
		t.Fatalf("save-off marks = %+v, %v", marks, err)
	}
	if err := q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: sql.NullInt64{}, ServerID: b}); err != nil {
		t.Fatal(err)
	}
	if marks, _ := q.BackupsListSaveOff(ctx); len(marks) != 0 {
		t.Fatalf("cleared marks = %+v", marks)
	}
}

func TestBackupTimezoneSetting(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	if tz, err := q.BackupsGetTimezone(ctx); err != nil || tz != "Europe/Berlin" {
		t.Fatalf("default time zone = %q, %v", tz, err)
	}
	if err := q.ServersSetBackupTimezone(ctx, store.ServersSetBackupTimezoneParams{BackupTimezone: "UTC", UpdatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	s, err := q.ServersGetSettings(ctx)
	if err != nil || s.BackupTimezone != "UTC" || s.UpdatedAt != 7 {
		t.Fatalf("settings = %+v, %v", s, err)
	}
}

func TestBackupScheduleChecksAndCascade(t *testing.T) {
	db := storetest.Open(t)
	ctx := t.Context()
	id := storetest.CreateServer(t, db, "survival")
	for _, col := range []string{"mode = 'hourly'", "interval_hours = 0", "interval_hours = 169", "time_of_day = 1440",
		"weekday = 7", "last_status = 'done'"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO backup_schedules (server_id, updated_at) VALUES (?, 0)
			ON CONFLICT (server_id) DO NOTHING`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE backup_schedules SET `+col+` WHERE server_id = ?`, id); err == nil {
			t.Errorf("%s accepted", col)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM backup_schedules`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after server delete = %d, %v", n, err)
	}
}
```

Hinweis: Die Feld- und Parameternamen entsprechen dem, was sqlc 1.31.1 für die Queries in Step 3 erzeugt. Erzeugt `make generate` andere Namen (z. B. `LastSnapshotId`), werden **die Tests** an die generierten Namen angepasst und die Abweichung im PR vermerkt. B2 liest die Namen aus `backups.sql.go`.

- [ ] **Step 2: servers-Tests schreiben (rot)**

`internal/servers/helpers_test.go`:

- in `env` nach `gate    *fakeGate` das Feld `purger  *fakePurger` ergänzen;
- in `newEnv` im `env`-Literal `purger: &fakePurger{},` ergänzen und in `servers.Deps{…}` `Backups: e.purger,`;
- am Ende anfügen:

```go
// fakePurger is a servers.BackupPurger that records its calls.
type fakePurger struct {
	mu    sync.Mutex
	calls []int64
	err   error
}

func (p *fakePurger) PurgeBackups(_ context.Context, id int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, id)
	return p.err
}

func (p *fakePurger) setErr(err error) { p.mu.Lock(); p.err = err; p.mu.Unlock() }

func (p *fakePurger) takeCalls() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.calls
	p.calls = nil
	return c
}
```

Neue Datei `internal/servers/purge_test.go`:

```go
package servers_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// Backups spec §5.6: DELETE /servers/{id}?purge_backups=1 deletes the restic repository after
// the server is gone; without the parameter the repository stays.
func TestDeletePurgesBackupsOnlyOnRequest(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	keep := storetest.CreateServer(t, e.db, "keep")
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", keep), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete without purge: %d %s", rec.Code, rec.Body)
	}
	if calls := e.purger.takeCalls(); len(calls) != 0 {
		t.Fatalf("purged without the parameter: %v", calls)
	}
	if _, ok := e.rec.Entries[len(e.rec.Entries)-1].Details["backups_purged"]; ok {
		t.Fatal("audit names backups_purged without the parameter")
	}

	for _, v := range []string{"1", "true"} {
		id := storetest.CreateServer(t, e.db, "purge-"+v)
		rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=%s", id, v), nil, &admin)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("purge_backups=%s: %d %s", v, rec.Code, rec.Body)
		}
		if calls := e.purger.takeCalls(); !slices.Equal(calls, []int64{id}) {
			t.Fatalf("purge_backups=%s: purger calls %v", v, calls)
		}
		en := e.rec.Entries[len(e.rec.Entries)-1]
		if purged, _ := en.Details["backups_purged"].(bool); en.Action != audit.ActionServerDelete || !purged {
			t.Fatalf("audit = %+v", en)
		}
	}
	for _, v := range []string{"0", "false"} {
		id := storetest.CreateServer(t, e.db, "nopurge-"+v)
		if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=%s", id, v), nil, &admin); rec.Code != http.StatusNoContent {
			t.Fatalf("purge_backups=%s: %d", v, rec.Code)
		}
		if calls := e.purger.takeCalls(); len(calls) != 0 {
			t.Fatalf("purge_backups=%s purged: %v", v, calls)
		}
	}
}

// A failed purge leaves the server deleted (204) and an orphaned repository; the audit entry
// says so.
func TestDeleteSurvivesAFailedPurge(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := storetest.CreateServer(t, e.db, "lobby")
	e.purger.setErr(errors.New("files service down"))
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", id), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete with failing purge: %d %s", rec.Code, rec.Body)
	}
	if n := e.count(t, "servers"); n != 0 {
		t.Fatalf("server rows left: %d", n)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if purged, ok := en.Details["backups_purged"].(bool); !ok || purged {
		t.Fatalf("audit = %+v, want backups_purged false", en)
	}
	if !strings.Contains(e.logs.String(), "purging the backups failed") {
		t.Fatalf("log = %q", e.logs.String())
	}
}

// An invalid value is refused before anything happens; a delete that fails does not purge.
func TestDeletePurgeValidationAndFailedDelete(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	stub := storetest.CreateServer(t, e.db, "lobby")
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=yes", stub), nil, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("purge_backups=yes: %d %s", rec.Code, rec.Body)
	}
	if n := e.count(t, "servers"); n != 1 {
		t.Fatalf("refused delete removed rows: %d", n)
	}
	s := e.create(t, paper("survival"))
	e.agent.setErr(agentapi.ErrUnavailable)
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", s.ID), nil, &admin); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete with agent down: %d %s", rec.Code, rec.Body)
	}
	if calls := e.purger.takeCalls(); len(calls) != 0 {
		t.Fatalf("a failed delete purged: %v", calls)
	}
}
```

`internal/servers/settings_test.go`:

- Import `_ "time/tzdata"` ergänzen (Zeitzonen-Prüfung ohne Zoneinfo des Hosts) sowie `"github.com/Bernotese/mc-panel/internal/store"`;
- `settingsResp` um `BackupTimezone string \`json:"backup_timezone"\`` ergänzen;
- in `TestSettingsDefaultsAndDockerStatus` die erste Prüfung erweitern: `|| s.BackupTimezone != "Europe/Berlin"`;
- in `TestUpdateSettingsValidation` die Fälle ergänzen:

```go
		{`{"backup_timezone":null}`, "validation_failed"},
		{`{"backup_timezone":""}`, "validation_failed"},
		{`{"backup_timezone":"Local"}`, "validation_failed"},
		{`{"backup_timezone":"Mars/Olympus"}`, "validation_failed"},
		{`{"backup_timezone":"../../etc/passwd"}`, "validation_failed"},
		{`{"backup_timezone":5}`, "bad_request"},
```

- am Ende anfügen:

```go
// Backups spec §5.1: the time zone of all backup schedules is a server setting.
func TestUpdateBackupTimezone(t *testing.T) {
	e := newEnv(t)
	s := e.putSettings(t, `{"backup_timezone":"America/New_York"}`)
	if s.BackupTimezone != "America/New_York" {
		t.Fatalf("response = %+v", s)
	}
	if tz, err := store.New(e.db).BackupsGetTimezone(t.Context()); err != nil || tz != "America/New_York" {
		t.Fatalf("stored = %q, %v", tz, err)
	}
	if len(e.rec.Entries) != 1 {
		t.Fatalf("audit = %v", e.rec.Actions())
	}
	en := e.rec.Entries[0]
	tz, _ := en.Details["backup_timezone"].(map[string]any)
	if en.Action != audit.ActionServerSettingsUpdate || tz["old"] != "Europe/Berlin" || tz["new"] != "America/New_York" {
		t.Fatalf("audit entry = %+v", en)
	}
	// unchanged value: no audit entry; other fields stay
	e.putSettings(t, `{"backup_timezone":"America/New_York"}`)
	if got := e.putSettings(t, `{"base_domain":"mc.example.com"}`); got.BackupTimezone != "America/New_York" {
		t.Fatalf("base domain update reset the time zone: %+v", got)
	}
	if got := e.rec.Actions(); len(got) != 2 {
		t.Fatalf("audit = %v, want the time zone and the base domain change", got)
	}
}
```

- [ ] **Step 3: `make check` (rot)**

Run: `git add -N internal/store/backups_test.go internal/servers/purge_test.go && make check`
Expected: FAIL – `undefined: store.BackupsEnsureScheduleParams`, unknown field `Backups` in `servers.Deps` usw.

- [ ] **Step 4: Migration und Queries**

Neue Datei `internal/store/migrations/00006_backups.sql` (Tabelle wortgleich zu Spec §5.1, Kommentare englisch):

```sql
-- +goose Up
-- Sub-project 4: backup schedule, retention and the last scheduled run per server. The
-- snapshots themselves live in the restic repository of the server (the source of truth).
CREATE TABLE backup_schedules (
    server_id        INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    mode             TEXT    NOT NULL DEFAULT 'off' CHECK (mode IN ('off','interval','daily','weekly')),
    interval_hours   INTEGER NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 168),
    time_of_day      INTEGER NOT NULL DEFAULT 240 CHECK (time_of_day BETWEEN 0 AND 1439), -- minutes
    weekday          INTEGER NOT NULL DEFAULT 0 CHECK (weekday BETWEEN 0 AND 6),          -- 0 = Sunday
    keep_last        INTEGER NOT NULL DEFAULT 24,
    keep_daily       INTEGER NOT NULL DEFAULT 7,
    keep_weekly      INTEGER NOT NULL DEFAULT 4,
    keep_monthly     INTEGER NOT NULL DEFAULT 6,
    next_run_at      INTEGER,          -- NULL with mode = 'off'
    last_run_at      INTEGER,
    last_status      TEXT CHECK (last_status IN ('ok','failed','running')),
    last_error       TEXT,
    last_snapshot_id TEXT,
    saveoff_since    INTEGER,          -- set while the panel has sent save-off
    updated_at       INTEGER NOT NULL
);
ALTER TABLE instance_settings ADD COLUMN backup_timezone TEXT NOT NULL DEFAULT 'Europe/Berlin';

-- +goose Down
ALTER TABLE instance_settings DROP COLUMN backup_timezone;
DROP TABLE backup_schedules;
```

Neue Datei `internal/store/queries/backups.sql` – die Queries aus V2 wortgleich (Abschnitt „V2 – Daten und Querschnitt“ der Übersicht, von `BackupsEnsureSchedule` bis `BackupsGetTimezone`).

`internal/store/queries/servers.sql` nach `ServersUpdateSettings` anfügen:

```sql
-- name: ServersSetBackupTimezone :exec
UPDATE instance_settings SET backup_timezone = ?, updated_at = ? WHERE id = 1;
```

Run: `make generate`
Expected: neue `internal/store/backups.sql.go`, geänderte `servers.sql.go` und `models.go` (`InstanceSetting.BackupTimezone`, `BackupSchedule`).

- [ ] **Step 5: Codes, Audit, Notify**

`internal/httpx/errors.go` nach `CodePluginIncompatible`:

```go
	CodeBackupsUnavailable      = "backups_unavailable"
	CodeBackupRunning           = "backup_running"
```

`internal/audit/types.go` nach `ActionPluginToggle`:

```go
	ActionBackupCreate         = "backup.create"
	ActionBackupRestore        = "backup.restore"
	ActionBackupDelete         = "backup.delete"
	ActionBackupPin            = "backup.pin"
	ActionBackupUnpin          = "backup.unpin"
	ActionBackupDownload       = "backup.download"
	ActionBackupScheduleUpdate = "backup.schedule_update"
```

`internal/notify/notify.go` nach `EventAuditChainBroken`:

```go
	EventBackupFailed       EventType = "backup.failed"
```

`internal/notify/sender.go` in `defaultTitles` nach `EventAuditChainBroken`:

```go
	EventBackupFailed:       "Backup failed",
```

- [ ] **Step 6: servers – `BackupPurger`, Zeitzone, `purge_backups`**

`internal/servers/gate.go` am Ende anfügen:

```go
// BackupPurger deletes the backups of a deleted server (backups spec §5.6); backups.Handler
// implements it.
type BackupPurger interface {
	PurgeBackups(ctx context.Context, serverID int64) error
}

// noPurger has nothing to delete (no backups wired).
type noPurger struct{}

func (noPurger) PurgeBackups(context.Context, int64) error { return nil }
```

`internal/servers/handler.go`: in `Deps` nach `Gate`:

```go
	// Backups deletes a server's backups on DELETE /servers/{id}?purge_backups=1; nil = nothing
	// to delete.
	Backups BackupPurger
```

im `Handler` nach `gate DataGate` das Feld `backups BackupPurger`; in `NewHandler`:

```go
	purger := d.Backups
	if purger == nil {
		purger = noPurger{}
	}
```

und im Literal `backups: purger,`.

`internal/servers/settings.go`:

- Import `"github.com/Bernotese/mc-panel/internal/files/filesapi"` ergänzen;
- `settingsJSON` um `BackupTimezone string \`json:"backup_timezone"\`` (nach `CurseForgeAPIKeySet`) ergänzen und in `settingsBody` im Literal `BackupTimezone: row.BackupTimezone,` setzen;
- `updateSettingsRequest` um `BackupTimezone optional[string] \`json:"backup_timezone"\`` ergänzen (Kommentar: `// absent fields stay, null removes the value; backup_timezone cannot be null`);
- in `handleUpdateSettings` nach dem CurseForge-Block (vor `var ( oldBase …`):

```go
	if req.BackupTimezone.Set && (req.BackupTimezone.Value == nil || !filesapi.ValidTimezone(*req.BackupTimezone.Value)) {
		writeValidation(w, r, "backup_timezone must be an IANA time zone such as Europe/Berlin")
		return
	}
```

- den `var`-Block um `oldTZ string` und `tzChanged bool` erweitern;
- in der Transaktion nach `if err := q.ServersUpdateSettings(ctx, params); err != nil { return err }`:

```go
		oldTZ = cur.BackupTimezone
		if req.BackupTimezone.Set && *req.BackupTimezone.Value != cur.BackupTimezone {
			tzChanged = true
			if err := q.ServersSetBackupTimezone(ctx, store.ServersSetBackupTimezoneParams{
				BackupTimezone: *req.BackupTimezone.Value, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
```

(der Zeitzonen-Wechsel braucht keine neue Revision und keinen Abgleich; die Zeitpläne liest B2);
- in den `details` nach dem CurseForge-Block:

```go
	if tzChanged {
		details["backup_timezone"] = map[string]any{"old": oldTZ, "new": *req.BackupTimezone.Value}
	}
```

`internal/servers/servers.go` – `handleDeleteServer` ersetzen durch:

```go
// purgeBackupsParam parses ?purge_backups: absent, "0" and "false" = no; "1" and "true" = yes.
func purgeBackupsParam(r *http.Request) (purge, ok bool) {
	switch r.URL.Query().Get("purge_backups") {
	case "", "0", "false":
		return false, true
	case "1", "true":
		return true, true
	}
	return false, false
}

func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	purge, ok := purgeBackupsParam(r)
	if !ok {
		writeValidation(w, r, "purge_backups must be true, false, 1 or 0")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	configured, ok := h.deleteServerRows(w, r, s)
	if !ok {
		return
	}
	details := map[string]any{"name": s.Name, "configured": configured}
	if purge {
		// After the commit and outside the reconcile lock: deleting a large repository can take
		// minutes. A failure leaves an orphaned repository (as without the option); the server
		// stays deleted.
		if err := h.backups.PurgeBackups(ctx, s.ID); err != nil {
			h.log.Error("servers: delete: purging the backups failed", "server_id", s.ID, "err", err)
			details["backups_purged"] = false
		} else {
			details["backups_purged"] = true
		}
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, details)
	w.WriteHeader(http.StatusNoContent)
}

// deleteServerRows deletes the rows of s and (if configured and not foreign) its container under
// the reconcile lock. On failure it has written the error answer and returns ok = false.
func (h *Handler) deleteServerRows(w http.ResponseWriter, r *http.Request, s store.Server) (configured, ok bool) {
	ctx := r.Context()
	// Exclude a concurrent reconcile pass or restart: one that read this server before the
	// delete would otherwise recreate the container after the remove (orphan container).
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	_, err := h.q.ServersGetInstance(ctx, s.ID)
	configured = err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return false, false
	}
	remove := configured
	if configured {
		foreign, err := h.isForeign(ctx, s)
		if err != nil {
			writeAgentError(w, r, err)
			return false, false
		}
		if foreign {
			h.log.Warn("servers: delete leaves the container of another server alone", "server_id", s.ID, "name", s.Name)
			remove = false
		}
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		httpx.Internal(w, r, err)
		return false, false
	}
	defer func() { _ = tx.Rollback() }()
	n, err := h.q.WithTx(tx).RbacDeleteServer(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return false, false
	}
	if n == 0 { // deleted concurrently between lookup and delete
		writeNotFound(w, r)
		return false, false
	}
	if remove {
		if err := h.inst.Remove(ctx, s.ID); err != nil {
			writeAgentError(w, r, err) // the rollback keeps the rows
			return false, false
		}
	}
	if err := tx.Commit(); err != nil {
		h.log.Error("servers: delete: commit failed after the agent accepted the remove", "server_id", s.ID, "err", err)
		httpx.Internal(w, r, err)
		return false, false
	}
	delete(h.reconcile.attempts, s.ID)
	delete(h.reconcile.foreign, s.ID)
	h.setPanelError(s.ID, "")
	return configured, true
}
```

(Das Audit lag bisher innerhalb der Sperre; `record` öffnet eine eigene Transaktion und braucht die Sperre nicht.)

- [ ] **Step 7: OpenAPI, Frontend-Codes, Mocks**

`api/openapi.yaml`:

- im Error-Enum nach `plugin_incompatible` `backups_unavailable, backup_running` einfügen (vor `internal`);
- `ServerSettings` ersetzen:

```yaml
    ServerSettings:
      type: object
      required: [base_domain, curseforge_api_key_set, backup_timezone, docker, unknown_containers]
      properties:
        base_domain: { type: [string, 'null'] }
        curseforge_api_key_set: { type: boolean }
        backup_timezone: { type: string, description: 'IANA time zone of all backup schedules (default Europe/Berlin)' }
        docker: { type: string, enum: [ok, unavailable, unknown] }
        unknown_containers: { type: array, items: { $ref: '#/components/schemas/UnknownContainer' } }
```

- `UpdateServerSettingsRequest`: Beschreibung zu `'Omitted fields stay unchanged; null removes the value (not allowed for backup_timezone).'` und Eigenschaft ergänzen:

```yaml
        backup_timezone: { type: string, minLength: 1, maxLength: 64, description: 'IANA time zone such as Europe/Berlin' }
```

- `deleteServer`: unter `parameters` nach `IdPath` ergänzen und eine `400`-Antwort aufnehmen:

```yaml
        - { name: purge_backups, in: query, required: false, description: 'true or 1: also delete the restic repository and key of the server (default false: they stay on the host)', schema: { type: boolean } }
```

```yaml
        '400': { $ref: '#/components/responses/BadRequest' }
```

Run: `make generate`

`web/src/api/errors.ts`, `KNOWN_CODES` nach `'plugin_incompatible',`:

```ts
  'backups_unavailable',
  'backup_running',
```

`web/src/i18n/i18n.test.ts`, `ERROR_CODES` gleich ergänzen.

`web/src/i18n/de.ts`, `errors` nach `plugin_incompatible`:

```ts
    backups_unavailable: 'Backups sind auf diesem Host nicht verfügbar (restic fehlt).',
    backup_running: 'Für diesen Server läuft oder wartet bereits ein Backup-Auftrag.',
```

`web/src/i18n/en.ts`, `errors` nach `plugin_incompatible`:

```ts
    backups_unavailable: 'Backups are not available on this host (restic is missing).',
    backup_running: 'A backup job of this server is already queued or running.',
```

`web/src/mocks/db.ts`:

- `MockServerSettings` um `backup_timezone: string` ergänzen;
- im Seed `serverSettings: { base_domain: 'mc.example.com', curseforge_api_key_set: false, backup_timezone: 'Europe/Berlin', docker: 'ok' },`.

`web/src/mocks/handlers/servers.ts`:

- über `settingsBody` ergänzen:

```ts
/** Like filesapi.ValidTimezone: an IANA name the browser knows, not empty, at most 64 characters. */
function validTimezone(tz: unknown): tz is string {
  if (typeof tz !== 'string' || tz === '' || tz === 'Local' || tz.length > 64) return false
  try {
    new Intl.DateTimeFormat('en', { timeZone: tz })
    return true
  } catch {
    return false
  }
}
```

- in `updateServerSettings` nach dem CurseForge-Block:

```ts
      if (body.backup_timezone !== undefined) {
        if (!validTimezone(body.backup_timezone)) return apiError(400, 'validation_failed', 'backup_timezone must be an IANA time zone such as Europe/Berlin')
        if (body.backup_timezone !== db.serverSettings.backup_timezone) {
          details.backup_timezone = { old: db.serverSettings.backup_timezone, new: body.backup_timezone }
          db.serverSettings.backup_timezone = body.backup_timezone
        }
      }
```

- `deleteServer` ersetzen:

```ts
  op(
    'deleteServer',
    http.delete(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const raw = new URL(request.url).searchParams.get('purge_backups')
      if (raw !== null && !['1', 'true', '0', 'false'].includes(raw)) return apiError(400, 'validation_failed', 'purge_backups must be true, false, 1 or 0')
      const purge = raw === '1' || raw === 'true'
      const id = idParam(params)
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      db.servers = db.servers.filter((s) => s.id !== id)
      for (const [userId, grants] of db.grants) db.grants.set(userId, grants.filter((gr) => gr.server_id !== id))
      const details: Record<string, unknown> = { name: server.name }
      if (purge) {
        // Task 3 adds db.backups/db.backupJobs; until then there is nothing to purge.
        details.backups_purged = true
      }
      recordAudit(g.user, 'server.delete', `server:${server.id}`, 'success', details)
      return noContent()
    }),
  ),
```

`web/src/mocks/handlers.test.ts`, Test „keeps server settings …“: die erste Erwartung durch

```ts
    expect(await get()).toEqual({ base_domain: 'mc.example.com', curseforge_api_key_set: false, backup_timezone: 'Europe/Berlin', docker: 'ok', unknown_containers: [] })
```

ersetzen und vor dem Testende anfügen:

```ts
    await callApi('PUT', '/servers/settings', { body: { backup_timezone: 'America/New_York' }, csrf: me.csrf_token })
    expect(field(await get(), 'backup_timezone')).toBe('America/New_York')
    for (const tz of [null, '', 'Mars/Olympus']) {
      const res = await callApi('PUT', '/servers/settings', { body: { backup_timezone: tz }, csrf: me.csrf_token })
      expect(field(res.body, 'error')).toBe('validation_failed')
    }
```

`web/src/features/settings/settings-page.test.tsx`, in der Vorlage `HttpResponse.json<ServerSettings>({…})` nach `curseforge_api_key_set: false,` die Zeile `backup_timezone: 'Europe/Berlin',` ergänzen.

- [ ] **Step 8: `make check`, Tests auf der VM**

Run: `make check`
Expected: PASS.

Run: `make test-vm SUITE=go` und `make test-vm SUITE=web`
Expected: PASS, insbesondere `TestBackupScheduleDefaultsAndRoundTrip`, `TestBackupTimezoneSetting`, `TestBackupScheduleChecksAndCascade`, `TestDeletePurgesBackupsOnlyOnRequest`, `TestDeleteSurvivesAFailedPurge`, `TestUpdateBackupTimezone`, `TestErrorCodesMatchOpenAPI`, `TestDeleteServerRemovesContainerFirst` (unverändert grün), Vitest `handlers.test.ts` und `i18n.test.ts`.

- [ ] **Step 9: Commit, Push, CI**

```bash
git add internal/store internal/httpx/errors.go internal/audit/types.go internal/notify internal/servers \
  api/openapi.yaml web/src/api web/src/i18n web/src/mocks web/src/features/settings/settings-page.test.tsx
git commit -m "feat(backups): schedule table, codes, audit actions, time zone setting and purge on delete

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks --watch --interval 30
```

Expected: `CI OK` grün (inkl. „Check sqlc output is up to date“ und „Generated files are committed“).

---

### Task 3: panel-Vertrag – OpenAPI, Stub `internal/backups`, Verdrahtung, Mocks, restic für Tests

**Files:**
- Modify: `api/openapi.yaml` (Tag `backups`, acht Operationen, Parameter `SnapshotPath`, Schemas), `web/src/api/schema.d.ts` (generiert)
- Modify: `web/src/api/types.ts` (Aliase)
- Create: `internal/backups/handler.go`, `internal/backups/routes_test.go`
- Modify: `cmd/panel/app.go` (`appDeps.Backups`, `backupsH`, `AnyGate`, `Backups`, Routen, `Run`), `cmd/panel/main.go` (`Backups: fc`, `_ "time/tzdata"`)
- Create: `web/src/mocks/handlers/backups.ts`
- Modify: `web/src/mocks/db.ts` (`MockBackups`, `MockBackupJob`, `backups`, `backupJobs`, Seed), `web/src/mocks/handlers.ts`, `web/src/mocks/handlers/servers.ts` (Purge im Löschen), `web/src/mocks/handlers.test.ts`
- Create: `scripts/install-restic.sh` (gemeinsame, gepinnte Installation nach `/usr/local/bin/restic`)
- Modify: `scripts/vm-test.sh` (`need_restic` ruft das Skript, `MCPANEL_TEST_RESTIC`), `.github/workflows/ci.yml` (Go-Job: Skript aufrufen, Variable setzen)

**Interfaces:**
- Consumes: Aufgaben 1–2 (`filesapi.BackupClient`, `servers.AnyGate`, `servers.Deps.Gate/Backups`, `httpx.CodeBackupsUnavailable`, `notify.Notifier`).
- Produces: V3 (OpenAPI), V4 (`backups.Deps`, `NewHandler`, `Routes`, `Run`, `DataJobRunning`, `PurgeBackups`), V6 (Mock), `appDeps.Backups filesapi.BackupClient`, TS-Aliase:

```ts
BackupKind, BackupSnapshot, BackupJobKind, BackupJobTrigger, BackupJobState, BackupJobStep, BackupJobError,
BackupJob, BackupJobResponse, BackupJobStarted, BackupScheduleMode, BackupLastStatus, BackupSchedule, BackupScheduleResponse,
UpdateBackupScheduleRequest, BackupOverview, CreateBackupRequest, PinBackupRequest, BackupSnapshotResponse
```

und im Mock `BACKUP_JOB_MS = 4000` (exportiert aus `web/src/mocks/handlers/backups.ts`), `db.backups`, `db.backupJobs`, `mockSnapshotId(n: number): string` (exportiert aus `db.ts`).

- [ ] **Step 1: OpenAPI erweitern, `make generate`**

Tag einfügen (nach `plugins`):

```yaml
  - { name: backups, description: 'Backups of a server: restic snapshots, schedule and retention, restore, download' }
```

Unter `components.parameters` nach `PluginProjectQuery`:

```yaml
    SnapshotPath: { name: snapshot, in: path, required: true, description: 'restic snapshot ID (64 lowercase hex characters)', schema: { type: string, pattern: '^[0-9a-f]{64}$' } }
```

Pfade nach `/servers/{id}/plugins/icon` (vor `/audit`) einfügen:

```yaml
  /servers/{id}/backups:
    get:
      operationId: getBackups
      tags: [backups]
      summary: Snapshots, current job and schedule of a server
      description: 'The snapshots come from the restic repository of the server, newest first. Without restic on the host or without the file service, available is false and snapshots is empty (no error).'
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/BackupOverview' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
    post:
      operationId: createBackup
      tags: [backups]
      summary: Queue a manual backup (consistent through save-off/save-all flush/save-on while the server runs; audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/CreateBackupRequest' } } }
      responses:
        '202': { description: Job queued, content: { application/json: { schema: { $ref: '#/components/schemas/BackupJobStarted' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'backup_running (a backup, restore or delete job of this server is queued or running) or conflict (the server has no instance or data directory yet)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { description: 'backups_unavailable (restic is missing on the host) or files_unavailable (file service)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
  /servers/{id}/backups/job:
    get:
      operationId: getBackupJob
      tags: [backups]
      summary: The queued, running or last backup job of the server (null before the first one)
      parameters:
        - $ref: '#/components/parameters/IdPath'
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/BackupJobResponse' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
  /servers/{id}/backups/schedule:
    put:
      operationId: updateBackupSchedule
      tags: [backups]
      summary: Set schedule and retention of the server (audited)
      description: 'Times are in the panel time zone (ServerSettings.backup_timezone). The retention applies only to scheduled snapshots; manual, pre-restore and pinned ones stay.'
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/UpdateBackupScheduleRequest' } } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/BackupScheduleResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
  /servers/{id}/backups/{snapshot}:
    delete:
      operationId: deleteBackup
      tags: [backups]
      summary: Queue deleting one snapshot (also pinned and pre-restore ones; sudo; audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/SnapshotPath'
        - $ref: '#/components/parameters/CSRF'
      responses:
        '202': { description: Job queued, content: { application/json: { schema: { $ref: '#/components/schemas/BackupJobStarted' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'backup_running (a backup job of this server is queued or running)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { description: 'backups_unavailable or files_unavailable', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
  /servers/{id}/backups/{snapshot}/pin:
    post:
      operationId: pinBackup
      tags: [backups]
      summary: Pin or unpin a scheduled snapshot (pinned ones survive the retention; the snapshot gets a new ID; audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/SnapshotPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/PinBackupRequest' } } }
      responses:
        '200': { description: The snapshot with its new ID, content: { application/json: { schema: { $ref: '#/components/schemas/BackupSnapshotResponse' } } } }
        '400': { description: 'validation_failed (also: not a scheduled snapshot)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'backup_running (a backup job of this server is queued or running)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { description: 'backups_unavailable or files_unavailable', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
  /servers/{id}/backups/{snapshot}/restore:
    post:
      operationId: restoreBackup
      tags: [backups]
      summary: Queue a full restore (stops the server, takes a pre-restore snapshot, restores, starts it again if it should run; sudo; audited)
      description: 'Starts and restarts of the server answer 409 until the job ends. Not enough disk space shows as error_code insufficient_storage in the job.'
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/SnapshotPath'
        - $ref: '#/components/parameters/CSRF'
      responses:
        '202': { description: Job queued, content: { application/json: { schema: { $ref: '#/components/schemas/BackupJobStarted' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { description: 'backup_running (a backup job of this server is queued or running) or conflict (the server has no instance)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '503': { description: 'backups_unavailable, files_unavailable or agent_unavailable', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
  /servers/{id}/backups/{snapshot}/download:
    get:
      operationId: downloadBackup
      tags: [backups]
      summary: Download a snapshot as a zip stream (audited)
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/SnapshotPath'
      responses:
        '200':
          description: 'Zip stream with Content-Disposition attachment; filename "<server>-<YYYYMMDD-HHMMSS>.zip"'
          content:
            application/zip: { schema: { type: string, format: binary } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { description: 'backups_unavailable or files_unavailable', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```

Schemas nach `PluginJobResponse` (vor `PermissionGrant`) einfügen:

```yaml
    BackupKind: { type: string, enum: [scheduled, manual, pre-restore] }
    BackupSnapshot:
      type: object
      required: [id, short_id, time, kind, pinned, note, bytes]
      properties:
        id: { type: string, pattern: '^[0-9a-f]{64}$' }
        short_id: { type: string }
        time: { type: integer, format: int64, description: Unix ms }
        kind: { $ref: '#/components/schemas/BackupKind' }
        pinned: { type: boolean, description: 'Only scheduled snapshots can be pinned; pinned ones survive the retention' }
        note: { type: [string, 'null'] }
        bytes: { type: integer, format: int64, description: Size of the backed up data }
    BackupJobKind: { type: string, enum: [backup, restore, delete] }
    BackupJobTrigger: { type: string, enum: [manual, scheduled] }
    BackupJobState: { type: string, enum: [queued, running, done, failed] }
    BackupJobStep: { type: string, enum: [queued, saving, backing_up, resuming, pruning, stopping, pre_restore, restoring, deleting, done] }
    BackupJobError: { type: string, enum: [rcon_unavailable, agent_unavailable, files_unavailable, files_busy, backups_unavailable, insufficient_storage, too_large, stop_timeout, canceled, internal] }
    BackupJob:
      type: object
      required: [id, kind, trigger, state, step, queue_position, bytes, snapshot, error_code, error, queued_at, started_at, finished_at]
      properties:
        id: { type: string }
        kind: { $ref: '#/components/schemas/BackupJobKind' }
        trigger: { $ref: '#/components/schemas/BackupJobTrigger' }
        state: { $ref: '#/components/schemas/BackupJobState' }
        step: { $ref: '#/components/schemas/BackupJobStep' }
        queue_position: { type: integer, description: 'Position in the panel-wide queue (1 = next); 0 = not waiting' }
        bytes: { type: integer, format: int64, description: 'Bytes read (backup) resp. written (restore) so far' }
        snapshot: { type: [string, 'null'], description: 'Backup: the new snapshot once done; restore and delete: the source snapshot' }
        error_code: { anyOf: [{ $ref: '#/components/schemas/BackupJobError' }, { type: 'null' }] }
        error: { type: string }
        queued_at: { type: integer, format: int64, description: Unix ms }
        started_at: { type: integer, format: int64, description: 0 while queued }
        finished_at: { type: integer, format: int64, description: 0 while queued or running }
    BackupJobResponse:
      type: object
      required: [job]
      properties:
        job: { anyOf: [{ $ref: '#/components/schemas/BackupJob' }, { type: 'null' }] }
    BackupJobStarted:
      type: object
      required: [job]
      description: 'Answer of createBackup, restoreBackup and deleteBackup: the queued job'
      properties:
        job: { $ref: '#/components/schemas/BackupJob' }
    BackupScheduleMode: { type: string, enum: ['off', interval, daily, weekly] }
    BackupLastStatus: { type: string, enum: [ok, failed, running] }
    BackupSchedule:
      type: object
      required: [mode, interval_hours, time_of_day, weekday, keep_last, keep_daily, keep_weekly, keep_monthly, timezone, next_run_at, last_run_at, last_status, last_error, last_snapshot_id]
      properties:
        mode: { $ref: '#/components/schemas/BackupScheduleMode' }
        interval_hours: { type: integer, minimum: 1, maximum: 168 }
        time_of_day: { type: integer, minimum: 0, maximum: 1439, description: Minutes after midnight in the panel time zone }
        weekday: { type: integer, minimum: 0, maximum: 6, description: '0 = Sunday' }
        keep_last: { type: integer, minimum: 0, maximum: 1000 }
        keep_daily: { type: integer, minimum: 0, maximum: 1000 }
        keep_weekly: { type: integer, minimum: 0, maximum: 1000 }
        keep_monthly: { type: integer, minimum: 0, maximum: 1000 }
        timezone: { type: string, description: 'The panel time zone (ServerSettings.backup_timezone)' }
        next_run_at: { type: [integer, 'null'], format: int64, description: 'Unix ms; null with mode off' }
        last_run_at: { type: [integer, 'null'], format: int64, description: 'Start of the last scheduled run, Unix ms' }
        last_status: { anyOf: [{ $ref: '#/components/schemas/BackupLastStatus' }, { type: 'null' }] }
        last_error: { type: [string, 'null'] }
        last_snapshot_id: { type: [string, 'null'] }
    BackupScheduleResponse:
      type: object
      required: [schedule]
      properties:
        schedule: { $ref: '#/components/schemas/BackupSchedule' }
    UpdateBackupScheduleRequest:
      type: object
      additionalProperties: false
      required: [mode, interval_hours, time_of_day, weekday, keep_last, keep_daily, keep_weekly, keep_monthly]
      description: 'With a mode other than off at least one keep_* value must be > 0.'
      properties:
        mode: { $ref: '#/components/schemas/BackupScheduleMode' }
        interval_hours: { type: integer, minimum: 1, maximum: 168 }
        time_of_day: { type: integer, minimum: 0, maximum: 1439 }
        weekday: { type: integer, minimum: 0, maximum: 6 }
        keep_last: { type: integer, minimum: 0, maximum: 1000 }
        keep_daily: { type: integer, minimum: 0, maximum: 1000 }
        keep_weekly: { type: integer, minimum: 0, maximum: 1000 }
        keep_monthly: { type: integer, minimum: 0, maximum: 1000 }
    BackupOverview:
      type: object
      required: [available, snapshots, job, schedule]
      properties:
        available: { type: boolean, description: 'false: restic missing on the host or file service unreachable' }
        snapshots: { type: array, items: { $ref: '#/components/schemas/BackupSnapshot' } }
        job: { anyOf: [{ $ref: '#/components/schemas/BackupJob' }, { type: 'null' }] }
        schedule: { $ref: '#/components/schemas/BackupSchedule' }
    CreateBackupRequest:
      type: object
      additionalProperties: false
      properties:
        note: { type: [string, 'null'], maxLength: 64, description: 'At most 64 characters, no control characters; null or empty = no note' }
    PinBackupRequest:
      type: object
      additionalProperties: false
      required: [pinned]
      properties:
        pinned: { type: boolean }
    BackupSnapshotResponse:
      type: object
      required: [snapshot]
      properties:
        snapshot: { $ref: '#/components/schemas/BackupSnapshot' }
```

Run: `make generate`
Expected: `web/src/api/schema.d.ts` enthält `getBackups` … `downloadBackup` und die Schemas.

- [ ] **Step 2: Go-Tests schreiben (rot)**

Neue Datei `internal/backups/routes_test.go`:

```go
package backups_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var (
	backupsRule = rbac.Server(rbac.PermServerBackups, "id")
	snap        = strings.Repeat("ab", 32)
)

// wantRoutes are exactly the backups operations of api/openapi.yaml (V3).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/backups":                      backupsRule,
	"POST /api/servers/{id}/backups":                     backupsRule,
	"GET /api/servers/{id}/backups/job":                  backupsRule,
	"PUT /api/servers/{id}/backups/schedule":             backupsRule,
	"POST /api/servers/{id}/backups/{snapshot}/pin":      backupsRule,
	"POST /api/servers/{id}/backups/{snapshot}/restore":  backupsRule.WithSudo(),
	"DELETE /api/servers/{id}/backups/{snapshot}":        backupsRule.WithSudo(),
	"GET /api/servers/{id}/backups/{snapshot}/download":  backupsRule,
}

type env struct {
	db  *sql.DB
	mux *chi.Mux
	rt  *rbac.Router
	h   *backups.Handler
}

// newEnv mounts the handler under /api on a real rbac.Router; no files service, agent or
// notifier: the defaults must build.
func newEnv(t *testing.T) env {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	h := backups.NewHandler(backups.Deps{
		DB: db, Clock: clk, Audit: &audit.MemoryRecorder{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) { h.Routes(api) })
	mux.Handle("/*", http.NotFoundHandler())
	return env{db: db, mux: mux, rt: rt, h: h}
}

func (e env) do(method, path string, p *principal.Principal) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPost || method == http.MethodPut {
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

func expand(pattern string) string {
	return strings.NewReplacer("{id}", "1", "{snapshot}", snap).Replace(pattern)
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

// Phase A: every route answers 503 backups_unavailable until phase B2 implements it.
func TestStubsAnswerNotImplemented(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "root", "admin")
	admin := principal.Principal{
		UserID: uid, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1,
		SudoUntil: t0.Add(5 * time.Minute),
	}
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		path := expand(pattern)
		rec := e.do(method, path, &admin)
		var body httpx.ErrorBody
		if rec.Code != http.StatusServiceUnavailable || json.Unmarshal(rec.Body.Bytes(), &body) != nil ||
			body.Error != httpx.CodeBackupsUnavailable || body.Message != "backups not implemented yet" {
			t.Errorf("%s: %d %s", key, rec.Code, rec.Body)
		}
		if rec := e.do(method, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
	}
}

// A user without server.backups is refused; restore and delete need sudo even for admins.
func TestStubsCheckPermissionAndSudo(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "bob", "user")
	bob := principal.Principal{UserID: uid, Username: "bob", Role: principal.RoleUser, Stage: principal.StageFull, SessionID: 2}
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		if rec := e.do(method, expand(pattern), &bob); rec.Code != http.StatusForbidden {
			t.Errorf("%s without server.backups: %d", key, rec.Code)
		}
	}
	aid := storetest.CreateUser(t, e.db, "root", "admin")
	noSudo := principal.Principal{UserID: aid, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 3}
	for _, key := range []string{"POST /api/servers/{id}/backups/{snapshot}/restore", "DELETE /api/servers/{id}/backups/{snapshot}"} {
		method, pattern, _ := strings.Cut(key, " ")
		rec := e.do(method, expand(pattern), &noSudo)
		var body httpx.ErrorBody
		if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error != httpx.CodeSudoRequired {
			t.Errorf("%s without sudo: %d %s", key, rec.Code, rec.Body)
		}
	}
}

func TestStubGateAndPurge(t *testing.T) {
	e := newEnv(t)
	if e.h.DataJobRunning(context.Background(), 1) {
		t.Fatal("the stub holds starts")
	}
	if err := e.h.PurgeBackups(context.Background(), 1); err != nil {
		t.Fatalf("PurgeBackups = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
```


- [ ] **Step 3: `make check` (rot)**

Run: `git add -N internal/backups && make check`
Expected: FAIL – `package github.com/Bernotese/mc-panel/internal/backups` fehlt.

- [ ] **Step 4: Stub-Paket `internal/backups`**

Neue Datei `internal/backups/handler.go`:

```go
// Package backups implements the backups of a Minecraft server (sub-project 4): manual and
// scheduled restic snapshots that are consistent while the server runs (save-off, save-all
// flush, save-on over RCON), retention, pinning, a full restore with a safety snapshot and the
// zip download. restic runs only inside the unprivileged mcpanel-files service; this package
// orchestrates it (queue, jobs, schedule ticker) and never touches files itself.
//
// Phase A of the plan registers the routes as stubs; phase B2 implements them.
package backups

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the backup handlers (contract V4).
type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Notifier  notify.Notifier          // nil = notify.Nop{}
	Files     filesapi.BackupClient    // nil = no service: overview available=false, actions 503 files_unavailable
	Instances agentapi.InstancesClient // actual state + power stop; nil = treat as stopped, restore fails agent_unavailable
	Console   agentapi.ConsoleClient   // RCON; nil = running servers fail rcon_unavailable
	Reconcile func()                   // asks servers for an immediate reconcile pass; nil = no-op
	Log       *slog.Logger             // nil = slog.Default()
}

type Handler struct {
	q         *store.Queries
	clock     clock.Clock
	rec       audit.Recorder
	notifier  notify.Notifier
	files     filesapi.BackupClient
	inst      agentapi.InstancesClient
	console   agentapi.ConsoleClient
	reconcile func()
	log       *slog.Logger
}

func NewHandler(d Deps) *Handler {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	n := d.Notifier
	if n == nil {
		n = notify.Nop{}
	}
	rc := d.Reconcile
	if rc == nil {
		rc = func() {}
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, notifier: n, files: d.Files,
		inst: d.Instances, console: d.Console, reconcile: rc, log: log,
	}
}

// Routes registers the backup routes (paths relative to /api, contract V3), all with
// server.backups (admins always); restore and delete additionally need sudo.
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerBackups, "id")
	api.Get("/servers/{id}/backups", rule, h.notImplemented)
	api.Post("/servers/{id}/backups", rule, h.notImplemented)
	api.Get("/servers/{id}/backups/job", rule, h.notImplemented)
	api.Put("/servers/{id}/backups/schedule", rule, h.notImplemented)
	api.Post("/servers/{id}/backups/{snapshot}/pin", rule, h.notImplemented)
	api.Post("/servers/{id}/backups/{snapshot}/restore", rule.WithSudo(), h.notImplemented)
	api.Delete("/servers/{id}/backups/{snapshot}", rule.WithSudo(), h.notImplemented)
	api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.notImplemented)
}

// Run blocks until ctx is done (phase B2: recovery, ticker, queue worker).
func (h *Handler) Run(ctx context.Context) {
	<-ctx.Done()
}

// DataJobRunning is true while the panel holds a restore job for the server (servers.DataGate;
// phase A has no jobs).
func (h *Handler) DataJobRunning(context.Context, int64) bool { return false }

// PurgeBackups deletes the backups of a deleted server (servers.BackupPurger; phase A has
// nothing to delete).
func (h *Handler) PurgeBackups(context.Context, int64) error { return nil }

// notImplemented answers every route until phase B2 replaces the handlers.
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, "backups not implemented yet")
}
```

Phase A liest die Felder `q`, `clock`, `rec`, `notifier`, `files`, `inst`, `console`, `reconcile` und `log` noch nicht; das Setzen im Composite-Literal reicht `unused` (wie bei den Stubs von 3b/3c). Meldet golangci-lint trotzdem `U1000`, bekommt nur dieses Feld `//nolint:unused // read by phase B2 (plan 03)`.

- [ ] **Step 5: Verdrahtung in `cmd/panel`**

`cmd/panel/app.go`:

- Import `"github.com/Bernotese/mc-panel/internal/backups"` ergänzen;
- in `appDeps` nach `Files`:

```go
	// Backups is the backup API of the files service (in production the same *filesapi.UnixClient
	// as Files); nil means no service: the backups tab reports "not available".
	Backups filesapi.BackupClient
```

- den Block von `serversH := servers.NewHandler(` bis zu seiner schließenden Klammer ersetzen durch:

```go
	// The backups handler needs servers for an immediate reconcile pass after a restore, the
	// servers handler needs backups as data gate and purger: serversH is assigned right below.
	var serversH *servers.Handler
	backupsH := backups.NewHandler(backups.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Notifier: disp, Files: d.Backups,
		Instances: d.Instances, Console: d.Console,
		Reconcile: func() { serversH.Trigger() },
	})
	serversH = servers.NewHandler(servers.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Box: box, Instances: d.Instances,
		Catalog: cat,
		// The panel's own restore hold first (a map lookup), then the file service's jobs.
		Gate:    servers.AnyGate(backupsH, filesH),
		Backups: backupsH,
	})
```

- in `rt.Route` nach `pluginsH.Routes(api)`: `backupsH.Routes(api)`;
- in `run` `wg.Add(5)` → `wg.Add(6)` und nach der `pluginsH`-Zeile:

```go
		go func() { defer wg.Done(); backupsH.Run(ctx) }()
```

Wichtig: `d.Backups` ist ein Interface. Ein nicht gesetztes Feld bleibt ein echtes `nil`-Interface, das `backups.NewHandler` als „kein Dienst“ erkennt. Nie einen typisierten `nil`-Zeiger zuweisen.

`cmd/panel/main.go`:

- Import `_ "time/tzdata" // backup schedules and their time zone on hosts without zoneinfo` in den Standardbibliotheks-Block;
- `appDeps{…, Files: fc, Version: version}` → `appDeps{…, Files: fc, Backups: fc, Version: version}`.

- [ ] **Step 6: TS-Typ-Aliase**

`web/src/api/types.ts` nach `PluginJobResponse`:

```ts
export type BackupKind = Schemas['BackupKind']
export type BackupSnapshot = Schemas['BackupSnapshot']
export type BackupJobKind = Schemas['BackupJobKind']
export type BackupJobTrigger = Schemas['BackupJobTrigger']
export type BackupJobState = Schemas['BackupJobState']
export type BackupJobStep = Schemas['BackupJobStep']
export type BackupJobError = Schemas['BackupJobError']
export type BackupJob = Schemas['BackupJob']
export type BackupJobResponse = Schemas['BackupJobResponse']
export type BackupJobStarted = Schemas['BackupJobStarted']
export type BackupScheduleMode = Schemas['BackupScheduleMode']
export type BackupLastStatus = Schemas['BackupLastStatus']
export type BackupSchedule = Schemas['BackupSchedule']
export type BackupScheduleResponse = Schemas['BackupScheduleResponse']
export type UpdateBackupScheduleRequest = Schemas['UpdateBackupScheduleRequest']
export type BackupOverview = Schemas['BackupOverview']
export type CreateBackupRequest = Schemas['CreateBackupRequest']
export type PinBackupRequest = Schemas['PinBackupRequest']
export type BackupSnapshotResponse = Schemas['BackupSnapshotResponse']
```

- [ ] **Step 7: Mock-Tests schreiben (rot)**

`web/src/mocks/handlers.test.ts`:

- Import ergänzen: `import type { BackupJob, BackupOverview, BackupSnapshot } from '@/api/types'`;
- in „mocks every operationId …“ `toHaveLength(87)` → `toHaveLength(95)`;
- im `describe('mock behaviour', …)` anfügen:

```ts
  it('lists backups and runs a manual backup as a job, one per server', async () => {
    const me = loginAs('admin')
    const overview = (await callApi('GET', '/servers/1/backups')).body as BackupOverview
    expect(overview.available).toBe(true)
    expect(overview.snapshots.map((s) => s.kind)).toEqual(['scheduled', 'manual', 'scheduled', 'pre-restore', 'scheduled', 'scheduled'])
    expect(overview.snapshots.filter((s) => s.pinned)).toHaveLength(1)
    expect(overview.snapshots[1].note).toBe('vor Update auf 1.21.11')
    expect(overview.schedule).toMatchObject({ mode: 'daily', time_of_day: 240, keep_last: 24, timezone: 'Europe/Berlin', last_status: 'ok' })
    expect(overview.job).toBeNull()

    const create = (body: unknown) => callApi('POST', '/servers/1/backups', { body, csrf: me.csrf_token })
    const started = await create({ note: 'vor dem Umbau' })
    expect(started.status).toBe(202)
    expect(field(started.body, 'job')).toMatchObject({ kind: 'backup', trigger: 'manual', state: 'queued', step: 'queued', queue_position: 1 })
    expect(field((await create({})).body, 'error')).toBe('backup_running')
    db.backupJobs[1].startedMs -= 60_000
    const job = field((await callApi('GET', '/servers/1/backups/job')).body, 'job') as BackupJob
    expect(job).toMatchObject({ state: 'done', step: 'done', queue_position: 0 })
    const after = (await callApi('GET', '/servers/1/backups')).body as BackupOverview
    expect(after.snapshots[0]).toMatchObject({ kind: 'manual', note: 'vor dem Umbau', id: job.snapshot })
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.create', target: 'server:1', details: { note: 'vor dem Umbau' } })

    expect(field((await create({ note: 'a\nb' })).body, 'error')).toBe('validation_failed')
    expect(field((await create({ note: 'x'.repeat(65) })).body, 'error')).toBe('validation_failed')
    expect(field((await create({ extra: 1 })).body, 'error')).toBe('bad_request')
  })

  it('pins scheduled snapshots only and restores or deletes with sudo', async () => {
    let me = loginAs('admin')
    const list = async () => ((await callApi('GET', '/servers/1/backups')).body as BackupOverview).snapshots
    const [scheduled, manual] = await list()
    const pin = await callApi('POST', `/servers/1/backups/${scheduled.id}/pin`, { body: { pinned: true }, csrf: me.csrf_token })
    const pinned = field(pin.body, 'snapshot') as BackupSnapshot
    expect(pinned.pinned).toBe(true)
    expect(pinned.id).not.toBe(scheduled.id)
    expect(pinned.id).toMatch(/^[0-9a-f]{64}$/)
    expect((await list())[0].id).toBe(pinned.id)
    const pinManual = await callApi('POST', `/servers/1/backups/${manual.id}/pin`, { body: { pinned: true }, csrf: me.csrf_token })
    expect(field(pinManual.body, 'error')).toBe('validation_failed')
    expect((await callApi('POST', `/servers/1/backups/${'0'.repeat(64)}/pin`, { body: { pinned: true }, csrf: me.csrf_token })).status).toBe(404)
    expect((await callApi('POST', '/servers/1/backups/nope/pin', { body: { pinned: true }, csrf: me.csrf_token })).status).toBe(400)

    const restore = (id: string) => callApi('POST', `/servers/1/backups/${id}/restore`, { csrf: me.csrf_token })
    expect(field((await restore(manual.id)).body, 'error')).toBe('sudo_required')
    me = loginAs('admin', { sudo: true })
    const started = await restore(manual.id)
    expect(started.status).toBe(202)
    expect(field(started.body, 'job')).toMatchObject({ kind: 'restore', snapshot: manual.id })
    db.backupJobs[1].startedMs -= 60_000
    expect(field((await callApi('GET', '/servers/1/backups/job')).body, 'job')).toMatchObject({ state: 'done' })
    expect((await list())[0].kind).toBe('pre-restore')
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.restore', details: { snapshot: manual.id } })

    const del = await callApi('DELETE', `/servers/1/backups/${manual.id}`, { csrf: me.csrf_token })
    expect(del.status).toBe(202)
    db.backupJobs[1].startedMs -= 60_000
    await callApi('GET', '/servers/1/backups/job')
    expect((await list()).some((s) => s.id === manual.id)).toBe(false)
  })

  it('validates schedules, reports unavailable backups and downloads a zip', async () => {
    const me = loginAs('admin')
    const put = (id: number, body: unknown) => callApi('PUT', `/servers/${id}/backups/schedule`, { body, csrf: me.csrf_token })
    const weekly = { mode: 'weekly', interval_hours: 24, time_of_day: 600, weekday: 6, keep_last: 0, keep_daily: 0, keep_weekly: 8, keep_monthly: 0 }
    const ok = await put(2, weekly)
    expect(ok.status).toBe(200)
    const schedule = field(ok.body, 'schedule') as BackupOverview['schedule']
    expect(schedule).toMatchObject({ mode: 'weekly', weekday: 6, keep_weekly: 8 })
    expect(schedule.next_run_at).toBeGreaterThan(Date.now())
    expect(field((await put(2, { ...weekly, keep_weekly: 0 })).body, 'error')).toBe('validation_failed')
    expect(field((await put(2, { ...weekly, time_of_day: 1440 })).body, 'error')).toBe('validation_failed')
    expect(field((await put(2, { ...weekly, mode: 'hourly' })).body, 'error')).toBe('validation_failed')
    expect(field((await put(2, { ...weekly, extra: true })).body, 'error')).toBe('bad_request')
    expect(field((await put(2, { ...weekly, mode: 'off', keep_weekly: 0 })).body, 'schedule')).toMatchObject({ mode: 'off', next_run_at: null })
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.schedule_update', target: 'server:2' })

    expect(((await callApi('GET', '/servers/4/backups')).body as BackupOverview).available).toBe(false)
    expect(field((await callApi('POST', '/servers/4/backups', { body: {}, csrf: me.csrf_token })).body, 'error')).toBe('backups_unavailable')
    expect(field((await callApi('POST', '/servers/3/backups', { body: {}, csrf: me.csrf_token })).body, 'error')).toBe('conflict')
    expect(((await callApi('GET', '/servers/3/backups')).body as BackupOverview).snapshots).toEqual([])

    const snap = ((await callApi('GET', '/servers/1/backups')).body as BackupOverview).snapshots[0]
    const res = await fetch(`${window.location.origin}/api/servers/1/backups/${snap.id}/download`)
    expect(res.status).toBe(200)
    expect(res.headers.get('Content-Type')).toBe('application/zip')
    expect(res.headers.get('Content-Disposition')).toMatch(/^attachment; filename="survival-\d{8}-\d{6}\.zip"$/)
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.download', details: { snapshot: snap.id } })
  })

  it('requires server.backups and purges backups with the deleted server', async () => {
    loginAs('bob')
    expect((await callApi('GET', '/servers/1/backups')).status).toBe(403)
    const me = loginAs('admin', { sudo: true })
    expect(db.backups[1]).toBeDefined()
    expect((await callApi('DELETE', '/servers/1?purge_backups=yes', { csrf: me.csrf_token })).status).toBe(400)
    expect((await callApi('DELETE', '/servers/1?purge_backups=1', { csrf: me.csrf_token })).status).toBe(204)
    expect(db.backups[1]).toBeUndefined()
    expect(db.audit.at(-1)).toMatchObject({ action: 'server.delete', details: { backups_purged: true } })
  })
```

(`bob` hat im Seed keine Grants; `web/src/test/setup.ts` ruft `resetDb()` vor jedem Test auf.)

- [ ] **Step 8: Mock implementieren**

`web/src/mocks/db.ts`:

- Typ-Imports um `BackupJob, BackupJobStep, BackupSchedule, BackupSnapshot` ergänzen;
- nach `MockPluginJob` einfügen:

```ts
/** Backups of one server (V6): restic availability, snapshots (newest first) and the stored schedule. */
export interface MockBackups {
  available: boolean
  snapshots: BackupSnapshot[]
  schedule: Omit<BackupSchedule, 'timezone'> // timezone comes from serverSettings on every read
}

/** What a finished mock job changes. */
export type MockBackupResult =
  | { kind: 'create'; snapshot: BackupSnapshot }
  | { kind: 'restore'; preRestore: BackupSnapshot }
  | { kind: 'delete'; id: string }

export interface MockBackupJob {
  job: BackupJob
  phases: BackupJobStep[] // starts with 'queued'; each phase lasts BACKUP_JOB_MS / phases.length
  startedMs: number
  result: MockBackupResult
}

/** A deterministic 64-hex snapshot ID. */
export function mockSnapshotId(n: number): string {
  return ((n * 2654435761) >>> 0).toString(16).padStart(8, '0').repeat(8)
}

export function defaultBackupSchedule(): MockBackups['schedule'] {
  return {
    mode: 'off', interval_hours: 24, time_of_day: 240, weekday: 0,
    keep_last: 24, keep_daily: 7, keep_weekly: 4, keep_monthly: 6,
    next_run_at: null, last_run_at: null, last_status: null, last_error: null, last_snapshot_id: null,
  }
}

function snapshot(n: number, time: number, kind: BackupSnapshot['kind'], opts: { pinned?: boolean; note?: string; gib?: number } = {}): BackupSnapshot {
  const id = mockSnapshotId(n)
  return { id, short_id: id.slice(0, 8), time, kind, pinned: opts.pinned ?? false, note: opts.note ?? null, bytes: Math.round((opts.gib ?? 1.2) * GIB) }
}

/** survival: six snapshots, daily at 04:00; creative: none, last run failed; modded: restic missing. */
function seedBackups(now: number): Record<number, MockBackups> {
  const survival = [
    snapshot(1, now - 8 * HOUR, 'scheduled'),
    snapshot(2, now - DAY - 3 * HOUR, 'manual', { note: 'vor Update auf 1.21.11' }),
    snapshot(3, now - DAY - 8 * HOUR, 'scheduled'),
    snapshot(4, now - 2 * DAY, 'pre-restore'),
    snapshot(5, now - 3 * DAY - 8 * HOUR, 'scheduled', { pinned: true, gib: 1.1 }),
    snapshot(6, now - 9 * DAY - 8 * HOUR, 'scheduled', { gib: 0.9 }),
  ]
  return {
    1: {
      available: true,
      snapshots: survival,
      schedule: { ...defaultBackupSchedule(), mode: 'daily', next_run_at: now + 16 * HOUR, last_run_at: now - 8 * HOUR, last_status: 'ok', last_snapshot_id: survival[0].id },
    },
    2: {
      available: true,
      snapshots: [],
      schedule: { ...defaultBackupSchedule(), last_run_at: now - 2 * DAY, last_status: 'failed', last_error: 'RCON nicht erreichbar' },
    },
    4: { available: false, snapshots: [], schedule: defaultBackupSchedule() },
  }
}
```

(`GIB`, `HOUR`, `DAY` existieren in `db.ts`; fehlt `GIB` als Export, die vorhandene Konstante nutzen.)

- `MockDb` um `backups: Record<number, MockBackups> // per server id (created on first access)` und `backupJobs: Record<number, MockBackupJob> // queued, running or last job per server id` ergänzen;
- in `seed` nach `pluginJobs: {},`: `backups: seedBackups(now),` und `backupJobs: {},`.

Neue Datei `web/src/mocks/handlers/backups.ts`:

```ts
import { http, HttpResponse } from 'msw'
import type { BackupJob, BackupJobKind, BackupJobStep, BackupOverview, BackupSchedule, BackupSnapshot, UpdateBackupScheduleRequest } from '@/api/types'
import { db, defaultBackupSchedule, nextId, recordAudit, type MockBackupJob, type MockBackupResult, type MockBackups, type MockServer } from '../db'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

/** A mock job takes about this long; its phases (queued first) get equal shares. */
export const BACKUP_JOB_MS = 4000
const SNAPSHOT_RE = /^[0-9a-f]{64}$/
const MAX_NOTE = 64
// eslint-disable-next-line no-control-regex -- matches control characters on purpose (filesapi.ValidNote)
const CONTROL_RE = /[\u0000-\u001f\u007f]/
const SCHEDULE_KEYS = ['mode', 'interval_hours', 'time_of_day', 'weekday', 'keep_last', 'keep_daily', 'keep_weekly', 'keep_monthly'] as const
const MODES = ['off', 'interval', 'daily', 'weekly'] as const
/** The smallest valid zip: an empty end-of-central-directory record. */
const EMPTY_ZIP = Uint8Array.from([0x50, 0x4b, 0x05, 0x06, ...new Array<number>(18).fill(0)])
const HOUR_MS = 3_600_000

type Params = Parameters<typeof idParam>[0]

interface Ctx {
  g: Authed
  server: MockServer
  b: MockBackups
}

/** Session, server.backups and server lookup shared by every operation. */
function access(request: Request, params: Params, opts: { sudo?: boolean } = {}): Ctx | ErrorResponse {
  const g = guard(request, { sudo: opts.sudo })
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes('server.backups')) return apiError(403, 'forbidden', 'missing permission server.backups')
  const server = db.servers.find((s) => s.id === id)
  if (!server) return apiError(404, 'not_found', 'server not found')
  db.backups[id] ??= { available: true, snapshots: [], schedule: defaultBackupSchedule() }
  jobNow(id) // apply a finished job before anything reads the snapshots
  return { g, server, b: db.backups[id] }
}

/** The snapshot of the path, or 400 (malformed) / 404 (unknown). */
function findSnapshot(c: Ctx, params: Params): BackupSnapshot | ErrorResponse {
  const raw = params.snapshot
  const id = typeof raw === 'string' ? raw : ''
  if (!SNAPSHOT_RE.test(id)) return apiError(400, 'validation_failed', 'snapshot must be 64 lowercase hex characters')
  return c.b.snapshots.find((s) => s.id === id) ?? apiError(404, 'not_found', 'snapshot not found')
}

/** Like the panel: no instance → 409 conflict, restic missing → 503, a job → 409 backup_running. */
function canStart(c: Ctx): ErrorResponse | null {
  if (!c.server.instance) return apiError(409, 'conflict', 'the server has no instance yet')
  if (!c.b.available) return apiError(503, 'backups_unavailable', 'restic is not available on this host')
  const job = jobNow(c.server.id)
  if (job && (job.state === 'queued' || job.state === 'running')) return apiError(409, 'backup_running', 'a backup job of this server is queued or running')
  return null
}

function randomId(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(32)), (b) => b.toString(16).padStart(2, '0')).join('')
}

function newSnapshot(kind: BackupSnapshot['kind'], note: string | null, bytes: number): BackupSnapshot {
  const id = randomId()
  return { id, short_id: id.slice(0, 8), time: Date.now(), kind, pinned: false, note, bytes }
}

function startJob(c: Ctx, kind: BackupJobKind, steps: BackupJobStep[], result: MockBackupResult, snapshot: string | null): BackupJob {
  const now = Date.now()
  const job: BackupJob = {
    id: `bj-${nextId()}`, kind, trigger: 'manual', state: 'queued', step: 'queued', queue_position: 1, bytes: 0,
    snapshot, error_code: null, error: '', queued_at: now, started_at: 0, finished_at: 0,
  }
  db.backupJobs[c.server.id] = { job, phases: ['queued', ...steps], startedMs: now, result }
  return { ...job }
}

function applyResult(serverId: number, mj: MockBackupJob): void {
  const b = db.backups[serverId]
  if (!b) return
  const r = mj.result
  switch (r.kind) {
    case 'create':
      b.snapshots.unshift(r.snapshot)
      mj.job.snapshot = r.snapshot.id
      break
    case 'restore':
      b.snapshots.unshift(r.preRestore)
      break
    case 'delete':
      b.snapshots = b.snapshots.filter((s) => s.id !== r.id)
      break
  }
}

/** Advances the job of a server by the elapsed time; a finished job applies its result once. */
function jobNow(serverId: number): BackupJob | null {
  const mj = db.backupJobs[serverId]
  if (!mj) return null
  const { job } = mj
  if (job.state === 'queued' || job.state === 'running') {
    const share = BACKUP_JOB_MS / mj.phases.length
    const i = Math.floor((Date.now() - mj.startedMs) / share)
    if (i >= mj.phases.length) {
      applyResult(serverId, mj)
      Object.assign(job, { state: 'done', step: 'done', queue_position: 0, finished_at: Date.now() })
      if (job.started_at === 0) job.started_at = mj.startedMs + share
    } else {
      job.step = mj.phases[i]
      job.state = i === 0 ? 'queued' : 'running'
      job.queue_position = i === 0 ? 1 : 0
      if (i > 0 && job.started_at === 0) job.started_at = mj.startedMs + share
      if (job.step === 'backing_up' || job.step === 'restoring') job.bytes = Math.round(((i + 1) / mj.phases.length) * 1.2 * 1024 ** 3)
    }
  }
  return { ...job }
}

function scheduleBody(b: MockBackups): BackupSchedule {
  return { ...b.schedule, timezone: db.serverSettings.backup_timezone }
}

/** A rough next run in the browser's time zone (the panel computes it in the panel time zone). */
function nextRun(s: MockBackups['schedule'], now: number): number | null {
  switch (s.mode) {
    case 'off':
      return null
    case 'interval':
      return (s.last_run_at ?? now) + s.interval_hours * HOUR_MS
    case 'daily':
    case 'weekly': {
      const d = new Date(now)
      d.setHours(Math.floor(s.time_of_day / 60), s.time_of_day % 60, 0, 0)
      if (s.mode === 'weekly') d.setDate(d.getDate() + ((s.weekday - d.getDay() + 7) % 7))
      while (d.getTime() <= now) d.setDate(d.getDate() + (s.mode === 'weekly' ? 7 : 1))
      return d.getTime()
    }
  }
}

function validNote(n: string): boolean {
  return [...n].length <= MAX_NOTE && !CONTROL_RE.test(n) && n.trim() !== ''
}

/** "<server>-YYYYMMDD-HHMMSS.zip" in UTC, like the panel. */
function zipName(server: MockServer, time: number): string {
  const iso = new Date(time).toISOString() // 2026-10-12T04:00:00.000Z
  return `${server.name}-${iso.slice(0, 10).replaceAll('-', '')}-${iso.slice(11, 19).replaceAll(':', '')}.zip`
}

export const backupOperations: MockOperation[] = [
  op(
    'getBackups',
    http.get(`${API}/servers/:id/backups`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      return HttpResponse.json<BackupOverview>({
        available: c.b.available,
        snapshots: c.b.available ? c.b.snapshots : [],
        job: jobNow(c.server.id),
        schedule: scheduleBody(c.b),
      })
    }),
  ),
  op(
    'createBackup',
    http.post(`${API}/servers/:id/backups`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<Record<string, unknown>>(request)
      if (!body || typeof body !== 'object' || Object.keys(body).some((k) => k !== 'note')) return apiError(400, 'bad_request', 'invalid JSON body')
      const note = body.note ?? null
      if (note !== null && typeof note !== 'string') return apiError(400, 'bad_request', 'note must be a string')
      if (note !== null && note !== '' && !validNote(note)) return apiError(400, 'validation_failed', 'note: at most 64 characters, no control characters')
      const refused = canStart(c)
      if (refused) return refused
      const running = c.server.status.state === 'running'
      const steps: BackupJobStep[] = running ? ['saving', 'backing_up', 'resuming'] : ['backing_up']
      const snap = newSnapshot('manual', note || null, Math.round(1.2 * 1024 ** 3))
      const job = startJob(c, 'backup', steps, { kind: 'create', snapshot: snap }, null)
      recordAudit(c.g.user, 'backup.create', `server:${c.server.id}`, 'success', { note: note || null })
      return HttpResponse.json({ job }, { status: 202 })
    }),
  ),
  op(
    'getBackupJob',
    http.get(`${API}/servers/:id/backups/job`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      return HttpResponse.json({ job: jobNow(c.server.id) })
    }),
  ),
  op(
    'updateBackupSchedule',
    http.put(`${API}/servers/:id/backups/schedule`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<Record<string, unknown>>(request)
      if (!body || typeof body !== 'object') return apiError(400, 'bad_request', 'invalid JSON body')
      const keys = Object.keys(body)
      if (keys.some((k) => !(SCHEDULE_KEYS as readonly string[]).includes(k)) || SCHEDULE_KEYS.some((k) => !(k in body))) {
        return apiError(400, 'bad_request', 'unknown or missing field')
      }
      const req = body as unknown as UpdateBackupScheduleRequest
      const int = (v: unknown, lo: number, hi: number) => Number.isInteger(v) && (v as number) >= lo && (v as number) <= hi
      if (!(MODES as readonly string[]).includes(req.mode)) return apiError(400, 'validation_failed', 'mode must be off, interval, daily or weekly')
      if (!int(req.interval_hours, 1, 168) || !int(req.time_of_day, 0, 1439) || !int(req.weekday, 0, 6)) {
        return apiError(400, 'validation_failed', 'interval_hours 1..168, time_of_day 0..1439, weekday 0..6')
      }
      const keep = [req.keep_last, req.keep_daily, req.keep_weekly, req.keep_monthly]
      if (!keep.every((k) => int(k, 0, 1000))) return apiError(400, 'validation_failed', 'keep values must be 0..1000')
      if (req.mode !== 'off' && keep.every((k) => k === 0)) return apiError(400, 'validation_failed', 'at least one keep value must be > 0')
      const s = { ...c.b.schedule, ...req }
      s.next_run_at = nextRun(s, Date.now())
      c.b.schedule = s
      recordAudit(c.g.user, 'backup.schedule_update', `server:${c.server.id}`, 'success', { mode: req.mode })
      return HttpResponse.json({ schedule: scheduleBody(c.b) })
    }),
  ),
  op(
    'pinBackup',
    http.post(`${API}/servers/:id/backups/:snapshot/pin`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const snap = findSnapshot(c, params)
      if (snap instanceof Response) return snap
      const body = await readJson<{ pinned?: unknown }>(request)
      if (!body || typeof body.pinned !== 'boolean' || Object.keys(body).length !== 1) return apiError(400, 'bad_request', 'invalid JSON body')
      if (snap.kind !== 'scheduled') return apiError(400, 'validation_failed', 'only scheduled snapshots can be pinned')
      const job = jobNow(c.server.id)
      if (job && (job.state === 'queued' || job.state === 'running')) return apiError(409, 'backup_running', 'a backup job of this server is queued or running')
      // restic rewrites a snapshot when its tags change: it gets a new ID
      const id = randomId()
      Object.assign(snap, { id, short_id: id.slice(0, 8), pinned: body.pinned })
      recordAudit(c.g.user, body.pinned ? 'backup.pin' : 'backup.unpin', `server:${c.server.id}`, 'success', { snapshot: id })
      return HttpResponse.json({ snapshot: snap })
    }),
  ),
  op(
    'restoreBackup',
    http.post(`${API}/servers/:id/backups/:snapshot/restore`, ({ request, params }) => {
      const c = access(request, params, { sudo: true })
      if (c instanceof Response) return c
      const snap = findSnapshot(c, params)
      if (snap instanceof Response) return snap
      const refused = canStart(c)
      if (refused) return refused
      const running = c.server.status.state === 'running'
      const steps: BackupJobStep[] = running ? ['stopping', 'pre_restore', 'restoring'] : ['pre_restore', 'restoring']
      const pre = newSnapshot('pre-restore', null, snap.bytes)
      const job = startJob(c, 'restore', steps, { kind: 'restore', preRestore: pre }, snap.id)
      recordAudit(c.g.user, 'backup.restore', `server:${c.server.id}`, 'success', { snapshot: snap.id, time: snap.time })
      return HttpResponse.json({ job }, { status: 202 })
    }),
  ),
  op(
    'deleteBackup',
    http.delete(`${API}/servers/:id/backups/:snapshot`, ({ request, params }) => {
      const c = access(request, params, { sudo: true })
      if (c instanceof Response) return c
      const snap = findSnapshot(c, params)
      if (snap instanceof Response) return snap
      const refused = canStart(c)
      if (refused) return refused
      const job = startJob(c, 'delete', ['deleting'], { kind: 'delete', id: snap.id }, snap.id)
      recordAudit(c.g.user, 'backup.delete', `server:${c.server.id}`, 'success', { snapshot: snap.id })
      return HttpResponse.json({ job }, { status: 202 })
    }),
  ),
  op(
    'downloadBackup',
    http.get(`${API}/servers/:id/backups/:snapshot/download`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      if (!c.b.available) return apiError(503, 'backups_unavailable', 'restic is not available on this host')
      const snap = findSnapshot(c, params)
      if (snap instanceof Response) return snap
      recordAudit(c.g.user, 'backup.download', `server:${c.server.id}`, 'success', { snapshot: snap.id })
      return new HttpResponse(EMPTY_ZIP.slice(), {
        headers: { 'Content-Type': 'application/zip', 'Content-Disposition': `attachment; filename="${zipName(c.server, snap.time)}"` },
      })
    }),
  ),
]
```

(`getBackups` für `lobby` ohne Instanz liefert über `access` eine leere Übersicht; `createBackup` dort antwortet `409 conflict` aus `canStart`. Prüft eslint `@typescript-eslint/switch-exhaustiveness-check` o. ä. in `nextRun`, ist die Funktion bereits vollständig.)

`web/src/mocks/handlers.ts`: `import { backupOperations } from './handlers/backups'` ergänzen und `...backupOperations,` nach `...pluginsOperations,` einfügen.

`web/src/mocks/handlers/servers.ts`, im `deleteServer`-Mock aus Aufgabe 2 den `purge`-Block ersetzen:

```ts
      if (purge) {
        delete db.backups[id]
        delete db.backupJobs[id]
        details.backups_purged = true
      }
```

- [ ] **Step 9: restic für die Tests (gemeinsames Skript, VM und CI)**

Neue Datei `scripts/install-restic.sh` (ausführbar, `chmod 0755`):

```bash
#!/usr/bin/env bash
# Installs the pinned restic release to /usr/local/bin/restic (sub-project 4). Idempotent: an
# installed binary of the same version is kept. Used by scripts/vm-test.sh and the Go job of the
# CI (backup integration tests), and later by the instances job and scripts/testvm-deploy.sh.
# Needs root (run it with sudo).
#
#   sudo bash scripts/install-restic.sh
set -euo pipefail

RESTIC_VERSION="0.19.1"
# SHA-256 of restic_<version>_linux_<arch>.bz2 from the release's SHA256SUMS.
RESTIC_SHA256_AMD64="f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c"
RESTIC_SHA256_ARM64="a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465"
BIN="/usr/local/bin/restic"

die() {
  echo "install-restic: $*" >&2
  exit 1
}

if [ -x "${BIN}" ] && "${BIN}" version 2> /dev/null | grep -q "^restic ${RESTIC_VERSION} "; then
  exit 0
fi
[ "$(id -u)" -eq 0 ] || die "must run as root (sudo bash $0)"

case "$(uname -m)" in
  x86_64) arch=amd64 sum="${RESTIC_SHA256_AMD64}" ;;
  aarch64 | arm64) arch=arm64 sum="${RESTIC_SHA256_ARM64}" ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
command -v bunzip2 > /dev/null || apt-get install -y bzip2

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
name="restic_${RESTIC_VERSION}_linux_${arch}.bz2"
curl -fsSL -o "${tmp}/${name}" "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/${name}"
echo "${sum}  ${tmp}/${name}" | sha256sum -c -
bunzip2 "${tmp}/${name}"
install -m 0755 "${tmp}/${name%.bz2}" "${BIN}"
"${BIN}" version
```

`scripts/vm-test.sh`, nach `need_golangci` einfügen:

```bash
# restic for the backup integration tests of the files service (sub-project 4); they skip
# without MCPANEL_TEST_RESTIC. The shared script keeps an installed binary of the pinned version.
need_restic() {
  if ! /usr/local/bin/restic version 2> /dev/null | grep -q "^restic 0\.19\.1 "; then
    log "installing restic (scripts/install-restic.sh)"
    sudo bash scripts/install-restic.sh
  fi
  export MCPANEL_TEST_RESTIC=/usr/local/bin/restic
}
```

und `suite_go` ersetzen:

```bash
suite_go() {
  need_go
  need_restic
  log "go test -race ./... (MCPANEL_TEST_RESTIC=${MCPANEL_TEST_RESTIC})"
  CGO_ENABLED=1 go test -race -count=1 ./...
}
```

(Die Versionsprüfung in `need_restic` spart nur den `sudo`-Aufruf; maßgeblich ist die Version im Skript. Wer die Version ändert, ändert beide Stellen.)

`.github/workflows/ci.yml`, Job `go`: vor „Tests (race detector)“ einfügen und den Testschritt um `env` ergänzen:

```yaml
      # restic for the backup integration tests of the files service (sub-project 4); without
      # MCPANEL_TEST_RESTIC they skip. Pinned release binary with SHA-256 check.
      - name: Install restic
        run: sudo bash scripts/install-restic.sh

      # The race run executes every test, so a separate run without -race would only repeat it
      # (the release workflow still runs both).
      - name: Tests (race detector)
        env:
          MCPANEL_TEST_RESTIC: /usr/local/bin/restic
        run: make test-race
```

Im Job `changes` den Go-Filter um das Skript erweitern, damit eine Änderung daran den Go-Job auslöst (Phase C erweitert `instances` entsprechend):

```yaml
          out go "^(cmd|internal|api)/|${gomod}|^sqlc\.yaml$|^\.golangci\.yml$|^Makefile$|^scripts/install-restic\.sh$"
```

- [ ] **Step 10: `make check`, Tests auf der VM**

Run: `make check`
Expected: PASS.

Run: `make test-vm SUITE=go`, `make test-vm SUITE=web`, `make test-vm SUITE=lint`
Expected: PASS. Im Go-Lauf erscheint einmalig „installing restic (scripts/install-restic.sh)“, danach `MCPANEL_TEST_RESTIC=/usr/local/bin/restic`. Grün sind insbesondere `TestRouteContract`, `TestStubsAnswerNotImplemented`, `TestStubsCheckPermissionAndSudo`, `TestStubGateAndPurge` (`internal/backups`), `TestRoutesMatchOpenAPI`, `TestEveryRouteDeclaresARule` (`cmd/panel`) und die vier neuen Vitest-Fälle in `handlers.test.ts` (95 Operationen).

- [ ] **Step 11: Commit, Push, CI, PR bereit**

```bash
git add api/openapi.yaml web/src/api internal/backups cmd/panel web/src/mocks scripts/install-restic.sh scripts/vm-test.sh .github/workflows/ci.yml
git commit -m "feat(backups): panel contract with stub routes, mocks and restic for the tests

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
make test-vm
gh pr checks --watch --interval 30
gh pr ready
```

Expected: `make test-vm` (alle Suites) und `CI OK` grün (Go-Job zeigt „Install restic“ mit `restic 0.19.1`). Danach dem Lead den PR melden.
