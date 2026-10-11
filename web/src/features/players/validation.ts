/** Same rules as the panel (spec §6.5); the panel stays authoritative. */
export const PLAYER_NAME = /^[A-Za-z0-9_]{3,16}$/
export const MAX_REASON_LENGTH = 200

/** Mirrors Go's unicode.IsPrint: no control/format/private characters, no separators except the ASCII space. */
const NOT_PRINTABLE = /[\p{C}\p{Zl}\p{Zp}]|(?! )\p{Zs}/u

export function playerNameProblem(name: string): string | null {
  return PLAYER_NAME.test(name) ? null : 'players.validation.name_invalid'
}

export function reasonProblem(reason: string): string | null {
  if ([...reason].length > MAX_REASON_LENGTH) return 'players.validation.reason_too_long'
  if (NOT_PRINTABLE.test(reason)) return 'players.validation.reason_control'
  return null
}
