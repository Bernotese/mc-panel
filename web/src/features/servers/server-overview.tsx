import { useTranslation } from 'react-i18next'
import type { InstanceConfig, Server } from '@/api/types'
import { Section } from '@/components/section'
import { OnlinePlayersCard } from '@/features/players/online-players-card'
import { useFormat } from '@/lib/format'
import { describeInstance, formatGiB, javaLabel } from './format'

/** Spec §4.4: status, address, power and resources live in the server header; the overview keeps the details and online players. */
export function ServerOverview({ server, instance }: { server: Server; instance: InstanceConfig; extracting?: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const { status } = server
  const running = status.state === 'running'
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      {status.state === 'unknown' ? <p className="text-muted-foreground text-sm xl:col-span-2">{t('servers.overview.unknown_hint')}</p> : null}
      <Section title={t('servers.overview.title')}>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('servers.overview.type')}</dt>
          <dd>{describeInstance(instance, t)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.java')}</dt>
          <dd>{javaLabel(instance.java)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.memory_assigned')}</dt>
          <dd className="tabular-nums">{formatGiB(instance.memory_mib * 1024 ** 2, fmt.locale)} GiB</dd>
          <dt className="text-muted-foreground">{t('servers.overview.started')}</dt>
          <dd>{running && status.started_at > 0 ? fmt.relative(status.started_at) : '–'}</dd>
          {status.operation ? (
            <>
              <dt className="text-muted-foreground">{t('servers.overview.status')}</dt>
              <dd>{t(`servers.operation.${status.operation}`)}</dd>
            </>
          ) : null}
        </dl>
      </Section>
      <OnlinePlayersCard serverId={server.id} />
      {!server.permissions.includes('server.power') ? <p className="text-muted-foreground text-sm xl:col-span-2">{t('servers.no_power')}</p> : null}
    </div>
  )
}
