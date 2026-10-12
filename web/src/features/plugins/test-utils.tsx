import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import type {
  InstalledJar,
  PluginChannel,
  PluginInventory,
  PluginJob,
  PluginJobStep,
  PluginPlanEntry,
  PluginSearchHit,
  PluginUpdate,
  PluginVersion,
} from '@/api/types'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

const slug = (title: string) => title.toLowerCase().replace(/[^a-z0-9]+/g, '-')

export function jar(file: string, over: Partial<InstalledJar> = {}): InstalledJar {
  return {
    file,
    enabled: !file.toLowerCase().endsWith('.disabled'),
    size: 1024,
    mtime: Date.now() - 60_000,
    source: 'manual',
    project: null,
    version: null,
    meta: null,
    installed_by_panel: false,
    ...over,
  }
}

export function modrinthJar(file: string, title: string, version: string, over: Partial<InstalledJar> = {}): InstalledJar {
  return jar(file, {
    source: 'modrinth',
    project: { id: `p-${slug(title)}`, slug: slug(title), title, page_url: `https://modrinth.com/plugin/${slug(title)}`, icon_url: null },
    version: { id: `v-${version}`, name: version, channel: 'release' },
    installed_by_panel: true,
    ...over,
  })
}

export function hangarJar(file: string, title: string, version: string, channel: PluginChannel = 'release'): InstalledJar {
  return jar(file, {
    source: 'hangar',
    project: { id: '42', slug: title, title, page_url: `https://hangar.papermc.io/ViaVersion/${title}`, icon_url: null },
    version: { id: version, name: version, channel },
    installed_by_panel: true,
  })
}

export function inventory(over: Partial<PluginInventory> = {}): PluginInventory {
  return {
    kind: 'plugin',
    dir: 'plugins',
    game_version: '1.21.11',
    sources: ['modrinth', 'hangar'],
    jars: [],
    truncated: false,
    restart_needed: false,
    source_errors: [],
    ...over,
  }
}

/** Replaces the mock inventory for one test; the update list is empty unless a test overrides it afterwards. */
export function mockInventory(inv: PluginInventory) {
  server.use(
    http.get('*/api/servers/:id/plugins', () => HttpResponse.json(inv)),
    http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [], checked_at: Date.now() })),
  )
}

export function hit(title: string, over: Partial<PluginSearchHit> = {}): PluginSearchHit {
  return {
    source: 'modrinth',
    project_id: `id-${slug(title)}`,
    slug: slug(title),
    title,
    description: `${title} description`,
    author: 'someone',
    downloads: 1234,
    icon_url: null,
    page_url: `https://modrinth.com/plugin/${slug(title)}`,
    installed: false,
    ...over,
  }
}

export function version(id: string, name: string, channel: PluginChannel = 'release', over: Partial<PluginVersion> = {}): PluginVersion {
  return { id, name, channel, published_at: Date.UTC(2026, 9, 1), downloadable: true, external_url: null, ...over }
}

export function planEntry(title: string, versionName: string, requiredBy: string | null = null): PluginPlanEntry {
  return {
    source: 'modrinth',
    project_id: `id-${slug(title)}`,
    slug: slug(title),
    title,
    version_id: `v-${versionName}`,
    version_name: versionName,
    channel: 'release',
    filename: `${title}-${versionName}.jar`,
    size: 100 * 1024,
    required_by: requiredBy,
  }
}

export function step(title: string, file: string, state: PluginJobStep['state'], errorCode: PluginJobStep['error_code'] = null): PluginJobStep {
  return { title, file, state, error_code: errorCode, error: errorCode ? 'details from the panel' : '' }
}

export function job(kind: PluginJob['kind'], state: PluginJob['state'], steps: PluginJobStep[]): PluginJob {
  return { id: 'job-1', kind, state, steps, started_at: Date.now() - 1000, finished_at: state === 'running' ? 0 : Date.now() }
}

export function update(file: string, from: string, to: string): PluginUpdate {
  return { file, from: { id: `v-${from}`, name: from, channel: 'release' }, to: { id: `v-${to}`, name: to, channel: 'release' } }
}

export async function openPlugins(serverId = 1, username = 'admin', tab: 'Plugins' | 'Mods' = 'Plugins') {
  const user = userEvent.setup()
  const { queryClient } = await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: tab }))
  const region = await screen.findByRole('region', { name: tab === 'Plugins' ? 'Installierte Plugins' : 'Installierte Mods' })
  return { user, region, queryClient }
}

/** Task 14: the "Entdecken" view of the tab. */
export async function openDiscover(serverId = 1, username = 'admin', tab: 'Plugins' | 'Mods' = 'Plugins') {
  const { user, queryClient } = await openPlugins(serverId, username, tab)
  await user.click(screen.getByRole('tab', { name: 'Entdecken' }))
  const region = await screen.findByRole('region', { name: 'Entdecken' })
  return { user, region, queryClient }
}
