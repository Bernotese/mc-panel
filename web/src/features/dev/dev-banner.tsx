import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Moon, RefreshCw } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { DevSummary, Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { serverKey, serversKey } from '@/features/servers/api'
import { SLOW_POLL_MS } from '@/features/servers/status'
import { useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { DEV_PENDING_POLL_MS, devKey, devQueryOptions, syncDev, updateDev } from './api'
import { useSyncWhen } from './use-sync-when'

/**
 * Spec §8: in the header of every area of a dev: which prod it copies, when it is overwritten next (or that the sync
 * is paused), the last result and whether it sleeps. Sync now and the pause switch need server.files.
 */
export function DevBanner({ server, dev }: { server: Server; dev: DevSummary }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const syncWhen = useSyncWhen()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const canManage = server.permissions.includes('server.files')
  // The state comes from the server (polled with it); paused_at and the error text exist only on the dev resource.
  const details = useQuery({ ...devQueryOptions(server.id), refetchInterval: dev.sync_pending ? DEV_PENDING_POLL_MS : SLOW_POLL_MS })
  const pausedAt = details.data?.paused_at ?? null
  const error = details.data?.last_sync_error ?? null

  const refresh = () => void queryClient.invalidateQueries({ queryKey: serverKey(server.id) })
  const sync = useMutation({
    mutationFn: () => syncDev(server.id),
    onSuccess: (updated) => {
      queryClient.setQueryData(devKey(server.id), updated)
      toast.success(t('dev.banner.sync_requested'))
      refresh()
    },
    onError: notifyError,
  })
  const pause = useMutation({
    mutationFn: (paused: boolean) => updateDev(server.id, { sync_paused: paused }),
    onSuccess: (updated) => {
      queryClient.setQueryData(devKey(server.id), updated)
      toast.success(t(updated.sync_paused ? 'dev.banner.paused_done' : 'dev.banner.resumed_done'))
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
    onError: notifyError,
  })

  let status: string
  if (dev.sync_pending) status = t(dev.last_sync_status === 'running' ? 'dev.banner.syncing' : 'dev.banner.sync_waiting')
  else if (dev.sync_paused) status = pausedAt !== null ? t('dev.banner.paused_since', { when: fmt.date(pausedAt) }) : t('dev.banner.paused')
  else if (dev.next_sync_at !== null) status = t('dev.banner.overwritten', { when: syncWhen(dev.next_sync_at) })
  else status = t('dev.banner.overwritten_unscheduled')

  return (
    <section
      aria-labelledby={`${id}-title`}
      data-tone="dev"
      className="bg-card border-status-dev/60 flex flex-col gap-3 border-2 border-l-4 p-4 lg:flex-row lg:items-center lg:justify-between"
    >
      <div className="min-w-0 space-y-1.5 text-sm">
        <h2 id={`${id}-title`} className="sr-only">
          {t('dev.banner.title')}
        </h2>
        <p className="leading-relaxed">
          {t('dev.banner.copy_of')}{' '}
          <Link to="/servers/$id" params={{ id: String(dev.source_id) }} className="text-status-dev font-semibold underline underline-offset-4">
            {dev.source_name}
          </Link>{' '}
          <span className={dev.sync_paused && !dev.sync_pending ? 'text-status-starting font-medium' : undefined}>{status}</span>
        </p>
        <LastSync status={dev.last_sync_status} at={dev.last_sync_at} error={error} pending={dev.sync_pending} />
        {dev.sleeping && !dev.sync_pending ? (
          <p className="text-muted-foreground flex items-center gap-1.5">
            <Moon aria-hidden="true" className="size-3.5 shrink-0" />
            {server.address ? t('dev.banner.sleeping_address', { address: server.address }) : t('dev.banner.sleeping')}
          </p>
        ) : null}
      </div>
      {canManage ? (
        <div className="flex shrink-0 flex-wrap items-center gap-x-5 gap-y-3">
          <div className="flex items-center gap-2">
            <Switch
              id={`${id}-pause`}
              checked={dev.sync_paused}
              disabled={pause.isPending}
              onCheckedChange={(paused) => pause.mutate(paused)}
            />
            <Label htmlFor={`${id}-pause`}>{t('dev.banner.pause')}</Label>
          </div>
          <Button type="button" variant="outline" disabled={sync.isPending || dev.sync_pending} onClick={() => sync.mutate()}>
            <RefreshCw aria-hidden="true" className={dev.sync_pending ? 'motion-safe:animate-spin' : undefined} />
            {t('dev.banner.sync_now')}
          </Button>
        </div>
      ) : null}
    </section>
  )
}

function LastSync({ status, at, error, pending }: { status: DevSummary['last_sync_status']; at: number | null; error: string | null; pending: boolean }) {
  const { t } = useTranslation()
  const syncWhen = useSyncWhen()
  if (at === null) return pending ? null : <p className="text-muted-foreground">{t('dev.banner.never_synced')}</p>
  const when = syncWhen(at)
  if (status === 'failed') {
    return (
      <p role="alert" className="text-status-error">
        {error ? t('dev.banner.last_failed_error', { when, error }) : t('dev.banner.last_failed', { when })}
      </p>
    )
  }
  return <p className="text-muted-foreground">{t('dev.banner.last_ok', { when })}</p>
}
