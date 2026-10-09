import { browserSupportsWebAuthn, startAuthentication } from '@simplewebauthn/browser'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { requestSudo } from './sudo-store'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
})

function open(): Promise<boolean> {
  let answer: Promise<boolean> = Promise.resolve(false)
  act(() => {
    answer = requestSudo()
  })
  return answer
}

describe('SudoDialog', () => {
  it('confirms with a TOTP code, shows wrong codes inline and resolves the request', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const answer = open()
    const dialog = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    const input = within(dialog).getByLabelText('Code aus der Authenticator-App')
    await user.type(input, '000000')
    await user.click(within(dialog).getByRole('button', { name: 'Bestätigen' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Der Code ist ungültig.')
    await user.clear(input)
    await user.type(input, MOCK_TOTP_CODE)
    await user.click(within(dialog).getByRole('button', { name: 'Bestätigen' }))
    await expect(answer).resolves.toBe(true)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('resolves false when cancelled', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const answer = open()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
    await expect(answer).resolves.toBe(false)
  })

  it('offers passkeys only when the browser supports WebAuthn', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    open()
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).queryByRole('tab', { name: 'Passkey' })).not.toBeInTheDocument()
    expect(within(dialog).getByLabelText('Code aus der Authenticator-App')).toBeInTheDocument()
  })

  it('confirms with a passkey', async () => {
    vi.mocked(startAuthentication).mockResolvedValue({
      id: 'c',
      rawId: 'c',
      type: 'public-key',
      response: {},
      clientExtensionResults: {},
    } as unknown as Awaited<ReturnType<typeof startAuthentication>>)
    const user = userEvent.setup()
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const answer = open()
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('tab', { name: 'Passkey' }))
    await user.click(within(dialog).getByRole('button', { name: 'Mit Passkey bestätigen' }))
    await expect(answer).resolves.toBe(true)
  })

  it('shows a single dialog for concurrent sudo requests', async () => {
    await renderWithProviders(<p>page</p>, { me: loginAs('admin') })
    const a = open()
    const b = open()
    expect(a).toBe(b)
    expect(await screen.findAllByRole('dialog')).toHaveLength(1)
  })
})
