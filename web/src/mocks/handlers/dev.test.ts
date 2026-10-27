import { afterEach, describe, expect, it, vi } from 'vitest'
import type { DevInstance, Server } from '@/api/types'
import { callApi, field } from '@/test/call-api'
import { db, loginAs } from '../db'
import { DEV_SYNC_MS } from './dev-model'

afterEach(() => {
  vi.useRealTimers()
})

describe('mock dev instances', () => {
  it('seeds survival with the sleeping dev-survival', async () => {
    loginAs('admin')
    const list = (await callApi('GET', '/servers')).body as Server[]
    const prod = list.find((s) => s.name === 'survival')
    const dev = list.find((s) => s.name === 'dev-survival')
    expect(prod?.dev_id).toBe(dev?.id)
    expect(prod?.dev).toBeUndefined()
    expect(dev?.dev).toMatchObject({ source_id: 1, source_name: 'survival', sleeping: true, sync_paused: false, last_sync_status: 'ok', sync_pending: false })
    expect(dev?.dev?.next_sync_at).toBeGreaterThan(Date.now())
    expect(dev?.instance?.properties).toMatchObject({ motd: '[DEV] survival', view_distance: 4, simulation_distance: 3 })
    expect(dev?.address).toBe('dev-survival.mc.example.com')
  })

  it('creates a dev with sudo only, once, and refuses devs of devs', async () => {
    const me = loginAs('admin')
    const create = (id: number, body: unknown = {}) => callApi('POST', `/servers/${id}/dev`, { body, csrf: me.csrf_token })
    expect(field((await create(2)).body, 'error')).toBe('sudo_required')
    const sudo = loginAs('admin', { sudo: true })
    const post = (id: number, body: unknown = {}) => callApi('POST', `/servers/${id}/dev`, { body, csrf: sudo.csrf_token })
    const created = await post(4)
    expect(created.status).toBe(201)
    const dev = created.body as Server
    expect(dev).toMatchObject({ name: 'dev-modded', dev: { source_id: 4, sync_pending: true, last_sync_status: 'running', sleeping: true } })
    expect(dev.instance?.memory_mib).toBe(3072) // fabric: a mod loader
    expect(db.audit.at(-1)).toMatchObject({ action: 'dev.create' })
    expect(field((await post(4)).body, 'error')).toBe('dev_exists')
    expect(field((await post(dev.id)).body, 'error')).toBe('conflict')
    expect(field((await post(3)).body, 'error')).toBe('conflict') // lobby has no instance
    expect(field((await post(2, { memory_mib: 1000 })).body, 'error')).toBe('validation_failed')
    expect(field((await post(2, { name: 'x' })).body, 'error')).toBe('bad_request')
    expect((await post(2)).status).toBe(201)
    expect(((await callApi('GET', '/servers/2')).body as Server).dev_id).toBeDefined()
  })

  it('pauses, schedules and applies the overrides with PATCH', async () => {
    const me = loginAs('admin')
    const patch = (body: unknown) => callApi('PATCH', '/servers/5/dev', { body, csrf: me.csrf_token })
    const paused = (await patch({ sync_paused: true })).body as DevInstance
    expect(paused).toMatchObject({ sync_paused: true, next_sync_at: null })
    expect(paused.paused_at).not.toBeNull()
    const resumed = (await patch({ sync_paused: false, sync_time: 120, idle_minutes: 30 })).body as DevInstance
    expect(resumed).toMatchObject({ sync_paused: false, paused_at: null, sync_time: 120, idle_minutes: 30 })
    expect(resumed.next_sync_at).toBeGreaterThan(Date.now())
    await patch({ view_distance: 6 })
    expect(((await callApi('GET', '/servers/5')).body as Server).instance?.properties.view_distance).toBe(6)
    expect(field((await patch({ idle_minutes: 4 })).body, 'error')).toBe('validation_failed')
    expect(field((await patch({ motd: 'x' })).body, 'error')).toBe('bad_request')
    expect(field((await callApi('PATCH', '/servers/1/dev', { body: {}, csrf: me.csrf_token })).body, 'error')).toBe('not_dev')
  })

  it('runs a sync now and refuses a second one while it runs', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    const me = loginAs('admin')
    const sync = () => callApi('POST', '/servers/5/dev/sync', { csrf: me.csrf_token })
    const first = await sync()
    expect(first.status).toBe(202)
    expect(first.body).toMatchObject({ sync_pending: true, last_sync_status: 'running' })
    expect(field((await sync()).body, 'error')).toBe('backup_running')
    vi.setSystemTime(Date.now() + DEV_SYNC_MS)
    const done = (await callApi('GET', '/servers/5/dev')).body as DevInstance
    expect(done).toMatchObject({ sync_pending: false, last_sync_status: 'ok', sleeping: true })
    expect(done.last_sync_at).toBe(Date.now())
  })

  it('keeps the dev ops list with validated names', async () => {
    const me = loginAs('admin')
    expect((await callApi('GET', '/servers/5/dev/ops')).body).toEqual({ names: ['Steve'] })
    const add = (name: string) => callApi('POST', '/servers/5/dev/ops', { body: { name }, csrf: me.csrf_token })
    expect((await add('Alex')).body).toEqual({ names: ['Alex', 'Steve'] })
    expect((await add('alex')).body).toEqual({ names: ['Alex', 'Steve'] })
    expect(field((await add('no spaces')).body, 'error')).toBe('validation_failed')
    expect((await callApi('DELETE', '/servers/5/dev/ops/steve', { csrf: me.csrf_token })).body).toEqual({ names: ['Alex'] })
    expect(db.audit.at(-1)).toMatchObject({ action: 'dev.op_remove' })
  })

  it('resolves permissions through the prod, without backups', async () => {
    loginAs('alice') // server.view and server.power on survival
    const dev = (await callApi('GET', '/servers/5')).body as Server
    expect(dev.permissions).toEqual(['server.view', 'server.power'])
    expect((await callApi('GET', '/servers/5/dev')).status).toBe(200)
    expect((await callApi('GET', '/servers/5/dev/ops')).status).toBe(403)
    loginAs('admin')
    expect(field((await callApi('GET', '/servers/5/backups')).body, 'error')).toBe('dev_managed')
  })

  it('guards servers: dev- names, prod deletion and dev updates', async () => {
    const me = loginAs('admin', { sudo: true })
    const csrf = me.csrf_token
    const createServer = await callApi('POST', '/servers', { body: { name: 'dev-x', type: 'paper', version: '1.21.11', memory_mib: 2048, eula_accepted: true }, csrf })
    expect(field(createServer.body, 'error')).toBe('validation_failed')
    expect(field((await callApi('DELETE', '/servers/1', { csrf })).body, 'error')).toBe('dev_exists')
    expect(field((await callApi('PATCH', '/servers/5', { body: { version: '26.2' }, csrf })).body, 'error')).toBe('dev_managed')
    expect((await callApi('PATCH', '/servers/5', { body: { memory_mib: 3072 }, csrf })).status).toBe(200)
    await callApi('POST', '/servers/5/power', { body: { action: 'start' }, csrf })
    expect(((await callApi('GET', '/servers/5')).body as Server).dev?.sleeping).toBe(false)
    await callApi('POST', '/servers/5/power', { body: { action: 'stop' }, csrf })
    expect(((await callApi('GET', '/servers/5')).body as Server).dev?.sleeping).toBe(true)
    expect((await callApi('DELETE', '/servers/5', { csrf })).status).toBe(204)
    expect(db.audit.at(-1)).toMatchObject({ action: 'dev.delete' })
    expect((await callApi('DELETE', '/servers/1', { csrf })).status).toBe(204)
  })
})
