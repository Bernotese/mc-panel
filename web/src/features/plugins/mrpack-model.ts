import type { JavaVersion, MrpackIndex, MrpackPlan, MrpackPlanEntry, UpdateServerRequest } from '@/api/types'
import { MrpackReadError } from './mrpack-read'

/** The panel's request body limit (mrpack spec §2). */
export const MAX_BODY_BYTES = 1024 * 1024

export function jsonBytes(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value)).length
}

/**
 * The index as it is sent: downloads and fileSize are never used by the panel (spec §3.1) and only cost body bytes,
 * so they are dropped from every file entry. Everything else goes as it is; the panel validates it (spec §4.1).
 */
export function toRequestIndex(raw: unknown): MrpackIndex {
  const index = { ...(raw as Record<string, unknown>) }
  if (Array.isArray(index.files)) {
    index.files = index.files.map((f: unknown) => {
      if (typeof f !== 'object' || f === null || Array.isArray(f)) return f
      const rest = { ...(f as Record<string, unknown>) }
      delete rest.downloads
      delete rest.fileSize
      return rest
    })
  }
  if (jsonBytes(index) > MAX_BODY_BYTES) {
    throw new MrpackReadError('too_large_to_send', 'the index exceeds 1 MiB without downloads and fileSize')
  }
  return index as unknown as MrpackIndex
}

export interface PlanGroups {
  install: MrpackPlanEntry[]
  update: MrpackPlanEntry[]
  unchanged: MrpackPlanEntry[]
  skip: MrpackPlanEntry[]
}

export function groupPlan(plan: MrpackPlan): PlanGroups {
  const groups: PlanGroups = { install: [], update: [], unchanged: [], skip: [] }
  for (const e of plan.entries) groups[e.action].push(e)
  return groups
}

/** Optional install/update entries the user deselected; only these may go into exclude (spec §4). */
export function excludeList(plan: MrpackPlan, deselected: ReadonlySet<string>): string[] {
  return plan.entries.filter((e) => e.optional && (e.action === 'install' || e.action === 'update') && deselected.has(e.sha512)).map((e) => e.sha512)
}

/** Install and update steps the import will run. */
export function importCount(plan: MrpackPlan, deselected: ReadonlySet<string>): number {
  return plan.entries.filter((e) => (e.action === 'install' || e.action === 'update') && !(e.optional && deselected.has(e.sha512))).length
}

export function entryTitle(e: MrpackPlanEntry): string {
  return e.project?.title.trim() || e.file
}

export function minecraftMismatch(plan: MrpackPlan): boolean {
  return plan.game_version.pack !== plan.game_version.server
}

export function loaderMismatch(plan: MrpackPlan): boolean {
  return plan.loader.server !== null && plan.loader.server !== plan.loader.pack
}

/**
 * "Server-Version auf X umstellen" (spec §5): the pack's Minecraft version with the Java the catalog names for it, and the
 * pack's loader version only where the server pins one that differs (a server on the newest loader stays there).
 */
export function versionSwitch(plan: MrpackPlan, java: JavaVersion): UpdateServerRequest {
  const body: UpdateServerRequest = {}
  if (minecraftMismatch(plan)) {
    body.version = plan.game_version.pack
    body.java = java
  }
  if (loaderMismatch(plan)) body.loader_version = plan.loader.pack
  return body
}
