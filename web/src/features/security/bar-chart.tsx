import { useId } from 'react'
import type { SeriesPoint } from '@/api/types'

const W = 640
const H = 220
const M = { top: 12, right: 8, bottom: 28, left: 36 }
const INNER_W = W - M.left - M.right
const INNER_H = H - M.top - M.bottom
const BASELINE = M.top + INNER_H
const GAP = 2

// eslint-disable-next-line react-refresh/only-export-components -- pure helper, unit-tested directly
export function niceMax(value: number): number {
  if (value <= 0) return 1
  const exp = 10 ** Math.floor(Math.log10(value))
  const f = value / exp
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10
  return nice * exp
}

export interface ChartLabels {
  time: string
  success: string
  failure: string
}

interface LoginBarChartProps {
  data: SeriesPoint[]
  title: string
  formatTick(t: number): string
  labels: ChartLabels
}

export function LoginBarChart({ data, title, formatTick, labels }: LoginBarChartProps) {
  const titleId = useId()
  const descId = useId()
  const max = niceMax(Math.max(0, ...data.map((d) => d.success + d.failure)))
  const slot = data.length > 0 ? INNER_W / data.length : INNER_W
  const barW = Math.max(2, Math.min(28, slot * 0.7))
  const scale = (v: number) => (v / max) * INNER_H
  const tickEvery = Math.max(1, Math.ceil(data.length / 6))
  const totals = data.reduce((acc, d) => ({ success: acc.success + d.success, failure: acc.failure + d.failure }), { success: 0, failure: 0 })

  return (
    <figure className="space-y-3">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-labelledby={`${titleId} ${descId}`} className="h-auto w-full">
        <title id={titleId}>{title}</title>
        <desc id={descId}>{`${labels.success}: ${totals.success}, ${labels.failure}: ${totals.failure}`}</desc>
        {[0, 0.5, 1].map((f) => {
          const y = BASELINE - f * INNER_H
          return (
            <g key={f}>
              <line x1={M.left} x2={W - M.right} y1={y} y2={y} className="stroke-border" strokeWidth={1} />
              <text x={M.left - 6} y={y} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[10px]">
                {Math.round(max * f)}
              </text>
            </g>
          )
        })}
        {data.map((d, i) => {
          const x = M.left + i * slot + (slot - barW) / 2
          const hs = scale(d.success)
          const hf = scale(d.failure)
          const gap = hs > 0 && hf > 0 ? GAP : 0
          return (
            <g key={d.t} data-testid="chart-bar">
              <title>{`${formatTick(d.t)} – ${labels.success}: ${d.success}, ${labels.failure}: ${d.failure}`}</title>
              <rect x={M.left + i * slot} y={M.top} width={slot} height={INNER_H} fill="transparent" />
              {hs > 0 ? <rect data-series="success" x={x} y={BASELINE - hs} width={barW} height={hs} rx={2} className="fill-[var(--chart-2)]" /> : null}
              {hf > 0 ? (
                <rect data-series="failure" x={x} y={BASELINE - hs - gap - hf} width={barW} height={hf} rx={2} className="fill-[var(--chart-1)]" />
              ) : null}
              {i % tickEvery === 0 ? (
                <text x={x + barW / 2} y={H - 8} textAnchor="middle" className="fill-muted-foreground text-[10px]">
                  {formatTick(d.t)}
                </text>
              ) : null}
            </g>
          )
        })}
      </svg>
      <figcaption data-testid="chart-legend" className="flex flex-wrap gap-4 text-sm">
        <span className="flex items-center gap-2">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm bg-[var(--chart-2)]" />
          {labels.success}
        </span>
        <span className="flex items-center gap-2">
          <span aria-hidden="true" className="inline-block size-3 rounded-sm bg-[var(--chart-1)]" />
          {labels.failure}
        </span>
      </figcaption>
      <table className="sr-only">
        <caption>{title}</caption>
        <thead>
          <tr>
            <th scope="col">{labels.time}</th>
            <th scope="col">{labels.success}</th>
            <th scope="col">{labels.failure}</th>
          </tr>
        </thead>
        <tbody>
          {data.map((d) => (
            <tr key={d.t}>
              <td>{formatTick(d.t)}</td>
              <td>{d.success}</td>
              <td>{d.failure}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  )
}
