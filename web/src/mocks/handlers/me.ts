import { http, HttpResponse } from 'msw'
import type { Locale, Me, Passkey, PasskeyCreated, RecoveryCodesResponse, Session, TotpSetupResponse } from '@/api/types'
import {
  db,
  generateRecoveryCodes,
  hasSecondFactor,
  MOCK_TOTP_CODE,
  MOCK_TOTP_SECRET,
  nextId,
  recordAudit,
  rotateSession,
  toMe,
  toSession,
  WEBAUTHN_CHALLENGE,
} from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, type Authed, type MockOperation } from './util'

/** Setup-stage users may add a first factor without sudo; full-stage users with 2FA need sudo. */
function needsSudoForNewFactor(g: Authed): boolean {
  return g.session.stage === 'full' && hasSecondFactor(g.user) && !(g.session.sudoUntil > Date.now())
}

function finishFirstFactor(g: Authed): string[] {
  const codes = g.user.recoveryCodes.length === 0 ? generateRecoveryCodes() : []
  if (codes.length > 0) g.user.recoveryCodes = codes
  if (g.session.stage === 'setup') g.session.stage = 'full'
  rotateSession(g.session)
  return codes
}

export const meOperations: MockOperation[] = [
  op(
    'updateMe',
    http.patch(`${API}/me`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ locale?: Locale }>(request)
      if (body?.locale !== 'de' && body?.locale !== 'en') return apiError(400, 'bad_request', 'locale must be de or en')
      g.user.locale = body.locale
      return HttpResponse.json<Me>(toMe(g.user, g.session))
    }),
  ),

  op(
    'changePassword',
    http.post(`${API}/me/password`, async ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const body = await readJson<{ current_password?: string; new_password?: string }>(request)
      if (body?.current_password !== g.user.password) return apiError(401, 'invalid_credentials', 'current password is wrong')
      const next = body.new_password ?? ''
      if (next.length < 12 || next.length > 256) return apiError(400, 'validation_failed', 'password must be 12-256 characters')
      g.user.password = next
      db.sessions = db.sessions.filter((s) => s.userId !== g.user.id || s.id === g.session.id)
      rotateSession(g.session)
      recordAudit(g.user, 'auth.password.change', g.user.username)
      return noContent()
    }),
  ),

  op(
    'setupTotp',
    http.post(`${API}/me/totp/setup`, ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      if (needsSudoForNewFactor(g)) return apiError(403, 'sudo_required', 're-authentication required')
      if (g.user.totpEnabled) return apiError(409, 'conflict', 'TOTP already enabled')
      g.user.totpPending = true
      const label = encodeURIComponent(`mc-panel:${g.user.username}`)
      return HttpResponse.json<TotpSetupResponse>({
        secret: MOCK_TOTP_SECRET,
        otpauth_url: `otpauth://totp/${label}?secret=${MOCK_TOTP_SECRET}&issuer=mc-panel&algorithm=SHA1&digits=6&period=30`,
      })
    }),
  ),

  op(
    'enableTotp',
    http.post(`${API}/me/totp/enable`, async ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      if (!g.user.totpPending) return apiError(409, 'conflict', 'no TOTP setup pending')
      const body = await readJson<{ code?: string }>(request)
      if (body?.code !== MOCK_TOTP_CODE) return apiError(401, 'invalid_code', 'invalid code')
      g.user.totpEnabled = true
      g.user.totpPending = false
      const codes = finishFirstFactor(g)
      recordAudit(g.user, 'auth.totp.enable', g.user.username)
      return HttpResponse.json<RecoveryCodesResponse>({ codes })
    }),
  ),

  op(
    'disableTotp',
    http.delete(`${API}/me/totp`, ({ request }) => {
      const g = guard(request, { sudo: true })
      if (g instanceof Response) return g
      if (!g.user.totpEnabled) return apiError(409, 'conflict', 'TOTP is not enabled')
      const lastFactor = g.user.passkeys.length === 0
      if (lastFactor && (g.user.role === 'admin' || db.settings.enforce_2fa_for_users)) {
        return apiError(409, 'conflict', 'cannot remove the last second factor')
      }
      g.user.totpEnabled = false
      recordAudit(g.user, 'auth.totp.disable', g.user.username)
      return noContent()
    }),
  ),

  op(
    'beginPasskeyRegistration',
    http.post(`${API}/me/passkeys/begin`, ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      if (needsSudoForNewFactor(g)) return apiError(403, 'sudo_required', 're-authentication required')
      return HttpResponse.json({
        publicKey: {
          challenge: WEBAUTHN_CHALLENGE,
          rp: { name: 'mc-panel', id: 'localhost' },
          user: { id: `dXNlci0${g.user.id}`, name: g.user.username, displayName: g.user.username },
          pubKeyCredParams: [
            { type: 'public-key', alg: -7 },
            { type: 'public-key', alg: -257 },
          ],
          timeout: 60_000,
          attestation: 'none',
          excludeCredentials: [],
          authenticatorSelection: { residentKey: 'preferred', userVerification: 'preferred' },
        },
      })
    }),
  ),

  op(
    'finishPasskeyRegistration',
    http.post(`${API}/me/passkeys/finish`, async ({ request }) => {
      const g = guard(request, { stages: ['setup', 'full'] })
      if (g instanceof Response) return g
      const body = await readJson<{ name?: string; credential?: unknown }>(request)
      const name = body?.name?.trim() ?? ''
      if (name.length < 1 || name.length > 64) return apiError(400, 'validation_failed', 'name must be 1-64 characters')
      if (typeof body?.credential !== 'object' || body.credential === null) return apiError(400, 'bad_request', 'credential missing')
      const passkey: Passkey = { id: nextId(), name, created_at: Date.now(), last_used_at: null }
      g.user.passkeys.push(passkey)
      const codes = finishFirstFactor(g)
      recordAudit(g.user, 'auth.passkey.add', g.user.username, 'success', { name })
      return HttpResponse.json<PasskeyCreated>({ passkey, recovery_codes: codes }, { status: 201 })
    }),
  ),

  op(
    'listPasskeys',
    http.get(`${API}/me/passkeys`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      return HttpResponse.json<Passkey[]>(g.user.passkeys)
    }),
  ),

  op(
    'deletePasskey',
    http.delete(`${API}/me/passkeys/:id`, ({ request, params }) => {
      const g = guard(request, { sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const passkey = g.user.passkeys.find((p) => p.id === id)
      if (!passkey) return apiError(404, 'not_found', 'passkey not found')
      const lastFactor = !g.user.totpEnabled && g.user.passkeys.length === 1
      if (lastFactor && (g.user.role === 'admin' || db.settings.enforce_2fa_for_users)) {
        return apiError(409, 'conflict', 'cannot remove the last second factor')
      }
      g.user.passkeys = g.user.passkeys.filter((p) => p.id !== id)
      recordAudit(g.user, 'auth.passkey.delete', g.user.username, 'success', { name: passkey.name })
      return noContent()
    }),
  ),

  op(
    'regenerateRecoveryCodes',
    http.post(`${API}/me/recovery-codes`, ({ request }) => {
      const g = guard(request, { sudo: true })
      if (g instanceof Response) return g
      g.user.recoveryCodes = generateRecoveryCodes()
      recordAudit(g.user, 'auth.recovery.regenerate', g.user.username)
      return HttpResponse.json<RecoveryCodesResponse>({ codes: [...g.user.recoveryCodes] })
    }),
  ),

  op(
    'listMySessions',
    http.get(`${API}/me/sessions`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      return HttpResponse.json<Session[]>(db.sessions.filter((s) => s.userId === g.user.id).map(toSession))
    }),
  ),

  op(
    'revokeMySession',
    http.delete(`${API}/me/sessions/:id`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!db.sessions.some((s) => s.id === id && s.userId === g.user.id)) return apiError(404, 'not_found', 'session not found')
      db.sessions = db.sessions.filter((s) => s.id !== id)
      if (id === db.currentSessionId) db.currentSessionId = null
      return noContent()
    }),
  ),

  op(
    'revokeOtherSessions',
    http.post(`${API}/me/sessions/revoke-others`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      db.sessions = db.sessions.filter((s) => s.userId !== g.user.id || s.id === g.session.id)
      return noContent()
    }),
  ),
]
