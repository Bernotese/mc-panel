import { http, HttpResponse } from 'msw'
import type { DevInstance, DevOps, Permission, Server, UpdateDevRequest } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { PLAYER_NAME } from '@/features/players/validation'
import { hasLoader, isModpack } from '@/features/servers/validation'
import { db, devInstanceConfig, emptyPlayers, nextId, recordAudit, stoppedStatus, type MockDev, type MockServer } from '../db'
import { applyDevSpec, DEV_PREFIX, devBody, devOf, MAX_SERVER_NAME, nextSyncAt, settleDev } from './dev-model'
import { MEMORY_MESSAGE, memoryLimits, toServer, validMemory } from './servers'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

type Params = Parameters<typeof idParam>[0]

interface Ctx {
  g: Authed
  server: MockServer
  dev: MockDev
}

const UPDATE_KEYS = ['sync_paused', 'sync_time', 'idle_minutes', 'view_distance', 'simulation_distance'] as const

/** Session, permission and dev lookup of the operations on a dev's id; 409 not_dev on any other server. */
function access(request: Request, params: Params, perm: Permission): Ctx | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes(perm)) return apiError(403, 'forbidden', `missing permission ${perm}`)
  const server = db.servers.find((s) => s.id === id)
  if (!server) return apiError(404, 'not_found', 'server not found')
  if (!server.dev) return apiError(409, 'not_dev', 'the server is not a dev instance')
  settleDev(server)
  return { g, server, dev: server.dev }
}

function isInt(v: unknown, min: number, max: number): boolean {
  return typeof v === 'number' && Number.isInteger(v) && v >= min && v <= max
}

/** Like the panel: unknown fields or wrong types are a bad request, values out of range fail validation. */
function checkUpdate(body: unknown): UpdateDevRequest | ErrorResponse {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return apiError(400, 'bad_request', 'invalid JSON body')
  const raw = body as Record<string, unknown>
  if (Object.keys(raw).some((k) => !(UPDATE_KEYS as readonly string[]).includes(k))) return apiError(400, 'bad_request', 'invalid JSON body')
  if (raw.sync_paused !== undefined && typeof raw.sync_paused !== 'boolean') return apiError(400, 'bad_request', 'invalid JSON body')
  const ranges: Array<[keyof UpdateDevRequest, number, number]> = [
    ['sync_time', 0, 1439],
    ['idle_minutes', 5, 120],
    ['view_distance', 3, 32],
    ['simulation_distance', 3, 32],
  ]
  for (const [key, min, max] of ranges) {
    if (raw[key] !== undefined && !isInt(raw[key], min, max)) return apiError(400, 'validation_failed', `${key}: must be ${min}..${max}`)
  }
  return raw as UpdateDevRequest
}

function opsBody(dev: MockDev): DevOps {
  return { names: [...dev.ops].sort((a, b) => a.localeCompare(b, 'en', { sensitivity: 'base' })) }
}

function opName(raw: unknown): string | ErrorResponse {
  return typeof raw === 'string' && PLAYER_NAME.test(raw) ? raw : apiError(400, 'validation_failed', 'name must match ^[A-Za-z0-9_]{3,16}$')
}

export const devOperations: MockOperation[] = [
  op(
    'createDevInstance',
    http.post(`${API}/servers/:id/dev`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<Record<string, unknown>>(request)
      if (typeof body !== 'object' || body === null || Array.isArray(body) || Object.keys(body).some((k) => k !== 'memory_mib')) {
        return apiError(400, 'bad_request', 'invalid JSON body')
      }
      const prod = db.servers.find((s) => s.id === idParam(params))
      if (!prod) return apiError(404, 'not_found', 'server not found')
      if (prod.dev) return apiError(409, 'conflict', 'a dev instance cannot have a dev instance')
      if (!prod.instance) return apiError(409, 'conflict', 'the server has no instance yet')
      if (devOf(prod.id)) return apiError(409, 'dev_exists', 'the server already has a dev instance')
      if (!db.serverSettings.base_domain) return apiError(409, 'base_domain_missing', 'set a base domain in the server settings first')
      const name = DEV_PREFIX + prod.name
      if (name.length > MAX_SERVER_NAME) return apiError(400, 'validation_failed', 'the name of a dev instance would exceed 32 characters')
      if (db.servers.some((s) => s.name === name)) return apiError(409, 'conflict', 'server name already taken')
      const loader = hasLoader(prod.instance.type) || isModpack(prod.instance.type)
      const memory = body.memory_mib ?? Math.min(loader ? 3072 : 2048, memoryLimits().max_mib)
      if (!validMemory(memory)) return apiError(400, 'validation_failed', MEMORY_MESSAGE)
      const now = Date.now()
      const dev: MockDev = {
        sourceId: prod.id,
        syncPaused: false,
        pausedAt: null,
        syncTime: 300,
        idleMinutes: 15,
        viewDistance: 4,
        simulationDistance: 3,
        nextSyncAt: null,
        lastSyncAt: null,
        // The first sync is queued at once.
        lastSyncStatus: 'running',
        lastSyncError: null,
        syncSince: now,
        sleeping: true,
        ops: [],
      }
      dev.nextSyncAt = nextSyncAt(dev, now)
      const server: MockServer = {
        id: nextId(),
        name,
        instance: devInstanceConfig(prod.name, prod.instance, memory, dev),
        status: stoppedStatus(),
        revision: 1,
        runningRevision: 1,
        players: emptyPlayers(),
        dev,
      }
      db.servers.push(server)
      recordAudit(g.user, 'dev.create', `server:${server.id}`, 'success', { name, source: prod.name, memory_mib: memory })
      return HttpResponse.json<Server>(toServer(server, [...ALL_PERMISSIONS]), { status: 201 })
    }),
  ),

  op(
    'getDevInstance',
    http.get(`${API}/servers/:id/dev`, ({ request, params }) => {
      const c = access(request, params, 'server.view')
      if (c instanceof Response) return c
      return HttpResponse.json<DevInstance>(devBody(c.server, c.dev))
    }),
  ),

  op(
    'updateDevInstance',
    http.patch(`${API}/servers/:id/dev`, async ({ request, params }) => {
      const c = access(request, params, 'server.files')
      if (c instanceof Response) return c
      const body = checkUpdate(await readJson<unknown>(request))
      if (body instanceof Response) return body
      const { dev } = c
      const now = Date.now()
      const before = JSON.stringify(dev)
      if (body.sync_paused !== undefined && body.sync_paused !== dev.syncPaused) {
        dev.syncPaused = body.sync_paused
        dev.pausedAt = body.sync_paused ? now : null
      }
      if (body.sync_time !== undefined) dev.syncTime = body.sync_time
      if (body.idle_minutes !== undefined) dev.idleMinutes = body.idle_minutes
      const overrides =
        (body.view_distance ?? dev.viewDistance) !== dev.viewDistance || (body.simulation_distance ?? dev.simulationDistance) !== dev.simulationDistance
      if (body.view_distance !== undefined) dev.viewDistance = body.view_distance
      if (body.simulation_distance !== undefined) dev.simulationDistance = body.simulation_distance
      dev.nextSyncAt = nextSyncAt(dev, now)
      if (overrides) applyDevSpec(c.server)
      if (JSON.stringify(dev) !== before) recordAudit(c.g.user, 'dev.settings_update', `server:${c.server.id}`, 'success', { ...body })
      return HttpResponse.json<DevInstance>(devBody(c.server, dev))
    }),
  ),

  op(
    'syncDevInstance',
    http.post(`${API}/servers/:id/dev/sync`, ({ request, params }) => {
      const c = access(request, params, 'server.files')
      if (c instanceof Response) return c
      if (c.dev.syncSince !== null) return apiError(409, 'backup_running', 'a sync of this dev instance is queued or running')
      c.dev.syncSince = Date.now()
      c.dev.lastSyncStatus = 'running'
      recordAudit(c.g.user, 'dev.sync', `server:${c.server.id}`, 'success', { trigger: 'manual' })
      return HttpResponse.json<DevInstance>(devBody(c.server, c.dev), { status: 202 })
    }),
  ),

  op(
    'listDevOps',
    http.get(`${API}/servers/:id/dev/ops`, ({ request, params }) => {
      const c = access(request, params, 'server.console')
      if (c instanceof Response) return c
      return HttpResponse.json<DevOps>(opsBody(c.dev))
    }),
  ),

  op(
    'addDevOp',
    http.post(`${API}/servers/:id/dev/ops`, async ({ request, params }) => {
      const c = access(request, params, 'server.console')
      if (c instanceof Response) return c
      const body = await readJson<{ name?: unknown }>(request)
      if (typeof body !== 'object' || body === null) return apiError(400, 'bad_request', 'invalid JSON body')
      const name = opName(body.name)
      if (name instanceof Response) return name
      // Minecraft names are case-insensitive: adding a known name again changes nothing.
      if (!c.dev.ops.some((n) => n.toLowerCase() === name.toLowerCase())) {
        c.dev.ops.push(name)
        recordAudit(c.g.user, 'dev.op_add', `server:${c.server.id}`, 'success', { player: name })
      }
      return HttpResponse.json<DevOps>(opsBody(c.dev))
    }),
  ),

  op(
    'removeDevOp',
    http.delete(`${API}/servers/:id/dev/ops/:name`, ({ request, params }) => {
      const c = access(request, params, 'server.console')
      if (c instanceof Response) return c
      const name = opName(params.name)
      if (name instanceof Response) return name
      const kept = c.dev.ops.filter((n) => n.toLowerCase() !== name.toLowerCase())
      if (kept.length !== c.dev.ops.length) {
        c.dev.ops = kept
        recordAudit(c.g.user, 'dev.op_remove', `server:${c.server.id}`, 'success', { player: name })
      }
      return HttpResponse.json<DevOps>(opsBody(c.dev))
    }),
  ),
]
