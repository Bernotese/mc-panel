# mc-panel Teilprojekt 4 – Phase B3: Frontend Backups (Aufgaben 14–17)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der Tab „Backups“ auf der Server-Detailseite: Snapshot-Liste (Zeit relativ + absolut, Art-Badge, Notiz, Größe, Pin), „Backup erstellen“ mit optionaler Notiz, Job-Anzeige mit 2-s-Abfrage solange `queued`/`running`, Aktionen pro Snapshot (Anpinnen/Lösen nur bei geplanten, Zip-Download, Wiederherstellen mit Namensbestätigung + Sudo, Löschen mit Bestätigung + Sudo), Zeitplan-Karte (Modus, Intervall/Uhrzeit/Wochentag, Retention, Zeitzonen-Hinweis, nächster Lauf, letzter Status) und Hinweis bei `available = false`; dazu die Zeitzone in den Einstellungen, die Checkbox „Backups ebenfalls löschen“ beim Server-Löschen, der Rechte-Hinweis zu `server.backups` und Playwright gegen den Mock.

**Architecture:** Alles Neue liegt in `web/src/features/backups/`. Reine Logik (Notiz- und Zeitplan-Validierung, Uhrzeit ↔ Minuten, Wochentag-Reihenfolge, Art-/Schritt-/Fehler-Schlüssel, aktiver Job) steht in `model.ts` und ist per Vitest getestet; `api.ts` hält Query-Keys, Query-Options und Aufrufe über den vorhandenen Client (`@/api/client`, `unwrap`/`unwrapVoid`). Übersicht und Job hängen unter `['servers', id, 'backups', …]`, damit Power-Aktionen und Einstellungs-Änderungen (Zeitzone) sie über das vorhandene `serversKey`-Präfix mit auffrischen. Der Job wird wie bei Plugins (3c) per `setQueryData` aus der `202`-Antwort gestartet und alle 2 s abgefragt, solange er `queued` oder `running` ist; sein Ende lädt die Übersicht neu, nach einem Restore auch den Server. Die Detailseite zeigt den Tab nur bei `server.backups` und vorhandener Instanz.

**Tech Stack:** React 19, TypeScript, TanStack Query (`useQuery`, `useMutation`), shadcn/ui (Tabs, Dialog, DropdownMenu, Badge, Button, Input, Label), `DataList`, `ConfirmDialog`, `SectionCard`, `NativeSelect`, `FieldRow`, react-i18next, MSW 2, Vitest + Testing Library, Playwright. Keine neuen Pakete.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-backups-design.md` (§5.7, §5.10, §6) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-12-backups-00-overview.md` (V3, V6; „Ausführung“, „Global Constraints“ und Dateieigentum B3 gelten vollständig).

## Ausführung dieser Phase

- Teammate „frontend“, nach dem Merge von Phase A: Worktree `git worktree add ../mc-panel-wt/feat-backups-frontend -b feat/backups-frontend origin/main`, dort einmal `(cd web && npm ci)`.
- Lokal nur `make check` (gofmt, vet, Go-Build, `tsc`). Neue Dateien vor `make check` mit `git add -N <datei>` vormerken.
- „Run: `npx vitest run …`“ in den Schritten heißt: **auf der Test-VM**, nicht auf dem Mac: einmal `make test-vm SUITE=web` (kopiert den Arbeitsbaum, auch ungetrackte Dateien), danach einzelne Dateien mit `ssh claude@192.168.1.117 'cd ~/mc-panel-test/feat-backups-frontend/web && PATH=/usr/local/lib/nodejs/bin:$PATH npx vitest run src/features/backups/model.test.ts'` (Verzeichnis = Basisname des Worktrees; nach Änderungen erneut `make test-vm SUITE=web`). Playwright: `make test-vm SUITE=e2e`. SSH braucht eine Bash ohne Sandbox. Vor „ready for review“ `make test-vm` (alle Suites).
- Ein Draft-PR für die ganze Phase (Titel `feat(web): backups tab`), ein Commit pro Aufgabe; Mergen nur durch den Nutzer. CI-Gate: `gh pr checks <n> --watch` (Job `CI OK`).
- Dateieigentum B3: `web/src/features/backups/**` (neu), `web/src/features/servers/**`, `web/src/features/users/**`, `web/src/features/settings/**`, Locale-Dateien `web/src/i18n/de.ts`, `en.ts`, `i18n.test.ts` (nur `DYNAMIC_KEYS` und Nicht-`errors.*`-Texte), `web/e2e/**`, `web/playwright.config.ts` (nur `testMatch`). **Nicht** ändern: `web/src/api/**`, `web/src/mocks/**` (Phase A). Fehlt dort etwas oder verhält sich der Mock anders als unten angenommen → Lead fragen (Änderung per eigenem PR auf `main`, danach `git merge origin/main`).

## Was Phase A liefert (Annahmen dieses Plans)

- `web/src/api/schema.d.ts` enthält die acht Backup-Operationen aus V3 sowie `backup_timezone` in `ServerSettings`/`UpdateServerSettingsRequest` und den Query-Parameter `purge_backups` bei `deleteServer`. `web/src/api/types.ts` exportiert für **jedes benannte Schema** aus V3 einen Alias nach dem Muster `export type BackupJob = Schemas['BackupJob']`: `BackupKind`, `BackupSnapshot`, `BackupJobKind`, `BackupJobTrigger`, `BackupJobState`, `BackupJobStep`, `BackupJobError`, `BackupJob`, `BackupJobResponse`, `BackupJobStarted`, `BackupLastStatus`, `BackupScheduleMode`, `BackupSchedule`, `BackupScheduleResponse`, `UpdateBackupScheduleRequest`, `BackupOverview`, `CreateBackupRequest`, `PinBackupRequest`, `BackupSnapshotResponse`. `last_status` hat das benannte Schema `BackupLastStatus` (nullable im Zeitplan).
- `web/src/api/errors.ts` (`KNOWN_CODES`) kennt `backups_unavailable` und `backup_running`; `de.ts`/`en.ts` haben `errors.backups_unavailable`/`errors.backup_running`, `i18n.test.ts` hat beide in `ERROR_CODES`. Alles unter `backups.*` und die übrigen neuen Texte schreibt dieser Plan.
- MSW-Mocks (`web/src/mocks/handlers/backups.ts`, V6):
  - Server 1 „survival“ (paper, läuft): sechs Snapshots, neueste zuerst; vier `scheduled` (einer `pinned: true`), ein `manual` mit Notiz „vor Update auf 1.21.11“, ein `pre-restore`; Zeitplan `daily`, `time_of_day: 240`, Retention 24/7/4/6, `timezone: 'Europe/Berlin'`, `last_status: 'ok'`.
  - Server 2 „creative“: keine Snapshots, Zeitplan `off`, `last_status: 'failed'`, `last_error: 'RCON nicht erreichbar'`.
  - Server 4 „modded“: `available: false`, `snapshots: []`.
  - Server 3 „lobby“ (ohne Instanz): leere Übersicht (der Tab erscheint dort nicht).
  - `createBackup`: Job läuft über ~4 s `queued` → `saving` → `backing_up` → `resuming` → `done` und legt dann einen `manual`-Snapshot (mit der gesendeten Notiz) an; `restoreBackup`: `stopping` → `pre_restore` → `restoring` → `done`, legt einen `pre-restore`-Snapshot an; `deleteBackup`: `deleting` → `done`, entfernt den Snapshot. Ein zweiter Job → `409 backup_running`.
  - `pinBackup` vergibt eine neue zufällige 64-Hex-ID; nicht geplante → `400 validation_failed`.
  - `restoreBackup` und `deleteBackup` verlangen Sudo (`util.ts`, `sudo: true`), `downloadBackup` liefert eine kleine Zip.
  - `updateBackupSchedule` validiert wie V2 und rechnet `next_run_at` grob.
  - Admins haben `server.backups` auf allen Servern; `alice` hat auf Server 1 nur `server.view` + `server.power`.
  - `ServerSettings` hat `backup_timezone: 'Europe/Berlin'`; `PUT /servers/settings` übernimmt `backup_timezone` (Admin + Sudo); `DELETE /servers/{id}` akzeptiert `purge_backups`.

## Abweichungen

1. **Tab-Integration in Aufgabe 14:** Die Tests der Aufgaben 14–16 öffnen den Tab wie in 3b/3c über `ServerDetailPage` (`test-utils.tsx`). Aufgabe 14 verdrahtet deshalb den Tab und passt `server-detail-page.test.tsx` an (acht statt sieben Tabs).
2. **Alle Texte in Aufgabe 14:** Der komplette `backups.*`-Block plus `servers.detail.tabs.backups`, `servers.delete.purge_*`, `settings.backup_timezone.*` und `permissions.backups_warning` kommt in einem Schritt, damit spätere Aufgaben `de.ts`/`en.ts` nicht stückweise anfassen.
3. **Job über eigene Abfrage:** `BackupOverview.job` wird nicht ausgewertet; der Tab fragt `GET …/backups/job` separat (eine zusätzliche Anfrage beim Öffnen), damit nur diese eine Abfrage pollt und die teurere Übersicht (restic `snapshots`) nur bei Bedarf neu lädt.
4. **Zeitzonen-Auswahl:** `Intl.supportedValuesOf('timeZone')` enthält in V8 kein `UTC`; die Auswahl ist deshalb „aktueller Wert, `UTC`, dann die Liste“ (ohne Doppelte). Ohne `Intl.supportedValuesOf` (alte Browser) bleibt nur aktueller Wert + `UTC`.
5. **`deleteServer(id, purgeBackups)`:** Die vorhandene Funktion in `web/src/features/servers/api.ts` bekommt einen zweiten Parameter; der Query-Parameter wird nur bei `true` gesendet.
6. **Download als Link:** wie im Dateien-Tab ein `<a href … download>`; ein `429 rate_limited` (mehr als zwei Downloads) sieht der Nutzer als abgebrochenen Browser-Download, nicht als Toast (gleiches Verhalten wie `files.download`).

## Sicherheit im Frontend

- Notizen, Fehlertexte (`job.error`, `last_error`) und Snapshot-IDs sind React-Textknoten; kein `dangerouslySetInnerHTML` (Test in Aufgabe 14 mit `<b>`-Notiz).
- Die Download-URL wird aus Server-ID und einer Snapshot-ID gebaut, die `isSnapshotId` (64 Hex) bestanden hat; andere IDs bekommen keinen Link.
- Restore und Löschen laufen über den globalen Sudo-Dialog; ein abgebrochener Sudo-Dialog erzeugt keinen Fehler-Toast (`isSudoCancelled`).

## Review Focus (aus der Übersicht, für B3 relevant)

- **Gleichzeitigkeit (Focus 4):** Solange ein Job `queued`/`running` ist, sind „Backup erstellen“, Pin, Wiederherstellen und Löschen gesperrt; `409 backup_running` bekommt einen eigenen Text und lädt den Job neu (ein anderer Tab oder der Zeitplan hat ihn gestartet); ein Pin ersetzt die alte ID in der Liste sofort (zweiter Klick nutzt die neue ID). → Aufgaben 14, 15.
- **Betriebsfehler (Focus 5):** `available = false` → nur Hinweis; Job-Fehler (`insufficient_storage`, `backups_unavailable`, `rcon_unavailable`, …) werden übersetzt angezeigt, `job.error` als Detail. → Aufgaben 14, 15.
- **Panel stirbt mitten im Job (Focus 1):** Verschwindet ein laufender Job (Antwort `job: null`) oder taucht ein fremder auf, lädt der Tab die Übersicht neu, ohne einen Erfolg zu behaupten. → Aufgabe 14.

## Dateistruktur (B3)

```
web/src/features/backups/model.ts (+test)          (14) Validierung, Uhrzeit, Wochentage, Schlüssel, aktiver Job
web/src/features/backups/api.ts                    (14) Keys, Query-Options, Aufrufe, startBackupJob, replaceSnapshot
web/src/features/backups/messages.ts               (14) Fehlertexte für 409, Toast-Hook
web/src/features/backups/use-backup-job.ts         (14) Job-Abfrage (2 s solange aktiv), Ende → Neuladen + Toast
web/src/features/backups/job-status.tsx            (14) Job-Anzeige
web/src/features/backups/snapshot-list.tsx         (14–15) DataList mit Art, Notiz, Größe, Pin, Menü
web/src/features/backups/create-dialog.tsx         (14) Notiz-Dialog
web/src/features/backups/backups-tab.tsx (+test)   (14–16) Tab
web/src/features/backups/test-utils.tsx            (14) Fixtures, mockOverview, jobSequence, openBackups
web/src/features/backups/restore-dialog.tsx        (15) Namensbestätigung
web/src/features/backups/backups-actions.test.tsx  (15) Pin, Download, Restore, Löschen
web/src/features/backups/schedule-card.tsx (+test) (16) Zeitplan
web/src/features/servers/server-detail-page.tsx (+test) (14) Tab „Backups“
web/src/features/servers/api.ts, delete-server-section.tsx (16) purge_backups
web/src/features/settings/time-zones.ts (+test), backup-timezone-card.tsx, settings-page.tsx (+test) (16)
web/src/features/users/permissions-page.tsx (+test) (16) Hinweis bei server.backups
web/src/i18n/de.ts, en.ts, i18n.test.ts            (14) alle Texte von TP4 (ohne errors.*)
web/e2e/backups.spec.ts, web/playwright.config.ts  (17) Playwright
```

---

### Task 14: Grundlagen, Snapshot-Liste, Erstellen, Job und Tab

**Files:**
- Create: `web/src/features/backups/model.ts`, `model.test.ts`, `api.ts`, `messages.ts`, `use-backup-job.ts`, `job-status.tsx`, `snapshot-list.tsx`, `create-dialog.tsx`, `backups-tab.tsx`, `backups-tab.test.tsx`, `test-utils.tsx`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `web/src/features/servers/server-detail-page.test.tsx`
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`

**Interfaces:**
- Consumes: `api` (`@/api/client`), `unwrap`/`unwrapVoid`/`isApiError`/`isSudoCancelled` (`@/api/errors`), Backup-Typen aus `@/api/types` (Phase A), `DataList`/`Column` (`@/components/data-list`), `SectionCard` (`@/features/profile/section-card`), `FieldRow` (`@/features/servers/field-row`), `FieldError` (`@/features/servers/validation`), `formatBytes` (`@/features/files/format`), `useFormat` (`@/lib/format`), `errorMessage` (`@/lib/error-message`), `serverKey` (`@/features/servers/api`).
- Produces (von Aufgaben 15–17 genutzt):
  - `model.ts`: `NOTE_MAX_RUNES`, `KEEP_MAX`, `INTERVAL_MIN_HOURS`, `INTERVAL_MAX_HOURS`, `SCHEDULE_MODES`, `WEEKDAYS`, `KEEP_FIELDS`, `type KeepField`, `normalizeNote(s)`, `validateNote(s)`, `isSnapshotId(s)`, `isJobActive(data)`, `kindKey(kind)`, `stepKey(step)`, `jobErrorKey(code)`, `jobTitleKey(job)`, `minutesToTime(m)`, `timeToMinutes(s)`, `type ScheduleDraft`, `type ScheduleErrors`, `draftFromSchedule(s)`, `validateSchedule(d)`, `scheduleRequest(d, original)`, `sameSchedule(req, s)`.
  - `api.ts`: `JOB_POLL_MS`, `overviewKey`, `backupJobKey`, `overviewQueryOptions`, `backupJobQueryOptions`, `createBackup`, `restoreBackup`, `deleteBackup`, `pinBackup`, `updateBackupSchedule`, `backupDownloadUrl`, `startBackupJob`, `refreshJobOnConflict`, `replaceSnapshot`, `setSchedule`, `refreshBackups`.
  - `messages.ts`: `backupErrorMessage(err, t)`, `useBackupErrorToast()`.
  - `use-backup-job.ts`: `useBackupJob(serverId): { job: BackupJob | null; active: boolean }`.
  - `job-status.tsx`: `JobStatus({ job, onDismiss })`.
  - `snapshot-list.tsx`: `SnapshotList({ serverId, snapshots, busy, pendingId, onPin?, onRestore?, onDelete? })`.
  - `backups-tab.tsx`: `BackupsTab({ server })`.
  - `test-utils.tsx`: `snapshotId(n)`, `snapshot(n, over)`, `schedule(over)`, `overview(over)`, `backupJob(kind, state, over)`, `mockOverview(o)`, `jobSequence(seq)`, `openBackups(serverId?, username?, opts?)`.
  - i18n: kompletter Block `backups.*`, `servers.detail.tabs.backups`, `servers.delete.purge_backups`/`purge_backups_hint`, `settings.backup_timezone.*`, `permissions.backups_warning`.

- [ ] **Step 1: Tests für die reine Logik schreiben (rot)**

`web/src/features/backups/model.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { BackupJob, BackupSchedule } from '@/api/types'
import {
  draftFromSchedule,
  isJobActive,
  isSnapshotId,
  jobErrorKey,
  jobTitleKey,
  kindKey,
  minutesToTime,
  normalizeNote,
  sameSchedule,
  scheduleRequest,
  stepKey,
  timeToMinutes,
  validateNote,
  validateSchedule,
  WEEKDAYS,
} from './model'

const base: BackupSchedule = {
  mode: 'daily',
  interval_hours: 24,
  time_of_day: 240,
  weekday: 0,
  keep_last: 24,
  keep_daily: 7,
  keep_weekly: 4,
  keep_monthly: 6,
  timezone: 'Europe/Berlin',
  next_run_at: null,
  last_run_at: null,
  last_status: null,
  last_error: null,
  last_snapshot_id: null,
}

function job(state: BackupJob['state'], over: Partial<BackupJob> = {}): BackupJob {
  return {
    id: 'j1',
    kind: 'backup',
    trigger: 'manual',
    state,
    step: 'queued',
    queue_position: 0,
    bytes: 0,
    snapshot: null,
    error_code: null,
    error: '',
    queued_at: 1,
    started_at: 0,
    finished_at: 0,
    ...over,
  }
}

describe('notes', () => {
  it('trims and accepts an empty note as "no note"', () => {
    expect(normalizeNote('  vor Update  ')).toBe('vor Update')
    expect(validateNote('')).toBeNull()
    expect(validateNote(normalizeNote('   '))).toBeNull()
  })

  it('rejects control characters and more than 64 characters (runes, not bytes)', () => {
    expect(validateNote('a\tb')).toEqual({ key: 'backups.create.validation.control' })
    expect(validateNote('a\u007fb')).toEqual({ key: 'backups.create.validation.control' })
    expect(validateNote('ä'.repeat(64))).toBeNull()
    expect(validateNote('ä'.repeat(65))).toEqual({ key: 'backups.create.validation.too_long', params: { max: 64 } })
    expect(validateNote('😀'.repeat(64))).toBeNull()
  })
})

describe('snapshot ids', () => {
  it('accepts exactly 64 lowercase hex characters', () => {
    expect(isSnapshotId('a'.repeat(64))).toBe(true)
    expect(isSnapshotId('A'.repeat(64))).toBe(false)
    expect(isSnapshotId('a'.repeat(63))).toBe(false)
    expect(isSnapshotId(`${'a'.repeat(63)}/`)).toBe(false)
  })
})

describe('jobs', () => {
  it('treats queued and running jobs as active', () => {
    expect(isJobActive(undefined)).toBe(false)
    expect(isJobActive({ job: null })).toBe(false)
    expect(isJobActive({ job: job('queued') })).toBe(true)
    expect(isJobActive({ job: job('running') })).toBe(true)
    expect(isJobActive({ job: job('done') })).toBe(false)
    expect(isJobActive({ job: job('failed') })).toBe(false)
  })

  it('builds the translation keys', () => {
    expect(kindKey('pre-restore')).toBe('backups.kind.pre_restore')
    expect(kindKey('manual')).toBe('backups.kind.manual')
    expect(stepKey('backing_up')).toBe('backups.step.backing_up')
    expect(jobErrorKey('insufficient_storage')).toBe('backups.job_error.insufficient_storage')
    expect(jobTitleKey(job('running', { kind: 'restore' }))).toBe('backups.job.active.restore')
    expect(jobTitleKey(job('queued'))).toBe('backups.job.active.backup')
    expect(jobTitleKey(job('done', { kind: 'delete' }))).toBe('backups.job.done.delete')
    expect(jobTitleKey(job('failed'))).toBe('backups.job.failed.backup')
  })
})

describe('time of day', () => {
  it('converts minutes and HH:MM both ways', () => {
    expect(minutesToTime(0)).toBe('00:00')
    expect(minutesToTime(240)).toBe('04:00')
    expect(minutesToTime(1439)).toBe('23:59')
    expect(timeToMinutes('04:30')).toBe(270)
    expect(timeToMinutes('23:59')).toBe(1439)
    expect(timeToMinutes('24:00')).toBeNull()
    expect(timeToMinutes('4:30')).toBeNull()
    expect(timeToMinutes('')).toBeNull()
  })

  it('lists the weekdays Monday first with the contract values (0 = Sunday)', () => {
    expect(WEEKDAYS).toEqual([1, 2, 3, 4, 5, 6, 0])
  })
})

describe('schedule form', () => {
  it('starts from the stored schedule and sends it unchanged', () => {
    const draft = draftFromSchedule(base)
    expect(draft).toEqual({
      mode: 'daily',
      interval_hours: '24',
      time: '04:00',
      weekday: 0,
      keep_last: '24',
      keep_daily: '7',
      keep_weekly: '4',
      keep_monthly: '6',
    })
    expect(validateSchedule(draft)).toEqual({})
    const req = scheduleRequest(draft, base)
    expect(req).toEqual({
      mode: 'daily',
      interval_hours: 24,
      time_of_day: 240,
      weekday: 0,
      keep_last: 24,
      keep_daily: 7,
      keep_weekly: 4,
      keep_monthly: 6,
    })
    expect(req && sameSchedule(req, base)).toBe(true)
  })

  it('checks only the fields of the chosen mode', () => {
    const draft = { ...draftFromSchedule(base), mode: 'interval' as const, interval_hours: '169', time: 'x' }
    expect(validateSchedule(draft)).toEqual({ interval_hours: { key: 'backups.schedule.validation.range', params: { min: 1, max: 168 } } })
    expect(validateSchedule({ ...draft, interval_hours: '0' }).interval_hours).toBeDefined()
    expect(validateSchedule({ ...draft, interval_hours: '1.5' }).interval_hours).toBeDefined()
    expect(validateSchedule({ ...draft, interval_hours: '6' })).toEqual({})
    expect(validateSchedule({ ...draftFromSchedule(base), time: '25:00' })).toEqual({ time: { key: 'backups.schedule.validation.time' } })
  })

  it('needs one retention value above 0 unless the schedule is off', () => {
    const zero = { ...draftFromSchedule(base), keep_last: '0', keep_daily: '0', keep_weekly: '0', keep_monthly: '0' }
    expect(validateSchedule(zero)).toEqual({ keep: { key: 'backups.schedule.validation.keep_required' } })
    expect(validateSchedule({ ...zero, mode: 'off' })).toEqual({})
    expect(validateSchedule({ ...draftFromSchedule(base), keep_monthly: '1001' })).toEqual({
      keep_monthly: { key: 'backups.schedule.validation.range', params: { min: 0, max: 1000 } },
    })
  })

  it('keeps hidden fields from the stored schedule', () => {
    const draft = { ...draftFromSchedule(base), mode: 'interval' as const, interval_hours: '6', time: 'kaputt' }
    expect(scheduleRequest(draft, base)).toMatchObject({ mode: 'interval', interval_hours: 6, time_of_day: 240, weekday: 0 })
    expect(scheduleRequest({ ...draft, interval_hours: '' }, base)).toBeNull()
  })
})
```

- [ ] **Step 2: Rot prüfen**

Run: `make check` → FAIL (`./model` existiert nicht, tsc meldet fehlende Module).

- [ ] **Step 3: `model.ts` implementieren**

`web/src/features/backups/model.ts`:

```ts
import type {
  BackupJob,
  BackupJobError,
  BackupJobResponse,
  BackupJobStep,
  BackupKind,
  BackupSchedule,
  BackupScheduleMode,
  UpdateBackupScheduleRequest,
} from '@/api/types'
import type { FieldError } from '@/features/servers/validation'

/** Contract limits (overview "Global Constraints"); the panel validates again. */
export const NOTE_MAX_RUNES = 64
export const KEEP_MAX = 1000
export const INTERVAL_MIN_HOURS = 1
export const INTERVAL_MAX_HOURS = 168
export const SCHEDULE_MODES: readonly BackupScheduleMode[] = ['off', 'interval', 'daily', 'weekly']
/** Monday first as in German calendars; the values are the contract's (0 = Sunday). */
export const WEEKDAYS = [1, 2, 3, 4, 5, 6, 0] as const
export const KEEP_FIELDS = ['keep_last', 'keep_daily', 'keep_weekly', 'keep_monthly'] as const
export type KeepField = (typeof KEEP_FIELDS)[number]

const SNAPSHOT_ID = /^[0-9a-f]{64}$/
const TIME = /^([01]\d|2[0-3]):([0-5]\d)$/
const DIGITS = /^\d+$/

function hasControl(s: string): boolean {
  for (const ch of s) {
    const code = ch.codePointAt(0) ?? 0
    if (code < 0x20 || code === 0x7f) return true
  }
  return false
}

/** The note as sent: surrounding spaces removed; "" means no note. */
export function normalizeNote(input: string): string {
  return input.trim()
}

/** Expects a normalized note (normalizeNote). */
export function validateNote(note: string): FieldError | null {
  if (note === '') return null
  if (hasControl(note)) return { key: 'backups.create.validation.control' }
  if ([...note].length > NOTE_MAX_RUNES) return { key: 'backups.create.validation.too_long', params: { max: NOTE_MAX_RUNES } }
  return null
}

export function isSnapshotId(id: string): boolean {
  return SNAPSHOT_ID.test(id)
}

export function isJobActive(data: BackupJobResponse | undefined): boolean {
  const state = data?.job?.state
  return state === 'queued' || state === 'running'
}

export function kindKey(kind: BackupKind): string {
  return `backups.kind.${kind === 'pre-restore' ? 'pre_restore' : kind}`
}

export function stepKey(step: BackupJobStep): string {
  return `backups.step.${step}`
}

export function jobErrorKey(code: BackupJobError): string {
  return `backups.job_error.${code}`
}

/** Region title and toast of a job: "Backup läuft …", "Wiederherstellung abgeschlossen", … */
export function jobTitleKey(job: BackupJob): string {
  const phase = job.state === 'done' ? 'done' : job.state === 'failed' ? 'failed' : 'active'
  return `backups.job.${phase}.${job.kind}`
}

export function minutesToTime(minutes: number): string {
  const h = Math.floor(minutes / 60)
  const m = minutes % 60
  return `${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`
}

export function timeToMinutes(value: string): number | null {
  const match = TIME.exec(value)
  if (!match) return null
  return Number(match[1]) * 60 + Number(match[2])
}

/** Form state: numbers stay strings while the user types. */
export interface ScheduleDraft {
  mode: BackupScheduleMode
  interval_hours: string
  time: string
  weekday: number
  keep_last: string
  keep_daily: string
  keep_weekly: string
  keep_monthly: string
}

export type ScheduleErrors = Partial<Record<'interval_hours' | 'time' | 'keep' | KeepField, FieldError>>

export function draftFromSchedule(s: BackupSchedule): ScheduleDraft {
  return {
    mode: s.mode,
    interval_hours: String(s.interval_hours),
    time: minutesToTime(s.time_of_day),
    weekday: s.weekday,
    keep_last: String(s.keep_last),
    keep_daily: String(s.keep_daily),
    keep_weekly: String(s.keep_weekly),
    keep_monthly: String(s.keep_monthly),
  }
}

function parseInt0(value: string): number | null {
  return DIGITS.test(value) ? Number(value) : null
}

function inRange(value: string, min: number, max: number): number | null {
  const n = parseInt0(value)
  return n !== null && n >= min && n <= max ? n : null
}

function rangeError(min: number, max: number): FieldError {
  return { key: 'backups.schedule.validation.range', params: { min, max } }
}

/** Only the visible fields are checked: hidden ones keep their stored values (scheduleRequest). */
export function validateSchedule(d: ScheduleDraft): ScheduleErrors {
  const errors: ScheduleErrors = {}
  if (d.mode === 'interval' && inRange(d.interval_hours, INTERVAL_MIN_HOURS, INTERVAL_MAX_HOURS) === null) {
    errors.interval_hours = rangeError(INTERVAL_MIN_HOURS, INTERVAL_MAX_HOURS)
  }
  if ((d.mode === 'daily' || d.mode === 'weekly') && timeToMinutes(d.time) === null) {
    errors.time = { key: 'backups.schedule.validation.time' }
  }
  let anyKeep = false
  for (const field of KEEP_FIELDS) {
    const n = inRange(d[field], 0, KEEP_MAX)
    if (n === null) errors[field] = rangeError(0, KEEP_MAX)
    else if (n > 0) anyKeep = true
  }
  const keepValid = KEEP_FIELDS.every((f) => errors[f] === undefined)
  if (d.mode !== 'off' && keepValid && !anyKeep) errors.keep = { key: 'backups.schedule.validation.keep_required' }
  return errors
}

/** null while the draft is invalid. Fields the mode does not show fall back to the stored values. */
export function scheduleRequest(d: ScheduleDraft, original: BackupSchedule): UpdateBackupScheduleRequest | null {
  if (Object.keys(validateSchedule(d)).length > 0) return null
  const interval = inRange(d.interval_hours, INTERVAL_MIN_HOURS, INTERVAL_MAX_HOURS) ?? original.interval_hours
  const time = timeToMinutes(d.time) ?? original.time_of_day
  return {
    mode: d.mode,
    interval_hours: interval,
    time_of_day: time,
    weekday: d.weekday,
    keep_last: Number(d.keep_last),
    keep_daily: Number(d.keep_daily),
    keep_weekly: Number(d.keep_weekly),
    keep_monthly: Number(d.keep_monthly),
  }
}

export function sameSchedule(req: UpdateBackupScheduleRequest, s: BackupSchedule): boolean {
  return (
    req.mode === s.mode &&
    req.interval_hours === s.interval_hours &&
    req.time_of_day === s.time_of_day &&
    req.weekday === s.weekday &&
    req.keep_last === s.keep_last &&
    req.keep_daily === s.keep_daily &&
    req.keep_weekly === s.keep_weekly &&
    req.keep_monthly === s.keep_monthly
  )
}
```

- [ ] **Step 4: Grün prüfen**

Run: `make check` (PASS), dann `make test-vm SUITE=web` bzw. einzeln `npx vitest run src/features/backups/model.test.ts` auf der VM → PASS.

- [ ] **Step 5: Texte anlegen (de/en) und dynamische Schlüssel registrieren**

In `web/src/i18n/de.ts` einfügen:

- unter `servers.detail.tabs` nach `mods`: `backups: 'Backups',`
- unter `servers.delete` am Ende:

```ts
      purge_backups: 'Backups ebenfalls löschen',
      purge_backups_hint: 'Ohne diese Option bleiben die Backups des Servers auf dem Host liegen und belegen weiter Platz.',
```

- unter `permissions` nach `plugins_warning`:

```ts
    backups_warning:
      '„Backups“ erlaubt, alle Dateien des Servers herunterzuladen (auch Konfigurationen mit Passwörtern) und den Server auf einen älteren Stand zurückzusetzen. Vergib es nur an Personen, denen du vertraust.',
```

- unter `settings` (neben `base_domain`):

```ts
    backup_timezone: {
      title: 'Zeitzone für Backups',
      description: 'In dieser Zeitzone laufen die täglichen und wöchentlichen Backup-Zeitpläne aller Server.',
      label: 'Zeitzone',
      save: 'Zeitzone speichern',
      saved: 'Zeitzone gespeichert.',
    },
```

- neuer Top-Level-Block nach `plugins`:

```ts
  backups: {
    unavailable: 'Backups sind auf diesem Host nicht verfügbar (restic fehlt oder der Dateidienst ist nicht erreichbar).',
    snapshots: {
      title: 'Snapshots',
      caption: 'Snapshots dieses Servers',
      empty: 'Noch keine Backups. Erstelle das erste mit „Backup erstellen“.',
      col_time: 'Zeitpunkt',
      col_kind: 'Art',
      col_note: 'Notiz',
      col_size: 'Größe',
      col_actions: 'Aktionen',
      pinned: 'Angepinnt',
      actions_label: 'Aktionen für den Snapshot vom {{when}}',
      pin: 'Anpinnen',
      unpin: 'Lösen',
      download: 'Als Zip herunterladen',
      restore: 'Wiederherstellen',
      delete: 'Löschen',
      pinned_done: 'Snapshot angepinnt.',
      unpinned_done: 'Snapshot gelöst.',
    },
    kind: {
      scheduled: 'Geplant',
      manual: 'Manuell',
      pre_restore: 'Vor Restore',
    },
    create: {
      button: 'Backup erstellen',
      title: 'Backup erstellen',
      description: 'Läuft der Server, speichert er während des Backups nicht; danach geht es normal weiter.',
      note_label: 'Notiz (optional)',
      note_hint: 'Höchstens 64 Zeichen, z. B. „vor Update auf 1.22“.',
      submit: 'Backup starten',
      started: 'Backup gestartet.',
      validation: {
        control: 'Die Notiz darf keine Steuerzeichen enthalten.',
        too_long: 'Die Notiz darf höchstens {{max}} Zeichen lang sein.',
      },
    },
    restore: {
      title: 'Snapshot vom {{when}} wiederherstellen?',
      description:
        'Das Panel stoppt den Server, sichert den aktuellen Stand als Snapshot „Vor Restore“, ersetzt das Datenverzeichnis vollständig durch diesen Snapshot und startet den Server wieder, wenn er laufen soll.',
      confirm_label: 'Zum Bestätigen den Namen „{{name}}“ eintippen',
      submit: 'Wiederherstellen',
      started: 'Wiederherstellung gestartet.',
    },
    delete: {
      title: 'Snapshot vom {{when}} löschen?',
      description: 'Der Snapshot wird endgültig entfernt. Das lässt sich nicht rückgängig machen.',
      submit: 'Snapshot löschen',
      started: 'Löschen gestartet.',
    },
    job: {
      active: {
        backup: 'Backup läuft …',
        restore: 'Wiederherstellung läuft …',
        delete: 'Snapshot wird gelöscht …',
      },
      done: {
        backup: 'Backup abgeschlossen',
        restore: 'Wiederherstellung abgeschlossen',
        delete: 'Snapshot gelöscht',
      },
      failed: {
        backup: 'Backup fehlgeschlagen',
        restore: 'Wiederherstellung fehlgeschlagen',
        delete: 'Löschen fehlgeschlagen',
      },
      scheduled: 'Geplanter Lauf',
      queue_position: 'Wartet auf einen freien Platz (Position {{position}}).',
      bytes: '{{bytes}} verarbeitet',
      dismiss: 'Ausblenden',
    },
    step: {
      queued: 'Wartet',
      saving: 'Speichern im Spiel wird angehalten …',
      backing_up: 'Daten werden gesichert …',
      resuming: 'Speichern im Spiel läuft wieder …',
      pruning: 'Alte Snapshots werden aufgeräumt …',
      stopping: 'Server wird gestoppt …',
      pre_restore: 'Sicherheits-Snapshot wird erstellt …',
      restoring: 'Snapshot wird eingespielt …',
      deleting: 'Snapshot wird gelöscht …',
      done: 'Fertig.',
    },
    job_error: {
      rcon_unavailable: 'Der Server antwortet nicht auf RCON (startet er noch?). Es wurde kein Snapshot erstellt.',
      agent_unavailable: 'Der Agent ist nicht erreichbar.',
      files_unavailable: 'Der Dateidienst ist nicht erreichbar.',
      files_busy: 'Der Dateidienst ist für diesen Server gerade beschäftigt (z. B. beim Entpacken). Bitte später erneut versuchen.',
      backups_unavailable: 'Backups sind auf diesem Host nicht verfügbar (restic fehlt).',
      insufficient_storage: 'Auf dem Host ist nicht genug Platz frei.',
      too_large: 'Das Datenverzeichnis überschreitet die Größengrenze für Backups.',
      stop_timeout: 'Der Server ließ sich nicht rechtzeitig stoppen.',
      canceled: 'Der Auftrag wurde abgebrochen (das Panel wurde beendet).',
      internal: 'Interner Fehler.',
    },
    error: {
      job_running: 'Für diesen Server läuft oder wartet bereits ein Backup-Auftrag.',
      conflict: 'Das geht gerade nicht: Der Server hat noch keine Daten, oder ein Dateiauftrag läuft.',
    },
    schedule: {
      title: 'Zeitplan',
      description: 'Geplante Backups und wie lange sie aufbewahrt werden.',
      mode_label: 'Zeitplan',
      mode: {
        off: 'Aus',
        interval: 'Alle N Stunden',
        daily: 'Täglich',
        weekly: 'Wöchentlich',
      },
      interval_label: 'Intervall in Stunden',
      time_label: 'Uhrzeit',
      weekday_label: 'Wochentag',
      weekday: {
        0: 'Sonntag',
        1: 'Montag',
        2: 'Dienstag',
        3: 'Mittwoch',
        4: 'Donnerstag',
        5: 'Freitag',
        6: 'Samstag',
      },
      retention_title: 'Aufbewahrung',
      retention_hint: 'Gilt nur für geplante Snapshots. Manuelle, vor einem Restore erstellte und angepinnte Snapshots bleiben, bis du sie löschst.',
      keep_last: 'Letzte behalten',
      keep_daily: 'Tage behalten',
      keep_weekly: 'Wochen behalten',
      keep_monthly: 'Monate behalten',
      timezone_hint: 'Uhrzeiten gelten in der Zeitzone des Panels: {{timezone}}. Administratoren ändern sie unter Einstellungen.',
      next_run: 'Nächster Lauf: {{when}} ({{relative}})',
      last_run: 'Letzter geplanter Lauf: {{when}} – {{status}}',
      last_status: {
        ok: 'erfolgreich',
        failed: 'fehlgeschlagen',
        running: 'läuft',
      },
      save: 'Zeitplan speichern',
      saved: 'Zeitplan gespeichert.',
      validation: {
        range: 'Erlaubt sind ganze Zahlen von {{min}} bis {{max}}.',
        time: 'Bitte eine Uhrzeit im Format HH:MM angeben.',
        keep_required: 'Mindestens ein Aufbewahrungswert muss größer als 0 sein.',
      },
    },
  },
```

In `web/src/i18n/en.ts` an denselben Stellen:

```ts
// servers.detail.tabs
      backups: 'Backups',
// servers.delete
      purge_backups: 'Delete backups too',
      purge_backups_hint: 'Without this option the server’s backups stay on the host and keep using space.',
// permissions
    backups_warning:
      '“Backups” allows downloading every file of the server (including configurations with passwords) and resetting the server to an older state. Only grant it to people you trust.',
// settings
    backup_timezone: {
      title: 'Time zone for backups',
      description: 'Daily and weekly backup schedules of all servers run in this time zone.',
      label: 'Time zone',
      save: 'Save time zone',
      saved: 'Time zone saved.',
    },
```

```ts
  backups: {
    unavailable: 'Backups are not available on this host (restic is missing or the file service is unreachable).',
    snapshots: {
      title: 'Snapshots',
      caption: 'Snapshots of this server',
      empty: 'No backups yet. Create the first one with “Create backup”.',
      col_time: 'Time',
      col_kind: 'Kind',
      col_note: 'Note',
      col_size: 'Size',
      col_actions: 'Actions',
      pinned: 'Pinned',
      actions_label: 'Actions for the snapshot from {{when}}',
      pin: 'Pin',
      unpin: 'Unpin',
      download: 'Download as zip',
      restore: 'Restore',
      delete: 'Delete',
      pinned_done: 'Snapshot pinned.',
      unpinned_done: 'Snapshot unpinned.',
    },
    kind: {
      scheduled: 'Scheduled',
      manual: 'Manual',
      pre_restore: 'Before restore',
    },
    create: {
      button: 'Create backup',
      title: 'Create backup',
      description: 'If the server is running, it does not save during the backup and continues normally afterwards.',
      note_label: 'Note (optional)',
      note_hint: 'At most 64 characters, e.g. “before update to 1.22”.',
      submit: 'Start backup',
      started: 'Backup started.',
      validation: {
        control: 'The note must not contain control characters.',
        too_long: 'The note can be at most {{max}} characters long.',
      },
    },
    restore: {
      title: 'Restore the snapshot from {{when}}?',
      description:
        'The panel stops the server, saves the current state as a “Before restore” snapshot, replaces the whole data directory with this snapshot and starts the server again if it should be running.',
      confirm_label: 'Type the name “{{name}}” to confirm',
      submit: 'Restore',
      started: 'Restore started.',
    },
    delete: {
      title: 'Delete the snapshot from {{when}}?',
      description: 'The snapshot is removed for good. This cannot be undone.',
      submit: 'Delete snapshot',
      started: 'Deletion started.',
    },
    job: {
      active: {
        backup: 'Backup running …',
        restore: 'Restore running …',
        delete: 'Deleting snapshot …',
      },
      done: {
        backup: 'Backup finished',
        restore: 'Restore finished',
        delete: 'Snapshot deleted',
      },
      failed: {
        backup: 'Backup failed',
        restore: 'Restore failed',
        delete: 'Deletion failed',
      },
      scheduled: 'Scheduled run',
      queue_position: 'Waiting for a free slot (position {{position}}).',
      bytes: '{{bytes}} processed',
      dismiss: 'Dismiss',
    },
    step: {
      queued: 'Waiting',
      saving: 'Pausing saves in the game …',
      backing_up: 'Backing up data …',
      resuming: 'Saves in the game resume …',
      pruning: 'Cleaning up old snapshots …',
      stopping: 'Stopping the server …',
      pre_restore: 'Creating a safety snapshot …',
      restoring: 'Restoring the snapshot …',
      deleting: 'Deleting the snapshot …',
      done: 'Done.',
    },
    job_error: {
      rcon_unavailable: 'The server does not answer RCON (is it still starting?). No snapshot was created.',
      agent_unavailable: 'The agent is unreachable.',
      files_unavailable: 'The file service is unreachable.',
      files_busy: 'The file service is busy with another job for this server (for example an extract). Try again later.',
      backups_unavailable: 'Backups are not available on this host (restic is missing).',
      insufficient_storage: 'There is not enough free space on the host.',
      too_large: 'The data directory exceeds the size limit for backups.',
      stop_timeout: 'The server did not stop in time.',
      canceled: 'The job was cancelled (the panel shut down).',
      internal: 'Internal error.',
    },
    error: {
      job_running: 'A backup job for this server is already running or waiting.',
      conflict: 'Not possible right now: the server has no data yet, or a file job is running.',
    },
    schedule: {
      title: 'Schedule',
      description: 'Scheduled backups and how long they are kept.',
      mode_label: 'Schedule',
      mode: {
        off: 'Off',
        interval: 'Every N hours',
        daily: 'Daily',
        weekly: 'Weekly',
      },
      interval_label: 'Interval in hours',
      time_label: 'Time',
      weekday_label: 'Weekday',
      weekday: {
        0: 'Sunday',
        1: 'Monday',
        2: 'Tuesday',
        3: 'Wednesday',
        4: 'Thursday',
        5: 'Friday',
        6: 'Saturday',
      },
      retention_title: 'Retention',
      retention_hint: 'Applies to scheduled snapshots only. Manual, pre-restore and pinned snapshots stay until you delete them.',
      keep_last: 'Keep last',
      keep_daily: 'Keep days',
      keep_weekly: 'Keep weeks',
      keep_monthly: 'Keep months',
      timezone_hint: 'Times are in the panel’s time zone: {{timezone}}. Administrators change it under Settings.',
      next_run: 'Next run: {{when}} ({{relative}})',
      last_run: 'Last scheduled run: {{when}} – {{status}}',
      last_status: {
        ok: 'successful',
        failed: 'failed',
        running: 'running',
      },
      save: 'Save schedule',
      saved: 'Schedule saved.',
      validation: {
        range: 'Whole numbers from {{min}} to {{max}} are allowed.',
        time: 'Please enter a time as HH:MM.',
        keep_required: 'At least one retention value must be above 0.',
      },
    },
  },
```

In `web/src/i18n/i18n.test.ts` in `DYNAMIC_KEYS` (nach den `plugins.*`-Einträgen):

```ts
  ...['scheduled', 'manual', 'pre_restore'].map((k) => `backups.kind.${k}`),
  ...['queued', 'saving', 'backing_up', 'resuming', 'pruning', 'stopping', 'pre_restore', 'restoring', 'deleting', 'done'].map(
    (s) => `backups.step.${s}`,
  ),
  ...[
    'rcon_unavailable',
    'agent_unavailable',
    'files_unavailable',
    'files_busy',
    'backups_unavailable',
    'insufficient_storage',
    'too_large',
    'stop_timeout',
    'canceled',
    'internal',
  ].map((c) => `backups.job_error.${c}`),
  ...['active', 'done', 'failed'].flatMap((p) => ['backup', 'restore', 'delete'].map((k) => `backups.job.${p}.${k}`)),
  ...['off', 'interval', 'daily', 'weekly'].map((m) => `backups.schedule.mode.${m}`),
  ...['0', '1', '2', '3', '4', '5', '6'].map((d) => `backups.schedule.weekday.${d}`),
  ...['ok', 'failed', 'running'].map((s) => `backups.schedule.last_status.${s}`),
  ...['range', 'time', 'keep_required'].map((v) => `backups.schedule.validation.${v}`),
  ...['control', 'too_long'].map((v) => `backups.create.validation.${v}`),
  ...['keep_last', 'keep_daily', 'keep_weekly', 'keep_monthly'].map((k) => `backups.schedule.${k}`),
```

- [ ] **Step 6: Fixtures und Tab-Tests schreiben (rot)**

`web/src/features/backups/test-utils.tsx`:

```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import type { BackupJob, BackupOverview, BackupSchedule, BackupSnapshot } from '@/api/types'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

/** A valid 64-hex snapshot id that is unique per n. */
export function snapshotId(n: number): string {
  return n.toString(16).padStart(64, '0')
}

const HOUR = 3_600_000

export function snapshot(n: number, over: Partial<BackupSnapshot> = {}): BackupSnapshot {
  const id = snapshotId(n)
  return { id, short_id: id.slice(-8), time: Date.now() - n * HOUR, kind: 'scheduled', pinned: false, note: null, bytes: 512 * 1024 * 1024, ...over }
}

export function schedule(over: Partial<BackupSchedule> = {}): BackupSchedule {
  return {
    mode: 'daily',
    interval_hours: 24,
    time_of_day: 240,
    weekday: 0,
    keep_last: 24,
    keep_daily: 7,
    keep_weekly: 4,
    keep_monthly: 6,
    timezone: 'Europe/Berlin',
    next_run_at: Date.now() + 5 * HOUR,
    last_run_at: Date.now() - 19 * HOUR,
    last_status: 'ok',
    last_error: null,
    last_snapshot_id: snapshotId(1),
    ...over,
  }
}

export function overview(over: Partial<BackupOverview> = {}): BackupOverview {
  return { available: true, snapshots: [], job: null, schedule: schedule(), ...over }
}

export function backupJob(kind: BackupJob['kind'], state: BackupJob['state'], over: Partial<BackupJob> = {}): BackupJob {
  const now = Date.now()
  return {
    id: 'job-1',
    kind,
    trigger: 'manual',
    state,
    step: state === 'queued' ? 'queued' : state === 'running' ? 'backing_up' : 'done',
    queue_position: 0,
    bytes: 0,
    snapshot: null,
    error_code: null,
    error: '',
    queued_at: now - 2000,
    started_at: state === 'queued' ? 0 : now - 1000,
    finished_at: state === 'done' || state === 'failed' ? now : 0,
    ...over,
  }
}

/** Replaces the mock overview for one test; the job stays null unless jobSequence is used. */
export function mockOverview(o: BackupOverview) {
  server.use(
    http.get('*/api/servers/:id/backups', () => HttpResponse.json(o)),
    http.get('*/api/servers/:id/backups/job', () => HttpResponse.json({ job: null })),
  )
}

/** GET …/backups/job answers null until `started`, then the sequence (the last entry repeats). */
export function jobSequence(sequence: BackupJob[]) {
  const state = { started: false, polls: 0 }
  server.use(
    http.get('*/api/servers/:id/backups/job', () => {
      if (!state.started) return HttpResponse.json({ job: null })
      const next = sequence[Math.min(state.polls, sequence.length - 1)]
      state.polls += 1
      return HttpResponse.json({ job: next })
    }),
  )
  return state
}

export async function openBackups(serverId = 1, username = 'admin', opts: { sudo?: boolean } = { sudo: true }) {
  const user = userEvent.setup()
  const { queryClient } = await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username, { sudo: opts.sudo }) })
  await user.click(await screen.findByRole('tab', { name: 'Backups' }))
  return { user, queryClient }
}
```

`web/src/features/backups/backups-tab.test.tsx`:

```tsx
import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '@/mocks/node'
import { JOB_POLL_MS } from './api'
import { backupJob, jobSequence, mockOverview, openBackups, overview, snapshot } from './test-utils'

// Real time keeps flowing (MSW, userEvent); advanceTimersByTimeAsync jumps over the 2-s poll interval.
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('backups tab', () => {
  it('lists the snapshots with kind, note, size and pin, newest first', async () => {
    mockOverview(
      overview({
        snapshots: [
          snapshot(1, { kind: 'manual', note: '<b>vor Update</b>' }),
          snapshot(2, { pinned: true }),
          snapshot(3, { kind: 'pre-restore', bytes: 1024 }),
        ],
      }),
    )
    await openBackups()
    const list = await screen.findByRole('region', { name: 'Snapshots' })
    expect(within(list).getByText('Manuell')).toBeInTheDocument()
    // Notes are text, never markup.
    expect(within(list).getByText('<b>vor Update</b>')).toBeInTheDocument()
    expect(within(list).getAllByText('512 MiB')).toHaveLength(2)
    expect(within(list).getByText('1 KiB')).toBeInTheDocument()
    expect(within(list).getByText('Vor Restore')).toBeInTheDocument()
    expect(within(list).getByText('Angepinnt')).toBeInTheDocument()
    expect(within(list).getAllByText('Geplant')).toHaveLength(1)
  })

  it('shows an empty list with a hint', async () => {
    mockOverview(overview())
    await openBackups()
    expect(await screen.findByText('Noch keine Backups. Erstelle das erste mit „Backup erstellen“.')).toBeInTheDocument()
  })

  it('shows only a hint when backups are unavailable on the host', async () => {
    mockOverview(overview({ available: false }))
    await openBackups()
    expect(await screen.findByText(/Backups sind auf diesem Host nicht verfügbar/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Backup erstellen' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Zeitplan' })).not.toBeInTheDocument()
  })

  it('validates the note and starts a backup with the trimmed note', async () => {
    mockOverview(overview())
    let body: unknown = null
    server.use(
      http.post('*/api/servers/:id/backups', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ job: backupJob('backup', 'queued', { queue_position: 1 }) }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Backup erstellen' })
    const note = within(dialog).getByLabelText('Notiz (optional)')
    await user.type(note, 'x'.repeat(65))
    expect(within(dialog).getByText('Die Notiz darf höchstens 64 Zeichen lang sein.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Backup starten' })).toBeDisabled()
    await user.clear(note)
    await user.type(note, '  vor Update  ')
    await user.click(within(dialog).getByRole('button', { name: 'Backup starten' }))
    await waitFor(() => expect(body).toEqual({ note: 'vor Update' }))
    expect(await screen.findByText('Backup gestartet.')).toBeInTheDocument()
    const job = await screen.findByRole('region', { name: 'Backup läuft …' })
    expect(within(job).getByText('Wartet auf einen freien Platz (Position 1).')).toBeInTheDocument()
  })

  it('sends no note when the field is empty', async () => {
    mockOverview(overview())
    let body: unknown = null
    server.use(
      http.post('*/api/servers/:id/backups', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ job: backupJob('backup', 'queued') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))
    await waitFor(() => expect(body).toEqual({}))
  })

  it('follows the job, locks the actions and reloads the snapshots when it is done', async () => {
    let listed = [snapshot(2)]
    let overviewCalls = 0
    server.use(
      http.get('*/api/servers/:id/backups', () => {
        overviewCalls += 1
        return HttpResponse.json(overview({ snapshots: listed }))
      }),
    )
    const jobs = jobSequence([
      backupJob('backup', 'running', { step: 'saving' }),
      backupJob('backup', 'running', { step: 'backing_up', bytes: 3 * 1024 * 1024 }),
      backupJob('backup', 'done', { snapshot: snapshot(1).id }),
    ])
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        jobs.started = true
        return HttpResponse.json({ job: backupJob('backup', 'queued') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))

    const progress = await screen.findByRole('region', { name: 'Backup läuft …' })
    expect(screen.getByRole('button', { name: 'Backup erstellen' })).toBeDisabled()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await within(progress).findByText('Speichern im Spiel wird angehalten …')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await within(progress).findByText('3 MiB verarbeitet')).toBeInTheDocument()
    const before = overviewCalls
    listed = [snapshot(1, { kind: 'manual' }), snapshot(2)]
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByRole('region', { name: 'Backup abgeschlossen' })).toBeInTheDocument()
    expect(screen.getAllByText('Backup abgeschlossen').length).toBeGreaterThan(0) // region title + toast
    await waitFor(() => expect(overviewCalls).toBeGreaterThan(before))
    expect(await screen.findByText('Manuell')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Backup erstellen' })).toBeEnabled()
    const seen = jobs.polls
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS * 4))
    expect(jobs.polls).toBe(seen)
  })

  it('shows a failed job with its translated reason and the detail text', async () => {
    mockOverview(overview())
    const jobs = jobSequence([
      backupJob('backup', 'failed', { error_code: 'insufficient_storage', error: 'only 1 GiB free' }),
    ])
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        jobs.started = true
        return HttpResponse.json({ job: backupJob('backup', 'running') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    const failed = await screen.findByRole('region', { name: 'Backup fehlgeschlagen' })
    expect(within(failed).getByText('Auf dem Host ist nicht genug Platz frei.')).toBeInTheDocument()
    expect(within(failed).getByText('only 1 GiB free')).toBeInTheDocument()
    await act(async () => {
      within(failed).getByRole('button', { name: 'Ausblenden' }).click()
    })
    expect(screen.queryByRole('region', { name: 'Backup fehlgeschlagen' })).not.toBeInTheDocument()
  })

  it('explains backup_running and picks up the job another tab started', async () => {
    mockOverview(overview())
    const jobs = jobSequence([backupJob('backup', 'running', { trigger: 'scheduled' })])
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        jobs.started = true
        return HttpResponse.json({ error: 'backup_running', message: 'a job runs' }, { status: 409 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))
    expect(await screen.findByText('Für diesen Server läuft oder wartet bereits ein Backup-Auftrag.')).toBeInTheDocument()
    const progress = await screen.findByRole('region', { name: 'Backup läuft …' })
    expect(within(progress).getByText('Geplanter Lauf')).toBeInTheDocument()
  })

  it('reloads the snapshots without a toast when a running job vanishes (panel restart)', async () => {
    let overviewCalls = 0
    server.use(
      http.get('*/api/servers/:id/backups', () => {
        overviewCalls += 1
        return HttpResponse.json(overview())
      }),
    )
    const jobs = jobSequence([backupJob('backup', 'running')])
    jobs.started = true
    await openBackups()
    await screen.findByRole('region', { name: 'Backup läuft …' })
    const before = overviewCalls
    server.use(http.get('*/api/servers/:id/backups/job', () => HttpResponse.json({ job: null })))
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    await waitFor(() => expect(overviewCalls).toBeGreaterThan(before))
    expect(screen.queryByText('Backup abgeschlossen')).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Backup läuft …' })).not.toBeInTheDocument()
  })
})
```

In `web/src/features/servers/server-detail-page.test.tsx`:

- im Test „shows status, address, resources and power buttons on the overview“ die erwartete Liste um `'Backups'` nach `'Plugins'` ergänzen,
- im Test „lets the tab bar scroll sideways …“ `toHaveLength(7)` → `toHaveLength(8)`,
- zwei neue Tests anhängen:

```tsx
  it('shows the backups tab only with the backups permission', async () => {
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(screen.queryByRole('tab', { name: 'Backups' })).not.toBeInTheDocument()
  })

  it('shows the backups tab to a user with the backups permission, also on vanilla servers', async () => {
    db.grants.set(2, [{ server_id: 2, permissions: ['server.view', 'server.backups'] }])
    await renderWithProviders(<ServerDetailPage serverId={2} />, { me: loginAs('alice') })
    expect(await screen.findByRole('tab', { name: 'Backups' })).toBeInTheDocument()
  })
```

- [ ] **Step 7: Rot prüfen**

Run: `make check` → FAIL (fehlende Module `./api`, `BackupsTab`).

- [ ] **Step 8: `api.ts` und `messages.ts` implementieren**

`web/src/features/backups/api.ts`:

```ts
import { queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { isApiError, unwrap } from '@/api/errors'
import type { BackupJob, BackupJobResponse, BackupJobStarted, BackupOverview, BackupSchedule, BackupSnapshot, UpdateBackupScheduleRequest } from '@/api/types'

/** The job is polled every 2 s while it is queued or running (spec §6.1). */
export const JOB_POLL_MS = 2_000

/** Below serverKey(id): power actions and settings changes (time zone) refresh them via the serversKey prefix. */
export const overviewKey = (id: number) => ['servers', id, 'backups', 'overview'] as const
export const backupJobKey = (id: number) => ['servers', id, 'backups', 'job'] as const

export function overviewQueryOptions(id: number) {
  return queryOptions<BackupOverview>({
    queryKey: overviewKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/backups', { params: { path: { id } } })),
  })
}

export function backupJobQueryOptions(id: number) {
  return queryOptions<BackupJobResponse>({
    queryKey: backupJobKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/backups/job', { params: { path: { id } } })),
  })
}

/** A 202 always carries a job (V3 BackupJobStarted). */
function requireJob(r: BackupJobStarted): BackupJob {
  return r.job
}

/** note "" = no note: the body stays empty (V3 CreateBackupRequest.note is optional). */
export async function createBackup(id: number, note: string): Promise<BackupJob> {
  const body = note === '' ? {} : { note }
  return requireJob(await unwrap(api.POST('/servers/{id}/backups', { params: { path: { id } }, body })))
}

export async function restoreBackup(id: number, snapshot: string): Promise<BackupJob> {
  return requireJob(await unwrap(api.POST('/servers/{id}/backups/{snapshot}/restore', { params: { path: { id, snapshot } } })))
}

export async function deleteBackup(id: number, snapshot: string): Promise<BackupJob> {
  return requireJob(await unwrap(api.DELETE('/servers/{id}/backups/{snapshot}', { params: { path: { id, snapshot } } })))
}

/** restic rewrites a pinned snapshot: the answer carries the NEW id (spec §3.3). */
export async function pinBackup(id: number, snapshot: string, pinned: boolean): Promise<BackupSnapshot> {
  const r = await unwrap(api.POST('/servers/{id}/backups/{snapshot}/pin', { params: { path: { id, snapshot } }, body: { pinned } }))
  return r.snapshot
}

export async function updateBackupSchedule(id: number, body: UpdateBackupScheduleRequest): Promise<BackupSchedule> {
  const r = await unwrap(api.PUT('/servers/{id}/backups/schedule', { params: { path: { id } }, body }))
  return r.schedule
}

/** Only for ids that passed isSnapshotId; the panel audits the download. */
export function backupDownloadUrl(id: number, snapshot: string): string {
  return `/api/servers/${id}/backups/${encodeURIComponent(snapshot)}/download`
}

/** The 202 answer becomes the job query's data, which starts the 2-s polling (same pattern as plugins in 3c). */
export function startBackupJob(queryClient: QueryClient, id: number, job: BackupJob): void {
  queryClient.setQueryData<BackupJobResponse>(backupJobKey(id), { job })
}

/** 409 backup_running = a job this tab may not know about (another tab, the schedule): ask for it again. */
export function refreshJobOnConflict(queryClient: QueryClient, id: number, err: unknown): void {
  if (isApiError(err, 'backup_running')) void queryClient.invalidateQueries({ queryKey: backupJobKey(id) })
}

/** After a pin the old id is gone; a second click must use the new one at once. */
export function replaceSnapshot(queryClient: QueryClient, id: number, oldId: string, next: BackupSnapshot): void {
  queryClient.setQueryData<BackupOverview>(overviewKey(id), (prev) =>
    prev ? { ...prev, snapshots: prev.snapshots.map((s) => (s.id === oldId ? next : s)) } : prev,
  )
}

export function setSchedule(queryClient: QueryClient, id: number, schedule: BackupSchedule): void {
  queryClient.setQueryData<BackupOverview>(overviewKey(id), (prev) => (prev ? { ...prev, schedule } : prev))
}

export function refreshBackups(queryClient: QueryClient, id: number): Promise<void> {
  return queryClient.invalidateQueries({ queryKey: overviewKey(id) })
}
```

`web/src/features/backups/messages.ts`:

```ts
import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import { errorMessage } from '@/lib/error-message'

/** Backup-specific texts for the 409 answers (spec §5.8); everything else as everywhere else. */
export function backupErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'backup_running')) return t('backups.error.job_running')
  if (isApiError(err, 'conflict')) return t('backups.error.conflict')
  return errorMessage(err, t)
}

export function useBackupErrorToast(): (err: unknown) => void {
  const { t } = useTranslation()
  return useCallback(
    (err: unknown) => {
      if (isSudoCancelled(err)) return // the user dismissed the sudo dialog on purpose
      toast.error(backupErrorMessage(err, t))
    },
    [t],
  )
}
```

- [ ] **Step 9: Job-Hook und Job-Anzeige implementieren**

`web/src/features/backups/use-backup-job.ts`:

```ts
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { BackupJob } from '@/api/types'
import { serverKey } from '@/features/servers/api'
import { backupJobQueryOptions, JOB_POLL_MS, refreshBackups } from './api'
import { isJobActive, jobTitleKey } from './model'

const active = (job: BackupJob | null | undefined) => job?.state === 'queued' || job?.state === 'running'

/**
 * The server's backup job (spec §5.3/§6.1): polled every 2 s while queued or running, otherwise not at all. When a job
 * this tab saw active ends, the snapshots are reloaded (after a restore also the server state) and a toast reports the
 * result. A vanished active job (panel restart) or a new one the tab did not see start reloads without a toast.
 */
export function useBackupJob(serverId: number): { job: BackupJob | null; active: boolean } {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    ...backupJobQueryOptions(serverId),
    refetchInterval: (q) => (isJobActive(q.state.data) ? JOB_POLL_MS : false),
  })
  const data = query.data
  // undefined = no answer yet: the first answer only tells what was there before the tab looked.
  const last = useRef<BackupJob | null | undefined>(undefined)
  useEffect(() => {
    if (data === undefined) return
    const job = data.job
    const previous = last.current
    last.current = job
    if (previous === undefined) return
    const reload = (restore: boolean) => {
      void refreshBackups(queryClient, serverId)
      if (restore) void queryClient.invalidateQueries({ queryKey: serverKey(serverId), exact: true })
    }
    if (active(previous) && job !== null && job.id === previous?.id) {
      if (active(job)) return
      if (job.state === 'done') toast.success(t(jobTitleKey(job)))
      else toast.error(t(jobTitleKey(job)))
      reload(job.kind === 'restore')
      return
    }
    const vanished = active(previous) && job === null
    // A job this tab started itself (still active) reloads when it ends; any other new id may have changed the repo.
    const replaced = job !== null && job.id !== previous?.id && (active(previous) || !active(job))
    if (vanished || replaced) reload(previous?.kind === 'restore' || job?.kind === 'restore')
  }, [data, queryClient, serverId, t])
  const job = data?.job ?? null
  return { job, active: active(job) }
}
```

`web/src/features/backups/job-status.tsx`:

```tsx
import { CircleCheck, CircleX, LoaderCircle } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { BackupJob } from '@/api/types'
import { Button } from '@/components/ui/button'
import { formatBytes } from '@/features/files/format'
import { useFormat } from '@/lib/format'
import { jobErrorKey, jobTitleKey, stepKey } from './model'

export function JobStatus({ job, onDismiss }: { job: BackupJob; onDismiss(): void }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const isActive = job.state === 'queued' || job.state === 'running'
  const Icon = job.state === 'done' ? CircleCheck : job.state === 'failed' ? CircleX : LoaderCircle
  return (
    <section aria-labelledby={id} className="flex flex-col gap-2 rounded-md border p-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex gap-3">
        <Icon aria-hidden="true" className={isActive ? 'mt-0.5 size-5 animate-spin' : 'mt-0.5 size-5'} />
        <div className="space-y-1 text-sm">
          <h2 id={id} className="font-medium">
            {t(jobTitleKey(job))}
          </h2>
          {job.trigger === 'scheduled' ? <p className="text-muted-foreground">{t('backups.job.scheduled')}</p> : null}
          {job.state !== 'failed' ? <p>{t(stepKey(job.step))}</p> : null}
          {job.state === 'queued' && job.queue_position > 0 ? (
            <p className="text-muted-foreground">{t('backups.job.queue_position', { position: job.queue_position })}</p>
          ) : null}
          {isActive && job.bytes > 0 ? (
            <p className="text-muted-foreground">{t('backups.job.bytes', { bytes: formatBytes(job.bytes, fmt.locale) })}</p>
          ) : null}
          {job.state === 'failed' ? (
            <div role="alert" className="text-destructive space-y-1">
              <p>{t(jobErrorKey(job.error_code ?? 'internal'))}</p>
              {job.error !== '' ? <p className="text-muted-foreground break-words">{job.error}</p> : null}
            </div>
          ) : null}
        </div>
      </div>
      {isActive ? null : (
        <Button type="button" variant="outline" size="sm" onClick={onDismiss}>
          {t('backups.job.dismiss')}
        </Button>
      )}
    </section>
  )
}
```

- [ ] **Step 10: Snapshot-Liste, Erstellen-Dialog und Tab implementieren**

`web/src/features/backups/snapshot-list.tsx` (Menü-Einträge für Pin/Restore/Löschen kommen über optionale Props; Aufgabe 15 übergibt sie):

```tsx
import { MoreHorizontal, Pin } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { BackupSnapshot } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { formatBytes } from '@/features/files/format'
import { useFormat } from '@/lib/format'
import { backupDownloadUrl } from './api'
import { isSnapshotId, kindKey } from './model'

export interface SnapshotListProps {
  serverId: number
  snapshots: BackupSnapshot[]
  /** A job is queued or running: everything except the download is locked (the panel answers 409 anyway). */
  busy: boolean
  /** The snapshot whose pin request is in flight. */
  pendingId: string | null
  onPin?(s: BackupSnapshot, pinned: boolean): void
  onRestore?(s: BackupSnapshot): void
  onDelete?(s: BackupSnapshot): void
}

export function SnapshotList({ serverId, snapshots, busy, pendingId, onPin, onRestore, onDelete }: SnapshotListProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const columns: Column<BackupSnapshot>[] = [
    {
      id: 'time',
      header: t('backups.snapshots.col_time'),
      cell: (s) => (
        <span title={fmt.dateTime(s.time)}>
          <span className="block">{fmt.dateTime(s.time)}</span>
          <span className="text-muted-foreground block text-xs">{fmt.relative(s.time)}</span>
        </span>
      ),
    },
    {
      id: 'kind',
      header: t('backups.snapshots.col_kind'),
      cell: (s) => (
        <span className="flex flex-wrap items-center gap-1">
          <Badge variant={s.kind === 'scheduled' ? 'secondary' : 'outline'}>{t(kindKey(s.kind))}</Badge>
          {s.pinned ? (
            <Badge variant="default">
              <Pin aria-hidden="true" />
              {t('backups.snapshots.pinned')}
            </Badge>
          ) : null}
        </span>
      ),
    },
    { id: 'note', header: t('backups.snapshots.col_note'), cell: (s) => <span className="break-words">{s.note ?? ''}</span> },
    { id: 'size', header: t('backups.snapshots.col_size'), cell: (s) => formatBytes(s.bytes, fmt.locale), className: 'whitespace-nowrap' },
    {
      id: 'actions',
      header: t('backups.snapshots.col_actions'),
      className: 'text-right',
      cell: (s) => (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="ghost" size="icon-sm" aria-label={t('backups.snapshots.actions_label', { when: fmt.dateTime(s.time) })}>
              <MoreHorizontal aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {onPin && s.kind === 'scheduled' ? (
              <DropdownMenuItem disabled={busy || pendingId === s.id} onSelect={() => onPin(s, !s.pinned)}>
                {s.pinned ? t('backups.snapshots.unpin') : t('backups.snapshots.pin')}
              </DropdownMenuItem>
            ) : null}
            {isSnapshotId(s.id) ? (
              <DropdownMenuItem asChild>
                <a href={backupDownloadUrl(serverId, s.id)} download>
                  {t('backups.snapshots.download')}
                </a>
              </DropdownMenuItem>
            ) : null}
            {onRestore ? (
              <DropdownMenuItem disabled={busy} onSelect={() => onRestore(s)}>
                {t('backups.snapshots.restore')}
              </DropdownMenuItem>
            ) : null}
            {onDelete ? (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" disabled={busy} onSelect={() => onDelete(s)}>
                  {t('backups.snapshots.delete')}
                </DropdownMenuItem>
              </>
            ) : null}
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ]
  return (
    <DataList items={snapshots} columns={columns} rowKey={(s) => s.id} caption={t('backups.snapshots.caption')} empty={t('backups.snapshots.empty')} />
  )
}
```

`web/src/features/backups/create-dialog.tsx`:

```tsx
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { FieldRow } from '@/features/servers/field-row'
import { normalizeNote, validateNote } from './model'

export function CreateBackupDialog({ pending, onClose, onSubmit }: { pending: boolean; onClose(): void; onSubmit(note: string): void }) {
  const { t } = useTranslation()
  const id = useId()
  const [text, setText] = useState('')
  const note = normalizeNote(text)
  const error = validateNote(note)
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('backups.create.title')}</DialogTitle>
          <DialogDescription>{t('backups.create.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (error === null) onSubmit(note)
          }}
        >
          <FieldRow id={`${id}-note`} label={t('backups.create.note_label')} hint={t('backups.create.note_hint')} error={error ?? undefined}>
            {(c) => <Input {...c} value={text} autoComplete="off" onChange={(e) => setText(e.target.value)} />}
          </FieldRow>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={error !== null || pending}>
              {t('backups.create.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

`web/src/features/backups/backups-tab.tsx` (Stand nach Aufgabe 14; Aufgabe 15 ergänzt Pin/Restore/Löschen, Aufgabe 16 die Zeitplan-Karte):

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { createBackup, overviewQueryOptions, refreshJobOnConflict, startBackupJob } from './api'
import { CreateBackupDialog } from './create-dialog'
import { JobStatus } from './job-status'
import { useBackupErrorToast } from './messages'
import { SnapshotList } from './snapshot-list'
import { useBackupJob } from './use-backup-job'

export function BackupsTab({ server }: { server: Server }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyToast = useBackupErrorToast()
  const notifyError = (err: unknown) => {
    refreshJobOnConflict(queryClient, server.id, err)
    notifyToast(err)
  }
  const overview = useQuery(overviewQueryOptions(server.id))
  const { job, active } = useBackupJob(server.id)
  const [creating, setCreating] = useState(false)
  const [dismissed, setDismissed] = useState<string | null>(null)
  // The panel keeps the last job result (spec §5.3); one that ended before the tab was opened is not shown again.
  const [openedAt] = useState(() => Date.now())
  // A job this tab saw active stays visible when it ends, independent of the panel's clock.
  const [watched, setWatched] = useState<string | null>(null)
  if (active && job !== null && job.id !== watched) setWatched(job.id)

  const create = useMutation({
    mutationFn: (note: string) => createBackup(server.id, note),
    onSuccess: (started) => {
      startBackupJob(queryClient, server.id, started)
      setCreating(false)
      toast.success(t('backups.create.started'))
    },
    onError: (err) => {
      setCreating(false)
      notifyError(err)
    },
  })

  const busy = active || create.isPending
  const visibleJob = job !== null && job.id !== dismissed && (active || job.id === watched || job.finished_at >= openedAt) ? job : null

  if (overview.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  if (overview.isError) {
    return (
      <p role="alert" className="text-destructive text-sm">
        {errorMessage(overview.error, t)}
      </p>
    )
  }
  const data = overview.data
  if (!data.available) {
    return (
      <p role="status" className="rounded-md border p-4 text-sm">
        {t('backups.unavailable')}
      </p>
    )
  }

  return (
    <div className="space-y-4">
      {visibleJob ? <JobStatus job={visibleJob} onDismiss={() => setDismissed(visibleJob.id)} /> : null}
      <SectionCard
        title={t('backups.snapshots.title')}
        actions={
          <Button type="button" size="sm" disabled={busy} onClick={() => setCreating(true)}>
            {t('backups.create.button')}
          </Button>
        }
      >
        <SnapshotList serverId={server.id} snapshots={data.snapshots} busy={busy} pendingId={null} />
      </SectionCard>
      {creating ? <CreateBackupDialog pending={create.isPending} onClose={() => setCreating(false)} onSubmit={(note) => create.mutate(note)} /> : null}
    </div>
  )
}
```

- [ ] **Step 11: Tab in die Detailseite einhängen**

In `web/src/features/servers/server-detail-page.tsx`:

```tsx
import { BackupsTab } from '@/features/backups/backups-tab'
// …
type DetailTab = 'overview' | 'console' | 'players' | 'files' | 'plugins' | 'backups' | 'settings' | 'delete'
// … nach pluginsKind:
  // Spec §6.1: every instance type has backups; the tab needs server.backups.
  const canBackups = server.permissions.includes('server.backups')
// … in available nach plugins:
  if (canBackups) available.push('backups')
// … TabsList nach dem Plugins-Trigger:
          {canBackups ? <TabsTrigger value="backups">{t('servers.detail.tabs.backups')}</TabsTrigger> : null}
// … nach dem Plugins-TabsContent:
        {canBackups ? (
          <TabsContent value="backups" className="pt-4">
            <BackupsTab server={server} />
          </TabsContent>
        ) : null}
```

Den Kommentar über der `TabsList` auf „Up to eight tabs …“ ändern.

- [ ] **Step 12: Grün prüfen**

Run: `make check` → PASS; `make test-vm SUITE=web` → `model.test.ts`, `backups-tab.test.tsx`, `server-detail-page.test.tsx`, `i18n.test.ts` PASS.

- [ ] **Step 13: Commit**

```bash
git add web/src/features/backups web/src/features/servers/server-detail-page.tsx web/src/features/servers/server-detail-page.test.tsx web/src/i18n
git commit -m "feat(web): backups tab with snapshot list, create dialog and job status"
```

---

### Task 15: Snapshot-Aktionen (Pin, Download, Restore, Löschen)

**Files:**
- Create: `web/src/features/backups/restore-dialog.tsx`, `web/src/features/backups/backups-actions.test.tsx`
- Modify: `web/src/features/backups/backups-tab.tsx`

**Interfaces:**
- Consumes: aus Aufgabe 14 `SnapshotList` (Props `onPin`, `onRestore`, `onDelete`, `pendingId`), `pinBackup`, `restoreBackup`, `deleteBackup`, `replaceSnapshot`, `startBackupJob`, `refreshBackups`, `refreshJobOnConflict`, `useBackupErrorToast`, `backupErrorMessage`, Fixtures aus `test-utils.tsx`; `ConfirmDialog` (`@/components/confirm-dialog`), `isSudoCancelled`.
- Produces: `RestoreDialog({ serverName, when, pending, onClose, onConfirm })`; `BackupsTab` mit allen Aktionen.

- [ ] **Step 1: Tests schreiben (rot)**

`web/src/features/backups/backups-actions.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server } from '@/mocks/node'
import { backupJob, mockOverview, openBackups, overview, snapshot, snapshotId } from './test-utils'

async function openMenu(user: Awaited<ReturnType<typeof openBackups>>['user'], index = 0) {
  const triggers = await screen.findAllByRole('button', { name: /^Aktionen für den Snapshot vom / })
  await user.click(triggers[index])
}

describe('snapshot actions', () => {
  it('offers pinning only for scheduled snapshots and replaces the id with the answer', async () => {
    mockOverview(overview({ snapshots: [snapshot(1), snapshot(2, { kind: 'manual' })] }))
    const pinned: string[] = []
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/pin', async ({ params, request }) => {
        pinned.push(String(params.snapshot))
        const body = (await request.json()) as { pinned: boolean }
        return HttpResponse.json({ snapshot: snapshot(1, { id: snapshotId(99), pinned: body.pinned }) })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user, 1)
    expect(screen.queryByRole('menuitem', { name: 'Anpinnen' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')

    await openMenu(user, 0)
    await user.click(await screen.findByRole('menuitem', { name: 'Anpinnen' }))
    expect(await screen.findByText('Snapshot angepinnt.')).toBeInTheDocument()
    expect(await screen.findByText('Angepinnt')).toBeInTheDocument()
    await openMenu(user, 0)
    await user.click(await screen.findByRole('menuitem', { name: 'Lösen' }))
    await waitFor(() => expect(pinned).toEqual([snapshotId(1), snapshotId(99)]))
  })

  it('links the download to the API', async () => {
    mockOverview(overview({ snapshots: [snapshot(7)] }))
    const { user } = await openBackups()
    await openMenu(user)
    const link = await screen.findByRole('menuitem', { name: 'Als Zip herunterladen' })
    expect(link).toHaveAttribute('href', `/api/servers/1/backups/${snapshotId(7)}/download`)
    expect(link).toHaveAttribute('download')
  })

  it('restores only after the server name was typed and starts the job', async () => {
    mockOverview(overview({ snapshots: [snapshot(3)] }))
    let restored: string | null = null
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/restore', ({ params }) => {
        restored = String(params.snapshot)
        return HttpResponse.json({ job: backupJob('restore', 'running', { step: 'stopping', snapshot: String(params.snapshot) }) }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Wiederherstellen' }))
    const dialog = await screen.findByRole('dialog', { name: /wiederherstellen\?$/ })
    expect(within(dialog).getByText(/sichert den aktuellen Stand als Snapshot „Vor Restore“/)).toBeInTheDocument()
    const submit = within(dialog).getByRole('button', { name: 'Wiederherstellen' })
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'surviva')
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'l')
    await user.click(submit)
    await waitFor(() => expect(restored).toBe(snapshotId(3)))
    expect(await screen.findByText('Wiederherstellung gestartet.')).toBeInTheDocument()
    const progress = await screen.findByRole('region', { name: 'Wiederherstellung läuft …' })
    expect(within(progress).getByText('Server wird gestoppt …')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Backup erstellen' })).toBeDisabled()
  })

  it('asks for sudo before a restore and shows nothing when it is cancelled', async () => {
    mockOverview(overview({ snapshots: [snapshot(3)] }))
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/restore', () =>
        HttpResponse.json({ error: 'sudo_required', message: 're-authentication required' }, { status: 403 }),
      ),
    )
    const { user } = await openBackups(1, 'admin', { sudo: false })
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Wiederherstellen' }))
    const dialog = await screen.findByRole('dialog', { name: /wiederherstellen\?$/ })
    await user.type(within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    await user.click(within(dialog).getByRole('button', { name: 'Wiederherstellen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
  })

  it('deletes a snapshot after confirmation', async () => {
    mockOverview(overview({ snapshots: [snapshot(4, { kind: 'pre-restore' })] }))
    let deleted: string | null = null
    server.use(
      http.delete('*/api/servers/:id/backups/:snapshot', ({ params }) => {
        deleted = String(params.snapshot)
        return HttpResponse.json({ job: backupJob('delete', 'running', { step: 'deleting' }) }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Löschen' }))
    const dialog = await screen.findByRole('dialog', { name: /löschen\?$/ })
    await user.click(within(dialog).getByRole('button', { name: 'Snapshot löschen' }))
    await waitFor(() => expect(deleted).toBe(snapshotId(4)))
    expect(await screen.findByRole('region', { name: 'Snapshot wird gelöscht …' })).toBeInTheDocument()
  })

  it('locks restore, delete and pin while a job runs, but not the download', async () => {
    mockOverview(overview({ snapshots: [snapshot(1)] }))
    server.use(http.get('*/api/servers/:id/backups/job', () => HttpResponse.json({ job: backupJob('backup', 'running') })))
    const { user } = await openBackups()
    await screen.findByRole('region', { name: 'Backup läuft …' })
    await openMenu(user)
    expect(await screen.findByRole('menuitem', { name: 'Wiederherstellen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Löschen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Anpinnen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Als Zip herunterladen' })).not.toHaveAttribute('data-disabled')
  })

  it('reloads the list when a pin hits a snapshot that is gone', async () => {
    let calls = 0
    server.use(
      http.get('*/api/servers/:id/backups', () => {
        calls += 1
        return HttpResponse.json(overview({ snapshots: [snapshot(1)] }))
      }),
      http.post('*/api/servers/:id/backups/:snapshot/pin', () => HttpResponse.json({ error: 'not_found', message: 'gone' }, { status: 404 })),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Anpinnen' }))
    await waitFor(() => expect(calls).toBeGreaterThan(1))
  })
})
```

- [ ] **Step 2: Rot prüfen**

Run: `make check` (PASS, Tests kompilieren), `make test-vm SUITE=web` → `backups-actions.test.tsx` FAIL (keine Menüpunkte Anpinnen/Wiederherstellen/Löschen).

- [ ] **Step 3: Restore-Dialog implementieren**

`web/src/features/backups/restore-dialog.tsx`:

```tsx
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export interface RestoreDialogProps {
  serverName: string
  /** Formatted snapshot time for the title. */
  when: string
  pending: boolean
  onClose(): void
  onConfirm(): void
}

/** A restore replaces the whole data directory (spec §5.5): the server name must be typed, like when deleting a server. */
export function RestoreDialog({ serverName, when, pending, onClose, onConfirm }: RestoreDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const [typed, setTyped] = useState('')
  const matches = typed.trim() === serverName
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('backups.restore.title', { when })}</DialogTitle>
          <DialogDescription>{t('backups.restore.description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (matches) onConfirm()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-confirm`}>{t('backups.restore.confirm_label', { name: serverName })}</Label>
            <Input
              id={`${id}-confirm`}
              value={typed}
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setTyped(e.target.value)}
            />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="destructive" disabled={!matches || pending}>
              {t('backups.restore.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 4: Aktionen im Tab verdrahten**

In `web/src/features/backups/backups-tab.tsx`:

```tsx
// zusätzliche Imports
import type { BackupSnapshot } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { isApiError } from '@/api/errors'
import { useFormat } from '@/lib/format'
import { deleteBackup, pinBackup, refreshBackups, replaceSnapshot, restoreBackup } from './api'
import { RestoreDialog } from './restore-dialog'

// im Komponentenrumpf nach `create`
  const fmt = useFormat()
  const [restoring, setRestoring] = useState<BackupSnapshot | null>(null)
  const [deleting, setDeleting] = useState<BackupSnapshot | null>(null)

  const pin = useMutation({
    mutationFn: ({ s, pinned }: { s: BackupSnapshot; pinned: boolean }) => pinBackup(server.id, s.id, pinned),
    onSuccess: (next, { s, pinned }) => {
      replaceSnapshot(queryClient, server.id, s.id, next)
      toast.success(pinned ? t('backups.snapshots.pinned_done') : t('backups.snapshots.unpinned_done'))
    },
    onError: (err) => {
      // not_found: the snapshot changed meanwhile (another tab pinned it, retention removed it) – the repo is the truth.
      if (isApiError(err, 'not_found')) void refreshBackups(queryClient, server.id)
      notifyError(err)
    },
  })

  const restore = useMutation({
    mutationFn: (s: BackupSnapshot) => restoreBackup(server.id, s.id),
    onSuccess: (started) => {
      startBackupJob(queryClient, server.id, started)
      setRestoring(null)
      toast.success(t('backups.restore.started'))
    },
    onError: (err) => {
      setRestoring(null)
      notifyError(err)
    },
  })

  const remove = useMutation({
    mutationFn: (s: BackupSnapshot) => deleteBackup(server.id, s.id),
    onSuccess: (started) => {
      startBackupJob(queryClient, server.id, started)
      setDeleting(null)
      toast.success(t('backups.delete.started'))
    },
    onError: (err) => {
      setDeleting(null)
      notifyError(err)
    },
  })

// busy erweitern
  const busy = active || create.isPending || restore.isPending || remove.isPending

// SnapshotList-Aufruf ersetzen
        <SnapshotList
          serverId={server.id}
          snapshots={data.snapshots}
          busy={busy}
          pendingId={pin.isPending ? (pin.variables?.s.id ?? null) : null}
          onPin={(s, pinned) => pin.mutate({ s, pinned })}
          onRestore={setRestoring}
          onDelete={setDeleting}
        />

// nach dem CreateBackupDialog
      {restoring ? (
        <RestoreDialog
          serverName={server.name}
          when={fmt.dateTime(restoring.time)}
          pending={restore.isPending}
          onClose={() => setRestoring(null)}
          onConfirm={() => restore.mutate(restoring)}
        />
      ) : null}
      {deleting ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setDeleting(null))}
          title={t('backups.delete.title', { when: fmt.dateTime(deleting.time) })}
          description={t('backups.delete.description')}
          confirmLabel={t('backups.delete.submit')}
          destructive
          pending={remove.isPending || busy}
          onConfirm={() => remove.mutate(deleting)}
        />
      ) : null}
```

`useFormat()` steht wie die übrigen Hooks **vor** den frühen `return`s.

- [ ] **Step 5: Grün prüfen**

Run: `make check` → PASS; `make test-vm SUITE=web` → `backups-actions.test.tsx`, `backups-tab.test.tsx` PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/backups
git commit -m "feat(web): pin, download, restore and delete backup snapshots"
```

---

### Task 16: Zeitplan-Karte, Zeitzone, Lösch-Checkbox, Rechte-Hinweis

**Files:**
- Create: `web/src/features/backups/schedule-card.tsx`, `web/src/features/backups/schedule-card.test.tsx`
- Create: `web/src/features/settings/time-zones.ts`, `web/src/features/settings/time-zones.test.ts`, `web/src/features/settings/backup-timezone-card.tsx`
- Modify: `web/src/features/backups/backups-tab.tsx`
- Modify: `web/src/features/settings/settings-page.tsx`, `web/src/features/settings/settings-page.test.tsx`
- Modify: `web/src/features/servers/api.ts`, `web/src/features/servers/delete-server-section.tsx`, `web/src/features/servers/server-detail-page.test.tsx`
- Modify: `web/src/features/users/permissions-page.tsx`, `web/src/features/users/permissions-page.test.tsx`

**Interfaces:**
- Consumes: aus Aufgabe 14 `draftFromSchedule`, `validateSchedule`, `scheduleRequest`, `sameSchedule`, `SCHEDULE_MODES`, `WEEKDAYS`, `KEEP_FIELDS`, `updateBackupSchedule`, `setSchedule`, Fixtures; `FieldRow`, `NativeSelect` (`@/components/native-select`), `SettingsCard`, `updateServerSettings`, `serverSettingsKey`, `serversKey`, `newlyGranted`.
- Produces: `ScheduleCard({ serverId, schedule })`; `timeZoneOptions(current, supported?)`; `BackupTimezoneCard({ settings })`; `deleteServer(id, purgeBackups?)`.

- [ ] **Step 1: Tests schreiben (rot)**

`web/src/features/backups/schedule-card.test.tsx`:

```tsx
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { UpdateBackupScheduleRequest } from '@/api/types'
import { server } from '@/mocks/node'
import { mockOverview, openBackups, overview, schedule } from './test-utils'

async function openSchedule(o = overview()) {
  mockOverview(o)
  const opened = await openBackups()
  const card = await screen.findByRole('region', { name: 'Zeitplan' })
  return { ...opened, card }
}

describe('schedule card', () => {
  it('shows the fields of the chosen mode, the time zone and the next run', async () => {
    const { user, card } = await openSchedule()
    const mode = within(card).getByRole('combobox', { name: 'Zeitplan' })
    expect(mode).toHaveValue('daily')
    expect(within(card).getByLabelText('Uhrzeit')).toHaveValue('04:00')
    expect(within(card).queryByLabelText('Wochentag')).not.toBeInTheDocument()
    expect(within(card).queryByLabelText('Intervall in Stunden')).not.toBeInTheDocument()
    expect(within(card).getByText(/Zeitzone des Panels: Europe\/Berlin/)).toBeInTheDocument()
    expect(within(card).getByText(/^Nächster Lauf: /)).toBeInTheDocument()
    expect(within(card).getByText(/^Letzter geplanter Lauf: .* – erfolgreich$/)).toBeInTheDocument()

    await user.selectOptions(mode, 'weekly')
    const weekday = within(card).getByRole('combobox', { name: 'Wochentag' })
    expect(within(weekday).getAllByRole('option').map((o) => o.textContent)).toEqual([
      'Montag',
      'Dienstag',
      'Mittwoch',
      'Donnerstag',
      'Freitag',
      'Samstag',
      'Sonntag',
    ])
    await user.selectOptions(mode, 'interval')
    expect(within(card).getByLabelText('Intervall in Stunden')).toHaveValue('24')
    expect(within(card).queryByLabelText('Uhrzeit')).not.toBeInTheDocument()
  })

  it('saves a weekly schedule and keeps the hidden interval', async () => {
    let body: UpdateBackupScheduleRequest | null = null
    server.use(
      http.put('*/api/servers/:id/backups/schedule', async ({ request }) => {
        body = (await request.json()) as UpdateBackupScheduleRequest
        return HttpResponse.json({ schedule: schedule({ ...body, next_run_at: Date.now() + 3_600_000 }) })
      }),
    )
    const { user, card } = await openSchedule()
    const save = within(card).getByRole('button', { name: 'Zeitplan speichern' })
    expect(save).toBeDisabled()
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Zeitplan' }), 'weekly')
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Wochentag' }), 'Samstag')
    // jsdom sanitizes partial values of <input type="time">; set the complete value at once.
    fireEvent.change(within(card).getByLabelText('Uhrzeit'), { target: { value: '03:15' } })
    await user.clear(within(card).getByLabelText('Monate behalten'))
    await user.type(within(card).getByLabelText('Monate behalten'), '12')
    await user.click(save)
    await waitFor(() =>
      expect(body).toEqual({
        mode: 'weekly',
        interval_hours: 24,
        time_of_day: 195,
        weekday: 6,
        keep_last: 24,
        keep_daily: 7,
        keep_weekly: 4,
        keep_monthly: 12,
      }),
    )
    expect(await screen.findByText('Zeitplan gespeichert.')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeDisabled()
  })

  it('blocks saving invalid values and explains why', async () => {
    const { user, card } = await openSchedule()
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Zeitplan' }), 'interval')
    const interval = within(card).getByLabelText('Intervall in Stunden')
    await user.clear(interval)
    await user.type(interval, '200')
    expect(within(card).getByText('Erlaubt sind ganze Zahlen von 1 bis 168.')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeDisabled()
    await user.clear(interval)
    await user.type(interval, '6')
    for (const label of ['Letzte behalten', 'Tage behalten', 'Wochen behalten', 'Monate behalten']) {
      await user.clear(within(card).getByLabelText(label))
      await user.type(within(card).getByLabelText(label), '0')
    }
    expect(within(card).getByText('Mindestens ein Aufbewahrungswert muss größer als 0 sein.')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeDisabled()
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Zeitplan' }), 'off')
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeEnabled()
  })

  it('shows the error of the last failed run and no next run while off', async () => {
    const { card } = await openSchedule(
      overview({ schedule: schedule({ mode: 'off', next_run_at: null, last_status: 'failed', last_error: 'RCON nicht erreichbar' }) }),
    )
    expect(within(card).getByText(/– fehlgeschlagen$/)).toBeInTheDocument()
    expect(within(card).getByText('RCON nicht erreichbar')).toBeInTheDocument()
    expect(within(card).queryByText(/^Nächster Lauf/)).not.toBeInTheDocument()
  })
})
```

`web/src/features/settings/time-zones.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { timeZoneOptions } from './time-zones'

describe('timeZoneOptions', () => {
  it('puts the current value and UTC first, without duplicates', () => {
    expect(timeZoneOptions('Europe/Berlin', ['Africa/Abidjan', 'Europe/Berlin', 'UTC'])).toEqual(['Europe/Berlin', 'UTC', 'Africa/Abidjan'])
  })

  it('keeps an unknown stored value selectable', () => {
    expect(timeZoneOptions('Etc/GMT+1', ['Europe/Berlin'])).toEqual(['Etc/GMT+1', 'UTC', 'Europe/Berlin'])
  })

  it('falls back to current value and UTC without Intl support', () => {
    expect(timeZoneOptions('Europe/Berlin', [])).toEqual(['Europe/Berlin', 'UTC'])
  })
})
```

In `web/src/features/settings/settings-page.test.tsx` anhängen (Aufbau wie die vorhandenen Tests der Datei; `loginAs('admin', { sudo: true })`):

```tsx
  it('saves the time zone of the backup schedules', async () => {
    const user = userEvent.setup()
    let sent: unknown = null
    server.use(
      http.put('*/api/servers/settings', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json({ ...db.serverSettings, backup_timezone: 'UTC' })
      }),
    )
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
    const card = await screen.findByRole('region', { name: 'Zeitzone für Backups' })
    const select = within(card).getByRole('combobox', { name: 'Zeitzone' })
    expect(select).toHaveValue('Europe/Berlin')
    const save = within(card).getByRole('button', { name: 'Zeitzone speichern' })
    expect(save).toBeDisabled()
    await user.selectOptions(select, 'UTC')
    await user.click(save)
    await waitFor(() => expect(sent).toEqual({ backup_timezone: 'UTC' }))
    expect(await screen.findByText('Zeitzone gespeichert.')).toBeInTheDocument()
  })
```

Den genauen Namen des Mock-Einstellungsobjekts (`db.serverSettings` oder wie Phase A es nennt) aus `web/src/mocks/db.ts` übernehmen; fehlende Imports (`http`, `HttpResponse`, `server`, `within`, `waitFor`, `userEvent`, `db`) ergänzen.

In `web/src/features/servers/server-detail-page.test.tsx` anhängen:

```tsx
  it('deletes the backups too when the checkbox is set', async () => {
    const user = userEvent.setup()
    let query: string | null = null
    server.use(
      http.delete('*/api/servers/:id', ({ request }) => {
        query = new URL(request.url).search
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await renderWithProviders(<ServerDetailPage serverId={1} />, { path: '/servers/1', me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('tab', { name: 'Löschen' }))
    expect(screen.getByText('Ohne diese Option bleiben die Backups des Servers auf dem Host liegen und belegen weiter Platz.')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'Backups ebenfalls löschen' }))
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    await waitFor(() => expect(query).toBe('?purge_backups=true'))
  })

  it('offers no backup checkbox for a server without instance and sends no parameter by default', async () => {
    const user = userEvent.setup()
    let query: string | null = null
    server.use(
      http.delete('*/api/servers/:id', ({ request }) => {
        query = new URL(request.url).search
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await renderWithProviders(<ServerDetailPage serverId={3} />, { path: '/servers/3', me: loginAs('admin', { sudo: true }) })
    await screen.findByLabelText('Zum Bestätigen den Namen „lobby“ eintippen')
    expect(screen.queryByRole('checkbox', { name: 'Backups ebenfalls löschen' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    await waitFor(() => expect(query).toBe(''))
  })
```

(Imports `http`, `HttpResponse` aus `msw` und `server` aus `@/mocks/node` ergänzen, falls die Datei sie noch nicht hat.)

In `web/src/features/users/permissions-page.test.tsx` anhängen:

```tsx
  it('warns before granting backup access', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UserPermissionsPage userId={2} />, { me: loginAs('admin', { sudo: true }) })
    await screen.findByRole('heading', { name: 'Rechte von alice' })
    expect(screen.queryByText(/„Backups“ erlaubt/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'survival: Backups' }))
    expect(screen.getByText(/„Backups“ erlaubt, alle Dateien des Servers herunterzuladen/)).toBeInTheDocument()
    expect(screen.queryByText(/„Dateien“ erlaubt/)).not.toBeInTheDocument()
  })
```

- [ ] **Step 2: Rot prüfen**

Run: `make check` → FAIL (`./time-zones` fehlt). Nach Anlegen leerer Exporte: `make test-vm SUITE=web` → die neuen Tests FAIL.

- [ ] **Step 3: Zeitplan-Karte implementieren**

`web/src/features/backups/schedule-card.tsx`:

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { BackupSchedule, BackupScheduleMode } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { SectionCard } from '@/features/profile/section-card'
import { FieldRow } from '@/features/servers/field-row'
import { useFormat } from '@/lib/format'
import { setSchedule, updateBackupSchedule } from './api'
import { useBackupErrorToast } from './messages'
import {
  draftFromSchedule,
  INTERVAL_MAX_HOURS,
  INTERVAL_MIN_HOURS,
  KEEP_FIELDS,
  KEEP_MAX,
  sameSchedule,
  SCHEDULE_MODES,
  scheduleRequest,
  validateSchedule,
  WEEKDAYS,
  type ScheduleDraft,
} from './model'

/** Keyed by the stored schedule: after a save (or a refetch with other values) the form starts from the new state. */
export function ScheduleCard({ serverId, schedule }: { serverId: number; schedule: BackupSchedule }) {
  const key = JSON.stringify([schedule.mode, schedule.interval_hours, schedule.time_of_day, schedule.weekday, ...KEEP_FIELDS.map((f) => schedule[f])])
  return <ScheduleForm key={key} serverId={serverId} schedule={schedule} />
}

function ScheduleForm({ serverId, schedule }: { serverId: number; schedule: BackupSchedule }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useBackupErrorToast()
  const [draft, setDraft] = useState<ScheduleDraft>(() => draftFromSchedule(schedule))
  const errors = validateSchedule(draft)
  const request = scheduleRequest(draft, schedule)
  const unchanged = request !== null && sameSchedule(request, schedule)
  const set = (patch: Partial<ScheduleDraft>) => setDraft((d) => ({ ...d, ...patch }))

  const save = useMutation({
    mutationFn: () => {
      if (request === null) throw new Error('backups: invalid schedule')
      return updateBackupSchedule(serverId, request)
    },
    onSuccess: (next) => {
      setSchedule(queryClient, serverId, next)
      toast.success(t('backups.schedule.saved'))
    },
    onError: notifyError,
  })

  return (
    <SectionCard title={t('backups.schedule.title')} description={t('backups.schedule.description')}>
      <form
        className="space-y-4 text-sm"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (request !== null && !unchanged) save.mutate()
        }}
      >
        <div className="grid gap-4 sm:grid-cols-3">
          <FieldRow id={`${id}-mode`} label={t('backups.schedule.mode_label')}>
            {(c) => (
              <NativeSelect {...c} value={draft.mode} onChange={(e) => set({ mode: e.target.value as BackupScheduleMode })}>
                {SCHEDULE_MODES.map((m) => (
                  <option key={m} value={m}>
                    {t(`backups.schedule.mode.${m}`)}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FieldRow>
          {draft.mode === 'interval' ? (
            <FieldRow id={`${id}-interval`} label={t('backups.schedule.interval_label')} error={errors.interval_hours}>
              {(c) => (
                <Input
                  {...c}
                  type="number"
                  inputMode="numeric"
                  min={INTERVAL_MIN_HOURS}
                  max={INTERVAL_MAX_HOURS}
                  value={draft.interval_hours}
                  onChange={(e) => set({ interval_hours: e.target.value })}
                />
              )}
            </FieldRow>
          ) : null}
          {draft.mode === 'weekly' ? (
            <FieldRow id={`${id}-weekday`} label={t('backups.schedule.weekday_label')}>
              {(c) => (
                <NativeSelect {...c} value={String(draft.weekday)} onChange={(e) => set({ weekday: Number(e.target.value) })}>
                  {WEEKDAYS.map((d) => (
                    <option key={d} value={String(d)}>
                      {t(`backups.schedule.weekday.${d}`)}
                    </option>
                  ))}
                </NativeSelect>
              )}
            </FieldRow>
          ) : null}
          {draft.mode === 'daily' || draft.mode === 'weekly' ? (
            <FieldRow id={`${id}-time`} label={t('backups.schedule.time_label')} error={errors.time}>
              {(c) => <Input {...c} type="time" value={draft.time} onChange={(e) => set({ time: e.target.value })} />}
            </FieldRow>
          ) : null}
        </div>

        <fieldset className="space-y-2">
          <legend className="font-medium">{t('backups.schedule.retention_title')}</legend>
          <p className="text-muted-foreground">{t('backups.schedule.retention_hint')}</p>
          <div className="grid gap-4 sm:grid-cols-4">
            {KEEP_FIELDS.map((f) => (
              <FieldRow key={f} id={`${id}-${f}`} label={t(`backups.schedule.${f}`)} error={errors[f]}>
                {(c) => (
                  <Input {...c} type="number" inputMode="numeric" min={0} max={KEEP_MAX} value={draft[f]} onChange={(e) => set({ [f]: e.target.value })} />
                )}
              </FieldRow>
            ))}
          </div>
          {errors.keep ? (
            <p role="alert" className="text-destructive">
              {t(errors.keep.key)}
            </p>
          ) : null}
        </fieldset>

        <div className="text-muted-foreground space-y-1">
          <p>{t('backups.schedule.timezone_hint', { timezone: schedule.timezone })}</p>
          {schedule.mode !== 'off' && schedule.next_run_at !== null ? (
            <p>{t('backups.schedule.next_run', { when: fmt.dateTime(schedule.next_run_at), relative: fmt.relative(schedule.next_run_at) })}</p>
          ) : null}
          {schedule.last_run_at !== null && schedule.last_status !== null ? (
            <p>
              {t('backups.schedule.last_run', {
                when: fmt.dateTime(schedule.last_run_at),
                status: t(`backups.schedule.last_status.${schedule.last_status}`),
              })}
            </p>
          ) : null}
          {schedule.last_status === 'failed' && schedule.last_error ? <p className="text-destructive break-words">{schedule.last_error}</p> : null}
        </div>

        <Button type="submit" disabled={request === null || unchanged || save.isPending}>
          {t('backups.schedule.save')}
        </Button>
      </form>
    </SectionCard>
  )
}
```

Hinweis für den Test „shows the error of the last failed run …“: `last_run_at` ist in der Fixture gesetzt (`schedule()`), also erscheint die Zeile „Letzter geplanter Lauf: … – fehlgeschlagen“.

In `web/src/features/backups/backups-tab.tsx` nach der `SectionCard` der Snapshots:

```tsx
import { ScheduleCard } from './schedule-card'
// …
      <ScheduleCard serverId={server.id} schedule={data.schedule} />
```

- [ ] **Step 4: Zeitzone in den Einstellungen**

`web/src/features/settings/time-zones.ts`:

```ts
/** IANA names the browser knows; V8 leaves out "UTC", old browsers have no Intl.supportedValuesOf at all. */
function supportedTimeZones(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }
  try {
    return intl.supportedValuesOf?.('timeZone') ?? []
  } catch {
    return []
  }
}

/** The stored value first (also an unknown one stays selectable), then UTC, then the browser's list. */
export function timeZoneOptions(current: string, supported: string[] = supportedTimeZones()): string[] {
  return [...new Set([current, 'UTC', ...supported])]
}
```

`web/src/features/settings/backup-timezone-card.tsx`:

```tsx
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerSettings } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { serverSettingsKey, serversKey, updateServerSettings } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { useErrorToast } from '@/lib/error-message'
import { SettingsCard } from './settings-card'
import { timeZoneOptions } from './time-zones'

export function BackupTimezoneCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [draft, setDraft] = useState<string | null>(null)
  const value = draft ?? settings.backup_timezone
  const options = useMemo(() => timeZoneOptions(settings.backup_timezone), [settings.backup_timezone])

  const save = useMutation({
    mutationFn: (zone: string) => updateServerSettings({ backup_timezone: zone }),
    onSuccess: (result) => {
      queryClient.setQueryData(serverSettingsKey, result)
      // next_run_at and the hint of every schedule change with the zone; the backup queries sit below serversKey.
      void queryClient.invalidateQueries({ queryKey: serversKey })
      setDraft(null)
      toast.success(t('settings.backup_timezone.saved'))
    },
    onError: notifyError,
  })

  return (
    <SettingsCard title={t('settings.backup_timezone.title')} description={t('settings.backup_timezone.description')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (value !== settings.backup_timezone) save.mutate(value)
        }}
      >
        <FieldRow id={`${id}-zone`} label={t('settings.backup_timezone.label')}>
          {(c) => (
            <NativeSelect {...c} className="w-full sm:w-80" value={value} onChange={(e) => setDraft(e.target.value)}>
              {options.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        <Button type="submit" disabled={value === settings.backup_timezone || save.isPending}>
          {t('settings.backup_timezone.save')}
        </Button>
      </form>
    </SettingsCard>
  )
}
```

In `web/src/features/settings/settings-page.tsx` im Grid nach `<CurseForgeCard …/>`: `<BackupTimezoneCard settings={settings.data} />` (Import ergänzen).

- [ ] **Step 5: Checkbox beim Server-Löschen**

`web/src/features/servers/api.ts`:

```ts
/** purgeBackups also removes the server's restic repository (backups spec §5.6); the parameter is only sent when true. */
export function deleteServer(id: number, purgeBackups = false): Promise<void> {
  return unwrapVoid(
    api.DELETE('/servers/{id}', { params: { path: { id }, query: purgeBackups ? { purge_backups: true } : undefined } }),
  )
}
```

`web/src/features/servers/delete-server-section.tsx`: State `const [purge, setPurge] = useState(false)`, `mutationFn: () => deleteServer(server.id, purge)`, und vor dem Submit-Button (nur bei `server.instance`):

```tsx
        {server.instance ? (
          <div className="space-y-1">
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" className="accent-primary size-4" checked={purge} onChange={(e) => setPurge(e.target.checked)} />
              {t('servers.delete.purge_backups')}
            </label>
            <p className="text-muted-foreground text-sm">{t('servers.delete.purge_backups_hint')}</p>
          </div>
        ) : null}
```

- [ ] **Step 6: Rechte-Hinweis**

`web/src/features/users/permissions-page.tsx` nach `pluginsWarning`:

```tsx
  // Spec §5.10: server.backups reads every file (download) and resets the server.
  const backupsWarning = !targetIsAdmin && saved !== null && newlyGranted(current, saved, 'server.backups').length > 0
```

und nach dem Plugins-Hinweis:

```tsx
      {backupsWarning ? (
        <p role="status" className="border-destructive/50 mb-4 rounded-md border p-3 text-sm">
          {t('permissions.backups_warning')}
        </p>
      ) : null}
```

- [ ] **Step 7: Grün prüfen**

Run: `make check` → PASS; `make test-vm SUITE=web` → `schedule-card.test.tsx`, `time-zones.test.ts`, `settings-page.test.tsx`, `server-detail-page.test.tsx`, `permissions-page.test.tsx`, `i18n.test.ts` PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/backups web/src/features/settings web/src/features/servers web/src/features/users
git commit -m "feat(web): backup schedule, time zone setting, purge on delete and permission hint"
```

---

### Task 17: Playwright gegen den Mock

**Files:**
- Create: `web/e2e/backups.spec.ts`
- Modify: `web/playwright.config.ts` (nur `testMatch` des Projekts `mock`)

**Interfaces:**
- Consumes: `signIn`, `confirmSudo` (`web/e2e/helpers.ts`), Mock V6 (Seed von „survival“ und „modded“), Texte aus Aufgabe 14.
- Produces: E2E-Abdeckung für Erstellen, Pin, Restore mit Sudo, Zeitplan und `available = false`.

- [ ] **Step 1: Test schreiben**

`web/e2e/backups.spec.ts`:

```ts
import { expect, test } from '@playwright/test'
import { confirmSudo, signIn } from './helpers'

test('admin creates a backup with a note, pins a scheduled snapshot and restores one', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await page.getByRole('tab', { name: 'Backups', exact: true }).click()
  const list = page.getByRole('region', { name: 'Snapshots' })
  await expect(list.getByText('vor Update auf 1.21.11')).toBeVisible()
  await expect(list.getByText('Angepinnt')).toHaveCount(1)

  // Create a backup with a note and follow the job.
  await page.getByRole('button', { name: 'Backup erstellen' }).click()
  const create = page.getByRole('dialog', { name: 'Backup erstellen' })
  await create.getByLabel('Notiz (optional)').fill('vor dem Test')
  await create.getByRole('button', { name: 'Backup starten' }).click()
  await expect(create).toBeHidden()
  await expect(page.getByRole('region', { name: 'Backup läuft …' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Backup abgeschlossen' })).toBeVisible({ timeout: 15_000 })
  await expect(list.getByText('vor dem Test')).toBeVisible()

  // Pin the newest scheduled snapshot (its menu offers "Anpinnen").
  const menus = list.getByRole('button', { name: /^Aktionen für den Snapshot vom / })
  const count = await menus.count()
  for (let i = 0; i < count; i++) {
    await menus.nth(i).click()
    const pin = page.getByRole('menuitem', { name: 'Anpinnen' })
    if (await pin.isVisible()) {
      await pin.click()
      break
    }
    await page.keyboard.press('Escape')
  }
  await expect(page.getByText('Snapshot angepinnt.')).toBeVisible()
  await expect(list.getByText('Angepinnt')).toHaveCount(2)

  // Restore the manual snapshot: name confirmation, then sudo.
  const before = await list.getByText('Vor Restore').count()
  await list.getByRole('button', { name: /^Aktionen für den Snapshot vom / }).first().click()
  await page.getByRole('menuitem', { name: 'Wiederherstellen' }).click()
  const restore = page.getByRole('dialog', { name: /wiederherstellen\?$/ })
  await expect(restore.getByRole('button', { name: 'Wiederherstellen' })).toBeDisabled()
  await restore.getByLabel('Zum Bestätigen den Namen „survival“ eintippen').fill('survival')
  await restore.getByRole('button', { name: 'Wiederherstellen' }).click()
  await confirmSudo(page)
  await expect(page.getByRole('region', { name: 'Wiederherstellung läuft …' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Wiederherstellung abgeschlossen' })).toBeVisible({ timeout: 15_000 })
  await expect(list.getByText('Vor Restore')).toHaveCount(before + 1)
})

test('admin changes the schedule of a server', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await page.getByRole('tab', { name: 'Backups', exact: true }).click()
  const card = page.getByRole('region', { name: 'Zeitplan' })
  await expect(card.getByText(/Zeitzone des Panels: Europe\/Berlin/)).toBeVisible()
  await card.getByRole('combobox', { name: 'Zeitplan' }).selectOption('interval')
  await card.getByLabel('Intervall in Stunden').fill('6')
  await card.getByRole('button', { name: 'Zeitplan speichern' }).click()
  await expect(page.getByText('Zeitplan gespeichert.')).toBeVisible()
  await expect(card.getByText(/^Nächster Lauf: /)).toBeVisible()
})

test('a host without restic shows only a hint', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'modded', exact: true }).click()
  await page.getByRole('tab', { name: 'Backups', exact: true }).click()
  await expect(page.getByText(/Backups sind auf diesem Host nicht verfügbar/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Backup erstellen' })).toHaveCount(0)
})
```

Hinweis zum Restore-Schritt: Der Mock sortiert neueste zuerst; nach dem Erstellen ist der erste Eintrag das eben angelegte manuelle Backup. Liegt der Mock anders (z. B. Pin-ID neu, Reihenfolge unverändert), bleibt der Test gültig, weil er nur den ersten Eintrag nutzt.

- [ ] **Step 2: Spec-Datei registrieren**

In `web/playwright.config.ts` im Projekt `mock`:

```ts
      testMatch: /(?<![\w-])(totp|smoke|servers|plugins|backups)\.spec\.ts$/,
```

- [ ] **Step 3: Lauf**

Run: `make check` → PASS; `make test-vm SUITE=e2e` → `backups.spec.ts` (3 Tests) PASS. Schlägt ein Selektor fehl, weil der Mock anders seedet als in „Was Phase A liefert“ beschrieben, den Mock **nicht** ändern, sondern den Lead fragen.

- [ ] **Step 4: Gesamtlauf und PR**

Run: `make test-vm` (alle Suites) → PASS. Dann Push und Draft-PR „feat(web): backups tab“, `gh pr checks <n> --watch` bis `CI OK`.

- [ ] **Step 5: Commit**

```bash
git add web/e2e/backups.spec.ts web/playwright.config.ts
git commit -m "test(web): end-to-end backups against the mock"
```

---

## Selbstprüfung (Spec §6 ↔ Aufgaben)

| Spec | Aufgabe |
|---|---|
| §6.1 Tab sichtbar mit `server.backups` | 14 (Detailseite, Tests alice/Grant) |
| §6.1 Button „Backup erstellen“ mit optionaler Notiz | 14 (Dialog, Validierung, getrimmte Notiz, leerer Body) |
| §6.1 Job-Anzeige (Schritt, Bytes, Warteposition), Polling 2 s solange `queued`/`running` | 14 (`useBackupJob`, `JobStatus`, Polling-Test inkl. Stopp) |
| §6.1 Liste: Zeit relativ + absolut, Art-Badge, Notiz, Größe, Pin | 14 |
| §6.1 Aktionen Anpinnen/Lösen (nur geplante), Herunterladen, Wiederherstellen, Löschen | 15 |
| §6.1 Restore-Dialog mit Erklärung + Namenseingabe, danach Sudo; Löschen mit Bestätigung + Sudo | 15 (inkl. abgebrochener Sudo-Dialog) |
| §6.1 Karte „Zeitplan“: Modus, Intervall/Uhrzeit/Wochentag, Retention, Zeitzonen-Hinweis, nächster Lauf, letzter Status mit Fehler | 16 |
| §6.1 `available = false` → nur Hinweis | 14, 17 |
| §6.2 Zeitzone in den Einstellungen (`Intl.supportedValuesOf`) | 16 |
| §6.2 Checkbox „Backups ebenfalls löschen“ | 16 |
| §6.2 Rechte-Hinweis `server.backups` | 16 |
| §6.2 i18n de/en | 14 |
| §8 Playwright gegen den Mock | 17 |
