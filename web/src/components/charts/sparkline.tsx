import { cn } from '@/lib/utils'

const W = 100
const H = 24

const r = (n: number) => Math.round(n * 100) / 100

/** A tiny zero-based line without axes; the caller's label carries the numbers (spec §7.1). */
export function Sparkline({ points, label, className }: { points: readonly (readonly [number, number])[]; label: string; className?: string }) {
  if (points.length < 2) return null
  const t0 = points[0][0]
  const span = points[points.length - 1][0] - t0 || 1
  const max = Math.max(1, ...points.map((p) => p[1]))
  const d = points.map(([t, v], i) => `${i === 0 ? 'M' : 'L'}${r(((t - t0) / span) * W)},${r(H - 1 - (Math.max(0, v) / max) * (H - 2))}`).join(' ')
  return (
    <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={label} className={cn('h-6 w-full', className)}>
      <path d={d} className="stroke-primary fill-none" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}
