import { describe, expect, it } from 'vitest'
import type { InstalledJar, PluginJob, PluginSearchHit, PluginVersion } from '@/api/types'
import { nextOffset } from './api'
import {
  canInstall,
  defaultVersion,
  externalHref,
  iconSrc,
  isJobRunning,
  jarName,
  jarVersion,
  jobProgress,
  pluginKindOf,
  sourcesFor,
  uniqueHits,
  updatesByFile,
} from './model'

const base: InstalledJar = {
  file: 'Foo-1.0.jar',
  enabled: true,
  size: 10,
  mtime: 0,
  source: 'manual',
  project: null,
  version: null,
  meta: null,
  installed_by_panel: false,
}

function v(id: string, channel: PluginVersion['channel'] = 'release', downloadable = true): PluginVersion {
  return { id, name: id, channel, published_at: 0, downloadable, external_url: downloadable ? null : 'https://example.org/x.jar' }
}

describe('pluginKindOf', () => {
  it.each([
    ['paper', 'plugin'],
    ['purpur', 'plugin'],
    ['fabric', 'mod'],
    ['neoforge', 'mod'],
    ['vanilla', null],
    ['modrinth', null],
    ['curseforge', null],
  ] as const)('%s → %s', (type, kind) => {
    expect(pluginKindOf(type)).toBe(kind)
  })

  it('offers Hangar only for plugins', () => {
    expect(sourcesFor('plugin')).toEqual(['modrinth', 'hangar'])
    expect(sourcesFor('mod')).toEqual(['modrinth'])
  })
})

describe('jarName and jarVersion', () => {
  it('prefers the project title, then the jar metadata, then the file name', () => {
    const project = { id: 'p', slug: 'foo', title: 'Foo Plugin', page_url: 'https://modrinth.com/plugin/foo', icon_url: null }
    const meta = { kind: 'plugin' as const, id: '', name: 'FooMeta', version: '0.9' }
    expect(jarName({ ...base, project, meta })).toBe('Foo Plugin')
    expect(jarName({ ...base, project: { ...project, title: '  ' }, meta })).toBe('FooMeta')
    expect(jarName({ ...base, meta })).toBe('FooMeta')
    expect(jarName({ ...base, meta: { ...meta, name: '' } })).toBe('Foo-1.0.jar')
    expect(jarName(base)).toBe('Foo-1.0.jar')
  })

  it('prefers the matched version over the jar metadata', () => {
    const meta = { kind: 'plugin' as const, id: '', name: 'Foo', version: '0.9' }
    expect(jarVersion({ ...base, meta, version: { id: 'v1', name: '1.0', channel: 'release' } })).toBe('1.0')
    expect(jarVersion({ ...base, meta })).toBe('0.9')
    expect(jarVersion(base)).toBeNull()
  })
})

describe('defaultVersion', () => {
  it('takes the newest downloadable release', () => {
    expect(defaultVersion([v('3-beta', 'beta'), v('2'), v('1')])).toBe('2')
    expect(defaultVersion([v('3', 'release', false), v('2')])).toBe('2')
  })
  it('falls back to the newest downloadable pre-release', () => {
    expect(defaultVersion([v('3-alpha', 'alpha'), v('2-beta', 'beta')])).toBe('3-alpha')
  })
  it('keeps a preferred version only if it can be downloaded', () => {
    expect(defaultVersion([v('3-beta', 'beta'), v('2')], '3-beta')).toBe('3-beta')
    expect(defaultVersion([v('3', 'release', false), v('2')], '3')).toBe('2')
    expect(defaultVersion([v('2')], 'gone')).toBe('2')
  })
  it('returns null without a downloadable version', () => {
    expect(defaultVersion([v('1', 'release', false)])).toBeNull()
    expect(defaultVersion([])).toBeNull()
  })
})

describe('jobs and plans', () => {
  const job: PluginJob = {
    id: 'j',
    kind: 'install',
    state: 'running',
    started_at: 1,
    finished_at: 0,
    steps: [
      { title: 'A', file: 'a.jar', state: 'done', error_code: null, error: '' },
      { title: 'B', file: 'b.jar', state: 'failed', error_code: 'hash_mismatch', error: 'x' },
      { title: 'C', file: 'c.jar', state: 'skipped', error_code: null, error: '' },
      { title: 'D', file: 'd.jar', state: 'downloading', error_code: null, error: '' },
      { title: 'E', file: 'e.jar', state: 'pending', error_code: null, error: '' },
    ],
  }

  it('counts finished steps', () => {
    expect(jobProgress(job)).toEqual({ finished: 3, total: 5 })
  })
  it('knows a running job', () => {
    expect(isJobRunning({ job })).toBe(true)
    expect(isJobRunning({ job: { ...job, state: 'done' } })).toBe(false)
    expect(isJobRunning({ job: null })).toBe(false)
    expect(isJobRunning(undefined)).toBe(false)
  })
  it('allows installing only a non-empty plan without incompatibilities', () => {
    const entry = {
      source: 'modrinth' as const,
      project_id: 'p',
      slug: 'p',
      title: 'P',
      version_id: 'v',
      version_name: '1',
      channel: 'release' as const,
      filename: 'p.jar',
      size: 1,
      required_by: null,
    }
    expect(canInstall({ install: [entry], already: [], external: [], incompatible: [] })).toBe(true)
    expect(canInstall({ install: [], already: [], external: [], incompatible: [] })).toBe(false)
    expect(canInstall({ install: [entry], already: [], external: [], incompatible: [{ title: 'X', reason: 'client_only' }] })).toBe(false)
  })
  it('indexes updates by file', () => {
    const update = { file: 'a.jar', from: { id: '1', name: '1', channel: 'release' as const }, to: { id: '2', name: '2', channel: 'release' as const } }
    expect(updatesByFile([update]).get('a.jar')).toBe(update)
    expect(updatesByFile(undefined).size).toBe(0)
  })
})

describe('search pages', () => {
  const hit = (project_id: string): PluginSearchHit => ({
    source: 'modrinth',
    project_id,
    slug: project_id,
    title: project_id,
    description: '',
    author: 'a',
    downloads: 0,
    icon_url: null,
    page_url: `https://modrinth.com/plugin/${project_id}`,
    installed: false,
  })

  it('computes the next offset and stops at the end or at 1000', () => {
    expect(nextOffset({ hits: Array.from({ length: 20 }, (_, i) => hit(`p${i}`)), total: 45, offset: 0 })).toBe(20)
    expect(nextOffset({ hits: [hit('a')], total: 21, offset: 20 })).toBeUndefined()
    expect(nextOffset({ hits: [], total: 50, offset: 40 })).toBeUndefined()
    expect(nextOffset({ hits: Array.from({ length: 20 }, (_, i) => hit(`p${i}`)), total: 5000, offset: 980 })).toBe(1000)
    expect(nextOffset({ hits: Array.from({ length: 20 }, (_, i) => hit(`p${i}`)), total: 5000, offset: 1000 })).toBeUndefined()
  })

  it('drops hits a later page repeats', () => {
    expect(uniqueHits([hit('a'), hit('b'), hit('a')]).map((h) => h.project_id)).toEqual(['a', 'b'])
  })
})

describe('URL filters', () => {
  it('only lets proxied icons through', () => {
    expect(iconSrc('/api/servers/1/plugins/icon?url=https%3A%2F%2Fcdn.modrinth.com%2Fa.png')).toBe(
      '/api/servers/1/plugins/icon?url=https%3A%2F%2Fcdn.modrinth.com%2Fa.png',
    )
    expect(iconSrc('https://cdn.modrinth.com/a.png')).toBeNull()
    expect(iconSrc('//evil.example/a.png')).toBeNull()
    expect(iconSrc('/api/servers/1/files/download?path=x')).toBeNull()
    expect(iconSrc(null)).toBeNull()
  })

  it('only links http(s) URLs', () => {
    expect(externalHref('https://modrinth.com/plugin/luckperms')).toBe('https://modrinth.com/plugin/luckperms')
    expect(externalHref('http://example.org/x.jar')).toBe('http://example.org/x.jar')
    expect(externalHref('javascript:alert(1)')).toBeNull()
    expect(externalHref('data:text/html,hi')).toBeNull()
    expect(externalHref('not a url')).toBeNull()
    expect(externalHref(null)).toBeNull()
  })
})
