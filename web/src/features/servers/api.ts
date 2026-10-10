import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type {
  CreateServerRequest,
  InstanceType,
  PowerAction,
  PowerResponse,
  Server,
  ServerSettings,
  UpdateServerRequest,
  UpdateServerSettingsRequest,
} from '@/api/types'

export const serversKey = ['servers'] as const
/** Detail keys share the list prefix, so invalidating serversKey refreshes open detail pages too. */
export const serverKey = (id: number) => ['servers', id] as const
export const catalogKey = (type: InstanceType) => ['server-catalog', type] as const
export const serverSettingsKey = ['server-settings'] as const

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

export function createServer(body: CreateServerRequest): Promise<Server> {
  return unwrap(api.POST('/servers', { body }))
}

export function updateServer(id: number, body: UpdateServerRequest): Promise<Server> {
  return unwrap(api.PATCH('/servers/{id}', { params: { path: { id } }, body }))
}

export function deleteServer(id: number): Promise<void> {
  return unwrapVoid(api.DELETE('/servers/{id}', { params: { path: { id } } }))
}

export function powerServer(id: number, action: PowerAction): Promise<PowerResponse> {
  return unwrap(api.POST('/servers/{id}/power', { params: { path: { id } }, body: { action } }))
}

export function updateServerSettings(body: UpdateServerSettingsRequest): Promise<ServerSettings> {
  return unwrap(api.PUT('/servers/settings', { body }))
}
