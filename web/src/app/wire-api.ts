import type { QueryClient } from '@tanstack/react-query'
import type { AnyRouter } from '@tanstack/react-router'
import { configureApiHandlers } from '@/api/handlers'
import { applyMe, clearSession, refreshMe } from '@/features/auth/me'
import { requestSudo } from '@/features/sudo/sudo-store'

export interface AppNavigator {
  push(path: string): void
  currentPath(): string
}

export function routerNavigator(router: AnyRouter): AppNavigator {
  return {
    push: (path) => router.history.push(path),
    currentPath: () => router.history.location.pathname,
  }
}

/** Connects the framework-free API middleware with React Query, the router and the sudo dialog. */
export function wireApi(queryClient: QueryClient, nav: AppNavigator): void {
  configureApiHandlers({
    onMe: (me) => applyMe(queryClient, me),
    onUnauthorized: () => {
      clearSession(queryClient)
      // Only the credential form itself needs no redirect; an expired pre-auth session on /login/2fa must go back.
      if (nav.currentPath() !== '/login') nav.push('/login')
    },
    onSetupRequired: async () => {
      // The cached Me may still say `full` (the backend downgrades sessions when 2FA becomes mandatory). Refresh it
      // first, otherwise the setup guard bounces us straight back to the dashboard and the request fails again.
      try {
        await refreshMe(queryClient)
      } catch {
        // navigate anyway; the guards on the target route decide what to do with the (possibly stale) state
      }
      if (nav.currentPath() !== '/setup-2fa') nav.push('/setup-2fa')
    },
    requestSudo,
  })
}
