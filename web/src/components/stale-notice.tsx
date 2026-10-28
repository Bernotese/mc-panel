import { CloudOff, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/lib/error-message'
import { useSinceText, type Freshness } from '@/lib/freshness'
import { cn } from '@/lib/utils'

/**
 * Issue #155: one notice for a surface whose cached data could not be refreshed – why, since when, and a retry.
 * The data stays visible next to it, marked stale by the surface itself; it disappears with the next success.
 */
export function StaleNotice({ freshness, className }: { freshness: Freshness; className?: string }) {
  const { t } = useTranslation()
  const sinceText = useSinceText()
  if (!freshness.stale) return null
  let reason: string
  if (freshness.offline) reason = t('freshness.offline')
  else if (isApiError(freshness.error, 'agent_unavailable')) reason = t('freshness.agent_unavailable')
  else reason = errorMessage(freshness.error, t)
  return (
    <div
      role="status"
      data-testid="stale-notice"
      className={cn('border-status-starting/60 bg-card flex flex-col gap-3 border-2 p-3 text-sm sm:flex-row sm:items-center sm:justify-between', className)}
    >
      <div className="flex min-w-0 gap-2">
        <CloudOff aria-hidden="true" className="text-status-starting mt-0.5 size-4 shrink-0" />
        <p className="min-w-0">
          <span className="text-status-starting font-medium">{t('freshness.title')}</span> {reason}{' '}
          <span className="text-muted-foreground">{t('freshness.since', { when: sinceText(freshness) })}</span>
        </p>
      </div>
      <Button type="button" variant="outline" size="sm" className="shrink-0 self-start sm:self-center" disabled={freshness.retrying} onClick={freshness.retry}>
        <RefreshCw aria-hidden="true" className={freshness.retrying ? 'motion-safe:animate-spin' : undefined} />
        {t('common.retry')}
      </Button>
    </div>
  )
}
