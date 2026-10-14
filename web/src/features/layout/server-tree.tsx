import { useQuery } from '@tanstack/react-query'
import { Link, useRouterState } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useMe } from '@/features/auth/me'
import { serversQueryOptions } from '@/features/servers/api'
import { sectionForSwitch, sectionFromPath, sectionHref, sectionLabelKey, serverIdFromPath, serverSections } from '@/features/servers/sections'
import { pollInterval, stateDotClass, stateLabelKey } from '@/features/servers/status'
import { cn } from '@/lib/utils'

const ROW =
  'hover:bg-sidebar-accent focus-visible:ring-ring flex min-w-0 items-center gap-2 px-2 py-1.5 text-sm outline-none focus-visible:ring-2 focus-visible:ring-inset'
const ACTIVE = 'bg-sidebar-accent text-sidebar-accent-foreground font-medium shadow-[inset_3px_0_0_var(--primary)]'

/** Spec §4.1: every visible server; the open one is expanded with its sections. Shares the dashboard's servers query. */
export function ServerTree({ onNavigate, className }: { onNavigate?: () => void; className?: string }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const servers = useQuery({ ...serversQueryOptions, refetchInterval: (q) => pollInterval(q.state.data) })
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const activeId = serverIdFromPath(pathname)
  const section = sectionFromPath(pathname)

  return (
    <nav
      aria-label={t('nav.servers')}
      className={cn('bg-sidebar text-sidebar-foreground flex h-full w-56 shrink-0 flex-col gap-1 overflow-y-auto border-r-2 border-[var(--sidebar-border)] p-2', className)}
    >
      <p aria-hidden="true" className="pixel-heading text-muted-foreground px-2 pt-1.5 pb-2 text-xs tracking-widest uppercase">
        {t('nav.servers')}
      </p>
      {servers.data && servers.data.length === 0 ? <p className="text-muted-foreground px-2 text-sm">{t('nav.no_servers')}</p> : null}
      <ul className="flex flex-col gap-0.5">
        {(servers.data ?? []).map((server) => {
          const active = server.id === activeId
          // Switching servers keeps the open section when the target allows it (spec §4.3).
          const target = active ? 'overview' : sectionForSwitch(activeId === null ? 'overview' : section, server, isAdmin)
          return (
            <li key={server.id}>
              <Link
                to={sectionHref(target)}
                params={{ id: String(server.id) }}
                // Exact: the router marks fuzzy matches as aria-current, but /servers/1 must not be "current" on /servers/1/console.
                activeOptions={{ exact: true, includeSearch: false }}
                aria-current={active && section === 'overview' ? 'page' : undefined}
                onClick={onNavigate}
                className={cn(ROW, active && ACTIVE)}
              >
                {/* Status only as tooltip: the link name must stay exactly the server name (tests and e2e select by it). */}
                <span aria-hidden="true" title={t(stateLabelKey(server.status.state))} className={cn('size-2 shrink-0', stateDotClass(server.status.state))} />
                <span className="truncate">{server.name}</span>
              </Link>
              {active ? (
                <ul aria-label={t('nav.server_sections', { name: server.name })} className="border-border mt-0.5 mb-1.5 ml-3 flex flex-col border-l-2 pl-1.5">
                  {serverSections(server, isAdmin).map((s) => (
                    <li key={s}>
                      <Link
                        to={sectionHref(s)}
                        params={{ id: String(server.id) }}
                        activeOptions={{ exact: true, includeSearch: false }}
                        aria-current={s === section ? 'page' : undefined}
                        onClick={onNavigate}
                        className={cn(ROW, 'py-1', s === section ? 'text-primary font-semibold' : 'text-muted-foreground hover:text-foreground')}
                      >
                        {t(sectionLabelKey(s, server))}
                      </Link>
                    </li>
                  ))}
                </ul>
              ) : null}
            </li>
          )
        })}
      </ul>
      {isAdmin ? (
        <Link
          to="/servers/new"
          onClick={onNavigate}
          activeProps={{ 'aria-current': 'page', className: ACTIVE }}
          className={cn(ROW, 'text-primary mt-2 border border-dashed border-[var(--sidebar-border)]')}
        >
          <Plus className="size-4" aria-hidden="true" />
          {t('nav.new_server')}
        </Link>
      ) : null}
    </nav>
  )
}
