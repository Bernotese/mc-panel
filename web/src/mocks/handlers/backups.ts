import { http, HttpResponse } from 'msw'
import type { BackupJob, BackupJobKind, BackupJobStep, BackupOverview, BackupSchedule, BackupSnapshot, UpdateBackupScheduleRequest } from '@/api/types'
import { db, defaultBackupSchedule, findUser, nextId, recordAudit, type MockBackupJob, type MockBackupResult, type MockBackups, type MockServer } from '../db'
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

function startJob(
  c: Ctx,
  kind: BackupJobKind,
  steps: BackupJobStep[],
  result: MockBackupResult,
  snapshot: string | null,
  audit: MockBackupJob['audit'],
): BackupJob {
  const now = Date.now()
  const job: BackupJob = {
    id: `bj-${nextId()}`, kind, trigger: 'manual', state: 'queued', step: 'queued', queue_position: 1, bytes: 0,
    snapshot, error_code: null, error: '', queued_at: now, started_at: 0, finished_at: 0,
  }
  db.backupJobs[c.server.id] = { job, phases: ['queued', ...steps], startedMs: now, result, actorId: c.g.user.id, audit }
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
  recordAudit(findUser(mj.actorId) ?? null, mj.audit.action, `server:${serverId}`, 'success', mj.audit.details)
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

interface WallClock {
  year: number
  month: number // 1..12
  day: number
  hour: number
  minute: number
  second: number
  weekday: number // 0 = Sunday
}

/** The wall clock of an instant in a time zone. */
function wallClock(ms: number, tz: string): WallClock {
  const parts = new Intl.DateTimeFormat('en-GB', {
    timeZone: tz,
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(new Date(ms))
  const n = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((x) => x.type === type)?.value ?? 0)
  const w = { year: n('year'), month: n('month'), day: n('day'), hour: n('hour'), minute: n('minute'), second: n('second') }
  return { ...w, weekday: new Date(Date.UTC(w.year, w.month - 1, w.day)).getUTCDay() }
}

/** The instant of a wall-clock time (minutes after midnight) in a time zone; day may overflow the month. */
function zonedTime(tz: string, year: number, month: number, day: number, minutes: number): number {
  const local = Date.UTC(year, month - 1, day, 0, minutes)
  const offset = (ms: number) => {
    const w = wallClock(ms, tz)
    return Date.UTC(w.year, w.month - 1, w.day, w.hour, w.minute, w.second) - ms
  }
  // the second pass corrects the offset across a DST change
  return local - offset(local - offset(local))
}

/** The next run like the panel: daily/weekly in the panel time zone (backup_timezone). */
function nextRun(s: MockBackups['schedule'], now: number): number | null {
  switch (s.mode) {
    case 'off':
      return null
    case 'interval': {
      // like the panel: N hours after the last run, or N hours from now if that is not in the future
      const step = s.interval_hours * HOUR_MS
      const fromLast = s.last_run_at === null ? null : s.last_run_at + step
      return fromLast !== null && fromLast > now ? fromLast : now + step
    }
    case 'daily':
    case 'weekly': {
      const tz = db.serverSettings.backup_timezone
      const today = wallClock(now, tz)
      const step = s.mode === 'weekly' ? 7 : 1
      let k = s.mode === 'weekly' ? (s.weekday - today.weekday + 7) % 7 : 0
      let t = zonedTime(tz, today.year, today.month, today.day + k, s.time_of_day)
      while (t <= now) {
        k += step
        t = zonedTime(tz, today.year, today.month, today.day + k, s.time_of_day)
      }
      return t
    }
  }
}

/** A queued or running restore holds the server like the panel's data gate (deleteServer answers 409). */
export function restoreActive(serverId: number): boolean {
  const job = jobNow(serverId)
  return job !== null && job.kind === 'restore' && (job.state === 'queued' || job.state === 'running')
}

function validNote(n: string): boolean {
  return [...n].length <= MAX_NOTE && !CONTROL_RE.test(n) && n.trim() !== ''
}

/** "<server>-YYYYMMDD-HHMMSS.zip" in the panel time zone (backup_timezone), like the panel. */
function zipName(server: MockServer, time: number): string {
  const w = wallClock(time, db.serverSettings.backup_timezone)
  const p = (v: number, len = 2) => String(v).padStart(len, '0')
  return `${server.name}-${p(w.year, 4)}${p(w.month)}${p(w.day)}-${p(w.hour)}${p(w.minute)}${p(w.second)}.zip`
}

/** The schedule fields of an audit entry (from/to), like the panel. */
function scheduleDetails(s: MockBackups['schedule']): Record<string, unknown> {
  const { mode, interval_hours, time_of_day, weekday, keep_last, keep_daily, keep_weekly, keep_monthly } = s
  return { mode, interval_hours, time_of_day, weekday, keep_last, keep_daily, keep_weekly, keep_monthly }
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
      const job = startJob(c, 'backup', steps, { kind: 'create', snapshot: snap }, null, {
        action: 'backup.create',
        details: { note: note || null, snapshot: snap.id },
      })
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
      const before = c.b.schedule
      const s = { ...before, ...req }
      s.next_run_at = nextRun(s, Date.now())
      c.b.schedule = s
      recordAudit(c.g.user, 'backup.schedule_update', `server:${c.server.id}`, 'success', { from: scheduleDetails(before), to: scheduleDetails(s) })
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
      const old = snap.id
      const id = randomId()
      Object.assign(snap, { id, short_id: id.slice(0, 8), pinned: body.pinned })
      recordAudit(c.g.user, body.pinned ? 'backup.pin' : 'backup.unpin', `server:${c.server.id}`, 'success', { snapshot: old, new_snapshot: id })
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
      const job = startJob(c, 'restore', steps, { kind: 'restore', preRestore: pre }, snap.id, {
        action: 'backup.restore',
        details: { snapshot: snap.id, time: snap.time, kind: snap.kind },
      })
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
      const job = startJob(c, 'delete', ['deleting'], { kind: 'delete', id: snap.id }, snap.id, {
        action: 'backup.delete',
        details: { snapshot: snap.id, kind: snap.kind },
      })
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
