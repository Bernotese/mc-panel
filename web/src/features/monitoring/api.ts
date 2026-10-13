import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { LiveMetrics, MetricsRange, MetricsSeries, MonitoringOverview, MonitoringSettings, UpdateMonitoringSettingsRequest } from '@/api/types'

/** Live header while the tab is open (spec §6.2). */
export const LIVE_POLL_MS = 5_000
/** Dashboard values follow the 30-s collector pass. */
export const OVERVIEW_POLL_MS = 30_000
/** 1 h shows 30-s values; longer ranges change at most every 5 minutes. */
export const metricsPollMs = (range: MetricsRange) => (range === '1h' ? 30_000 : 300_000)

export const monitoringOverviewKey = ['monitoring', 'overview'] as const
export const monitoringSettingsKey = ['monitoring', 'settings'] as const
/** Below serverKey(id): a deleted or reloaded server takes its metrics along via the servers prefix. */
export const serverMetricsKey = (id: number, range: MetricsRange) => ['servers', id, 'metrics', range] as const
export const liveMetricsKey = (id: number) => ['servers', id, 'metrics', 'live'] as const

export const monitoringOverviewQueryOptions = queryOptions<MonitoringOverview>({
  queryKey: monitoringOverviewKey,
  queryFn: () => unwrap(api.GET('/monitoring/overview')),
})

export function serverMetricsQueryOptions(id: number, range: MetricsRange) {
  return queryOptions<MetricsSeries>({
    queryKey: serverMetricsKey(id, range),
    queryFn: () => unwrap(api.GET('/servers/{id}/metrics', { params: { path: { id }, query: { range } } })),
  })
}

export function liveMetricsQueryOptions(id: number) {
  return queryOptions<LiveMetrics>({
    queryKey: liveMetricsKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/metrics/live', { params: { path: { id } } })),
  })
}

export const monitoringSettingsQueryOptions = queryOptions<MonitoringSettings>({
  queryKey: monitoringSettingsKey,
  queryFn: () => unwrap(api.GET('/monitoring/settings')),
})

export function updateMonitoringSettings(body: UpdateMonitoringSettingsRequest): Promise<MonitoringSettings> {
  return unwrap(api.PUT('/monitoring/settings', { body }))
}

export function updateServerAlerts(id: number, downAlerts: boolean): Promise<void> {
  return unwrapVoid(api.PUT('/servers/{id}/alerts', { params: { path: { id } }, body: { down_alerts: downAlerts } }))
}
