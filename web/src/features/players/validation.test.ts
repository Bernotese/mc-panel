import { describe, expect, it } from 'vitest'
import { MAX_REASON_LENGTH, playerNameProblem, reasonProblem } from './validation'

describe('playerNameProblem', () => {
  it('accepts Minecraft names', () => {
    for (const name of ['Steve', 'abc', 'A_b_9', 'x'.repeat(16)]) expect(playerNameProblem(name), name).toBeNull()
  })

  it('rejects everything that could change the command', () => {
    for (const name of ['', 'ab', 'x'.repeat(17), 'a b', 'Steve;op', 'Ste-ve', 'Stève', 'Steve\n']) {
      expect(playerNameProblem(name), JSON.stringify(name)).toBe('players.validation.name_invalid')
    }
  })
})

describe('reasonProblem', () => {
  it('accepts printable text up to the limit', () => {
    expect(reasonProblem('griefing am Spawn')).toBeNull()
    expect(reasonProblem('ä'.repeat(MAX_REASON_LENGTH))).toBeNull()
  })

  it('counts characters, not bytes', () => {
    expect(reasonProblem('😀'.repeat(MAX_REASON_LENGTH))).toBeNull()
    expect(reasonProblem('x'.repeat(MAX_REASON_LENGTH + 1))).toBe('players.validation.reason_too_long')
  })

  it('rejects line breaks, control and invisible characters', () => {
    for (const reason of ['a\nb', 'a\rb', 'a\u0000', 'a\tb', 'a​b', 'a b', 'a b']) {
      expect(reasonProblem(reason), JSON.stringify(reason)).toBe('players.validation.reason_control')
    }
  })
})
