import { afterEach, describe, expect, it, vi } from 'vitest'
import { RAIL_STORAGE_KEY, readRailExpanded, writeRailExpanded } from './rail-state'

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('rail state', () => {
  it('defaults to collapsed and round-trips', () => {
    expect(readRailExpanded()).toBe(false)
    writeRailExpanded(true)
    expect(localStorage.getItem(RAIL_STORAGE_KEY)).toBe('expanded')
    expect(readRailExpanded()).toBe(true)
    writeRailExpanded(false)
    expect(localStorage.getItem(RAIL_STORAGE_KEY)).toBe('collapsed')
    expect(readRailExpanded()).toBe(false)
  })
  it('stays usable when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(readRailExpanded()).toBe(false)
    expect(() => writeRailExpanded(true)).not.toThrow()
  })
})
