import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { Section } from '@/components/section'
import { StaleNotice } from '@/components/stale-notice'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { withDevsAfterSources } from '@/features/dev/model'
import { monitoringOverviewQueryOptions, OVERVIEW_POLL_MS } from '@/features/monitoring/api'
import { HostCard } from '@/features/monitoring/host-card'
import { serverSettingsQueryOptions, serversQueryOptions } from '@/features/servers/api'
import { pollInterval } from '@/features/servers/status'
import { errorMessage } from '@/lib/error-message'
import { freshnessOf, isStale } from '@/lib/freshness'
import { ServerRow } from './server-row'

export function DashboardPage() {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  // 3 s while a server is changing state, 15 s otherwise (spec §9).
  const servers = useQuery({ ...serversQueryOptions, refetchInterval: (query) => pollInterval(query.state.data) })
  // Only admins may read the instance settings; without a base domain no server can be created.
  const settings = useQuery({ ...serverSettingsQueryOptions, enabled: isAdmin })
  const baseDomainMissing = isAdmin && settings.data?.base_domain === null
  // Monitoring values are an extra: a failed overview leaves the cards as they are (spec §7.1).
  const monitoring = useQuery({ ...monitoringOverviewQueryOptions, refetchInterval: OVERVIEW_POLL_MS })
  const metricsById = new Map((monitoring.data?.servers ?? []).map((m) => [m.id, m]))
  const host = isAdmin ? (monitoring.data?.host ?? null) : null
  // A failed refresh keeps what was loaded and marks it stale (issue #155); only a list that never loaded is an error.
  const serversStale = isStale(servers)
  const metricsStale = isStale(monitoring)
  const freshness = freshnessOf(servers, monitoring)

  let content: ReactNode
  if (servers.isPending) {
    content = <p className="text-muted-foreground">{t('common.loading')}</p>
  } else if (servers.data === undefined) {
    content = (
      <p role="alert" className="text-destructive">
        {errorMessage(servers.error, t)}
      </p>
    )
  } else if (servers.data.length === 0) {
    content = <p className="text-muted-foreground">{t('dashboard.empty')}</p>
  } else {
    content = (
      <ul aria-label={t('dashboard.list_label')} className="space-y-2">
        {withDevsAfterSources(servers.data).map((server) => (
          <li key={server.id}>
            <ServerRow server={server} metrics={metricsById.get(server.id)} stale={serversStale} metricsStale={metricsStale} />
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
      <div className="space-y-6">
        {baseDomainMissing ? (
          <Section title={t('dashboard.base_domain_missing_title')} tone="danger">
            <div role="status" className="space-y-2 text-sm">
              <p className="text-muted-foreground">{t('dashboard.base_domain_missing')}</p>
              <Link to="/settings" className="text-primary inline-block underline underline-offset-4">
                {t('settings.link')}
              </Link>
            </div>
          </Section>
        ) : null}
        <StaleNotice freshness={freshness} />
        {host ? <HostCard host={host} stale={metricsStale} /> : null}
        {content}
      </div>
    </>
  )
}
