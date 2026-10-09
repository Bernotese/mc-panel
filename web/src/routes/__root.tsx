// web/src/routes/__root.tsx
import { createRootRouteWithContext, Outlet } from '@tanstack/react-router'
import { AppToaster } from '@/components/app-toaster'
import { NotFoundPage } from '@/components/not-found-page'
import { SudoDialog } from '@/features/sudo/sudo-dialog'
import type { RouterContext } from '@/router'

export const Route = createRootRouteWithContext<RouterContext>()({
  component: RootLayout,
  notFoundComponent: NotFoundPage,
})

function RootLayout() {
  return (
    <>
      <Outlet />
      <SudoDialog />
      <AppToaster />
    </>
  )
}
