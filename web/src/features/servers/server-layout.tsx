import { useQuery } from '@tanstack/react-query'
import { Link, Outlet } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { useMe } from '@/features/auth/me'
import { isExtracting, useFileJob } from '@/features/files/use-file-job'
import { errorMessage } from '@/lib/error-message'
import { serverQueryOptions } from './api'
import { serverSections } from './sections'
import { ServerContextProvider } from './server-context'
import { ServerHeader } from './server-header'
import { detailPollInterval } from './status'

/** Keyed by id in the route: switching servers must reset the header and section state. */
export function ServerLayout({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const query = useQuery({
    ...serverQueryOptions(serverId),
    // Fast while the server changes state; no polling at all without data or after 403/404.
    refetchInterval: (q) => detailPollInterval(q.state.data, q.state.error),
  })
  // Users with server.files see the lock; everyone else gets the panel's 409 "extracting" on start.
  const filesAllowed = query.data?.permissions.includes('server.files') === true && query.data.instance !== null
  const fileJob = useFileJob(serverId, filesAllowed)

  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  // A failed refetch keeps the old data: after 403/404 the server is gone or hidden, so stale data must not look live.
  const gone = isApiError(query.error, 'not_found') || isApiError(query.error, 'forbidden')
  if (!query.data || gone) {
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
  const value = { server, isAdmin, extracting: isExtracting(fileJob.data), sections: serverSections(server, isAdmin) }
  return (
    <ServerContextProvider value={value}>
      <div className="flex min-w-0 flex-col gap-6">
        <ServerHeader />
        <Outlet />
      </div>
    </ServerContextProvider>
  )
}
