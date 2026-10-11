import { describe, expect, it } from 'vitest'
import { commandProblem, MAX_COMMAND_BYTES, normalizeCommand } from './command'

describe('normalizeCommand', () => {
  it('removes exactly one leading slash', () => {
    expect(normalizeCommand('/list')).toBe('list')
    expect(normalizeCommand('//list')).toBe('/list')
    expect(normalizeCommand('list')).toBe('list')
  })
})

describe('commandProblem', () => {
  it('accepts normal commands with and without slash', () => {
    expect(commandProblem('list')).toBeNull()
    expect(commandProblem('/say Grüße an alle')).toBeNull()
  })

  it('rejects empty commands and a lone slash', () => {
    expect(commandProblem('')).toBe('empty')
    expect(commandProblem('   ')).toBe('empty')
    expect(commandProblem('/')).toBe('empty')
  })

  it('rejects control characters', () => {
    expect(commandProblem('say a\nop me')).toBe('control')
    expect(commandProblem('say a\tb')).toBe('control')
    expect(commandProblem('say \u0000')).toBe('control')
    expect(commandProblem('say \u007f')).toBe('control')
  })

  it('counts UTF-8 bytes after removing the slash', () => {
    expect(commandProblem('/' + 'a'.repeat(MAX_COMMAND_BYTES))).toBeNull()
    expect(commandProblem('a'.repeat(MAX_COMMAND_BYTES + 1))).toBe('too_long')
    expect(commandProblem('ä'.repeat(MAX_COMMAND_BYTES / 2))).toBeNull()
    expect(commandProblem('ä'.repeat(MAX_COMMAND_BYTES / 2) + 'a')).toBe('too_long')
  })
})
