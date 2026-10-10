import { useTranslation } from 'react-i18next'
import type { ServerState } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { stateBadgeVariant, stateLabelKey } from './status'

export function StatusBadge({ state }: { state: ServerState }) {
  const { t } = useTranslation()
  return <Badge variant={stateBadgeVariant(state)}>{t(stateLabelKey(state))}</Badge>
}
