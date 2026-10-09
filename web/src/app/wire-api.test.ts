import { QueryClient } from '@tanstack/react-query'
import { describe, expect, it, vi } from 'vitest'
import { apiHandlers } from '@/api/handlers'
import { meKey } from '@/features/auth/me'
import { isSudoOpen, settleSudo } from '@/features/sudo/sudo-store'
import { loginAs } from '@/mocks/db'
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

  it('does not navigate again when already on a login page', () => {
    const nav = fakeNavigator('/login/2fa')
    wireApi(new QueryClient(), nav)
    apiHandlers().onUnauthorized()
    expect(nav.push).not.toHaveBeenCalled()
  })

  it('sends setup-stage users to /setup-2fa', () => {
    const nav = fakeNavigator('/users')
    wireApi(new QueryClient(), nav)
    apiHandlers().onSetupRequired()
    expect(nav.push).toHaveBeenCalledWith('/setup-2fa')
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
    const answer = apiHandlers().requestSudo()
    expect(isSudoOpen()).toBe(true)
    settleSudo(true)
    await expect(answer).resolves.toBe(true)
  })
})
