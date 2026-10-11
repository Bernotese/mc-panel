import { describe, expect, it } from 'vitest'
import { parseFormatted, plainText } from './ansi'

const ESC = '\u001b'

describe('parseFormatted', () => {
  it('returns plain text as one segment', () => {
    expect(parseFormatted('[10:00:00 INFO]: Done')).toEqual([{ text: '[10:00:00 INFO]: Done' }])
  })

  it('returns no segments for an empty line', () => {
    expect(parseFormatted('')).toEqual([])
  })

  it('applies ANSI foreground colours and resets', () => {
    expect(parseFormatted(`${ESC}[33mWARN${ESC}[0m done`)).toEqual([{ text: 'WARN', color: 'yellow' }, { text: ' done' }])
  })

  it('combines bold with bright colours and clears them separately', () => {
    expect(parseFormatted(`${ESC}[1;91mERR${ESC}[22m x${ESC}[39my`)).toEqual([
      { text: 'ERR', color: 'bright-red', bold: true },
      { text: ' x', color: 'bright-red' },
      { text: 'y' },
    ])
  })

  it('treats an empty SGR parameter list as reset', () => {
    expect(parseFormatted(`${ESC}[32mok${ESC}[m.`)).toEqual([{ text: 'ok', color: 'green' }, { text: '.' }])
  })

  it('skips 256-colour and true-colour parameters without applying them', () => {
    expect(parseFormatted(`${ESC}[38;5;208mX${ESC}[38;2;1;2;3mY${ESC}[39mZ`)).toEqual([{ text: 'XYZ' }])
  })

  it('drops other CSI sequences, lone escapes and unterminated sequences', () => {
    expect(parseFormatted(`a${ESC}[2Kb${ESC}c${ESC}[12`)).toEqual([{ text: 'abc' }])
  })

  it('applies Minecraft colour and format codes', () => {
    expect(parseFormatted('§aGrün §lfett§r normal')).toEqual([
      { text: 'Grün ', color: 'bright-green' },
      { text: 'fett', color: 'bright-green', bold: true },
      { text: ' normal' },
    ])
  })

  it('lets a Minecraft colour code reset the formats like the game does', () => {
    expect(parseFormatted('§l§cX')).toEqual([{ text: 'X', color: 'bright-red' }])
  })

  it('accepts upper-case codes and maps all format codes', () => {
    expect(parseFormatted('§A§K1§r§M2§r§N3§r§O4')).toEqual([
      { text: '1', color: 'bright-green', obfuscated: true },
      { text: '2', strike: true },
      { text: '3', underline: true },
      { text: '4', italic: true },
    ])
  })

  it('drops unknown codes and a trailing section sign', () => {
    expect(parseFormatted('§zX§')).toEqual([{ text: 'X' }])
  })

  it('removes control characters but keeps tabs', () => {
    expect(parseFormatted('a\u0007b\tc\u007f\u0000d')).toEqual([{ text: 'ab\tcd' }])
  })

  it('keeps markup as plain text', () => {
    expect(parseFormatted('<img src=x onerror=alert(1)>')).toEqual([{ text: '<img src=x onerror=alert(1)>' }])
  })
})

describe('plainText', () => {
  it('strips all formatting', () => {
    expect(plainText(`§6There are ${ESC}[1m2${ESC}[0m players`)).toBe('There are 2 players')
  })
})
