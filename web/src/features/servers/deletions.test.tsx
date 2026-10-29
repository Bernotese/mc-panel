import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ServerDeletion } from '@/api/types'
import { DashboardPage } from '@/features/dashboard/dashboard-page'
import { SettingsPage } from '@/features/settings/settings-page'
import { db, loginAs, MOCK_TOTP_CODE, seedDeletionScenario } from '@/mocks/db'
import { server as mockApi } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { DELETION_FAST_POLL_MS } from './deletions-model'
import { renderApp } from './test-utils'

function deletion(over: Partial<ServerDeletion>): ServerDeletion {
  const now = Date.now()
  return {
    server_id: 50,
    name: 'gone',
    state: 'pending',
    container: 'pending',
    container_error: null,
    backups: 'none',
    backups_error: null,
    requested_at: now,
    updated_at: now,
    completed_at: null,
    ...over,
  }
}

async function deleteSurvival(answer: ServerDeletion) {
  db.servers = db.servers.filter((s) => !s.dev) // survival's dev would block the delete (409 dev_exists)
  mockApi.use(http.delete('*/api/servers/:id', () => HttpResponse.json(answer, { status: 202 })))
  const user = userEvent.setup()
  const { router } = await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
  await user.type(await screen.findByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
  await user.click(screen.getByRole('button', { name: 'Server löschen' }))
  return { user, router }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('deleting a server (#148)', () => {
  it('says when the backups could not be deleted, with the error and a way to the details', async () => {
    const { router } = await deleteSurvival(
      deletion({ name: 'survival', state: 'partial', container: 'removed', backups: 'failed', backups_error: 'restic: repository is already locked' }),
    )
    expect(await screen.findByText('„survival“ ist gelöscht, aber die Backups ließen sich nicht löschen. Das Panel versucht es weiter.')).toBeInTheDocument()
    expect(screen.getByText('restic: repository is already locked')).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    // A plain click: sonner's pointer handler wants setPointerCapture, which jsdom lacks.
    fireEvent.click(screen.getByRole('button', { name: 'Details' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'))
  })

  it('says when the container could not be removed yet', async () => {
    await deleteSurvival(deletion({ name: 'survival', state: 'failed', container_error: 'agent unavailable' }))
    expect(await screen.findByText('„survival“ ist aus dem Panel entfernt, aber der Container ließ sich noch nicht entfernen. Das Panel versucht es weiter.')).toBeInTheDocument()
    expect(screen.getByText('agent unavailable')).toBeInTheDocument()
  })
})

describe('open deletions on the dashboard (#148)', () => {
  it('shows admins the deletions that are not done, with a retry for failed and partial ones', async () => {
    seedDeletionScenario()
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin', { sudo: true }) })
    const section = await screen.findByRole('region', { name: 'Löschungen in Arbeit' })
    const items = within(within(section).getByRole('list', { name: 'Löschungen' })).getAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual([expect.stringContaining('old-lobby'), expect.stringContaining('event-2025')])
    expect(items[0]).toHaveTextContent('Fehlgeschlagen')
    expect(items[0]).toHaveTextContent('Container: docker stop mcpanel-mc-41: context deadline exceeded')
    expect(items[1]).toHaveTextContent('Teilweise')
    expect(items[1]).toHaveTextContent('Container entfernt, Daten nach deleted/ verschoben.')
    expect(items[1]).toHaveTextContent('Backups: restic forget: repository is already locked by PID 4242')
    expect(within(section).queryByText('testwelt')).not.toBeInTheDocument()

    await user.click(within(section).getByRole('button', { name: 'Löschung von old-lobby erneut versuchen' }))
    expect(await screen.findByText('Löschung von „old-lobby“ abgeschlossen.')).toBeInTheDocument()
    await waitFor(() => expect(within(section).queryByText('old-lobby')).not.toBeInTheDocument())
    expect(db.deletions.find((d) => d.name === 'old-lobby')?.state).toBe('done')
  })

  it('asks for the second factor before a retry', async () => {
    seedDeletionScenario()
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Löschung von event-2025 erneut versuchen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByText('Löschung von „event-2025“ abgeschlossen.')).toBeInTheDocument()
  })

  it('follows a pending deletion until the agent confirms it, then hides the section', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    db.deletions.push(deletion({ name: 'gone' }))
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const section = await screen.findByRole('region', { name: 'Löschungen in Arbeit' })
    expect(within(section).getByText('Läuft')).toBeInTheDocument()
    expect(within(section).getByText('Der Container wird entfernt …')).toBeInTheDocument()
    expect(within(section).queryByRole('button', { name: /erneut versuchen/ })).not.toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(DELETION_FAST_POLL_MS + 100))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Löschungen in Arbeit' })).not.toBeInTheDocument())
  })

  it('shows nothing when every deletion is done', async () => {
    db.deletions.push(deletion({ state: 'done', container: 'removed', completed_at: Date.now() }))
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    await screen.findByRole('article', { name: 'survival' })
    expect(screen.queryByRole('region', { name: 'Löschungen in Arbeit' })).not.toBeInTheDocument()
  })

  it('is not asked for by other users', async () => {
    seedDeletionScenario()
    let asked = false
    mockApi.use(
      http.get('*/api/servers/deletions', () => {
        asked = true
        return undefined
      }),
    )
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    await screen.findByRole('article', { name: 'survival' })
    expect(screen.queryByRole('region', { name: 'Löschungen in Arbeit' })).not.toBeInTheDocument()
    expect(asked).toBe(false)
  })
})

describe('deleted servers in the settings (#148)', () => {
  it('lists the deletions of the last 7 days, open ones first', async () => {
    seedDeletionScenario()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    const card = await screen.findByRole('region', { name: 'Gelöschte Server' })
    const items = within(await within(card).findByRole('list', { name: 'Löschungen' })).getAllByRole('listitem')
    expect(items.map((i) => i.textContent)).toEqual([
      expect.stringContaining('old-lobby'),
      expect.stringContaining('event-2025'),
      expect.stringContaining('testwelt'),
    ])
    expect(items[2]).toHaveTextContent('Erledigt')
    expect(items[2]).toHaveTextContent('Backups gelöscht.')
    expect(within(items[2]).queryByRole('button')).not.toBeInTheDocument()
  })

  it('says when there is nothing to show', async () => {
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    const card = await screen.findByRole('region', { name: 'Gelöschte Server' })
    expect(await within(card).findByText('Keine Löschungen in den letzten 7 Tagen.')).toBeInTheDocument()
  })
})
