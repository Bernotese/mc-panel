import { Link } from '@tanstack/react-router'
import { Users } from 'lucide-react'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { OverviewServer, Server, ServerState } from '@/api/types'
import { Sparkline } from '@/components/charts/sparkline'
import { PingBars } from '@/components/ping-bars'
import { PixelIcon } from '@/components/pixel-icon'
import { alertKey, formatDecimal, reachability } from '@/features/monitoring/model'
import { AddressLine } from '@/features/servers/address-line'
import { describeInstance, formatCpu, formatGiB } from '@/features/servers/format'
import { PowerButtons } from '@/features/servers/power-buttons'
import { StatusBadge } from '@/features/servers/status-badge'
import { useFormat } from '@/lib/format'
import { cn } from '@/lib/utils'

const TRANSITIONAL: ReadonlySet<ServerState> = new Set<ServerState>(['creating', 'starting', 'stopping', 'restarting', 'unknown'])

function iconState(state: ServerState): 'online' | 'offline' | 'unknown' {
  if (state === 'running') return 'online'
  return TRANSITIONAL.has(state) ? 'unknown' : 'offline'
}

/** One "MOTD" fact; each value is its own text node so that "–" and numbers stay findable. */
function Fact({ label, children, className }: { label?: string; children: ReactNode; className?: string }) {
  return (
    <span className={cn('inline-flex items-baseline gap-1', className)}>
      {label ? <span className="text-muted-foreground text-xs">{label}</span> : null}
      <span className="font-medium">{children}</span>
    </span>
  )
}

/** One server in the multiplayer-style list (spec §5.1); every missing value is "–", never 0. */
export function ServerRow({ server, metrics }: { server: Server; metrics?: OverviewServer }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const { instance, status } = server
  const canPower = server.permissions.includes('server.power')
  const running = status.state === 'running'
  const showMemory = running && status.memory_limit_bytes > 0
  const missing = t('monitoring.missing')
  const reach = reachability(server, metrics)
  // The schema types a point as number[] (minItems/maxItems 2); the chart wants [at, value] pairs.
  const spark = (metrics?.sparkline ?? []).map(([at, v]) => [at, v] as const)
  const sparkMax = spark.reduce((m, [, v]) => Math.max(m, v), 0)

  return (
    <article
      aria-labelledby={headingId}
      className="bg-card hover:bg-accent/40 flex flex-col gap-3 border p-3 transition-colors sm:flex-row sm:items-center"
    >
      <div className="flex min-w-0 flex-1 gap-3">
        <PixelIcon kind="server" state={iconState(status.state)} size={40} className="mt-0.5" />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h2 id={headingId} className="min-w-0 font-semibold break-words">
              <Link
                to="/servers/$id"
                params={{ id: String(server.id) }}
                className="focus-visible:ring-ring outline-none hover:underline focus-visible:ring-2"
              >
                {server.name}
              </Link>
            </h2>
            <StatusBadge state={status.state} />
            {instance ? <AddressLine address={server.address} /> : null}
            {metrics && instance ? (
              <span className="flex items-center gap-2 sm:ml-auto">
                <Users aria-hidden="true" className="text-muted-foreground size-3.5" />
                <span className="font-medium tabular-nums">
                  {metrics.players === null
                    ? missing
                    : t('monitoring.players_value', { players: metrics.players, max: metrics.players_max ?? missing })}
                </span>
                {reach ? (
                  <>
                    <PingBars level={reach === 'reachable' ? 5 : 0} label={t(`monitoring.reachability.${reach}`)} />
                    <span aria-hidden="true" className={cn('text-xs', reach === 'unreachable' ? 'text-destructive' : 'text-muted-foreground')}>
                      {t(`monitoring.reachability.${reach}`)}
                    </span>
                  </>
                ) : null}
              </span>
            ) : null}
          </div>
          <p data-testid="server-metrics" className="text-muted-foreground flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm tabular-nums">
            {instance ? (
              <Fact className="text-foreground">{describeInstance(instance, t)}</Fact>
            ) : (
              <Fact>{t('servers.not_configured_hint')}</Fact>
            )}
            {instance && metrics ? (
              <>
                <Fact label={t('monitoring.tps')} className="text-mc-b">
                  {metrics.tps === null ? missing : formatDecimal(metrics.tps, fmt.locale)}
                </Fact>
                <Fact label={t('monitoring.cpu')}>
                  {metrics.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(metrics.cpu_percent, fmt.locale) })}
                </Fact>
              </>
            ) : null}
            {showMemory ? (
              <Fact>
                {t('servers.memory_usage', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })}
              </Fact>
            ) : null}
            {status.restart_required ? <Fact className="text-mc-6">{t('servers.restart_required')}</Fact> : null}
            {metrics?.alerts.map((kind) => (
              <Fact key={kind} className="text-destructive">
                {t(alertKey(kind))}
              </Fact>
            ))}
          </p>
          {instance && !canPower ? <p className="text-muted-foreground text-xs">{t('servers.no_power')}</p> : null}
        </div>
      </div>
      {spark.length >= 2 ? (
        <div className="hidden w-40 shrink-0 lg:block">
          <Sparkline points={spark} spanMs={3_600_000} label={t('monitoring.sparkline', { current: spark[spark.length - 1][1], max: sparkMax })} />
        </div>
      ) : null}
      {instance && canPower ? (
        <div className="shrink-0 self-end sm:self-center">
          <PowerButtons server={server} compact />
        </div>
      ) : null}
    </article>
  )
}
