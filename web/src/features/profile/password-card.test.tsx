import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { getCsrfToken } from '@/api/csrf'
import { currentSession, db, loginAs, MOCK_PASSWORD } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasswordCard } from './password-card'

async function fill(current: string, next: string, confirm: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Aktuelles Passwort'), current)
  await user.type(screen.getByLabelText('Neues Passwort'), next)
  await user.type(screen.getByLabelText('Neues Passwort wiederholen'), confirm)
  return user
}

describe('PasswordCard', () => {
  it('checks length and confirmation locally', async () => {
    await renderWithProviders(<PasswordCard />, { me: loginAs('admin') })
    await fill(MOCK_PASSWORD, 'short', 'other')
    expect(screen.getByText('Das neue Passwort muss 12 bis 256 Zeichen lang sein.')).toBeInTheDocument()
    expect(screen.getByText('Die Passwörter stimmen nicht überein.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Passwort ändern' })).toBeDisabled()
  })

  it('explains a wrong current password', async () => {
    await renderWithProviders(<PasswordCard />, { me: loginAs('admin') })
    const user = await fill('not-the-password', 'brand-new-password', 'brand-new-password')
    await user.click(screen.getByRole('button', { name: 'Passwort ändern' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Das aktuelle Passwort ist falsch.')
  })

  it('changes the password, signs out other sessions and refreshes the CSRF token', async () => {
    await renderWithProviders(<PasswordCard />, { me: loginAs('admin') })
    const user = await fill(MOCK_PASSWORD, 'brand-new-password', 'brand-new-password')
    await user.click(screen.getByRole('button', { name: 'Passwort ändern' }))
    expect(await screen.findByText('Passwort geändert. Andere Sitzungen wurden abgemeldet.')).toBeInTheDocument()
    expect(db.sessions.filter((s) => s.userId === 1)).toHaveLength(1)
    expect(getCsrfToken()).toBe(currentSession()?.csrf)
    expect(screen.getByLabelText('Aktuelles Passwort')).toHaveValue('')
  })
})
