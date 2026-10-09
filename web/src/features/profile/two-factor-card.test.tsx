import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { TwoFactorCard } from './two-factor-card'

vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn(async () => 'data:image/png;base64,QR') } }))

describe('TwoFactorCard', () => {
  it('sets up TOTP for a user without 2FA and shows the first recovery codes', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<TwoFactorCard />, { me: loginAs('alice') })
    const card = screen.getByRole('region', { name: 'Authenticator-App (TOTP)' })
    expect(within(card).getByText('Nicht eingerichtet')).toBeInTheDocument()
    await user.click(within(card).getByRole('button', { name: 'Einrichten' }))
    const dialog = screen.getByRole('dialog', { name: 'Authenticator-App einrichten' })
    await user.click(within(dialog).getByRole('button', { name: 'Einrichtung starten' }))
    await user.type(await within(dialog).findByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(dialog).getByRole('button', { name: 'Aktivieren' }))
    expect(await within(dialog).findByRole('list', { name: 'Wiederherstellungscodes' })).toBeInTheDocument()
    await user.click(within(dialog).getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(within(dialog).getByRole('button', { name: 'Fertig' }))
    await waitFor(() => expect(within(card).getByText('Aktiv')).toBeInTheDocument())
  })

  it('disables TOTP after confirmation and sudo', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<TwoFactorCard />, { me: loginAs('admin') })
    const card = screen.getByRole('region', { name: 'Authenticator-App (TOTP)' })
    await user.click(within(card).getByRole('button', { name: 'Deaktivieren' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Authenticator-App deaktivieren?' })).getByRole('button', { name: 'Deaktivieren' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(within(card).getByText('Nicht eingerichtet')).toBeInTheDocument())
  })
})
