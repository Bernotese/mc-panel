import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { StatSlot } from '@/components/stat-slot'
import { Button } from '@/components/ui/button'
import { DevBadge } from '@/features/dev/dev-badge'
import { DevBanner } from '@/features/dev/dev-banner'
import { monitoringOverviewQueryOptions, OVERVIEW_POLL_MS } from '@/features/monitoring/api'
import { formatDecimal } from '@/features/monitoring/model'
import { useFormat } from '@/lib/format'
import { AddressLine } from './address-line'
import { formatCpu, formatGiB } from './format'
import { PowerButtons } from './power-buttons'
import { useServerContext } from './server-context'
import { ServerStatusBadge } from './status-badge'
import { usePower } from './use-power'
import { WebMapLink } from './web-map-link'

function tpsTone(tps: number | null): 'default' | 'warn' | 'danger' {
  if (tps === null) return 'default'
  if (tps < 15) return 'danger'
  return tps < 18 ? 'warn' : 'default'
}

/** Spec §4.4: always visible above every section of a server. */
export function ServerHeader() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const { server, isAdmin, extracting } = useServerContext()
  const { status, instance } = server
  // Same query as the dashboard: one request serves both, and missing values stay null ("–"), never 0.
  const overview = useQuery({ ...monitoringOverviewQueryOptions, refetchInterval: OVERVIEW_POLL_MS, enabled: instance !== null })
  const metrics = overview.data?.servers.find((s) => s.id === server.id)
  const canPower = server.permissions.includes('server.power')
  const running = status.state === 'running' && status.memory_limit_bytes > 0

  const players =
    metrics && metrics.players !== null ? t('servers.header.players_value', { players: metrics.players, max: metrics.players_max ?? '–' }) : null
  const tpsValue = metrics?.tps ?? null
  const tps = tpsValue !== null ? formatDecimal(tpsValue, fmt.locale) : null
  const memory = running
    ? t('servers.header.memory_value', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })
    : null
  const cpu = running ? t('servers.cpu_value', { value: formatCpu(status.cpu_percent, fmt.locale) }) : null
  const iconState = status.state === 'running' ? 'online' : status.state === 'unknown' ? 'unknown' : 'offline'

  return (
    <section aria-labelledby={id} className="flex flex-col gap-6">
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div className="flex min-w-0 items-center gap-3">
          <PixelIcon kind="server" state={iconState} size={36} className="hidden sm:block" />
          <div className="flex min-w-0 flex-col gap-1.5">
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
              <h1 id={id} className="pixel-heading text-xl break-all md:text-2xl">
                {server.name}
              </h1>
              {server.dev ? <DevBadge source={server.dev.source_name} /> : null}
              <ServerStatusBadge server={server} />
            </div>
            {instance ? (
              <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1">
                <AddressLine address={server.address} />
                <WebMapLink server={server} />
              </div>
            ) : null}
          </div>
        </div>
        {instance && canPower ? (
          <div className="flex shrink-0 flex-col gap-1 md:items-end">
            <PowerButtons server={server} extracting={extracting} />
          </div>
        ) : null}
      </div>
      {server.dev ? <DevBanner server={server} dev={server.dev} /> : null}
      {instance ? (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4 lg:gap-6">
          <StatSlot label={t('servers.header.players')} value={players} />
          <StatSlot label={t('servers.header.tps')} value={tps} tone={tpsTone(tpsValue)} />
          <StatSlot label={t('servers.header.memory')} value={memory} bar={running ? status.memory_bytes / status.memory_limit_bytes : null} />
          <StatSlot label={t('servers.header.cpu')} value={cpu} />
        </div>
      ) : null}
      {status.restart_required ? <RestartBanner canPower={canPower} /> : null}
      {status.last_error && isAdmin ? (
        <div role="alert" className="border-status-error/60 bg-card space-y-1 border-2 p-3 text-sm">
          <p className="text-status-error font-medium">{t('servers.overview.last_error')}</p>
          <p className="font-mono break-words">{status.last_error}</p>
        </div>
      ) : status.state === 'error' ? (
        // Only admins get the error text from the API (it can name host paths and Docker internals).
        <div role="alert" className="border-status-error/60 bg-card border-2 p-3 text-sm">
          <p>{t('servers.overview.error_hidden')}</p>
        </div>
      ) : null}
    </section>
  )
}

function RestartBanner({ canPower }: { canPower: boolean }) {
  const { t } = useTranslation()
  const { server, extracting } = useServerContext()
  const power = usePower(server)
  return (
    <div role="status" className="bevel-inset flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="space-y-1">
        <p className="text-status-starting font-medium">{t('servers.restart_required')}</p>
        <p className="text-muted-foreground text-sm">{t('servers.restart.description')}</p>
      </div>
      {canPower ? (
        <Button type="button" disabled={power.isPending || extracting} onClick={() => power.mutate('restart')}>
          {t('servers.restart.now')}
        </Button>
      ) : null}
    </div>
  )
}
