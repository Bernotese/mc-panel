import { HttpResponse, type HttpHandler } from 'msw'
import type { ErrorCode, Permission, Stage } from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { currentSession, db, findUser, type MockSession, type MockUser } from '../db'

/** Wildcard origin: matches in jsdom (http://localhost:3000) and in the browser worker. */
export const API = '*/api'
export const ALL_STAGES: Stage[] = ['preauth', 'setup', 'full']

export interface MockOperation {
  operationId: string
  handler: HttpHandler
}

export function op(operationId: string, handler: HttpHandler): MockOperation {
  return { operationId, handler }
}

export function apiError(status: number, code: ErrorCode, message: string = code) {
  return HttpResponse.json({ error: code, message }, { status })
}

export type ErrorResponse = ReturnType<typeof apiError>

export interface Authed {
  session: MockSession
  user: MockUser
}

export interface GuardOptions {
  stages?: Stage[]
  admin?: boolean
  sudo?: boolean
}

/** Mirrors the panel middleware order: session → CSRF → stage → role → sudo. */
export function guard(request: Request, opts: GuardOptions = {}): Authed | ErrorResponse {
  const session = currentSession()
  const user = session ? findUser(session.userId) : undefined
  if (!session || !user) return apiError(401, 'unauthorized', 'authentication required')
  if (request.method !== 'GET' && request.headers.get('X-CSRF-Token') !== session.csrf) {
    return apiError(403, 'csrf_invalid', 'missing or invalid CSRF token')
  }
  const stages = opts.stages ?? ['full']
  if (!stages.includes(session.stage)) {
    return session.stage === 'setup'
      ? apiError(403, 'setup_2fa_required', 'two-factor setup required')
      : apiError(401, 'unauthorized', 'authentication incomplete')
  }
  if (opts.admin && user.role !== 'admin') return apiError(403, 'forbidden', 'admin role required')
  if (opts.sudo && !(session.sudoUntil > Date.now())) return apiError(403, 'sudo_required', 're-authentication required')
  session.lastSeenAt = Date.now()
  return { session, user }
}

export async function readJson<T>(request: Request): Promise<T | null> {
  try {
    return (await request.json()) as T
  } catch {
    return null
  }
}

export function idParam(params: Record<string, string | readonly string[] | undefined>): number {
  const raw = params.id
  const value = Number(typeof raw === 'string' ? raw : raw?.[0])
  return Number.isInteger(value) && value > 0 ? value : -1
}

export function noContent() {
  return new HttpResponse(null, { status: 204 })
}

export function paginate<T>(items: T[], url: URL): { items: T[]; next_cursor: string | null } {
  const offset = Math.max(0, Number(url.searchParams.get('cursor') ?? '0') || 0)
  const limit = Math.min(200, Math.max(1, Number(url.searchParams.get('limit') ?? '50') || 50))
  const next = offset + limit < items.length ? String(offset + limit) : null
  return { items: items.slice(offset, offset + limit), next_cursor: next }
}

export function serverPermissions(user: MockUser, serverId: number): Permission[] {
  if (user.role === 'admin') return [...ALL_PERMISSIONS]
  return db.grants.get(user.id)?.find((g) => g.server_id === serverId)?.permissions ?? []
}
