import { parseFormatted } from '@/features/console/ansi'
import { mcColorClass } from '@/lib/mc-colors'
import { cn } from '@/lib/utils'

export function McText({ text, className }: { text: string; className?: string }) {
  const segments = parseFormatted(text)
  return (
    <span className={cn('font-pixel', className)}>
      {segments.map((s, i) => (
        // Segments are positional and never reordered, so the index is a stable key.
        <span
          key={i}
          className={cn(s.color && mcColorClass(s.color), s.bold && 'font-bold', s.italic && 'italic', s.underline && 'underline', s.strike && 'line-through')}
        >
          {s.text}
        </span>
      ))}
    </span>
  )
}
