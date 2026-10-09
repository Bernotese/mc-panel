import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it } from 'vitest'
import { getCsrfToken } from '@/api/csrf'
import i18n from '@/i18n'
import { loginAs } from '@/mocks/db'
import { applyMe, clearSession, fetchMe, loadMe, meKey, refreshMe } from './me'

describe('me cache', () => {
  it('applyMe stores the token, caches Me and switches the language', () => {
    const qc = new QueryClient()
    const me = loginAs('bob')
    applyMe(qc, me)
    expect(getCsrfToken()).toBe(me.csrf_token)
    expect(qc.getQueryData(meKey)).toEqual(me)
    expect(i18n.language).toBe('en')
    expect(document.documentElement.lang).toBe('en')
  })

  it('fetchMe resolves null without a session', async () => {
    expect(await fetchMe()).toBeNull()
  })

  it('loadMe uses the cache while refreshMe bypasses it', async () => {
    const qc = new QueryClient()
    loginAs('admin')
    expect((await loadMe(qc))?.username).toBe('admin')
    loginAs('alice')
    expect((await loadMe(qc))?.username).toBe('admin')
    expect((await refreshMe(qc))?.username).toBe('alice')
  })

  it('clearSession forgets Me, the token and all other cached data', () => {
    const qc = new QueryClient()
    applyMe(qc, loginAs('admin'))
    qc.setQueryData(['users'], [])
    clearSession(qc)
    expect(qc.getQueryData(meKey)).toBeNull()
    expect(qc.getQueryData(['users'])).toBeUndefined()
    expect(getCsrfToken()).toBe('')
  })
})
