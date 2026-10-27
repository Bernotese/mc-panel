import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

/**
 * Spec §8: marks a dev instance wherever its name is shown. `decorative` hides it from assistive technology where the
 * surrounding link must keep the bare server name (tree); the tooltip still names the source.
 */
export function DevBadge({ source, decorative = false, className }: { source: string; decorative?: boolean; className?: string }) {
  const { t } = useTranslation()
  return (
    <Badge
      variant="outline"
      title={t('dev.badge_title', { source })}
      aria-hidden={decorative ? true : undefined}
      className={cn('border-status-dev text-status-dev h-5 px-1.5', className)}
    >
      {t('dev.badge')}
    </Badge>
  )
}
