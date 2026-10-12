# Teilprojekt 4 – panel (Aufgaben 9–13) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das panel bekommt das Paket `internal/backups`: Zeitpläne mit Retention, eine panel-weite Warteschlange, Backup-Jobs mit `save-off`/`save-all flush`/`save-on` bei laufendem Server, Restore mit Sperre, Stopp und Sicherheits-Snapshot, Löschen, Anpinnen, Purge beim Löschen eines Servers, die Übersicht, Zip-Download und `backup.failed`-Benachrichtigungen.

**Architecture:** `backups.Handler` besitzt die acht Routen aus V3. Alle Jobs (Backup, Restore, Delete) laufen über eine `queue` im Speicher: einer pro Server (`409 backup_running`), einer gleichzeitig im ganzen panel (FIFO, `queue_position`), ausgeführt von einem Worker, den `Run` startet. Jeder Job ruft den Dienst `mcpanel-files` (`filesapi.BackupClient`) auf und fragt dessen Job alle 2 s ab. RCON und Ist-Zustand kommen vom agent. Ein Ticker (jede Minute) startet fällige Zeitpläne, erkennt einen Wechsel der Zeitzone und wiederholt ausstehende `save-on`. `saveoff_since` in der DB macht `save-on` absturzsicher. Die Zeitplan-Rechnung ist eine reine Funktion (`NextRun`) und wird mit festen Zeitpunkten getestet.

**Tech Stack:** Go (Standardbibliothek, chi, sqlc/`store`), `httptest`, Fakes für Dienst, agent und Konsole. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (§2, §5). Verträge V1–V6, Global Constraints, Review Focus und Dateieigentum B2: `docs/superpowers/plans/2026-10-12-backups-00-overview.md`.

## Ausführung dieser Phase

- **Branch/Worktree:** `git fetch origin && git worktree add ../mc-panel-wt/feat-backups-panel -b feat/backups-panel origin/main` (erst nachdem Phase A gemerged ist). Alle Befehle laufen im Worktree.
- **Draft-PR** gleich nach dem ersten Commit: `gh pr create --draft --base main --head feat/backups-panel --title "feat(backups): panel side of backups (sub-project 4)" --body "<Inhalt; endet mit der Attributionszeile aus der Systemvorgabe>"`. Nach jedem Push: `gh pr checks <n> --watch`, Fehler aus `gh run view <id> --log-failed` beheben.
- **Lokal nur `make check`** (CLAUDE.md). Neue Dateien vor `make check` mit `git add -N` vormerken. „Rot“ heißt: `make check` scheitert am Kompilieren der Tests oder der VM-Lauf ist rot. „Grün“ heißt: `make test-vm SUITE=go` ist grün; einzelne Tests nach dem ersten `make test-vm`:
  ```bash
  ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-panel && PATH=/usr/local/go/bin:$PATH go test -race ./internal/backups -run TestName -count=1'
  ```
  (SSH braucht eine Bash ohne Sandbox.) Vor „ready for review“ `make test-vm` (alle Suites).
- **Formatierung:** Die Codeblöcke sind inhaltlich verbindlich; Ausrichtung und Importgruppen erledigt `gofmt -w` bzw. `golangci-lint fmt ./internal/backups/... ./cmd/panel/...` (auf der VM über `make test-vm SUITE=lint`) vor dem Commit.
- **Dateieigentum:** nur `internal/backups/**` und `cmd/panel/**`. Fehlt etwas aus Phase A (`filesapi`, `store`, `httpx`, `audit`, `notify`, `servers`, `api/openapi.yaml`), den Lead fragen; nicht selbst ändern.
- **Commits:** Conventional Commits, eine Aufgabe = mindestens ein Commit, Nachricht endet mit
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R
  ```

## Was Phase A liefert (Annahmen, in Step 0 von Aufgabe 9 geprüft)

- `internal/files/filesapi` (V1): `BackupClient`, `Snapshot`, `SnapshotKind` (`SnapshotScheduled`, `SnapshotManual`, `SnapshotPreRestore`), `BackupsStatus`, `CreateBackupRequest`, `ForgetRequest` (+ `Validate`), `ForgetPolicy` (+ `Validate`), `ValidSnapshotID`, `ValidNote`, `JobKind` (`JobExtract`, `JobBackup`, `JobRestore`, `JobForget`), `Job.Kind`/`Job.Snapshot`/`Job.Skipped`/`Job.Code`, `CodeTooLarge`/`CodeInsufficientStorage`/`CodeBusy`/`CodeUnavailable`, `ErrBackupsUnavailable`, dazu die vorhandenen `ErrUnavailable`, `ErrBusy`, `ErrNotFound`, `ErrInvalid`, `ErrInsufficientStorage`, `ErrTooLarge`, `JobRunning`/`JobDone`/`JobFailed`, `Download`.
- `store` (V2): `BackupSchedule` mit `ServerID int64, Mode string, IntervalHours, TimeOfDay, Weekday, KeepLast, KeepDaily, KeepWeekly, KeepMonthly int64, NextRunAt, LastRunAt sql.NullInt64, LastStatus, LastError, LastSnapshotID sql.NullString, SaveoffSince sql.NullInt64, UpdatedAt int64`; Queries `BackupsEnsureSchedule(ctx, BackupsEnsureScheduleParams{ServerID, UpdatedAt})`, `BackupsGetSchedule(ctx, serverID)`, `BackupsUpdateSchedule(ctx, BackupsUpdateScheduleParams{Mode, IntervalHours, TimeOfDay, Weekday, KeepLast, KeepDaily, KeepWeekly, KeepMonthly, NextRunAt, UpdatedAt, ServerID})`, `BackupsListDue(ctx, nextRunAt sql.NullInt64)`, `BackupsListEnabled(ctx)`, `BackupsSetNextRun(ctx, BackupsSetNextRunParams{NextRunAt, ServerID})`, `BackupsStartRun(ctx, BackupsStartRunParams{LastRunAt, ServerID})`, `BackupsFinishRun(ctx, BackupsFinishRunParams{LastStatus, LastError, LastSnapshotID, NextRunAt, ServerID})`, `BackupsFailRunning(ctx, lastError sql.NullString)`, `BackupsSetSaveOff(ctx, BackupsSetSaveOffParams{SaveoffSince, ServerID})`, `BackupsListSaveOff(ctx) ([]BackupsListSaveOffRow{ServerID, SaveoffSince}, error)`, `BackupsGetTimezone(ctx) (string, error)`.
- `httpx.CodeBackupsUnavailable`, `httpx.CodeBackupRunning`; `audit.ActionBackupCreate`, `ActionBackupRestore`, `ActionBackupDelete`, `ActionBackupPin`, `ActionBackupUnpin`, `ActionBackupDownload`, `ActionBackupScheduleUpdate`; `notify.EventBackupFailed`.
- `internal/backups/handler.go` (Stub nach V4: `Deps`, `Handler`, `NewHandler`, `Routes` mit acht Routen auf einen Stub → `503 backups_unavailable` „backups not implemented yet“, `Run`, `DataJobRunning`, `PurgeBackups`) und `internal/backups/routes_test.go` (Routenvertrag, Stub-Antworten, Rechte, `Run` endet). Diese Phase ersetzt `handler.go` vollständig.
- `cmd/panel/app.go` (V4): `appDeps.Backups filesapi.BackupClient`, `backupsH := backups.NewHandler(...)`, `servers.Deps{Gate: servers.AnyGate(backupsH, filesH), Backups: backupsH}`, `Reconcile` per Closure auf `serversH.Trigger`, `backupsH.Routes(api)`, `backupsH.Run(ctx)` in `run`; `cmd/panel/main.go` importiert `_ "time/tzdata"` und setzt `Backups` auf denselben `*filesapi.UnixClient` wie `Files`.

**Weichen Namen aus Phase A ab, gelten die Namen aus Phase A; Werte (Codes, Aktionen, Pfade, JSON-Felder) aus V1–V4 sind verbindlich.**

## Festlegungen dieses Teilplans (interne Typen und Signaturen)

Einmal zentral festgelegt und in allen Aufgaben genau so verwendet.

```go
package backups // github.com/Bernotese/mc-panel/internal/backups

// handler.go
type Deps struct { DB; Clock; Audit; Notifier; Files; Instances; Console; Reconcile; Log } // V4, exakt
func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
func (h *Handler) Run(ctx context.Context)
func (h *Handler) DataJobRunning(ctx context.Context, serverID int64) bool
func (h *Handler) PurgeBackups(ctx context.Context, serverID int64) error
type server struct { ID int64; Name string; HasInstance bool }
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (server, bool)
func snapshotParam(w http.ResponseWriter, r *http.Request) (string, bool)
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any)
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error)
func classify(err error) (status int, code, msg string, ok bool)
func rejected(err error) bool
var errBackupRunning, errShuttingDown, errNoInstance, errSnapshotNotFound, errNotAvailable, errTooManyDownloads, errNoFiles error

// schedule.go
type Mode string // ModeOff, ModeInterval, ModeDaily, ModeWeekly
type Schedule struct { Mode Mode; IntervalHours, TimeOfDay int; Weekday time.Weekday; KeepLast, KeepDaily, KeepWeekly, KeepMonthly int }
var DefaultSchedule Schedule
func (s Schedule) Validate() error
func (s Schedule) Policy() filesapi.ForgetPolicy
func NextRun(s Schedule, loc *time.Location, after, lastStart time.Time) (time.Time, bool)

// store.go
func (h *Handler) loadSchedule(ctx context.Context, serverID int64) (store.BackupSchedule, error)
func scheduleOf(r store.BackupSchedule) Schedule
func (h *Handler) location(ctx context.Context) (loc *time.Location, configured string, err error)
type scheduleView struct { … }   // OpenAPI BackupSchedule
func viewSchedule(r store.BackupSchedule, tz string) scheduleView
func nullTime(t time.Time) sql.NullInt64
func timeOf(n sql.NullInt64) time.Time
func nullString(s string) sql.NullString

// jobs.go / queue.go
const kindBackup, kindRestore, kindDelete, triggerManual, triggerScheduled,
	stateQueued, stateRunning, stateDone, stateFailed, step… (V3 BackupJobStep), code… (V3 BackupJobError)
type jobView struct { … }        // OpenAPI BackupJob
type jobError struct{ code, msg string }
func failf(code, format string, args ...any) error
func jobErrorOf(err error) (code, msg string)
type runFunc func(ctx context.Context, j *job) (snapshot string, err error)
type jobSpec struct { serverID int64; name, kind, trigger, snapshot string; run runFunc; after func() }
type job struct { … }
func (j *job) setStep(step string)
func (j *job) setBytes(n int64)
type queue struct { … }
func newQueue(clk clock.Clock) *queue
func (q *queue) add(base context.Context, s jobSpec) (jobView, error) // errBackupRunning | errShuttingDown
func (q *queue) get(serverID int64) *jobView
func (q *queue) busy(serverID int64) bool          // queued or running
func (q *queue) runningFor(serverID int64) bool
func (q *queue) restoreHeld(serverID int64) bool     // restore queued or running
func (q *queue) work(ctx context.Context)
func (q *queue) drop(serverID int64, wait time.Duration)
func (q *queue) close()
func (q *queue) idle() bool

// backup.go
func (h *Handler) ready(ctx context.Context, srv server, needInstance bool) error
func (h *Handler) instanceState(ctx context.Context, id int64) (agentapi.ContainerState, error)
func (h *Handler) containerRunning(ctx context.Context, id int64) (bool, error)
func (h *Handler) rcon(ctx context.Context, id int64, cmd string) error
func (h *Handler) markSaveOff(ctx context.Context, id int64) error
func (h *Handler) resume(id int64) bool
func (h *Handler) waitService(ctx context.Context, j *job, id int64, jobID string) (filesapi.Job, error)
func serviceJobCode(code string) string // filesapi.Job.Code -> BackupJobError (V5)
func (h *Handler) cancelService(id int64)
func (h *Handler) runBackup(ctx context.Context, j *job, kind filesapi.SnapshotKind, note string) (string, error)

// recover.go, ticker.go, retention.go, notify.go
func (h *Handler) recover(ctx context.Context)
func (h *Handler) tick(ctx context.Context)
func (h *Handler) retrySaveOn(ctx context.Context)
func (h *Handler) checkTimezone(ctx context.Context, now time.Time)
func (h *Handler) enqueueDue(ctx context.Context, now time.Time)
func (h *Handler) runScheduled(ctx context.Context, j *job) (string, error)
func (h *Handler) prune(ctx context.Context, j *job) error
type alerts struct { … }
func (h *Handler) alert(ctx context.Context, serverID int64, name, what, msg string)
func (h *Handler) alertOK(serverID int64, what string)

// restore.go, manage.go, overview.go, download.go
func (h *Handler) runRestore(ctx context.Context, j *job, snapshot string) error
func (h *Handler) stop(ctx context.Context, id int64) error
func (h *Handler) findSnapshot(ctx context.Context, serverID int64, id string) (filesapi.Snapshot, error)
type snapshotView struct { … }   // OpenAPI BackupSnapshot
func viewSnapshot(s filesapi.Snapshot) snapshotView
type userSlots struct { … }
func newUserSlots(maxPerUser int) *userSlots
```

## Abweichungen und Präzisierungen (begründet)

1. **`save-on` wird wiederholt, bis es klappt:** Spec §5.4 Schritt 3 nennt ein fehlgeschlagenes `save-on` „unschädlich“, weil das meist einen inzwischen gestoppten Server betrifft. Antwortet RCON aber nur kurz nicht (Server läuft weiter), bliebe er ohne Speichern. Deshalb bleibt `saveoff_since` gesetzt, solange `save-on` nicht bestätigt ist (oder der agent `not_running`/`not_found` meldet), und jeder Tick versucht es erneut für Server ohne laufenden Job (Review Focus 1).
2. **Dienst belegt (`429 busy`) beim Start eines Backups** (z. B. Entpacken im Dateien-Tab): Der Job endet mit `files_busy` (V3/V5) und dem Text „the file service is busy with another job for this server …“. Scheitert ein Dienst-Job selbst, bildet `waitService` dessen Wire-Code `filesapi.Job.Code` ab (`too_large`, `insufficient_storage`, `files_busy`, `backups_unavailable`, sonst `internal`; `TestServiceJobFailureCodes`).
3. **Snapshot-Prüfung vor dem Einreihen:** Restore, Delete und Download prüfen die ID gegen die Snapshot-Liste des Dienstes, damit eine unbekannte ID synchron `404 not_found` bekommt statt eines fehlgeschlagenen Jobs. Pin verlässt sich auf den Dienst (`404` bzw. `400` für nicht geplante Snapshots).
4. **Audit-Zeitpunkt:** `backup.create`, `backup.restore` und `backup.delete` werden am Job-Ende mit Ergebnis geschrieben (Akteur und IP aus dem Kontext der auslösenden Anfrage, wie TP3c). Abgelehnte Anfragen (`400`, `404`, `409`, `429`, `503` vor dem Einreihen) werden nicht auditiert.
5. **Zeitzonenwechsel bei laufendem panel:** Der Ticker merkt sich die Zeitzone des letzten Ticks und rechnet bei einer Änderung `next_run_at` aller aktiven Zeitpläne neu. Ein Wechsel, während das panel aus war, wirkt erst nach dem nächsten Lauf jedes Zeitplans; ein Neurechnen beim ersten Tick würde verpasste Termine in die Zukunft schieben (Spec §5.2 verlangt, dass sie einmal nachgeholt werden).
6. **Intervall-Anker:** `interval` rechnet ab dem Start des letzten **geplanten** Laufs (`last_run_at`); manuelle Backups verschieben den Zeitplan nicht. Liegt dieser Zeitpunkt + N nicht in der Zukunft, gilt jetzt + N.
7. **Docker nicht verfügbar / agent weg** beim Backup: Der Ist-Zustand ist unbekannt, ein Snapshot könnte inkonsistent sein; der Job endet mit `agent_unavailable` (kein RCON, kein Dienst-Aufruf). Zustand `restarting` zählt als laufend.
8. **Wartezeiten mit echter Zeit:** Abfrage-Intervall (2 s), RCON-Frist (60 s), Stopp-Frist (120 s) und die 20 s beim Beenden laufen über `time`/`context.WithTimeout`; `clock.Clock` steuert nur Logik (Zeitpläne, Zeitstempel, Benachrichtigungs-Drossel). Die Tests verkürzen die Wartezeiten über `export_test.go`.
9. **Purge bei noch beschäftigtem Dienst:** Nach dem Abbrechen eines laufenden Jobs kann der Dienst kurz `429 busy` melden; `PurgeBackups` versucht es bis zu fünfmal im Abfrage-Intervall.
10. **Benachrichtigung:** `Username` ist beim Restore der auslösende Benutzer, bei geplanten Backups leer. Abgebrochene Jobs (Herunterfahren, Server gelöscht) und Backups eines Servers ohne Datenverzeichnis (`ErrNotFound` des Dienstes) melden nichts.
11. **Dienst-Job verloren:** Liefert `/v1/jobs` einen anderen Job als den gestarteten oder keinen (Dienst neu gestartet), endet der panel-Job mit `files_unavailable`. Fünf aufeinanderfolgende Abfragefehler ebenso (vorher `CancelJob`).

## Review Focus (dieser Teilplan)

- **#1 RCON scheitert / panel stirbt im Backup:** `TestSaveOffFailsStillSendsSaveOn`, `TestFlushFailsStillSendsSaveOn`, `TestSaveOnAfterServiceJobFails`, `TestSaveOnAfterCreateRefused`, `TestSaveOnRetriedByTicker`, `TestShutdownCancelsServiceJobAndResumes`, `TestRecoverAfterCrash`, `TestAgentDownFailsBackup` (Aufgabe 10).
- **#4 Gleichzeitigkeit:** `TestCreateTwiceConcurrently`, `TestQueueRunsOneJobAtATime` (Aufgabe 10); `TestTickSkipsBusyServer`, `TestTimezoneChangeRecomputes` (Aufgabe 11); `TestRestoreWhileBackupQueued`, `TestGateHoldsWhileRestoreQueued`, `TestGateHoldsWhileStopping`, `TestPinWhileServiceBusy`, `TestGateHoldsDuringRestore`, `TestPinWhileJobRunning`, `TestPinTwiceOldIDGone`, `TestPurgeDropsQueuedJob`, `TestPurgeCancelsRunningJob` (Aufgabe 12); `TestBackupsEndToEnd` (Start während Restore → `409`, Aufgabe 13).
- **#5 Betriebsfehler (panel-Seite):** `TestCreateUnavailable` (restic fehlt, Dienst fehlt), `TestRestoreInsufficientStorage`, `TestServiceLosesJob`, `TestServiceUnreachableWhilePolling`, `TestServiceJobFailureCodes`, `TestSlowRoutesOutliveWriteTimeout`, `TestBackupAndRetentionShareZone` (Aufgaben 10–12).

## Dateistruktur

```
internal/backups/handler.go          (9; 10–13 ergänzen)  Deps, Handler, Routes, Run, loadServer, Fehler, noConsole
internal/backups/stubs.go            (9; 10, 12 kürzen, 13 löscht)  Übergangs-Handler
internal/backups/schedule.go         (9)  Mode, Schedule, Validate, Policy, NextRun
internal/backups/store.go            (9)  DB-Helfer, scheduleView, location
internal/backups/schedule_api.go     (9)  PUT schedule
internal/backups/jobs.go             (10) Zustände, jobView, jobError, jobErrorOf
internal/backups/queue.go            (10) queue (FIFO, einer pro Server, einer gesamt)
internal/backups/backup.go           (10) ready, Ist-Zustand, RCON, waitService, runBackup, POST/GET job
internal/backups/recover.go          (10) Wiederanlauf
internal/backups/ticker.go           (10; 11 ergänzt)  tick, retrySaveOn, checkTimezone, enqueueDue
internal/backups/retention.go        (11) runScheduled, prune, finishScheduled
internal/backups/notify.go           (11) alerts, alert, alertOK
internal/backups/restore.go          (12) runRestore, stop, POST restore
internal/backups/manage.go           (12) DELETE snapshot, POST pin, PurgeBackups
internal/backups/overview.go         (12) GET backups, snapshotView, findSnapshot
internal/backups/download.go         (13) GET download, userSlots, deadlineWriter, idleReader
internal/backups/export_test.go      (9–13) Test-Hooks
internal/backups/helpers_test.go     (9; 10 ergänzt)  env, Anfragen, Rechte, Instanzen, ctxRecorder
internal/backups/fakes_test.go       (9; 10, 12, 13 ergänzen)  fakeFiles, fakeAgent, fakeConsole
internal/backups/schedule_test.go    (9)  NextRun, Validate
internal/backups/schedule_api_test.go (9) PUT schedule
internal/backups/backup_test.go      (10) Jobs, Warteschlange, Backup-Ablauf, Wiederanlauf
internal/backups/ticker_test.go      (11) Ticker, Retention, Zeitzone, Benachrichtigung
internal/backups/restore_test.go     (12) Restore, Sperre
internal/backups/manage_test.go      (12) Übersicht, Löschen, Pin, Purge
internal/backups/download_test.go    (13) Download
internal/backups/routes_test.go      (Phase A; 9, 13 ändern)  Routenvertrag, Stub-Liste
cmd/panel/fakebackups_test.go        (13) Fake-BackupClient für die Ende-zu-Ende-Tests
cmd/panel/backups_test.go            (13) Ende-zu-Ende über die echte Middleware
cmd/panel/integration_test.go        (13) env bekommt den Fake
cmd/panel/app.go                     (13) Verdrahtung prüfen/ergänzen
```

---

### Task 9: Grundgerüst, Zeitplan-Rechnung und Zeitplan-Route

**Files:**
- Create: `internal/backups/schedule.go`, `internal/backups/store.go`, `internal/backups/schedule_api.go`, `internal/backups/stubs.go`, `internal/backups/export_test.go`, `internal/backups/helpers_test.go`, `internal/backups/fakes_test.go`, `internal/backups/schedule_test.go`, `internal/backups/schedule_api_test.go`
- Modify (ersetzen): `internal/backups/handler.go`
- Modify: `internal/backups/routes_test.go` (Phase A; vollständig auf die neuen Helfer umgestellt, Step 14)

**Interfaces:**
- Consumes: V1 (`filesapi.BackupClient`, `ForgetPolicy`), V2 (`store.BackupSchedule`, `Backups…`-Queries, `httpx`-Codes, `audit.ActionBackupScheduleUpdate`), V4 (`Deps`).
- Produces: `Handler`, `server`, `loadServer`, `snapshotParam`, `record`, `classify`, `writeError`, `rejected`, Fehler-Sentinels; `Schedule`, `Mode*`, `DefaultSchedule`, `NextRun`; `loadSchedule`, `scheduleOf`, `location`, `scheduleView`, `viewSchedule`, `nullTime`, `timeOf`, `nullString`; Test-Umgebung `env` (`newEnv`, `newEnvNoRun`, `run`, `do`, `admin`, `sudoAdmin`, `userP`, `grant`, `instance`), Fakes `fakeFiles`, `fakeAgent`, `fakeConsole`.

- [ ] **Step 0: Phase A prüfen**

```bash
git log --oneline -5 origin/main
grep -n "BackupClient interface" -A 12 internal/files/filesapi/backups.go
grep -n "func (q \*Queries) Backups" internal/store/backups.sql.go
grep -n "type BackupSchedule struct" -A 18 internal/store/models.go
grep -n "CodeBackupsUnavailable\|CodeBackupRunning" internal/httpx/errors.go
grep -n "ActionBackup" internal/audit/types.go
grep -n "EventBackupFailed" internal/notify/notify.go
grep -n "func Test\|^func \|^var " internal/backups/routes_test.go
grep -n "backups\." cmd/panel/app.go
```

Expected: alle Namen aus „Was Phase A liefert“ vorhanden. Abweichende Namen notieren und im ganzen Plan ersetzen. Phase A definiert in `routes_test.go` eigene Helfer (`t0`, `env` mit `db`/`mux`/`rt`/`h`, `newEnv`, `do`, `expand`, `snap`, `backupsRule`, `wantRoutes`) und die Tests `TestRouteContract`, `TestStubsAnswerNotImplemented`, `TestStubsCheckPermissionAndSudo`, `TestStubGateAndPurge`. Sie kollidieren mit `helpers_test.go` aus Step 5 (`t0`, `env`, `newEnv`, `do`; außerdem legt `newEnvNoRun` den Benutzer `root` schon an, den die Phase-A-Tests ein zweites Mal anlegen würden). Step 14 ersetzt `routes_test.go` deshalb **vollständig**. Damit das Test-Paket zwischen Step 5 und Step 14 kompiliert, in Step 5 zuerst `git rm internal/backups/routes_test.go` (Step 14 legt die neue Fassung an).

- [ ] **Step 1: Zeitplan-Tests schreiben** – `internal/backups/schedule_test.go`

```go
package backups_test

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // fixed zone data: the expected values below must not depend on the host

	"github.com/Bernotese/mc-panel/internal/backups"
)

func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func utc(y int, m time.Month, d, hh, mm int) time.Time { return time.Date(y, m, d, hh, mm, 0, 0, time.UTC) }

func daily(minute int) backups.Schedule {
	s := backups.DefaultSchedule
	s.Mode, s.TimeOfDay = backups.ModeDaily, minute
	return s
}

func TestNextRunDaily(t *testing.T) {
	loc := berlin(t)
	cases := []struct {
		name  string
		after time.Time
		want  time.Time
	}{
		{"later today", utc(2026, 10, 12, 1, 59), utc(2026, 10, 12, 2, 0)},  // 03:59 CEST -> 04:00 CEST
		{"exactly at the slot", utc(2026, 10, 12, 2, 0), utc(2026, 10, 13, 2, 0)},
		{"passed today", utc(2026, 10, 12, 10, 0), utc(2026, 10, 13, 2, 0)},
		{"after the clock change", utc(2026, 10, 25, 0, 0), utc(2026, 10, 25, 3, 0)}, // 04:00 CET = 03:00Z
	}
	for _, c := range cases {
		got, ok := backups.NextRun(daily(240), loc, c.after, time.Time{})
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: NextRun(%v) = %v %v, want %v", c.name, c.after, got, ok, c.want)
		}
	}
}

// 2026-03-29 02:00 CET jumps to 03:00 CEST: a 02:30 slot moves to 03:30 CEST (time.Date).
func TestNextRunDailyDSTGap(t *testing.T) {
	got, _ := backups.NextRun(daily(150), berlin(t), utc(2026, 3, 28, 23, 0), time.Time{})
	if want := utc(2026, 3, 29, 1, 30); !got.Equal(want) {
		t.Fatalf("gap: %v, want %v (03:30 CEST)", got, want)
	}
}

// 2026-10-25 03:00 CEST falls back to 02:00 CET: 02:30 exists twice and runs once, at the
// earlier instant; the next run is on the following day.
func TestNextRunDailyDSTRepeat(t *testing.T) {
	loc := berlin(t)
	got, _ := backups.NextRun(daily(150), loc, utc(2026, 10, 24, 22, 0), time.Time{})
	if want := utc(2026, 10, 25, 0, 30); !got.Equal(want) {
		t.Fatalf("first 02:30: %v, want %v (02:30 CEST)", got, want)
	}
	got, _ = backups.NextRun(daily(150), loc, utc(2026, 10, 25, 0, 35), time.Time{})
	if want := utc(2026, 10, 26, 1, 30); !got.Equal(want) {
		t.Fatalf("after the run: %v, want %v (next day, not the repeated 02:30 CET)", got, want)
	}
}

func TestNextRunWeekly(t *testing.T) {
	loc := berlin(t)
	s := backups.DefaultSchedule
	s.Mode, s.Weekday, s.TimeOfDay = backups.ModeWeekly, time.Sunday, 240
	cases := []struct{ after, want time.Time }{
		{utc(2026, 10, 12, 10, 0), utc(2026, 10, 18, 2, 0)}, // Monday -> Sunday 04:00 CEST
		{utc(2026, 10, 18, 1, 0), utc(2026, 10, 18, 2, 0)},  // Sunday 03:00 CEST -> same day
		{utc(2026, 10, 18, 2, 0), utc(2026, 10, 25, 3, 0)},  // next Sunday, now 04:00 CET
	}
	for _, c := range cases {
		if got, ok := backups.NextRun(s, loc, c.after, time.Time{}); !ok || !got.Equal(c.want) {
			t.Errorf("NextRun(%v) = %v %v, want %v", c.after, got, ok, c.want)
		}
	}
}

func TestNextRunInterval(t *testing.T) {
	s := backups.DefaultSchedule
	s.Mode, s.IntervalHours = backups.ModeInterval, 6
	now := utc(2026, 10, 12, 10, 0)
	cases := []struct {
		name      string
		lastStart time.Time
		want      time.Time
	}{
		{"no run yet", time.Time{}, now.Add(6 * time.Hour)},
		{"last run 2h ago", now.Add(-2 * time.Hour), now.Add(4 * time.Hour)},
		{"last run exactly 6h ago", now.Add(-6 * time.Hour), now.Add(6 * time.Hour)},
		{"missed", now.Add(-30 * time.Hour), now.Add(6 * time.Hour)},
	}
	for _, c := range cases {
		if got, ok := backups.NextRun(s, time.UTC, now, c.lastStart); !ok || !got.Equal(c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, ok, c.want)
		}
	}
}

func TestNextRunOff(t *testing.T) {
	if _, ok := backups.NextRun(backups.DefaultSchedule, time.UTC, utc(2026, 10, 12, 10, 0), time.Time{}); ok {
		t.Fatal("mode off must have no next run")
	}
}

func TestScheduleValidate(t *testing.T) {
	ok := daily(240)
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := backups.DefaultSchedule.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	off := backups.DefaultSchedule
	off.KeepLast, off.KeepDaily, off.KeepWeekly, off.KeepMonthly = 0, 0, 0, 0
	if err := off.Validate(); err != nil {
		t.Fatalf("off without retention is valid: %v", err)
	}
	bad := []struct {
		name string
		mut  func(*backups.Schedule)
		want string
	}{
		{"mode", func(s *backups.Schedule) { s.Mode = "hourly" }, "mode"},
		{"interval low", func(s *backups.Schedule) { s.IntervalHours = 0 }, "interval_hours"},
		{"interval high", func(s *backups.Schedule) { s.IntervalHours = 169 }, "interval_hours"},
		{"time low", func(s *backups.Schedule) { s.TimeOfDay = -1 }, "time_of_day"},
		{"time high", func(s *backups.Schedule) { s.TimeOfDay = 1440 }, "time_of_day"},
		{"weekday", func(s *backups.Schedule) { s.Weekday = 7 }, "weekday"},
		{"keep high", func(s *backups.Schedule) { s.KeepDaily = 1001 }, "keep_daily"},
		{"keep negative", func(s *backups.Schedule) { s.KeepLast = -1 }, "keep_last"},
		{"no retention", func(s *backups.Schedule) { s.KeepLast, s.KeepDaily, s.KeepWeekly, s.KeepMonthly = 0, 0, 0, 0 }, "keep_"},
	}
	for _, b := range bad {
		s := daily(240)
		b.mut(&s)
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: err = %v, want mention of %q", b.name, err, b.want)
		}
	}
}
```

- [ ] **Step 2: Rot prüfen**

Run: `git add -N internal/backups && make check`
Expected: FAIL – `undefined: backups.Schedule`, `backups.NextRun`, `backups.DefaultSchedule`, `backups.ModeDaily` …

- [ ] **Step 3: `internal/backups/schedule.go` schreiben**

```go
package backups

import (
	"errors"
	"fmt"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Mode is how a server's backups are scheduled (spec §5.1).
type Mode string

const (
	ModeOff      Mode = "off"
	ModeInterval Mode = "interval"
	ModeDaily    Mode = "daily"
	ModeWeekly   Mode = "weekly"
)

const (
	maxIntervalHours = 168
	maxKeep          = 1000
	minutesPerDay    = 24 * 60
)

// Schedule is the editable part of a backup_schedules row.
type Schedule struct {
	Mode          Mode
	IntervalHours int
	TimeOfDay     int          // minutes after midnight in the panel time zone
	Weekday       time.Weekday // weekly only; 0 = Sunday
	KeepLast      int
	KeepDaily     int
	KeepWeekly    int
	KeepMonthly   int
}

// DefaultSchedule are the column defaults of migration 00006.
var DefaultSchedule = Schedule{
	Mode: ModeOff, IntervalHours: 24, TimeOfDay: 240, Weekday: time.Sunday,
	KeepLast: 24, KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 6,
}

var errInvalidSchedule = errors.New("invalid schedule")

// Validate checks the ranges of spec §5.1; every value is checked in every mode, so a later
// switch of the mode never meets a stored value out of range.
func (s Schedule) Validate() error {
	switch s.Mode {
	case ModeOff, ModeInterval, ModeDaily, ModeWeekly:
	default:
		return fmt.Errorf("%w: mode must be off, interval, daily or weekly", errInvalidSchedule)
	}
	if s.IntervalHours < 1 || s.IntervalHours > maxIntervalHours {
		return fmt.Errorf("%w: interval_hours must be 1..%d", errInvalidSchedule, maxIntervalHours)
	}
	if s.TimeOfDay < 0 || s.TimeOfDay >= minutesPerDay {
		return fmt.Errorf("%w: time_of_day must be 0..%d", errInvalidSchedule, minutesPerDay-1)
	}
	if s.Weekday < time.Sunday || s.Weekday > time.Saturday {
		return fmt.Errorf("%w: weekday must be 0..6", errInvalidSchedule)
	}
	keeps := []struct {
		name string
		v    int
	}{{"keep_last", s.KeepLast}, {"keep_daily", s.KeepDaily}, {"keep_weekly", s.KeepWeekly}, {"keep_monthly", s.KeepMonthly}}
	total := 0
	for _, k := range keeps {
		if k.v < 0 || k.v > maxKeep {
			return fmt.Errorf("%w: %s must be 0..%d", errInvalidSchedule, k.name, maxKeep)
		}
		total += k.v
	}
	if s.Mode != ModeOff && total == 0 {
		return fmt.Errorf("%w: at least one keep_* value must be > 0", errInvalidSchedule)
	}
	return nil
}

// Policy is the retention of the schedule as the files service expects it.
func (s Schedule) Policy() filesapi.ForgetPolicy {
	return filesapi.ForgetPolicy{KeepLast: s.KeepLast, KeepDaily: s.KeepDaily, KeepWeekly: s.KeepWeekly, KeepMonthly: s.KeepMonthly}
}

// NextRun returns the first scheduled time strictly after `after` (spec §5.2); ok is false for
// ModeOff. lastStart is the start of the last scheduled run (zero = none); only ModeInterval
// uses it. Daily and weekly slots are wall-clock times in loc: a slot in a DST gap moves
// forward like time.Date (02:30 -> 03:30), a repeated hour counts once (the earlier instant).
func NextRun(s Schedule, loc *time.Location, after, lastStart time.Time) (time.Time, bool) {
	switch s.Mode {
	case ModeInterval:
		every := time.Duration(s.IntervalHours) * time.Hour
		if !lastStart.IsZero() {
			if t := lastStart.Add(every); t.After(after) {
				return t, true
			}
		}
		return after.Add(every), true
	case ModeDaily, ModeWeekly:
		y, m, d := after.In(loc).Date()
		// Eight days always contain the next weekly slot (today's may have passed).
		for i := 0; i <= 7; i++ {
			// Calendar arithmetic at noon UTC: no zone transition can shift the date.
			day := time.Date(y, m, d+i, 12, 0, 0, 0, time.UTC)
			if s.Mode == ModeWeekly && day.Weekday() != s.Weekday {
				continue
			}
			dy, dm, dd := day.Date()
			if t := slot(dy, dm, dd, s.TimeOfDay, loc); t.After(after) {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// slot is the earliest instant whose wall clock in loc shows the date at minute.
func slot(y int, m time.Month, d, minute int, loc *time.Location) time.Time {
	t := time.Date(y, m, d, minute/60, minute%60, 0, 0, loc)
	// time.Date picks the later instant of a repeated hour; step back to the earlier one.
	for _, back := range []time.Duration{time.Hour, 30 * time.Minute} {
		if e := t.Add(-back); sameWall(e.In(loc), t.In(loc)) {
			return e
		}
	}
	return t
}

func sameWall(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd && a.Hour() == b.Hour() && a.Minute() == b.Minute()
}
```

- [ ] **Step 4: `make check`**

Run: `make check`
Expected: PASS (die Zeitplan-Tests kompilieren; die übrigen Paketdateien sind noch der Stub aus Phase A).

- [ ] **Step 5: Test-Umgebung und Fakes schreiben** – `internal/backups/helpers_test.go`

```go
package backups_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// t0 is a Monday, 12:00 in Europe/Berlin (CEST).
var t0 = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

// env is a real database plus the backups handler on a real rbac.Router with fakes for the
// files service, the agent's instance and console APIs and the notifier.
type env struct {
	t       *testing.T
	db      *sql.DB
	clk     *clock.Fake
	rec     *ctxRecorder
	files   *fakeFiles
	agent   *fakeAgent
	console *fakeConsole
	notes   *notify.Recording
	kicks   *atomic.Int32
	h       *backups.Handler
	mux     *chi.Mux
	rt      *rbac.Router // registered routes (routes_test.go: route contract)
	rootID  int64
	stop    func()
}

// newEnv builds the env and starts Run.
func newEnv(t *testing.T) *env {
	e := newEnvNoRun(t, nil)
	e.run()
	return e
}

// newEnvNoRun builds the env without Run (recovery tests seed the DB first); opt may change
// the dependencies (e.g. Files = nil) before the handler is built.
func newEnvNoRun(t *testing.T, opt func(*backups.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &ctxRecorder{}, agent: newFakeAgent(),
		console: newFakeConsole(), notes: &notify.Recording{}, kicks: &atomic.Int32{},
	}
	e.files = newFakeFiles(e.clk)
	d := backups.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Notifier: e.notes, Files: e.files, Instances: e.agent,
		Console: e.console, Reconcile: func() { e.kicks.Add(1) },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = backups.NewHandler(d)
	backups.SetTimings(e.h, backups.Timings{
		Poll: 5 * time.Millisecond, RCON: 2 * time.Second, Stop: 300 * time.Millisecond,
		Shutdown: 5 * time.Second, IO: 2 * time.Second,
	})
	backups.SetTickEvery(e.h, time.Hour) // tests tick by hand (backups.Tick)
	mux := chi.NewRouter()
	e.rt = rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk)
	e.h.Routes(e.rt)
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	e.stop = func() {}
	return e
}

// run starts Run in the background; stop (also at cleanup) cancels it and waits.
func (e *env) run() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	var once sync.Once
	e.stop = func() { once.Do(func() { cancel(); <-done }) }
	e.t.Cleanup(e.stop)
}

func (e *env) admin() principal.Principal {
	return principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1}
}

// sudoAdmin is admin with a fresh sudo window (restore and delete need it).
func (e *env) sudoAdmin() principal.Principal {
	p := e.admin()
	p.SudoUntil = t0.Add(72 * time.Hour)
	return p
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func bPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d/backups%s", id, suffix) }

// do sends a JSON request with client IP 203.0.113.7 as p (nil = anonymous). body may be a
// string (sent as is) or any JSON value.
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
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

// must is do as p that must answer want.
func (e *env) must(want int, method, path string, body any, p principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := e.do(method, path, body, &p)
	if rec.Code != want {
		e.t.Fatalf("%s %s = %d %s, want %d", method, path, rec.Code, rec.Body, want)
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
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// instance inserts a paper server with an instance.
func (e *env) instance(name string) int64 {
	e.t.Helper()
	id := storetest.CreateServer(e.t, e.db, name)
	if _, err := store.New(e.db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: string(instancespec.TypePaper), Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// schedule returns the server's row (it must exist).
func (e *env) schedule(id int64) store.BackupSchedule {
	e.t.Helper()
	r, err := store.New(e.db).BackupsGetSchedule(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

// eventually polls cond for up to 5 s (background jobs use wall time).
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ctxRecorder records audit entries together with the actor from the context.
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
```

- [ ] **Step 6: Fakes schreiben** – `internal/backups/fakes_test.go`

```go
package backups_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// fakeFiles is an in-memory mcpanel-files for the backup endpoints. A service job starts as
// running and ends at the first Job poll, unless hold[plan] is an open channel (then it runs
// until the channel is closed). plan is the snapshot kind for create ("manual", "scheduled",
// "pre-restore"), "restore" or "forget". fail[plan] ends such a job as failed with that text and
// failCode[plan] as its wire code (filesapi.Job.Code, "" = none).
type fakeFiles struct {
	mu        sync.Mutex
	clk       *clock.Fake
	status    filesapi.BackupsStatus
	snaps     map[int64][]filesapi.Snapshot // newest first
	jobs      map[int64]*fakeJob            // last job per instance
	hold      map[string]chan struct{}
	fail      map[string]string
	failCode  map[string]string
	createTZ  map[int64]string   // Timezone of the last CreateBackup per server
	errs      map[string]error   // by operation: status, list, create, restore, forget, pin, download, purge, cancel, job
	errQueue  map[string][]error // used up one per call before errs
	loseJobs  bool               // Job answers nil (a restarted service)
	calls     []string
	seq       int
	zip       []byte
	dlBody    func() io.ReadCloser // overrides the download body
	downloads int                  // DownloadBackup calls that returned a body
}

type fakeJob struct {
	job       filesapi.Job
	plan      string
	snapKind  filesapi.SnapshotKind
	note      string
	forgetIDs []string
	restoreOf string
}

func newFakeFiles(clk *clock.Fake) *fakeFiles {
	return &fakeFiles{
		clk: clk, status: filesapi.BackupsStatus{Available: true, Version: "0.19.1"},
		snaps: map[int64][]filesapi.Snapshot{}, jobs: map[int64]*fakeJob{}, hold: map[string]chan struct{}{},
		fail: map[string]string{}, failCode: map[string]string{}, createTZ: map[int64]string{}, errs: map[string]error{}, errQueue: map[string][]error{},
		zip: []byte("PK\x05\x06" + strings.Repeat("\x00", 18)), // an empty zip archive
	}
}

func short(id string) string { return id[:8] }

// errFor returns the injected error of op (queue first); call with f.mu held.
func (f *fakeFiles) errFor(op string) error {
	if q := f.errQueue[op]; len(q) > 0 {
		f.errQueue[op] = q[1:]
		return q[0]
	}
	return f.errs[op]
}

// add seeds a snapshot and returns its id.
func (f *fakeFiles) add(id int64, kind filesapi.SnapshotKind, pinned bool, note string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(id, kind, pinned, note)
}

func (f *fakeFiles) addLocked(id int64, kind filesapi.SnapshotKind, pinned bool, note string) string {
	f.seq++
	sid := fmt.Sprintf("%064x", f.seq)
	s := filesapi.Snapshot{ID: sid, ShortID: sid[:8], Time: f.clk.Now().UnixMilli(), Kind: kind, Pinned: pinned, Note: note, Bytes: 4096}
	f.snaps[id] = append([]filesapi.Snapshot{s}, f.snaps[id]...)
	return sid
}

// holdPlan keeps the next jobs of plan running; the returned func releases them.
func (f *fakeFiles) holdPlan(plan string) func() {
	ch := make(chan struct{})
	f.mu.Lock()
	f.hold[plan] = ch
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (f *fakeFiles) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeFiles) count(prefix string) int {
	n := 0
	for _, c := range f.callLog() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeFiles) list(id int64) []filesapi.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.snaps[id])
}

// start creates a running job; call with f.mu held.
func (f *fakeFiles) start(id int64, kind filesapi.JobKind, plan string) (*fakeJob, error) {
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		return nil, fmt.Errorf("%w: a job is running for this server", filesapi.ErrBusy)
	}
	f.seq++
	j := &fakeJob{plan: plan, job: filesapi.Job{
		ID: fmt.Sprintf("job%d", f.seq), Kind: kind, State: filesapi.JobRunning, StartedAt: f.clk.Now().UnixMilli(),
	}}
	f.jobs[id] = j
	return j, nil
}

func (f *fakeFiles) BackupsStatus(context.Context) (filesapi.BackupsStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor("status"); err != nil {
		return filesapi.BackupsStatus{}, err
	}
	return f.status, nil
}

func (f *fakeFiles) Snapshots(_ context.Context, id int64) ([]filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor("list"); err != nil {
		return nil, err
	}
	out := slices.Clone(f.snaps[id])
	if out == nil {
		out = []filesapi.Snapshot{}
	}
	return out, nil
}

func (f *fakeFiles) CreateBackup(_ context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("create %d %s", req.ID, req.Kind))
	f.createTZ[req.ID] = req.Timezone
	if err := f.errFor("create"); err != nil {
		return filesapi.Job{}, err
	}
	j, err := f.start(req.ID, filesapi.JobBackup, string(req.Kind))
	if err != nil {
		return filesapi.Job{}, err
	}
	j.snapKind, j.note = req.Kind, req.Note
	return j.job, nil
}

func (f *fakeFiles) RestoreBackup(_ context.Context, id int64, snapshot string) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("restore %d %s", id, short(snapshot)))
	if err := f.errFor("restore"); err != nil {
		return filesapi.Job{}, err
	}
	if !slices.ContainsFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snapshot }) {
		return filesapi.Job{}, filesapi.ErrNotFound
	}
	j, err := f.start(id, filesapi.JobRestore, "restore")
	if err != nil {
		return filesapi.Job{}, err
	}
	j.restoreOf = snapshot
	j.job.Snapshot = snapshot
	return j.job, nil
}

func (f *fakeFiles) ForgetBackups(_ context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Policy != nil {
		p := req.Policy
		f.calls = append(f.calls, fmt.Sprintf("forget %d policy %d/%d/%d/%d %s", req.ID, p.KeepLast, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, req.Timezone))
	} else {
		ids := make([]string, len(req.Snapshots))
		for i, s := range req.Snapshots {
			ids[i] = short(s)
		}
		f.calls = append(f.calls, fmt.Sprintf("forget %d ids %s", req.ID, strings.Join(ids, ",")))
	}
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	if err := f.errFor("forget"); err != nil {
		return filesapi.Job{}, err
	}
	for _, s := range req.Snapshots {
		if !slices.ContainsFunc(f.snaps[req.ID], func(x filesapi.Snapshot) bool { return x.ID == s }) {
			return filesapi.Job{}, filesapi.ErrNotFound
		}
	}
	j, err := f.start(req.ID, filesapi.JobForget, "forget")
	if err != nil {
		return filesapi.Job{}, err
	}
	j.forgetIDs = slices.Clone(req.Snapshots)
	return j.job, nil
}

func (f *fakeFiles) PinBackup(_ context.Context, id int64, snapshot string, pinned bool) (filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("pin %d %s %v", id, short(snapshot), pinned))
	if err := f.errFor("pin"); err != nil {
		return filesapi.Snapshot{}, err
	}
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		return filesapi.Snapshot{}, filesapi.ErrBusy
	}
	i := slices.IndexFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snapshot })
	if i < 0 {
		return filesapi.Snapshot{}, filesapi.ErrNotFound
	}
	s := f.snaps[id][i]
	if s.Kind != filesapi.SnapshotScheduled {
		return filesapi.Snapshot{}, fmt.Errorf("%w: only scheduled snapshots can be pinned", filesapi.ErrInvalid)
	}
	f.seq++
	s.ID = fmt.Sprintf("%064x", f.seq) // restic rewrites the snapshot: a new id
	s.ShortID, s.Pinned = s.ID[:8], pinned
	f.snaps[id][i] = s
	return s, nil
}

func (f *fakeFiles) DownloadBackup(_ context.Context, id int64, snapshot string) (filesapi.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("download %d %s", id, short(snapshot)))
	if err := f.errFor("download"); err != nil {
		return filesapi.Download{}, err
	}
	if !slices.ContainsFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snapshot }) {
		return filesapi.Download{}, filesapi.ErrNotFound
	}
	f.downloads++
	body := io.NopCloser(bytes.NewReader(f.zip))
	if f.dlBody != nil {
		body = f.dlBody()
	}
	return filesapi.Download{Body: body, Size: -1, IsDir: true}, nil
}

func (f *fakeFiles) PurgeBackups(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("purge %d", id))
	if err := f.errFor("purge"); err != nil {
		return err
	}
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		return filesapi.ErrBusy
	}
	delete(f.snaps, id)
	return nil
}

func (f *fakeFiles) CancelJob(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("cancel %d", id))
	if err := f.errFor("cancel"); err != nil {
		return err
	}
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		j.job.State, j.job.Error, j.job.FinishedAt = filesapi.JobFailed, "canceled", f.clk.Now().UnixMilli()
	}
	return nil
}

func (f *fakeFiles) Job(_ context.Context, id int64) (*filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor("job"); err != nil {
		return nil, err
	}
	j := f.jobs[id]
	if j == nil || f.loseJobs {
		return nil, nil
	}
	if j.job.State == filesapi.JobRunning {
		j.job.Bytes += 1024
		held := false
		if ch, ok := f.hold[j.plan]; ok {
			select {
			case <-ch:
			default:
				held = true
			}
		}
		if !held {
			f.complete(id, j)
		}
	}
	out := j.job
	return &out, nil
}

// complete ends a running job as planned; call with f.mu held.
func (f *fakeFiles) complete(id int64, j *fakeJob) {
	j.job.FinishedAt = f.clk.Now().UnixMilli()
	if msg := f.fail[j.plan]; msg != "" {
		j.job.State, j.job.Error, j.job.Code = filesapi.JobFailed, msg, f.failCode[j.plan]
		return
	}
	j.job.State = filesapi.JobDone
	switch j.job.Kind {
	case filesapi.JobBackup:
		j.job.Snapshot = f.addLocked(id, j.snapKind, false, j.note)
	case filesapi.JobForget:
		f.snaps[id] = slices.DeleteFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return slices.Contains(j.forgetIDs, s.ID) })
	}
}

// runningJob seeds a running service job of kind (recovery tests); it stays running while
// hold[plan] is open.
func (f *fakeFiles) runningJob(id int64, kind filesapi.JobKind, plan string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.start(id, kind, plan); err != nil {
		panic(err)
	}
}

var _ filesapi.BackupClient = (*fakeFiles)(nil)

// fakeAgent is the agent's instance API: states per server, stop takes effect at once unless
// stuck.
type fakeAgent struct {
	mu     sync.Mutex
	state  map[int64]agentapi.ContainerState
	powers []string
	stuck  bool
	err    error
}

func newFakeAgent() *fakeAgent { return &fakeAgent{state: map[int64]agentapi.ContainerState{}} }

func (a *fakeAgent) set(id int64, s agentapi.ContainerState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state[id] = s
}

func (a *fakeAgent) powerLog() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.powers)
}

func (a *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return agentapi.InstancesResponse{}, a.err
	}
	out := agentapi.InstancesResponse{Docker: agentapi.DockerOK}
	for id, s := range a.state {
		out.Instances = append(out.Instances, agentapi.InstanceStatus{ID: id, State: s})
	}
	return out, nil
}

func (a *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error { return nil }

func (a *fakeAgent) Power(_ context.Context, id int64, action agentapi.PowerAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.powers = append(a.powers, fmt.Sprintf("%s %d", action, id))
	if a.err != nil {
		return a.err
	}
	if action == agentapi.PowerStop && !a.stuck {
		a.state[id] = agentapi.StateExited
	}
	return nil
}

func (a *fakeAgent) Remove(context.Context, int64) error { return nil }

// fakeConsole records RCON commands as "<id> <command>"; errs[command] is used up one per call.
type fakeConsole struct {
	mu   sync.Mutex
	cmds []string
	errs map[string][]error
}

func newFakeConsole() *fakeConsole { return &fakeConsole{errs: map[string][]error{}} }

func (c *fakeConsole) failNext(cmd string, errs ...error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errs[cmd] = append(c.errs[cmd], errs...)
}

func (c *fakeConsole) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.cmds)
}

func (c *fakeConsole) Rcon(_ context.Context, id int64, cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cmds = append(c.cmds, fmt.Sprintf("%d %s", id, cmd))
	if q := c.errs[cmd]; len(q) > 0 {
		c.errs[cmd] = q[1:]
		return "", q[0]
	}
	return "ok", nil
}

func (c *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}

func (c *fakeConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrUnavailable
}
```

- [ ] **Step 7: Test-Hooks** – `internal/backups/export_test.go`

```go
package backups

import (
	"context"
	"time"
)

// Timings shortens the waits of the handler (all wall time).
type Timings struct{ Poll, RCON, Stop, Shutdown, IO time.Duration }

func SetTimings(h *Handler, t Timings) {
	h.pollEvery, h.rconTimeout, h.stopTimeout, h.shutdownWait, h.ioTimeout = t.Poll, t.RCON, t.Stop, t.Shutdown, t.IO
}

// SetTickEvery sets the ticker interval of Run (must be called before Run).
func SetTickEvery(h *Handler, d time.Duration) { h.tickEvery = d }
```

(Aufgabe 10 ergänzt `Tick` und `WaitIdle`.)

- [ ] **Step 8: Zeitplan-Route testen** – `internal/backups/schedule_api_test.go`

```go
package backups_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
)

type scheduleJSON struct {
	Mode           string  `json:"mode"`
	IntervalHours  int     `json:"interval_hours"`
	TimeOfDay      int     `json:"time_of_day"`
	Weekday        int     `json:"weekday"`
	KeepLast       int     `json:"keep_last"`
	KeepDaily      int     `json:"keep_daily"`
	KeepWeekly     int     `json:"keep_weekly"`
	KeepMonthly    int     `json:"keep_monthly"`
	Timezone       string  `json:"timezone"`
	NextRunAt      *int64  `json:"next_run_at"`
	LastRunAt      *int64  `json:"last_run_at"`
	LastStatus     *string `json:"last_status"`
	LastError      *string `json:"last_error"`
	LastSnapshotID *string `json:"last_snapshot_id"`
}

func scheduleBody(mode string, interval, timeOfDay, weekday int) map[string]any {
	return map[string]any{
		"mode": mode, "interval_hours": interval, "time_of_day": timeOfDay, "weekday": weekday,
		"keep_last": 24, "keep_daily": 7, "keep_weekly": 4, "keep_monthly": 6,
	}
}

func putSchedule(e *env, id int64, body any) scheduleJSON {
	e.t.Helper()
	rec := e.must(http.StatusOK, http.MethodPut, bPath(id, "/schedule"), body, e.admin())
	return decode[struct {
		Schedule scheduleJSON `json:"schedule"`
	}](e.t, rec).Schedule
}

func TestUpdateScheduleDaily(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	s := putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	want := time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC).UnixMilli() // 04:00 CEST tomorrow
	if s.Mode != "daily" || s.TimeOfDay != 240 || s.Timezone != "Europe/Berlin" || s.NextRunAt == nil || *s.NextRunAt != want {
		t.Fatalf("schedule = %+v, next want %d", s, want)
	}
	if s.LastRunAt != nil || s.LastStatus != nil || s.LastError != nil || s.LastSnapshotID != nil {
		t.Fatalf("last_* must be null before any run: %+v", s)
	}
	if row := e.schedule(id); !row.NextRunAt.Valid || row.NextRunAt.Int64 != want || row.Mode != "daily" {
		t.Fatalf("row = %+v", row)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionBackupScheduleUpdate || entries[0].Result != audit.ResultSuccess || actors[0] != "root" {
		t.Fatalf("audit = %+v %v", entries, actors)
	}
	to, _ := entries[0].Details["to"].(map[string]any)
	if to["mode"] != "daily" || to["time_of_day"] != 240 {
		t.Fatalf("audit details = %+v", entries[0].Details)
	}
}

func TestUpdateScheduleOffClearsNextRun(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	if s := putSchedule(e, id, scheduleBody("off", 24, 240, 0)); s.NextRunAt != nil {
		t.Fatalf("off keeps next_run_at %v", *s.NextRunAt)
	}
}

func TestUpdateScheduleIntervalAnchorsOnLastRun(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, last_run_at, updated_at) VALUES (?, ?, 0)`, id, t0.Add(-2*time.Hour).UnixMilli())
	s := putSchedule(e, id, scheduleBody("interval", 6, 240, 0))
	if want := t0.Add(4 * time.Hour).UnixMilli(); s.NextRunAt == nil || *s.NextRunAt != want {
		t.Fatalf("next = %v, want %d", s.NextRunAt, want)
	}
}

func TestUpdateScheduleValidation(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	missing := scheduleBody("daily", 24, 240, 0)
	delete(missing, "keep_monthly")
	noKeep := scheduleBody("daily", 24, 240, 0)
	noKeep["keep_last"], noKeep["keep_daily"], noKeep["keep_weekly"], noKeep["keep_monthly"] = 0, 0, 0, 0
	unknown := scheduleBody("daily", 24, 240, 0)
	unknown["cron"] = "* * * * *"
	cases := []struct {
		name string
		body any
		code string
	}{
		{"missing field", missing, "validation_failed"},
		{"bad mode", scheduleBody("hourly", 24, 240, 0), "validation_failed"},
		{"interval 0", scheduleBody("interval", 0, 240, 0), "validation_failed"},
		{"time 1440", scheduleBody("daily", 24, 1440, 0), "validation_failed"},
		{"weekday 7", scheduleBody("weekly", 24, 240, 7), "validation_failed"},
		{"no retention", noKeep, "validation_failed"},
		{"unknown field", unknown, "bad_request"},
		{"not json", "{", "bad_request"},
	}
	for _, c := range cases {
		a := e.admin()
		rec := e.do(http.MethodPut, bPath(id, "/schedule"), c.body, &a)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != c.code {
			t.Errorf("%s: %d %s, want 400 %s", c.name, rec.Code, rec.Body, c.code)
		}
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("rejected requests were audited: %v", e.rec.actions())
	}
}

func TestUpdateSchedulePermissions(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	viewer := userP(storeUser(t, e, "viewer"), "viewer")
	grant(t, e.db, viewer.UserID, id, "server.view")
	if rec := e.do(http.MethodPut, bPath(id, "/schedule"), scheduleBody("daily", 24, 240, 0), &viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("server.view only = %d", rec.Code)
	}
	op := userP(storeUser(t, e, "op"), "op")
	grant(t, e.db, op.UserID, id, "server.view", "server.backups")
	e.must(http.StatusOK, http.MethodPut, bPath(id, "/schedule"), scheduleBody("daily", 24, 240, 0), op)
	a := e.admin()
	if rec := e.do(http.MethodPut, bPath(9999, "/schedule"), scheduleBody("daily", 24, 240, 0), &a); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server = %d", rec.Code)
	}
}
```

Dazu in `helpers_test.go` ergänzen:

```go
func storeUser(t *testing.T, e *env, name string) int64 {
	t.Helper()
	return storetest.CreateUser(t, e.db, name, "user")
}
```

- [ ] **Step 9: Rot prüfen**

Run: `git add -N internal/backups && make check`
Expected: FAIL – `undefined: backups.SetTimings`, `backups.Timings`-Felder passen nicht zum Stub, `h.pollEvery` unbekannt (der Stub aus Phase A hat diese Felder nicht).

- [ ] **Step 10: `internal/backups/handler.go` ersetzen**

```go
// Package backups implements backups of Minecraft servers (sub-project 4): manual and
// scheduled snapshots with retention, pinning, restore, deletion and zip downloads. Data is
// only read and written by the unprivileged mcpanel-files service (restic runs there); the
// panel orders the steps, keeps the schedules and sends RCON commands through the agent.
package backups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

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

// Timings (spec §5, Global Constraints). The waits use wall time; clock.Clock only drives
// logic (schedules, timestamps, the notification throttle). Tests shorten them.
const (
	tickEvery      = time.Minute
	pollEvery      = 2 * time.Second
	rconTimeout    = 60 * time.Second
	stopTimeout    = 120 * time.Second
	shutdownWait   = 20 * time.Second
	cleanupTimeout = 10 * time.Second
	ioTimeout      = 60 * time.Second
	maxPollMisses  = 5
)

type Handler struct {
	q         *store.Queries
	clock     clock.Clock
	rec       audit.Recorder
	notifier  notify.Notifier
	files     filesapi.BackupClient    // nil = no service
	inst      agentapi.InstancesClient // nil = no agent
	console   agentapi.ConsoleClient
	reconcile func()
	log       *slog.Logger

	tickEvery, pollEvery, rconTimeout, stopTimeout, shutdownWait, ioTimeout time.Duration
	ioClock                                                              clock.Clock // wall clock for I/O deadlines (always clock.Real)
}

func NewHandler(d Deps) *Handler {
	n := d.Notifier
	if n == nil {
		n = notify.Nop{}
	}
	con := d.Console
	if con == nil {
		con = noConsole{}
	}
	rf := d.Reconcile
	if rf == nil {
		rf = func() {}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, notifier: n, files: d.Files, inst: d.Instances,
		console: con, reconcile: rf, log: log,
		tickEvery: tickEvery, pollEvery: pollEvery, rconTimeout: rconTimeout, stopTimeout: stopTimeout,
		shutdownWait: shutdownWait, ioTimeout: ioTimeout, ioClock: clock.Real{},
	}
}

// Routes registers the backup routes (paths relative to /api, contract V3).
func (h *Handler) Routes(api *rbac.Router) {
	rule := rbac.Server(rbac.PermServerBackups, "id")
	sudo := rule.WithSudo()
	api.Get("/servers/{id}/backups", rule, h.notImplemented)
	api.Post("/servers/{id}/backups", rule, h.notImplemented)
	api.Get("/servers/{id}/backups/job", rule, h.notImplemented)
	api.Put("/servers/{id}/backups/schedule", rule, h.handleUpdateSchedule)
	api.Post("/servers/{id}/backups/{snapshot}/pin", rule, h.notImplemented)
	api.Post("/servers/{id}/backups/{snapshot}/restore", sudo, h.notImplemented)
	api.Delete("/servers/{id}/backups/{snapshot}", sudo, h.notImplemented)
	api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.notImplemented)
}

// Run blocks until ctx is done (task 10 adds recovery, the worker and the ticker).
func (h *Handler) Run(ctx context.Context) { <-ctx.Done() }

// DataJobRunning is true while the panel holds a restore job for the server (task 12).
func (h *Handler) DataJobRunning(context.Context, int64) bool { return false }

// PurgeBackups deletes the server's repository (task 12 drops its jobs first).
func (h *Handler) PurgeBackups(ctx context.Context, serverID int64) error {
	if h.files == nil {
		return nil
	}
	return h.files.PurgeBackups(ctx, serverID)
}

// server is a loaded server; HasInstance says whether it has a Minecraft instance.
type server struct {
	ID          int64
	Name        string
	HasInstance bool
}

// loadServer returns the server of the {id} path parameter; it writes 404 for malformed ids
// and unknown servers.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (server, bool) {
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
	_, err = h.q.ServersGetInstance(ctx, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return server{}, false
	}
	return server{ID: s.ID, Name: s.Name, HasInstance: err == nil}, true
}

// snapshotParam returns the {snapshot} path parameter; it writes 400 unless it is 64 hex chars.
func snapshotParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	s := chi.URLParam(r, "snapshot")
	if !filesapi.ValidSnapshotID(s) {
		writeValidation(w, r, "snapshot must be 64 lowercase hex characters")
		return "", false
	}
	return s, true
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

// record writes an audit entry; actor and IP come from ctx (a job keeps those of the request
// that started it). A failing audit write is logged.
func (h *Handler) record(ctx context.Context, action string, serverID int64, res audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: serverTarget(serverID), Result: res, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "server_id", serverID, "err", err)
	}
}

var (
	errBackupRunning    = errors.New("a backup job is already queued or running for this server")
	errShuttingDown     = errors.New("the panel is shutting down")
	errNoInstance       = errors.New("the server has no instance yet")
	errSnapshotNotFound = errors.New("snapshot not found")
	errNotAvailable     = errors.New("backups are not available on this host (restic is missing)")
	errTooManyDownloads = errors.New("at most 2 backup downloads at a time, try again when one is done")
	errNoFiles          = fmt.Errorf("%w: no file service configured", filesapi.ErrUnavailable)
	// errFilesBusy: the service refused a synchronous call (pin) with 429 busy while no panel
	// job runs for the server (a download or an extract in the files tab).
	errFilesBusy = errors.New("the file service is busy with this server (download or extract); try again later")
)

// classify maps an error to its API answer; ok is false for an internal error.
func classify(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, errBackupRunning):
		return http.StatusConflict, httpx.CodeBackupRunning, errBackupRunning.Error(), true
	case errors.Is(err, filesapi.ErrBusy):
		// same code as a panel job (V3 knows no other 409 for pin), but a message that says why
		return http.StatusConflict, httpx.CodeBackupRunning, errFilesBusy.Error(), true
	case errors.Is(err, errNoInstance):
		return http.StatusConflict, httpx.CodeConflict, errNoInstance.Error(), true
	case errors.Is(err, errSnapshotNotFound), errors.Is(err, filesapi.ErrNotFound):
		return http.StatusNotFound, httpx.CodeNotFound, errSnapshotNotFound.Error(), true
	case errors.Is(err, errNotAvailable), errors.Is(err, filesapi.ErrBackupsUnavailable):
		return http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, errNotAvailable.Error(), true
	case errors.Is(err, errShuttingDown):
		return http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, errShuttingDown.Error(), true
	case errors.Is(err, filesapi.ErrInvalid):
		return http.StatusBadRequest, httpx.CodeValidation, "the file service refused the request: " + err.Error(), true
	case errors.Is(err, errTooManyDownloads):
		return http.StatusTooManyRequests, httpx.CodeRateLimited, errTooManyDownloads.Error(), true
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable, httpx.CodeFilesUnavailable, "the file service is not reachable", true
	}
	return http.StatusInternalServerError, httpx.CodeInternal, "internal server error", false
}

// rejected reports whether err refused a request (not audited) rather than a failed action.
func rejected(err error) bool {
	return errors.Is(err, errBackupRunning) || errors.Is(err, errNoInstance) || errors.Is(err, errSnapshotNotFound) ||
		errors.Is(err, errTooManyDownloads) || errors.Is(err, filesapi.ErrBusy) || errors.Is(err, filesapi.ErrNotFound) ||
		errors.Is(err, filesapi.ErrInvalid)
}

// writeError answers err (classify); a client that went away gets nothing.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		h.log.DebugContext(r.Context(), "backups: request ended", "err", err)
		return
	}
	status, code, msg, ok := classify(err)
	if !ok {
		httpx.Internal(w, r, err)
		return
	}
	if status >= 500 {
		h.log.WarnContext(r.Context(), "backups: request failed", "code", code, "err", err)
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "5")
	}
	httpx.WriteError(w, r, status, code, msg)
}

// noConsole stands in for a missing agent console client.
type noConsole struct{}

func (noConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}
func (noConsole) Rcon(context.Context, int64, string) (string, error) { return "", agentapi.ErrUnavailable }
func (noConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrUnavailable
}
```

- [ ] **Step 11: `internal/backups/stubs.go`**

```go
package backups

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// notImplemented answers the routes that later tasks of phase B2 implement (deleted in task 13).
func (h *Handler) notImplemented(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeBackupsUnavailable, "backups not implemented yet")
}
```

- [ ] **Step 12: `internal/backups/store.go`**

```go
package backups

import (
	"context"
	"database/sql"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

func nullTime(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }

func timeOf(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return time.UnixMilli(n.Int64)
}

// nullString maps "" to NULL.
func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func ptrInt(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func ptrString(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// loadSchedule returns the server's row, created with the defaults on first use.
func (h *Handler) loadSchedule(ctx context.Context, serverID int64) (store.BackupSchedule, error) {
	if err := h.q.BackupsEnsureSchedule(ctx, store.BackupsEnsureScheduleParams{ServerID: serverID, UpdatedAt: h.clock.Now().UnixMilli()}); err != nil {
		return store.BackupSchedule{}, err
	}
	return h.q.BackupsGetSchedule(ctx, serverID)
}

func scheduleOf(r store.BackupSchedule) Schedule {
	return Schedule{
		Mode: Mode(r.Mode), IntervalHours: int(r.IntervalHours), TimeOfDay: int(r.TimeOfDay), Weekday: time.Weekday(r.Weekday),
		KeepLast: int(r.KeepLast), KeepDaily: int(r.KeepDaily), KeepWeekly: int(r.KeepWeekly), KeepMonthly: int(r.KeepMonthly),
	}
}

// location returns the panel time zone of the schedules and its configured name. An invalid
// stored name (the settings route validates, so only a hand-edited DB) falls back to UTC
// instead of stopping all backups.
func (h *Handler) location(ctx context.Context) (*time.Location, string, error) {
	name, err := h.q.BackupsGetTimezone(ctx)
	if err != nil {
		return nil, "", err
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		h.log.WarnContext(ctx, "backups: invalid backup_timezone, using UTC", "timezone", name, "err", err)
		return time.UTC, name, nil
	}
	return loc, name, nil
}

// tzName is the zone sent with every backup and every retention forget: restic stores the
// snapshot time in the backup's zone and forget buckets days/weeks/months by it, so both must
// use the same source. An invalid stored name falls back to UTC (location), an unreadable
// setting too – never "" (the service's own zone), which forget would not accept.
func (h *Handler) tzName(ctx context.Context) string {
	loc, _, err := h.location(ctx)
	if err != nil {
		return time.UTC.String()
	}
	return loc.String()
}

// scheduleView is the OpenAPI schema BackupSchedule.
type scheduleView struct {
	Mode           string  `json:"mode"`
	IntervalHours  int64   `json:"interval_hours"`
	TimeOfDay      int64   `json:"time_of_day"`
	Weekday        int64   `json:"weekday"`
	KeepLast       int64   `json:"keep_last"`
	KeepDaily      int64   `json:"keep_daily"`
	KeepWeekly     int64   `json:"keep_weekly"`
	KeepMonthly    int64   `json:"keep_monthly"`
	Timezone       string  `json:"timezone"`
	NextRunAt      *int64  `json:"next_run_at"`
	LastRunAt      *int64  `json:"last_run_at"`
	LastStatus     *string `json:"last_status"`
	LastError      *string `json:"last_error"`
	LastSnapshotID *string `json:"last_snapshot_id"`
}

func viewSchedule(r store.BackupSchedule, tz string) scheduleView {
	return scheduleView{
		Mode: r.Mode, IntervalHours: r.IntervalHours, TimeOfDay: r.TimeOfDay, Weekday: r.Weekday,
		KeepLast: r.KeepLast, KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly, KeepMonthly: r.KeepMonthly,
		Timezone: tz, NextRunAt: ptrInt(r.NextRunAt), LastRunAt: ptrInt(r.LastRunAt),
		LastStatus: ptrString(r.LastStatus), LastError: ptrString(r.LastError), LastSnapshotID: ptrString(r.LastSnapshotID),
	}
}

type scheduleResponse struct {
	Schedule scheduleView `json:"schedule"`
}
```

- [ ] **Step 13: `internal/backups/schedule_api.go`**

```go
package backups

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// updateScheduleRequest is the OpenAPI schema UpdateBackupScheduleRequest; all fields are
// required (pointers detect a missing one).
type updateScheduleRequest struct {
	Mode          *string `json:"mode"`
	IntervalHours *int    `json:"interval_hours"`
	TimeOfDay     *int    `json:"time_of_day"`
	Weekday       *int    `json:"weekday"`
	KeepLast      *int    `json:"keep_last"`
	KeepDaily     *int    `json:"keep_daily"`
	KeepWeekly    *int    `json:"keep_weekly"`
	KeepMonthly   *int    `json:"keep_monthly"`
}

func (req updateScheduleRequest) schedule() (Schedule, error) {
	ints := []struct {
		name string
		v    *int
	}{
		{"interval_hours", req.IntervalHours}, {"time_of_day", req.TimeOfDay}, {"weekday", req.Weekday},
		{"keep_last", req.KeepLast}, {"keep_daily", req.KeepDaily}, {"keep_weekly", req.KeepWeekly}, {"keep_monthly", req.KeepMonthly},
	}
	if req.Mode == nil {
		return Schedule{}, fmt.Errorf("%w: mode is required", errInvalidSchedule)
	}
	for _, f := range ints {
		if f.v == nil {
			return Schedule{}, fmt.Errorf("%w: %s is required", errInvalidSchedule, f.name)
		}
	}
	return Schedule{
		Mode: Mode(*req.Mode), IntervalHours: *req.IntervalHours, TimeOfDay: *req.TimeOfDay, Weekday: time.Weekday(*req.Weekday),
		KeepLast: *req.KeepLast, KeepDaily: *req.KeepDaily, KeepWeekly: *req.KeepWeekly, KeepMonthly: *req.KeepMonthly,
	}, nil
}

func scheduleDetails(s Schedule) map[string]any {
	return map[string]any{
		"mode": string(s.Mode), "interval_hours": s.IntervalHours, "time_of_day": s.TimeOfDay, "weekday": int(s.Weekday),
		"keep_last": s.KeepLast, "keep_daily": s.KeepDaily, "keep_weekly": s.KeepWeekly, "keep_monthly": s.KeepMonthly,
	}
}

func (h *Handler) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req updateScheduleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, err := req.schedule()
	if err == nil {
		err = s.Validate()
	}
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	before, err := h.loadSchedule(ctx, srv.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	loc, tz, err := h.location(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now()
	var next sql.NullInt64
	if t, ok := NextRun(s, loc, now, timeOf(before.LastRunAt)); ok {
		next = nullTime(t)
	}
	if err := h.q.BackupsUpdateSchedule(ctx, store.BackupsUpdateScheduleParams{
		Mode: string(s.Mode), IntervalHours: int64(s.IntervalHours), TimeOfDay: int64(s.TimeOfDay), Weekday: int64(s.Weekday),
		KeepLast: int64(s.KeepLast), KeepDaily: int64(s.KeepDaily), KeepWeekly: int64(s.KeepWeekly), KeepMonthly: int64(s.KeepMonthly),
		NextRunAt: next, UpdatedAt: now.UnixMilli(), ServerID: srv.ID,
	}); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionBackupScheduleUpdate, srv.ID, audit.ResultSuccess,
		map[string]any{"from": scheduleDetails(scheduleOf(before)), "to": scheduleDetails(s)})
	after, err := h.q.BackupsGetSchedule(ctx, srv.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scheduleResponse{Schedule: viewSchedule(after, tz)})
}
```

(Import `database/sql` für `sql.NullInt64` ergänzen.)

- [ ] **Step 14: `routes_test.go` auf die neuen Helfer umstellen**

`internal/backups/routes_test.go` (in Step 5 entfernt) mit dieser Fassung neu anlegen. Sie behält den Routenvertrag und die Rechte-/Sudo-Prüfung aus Phase A, nutzt aber `newEnv`/`env`/`do`/`admin` aus `helpers_test.go` (die Routen hängen dort ohne `/api`-Präfix am Router, deshalb fehlt es in `wantRoutes`). `TestStubsAnswerNotImplemented` wird zu `TestRemainingStubs`; `TestStubGateAndPurge` entfällt (`newEnv` startet `Run` bereits und räumt es beim Cleanup ab; Sperre und Purge testet Aufgabe 12).

```go
package backups_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var (
	backupsRule = rbac.Server(rbac.PermServerBackups, "id")
	snap        = strings.Repeat("ab", 32)
)

// wantRoutes are exactly the backups operations of api/openapi.yaml (V3); the test router has
// no /api prefix.
var wantRoutes = map[string]rbac.Rule{
	"GET /servers/{id}/backups":                     backupsRule,
	"POST /servers/{id}/backups":                    backupsRule,
	"GET /servers/{id}/backups/job":                 backupsRule,
	"PUT /servers/{id}/backups/schedule":            backupsRule,
	"POST /servers/{id}/backups/{snapshot}/pin":     backupsRule,
	"POST /servers/{id}/backups/{snapshot}/restore": backupsRule.WithSudo(),
	"DELETE /servers/{id}/backups/{snapshot}":       backupsRule.WithSudo(),
	"GET /servers/{id}/backups/{snapshot}/download": backupsRule,
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
	if un := rbac.Uncovered(e.mux, got, func(string, string) bool { return false }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// A user without server.backups is refused; restore and delete need sudo even for admins;
// anonymous requests are 401.
func TestRoutesCheckPermissionAndSudo(t *testing.T) {
	e := newEnv(t)
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		if rec := e.do(method, expand(pattern), "{}", &bob); rec.Code != http.StatusForbidden {
			t.Errorf("%s without server.backups: %d", key, rec.Code)
		}
		if rec := e.do(method, expand(pattern), "{}", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
	}
	noSudo := e.admin()
	for _, key := range []string{"POST /servers/{id}/backups/{snapshot}/restore", "DELETE /servers/{id}/backups/{snapshot}"} {
		method, pattern, _ := strings.Cut(key, " ")
		rec := e.do(method, expand(pattern), "{}", &noSudo)
		if rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
			t.Errorf("%s without sudo: %d %s", key, rec.Code, rec.Body)
		}
	}
}

// stubbed are the routes that still answer 503 backups_unavailable; each task of phase B2
// removes its routes, task 13 deletes the list and this test.
var stubbed = []struct{ method, path string }{
	{http.MethodGet, "/backups"},
	{http.MethodPost, "/backups"},
	{http.MethodGet, "/backups/job"},
	{http.MethodPost, "/backups/" + strings.Repeat("a", 64) + "/pin"},
	{http.MethodPost, "/backups/" + strings.Repeat("a", 64) + "/restore"},
	{http.MethodDelete, "/backups/" + strings.Repeat("a", 64)},
	{http.MethodGet, "/backups/" + strings.Repeat("a", 64) + "/download"},
}

func TestRemainingStubs(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	for _, s := range stubbed {
		p := e.sudoAdmin()
		rec := e.do(s.method, fmt.Sprintf("/servers/%d%s", id, s.path), "{}", &p)
		if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "backups_unavailable" {
			t.Errorf("%s %s = %d %s", s.method, s.path, rec.Code, rec.Body)
		}
	}
}
```

(`fmt` braucht nur `TestRemainingStubs`; Aufgabe 13 löscht ihn samt `stubbed` und entfernt dann den Import.)

- [ ] **Step 15: Grün prüfen**

Run: `gofmt -w internal/backups && make check && make test-vm SUITE=go`
Expected: PASS (`TestNextRun*`, `TestScheduleValidate`, `TestUpdateSchedule*`, `TestRemainingStubs`, `TestRouteContract`, `TestRoutesCheckPermissionAndSudo`).

- [ ] **Step 16: Commit**

```bash
git add internal/backups
git commit -m "feat(backups): schedule computation and the schedule route

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 10: Warteschlange, Jobs, Backup-Ablauf und Wiederanlauf

**Files:**
- Create: `internal/backups/jobs.go`, `internal/backups/queue.go`, `internal/backups/backup.go`, `internal/backups/recover.go`, `internal/backups/ticker.go`, `internal/backups/backup_test.go`
- Modify: `internal/backups/handler.go` (Felder `queue`, `tickMu`; `Run`; Routen `POST /backups`, `GET /backups/job`), `internal/backups/export_test.go` (`Tick`, `WaitIdle`), `internal/backups/routes_test.go` (`stubbed` ohne die beiden Routen)

**Interfaces:**
- Consumes: Aufgabe 9 (`Handler`, `loadServer`, `record`, `writeError`, `classify`, Sentinels, `nullTime`, `loadSchedule`), V1 (`CreateBackup`, `Job`, `CancelJob`, `BackupsStatus`), `agentapi.ConsoleClient.Rcon`, `agentapi.InstancesClient.Instances`.
- Produces: `jobView`, `jobError`, `failf`, `jobErrorOf`, `jobSpec`, `job.setStep/setBytes`, `queue` (`add`, `get`, `busy`, `runningFor`, `restoreHeld`, `work`, `drop`, `close`, `idle`), `ready`, `instanceState`, `containerRunning`, `rcon`, `markSaveOff`, `resume`, `waitService`, `cancelService`, `runBackup`, `recover`, `tick`, `retrySaveOn`; Test-Hooks `Tick(ctx, h)`, `WaitIdle(h) bool`.

- [ ] **Step 1: Tests schreiben** – `internal/backups/backup_test.go`

```go
package backups_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type jobJSON struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Trigger       string  `json:"trigger"`
	State         string  `json:"state"`
	Step          string  `json:"step"`
	QueuePosition int     `json:"queue_position"`
	Bytes         int64   `json:"bytes"`
	Snapshot      *string `json:"snapshot"`
	ErrorCode     *string `json:"error_code"`
	Error         string  `json:"error"`
	QueuedAt      int64   `json:"queued_at"`
	StartedAt     int64   `json:"started_at"`
	FinishedAt    int64   `json:"finished_at"`
}

type jobResp struct {
	Job *jobJSON `json:"job"`
}

func (e *env) job(id int64) *jobJSON {
	e.t.Helper()
	return decode[jobResp](e.t, e.must(http.StatusOK, http.MethodGet, bPath(id, "/job"), nil, e.admin())).Job
}

// waitJob waits until the server's job has ended and returns it.
func (e *env) waitJob(id int64) jobJSON {
	e.t.Helper()
	var j *jobJSON
	eventually(e.t, "job end", func() bool {
		j = e.job(id)
		return j != nil && (j.State == "done" || j.State == "failed")
	})
	return *j
}

// waitStep waits until the server's job is running in step.
func (e *env) waitStep(id int64, step string) {
	e.t.Helper()
	eventually(e.t, "step "+step, func() bool {
		j := e.job(id)
		return j != nil && j.State == "running" && j.Step == step
	})
}

func (e *env) create(id int64, body any) jobJSON {
	e.t.Helper()
	rec := e.must(http.StatusAccepted, http.MethodPost, bPath(id, ""), body, e.admin())
	return *decode[jobResp](e.t, rec).Job
}

func code(j jobJSON) string {
	if j.ErrorCode == nil {
		return ""
	}
	return *j.ErrorCode
}

func TestJobRouteBeforeAnyJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	if j := e.job(id); j != nil {
		t.Fatalf("job = %+v, want null", j)
	}
}

func TestCreateBackupStoppedServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	j := e.create(id, map[string]any{"note": "vor Update"})
	if j.Kind != "backup" || j.Trigger != "manual" || j.QueuedAt != t0.UnixMilli() {
		t.Fatalf("queued job = %+v", j)
	}
	done := e.waitJob(id)
	if done.State != "done" || done.Step != "done" || done.Snapshot == nil || done.ErrorCode != nil {
		t.Fatalf("job = %+v", done)
	}
	if got := e.files.callLog(); !slices.Equal(got, []string{fmt.Sprintf("create %d manual", id)}) {
		t.Fatalf("calls = %v", got)
	}
	if s := e.files.list(id); len(s) != 1 || s[0].Note != "vor Update" || s[0].ID != *done.Snapshot {
		t.Fatalf("snapshots = %+v", s)
	}
	if cmds := e.console.sent(); len(cmds) != 0 {
		t.Fatalf("a stopped server got RCON: %v", cmds)
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionBackupCreate || entries[0].Result != audit.ResultSuccess ||
		actors[0] != "root" || entries[0].Details["snapshot"] != *done.Snapshot || entries[0].Details["note"] != "vor Update" {
		t.Fatalf("audit = %+v %v", entries, actors)
	}
}

func TestCreateBackupRunningServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	want := []string{fmt.Sprintf("%d save-off", id), fmt.Sprintf("%d save-all flush", id), fmt.Sprintf("%d save-on", id)}
	if got := e.console.sent(); !slices.Equal(got, want) {
		t.Fatalf("rcon = %v, want %v", got, want)
	}
	if row := e.schedule(id); row.SaveoffSince.Valid {
		t.Fatalf("saveoff_since still set: %+v", row)
	}
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	for _, note := range []string{strings.Repeat("ä", 65), "a\nb", "   "} {
		a := e.admin()
		if rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{"note": note}, &a); rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("note %q = %d %s", note, rec.Code, rec.Body)
		}
	}
	lobby := storeServer(t, e, "lobby") // no instance
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(lobby, ""), map[string]any{}, &a); rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("no instance = %d %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("rejections audited: %v", e.rec.actions())
	}
}

func TestCreateUnavailable(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.status = filesapi.BackupsStatus{Available: false, Reason: "restic not found"}
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "backups_unavailable" {
		t.Fatalf("no restic = %d %s", rec.Code, rec.Body)
	}
	e2 := newEnvNoRun(t, func(d *backups.Deps) { d.Files = nil })
	e2.run()
	id2 := e2.instance("survival")
	a2 := e2.admin()
	if rec := e2.do(http.MethodPost, bPath(id2, ""), map[string]any{}, &a2); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "files_unavailable" {
		t.Fatalf("no service = %d %s", rec.Code, rec.Body)
	}
}

func TestCreateTwiceConcurrently(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("manual")
	defer release()
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := e.admin()
			rec := e.do(http.MethodPost, bPath(id, ""), map[string]any{}, &a)
			if rec.Code == http.StatusConflict && errCode(t, rec) != "backup_running" {
				t.Errorf("409 with %s", rec.Body)
			}
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	var got []int
	for c := range codes {
		got = append(got, c)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int{http.StatusAccepted, http.StatusConflict}) {
		t.Fatalf("codes = %v", got)
	}
}

func TestQueueRunsOneJobAtATime(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	qb := e.create(b, map[string]any{})
	if qb.State != "queued" || qb.QueuePosition != 1 {
		t.Fatalf("second job = %+v, want queued at 1", qb)
	}
	if n := e.files.count(fmt.Sprintf("create %d", b)); n != 0 {
		t.Fatalf("beta started while alpha runs")
	}
	release()
	if j := e.waitJob(b); j.State != "done" {
		t.Fatalf("beta = %+v", j)
	}
	if got := e.files.callLog(); !slices.Equal(got, []string{fmt.Sprintf("create %d manual", a), fmt.Sprintf("create %d manual", b)}) {
		t.Fatalf("calls = %v", got)
	}
}

// Review Focus 1: every failure after save-off ends with save-on.
func TestSaveOffFailsStillSendsSaveOn(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-off", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "failed" || code(j) != "rcon_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); !slices.Equal(got, []string{fmt.Sprintf("%d save-off", id), fmt.Sprintf("%d save-on", id)}) {
		t.Fatalf("rcon = %v", got)
	}
	if n := e.files.count("create"); n != 0 {
		t.Fatal("the service was called although save-off failed")
	}
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since still set")
	}
}

func TestFlushFailsStillSendsSaveOn(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-all flush", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "rcon_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

func TestSaveOnAfterServiceJobFails(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.files.fail["manual"] = "restic: disk full"
	e.create(id, map[string]any{})
	j := e.waitJob(id)
	if j.State != "failed" || code(j) != "internal" || j.Error != "restic: disk full" {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
	if es, _ := e.rec.all(); len(es) != 1 || es[0].Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", es)
	}
}

// A failed service job carries its wire code (filesapi.Job.Code); the panel job maps it (V5).
func TestServiceJobFailureCodes(t *testing.T) {
	for _, c := range []struct{ wire, want string }{
		{filesapi.CodeTooLarge, "too_large"},
		{filesapi.CodeInsufficientStorage, "insufficient_storage"},
		{filesapi.CodeBusy, "files_busy"},
		{filesapi.CodeUnavailable, "backups_unavailable"},
		{"", "internal"},
		{"something_new", "internal"},
	} {
		t.Run(c.want+"/"+c.wire, func(t *testing.T) {
			e := newEnv(t)
			id := e.instance("survival")
			e.files.fail["manual"] = "the job failed"
			e.files.failCode["manual"] = c.wire
			e.create(id, map[string]any{})
			if j := e.waitJob(id); j.State != "failed" || code(j) != c.want || j.Error != "the job failed" {
				t.Fatalf("job = %+v, want code %s", j, c.want)
			}
		})
	}
}

func TestSaveOnAfterCreateRefused(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.files.errs["create"] = filesapi.ErrBusy // e.g. an extract runs in the files tab
	e.create(id, map[string]any{})
	j := e.waitJob(id)
	if code(j) != "files_busy" || !strings.Contains(j.Error, "busy") {
		t.Fatalf("job = %+v", j)
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
}

func TestSaveOnRetriedByTicker(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-on", agentapi.ErrRconUnavailable)
	e.create(id, map[string]any{})
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("a failed save-on fails the backup: %+v", j)
	}
	if !e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since cleared although save-on failed")
	}
	backups.Tick(t.Context(), e.h)
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) || len(got) != 4 {
		t.Fatalf("rcon = %v, want a second save-on", got)
	}
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since still set after the retry")
	}
}

func TestSaveOnNotRunningClears(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.console.failNext("save-on", agentapi.ErrNotRunning) // the server stopped meanwhile
	e.create(id, map[string]any{})
	e.waitJob(id)
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("a stopped server keeps saveoff_since")
	}
}

func TestShutdownCancelsServiceJobAndResumes(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	e.stop()
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v, want one cancel", e.files.callLog())
	}
	if got := e.console.sent(); got[len(got)-1] != fmt.Sprintf("%d save-on", id) {
		t.Fatalf("rcon = %v", got)
	}
	if e.schedule(id).SaveoffSince.Valid {
		t.Fatal("saveoff_since still set after shutdown")
	}
}

func TestRecoverAfterCrash(t *testing.T) {
	e := newEnvNoRun(t, nil)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	mustExec(t, e.db, `INSERT INTO backup_schedules (server_id, mode, next_run_at, last_status, saveoff_since, updated_at)
		VALUES (?, 'daily', ?, 'running', ?, 0)`, id, t0.Add(-time.Hour).UnixMilli(), t0.Add(-10*time.Minute).UnixMilli())
	release := e.files.holdPlan("scheduled")
	defer release()
	e.files.runningJob(id, filesapi.JobBackup, "scheduled") // the backup the crashed panel started
	e.run()
	eventually(t, "save-on", func() bool { return slices.Contains(e.console.sent(), fmt.Sprintf("%d save-on", id)) })
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	row := e.schedule(id)
	if row.SaveoffSince.Valid || row.LastStatus.String != "failed" || row.LastError.String != "the panel restarted during the backup" {
		t.Fatalf("row = %+v", row)
	}
}

func TestAgentDownFailsBackup(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.err = agentapi.ErrUnavailable
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "agent_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if len(e.console.sent()) != 0 || e.files.count("create") != 0 {
		t.Fatalf("rcon %v, calls %v", e.console.sent(), e.files.callLog())
	}
}

func TestServiceLosesJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.loseJobs = true
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "files_unavailable" {
		t.Fatalf("job = %+v", j)
	}
}

func TestServiceUnreachableWhilePolling(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.errs["job"] = filesapi.ErrUnavailable
	e.create(id, map[string]any{})
	if j := e.waitJob(id); code(j) != "files_unavailable" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count(fmt.Sprintf("cancel %d", id)); n != 1 {
		t.Fatalf("calls = %v, want a cancel after the misses", e.files.callLog())
	}
}
```

Dazu in `helpers_test.go` (Import `time` ist vorhanden, `backup_test.go` braucht `time` ebenfalls im Import-Block):

```go
func storeServer(t *testing.T, e *env, name string) int64 {
	t.Helper()
	return storetest.CreateServer(t, e.db, name)
}
```

- [ ] **Step 2: Rot prüfen**

Run: `git add -N internal/backups && make check`
Expected: FAIL – `undefined: backups.Tick`.

- [ ] **Step 3: `internal/backups/jobs.go`**

```go
package backups

import (
	"context"
	"errors"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	kindBackup  = "backup"
	kindRestore = "restore"
	kindDelete  = "delete"

	triggerManual    = "manual"
	triggerScheduled = "scheduled"

	stateQueued  = "queued"
	stateRunning = "running"
	stateDone    = "done"
	stateFailed  = "failed"

	stepQueued     = "queued"
	stepSaving     = "saving"
	stepBackingUp  = "backing_up"
	stepResuming   = "resuming"
	stepPruning    = "pruning"
	stepStopping   = "stopping"
	stepPreRestore = "pre_restore"
	stepRestoring  = "restoring"
	stepDeleting   = "deleting"
	stepDone       = "done"

	codeRcon        = "rcon_unavailable"
	codeAgent       = "agent_unavailable"
	codeFiles       = "files_unavailable"
	codeFilesBusy   = "files_busy"
	codeBackups     = "backups_unavailable"
	codeStorage     = "insufficient_storage"
	codeTooLarge    = "too_large"
	codeStopTimeout = "stop_timeout"
	codeCanceled    = "canceled"
	codeInternal    = "internal"

	msgCanceled = "the job was canceled (the panel shut down or the server was deleted)"
)

// jobView is the OpenAPI schema BackupJob.
type jobView struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	Trigger       string  `json:"trigger"`
	State         string  `json:"state"`
	Step          string  `json:"step"`
	QueuePosition int     `json:"queue_position"`
	Bytes         int64   `json:"bytes"`
	Snapshot      *string `json:"snapshot"`
	ErrorCode     *string `json:"error_code"`
	Error         string  `json:"error"`
	QueuedAt      int64   `json:"queued_at"`
	StartedAt     int64   `json:"started_at"`
	FinishedAt    int64   `json:"finished_at"`
}

type jobResponse struct {
	Job *jobView `json:"job"`
}

// jobError is the failure of a job step with its BackupJobError code.
type jobError struct{ code, msg string }

func (e *jobError) Error() string { return e.msg }

func failf(code, format string, args ...any) error {
	return &jobError{code: code, msg: fmt.Sprintf(format, args...)}
}

// jobErrorOf maps a job's error to its code and message (V5); messages never carry host paths
// (the service already scrubs its own).
func jobErrorOf(err error) (code, msg string) {
	var je *jobError
	switch {
	case errors.As(err, &je):
		return je.code, je.msg
	case errors.Is(err, context.Canceled):
		return codeCanceled, msgCanceled
	case errors.Is(err, filesapi.ErrBackupsUnavailable):
		return codeBackups, "restic is not available on this host"
	case errors.Is(err, filesapi.ErrInsufficientStorage):
		return codeStorage, "not enough free disk space"
	case errors.Is(err, filesapi.ErrTooLarge):
		return codeTooLarge, "the data directory exceeds the backup limits"
	case errors.Is(err, filesapi.ErrBusy):
		return codeFilesBusy, "the file service is busy with another job for this server (for example an extract); try again later"
	case errors.Is(err, filesapi.ErrNotFound):
		return codeInternal, "the server has no data directory yet or the snapshot is gone"
	case errors.Is(err, filesapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return codeFiles, "the file service is not reachable"
	case errors.Is(err, agentapi.ErrUnavailable), errors.Is(err, agentapi.ErrDockerUnavailable):
		return codeAgent, "the agent is not reachable"
	}
	return codeInternal, err.Error()
}
```

- [ ] **Step 4: `internal/backups/queue.go`**

```go
package backups

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// runFunc runs a job; the snapshot is the job's result (backup) or "".
type runFunc func(ctx context.Context, j *job) (snapshot string, err error)

// jobSpec describes a job to queue.
type jobSpec struct {
	serverID int64
	name     string // server name, for notifications
	kind     string
	trigger  string
	snapshot string // restore/delete: the source snapshot
	run      runFunc
	after    func() // runs after the job has ended (the restore gate is open by then); may be nil
}

type job struct {
	q      *queue
	spec   jobSpec
	view   jobView // guarded by q.mu
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (j *job) setStep(step string) {
	j.q.mu.Lock()
	defer j.q.mu.Unlock()
	j.view.Step, j.view.Bytes = step, 0
}

func (j *job) setBytes(n int64) {
	j.q.mu.Lock()
	defer j.q.mu.Unlock()
	j.view.Bytes = n
}

// queue keeps the backup jobs in memory (spec §5.3): the newest job per server (at most one
// queued or running), a FIFO of waiting jobs and at most one running job in the panel. The
// last result stays until the next job of the server or a panel restart.
type queue struct {
	clock   clock.Clock
	mu      sync.Mutex
	last    map[int64]*job
	waiting []*job
	running *job
	wake    chan struct{}
	closed  bool
}

func newQueue(clk clock.Clock) *queue {
	return &queue{clock: clk, last: map[int64]*job{}, wake: make(chan struct{}, 1)}
}

func newJobID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func active(j *job) bool {
	return j != nil && (j.view.State == stateQueued || j.view.State == stateRunning)
}

// add queues a job. base only passes its values (actor, IP) to the job.
func (q *queue) add(base context.Context, s jobSpec) (jobView, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.closed:
		return jobView{}, errShuttingDown
	case active(q.last[s.serverID]):
		return jobView{}, errBackupRunning
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(base))
	j := &job{q: q, spec: s, ctx: ctx, cancel: cancel, done: make(chan struct{}), view: jobView{
		ID: newJobID(), Kind: s.kind, Trigger: s.trigger, State: stateQueued, Step: stepQueued,
		QueuedAt: q.clock.Now().UnixMilli(),
	}}
	if s.snapshot != "" {
		snap := s.snapshot
		j.view.Snapshot = &snap
	}
	q.last[s.serverID] = j
	q.waiting = append(q.waiting, j)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return q.viewLocked(j), nil
}

// viewLocked copies the job's view with its current queue position; call with q.mu held.
func (q *queue) viewLocked(j *job) jobView {
	v := j.view
	if v.Snapshot != nil {
		s := *v.Snapshot
		v.Snapshot = &s
	}
	if v.ErrorCode != nil {
		c := *v.ErrorCode
		v.ErrorCode = &c
	}
	if v.State == stateQueued {
		v.QueuePosition = slices.Index(q.waiting, j) + 1
	}
	return v
}

func (q *queue) get(serverID int64) *jobView {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.last[serverID]
	if j == nil {
		return nil
	}
	v := q.viewLocked(j)
	return &v
}

// busy reports whether the server has a queued or running job.
func (q *queue) busy(serverID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return active(q.last[serverID])
}

func (q *queue) runningFor(serverID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running != nil && q.running.spec.serverID == serverID
}

// restoreHeld reports whether the server has a restore job that waits or runs: the gate closes
// with the 202, not only when the worker picks the job up (another server's job may still run).
func (q *queue) restoreHeld(serverID int64) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	j := q.last[serverID]
	return active(j) && j.spec.kind == kindRestore
}

// idle reports whether no job waits or runs.
func (q *queue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running == nil && len(q.waiting) == 0
}

// work runs queued jobs one at a time until ctx is done or the queue is closed.
func (q *queue) work(ctx context.Context) {
	for {
		j := q.take(ctx)
		if j == nil {
			return
		}
		snap, err := j.spec.run(j.ctx, j)
		q.finish(j, snap, err)
		if j.spec.after != nil {
			j.spec.after()
		}
	}
}

func (q *queue) take(ctx context.Context) *job {
	for {
		q.mu.Lock()
		if q.closed {
			q.mu.Unlock()
			return nil
		}
		if len(q.waiting) > 0 {
			j := q.waiting[0]
			q.waiting = q.waiting[1:]
			q.running = j
			j.view.State, j.view.StartedAt = stateRunning, q.clock.Now().UnixMilli()
			q.mu.Unlock()
			return j
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-q.wake:
		}
	}
}

func (q *queue) finish(j *job, snapshot string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	j.view.FinishedAt = q.clock.Now().UnixMilli()
	if snapshot != "" {
		s := snapshot
		j.view.Snapshot = &s
	}
	if err != nil {
		code, msg := jobErrorOf(err)
		j.view.State, j.view.ErrorCode, j.view.Error = stateFailed, &code, msg
	} else {
		j.view.State, j.view.Step = stateDone, stepDone
	}
	if q.running == j {
		q.running = nil
	}
	j.cancel()
	close(j.done)
}

// cancelLocked ends a waiting job without running it; call with q.mu held.
func (q *queue) cancelLocked(j *job) {
	code := codeCanceled
	j.view.State, j.view.ErrorCode, j.view.Error = stateFailed, &code, msgCanceled
	j.view.FinishedAt = q.clock.Now().UnixMilli()
	j.cancel()
	close(j.done)
}

// drop ends the server's job: a waiting one is removed and fails as canceled, a running one is
// cancelled and awaited for at most wait.
func (q *queue) drop(serverID int64, wait time.Duration) {
	q.mu.Lock()
	j := q.last[serverID]
	if !active(j) {
		q.mu.Unlock()
		return
	}
	if j.view.State == stateQueued {
		if i := slices.Index(q.waiting, j); i >= 0 {
			q.waiting = slices.Delete(q.waiting, i, i+1)
		}
		q.cancelLocked(j)
		q.mu.Unlock()
		return
	}
	j.cancel()
	done := j.done
	q.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// close refuses new jobs, ends the waiting ones as canceled and cancels the running one.
func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	for _, j := range q.waiting {
		q.cancelLocked(j)
	}
	q.waiting = nil
	if q.running != nil {
		q.running.cancel()
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
```

- [ ] **Step 5: `internal/backups/backup.go`**

```go
package backups

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// ready checks what a job needs before it is queued: an instance (if needInstance), a file
// service and restic on the host.
func (h *Handler) ready(ctx context.Context, srv server, needInstance bool) error {
	if needInstance && !srv.HasInstance {
		return errNoInstance
	}
	if h.files == nil {
		return errNoFiles
	}
	st, err := h.files.BackupsStatus(ctx)
	if err != nil {
		return err
	}
	if !st.Available {
		return fmt.Errorf("%w: %s", errNotAvailable, st.Reason)
	}
	return nil
}

// instanceState asks the agent for the container state; no agent counts as stopped.
func (h *Handler) instanceState(ctx context.Context, id int64) (agentapi.ContainerState, error) {
	if h.inst == nil {
		return agentapi.StateMissing, nil
	}
	resp, err := h.inst.Instances(ctx)
	if err != nil {
		return "", err
	}
	if resp.Docker != agentapi.DockerOK {
		return "", agentapi.ErrDockerUnavailable
	}
	for _, s := range resp.Instances {
		if s.ID == id {
			return s.State, nil
		}
	}
	return agentapi.StateMissing, nil
}

func isRunning(s agentapi.ContainerState) bool {
	return s == agentapi.StateRunning || s == agentapi.StateRestarting
}

// containerRunning reports whether the server runs; an unknown state fails the job (a
// snapshot of a running server without save-off could be inconsistent).
func (h *Handler) containerRunning(ctx context.Context, id int64) (bool, error) {
	st, err := h.instanceState(ctx, id)
	if err != nil {
		return false, failf(codeAgent, "the agent is not reachable, the server state is unknown")
	}
	return isRunning(st), nil
}

func (h *Handler) rcon(ctx context.Context, id int64, cmd string) error {
	ctx, cancel := context.WithTimeout(ctx, h.rconTimeout)
	defer cancel()
	if _, err := h.console.Rcon(ctx, id, cmd); err != nil {
		return failf(codeRcon, "RCON command %q failed: %v", cmd, err)
	}
	return nil
}

// markSaveOff records that the panel is about to send save-off (spec §5.4, crash recovery).
func (h *Handler) markSaveOff(ctx context.Context, id int64) error {
	if _, err := h.loadSchedule(ctx, id); err != nil {
		return err
	}
	return h.q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{SaveoffSince: nullTime(h.clock.Now()), ServerID: id})
}

// resume sends save-on. It clears saveoff_since once the server saves again or is no longer
// running; otherwise the mark stays and the ticker retries (Review Focus 1). It never uses a
// job's context: it must also run after a cancel.
func (h *Handler) resume(id int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), h.rconTimeout)
	defer cancel()
	_, err := h.console.Rcon(ctx, id, "save-on")
	if err != nil && !errors.Is(err, agentapi.ErrNotRunning) && !errors.Is(err, agentapi.ErrNotFound) {
		h.log.WarnContext(ctx, "backups: save-on failed, the next tick retries", "server_id", id, "err", err)
		return false
	}
	if err := h.q.BackupsSetSaveOff(ctx, store.BackupsSetSaveOffParams{ServerID: id}); err != nil {
		h.log.ErrorContext(ctx, "backups: clear saveoff_since", "server_id", id, "err", err)
		return false
	}
	return true
}

// cancelService asks the service to cancel the server's running job (best effort).
func (h *Handler) cancelService(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := h.files.CancelJob(ctx, id); err != nil {
		h.log.WarnContext(ctx, "backups: cancel the service job", "server_id", id, "err", err)
	}
}

// waitService polls the service job jobID of the server until it ended; progress goes to j.
// A cancelled ctx cancels the service job.
func (h *Handler) waitService(ctx context.Context, j *job, id int64, jobID string) (filesapi.Job, error) {
	misses := 0
	for {
		select {
		case <-ctx.Done():
			h.cancelService(id)
			return filesapi.Job{}, ctx.Err()
		case <-time.After(h.pollEvery):
		}
		sj, err := h.files.Job(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				continue // the select above cancels
			}
			if misses++; misses >= maxPollMisses {
				h.cancelService(id)
				return filesapi.Job{}, fmt.Errorf("poll the service job: %w", err)
			}
			continue
		}
		misses = 0
		if sj == nil || sj.ID != jobID {
			return filesapi.Job{}, failf(codeFiles, "the file service lost the job (was it restarted?)")
		}
		j.setBytes(sj.Bytes)
		switch sj.State {
		case filesapi.JobDone:
			return *sj, nil
		case filesapi.JobFailed:
			return *sj, failf(serviceJobCode(sj.Code), "%s", sj.Error)
		}
	}
}

// serviceJobCode maps the wire code of a failed service job (filesapi.Job.Code) to a
// BackupJobError (V5); unknown or empty codes are internal.
func serviceJobCode(code string) string {
	switch code {
	case filesapi.CodeTooLarge:
		return codeTooLarge
	case filesapi.CodeInsufficientStorage:
		return codeStorage
	case filesapi.CodeBusy:
		return codeFilesBusy
	case filesapi.CodeUnavailable:
		return codeBackups
	}
	return codeInternal
}

// runBackup takes one snapshot of kind; a running server gets save-off/save-all flush before
// and save-on after it, in every path (spec §5.4).
func (h *Handler) runBackup(ctx context.Context, j *job, kind filesapi.SnapshotKind, note string) (snapshot string, err error) {
	id := j.spec.serverID
	running, err := h.containerRunning(ctx, id)
	if err != nil {
		return "", err
	}
	if running {
		j.setStep(stepSaving)
		if err := h.markSaveOff(ctx, id); err != nil {
			return "", err
		}
		defer func() {
			j.setStep(stepResuming)
			h.resume(id)
		}()
		if err := h.rcon(ctx, id, "save-off"); err != nil {
			return "", err
		}
		if err := h.rcon(ctx, id, "save-all flush"); err != nil {
			return "", err
		}
	}
	j.setStep(stepBackingUp)
	sj, err := h.files.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: id, Kind: kind, Note: note, Timezone: h.tzName(ctx)})
	if err != nil {
		return "", err
	}
	done, err := h.waitService(ctx, j, id, sj.ID)
	if err != nil {
		return "", err
	}
	if done.Snapshot == "" {
		return "", failf(codeInternal, "the file service reported no snapshot")
	}
	return done.Snapshot, nil
}

// createRequest is the OpenAPI schema CreateBackupRequest.
type createRequest struct {
	Note *string `json:"note"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	note := ""
	if req.Note != nil {
		note = *req.Note
	}
	if !filesapi.ValidNote(note) {
		writeValidation(w, r, "note: at most 64 characters, no control characters, not only spaces")
		return
	}
	if err := h.ready(ctx, srv, true); err != nil {
		h.writeError(w, r, err)
		return
	}
	v, err := h.queue.add(ctx, jobSpec{
		serverID: srv.ID, name: srv.Name, kind: kindBackup, trigger: triggerManual,
		run: func(ctx context.Context, j *job) (string, error) {
			snap, err := h.runBackup(ctx, j, filesapi.SnapshotManual, note)
			details := map[string]any{"note": nil, "snapshot": nil}
			if note != "" {
				details["note"] = note
			}
			if snap != "" {
				details["snapshot"] = snap
			}
			h.record(ctx, audit.ActionBackupCreate, srv.ID, result(err), details)
			return snap, err
		},
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, jobResponse{Job: &v})
}

func (h *Handler) handleJob(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, jobResponse{Job: h.queue.get(srv.ID)})
}
```

Hinweis: In `runBackup` steht das `defer` mit `save-on` **vor** dem ersten RCON-Aufruf, damit auch ein fehlgeschlagenes `save-off` (Ergebnis unbekannt) mit `save-on` endet.

- [ ] **Step 6: `internal/backups/recover.go`**

```go
package backups

import (
	"context"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const msgRestarted = "the panel restarted during the backup"

// recover runs once before the worker starts (spec §5.4, crash of the panel): scheduled runs
// that were still running failed, and every server the panel had sent save-off gets save-on,
// after a backup job the crashed panel left running in the service is cancelled.
func (h *Handler) recover(ctx context.Context) {
	if err := h.q.BackupsFailRunning(ctx, nullString(msgRestarted)); err != nil {
		h.log.ErrorContext(ctx, "backups: mark interrupted runs failed", "err", err)
	}
	rows, err := h.q.BackupsListSaveOff(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list save-off marks", "err", err)
		return
	}
	for _, r := range rows {
		if h.files != nil {
			if sj, err := h.files.Job(ctx, r.ServerID); err == nil && sj != nil && sj.State == filesapi.JobRunning && sj.Kind == filesapi.JobBackup {
				h.cancelService(r.ServerID)
			}
		}
		h.resume(r.ServerID)
	}
}
```

- [ ] **Step 7: `internal/backups/ticker.go`**

```go
package backups

import "context"

// tick runs once per tickEvery (and once when Run starts). Task 11 adds the time zone check
// and the due schedules.
func (h *Handler) tick(ctx context.Context) {
	h.tickMu.Lock()
	defer h.tickMu.Unlock()
	h.retrySaveOn(ctx)
}

// retrySaveOn repeats save-on for servers whose last attempt failed (Review Focus 1); a server
// with a running job is skipped, its job sends save-on itself.
func (h *Handler) retrySaveOn(ctx context.Context) {
	rows, err := h.q.BackupsListSaveOff(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list save-off marks", "err", err)
		return
	}
	for _, r := range rows {
		if !h.queue.runningFor(r.ServerID) {
			h.resume(r.ServerID)
		}
	}
}
```

- [ ] **Step 8: `handler.go` ergänzen**

Im `Handler` nach `log *slog.Logger`:

```go
	queue  *queue
	tickMu sync.Mutex // serialises ticks (Run and tests)
```

In `NewHandler` im Literal `queue: newQueue(d.Clock),` ergänzen; Import `sync`. Routen umstellen:

```go
	api.Post("/servers/{id}/backups", rule, h.handleCreate)
	api.Get("/servers/{id}/backups/job", rule, h.handleJob)
```

`Run` ersetzen:

```go
// Run blocks until ctx is done: recovery first, then the worker and the ticker. On ctx.Done it
// refuses new jobs, cancels the running one (which cancels its service job and sends save-on)
// and waits up to shutdownWait for it.
func (h *Handler) Run(ctx context.Context) {
	h.recover(ctx)
	workDone := make(chan struct{})
	go func() {
		defer close(workDone)
		h.queue.work(ctx)
	}()
	t := time.NewTicker(h.tickEvery)
	defer t.Stop()
	for {
		h.tick(ctx)
		select {
		case <-ctx.Done():
			h.queue.close()
			wait := time.NewTimer(h.shutdownWait)
			defer wait.Stop()
			select {
			case <-workDone:
			case <-wait.C:
				h.log.Warn("backups: the running job did not end within the shutdown wait")
			}
			return
		case <-t.C:
		}
	}
}
```

- [ ] **Step 9: Test-Hooks ergänzen** – `export_test.go`

```go
// Tick runs one ticker pass.
func Tick(ctx context.Context, h *Handler) { h.tick(ctx) }

// WaitIdle waits up to 5 s until no job waits or runs.
func WaitIdle(h *Handler) bool {
	deadline := time.Now().Add(5 * time.Second)
	for !h.queue.idle() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}
```

In `routes_test.go` die Einträge `{http.MethodPost, "/backups"}` und `{http.MethodGet, "/backups/job"}` aus `stubbed` entfernen.

- [ ] **Step 10: Grün prüfen**

Run: `gofmt -w internal/backups && make check && make test-vm SUITE=go`
Expected: PASS, insbesondere alle Tests aus `backup_test.go` mit `-race`.

- [ ] **Step 11: Commit**

```bash
git add internal/backups
git commit -m "feat(backups): job queue, consistent backups and crash recovery

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 11: Ticker, Retention, Zeitzonenwechsel und Benachrichtigung

**Files:**
- Create: `internal/backups/retention.go`, `internal/backups/notify.go`, `internal/backups/ticker_test.go`
- Modify: `internal/backups/ticker.go` (`tick` vollständig, `checkTimezone`, `enqueueDue`), `internal/backups/handler.go` (Felder `alerts`, `lastTZ`)

**Interfaces:**
- Consumes: Aufgabe 9 (`NextRun`, `scheduleOf`, `location`, `loadSchedule`), Aufgabe 10 (`queue.add`, `runBackup`, `waitService`, `jobErrorOf`), V1 (`ForgetBackups`, `ForgetRequest`), V2 (`BackupsListDue`, `BackupsListEnabled`, `BackupsSetNextRun`, `BackupsStartRun`, `BackupsFinishRun`), `notify.EventBackupFailed`.
- Produces: `runScheduled`, `prune`, `finishScheduled`, `enqueueDue`, `checkTimezone`, `alerts`, `alert(ctx, serverID, name, what, msg)`, `alertOK(serverID, what)`, Konstanten `alertBackup = "backup"`, `alertRestore = "restore"`.

- [ ] **Step 1: Tests schreiben** – `internal/backups/ticker_test.go`

```go
package backups_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/notify"
)

func (e *env) tick() {
	e.t.Helper()
	backups.Tick(e.t.Context(), e.h)
	if !backups.WaitIdle(e.h) {
		e.t.Fatal("jobs did not finish")
	}
}

func TestTickRunsDueSchedule(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	e.tick()
	if n := e.files.count("create"); n != 0 {
		t.Fatalf("ran before it was due: %v", e.files.callLog())
	}
	due := time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC)
	e.clk.Set(due)
	e.tick()
	calls := e.files.callLog()
	if !slices.Equal(calls, []string{fmt.Sprintf("create %d scheduled", id), fmt.Sprintf("forget %d policy 24/7/4/6 Europe/Berlin", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	row := e.schedule(id)
	snaps := e.files.list(id)
	if row.LastStatus.String != "ok" || row.LastError.Valid || row.LastRunAt.Int64 != due.UnixMilli() ||
		len(snaps) != 1 || row.LastSnapshotID.String != snaps[0].ID || row.NextRunAt.Int64 != due.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("row = %+v, snaps %+v", row, snaps)
	}
	if j := e.job(id); j.Trigger != "scheduled" || j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	if es, _ := e.rec.all(); len(es) != 1 { // only the schedule update; scheduled runs are no user action
		t.Fatalf("audit = %v", e.rec.actions())
	}
	if tz := e.files.createTZ[id]; tz != "Europe/Berlin" {
		t.Fatalf("backup timezone = %q", tz)
	}
}

// Backup and retention use the same zone, also when the stored one is invalid (both UTC):
// forget buckets by the zone the snapshot times were stored in.
func TestBackupAndRetentionShareZone(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	if _, err := e.db.ExecContext(t.Context(), `UPDATE instance_settings SET backup_timezone = 'Mars/Olympus' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(time.Date(2026, 10, 13, 4, 0, 0, 0, time.UTC))
	e.tick()
	calls := e.files.callLog()
	if tz := e.files.createTZ[id]; tz != "UTC" || len(calls) != 2 || !strings.HasSuffix(calls[1], " UTC") {
		t.Fatalf("backup timezone %q, calls %v", tz, calls)
	}
}

func TestRetentionOnlyAfterScheduledSuccess(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.create(id, map[string]any{})
	e.waitJob(id)
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.fail["scheduled"] = "boom"
	e.clk.Advance(time.Hour)
	e.tick()
	if n := e.files.count("forget"); n != 0 {
		t.Fatalf("forget after a manual backup or a failed run: %v", e.files.callLog())
	}
}

func TestRetentionFailureKeepsRunOK(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.fail["forget"] = "repository is locked"
	e.clk.Advance(time.Hour)
	e.tick()
	row := e.schedule(id)
	if row.LastStatus.String != "ok" || row.LastError.String != "retention: repository is locked" {
		t.Fatalf("row = %+v", row)
	}
	if len(e.notes.Types()) != 0 {
		t.Fatalf("a retention failure alone notified: %v", e.notes.Types())
	}
}

func TestTickSkipsBusyServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	release := e.files.holdPlan("manual")
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	e.clk.Advance(time.Hour)
	backups.Tick(t.Context(), e.h)
	if j := e.job(id); j.Trigger != "manual" {
		t.Fatalf("the scheduled run replaced the manual job: %+v", j)
	}
	release()
	e.waitJob(id)
	e.tick()
	if n := e.files.count(fmt.Sprintf("create %d scheduled", id)); n != 1 {
		t.Fatalf("calls = %v, want the scheduled run on the next tick", e.files.callLog())
	}
}

func TestMissedRunRunsOnce(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	mustExec(t, e.db, `UPDATE backup_schedules SET next_run_at = ? WHERE server_id = ?`, t0.Add(-72*time.Hour).UnixMilli(), id)
	e.tick()
	e.tick()
	if n := e.files.count("create"); n != 1 {
		t.Fatalf("calls = %v, want exactly one catch-up run", e.files.callLog())
	}
	if want := time.Date(2026, 10, 13, 2, 0, 0, 0, time.UTC).UnixMilli(); e.schedule(id).NextRunAt.Int64 != want {
		t.Fatalf("next = %d, want %d", e.schedule(id).NextRunAt.Int64, want)
	}
}

func TestTimezoneChangeRecomputes(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("daily", 24, 240, 0))
	e.tick() // remembers Europe/Berlin
	mustExec(t, e.db, `UPDATE instance_settings SET backup_timezone = 'America/New_York' WHERE id = 1`)
	e.tick()
	// t0 is 06:00 EDT: 04:00 EDT has passed, next is tomorrow 04:00 EDT = 08:00Z.
	if want := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC).UnixMilli(); e.schedule(id).NextRunAt.Int64 != want {
		t.Fatalf("next = %d, want %d", e.schedule(id).NextRunAt.Int64, want)
	}
	if s := putSchedule(e, id, scheduleBody("daily", 24, 240, 0)); s.Timezone != "America/New_York" {
		t.Fatalf("timezone = %q", s.Timezone)
	}
}

func TestScheduledFailureNotifiesThrottled(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.fail["scheduled"] = "restic: repository damaged"
	run := func() {
		e.clk.Advance(time.Hour)
		e.tick()
	}
	run() // T+1h: notifies
	run() // T+2h: within 6 h, silent
	if got := e.notes.Types(); len(got) != 1 || got[0] != notify.EventBackupFailed {
		t.Fatalf("events = %v", got)
	}
	ev := e.notes.Events[0]
	if !strings.Contains(ev.Title, "survival") || ev.Message != "restic: repository damaged" {
		t.Fatalf("event = %+v", ev)
	}
	e.clk.Advance(4 * time.Hour) // T+6h since the first alert
	run()
	if n := len(e.notes.Types()); n != 2 {
		t.Fatalf("events after 6 h = %d", n)
	}
	delete(e.files.fail, "scheduled")
	run() // success resets the throttle
	e.files.fail["scheduled"] = "boom"
	run()
	if n := len(e.notes.Types()); n != 3 {
		t.Fatalf("a failure after a success must notify at once, events = %d", n)
	}
	if row := e.schedule(id); row.LastStatus.String != "failed" || row.LastError.String != "boom" {
		t.Fatalf("row = %+v", row)
	}
}

func TestNoNotificationWithoutDataDir(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	putSchedule(e, id, scheduleBody("interval", 1, 240, 0))
	e.files.errs["create"] = filesapi.ErrNotFound // never started: no data directory
	e.clk.Advance(time.Hour)
	e.tick()
	if row := e.schedule(id); row.LastStatus.String != "failed" {
		t.Fatalf("row = %+v", row)
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("events = %v", e.notes.Types())
	}
}
```

`notify.Recording.Events` ist ein exportiertes Feld; die Tests lesen es erst nach `WaitIdle` (kein Wettlauf mit dem Job).

- [ ] **Step 2: Rot prüfen**

Run: `make check && make test-vm SUITE=go`
Expected: kompiliert, `TestTickRunsDueSchedule`, `TestMissedRunRunsOnce`, `TestTimezoneChangeRecomputes`, `TestScheduledFailureNotifiesThrottled` … FAIL (der Ticker startet noch keine Zeitpläne).

- [ ] **Step 3: `internal/backups/notify.go`**

```go
package backups

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const (
	alertEvery   = 6 * time.Hour // spec §5.9: per server and kind at most once in 6 h
	alertBackup  = "backup"
	alertRestore = "restore"
)

// alerts remembers when a backup.failed notification went out per server and kind.
type alerts struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newAlerts() *alerts { return &alerts{last: map[string]time.Time{}} }

func alertKey(serverID int64, what string) string { return fmt.Sprintf("%d/%s", serverID, what) }

// alert sends backup.failed unless one went out for the server and kind within alertEvery.
func (h *Handler) alert(ctx context.Context, serverID int64, name, what, msg string) {
	now := h.clock.Now()
	key := alertKey(serverID, what)
	h.alerts.mu.Lock()
	if last, ok := h.alerts.last[key]; ok && now.Sub(last) < alertEvery {
		h.alerts.mu.Unlock()
		return
	}
	h.alerts.last[key] = now
	h.alerts.mu.Unlock()
	title := "Backup failed: " + name
	if what == alertRestore {
		title = "Restore failed: " + name
	}
	p, _ := principal.From(ctx) // a restore keeps the user who started it; scheduled runs have none
	h.notifier.Notify(context.WithoutCancel(ctx), notify.Event{
		Type: notify.EventBackupFailed, Title: title, Message: msg, Username: p.Username, Time: now,
	})
}

// alertOK resets the throttle after a success.
func (h *Handler) alertOK(serverID int64, what string) {
	h.alerts.mu.Lock()
	defer h.alerts.mu.Unlock()
	delete(h.alerts.last, alertKey(serverID, what))
}
```

- [ ] **Step 4: `internal/backups/retention.go`**

```go
package backups

import (
	"context"
	"errors"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// runScheduled is a scheduled run (spec §5.4): backup, retention on success, last_* and
// next_run_at, backup.failed on failure.
func (h *Handler) runScheduled(ctx context.Context, j *job) (string, error) {
	id := j.spec.serverID
	started := h.clock.Now()
	if err := h.q.BackupsStartRun(ctx, store.BackupsStartRunParams{LastRunAt: nullTime(started), ServerID: id}); err != nil {
		return "", err
	}
	snap, err := h.runBackup(ctx, j, filesapi.SnapshotScheduled, "")
	hint := ""
	if err == nil {
		if perr := h.prune(ctx, j); perr != nil {
			_, msg := jobErrorOf(perr)
			hint = "retention: " + msg
			h.log.WarnContext(ctx, "backups: retention failed", "server_id", id, "err", perr)
		}
	}
	h.finishScheduled(ctx, j, started, snap, err, hint)
	return snap, err
}

// prune applies the server's retention to its scheduled snapshots (forget --keep-*, pinned
// ones stay; manual and pre-restore snapshots are never touched).
func (h *Handler) prune(ctx context.Context, j *job) error {
	id := j.spec.serverID
	row, err := h.q.BackupsGetSchedule(ctx, id)
	if err != nil {
		return err
	}
	p := scheduleOf(row).Policy()
	if p.Validate() != nil { // switched off meanwhile without retention: nothing to apply
		return nil
	}
	j.setStep(stepPruning)
	// the same zone as the backups (tzName), so the buckets match the stored snapshot times
	sj, err := h.files.ForgetBackups(ctx, filesapi.ForgetRequest{ID: id, Policy: &p, Timezone: h.tzName(ctx)})
	if err != nil {
		return err
	}
	_, err = h.waitService(ctx, j, id, sj.ID)
	return err
}

// finishScheduled writes last_* and the next run, and notifies a failure (not for a cancel or
// a server without data directory). A full success leaves last_error NULL; a failed forget
// alone keeps last_status ok with last_error "retention: <message>".
func (h *Handler) finishScheduled(ctx context.Context, j *job, started time.Time, snap string, runErr error, hint string) {
	id := j.spec.serverID
	ctx = context.WithoutCancel(ctx) // also after a cancel the row must be written
	row, err := h.q.BackupsGetSchedule(ctx, id)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: finish run", "server_id", id, "err", err)
		return
	}
	loc, _, err := h.location(ctx)
	if err != nil {
		loc = time.UTC
	}
	var next sql.NullInt64
	if t, ok := NextRun(scheduleOf(row), loc, h.clock.Now(), started); ok {
		next = nullTime(t)
	}
	status, msg := "ok", hint
	if runErr != nil {
		status = "failed"
		_, msg = jobErrorOf(runErr)
	}
	if err := h.q.BackupsFinishRun(ctx, store.BackupsFinishRunParams{
		LastStatus: nullString(status), LastError: nullString(msg), LastSnapshotID: nullString(snap), NextRunAt: next, ServerID: id,
	}); err != nil {
		h.log.ErrorContext(ctx, "backups: finish run", "server_id", id, "err", err)
	}
	switch {
	case runErr == nil:
		h.alertOK(id, alertBackup)
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, filesapi.ErrNotFound):
		// shutdown, a deleted server or a server that never ran: nothing to report
	default:
		h.alert(ctx, id, j.spec.name, alertBackup, msg)
	}
}
```

(Imports in `retention.go`: `context`, `database/sql`, `errors`, `time`, `filesapi`, `store`.)

- [ ] **Step 5: `ticker.go` vervollständigen**

`tick` ersetzen und zwei Funktionen ergänzen:

```go
// tick runs once per tickEvery (and once when Run starts): pending save-on, a changed time
// zone, then the due schedules.
func (h *Handler) tick(ctx context.Context) {
	h.tickMu.Lock()
	defer h.tickMu.Unlock()
	now := h.clock.Now()
	h.retrySaveOn(ctx)
	h.checkTimezone(ctx, now)
	h.enqueueDue(ctx, now)
}

// checkTimezone recomputes next_run_at of every active schedule when the panel time zone
// changed since the last tick (deviation 5: the first tick only remembers it).
func (h *Handler) checkTimezone(ctx context.Context, now time.Time) {
	loc, name, err := h.location(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: read the time zone", "err", err)
		return
	}
	if h.lastTZ == "" || h.lastTZ == name {
		h.lastTZ = name
		return
	}
	h.lastTZ = name
	rows, err := h.q.BackupsListEnabled(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list schedules", "err", err)
		return
	}
	for _, r := range rows {
		var next sql.NullInt64
		if t, ok := NextRun(scheduleOf(r), loc, now, timeOf(r.LastRunAt)); ok {
			next = nullTime(t)
		}
		if err := h.q.BackupsSetNextRun(ctx, store.BackupsSetNextRunParams{NextRunAt: next, ServerID: r.ServerID}); err != nil {
			h.log.ErrorContext(ctx, "backups: set next run", "server_id", r.ServerID, "err", err)
		}
	}
}

// enqueueDue queues a scheduled run for every due schedule; a server with a queued or running
// job is skipped and checked again on the next tick.
func (h *Handler) enqueueDue(ctx context.Context, now time.Time) {
	rows, err := h.q.BackupsListDue(ctx, nullTime(now))
	if err != nil {
		h.log.ErrorContext(ctx, "backups: list due schedules", "err", err)
		return
	}
	for _, r := range rows {
		s, err := h.q.RbacGetServer(ctx, r.ServerID)
		if err != nil {
			continue // deleted meanwhile (the row goes with it)
		}
		_, err = h.queue.add(context.Background(), jobSpec{
			serverID: r.ServerID, name: s.Name, kind: kindBackup, trigger: triggerScheduled, run: h.runScheduled,
		})
		if errors.Is(err, errShuttingDown) {
			return
		}
	}
}
```

(Imports: `context`, `database/sql`, `errors`, `time`, `store`.)

- [ ] **Step 6: `handler.go` ergänzen**

Im `Handler`:

```go
	alerts *alerts
	lastTZ string // panel time zone of the previous tick ("" before the first; guarded by tickMu)
```

In `NewHandler`: `alerts: newAlerts(),`.

- [ ] **Step 7: Grün prüfen**

Run: `gofmt -w internal/backups && make check && make test-vm SUITE=go`
Expected: PASS (alle Tests aus `ticker_test.go`, dazu weiterhin `backup_test.go`).

- [ ] **Step 8: Commit**

```bash
git add internal/backups
git commit -m "feat(backups): scheduled runs with retention, time zone changes and failure alerts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 12: Restore mit Sperre, Löschen, Pin, Purge und Übersicht

**Files:**
- Create: `internal/backups/restore.go`, `internal/backups/manage.go`, `internal/backups/overview.go`, `internal/backups/restore_test.go`, `internal/backups/manage_test.go`
- Modify: `internal/backups/handler.go` (Routen; `DataJobRunning` und `PurgeBackups` entfernen – sie stehen jetzt in `restore.go`/`manage.go`), `internal/backups/routes_test.go` (`stubbed` enthält nur noch `download`)

**Interfaces:**
- Consumes: Aufgaben 9–11 (`queue`, `runBackup`-Bausteine `waitService`, `instanceState`, `isRunning`, `ready`, `alert`, `alertOK`, `record`), V1 (`Snapshots`, `RestoreBackup`, `ForgetBackups`, `PinBackup`, `PurgeBackups`), `agentapi.PowerStop`.
- Produces: `DataJobRunning`, `PurgeBackups`, `runRestore`, `stop`, `findSnapshot`, `snapshotView`, `viewSnapshot`, Handler `handleOverview`, `handleRestore`, `handleDelete`, `handlePin`.

- [ ] **Step 1: Tests schreiben** – `internal/backups/restore_test.go`

```go
package backups_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/notify"
)

func (e *env) restore(id int64, snap string) jobJSON {
	e.t.Helper()
	rec := e.must(http.StatusAccepted, http.MethodPost, bPath(id, "/"+snap+"/restore"), nil, e.sudoAdmin())
	return *decode[jobResp](e.t, rec).Job
}

func TestRestoreRunningServer(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	j := e.restore(id, snap)
	if j.Kind != "restore" || j.Snapshot == nil || *j.Snapshot != snap {
		t.Fatalf("queued = %+v", j)
	}
	if done := e.waitJob(id); done.State != "done" {
		t.Fatalf("job = %+v", done)
	}
	if got := e.agent.powerLog(); !slices.Equal(got, []string{fmt.Sprintf("stop %d", id)}) {
		t.Fatalf("powers = %v", got)
	}
	want := []string{fmt.Sprintf("create %d pre-restore", id), fmt.Sprintf("restore %d %s", id, snap[:8])}
	if got := e.files.callLog(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	if len(e.console.sent()) != 0 {
		t.Fatalf("restore sent RCON: %v", e.console.sent())
	}
	eventually(t, "reconcile kick", func() bool { return e.kicks.Load() == 1 })
	if e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate still closed after the restore")
	}
	entries, actors := e.rec.all()
	if len(entries) != 1 || entries[0].Action != audit.ActionBackupRestore || entries[0].Result != audit.ResultSuccess ||
		actors[0] != "root" || entries[0].Details["snapshot"] != snap {
		t.Fatalf("audit = %+v", entries)
	}
}

func TestGateHoldsDuringRestore(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("restore")
	e.restore(id, snap)
	e.waitStep(id, "restoring")
	if !e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate open while restoring")
	}
	other := e.instance("other")
	if e.h.DataJobRunning(t.Context(), other) {
		t.Fatal("gate closed for another server")
	}
	release()
	e.waitJob(id)
	if e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate still closed")
	}
}

func TestGateHoldsWhileStopping(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	e.agent.stuck = true
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.restore(id, snap)
	e.waitStep(id, "stopping")
	if !e.h.DataJobRunning(t.Context(), id) {
		t.Fatal("gate open while stopping")
	}
	j := e.waitJob(id)
	if code(j) != "stop_timeout" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count("create") + e.files.count("restore"); n != 0 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if got := e.notes.Types(); len(got) != 1 || got[0] != notify.EventBackupFailed {
		t.Fatalf("events = %v", got)
	}
	if ev := e.notes.Events[0]; ev.Username != "root" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestRestorePreRestoreFails(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.fail["pre-restore"] = "restic: no space left"
	e.restore(id, snap)
	if j := e.waitJob(id); j.State != "failed" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count("restore"); n != 0 {
		t.Fatalf("restored without a safety snapshot: %v", e.files.callLog())
	}
}

func TestRestoreInsufficientStorage(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.errs["restore"] = filesapi.ErrInsufficientStorage
	e.restore(id, snap)
	if j := e.waitJob(id); code(j) != "insufficient_storage" {
		t.Fatalf("job = %+v", j)
	}
}

func TestRestoreRequests(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/restore"), nil, &a); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("without sudo = %d %s", rec.Code, rec.Body)
	}
	s := e.sudoAdmin()
	if rec := e.do(http.MethodPost, bPath(id, "/XYZ/restore"), nil, &s); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", rec.Code)
	}
	unknown := fmt.Sprintf("%064x", 999)
	if rec := e.do(http.MethodPost, bPath(id, "/"+unknown+"/restore"), nil, &s); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown snapshot = %d", rec.Code)
	}
	if len(e.rec.actions()) != 0 || e.files.count("create") != 0 {
		t.Fatalf("rejections had effects: %v %v", e.rec.actions(), e.files.callLog())
	}
}

// Review Focus 4: the gate closes with the 202 although the restore still waits behind another
// server's backup, so a power start right after the restore request is refused.
func TestGateHoldsWhileRestoreQueued(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	snap := e.files.add(b, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("manual")
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	j := e.restore(b, snap)
	if j.State != "queued" || j.QueuePosition != 1 {
		t.Fatalf("restore = %+v", j)
	}
	if !e.h.DataJobRunning(t.Context(), b) {
		t.Fatal("gate open while the restore waits")
	}
	if e.h.DataJobRunning(t.Context(), a) {
		t.Fatal("gate closed for the server that only backs up")
	}
	release()
	e.waitJob(b)
	if e.h.DataJobRunning(t.Context(), b) {
		t.Fatal("gate still closed after the restore")
	}
}

// Review Focus 4: a restore while a backup of the same server waits or runs.
func TestRestoreWhileBackupQueued(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	snap := e.files.add(b, filesapi.SnapshotManual, false, "")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	e.create(b, map[string]any{}) // queued behind alpha
	s := e.sudoAdmin()
	if rec := e.do(http.MethodPost, bPath(b, "/"+snap+"/restore"), nil, &s); rec.Code != http.StatusConflict || errCode(t, rec) != "backup_running" {
		t.Fatalf("restore while queued = %d %s", rec.Code, rec.Body)
	}
}

func TestRestoreWithoutAgent(t *testing.T) {
	e := newEnvNoRun(t, func(d *backups.Deps) { d.Instances = nil })
	e.run()
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.restore(id, snap)
	if j := e.waitJob(id); code(j) != "agent_unavailable" {
		t.Fatalf("job = %+v", j)
	}
}
```

- [ ] **Step 2: Tests schreiben** – `internal/backups/manage_test.go`

```go
package backups_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type snapshotJSON struct {
	ID      string  `json:"id"`
	ShortID string  `json:"short_id"`
	Time    int64   `json:"time"`
	Kind    string  `json:"kind"`
	Pinned  bool    `json:"pinned"`
	Note    *string `json:"note"`
	Bytes   int64   `json:"bytes"`
}

type overviewJSON struct {
	Available bool           `json:"available"`
	Snapshots []snapshotJSON `json:"snapshots"`
	Job       *jobJSON       `json:"job"`
	Schedule  scheduleJSON   `json:"schedule"`
}

func (e *env) overview(id int64) overviewJSON {
	e.t.Helper()
	return decode[overviewJSON](e.t, e.must(http.StatusOK, http.MethodGet, bPath(id, ""), nil, e.admin()))
}

func TestOverview(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	old := e.files.add(id, filesapi.SnapshotScheduled, true, "")
	e.clk.Advance(time.Minute)
	manual := e.files.add(id, filesapi.SnapshotManual, false, "vor Update")
	o := e.overview(id)
	if !o.Available || o.Job != nil || len(o.Snapshots) != 2 {
		t.Fatalf("overview = %+v", o)
	}
	if s := o.Snapshots[0]; s.ID != manual || s.Kind != "manual" || s.Note == nil || *s.Note != "vor Update" || s.ShortID != manual[:8] || s.Bytes != 4096 {
		t.Fatalf("first = %+v", s)
	}
	if s := o.Snapshots[1]; s.ID != old || !s.Pinned || s.Note != nil {
		t.Fatalf("second = %+v", s)
	}
	if o.Schedule.Mode != "off" || o.Schedule.KeepLast != 24 || o.Schedule.Timezone != "Europe/Berlin" || o.Schedule.NextRunAt != nil {
		t.Fatalf("schedule = %+v", o.Schedule)
	}
}

func TestOverviewUnavailable(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.status = filesapi.BackupsStatus{Available: false, Reason: "restic not found"}
	if o := e.overview(id); o.Available || len(o.Snapshots) != 0 {
		t.Fatalf("no restic = %+v", o)
	}
	e.files.status = filesapi.BackupsStatus{Available: true}
	e.files.errs["list"] = filesapi.ErrUnavailable
	if o := e.overview(id); o.Available || o.Snapshots == nil {
		t.Fatalf("list error = %+v (snapshots must be [] not null)", o)
	}
	e2 := newEnvNoRun(t, func(d *backups.Deps) { d.Files = nil })
	e2.run()
	id2 := e2.instance("survival")
	if o := e2.overview(id2); o.Available {
		t.Fatalf("no service = %+v", o)
	}
	lobby := storeServer(t, e, "lobby")
	delete(e.files.errs, "list")
	if o := e.overview(lobby); !o.Available || len(o.Snapshots) != 0 {
		t.Fatalf("server without instance = %+v", o)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotPreRestore, false, "")
	a := e.admin()
	if rec := e.do(http.MethodDelete, bPath(id, "/"+snap), nil, &a); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("without sudo = %d", rec.Code)
	}
	rec := e.must(http.StatusAccepted, http.MethodDelete, bPath(id, "/"+snap), nil, e.sudoAdmin())
	if j := decode[jobResp](t, rec).Job; j.Kind != "delete" {
		t.Fatalf("job = %+v", j)
	}
	if j := e.waitJob(id); j.State != "done" {
		t.Fatalf("job = %+v", j)
	}
	if len(e.files.list(id)) != 0 || e.files.count(fmt.Sprintf("forget %d ids %s", id, snap[:8])) != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
	if es, _ := e.rec.all(); len(es) != 1 || es[0].Action != audit.ActionBackupDelete || es[0].Details["snapshot"] != snap {
		t.Fatalf("audit = %+v", es)
	}
	if rec := e.do(http.MethodDelete, bPath(id, "/"+snap), nil, ptr(e.sudoAdmin())); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d", rec.Code)
	}
}

func TestPin(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	rec := e.must(http.StatusOK, http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, e.admin())
	pinned := decode[struct {
		Snapshot snapshotJSON `json:"snapshot"`
	}](t, rec).Snapshot
	if !pinned.Pinned || pinned.ID == snap {
		t.Fatalf("pinned = %+v, want a new id", pinned)
	}
	e.must(http.StatusOK, http.MethodPost, bPath(id, "/"+pinned.ID+"/pin"), map[string]any{"pinned": false}, e.admin())
	if got := e.rec.actions(); len(got) != 2 || got[0] != audit.ActionBackupPin+":success" || got[1] != audit.ActionBackupUnpin+":success" {
		t.Fatalf("audit = %v", got)
	}
	manual := e.files.add(id, filesapi.SnapshotManual, false, "")
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+manual+"/pin"), map[string]any{"pinned": true}, &a); rec.Code != http.StatusBadRequest {
		t.Fatalf("pin manual = %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{}, &a); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing pinned = %d", rec.Code)
	}
}

func TestPinTwiceOldIDGone(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	e.must(http.StatusOK, http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, e.admin())
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, &a); rec.Code != http.StatusNotFound {
		t.Fatalf("second tab with the old id = %d %s", rec.Code, rec.Body)
	}
}

func TestPinWhileJobRunning(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	a := e.admin()
	if rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, &a); rec.Code != http.StatusConflict || errCode(t, rec) != "backup_running" {
		t.Fatalf("pin during a job = %d %s", rec.Code, rec.Body)
	}
}

// The service refuses a pin with 429 busy while a download or extract runs: still 409
// backup_running, but the message names the file service, not a backup job.
func TestPinWhileServiceBusy(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotScheduled, false, "")
	e.files.errs["pin"] = filesapi.ErrBusy
	a := e.admin()
	rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/pin"), map[string]any{"pinned": true}, &a)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "backup_running" ||
		!strings.Contains(rec.Body.String(), "file service is busy") {
		t.Fatalf("pin while the service is busy = %d %s", rec.Code, rec.Body)
	}
}

func TestPurgeDropsQueuedJob(t *testing.T) {
	e := newEnv(t)
	a := e.instance("alpha")
	b := e.instance("beta")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(a, map[string]any{})
	e.waitStep(a, "backing_up")
	e.create(b, map[string]any{})
	if err := e.h.PurgeBackups(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if j := e.job(b); j.State != "failed" || code(*j) != "canceled" {
		t.Fatalf("beta job = %+v", j)
	}
	release()
	backups.WaitIdle(e.h)
	if e.files.count(fmt.Sprintf("create %d", b)) != 0 || e.files.count(fmt.Sprintf("purge %d", b)) != 1 {
		t.Fatalf("calls = %v", e.files.callLog())
	}
}

func TestPurgeCancelsRunningJob(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	release := e.files.holdPlan("manual")
	defer release()
	e.create(id, map[string]any{})
	e.waitStep(id, "backing_up")
	if err := e.h.PurgeBackups(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	calls := e.files.callLog()
	if e.files.count(fmt.Sprintf("cancel %d", id)) != 1 || calls[len(calls)-1] != fmt.Sprintf("purge %d", id) {
		t.Fatalf("calls = %v", calls)
	}
}

func ptr[T any](v T) *T { return &v }
```

(`manage_test.go` braucht zusätzlich die Importe `strings` und `time`.)

- [ ] **Step 3: Rot prüfen**

Run: `git add -N internal/backups && make check`
Expected: kompiliert; auf der VM FAIL – die Routen antworten noch `503` aus `stubs.go`, `DataJobRunning` ist immer `false`.

- [ ] **Step 4: `internal/backups/overview.go`**

```go
package backups

import (
	"context"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// snapshotView is the OpenAPI schema BackupSnapshot.
type snapshotView struct {
	ID      string  `json:"id"`
	ShortID string  `json:"short_id"`
	Time    int64   `json:"time"`
	Kind    string  `json:"kind"`
	Pinned  bool    `json:"pinned"`
	Note    *string `json:"note"`
	Bytes   int64   `json:"bytes"`
}

func viewSnapshot(s filesapi.Snapshot) snapshotView {
	v := snapshotView{ID: s.ID, ShortID: s.ShortID, Time: s.Time, Kind: string(s.Kind), Pinned: s.Pinned, Bytes: s.Bytes}
	if s.Note != "" {
		n := s.Note
		v.Note = &n
	}
	return v
}

type overviewResponse struct {
	Available bool           `json:"available"`
	Snapshots []snapshotView `json:"snapshots"`
	Job       *jobView       `json:"job"`
	Schedule  scheduleView   `json:"schedule"`
}

type snapshotResponse struct {
	Snapshot snapshotView `json:"snapshot"`
}

// findSnapshot looks id up in the server's repository.
func (h *Handler) findSnapshot(ctx context.Context, serverID int64, id string) (filesapi.Snapshot, error) {
	list, err := h.files.Snapshots(ctx, serverID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
	}
	return filesapi.Snapshot{}, errSnapshotNotFound
}

// handleOverview never fails for a missing service or restic: the tab shows its hint (V3).
func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	row, err := h.loadSchedule(ctx, srv.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	_, tz, err := h.location(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := overviewResponse{Snapshots: []snapshotView{}, Job: h.queue.get(srv.ID), Schedule: viewSchedule(row, tz)}
	if h.ready(ctx, srv, false) == nil {
		list, err := h.files.Snapshots(ctx, srv.ID)
		if err != nil {
			h.log.WarnContext(ctx, "backups: list snapshots", "server_id", srv.ID, "err", err)
		} else {
			out.Available = true
			for _, s := range list {
				out.Snapshots = append(out.Snapshots, viewSnapshot(s))
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
```

- [ ] **Step 5: `internal/backups/restore.go`**

```go
package backups

import (
	"context"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// DataJobRunning is true while the panel holds a restore job for the server, from the 202 (also
// while it waits behind another server's job) to the end of the service's restore
// (servers.DataGate; spec §5.5). The service's own restore job is covered by
// serverfiles.DataJobRunning, also across a panel restart.
func (h *Handler) DataJobRunning(_ context.Context, serverID int64) bool {
	return h.queue.restoreHeld(serverID)
}

// stop stops a running server through the agent (desired_state stays; the gate keeps the
// reconcile loop from starting it) and waits until it no longer runs.
func (h *Handler) stop(ctx context.Context, id int64) error {
	if h.inst == nil {
		return failf(codeAgent, "no agent: the server cannot be stopped")
	}
	st, err := h.instanceState(ctx, id)
	if err != nil {
		return failf(codeAgent, "the agent is not reachable, the server state is unknown")
	}
	if !isRunning(st) {
		return nil
	}
	if err := h.inst.Power(ctx, id, agentapi.PowerStop); err != nil {
		return failf(codeAgent, "stopping the server failed: %v", err)
	}
	sctx, cancel := context.WithTimeout(ctx, h.stopTimeout)
	defer cancel()
	for {
		select {
		case <-sctx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return failf(codeStopTimeout, "the server did not stop within %s", h.stopTimeout)
		case <-time.After(h.pollEvery):
		}
		if st, err := h.instanceState(sctx, id); err == nil && !isRunning(st) {
			return nil
		}
	}
}

// runRestore is the restore flow of spec §5.5; the queue holds the gate while it runs.
func (h *Handler) runRestore(ctx context.Context, j *job, snapshot string) error {
	id := j.spec.serverID
	j.setStep(stepStopping)
	if err := h.stop(ctx, id); err != nil {
		return err
	}
	j.setStep(stepPreRestore)
	pre, err := h.files.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: id, Kind: filesapi.SnapshotPreRestore, Timezone: h.tzName(ctx)})
	if err != nil {
		return err
	}
	if _, err := h.waitService(ctx, j, id, pre.ID); err != nil {
		return err
	}
	j.setStep(stepRestoring)
	rj, err := h.files.RestoreBackup(ctx, id, snapshot)
	if err != nil {
		return err
	}
	_, err = h.waitService(ctx, j, id, rj.ID)
	return err
}

func (h *Handler) handleRestore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	if err := h.ready(ctx, srv, true); err != nil {
		h.writeError(w, r, err)
		return
	}
	if h.queue.busy(srv.ID) {
		h.writeError(w, r, errBackupRunning)
		return
	}
	s, err := h.findSnapshot(ctx, srv.ID, snap)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	v, err := h.queue.add(ctx, jobSpec{
		serverID: srv.ID, name: srv.Name, kind: kindRestore, trigger: triggerManual, snapshot: snap,
		after: h.reconcile, // the gate is open again: start the server if it should run
		run: func(ctx context.Context, j *job) (string, error) {
			err := h.runRestore(ctx, j, snap)
			h.record(ctx, audit.ActionBackupRestore, srv.ID, result(err), map[string]any{"snapshot": snap, "time": s.Time, "kind": string(s.Kind)})
			switch {
			case err == nil:
				h.alertOK(srv.ID, alertRestore)
			case ctx.Err() == nil:
				_, msg := jobErrorOf(err)
				h.alert(ctx, srv.ID, srv.Name, alertRestore, msg)
			}
			return snap, err
		},
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, jobResponse{Job: &v})
}
```

- [ ] **Step 6: `internal/backups/manage.go`**

```go
package backups

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

const purgeWait = 20 * time.Second

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	if err := h.ready(ctx, srv, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	s, err := h.findSnapshot(ctx, srv.ID, snap)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	v, err := h.queue.add(ctx, jobSpec{
		serverID: srv.ID, name: srv.Name, kind: kindDelete, trigger: triggerManual, snapshot: snap,
		run: func(ctx context.Context, j *job) (string, error) {
			j.setStep(stepDeleting)
			sj, err := h.files.ForgetBackups(ctx, filesapi.ForgetRequest{ID: srv.ID, Snapshots: []string{snap}})
			if err == nil {
				_, err = h.waitService(ctx, j, srv.ID, sj.ID)
			}
			h.record(ctx, audit.ActionBackupDelete, srv.ID, result(err), map[string]any{"snapshot": snap, "kind": string(s.Kind)})
			return snap, err
		},
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, jobResponse{Job: &v})
}

// pinRequest is the OpenAPI schema PinBackupRequest.
type pinRequest struct {
	Pinned *bool `json:"pinned"`
}

func (h *Handler) handlePin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	var req pinRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Pinned == nil {
		writeValidation(w, r, "pinned is required")
		return
	}
	if err := h.ready(ctx, srv, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	if h.queue.busy(srv.ID) {
		h.writeError(w, r, errBackupRunning)
		return
	}
	s, err := h.files.PinBackup(ctx, srv.ID, snap, *req.Pinned)
	action := audit.ActionBackupPin
	if !*req.Pinned {
		action = audit.ActionBackupUnpin
	}
	if err != nil {
		if !rejected(err) {
			h.record(ctx, action, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
		}
		h.writeError(w, r, err)
		return
	}
	h.record(ctx, action, srv.ID, audit.ResultSuccess, map[string]any{"snapshot": snap, "new_snapshot": s.ID})
	httpx.WriteJSON(w, http.StatusOK, snapshotResponse{Snapshot: viewSnapshot(s)})
}

// PurgeBackups ends the server's jobs (a waiting one is dropped, a running one cancelled and
// awaited) and deletes its repository (servers.BackupPurger, spec §5.6). The service may report
// busy for a moment after a cancel; the purge is retried a few times. The waiting before the
// service call is bounded (purgeWait + 5 × pollEvery ≈ 30 s): servers' DELETE gives the whole
// purge filesapi.DeleteTimeout + 1 min (context and write deadline, V2).
func (h *Handler) PurgeBackups(ctx context.Context, serverID int64) error {
	h.queue.drop(serverID, purgeWait)
	if h.files == nil {
		return nil
	}
	var err error
	for range 5 {
		if err = h.files.PurgeBackups(ctx, serverID); !errors.Is(err, filesapi.ErrBusy) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(h.pollEvery):
		}
	}
	return err
}
```

- [ ] **Step 7: `handler.go` anpassen**

Die Stub-Methoden `DataJobRunning` und `PurgeBackups` aus `handler.go` löschen. Übersicht, Pin, Restore und Löschen rufen den Dienst synchron mit `filesapi.BackupCallTimeout` (2 min; restic liest den Repo-Index) auf – länger als die `WriteTimeout` des panels (60 s). Sie bekommen deshalb eine eigene Schreibfrist (Helfer in `handler.go`, `ignoreUnsupported` nutzt Aufgabe 13 mit):

```go
// slowWriteTimeout is the write deadline of the routes that wait for a synchronous restic call
// (overview, pin, restore and delete pre-checks): the client timeout plus a margin.
const slowWriteTimeout = filesapi.BackupCallTimeout + 30*time.Second

// slow extends the write deadline of a route beyond the server's WriteTimeout (pattern of the
// serverfiles downloads); recorders without deadline support are fine.
func (h *Handler) slow(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ignoreUnsupported(http.NewResponseController(w).SetWriteDeadline(time.Now().Add(slowWriteTimeout))); err != nil {
			h.log.WarnContext(r.Context(), "backups: extend write deadline", "err", err)
		}
		next(w, r)
	}
}

func ignoreUnsupported(err error) error {
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
```

(`time.Now()` ist hier zulässig: eine Netzwerkfrist, keine Logik – wie `ioClock` im Download; bei Bedarf `h.ioClock` aus Aufgabe 13 vorziehen.) Routen:

```go
	api.Get("/servers/{id}/backups", rule, h.slow(h.handleOverview))
	api.Post("/servers/{id}/backups", rule, h.handleCreate)
	api.Get("/servers/{id}/backups/job", rule, h.handleJob)
	api.Put("/servers/{id}/backups/schedule", rule, h.handleUpdateSchedule)
	api.Post("/servers/{id}/backups/{snapshot}/pin", rule, h.slow(h.handlePin))
	api.Post("/servers/{id}/backups/{snapshot}/restore", sudo, h.slow(h.handleRestore))
	api.Delete("/servers/{id}/backups/{snapshot}", sudo, h.slow(h.handleDelete))
	api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.notImplemented)
```

Test in `manage_test.go` (die Frist selbst lässt sich mit `httptest` nicht beobachten; der Test sichert, dass ein langsamer Dienst-Aufruf über 60 s hinaus nicht an der Route scheitert, über einen echten `http.Server` mit kurzer `WriteTimeout`):

```go
// The synchronous routes outlive the server's WriteTimeout (here shortened to 50 ms).
func TestSlowRoutesOutliveWriteTimeout(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	e.files.add(id, filesapi.SnapshotScheduled, false, "")
	e.files.delay["list"] = 200 * time.Millisecond
	a := e.admin()
	// the same principal and client IP as e.do, but through a real server with a WriteTimeout
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := httpx.WithClientIP(r.Context(), netip.MustParseAddr("203.0.113.7"))
		e.mux.ServeHTTP(w, r.WithContext(principal.With(ctx, a)))
	}))
	srv.Config.WriteTimeout = 50 * time.Millisecond
	srv.Start()
	defer srv.Close()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+bPath(id, ""), nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("overview through a short WriteTimeout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
```

Dafür bekommt `fakeFiles` ein Feld `delay map[string]time.Duration` (by operation, in `newFakeFiles` initialisiert; `Snapshots` schläft `delay["list"]` außerhalb von `f.mu` vor der Antwort). `manage_test.go` importiert dafür zusätzlich `net/http/httptest`, `net/netip`, `internal/httpx` und `internal/principal`. Ohne `slow` bricht der Server die Antwort nach 50 ms ab und der Client sieht einen Fehler (rot).

In `routes_test.go` enthält `stubbed` nur noch den Download-Eintrag.

- [ ] **Step 8: Grün prüfen**

Run: `gofmt -w internal/backups && make check && make test-vm SUITE=go`
Expected: PASS (`restore_test.go`, `manage_test.go` und alle bisherigen Tests).

- [ ] **Step 9: Commit**

```bash
git add internal/backups
git commit -m "feat(backups): restore with start gate, delete, pin, purge and the overview

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 13: Download, Verdrahtung und Ende-zu-Ende-Test

**Files:**
- Create: `internal/backups/download.go`, `internal/backups/download_test.go`, `cmd/panel/fakebackups_test.go`, `cmd/panel/backups_test.go`
- Delete: `internal/backups/stubs.go`
- Modify: `internal/backups/handler.go` (Route `download`, Feld `downloads`), `internal/backups/routes_test.go` (`stubbed` und `TestRemainingStubs` löschen), `cmd/panel/integration_test.go` (Fake einhängen), `cmd/panel/app.go` (Verdrahtung prüfen)

**Interfaces:**
- Consumes: Aufgaben 9–12, V1 (`DownloadBackup`, `Download`), V4 (`appDeps.Backups`, `servers.AnyGate`), vorhandene cmd/panel-Helfer (`newEnv`, `loginAdminWithTOTP`, `sudo`, `paperServer`, `client.must`, `client.do`, `client.raw`).
- Produces: `handleDownload`, `userSlots`; `fakeBackups` (cmd/panel) mit `holdPlan(plan) func()`, `purged(id) bool`, `zip []byte`.

- [ ] **Step 1: Tests schreiben** – `internal/backups/download_test.go`

```go
package backups_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestDownload(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "") // t0 = 12:00:00 in Berlin
	rec := e.must(http.StatusOK, http.MethodGet, bPath(id, "/"+snap+"/download"), nil, e.admin())
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content type %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="survival-20261012-120000.zip"` {
		t.Fatalf("content disposition %q", cd)
	}
	if !bytes.Equal(rec.Body.Bytes(), e.files.zip) {
		t.Fatalf("body = %q", rec.Body.Bytes())
	}
	if got := e.rec.actions(); len(got) != 1 || got[0] != audit.ActionBackupDownload+":success" {
		t.Fatalf("audit = %v", got)
	}
}

func TestDownloadRejections(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	a := e.admin()
	if rec := e.do(http.MethodGet, bPath(id, "/"+fmt.Sprintf("%064x", 77)+"/download"), nil, &a); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown = %d", rec.Code)
	}
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	e.files.errs["download"] = filesapi.ErrBusy // a forget job holds the repository
	if rec := e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a); rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" {
		t.Fatalf("busy = %d %s", rec.Code, rec.Body)
	}
	e.files.status = filesapi.BackupsStatus{Available: false}
	if rec := e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no restic = %d", rec.Code)
	}
	if n := len(e.rec.actions()); n != 0 {
		t.Fatalf("rejections audited: %v", e.rec.actions())
	}
}

// blockingBody blocks reads until release is closed.
type blockingBody struct{ release chan struct{} }

func (b blockingBody) Read([]byte) (int, error) { <-b.release; return 0, io.EOF }
func (b blockingBody) Close() error             { return nil }

func TestDownloadLimitPerUser(t *testing.T) {
	e := newEnv(t)
	id := e.instance("survival")
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	release := make(chan struct{})
	e.files.dlBody = func() io.ReadCloser { return blockingBody{release: release} }
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a := e.admin()
			e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a)
		}()
	}
	eventually(t, "two open downloads", func() bool {
		e.files.mu.Lock()
		defer e.files.mu.Unlock()
		return e.files.downloads == 2
	})
	a := e.admin()
	if rec := e.do(http.MethodGet, bPath(id, "/"+snap+"/download"), nil, &a); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third download = %d", rec.Code)
	}
	close(release)
	wg.Wait()
}
```

- [ ] **Step 2: Rot prüfen**

Run: `make check && make test-vm SUITE=go`
Expected: FAIL – `TestDownload*` (Route antwortet `503` aus `stubs.go`).

- [ ] **Step 3: `internal/backups/download.go`**

```go
package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const maxDownloadsPerUser = 2

// userSlots counts running downloads per user.
type userSlots struct {
	mu  sync.Mutex
	max int
	n   map[int64]int
}

func newUserSlots(maxPerUser int) *userSlots { return &userSlots{max: maxPerUser, n: map[int64]int{}} }

// acquire takes a slot of userID; release must be called exactly once.
func (u *userSlots) acquire(userID int64) (release func(), ok bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.n[userID] >= u.max {
		return nil, false
	}
	u.n[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			u.mu.Lock()
			defer u.mu.Unlock()
			if u.n[userID]--; u.n[userID] <= 0 {
				delete(u.n, userID)
			}
		})
	}, true
}

// ignoreUnsupported comes from handler.go (task 12).

// deadlineWriter sets a fresh write deadline before every write and flushes after it (pattern
// of serverfiles: the server's WriteTimeout does not bound a long download).
type deadlineWriter struct {
	w     io.Writer
	dl    func() error
	flush func() error
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	if err := d.dl(); err != nil {
		return 0, err
	}
	n, err := d.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, d.flush()
}

// idleReader cancels the service's download when a read waits longer than timeout.
type idleReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (i *idleReader) Read(p []byte) (int, error) {
	i.timer.Reset(i.timeout)
	n, err := i.r.Read(p)
	i.timer.Stop()
	return n, err
}

var errDownloadIdle = errors.New("the file service sent nothing for too long")

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rc := http.NewResponseController(w)
	if err := ignoreUnsupported(rc.SetReadDeadline(time.Time{})); err != nil {
		h.log.DebugContext(ctx, "clear read deadline", "err", err)
	}
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	if err := h.ready(ctx, srv, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	s, err := h.findSnapshot(ctx, srv.ID, snap)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	pr, _ := principal.From(ctx) // guaranteed by the rbac rule
	release, ok := h.downloads.acquire(pr.UserID)
	if !ok {
		h.writeError(w, r, errTooManyDownloads)
		return
	}
	defer release()
	dctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	d, err := h.files.DownloadBackup(dctx, srv.ID, snap)
	if errors.Is(err, filesapi.ErrBusy) { // zip slots full or a forget job holds the repository
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "the file service is busy, try again shortly")
		return
	}
	if err != nil {
		if !rejected(err) {
			h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
		}
		h.writeError(w, r, err)
		return
	}
	defer func() { _ = d.Body.Close() }()
	dw := &deadlineWriter{
		w:     w,
		dl:    func() error { return ignoreUnsupported(rc.SetWriteDeadline(h.ioClock.Now().Add(h.ioTimeout))) },
		flush: func() error { return ignoreUnsupported(rc.Flush()) },
	}
	if err := dw.dl(); err != nil {
		h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
		httpx.Internal(w, r, fmt.Errorf("download: set write deadline: %w", err))
		return
	}
	loc, _, err := h.location(ctx)
	if err != nil {
		loc = time.UTC
	}
	// Server names are lowercase DNS labels (instancespec.ValidName): safe in the header as is.
	name := fmt.Sprintf("%s-%s.zip", srv.Name, time.UnixMilli(s.Time).In(loc).Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.WriteHeader(http.StatusOK)
	idle := &idleReader{r: d.Body, timer: time.AfterFunc(h.ioTimeout, func() { cancel(errDownloadIdle) }), timeout: h.ioTimeout}
	idle.timer.Stop()
	if _, err := io.Copy(dw, idle); err == nil {
		h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultSuccess, map[string]any{"snapshot": snap})
		return
	} else if errors.Is(context.Cause(dctx), errDownloadIdle) {
		h.log.WarnContext(ctx, "backup download aborted, the file service stalled", "server_id", srv.ID)
	} else {
		h.log.DebugContext(ctx, "backup download aborted", "server_id", srv.ID, "err", err)
	}
	h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
	// The status line is out: end the response without its regular end so the browser reports
	// a failed download instead of keeping a truncated zip.
	panic(http.ErrAbortHandler)
}
```

`golangci-lint` meldet ggf. `if … else` nach `return` (`revive`/`gocritic`): dann die Fehlerzweige als `switch` nach dem `io.Copy` schreiben; Verhalten unverändert.

- [ ] **Step 4: `handler.go` ergänzen, Stubs entfernen**

Im `Handler`: `downloads *userSlots`; in `NewHandler`: `downloads: newUserSlots(maxDownloadsPerUser),`. Route: `api.Get("/servers/{id}/backups/{snapshot}/download", rule, h.handleDownload)`. `git rm internal/backups/stubs.go`; in `routes_test.go` `stubbed` und `TestRemainingStubs` löschen (übrige Imports bereinigen).

- [ ] **Step 5: Grün prüfen (Paket)**

Run: `gofmt -w internal/backups && make check && make test-vm SUITE=go`
Expected: PASS für `./internal/backups/...`.

- [ ] **Step 6: Fake-Dienst für cmd/panel** – `cmd/panel/fakebackups_test.go`

```go
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// fakeBackups is the backup side of mcpanel-files: a running job ends at the first poll unless
// its plan ("manual", "scheduled", "pre-restore", "restore", "forget") is held.
type fakeBackups struct {
	mu     sync.Mutex
	snaps  map[int64][]filesapi.Snapshot
	jobs   map[int64]*filesapi.Job
	plans  map[int64]string
	kinds  map[int64]filesapi.SnapshotKind
	notes  map[int64]string
	hold   map[string]chan struct{}
	purges []int64
	seq    int
	zip    []byte
}

func newFakeBackups() *fakeBackups {
	return &fakeBackups{
		snaps: map[int64][]filesapi.Snapshot{}, jobs: map[int64]*filesapi.Job{}, plans: map[int64]string{},
		kinds: map[int64]filesapi.SnapshotKind{}, notes: map[int64]string{}, hold: map[string]chan struct{}{},
		zip: []byte("PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
	}
}

func (f *fakeBackups) holdPlan(plan string) func() {
	ch := make(chan struct{})
	f.mu.Lock()
	f.hold[plan] = ch
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (f *fakeBackups) purged(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.purges, id)
}

func (f *fakeBackups) start(id int64, kind filesapi.JobKind, plan string) (filesapi.Job, error) {
	if j := f.jobs[id]; j != nil && j.State == filesapi.JobRunning {
		return filesapi.Job{}, filesapi.ErrBusy
	}
	f.seq++
	j := &filesapi.Job{ID: fmt.Sprintf("job%d", f.seq), Kind: kind, State: filesapi.JobRunning}
	f.jobs[id], f.plans[id] = j, plan
	return *j, nil
}

func (f *fakeBackups) has(id int64, snap string) bool {
	return slices.ContainsFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snap })
}

func (f *fakeBackups) BackupsStatus(context.Context) (filesapi.BackupsStatus, error) {
	return filesapi.BackupsStatus{Available: true, Version: "0.19.1"}, nil
}

func (f *fakeBackups) Snapshots(_ context.Context, id int64) ([]filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.snaps[id])
	if out == nil {
		out = []filesapi.Snapshot{}
	}
	return out, nil
}

func (f *fakeBackups) CreateBackup(_ context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, err := f.start(req.ID, filesapi.JobBackup, string(req.Kind))
	f.kinds[req.ID], f.notes[req.ID] = req.Kind, req.Note
	return j, err
}

func (f *fakeBackups) RestoreBackup(_ context.Context, id int64, snap string) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(id, snap) {
		return filesapi.Job{}, filesapi.ErrNotFound
	}
	return f.start(id, filesapi.JobRestore, "restore")
}

func (f *fakeBackups) ForgetBackups(_ context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	f.snaps[req.ID] = slices.DeleteFunc(f.snaps[req.ID], func(s filesapi.Snapshot) bool { return slices.Contains(req.Snapshots, s.ID) })
	return f.start(req.ID, filesapi.JobForget, "forget")
}

func (f *fakeBackups) PinBackup(_ context.Context, id int64, snap string, pinned bool) (filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snap })
	if i < 0 {
		return filesapi.Snapshot{}, filesapi.ErrNotFound
	}
	f.seq++
	s := f.snaps[id][i]
	s.ID, s.Pinned = fmt.Sprintf("%064x", f.seq), pinned
	s.ShortID = s.ID[:8]
	f.snaps[id][i] = s
	return s, nil
}

func (f *fakeBackups) DownloadBackup(_ context.Context, id int64, snap string) (filesapi.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(id, snap) {
		return filesapi.Download{}, filesapi.ErrNotFound
	}
	return filesapi.Download{Body: io.NopCloser(bytes.NewReader(f.zip)), Size: -1, IsDir: true}, nil
}

func (f *fakeBackups) PurgeBackups(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges = append(f.purges, id)
	delete(f.snaps, id)
	return nil
}

func (f *fakeBackups) CancelJob(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j := f.jobs[id]; j != nil && j.State == filesapi.JobRunning {
		j.State, j.Error = filesapi.JobFailed, "canceled"
	}
	return nil
}

func (f *fakeBackups) Job(_ context.Context, id int64) (*filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		return nil, nil
	}
	if j.State == filesapi.JobRunning {
		held := false
		if ch, ok := f.hold[f.plans[id]]; ok {
			select {
			case <-ch:
			default:
				held = true
			}
		}
		if !held {
			j.State = filesapi.JobDone
			if j.Kind == filesapi.JobBackup {
				f.seq++
				sid := fmt.Sprintf("%064x", f.seq)
				f.snaps[id] = append([]filesapi.Snapshot{{ID: sid, ShortID: sid[:8], Time: 1_760_270_400_000, Kind: f.kinds[id], Note: f.notes[id], Bytes: 2048}}, f.snaps[id]...)
				j.Snapshot = sid
			}
		}
	}
	out := *j
	return &out, nil
}

var _ filesapi.BackupClient = (*fakeBackups)(nil)
```

- [ ] **Step 7: Fake in `cmd/panel/integration_test.go` einhängen**

Im `env`-Struct `backups *fakeBackups` ergänzen; in `newEnv` vor `buildApp`: `fb := newFakeBackups()`, im `appDeps`-Literal `Backups: fb` (Phase A hat das Feld angelegt; ein vorhandenes `Backups: nil` ersetzen), im zurückgegebenen `&env{…}` `backups: fb`.

- [ ] **Step 8: Ende-zu-Ende-Test** – `cmd/panel/backups_test.go`

```go
package main

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// waitBackup polls the server's backup job until it is in step (or ended, step "").
func waitBackup(t *testing.T, c *client, id int64, step string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) // the panel polls the service every 2 s
	for {
		out := c.must(200, "GET", fmt.Sprintf("/servers/%d/backups/job", id), nil)
		if j, _ := out["job"].(map[string]any); j != nil {
			ended := j["state"] == "done" || j["state"] == "failed"
			if (step == "" && ended) || (step != "" && j["state"] == "running" && j["step"] == step) {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup job never reached %q: %v", step, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The backup routes through the real middleware chain (CSRF, rbac, sudo, audit) against the
// fake files service; the restore holds the start gate of the servers handler.
func TestBackupsEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))
	base := fmt.Sprintf("/servers/%d/backups", id)

	if ov := admin.must(200, "GET", base, nil); ov["available"] != true || len(ov["snapshots"].([]any)) != 0 || ov["job"] != nil {
		t.Fatalf("overview = %v", ov)
	}

	// creating needs the CSRF token like every POST
	csrf := admin.csrf
	admin.csrf = ""
	if code, _ := admin.do("POST", base, map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("without CSRF = %d", code)
	}
	admin.csrf = csrf
	admin.must(202, "POST", base, map[string]any{"note": "vor Update"})
	if j := waitBackup(t, admin, id, ""); j["state"] != "done" {
		t.Fatalf("backup = %v", j)
	}
	snaps := admin.must(200, "GET", base, nil)["snapshots"].([]any)
	if len(snaps) != 1 || snaps[0].(map[string]any)["note"] != "vor Update" {
		t.Fatalf("snapshots = %v", snaps)
	}
	snap := snaps[0].(map[string]any)["id"].(string)

	sched := admin.must(200, "PUT", base+"/schedule", map[string]any{
		"mode": "daily", "interval_hours": 24, "time_of_day": 240, "weekday": 0,
		"keep_last": 24, "keep_daily": 7, "keep_weekly": 4, "keep_monthly": 6,
	})["schedule"].(map[string]any)
	if sched["timezone"] != "Europe/Berlin" || sched["next_run_at"] == nil {
		t.Fatalf("schedule = %v", sched)
	}

	// restore: while it runs, power start answers 409 (servers.AnyGate)
	release := e.backups.holdPlan("restore")
	admin.must(202, "POST", base+"/"+snap+"/restore", nil)
	// right after the 202 (the job may still wait for the worker) the gate is already closed
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}); code != http.StatusConflict {
		t.Fatalf("start right after the restore request = %d %v", code, out)
	}
	waitBackup(t, admin, id, "restoring")
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}); code != http.StatusConflict {
		t.Fatalf("start during restore = %d %v", code, out)
	}
	release()
	if j := waitBackup(t, admin, id, ""); j["state"] != "done" {
		t.Fatalf("restore = %v", j)
	}
	if n := len(admin.must(200, "GET", base, nil)["snapshots"].([]any)); n != 2 { // + pre-restore
		t.Fatalf("snapshots after restore = %d", n)
	}

	resp, body := admin.raw(base + "/" + snap + "/download")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" || string(body) != string(e.backups.zip) {
		t.Fatalf("download = %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	admin.must(204, "DELETE", fmt.Sprintf("/servers/%d?purge_backups=1", id), nil)
	if !e.backups.purged(id) {
		t.Fatal("deleting the server with purge_backups did not purge the repository")
	}
}
```

(`client.raw` steht in `cmd/panel/plugins_test.go`, gleiches Paket.)

- [ ] **Step 9: Verdrahtung prüfen** – `cmd/panel/app.go`

Run: `grep -n "backups\.\|backupsH\|AnyGate\|Backups:" cmd/panel/app.go cmd/panel/main.go`
Expected (V4; fehlt etwas, genau so ergänzen):

```go
	var serversH *servers.Handler
	backupsH := backups.NewHandler(backups.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Notifier: disp, Files: d.Backups,
		Instances: d.Instances, Console: d.Console,
		Reconcile: func() { serversH.Trigger() },
	})
	serversH = servers.NewHandler(servers.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Box: box, Instances: d.Instances,
		Catalog: cat, Gate: servers.AnyGate(backupsH, filesH), Backups: backupsH,
	})
```

dazu `backupsH.Routes(api)` und in `run` `go func() { defer wg.Done(); backupsH.Run(ctx) }()` mit passend erhöhtem `wg.Add`. `Notifier: disp` und `Console: d.Console` müssen gesetzt sein (sonst keine Benachrichtigungen bzw. kein `save-off`). `main.go`: `_ "time/tzdata"` und `Backups:` = derselbe `*filesapi.UnixClient` wie `Files`. Wichtig: `appDeps.Backups` bleibt ein `nil`-Interface, wenn kein Dienst konfiguriert ist (kein typisierter `nil`-Zeiger).

- [ ] **Step 10: Grün prüfen (alles)**

Run: `gofmt -w internal/backups cmd/panel && make check && make test-vm`
Expected: PASS aller Suites; `TestBackupsEndToEnd` braucht wegen des echten 2-s-Abfrageintervalls rund 10 s; `TestRouteCoverage` und der OpenAPI-Abgleich bleiben grün (Routen unverändert seit Phase A).

- [ ] **Step 11: Commit und PR fertig machen**

```bash
git add internal/backups cmd/panel
git commit -m "feat(backups): zip download, wiring and an end-to-end test

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/backups-panel
gh pr ready <n>
gh pr checks <n> --watch
```

Expected: Job `CI OK` grün. Danach dem Lead melden (PR-Nummer, Abweichungen aus diesem Plan, offene Punkte).

---

## Selbstprüfung gegen Spec und Verträge

- §5.1 Datenmodell: genutzt über V2-Queries (Aufgaben 9–11); `saveoff_since` (10), `last_*` (11).
- §5.2 Zeitplan: `NextRun` (9) inkl. DST-Lücke/-Doppelung, verpasste Termine (`TestMissedRunRunsOnce`), Zeitzonenwechsel (11, Abweichung 5).
- §5.3 Warteschlange/Jobs: `queue` (10), Steps und Codes aus V3, Abfrage alle 2 s, Abbruch beim Beenden mit 20 s.
- §5.4 Backup-Ablauf und Absturz: `runBackup`, `resume`, `recover`, `retrySaveOn` (10), Retention nur nach geplantem Erfolg, Forget-Fehler = ok mit Hinweis (11).
- §5.5 Restore: Sperre (`DataJobRunning`), Stopp mit 120 s, Pre-Restore, Restore, Reconcile anstoßen nach dem Öffnen der Sperre (`jobSpec.after`) (12).
- §5.6 Löschen und Purge (12); `purge_backups` am Server-Löschen liegt in Phase A (`servers`), Ende-zu-Ende in 13.
- §5.7 API: alle acht Routen mit Regeln aus V3 (9–13); Download 2 pro Benutzer, Fristen pro Operation (13); `available=false` ohne Fehler (12).
- §5.8 Fehlercodes: `backups_unavailable`, `backup_running`, `files_unavailable`, `conflict`, `not_found`, `validation_failed`, `rate_limited`; `insufficient_storage` nur im Job (V3-Präzisierung).
- §5.9 Audit/Benachrichtigung: alle sieben Aktionen außer der Zeitzone (Phase A, `servers`); `backup.failed` mit 6-h-Drossel und Reset nach Erfolg (11), Restore-Fehler (12).
