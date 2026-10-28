import { useMutation, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, ExternalLink, LoaderCircle, ShieldAlert } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { useErrorToast } from '@/lib/error-message'
import { SOURCE_REPO } from '@/lib/source'
import { resolveRecovery, serverKey, serversKey } from './api'

/** docs/install.md, section "A restore could not be completed" (data safety spec §2.3). */
export const RECOVERY_RUNBOOK = `${SOURCE_REPO}/blob/main/docs/install.md#a-restore-could-not-be-completed`

const JOBS: ReadonlySet<string> = new Set(['restore', 'extract', 'dev_sync'])

/**
 * Data safety spec §2.3: in the header of every area, what the data state of the server means – a job rewriting the
 * data (busy), the files service not answering (unknown, never treated as safe), or a stuck restore that an admin
 * resolves after checking the data directory. Safe shows nothing (except the result of a resolve).
 */
export function DataStateBanner({ server, isAdmin }: { server: Pick<Server, 'id' | 'data_state' | 'data_job'>; isAdmin: boolean }) {
  const { t } = useTranslation()
  // Kept after the resolve: the server is safe again, but the operator still needs to know where the folders went.
  const [resolved, setResolved] = useState<string | null>(null)

  if (server.data_state === 'busy') {
    const job = t(`servers.data.job.${server.data_job && JOBS.has(server.data_job) ? server.data_job : 'other'}`)
    return (
      <p role="status" className="bevel-inset flex items-start gap-2 px-4 py-3 text-sm">
        <LoaderCircle aria-hidden="true" className="text-status-starting mt-0.5 size-4 shrink-0 motion-safe:animate-spin" />
        {t('servers.data.busy', { job })}
      </p>
    )
  }
  if (server.data_state === 'unknown') {
    return (
      <div role="alert" className="border-status-starting/60 bg-card flex items-start gap-2 border-2 p-3 text-sm">
        <CircleAlert aria-hidden="true" className="text-status-starting mt-0.5 size-4 shrink-0" />
        <p>
          {t('servers.data.unknown')} <span className="text-muted-foreground">{t('servers.data.unknown_hint')}</span>
        </p>
      </div>
    )
  }
  if (server.data_state === 'recovery_required') return <RecoveryBanner serverId={server.id} isAdmin={isAdmin} onResolved={setResolved} />
  return resolved !== null ? <Resolved folder={resolved} onClose={() => setResolved(null)} /> : null
}

function RecoveryBanner({ serverId, isAdmin, onResolved }: { serverId: number; isAdmin: boolean; onResolved(folder: string): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [checked, setChecked] = useState(false)
  // Sudo comes from the global dialog: a 403 sudo_required opens it and retries this request.
  const resolve = useMutation({
    mutationFn: () => resolveRecovery(serverId),
    onSuccess: (recovery) => {
      onResolved(recovery.folder ?? '')
      toast.success(t('servers.data.resolved_title'))
      void queryClient.invalidateQueries({ queryKey: serverKey(serverId) })
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
    },
    onError: notifyError,
  })
  return (
    <section aria-labelledby={`${id}-title`} data-tone="danger" className="border-status-error/60 bg-card space-y-3 border-2 p-4 text-sm">
      <h2 id={`${id}-title`} className="text-status-error flex items-center gap-2 font-medium">
        <ShieldAlert aria-hidden="true" className="size-4 shrink-0" />
        {t('servers.data.recovery_title')}
      </h2>
      <p>{t('servers.data.recovery_text')}</p>
      <a href={RECOVERY_RUNBOOK} target="_blank" rel="noopener noreferrer" className="text-primary inline-flex items-center gap-1 underline underline-offset-4">
        {t('servers.data.recovery_runbook')}
        <ExternalLink aria-hidden="true" className="size-3.5" />
      </a>
      {isAdmin ? (
        <div className="space-y-3 border-t pt-3">
          <p className="text-muted-foreground">{t('servers.data.recovery_resolve_text')}</p>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              className="accent-primary size-4"
              checked={checked}
              disabled={resolve.isPending}
              onChange={(e) => setChecked(e.target.checked)}
            />
            {t('servers.data.recovery_checked')}
          </label>
          <Button type="button" variant="destructive" disabled={!checked || resolve.isPending} onClick={() => resolve.mutate()}>
            {t('servers.data.recovery_resolve')}
          </Button>
        </div>
      ) : (
        <p className="text-muted-foreground">{t('servers.data.recovery_admin_only')}</p>
      )}
    </section>
  )
}

function Resolved({ folder, onClose }: { folder: string; onClose(): void }) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <div role="status" aria-labelledby={`${id}-title`} className="bevel-inset flex flex-col gap-3 px-4 py-3 text-sm sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0 space-y-1">
        <p id={`${id}-title`} className="font-medium">
          {t('servers.data.resolved_title')}
        </p>
        <p className="break-words">
          {t('servers.data.resolved_text_before')} <code className="font-mono">backups/recovery/{folder}</code> {t('servers.data.resolved_text_after')}
        </p>
      </div>
      <Button type="button" variant="outline" size="sm" className="shrink-0" onClick={onClose}>
        {t('common.close')}
      </Button>
    </div>
  )
}
