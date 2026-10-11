import type { Difficulty, Gamemode, InstanceType, JavaVersion, LevelType } from '@/api/types'

/** An i18n key plus interpolation values; components translate it, tests check that the key exists. */
export interface FieldError {
  key: string
  params?: Record<string, string | number>
}

export const INSTANCE_TYPES: readonly InstanceType[] = ['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge']
export const JAVA_VERSIONS: readonly JavaVersion[] = ['java8', 'java17', 'java21', 'java25']
export const DIFFICULTIES: readonly Difficulty[] = ['peaceful', 'easy', 'normal', 'hard']
export const GAMEMODES: readonly Gamemode[] = ['survival', 'creative', 'adventure', 'spectator']
export const LEVEL_TYPES: readonly LevelType[] = ['normal', 'flat', 'large_biomes', 'amplified']

// Mirrors of internal/agent/instancespec – the panel stays authoritative, these only drive inline hints.
export const MIN_MEMORY_MIB = 1024
export const MAX_MEMORY_MIB = 32768
export const MEMORY_STEP_MIB = 256
export const DEFAULT_MEMORY_MIB = 4096
export const MAX_MOTD_LENGTH = 150
export const MAX_SEED_LENGTH = 64
export const MAX_BASE_DOMAIN_LENGTH = 200

const SERVER_NAME_RE = /^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$/
const VERSION_RE = /^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$/
const SLUG_RE = /^[a-z0-9-]{1,64}$/
const CURSEFORGE_URL_RE = /^https:\/\/www\.curseforge\.com\/minecraft\/modpacks\/[a-z0-9-]{1,64}$/
const CURSEFORGE_KEY_RE = /^[A-Za-z0-9$./_-]{1,128}$/
const DNS_LABEL_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
// Go's unicode.IsPrint rejects control/format/unassigned characters and every space except U+0020.
const NON_PRINTABLE_RE = /\p{C}|[^\S ]/u

function fieldError(key: string, params?: Record<string, string | number>): FieldError {
  return params ? { key, params } : { key }
}

export function isModpack(type: InstanceType): boolean {
  return type === 'modrinth' || type === 'curseforge'
}

export function hasLoader(type: InstanceType): boolean {
  return type === 'fabric' || type === 'neoforge'
}

export function validateServerName(name: string): FieldError | null {
  if (name === '') return fieldError('servers.validation.name_required')
  return SERVER_NAME_RE.test(name) ? null : fieldError('servers.validation.name_invalid')
}

export function validateVersion(version: string): FieldError | null {
  if (version === '') return fieldError('servers.validation.version_required')
  return version === 'LATEST' || VERSION_RE.test(version) ? null : fieldError('servers.validation.version_invalid')
}

/** Empty means "latest loader". */
export function validateLoaderVersion(loader: string): FieldError | null {
  if (loader === '') return null
  return VERSION_RE.test(loader) ? null : fieldError('servers.validation.loader_invalid')
}

export function validateModpack(type: InstanceType, modpack: string): FieldError | null {
  if (!isModpack(type)) return null
  if (modpack === '') return fieldError('servers.validation.modpack_required')
  if (type === 'modrinth') return SLUG_RE.test(modpack) ? null : fieldError('servers.validation.modpack_modrinth')
  return SLUG_RE.test(modpack) || CURSEFORGE_URL_RE.test(modpack) ? null : fieldError('servers.validation.modpack_curseforge')
}

/** Parses the GiB input ("4", "4.25", "4,25") into MiB; null for empty or non-numeric input. */
export function memoryMiBFromGiB(text: string): number | null {
  const normalized = text.trim().replace(',', '.')
  if (normalized === '') return null
  const gib = Number(normalized)
  return Number.isFinite(gib) ? gib * 1024 : null
}

export function validateMemoryMiB(mib: number | null): FieldError | null {
  const valid = mib !== null && Number.isInteger(mib) && mib >= MIN_MEMORY_MIB && mib <= MAX_MEMORY_MIB && mib % MEMORY_STEP_MIB === 0
  return valid ? null : fieldError('servers.validation.memory', { min: MIN_MEMORY_MIB / 1024, max: MAX_MEMORY_MIB / 1024 })
}

/**
 * MOTD and seed: printable text without backslashes (instancespec.printable – itzg would read a backslash as an escape),
 * counted in code points like Go's utf8.RuneCountInString.
 */
export function validateText(value: string, maxLength: number): FieldError | null {
  if ([...value].length > maxLength) return fieldError('servers.validation.text_too_long', { max: maxLength })
  if (NON_PRINTABLE_RE.test(value)) return fieldError('servers.validation.text_control')
  return value.includes('\\') ? fieldError('servers.validation.text_backslash') : null
}

export function validateIntInRange(text: string, min: number, max: number): FieldError | null {
  const trimmed = text.trim()
  const n = Number(trimmed)
  return /^\d+$/.test(trimmed) && n >= min && n <= max ? null : fieldError('servers.validation.integer_range', { min, max })
}

/** The panel stores base domains in lower case; the form sends them that way. */
export function normalizeBaseDomain(domain: string): string {
  return domain.trim().toLowerCase()
}

export function validateBaseDomain(domain: string): FieldError | null {
  if (domain === '') return fieldError('servers.validation.base_domain_required')
  const labels = domain.split('.')
  const valid = domain.length <= MAX_BASE_DOMAIN_LENGTH && labels.length >= 2 && labels.every((l) => DNS_LABEL_RE.test(l))
  return valid ? null : fieldError('servers.validation.base_domain_invalid')
}

export function validateCurseForgeKey(key: string): FieldError | null {
  if (key === '') return fieldError('servers.validation.curseforge_key_required')
  return CURSEFORGE_KEY_RE.test(key) ? null : fieldError('servers.validation.curseforge_key_invalid')
}
