import { Outlet } from '@tanstack/react-router'
import { Menu, Server as ServerIcon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { useMediaQuery } from '@/lib/use-media-query'
import { Breadcrumb } from './breadcrumb'
import { IconRail } from './icon-rail'
import { ServerTree } from './server-tree'
import { readTreeOpen, writeTreeOpen } from './tree-state'

const LG_QUERY = '(min-width: 1024px)'
const MD_QUERY = '(min-width: 768px)'

/** Spec §4.1/§4.2: rail + tree from lg, rail + tree flyout from md, top bar + drawer below. */
export function AppLayout() {
  const { t } = useTranslation()
  const lg = useMediaQuery(LG_QUERY)
  const md = useMediaQuery(MD_QUERY)
  const [treeOpen, setTreeOpen] = useState(readTreeOpen)
  const [menuOpen, setMenuOpen] = useState(false)
  const [flyoutOpen, setFlyoutOpen] = useState(false)

  function toggleTree() {
    const next = !treeOpen
    writeTreeOpen(next)
    setTreeOpen(next)
  }

  return (
    <div className="bg-background flex min-h-svh">
      <a href="#main" className="focus:bg-background sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:px-3 focus:py-2">
        {t('nav.skip')}
      </a>
      {md ? (
        <aside className="sticky top-0 flex h-svh shrink-0">
          <IconRail treeOpen={lg ? treeOpen : flyoutOpen} onToggleTree={lg ? toggleTree : () => setFlyoutOpen(true)} />
          {lg && treeOpen ? <ServerTree /> : null}
        </aside>
      ) : null}
      {md && !lg ? (
        <Sheet open={flyoutOpen} onOpenChange={setFlyoutOpen}>
          {/* Opens beside the rail, which stays visible. */}
          <SheetContent side="left" className="w-60 gap-0 p-0 data-[side=left]:left-14 sm:max-w-60">
            <SheetHeader className="sr-only">
              <SheetTitle>{t('nav.servers')}</SheetTitle>
              <SheetDescription>{t('nav.open_servers')}</SheetDescription>
            </SheetHeader>
            <ServerTree onNavigate={() => setFlyoutOpen(false)} className="w-full border-r-0" />
          </SheetContent>
        </Sheet>
      ) : null}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="bg-background/95 sticky top-0 z-10 flex h-11 items-center gap-2 border-b px-4 backdrop-blur md:px-6">
          {!md ? (
            <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
              <SheetTrigger asChild>
                <Button type="button" variant="ghost" size="icon" aria-label={t('nav.open_menu')} className="-ml-2">
                  <Menu className="size-5" aria-hidden="true" />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="flex w-[18.5rem] flex-row gap-0 p-0 sm:max-w-[18.5rem]">
                <SheetHeader className="sr-only">
                  <SheetTitle>{t('auth.brand')}</SheetTitle>
                  <SheetDescription>{t('nav.menu_description')}</SheetDescription>
                </SheetHeader>
                <IconRail onNavigate={() => setMenuOpen(false)} />
                <ServerTree onNavigate={() => setMenuOpen(false)} className="min-w-0 flex-1 border-r-0 pt-12" />
              </SheetContent>
            </Sheet>
          ) : null}
          {md && !lg ? (
            <Button type="button" variant="ghost" size="icon" aria-label={t('nav.open_servers')} className="-ml-2" onClick={() => setFlyoutOpen(true)}>
              <ServerIcon className="size-4" aria-hidden="true" />
            </Button>
          ) : null}
          <Breadcrumb />
        </header>
        <main id="main" tabIndex={-1} className="w-full min-w-0 flex-1 p-4 outline-none md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
