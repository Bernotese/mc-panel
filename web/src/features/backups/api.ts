import { queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { isApiError, unwrap } from '@/api/errors'
import type { BackupJob, BackupJobResponse, BackupJobStarted, BackupOverview, BackupSchedule, BackupSnapshot, UpdateBackupScheduleRequest } from '@/api/types'
import { serverKey } from '@/features/servers/api'

/** The job is polled every 2 s while it is queued or running (spec §6.1). */
export const JOB_POLL_MS = 2_000

/** Below serverKey(id): power actions and settings changes (time zone) refresh them via the serversKey prefix. */
export const overviewKey = (id: number) => ['servers', id, 'backups', 'overview'] as const
export const backupJobKey = (id: number) => ['servers', id, 'backups', 'job'] as const

export function overviewQueryOptions(id: number) {
  return queryOptions<BackupOverview>({
    queryKey: overviewKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/backups', { params: { path: { id } } })),
  })
}

export function backupJobQueryOptions(id: number) {
  return queryOptions<BackupJobResponse>({
    queryKey: backupJobKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/backups/job', { params: { path: { id } } })),
  })
}

/** A 202 always carries a job (V3 BackupJobStarted). */
function requireJob(r: BackupJobStarted): BackupJob {
  return r.job
}

/** note "" = no note: the body stays empty (V3 CreateBackupRequest.note is optional). */
export async function createBackup(id: number, note: string): Promise<BackupJob> {
  const body = note === '' ? {} : { note }
  return requireJob(await unwrap(api.POST('/servers/{id}/backups', { params: { path: { id } }, body })))
}

export async function restoreBackup(id: number, snapshot: string): Promise<BackupJob> {
  return requireJob(await unwrap(api.POST('/servers/{id}/backups/{snapshot}/restore', { params: { path: { id, snapshot } } })))
}

export async function deleteBackup(id: number, snapshot: string): Promise<BackupJob> {
  return requireJob(await unwrap(api.DELETE('/servers/{id}/backups/{snapshot}', { params: { path: { id, snapshot } } })))
}

/** restic rewrites a pinned snapshot: the answer carries the NEW id (spec §3.3). */
export async function pinBackup(id: number, snapshot: string, pinned: boolean): Promise<BackupSnapshot> {
  const r = await unwrap(api.POST('/servers/{id}/backups/{snapshot}/pin', { params: { path: { id, snapshot } }, body: { pinned } }))
  return r.snapshot
}

export async function updateBackupSchedule(id: number, body: UpdateBackupScheduleRequest): Promise<BackupSchedule> {
  const r = await unwrap(api.PUT('/servers/{id}/backups/schedule', { params: { path: { id } }, body }))
  return r.schedule
}

/** Only for ids that passed isSnapshotId; the panel audits the download. */
export function backupDownloadUrl(id: number, snapshot: string): string {
  return `/api/servers/${id}/backups/${encodeURIComponent(snapshot)}/download`
}

/** The 202 answer becomes the job query's data, which starts the 2-s polling (same pattern as plugins in 3c). */
export function startBackupJob(queryClient: QueryClient, id: number, job: BackupJob): void {
  queryClient.setQueryData<BackupJobResponse>(backupJobKey(id), { job })
}

/** 409 backup_running = a job this tab may not know about (another tab, the schedule): ask for it again. */
export function refreshJobOnConflict(queryClient: QueryClient, id: number, err: unknown): void {
  if (isApiError(err, 'backup_running')) void queryClient.invalidateQueries({ queryKey: backupJobKey(id) })
}

/** After a pin the old id is gone; a second click must use the new one at once. */
export function replaceSnapshot(queryClient: QueryClient, id: number, oldId: string, next: BackupSnapshot): void {
  queryClient.setQueryData<BackupOverview>(overviewKey(id), (prev) =>
    prev ? { ...prev, snapshots: prev.snapshots.map((s) => (s.id === oldId ? next : s)) } : prev,
  )
}

export function setSchedule(queryClient: QueryClient, id: number, schedule: BackupSchedule): void {
  queryClient.setQueryData<BackupOverview>(overviewKey(id), (prev) => (prev ? { ...prev, schedule } : prev))
}

/**
 * Queries a finished restore makes stale: everything below serverKey(id) (state, files, plugins, players, backups) as a
 * prefix, and the file editor contents, which sit outside it (files/api.ts fileContentKey).
 */
export function restoredQueryKeys(id: number): readonly (readonly unknown[])[] {
  return [serverKey(id), ['file-content', id]]
}

export function refreshBackups(queryClient: QueryClient, id: number): Promise<void> {
  return queryClient.invalidateQueries({ queryKey: overviewKey(id) })
}
