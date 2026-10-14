import { useTranslation } from 'react-i18next'
import type { ServerState } from '@/api/types'
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
