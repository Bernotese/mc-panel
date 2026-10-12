import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { PluginSearchHit, PluginSource } from '@/api/types'
import { server } from '@/mocks/node'
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

describe('Discover', () => {
  it('shows popular projects without a search term and switches the source', async () => {
    const calls = searchMock((source) => [hit(source === 'hangar' ? 'ViaBackwards' : 'LuckPerms', { source })])
    const { user, region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'LuckPerms' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeInTheDocument()
    const sources = within(region).getByRole('group', { name: 'Quelle' })
    expect(within(sources).getByRole('button', { name: 'Modrinth' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(within(sources).getByRole('button', { name: 'Hangar' }))
    expect(await within(region).findByRole('heading', { name: 'ViaBackwards' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Beliebt auf Hangar' })).toBeInTheDocument()
    expect(calls.map((c) => [c.source, c.q, c.offset])).toEqual([
      ['modrinth', null, null],
      ['hangar', null, null],
    ])
  })

  it('searches after a typing pause instead of on every key', async () => {
    const calls = searchMock(() => [hit('ViaVersion'), hit('ViaBackwards'), hit('LuckPerms')])
    const { user, region } = await openDiscover()
    await within(region).findByRole('heading', { name: 'LuckPerms' })
    await user.type(within(region).getByLabelText('Suchen'), 'via')
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
    expect(within(region).queryByRole('group', { name: 'Quelle' })).not.toBeInTheDocument()
    expect(within(region).queryByRole('button', { name: 'Hangar' })).not.toBeInTheDocument()
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
