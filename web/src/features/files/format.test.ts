import { describe, expect, it } from 'vitest'
import { editorLanguage, formatBytes, isZip, sortEntries } from './format'
import type { FileEntry } from '@/api/types'

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [1023, '1.023 B'],
    [1024, '1 KiB'],
    [1536, '1,5 KiB'],
    [2 * 1024 * 1024, '2 MiB'],
    [4 * 1024 ** 3, '4 GiB'],
  ])('%d bytes → %s', (bytes, text) => {
    expect(formatBytes(bytes, 'de')).toBe(text)
  })
  it('uses the locale', () => {
    expect(formatBytes(1536, 'en')).toBe('1.5 KiB')
  })
})

describe('editorLanguage', () => {
  it.each([
    ['config.yml', 'yaml'],
    ['paper-global.YAML', 'yaml'],
    ['ops.json', 'json'],
    ['pack.mcmeta', 'json'],
    ['fabric.toml', 'toml'],
    ['server.properties', 'properties'],
    ['settings.ini', 'properties'],
    ['latest.log', 'text'],
    ['Dockerfile', 'text'],
    ['.gitignore', 'text'],
  ])('%s → %s', (name, language) => {
    expect(editorLanguage(name)).toBe(language)
  })
})

describe('sortEntries', () => {
  const entries: FileEntry[] = [
    { name: 'b.txt', type: 'file', size: 10, mtime: 3 },
    { name: 'world', type: 'dir', size: 0, mtime: 1 },
    { name: 'a10.txt', type: 'file', size: 30, mtime: 2 },
    { name: 'a9.txt', type: 'file', size: 20, mtime: 4 },
    { name: 'plugins', type: 'dir', size: 0, mtime: 5 },
  ]
  const names = (list: FileEntry[]) => list.map((e) => e.name)

  it('keeps folders first and sorts names naturally', () => {
    expect(names(sortEntries(entries, 'name', false))).toEqual(['plugins', 'world', 'a9.txt', 'a10.txt', 'b.txt'])
    expect(names(sortEntries(entries, 'name', true))).toEqual(['world', 'plugins', 'b.txt', 'a10.txt', 'a9.txt'])
  })
  it('sorts by size and date', () => {
    expect(names(sortEntries(entries, 'size', true)).slice(2)).toEqual(['a10.txt', 'a9.txt', 'b.txt'])
    expect(names(sortEntries(entries, 'mtime', false))).toEqual(['world', 'plugins', 'a10.txt', 'b.txt', 'a9.txt'])
  })
  it('does not change the input', () => {
    const copy = [...entries]
    sortEntries(entries, 'size', false)
    expect(entries).toEqual(copy)
  })
})

it('recognises zip files', () => {
  expect(isZip('world.zip')).toBe(true)
  expect(isZip('WORLD.ZIP')).toBe(true)
  expect(isZip('world.zip.txt')).toBe(false)
})
