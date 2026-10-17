import { ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/types'
import { cn } from '@/lib/utils'

/** Web ports spec §3.4: for everyone who sees the server, once the route is live. */
export function WebMapLink({ server, className }: { server: Pick<Server, 'web_status' | 'web_url'>; className?: string }) {
  const { t } = useTranslation()
  if (server.web_status !== 'active' || !server.web_url) return null
  return (
    <a
      href={server.web_url}
      target="_blank"
      rel="noopener noreferrer"
      className={cn('text-primary inline-flex w-fit items-center gap-1 text-sm underline underline-offset-4', className)}
    >
      {t('servers.web.open')}
      <ExternalLink aria-hidden="true" className="size-3.5" />
    </a>
  )
}
