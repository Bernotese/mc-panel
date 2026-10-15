import { http, HttpResponse } from 'msw'
import type {
  InstallPluginRequest,
  InstalledJar,
  MrpackEnvSide,
  MrpackImportRequest,
  MrpackPlan,
  MrpackPlanEntry,
  PluginChannel,
  PluginFileRequest,
  PluginInventory,
  PluginJob,
  PluginJobStep,
  PluginKind,
  PluginPlanEntry,
  PluginProjectRef,
  PluginResolution,
  PluginSearchHit,
  PluginSource,
  PluginStepErrorCode,
  PluginUpdate,
  PluginVersion,
  TogglePluginRequest,
  UpdatePluginsRequest,
} from '@/api/types'
import { DAY, db, findUser, pluginIconUrl, recordAudit, type MockPluginChange, type MockPluginInventory, type MockServer } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

/** A mock job takes about this long; its steps get equal shares. */
export const PLUGIN_JOB_MS = 4000
const PAGE = 20
const MAX_OFFSET = 1000
const MAX_QUERY = 100
const MAX_VERSIONS = 25
const MAX_JOBS = 4 // panel-wide (V3: 429 rate_limited)
const MAX_DEPTH = 5
const MAX_PACKAGES = 20
const MIB = 1024 * 1024
const NAME_RE = /^[A-Za-z0-9._+-]{1,128}$/
const JAR_RE = /^.+\.jar(\.disabled)?$/i
// eslint-disable-next-line no-control-regex -- matches control characters on purpose (filesapi.CleanPath)
const BAD_FILE_RE = /[\u0000-\u001f\u007f\\/]/
const ICON_PREFIXES = ['https://cdn.modrinth.com/', 'https://hangarcdn.papermc.io/']
const CHANNEL_RANK: Record<PluginChannel, number> = { release: 0, beta: 1, alpha: 2 }
/** A 1×1 PNG, served for every icon. */
const ICON_PNG = Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII='), (c) => c.charCodeAt(0))

type Params = Parameters<typeof idParam>[0]

interface MockVersion {
  id: string
  name: string
  channel: PluginChannel
  daysAgo: number
  filename: string
  size: number
  external: string | null // only available externally (Hangar externalUrl)
}

interface MockProject {
  source: PluginSource
  kind: PluginKind
  id: string
  slug: string
  title: string
  author: string
  description: string
  downloads: number
  clientOnly: boolean // Modrinth server_side: unsupported
  deps: string[] // required dependencies: project ids of the same source
  versions: MockVersion[] // newest first; every one is compatible with every game version
}

function v(id: string, name: string, channel: PluginChannel, daysAgo: number, filename: string, external: string | null = null): MockVersion {
  return { id, name, channel, daysAgo, filename, size: 2 * MIB, external }
}

/** The mock catalog (V5 plus chunkyborder, deviation 7 of plan 01). */
const CATALOG: MockProject[] = [
  {
    source: 'modrinth', kind: 'plugin', id: 'Vebnzrzj', slug: 'luckperms', title: 'LuckPerms', author: 'Luck',
    description: 'A permissions plugin for Minecraft servers.', downloads: 3_400_000, clientOnly: false, deps: [],
    versions: [
      v('lp560b1', '5.6.0-beta.1', 'beta', 2, 'LuckPerms-Bukkit-5.6.0-beta.1.jar'),
      v('lp5572', '5.5.72', 'release', 5, 'LuckPerms-Bukkit-5.5.72.jar'),
      v('lp5571', '5.5.71', 'release', 40, 'LuckPerms-Bukkit-5.5.71.jar'),
    ],
  },
  {
    source: 'modrinth', kind: 'plugin', id: 'fALzjamp', slug: 'chunky', title: 'Chunky', author: 'pop4959',
    description: 'Pre-generates chunks, quickly and efficiently.', downloads: 2_100_000, clientOnly: false, deps: [],
    versions: [v('ch1441b', '1.4.41-beta', 'beta', 3, 'Chunky-Bukkit-1.4.41-beta.jar'), v('ch1440', '1.4.40', 'release', 20, 'Chunky-Bukkit-1.4.40.jar')],
  },
  {
    source: 'modrinth', kind: 'plugin', id: 's86X568j', slug: 'chunkyborder', title: 'ChunkyBorder', author: 'pop4959',
    description: 'A world border plugin that works with Chunky.', downloads: 600_000, clientOnly: false, deps: ['fALzjamp'],
    versions: [v('cb1224b', '1.2.24-beta', 'beta', 4, 'ChunkyBorder-Bukkit-1.2.24-beta.jar'), v('cb1223', '1.2.23', 'release', 25, 'ChunkyBorder-Bukkit-1.2.23.jar')],
  },
  {
    source: 'modrinth', kind: 'plugin', id: 'swbUV1cr', slug: 'bluemap', title: 'BlueMap', author: 'TBlueF',
    description: 'A 3D map of your Minecraft world in the browser.', downloads: 1_500_000, clientOnly: false, deps: [],
    versions: [v('bm58b', '5.8-beta', 'beta', 6, 'bluemap-5.8-beta-paper.jar'), v('bm57', '5.7', 'release', 30, 'bluemap-5.7-paper.jar')],
  },
  {
    source: 'hangar', kind: 'plugin', id: '2', slug: 'ViaVersion', title: 'ViaVersion', author: 'ViaVersion',
    description: 'Allows newer clients to connect to older servers.', downloads: 9_000_000, clientOnly: false, deps: [],
    versions: [v('5.6.0-beta', '5.6.0-beta', 'beta', 1, 'ViaVersion-5.6.0-beta.jar'), v('5.5.1', '5.5.1', 'release', 12, 'ViaVersion-5.5.1.jar')],
  },
  {
    source: 'hangar', kind: 'plugin', id: '3', slug: 'ViaBackwards', title: 'ViaBackwards', author: 'ViaVersion',
    description: 'Allows older clients to connect to newer servers.', downloads: 4_000_000, clientOnly: false, deps: ['2'],
    versions: [v('5.6.0-beta', '5.6.0-beta', 'beta', 1, 'ViaBackwards-5.6.0-beta.jar'), v('5.5.1', '5.5.1', 'release', 12, 'ViaBackwards-5.5.1.jar')],
  },
  {
    source: 'hangar', kind: 'plugin', id: '50', slug: 'Essentials', title: 'EssentialsX', author: 'EssentialsX',
    description: 'The essential plugin suite for Minecraft servers.', downloads: 5_000_000, clientOnly: false, deps: [],
    versions: [
      v('2.22.0-dev', '2.22.0-dev', 'beta', 2, 'EssentialsX-2.22.0-dev.jar', 'https://essentialsx.net/downloads.html'),
      v('2.21.2', '2.21.2', 'release', 60, 'EssentialsX-2.21.2.jar', 'https://github.com/EssentialsX/Essentials/releases/tag/2.21.2'),
    ],
  },
  {
    source: 'hangar', kind: 'plugin', id: '25', slug: 'Chunky', title: 'Chunky', author: 'pop4959',
    description: 'Pre-generates chunks, quickly and efficiently.', downloads: 800_000, clientOnly: false, deps: [],
    versions: [v('1.4.41-beta', '1.4.41-beta', 'beta', 3, 'Chunky-Bukkit-1.4.41-beta.jar'), v('1.4.40', '1.4.40', 'release', 20, 'Chunky-Bukkit-1.4.40.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: 'PtjYWJkn', slug: 'sodium-extra', title: 'Sodium Extra', author: 'FlashyReese',
    description: 'Extra client-side options for Sodium.', downloads: 8_000_000, clientOnly: true, deps: [],
    versions: [v('se0613b', '0.6.13-beta', 'beta', 2, 'sodium-extra-0.6.13-beta.jar'), v('se0612', '0.6.12', 'release', 15, 'sodium-extra-0.6.12.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: 'gvQqBUqZ', slug: 'lithium', title: 'Lithium', author: 'CaffeineMC',
    description: 'Optimizes game physics, mob AI and block ticking on the server.', downloads: 30_000_000, clientOnly: false, deps: [],
    versions: [v('li0151b', '0.15.1-beta', 'beta', 2, 'lithium-fabric-0.15.1-beta.jar'), v('li0150', '0.15.0', 'release', 14, 'lithium-fabric-0.15.0.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: 'P7dR8mSH', slug: 'fabric-api', title: 'Fabric API', author: 'modmuss50',
    description: 'Core library for most Fabric mods.', downloads: 90_000_000, clientOnly: false, deps: [],
    versions: [v('fapi0111b', '0.111.0-beta+1.21.11', 'beta', 1, 'fabric-api-0.111.0-beta.jar'), v('fapi0110', '0.110.0+1.21.11', 'release', 10, 'fabric-api-0.110.0.jar')],
  },
  {
    source: 'modrinth', kind: 'mod', id: '4WWQxlQP', slug: 'servercore', title: 'ServerCore', author: 'Wesley1808',
    description: 'Performance improvements and configuration for Fabric servers.', downloads: 1_200_000, clientOnly: false, deps: ['P7dR8mSH'],
    versions: [v('sc159b', '1.5.9-beta', 'beta', 2, 'servercore-fabric-1.5.9-beta.jar'), v('sc158', '1.5.8', 'release', 18, 'servercore-fabric-1.5.8.jar')],
  },
]

interface Ctx {
  g: Authed
  server: MockServer
  kind: PluginKind
  inv: MockPluginInventory
}

function kindOf(server: MockServer): PluginKind | null {
  switch (server.instance?.type) {
    case 'paper':
    case 'purpur':
      return 'plugin'
    case 'fabric':
    case 'neoforge':
      return 'mod'
    default:
      return null
  }
}

/** Session, server.plugins, server lookup and type check shared by every operation except the icon. */
function access(request: Request, params: Params): Ctx | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes('server.plugins')) return apiError(403, 'forbidden', 'missing permission server.plugins')
  const server = db.servers.find((s) => s.id === id)
  if (!server?.instance) return apiError(404, 'not_found', 'server not found')
  const kind = kindOf(server)
  if (!kind) return apiError(409, 'plugins_unsupported', 'this server type has no plugins or mods')
  db.plugins[id] ??= { kind, dir: kind === 'plugin' ? 'plugins' : 'mods', jars: [], changedAt: 0, checkedAt: 0 }
  jobNow(id) // apply finished steps before anything reads the inventory
  return { g, server, kind, inv: db.plugins[id] }
}

function validation(message: string): ErrorResponse {
  return apiError(400, 'validation_failed', message)
}

/** The instance's game version, LATEST resolved through the mock catalog (null = catalog unavailable). */
function gameVersion(server: MockServer): string | null {
  const inst = server.instance
  if (!inst) return null
  if (inst.version !== 'LATEST') return inst.version
  return db.catalogs[inst.type]?.versions[0]?.id ?? null
}

function catalogUnavailable(): ErrorResponse {
  return apiError(502, 'catalog_unavailable', 'the newest game version cannot be resolved')
}

function sourcesOf(kind: PluginKind): PluginSource[] {
  return kind === 'plugin' ? ['modrinth', 'hangar'] : ['modrinth']
}

/** source query/body value; hangar only for plugins. */
function parseSource(raw: unknown, kind: PluginKind): PluginSource | null {
  if (raw === 'modrinth') return raw
  if (raw === 'hangar' && kind === 'plugin') return raw
  return null
}

function findProject(source: PluginSource, kind: PluginKind, ref: string): MockProject | undefined {
  return CATALOG.find((p) => p.source === source && p.kind === kind && (p.id === ref || p.slug.toLowerCase() === ref.toLowerCase()))
}

function iconCdn(p: MockProject): string {
  return p.source === 'modrinth' ? `https://cdn.modrinth.com/data/${p.id}/icon.png` : `https://hangarcdn.papermc.io/avatars/project/${p.id}.webp`
}

function pageUrl(p: MockProject): string {
  return p.source === 'modrinth' ? `https://modrinth.com/${p.kind}/${p.slug}` : `https://hangar.papermc.io/${p.author}/${p.slug}`
}

function projectRef(serverId: number, p: MockProject): PluginProjectRef {
  return { id: p.id, slug: p.slug, title: p.title, page_url: pageUrl(p), icon_url: pluginIconUrl(serverId, iconCdn(p)) }
}

/** The jar of a project in the inventory (enabled or disabled). */
function installedJar(inv: MockPluginInventory, source: PluginSource, projectId: string): InstalledJar | undefined {
  return inv.jars.find((j) => j.source === source && j.project?.id === projectId)
}

/** Lower-case name without ".disabled": two jars with the same base collide. */
function baseName(file: string): string {
  return file.toLowerCase().replace(/\.disabled$/, '')
}

function jarTitle(jar: InstalledJar): string {
  return jar.project?.title ?? jar.meta?.name ?? jar.file
}

/** Same rules as the panel's file parameter: a plain name ending in .jar or .jar.disabled. */
function validJarFile(file: unknown): file is string {
  if (typeof file !== 'string' || new TextEncoder().encode(file).length > 255 || BAD_FILE_RE.test(file) || !JAR_RE.test(file)) return false
  const lower = file.toLowerCase()
  return !lower.startsWith('.mcpanel-') && !lower.startsWith('.rcon-cli.')
}

function onlyKeys(body: object, keys: string[]): boolean {
  return Object.keys(body).every((k) => keys.includes(k))
}

function newestDownloadable(p: MockProject): MockVersion | undefined {
  return p.versions.find((x) => x.external === null && x.channel === 'release') ?? p.versions.find((x) => x.external === null)
}

function newJar(serverId: number, p: MockProject, ver: MockVersion, enabled: boolean): InstalledJar {
  return {
    file: enabled ? ver.filename : `${ver.filename}.disabled`,
    enabled,
    size: ver.size,
    mtime: Date.now(),
    source: p.source,
    project: projectRef(serverId, p),
    version: { id: ver.id, name: ver.name, channel: ver.channel },
    meta: { kind: p.kind, id: p.kind === 'mod' ? p.slug : '', name: p.title, version: ver.name },
    installed_by_panel: true,
  }
}

function planEntry(p: MockProject, ver: MockVersion, requiredBy: string | null): PluginPlanEntry {
  return {
    source: p.source,
    project_id: p.id,
    slug: p.slug,
    title: p.title,
    version_id: ver.id,
    version_name: ver.name,
    channel: ver.channel,
    filename: ver.filename,
    size: ver.size,
    required_by: requiredBy,
  }
}

interface PlanItem {
  entry: PluginPlanEntry
  project: MockProject
  version: MockVersion
}

/**
 * The plan of resolve and install (spec §6.3): required dependencies first (depth 5, 20 packages, cycles
 * ignored), installed projects as `already`. The requested project itself is `already` when it is installed
 * and no version or exactly the installed version is requested (like plan 03, deviation 4); another version
 * is installed and replaces its old jar (switching versions).
 */
function buildPlan(c: Ctx, source: PluginSource, ref: string, versionId: string | null): { resolution: PluginResolution; items: PlanItem[] } | ErrorResponse {
  const root = findProject(source, c.kind, ref)
  if (!root) return apiError(404, 'not_found', 'project not found')
  if (root.clientOnly) return apiError(409, 'plugin_incompatible', `${root.title} is a client-side mod`)
  let version: MockVersion | undefined
  if (versionId !== null) {
    version = root.versions.find((x) => x.id === versionId)
    if (!version) return apiError(404, 'not_found', 'version not found')
    if (version.external !== null) return apiError(409, 'plugin_not_downloadable', 'this version is only available externally')
  } else {
    version = newestDownloadable(root)
    if (!version) {
      return root.versions.length > 0
        ? apiError(409, 'plugin_not_downloadable', 'every compatible version is only available externally')
        : apiError(409, 'plugin_incompatible', 'no compatible version')
    }
  }
  const resolution: PluginResolution = { install: [], already: [], external: [], incompatible: [] }
  const items: PlanItem[] = []
  const have = installedJar(c.inv, root.source, root.id)
  if (have && (versionId === null || have.version?.id === version.id)) {
    resolution.already.push({ source: root.source, project_id: root.id, title: root.title, file: have.file })
    return { resolution, items }
  }
  const seen = new Set<string>([root.id])
  const visit = (p: MockProject, depth: number) => {
    for (const depId of p.deps) {
      if (seen.has(depId)) continue
      seen.add(depId)
      const dep = CATALOG.find((d) => d.source === p.source && d.id === depId)
      if (!dep) {
        resolution.incompatible.push({ title: depId, reason: 'no_compatible_version' })
        continue
      }
      const have = installedJar(c.inv, dep.source, dep.id)
      if (have) {
        resolution.already.push({ source: dep.source, project_id: dep.id, title: dep.title, file: have.file })
        continue
      }
      if (dep.clientOnly) {
        resolution.incompatible.push({ title: dep.title, reason: 'client_only' })
        continue
      }
      if (depth >= MAX_DEPTH) {
        resolution.incompatible.push({ title: dep.title, reason: 'too_deep' })
        continue
      }
      if (items.length + 1 >= MAX_PACKAGES) {
        resolution.incompatible.push({ title: dep.title, reason: 'too_many' })
        continue
      }
      const depVersion = newestDownloadable(dep)
      if (!depVersion) {
        resolution.incompatible.push({ title: dep.title, reason: dep.versions.length > 0 ? 'not_downloadable' : 'no_compatible_version' })
        continue
      }
      visit(dep, depth + 1) // its own dependencies come first
      items.push({ entry: planEntry(dep, depVersion, p.title), project: dep, version: depVersion })
    }
  }
  visit(root, 1)
  items.push({ entry: planEntry(root, version, null), project: root, version })
  resolution.install = items.map((i) => i.entry)
  return { resolution, items }
}

interface MockUpdate {
  jar: InstalledJar
  project: MockProject
  to: MockVersion
}

/** Updates of Modrinth/Hangar jars: the newest downloadable version in the same or a more stable channel. */
function availableUpdates(inv: MockPluginInventory): MockUpdate[] {
  const out: MockUpdate[] = []
  for (const jar of inv.jars) {
    const { project: ref, version: current } = jar
    if (jar.source === 'manual' || !ref || !current) continue
    const project = CATALOG.find((p) => p.source === jar.source && p.id === ref.id)
    if (!project) continue
    const to = project.versions.find((x) => x.external === null && CHANNEL_RANK[x.channel] <= CHANNEL_RANK[current.channel])
    if (!to || to.id === current.id) continue
    const currentIndex = project.versions.findIndex((x) => x.id === current.id)
    if (currentIndex >= 0 && project.versions.indexOf(to) > currentIndex) continue // only newer versions
    out.push({ jar, project, to })
  }
  return out.sort((a, b) => (a.jar.file < b.jar.file ? -1 : a.jar.file > b.jar.file ? 1 : 0))
}

/** Applies a finished step to the inventory and audits it as the user who started the job. */
function applyChange(serverId: number, actorId: number, change: MockPluginChange | null): void {
  const inv = db.plugins[serverId]
  if (!change || !inv) return
  const addBase = baseName(change.add.file)
  inv.jars = inv.jars.filter((j) => j.file !== change.remove && baseName(j.file) !== addBase)
  inv.jars.push(change.add)
  inv.changedAt = Date.now()
  recordAudit(findUser(actorId) ?? null, change.action, `server:${serverId}`, 'success', change.details)
}

/** Progress of a mock job: every step takes PLUGIN_JOB_MS / steps, pending → downloading → done. */
function jobNow(serverId: number): PluginJob | null {
  const mj = db.pluginJobs[serverId]
  if (!mj) return null
  const { job } = mj
  if (job.state === 'running') {
    const stepMs = PLUGIN_JOB_MS / Math.max(1, job.steps.length)
    const elapsed = Date.now() - mj.startedMs
    job.steps.forEach((step, i) => {
      if (step.state !== 'pending' && step.state !== 'downloading') return
      if (elapsed >= (i + 1) * stepMs) {
        const change = mj.changes[i]
        if (change?.fail) {
          step.state = 'failed'
          step.error_code = change.fail
          step.error = 'mock: the download failed'
          return
        }
        applyChange(serverId, mj.actorId, change)
        step.state = 'done'
      } else if (elapsed >= i * stepMs) {
        step.state = 'downloading'
      }
    })
    if (job.steps.every((s) => s.state !== 'pending' && s.state !== 'downloading')) {
      job.state = job.steps.some((s) => s.state === 'failed') ? 'failed' : 'done'
      job.finished_at = Date.now()
    }
  }
  return structuredClone(job)
}

function runningJobs(): number {
  return Object.keys(db.pluginJobs).filter((id) => jobNow(Number(id))?.state === 'running').length
}

/** 409 conflict while this server runs a job, 429 when four run panel-wide. */
function jobBlocked(c: Ctx): ErrorResponse | null {
  if (jobNow(c.server.id)?.state === 'running') return apiError(409, 'conflict', 'a plugin job of this server is running')
  if (runningJobs() >= MAX_JOBS) return apiError(429, 'rate_limited', 'too many plugin jobs are running, try again shortly')
  return null
}

function startJob(c: Ctx, kind: PluginJob['kind'], steps: PluginJobStep[], changes: Array<MockPluginChange | null>): PluginJob | null {
  const now = Date.now()
  db.pluginJobs[c.server.id] = {
    job: { id: `pjob-${now}`, kind, state: 'running', steps, started_at: now, finished_at: 0 },
    actorId: c.g.user.id,
    startedMs: now,
    changes,
  }
  return jobNow(c.server.id)
}

function step(title: string, file: string): PluginJobStep {
  return { title, file, state: 'pending', error_code: null, error: '' }
}

function inventory(c: Ctx): PluginInventory {
  const jars = [...c.inv.jars].sort((a, b) => (a.file < b.file ? -1 : a.file > b.file ? 1 : 0))
  return {
    kind: c.kind,
    dir: c.inv.dir,
    game_version: gameVersion(c.server),
    sources: sourcesOf(c.kind),
    jars: structuredClone(jars),
    truncated: false,
    restart_needed: c.server.status.state === 'running' && c.inv.changedAt > c.server.status.started_at,
    source_errors: [],
  }
}

function toVersion(x: MockVersion): PluginVersion {
  return {
    id: x.id,
    name: x.name,
    channel: x.channel,
    published_at: Date.now() - x.daysAgo * DAY,
    downloadable: x.external === null,
    external_url: x.external,
  }
}

/** Shared query checks of versions and resolve. */
function projectQuery(c: Ctx, url: URL): { source: PluginSource; project: string } | ErrorResponse {
  const source = parseSource(url.searchParams.get('source'), c.kind)
  if (!source) return validation('source must be modrinth or hangar (hangar only for plugins)')
  const project = url.searchParams.get('project') ?? ''
  if (!NAME_RE.test(project)) return validation('project: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
  return { source, project }
}

/**
 * Mock Modrinth answers of POST /v2/version_files for the mrpack import: a few fixed sha512 values (the e2e fixture and
 * the tests use them). `loaders` defaults to fabric; `fail` lets the import step fail like a broken download.
 */
const mockSha = (byte: number) => byte.toString(16).padStart(2, '0').repeat(64)
export const MRPACK_MOCK_HASHES = {
  lithium: mockSha(0xa1), // lithium 0.15.0
  fabricApiBeta: mockSha(0xa2), // fabric-api 0.111.0-beta (installed on modded: 0.110.0)
  fabricApi: mockSha(0xa3), // fabric-api 0.110.0 (installed on modded)
  servercore: mockSha(0xa4), // servercore 1.5.8
  sodiumExtra: mockSha(0xa5), // client-only project
  servercoreNeoforge: mockSha(0xa6), // a servercore build for neoforge only
  lithiumBroken: mockSha(0xa7), // lithium 0.15.1-beta, whose download fails
} as const
const MRPACK_FILES: Record<string, { project: string; version: string; loaders?: string[]; fail?: PluginStepErrorCode }> = {
  [MRPACK_MOCK_HASHES.lithium]: { project: 'gvQqBUqZ', version: 'li0150' },
  [MRPACK_MOCK_HASHES.fabricApiBeta]: { project: 'P7dR8mSH', version: 'fapi0111b' },
  [MRPACK_MOCK_HASHES.fabricApi]: { project: 'P7dR8mSH', version: 'fapi0110' },
  [MRPACK_MOCK_HASHES.servercore]: { project: '4WWQxlQP', version: 'sc158' },
  [MRPACK_MOCK_HASHES.sodiumExtra]: { project: 'PtjYWJkn', version: 'se0612' },
  [MRPACK_MOCK_HASHES.servercoreNeoforge]: { project: '4WWQxlQP', version: 'sc159b', loaders: ['neoforge'] },
  [MRPACK_MOCK_HASHES.lithiumBroken]: { project: 'gvQqBUqZ', version: 'li0151b', fail: 'download_failed' },
}
const MRPACK_DEP_KEYS = ['minecraft', 'fabric-loader', 'neoforge', 'forge', 'quilt-loader']
const MRPACK_VERSION_RE = /^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$/
const SHA512_RE = /^[0-9a-f]{128}$/i
const MOD_PATH_RE = /^mods\/([^/]+\.jar)$/

interface MockMrpackFile {
  path: string
  sha512: string
  server: MrpackEnvSide | null
}

interface MockMrpackIndex {
  name: string
  versionId: string
  files: MockMrpackFile[]
  deps: Record<string, string>
}

/** The checks of spec §4.1, in short: enough for the UI's error paths. */
function parseMrpackIndex(raw: unknown): MockMrpackIndex | ErrorResponse {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return validation('index: a JSON object is required')
  const i = raw as Record<string, unknown>
  if (i.formatVersion !== 1 || i.game !== 'minecraft') return validation('index: formatVersion 1 and game minecraft are required')
  if (typeof i.name !== 'string' || i.name.length < 1 || i.name.length > 128) return validation('index.name: 1 to 128 characters')
  const versionId = i.versionId ?? ''
  if (typeof versionId !== 'string' || versionId.length > 128) return validation('index.versionId: at most 128 characters')
  if (!Array.isArray(i.files) || i.files.length > 2000) return validation('index.files: at most 2000 entries')
  if (!i.dependencies || typeof i.dependencies !== 'object') return validation('index.dependencies is required')
  const deps: Record<string, string> = {}
  for (const [k, v] of Object.entries(i.dependencies as Record<string, unknown>)) {
    if (!MRPACK_DEP_KEYS.includes(k) || typeof v !== 'string' || !MRPACK_VERSION_RE.test(v)) return validation(`index.dependencies.${k}: unknown key or invalid version`)
    deps[k] = v
  }
  if (!deps.minecraft) return validation('index.dependencies.minecraft is required')
  const files: MockMrpackFile[] = []
  const hashes = new Set<string>()
  const names = new Set<string>()
  for (const f of i.files as unknown[]) {
    const file = f as { path?: unknown; hashes?: { sha512?: unknown }; env?: { server?: unknown } }
    const path = file?.path
    const sha = file?.hashes?.sha512
    if (typeof path !== 'string' || path === '' || path.length > 255 || path.startsWith('/') || path.includes('\\') || path.split('/').includes('..')) {
      return validation('index.files: invalid path')
    }
    if (typeof sha !== 'string' || !SHA512_RE.test(sha)) return validation(`index.files: ${path} has no valid sha512`)
    const lower = sha.toLowerCase()
    if (hashes.has(lower) || names.has(path.toLowerCase())) return validation(`index.files: ${path} is listed twice`)
    hashes.add(lower)
    names.add(path.toLowerCase())
    const env = file.env?.server
    files.push({ path, sha512: lower, server: env === 'required' || env === 'optional' || env === 'unsupported' ? env : null })
  }
  return { name: i.name, versionId, files, deps }
}

interface MrpackItem {
  entry: MrpackPlanEntry
  project: MockProject
  version: MockVersion
  fail?: PluginStepErrorCode
}

/** Spec §3, step by step, against the mock catalog and inventory. */
function mrpackPlan(c: Ctx, index: MockMrpackIndex): { plan: MrpackPlan; items: MrpackItem[] } | ErrorResponse {
  const type = c.server.instance?.type
  const loaderKey = type === 'neoforge' ? 'neoforge' : 'fabric-loader'
  const otherLoaders = MRPACK_DEP_KEYS.filter((k) => k !== 'minecraft' && k !== loaderKey)
  if (!index.deps[loaderKey] || otherLoaders.some((k) => index.deps[k])) return apiError(409, 'mrpack_incompatible', 'the modpack is for another mod loader')
  const server = gameVersion(c.server)
  if (server === null) return catalogUnavailable()
  const loaderServer = c.server.instance?.loader_version ?? null
  const loaderPack = index.deps[loaderKey]
  const entries: MrpackPlanEntry[] = []
  const items: MrpackItem[] = []
  const matched = new Set<string>()
  let ignored = 0
  for (const f of index.files) {
    const m = MOD_PATH_RE.exec(f.path)
    if (!m) {
      ignored++
      continue
    }
    const file = m[1]
    const base: MrpackPlanEntry = { file, sha512: f.sha512, action: 'skip', reason: null, optional: f.server === 'optional', project: null, version: null, replaces: null }
    const known = MRPACK_FILES[f.sha512]
    const project = known ? CATALOG.find((p) => p.source === 'modrinth' && p.id === known.project) : undefined
    const version = project?.versions.find((x) => x.id === known?.version)
    if (f.server === 'unsupported' || project?.clientOnly) {
      entries.push({ ...base, reason: 'client_only', project: project ? projectRef(c.server.id, project) : null })
      continue
    }
    if (!known || !project || !version) {
      entries.push({ ...base, reason: 'not_on_modrinth' })
      continue
    }
    const ref = projectRef(c.server.id, project)
    const verRef = { id: version.id, name: version.name, channel: version.channel }
    if (!(known.loaders ?? ['fabric']).includes(type ?? '')) {
      entries.push({ ...base, reason: 'incompatible', project: ref, version: verRef })
      continue
    }
    const have = installedJar(c.inv, 'modrinth', project.id)
    if (have && have.version?.id === version.id) {
      matched.add(have.file)
      entries.push({ ...base, file: have.file, action: 'unchanged', project: ref, version: verRef })
      continue
    }
    if (have) {
      matched.add(have.file)
      const target = have.enabled ? file : `${file}.disabled`
      const entry: MrpackPlanEntry = { ...base, file: target, action: 'update', project: ref, version: verRef, replaces: have.file }
      entries.push(entry)
      items.push({ entry, project, version, fail: known.fail })
      continue
    }
    if (c.inv.jars.some((j) => baseName(j.file) === baseName(file))) {
      entries.push({ ...base, reason: 'file_exists', project: ref, version: verRef })
      continue
    }
    const entry: MrpackPlanEntry = { ...base, action: 'install', project: ref, version: verRef }
    entries.push(entry)
    items.push({ entry, project, version, fail: known.fail })
  }
  const minecraftDiffers = index.deps.minecraft !== server
  const loaderDiffers = loaderServer !== null && loaderServer !== loaderPack
  return {
    plan: {
      pack: { name: index.name, version_id: index.versionId },
      game_version: { pack: index.deps.minecraft, server },
      loader: { name: type === 'neoforge' ? 'neoforge' : 'fabric', pack: loaderPack, server: loaderServer },
      version_mismatch: minecraftDiffers || loaderDiffers,
      entries,
      kept: c.inv.jars.filter((j) => !matched.has(j.file)).map((j) => j.file).sort(),
      ignored_files: ignored,
    },
    items,
  }
}

export const pluginsOperations: MockOperation[] = [
  op(
    'listPlugins',
    http.get(`${API}/servers/:id/plugins`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      return HttpResponse.json(inventory(c))
    }),
  ),
  op(
    'searchPlugins',
    http.get(`${API}/servers/:id/plugins/search`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const url = new URL(request.url)
      const source = parseSource(url.searchParams.get('source'), c.kind)
      if (!source) return validation('source must be modrinth or hangar (hangar only for plugins)')
      const q = url.searchParams.get('q') ?? ''
      if (q.length > MAX_QUERY) return validation(`q: at most ${MAX_QUERY} characters`)
      const rawOffset = url.searchParams.get('offset') ?? '0'
      const offset = Number(rawOffset)
      if (!/^\d+$/.test(rawOffset) || offset > MAX_OFFSET) return validation(`offset: 0 to ${MAX_OFFSET}`)
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const needle = q.trim().toLowerCase()
      const matches = CATALOG.filter(
        (p) => p.source === source && p.kind === c.kind && !p.clientOnly && (needle === '' || [p.title, p.slug, p.description].some((s) => s.toLowerCase().includes(needle))),
      ).sort((a, b) => b.downloads - a.downloads)
      const hits: PluginSearchHit[] = matches.slice(offset, offset + PAGE).map((p) => ({
        source: p.source,
        project_id: p.id,
        slug: p.slug,
        title: p.title,
        description: p.description,
        author: p.author,
        downloads: p.downloads,
        icon_url: pluginIconUrl(c.server.id, iconCdn(p)),
        page_url: pageUrl(p),
        installed: installedJar(c.inv, p.source, p.id) !== undefined,
      }))
      return HttpResponse.json({ hits, total: matches.length, offset })
    }),
  ),
  op(
    'listPluginVersions',
    http.get(`${API}/servers/:id/plugins/versions`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const q = projectQuery(c, new URL(request.url))
      if (q instanceof Response) return q
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const p = findProject(q.source, c.kind, q.project)
      if (!p) return apiError(404, 'not_found', 'project not found')
      return HttpResponse.json({ versions: p.clientOnly ? [] : p.versions.slice(0, MAX_VERSIONS).map(toVersion) })
    }),
  ),
  op(
    'resolvePlugin',
    http.get(`${API}/servers/:id/plugins/resolve`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const url = new URL(request.url)
      const q = projectQuery(c, url)
      if (q instanceof Response) return q
      const version = url.searchParams.get('version')
      if (version !== null && !NAME_RE.test(version)) return validation('version: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const plan = buildPlan(c, q.source, q.project, version)
      if (plan instanceof Response) return plan
      return HttpResponse.json(plan.resolution)
    }),
  ),
  op(
    'installPlugin',
    http.post(`${API}/servers/:id/plugins/install`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<InstallPluginRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['source', 'project', 'version'])) return validation('invalid body')
      const source = parseSource(body.source, c.kind)
      if (!source) return validation('source must be modrinth or hangar (hangar only for plugins)')
      if (typeof body.project !== 'string' || !NAME_RE.test(body.project)) return validation('project: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
      const version = body.version ?? null
      if (version !== null && (typeof version !== 'string' || !NAME_RE.test(version))) return validation('version: 1 to 128 characters of A-Z a-z 0-9 . _ + -')
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const blocked = jobBlocked(c)
      if (blocked) return blocked
      const plan = buildPlan(c, source, body.project, version)
      if (plan instanceof Response) return plan
      if (plan.resolution.incompatible.length > 0) return apiError(409, 'plugin_incompatible', 'a required dependency has no compatible version')
      if (plan.items.length === 0) return apiError(409, 'conflict', 'the project is already installed')
      const changes: MockPluginChange[] = []
      for (const item of plan.items) {
        const old = installedJar(c.inv, item.project.source, item.project.id)
        const add = newJar(c.server.id, item.project, item.version, old ? old.enabled : true)
        const taken = c.inv.jars.find((j) => j !== old && baseName(j.file) === baseName(add.file))
        if (taken) return apiError(409, 'file_exists', `${add.file} belongs to another jar`)
        changes.push({
          remove: old?.file ?? null,
          add,
          action: 'plugin.install',
          details: { source: item.project.source, project: item.project.id, version: item.version.id, file: add.file },
        })
      }
      const steps = changes.map((ch, i) => step(plan.items[i].project.title, ch.add.file))
      return HttpResponse.json(startJob(c, 'install', steps, changes), { status: 202 })
    }),
  ),
  op(
    'getPluginUpdates',
    http.get(`${API}/servers/:id/plugins/updates`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const refresh = new URL(request.url).searchParams.get('refresh')
      if (refresh === 'true' || refresh === '1' || c.inv.checkedAt === 0) c.inv.checkedAt = Date.now()
      const updates: PluginUpdate[] = availableUpdates(c.inv).map((u) => ({
        file: u.jar.file,
        from: { id: u.jar.version?.id ?? '', name: u.jar.version?.name ?? '', channel: u.jar.version?.channel ?? 'release' },
        to: { id: u.to.id, name: u.to.name, channel: u.to.channel },
      }))
      return HttpResponse.json({ updates, checked_at: c.inv.checkedAt })
    }),
  ),
  op(
    'updatePlugins',
    http.post(`${API}/servers/:id/plugins/update`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<UpdatePluginsRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['files']) || !Array.isArray(body.files) || body.files.length > 500 || !body.files.every(validJarFile)) {
        return validation('files: up to 500 jar file names')
      }
      if (gameVersion(c.server) === null) return catalogUnavailable()
      const blocked = jobBlocked(c)
      if (blocked) return blocked
      const all = availableUpdates(c.inv)
      const missing = body.files.find((f) => !all.some((u) => u.jar.file === f))
      if (missing !== undefined) return validation(`no update available for ${missing}`)
      const targets = body.files.length > 0 ? body.files : all.map((u) => u.jar.file)
      if (targets.length === 0) return validation('no updates available')
      const steps: PluginJobStep[] = []
      const changes: Array<MockPluginChange | null> = []
      for (const file of targets) {
        const jar = c.inv.jars.find((j) => j.file === file)
        const u = all.find((x) => x.jar.file === file)
        if (!jar || !u) {
          steps.push({ ...step(jar ? jarTitle(jar) : file, file), state: 'skipped' })
          changes.push(null)
          continue
        }
        const add = newJar(c.server.id, u.project, u.to, jar.enabled)
        if (c.inv.jars.some((j) => j !== jar && baseName(j.file) === baseName(add.file))) {
          steps.push({ ...step(u.project.title, add.file), state: 'failed', error_code: 'file_exists', error: `${add.file} belongs to another jar` })
          changes.push(null)
          continue
        }
        steps.push(step(u.project.title, add.file))
        changes.push({ remove: jar.file, add, action: 'plugin.update', details: { file: add.file, from: jar.version?.name ?? '', to: u.to.name } })
      }
      return HttpResponse.json(startJob(c, 'update', steps, changes), { status: 202 })
    }),
  ),
  op(
    'togglePlugin',
    http.post(`${API}/servers/:id/plugins/toggle`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<TogglePluginRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['file', 'enabled']) || !validJarFile(body.file) || typeof body.enabled !== 'boolean') {
        return validation('file must be a jar name, enabled a boolean')
      }
      const jar = c.inv.jars.find((j) => j.file === body.file)
      if (!jar) return apiError(404, 'not_found', 'jar not found')
      if (jobNow(c.server.id)?.state === 'running') return apiError(409, 'conflict', 'a plugin job of this server is running')
      if (jar.enabled === body.enabled) return noContent()
      const target = body.enabled ? jar.file.replace(/\.disabled$/i, '') : `${jar.file}.disabled`
      if (c.inv.jars.some((j) => j !== jar && j.file.toLowerCase() === target.toLowerCase())) return apiError(409, 'file_exists', `${target} exists`)
      jar.file = target
      jar.enabled = body.enabled
      c.inv.changedAt = Date.now()
      recordAudit(c.g.user, 'plugin.toggle', `server:${c.server.id}`, 'success', { file: body.file, enabled: body.enabled })
      return noContent()
    }),
  ),
  op(
    'deletePlugin',
    http.post(`${API}/servers/:id/plugins/delete`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      const body = await readJson<PluginFileRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['file']) || !validJarFile(body.file)) return validation('file must be a jar name')
      const jar = c.inv.jars.find((j) => j.file === body.file)
      if (!jar) return apiError(404, 'not_found', 'jar not found')
      if (jobNow(c.server.id)?.state === 'running') return apiError(409, 'conflict', 'a plugin job of this server is running')
      c.inv.jars = c.inv.jars.filter((j) => j !== jar)
      c.inv.changedAt = Date.now()
      recordAudit(c.g.user, 'plugin.remove', `server:${c.server.id}`, 'success', { file: body.file })
      return noContent()
    }),
  ),
  op(
    'getPluginJob',
    http.get(`${API}/servers/:id/plugins/jobs`, ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      return HttpResponse.json({ job: jobNow(c.server.id) })
    }),
  ),
  op(
    'previewMrpack',
    http.post(`${API}/servers/:id/plugins/mrpack/preview`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      if (c.kind !== 'mod') return apiError(409, 'plugins_unsupported', 'only fabric and neoforge servers import modpacks')
      const index = parseMrpackIndex(await readJson<unknown>(request))
      if (index instanceof Response) return index
      const plan = mrpackPlan(c, index)
      if (plan instanceof Response) return plan
      return HttpResponse.json(plan.plan)
    }),
  ),
  op(
    'importMrpack',
    http.post(`${API}/servers/:id/plugins/mrpack/import`, async ({ request, params }) => {
      const c = access(request, params)
      if (c instanceof Response) return c
      if (c.kind !== 'mod') return apiError(409, 'plugins_unsupported', 'only fabric and neoforge servers import modpacks')
      const body = await readJson<MrpackImportRequest>(request)
      if (!body || typeof body !== 'object' || !onlyKeys(body, ['index', 'exclude', 'accept_mismatch'])) return validation('invalid body')
      if (!Array.isArray(body.exclude) || typeof body.accept_mismatch !== 'boolean') return validation('exclude and accept_mismatch are required')
      const index = parseMrpackIndex(body.index)
      if (index instanceof Response) return index
      const result = mrpackPlan(c, index)
      if (result instanceof Response) return result
      const { plan, items } = result
      const optional = new Set(plan.entries.filter((e) => e.optional && (e.action === 'install' || e.action === 'update')).map((e) => e.sha512))
      const exclude = new Set<string>()
      for (const h of body.exclude) {
        const sha = typeof h === 'string' ? h.toLowerCase() : ''
        if (!optional.has(sha)) return validation('exclude names a hash that is not an optional entry of the plan')
        exclude.add(sha)
      }
      if (plan.version_mismatch && !body.accept_mismatch) return apiError(409, 'mrpack_version_mismatch', 'the modpack is for another Minecraft or loader version')
      const blocked = jobBlocked(c)
      if (blocked) return blocked
      const chosen = items.filter((i) => !exclude.has(i.entry.sha512))
      if (chosen.length === 0) return apiError(409, 'conflict', 'the modpack has nothing to install or update')
      const changes: MockPluginChange[] = chosen.map((i) => {
        const old = i.entry.replaces ? c.inv.jars.find((j) => j.file === i.entry.replaces) : undefined
        const add = newJar(c.server.id, i.project, i.version, old ? old.enabled : true)
        return {
          remove: old?.file ?? null,
          add,
          action: i.entry.action === 'update' ? 'plugin.update' : 'plugin.install',
          details: { source: 'modrinth', project: i.project.id, version: i.version.id, file: add.file, via: 'mrpack' },
          fail: i.fail,
        }
      })
      const steps = changes.map((ch, i) => step(chosen[i].project.title, ch.add.file))
      recordAudit(c.g.user, 'plugin.mrpack_import', `server:${c.server.id}`, 'success', { pack: plan.pack.name, version: plan.pack.version_id })
      return HttpResponse.json(startJob(c, 'mrpack', steps, changes), { status: 202 })
    }),
  ),
  op(
    'getPluginIcon',
    http.get(`${API}/servers/:id/plugins/icon`, ({ request, params }) => {
      // Like the panel: permission and server, but no plugins_unsupported check.
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.plugins')) return apiError(403, 'forbidden', 'missing permission server.plugins')
      if (!db.servers.find((s) => s.id === id)?.instance) return apiError(404, 'not_found', 'server not found')
      const raw = new URL(request.url).searchParams.get('url') ?? ''
      if (raw.length > 512 || !ICON_PREFIXES.some((p) => raw.startsWith(p))) return validation('url must point to cdn.modrinth.com or hangarcdn.papermc.io')
      return new HttpResponse(ICON_PNG.slice(), {
        headers: { 'Content-Type': 'image/png', 'Cache-Control': 'private, max-age=86400', 'X-Content-Type-Options': 'nosniff' },
      })
    }),
  ),
]
