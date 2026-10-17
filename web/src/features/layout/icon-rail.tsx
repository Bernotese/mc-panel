import { Link, useRouterState } from '@tanstack/react-router'
import { ChevronsLeft, ChevronsRight, LogOut, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { RunningSourceLink } from '@/components/source-link'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { useLogout } from '@/features/auth/use-logout'
import { cn } from '@/lib/utils'
import { navItemsFor, type NavItem } from './nav'

// Hover styles only apply to inactive items: as plain classes they fought the active ones (white box on hover in light).
const ITEM =
  'text-muted-foreground hover:not-data-[status=active]:bg-sidebar-accent hover:not-data-[status=active]:text-foreground focus-visible:ring-ring flex h-10 items-center outline-none focus-visible:ring-2 data-[status=active]:bg-primary data-[status=active]:text-primary-foreground data-[status=active]:bevel-primary'

function RailLink({ item, expanded, onNavigate }: { item: NavItem; expanded: boolean; onNavigate?: () => void }) {
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
        // The tooltip only helps while the label is hidden.
        title={expanded ? undefined : label}
        // Exact for "/": every path starts with it.
        activeOptions={{ exact: item.to === '/', includeSearch: false }}
        activeProps={{ 'aria-current': 'page' }}
        onClick={onNavigate}
        className={cn(ITEM, expanded ? 'w-full gap-3 px-2.5 text-sm font-medium' : 'w-10 justify-center', inServers && 'text-primary bg-sidebar-accent bevel')}
      >
        <Icon className="size-5 shrink-0" aria-hidden="true" />
        {expanded ? <span className="truncate">{label}</span> : null}
      </Link>
    </li>
  )
}

/**
 * Spec §4.1: 56 px icon rail; labels are aria-labels and tooltips (tests and e2e click them by name).
 * On desktop it can be expanded (`onToggleRail`) to show each label next to its icon.
 */
export function IconRail({
  treeOpen,
  onToggleTree,
  expanded = false,
  onToggleRail,
  onNavigate,
}: {
  treeOpen?: boolean
  onToggleTree?: () => void
  expanded?: boolean
  onToggleRail?: () => void
  onNavigate?: () => void
}) {
  const { t } = useTranslation()
  const navId = useId()
  const me = useMe()
  const logout = useLogout()
  const role = me?.role ?? 'user'
  const toggleLabel = treeOpen ? t('nav.tree_collapse') : t('nav.tree_expand')
  const railLabel = expanded ? t('nav.rail_collapse') : t('nav.rail_expand')
  return (
    <div
      data-expanded={expanded}
      className={cn(
        'flex h-full shrink-0 flex-col gap-1 border-r-2 border-[var(--sidebar-border)] bg-[var(--rail)] py-3',
        expanded ? 'w-52 px-2' : 'w-14 items-center',
      )}
    >
      <nav id={navId} aria-label={t('nav.label')} className={cn('flex min-h-0 w-full flex-1 flex-col', !expanded && 'items-center')}>
        <ul className={cn('flex flex-col gap-1.5', expanded ? 'items-stretch' : 'items-center')}>
          <li>
            {/* The brand link to the home page; the router marks it current there like the "Server" entry (same page). */}
            <Link
              to="/"
              aria-label={t('nav.home')}
              title={t('nav.home')}
              onClick={onNavigate}
              className={cn('focus-visible:ring-ring mb-3 block w-fit p-1 outline-none hover:brightness-110 focus-visible:ring-2', expanded && 'ml-1.5')}
            >
              <PixelIcon kind="grass" size={30} />
            </Link>
          </li>
          {navItemsFor(role, 'top').map((item) => (
            <RailLink key={item.to} item={item} expanded={expanded} onNavigate={onNavigate} />
          ))}
        </ul>
        <ul className={cn('mt-auto flex flex-col gap-1.5 pb-1', expanded ? 'items-stretch' : 'items-center')}>
          {navItemsFor(role, 'bottom').map((item) => (
            <RailLink key={item.to} item={item} expanded={expanded} onNavigate={onNavigate} />
          ))}
        </ul>
      </nav>
      {/* Expanded, the tools sit in one row below the labelled links; collapsed, they stack like the icons. */}
      <div
        className={cn('flex gap-1', expanded ? 'flex-row items-center justify-between border-t border-[var(--sidebar-border)] pt-2' : 'flex-col items-center')}
      >
        {onToggleRail ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={railLabel}
            title={railLabel}
            aria-expanded={expanded}
            aria-controls={navId}
            onClick={onToggleRail}
            className={cn('text-muted-foreground', expanded && 'order-last')}
          >
            {expanded ? <ChevronsLeft aria-hidden="true" /> : <ChevronsRight aria-hidden="true" />}
          </Button>
        ) : null}
        {onToggleTree ? (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={toggleLabel}
            title={toggleLabel}
            aria-expanded={treeOpen === true}
            onClick={onToggleTree}
            className="text-muted-foreground"
          >
            {treeOpen ? <PanelLeftClose aria-hidden="true" /> : <PanelLeftOpen aria-hidden="true" />}
          </Button>
        ) : null}
        <RunningSourceLink />
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
    </div>
  )
}
