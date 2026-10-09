import type { QueryClient } from '@tanstack/react-query'
import type { AnyRouter } from '@tanstack/react-router'
import { configureApiHandlers } from '@/api/handlers'
import { applyMe, clearSession } from '@/features/auth/me'
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
      if (!nav.currentPath().startsWith('/login')) nav.push('/login')
    },
    onSetupRequired: () => {
      if (nav.currentPath() !== '/setup-2fa') nav.push('/setup-2fa')
    },
    requestSudo,
  })
}
