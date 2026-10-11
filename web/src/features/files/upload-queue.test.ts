import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/errors'
import type { UploadTransport } from './upload'
import { nextToStart, uploadReducer, useUploadQueue, type UploadItem } from './upload-queue'

const item = (id: number, status: UploadItem['status'] = 'queued'): UploadItem => ({
  id,
  name: `f${id}`,
  path: `f${id}`,
  size: 100,
  loaded: 0,
  status,
  overwrite: false,
  error: null,
})

describe('uploadReducer', () => {
  it('adds, starts, tracks progress and finishes', () => {
    let items = uploadReducer([], { type: 'add', items: [item(1), item(2)] })
    items = uploadReducer(items, { type: 'start', id: 1 })
    items = uploadReducer(items, { type: 'progress', id: 1, loaded: 40 })
    expect(items[0]).toMatchObject({ status: 'uploading', loaded: 40 })
    items = uploadReducer(items, { type: 'done', id: 1 })
    expect(items[0]).toMatchObject({ status: 'done', loaded: 100 })
  })

  it('pauses on a conflict until the user decides', () => {
    let items = uploadReducer([item(1, 'uploading'), item(2)], { type: 'conflict', id: 1 })
    expect(nextToStart(items)).toBeUndefined()
    items = uploadReducer(items, { type: 'overwrite', id: 1 })
    expect(items[0]).toMatchObject({ status: 'queued', overwrite: true, loaded: 0 })
    expect(nextToStart(items)?.id).toBe(1)
    items = uploadReducer([item(1, 'conflict'), item(2)], { type: 'skip', id: 1 })
    expect(items[0].status).toBe('skipped')
    expect(nextToStart(items)?.id).toBe(2)
  })

  it('runs one upload at a time', () => {
    expect(nextToStart([item(1, 'uploading'), item(2)])).toBeUndefined()
    expect(nextToStart([item(1, 'done'), item(2)])?.id).toBe(2)
    expect(nextToStart([item(1, 'failed'), item(2, 'cancelled')])).toBeUndefined()
  })

  it('cancels only unfinished items and clears finished ones', () => {
    let items = uploadReducer([item(1, 'done'), item(2, 'queued'), item(3, 'uploading')], { type: 'cancel', id: 2 })
    items = uploadReducer(items, { type: 'cancel', id: 1 })
    expect(items.map((i) => i.status)).toEqual(['done', 'cancelled', 'uploading'])
    items = uploadReducer(items, { type: 'clear' })
    expect(items.map((i) => i.id)).toEqual([3])
  })

  it('records failures', () => {
    const items = uploadReducer([item(1, 'uploading')], { type: 'failed', id: 1, error: 'boom' })
    expect(items[0]).toMatchObject({ status: 'failed', error: 'boom' })
  })
})

interface Call {
  path: string
  overwrite: boolean
  signal: AbortSignal
  resolve(): void
  reject(err: unknown): void
}

/** A transport whose uploads finish only when the test says so; it rejects with AbortError like XHR does. */
function fakeTransport() {
  const calls: Call[] = []
  const transport: UploadTransport = (_serverId, path, _file, options) =>
    new Promise((resolve, reject) => {
      const call: Call = { path, overwrite: options.overwrite, signal: options.signal as AbortSignal, resolve: () => resolve({ size: 1 }), reject }
      options.signal?.addEventListener('abort', () => reject(new DOMException('cancelled', 'AbortError')))
      calls.push(call)
    })
  return { calls, transport }
}

describe('useUploadQueue', () => {
  it('uploads in the order the files were added, one at a time', async () => {
    const { calls, transport } = fakeTransport()
    const onUploaded = vi.fn()
    const { result } = renderHook(() => useUploadQueue(1, { onUploaded, transport }))
    act(() => result.current.add([new File(['a'], 'a.txt'), new File(['b'], 'b.txt')], 'plugins'))
    expect(calls.map((c) => c.path)).toEqual(['plugins/a.txt'])
    await act(async () => calls[0].resolve())
    expect(calls.map((c) => c.path)).toEqual(['plugins/a.txt', 'plugins/b.txt'])
    await act(async () => calls[1].resolve())
    expect(result.current.items.map((i) => i.status)).toEqual(['done', 'done'])
    expect(onUploaded).toHaveBeenCalledTimes(2)
  })

  it('starts the next upload after cancelling the running one', async () => {
    const { calls, transport } = fakeTransport()
    const { result } = renderHook(() => useUploadQueue(1, { onUploaded: vi.fn(), transport }))
    act(() => result.current.add([new File(['a'], 'a.txt'), new File(['b'], 'b.txt')], ''))
    const first = result.current.items[0].id
    await act(async () => result.current.cancel(first))
    expect(calls[0].signal.aborted).toBe(true)
    expect(calls.map((c) => c.path)).toEqual(['a.txt', 'b.txt'])
    expect(result.current.items.map((i) => i.status)).toEqual(['cancelled', 'uploading'])
  })

  it('asks for a decision on file_exists and retries with overwrite', async () => {
    const { calls, transport } = fakeTransport()
    const { result } = renderHook(() => useUploadQueue(1, { onUploaded: vi.fn(), transport }))
    act(() => result.current.add([new File(['a'], 'a.txt')], ''))
    await act(async () => calls[0].reject(new ApiError({ status: 409, code: 'file_exists', message: 'exists' })))
    expect(result.current.items[0].status).toBe('conflict')
    act(() => result.current.overwrite(result.current.items[0].id))
    expect(calls[1]).toMatchObject({ path: 'a.txt', overwrite: true })
  })

  it('aborts running uploads when the tab unmounts', () => {
    const { calls, transport } = fakeTransport()
    const { result, unmount } = renderHook(() => useUploadQueue(1, { onUploaded: vi.fn(), transport }))
    act(() => result.current.add([new File(['a'], 'a.txt')], ''))
    expect(calls[0].signal.aborted).toBe(false)
    unmount()
    expect(calls[0].signal.aborted).toBe(true)
  })
})
