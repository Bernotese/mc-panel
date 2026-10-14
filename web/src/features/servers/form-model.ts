import type { Catalog, Difficulty, Gamemode, InstanceConfig, InstanceType, JavaVersion, LevelType, ServerMemoryLimits, ServerProperties } from '@/api/types'
import { memoryInputValue } from './format'
import { javaFor } from './java'
import {
  DEFAULT_MEMORY_MIB,
  hasLoader,
  isModpack,
  MAX_MOTD_LENGTH,
  MAX_SEED_LENGTH,
  memoryMiBFromGiB,
  STATIC_MEMORY_LIMITS,
  validateIntInRange,
  validateLoaderVersion,
  validateMemoryMiB,
  validateModpack,
  validateServerName,
  validateText,
  validateVersion,
  type FieldError,
  type MemoryLimits,
} from './validation'

export const EULA_URL = 'https://aka.ms/MinecraftEULA'

/** Properties as edited in the form: numbers stay text until they are valid. */
export interface PropertiesDraft {
  motd: string
  difficulty: Difficulty
  gamemode: Gamemode
  max_players: string
  pvp: boolean
  hardcore: boolean
  allow_flight: boolean
  enable_command_block: boolean
  online_mode: boolean
  whitelist_enabled: boolean
  view_distance: string
  simulation_distance: string
  spawn_protection: string
  seed: string
  level_type: LevelType
}

export interface ServerFormValue {
  name: string
  type: InstanceType
  /** '' = newest version of the catalog. */
  version: string
  /** Free text instead of the catalog select. */
  manualVersion: boolean
  /** '' = latest loader (null in the API). */
  loaderVersion: string
  modpack: string
  /** '' = derived from type and version. */
  javaOverride: JavaVersion | ''
  memoryGiB: string
  properties: PropertiesDraft
  eulaAccepted: boolean
}

export interface ServerFormResult {
  name: string
  type: InstanceType
  version: string
  loader_version: string | null
  modpack: string | null
  java: JavaVersion
  memory_mib: number
  properties: ServerProperties
}

export const PROPERTY_FIELDS = [
  'properties.motd',
  'properties.max_players',
  'properties.view_distance',
  'properties.simulation_distance',
  'properties.spawn_protection',
  'properties.seed',
] as const

export type PropertyField = (typeof PROPERTY_FIELDS)[number]
export type FormField = 'name' | 'type' | 'version' | 'loader_version' | 'modpack' | 'java' | 'memory' | 'eula' | PropertyField
export type FormErrors = Partial<Record<FormField, FieldError>>

export interface FormContext {
  mode: 'create' | 'edit'
  catalog: Catalog | undefined
  curseforgeKeySet: boolean
  /** Per-server memory bounds of this host; omitted = instancespec's static 1..32 GiB. */
  memory?: MemoryLimits
}

/** The bounds the form enforces: the host limits once the panel reported them, the static ones before. */
export function memoryLimitsFrom(limits: ServerMemoryLimits | undefined): MemoryLimits {
  return limits ? { min: limits.min_mib, max: limits.max_mib, step: limits.step_mib } : STATIC_MEMORY_LIMITS
}

/**
 * Sum of all servers' memory if this server gets mib, when it exceeds the host memory (only a warning: stopped servers
 * use no memory). ownMiB is what this server already counts in allocated_mib (0 for a new one).
 */
export function memoryOvercommit(mib: number | null, limits: ServerMemoryLimits | undefined, ownMiB: number): { totalMiB: number; hostMiB: number } | null {
  if (mib === null || !limits || limits.host_total_mib === null) return null
  const totalMiB = limits.allocated_mib - ownMiB + mib
  return totalMiB > limits.host_total_mib ? { totalMiB, hostMiB: limits.host_total_mib } : null
}

/** Defaults of spec §7; the MOTD default (server name) is applied by the panel when motd is omitted. */
export function defaultPropertiesDraft(): PropertiesDraft {
  return {
    motd: '',
    difficulty: 'normal',
    gamemode: 'survival',
    max_players: '20',
    pvp: true,
    hardcore: false,
    allow_flight: false,
    enable_command_block: false,
    online_mode: true,
    whitelist_enabled: false,
    view_distance: '10',
    simulation_distance: '10',
    spawn_protection: '16',
    seed: '',
    level_type: 'normal',
  }
}

export function emptyServerForm(): ServerFormValue {
  return {
    name: '',
    type: 'paper',
    version: '',
    manualVersion: false,
    loaderVersion: '',
    modpack: '',
    javaOverride: '',
    memoryGiB: memoryInputValue(DEFAULT_MEMORY_MIB),
    properties: defaultPropertiesDraft(),
    eulaAccepted: false,
  }
}

function numberText(value: number | undefined, fallback: string): string {
  return value === undefined ? fallback : String(value)
}

export function formFromInstance(name: string, instance: InstanceConfig): ServerFormValue {
  const p = instance.properties
  const d = defaultPropertiesDraft()
  return {
    name,
    type: instance.type,
    version: instance.version,
    manualVersion: false,
    loaderVersion: instance.loader_version ?? '',
    modpack: instance.modpack ?? '',
    // A derived java stays "automatic" so that a version change re-derives it; an explicit override stays visible.
    javaOverride: instance.java === javaFor(instance.type, instance.version) ? '' : instance.java,
    memoryGiB: memoryInputValue(instance.memory_mib),
    properties: {
      motd: p.motd ?? name,
      difficulty: p.difficulty ?? d.difficulty,
      gamemode: p.gamemode ?? d.gamemode,
      max_players: numberText(p.max_players, d.max_players),
      pvp: p.pvp ?? d.pvp,
      hardcore: p.hardcore ?? d.hardcore,
      allow_flight: p.allow_flight ?? d.allow_flight,
      enable_command_block: p.enable_command_block ?? d.enable_command_block,
      online_mode: p.online_mode ?? d.online_mode,
      whitelist_enabled: p.whitelist_enabled ?? d.whitelist_enabled,
      view_distance: numberText(p.view_distance, d.view_distance),
      simulation_distance: numberText(p.simulation_distance, d.simulation_distance),
      spawn_protection: numberText(p.spawn_protection, d.spawn_protection),
      seed: p.seed ?? d.seed,
      level_type: p.level_type ?? d.level_type,
    },
    eulaAccepted: true,
  }
}

export function effectiveVersion(value: ServerFormValue, catalog: Catalog | undefined): string {
  if (isModpack(value.type)) return ''
  const typed = value.version.trim()
  return typed !== '' ? typed : (catalog?.versions[0]?.id ?? '')
}

export function effectiveJava(value: ServerFormValue, catalog: Catalog | undefined): JavaVersion {
  if (value.javaOverride !== '') return value.javaOverride
  const version = effectiveVersion(value, catalog)
  return catalog?.versions.find((v) => v.id === version)?.java ?? javaFor(value.type, version)
}

export function validateServerForm(value: ServerFormValue, ctx: FormContext): FormErrors {
  const errors: FormErrors = {}
  const check = (field: FormField, error: FieldError | null) => {
    if (error) errors[field] = error
  }
  if (ctx.mode === 'create') {
    check('name', validateServerName(value.name.trim()))
    if (value.type === 'curseforge' && !ctx.curseforgeKeySet) errors.type = { key: 'servers.validation.curseforge_key_missing' }
    if (!value.eulaAccepted) errors.eula = { key: 'servers.validation.eula_required' }
  }
  if (!isModpack(value.type)) check('version', validateVersion(effectiveVersion(value, ctx.catalog)))
  if (hasLoader(value.type)) check('loader_version', validateLoaderVersion(value.loaderVersion.trim()))
  check('modpack', validateModpack(value.type, value.modpack.trim()))
  if (value.type === 'curseforge' && effectiveJava(value, ctx.catalog) === 'java8') errors.java = { key: 'servers.validation.java_curseforge' }
  check('memory', validateMemoryMiB(memoryMiBFromGiB(value.memoryGiB), ctx.memory))
  const p = value.properties
  check('properties.motd', validateText(p.motd, MAX_MOTD_LENGTH))
  check('properties.max_players', validateIntInRange(p.max_players, 1, 500))
  check('properties.view_distance', validateIntInRange(p.view_distance, 3, 32))
  check('properties.simulation_distance', validateIntInRange(p.simulation_distance, 3, 32))
  check('properties.spawn_protection', validateIntInRange(p.spawn_protection, 0, 256))
  check('properties.seed', validateText(p.seed, MAX_SEED_LENGTH))
  return errors
}

/** Turns a valid form into the fields of CreateServerRequest/UpdateServerRequest; null while anything is invalid. */
export function resolveServerForm(value: ServerFormValue, ctx: FormContext): ServerFormResult | null {
  if (Object.keys(validateServerForm(value, ctx)).length > 0) return null
  const p = value.properties
  const properties: ServerProperties = {
    difficulty: p.difficulty,
    gamemode: p.gamemode,
    max_players: Number(p.max_players.trim()),
    pvp: p.pvp,
    hardcore: p.hardcore,
    allow_flight: p.allow_flight,
    enable_command_block: p.enable_command_block,
    online_mode: p.online_mode,
    whitelist_enabled: p.whitelist_enabled,
    view_distance: Number(p.view_distance.trim()),
    simulation_distance: Number(p.simulation_distance.trim()),
    spawn_protection: Number(p.spawn_protection.trim()),
    seed: p.seed,
    level_type: p.level_type,
  }
  // A new server without MOTD shows its name (spec §7); an existing one keeps exactly what the admin typed.
  if (ctx.mode === 'edit' || p.motd !== '') properties.motd = p.motd
  const loader = value.loaderVersion.trim()
  return {
    name: value.name.trim(),
    type: value.type,
    version: effectiveVersion(value, ctx.catalog),
    loader_version: hasLoader(value.type) && loader !== '' ? loader : null,
    modpack: isModpack(value.type) ? value.modpack.trim() : null,
    java: effectiveJava(value, ctx.catalog),
    memory_mib: memoryMiBFromGiB(value.memoryGiB) ?? DEFAULT_MEMORY_MIB,
    properties,
  }
}

export function sameFormValue(a: ServerFormValue, b: ServerFormValue): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}
