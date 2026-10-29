import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type {
  CreateServerRequest,
  InstanceType,
  PowerAction,
  PowerResponse,
  Recovery,
  Server,
  ServerDeletion,
  ServerSettings,
  UpdateServerRequest,
  UpdateServerSettingsRequest,
} from '@/api/types'

export const serversKey = ['servers'] as const
/** Detail keys share the list prefix, so invalidating serversKey refreshes open detail pages too. */
export const serverKey = (id: number) => ['servers', id] as const
export const catalogKey = (type: InstanceType) => ['server-catalog', type] as const
export const serverSettingsKey = ['server-settings'] as const
/** Under the list prefix: creating, changing or deleting a server (which invalidate serversKey) changes allocated_mib. */
export const memoryLimitsKey = ['servers', 'memory'] as const

/** The panel caches catalogs for an hour; on errors the form switches to free text instead of retrying. */
const CATALOG_STALE_MS = 60 * 60_000

export const serversQueryOptions = queryOptions({
  queryKey: serversKey,
  queryFn: () => unwrap(api.GET('/servers')),
})

export function serverQueryOptions(id: number) {
  return queryOptions({
    queryKey: serverKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}', { params: { path: { id } } })),
  })
}

export function catalogQueryOptions(type: InstanceType) {
  return queryOptions({
    queryKey: catalogKey(type),
    queryFn: () => unwrap(api.GET('/servers/catalog/{type}', { params: { path: { type } } })),
    staleTime: CATALOG_STALE_MS,
    retry: false,
  })
}

export const serverSettingsQueryOptions = queryOptions({
  queryKey: serverSettingsKey,
  queryFn: () => unwrap(api.GET('/servers/settings')),
})

/** Memory bounds of this host and the suggested size of a new server per type (issue #79). */
export const memoryLimitsQueryOptions = queryOptions({
  queryKey: memoryLimitsKey,
  queryFn: () => unwrap(api.GET('/servers/memory')),
})

export function createServer(body: CreateServerRequest): Promise<Server> {
  return unwrap(api.POST('/servers', { body }))
}

export function updateServer(id: number, body: UpdateServerRequest): Promise<Server> {
  return unwrap(api.PATCH('/servers/{id}', { params: { path: { id } }, body }))
}

/** Data safety spec §2.3 (admin, sudo): moves the half-swapped folders of a stuck restore out and frees the server. */
export function resolveRecovery(id: number): Promise<Recovery> {
  return unwrap(api.POST('/servers/{id}/recovery/resolve', { params: { path: { id } } }))
}

/** purgeBackups also removes the server's restic repository (backups spec §5.6); the parameter is only sent when true. */
/** 202 with the deletion's first state (#148): the server is gone from the panel, its cleanup may still be running. */
export function deleteServer(id: number, purgeBackups = false): Promise<ServerDeletion> {
  return unwrap(api.DELETE('/servers/{id}', { params: { path: { id }, query: purgeBackups ? { purge_backups: true } : undefined } }))
}

/** Under the list prefix, so the list's invalidations refresh it too. Admins only. */
export const serverDeletionsKey = ['servers', 'deletions'] as const

export const serverDeletionsQueryOptions = queryOptions<ServerDeletion[]>({
  queryKey: serverDeletionsKey,
  queryFn: () => unwrap(api.GET('/servers/deletions')),
})

/**
 * Admin, sudo: closes a deletion that cannot finish as abandoned and frees the name; nothing is removed, the leftovers
 * stay on the host for the operator.
 */
export function abandonServerDeletion(id: number): Promise<ServerDeletion> {
  return unwrap(api.POST('/servers/deletions/{id}/abandon', { params: { path: { id } } }))
}

/** Admin, sudo: runs the next cleanup attempt of a deletion at once. */
export function retryServerDeletion(id: number): Promise<ServerDeletion> {
  return unwrap(api.POST('/servers/deletions/{id}/retry', { params: { path: { id } } }))
}

export function powerServer(id: number, action: PowerAction): Promise<PowerResponse> {
  return unwrap(api.POST('/servers/{id}/power', { params: { path: { id } }, body: { action } }))
}

export function updateServerSettings(body: UpdateServerSettingsRequest): Promise<ServerSettings> {
  return unwrap(api.PUT('/servers/settings', { body }))
}
