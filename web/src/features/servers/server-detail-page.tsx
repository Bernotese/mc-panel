import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { PageHeader } from '@/components/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMe } from '@/features/auth/me'
import { errorMessage } from '@/lib/error-message'
import { serverQueryOptions } from './api'
import { DeleteServerSection } from './delete-server-section'
import { describeInstance } from './format'
import { ServerOverview } from './server-overview'
import { ServerSettingsTab } from './server-settings-tab'
import { detailPollInterval } from './status'
import { StatusBadge } from './status-badge'

export function ServerDetailPage({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const query = useQuery({
    ...serverQueryOptions(serverId),
    // Fast while the server changes state; no polling at all without data or after 403/404.
    refetchInterval: (q) => detailPollInterval(q.state.data, q.state.error),
  })

  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  if (!query.data) {
    let message = errorMessage(query.error, t)
    if (isApiError(query.error, 'not_found')) message = t('servers.detail.not_found')
    else if (isApiError(query.error, 'forbidden')) message = t('servers.detail.forbidden')
    return (
      <div className="space-y-3">
        <p role="alert" className="text-destructive">
          {message}
        </p>
        <Link to="/" className="text-primary underline underline-offset-4">
          {t('not_found.home')}
        </Link>
      </div>
    )
  }

  const server = query.data
  const header = (
    <PageHeader
      title={server.name}
      description={server.instance ? describeInstance(server.instance, t) : undefined}
      actions={<StatusBadge state={server.status.state} />}
    />
  )

  if (server.instance === null) {
    return (
      <>
        {header}
        <div className="max-w-3xl space-y-4">
          <p className="text-muted-foreground">{t('servers.detail.not_configured')}</p>
          {isAdmin ? <DeleteServerSection server={server} /> : null}
        </div>
      </>
    )
  }

  const instance = server.instance
  return (
    <>
      {header}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">{t('servers.detail.tabs.overview')}</TabsTrigger>
          {isAdmin ? <TabsTrigger value="settings">{t('servers.detail.tabs.settings')}</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="delete">{t('servers.detail.tabs.delete')}</TabsTrigger> : null}
        </TabsList>
        <TabsContent value="overview" className="pt-4">
          <ServerOverview server={server} instance={instance} />
        </TabsContent>
        {isAdmin ? (
          <TabsContent value="settings" className="pt-4">
            <ServerSettingsTab server={server} instance={instance} />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="delete" className="pt-4">
            <DeleteServerSection server={server} />
          </TabsContent>
        ) : null}
      </Tabs>
    </>
  )
}
