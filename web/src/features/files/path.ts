/** Same rules as filesapi.CleanPath (spec §3.4). The service checks again; this only spares a round trip. */
export const MAX_PATH_BYTES = 4096
export const MAX_COMPONENT_BYTES = 255

export type PathProblem = 'empty' | 'slash' | 'invalid' | 'dot_dot' | 'control' | 'too_long' | 'component_too_long' | 'hidden'

const encoder = new TextEncoder()
const byteLength = (s: string) => encoder.encode(s).length
/** A lone surrogate is not valid UTF-8 (a paired one forms a single code point and does not match). */
const LONE_SURROGATE = /\p{Cs}/u
const HIDDEN_TOP = new Set(['.rcon-cli.env', '.rcon-cli.yaml'])
const HIDDEN_PREFIX = '.mcpanel-'

function hasControl(s: string): boolean {
  for (const ch of s) {
    const code = ch.codePointAt(0) ?? 0
    if (code < 0x20 || code === 0x7f) return true
  }
  return false
}

export function cleanPath(input: string): { path: string } | { problem: PathProblem } {
  if (input === '' || input === '.') return { path: '' }
  if (hasControl(input)) return { problem: 'control' }
  if (LONE_SURROGATE.test(input) || input.startsWith('/') || input.includes('\\')) return { problem: 'invalid' }
  if (byteLength(input) > MAX_PATH_BYTES) return { problem: 'too_long' }
  // Like filesapi.CleanPath: one trailing slash (a folder path) is dropped, a second one is an empty component.
  const parts = (input.endsWith('/') ? input.slice(0, -1) : input).split('/')
  for (const part of parts) {
    if (part === '..') return { problem: 'dot_dot' }
    if (part === '' || part === '.') return { problem: 'invalid' }
    if (byteLength(part) > MAX_COMPONENT_BYTES) return { problem: 'component_too_long' }
  }
  return { path: parts.join('/') }
}

export function pathProblem(input: string): PathProblem | null {
  const result = cleanPath(input)
  return 'problem' in result ? result.problem : null
}

/** True for the top-level RCON files and anything named .mcpanel-* at any depth (spec §3.4). */
export function isHiddenPath(path: string): boolean {
  if (path === '') return false
  const parts = path.split('/')
  if (parts.length === 1 && HIDDEN_TOP.has(parts[0])) return true
  return parts.some((p) => p.startsWith(HIDDEN_PREFIX))
}

/** Checks a single new name (folder, file, upload) that will be created in dir. */
export function componentProblem(name: string, dir = ''): PathProblem | null {
  if (name.trim() === '') return 'empty'
  if (name.includes('/')) return 'slash'
  const problem = pathProblem(name)
  if (problem) return problem
  if (name === '.') return 'invalid'
  return isHiddenPath(joinPath(dir, name)) ? 'hidden' : null
}

/** A rename/move target: a valid, non-root path that names nothing hidden. */
export function targetProblem(target: string): PathProblem | null {
  if (target.trim() === '') return 'empty'
  return pathProblem(target) ?? (isHiddenPath(target) ? 'hidden' : null)
}

export function joinPath(dir: string, name: string): string {
  return dir === '' ? name : `${dir}/${name}`
}

export function parentPath(path: string): string {
  const i = path.lastIndexOf('/')
  return i < 0 ? '' : path.slice(0, i)
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1)
}

export function breadcrumbs(path: string): { name: string; path: string }[] {
  if (path === '') return []
  const parts = path.split('/')
  return parts.map((name, i) => ({ name, path: parts.slice(0, i + 1).join('/') }))
}

export function stripZip(name: string): string {
  return name.toLowerCase().endsWith('.zip') ? name.slice(0, -4) : name
}
