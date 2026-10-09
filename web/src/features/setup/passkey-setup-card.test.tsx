import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasskeySetupCard } from './passkey-setup-card'

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

describe('PasskeySetupCard', () => {
  it('registers a named passkey', async () => {
    const user = userEvent.setup()
    const onRegistered = vi.fn()
    await renderWithProviders(<PasskeySetupCard onRegistered={onRegistered} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    const name = screen.getByLabelText('Name des Passkeys')
    expect(name).toHaveValue('Mein Passkey')
    await user.clear(name)
    await user.type(name, 'YubiKey 5C')
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    await vi.waitFor(() => expect(onRegistered).toHaveBeenCalledTimes(1))
    expect(onRegistered.mock.calls[0][0]).toMatchObject({ passkey: { name: 'YubiKey 5C' } })
  })

  it('shows a readable message when the user cancels the browser dialog', async () => {
    vi.mocked(startRegistration).mockRejectedValue(Object.assign(new Error('cancelled'), { name: 'NotAllowedError' }))
    const user = userEvent.setup()
    await renderWithProviders(<PasskeySetupCard onRegistered={() => undefined} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Vorgang abgebrochen oder Zeit abgelaufen.')
  })

  it('explains that the browser lacks passkey support', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderWithProviders(<PasskeySetupCard onRegistered={() => undefined} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    expect(screen.getByText('Dieser Browser unterstützt keine Passkeys.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Passkey registrieren' })).not.toBeInTheDocument()
  })
})
