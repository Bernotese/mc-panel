import { describe, expect, it } from 'vitest'
import { callApi, field } from '@/test/call-api'
import { db, findUserByName, loginAs, MOCK_PASSWORD, MOCK_TOTP_CODE } from '../db'

describe('mock auth flow', () => {
  it('rejects a wrong password and records the attempt', async () => {
    const before = db.attempts.length
    const res = await callApi('POST', '/auth/login', { body: { username: 'admin', password: 'wrong-password-123' } })
    expect(res.status).toBe(401)
    expect(field(res.body, 'error')).toBe('invalid_credentials')
    expect(db.attempts).toHaveLength(before + 1)
    expect(db.attempts[0].success).toBe(false)
  })

  it('walks admin through password and TOTP and rotates the CSRF token', async () => {
    const login = await callApi('POST', '/auth/login', { body: { username: 'admin', password: MOCK_PASSWORD } })
    expect(login.status).toBe(200)
    expect(field(login.body, 'stage')).toBe('preauth')
    expect(field(login.body, 'second_factors')).toEqual(['totp', 'webauthn', 'recovery'])
    const preCsrf = String(field(login.body, 'csrf_token'))

    const noCsrf = await callApi('POST', '/auth/2fa/totp', { body: { code: MOCK_TOTP_CODE } })
    expect(field(noCsrf.body, 'error')).toBe('csrf_invalid')
    const wrong = await callApi('POST', '/auth/2fa/totp', { body: { code: '000000' }, csrf: preCsrf })
    expect(wrong.status).toBe(401)
    expect(field(wrong.body, 'error')).toBe('invalid_code')

    const full = await callApi('POST', '/auth/2fa/totp', { body: { code: MOCK_TOTP_CODE }, csrf: preCsrf })
    expect(field(full.body, 'stage')).toBe('full')
    expect(field(full.body, 'csrf_token')).not.toBe(preCsrf)
  })

  it('forces a fresh admin into the setup stage until TOTP is enabled', async () => {
    const login = await callApi('POST', '/auth/login', { body: { username: 'newadmin', password: MOCK_PASSWORD } })
    expect(field(login.body, 'stage')).toBe('setup')
    const csrf = String(field(login.body, 'csrf_token'))

    const blocked = await callApi('GET', '/me/passkeys')
    expect(field(blocked.body, 'error')).toBe('setup_2fa_required')

    const setup = await callApi('POST', '/me/totp/setup', { csrf })
    expect(field(setup.body, 'secret')).toBe('JBSWY3DPEHPK3PXP')
    const enable = await callApi('POST', '/me/totp/enable', { body: { code: MOCK_TOTP_CODE }, csrf })
    expect(field(enable.body, 'codes')).toHaveLength(10)

    const me = await callApi('GET', '/auth/me')
    expect(field(me.body, 'stage')).toBe('full')
    expect(field(me.body, 'csrf_token')).not.toBe(csrf)
  })

  it('requires sudo for sensitive actions and rotates the token on sudo', async () => {
    const me = loginAs('admin')
    const denied = await callApi('DELETE', '/me/totp', { csrf: me.csrf_token })
    expect(field(denied.body, 'error')).toBe('sudo_required')

    const sudo = await callApi('POST', '/auth/sudo/totp', { body: { code: MOCK_TOTP_CODE }, csrf: me.csrf_token })
    expect(Number(field(sudo.body, 'sudo_until'))).toBeGreaterThan(Date.now())

    const stale = await callApi('DELETE', '/me/totp', { csrf: me.csrf_token })
    expect(field(stale.body, 'error')).toBe('csrf_invalid')

    const fresh = await callApi('GET', '/auth/me')
    const ok = await callApi('DELETE', '/me/totp', { csrf: String(field(fresh.body, 'csrf_token')) })
    expect(ok.status).toBe(204)
    expect(findUserByName('admin')?.totpEnabled).toBe(false)
  })

  it('refuses to remove the last second factor of an admin', async () => {
    const me = loginAs('admin', { sudo: true })
    const admin = findUserByName('admin')
    if (!admin) throw new Error('seed missing')
    admin.passkeys = []
    const res = await callApi('DELETE', '/me/totp', { csrf: me.csrf_token })
    expect(res.status).toBe(409)
    expect(field(res.body, 'error')).toBe('conflict')
  })

  it('lists own sessions with the current one flagged and revokes the others', async () => {
    const me = loginAs('admin')
    const list = await callApi('GET', '/me/sessions')
    const sessions = list.body as Array<{ current: boolean }>
    expect(sessions).toHaveLength(3)
    expect(sessions.filter((s) => s.current)).toHaveLength(1)
    const revoke = await callApi('POST', '/me/sessions/revoke-others', { csrf: me.csrf_token })
    expect(revoke.status).toBe(204)
    expect((await callApi('GET', '/me/sessions')).body).toHaveLength(1)
  })

  it('lets admins see every session but users not', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/admin/sessions')).status).toBe(403)
    loginAs('admin')
    const all = (await callApi('GET', '/admin/sessions')).body as Array<{ username: string }>
    expect(all.map((s) => s.username)).toEqual(expect.arrayContaining(['admin', 'alice']))
  })
})
