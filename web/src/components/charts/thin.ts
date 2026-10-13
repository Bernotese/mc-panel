/** One plotted value: for raw data min = max = v; for 5-min/1-h averages the API's min/max (spec §6.1). */
export interface ChartPoint {
  t: number
  v: number
  min: number
  max: number
}

/** Splits a series where neighbours are more than `maxGapSteps` steps apart: a line never bridges a gap (spec §7.4). */
export function splitSegments(points: readonly ChartPoint[], stepMs: number, maxGapSteps = 2): ChartPoint[][] {
  const out: ChartPoint[][] = []
  let current: ChartPoint[] = []
  for (const point of points) {
    const prev = current[current.length - 1]
    if (prev && point.t - prev.t > stepMs * maxGapSteps) {
      out.push(current)
      current = []
    }
    current.push(point)
  }
  if (current.length > 0) out.push(current)
  return out
}

/** At most `buckets` points: each bucket keeps the average of v and the extreme min/max, so peaks survive. */
export function thinPoints(points: readonly ChartPoint[], buckets: number): ChartPoint[] {
  if (buckets < 1 || points.length <= buckets) return [...points]
  const out: ChartPoint[] = []
  const size = points.length / buckets
  for (let b = 0; b < buckets; b++) {
    const start = Math.floor(b * size)
    const end = Math.floor((b + 1) * size)
    if (end <= start) continue
    let sum = 0
    let min = Infinity
    let max = -Infinity
    for (let i = start; i < end; i++) {
      const point = points[i]
      sum += point.v
      min = Math.min(min, point.min)
      max = Math.max(max, point.max)
    }
    out.push({ t: points[start].t, v: sum / (end - start), min, max })
  }
  return out
}

/** Gaps first, then thinning per segment in proportion to its length (at least one point each). */
export function prepareSeries(points: readonly ChartPoint[], stepMs: number, maxPoints: number): ChartPoint[][] {
  const total = Math.max(1, points.length)
  return splitSegments(points, stepMs).map((segment) =>
    thinPoints(segment, Math.max(1, Math.floor((maxPoints * segment.length) / total))),
  )
}
