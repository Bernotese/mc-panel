import { http, HttpResponse } from 'msw'
import type {
  Catalog,
  CreateServerRequest,
  InstanceType,
  Permission,
  PowerAction,
  Recovery,
  Server,
  ServerDeletion,
  ServerMemoryLimits,
  ServerSettings,
  UpdateServerRequest,
  UpdateServerSettingsRequest,
} from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { javaFor } from '@/features/servers/java'
import { db, emptyPlayers, nextId, recordAudit, resolveProperties, runningStatus, stoppedStatus, type MockServer } from '../db'
import { restoreActive } from './backups'
import { DEV_PREFIX, devOf, devSummary, settleDev } from './dev-model'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type MockOperation } from './util'

const SERVER_NAME = /^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$/
const ACTIONS: PowerAction[] = ['start', 'stop', 'restart']
const TYPES: InstanceType[] = ['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge']
const DNS_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
const CURSEFORGE_KEY = /^[A-Za-z0-9$./_-]{1,128}$/

/** Same rule as instancespec.ValidBaseDomain (after lowercasing). */
function validBaseDomain(d: string): boolean {
  const labels = d.split('.')
  return d.length <= 200 && labels.length >= 2 && labels.every((l) => DNS_LABEL.test(l))
}

/** Installed memory of the mock host (26 GiB): the per-server limit is (26 − 1) / 1.25 = 20 GiB. */
const MOCK_HOST_MIB = 26624
/** Same budget model as internal/servers/memory.go: containers get 1.25 × heap, 1 GiB stays free. */
const MEMORY_RESERVE_MIB = 1024
const CONTAINER_OVERHEAD_PERCENT = 25

function containerMiB(heapMiB: number): number {
  return (heapMiB * (100 + CONTAINER_OVERHEAD_PERCENT)) / 100
}

/** Largest heap in 256 MiB steps whose container fits into containerMiB. */
function heapForContainerMiB(container: number): number {
  const m = Math.floor((container * 100) / (100 + CONTAINER_OVERHEAD_PERCENT))
  return m - (m % 256)
}

/** Same formula as servers.MaxMemoryMiB: (host − 1 GiB) / 1.25 in 256 MiB steps, within 1..32 GiB. */
function maxMemoryMiB(hostMiB: number): number {
  return Math.min(Math.max(heapForContainerMiB(hostMiB - MEMORY_RESERVE_MIB), 1024), 32768)
}

/** Same rule as servers.RecommendedMemoryMiB; null = not even the minimum fits the free budget. */
function recommendedMemoryMiB(type: InstanceType, hostMiB: number, allocatedContainerMiB: number): number | null {
  const base = type === 'fabric' || type === 'neoforge' || type === 'modrinth' || type === 'curseforge' ? 6144 : 4096
  const free = Math.max(hostMiB - MEMORY_RESERVE_MIB - allocatedContainerMiB, 0)
  let rec = Math.min(base, heapForContainerMiB(Math.floor(free / 2)))
  if (rec < 1024) {
    if (heapForContainerMiB(free) < 1024) return null
    rec = 1024
  }
  return Math.min(rec, maxMemoryMiB(hostMiB))
}

export function memoryLimits(): ServerMemoryLimits {
  const heaps = db.servers.map((s) => s.instance?.memory_mib ?? 0)
  const allocated = heaps.reduce((sum, m) => sum + m, 0)
  const allocatedContainer = heaps.reduce((sum, m) => sum + containerMiB(m), 0)
  const recommended = Object.fromEntries(TYPES.map((t) => [t, recommendedMemoryMiB(t, MOCK_HOST_MIB, allocatedContainer)])) as ServerMemoryLimits['recommended_mib']
  return {
    min_mib: 1024,
    max_mib: maxMemoryMiB(MOCK_HOST_MIB),
    step_mib: 256,
    host_total_mib: MOCK_HOST_MIB,
    host_reserve_mib: MEMORY_RESERVE_MIB,
    container_overhead_percent: CONTAINER_OVERHEAD_PERCENT,
    container_budget_mib: MOCK_HOST_MIB - MEMORY_RESERVE_MIB,
    allocated_mib: allocated,
    allocated_container_mib: allocatedContainer,
    recommended_mib: recommended,
  }
}

export function validMemory(m: unknown): m is number {
  return typeof m === 'number' && Number.isInteger(m) && m >= 1024 && m <= maxMemoryMiB(MOCK_HOST_MIB) && m % 256 === 0
}

export const MEMORY_MESSAGE = `memory_mib: must be 1024..${maxMemoryMiB(MOCK_HOST_MIB)} in steps of 256 on this host`

/** Like filesapi.ValidTimezone: an exact IANA name the browser lists (or UTC), never an offset. */
function validTimezone(tz: unknown): tz is string {
  if (typeof tz !== 'string' || tz.length > 64) return false
  return tz === 'UTC' || Intl.supportedValuesOf('timeZone').includes(tz)
}

function settingsBody(): ServerSettings {
  return { ...db.serverSettings, unknown_containers: [] }
}

/** A new container comes up between two reads: "creating" becomes "running" on the next GET; a dev's sync ends. */
function settle(s: MockServer): void {
  settleDev(s)
  if (s.status.state === 'creating' && s.instance) {
    s.status = runningStatus(s.instance.memory_mib, Date.now())
    s.runningRevision = s.revision
  }
}

/** Same rule as agentapi.ValidWebPort. */
function validWebPort(p: unknown): p is number {
  return typeof p === 'number' && Number.isInteger(p) && p >= 1024 && p <= 65535 && p !== 25565 && p !== 25575
}

/** Same rules as agentapi.ValidWebPath and MaxWebTargets. */
const WEB_PATH = /^([a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?)?$/
const MAX_WEB_ROUTES = 8

type WebRoute = { path: string; port: number }

/**
 * Like the panel: sorts by path first (clients need not send the list in order), then applies the rules of
 * agentapi.CheckWebTargets; the message names the first broken route by its index in the sorted list. Returns the
 * sorted list or the error response.
 */
function checkWebRoutes(raw: unknown): WebRoute[] | Response {
  if (raw === null) return apiError(400, 'validation_failed', 'web_routes: must not be null')
  // Like the panel's strict JSON decoding: an unknown field (e.g. url) or a wrong type is a bad request.
  const shapeOk =
    Array.isArray(raw) &&
    raw.every(
      (r: unknown) =>
        typeof r === 'object' &&
        r !== null &&
        Object.keys(r).every((k) => k === 'path' || k === 'port') &&
        typeof (r as Partial<WebRoute>).path === 'string' &&
        typeof (r as Partial<WebRoute>).port === 'number',
    )
  if (!shapeOk) return apiError(400, 'bad_request', 'invalid JSON body')
  const routes = (raw as WebRoute[]).map((r) => ({ path: r.path, port: r.port }))
  if (routes.length > MAX_WEB_ROUTES) return apiError(400, 'validation_failed', `web_routes: at most ${MAX_WEB_ROUTES} routes`)
  const invalid = (msg: string) => apiError(400, 'validation_failed', msg)
  const sorted = routes.sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
  for (const [i, r] of sorted.entries()) {
    if (!WEB_PATH.test(r.path)) return invalid(`web_routes: targets[${i}].path: must be "" or 1-32 characters of [a-z0-9-], first and last a letter or digit`)
    if (!validWebPort(r.port)) return invalid(`web_routes: targets[${i}].port: must be 1024..65535 and not 25565 or 25575`)
    if (i > 0 && sorted[i - 1].path === r.path) return invalid(`web_routes: targets[${i}].path: duplicate path`)
    if (sorted.findIndex((o) => o.port === r.port) !== i) return invalid(`web_routes: targets[${i}].port: duplicate port`)
  }
  return sorted
}

/** The reconcile applies a changed route list within seconds: "pending" becomes "active" on the next GET. */
function settleWeb(s: MockServer): void {
  if (s.webRoutes?.length && s.webState === 'pending' && db.serverSettings.base_domain) s.webState = 'active'
}

/**
 * Like the panel: off without routes or base domain; web_url only with a root route; web_routes (always, [] when
 * none, url only with a base domain) and web_error only for admins.
 */
function webFields(s: MockServer, admin: boolean): Pick<Server, 'web_status' | 'web_url' | 'web_routes' | 'web_error'> {
  const base = db.serverSettings.base_domain
  const routes = s.webRoutes ?? []
  const fqdn = base ? `${s.name}.${base}` : null
  const listed = admin ? { web_routes: routes.map((r) => (fqdn ? { ...r, url: r.path ? `https://${fqdn}/${r.path}/` : `https://${fqdn}/` } : { ...r })) } : {}
  if (routes.length === 0 || !fqdn) return { web_status: 'off', ...listed }
  const status = s.webState ?? 'pending'
  return {
    web_status: status,
    ...(routes.some((r) => r.path === '') ? { web_url: `https://${fqdn}` } : {}),
    ...listed,
    ...(admin && status === 'error' && s.webError ? { web_error: s.webError } : {}),
  }
}

/** New desired revision: a running server needs a restart, a stopped one is recreated at once. */
function bumpRevision(s: MockServer): void {
  s.revision += 1
  if (s.status.state === 'running') s.status.restart_required = true
  else s.runningRevision = s.revision
}

/**
 * Like the panel: only admins get the raw last_error, other users see the state only; the seed
 * only goes to admins and users with server.files.
 */
export function toServer(s: MockServer, permissions: Permission[], admin = true): Server {
  const base = db.serverSettings.base_domain
  let instance = s.instance
  if (instance && !admin && !permissions.includes('server.files')) {
    const properties = { ...instance.properties }
    delete properties.seed
    instance = { ...instance, properties }
  }
  return {
    id: s.id,
    name: s.name,
    permissions,
    address: s.instance && base ? `${s.name}.${base}` : null,
    instance: instance ? { ...instance, revision: s.revision } : null,
    status: admin ? s.status : { ...s.status, last_error: '' },
    down_alerts: admin ? (db.monitoring.downAlerts[s.id] ?? true) : null,
    ...webFields(s, admin),
    ...(s.dev ? { dev: devSummary(s.dev) } : {}),
    ...withDevId(s.id),
    data_state: s.dataState ?? 'safe',
    data_job: s.dataState === 'busy' ? (s.dataJob ?? null) : null,
  }
}

function withDevId(id: number): Pick<Server, 'dev_id'> {
  const dev = devOf(id)
  return dev ? { dev_id: dev.id } : {}
}

export const serverOperations: MockOperation[] = [
  op(
    'listServers',
    http.get(`${API}/servers`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const visible = db.servers
        .map((s) => {
          settle(s)
          settleWeb(s)
          return toServer(s, serverPermissions(g.user, s.id), g.user.role === 'admin')
        })
        .filter((s) => s.permissions.length > 0)
      return HttpResponse.json<Server[]>(visible)
    }),
  ),

  op(
    'createServer',
    http.post(`${API}/servers`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<Partial<CreateServerRequest>>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      if (!db.serverSettings.base_domain) return apiError(409, 'base_domain_missing', 'set a base domain in the server settings first')
      const name = body.name ?? ''
      if (!SERVER_NAME.test(name)) return apiError(400, 'validation_failed', 'name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$')
      if (name.startsWith(DEV_PREFIX)) return apiError(400, 'validation_failed', 'names starting with dev- are reserved for dev instances')
      if (body.eula_accepted !== true) return apiError(400, 'validation_failed', 'eula_accepted must be true')
      if (!body.type || !TYPES.includes(body.type)) return apiError(400, 'validation_failed', 'type: unknown type')
      if (!validMemory(body.memory_mib)) return apiError(400, 'validation_failed', MEMORY_MESSAGE)
      if (db.servers.some((s) => s.name === name)) return apiError(409, 'conflict', 'server name already taken')
      const version = body.version ?? ''
      const server: MockServer = {
        id: nextId(),
        name,
        instance: {
          type: body.type,
          version,
          loader_version: body.loader_version ?? null,
          modpack: body.modpack ?? null,
          java: body.java ?? javaFor(body.type, version),
          memory_mib: body.memory_mib,
          properties: resolveProperties(name, body.properties),
        },
        status: { ...runningStatus(body.memory_mib, 0), state: 'creating', operation: 'pulling', memory_bytes: 0, memory_limit_bytes: 0, cpu_percent: 0 },
        revision: 1,
        runningRevision: 1,
        players: emptyPlayers(),
      }
      db.servers.push(server)
      recordAudit(g.user, 'server.create', `server:${server.id}`, 'success', { name, type: body.type, version })
      return HttpResponse.json<Server>(toServer(server, [...ALL_PERMISSIONS]), { status: 201 })
    }),
  ),

  op(
    'getServerCatalog',
    http.get(`${API}/servers/catalog/:type`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const type = params.type as InstanceType
      if (!TYPES.includes(type) || type === 'modrinth' || type === 'curseforge') {
        return apiError(404, 'not_found', 'no version catalog for this type')
      }
      const catalog = db.catalogs[type]
      if (!catalog) return apiError(502, 'catalog_unavailable', 'version catalog unavailable, enter the version manually')
      return HttpResponse.json<Catalog>(catalog)
    }),
  ),

  // Before getServer: MSW matches in order and /servers/:id would also match /servers/memory.
  op(
    'getServerMemoryLimits',
    http.get(`${API}/servers/memory`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<ServerMemoryLimits>(memoryLimits())
    }),
  ),

  // Before getServer: MSW matches in order and /servers/:id would also match /servers/settings.
  op(
    'getServerSettings',
    http.get(`${API}/servers/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<ServerSettings>(settingsBody())
    }),
  ),

  op(
    'updateServerSettings',
    http.put(`${API}/servers/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<UpdateServerSettingsRequest>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      const details: Record<string, unknown> = {}
      if (body.base_domain !== undefined) {
        const next = body.base_domain === null ? null : body.base_domain.toLowerCase()
        if (next !== null && !validBaseDomain(next)) return apiError(400, 'validation_failed', 'base_domain: invalid DNS name')
        if (next !== db.serverSettings.base_domain) {
          details.base_domain = { old: db.serverSettings.base_domain, new: next }
          db.serverSettings.base_domain = next
          for (const s of db.servers) {
            if (s.instance) bumpRevision(s)
            // the route list changes with the base domain: pending until the agent confirms
            if (s.webRoutes?.length) s.webState = 'pending'
          }
        }
      }
      if (body.curseforge_api_key !== undefined) {
        if (body.curseforge_api_key !== null && !CURSEFORGE_KEY.test(body.curseforge_api_key)) {
          return apiError(400, 'validation_failed', 'curseforge_api_key must match ^[A-Za-z0-9$./_-]{1,128}$')
        }
        db.serverSettings.curseforge_api_key_set = body.curseforge_api_key !== null
        details.curseforge_api_key = body.curseforge_api_key === null ? 'removed' : 'set'
      }
      if (body.backup_timezone !== undefined) {
        if (!validTimezone(body.backup_timezone)) return apiError(400, 'validation_failed', 'backup_timezone must be an IANA time zone such as Europe/Berlin')
        if (body.backup_timezone !== db.serverSettings.backup_timezone) {
          details.backup_timezone = { old: db.serverSettings.backup_timezone, new: body.backup_timezone }
          db.serverSettings.backup_timezone = body.backup_timezone
        }
      }
      if (Object.keys(details).length > 0) recordAudit(g.user, 'server.settings_update', 'servers', 'success', details)
      return HttpResponse.json<ServerSettings>(settingsBody())
    }),
  ),

  op(
    'getServer',
    http.get(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      const permissions = serverPermissions(g.user, id)
      if (!permissions.includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      settle(server)
      settleWeb(server)
      return HttpResponse.json<Server>(toServer(server, permissions, g.user.role === 'admin'))
    }),
  ),

  op(
    'updateServer',
    http.patch(`${API}/servers/:id`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const server = db.servers.find((s) => s.id === idParam(params))
      if (!server) return apiError(404, 'not_found', 'server not found')
      if (!server.instance) return apiError(409, 'conflict', 'server is not configured (no instance)')
      const body = await readJson<UpdateServerRequest>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      // Like the panel: a stale draft is refused as a whole before anything else is checked.
      if (body.expected_revision != null && body.expected_revision !== server.revision) {
        return apiError(409, 'server_modified', `the server was changed meanwhile (revision ${server.revision}, expected ${body.expected_revision}): reload it and apply the changes again`)
      }
      // A dev takes everything but its memory from its source at every sync.
      if (server.dev && Object.keys(body).some((k) => k !== 'memory_mib' && k !== 'expected_revision')) {
        return apiError(409, 'dev_managed', 'a dev instance only accepts memory_mib; everything else follows its source')
      }
      if (body.memory_mib !== undefined && !validMemory(body.memory_mib)) {
        return apiError(400, 'validation_failed', MEMORY_MESSAGE)
      }
      let webRoutes: WebRoute[] | undefined
      if (body.web_routes !== undefined) {
        const checked = checkWebRoutes(body.web_routes)
        if (checked instanceof Response) return checked
        if (checked.length > 0 && !db.serverSettings.base_domain) return apiError(409, 'conflict', 'base domain required')
        webRoutes = checked
      }
      const before = JSON.stringify(server.instance)
      const next = { ...server.instance }
      if (body.version !== undefined) next.version = body.version
      if (body.loader_version !== undefined) next.loader_version = body.loader_version
      if (body.modpack !== undefined) next.modpack = body.modpack
      if (body.java !== undefined) next.java = body.java
      if (body.memory_mib !== undefined) next.memory_mib = body.memory_mib
      if (body.properties !== undefined) next.properties = resolveProperties(server.name, body.properties)
      if (JSON.stringify(next) !== before) {
        server.instance = next
        bumpRevision(server)
        recordAudit(g.user, 'server.update', `server:${server.id}`, 'success', { name: server.name, revision: server.revision })
      }
      // Not part of the instance spec: no revision bump, an audit entry of its own.
      if (webRoutes && JSON.stringify(webRoutes) !== JSON.stringify(server.webRoutes ?? [])) {
        recordAudit(g.user, 'server.web_routes_update', `server:${server.id}`, 'success', { name: server.name, old: server.webRoutes ?? [], new: webRoutes })
        server.webRoutes = webRoutes
        server.webState = 'pending'
        delete server.webError
      }
      return HttpResponse.json<Server>(toServer(server, [...ALL_PERMISSIONS]))
    }),
  ),

  op(
    'deleteServer',
    http.delete(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const raw = new URL(request.url).searchParams.get('purge_backups')
      // like the panel: absent or empty = no
      if (raw !== null && !['', '1', 'true', '0', 'false'].includes(raw)) return apiError(400, 'validation_failed', 'purge_backups must be true, false, 1 or 0')
      const purge = raw === '1' || raw === 'true'
      const id = idParam(params)
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      if (restoreActive(id)) return apiError(409, 'conflict', 'a file or backup job is running for this server; try again when it is done')
      if (devOf(id)) return apiError(409, 'dev_exists', 'delete the dev instance of this server first')
      db.servers = db.servers.filter((s) => s.id !== id)
      for (const [userId, grants] of db.grants) db.grants.set(userId, grants.filter((gr) => gr.server_id !== id))
      const details: Record<string, unknown> = { name: server.name }
      if (purge) {
        delete db.backups[id]
        delete db.backupJobs[id]
        details.backups_purged = true
      }
      recordAudit(g.user, 'server.delete', `server:${server.id}`, 'success', details)
      if (server.dev) recordAudit(g.user, 'dev.delete', `server:${server.id}`, 'success', { name: server.name })
      // Like the panel: a tombstone; the mock agent confirms the removal at once.
      const now = Date.now()
      const deletion: ServerDeletion = {
        server_id: id, name: server.name, state: 'done', container: server.instance ? 'removed' : 'none', container_error: null,
        backups: purge ? 'purged' : 'none', backups_error: null, requested_at: now, updated_at: now, completed_at: now,
      }
      db.deletions.unshift(deletion)
      return HttpResponse.json<ServerDeletion>(deletion, { status: 202 })
    }),
  ),

  op(
    'listServerDeletions',
    http.get(`${API}/servers/deletions`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const since = Date.now() - 7 * 24 * 3600 * 1000
      return HttpResponse.json<ServerDeletion[]>(db.deletions.filter((d) => d.completed_at === null || d.completed_at >= since))
    }),
  ),

  op(
    'abandonServerDeletion',
    http.post(`${API}/servers/deletions/:id/abandon`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const d = db.deletions.find((x) => x.server_id === idParam(params))
      if (!d) return apiError(404, 'not_found', 'deletion not found')
      if (d.completed_at !== null) return apiError(409, 'conflict', 'the deletion is complete')
      const now = Date.now()
      Object.assign(d, { state: 'abandoned', updated_at: now, completed_at: now })
      recordAudit(g.user, 'server.delete_abandon', `server:${d.server_id}`, 'success', { name: d.name })
      return HttpResponse.json<ServerDeletion>(d)
    }),
  ),

  op(
    'retryServerDeletion',
    http.post(`${API}/servers/deletions/:id/retry`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const d = db.deletions.find((x) => x.server_id === idParam(params))
      if (!d) return apiError(404, 'not_found', 'deletion not found')
      if (d.completed_at !== null) return apiError(409, 'conflict', 'the deletion is complete')
      // The mock's cleanup always succeeds on a retry.
      const now = Date.now()
      if (d.container === 'pending') d.container = 'removed'
      if (d.backups === 'pending' || d.backups === 'failed') d.backups = 'purged'
      Object.assign(d, { state: 'done', container_error: null, backups_error: null, updated_at: now, completed_at: now })
      return HttpResponse.json<ServerDeletion>(d)
    }),
  ),

  op(
    'resolveServerRecovery',
    http.post(`${API}/servers/:id/recovery/resolve`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const server = db.servers.find((s) => s.id === idParam(params))
      if (!server) return apiError(404, 'not_found', 'server not found')
      if (server.dataState === 'unknown') return apiError(503, 'data_state_unknown', 'the files service did not report the data state')
      if (server.dataState !== 'recovery_required' || !server.recovery) return apiError(409, 'recovery_required', 'the server has no unresolved restore')
      // Like the files service: the half-swapped folders move to backups/recovery/<id>-<unix>, the journal entry goes.
      const recovery: Recovery = { ...server.recovery, folder: `${server.id}-${Math.floor(Date.now() / 1000)}` }
      server.dataState = 'safe'
      server.recovery = undefined
      recordAudit(g.user, 'server.recovery_resolve', `server:${server.id}`, 'success', { name: server.name, folder: recovery.folder })
      return HttpResponse.json(recovery)
    }),
  ),

  op(
    'serverPower',
    http.post(`${API}/servers/:id/power`, async ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.power')) return apiError(403, 'forbidden', 'missing permission server.power')
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      const body = await readJson<{ action?: PowerAction }>(request)
      if (!body?.action || !ACTIONS.includes(body.action)) return apiError(400, 'validation_failed', 'action must be start, stop or restart')
      if (!server.instance) return apiError(409, 'conflict', 'server is not configured (no instance)')
      // Data safety spec §1.1: only a safe data directory admits a start or restart; stop always works.
      if (body.action !== 'stop') {
        if (server.dataState === 'recovery_required') return apiError(409, 'recovery_required', 'a restore of this server could not be completed; resolve it first')
        if (server.dataState === 'unknown') return apiError(503, 'data_state_unknown', 'the files service did not report the data state')
        if (server.dataState === 'busy') return apiError(409, 'conflict', 'a job is rewriting the data directory')
      }
      settle(server)
      // A dev that is stopped by hand sleeps (its waker wakes it); a start wakes it.
      if (server.dev) server.dev.sleeping = body.action === 'stop'
      if (body.action === 'stop') {
        server.status = stoppedStatus()
      } else if (body.action === 'start' && server.status.state === 'running') {
        // already running: nothing to do
      } else {
        // start of a stopped server and every restart: up in the current revision
        server.status = runningStatus(server.instance.memory_mib, Date.now())
        server.runningRevision = server.revision
      }
      recordAudit(g.user, 'server.power', `server:${server.id}`, 'success', { action: body.action, name: server.name })
      return HttpResponse.json({ status: 'accepted' as const }, { status: 202 })
    }),
  ),
]
