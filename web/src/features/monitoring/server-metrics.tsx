import { useTranslation } from 'react-i18next'
import type { OverviewServer, Server } from '@/api/types'
import { Sparkline } from '@/components/charts/sparkline'
import { Badge } from '@/components/ui/badge'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { useFormat } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Meter } from './meter'
import { alertKey, formatDecimal, reachability } from './model'

/** Monitoring values on a dashboard card (spec §7.1); every missing value is "–", never 0. */
export function ServerMetrics({ server, metrics }: { server: Server; metrics: OverviewServer | undefined }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  if (!metrics) return null
  const missing = t('monitoring.missing')
  const reach = reachability(server, metrics)
  const players = metrics.players === null ? missing : t('monitoring.players_value', { players: metrics.players, max: metrics.players_max ?? missing })
  const tps = metrics.tps === null ? missing : formatDecimal(metrics.tps, fmt.locale)
  const cpu = metrics.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(metrics.cpu_percent, fmt.locale) })
  // The schema types a point as number[] (minItems/maxItems 2); the chart wants [at, value] pairs.
  const spark = metrics.sparkline.map(([at, v]) => [at, v] as const)
  const sparkMax = spark.reduce((m, [, v]) => Math.max(m, v), 0)

  return (
    <div className="space-y-2 text-sm" data-testid="server-metrics">
      {reach ? (
        <p className={cn('flex items-center gap-2', reach === 'unreachable' ? 'text-destructive' : 'text-muted-foreground')}>
          <span aria-hidden="true" className={cn('inline-block size-2 rounded-full', reach === 'unreachable' ? 'bg-destructive' : 'bg-primary')} />
          {t(`monitoring.reachability.${reach}`)}
        </p>
      ) : null}
      <dl className="grid grid-cols-3 gap-2 tabular-nums">
        <div>
          <dt className="text-muted-foreground text-xs">{t('monitoring.players')}</dt>
          <dd>{players}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground text-xs">{t('monitoring.tps')}</dt>
          <dd>{tps}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground text-xs">{t('monitoring.cpu')}</dt>
          <dd>{cpu}</dd>
        </div>
      </dl>
      {metrics.memory_bytes !== null && metrics.memory_limit_bytes !== null && metrics.memory_limit_bytes > 0 ? (
        <Meter
          label={t('monitoring.memory')}
          value={metrics.memory_bytes}
          max={metrics.memory_limit_bytes}
          valueText={t('monitoring.host.memory_value', { used: formatGiB(metrics.memory_bytes, fmt.locale), total: formatGiB(metrics.memory_limit_bytes, fmt.locale) })}
        />
      ) : null}
      {spark.length >= 2 ? (
        <Sparkline points={spark} spanMs={3_600_000} label={t('monitoring.sparkline', { current: spark[spark.length - 1][1], max: sparkMax })} />
      ) : null}
      {metrics.alerts.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {metrics.alerts.map((kind) => (
            <Badge key={kind} variant="destructive">
              {t(alertKey(kind))}
            </Badge>
          ))}
        </div>
      ) : null}
    </div>
  )
}
