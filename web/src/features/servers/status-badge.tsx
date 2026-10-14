import { useTranslation } from 'react-i18next'
import type { ServerState } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { stateBadgeVariant, stateLabelKey } from './status'

/** Status lamp colour shared by the badge and the server tree. */
export function stateDotClass(state: ServerState): string {
  if (state === 'running') return 'bg-status-online shadow-[0_0_6px_var(--status-online)]'
  if (state === 'error') return 'bg-status-error'
  if (state === 'stopped' || state === 'not_configured') return 'bg-status-offline'
  return 'bg-status-starting'
}

export function StatusBadge({ state }: { state: ServerState }) {
  const { t } = useTranslation()
  return (
    <Badge variant={stateBadgeVariant(state)}>
      <span aria-hidden="true" className={cn('size-1.5 shrink-0', stateDotClass(state))} />
      {t(stateLabelKey(state))}
    </Badge>
  )
}
