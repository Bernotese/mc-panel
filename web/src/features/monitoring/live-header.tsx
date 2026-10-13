import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { LIVE_POLL_MS, liveMetricsQueryOptions } from './api'
import { formatDecimal, tpsSourceKey } from './model'

/**
 * Polled every 5 s while mounted; Radix unmounts the inactive tab, which ends the polling (spec §6.2).
 * `running`: the container runs; otherwise there is no TPS source to name, only "stopped".
 */
export function LiveHeader({ serverId, running }: { serverId: number; running: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const live = useQuery({ ...liveMetricsQueryOptions(serverId), refetchInterval: LIVE_POLL_MS })
  const missing = t('monitoring.missing')

  let body
  if (live.isPending) body = <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  else if (live.isError)
    body = (
      <p role="alert" className="text-destructive text-sm">
        {isApiError(live.error, 'agent_unavailable') ? t('monitoring.live.agent_unavailable') : errorMessage(live.error, t)}
      </p>
    )
  else {
    const m = live.data
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
        {running && m.up === false ? (
          <p role="status" className="text-destructive text-sm">
            {t('monitoring.live.unreachable')}
          </p>
        ) : null}
        <dl className="grid grid-cols-2 gap-3 text-sm tabular-nums sm:grid-cols-5">
          {items.map(([label, value]) => (
            <div key={label}>
              <dt className="text-muted-foreground text-xs">{label}</dt>
              <dd className="text-base font-medium">{value}</dd>
            </div>
          ))}
        </dl>
        {!running ? (
          <p className="text-muted-foreground text-xs">{t('monitoring.live.stopped')}</p>
        ) : m.tps_source !== 'none' ? (
          <p className="text-muted-foreground text-xs">{t(tpsSourceKey(m.tps_source))}</p>
        ) : null}
        {m.player_names.length > 0 ? (
          <ul aria-label={t('monitoring.live.player_names')} className="flex flex-wrap gap-2 text-sm">
            {m.player_names.map((name) => (
              <li key={name} className="bg-muted rounded px-2 py-0.5">
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
    <section aria-labelledby={id} className="space-y-3 rounded-md border p-4">
      <h3 id={id} className="font-semibold">
        {t('monitoring.live.title')}
      </h3>
      {body}
    </section>
  )
}
