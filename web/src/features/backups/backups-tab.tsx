import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { createBackup, overviewQueryOptions, refreshJobOnConflict, startBackupJob } from './api'
import { CreateBackupDialog } from './create-dialog'
import { JobStatus } from './job-status'
import { useBackupErrorToast } from './messages'
import { SnapshotList } from './snapshot-list'
import { useBackupJob } from './use-backup-job'

export function BackupsTab({ server }: { server: Server }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyToast = useBackupErrorToast()
  const notifyError = (err: unknown) => {
    refreshJobOnConflict(queryClient, server.id, err)
    notifyToast(err)
  }
  const overview = useQuery(overviewQueryOptions(server.id))
  const { job, active } = useBackupJob(server.id)
  const [creating, setCreating] = useState(false)
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

  const busy = active || create.isPending
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
        <SnapshotList serverId={server.id} snapshots={data.snapshots} busy={busy} pendingId={null} />
      </SectionCard>
      {creating ? <CreateBackupDialog pending={create.isPending} onClose={() => setCreating(false)} onSubmit={(note) => create.mutate(note)} /> : null}
    </div>
  )
}
