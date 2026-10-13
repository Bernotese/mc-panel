import type { AlertKind, MetricPoint, MetricsRange, MetricsSeries, MonitoringSettings, OverviewServer, Server, TpsSource, UpdateMonitoringSettingsRequest } from '@/api/types'
import type { ChartPoint } from '@/components/charts/thin'
import type { FieldError } from '@/features/servers/validation'

export const RANGES = ['1h', '24h', '7d', '30d'] as const satisfies readonly MetricsRange[]
export const RANGE_MS: Record<MetricsRange, number> = { '1h': 3_600_000, '24h': 86_400_000, '7d': 7 * 86_400_000, '30d': 30 * 86_400_000 }
export const GIB = 1024 ** 3

export function toChartPoints(points: readonly MetricPoint[] | undefined): ChartPoint[] {
  return (points ?? []).map(([t, v, min, max]) => ({ t, v, min, max }))
}

export function metricPoints(data: MetricsSeries | undefined, metric: string): ChartPoint[] {
  return toChartPoints(data?.series[metric])
}

export function lastValue(points: readonly ChartPoint[]): number | null {
  return points.length > 0 ? points[points.length - 1].v : null
}

/** Only for running containers; "stopped" is already the status badge (spec §7.1). */
export type Reachability = 'reachable' | 'unreachable'

export function reachability(server: Server, metrics: OverviewServer | undefined): Reachability | null {
  if (server.status.state !== 'running' || !metrics) return null
  if (metrics.up === true) return 'reachable'
  if (metrics.up === false) return 'unreachable'
  return null
}

export function alertKey(kind: AlertKind): string {
  return `monitoring.alert.${kind.replace('.', '_')}`
}

export function tpsSourceKey(source: TpsSource): string {
  return `monitoring.tps_source.${source}`
}

export function percent(used: number | null, total: number | null): number | null {
  if (used === null || total === null || total <= 0) return null
  return Math.min(100, Math.max(0, (used / total) * 100))
}

export function formatDecimal(n: number, locale: string, digits = 1): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(n)
}

export const SETTINGS_FIELDS = ['disk_min_percent', 'disk_min_gib', 'down_after_min', 'crash_restarts', 'crash_window_min'] as const
export type SettingsField = (typeof SETTINGS_FIELDS)[number]
export type SettingsDraft = Record<SettingsField, string>

/** Spec §4 / Global Constraints; the disk threshold is edited in GiB (1 GiB – 10 TiB). */
export const SETTINGS_LIMITS: Record<SettingsField, readonly [number, number]> = {
  disk_min_percent: [1, 50],
  disk_min_gib: [1, 10_240],
  down_after_min: [1, 60],
  crash_restarts: [2, 20],
  crash_window_min: [1, 120],
}

export function draftFromSettings(s: MonitoringSettings): SettingsDraft {
  const gib = s.disk_min_bytes / GIB
  return {
    disk_min_percent: String(s.disk_min_percent),
    disk_min_gib: Number.isInteger(gib) ? String(gib) : String(Math.round(gib * 100) / 100),
    down_after_min: String(s.down_after_min),
    crash_restarts: String(s.crash_restarts),
    crash_window_min: String(s.crash_window_min),
  }
}

export interface SettingsValidation {
  errors: Partial<Record<SettingsField, FieldError>>
  request: UpdateMonitoringSettingsRequest | null
}

export function validateSettings(d: SettingsDraft): SettingsValidation {
  const errors: Partial<Record<SettingsField, FieldError>> = {}
  const values: Partial<Record<SettingsField, number>> = {}
  for (const field of SETTINGS_FIELDS) {
    const decimal = field === 'disk_min_gib'
    const raw = d[field].trim().replace(',', '.')
    const [min, max] = SETTINGS_LIMITS[field]
    const ok = decimal ? /^\d+(\.\d{1,2})?$/.test(raw) : /^\d+$/.test(raw)
    if (!ok) {
      errors[field] = { key: decimal ? 'monitoring.settings.validation.decimal' : 'monitoring.settings.validation.integer' }
      continue
    }
    const n = Number(raw)
    if (n < min || n > max) errors[field] = { key: 'monitoring.settings.validation.range', params: { min, max } }
    else values[field] = n
  }
  if (Object.keys(errors).length > 0) return { errors, request: null }
  return {
    errors,
    request: {
      disk_min_percent: values.disk_min_percent!,
      disk_min_bytes: Math.round(values.disk_min_gib! * GIB),
      down_after_min: values.down_after_min!,
      crash_restarts: values.crash_restarts!,
      crash_window_min: values.crash_window_min!,
    },
  }
}

export function sameSettings(req: UpdateMonitoringSettingsRequest, s: MonitoringSettings): boolean {
  return (
    req.disk_min_percent === s.disk_min_percent &&
    req.disk_min_bytes === s.disk_min_bytes &&
    req.down_after_min === s.down_after_min &&
    req.crash_restarts === s.crash_restarts &&
    req.crash_window_min === s.crash_window_min
  )
}
