import { describe, expect, it } from 'vitest'
import { nextToStart, uploadReducer, type UploadItem } from './upload-queue'

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
