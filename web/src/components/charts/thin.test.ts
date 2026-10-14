import { describe, expect, it } from 'vitest'
import { prepareSeries, splitSegments, thinPoints, type ChartPoint } from './thin'

const STEP = 30_000
const p = (i: number, v: number, min = v, max = v): ChartPoint => ({ t: i * STEP, v, min, max })

describe('splitSegments', () => {
  it('keeps neighbours up to two steps apart in one line', () => {
    const segs = splitSegments([p(0, 1), p(1, 2), p(3, 3)], STEP)
    expect(segs).toHaveLength(1)
  })

  it('cuts where more than two steps are missing', () => {
    const segs = splitSegments([p(0, 1), p(1, 2), p(5, 4), p(6, 5)], STEP)
    expect(segs.map((s) => s.map((x) => x.v))).toEqual([
      [1, 2],
      [4, 5],
    ])
  })

  it('returns no segment for no points', () => {
    expect(splitSegments([], STEP)).toEqual([])
  })
})

describe('thinPoints', () => {
  it('leaves short series alone', () => {
    const pts = [p(0, 1), p(1, 2)]
    expect(thinPoints(pts, 5)).toEqual(pts)
  })

  it('averages buckets but keeps their extremes', () => {
    const pts = Array.from({ length: 10 }, (_, i) => p(i, i, i, i === 7 ? 100 : i))
    const out = thinPoints(pts, 5)
    expect(out).toHaveLength(5)
    expect(out[0]).toEqual({ t: 0, v: 0.5, min: 0, max: 1 })
    expect(out[3]).toEqual({ t: 6 * STEP, v: 6.5, min: 6, max: 100 })
  })
})

describe('prepareSeries', () => {
  it('thins 2880 raw points to at most the pixel budget without bridging gaps', () => {
    const pts = [...Array.from({ length: 1440 }, (_, i) => p(i, 20)), ...Array.from({ length: 1440 }, (_, i) => p(i + 1500, 19))]
    const segs = prepareSeries(pts, STEP, 580)
    expect(segs).toHaveLength(2)
    expect(segs.flat().length).toBeLessThanOrEqual(580)
    expect(segs[0].every((x) => x.v === 20)).toBe(true)
  })
})
