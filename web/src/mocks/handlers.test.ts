import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import type { BackupJob, BackupOverview, BackupSnapshot } from '@/api/types'
import type { LiveMetrics, MetricsSeries, MonitoringOverview, MonitoringSettings } from '@/api/types'
import type { PluginInventory, PluginJob, PluginResolution, PluginSearchResult, PluginUpdates, PluginVersionList } from '@/api/types'
import type { Server } from '@/api/types'
import { callApi, field } from '@/test/call-api'
import { db, defaultMonitoring, loginAs } from './db'
import { operations } from './handlers'

const openapiYaml = fs.readFileSync(path.resolve(import.meta.dirname, '../../../api/openapi.yaml'), 'utf8')

describe('mock coverage', () => {
  it('mocks every operationId of api/openapi.yaml exactly once', () => {
    const ids = [...openapiYaml.matchAll(/operationId:\s*(\w+)/g)].map((m) => m[1]).sort()
    expect(ids).toHaveLength(106)
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

  it('validates file requests like the panel', async () => {
    const me = loginAs('admin')
    const put = (body: unknown) => callApi('PUT', '/servers/1/files/content', { body, csrf: me.csrf_token })
    expect(field((await put({ path: 'server.properties', content: 'x' })).body, 'error')).toBe('validation_failed')
    // Hidden names answer 404 on every operation, also with different case.
    expect((await put({ path: '.rcon-cli.env', content: 'x', expected_mtime: null })).status).toBe(404)
    expect((await callApi('GET', '/servers/1/files/content?path=.RCON-CLI.ENV')).status).toBe(404)
    const upload = await fetch(`${window.location.origin}/api/servers/1/files/upload?path=.rcon-cli.env&overwrite=true`, {
      method: 'POST',
      headers: { 'X-CSRF-Token': me.csrf_token, 'Content-Type': 'application/octet-stream' },
      body: new Uint8Array([1]),
    })
    expect(upload.status).toBe(404)
    const post = (path: string, body: unknown) => callApi('POST', `/servers/2/files/${path}`, { body, csrf: me.csrf_token })
    expect((await post('mkdir', { path: '.mcpanel-x' })).status).toBe(404)
    expect((await post('rename', { from: 'ops.json', to: '.rcon-cli.yaml' })).status).toBe(404)
    expect((await post('delete', { path: '.rcon-cli.env' })).status).toBe(404)
    expect((await post('extract', { path: 'world-backup.zip', dest: '.mcpanel-tmp' })).status).toBe(404)
    // Without a known state (agent unreachable) extraction cannot be checked.
    db.servers[1].status = { ...db.servers[1].status, state: 'unknown' }
    const unknown = await post('extract', { path: 'world-backup.zip', dest: 'restored' })
    expect(unknown.status).toBe(503)
    expect(field(unknown.body, 'error')).toBe('agent_unavailable')
  })

  it('lists the plugin inventory per server type', async () => {
    loginAs('admin')
    const inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv).toMatchObject({ kind: 'plugin', dir: 'plugins', game_version: '1.21.11', sources: ['modrinth', 'hangar'], truncated: false, restart_needed: false, source_errors: [] })
    expect(inv.jars.map((j) => j.file)).toEqual(['Disabled.jar.disabled', 'LuckPerms-Bukkit-5.5.71.jar', 'OldTool.jar', 'ViaVersion-5.5.1.jar'])
    expect(inv.jars.find((j) => j.file === 'OldTool.jar')).toMatchObject({ source: 'manual', project: null, version: null, meta: { name: 'OldTool', version: '1.0' }, installed_by_panel: false })
    expect(inv.jars.find((j) => j.file === 'Disabled.jar.disabled')).toMatchObject({ enabled: false, source: 'modrinth', installed_by_panel: false })
    expect(inv.jars.map((j) => j.project?.title)).toEqual(['BlueMap', 'LuckPerms', undefined, 'ViaVersion'])
    expect(inv.jars[1].project?.icon_url).toMatch(/^\/api\/servers\/1\/plugins\/icon\?url=https%3A%2F%2Fcdn\.modrinth\.com%2F/)
    const mods = (await callApi('GET', '/servers/4/plugins')).body as PluginInventory
    expect(mods).toMatchObject({ kind: 'mod', dir: 'mods', sources: ['modrinth'] })
    expect(mods.jars.map((j) => j.file)).toEqual(['fabric-api-0.110.0.jar'])
    expect(field((await callApi('GET', '/servers/2/plugins')).body, 'error')).toBe('plugins_unsupported')
    expect((await callApi('GET', '/servers/3/plugins')).status).toBe(404)
  })

  it('searches compatible projects per source and kind', async () => {
    loginAs('admin')
    const search = async (id: number, query: string) => (await callApi('GET', `/servers/${id}/plugins/search?${query}`)).body as PluginSearchResult
    const modrinth = await search(1, 'source=modrinth')
    expect(modrinth.hits.map((h) => h.slug).sort()).toEqual(['bluemap', 'chunky', 'chunkyborder', 'luckperms'])
    expect(modrinth.total).toBe(4)
    expect(modrinth.hits.find((h) => h.slug === 'luckperms')?.installed).toBe(true)
    expect(modrinth.hits.find((h) => h.slug === 'chunky')?.installed).toBe(false)
    expect((await search(1, 'source=modrinth&offset=2')).hits).toHaveLength(2)
    const via = await search(1, 'source=hangar&q=ViaBack')
    expect(via.hits.map((h) => h.title)).toEqual(['ViaBackwards'])
    expect(via.hits[0]).toMatchObject({ installed: false, page_url: 'https://hangar.papermc.io/ViaVersion/ViaBackwards' })
    const mods = await search(4, 'source=modrinth')
    expect(mods.hits.map((h) => h.slug).sort()).toEqual(['fabric-api', 'lithium', 'servercore']) // sodium-extra is client-only
    expect(field((await callApi('GET', '/servers/4/plugins/search?source=hangar')).body, 'error')).toBe('validation_failed')
    expect((await callApi('GET', '/servers/1/plugins/search?source=modrinth&offset=1001')).status).toBe(400)
    expect((await callApi('GET', `/servers/1/plugins/search?source=modrinth&q=${'x'.repeat(101)}`)).status).toBe(400)
  })

  it('resolves dependencies and refuses versions that are only available externally', async () => {
    loginAs('admin')
    const resolve = (id: number, query: string) => callApi('GET', `/servers/${id}/plugins/resolve?${query}`)
    const viaBackwards = (await resolve(1, 'source=hangar&project=ViaBackwards')).body as PluginResolution
    expect(viaBackwards.install.map((e) => e.title)).toEqual(['ViaBackwards'])
    expect(viaBackwards.already).toEqual([{ source: 'hangar', project_id: '2', title: 'ViaVersion', file: 'ViaVersion-5.5.1.jar' }])
    const border = (await resolve(1, 'source=modrinth&project=chunkyborder')).body as PluginResolution
    expect(border.install.map((e) => [e.title, e.required_by])).toEqual([
      ['Chunky', 'ChunkyBorder'],
      ['ChunkyBorder', null],
    ])
    expect(border.incompatible).toEqual([])
    // project accepts the slug and the project id; the same installed version is "already", another one is installed
    expect(((await resolve(1, 'source=modrinth&project=Vebnzrzj&version=lp5571')).body as PluginResolution).already.map((a) => a.file)).toEqual(['LuckPerms-Bukkit-5.5.71.jar'])
    expect(((await resolve(1, 'source=modrinth&project=luckperms&version=lp5572')).body as PluginResolution).install.map((e) => e.filename)).toEqual(['LuckPerms-Bukkit-5.5.72.jar'])
    expect(field((await resolve(1, 'source=hangar&project=Essentials')).body, 'error')).toBe('plugin_not_downloadable')
    const essentials = (await callApi('GET', '/servers/1/plugins/versions?source=hangar&project=Essentials')).body as PluginVersionList
    expect(essentials.versions.length).toBeGreaterThan(0)
    expect(essentials.versions.every((x) => !x.downloadable && x.external_url !== null)).toBe(true)
    const lp = (await callApi('GET', '/servers/1/plugins/versions?source=modrinth&project=luckperms')).body as PluginVersionList
    expect(lp.versions.map((x) => x.channel)).toEqual(['beta', 'release', 'release'])
    expect(field((await resolve(4, 'source=modrinth&project=sodium-extra')).body, 'error')).toBe('plugin_incompatible')
    expect(((await resolve(4, 'source=modrinth&project=servercore')).body as PluginResolution).already.map((a) => a.title)).toEqual(['Fabric API'])
    expect((await resolve(1, 'source=modrinth&project=../x')).status).toBe(400)
  })

  it('installs with a job, one job per server', async () => {
    const me = loginAs('admin')
    const install = (body: object) => callApi('POST', '/servers/1/plugins/install', { body, csrf: me.csrf_token })
    const started = await install({ source: 'modrinth', project: 'chunkyborder' })
    expect(started.status).toBe(202)
    const job = started.body as PluginJob
    expect(job).toMatchObject({ kind: 'install', state: 'running', finished_at: 0 })
    expect(job.steps.map((s) => [s.file, s.state])).toEqual([
      ['Chunky-Bukkit-1.4.40.jar', 'downloading'],
      ['ChunkyBorder-Bukkit-1.2.23.jar', 'pending'],
    ])
    expect(field((await install({ source: 'hangar', project: 'ViaBackwards' })).body, 'error')).toBe('conflict')
    expect(field((await install({ source: 'modrinth', project: 'luckperms', extra: 1 })).body, 'error')).toBe('validation_failed')
    db.pluginJobs[1].startedMs -= 60_000
    const done = field((await callApi('GET', '/servers/1/plugins/jobs')).body, 'job') as PluginJob
    expect(done.state).toBe('done')
    expect(done.steps.every((s) => s.state === 'done')).toBe(true)
    const inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv.jars.map((j) => j.file)).toEqual(expect.arrayContaining(['Chunky-Bukkit-1.4.40.jar', 'ChunkyBorder-Bukkit-1.2.23.jar']))
    expect(inv.restart_needed).toBe(true) // survival runs and the panel changed its plugins
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.install', target: 'server:1', details: { project: 's86X568j', version: 'cb1223', file: 'ChunkyBorder-Bukkit-1.2.23.jar' } })
    const via = await install({ source: 'hangar', project: 'ViaBackwards' })
    expect((via.body as PluginJob).steps.map((s) => s.title)).toEqual(['ViaBackwards'])
    db.pluginJobs[1].startedMs -= 60_000
    // nothing to do: the requested project is installed (no version, or the installed one) -> 409 conflict
    expect(field((await install({ source: 'modrinth', project: 'chunkyborder' })).body, 'error')).toBe('conflict')
    expect(((await callApi('GET', '/servers/1/plugins/resolve?source=modrinth&project=luckperms')).body as PluginResolution).already.map((a) => a.file)).toEqual(['LuckPerms-Bukkit-5.5.71.jar'])
  })

  it('updates, toggles and removes jars', async () => {
    const me = loginAs('admin')
    const post = (path: string, body: unknown) => callApi('POST', `/servers/1/plugins/${path}`, { body, csrf: me.csrf_token })
    const updates = (await callApi('GET', '/servers/1/plugins/updates')).body as PluginUpdates
    expect(updates.updates).toEqual([
      { file: 'LuckPerms-Bukkit-5.5.71.jar', from: { id: 'lp5571', name: '5.5.71', channel: 'release' }, to: { id: 'lp5572', name: '5.5.72', channel: 'release' } },
    ])
    for (const refresh of ['true', '1']) {
      const refreshed = (await callApi('GET', `/servers/1/plugins/updates?refresh=${refresh}`)).body as PluginUpdates
      expect(refreshed.checked_at).toBeGreaterThan(updates.checked_at)
    }
    expect(field((await post('update', { files: ['OldTool.jar'] })).body, 'error')).toBe('validation_failed') // no update for a manual jar
    expect((await post('update', { files: [] })).status).toBe(202)
    expect(field((await post('toggle', { file: 'OldTool.jar', enabled: false })).body, 'error')).toBe('conflict')
    db.pluginJobs[1].startedMs -= 60_000
    let inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv.jars.find((j) => j.project?.slug === 'luckperms')).toMatchObject({ file: 'LuckPerms-Bukkit-5.5.72.jar', version: { name: '5.5.72' } })
    expect(inv.jars.map((j) => j.file)).not.toContain('LuckPerms-Bukkit-5.5.71.jar')
    expect(((await callApi('GET', '/servers/1/plugins/updates')).body as PluginUpdates).updates).toEqual([])
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.update', details: { file: 'LuckPerms-Bukkit-5.5.72.jar', from: '5.5.71', to: '5.5.72' } })

    expect((await post('toggle', { file: 'OldTool.jar', enabled: false })).status).toBe(204)
    inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    const old = inv.jars.find((j) => j.file === 'OldTool.jar.disabled')
    expect(old).toMatchObject({ enabled: false, meta: { name: 'OldTool' } })
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.toggle', details: { file: 'OldTool.jar', enabled: false } })
    expect((await post('toggle', { file: 'OldTool.jar', enabled: true })).status).toBe(404)
    expect((await post('toggle', { file: '../x.jar', enabled: true })).status).toBe(400)
    expect((await post('toggle', { file: 'server.properties', enabled: true })).status).toBe(400)
    // Enabling must not overwrite another jar with the target name (case-insensitive).
    if (!old) throw new Error('OldTool.jar.disabled missing')
    db.plugins[1].jars.push({ ...old, file: 'oldtool.JAR', enabled: true })
    expect(field((await post('toggle', { file: 'OldTool.jar.disabled', enabled: true })).body, 'error')).toBe('file_exists')

    expect((await post('delete', { file: 'OldTool.jar.disabled' })).status).toBe(204)
    expect(db.audit.at(-1)).toMatchObject({ action: 'plugin.remove', details: { file: 'OldTool.jar.disabled' } })
    inv = (await callApi('GET', '/servers/1/plugins')).body as PluginInventory
    expect(inv.jars.map((j) => j.file)).not.toContain('OldTool.jar.disabled')
    expect(inv.restart_needed).toBe(true)
    expect((await post('delete', { file: 'OldTool.jar.disabled' })).status).toBe(404)
  })

  it('requires server.plugins and serves icons only from the CDNs', async () => {
    loginAs('alice')
    expect((await callApi('GET', '/servers/1/plugins')).status).toBe(403)
    expect((await callApi('GET', `/servers/1/plugins/icon?url=${encodeURIComponent('https://cdn.modrinth.com/x.png')}`)).status).toBe(403)
    loginAs('admin')
    const icon = await fetch(`${window.location.origin}/api/servers/1/plugins/icon?url=${encodeURIComponent('https://cdn.modrinth.com/data/Vebnzrzj/icon.png')}`)
    expect(icon.status).toBe(200)
    expect(icon.headers.get('Content-Type')).toBe('image/png')
    expect([...new Uint8Array(await icon.arrayBuffer()).slice(0, 4)]).toEqual([0x89, 0x50, 0x4e, 0x47])
    expect((await callApi('GET', `/servers/1/plugins/icon?url=${encodeURIComponent('https://evil.example/x.png')}`)).status).toBe(400)
    expect((await callApi('GET', `/servers/1/plugins/icon?url=${encodeURIComponent('http://cdn.modrinth.com/x.png')}`)).status).toBe(400)
    // The icon route has no plugins_unsupported check (V3).
    expect((await fetch(`${window.location.origin}/api/servers/2/plugins/icon?url=${encodeURIComponent('https://cdn.modrinth.com/x.png')}`)).status).toBe(200)
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

  it('shows the seed only to admins and users with server.files', async () => {
    loginAs('alice')
    const list = (await callApi('GET', '/servers')).body as Server[]
    expect(list[0].permissions).not.toContain('server.files')
    expect(list[0].instance?.properties).not.toHaveProperty('seed')
    expect(((await callApi('GET', '/servers/1')).body as Server).instance?.properties).not.toHaveProperty('seed')
    loginAs('admin')
    expect(((await callApi('GET', '/servers/1')).body as Server).instance?.properties).toHaveProperty('seed')
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
    expect(await get()).toEqual({ base_domain: 'mc.example.com', curseforge_api_key_set: false, backup_timezone: 'Europe/Berlin', docker: 'ok', unknown_containers: [] })
    await callApi('PUT', '/servers/settings', { body: { curseforge_api_key: 'abc$123' }, csrf: me.csrf_token })
    expect(field(await get(), 'curseforge_api_key_set')).toBe(true)
    expect(JSON.stringify(await get())).not.toContain('abc$123')
    await callApi('PUT', '/servers/settings', { body: { base_domain: 'Play.Example.org' }, csrf: me.csrf_token })
    expect(await get()).toMatchObject({ base_domain: 'play.example.org', curseforge_api_key_set: true })
    await callApi('PUT', '/servers/settings', { body: { base_domain: null, curseforge_api_key: null }, csrf: me.csrf_token })
    expect(await get()).toMatchObject({ base_domain: null, curseforge_api_key_set: false })
    const bad = await callApi('PUT', '/servers/settings', { body: { base_domain: 'localhost' }, csrf: me.csrf_token })
    expect(field(bad.body, 'error')).toBe('validation_failed')
    await callApi('PUT', '/servers/settings', { body: { backup_timezone: 'America/New_York' }, csrf: me.csrf_token })
    expect(field(await get(), 'backup_timezone')).toBe('America/New_York')
    for (const tz of [null, '', 'Mars/Olympus', 'Local', '+01:00', 'europe/berlin']) {
      const res = await callApi('PUT', '/servers/settings', { body: { backup_timezone: tz }, csrf: me.csrf_token })
      expect(field(res.body, 'error')).toBe('validation_failed')
    }
  })

  it('reports the memory limits of the mock host and enforces them', async () => {
    const me = loginAs('admin', { sudo: true })
    const limits = (await callApi('GET', '/servers/memory')).body
    // (26624 − 1024) / 1.25 = 20480; three seeded servers with 4 + 2 + 4 GiB
    expect(limits).toMatchObject({ min_mib: 1024, max_mib: 20480, step_mib: 256, host_total_mib: 26624, allocated_mib: 10240 })
    expect(field(limits, 'recommended_mib')).toMatchObject({ paper: 4096, fabric: 6144 })
    const body = { name: 'huge', type: 'paper', version: '1.21.11', memory_mib: 20736, eula_accepted: true }
    const res = await callApi('POST', '/servers', { body, csrf: me.csrf_token })
    expect(field(res.body, 'error')).toBe('validation_failed')
    loginAs('alice')
    expect((await callApi('GET', '/servers/memory')).status).toBe(403)
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
      web_status: 'off',
      web_routes: [],
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

  it('publishes web routes like the panel: validated, sorted, pending then active; admin-only fields; 409 without base domain', async () => {
    const me = loginAs('admin', { sudo: true })
    const patch = (body: unknown) => callApi('PATCH', '/servers/2', { body, csrf: me.csrf_token })
    const invalid = [
      null,
      [{ path: 'Afk', port: 8200 }],
      [{ path: '-a', port: 8200 }],
      [{ path: 'a'.repeat(33), port: 8200 }],
      [{ path: '', port: 25565 }],
      [{ path: '', port: 1023 }],
      [{ path: '', port: 8100 }, { path: 'afk-smoke', port: 8100 }],
      [{ path: '', port: 8100 }, { path: '', port: 8200 }],
      Array.from({ length: 9 }, (_, i) => ({ path: `p${i}`, port: 8100 + i })),
    ]
    for (const web_routes of invalid) {
      const res = await patch({ web_routes })
      expect(res.status, JSON.stringify(web_routes)).toBe(400)
      expect(field(res.body, 'error')).toBe('validation_failed')
    }
    expect(field((await patch({ web_routes: null })).body, 'message')).toBe('web_routes: must not be null')
    // like the panel's strict decoding: the read-only url (or any other field) in an entry is a bad request
    const withUrl = await patch({ web_routes: [{ path: '', port: 8100, url: 'https://x.example/' }] })
    expect(withUrl.status).toBe(400)
    expect(field(withUrl.body, 'error')).toBe('bad_request')
    expect(db.audit.some((e) => e.action === 'server.web_routes_update')).toBe(false)

    // an unsorted list is accepted and stored sorted, root first
    const published = await patch({ web_routes: [{ path: 'afk-smoke', port: 8200 }, { path: '', port: 8100 }] })
    expect(published.status).toBe(200)
    expect(published.body).toMatchObject({
      web_status: 'pending',
      web_url: 'https://creative.mc.example.com',
      web_routes: [
        { path: '', port: 8100, url: 'https://creative.mc.example.com/' },
        { path: 'afk-smoke', port: 8200, url: 'https://creative.mc.example.com/afk-smoke/' },
      ],
      status: { restart_required: false },
    })
    expect(db.audit.at(-1)).toMatchObject({
      action: 'server.web_routes_update',
      target: 'server:2',
      details: { name: 'creative', old: [], new: [{ path: '', port: 8100 }, { path: 'afk-smoke', port: 8200 }] },
    })
    // the same list again writes no second entry
    const audits = db.audit.length
    await patch({ web_routes: [{ path: '', port: 8100 }, { path: 'afk-smoke', port: 8200 }] })
    expect(db.audit.length).toBe(audits)
    expect((await callApi('GET', '/servers/2')).body).toMatchObject({ web_status: 'active' })

    loginAs('alice')
    const viewer = (await callApi('GET', '/servers/2')).body as Record<string, unknown>
    expect(viewer).toMatchObject({ web_status: 'active', web_url: 'https://creative.mc.example.com' })
    expect(viewer).not.toHaveProperty('web_routes')
    expect(viewer).not.toHaveProperty('web_error')

    // without a root route there is no web_url, the routes are still listed
    const admin = loginAs('admin', { sudo: true })
    const noRoot = await callApi('PATCH', '/servers/2', { body: { web_routes: [{ path: 'afk-smoke', port: 8200 }] }, csrf: admin.csrf_token })
    expect(noRoot.body).toMatchObject({ web_status: 'pending', web_routes: [{ path: 'afk-smoke', port: 8200, url: 'https://creative.mc.example.com/afk-smoke/' }] })
    expect(noRoot.body).not.toHaveProperty('web_url')

    // always listed for admins, also for a server without an instance
    expect((await callApi('GET', '/servers/3')).body).toMatchObject({ web_status: 'off', web_routes: [] })

    const off = await callApi('PATCH', '/servers/2', { body: { web_routes: [] }, csrf: admin.csrf_token })
    expect(off.body).toMatchObject({ web_status: 'off', web_routes: [] })
    expect(off.body).not.toHaveProperty('web_url')

    db.serverSettings.base_domain = null
    const noDomain = await callApi('PATCH', '/servers/2', { body: { web_routes: [{ path: '', port: 8100 }] }, csrf: admin.csrf_token })
    expect(noDomain.status).toBe(409)
    expect(noDomain.body).toMatchObject({ error: 'conflict', message: 'base domain required' })
    // [] needs no base domain
    expect((await callApi('PATCH', '/servers/2', { body: { web_routes: [] }, csrf: admin.csrf_token })).status).toBe(200)
    // admins still see the stored routes (without url) while the base domain is missing
    const stored = (await callApi('GET', '/servers/1')).body
    expect(stored).toMatchObject({ web_status: 'off', web_routes: [{ path: '', port: 8100 }] })
    expect(stored).not.toHaveProperty('web_url')
    expect((stored as { web_routes: object[] }).web_routes[0]).not.toHaveProperty('url')
    // a new base domain changes the route list: pending first, active after the agent confirmed
    await callApi('PUT', '/servers/settings', { body: { base_domain: 'play.example.org' }, csrf: admin.csrf_token })
    const after = (await callApi('PATCH', '/servers/1', { body: {}, csrf: admin.csrf_token })).body
    expect(after).toMatchObject({ web_status: 'pending', web_url: 'https://survival.play.example.org', web_routes: [{ url: 'https://survival.play.example.org/' }] })
    expect((await callApi('GET', '/servers/1')).body).toMatchObject({ web_status: 'active' })
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

  it('lists backups and runs a manual backup as a job, one per server', async () => {
    const me = loginAs('admin')
    const overview = (await callApi('GET', '/servers/1/backups')).body as BackupOverview
    expect(overview.available).toBe(true)
    expect(overview.snapshots.map((s) => s.kind)).toEqual(['scheduled', 'manual', 'scheduled', 'pre-restore', 'scheduled', 'scheduled'])
    expect(overview.snapshots.filter((s) => s.pinned)).toHaveLength(1)
    expect(overview.snapshots[1].note).toBe('vor Update auf 1.21.11')
    expect(overview.schedule).toMatchObject({ mode: 'daily', time_of_day: 240, keep_last: 24, timezone: 'Europe/Berlin', last_status: 'ok' })
    expect(overview.job).toBeNull()

    const create = (body: unknown) => callApi('POST', '/servers/1/backups', { body, csrf: me.csrf_token })
    const started = await create({ note: 'vor dem Umbau' })
    expect(started.status).toBe(202)
    expect(field(started.body, 'job')).toMatchObject({ kind: 'backup', trigger: 'manual', state: 'queued', step: 'queued', queue_position: 1 })
    expect(field((await create({})).body, 'error')).toBe('backup_running')
    db.backupJobs[1].startedMs -= 60_000
    const job = field((await callApi('GET', '/servers/1/backups/job')).body, 'job') as BackupJob
    expect(job).toMatchObject({ state: 'done', step: 'done', queue_position: 0 })
    const after = (await callApi('GET', '/servers/1/backups')).body as BackupOverview
    expect(after.snapshots[0]).toMatchObject({ kind: 'manual', note: 'vor dem Umbau', id: job.snapshot })
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.create', target: 'server:1', details: { note: 'vor dem Umbau', snapshot: job.snapshot } })
    const again = await create({})
    expect(again.status).toBe(429)
    expect(field(again.body, 'error')).toBe('rate_limited')

    expect(field((await create({ note: 'a\nb' })).body, 'error')).toBe('validation_failed')
    expect(field((await create({ note: 'x'.repeat(65) })).body, 'error')).toBe('validation_failed')
    expect(field((await create({ extra: 1 })).body, 'error')).toBe('bad_request')
  })

  it('pins scheduled snapshots only and pins, restores or deletes with sudo', async () => {
    let me = loginAs('admin')
    const list = async () => ((await callApi('GET', '/servers/1/backups')).body as BackupOverview).snapshots
    const [scheduled, manual] = await list()
    const pinNoSudo = await callApi('POST', `/servers/1/backups/${scheduled.id}/pin`, { body: { pinned: true }, csrf: me.csrf_token })
    expect(field(pinNoSudo.body, 'error')).toBe('sudo_required')
    const restore = (id: string) => callApi('POST', `/servers/1/backups/${id}/restore`, { csrf: me.csrf_token })
    expect(field((await restore(manual.id)).body, 'error')).toBe('sudo_required')
    me = loginAs('admin', { sudo: true })
    const pin = await callApi('POST', `/servers/1/backups/${scheduled.id}/pin`, { body: { pinned: true }, csrf: me.csrf_token })
    const pinned = field(pin.body, 'snapshot') as BackupSnapshot
    expect(pinned.pinned).toBe(true)
    expect(pinned.id).not.toBe(scheduled.id)
    expect(pinned.id).toMatch(/^[0-9a-f]{64}$/)
    expect((await list())[0].id).toBe(pinned.id)
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.pin', details: { snapshot: scheduled.id, new_snapshot: pinned.id } })
    const pinManual = await callApi('POST', `/servers/1/backups/${manual.id}/pin`, { body: { pinned: true }, csrf: me.csrf_token })
    expect(field(pinManual.body, 'error')).toBe('validation_failed')
    expect((await callApi('POST', `/servers/1/backups/${'0'.repeat(64)}/pin`, { body: { pinned: true }, csrf: me.csrf_token })).status).toBe(404)
    expect((await callApi('POST', '/servers/1/backups/nope/pin', { body: { pinned: true }, csrf: me.csrf_token })).status).toBe(400)

    const started = await restore(manual.id)
    expect(started.status).toBe(202)
    expect(field(started.body, 'job')).toMatchObject({ kind: 'restore', snapshot: manual.id })
    db.backupJobs[1].startedMs -= 60_000
    expect(field((await callApi('GET', '/servers/1/backups/job')).body, 'job')).toMatchObject({ state: 'done' })
    expect((await list())[0].kind).toBe('pre-restore')
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.restore', details: { snapshot: manual.id, time: manual.time, kind: 'manual' } })

    const del = await callApi('DELETE', `/servers/1/backups/${manual.id}`, { csrf: me.csrf_token })
    expect(del.status).toBe(202)
    db.backupJobs[1].startedMs -= 60_000
    await callApi('GET', '/servers/1/backups/job')
    expect((await list()).some((s) => s.id === manual.id)).toBe(false)
  })

  it('validates schedules, reports unavailable backups and downloads a zip', async () => {
    let me = loginAs('admin')
    const put = (id: number, body: unknown) => callApi('PUT', `/servers/${id}/backups/schedule`, { body, csrf: me.csrf_token })
    const weekly = { mode: 'weekly', interval_hours: 24, time_of_day: 600, weekday: 6, keep_last: 0, keep_daily: 0, keep_weekly: 8, keep_monthly: 0 }
    expect(field((await put(2, weekly)).body, 'error')).toBe('sudo_required')
    me = loginAs('admin', { sudo: true })
    const ok = await put(2, weekly)
    expect(ok.status).toBe(200)
    const schedule = field(ok.body, 'schedule') as BackupOverview['schedule']
    expect(schedule).toMatchObject({ mode: 'weekly', weekday: 6, keep_weekly: 8 })
    expect(schedule.next_run_at).toBeGreaterThan(Date.now())
    expect(field((await put(2, { ...weekly, keep_weekly: 0 })).body, 'error')).toBe('validation_failed')
    expect(field((await put(2, { ...weekly, time_of_day: 1440 })).body, 'error')).toBe('validation_failed')
    expect(field((await put(2, { ...weekly, mode: 'hourly' })).body, 'error')).toBe('validation_failed')
    expect(field((await put(2, { ...weekly, extra: true })).body, 'error')).toBe('bad_request')
    expect(field((await put(2, { ...weekly, mode: 'off', keep_weekly: 0 })).body, 'schedule')).toMatchObject({ mode: 'off', next_run_at: null })
    expect(db.audit.at(-1)).toMatchObject({
      action: 'backup.schedule_update',
      target: 'server:2',
      details: { from: { mode: 'weekly', keep_weekly: 8 }, to: { mode: 'off', keep_weekly: 0 } },
    })
    // interval: N hours after the last run (two days ago for creative), else N hours from now
    const interval = field((await put(2, { ...weekly, mode: 'interval', interval_hours: 6 })).body, 'schedule') as BackupOverview['schedule']
    expect(interval.next_run_at).toBeGreaterThan(Date.now() + 5.9 * 3_600_000)
    expect(interval.next_run_at).toBeLessThan(Date.now() + 6.1 * 3_600_000)

    // daily/weekly in the panel time zone, not the browser's: Kathmandu is UTC+05:45 all year
    expect(Intl.DateTimeFormat().resolvedOptions().timeZone).not.toBe('Asia/Kathmandu')
    db.serverSettings.backup_timezone = 'Asia/Kathmandu'
    const offset = (5 * 60 + 45) * 60_000
    const daily = field((await put(2, { ...weekly, mode: 'daily', time_of_day: 240 })).body, 'schedule') as BackupOverview['schedule']
    expect(daily.timezone).toBe('Asia/Kathmandu')
    const nextDaily = daily.next_run_at ?? 0
    expect(nextDaily).toBeGreaterThan(Date.now())
    expect(nextDaily).toBeLessThanOrEqual(Date.now() + 86_400_000)
    expect((nextDaily + offset) % 86_400_000).toBe(240 * 60_000) // 04:00 in Kathmandu
    const wk = field((await put(2, { ...weekly, time_of_day: 600, weekday: 3 })).body, 'schedule') as BackupOverview['schedule']
    const nextWeekly = wk.next_run_at ?? 0
    expect(nextWeekly).toBeGreaterThan(Date.now())
    expect(nextWeekly).toBeLessThanOrEqual(Date.now() + 7 * 86_400_000)
    expect((nextWeekly + offset) % 86_400_000).toBe(600 * 60_000) // 10:00 in Kathmandu
    expect(new Date(nextWeekly + offset).getUTCDay()).toBe(3) // a Wednesday there
    db.serverSettings.backup_timezone = 'Europe/Berlin'

    expect(((await callApi('GET', '/servers/4/backups')).body as BackupOverview).available).toBe(false)
    expect(field((await callApi('POST', '/servers/4/backups', { body: {}, csrf: me.csrf_token })).body, 'error')).toBe('backups_unavailable')
    expect(field((await callApi('POST', '/servers/3/backups', { body: {}, csrf: me.csrf_token })).body, 'error')).toBe('conflict')
    expect(((await callApi('GET', '/servers/3/backups')).body as BackupOverview).snapshots).toEqual([])

    const snap = ((await callApi('GET', '/servers/1/backups')).body as BackupOverview).snapshots[0]
    const res = await fetch(`${window.location.origin}/api/servers/1/backups/${snap.id}/download`)
    expect(res.status).toBe(200)
    expect(res.headers.get('Content-Type')).toBe('application/zip')
    expect(res.headers.get('Content-Disposition')).toMatch(/^attachment; filename="survival-\d{8}-\d{6}\.zip"$/)
    expect(db.audit.at(-1)).toMatchObject({ action: 'backup.download', details: { snapshot: snap.id } })
    // the file name uses the panel time zone: 02:30 UTC is 04:30 in Berlin (CEST)
    db.backups[1].snapshots[0].time = Date.UTC(2026, 9, 12, 2, 30, 0)
    const named = await fetch(`${window.location.origin}/api/servers/1/backups/${snap.id}/download`)
    expect(named.headers.get('Content-Disposition')).toBe('attachment; filename="survival-20261012-043000.zip"')
  })

  it('requires server.backups and purges backups with the deleted server', async () => {
    loginAs('bob')
    expect((await callApi('GET', '/servers/1/backups')).status).toBe(403)
    const me = loginAs('admin', { sudo: true })
    expect(db.backups[1]).toBeDefined()
    expect((await callApi('DELETE', '/servers/1?purge_backups=yes', { csrf: me.csrf_token })).status).toBe(400)
    // a restore holds the server: no delete until it is done
    const snap = ((await callApi('GET', '/servers/1/backups')).body as BackupOverview).snapshots[0]
    expect((await callApi('POST', `/servers/1/backups/${snap.id}/restore`, { csrf: me.csrf_token })).status).toBe(202)
    const held = await callApi('DELETE', '/servers/1?purge_backups=1', { csrf: me.csrf_token })
    expect(held.status).toBe(409)
    expect(field(held.body, 'error')).toBe('conflict')
    db.backupJobs[1].startedMs -= 60_000
    // an empty value means no purge (like the panel)
    expect((await callApi('DELETE', '/servers/2?purge_backups=', { csrf: me.csrf_token })).status).toBe(204)
    expect(db.backups[2]).toBeDefined()
    expect((await callApi('DELETE', '/servers/1?purge_backups=1', { csrf: me.csrf_token })).status).toBe(204)
    expect(db.backups[1]).toBeUndefined()
    expect(db.audit.at(-1)).toMatchObject({ action: 'server.delete', details: { backups_purged: true } })
  })

  it('serves deterministic monitoring series and live values', async () => {
    loginAs('admin')
    const s = (await callApi('GET', '/servers/1/metrics?range=24h')).body as MetricsSeries
    expect(s.range).toBe('24h')
    expect(s.step_ms).toBe(300_000)
    expect(s.series.players).toHaveLength(288)
    expect(s.series.tps.every(([, avg]) => avg > 0 && avg <= 20)).toBe(true)
    expect(s.series.players.every(([, avg]) => avg >= 0 && avg <= 12)).toBe(true)
    const again = (await callApi('GET', '/servers/1/metrics?range=24h')).body as MetricsSeries
    expect(again.series.players.map((p) => p[1])).toEqual(s.series.players.map((p) => p[1]))
    expect(((await callApi('GET', '/servers/1/metrics?range=30d')).body as MetricsSeries).series.players).toHaveLength(720)
    expect(field((await callApi('GET', '/servers/1/metrics?range=2h')).body, 'error')).toBe('validation_failed')
    expect(Object.values(((await callApi('GET', '/servers/3/metrics?range=1h')).body as MetricsSeries).series).every((v) => v.length === 0)).toBe(true)
    const live = (await callApi('GET', '/servers/1/metrics/live')).body as LiveMetrics
    expect(live).toMatchObject({ up: true, tps_source: 'tick_query' })
    expect(live.player_names.length).toBeGreaterThan(0)
    const modded = (await callApi('GET', '/servers/4/metrics/live')).body as LiveMetrics
    expect(modded).toMatchObject({ up: false, tps_source: 'none' })
    const ov = (await callApi('GET', '/monitoring/overview')).body as MonitoringOverview
    expect(modded.players_max).not.toBeNull()
    expect(modded.players_max).toBe(ov.servers.find((x) => x.id === 4)?.players_max)
    expect(((await callApi('GET', '/servers/2/metrics/live')).body as LiveMetrics)).toMatchObject({ up: null, tps_source: 'unknown' })
  })

  it('filters the overview and hides host values from users', async () => {
    loginAs('admin')
    const ov = (await callApi('GET', '/monitoring/overview')).body as MonitoringOverview
    expect(ov.servers.map((s) => s.id).sort()).toEqual([1, 2, 3, 4])
    expect(ov.servers.find((s) => s.id === 4)?.alerts).toEqual(['server.down'])
    expect(ov.host?.alerts).toEqual([])
    db.monitoring.diskLow = true
    expect(((await callApi('GET', '/monitoring/overview')).body as MonitoringOverview).host?.alerts).toEqual(['host.disk_low'])
    db.monitoring.diskLow = false
    loginAs('alice')
    const user = (await callApi('GET', '/monitoring/overview')).body as MonitoringOverview
    expect(user.host).toBeNull()
    expect(user.servers.map((s) => s.id).sort()).toEqual([1, 2])
    expect((await callApi('GET', '/monitoring/host?range=1h')).status).toBe(403)
  })

  it('saves monitoring settings with sudo and validates them', async () => {
    const plain = loginAs('admin')
    const body = { disk_min_percent: 20, disk_min_bytes: 5 * 1024 ** 3, down_after_min: 2, crash_restarts: 4, crash_window_min: 15 }
    expect(field((await callApi('PUT', '/monitoring/settings', { body, csrf: plain.csrf_token })).body, 'error')).toBe('sudo_required')
    const me = loginAs('admin', { sudo: true })
    const saved = (await callApi('PUT', '/monitoring/settings', { body, csrf: me.csrf_token })).body as MonitoringSettings
    expect(saved).toMatchObject(body)
    expect(field((await callApi('PUT', '/monitoring/settings', { body: { ...body, crash_restarts: 1 }, csrf: me.csrf_token })).body, 'error')).toBe('validation_failed')
    const settings = (await callApi('GET', '/monitoring/settings')).body as MonitoringSettings
    expect(settings.alerts.map((a) => a.kind)).toEqual(['server.down'])
    expect((await callApi('PUT', '/servers/4/alerts', { body: { down_alerts: false }, csrf: me.csrf_token })).status).toBe(204)
    expect(field((await callApi('GET', '/servers/4')).body, 'down_alerts')).toBe(false)
    expect(((await callApi('GET', '/monitoring/overview')).body as MonitoringOverview).servers.find((s) => s.id === 4)?.alerts).toEqual([])
    db.monitoring = defaultMonitoring()
  })
})
