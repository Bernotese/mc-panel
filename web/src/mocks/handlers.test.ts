import { describe, expect, it } from 'vitest'
import openapiYaml from '../../../api/openapi.yaml?raw'
import { callApi, field } from '@/test/call-api'
import { db, loginAs } from './db'
import { operations } from './handlers'

describe('mock coverage', () => {
  it('mocks every operationId of api/openapi.yaml exactly once', () => {
    const ids = [...openapiYaml.matchAll(/operationId:\s*(\w+)/g)].map((m) => m[1]).sort()
    expect(ids).toHaveLength(57)
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

  it('buckets the overview series by range', async () => {
    loginAs('admin')
    expect(field((await callApi('GET', '/security/overview?range=24h')).body, 'series')).toHaveLength(24)
    expect(field((await callApi('GET', '/security/overview?range=7d')).body, 'series')).toHaveLength(7)
    expect(field((await callApi('GET', '/security/overview?range=30d')).body, 'series')).toHaveLength(30)
  })
})
