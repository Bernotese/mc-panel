import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { UsersPage } from './users-page'

describe('UsersPage', () => {
  it('shows a dedicated empty state instead of the loading text', async () => {
    server.use(http.get('*/api/users', () => HttpResponse.json([])))
    await renderWithProviders(<UsersPage />, { me: loginAs('admin') })
    expect(await screen.findByText('Keine Benutzer vorhanden.')).toBeInTheDocument()
    expect(screen.queryByText('Lädt …')).not.toBeInTheDocument()
  })

  it('lists users with role, 2FA state and marks the current user', async () => {
    await renderWithProviders(<UsersPage />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Benutzerliste' })
    const adminRow = within(table).getByRole('row', { name: /admin \(du\)/ })
    expect(within(adminRow).getByText('TOTP')).toBeInTheDocument()
    expect(within(adminRow).getByText('Passkeys: 1')).toBeInTheDocument()
    const aliceRow = within(table).getByRole('row', { name: /alice/ })
    expect(within(aliceRow).getByRole('combobox', { name: 'Rolle von alice' })).toHaveValue('user')
    expect(within(aliceRow).getByText('keine')).toBeInTheDocument()
  })

  it('creates a user after sudo confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Benutzer anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benutzer anlegen' })
    await user.type(within(dialog).getByLabelText('Benutzername'), 'carol')
    await user.type(within(dialog).getByLabelText('Initiales Passwort'), 'long-enough-password')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByRole('row', { name: /carol/ })).toBeInTheDocument()
  })

  it('validates username and password before sending', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Benutzer anlegen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benutzer anlegen' })
    await user.type(within(dialog).getByLabelText('Benutzername'), 'a b')
    await user.type(within(dialog).getByLabelText('Initiales Passwort'), 'short')
    expect(within(dialog).getByText('Der Benutzername entspricht nicht den Regeln.')).toBeInTheDocument()
    expect(within(dialog).getByText('Das Passwort muss 12 bis 256 Zeichen lang sein.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Anlegen' })).toBeDisabled()
  })

  it('changes a role', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    await user.selectOptions(await screen.findByRole('combobox', { name: 'Rolle von alice' }), 'admin')
    expect(await screen.findByText('Rolle von alice geändert: Administrator')).toBeInTheDocument()
    expect(db.users.find((u) => u.username === 'alice')?.role).toBe('admin')
  })

  it('deletes a user after confirmation and never offers self-deletion', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByRole('button', { name: 'admin löschen' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'bob löschen' }))
    const confirm = screen.getByRole('dialog', { name: 'Benutzer bob löschen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(screen.queryByRole('row', { name: /bob/ })).not.toBeInTheDocument())
  })

  it('resets the 2FA of a user', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UsersPage />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: '2FA von bob zurücksetzen' }))
    const confirm = screen.getByRole('dialog', { name: '2FA von bob zurücksetzen?' })
    await user.click(within(confirm).getByRole('button', { name: '2FA zurücksetzen' }))
    expect(await screen.findByText('2FA von bob zurückgesetzt.')).toBeInTheDocument()
    expect(db.users.find((u) => u.username === 'bob')?.totpEnabled).toBe(false)
  })
})
