import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { FileContent, FileJob, FileJobResponse, FileList, FileWriteResult, WriteFileRequest } from '@/api/types'

/** The job is polled every 2 s while it runs (spec §6.3). */
export const JOB_POLL_MS = 2_000

/** Below serverKey(id): invalidating the server also refreshes listings. */
export const filesKey = (id: number) => ['servers', id, 'files'] as const
export const fileListsKey = (id: number) => ['servers', id, 'files', 'list'] as const
export const fileListKey = (id: number, path: string) => ['servers', id, 'files', 'list', path] as const
export const fileJobKey = (id: number) => ['servers', id, 'files', 'job'] as const
/**
 * Deliberately NOT below ['servers', …]: power actions invalidate every server query, and a refetched file
 * would replace the mtime the editor compares against (spec §6.2: conflicts must be detected, not hidden).
 */
export const fileContentKey = (id: number, path: string) => ['file-content', id, path] as const

export function fileListQueryOptions(id: number, path: string) {
  return queryOptions<FileList>({
    queryKey: fileListKey(id, path),
    queryFn: () => unwrap(api.GET('/servers/{id}/files', { params: { path: { id }, query: { path } } })),
  })
}

export function readFile(id: number, path: string): Promise<FileContent> {
  return unwrap(api.GET('/servers/{id}/files/content', { params: { path: { id }, query: { path } } }))
}

export function writeFile(id: number, body: WriteFileRequest): Promise<FileWriteResult> {
  return unwrap(api.PUT('/servers/{id}/files/content', { params: { path: { id } }, body }))
}

export function makeDirectory(id: number, path: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/files/mkdir', { params: { path: { id } }, body: { path } }))
}

export function renameFile(id: number, from: string, to: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/files/rename', { params: { path: { id } }, body: { from, to } }))
}

export function deleteFile(id: number, path: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/files/delete', { params: { path: { id } }, body: { path } }))
}

export function extractArchive(id: number, path: string, dest: string): Promise<FileJob> {
  return unwrap(api.POST('/servers/{id}/files/extract', { params: { path: { id } }, body: { path, dest } }))
}

export function fileJobQueryOptions(id: number) {
  return queryOptions<FileJobResponse>({
    queryKey: fileJobKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/files/jobs', { params: { path: { id } } })),
  })
}

/**
 * A plain same-origin link: the browser streams the file (or the folder zip) to disk itself, so even a 4 GiB
 * world never passes through JavaScript memory. The session cookie authenticates it like the console stream.
 */
export function downloadUrl(id: number, path: string): string {
  return `/api/servers/${id}/files/download?path=${encodeURIComponent(path)}`
}
