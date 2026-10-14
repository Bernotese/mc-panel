import { cn } from '@/lib/utils'

/** Horizontal bar with role="meter"; the width is the only inline style (allowed by the CSP's style-src). */
export function Meter({ label, value, max, valueText, danger = false }: { label: string; value: number; max: number; valueText: string; danger?: boolean }) {
  const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={value}
      aria-valuetext={valueText}
      className="bg-muted h-2 w-full overflow-hidden rounded-full"
    >
      <div className={cn('h-full rounded-full', danger ? 'bg-destructive' : 'bg-primary')} style={{ width: `${pct}%` }} />
    </div>
  )
}
