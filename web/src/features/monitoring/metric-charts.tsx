import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import type { MetricsRange, MetricsSeries } from '@/api/types'
import { LineChart, type ChartAxis } from '@/components/charts/line-chart'
import type { ChartPoint } from '@/components/charts/thin'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { isStale } from '@/lib/freshness'
import { cn } from '@/lib/utils'
import { metricsPollMs, serverMetricsQueryOptions } from './api'
import { formatDecimal, GIB, lastValue, metricPoints, RANGE_MS } from './model'

function newestPoint(data: MetricsSeries): number {
  let newest = 0
  for (const points of Object.values(data.series)) if (points.length > 0) newest = Math.max(newest, points[points.length - 1][0])
  return newest
}

/** Bytes → GiB before the scale: nice GiB steps instead of nice byte steps (1,9 / 3,7 GiB). */
function inGiB(points: ChartPoint[]): ChartPoint[] {
  return points.map((p) => ({ t: p.t, v: p.v / GIB, min: p.min / GIB, max: p.max / GIB }))
}

/**
 * The four charts of the tab (spec §7.2); 24 h and more show the average with a min–max band.
 * `tpsUnavailable`: the live source is "none", the TPS chart says so instead of "no data". A failed refresh keeps the
 * charts dimmed (the tab shows the notice, issue #155); only a series that never loaded gives an error.
 */
export function MetricCharts({ serverId, range, tpsUnavailable = false }: { serverId: number; range: MetricsRange; tpsUnavailable?: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const query = useQuery({ ...serverMetricsQueryOptions(serverId, range), refetchInterval: metricsPollMs(range), placeholderData: keepPreviousData })
  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  // Placeholder data belongs to the previous range: an error for the new one has nothing of its own to keep.
  if (query.data === undefined || (query.isError && query.isPlaceholderData))
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(query.error, t)}
      </p>
    )
  const data = query.data
  const stale = isStale(query) && !query.isPlaceholderData
  // The window ends when the answer arrived (render stays pure: no Date.now()); placeholder data of the previous
  // range has no update time of its own, so its newest point stands in until the new range is loaded. The chart
  // leaves out points outside the window; placeholder data is dimmed.
  const to = query.dataUpdatedAt > 0 ? query.dataUpdatedAt : newestPoint(data)
  const from = to - RANGE_MS[range]
  const band = range !== '1h'
  const long = range === '7d' || range === '30d'
  const common = { from, to, stepMs: data.step_ms, band, formatTime: long ? fmt.dateTime : fmt.time, formatTick: long ? fmt.date : fmt.time }
  const memLimit = lastValue(metricPoints(data, 'memory_limit_bytes'))
  const dec = (v: number, digits?: number) => formatDecimal(v, fmt.locale, digits ?? 1)

  return (
    <div
      className={cn('grid gap-6 transition-opacity lg:grid-cols-2', (query.isPlaceholderData || stale) && 'opacity-50')}
      aria-busy={query.isPlaceholderData || undefined}
      data-stale={stale || undefined}
    >
      <LineChart
        {...common}
        title={t('monitoring.charts.players')}
        integer
        series={[
          { id: 'players', label: t('monitoring.charts.players'), tone: 'primary', points: metricPoints(data, 'players') },
          { id: 'players_max', label: t('monitoring.charts.players_max'), tone: 'muted', points: metricPoints(data, 'players_max') },
        ]}
        formatValue={(v) => fmt.number(Math.round(v))}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.tps_mspt')}
        emptyText={tpsUnavailable ? t('monitoring.tps_source.none') : undefined}
        series={[
          { id: 'tps', label: t('monitoring.tps'), tone: 'primary', points: metricPoints(data, 'tps') },
          { id: 'mspt', label: t('monitoring.mspt'), tone: 'secondary', axis: 'right', points: metricPoints(data, 'mspt') },
        ]}
        formatValue={(v: number, axis: ChartAxis, digits?: number) => (axis === 'right' ? t('monitoring.ms', { value: dec(v, digits) }) : dec(v, digits))}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.cpu')}
        series={[{ id: 'cpu', label: t('monitoring.cpu'), tone: 'primary', points: metricPoints(data, 'cpu_percent') }]}
        formatValue={(v, _axis, digits) => t('monitoring.percent', { value: dec(v, digits) })}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.memory')}
        series={[{ id: 'memory', label: t('monitoring.memory'), tone: 'primary', points: inGiB(metricPoints(data, 'memory_bytes')) }]}
        limit={memLimit === null ? undefined : { value: memLimit / GIB, label: t('monitoring.charts.memory_limit') }}
        formatValue={(v, _axis, digits) => t('monitoring.gib', { value: dec(v, digits) })}
      />
    </div>
  )
}
