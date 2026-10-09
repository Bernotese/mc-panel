import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { currentSession, db, findUserByName, loginAs, MOCK_PASSWORD, MOCK_TOTP_CODE } from '@/mocks/db'
import { server } from '@/mocks/node'
import { api } from './client'
import { getCsrfToken, setCsrfToken } from './csrf'
import { unwrap, unwrapVoid } from './errors'
import { configureApiHandlers } from './handlers'

describe('api client', () => {
  it('stores the CSRF token from Me responses and sends it on mutations', async () => {
    const onMe = vi.fn()
    configureApiHandlers({ onMe })
    const pre = await unwrap(api.POST('/auth/login', { body: { username: 'admin', password: MOCK_PASSWORD } }))
    expect(getCsrfToken()).toBe(pre.csrf_token)
    const full = await unwrap(api.POST('/auth/2fa/totp', { body: { code: MOCK_TOTP_CODE } }))
    expect(full.stage).toBe('full')
    expect(getCsrfToken()).toBe(full.csrf_token)
    expect(onMe).toHaveBeenCalledTimes(2)
  })

  it('does not send the CSRF header on GET', async () => {
    let seen: string | null = 'unset'
    server.use(
      http.get('*/api/servers', ({ request }) => {
        seen = request.headers.get('X-CSRF-Token')
        return HttpResponse.json([])
      }),
    )
    setCsrfToken('token-1')
    await unwrap(api.GET('/servers'))
    expect(seen).toBeNull()
  })

  it('refetches Me after an endpoint that rotates the session', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    const onMe = vi.fn()
    configureApiHandlers({ onMe })
    const res = await unwrap(api.POST('/auth/sudo/totp', { body: { code: MOCK_TOTP_CODE } }))
    expect(getCsrfToken()).not.toBe(me.csrf_token)
    expect(getCsrfToken()).toBe(currentSession()?.csrf)
    expect(onMe).toHaveBeenCalledWith(expect.objectContaining({ sudo_until: res.sudo_until }))
  })

  it('recovers exactly once from a stale CSRF token', async () => {
    loginAs('admin')
    setCsrfToken('stale-token')
    await unwrapVoid(api.POST('/me/sessions/revoke-others'))
    expect(getCsrfToken()).toBe(currentSession()?.csrf)
    expect(db.sessions.filter((s) => s.userId === 1)).toHaveLength(1)
  })

  it('redirects on 401 unauthorized but not on wrong passwords or codes', async () => {
    const onUnauthorized = vi.fn()
    configureApiHandlers({ onUnauthorized })
    await expect(unwrap(api.POST('/auth/login', { body: { username: 'admin', password: 'wrong-password!' } }))).rejects.toMatchObject({
      status: 401,
      code: 'invalid_credentials',
    })
    const pre = loginAs('admin', { stage: 'preauth' })
    setCsrfToken(pre.csrf_token)
    await expect(unwrap(api.POST('/auth/2fa/totp', { body: { code: '000000' } }))).rejects.toMatchObject({ code: 'invalid_code' })
    expect(onUnauthorized).not.toHaveBeenCalled()

    db.currentSessionId = null
    await expect(unwrap(api.GET('/me/sessions'))).rejects.toMatchObject({ code: 'unauthorized' })
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(getCsrfToken()).toBe('')
  })

  it('asks for sudo and retries the original request with the rotated token', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    const requestSudo = vi.fn(async () => {
      await unwrap(api.POST('/auth/sudo/totp', { body: { code: MOCK_TOTP_CODE } }))
      return true
    })
    configureApiHandlers({ requestSudo })
    await unwrapVoid(api.DELETE('/me/totp'))
    expect(requestSudo).toHaveBeenCalledTimes(1)
    expect(findUserByName('admin')?.totpEnabled).toBe(false)
  })

  it('returns sudo_required when the dialog is cancelled', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    configureApiHandlers({ requestSudo: async () => false })
    await expect(unwrapVoid(api.DELETE('/me/totp'))).rejects.toMatchObject({ status: 403, code: 'sudo_required' })
    expect(findUserByName('admin')?.totpEnabled).toBe(true)
  })

  it('retries a sudo_required request at most once (no dialog loop)', async () => {
    const me = loginAs('admin')
    setCsrfToken(me.csrf_token)
    let hits = 0
    server.use(
      http.post('*/api/users', () => {
        hits += 1
        return HttpResponse.json({ error: 'sudo_required', message: 'still no sudo' }, { status: 403 })
      }),
    )
    const requestSudo = vi.fn(async () => true)
    configureApiHandlers({ requestSudo })
    await expect(
      unwrap(api.POST('/users', { body: { username: 'carol', password: 'long-enough-password', role: 'user' } })),
    ).rejects.toMatchObject({ code: 'sudo_required' })
    expect(requestSudo).toHaveBeenCalledTimes(1)
    expect(hits).toBe(2)
  })

  it('reports setup_2fa_required', async () => {
    const me = loginAs('newadmin', { stage: 'setup' })
    setCsrfToken(me.csrf_token)
    const onSetupRequired = vi.fn()
    configureApiHandlers({ onSetupRequired })
    await expect(unwrap(api.GET('/me/passkeys'))).rejects.toMatchObject({ code: 'setup_2fa_required' })
    expect(onSetupRequired).toHaveBeenCalledTimes(1)
  })
})
