import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { DashboardPage } from './dashboard-page'

describe('DashboardPage', () => {
  it('shows a restricted user only permitted servers and power buttons only with server.power', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    expect(within(survival).getByRole('button', { name: 'Starten' })).toBeInTheDocument()
    const creative = screen.getByRole('article', { name: 'creative' })
    expect(within(creative).queryByRole('button', { name: 'Starten' })).not.toBeInTheDocument()
    expect(within(creative).getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Server anlegen' })).not.toBeInTheDocument()
  })

  it('sends a power action and confirms it with a toast', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const survival = await screen.findByRole('article', { name: 'survival' })
    await user.click(within(survival).getByRole('button', { name: 'Neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
  })

  it('lets an admin create a server after confirming sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Server anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Server anlegen' })
    await user.type(within(dialog).getByLabelText('Name'), 'skyblock')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))

    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))

    expect(await screen.findByRole('article', { name: 'skyblock' })).toBeInTheDocument()
  })

  it('validates the server name before sending', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Server anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Server anlegen' })
    await user.type(within(dialog).getByLabelText('Name'), 'Bad Name')
    expect(within(dialog).getByText('Der Name entspricht nicht den Regeln.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Anlegen' })).toBeDisabled()
  })

  it('deletes a server after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin', { sudo: true }) })
    const lobby = await screen.findByRole('article', { name: 'lobby' })
    await user.click(within(lobby).getByRole('button', { name: 'lobby löschen' }))
    const confirm = screen.getByRole('dialog', { name: 'Server „lobby“ löschen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Löschen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: 'lobby' })).not.toBeInTheDocument()
  })
})
