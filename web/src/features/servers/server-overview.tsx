import { useTranslation } from 'react-i18next'
import type { InstanceConfig, Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { useFormat } from '@/lib/format'
import { AddressLine } from './address-line'
import { describeInstance, formatCpu, formatGiB, javaLabel } from './format'
import { PowerButtons } from './power-buttons'
import { StatusBadge } from './status-badge'
import { usePower } from './use-power'

export function ServerOverview({ server, instance }: { server: Server; instance: InstanceConfig }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const { status } = server
  const canPower = server.permissions.includes('server.power')
  const running = status.state === 'running'
  const showResources = running && status.memory_limit_bytes > 0

  return (
    <div className="space-y-4">
      {status.restart_required ? <RestartBanner server={server} canPower={canPower} /> : null}
      {status.state === 'unknown' ? <p className="text-muted-foreground text-sm">{t('servers.overview.unknown_hint')}</p> : null}
      <SectionCard title={t('servers.overview.title')}>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('servers.overview.status')}</dt>
          <dd className="flex flex-wrap items-center gap-2">
            <StatusBadge state={status.state} />
            {status.operation ? <span className="text-muted-foreground">{t(`servers.operation.${status.operation}`)}</span> : null}
          </dd>
          <dt className="text-muted-foreground">{t('servers.overview.address')}</dt>
          <dd className="min-w-0">
            <AddressLine address={server.address} />
          </dd>
          <dt className="text-muted-foreground">{t('servers.overview.type')}</dt>
          <dd>{describeInstance(instance, t)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.java')}</dt>
          <dd>{javaLabel(instance.java)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.memory')}</dt>
          <dd className="tabular-nums">
            {showResources
              ? t('servers.memory_value', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })
              : '—'}
          </dd>
          <dt className="text-muted-foreground">{t('servers.overview.cpu')}</dt>
          <dd className="tabular-nums">{showResources ? t('servers.cpu_value', { value: formatCpu(status.cpu_percent, fmt.locale) }) : '—'}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.started')}</dt>
          <dd>{running && status.started_at > 0 ? fmt.relative(status.started_at) : '—'}</dd>
        </dl>
        {status.last_error ? (
          <div role="alert" className="border-destructive/50 mt-4 space-y-1 rounded-md border p-3 text-sm">
            <p className="font-medium">{t('servers.overview.last_error')}</p>
            <p className="font-mono break-words">{status.last_error}</p>
          </div>
        ) : null}
      </SectionCard>
      <SectionCard title={t('servers.overview.controls')}>
        {canPower ? <PowerButtons server={server} /> : <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p>}
      </SectionCard>
    </div>
  )
}

function RestartBanner({ server, canPower }: { server: Server; canPower: boolean }) {
  const { t } = useTranslation()
  const power = usePower(server)
  return (
    <div role="status" className="flex flex-col gap-3 rounded-md border p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="space-y-1">
        <p className="font-medium">{t('servers.restart_required')}</p>
        <p className="text-muted-foreground text-sm">{t('servers.restart.description')}</p>
      </div>
      {canPower ? (
        <Button type="button" disabled={power.isPending} onClick={() => power.mutate('restart')}>
          {t('servers.restart.now')}
        </Button>
      ) : null}
    </div>
  )
}
