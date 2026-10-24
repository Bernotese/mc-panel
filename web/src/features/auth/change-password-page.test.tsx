import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db, findUserByName, loginAs, MOCK_PASSWORD } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { ChangePasswordPage } from './change-password-page'

const NEW_PASSWORD = 'a brand new passphrase'

function mustChange(username: string) {
  findUserByName(username)!.mustChangePassword = true
  return loginAs(username)
}

async function fill(current: string, next: string, confirm: string = next) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Aktuelles Passwort'), current)
  await user.type(screen.getByLabelText('Neues Passwort'), next)
  await user.type(screen.getByLabelText('Neues Passwort wiederholen'), confirm)
  return user
}

describe('ChangePasswordPage', () => {
  it('explains why the change is required and offers the logout', async () => {
    await renderWithProviders(<ChangePasswordPage />, { path: '/change-password', me: mustChange('alice') })
    expect(screen.getByRole('heading', { name: 'Neues Passwort wählen' })).toBeInTheDocument()
    expect(screen.getByText(/Angemeldet als alice\. Ein Administrator hat dein Passwort festgelegt/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Abmelden' })).toBeInTheDocument()
  })

  it('refuses to keep the password the admin set', async () => {
    await renderWithProviders(<ChangePasswordPage />, { path: '/change-password', me: mustChange('alice') })
    await fill(MOCK_PASSWORD, MOCK_PASSWORD)
    expect(screen.getByText('Das neue Passwort muss sich vom aktuellen unterscheiden.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Passwort ändern und weiter' })).toBeDisabled()
  })

  it('shows a validation error of the server', async () => {
    server.use(http.post('*/api/me/password', () => HttpResponse.json({ error: 'validation_failed', message: 'password is too common' }, { status: 400 })))
    await renderWithProviders(<ChangePasswordPage />, { path: '/change-password', me: mustChange('alice') })
    const user = await fill(MOCK_PASSWORD, NEW_PASSWORD)
    await user.click(screen.getByRole('button', { name: 'Passwort ändern und weiter' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Eingaben ungültig (password is too common)')
  })

  it('explains a wrong current password', async () => {
    await renderWithProviders(<ChangePasswordPage />, { path: '/change-password', me: mustChange('alice') })
    const user = await fill('not-the-password', NEW_PASSWORD)
    await user.click(screen.getByRole('button', { name: 'Passwort ändern und weiter' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Das aktuelle Passwort ist falsch.')
  })

  it('continues to the panel after the change', async () => {
    const { router } = await renderWithProviders(<ChangePasswordPage />, { path: '/change-password', me: mustChange('alice') })
    const user = await fill(MOCK_PASSWORD, NEW_PASSWORD)
    await user.click(screen.getByRole('button', { name: 'Passwort ändern und weiter' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(findUserByName('alice')).toMatchObject({ password: NEW_PASSWORD, mustChangePassword: false })
  })

  it('continues to the 2FA setup when it is mandatory', async () => {
    const { router } = await renderWithProviders(<ChangePasswordPage />, { path: '/change-password', me: mustChange('newadmin') })
    const user = await fill(MOCK_PASSWORD, NEW_PASSWORD)
    await user.click(screen.getByRole('button', { name: 'Passwort ändern und weiter' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/setup-2fa'))
    expect(db.sessions.filter((s) => s.userId === 3)).toHaveLength(1)
  })
})
