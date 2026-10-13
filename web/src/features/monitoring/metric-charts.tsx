import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import type { MetricsRange } from '@/api/types'
import { LineChart, type ChartAxis } from '@/components/charts/line-chart'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { metricsPollMs, serverMetricsQueryOptions } from './api'
import { formatDecimal, lastValue, metricPoints, RANGE_MS } from './model'

/** The four charts of the tab (spec §7.2); 24 h and more show the average with a min–max band. */
export function MetricCharts({ serverId, range }: { serverId: number; range: MetricsRange }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const query = useQuery({ ...serverMetricsQueryOptions(serverId, range), refetchInterval: metricsPollMs(range), placeholderData: keepPreviousData })
  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  if (query.isError)
    return (
      <p role="alert" className="text-destructive">
        {errorMessage(query.error, t)}
      </p>
    )
  const data = query.data
  const to = query.dataUpdatedAt || Date.now()
  const from = to - RANGE_MS[range]
  const band = range !== '1h'
  const long = range === '7d' || range === '30d'
  const common = { from, to, stepMs: data.step_ms, band, formatTime: long ? fmt.dateTime : fmt.time, formatTick: long ? fmt.date : fmt.time }
  const memLimit = lastValue(metricPoints(data, 'memory_limit_bytes'))

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <LineChart
        {...common}
        title={t('monitoring.charts.players')}
        series={[
          { id: 'players', label: t('monitoring.charts.players'), tone: 'primary', points: metricPoints(data, 'players') },
          { id: 'players_max', label: t('monitoring.charts.players_max'), tone: 'muted', points: metricPoints(data, 'players_max') },
        ]}
        formatValue={(v) => fmt.number(Math.round(v))}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.tps_mspt')}
        series={[
          { id: 'tps', label: t('monitoring.tps'), tone: 'primary', points: metricPoints(data, 'tps') },
          { id: 'mspt', label: t('monitoring.mspt'), tone: 'secondary', axis: 'right', points: metricPoints(data, 'mspt') },
        ]}
        formatValue={(v: number, axis: ChartAxis) => (axis === 'right' ? t('monitoring.ms', { value: formatDecimal(v, fmt.locale) }) : formatDecimal(v, fmt.locale))}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.cpu')}
        series={[{ id: 'cpu', label: t('monitoring.cpu'), tone: 'primary', points: metricPoints(data, 'cpu_percent') }]}
        formatValue={(v) => t('monitoring.percent', { value: formatCpu(v, fmt.locale) })}
      />
      <LineChart
        {...common}
        title={t('monitoring.charts.memory')}
        series={[{ id: 'memory', label: t('monitoring.memory'), tone: 'primary', points: metricPoints(data, 'memory_bytes') }]}
        limit={memLimit === null ? undefined : { value: memLimit, label: t('monitoring.charts.memory_limit') }}
        formatValue={(v) => t('monitoring.gib', { value: formatGiB(v, fmt.locale) })}
      />
    </div>
  )
}
