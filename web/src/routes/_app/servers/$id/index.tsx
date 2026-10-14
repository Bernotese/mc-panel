import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { DeleteServerSection } from '@/features/servers/delete-server-section'
import { useServerContext } from '@/features/servers/server-context'
import { ServerOverview } from '@/features/servers/server-overview'

export const Route = createFileRoute('/_app/servers/$id/')({ component: OverviewRoute })

function OverviewRoute() {
  const { t } = useTranslation()
  const { server, isAdmin, extracting } = useServerContext()
  if (server.instance === null) {
    // Pre-instance servers can only be deleted (spec §4.3: their only section is the overview).
    return (
      <div className="max-w-3xl space-y-4">
        <p className="text-muted-foreground">{t('servers.detail.not_configured')}</p>
        {isAdmin ? <DeleteServerSection server={server} /> : null}
      </div>
    )
  }
  return <ServerOverview server={server} instance={server.instance} extracting={extracting} />
}
