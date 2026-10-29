import type { ServerDeletion } from '@/api/types'

/** While the agent has not confirmed a remove yet, its confirmation comes within seconds. */
export const DELETION_FAST_POLL_MS = 3_000
/** Failed and partial deletions are retried by the panel with back-off (10 s … 1 h); the list follows slowly. */
export const DELETION_SLOW_POLL_MS = 15_000

/** Done and abandoned deletions are closed (completed_at set); everything else is still being worked on. */
export function isOpen(d: ServerDeletion): boolean {
  return d.state !== 'done' && d.state !== 'abandoned'
}

/**
 * A failed or partial deletion can be retried at once or abandoned (admin, sudo); a pending one is still in its first
 * attempt.
 */
export function isRetryable(d: ServerDeletion): boolean {
  return d.state === 'failed' || d.state === 'partial'
}

/** No polling once everything is done. */
export function deletionsPollInterval(list: readonly ServerDeletion[] | undefined): number | false {
  if (!list?.some(isOpen)) return false
  return list.some((d) => d.state === 'pending') ? DELETION_FAST_POLL_MS : DELETION_SLOW_POLL_MS
}

/** Open deletions first, newest first within each group. */
export function sortDeletions(list: readonly ServerDeletion[]): ServerDeletion[] {
  return [...list].sort((a, b) => Number(isOpen(b)) - Number(isOpen(a)) || b.requested_at - a.requested_at)
}
