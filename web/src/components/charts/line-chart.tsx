import { memo, useId, useMemo, useState, type KeyboardEvent, type MouseEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { niceTicks, tickDecimals, timeTicks, type Ticks } from './scale'
import { prepareSeries, type ChartPoint } from './thin'

export type ChartTone = 'primary' | 'secondary' | 'muted'
export type ChartAxis = 'left' | 'right'

export interface ChartSeries {
  id: string
  label: string
  points: ChartPoint[]
  tone: ChartTone
  axis?: ChartAxis
}

export interface ChartLimit {
  value: number
  label: string
}

/**
 * Formats a value of an axis. `digits` is set for axis ticks (derived from the tick step, so 0.02 steps
 * do not all read "0"); without it the caller picks its usual precision (readout, table, summary).
 */
export type ChartValueFormatter = (value: number, axis: ChartAxis, digits?: number) => string

export interface LineChartProps {
  title: string
  from: number
  to: number
  /** Distance of two stored points (V3 step_ms); gaps above two steps are not bridged. */
  stepMs: number
  series: ChartSeries[]
  formatValue: ChartValueFormatter
  /** Time in the readout and the table. */
  formatTime(t: number): string
  /** Time on the x axis (default formatTime). */
  formatTick?(t: number): string
  /** Draws the min–max band (5-min and 1-h averages); summary, readout and table then name min and max. */
  band?: boolean
  /** The y axis starts at 0 (default true). */
  zeroBased?: boolean
  /** Whole-number values (player counts): the axis steps by at least 1. */
  integer?: boolean
  /** Dashed reference line on the left axis, e.g. the RAM limit. */
  limit?: ChartLimit
  /** Text instead of "no data" when no series has values. */
  emptyText?: string
}

const W = 640
const H = 200
const M_TOP = 10
const M_BOTTOM = 24
const M_SIDE = 48
const M_RIGHT_EMPTY = 12

const STROKE: Record<ChartTone, string> = { primary: 'stroke-primary', secondary: 'stroke-chart-2', muted: 'stroke-muted-foreground' }
const FILL: Record<ChartTone, string> = { primary: 'fill-primary', secondary: 'fill-chart-2', muted: 'fill-muted-foreground' }
const SWATCH: Record<ChartTone, string> = { primary: 'bg-primary', secondary: 'bg-chart-2', muted: 'bg-muted-foreground' }

interface PreparedSeries extends ChartSeries {
  segments: ChartPoint[][]
  /** The thinned points of all segments and their times (ascending), for the nearest-point lookups. */
  flat: ChartPoint[]
  times: number[]
}

type BandText = (value: string, min: string, max: string) => string

function axisTicks(series: readonly ChartSeries[], axis: ChartAxis, band: boolean, zeroBased: boolean, minStep: number, extra?: number): Ticks | null {
  let lo = Infinity
  let hi = -Infinity
  for (const s of series) {
    if ((s.axis ?? 'left') !== axis) continue
    for (const p of s.points) {
      lo = Math.min(lo, band ? p.min : p.v)
      hi = Math.max(hi, band ? p.max : p.v)
    }
  }
  if (lo === Infinity) return null
  if (extra !== undefined) hi = Math.max(hi, extra)
  if (zeroBased) lo = Math.min(0, lo)
  return niceTicks(lo, hi, 4, minStep)
}

/** Index of the time closest to t (times ascending, not empty). */
function nearestIndex(times: readonly number[], t: number): number {
  let lo = 0
  let hi = times.length - 1
  while (hi - lo > 1) {
    const mid = (lo + hi) >> 1
    if (times[mid] < t) lo = mid
    else hi = mid
  }
  return Math.abs(times[lo] - t) <= Math.abs(times[hi] - t) ? lo : hi
}

function nearestPoint(s: PreparedSeries, t: number, tolerance: number): ChartPoint | null {
  if (s.flat.length === 0) return null
  const p = s.flat[nearestIndex(s.times, t)]
  return Math.abs(p.t - t) <= tolerance ? p : null
}

function stats(points: readonly ChartPoint[], band: boolean) {
  let min = Infinity
  let max = -Infinity
  for (const p of points) {
    min = Math.min(min, band ? p.min : p.v)
    max = Math.max(max, band ? p.max : p.v)
  }
  return { current: points[points.length - 1].v, min, max }
}

/** One point as text: the value, with a band also its min and max. */
function pointText(p: ChartPoint, axis: ChartAxis, band: boolean, formatValue: ChartValueFormatter, bandText: BandText): string {
  const value = formatValue(p.v, axis)
  return band && p.min !== p.max ? bandText(value, formatValue(p.min, axis), formatValue(p.max, axis)) : value
}

interface ChartTableProps {
  id: string
  title: string
  timeLabel: string
  series: PreparedSeries[]
  times: number[]
  tolerance: number
  band: boolean
  formatValue: ChartValueFormatter
  formatTime(t: number): string
  bandText: BandText
}

/** The table alternative; memoised so that moving the readout (active point) does not re-render every row. */
const ChartTable = memo(function ChartTable({ id, title, timeLabel, series, times, tolerance, band, formatValue, formatTime, bandText }: ChartTableProps) {
  return (
    <div id={id} className="max-h-64 overflow-auto border">
      <table className="w-full text-sm tabular-nums">
        <caption className="sr-only">{title}</caption>
        <thead>
          <tr>
            <th scope="col" className="px-2 py-1 text-left font-heading text-xs font-normal uppercase">
              {timeLabel}
            </th>
            {series.map((s) => (
              <th key={s.id} scope="col" className="px-2 py-1 text-right font-heading text-xs font-normal uppercase">
                {s.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {times.map((time) => (
            <tr key={time} className="border-t">
              <td className="px-2 py-1">{formatTime(time)}</td>
              {series.map((s) => {
                const p = nearestPoint(s, time, tolerance)
                return (
                  <td key={s.id} className="px-2 py-1 text-right">
                    {p ? pointText(p, s.axis ?? 'left', band, formatValue, bandText) : '–'}
                  </td>
                )
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
})

/** Line chart for metric series (spec §7.4): own SVG, theme colours, keyboard readout and a table alternative. */
export function LineChart({
  title,
  from,
  to,
  stepMs,
  series,
  formatValue,
  formatTime,
  formatTick,
  band = false,
  zeroBased = true,
  integer = false,
  limit,
  emptyText,
}: LineChartProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const descId = useId()
  const hintId = useId()
  const tableId = useId()
  const [active, setActive] = useState<number | null>(null)
  // Only keyboard use is announced; a mouse sweep over the chart would flood the screen reader.
  const [viaKeyboard, setViaKeyboard] = useState(false)
  const [showTable, setShowTable] = useState(false)
  const tick = formatTick ?? formatTime

  // Points outside [from, to] (e.g. the previous, longer range while a new one loads) are not drawn.
  const visible = useMemo(() => series.map((s) => ({ ...s, points: s.points.filter((p) => p.t >= from && p.t <= to) })), [series, from, to])
  const hasRight = visible.some((s) => s.axis === 'right' && s.points.length > 0)
  const marginRight = hasRight ? M_SIDE : M_RIGHT_EMPTY
  const innerW = W - M_SIDE - marginRight
  const innerH = H - M_TOP - M_BOTTOM
  const span = Math.max(1, to - from)
  const minStep = integer ? 1 : 0

  const left = useMemo(() => axisTicks(visible, 'left', band, zeroBased, minStep, limit?.value), [visible, band, zeroBased, minStep, limit?.value])
  const right = useMemo(() => (hasRight ? axisTicks(visible, 'right', band, zeroBased, minStep) : null), [visible, band, zeroBased, minStep, hasRight])
  const prepared = useMemo<PreparedSeries[]>(
    () =>
      visible.map((s) => {
        const segments = prepareSeries(s.points, stepMs, Math.floor(innerW))
        const flat = segments.flat()
        return { ...s, segments, flat, times: flat.map((p) => p.t) }
      }),
    [visible, stepMs, innerW],
  )
  const bandText = useMemo<BandText>(() => (value, min, max) => t('charts.band', { value, min, max }), [t])
  // The readout walks over the (thinned) times of the first series with data.
  const anchor = prepared.find((s) => s.flat.length > 0)
  const times = anchor?.times ?? []
  const tolerance = Math.max(stepMs, span / innerW) * 2

  if (!anchor) {
    return (
      <figure className="space-y-2">
        <figcaption className="pixel-heading text-xs uppercase">{title}</figcaption>
        <p className="text-muted-foreground text-sm">{emptyText ?? t('charts.no_data')}</p>
      </figure>
    )
  }

  const description = visible
    .map((s) => {
      if (s.points.length === 0) return t('charts.summary_empty', { label: s.label })
      const st = stats(s.points, band)
      const axis = s.axis ?? 'left'
      return t('charts.summary', { label: s.label, current: formatValue(st.current, axis), min: formatValue(st.min, axis), max: formatValue(st.max, axis) })
    })
    .join('; ')

  const x = (time: number) => M_SIDE + ((time - from) / span) * innerW
  const y = (v: number, ticks: Ticks) => M_TOP + innerH - ((v - ticks.min) / (ticks.max - ticks.min || 1)) * innerH
  const ticksOf = (axis: ChartAxis | undefined) => ((axis ?? 'left') === 'right' ? right : left)

  const activeTime = active === null ? null : times[active]
  const readout =
    activeTime === null
      ? ''
      : `${formatTime(activeTime)} – ${prepared
          .map((s) => {
            const p = nearestPoint(s, activeTime, tolerance)
            return `${s.label}: ${p ? pointText(p, s.axis ?? 'left', band, formatValue, bandText) : '–'}`
          })
          .join(', ')}`

  function onKeyDown(e: KeyboardEvent<SVGSVGElement>) {
    const last = times.length - 1
    let next: number | null
    if (e.key === 'ArrowRight') next = active === null ? last : Math.min(last, active + 1)
    else if (e.key === 'ArrowLeft') next = active === null ? last : Math.max(0, active - 1)
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = last
    else if (e.key === 'Escape') next = null
    else return
    e.preventDefault()
    setViaKeyboard(true)
    setActive(next)
  }

  function onMouseMove(e: MouseEvent<SVGSVGElement>) {
    const rect = e.currentTarget.getBoundingClientRect()
    if (rect.width <= 0) return
    const px = ((e.clientX - rect.left) / rect.width) * W
    const time = from + ((px - M_SIDE) / innerW) * span
    setViaKeyboard(false)
    setActive(nearestIndex(times, time))
  }

  const xTicks = timeTicks(from, to)
  const leftDigits = left ? tickDecimals(left.step) : 0
  const rightDigits = right ? tickDecimals(right.step) : 0

  return (
    <figure className="space-y-2">
      <figcaption className="flex flex-wrap items-center justify-between gap-2 text-sm">
        <span className="pixel-heading text-xs uppercase">{title}</span>
        <span className="flex flex-wrap gap-3" data-testid="chart-legend">
          {series.map((s) => (
            <span key={s.id} className="text-muted-foreground flex items-center gap-1.5">
              <span aria-hidden="true" className={cn('inline-block h-1 w-3', SWATCH[s.tone])} />
              {s.label}
            </span>
          ))}
          {limit ? (
            <span className="text-muted-foreground flex items-center gap-1.5">
              <span aria-hidden="true" className="border-destructive inline-block w-3 border-t border-dashed" />
              {limit.label}
            </span>
          ) : null}
        </span>
      </figcaption>
      <svg
        viewBox={`0 0 ${W} ${H}`}
        role="img"
        tabIndex={0}
        aria-labelledby={titleId}
        aria-describedby={`${descId} ${hintId}`}
        className="focus-visible:ring-ring h-auto w-full touch-pan-y outline-none select-none focus-visible:ring-2"
        onKeyDown={onKeyDown}
        onMouseMove={onMouseMove}
        onMouseLeave={() => setActive(null)}
        onBlur={() => setActive(null)}
      >
        <title id={titleId}>{title}</title>
        <desc id={descId}>{description}</desc>
        {left?.values.map((v) => (
          <g key={`l${v}`}>
            <line x1={M_SIDE} x2={W - marginRight} y1={y(v, left)} y2={y(v, left)} className="stroke-border" strokeWidth={1} />
            <text data-axis="left" x={M_SIDE - 6} y={y(v, left)} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[10px]">
              {formatValue(v, 'left', leftDigits)}
            </text>
          </g>
        ))}
        {right?.values.map((v) => (
          <text key={`r${v}`} data-axis="right" x={W - marginRight + 6} y={y(v, right)} dy="0.32em" className="fill-muted-foreground text-[10px]">
            {formatValue(v, 'right', rightDigits)}
          </text>
        ))}
        {xTicks.map((time) => (
          <text key={time} x={x(time)} y={H - 6} textAnchor="middle" className="fill-muted-foreground text-[10px]">
            {tick(time)}
          </text>
        ))}
        {limit && left ? (
          <line
            data-testid="chart-limit"
            x1={M_SIDE}
            x2={W - marginRight}
            y1={y(limit.value, left)}
            y2={y(limit.value, left)}
            className="stroke-destructive"
            strokeWidth={1}
            strokeDasharray="4 3"
          />
        ) : null}
        {prepared.map((s) => {
          const ticks = ticksOf(s.axis)
          if (!ticks) return null
          return (
            <g key={s.id}>
              {band
                ? s.segments
                    .filter((seg) => seg.length > 1)
                    .map((seg, i) => (
                      <polygon
                        key={`b${i}`}
                        data-testid="chart-band"
                        data-series={s.id}
                        points={[...seg.map((p) => `${x(p.t)},${y(p.max, ticks)}`), ...[...seg].reverse().map((p) => `${x(p.t)},${y(p.min, ticks)}`)].join(' ')}
                        className={cn(FILL[s.tone], 'opacity-15')}
                      />
                    ))
                : null}
              {s.segments.map((seg, i) =>
                seg.length === 1 ? (
                  <circle key={`d${i}`} data-testid="chart-dot" data-series={s.id} cx={x(seg[0].t)} cy={y(seg[0].v, ticks)} r={2} className={FILL[s.tone]} />
                ) : (
                  <path
                    key={`l${i}`}
                    data-testid="chart-line"
                    data-series={s.id}
                    d={seg.map((p, j) => `${j === 0 ? 'M' : 'L'}${x(p.t).toFixed(1)},${y(p.v, ticks).toFixed(1)}`).join(' ')}
                    className={cn(STROKE[s.tone], 'fill-none')}
                    strokeWidth={1.5}
                    strokeLinejoin="round"
                  />
                ),
              )}
            </g>
          )
        })}
        {activeTime !== null ? (
          <g data-testid="chart-cursor">
            <line x1={x(activeTime)} x2={x(activeTime)} y1={M_TOP} y2={M_TOP + innerH} className="stroke-foreground/40" strokeWidth={1} />
            {prepared.map((s) => {
              const ticks = ticksOf(s.axis)
              const p = ticks ? nearestPoint(s, activeTime, tolerance) : null
              return p && ticks ? <circle key={s.id} cx={x(p.t)} cy={y(p.v, ticks)} r={3} className={FILL[s.tone]} /> : null
            })}
          </g>
        ) : null}
      </svg>
      <p id={hintId} className="sr-only">
        {t('charts.keyboard_hint')}
      </p>
      <p role="status" aria-live={viaKeyboard ? 'polite' : 'off'} className="text-muted-foreground min-h-5 text-xs tabular-nums">
        {readout}
      </p>
      <Button type="button" variant="ghost" size="sm" aria-expanded={showTable} aria-controls={tableId} onClick={() => setShowTable((v) => !v)}>
        {showTable ? t('charts.hide_table') : t('charts.show_table')}
      </Button>
      {showTable ? (
        <ChartTable
          id={tableId}
          title={title}
          timeLabel={t('charts.time')}
          series={prepared}
          times={times}
          tolerance={tolerance}
          band={band}
          formatValue={formatValue}
          formatTime={formatTime}
          bandText={bandText}
        />
      ) : null}
    </figure>
  )
}
