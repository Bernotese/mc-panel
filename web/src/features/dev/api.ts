import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { CreateDevRequest, DevInstance, DevOps, Server, UpdateDevRequest } from '@/api/types'

/** A sync takes minutes; while one is pending the dev's state is polled at this pace. */
export const DEV_PENDING_POLL_MS = 3_000

/** Below serverKey(id): invalidating serversKey (power, settings, deletes) refreshes them too. */
export const devKey = (id: number) => ['servers', id, 'dev'] as const
export const devOpsKey = (id: number) => ['servers', id, 'dev', 'ops'] as const

export function devQueryOptions(id: number) {
  return queryOptions<DevInstance>({
    queryKey: devKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/dev', { params: { path: { id } } })),
  })
}

export function devOpsQueryOptions(id: number) {
  return queryOptions<DevOps>({
    queryKey: devOpsKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/dev/ops', { params: { path: { id } } })),
  })
}

/** Creates dev-<name> of prod `id` (admin, sudo); the answer is the new dev server. */
export function createDev(id: number, body: CreateDevRequest): Promise<Server> {
  return unwrap(api.POST('/servers/{id}/dev', { params: { path: { id } }, body }))
}

export function updateDev(id: number, body: UpdateDevRequest): Promise<DevInstance> {
  return unwrap(api.PATCH('/servers/{id}/dev', { params: { path: { id } }, body }))
}

export function syncDev(id: number): Promise<DevInstance> {
  return unwrap(api.POST('/servers/{id}/dev/sync', { params: { path: { id } } }))
}

export function addDevOp(id: number, name: string): Promise<DevOps> {
  return unwrap(api.POST('/servers/{id}/dev/ops', { params: { path: { id } }, body: { name } }))
}

export function removeDevOp(id: number, name: string): Promise<DevOps> {
  return unwrap(api.DELETE('/servers/{id}/dev/ops/{name}', { params: { path: { id, name } } }))
}
