import { useTranslation } from 'react-i18next'
import type { ServerSettings } from '@/api/types'
import { SettingsCard } from './settings-card'

/** docker "unknown" means the panel got no answer from the agent at all. */
export function InstanceStatusCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const agentReachable = settings.docker !== 'unknown'
  return (
    <SettingsCard title={t('settings.status.title')}>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        <dt className="text-muted-foreground">{t('settings.status.agent')}</dt>
        <dd>{agentReachable ? t('settings.status.reachable') : t('settings.status.unreachable')}</dd>
        <dt className="text-muted-foreground">{t('settings.status.docker')}</dt>
        <dd>{t(`settings.docker.${settings.docker}`)}</dd>
      </dl>
      {settings.docker === 'unavailable' ? <p className="text-muted-foreground">{t('settings.status.unavailable_hint')}</p> : null}
      {settings.docker === 'unknown' ? <p className="text-muted-foreground">{t('settings.status.unknown_hint')}</p> : null}
      <div className="space-y-2">
        <h4 className="font-medium">{t('settings.status.unknown_containers')}</h4>
        {settings.unknown_containers.length === 0 ? (
          <p className="text-muted-foreground">{t('settings.status.unknown_containers_none')}</p>
        ) : (
          <>
            <ul className="list-disc space-y-1 pl-5 font-mono">
              {settings.unknown_containers.map((c) => (
                <li key={c.id}>{t('settings.status.container', { name: c.name, id: c.id })}</li>
              ))}
            </ul>
            <p className="text-muted-foreground">{t('settings.status.unknown_containers_hint')}</p>
          </>
        )}
      </div>
    </SettingsCard>
  )
}
