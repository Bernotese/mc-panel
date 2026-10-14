import { Link, useRouterState } from '@tanstack/react-router'
import { LogOut, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { useLogout } from '@/features/auth/use-logout'
import { cn } from '@/lib/utils'
import { navItemsFor, type NavItem } from './nav'

// Hover styles only apply to inactive items: as plain classes they fought the active ones (white box on hover in light).
const ITEM =
  'text-muted-foreground hover:not-data-[status=active]:bg-sidebar-accent hover:not-data-[status=active]:text-foreground focus-visible:ring-ring flex size-10 items-center justify-center outline-none focus-visible:ring-2 data-[status=active]:bg-primary data-[status=active]:text-primary-foreground data-[status=active]:bevel-primary'

function RailLink({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  const { t } = useTranslation()
  const Icon = item.icon
  const label = t(item.labelKey)
  // Server pages belong to the "Server" area: highlight it there too (aria-current stays with the exact page).
  const inServers = useRouterState({ select: (s) => item.to === '/' && s.location.pathname.startsWith('/servers/') })
  return (
    <li>
      <Link
        to={item.to}
        aria-label={label}
        title={label}
        // Exact for "/": every path starts with it.
        activeOptions={{ exact: item.to === '/', includeSearch: false }}
        activeProps={{ 'aria-current': 'page' }}
        onClick={onNavigate}
        className={cn(ITEM, inServers && 'text-primary bg-sidebar-accent bevel')}
      >
        <Icon className="size-5" aria-hidden="true" />
      </Link>
    </li>
  )
}

/** Spec §4.1: 56 px icon rail; labels are aria-labels and tooltips (tests and e2e click them by name). */
export function IconRail({ treeOpen, onToggleTree, onNavigate }: { treeOpen?: boolean; onToggleTree?: () => void; onNavigate?: () => void }) {
  const { t } = useTranslation()
  const me = useMe()
  const logout = useLogout()
  const role = me?.role ?? 'user'
  const toggleLabel = treeOpen ? t('nav.tree_collapse') : t('nav.tree_expand')
  return (
    <div className="flex h-full w-14 shrink-0 flex-col items-center gap-1 border-r-2 border-[var(--sidebar-border)] bg-[var(--rail)] py-3">
      <nav aria-label={t('nav.label')} className="flex min-h-0 flex-1 flex-col items-center">
        <ul className="flex flex-col items-center gap-1.5">
          <li>
            {/* The brand link to the home page; the router marks it current there like the "Server" entry (same page). */}
            <Link
              to="/"
              aria-label={t('nav.home')}
              title={t('nav.home')}
              onClick={onNavigate}
              className="focus-visible:ring-ring mb-3 block p-1 outline-none hover:brightness-110 focus-visible:ring-2"
            >
              <PixelIcon kind="grass" size={30} />
            </Link>
          </li>
          {navItemsFor(role, 'top').map((item) => (
            <RailLink key={item.to} item={item} onNavigate={onNavigate} />
          ))}
        </ul>
        <ul className="mt-auto flex flex-col items-center gap-1.5 pb-1">
          {navItemsFor(role, 'bottom').map((item) => (
            <RailLink key={item.to} item={item} onNavigate={onNavigate} />
          ))}
        </ul>
      </nav>
      {onToggleTree ? (
        <Button type="button" variant="ghost" size="icon" aria-label={toggleLabel} title={toggleLabel} onClick={onToggleTree} className="text-muted-foreground">
          {treeOpen ? <PanelLeftClose aria-hidden="true" /> : <PanelLeftOpen aria-hidden="true" />}
        </Button>
      ) : null}
      <ThemeToggle />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={t('auth.logout')}
        title={t('auth.logout')}
        disabled={logout.isPending}
        onClick={() => logout.mutate()}
        className="text-muted-foreground hover:text-status-error"
      >
        <LogOut aria-hidden="true" />
      </Button>
    </div>
  )
}
