import { useQuery, type QueryClient } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import type { Server } from '@/api/types'
import { serverQueryOptions } from '@/features/servers/api'
import { loginAs } from '@/mocks/db'
import { renderWithProviders, type RenderOptions, type TestRouter } from '@/test/render'

// eslint-disable-next-line react-refresh/only-export-components -- test-only helper module, never hot-reloaded
function WithServer({ serverId, render }: { serverId: number; render: (server: Server) => ReactNode }) {
  const query = useQuery(serverQueryOptions(serverId))
  return query.data ? <>{render(query.data)}</> : null
}

/** Renders one server section the way the server route does: with the server loaded from the (mock) API. */
export function renderSection(serverId: number, section: (server: Server) => ReactNode, options: RenderOptions = {}) {
  return renderWithProviders(<WithServer serverId={serverId} render={section} />, options)
}

/**
 * Like renderSection, plus a userEvent instance and login: renders one server area (files, plugins, backups, settings …)
 * at /servers/<id>. The test router matches every path, so navigation can be asserted via router.state.location.
 */
export async function renderServerTab(
  render: (server: Server) => ReactNode,
  { serverId = 1, username = 'admin', sudo, path }: { serverId?: number; username?: string; sudo?: boolean; path?: string } = {},
): Promise<{ user: ReturnType<typeof userEvent.setup>; router: TestRouter; queryClient: QueryClient }> {
  const user = userEvent.setup()
  const { router, queryClient } = await renderSection(serverId, render, {
    me: loginAs(username, sudo === undefined ? {} : { sudo }),
    path: path ?? `/servers/${serverId}`,
  })
  return { user, router, queryClient }
}
