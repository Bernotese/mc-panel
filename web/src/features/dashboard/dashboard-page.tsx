import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { serverSettingsQueryOptions, serversQueryOptions } from '@/features/servers/api'
import { pollInterval } from '@/features/servers/status'
import { errorMessage } from '@/lib/error-message'
import { ServerCard } from './server-card'

export function DashboardPage() {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  // 3 s while a server is changing state, 15 s otherwise (spec §9).
  const servers = useQuery({ ...serversQueryOptions, refetchInterval: (query) => pollInterval(query.state.data) })
  // Only admins may read the instance settings; without a base domain no server can be created.
  const settings = useQuery({ ...serverSettingsQueryOptions, enabled: isAdmin })
  const baseDomainMissing = isAdmin && settings.data?.base_domain === null

  let content: ReactNode
  if (servers.isPending) {
    content = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (servers.isError) {
    content = (
      <p role="alert" className="text-destructive">
        {errorMessage(servers.error, t)}
      </p>
    )
  } else if (servers.data.length === 0) {
    content = <p className="text-muted-foreground">{t('dashboard.empty')}</p>
  } else {
    content = (
      <ul className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {servers.data.map((server) => (
          <li key={server.id}>
            <ServerCard server={server} />
          </li>
        ))}
      </ul>
    )
  }

  let createAction: ReactNode
  if (isAdmin) {
    createAction =
      settings.isPending || baseDomainMissing ? (
        <Button type="button" disabled>
          {t('dashboard.create_server')}
        </Button>
      ) : (
        <Button asChild>
          <Link to="/servers/new">{t('dashboard.create_server')}</Link>
        </Button>
      )
  }

  return (
    <>
      <PageHeader title={t('dashboard.title')} description={t('dashboard.description')} actions={createAction} />
      {baseDomainMissing ? (
        <div role="status" className="mb-6 rounded-md border p-4 text-sm">
          <p className="font-medium">{t('dashboard.base_domain_missing_title')}</p>
          <p className="text-muted-foreground mt-1">{t('dashboard.base_domain_missing')}</p>
        </div>
      ) : null}
      {content}
    </>
  )
}
