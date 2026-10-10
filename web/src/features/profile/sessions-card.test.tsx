import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { AdminSessionsCard } from './admin-sessions-card'
import { SessionsCard } from './sessions-card'

describe('SessionsCard', () => {
  it('lists own sessions and flags the current one', async () => {
    await renderWithProviders(<SessionsCard />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Deine Sitzungen' })
    expect(within(table).getAllByRole('row')).toHaveLength(4)
    expect(within(table).getByText('Diese Sitzung')).toBeInTheDocument()
    expect(within(table).getByText('Safari auf iOS')).toBeInTheDocument()
  })

  it('revokes a single other session', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SessionsCard />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Sitzung Firefox auf Windows abmelden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Deine Sitzungen' })).getAllByRole('row')).toHaveLength(3))
  })

  it('revokes all other sessions after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SessionsCard />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Alle anderen abmelden' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Alle anderen Sitzungen abmelden?' })).getByRole('button', { name: 'Alle anderen abmelden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Deine Sitzungen' })).getAllByRole('row')).toHaveLength(2))
  })
})

describe('AdminSessionsCard', () => {
  it('shows sessions of all users and revokes one after sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AdminSessionsCard />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Sitzungen aller Benutzer' })
    const aliceRow = within(table).getByRole('row', { name: /alice/ })
    await user.click(within(aliceRow).getByRole('button', { name: 'Sitzung Chrome auf Linux abmelden' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Sitzungen aller Benutzer' })).queryByRole('row', { name: /alice/ })).not.toBeInTheDocument())
  })
})
