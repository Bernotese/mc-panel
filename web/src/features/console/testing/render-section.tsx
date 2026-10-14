import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { Server } from '@/api/types'
import { serverQueryOptions } from '@/features/servers/api'
import { renderWithProviders, type RenderOptions } from '@/test/render'

function WithServer({ serverId, children }: { serverId: number; children: (server: Server) => ReactNode }) {
  const query = useQuery(serverQueryOptions(serverId))
  return query.data ? <>{children(query.data)}</> : null
}

/** Renders one server section the way the server route does: with the server loaded from the (mock) API. */
export function renderSection(serverId: number, section: (server: Server) => ReactNode, options: RenderOptions = {}) {
  return renderWithProviders(<WithServer serverId={serverId}>{section}</WithServer>, options)
}
