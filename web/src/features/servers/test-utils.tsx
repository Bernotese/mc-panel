import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import type { Me } from '@/api/types'
import { routerNavigator, wireApi } from '@/app/wire-api'
import { applyMe } from '@/features/auth/me'
import { createAppRouter } from '@/router'
import { createTestQueryClient } from '@/test/render'

/** Full app (real route tree, guards and AppLayout) at `path` – for the server layout, its sections and the tree. */
export async function renderApp(path: string, me: Me | null, queryClient = createTestQueryClient()) {
  if (me) applyMe(queryClient, me)
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  wireApi(queryClient, routerNavigator(router))
  await router.load()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...result, router, queryClient }
}
