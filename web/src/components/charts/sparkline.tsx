import { cn } from '@/lib/utils'
import { splitSegments } from './thin'

const W = 100
const H = 24

const r = (n: number) => Math.round(n * 100) / 100

export interface SparklineProps {
  /** [at (Unix ms), value] pairs, ascending. */
  points: readonly (readonly [number, number])[]
  label: string
  className?: string
  /** Width of the x axis in ms, ending at the newest point (e.g. one hour); default: the points' own extent. */
  spanMs?: number
  /** Distance of two stored points; gaps above two steps are not bridged (default 30 s). */
  stepMs?: number
}

/** A tiny zero-based line without axes; the caller's label carries the numbers (spec §7.1). */
export function Sparkline({ points, label, className, spanMs, stepMs = 30_000 }: SparklineProps) {
  if (points.length < 2) return null
  const end = points[points.length - 1][0]
  const start = spanMs !== undefined ? end - spanMs : points[0][0]
  const span = end - start || 1
  const max = Math.max(1, ...points.map((p) => p[1]))
  const segments = splitSegments(
    points.filter(([t]) => t >= start).map(([t, v]) => ({ t, v, min: v, max: v })),
    stepMs,
  )
  // A lone point becomes a zero-length segment that the round line cap draws as a dot.
  const d = segments
    .map((seg) => {
      const cmds = seg.map((p, i) => `${i === 0 ? 'M' : 'L'}${r(((p.t - start) / span) * W)},${r(H - 1 - (Math.max(0, p.v) / max) * (H - 2))}`)
      return seg.length === 1 ? `${cmds[0]} L${cmds[0].slice(1)}` : cmds.join(' ')
    })
    .join(' ')
  return (
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={label} className={cn('h-6 w-full', className)}>
      <path d={d} className="stroke-primary fill-none" strokeWidth={1.5} strokeLinecap="round" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}
