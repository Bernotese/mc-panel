import type {
  InstalledJar,
  InstanceType,
  PluginJob,
  PluginJobResponse,
  PluginKind,
  PluginResolution,
  PluginSearchHit,
  PluginSource,
  PluginUpdate,
  PluginVersion,
} from '@/api/types'

/** What the install dialog is opened for: a search hit or an installed jar ("Version wechseln"). */
export interface InstallTarget {
  source: PluginSource
  /** project_id of the hit resp. project.id of the jar (V3 `project`). */
  project: string
  title: string
  /** Preselected version id; the dialog falls back to the newest release. */
  version?: string | null
}

const KINDS: Partial<Record<InstanceType, PluginKind>> = { paper: 'plugin', purpur: 'plugin', fabric: 'mod', neoforge: 'mod' }

/** Spec §3.1: only these four types get a "Plugins"/"Mods" tab; vanilla and modpacks never (no 409 in the UI). */
export function pluginKindOf(type: InstanceType): PluginKind | null {
  return KINDS[type] ?? null
}

/** Spec §3.1: Hangar has no mods. Used until the inventory names the sources itself. */
export function sourcesFor(kind: PluginKind): PluginSource[] {
  return kind === 'plugin' ? ['modrinth', 'hangar'] : ['modrinth']
}

/** Spec §7.1: project title, else the name from plugin.yml/fabric.mod.json/neoforge.mods.toml, else the file name. */
export function jarName(jar: InstalledJar): string {
  return jar.project?.title.trim() || jar.meta?.name.trim() || jar.file
}

export function jarVersion(jar: InstalledJar): string | null {
  return jar.version?.name || jar.meta?.version || null
}

/** Versions come newest first (V3). Spec §7.1: default is the newest release; external-only ones cannot be chosen. */
export function defaultVersion(versions: readonly PluginVersion[], preferred?: string | null): string | null {
  const downloadable = versions.filter((v) => v.downloadable)
  if (preferred && downloadable.some((v) => v.id === preferred)) return preferred
  return (downloadable.find((v) => v.channel === 'release') ?? downloadable[0])?.id ?? null
}

export function isJobRunning(data: PluginJobResponse | undefined): boolean {
  return data?.job?.state === 'running'
}

export function updatesByFile(updates: readonly PluginUpdate[] | undefined): Map<string, PluginUpdate> {
  return new Map((updates ?? []).map((u) => [u.file, u]))
}

/** Spec §6.3: an incompatible dependency makes the plan unusable; an empty plan has nothing to do. */
export function canInstall(plan: PluginResolution): boolean {
  return plan.incompatible.length === 0 && plan.install.length > 0
}

export function jobProgress(job: PluginJob): { finished: number; total: number } {
  const finished = job.steps.filter((s) => s.state === 'done' || s.state === 'failed' || s.state === 'skipped').length
  return { finished, total: job.steps.length }
}

/** Upstream rankings shift between pages; a repeated project would show twice (and collide as React key). */
export function uniqueHits(hits: readonly PluginSearchHit[]): PluginSearchHit[] {
  const seen = new Set<string>()
  return hits.filter((h) => {
    const key = `${h.source}:${h.project_id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

const ICON_PROXY = /^\/api\/servers\/\d+\/plugins\/icon\?url=/

/** Spec §6.8: icons only through the panel's proxy (CSP img-src 'self'); any other URL becomes the placeholder. */
export function iconSrc(url: string | null | undefined): string | null {
  return url && ICON_PROXY.test(url) ? url : null
}

/** Links from upstream answers only for http(s); a javascript: or data: URL never becomes an href. */
export function externalHref(url: string | null | undefined): string | null {
  if (!url) return null
  try {
    const { protocol } = new URL(url)
    return protocol === 'https:' || protocol === 'http:' ? url : null
  } catch {
    return null
  }
}

/** Issue #85: the "Ergebnisse von …" line links to the source's own listing (fixed URLs, never from an upstream answer). */
export function sourceHomeUrl(source: PluginSource, kind: PluginKind): string {
  if (source === 'hangar') return 'https://hangar.papermc.io/'
  return kind === 'mod' ? 'https://modrinth.com/mods' : 'https://modrinth.com/plugins'
}

export type StatusFilter = 'all' | 'enabled' | 'disabled'
export type OriginFilter = 'all' | InstalledJar['source']

export interface JarFilter {
  query: string
  status: StatusFilter
  origin: OriginFilter
}

export const NO_JAR_FILTER: JarFilter = { query: '', status: 'all', origin: 'all' }

export function isJarFilterActive(f: JarFilter): boolean {
  return f.query.trim() !== '' || f.status !== 'all' || f.origin !== 'all'
}

/** Issue #85: the "Installiert" filters; the name search matches the shown name and the file name, case-insensitively. */
export function filterJars(jars: readonly InstalledJar[], f: JarFilter): InstalledJar[] {
  const needle = f.query.trim().toLowerCase()
  return jars.filter(
    (jar) =>
      (f.status === 'all' || jar.enabled === (f.status === 'enabled')) &&
      (f.origin === 'all' || jar.source === f.origin) &&
      (needle === '' || jarName(jar).toLowerCase().includes(needle) || jar.file.toLowerCase().includes(needle)),
  )
}
