import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, Outlet, RouterProvider } from '@tanstack/react-router'
import { render, type RenderResult } from '@testing-library/react'
import type { ReactNode } from 'react'
import type { Me } from '@/api/types'
import { routerNavigator, wireApi } from '@/app/wire-api'
import { AppToaster } from '@/components/app-toaster'
import { applyMe } from '@/features/auth/me'
import { SudoDialog } from '@/features/sudo/sudo-dialog'

export function createTestQueryClient(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } } })
}

function createTestRouter(ui: ReactNode, path: string) {
  const rootRoute = createRootRoute({
    component: () => (
      <>
        <Outlet />
        <SudoDialog />
        <AppToaster />
      </>
    ),
  })
  const indexRoute = createRoute({ getParentRoute: () => rootRoute, path: '/', component: () => <>{ui}</> })
  const splatRoute = createRoute({ getParentRoute: () => rootRoute, path: '$', component: () => <>{ui}</> })
  return createRouter({
    routeTree: rootRoute.addChildren([indexRoute, splatRoute]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
}

export type TestRouter = ReturnType<typeof createTestRouter>

export interface RenderOptions {
  path?: string
  me?: Me | null
  queryClient?: QueryClient
}

/** Renders a page inside a memory router that matches every path; assert navigation via router.state.location. */
export async function renderWithProviders(
  ui: ReactNode,
  { path = '/', me = null, queryClient = createTestQueryClient() }: RenderOptions = {},
): Promise<RenderResult & { router: TestRouter; queryClient: QueryClient }> {
  if (me) applyMe(queryClient, me)
  const router = createTestRouter(ui, path)
  wireApi(queryClient, routerNavigator(router))
  await router.load()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...result, router, queryClient }
}
