import { describe, expect, it } from 'vitest'
import { baseName, breadcrumbs, cleanPath, componentProblem, isHiddenPath, joinPath, parentPath, pathProblem, stripZip, targetProblem } from './path'

describe('cleanPath (mirrors filesapi.CleanPath)', () => {
  it.each([
    ['', ''],
    ['.', ''],
    ['server.properties', 'server.properties'],
    ['plugins/Essentials/config.yml', 'plugins/Essentials/config.yml'],
    ['Welt mit Leerzeichen/level.dat', 'Welt mit Leerzeichen/level.dat'],
    ['ünïcödé/日本', 'ünïcödé/日本'],
    ['plugins/Essentials/', 'plugins/Essentials'], // one trailing slash is fine (as in filesapi)
    ['.hidden', '.hidden'],
    ['a..b', 'a..b'],
  ])('accepts %j', (input, path) => {
    expect(cleanPath(input)).toEqual({ path })
  })

  it.each([
    ['/etc/passwd', 'invalid'],
    ['plugins\\x', 'invalid'],
    ['a//b', 'invalid'],
    ['a/b//', 'invalid'],
    ['a/./b', 'invalid'],
    ['./a', 'invalid'],
    ['..', 'dot_dot'],
    ['plugins/../../etc', 'dot_dot'],
    ['a\nb', 'control'],
    ['a\u0000b', 'control'],
    ['a\u007fb', 'control'],
    ['bad\ud800', 'invalid'],
    [`${'a/'.repeat(2048)}b`, 'too_long'],
    ['x'.repeat(256), 'component_too_long'],
    [`${'ä'.repeat(128)}`, 'component_too_long'], // 256 Byte UTF-8
  ])('rejects %j as %s', (input, problem) => {
    expect(cleanPath(input)).toEqual({ problem })
    expect(pathProblem(input)).toBe(problem)
  })

  it('allows exactly 255 bytes per component', () => {
    expect(pathProblem('x'.repeat(255))).toBeNull()
  })
})

describe('hidden names', () => {
  it.each(['.rcon-cli.env', '.rcon-cli.yaml', '.mcpanel-tmp-1', 'world/.mcpanel-tmp-x/a'])('hides %j', (p) => {
    expect(isHiddenPath(p)).toBe(true)
  })
  it.each(['plugins/.rcon-cli.env', 'rcon-cli.env', '.mcpanelx', 'server.properties'])('shows %j', (p) => {
    expect(isHiddenPath(p)).toBe(false)
  })
})

describe('componentProblem', () => {
  it.each([
    ['', 'empty'],
    ['   ', 'empty'],
    ['a/b', 'slash'],
    ['..', 'dot_dot'],
    ['.', 'invalid'],
    ['a\tb', 'control'],
    ['.mcpanel-x', 'hidden'],
    ['x'.repeat(256), 'component_too_long'],
  ])('rejects %j as %s', (name, problem) => {
    expect(componentProblem(name)).toBe(problem)
  })
  it('accepts a normal name', () => {
    expect(componentProblem('config.yml')).toBeNull()
  })
  it('rejects the top-level RCON files only at the top level', () => {
    expect(componentProblem('.rcon-cli.env', '')).toBe('hidden')
    expect(componentProblem('.rcon-cli.env', 'plugins')).toBeNull()
  })
})

describe('targetProblem', () => {
  it.each([
    ['', 'empty'],
    ['plugins/../x', 'dot_dot'],
    ['.rcon-cli.env', 'hidden'],
    ['world/.mcpanel-tmp', 'hidden'],
  ])('rejects %j as %s', (target, problem) => {
    expect(targetProblem(target)).toBe(problem)
  })
  it('accepts a nested target', () => {
    expect(targetProblem('plugins/alt/config.yml')).toBeNull()
  })
})

describe('path helpers', () => {
  it('joins, splits and names paths', () => {
    expect(joinPath('', 'a')).toBe('a')
    expect(joinPath('a/b', 'c')).toBe('a/b/c')
    expect(parentPath('a/b/c')).toBe('a/b')
    expect(parentPath('a')).toBe('')
    expect(parentPath('')).toBe('')
    expect(baseName('a/b/c.yml')).toBe('c.yml')
    expect(baseName('')).toBe('')
    expect(stripZip('world-backup.zip')).toBe('world-backup')
    expect(stripZip('WORLD.ZIP')).toBe('WORLD')
    expect(stripZip('notes.txt')).toBe('notes.txt')
  })

  it('builds breadcrumbs from the root', () => {
    expect(breadcrumbs('')).toEqual([])
    expect(breadcrumbs('plugins/Essentials')).toEqual([
      { name: 'plugins', path: 'plugins' },
      { name: 'Essentials', path: 'plugins/Essentials' },
    ])
  })
})
