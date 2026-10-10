import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_TOTP_CODE, MOCK_TOTP_SECRET } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { TotpSetupCard } from './totp-setup-card'

vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn(async () => 'data:image/png;base64,QR') } }))

describe('TotpSetupCard', () => {
  it('shows QR code and secret, rejects wrong codes and reports the recovery codes', async () => {
    const user = userEvent.setup()
    const onEnabled = vi.fn()
    await renderWithProviders(<TotpSetupCard onEnabled={onEnabled} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    await user.click(screen.getByRole('button', { name: 'Einrichtung starten' }))
    const qr = await screen.findByRole('img', { name: 'QR-Code für die Authenticator-App' })
    expect(qr).toHaveAttribute('src', 'data:image/png;base64,QR')
    expect(screen.getByTestId('totp-secret')).toHaveTextContent(MOCK_TOTP_SECRET)

    const input = screen.getByLabelText('Code aus der Authenticator-App')
    await user.type(input, '000000')
    await user.click(screen.getByRole('button', { name: 'Aktivieren' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Der Code ist ungültig.')

    await user.clear(input)
    await user.type(input, MOCK_TOTP_CODE)
    await user.click(screen.getByRole('button', { name: 'Aktivieren' }))
    await vi.waitFor(() => expect(onEnabled).toHaveBeenCalledTimes(1))
    expect(onEnabled.mock.calls[0][0]).toHaveLength(10)
  })

  it('shows the 409 conflict when TOTP is already enabled', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<TotpSetupCard onEnabled={() => undefined} />, { me: loginAs('admin', { stage: 'full', sudo: true }) })
    await user.click(screen.getByRole('button', { name: 'Einrichtung starten' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Konflikt mit dem aktuellen Zustand (TOTP already enabled)')
  })
})
