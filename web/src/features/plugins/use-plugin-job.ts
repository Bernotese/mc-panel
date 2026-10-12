import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { PluginJob } from '@/api/types'
import { JOB_POLL_MS, pluginJobQueryOptions, refreshPluginState } from './api'
import { isJobRunning } from './model'

/**
 * The server's plugin job (spec §6.4/§7.1): polled every 2 s while it runs, otherwise not at all. When a job this tab
 * saw running ends, inventory, updates, "installiert" badges and previews are reloaded and a toast reports the result.
 */
export function usePluginJob(serverId: number): { job: PluginJob | null; running: boolean } {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    ...pluginJobQueryOptions(serverId),
    refetchInterval: (q) => (isJobRunning(q.state.data) ? JOB_POLL_MS : false),
  })
  const job = query.data?.job ?? null
  const lastState = useRef(job?.state)
  useEffect(() => {
    const previous = lastState.current
    lastState.current = job?.state
    if (previous !== 'running' || !job || job.state === 'running') return
    if (job.state === 'done') toast.success(t('plugins.job.toast_done'))
    else toast.error(t('plugins.job.toast_failed'))
    void refreshPluginState(queryClient, serverId)
  }, [job, queryClient, serverId, t])
  return { job, running: job?.state === 'running' }
}
