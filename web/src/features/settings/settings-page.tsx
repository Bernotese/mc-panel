import { useQuery } from '@tanstack/react-query'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { serverSettingsQueryOptions } from '@/features/servers/api'
import { errorMessage } from '@/lib/error-message'
import { BackupTimezoneCard } from './backup-timezone-card'
import { BaseDomainCard } from './base-domain-card'
import { CurseForgeCard } from './curseforge-card'
import { InstanceStatusCard } from './instance-status-card'
import { MonitoringCard } from './monitoring-card'

export function SettingsPage() {
  const { t } = useTranslation()
  const headingId = useId()
  const monitoringHeadingId = useId()
  const settings = useQuery(serverSettingsQueryOptions)

  let content: ReactNode
  if (settings.isPending) {
    content = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (settings.isError) {
    content = (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  } else {
    content = (
      <div className="flex flex-col gap-6">
        <BaseDomainCard settings={settings.data} />
        <CurseForgeCard settings={settings.data} />
        <BackupTimezoneCard settings={settings.data} />
        <InstanceStatusCard settings={settings.data} />
      </div>
    )
  }

  return (
    <>
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      <div className="flex max-w-3xl flex-col gap-10">
        <section aria-labelledby={headingId} className="space-y-4">
          <h2 id={headingId} className="pixel-heading text-muted-foreground text-sm uppercase">
            {t('settings.minecraft.title')}
          </h2>
          {content}
        </section>
        {/* Loads on its own: a failure of the server settings does not hide it. */}
        <section aria-labelledby={monitoringHeadingId} className="space-y-4">
          <h2 id={monitoringHeadingId} className="pixel-heading text-muted-foreground text-sm uppercase">
            {t('settings.monitoring_section.title')}
          </h2>
          <MonitoringCard />
        </section>
      </div>
    </>
  )
}
