import type { FileEntry } from '@/api/types'

/** Defaults of files.toml (spec §3.1); the service is authoritative, the UI only avoids pointless requests. */
export const EDITOR_MAX_BYTES = 5 * 1024 * 1024
export const MAX_UPLOAD_BYTES = 4 * 1024 ** 3

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const

export function formatBytes(bytes: number, locale: string): string {
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit += 1
  }
  const text = new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(value)
  return `${text} ${UNITS[unit]}`
}

export type EditorLanguage = 'yaml' | 'json' | 'toml' | 'properties' | 'text'

const LANGUAGES: Record<string, EditorLanguage> = {
  yml: 'yaml',
  yaml: 'yaml',
  json: 'json',
  json5: 'json',
  mcmeta: 'json',
  toml: 'toml',
  properties: 'properties',
  ini: 'properties',
  cfg: 'properties',
  conf: 'properties',
}

export function editorLanguage(name: string): EditorLanguage {
  const dot = name.lastIndexOf('.')
  if (dot <= 0) return 'text' // no extension, or a dotfile such as .gitignore
  return LANGUAGES[name.slice(dot + 1).toLowerCase()] ?? 'text'
}

export type SortKey = 'name' | 'size' | 'mtime'

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/** Folders always come first; ties fall back to the name. Returns a new array. */
export function sortEntries(entries: readonly FileEntry[], key: SortKey, descending: boolean): FileEntry[] {
  const factor = descending ? -1 : 1
  return [...entries].sort((a, b) => {
    const folderA = a.type === 'dir' ? 0 : 1
    const folderB = b.type === 'dir' ? 0 : 1
    if (folderA !== folderB) return folderA - folderB
    let cmp = 0
    if (key === 'size') cmp = a.size - b.size
    if (key === 'mtime') cmp = a.mtime - b.mtime
    if (cmp === 0) cmp = collator.compare(a.name, b.name)
    return cmp * factor
  })
}

export function isZip(name: string): boolean {
  return name.toLowerCase().endsWith('.zip')
}
