import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { renderApp } from '@/features/servers/test-utils'
import { db, loginAs } from '@/mocks/db'
import { setViewport } from '@/test/media'

function railLabels(nav: HTMLElement) {
  return within(nav)
    .getAllByRole('link')
    .map((l) => l.getAttribute('aria-label'))
}

describe('AppLayout', () => {
  it('shows the full rail to admins', async () => {
    await renderApp('/', loginAs('admin'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(railLabels(nav)).toEqual(['Startseite', 'Server', 'Benutzer', 'Security Center', 'Audit-Log', 'Einstellungen', 'Profil'])
    expect(screen.getByRole('link', { name: 'Zum Inhalt springen' })).toHaveAttribute('href', '#main')
  })

  it('hides admin areas from regular users', async () => {
    await renderApp('/', loginAs('alice'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(railLabels(nav)).toEqual(['Startseite', 'Server', 'Profil'])
  })

  it('marks the current page', async () => {
    await renderApp('/profile', loginAs('alice'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getByRole('link', { name: 'Profil' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Server' })).not.toHaveAttribute('aria-current')
  })

  it('marks the servers entry on the home page', async () => {
    await renderApp('/', loginAs('alice'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getByRole('link', { name: 'Server' })).toHaveAttribute('aria-current', 'page')
  })

  it('marks only the open section of the active server as current', async () => {
    // Players, not the console: jsdom has no EventSource.
    await renderApp('/servers/1/players', loginAs('admin'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(await within(tree).findByRole('link', { name: 'Spieler' })).toHaveAttribute('aria-current', 'page')
    expect(within(tree).getByRole('link', { name: 'survival' })).not.toHaveAttribute('aria-current')
    expect(within(tree).getByRole('link', { name: 'Übersicht' })).not.toHaveAttribute('aria-current')
  })

  it('lists permitted servers in the tree and expands the active one', async () => {
    await renderApp('/servers/1', loginAs('alice'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(await within(tree).findByRole('link', { name: 'survival' })).toHaveAttribute('aria-current', 'page')
    expect(within(tree).getByRole('link', { name: 'creative' })).toBeInTheDocument()
    expect(within(tree).queryByRole('link', { name: 'lobby' })).not.toBeInTheDocument()
    expect(within(tree).getByRole('list', { name: 'Bereiche von survival' })).toBeInTheDocument()
    expect(within(tree).queryByRole('list', { name: 'Bereiche von creative' })).not.toBeInTheDocument()
    expect(within(tree).queryByRole('link', { name: 'Neuer Server' })).not.toBeInTheDocument()
  })

  it('keeps the section when switching servers if it is allowed, else opens the overview', async () => {
    const user = userEvent.setup()
    const { router } = await renderApp('/servers/1/files', loginAs('admin'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(await within(tree).findByRole('link', { name: 'creative' })).toHaveAttribute('href', '/servers/2/files')
    expect(within(tree).getByRole('link', { name: 'Dateien' })).toHaveAttribute('aria-current', 'page')
    await user.click(within(tree).getByRole('link', { name: 'creative' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/2/files'))
    await user.click(within(tree).getByRole('link', { name: 'Backups' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/2/backups'))
  })

  it('opens the overview of a server that lacks the current section', async () => {
    const { router } = await renderApp('/servers/1/plugins', loginAs('admin'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    // creative is vanilla: no plugins section; modded calls it "Mods" but it is the same section.
    expect(await within(tree).findByRole('link', { name: 'creative' })).toHaveAttribute('href', '/servers/2')
    expect(within(tree).getByRole('link', { name: 'modded' })).toHaveAttribute('href', '/servers/4/plugins')
    expect(router.state.location.pathname).toBe('/servers/1/plugins')
  })

  it('collapses the tree and remembers it', async () => {
    const user = userEvent.setup()
    await renderApp('/', loginAs('admin'))
    await user.click(await screen.findByRole('button', { name: 'Serverliste einklappen' }))
    expect(screen.queryByRole('navigation', { name: 'Server' })).not.toBeInTheDocument()
    expect(localStorage.getItem('mcpanel.tree')).toBe('closed')
    await user.click(screen.getByRole('button', { name: 'Serverliste ausklappen' }))
    expect(screen.getByRole('navigation', { name: 'Server' })).toBeInTheDocument()
    expect(localStorage.getItem('mcpanel.tree')).toBe('open')
  })

  it('starts with a collapsed tree when that was stored', async () => {
    localStorage.setItem('mcpanel.tree', 'closed')
    await renderApp('/', loginAs('admin'))
    expect(await screen.findByRole('button', { name: 'Serverliste ausklappen' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Server' })).not.toBeInTheDocument()
  })

  it('keeps the tree usable when storage throws', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    try {
      const user = userEvent.setup()
      await renderApp('/', loginAs('admin'))
      expect(await screen.findByRole('navigation', { name: 'Server' })).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Serverliste einklappen' }))
      expect(screen.queryByRole('navigation', { name: 'Server' })).not.toBeInTheDocument()
    } finally {
      vi.restoreAllMocks()
    }
  })

  it('expands the rail with labels and remembers it', async () => {
    const user = userEvent.setup()
    await renderApp('/', loginAs('admin'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).queryByText('Audit-Log')).not.toBeInTheDocument()
    const expand = screen.getByRole('button', { name: 'Menü ausklappen' })
    expect(expand).toHaveAttribute('aria-expanded', 'false')
    expect(expand).toHaveAttribute('aria-controls', nav.id)
    await user.click(expand)
    expect(within(nav).getByText('Audit-Log')).toBeInTheDocument()
    expect(localStorage.getItem('mcpanel.rail')).toBe('expanded')
    // The names stay the same, so everything that selects links by name keeps working.
    expect(railLabels(nav)).toEqual(['Startseite', 'Server', 'Benutzer', 'Security Center', 'Audit-Log', 'Einstellungen', 'Profil'])
    const collapse = screen.getByRole('button', { name: 'Menü einklappen' })
    expect(collapse).toHaveAttribute('aria-expanded', 'true')
    await user.click(collapse)
    expect(within(nav).queryByText('Audit-Log')).not.toBeInTheDocument()
    expect(localStorage.getItem('mcpanel.rail')).toBe('collapsed')
  })

  it('starts with an expanded rail when that was stored', async () => {
    localStorage.setItem('mcpanel.rail', 'expanded')
    await renderApp('/', loginAs('admin'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getByText('Profil')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Menü einklappen' })).toBeInTheDocument()
  })

  it('keeps the narrow rail on tablets', async () => {
    localStorage.setItem('mcpanel.rail', 'expanded')
    setViewport('tablet')
    await renderApp('/', loginAs('admin'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).queryByText('Profil')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Menü (aus|ein)klappen/ })).not.toBeInTheDocument()
  })

  it('offers admins a new-server link in the tree', async () => {
    await renderApp('/', loginAs('admin'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(within(tree).getByRole('link', { name: 'Neuer Server' })).toHaveAttribute('href', '/servers/new')
  })

  it('shows a breadcrumb for server sections', async () => {
    await renderApp('/servers/1/players', loginAs('admin'))
    const crumb = await screen.findByRole('navigation', { name: 'Brotkrume' })
    await waitFor(() => expect(crumb).toHaveTextContent('survival/Spieler'))
    expect(within(crumb).getByRole('link', { name: 'survival' })).toHaveAttribute('href', '/servers/1')
  })

  it('shows a breadcrumb for other pages', async () => {
    await renderApp('/users/2/permissions', loginAs('admin'))
    const crumb = await screen.findByRole('navigation', { name: 'Brotkrume' })
    expect(crumb).toHaveTextContent('Benutzer')
  })

  it('opens the tree as a flyout on tablets', async () => {
    setViewport('tablet')
    const user = userEvent.setup()
    const { router } = await renderApp('/', loginAs('admin'))
    expect(await screen.findByRole('navigation', { name: 'Hauptnavigation' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Server' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Serverliste öffnen' }))
    const flyout = await screen.findByRole('dialog', { name: 'Server' })
    await user.click(await within(flyout).findByRole('link', { name: 'creative' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/2'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('uses a drawer with rail and tree on phones', async () => {
    setViewport('mobile')
    const user = userEvent.setup()
    const { router } = await renderApp('/', loginAs('admin'))
    await user.click(await screen.findByRole('button', { name: 'Menü öffnen' }))
    expect(screen.getAllByRole('navigation', { name: 'Hauptnavigation' })).toHaveLength(1)
    const sheet = await screen.findByRole('dialog', { name: 'mc-panel' })
    expect(within(sheet).getByRole('navigation', { name: 'Hauptnavigation' })).toBeInTheDocument()
    expect(within(sheet).getByRole('navigation', { name: 'Server' })).toBeInTheDocument()
    await user.click(within(sheet).getByRole('link', { name: 'Profil' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('has no rail outside the drawer on phones', async () => {
    setViewport('mobile')
    await renderApp('/', loginAs('admin'))
    expect(await screen.findByRole('button', { name: 'Menü öffnen' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Hauptnavigation' })).not.toBeInTheDocument()
  })

  it('signs out', async () => {
    const user = userEvent.setup()
    const { router } = await renderApp('/', loginAs('admin'))
    await user.click(await screen.findByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(db.currentSessionId).toBeNull()
  })
})
