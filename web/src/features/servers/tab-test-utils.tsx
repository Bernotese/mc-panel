import { useQuery, type QueryClient } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import type { Server } from '@/api/types'
import { loginAs } from '@/mocks/db'
import { renderWithProviders, type TestRouter } from '@/test/render'
import { serverQueryOptions } from './api'

// eslint-disable-next-line react-refresh/only-export-components -- test-only helper, never hot-reloaded
function WithServer({ serverId, render }: { serverId: number; render: (server: Server) => ReactNode }) {
  const query = useQuery(serverQueryOptions(serverId))
  if (!query.data) return null
  return <>{render(query.data)}</>
}

/**
 * Renders one server area (files, plugins, backups, settings …) with its loaded server, independent of the detail page
 * and its navigation (T2). The test router matches every path, so navigation can be asserted via router.state.location.
 */
export async function renderServerTab(
  render: (server: Server) => ReactNode,
  { serverId = 1, username = 'admin', sudo, path }: { serverId?: number; username?: string; sudo?: boolean; path?: string } = {},
): Promise<{ user: ReturnType<typeof userEvent.setup>; router: TestRouter; queryClient: QueryClient }> {
  const user = userEvent.setup()
  const { router, queryClient } = await renderWithProviders(<WithServer serverId={serverId} render={render} />, {
    me: loginAs(username, sudo === undefined ? {} : { sudo }),
    path: path ?? `/servers/${serverId}`,
  })
  return { user, router, queryClient }
}
