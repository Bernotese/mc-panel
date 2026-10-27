import type { DevInstance, Server, UpdateDevRequest } from '@/api/types'
import { minutesToTime, timeToMinutes } from '@/features/backups/model'
import { validateIntInRange, type FieldError } from '@/features/servers/validation'
import { formatDateTime, formatTime } from '@/lib/format'

/** Dev instances spec §2: the dev of <name> is always dev-<name>, and names have at most 32 characters. */
export const DEV_PREFIX = 'dev-'
const MAX_NAME = 32

export const IDLE_MINUTES = { min: 5, max: 120 } as const
export const DISTANCE = { min: 3, max: 32 } as const

export function devName(prodName: string): string {
  return DEV_PREFIX + prodName
}

/** False for prod names longer than 28 characters: their dev would exceed the name limit. */
export function canHaveDev(prodName: string): boolean {
  return devName(prodName).length <= MAX_NAME
}

/** Suggested dev memory (spec §2): 2 GiB, 3 GiB with a mod loader or modpack, at most the host's limit. */
export function defaultDevMemoryMiB(server: Pick<Server, 'instance'>, maxMiB: number): number {
  const type = server.instance?.type
  const loader = type === 'fabric' || type === 'neoforge' || type === 'modrinth' || type === 'curseforge'
  return Math.min(loader ? 3072 : 2048, maxMiB)
}

type TreeServer = Pick<Server, 'id' | 'dev'>

export interface TreeEntry<T extends TreeServer> {
  server: T
  dev: T | null
}

/** Spec §8: a dev is listed directly under its prod; a dev whose prod is not in the list stays where it is. */
export function serverTreeEntries<T extends TreeServer>(servers: readonly T[]): TreeEntry<T>[] {
  const ids = new Set(servers.map((s) => s.id))
  const nested = (s: T) => s.dev !== undefined && ids.has(s.dev.source_id)
  const devs = new Map(servers.filter(nested).map((s) => [s.dev?.source_id, s]))
  return servers.filter((s) => !nested(s)).map((s) => ({ server: s, dev: devs.get(s.id) ?? null }))
}

/** The list order of the dashboard: every dev right after its prod. */
export function withDevsAfterSources<T extends TreeServer>(servers: readonly T[]): T[] {
  return serverTreeEntries(servers).flatMap((e) => (e.dev ? [e.server, e.dev] : [e.server]))
}

function localDay(ms: number): number {
  const d = new Date(ms)
  return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000
}

/** "heute 05:00", "morgen 05:00" (or "today/tomorrow" in English); further away the full date. */
export function formatSyncWhen(ms: number, locale: string, now: number = Date.now()): string {
  const days = localDay(ms) - localDay(now)
  if (Math.abs(days) > 1) return formatDateTime(ms, locale)
  const day = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' }).format(days, 'day')
  return `${day} ${formatTime(ms, locale)}`
}

/** Form state of the dev settings: numbers stay strings while the user types. */
export interface DevSettingsDraft {
  time: string
  idle: string
  view: string
  simulation: string
}

export type DevSettingsErrors = Partial<Record<keyof DevSettingsDraft, FieldError>>

export function draftFromDev(dev: DevInstance): DevSettingsDraft {
  return {
    time: minutesToTime(dev.sync_time),
    idle: String(dev.idle_minutes),
    view: String(dev.view_distance),
    simulation: String(dev.simulation_distance),
  }
}

export function validateDevSettings(d: DevSettingsDraft): DevSettingsErrors {
  const errors: DevSettingsErrors = {}
  if (timeToMinutes(d.time) === null) errors.time = { key: 'backups.schedule.validation.time' }
  const idle = validateIntInRange(d.idle, IDLE_MINUTES.min, IDLE_MINUTES.max)
  if (idle) errors.idle = idle
  const view = validateIntInRange(d.view, DISTANCE.min, DISTANCE.max)
  if (view) errors.view = view
  const simulation = validateIntInRange(d.simulation, DISTANCE.min, DISTANCE.max)
  if (simulation) errors.simulation = simulation
  return errors
}

/** Only the fields that differ from the stored dev (omitted = unchanged); call after validateDevSettings passed. */
export function devSettingsPatch(d: DevSettingsDraft, dev: DevInstance): UpdateDevRequest {
  const patch: UpdateDevRequest = {}
  const time = timeToMinutes(d.time)
  if (time !== null && time !== dev.sync_time) patch.sync_time = time
  const idle = Number(d.idle.trim())
  if (idle !== dev.idle_minutes) patch.idle_minutes = idle
  const view = Number(d.view.trim())
  if (view !== dev.view_distance) patch.view_distance = view
  const simulation = Number(d.simulation.trim())
  if (simulation !== dev.simulation_distance) patch.simulation_distance = simulation
  return patch
}
