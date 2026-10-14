import { describe, expect, it } from 'vitest'
import { fillHeight } from './fill-height'

describe('fillHeight', () => {
  it('fills the space from the element to the bottom of the window', () => {
    expect(fillHeight(900, 300, 24, 320)).toBe(576)
  })
  it('never goes below the minimum', () => {
    expect(fillHeight(600, 500, 24, 320)).toBe(320)
    expect(fillHeight(0, 0, 0, 320)).toBe(320)
  })
  it('rounds down to whole pixels', () => {
    expect(fillHeight(900.7, 300.2, 24, 320)).toBe(576)
  })
})
