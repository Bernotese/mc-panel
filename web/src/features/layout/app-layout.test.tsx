import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { setViewport } from '@/test/media'
import { renderWithProviders } from '@/test/render'
import { AppLayout } from './app-layout'

describe('AppLayout', () => {
  it('shows the full navigation to admins on desktop', async () => {
    await renderWithProviders(<AppLayout />, { me: loginAs('admin') })
    const nav = screen.getByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getAllByRole('link').map((l) => l.textContent)).toEqual([
      'Übersicht',
      'Benutzer',
      'Security Center',
      'Audit-Log',
      'Einstellungen',
      'Profil',
    ])
    expect(screen.getByRole('link', { name: 'Zum Inhalt springen' })).toHaveAttribute('href', '#main')
  })

  it('hides admin areas from regular users', async () => {
    await renderWithProviders(<AppLayout />, { me: loginAs('alice') })
    const nav = screen.getByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getAllByRole('link').map((l) => l.textContent)).toEqual(['Übersicht', 'Profil'])
  })

  it('marks the current page', async () => {
    await renderWithProviders(<AppLayout />, { path: '/profile', me: loginAs('alice') })
    expect(screen.getByRole('link', { name: 'Profil' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Übersicht' })).not.toHaveAttribute('aria-current')
  })

  it('uses a sheet instead of the sidebar on phones', async () => {
    setViewport('mobile')
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<AppLayout />, { me: loginAs('admin') })
    expect(screen.queryByRole('navigation', { name: 'Hauptnavigation' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Menü öffnen' }))
    const sheet = await screen.findByRole('dialog', { name: 'mc-panel' })
    await user.click(within(sheet).getByRole('link', { name: 'Profil' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('signs out', async () => {
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<AppLayout />, { me: loginAs('admin') })
    await user.click(screen.getByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(db.currentSessionId).toBeNull()
  })
})
