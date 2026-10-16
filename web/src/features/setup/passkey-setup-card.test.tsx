import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_PASSWORD } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { PasskeySetupCard } from './passkey-setup-card'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(startRegistration).mockClear()
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
    // the first second factor needs the current password (SEC-15)
    expect(await screen.findByRole('alert')).toHaveTextContent('Bitte gib dein aktuelles Passwort ein.')
    expect(startRegistration).not.toHaveBeenCalled()
    const password = screen.getByLabelText('Aktuelles Passwort')
    await user.type(password, 'not the password')
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Das Passwort ist falsch.')
    await user.clear(password)
    await user.type(password, MOCK_PASSWORD)
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    await vi.waitFor(() => expect(onRegistered).toHaveBeenCalledTimes(1))
    expect(onRegistered.mock.calls[0][0]).toMatchObject({ passkey: { name: 'YubiKey 5C' } })
  })

  it('asks for no password when a second factor already exists', async () => {
    await renderWithProviders(<PasskeySetupCard onRegistered={() => undefined} />, { me: loginAs('admin', { stage: 'full', sudo: true }) })
    expect(screen.getByLabelText('Name des Passkeys')).toBeInTheDocument()
    expect(screen.queryByLabelText('Aktuelles Passwort')).not.toBeInTheDocument()
  })

  it('shows a readable message when the user cancels the browser dialog', async () => {
    vi.mocked(startRegistration).mockRejectedValue(Object.assign(new Error('cancelled'), { name: 'NotAllowedError' }))
    const user = userEvent.setup()
    await renderWithProviders(<PasskeySetupCard onRegistered={() => undefined} />, { me: loginAs('newadmin', { stage: 'setup' }) })
    await user.type(screen.getByLabelText('Aktuelles Passwort'), MOCK_PASSWORD)
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
