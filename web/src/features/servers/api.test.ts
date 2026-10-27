import { describe, expect, it } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { db, loginAs } from '@/mocks/db'
import { createTestQueryClient } from '@/test/render'
import {
  catalogQueryOptions,
  createServer,
  powerServer,
  serverQueryOptions,
  serverSettingsQueryOptions,
  serversQueryOptions,
  updateServer,
  updateServerSettings,
} from './api'

function signIn(username: string, sudo = false): void {
  setCsrfToken(loginAs(username, { sudo }).csrf_token)
}

describe('servers api against the mock', () => {
  it('lists servers with instance, address and status', async () => {
    signIn('admin')
    const servers = await createTestQueryClient().fetchQuery(serversQueryOptions)
    expect(servers.map((s) => [s.name, s.status.state])).toEqual([
      ['survival', 'running'],
      ['creative', 'stopped'],
      ['lobby', 'not_configured'],
      ['modded', 'stopped'],
      ['dev-survival', 'stopped'],
    ])
    expect(servers[0].address).toBe('survival.mc.example.com')
    expect(servers[2].instance).toBeNull()
  })

  it('creates a server that runs from the next read on', async () => {
    signIn('admin', true)
    const created = await createServer({ name: 'skyblock', type: 'paper', version: '26.2', memory_mib: 4096, eula_accepted: true })
    expect(created.status.state).toBe('creating')
    const fresh = await createTestQueryClient().fetchQuery(serverQueryOptions(created.id))
    expect(fresh.status.state).toBe('running')
  })

  it('flags a restart after changing a running server and clears it on restart', async () => {
    signIn('admin', true)
    const updated = await updateServer(1, { memory_mib: 6144 })
    expect(updated.status.restart_required).toBe(true)
    await powerServer(1, 'restart')
    const fresh = await createTestQueryClient().fetchQuery(serverQueryOptions(1))
    expect(fresh.status.restart_required).toBe(false)
    expect(fresh.instance?.memory_mib).toBe(6144)
  })

  it('reports an unavailable catalog as catalog_unavailable without retrying', async () => {
    signIn('admin')
    delete db.catalogs.vanilla
    expect(catalogQueryOptions('vanilla').retry).toBe(false)
    await expect(createTestQueryClient().fetchQuery(catalogQueryOptions('vanilla'))).rejects.toMatchObject({
      status: 502,
      code: 'catalog_unavailable',
    })
  })

  it('stores the CurseForge key write-only', async () => {
    signIn('admin', true)
    const settings = await updateServerSettings({ curseforge_api_key: 'abc$def' })
    expect(settings.curseforge_api_key_set).toBe(true)
    expect(JSON.stringify(settings)).not.toContain('abc$def')
    const fresh = await createTestQueryClient().fetchQuery(serverSettingsQueryOptions)
    expect(fresh.base_domain).toBe('mc.example.com')
    expect(fresh.curseforge_api_key_set).toBe(true)
  })
})
