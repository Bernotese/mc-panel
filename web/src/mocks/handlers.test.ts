import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import { callApi, field } from '@/test/call-api'
import { db, loginAs } from './db'
import { operations } from './handlers'

const openapiYaml = fs.readFileSync(path.resolve(import.meta.dirname, '../../../api/openapi.yaml'), 'utf8')

describe('mock coverage', () => {
  it('mocks every operationId of api/openapi.yaml exactly once', () => {
    const ids = [...openapiYaml.matchAll(/operationId:\s*(\w+)/g)].map((m) => m[1]).sort()
    expect(ids).toHaveLength(61)
    expect(operations.map((o) => o.operationId).sort()).toEqual(ids)
  })
})

describe('mock behaviour', () => {
  it('shows a restricted user only the servers they hold permissions on', async () => {
    loginAs('alice')
    const list = (await callApi('GET', '/servers')).body as Array<{ name: string; permissions: string[] }>
    expect(list.map((s) => s.name)).toEqual(['survival', 'creative'])
    expect(list[0].permissions).toContain('server.power')
    expect((await callApi('GET', '/servers/3')).status).toBe(403)
  })

  it('creates users only with sudo and rejects duplicate names', async () => {
    const me = loginAs('admin')
    const body = { username: 'carol', password: 'long-enough-password', role: 'user' }
    expect(field((await callApi('POST', '/users', { body, csrf: me.csrf_token })).body, 'error')).toBe('sudo_required')
    const sudo = loginAs('admin', { sudo: true })
    expect((await callApi('POST', '/users', { body, csrf: sudo.csrf_token })).status).toBe(201)
    expect((await callApi('POST', '/users', { body, csrf: sudo.csrf_token })).status).toBe(409)
  })

  it('refuses to demote the last admin', async () => {
    const me = loginAs('admin', { sudo: true })
    db.users = db.users.filter((u) => u.username !== 'newadmin')
    const res = await callApi('PATCH', '/users/1', { body: { role: 'user' }, csrf: me.csrf_token })
    expect(res.status).toBe(409)
  })

  it('pages login attempts with an opaque cursor and filters by result', async () => {
    loginAs('admin')
    const first = await callApi('GET', '/security/attempts?limit=50')
    expect(field(first.body, 'items')).toHaveLength(50)
    expect(field(first.body, 'next_cursor')).toBe('50')
    const last = await callApi('GET', '/security/attempts?limit=50&cursor=100')
    expect(field(last.body, 'next_cursor')).toBeNull()
    const failures = (await callApi('GET', '/security/attempts?success=false')).body as { items: Array<{ success: boolean }> }
    expect(failures.items.every((a) => !a.success)).toBe(true)
  })

  it('validates country codes in security settings', async () => {
    const me = loginAs('admin', { sudo: true })
    const res = await callApi('PUT', '/security/settings', {
      body: { enforce_2fa_for_users: false, admin_allowlist_only: false, allowed_countries: ['Germany'] },
      csrf: me.csrf_token,
    })
    expect(field(res.body, 'error')).toBe('validation_failed')
  })

  it('filters the audit log by result and pages it', async () => {
    loginAs('admin')
    const page = (await callApi('GET', '/audit?result=denied&limit=5')).body as {
      items: Array<{ result: string }>
      next_cursor: string | null
    }
    expect(page.items).toHaveLength(5)
    expect(page.items.every((e) => e.result === 'denied')).toBe(true)
    expect(page.next_cursor).toBe('5')
  })

  it('keeps server settings: absent stays, null removes, the key is write-only', async () => {
    const me = loginAs('admin', { sudo: true })
    const get = async () => (await callApi('GET', '/servers/settings')).body
    expect(await get()).toEqual({ base_domain: 'mc.example.com', curseforge_api_key_set: false, docker: 'ok', unknown_containers: [] })
    await callApi('PUT', '/servers/settings', { body: { curseforge_api_key: 'abc$123' }, csrf: me.csrf_token })
    expect(field(await get(), 'curseforge_api_key_set')).toBe(true)
    expect(JSON.stringify(await get())).not.toContain('abc$123')
    await callApi('PUT', '/servers/settings', { body: { base_domain: 'Play.Example.org' }, csrf: me.csrf_token })
    expect(await get()).toMatchObject({ base_domain: 'play.example.org', curseforge_api_key_set: true })
    await callApi('PUT', '/servers/settings', { body: { base_domain: null, curseforge_api_key: null }, csrf: me.csrf_token })
    expect(await get()).toMatchObject({ base_domain: null, curseforge_api_key_set: false })
    const bad = await callApi('PUT', '/servers/settings', { body: { base_domain: 'localhost' }, csrf: me.csrf_token })
    expect(field(bad.body, 'error')).toBe('validation_failed')
  })

  it('creates a server that is running on the next read', async () => {
    const me = loginAs('admin', { sudo: true })
    const body = { name: 'skyblock', type: 'fabric', version: '1.20.4', memory_mib: 2048, eula_accepted: true }
    const created = await callApi('POST', '/servers', { body, csrf: me.csrf_token })
    expect(created.status).toBe(201)
    expect(created.body).toMatchObject({
      address: 'skyblock.mc.example.com',
      instance: { type: 'fabric', java: 'java17', properties: { motd: 'skyblock', max_players: 20 } },
      status: { state: 'creating', operation: 'pulling', desired_state: 'running' },
    })
    const id = field(created.body, 'id') as number
    expect(field((await callApi('GET', `/servers/${id}`)).body, 'status')).toMatchObject({ state: 'running', operation: '' })
    expect((await callApi('POST', '/servers', { body, csrf: me.csrf_token })).status).toBe(409)
    const noEula = await callApi('POST', '/servers', { body: { ...body, name: 'other', eula_accepted: false }, csrf: me.csrf_token })
    expect(field(noEula.body, 'error')).toBe('validation_failed')
    db.serverSettings.base_domain = null
    const noDomain = await callApi('POST', '/servers', { body: { ...body, name: 'other' }, csrf: me.csrf_token })
    expect(noDomain.status).toBe(409)
    expect(field(noDomain.body, 'error')).toBe('base_domain_missing')
  })

  it('marks a running server as restart_required after an update, a stopped one not', async () => {
    const me = loginAs('admin', { sudo: true })
    const running = await callApi('PATCH', '/servers/1', { body: { memory_mib: 6144 }, csrf: me.csrf_token })
    expect(running.body).toMatchObject({ instance: { memory_mib: 6144 }, status: { state: 'running', restart_required: true } })
    const stopped = await callApi('PATCH', '/servers/2', { body: { properties: { motd: 'Hi' } }, csrf: me.csrf_token })
    expect(stopped.body).toMatchObject({ instance: { properties: { motd: 'Hi', gamemode: 'survival' } }, status: { restart_required: false } })
    expect((await callApi('PATCH', '/servers/3', { body: { memory_mib: 2048 }, csrf: me.csrf_token })).status).toBe(409)
  })

  it('applies power actions to the mock state', async () => {
    const me = loginAs('admin', { sudo: true })
    const power = (id: number, action: string) => callApi('POST', `/servers/${id}/power`, { body: { action }, csrf: me.csrf_token })
    const status = async (id: number) => field((await callApi('GET', `/servers/${id}`)).body, 'status')
    await callApi('PATCH', '/servers/1', { body: { memory_mib: 6144 }, csrf: me.csrf_token })
    expect((await power(1, 'restart')).status).toBe(202)
    expect(await status(1)).toMatchObject({ state: 'running', restart_required: false, memory_limit_bytes: 6144 * 1.25 * 1024 * 1024 })
    await power(1, 'stop')
    expect(await status(1)).toMatchObject({ state: 'stopped', desired_state: 'stopped', started_at: 0 })
    await power(2, 'start')
    expect(await status(2)).toMatchObject({ state: 'running', desired_state: 'running' })
    const stub = await power(3, 'start')
    expect(stub.status).toBe(409)
    expect(field(stub.body, 'error')).toBe('conflict')
  })

  it('serves version catalogs: 404 for modpacks, 502 when missing', async () => {
    loginAs('admin')
    const fabric = await callApi('GET', '/servers/catalog/fabric')
    expect(fabric.body).toMatchObject({ versions: [{ id: '26.3', java: 'java25' }, { id: '1.21.11', java: 'java21' }], loaders: ['0.19.5', '0.19.4'] })
    expect((await callApi('GET', '/servers/catalog/modrinth')).status).toBe(404)
    delete db.catalogs.paper
    const paper = await callApi('GET', '/servers/catalog/paper')
    expect(paper.status).toBe(502)
    expect(field(paper.body, 'error')).toBe('catalog_unavailable')
    loginAs('alice')
    expect((await callApi('GET', '/servers/catalog/vanilla')).status).toBe(403)
  })

  it('buckets the overview series by range', async () => {
    loginAs('admin')
    expect(field((await callApi('GET', '/security/overview?range=24h')).body, 'series')).toHaveLength(24)
    expect(field((await callApi('GET', '/security/overview?range=7d')).body, 'series')).toHaveLength(7)
    expect(field((await callApi('GET', '/security/overview?range=30d')).body, 'series')).toHaveLength(30)
  })
})
