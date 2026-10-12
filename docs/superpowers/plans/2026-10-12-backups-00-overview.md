# mc-panel Teilprojekt 4: Backups – Implementierungsplan (Übersicht + Verträge)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Backups der Server-Datenverzeichnisse, manuell und nach Zeitplan, mit gestufter Retention, Anpinnen, konsistentem Snapshot bei laufendem Server (`save-off`/`save-all flush`/`save-on`), vollständigem Restore mit Sicherheits-Snapshot, Zip-Download und Fehlermeldungen über `notify`. Nur lokale restic-Repos.

**Architecture:** Der Dateidienst `mcpanel-files` (Benutzer `mcpanel-mc`, kein Netz) startet restic als Kindprozess. restic sieht das Datenverzeichnis nie: Beim Backup erzeugt der Dienst einen tar-Stream über `os.Root` (`restic backup --stdin`), Restore und Download lesen `restic dump` als tar-Stream und entpacken bzw. wandeln ihn mit den Regeln aus TP3b. Ein restic-Repo pro Server unter `<instances_dir>/backups/repos/<id>`. Das neue panel-Paket `internal/backups` orchestriert Zeitplan (Ticker über `clock.Clock`), eine panel-weite Warteschlange, Jobs, RCON-Schritte über den agent, die Restore-Sperre (verallgemeinerte Extract-Sperre), Audit und `backup.failed`-Benachrichtigungen. Der agent bekommt nur das Verzeichnis `backups/` in `ensureLayout`.

**Tech Stack:** Go (Standardbibliothek, chi, `modernc.org/sqlite`, `BurntSushi/toml`, `archive/tar`, `archive/zip`, `os/exec`, `time/tzdata`); restic ≥ 0.17 als externes Binary (CI/VM 0.19.1); React 19, TanStack Router/Query, shadcn/ui, react-i18next, MSW 2, Vitest, Playwright. Keine neuen Go- und keine neuen Frontend-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Aufgaben | Inhalt |
|---|---|---|
| `2026-10-12-backups-00-overview.md` (diese) | – | Ausführung, Constraints, Review Focus, Dateistruktur, Verträge V1–V6 |
| `2026-10-12-backups-01-contracts.md` | 1–3 | `filesapi` (Backup-Typen, `BackupClient`, Job-Felder), Extract-Sperre → `DataGate` mit Job-Art-Filter; Migration + sqlc, Fehlercodes, Audit-Aktionen, Notify-Ereignis, `servers` (Zeitzone in den Einstellungen, `purge_backups`); panel-Vertrag (OpenAPI, Stub `internal/backups`, Verdrahtung, MSW-Mocks), restic-Installation auf VM und CI |
| `2026-10-12-backups-02-files.md` | 4–8 | Dienst: Konfiguration, Layout, restic-Ausführung, Status; tar-Erzeugung + Backup-Job + Liste; tar-Entpacken + Tausch + Restore-Job + Abbrechen; Forget/Pin/Purge + Download; Integrationstests mit echtem restic |
| `2026-10-12-backups-03-panel.md` | 9–13 | panel: Zeitplan-Rechnung + Zeitplan-Route; Warteschlange + Jobs + Backup-Ablauf + Wiederanlauf; Ticker + Retention + Zeitzonenwechsel + Benachrichtigung; Restore + Sperre + Löschen + Pin + Purge + Übersicht; Download + Verdrahtung + Gesamttest |
| `2026-10-12-backups-04-frontend.md` | 14–17 | Frontend: Tab „Backups“ mit Liste, Erstellen, Job-Anzeige; Snapshot-Aktionen (Pin, Download, Restore-Dialog, Löschen); Zeitplan-Karte, Zeitzone in den Einstellungen, Checkbox beim Server-Löschen, Rechte-Hinweis; Playwright |
| `2026-10-12-backups-05-ci-docs.md` | 18–19 | CI-Abnahme in `scripts/ci-instances.sh`, Units in `scripts/testvm-deploy.sh`, Doku, Checkliste `docs/dev/acceptance-tp4.md`, Gesamt-Review |

## Ausführung (Agent-Teams, Tests auf der Test-VM)

Es gelten die Arbeitsregeln aus `CLAUDE.md`:

- **Auf dem Mac nur `make check`** (und `make generate`, wo der Plan es sagt). Neue, noch ungetrackte Dateien vor `make check` mit `git add -N` vormerken (sonst prüft gofmt sie nicht).
- **Tests auf der Test-VM:** „Run: go test …“ / „npx vitest …“ / Playwright heißt `make test-vm SUITE=go|web|e2e|lint` (einzelne Tests per `ssh claude@192.168.1.117 'cd ~/mc-panel-test/<worktree-name> && PATH=/usr/local/go/bin:$PATH go test …'`, siehe `docs/dev/README.md`, „Tests auf der Test-VM“). Vor jedem Push mindestens die betroffenen Suites, vor „ready for review“ `make test-vm` (alle). SSH braucht eine Bash ohne Sandbox. Die Root-Abnahmen (`nftables`, `instances`) laufen nur in der CI.
- **restic auf der VM:** Ab Phase A installiert `scripts/vm-test.sh` restic 0.19.1 nach `/usr/local/bin/restic` und setzt `MCPANEL_TEST_RESTIC=/usr/local/bin/restic` für die Go-Tests; die CI macht das im Go-Job ebenso. Integrationstests mit echtem restic überspringen sich ohne diese Variable (`t.Skip`), auf dem Mac also immer.
- **TDD:** Test zuerst; „rot“ = `make check`-Kompilierfehler bzw. roter VM-Lauf, „grün“ = grüner VM-Lauf. Die CI (`gh pr checks <n> --watch`, Job `CI OK`) ist das Gate vor dem Merge.
- **Nichts Systemnahes auf dem Mac** (kein `brew install restic`). Auf der Test-VM nie deployen (`make deploy-testvm` macht nur der Lead).
- **Git:** nie auf `main`; je Phase ein Branch und Draft-PR; jeder Teammate in eigenem Worktree (`git worktree add ../mc-panel-wt/<branch> -b <branch> origin/main`). Mergen nur durch den Nutzer. Die Plan-Dateien liegen bis zum Merge des Doku-PRs nur auf dem Branch `docs/tp4-backups-spec`; Teammates lesen sie unter `/Users/jannes/Workspace/mc-panel/docs/superpowers/plans/` (Checkout des Leads, nicht verändern).

| Phase | Branch / PR | Wer | Aufgaben | Startet, wenn |
|---|---|---|---|---|
| A | `feat/backups-contracts` | Teammate „contracts“ | 1–3 | – |
| B1 | `feat/backups-files` | Teammate „files“ | 4–8 | A gemerged |
| B2 | `feat/backups-panel` | Teammate „panel“ | 9–13 | A gemerged |
| B3 | `feat/backups-frontend` | Teammate „frontend“ | 14–17 | A gemerged |
| C | `feat/backups-ci-docs` | Teammate „ci-docs“ | 18 (19 koordiniert der Lead) | B1, B2, B3 gemerged |

Dateieigentum in Phase B (disjunkt):

- **B1:** `internal/files/**` außer `internal/files/filesapi/**`; `internal/config/files.go`, `internal/config/files_test.go`; `internal/agent/instances/datadir*.go` (nur `ensureLayout` und dessen Tests); `cmd/files/**`.
- **B2:** `internal/backups/**`, `cmd/panel/**`.
- **B3:** `web/src/features/backups/**` (neu), `web/src/features/servers/**`, `web/src/features/users/**`, `web/src/features/settings/**`, Locale-Dateien unter `web/src/i18n/**` (außer `errors.*`), `web/e2e/**`, `web/playwright.config.ts` (nur `testMatch` um `backups` erweitern, falls nötig).
- **Nur Phase A** ändert `internal/files/filesapi/**`, `internal/serverfiles/**`, `internal/servers/**`, `internal/store/**`, `internal/httpx/errors.go`, `internal/audit/types.go`, `internal/notify/notify.go`, `internal/notify/sender.go`, `api/openapi.yaml`, `web/src/api/**`, `web/src/mocks/**`, in `web/src/i18n/**` nur `errors.*`/`ERROR_CODES`, außerdem `scripts/install-restic.sh` (neu, gemeinsame gepinnte Installation), `scripts/vm-test.sh` und `.github/workflows/ci.yml` (nur restic-Installation im Go-Job und `MCPANEL_TEST_RESTIC`). Bedarf in Phase B → Lead fragen (Änderung kommt per eigenem PR auf `main`, danach `git merge origin/main`).
- **Nur Phase C** ändert sonst `scripts/**`, `Makefile`, `.github/workflows/**`, `docs/**`, `README.md`, `CLAUDE.md`.

## Global Constraints

- Alle Global Constraints aus TP1–3c und alle Konventionen aus `CLAUDE.md` gelten (`clock.Clock` statt `time.Now()`, Kontext überall, Go-Tests nur Standardbibliothek, keine neuen Abhängigkeiten, Lint-Ausnahmen gezielt mit Begründung, gosec G204 für `exec.Command` mit Begründung, Conventional Commits; jede Commit-Nachricht endet mit den Zeilen `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`, auch wo ein Phasen-Plan nur die erste Zeile zeigt).
- Recht: alle Backup-Routen `rbac.Server(rbac.PermServerBackups, "id")`; Restore und Löschen eines Snapshots zusätzlich `.WithSudo()`. Admins immer.
- restic: Mindestversion `0.17.0`; CI/VM `0.19.1` aus `https://github.com/restic/restic/releases/download/v0.19.1/restic_0.19.1_linux_<amd64|arm64>.bz2` mit fest eingetragener SHA-256 (aus `SHA256SUMS` des Releases). Default-Binary `/usr/bin/restic`, Dev `restic` (PATH).
- restic-Aufrufe: feste argv ohne Shell, Umgebung leer bis auf `RESTIC_REPOSITORY`, `RESTIC_PASSWORD_FILE`, `HOME=<backups_dir>`, `RESTIC_PROGRESS_FPS=1` und (bei `backup` und `forget`) `TZ=<backup_timezone>`: restic speichert die Snapshot-Zeit in der Zone des Backup-Prozesses und bildet daraus die Tages-/Wochen-/Monatsgruppen von `forget`; immer `--no-cache`; `--json` wo verfügbar; `snapshots` mit `--no-lock`; vor jedem Job `restic unlock`; `backup` mit `--stdin --stdin-filename data.tar --host mcpanel --tag mcpanel --tag kind=<kind>` und optional `--tag note=<base64url ohne Padding>`; Pin = Tag `pinned`.
- Layout: `<instances_dir>/backups` `mcpanel-mc 0700` (agent), darin `repos/<id>/` und `keys/<id>` (0600, 32 Zufallsbytes base64) vom Dienst.
- Snapshot-IDs: genau 64 Zeichen `[0-9a-f]`; Notiz ≤ 64 Zeichen (Runen), gültiges UTF-8, keine Steuerzeichen (`< 0x20`, `0x7f`), nicht nur Leerzeichen; leer = keine Notiz.
- Grenzen Dienst (Defaults in `files.toml` `[limits]`): `max_backup_bytes = 68719476736` (64 GiB), `max_backup_entries = 1000000`, `max_backup_jobs = 1`; `reserve_bytes` aus TP3b (2 GiB) gilt für Backup (auf `backups/`) und Restore (auf `instances/<id>`, plus Snapshot-Größe). Ein Job pro Instanz über alle Arten (`extract|backup|restore|forget`).
- Grenzen panel: höchstens ein Backup-/Restore-/Delete-Job gleichzeitig im ganzen panel (FIFO), einer pro Server (`409 backup_running`); RCON-Fristen je 60 s; Stopp-Frist beim Restore 120 s; Job-Abfrage beim Dienst alle 2 s; Download ≤ 2 gleichzeitig pro Benutzer (`429 rate_limited`); Benachrichtigung `backup.failed` pro Server und Art höchstens einmal in 6 h; Ticker jede Minute.
- Zeitplan: `mode` ∈ `off|interval|daily|weekly`, `interval_hours` 1–168, `time_of_day` 0–1439 (Minuten), `weekday` 0–6 (0 = Sonntag), `keep_*` 0–1000, bei `mode ≠ off` mindestens ein `keep_*` > 0; Defaults `off`, 24, 240, 0, 24/7/4/6. Zeitzone global `instance_settings.backup_timezone`, Default `Europe/Berlin`, IANA, `time.LoadLocation`; panel und Dienst importieren `time/tzdata`.
- Neue panel-Fehlercodes: `backups_unavailable` (503), `backup_running` (409). Neuer Dienst-Code: `unavailable` (503). Neue Audit-Aktionen: `backup.create`, `backup.restore`, `backup.delete`, `backup.pin`, `backup.unpin`, `backup.download`, `backup.schedule_update`. Neues Ereignis `backup.failed`.
- Fehlermeldungen und `job.error` enthalten nie Host-Pfade (Dienst ersetzt sie wie in TP3b §4).
- Frontend: Texte `de`/`en`, keine Inline-Skripte, keine CDNs, kein `dangerouslySetInnerHTML`, MSW-Mock für jede OpenAPI-Operation; Zeiten in der Browser-Zeitzone, Zeitplan-Uhrzeiten in der panel-Zeitzone (angezeigt).

## Review Focus

1. **Laufender Server, RCON scheitert oder das panel stirbt mitten im Backup:** `save-off` gesendet, dann Dienst-Fehler, Zeitüberschreitung, Abbruch durch Herunterfahren oder `kill -9` des panels; erwartet: `save-on` folgt in jedem Pfad (beim nächsten Start über `saveoff_since`), nie ein Server, der dauerhaft nicht speichert. → Aufgabe 10 (Tests für jeden Fehlerpfad und den Wiederanlauf).
2. **Feindliches Datenverzeichnis beim Backup:** Ordner wird während des Durchlaufs gegen einen Symlink auf `../<andere-id>` getauscht, FIFO, Gerätedatei, Datei schrumpft/wächst beim Lesen, Namen mit Steuerzeichen oder ungültigem UTF-8, 1 000 001 Einträge; erwartet: nichts außerhalb von `instances/<id>` im tar, keine Hänger, Grenzen greifen, `skipped` zählt. → Aufgabe 5.
3. **Feindlicher oder kaputter Snapshot beim Restore** (Repo-Manipulation durch einen kompromittierten `mcpanel-mc`-Prozess): tar mit Symlink-, Hardlink-, Geräte-Einträgen, `../`, absoluten Pfaden, doppelten Namen, verborgenen Namen, falschen Größen, abgeschnittenem Stream; erwartet: Job `failed`, Datenverzeichnis unverändert, kein Byte außerhalb des Temp-Ordners. → Aufgabe 6.
4. **Gleichzeitigkeit:** Restore angefordert, während ein Backup desselben Servers wartet oder läuft; Entpacken im Dateien-Tab während eines Backups; Start/Neustart während eines Restores (auch nach panel-Neustart mit noch laufendem Dienst-Job); Server gelöscht, während sein Job wartet; Zeitzonenwechsel mit wartenden Läufen; zwei Tabs pinnen denselben Snapshot; erwartet: `409 backup_running`/`429 busy`/`409 conflict` statt Durcheinander, Sperre hält, verworfene Jobs ohne Nebenwirkungen. → Aufgaben 6, 10, 12.
5. **Betriebsfehler:** Platte läuft während Backup oder Restore voll, restic fehlt oder ist zu alt, verwaiste restic-Sperre nach Absturz, Repo-Schlüssel fehlt, Repo ohne Snapshots; erwartet: klare Fehler (`507`, `backups_unavailable`, Job-Fehler ohne Pfade), `restic unlock` vor jedem Job, Rest des Dienstes läuft weiter. → Aufgaben 4, 6, 8.

## Dateistruktur

```
internal/files/filesapi/backups.go, errors.go, client.go, types.go (1)  Backup-Typen, BackupClient, Job-Felder, Code unavailable
internal/serverfiles/extract.go, handler.go                 (1)  DataJobRunning mit Job-Art-Filter
internal/servers/gate.go, power.go, reconcile.go, handler.go (1–2) DataGate, AnyGate, BackupPurger
internal/servers/settings.go, servers.go                    (2)  backup_timezone, purge_backups
internal/store/migrations/00006_backups.sql                 (2)  backup_schedules, backup_timezone
internal/store/queries/backups.sql → backups.sql.go         (2)  Backups…-Queries
internal/httpx/errors.go, internal/audit/types.go           (2)  Codes, Aktionen
internal/notify/notify.go, sender.go                        (2)  EventBackupFailed
api/openapi.yaml, web/src/api/schema.d.ts, types.ts         (3)  Vertrag
internal/backups/handler.go                                 (3)  Deps, Routes (Stubs), ab B2 echt
cmd/panel/app.go, main.go                                   (3)  Verdrahtung
web/src/mocks/handlers/backups.ts, db.ts                    (3)  Mocks
scripts/install-restic.sh, vm-test.sh, .github/workflows/ci.yml (3) restic für Tests
internal/config/files.go, internal/agent/instances/datadir.go (4) Schlüssel, Layout
internal/files/restic.go, backupsvc.go                      (4)  resticRunner, Status, Repo/Schlüssel
internal/files/tarwrite.go, backup.go                       (5)  tar über os.Root, Backup-Job, Liste
internal/files/tarextract.go, restore.go, jobs.go           (6)  Entpacken, Tausch, Restore-Job, Abbrechen
internal/files/forget.go, tarzip.go, server.go              (7)  Forget/Pin/Purge, Download, Endpunkte
internal/files/restic_integration_test.go                   (8)  echtes restic
internal/backups/schedule.go, store.go, schedule_api.go     (9)  Zeitplan-Rechnung, DB, Route
internal/backups/queue.go, jobs.go, backup.go, recover.go   (10) Warteschlange, Jobs, Backup-Ablauf, Wiederanlauf
internal/backups/ticker.go, retention.go, notify.go         (11) Ticker, Retention, Zeitzone, Benachrichtigung
internal/backups/restore.go, gate.go, manage.go, overview.go (12) Restore, Sperre, Löschen, Pin, Purge, Übersicht
internal/backups/download.go; cmd/panel                     (13) Download, Verdrahtung, Gesamttest
web/src/features/backups/**                                 (14–16) Tab, Liste, Dialoge, Zeitplan
web/src/features/servers/**, settings/**, users/**          (16) Tab-Einbindung, Zeitzone, Lösch-Checkbox, Hinweis
web/e2e/backups.spec.ts                                     (17) Playwright
scripts/ci-instances.sh, scripts/testvm-deploy.sh           (18) CI-Abnahme, Units
docs/dev/acceptance-tp4.md, docs/dev/README.md, CLAUDE.md, README.md, Specs (19)
```

## Verträge (verbindlich)

### V1 – `filesapi` (Aufgabe 1, Paket `github.com/Bernotese/mc-panel/internal/files/filesapi`, neue Datei `backups.go`)

```go
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
	MaxNoteRunes     = 64
	MaxForgetIDs     = 100

	// BackupCallTimeout bounds list, pin and the restore pre-check (restic reads the repo index).
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

type SnapshotKind string

const (
	SnapshotScheduled  SnapshotKind = "scheduled"
	SnapshotManual     SnapshotKind = "manual"
	SnapshotPreRestore SnapshotKind = "pre-restore"
)

func (k SnapshotKind) Valid() bool

type Snapshot struct {
	ID      string       `json:"id"`       // 64 hex
	ShortID string       `json:"short_id"` // 8 hex
	Time    int64        `json:"time"`     // Unix ms
	Kind    SnapshotKind `json:"kind"`
	Pinned  bool         `json:"pinned"`
	Note    string       `json:"note"`  // "" = none
	Bytes   int64        `json:"bytes"` // size of data.tar (summary.total_bytes_processed)
}

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

// Validate: ID > 0, valid kind, ValidNote (only manual), Timezone "" or ValidTimezone; errors wrap ErrInvalid.
func (r CreateBackupRequest) Validate() error
func ValidTimezone(tz string) bool // IANA name ≤ MaxTimezoneLen accepted by time.LoadLocation (not "Local")

type SnapshotRequest struct {
	ID       int64  `json:"id"`
	Snapshot string `json:"snapshot"`
}

type ForgetPolicy struct {
	KeepLast    int `json:"keep_last"`
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

// Validate: every value 0..1000, at least one > 0.
func (p ForgetPolicy) Validate() error

// ForgetRequest carries exactly one of Snapshots (1..MaxForgetIDs IDs) or Policy (then Timezone
// is required, an IANA name accepted by time.LoadLocation).
type ForgetRequest struct {
	ID        int64         `json:"id"`
	Snapshots []string      `json:"snapshots,omitempty"`
	Policy    *ForgetPolicy `json:"policy,omitempty"`
	Timezone  string        `json:"timezone,omitempty"`
}

func (r ForgetRequest) Validate() error

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

func ValidSnapshotID(s string) bool // exactly 64 chars [0-9a-f]
func ValidNote(s string) bool       // "" or ≤ MaxNoteRunes runes, valid UTF-8, no control chars, not only spaces

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

var _ BackupClient = (*UnixClient)(nil)
```

- `Job` (`types.go`) bekommt vier Felder: `Kind JobKind \`json:"kind"\``, `Snapshot string \`json:"snapshot"\`` (Backup: neue ID nach `done`; Restore: Quelle; sonst ""), `Skipped int64 \`json:"skipped"\`` und `Code string \`json:"code"\`` (Wire-Code des Fehlers eines `failed`-Jobs, z. B. `too_large`, `insufficient_storage`, `busy`, `unavailable`; "" sonst und bei Fehlern ohne Sentinel). Extract-Jobs setzen `Kind: JobExtract`. Phase A legt die Felder an, der Dienst füllt `Kind` und `Code` ab Phase B1 (`jobs.finish` setzt `Code` über `filesapi.CodeOf(err)`).
- `errors.go`: `CodeUnavailable = "unavailable"` (503), `ErrBackupsUnavailable = errors.New(CodeUnavailable)`, Eintrag in `codes`. Nicht zu verwechseln mit `ErrUnavailable` (Dienst nicht erreichbar).
- Client: ungültige IDs, Snapshot-IDs, Notizen, Policies → `ErrInvalid` ohne Aufruf. Zeitlimits: `BackupsStatus` `HealthTimeout`; `Snapshots`, `PinBackup`, `RestoreBackup` `BackupCallTimeout`; `CreateBackup`, `ForgetBackups`, `CancelJob` `CallTimeout`; `PurgeBackups` `DeleteTimeout`; `DownloadBackup` wie `Download` (`DownloadHeaderTimeout`, Body ohne Frist, `Name` = ""). `Snapshots` liefert nie `nil`.
- `serverfiles` (Aufgabe 1): `ExtractRunning` heißt `DataJobRunning` und meldet nur noch Jobs mit `Kind` ∈ {`""`, `extract`, `restore`} im Zustand `running` (ein laufendes Backup sperrt den Start nicht). Fakes in `internal/serverfiles/helpers_test.go` und `cmd/panel/fakefiles_test.go` setzen `Kind`.

### V2 – Daten und Querschnitt (Aufgabe 2)

Migration `internal/store/migrations/00006_backups.sql` (goose `Up`/`Down`) mit Tabelle und `ALTER TABLE` aus Spec §5.1 (wortgleich; `Down`: `DROP TABLE backup_schedules;` und `ALTER TABLE instance_settings DROP COLUMN backup_timezone;`). Queries `internal/store/queries/backups.sql`:

```sql
-- name: BackupsEnsureSchedule :exec
INSERT INTO backup_schedules (server_id, updated_at) VALUES (?, ?) ON CONFLICT (server_id) DO NOTHING;

-- name: BackupsGetSchedule :one
SELECT * FROM backup_schedules WHERE server_id = ?;

-- name: BackupsUpdateSchedule :exec
UPDATE backup_schedules SET mode = ?, interval_hours = ?, time_of_day = ?, weekday = ?,
    keep_last = ?, keep_daily = ?, keep_weekly = ?, keep_monthly = ?, next_run_at = ?, updated_at = ?
WHERE server_id = ?;

-- name: BackupsListDue :many
SELECT * FROM backup_schedules
WHERE mode != 'off' AND next_run_at IS NOT NULL AND next_run_at <= ?
ORDER BY next_run_at, server_id;

-- name: BackupsListEnabled :many
SELECT * FROM backup_schedules WHERE mode != 'off' ORDER BY server_id;

-- name: BackupsSetNextRun :exec
UPDATE backup_schedules SET next_run_at = ? WHERE server_id = ?;

-- name: BackupsStartRun :exec
UPDATE backup_schedules SET last_status = 'running', last_run_at = ?, last_error = NULL WHERE server_id = ?;

-- name: BackupsFinishRun :exec
UPDATE backup_schedules SET last_status = ?, last_error = ?, last_snapshot_id = ?, next_run_at = ?
WHERE server_id = ?;

-- name: BackupsFailRunning :exec
UPDATE backup_schedules SET last_status = 'failed', last_error = ? WHERE last_status = 'running';

-- name: BackupsSetSaveOff :exec
UPDATE backup_schedules SET saveoff_since = ? WHERE server_id = ?;

-- name: BackupsListSaveOff :many
SELECT server_id, saveoff_since FROM backup_schedules WHERE saveoff_since IS NOT NULL ORDER BY server_id;

-- name: BackupsGetTimezone :one
SELECT backup_timezone FROM instance_settings WHERE id = 1;
```

Dazu in `internal/store/queries/servers.sql`: `-- name: ServersSetBackupTimezone :exec` `UPDATE instance_settings SET backup_timezone = ?, updated_at = ? WHERE id = 1;` (`ServersGetSettings` liefert die neue Spalte über `SELECT *`). Generierter Typ `store.BackupSchedule` (Feldnamen nach sqlc; Phase A committet `backups.sql.go`).

- `internal/httpx/errors.go`: `CodeBackupsUnavailable = "backups_unavailable"`, `CodeBackupRunning = "backup_running"`.
- Audit-Ziel aller `backup.*`-Einträge ist das Server-Ziel wie bei den Plugin-Aktionen (`server:<id>`).
- `internal/audit/types.go`: `ActionBackupCreate = "backup.create"`, `ActionBackupRestore = "backup.restore"`, `ActionBackupDelete = "backup.delete"`, `ActionBackupPin = "backup.pin"`, `ActionBackupUnpin = "backup.unpin"`, `ActionBackupDownload = "backup.download"`, `ActionBackupScheduleUpdate = "backup.schedule_update"`.
- `internal/notify/notify.go`: `EventBackupFailed EventType = "backup.failed"`; `sender.go`: Titel `"Backup failed"` (bzw. das Muster der übrigen Titel).
- `internal/servers` (Aufgabe 2):
  - `gate.go`: `ExtractGate` → `DataGate { DataJobRunning(ctx context.Context, serverID int64) bool }`; `func AnyGate(gates ...DataGate) DataGate` (wahr, sobald einer wahr ist; `nil`-Einträge übersprungen). `Deps.Extract` → `Deps.Gate DataGate`. Meldung der Power-`409`: „a file or backup job is rewriting this server's data; try again when it is done“.
  - `BackupPurger { PurgeBackups(ctx context.Context, serverID int64) error }`, `Deps.Backups BackupPurger` (nil = nichts zu löschen).
  - `DELETE /servers/{id}?purge_backups=1|true`: nach erfolgreichem Commit `Backups.PurgeBackups` mit `context.WithoutCancel(r.Context())` und eigener Frist `filesapi.DeleteTimeout + time.Minute` (die Minute deckt das Beenden der Jobs in `backups.PurgeBackups`, ≤ 30 s); vorher setzt der Handler die Schreibfrist der Antwort per `http.NewResponseController(w).SetWriteDeadline` auf jetzt + dieselbe Frist + 10 s (wie die Download-Routen), damit das `204` nicht an der 60-s-`WriteTimeout` verloren geht und ein Client-Abbruch das Löschen nicht mittendrin beendet; Erfolg → Audit-Detail `backups_purged: true`, Fehler → Log + `backups_purged: false`, Antwort bleibt `204`. Ohne Parameter fehlt das Detail. Ungültiger Wert → `400 validation_failed`.
  - Einstellungen: `settingsJSON.BackupTimezone string \`json:"backup_timezone"\``; `updateSettingsRequest.BackupTimezone optional[string]` (fehlt = bleibt; `null` → `400`); Prüfung `time.LoadLocation` und Länge ≤ 64, sonst `400 validation_failed`; Audit im vorhandenen `server.settings_update` mit `backup_timezone: {old, new}` (wie `base_domain` im selben Eintrag). `internal/servers` importiert `time/tzdata` nicht; das macht `cmd/panel` (Aufgabe 3).

### V3 – panel-API (Aufgabe 3, `api/openapi.yaml`, Tag `backups`)

Alle Pfade mit Pfad-Parameter `id` (int64); `{snapshot}` ist ein String mit `pattern: '^[0-9a-f]{64}$'`. Jede Operation kann zusätzlich `401`, `403` und `404 not_found`.

| Pfad | Methode | operationId | Request | Antworten |
|---|---|---|---|---|
| `/servers/{id}/backups` | get | `getBackups` | – | `200 BackupOverview` |
| `/servers/{id}/backups` | post | `createBackup` | `CreateBackupRequest` | `202 BackupJobStarted`, 400, 409 (`backup_running`, `conflict` = Server hat noch keine Instanz), 503 (`backups_unavailable`, `files_unavailable`) |
| `/servers/{id}/backups/job` | get | `getBackupJob` | – | `200 BackupJobResponse` |
| `/servers/{id}/backups/schedule` | put | `updateBackupSchedule` | `UpdateBackupScheduleRequest` | `200 BackupScheduleResponse`, 400 |
| `/servers/{id}/backups/{snapshot}/pin` | post | `pinBackup` | `PinBackupRequest` | `200 BackupSnapshotResponse`, 400, 409 (`backup_running`), 503 |
| `/servers/{id}/backups/{snapshot}/restore` | post | `restoreBackup` | – | `202 BackupJobStarted`, 400, 403 (`sudo_required`), 409 (`backup_running`, `conflict`), 503 |
| `/servers/{id}/backups/{snapshot}` | delete | `deleteBackup` | – | `202 BackupJobStarted`, 400, 403 (`sudo_required`), 409 (`backup_running`), 503 |
| `/servers/{id}/backups/{snapshot}/download` | get | `downloadBackup` | – | `200 application/zip` (binary), 400, 429, 503 |

Außerdem: `ServerSettings` bekommt `backup_timezone: string` (required), `UpdateServerSettingsRequest` bekommt optional `backup_timezone: string`; `deleteServer` bekommt Query `purge_backups` (boolean, optional). Error-Enum um `backups_unavailable`, `backup_running` erweitert.

Schemas (Request-Bodies `additionalProperties: false`; Zeitpunkte Unix-ms `integer`/`int64`; nullable als `type: [X, 'null']`):

```
BackupKind            enum scheduled | manual | pre-restore
BackupSnapshot        {id, short_id: string; time: integer; kind: BackupKind; pinned: boolean;
                       note: string|null; bytes: integer}
BackupJobKind         enum backup | restore | delete
BackupJobTrigger      enum manual | scheduled
BackupJobState        enum queued | running | done | failed
BackupJobStep         enum queued | saving | backing_up | resuming | pruning | stopping | pre_restore | restoring | deleting | done
BackupJobError        enum rcon_unavailable | agent_unavailable | files_unavailable | files_busy | backups_unavailable |
                           insufficient_storage | too_large | stop_timeout | canceled | internal
BackupJob             {id: string; kind: BackupJobKind; trigger: BackupJobTrigger; state: BackupJobState;
                       step: BackupJobStep; queue_position: integer (0 = not waiting); bytes: integer;
                       snapshot: string|null; error_code: BackupJobError|null; error: string;
                       queued_at, started_at, finished_at: integer (0 = not yet)}
BackupJobResponse     {job: BackupJob|null}            (nur getBackupJob)
BackupJobStarted      {job: BackupJob}                 (202 von create/restore/delete)
BackupLastStatus      enum ok | failed | running
BackupScheduleMode    enum off | interval | daily | weekly
BackupSchedule        {mode: BackupScheduleMode; interval_hours, time_of_day, weekday,
                       keep_last, keep_daily, keep_weekly, keep_monthly: integer; timezone: string;
                       next_run_at: integer|null; last_run_at: integer|null;
                       last_status: BackupLastStatus|null; last_error: string|null;
                       last_snapshot_id: string|null}
BackupScheduleResponse {schedule: BackupSchedule}
UpdateBackupScheduleRequest {mode, interval_hours, time_of_day, weekday,
                       keep_last, keep_daily, keep_weekly, keep_monthly}   (alle Pflicht)
BackupOverview        {available: boolean; snapshots: BackupSnapshot[]; job: BackupJob|null; schedule: BackupSchedule}
CreateBackupRequest   {note?: string|null}
PinBackupRequest      {pinned: boolean}
BackupSnapshotResponse {snapshot: BackupSnapshot}
```

- `snapshot` im Job: Backup → neue Snapshot-ID nach `done`; Restore/Delete → Quell-ID.
- `getBackups` bei `available = false` oder nicht erreichbarem Dienst: `200` mit `available: false`, `snapshots: []` (kein Fehler), damit der Tab den Hinweis zeigen kann.
- Präzisierung zu Spec §5.8: Fehlt beim Restore der Platz, steht das im Job (`error_code: insufficient_storage`), nicht als HTTP-507, weil die Platzprüfung erst nach Stopp und Sicherheits-Snapshot im Dienst läuft.

### V4 – panel-intern (Aufgaben 3, 9–13)

```go
package backups // github.com/Bernotese/mc-panel/internal/backups

type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Notifier  notify.Notifier         // nil = notify.Nop{}
	Files     filesapi.BackupClient   // nil = no service: overview available=false, actions 503 files_unavailable
	Instances agentapi.InstancesClient // actual state + power stop; nil = treat as stopped, restore fails agent_unavailable
	Console   agentapi.ConsoleClient   // RCON; nil = running servers fail rcon_unavailable
	Reconcile func()                  // asks servers for an immediate reconcile pass (servers.Handler.Trigger); nil = no-op
	Log       *slog.Logger            // nil = slog.Default()
}

func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
// Run blocks until ctx is done: recovery (saveoff_since, running rows), ticker, queue worker; on
// ctx.Done it cancels the running service job, sends save-on if needed and returns within 20 s.
func (h *Handler) Run(ctx context.Context)
// DataJobRunning is true while the panel holds a restore job for the server (servers.DataGate).
func (h *Handler) DataJobRunning(ctx context.Context, serverID int64) bool
// PurgeBackups drops queued jobs of the server, cancels a running one and calls Files.PurgeBackups
// (servers.BackupPurger).
func (h *Handler) PurgeBackups(ctx context.Context, serverID int64) error
```

Phase A registriert alle acht Routen mit den Regeln aus V3; bis B2 antworten sie `503 backups_unavailable` („backups not implemented yet“), `getBackups` und `getBackupJob` ebenfalls; `Run` kehrt nach `<-ctx.Done()` zurück, `DataJobRunning` ist `false`, `PurgeBackups` `nil`. `cmd/panel/app.go`: `appDeps.Backups filesapi.BackupClient` (in `main.go` derselbe `*filesapi.UnixClient` wie `Files`), `backupsH := backups.NewHandler(...)`, `servers.Deps{Gate: servers.AnyGate(backupsH, filesH), Backups: backupsH}`, `Reconcile: serversH.Trigger` (nach `serversH` erzeugen; `backupsH` vorher anlegen und `Reconcile` per Closure `func() { serversH.Trigger() }` setzen), `backupsH.Routes(api)`, `backupsH.Run` in `run`. `cmd/panel/main.go` importiert `_ "time/tzdata"`.

### V5 – Dienst-Verhalten, das das panel voraussetzt (Aufgaben 4–8)

- `BackupsStatus` ist immer erreichbar (auch ohne restic); alle anderen `/v1/backups/*` außer `purge` ohne restic → `503 unavailable`.
- `CreateBackup` ohne `instances/<id>` → `404 not_found`; mit laufendem Job der Instanz oder vollem `max_backup_jobs` → `429 busy`; zu wenig Platz → `507`. Job läuft bis `done` (dann `Snapshot` gesetzt) oder `failed`.
- `RestoreBackup`: unbekannter Snapshot → `404`; zu wenig Platz → `507`; sonst `202`. Der Tausch beginnt erst nach vollständigem Entpacken; `CancelJob` wirkt bis dahin.
- `ForgetBackups` mit `Policy` berücksichtigt nur `kind=scheduled`, behält `pinned`; mit `Snapshots` löscht genau diese (unbekannte ID → `404` vor dem Job).
- `PinBackup`: nur `kind=scheduled` (sonst `400 invalid`), liefert den Snapshot mit neuer ID; laufender Job der Instanz → `429 busy`.
- `PurgeBackups` ohne Repo → `204`; laufender Job → `429 busy`. Purge braucht restic nicht: der Dienst löscht `repos/<id>` und `keys/<id>` über ein `os.Root` auf `backups/` auch dann, wenn restic fehlt (kein `503 unavailable`).
- `CreateBackup` prüft die Anfrage mit `CreateBackupRequest.Validate` (ID, Art, Notiz, Zeitzone) → `400 invalid`.
- `CancelJob` ohne laufenden Job → `204`; Extract-Job → `400 invalid`.
- `Job` liefert den letzten Job jeder Art (`Kind` gesetzt).
- Das panel bildet Dienst-Fehler im Job so ab: `ErrUnavailable` → `files_unavailable`, `ErrBackupsUnavailable` → `backups_unavailable`, `ErrInsufficientStorage` → `insufficient_storage`, `ErrTooLarge` → `too_large`, `ErrBusy` → `files_busy` (z. B. Entpacken läuft); ein `failed`-Dienst-Job wird über `Job.Code` genauso abgebildet (`too_large`, `insufficient_storage`, `busy` → `files_busy`, `unavailable` → `backups_unavailable`), sonst Job `failed` sonst → `internal` mit `job.error` als Text.

### V6 – MSW-Mock (Aufgabe 3, `web/src/mocks/handlers/backups.ts`)

- Pro Server im Speicher (`db.backups: Record<serverId, MockBackups>`): `survival` mit sechs Snapshots (vier geplante, davon einer angepinnt; ein manueller mit Notiz „vor Update auf 1.21.11“; ein Pre-Restore), Zeitplan `daily` 04:00, Retention 24/7/4/6, `last_status: ok`; `creative` ohne Snapshots, Zeitplan `off`, `last_status: failed` mit `last_error` „RCON nicht erreichbar“; `modded` mit `available: false`; `lobby` (ohne Instanz) → leere Übersicht.
- `createBackup` erzeugt einen Job, der über ~4 s `queued` → `saving` → `backing_up` → `resuming` → `done` geht und dann einen manuellen Snapshot anlegt; `restoreBackup` geht `stopping` → `pre_restore` → `restoring` → `done` und legt einen Pre-Restore-Snapshot an; `deleteBackup` `deleting` → `done`. Ein zweiter Job → `409 backup_running`.
- `pinBackup` vergibt eine neue zufällige 64-Hex-ID; nicht geplante → `400 validation_failed`.
- `restoreBackup` und `deleteBackup` verlangen Sudo wie die übrigen Sudo-Mocks; `downloadBackup` liefert eine kleine eingebettete Zip (Uint8Array) mit `Content-Type: application/zip` und einem Dateinamen `<server>-<YYYYMMDD-HHMMSS>.zip` in der panel-Zeitzone (`backup_timezone` der Settings), wie das panel.
- `updateBackupSchedule` validiert wie V2/Global Constraints und rechnet `next_run_at` im Browser wie das panel: `daily`/`weekly` nächster Termin ab jetzt, `interval` `last_run_at + N h`, falls das nicht in der Zukunft liegt `jetzt + N h`. Audit-Details wie das panel: create `{note, snapshot}`, restore `{snapshot, time, kind}`, pin/unpin `{snapshot: <alte ID>, new_snapshot: <neue ID>}`, schedule_update `{from, to}`.
- Rechte über `serverPermissions` (`server.backups`) wie die übrigen Server-Mocks; `ServerSettings`-Mock bekommt `backup_timezone: 'Europe/Berlin'` (PUT mit Sudo und Prüfung der Zone), `deleteServer` akzeptiert `purge_backups`.
