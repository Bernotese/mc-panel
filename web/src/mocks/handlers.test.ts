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
    expect(ids).toHaveLength(76)
    expect(operations.map((o) => o.operationId).sort()).toEqual(ids)
  })
})

describe('mock behaviour', () => {
  it('lists files without the RCON files and edits with a conflict check', async () => {
    const me = loginAs('admin')
    const root = (await callApi('GET', '/servers/1/files')).body as { entries: Array<{ name: string; type: string }> }
    const names = root.entries.map((e) => e.name)
    expect(names).toContain('server.properties')
    expect(names).not.toContain('.rcon-cli.env')
    expect(root.entries[0].type).toBe('dir') // folders first
    const read = await callApi('GET', '/servers/1/files/content?path=server.properties')
    expect(field(read.body, 'content')).toContain('rcon.password=********')
    const mtime = field(read.body, 'mtime') as number
    const save = (expected: number | null) =>
      callApi('PUT', '/servers/1/files/content', { body: { path: 'server.properties', content: 'motd=x\n', expected_mtime: expected }, csrf: me.csrf_token })
    expect((await save(mtime - 1)).status).toBe(409)
    expect(field((await save(mtime - 1)).body, 'error')).toBe('file_modified')
    expect((await save(mtime)).status).toBe(200)
    expect(field((await save(null)).body, 'error')).toBe('file_exists')
    expect(db.audit.at(-1)).toMatchObject({ action: 'file.write', details: { path: 'server.properties' } })
    expect(field((await callApi('GET', '/servers/1/files/content?path=plugins/EssentialsX.jar')).body, 'error')).toBe('not_text')
    expect((await callApi('GET', '/servers/1/files/content?path=../x')).status).toBe(400)
  })

  it('uploads, manages and extracts files', async () => {
    const me = loginAs('admin')
    const upload = (path: string, overwrite = false) =>
      fetch(`${window.location.origin}/api/servers/1/files/upload?path=${encodeURIComponent(path)}&overwrite=${overwrite}`, {
        method: 'POST',
        headers: { 'X-CSRF-Token': me.csrf_token, 'Content-Type': 'application/octet-stream' },
        body: new Uint8Array([1, 2, 3]),
      })
    expect((await upload('plugins/new.jar')).status).toBe(201)
    expect((await upload('plugins/new.jar')).status).toBe(409)
    expect((await upload('plugins/new.jar', true)).status).toBe(201)
    const post = (path: string, body: unknown) => callApi('POST', `/servers/1/files/${path}`, { body, csrf: me.csrf_token })
    expect((await post('mkdir', { path: 'backups' })).status).toBe(201)
    expect((await post('mkdir', { path: 'backups' })).status).toBe(409)
    expect((await post('rename', { from: 'backups', to: 'old-backups' })).status).toBe(204)
    expect((await post('delete', { path: 'old-backups' })).status).toBe(204)
    expect((await post('delete', { path: '' })).status).toBe(400)
    // The running server refuses extraction; the stopped one runs a job.
    expect(field((await post('extract', { path: 'world-backup.zip', dest: 'restored' })).body, 'error')).toBe('instance_running')
    const job = await callApi('POST', '/servers/2/files/extract', { body: { path: 'world-backup.zip', dest: 'restored' }, csrf: me.csrf_token })
    expect(job.status).toBe(202)
    expect(field(job.body, 'state')).toBe('running')
    const status = await callApi('GET', '/servers/2/files/jobs')
    expect(field(field(status.body, 'job'), 'dest')).toBe('restored')
  })

  it('requires server.files for the file browser', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/servers/1/files')).status).toBe(403)
  })

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

  it('runs console commands and player actions on running servers only', async () => {
    const me = loginAs('admin')
    const cmd = (id: number, command: string) => callApi('POST', `/servers/${id}/console`, { body: { command }, csrf: me.csrf_token })
    expect(field((await cmd(1, '/list')).body, 'output')).toMatch(/^There are 2 of a max of 20 players online: Alex, Steve$/)
    expect(field((await cmd(1, 'say a\nstop')).body, 'error')).toBe('validation_failed')
    expect(field((await cmd(2, 'list')).body, 'error')).toBe('instance_not_running')
    expect(db.audit.at(-1)).toMatchObject({ action: 'server.console_command', details: { command: 'list' } })

    const act = (body: object) => callApi('POST', '/servers/1/players/actions', { body, csrf: me.csrf_token })
    expect(field((await act({ action: 'op', name: 'Steve' })).body, 'output')).toBe('Made Steve a server operator')
    expect(field((await act({ action: 'ban', name: 'Steve', reason: 'griefing' })).body, 'output')).toBe('Banned Steve: griefing')
    expect(field((await act({ action: 'op', name: 'Steve', reason: 'x' })).body, 'error')).toBe('validation_failed')
    expect(field((await act({ action: 'kick', name: 'a b' })).body, 'error')).toBe('validation_failed')
    expect(field((await act({ action: 'kick', name: 'Alex', reason: 'a\nb' })).body, 'error')).toBe('validation_failed')
    const lists = (await callApi('GET', '/servers/1/players/lists')).body as { ops: Array<{ name: string }>; bans: Array<{ name: string; reason: string }> }
    expect(lists.ops.map((o) => o.name)).toContain('Steve')
    expect(lists.bans).toContainEqual(expect.objectContaining({ name: 'Steve', reason: 'griefing' }))
    expect((await callApi('GET', '/servers/1/players')).body).toMatchObject({ available: true, online: [{ name: 'Alex' }], max: 20 })
    expect((await callApi('GET', '/servers/2/players')).body).toEqual({ available: false, online: [], max: null })
    expect((await callApi('GET', '/servers/3/players')).status).toBe(404)
  })

  it('gives server.view only the online players', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/servers/1/players')).status).toBe(200)
    expect((await callApi('GET', '/servers/1/players/lists')).status).toBe(403)
    expect((await callApi('GET', '/servers/1/console/stream')).status).toBe(403)
  })

  it('streams console lines as server-sent events', async () => {
    loginAs('admin')
    const read = async (id: number, until: string) => {
      // Closing goes through the abort signal: the response body is tee'd on its way through MSW, so
      // reader.cancel() would wait for the other branch and never settle.
      const abort = new AbortController()
      const res = await fetch(`${window.location.origin}/api/servers/${id}/console/stream`, {
        headers: { Accept: 'text/event-stream' },
        signal: abort.signal,
      })
      expect(res.headers.get('Content-Type')).toBe('text/event-stream')
      const reader = res.body!.getReader()
      const decoder = new TextDecoder()
      let text = ''
      while (!text.includes(until)) {
        const { value, done } = await reader.read()
        if (done) break
        text += decoder.decode(value, { stream: true })
      }
      abort.abort()
      void reader.cancel().catch(() => undefined)
      return text
    }
    const running = await read(1, 'RCON Client /127.0.0.1 started')
    expect(running).toContain('event: state\ndata: {"running":true}')
    expect(running).toMatch(/id: \d{16,}\nevent: line\ndata: \{"ts":\d+,"stream":"out","line":"\[\d\d:\d\d:\d\d INFO\]: Starting minecraft server/)
    expect(running).toContain('Thread RCON Client /127.0.0.1 started')
    expect(await read(2, 'running')).toContain('event: state\ndata: {"running":false}')
  })
})
