import { useNavigate } from '@tanstack/react-router'
import type { MouseEvent, ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * Link to the server's settings area. It navigates through the router (so the files area's blocker can ask first) via
 * `href`, because the typed route `/servers/$id/settings` only exists once the shell (T2) is integrated; the lead may
 * swap it for a typed <Link> afterwards.
 */
export function ServerSettingsLink({ serverId, children, className }: { serverId: number; children: ReactNode; className?: string }) {
  const navigate = useNavigate()
  const href = `/servers/${serverId}/settings`
  function onClick(event: MouseEvent<HTMLAnchorElement>) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    void navigate({ href })
  }
  return (
    <a href={href} onClick={onClick} className={cn('text-primary underline-offset-4 hover:underline', className)}>
      {children}
    </a>
  )
}
