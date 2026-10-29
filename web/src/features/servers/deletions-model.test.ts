import { describe, expect, it } from 'vitest'
import type { ServerDeletion } from '@/api/types'
import { DELETION_FAST_POLL_MS, DELETION_SLOW_POLL_MS, deletionsPollInterval, isRetryable, sortDeletions } from './deletions-model'

function deletion(over: Partial<ServerDeletion>): ServerDeletion {
  return {
    server_id: 1,
    name: 'x',
    state: 'done',
    container: 'removed',
    container_error: null,
    backups: 'none',
    backups_error: null,
    requested_at: 0,
    updated_at: 0,
    completed_at: 0,
    ...over,
  }
}

describe('deletions model (#148)', () => {
  it('polls fast while a remove waits for the agent, slowly while one is retried, not at all when done', () => {
    expect(deletionsPollInterval(undefined)).toBe(false)
    expect(deletionsPollInterval([deletion({})])).toBe(false)
    expect(deletionsPollInterval([deletion({}), deletion({ state: 'partial' })])).toBe(DELETION_SLOW_POLL_MS)
    expect(deletionsPollInterval([deletion({ state: 'failed' }), deletion({ state: 'pending' })])).toBe(DELETION_FAST_POLL_MS)
  })

  it('offers a retry only for failed and partial deletions', () => {
    expect((['pending', 'failed', 'partial', 'done'] as const).filter((state) => isRetryable(deletion({ state })))).toEqual(['failed', 'partial'])
  })

  it('lists open deletions first, newest first', () => {
    const list = [deletion({ name: 'old-done', requested_at: 1 }), deletion({ name: 'open', state: 'failed', requested_at: 2 }), deletion({ name: 'new-done', requested_at: 3 })]
    expect(sortDeletions(list).map((d) => d.name)).toEqual(['open', 'new-done', 'old-done'])
  })
})
