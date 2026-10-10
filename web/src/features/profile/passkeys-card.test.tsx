import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasskeysCard } from './passkeys-card'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
  vi.mocked(startRegistration).mockResolvedValue({
    id: 'c',
    rawId: 'c',
    type: 'public-key',
    response: {},
    clientExtensionResults: {},
  } as unknown as Awaited<ReturnType<typeof startRegistration>>)
})

describe('PasskeysCard', () => {
  it('lists, adds and removes passkeys', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<PasskeysCard />, { me: loginAs('admin', { sudo: true }) })
    const table = await screen.findByRole('table', { name: 'Registrierte Passkeys' })
    expect(within(table).getByText('MacBook Touch ID')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Passkey hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Passkey hinzufügen' })
    const name = within(dialog).getByLabelText('Name des Passkeys')
    await user.clear(name)
    await user.type(name, 'YubiKey')
    await user.click(within(dialog).getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByText('Passkey YubiKey hinzugefügt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /YubiKey/ })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'MacBook Touch ID entfernen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Passkey MacBook Touch ID entfernen?' })).getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(screen.queryByRole('row', { name: /MacBook Touch ID/ })).not.toBeInTheDocument())
  })

  it('hides "add" when the browser lacks WebAuthn', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderWithProviders(<PasskeysCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Dieser Browser unterstützt keine Passkeys.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Passkey hinzufügen' })).not.toBeInTheDocument()
  })
})
