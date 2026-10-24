import createClient, { type Middleware } from 'openapi-fetch'
import { BACKGROUND_HEADER, isBackground } from './background'
import { getCsrfToken, setCsrfToken } from './csrf'
import { markSudoCancelled } from './errors'
import { apiHandlers } from './handlers'
import type { paths } from './schema'
import type { Me } from './types'

/** The contract marks X-CSRF-Token as required header; the middleware sets it, so callers never pass it. */
type StripCsrfHeader<Op> = Op extends { parameters: infer P }
  ? Omit<Op, 'parameters'> & { parameters: Omit<P, 'header'> & { header?: never } }
  : Op

export type ClientPaths = {
  [Path in keyof paths]: {
    [Method in keyof paths[Path]]: Method extends 'parameters' ? paths[Path][Method] : StripCsrfHeader<paths[Path][Method]>
  }
}

export const ME_PATHS: ReadonlySet<string> = new Set([
  '/auth/login',
  '/auth/me',
  '/auth/2fa/totp',
  '/auth/2fa/recovery',
  '/auth/2fa/webauthn/finish',
  '/me',
])

/**
 * Endpoints after which the backend rotates the session token without returning Me (sudo grants, first-factor
 * enrolment of a setup-stage session). /me/password keeps the CSRF token: it revokes the other sessions and, for a
 * forced change, rotates only the cookie; the change-password page reloads Me itself for the new stage.
 */
export const ROTATING_PATHS: ReadonlySet<string> = new Set([
  '/auth/sudo/totp',
  '/auth/sudo/webauthn/finish',
  '/me/totp/enable',
  '/me/passkeys/finish',
])

function isMe(value: unknown): value is Me {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as { csrf_token?: unknown }).csrf_token === 'string' &&
    typeof (value as { stage?: unknown }).stage === 'string'
  )
}

async function readErrorCode(response: Response): Promise<string | null> {
  try {
    const body: unknown = await response.clone().json()
    const code = typeof body === 'object' && body !== null ? (body as { error?: unknown }).error : undefined
    return typeof code === 'string' ? code : null
  } catch {
    return null
  }
}

export function createApiClient(baseUrl: string = `${window.location.origin}/api`) {
  // Late-bound so that MSW (tests) can patch globalThis.fetch after this module was imported.
  const send = (request: Request): Promise<Response> => globalThis.fetch(request)
  const client = createClient<ClientPaths>({ baseUrl, credentials: 'same-origin', fetch: send })
  const retryCopies = new WeakMap<Request, Request>()

  async function captureMe(response: Response): Promise<void> {
    try {
      const body: unknown = await response.clone().json()
      if (isMe(body)) {
        setCsrfToken(body.csrf_token)
        apiHandlers().onMe(body)
      }
    } catch {
      // not JSON – nothing to capture
    }
  }

  async function refreshMe(): Promise<void> {
    const response = await send(new Request(`${baseUrl}/auth/me`, { credentials: 'same-origin', headers: { Accept: 'application/json' } }))
    if (response.ok) await captureMe(response)
  }

  async function afterSuccess(response: Response, schemaPath: string): Promise<void> {
    if (ME_PATHS.has(schemaPath)) await captureMe(response)
    if (ROTATING_PATHS.has(schemaPath)) await refreshMe()
  }

  async function retryOnce(copy: Request, schemaPath: string): Promise<Response> {
    const token = getCsrfToken()
    if (token) copy.headers.set('X-CSRF-Token', token)
    const response = await send(copy)
    if (response.ok) {
      await afterSuccess(response, schemaPath)
    } else if (response.status === 401 && (await readErrorCode(response)) === 'unauthorized') {
      setCsrfToken('')
      apiHandlers().onUnauthorized()
    }
    return response
  }

  const middleware: Middleware = {
    onRequest({ request }) {
      // Synchronous on purpose: isBackground() is only true during the poll callback (api/background.ts).
      if (isBackground()) request.headers.set(BACKGROUND_HEADER, '1')
      if (request.method !== 'GET' && request.method !== 'HEAD') {
        const token = getCsrfToken()
        if (token) request.headers.set('X-CSRF-Token', token)
      }
      retryCopies.set(request, request.clone())
      return request
    },
    async onResponse({ request, response, schemaPath }) {
      const copy = retryCopies.get(request)
      retryCopies.delete(request)
      if (response.ok) {
        await afterSuccess(response, schemaPath)
        return response
      }
      const code = await readErrorCode(response)
      if (response.status === 401 && code === 'unauthorized') {
        setCsrfToken('')
        apiHandlers().onUnauthorized()
        return response
      }
      if (response.status !== 403 || !copy) return response
      if (code === 'setup_2fa_required') {
        await apiHandlers().onSetupRequired()
        return response
      }
      if (code === 'password_change_required') {
        await apiHandlers().onPasswordChangeRequired()
        return response
      }
      if (code === 'csrf_invalid') {
        await refreshMe()
        return retryOnce(copy, schemaPath)
      }
      if (code === 'sudo_required' && !schemaPath.startsWith('/auth/')) {
        const confirmed = await apiHandlers().requestSudo({ method: request.method, path: schemaPath })
        return confirmed ? retryOnce(copy, schemaPath) : markSudoCancelled(response)
      }
      return response
    },
  }
  client.use(middleware)
  return client
}

export type ApiClient = ReturnType<typeof createApiClient>

export const api: ApiClient = createApiClient()
