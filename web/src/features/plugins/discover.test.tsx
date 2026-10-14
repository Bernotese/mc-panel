import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { PluginSearchHit, PluginSource } from '@/api/types'
import { server } from '@/mocks/node'
import { SEARCH_DEBOUNCE_MS, searchesKey } from './api'
import { hit, openDiscover } from './test-utils'

/** Answers searches from a fixed list (20 per page, substring filter like the mock) and records what was asked. */
function searchMock(all: (source: PluginSource) => PluginSearchHit[]) {
  const calls: { source: string | null; q: string | null; offset: string | null }[] = []
  server.use(
    http.get('*/api/servers/:id/plugins/search', ({ request }) => {
      const url = new URL(request.url)
      const source = url.searchParams.get('source') as PluginSource
      const q = url.searchParams.get('q')
      const offset = Number(url.searchParams.get('offset') ?? '0')
      calls.push({ source, q, offset: url.searchParams.get('offset') })
      const list = all(source).filter((h) => !q || h.title.toLowerCase().includes(q.toLowerCase()))
      return HttpResponse.json({ hits: list.slice(offset, offset + 20), total: list.length, offset })
    }),
  )
  return calls
}

afterEach(() => {
  vi.useRealTimers()
})

describe('Discover', () => {
  it('shows popular projects without a search term and switches the source', async () => {
    const calls = searchMock((source) => [hit(source === 'hangar' ? 'ViaBackwards' : 'LuckPerms', { source })])
    const { user, region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'LuckPerms' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeInTheDocument()
    const sources = within(region).getByRole('tablist', { name: 'Quelle' })
    expect(within(sources).getByRole('tab', { name: 'Modrinth' })).toHaveAttribute('aria-selected', 'true')
    await user.click(within(sources).getByRole('tab', { name: 'Hangar' }))
    expect(await within(region).findByRole('heading', { name: 'ViaBackwards' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Beliebt auf Hangar' })).toBeInTheDocument()
    expect(calls.map((c) => [c.source, c.q, c.offset])).toEqual([
      ['modrinth', null, null],
      ['hangar', null, null],
    ])
  })

  it('shows the results as a tile grid with the install action in each tile', async () => {
    const { region } = await openDiscover()
    await within(region).findAllByRole('listitem')
    const list = within(region)
      .getAllByRole('list')
      .find((l) => l.getAttribute('data-layout') === 'grid')
    expect(list).toBeDefined()
    const tiles = within(list as HTMLElement).getAllByRole('listitem')
    expect(tiles.length).toBeGreaterThan(0)
    expect(within(tiles[0]).getByRole('heading', { level: 4 })).toBeInTheDocument()
  })

  it('searches after a typing pause instead of on every key', async () => {
    // Fake timers jump over the typing pause instead of waiting 400 ms of real time.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const calls = searchMock(() => [hit('ViaVersion'), hit('ViaBackwards'), hit('LuckPerms')])
    const { user, region } = await openDiscover()
    await within(region).findByRole('heading', { name: 'LuckPerms' })
    await user.type(within(region).getByLabelText('Suchen'), 'via')
    await act(() => vi.advanceTimersByTimeAsync(SEARCH_DEBOUNCE_MS))
    expect(await within(region).findByRole('heading', { name: 'Treffer auf Modrinth' })).toBeInTheDocument()
    await waitFor(() => expect(within(region).queryByRole('heading', { name: 'LuckPerms' })).not.toBeInTheDocument())
    expect(within(region).getByRole('heading', { name: 'ViaBackwards' })).toBeInTheDocument()
    expect(calls.map((c) => c.q)).toEqual([null, 'via'])
  })

  it('loads more results with the offset', async () => {
    const calls = searchMock(() => Array.from({ length: 25 }, (_, i) => hit(`Plugin ${i + 1}`)))
    const { user, region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'Plugin 20' })).toBeInTheDocument()
    expect(within(region).queryByRole('heading', { name: 'Plugin 21' })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Mehr laden' }))
    expect(await within(region).findByRole('heading', { name: 'Plugin 25' })).toBeInTheDocument()
    expect(within(region).queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument()
    expect(calls.map((c) => c.offset)).toEqual([null, '20'])
  })

  it('marks installed projects and opens the install dialog for the others', async () => {
    searchMock(() => [hit('LuckPerms', { installed: true }), hit('Chunky')])
    server.use(http.get('*/api/servers/:id/plugins/versions', () => HttpResponse.json({ versions: [] })))
    const { user, region } = await openDiscover()
    await user.click(await within(region).findByRole('switch', { name: 'Installierte ausblenden' }))
    const luckperms = (await within(region).findByRole('heading', { name: 'LuckPerms' })).closest('li') as HTMLElement
    expect(within(luckperms).getByText('Installiert')).toBeInTheDocument()
    expect(within(luckperms).queryByRole('button', { name: 'LuckPerms installieren' })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Chunky installieren' }))
    const dialog = await screen.findByRole('dialog', { name: 'Chunky installieren' })
    expect(await within(dialog).findByText('Für diese Spielversion gibt es keine passende Version.')).toBeInTheDocument()
  })

  it('shows descriptions as plain text and links project pages safely', async () => {
    searchMock(() => [hit('Chunky', { description: '<b>fett</b> **kein Markdown**' })])
    const { region } = await openDiscover()
    expect(await within(region).findByText('<b>fett</b> **kein Markdown**')).toBeInTheDocument()
    expect(region.querySelector('b')).toBeNull()
    const link = within(region).getByRole('link', { name: 'Projektseite von Chunky (neues Fenster)' })
    expect(link).toHaveAttribute('href', 'https://modrinth.com/plugin/chunky')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('offers only Modrinth for mods', async () => {
    searchMock(() => [hit('Lithium', { page_url: 'https://modrinth.com/mod/lithium' })])
    const { region } = await openDiscover(4, 'admin', 'Mods')
    expect(await within(region).findByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeInTheDocument()
    expect(within(region).queryByRole('tablist', { name: 'Quelle' })).not.toBeInTheDocument()
    expect(within(region).queryByRole('tab', { name: 'Hangar' })).not.toBeInTheDocument()
    expect(within(region).getByRole('link', { name: 'Modrinth öffnen (neues Fenster)' })).toHaveAttribute('href', 'https://modrinth.com/mods')
  })

  it('offers a retry when the source is unreachable', async () => {
    let fail = true
    server.use(
      http.get('*/api/servers/:id/plugins/search', () =>
        fail
          ? HttpResponse.json({ error: 'plugin_source_unavailable', message: 'modrinth: 503' }, { status: 502 })
          : HttpResponse.json({ hits: [hit('Chunky')], total: 1, offset: 0 }),
      ),
    )
    const { user, region } = await openDiscover()
    const alert = await within(region).findByRole('alert')
    expect(alert).toHaveTextContent('Die Suche ist gerade nicht möglich.')
    fail = false
    await user.click(within(alert).getByRole('button', { name: 'Erneut versuchen' }))
    expect(await within(region).findByRole('heading', { name: 'Chunky' })).toBeInTheDocument()
  })
})

describe('Discover – origin', () => {
  it('names the source on every tile and links the source above the results', async () => {
    searchMock((source) => [hit(source === 'hangar' ? 'ViaBackwards' : 'LuckPerms', { source })])
    const { user, region } = await openDiscover()
    const tile = (await within(region).findByRole('heading', { name: 'LuckPerms' })).closest('li') as HTMLElement
    expect(within(tile).getByText('Modrinth')).toBeInTheDocument()
    expect(within(region).getByText('Ergebnisse von')).toBeInTheDocument()
    const link = within(region).getByRole('link', { name: 'Modrinth öffnen (neues Fenster)' })
    expect(link).toHaveAttribute('href', 'https://modrinth.com/plugins')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    await user.click(within(region).getByRole('tab', { name: 'Hangar' }))
    const hangarTile = (await within(region).findByRole('heading', { name: 'ViaBackwards' })).closest('li') as HTMLElement
    expect(within(hangarTile).getByText('Hangar')).toBeInTheDocument()
    expect(within(region).getByRole('link', { name: 'Hangar öffnen (neues Fenster)' })).toHaveAttribute('href', 'https://hangar.papermc.io/')
  })
})

describe('Discover – hide installed', () => {
  it('hides installed projects by default and remembers the choice', async () => {
    searchMock(() => [hit('LuckPerms', { installed: true }), hit('Chunky')])
    const { user, region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'Chunky' })).toBeInTheDocument()
    expect(within(region).queryByRole('heading', { name: 'LuckPerms' })).not.toBeInTheDocument()
    expect(within(region).getByText('Ausgeblendet, weil installiert: 1')).toBeInTheDocument()
    const toggle = within(region).getByRole('switch', { name: 'Installierte ausblenden' })
    expect(toggle).toBeChecked()
    await user.click(toggle)
    expect(within(region).getByRole('heading', { name: 'LuckPerms' })).toBeInTheDocument()
    expect(localStorage.getItem('mcpanel.plugins.hideInstalled')).toBe('off')
  })

  it('starts with installed projects shown when that was chosen before', async () => {
    localStorage.setItem('mcpanel.plugins.hideInstalled', 'off')
    searchMock(() => [hit('LuckPerms', { installed: true }), hit('Chunky')])
    const { region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'LuckPerms' })).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'Installierte ausblenden' })).not.toBeChecked()
  })

  it('loads further pages on its own when hiding leaves a page almost empty', async () => {
    // Page 1: 15 installed, 5 new; page 2: 20 new – the second page comes without "Mehr laden".
    const all = Array.from({ length: 40 }, (_, i) => hit(`Plugin ${i + 1}`, { installed: i < 15 }))
    const calls = searchMock(() => all)
    const { region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'Plugin 21' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Plugin 16' })).toBeInTheDocument()
    expect(within(region).queryByRole('heading', { name: 'Plugin 1' })).not.toBeInTheDocument()
    expect(calls.map((c) => c.offset)).toEqual([null, '20'])
    expect(within(region).queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument()
  })

  it('stops loading on its own after a few pages and says when everything shown is installed', async () => {
    const all = Array.from({ length: 200 }, (_, i) => hit(`Plugin ${i + 1}`, { installed: true }))
    const calls = searchMock(() => all)
    const { user, region } = await openDiscover()
    expect(await within(region).findByText('Alle bisher geladenen Treffer sind schon installiert.')).toBeInTheDocument()
    await waitFor(() => expect(calls).toHaveLength(4))
    expect(within(region).getByText('Ausgeblendet, weil installiert: 80')).toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Mehr laden' }))
    await waitFor(() => expect(calls).toHaveLength(8))
    expect(calls.map((c) => c.offset)).toEqual([null, '20', '40', '60', '80', '100', '120', '140'])
  })
})

describe('Discover – stale results', () => {
  it('says when refreshing the results failed instead of showing the old ones silently', async () => {
    let fail = false
    server.use(
      http.get('*/api/servers/:id/plugins/search', () =>
        fail
          ? HttpResponse.json({ error: 'plugin_source_unavailable', message: 'modrinth: 503' }, { status: 502 })
          : HttpResponse.json({ hits: [hit('Chunky')], total: 1, offset: 0 }),
      ),
    )
    const { user, region, queryClient } = await openDiscover()
    await within(region).findByRole('heading', { name: 'Chunky' })
    fail = true
    await act(() => queryClient.invalidateQueries({ queryKey: searchesKey(1) }))
    const alert = await within(region).findByRole('alert')
    expect(alert).toHaveTextContent('Die Suche ist gerade nicht möglich.')
    expect(within(region).getByRole('heading', { name: 'Chunky' })).toBeInTheDocument()
    fail = false
    await user.click(within(alert).getByRole('button', { name: 'Erneut versuchen' }))
    await waitFor(() => expect(within(region).queryByRole('alert')).not.toBeInTheDocument())
  })
})
