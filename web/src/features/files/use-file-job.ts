import { useQuery } from '@tanstack/react-query'
import { JOB_POLL_MS, fileJobQueryOptions } from './api'
import type { FileJobResponse } from '@/api/types'

export function isExtracting(data: FileJobResponse | undefined): boolean {
  return data?.job?.state === 'running'
}

/** Shared by the files tab and the detail page (same query key): polls only while a job runs. */
export function useFileJob(serverId: number, enabled: boolean) {
  return useQuery({
    ...fileJobQueryOptions(serverId),
    enabled,
    refetchInterval: (query) => (isExtracting(query.state.data) ? JOB_POLL_MS : false),
  })
}
