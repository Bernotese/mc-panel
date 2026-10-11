import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { ConsoleCommandResponse, PlayerActionRequest } from '@/api/types'

/** Online players are refreshed every 15 s while the page is visible (TanStack pauses in the background). */
export const ONLINE_POLL_MS = 15_000

/** Below the server key: invalidating serverKey(id) or serversKey refreshes the player data as well. */
export const playersKey = (id: number) => ['servers', id, 'players'] as const
export const onlinePlayersKey = (id: number) => ['servers', id, 'players', 'online'] as const
export const playerListsKey = (id: number) => ['servers', id, 'players', 'lists'] as const

export function onlinePlayersQueryOptions(id: number) {
  return queryOptions({
    queryKey: onlinePlayersKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/players', { params: { path: { id } } })),
  })
}

export function playerListsQueryOptions(id: number) {
  return queryOptions({
    queryKey: playerListsKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/players/lists', { params: { path: { id } } })),
  })
}

export function playerAction(id: number, body: PlayerActionRequest): Promise<ConsoleCommandResponse> {
  return unwrap(api.POST('/servers/{id}/players/actions', { params: { path: { id } }, body }))
}
