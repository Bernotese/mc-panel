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
  className,
}: {
  label: string
  value: ReactNode | null | undefined
  hint?: ReactNode
  bar?: number | null
  tone?: 'default' | 'warn' | 'danger'
  className?: string
}) {
  const showBar = typeof bar === 'number' && !Number.isNaN(bar)
  const pct = showBar ? Math.round(clamp01(bar) * 100) : 0
  return (
    <div data-tone={tone} className={cn('bevel-inset min-w-0 px-3 py-2', className)}>
      <div className="text-muted-foreground truncate text-xs">{label}</div>
      <div
        className={cn(
          'font-pixel text-foreground text-lg leading-tight font-semibold tabular-nums',
          tone === 'warn' && 'text-status-starting',
          tone === 'danger' && 'text-status-error',
        )}
      >
        {value === null || value === undefined ? '–' : value}
      </div>
      {hint ? <div className="text-muted-foreground truncate text-xs">{hint}</div> : null}
      {showBar ? (
        <div role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="bg-background mt-1.5 h-1.5">
          <div
            className={cn('h-full', tone === 'danger' ? 'bg-status-error' : tone === 'warn' ? 'bg-status-starting' : 'bg-primary')}
            style={{ width: `${pct}%` }}
          />
        </div>
      ) : null}
    </div>
  )
}
