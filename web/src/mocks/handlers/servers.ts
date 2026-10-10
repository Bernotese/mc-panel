import { http, HttpResponse } from 'msw'
import type { PowerAction, Server, ServerSettings, UpdateServerSettingsRequest } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { db, nextId, recordAudit } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type MockOperation } from './util'

const SERVER_NAME = /^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$/
const ACTIONS: PowerAction[] = ['start', 'stop', 'restart']

const DNS_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
const CURSEFORGE_KEY = /^[A-Za-z0-9$./_-]{1,128}$/

/** Same rule as instancespec.ValidBaseDomain (after lowercasing). */
function validBaseDomain(d: string): boolean {
  const labels = d.split('.')
  return d.length <= 200 && labels.length >= 2 && labels.every((l) => DNS_LABEL.test(l))
}

function settingsBody(): ServerSettings {
  return { ...db.serverSettings, unknown_containers: [] }
}

export const serverOperations: MockOperation[] = [
  op(
    'listServers',
    http.get(`${API}/servers`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const visible = db.servers
        .map((s) => ({ id: s.id, name: s.name, permissions: serverPermissions(g.user, s.id) }))
        .filter((s) => s.permissions.length > 0)
      return HttpResponse.json<Server[]>(visible)
    }),
  ),

  op(
    'createServer',
    http.post(`${API}/servers`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ name?: string }>(request)
      const name = body?.name ?? ''
      if (!SERVER_NAME.test(name)) return apiError(400, 'validation_failed', 'name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$')
      if (db.servers.some((s) => s.name === name)) return apiError(409, 'conflict', 'server name taken')
      const server = { id: nextId(), name }
      db.servers.push(server)
      recordAudit(g.user, 'server.create', `server:${name}`)
      return HttpResponse.json<Server>({ ...server, permissions: [...ALL_PERMISSIONS] }, { status: 201 })
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
        }
      }
      if (body.curseforge_api_key !== undefined) {
        if (body.curseforge_api_key !== null && !CURSEFORGE_KEY.test(body.curseforge_api_key)) {
          return apiError(400, 'validation_failed', 'curseforge_api_key must match ^[A-Za-z0-9$./_-]{1,128}$')
        }
        db.serverSettings.curseforge_api_key_set = body.curseforge_api_key !== null
        details.curseforge_api_key = body.curseforge_api_key === null ? 'removed' : 'set'
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
      return HttpResponse.json<Server>({ ...server, permissions })
    }),
  ),

  op(
    'deleteServer',
    http.delete(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      db.servers = db.servers.filter((s) => s.id !== id)
      for (const [userId, grants] of db.grants) db.grants.set(userId, grants.filter((gr) => gr.server_id !== id))
      recordAudit(g.user, 'server.delete', `server:${server.name}`)
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
      recordAudit(g.user, 'server.power', `server:${server.name}`, 'success', { action: body.action })
      return HttpResponse.json({ status: 'accepted' as const }, { status: 202 })
    }),
  ),
]
