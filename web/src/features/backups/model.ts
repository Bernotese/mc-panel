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

/** The IANA zone the browser formats times in; null if the browser does not tell. */
export function browserTimeZone(): string | null {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || null
  } catch {
    return null
  }
}
