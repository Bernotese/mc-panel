import { useTranslation } from 'react-i18next'
import { DeletionList } from '@/features/servers/deletions'
import { sortDeletions } from '@/features/servers/deletions-model'
import { useServerDeletions } from '@/features/servers/use-server-deletions'
import { errorMessage } from '@/lib/error-message'
import { SettingsCard } from './settings-card'

/** Deleted servers of the last 7 days (#148): what was cleaned up, what failed and is retried, with a retry button. */
export function DeletionsCard() {
  const { t } = useTranslation()
  const deletions = useServerDeletions(true)
  let body
  if (deletions.isPending) body = <p className="text-muted-foreground">{t('common.loading')}</p>
  else if (deletions.isError)
    body = (
      <p role="alert" className="text-destructive">
        {errorMessage(deletions.error, t)}
      </p>
    )
  else if (deletions.data.length === 0) body = <p className="text-muted-foreground">{t('servers.deletions.none')}</p>
  else body = <DeletionList deletions={sortDeletions(deletions.data)} />
  return (
    <SettingsCard title={t('servers.deletions.all_title')} description={t('servers.deletions.all_description')}>
      {body}
    </SettingsCard>
  )
}
