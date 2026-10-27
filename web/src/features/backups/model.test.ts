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
    expect(kindKey('dev-sync')).toBe('backups.kind.dev_sync')
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
