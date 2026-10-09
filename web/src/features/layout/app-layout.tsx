// web/src/features/layout/app-layout.tsx – replaced by the full app shell in Task 20
import { Outlet } from '@tanstack/react-router'

export function AppLayout() {
  return (
    <main id="main" className="mx-auto w-full max-w-6xl p-4 md:p-6">
      <Outlet />
    </main>
  )
}
