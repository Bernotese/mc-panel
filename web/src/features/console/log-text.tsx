import { mcColorClass } from '@/lib/mc-colors'
import { cn } from '@/lib/utils'
import type { Segment } from './ansi'

export function LogText({ segments }: { segments: readonly Segment[] }) {
  return (
    <>
      {segments.map((s, i) => (
        <span
          key={i}
          className={cn(
            s.color && mcColorClass(s.color),
            s.bold && 'font-bold',
            s.italic && 'italic',
            s.underline && 'underline',
            s.strike && 'line-through',
            s.obfuscated && 'blur-[2px]',
          )}
        >
          {s.text}
        </span>
      ))}
    </>
  )
}
