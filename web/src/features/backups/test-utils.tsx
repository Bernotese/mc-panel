import { http, HttpResponse } from 'msw'
import type { BackupJob, BackupOverview, BackupSchedule, BackupSnapshot } from '@/api/types'
import { renderServerTab } from '@/test/render-section'
import { server } from '@/mocks/node'
import { BackupsTab } from './backups-tab'

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
  const { user, queryClient } = await renderServerTab((s) => <BackupsTab server={s} />, {
    serverId,
    username,
    sudo: opts.sudo,
    path: `/servers/${serverId}/backups`,
  })
  return { user, queryClient }
}
