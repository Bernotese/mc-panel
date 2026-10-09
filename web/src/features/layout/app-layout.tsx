import { Link, Outlet } from '@tanstack/react-router'
import { LogOut, Menu } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { useMe } from '@/features/auth/me'
import { useLogout } from '@/features/auth/use-logout'
import { DESKTOP_QUERY, useMediaQuery } from '@/lib/use-media-query'
import { navItemsFor, type NavItem } from './nav'

function NavLinks({ items, onNavigate }: { items: NavItem[]; onNavigate?: () => void }) {
  const { t } = useTranslation()
  return (
    <nav aria-label={t('nav.label')} className="p-2">
      <ul className="space-y-1">
        {items.map((item) => {
          const Icon = item.icon
          return (
            <li key={item.to}>
              <Link
                to={item.to}
                activeOptions={{ exact: item.to === '/' }}
                activeProps={{ 'aria-current': 'page', className: 'bg-accent text-accent-foreground' }}
                onClick={onNavigate}
                className="hover:bg-accent focus-visible:ring-ring flex items-center gap-3 rounded-md px-3 py-2 text-sm font-medium outline-none focus-visible:ring-2"
              >
                <Icon className="size-4" aria-hidden="true" />
                {t(item.labelKey)}
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

export function AppLayout() {
  const { t } = useTranslation()
  const me = useMe()
  const logout = useLogout()
  const desktop = useMediaQuery(DESKTOP_QUERY)
  const [menuOpen, setMenuOpen] = useState(false)
  const items = navItemsFor(me?.role ?? 'user')

  return (
    <div className="bg-background min-h-svh md:grid md:grid-cols-[15rem_minmax(0,1fr)]">
      <a
        href="#main"
        className="focus:bg-background sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-md focus:px-3 focus:py-2"
      >
        {t('nav.skip')}
      </a>
      {desktop ? (
        <aside className="bg-muted/30 sticky top-0 flex h-svh flex-col border-r">
          <div className="flex h-14 items-center px-4 font-semibold">{t('auth.brand')}</div>
          <NavLinks items={items} />
        </aside>
      ) : null}
      <div className="flex min-w-0 flex-col">
        <header className="bg-background/95 sticky top-0 z-10 flex h-14 items-center gap-2 border-b px-4 backdrop-blur">
          {!desktop ? (
            <>
              <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
                <SheetTrigger asChild>
                  <Button type="button" variant="ghost" size="icon" aria-label={t('nav.open_menu')}>
                    <Menu className="size-5" aria-hidden="true" />
                  </Button>
                </SheetTrigger>
                <SheetContent side="left" className="w-72 p-0">
                  <SheetHeader className="border-b px-4 py-3">
                    <SheetTitle>{t('auth.brand')}</SheetTitle>
                    <SheetDescription className="sr-only">{t('nav.menu_description')}</SheetDescription>
                  </SheetHeader>
                  <NavLinks items={items} onNavigate={() => setMenuOpen(false)} />
                </SheetContent>
              </Sheet>
              <span className="font-semibold">{t('auth.brand')}</span>
            </>
          ) : null}
          <div className="ml-auto flex items-center gap-1">
            {me ? <span className="text-muted-foreground hidden text-sm sm:inline">{t('nav.signed_in_as', { username: me.username })}</span> : null}
            <ThemeToggle />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t('auth.logout')}
              title={t('auth.logout')}
              disabled={logout.isPending}
              onClick={() => logout.mutate()}
            >
              <LogOut className="size-4" aria-hidden="true" />
            </Button>
          </div>
        </header>
        <main id="main" tabIndex={-1} className="mx-auto w-full max-w-6xl flex-1 p-4 outline-none md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
