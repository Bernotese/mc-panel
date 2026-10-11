/** Same rules as agentapi.ValidateCommand (spec §5.3); the panel strips one leading "/" first. */
export const MAX_COMMAND_BYTES = 1000

export type CommandProblem = 'empty' | 'too_long' | 'control'

export function normalizeCommand(input: string): string {
  return input.startsWith('/') ? input.slice(1) : input
}

export function commandProblem(input: string): CommandProblem | null {
  const command = normalizeCommand(input)
  if (command.trim() === '') return 'empty'
  for (const ch of command) {
    const code = ch.codePointAt(0) ?? 0
    if (code < 0x20 || code === 0x7f) return 'control'
  }
  if (new TextEncoder().encode(command).length > MAX_COMMAND_BYTES) return 'too_long'
  return null
}
