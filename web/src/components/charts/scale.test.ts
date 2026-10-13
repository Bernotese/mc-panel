import { describe, expect, it } from 'vitest'
import { niceTicks, tickDecimals, timeTicks } from './scale'

describe('niceTicks', () => {
  it('uses 1/2/5 × 10^n steps that cover the range', () => {
    expect(niceTicks(0, 20)).toEqual({ min: 0, max: 20, step: 5, values: [0, 5, 10, 15, 20] })
    expect(niceTicks(0, 12)).toEqual({ min: 0, max: 15, step: 5, values: [0, 5, 10, 15] })
    expect(niceTicks(19.2, 20)).toEqual({ min: 19.2, max: 20, step: 0.2, values: [19.2, 19.4, 19.6, 19.8, 20] })
  })

  it('opens up a flat or empty range instead of dividing by zero', () => {
    expect(niceTicks(0, 0)).toEqual({ min: 0, max: 1, step: 0.5, values: [0, 0.5, 1] })
    expect(niceTicks(5, 5)).toEqual({ min: 4.5, max: 5.5, step: 0.5, values: [4.5, 5, 5.5] })
  })

  it('swaps a reversed range and falls back for non-finite input', () => {
    expect(niceTicks(20, 0).values).toEqual([0, 5, 10, 15, 20])
    expect(niceTicks(Number.NaN, 3)).toEqual(niceTicks(0, 1))
  })

  it('handles byte-sized values', () => {
    const gib = 1024 ** 3
    const t = niceTicks(0, 5 * gib)
    expect(t.min).toBe(0)
    expect(t.max).toBeGreaterThanOrEqual(5 * gib)
    expect(t.values.length).toBeLessThanOrEqual(6)
  })
})

describe('minStep and tickDecimals', () => {
  it('keeps whole-number axes at steps of at least 1', () => {
    expect(niceTicks(0, 1, 4, 1)).toEqual({ min: 0, max: 1, step: 1, values: [0, 1] })
    expect(niceTicks(0, 0, 4, 1).values).toEqual([0, 1])
  })

  it('needs as many decimals as the step', () => {
    expect(tickDecimals(5)).toBe(0)
    expect(tickDecimals(0.5)).toBe(1)
    expect(tickDecimals(0.02)).toBe(2)
    expect(tickDecimals(0)).toBe(0)
  })
})

describe('timeTicks', () => {
  it('picks a round step with at most count + 1 ticks', () => {
    expect(timeTicks(0, 3_600_000, 5, 0)).toEqual([0, 900_000, 1_800_000, 2_700_000, 3_600_000])
    expect(timeTicks(1_000, 86_401_000, 5, 0)).toEqual([21_600_000, 43_200_000, 64_800_000, 86_400_000])
  })

  it('aligns ticks to local time via the offset', () => {
    // UTC+1: local midnight is 23:00 UTC.
    const ticks = timeTicks(0, 2 * 86_400_000, 2, 3_600_000)
    expect(ticks).toEqual([82_800_000, 169_200_000])
  })
})
