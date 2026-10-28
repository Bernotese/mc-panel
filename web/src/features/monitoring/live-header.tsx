import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { Section } from '@/components/section'
import { StatSlot } from '@/components/stat-slot'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { isStale } from '@/lib/freshness'
import { cn } from '@/lib/utils'
import { LIVE_POLL_MS, liveMetricsQueryOptions } from './api'
import { formatDecimal, tpsSourceKey } from './model'

/**
 * Polled every 5 s while mounted; Radix unmounts the inactive tab, which ends the polling (spec §6.2).
 * `running`: the container runs; otherwise there is no TPS source to name, only "stopped". A failed refresh keeps the
 * last values muted (the tab shows the notice, issue #155); only values that never loaded give an error.
 */
export function LiveHeader({ serverId, running }: { serverId: number; running: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const live = useQuery({ ...liveMetricsQueryOptions(serverId), refetchInterval: LIVE_POLL_MS })
  const missing = t('monitoring.missing')

  let body
  if (live.isPending) body = <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  else if (live.data === undefined)
    body = (
      <p role="alert" className="text-destructive text-sm">
        {isApiError(live.error, 'agent_unavailable') ? t('monitoring.live.agent_unavailable') : errorMessage(live.error, t)}
      </p>
    )
  else {
    const m = live.data
    const stale = isStale(live)
    const tps = running && m.tps_source === 'none' ? t('monitoring.tps_source.none') : m.tps === null ? missing : formatDecimal(m.tps, fmt.locale)
    const items: [string, string][] = [
      [t('monitoring.players'), m.players === null ? missing : t('monitoring.players_value', { players: m.players, max: m.players_max ?? missing })],
      [t('monitoring.tps'), tps],
      [t('monitoring.mspt'), m.mspt === null ? missing : t('monitoring.ms', { value: formatDecimal(m.mspt, fmt.locale) })],
      [t('monitoring.cpu'), m.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(m.cpu_percent, fmt.locale) })],
      [
        t('monitoring.memory'),
        m.memory_bytes === null || m.memory_limit_bytes === null
          ? missing
          : t('monitoring.host.memory_value', { used: formatGiB(m.memory_bytes, fmt.locale), total: formatGiB(m.memory_limit_bytes, fmt.locale) }),
      ],
    ]
    body = (
      <div className="space-y-3">
        {running && m.up === false && !stale ? (
          <p role="status" className="text-destructive text-sm">
            {t('monitoring.live.unreachable')}
          </p>
        ) : null}
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          {items.map(([label, value]) => (
            <StatSlot key={label} label={label} value={value} stale={stale} />
          ))}
        </div>
        {!running ? (
          <p className="text-muted-foreground text-xs">{t('monitoring.live.stopped')}</p>
        ) : m.tps_source !== 'none' ? (
          <p className="text-muted-foreground text-xs">{t(tpsSourceKey(m.tps_source))}</p>
        ) : null}
        {m.player_names.length > 0 ? (
          <ul
            aria-label={t('monitoring.live.player_names')}
            data-stale={stale || undefined}
            className={cn('flex flex-wrap gap-2 text-sm', stale && 'text-muted-foreground')}
          >
            {m.player_names.map((name) => (
              <li key={name} className="bevel-inset font-pixel px-2 py-0.5">
                {name}
              </li>
            ))}
          </ul>
        ) : m.players === 0 ? (
          <p className="text-muted-foreground text-sm">{t('monitoring.live.no_players')}</p>
        ) : null}
      </div>
    )
  }

  return (
    <Section title={t('monitoring.live.title')} headingLevel={3}>
      {body}
    </Section>
  )
}
