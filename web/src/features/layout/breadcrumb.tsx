import { useQuery } from '@tanstack/react-query'
import { Link, useRouterState } from '@tanstack/react-router'
import { Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { DevBadge } from '@/features/dev/dev-badge'
import { serverQueryOptions } from '@/features/servers/api'
import { sectionFromPath, sectionLabelKey, serverIdFromPath } from '@/features/servers/sections'
import { NAV_ITEMS } from './nav'

interface Crumb {
  label: string
  /** Only server crumbs link (back to its overview); the last crumb is the current page. */
  serverId?: number
  /** A dev's crumb carries the DEV badge (with the source name as tooltip). */
  devOf?: string
}

export function Breadcrumb() {
  const { t } = useTranslation()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const serverId = serverIdFromPath(pathname)
  // Same key as the server layout: served from its cache, no extra request.
  const server = useQuery({ ...serverQueryOptions(serverId ?? 0), enabled: serverId !== null }).data
  const crumbs: Crumb[] = []
  if (serverId !== null) {
    // A dev sits below its prod, like in the tree (dev instances spec §8).
    if (server?.dev) crumbs.push({ label: server.dev.source_name, serverId: server.dev.source_id })
    crumbs.push({ label: server?.name ?? `#${serverId}`, serverId, devOf: server?.dev?.source_name })
    if (server) crumbs.push({ label: t(sectionLabelKey(sectionFromPath(pathname), server)) })
  } else if (pathname === '/servers/new') {
    crumbs.push({ label: t('nav.dashboard') }, { label: t('nav.new_server') })
  } else {
    const item = NAV_ITEMS.find((i) => i.to !== '/' && (pathname === i.to || pathname.startsWith(`${i.to}/`)))
    crumbs.push({ label: t(item ? item.labelKey : 'nav.dashboard') })
  }
  return (
    <nav aria-label={t('nav.breadcrumb')} className="min-w-0 truncate font-mono text-xs">
      {crumbs.map((c, i) => (
        <Fragment key={`${i}-${c.label}`}>
          {i > 0 ? (
            <span aria-hidden="true" className="text-muted-foreground px-1.5">
              /
            </span>
          ) : null}
          {c.serverId !== undefined && i < crumbs.length - 1 ? (
            <Link to="/servers/$id" params={{ id: String(c.serverId) }} className="text-muted-foreground hover:text-foreground">
              {c.label}
            </Link>
          ) : (
            <span aria-current={i === crumbs.length - 1 ? 'page' : undefined} className="text-foreground">
              {c.label}
            </span>
          )}
          {c.devOf !== undefined ? <DevBadge source={c.devOf} className="ml-1.5 align-middle" /> : null}
        </Fragment>
      ))}
    </nav>
  )
}
