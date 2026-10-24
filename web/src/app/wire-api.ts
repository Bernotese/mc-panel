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
  const sendToStagePage = async (path: '/setup-2fa' | '/change-password') => {
    // The cached Me may still say `full` (the backend derives `setup` and `password` on every request). Refresh it
    // first, otherwise the target page's guard bounces us straight back to the dashboard and the request fails again.
    try {
      await refreshMe(queryClient)
    } catch {
      // navigate anyway; the guards on the target route decide what to do with the (possibly stale) state
    }
    if (nav.currentPath() !== path) nav.push(path)
  }
  configureApiHandlers({
    onMe: (me) => applyMe(queryClient, me),
    onUnauthorized: () => {
      clearSession(queryClient)
      // Only the credential form itself needs no redirect; an expired pre-auth session on /login/2fa must go back.
      if (nav.currentPath() !== '/login') nav.push('/login')
    },
    onSetupRequired: () => sendToStagePage('/setup-2fa'),
    onPasswordChangeRequired: () => sendToStagePage('/change-password'),
    requestSudo,
  })
}
