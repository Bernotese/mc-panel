import { useCallback, useEffect, useReducer, useRef } from 'react'
import { isApiError } from '@/api/errors'
import { MAX_UPLOAD_BYTES } from './format'
import { componentProblem, joinPath } from './path'
import { UploadInterruptedError, uploadFile, type UploadTransport } from './upload'

export type UploadStatus = 'queued' | 'uploading' | 'conflict' | 'done' | 'failed' | 'cancelled' | 'skipped'

export interface UploadItem {
  id: number
  name: string
  path: string
  size: number
  loaded: number
  status: UploadStatus
  overwrite: boolean
  /** i18n key (local check) or the error thrown by the transport. */
  error: unknown
}

export type UploadAction =
  | { type: 'add'; items: UploadItem[] }
  | { type: 'start'; id: number }
  | { type: 'progress'; id: number; loaded: number }
  | { type: 'done'; id: number }
  | { type: 'failed'; id: number; error: unknown }
  | { type: 'conflict'; id: number }
  | { type: 'overwrite'; id: number }
  | { type: 'skip'; id: number }
  | { type: 'cancel'; id: number }
  | { type: 'clear' }

const FINISHED: ReadonlySet<UploadStatus> = new Set<UploadStatus>(['done', 'failed', 'cancelled', 'skipped'])

function update(items: UploadItem[], id: number, change: (item: UploadItem) => UploadItem): UploadItem[] {
  return items.map((item) => (item.id === id ? change(item) : item))
}

export function uploadReducer(items: UploadItem[], action: UploadAction): UploadItem[] {
  switch (action.type) {
    case 'add':
      return [...items, ...action.items]
    case 'start':
      return update(items, action.id, (i) => ({ ...i, status: 'uploading', loaded: 0, error: null }))
    case 'progress':
      return update(items, action.id, (i) => ({ ...i, loaded: action.loaded }))
    case 'done':
      return update(items, action.id, (i) => ({ ...i, status: 'done', loaded: i.size }))
    case 'failed':
      return update(items, action.id, (i) => ({ ...i, status: 'failed', error: action.error }))
    case 'conflict':
      return update(items, action.id, (i) => ({ ...i, status: 'conflict' }))
    case 'overwrite':
      return update(items, action.id, (i) => ({ ...i, status: 'queued', overwrite: true, loaded: 0 }))
    case 'skip':
      return update(items, action.id, (i) => ({ ...i, status: 'skipped' }))
    case 'cancel':
      return update(items, action.id, (i) => (FINISHED.has(i.status) ? i : { ...i, status: 'cancelled' }))
    case 'clear':
      return items.filter((i) => !FINISHED.has(i.status))
  }
}

/** One upload at a time (the panel allows two per user, spec §5.1); a pending conflict pauses the queue. */
export function nextToStart(items: UploadItem[]): UploadItem | undefined {
  if (items.some((i) => i.status === 'uploading' || i.status === 'conflict')) return undefined
  return items.find((i) => i.status === 'queued')
}

export interface UploadQueueOptions {
  onUploaded(): void
  transport?: UploadTransport
  /**
   * Whether the (cached) listing already shows a file at path: such an upload asks "overwrite?" before a byte is sent.
   * The panel's own 409 can otherwise arrive as a reset connection while the browser still sends (HTTP/1.1).
   */
  exists?(path: string): boolean
}

/** Uploads that leaving the files tab would cancel or drop. */
const PENDING: ReadonlySet<UploadStatus> = new Set<UploadStatus>(['queued', 'uploading', 'conflict'])

export function useUploadQueue(serverId: number, { onUploaded, transport = uploadFile, exists }: UploadQueueOptions) {
  const [items, dispatch] = useReducer(uploadReducer, [])
  const files = useRef(new Map<number, File>())
  const controllers = useRef(new Map<number, AbortController>())
  const seq = useRef(0)
  const latest = useRef({ onUploaded, transport, exists })
  useEffect(() => {
    latest.current = { onUploaded, transport, exists }
  })

  const next = nextToStart(items)
  const nextId = next?.id
  const nextPath = next?.path
  const nextOverwrite = next?.overwrite ?? false

  useEffect(() => {
    if (nextId === undefined || nextPath === undefined) return
    const file = files.current.get(nextId)
    if (!file) return
    if (!nextOverwrite && latest.current.exists?.(nextPath)) {
      dispatch({ type: 'conflict', id: nextId })
      return
    }
    const controller = new AbortController()
    controllers.current.set(nextId, controller)
    dispatch({ type: 'start', id: nextId })
    latest.current
      .transport(serverId, nextPath, file, {
        overwrite: nextOverwrite,
        signal: controller.signal,
        onProgress: (loaded) => dispatch({ type: 'progress', id: nextId, loaded }),
      })
      .then(() => {
        dispatch({ type: 'done', id: nextId })
        files.current.delete(nextId)
        latest.current.onUploaded()
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') dispatch({ type: 'cancel', id: nextId })
        else if (isApiError(err, 'file_exists') && !nextOverwrite) dispatch({ type: 'conflict', id: nextId })
        else if (err instanceof UploadInterruptedError) dispatch({ type: 'failed', id: nextId, error: 'files.uploads.interrupted' })
        else dispatch({ type: 'failed', id: nextId, error: err })
      })
      .finally(() => controllers.current.delete(nextId))
  }, [serverId, nextId, nextPath, nextOverwrite])

  // Only queued/uploading/conflict items still need their File; release the rest so memory can be reclaimed.
  useEffect(() => {
    for (const item of items) {
      if (item.status === 'done' || item.status === 'failed' || item.status === 'cancelled' || item.status === 'skipped') {
        files.current.delete(item.id)
      }
    }
  }, [items])

  // Leaving the tab cancels running uploads; the service discards the temp file (spec §4).
  useEffect(() => {
    const running = controllers.current
    return () => {
      for (const controller of running.values()) controller.abort()
    }
  }, [])

  const add = useCallback((list: File[], dir: string) => {
    const added: UploadItem[] = list.map((file) => {
      seq.current += 1
      const id = seq.current
      files.current.set(id, file)
      const problem = componentProblem(file.name, dir)
      const tooLarge = file.size > MAX_UPLOAD_BYTES
      return {
        id,
        name: file.name,
        path: joinPath(dir, file.name),
        size: file.size,
        loaded: 0,
        status: problem || tooLarge ? 'failed' : 'queued',
        overwrite: false,
        error: problem ? `files.validation.${problem}` : tooLarge ? 'files.validation.too_large' : null,
      }
    })
    dispatch({ type: 'add', items: added })
  }, [])

  const cancel = useCallback((id: number) => {
    controllers.current.get(id)?.abort()
    dispatch({ type: 'cancel', id })
  }, [])
  const overwrite = useCallback((id: number) => dispatch({ type: 'overwrite', id }), [])
  const skip = useCallback((id: number) => dispatch({ type: 'skip', id }), [])
  const clear = useCallback(() => dispatch({ type: 'clear' }), [])

  const active = items.some((i) => PENDING.has(i.status))

  return { items, active, add, cancel, overwrite, skip, clear }
}
