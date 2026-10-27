import { http, HttpResponse } from 'msw'
import type { AlertKind, FiringAlert, HostSnapshot, LiveMetrics, MetricPoint, MetricsRange, MetricsSeries, MonitoringOverview, MonitoringSettings, OverviewServer, UpdateMonitoringSettingsRequest } from '@/api/types'
import { db, memoryLimitBytes, recordAudit, type MockServer } from '../db'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type MockOperation } from './util'

const GIB = 1024 ** 3
const STEP: Record<MetricsRange, number> = { '1h': 30_000, '24h': 300_000, '7d': 3_600_000, '30d': 3_600_000 }
const SPAN: Record<MetricsRange, number> = { '1h': 3_600_000, '24h': 86_400_000, '7d': 7 * 86_400_000, '30d': 30 * 86_400_000 }
const RANGES = Object.keys(STEP) as MetricsRange[]
const LIMITS: Record<keyof UpdateMonitoringSettingsRequest, [number, number]> = {
  disk_min_percent: [1, 50],
  disk_min_bytes: [GIB, 10 * 1024 * GIB],
  down_after_min: [1, 60],
  crash_restarts: [2, 20],
  crash_window_min: [1, 120],
}
const LIVE_STEP = 5_000 // live values change every 5 s (V6)
const NAMES = ['Alex', 'Steve', 'Notch_Fan', 'Builder42', 'Creeper_X']

/** Deterministic noise in [0, 1) for a seed and an integer index. */
function noise(seed: number, i: number): number {
  const x = Math.sin(seed * 12.9898 + i * 78.233) * 43758.5453
  return x - Math.floor(x)
}

/** The mock scenario of a server (Abweichung 7): survival up, modded down, others without values. */
type Scenario = 'up' | 'down' | 'off'
function scenario(s: MockServer): Scenario {
  if (!s.instance) return 'off'
  if (s.name === 'modded') return 'down'
  return s.status.state === 'running' ? 'up' : 'off'
}

/** Value of a metric at time t for a running server (players follow the time of day, at most 12).
 * The noise index changes every step (30 s for the history and the overview, 5 s for live). */
function value(s: MockServer, metric: string, t: number, now: number, step = 30_000): number | null {
  const seed = s.id
  const hour = new Date(t).getUTCHours() + new Date(t).getUTCMinutes() / 60
  const i = Math.floor(t / step)
  switch (metric) {
    case 'up':
      return 1
    case 'players':
      return Math.min(12, Math.max(0, Math.round(6 + 6 * Math.sin(((hour - 14) / 24) * 2 * Math.PI) + 2 * noise(seed, i) - 1)))
    case 'players_max':
      return s.instance?.properties.max_players ?? 20
    case 'tps': {
      const dip = Math.abs(t - (now - 3 * 3_600_000)) < 600_000 ? 6 : 0 // a dip 3 h ago
      return Math.min(20, 19.5 + 0.5 * noise(seed + 1, i)) - dip
    }
    case 'mspt':
      return 18 + 12 * noise(seed + 2, i)
    case 'cpu_percent':
      return 8 + 10 * noise(seed + 3, i)
    case 'memory_bytes':
      return Math.round(memoryLimitBytes(s.instance?.memory_mib ?? 2048) * (0.45 + 0.1 * noise(seed + 4, i)))
    case 'memory_limit_bytes':
      return memoryLimitBytes(s.instance?.memory_mib ?? 2048)
  }
  return null
}

const SERVER_METRICS = ['up', 'players', 'players_max', 'tps', 'mspt', 'cpu_percent', 'memory_bytes', 'memory_limit_bytes']

function points(range: MetricsRange, now: number, f: (t: number) => number | null): MetricPoint[] {
  const step = STEP[range]
  const end = Math.floor(now / step) * step
  const out: MetricPoint[] = []
  for (let t = end - SPAN[range] + step; t <= end; t += step) {
    const v = f(t)
    if (v === null) continue
    const spread = range === '1h' ? 0 : Math.abs(v) * 0.08
    out.push([t, v, v - spread, v + spread])
  }
  return out
}

function serverSeries(s: MockServer, range: MetricsRange, now: number): MetricsSeries['series'] {
  const sc = scenario(s)
  const series: MetricsSeries['series'] = {}
  // like the backend: every metric key is present, empty without values (V6)
  if (sc === 'off') return Object.fromEntries(SERVER_METRICS.map((m) => [m, []]))
  for (const m of SERVER_METRICS) {
    // modded went down 5 minutes ago: up = 0 since then, no other values since then
    series[m] = points(range, now, (t) => {
      if (sc === 'down' && t > now - 5 * 60_000) return m === 'up' ? 0 : null
      if (sc === 'down' && (m === 'tps' || m === 'mspt')) return null // tps_source none
      return value(s, m, t, now)
    })
  }
  return series
}

function hostValues(now: number): Omit<HostSnapshot, 'alerts'> {
  const total = 512 * GIB
  const settings = db.monitoring.settings
  const threshold = Math.max(settings.disk_min_bytes, (total * settings.disk_min_percent) / 100)
  return {
    at: Math.floor(now / 30_000) * 30_000,
    cpu_percent: 23 + 10 * noise(99, Math.floor(now / 30_000)),
    load1: 1.4,
    memory_used: 9 * GIB,
    memory_total: 16 * GIB,
    disk_free: db.monitoring.diskLow ? threshold - 2 * GIB : threshold + 6 * GIB,
    disk_total: total,
  }
}

function serverAlerts(s: MockServer): AlertKind[] {
  // A dev raises no down alerts: it sleeps on purpose (dev instances spec §2).
  return scenario(s) === 'down' && !s.dev && (db.monitoring.downAlerts[s.id] ?? true) ? ['server.down'] : []
}

function firing(now: number): FiringAlert[] {
  const out: FiringAlert[] = db.servers
    .filter((s) => serverAlerts(s).length > 0)
    .map((s) => ({ kind: 'server.down', server_id: s.id, server_name: s.name, since: now - 5 * 60_000, detail: 'not reachable for 5 minutes' }))
  if (db.monitoring.diskLow) out.push({ kind: 'host.disk_low', server_id: null, server_name: null, since: now - 10 * 60_000, detail: 'less free space than the threshold' })
  return out
}

function settingsBody(now: number): MonitoringSettings {
  return { ...db.monitoring.settings, alerts: firing(now) }
}

function rangeParam(request: Request): MetricsRange | null {
  const r = new URL(request.url).searchParams.get('range')
  return RANGES.includes(r as MetricsRange) ? (r as MetricsRange) : null
}

export const monitoringOperations: MockOperation[] = [
  op(
    'getServerMetrics',
    http.get(`${API}/servers/:id/metrics`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const s = db.servers.find((x) => x.id === id)
      if (!s) return apiError(404, 'not_found', 'server not found')
      const range = rangeParam(request)
      if (!range) return apiError(400, 'validation_failed', 'range must be 1h, 24h, 7d or 30d')
      return HttpResponse.json<MetricsSeries>({ range, step_ms: STEP[range], series: serverSeries(s, range, Date.now()) })
    }),
  ),
  op(
    'getServerLiveMetrics',
    http.get(`${API}/servers/:id/metrics/live`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const s = db.servers.find((x) => x.id === id)
      if (!s) return apiError(404, 'not_found', 'server not found')
      const now = Date.now()
      const sc = scenario(s)
      const empty: LiveMetrics = { at: null, up: null, players: null, players_max: null, player_names: [], tps: null, mspt: null, cpu_percent: null, memory_bytes: null, memory_limit_bytes: null, tps_source: 'unknown' }
      if (sc === 'off') return HttpResponse.json<LiveMetrics>(empty)
      // like the overview: modded keeps its players_max; stopped servers stay at tps_source unknown
      if (sc === 'down') return HttpResponse.json<LiveMetrics>({ ...empty, at: now, up: false, players_max: value(s, 'players_max', now, now), tps_source: 'none' })
      // live always shows at least one named player (V6), whatever the time of day
      const players = Math.max(1, value(s, 'players', now, now, LIVE_STEP) ?? 0)
      return HttpResponse.json<LiveMetrics>({
        at: now,
        up: true,
        players,
        players_max: value(s, 'players_max', now, now),
        player_names: NAMES.slice(0, Math.min(players, 12)),
        tps: value(s, 'tps', now, now, LIVE_STEP),
        mspt: value(s, 'mspt', now, now, LIVE_STEP),
        cpu_percent: value(s, 'cpu_percent', now, now, LIVE_STEP),
        memory_bytes: value(s, 'memory_bytes', now, now, LIVE_STEP),
        memory_limit_bytes: value(s, 'memory_limit_bytes', now, now),
        tps_source: 'tick_query',
      })
    }),
  ),
  op(
    'updateServerAlerts',
    http.put(`${API}/servers/:id/alerts`, async ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const s = db.servers.find((x) => x.id === idParam(params))
      if (!s) return apiError(404, 'not_found', 'server not found')
      const body = await readJson<{ down_alerts?: unknown }>(request)
      if (!body || typeof body.down_alerts !== 'boolean') return apiError(400, 'validation_failed', 'down_alerts is required')
      db.monitoring.downAlerts[s.id] = body.down_alerts
      recordAudit(g.user, 'server.alerts_update', `server:${s.id}`, 'success', { down_alerts: body.down_alerts })
      return new HttpResponse(null, { status: 204 })
    }),
  ),
  op(
    'getMonitoringOverview',
    http.get(`${API}/monitoring/overview`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const now = Date.now()
      const servers: OverviewServer[] = db.servers
        .filter((s) => serverPermissions(g.user, s.id).includes('server.view'))
        .map((s) => {
          const sc = scenario(s)
          const at = (m: string) => (sc === 'up' ? value(s, m, now, now) : null)
          return {
            id: s.id,
            up: sc === 'off' ? null : sc === 'up',
            players: at('players'),
            players_max: sc === 'off' ? null : value(s, 'players_max', now, now),
            tps: at('tps'),
            cpu_percent: at('cpu_percent'),
            memory_bytes: at('memory_bytes'),
            memory_limit_bytes: at('memory_limit_bytes'),
            sparkline: sc === 'up' ? points('1h', now, (t) => value(s, 'players', t, now)).map(([t, v]) => [t, v]) : [],
            alerts: serverAlerts(s),
          }
        })
      const host: HostSnapshot | null = g.user.role === 'admin' ? { ...hostValues(now), alerts: db.monitoring.diskLow ? ['host.disk_low'] : [] } : null
      return HttpResponse.json<MonitoringOverview>({ servers, host })
    }),
  ),
  op(
    'getHostMetrics',
    http.get(`${API}/monitoring/host`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const range = rangeParam(request)
      if (!range) return apiError(400, 'validation_failed', 'range must be 1h, 24h, 7d or 30d')
      const now = Date.now()
      const h = hostValues(now)
      const series: MetricsSeries['series'] = {
        cpu_percent: points(range, now, (t) => 20 + 15 * noise(99, Math.floor(t / 30_000))),
        load1: points(range, now, (t) => 1 + noise(98, Math.floor(t / 30_000))),
        memory_used: points(range, now, (t) => 8 * GIB + GIB * noise(97, Math.floor(t / 30_000))),
        memory_total: points(range, now, () => h.memory_total),
        disk_free: points(range, now, (t) => (h.disk_free ?? 0) + Math.round(((now - t) / SPAN['30d']) * 20 * GIB)),
        disk_total: points(range, now, () => h.disk_total),
      }
      return HttpResponse.json<MetricsSeries>({ range, step_ms: STEP[range], series })
    }),
  ),
  op(
    'getMonitoringSettings',
    http.get(`${API}/monitoring/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<MonitoringSettings>(settingsBody(Date.now()))
    }),
  ),
  op(
    'updateMonitoringSettings',
    http.put(`${API}/monitoring/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<Record<string, unknown>>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      const keys = Object.keys(LIMITS) as (keyof UpdateMonitoringSettingsRequest)[]
      if (Object.keys(body).some((k) => !keys.includes(k as keyof UpdateMonitoringSettingsRequest))) return apiError(400, 'bad_request', 'unknown field')
      for (const k of keys) {
        const v = body[k]
        const [lo, hi] = LIMITS[k]
        if (typeof v !== 'number' || !Number.isInteger(v) || v < lo || v > hi) return apiError(400, 'validation_failed', `${k} must be between ${lo} and ${hi}`)
      }
      const from = { ...db.monitoring.settings }
      db.monitoring.settings = body as unknown as UpdateMonitoringSettingsRequest
      recordAudit(g.user, 'monitoring.settings_update', 'monitoring', 'success', { from, to: db.monitoring.settings })
      return HttpResponse.json<MonitoringSettings>(settingsBody(Date.now()))
    }),
  ),
]
