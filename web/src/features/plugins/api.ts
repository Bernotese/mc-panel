import { infiniteQueryOptions, queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { isApiError, unwrap, unwrapVoid } from '@/api/errors'
import type {
  InstallPluginRequest,
  MrpackImportRequest,
  MrpackIndex,
  MrpackPlan,
  PluginInventory,
  PluginJob,
  PluginJobResponse,
  PluginResolution,
  PluginSearchResult,
  PluginSource,
  PluginUpdates,
  PluginVersionList,
} from '@/api/types'

/** The job is polled every 2 s while it runs (spec §7.1). */
export const JOB_POLL_MS = 2_000
/** The search waits for a typing pause (spec §7.1 "Suchfeld verzögert"). */
export const SEARCH_DEBOUNCE_MS = 400
/** Contract limits of GET …/plugins/search (V3). */
export const MAX_QUERY_LENGTH = 100
export const MAX_SEARCH_OFFSET = 1000
/** Like the panel caches (search 5 min, versions 15 min): switching views does not ask again. */
const SEARCH_STALE_MS = 5 * 60_000
const VERSIONS_STALE_MS = 15 * 60_000

/** Below serverKey(id): a power action refreshes restart_needed together with the server. */
export const inventoryKey = (id: number) => ['servers', id, 'plugins', 'inventory'] as const
export const updatesKey = (id: number) => ['servers', id, 'plugins', 'updates'] as const
export const pluginJobKey = (id: number) => ['servers', id, 'plugins', 'job'] as const
/**
 * Deliberately NOT below ['servers', …]: every power action invalidates the server queries, and re-running all loaded
 * search pages, version lists and previews would cost upstream calls and the 60/min limit (spec §6.6).
 */
export const searchesKey = (id: number) => ['plugin-upstream', id, 'search'] as const
export const searchKey = (id: number, source: PluginSource, q: string) => ['plugin-upstream', id, 'search', source, q] as const
export const versionsKey = (id: number, source: PluginSource, project: string) => ['plugin-upstream', id, 'versions', source, project] as const
export const resolvesKey = (id: number) => ['plugin-upstream', id, 'resolve'] as const
export const resolveKey = (id: number, source: PluginSource, project: string, version: string) =>
  ['plugin-upstream', id, 'resolve', source, project, version] as const

/** Below resolvesKey: a finished job or a removal makes the mrpack preview stale like the install previews. */
export const mrpackPreviewKey = (id: number, token: number) => [...resolvesKey(id), 'mrpack', token] as const

export function inventoryQueryOptions(id: number) {
  return queryOptions<PluginInventory>({
    queryKey: inventoryKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins', { params: { path: { id } } })),
  })
}

/** Served from the panel's 1-h cache; no upstream call per tab visit. */
export function updatesQueryOptions(id: number) {
  return queryOptions<PluginUpdates>({
    queryKey: updatesKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/updates', { params: { path: { id } } })),
    retry: false,
  })
}

/** "Nach Updates suchen": bypasses the cache and counts against the 60/min limit (spec §6.6). */
export function checkUpdates(id: number): Promise<PluginUpdates> {
  return unwrap(api.GET('/servers/{id}/plugins/updates', { params: { path: { id }, query: { refresh: true } } }))
}

export function nextOffset(last: PluginSearchResult): number | undefined {
  const next = last.offset + last.hits.length
  return last.hits.length > 0 && next < last.total && next <= MAX_SEARCH_OFFSET ? next : undefined
}

/** Empty q = the most popular matching projects (spec §7.1); 20 hits per page, "Mehr laden" via offset. */
export function searchQueryOptions(id: number, source: PluginSource, q: string) {
  return infiniteQueryOptions({
    queryKey: searchKey(id, source, q),
    initialPageParam: 0,
    queryFn: ({ pageParam }): Promise<PluginSearchResult> =>
      unwrap(
        api.GET('/servers/{id}/plugins/search', {
          params: { path: { id }, query: { source, q: q === '' ? undefined : q, offset: pageParam === 0 ? undefined : pageParam } },
        }),
      ),
    getNextPageParam: (last) => nextOffset(last),
    staleTime: SEARCH_STALE_MS,
    retry: false,
  })
}

export function versionsQueryOptions(id: number, source: PluginSource, project: string) {
  return queryOptions<PluginVersionList>({
    queryKey: versionsKey(id, source, project),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/versions', { params: { path: { id }, query: { source, project } } })),
    staleTime: VERSIONS_STALE_MS,
    retry: false,
  })
}

/** Depends on the inventory ("Bereits vorhanden"): refreshPluginState invalidates it after every change. */
export function resolveQueryOptions(id: number, source: PluginSource, project: string, version: string) {
  return queryOptions<PluginResolution>({
    queryKey: resolveKey(id, source, project, version),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/resolve', { params: { path: { id }, query: { source, project, version } } })),
    retry: false,
  })
}

/** The plan of a modpack import (mrpack spec §3); token tells two chosen files apart. */
export function mrpackPreviewQueryOptions(id: number, token: number, index: MrpackIndex | undefined) {
  return queryOptions<MrpackPlan>({
    queryKey: mrpackPreviewKey(id, token),
    queryFn: () => unwrap(api.POST('/servers/{id}/plugins/mrpack/preview', { params: { path: { id } }, body: index as MrpackIndex })),
    enabled: index !== undefined,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  })
}

export function importMrpack(id: number, body: MrpackImportRequest): Promise<PluginJob> {
  return unwrap(api.POST('/servers/{id}/plugins/mrpack/import', { params: { path: { id } }, body }))
}

export function installPlugin(id: number, body: InstallPluginRequest): Promise<PluginJob> {
  return unwrap(api.POST('/servers/{id}/plugins/install', { params: { path: { id } }, body }))
}

/** files = [] updates every jar with an update (V3). */
export function updatePlugins(id: number, files: string[]): Promise<PluginJob> {
  return unwrap(api.POST('/servers/{id}/plugins/update', { params: { path: { id } }, body: { files } }))
}

export function togglePlugin(id: number, file: string, enabled: boolean): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/plugins/toggle', { params: { path: { id } }, body: { file, enabled } }))
}

export function deletePlugin(id: number, file: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/plugins/delete', { params: { path: { id } }, body: { file } }))
}

export function pluginJobQueryOptions(id: number) {
  return queryOptions<PluginJobResponse>({
    queryKey: pluginJobKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/jobs', { params: { path: { id } } })),
  })
}

/** The 202 answer becomes the job query's data, which starts the 2-s polling (same pattern as extracting in 3b). */
export function startJob(queryClient: QueryClient, id: number, job: PluginJob): void {
  queryClient.setQueryData<PluginJobResponse>(pluginJobKey(id), { job })
}

/**
 * 409 conflict = a job of this server runs that this tab may not know about (another browser tab or user started it):
 * asking for it again starts the polling, the locks and the reload when it ends.
 */
export function refreshJobOnConflict(queryClient: QueryClient, id: number, err: unknown): void {
  if (isApiError(err, 'conflict')) void queryClient.invalidateQueries({ queryKey: pluginJobKey(id) })
}

/**
 * After a toggle the jar list and its updates are stale, but not the search or the previews: a disabled jar still counts
 * as installed (spec §6.3), and asking Modrinth/Hangar again would only cost upstream calls and the 60/min limit.
 */
export function refreshInventory(queryClient: QueryClient, id: number): Promise<unknown> {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: inventoryKey(id) }),
    queryClient.invalidateQueries({ queryKey: updatesKey(id) }),
  ])
}

/** After a job or a removal the disk changed: inventory, updates, "installiert" badges and previews are stale. */
export function refreshPluginState(queryClient: QueryClient, id: number): Promise<unknown> {
  return Promise.all([
    refreshInventory(queryClient, id),
    queryClient.invalidateQueries({ queryKey: searchesKey(id) }),
    queryClient.invalidateQueries({ queryKey: resolvesKey(id) }),
  ])
}
