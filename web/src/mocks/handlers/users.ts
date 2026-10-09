import { http, HttpResponse } from 'msw'
import type { Permission, PermissionGrant, Role, User, UserPermissions } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { db, findUser, nextId, recordAudit, toUser, type MockUser } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, type MockOperation } from './util'

const USERNAME = /^[a-zA-Z0-9_.-]{3,32}$/

function isRole(value: unknown): value is Role {
  return value === 'admin' || value === 'user'
}

function withView(perms: Permission[]): Permission[] {
  const set = new Set<Permission>(perms)
  if (set.size > 0) set.add('server.view')
  return ALL_PERMISSIONS.filter((p) => set.has(p))
}

function adminCount(): number {
  return db.users.filter((u) => u.role === 'admin').length
}

export const userOperations: MockOperation[] = [
  op(
    'listUsers',
    http.get(`${API}/users`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<User[]>(db.users.map(toUser))
    }),
  ),

  op(
    'createUser',
    http.post(`${API}/users`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<{ username?: string; password?: string; role?: string }>(request)
      const username = body?.username ?? ''
      const password = body?.password ?? ''
      if (!USERNAME.test(username)) return apiError(400, 'validation_failed', 'username must match ^[a-zA-Z0-9_.-]{3,32}$')
      if (password.length < 12 || password.length > 256) return apiError(400, 'validation_failed', 'password must be 12-256 characters')
      if (!isRole(body?.role)) return apiError(400, 'validation_failed', 'invalid role')
      if (db.users.some((u) => u.username.toLowerCase() === username.toLowerCase())) return apiError(409, 'conflict', 'username taken')
      const user: MockUser = {
        id: nextId(),
        username,
        password,
        role: body.role,
        locale: 'de',
        totpEnabled: false,
        totpPending: false,
        passkeys: [],
        recoveryCodes: [],
        createdAt: Date.now(),
        lastLoginAt: null,
      }
      db.users.push(user)
      recordAudit(g.user, 'user.create', `user:${username}`)
      return HttpResponse.json<User>(toUser(user), { status: 201 })
    }),
  ),

  op(
    'getUser',
    http.get(`${API}/users/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      return HttpResponse.json<User>(toUser(user))
    }),
  ),

  op(
    'updateUser',
    http.patch(`${API}/users/:id`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      const body = await readJson<{ role?: string; password?: string }>(request)
      if (body?.role !== undefined && !isRole(body.role)) return apiError(400, 'validation_failed', 'invalid role')
      if (body?.password !== undefined && (body.password.length < 12 || body.password.length > 256)) {
        return apiError(400, 'validation_failed', 'password must be 12-256 characters')
      }
      if (body?.role === 'user' && user.role === 'admin' && adminCount() === 1) return apiError(409, 'conflict', 'cannot demote the last admin')
      if (isRole(body?.role)) user.role = body.role
      if (body?.password !== undefined) user.password = body.password
      recordAudit(g.user, 'user.update', `user:${user.username}`, 'success', { role: user.role })
      return HttpResponse.json<User>(toUser(user))
    }),
  ),

  op(
    'deleteUser',
    http.delete(`${API}/users/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      if (user.id === g.user.id) return apiError(409, 'conflict', 'cannot delete yourself')
      if (user.role === 'admin' && adminCount() === 1) return apiError(409, 'conflict', 'cannot delete the last admin')
      db.users = db.users.filter((u) => u.id !== user.id)
      db.sessions = db.sessions.filter((s) => s.userId !== user.id)
      db.grants.delete(user.id)
      recordAudit(g.user, 'user.delete', `user:${user.username}`)
      return noContent()
    }),
  ),

  op(
    'resetUser2fa',
    http.post(`${API}/users/:id/reset-2fa`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      user.totpEnabled = false
      user.totpPending = false
      user.passkeys = []
      user.recoveryCodes = []
      recordAudit(g.user, 'user.reset_2fa', `user:${user.username}`)
      return noContent()
    }),
  ),

  op(
    'getUserPermissions',
    http.get(`${API}/users/:id/permissions`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      return HttpResponse.json<UserPermissions>({ grants: db.grants.get(user.id) ?? [] })
    }),
  ),

  op(
    'setUserPermissions',
    http.put(`${API}/users/:id/permissions`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const user = findUser(idParam(params))
      if (!user) return apiError(404, 'not_found', 'user not found')
      const body = await readJson<{ grants?: PermissionGrant[] }>(request)
      if (!Array.isArray(body?.grants)) return apiError(400, 'bad_request', 'grants required')
      for (const grant of body.grants) {
        if (!db.servers.some((s) => s.id === grant.server_id)) return apiError(400, 'validation_failed', `unknown server ${grant.server_id}`)
        if (!grant.permissions.every((p: Permission) => ALL_PERMISSIONS.includes(p))) return apiError(400, 'validation_failed', 'unknown permission')
      }
      // Stream D contract: any permission on a server implies server.view.
      const grants = body.grants
        .filter((gr) => gr.permissions.length > 0)
        .map((gr) => ({ server_id: gr.server_id, permissions: withView(gr.permissions) }))
      db.grants.set(user.id, grants)
      recordAudit(g.user, 'permissions.set', `user:${user.username}`, 'success', { grants })
      return HttpResponse.json<UserPermissions>({ grants })
    }),
  ),
]
