import type { LogColor } from '@/features/console/ansi'

/** Tailwind class per Minecraft/ANSI colour name; the values come from the --mc-* tokens (console-scope overrides them). */
const CLASS: Readonly<Record<LogColor, string>> = {
  black: 'text-mc-0',
  blue: 'text-mc-1',
  green: 'text-mc-2',
  cyan: 'text-mc-3',
  red: 'text-mc-4',
  magenta: 'text-mc-5',
  yellow: 'text-mc-6',
  white: 'text-mc-7',
  'bright-black': 'text-mc-8',
  'bright-blue': 'text-mc-9',
  'bright-green': 'text-mc-a',
  'bright-cyan': 'text-mc-b',
  'bright-red': 'text-mc-c',
  'bright-magenta': 'text-mc-d',
  'bright-yellow': 'text-mc-e',
  'bright-white': 'text-mc-f',
}

export function mcColorClass(color: LogColor): string {
  return CLASS[color]
}
