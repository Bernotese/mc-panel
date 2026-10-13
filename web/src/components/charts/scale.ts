export interface Ticks {
  min: number
  max: number
  step: number
  values: number[]
}

/** Tolerance for float noise (0.8 / 4 = 0.20000000000000018). */
const EPS = 1e-9

function decimals(step: number): number {
  return Math.max(0, -Math.floor(Math.log10(step)))
}

/** "Nice" y-axis ticks: steps of 1/2/5 × 10^n that cover [lo, hi] in about `count` intervals. */
export function niceTicks(lo: number, hi: number, count = 4): Ticks {
  if (!Number.isFinite(lo) || !Number.isFinite(hi)) return niceTicks(0, 1, count)
  if (hi < lo) [lo, hi] = [hi, lo]
  if (hi === lo) {
    if (hi === 0) hi = 1
    else {
      const pad = Math.abs(hi) * 0.1
      lo -= pad
      hi += pad
    }
  }
  const raw = (hi - lo) / Math.max(1, count)
  const exp = 10 ** Math.floor(Math.log10(raw))
  const f = raw / exp
  const nice = f <= 1 + EPS ? 1 : f <= 2 + EPS ? 2 : f <= 5 + EPS ? 5 : 10
  const step = nice * exp
  const d = decimals(step)
  const round = (v: number) => Number(v.toFixed(d))
  const min = round(Math.floor(lo / step + EPS) * step)
  const max = round(Math.ceil(hi / step - EPS) * step)
  const values: number[] = []
  for (let i = 0; min + i * step <= max + step / 2; i++) values.push(round(min + i * step))
  return { min, max, step: round(step), values }
}

const TIME_STEPS = [
  60_000, 300_000, 600_000, 900_000, 1_800_000, 3_600_000, 7_200_000, 10_800_000, 21_600_000, 43_200_000, 86_400_000, 172_800_000,
  604_800_000,
]

/**
 * Tick times on round local boundaries (minutes, hours, days) with at most `count` intervals in [from, to].
 * `offsetMs` is the local UTC offset (positive east), by default the browser's at `from`.
 */
export function timeTicks(from: number, to: number, count = 5, offsetMs = -new Date(from).getTimezoneOffset() * 60_000): number[] {
  const span = Math.max(1, to - from)
  const step = TIME_STEPS.find((s) => span / s <= count) ?? TIME_STEPS[TIME_STEPS.length - 1]
  const first = Math.ceil((from + offsetMs) / step) * step - offsetMs
  const out: number[] = []
  for (let t = first; t <= to; t += step) out.push(t)
  return out
}
