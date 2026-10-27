import { Moon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Server, ServerState } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { stateBadgeVariant, stateDotClass, stateLabelKey } from './status'

export function StatusBadge({ state }: { state: ServerState }) {
  const { t } = useTranslation()
  return (
    <Badge variant={stateBadgeVariant(state)}>
      <span aria-hidden="true" className={cn('size-1.5 shrink-0', stateDotClass(state))} />
      {t(stateLabelKey(state))}
    </Badge>
  )
}

/** Like StatusBadge, but a stopped dev that sleeps says so: a connection wakes it (dev instances spec §6). */
export function ServerStatusBadge({ server }: { server: Pick<Server, 'status' | 'dev'> }) {
  const { t } = useTranslation()
  if (server.dev?.sleeping && server.status.state === 'stopped') {
    return (
      <Badge variant="secondary" title={t('dev.sleeping_hint')}>
        <Moon aria-hidden="true" />
        {t('dev.sleeping')}
      </Badge>
    )
  }
  return <StatusBadge state={server.status.state} />
}
