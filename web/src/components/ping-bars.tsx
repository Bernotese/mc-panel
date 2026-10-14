import { cn } from '@/lib/utils'

const HEIGHTS = [4, 6, 8, 10, 12]

export function PingBars({ level, label, className }: { level: 0 | 1 | 2 | 3 | 4 | 5 | null; label: string; className?: string }) {
  const lit = level ?? 0
  return (
    <span role="img" aria-label={label} className={cn('inline-flex h-3 items-end gap-px', className)}>
      {HEIGHTS.map((h, i) => {
        const on = i < lit
        return (
          <span
            key={h}
            data-on={on}
            className={cn('block w-[3px]', on ? (lit <= 1 ? 'bg-status-error' : lit <= 2 ? 'bg-status-starting' : 'bg-status-online') : 'bg-status-offline/40')}
            style={{ height: `${h}px` }}
          />
        )
      })}
    </span>
  )
}
