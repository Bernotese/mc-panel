import { Moon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Server, ServerState } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { stateBadgeVariant, stateDotClass, stateLabelKey } from './status'

/** `stale`: the state is the last known one (issue #155) – said in the label, the lamp dimmed, no state colour. */
export function StatusBadge({ state, stale = false }: { state: ServerState; stale?: boolean }) {
  const { t } = useTranslation()
  const label = t(stateLabelKey(state))
  return (
    <Badge variant={stale ? 'outline' : stateBadgeVariant(state)} title={stale ? t('freshness.last_known_hint') : undefined}>
      <span aria-hidden="true" className={cn('size-1.5 shrink-0', stateDotClass(state), stale && 'opacity-40 shadow-none')} />
      {stale ? t('freshness.last_known', { state: label }) : label}
    </Badge>
  )
}

/** Like StatusBadge, but a stopped dev that sleeps says so: a connection wakes it (dev instances spec §6). */
export function ServerStatusBadge({ server, stale = false }: { server: Pick<Server, 'status' | 'dev'>; stale?: boolean }) {
  const { t } = useTranslation()
  if (server.dev?.sleeping && server.status.state === 'stopped') {
    return (
      <Badge variant={stale ? 'outline' : 'secondary'} title={t(stale ? 'freshness.last_known_hint' : 'dev.sleeping_hint')}>
        <Moon aria-hidden="true" className={stale ? 'opacity-40' : undefined} />
        {stale ? t('freshness.last_known', { state: t('dev.sleeping') }) : t('dev.sleeping')}
      </Badge>
    )
  }
  return <StatusBadge state={server.status.state} stale={stale} />
}
