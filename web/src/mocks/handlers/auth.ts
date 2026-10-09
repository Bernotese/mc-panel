import { http, HttpResponse } from 'msw'
import type { AttemptStage, Me, SudoResponse } from '@/api/types'
import {
  db,
  findUserByName,
  MOCK_TOTP_CODE,
  normalizeCode,
  recordAttempt,
  recordAudit,
  rotateSession,
  startSession,
  stageAfterPassword,
  SUDO_MS,
  toMe,
  WEBAUTHN_CHALLENGE,
} from '../db'
import { ALL_STAGES, API, apiError, guard, noContent, op, readJson, type Authed, type MockOperation } from './util'

function requestOptions() {
  return {
    publicKey: {
      challenge: WEBAUTHN_CHALLENGE,
      timeout: 60_000,
      rpId: 'localhost',
      allowCredentials: [],
      userVerification: 'preferred',
    },
  }
}

function hasCredential(body: { credential?: unknown } | null): boolean {
  const credential = body?.credential
  return typeof credential === 'object' && credential !== null
}

function completeLogin(g: Authed, method: AttemptStage) {
  recordAttempt(g.user.username, method, true, 'ok')
  g.session.stage = 'full'
  rotateSession(g.session)
  g.user.lastLoginAt = Date.now()
  recordAudit(g.user, 'auth.login', g.user.username, 'success', { method })
  return HttpResponse.json<Me>(toMe(g.user, g.session))
}

function grantSudo(g: Authed) {
  recordAttempt(g.user.username, 'sudo', true, 'ok')
  g.session.sudoUntil = Date.now() + SUDO_MS
  rotateSession(g.session)
  recordAudit(g.user, 'auth.sudo', g.user.username)
  return HttpResponse.json<SudoResponse>({ sudo_until: g.session.sudoUntil })
}

export const authOperations: MockOperation[] = [
  op('getHealth', http.get(`${API}/health`, () => HttpResponse.json({ status: 'ok' as const }))),

  op(
    'login',
    http.post(`${API}/auth/login`, async ({ request }) => {
      const body = await readJson<{ username?: string; password?: string }>(request)
      if (!body?.username || !body.password) return apiError(400, 'bad_request', 'username and password required')
      const user = findUserByName(body.username)
      if (!user || user.password !== body.password) {
        recordAttempt(body.username, 'password', false, 'invalid_credentials')
        return apiError(401, 'invalid_credentials', 'invalid username or password')
      }
      recordAttempt(user.username, 'password', true, 'ok')
      const session = startSession(user, stageAfterPassword(user))
      if (session.stage === 'full') {
        user.lastLoginAt = Date.now()
        recordAudit(user, 'auth.login', user.username)
      }
      return HttpResponse.json<Me>(toMe(user, session))
    }),
  ),

  op(
    'getMe',
    http.get(`${API}/auth/me`, ({ request }) => {
      const g = guard(request, { stages: ALL_STAGES })
      if (g instanceof Response) return g
      return HttpResponse.json<Me>(toMe(g.user, g.session))
    }),
  ),

  op(
    'logout',
    http.post(`${API}/auth/logout`, ({ request }) => {
      const g = guard(request, { stages: ALL_STAGES })
      if (g instanceof Response) return g
      db.sessions = db.sessions.filter((s) => s.id !== g.session.id)
      db.currentSessionId = null
      return noContent()
    }),
  ),

  op(
    'verifyTotp',
    http.post(`${API}/auth/2fa/totp`, async ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      const body = await readJson<{ code?: string }>(request)
      if (!g.user.totpEnabled || body?.code !== MOCK_TOTP_CODE) {
        recordAttempt(g.user.username, 'totp', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'invalid code')
      }
      return completeLogin(g, 'totp')
    }),
  ),

  op(
    'verifyRecovery',
    http.post(`${API}/auth/2fa/recovery`, async ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      const body = await readJson<{ code?: string }>(request)
      const code = normalizeCode(body?.code ?? '')
      const index = g.user.recoveryCodes.findIndex((c) => normalizeCode(c) === code)
      if (!code || index < 0) {
        recordAttempt(g.user.username, 'recovery', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'invalid recovery code')
      }
      g.user.recoveryCodes.splice(index, 1)
      return completeLogin(g, 'recovery')
    }),
  ),

  op(
    'beginWebAuthnLogin',
    http.post(`${API}/auth/2fa/webauthn/begin`, ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      if (g.user.passkeys.length === 0) return apiError(409, 'conflict', 'no passkeys registered')
      return HttpResponse.json(requestOptions())
    }),
  ),

  op(
    'finishWebAuthnLogin',
    http.post(`${API}/auth/2fa/webauthn/finish`, async ({ request }) => {
      const g = guard(request, { stages: ['preauth'] })
      if (g instanceof Response) return g
      const body = await readJson<{ credential?: unknown }>(request)
      if (!hasCredential(body)) {
        recordAttempt(g.user.username, 'webauthn', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'assertion rejected')
      }
      g.user.passkeys[0].last_used_at = Date.now()
      return completeLogin(g, 'webauthn')
    }),
  ),

  op(
    'sudoTotp',
    http.post(`${API}/auth/sudo/totp`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ code?: string }>(request)
      if (!g.user.totpEnabled || body?.code !== MOCK_TOTP_CODE) {
        recordAttempt(g.user.username, 'sudo', false, 'invalid_code')
        return apiError(401, 'invalid_code', 'invalid code')
      }
      return grantSudo(g)
    }),
  ),

  op(
    'beginSudoWebAuthn',
    http.post(`${API}/auth/sudo/webauthn/begin`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      if (g.user.passkeys.length === 0) return apiError(409, 'conflict', 'no passkeys registered')
      return HttpResponse.json(requestOptions())
    }),
  ),

  op(
    'finishSudoWebAuthn',
    http.post(`${API}/auth/sudo/webauthn/finish`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ credential?: unknown }>(request)
      if (!hasCredential(body)) return apiError(401, 'invalid_code', 'assertion rejected')
      return grantSudo(g)
    }),
  ),
]
