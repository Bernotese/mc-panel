import { describe, expect, it } from 'vitest'
import i18n from '@/i18n'
import {
  memoryMiBFromGiB,
  normalizeBaseDomain,
  validateBaseDomain,
  validateCurseForgeKey,
  validateIntInRange,
  validateLoaderVersion,
  validateMemoryMiB,
  validateModpack,
  validateServerName,
  validateText,
  validateVersion,
  type FieldError,
} from './validation'

function expectError(error: FieldError | null, key: string): void {
  expect(error?.key).toBe(key)
  expect(i18n.exists(key), key).toBe(true)
}

describe('server names (instancespec.ValidName)', () => {
  it('accepts valid names', () => {
    for (const n of ['survival', 'a1', 'creative-2', 'a'.repeat(32)]) expect(validateServerName(n), n).toBeNull()
  })

  it('rejects invalid names', () => {
    expectError(validateServerName(''), 'servers.validation.name_required')
    for (const n of ['a', '-x', 'Upper', 'a_b', 'a.b', 'a'.repeat(33)]) expectError(validateServerName(n), 'servers.validation.name_invalid')
  })
})

describe('base domain (instancespec.ValidBaseDomain)', () => {
  it('accepts lowercase DNS names with at least two labels', () => {
    for (const d of ['mc.example.com', 'example.de', 'mc.localhost', 'a-b.c-d.example']) expect(validateBaseDomain(d), d).toBeNull()
  })

  it('rejects everything else', () => {
    expectError(validateBaseDomain(''), 'servers.validation.base_domain_required')
    for (const d of ['localhost', 'MC.example.com', '-a.example', 'a..b', 'a.b.', '.a.b', 'a_b.example', 'a.b:25565', 'a.'.repeat(101) + 'de']) {
      expectError(validateBaseDomain(d), 'servers.validation.base_domain_invalid')
    }
  })

  it('normalizes input before validation', () => {
    expect(normalizeBaseDomain('  MC.Example.com ')).toBe('mc.example.com')
  })
})

describe('versions, loaders and modpacks', () => {
  it('validates versions like instancespec', () => {
    for (const v of ['LATEST', '1.21.11', '26.4-snapshot-3', '1.20.4-rc1']) expect(validateVersion(v), v).toBeNull()
    expectError(validateVersion(''), 'servers.validation.version_required')
    for (const v of ['1.21 4', '1.21\nEULA=FALSE', '1'.repeat(65), '.1']) expectError(validateVersion(v), 'servers.validation.version_invalid')
  })

  it('treats an empty loader as latest', () => {
    expect(validateLoaderVersion('')).toBeNull()
    expect(validateLoaderVersion('0.19.5')).toBeNull()
    expectError(validateLoaderVersion('0.19 5'), 'servers.validation.loader_invalid')
  })

  it('checks modpacks per type', () => {
    expect(validateModpack('paper', '')).toBeNull()
    expectError(validateModpack('modrinth', ''), 'servers.validation.modpack_required')
    expect(validateModpack('modrinth', 'cobblemon-fabric')).toBeNull()
    expectError(validateModpack('modrinth', 'Bad Slug'), 'servers.validation.modpack_modrinth')
    expect(validateModpack('curseforge', 'atm10')).toBeNull()
    expect(validateModpack('curseforge', 'https://www.curseforge.com/minecraft/modpacks/all-the-mods-10')).toBeNull()
    expectError(validateModpack('curseforge', 'https://evil.example/minecraft/modpacks/x'), 'servers.validation.modpack_curseforge')
  })
})

describe('memory', () => {
  it('parses GiB input into MiB', () => {
    expect(memoryMiBFromGiB('4')).toBe(4096)
    expect(memoryMiBFromGiB('4.25')).toBe(4352)
    expect(memoryMiBFromGiB(' 6,5 ')).toBe(6656)
    expect(memoryMiBFromGiB('')).toBeNull()
    expect(memoryMiBFromGiB('abc')).toBeNull()
  })

  it('allows 1 to 32 GiB in steps of 0.25 GiB', () => {
    for (const mib of [1024, 4352, 32768]) expect(validateMemoryMiB(mib), String(mib)).toBeNull()
    for (const mib of [null, 768, 33024, 4000, 4403.2]) expectError(validateMemoryMiB(mib), 'servers.validation.memory')
    expect(validateMemoryMiB(512)?.params).toEqual({ min: 1, max: 32 })
  })
})

describe('properties', () => {
  it('rejects control characters and overlong text like unicode.IsPrint', () => {
    expect(validateText('§aWillkommen auf unserem Server äöü', 150)).toBeNull()
    expect(validateText('ä'.repeat(150), 150)).toBeNull()
    expectError(validateText('ä'.repeat(151), 150), 'servers.validation.text_too_long')
    for (const v of ['a\nENABLE_RCON=false', 'a\u0000b', 'a\tb', 'a\u00a0b', 'a\u200bb']) expectError(validateText(v, 150), 'servers.validation.text_control')
  })

  it('rejects backslashes like instancespec (motd and seed)', () => {
    expectError(validateText('C:\\worlds', 150), 'servers.validation.text_backslash')
    expectError(validateText('\\n', 64), 'servers.validation.text_backslash')
  })

  it('accepts whole numbers in range only', () => {
    expect(validateIntInRange('20', 1, 500)).toBeNull()
    expect(validateIntInRange('0', 0, 256)).toBeNull()
    for (const v of ['0', '501', '', '2.5', '-1', 'abc']) expectError(validateIntInRange(v, 1, 500), 'servers.validation.integer_range')
  })

  it('checks CurseForge keys', () => {
    expect(validateCurseForgeKey('$2a$10$abcdefghijklmnopqrstuv')).toBeNull()
    expectError(validateCurseForgeKey(''), 'servers.validation.curseforge_key_required')
    expectError(validateCurseForgeKey('k\nX=1'), 'servers.validation.curseforge_key_invalid')
    expectError(validateCurseForgeKey('x'.repeat(129)), 'servers.validation.curseforge_key_invalid')
  })
})
