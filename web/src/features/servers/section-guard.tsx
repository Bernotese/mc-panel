import { Navigate } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import type { ServerSection } from './sections'
import { useServerContext } from './server-context'

/** A section that is not (or no longer) allowed falls back to the overview (spec §4.3), also after a revoked permission. */
export function SectionGuard({ section, children }: { section: ServerSection; children: ReactNode }) {
  const { server, sections } = useServerContext()
  if (!sections.includes(section)) return <Navigate to="/servers/$id" params={{ id: String(server.id) }} replace />
  return <>{children}</>
}
