import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { BackupJob } from '@/api/types'
import { serverKey } from '@/features/servers/api'
import { backupJobQueryOptions, JOB_POLL_MS, refreshBackups } from './api'
import { isJobActive, jobTitleKey } from './model'

const active = (job: BackupJob | null | undefined) => job?.state === 'queued' || job?.state === 'running'

/**
 * The server's backup job (spec §5.3/§6.1): polled every 2 s while queued or running, otherwise not at all. When a job
 * this tab saw active ends, the snapshots are reloaded (after a restore also the server state) and a toast reports the
 * result. A vanished active job (panel restart) or a new one the tab did not see start reloads without a toast.
 */
export function useBackupJob(serverId: number): { job: BackupJob | null; active: boolean } {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    ...backupJobQueryOptions(serverId),
    refetchInterval: (q) => (isJobActive(q.state.data) ? JOB_POLL_MS : false),
  })
  const data = query.data
  // undefined = no answer yet: the first answer only tells what was there before the tab looked.
  const last = useRef<BackupJob | null | undefined>(undefined)
  useEffect(() => {
    if (data === undefined) return
    const job = data.job
    const previous = last.current
    last.current = job
    if (previous === undefined) return
    const reload = (restore: boolean) => {
      void refreshBackups(queryClient, serverId)
      if (restore) void queryClient.invalidateQueries({ queryKey: serverKey(serverId), exact: true })
    }
    if (active(previous) && job !== null && job.id === previous?.id) {
      if (active(job)) return
      if (job.state === 'done') toast.success(t(jobTitleKey(job)))
      else toast.error(t(jobTitleKey(job)))
      reload(job.kind === 'restore')
      return
    }
    const vanished = active(previous) && job === null
    // A job this tab started itself (still active) reloads when it ends; any other new id may have changed the repo.
    const replaced = job !== null && job.id !== previous?.id && (active(previous) || !active(job))
    if (vanished || replaced) reload(previous?.kind === 'restore' || job?.kind === 'restore')
  }, [data, queryClient, serverId, t])
  const job = data?.job ?? null
  return { job, active: active(job) }
}
