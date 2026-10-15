import { http, HttpResponse } from 'msw'
import type {
  Catalog,
  CreateServerRequest,
  InstanceType,
  Permission,
  PowerAction,
  Server,
  ServerMemoryLimits,
  ServerSettings,
  UpdateServerRequest,
  UpdateServerSettingsRequest,
} from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { javaFor } from '@/features/servers/java'
import { db, emptyPlayers, nextId, recordAudit, resolveProperties, runningStatus, stoppedStatus, type MockServer } from '../db'
import { restoreActive } from './backups'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type MockOperation } from './util'

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
const MEMORY_RESERVE_MIB = 1024

/** Same formula as servers.MaxMemoryMiB: (host − 1 GiB) / 1.25 in 256 MiB steps, within 1..32 GiB. */
function maxMemoryMiB(hostMiB: number): number {
  const m = Math.floor(((hostMiB - MEMORY_RESERVE_MIB) * 4) / 5)
  return Math.min(Math.max(m - (m % 256), 1024), 32768)
}

/** Same rule as servers.RecommendedMemoryMiB. */
function recommendedMemoryMiB(type: InstanceType, hostMiB: number, allocatedMiB: number): number {
  const base = type === 'fabric' || type === 'neoforge' || type === 'modrinth' || type === 'curseforge' ? 6144 : 4096
  const half = Math.floor(Math.max(hostMiB - MEMORY_RESERVE_MIB - allocatedMiB, 0) / 2)
  return Math.min(Math.max(Math.min(base, half - (half % 256)), 1024), maxMemoryMiB(hostMiB))
}

function memoryLimits(): ServerMemoryLimits {
  const allocated = db.servers.reduce((sum, s) => sum + (s.instance?.memory_mib ?? 0), 0)
  const recommended = Object.fromEntries(TYPES.map((t) => [t, recommendedMemoryMiB(t, MOCK_HOST_MIB, allocated)])) as ServerMemoryLimits['recommended_mib']
  return { min_mib: 1024, max_mib: maxMemoryMiB(MOCK_HOST_MIB), step_mib: 256, host_total_mib: MOCK_HOST_MIB, allocated_mib: allocated, recommended_mib: recommended }
}

function validMemory(m: unknown): m is number {
  return typeof m === 'number' && Number.isInteger(m) && m >= 1024 && m <= maxMemoryMiB(MOCK_HOST_MIB) && m % 256 === 0
}

const MEMORY_MESSAGE = `memory_mib: must be 1024..${maxMemoryMiB(MOCK_HOST_MIB)} in steps of 256 on this host`

/** Like filesapi.ValidTimezone: an exact IANA name the browser lists (or UTC), never an offset. */
function validTimezone(tz: unknown): tz is string {
  if (typeof tz !== 'string' || tz.length > 64) return false
  return tz === 'UTC' || Intl.supportedValuesOf('timeZone').includes(tz)
}

function settingsBody(): ServerSettings {
  return { ...db.serverSettings, unknown_containers: [] }
}

/** A new container comes up between two reads: "creating" becomes "running" on the next GET. */
function settle(s: MockServer): void {
  if (s.status.state === 'creating' && s.instance) {
    s.status = runningStatus(s.instance.memory_mib, Date.now())
    s.runningRevision = s.revision
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
function toServer(s: MockServer, permissions: Permission[], admin = true): Server {
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
    instance,
    status: admin ? s.status : { ...s.status, last_error: '' },
    down_alerts: admin ? (db.monitoring.downAlerts[s.id] ?? true) : null,
  }
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
          for (const s of db.servers) if (s.instance) bumpRevision(s)
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
      if (body.memory_mib !== undefined && !validMemory(body.memory_mib)) {
        return apiError(400, 'validation_failed', MEMORY_MESSAGE)
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
      db.servers = db.servers.filter((s) => s.id !== id)
      for (const [userId, grants] of db.grants) db.grants.set(userId, grants.filter((gr) => gr.server_id !== id))
      const details: Record<string, unknown> = { name: server.name }
      if (purge) {
        delete db.backups[id]
        delete db.backupJobs[id]
        details.backups_purged = true
      }
      recordAudit(g.user, 'server.delete', `server:${server.id}`, 'success', details)
      return noContent()
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
      settle(server)
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
