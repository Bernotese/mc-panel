import { QueryObserver } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { createQueryClient } from '@/app/query-client'
import { server } from '@/mocks/node'
import { BACKGROUND_HEADER, runInBackground } from './background'
import { api } from './client'
import { unwrap } from './errors'

function recordHeaders(): (string | null)[] {
  const seen: (string | null)[] = []
  server.use(
    http.get('*/api/servers', ({ request }) => {
      seen.push(request.headers.get(BACKGROUND_HEADER))
      return HttpResponse.json([])
    }),
  )
  return seen
}

describe('background requests (SEC-19)', () => {
  it('marks refetchInterval polls, but not the initial load or a refetch after a user action', async () => {
    const seen = recordHeaders()
    const client = createQueryClient()
    const observer = new QueryObserver(client, {
      queryKey: ['servers'],
      queryFn: () => unwrap(api.GET('/servers')),
      refetchInterval: 20,
    })
    const unsubscribe = observer.subscribe(() => undefined)
    try {
      await expect.poll(() => seen.length, { timeout: 2000 }).toBeGreaterThanOrEqual(3)
      expect(seen[0]).toBeNull()
      expect(seen.slice(1, 3)).toEqual(['1', '1'])
      const before = seen.length
      await client.refetchQueries({ queryKey: ['servers'] })
      expect(seen.slice(before)).toContain(null)
    } finally {
      unsubscribe()
      client.clear()
    }
  })

  it('leaves explicit requests unmarked', async () => {
    const seen = recordHeaders()
    await unwrap(api.GET('/servers'))
    await runInBackground(() => unwrap(api.GET('/servers')))
    expect(seen).toEqual([null, '1'])
  })
})
