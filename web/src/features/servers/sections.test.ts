import { describe, expect, it } from 'vitest'
import type { InstanceConfig, Permission, Server } from '@/api/types'
import { sectionForSwitch, sectionFromPath, sectionHref, sectionLabelKey, serverSections } from './sections'

function instance(type: InstanceConfig['type']): InstanceConfig {
  return { type, version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: {} as InstanceConfig['properties'] }
}
function srv(perms: Permission[], type: InstanceConfig['type'] | null = 'paper'): Pick<Server, 'permissions' | 'instance'> {
  return { permissions: perms, instance: type ? instance(type) : null }
}

describe('serverSections', () => {
  it('gives view-only users overview and monitoring', () => {
    expect(serverSections(srv(['server.view']), false)).toEqual(['overview', 'monitoring'])
  })
  it('adds console and players with server.console', () => {
    expect(serverSections(srv(['server.view', 'server.console']), false)).toEqual(['overview', 'monitoring', 'console', 'players'])
  })
  it('adds files with server.files', () => {
    expect(serverSections(srv(['server.view', 'server.files']), false)).toEqual(['overview', 'monitoring', 'files'])
  })
  it('adds plugins only for plugin/mod types with server.plugins', () => {
    expect(serverSections(srv(['server.view', 'server.plugins'], 'paper'), false)).toContain('plugins')
    expect(serverSections(srv(['server.view', 'server.plugins'], 'purpur'), false)).toContain('plugins')
    expect(serverSections(srv(['server.view', 'server.plugins'], 'fabric'), false)).toContain('plugins')
    expect(serverSections(srv(['server.view', 'server.plugins'], 'neoforge'), false)).toContain('plugins')
    expect(serverSections(srv(['server.view', 'server.plugins'], 'vanilla'), false)).not.toContain('plugins')
    expect(serverSections(srv(['server.view'], 'paper'), false)).not.toContain('plugins')
  })
  it('adds backups on every type with server.backups', () => {
    expect(serverSections(srv(['server.view', 'server.backups'], 'vanilla'), false)).toEqual(['overview', 'monitoring', 'backups'])
  })
  it('ignores server.power for sections', () => {
    expect(serverSections(srv(['server.view', 'server.power']), false)).toEqual(['overview', 'monitoring'])
  })
  it('adds settings for admins only, in fixed order', () => {
    const all: Permission[] = ['server.view', 'server.power', 'server.console', 'server.files', 'server.plugins', 'server.backups']
    expect(serverSections(srv(all), true)).toEqual(['overview', 'monitoring', 'console', 'players', 'files', 'plugins', 'backups', 'settings'])
    expect(serverSections(srv(all), false)).not.toContain('settings')
    expect(serverSections(srv([...all].reverse()), true)).toEqual(serverSections(srv(all), true))
  })
  it('gives a server without instance only the overview', () => {
    expect(serverSections(srv(['server.view', 'server.console', 'server.files'], null), true)).toEqual(['overview'])
  })
})

describe('sectionLabelKey', () => {
  it('names plugins "Mods" on mod servers', () => {
    expect(sectionLabelKey('plugins', srv([], 'neoforge'))).toBe('servers.detail.tabs.mods')
    expect(sectionLabelKey('plugins', srv([], 'purpur'))).toBe('servers.detail.tabs.plugins')
    expect(sectionLabelKey('console', srv([]))).toBe('servers.detail.tabs.console')
  })
})

describe('sectionHref / sectionForSwitch / sectionFromPath', () => {
  it('maps sections to route paths', () => {
    expect(sectionHref('overview')).toBe('/servers/$id')
    expect(sectionHref('files')).toBe('/servers/$id/files')
  })
  it('keeps an allowed section and falls back to overview otherwise', () => {
    expect(sectionForSwitch('files', srv(['server.view', 'server.files']), false)).toBe('files')
    expect(sectionForSwitch('files', srv(['server.view']), false)).toBe('overview')
    expect(sectionForSwitch('plugins', srv(['server.view', 'server.plugins'], 'vanilla'), true)).toBe('overview')
  })
  it('reads the section from a pathname', () => {
    expect(sectionFromPath('/servers/1')).toBe('overview')
    expect(sectionFromPath('/servers/1/')).toBe('overview')
    expect(sectionFromPath('/servers/1/console')).toBe('console')
    expect(sectionFromPath('/servers/1/bogus')).toBe('overview')
    expect(sectionFromPath('/profile')).toBe('overview')
  })
})
