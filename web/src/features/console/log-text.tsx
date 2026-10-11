import { cn } from '@/lib/utils'
import type { LogColor, Segment } from './ansi'

/** Readable on the always-dark console background; static strings so that Tailwind finds them. */
const COLOR_CLASS: Record<LogColor, string> = {
  black: 'text-zinc-500',
  red: 'text-red-400',
  green: 'text-green-400',
  yellow: 'text-yellow-400',
  blue: 'text-blue-400',
  magenta: 'text-fuchsia-400',
  cyan: 'text-cyan-400',
  white: 'text-zinc-200',
  'bright-black': 'text-zinc-400',
  'bright-red': 'text-red-300',
  'bright-green': 'text-green-300',
  'bright-yellow': 'text-yellow-200',
  'bright-blue': 'text-blue-300',
  'bright-magenta': 'text-fuchsia-300',
  'bright-cyan': 'text-cyan-300',
  'bright-white': 'text-white',
}

export function LogText({ segments }: { segments: readonly Segment[] }) {
  return (
    <>
      {segments.map((s, i) => (
        <span
          key={i}
          className={cn(
            s.color && COLOR_CLASS[s.color],
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
