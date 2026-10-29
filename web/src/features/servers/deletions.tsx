import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { LoaderCircle, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerDeletion } from '@/api/types'
import { Section } from '@/components/section'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { retryServerDeletion, serverDeletionsKey } from './api'
import { isOpen, isRetryable, sortDeletions } from './deletions-model'
import { useServerDeletions } from './use-server-deletions'

const BADGE: Record<ServerDeletion['state'], 'default' | 'secondary' | 'destructive' | 'outline'> = {
  pending: 'outline',
  failed: 'destructive',
  partial: 'outline',
  done: 'secondary',
}

/**
 * Dashboard: only while a deletion is not done – the server is gone from the list, but its container or backups are
 * still being cleaned up (or failed and are retried). Nothing when everything is done.
 */
export function OpenDeletionsSection({ isAdmin }: { isAdmin: boolean }) {
  const { t } = useTranslation()
  const deletions = useServerDeletions(isAdmin)
  const open = sortDeletions(deletions.data ?? []).filter(isOpen)
  if (!isAdmin || open.length === 0) return null
  return (
    <Section
      title={t('servers.deletions.open_title')}
      actions={
        <Link to="/settings" className="text-primary text-sm underline underline-offset-4">
          {t('servers.deletions.all_link')}
        </Link>
      }
    >
      <DeletionList deletions={open} />
    </Section>
  )
}

export function DeletionList({ deletions }: { deletions: readonly ServerDeletion[] }) {
  const { t } = useTranslation()
  return (
    <ul aria-label={t('servers.deletions.list_label')} className="space-y-2">
      {deletions.map((d) => (
        <DeletionItem key={d.server_id} deletion={d} />
      ))}
    </ul>
  )
}

function DeletionItem({ deletion: d }: { deletion: ServerDeletion }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  // Sudo comes from the global dialog: a 403 sudo_required opens it and retries this request.
  const retry = useMutation({
    mutationFn: () => retryServerDeletion(d.server_id),
    onSuccess: (updated) => {
      queryClient.setQueryData<ServerDeletion[]>(serverDeletionsKey, (list) => list?.map((x) => (x.server_id === updated.server_id ? updated : x)))
      if (updated.state === 'done') toast.success(t('servers.deletions.retried_done', { name: d.name }))
      else toast.info(t('servers.deletions.retried_open', { name: d.name }))
    },
    onError: notifyError,
    onSettled: () => void queryClient.invalidateQueries({ queryKey: serverDeletionsKey }),
  })
  return (
    <li className="bevel-inset space-y-1.5 px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-medium break-all">{d.name}</span>
        <Badge variant={BADGE[d.state]}>
          {d.state === 'pending' ? <LoaderCircle aria-hidden="true" className="motion-safe:animate-spin" /> : null}
          {t(`servers.deletions.state.${d.state}`)}
        </Badge>
        <span className="text-muted-foreground text-xs">
          {d.completed_at !== null
            ? t('servers.deletions.completed', { when: fmt.dateTime(d.completed_at) })
            : t('servers.deletions.requested', { when: fmt.dateTime(d.requested_at) })}
        </span>
        {isRetryable(d) ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="sm:ml-auto"
            aria-label={t('servers.deletions.retry_label', { name: d.name })}
            disabled={retry.isPending}
            onClick={() => retry.mutate()}
          >
            <RefreshCw aria-hidden="true" className={retry.isPending ? 'motion-safe:animate-spin' : undefined} />
            {t('servers.deletions.retry')}
          </Button>
        ) : null}
      </div>
      <p className="text-muted-foreground">{t(`servers.deletions.container.${d.container}`)}</p>
      {d.container_error ? <p className="text-destructive break-words">{t('servers.deletions.container_error', { error: d.container_error })}</p> : null}
      {d.backups !== 'none' ? <p className="text-muted-foreground">{t(`servers.deletions.backups.${d.backups}`)}</p> : null}
      {d.backups_error ? <p className="text-destructive break-words">{t('servers.deletions.backups_error', { error: d.backups_error })}</p> : null}
    </li>
  )
}
