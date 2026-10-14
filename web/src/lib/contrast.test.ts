import { describe, expect, it } from 'vitest'
import { contrastRatio } from './contrast'

describe('contrastRatio', () => {
  it('is 21 for black on white and symmetric', () => {
    expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 1)
    expect(contrastRatio('#ffffff', '#000000')).toBeCloseTo(21, 1)
  })
  it('is 1 for identical colours', () => {
    expect(contrastRatio('#5cc14a', '#5cc14a')).toBeCloseTo(1, 5)
  })
  it('accepts short hex', () => {
    expect(contrastRatio('#fff', '#000')).toBeCloseTo(21, 1)
  })
  it('rejects non-hex input', () => {
    expect(() => contrastRatio('red', '#000')).toThrow()
  })
})
