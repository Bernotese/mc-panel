import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { BackupSnapshot, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import {
  createBackup,
  deleteBackup,
  overviewQueryOptions,
  pinBackup,
  refreshBackups,
  refreshJobOnConflict,
  replaceSnapshot,
  restoreBackup,
  startBackupJob,
} from './api'
import { CreateBackupDialog } from './create-dialog'
import { JobStatus } from './job-status'
import { useBackupErrorToast } from './messages'
import { RestoreDialog } from './restore-dialog'
import { SnapshotList } from './snapshot-list'
import { useBackupJob } from './use-backup-job'

export function BackupsTab({ server }: { server: Server }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyToast = useBackupErrorToast()
  const notifyError = (err: unknown) => {
    refreshJobOnConflict(queryClient, server.id, err)
    notifyToast(err)
  }
  const overview = useQuery(overviewQueryOptions(server.id))
  const { job, active } = useBackupJob(server.id)
  const [creating, setCreating] = useState(false)
  const [restoring, setRestoring] = useState<BackupSnapshot | null>(null)
  const [deleting, setDeleting] = useState<BackupSnapshot | null>(null)
  const [dismissed, setDismissed] = useState<string | null>(null)
  // The panel keeps the last job result (spec §5.3); one that ended before the tab was opened is not shown again.
  const [openedAt] = useState(() => Date.now())
  // A job this tab saw active stays visible when it ends, independent of the panel's clock.
  const [watched, setWatched] = useState<string | null>(null)
  if (active && job !== null && job.id !== watched) setWatched(job.id)

  const create = useMutation({
    mutationFn: (note: string) => createBackup(server.id, note),
    onSuccess: (started) => {
      startBackupJob(queryClient, server.id, started)
      setCreating(false)
      toast.success(t('backups.create.started'))
    },
    onError: (err) => {
      setCreating(false)
      notifyError(err)
    },
  })

  const pin = useMutation({
    mutationFn: ({ s, pinned }: { s: BackupSnapshot; pinned: boolean }) => pinBackup(server.id, s.id, pinned),
    onSuccess: (next, { s, pinned }) => {
      replaceSnapshot(queryClient, server.id, s.id, next)
      toast.success(pinned ? t('backups.snapshots.pinned_done') : t('backups.snapshots.unpinned_done'))
    },
    onError: (err) => {
      // not_found: the snapshot changed meanwhile (another tab pinned it, retention removed it) – the repo is the truth.
      if (isApiError(err, 'not_found')) void refreshBackups(queryClient, server.id)
      // 409 backup_running while this tab knows no running job: the file service refused the pin
      // (a download or an extract runs) – say so instead of "a job runs".
      if (isApiError(err, 'backup_running') && !active) {
        refreshJobOnConflict(queryClient, server.id, err)
        toast.error(t('backups.error.files_busy'))
        return
      }
      notifyError(err)
    },
  })

  const restore = useMutation({
    mutationFn: (s: BackupSnapshot) => restoreBackup(server.id, s.id),
    onSuccess: (started) => {
      startBackupJob(queryClient, server.id, started)
      setRestoring(null)
      toast.success(t('backups.restore.started'))
    },
    onError: (err) => {
      setRestoring(null)
      notifyError(err)
    },
  })

  const remove = useMutation({
    mutationFn: (s: BackupSnapshot) => deleteBackup(server.id, s.id),
    onSuccess: (started) => {
      startBackupJob(queryClient, server.id, started)
      setDeleting(null)
      toast.success(t('backups.delete.started'))
    },
    onError: (err) => {
      setDeleting(null)
      notifyError(err)
    },
  })

  const busy = active || create.isPending || restore.isPending || remove.isPending
  const visibleJob = job !== null && job.id !== dismissed && (active || job.id === watched || job.finished_at >= openedAt) ? job : null

  if (overview.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  if (overview.isError) {
    return (
      <p role="alert" className="text-destructive text-sm">
        {errorMessage(overview.error, t)}
      </p>
    )
  }
  const data = overview.data
  if (!data.available) {
    return (
      <p role="status" className="rounded-md border p-4 text-sm">
        {t('backups.unavailable')}
      </p>
    )
  }

  return (
    <div className="space-y-4">
      {visibleJob ? <JobStatus job={visibleJob} onDismiss={() => setDismissed(visibleJob.id)} /> : null}
      <SectionCard
        title={t('backups.snapshots.title')}
        actions={
          <Button type="button" size="sm" disabled={busy} onClick={() => setCreating(true)}>
            {t('backups.create.button')}
          </Button>
        }
      >
        <SnapshotList
          serverId={server.id}
          snapshots={data.snapshots}
          busy={busy}
          pendingId={pin.isPending ? (pin.variables?.s.id ?? null) : null}
          onPin={(s, pinned) => pin.mutate({ s, pinned })}
          onRestore={setRestoring}
          onDelete={setDeleting}
        />
      </SectionCard>
      {creating ? <CreateBackupDialog pending={create.isPending} onClose={() => setCreating(false)} onSubmit={(note) => create.mutate(note)} /> : null}
      {restoring ? (
        <RestoreDialog
          serverName={server.name}
          when={fmt.dateTime(restoring.time)}
          pending={restore.isPending}
          onClose={() => setRestoring(null)}
          onConfirm={() => restore.mutate(restoring)}
        />
      ) : null}
      {deleting ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setDeleting(null))}
          title={t('backups.delete.title', { when: fmt.dateTime(deleting.time) })}
          description={t('backups.delete.description')}
          confirmLabel={t('backups.delete.submit')}
          destructive
          pending={remove.isPending || busy}
          onConfirm={() => remove.mutate(deleting)}
        />
      ) : null}
    </div>
  )
}
