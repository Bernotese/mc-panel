import { afterEach, describe, expect, it, vi } from 'vitest'
import { readTreeOpen, TREE_STORAGE_KEY, writeTreeOpen } from './tree-state'

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('tree state', () => {
  it('defaults to open and round-trips', () => {
    expect(readTreeOpen()).toBe(true)
    writeTreeOpen(false)
    expect(localStorage.getItem(TREE_STORAGE_KEY)).toBe('closed')
    expect(readTreeOpen()).toBe(false)
    writeTreeOpen(true)
    expect(localStorage.getItem(TREE_STORAGE_KEY)).toBe('open')
    expect(readTreeOpen()).toBe(true)
  })
  it('stays usable when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(readTreeOpen()).toBe(true)
    expect(() => writeTreeOpen(false)).not.toThrow()
  })
})
