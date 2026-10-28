import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

function clamp01(v: number): number {
  return Math.min(1, Math.max(0, v))
}

export function StatSlot({
  label,
  value,
  hint,
  bar,
  tone = 'default',
  stale = false,
  className,
}: {
  label: string
  value: ReactNode | null | undefined
  hint?: ReactNode
  bar?: number | null
  tone?: 'default' | 'warn' | 'danger'
  /** The value is the last known one and could not be refreshed (issue #155): muted, without the tone. */
  stale?: boolean
  className?: string
}) {
  const showBar = typeof bar === 'number' && !Number.isNaN(bar)
  const pct = showBar ? Math.round(clamp01(bar) * 100) : 0
  return (
    <div data-tone={tone} data-stale={stale || undefined} className={cn('bevel-inset min-w-0 px-3 py-2.5', className)}>
      <div className="text-foreground/80 truncate text-xs font-medium">{label}</div>
      {/* Issue #84: values in the UI font with tabular figures; the pixel font made "8 GiB" read as "S 6iB". */}
      <div
        className={cn(
          'text-foreground mt-0.5 text-xl leading-tight font-semibold tabular-nums',
          !stale && tone === 'warn' && 'text-status-starting',
          !stale && tone === 'danger' && 'text-status-error',
          stale && 'text-muted-foreground',
        )}
      >
        {value === null || value === undefined ? '–' : value}
      </div>
      {hint ? <div className="text-muted-foreground truncate text-xs">{hint}</div> : null}
      {showBar ? (
        <div role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="bg-background mt-1.5 h-1.5">
          <div
            className={cn(
              'h-full',
              stale ? 'bg-muted-foreground' : tone === 'danger' ? 'bg-status-error' : tone === 'warn' ? 'bg-status-starting' : 'bg-primary',
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      ) : null}
    </div>
  )
}
