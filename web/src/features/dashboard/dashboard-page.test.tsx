import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { DashboardPage } from './dashboard-page'

const GIB = 1024 ** 3

function mockServer(id: number) {
  const server = db.servers.find((s) => s.id === id)
  if (!server) throw new Error(`mock server ${id} missing`)
  return server
}

describe('DashboardPage', () => {
  it('shows a restricted user only permitted servers and power buttons only with server.power', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    expect(within(survival).getByRole('group', { name: 'Steuerung für survival' })).toBeInTheDocument()
    expect(within(survival).getByRole('button', { name: 'Starten' })).toBeInTheDocument()
    const creative = screen.getByRole('article', { name: 'creative' })
    expect(within(creative).queryByRole('button', { name: 'Starten' })).not.toBeInTheDocument()
    expect(within(creative).getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Server anlegen' })).not.toBeInTheDocument()
  })

  it('shows status, type, version, address and memory on each card', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, memory_bytes: 2 * GIB, memory_limit_bytes: 5 * GIB }
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })

    const card = await screen.findByRole('article', { name: 'survival' })
    expect(within(card).getByText('Läuft')).toBeInTheDocument()
    expect(within(card).getByText('Paper 1.21.11')).toBeInTheDocument()
    expect(within(card).getByText('survival.mc.example.com')).toBeInTheDocument()
    expect(within(card).getByText('RAM: 2 / 5 GiB')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Starten' })).toBeDisabled()
    expect(within(card).getByRole('button', { name: 'Stoppen' })).toBeEnabled()

    const creative = screen.getByRole('article', { name: 'creative' })
    expect(within(creative).getByText('Gestoppt')).toBeInTheDocument()
    expect(within(creative).getByText('Vanilla 26.3')).toBeInTheDocument()
    expect(within(creative).queryByText(/^RAM:/)).not.toBeInTheDocument()

    const lobby = screen.getByRole('article', { name: 'lobby' })
    expect(within(lobby).getByText('Nicht eingerichtet')).toBeInTheDocument()
    expect(within(lobby).getByText('Noch ohne Minecraft-Instanz – kann nur gelöscht werden.')).toBeInTheDocument()
    expect(within(lobby).queryByRole('group')).not.toBeInTheDocument()
  })

  it('marks servers that need a restart', async () => {
    const survival = mockServer(1)
    survival.revision += 1
    survival.status = { ...survival.status, restart_required: true }
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const card = await screen.findByRole('article', { name: 'survival' })
    expect(within(card).getByText('Neustart nötig')).toBeInTheDocument()
    expect(within(screen.getByRole('article', { name: 'creative' })).queryByText('Neustart nötig')).not.toBeInTheDocument()
  })

  it('copies the address to the clipboard', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Adresse survival.mc.example.com kopieren' }))
    expect(await navigator.clipboard.readText()).toBe('survival.mc.example.com')
    expect(await screen.findByText('Adresse kopiert.')).toBeInTheDocument()
  })

  it('sends a power action and confirms it with a toast', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
  })

  it('asks for confirmation before stopping a server', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Stoppen' }))
    const confirm = screen.getByRole('dialog', { name: 'Server „survival“ stoppen?' })
    expect(screen.queryByText('Stopp für „survival“ angefordert.')).not.toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: 'Stoppen' }))
    expect(await screen.findByText('Stopp für „survival“ angefordert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Server „survival“ stoppen?' })).not.toBeInTheDocument())
  })

  it('blocks server creation for admins until a base domain is set', async () => {
    db.serverSettings.base_domain = null
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    expect(await screen.findByText('Keine Basisdomain festgelegt')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Server anlegen' })).toBeDisabled()
  })

  it('links admins to the create page when a base domain is set', async () => {
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const create = await screen.findByRole('link', { name: 'Server anlegen' })
    expect(create).toHaveAttribute('href', '/servers/new')
    expect(screen.queryByText('Keine Basisdomain festgelegt')).not.toBeInTheDocument()
    await user.click(create)
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/new'))
  })

  it('shows no error toast after the user cancelled the sudo dialog', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const lobby = await screen.findByRole('article', { name: 'lobby' })
    await user.click(within(lobby).getByRole('button', { name: 'lobby löschen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })).getByRole('button', { name: 'Löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })).toBeInTheDocument()
  })

  it('deletes a server after confirmation and sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const lobby = await screen.findByRole('article', { name: 'lobby' })
    await user.click(within(lobby).getByRole('button', { name: 'lobby löschen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })).getByRole('button', { name: 'Löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument())
  })
})
