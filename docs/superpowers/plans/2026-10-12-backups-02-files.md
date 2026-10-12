# Teilprojekt 4 – Dienst `mcpanel-files`: restic-Backups, Restore, Forget/Pin/Purge, Download (Aufgaben 4–8) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der unprivilegierte Dienst `mcpanel-files` bekommt die Dienst-Seite von Spec 4 §3–4: restic als Kindprozess hinter einem Interface, `GET /v1/backups/status`, Backups als tar-Stream über `os.Root` (`restic backup --stdin`), Snapshot-Liste aus restic, Restore aus `restic dump` mit den Entpack-Regeln aus TP3b und Tausch der obersten Ebene, Forget (Policy oder IDs), Pin über restic-Tags, Purge, Zip-Download eines Snapshots und `POST /v1/jobs/cancel`. Dazu `files.toml`-Schlüssel und das Verzeichnis `backups/` in `ensureLayout` des agents.

**Architecture:** Alles folgt dem Muster von `internal/files`: jede Anfrage öffnet `<instances_dir>/instances/<id>` per `s.open` (`os.Root`), Pfade nur über `lstat`/`openRegular`/`openDir` (`O_NOFOLLOW|O_NONBLOCK`, `os.SameFile`). restic sieht das Datenverzeichnis nie: `writeBackupTar` geht es sortiert durch und schreibt PAX-tar in die stdin-Pipe von restic; `extractBackupTar` liest den tar aus `restic dump` und schreibt nur in einen frischen Temp-Ordner `.mcpanel-tmp-<token>-<zufall>/new`, danach tauscht `swapTop` per `rename` die oberste Ebene. Die Job-Verwaltung (`jobs.go`) wird verallgemeinert: ein Job pro Instanz über alle Arten (`extract|backup|restore|forget`), Entpack-Plätze (`maxJobsTotal = 2`) und restic-Plätze (`max_backup_jobs`) getrennt gezählt, eigener Kontext pro Job zum Abbrechen, „Commit“-Markierung für den Tausch. restic läuft über `resticRunner` (Produktion: `os/exec` mit leerer Umgebung, `SIGINT` bei Abbruch, `SIGKILL` nach 10 s), in Unit-Tests über einen In-Memory-Fake; Integrationstests mit echtem restic laufen nur mit `MCPANEL_TEST_RESTIC`.

**Tech Stack:** Go 1.27 (Standardbibliothek: `archive/tar`, `archive/zip`, `os/exec`, `encoding/base64`, `encoding/json`, `regexp`, `time/tzdata`), vorhandene Helfer aus `internal/files` und `internal/files/filesapi` (V1 aus Phase A), `github.com/BurntSushi/toml` (vorhanden). restic ≥ 0.17 als externes Binary (CI/VM 0.19.1). Tests nur Standardbibliothek. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (§3 Architektur, §4 Dienst, §8 Tests Zeile „Dienst“); für den Dienst selbst `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§3.2 Symlink-Regeln, §3.3 Temp-Namen, §4.1 Grenzen, §4.2 Entpacken).

## Ausführung dieser Phase

Es gelten alle Global Constraints, die Verträge (vor allem V1 und V5) und das Dateieigentum aus `docs/superpowers/plans/2026-10-12-backups-00-overview.md` sowie `CLAUDE.md`. Phase A (Aufgaben 1–3, Branch `feat/backups-contracts`) ist auf `main` gemerged, bevor diese Phase startet; damit installiert `scripts/vm-test.sh` restic 0.19.1 auf der Test-VM und setzt `MCPANEL_TEST_RESTIC`.

- **Branch und Worktree:** `git fetch origin && git worktree add ../mc-panel-wt/feat-backups-files -b feat/backups-files origin/main`; alle Befehle unten laufen in `../mc-panel-wt/feat-backups-files`.
- **Lokal nur `make check`** (gofmt, `go vet`, Build macOS + Linux, tsc). Neue Dateien vorher mit `git add -N internal/files internal/config internal/agent/instances cmd/files` vormerken, sonst prüft gofmt sie nicht.
- **Tests auf der Test-VM:** „Run: `go test …`“ heißt `make test-vm SUITE=go` (ganze Go-Suite) bzw. nach einem `make test-vm` gezielt `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-files && PATH=/usr/local/go/bin:$PATH MCPANEL_TEST_RESTIC=/usr/local/bin/restic go test ./internal/files -run <Name>'` (SSH braucht eine Bash ohne Sandbox). Vor jedem Push mindestens `SUITE=go` und `SUITE=lint`, vor „ready“ `make test-vm`. „Expected: FAIL“ bei einem Kompilierfehler sieht man schon in `make check`.
- **Draft-PR** beim ersten Push (Aufgabe 4), danach jede Aufgabe als eigener Commit auf demselben Branch. PR nicht selbst auf „ready“ setzen und nicht mergen; nach Aufgabe 8 dem Lead Bescheid geben.
- **Dateieigentum (B1):** `internal/files/**` **außer** `internal/files/filesapi/**`; `internal/config/files.go`, `internal/config/files_test.go`; `internal/agent/instances/datadir.go`, `internal/agent/instances/datadir_test.go`; `cmd/files/**`. `filesapi` wird nur benutzt. Passt etwas aus `filesapi` nicht (Name, Signatur, Verhalten), **nicht** ändern, sondern dem Lead melden.
- Auf der Test-VM nie deployen.

### Was Phase A in `filesapi` liefert (benutzt, nicht geändert)

V1 aus der Übersicht wörtlich, hier die Teile, die diese Phase braucht:

```go
const (
	PathBackupsStatus, PathBackupsList, PathBackupsCreate, PathBackupsRestore, PathBackupsForget,
	PathBackupsPin, PathBackupsDownload, PathBackupsPurge, PathJobsCancel // "/v1/backups/…", "/v1/jobs/cancel"
	MinResticVersion = "0.17.0"
	MaxNoteRunes     = 64
	MaxForgetIDs     = 100
	CodeUnavailable  = "unavailable" // 503, in der codes-Tabelle
)
var ErrBackupsUnavailable = errors.New(CodeUnavailable)

type JobKind string // JobExtract "extract" | JobBackup "backup" | JobRestore "restore" | JobForget "forget"
type SnapshotKind string // SnapshotScheduled "scheduled" | SnapshotManual "manual" | SnapshotPreRestore "pre-restore"
func (k SnapshotKind) Valid() bool
type Snapshot struct{ ID, ShortID string; Time int64; Kind SnapshotKind; Pinned bool; Note string; Bytes int64 }
type BackupsStatus struct{ Available bool; Version, Reason string }
type IDRequest struct{ ID int64 }
type CreateBackupRequest struct{ ID int64; Kind SnapshotKind; Note string }
type SnapshotRequest struct{ ID int64; Snapshot string }
type ForgetPolicy struct{ KeepLast, KeepDaily, KeepWeekly, KeepMonthly int }
func (p ForgetPolicy) Validate() error
type ForgetRequest struct{ ID int64; Snapshots []string; Policy *ForgetPolicy; Timezone string }
func (r ForgetRequest) Validate() error // genau eins von Snapshots (1..MaxForgetIDs) / Policy (+ Timezone)
type PinRequest struct{ ID int64; Snapshot string; Pinned bool }
type SnapshotsResponse struct{ Snapshots []Snapshot }
type SnapshotResponse struct{ Snapshot Snapshot }
func ValidSnapshotID(s string) bool
func ValidNote(s string) bool
// Job (types.go) hat zusätzlich: Kind JobKind, Snapshot string, Skipped int64.
// (*UnixClient) implementiert BackupClient (in den e2e-Tests dieser Phase benutzt).
```

Vorausgesetzt (Phase A): `ForgetPolicy.Validate`, `ForgetRequest.Validate` und `ValidNote` liefern Fehler, die `filesapi.ErrInvalid` wrappen; `CodeOf(ErrBackupsUnavailable)` = `("unavailable", 503)`. Fehlt etwas davon nach dem Merge von Phase A: dem Lead melden (nicht nachbauen).

### Verbindliche Werte dieser Phase

| Thema | Wert |
|---|---|
| `files.toml` | `restic_binary` (Default `/usr/bin/restic`, Dev `restic`); `[limits]` `max_backup_bytes = 68719476736`, `max_backup_entries = 1000000`, `max_backup_jobs = 1`; Prüfung: `restic_binary` ohne Dev eine saubere absolute Pfadangabe, mit Dev nicht leer (ohne `/` → `exec.LookPath`, sonst sauber absolut); `max_backup_bytes`/`max_backup_entries` > 0, `max_backup_jobs` 1–8 |
| Layout | `<instances_dir>/backups` `mcpanel-mc` (UID/GID aus `instances.Config`) `0700` vom agent (`ensureLayout`, `ensureOwnedDir` über `O_NOFOLLOW`-Descriptor, Symlink → Fehler); Dev-Dienst legt `backups/` selbst an (`0700`); `repos/`, `keys/` legt der Dienst an (`0700`, `Lstat`-Prüfung: echter Ordner, kein Symlink) |
| Schlüssel | `backups/keys/<id>`: 32 Zufallsbytes, `base64.StdEncoding`, ohne Zeilenumbruch, `O_WRONLY\|O_CREATE\|O_EXCL\|O_NOFOLLOW`, `0600`; vorhandener Schlüssel wird nie überschrieben; Repo vorhanden, Schlüssel fehlt → Job `failed` „the key of this server's backup repository is missing (keys/<id>)“ |
| restic-Umgebung | genau `HOME=<backups>`, `RESTIC_PROGRESS_FPS=1`, mit Server zusätzlich `RESTIC_REPOSITORY=<backups>/repos/<id>`, `RESTIC_PASSWORD_FILE=<backups>/keys/<id>`; `TZ=<zone>` nur bei `forget` mit Policy; `cmd.Dir = "/"`; keine geerbten Variablen |
| restic-argv | immer `--no-cache` vorn; `version`; `init`; `unlock`; `--json backup --stdin --stdin-filename data.tar --host mcpanel --tag mcpanel --tag kind=<kind> [--tag note=<base64url ohne Padding>]`; `--json --no-lock snapshots --host mcpanel --tag mcpanel`; `dump <id> /data.tar`; `--json tag --add pinned <id>` bzw. `--remove pinned`; `forget --prune --max-unused 10% <ids…>` bzw. `forget --prune --max-unused 10% --host mcpanel --tag kind=scheduled --keep-tag pinned --group-by host [--keep-last N] [--keep-daily N] [--keep-weekly N] [--keep-monthly N]` (Werte 0 weggelassen) |
| restic-Prozess | Abbruch über `ctx` → `SIGINT`, nach `resticGrace = 10 s` `SIGKILL` (`cmd.Cancel`, `cmd.WaitDelay`); stderr: letzte 4 KiB (`tailBuffer`); stdout kleiner Aufrufe ≤ 16 MiB; Version: `restic version` mit Frist 10 s, Regex `^restic (\d+)\.(\d+)\.(\d+)`, ≥ `filesapi.MinResticVersion` |
| Exit-Codes | 10 → `ErrNotFound` („backup repository does not exist“), 11 → `ErrBusy` („the backup repository is locked“), 12 → interner Fehler „the key does not match the backup repository“, sonst „restic <op> failed: <letzte stderr-Zeile>“ (≤ 300 Bytes, `message` aus einer JSON-Zeile, durch `publicMessage`); volles stderr ins Log (`Warn`) |
| Backup-Stream | sortierte Tiefensuche über `os.Root` (`readDirSorted`, ein offener Ordner zur Zeit); PAX, nur `TypeDir` (Name mit `/`) und `TypeReg`, Name relativ, `Mode = perm`, `ModTime`, `Uid/Gid = 0`, leere Namen; ausgelassen und in `skipped` gezählt: Symlinks, Sonderdateien, verborgene Einträge, Namen ≠ `CleanPath`, während des Durchlaufs getauschte/verschwundene Einträge, nicht lesbares/zu großes `server.properties`; `server.properties` (oben) geschwärzt (`redactProperties`); Datei schrumpft → mit Nullen bis zur `Stat`-Größe aufgefüllt, wächst → nach der `Stat`-Größe abgeschnitten |
| Backup-Grenzen | > `max_backup_entries` Einträge oder > `max_backup_bytes` Inhalt → `ErrTooLarge`; vorher freier Platz auf `backups/` ≥ `reserve_bytes`, sonst `507`; bei jedem Fehler wird restic **erst getötet, dann** stdin geschlossen (kein Snapshot aus einem abgeschnittenen Stream) |
| Restore | Vorprüfung synchron: Snapshot im Repo (`404`), `s.reserve(id, snapshot.Bytes)` (`507`); Entpacken nach `<tmp>/new` mit: `CleanPath(name) == name`, nicht verborgen, nur `TypeDir`/`TypeReg` (sonst `invalid`), keine doppelten Namen, Datei-Größe exakt (`invalid` bei abgeschnittenem Stream), Summen gegen `max_backup_*`, Dateien `0644` bzw. `0755` bei gesetztem Ausführbit, Ordner `0755`, mtime aus dem tar (Ordner zuletzt, tiefste zuerst); dann `beginCommit`, `swapTop`, `RemoveAll(<tmp>)` |
| Tausch | nur `rename` über `os.Root`: alle sichtbaren Einträge oben → `<tmp>/old/`, dann alle Einträge aus `<tmp>/new/` nach oben; verborgene Einträge (`.rcon-cli.*`, `.mcpanel-*`) bleiben; Fehler → Rückabwicklung der erfolgten Umbenennungen in umgekehrter Reihenfolge (best effort), Job `failed` |
| Jobs | ein Job (oder Vorprüfung oder Sperre) pro Instanz über alle Arten, `429 busy`; Entpacken ≤ `maxJobsTotal = 2`, restic-Jobs ≤ `max_backup_jobs`; `forget` ist abgelehnt, solange ein Backup-Download der Instanz läuft, und ein Download, solange ein `forget` läuft (`429 busy`); `pin`/`purge` brauchen eine Instanz ohne Job und ohne Download |
| Abbrechen | `POST /v1/jobs/cancel {id}`: kein laufender Job → `204`; Extract → `400 invalid`; Backup/Forget → Kontext abgebrochen; Restore nur bis `beginCommit`; Fehlertext eines abgebrochenen Jobs „the job was canceled“, beim Beenden des Dienstes „the files service is shutting down“ |
| Download | Vorprüfungen (Snapshot, Download-Sperre, einer der 4 Zip-Plätze) vor dem ersten Byte; `start` erst beim ersten tar-Eintrag (ein sofort scheiterndes `dump` wird noch als JSON beantwortet); Zip mit denselben Namensregeln wie der Ordner-Download (`zipSafeName` je Komponente), ausgelassene Ordner samt Inhalt; Grenzen `max_backup_entries` / 16 MiB Namen / `max_backup_bytes` |
| HTTP | alle Bodies ≤ `filesapi.MaxSmallJSONBytes`, `DisallowUnknownFields`; `list`, `restore`, `forget`, `pin`, `purge` laufen über `longOp` (restic liest den Index); `status` immer, alle anderen `/v1/backups/*` ohne restic `503 unavailable` |

## Review Focus (aus der Übersicht, in dieser Phase)

- **#2 Feindliches Datenverzeichnis beim Backup:** Ordner wird während des Durchlaufs gegen einen Symlink auf die Nachbar-Instanz getauscht, Datei gegen einen Symlink getauscht, FIFO, Socket, Namen mit Steuerzeichen bzw. ungültigem UTF-8 (nur Linux), Datei schrumpft/wächst, > `max_backup_entries` Einträge, > `max_backup_bytes`; erwartet: nichts außerhalb von `instances/<id>` im tar, keine Hänger, Grenzen greifen, `skipped` zählt, kein Snapshot bei Grenzüberschreitung. → `TestBackupTarSwappedFolderStaysInside`, `TestBackupTarSwappedFileIsSkipped`, `TestBackupTarSkipsSpecialFiles` (Linux), `TestBackupTarBadNames` (Linux), `TestBackupTarShrinkingFile`, `TestBackupTarLimits`, `TestBackupTooLargeLeavesNoSnapshot` (Aufgabe 5).
- **#3 Feindlicher oder kaputter Snapshot beim Restore:** tar mit Symlink-, Hardlink-, Geräte-, FIFO-, Sparse-Einträgen, `../x`, `/abs`, `a//b`, verborgenen Namen (`.mcpanel-x`, `.rcon-cli.env`), doppelten Namen, Datei als Elternteil eines Eintrags, abgeschnittenem Stream, falscher Größe, Grenzüberschreitung; erwartet: Job `failed`, Datenverzeichnis byte-gleich, kein Temp-Rest, nichts außerhalb des Temp-Ordners. → `TestExtractBackupTarRefuses` (tabellarisch), `TestRestoreHostileSnapshotLeavesDataAlone` (Aufgabe 6).
- **#5 Betriebsfehler:** Platte voll vor Backup und Restore (`507`), restic fehlt/zu alt/Versionsabfrage hängt, verwaiste Sperre nach `kill -9`, Schlüssel fehlt, Repo ohne Snapshots, Fehlertexte ohne Host-Pfade. → `TestInitBackupsStatus`, `TestBackupNeedsFreeSpace`, `TestRestoreNeedsFreeSpace`, `TestBackupMissingKeyFails`, `TestSnapshotsWithoutRepo` (Aufgaben 4–6), `TestResticStaleLockIsRemoved`, `TestResticMissingKey`, `TestResticErrorsHideHostPaths` (Aufgabe 8).
- **#4 Gleichzeitigkeit (Dienst-Seite):** Backup während Entpacken (und umgekehrt) → `429 busy`; zweites Backup derselben Instanz → `429`; Download während Forget und umgekehrt → `429`; Pin während eines Jobs → `429`; Abbrechen eines Restores nach Beginn des Tauschs bleibt wirkungslos; `GET /v1/jobs` antwortet immer sofort. → `TestJobsOnePerInstanceAcrossKinds`, `TestJobsResticSlots`, `TestDownloadAndForgetExclude`, `TestCancelRestoreAfterCommitIsIgnored` (Aufgaben 5–7).

## Abweichungen und Präzisierungen

- **Zeitzone der Retention (Vertragsproblem, dem Lead gemeldet):** restic ordnet Snapshots für `--keep-daily/weekly/monthly` nach `Time.Year()/Month()/Day()` der *gespeicherten* Snapshot-Zeit ein (`internal/data/snapshot_policy.go`, restic 0.19.1); die gespeicherte Zeit trägt die Zeitzone des **Backup**-Prozesses. `TZ` beim `forget` (V1, Spec §3.3) ändert die Tagesgrenzen daher nicht. Richtig wäre `TZ` auch beim `backup` — dafür fehlt in V1 `CreateBackupRequest.Timezone`. **Vom Lead entschieden:** V1 hat jetzt `CreateBackupRequest.Timezone`; der Dienst setzt `TZ` per `tzEnv` beim `backup` (Aufgabe 5) und beim `forget` (Aufgabe 7). Aufgabe 8 prüft im Integrationstest, dass die Snapshot-Zeit die Zone trägt (`restic snapshots --json` → `time` mit dem Offset der Zone).
- **`ForgetBackups` mit IDs liest vorab die Snapshot-Liste** (unbekannte ID → `404`, V5). Das dauert bei großen Repos länger als `CallTimeout` (10 s), den V1 für `ForgetBackups` vorsieht; der Handler läuft trotzdem über `longOp`. Empfehlung an den Lead: `ForgetBackups` im Client mit `BackupCallTimeout`.
- **`purge` ohne restic → `503 unavailable`** wie alle anderen `/v1/backups/*` (Spec §3.2), obwohl Purge restic nicht bräuchte.
- **Verborgene Einträge zählen in `skipped`** (Spec §4.2 nennt sie in derselben Liste); `.rcon-cli.env` ist daher fast immer dabei.
- **Sortierte Tiefensuche statt `walkTree`:** `walkTree` liefert die Verzeichnisreihenfolge; Spec §4.2 verlangt eine feste, sortierte Reihenfolge (stabile tar-Streams deduplizieren besser). `readDirSorted` liest einen Ordner vollständig (höchstens `max_backup_entries + 1` Namen), schließt ihn und steigt dann ab – wie `walkTree` ist höchstens ein Ordner offen.
- **Pin liest die neue ID aus der JSON-Ausgabe von `restic tag`** (`message_type: changed`, `new_snapshot_id`, restic 0.19.1 `cmd_tag.go`); nur ohne diese Zeile (ältere restic-Version) sucht es wie in Spec §3.3 über gleiche Zeit und Art. Ist der Tag schon im gewünschten Zustand, ruft der Dienst restic gar nicht auf und die ID bleibt.
- **Forget mit Policy ohne Repo** ist ein Job, der sofort `done` endet (nichts zu tun); mit IDs → `404 not_found`.
- **Restore-Rückabwicklung:** Spec §4.3 lässt den Tausch „ohne Abbruchpunkt durchlaufen“; scheitert ein `rename` mittendrin (z. B. `EXDEV` nach einem fremden Mount), macht `swapTop` die erfolgten Umbenennungen rückgängig, bevor der Job `failed` meldet. Ein Absturz im Tausch bleibt wie in der Spec (Pre-Restore-Snapshot des panels).
- **Neue Dateien über die Übersicht hinaus:** `server_backups.go` (Handler der neuen Routen, damit `server.go` klein bleibt), `restic_fake_test.go` (In-Memory-restic für Unit-Tests), `restic_exec_test.go` (Shell-Skript als Fake-Binary für `execRunner`).

## Dateistruktur

```
internal/config/files.go, files_test.go          (4)  restic_binary, max_backup_* + Validierung
internal/agent/instances/datadir.go, _test.go    (4)  BackupsDir, ensureOwnedDir, ensureLayout
internal/files/restic.go                         (4)  resticRunner, resticCmd, resticProc, execRunner, Version, Fehler
internal/files/backupsvc.go                      (4)  Status, Verzeichnisse, Schlüssel, Repo, restic-Aufruf-Helfer
internal/files/service.go                        (4)  Config.ResticBinary, Runner, fsErr; (5) Hooks
internal/files/server_backups.go                 (4)  backupRoutes + Handler (wächst bis 7)
internal/files/server.go                         (4)  backupRoutes einhängen
cmd/files/main.go                                (4)  ResticBinary, InitBackups, time/tzdata
internal/files/restic_fake_test.go               (4)  fakeRestic (bis 7 erweitert), newBackupService
internal/files/restic_exec_test.go               (4)  execRunner gegen ein Shell-Skript
internal/files/backupsvc_test.go                 (4)  Status, Verzeichnisse
internal/files/helpers_test.go                   (4)  testLimits um max_backup_*
internal/files/jobs.go                           (5)  Arten, Plätze, Sperren; (6) Abbrechen, Commit; (7) Leser
internal/files/extract.go                        (5)  neue jobs-Signaturen
internal/files/tarwrite.go, tarwrite_test.go     (5)  Backup-Stream
internal/files/tarwrite_linux_test.go            (5)  FIFO, Socket, ungültige Namen
internal/files/backup.go, backup_test.go         (5)  CreateBackup, Snapshots, Tags
internal/files/jobs_test.go                      (5)  Job-Arten und Plätze
internal/files/tarextract.go, tarextract_test.go (6)  Restore-Stream
internal/files/restore.go, restore_test.go       (6)  RestoreBackup, dump, swapTop, CancelJob
internal/files/forget.go, forget_test.go         (7)  ForgetBackups, PinBackup, PurgeBackups
internal/files/tarzip.go, tarzip_test.go         (7)  DownloadBackup, tar→Zip
internal/files/server_backups_test.go            (4–7) HTTP-Ebene
internal/files/restic_integration_test.go        (8)  echtes restic (MCPANEL_TEST_RESTIC)
internal/files/backups_e2e_test.go               (8)  filesapi.UnixClient über den Unix-Socket
```

---

### Task 4: Konfiguration, Layout, restic-Ausführung und Status

**Files:**
- Modify: `internal/config/files.go`, `internal/config/files_test.go`
- Modify: `internal/agent/instances/datadir.go`, `internal/agent/instances/datadir_test.go`
- Create: `internal/files/restic.go`, `internal/files/backupsvc.go`, `internal/files/server_backups.go`
- Modify: `internal/files/service.go`, `internal/files/server.go`, `internal/files/helpers_test.go`, `internal/files/service_test.go`
- Modify: `cmd/files/main.go`
- Test: `internal/files/restic_exec_test.go`, `internal/files/restic_fake_test.go`, `internal/files/backupsvc_test.go`, `internal/files/server_backups_test.go`

**Interfaces:**
- Consumes: `filesapi.BackupsStatus`, `filesapi.MinResticVersion`, `filesapi.ErrBackupsUnavailable`, `filesapi.PathBackupsStatus`, vorhandene `s.publicMessage`, `writeJSON`, `s.fail`, `cleanAbsPath` (config), `ensureDir`/`openDirNoFollow` (agent).
- Produces:

```go
// internal/config
type FilesLimits struct { …; MaxBackupBytes int64 `toml:"max_backup_bytes"`; MaxBackupEntries int64 `toml:"max_backup_entries"`; MaxBackupJobs int `toml:"max_backup_jobs"` }
type Files struct { …; ResticBinary string `toml:"restic_binary"` }

// internal/agent/instances
func BackupsDir(instancesDir string) string // <instances_dir>/backups

// internal/files
type Config struct { …; ResticBinary string; runner resticRunner /* tests */ }
type resticRunner interface{ Start(ctx context.Context, c resticCmd) (resticProc, error) }
type resticCmd struct{ Args, Env []string; Stdin bool; Stdout io.Writer }
type resticProc interface{ Stdin() io.WriteCloser; Kill(); Wait() error }
type resticExit struct{ Code int; Stderr string } // Code -1 = killed by a signal
func (s *Service) InitBackups(ctx context.Context) filesapi.BackupsStatus
func (s *Service) BackupsStatus() filesapi.BackupsStatus
func (s *Service) requireBackups(id int64) error
func (s *Service) backupsDir() string
func (s *Service) repoDir(id int64) string
func (s *Service) keyFile(id int64) string
func (s *Service) repoExists(id int64) bool
func (s *Service) ensureRepo(ctx context.Context, id int64) error
func (s *Service) resticEnv(id int64, extra ...string) []string
func (s *Service) restic(ctx context.Context, id int64, extraEnv []string, args ...string) ([]byte, error)
func (s *Service) resticErr(op string, err error) error
func (s *Server) backupRoutes() map[string]route
// tests: newBackupService(t, mod...) (*Service, string, *fakeRestic), (*fakeRestic) Start
```

- [ ] **Step 1: Konfiguration – failing tests**

In `internal/config/files_test.go` anhängen:

```go
func TestFilesBackupDefaults(t *testing.T) {
	f := config.DefaultFiles()
	if f.ResticBinary != "/usr/bin/restic" {
		t.Fatalf("restic_binary = %q", f.ResticBinary)
	}
	l := f.Limits
	if l.MaxBackupBytes != 64<<30 || l.MaxBackupEntries != 1_000_000 || l.MaxBackupJobs != 1 {
		t.Fatalf("backup limits = %+v", l)
	}
	if d := config.DevFiles(t.TempDir()); d.ResticBinary != "restic" {
		t.Fatalf("dev restic_binary = %q", d.ResticBinary)
	}
}

func TestFilesBackupValidation(t *testing.T) {
	ok := config.DefaultFiles()
	ok.PanelUID = 998
	for _, c := range []struct {
		name string
		mod  func(*config.Files)
		want string // "" = valid
	}{
		{"defaults", func(*config.Files) {}, ""},
		{"relative binary", func(f *config.Files) { f.ResticBinary = "restic" }, "restic_binary"},
		{"unclean binary", func(f *config.Files) { f.ResticBinary = "/usr/bin/../bin/restic" }, "restic_binary"},
		{"empty binary", func(f *config.Files) { f.ResticBinary = "" }, "restic_binary"},
		{"dev name", func(f *config.Files) { f.Dev = true; f.ResticBinary = "restic" }, ""},
		{"dev empty", func(f *config.Files) { f.Dev = true; f.ResticBinary = "" }, "restic_binary"},
		{"dev relative path", func(f *config.Files) { f.Dev = true; f.ResticBinary = "bin/restic" }, "restic_binary"},
		{"bytes", func(f *config.Files) { f.Limits.MaxBackupBytes = 0 }, "max_backup_bytes"},
		{"entries", func(f *config.Files) { f.Limits.MaxBackupEntries = -1 }, "max_backup_entries"},
		{"jobs low", func(f *config.Files) { f.Limits.MaxBackupJobs = 0 }, "max_backup_jobs"},
		{"jobs high", func(f *config.Files) { f.Limits.MaxBackupJobs = 9 }, "max_backup_jobs"},
	} {
		f := ok
		c.mod(&f)
		err := f.Validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want an error about %s", c.name, err, c.want)
		}
	}
}

func TestLoadFilesBackupKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "files.toml")
	src := "panel_uid = 998\nrestic_binary = \"/usr/local/bin/restic\"\n[limits]\nmax_backup_jobs = 2\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := config.LoadFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.ResticBinary != "/usr/local/bin/restic" || f.Limits.MaxBackupJobs != 2 || f.Limits.MaxBackupEntries != 1_000_000 {
		t.Fatalf("loaded = %+v", f)
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`f.ResticBinary undefined`, `l.MaxBackupBytes undefined`).

- [ ] **Step 3: Konfiguration implementieren**

In `internal/config/files.go`:

```go
// FilesLimits are the limits of the files service (spec 3b §3.1, spec 4 §3.2).
type FilesLimits struct {
	MaxUploadBytes    int64 `toml:"max_upload_bytes"`
	MaxExtractBytes   int64 `toml:"max_extract_bytes"`
	MaxExtractEntries int64 `toml:"max_extract_entries"`
	ReserveBytes      int64 `toml:"reserve_bytes"`
	MaxTextBytes      int64 `toml:"max_text_bytes"`
	MaxListEntries    int   `toml:"max_list_entries"`
	MaxBackupBytes    int64 `toml:"max_backup_bytes"`   // content of one snapshot (tar)
	MaxBackupEntries  int64 `toml:"max_backup_entries"` // entries of one snapshot
	MaxBackupJobs     int   `toml:"max_backup_jobs"`    // concurrent restic jobs (backup, restore, forget)
}

// maxBackupJobsLimit bounds max_backup_jobs: every restic job holds its own memory and disk I/O.
const maxBackupJobsLimit = 8
```

`Files` bekommt nach `InstancesDir`:

```go
	// ResticBinary is the restic executable (spec 4 §3.2): an absolute path; with dev also a
	// bare name that is looked up in PATH.
	ResticBinary string      `toml:"restic_binary"`
```

`defaultFilesLimits` ergänzt `MaxBackupBytes: 64 << 30, MaxBackupEntries: 1_000_000, MaxBackupJobs: 1`; `DefaultFiles` setzt `ResticBinary: "/usr/bin/restic"`; `DevFiles` setzt `f.ResticBinary = "restic"`.

In `Validate` nach der `instances_dir`-Prüfung:

```go
	if err := checkResticBinary(f.ResticBinary, f.Dev); err != nil {
		errs = append(errs, err)
	}
```

Die Schleife über die `> 0`-Grenzen bekommt `"max_backup_bytes": l.MaxBackupBytes, "max_backup_entries": l.MaxBackupEntries`; danach:

```go
	if l.MaxBackupJobs < 1 || l.MaxBackupJobs > maxBackupJobsLimit {
		errs = append(errs, fmt.Errorf("limits.max_backup_jobs must be between 1 and %d", maxBackupJobsLimit))
	}
```

und am Dateiende:

```go
// checkResticBinary: in production a clean absolute path; with dev also a bare command name
// (looked up in PATH by the service).
func checkResticBinary(p string, dev bool) error {
	if dev && p != "" && !strings.ContainsRune(p, '/') && !strings.ContainsRune(p, 0) {
		return nil
	}
	return cleanAbsPath("restic_binary", p)
}
```

- [ ] **Step 4: Run, expect PASS**

Run: `make check`, dann `make test-vm SUITE=go` (bzw. gezielt `go test ./internal/config -run 'TestFiles|TestLoadFiles|TestDefaultFiles|TestDevFiles'`)
Expected: PASS.

- [ ] **Step 5: agent-Layout – failing test**

In `internal/agent/instances/datadir_test.go` anhängen:

```go
// TestInitCreatesBackupsDir: backups/ belongs to the container user (the files service runs as
// that user and owns the restic repositories) with 0700; a loosened mode is repaired, and a
// symlink in its place is refused, never followed (spec 4 §3.1).
func TestInitCreatesBackupsDir(t *testing.T) {
	e := newEnv(t)
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	dir := instances.BackupsDir(e.dir)
	if dir != filepath.Join(e.dir, "backups") {
		t.Fatalf("BackupsDir = %s", dir)
	}
	if uid, gid, mode := owner(t, dir); uid != os.Getuid() || gid != os.Getgid() || mode != 0o700 {
		t.Fatalf("backups/ = %d:%d %v, want %d:%d 0700", uid, gid, mode, os.Getuid(), os.Getgid())
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, dir); got != 0o700 {
		t.Fatalf("backups/ after repair = %v, want 0700", got)
	}
}

func TestInitRefusesSymlinkedBackupsDir(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, instances.BackupsDir(e.dir)); err != nil {
		t.Fatal(err)
	}
	if err := e.m.Init(); err == nil {
		t.Fatal("Init followed a symlinked backups/")
	}
	if got := perm(t, target); got != 0o755 {
		t.Fatalf("symlink target mode changed to %v", got)
	}
}
```

(`newEnv` setzt `UID: os.Getuid(), GID: os.Getgid()`; als Nicht-root darf der Test nur auf sich selbst `chown`en, deshalb prüft er genau diese Werte.)

- [ ] **Step 6: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`undefined: instances.BackupsDir`).

- [ ] **Step 7: agent-Layout implementieren**

In `internal/agent/instances/datadir.go`:

```go
// BackupsDir is <instances_dir>/backups: the restic repositories and their keys (spec 4 §3.1),
// owned by the container user with 0700. No container sees it; the agent never looks inside.
func BackupsDir(instancesDir string) string { return filepath.Join(instancesDir, "backups") }
```

`ensureLayout` wird zu:

```go
// ensureLayout makes sure <instances_dir> exists (0755), that instances/ is root:<mc_user gid>
// 0750, deleted/ root:root 0700 and backups/ <mc_user>:<mc_user gid> 0700 (in production). The
// panel user must not read world data; the files service (running as mc_user, spec 3b §3.3)
// may enter instances/ but never write into it and owns backups/ (spec 4 §3.1); no container
// user may swap either directory.
func (m *Manager) ensureLayout() error {
	if err := os.MkdirAll(m.host.InstancesDir, 0o755); err != nil { //nolint:gosec // G301: spec §3.3, root:root 0755; contains no secrets itself
		return err
	}
	if err := ensureDir(filepath.Join(m.host.InstancesDir, "instances"), m.host.GID, 0o750); err != nil {
		return err
	}
	if err := ensureDir(DeletedDir(m.host.InstancesDir), os.Getegid(), 0o700); err != nil {
		return err
	}
	return ensureOwnedDir(BackupsDir(m.host.InstancesDir), m.host.UID, m.host.GID, 0o700)
}
```

und nach `ensureDir`:

```go
// ensureOwnedDir is ensureDir for a directory that belongs to another user (uid:gid): created
// if needed, then mode and owner are enforced through an O_NOFOLLOW descriptor (a symlink in
// its place is refused). The mode is set before the owner, like ensureDir.
func ensureOwnedDir(dir string, uid, gid int, perm os.FileMode) error {
	if err := os.Mkdir(dir, perm); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	f, err := openDirNoFollow(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	if err := f.Chown(uid, gid); err != nil {
		return fmt.Errorf("chown %s to %d:%d: %w", dir, uid, gid, err)
	}
	return nil
}
```

- [ ] **Step 8: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test ./internal/agent/instances -run 'TestInit'`)
Expected: PASS, auch die vorhandenen `TestInitRepairsExistingLayout`/`TestInstancesDirBelongsToMCGroup`.

- [ ] **Step 9: restic-Ausführung – failing tests (Fake-Binary)**

`internal/files/restic_exec_test.go`:

```go
package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeBinary writes a shell script that stands in for restic: it records its argv and
// environment next to itself and then acts according to its first argument after the record
// prefix: "cat" copies stdin to stdout, "sleep" waits for SIGINT (exit 130), "fail" exits 12.
func fakeBinary(t *testing.T) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "rec")
	bin = filepath.Join(dir, "restic")
	script := `#!/bin/sh
printf '%s\n' "$@" > "` + record + `.args"
env > "` + record + `.env"
case "$1" in
  cat) cat ;;
  sleep) trap 'echo interrupted >&2; exit 130' INT; while :; do sleep 0.05; done ;;
  fail) echo "Fatal: wrong password" >&2; exit 12 ;;
  version) echo "restic 0.19.1 compiled with go1.25.1 on linux/amd64" ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // G306: test script below t.TempDir()
		t.Fatal(err)
	}
	return bin, record
}

func TestExecRunnerArgsEnvAndStdout(t *testing.T) {
	bin, rec := fakeBinary(t)
	var out bytes.Buffer
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{
		Args: []string{"cat", "--no-cache"}, Env: []string{"HOME=/h", "RESTIC_PROGRESS_FPS=1"}, Stdin: true, Stdout: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stdin().Write([]byte("data.tar bytes")); err != nil {
		t.Fatal(err)
	}
	if err := p.Stdin().Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if out.String() != "data.tar bytes" {
		t.Fatalf("stdout = %q", out.String())
	}
	args, _ := os.ReadFile(rec + ".args")
	if string(args) != "cat\n--no-cache\n" {
		t.Fatalf("args = %q", args)
	}
	env, _ := os.ReadFile(rec + ".env")
	// Only our variables plus what /bin/sh adds itself.
	for _, line := range strings.Split(strings.TrimSpace(string(env)), "\n") {
		k, _, _ := strings.Cut(line, "=")
		if !slices.Contains([]string{"HOME", "RESTIC_PROGRESS_FPS", "PWD", "SHLVL", "_", "OLDPWD"}, k) {
			t.Errorf("unexpected variable in the restic environment: %s", line)
		}
	}
	if !strings.Contains(string(env), "HOME=/h\n") || !strings.Contains(string(env), "PWD=/\n") {
		t.Fatalf("env = %s", env)
	}
}

func TestExecRunnerExitCodeAndStderr(t *testing.T) {
	bin, _ := fakeBinary(t)
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{Args: []string{"fail"}})
	if err != nil {
		t.Fatal(err)
	}
	var ex *resticExit
	if err := p.Wait(); !errors.As(err, &ex) || ex.Code != 12 || !strings.Contains(ex.Stderr, "wrong password") {
		t.Fatalf("wait = %v, want exit 12 with stderr", err)
	}
}

func TestExecRunnerCancelSendsInterrupt(t *testing.T) {
	bin, _ := fakeBinary(t)
	ctx, cancel := context.WithCancel(context.Background())
	p, err := execRunner{bin: bin}.Start(ctx, resticCmd{Args: []string{"sleep"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // the trap is installed
	cancel()
	var ex *resticExit
	if err := p.Wait(); !errors.As(err, &ex) || ex.Code != 130 || !strings.Contains(ex.Stderr, "interrupted") {
		t.Fatalf("wait after cancel = %v, want exit 130 after SIGINT", err)
	}
}

func TestExecRunnerKill(t *testing.T) {
	bin, _ := fakeBinary(t)
	p, err := execRunner{bin: bin}.Start(context.Background(), resticCmd{Args: []string{"sleep"}, Stdin: true})
	if err != nil {
		t.Fatal(err)
	}
	p.Kill()
	var ex *resticExit
	if err := p.Wait(); !errors.As(err, &ex) || ex.Code != -1 {
		t.Fatalf("wait after kill = %v, want a signal exit (-1)", err)
	}
	_ = p.Stdin().Close() // after the process is gone: harmless
}

func TestParseResticVersion(t *testing.T) {
	for _, c := range []struct {
		out string
		v   [3]int
		ok  bool
	}{
		{"restic 0.19.1 compiled with go1.25.1 on linux/amd64\n", [3]int{0, 19, 1}, true},
		{"restic 0.17.0 compiled with go1.22.5 on darwin/arm64", [3]int{0, 17, 0}, true},
		{"restic 1.2.3-dev (compiled manually)", [3]int{1, 2, 3}, true},
		{"rustic 0.9.0", [3]int{}, false},
		{"", [3]int{}, false},
	} {
		v, ok := parseResticVersion(c.out)
		if ok != c.ok || v != c.v {
			t.Errorf("%q: %v %v, want %v %v", c.out, v, ok, c.v, c.ok)
		}
	}
	if !versionAtLeast([3]int{0, 17, 0}, "0.17.0") || versionAtLeast([3]int{0, 16, 4}, "0.17.0") || !versionAtLeast([3]int{1, 0, 0}, "0.17.0") {
		t.Fatal("versionAtLeast")
	}
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := &tailBuffer{max: 8}
	_, _ = b.Write([]byte("0123456789"))
	_, _ = b.Write([]byte("ab"))
	if b.String() != "456789ab" {
		t.Fatalf("tail = %q", b.String())
	}
}

func TestLastLine(t *testing.T) {
	for in, want := range map[string]string{
		"a\nFatal: repository is already locked\n\n": "Fatal: repository is already locked",
		`{"message_type":"exit_error","code":1,"message":"Fatal: no such snapshot"}`: "Fatal: no such snapshot",
		strings.Repeat("x", 400): strings.Repeat("x", 300),
		"":                       "",
	} {
		if got := lastLine(in); got != want {
			t.Errorf("lastLine(%.20q) = %.40q, want %.40q", in, got, want)
		}
	}
}
```

- [ ] **Step 10: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`undefined: execRunner`, `resticCmd`, …).

- [ ] **Step 11: `restic.go` implementieren**

```go
package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	resticStderrMax   = 4 << 10          // tail of stderr kept for logs and messages
	resticStdoutMax   = 16 << 20         // stdout of small calls (snapshots, tag, version)
	resticGrace       = 10 * time.Second // SIGINT, then SIGKILL (spec 4 §4.5)
	resticVersionWait = 10 * time.Second // spec 4 §3.2
	resticMessageMax  = 300              // bytes of restic's last line in an answer

	// restic exit codes (restic.readthedocs.io, "Scripting", since 0.17).
	exitNoRepo        = 10
	exitLocked        = 11
	exitWrongPassword = 12
)

// resticRunner starts restic processes: execRunner in production, fakeRestic in unit tests.
type resticRunner interface {
	Start(ctx context.Context, c resticCmd) (resticProc, error)
}

// resticCmd is one restic invocation.
type resticCmd struct {
	Args   []string  // without the binary; argv, never a shell
	Env    []string  // the complete environment (nothing is inherited)
	Stdin  bool      // Stdin() of the process is a pipe
	Stdout io.Writer // nil = discarded
}

// resticProc is a started restic process.
type resticProc interface {
	Stdin() io.WriteCloser // nil unless resticCmd.Stdin
	// Kill ends the process at once (SIGKILL). A stdin backup that is killed before its stdin
	// ends never creates a snapshot.
	Kill()
	// Wait waits for the exit; a non-zero exit is a *resticExit.
	Wait() error
}

// resticExit is a failed restic run. Code is -1 when a signal ended the process.
type resticExit struct {
	Code   int
	Stderr string // the last resticStderrMax bytes
}

func (e *resticExit) Error() string {
	return fmt.Sprintf("restic exited with code %d: %s", e.Code, lastLine(e.Stderr))
}

// execRunner runs the restic binary at bin.
type execRunner struct{ bin string }

func (r execRunner) Start(ctx context.Context, c resticCmd) (resticProc, error) {
	cmd := exec.CommandContext(ctx, r.bin, c.Args...) //nolint:gosec // G204: the binary comes from files.toml; argv is built by the service from validated ids and fixed flags, no shell
	cmd.Env = c.Env
	if cmd.Env == nil {
		cmd.Env = []string{} // never inherit the service's environment
	}
	cmd.Dir = "/"
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = resticGrace
	stderr := &tailBuffer{max: resticStderrMax}
	cmd.Stderr = stderr
	cmd.Stdout = c.Stdout
	p := &execProc{cmd: cmd, stderr: stderr}
	if c.Stdin {
		w, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		p.stdin = w
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return p, nil
}

type execProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer
}

func (p *execProc) Stdin() io.WriteCloser { return p.stdin }

func (p *execProc) Kill() { _ = p.cmd.Process.Kill() }

func (p *execProc) Wait() error {
	err := p.cmd.Wait()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee):
		return &resticExit{Code: ee.ExitCode(), Stderr: p.stderr.String()}
	case errors.Is(err, exec.ErrWaitDelay):
		return &resticExit{Code: -1, Stderr: p.stderr.String()}
	}
	return err
}

// tailBuffer keeps the last max bytes written to it. Safe for concurrent use.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// limitedBuffer collects at most max bytes; more is recorded as overflow.
type limitedBuffer struct {
	mu       sync.Mutex
	max      int
	b        []byte
	overflow bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.b)+len(p) > l.max {
		l.overflow = true
		p = p[:max(l.max-len(l.b), 0)]
	}
	l.b = append(l.b, p...)
	return len(p), nil
}

func (l *limitedBuffer) Bytes() []byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b
}

var versionRe = regexp.MustCompile(`^restic (\d+)\.(\d+)\.(\d+)`)

// parseResticVersion reads "restic X.Y.Z …" (the first line of `restic version`).
func parseResticVersion(out string) ([3]int, bool) {
	m := versionRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		v[i] = n
	}
	return v, true
}

// versionAtLeast compares v with a "X.Y.Z" minimum.
func versionAtLeast(v [3]int, minimum string) bool {
	m, ok := parseResticVersion("restic " + minimum)
	if !ok {
		return false
	}
	for i := range v {
		if v[i] != m[i] {
			return v[i] > m[i]
		}
	}
	return true
}

// lastLine returns the last non-empty line of restic's stderr, shortened to resticMessageMax
// bytes; for a JSON error line its "message".
func lastLine(stderr string) string {
	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	line := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			line = l
			break
		}
	}
	var msg struct {
		Message string `json:"message"`
	}
	if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &msg) == nil && msg.Message != "" {
		line = msg.Message
	}
	if len(line) > resticMessageMax {
		line = line[:resticMessageMax]
	}
	return line
}
```

- [ ] **Step 12: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test ./internal/files -run 'TestExecRunner|TestParseResticVersion|TestTailBuffer|TestLastLine'`)
Expected: PASS.

- [ ] **Step 13: Fake-restic und Status – failing tests**

`internal/files/restic_fake_test.go` (wird in Aufgaben 5–7 benutzt und erweitert; hier vollständig angelegt):

```go
package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRestic is an in-memory restic for unit tests: one repository per RESTIC_REPOSITORY. It
// understands exactly the commands the service sends (spec 4 §3.3) and records every call.
type fakeRestic struct {
	mu      sync.Mutex
	version string                 // first line of "restic version"
	repos   map[string]*fakeRepo   // by RESTIC_REPOSITORY
	calls   []fakeCall             // every Start
	fail    map[string]*resticExit // command → exit instead of running it
	next    time.Time              // time of the next snapshot (+1 min per backup)
	// stdinRead, if set, receives the number of bytes a backup read before it ended (EOF or kill).
	stdinRead chan int
	// sawEOF records per backup whether stdin ended (true) or the process was killed first.
	sawEOF []bool
	// gate, if set, is waited on by every backup before it reads stdin and by every dump
	// before it writes (tests: a job that "runs").
	gate chan struct{}
}

type fakeCall struct{ Args, Env []string }

type fakeRepo struct{ snaps []*fakeSnap }

type fakeSnap struct {
	ID   string
	Time time.Time
	Tags []string
	Data []byte
}

func newFakeRestic() *fakeRestic {
	return &fakeRestic{
		version: "restic 0.19.1 compiled with go1.25.1 on linux/amd64",
		repos:   map[string]*fakeRepo{},
		fail:    map[string]*resticExit{},
		next:    time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC),
	}
}

// newBackupService is newService with the fake restic, the backups directory of production
// (0700, normally created by the agent) and InitBackups already run.
func newBackupService(t *testing.T, mod ...func(*Config)) (*Service, string, *fakeRestic) {
	t.Helper()
	f := newFakeRestic()
	s, data := newService(t, append([]func(*Config){func(c *Config) { c.runner = f }}, mod...)...)
	if err := os.Mkdir(s.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if st := s.InitBackups(context.Background()); !st.Available {
		t.Fatalf("InitBackups: %+v", st)
	}
	return s, data, f
}

func randomID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// command returns the restic command word (after the global flags).
func command(args []string) (string, []string) {
	for i, a := range args {
		if !strings.HasPrefix(a, "--") {
			return a, args[i+1:]
		}
	}
	return "", nil
}

func envOf(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// callsOf returns the recorded calls of one command.
func (f *fakeRestic) callsOf(cmd string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if w, _ := command(c.Args); w == cmd {
			out = append(out, c)
		}
	}
	return out
}

// snaps returns a copy of the snapshots of the repository at path.
func (f *fakeRestic) snaps(repo string) []fakeSnap {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeSnap
	if r := f.repos[repo]; r != nil {
		for _, s := range r.snaps {
			out = append(out, *s)
		}
	}
	return out
}

// addSnap puts a snapshot into the repository at repo (creating the repo on disk if needed).
func (f *fakeRestic) addSnap(t *testing.T, repo string, at time.Time, data []byte, tags ...string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.repos[repo]
	if r == nil {
		if err := os.MkdirAll(repo, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, "config"), []byte("fake"), 0o600); err != nil {
			t.Fatal(err)
		}
		r = &fakeRepo{}
		f.repos[repo] = r
	}
	id := randomID()
	r.snaps = append(r.snaps, &fakeSnap{ID: id, Time: at, Tags: tags, Data: data})
	return id
}

type fakeProc struct {
	stdin    *io.PipeWriter
	done     chan struct{}
	killed   chan struct{}
	killOnce sync.Once
	err      error
}

func (p *fakeProc) Stdin() io.WriteCloser {
	if p.stdin == nil {
		return nil
	}
	return p.stdin
}

func (p *fakeProc) Kill() { p.killOnce.Do(func() { close(p.killed) }) }

func (p *fakeProc) Wait() error { <-p.done; return p.err }

var errFakeKilled = errors.New("fake restic: killed")

func (f *fakeRestic) Start(ctx context.Context, c resticCmd) (resticProc, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Args: slices.Clone(c.Args), Env: slices.Clone(c.Env)})
	f.mu.Unlock()
	p := &fakeProc{done: make(chan struct{}), killed: make(chan struct{})}
	var stdin *io.PipeReader
	if c.Stdin {
		stdin, p.stdin = io.Pipe()
	}
	go func() {
		defer close(p.done)
		p.err = f.run(ctx, p, c, stdin)
		if stdin != nil {
			_ = stdin.CloseWithError(errFakeKilled) // a writer after the end fails like a broken pipe
		}
	}()
	return p, nil
}

func (f *fakeRestic) exit(code int, msg string) error { return &resticExit{Code: code, Stderr: msg + "\n"} }

func (f *fakeRestic) run(ctx context.Context, p *fakeProc, c resticCmd, stdin *io.PipeReader) error {
	cmd, rest := command(c.Args)
	f.mu.Lock()
	failure := f.fail[cmd]
	f.mu.Unlock()
	if failure != nil {
		return failure
	}
	out := c.Stdout
	if out == nil {
		out = io.Discard
	}
	repoPath := envOf(c.Env, "RESTIC_REPOSITORY")
	switch cmd {
	case "version":
		_, _ = fmt.Fprintln(out, f.version)
		return nil
	case "init":
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.repos[repoPath] != nil {
			return f.exit(1, "Fatal: create repository at "+repoPath+" failed: config file already exists")
		}
		if err := os.MkdirAll(repoPath, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(repoPath, "config"), []byte("fake"), 0o600); err != nil {
			return err
		}
		f.repos[repoPath] = &fakeRepo{}
		return nil
	case "unlock":
		return nil
	}
	f.mu.Lock()
	repo := f.repos[repoPath]
	f.mu.Unlock()
	if repo == nil {
		return f.exit(exitNoRepo, "Fatal: repository does not exist: unable to open config file")
	}
	switch cmd {
	case "backup":
		return f.backup(ctx, p, c, rest, repo, stdin, out)
	case "snapshots":
		return f.snapshots(repo, out)
	case "dump":
		return f.dump(ctx, p, rest, repo, out)
	case "tag":
		return f.tag(rest, repo, out)
	case "forget":
		return f.forget(rest, repo)
	}
	return f.exit(1, "fake restic: unknown command "+cmd)
}

func (f *fakeRestic) backup(ctx context.Context, p *fakeProc, c resticCmd, rest []string, repo *fakeRepo, stdin *io.PipeReader, out io.Writer) error {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-p.killed:
			return &resticExit{Code: -1}
		case <-ctx.Done(): // like SIGINT
			return &resticExit{Code: 130, Stderr: "signal interrupt received, cleaning up\n"}
		}
	}
	var tags []string
	for i := 0; i < len(rest)-1; i++ {
		if rest[i] == "--tag" {
			tags = append(tags, rest[i+1])
		}
	}
	var buf bytes.Buffer
	readDone := make(chan error, 1)
	go func() { _, err := io.Copy(&buf, stdin); readDone <- err }()
	select {
	case err := <-readDone:
		f.mu.Lock()
		f.sawEOF = append(f.sawEOF, err == nil)
		f.mu.Unlock()
		if f.stdinRead != nil {
			f.stdinRead <- buf.Len()
		}
		if err != nil {
			return f.exit(1, "Fatal: reading stdin: "+err.Error())
		}
	case <-p.killed:
		_ = stdin.CloseWithError(errFakeKilled)
		<-readDone
		f.mu.Lock()
		f.sawEOF = append(f.sawEOF, false)
		f.mu.Unlock()
		if f.stdinRead != nil {
			f.stdinRead <- buf.Len()
		}
		return &resticExit{Code: -1}
	case <-ctx.Done():
		_ = stdin.CloseWithError(errFakeKilled)
		<-readDone
		return &resticExit{Code: 130, Stderr: "signal interrupt received, cleaning up\n"}
	}
	f.mu.Lock()
	at := f.next
	f.next = f.next.Add(time.Minute)
	id := randomID()
	repo.snaps = append(repo.snaps, &fakeSnap{ID: id, Time: at, Tags: tags, Data: buf.Bytes()})
	f.mu.Unlock()
	_, _ = fmt.Fprintf(out, `{"message_type":"status","percent_done":1}`+"\n")
	_, _ = fmt.Fprintf(out, `{"message_type":"summary","total_bytes_processed":%d,"snapshot_id":%q}`+"\n", buf.Len(), id)
	return nil
}

func (f *fakeRestic) snapshots(repo *fakeRepo, out io.Writer) error {
	type summary struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
	}
	type snap struct {
		Time     string   `json:"time"`
		ID       string   `json:"id"`
		ShortID  string   `json:"short_id"`
		Hostname string   `json:"hostname"`
		Paths    []string `json:"paths"`
		Tags     []string `json:"tags"`
		Summary  summary  `json:"summary"`
	}
	f.mu.Lock()
	list := []snap{}
	for _, s := range repo.snaps {
		if !slices.Contains(s.Tags, "mcpanel") {
			continue
		}
		list = append(list, snap{
			Time: s.Time.Format(time.RFC3339Nano), ID: s.ID, ShortID: s.ID[:8], Hostname: "mcpanel",
			Paths: []string{"/data.tar"}, Tags: s.Tags, Summary: summary{TotalBytesProcessed: int64(len(s.Data))},
		})
	}
	f.mu.Unlock()
	return json.NewEncoder(out).Encode(list)
}

func (f *fakeRestic) find(repo *fakeRepo, id string) *fakeSnap {
	for _, s := range repo.snaps {
		if s.ID == id {
			return s
		}
	}
	return nil
}

func (f *fakeRestic) dump(ctx context.Context, p *fakeProc, rest []string, repo *fakeRepo, out io.Writer) error {
	if len(rest) != 2 || rest[1] != "/data.tar" {
		return f.exit(1, "Fatal: no file and no snapshot ID specified")
	}
	f.mu.Lock()
	s := f.find(repo, rest[0])
	f.mu.Unlock()
	if s == nil {
		return f.exit(1, "Fatal: failed to find snapshot: no matching ID found for prefix "+rest[0])
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-p.killed:
			return &resticExit{Code: -1}
		case <-ctx.Done():
			return &resticExit{Code: 130}
		}
	}
	if _, err := out.Write(s.Data); err != nil {
		return &resticExit{Code: 1, Stderr: "Fatal: write: " + err.Error()}
	}
	return nil
}

func (f *fakeRestic) tag(rest []string, repo *fakeRepo, out io.Writer) error {
	if len(rest) != 3 || rest[1] != "pinned" || (rest[0] != "--add" && rest[0] != "--remove") {
		return f.exit(1, "fake restic: unexpected tag arguments "+strings.Join(rest, " "))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.find(repo, rest[2])
	if s == nil {
		return f.exit(1, "Fatal: no matching ID found")
	}
	has := slices.Contains(s.Tags, "pinned")
	if (rest[0] == "--add") == has {
		_, _ = fmt.Fprintln(out, `{"message_type":"summary","changed_snapshots":0}`)
		return nil
	}
	old := s.ID
	if rest[0] == "--add" {
		s.Tags = append(s.Tags, "pinned")
	} else {
		s.Tags = slices.DeleteFunc(s.Tags, func(t string) bool { return t == "pinned" })
	}
	s.ID = randomID()
	_, _ = fmt.Fprintf(out, `{"message_type":"changed","old_snapshot_id":%q,"new_snapshot_id":%q}`+"\n", old, s.ID)
	_, _ = fmt.Fprintln(out, `{"message_type":"summary","changed_snapshots":1}`)
	return nil
}

// forget removes explicit IDs; with a policy it applies only --keep-last to unpinned
// kind=scheduled snapshots (enough for unit tests; the real policy is covered in Aufgabe 8).
func (f *fakeRestic) forget(rest []string, repo *fakeRepo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keepLast := -1
	var ids []string
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--keep-last":
			keepLast, _ = strconv.Atoi(rest[i+1])
			i++
		case strings.HasPrefix(a, "--") && a != "--prune":
			i++ // flag with a value
		case a == "--prune":
		default:
			ids = append(ids, a)
		}
	}
	if len(ids) > 0 {
		for _, id := range ids {
			if f.find(repo, id) == nil {
				return f.exit(1, "Fatal: no matching ID found for prefix "+id)
			}
		}
		repo.snaps = slices.DeleteFunc(repo.snaps, func(s *fakeSnap) bool { return slices.Contains(ids, s.ID) })
		return nil
	}
	if keepLast < 0 {
		return nil
	}
	var sched []*fakeSnap
	for _, s := range repo.snaps {
		if slices.Contains(s.Tags, "kind=scheduled") && !slices.Contains(s.Tags, "pinned") {
			sched = append(sched, s)
		}
	}
	slices.SortFunc(sched, func(a, b *fakeSnap) int { return b.Time.Compare(a.Time) })
	if len(sched) > keepLast {
		drop := sched[keepLast:]
		repo.snaps = slices.DeleteFunc(repo.snaps, func(s *fakeSnap) bool { return slices.Contains(drop, s) })
	}
	return nil
}
```

`internal/files/helpers_test.go`: `testLimits` bekommt `MaxBackupBytes: 1 << 20, MaxBackupEntries: 100, MaxBackupJobs: 1`.

`internal/files/backupsvc_test.go`:

```go
package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestInitBackupsStatus(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if st := s.BackupsStatus(); !st.Available || st.Version != "0.19.1" || st.Reason != "" {
		t.Fatalf("status = %+v", st)
	}
	if c := f.callsOf("version"); len(c) != 1 || strings.Join(c[0].Env, " ") != "HOME="+s.backupsDir()+" RESTIC_PROGRESS_FPS=1" {
		t.Fatalf("version calls = %+v", c)
	}

	f.version = "restic 0.16.4 compiled with go1.21 on linux/amd64"
	if st := s.InitBackups(ctx); st.Available || !strings.Contains(st.Reason, "0.16.4") || !strings.Contains(st.Reason, "0.17.0") {
		t.Fatalf("old restic = %+v", st)
	}
	f.version = "something else"
	if st := s.InitBackups(ctx); st.Available || st.Reason == "" {
		t.Fatalf("unparsable version = %+v", st)
	}
	f.fail["version"] = &resticExit{Code: 1, Stderr: "boom\n"}
	if st := s.InitBackups(ctx); st.Available || st.Reason == "" {
		t.Fatalf("failing version = %+v", st)
	}

	// No binary at all (production path that does not exist): unavailable, never a host path.
	s2, _ := newService(t, func(c *Config) { c.ResticBinary = "/nonexistent/restic" })
	st := s2.InitBackups(ctx)
	if st.Available || strings.Contains(st.Reason, "/nonexistent") || st.Reason == "" {
		t.Fatalf("missing binary = %+v", st)
	}
}

// hangingRunner never answers: the version check must give up after resticVersionWait.
type hangingRunner struct{}

func (hangingRunner) Start(ctx context.Context, _ resticCmd) (resticProc, error) {
	p := &fakeProc{done: make(chan struct{}), killed: make(chan struct{})}
	go func() { <-ctx.Done(); p.err = &resticExit{Code: 130}; close(p.done) }()
	return p, nil
}

func TestInitBackupsVersionTimeout(t *testing.T) {
	s, _ := newService(t, func(c *Config) { c.runner = hangingRunner{} })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond) // stands in for the 10 s limit
	defer cancel()
	if st := s.InitBackups(ctx); st.Available {
		t.Fatalf("hanging restic = %+v", st)
	}
}

func TestRequireBackups(t *testing.T) {
	s, _, _ := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"repos", "keys"} {
		if fi, err := os.Lstat(filepath.Join(s.backupsDir(), sub)); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", sub, fi, err)
		}
	}
	if err := s.requireBackups(0); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v", err)
	}

	// Production without backups/ (the agent did not run yet): unavailable, no host path.
	s2, _ := newService(t, func(c *Config) { c.runner = newFakeRestic() })
	s2.InitBackups(context.Background())
	err := s2.requireBackups(1)
	if !errors.Is(err, filesapi.ErrBackupsUnavailable) || strings.Contains(s2.publicMessage(err.Error()), s2.cfg.InstancesDir) {
		t.Fatalf("missing backups/: %v", err)
	}
	// A symlink in place of repos/ is refused, never followed.
	s3, _, _ := newBackupService(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(s3.backupsDir(), "repos")); err != nil {
		t.Fatal(err)
	}
	if err := s3.requireBackups(1); err == nil {
		t.Fatal("symlinked repos/ accepted")
	}
	// Dev creates backups/ itself.
	s4, _ := newService(t, func(c *Config) { c.runner = newFakeRestic(); c.Dev = true })
	s4.InitBackups(context.Background())
	if err := s4.requireBackups(1); err != nil {
		t.Fatalf("dev: %v", err)
	}
	// Without restic: unavailable with the reason.
	s5, _ := newService(t)
	if err := os.Mkdir(s5.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	s5.InitBackups(context.Background())
	if err := s5.requireBackups(1); !errors.Is(err, filesapi.ErrBackupsUnavailable) {
		t.Fatalf("no restic: %v", err)
	}
}

func TestEnsureRepoCreatesKeyAndRepo(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if s.repoExists(1) {
		t.Fatal("repo before init")
	}
	if err := s.ensureRepo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(s.keyFile(1))
	if err != nil || len(key) != 44 || strings.ContainsRune(string(key), '\n') { // base64 of 32 bytes
		t.Fatalf("key = %q %v", key, err)
	}
	if fi, _ := os.Lstat(s.keyFile(1)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v", fi.Mode())
	}
	if !s.repoExists(1) || len(f.callsOf("init")) != 1 {
		t.Fatalf("init calls = %d", len(f.callsOf("init")))
	}
	env := strings.Join(f.callsOf("init")[0].Env, " ")
	want := "HOME=" + s.backupsDir() + " RESTIC_PROGRESS_FPS=1 RESTIC_REPOSITORY=" + s.repoDir(1) + " RESTIC_PASSWORD_FILE=" + s.keyFile(1)
	if env != want {
		t.Fatalf("env = %s, want %s", env, want)
	}
	// Second call: nothing new, the key stays.
	if err := s.ensureRepo(ctx, 1); err != nil || len(f.callsOf("init")) != 1 {
		t.Fatalf("second ensureRepo: %v, %d inits", err, len(f.callsOf("init")))
	}
	if again, _ := os.ReadFile(s.keyFile(1)); string(again) != string(key) {
		t.Fatal("key was replaced")
	}
	// Repository without key: refused with a message that names no host path.
	if err := os.Remove(s.keyFile(1)); err != nil {
		t.Fatal(err)
	}
	err = s.ensureRepo(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "keys/1") || strings.Contains(err.Error(), s.cfg.InstancesDir) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestResticErrMapping(t *testing.T) {
	s, _, _ := newBackupService(t)
	for _, c := range []struct {
		code int
		want error
	}{
		{exitNoRepo, filesapi.ErrNotFound},
		{exitLocked, filesapi.ErrBusy},
	} {
		if err := s.resticErr("snapshots", &resticExit{Code: c.code}); !errors.Is(err, c.want) {
			t.Errorf("exit %d: %v", c.code, err)
		}
	}
	err := s.resticErr("backup", &resticExit{Code: 1, Stderr: "Fatal: unable to open " + s.repoDir(1) + "/config\n"})
	if msg := s.publicMessage(err.Error()); strings.Contains(msg, s.cfg.InstancesDir) || !strings.Contains(msg, "restic backup failed") {
		t.Fatalf("message = %q", msg)
	}
	if err := s.resticErr("tag", &resticExit{Code: exitWrongPassword}); err == nil || errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("wrong password = %v (internal error expected)", err)
	}
}
```

`internal/files/server_backups_test.go`:

```go
package files

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestServerBackupsStatus(t *testing.T) {
	s, _, _ := newBackupService(t)
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	resp, err := http.Get(ts.URL + filesapi.PathBackupsStatus)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var st filesapi.BackupsStatus
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil || !st.Available {
		t.Fatalf("status %d %+v", resp.StatusCode, st)
	}
	// Without restic the status still answers.
	s2, _ := newService(t)
	s2.InitBackups(t.Context())
	ts2 := startHTTP(t, NewServer(s2, "test", discardLog()))
	resp2, err := http.Get(ts2.URL + filesapi.PathBackupsStatus)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status without restic: %d", resp2.StatusCode)
	}
}
```

In `internal/files/service_test.go`, `TestStatusFor`: Zeile `{filesapi.ErrBackupsUnavailable, http.StatusServiceUnavailable, filesapi.CodeUnavailable},` vor der `boom`-Zeile.

- [ ] **Step 14: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`c.runner undefined`, `s.InitBackups undefined`, …).

- [ ] **Step 15: `backupsvc.go`, Service und Server implementieren**

`internal/files/service.go`:
- `Config` bekommt nach `Disk`:

```go
	ResticBinary string // spec 4 §3.2; "" = backups unavailable. A bare name is looked up in PATH (dev).
	runner       resticRunner // tests: replaces the restic binary
```

- `Service` bekommt (nach `instHash`):

```go
	runner     resticRunner // nil = no usable binary (runnerWhy says why)
	runnerWhy  string
	backupMu   sync.Mutex
	backupStat filesapi.BackupsStatus // set by InitBackups
	// backupHook runs before every entry of a backup stream; swapHook before every rename of
	// a restore swap (tests: swap folders, fail a step).
	backupHook func(p string)
	swapHook   func(step int) error
```

- In `New` vor `return s`:

```go
	s.runner, s.runnerWhy = cfg.runner, ""
	if s.runner == nil {
		s.runner, s.runnerWhy = newExecRunner(cfg.ResticBinary)
	}
	s.backupStat = filesapi.BackupsStatus{Reason: "the restic check has not run yet"}
```

  und `jobs: newJobs()` bleibt in dieser Aufgabe unverändert.
- `fsErr`: die Liste der durchgereichten Sentinels bekommt `errors.Is(err, filesapi.ErrBackupsUnavailable)`.

`internal/files/backupsvc.go`:

```go
package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// newExecRunner resolves the configured binary: an absolute path is taken as is (a missing
// file shows up in the version check), a bare name is looked up in PATH (dev).
func newExecRunner(bin string) (resticRunner, string) {
	switch {
	case bin == "":
		return nil, "restic is not configured (restic_binary)"
	case filepath.IsAbs(bin):
		return execRunner{bin: bin}, ""
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return nil, "restic was not found in PATH"
	}
	return execRunner{bin: p}, ""
}

// InitBackups checks the restic binary once (spec 4 §3.2: `restic version`, at most
// resticVersionWait, at least filesapi.MinResticVersion) and remembers the result for
// BackupsStatus. The reason never names a host path.
func (s *Service) InitBackups(ctx context.Context) filesapi.BackupsStatus {
	st := s.probeRestic(ctx)
	s.backupMu.Lock()
	s.backupStat = st
	s.backupMu.Unlock()
	if st.Available {
		s.log.Info("files: backups available", "restic", st.Version)
	} else {
		s.log.Warn("files: backups unavailable", "reason", st.Reason)
	}
	return st
}

func (s *Service) probeRestic(ctx context.Context) filesapi.BackupsStatus {
	if s.runner == nil {
		return filesapi.BackupsStatus{Reason: s.runnerWhy}
	}
	ctx, cancel := context.WithTimeout(ctx, resticVersionWait)
	defer cancel()
	out := &limitedBuffer{max: 64 << 10}
	p, err := s.runner.Start(ctx, resticCmd{Args: []string{"version"}, Env: s.resticEnv(0), Stdout: out})
	if err != nil {
		s.log.Warn("files: cannot start restic", "err", err)
		return filesapi.BackupsStatus{Reason: "restic cannot be started (restic_binary)"}
	}
	if err := p.Wait(); err != nil {
		s.log.Warn("files: restic version failed", "err", err)
		return filesapi.BackupsStatus{Reason: "restic version failed"}
	}
	v, ok := parseResticVersion(string(out.Bytes()))
	if !ok {
		return filesapi.BackupsStatus{Reason: "restic version printed an unknown format"}
	}
	ver := fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	if !versionAtLeast(v, filesapi.MinResticVersion) {
		return filesapi.BackupsStatus{Version: ver, Reason: fmt.Sprintf("restic %s is too old (%s or newer is needed)", ver, filesapi.MinResticVersion)}
	}
	return filesapi.BackupsStatus{Available: true, Version: ver}
}

// BackupsStatus returns the result of the last InitBackups.
func (s *Service) BackupsStatus() filesapi.BackupsStatus {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	return s.backupStat
}

func (s *Service) backupsDir() string { return filepath.Join(s.cfg.InstancesDir, "backups") }

func (s *Service) repoDir(id int64) string {
	return filepath.Join(s.backupsDir(), "repos", strconv.FormatInt(id, 10))
}

func (s *Service) keyFile(id int64) string {
	return filepath.Join(s.backupsDir(), "keys", strconv.FormatInt(id, 10))
}

// requireBackups checks that backups can run for server id: restic is usable and backups/,
// repos/ and keys/ are real folders (repos/ and keys/ are created on demand; backups/ itself
// is created by the agent, in dev by the service). Containers never see backups/, so plain
// paths are fine here; symlinks are still refused.
func (s *Service) requireBackups(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	if st := s.BackupsStatus(); !st.Available {
		return fmt.Errorf("%w: %s", filesapi.ErrBackupsUnavailable, st.Reason)
	}
	if s.cfg.Dev {
		if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
			return err
		}
	}
	if err := realDir(s.backupsDir()); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: the backup directory does not exist yet (the agent creates it at start)", filesapi.ErrBackupsUnavailable)
		}
		return err
	}
	for _, sub := range []string{"repos", "keys"} {
		dir := filepath.Join(s.backupsDir(), sub)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := realDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// realDir: dir exists and is a directory itself (Lstat: a symlink is refused).
func realDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a real directory", dir)
	}
	return nil
}

// repoExists reports whether the repository of server id has been initialised.
func (s *Service) repoExists(id int64) bool {
	fi, err := os.Lstat(filepath.Join(s.repoDir(id), "config"))
	return err == nil && fi.Mode().IsRegular()
}

func (s *Service) keyExists(id int64) bool {
	fi, err := os.Lstat(s.keyFile(id))
	return err == nil && fi.Mode().IsRegular()
}

// ensureKey creates the repository key of server id if it does not exist (never replaced).
func (s *Service) ensureKey(id int64) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	f, err := os.OpenFile(s.keyFile(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // G304: <instances_dir>/backups/keys/<id>, id is an int64 > 0
	if errors.Is(err, fs.ErrExist) {
		if s.keyExists(id) {
			return nil
		}
		return fmt.Errorf("keys/%d is not a regular file", id)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(b)); err != nil {
		_ = f.Close()
		_ = os.Remove(s.keyFile(id))
		return err
	}
	return f.Close()
}

// ensureRepo initialises the repository of server id on its first backup (spec 4 §3.1). A
// repository without its key is unreadable: that is an error, never a new key.
func (s *Service) ensureRepo(ctx context.Context, id int64) error {
	if s.repoExists(id) {
		if !s.keyExists(id) {
			return fmt.Errorf("the key of this server's backup repository is missing (keys/%d)", id)
		}
		return nil
	}
	if err := s.ensureKey(id); err != nil {
		return fmt.Errorf("create the repository key: %w", err)
	}
	_, err := s.restic(ctx, id, nil, "init")
	return err
}

// resticEnv is the complete environment of a restic call (spec 4 §3.3); id 0 = no repository.
func (s *Service) resticEnv(id int64, extra ...string) []string {
	env := []string{"HOME=" + s.backupsDir(), "RESTIC_PROGRESS_FPS=1"}
	if id > 0 {
		env = append(env, "RESTIC_REPOSITORY="+s.repoDir(id), "RESTIC_PASSWORD_FILE="+s.keyFile(id))
	}
	return append(env, extra...)
}

// restic runs one small restic command for server id (args after "--no-cache") and returns
// its stdout (at most resticStdoutMax bytes).
func (s *Service) restic(ctx context.Context, id int64, extraEnv []string, args ...string) ([]byte, error) {
	out := &limitedBuffer{max: resticStdoutMax}
	p, err := s.runner.Start(ctx, resticCmd{Args: append([]string{"--no-cache"}, args...), Env: s.resticEnv(id, extraEnv...), Stdout: out})
	if err != nil {
		return nil, fmt.Errorf("start restic: %w", err)
	}
	op := subcommand(args)
	if err := p.Wait(); err != nil {
		return nil, s.resticErr(op, err)
	}
	if out.overflow {
		return nil, fmt.Errorf("restic %s printed more than %d bytes", op, resticStdoutMax)
	}
	return bytes.Clone(out.Bytes()), nil
}

// subcommand is the first argument that is not a flag.
func subcommand(args []string) string {
	for _, a := range args {
		if len(a) > 0 && a[0] != '-' {
			return a
		}
	}
	return "?"
}

// resticErr maps a failed restic run (spec 4 §3.3): the full stderr goes to the log, the
// answer gets a short message (publicMessage removes host paths when it leaves the service).
func (s *Service) resticErr(op string, err error) error {
	var ex *resticExit
	if !errors.As(err, &ex) {
		return fmt.Errorf("restic %s: %w", op, err)
	}
	s.log.Warn("files: restic failed", "op", op, "code", ex.Code, "stderr", ex.Stderr)
	switch ex.Code {
	case exitNoRepo:
		return fmt.Errorf("%w: backup repository does not exist", filesapi.ErrNotFound)
	case exitLocked:
		return fmt.Errorf("%w: the backup repository is locked", filesapi.ErrBusy)
	case exitWrongPassword:
		return errors.New("the key does not match the backup repository")
	}
	return fmt.Errorf("restic %s failed: %s", op, lastLine(ex.Stderr))
}
```

`internal/files/server_backups.go`:

```go
package files

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// backupRoutes are the endpoints of spec 4 §4.1; NewServer adds them to its routes.
func (s *Server) backupRoutes() map[string]route {
	return map[string]route{
		filesapi.PathBackupsStatus: {method: http.MethodGet, handler: s.backupsStatus},
	}
}

// backupsStatus always answers, also without restic (spec 4 §3.2).
func (s *Server) backupsStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.BackupsStatus())
}
```

`internal/files/server.go`, in `NewServer` nach dem `routes`-Literal:

```go
	for p, rt := range s.backupRoutes() {
		s.routes[p] = rt
	}
```

`cmd/files/main.go`:
- Import `_ "time/tzdata"` (Zeitzonen-Prüfung der Forget-Policy auf Hosts ohne Zoneinfo, Global Constraints).
- `files.New(files.Config{…, ResticBinary: cfg.ResticBinary})`.
- Direkt danach, vor dem Cleanup-Goroutine:

```go
	// Spec 4 §3.2: one restic check at start; without restic the backup endpoints answer 503
	// and everything else works.
	svc.InitBackups(ctx)
```

- Log-Zeile `files started` um `"restic_binary", cfg.ResticBinary` ergänzen.

- [ ] **Step 16: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test ./internal/files -run 'TestInitBackups|TestRequireBackups|TestEnsureRepo|TestResticErr|TestServerBackupsStatus|TestStatusFor'` und `go test ./cmd/files`)
Expected: PASS.

- [ ] **Step 17: Commit und Draft-PR**

```bash
git add internal/config/files.go internal/config/files_test.go internal/agent/instances/datadir.go internal/agent/instances/datadir_test.go \
  internal/files/restic.go internal/files/backupsvc.go internal/files/server_backups.go internal/files/service.go internal/files/server.go \
  internal/files/helpers_test.go internal/files/service_test.go internal/files/restic_exec_test.go internal/files/restic_fake_test.go \
  internal/files/backupsvc_test.go internal/files/server_backups_test.go cmd/files/main.go
git commit -m "feat(files): restic runner, backup status and the backups directory

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/backups-files
gh pr create --draft --title "feat(files): backups in the files service (sub-project 4)" --body "Phase B1 of docs/superpowers/plans/2026-10-12-backups-02-files.md (tasks 4–8).

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 5: Job-Arten, Backup-Stream, Backup-Job und Snapshot-Liste

**Files:**
- Modify: `internal/files/jobs.go`, `internal/files/extract.go`, `internal/files/service.go` (`newJobs`), `internal/files/server_backups.go`, `internal/files/review_test.go` (`TestJobErrorHasNoHostPath`: neue `jobs`-Signaturen)
- Create: `internal/files/tarwrite.go`, `internal/files/backup.go`
- Test: `internal/files/jobs_test.go`, `internal/files/tarwrite_test.go`, `internal/files/tarwrite_linux_test.go`, `internal/files/backup_test.go`, `internal/files/server_backups_test.go`

**Interfaces:**
- Consumes: Aufgabe 4 (`requireBackups`, `ensureRepo`, `restic`, `resticEnv`, `resticErr`, `runner`, `backupHook`, `fakeRestic`, `newBackupService`); vorhanden `openDir`, `openRegular`, `readSmall`, `redactProperties`, `isServerProps`, `fsErr`, `s.open`, `s.disk`, `waitJob`, `put`, `get`, `startHTTP`, `postJSON`, `errCode`, `makeZip`, `putBytes`.
- Produces:

```go
type jobSpec struct {
	kind                     filesapi.JobKind
	path, dest, snapshot     string
	totalBytes, totalEntries int64
}
const jobLock filesapi.JobKind = "lock" // pin, purge: the instance is taken, no job
func newJobs(maxRestic int) *jobs
func (j *jobs) acquire(id int64, kind filesapi.JobKind) error
func (j *jobs) release(id int64)
func (j *jobs) start(id int64, spec jobSpec, now func() time.Time, run func(ctx context.Context) error) (filesapi.Job, error)
func (j *jobs) progress(id, bytes int64)  // bytes += n, entries++
func (j *jobs) skip(id int64)             // skipped++
func (j *jobs) setSnapshot(id int64, sid string)
func (j *jobs) stopErr() error            // errShuttingDown or errCanceled
var errCanceled = errors.New("the job was canceled")

func writeBackupTar(ctx context.Context, root *os.Root, w io.Writer, lim config.FilesLimits, hook func(string), onEntry func(int64), onSkip func()) error
func (s *Service) CreateBackup(ctx context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error)
func (s *Service) Snapshots(ctx context.Context, id int64) ([]filesapi.Snapshot, error)
func (s *Service) findSnapshot(ctx context.Context, id int64, sid string) (filesapi.Snapshot, error)
func noteTag(note string) string
func tzEnv(zone string) []string // nil for ""
```

- [ ] **Step 1: Job-Arten – failing tests**

`internal/files/jobs_test.go`:

```go
package files

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// TestJobsOnePerInstanceAcrossKinds: extract, backup, restore, forget and the pin/purge lock
// exclude each other per instance (spec 4 §4.1); other instances are not affected.
func TestJobsOnePerInstanceAcrossKinds(t *testing.T) {
	kinds := []filesapi.JobKind{filesapi.JobExtract, filesapi.JobBackup, filesapi.JobRestore, filesapi.JobForget, jobLock}
	for _, first := range kinds {
		for _, second := range kinds {
			j := newJobs(4)
			if err := j.acquire(1, first); err != nil {
				t.Fatalf("%s: %v", first, err)
			}
			if err := j.acquire(1, second); !errors.Is(err, filesapi.ErrBusy) {
				t.Errorf("%s then %s on the same instance: %v, want busy", first, second, err)
			}
			if err := j.acquire(2, second); err != nil {
				t.Errorf("%s then %s on another instance: %v", first, second, err)
			}
			j.release(1)
			if err := j.acquire(1, second); err != nil {
				t.Errorf("%s after release: %v", second, err)
			}
		}
	}
}

// TestJobsResticSlots: restic jobs count against max_backup_jobs, extracts against maxJobsTotal;
// the two limits are independent, the pin/purge lock counts against neither.
func TestJobsResticSlots(t *testing.T) {
	j := newJobs(1)
	if err := j.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	for _, k := range []filesapi.JobKind{filesapi.JobBackup, filesapi.JobRestore, filesapi.JobForget} {
		if err := j.acquire(2, k); !errors.Is(err, filesapi.ErrBusy) {
			t.Errorf("second restic job (%s): %v, want busy", k, err)
		}
	}
	for id := int64(2); id <= 3; id++ {
		if err := j.acquire(id, filesapi.JobExtract); err != nil {
			t.Fatalf("extract %d: %v", id, err)
		}
	}
	if err := j.acquire(4, filesapi.JobExtract); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("third extract: %v", err)
	}
	if err := j.acquire(5, jobLock); err != nil {
		t.Fatalf("lock: %v", err)
	}
	j.release(1)
	if err := j.acquire(6, filesapi.JobForget); err != nil {
		t.Fatalf("restic slot after release: %v", err)
	}
}

func TestJobsStartRecordsKindAndSnapshot(t *testing.T) {
	j := newJobs(1)
	if err := j.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	job, err := j.start(1, jobSpec{kind: filesapi.JobBackup}, time.Now, func(ctx context.Context) error {
		<-release
		j.progress(1, 10)
		j.skip(1)
		j.setSnapshot(1, "abc")
		return nil
	})
	if err != nil || job.Kind != filesapi.JobBackup || job.State != jobRunning {
		t.Fatalf("start = %+v %v", job, err)
	}
	close(release)
	j.close()
	got := j.get(1)
	if got.State != jobDone || got.Bytes != 10 || got.Entries != 1 || got.Skipped != 1 || got.Snapshot != "abc" {
		t.Fatalf("job = %+v", got)
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`too many arguments in call to j.acquire`, `undefined: jobLock`, …).

- [ ] **Step 3: `jobs.go` verallgemeinern**

`jobs.go` vollständig ersetzen:

```go
package files

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Short local names for the job states of filesapi.
const (
	jobRunning = filesapi.JobRunning
	jobDone    = filesapi.JobDone
	jobFailed  = filesapi.JobFailed
)

// jobLock takes an instance for an operation that is not a job (pin, purge): it excludes jobs
// of every kind but counts against no slot.
const jobLock filesapi.JobKind = "lock"

var (
	errShuttingDown = errors.New("the files service is shutting down")
	errCanceled     = errors.New("the job was canceled")
)

// jobSpec describes a job when it starts.
type jobSpec struct {
	kind                     filesapi.JobKind
	path, dest, snapshot     string
	totalBytes, totalEntries int64
}

// jobs tracks the jobs of all kinds (spec 3b §4.2, spec 4 §4.1): the last one per instance, at
// most one pre-check, running job or lock per instance, at most maxJobsTotal extracts and
// maxRestic restic jobs (backup, restore, forget) overall. A slot is taken before the
// pre-check, so pre-checks are throttled like jobs. Jobs live in memory; a restart forgets them
// (and CleanupTemp removes their temp folders).
type jobs struct {
	mu        sync.Mutex
	last      map[int64]*filesapi.Job
	busy      map[int64]filesapi.JobKind // instances with a pre-check, a running job or a lock
	slots     int                        // extract pre-checks and jobs
	restic    int                        // restic pre-checks and jobs
	maxRestic int
	cancels   map[int64]context.CancelFunc
	committed map[int64]bool // restores in their swap: cancel has no effect any more
	closed    bool           // Close was called: no new jobs (wg.Add only happens under mu)
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	hook      func(ctx context.Context, id int64, entry string) // tests: runs before each extracted entry
	scrub     func(string) string                               // job errors without host paths (Service.publicMessage)
}

func newJobs(maxRestic int) *jobs {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobs{
		last: map[int64]*filesapi.Job{}, busy: map[int64]filesapi.JobKind{}, maxRestic: max(maxRestic, 1),
		cancels: map[int64]context.CancelFunc{}, committed: map[int64]bool{}, ctx: ctx, cancel: cancel,
	}
}

func isResticKind(k filesapi.JobKind) bool {
	return k == filesapi.JobBackup || k == filesapi.JobRestore || k == filesapi.JobForget
}

// busyText describes what holds an instance.
func busyText(k filesapi.JobKind) string {
	switch k {
	case filesapi.JobExtract:
		return "an archive is already being extracted for this server"
	case filesapi.JobBackup:
		return "a backup is running for this server"
	case filesapi.JobRestore:
		return "a restore is running for this server"
	case filesapi.JobForget:
		return "backups of this server are being deleted"
	}
	return "another backup operation is running for this server"
}

// acquire takes the slot of instance id for a pre-check of the given kind; the caller must
// either start a job with it or give it back with release.
func (j *jobs) acquire(id int64, kind filesapi.JobKind) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.acquireLocked(id, kind)
}

func (j *jobs) acquireLocked(id int64, kind filesapi.JobKind) error {
	switch {
	case j.closed:
		return errShuttingDown
	case j.busy[id] != "":
		return fmt.Errorf("%w: %s", filesapi.ErrBusy, busyText(j.busy[id]))
	case kind == filesapi.JobExtract && j.slots >= maxJobsTotal:
		return fmt.Errorf("%w: too many archives are being extracted", filesapi.ErrBusy)
	case isResticKind(kind) && j.restic >= j.maxRestic:
		return fmt.Errorf("%w: too many backup jobs are running", filesapi.ErrBusy)
	}
	j.busy[id] = kind
	switch {
	case kind == filesapi.JobExtract:
		j.slots++
	case isResticKind(kind):
		j.restic++
	}
	return nil
}

// release gives back a slot that did not become a job (or a lock).
func (j *jobs) release(id int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.freeLocked(id)
}

func (j *jobs) freeLocked(id int64) {
	kind, ok := j.busy[id]
	if !ok {
		return
	}
	delete(j.busy, id)
	switch {
	case kind == filesapi.JobExtract:
		j.slots--
	case isResticKind(kind):
		j.restic--
	}
}

// start turns the slot of instance id into a running job: the job is registered as running
// before start returns (so a GET /v1/jobs after the 202 always sees it), then run executes in
// the background with its own context (child of the jobs' context, cancelled by cancelJob or
// close). On error the slot is given back.
func (j *jobs) start(id int64, spec jobSpec, now func() time.Time, run func(ctx context.Context) error) (filesapi.Job, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		j.freeLocked(id)
		return filesapi.Job{}, errShuttingDown
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		j.freeLocked(id)
		return filesapi.Job{}, err
	}
	job := &filesapi.Job{
		ID: hex.EncodeToString(b[:]), Kind: spec.kind, State: jobRunning, Path: spec.path, Dest: spec.dest,
		Snapshot: spec.snapshot, TotalBytes: spec.totalBytes, TotalEntries: spec.totalEntries, StartedAt: now().UnixMilli(),
	}
	jctx, cancel := context.WithCancel(j.ctx)
	j.last[id] = job
	j.cancels[id] = cancel
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		err := run(jctx)
		cancel()
		j.finish(id, err, now())
	}()
	return *job, nil
}

func (j *jobs) update(id int64, f func(*filesapi.Job)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		f(job)
	}
}

// progress reports one written entry with bytes bytes.
func (j *jobs) progress(id, bytes int64) {
	j.update(id, func(job *filesapi.Job) { job.Bytes += bytes; job.Entries++ })
}

// skip reports one left-out entry (spec 4 §4.2).
func (j *jobs) skip(id int64) { j.update(id, func(job *filesapi.Job) { job.Skipped++ }) }

func (j *jobs) setSnapshot(id int64, sid string) {
	j.update(id, func(job *filesapi.Job) { job.Snapshot = sid })
}

// stopErr is the error of a job whose context ended: the service shuts down, or cancelJob.
func (j *jobs) stopErr() error {
	if j.ctx.Err() != nil {
		return errShuttingDown
	}
	return errCanceled
}

func (j *jobs) finish(id int64, err error, now time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if job := j.last[id]; job != nil {
		job.FinishedAt = now.UnixMilli()
		if err != nil {
			job.State = jobFailed
			job.Error = err.Error()
			if j.scrub != nil {
				job.Error = j.scrub(job.Error)
			}
		} else {
			job.State = jobDone
		}
	}
	delete(j.cancels, id)
	delete(j.committed, id)
	j.freeLocked(id)
}

// close stops new jobs, cancels the running ones and waits for them. It may be called more
// than once.
func (j *jobs) close() {
	j.mu.Lock()
	j.closed = true
	j.mu.Unlock()
	j.cancel()
	j.wg.Wait()
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

// Job returns a copy of the last job (any kind) of server id, or nil.
func (s *Service) Job(id int64) *filesapi.Job { return s.jobs.get(id) }
```

`internal/files/service.go`: `jobs: newJobs(cfg.Limits.MaxBackupJobs)` im `Service`-Literal (statt `newJobs()`).

`internal/files/extract.go`: `s.jobs.acquire(req.ID)` → `s.jobs.acquire(req.ID, filesapi.JobExtract)`; der `start`-Aufruf wird zu

```go
	job, err := s.jobs.start(req.ID, jobSpec{kind: filesapi.JobExtract, path: src, dest: dest, totalBytes: plan.bytes, totalEntries: plan.entries},
		s.clock.Now, func(jctx context.Context) error {
```

(Rumpf unverändert). Der Kommentar von `archivePlan` bleibt.

`internal/files/review_test.go`, `TestJobErrorHasNoHostPath`: `s.jobs.acquire(1)` → `s.jobs.acquire(1, filesapi.JobExtract)`, `s.jobs.start(1, "w.zip", "w", archivePlan{}, s.clock.Now, …)` → `s.jobs.start(1, jobSpec{kind: filesapi.JobExtract, path: "w.zip", dest: "w"}, s.clock.Now, …)`.

- [ ] **Step 4: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test ./internal/files -run 'TestJobs|TestExtract|TestServerJobs'`)
Expected: PASS, auch alle vorhandenen Extract-Tests (`TestJobsGuaranteesForThePanel` prüft weiter `busy`).

- [ ] **Step 5: Backup-Stream – failing tests**

`internal/files/tarwrite_test.go`:

```go
package files

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type tarItem struct {
	name string
	typ  byte
	body string
	mode int64
}

func readTar(t *testing.T, b []byte) []tarItem {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	var out []tarItem
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
			t.Fatalf("%s carries an owner: %+v", h.Name, h)
		}
		out = append(out, tarItem{name: h.Name, typ: h.Typeflag, body: string(body), mode: h.Mode})
	}
}

func tarNames(items []tarItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.name
	}
	return out
}

// backupTar runs writeBackupTar on the data directory of server 1.
func backupTar(t *testing.T, s *Service, hook func(string)) ([]byte, int, error) {
	t.Helper()
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var buf bytes.Buffer
	skipped := 0
	err = writeBackupTar(context.Background(), root, &buf, s.cfg.Limits, hook, func(int64) {}, func() { skipped++ })
	return buf.Bytes(), skipped, err
}

func TestBackupTarContentOrderAndRedaction(t *testing.T) {
	s, data := newService(t)
	put(t, data, "world/region/r.0.0.mca", "region")
	put(t, data, "b.txt", "b")
	put(t, data, "a.txt", "a")
	put(t, data, "server.properties", "motd=hi\nrcon.password=s3cret\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")
	put(t, data, "world/.mcpanel-tmp-x/leftover", "x")
	if err := os.Chmod(filepath.Join(data, "a.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := readTar(t, b)
	want := []string{"a.txt", "b.txt", "server.properties", "world/", "world/region/", "world/region/r.0.0.mca"}
	if got := tarNames(items); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v (sorted, hidden left out)", got, want)
	}
	if skipped != 2 { // .rcon-cli.env and .mcpanel-tmp-x
		t.Fatalf("skipped = %d, want 2", skipped)
	}
	for _, it := range items {
		switch it.name {
		case "server.properties":
			if strings.Contains(it.body, "s3cret") || !strings.Contains(it.body, "rcon.password=********") {
				t.Fatalf("server.properties not redacted: %q", it.body)
			}
		case "a.txt":
			if it.mode&0o111 == 0 || it.typ != tar.TypeReg {
				t.Fatalf("a.txt = %+v", it)
			}
		case "world/":
			if it.typ != tar.TypeDir {
				t.Fatalf("world/ = %+v", it)
			}
		}
	}
}

// TestBackupTarSwappedFolderStaysInside (review focus #2): a running server swaps a folder for a
// symlink to the neighbour instance while the walk is under way; nothing of the neighbour may end
// up in the stream.
func TestBackupTarSwappedFolderStaysInside(t *testing.T) {
	s, data := newService(t)
	other := filepath.Join(s.cfg.InstancesDir, "instances", "2")
	put(t, other, "secret.txt", "other server")
	put(t, data, "world/level.dat", "mine")
	put(t, data, "zz/after.txt", "z")
	swapped := false
	hook := func(p string) {
		if p == "world/level.dat" && !swapped { // world/ was listed, now swap it
			swapped = true
			if err := os.Rename(filepath.Join(data, "world"), filepath.Join(data, "world-old")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(data, "world")); err != nil {
				t.Fatal(err)
			}
		}
	}
	b, _, err := backupTar(t, s, hook)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range readTar(t, b) {
		if strings.Contains(it.body, "other server") || strings.Contains(it.name, "secret") {
			t.Fatalf("the neighbour's data is in the backup: %+v", it)
		}
	}
	if !swapped {
		t.Fatal("hook did not run")
	}
}

func TestBackupTarSwappedFileIsSkipped(t *testing.T) {
	s, data := newService(t)
	outside := filepath.Join(t.TempDir(), "host-secret")
	if err := os.WriteFile(outside, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	put(t, data, "a.txt", "a")
	put(t, data, "b.txt", "b")
	hook := func(p string) {
		if p == "b.txt" {
			_ = os.Remove(filepath.Join(data, "b.txt"))
			_ = os.Symlink(outside, filepath.Join(data, "b.txt"))
		}
	}
	b, skipped, err := backupTar(t, s, hook)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range readTar(t, b) {
		if it.body == "host" {
			t.Fatalf("followed the swapped-in symlink: %+v", it)
		}
	}
	if skipped == 0 {
		t.Fatal("the swapped file was not counted as skipped")
	}
}

func TestBackupTarSkipsSymlinks(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "a")
	if err := os.Symlink("a.txt", filepath.Join(data, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(data, "dirlink")); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(readTar(t, b)); len(got) != 1 || got[0] != "a.txt" || skipped != 2 {
		t.Fatalf("entries %v, skipped %d", got, skipped)
	}
}

// TestBackupTarShrinkingFile: a file that changes size between Stat and read keeps the size of
// its Stat (padded with zeros or cut), so the tar stays well-formed.
func TestBackupTarShrinkingFile(t *testing.T) {
	s, data := newService(t)
	put(t, data, "log.txt", "0123456789")
	// The hook runs before the entry's Lstat and open: the header carries the new size.
	b, _, err := backupTar(t, s, func(p string) {
		if p == "log.txt" {
			_ = os.Truncate(filepath.Join(data, "log.txt"), 4)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	items := readTar(t, b)
	if len(items) != 1 || items[0].body != "0123" {
		t.Fatalf("items = %+v", items)
	}
	// Padding path: copyPadded fills a short reader with zeros up to the announced size.
	var buf bytes.Buffer
	if err := copyPadded(&buf, strings.NewReader("ab"), 5); err != nil || buf.String() != "ab\x00\x00\x00" {
		t.Fatalf("copyPadded = %q %v", buf.String(), err)
	}
	buf.Reset()
	if err := copyPadded(&buf, strings.NewReader("abcdefgh"), 3); err != nil || buf.String() != "abc" {
		t.Fatalf("copyPadded (grown) = %q %v", buf.String(), err)
	}
}

func TestBackupTarLimits(t *testing.T) {
	s, data := newService(t, func(c *Config) { c.Limits.MaxBackupEntries = 3; c.Limits.MaxBackupBytes = 10 })
	put(t, data, "a", "12345")
	put(t, data, "b", "12345")
	if _, _, err := backupTar(t, s, nil); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	put(t, data, "c", "1")
	if _, _, err := backupTar(t, s, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("over the byte limit: %v", err)
	}
	s2, data2 := newService(t, func(c *Config) { c.Limits.MaxBackupEntries = 2 })
	put(t, data2, "x/y/z", "")
	if _, _, err := backupTar(t, s2, nil); !errors.Is(err, filesapi.ErrTooLarge) {
		t.Fatalf("over the entry limit: %v", err)
	}
}
```

`internal/files/tarwrite_linux_test.go`:

```go
//go:build linux

package files

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestBackupTarSkipsSpecialFiles(t *testing.T) {
	s, data := newService(t)
	put(t, data, "a.txt", "a")
	if err := syscall.Mkfifo(filepath.Join(data, "fifo"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "mcs") // socket paths are short
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Rename(filepath.Join(dir, "s"), filepath.Join(data, "sock")); err != nil {
		t.Fatal(err)
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(readTar(t, b)); len(got) != 1 || got[0] != "a.txt" || skipped != 2 {
		t.Fatalf("entries %v, skipped %d", got, skipped)
	}
}

// TestBackupTarBadNames: names that filesapi.CleanPath refuses (control characters, invalid
// UTF-8, a backslash) are left out with their content - Linux allows any byte but '/' and NUL.
func TestBackupTarBadNames(t *testing.T) {
	s, data := newService(t)
	put(t, data, "ok.txt", "ok")
	for _, n := range []string{"tab\tname", "bad\xffutf8", `back\slash`} {
		put(t, data, n+"/inner.txt", "x")
	}
	b, skipped, err := backupTar(t, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := tarNames(readTar(t, b)); len(got) != 1 || got[0] != "ok.txt" || skipped != 3 {
		t.Fatalf("entries %v, skipped %d", got, skipped)
	}
}
```

- [ ] **Step 6: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`undefined: writeBackupTar`, `copyPadded`).

- [ ] **Step 7: `tarwrite.go` implementieren**

```go
package files

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// errSkipEntry: the entry vanished, was swapped or cannot be redacted; it is left out.
var errSkipEntry = errors.New("entry left out")

// backupBudget counts a backup stream against max_backup_entries and max_backup_bytes.
type backupBudget struct {
	lim            config.FilesLimits
	entries, bytes int64
}

func (b *backupBudget) add(size int64) error {
	b.entries++
	if b.entries > b.lim.MaxBackupEntries {
		return fmt.Errorf("%w: the data directory has more than %d entries", filesapi.ErrTooLarge, b.lim.MaxBackupEntries)
	}
	if size > b.lim.MaxBackupBytes-b.bytes {
		return fmt.Errorf("%w: the data directory holds more than %d bytes", filesapi.ErrTooLarge, b.lim.MaxBackupBytes)
	}
	b.bytes += size
	return nil
}

// writeBackupTar streams the data directory of root as a PAX tar (spec 4 §4.2): a sorted
// depth-first walk that never follows a symlink (readDirSorted opens folders with openDir,
// files go through openRegular), only folders and regular files, names relative to the root,
// no owners. Symlinks, special files, hidden entries, names that filesapi.CleanPath refuses and
// entries that vanish or are swapped during the walk are left out (onSkip). server.properties at
// the top level is redacted. hook (tests) runs before each entry; onEntry reports each written
// entry with its content size.
func writeBackupTar(ctx context.Context, root *os.Root, w io.Writer, lim config.FilesLimits, hook func(string), onEntry func(int64), onSkip func()) error {
	tw := tar.NewWriter(w)
	b := &backupBudget{lim: lim}
	if err := walkBackup(ctx, root, "", tw, b, hook, onEntry, onSkip); err != nil {
		return err
	}
	return tw.Close()
}

func walkBackup(ctx context.Context, root *os.Root, dir string, tw *tar.Writer, b *backupBudget, hook func(string), onEntry func(int64), onSkip func()) error {
	entries, err := readDirSorted(root, dir, b.lim.MaxBackupEntries-b.entries)
	if err != nil {
		if dir != "" && (errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular)) {
			onSkip() // the folder vanished or was swapped after it was listed
			return nil
		}
		return err
	}
	for _, name := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filesapi.Join(dir, name)
		if hook != nil {
			hook(p)
		}
		if clean, cerr := filesapi.CleanPath(p); cerr != nil || clean != p || filesapi.IsHidden(p) {
			onSkip()
			continue
		}
		info, err := root.Lstat(p)
		if err != nil {
			onSkip() // vanished meanwhile
			continue
		}
		switch {
		case info.IsDir():
			if err := b.add(0); err != nil {
				return err
			}
			if err := tw.WriteHeader(tarHeader(p+"/", tar.TypeDir, info, 0)); err != nil {
				return err
			}
			onEntry(0)
			if err := walkBackup(ctx, root, p, tw, b, hook, onEntry, onSkip); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			n, err := writeTarFile(tw, root, p, b)
			if errors.Is(err, errSkipEntry) {
				onSkip()
				continue
			}
			if err != nil {
				return err
			}
			onEntry(n)
		default:
			onSkip() // symlink, FIFO, device, socket
		}
	}
	return nil
}

// readDirSorted reads all names of a real folder (openDir: no symlink, no FIFO) and closes it
// before returning, so a deep walk keeps one folder open at a time. More than limit names end
// the walk with ErrTooLarge before anything else is read.
func readDirSorted(root *os.Root, dir string, limit int64) ([]string, error) {
	f, _, err := openDir(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	for {
		batch, err := f.Readdirnames(walkBatch)
		names = append(names, batch...)
		if int64(len(names)) > limit {
			return nil, fmt.Errorf("%w: the data directory has too many entries for a backup", filesapi.ErrTooLarge)
		}
		if errors.Is(err, io.EOF) || (err == nil && len(batch) == 0) {
			break
		}
		if err != nil {
			return nil, fsErr(err)
		}
	}
	slices.Sort(names)
	return names, nil
}

// writeTarFile writes one regular file: exactly the size its Stat after the open reported
// (a file that shrinks is padded with zeros, one that grows is cut), server.properties
// redacted. A file that vanished, was swapped or cannot be redacted is errSkipEntry.
func writeTarFile(tw *tar.Writer, root *os.Root, p string, b *backupBudget) (int64, error) {
	if isServerProps(p) {
		data, st, err := readSmall(root, p, b.lim.MaxTextBytes)
		if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) || errors.Is(err, filesapi.ErrTooLarge) {
			return 0, errSkipEntry
		}
		if err != nil {
			return 0, err
		}
		red := redactProperties(string(data))
		if err := b.add(int64(len(red))); err != nil {
			return 0, err
		}
		if err := tw.WriteHeader(tarHeader(p, tar.TypeReg, st, int64(len(red)))); err != nil {
			return 0, err
		}
		_, err = io.WriteString(tw, red)
		return int64(len(red)), err
	}
	f, st, err := openRegular(root, p, os.O_RDONLY)
	if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
		return 0, errSkipEntry
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	size := st.Size()
	if err := b.add(size); err != nil {
		return 0, err
	}
	if err := tw.WriteHeader(tarHeader(p, tar.TypeReg, st, size)); err != nil {
		return 0, err
	}
	if err := copyPadded(tw, f, size); err != nil {
		return 0, fsErr(err)
	}
	return size, nil
}

// copyPadded copies exactly size bytes: what r has, then zeros (the file shrank meanwhile).
func copyPadded(w io.Writer, r io.Reader, size int64) error {
	n, err := io.CopyN(w, r, size)
	if errors.Is(err, io.EOF) {
		_, err = io.CopyN(w, zeroFill{}, size-n)
	}
	return err
}

// zeroFill yields zero bytes forever.
type zeroFill struct{}

func (zeroFill) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// tarHeader: PAX, no owner names or ids, permission bits and mtime of info.
func tarHeader(name string, typ byte, info fs.FileInfo, size int64) *tar.Header {
	return &tar.Header{
		Typeflag: typ, Name: name, Size: size, Mode: int64(info.Mode().Perm()),
		ModTime: info.ModTime(), Format: tar.FormatPAX,
	}
}
```

Hinweis zum Test `TestBackupTarShrinkingFile`: der Hook läuft vor `Lstat`/`openRegular`; die Kürzung landet daher schon im `Stat` (Größe 4). Der Auffüll-Pfad selbst ist über `copyPadded` direkt abgedeckt.

- [ ] **Step 8: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test ./internal/files -run 'TestBackupTar'`)
Expected: PASS (die `_linux`-Tests laufen nur auf der VM).

- [ ] **Step 9: Backup-Job und Liste – failing tests**

`internal/files/backup_test.go`:

```go
package files

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestCreateBackupRunsResticWithTheStream(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "level")
	put(t, data, "server.properties", "rcon.password=s3cret\n")
	job, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual, Note: "vor Update"})
	if err != nil || job.Kind != filesapi.JobBackup || job.State != jobRunning {
		t.Fatalf("create = %+v %v", job, err)
	}
	done := waitJob(t, s, 1)
	if done.State != jobDone || !filesapi.ValidSnapshotID(done.Snapshot) || done.Entries != 3 {
		t.Fatalf("job = %+v", done)
	}
	backups := f.callsOf("backup")
	if len(backups) != 1 {
		t.Fatalf("backup calls = %d", len(backups))
	}
	want := []string{"--no-cache", "--json", "backup", "--stdin", "--stdin-filename", "data.tar", "--host", "mcpanel",
		"--tag", "mcpanel", "--tag", "kind=manual", "--tag", noteTag("vor Update")}
	if !slices.Equal(backups[0].Args, want) {
		t.Fatalf("args = %q\nwant   %q", backups[0].Args, want)
	}
	if len(f.callsOf("init")) != 1 || len(f.callsOf("unlock")) != 1 {
		t.Fatalf("init %d, unlock %d", len(f.callsOf("init")), len(f.callsOf("unlock")))
	}
	snaps := f.snaps(s.repoDir(1))
	if len(snaps) != 1 || strings.Contains(string(snaps[0].Data), "s3cret") || !strings.Contains(string(snaps[0].Data), "level") {
		t.Fatalf("snapshot data = %q", snaps[0].Data)
	}
	if !f.sawEOF[0] {
		t.Fatal("restic did not see the end of the stream")
	}

	list, err := s.Snapshots(ctx, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("snapshots = %+v %v", list, err)
	}
	sn := list[0]
	if sn.ID != done.Snapshot || sn.ShortID != sn.ID[:8] || sn.Kind != filesapi.SnapshotManual || sn.Note != "vor Update" ||
		sn.Pinned || sn.Bytes != int64(len(snaps[0].Data)) || sn.Time != snaps[0].Time.UnixMilli() {
		t.Fatalf("snapshot = %+v", sn)
	}
	if c := f.callsOf("snapshots"); !slices.Equal(c[0].Args, []string{"--no-cache", "--json", "--no-lock", "snapshots", "--host", "mcpanel", "--tag", "mcpanel"}) {
		t.Fatalf("snapshots args = %q", c[0].Args)
	}
}

func TestCreateBackupValidation(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newBackupService(t)
	for _, req := range []filesapi.CreateBackupRequest{
		{ID: 1, Kind: "weekly"},
		{ID: 1, Kind: filesapi.SnapshotScheduled, Note: "only manual backups carry a note"},
		{ID: 1, Kind: filesapi.SnapshotManual, Note: "bad\nnote"},
		{ID: 1, Kind: filesapi.SnapshotManual, Note: strings.Repeat("ä", 65)},
		{ID: 0, Kind: filesapi.SnapshotManual},
	} {
		if _, err := s.CreateBackup(ctx, req); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%+v: %v, want invalid", req, err)
		}
	}
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 7, Kind: filesapi.SnapshotManual}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("no data directory: %v", err)
	}
}

func TestBackupNeedsFreeSpace(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 9} // below the 1 KiB test reserve
	s, _, f := newBackupService(t, func(c *Config) { c.Disk = disk })
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("full disk: %v", err)
	}
	if len(f.callsOf("backup")) != 0 {
		t.Fatal("restic ran on a full disk")
	}
	if err := s.jobs.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatalf("slot leaked: %v", err)
	}
}

// TestBackupTooLargeLeavesNoSnapshot (review focus #2): a stream over the limit kills restic
// before its stdin ends, so no snapshot of a cut stream exists.
func TestBackupTooLargeLeavesNoSnapshot(t *testing.T) {
	s, data, f := newBackupService(t, func(c *Config) { c.Limits.MaxBackupBytes = 4 })
	put(t, data, "a", "12345")
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "more than 4 bytes") {
		t.Fatalf("job = %+v", job)
	}
	if len(f.snaps(s.repoDir(1))) != 0 || len(f.sawEOF) != 1 || f.sawEOF[0] {
		t.Fatalf("snapshots %d, sawEOF %v: restic must be killed before the stream ends", len(f.snaps(s.repoDir(1))), f.sawEOF)
	}
}

func TestBackupResticFailureIsReported(t *testing.T) {
	s, _, f := newBackupService(t)
	f.fail["backup"] = &resticExit{Code: 1, Stderr: "Fatal: unable to save snapshot: disk full in " + s.repoDir(1) + "\n"}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "restic backup failed") || strings.Contains(job.Error, s.cfg.InstancesDir) {
		t.Fatalf("job = %+v", job)
	}
}

func TestBackupMissingKeyFails(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=manual") // repository exists, key does not
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed || !strings.Contains(job.Error, "key") {
		t.Fatalf("job = %+v", job)
	}
}

func TestSnapshotsWithoutRepoAndFiltering(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if list, err := s.Snapshots(ctx, 1); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("no repo: %v %v", list, err)
	}
	if len(f.callsOf("snapshots")) != 0 {
		t.Fatal("restic was asked although no repository exists")
	}
	repo := s.repoDir(1)
	t0 := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)
	old := f.addSnap(t, repo, t0, []byte("x"), "mcpanel", "kind=scheduled", "pinned")
	newer := f.addSnap(t, repo, t0.Add(time.Hour), []byte("xy"), "mcpanel", "kind=pre-restore")
	f.addSnap(t, repo, t0.Add(2*time.Hour), nil, "mcpanel")                     // no kind: ignored
	f.addSnap(t, repo, t0.Add(3*time.Hour), nil, "mcpanel", "kind=hourly")      // unknown kind: ignored
	f.addSnap(t, repo, t0.Add(4*time.Hour), nil, "mcpanel", "kind=manual", "note=!!") // broken note: no note
	list, err := s.Snapshots(ctx, 1)
	if err != nil || len(list) != 3 {
		t.Fatalf("list = %+v %v", list, err)
	}
	if list[1].ID != newer || list[2].ID != old || !list[2].Pinned || list[2].Kind != filesapi.SnapshotScheduled || list[0].Note != "" {
		t.Fatalf("order/fields = %+v", list)
	}
	if _, err := s.findSnapshot(ctx, 1, strings.Repeat("a", 64)); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
}

func TestNoteTagRoundTrip(t *testing.T) {
	for _, n := range []string{"vor Update auf 1.21.11", "ä/ö=ü,+", strings.Repeat("x", 64)} {
		tag := noteTag(n)
		if strings.ContainsAny(tag[len("note="):], ",=/+") {
			t.Fatalf("tag %q carries characters restic would split or that are not base64url", tag)
		}
		if got := parseNote(tag[len("note="):]); got != n {
			t.Fatalf("round trip %q -> %q", n, got)
		}
	}
}

func TestBackupsCancelWhileRunning(t *testing.T) {
	s, data, f := newBackupService(t)
	f.gate = make(chan struct{}) // the backup waits before it reads its stdin
	put(t, data, "a", "a")
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("second backup: %v", err)
	}
	s.Close() // shutdown: the job ends as failed, no snapshot
	job := s.Job(1)
	if job.State != jobFailed || len(f.snaps(s.repoDir(1))) != 0 {
		t.Fatalf("after close: %+v, %d snapshots", job, len(f.snaps(s.repoDir(1))))
	}
}
```

In `internal/files/server_backups_test.go` anhängen:

```go
func TestServerBackupsCreateAndList(t *testing.T) {
	s, data, _ := newBackupService(t)
	put(t, data, "a.txt", "a")
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	status, body := postJSON(t, ts, filesapi.PathBackupsCreate, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual})
	if status != http.StatusAccepted {
		t.Fatalf("create: %d %s", status, body)
	}
	waitJob(t, s, 1)
	status, body = postJSON(t, ts, filesapi.PathBackupsList, filesapi.IDRequest{ID: 1})
	var res filesapi.SnapshotsResponse
	if status != http.StatusOK || json.Unmarshal(body, &res) != nil || len(res.Snapshots) != 1 {
		t.Fatalf("list: %d %s", status, body)
	}
	status, body = postJSON(t, ts, filesapi.PathBackupsCreate, map[string]any{"id": 1, "kind": "manual", "extra": 1})
	if status != http.StatusBadRequest || errCode(t, body) != filesapi.CodeInvalid {
		t.Fatalf("unknown field: %d %s", status, body)
	}
	// Without restic: 503 unavailable.
	s2, _ := newService(t)
	s2.InitBackups(t.Context())
	ts2 := startHTTP(t, NewServer(s2, "test", discardLog()))
	status, body = postJSON(t, ts2, filesapi.PathBackupsList, filesapi.IDRequest{ID: 1})
	if status != http.StatusServiceUnavailable || errCode(t, body) != filesapi.CodeUnavailable {
		t.Fatalf("list without restic: %d %s", status, body)
	}
}
```

- [ ] **Step 10: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`s.CreateBackup undefined`, `noteTag`, `parseNote`, …).

- [ ] **Step 11: `backup.go` implementieren**

```go
package files

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	resticHost     = "mcpanel"
	resticPanelTag = "mcpanel"
	kindTagPrefix  = "kind="
	noteTagPrefix  = "note="
	pinnedTag      = "pinned"
	stdinFilename  = "data.tar"
)

// noteTag encodes a note as a restic tag (base64url without padding: no ',' that restic would
// split at, spec 4 §4.1).
func noteTag(note string) string {
	return noteTagPrefix + base64.RawURLEncoding.EncodeToString([]byte(note))
}

// parseNote decodes the value of a note tag; anything broken is no note.
func parseNote(v string) string {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || !filesapi.ValidNote(string(b)) {
		return ""
	}
	return string(b)
}

// tzEnv is the TZ variable for restic (empty zone = none).
func tzEnv(zone string) []string {
	if zone == "" {
		return nil
	}
	return []string{"TZ=" + zone}
}

// CreateBackup checks the request and the free space and starts a backup job (spec 4 §4.2).
func (s *Service) CreateBackup(ctx context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	switch {
	case !req.Kind.Valid():
		return filesapi.Job{}, fmt.Errorf("%w: unknown snapshot kind %q", filesapi.ErrInvalid, req.Kind)
	case req.Note != "" && req.Kind != filesapi.SnapshotManual:
		return filesapi.Job{}, fmt.Errorf("%w: only manual backups carry a note", filesapi.ErrInvalid)
	case !filesapi.ValidNote(req.Note):
		return filesapi.Job{}, fmt.Errorf("%w: the note must be at most %d characters without control characters", filesapi.ErrInvalid, filesapi.MaxNoteRunes)
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.jobs.acquire(req.ID, filesapi.JobBackup); err != nil {
		return filesapi.Job{}, err
	}
	ownSlot := true
	defer func() {
		if ownSlot {
			s.jobs.release(req.ID)
		}
	}()
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
	// Deduplication makes the growth of the repository unknown up front: only the reserve is
	// checked (spec 4 §4.2); a disk that fills up anyway fails the job in restic.
	avail, err := s.disk.Available(s.backupsDir())
	if err != nil {
		return filesapi.Job{}, fmt.Errorf("check free space: %w", err)
	}
	if avail < uint64(max(s.cfg.Limits.ReserveBytes, 0)) {
		return filesapi.Job{}, fmt.Errorf("%w: %d bytes free for backups, the reserve is %d bytes",
			filesapi.ErrInsufficientStorage, avail, s.cfg.Limits.ReserveBytes)
	}
	ownSlot = false
	job, err := s.jobs.start(req.ID, jobSpec{kind: filesapi.JobBackup}, s.clock.Now, func(jctx context.Context) error {
		defer root.Close()
		err := s.runBackup(jctx, root, req)
		if err != nil {
			s.log.Warn("files: backup failed", "id", req.ID, "kind", req.Kind, "err", err)
		}
		return err
	})
	if err != nil {
		return filesapi.Job{}, err
	}
	handedOver = true
	return job, nil
}

// runBackup streams the data directory into `restic backup --stdin`. On every error restic is
// killed before its stdin is closed: a stream that ended early must never become a snapshot.
func (s *Service) runBackup(ctx context.Context, root *os.Root, req filesapi.CreateBackupRequest) error {
	if err := s.ensureRepo(ctx, req.ID); err != nil {
		return s.ctxErr(ctx, err)
	}
	if _, err := s.restic(ctx, req.ID, nil, "unlock"); err != nil {
		return s.ctxErr(ctx, err)
	}
	args := []string{"--no-cache", "--json", "backup", "--stdin", "--stdin-filename", stdinFilename,
		"--host", resticHost, "--tag", resticPanelTag, "--tag", kindTagPrefix + string(req.Kind)}
	if req.Note != "" {
		args = append(args, "--tag", noteTag(req.Note))
	}
	out := &limitedBuffer{max: resticStdoutMax}
	// TZ: restic stores the snapshot time in this zone; forget buckets days/weeks/months by it.
	p, err := s.runner.Start(ctx, resticCmd{Args: args, Env: append(s.resticEnv(req.ID), tzEnv(req.Timezone)...), Stdin: true, Stdout: out})
	if err != nil {
		return fmt.Errorf("start restic: %w", err)
	}
	stdin := p.Stdin()
	werr := writeBackupTar(ctx, root, stdin, s.cfg.Limits, s.backupHook,
		func(n int64) { s.jobs.progress(req.ID, n) }, func() { s.jobs.skip(req.ID) })
	if werr != nil {
		p.Kill() // first: restic must not see the end of the stream
		perr := p.Wait()
		_ = stdin.Close()
		var ex *resticExit
		switch {
		case ctx.Err() != nil:
			return s.jobs.stopErr()
		case errors.As(perr, &ex) && ex.Code != -1:
			return s.resticErr("backup", perr) // restic ended on its own: its error explains the broken pipe
		}
		return werr
	}
	if err := stdin.Close(); err != nil {
		p.Kill()
		_ = p.Wait()
		return fmt.Errorf("finish the backup stream: %w", err)
	}
	if err := p.Wait(); err != nil {
		return s.ctxErr(ctx, s.resticErr("backup", err))
	}
	sid, err := backupSnapshotID(out.Bytes())
	if err != nil {
		return err
	}
	s.jobs.setSnapshot(req.ID, sid)
	return nil
}

// ctxErr prefers the job's stop reason when its context ended (restic was interrupted).
func (s *Service) ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return s.jobs.stopErr()
	}
	return err
}

// backupSnapshotID reads the summary line of `restic backup --json`.
func backupSnapshotID(out []byte) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m struct {
			MessageType string `json:"message_type"`
			SnapshotID  string `json:"snapshot_id"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.MessageType == "summary" {
			if !filesapi.ValidSnapshotID(m.SnapshotID) {
				return "", errors.New("restic finished without a snapshot")
			}
			return m.SnapshotID, nil
		}
	}
	return "", errors.New("restic printed no backup summary")
}

// resticSnapshot is one element of `restic snapshots --json`.
type resticSnapshot struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
	Summary *struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
	} `json:"summary"`
}

// toSnapshot converts a restic snapshot; snapshots without the panel tag or a known kind are
// not the panel's (false).
func toSnapshot(r resticSnapshot) (filesapi.Snapshot, bool) {
	if !filesapi.ValidSnapshotID(r.ID) || !slices.Contains(r.Tags, resticPanelTag) {
		return filesapi.Snapshot{}, false
	}
	sn := filesapi.Snapshot{ID: r.ID, ShortID: r.ID[:8], Time: r.Time.UnixMilli()}
	for _, t := range r.Tags {
		switch {
		case strings.HasPrefix(t, kindTagPrefix):
			sn.Kind = filesapi.SnapshotKind(strings.TrimPrefix(t, kindTagPrefix))
		case strings.HasPrefix(t, noteTagPrefix):
			sn.Note = parseNote(strings.TrimPrefix(t, noteTagPrefix))
		case t == pinnedTag:
			sn.Pinned = true
		}
	}
	if !sn.Kind.Valid() {
		return filesapi.Snapshot{}, false
	}
	if r.Summary != nil {
		sn.Bytes = r.Summary.TotalBytesProcessed
	}
	return sn, true
}

// Snapshots lists the panel's snapshots of server id, newest first (spec 4 §4.1); without a
// repository the list is empty and restic is not asked.
func (s *Service) Snapshots(ctx context.Context, id int64) ([]filesapi.Snapshot, error) {
	if err := s.requireBackups(id); err != nil {
		return nil, err
	}
	if !s.repoExists(id) {
		return []filesapi.Snapshot{}, nil
	}
	out, err := s.restic(ctx, id, nil, "--json", "--no-lock", "snapshots", "--host", resticHost, "--tag", resticPanelTag)
	if err != nil {
		return nil, err
	}
	var raw []resticSnapshot
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("restic snapshots printed invalid JSON: %w", err)
	}
	list := make([]filesapi.Snapshot, 0, len(raw))
	for _, r := range raw {
		if sn, ok := toSnapshot(r); ok {
			list = append(list, sn)
		}
	}
	slices.SortFunc(list, func(a, b filesapi.Snapshot) int {
		if c := cmp.Compare(b.Time, a.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return list, nil
}

// findSnapshot returns snapshot sid of server id (ErrNotFound if this repository has none).
func (s *Service) findSnapshot(ctx context.Context, id int64, sid string) (filesapi.Snapshot, error) {
	if !filesapi.ValidSnapshotID(sid) {
		return filesapi.Snapshot{}, fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	list, err := s.Snapshots(ctx, id)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	for _, sn := range list {
		if sn.ID == sid {
			return sn, nil
		}
	}
	return filesapi.Snapshot{}, fmt.Errorf("%w: no such snapshot", filesapi.ErrNotFound)
}
```

`internal/files/server_backups.go`: `backupRoutes` bekommt

```go
		filesapi.PathBackupsList:   {method: http.MethodPost, handler: s.backupsList},
		filesapi.PathBackupsCreate: {method: http.MethodPost, handler: s.backupsCreate},
```

und die Handler:

```go
// backupsList: restic reads the repository index, which can outlast the server's WriteTimeout
// (the client waits filesapi.BackupCallTimeout): longOp.
func (s *Server) backupsList(w http.ResponseWriter, r *http.Request) {
	var req filesapi.IDRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var list []filesapi.Snapshot
	err := s.longOp(w, "backups.list", func() error {
		var err error
		list, err = s.svc.Snapshots(r.Context(), req.ID)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.list", err)
		return
	}
	writeJSON(w, http.StatusOK, filesapi.SnapshotsResponse{Snapshots: list})
}

func (s *Server) backupsCreate(w http.ResponseWriter, r *http.Request) {
	var req filesapi.CreateBackupRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	job, err := s.svc.CreateBackup(r.Context(), req)
	if err != nil {
		s.fail(w, r, "backups.create", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
```

- [ ] **Step 12: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test -race ./internal/files -run 'TestCreateBackup|TestBackup|TestSnapshots|TestNoteTag|TestServerBackups'`)
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add internal/files/jobs.go internal/files/jobs_test.go internal/files/extract.go internal/files/service.go internal/files/tarwrite.go \
  internal/files/tarwrite_test.go internal/files/tarwrite_linux_test.go internal/files/backup.go internal/files/backup_test.go \
  internal/files/server_backups.go internal/files/server_backups_test.go
git commit -m "feat(files): backup jobs that stream the data directory into restic

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
```

---

### Task 6: Restore – tar-Entpacken, Tausch der obersten Ebene, Abbrechen

**Files:**
- Create: `internal/files/tarextract.go`, `internal/files/restore.go`
- Modify: `internal/files/jobs.go` (`cancelJob`, `beginCommit`), `internal/files/server_backups.go`
- Test: `internal/files/tarextract_test.go`, `internal/files/restore_test.go`, `internal/files/server_backups_test.go`

**Interfaces:**
- Consumes: Aufgaben 4–5 (`findSnapshot`, `restic`, `resticEnv`, `resticErr`, `ctxErr`, `jobs.start`, `jobSpec`, `stopErr`, `swapHook`, `fakeRestic.addSnap`, `fakeRestic.gate`), vorhanden `s.reserve`, `landingWriter`, `ctxReader`, `s.tempName`, `openDir`, `fsErr`, `dirMode`, `fileMode`, `noTemp`.
- Produces:

```go
func extractBackupTar(ctx context.Context, root *os.Root, dir string, r io.Reader, lim config.FilesLimits, res *reservation, onEntry func(int64)) error
func (s *Service) RestoreBackup(ctx context.Context, req filesapi.SnapshotRequest) (filesapi.Job, error)
func (s *Service) dump(ctx context.Context, id int64, sid string, consume func(io.Reader) error) error
func swapTop(root *os.Root, tmp string, hook func(step int) error) error
func (s *Service) CancelJob(id int64) error
func (j *jobs) cancelJob(id int64) error
func (j *jobs) beginCommit(ctx context.Context, id int64) bool
// tests: makeTar(t, ...tarEntry) []byte
```

- [ ] **Step 1: tar-Entpacken – failing tests**

`internal/files/tarextract_test.go`:

```go
package files

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type tarEntry struct {
	name     string
	typ      byte
	body     string
	size     int64 // -1 = len(body)
	mode     int64
	linkname string
}

// makeTar builds a tar exactly as given (lying sizes are written with tar.Writer's raw header
// and a short body: the stream then ends early).
func makeTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		size := e.size
		if size < 0 {
			size = int64(len(e.body))
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Size: size, Mode: mode, Linkname: e.linkname,
			ModTime: time.Unix(1_700_000_000, 0), Format: tar.FormatPAX}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil && size >= int64(len(e.body)) {
				t.Fatal(err)
			}
		}
		if size > int64(len(e.body)) {
			return buf.Bytes() // cut: the declared rest never comes
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func reg(name, body string) tarEntry { return tarEntry{name: name, typ: tar.TypeReg, body: body, size: -1} }
func dir(name string) tarEntry       { return tarEntry{name: name, typ: tar.TypeDir} }

func extractInto(t *testing.T, s *Service, b []byte) (string, error) {
	t.Helper()
	root, err := s.open(1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("out", dirMode); err != nil {
		t.Fatal(err)
	}
	res, err := s.reserve(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer res.release()
	return "out", extractBackupTar(context.Background(), root, "out", bytes.NewReader(b), s.cfg.Limits, res, func(int64) {})
}

func TestExtractBackupTarWritesFilesModesAndTimes(t *testing.T) {
	s, data := newService(t)
	b := makeTar(t, dir("world/"), reg("world/level.dat", "lvl"), tarEntry{name: "run.sh", typ: tar.TypeReg, body: "#!", size: -1, mode: 0o750},
		reg("deep/a/b.txt", "b")) // parents without own entries are created
	if _, err := extractInto(t, s, b); err != nil {
		t.Fatal(err)
	}
	if got, _ := get(t, data, "out/world/level.dat"); got != "lvl" {
		t.Fatalf("level.dat = %q", got)
	}
	if got, _ := get(t, data, "out/deep/a/b.txt"); got != "b" {
		t.Fatalf("b.txt = %q", got)
	}
	fi, _ := os.Stat(filepath.Join(data, "out/run.sh"))
	if fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("run.sh lost its execute bit: %v", fi.Mode())
	}
	fi, _ = os.Stat(filepath.Join(data, "out/world/level.dat"))
	if !fi.ModTime().Equal(time.Unix(1_700_000_000, 0)) || fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("level.dat = %v %v", fi.ModTime(), fi.Mode())
	}
}

// TestExtractBackupTarRefuses (review focus #3): a hostile or broken stream fails before or
// while writing, and never writes outside the target folder.
func TestExtractBackupTarRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		tar  func(t *testing.T) []byte
		want error
	}{
		{"symlink", func(t *testing.T) []byte { return makeTar(t, tarEntry{name: "l", typ: tar.TypeSymlink, linkname: "/etc"}) }, filesapi.ErrInvalid},
		{"hardlink", func(t *testing.T) []byte {
			return makeTar(t, reg("a", "x"), tarEntry{name: "b", typ: tar.TypeLink, linkname: "a"})
		}, filesapi.ErrInvalid},
		{"char device", func(t *testing.T) []byte { return makeTar(t, tarEntry{name: "c", typ: tar.TypeChar}) }, filesapi.ErrInvalid},
		{"fifo", func(t *testing.T) []byte { return makeTar(t, tarEntry{name: "f", typ: tar.TypeFifo}) }, filesapi.ErrInvalid},
		{"dotdot", func(t *testing.T) []byte { return makeTar(t, reg("../escape", "x")) }, filesapi.ErrInvalid},
		{"absolute", func(t *testing.T) []byte { return makeTar(t, reg("/etc/passwd", "x")) }, filesapi.ErrInvalid},
		{"double slash", func(t *testing.T) []byte { return makeTar(t, reg("a//b", "x")) }, filesapi.ErrInvalid},
		{"hidden temp", func(t *testing.T) []byte { return makeTar(t, reg(".mcpanel-x", "x")) }, filesapi.ErrInvalid},
		{"hidden rcon", func(t *testing.T) []byte { return makeTar(t, reg(".rcon-cli.env", "x")) }, filesapi.ErrInvalid},
		{"duplicate", func(t *testing.T) []byte { return makeTar(t, reg("a", "1"), reg("a", "2")) }, filesapi.ErrInvalid},
		{"file is a parent", func(t *testing.T) []byte { return makeTar(t, reg("a", "1"), reg("a/b", "2")) }, filesapi.ErrInvalid},
		{"cut stream", func(t *testing.T) []byte { return makeTar(t, tarEntry{name: "big", typ: tar.TypeReg, body: "abc", size: 100}) }, filesapi.ErrInvalid},
		{"garbage", func(*testing.T) []byte { return bytes.Repeat([]byte{0xff}, 1024) }, filesapi.ErrInvalid},
		{"too many entries", func(t *testing.T) []byte {
			var es []tarEntry
			for i := range 101 {
				es = append(es, reg("f"+string(rune('a'+i%26))+string(rune('a'+i/26)), ""))
			}
			return makeTar(t, es...)
		}, filesapi.ErrTooLarge},
		{"too many bytes", func(t *testing.T) []byte { return makeTar(t, reg("a", string(make([]byte, 1<<20+1)))) }, filesapi.ErrTooLarge},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, data := newService(t)
			_, err := extractInto(t, s, c.tar(t))
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			// Nothing outside out/ (the parent of the data directory stays untouched as well).
			for _, n := range names(t, data) {
				if n != "out" {
					t.Fatalf("written outside the target: %s", n)
				}
			}
			if _, err := os.Lstat(filepath.Join(s.cfg.InstancesDir, "instances", "escape")); err == nil {
				t.Fatal("escaped the data directory")
			}
		})
	}
}
```

- [ ] **Step 2: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`undefined: extractBackupTar`).

- [ ] **Step 3: `tarextract.go` implementieren**

```go
package files

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// extractBackupTar unpacks a backup stream into folder dir of root (a fresh temp folder of the
// restore, spec 4 §4.3) with the rules of the zip extract (spec 3b §4.2): clean relative names
// only, no hidden names, only folders and regular files, no duplicates, sizes re-counted
// against max_backup_bytes / max_backup_entries, files 0644 (0755 with an execute bit), folders
// 0755, times from the stream. Every violation is ErrInvalid or ErrTooLarge; the caller removes
// the temp folder.
func extractBackupTar(ctx context.Context, root *os.Root, dir string, r io.Reader, lim config.FilesLimits, res *reservation, onEntry func(int64)) error {
	tr := tar.NewReader(r)
	seen := map[string]bool{}
	var entries, total int64
	type dirTime struct {
		path string
		t    time.Time
	}
	var dirTimes []dirTime
	invalid := func(name string, err error) error {
		if fe := fsErr(err); errors.Is(fe, filesapi.ErrInsufficientStorage) {
			return fe
		}
		return fmt.Errorf("%w: entry %q: %w", filesapi.ErrInvalid, name, err)
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: the backup stream is damaged: %w", filesapi.ErrInvalid, err)
		}
		entries++
		if entries > lim.MaxBackupEntries {
			return fmt.Errorf("%w: the snapshot has more than %d entries", filesapi.ErrTooLarge, lim.MaxBackupEntries)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		clean, cerr := filesapi.CleanPath(name)
		switch {
		case cerr != nil:
			return fmt.Errorf("%w: entry %q: %w", filesapi.ErrInvalid, hdr.Name, cerr)
		case clean == "" || clean != name:
			return fmt.Errorf("%w: entry %q is not a clean relative path", filesapi.ErrInvalid, hdr.Name)
		case filesapi.IsHidden(clean):
			return fmt.Errorf("%w: entry %q uses a reserved name", filesapi.ErrInvalid, hdr.Name)
		case seen[clean]:
			return fmt.Errorf("%w: entry %q appears twice", filesapi.ErrInvalid, hdr.Name)
		}
		seen[clean] = true
		target := dir + "/" + clean
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, dirMode); err != nil { //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
				return invalid(hdr.Name, err)
			}
			dirTimes = append(dirTimes, dirTime{target, hdr.ModTime})
			onEntry(0)
		case tar.TypeReg:
			if hdr.Size < 0 || hdr.Size > lim.MaxBackupBytes-total {
				return fmt.Errorf("%w: the snapshot expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxBackupBytes)
			}
			total += hdr.Size
			if err := root.MkdirAll(parentOf(target), dirMode); err != nil { //nolint:gosec // G301: see above
				return invalid(hdr.Name, err)
			}
			mode := os.FileMode(fileMode)
			if hdr.Mode&0o111 != 0 {
				mode = dirMode // 0755: keep an execute bit (start scripts)
			}
			out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // G302/G304: new file in the restore's temp folder inside the instance's os.Root
			if err != nil {
				return invalid(hdr.Name, err)
			}
			n, err := io.Copy(landingWriter{w: out, res: res}, io.LimitReader(ctxReader{ctx: ctx, r: tr}, hdr.Size))
			cerr := out.Close()
			switch {
			case errors.Is(err, errShuttingDown):
				return ctx.Err()
			case err != nil:
				return invalid(hdr.Name, err)
			case n != hdr.Size:
				return fmt.Errorf("%w: entry %q is cut off", filesapi.ErrInvalid, hdr.Name)
			case cerr != nil:
				return fsErr(cerr)
			}
			_ = root.Chtimes(target, hdr.ModTime, hdr.ModTime)
			onEntry(n)
		default:
			return fmt.Errorf("%w: entry %q is neither a file nor a folder", filesapi.ErrInvalid, hdr.Name)
		}
	}
	sort.SliceStable(dirTimes, func(a, b int) bool {
		return strings.Count(dirTimes[a].path, "/") > strings.Count(dirTimes[b].path, "/")
	})
	for _, d := range dirTimes {
		_ = root.Chtimes(d.path, d.t, d.t)
	}
	return nil
}
```

(Ein `MkdirAll` unterhalb einer Datei meldet `ENOTDIR`/`EEXIST`; `invalid` macht daraus `ErrInvalid`, nur ein voller Datenträger bleibt `ErrInsufficientStorage`.)

- [ ] **Step 4: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test ./internal/files -run 'TestExtractBackupTar'`)
Expected: PASS.

- [ ] **Step 5: Restore-Job, Tausch, Abbrechen – failing tests**

`internal/files/restore_test.go`:

```go
package files

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// snapshotOf puts a tar as a snapshot of server 1 into the fake repository (with key).
func snapshotOf(t *testing.T, s *Service, f *fakeRestic, b []byte) string {
	t.Helper()
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	return f.addSnap(t, s.repoDir(1), time.Now(), b, "mcpanel", "kind=manual")
}

func TestRestoreReplacesTheTopLevel(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "new world")
	put(t, data, "only-now.txt", "gone after restore")
	put(t, data, ".rcon-cli.env", "password=keep\n")
	sid := snapshotOf(t, s, f, makeTar(t, dir("world/"), reg("world/level.dat", "old world"), reg("server.properties", "motd=old\n")))
	job, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid})
	if err != nil || job.Kind != filesapi.JobRestore || job.Snapshot != sid {
		t.Fatalf("restore = %+v %v", job, err)
	}
	if done := waitJob(t, s, 1); done.State != jobDone {
		t.Fatalf("job = %+v", done)
	}
	if got, _ := get(t, data, "world/level.dat"); got != "old world" {
		t.Fatalf("level.dat = %q", got)
	}
	if _, ok := get(t, data, "only-now.txt"); ok {
		t.Fatal("an entry that is not in the snapshot survived")
	}
	if got, _ := get(t, data, ".rcon-cli.env"); got != "password=keep\n" {
		t.Fatalf("hidden file changed: %q", got)
	}
	noTemp(t, data)
	if c := f.callsOf("dump"); len(c) != 1 || !slices.Equal(c[0].Args, []string{"--no-cache", "dump", sid, "/data.tar"}) {
		t.Fatalf("dump calls = %+v", c)
	}
}

// TestRestoreHostileSnapshotLeavesDataAlone (review focus #3).
func TestRestoreHostileSnapshotLeavesDataAlone(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "world/level.dat", "current")
	sid := snapshotOf(t, s, f, makeTar(t, reg("ok.txt", "x"), tarEntry{name: "evil", typ: '2', linkname: "/etc/shadow"}))
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "neither a file nor a folder") {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "world/level.dat"); got != "current" {
		t.Fatalf("data changed: %q", got)
	}
	if _, ok := get(t, data, "ok.txt"); ok {
		t.Fatal("part of a failed restore is visible")
	}
	noTemp(t, data)
}

func TestRestorePreChecks(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: "nothex"}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: strings.Repeat("a", 64)}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
	sid := snapshotOf(t, s, f, makeTar(t, reg("a", "x")))
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 9, Snapshot: sid}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("no data directory: %v", err)
	}
}

func TestRestoreNeedsFreeSpace(t *testing.T) {
	disk := &fakeDisk{avail: 1 << 40}
	s, _, f := newBackupService(t, func(c *Config) { c.Disk = disk })
	sid := snapshotOf(t, s, f, makeTar(t, reg("a", strings.Repeat("x", 4096))))
	disk.set(4096) // snapshot (≈5 KiB tar) + 1 KiB reserve do not fit
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); !errors.Is(err, filesapi.ErrInsufficientStorage) {
		t.Fatalf("full disk: %v", err)
	}
	if len(f.callsOf("dump")) != 0 {
		t.Fatal("dump ran on a full disk")
	}
}

func TestSwapRollsBackOnFailure(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current a")
	put(t, data, "b.txt", "current b")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old a"), reg("c.txt", "old c")))
	s.swapHook = func(step int) error {
		if step == 3 { // after both old entries moved away and one new one moved up
			return errors.New("simulated rename failure")
		}
		return nil
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed {
		t.Fatalf("job = %+v", job)
	}
	if a, _ := get(t, data, "a.txt"); a != "current a" {
		t.Fatalf("a.txt = %q (rollback)", a)
	}
	if b, _ := get(t, data, "b.txt"); b != "current b" {
		t.Fatalf("b.txt = %q (rollback)", b)
	}
	if _, ok := get(t, data, "c.txt"); ok {
		t.Fatal("c.txt of the snapshot is visible after the rollback")
	}
	noTemp(t, data)
}

func TestCancelJob(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	if err := s.CancelJob(1); err != nil {
		t.Fatalf("no job: %v", err)
	}
	f.gate = make(chan struct{})
	put(t, data, "a", "a")
	if _, err := s.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || job.Error != "the job was canceled" || len(f.snaps(s.repoDir(1))) != 0 {
		t.Fatalf("canceled backup: %+v", job)
	}
	// Extract jobs cannot be canceled.
	f.gate = nil
	putBytes(t, data, "w.zip", makeZip(t, zipEntry{name: "x.txt", body: "x"}))
	release := make(chan struct{})
	s.jobs.hook = func(ctx context.Context, _ int64, _ string) {
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	if _, err := s.Extract(ctx, filesapi.ExtractRequest{ID: 1, Path: "w.zip", Dest: "w"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelJob(1); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("cancel extract: %v", err)
	}
	close(release)
	waitJob(t, s, 1)
}

func TestCancelRestoreBeforeAndAfterCommit(t *testing.T) {
	ctx := context.Background()
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "current")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "old")))

	// Before the swap: the data directory stays.
	f.gate = make(chan struct{})
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobFailed || job.Error != "the job was canceled" {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "current" {
		t.Fatalf("a.txt = %q", got)
	}
	noTemp(t, data)

	// During the swap: cancel has no effect, the restore completes.
	f.gate = nil
	entered := make(chan struct{})
	proceed := make(chan struct{})
	s.swapHook = func(step int) error {
		if step == 0 {
			close(entered)
			<-proceed
		}
		return nil
	}
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := s.CancelJob(1); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if job := waitJob(t, s, 1); job.State != jobDone {
		t.Fatalf("job after a late cancel = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "old" {
		t.Fatalf("a.txt = %q", got)
	}
}
```

In `internal/files/server_backups_test.go` anhängen:

```go
func TestServerRestoreAndCancel(t *testing.T) {
	s, data, f := newBackupService(t)
	put(t, data, "a.txt", "now")
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "then")))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))
	status, body := postJSON(t, ts, filesapi.PathBackupsRestore, filesapi.SnapshotRequest{ID: 1, Snapshot: sid})
	if status != http.StatusAccepted {
		t.Fatalf("restore: %d %s", status, body)
	}
	waitJob(t, s, 1)
	if got, _ := get(t, data, "a.txt"); got != "then" {
		t.Fatalf("a.txt = %q", got)
	}
	status, body = postJSON(t, ts, filesapi.PathBackupsRestore, filesapi.SnapshotRequest{ID: 1, Snapshot: strings.Repeat("b", 64)})
	if status != http.StatusNotFound || errCode(t, body) != filesapi.CodeNotFound {
		t.Fatalf("unknown snapshot: %d %s", status, body)
	}
	if status, body = postJSON(t, ts, filesapi.PathJobsCancel, filesapi.IDRequest{ID: 1}); status != http.StatusNoContent {
		t.Fatalf("cancel without job: %d %s", status, body)
	}
}
```

(Import `strings` in `server_backups_test.go` ergänzen.)

- [ ] **Step 6: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`s.RestoreBackup undefined`, `s.CancelJob`, …).

- [ ] **Step 7: `jobs.go` ergänzen**

```go
// cancelJob cancels the running job of instance id (spec 4 §4.5): nothing running is fine; an
// extract cannot be cancelled; a restore that started its swap is not interrupted any more.
func (j *jobs) cancelJob(id int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.last[id]
	switch {
	case job == nil || job.State != jobRunning:
		return nil
	case job.Kind == filesapi.JobExtract:
		return fmt.Errorf("%w: an extract job cannot be canceled", filesapi.ErrInvalid)
	case j.committed[id]:
		return nil
	}
	if c := j.cancels[id]; c != nil {
		c()
	}
	return nil
}

// beginCommit marks the restore of instance id as swapping: from now on cancelJob has no
// effect. false = it was cancelled (or the service shuts down) before.
func (j *jobs) beginCommit(ctx context.Context, id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	j.committed[id] = true
	return true
}
```

- [ ] **Step 8: `restore.go` implementieren**

```go
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// RestoreBackup checks the snapshot and the free space synchronously and starts a restore job
// (spec 4 §4.3). The panel stops the server before; the service does not check that.
func (s *Service) RestoreBackup(ctx context.Context, req filesapi.SnapshotRequest) (filesapi.Job, error) {
	if !filesapi.ValidSnapshotID(req.Snapshot) {
		return filesapi.Job{}, fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.jobs.acquire(req.ID, filesapi.JobRestore); err != nil {
		return filesapi.Job{}, err
	}
	ownSlot := true
	defer func() {
		if ownSlot {
			s.jobs.release(req.ID)
		}
	}()
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
	snap, err := s.findSnapshot(ctx, req.ID, req.Snapshot)
	if err != nil {
		return filesapi.Job{}, err
	}
	res, err := s.reserve(req.ID, snap.Bytes)
	if err != nil {
		return filesapi.Job{}, err
	}
	if err := ctx.Err(); err != nil { // the caller gave up during the pre-check
		res.release()
		return filesapi.Job{}, err
	}
	ownSlot = false
	job, err := s.jobs.start(req.ID, jobSpec{kind: filesapi.JobRestore, snapshot: snap.ID, totalBytes: snap.Bytes}, s.clock.Now,
		func(jctx context.Context) error {
			defer root.Close()
			defer res.release()
			err := s.runRestore(jctx, root, req.ID, snap.ID, res)
			if err != nil {
				s.log.Warn("files: restore failed", "id", req.ID, "snapshot", snap.ID, "err", err)
			}
			return err
		})
	if err != nil {
		res.release()
		return filesapi.Job{}, err
	}
	handedOver = true
	return job, nil
}

func (s *Service) runRestore(ctx context.Context, root *os.Root, id int64, sid string, res *reservation) error {
	if _, err := s.restic(ctx, id, nil, "unlock"); err != nil {
		return s.ctxErr(ctx, err)
	}
	tmp, err := s.tempName("")
	if err != nil {
		return err
	}
	if err := root.Mkdir(tmp, dirMode); err != nil { //nolint:gosec // G301: temp folder, 0755 like extract
		return fsErr(err)
	}
	defer func() { _ = root.RemoveAll(tmp) }() // after a swap only the old entries are left in it
	if err := root.Mkdir(tmp+"/new", dirMode); err != nil { //nolint:gosec // G301: see above
		return fsErr(err)
	}
	err = s.dump(ctx, id, sid, func(r io.Reader) error {
		return extractBackupTar(ctx, root, tmp+"/new", r, s.cfg.Limits, res, func(n int64) { s.jobs.progress(id, n) })
	})
	if err != nil {
		return s.ctxErr(ctx, err)
	}
	if !s.jobs.beginCommit(ctx, id) {
		return s.jobs.stopErr()
	}
	return swapTop(root, tmp, s.swapHook)
}

// dump streams snapshot sid of server id (`restic dump <id> /data.tar`) into consume. When
// consume fails, restic is killed; when it succeeds, the rest of the stream is drained so restic
// can finish, and restic's own exit decides. A failure of restic itself wins over the damaged
// stream it causes.
func (s *Service) dump(ctx context.Context, id int64, sid string, consume func(io.Reader) error) error {
	pr, pw := io.Pipe()
	p, err := s.runner.Start(ctx, resticCmd{Args: []string{"--no-cache", "dump", sid, "/" + stdinFilename}, Env: s.resticEnv(id), Stdout: pw})
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("start restic: %w", err)
	}
	waited := make(chan error, 1)
	go func() {
		err := p.Wait()
		_ = pw.CloseWithError(err) // nil: EOF for the reader
		waited <- err
	}()
	cerr := consume(pr)
	if cerr == nil {
		_, cerr = io.Copy(io.Discard, pr)
	}
	if cerr != nil {
		p.Kill()
		_ = pr.CloseWithError(cerr)
	}
	werr := <-waited
	var ex *resticExit
	switch {
	case werr != nil && errors.As(werr, &ex) && ex.Code != -1:
		return s.resticErr("dump", werr)
	case cerr != nil:
		return cerr
	case werr != nil:
		return s.resticErr("dump", werr)
	}
	return nil
}

// visibleNames lists the names of folder dir that are not hidden (spec 4 §4.3: hidden entries
// such as .rcon-cli.env and the service's temp folders stay where they are).
func visibleNames(root *os.Root, dir string) ([]string, error) {
	f, _, err := openDir(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	all, err := f.Readdirnames(-1)
	if err != nil {
		return nil, fsErr(err)
	}
	out := slices.DeleteFunc(all, func(n string) bool { return filesapi.IsHidden(n) })
	slices.Sort(out)
	return out, nil
}

// swapTop replaces the visible top-level entries of the data directory by the entries of
// <tmp>/new (renames only, no copying): the current ones move to <tmp>/old first, then the new
// ones move up. A failing rename undoes the renames done so far, newest first (best effort).
// hook (tests) runs before every rename with its step number.
func swapTop(root *os.Root, tmp string, hook func(step int) error) error {
	old, nw := tmp+"/old", tmp+"/new"
	if err := root.Mkdir(old, dirMode); err != nil { //nolint:gosec // G301: temp folder
		return fsErr(err)
	}
	cur, err := visibleNames(root, "")
	if err != nil {
		return err
	}
	next, err := visibleNames(root, nw)
	if err != nil {
		return err
	}
	type move struct{ from, to string }
	var plan []move
	for _, n := range cur {
		plan = append(plan, move{n, old + "/" + n})
	}
	for _, n := range next {
		plan = append(plan, move{nw + "/" + n, n})
	}
	for i, m := range plan {
		var err error
		if hook != nil {
			err = hook(i)
		}
		if err == nil {
			err = root.Rename(m.from, m.to)
		}
		if err != nil {
			for k := i - 1; k >= 0; k-- {
				_ = root.Rename(plan[k].to, plan[k].from)
			}
			return fmt.Errorf("swap the restored files into place: %w", fsErr(err))
		}
	}
	return nil
}

// CancelJob cancels the running job of server id (spec 4 §4.5).
func (s *Service) CancelJob(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	return s.jobs.cancelJob(id)
}
```

`internal/files/server_backups.go`: Routen

```go
		filesapi.PathBackupsRestore: {method: http.MethodPost, handler: s.backupsRestore},
		filesapi.PathJobsCancel:     {method: http.MethodPost, handler: s.cancelJob},
```

und Handler:

```go
// backupsRestore: the pre-check lists the snapshots (restic reads the index): longOp.
func (s *Server) backupsRestore(w http.ResponseWriter, r *http.Request) {
	var req filesapi.SnapshotRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var job filesapi.Job
	err := s.longOp(w, "backups.restore", func() error {
		var err error
		job, err = s.svc.RestoreBackup(r.Context(), req)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.restore", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	var req filesapi.IDRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.CancelJob(req.ID); err != nil {
		s.fail(w, r, "jobs.cancel", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

Hinweis zur Cleanup-Garantie: der Temp-Ordner trägt das Prozess-Token (`s.tempName`); stirbt der Dienst mitten im Tausch, entfernt `CleanupTemp` beim nächsten Start den Rest (TP3b §3.3), der Pre-Restore-Snapshot des panels stellt den Stand wieder her (Spec §4.3 Punkt 4).

- [ ] **Step 9: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test -race ./internal/files -run 'TestRestore|TestSwap|TestCancel|TestServerRestore|TestExtract|TestJobs'`)
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/files/tarextract.go internal/files/tarextract_test.go internal/files/restore.go internal/files/restore_test.go \
  internal/files/jobs.go internal/files/server_backups.go internal/files/server_backups_test.go internal/files/tarwrite.go
git commit -m "feat(files): restore a snapshot through the extract rules and a top-level swap

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
```

---

### Task 7: Forget, Pin, Purge und Snapshot-Download als Zip

**Files:**
- Create: `internal/files/forget.go`, `internal/files/tarzip.go`
- Modify: `internal/files/jobs.go` (Download-Leser, Sperre), `internal/files/server_backups.go`
- Test: `internal/files/forget_test.go`, `internal/files/tarzip_test.go`, `internal/files/server_backups_test.go`

**Interfaces:**
- Consumes: Aufgaben 4–6 (`restic`, `findSnapshot`, `Snapshots`, `dump`, `tzEnv`, `pinnedTag`, `resticHost`, `jobs.acquire`, `jobLock`), vorhanden `zipSafeName`, `zipLimit`, `maxZipNameBytes`, `slot`, `s.zipDownloads`, `deadlineWriter`, `ioTimeout`.
- Produces:

```go
func (s *Service) ForgetBackups(ctx context.Context, req filesapi.ForgetRequest) (filesapi.Job, error)
func forgetArgs(req filesapi.ForgetRequest) []string
func (s *Service) PinBackup(ctx context.Context, req filesapi.PinRequest) (filesapi.Snapshot, error)
func (s *Service) PurgeBackups(ctx context.Context, id int64) error
func (s *Service) DownloadBackup(ctx context.Context, id int64, sid string, start func(), w io.Writer) error
func tarToZip(ctx context.Context, r io.Reader, w io.Writer, lim config.FilesLimits, begin func()) error
func (j *jobs) addReader(id int64) (release func(), err error)
func (j *jobs) lock(id int64) (release func(), err error)
```

- [ ] **Step 1: failing tests**

`internal/files/forget_test.go`:

```go
package files

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestForgetArgs(t *testing.T) {
	ids := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	if got := forgetArgs(filesapi.ForgetRequest{ID: 1, Snapshots: ids}); !slices.Equal(got,
		[]string{"forget", "--prune", "--max-unused", "10%", ids[0], ids[1]}) {
		t.Fatalf("ids: %q", got)
	}
	got := forgetArgs(filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 24, KeepDaily: 0, KeepWeekly: 4, KeepMonthly: 6}, Timezone: "Europe/Berlin"})
	want := []string{"forget", "--prune", "--max-unused", "10%", "--host", "mcpanel", "--tag", "kind=scheduled",
		"--keep-tag", "pinned", "--group-by", "host", "--keep-last", "24", "--keep-weekly", "4", "--keep-monthly", "6"}
	if !slices.Equal(got, want) {
		t.Fatalf("policy: %q\nwant %q", got, want)
	}
}

func TestForgetPolicyJob(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	repo := s.repoDir(1)
	t0 := time.Now()
	f.addSnap(t, repo, t0, nil, "mcpanel", "kind=scheduled")
	keep := f.addSnap(t, repo, t0.Add(time.Hour), nil, "mcpanel", "kind=scheduled")
	pinned := f.addSnap(t, repo, t0.Add(-time.Hour), nil, "mcpanel", "kind=scheduled", "pinned")
	manual := f.addSnap(t, repo, t0.Add(-2*time.Hour), nil, "mcpanel", "kind=manual")
	job, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "Europe/Berlin"})
	if err != nil || job.Kind != filesapi.JobForget {
		t.Fatalf("forget = %+v %v", job, err)
	}
	if done := waitJob(t, s, 1); done.State != jobDone {
		t.Fatalf("job = %+v", done)
	}
	var left []string
	for _, sn := range f.snaps(repo) {
		left = append(left, sn.ID)
	}
	slices.Sort(left)
	want := []string{keep, pinned, manual}
	slices.Sort(want)
	if !slices.Equal(left, want) {
		t.Fatalf("left = %v, want %v", left, want)
	}
	c := f.callsOf("forget")
	if len(c) != 1 || envOf(c[0].Env, "TZ") != "Europe/Berlin" || len(f.callsOf("unlock")) != 1 {
		t.Fatalf("forget calls = %+v", c)
	}
}

func TestForgetIDsAndErrors(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	// Policy without repository: a job that is done at once.
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone || len(f.callsOf("forget")) != 0 {
		t.Fatalf("policy without repo: %+v", job)
	}
	// IDs without repository / unknown IDs: 404 before any job.
	unknown := strings.Repeat("c", 64)
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{unknown}}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("ids without repo: %v", err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	sid := f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=pre-restore")
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid, unknown}}); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("one unknown id: %v", err)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid}}); err != nil {
		t.Fatal(err)
	}
	if job := waitJob(t, s, 1); job.State != jobDone || len(f.snaps(s.repoDir(1))) != 0 {
		t.Fatalf("forget id: %+v, %d left", job, len(f.snaps(s.repoDir(1))))
	}
	// Invalid requests (filesapi.ForgetRequest.Validate).
	for _, req := range []filesapi.ForgetRequest{
		{ID: 1},
		{ID: 1, Snapshots: []string{sid}, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "UTC"},
		{ID: 1, Policy: &filesapi.ForgetPolicy{}, Timezone: "UTC"},
		{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "Mars/Olympus"},
	} {
		if _, err := s.ForgetBackups(ctx, req); !errors.Is(err, filesapi.ErrInvalid) {
			t.Errorf("%+v: %v", req, err)
		}
	}
}

func TestPinBackup(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	sched := f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=scheduled")
	manual := f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=manual")
	sn, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sched, Pinned: true})
	if err != nil || !sn.Pinned || sn.ID == sched || sn.Kind != filesapi.SnapshotScheduled {
		t.Fatalf("pin = %+v %v (new id expected)", sn, err)
	}
	if c := f.callsOf("tag"); !slices.Equal(c[0].Args, []string{"--no-cache", "--json", "tag", "--add", "pinned", sched}) {
		t.Fatalf("tag args = %q", c[0].Args)
	}
	same, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sn.ID, Pinned: true})
	if err != nil || same.ID != sn.ID {
		t.Fatalf("pin again = %+v %v (no change, same id)", same, err)
	}
	un, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sn.ID, Pinned: false})
	if err != nil || un.Pinned || un.ID == sn.ID {
		t.Fatalf("unpin = %+v %v", un, err)
	}
	if _, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: manual, Pinned: true}); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("pin manual: %v", err)
	}
	// While a job runs for the instance: busy.
	if err := s.jobs.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: un.ID, Pinned: true}); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("pin during a job: %v", err)
	}
	s.jobs.release(1)
}

func TestPurgeBackups(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatalf("purge without repo: %v", err)
	}
	if err := s.ensureKey(1); err != nil {
		t.Fatal(err)
	}
	f.addSnap(t, s.repoDir(1), time.Now(), nil, "mcpanel", "kind=manual")
	if err := s.jobs.acquire(1, filesapi.JobForget); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeBackups(ctx, 1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("purge during a job: %v", err)
	}
	s.jobs.release(1)
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{s.repoDir(1), s.keyFile(1)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", p, err)
		}
	}
}

// TestDownloadAndForgetExclude (review focus #4).
func TestDownloadAndForgetExclude(t *testing.T) {
	j := newJobs(1)
	release, err := j.addReader(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.acquire(1, filesapi.JobForget); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("forget during a download: %v", err)
	}
	if _, err := j.lock(1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("pin/purge during a download: %v", err)
	}
	if err := j.acquire(1, filesapi.JobBackup); err != nil {
		t.Fatalf("backup during a download: %v", err) // both only read-lock the repository
	}
	j.release(1)
	release()
	if err := j.acquire(1, filesapi.JobForget); err != nil {
		t.Fatal(err)
	}
	if _, err := j.addReader(1); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("download during forget: %v", err)
	}
}
```

`internal/files/tarzip_test.go`:

```go
package files

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func zipNames(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(body)
	}
	return out
}

func TestTarToZip(t *testing.T) {
	b := makeTar(t, dir("world/"), reg("world/level.dat", "lvl"), reg("CON.txt", "windows"), dir("bad:dir/"), reg("bad:dir/x", "x"),
		reg("server.properties", "rcon.password=********\n"))
	var out bytes.Buffer
	begun := false
	if err := tarToZip(context.Background(), bytes.NewReader(b), &out, testLimits(), func() { begun = true }); err != nil {
		t.Fatal(err)
	}
	got := zipNames(t, out.Bytes())
	if !begun || got["world/level.dat"] != "lvl" || got["server.properties"] == "" || len(got) != 3 {
		t.Fatalf("zip = %v", got) // world/, world/level.dat, server.properties; CON.txt and bad:dir/ left out
	}
}

func TestTarToZipErrorsBeforeBegin(t *testing.T) {
	begun := false
	err := tarToZip(context.Background(), strings.NewReader("garbage, shorter than one tar block"), io.Discard, testLimits(), func() { begun = true })
	if !errors.Is(err, filesapi.ErrInvalid) || begun {
		t.Fatalf("garbage: %v, begun %v", err, begun)
	}
}

func TestDownloadBackup(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "hello")))
	var out bytes.Buffer
	started := false
	if err := s.DownloadBackup(ctx, 1, sid, func() { started = true }, &out); err != nil {
		t.Fatal(err)
	}
	if !started || zipNames(t, out.Bytes())["a.txt"] != "hello" {
		t.Fatalf("download = %v", zipNames(t, out.Bytes()))
	}
	// Unknown snapshot and a failing dump are answered before the first byte.
	if err := s.DownloadBackup(ctx, 1, strings.Repeat("d", 64), func() { t.Fatal("started") }, io.Discard); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	f.fail["dump"] = &resticExit{Code: 1, Stderr: "Fatal: pack file is damaged\n"}
	if err := s.DownloadBackup(ctx, 1, sid, func() { t.Fatal("started") }, io.Discard); err == nil || !strings.Contains(err.Error(), "restic dump failed") {
		t.Fatalf("failing dump: %v", err)
	}
	delete(f.fail, "dump")
	// While a forget runs: busy.
	if err := s.jobs.acquire(1, filesapi.JobForget); err != nil {
		t.Fatal(err)
	}
	if err := s.DownloadBackup(ctx, 1, sid, func() {}, io.Discard); !errors.Is(err, filesapi.ErrBusy) {
		t.Fatalf("during forget: %v", err)
	}
	s.jobs.release(1)
}
```

In `internal/files/server_backups_test.go` anhängen:

```go
func TestServerForgetPinPurgeDownload(t *testing.T) {
	s, _, f := newBackupService(t)
	sid := snapshotOf(t, s, f, makeTar(t, reg("a.txt", "hello")))
	ts := startHTTP(t, NewServer(s, "test", discardLog()))

	resp, err := http.Get(ts.URL + filesapi.PathBackupsDownload + "?id=1&snapshot=" + sid)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get(filesapi.HeaderKind) != "dir" || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("download: %d %v", resp.StatusCode, resp.Header)
	}
	if zipNames(t, body)["a.txt"] != "hello" {
		t.Fatal("download content")
	}
	resp, err = http.Get(ts.URL + filesapi.PathBackupsDownload + "?id=1&snapshot=xyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad snapshot id: %d", resp.StatusCode)
	}

	status, b := postJSON(t, ts, filesapi.PathBackupsPin, filesapi.PinRequest{ID: 1, Snapshot: sid, Pinned: true})
	if status != http.StatusBadRequest || errCode(t, b) != filesapi.CodeInvalid { // a manual snapshot
		t.Fatalf("pin manual: %d %s", status, b)
	}
	status, b = postJSON(t, ts, filesapi.PathBackupsForget, filesapi.ForgetRequest{ID: 1, Snapshots: []string{sid}})
	if status != http.StatusAccepted {
		t.Fatalf("forget: %d %s", status, b)
	}
	waitJob(t, s, 1)
	if status, b = postJSON(t, ts, filesapi.PathBackupsPurge, filesapi.IDRequest{ID: 1}); status != http.StatusNoContent {
		t.Fatalf("purge: %d %s", status, b)
	}
}
```

(Import `io` in `server_backups_test.go` ergänzen.)

- [ ] **Step 2: Run, expect FAIL**

Run: `make check`
Expected: FAIL (`undefined: forgetArgs`, `s.ForgetBackups`, `tarToZip`, `j.addReader`, …).

- [ ] **Step 3: `jobs.go` – Leser und Sperre**

Im `jobs`-Struct `readers map[int64]int // running backup downloads per instance` ergänzen, in `newJobs` mit `readers: map[int64]int{}` anlegen. In `acquireLocked` nach dem `busy`-Fall:

```go
	case (kind == filesapi.JobForget || kind == jobLock) && j.readers[id] > 0:
		return fmt.Errorf("%w: a backup download is running for this server", filesapi.ErrBusy)
```

und:

```go
// lock takes instance id for pin or purge (no job; excludes every job and download).
func (j *jobs) lock(id int64) (func(), error) {
	if err := j.acquire(id, jobLock); err != nil {
		return nil, err
	}
	return func() { j.release(id) }, nil
}

// addReader registers a backup download of instance id (spec 4 §4.4): refused while a forget
// runs (prune holds an exclusive repository lock); a forget, pin or purge is refused while one
// runs.
func (j *jobs) addReader(id int64) (func(), error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case j.closed:
		return nil, errShuttingDown
	case j.busy[id] == filesapi.JobForget || j.busy[id] == jobLock:
		return nil, fmt.Errorf("%w: %s", filesapi.ErrBusy, busyText(j.busy[id]))
	}
	j.readers[id]++
	return func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if j.readers[id]--; j.readers[id] <= 0 {
			delete(j.readers, id)
		}
	}, nil
}
```

- [ ] **Step 4: `forget.go` implementieren**

```go
package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// forgetArgs builds the restic arguments of a forget job (spec 4 §3.3, §5.4): explicit IDs, or
// the policy over the unpinned kind=scheduled snapshots of the host "mcpanel". Zero values are
// left out (restic would read 0 as "keep none of this kind").
func forgetArgs(req filesapi.ForgetRequest) []string {
	args := []string{"forget", "--prune", "--max-unused", "10%"}
	if req.Policy == nil {
		return append(args, req.Snapshots...)
	}
	args = append(args, "--host", resticHost, "--tag", kindTagPrefix+string(filesapi.SnapshotScheduled),
		"--keep-tag", pinnedTag, "--group-by", "host")
	for _, k := range []struct {
		flag string
		n    int
	}{
		{"--keep-last", req.Policy.KeepLast}, {"--keep-daily", req.Policy.KeepDaily},
		{"--keep-weekly", req.Policy.KeepWeekly}, {"--keep-monthly", req.Policy.KeepMonthly},
	} {
		if k.n > 0 {
			args = append(args, k.flag, strconv.Itoa(k.n))
		}
	}
	return args
}

// ForgetBackups starts a forget job (spec 4 §4.1): with IDs every one must exist (404 before the
// job); with a policy and no repository the job ends done at once.
func (s *Service) ForgetBackups(ctx context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.jobs.acquire(req.ID, filesapi.JobForget); err != nil {
		return filesapi.Job{}, err
	}
	ownSlot := true
	defer func() {
		if ownSlot {
			s.jobs.release(req.ID)
		}
	}()
	if len(req.Snapshots) > 0 {
		if !s.repoExists(req.ID) {
			return filesapi.Job{}, fmt.Errorf("%w: this server has no backups", filesapi.ErrNotFound)
		}
		list, err := s.Snapshots(ctx, req.ID)
		if err != nil {
			return filesapi.Job{}, err
		}
		known := map[string]bool{}
		for _, sn := range list {
			known[sn.ID] = true
		}
		for _, id := range req.Snapshots {
			if !known[id] {
				return filesapi.Job{}, fmt.Errorf("%w: no such snapshot %s", filesapi.ErrNotFound, id[:8])
			}
		}
	}
	ownSlot = false
	return s.jobs.start(req.ID, jobSpec{kind: filesapi.JobForget}, s.clock.Now, func(jctx context.Context) error {
		if !s.repoExists(req.ID) {
			return nil // policy, nothing to forget yet
		}
		if _, err := s.restic(jctx, req.ID, nil, "unlock"); err != nil {
			return s.ctxErr(jctx, err)
		}
		var env []string
		if req.Policy != nil {
			env = tzEnv(req.Timezone)
		}
		_, err := s.restic(jctx, req.ID, env, forgetArgs(req)...)
		if err != nil {
			s.log.Warn("files: forget failed", "id", req.ID, "err", err)
		}
		return s.ctxErr(jctx, err)
	})
}

// PinBackup adds or removes the pinned tag of a scheduled snapshot (spec 4 §4.1). restic
// rewrites the snapshot: the answer carries the new ID (read from `restic tag --json`).
func (s *Service) PinBackup(ctx context.Context, req filesapi.PinRequest) (filesapi.Snapshot, error) {
	if !filesapi.ValidSnapshotID(req.Snapshot) {
		return filesapi.Snapshot{}, fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Snapshot{}, err
	}
	release, err := s.jobs.lock(req.ID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	defer release()
	sn, err := s.findSnapshot(ctx, req.ID, req.Snapshot)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	if sn.Kind != filesapi.SnapshotScheduled {
		return filesapi.Snapshot{}, fmt.Errorf("%w: only scheduled snapshots can be pinned", filesapi.ErrInvalid)
	}
	if sn.Pinned == req.Pinned {
		return sn, nil
	}
	if _, err := s.restic(ctx, req.ID, nil, "unlock"); err != nil {
		return filesapi.Snapshot{}, err
	}
	op := "--add"
	if !req.Pinned {
		op = "--remove"
	}
	out, err := s.restic(ctx, req.ID, nil, "--json", "tag", op, pinnedTag, sn.ID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	if newID := changedSnapshotID(out, sn.ID); newID != sn.ID {
		return s.findSnapshot(ctx, req.ID, newID)
	}
	// An older restic without JSON output for tag: the rewritten snapshot keeps time and kind.
	list, err := s.Snapshots(ctx, req.ID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	for _, c := range list {
		if c.Time == sn.Time && c.Kind == sn.Kind && c.Pinned == req.Pinned && c.ID != sn.ID {
			return c, nil
		}
	}
	return filesapi.Snapshot{}, fmt.Errorf("restic tag changed no snapshot")
}

// changedSnapshotID reads new_snapshot_id from the "changed" message of `restic tag --json`
// (restic 0.19.1 cmd_tag.go); without one the old ID comes back.
func changedSnapshotID(out []byte, old string) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var m struct {
			MessageType   string `json:"message_type"`
			OldSnapshotID string `json:"old_snapshot_id"`
			NewSnapshotID string `json:"new_snapshot_id"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.MessageType == "changed" && m.OldSnapshotID == old && filesapi.ValidSnapshotID(m.NewSnapshotID) {
			return m.NewSnapshotID
		}
	}
	return old
}

// PurgeBackups removes the repository and the key of server id (spec 4 §5.6); nothing there is
// fine. backups/ is only writable by the service's user and invisible to containers.
func (s *Service) PurgeBackups(ctx context.Context, id int64) error {
	if err := s.requireBackups(id); err != nil {
		return err
	}
	release, err := s.jobs.lock(id)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.RemoveAll(s.repoDir(id)); err != nil {
		return fmt.Errorf("remove the backup repository: %w", err)
	}
	if err := os.Remove(s.keyFile(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the repository key: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: `tarzip.go` implementieren**

```go
package files

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// DownloadBackup streams snapshot sid of server id as a zip (spec 4 §4.4). Snapshot, download
// lock and zip slot are checked before the first byte; start is called right before the first
// zip entry, so a dump that fails at once is still answered as an error.
func (s *Service) DownloadBackup(ctx context.Context, id int64, sid string, start func(), w io.Writer) error {
	if !filesapi.ValidSnapshotID(sid) {
		return fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	if err := s.requireBackups(id); err != nil {
		return err
	}
	releaseReader, err := s.jobs.addReader(id)
	if err != nil {
		return err
	}
	defer releaseReader()
	sn, err := s.findSnapshot(ctx, id, sid)
	if err != nil {
		return err
	}
	releaseZip, err := slot(s.zipDownloads, "folder downloads")
	if err != nil {
		return err
	}
	defer releaseZip()
	return s.dump(ctx, id, sn.ID, func(r io.Reader) error {
		return tarToZip(ctx, r, w, s.cfg.Limits, start)
	})
}

// zipSafePath: every component passes zipSafeName (names Windows cannot store are left out,
// like in the folder download).
func zipSafePath(clean string) bool {
	for _, c := range strings.Split(clean, "/") {
		if !zipSafeName(c) {
			return false
		}
	}
	return true
}

// tarToZip converts a backup stream to a zip on the fly. Only folders and regular files with
// clean, visible, zip-safe names go in; a folder that is left out takes its content along.
// begin runs once before the first byte of the zip (also for an empty stream).
func tarToZip(ctx context.Context, r io.Reader, w io.Writer, lim config.FilesLimits, begin func()) error {
	tr := tar.NewReader(r)
	var zw *zip.Writer
	open := func() {
		if zw == nil {
			begin()
			zw = zip.NewWriter(w)
		}
	}
	l := &zipLimit{max: lim.MaxBackupEntries}
	var skipped []string
	var total int64
	under := func(p string) bool {
		for _, d := range skipped {
			if strings.HasPrefix(p, d+"/") {
				return true
			}
		}
		return false
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: the backup stream is damaged: %w", filesapi.ErrInvalid, err)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		clean, cerr := filesapi.CleanPath(name)
		ok := cerr == nil && clean != "" && clean == name && !filesapi.IsHidden(clean) && zipSafePath(clean) && !under(clean)
		switch {
		case !ok || (hdr.Typeflag != tar.TypeDir && hdr.Typeflag != tar.TypeReg):
			if hdr.Typeflag == tar.TypeDir {
				skipped = append(skipped, name)
			}
			continue
		}
		if err := l.add(clean); err != nil {
			return err
		}
		open()
		if hdr.Typeflag == tar.TypeDir {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: clean + "/", Method: zip.Store, Modified: hdr.ModTime}); err != nil {
				return err
			}
			continue
		}
		if hdr.Size < 0 || hdr.Size > lim.MaxBackupBytes-total {
			return fmt.Errorf("%w: the snapshot expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxBackupBytes)
		}
		total += hdr.Size
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: clean, Method: zip.Deflate, Modified: hdr.ModTime})
		if err != nil {
			return err
		}
		n, err := io.Copy(fw, io.LimitReader(tr, hdr.Size))
		if err != nil {
			return err
		}
		if n != hdr.Size {
			return fmt.Errorf("%w: entry %q is cut off", filesapi.ErrInvalid, hdr.Name)
		}
	}
	open()
	return zw.Close()
}
```

- [ ] **Step 6: Routen**

`backupRoutes` bekommt

```go
		filesapi.PathBackupsForget:   {method: http.MethodPost, handler: s.backupsForget},
		filesapi.PathBackupsPin:      {method: http.MethodPost, handler: s.backupsPin},
		filesapi.PathBackupsPurge:    {method: http.MethodPost, handler: s.backupsPurge},
		filesapi.PathBackupsDownload: {method: http.MethodGet, handler: s.backupsDownload},
```

und Handler (Import `time` ergänzen):

```go
func (s *Server) backupsForget(w http.ResponseWriter, r *http.Request) {
	var req filesapi.ForgetRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var job filesapi.Job
	err := s.longOp(w, "backups.forget", func() error {
		var err error
		job, err = s.svc.ForgetBackups(r.Context(), req)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.forget", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) backupsPin(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PinRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var sn filesapi.Snapshot
	err := s.longOp(w, "backups.pin", func() error {
		var err error
		sn, err = s.svc.PinBackup(r.Context(), req)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.pin", err)
		return
	}
	writeJSON(w, http.StatusOK, filesapi.SnapshotResponse{Snapshot: sn})
}

func (s *Server) backupsPurge(w http.ResponseWriter, r *http.Request) {
	var req filesapi.IDRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.longOp(w, "backups.purge", func() error { return s.svc.PurgeBackups(r.Context(), req.ID) }); err != nil {
		s.fail(w, r, "backups.purge", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// backupsDownload: GET ?id=&snapshot=, a zip stream like the folder download (errors after the
// first byte abort the response).
func (s *Server) backupsDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, err := parseID(q.Get("id"))
	if err != nil {
		s.fail(w, r, "backups.download", err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{}) // the pre-check lists the snapshots
	started := false
	start := func() {
		started = true
		h := w.Header()
		h.Set(filesapi.HeaderKind, "dir")
		h.Set("Content-Type", "application/zip")
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()
	}
	err = s.svc.DownloadBackup(r.Context(), id, q.Get("snapshot"), start, &deadlineWriter{w: w, rc: rc, now: s.io.Now})
	if err == nil {
		return
	}
	if !started {
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		s.fail(w, r, "backups.download", err)
		return
	}
	if r.Context().Err() == nil {
		s.log.Warn("files: backup download aborted", "id", id, "err", err)
	}
	panic(http.ErrAbortHandler)
}
```

- [ ] **Step 7: Run, expect PASS**

Run: `make check`; `make test-vm SUITE=go` (gezielt `go test -race ./internal/files -run 'TestForget|TestPin|TestPurge|TestDownload|TestTarToZip|TestServerForget'`), danach `make test-vm SUITE=lint`
Expected: PASS; Lint ohne Befund (Ausnahmen nur gezielt mit Begründung).

- [ ] **Step 8: Commit**

```bash
git add internal/files/forget.go internal/files/forget_test.go internal/files/tarzip.go internal/files/tarzip_test.go \
  internal/files/jobs.go internal/files/server_backups.go internal/files/server_backups_test.go
git commit -m "feat(files): forget, pin, purge and zip downloads of snapshots

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
```

---

### Task 8: Integrationstests mit echtem restic und Ende-zu-Ende über den Unix-Socket

**Files:**
- Create: `internal/files/restic_integration_test.go`, `internal/files/backups_e2e_test.go`

**Interfaces:**
- Consumes: alles aus Aufgaben 4–7; `filesapi.UnixClient` (V1: `BackupsStatus`, `Snapshots`, `CreateBackup`, `RestoreBackup`, `ForgetBackups`, `PinBackup`, `DownloadBackup`, `PurgeBackups`, `CancelJob`, `Job`), `startUnix`-Muster aus `e2e_test.go`.
- Produces: keine neuen Symbole außer Test-Helfern (`realRestic`, `newRealService`, `waitDone`).

- [ ] **Step 1: Tests schreiben**

`internal/files/restic_integration_test.go`:

```go
package files

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// realRestic returns the restic binary for integration tests (MCPANEL_TEST_RESTIC, set by
// scripts/vm-test.sh and CI); without it the test is skipped (always on the Mac).
func realRestic(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MCPANEL_TEST_RESTIC")
	if bin == "" {
		t.Skip("MCPANEL_TEST_RESTIC is not set")
	}
	return bin
}

func newRealService(t *testing.T) (*Service, string) {
	t.Helper()
	bin := realRestic(t)
	s, data := newService(t, func(c *Config) {
		c.ResticBinary = bin
		c.Limits.MaxBackupBytes = 64 << 20
		c.Limits.MaxBackupEntries = 10_000
	})
	if err := os.Mkdir(s.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if st := s.InitBackups(context.Background()); !st.Available {
		t.Fatalf("restic unavailable: %+v", st)
	}
	return s, data
}

func waitDone(t *testing.T, s *Service, id int64) filesapi.Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		j := s.Job(id)
		if j != nil && j.State != jobRunning {
			if j.State != jobDone {
				t.Fatalf("job failed: %+v", *j)
			}
			return *j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job still running: %+v", j)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func backupNow(t *testing.T, s *Service, kind filesapi.SnapshotKind, note string) string {
	t.Helper()
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: kind, Note: note}); err != nil {
		t.Fatal(err)
	}
	return waitDone(t, s, 1).Snapshot
}

func TestResticBackupListRestoreDownload(t *testing.T) {
	ctx := context.Background()
	s, data := newRealService(t)
	put(t, data, "world/level.dat", "version one")
	put(t, data, "server.properties", "motd=hi\nrcon.password=s3cret\n")
	put(t, data, ".rcon-cli.env", "password=s3cret\n")
	sid := backupNow(t, s, filesapi.SnapshotManual, "vor Update")

	list, err := s.Snapshots(ctx, 1)
	if err != nil || len(list) != 1 || list[0].ID != sid || list[0].Kind != filesapi.SnapshotManual || list[0].Note != "vor Update" || list[0].Bytes <= 0 {
		t.Fatalf("list = %+v %v", list, err)
	}

	put(t, data, "world/level.dat", "version two")
	put(t, data, "new.txt", "appears later")
	if _, err := s.RestoreBackup(ctx, filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	if got, _ := get(t, data, "world/level.dat"); got != "version one" {
		t.Fatalf("level.dat = %q", got)
	}
	if _, ok := get(t, data, "new.txt"); ok {
		t.Fatal("new.txt survived the restore")
	}
	if got, _ := get(t, data, ".rcon-cli.env"); got != "password=s3cret\n" {
		t.Fatalf("hidden file changed: %q", got)
	}
	if got, _ := get(t, data, "server.properties"); strings.Contains(got, "s3cret") {
		t.Fatalf("the password went into the snapshot: %q", got)
	}
	noTemp(t, data)

	var zipBuf bytes.Buffer
	if err := s.DownloadBackup(ctx, 1, sid, func() {}, &zipBuf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if strings.Contains(string(b), "s3cret") {
			t.Fatalf("%s carries the password", f.Name)
		}
		found = found || f.Name == "world/level.dat"
	}
	if !found {
		t.Fatal("world/level.dat missing in the zip")
	}
}

func TestResticPinAndForgetPolicy(t *testing.T) {
	ctx := context.Background()
	s, data := newRealService(t)
	var sched []string
	for i := range 3 {
		put(t, data, "world/level.dat", strings.Repeat("x", i+1))
		sched = append(sched, backupNow(t, s, filesapi.SnapshotScheduled, ""))
	}
	manual := backupNow(t, s, filesapi.SnapshotManual, "")
	pinned, err := s.PinBackup(ctx, filesapi.PinRequest{ID: 1, Snapshot: sched[0], Pinned: true})
	if err != nil || !pinned.Pinned || pinned.ID == sched[0] {
		t.Fatalf("pin = %+v %v", pinned, err)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 1}, Timezone: "Europe/Berlin"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	list, err := s.Snapshots(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sn := range list {
		ids = append(ids, sn.ID)
	}
	slices.Sort(ids)
	want := []string{sched[2], manual, pinned.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("after forget: %v, want newest scheduled + manual + pinned %v", ids, want)
	}
	if _, err := s.ForgetBackups(ctx, filesapi.ForgetRequest{ID: 1, Snapshots: []string{pinned.ID}}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	if list, _ := s.Snapshots(ctx, 1); len(list) != 2 {
		t.Fatalf("after forget by id: %+v", list)
	}
	if err := s.PurgeBackups(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(s.repoDir(1)); !os.IsNotExist(err) {
		t.Fatalf("repo after purge: %v", err)
	}
}

// TestResticStaleLockIsRemoved (review focus #5): a restic killed with SIGKILL leaves its lock;
// the next job's `restic unlock` removes it.
// TestResticSnapshotTimeCarriesZone pins the reason for CreateBackupRequest.Timezone: restic
// stores the snapshot time in the zone of the backup process, and forget buckets by it.
func TestResticSnapshotTimeCarriesZone(t *testing.T) {
	bin := realRestic(t)
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled, Timezone: "Pacific/Kiritimati"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1)
	cmd := exec.Command(bin, "--no-cache", "--json", "--no-lock", "snapshots") //nolint:gosec // G204: test binary from MCPANEL_TEST_RESTIC
	cmd.Env = s.resticEnv(1)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var snaps []struct {
		Time string `json:"time"`
	}
	if err := json.Unmarshal(out, &snaps); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %s %v", out, err)
	}
	if !strings.HasSuffix(snaps[0].Time, "+14:00") {
		t.Fatalf("snapshot time %q is not in Pacific/Kiritimati (+14:00)", snaps[0].Time)
	}
}

func TestResticStaleLockIsRemoved(t *testing.T) {
	bin := realRestic(t)
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	backupNow(t, s, filesapi.SnapshotManual, "")
	cmd := exec.Command(bin, "--no-cache", "backup", "--stdin", "--stdin-filename", "x") //nolint:gosec // G204: test binary from MCPANEL_TEST_RESTIC
	cmd.Env = s.resticEnv(1)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		locks, _ := os.ReadDir(filepath.Join(s.repoDir(1), "locks"))
		if len(locks) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restic took no lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	_ = stdin.Close()
	if _, err := s.ForgetBackups(context.Background(), filesapi.ForgetRequest{ID: 1, Policy: &filesapi.ForgetPolicy{KeepLast: 5}, Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s, 1) // forget needs an exclusive lock: it only works after the stale one is gone
}

func TestResticMissingKey(t *testing.T) {
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	backupNow(t, s, filesapi.SnapshotManual, "")
	if err := os.Remove(s.keyFile(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || !strings.Contains(job.Error, "key") {
		t.Fatalf("job = %+v", job)
	}
}

// TestResticErrorsHideHostPaths: a broken repository yields restic messages with host paths;
// what leaves the service names none.
func TestResticErrorsHideHostPaths(t *testing.T) {
	s, data := newRealService(t)
	put(t, data, "a.txt", "a")
	sid := backupNow(t, s, filesapi.SnapshotManual, "")
	if err := os.RemoveAll(filepath.Join(s.repoDir(1), "data")); err != nil { // all packs gone
		t.Fatal(err)
	}
	if _, err := s.RestoreBackup(context.Background(), filesapi.SnapshotRequest{ID: 1, Snapshot: sid}); err != nil {
		t.Fatal(err)
	}
	job := waitJob(t, s, 1)
	if job.State != jobFailed || strings.Contains(job.Error, s.cfg.InstancesDir) {
		t.Fatalf("job = %+v", job)
	}
	if got, _ := get(t, data, "a.txt"); got != "a" {
		t.Fatalf("data changed by a failed restore: %q", got)
	}
	noTemp(t, data)
}
```

`internal/files/backups_e2e_test.go`:

```go
package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/unixsock"
)

// TestBackupsEndToEndWithClient drives the panel's client against the service over a real
// Unix socket with the fake restic (no MCPANEL_TEST_RESTIC needed).
func TestBackupsEndToEndWithClient(t *testing.T) {
	dir, err := os.MkdirTemp("", "mcb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	s, data, _ := newBackupService(t)
	srv := NewServer(s, "e2e", discardLog())
	sock := filepath.Join(dir, "f.sock")
	l, err := unixsock.Listen(sock, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(uint32(os.Getuid())), discardLog())) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	c := filesapi.NewClient(sock)
	ctx := context.Background()

	st, err := c.BackupsStatus(ctx)
	if err != nil || !st.Available {
		t.Fatalf("status = %+v %v", st, err)
	}
	put(t, data, "a.txt", "one")
	if _, err := c.CreateBackup(ctx, filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotScheduled}); err != nil {
		t.Fatal(err)
	}
	var job *filesapi.Job
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if job, err = c.Job(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if job != nil && job.State != filesapi.JobRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not finish")
		}
	}
	if job.Kind != filesapi.JobBackup || job.State != filesapi.JobDone {
		t.Fatalf("job = %+v", job)
	}
	snaps, err := c.Snapshots(ctx, 1)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots = %+v %v", snaps, err)
	}
	pinned, err := c.PinBackup(ctx, 1, snaps[0].ID, true)
	if err != nil || !pinned.Pinned {
		t.Fatalf("pin = %+v %v", pinned, err)
	}
	d, err := c.DownloadBackup(ctx, 1, pinned.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(d.Body)
	_ = d.Body.Close()
	if !bytes.HasPrefix(b, []byte("PK")) {
		t.Fatalf("download is no zip: %q", b[:min(len(b), 16)])
	}
	if _, err := c.RestoreBackup(ctx, 1, pinned.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.CancelJob(ctx, 1); err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, 1)
	if _, err := c.RestoreBackup(ctx, 1, "0000000000000000000000000000000000000000000000000000000000000000"); !errors.Is(err, filesapi.ErrNotFound) {
		t.Fatalf("unknown snapshot: %v", err)
	}
	if err := c.PurgeBackups(ctx, 1); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run auf der VM**

Run: `make test-vm SUITE=go` (die VM setzt `MCPANEL_TEST_RESTIC`), gezielt `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-files && PATH=/usr/local/go/bin:$PATH MCPANEL_TEST_RESTIC=/usr/local/bin/restic go test -race -v ./internal/files -run "TestRestic|TestBackupsEndToEnd"'`
Expected: PASS; die `TestRestic*`-Tests laufen (nicht `SKIP`). Läuft einer als `SKIP`, ist Phase A (restic in `scripts/vm-test.sh`) nicht gemerged → Lead fragen.

Weicht echtes restic von den Annahmen ab (z. B. `forget --prune --max-unused` oder die JSON-Felder), den Fake (`restic_fake_test.go`) und den Produktionscode gemeinsam anpassen, nie nur den Fake.

- [ ] **Step 3: Gesamtlauf und Lint**

Run: `make check`; `make test-vm` (alle Suites)
Expected: PASS.

- [ ] **Step 4: Commit, PR bereitmachen melden**

```bash
git add internal/files/restic_integration_test.go internal/files/backups_e2e_test.go
git commit -m "test(files): backups against a real restic and through the panel's client

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push
gh pr checks --watch
```

Danach dem Lead Bescheid geben (PR-Nummer, CI-Status, offene Punkte aus „Abweichungen und Präzisierungen“). PR nicht selbst auf „ready“ setzen.

## Self-Review

- **Spec-Abdeckung:** §3.1 (Layout, Schlüssel, Repo pro Server) → Aufgabe 4; §3.2 (Schlüssel, Versionsprüfung, `503` ohne restic, Dev) → Aufgabe 4; Unit-Werte `MemoryMax`/`ReadWritePaths`/`IOWeight` gehören zu Phase C (`scripts/testvm-deploy.sh`); §3.3 (argv, Umgebung, `unlock`, `--no-lock`, Tags, Pin-ID, Forget, Fehlertexte, ID-Prüfung) → Aufgaben 4, 5, 7; §4.1 (Endpunkte, Job-Felder, ein Job pro Instanz, `max_backup_jobs`, Note) → Aufgaben 5–7; §4.2 → Aufgabe 5; §4.3 → Aufgabe 6; §4.4 → Aufgabe 7; §4.5 → Aufgabe 6; §8 Zeile „Dienst“ → Aufgaben 5–8.
- **Typkonsistenz:** `jobSpec`, `jobLock`, `newJobs(int)`, `acquire(id, kind)`, `start(id, spec, now, run)` werden ab Aufgabe 5 überall so benutzt; `resticCmd`/`resticProc`/`resticExit` aus Aufgabe 4 in 5–7 unverändert; `fakeRestic.gate`, `addSnap`, `callsOf`, `snaps`, `sawEOF` sind in Aufgabe 4 angelegt.
- **Bekannte Grenzen:** Zeitzone der Retention (siehe „Abweichungen“), Timeout von `ForgetBackups` im Client.
