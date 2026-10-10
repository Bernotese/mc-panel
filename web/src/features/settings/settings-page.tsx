import { useQuery } from '@tanstack/react-query'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { serverSettingsQueryOptions } from '@/features/servers/api'
import { errorMessage } from '@/lib/error-message'
import { BaseDomainCard } from './base-domain-card'
import { CurseForgeCard } from './curseforge-card'
import { InstanceStatusCard } from './instance-status-card'

export function SettingsPage() {
  const { t } = useTranslation()
  const headingId = useId()
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
      <div className="grid gap-4 lg:grid-cols-2">
        <BaseDomainCard settings={settings.data} />
        <CurseForgeCard settings={settings.data} />
        <InstanceStatusCard settings={settings.data} />
      </div>
    )
  }

  return (
    <>
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      <section aria-labelledby={headingId} className="space-y-4">
        <h2 id={headingId} className="text-lg font-semibold">
          {t('settings.minecraft.title')}
        </h2>
        {content}
      </section>
    </>
  )
}
