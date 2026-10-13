import { http, HttpResponse } from 'msw'
import type { HostSnapshot, LiveMetrics, MetricsRange, MetricsSeries, MonitoringOverview, MonitoringSettings, OverviewServer } from '@/api/types'
import { server } from '@/mocks/node'
import { GIB } from './model'

export function overviewServer(id: number, over: Partial<OverviewServer> = {}): OverviewServer {
  return {
    id,
    up: true,
    players: 0,
    players_max: 20,
    tps: 20,
    cpu_percent: 5,
    memory_bytes: 2 * GIB,
    memory_limit_bytes: 5 * GIB,
    sparkline: [],
    alerts: [],
    ...over,
  }
}

export function hostSnapshot(over: Partial<HostSnapshot> = {}): HostSnapshot {
  return {
    at: Date.now(),
    cpu_percent: 23.4,
    load1: 0.52,
    memory_used: 6 * GIB,
    memory_total: 16 * GIB,
    disk_free: 80 * GIB,
    disk_total: 200 * GIB,
    alerts: [],
    ...over,
  }
}

export function liveMetrics(over: Partial<LiveMetrics> = {}): LiveMetrics {
  return {
    at: Date.now(),
    up: true,
    players: 2,
    players_max: 20,
    player_names: ['Alex', 'Steve'],
    tps: 19.9,
    mspt: 12.3,
    cpu_percent: 31.5,
    memory_bytes: 3 * GIB,
    memory_limit_bytes: 5 * GIB,
    tps_source: 'tick_query',
    ...over,
  }
}

const STEPS: Record<MetricsRange, number> = { '1h': 30_000, '24h': 300_000, '7d': 3_600_000, '30d': 3_600_000 }

/** Three points per metric ending now; values from `values` (default 1, 2, 3). */
export function metricsSeries(range: MetricsRange, metrics: string[], values: [number, number, number] = [1, 2, 3]): MetricsSeries {
  const step = STEPS[range]
  const now = Date.now()
  const series: MetricsSeries['series'] = {}
  for (const m of metrics) series[m] = values.map((v, i) => [now - (2 - i) * step, v, v, v])
  return { range, step_ms: step, series }
}

export function monitoringSettings(over: Partial<MonitoringSettings> = {}): MonitoringSettings {
  return { disk_min_percent: 10, disk_min_bytes: 10 * GIB, down_after_min: 3, crash_restarts: 3, crash_window_min: 10, alerts: [], ...over }
}

export function mockOverview(o: MonitoringOverview) {
  server.use(http.get('*/api/monitoring/overview', () => HttpResponse.json(o)))
}

export function mockLive(l: LiveMetrics | ((n: number) => LiveMetrics)) {
  const state = { calls: 0 }
  server.use(
    http.get('*/api/servers/:id/metrics/live', () => {
      state.calls += 1
      return HttpResponse.json(typeof l === 'function' ? l(state.calls) : l)
    }),
  )
  return state
}

/** Records the requested ranges and answers with metricsSeries for all server metrics. */
export function mockMetrics() {
  const ranges: string[] = []
  server.use(
    http.get('*/api/servers/:id/metrics', ({ request }) => {
      const range = (new URL(request.url).searchParams.get('range') ?? '1h') as MetricsRange
      ranges.push(range)
      return HttpResponse.json(metricsSeries(range, ['up', 'players', 'players_max', 'tps', 'mspt', 'cpu_percent', 'memory_bytes', 'memory_limit_bytes']))
    }),
  )
  return ranges
}

export function mockMonitoringSettings(s: MonitoringSettings) {
  server.use(http.get('*/api/monitoring/settings', () => HttpResponse.json(s)))
}
