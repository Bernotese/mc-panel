import { useQuery } from '@tanstack/react-query'
import { serverDeletionsQueryOptions } from './api'
import { deletionsPollInterval } from './deletions-model'

/** The admin's deletions (#148), polled while one is open; `enabled` keeps other users from asking. */
export function useServerDeletions(enabled: boolean) {
  return useQuery({ ...serverDeletionsQueryOptions, enabled, refetchInterval: (q) => deletionsPollInterval(q.state.data) })
}
