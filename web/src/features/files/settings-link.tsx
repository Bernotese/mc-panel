import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

/** Link to the server's settings area; a router link, so the files area's blocker can ask first. */
export function ServerSettingsLink({ serverId, children, className }: { serverId: number; children: ReactNode; className?: string }) {
  return (
    <Link to="/servers/$id/settings" params={{ id: String(serverId) }} className={cn('text-primary underline-offset-4 hover:underline', className)}>
      {children}
    </Link>
  )
}
