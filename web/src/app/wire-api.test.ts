import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { apiHandlers } from '@/api/handlers'
import { meKey } from '@/features/auth/me'
import { isSudoOpen, settleSudo } from '@/features/sudo/sudo-store'
import { db, loginAs } from '@/mocks/db'
import { wireApi } from './wire-api'

function fakeNavigator(path: string) {
  return { push: vi.fn(), currentPath: () => path }
}

describe('wireApi', () => {
  it('sends unauthenticated users to /login and drops cached data', () => {
    const qc = new QueryClient()
    qc.setQueryData(meKey, loginAs('admin'))
    qc.setQueryData(['users'], [])
    const nav = fakeNavigator('/users')
    wireApi(qc, nav)
    apiHandlers().onUnauthorized()
    expect(qc.getQueryData(meKey)).toBeNull()
    expect(qc.getQueryData(['users'])).toBeUndefined()
    expect(nav.push).toHaveBeenCalledWith('/login')
  })

  it('does not navigate again when already on /login', () => {
    const nav = fakeNavigator('/login')
    wireApi(new QueryClient(), nav)
    apiHandlers().onUnauthorized()
    expect(nav.push).not.toHaveBeenCalled()
  })

  it('sends an expired pre-auth session on /login/2fa back to /login', () => {
    const nav = fakeNavigator('/login/2fa')
    wireApi(new QueryClient(), nav)
    apiHandlers().onUnauthorized()
    expect(nav.push).toHaveBeenCalledWith('/login')
  })

  it('sends setup-stage users to /setup-2fa', async () => {
    const nav = fakeNavigator('/users')
    wireApi(new QueryClient(), nav)
    await apiHandlers().onSetupRequired()
    expect(nav.push).toHaveBeenCalledWith('/setup-2fa')
  })

  it('sends users who must change their password to /change-password after refreshing Me', async () => {
    const qc = new QueryClient()
    db.users.find((u) => u.username === 'alice')!.mustChangePassword = true
    qc.setQueryData(meKey, { ...loginAs('alice'), stage: 'full' })
    const stageAtPush: Array<string | undefined> = []
    const nav = { currentPath: () => '/', push: vi.fn((path: string) => stageAtPush.push(`${path} ${qc.getQueryData<{ stage: string }>(meKey)?.stage}`)) }
    wireApi(qc, nav)
    await apiHandlers().onPasswordChangeRequired()
    expect(stageAtPush).toEqual(['/change-password password'])
  })

  it('refreshes a cached full-stage Me before navigating to /setup-2fa', async () => {
    const qc = new QueryClient()
    qc.setQueryData(meKey, { ...loginAs('newadmin', { stage: 'full' }), stage: 'full' })
    const stageAtPush: Array<string | undefined> = []
    const nav = { currentPath: () => '/', push: vi.fn(() => stageAtPush.push(qc.getQueryData<{ stage: string }>(meKey)?.stage)) }
    wireApi(qc, nav)
    await apiHandlers().onSetupRequired()
    expect(stageAtPush).toEqual(['setup'])
  })

  it('applies every Me the client sees', () => {
    const qc = new QueryClient()
    wireApi(qc, fakeNavigator('/'))
    const me = loginAs('admin')
    apiHandlers().onMe(me)
    expect(qc.getQueryData(meKey)).toEqual(me)
  })

  it('routes sudo requests to the sudo store', async () => {
    wireApi(new QueryClient(), fakeNavigator('/'))
    const answer = apiHandlers().requestSudo({ method: 'DELETE', path: '/servers/{id}' })
    expect(isSudoOpen()).toBe(true)
    settleSudo(true)
    await expect(answer).resolves.toBe(true)
  })
})
