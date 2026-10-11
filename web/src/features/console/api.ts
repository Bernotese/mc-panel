import { api } from '@/api/client'
import { toApiError, unwrap } from '@/api/errors'
import type { ConsoleCommandResponse } from '@/api/types'

/** Same origin as the API client; EventSource sends the session cookie by itself. */
export function consoleStreamUrl(id: number): string {
  return `/api/servers/${id}/console/stream`
}

/**
 * EventSource reports a refused stream only as "error" with readyState CLOSED and no status code. This asks once
 * via the API client (whose middleware handles 401 → login) with tail=0 and closes an opened stream right away.
 * Returns null if the stream answered 200, otherwise the error (ApiError or network error).
 */
export async function probeConsoleStream(id: number): Promise<unknown> {
  try {
    const { data, error, response } = await api.GET('/servers/{id}/console/stream', {
      params: { path: { id }, query: { tail: 0 } }, // tail=0: the probe must not replay the log
      parseAs: 'stream',
      headers: { Accept: 'text/event-stream' },
    })
    if (response.ok) {
      await data?.cancel()
      return null
    }
    return toApiError(response, error)
  } catch (err) {
    return err
  }
}

export function sendConsoleCommand(id: number, command: string): Promise<ConsoleCommandResponse> {
  return unwrap(api.POST('/servers/{id}/console', { params: { path: { id } }, body: { command } }))
}
